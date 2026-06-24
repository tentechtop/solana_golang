package posnode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const maxBootstrapRegistryBytes = 4 << 20

type bootstrapCoordinator struct {
	mutex          sync.Mutex
	config         nodeConfig
	logger         *slog.Logger
	registryPath   string
	registrations  map[string]rpc.BootstrapValidatorRegistrationRequest
	frozen         bool
	frozenManifest rpc.BootstrapManifestResult
}

type bootstrapRegistryFile struct {
	Registrations    []rpc.BootstrapValidatorRegistrationRequest `json:"registrations"`
	Frozen           bool                                        `json:"frozen"`
	FrozenManifest   rpc.BootstrapManifestResult                 `json:"frozen_manifest,omitempty"`
	UpdatedUnixMilli int64                                       `json:"updated_unix_milli"`
}

func (node *posNode) BootstrapRegisterValidator(ctx context.Context, request rpc.BootstrapValidatorRegistrationRequest) (rpc.BootstrapRegisterValidatorResult, error) {
	if node.bootstrapCoordinator == nil {
		return rpc.BootstrapRegisterValidatorResult{}, fmt.Errorf("posnode: bootstrap coordinator is disabled")
	}
	result, err := node.bootstrapCoordinator.BootstrapRegisterValidator(ctx, request)
	if err != nil {
		return rpc.BootstrapRegisterValidatorResult{}, err
	}
	if result.Ready {
		manifest, manifestErr := node.bootstrapCoordinator.GetBootstrapManifest(ctx)
		if manifestErr != nil {
			return rpc.BootstrapRegisterValidatorResult{}, manifestErr
		}
		if err := node.activateBootstrapManifest(ctx, manifest); err != nil {
			return rpc.BootstrapRegisterValidatorResult{}, err
		}
	}
	return result, nil
}

func (node *posNode) GetBootstrapManifest(ctx context.Context) (rpc.BootstrapManifestResult, error) {
	if node.bootstrapCoordinator == nil {
		return rpc.BootstrapManifestResult{}, fmt.Errorf("posnode: bootstrap coordinator is disabled")
	}
	manifest, err := node.bootstrapCoordinator.GetBootstrapManifest(ctx)
	if err != nil {
		return rpc.BootstrapManifestResult{}, err
	}
	if manifest.Ready {
		if err := node.activateBootstrapManifest(ctx, manifest); err != nil {
			return rpc.BootstrapManifestResult{}, err
		}
	}
	return manifest, nil
}

func (node *posNode) GetBootstrapStatus(ctx context.Context) (rpc.BootstrapStatusResult, error) {
	if node.bootstrapCoordinator == nil {
		return rpc.BootstrapStatusResult{}, fmt.Errorf("posnode: bootstrap coordinator is disabled")
	}
	return node.bootstrapCoordinator.GetBootstrapStatus(ctx)
}

func (node *posNode) activateBootstrapManifest(ctx context.Context, manifest rpc.BootstrapManifestResult) error {
	_ = ctx
	if !manifest.Ready {
		return nil
	}
	node.mutex.Lock()
	if node.bootstrapManifestApplied && node.config.ChainIdentityHash == manifest.ChainIdentityHash {
		node.mutex.Unlock()
		return nil
	}
	joinedConfig, err := applyBootstrapManifest(node.config, manifest)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	node.config = joinedConfig
	node.bootstrapManifestApplied = true
	peers := append([]peerConfig(nil), joinedConfig.BootstrapPeers...)
	node.mutex.Unlock()
	if err := node.addBootstrapManifestPeers(peers); err != nil {
		return err
	}
	node.logger.Info("bootstrap manifest activated",
		slog.String("chain_id", joinedConfig.ChainID),
		slog.String("chain_identity_hash", joinedConfig.ChainIdentityHash),
		slog.String("genesis_hash", joinedConfig.GenesisHash),
		slog.Int("bootstrap_peers", len(peers)),
	)
	return nil
}

