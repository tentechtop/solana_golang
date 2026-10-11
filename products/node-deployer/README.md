# Ordexus 节点部署器

在 Windows 电脑上填写 Linux 服务器 IP、SSH 用户和密码，部署一个全节点，连接已有 Ordexus 网络。部署器自动识别服务器 x64 / ARM64 架构，上传随包提供的节点二进制、核对 SHA-256、创建独立 systemd 服务，并设置服务器开机启动。全节点同步已有账本，不自动创建新链、质押或持有创世钱包密钥。

## 使用

1. 将完整部署包解压到固定目录，双击 `OrdexusNodeDeployer.exe`。程序打开本机浏览器界面，只监听 `127.0.0.1`。
2. 填写服务器 IP、SSH 用户和密码。SSH 默认端口 22；服务器需要 Linux、systemd、curl、sha256sum、ss，以及 root 或可使用 sudo 的用户。
3. 首次使用设置已有网络的引导 RPC，点击「保存为默认网络」。以后使用同一个网络就只需填写服务器连接信息；公开地址保存在部署包的 `config/network.json`，不保存 SSH 密码。
4. 点击「检查服务器」。首次连接时核对 SSH 指纹，点击界面中的信任按钮继续。已保存服务器密钥发生变化时，连接会停止。
5. 检查通过后点击「开始部署」，查看进度、最终链 ID、服务名和 RPC 地址。

高级设置支持节点名称、P2P / RPC 端口、网络共识门限、firewalld / UFW 放行，以及是否开放 RPC。默认全节点不参与验证或签署区块；RPC 只监听服务器回环地址。默认名称 `fullnode-01`，P2P `5102`，RPC `8902`。P2P 端口和需要开放的 RPC 端口还需在云安全组允许；部署器不管理云账号。

部署目录为 `/opt/ordexus-nodes/<节点名称>`，服务为 `ordexus-node-<节点名称>.service`，服务以独立 `ordexus-node` 用户运行。同名目录、现有服务或端口已占用时停止部署，不覆盖原账本、不停止其他节点。当前版本用于新增节点，升级已有节点需另行维护。

## 连接已有网络

引导 RPC 必须支持 `getBootstrapManifest`，清单就绪、链身份与当前节点版本兼容，并提供服务器可达的 TCP / QUIC P2P 地址。部署器复用核心的清单校验和链身份计算，同时核对 `config/genesis-account.json` 的统一创世公钥与 100 亿 ORDU 初始发行量；不以自行生成创世配置解决兼容问题。

当前手机测试链的 P2P 均绑定 `127.0.0.1`，仅有局域网 RPC 转发，公网服务器不能直接加入。需要可达的已有引导节点，或另行配置该链的 P2P 网络。历史公网 RPC 若不支持当前清单接口，也不能作为部署目标网络。

部署成功会区分「正在同步」和「等待 P2P 连接」，不把 systemd 服务启动当作链数据已经完全同步。可在服务器查询：

```sh
systemctl status ordexus-node-fullnode-01.service
journalctl -u ordexus-node-fullnode-01.service -n 100 --no-pager
```

RPC 保持回环监听时，可用 SSH 隧道访问：

```sh
ssh -L 8902:127.0.0.1:8902 用户@服务器IP
```

密码仅在本次进程内用于 SSH / sudo，不写入配置、命令参数、环境变量或日志。已信任的 SSH 公钥记录在当前 Windows 用户配置目录的 `OrdexusNodeDeployer/known_hosts`；其中不含密码或钱包密钥。

## 从源码打包

在 `ordexus-core` 中使用 Go 1.24.6 和 PowerShell：

```powershell
.\tools\build_node_deployer.ps1
# 已确认的引导 RPC 可作为默认地址写入部署包：
.\tools\build_node_deployer.ps1 -BootstrapRPC 'https://你的引导节点/'
```

输出为 `dist/ordexus-node-deployer/`，包含 Windows EXE、Linux 两种架构二进制、公开创世配置、说明与文件校验和。目标服务器无需安装 Go。

## 本次验证范围

2026-10-10 已通过部署器与核心配置测试、Go vet、Windows EXE 和 Linux amd64 / arm64 构建。SSH 集成测试使用本机临时 SSH 服务，核对首次信任前不发送密码、服务器密钥变化时拒绝连接，以及二进制上传内容完整。实际界面已检查高级设置、默认网络保存、连接失败提示和部署按钮状态。

尚未提供目标服务器凭据与可用引导 RPC，本次没有在真实服务器安装或启动节点。首次实际部署时仍需确认服务器可达的引导 P2P 地址、网络共识门限和云安全组。
