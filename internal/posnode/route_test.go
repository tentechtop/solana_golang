package posnode

import "testing"

func TestTransactionRouteStopsAtMaxHops(t *testing.T) {
	route := transactionRouteEnvelope{OriginPeerID: "peer-origin", HopCount: 1, MaxHops: 2}
	next, ok := route.nextHop("peer-local")
	if !ok {
		t.Fatal("nextHop() ok = false, want true")
	}
	if next.HopCount != 2 {
		t.Fatalf("HopCount = %d, want 2", next.HopCount)
	}
	if _, ok := next.nextHop("peer-local"); ok {
		t.Fatal("nextHop() ok = true after max hops")
	}
}

func TestVoteRouteDefaultsOriginAndHops(t *testing.T) {
	route := voteRouteEnvelope{}
	next, ok := route.nextHop("peer-local")
	if !ok {
		t.Fatal("nextHop() ok = false, want true")
	}
	if next.OriginPeerID != "peer-local" {
		t.Fatalf("OriginPeerID = %q, want peer-local", next.OriginPeerID)
	}
	if next.HopCount != 1 {
		t.Fatalf("HopCount = %d, want 1", next.HopCount)
	}
	if next.MaxHops != defaultVoteMaxHops {
		t.Fatalf("MaxHops = %d, want %d", next.MaxHops, defaultVoteMaxHops)
	}
}

func TestPrioritizePeerIDsMovesLeadersToFront(t *testing.T) {
	connectedPeerIDs := []string{"peer-c", "peer-a", "peer-b", "peer-d"}
	leaderPeerIDs := []string{"peer-b", "peer-x", "peer-a", "peer-b"}

	got := prioritizePeerIDs(connectedPeerIDs, leaderPeerIDs)
	want := []string{"peer-b", "peer-a", "peer-c", "peer-d"}
	if len(got) != len(want) {
		t.Fatalf("peer count = %d, want %d: %+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("peer %d = %s, want %s; all peers = %+v", index, got[index], want[index], got)
		}
	}
}
