package shielded

import (
	"sync"
	"testing"
)

func TestIncrementalMerkleTreeRootAndWitnesses(t *testing.T) {
	const depth = 4
	tree, err := NewIncrementalMerkleTree(depth)
	if err != nil {
		t.Fatalf("NewIncrementalMerkleTree() error = %v", err)
	}
	if tree.Depth() != depth || tree.Capacity() != 1<<depth || tree.LeafCount() != 0 {
		t.Fatalf("empty tree metadata = depth %d capacity %d count %d", tree.Depth(), tree.Capacity(), tree.LeafCount())
	}
	if tree.Root() != naiveMerkleRoot(depth, nil) {
		t.Fatal("空树根与固定深度零填充结果不一致")
	}

	commitments := make([]Commitment, 0, 7)
	previousRoot := tree.Root()
	for commitmentIndex := uint64(0); commitmentIndex < 7; commitmentIndex++ {
		commitment := digestFromUint64(100 + commitmentIndex)
		position, err := tree.Append(commitment)
		if err != nil {
			t.Fatalf("Append(%d) error = %v", commitmentIndex, err)
		}
		if position != commitmentIndex {
			t.Fatalf("Append() position = %d, want %d", position, commitmentIndex)
		}
		commitments = append(commitments, commitment)
		root := tree.Root()
		if root == previousRoot {
			t.Fatalf("append %d did not change root", commitmentIndex)
		}
		if expectedRoot := naiveMerkleRoot(depth, commitments); root != expectedRoot {
			t.Fatalf("root after append %d = %s, want %s", commitmentIndex, root, expectedRoot)
		}
		previousRoot = root
	}

	root := tree.Root()
	for position, commitment := range commitments {
		witness, err := tree.Witness(uint64(position))
		if err != nil {
			t.Fatalf("Witness(%d) error = %v", position, err)
		}
		if witness.Depth != depth || witness.Position != uint64(position) || len(witness.Siblings) != depth {
			t.Fatalf("Witness(%d) metadata invalid: %+v", position, witness)
		}
		computedRoot, err := witness.ComputeRoot(commitment)
		if err != nil {
			t.Fatalf("ComputeRoot(%d) error = %v", position, err)
		}
		if computedRoot != root || !VerifyMerklePath(commitment, witness, root) {
			t.Fatalf("Witness(%d) did not verify current root", position)
		}
	}
	if _, err := tree.Witness(tree.LeafCount()); err == nil {
		t.Fatal("Witness() 接受尚未追加的叶子位置")
	}
}

