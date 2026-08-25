package shielded

import (
	"fmt"
	"sync"
)

const (
	// MinMerkleDepth 是屏蔽承诺树允许的最小固定深度。
	MinMerkleDepth = 1
	// MaxMerkleDepth 避免 uint64 叶子位置和容量计算溢出。
	MaxMerkleDepth = 63
)

// MerkleRoot 是固定深度票据承诺树的根摘要。
type MerkleRoot = Digest

// MerkleWitness 保存从指定叶子到固定深度树根的认证路径。
type MerkleWitness struct {
	Depth    int
	Position uint64
	Siblings []Digest
}

// IncrementalMerkleTree 按追加顺序维护固定深度票据承诺树。
//
// 追加和读取方法由内部读写锁保护。frontier 用于 O(depth) 计算当前根，
// levels 保留已完成子树，使钱包能够为历史叶子创建认证路径。
type IncrementalMerkleTree struct {
	mutex      sync.RWMutex
	depth      int
	leafCount  uint64
	frontier   []Digest
	emptyRoots []Digest
	levels     [][]Digest
}

// NewIncrementalMerkleTree 创建指定固定深度的空承诺树。
func NewIncrementalMerkleTree(depth int) (*IncrementalMerkleTree, error) {
	if depth < MinMerkleDepth || depth > MaxMerkleDepth {
		return nil, fmt.Errorf("shielded: merkle depth must be in [%d, %d], got %d", MinMerkleDepth, MaxMerkleDepth, depth)
	}

	tree := &IncrementalMerkleTree{
		depth:      depth,
		frontier:   make([]Digest, depth+1),
		emptyRoots: make([]Digest, depth+1),
		levels:     make([][]Digest, depth+1),
	}
	tree.emptyRoots[0] = hashMerkleEmptyLeaf()
	for level := 0; level < depth; level++ {
		tree.emptyRoots[level+1] = hashMerkleNode(level, tree.emptyRoots[level], tree.emptyRoots[level])
	}
	return tree, nil
}

// NewCircuitMerkleTree 创建与 V2 证明电路深度完全一致的空承诺树。
func NewCircuitMerkleTree() (*IncrementalMerkleTree, error) {
	return NewIncrementalMerkleTree(CircuitMerkleDepth)
}

// Depth 返回承诺树的固定深度。
func (tree *IncrementalMerkleTree) Depth() int {
	if tree == nil {
		return 0
	}
	return tree.depth
}

// Capacity 返回承诺树能够容纳的最大叶子数量。
func (tree *IncrementalMerkleTree) Capacity() uint64 {
	if tree == nil || tree.depth < MinMerkleDepth || tree.depth > MaxMerkleDepth {
		return 0
	}
	return uint64(1) << tree.depth
}

// LeafCount 返回已经追加的承诺数量。
func (tree *IncrementalMerkleTree) LeafCount() uint64 {
	if tree == nil {
		return 0
	}
	tree.mutex.RLock()
	defer tree.mutex.RUnlock()
	return tree.leafCount
}

// Append 将票据承诺追加到下一叶子位置并返回该位置。
func (tree *IncrementalMerkleTree) Append(commitment Commitment) (uint64, error) {
	if tree == nil {
		return 0, fmt.Errorf("shielded: merkle tree is nil")
	}
	tree.mutex.Lock()
	defer tree.mutex.Unlock()
	if commitment.IsZero() {
		return 0, fmt.Errorf("shielded: commitment cannot be zero")
	}
	if err := validateCanonicalField(commitment[:]); err != nil {
		return 0, fmt.Errorf("shielded: append invalid commitment: %w", err)
	}

	capacity := uint64(1) << tree.depth
	if tree.leafCount >= capacity {
		return 0, fmt.Errorf("shielded: merkle tree is full")
	}

	position := tree.leafCount
	leafHash := hashMerkleLeaf(commitment)
	tree.levels[0] = append(tree.levels[0], leafHash)
	current := leafHash
	for level := 0; level < tree.depth; level++ {
		indexAtLevel := position >> level
		if indexAtLevel&1 == 0 {
			tree.frontier[level] = current
			break
		}

		levelNodes := tree.levels[level]
		leftIndex := int(indexAtLevel - 1)
		if leftIndex < 0 || leftIndex >= len(levelNodes) {
			return 0, fmt.Errorf("shielded: merkle frontier invariant failed at level %d", level)
		}
		current = hashMerkleNode(level, levelNodes[leftIndex], current)
		tree.levels[level+1] = append(tree.levels[level+1], current)
		tree.frontier[level] = Digest{}
		if level == tree.depth-1 {
			tree.frontier[tree.depth] = current
		}
	}
	tree.leafCount++
	return position, nil
}

// Root 使用当前 frontier 计算包含空叶子填充的固定深度树根。
func (tree *IncrementalMerkleTree) Root() MerkleRoot {
	if tree == nil {
		return MerkleRoot{}
	}
	tree.mutex.RLock()
	defer tree.mutex.RUnlock()
	return tree.rootLocked()
}