func (node *posNode) addBootstrapManifestPeers(peers []peerConfig) error {
	if node.host == nil {
		return nil
	}
	for _, peerConfig := range peers {
		if peerConfig.PeerID == "" || peerConfig.PeerID == node.peerKeyPair.peerID {
			continue
		}
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, peerConfig.IP, peerConfig.p2pProtocol(), peerConfig.Port, peerConfig.PeerID)
		if err != nil {
			return fmt.Errorf("posnode: build bootstrap manifest peer address: %w", err)
		}
		peer, err := p2p.NewPeer(peerConfig.PeerID, []utils.MultiAddress{address})
		if err != nil {
			return fmt.Errorf("posnode: create bootstrap manifest peer: %w", err)
		}
		peer.Role = peerConfig.ResolvedRole
		peer.Capabilities = peerConfig.ResolvedCapabilities
		peer.Validator = peer.Capabilities&p2p.PeerCapabilityValidator != 0
		if err := node.host.AddPeer(peer); err != nil {
			return fmt.Errorf("posnode: add bootstrap manifest peer: %w", err)
		}
		node.addKnownPeerID(peerConfig.PeerID)
	}
	return nil
}

func newBootstrapCoordinator(config nodeConfig, logger *slog.Logger) (*bootstrapCoordinator, error) {
	coordinator := &bootstrapCoordinator{
		config:        config,
		logger:        logger,
		registryPath:  bootstrapRegistryPath(config),
		registrations: make(map[string]rpc.BootstrapValidatorRegistrationRequest),
	}
	if err := coordinator.loadRegistry(); err != nil {
		return nil, err
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if err := coordinator.freezeIfReadyLocked(); err != nil {
		return nil, err
	}
	return coordinator, nil
}

func bootstrapRegistryPath(config nodeConfig) string {
	if strings.TrimSpace(config.BootstrapCoordinator.RegistryPath) != "" {
		return filepath.Clean(config.BootstrapCoordinator.RegistryPath)
	}
	dataRootPath := strings.TrimSpace(config.DataRootPath)
	if dataRootPath == "" {
		dataRootPath = strings.TrimSpace(config.DataPath)
	}
	return filepath.Join(filepath.Clean(dataRootPath), "bootstrap-registry.json")
}

func (coordinator *bootstrapCoordinator) BootstrapRegisterValidator(ctx context.Context, request rpc.BootstrapValidatorRegistrationRequest) (rpc.BootstrapRegisterValidatorResult, error) {
	_ = ctx
	normalized, err := normalizeBootstrapRegistration(request, coordinator.config.ChainID)
	if err != nil {
		return rpc.BootstrapRegisterValidatorResult{}, err
	}
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.frozen {
		if existing, ok := coordinator.registrations[normalized.PeerID]; ok && sameBootstrapValidatorIdentity(existing, normalized) {
			return coordinator.registerResultLocked(true), nil
		}
		return rpc.BootstrapRegisterValidatorResult{}, fmt.Errorf("posnode: bootstrap manifest already frozen")
	}
	if err := coordinator.ensureRegistrationUniqueLocked(normalized); err != nil {
		return rpc.BootstrapRegisterValidatorResult{}, err
	}
	coordinator.registrations[normalized.PeerID] = normalized
	if err := coordinator.freezeIfReadyLocked(); err != nil {
		return rpc.BootstrapRegisterValidatorResult{}, err
	}
	if err := coordinator.saveRegistryLocked(); err != nil {
		return rpc.BootstrapRegisterValidatorResult{}, err
	}
	coordinator.logger.Info("bootstrap validator registered",
		slog.String("peer_id", normalized.PeerID),
		slog.String("validator", normalized.ValidatorAddress),
		slog.Int("validator_count", len(coordinator.registrations)),
		slog.Int("min_validators", coordinator.config.BootstrapCoordinator.MinValidators),
		slog.Bool("ready", coordinator.frozen),
	)
	return coordinator.registerResultLocked(true), nil
}

func (coordinator *bootstrapCoordinator) GetBootstrapManifest(ctx context.Context) (rpc.BootstrapManifestResult, error) {
	_ = ctx
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.frozen {
		return coordinator.frozenManifest, nil
	}
	return rpc.BootstrapManifestResult{
		Ready:          false,
		ValidatorCount: len(coordinator.registrations),
		MinValidators:  coordinator.config.BootstrapCoordinator.MinValidators,
	}, nil
}

func (coordinator *bootstrapCoordinator) GetBootstrapStatus(ctx context.Context) (rpc.BootstrapStatusResult, error) {
	_ = ctx
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	peerIDs := make([]string, 0, len(coordinator.registrations))
	for peerID := range coordinator.registrations {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)
	status := rpc.BootstrapStatusResult{
		Ready:             coordinator.frozen,
		ValidatorCount:    len(coordinator.registrations),
		MinValidators:     coordinator.config.BootstrapCoordinator.MinValidators,
		RegisteredPeerIDs: peerIDs,
	}
	if coordinator.frozen {
		status.GenesisStartUnixMilli = coordinator.frozenManifest.GenesisStartUnixMilli
		status.ChainIdentityHash = coordinator.frozenManifest.ChainIdentityHash
		status.GenesisHash = coordinator.frozenManifest.GenesisHash
	}
	return status, nil
}

func (coordinator *bootstrapCoordinator) loadRegistry() error {
	info, err := os.Stat(coordinator.registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("posnode: stat bootstrap registry: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("posnode: bootstrap registry is a directory")
	}
	if info.Size() > maxBootstrapRegistryBytes {
		return fmt.Errorf("posnode: bootstrap registry file too large")
	}
	data, err := os.ReadFile(coordinator.registryPath)
	if err != nil {
		return fmt.Errorf("posnode: read bootstrap registry: %w", err)
	}
	file := bootstrapRegistryFile{}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("posnode: decode bootstrap registry: %w", err)
	}
	for _, request := range file.Registrations {
		normalized, err := normalizeBootstrapRegistration(request, coordinator.config.ChainID)
		if err != nil {
			return fmt.Errorf("posnode: invalid bootstrap registry entry: %w", err)
		}
		coordinator.registrations[normalized.PeerID] = normalized
	}
	coordinator.frozen = file.Frozen
	coordinator.frozenManifest = file.FrozenManifest
	if coordinator.frozen && !coordinator.frozenManifest.Ready {
		return fmt.Errorf("posnode: frozen bootstrap registry has no ready manifest")
	}
	if coordinator.frozen && coordinator.frozenManifest.ValidatorCount < coordinator.config.BootstrapCoordinator.MinValidators {
		return fmt.Errorf("posnode: frozen bootstrap registry validator count below threshold")
	}
	return nil
}

func (coordinator *bootstrapCoordinator) saveRegistryLocked() error {
	if err := os.MkdirAll(filepath.Dir(coordinator.registryPath), 0o755); err != nil {
		return fmt.Errorf("posnode: create bootstrap registry directory: %w", err)
	}
	registrations := coordinator.sortedRegistrationsLocked()
	file := bootstrapRegistryFile{
		Registrations:    registrations,
		Frozen:           coordinator.frozen,
		FrozenManifest:   coordinator.frozenManifest,
		UpdatedUnixMilli: time.Now().UnixMilli(),
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("posnode: encode bootstrap registry: %w", err)
	}
	tempPath := coordinator.registryPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return fmt.Errorf("posnode: write bootstrap registry: %w", err)
	}
	if err := os.Rename(tempPath, coordinator.registryPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("posnode: replace bootstrap registry: %w", err)
	}
	return nil
}

