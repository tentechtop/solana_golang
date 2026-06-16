# Turbine 网络分层实现报告

生成时间：2026-06-16

## 实现目标

实现类似 Solana Turbine 的分层传播：

- leader 不再把 proposal 广播给全网。
- leader 只发给 Turbine 第一层子节点。
- 收到 proposal 的节点验证通过后，只转发给自己的直接子节点。
- 所有验证者基于相同 `epoch snapshot + slot + leader + fanout` 独立计算自己的层级、父节点和子节点。
- `getNodeStatus` 和 `getMetrics` 能暴露当前节点所在 Turbine 层。

## 核心设计

拓扑输入：

- `EpochSnapshot`
- `slot`
- `leader_id`
- `turbine_fanout`

确定性规则：

- leader 固定为 root，layer 为 `0`。
- 非 leader 验证者按 `RandomSeed + SnapshotStateRoot + slot + validator_id` 计算哈希排序。
- 按数组堆规则生成 fanout 树：
  - 节点 `i` 的父节点为 `(i - 1) / fanout`
  - 节点 `i` 的子节点为 `i*fanout+1 ... i*fanout+fanout`
- 每个节点本地可计算：
  - `layer`
  - `parent_peer_id`
  - `child_peer_ids`

## 代码变更

新增核心文件：

- `consensus/turbine.go`
- `consensus/turbine_test.go`
- `cmd/posnode/turbine.go`
- `cmd/posnode/turbine_test.go`

配置新增：

```json
{
  "turbine_fanout": 2
}
```

默认值：

- `DefaultTurbineFanout = 2`
- 最大值：`MaxTurbineFanout = 1024`

状态接口新增字段：

```json
{
  "turbine": {
    "slot": 1,
    "fanout": 2,
    "layer": 1,
    "leader_id": "...",
    "leader_peer_id": "...",
    "parent_validator_id": "...",
    "parent_peer_id": "...",
    "child_validator_ids": ["..."],
    "child_peer_ids": ["..."],
    "validator_in_tree": true,
    "turbine_available": true
  }
}
```

metrics 新增：

- `turbine_layer`
- `turbine_fanout`
- `turbine_parent_peer`
- `turbine_child_count`

## 验证命令

局部验证：

```powershell
go test ./consensus -run TestTurbine -count 1
go test ./consensus ./cmd/posnode -run "TestTurbine|TestNormalizeNodeConfig|TestHTTPJSONRPC" -count 1
```

局部结果：

```text
ok  	solana_golang/consensus	0.645s
ok  	solana_golang/cmd/posnode	0.947s
```

全量门禁：

```powershell
go test ./...
go vet ./...
go build ./cmd/posnode
```

全量结果：

```text
ok  	solana_golang/cmd/posnode	0.950s
ok  	solana_golang/consensus	0.350s
```

`go vet ./...` 和 `go build ./cmd/posnode` 通过，临时 `posnode.exe` 已清理。

## 结论

代码级 Turbine 分层传播已实现。leader proposal 传播路径从全网广播改为分层 fanout，节点可以通过状态和指标知道自己在 Turbine 树中的层级、父节点和子节点。

仍需真实多机验证：

- 5 台机器连续运行 30 分钟、2 小时、24 小时。
- 比对每台节点的 `slot`、`height`、`leader`、`turbine.layer`、`turbine.parent_peer_id`、`turbine.child_peer_ids`。
- 故障注入：第一层节点掉线、第二层节点延迟、乱序 proposal、leader 离线。
