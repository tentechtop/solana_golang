package posnode

import (
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
)

const (
	leaderTimeoutSlotMultiplier = 8
	maxRoundTimeoutBackoff      = 3
	minSlotTickInterval         = 50 * time.Millisecond
	maxSlotTickInterval         = 500 * time.Millisecond
)

// slotSkipTimeout 覆盖提案传播、投票回传和 QC 扩散三个网络阶段。
// slot_millis 只是网络阶段预算，不代表每个 slot 必须生成区块。
func (node *posNode) slotSkipTimeout() time.Duration {
	slotDuration := node.config.slotDuration()
	if slotDuration <= 0 {
		return 0
	}
	timeout := slotDuration * leaderTimeoutSlotMultiplier
	minimumRecoveryTimeout := node.livenessQuorumStabilizationDelay() + slotDuration
	if timeout < minimumRecoveryTimeout {
		return minimumRecoveryTimeout
	}
	return timeout
}

// roundLeaderTimeout 只对仍可直连的 Leader 做指数退避。
// 已断开 Leader 的每一轮仍须等待基础窗口并形成 TimeoutQC，但不应因连续离线 Leader 放大确认延迟。
func (node *posNode) roundLeaderTimeout(parentSlot uint64, roundSlot uint64) time.Duration {
	baseTimeout := node.slotSkipTimeout()
	if baseTimeout <= 0 || roundSlot <= parentSlot || roundSlot-parentSlot <= 1 {
		return baseTimeout
	}
	return roundLeaderTimeoutForAvailability(
		baseTimeout,
		roundSlot-parentSlot-1,
		node.roundLeaderKnownUnavailable(roundSlot),
	)
}

func roundLeaderTimeoutForAvailability(baseTimeout time.Duration, skippedRounds uint64, leaderUnavailable bool) time.Duration {
	if baseTimeout <= 0 || skippedRounds == 0 || leaderUnavailable {
		return baseTimeout
	}
	if skippedRounds > maxRoundTimeoutBackoff {
		skippedRounds = maxRoundTimeoutBackoff
	}
	multiplier := time.Duration(1 << skippedRounds)
	if baseTimeout > time.Duration(^uint64(0)>>1)/multiplier {
		return time.Duration(^uint64(0) >> 1)
	}
	return baseTimeout * multiplier
}

// roundLeaderKnownUnavailable 使用当前 epoch 的共识身份和直连状态判断 Leader 是否已明确离线。
// 缺少调度、身份或网络状态时保守返回 false，继续使用退避窗口。
func (node *posNode) roundLeaderKnownUnavailable(roundSlot uint64) bool {
	if node.host == nil {
		return false
	}
	leaderID, err := node.leaderSchedule.LeaderForSlot(roundSlot)
	if err != nil || leaderID == consensusValidatorID(node) {
		return false
	}
	for _, validator := range node.epochSnapshot.Validators {
		if validator.ValidatorID != leaderID {
			continue
		}
		if validator.P2PPeerID == "" {
			return false
		}
		_, connected := node.host.ConnectionState(validator.P2PPeerID)
		return !connected
	}
	return false
}

func consensusValidatorID(node *posNode) consensus.ValidatorID {
	return consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
}

// roundProposalReadyAt 给单验证者模式设置最小出块间隔。
// 多验证者仍由提案、投票和 QC 驱动；单验证者不能按高频调度 tick 连续生成临时块压垮跟随节点。
func roundProposalReadyAt(
	now time.Time,
	roundStartedAt time.Time,
	minimumInterval time.Duration,
	validatorCount int,
) bool {
	if validatorCount != 1 || minimumInterval <= 0 || roundStartedAt.IsZero() {
		return true
	}
	return !now.Before(roundStartedAt.Add(minimumInterval))
}

// slotTickInterval 计算 slot 驱动频率，短 slot 需要更密集 tick 来降低本机调度抖动。
func (node *posNode) slotTickInterval() time.Duration {
	slotDuration := node.config.slotDuration()
	if slotDuration <= 0 {
		return maxSlotTickInterval
	}
	interval := slotDuration / 4
	if interval < minSlotTickInterval {
		return minSlotTickInterval
	}
	if interval > maxSlotTickInterval {
		return maxSlotTickInterval
	}
	return interval
}

// peerStatusTimeout 计算同步状态探测超时；控制面请求不能使用出块 deadline，避免低配公网节点被误判离线。
func (node *posNode) peerStatusTimeout() time.Duration {
	slotDuration := node.config.slotDuration()
	if slotDuration <= 0 {
		return minPeerStatusTimeout
	}
	timeout := slotDuration * peerStatusTimeoutSlotMultiplier
	if timeout < minPeerStatusTimeout {
		return minPeerStatusTimeout
	}
	if timeout > maxPeerStatusTimeout {
		return maxPeerStatusTimeout
	}
	return timeout
}
