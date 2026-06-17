package main

import (
	"testing"

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

	registerResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodRegisterValidator, []any{
		"rpc-source",
		validatorSeed,
		consensusSeed,
		"stage-rpc-peer",
		stake.MinimumStakeLamports,
	})
	registerResult := decodePosNodeRPCResult[rpc.TransactionSubmitResult](t, registerResponse)
	if registerResult.Signature == "" {
		t.Fatal("registerValidator signature is empty")
	}
	consensusKey := mustStructureKeyPair(consensusSeed)
	addValidatorStakeAccountToLedger(t, node.ledger, validator.PublicKey, stake.ValidatorState{
		ConsensusPublicKey: consensusKey.PublicKey,
		StakerAccount:      source.PublicKey,
		P2PPeerID:          "stage-rpc-peer",
		Status:             stake.ValidatorStatusActive,
		PendingStake:       stake.MinimumStakeLamports,
		ActivationEpoch:    node.ledger.Head().EpochID + 1,
	})

	stakeResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodStake, []any{
		"rpc-source",
		validator.PublicKey.String(),
		stake.MinimumStakeLamports,
	})
	stakeResult := decodePosNodeRPCResult[rpc.TransactionSubmitResult](t, stakeResponse)
	if stakeResult.Signature == "" {
		t.Fatal("stake signature is empty")
	}

	unstakeResponse := postPosNodeJSONRPC(t, server, 3, rpc.MethodUnstake, []any{
		"rpc-source",
		validator.PublicKey.String(),
		stake.MinimumStakeLamports,
		uint64(2),
	})
	if unstakeResponse.Error == nil {
		t.Fatal("unstake response error = nil, want pending stake rejection")
	}

	if len(node.mempool) != 2 {
		t.Fatalf("mempool size = %d, want 2", len(node.mempool))
	}
	if got := node.metrics.transactionsIn.Load(); got != 2 {
		t.Fatalf("transactionsIn = %d, want 2", got)
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
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
	registerResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodRegisterValidatorIdentity, []any{
		"rpc-source",
		validator.PublicKey.String(),
		consensusKey.PublicKey.String(),
		utils.Base58Encode(blsKeyPair.PublicKey),
		"stage-rpc-public-peer",
		stake.MinimumStakeLamports,
	})
	registerResult := decodePosNodeRPCResult[rpc.TransactionSubmitResult](t, registerResponse)
	if registerResult.Signature == "" {
		t.Fatal("registerValidatorIdentity signature is empty")
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
}
