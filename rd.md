# Solana Golang 总体设计文档

版本：v3  
状态：架构基线  
适用范围：共识、P2P 网络、存储、序列化、区块结构、节点启动、RPC、可观测性和后续开发路线。

## 1. 系统定位

本项目是一个自研公链原型，目标是构建一套可审计、可测试、可逐步生产化的区块链基础设施。

核心方向：

1. 不使用 libp2p。
2. 不使用 protobuf。
3. P2P 传输使用 Go 标准库 TCP 和 `quic-go`。
4. 节点间二进制消息使用 Borsh。
5. 链上结构、共识消息、数据库 raw payload 使用确定性二进制编码。
6. 外部 HTTP/RPC 使用 JSON DTO。
7. 共识时钟不使用 PoH，采用本地单调时钟、固定 slot、超时 skip 和投票 QC。
8. 存储优先使用 Pebble，保留 LevelDB 兼容能力。

系统设计目标：

1. 正确性优先：链上状态、共识状态、存储索引必须一致。
2. 性能可控：网络、存储、执行、共识都必须有边界和限流。
3. 安全可审计：所有关键消息必须可验证、可重放、可追踪。
4. 实现简单：优先清晰直接的 Go domain model，避免过早抽象。
5. 逐步生产化：当前允许原型，但每个模块要明确生产缺口。

## 2. 总体架构

```text
cmd
  -> config
  -> database
  -> schema registry
  -> p2p host
  -> consensus
  -> rpc

p2p
  -> TCP / QUIC transport
  -> frame
  -> Borsh message
  -> protocol handler

consensus
  -> slot clock
  -> proposal
  -> vote
  -> QC
  -> slot state

structure
  -> block
  -> transaction
  -> account
  -> hash/signature/public key

database
  -> Pebble / LevelDB
  -> table namespace
  -> read snapshot
  -> atomic write batch
  -> migration

schema + codec/borsh
  -> versioned raw bytes
  -> payload hash
  -> historical decoder
```

模块职责：

| 模块 | 职责 |
| --- | --- |
| `cmd` | 启动入口、配置装配、数据库初始化、节点身份加载、P2P/RPC 生命周期 |
| `config` | YAML 配置解析、默认值、启动参数校验 |
| `p2p` | 自研 P2P 层，包含 TCP、QUIC、连接、心跳、KAD、协议分发 |
| `consensus` | slot 时钟、skip、投票、QC、后续 proposal/finality 状态机 |
| `structure` | 区块、交易、账户、哈希、公钥、签名等核心事实模型 |
| `codec/borsh` | Borsh 基础编解码工具 |
| `schema` | raw bytes envelope、schema registry、历史版本解码 |
| `database` | 本地 KV 存储、表命名空间、事务、迁移、快照读 |
| `rpc` | JSON-RPC 外部接口、DTO 转换、请求边界校验 |
| `utils` | 无业务状态的通用工具，如日志、编码、哈希、ed25519 |
| `monitor` | 后续承载指标、延迟、错误率、资源使用观测 |
| `netsync` | 后续承载历史区块、状态、缺口补齐同步 |
| `vm` | 后续承载交易执行和状态转换 |

## 3. 核心数据流

### 3.1 启动数据流

```text
读取 YAML 配置
  -> 校验 config
  -> 初始化 slog
  -> 打开数据库
  -> 执行数据库 migration
  -> 注册 schema
  -> 检查或创建节点身份
  -> 创建 P2P Host
  -> 注册协议 handler
  -> 启动 P2P listener
  -> 启动 RPC server
  -> 等待关闭信号
```

启动要求：

1. 配置必须先校验再使用。
2. 数据库必须先迁移再读写业务数据。
3. 核心 schema 缺失时节点必须启动失败。
4. P2P peer id 必须来自数据库身份或启动时创建的身份。
5. 节点身份创建使用 `utils/ed25519.go` 生成 32 字节公钥和 32 字节私钥。
6. 私钥不得出现在日志和 RPC 响应中。

### 3.2 交易到区块数据流

```text
P2P/RPC 收到交易
  -> 解析 Borsh 或 JSON DTO
  -> 转换为 Transaction domain model
  -> Validate
  -> 进入交易池
  -> leader 按 slot 选择交易
  -> 执行交易
  -> 生成 block
  -> 共识 proposal
  -> validator vote
  -> QC
  -> 写入 block/header/index/state
  -> 广播 block/QC
```

约束：

