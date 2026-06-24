package posnode

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"solana_golang/blockchain"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestValidatorPairingCompletesAndWritesValidatorConfig(t *testing.T) {
	node, configPath := newValidatorPairingTestNode(t, 19010, true)
	pairing, err := node.GetValidatorPairing(context.Background())
	if err != nil {
		t.Fatalf("GetValidatorPairing() error = %v", err)
	}
	if pairing.State != "pending" || pairing.ValidatorAddress == "" || pairing.BLSPublicKey == "" {
		t.Fatalf("pairing = %+v, want pending public keys", pairing)
	}
	if pairing.Completed.Signature != "" {
		t.Fatal("pairing public status leaked completion before completion")
	}
	staker := mustStructureKeyPair("paired-wallet-staker")
	signature := addValidatorPairingRegistrationToMempool(t, node, staker, stake.MinimumStakeLamports)
	request := newValidatorPairingCompleteRequest(node, staker, signature, stake.MinimumStakeLamports)
	result, err := node.CompleteValidatorPairing(context.Background(), request)
	if err != nil {
		t.Fatalf("CompleteValidatorPairing() error = %v", err)
	}
	if !result.ConfigUpdated || !result.RestartRequired {
		t.Fatalf("complete result = %+v, want config update and restart", result)
	}
	updated := readConfigMap(t, configPath)
	if updated["node_role"] != "validator" || updated["staker_address"] != staker.PublicKey.String() {
		t.Fatalf("updated config = %+v, want validator role and staker address", updated)
	}
	if _, exists := updated["staker_seed"]; exists {
		t.Fatal("updated config contains staker_seed, want wallet-only staker key")
	}
}

func TestValidatorPairingRejectsBadToken(t *testing.T) {
	node, _ := newValidatorPairingTestNode(t, 19011, false)
	request := rpc.ValidatorPairingCompleteRequest{
		Token:            "bad-token",
		StakerAddress:    mustStructureKeyPair("paired-wallet-staker").PublicKey.String(),
		ValidatorAddress: node.validatorPairing.payload.ValidatorAddress,
		ConsensusAddress: node.validatorPairing.payload.ConsensusAddress,
		BLSPublicKey:     node.validatorPairing.payload.BLSPublicKey,
		NodePeerID:       node.validatorPairing.payload.NodePeerID,
		StakeLamports:    10_000_000,
		Signature:        "test-signature",
	}
	if _, err := node.CompleteValidatorPairing(context.Background(), request); err == nil {
		t.Fatal("CompleteValidatorPairing() error = nil, want bad token rejection")
	}
}

func TestValidatorPairingRejectsBelowMinimumStake(t *testing.T) {
	node, _ := newValidatorPairingTestNode(t, 19012, false)
	staker := mustStructureKeyPair("paired-wallet-staker-below-minimum")
	signature := addValidatorPairingRegistrationToMempool(t, node, staker, stake.MinimumStakeLamports)
	request := newValidatorPairingCompleteRequest(node, staker, signature, stake.MinimumStakeLamports-1)

	_, err := node.CompleteValidatorPairing(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("CompleteValidatorPairing() error = %v, want below minimum rejection", err)
	}
}

func TestValidatorPairingRejectsMissingRegistrationTransaction(t *testing.T) {
	node, _ := newValidatorPairingTestNode(t, 19013, false)
	staker := mustStructureKeyPair("paired-wallet-staker-missing-transaction")
	transaction := newValidatorPairingRegistrationTransaction(t, staker, node.validatorPairing.payload, stake.MinimumStakeLamports)
	signature := mustTransactionID(t, transaction)
	request := newValidatorPairingCompleteRequest(node, staker, signature, stake.MinimumStakeLamports)

	_, err := node.CompleteValidatorPairing(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("CompleteValidatorPairing() error = %v, want missing registration transaction rejection", err)
	}
}

func TestValidatorPairingRejectsMismatchedRegistrationTransaction(t *testing.T) {
	node, _ := newValidatorPairingTestNode(t, 19014, false)
	staker := mustStructureKeyPair("paired-wallet-staker-mismatch")
	wrongPayload := node.validatorPairing.payload
	wrongPayload.ValidatorAddress = mustStructureKeyPair("paired-wallet-wrong-validator").PublicKey.String()
	transaction := newValidatorPairingRegistrationTransaction(t, staker, wrongPayload, stake.MinimumStakeLamports)
	signature := addTransactionToMempool(t, node, transaction)
	request := newValidatorPairingCompleteRequest(node, staker, signature, stake.MinimumStakeLamports)

	_, err := node.CompleteValidatorPairing(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "validator account mismatch") {
		t.Fatalf("CompleteValidatorPairing() error = %v, want validator mismatch rejection", err)
	}
}

