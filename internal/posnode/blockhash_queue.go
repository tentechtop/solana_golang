package posnode

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/structure"
)

// restoreRecentBlockhashQueue 从持久化主链恢复交易有效期窗口，避免节点重启后误拒绝仍在有效期内的交易。
func restoreRecentBlockhashQueue(ledger *blockchain.Ledger) (structure.BlockhashQueue, error) {
	queue := structure.NewBlockhashQueue(structure.MaxRecentBlockhashAgeSlots)
	if ledger == nil {
		return queue, fmt.Errorf("posnode: restore blockhash queue: ledger is nil")
	}
	head := ledger.Head()
	startHeight := uint64(1)
	if head.Height > structure.MaxRecentBlockhashAgeSlots {
		startHeight = head.Height - structure.MaxRecentBlockhashAgeSlots
	}
	for height := startHeight; height <= head.Height && head.Height > 0; height++ {
		proposal, blockhash, found, err := ledger.BlockByHeight(height)
		if err != nil {
			return structure.BlockhashQueue{}, fmt.Errorf("posnode: restore blockhash at height %d: %w", height, err)
		}
		if !found {
			continue
		}
		if err := queue.Add(structure.RecentBlockhashEntry{
			Blockhash:     blockhash,
			Slot:          proposal.Header.Slot,
			FeeCalculator: structure.DefaultFeeCalculator(),
			TimestampUnix: proposal.Header.TimestampUnixMilli / 1000,
		}); err != nil {
			return structure.BlockhashQueue{}, fmt.Errorf("posnode: restore blockhash at height %d: %w", height, err)
		}
	}
	if _, exists := queue.Find(head.BlockHash); !exists {
		if err := queue.Add(structure.RecentBlockhashEntry{
			Blockhash:     head.BlockHash,
			Slot:          head.Slot,
			FeeCalculator: structure.DefaultFeeCalculator(),
			TimestampUnix: time.Now().Unix(),
		}); err != nil {
			return structure.BlockhashQueue{}, fmt.Errorf("posnode: restore current blockhash: %w", err)
		}
	}
	return queue, nil
}

// recordCommittedBlockhash 记录已提交区块哈希 + RPC 和自动注册交易需要持续可用的 recent blockhash 窗口。
func (node *posNode) recordCommittedBlockhash(slot uint64, blockhash structure.Hash) {
	if blockhash == (structure.Hash{}) {
		return
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.blockhashQueue.Find(blockhash); exists {
		return
	}
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     blockhash,
		Slot:          slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		node.logger.Warn("posnode record committed blockhash failed",
			slog.Uint64("slot", slot),
			slog.String("block_hash", blockhash.String()),
			slog.Any("error", err),
		)
	}
}
