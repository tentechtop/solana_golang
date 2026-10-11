# RWA v1：合规股权份额与现实资产 NFT

RWA v1 使用独立、不可升级的 OVM 模板。它不复用可自由转账的普通 Token/NFT：持有人准入、转让限制、投票快照锁和分红账务由链上状态机强制执行。

当前模板 SHA-256：`e220ae6e9dc28d67f87024377689257c507f0034ea2b0aa4be0413cbebcb304b`。

## 资产类型

- `share`：同质化整数份额，`decimals=0`；每一份在提案快照中对应一票，并按份额参与原生币分红。
- `nft`：唯一现实资产凭证，`decimals=0`、`max_supply=1`；适合房产、设备或单笔债权的唯一链上映射。v1 NFT 不参与份额治理和分红。

两类资产均绑定一个不可变的法律材料 SHA-256。链上只保存哈希，不保存身份证件、合同正文或其他个人信息。模板不会验证现实资产是否存在、权属是否真实，也不会使代币自动取得某一司法辖区的股权法律效力。

## 强制规则

1. 发行方先为地址创建合规持仓账户并登记身份材料承诺；只有 `approved=true` 且未冻结的账户可以接收发行和普通转让。
2. 股权和 NFT 均不得超过 `max_supply`；NFT 只能发行和转移一个单位。
3. 发行、普通转让、销毁和强制过户在全局暂停时被拒绝。
4. 冻结或撤销准入的持有人不能普通转出；发行方可依据链上交易携带的法律依据哈希执行强制过户，但接收方仍须合规。
5. 同一时刻只允许一个投票或分红快照。快照期间禁止发行、转让、销毁、强制过户和修改投资者合规状态，从而保证持仓即快照权重。
6. 提案到期后任何签名者都可以完成计票并解除锁定。通过条件为参与权重达到向上取整的法定比例，且赞成权重大于反对权重。
7. 分红资金在创建分红时从发行方系统账户进入合约拥有的分红账户；每个地址只能领取一次，金额为 `floor(total * snapshot_holding / snapshot_supply)`。到期后任何签名者可以关闭分红，舍入余数和无人领取部分退回当前发行权限地址。
8. 发行权限交接使用“当前权限提议、接任地址签名接受”的两阶段流程。

## 状态账户

| kind | 状态 | 主要字段 |
| --- | --- | --- |
| 48 | mint | 类型、发行方、供应量、上限、材料哈希、暂停、活动提案/分红 |
| 49 | holding | mint、持有人、整数余额、准入、冻结、身份材料承诺 |
| 50 | proposal | 快照供应量、截止 slot、法定比例、赞成/反对/弃权、结果 |
| 51 | vote | 提案、投票人、快照权重、选项 |
| 52 | dividend | 快照供应量、资金总额、已领取额、截止 slot、材料哈希 |
| 53 | dividend claim | 分红、领取人、领取额 |

所有账户地址按 `ordexus.rwa.*.v1` 域确定性派生。派生出的私钥只用于系统程序初次分配空账户；合约所有权、规范地址绑定和真实钱包签名共同保护状态修改。

## 指令 ABI

指令继续使用统一资产 envelope：`u32 type | u64 amount | u8 decimals | string name | string symbol | string URI`。下表账户均按顺序传入，首账户是交易签名者。

| 编号 | 操作 | 账户 | envelope 用法 |
| --- | --- | --- | --- |
| 300 | 初始化 | 发行方、mint | amount=上限，name/symbol，URI=`share:<hash>` 或 `nft:<hash>` |
| 301 | 设置投资者 | 发行方、mint、持有人、holding | amount=0/1，URI=身份材料哈希 |
| 302 | 发行 | 发行方、mint、目标 holding | amount=数量 |
| 303 | 转让 | 持有人、mint、来源 holding、目标 holding | amount=数量 |
| 304 | 销毁 | 持有人、mint、holding | amount=数量 |
| 305 | 暂停 | 发行方、mint | amount=0/1 |
| 306 | 冻结持仓 | 发行方、mint、holding | amount=0/1 |
| 307 | 创建提案 | 发行方、mint、proposal | amount=截止 slot，decimals=法定人数百分比，name=标题，URI=材料哈希 |
| 308 | 投票 | 投票人、mint、holding、proposal、vote | amount=1赞成/2反对/3弃权 |
| 309 | 完成计票 | 任意签名者、mint、proposal | 无 |
| 310 | 创建分红 | 发行方、mint、dividend | amount=原生币数量，name=截止 slot，URI=材料哈希 |
| 311 | 领取分红 | 持有人、mint、holding、dividend、claim | 无 |
| 312 | 关闭分红 | 任意签名者、mint、dividend、当前发行权限钱包 | 无 |
| 313 | 提议新权限 | 当前发行方、mint、接任钱包 | 无 |
| 314 | 接受权限 | 接任钱包、mint | 无 |
| 315 | 强制过户 | 发行方、mint、来源 holding、目标 holding | amount=数量，URI=法律依据哈希 |