func (coordinator *bootstrapCoordinator) freezeIfReadyLocked() error {
	if coordinator.frozen || len(coordinator.registrations) < coordinator.config.BootstrapCoordinator.MinValidators {
		return nil
	}
	manifest, err := coordinator.buildManifestLocked()
	if err != nil {
		return err
	}
	coordinator.frozen = true
	coordinator.frozenManifest = manifest
	coordinator.logger.Info("bootstrap manifest frozen",
		slog.Int("validator_count", manifest.ValidatorCount),
		slog.Int("min_validators", manifest.MinValidators),
		slog.Int64("genesis_start_unix_millis", manifest.GenesisStartUnixMilli),
		slog.String("chain_identity_hash", manifest.ChainIdentityHash),
	)
	return nil
}

func (coordinator *bootstrapCoordinator) buildManifestLocked() (rpc.BootstrapManifestResult, error) {
	registrations := coordinator.sortedRegistrationsLocked()
	genesisValidators := make([]genesisValidatorConfig, 0, len(registrations))
	bootstrapPeers := make([]peerConfig, 0, len(registrations))
	for _, registration := range registrations {
		genesisValidators = append(genesisValidators, genesisValidatorConfig{
			StakerAddress:      registration.StakerAddress,
			ValidatorAddress:   registration.ValidatorAddress,
			ConsensusPublicKey: registration.ConsensusPublicKey,
			BLSPublicKeyBase64: registration.BLSPublicKeyBase64,
			PeerID:             registration.PeerID,
			StakeLamports:      registration.StakeLamports,
			CommissionBps:      registration.CommissionBps,
		})
		bootstrapPeers = append(bootstrapPeers, peerConfig{
			PeerID:       registration.PeerID,
			IP:           registration.AdvertisedIP,
			Port:         registration.AdvertisedPort,
			Network:      registration.Network,
			Role:         string(p2p.PeerRoleValidator),
			Capabilities: []string{"validator", "relay", "state_sync", "dht"},
		})
	}
	manifestConfig := coordinator.config
	manifestConfig.Genesis.InitialValidators = genesisValidators
	manifestConfig.BootstrapPeers = bootstrapPeers
	manifestConfig.GenesisStartMs = time.Now().Add(time.Duration(coordinator.config.BootstrapCoordinator.GenesisStartDelayMillis) * time.Millisecond).UnixMilli()
	normalized, err := normalizeNodeConfig(manifestConfig)
	if err != nil {
		return rpc.BootstrapManifestResult{}, fmt.Errorf("posnode: build bootstrap manifest identity: %w", err)
	}
	return bootstrapManifestFromConfig(normalized, true, len(registrations), coordinator.config.BootstrapCoordinator.MinValidators)
}

