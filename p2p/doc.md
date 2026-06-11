# P2P 模块说明

## 1. 当前目标

`p2p` 模块负责节点之间的连接、拨号、消息收发和传输协议选择。上层模块不直接依赖 TCP 或 QUIC，而是通过 `Host`、`Transport`、`Connection` 和 `Message` 这组接口访问网络能力。

当前基础架构已经拆成以下文件：

| 文件 | 职责 |
| --- | --- |
| `transport.go` | 定义 `Transport`、`Connection` 和 `ConnectionHandler` |
| `tcp_transport.go` | 实现 TCP 监听、拨号、连接读写和长度前缀消息帧 |
| `quic_transport.go` | 保留 QUIC 传输适配器边界 |
| `host.go` | 管理传输实现、节点表、连接池、拨号和发送 |
| `peer.go` | 定义节点 ID、地址列表、状态、角色、能力和快照 |
| `message.go` | 定义 P2P 二进制消息、消息类型、编码和解码 |
| `protocol.go` | 定义协议编号、协议名、响应语义和消息优先级 |
| `protocol_registry.go` | 管理协议注册、处理器绑定和请求响应校验 |
| `errors.go` | 定义 P2P 层统一错误 |

## 2. 分层边界

P2P 模块分为三层：

| 层级 | 主要职责 |
| --- | --- |
| Host 层 | 管理节点、连接池、协议选择、发送和广播 |
| Transport 层 | 实现具体传输协议的监听、拨号和连接创建 |
| Connection 层 | 负责单连接上的消息读写、关闭和远端信息 |

上层业务只应该依赖 `Host`：

```go
message, err := p2p.NewMessage(p2p.MessageTypePing, []byte("hello"))
if err != nil {
	return err
}
return host.Send(ctx, peerID, message)
```

交易池、共识、区块同步和 DHT 都不应该直接调用 TCP 或 QUIC 实现。

协议处理也统一挂到 `Host`：

```go
err := host.RegisterResultHandler(
	p2p.ProtocolSpec{
		ID:          p2p.ProtocolPingV1,
		Name:        "/p2p/ping/1.0.0",
		HasResponse: true,
		Priority:    p2p.MessagePriorityHigh,
	},
	func(ctx context.Context, request p2p.Message) (p2p.Message, error) {
		return p2p.NewResponseMessage(host.PeerID(), p2p.MessageTypePong, request.ID, []byte("pong"))
	},
)
```

## 3. 地址格式

当前 multi-address 只允许 TCP 和 QUIC：

```text
/ip4/127.0.0.1/tcp/9001/p2p/<peer_id>
/ip4/127.0.0.1/quic/9002/p2p/<peer_id>
```

`peer_id` 必须是 Base58 编码后的 32 字节节点 ID。节点可以同时保存多个地址，`Host` 默认按 `QUIC -> TCP` 的顺序尝试拨号。QUIC 当前没有接入底层实现时，会返回 `ErrTransportUnavailable`，Host 可以继续尝试 TCP。

## 4. 当前已实现能力

当前代码已经具备以下基础能力：

- TCP 监听。
- TCP 拨号。
- 长度前缀消息帧。
- 固定二进制消息头。
- 协议编号路由。
- 协议注册表。
- 请求响应 ID 关联。
- 最大消息大小限制。
- 节点 ID 校验。
- 节点地址归属校验。
- 节点角色、能力、同步高度和状态快照。
- 连接池保存。
- 按协议选择传输实现。
- QUIC 不可用时的明确错误。
- Host 发送消息时自动补齐来源、目标、消息 ID 和创建时间。

## 5. 消息头格式

消息使用 `4 字节帧长度 + 固定消息头 + payload` 的格式。帧长度和头部整数均为大端序。

固定消息头字段：

| 字段 | 长度 | 说明 |
| --- | --- | --- |
| version | 2 字节 | 消息协议版本 |
| from_peer_id | 32 字节 | 发送方节点 ID，Base58 解码后的原始字节 |
| to_peer_id | 32 字节 | 目标节点 ID，空目标使用全零 |
| message_id | 16 字节 | 时间有序消息 ID |
| request_id | 16 字节 | 请求响应关联 ID，普通消息使用全零 |
| flag | 1 字节 | `0` 请求或普通消息，`1` 响应消息 |
| protocol | 4 字节 | 协议编号 |
| created_at | 8 字节 | 创建时间，毫秒时间戳 |
| payload_length | 4 字节 | payload 长度 |

这样做的原因：

- 固定头部减少字段解析成本。
- 协议编号可以直接作为注册表 key。
- 请求响应 ID 可以在并发连接中稳定配对。
- 发送方和目标节点 ID 进入消息头，便于连接复用和错误路由检查。
- payload 仍保持字节数组，方便后续接入 Borsh 或固定字段布局。

## 6. QUIC 后续接入方式

Go 标准库没有直接提供 QUIC 传输实现，因此 `quic_transport.go` 当前只保留稳定适配器边界。后续接入真实 QUIC 时，不需要修改上层业务，只需要替换 `QUICTransport` 内部实现。

建议步骤：

