package posnode

import (
	"testing"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/utils"
)

func TestStageBusinessRPCValidatorRegisterAndStakeEntrypoints(t *testing.T) {
	node, source, _ := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	validatorSeed := "stage-rpc-validator"
	consensusSeed := "stage-rpc-consensus"
	validator := mustStructureKeyPair(validatorSeed)
	consensusKey := mustStructureKeyPair(consensusSeed)

	registerTransaction, err := blockchain.NewRegisterValidatorTransaction(
		source,
		validator.PublicKey,
		consensusKey.PublicKey,
		"stage-rpc-peer",
		stake.MinimumStakeLamports,
		node.ledger.Head().BlockHash,
	)
	if err != nil {
		t.Fatalf("NewRegisterValidatorTransaction() error = %v", err)
	}
	registerResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodSendTransaction, []any{encodeRPCTransaction(t, registerTransaction)})
	registerSignature := decodePosNodeRPCResult[string](t, registerResponse)
	if registerSignature == "" {
		t.Fatal("sendTransaction register signature is empty")
	}
	addValidatorStakeAccountToLedger(t, node.ledger, validator.PublicKey, stake.ValidatorState{
		ConsensusPublicKey: consensusKey.PublicKey,
		StakerAccount:      source.PublicKey,
		P2PPeerID:          "stage-rpc-peer",
		Status:             stake.ValidatorStatusActive,
		PendingStake:       stake.MinimumStakeLamports,
		ActivationEpoch:    node.ledger.Head().EpochID + 1,
	})

	stakeTransaction, err := blockchain.NewStakeTransaction(source, validator.PublicKey, stake.MinimumStakeLamports, node.ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewStakeTransaction() error = %v", err)
	}
	stakeResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodSendTransaction, []any{encodeRPCTransaction(t, stakeTransaction)})
	stakeSignature := decodePosNodeRPCResult[string](t, stakeResponse)
	if stakeSignature == "" {
		t.Fatal("sendTransaction stake signature is empty")
	}

	unstakeResponse := postPosNodeJSONRPC(t, server, 3, rpc.MethodUnstake, []any{
		"rpc-source",
		validator.PublicKey.String(),
		stake.MinimumStakeLamports,
		uint64(2),
	})
	if unstakeResponse.Error == nil {
		t.Fatal("unstake response error = nil, want signed transaction requirement")
	}

	if len(node.mempool) != 2 {
		t.Fatalf("mempool size = %d, want 2", len(node.mempool))
	}
	if got := node.metrics.transactionsIn.Load(); got != 2 {
		t.Fatalf("transactionsIn = %d, want 2", got)
	}
	if got := node.metrics.transactionsDrop.Load(); got != 0 {
		t.Fatalf("transactionsDrop = %d, want 0", got)
	}
	if source.PublicKey.IsZero() {
		t.Fatal("source key unexpectedly empty")
	}
}

func TestStageBusinessRPCRejectsBelowMinimumStake(t *testing.T) {
	node, _, _ := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodRegisterValidator, []any{
		"rpc-source",
		"stage-rpc-low-validator",
		"stage-rpc-low-consensus",
		"stage-rpc-low-peer",
		stake.MinimumStakeLamports - 1,
	})
	if response.Error == nil {
		t.Fatal("registerValidator below minimum error = nil")
	}
	if response.Error.Data != "registerValidator requires wallet-local signing; submit the signed transaction with sendTransaction" {
		t.Fatalf("error data = %v, want signed transaction requirement", response.Error.Data)
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
}

func TestStageBusinessRPCValidatorPublicIdentityEntrypoints(t *testing.T) {
	node, _, _ := newRPCIntegrationTestNode(t)
	localBLSKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte("stage-rpc-local-consensus")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed(local) error = %v", err)
	}
	node.config.NodeName = "stage-rpc-local"
	node.config.StakeLamports = stake.MinimumStakeLamports
	node.stakerKeyPair = mustStructureKeyPair("stage-rpc-local-staker")
	node.validatorKeyPair = mustStructureKeyPair("stage-rpc-local-validator")
	node.consensusKeyPair = mustStructureKeyPair("stage-rpc-local-consensus")
	node.blsKeyPair = localBLSKeyPair
	node.peerKeyPair.peerID = "stage-rpc-local-peer"
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	identityResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetLocalValidatorIdentity, []any{})
	identityResult := decodePosNodeRPCResult[rpc.LocalValidatorIdentityResult](t, identityResponse)
	if identityResult.ValidatorAddress == "" || identityResult.ConsensusPublicKey == "" || identityResult.BLSPublicKey == "" {
		t.Fatalf("local validator identity incomplete: %+v", identityResult)
	}
	if identityResult.RecommendedStakeLamports < stake.MinimumStakeLamports {
		t.Fatalf("recommended stake = %d, want >= %d", identityResult.RecommendedStakeLamports, stake.MinimumStakeLamports)
	}

	validator := mustStructureKeyPair("stage-rpc-public-validator")
	consensusKey := mustStructureKeyPair("stage-rpc-public-consensus")
	blsKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte("stage-rpc-public-consensus")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
	}
	registerTransaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(
		mustStructureKeyPair("rpc-source"),
		validator.PublicKey,
		consensusKey.PublicKey,
		blsKeyPair.PublicKey,
		"stage-rpc-public-peer",
		stake.MinimumStakeLamports,
		node.ledger.Head().BlockHash,
	)
	if err != nil {
		t.Fatalf("NewRegisterValidatorTransactionWithBLS() error = %v", err)
	}
	registerResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodSendTransaction, []any{encodeRPCTransaction(t, registerTransaction)})
	registerSignature := decodePosNodeRPCResult[string](t, registerResponse)
	if registerSignature == "" {
		t.Fatal("sendTransaction register identity signature is empty")
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
}
