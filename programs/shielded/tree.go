package shielded

import (
	"fmt"
	"math/big"

	"solana_golang/structure"
	"solana_golang/zk"
)

// PoolAccountDataCapacity 是当前树深度和锚点窗口下池状态的最大规范编码长度。
const PoolAccountDataCapacity = 2 + 2 + 4 + structure.HashSize + 1 + 8 + 4 +
	zk.ShieldedMerkleDepth*structure.HashSize + structure.HashSize + 4 +
	structure.MaxShieldedRecentAnchors*structure.HashSize + 8

// NewPoolState 创建空的全局承诺树状态。
func NewPoolState(verifyingKeyHash structure.Hash) (structure.ShieldedPoolState, error) {
	emptyRoot, err := zk.ComputeShieldedEmptyRoot(zk.ShieldedMerkleDepth)
	if err != nil {
		return structure.ShieldedPoolState{}, err
	}
	encodedRoot, err := zk.ShieldedFieldBytes(emptyRoot)
	if err != nil {
		return structure.ShieldedPoolState{}, err
	}
	var root structure.Hash
	copy(root[:], encodedRoot[:])
	state := structure.ShieldedPoolState{
		StateEncodingVersion: structure.ShieldedPoolStateEncodingVersion,
		ProtocolVersion:      structure.ShieldedProtocolVersion,
		CircuitID:            zk.ShieldedCircuitID,
		VerifyingKeyHash:     verifyingKeyHash,
		TreeDepth:            zk.ShieldedMerkleDepth,
		Frontier:             make([]structure.Hash, zk.ShieldedMerkleDepth),
		CurrentRoot:          root,
		RecentAnchors:        []structure.Hash{root},
	}
	return state, state.Validate()
}

// DefaultPoolState 使用协议内置参数创建空池状态。
func DefaultPoolState() (structure.ShieldedPoolState, error) {
	parameters, err := zk.DefaultShieldedCircuitParameters()
	if err != nil {
		return structure.ShieldedPoolState{}, err
	}
	verifyingKeyDigest, err := parameters.VerifyingKeyHash()
	if err != nil {
		return structure.ShieldedPoolState{}, err
	}
	verifyingKeyHash, err := structure.NewHash(verifyingKeyDigest[:])
	if err != nil {
		return structure.ShieldedPoolState{}, err
	}
	return NewPoolState(verifyingKeyHash)
}

func appendOutputCommitments(state *structure.ShieldedPoolState, actions []structure.ShieldedAction) error {
	for _, action := range actions {
		if !action.EnabledOutput {
			continue
		}
		commitment, err := zk.ShieldedFieldFromBytes(action.NoteCommitment[:])
		if err != nil {
			return fmt.Errorf("%w: invalid output commitment: %v", structure.ErrInvalidShieldedPoolState, err)
		}
		if err := appendCommitment(state, commitment); err != nil {
			return err
		}
	}
	return nil
}

func appendCommitment(state *structure.ShieldedPoolState, commitment *big.Int) error {
	depth := int(state.TreeDepth)
	capacity := uint64(1) << state.TreeDepth
	if state.LeafCount >= capacity {
		return fmt.Errorf("%w: commitment tree is full", structure.ErrInvalidShieldedPoolState)
	}
	if len(state.Frontier) != depth {
		return fmt.Errorf("%w: invalid frontier length", structure.ErrInvalidShieldedPoolState)
	}
	position := state.LeafCount
	current := new(big.Int).Set(commitment)
	completedRoot := (*big.Int)(nil)
	for level := 0; level < depth; level++ {
		if position&(uint64(1)<<level) == 0 {
			encoded, err := zk.ShieldedFieldBytes(current)
			if err != nil {
				return err
			}
			copy(state.Frontier[level][:], encoded[:])
			break
		}
		left, err := zk.ShieldedFieldFromBytes(state.Frontier[level][:])
		if err != nil {
			return fmt.Errorf("%w: invalid frontier node %d", structure.ErrInvalidShieldedPoolState, level)
		}
		current, err = zk.ComputeShieldedMerkleNode(left, current)
		if err != nil {
			return err
		}
		state.Frontier[level] = structure.Hash{}
		if level == depth-1 {
			completedRoot = current
		}
	}
	state.LeafCount++
	var root *big.Int
	var err error
	if state.LeafCount == capacity {
		root = completedRoot
		if root == nil {
			return fmt.Errorf("%w: missing completed tree root", structure.ErrInvalidShieldedPoolState)
		}
	} else {
		root, err = frontierRoot(state)
		if err != nil {
			return err
		}
	}
	encodedRoot, err := zk.ShieldedFieldBytes(root)
	if err != nil {
		return err
	}
	copy(state.CurrentRoot[:], encodedRoot[:])
	appendRecentAnchor(state, state.CurrentRoot)
	return nil
}

func frontierRoot(state *structure.ShieldedPoolState) (*big.Int, error) {
	emptyRoots := make([]*big.Int, int(state.TreeDepth)+1)
	emptyRoots[0] = big.NewInt(0)
	for level := 0; level < int(state.TreeDepth); level++ {
		next, err := zk.ComputeShieldedMerkleNode(emptyRoots[level], emptyRoots[level])
		if err != nil {
			return nil, err
		}
		emptyRoots[level+1] = next
	}
	current := new(big.Int).Set(emptyRoots[0])
	for level := 0; level < int(state.TreeDepth); level++ {
		var err error
		if state.LeafCount&(uint64(1)<<level) != 0 {
			frontierNode, decodeErr := zk.ShieldedFieldFromBytes(state.Frontier[level][:])
			if decodeErr != nil {
				return nil, fmt.Errorf("%w: invalid frontier node %d", structure.ErrInvalidShieldedPoolState, level)
			}
			current, err = zk.ComputeShieldedMerkleNode(frontierNode, current)
		} else {
			current, err = zk.ComputeShieldedMerkleNode(current, emptyRoots[level])
		}
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

func validateAnchor(state structure.ShieldedPoolState, anchor structure.Hash) error {
	if anchor == state.CurrentRoot {
		return nil
	}
	for _, recentAnchor := range state.RecentAnchors {
		if anchor == recentAnchor {
			return nil
		}
	}
	return fmt.Errorf("%w: anchor is not recent", structure.ErrInvalidShieldedBundle)
}

func appendRecentAnchor(state *structure.ShieldedPoolState, root structure.Hash) {
	if len(state.RecentAnchors) > 0 && state.RecentAnchors[len(state.RecentAnchors)-1] == root {
		return
	}
	state.RecentAnchors = append(state.RecentAnchors, root)
	if len(state.RecentAnchors) > structure.MaxShieldedRecentAnchors {
		state.RecentAnchors = append([]structure.Hash(nil), state.RecentAnchors[len(state.RecentAnchors)-structure.MaxShieldedRecentAnchors:]...)
	}
}
