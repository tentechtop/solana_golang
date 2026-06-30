package posnode

import (
	"context"
	"log/slog"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
)

const (
	livenessGateStateReady       = "ready"
	livenessGateStateDegraded    = "degraded"
	livenessGateStateDisabled    = "disabled"
	livenessGateStateUnavailable = "unavailable"

	livenessGateModeProducing     = "producing"
	livenessGateModeRPCOnly       = "rpc_only"
	livenessGateModeWaitingEpoch  = "waiting_epoch"
	livenessGateModeWaitingQuorum = "waiting_quorum"

	livenessGateReasonDisabled             = "consensus_disabled"
	livenessGateReasonSnapshotUnavailable  = "validator_snapshot_unavailable"
	livenessGateReasonReachableStakeQuorum = "reachable_stake_quorum_ready"
	livenessGateReasonReachableStakeLow    = "reachable_stake_below_quorum"

	minLivenessGateInterval        = time.Second
	maxLivenessGateInterval        = 5 * time.Second
	minLivenessReachabilityWindow  = 3 * time.Second
	maxLivenessReachabilityWindow  = 15 * time.Second
	livenessReachabilityMultiplier = 2
)

// livenessGateLoop 定期刷新运行期活性门禁 + leader 出块前需要稳定的可观测状态。
func (node *posNode) livenessGateLoop(ctx context.Context) {
	ticker := time.NewTicker(node.livenessGateInterval())
	defer ticker.Stop()
	node.refreshLivenessGate(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			node.refreshLivenessGate(now)
		}
	}
}

// refreshLivenessGate 刷新活性门禁快照 + RPC 和出块路径共享同一份线程安全状态。
func (node *posNode) refreshLivenessGate(now time.Time) livenessGateJSON {
	node.mutex.Lock()
	snapshot := node.epochSnapshot
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	consensusEnabled := node.config.consensusEnabled()
	window := node.livenessReachabilityWindow()
	node.mutex.Unlock()

	reachablePeerIDs := node.livenessRecentReachablePeerIDs(snapshot, now, window)
	gate := buildLivenessGateFromSnapshot(snapshot, localValidatorID, reachablePeerIDs, now, window, consensusEnabled)

	node.mutex.Lock()
	previous := node.livenessGate
	node.livenessGate = gate
	changed := livenessGateChanged(previous, gate)
	node.mutex.Unlock()
	node.logLivenessGateTransition(previous, gate, changed)
	return gate
}

// livenessRecentReachablePeerIDs 收集近期可达 peer + 只把仍有连接活动的验证者计入 quorum。
func (node *posNode) livenessRecentReachablePeerIDs(snapshot consensus.EpochSnapshot, now time.Time, window time.Duration) map[string]struct{} {
	reachablePeerIDs := make(map[string]struct{})
	if node.host == nil {
		return reachablePeerIDs
	}
	for _, validator := range snapshot.Validators {
		if validator.P2PPeerID == "" {
			continue
		}
		connectionState, connected := node.host.ConnectionState(validator.P2PPeerID)
		if !connected {
			continue
		}
		if !connectionRecentlyReachable(connectionState, now, window) {
			continue
		}
		reachablePeerIDs[validator.P2PPeerID] = struct{}{}
	}
	return reachablePeerIDs
}