1. 交易进入业务层后必须以 Go domain struct 为权威事实。
2. 区块 hash 必须来自确定性序列化结果。
3. 交易执行结果必须可重复计算。
4. 未确认交易不能写入 finalized 视图。

### 3.3 共识数据流

```text
SlotClock.Tick
  -> 当前 slot
  -> 判断 leader
  -> proposal 或等待 proposal
  -> confirm vote 或 skip vote
  -> VoteCollector
  -> QuorumCertificate
  -> slot state update
  -> storage commit
  -> P2P broadcast
```

关键规则：

1. 本地超时只能触发 skip vote，不能直接确认 skip。
2. slot skipped 必须由 skip QC 证明。
3. 区块 confirmed 必须由 confirm QC 证明。
4. finalized 必须由后续 finality 规则证明。

## 4. 序列化架构

系统不使用 protobuf。

序列化边界：

| 场景 | 格式 |
| --- | --- |
| P2P frame header | 固定二进制字段 |
| P2P message body | Borsh |
| P2P 业务 payload | Borsh |
| 区块、交易、共识结构 | Borsh 或基于 Borsh 的固定字段顺序 |
| 数据库 raw payload | schema envelope + Borsh payload |
| 外部 HTTP/RPC | JSON DTO |
| 安全 hash/sign | canonical payload |

设计理由：

1. Borsh 字段顺序确定，适合区块链 hash、签名、跨语言验证。
2. protobuf 适合复杂 API 演进，但本项目当前协议结构不复杂。
3. 保留 protobuf 会引入 proto 文件、生成代码、DTO/domain/Borsh 三层转换。
4. 每个核心结构只维护一套 `MarshalBinary/Unmarshal...`，降低审计成本。

硬性规则：

1. Borsh 解码后必须 `EnsureEOF`。
2. Borsh 解码后必须 `Validate`。
3. 动态 bytes/string 必须有最大长度。
4. 历史 raw bytes 必须带 `type/version/codec/schema_id/payload_hash`。
5. JSON 不参与 P2P、链上编码、共识 hash、签名原文。
6. 安全签名必须包含 domain separator、message type、version、canonical payload。

## 5. 共识层设计

详细共识文档见 `consensus/doc.md`。

当前总方向：

1. 不使用 PoH 作为时钟。
2. 使用本地单调时钟推进 slot。
3. slot 使用固定时长，开发默认值为 400ms。
4. slot 内未收到有效 proposal，超过 skip timeout 后投 skip vote。
5. 收到有效 proposal 后投 confirm vote。
6. 达到 stake quorum 后生成 QC。
7. confirmed 和 finalized 必须区分。

当前已有代码：

1. `SlotClock`：计算当前 slot 和 skip deadline。
2. `Vote`：表达 confirm vote 和 skip vote。
3. `Quorum`：计算 stake 阈值。
4. `QuorumCertificate`：表达投票达到阈值后的证明。
5. `VoteCollector`：聚合验证者投票，拒绝重复和冲突投票。

生产化必须补齐：

1. `Proposal`：leader 提议区块。
2. `LeaderSchedule`：确定性 leader 选择。
3. `ValidatorSet`：验证者集合、stake、版本、epoch。
4. `VoteSignature`：投票签名和验签。
5. `QCSignature`：签名列表或聚合签名。
6. `SlotStateStore`：slot 状态落库和恢复。
7. `ForkChoice`：分叉选择和回滚边界。
8. `Finality`：最终确认规则。
9. `P2P Handler`：proposal/vote/QC 网络处理。

slot 状态建议：

```text
Open
  -> Proposed
  -> Confirmed
  -> Finalized

Open
  -> SkipVoted
  -> Skipped
```

共识安全规则：

1. 未知 validator 的投票必须拒绝。
2. 网络投票中的 stake 不可信，真实 stake 来自本地 validator set。
3. 同一 validator 在同一 slot 的同一投票类型只能选择一个结果。
4. confirm vote 必须绑定非空 block hash。
5. skip vote 必须使用空 block hash。
6. QC 的 voter 列表必须排序、唯一、可验签。
7. 本地时间不构成最终确认依据。

关于 Alpenglow：

1. 当前不新建顶层 `Alpenglow` 目录。
2. 通用共识能力保留在 `consensus/`。
3. 如果后续实现协议特定状态机，使用 `consensus/alpenglow/`。
4. 协议名称不能替代工程边界，proposal、vote、QC、slot clock 仍应清晰拆分。

## 6. 网络层设计

网络层目标是提供可控、可观测、可扩展的自研 P2P 能力。

