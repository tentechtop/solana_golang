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
