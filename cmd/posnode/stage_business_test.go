package main

import (
	"testing"

	"solana_golang/programs/stake"
	"solana_golang/rpc"
)

func TestStageBusinessRPCValidatorStakeAndUnstakeEntrypoints(t *testing.T) {
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
	unstakeResult := decodePosNodeRPCResult[rpc.TransactionSubmitResult](t, unstakeResponse)
	if unstakeResult.Signature == "" {
		t.Fatal("unstake signature is empty")
	}

	if len(node.mempool) != 3 {
		t.Fatalf("mempool size = %d, want 3", len(node.mempool))
	}
	if got := node.metrics.transactionsIn.Load(); got != 3 {
		t.Fatalf("transactionsIn = %d, want 3", got)
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