// buildLivenessGateFromSnapshot 计算门禁状态 + 只使用本地 epoch stake 防止网络载荷伪造权重。
func buildLivenessGateFromSnapshot(
	snapshot consensus.EpochSnapshot,
	localValidatorID consensus.ValidatorID,
	reachablePeerIDs map[string]struct{},
	now time.Time,
	window time.Duration,
	consensusEnabled bool,
) livenessGateJSON {
	if !consensusEnabled {
		return livenessGateDisabled(now, window)
	}
	requiredStake, err := livenessRequiredStake(snapshot.TotalActiveStake)
	if err != nil || len(snapshot.Validators) == 0 {
		return livenessGateUnavailable(snapshot, now, window)
	}
	reachableStake, reachableValidatorIDs := reachableStakeForSnapshot(snapshot, localValidatorID, reachablePeerIDs)
	gate := livenessGateJSON{
		State:                             livenessGateStateReady,
		Mode:                              livenessGateModeProducing,
		Reason:                            livenessGateReasonReachableStakeQuorum,
		Applicable:                        true,
		QuorumReady:                       true,
		ProductionEnabled:                 true,
		UserTransactionPackagingEnabled:   true,
		ReachableStakeLamports:            reachableStake,
		RequiredStakeLamports:             requiredStake,
		TotalActiveStakeLamports:          snapshot.TotalActiveStake,
		ReachableStakeWeightBps:           stakeWeightBps(reachableStake, snapshot.TotalActiveStake),
		ReachableValidatorCount:           len(reachableValidatorIDs),
		ValidatorCount:                    len(snapshot.Validators),
		ReachableValidatorIDs:             reachableValidatorIDs,
		RecentReachabilityWindowMillis:    window.Milliseconds(),
		LastReachableStakeUpdateUnixMilli: now.UnixMilli(),
		MinimumQuorumNumerator:            defaultConsensusQuorum,
		MinimumQuorumDenominator:          3,
	}
	if reachableStake >= requiredStake {
		return gate
	}
	gate.State = livenessGateStateDegraded
	gate.Mode = livenessGateModeWaitingQuorum
	gate.Reason = livenessGateReasonReachableStakeLow
	gate.QuorumReady = false
	gate.ProductionEnabled = false
	gate.UserTransactionPackagingEnabled = false
	return gate
}

// reachableStakeForSnapshot 汇总可达验证者权重 + 本地验证者不依赖网络连接即可参与本地投票。
func reachableStakeForSnapshot(
	snapshot consensus.EpochSnapshot,
	localValidatorID consensus.ValidatorID,
	reachablePeerIDs map[string]struct{},
) (uint64, []string) {
	reachableStake := uint64(0)
	reachableValidatorIDs := make([]string, 0, len(snapshot.Validators))
	for _, validator := range snapshot.Validators {
		if validator.StakeLamports == 0 {
			continue
		}
		if validator.ValidatorID != localValidatorID {
			if _, reachable := reachablePeerIDs[validator.P2PPeerID]; !reachable {
				continue
			}
		}
		if ^uint64(0)-reachableStake < validator.StakeLamports {
			return ^uint64(0), append(reachableValidatorIDs, string(validator.ValidatorID))
		}
		reachableStake += validator.StakeLamports
		reachableValidatorIDs = append(reachableValidatorIDs, string(validator.ValidatorID))
	}
	return reachableStake, reachableValidatorIDs
}

// connectionRecentlyReachable 判断连接是否可达 + Host 只返回当前连接，空闲 TCP 不能让 quorum 自锁。
func connectionRecentlyReachable(state p2p.ConnectionState, now time.Time, window time.Duration) bool {
	if state.ConnectedAtUnixMilli > 0 {
		return true
	}
	lastReachableUnixMilli := maxInt64(
		state.ConnectedAtUnixMilli,
		state.LastReadUnixMilli,
		state.LastWriteUnixMilli,
		state.LastHeartbeatUnixMilli,
	)
	if lastReachableUnixMilli <= 0 {
		return false
	}
	nowUnixMilli := now.UnixMilli()
	if lastReachableUnixMilli >= nowUnixMilli {
		return true
	}
	return time.Duration(nowUnixMilli-lastReachableUnixMilli)*time.Millisecond <= window
}

// livenessRequiredStake 计算 2/3 stake 阈值 + 复用 QC quorum 规则避免边界不一致。
func livenessRequiredStake(totalStake uint64) (uint64, error) {
	return (consensus.Quorum{Numerator: defaultConsensusQuorum, Denominator: 3}).RequiredStake(totalStake)
}

// livenessGateAllowsProduction 判断是否允许生产 + 不适用 gate 的 RPC 节点不能被误判为降级。
func livenessGateAllowsProduction(gate livenessGateJSON) bool {
	return !gate.Applicable || gate.ProductionEnabled
}

// livenessGateHealthOK 判断健康状态 + APP 可以区分 RPC 可用和共识等待 quorum。
func livenessGateHealthOK(gate livenessGateJSON) bool {
	return !gate.Applicable || gate.QuorumReady
}

