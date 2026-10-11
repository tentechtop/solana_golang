# P2P 公网中继与 QUIC 打洞测试

本工具使用项目真实的 `p2p.Host`、安全握手、签名中继协议和 QUIC/TCP 传输。
它运行两个独立的本地 P2P 测试节点，不启动共识或读写链账本。

## 环境和拓扑

```text
A 192.168.1.4:55201 ──主动连接──> R 101.35.87.31:55101/UDP
B 192.168.1.4:55202 ──主动连接──> R

强制中继：A → R → B → R → A
打洞：R 分发观察到的公网 IP:UDP 端口，A/B 同时从原监听端口拨号对方。
```

- SSH：`root@101.35.87.31:22`。密码仅从 `P2P_TEST_SSH_PASSWORD` 读取；不写入文件。
- 公网独立目录：`/opt/ordexus-core/p2p-wan-probe`。
- 独立 systemd 临时单元：`ordexus-p2p-probe-wan.service`；最长运行两小时，可重新部署。
- 公网 QUIC 端口 `55101/UDP`，TCP 端口 `55102/TCP`，每轮只启动一种传输。
- A/B 的 P2P 端口为 `55201`、`55202`；控制接口分别为 `127.0.0.1:55211`、`127.0.0.1:55212`。
- 公网控制接口仅监听 `127.0.0.1:55111`，通过 SSH 读取。没有公网 HTTP 管理端口。
- 独立安全网络 ID 为 `ordexus-core:p2p-wan-probe:v1`，节点身份在测试目录中独立生成。
- 本地二进制、身份、运行拓扑、JSON 测量数据位于 `tmp/p2p-wan/`，该目录已被 Git 忽略。

同一台 Windows 机器的两个进程共享局域网，但运营商可能为不同流分配不同公网出口。
这不等同于两台独立路由器或两个运营商网络。局域网直连、公网映射打洞、强制中继必须分别记账。

## 构建与部署

在 `ordexus-core` 目录运行。需要 Go、Python、Paramiko 和 PowerShell 7。
SSH 主机密钥必须已存在于当前用户的 `~/.ssh/known_hosts` 或指定的 `--known-hosts` 文件；脚本拒绝未知主机密钥。

```powershell
New-Item -ItemType Directory -Force tmp/p2p-wan | Out-Null
go build -o tmp/p2p-wan/p2pprobe-optimized.exe ./cmd/p2pprobe
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
go build -o tmp/p2p-wan/p2pprobe-optimized-linux ./cmd/p2pprobe
Remove-Item Env:GOOS, Env:GOARCH

# P2P_TEST_SSH_PASSWORD 由当前会话提供，不把真实密码写进命令文件。
python tools/p2p_wan_probe.py deploy --protocol quic --local-ip 192.168.1.4
python tools/p2p_wan_probe.py suite --duration 180
python tools/p2p_wan_probe.py status

# 比较 TCP；先停掉本轮自己的三个测试节点。
python tools/p2p_wan_probe.py stop
python tools/p2p_wan_probe.py deploy --protocol tcp --local-ip 192.168.1.4
python tools/p2p_wan_probe.py suite --duration 180
```

脚本只停止自身保存的本地 PID（同时核对二进制路径和身份参数）及上述专用服务。
不会调用已有链节点部署脚本，不停止验证节点，不改已有防火墙规则。
本地进程后台运行，结束测试后使用 `stop` 清理。重新构建 Windows 二进制前也先 `stop`。
部署前检查服务器的端口、服务和资源，避免与其他用途冲突。

## 单独测速和打洞

```powershell
$a = Invoke-RestMethod http://127.0.0.1:55211/status
$b = Invoke-RestMethod http://127.0.0.1:55212/status
$body = @{
  target = $b.peer_id
  mode = 'relay'
  duration_seconds = 30
  concurrency = 1
  payload_bytes = 65536
  timeout_millis = 15000
  interval_millis = 0
} | ConvertTo-Json
Invoke-RestMethod http://127.0.0.1:55211/load -Method Post -ContentType application/json -Body $body

# QUIC 模式且两端都已连接同一个公网中继时执行。
$body = @{ target = $b.peer_id } | ConvertTo-Json
Invoke-RestMethod http://127.0.0.1:55211/punch -Method Post -ContentType application/json -Body $body
```

