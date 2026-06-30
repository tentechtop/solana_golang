package posnode

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/utils"
)

func TestBootstrapCoordinatorFreezesManifestAtThreshold(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 2)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}

	first := testBootstrapRegistration(t, config, 1)
	firstResult, err := coordinator.BootstrapRegisterValidator(context.Background(), first)
	if err != nil {
		t.Fatalf("BootstrapRegisterValidator(first) error = %v", err)
	}
	if firstResult.Ready {
		t.Fatal("first registration Ready = true, want false")
	}

	second := testBootstrapRegistration(t, config, 2)
	secondResult, err := coordinator.BootstrapRegisterValidator(context.Background(), second)
	if err != nil {
		t.Fatalf("BootstrapRegisterValidator(second) error = %v", err)
	}
	if !secondResult.Ready {
		t.Fatal("second registration Ready = false, want true")
	}

	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}
	if !manifest.Ready || manifest.ValidatorCount != 2 || manifest.MinValidators != 2 {
		t.Fatalf("manifest readiness = ready:%v count:%d min:%d", manifest.Ready, manifest.ValidatorCount, manifest.MinValidators)
	}
	if manifest.ChainIdentityHash == "" || manifest.GenesisHash == "" {
		t.Fatal("manifest chain identity is empty")
	}
	if len(manifest.Genesis.InitialValidators) != 2 {
		t.Fatalf("manifest validators = %d, want 2", len(manifest.Genesis.InitialValidators))
	}
	if len(manifest.BootstrapPeers) != 2 {
		t.Fatalf("manifest bootstrap peers = %d, want 2", len(manifest.BootstrapPeers))
	}
	if len(manifest.Genesis.FundedAccounts) == 0 || manifest.Genesis.FundedAccounts[0].Seed != "" {
		t.Fatal("manifest funded accounts must expose address only")
	}
	if manifest.GenesisStartUnixMilli <= time.Now().UnixMilli() {
		t.Fatal("manifest genesis start must be in the future")
	}
}

func TestBootstrapManifestFrozenErrorDetection(t *testing.T) {
	if !isBootstrapManifestFrozenError(errors.New("posnode: bootstrap rpc error -32603 internal error: bootstrap register validator: posnode: bootstrap manifest already frozen")) {
		t.Fatal("frozen manifest error was not detected")
	}
	if isBootstrapManifestFrozenError(errors.New("temporary network timeout")) {
		t.Fatal("temporary error detected as frozen manifest")
	}
}

func TestBootstrapCoordinatorRejectsInvalidSignature(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}

	registration := testBootstrapRegistration(t, config, 1)
	registration.StakeLamports++
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err == nil {
		t.Fatal("BootstrapRegisterValidator() error = nil, want invalid signature rejection")
	}
}

func TestBootstrapCoordinatorAcceptsDiscoveryChainID(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, config, 1)
	registration.ChainID = ""
	signBootstrapStakerAuthorization(t, &registration, "bootstrap-staker-1")
	signBootstrapRegistration(t, &registration, "bootstrap-consensus-1")

	result, err := coordinator.BootstrapRegisterValidator(context.Background(), registration)
	if err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}
	if !result.Ready {
		t.Fatal("Ready = false, want true")
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}
	if manifest.ChainID != config.ChainID {
		t.Fatalf("manifest ChainID = %q, want %q", manifest.ChainID, config.ChainID)
	}
}

func TestBootstrapCoordinatorReloadsDiscoveryRegistration(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, config, 1)
	registration.ChainID = ""
	signBootstrapStakerAuthorization(t, &registration, "bootstrap-staker-1")
	signBootstrapRegistration(t, &registration, "bootstrap-consensus-1")
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}

	reloaded, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator(reload) error = %v", err)
	}
	manifest, err := reloaded.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}
	if !manifest.Ready || manifest.ChainID != config.ChainID {
		t.Fatalf("manifest ready=%v chain=%q, want ready chain %q", manifest.Ready, manifest.ChainID, config.ChainID)
	}
}

func TestBootstrapCoordinatorRejectsExplicitChainMismatch(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, config, 1)
	registration.ChainID = "wrong-chain"
	signBootstrapStakerAuthorization(t, &registration, "bootstrap-staker-1")
	signBootstrapRegistration(t, &registration, "bootstrap-consensus-1")

	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err == nil {
		t.Fatal("BootstrapRegisterValidator() error = nil, want chain mismatch rejection")
	}
}

func TestBuildBootstrapRegistrationOmitsDiscoveredChainID(t *testing.T) {
	config := minimalNodeConfigForValidation()
	config.ChainID = ""
	config.Genesis = genesisConfig{}
	config.BootstrapPeers = []peerConfig{{
		IP:   "127.0.0.1",
		Port: 5101,
		Role: "bootnode",
	}}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}

	registration, err := buildBootstrapRegistration(normalized)
	if err != nil {
		t.Fatalf("buildBootstrapRegistration() error = %v", err)
	}
	if registration.ChainID != "" {
		t.Fatalf("registration ChainID = %q, want discovery empty chain id", registration.ChainID)
	}
	if registration.PeerID == "" || registration.ConsensusPublicKey == "" || registration.Signature == "" {
		t.Fatal("registration identity fields are empty")
	}
}

