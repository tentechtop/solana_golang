# 共识层实现规划

版本：v1  
状态：当前实现说明；未标注“当前实现”的后续章节保留为历史规划
目标：本系统不使用 PoH 作为全局时钟，采用 QC 驱动轮次、超时证书换轮和逐块父 QC 引用；本地时间只触发投票，不能推进链状态。

## 一、核心结论

本系统共识层先采用简单、可审计、可测试的设计：

1. 已认证链头确定下一轮，不能根据墙钟计算或强制推进 slot。
2. leader 在当前 round 提议候选块，候选块不会直接改变主链头。
3. 验证者收到有效 proposal 后先投 prepare vote；PrepareQC 严格超过 `2/3` 后才投 confirm vote，ConfirmQC 严格超过 `2/3` 后提交候选块。
4. leader 超时后验证者只发送 timeout vote；严格超过 `2/3` 权重形成 TimeoutQC 后才进入下一轮。换轮证书与 ConfirmQC 保持拜占庭安全交集。
5. 创世后的每个 proposal 必须引用证明父块的 ConfirmQC；跨过空轮时还必须携带上一轮 TimeoutQC。
6. 本地签名投票必须先写安全日志并刷盘，再通过 P2P 广播；重启后恢复选择和投票高水位。
7. 连续 TimeoutQC 触发 `1x/2x/4x/8x` 超时退避，新块提交后恢复基础超时。
8. 验证者集合按 epoch 快照和链上状态计算，网络载荷不能自行声明有效权重。

当前不新建顶层 `Alpenglow` 目录。通用共识能力放在 `consensus/`；如果后续实现协议特定状态机，再放到 `consensus/alpenglow/`。

## 二、设计原则

1. 安全优先于活性。宁可 skip，也不能接受冲突投票或伪造 QC。
2. 时间只用于推进节奏，不直接证明历史顺序。
3. 链上事实必须由投票和 QC 确认，不由本地时间单独确认。
4. Borsh 是共识消息唯一二进制格式。
5. 网络字段不可信，所有 stake、validator、leader 信息必须以本地状态为准。
6. 每个验证者在同一 slot 可以先签一张 prepare；决定阶段的 confirm 与 timeout 严格互斥，同阶段冲突签名是可罚没证据。
7. 所有状态变化必须可落库、可重放、可审计；投票持久化失败时禁止广播。

## 三、当前实现

1. `consensus/slot.go`：Vote、严格大于 `2/3` 权重阈值、VoteCollector 和 QuorumCertificate。
2. `consensus/bls_qc.go`：单票 BLS 验签、聚合签名、validator bitmap 和按 epoch 权重重算 QC。
3. `consensus/proposal.go`：proposal、父 QC/TimeoutQC 哈希、epoch snapshot root、交易/回执/奖励/证据/state root 和 leader 签名。
4. `internal/posnode/main.go`：候选块传播、投票聚合、ConfirmQC 提交、TimeoutQC 换轮和证书转发。
5. `internal/posnode/round_state.go`：TimeoutQC 与本地签名投票安全日志恢复。
6. `blockchain/ledger.go`：候选块与主链头分离，只有节点层验证过 ConfirmQC 后才提升候选块。
7. P2P、同步和 RPC 已接入；同步时必须同时取得并验证区块 QC。

## 四、时间模型

共识层不使用 PoH hash chain 作为时钟。

节点为当前 round 维护本地单调计时器：

```text
round = certified_head.slot + 1
round = latest_timeout_qc.slot + 1  // TimeoutQC 必须绑定当前链头
timeout_deadline = round_started_at + adaptive_timeout
```

要求：

1. 代码使用 Go `time.Time` 的单调时间差值判断是否应发送 timeout vote。
2. 不允许直接用墙上时间回拨影响 slot。
3. 网络消息里的时间只能作为观测字段，不能作为共识事实。
4. 本地 deadline 到达不能直接更新 round、height 或 head。
5. round 只能由 ConfirmQC 提交新区块或 TimeoutQC 证明换轮。

推荐默认值：