`mode=relay` 使用新增的 `Host.RequestRelay(ctx, relayID, targetID, request)`。
它向指定中继发送，并按目标签名身份关联响应；不主动拨号目标。
测试开始时如果已经存在 A/B 直连，会拒绝这轮强制中继测量。
`mode=direct` 要求事先存在目标连接，使用项目原有的 `Host.Request`。
通过 `direct_before`、`direct_after`、`/status` 的连接列表检查测量路径。

`/punch` 返回 `success=true` 后，还要确认 `remote_address` 是中继观察到的目标映射地址，
并用 `mode=direct` 做实际载荷往返，不能仅凭协调消息已发送就判断成功。
打洞失败后仍可调用 `RequestRelay`。本实现没有跨 NAT 的成功保证。

同一局域网也可以主动交换已知内网地址，调用 `POST /dial`，请求体为
`{"address":"/ip4/192.168.1.4/quic/55202/p2p/<B-peer-id>"}`。
这个结果属于内网直连，不能算公网打洞成功。

## 指标解释

- `payload_mib_per_second` 只计算成功返回的业务载荷，单位 MiB/s（1 MiB = 1,048,576 字节）。
- echo 中继的一次往返需要 R 向 B 和 A 分别发送一份数据，因此同等出口带宽下，其有效载荷速度通常低于直接访问 R。
- `p50_ms/p95_ms/p99_ms` 为成功请求的往返延迟。失败请求单独计数，不能只看延迟百分位。
- `concurrency` 是同时在途请求数；窄带公网不要把提高并发直接视为加速。默认写超时是 5 秒，大包排队可能导致写超时及重连。
- 保留错误轮次、超时配置、方向和原始 JSON。改变超时或并发后的结果不能当作同参数的性能提升。
- `suite` 包含两个方向的 1 KiB/64 KiB 传输、双向持续通信、关闭 A 的中继连接、重启公网测试中继，以及 QUIC 打洞/失败回退。
- `fault` 可单独运行故障场景。JSON 中包含注入时间、前后连接状态、失败及恢复时间。

## 实现修复及验证范围

1. QUIC 使用同一个 `quic.Transport`/UDP socket 接受和拨出连接；公网观察端口与监听端口一致。
2. 中继响应交回请求管理器，验证内部来源、目标、消息 ID 与签名信封的一致性；安全 Host 拒绝未签名中继来源，拒绝消息不会先写入回程路由。
3. 中继去重缓存不再每包全表扫描；每秒至多清理一次，单个条目的过期判断仍精确执行。签名校验不占用缓存锁。
4. 打洞仅接受已连接且具有中继能力的介绍者；限制协调时间，给握手合理的拨号时间，避免对已有连接重复启动读循环。
5. 旧连接退出或异步写失败只清理对应连接 ID，保护已经建立的替代连接。
6. QUIC 关闭不再在取消流后发送 FIN，避免把正常关闭报告为错误。
7. 直连拨号退避不再误伤中继请求及发送回退；节点封禁仍然生效。此处没有取消退避或自动重放业务请求。

```powershell
go test ./p2p ./cmd/p2pprobe ./cmd/p2pstress -count=1
go vet ./p2p ./cmd/p2pprobe ./cmd/p2pstress
go test ./p2p -run '^$' -bench BenchmarkRelayAccept -benchtime=1s
```

三节点集成测试覆盖真实 QUIC 安全握手、两个方向的并发中继、来源校验、打洞直连、重复打洞、取消请求清理和关闭。
回环集成测试验证的是协议与 socket 复用，不模拟运营商 NAT。
公网测试结果见 [2026-09-08 测试报告](p2p-wan-test-2026-09-08.md)。

实现依据：同一 UDP socket 同时接受和拨号是 [quic-go Transport 文档](https://quic-go.net/docs/quic/transport/) 支持的能力。
不同 NAT 的映射、过滤和回环行为并不相同，参见 [RFC 4787，第 4–6 节](https://www.rfc-editor.org/rfc/rfc4787.html)。
