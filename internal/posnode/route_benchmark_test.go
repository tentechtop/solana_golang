package posnode

import (
	"context"
	"testing"

	"solana_golang/consensus"
)

func BenchmarkTransactionFastPathForSlot(b *testing.B) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(b, 128)
	localValidator := snapshot.Validators[0]
	forwardValidators := true
	node := newTurbinePositionTestNode(keysByValidator[localValidator.ValidatorID], snapshot, schedule, 4)
	node.config.TransactionLeaderForwardSlots = 4
	node.config.TransactionForwardValidators = &forwardValidators
	node.peerKeyPair.peerID = localValidator.P2PPeerID

	slotWindow := int(snapshot.EndSlot - snapshot.StartSlot - uint64(node.config.TransactionLeaderForwardSlots))
	if slotWindow <= 0 {
		b.Fatalf("slot window = %d, want positive", slotWindow)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		slot := snapshot.StartSlot + uint64(index%slotWindow)
		fastPath := node.transactionFastPathForSlotLocked(slot, true)
		if !fastPath.FastPathAvailable {
			b.Fatalf("fast path unavailable for slot %d", slot)
		}
	}
}

func BenchmarkVoteRouteTargets(b *testing.B) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(b, 128)
	slot := snapshot.StartSlot
	leaderID, err := schedule.LeaderForSlot(slot)
	if err != nil {
		b.Fatalf("LeaderForSlot() error = %v", err)
	}
	tree, err := consensus.NewTurbineTree(snapshot, slot, leaderID, 4)
	if err != nil {
		b.Fatalf("NewTurbineTree() error = %v", err)
	}

	localNode := consensus.TurbineNode{}
	for _, candidate := range tree.Nodes() {
		if candidate.Layer >= 2 {
			localNode = candidate
			break
		}
	}
	if localNode.ValidatorID == "" {
		b.Fatal("layer-2 validator not found")
	}

	node := newTurbinePositionTestNode(keysByValidator[localNode.ValidatorID], snapshot, schedule, 4)
	node.peerKeyPair.peerID = localNode.P2PPeerID
	vote := consensus.Vote{Slot: slot}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		targets := node.voteRouteTargets(ctx, vote, "", node.peerKeyPair.peerID)
		if len(targets) < 2 {
			b.Fatalf("target count = %d, want at least 2", len(targets))
		}
	}
}

func BenchmarkTurbineChildNodes(b *testing.B) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(b, 128)
	slot := snapshot.StartSlot
	leaderID, err := schedule.LeaderForSlot(slot)
	if err != nil {
		b.Fatalf("LeaderForSlot() error = %v", err)
	}
	node := newTurbinePositionTestNode(keysByValidator[leaderID], snapshot, schedule, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		children, position, err := node.turbineChildNodes(slot, leaderID)
		if err != nil {
			b.Fatalf("turbineChildNodes() error = %v", err)
		}
		if position.Layer != 0 || len(children) == 0 {
			b.Fatalf("invalid leader position %+v children=%d", position, len(children))
		}
	}
}
