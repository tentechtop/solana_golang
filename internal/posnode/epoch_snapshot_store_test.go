package posnode

import (
	"encoding/json"
	"testing"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestEpochContextLoadsPersistedHistoricalSnapshotWithoutLedger(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validatorSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSeed(0), validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	if err := node.persistEpochSnapshot(snapshot); err != nil {
		t.Fatalf("persistEpochSnapshot() error = %v", err)
	}

	node.epochSnapshot = consensus.EpochSnapshot{EpochID: 1}
	node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	node.ledger = nil
	contextValue, err := node.epochContextForSlotLocked(1)
	if err != nil {
		t.Fatalf("epochContextForSlotLocked() error = %v", err)
	}
	if contextValue.Snapshot.SnapshotStateRoot != snapshot.SnapshotStateRoot {
		t.Fatalf("snapshot root = %s, want %s", contextValue.Snapshot.SnapshotStateRoot.String(), snapshot.SnapshotStateRoot.String())
	}
}

func TestLoadEpochSnapshotRejectsTamperedRoot(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	validatorSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSeed(0), validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	persisted := persistedEpochSnapshot{Version: persistedEpochSnapshotVersion, Snapshot: snapshot}
	persisted.Snapshot.SnapshotStateRoot = structure.Hash{}
	data, err := json.Marshal(persisted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := db.Put(database.TableCheckpoint, epochSnapshotStateKey(0), data); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, _, err := node.loadEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSeed(0)); err == nil {
		t.Fatal("loadEpochSnapshot() error = nil, want tampered root rejection")
	}
}

func TestRebuildEpochTreatsRuntimeCountersAsSameConsensusSnapshot(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	seed := node.epochSeed(0)
	validatorSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	validators := validatorSet.Validators()
	// This fixture models a multi-validator snapshot; declare its threshold so
	// the mode reconciler does not interpret it as a malformed elder snapshot.
	node.config.ConsensusPolicy.MultiValidatorThreshold = len(validators)
	validators[0].MissedVoteCount++
	persistedSet, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	persistedSnapshot, err := consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, seed, persistedSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	if err := node.persistEpochSnapshot(persistedSnapshot); err != nil {
		t.Fatalf("persistEpochSnapshot() error = %v", err)
	}

	ledgerSnapshot, err := consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, seed, validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot(ledger) error = %v", err)
	}
	if persistedSnapshot.SnapshotStateRoot != ledgerSnapshot.SnapshotStateRoot {
		t.Fatalf("runtime counters changed persisted root from %s to %s", ledgerSnapshot.SnapshotStateRoot.String(), persistedSnapshot.SnapshotStateRoot.String())
	}
	if err := node.persistEpochSnapshot(ledgerSnapshot); err != nil {
		t.Fatalf("persistEpochSnapshot(same consensus inputs) error = %v", err)
	}
	node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	if err := node.rebuildEpoch(0, 1, seed); err != nil {
		t.Fatalf("rebuildEpoch() error = %v", err)
	}
	if node.epochSnapshot.SnapshotStateRoot != persistedSnapshot.SnapshotStateRoot {
		t.Fatalf("rebuilt snapshot root = %s, want persisted %s", node.epochSnapshot.SnapshotStateRoot.String(), persistedSnapshot.SnapshotStateRoot.String())
	}
}