func TestMerkleWitnessRejectsTampering(t *testing.T) {
	tree, err := NewIncrementalMerkleTree(4)
	if err != nil {
		t.Fatalf("NewIncrementalMerkleTree() error = %v", err)
	}
	commitments := []Commitment{
		digestFromUint64(201),
		digestFromUint64(202),
		digestFromUint64(203),
	}
	for _, commitment := range commitments {
		if _, err := tree.Append(commitment); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	witness, err := tree.Witness(1)
	if err != nil {
		t.Fatalf("Witness() error = %v", err)
	}
	root := tree.Root()
	if !witness.Verify(commitments[1], root) {
		t.Fatal("原始 witness 验证失败")
	}
	if witness.Verify(digestFromUint64(999), root) {
		t.Fatal("witness 接受被替换的 commitment")
	}
	tamperedRoot := root
	tamperedRoot[FieldElementSize-1] ^= 0x01
	if witness.Verify(commitments[1], tamperedRoot) {
		t.Fatal("witness 接受被篡改的 root")
	}
	tamperedSibling := witness
	tamperedSibling.Siblings = append([]Digest(nil), witness.Siblings...)
	tamperedSibling.Siblings[0] = digestFromUint64(777)
	if tamperedSibling.Verify(commitments[1], root) {
		t.Fatal("witness 接受被篡改的 sibling")
	}
	tamperedPosition := witness
	tamperedPosition.Position ^= 1
	if tamperedPosition.Verify(commitments[1], root) {
		t.Fatal("witness 接受被篡改的 position")
	}
	wrongDepth := witness
	wrongDepth.Depth++
	if wrongDepth.Verify(commitments[1], root) {
		t.Fatal("witness 接受不一致深度")
	}
	nonCanonicalSibling := witness
	nonCanonicalSibling.Siblings = append([]Digest(nil), witness.Siblings...)
	for index := range nonCanonicalSibling.Siblings[0] {
		nonCanonicalSibling.Siblings[0][index] = 0xff
	}
	if _, err := nonCanonicalSibling.ComputeRoot(commitments[1]); err == nil {
		t.Fatal("ComputeRoot() 接受非规范域 sibling")
	}
}

func TestIncrementalMerkleTreeCapacityAndValidation(t *testing.T) {
	if _, err := NewIncrementalMerkleTree(MinMerkleDepth - 1); err == nil {
		t.Fatal("NewIncrementalMerkleTree() 接受过小深度")
	}
	if _, err := NewIncrementalMerkleTree(MaxMerkleDepth + 1); err == nil {
		t.Fatal("NewIncrementalMerkleTree() 接受过大深度")
	}
	circuitTree, err := NewCircuitMerkleTree()
	if err != nil {
		t.Fatalf("NewCircuitMerkleTree() error = %v", err)
	}
	if circuitTree.Depth() != CircuitMerkleDepth {
		t.Fatalf("circuit tree depth = %d, want %d", circuitTree.Depth(), CircuitMerkleDepth)
	}

	tree, err := NewIncrementalMerkleTree(2)
	if err != nil {
		t.Fatalf("NewIncrementalMerkleTree() error = %v", err)
	}
	commitments := make([]Commitment, 4)
	for index := range commitments {
		commitments[index] = digestFromUint64(uint64(300 + index))
		if _, err := tree.Append(commitments[index]); err != nil {
			t.Fatalf("Append(%d) error = %v", index, err)
		}
	}
	if tree.LeafCount() != tree.Capacity() || tree.Root() != naiveMerkleRoot(2, commitments) {
		t.Fatal("满树根或叶子计数错误")
	}
	for position, commitment := range commitments {
		witness, err := tree.Witness(uint64(position))
		if err != nil || !witness.Verify(commitment, tree.Root()) {
			t.Fatalf("full tree witness %d failed: %v", position, err)
		}
	}
	if _, err := tree.Append(digestFromUint64(999)); err == nil {
		t.Fatal("Append() 接受超出固定容量的叶子")
	}
	var nonCanonical Commitment
	for index := range nonCanonical {
		nonCanonical[index] = 0xff
	}
	otherTree, err := NewIncrementalMerkleTree(2)
	if err != nil {
		t.Fatalf("NewIncrementalMerkleTree() other error = %v", err)
	}
	if _, err := otherTree.Append(nonCanonical); err == nil {
		t.Fatal("Append() 接受非规范域 commitment")
	}
	if _, err := otherTree.Append(Commitment{}); err == nil {
		t.Fatal("Append() 接受零 commitment")
	}
}

func TestIncrementalMerkleTreeConcurrentAppend(t *testing.T) {
	const appendCount = 64
	tree, err := NewIncrementalMerkleTree(8)
	if err != nil {
		t.Fatalf("NewIncrementalMerkleTree() error = %v", err)
	}
	commitmentsByPosition := make([]Commitment, appendCount)
	errors := make(chan error, appendCount)
	var resultMutex sync.Mutex
	var waitGroup sync.WaitGroup
	for index := 0; index < appendCount; index++ {
		commitment := digestFromUint64(uint64(1_000 + index))
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			position, appendErr := tree.Append(commitment)
			if appendErr != nil {
				errors <- appendErr
				return
			}
			resultMutex.Lock()
			commitmentsByPosition[position] = commitment
			resultMutex.Unlock()
		}()
	}
	waitGroup.Wait()
	close(errors)
	for appendErr := range errors {
		t.Fatalf("concurrent Append() error = %v", appendErr)
	}
	if tree.LeafCount() != appendCount {
		t.Fatalf("leaf count = %d, want %d", tree.LeafCount(), appendCount)
	}
	root := tree.Root()
	for position, commitment := range commitmentsByPosition {
		witness, err := tree.Witness(uint64(position))
		if err != nil || !witness.Verify(commitment, root) {
			t.Fatalf("concurrent witness %d failed: %v", position, err)
		}
	}
}

func naiveMerkleRoot(depth int, commitments []Commitment) MerkleRoot {
	capacity := 1 << depth
	currentLevel := make([]Digest, capacity)
	copy(currentLevel, commitments)
	for level := 0; level < depth; level++ {
		nextLevel := make([]Digest, len(currentLevel)/2)
		for index := range nextLevel {
			nextLevel[index] = hashMerkleNode(level, currentLevel[index*2], currentLevel[index*2+1])
		}
		currentLevel = nextLevel
	}
	return currentLevel[0]
}
