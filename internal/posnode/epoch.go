package posnode

import (
	"fmt"

	"solana_golang/structure"
)

func (node *posNode) ensureEpochForSlotLocked(slot uint64) error {
	epochID, startSlot := node.epochForSlot(slot)
	if node.epochSnapshot.EpochID == epochID &&
		node.epochSnapshot.StartSlot == startSlot &&
		slot <= node.epochSnapshot.EndSlot {
		return nil
	}
	return node.rebuildEpochLocked(epochID, startSlot, node.epochSeed(epochID))
}

func (node *posNode) epochForSlot(slot uint64) (uint64, uint64) {
	if slot == 0 {
		return 0, 1
	}
	epochID := (slot - 1) / node.config.EpochSlots
	return epochID, epochID*node.config.EpochSlots + 1
}

func (node *posNode) epochSeed(epochID uint64) structure.Hash {
	return mustHash(fmt.Sprintf("%s-epoch-%d", node.config.ChainID, epochID))
}
