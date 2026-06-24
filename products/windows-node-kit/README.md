# Windows 节点部署包

这个包给 Windows 用户加入公网引导网络使用。用户不需要手写创世验证者，只需要配置本机节点信息，启动节点后用钱包扫码授权，重启后节点会自动向公网引导节点注册。达到网络门限后自动出块。

## 运行要求

- Windows 10/11 x64。
- PowerShell。
- 网络可被其他节点访问：至少需要把 P2P 端口转发到本机。
- 钱包扫码时，手机需要能访问节点 RPC 地址。

## 一、解压

把压缩包解压到固定目录，例如：

```powershell
C:\solana-golang-node
```

后续命令都在这个目录执行。

## 二、配置节点

打开 PowerShell，进入解压目录：

```powershell
cd C:\solana-golang-node
```

配置节点：

```powershell
.\scripts\configure-node.ps1 -NodeName validator-win-01 -AdvertisedIP 你的公网IP -ListenPort 5102 -RPCPort 8899
```

参数说明：

- `NodeName`：节点名，每台机器必须不同。
- `AdvertisedIP`：公网可访问 IP。家庭宽带需要在路由器上把 `ListenPort` 转发到这台 Windows 机器。
- `ListenPort`：P2P 端口，每台机器必须不同。
- `RPCPort`：本机 RPC 端口，钱包扫码要访问这个端口。

同一台机器部署多个节点时，必须给每个节点使用不同的 `NodeName`、`DataPath`、`ListenPort`、`RPCPort`。

## 三、打开防火墙

用管理员 PowerShell 执行：

```powershell
.\scripts\open-firewall.ps1 -ListenPort 5102 -RPCPort 8899
```

安全边界：

- P2P 端口需要外部可访问。
- RPC 端口只建议在可信网络开放，用于钱包扫码和本机查询。

## 四、启动并扫码

普通 PowerShell 执行：

```powershell
.\scripts\start-node.ps1
```

终端会显示钱包配对二维码。用钱包扫码完成授权。

如果钱包暂时不能扫码，可以复制终端里的 `posvalpair:` payload，在钱包 CLI 里执行：

```powershell
.\bin\wallet.exe validator-pair -payload "posvalpair:..." -staker-key .\keys\staker.json -lamports 10000000
```

扫码完成后，配置文件会自动写入：

- `staker_address`
- `validator_key_path`
- `consensus_key_path`
- `bls_key_path`
- `bootstrap_join.staker_signature`

## 五、重启节点

扫码成功后，在节点窗口按 `Ctrl+C` 停止，再执行：

```powershell
.\scripts\start-node.ps1
```

重启后节点会自动注册到公网引导节点。网络达到门限后自动出块。

## 六、查看状态

```powershell
.\scripts\status-node.ps1
```

如果看到 `bootstrap_join` 或 `chain_progressing` 相关字段，说明节点已经进入自动加入流程。

## 七、设置开机自启

节点扫码并重启确认正常后，用管理员 PowerShell 执行：

```powershell
.\scripts\install-startup-task.ps1
```

取消开机自启：

```powershell
.\scripts\uninstall-startup-task.ps1
```

停止后台节点：

```powershell
.\scripts\stop-node.ps1
```

## 常见问题

1. 钱包扫不到节点

   检查手机和电脑是否在同一网络，检查 Windows 防火墙是否放行 RPC 端口。

2. 节点注册不上

   检查 `AdvertisedIP` 是否公网可达，路由器是否转发了 P2P 端口。

3. 仍看到旧验证者

   这是旧链数据或旧引导 registry 残留。必须使用新的公网引导节点数据目录，或清理旧数据后重新启动引导节点。

