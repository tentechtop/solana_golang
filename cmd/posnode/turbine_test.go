package main

import (
	"testing"

	"solana_golang/consensus"
	"solana_golang/programs/stake"
	"solana_golang/structure"
)

func TestTurbinePositionForSlotShowsNodeLayer(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 5)
	leaderID, err := schedule.LeaderForSlot(snapshot.StartSlot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	localValidatorID := firstNonLeaderValidatorID(t, snapshot, leaderID)
	node := newTurbinePositionTestNode(keysByValidator[localValidatorID], snapshot, schedule, 2)
	position := node.turbinePositionForSlotLocked(snapshot.StartSlot)
	if !position.TurbineAvailable || !position.ValidatorInTree {
		t.Fatalf("position unavailable: %+v", position)
	}
	if position.Layer < 1 {
		t.Fatalf("position layer = %d, want follower layer >= 1", position.Layer)
	}
	if position.Fanout != 2 {
		t.Fatalf("fanout = %d, want 2", position.Fanout)
	}
	if position.LeaderID == "" || position.LeaderPeerID == "" {
		t.Fatalf("leader missing in position: %+v", position)
	}
	if position.ParentPeerID == "" {
		t.Fatalf("parent peer missing in position: %+v", position)
	}
}

func TestTurbineLeaderTargetsOnlyDirectChildren(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 7)
	leaderID, err := schedule.LeaderForSlot(snapshot.StartSlot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	node := newTurbinePositionTestNode(keysByValidator[leaderID], snapshot, schedule, 2)
	children, position, err := node.turbineChildNodes(snapshot.StartSlot, leaderID)
	if err != nil {
		t.Fatalf("turbineChildNodes() error = %v", err)
	}
	if position.Layer != 0 {
		t.Fatalf("leader layer = %d, want 0", position.Layer)
	}
	if len(children) != 2 {
		t.Fatalf("leader direct children = %d, want 2", len(children))
	}
	for _, child := range children {
		if child.Layer != 1 {
			t.Fatalf("child layer = %d, want 1", child.Layer)
		}
		if child.ParentID != leaderID {
			t.Fatalf("child parent = %s, want %s", child.ParentID, leaderID)
		}
	}
}

func TestTurbineAllNodesComputeSamePropagationTree(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 30)
	slot := snapshot.StartSlot + 5
	leaderID, err := schedule.LeaderForSlot(slot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	referenceTree, err := consensus.NewTurbineTree(snapshot, slot, leaderID, 3)
	if err != nil {
		t.Fatalf("NewTurbineTree() error = %v", err)
	}
	for _, validator := range snapshot.Validators {
		node := newTurbinePositionTestNode(keysByValidator[validator.ValidatorID], snapshot, schedule, 3)
		position := node.turbinePositionForSlotLocked(slot)
		expectedNode, found := referenceTree.NodeByValidator(validator.ValidatorID)
		if !found {
			t.Fatalf("validator %s missing in reference tree", validator.ValidatorID)
		}
		assertTurbinePositionMatchesNode(t, position, expectedNode)
		assertTurbineChildrenMatchReference(t, position, referenceTree.ChildrenOf(validator.ValidatorID))
	}
}

func TestTurbineLayeredPropagationReachesEveryValidatorAcrossSlots(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 30)
	nodesByValidator := newTurbineNodesByValidator(keysByValidator, snapshot, schedule, 3)
	for slot := snapshot.StartSlot; slot <= snapshot.EndSlot; slot++ {
		leaderID, err := schedule.LeaderForSlot(slot)
		if err != nil {
			t.Fatalf("LeaderForSlot(%d) error = %v", slot, err)
		}
		visited := simulateTurbinePropagation(t, nodesByValidator, slot, leaderID)
		if len(visited) != len(snapshot.Validators) {
			t.Fatalf("slot %d visited validators = %d, want %d", slot, len(visited), len(snapshot.Validators))
		}
	}
}