func (coordinator *bootstrapCoordinator) sortedRegistrationsLocked() []rpc.BootstrapValidatorRegistrationRequest {
	registrations := make([]rpc.BootstrapValidatorRegistrationRequest, 0, len(coordinator.registrations))
	for _, registration := range coordinator.registrations {
		registrations = append(registrations, registration)
	}
	sort.Slice(registrations, func(left int, right int) bool {
		if registrations[left].PeerID == registrations[right].PeerID {
			return registrations[left].ValidatorAddress < registrations[right].ValidatorAddress
		}
		return registrations[left].PeerID < registrations[right].PeerID
	})
	return registrations
}

func (coordinator *bootstrapCoordinator) registerResultLocked(accepted bool) rpc.BootstrapRegisterValidatorResult {
	result := rpc.BootstrapRegisterValidatorResult{
		Accepted:       accepted,
		Ready:          coordinator.frozen,
		ValidatorCount: len(coordinator.registrations),
		MinValidators:  coordinator.config.BootstrapCoordinator.MinValidators,
	}
	if coordinator.frozen {
		result.GenesisStartUnixMilli = coordinator.frozenManifest.GenesisStartUnixMilli
		result.ChainIdentityHash = coordinator.frozenManifest.ChainIdentityHash
		result.GenesisHash = coordinator.frozenManifest.GenesisHash
	}
	return result
}

func (coordinator *bootstrapCoordinator) ensureRegistrationUniqueLocked(request rpc.BootstrapValidatorRegistrationRequest) error {
	for peerID, existing := range coordinator.registrations {
		if peerID == request.PeerID {
			if sameBootstrapValidatorIdentity(existing, request) {
				return nil
			}
			return fmt.Errorf("posnode: bootstrap peer id already registered with different identity")
		}
		if existing.StakerAddress == request.StakerAddress {
			return fmt.Errorf("posnode: bootstrap staker already registered")
		}
		if existing.ValidatorAddress == request.ValidatorAddress {
			return fmt.Errorf("posnode: bootstrap validator address already registered")
		}
		if existing.ConsensusPublicKey == request.ConsensusPublicKey {
			return fmt.Errorf("posnode: bootstrap consensus key already registered")
		}
	}
	return nil
}

func sameBootstrapValidatorIdentity(left rpc.BootstrapValidatorRegistrationRequest, right rpc.BootstrapValidatorRegistrationRequest) bool {
	return left.PeerID == right.PeerID &&
		left.StakerAddress == right.StakerAddress &&
		left.ValidatorAddress == right.ValidatorAddress &&
		left.ConsensusPublicKey == right.ConsensusPublicKey &&
		left.BLSPublicKeyBase64 == right.BLSPublicKeyBase64
}

