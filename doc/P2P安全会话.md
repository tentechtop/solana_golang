# P2P 安全会话设计

版本：v1

## 目标

P2P 传输层只负责连通，安全会话层负责节点认证、密钥协商、消息加密和未来 0RTT 恢复。TCP 和 QUIC 都必须走同一套安全会话，避免两套传输出现安全语义差异。

## 连接流程

1. TCP 或 QUIC 建立原始连接。
2. 主动方发送 `ProtocolSecureSessionV1` 请求。
3. 被动方校验节点签名、网络 ID、协议版本范围和时间窗口。
4. 被动方返回 `ProtocolSecureSessionV1` 响应。
5. 双方使用 X25519 共享密钥和握手 transcript 派生方向密钥。
6. Host 将安全连接写入连接池，并保存恢复票据。
7. 心跳、交易、区块和共识消息都通过 `SecureConnection` 自动加密 payload。

## 握手字段

| 字段 | 作用 |
| --- | --- |
| `peer_id` | 节点 ID，必须等于身份公钥的 base58。 |
| `identity_public_key` | 节点长期 Ed25519 公钥。 |
| `ephemeral_public_key` | 单次连接 X25519 临时公钥。 |
| `nonce` | 防止 transcript 重复。 |
| `network_id` | 网络身份，不一致直接断开。 |
| `software_version` | 软件版本，签名保护并记录，不默认作为硬拒绝条件。 |
| `min_protocol_version` | 本节点支持的最低 P2P 消息版本。 |
| `max_protocol_version` | 本节点支持的最高 P2P 消息版本。 |
| `resumption_ticket_id` | 未来 0RTT 恢复票据标识。 |
| `signature` | 节点私钥对握手内容签名。 |

## 拒绝条件

1. peer id 与公钥不匹配。
2. Ed25519 签名验证失败。
3. 网络 ID 不一致。
4. 协议版本范围没有交集。
5. 握手时间超出允许窗口。
6. 临时公钥、nonce、密文或会话 ID 长度非法。
7. 加密消息序号不是严格递增。

## 加密边界

安全连接只加密 `Message.Payload`，外层 `Message` 的 ID、类型、路由和请求关系作为 AEAD associated data 参与认证。这样中转节点可以按外层路由转发，但不能读取或篡改业务 payload。

## 0RTT 边界

当前只生成和保存恢复票据，不开放任意 0RTT 业务消息。后续允许 0RTT 时只能发送幂等消息，例如 ping、节点状态查询、只读能力查询。交易、区块、投票和会改变状态的消息必须等待握手确认后发送，因为 0RTT 天然存在重放风险。

## 开发要求

1. 新业务协议不得绕过 `SecureConnection` 直接读写原始 TCP/QUIC 连接。
2. 恢复票据可以持久化，当前方向密钥不得长期明文落库。
3. 日志可以记录 peer id、network id、software version 和 protocol version，不能记录私钥、共享密钥、方向密钥和恢复 secret。
4. 修改握手字段必须同步更新签名 transcript、Borsh 编解码和单元测试。