func TestBuildBootstrapRegistrationUsesStoredStakerSignature(t *testing.T) {
	staker := mustStructureKeyPair("bootstrap-wallet-staker")
	config := minimalNodeConfigForValidation()
	config.ChainID = "bootstrap-signed-chain"
	config.NodeName = "bootstrap-signed-validator"
	config.AdvertisedIP = "127.0.0.9"
	config.AdvertisedPort = 19199
	config.StakerAddress = staker.PublicKey.String()
	config.StakerSeed = ""
	config.BootstrapJoin = bootstrapJoinConfig{
		Enabled:               true,
		RPCURL:                "http://127.0.0.1:8899/",
		RegisteredAtUnixMilli: 123456789,
	}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	expected := rpc.BootstrapValidatorRegistrationRequest{
		ChainID:               normalized.ChainID,
		NodeName:              normalized.NodeName,
		PeerID:                mustRawKeyPair(normalized.PeerSeed).peerID,
		AdvertisedIP:          normalized.AdvertisedIP,
		AdvertisedPort:        normalized.AdvertisedPort,
		Network:               string(utils.ProtocolTCP),
		StakerAddress:         staker.PublicKey.String(),
		ValidatorAddress:      mustStructureKeyPair(normalized.ValidatorSeed).PublicKey.String(),
		ConsensusPublicKey:    mustStructureKeyPair(normalized.ConsensusSeed).PublicKey.String(),
		BLSPublicKeyBase64:    utils.Base64Encode(mustBLSKeyPair(normalized.ConsensusSeed).PublicKey),
		StakeLamports:         normalized.StakeLamports,
		RegisteredAtUnixMilli: normalized.BootstrapJoin.RegisteredAtUnixMilli,
	}
	signature, err := staker.Sign(bootstrapRegistrationSignBytes(expected))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	normalized.BootstrapJoin.StakerSignature = signature.String()

	registration, err := buildBootstrapRegistration(normalized)
	if err != nil {
		t.Fatalf("buildBootstrapRegistration() error = %v", err)
	}
	if registration.StakerSignature != signature.String() {
		t.Fatalf("StakerSignature = %q, want wallet signature", registration.StakerSignature)
	}
	if _, err := normalizeBootstrapRegistration(registration, normalized.ChainID); err != nil {
		t.Fatalf("normalizeBootstrapRegistration() error = %v", err)
	}
}

func TestApplyBootstrapManifestPreservesChainIdentity(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, config, 1)
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}

	validatorConfig := minimalNodeConfigForValidation()
	validatorConfig.NodeName = "validator-apply"
	validatorConfig.PeerSeed = "bootstrap-validator-peer-1"
	validatorConfig.StakerSeed = "bootstrap-staker-1"
	validatorConfig.ValidatorSeed = "bootstrap-validator-1"
	validatorConfig.ConsensusSeed = "bootstrap-consensus-1"
	validatorConfig.BootstrapJoin = bootstrapJoinConfig{Enabled: true, RPCURL: "http://127.0.0.1:8899/"}
	normalized, err := normalizeNodeConfig(validatorConfig)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	if err := validateBootstrapManifestForLocalNode(manifest, registration.PeerID); err != nil {
		t.Fatalf("validateBootstrapManifestForLocalNode() error = %v", err)
	}
	joined, err := applyBootstrapManifest(normalized, manifest)
	if err != nil {
		t.Fatalf("applyBootstrapManifest() error = %v", err)
	}
	if joined.ChainIdentityHash != manifest.ChainIdentityHash {
		t.Fatalf("chain identity = %s, want %s", joined.ChainIdentityHash, manifest.ChainIdentityHash)
	}
	if joined.GenesisHash != manifest.GenesisHash {
		t.Fatalf("genesis hash = %s, want %s", joined.GenesisHash, manifest.GenesisHash)
	}
}

func TestActivateBootstrapManifestUpdatesForwardPeers(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 1)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	registration := testBootstrapRegistration(t, config, 1)
	if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
		t.Fatalf("BootstrapRegisterValidator() error = %v", err)
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}
	node := &posNode{
		config:               config,
		logger:               testBootstrapLogger(),
		peerKeyPair:          mustRawKeyPair(config.PeerSeed),
		bootstrapCoordinator: coordinator,
	}

	if err := node.activateBootstrapManifest(context.Background(), manifest); err != nil {
		t.Fatalf("activateBootstrapManifest() error = %v", err)
	}
	if !node.bootstrapManifestApplied {
		t.Fatal("bootstrapManifestApplied = false, want true")
	}
	if node.config.ChainIdentityHash != manifest.ChainIdentityHash {
		t.Fatalf("chain identity = %s, want %s", node.config.ChainIdentityHash, manifest.ChainIdentityHash)
	}
	if len(node.config.BootstrapPeers) != 1 {
		t.Fatalf("bootstrap peers = %d, want 1", len(node.config.BootstrapPeers))
	}
	if node.config.BootstrapPeers[0].PeerID != registration.PeerID {
		t.Fatalf("peer id = %s, want %s", node.config.BootstrapPeers[0].PeerID, registration.PeerID)
	}
}