基本原则：

1. 不使用 libp2p。
2. TCP 使用 Go 标准库。
3. QUIC 使用 `quic-go`。
4. P2P message body 使用 Borsh。
5. KAD 只负责发现和路由，不负责身份授权。
6. 共识消息必须高优先级处理。

### 6.1 网络地址

系统使用 multi-address 风格表达节点地址：

```text
/ip4/127.0.0.1/tcp/5002/p2p/{peer_id}
/ip4/127.0.0.1/quic/5002/p2p/{peer_id}
```

配置示例：

```yaml
p2p:
  ip_type: "ip4"
  listen_ip: "0.0.0.0"
  listen_port: 5002
  default_protocol: "tcp"
  max_peers: 64
  network_id: "solana_golang:localnet:00000000000000000000000000000000"
  software_version: "solana_golang/0.1.0"
  min_outbound_peers: 8
  bootstrap_timeout_millis: 5000
```

peer id 规则：

1. 配置文件不保存 peer id。
2. 启动时必须检查数据库是否已有节点身份。
3. 数据库没有身份时，通过 ed25519 生成公钥/私钥并保存。
4. peer id 由数据库中的节点公钥 base58 派生。
4. 网络身份以数据库身份为准。
5. 私钥只能本地保存，不进入 P2P 消息。

### 6.2 P2P frame

P2P 使用两层结构：

```text
frame header
  magic
  protocol_version
  message_type
  payload_length
  checksum

message body
  Borsh fields
```

frame 职责：

1. 快速识别非法流量。
2. 限制 payload 长度。
3. 校验 checksum。
4. 提供 message type 路由。

message body 职责：

1. 表达业务消息。
2. 携带 request/response 语义。
3. 携带 peer route 字段。
4. 使用 Borsh 保持确定性。

### 6.3 协议注册

协议必须注册到白名单。

当前协议类别：

1. ping/pong。
2. handshake。
3. block receive/query。
4. transaction receive。
5. peer hints。
6. node status。
7. find node request/response。
8. consensus vote。
9. consensus QC。

新增协议流程：

1. 在 `p2p/protocol.go` 定义 `ProtocolID`。
2. 在 `DefaultProtocolSpecs` 注册协议名、响应语义、优先级。
3. 定义 payload domain struct。
4. 实现 Borsh 编解码和 Validate。
5. 注册 schema。
6. 注册 handler。
7. 写 frame、decode、handler、异常输入测试。

### 6.4 连接管理

Host 负责：

1. transport 注册。
2. peer 表管理。
3. connection 池管理。
4. 连接状态记录。
5. 心跳。
6. 失败计数。
7. idle 清理。
8. 协议分发。

连接状态至少包含：

```text
peer_id
connection_id
protocol
local_address
remote_address
connected_at
last_read
last_write
last_heartbeat
failure_count
```

心跳要求：

1. 周期性发送 ping。
2. pong 超时计失败。
3. 连续失败超过阈值关闭连接。
4. 心跳不允许阻塞共识消息处理。
5. 心跳日志必须包含 peer_id、connection_id、protocol。

### 6.5 Kademlia DHT

KAD 用于节点发现和邻居维护。

当前设计：

1. 32 字节 peer id 空间。
2. 256 个 bucket。
3. bucket 默认容量 20。
4. bucket 满时按节点质量淘汰或进入候选集合。
5. 节点质量包含成功次数、失败次数、连续失败、最近活跃时间。

DHT 边界：

1. DHT 发现的节点不自动获得 validator 权限。
2. DHT 返回的数据必须经过 hash、签名或 schema 校验。
3. DHT 不能替代共识层 validator set。
4. DHT 需要防 Sybil、坏路由和地址投毒。

### 6.6 网络优先级

建议优先级：

| 优先级 | 消息 |
| --- | --- |
| High | ping/pong、handshake、vote、QC、proposal |
| Normal | transaction、block receive、peer hints、node status |
| Low | 历史区块同步、批量状态同步 |

QUIC 场景下后续应使用独立 stream 或队列区分共识消息和大块同步数据。

## 7. 存储层设计

存储层目标是提供可靠的本地状态、索引、raw bytes 审计和重启恢复能力。

当前支持：

1. Pebble。
2. LevelDB。
3. 表命名空间。
4. 批量写。
5. 读事务快照。
6. 数据库 migration。
7. checkpoint。
8. cache policy。

### 7.1 表职责

当前主要表：

