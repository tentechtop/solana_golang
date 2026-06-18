package posnode

import (
	"io"
	"log/slog"
	"testing"

	"solana_golang/structure"
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