// Witness 为已经追加的叶子创建认证路径。
func (tree *IncrementalMerkleTree) Witness(position uint64) (MerkleWitness, error) {
	if tree == nil {
		return MerkleWitness{}, fmt.Errorf("shielded: merkle tree is nil")
	}
	tree.mutex.RLock()
	defer tree.mutex.RUnlock()
	if position >= tree.leafCount {
		return MerkleWitness{}, fmt.Errorf("shielded: merkle position %d is outside leaf count %d", position, tree.leafCount)
	}

	witness := MerkleWitness{
		Depth:    tree.depth,
		Position: position,
		Siblings: make([]Digest, tree.depth),
	}
	for level := 0; level < tree.depth; level++ {
		indexAtLevel := position >> level
		siblingIndex := indexAtLevel ^ 1
		siblingStart := siblingIndex << level
		witness.Siblings[level] = tree.subtreeRootLocked(siblingStart, level)
	}
	return witness, nil
}

// ComputeRoot 从承诺和认证路径重建固定深度树根。
func (witness MerkleWitness) ComputeRoot(commitment Commitment) (MerkleRoot, error) {
	if witness.Depth < MinMerkleDepth || witness.Depth > MaxMerkleDepth {
		return MerkleRoot{}, fmt.Errorf("shielded: invalid witness depth %d", witness.Depth)
	}
	if len(witness.Siblings) != witness.Depth {
		return MerkleRoot{}, fmt.Errorf("shielded: witness requires %d siblings, got %d", witness.Depth, len(witness.Siblings))
	}
	capacity := uint64(1) << witness.Depth
	if witness.Position >= capacity {
		return MerkleRoot{}, fmt.Errorf("shielded: witness position %d exceeds capacity %d", witness.Position, capacity)
	}
	if commitment.IsZero() {
		return MerkleRoot{}, fmt.Errorf("shielded: witness commitment cannot be zero")
	}
	if err := validateCanonicalField(commitment[:]); err != nil {
		return MerkleRoot{}, fmt.Errorf("shielded: invalid witness commitment: %w", err)
	}
	for siblingIndex, sibling := range witness.Siblings {
		if err := validateCanonicalField(sibling[:]); err != nil {
			return MerkleRoot{}, fmt.Errorf("shielded: invalid witness sibling %d: %w", siblingIndex, err)
		}
	}

	current := hashMerkleLeaf(commitment)
	for level, sibling := range witness.Siblings {
		if witness.Position&(uint64(1)<<level) == 0 {
			current = hashMerkleNode(level, current, sibling)
		} else {
			current = hashMerkleNode(level, sibling, current)
		}
	}
	return current, nil
}

// Verify 验证承诺和认证路径是否得到预期根。
func (witness MerkleWitness) Verify(commitment Commitment, expectedRoot MerkleRoot) bool {
	actualRoot, err := witness.ComputeRoot(commitment)
	if err != nil {
		return false
	}
	return actualRoot == expectedRoot
}

// VerifyMerklePath 验证承诺、认证路径和预期根。
func VerifyMerklePath(commitment Commitment, witness MerkleWitness, expectedRoot MerkleRoot) bool {
	return witness.Verify(commitment, expectedRoot)
}

func (tree *IncrementalMerkleTree) rootLocked() MerkleRoot {
	capacity := uint64(1) << tree.depth
	if tree.leafCount == capacity {
		return tree.frontier[tree.depth]
	}
	current := tree.emptyRoots[0]
	for level := 0; level < tree.depth; level++ {
		if tree.leafCount&(uint64(1)<<level) != 0 {
			current = hashMerkleNode(level, tree.frontier[level], current)
		} else {
			current = hashMerkleNode(level, current, tree.emptyRoots[level])
		}
	}
	return current
}

func (tree *IncrementalMerkleTree) subtreeRootLocked(start uint64, height int) Digest {
	if start >= tree.leafCount {
		return tree.emptyRoots[height]
	}
	subtreeSize := uint64(1) << height
	if start+subtreeSize <= tree.leafCount {
		index := start >> height
		if index < uint64(len(tree.levels[height])) {
			return tree.levels[height][index]
		}
	}
	if height == 0 {
		return tree.levels[0][start]
	}
	halfSize := uint64(1) << (height - 1)
	left := tree.subtreeRootLocked(start, height-1)
	right := tree.subtreeRootLocked(start+halfSize, height-1)
	return hashMerkleNode(height-1, left, right)
}

func hashMerkleEmptyLeaf() Digest {
	return Digest{}
}

func hashMerkleLeaf(commitment Commitment) Digest {
	return commitment
}

func hashMerkleNode(_ int, left Digest, right Digest) Digest {
	digest, err := mimcFieldHash(merkleDomainTag, left, right)
	if err != nil {
		// 调用点在进入树状态前已经验证域元素，触发该分支表示内部状态被破坏。
		panic(fmt.Sprintf("shielded: invalid internal Merkle field: %v", err))
	}
	return digest
}
