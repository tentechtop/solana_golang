# 全节点入口

面向部署者只使用一个节点程序：

```powershell
go build -o .\dist\solnode.exe .\cmd\main.go .\cmd\node_identity.go
.\dist\solnode.exe -config .\cmd\posnode\configs\fullnode-bootstrap.json
```

配置中的 `node_mode` 使用 `fullnode`。节点能力通过 `node_features` 开启：

```json
"node_features": {
  "bootnode_enabled": true,
  "wallet_pairing_enabled": true
}
```

- `bootnode_enabled`：节点在已签名 P2P 记录中声明 bootnode，并提供 DHT 节点发现和中继能力；不会启用验证或共识。
- `wallet_pairing_enabled`：启动钱包配对服务。钱包完成授权后，节点才会写入验证者、共识和 BLS 密钥配置并启用验证能力；质押账户私钥始终留在钱包端。

首次创建网络仍需在首台全节点上配置 `bootstrap_coordinator.enabled=true`。这是创世清单冻结流程，不是另一种对外节点类型；创世冻结前链账本尚不存在，因此该节点只提供节点发现和创世注册服务。创世冻结后，其他全节点按同一份清单加入网络。

`cmd/posnode`、`cmd/bootstrapnode`、`cmd/rpcnode` 和 `cmd/wallet` 保留为兼容入口及开发工具；部署脚本和文档以根目录 `cmd` 构建的 `solnode` 为准。

## 共识启动、退化和分区恢复

所有节点必须在创世配置中使用相同阈值；该值参与 `chain_identity_hash`，配置不同的节点无法加入同一网络：

```json
"consensus_policy": {
  "multi_validator_threshold": 4
},
"emergency_recovery": {
  "enabled": true
}
```

- `bootstrap_elder`：验证者不足阈值且网络从未进入多验证者模式时，由创世验证者持续出块并正常确认，供创世钱包提交注册和质押交易；创世验证者已退出链上集合时，才按权益和确定性破同票规则选择长老。
- `multi_validator`：达到阈值后仍保留至少 100 个区块的确定性交接期，并在统一的 epoch 边界切换；切换后使用完整验证者集合和严格大于 `2/3` 权益 QC。
- `recovery_elder`：网络曾进入多验证者模式后又正常降到阈值以下，或者收到恢复证书时，只由长老继续生成临时区块；`finalized` 不前进。
- 网络分区只会让本地 liveness gate 等待 quorum，不会根据本地 peer 数自动切换长老，因此少数分区不能自行出块或确认。

确认票和同 slot 的超时票属于两个独立共识阶段，不构成双投。超时票会发送给全部验证者，因此预定 leader 离线时，剩余严格超过 `2/3` 权益的节点仍能形成 timeout QC、跳过该 slot 并继续出块。重连节点通过区块同步同时校验和保存对应 QC，不能用只有区块、没有证书的链头参与共识。

分区恢复证书必须由状态接口返回的 `safety_checkpoint` 验证者集合按质押权重严格超过 `2/3` 签署。恢复证书可列出失联验证者，长老必须是剩余成员中权益最高者；证书通过 P2P 控制协议传播。恢复到足够验证者后，还需相同安全委员会签署多验证者激活证书，用它确认临时链头和新验证者集合，随后才恢复最终性。

```powershell
wallet emergency-recovery-create -rpc http://127.0.0.1:8899 -sequence 1 -elder-validator-id ID -consensus-key keys/consensus-1.json -out recovery.json
wallet emergency-recovery-sign -certificate-file recovery.json -consensus-key keys/consensus-2.json -out recovery-signed.json
wallet emergency-recovery-submit -rpc http://127.0.0.1:8899 -certificate-file recovery-signed.json

wallet multi-validator-activation-create -rpc http://127.0.0.1:8899 -sequence 2 -consensus-key keys/consensus-1.json -out activation.json
wallet multi-validator-activation-sign -certificate-file activation.json -consensus-key keys/consensus-2.json -out activation-signed.json
wallet multi-validator-activation-submit -rpc http://127.0.0.1:8899 -certificate-file activation-signed.json
```
