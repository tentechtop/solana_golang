package consensus

import "testing"

func TestEpochSnapshotValidateRejectsValidatorAccountSubstitution(t *testing.T) {
	_, snapshot, validators := newRewardTestState(t, []uint64{70, 30}, []uint16{0, 0})
	snapshot.Validators[0].AccountAddress = validators[1].AccountAddress
	if err := snapshot.Validate(); err == nil {
		t.Fatal("EpochSnapshot.Validate() error = nil, want validator account substitution rejection")
	}
}

func TestEpochSnapshotValidateRejectsValidatorIDSubstitution(t *testing.T) {
	_, snapshot, _ := newRewardTestState(t, []uint64{70, 30}, []uint16{0, 0})
	snapshot.Validators[0].ValidatorID = snapshot.Validators[1].ValidatorID
	if err := snapshot.Validate(); err == nil {
		t.Fatal("EpochSnapshot.Validate() error = nil, want validator id substitution rejection")
	}
}

func TestEpochSnapshotRootIgnoresRuntimePerformanceCounters(t *testing.T) {
	_, snapshot, _ := newRewardTestState(t, []uint64{70, 30}, []uint16{0, 0})
	validators := append([]ValidatorState(nil), snapshot.Validators...)
	validators[0].LastVotedSlot++
	validators[0].MissedProposalCount++
	validators[0].MissedVoteCount++
	validatorSet, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	updated, err := NewEpochSnapshot(
		snapshot.EpochID,
		snapshot.StartSlot,
		snapshot.EndSlot-snapshot.StartSlot+1,
		snapshot.RandomSeed,
		validatorSet,
	)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	if updated.SnapshotStateRoot != snapshot.SnapshotStateRoot {
		t.Fatalf("runtime counters changed snapshot root from %s to %s", snapshot.SnapshotStateRoot.String(), updated.SnapshotStateRoot.String())
	}
}
