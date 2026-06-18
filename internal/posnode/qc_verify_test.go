package posnode

import (
	"testing"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestVerifyQuorumCertificateRejectsUnsignedQCWhenBLSComplete(t *testing.T) {
	snapshot := newBLSCompleteEpochSnapshot(t)
	node := &posNode{
		config:        nodeConfig{EpochSlots: 8},
		epochSnapshot: snapshot,
	}
	qc := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               snapshot.StartSlot,
		BlockHeight:        1,
		BlockHash:          testHashFromText(t, "unsigned-bls-qc"),
		ThresholdStake:     67,
		ConfirmedStake:     100,
		Voters:             []string{string(snapshot.Validators[0].ValidatorID), string(snapshot.Validators[1].ValidatorID)},
		CreatedAtUnixMilli: 1710000000000,
	}
	if err := node.verifyQuorumCertificate(qc); err == nil {
		t.Fatal("verifyQuorumCertificate() error = nil, want unsigned QC rejection")
	}
}

func newBLSCompleteEpochSnapshot(t *testing.T) consensus.EpochSnapshot {
	t.Helper()
	validators := make([]consensus.ValidatorState, 2)
	for index := range validators {
		consensusKey := mustStructureKeyPair("qc-verify-consensus-" + string(rune('a'+index)))
		blsKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte("qc-verify-bls-" + string(rune('a'+index)))))
		if err != nil {
			t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
		}
		validators[index] = consensus.ValidatorState{
			AccountAddress:     mustStructureKeyPair("qc-verify-account-" + string(rune('a'+index))).PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			BLSPublicKey:       blsKeyPair.PublicKey,
			P2PPeerID:          "peer-qc-verify",
			StakeLamports:      50,
			Status:             consensus.ValidatorStatusActive,
		}
	}
	set, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(0, 1, 8, testHashFromText(t, "qc-verify-epoch"), set)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	return snapshot
}

func testHashFromText(t testing.TB, text string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(text)))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}
