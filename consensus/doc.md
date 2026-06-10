# 共识层实现规划

版本：v1  
状态：设计草案 + 最小示例  
目标：本系统不使用 PoH 作为时钟，采用本地单调时钟、固定 slot 时间、超时 skip、投票确认的共识雏形，并逐步演进到可生产运行的共识层。

## 一、核心结论

本系统共识层先采用简单、可审计、可测试的设计：

1. 本地单调时钟负责推进 slot。
2. 每个 slot 有固定时长。
3. leader 在 slot 内提议区块。
4. 超过 skip timeout 未收到有效 proposal，验证者投 skip vote。
5. 收到有效 proposal，验证者投 confirm vote。
6. confirm vote 或 skip vote 达到 quorum 后生成 QC。
7. QC 是本系统推进 slot 状态的基础证明。
8. 当前 `consensus/slot.go` 只是示例，不代表完整生产共识协议。

当前不新建顶层 `Alpenglow` 目录。通用共识能力放在 `consensus/`；如果后续实现协议特定状态机，再放到 `consensus/alpenglow/`。

## 二、设计原则

1. 安全优先于活性。宁可 skip，也不能接受冲突投票或伪造 QC。
2. 时间只用于推进节奏，不直接证明历史顺序。
3. 链上事实必须由投票和 QC 确认，不由本地时间单独确认。
4. Borsh 是共识消息唯一二进制格式。
5. 网络字段不可信，所有 stake、validator、leader 信息必须以本地状态为准。
6. 每个验证者在同一 slot 的同一投票类型下只能选择一个结果。
7. 所有状态变化必须可落库、可重放、可审计。

## 三、当前已有示例

当前示例文件：

```text
consensus/
  errors.go
  slot.go
  slot_test.go
```

已具备能力：

1. `SlotClock`：根据 `startedAt + slotDuration` 计算当前 slot。
2. `SlotTick`：返回 slot 起始时间、skip 截止时间、是否需要 skip。
3. `Vote`：表达 confirm vote 或 skip vote。
4. `Quorum`：用整数分数计算确认阈值。
5. `QuorumCertificate`：表达达到阈值后的确认凭证。
6. `VoteCollector`：聚合本地验证者投票，拒绝重复投票和冲突投票。
7. `MarshalBinary/Unmarshal...`：投票和 QC 使用 Borsh 编解码。

当前没有实现的能力：

1. 没有签名校验。
2. 没有 leader schedule。
3. 没有 proposal 结构。
4. 没有 fork choice。
5. 没有 finality 状态机。
6. 没有 P2P handler 接入。
7. 没有数据库恢复流程。

## 四、时间模型

共识层不使用 PoH hash chain 作为时钟。

节点启动后维护本地单调时钟：

```text
slot = initial_slot + elapsed_monotonic_time / slot_duration
slot_start = started_at + (slot - initial_slot) * slot_duration
skip_deadline = slot_start + skip_timeout
```

要求：

1. 代码必须使用 Go `time.Time` 的单调时间差值推进 slot。
2. 不允许直接用墙上时间回拨影响 slot。
3. 网络消息里的时间只能作为观测字段，不能作为共识事实。
4. 每个 slot 的开始和 skip deadline 必须可重复推导。
5. 后续需要增加 `clock_drift_tolerance`，用于拒绝明显过早或过晚的远端消息。

推荐默认值：

```text
slot_duration: 400ms
skip_timeout: 250ms
quorum: 2/3 stake
```

默认值只是开发阶段配置，生产环境必须通过链配置或创世配置固定。

## 五、slot 状态机

生产版本建议维护以下状态：

```text
Open
  -> Proposed
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
4. `Confirmed`：confirm vote 达到 quorum，生成 block QC。
5. `Skipped`：skip vote 达到 quorum，生成 skip QC。
6. `Finalized`：满足最终确认规则，区块不可回滚。

当前示例只做到 `Confirmed/Skipped` 的 QC 生成，不实现 `Finalized`。

## 六、slot 执行流程

每个 slot 的核心流程：

```text
1. 本地时钟进入 slot。
2. 判断本节点是否为当前 slot leader。
3. leader 构造 proposal 并广播。
4. validator 收到 proposal 后校验 leader、parent、block hash、交易、签名。
5. proposal 有效则投 confirm vote。
6. 到达 skip timeout 仍无有效 proposal，则投 skip vote。
7. 收集 vote，达到 quorum 后生成 QC。
8. 广播 QC。
9. 根据 QC 更新 slot 状态。
10. 进入下一个 slot。
```

注意：

1. 本地超时只能触发 skip vote，不能直接判定 slot skipped。
2. slot skipped 必须由 skip QC 证明。
3. 区块 confirmed 必须由 confirm QC 证明。
4. finalized 必须由后续 finality 规则证明，不能和 confirmed 混用。

## 七、proposal 设计

后续需要新增 `Proposal` 结构，建议字段：

```text
version u16
slot u64
parent_slot u64
block_height u64
block_hash [32]byte
parent_hash [32]byte
leader [32]byte
justify_qc bytes
payload_hash [32]byte
created_at_unix_milli i64
signature [64]byte
```

校验规则：

1. `slot` 必须等于 proposal 所属 slot。
2. `leader` 必须匹配本地 leader schedule。
3. `parent_slot` 必须小于 `slot`。
4. `parent_hash` 必须能连接本地已知链状态。
5. `block_hash` 必须等于区块头确定性 hash。
6. `justify_qc` 必须能证明父状态可作为扩展基础。
7. `signature` 必须使用 leader 公钥验签。
8. Borsh 解码后必须 `Validate`，并执行 `EnsureEOF`。

## 八、投票设计

当前示例已有 `Vote`：

```text
type
slot
block_hash
voter_id
stake
created_at_unix_milli
```

生产版本需要调整：

1. `stake` 只允许作为本地调试字段，网络投票权重必须来自本地 validator set。
2. `voter_id` 应替换或绑定到 validator public key。
3. 必须增加 `signature`。
4. 签名原文必须包含 domain separator、version、vote type、slot、block hash。
5. skip vote 的 block hash 必须为空。
6. confirm vote 的 block hash 必须非空。

投票限制：

1. 同一 validator 在同一 slot 的 confirm vote 只能选择一个 block hash。
2. 同一 validator 在同一 slot 的 skip vote 只能投一次。
3. 收到冲突投票必须记录证据，后续用于惩罚或审计。
4. 未知 validator 的投票必须拒绝。
5. stake 不匹配本地 validator set 的投票必须拒绝。

## 九、QC 设计

QC 是 quorum certificate，表示某一类投票达到阈值。

当前示例已有 `QuorumCertificate`：

```text
type
slot
block_hash
threshold_stake
confirmed_stake
voters
created_at_unix_milli
```

生产版本需要增加：

1. validator bitmap 或紧凑 voter index。
2. 聚合签名或签名列表。
3. validator set version。
4. epoch。
5. QC hash。

QC 校验规则：

1. QC 类型必须合法。
2. confirm QC 必须绑定 block hash。
3. skip QC 必须使用空 block hash。
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
