package main

import (
	"testing"

	"solana_golang/consensus"
)

func TestQCPropagationKeyIgnoresVoterOrder(t *testing.T) {
	blockHash := mustHash("qc-dedup-block")
	left := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               11,
		BlockHeight:        7,
		BlockHash:          blockHash,
		ThresholdStake:     67,
		ConfirmedStake:     70,
		Voters:             []string{"validator-b", "validator-a"},
		CreatedAtUnixMilli: 1710000000000,
	}
	right := left
	right.Voters = []string{"validator-a", "validator-b"}
	if qcPropagationKey(left) != qcPropagationKey(right) {
		t.Fatal("qcPropagationKey() differs for same voter set")
	}
}

func TestMarkQCSeenAllowsStrongerCertificate(t *testing.T) {
	node := &posNode{
		seenQCs: make(map[string]uint64),
	}
	blockHash := mustHash("qc-dedup-stronger-block")
	base := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               22,
		BlockHeight:        8,
		BlockHash:          blockHash,
		ThresholdStake:     67,
		ConfirmedStake:     70,
		Voters:             []string{"validator-a", "validator-b"},
		CreatedAtUnixMilli: 1710000000000,
	}
	duplicate := base
	duplicate.Voters = []string{"validator-b", "validator-a"}
	stronger := base
	stronger.ConfirmedStake = 100
	stronger.Voters = []string{"validator-a", "validator-b", "validator-c"}

	if !node.markQCSeen(base) {
		t.Fatal("markQCSeen(base) = false, want true")
	}
	if !node.hasQCSeen(duplicate) {
		t.Fatal("hasQCSeen(duplicate) = false, want true")
	}
	if node.markQCSeen(duplicate) {
		t.Fatal("markQCSeen(duplicate) = true, want false")
	}
	if !node.markQCSeen(stronger) {
		t.Fatal("markQCSeen(stronger) = false, want true")
	}
}
