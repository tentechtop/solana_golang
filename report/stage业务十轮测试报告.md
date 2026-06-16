# Stage 业务十轮测试报告

## 结论

本次不再围绕测试脚本优化，而是针对链业务主线做 10 轮验证：创世账户、真实账户交易、验证者注册、质押生效、撤回和再次质押、奖励一致性、惩罚、finalized 重组保护、RPC 外部入口、Turbine/快速通道、BLS 聚合签名、透明/隐私业务。

本机业务门禁结论：通过。可以进入受控 stage 环境继续做 5 机/30 节点长稳验证。

注意：这不是生产准入结论。stage 环境仍必须补真实多节点长稳、故障注入和 Linux/macOS race。

## 本次新增业务测试

- `consensus/stage_business_test.go`
  - 验证者注册后下个 epoch 生效并参与权重计算。
  - 撤回质押、提现、再次质押完整生命周期。
  - 奖励根被篡改时验证器拒绝 proposal。
  - 重复 reward QC 被拒绝。
  - 漏投后 jail，当前惩罚 epoch 有效质押为 0。
- `blockchain/stage_business_test.go`
  - finalized 高度以下禁止深度重组，失败后链头不变。
- `cmd/posnode/stage_business_test.go`
  - RPC `registerValidator` / `stake` / `unstake` 能构造真实签名交易并进入 mempool。
  - 低于最低质押金额的注册请求被拒绝，mempool 不被污染。

## 第1轮：创世账户与账本持久化

命令：

```powershell
go test .\blockchain -run "TestBuildGenesisStateCreatesTreasuryAndValidators|TestLedgerCommitPersistsAndReloads" -count 1
```

结果：

```text
ok  	solana_golang/blockchain	0.959s
```

结论：创世 treasury、初始 validator、账本提交和重启恢复通过。

## 第2轮：PoS 主业务闭环

命令：

```powershell
go test .\consensus -run "TestPoSRealAccountStakeVoteAndBlockFlow" -count 1
```

结果：

```text
ok  	solana_golang/consensus	0.876s
```

结论：真实账户注册、出块、验证、QC、转账、双 proposal evidence 主流程通过。

## 第3轮：注册验证者后下个 epoch 生效

命令：

```powershell
go test .\consensus -run "TestStageBusinessValidatorRegisterActivatesAndWeightsNextEpoch" -count 1
```

结果：

```text
ok  	solana_golang/consensus	0.569s
```

结论：节点不能凭空成为验证者，必须通过注册和质押交易；当前 epoch 权重为 0，下个 epoch 才进入 validator set。

## 第4轮：撤回质押、提现、再次质押

命令：

```powershell
go test .\consensus -run "TestStageBusinessUnstakeWithdrawAndRestakeLifecycle" -count 1
```

结果：

```text
ok  	solana_golang/consensus	0.855s
```

发现与修正：

- 初始断言把提现金额按完整 unstake 金额计算。
- 实际业务正确行为是：提现交易仍要扣交易费。
- 测试已改为校验 `提现到账 = 解锁金额 - 交易费`。

结论：撤回、解锁、提现、再次质押、下个 epoch 恢复权重通过，且余额审计包含手续费。

## 第5轮：奖励根防篡改

命令：

```powershell
go test .\consensus -run "TestStageBusinessProposalRewardTamperingIsRejected" -count 1
```

结果：

```text
ok  	solana_golang/consensus	0.855s
```

结论：leader 不能私自删改奖励列表；验证器会复算 reward root 并拒绝篡改 proposal。

## 第6轮：重复 Reward QC 拒绝

命令：

```powershell
go test .\consensus -run "TestStageBusinessDuplicateRewardQCIsRejected" -count 1
```

结果：

```text
ok  	solana_golang/consensus	1.070s
```

结论：同一 slot 的 reward QC 重复提交会被拒绝，避免重复记 vote credits。

## 第7轮：漏投惩罚与 jail

命令：