| 表 | 职责 |
| --- | --- |
| `TableAccount` | 账户状态 |
| `TableChain` | 链元信息 |
| `TableBlock` | 完整区块 |
| `TablePeer` | 节点信息 |
| `TableHeightToHash` | 高度到 hash 索引 |
| `TableHashToHeight` | hash 到高度索引 |
| `TableBlockHeader` | 区块头 |
| `TableBlockBody` | 区块体 |
| `TableTxToBlock` | 交易到区块索引 |
| `TableOrphan` | 临时孤块 |
| `TableCheckpoint` | 检查点 |

后续建议增加：

1. `TableConsensusSlot`：slot state。
2. `TableConsensusVote`：投票历史。
3. `TableConsensusQC`：QC。
4. `TableValidatorSet`：验证者集合。
5. `TableSchemaPayload`：schema envelope raw bytes 索引。

### 7.2 写一致性

必须原子写入的组合：

1. block header、block body、height index、hash index。
2. transaction index 和 block index。
3. proposal、vote/QC、slot state。
4. finalized checkpoint 和 chain head。
5. raw payload 和 payload hash 元信息。

写入原则：

1. 多表更新必须使用 `DataTransaction` 或等价原子批量写。
2. 写入前必须完成 Validate。
3. 写入 raw bytes 前必须完成 schema envelope 构造。
4. 已 finalized 数据不能被普通分叉回滚覆盖。
5. migration 必须单调递增并可审计。

### 7.3 读一致性

读取原则：

1. 跨多 key 查询必须使用 read transaction snapshot。
2. RPC 读取链状态必须明确 commitment。
3. finalized 读取只能来自 finalized checkpoint 或已证明状态。
4. confirmed 读取必须携带 QC。
5. 未确认 proposal 不得伪装为 confirmed block。

典型读取：

```text
GetBlock(slot)
  -> read snapshot
  -> height_to_hash
  -> block_header
  -> block_body
  -> qc/status
  -> build RPC DTO
```

### 7.4 key 设计

key 必须稳定、可排序、可前缀扫描。

建议：

```text
block:hash:{hash}
block:height:{big_endian_height}
tx:block:{tx_hash}
slot:state:{big_endian_slot}
slot:vote:{big_endian_slot}:{vote_type}:{validator_id}
slot:qc:{big_endian_slot}:{qc_type}
validator:set:{big_endian_epoch}
schema:{type}:{version}:{schema_id}:{payload_hash}
```

要求：

1. 数值索引用 big endian，保证字典序等于数值序。
2. hash 使用固定长度 bytes 或 hex 文本，不能混用。
3. key 前缀必须集中定义，避免散落拼接。
4. 删除范围必须先验证前缀，避免误删。

## 8. 区块与交易结构

区块结构由 `structure` 包承载。

区块头关键字段：

1. version。
2. slot。
3. parent slot。
4. block height。
5. parent hash。
6. previous blockhash。
7. blockhash。
8. transactions root。
9. accounts hash。
10. state root。
11. rewards hash。
12. entries hash。
13. timestamp。
14. leader。
15. transaction count。

区块规则：

1. `ParentSlot < Slot`。
2. `Blockhash` 不能为空。
3. `Leader` 不能为空。
4. `TimestampUnix` 必须为正。
5. `TransactionCount` 必须和交易数量一致。
6. `TransactionsRoot` 必须由交易 hash 计算得出。
7. runtime meta 不参与区块 hash。

交易规则：

1. 交易签名必须覆盖明确的 canonical payload。
2. 交易 hash 不应使用 JSON 或 protobuf。
3. 交易排序由共识 proposal 决定。
4. 执行层必须在相同输入下产出相同状态根。

## 9. 执行层规划

当前 `vm` 目录是后续执行层承载位置。

执行层职责：

1. 校验交易基本合法性。
2. 检查账户、余额、nonce、权限。
3. 执行指令或合约。
4. 生成 receipt。
5. 更新账户状态。
6. 计算 state root、accounts hash、transactions root。

执行层禁止：

1. 依赖本地墙上时间产生执行结果。
2. 依赖本地随机数产生执行结果。
3. 直接访问外部网络影响执行结果。
4. 使用非确定性 map 遍历结果生成 hash。
5. 绕过共识层决定交易顺序。

后续实现顺序：

1. 先实现最小账户模型和转账交易。
2. 再实现 instruction 执行。
3. 再实现合约 VM。
4. 最后实现并行执行和冲突检测。

## 10. RPC 层设计

RPC 使用 JSON DTO，只负责外部访问。

