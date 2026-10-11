package consensus

import "testing"

func TestRecoveryRewardProofSelectsCommitteeBySignatureAndWeight(t *testing.T) {
	_, current, validators := newRewardTestState(t, []uint64{2, 2, 2, 2}, []uint16{0, 0, 0, 0})
	elderSet, err := SingleValidatorSet(validators[0])
	if err != nil {
		t.Fatal(err)
	}
	old, err := NewEpochSnapshot(current.EpochID, current.StartSlot, current.EndSlot-current.StartSlot+1, current.RandomSeed, elderSet)
	if err != nil {
		t.Fatal(err)
	}
	oldQC := testRewardQC(old, 2, 2, []int{0})
	newQC := testRewardQC(current, 3, 3, []int{0, 1, 2})
	quorum := Quorum{Numerator: 2, Denominator: 3, Strict: true}
	selected, err := proofSnapshotForCertificate(oldQC, current, []EpochSnapshot{old}, quorum)
	if err != nil || selected.SnapshotStateRoot != old.SnapshotStateRoot {
		t.Fatalf("lost old elder QC: %v", err)
	}
	selected, err = proofSnapshotForCertificate(newQC, current, []EpochSnapshot{old}, quorum)
	if err != nil || selected.SnapshotStateRoot != current.SnapshotStateRoot {
		t.Fatalf("lost recovered QC: %v", err)
	}
	oldQC.ConfirmedStake = current.TotalActiveStake
	if _, err := proofSnapshotForCertificate(oldQC, current, []EpochSnapshot{old}, quorum); err == nil {
		t.Fatal("accepted forged QC weight")
	}
}