// livenessGateInterval 计算刷新周期 + 短 slot 快速发现恢复，长 slot 限制后台开销。
func (node *posNode) livenessGateInterval() time.Duration {
	interval := node.config.slotDuration()
	if interval < minLivenessGateInterval {
		return minLivenessGateInterval
	}
	if interval > maxLivenessGateInterval {
		return maxLivenessGateInterval
	}
	return interval
}

// livenessReachabilityWindow 计算近期窗口 + 必须覆盖控制面探测抖动但不能长期保留陈旧连接。
func (node *posNode) livenessReachabilityWindow() time.Duration {
	window := node.peerStatusTimeout() * livenessReachabilityMultiplier
	if window < minLivenessReachabilityWindow {
		return minLivenessReachabilityWindow
	}
	if window > maxLivenessReachabilityWindow {
		return maxLivenessReachabilityWindow
	}
	return window
}

// livenessGateDisabled 返回禁用态 + 非共识节点仍需要稳定 RPC 状态字段。
func livenessGateDisabled(now time.Time, window time.Duration) livenessGateJSON {
	return livenessGateJSON{
		State:                             livenessGateStateDisabled,
		Mode:                              livenessGateModeRPCOnly,
		Reason:                            livenessGateReasonDisabled,
		RecentReachabilityWindowMillis:    window.Milliseconds(),
		LastReachableStakeUpdateUnixMilli: now.UnixMilli(),
		MinimumQuorumNumerator:            defaultConsensusQuorum,
		MinimumQuorumDenominator:          3,
	}
}

// livenessGateUnavailable 返回等待 epoch 状态 + 缺少验证者快照时 leader 不能安全生产。
func livenessGateUnavailable(snapshot consensus.EpochSnapshot, now time.Time, window time.Duration) livenessGateJSON {
	return livenessGateJSON{
		State:                             livenessGateStateUnavailable,
		Mode:                              livenessGateModeWaitingEpoch,
		Reason:                            livenessGateReasonSnapshotUnavailable,
		Applicable:                        true,
		ValidatorCount:                    len(snapshot.Validators),
		TotalActiveStakeLamports:          snapshot.TotalActiveStake,
		RecentReachabilityWindowMillis:    window.Milliseconds(),
		LastReachableStakeUpdateUnixMilli: now.UnixMilli(),
		MinimumQuorumNumerator:            defaultConsensusQuorum,
		MinimumQuorumDenominator:          3,
	}
}

// livenessGateChanged 判断状态是否变化 + 只在关键状态转换时输出日志避免刷屏。
func livenessGateChanged(previous livenessGateJSON, current livenessGateJSON) bool {
	return previous.State != current.State ||
		previous.Mode != current.Mode ||
		previous.QuorumReady != current.QuorumReady ||
		previous.ReachableStakeLamports != current.ReachableStakeLamports ||
		previous.RequiredStakeLamports != current.RequiredStakeLamports
}

// logLivenessGateTransition 输出门禁转换日志 + 运维排查需要看到自动降级和恢复边界。
func (node *posNode) logLivenessGateTransition(previous livenessGateJSON, current livenessGateJSON, changed bool) {
	if !changed {
		return
	}
	if current.State == "" {
		return
	}
	logger := node.logger
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelInfo
	message := "posnode liveness gate ready"
	if current.Applicable && !current.QuorumReady {
		level = slog.LevelWarn
		message = "posnode liveness gate degraded"
	}
	logger.Log(context.Background(), level, message,
		slog.String("previous_state", previous.State),
		slog.String("state", current.State),
		slog.String("mode", current.Mode),
		slog.String("reason", current.Reason),
		slog.Uint64("reachable_stake", current.ReachableStakeLamports),
		slog.Uint64("required_stake", current.RequiredStakeLamports),
		slog.Uint64("total_stake", current.TotalActiveStakeLamports),
		slog.Uint64("reachable_weight_bps", current.ReachableStakeWeightBps),
		slog.Int("reachable_validators", current.ReachableValidatorCount),
		slog.Int("validator_count", current.ValidatorCount),
	)
}

// maxInt64 选择最大时间戳 + 活跃连接可能只更新读写或心跳中的一个字段。
func maxInt64(values ...int64) int64 {
	maxValue := int64(0)
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}
