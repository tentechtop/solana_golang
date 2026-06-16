package consensus

import (
	"testing"

	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestTurbineTreeLayersAndChildren(t *testing.T) {
	snapshot := newTurbineTestSnapshot(t, 7)
	leaderID := snapshot.Validators[0].ValidatorID
	tree, err := NewTurbineTree(snapshot, snapshot.StartSlot, leaderID, 2)
	if err != nil {
		t.Fatalf("NewTurbineTree() error = %v", err)
	}

	leader, found := tree.NodeByValidator(leaderID)
	if !found {
		t.Fatal("leader not found in turbine tree")
	}
	if leader.Layer != 0 || leader.ParentID != "" {
		t.Fatalf("leader position = %+v, want layer 0 without parent", leader)
	}
	leaderChildren := tree.ChildrenOf(leaderID)
	if len(leaderChildren) != 2 {
		t.Fatalf("leader children = %d, want 2", len(leaderChildren))
	}
	for _, child := range leaderChildren {
		if child.Layer != 1 {
			t.Fatalf("leader child layer = %d, want 1", child.Layer)
		}
		if child.ParentID != leaderID {
			t.Fatalf("child parent = %s, want %s", child.ParentID, leaderID)
		}
	}

	nodes := tree.Nodes()
	for _, node := range nodes {
		if node.ValidatorID == leaderID {
			continue
		}
		if node.Layer <= 0 {
			t.Fatalf("node %s layer = %d, want > 0", node.ValidatorID, node.Layer)
		}
		if node.ParentID == "" || node.ParentPeerID == "" {
			t.Fatalf("node %s parent missing: %+v", node.ValidatorID, node)
		}
	}
}

func TestTurbineTreeIsDeterministic(t *testing.T) {
	snapshot := newTurbineTestSnapshot(t, 9)
	leaderID := snapshot.Validators[3].ValidatorID
	left, err := NewTurbineTree(snapshot, snapshot.StartSlot+3, leaderID, 3)
	if err != nil {
		t.Fatalf("NewTurbineTree(left) error = %v", err)
	}
	right, err := NewTurbineTree(snapshot, snapshot.StartSlot+3, leaderID, 3)
	if err != nil {
		t.Fatalf("NewTurbineTree(right) error = %v", err)
	}
	leftNodes := left.Nodes()
	rightNodes := right.Nodes()
	if len(leftNodes) != len(rightNodes) {
		t.Fatalf("node count differs: %d != %d", len(leftNodes), len(rightNodes))
	}
	for index := range leftNodes {
		if leftNodes[index] != rightNodes[index] {
			t.Fatalf("node %d differs: %+v != %+v", index, leftNodes[index], rightNodes[index])
		}
	}
}

func TestTurbineTreeEveryNodeComputesSameRoute(t *testing.T) {
	snapshot := newTurbineTestSnapshot(t, 30)
	leaderID := snapshot.Validators[11].ValidatorID
	slot := snapshot.StartSlot + 9
	reference, err := NewTurbineTree(snapshot, slot, leaderID, 3)
	if err != nil {
		t.Fatalf("NewTurbineTree(reference) error = %v", err)
	}
	referenceNodes := reference.Nodes()
	for _, localValidator := range snapshot.Validators {
		localTree, err := NewTurbineTree(snapshot, slot, leaderID, 3)
		if err != nil {
			t.Fatalf("NewTurbineTree(local %s) error = %v", localValidator.ValidatorID, err)
		}
		assertTurbineTreeEqual(t, referenceNodes, localTree.Nodes())
		assertTurbineNodeRouteEqual(t, reference, localTree, localValidator.ValidatorID)
	}
	assertTurbineParentsCoverEveryFollowerOnce(t, reference, leaderID)
}

func assertTurbineTreeEqual(t *testing.T, expected []TurbineNode, actual []TurbineNode) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("node count = %d, want %d", len(actual), len(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("node %d = %+v, want %+v", index, actual[index], expected[index])
		}
	}
}

func assertTurbineNodeRouteEqual(t *testing.T, expected TurbineTree, actual TurbineTree, validatorID ValidatorID) {
	t.Helper()
	expectedNode, expectedFound := expected.NodeByValidator(validatorID)
	actualNode, actualFound := actual.NodeByValidator(validatorID)
	if actualFound != expectedFound {
		t.Fatalf("validator %s found = %v, want %v", validatorID, actualFound, expectedFound)
	}
	if actualNode != expectedNode {
		t.Fatalf("validator %s route = %+v, want %+v", validatorID, actualNode, expectedNode)
	}
	expectedChildren := expected.ChildrenOf(validatorID)
	actualChildren := actual.ChildrenOf(validatorID)
	assertTurbineTreeEqual(t, expectedChildren, actualChildren)
}

func assertTurbineParentsCoverEveryFollowerOnce(t *testing.T, tree TurbineTree, leaderID ValidatorID) {
	t.Helper()
	childParentCount := make(map[ValidatorID]int)
	for _, parent := range tree.Nodes() {
		for _, child := range tree.ChildrenOf(parent.ValidatorID) {
			childParentCount[child.ValidatorID]++
			if child.ParentID != parent.ValidatorID {
				t.Fatalf("child %s parent = %s, want %s", child.ValidatorID, child.ParentID, parent.ValidatorID)
			}
		}
	}
	for _, node := range tree.Nodes() {
		if node.ValidatorID == leaderID {
			continue
		}
		if childParentCount[node.ValidatorID] != 1 {
			t.Fatalf("validator %s parent count = %d, want 1", node.ValidatorID, childParentCount[node.ValidatorID])
		}
	}
}

func newTurbineTestSnapshot(t *testing.T, count int) EpochSnapshot {
	t.Helper()
	validators := make([]ValidatorState, count)
	for index := range validators {
		consensusKey := turbineTestKeyPair(t, byte(index+1))
		accountKey := turbineTestKeyPair(t, byte(index+31))
		validators[index] = ValidatorState{
			AccountAddress:     accountKey.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          "peer-turbine-" + utils.BytesToHex([]byte{byte(index)}),
			StakeLamports:      stake.MinimumStakeLamports,
			Status:             ValidatorStatusActive,
		}
	}
	set, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot, err := NewEpochSnapshot(0, 1, 32, turbineTestHash(t, "turbine-seed"), set)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	return snapshot
}

func turbineTestKeyPair(t *testing.T, seed byte) structure.SolanaKeyPair {
	t.Helper()
	value := make([]byte, structure.SolanaPrivateKeySeedSize)
	for index := range value {
		value[index] = seed + byte(index)
	}
	keyPair, err := structure.KeyPairFromSeed(value)
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	return keyPair
}

func turbineTestHash(t *testing.T, text string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(text)))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}