1. 选择维护活跃、接口稳定的 QUIC 库。
2. 在 `QUICTransportConfig` 中加入 TLS 配置、ALPN、握手超时、流数量上限和空闲超时。
3. 使用节点私钥派生或签发节点证书，证书只用于传输加密，节点身份仍以 `peer_id` 和握手签名为准。
4. `Listen` 中启动 QUIC listener，接收 session 后打开控制流。
5. `Dial` 中建立 QUIC session，并完成节点握手。
6. 为 `Connection` 增加 QUIC stream 管理，按消息优先级选择不同 stream。
7. 保持 `Connection.ReadMessage` 和 `Connection.WriteMessage` 接口不变。

建议 ALPN：

```text
solana-golang-p2p/1
```

## 7. 握手协议建议

当前基础架构还没有强制握手。后续需要在连接建立后增加握手消息，避免伪造节点身份。

握手消息建议包含：

- 协议版本。
- 链 ID。
- 创世块哈希。
- 本地 `peer_id`。
- 本地可公开地址列表。
- 当前时间戳。
- 随机 nonce。
- 对握手摘要的签名。

握手校验必须满足：

- `peer_id` 与公钥一致。
- 签名有效。
- 链 ID 和创世块哈希一致。
- 时间戳未明显偏离本地时间。
- nonce 未重复使用。
- 地址中的 `peer_id` 与握手身份一致。

## 8. 协议注册建议

协议注册必须遵守以下规则：

- 协议编号必须稳定，不允许随意复用。
- 协议名必须规范化，统一使用小写路径。
- 有响应协议必须通过 `RegisterResultHandler` 注册。
- 无响应协议必须通过 `RegisterVoidHandler` 注册。
- 有响应处理器返回的消息必须是响应消息，且 `request_id` 必须等于原请求的 `message_id`。
- 注册表不自动注册业务处理器，避免空处理器吞掉真实业务错误。

当前建议的协议范围：

| 协议 | 用途 |
| --- | --- |
| `ping/pong` | 探活和 RTT 测量 |
| `handshake` | 身份、链 ID、创世块哈希和能力交换 |
| `find-node` | DHT 邻近节点发现 |
| `peer-hints` | 轻量节点提示 |
| `node-status` | 同步高度、角色和能力传播 |
| `transaction/receive` | 交易传播 |
| `block/receive` | 区块传播 |
| `block/query-*` | 历史区块和区块头查询 |
| `hotstuff/vote` | 共识投票传播 |
| `hotstuff/qc` | 共识证书传播 |

## 9. 消息优先级建议

后续建议把消息分为三个优先级：

| 优先级 | 消息类型 | 处理策略 |
| --- | --- | --- |
| 高 | vote、qc、view-change | 小包、低延迟、独立队列 |
| 中 | transaction、block-header | 批量发送、限速重试 |
| 低 | block-body、state-sync、history-sync | 可分片、可断点续传 |

TCP 可以通过多连接或内部队列区分优先级。QUIC 可以通过多 stream 区分优先级，避免大块同步数据阻塞共识消息。

## 10. 节点模型建议

节点模型已经保留以下信息：

- 节点 ID。
- 多协议地址列表。
- 节点状态。
- 节点角色。
- 节点能力位。
- 协议版本。
- 软件版本。
- 最新 slot。
- 区块高度。
- 最新区块哈希。
- 是否验证节点。
- 质押权重。
- 首次发现时间。
- 最近活跃时间。
- 最近连接时间。
- 最近断开时间。
- 最近错误和失败次数。
- 收发字节计数。
- 最近 RTT。

DHT、连接池、黑名单和监控都应该读取 `PeerSnapshot`，不要直接暴露内部节点表。

## 11. DHT 集成建议

DHT 不应该负责传输细节，只负责发现和保存节点地址。

建议 DHT 中保存：

- `peer_id`
- 地址列表
- 节点公钥
- 最近成功连接时间
- 最近失败时间
- 失败次数
- 节点能力位
- 节点签名过的地址记录

Host 从 DHT 读取 peer 后，只关心地址中的协议字段，再选择对应 Transport 拨号。

## 12. 安全策略建议

后续需要补齐以下策略：

- 入站连接数限制。
- 单节点连接数限制。
- 单 IP 连接数限制。
- 消息大小限制。
- 消息速率限制。
- 握手超时限制。
- 重放 nonce 缓存。
- 节点黑名单。
- 失败拨号退避。
- 异常消息计分。
- 区块、交易和 QC 的签名校验。

当前 `message.go` 已经限制单条消息大小，但还没有做速率限制、握手认证和业务签名校验。

## 13. 观测指标建议

后续建议输出以下指标：

- 当前连接数。
- TCP 连接数。
- QUIC 连接数。
- 拨号成功次数。
- 拨号失败次数。
- 每类消息发送数量。
- 每类消息接收数量。
- 消息发送延迟。
- 消息读取延迟。
- 入站拒绝次数。
- 节点降级次数。
- DHT 查询延迟。

这些指标应该进入 `monitor` 模块，P2P 模块只负责暴露必要事件或统计值。

## 14. 推荐演进顺序

建议按以下顺序推进：

1. 保持当前 TCP 通路稳定，先服务交易广播和区块同步。
2. 增加连接握手，绑定 `peer_id`、链 ID 和创世块哈希。
3. 接入真实 QUIC，实现 session 和 stream。
4. 增加消息优先级队列，优先保障 HotStuff 投票和 QC。
5. 接入 Kademlia DHT，保存和发现多协议地址。
6. 增加入站限流、拨号退避和节点计分。
7. 将指标接入 `monitor`。
8. 增加跨节点集成测试和弱网测试。