职责：

1. 解析请求。
2. 校验参数。
3. 调用 domain service。
4. 转换响应 DTO。
5. 输出结构化错误。

边界：

1. JSON DTO 不能直接进入链上编码。
2. JSON DTO 不能作为 hash/sign 原文。
3. RPC 不能绕过共识状态读取未确认数据。
4. 大请求必须受 `max_body_bytes` 限制。
5. batch 必须受 `max_batch_size` 限制。

RPC commitment 建议：

```text
processed: 本地已处理，可能回滚
confirmed: 有 QC，可能被最终规则回滚
finalized: 已最终确认，不应回滚
```

## 11. 节点身份与密钥

节点身份是 P2P 和共识安全的基础。

设计：

1. 节点启动时先读取数据库身份。
2. 没有身份时生成 ed25519 公私钥。
3. 公钥用于 peer id 或 peer id 派生。
4. 私钥只保存在本地数据库或后续安全密钥存储。
5. 配置文件里的 peer id 不能覆盖数据库里的真实身份。

安全要求：

1. 私钥不得写入日志。
2. 私钥不得通过 RPC 返回。
3. 私钥不得放入 P2P 消息。
4. 身份不一致时启动应失败或明确告警。
5. 后续生产环境应支持加密私钥和密钥轮换。

## 12. 配置设计

当前配置：

```yaml
rpc:
  address: ":8899"
  max_body_bytes: 1048576
  max_batch_size: 32

log:
  level: "info"
  format: "json"
  output: "console"
  file_path: "./logs/solana_golang.log"

database:
  engine: "pebble"
  path: "./data/pebble"
  wal: true

p2p:
  ip_type: "ip4"
  listen_ip: "0.0.0.0"
  listen_port: 5002
  default_protocol: "tcp"
  max_peers: 64
  network_id: "solana_golang:localnet:00000000000000000000000000000000"
  software_version: "solana_golang/0.1.0"
  min_outbound_peers: 8
  bootstrap_timeout_millis: 5000
```

建议新增：

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

配置要求：

1. 所有外部可调字段必须有 Validate。
2. 所有时间配置必须明确单位。
3. 所有大小配置必须明确字节或 MB。
4. 默认值必须能本地启动。
5. 生产配置不得使用默认测试 peer id。

## 13. 可观测性

日志使用 `slog`。

关键日志：

1. 节点启动和关闭。
2. 配置加载。
3. 数据库打开、迁移、关闭。
4. 节点身份创建和加载。
5. P2P listener 启动。
6. peer 连接、断开、心跳失败。
7. P2P 解码失败和协议分发失败。
8. slot 推进。
9. proposal 接收和拒绝原因。
10. vote 接收和拒绝原因。
11. QC 生成和验证失败。
12. 区块写入和 finalized checkpoint 更新。

建议指标：

| 指标 | 含义 |
| --- | --- |
| `slot_lag_ms` | 本地 slot 推进延迟 |
| `proposal_latency_ms` | proposal 从 slot start 到接收耗时 |
| `vote_latency_ms` | vote 聚合耗时 |
| `qc_latency_ms` | QC 生成耗时 |
| `p2p_connections` | 当前连接数 |
| `p2p_heartbeat_failures` | 心跳失败次数 |
| `p2p_decode_errors` | 网络解码错误 |
| `db_write_latency_ms` | 数据库写入耗时 |
| `db_read_latency_ms` | 数据库读取耗时 |
| `block_execute_latency_ms` | 区块执行耗时 |

日志要求：

1. 关键路径必须带 slot、block_hash、peer_id、protocol。
2. 错误日志必须带可定位上下文。
3. 不得记录私钥、签名原文、敏感配置。
4. 高频日志必须限流或降级为 debug。

## 14. 安全设计

输入边界：

1. P2P frame 长度。
2. Borsh 容器长度。
3. RPC body 大小。
4. RPC batch 大小。
5. peer id 格式。
6. multi-address 格式。
7. block/transaction/proposal/vote/QC 字段。
8. 数据库 key 前缀和范围。

拒绝规则：

1. 未注册协议拒绝。
2. checksum 不匹配拒绝。
3. Borsh trailing bytes 拒绝。
4. 未知 schema 拒绝。
5. 未知 validator 拒绝。
6. 非 leader proposal 拒绝。
7. 重复投票拒绝。
8. 冲突投票拒绝并记录证据。
9. 无效签名拒绝。
10. 超出 slot drift tolerance 的共识消息拒绝或降级处理。

