package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/consensus"
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

func TestAcceptedQCCacheOnlyMatchesExactCertificate(t *testing.T) {
	node := &posNode{acceptedQCs: make(map[string]uint64)}
	base := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               31,
		BlockHeight:        19,
		BlockHash:          mustHash("accepted-qc-cache"),
		ThresholdStake:     67,
		ConfirmedStake:     70,
		Voters:             []string{"validator-a", "validator-b"},
		SignatureScheme:    consensus.BLSSignatureSchemeBasic,
		AggregateSignature: []byte{1, 2, 3},
		VoterBitmap:        []byte{3},
		CreatedAtUnixMilli: 1710000000000,
	}
	baseHash, err := hashQC(base)
	if err != nil {
		t.Fatalf("hashQC(base) error = %v", err)
	}
	if node.hasQCAccepted(baseHash) {
		t.Fatal("hasQCAccepted(base) = true before acceptance")
	}
	node.markQCAccepted(baseHash, base.Slot)
	if !node.hasQCAccepted(baseHash) {
		t.Fatal("hasQCAccepted(base) = false after acceptance")
	}

	tampered := base
	tampered.AggregateSignature = []byte{1, 2, 4}
	tamperedHash, err := hashQC(tampered)
	if err != nil {
		t.Fatalf("hashQC(tampered) error = %v", err)
	}
	if node.hasQCAccepted(tamperedHash) {
		t.Fatal("hasQCAccepted(tampered) = true; changed signature must be reverified")
	}

	stronger := base
	stronger.ConfirmedStake = 80
	strongerHash, err := hashQC(stronger)
	if err != nil {
		t.Fatalf("hashQC(stronger) error = %v", err)
	}
	if node.hasQCAccepted(strongerHash) {
		t.Fatal("hasQCAccepted(stronger) = true; changed stake must be reverified")
	}
}