func newValidatorPairingTestNode(t *testing.T, rpcPort int, writeConfigFile bool) (*posNode, string) {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "node.json")
	config := minimalNodeConfigForValidation()
	config.ConfigPath = configPath
	config.NodeRole = "full"
	config.RPCEnabled = true
	config.RPCPort = rpcPort
	config.DataPath = filepath.Join(tempDir, "data")
	config.AdvertisedIP = "192.168.1.10"
	disabled := false
	config.ValidatorEnabled = &disabled
	config.ConsensusEnabled = &disabled
	config.StakerSeed = ""
	config.ValidatorSeed = ""
	config.ConsensusSeed = ""
	config.ValidatorPairing = validatorPairingConfig{KeystoreDir: filepath.Join(tempDir, "keys")}
	if writeConfigFile {
		writeTestConfig(t, configPath, map[string]any{
			"node_name":         config.NodeName,
			"node_role":         "full",
			"validator_enabled": false,
			"consensus_enabled": false,
		})
	}
	normalized, err := normalizeNodeConfig(config)
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	normalized.ConfigPath = configPath
	node := &posNode{
		config:           normalized,
		peerKeyPair:      mustRawKeyPair("validator-pairing-peer"),
		logger:           testLogger(),
		seenTransactions: make(map[string]struct{}),
	}
	session, err := node.newValidatorPairingSession()
	if err != nil {
		t.Fatalf("newValidatorPairingSession() error = %v", err)
	}
	node.validatorPairing = session
	return node, configPath
}

func newValidatorPairingCompleteRequest(node *posNode, staker structure.SolanaKeyPair, signature string, stakeLamports uint64) rpc.ValidatorPairingCompleteRequest {
	return rpc.ValidatorPairingCompleteRequest{
		Token:            node.validatorPairing.payload.Token,
		StakerAddress:    "T" + staker.PublicKey.String(),
		ValidatorAddress: node.validatorPairing.payload.ValidatorAddress,
		ConsensusAddress: node.validatorPairing.payload.ConsensusAddress,
		BLSPublicKey:     node.validatorPairing.payload.BLSPublicKey,
		NodePeerID:       node.validatorPairing.payload.NodePeerID,
		StakeLamports:    stakeLamports,
		Signature:        signature,
	}
}

func addValidatorPairingRegistrationToMempool(t *testing.T, node *posNode, staker structure.SolanaKeyPair, stakeLamports uint64) string {
	t.Helper()
	transaction := newValidatorPairingRegistrationTransaction(t, staker, node.validatorPairing.payload, stakeLamports)
	return addTransactionToMempool(t, node, transaction)
}

func addTransactionToMempool(t *testing.T, node *posNode, transaction structure.Transaction) string {
	t.Helper()
	signature := mustTransactionID(t, transaction)
	node.mempool = append(node.mempool, transaction)
	node.seenTransactions[signature] = struct{}{}
	return signature
}

func newValidatorPairingRegistrationTransaction(t *testing.T, staker structure.SolanaKeyPair, payload validatorPairingPayload, stakeLamports uint64) structure.Transaction {
	t.Helper()
	validatorAddress, err := structure.PublicKeyFromBase58(payload.ValidatorAddress)
	if err != nil {
		t.Fatalf("validator address: %v", err)
	}
	consensusAddress, err := structure.PublicKeyFromBase58(payload.ConsensusAddress)
	if err != nil {
		t.Fatalf("consensus address: %v", err)
	}
	blsPublicKey, err := utils.Base58Decode(payload.BLSPublicKey)
	if err != nil {
		t.Fatalf("bls public key: %v", err)
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorAddress, consensusAddress, blsPublicKey, payload.NodePeerID, stakeLamports, mustHash("validator-pairing-registration"))
	if err != nil {
		t.Fatalf("NewRegisterValidatorTransactionWithBLS() error = %v", err)
	}
	return transaction
}

func mustTransactionID(t *testing.T, transaction structure.Transaction) string {
	t.Helper()
	signature, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}
	return signature
}

func writeTestConfig(t *testing.T, path string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readConfigMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	value := map[string]any{}
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return value
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}
