package posnode

import (
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/p2p"
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

func TestLivenessGateRequiresStakeStrictlyAboveTwoThirds(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{1, 1, 1})
	localValidatorID := snapshot.Validators[0].ValidatorID

	twoThirdsGate := buildLivenessGateFromSnapshot(
		snapshot,
		localValidatorID,
		map[string]struct{}{snapshot.Validators[1].P2PPeerID: {}},
		now,
		3*time.Second,
		true,
	)
	if twoThirdsGate.RequiredStakeLamports != 3 {
		t.Fatalf("required stake = %d, want 3", twoThirdsGate.RequiredStakeLamports)
	}
	if twoThirdsGate.QuorumReady {
		t.Fatal("exactly two thirds stake is quorum ready, want strict supermajority")
	}

	readyGate := buildLivenessGateFromSnapshot(
		snapshot,
		localValidatorID,
		map[string]struct{}{
			snapshot.Validators[1].P2PPeerID: {},
			snapshot.Validators[2].P2PPeerID: {},
		},
		now,
		3*time.Second,
		true,
	)
	if !readyGate.QuorumReady {
		t.Fatal("stake above two thirds is not quorum ready")
	}
}

func TestLivenessGateAllowsOneOnlineValidatorWithStakeSupermajority(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{70, 20, 10})

	gate := buildLivenessGateFromSnapshot(
		snapshot,
		snapshot.Validators[0].ValidatorID,
		nil,
		now,
		3*time.Second,
		true,
	)

	if !gate.ProductionEnabled || !gate.QuorumReady {
		t.Fatalf("gate = %+v, want production enabled for one online validator with 70%% stake", gate)
	}
	if gate.ReachableValidatorCount != 1 || gate.ReachableStakeLamports != 70 || gate.RequiredStakeLamports != 67 {
		t.Fatalf("reachable validators/stake/required = %d/%d/%d, want 1/70/67",
			gate.ReachableValidatorCount,
			gate.ReachableStakeLamports,
			gate.RequiredStakeLamports,
		)
	}
}

func TestLivenessGateEightValidatorsFaultRecoveryTwice(t *testing.T) {
	now := time.UnixMilli(1_710_000_000_000)
	snapshot := livenessTestSnapshot([]uint64{1, 1, 1, 1, 1, 1, 1, 1})
	localValidatorID := snapshot.Validators[0].ValidatorID

	assertGate := func(name string, peerIndexes []int, wantReady bool, wantReachable int) {
		t.Helper()
		reachablePeerIDs := make(map[string]struct{}, len(peerIndexes))
		for _, index := range peerIndexes {
			reachablePeerIDs[snapshot.Validators[index].P2PPeerID] = struct{}{}
		}
		gate := buildLivenessGateFromSnapshot(
			snapshot,
			localValidatorID,
			reachablePeerIDs,
			now,
			3*time.Second,
			true,
		)
		if gate.QuorumReady != wantReady || gate.ProductionEnabled != wantReady {
			t.Fatalf("%s gate = %+v, want ready=%t", name, gate, wantReady)
		}
		if gate.ReachableValidatorCount != wantReachable || gate.RequiredStakeLamports != 6 {
			t.Fatalf("%s reachable/required = %d/%d, want %d/6",
				name,
				gate.ReachableValidatorCount,
				gate.RequiredStakeLamports,
				wantReachable,
			)
		}
	}

	assertGate("healthy", []int{1, 2, 3, 4, 5, 6, 7}, true, 8)
	assertGate("first fault", []int{1, 2, 3, 4}, false, 5)
	assertGate("first recovery", []int{1, 2, 3, 4, 5, 6, 7}, true, 8)
	assertGate("second fault", []int{3, 4, 5, 6}, false, 5)
	assertGate("second recovery", []int{1, 2, 3, 4, 5, 6, 7}, true, 8)
}

func TestLivenessQuorumStabilizationResetsWhenReachableSetChanges(t *testing.T) {
	node := &posNode{config: nodeConfig{SlotMillis: 500}}
	startedAt := time.UnixMilli(1_710_000_000_000)
	readyGate := livenessGateJSON{
		State:                           livenessGateStateReady,
		Mode:                            livenessGateModeProducing,
		Reason:                          livenessGateReasonReachableStakeQuorum,
		Applicable:                      true,
		QuorumReady:                     true,
		ProductionEnabled:               true,
		UserTransactionPackagingEnabled: true,
		ReachableValidatorIDs:           []string{"validator-a", "validator-b", "validator-c"},
	}

	stabilizing := node.applyLivenessQuorumStabilizationLocked(readyGate, startedAt)
	if stabilizing.QuorumReady || stabilizing.ProductionEnabled || stabilizing.Reason != livenessGateReasonQuorumStabilizing {
		t.Fatalf("first ready observation = %+v, want stabilizing", stabilizing)
	}
	if got := node.applyLivenessQuorumStabilizationLocked(readyGate, startedAt.Add(2999*time.Millisecond)); got.QuorumReady {
		t.Fatal("quorum became ready before the 3 second stabilization window")
	}
	if got := node.applyLivenessQuorumStabilizationLocked(readyGate, startedAt.Add(3*time.Second)); !got.QuorumReady {
		t.Fatalf("stable quorum = %+v, want ready", got)
	}

	node.livenessGate = readyGate
	changedGate := readyGate
	changedGate.ReachableValidatorIDs = []string{"validator-a", "validator-b", "validator-d"}
	if got := node.applyLivenessQuorumStabilizationLocked(changedGate, startedAt.Add(4*time.Second)); !got.QuorumReady {
		t.Fatalf("membership churn with continuous quorum = %+v, want ready", got)
	}

	belowQuorum := changedGate
	belowQuorum.QuorumReady = false
	belowQuorum.ProductionEnabled = false
	node.livenessGate = belowQuorum
	node.applyLivenessQuorumStabilizationLocked(belowQuorum, startedAt.Add(5*time.Second))
	if got := node.applyLivenessQuorumStabilizationLocked(changedGate, startedAt.Add(6*time.Second)); got.QuorumReady {
		t.Fatal("quorum recovery skipped stabilization after a real outage")
	}
	if got := node.applyLivenessQuorumStabilizationLocked(changedGate, startedAt.Add(9*time.Second)); !got.QuorumReady {
		t.Fatalf("recovered stable quorum = %+v, want ready", got)
	}
}

func TestLivenessRequiredStakeHandlesBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		totalStake uint64
		want       uint64
	}{
		{name: "one", totalStake: 1, want: 1},
		{name: "divisible by three", totalStake: 3, want: 3},
		{name: "remainder one", totalStake: 4, want: 3},
		{name: "one hundred", totalStake: 100, want: 67},
		{name: "maximum uint64", totalStake: ^uint64(0), want: 12_297_829_382_473_034_411},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := livenessRequiredStake(testCase.totalStake)
			if err != nil {
				t.Fatalf("livenessRequiredStake() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("livenessRequiredStake() = %d, want %d", got, testCase.want)
			}
		})
	}

	if _, err := livenessRequiredStake(0); err == nil {
		t.Fatal("livenessRequiredStake(0) error = nil, want invalid quorum error")
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