func TestBootstrapTransactionForwardingPrioritizesLeaderWindow(t *testing.T) {
	config := testBootstrapCoordinatorConfig(t, 4)
	coordinator, err := newBootstrapCoordinator(config, testBootstrapLogger())
	if err != nil {
		t.Fatalf("newBootstrapCoordinator() error = %v", err)
	}
	for index := 1; index <= 4; index++ {
		registration := testBootstrapRegistration(t, config, index)
		if _, err := coordinator.BootstrapRegisterValidator(context.Background(), registration); err != nil {
			t.Fatalf("BootstrapRegisterValidator(%d) error = %v", index, err)
		}
	}
	manifest, err := coordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapManifest() error = %v", err)
	}
	node := &posNode{
		config:               config,
		logger:               testBootstrapLogger(),
		peerKeyPair:          mustRawKeyPair(config.PeerSeed),
		bootstrapCoordinator: coordinator,
	}
	if err := node.activateBootstrapManifest(context.Background(), manifest); err != nil {
		t.Fatalf("activateBootstrapManifest() error = %v", err)
	}

	connectedPeerIDs := make([]string, 0, len(node.config.BootstrapPeers))
	for index := len(node.config.BootstrapPeers) - 1; index >= 0; index-- {
		connectedPeerIDs = append(connectedPeerIDs, node.config.BootstrapPeers[index].PeerID)
	}
	leaderPeerIDs := node.bootstrapPreferredTransactionPeerIDsFromConfig()
	if len(leaderPeerIDs) == 0 {
		t.Fatal("leader peer ids are empty")
	}
	orderedPeerIDs := node.orderedBootstrapTransactionPeerIDs(context.Background(), connectedPeerIDs)

	assertPeerPrefix(t, orderedPeerIDs, leaderPeerIDs)
	assertPeerSetContains(t, orderedPeerIDs, connectedPeerIDs)
	assertUniquePeerIDs(t, orderedPeerIDs)
}

func testBootstrapCoordinatorConfig(t *testing.T, minValidators int) nodeConfig {
	t.Helper()
	config := minimalNodeConfigForValidation()
	config.NodeName = "bootstrap-test"
	config.PeerSeed = "bootstrap-peer"
	config.ListenPort = 19090
	config.BootstrapCoordinator = bootstrapCoordinatorConfig{
		Enabled:                 true,
		MinValidators:           minValidators,
		GenesisStartDelayMillis: 2_000,
		RegistryPath:            filepath.Join(t.TempDir(), "registry.json"),
	}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	return normalized
}

func testBootstrapRegistration(t *testing.T, config nodeConfig, index int) rpc.BootstrapValidatorRegistrationRequest {
	t.Helper()
	indexText := strconv.Itoa(index)
	peer := mustRawKeyPair("bootstrap-validator-peer-" + indexText)
	staker := mustStructureKeyPair("bootstrap-staker-" + indexText)
	validator := mustStructureKeyPair("bootstrap-validator-" + indexText)
	consensusKey := mustStructureKeyPair("bootstrap-consensus-" + indexText)
	blsKey := mustBLSKeyPair("bootstrap-consensus-" + indexText)
	request := rpc.BootstrapValidatorRegistrationRequest{
		ChainID:               config.ChainID,
		NodeName:              "validator-" + indexText,
		PeerID:                peer.peerID,
		AdvertisedIP:          "127.0.0." + indexText,
		AdvertisedPort:        19100 + index,
		Network:               string(utils.ProtocolTCP),
		StakerAddress:         staker.PublicKey.String(),
		ValidatorAddress:      validator.PublicKey.String(),
		ConsensusPublicKey:    consensusKey.PublicKey.String(),
		BLSPublicKeyBase64:    utils.Base64Encode(blsKey.PublicKey),
		StakeLamports:         stake.MinimumStakeLamports,
		RegisteredAtUnixMilli: time.Now().UnixMilli(),
	}
	signBootstrapStakerAuthorization(t, &request, "bootstrap-staker-"+indexText)
	signBootstrapRegistration(t, &request, "bootstrap-consensus-"+indexText)
	return request
}

func signBootstrapStakerAuthorization(t *testing.T, request *rpc.BootstrapValidatorRegistrationRequest, stakerSeed string) {
	t.Helper()
	stakerKey := mustStructureKeyPair(stakerSeed)
	signature, err := stakerKey.Sign(bootstrapRegistrationSignBytes(*request))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request.StakerSignature = signature.String()
}

func signBootstrapRegistration(t *testing.T, request *rpc.BootstrapValidatorRegistrationRequest, consensusSeed string) {
	t.Helper()
	consensusKey := mustStructureKeyPair(consensusSeed)
	signature, err := consensusKey.Sign(bootstrapRegistrationSignBytes(*request))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request.Signature = signature.String()
}

func testBootstrapLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