```text
slot_millis 默认值: 1000ms
当前示例配置: 400ms
leader_timeout: slot_millis * 3
连续超时退避: 1x / 2x / 4x / 8x
PrepareQC quorum: strict > 2/3 stake
ConfirmQC quorum: strict > 2/3 stake
TimeoutQC quorum: strict > 2/3 stake
```

默认值只是开发阶段配置，生产环境必须通过链配置或创世配置固定。

## 五、slot 状态机

生产版本建议维护以下状态：

```text
Open
  -> Proposed
  -> Prepared
  -> Confirmed
  -> Finalized

Open
  -> SkipVoted
  -> Skipped
```

状态含义：

1. `Open`：当前 slot 已开始，尚未接受有效 proposal 或 skip。
2. `Proposed`：收到 leader 的有效 proposal。
3. `SkipVoted`：本节点已对该 slot 投 skip vote。
4. `Prepared`：prepare vote 达到 quorum，生成 PrepareQC，但尚未提交区块。
5. `Confirmed`：confirm vote 达到 quorum，生成 block QC。
6. `Skipped`：skip vote 达到 quorum，生成 skip QC。
7. `Finalized`：满足最终确认规则，区块不可回滚。

当前实现把 ConfirmQC 作为候选块提交证据，把 TimeoutQC 作为换轮证据；`Finalized` 仍由独立最终性规则推进。

## 六、slot 执行流程

每个 slot 的核心流程：

```text
1. 根据已认证链头或最新 TimeoutQC 确定当前 round。
2. 判断本节点是否为当前 slot leader。
3. leader 构造 proposal 并广播。
4. validator 收到 proposal 后校验 leader、parent、block hash、交易、签名。
5. proposal 有效则投 prepare vote。
6. PrepareQC 形成后投 confirm vote。
7. 到达 timeout 仍未进入 Confirm 决定时，可投 skip vote；已签 Confirm 的节点不得改投 skip。
8. 收集 vote，达到 quorum 后生成对应 QC 并广播。
9. ConfirmQC 提交候选块，或 TimeoutQC 推进到下一轮；PrepareQC 不能改变链头。
10. 没有 ConfirmQC 或 TimeoutQC 时保持当前 round，即使本地计时器继续流逝也不能推进。
```

注意：

1. 本地超时只能触发 skip vote，不能直接判定 slot skipped。
2. slot skipped 必须由 skip QC 证明。
3. 区块 confirmed 必须由 confirm QC 证明。
4. finalized 必须由后续 finality 规则证明，不能和 confirmed 混用。

## 七、proposal 设计

当前 `BlockProposal` 的关键共识字段包括：

```text
version u16
slot u64
block_height u64
parent_hash [32]byte
previous_qc_hash [32]byte
timeout_qc_hash [32]byte
leader [32]byte
epoch u64
epoch_snapshot_root [32]byte
tx/receipt/reward/evidence/state roots
created_at_unix_milli i64
signature [64]byte
```

校验规则：

1. `slot` 必须等于 proposal 所属 slot。
2. `leader` 必须匹配本地 leader schedule。
3. `parent_slot` 必须小于 `slot`。
4. `parent_hash` 必须能连接本地已知链状态。
5. `block_hash` 必须等于区块头确定性 hash。
6. `PreviousQCHash` 必须等于父块 ConfirmQC 的规范哈希；第一块引用创世哈希。
7. 跨过空轮的 proposal 必须携带并承诺上一轮 TimeoutQC。
8. `signature` 必须使用 leader 公钥验签。
9. Borsh 解码后必须 `Validate`，并执行 `EnsureEOF`。

## 八、投票设计

当前实现的 `Vote`：

```text
type
slot
block_hash
voter_id
stake
created_at_unix_milli
```

验证规则：

1. `stake` 只作为投票声明，实际投票权重必须从本地 epoch snapshot 重算并完全匹配。
2. `voter_id` 必须绑定签名公钥和 epoch validator set。
3. 网络信封必须携带 Ed25519 签名和 BLS 单票证明；QC 必须能由完整 epoch BLS 公钥集合验签。
4. 签名原文包含固定版本、vote type、slot、height、block hash 和 voter ID。
5. timeout vote 的 block hash 必须绑定当前父块，block height 必须绑定目标高度。
6. confirm vote 的 block hash 必须非空。

