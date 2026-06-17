package main

import (
	"fmt"
	"sort"
	"strings"

	"solana_golang/consensus"
)

const (
	maxSeenQuorumCertificates = 8192
	minSeenQCRetentionSlots   = 1024
)

// qcPropagationKey 生成 QC 传播键 + 避免同一逻辑 QC 因签名编码差异重复泛洪。
func qcPropagationKey(qc consensus.QuorumCertificate) string {
	voters := append([]string(nil), qc.Voters...)
	sort.Strings(voters)
	return fmt.Sprintf(
		"%d:%d:%d:%s:%d:%d:%d:%s",
		qc.Type,
		qc.Slot,
		qc.BlockHeight,
		qc.BlockHash.String(),
		qc.ThresholdStake,
		qc.ConfirmedStake,
		qc.CreatedAtUnixMilli,
		strings.Join(voters, ","),
	)
}

// hasQCSeen 判断 QC 是否处理过 + 避免重复证书进入账本热路径。
func (node *posNode) hasQCSeen(qc consensus.QuorumCertificate) bool {
	key := qcPropagationKey(qc)
	node.mutex.Lock()
	defer node.mutex.Unlock()
	_, exists := node.seenQCs[key]
	return exists
}

// markQCSeen 记录已处理 QC + 让收到的更强 QC 可以继续传播。
func (node *posNode) markQCSeen(qc consensus.QuorumCertificate) bool {
	key := qcPropagationKey(qc)
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.seenQCs == nil {
		node.seenQCs = make(map[string]uint64)
	}
	if _, exists := node.seenQCs[key]; exists {
		return false
	}
	node.seenQCs[key] = qc.Slot
	node.pruneSeenQCsLocked(qc.Slot)
	return true
}

// pruneSeenQCsLocked 清理旧 QC 传播键 + 防止长稳运行时内存无界增长。
func (node *posNode) pruneSeenQCsLocked(currentSlot uint64) {
	if len(node.seenQCs) <= maxSeenQuorumCertificates {
		return
	}
	retentionSlots := node.config.EpochSlots
	if retentionSlots < minSeenQCRetentionSlots {
		retentionSlots = minSeenQCRetentionSlots
	}
	if currentSlot <= retentionSlots {
		return
	}
	cutoffSlot := currentSlot - retentionSlots
	for key, slot := range node.seenQCs {
		if slot < cutoffSlot {
			delete(node.seenQCs, key)
		}
	}
}