```powershell
go test .\consensus -run "TestStageBusinessMissedVotesJailAndExcludeValidator|TestApplyBlockRewardsJailsAndSlashesMissedVotes" -count 1
```

结果：

```text
ok  	solana_golang/consensus	0.619s
```

结论：长期漏投会触发 slash/jail；被 jail 的 validator 在惩罚 epoch 有效质押为 0。

## 第8轮：finalized 以下禁止重组

命令：

```powershell
go test .\blockchain -run "TestStageBusinessFinalizedBlocksRejectDeepReorg|TestLedgerReorganizeToBetterFork" -count 1
```

结果：

```text
ok  	solana_golang/blockchain	0.935s
```

发现与修正：

- 初始测试从 genesis 分叉，因 genesis proposal 未作为普通 block 保存，先触发 `block not found`。
- 已改为从持久化的高度 1 分叉，并让主链 finalized 到高度 2。

结论：普通更优分叉可重组；触碰 finalized 以下共同祖先的深度重组会被拒绝，链头保持不变。

## 第9轮：RPC 外部业务入口

命令：

```powershell
go test .\cmd\posnode -run "TestStageBusinessRPCValidatorStakeAndUnstakeEntrypoints|TestStageBusinessRPCRejectsBelowMinimumStake" -count 1
```

结果：

```text
ok  	solana_golang/cmd/posnode	0.967s
```

结论：

- `registerValidator`、`stake`、`unstake` 能构造真实签名交易并进入 mempool。
- 低于最低质押金额的注册请求会失败，mempool 不被污染。

## 第10轮：网络路由、BLS、透明/隐私业务

命令：

```powershell
go test .\cmd\posnode -run "TestConsensusStatusExposesLayerWeightAndStakeBuckets|TestGetConsensusStatusRPC|TestStatusSnapshotExposesTurbineAndFastPath|TestRouteProposalUsesTurbineChildren" -count 1
go test .\consensus -run "TestBLS|TestVoteCollectorFormsBLS|TestVerifyBLSAggregateWithStake" -count 1
go test .\runtime -run "TestPrivacy|Test.*Transparent|Test.*Audit" -count 1
go test .\programs\stake -run "TestEffectiveStake|TestMatureStake|TestSlash|Test.*Stake" -count 1
```

结果：

```text
ok  	solana_golang/cmd/posnode	0.920s
ok  	solana_golang/consensus	0.467s
ok  	solana_golang/runtime	0.596s
ok  	solana_golang/programs/stake	0.367s
```

结论：

- 共识状态 RPC 能暴露层级、权重、生效质押、待生效质押。
- Turbine 分层传播和交易快速通道测试通过。
- BLS 聚合签名、bitmap、按 stake 防伪造验证通过。
- 透明/隐私业务与审计相关测试通过。

## 最终本机门禁

命令：

```powershell
go test .\...
go vet .\...
go build .\cmd\posnode
```

结果：

```text
ok  	solana_golang	0.473s
ok  	solana_golang/blockchain	1.026s
ok  	solana_golang/cmd/posnode	1.009s
ok  	solana_golang/consensus	0.606s
ok  	solana_golang/runtime	(cached)
```

`go vet .\...` 通过。

`go build .\cmd\posnode` 通过。

构建产物 `posnode.exe` 已清理。

## Stage 准入判断

可以进入受控 stage 环境，前提：

- 使用独立 stage 数据目录，不复用本机开发数据。
- 开启 JSON 日志，保留所有节点日志。
- 用真实 5 机/30 节点配置启动。
- 使用 RPC 入口完成 treasury 转账、注册验证者、质押、撤回、再次质押。
- 用一致性巡检检查 slot、height、leader、state_root、block_hash、qc_hash、validator 权重、Turbine 路由和快速通道。

仍需在 stage 内完成：

- Linux/macOS 执行 `go test -race ./...`。
- 30 分钟、2 小时、24 小时长稳。
- 节点重启、断网、延迟、乱序、leader 离线、缺块同步、fork/reorg 故障注入。
- 多节点真实日志分析后再决定是否进入生产候选。