投票限制：

1. 同一 validator 在同一 slot 最多签一张 prepare 和一张决定票。Prepare 后可签 confirm 或 timeout，但 confirm 与 timeout 不能互换。
2. 同阶段不同类型、block height 或 block hash 构成冲突选择；Prepare 与后续决定票是合法阶段转换。
3. 收到同阶段冲突投票必须记录证据，后续用于惩罚或审计。
4. 未知 validator 的投票必须拒绝。
5. stake 不匹配本地 validator set 的投票必须拒绝。
6. ConfirmQC 与 TimeoutQC 的最小交集严格大于最大拜占庭权益；在诚实验证者禁止决定阶段双签的前提下，同一 slot 不可能同时形成两类决定证书。

## 九、QC 设计

QC 是 quorum certificate，表示某一类投票达到阈值。

当前实现的 `QuorumCertificate`：

```text
type
slot
block_hash
threshold_stake
confirmed_stake
voters
created_at_unix_milli
```

已实现：

1. validator bitmap。
2. BLS 聚合签名。
3. 根据 QC slot 选择 epoch snapshot。
4. 规范 QC hash 与完整证书 hash。

QC 校验规则：

1. QC 类型必须合法。
2. PrepareQC 和 ConfirmQC 必须绑定同阶段的 block hash；PrepareQC 只能触发 Confirm 投票，不得作为账本提交凭证。
3. TimeoutQC 必须绑定当前父块和目标高度。
4. voters 必须排序、唯一、属于同一 validator set。
5. confirmed stake 必须大于等于 threshold stake。
6. 每个签名必须验签通过。
7. QC 的 validator set version 必须和本地状态匹配。

## 十、leader schedule

生产版本必须实现确定性 leader schedule。

建议输入：

```text
epoch
slot
validator_set_hash
stake_distribution
random_seed
```

设计要求：

1. 所有节点对同一 epoch 和 slot 计算出同一个 leader。
2. leader 选择必须可重放。
3. stake 变化只能在 epoch 边界生效。
4. validator set 必须有版本号。
5. leader schedule 结果必须可缓存，但缓存不能作为权威事实。

初期可以先实现简单 round-robin：

```text
leader_index = slot % validator_count
```

后续再演进为 stake-weighted schedule。

## 十一、finality 规划

本系统必须区分 confirmed 和 finalized。

1. `Confirmed`：当前 slot 的 proposal 或 skip 达到 quorum。
2. `Finalized`：满足最终确认规则，不应再回滚。

初期建议：

1. 先只实现 confirmed，避免把示例 QC 误当最终确认。
2. 后续增加两阶段或多阶段 finality 规则。
3. finality 规则必须显式写入状态机和测试。
4. RPC 对外必须区分 confirmed block 和 finalized block。

不得做的事：

1. 不得收到单个 leader proposal 就 finalized。
2. 不得本地超时就 finalized skip。
3. 不得把 P2P 大多数消息数量当成 stake quorum。

## 十二、P2P 接入

P2P 已预留：

```text
ProtocolHotStuffVoteV1
ProtocolHotStuffQCV1
```

后续接入方式：

1. `Vote.MarshalBinary` 作为 P2P message payload。
2. `QuorumCertificate.MarshalBinary` 作为 P2P message payload。
3. P2P frame 负责 message type、长度和 checksum。
4. 业务 payload 使用 Borsh。
5. 接收端按协议号分发到 consensus handler。

接收流程：

```text
1. p2p 读取 frame。
2. 校验 magic/version/length/checksum。
3. Borsh 解码 vote 或 QC。
4. EnsureEOF。
5. Validate。
6. 验签。
7. 查本地 validator set。
8. 投递给 consensus state machine。
```

## 十三、存储设计

共识状态必须可恢复。

建议存储对象：

```text
slot_state
proposal
vote_history
quorum_certificate
validator_set
leader_schedule_cache
finalized_checkpoint
```

写一致性要求：

1. proposal、vote、QC、slot state 必须在同一状态转换中原子写入。
2. 已 finalized 的 checkpoint 不得被普通回滚覆盖。
3. vote history 必须保留足够长时间，用于拒绝重复投票和冲突投票。
4. 数据库 raw bytes 必须带 schema envelope。