攻击面：

1. P2P 大包耗尽内存。
2. 心跳放大。
3. DHT 地址投毒。
4. Sybil 节点污染 routing table。
5. 重复交易刷池。
6. 冲突投票扰乱共识。
7. 数据库范围删除误操作。
8. RPC batch 放大。

应对：

1. 明确最大长度。
2. 协议白名单。
3. 节点质量评分。
4. 连接失败惩罚。
5. 交易去重。
6. vote history。
7. 原子写和 checkpoint。
8. 参数校验和日志审计。

## 15. 测试策略

必须覆盖：

1. Borsh golden bytes。
2. Borsh round trip。
3. schema envelope hash mismatch。
4. P2P frame checksum mismatch。
5. P2P payload 超长。
6. TCP/QUIC 连接和心跳。
7. KAD bucket 淘汰。
8. slot clock 推进。
9. skip QC。
10. confirm QC。
11. 重复投票。
12. 冲突投票。
13. 数据库事务原子性。
14. 数据库 read snapshot 一致性。
15. migration 重启后幂等。
16. 区块 hash 确定性。
17. 交易 root 校验。
18. RPC 参数错误。

测试分层：

| 层级 | 目标 |
| --- | --- |
| 单元测试 | 编解码、校验、纯函数、边界条件 |
| 集成测试 | P2P、数据库、RPC、共识 handler |
| 回归测试 | golden bytes、schema 历史版本 |
| 压力测试 | 大量连接、批量消息、数据库批写 |
| 故障测试 | 断连、重连、重复消息、乱序消息 |

## 16. 当前实现状态

已完成或已有雏形：

1. 配置加载和校验。
2. 结构化日志。
3. Pebble/LevelDB 存储抽象。
4. 数据库 migration。
5. P2P Host。
6. TCP transport。
7. QUIC transport。
8. P2P frame + Borsh message。
9. KAD routing table。
10. schema registry。
11. codec/borsh。
12. 区块结构和确定性序列化。
13. 共识 slot/vote/QC 示例。
14. 节点身份创建和加载。

仍需生产化：

1. consensus proposal。
2. leader schedule。
3. validator set。
4. 共识签名。
5. consensus handler 接入 P2P。
6. slot state 落库。
7. finalized checkpoint。
8. 交易池。
9. 执行层状态机。
10. netsync 历史同步。
11. 完整 RPC 查询。
12. 监控指标。

## 17. 开发路线图

阶段一：协议和存储基线

1. 完成 Borsh/schema 规范。
2. 完成 P2P frame/message。
3. 完成节点身份。
4. 完成数据库 migration。
5. 完成区块基础结构。

阶段二：共识最小闭环

1. consensus config。
2. validator set。
3. leader schedule。
4. proposal。
5. signed vote。
6. QC。
7. slot state store。
8. P2P vote/QC/proposal handler。

阶段三：链状态闭环

1. 交易池。
2. 最小账户模型。
3. 区块执行。
4. state root。
5. block commit。
6. confirmed/finalized 查询。

阶段四：网络同步

1. peer discovery。
2. node status。
3. block headers sync。
4. block body sync。
5. common ancestor query。
6. checkpoint sync。

阶段五：生产强化

1. 签名聚合或签名压缩。
2. 惩罚证据。
3. 防 Sybil。
4. 资源限流。
5. metrics。
6. 压测。
7. 故障恢复演练。

## 18. 关键禁止事项

1. 禁止引入 protobuf。
2. 禁止使用 libp2p。
3. 禁止用 JSON 做 P2P 或链上核心编码。
4. 禁止直接信任网络消息里的 stake。
5. 禁止没有签名就接受生产共识消息。
6. 禁止将本地时间作为最终确认依据。
7. 禁止把 confirmed 当 finalized。
8. 禁止绕过 schema registry 持久化 raw bytes。
9. 禁止多表状态更新不使用原子写。
10. 禁止把私钥写入日志、RPC、P2P 消息。

## 19. 总结

本系统的主线是：

```text
自研 P2P
  + TCP/quic-go
  + Borsh
  + Schema Registry
  + Pebble/LevelDB
  + 本地单调 slot
  + skip/vote/QC
  + 明确 confirmed/finalized
```

当前代码已经具备网络、存储、序列化和共识示例的基础。下一步应优先把共识从示例推进到最小可运行闭环：validator set、leader schedule、proposal、签名 vote、QC、slot state store 和 P2P handler。只有完成这些，系统才具备真正的区块确认能力。
