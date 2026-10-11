# Dynamic bootstrap startup

## 大额质押收益分账升级

`stake_reward_exact_ratio_epoch` 指定委托／自质押分账使用精确整数比例的生效 epoch，所有出块、验块和历史同步节点必须使用同一值。新链省略时为 0，从创世开始使用修复算法。已有账本首次升级必须设置尚未到达的 epoch；之前的分账保留旧算术，以保持历史奖励根与状态根兼容。节点把约定生效值存入本地账本元数据，重启时拒绝更改或遗漏该值。此参数不修改创世、链 ID、身份或数据路径，不自动重新分配已经结算的历史余额。

手机四节点可将 `tools/ordexus_termux_stake_payout_upgrade.sh` 放到现有链目录，在实际 Termux 终端执行。脚本要求已经暂存并校验新二进制，核对四节点链 ID 和进程权限，先正常停止，再备份账本、配置、日志和旧二进制，并设置同一个未来生效 epoch。ADB `run-as` 无法向此手机的应用进程发信号，脚本会在修改前拒绝这种运行方式。具体本机准备状态见 `doc/termux-four-node-testing.md`。

现状：旧配置把公网、Mac、本地验证者直接写进 `genesis.initial_validators`，会导致“所有验证者”里长期残留旧机器身份。

目标：只先启动公网引导节点；其他节点启动后显示钱包配对二维码；钱包扫码授权后重启节点，节点自动向引导节点注册。至少一个验证者完成注册后，引导节点即可冻结创世 manifest；运行期仅在在线验证者权益严格大于总有效权益的 `2/3` 时出块。出块轮次从已确认链头顺延，`slot_millis` 是单个网络阶段预算，leader 基础超时为其 `3` 倍，不再用创世时间计算固定时钟。PrepareQC、ConfirmQC 与 TimeoutQC 都需要严格 `>2/3` 权益；有效提案先聚合 PrepareQC，然后才允许签 Confirm 并以 ConfirmQC 提交。Prepare 后未见 PrepareQC 仍可投 timeout，Confirm 与 timeout 则严格互斥；两类决定证书的交集严格大于最大拜占庭权益，防止冲突证书同时形成。连续形成 TimeoutQC 时等待窗口按 `1x/2x/4x/8x` 退避；本地超时只发送 timeout vote，未形成 TimeoutQC 不换轮。创世后的每个提案都必须引用父块 ConfirmQC，本地投票写盘成功后才会广播。

启动顺序：

1. 公网机器启动 `bootstrap-public.json`。
2. 每台待加入机器复制 `join-wallet-scan.json`，至少修改 `node_name`、`data_path`、`peer_seed`、`listen_port`、`rpc_port`，公网部署还要写正确 `advertised_ip`。
3. 启动加入节点后，用钱包扫码。CLI 回退命令是 `wallet validator-pair -payload <二维码payload> -staker-key <钱包key> -lamports 10000000`。
4. 扫码完成后节点配置会写入 `validator_key_path`、`consensus_key_path`、`bls_key_path`、`staker_address`、`bootstrap_join.staker_signature`。
5. 重启加入节点。首个验证者进入创世集合；创世冻结后再扫码的节点会由钱包自动提交链上注册交易，并在交易确认后的下一 epoch 加入验证者集合。

边界：

- 不要再手写 `genesis.initial_validators`。
- 不要复用历史静态验证者身份。
- 同一台机器起多个节点时，`data_path`、P2P/RPC 端口必须不同。

## 停链后的验证者恢复

普通注册交易仍然只能通过区块生效。网络因在线权益不足而停链时，不能直接信任 mempool 中的质押交易；否则每个节点看到的交易集合不同，会破坏共识。恢复流程使用预先配置的治理多签证书，只临时授权证书中唯一的一笔验证者注册交易参与当前权益集合，交易入块后继续走正常的链上激活规则。

所有验证者必须预先配置相同的治理公钥和阈值，例如：

```json
"emergency_recovery": {
  "enabled": true,
  "governance_public_keys": ["公钥1", "公钥2", "公钥3"],
  "threshold": 2
}
```

操作步骤：

1. 从任一停在相同链头的管理 RPC 创建证书并由第一个治理密钥签名：
   `wallet emergency-recovery-create -rpc http://127.0.0.1:8899 -chain-id CHAIN -sequence 1 -staker-key keys/staker.json -validator-address ADDRESS -consensus-address ADDRESS -bls-public-key KEY -peer-id PEER -lamports 30000000 -governance-key keys/governance-1.json -out recovery.json`
2. 其余治理成员依次追加签名：
   `wallet emergency-recovery-sign -certificate-file recovery.json -governance-key keys/governance-2.json -out recovery-signed.json`
3. 将同一份达到阈值的证书提交到仍在线的每个验证者管理 RPC，并提交到新验证者节点：
   `wallet emergency-recovery-submit -rpc http://127.0.0.1:8899 -certificate-file recovery-signed.json`
4. `sequence` 必须严格递增；证书绑定链 ID、当前高度、当前块哈希、过期时间和注册交易，不能跨链或换交易复用。

恢复权益加入后的在线权益仍必须严格大于“原有效权益 + 恢复验证者权益”的 `2/3`，否则节点继续等待，不会强行出块。治理私钥不要放在验证者主机上。