读一致性要求：

1. 读取最新链状态时必须优先读取 finalized checkpoint。
2. 读取 confirmed 状态时必须携带对应 QC。
3. 对外 RPC 不得把未确认 proposal 伪装成 confirmed block。

## 十四、安全边界

必须拒绝以下输入：

1. 空 validator。
2. 未知 validator。
3. 非法 slot。
4. 过早或过晚的远端 slot 消息。
5. 非当前 leader 的 proposal。
6. 父区块不存在的 proposal。
7. block hash 不匹配的 proposal。
8. skip vote 携带非空 block hash。
9. confirm vote 缺少 block hash。
10. 重复投票。
11. 冲突投票。
12. stake 与本地 validator set 不一致的投票。
13. 签名不合法的 vote、proposal、QC。
14. 超过最大长度的 Borsh 容器。
15. 未注册 schema 的历史 raw bytes。

## 十五、配置规划

建议新增共识配置：

```yaml
consensus:
  enabled: true
  initial_slot: 0
  slot_duration_ms: 400
  skip_timeout_ms: 250
  quorum_numerator: 2
  quorum_denominator: 3
  clock_drift_tolerance_ms: 1000
  validator_set_version: 1
```

约束：

1. `slot_duration_ms` 必须大于 0。
2. `skip_timeout_ms` 必须大于 0 且小于等于 `slot_duration_ms`。
3. quorum 必须满足 `0 < numerator <= denominator`。
4. validator set version 必须单调递增。

## 十六、开发阶段

阶段一：当前示例

1. slot clock。
2. skip timeout。
3. vote。
4. QC。
5. Borsh 编解码。
6. 单元测试。

阶段二：共识消息生产化

1. proposal 结构。
2. vote 签名。
3. QC 签名或签名列表。
4. schema registry 注册。
5. golden bytes 测试。

阶段三：P2P 接入

1. vote handler。
2. QC handler。
3. 去重缓存。
4. 高优先级广播。
5. 恶意输入测试。

阶段四：状态机和存储

1. slot state。
2. proposal store。
3. vote history。
4. QC store。
5. restart recovery。

阶段五：leader schedule 和 epoch

1. validator set。
2. epoch 边界。
3. deterministic leader schedule。
4. stake change activation。

阶段六：finality 和 fork choice

1. finalized checkpoint。
2. fork choice。
3. rollback 限制。
4. RPC commitment 区分。

## 十七、测试要求

必须覆盖：

1. slot 推进。
2. skip deadline。
3. quorum 向上取整。
4. confirm QC 生成。
5. skip QC 生成。
6. Borsh round trip。
7. 未知 validator。
8. 重复投票。
9. 冲突投票。
10. 非法 stake。
11. 非法 vote type。
12. 非法 QC voter list。
13. proposal leader 校验。
14. proposal parent 校验。
15. 重启恢复。
16. P2P 畸形 payload。
17. schema 版本兼容。

## 十八、禁止事项

1. 禁止使用 protobuf。
2. 禁止用 JSON 做 P2P 共识 payload。
3. 禁止把本地时间当成最终确认依据。
4. 禁止从网络投票载荷直接信任 stake。
5. 禁止没有签名就接受生产 vote、proposal、QC。
6. 禁止把 confirmed 和 finalized 混用。
7. 禁止把 `consensus/slot.go` 当前示例当成完整生产协议。
8. 禁止在没有 schema/version 的情况下持久化共识 raw bytes。

## 十九、最终目标

共识层最终要达到以下能力：

1. 节点可以用本地单调时钟稳定推进 slot。
2. leader 可以按确定性 schedule 出块。
3. validator 可以对有效 proposal 投票。
4. 超时 slot 可以通过 skip vote 推进。
5. 投票达到 stake quorum 后生成可验证 QC。
6. 节点可以通过 QC 同步确认状态。
7. finalized checkpoint 可以安全恢复。
8. 所有共识消息都能用 Borsh 确定性编码。
9. 所有关键状态都能审计、重放和测试。