func TestTransactionFastPathPrefersLeadersThenValidators(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 10)
	localValidatorID := snapshot.Validators[0].ValidatorID
	forwardValidators := true
	node := newTurbinePositionTestNode(keysByValidator[localValidatorID], snapshot, schedule, 2)
	node.config.TransactionLeaderForwardSlots = 4
	node.config.TransactionForwardValidators = &forwardValidators

	startSlot := snapshot.StartSlot + 2
	fastPath := node.transactionFastPathForSlotLocked(startSlot, false)
	if !fastPath.FastPathAvailable {
		t.Fatalf("fast path unavailable: %+v", fastPath)
	}
	expectedLeaders := expectedLeaderPeerIDs(t, snapshot, schedule, startSlot, fastPath.ForwardSlots)
	if len(fastPath.LeaderSlots) != fastPath.ForwardSlots+1 {
		t.Fatalf("leader slot count = %d, want %d", len(fastPath.LeaderSlots), fastPath.ForwardSlots+1)
	}
	assertPeerPrefix(t, fastPath.PreferredPeerIDs, expectedLeaders)
	assertPeerSetContains(t, fastPath.PreferredPeerIDs, expectedValidatorPeerIDs(snapshot))
}

func TestTransactionFastPathStableAcrossSlotsForAllNodes(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 30)
	forwardValidators := true
	var referenceBySlot map[uint64][]string
	for _, validator := range snapshot.Validators {
		node := newTurbinePositionTestNode(keysByValidator[validator.ValidatorID], snapshot, schedule, 3)
		node.config.TransactionLeaderForwardSlots = 4
		node.config.TransactionForwardValidators = &forwardValidators
		node.peerKeyPair.peerID = validator.P2PPeerID
		currentBySlot := make(map[uint64][]string)
		for slot := snapshot.StartSlot; slot <= snapshot.EndSlot-4; slot++ {
			fastPath := node.transactionFastPathForSlotLocked(slot, false)
			assertFastPathStableForSlot(t, fastPath, snapshot, schedule, slot)
			currentBySlot[slot] = append([]string(nil), fastPath.PreferredPeerIDs...)
			visiblePath := node.transactionFastPathForSlotLocked(slot, true)
			assertPeerIDAbsent(t, visiblePath.PreferredPeerIDs, validator.P2PPeerID)
		}
		if referenceBySlot == nil {
			referenceBySlot = currentBySlot
			continue
		}
		assertFastPathPeerListsEqual(t, referenceBySlot, currentBySlot, validator.ValidatorID)
	}
}

func TestTransactionFastPathCanExcludeLocalPeer(t *testing.T) {
	snapshot, schedule, keysByValidator := newTurbineTestSnapshotForNode(t, 6)
	localValidator := snapshot.Validators[0]
	forwardValidators := true
	node := newTurbinePositionTestNode(keysByValidator[localValidator.ValidatorID], snapshot, schedule, 2)
	node.config.TransactionLeaderForwardSlots = 3
	node.config.TransactionForwardValidators = &forwardValidators
	node.peerKeyPair.peerID = localValidator.P2PPeerID

	fastPath := node.transactionFastPathForSlotLocked(snapshot.StartSlot, true)
	for _, peerID := range fastPath.PreferredPeerIDs {
		if peerID == localValidator.P2PPeerID {
			t.Fatalf("local peer %s found in fast path: %+v", peerID, fastPath.PreferredPeerIDs)
		}
	}
}

func newTurbineNodesByValidator(keysByValidator map[consensus.ValidatorID]structure.SolanaKeyPair, snapshot consensus.EpochSnapshot, schedule consensus.LeaderSchedule, fanout int) map[consensus.ValidatorID]*posNode {
	nodes := make(map[consensus.ValidatorID]*posNode, len(keysByValidator))
	for validatorID, keyPair := range keysByValidator {
		nodes[validatorID] = newTurbinePositionTestNode(keyPair, snapshot, schedule, fanout)
	}
	return nodes
}