func normalizeBootstrapRegistration(request rpc.BootstrapValidatorRegistrationRequest, chainID string) (rpc.BootstrapValidatorRegistrationRequest, error) {
	request.ChainID = strings.TrimSpace(request.ChainID)
	request.NodeName = strings.TrimSpace(request.NodeName)
	request.PeerID = strings.TrimSpace(request.PeerID)
	request.AdvertisedIP = strings.TrimSpace(request.AdvertisedIP)
	request.Network = strings.TrimSpace(request.Network)
	request.StakerAddress = strings.TrimSpace(request.StakerAddress)
	request.ValidatorAddress = strings.TrimSpace(request.ValidatorAddress)
	request.ConsensusPublicKey = strings.TrimSpace(request.ConsensusPublicKey)
	request.BLSPublicKeyBase64 = strings.TrimSpace(request.BLSPublicKeyBase64)
	request.Signature = strings.TrimSpace(request.Signature)
	// 功能目的：允许空链 ID 发现模式；实现原因：显式链 ID 仍强校验，空值由引导节点绑定权威链。
	requestedChainID := request.ChainID
	if requestedChainID != "" && requestedChainID != chainID {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap chain id mismatch")
	}
	if err := validateBootstrapText(request.NodeName, 1, 128, "node name"); err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, err
	}
	if err := validateBootstrapPeerID(request.PeerID); err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, err
	}
	if net.ParseIP(request.AdvertisedIP) == nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap advertised ip is invalid")
	}
	if request.AdvertisedPort < 1 || request.AdvertisedPort > 65535 {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap advertised port is invalid")
	}
	if request.Network == "" {
		request.Network = string(utils.ProtocolTCP)
	}
	if request.Network != string(utils.ProtocolTCP) {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap network must be tcp")
	}
	if _, err := structure.PublicKeyFromBase58(request.StakerAddress); err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap staker address: %w", err)
	}
	if _, err := structure.PublicKeyFromBase58(request.ValidatorAddress); err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap validator address: %w", err)
	}
	consensusPublicKey, err := structure.PublicKeyFromBase58(request.ConsensusPublicKey)
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap consensus public key: %w", err)
	}
	blsPublicKey, err := utils.Base64Decode(request.BLSPublicKeyBase64)
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap bls public key: %w", err)
	}
	if err := consensus.ValidateBLSPublicKey(blsPublicKey); err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap bls public key: %w", err)
	}
	if request.StakeLamports < stake.MinimumStakeLamports {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap stake below minimum")
	}
	if request.RegisteredAtUnixMilli <= 0 {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap registered timestamp is invalid")
	}
	signature, err := structure.SignatureFromBase58(request.Signature)
	if err != nil {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap signature: %w", err)
	}
	if !structure.VerifyMessageSignature(consensusPublicKey, bootstrapRegistrationSignBytes(request), signature) {
		return rpc.BootstrapValidatorRegistrationRequest{}, fmt.Errorf("posnode: bootstrap registration signature invalid")
	}
	return request, nil
}

func validateBootstrapText(value string, minLength int, maxLength int, field string) error {
	if len(value) < minLength || len(value) > maxLength {
		return fmt.Errorf("posnode: bootstrap %s length is invalid", field)
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return fmt.Errorf("posnode: bootstrap %s contains control character", field)
		}
	}
	return nil
}

func validateBootstrapPeerID(peerID string) error {
	decoded, err := utils.Base58Decode(peerID)
	if err != nil {
		return fmt.Errorf("posnode: bootstrap peer id: %w", err)
	}
	if len(decoded) != structure.PublicKeySize {
		return fmt.Errorf("posnode: bootstrap peer id length is invalid")
	}
	return nil
}

func bootstrapRegistrationSignBytes(request rpc.BootstrapValidatorRegistrationRequest) []byte {
	fields := []string{
		"pos-bootstrap-register-v1",
		strings.TrimSpace(request.ChainID),
		strings.TrimSpace(request.NodeName),
		strings.TrimSpace(request.PeerID),
		strings.TrimSpace(request.AdvertisedIP),
		strconv.Itoa(request.AdvertisedPort),
		strings.TrimSpace(request.Network),
		strings.TrimSpace(request.StakerAddress),
		strings.TrimSpace(request.ValidatorAddress),
		strings.TrimSpace(request.ConsensusPublicKey),
		strings.TrimSpace(request.BLSPublicKeyBase64),
		strconv.FormatUint(request.StakeLamports, 10),
		strconv.FormatUint(uint64(request.CommissionBps), 10),
		strconv.FormatInt(request.RegisteredAtUnixMilli, 10),
	}
	return []byte(strings.Join(fields, "\n"))
}