## 部署与初始化

导出链固定模板：

```powershell
go run ./cmd/rwa-template
```

Ordexus Studio 示例位于 `ordexus-studio/examples/rwa.ovmasm` 和 `rwa.ovm.json`。先用现有 `wallet deploy-contract` 部署其不可升级字节码，再初始化股权：

```powershell
go run ./cmd/wallet rwa-bootstrap `
  -rpc http://127.0.0.1:8899 `
  -payer-key keys/issuer.json `
  -program PROGRAM_ADDRESS `
  -type share `
  -name "Example Equity" `
  -symbol EXEQ `
  -max-supply 10000 `
  -initial-supply 1000 `
  -document-hash LOWERCASE_SHA256 `
  -identity-hash LOWERCASE_SHA256
```

改为 `-type nft -max-supply 1 -initial-supply 1` 可创建唯一资产 NFT。初始化交易会原子创建规范 mint、发行方 holding，登记发行方准入并按需完成首次发行。

其他操作可通过现有 `contract-call` 提交。客户端必须先为新的 holding、proposal、vote、dividend 或 claim 创建由 RWA 合约拥有、按 `MaxAssetStateBytes` 租金预存的规范状态账户。

Ordexus 的 `src/utils/rwaProtocol.js` 固定同一模板并实现跨语言地址/ABI，`buildSignedRWATransaction` 可在本地完成部署、状态账户创建和各类 RWA 操作签名。钱包在签名前应调用 `getRwaCapabilities`，模板字节或最终确认能力不一致时必须停止。

公开与默认 RPC 提供：

- `getRwaCapabilities []`：固定模板、资产类型和最终确认承诺。
- `getRwaAssets [owner,cursor,limit]`：分页列出 RWA，并附带该 owner 的合规持仓。
- `getRwaState [mint,owner]`：mint、活动公司行动和 owner 持仓状态。
- `getRwaProposal [proposal]`：快照、计票和最终结果。
- `getRwaDividend [dividend,owner]`：资金账务及 owner 是否已经领取。

以上查询只读最终确认区块状态，uint64 资产与资金数值以十进制字符串返回，避免 JavaScript 精度丢失。

## 已知边界

- v1 使用单钱包发行权限加两阶段交接，尚未内置 M-of-N 多签和时间锁；生产发行应由外部多签程序持有权限后再验收。
- 为保证不依赖无限历史检查点，v1 在投票和分红窗口期间锁定所有会改变权重的操作；后续版本可增加每账户检查点以支持并发交易。
- 分红币种目前是链原生币；稳定币分红需要受控 CPI 或独立结算合约。
- 没有接入 KYC 服务、托管机构、产权登记机关、审计预言机或法院接口；`approved` 与所有材料哈希均由发行方负责核验。
- RWA 属于共识执行规则。现网启用前必须协调验证者版本和激活高度，并完成历史重放、快照兼容、模糊测试、第三方审计及目标司法辖区法律审查。

## 验证

```powershell
go test ./programs/vm ./blockchain ./rpc ./cmd/wallet
node ../ordexus/scripts/generate-rwa-fixture.mjs
go test ./runtime -run RWA
cd ../ordexus; node --test tests/rwa.test.mjs; npx tsc --noEmit
```

测试覆盖股权发行/受限转让、非合规接收拒绝、投票快照锁、重复投票、分红舍入与关闭、冻结、暂停、强制过户、权限交接、NFT 唯一供应、模板字节一致性、严格状态解码和失败操作原子回滚。