func simulateTurbinePropagation(t *testing.T, nodesByValidator map[consensus.ValidatorID]*posNode, slot uint64, leaderID consensus.ValidatorID) map[consensus.ValidatorID]struct{} {
	t.Helper()
	visited := make(map[consensus.ValidatorID]struct{}, len(nodesByValidator))
	queue := []consensus.ValidatorID{leaderID}
	visited[leaderID] = struct{}{}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		parentNode := nodesByValidator[parentID]
		if parentNode == nil {
			t.Fatalf("parent node %s missing", parentID)
		}
		children, position, err := parentNode.turbineChildNodes(slot, leaderID)
		if err != nil {
			t.Fatalf("turbineChildNodes(slot %d, parent %s) error = %v", slot, parentID, err)
		}
		if position.Layer < 0 {
			t.Fatalf("slot %d parent %s has invalid position %+v", slot, parentID, position)
		}
		for _, child := range children {
			if child.ParentID != parentID {
				t.Fatalf("slot %d child %s parent = %s, want %s", slot, child.ValidatorID, child.ParentID, parentID)
			}
			if _, exists := visited[child.ValidatorID]; exists {
				t.Fatalf("slot %d child %s reached twice", slot, child.ValidatorID)
			}
			visited[child.ValidatorID] = struct{}{}
			queue = append(queue, child.ValidatorID)
		}
	}
	return visited
}

func assertTurbinePositionMatchesNode(t *testing.T, actual turbinePositionJSON, expected consensus.TurbineNode) {
	t.Helper()
	if !actual.TurbineAvailable || !actual.ValidatorInTree {
		t.Fatalf("position unavailable: %+v", actual)
	}
	if actual.Layer != expected.Layer {
		t.Fatalf("layer = %d, want %d", actual.Layer, expected.Layer)
	}
	if actual.ParentValidator != string(expected.ParentID) {
		t.Fatalf("parent validator = %s, want %s", actual.ParentValidator, expected.ParentID)
	}
	if actual.ParentPeerID != expected.ParentPeerID {
		t.Fatalf("parent peer = %s, want %s", actual.ParentPeerID, expected.ParentPeerID)
	}
}

func assertTurbineChildrenMatchReference(t *testing.T, actual turbinePositionJSON, expected []consensus.TurbineNode) {
	t.Helper()
	if len(actual.ChildPeerIDs) != len(expected) {
		t.Fatalf("child count = %d, want %d", len(actual.ChildPeerIDs), len(expected))
	}
	for index, child := range expected {
		if actual.ChildValidators[index] != string(child.ValidatorID) {
			t.Fatalf("child validator %d = %s, want %s", index, actual.ChildValidators[index], child.ValidatorID)
		}
		if actual.ChildPeerIDs[index] != child.P2PPeerID {
			t.Fatalf("child peer %d = %s, want %s", index, actual.ChildPeerIDs[index], child.P2PPeerID)
		}
	}
}

func assertFastPathStableForSlot(t *testing.T, fastPath transactionFastJSON, snapshot consensus.EpochSnapshot, schedule consensus.LeaderSchedule, slot uint64) {
	t.Helper()
	if !fastPath.FastPathAvailable {
		t.Fatalf("slot %d fast path unavailable: %+v", slot, fastPath)
	}
	if fastPath.StartSlot != slot {
		t.Fatalf("start slot = %d, want %d", fastPath.StartSlot, slot)
	}
	expectedLeaders := expectedLeaderPeerIDs(t, snapshot, schedule, slot, fastPath.ForwardSlots)
	assertPeerPrefix(t, fastPath.PreferredPeerIDs, expectedLeaders)
	assertPeerSetContains(t, fastPath.PreferredPeerIDs, expectedValidatorPeerIDs(snapshot))
	assertUniquePeerIDs(t, fastPath.PreferredPeerIDs)
}

func assertFastPathPeerListsEqual(t *testing.T, expectedBySlot map[uint64][]string, actualBySlot map[uint64][]string, validatorID consensus.ValidatorID) {
	t.Helper()
	for slot, expected := range expectedBySlot {
		actual := actualBySlot[slot]
		if len(actual) != len(expected) {
			t.Fatalf("validator %s slot %d peer count = %d, want %d", validatorID, slot, len(actual), len(expected))
		}
		for index := range expected {
			if actual[index] != expected[index] {
				t.Fatalf("validator %s slot %d peer %d = %s, want %s", validatorID, slot, index, actual[index], expected[index])
			}
		}
	}
}

func assertPeerIDAbsent(t *testing.T, peerIDs []string, forbiddenPeerID string) {
	t.Helper()
	for _, peerID := range peerIDs {
		if peerID == forbiddenPeerID {
			t.Fatalf("peer %s must be absent from %+v", forbiddenPeerID, peerIDs)
		}
	}
}

