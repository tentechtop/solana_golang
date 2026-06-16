package consensus

import (
	"bytes"
	"fmt"
	"sort"

	"solana_golang/utils"
)

const (
	DefaultTurbineFanout = 2
	MaxTurbineFanout     = 1024
)

// TurbineNode 描述 Turbine 树节点 + 所有验证者用相同输入计算自己的网络层级。
type TurbineNode struct {
	ValidatorID  ValidatorID
	P2PPeerID    string
	Layer        int
	ParentID     ValidatorID
	ParentPeerID string
}

// TurbineTree 保存 slot 传播树 + leader 只需要把 proposal 发给自己的子节点。
type TurbineTree struct {
	Slot             uint64
	Fanout           int
	LeaderID         ValidatorID
	nodes            []TurbineNode
	indexByValidator map[ValidatorID]int
}

// NewTurbineTree 构建确定性传播树 + 按 epoch snapshot 和 slot 洗牌后生成分层 fanout。
func NewTurbineTree(snapshot EpochSnapshot, slot uint64, leaderID ValidatorID, fanout int) (TurbineTree, error) {
	if fanout <= 0 || fanout > MaxTurbineFanout {
		return TurbineTree{}, fmt.Errorf("%w: invalid turbine fanout", ErrInvalidQuorum)
	}
	if len(snapshot.Validators) == 0 {
		return TurbineTree{}, fmt.Errorf("%w: empty turbine validators", ErrInvalidVote)
	}
	leader, exists := snapshot.ValidatorByID(leaderID)
	if !exists {
		return TurbineTree{}, fmt.Errorf("%w: turbine leader not in snapshot", ErrUnknownValidator)
	}

	orderedValidators := turbineValidatorOrder(snapshot, slot, leaderID)
	nodes := make([]TurbineNode, 0, len(orderedValidators)+1)
	nodes = append(nodes, TurbineNode{
		ValidatorID: leader.ValidatorID,
		P2PPeerID:   leader.P2PPeerID,
		Layer:       0,
	})
	for _, validator := range orderedValidators {
		if validator.ValidatorID == leaderID {
			continue
		}
		nodes = append(nodes, TurbineNode{
			ValidatorID: validator.ValidatorID,
			P2PPeerID:   validator.P2PPeerID,
		})
	}
	for index := 1; index < len(nodes); index++ {
		parentIndex := (index - 1) / fanout
		nodes[index].Layer = turbineLayer(index, fanout)
		nodes[index].ParentID = nodes[parentIndex].ValidatorID
		nodes[index].ParentPeerID = nodes[parentIndex].P2PPeerID
	}

	indexByValidator := make(map[ValidatorID]int, len(nodes))
	for index, node := range nodes {
		indexByValidator[node.ValidatorID] = index
	}
	return TurbineTree{
		Slot:             slot,
		Fanout:           fanout,
		LeaderID:         leaderID,
		nodes:            nodes,
		indexByValidator: indexByValidator,
	}, nil
}

// Nodes 返回树节点副本 + 状态接口可以展示全网分层视图。
func (tree TurbineTree) Nodes() []TurbineNode {
	nodes := make([]TurbineNode, len(tree.nodes))
	copy(nodes, tree.nodes)
	return nodes
}

// NodeByValidator 查询单个验证者位置 + 节点用该结果知道自己在哪一层。
func (tree TurbineTree) NodeByValidator(validatorID ValidatorID) (TurbineNode, bool) {
	index, exists := tree.indexByValidator[validatorID]
	if !exists {
		return TurbineNode{}, false
	}
	return tree.nodes[index], true
}

// ChildrenOf 返回直接子节点 + proposal 只转发给这些节点。
func (tree TurbineTree) ChildrenOf(validatorID ValidatorID) []TurbineNode {
	index, exists := tree.indexByValidator[validatorID]
	if !exists {
		return nil
	}
	startIndex := index*tree.Fanout + 1
	if startIndex >= len(tree.nodes) {
		return nil
	}
	endIndex := startIndex + tree.Fanout
	if endIndex > len(tree.nodes) {
		endIndex = len(tree.nodes)
	}
	children := make([]TurbineNode, endIndex-startIndex)
	copy(children, tree.nodes[startIndex:endIndex])
	return children
}

func turbineValidatorOrder(snapshot EpochSnapshot, slot uint64, leaderID ValidatorID) []ValidatorState {
	validators := append([]ValidatorState(nil), snapshot.Validators...)
	sort.SliceStable(validators, func(leftIndex int, rightIndex int) bool {
		left := validators[leftIndex]
		right := validators[rightIndex]
		if left.ValidatorID == leaderID {
			return true
		}
		if right.ValidatorID == leaderID {
			return false
		}
		leftKey := turbineSortKey(snapshot, slot, left.ValidatorID)
		rightKey := turbineSortKey(snapshot, slot, right.ValidatorID)
		if compared := bytes.Compare(leftKey, rightKey); compared != 0 {
			return compared < 0
		}
		return left.ValidatorID < right.ValidatorID
	})
	return validators
}

func turbineSortKey(snapshot EpochSnapshot, slot uint64, validatorID ValidatorID) []byte {
	encoded := make([]byte, 0, len(snapshot.RandomSeed)+len(snapshot.SnapshotStateRoot)+len(validatorID)+16)
	encoded = append(encoded, []byte("pos-turbine-v1")...)
	encoded = append(encoded, snapshot.RandomSeed[:]...)
	encoded = append(encoded, snapshot.SnapshotStateRoot[:]...)
	encoded = appendUint64ForHash(encoded, slot)
	encoded = append(encoded, []byte(validatorID)...)
	return utils.SHA256(encoded)
}

func turbineLayer(index int, fanout int) int {
	layer := 0
	for index > 0 {
		index = (index - 1) / fanout
		layer++
	}
	return layer
}