func bootstrapManifestFromConfig(config nodeConfig, ready bool, validatorCount int, minValidators int) (rpc.BootstrapManifestResult, error) {
	genesis, err := bootstrapGenesisFromConfig(config)
	if err != nil {
		return rpc.BootstrapManifestResult{}, err
	}
	return rpc.BootstrapManifestResult{
		Ready:                         ready,
		ValidatorCount:                validatorCount,
		MinValidators:                 minValidators,
		ChainID:                       config.ChainID,
		ChainIdentityHash:             config.ChainIdentityHash,
		GenesisHash:                   config.GenesisHash,
		GenesisStartUnixMilli:         config.GenesisStartMs,
		SlotMillis:                    config.SlotMillis,
		EpochSlots:                    config.EpochSlots,
		FinalityDepth:                 config.FinalityDepth,
		TurbineFanout:                 config.TurbineFanout,
		TransactionLeaderForwardSlots: config.TransactionLeaderForwardSlots,
		TransactionForwardValidators:  config.forwardTransactionsToValidators(),
		PrivacyExecutionMode:          string(config.PrivacyExecutionMode),
		ContractDeploymentPolicy:      bootstrapContractPolicyFromConfig(config.ContractDeploymentPolicy),
		Genesis:                       genesis,
		BootstrapPeers:                bootstrapPeersFromConfig(config.BootstrapPeers),
	}, nil
}

func bootstrapGenesisFromConfig(config nodeConfig) (rpc.BootstrapGenesisResult, error) {
	genesis := rpc.BootstrapGenesisResult{
		InitialSupplyLamports: config.Genesis.InitialSupplyLamports,
		TreasuryAddress:       config.Genesis.TreasuryAddress,
		PrivacyExecutionMode:  string(config.Genesis.PrivacyExecutionMode),
		FundedAccounts:        make([]rpc.BootstrapGenesisAccountResult, 0, len(config.Genesis.FundedAccounts)),
		InitialValidators:     make([]rpc.BootstrapGenesisValidatorResult, 0, len(config.Genesis.InitialValidators)),
	}
	for _, account := range config.Genesis.FundedAccounts {
		address, err := genesisPublicKeyFromAddressOrSeed(account.Address, account.Seed, "funded account")
		if err != nil {
			return rpc.BootstrapGenesisResult{}, err
		}
		genesis.FundedAccounts = append(genesis.FundedAccounts, rpc.BootstrapGenesisAccountResult{
			Address:  address.String(),
			Lamports: account.Lamports,
		})
	}
	for _, validator := range config.Genesis.InitialValidators {
		genesis.InitialValidators = append(genesis.InitialValidators, rpc.BootstrapGenesisValidatorResult{
			StakerAddress:      validator.StakerAddress,
			ValidatorAddress:   validator.ValidatorAddress,
			ConsensusPublicKey: validator.ConsensusPublicKey,
			BLSPublicKeyBase64: validator.BLSPublicKeyBase64,
			PeerID:             validator.PeerID,
			StakeLamports:      validator.StakeLamports,
			CommissionBps:      validator.CommissionBps,
		})
	}
	return genesis, nil
}

func bootstrapPeersFromConfig(peers []peerConfig) []rpc.BootstrapPeerConfigResult {
	results := make([]rpc.BootstrapPeerConfigResult, 0, len(peers))
	for _, peer := range peers {
		results = append(results, rpc.BootstrapPeerConfigResult{
			PeerID:       peer.PeerID,
			IP:           peer.IP,
			Port:         peer.Port,
			Network:      peer.Network,
			Role:         peer.Role,
			Roles:        append([]string(nil), peer.Roles...),
			Capabilities: append([]string(nil), peer.Capabilities...),
		})
	}
	return results
}

func bootstrapContractPolicyFromConfig(config contractDeploymentPolicyConfig) rpc.BootstrapContractDeploymentPolicyResult {
	result := rpc.BootstrapContractDeploymentPolicyResult{
		AllowedDeployers:             append([]string(nil), config.AllowedDeployers...),
		MinDeploymentDepositLamports: config.MinDeploymentDepositLamports,
	}
	if config.RequireManifest != nil {
		result.RequireManifest = *config.RequireManifest
	}
	if config.AllowUpgradeableContracts != nil {
		result.AllowUpgradeableContracts = *config.AllowUpgradeableContracts
	}
	return result
}