func assertUniquePeerIDs(t *testing.T, peerIDs []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(peerIDs))
	for _, peerID := range peerIDs {
		if _, exists := seen[peerID]; exists {
			t.Fatalf("duplicate peer %s in %+v", peerID, peerIDs)
		}
		seen[peerID] = struct{}{}
	}
}

func expectedLeaderPeerIDs(t *testing.T, snapshot consensus.EpochSnapshot, schedule consensus.LeaderSchedule, startSlot uint64, forwardSlots int) []string {
	t.Helper()
	peerIDs := make([]string, 0, forwardSlots+1)
	for offset := 0; offset <= forwardSlots; offset++ {
		leaderID, err := schedule.LeaderForSlot(startSlot + uint64(offset))
		if err != nil {
			t.Fatalf("LeaderForSlot(%d) error = %v", startSlot+uint64(offset), err)
		}
		leader, exists := snapshot.ValidatorByID(leaderID)
		if !exists {
			t.Fatalf("leader %s missing from snapshot", leaderID)
		}
		peerIDs = append(peerIDs, leader.P2PPeerID)
	}
	return uniquePeerIDs(peerIDs)
}

func expectedValidatorPeerIDs(snapshot consensus.EpochSnapshot) []string {
	peerIDs := make([]string, 0, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
		peerIDs = append(peerIDs, validator.P2PPeerID)
	}
	return peerIDs
}

func assertPeerPrefix(t *testing.T, actual []string, expectedPrefix []string) {
	t.Helper()
	if len(actual) < len(expectedPrefix) {
		t.Fatalf("peer count = %d, want at least %d", len(actual), len(expectedPrefix))
	}
	for index, peerID := range expectedPrefix {
		if actual[index] != peerID {
			t.Fatalf("peer prefix %d = %s, want %s; all peers = %+v", index, actual[index], peerID, actual)
		}
	}
}

func assertPeerSetContains(t *testing.T, actual []string, expected []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(actual))
	for _, peerID := range actual {
		seen[peerID] = struct{}{}
	}
	for _, peerID := range expected {
		if _, exists := seen[peerID]; !exists {
			t.Fatalf("peer %s missing from %+v", peerID, actual)
		}
	}
}

func newTurbineTestSnapshotForNode(t *testing.T, count int) (consensus.EpochSnapshot, consensus.LeaderSchedule, map[consensus.ValidatorID]structure.SolanaKeyPair) {
	t.Helper()
	keysByValidator := make(map[consensus.ValidatorID]structure.SolanaKeyPair, count)
	validators := make([]consensus.ValidatorState, count)
	for index := range validators {
		accountKey := mustStructureKeyPair("cmd-turbine-account-" + string(rune('a'+index)))
		consensusKey := mustStructureKeyPair("cmd-turbine-consensus-" + string(rune('a'+index)))
		validatorID := consensus.NewValidatorID(consensusKey.PublicKey)
		keysByValidator[validatorID] = consensusKey
		validators[index] = consensus.ValidatorState{
			AccountAddress:     accountKey.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          "peer-cmd-turbine-" + string(rune('a'+index)),
			StakeLamports:      stake.MinimumStakeLamports,
			Status:             consensus.ValidatorStatusActive,
		}
	}
	set, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(0, 1, 16, testHashFromText(t, "cmd-turbine-seed"), set)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	schedule, err := consensus.NewLeaderSchedule(snapshot)
	if err != nil {
		t.Fatalf("NewLeaderSchedule() error = %v", err)
	}
	return snapshot, schedule, keysByValidator
}

func newTurbinePositionTestNode(keyPair structure.SolanaKeyPair, snapshot consensus.EpochSnapshot, schedule consensus.LeaderSchedule, fanout int) *posNode {
	return &posNode{
		config: nodeConfig{
			EpochSlots:    16,
			TurbineFanout: fanout,
		},
		consensusKeyPair: keyPair,
		epochSnapshot:    snapshot,
		leaderSchedule:   schedule,
	}
}

func firstNonLeaderValidatorID(t *testing.T, snapshot consensus.EpochSnapshot, leaderID consensus.ValidatorID) consensus.ValidatorID {
	t.Helper()
	for _, validator := range snapshot.Validators {
		if validator.ValidatorID != leaderID {
			return validator.ValidatorID
		}
	}
	t.Fatal("non leader validator not found")
	return ""
}
