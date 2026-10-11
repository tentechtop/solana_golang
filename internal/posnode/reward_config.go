package posnode

import (
	"encoding/json"
	"fmt"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

// rewardConfig is shared by proposal construction, live verification and history
// sync. The activation epoch is a network upgrade schedule, not a genesis edit.
func (config nodeConfig) rewardConfig() consensus.RewardConfig {
	reward := consensus.DefaultRewardConfig()
	reward.ExactStakePayoutActivationEpoch = config.StakeRewardExactRatioEpoch
	return reward
}

// Pin the schedule to the existing ledger so a restart cannot silently change
// the historical arithmetic used by proposal verification and synchronization.
func (node *posNode) ensureStakePayoutSchedule() error {
	key := []byte("posnode:rewards:exact-stake-payout-epoch:v1")
	data, err := node.db.Get(database.TableCheckpoint, key)
	if err != nil {
		return fmt.Errorf("posnode: read stake payout schedule: %w", err)
	}
	activation := node.config.StakeRewardExactRatioEpoch
	if len(data) != 0 {
		var stored uint64
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("posnode: decode stake payout schedule: %w", err)
		}
		if stored != activation {
			return fmt.Errorf("posnode: stake payout activation epoch %d differs from ledger schedule %d", activation, stored)
		}
		return nil
	}
	head := node.ledger.Head()
	if head.Height > 0 {
		currentEpoch, _ := node.epochForSlot(head.Slot)
		if activation <= currentEpoch {
			return fmt.Errorf("posnode: existing ledger requires an agreed future stake_reward_exact_ratio_epoch (current epoch %d)", currentEpoch)
		}
	}
	data, err = json.Marshal(activation)
	if err != nil {
		return err
	}
	return node.db.Put(database.TableCheckpoint, key, data)
}
