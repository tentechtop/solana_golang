package main

import (
	"log/slog"
	"time"

	"solana_golang/structure"
)

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