func applyBootstrapManifest(config nodeConfig, manifest rpc.BootstrapManifestResult) (nodeConfig, error) {
	if !manifest.Ready {
		return nodeConfig{}, fmt.Errorf("posnode: bootstrap manifest is not ready")
	}
	config.ChainID = strings.TrimSpace(manifest.ChainID)
	config.GenesisStartMs = manifest.GenesisStartUnixMilli
	config.SlotMillis = manifest.SlotMillis
	config.EpochSlots = manifest.EpochSlots
	config.FinalityDepth = manifest.FinalityDepth
	config.TurbineFanout = manifest.TurbineFanout
	config.TransactionLeaderForwardSlots = manifest.TransactionLeaderForwardSlots
	config.PrivacyExecutionMode = runtimepkg.PrivacyExecutionMode(manifest.PrivacyExecutionMode)
	forwardValidators := manifest.TransactionForwardValidators
	config.TransactionForwardValidators = &forwardValidators
	config.ContractDeploymentPolicy = contractDeploymentPolicyFromBootstrap(manifest.ContractDeploymentPolicy)
	config.Genesis = genesisConfig{
		InitialSupplyLamports: manifest.Genesis.InitialSupplyLamports,
		TreasuryAddress:       manifest.Genesis.TreasuryAddress,
		PrivacyExecutionMode:  runtimepkg.PrivacyExecutionMode(manifest.Genesis.PrivacyExecutionMode),
		FundedAccounts:        make([]genesisAccountConfig, 0, len(manifest.Genesis.FundedAccounts)),
		InitialValidators:     make([]genesisValidatorConfig, 0, len(manifest.Genesis.InitialValidators)),
	}
	for _, account := range manifest.Genesis.FundedAccounts {
		config.Genesis.FundedAccounts = append(config.Genesis.FundedAccounts, genesisAccountConfig{
			Address:  account.Address,
			Lamports: account.Lamports,
		})
	}
	for _, validator := range manifest.Genesis.InitialValidators {
		config.Genesis.InitialValidators = append(config.Genesis.InitialValidators, genesisValidatorConfig{
			StakerAddress:      validator.StakerAddress,
			ValidatorAddress:   validator.ValidatorAddress,
			ConsensusPublicKey: validator.ConsensusPublicKey,
			BLSPublicKeyBase64: validator.BLSPublicKeyBase64,
			PeerID:             validator.PeerID,
			StakeLamports:      validator.StakeLamports,
			CommissionBps:      validator.CommissionBps,
		})
	}
	config.BootstrapPeers = make([]peerConfig, 0, len(manifest.BootstrapPeers))
	for _, peer := range manifest.BootstrapPeers {
		config.BootstrapPeers = append(config.BootstrapPeers, peerConfig{
			PeerID:       peer.PeerID,
			IP:           peer.IP,
			Port:         peer.Port,
			Network:      peer.Network,
			Role:         peer.Role,
			Roles:        append([]string(nil), peer.Roles...),
			Capabilities: append([]string(nil), peer.Capabilities...),
		})
	}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		return nodeConfig{}, err
	}
	if normalized.ChainIdentityHash != manifest.ChainIdentityHash {
		return nodeConfig{}, fmt.Errorf("posnode: bootstrap chain identity mismatch")
	}
	if normalized.GenesisHash != manifest.GenesisHash {
		return nodeConfig{}, fmt.Errorf("posnode: bootstrap genesis hash mismatch")
	}
	return normalized, nil
}

func contractDeploymentPolicyFromBootstrap(policy rpc.BootstrapContractDeploymentPolicyResult) contractDeploymentPolicyConfig {
	requireManifest := policy.RequireManifest
	allowUpgradeable := policy.AllowUpgradeableContracts
	return contractDeploymentPolicyConfig{
		AllowedDeployers:             append([]string(nil), policy.AllowedDeployers...),
		MinDeploymentDepositLamports: policy.MinDeploymentDepositLamports,
		RequireManifest:              &requireManifest,
		AllowUpgradeableContracts:    &allowUpgradeable,
	}
}
