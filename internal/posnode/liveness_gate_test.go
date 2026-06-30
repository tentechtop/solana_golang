package posnode

import (
	"testing"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
)

func TestLivenessGateWaitsWhenReachableStakeBelowQuorum(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{40, 30, 30})
	gate := buildLivenessGateFromSnapshot(
		snapshot,
		snapshot.Validators[0].ValidatorID,
		map[string]struct{}{},
		now,
		3*time.Second,
		true,
	)

	if gate.State != livenessGateStateDegraded {
		t.Fatalf("state = %q, want degraded", gate.State)
	}
	if gate.Mode != livenessGateModeWaitingQuorum {
		t.Fatalf("mode = %q, want waiting_quorum", gate.Mode)
	}
	if gate.ProductionEnabled || gate.UserTransactionPackagingEnabled {
		t.Fatalf("production gate = %+v, want disabled production and packaging", gate)
	}
	if gate.ReachableStakeLamports != 40 || gate.RequiredStakeLamports != 67 {
		t.Fatalf("stake = reachable %d required %d, want 40/67", gate.ReachableStakeLamports, gate.RequiredStakeLamports)
	}
}

func TestLivenessGateRecoversWhenReachableStakeMeetsQuorum(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{40, 30, 30})
	reachablePeerIDs := map[string]struct{}{
		snapshot.Validators[1].P2PPeerID: {},
	}
	gate := buildLivenessGateFromSnapshot(
		snapshot,
		snapshot.Validators[0].ValidatorID,
		reachablePeerIDs,
		now,
		3*time.Second,
		true,
	)

	if gate.State != livenessGateStateReady || gate.Mode != livenessGateModeProducing {
		t.Fatalf("gate state = %s/%s, want ready/producing", gate.State, gate.Mode)
	}
	if !gate.ProductionEnabled || !gate.UserTransactionPackagingEnabled {
		t.Fatalf("production gate = %+v, want enabled production and packaging", gate)
	}
	if gate.ReachableStakeLamports != 70 || gate.ReachableValidatorCount != 2 {
		t.Fatalf("reachable = %d/%d, want 70 stake and 2 validators", gate.ReachableStakeLamports, gate.ReachableValidatorCount)
	}
}

func TestLivenessGateUsesCeilTwoThirdsThreshold(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{1, 1, 1})
	localValidatorID := snapshot.Validators[0].ValidatorID

	waitingGate := buildLivenessGateFromSnapshot(snapshot, localValidatorID, nil, now, 3*time.Second, true)
	if waitingGate.RequiredStakeLamports != 2 {
		t.Fatalf("required stake = %d, want 2", waitingGate.RequiredStakeLamports)
	}
	if waitingGate.QuorumReady {
		t.Fatal("single stake is quorum ready, want waiting quorum")
	}

	readyGate := buildLivenessGateFromSnapshot(
		snapshot,
		localValidatorID,
		map[string]struct{}{snapshot.Validators[1].P2PPeerID: {}},
		now,
		3*time.Second,
		true,
	)
	if !readyGate.QuorumReady {
		t.Fatal("two of three stake is not quorum ready")
	}
}

func TestConnectionRecentlyReachableUsesLatestActivity(t *testing.T) {
	now := time.UnixMilli(1_710_000_010_000)
	connectedState := p2p.ConnectionState{
		ConnectedAtUnixMilli:   now.Add(-time.Minute).UnixMilli(),
		LastReadUnixMilli:      now.Add(-time.Minute).UnixMilli(),
		LastWriteUnixMilli:     now.Add(-time.Minute).UnixMilli(),
		LastHeartbeatUnixMilli: now.Add(-time.Second).UnixMilli(),
	}
	if !connectionRecentlyReachable(connectedState, now, 3*time.Second) {
		t.Fatal("connected peer marked unreachable")
	}

	recentState := p2p.ConnectionState{
		LastHeartbeatUnixMilli: now.Add(-time.Second).UnixMilli(),
	}
	if !connectionRecentlyReachable(recentState, now, 3*time.Second) {
		t.Fatal("recent heartbeat marked unreachable")
	}

	staleActivityState := p2p.ConnectionState{
		LastReadUnixMilli:  now.Add(-10 * time.Second).UnixMilli(),
		LastWriteUnixMilli: now.Add(-10 * time.Second).UnixMilli(),
	}
	if connectionRecentlyReachable(staleActivityState, now, 3*time.Second) {
		t.Fatal("stale activity without current connection marked reachable")
	}
}

func livenessTestSnapshot(stakes []uint64) consensus.EpochSnapshot {
	validators := make([]consensus.ValidatorState, 0, len(stakes))
	totalStake := uint64(0)
	for index, stakeLamports := range stakes {
		validatorID := consensus.ValidatorID("validator-" + string(rune('a'+index)))
		validators = append(validators, consensus.ValidatorState{
			ValidatorID:   validatorID,
			P2PPeerID:     "peer-" + string(rune('a'+index)),
			StakeLamports: stakeLamports,
		})
		totalStake += stakeLamports
	}
	return consensus.EpochSnapshot{
		EpochID:          1,
		StartSlot:        1,
		EndSlot:          16,
		TotalActiveStake: totalStake,
		Validators:       validators,
	}
}
