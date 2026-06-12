# Solana Golang

<p align="center">
  <strong>🚀 基于 Go 语言自研的高性能区块链基础设施</strong>
  <br>
  <sub>Production-Ready · 从零构建 · 金融级安全</sub>
</p>

<p align="center">
  <a href="#-项目简介">简介</a> ·
  <a href="#-项目定位">定位</a> ·
  <a href="#-架构设计">架构</a> ·
  <a href="#-快速开始">快速开始</a> ·
  <a href="#-核心特性">特性</a> ·
  <a href="#-项目结构">结构</a> ·
  <a href="#-路线图">路线图</a> ·
  <a href="#-许可证">许可证</a>
</p>

---

## 📖 项目简介

**Solana Golang** 是一个从零开始、完全用 Go 语言实现的自研公链，目标是成为一条**真正可承载金融级业务的生产级链**。项目深受 Solana 架构思想的启发——包括 **Borsh 序列化**、**账户模型**、**交易结构**和 **Slot 时钟**——但在工程实现上做出了独立的架构决策，旨在构建一套**安全、高性能、可审计、可长期演进**的区块链基础设施。

> ⚠️ 本项目是 Solana 的 **Go 语言独立实现**，并非 Solana 官方项目。Solana 官方实现使用 Rust 语言，位于 [solana-labs/solana](https://github.com/solana-labs/solana)。

## 🎯 项目定位

| 维度 | 目标 |
|------|------|
| **可靠性** | 每个模块均经过严格的单元测试、边界测试、竞态测试与模糊测试，无例外 |
| **安全性** | 多层安全防护——加密会话、Schema 校验、签名验证、原子写入 |
| **性能** | 高吞吐 P2P 网络 + 高性能 LSM 存储引擎，面向大规模交易场景 |
| **可维护性** | 清晰的模块边界、结构化日志、内置指标、渐进式路线图 |
| **去中心化** | 自研 P2P 网络，不依赖任何中心化组件或第三方框架

### 设计哲学

- **生产即目标** —— 以金融级业务为最终交付标准，每个阶段都有明确的完成定义和质量门槛
- **测试驱动品质** —— 每个模块均覆盖单元测试、边界测试、竞态检测（`-race`）和模糊测试（fuzz），测试与功能代码同步交付
- **无外部框架依赖** —— 不使用 libp2p、不使用 protobuf，P2P 网络层完全自研，对每一层协议具备完全控制力
- **安全至上** —— 内置 Schema Registry，所有 raw bytes 强制携带类型/版本/编码/哈希元信息；签名必须包含 domain_separator + message_type + version；私钥绝不写入日志、RPC 或 P2P 消息
- **严格的序列化规范** —— Borsh 用于所有二进制协议，JSON 仅用于外部 RPC 接口，内外编码严格分离
- **工程可观测性** —— 结构化日志、内置指标、连接保护、速率限制，生产环境全链路可追踪
- **渐进式演进** —— 六阶段路线图，从协议基线到 ZK 零知识证明，每一步都有明确的交付物和质量标准

---

## 🏗️ 架构设计

```
┌──────────────────────────────────────────────────────────────┐
│                         RPC Layer                            │
│              JSON-RPC 2.0 · HTTP · 批量请求                   │
├──────────────────────────────────────────────────────────────┤
│                      Consensus Layer                         │
│         SlotClock · Vote · QuorumCertificate · QC            │
├──────────────────────────────────────────────────────────────┤
│                        P2P Network                           │
│   TCP/QUIC Transport · KAD DHT · Secure Session · 26 协议    │
├──────────────────────────────────────────────────────────────┤
│                      Business Models                         │
│     Account · Transaction · Block · Instruction · Merkle     │
├──────────────────────────────────────────────────────────────┤
│                       Storage Layer                          │
│    Pebble/LevelDB · Migration · Cache · Snapshot · WAL       │
├──────────────────────────────────────────────────────────────┤
│                    Serialization Layer                       │
│        Borsh Codec · Schema Registry · Envelope              │
└──────────────────────────────────────────────────────────────┘
```

### 模块依赖方向

```
cmd (入口装配)
 ├── config     ← YAML 配置加载与校验
 ├── database   ← Pebble/LevelDB 存储引擎
 ├── schema     ← Schema 注册中心
 ├── p2p        ← 自研 P2P 网络 (TCP + QUIC)
 ├── consensus  ← Slot 时钟 · 投票 · QC
 ├── rpc        ← JSON-RPC HTTP 服务
 ├── structure  ← 业务事实模型
 ├── codec      ← Borsh 编解码
 └── utils      ← 通用工具 (密码学·编码·钱包)
```

---

## ⚡ 快速开始

### 环境要求

- **Go** >= 1.24.6
- 支持的操作系统：Linux / macOS / Windows

### 构建

```bash
# 克隆仓库
git clone https://github.com/your-org/solana_golang.git
cd solana_golang

# 构建二进制
go build ./cmd

# 运行测试
go test ./...

# 竞态检测
go test -race ./...
```

### 启动节点

```bash
# 使用默认配置启动
go run ./cmd

# 指定配置文件
go run ./cmd -config config/local/config.yaml

# 通过环境变量指定配置
APP_CONFIG=config/demo/config.yaml go run ./cmd
```

### 默认配置概览

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| RPC 地址 | `:8899` | JSON-RPC HTTP 监听地址 |
| P2P 协议 | `quic` | 优先使用 QUIC，也支持 TCP |
| P2P 监听 | `0.0.0.0:5002` | P2P 网络监听地址 |
| 数据库引擎 | `pebble` | 高性能 LSM-based 存储 |
| 数据库路径 | `./data/p2p-test/local` | 数据持久化目录 |
| 最大 Peer 数 | `64` | P2P 连接上限 |
| 日志格式 | `json` | 结构化 JSON 日志 |

详细配置请参考 `config/` 目录下的多环境配置文件。

---

## 🔥 核心特性

### 🔗 自研 P2P 网络层

完全从零构建的去中心化对等网络，不依赖任何第三方 P2P 框架：

- **双传输协议**：TCP 与 QUIC 并行支持，运行时可切换
- **P2P Frame 协议**：Magic Number (`0x53475032`) + 版本 + 类型 + 长度 + SHA256 校验和
- **26 种内置协议**：控制面（Ping/Pong、握手、身份识别、节点发现）· 数据面（区块、交易）· 共识面（HotStuff 投票）
- **KAD DHT 路由**：256 Bucket Kademlia 分布式哈希表，高效节点发现
- **安全会话**：Ed25519 节点认证 + AES-256-GCM 加密通信
- **连接保护**：入站限制、IP 连接数限制、消息速率限制、Peer 评分
- **心跳探活**：周期性 Ping/Pong，自动剔除失联节点

### ⛓️ 共识层

基于 Slot 时钟的 HotStuff 风格共识：

- **SlotClock**：本地单调时钟 + 固定 Slot 时间（400ms/Slot）
- **投票机制**：Confirm Vote（含区块哈希）+ Skip Vote（空区块）
- **Quorum Certificate**：2/3 质押阈值 + 投票聚合器
- **冲突检测**：自动拒绝重复/冲突投票

### 💾 高性能存储

以 CockroachDB Pebble 为主引擎的企业级存储方案：

- **双引擎支持**：Pebble（高性能 LSM）+ LevelDB（兼容备选）
- **16 张核心表**：Account、Block、Transaction、UTXO、Peer 等
- **读事务快照**：MVCC 风格的一致读视图
- **原子批量写**：DataTransaction 保证多表更新的原子性
- **前缀/范围查询**：支持正序和逆序遍历
- **表级缓存**：TTL + 容量限制
- **Migration 框架**：版本化的数据库 Schema 升级

### 📦 Borsh 序列化与 Schema Registry

严格的序列化规范和类型安全：

- **Borsh Codec**：高效二进制编解码，支持零拷贝读取器
- **Schema Registry**：所有 raw bytes 强制携带类型/版本/编码/SchemaID/负载哈希
- **Envelope**：Magic Number (`0x53475352`) + 完整元信息
- **Canonical Hash**：签名哈希 = SHA256(domain_separator ‖ message_type ‖ version ‖ canonical_payload)
- **内外编码分离**：P2P 用 Borsh，外部 RPC 用 JSON DTO

### 🔐 密码学工具集

完整的 Solana 兼容密码学实现：

- **Ed25519** 签名/验签
- **X25519** 密钥交换
- **AES-256-GCM** 对称加密
- **HKDF** 密钥派生
- **BIP-39** 助记词生成
- **SLIP-0010** HD 钱包
- **Solana 派生路径** `m/44'/501'/...'`
- **PDA** (Program Derived Address)
- **Base58 / Base64 / Hex** 编码

---

## 📂 项目结构

```
solana_golang/
├── cmd/                        # 入口程序
│   ├── main.go                 #   主节点启动入口
│   ├── node_identity.go        #   节点身份管理 (Ed25519 密钥)
│   └── p2pstress/              #   P2P 压力测试工具
│
├── config/                     # 配置管理
│   ├── config.go               #   配置结构定义与校验
│   ├── loader.go               #   YAML 配置加载器
│   ├── local/config.yaml       #   本地开发环境配置
│   ├── demo/config.yaml        #   演示环境配置
│   ├── stage/config.yaml       #   预发布环境配置
│   └── prod/config.yaml        #   生产环境配置
│
├── p2p/                        # 自研 P2P 网络层 (61 文件)
│   ├── host.go                 #   P2P Host 主控
│   ├── message.go              #   P2P 消息帧 + Borsh 编解码
│   ├── protocol.go             #   协议定义 (26 种协议)
│   ├── protocol_registry.go    #   协议注册与分发
│   ├── tcp_transport.go        #   TCP 传输层
│   ├── quic_transport.go       #   QUIC 传输层
│   ├── secure_session.go       #   安全会话 (Ed25519 + AES-GCM)
│   ├── kad_routing_table.go    #   KAD 路由表 (256 Bucket)
│   ├── kad_protocol.go         #   KAD 协议处理
│   ├── peer_store.go           #   Peer 信息管理
│   ├── bootstrap.go            #   引导节点连接
│   ├── host_connections.go     #   连接池管理
│   ├── host_heartbeat.go       #   心跳探活
│   ├── peer_protection.go      #   Peer 保护 (限流·评分)
│   ├── metrics.go              #   P2P 指标
│   └── peerstore/              #   Peer 持久化存储
│
├── consensus/                  # 共识层
│   ├── slot.go                 #   SlotClock · Vote · QC
│   ├── errors.go               #   共识错误定义
│   └── doc.md                  #   共识设计文档
│
├── database/                   # 存储层
│   ├── interface.go            #   Database 接口定义 (16 表)
│   ├── service.go              #   完整数据库实现 (CRUD·事务·分页)
│   ├── factory.go              #   存储引擎工厂 (Pebble/LevelDB)
│   ├── migration.go            #   Schema Migration 框架
│   ├── key_codec.go            #   表前缀编码
│   ├── cache.go                #   表级缓存
│   ├── pebble/engine.go        #   Pebble 引擎适配
│   └── leveldb/engine.go       #   LevelDB 引擎适配
│
├── structure/                  # 业务事实模型 (27 文件)
│   ├── account.go              #   账户模型
│   ├── transaction.go          #   交易结构
│   ├── block.go                #   区块结构 + Merkle 树
│   ├── instruction.go          #   指令模型
│   ├── keypair.go              #   密钥对
│   ├── simulator.go            #   交易模拟执行器
│   └── sysvar.go               #   系统变量
│
├── rpc/                        # JSON-RPC 服务
│   ├── server.go               #   HTTP JSON-RPC 2.0 服务
│   ├── handler.go              #   内置 RPC 处理器
│   └── types.go                #   RPC 数据类型
│
├── codec/borsh/                # Borsh 编解码
│   ├── writer.go               #   小端写入器
│   └── reader.go               #   小端读取器 + 零拷贝
│
├── schema/                     # Schema Registry
│   ├── registry.go             #   Schema 注册与查找
│   └── envelope.go             #   Envelope (元信息 + payload)
│
├── utils/                      # 通用工具包 (23 文件)
│   ├── crypto/                 #   密码学 (Ed25519·AES·HKDF)
│   ├── encoding/               #   编码 (Base58·Hex·Base64)
│   ├── wallet/                 #   钱包 (BIP-39·SLIP-0010·PDA)
│   └── p2p/                    #   P2P 工具 (MultiAddress)
│
├── doc/                        # 设计文档
├── go.mod                      # Go 模块定义
└── LICENSE                     # Apache License 2.0
```

---

## 🗺️ 路线图

### ✅ 阶段一：协议与存储基线
- [x] Borsh 序列化规范
- [x] Schema Registry
- [x] P2P Frame + Message 协议
- [x] 节点身份管理 (Ed25519)
- [x] 数据库 Migration 框架
- [x] 区块结构与 Merkle 树
- [x] 交易模拟执行器

### 🔄 阶段二：共识最小闭环
- [ ] Validator Set 管理
- [ ] Leader Schedule 调度
- [ ] Proposal 结构
- [ ] 签名 Vote + QC 生成
- [ ] P2P 共识消息处理器

### 📋 阶段三：链状态闭环
- [ ] 交易池 (Mempool)
- [ ] 账户模型完整实现
- [ ] 区块执行引擎
- [ ] State Root 计算
- [ ] Block Commit 流程

### 📋 阶段四：网络同步
- [ ] Peer Discovery 增强
- [ ] Block Headers 同步
- [ ] Block Body 同步
- [ ] Checkpoint Sync

### 📋 阶段五：生产强化
- [ ] 签名聚合 (BLS)
- [ ] 惩罚证据 (Slashing)
- [ ] 防 Sybil 攻击
- [ ] 全局限流
- [ ] Prometheus Metrics
- [ ] 压力测试与性能调优

### 📋 阶段六：ZK 零知识证明

将"高速透明链"升级为"高速+隐私+可扩容"的全能链，以 L1 原语形式原生集成 ZK 能力，而非外挂 L2。

| 方向 | 目标 |
|------|------|
| **隐私交易** | 机密转账与余额隐藏，ZK + ElGamal 加密，Token 原生支持 |
| **ZK Compression** | 链下压缩状态，链上验证证明，存储成本降低 99%，租金降低 1000 倍 |
| **抗 MEV** | 订单加密，内存池不可见，从源头消除抢跑与三明治攻击 |
| **可验证链下计算** | ZK VM 链下执行，链上验证 proof，突破单链算力上限 |
| **zkKYC 与合规** | 链下完成身份认证，链上用 ZK 证明合规性，不泄露身份数据 |
| **轻客户端** | 区块状态证明，无需全量同步即可验证链上数据 |

---

## 🛡️ 安全规范

本项目遵循严格的安全编码规范：

| 禁止事项 | 说明 |
|----------|------|
| ❌ 引入 protobuf | 全项目统一使用 Borsh |
| ❌ 使用 libp2p | P2P 网络层完全自研 |
| ❌ JSON 做 P2P/链上编码 | JSON 仅限外部 RPC |
| ❌ 信任网络消息中的 stake | 必须本地验证 |
| ❌ 无签名接受共识消息 | 所有共识消息强制验签 |
| ❌ 绕过 Schema Registry | raw bytes 必须注册 |
| ❌ 多表非原子更新 | 必须使用 DataTransaction |
| ❌ 私钥写入日志/RPC | 密钥绝不出现在日志中 |

---

## 🧪 测试

本项目坚持**测试与功能代码同步交付**的原则，每个核心模块都经过多层测试验证：

| 测试类型 | 说明 |
|----------|------|
| **单元测试** | 覆盖所有公开接口、核心逻辑和边界条件 |
| **集成测试** | P2P 握手、数据库事务、共识投票等跨模块场景 |
| **竞态检测** | 所有并发模块必须通过 `go test -race`，无例外 |
| **模糊测试 (Fuzz)** | 对序列化、消息解析等输入敏感模块进行随机输入测试 |
| **边界测试** | 空值、零值、超大值、截断数据等极端输入 |
| **压力测试** | P2P 连接压测工具 `p2pstress`，验证高负载下的稳定性 |

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./p2p/...
go test ./database/...

# 详细输出
go test -v ./...

# 竞态检测
go test -race ./...

# 测试覆盖率
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 模糊测试
go test -fuzz=FuzzDecode -fuzztime=30s ./codec/...
go test -fuzz=FuzzMessage -fuzztime=30s ./p2p/...
```

---

## 📄 许可证

本项目基于 [Apache License 2.0](LICENSE) 开源。

```
Copyright 2024 Solana Golang Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
```

---

## 🙏 致谢

- [Solana](https://solana.com/) —— 灵感来源与架构参考
- [CockroachDB Pebble](https://github.com/cockroachdb/pebble) —— 高性能存储引擎
- [quic-go](https://github.com/quic-go/quic-go) —— QUIC 传输协议实现
- [Borsh](https://borsh.io/) —— 高效二进制序列化规范

---

<p align="center">
  <sub>Built with Go · Solana-inspired · Production-Ready · Financial-Grade</sub>
</p>
