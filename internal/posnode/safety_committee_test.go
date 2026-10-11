package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

func TestSafetyCommitteeUsesAuthenticatedEpochWeightsWhenCheckpointStateDiffers(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validators := append([]consensus.ValidatorState(nil), node.epochSnapshot.Validators...)
	validators[0].StakeLamports += 10000000
	committed, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatal(err)
	}
	node.epochSnapshot, err = consensus.NewEpochSnapshot(0, 1, 16, node.epochSnapshot.RandomSeed, committed)
	if err != nil {
		t.Fatal(err)
	}
	root, err := consensus.ValidatorSetRoot(committed)
	if err != nil {
		t.Fatal(err)
	}
	head := node.ledger.Head()
	node.safetyCheckpoint = consensusSafetyCheckpoint{FinalizedHeight: head.FinalizedHeight, FinalizedHash: head.FinalizedHash.String(), EpochID: 0, ValidatorSetRoot: root.String()}
	set, err := node.safetyValidatorSet()
	if err != nil {
		t.Fatal(err)
	}
	actualRoot, err := consensus.ValidatorSetRoot(set)
	if err != nil || actualRoot != root {
		t.Fatalf("safety committee changed its committed weights: %v", err)
	}
	node.safetyCheckpoint.ValidatorSetRoot = testHashFromText(t, "forged-safety-committee").String()
	if _, err := node.safetyValidatorSet(); err == nil {
		t.Fatal("accepted a committee without the committed root")
	}
	node.safetyCheckpoint.EpochID = 1
	node.safetyCheckpoint.ValidatorSetRoot = root.String()
	if _, err := node.safetyValidatorSet(); err == nil {
		t.Fatal("accepted an epoch without its authenticated transition proof")
	}
}
