# Prod 准入报告

测试日期：2026-06-16  
测试环境：Windows PowerShell，本地 Go 测试环境  
原始日志目录：`F:\workSpace2029\solana_golang\report\prod准入长稳输出`

## 准入结论

本次业务长稳测试未发现失败：已完成 17 轮完整 5 分钟业务测试，第 18 轮执行到第 13 个循环时按用户要求提前结束。累计记录 `exit_code=0` 的 Go 测试步骤 1468 次，失败次数 0，runner stderr 为 0 字节。提前结束后补跑短门禁，`go test .\... -count 1`、`go vet .\...`、`go build .\cmd\posnode` 全部通过。

准入判断：

| 级别 | 结论 | 原因 |
| --- | --- | --- |
| Stage | 通过 | 本地业务、P2P、Turbine、重组、质押投票奖励、隐私审计多轮未失败 |
| Prod 最终放行 | 暂缓 | 原计划 30 轮业务测试 + 20 分钟稳定性测试未完整跑完，且缺真实多机长稳和 Linux/macOS race |

## 执行边界

原计划：

- 30 轮业务测试，每轮 5 分钟。
- 最后一轮 20 分钟稳定性测试。

实际执行：

- 开始时间：2026-06-16T12:02:10+08:00。
- 停止时间：2026-06-16T13:33:51+08:00。
- 实际运行约 91 分钟。
- 完整业务轮次：17 轮。
- 部分业务轮次：第 18 轮，已完成 63 个成功步骤，停止时正在启动 P2P 网络路由测试。
- 20 分钟稳定性测试：未执行，按用户中断要求结束。

## 业务覆盖

每轮业务矩阵覆盖以下 Go 测试：

| 模块 | 覆盖内容 |
| --- | --- |
| `consensus` | PoS 真实账户、注册验证者、质押生效、投票、出块、奖励、惩罚、BLS QC、leader 离线 skip QC |
| `cmd/posnode` | RPC 交易提交、注册验证者、stake、unstake、mempool、QC 校验、共识状态、Turbine、交易快速通道、缺块/orphan |
| `blockchain` | 创世账户、账本持久化、fork/reorg、finalized 保护、重启后 reorg head 恢复 |
| `p2p` | 断连、延迟、乱序、心跳超时、请求重试、拨号退避、限流、防重放、队列优先级 |
| `programs/privacy` / `zk` / `runtime` | 透明到透明、透明到隐私、隐私到隐私、隐私到透明、审计和证明验证 |

## 轮次结果

| 轮次 | 状态 | 成功步骤 | 失败步骤 |
| --- | --- | ---: | ---: |
| 1 | 完整完成 | 90 | 0 |
| 2 | 完整完成 | 95 | 0 |
| 3 | 完整完成 | 95 | 0 |
| 4 | 完整完成 | 95 | 0 |
| 5 | 完整完成 | 95 | 0 |
| 6 | 完整完成 | 95 | 0 |
| 7 | 完整完成 | 95 | 0 |
| 8 | 完整完成 | 95 | 0 |
| 9 | 完整完成 | 90 | 0 |
| 10 | 完整完成 | 70 | 0 |
| 11 | 完整完成 | 70 | 0 |
| 12 | 完整完成 | 70 | 0 |
| 13 | 完整完成 | 70 | 0 |
| 14 | 完整完成 | 70 | 0 |
| 15 | 完整完成 | 70 | 0 |
| 16 | 完整完成 | 70 | 0 |
| 17 | 完整完成 | 70 | 0 |
| 18 | 提前结束，部分完成 | 63 | 0 |

合计：

- 成功步骤：1468。
- 失败步骤：0。
- runner stderr：0 字节。
- 构建产物已清理。

## 停止点说明

停止前最后完整成功步骤：

```text
go test .\blockchain -run TestBuildGenesisStateCreatesTreasuryAndValidators|TestLedgerCommitPersistsAndReloads|TestLedgerReorganizeToBetterFork|TestStageBusinessFinalizedBlocksRejectDeepReorg|TestFaultInjectionReorgPersistsAfterNodeRestart -count 1
ok solana_golang/blockchain
```

停止时正在启动：

```text
go test .\p2p -run TestFaultInjection|TestHostHeartbeatClosesExpiredConnection|TestHostRequestRetries|TestHostDialPeerBacksOff|TestHostRateLimitsInboundMessages|TestPeerProtection|TestQueuedConnection|TestConnectionMessageSequencer -count 1
```

该步骤没有失败记录，是人工提前结束导致没有完整 exit code。

## 结束后门禁

人工停止长跑后补跑：

| 命令 | 结果 |
| --- | --- |
| `go test .\... -count 1` | 通过 |
| `go vet .\...` | 通过 |
| `go build .\cmd\posnode` | 通过 |

## 测试驱动优化

本轮长跑没有暴露新的业务失败，因此没有进行业务代码修复。新增了可复用的长稳调度脚本：

- `tools/run_prod_business_soak.ps1`

脚本只负责调度真实 Go 测试、记录每轮日志和状态，不替代业务验证。

## 剩余风险

以下事项仍是生产放行前硬门槛：

1. 补完原计划：30 轮完整业务测试 + 20 分钟全仓稳定性测试。
2. 在 Linux 或 macOS 跑 `go test -race ./...`。Windows 本机 ThreadSanitizer 此前无法作为有效 race 门禁。
3. 使用真实 6 台机器、每台 5 个节点做 30 节点长稳。
4. 真实网络故障注入：断网、延迟、乱序、重启、leader 下线、缺块同步、fork/reorg。
5. 长稳期间每 10 秒比对所有节点 `slot`、`height`、`leader`、`state_root`、`block_hash`、`qc_hash`、`finalized_height`。
6. 接入实际日志采集、metrics、告警和仪表盘后再做一次准入。

## CTO 审查意见

当前本地业务链路质量已经具备进入 stage 的条件：多轮业务测试没有发现一致性、交易、质押、投票、奖励、隐私审计、Turbine 或 P2P 故障回归。

但这不是最终生产放行。原因很明确：本次按要求提前结束，没有完成原计划 30 轮和 20 分钟稳定性测试，也没有真实多机和 race 结果。下一步应进入 stage 环境做真实多节点长稳；stage 通过后再出正式生产准入结论。
