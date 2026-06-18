六节点测试配置

本目录用于“本机 + 4 台 Linux 虚拟机 + 1 台苹果电脑”的六节点 PoS 测试。

节点：
- node-local：本机，占位 IP 为 127.0.0.1，跨机器测试前必须改成其他机器能访问的本机局域网 IP。
- node-136：192.168.181.136
- node-137：192.168.181.137
- node-138：192.168.181.138
- node-139：192.168.181.139
- node-223：192.168.120.223

注意：
- 六台机器必须互相路由可达，且开放 TCP 5100 到 5105。
- genesis 必须完全一致，否则状态根、验证者集合和 leader schedule 会分叉。
- 创世资金账户由 consensus.HardcodedGenesisTreasurySeed 写死，多节点会推导出同一个 treasury 地址。
- 节点属性通过配置控制，不需要重新打包：本节点使用 node_role 和 node_capabilities，bootstrap_peers 中的对端使用 role 和 capabilities。
- role 支持 full、validator、bootnode、bootstrap、archive，其中 bootstrap 会按 bootnode 处理。
- capabilities 支持 relay、archive、validator、state_sync、dht；需要归档能力时只增加 archive，不要回退或补偿其他字段值。
- 公网中继节点必须配置 relay 能力；两个内网节点都先连上该公网节点后，普通 QUIC 直连失败会自动请求中继协调 QUIC 打洞。
- QUIC 打洞只使用中继节点观测到的 UDP 映射地址，打洞成功后节点之间走 QUIC 直连；失败时业务消息自动通过已连接 relay 节点转发兜底。

示例：
```json
{
  "node_role": "bootnode",
  "node_capabilities": ["relay", "dht", "archive"],
  "bootstrap_peers": [
    {
      "peer_id": "peer id",
      "ip": "127.0.0.1",
      "port": 5101,
      "network": "tcp",
      "role": "bootnode",
      "capabilities": ["relay", "dht"]
    }
  ]
}
```
