package posnode

import (
	"io"
	"log/slog"
	"testing"

	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestRecordCommittedBlockhashKeepsRecentWindowFresh(t *testing.T) {
	node := &posNode{
		blockhashQueue: structure.NewBlockhashQueue(2),
		logger:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	oldHash := mustHash("old-blockhash")
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     oldHash,
		Slot:          1,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("add old blockhash: %v", err)
	}

	newHash := mustHash("new-blockhash")
	node.recordCommittedBlockhash(4, newHash)

	if !node.blockhashQueue.IsRecent(newHash, 4) {
		t.Fatal("new committed blockhash is not recent")
	}
	if node.blockhashQueue.IsRecent(oldHash, 4) {
		t.Fatal("old blockhash stayed recent after trim")
	}
}

func TestRestoreRecentBlockhashQueueLoadsPersistedMainChainHistory(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	firstHead := node.ledger.Head()
	commitConsensusStatusState(t, node.ledger, node.ledger.State())
	secondHead := node.ledger.Head()

	queue, err := restoreRecentBlockhashQueue(node.ledger)
	if err != nil {
		t.Fatalf("restoreRecentBlockhashQueue() error = %v", err)
	}
	if !queue.IsRecent(firstHead.BlockHash, secondHead.Slot) {
		t.Fatal("persisted previous blockhash was not restored")
	}
	if !queue.IsRecent(secondHead.BlockHash, secondHead.Slot) {
		t.Fatal("persisted head blockhash was not restored")
	}
}
