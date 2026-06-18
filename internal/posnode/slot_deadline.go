package posnode

import "time"

const (
	slotDeadlineNumerator   = 9
	slotDeadlineDenominator = 10
	minSlotTickInterval     = 50 * time.Millisecond
	maxSlotTickInterval     = 500 * time.Millisecond
)

// slotSkipTimeout 计算 slot 出块窗口 + 与 SlotClock 的 skip 超时保持一致。
func (node *posNode) slotSkipTimeout() time.Duration {
	slotDuration := node.config.slotDuration()
	if slotDuration <= 0 {
		return 0
	}
	return slotDuration * slotDeadlineNumerator / slotDeadlineDenominator
}

// slotTickInterval 计算 slot 驱动频率 + 短 slot 需要更密集 tick 来降低本机调度抖动。
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

// slotStartTime 计算 slot 起点 + 所有节点必须从创世时间和固定 slot 时长推导一致结果。
func (node *posNode) slotStartTime(slot uint64) time.Time {
	startedAt := node.config.genesisStartTime()
	slotDuration := node.config.slotDuration()
	if slot <= 1 || slotDuration <= 0 {
		return startedAt
	}
	slotOffset := slot - 1
	maxOffset := uint64(time.Duration(1<<63-1) / slotDuration)
	if slotOffset > maxOffset {
		return startedAt.Add(time.Duration(1<<63 - 1))
	}
	return startedAt.Add(time.Duration(slotOffset) * slotDuration)
}

// slotProductionDeadline 计算出块截止时间 + leader 和 validator 使用同一条过期边界。
func (node *posNode) slotProductionDeadline(slot uint64) time.Time {
	return node.slotStartTime(slot).Add(node.slotSkipTimeout())
}

// slotDeadlinePassed 判断 slot 是否过期 + 配置无效时保持测试节点兼容。
func (node *posNode) slotDeadlinePassed(slot uint64, now time.Time) bool {
	if slot == 0 {
		return true
	}
	if node.config.SlotMillis <= 0 || node.config.GenesisStartMs == 0 {
		return false
	}
	return !now.Before(node.slotProductionDeadline(slot))
}

// peerStatusTimeout 计算同步状态探测超时 + 控制面请求不能使用出块 deadline，避免低配公网节点被误判离线。
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
