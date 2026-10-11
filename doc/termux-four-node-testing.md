# 安卓 Termux 四节点测试

## 2026-10-10 手动停止

北京时间 14:31 按用户要求停止 USB 手机 `2211133C`（ADB serial `11785d69`）上的节点运行。在实际 Termux 终端执行当前链的 `stop.sh` 后，复查四个节点和 LAN RPC 转发进程均不存在，回环 RPC `29101–29104`、TCP P2P `19401–19404` 及 LAN RPC `192.168.1.3:29101` 均无法连接，`termux:service-wakelock` 已释放。

链 ID、账本、配置和日志保留。本次仅停止运行，保留既有 Termux:Boot 开机自启入口；手机下次重启仍会自动恢复这条链。不得依据后文历史在线记录自行恢复电脑节点。检查证据在忽略目录 `tmp/ordexus-termux-20261009-224453-e7f705b6/manual-stop-20261010.json`。

## 2026-10-09 当前部署

设备为用户指定的 USB 手机 `2211133C`，ADB serial `11785d69`，安卓 13、ARM64。安装了官方 GitHub 的 Termux `v0.118.3` ARM64 包，APK 的 SHA-256 与该 release 的校验文件一致。未 Root。

当前测试链 ID：`ordexus-termux-20261009-224453-e7f705b6`。四个验证节点运行在同一台手机的 Termux 中，采用四份临时验证者身份、相同的测试创世配置、四个独立账本和加密 TCP P2P。用户要求仅保留这台手机的四节点，电脑节点已经停止。

统一创世钱包地址为 `T9kwu18BR43jxaL5GUH7ZwWXXYAoGEAXTv82KJFupEMyP`，链上原始公钥为 `9kwu18BR43jxaL5GUH7ZwWXXYAoGEAXTv82KJFupEMyP`。初始发行量为 100 亿 ORDU（`10000000000000000000` atomic units）。公开配置保存在 `config/local/genesis-account.json`；手机和电脑的测试链生成脚本均读取它，不再生成另一个创世账户。手机节点只持有此公钥，不持有用户创世钱包的私钥或助记词。

初始四节点实测创世余额均为 `9999999999460727120` atomic units，即 `9,999,999,999.460727120 ORDU`。少量差额用于四名测试验证者的初始质押及创世账户分配。

原手机链 `ordexus-termux-20261009-220225-c1e4d4d3` 使用了不同的临时创世账户，已正常停止；旧配置、账本和日志保留。当前手机链使用新链 ID 和新数据目录重建，不将旧测试交易记录伪装成新账本历史。

| 节点 | 手机 RPC | 手机 P2P |
| --- | --- | --- |
| node1 | `127.0.0.1:29101` | `127.0.0.1:19401` |
| node2 | `127.0.0.1:29102` | `127.0.0.1:19402` |
| node3 | `127.0.0.1:29103` | `127.0.0.1:19403` |
| node4 | `127.0.0.1:29104` | `127.0.0.1:19404` |

已验证：

- 四个独立节点进程启动成功，节点显示 `multi_validator`、四名验证者、三个已知 peer 和安全 P2P 会话。
- 当前四节点高度从 261 推进至 268；最终确认 slot 266 的区块哈希、状态根、交易根、回执根均一致，创世余额一致，交易提交能力为 ready。
- 历史手机链验证了两笔各 1 ORDU 的测试转账，第二笔在四节点最终确认，收款余额均为 `2000000000` atomic units。本次重建后的链仅做只读核验，没有搬运创世钱包密钥或代用户签署交易。
- `getMarketCapabilities`、`getMarketAssets`、`getMarketOrders` 在四个节点均正常响应；新测试链的资产和订单列表为空。这不代表已经验证挂单、成交和撤单。
- `go test ./rpc ./internal/posnode -run 'Market|FullNode' -count=1` 通过。
- Python 准备脚本通过语法检查，手机上的启动、停止和状态脚本通过 `bash -n`。

运行期间四节点合计 RSS 约 0.91 GiB，电池温度读数约 34°C；这些是短时测试读数，不是最低配置、持续功耗或峰值内存结论。每个节点设置 `GOMAXPROCS=2`、`GOMEMLIMIT=384MiB`、`GOGC=75`；`GOMEMLIMIT` 是 Go 运行时的软目标，并非进程内存硬上限。

当前证据保存在忽略的本地目录 `ordexus-core/tmp/ordexus-termux-20261009-224453-e7f705b6/`，包括 `manifest.json`、`verification.json`、`verify-readonly.py` 和 `pc-stop-report.json`。历史转账证据保留在旧手机链目录；临时测试身份不要提交、转发或用于真实资产。

电脑停止记录：停止 12 个链节点（3 组四节点）、旧电脑 LAN RPC 与对应 FRP 客户端；`Solana-FRP-Wallet-RPC` 自动恢复任务已禁用。电脑账本、配置和日志未删除，计划任务原定义保存在当前证据目录。未修改公网服务器上的其他服务。

## 手机上管理

手机目录：

```text
/data/data/com.termux/files/home/ordexus-termux-20261009-224453-e7f705b6/
```

已建立快捷入口 `~/ordexus-node-test`。在 Termux 中运行：

```sh
bash ~/ordexus-node-test/status.sh
bash ~/ordexus-node-test/stop.sh
bash ~/ordexus-node-test/start.sh
```

以上命令在手机 Termux 终端中执行，分别为查询状态、正常停止和恢复当前账本；按需要选择命令。日志位于 `~/ordexus-node-test/logs/node1.log` 至 `node4.log`。启动前检查端口占用；停止时核对 PID 的命令行与节点配置路径，避免结束其他进程。ADB `run-as` 与实际 Termux 终端可能受到不同的 Android 进程权限限制，不将 `run-as` 的信号失败当成节点已停止。手机开机启动已按下节配置；电脑旧节点自动恢复任务保持禁用。

手机上的钱包可以使用 `http://127.0.0.1:29101/` 访问这条独立测试链；其他手机不能通过自己的 `127.0.0.1` 访问它。节点本身的 RPC 和 P2P 仍绑定回环地址；已另加下面的局域网 RPC 入口，没有开放公网入口。

### 手机开机启动（2026-10-10，已安装并验证）

用户已要求当前手机的四节点开机启动。`tools/termux_start_on_boot.sh` 已复制到当前手机链目录的 `start-on-boot.sh`，由 `~/.termux/boot/10-ordexus-nodes` 调用，手机文件权限为 `0700`，校验和与本地一致。脚本仅恢复现有配置和账本，不生成新链或身份；通过可执行文件及完整配置参数识别已有节点，避免重启后 PID 复用和重复启动。开机任务执行后先启动四节点，局域网 RPC 按原监听 IP 和客户端允许列表重试最多五分钟，等待 Wi-Fi 就绪。脚本不是持续监控，手动停止后不会自动重新启动。

已下载官方 GitHub Termux:Boot `v0.8.1`；APK SHA-256 为 `97e6d336f05b59f7cb25e8efa7173e0a6e665f3022b75c6d55a69b6399ecacc8`，与官方校验文件一致，其签名证书与现有 GitHub Termux APK 相同。不能混装不同来源的 Termux 插件，也不能为换签名卸载现有 Termux。官方要求安装后打开 Termux:Boot 一次，见 [Termux:Boot 使用说明](https://github.com/termux/termux-boot#how-to-use)。

已安装并打开 Termux:Boot，确认 `flock` 可用、开机广播权限已授予；Termux 和 Termux:Boot 均在电池优化白名单中，小米「自启动管理」内两者开关均为开启。重复执行启动入口时复用原四个 PID，没有新增节点进程。

实际正常停止并重启手机两次，均由 Termux:Boot 的开机广播和 JobScheduler 日志确认自动执行，没有手动执行恢复命令。第一次四节点最终确认 slot 7130 一致；第二次在 07:17:10 自动启动节点、07:17:17 恢复 LAN RPC，节点 PID 为 `12611/12613/12615/12617`，最终确认 slot 7164 的区块及状态根一致，随后继续增长。两次均保持原链 ID、节点配置校验和和二进制，账本继续推进。第二次核验时屏幕为 `Asleep`，`termux:service-wakelock` 已持有；这是短时熄屏证据，不代表数小时后台可靠性已经验证。

第二次开机任务从 07:13:06 排队至 07:17:09，期间 JobScheduler 明确显示 `Restricted due to: thermal`，系统 Thermal Status 为 3；温控状态恢复为 0 后任务自行执行。高温时可能延后自启，本次没有禁用或绕过温控。证据位于当前忽略目录的 `boot-setup.json`、`boot-before-first-reboot.json`、`boot-after-first-reboot.json`、`boot-before-reboot.json`、`boot-after-reboot.json` 和 `boot-second-thermal-job.txt`。电脑节点仍为零，旧 FRP 启动任务仍为 Disabled。

### 从另一台手机的钱包连接

2026-10-09 实际地址：节点手机 Wi-Fi `192.168.1.3`，钱包手机 PLG110 为 `192.168.1.5`。在钱包的「账户 → RPC 节点 → 自定义」输入并保存：

```text
http://192.168.1.3:29101/
```

已经从钱包手机实际调用 `getHealth` 和 `getNodeStatus`，返回 `ok=true`、`chain_progressing=true`、四名验证者和当前测试链 ID，当前链检查时高度为 938。电脑也已通过该地址验证链 ID；历史链阶段验证不在允许列表中的源 IP 收到 HTTP 403。

局域网转发程序运行在节点手机 Termux 中，仅监听 `192.168.1.3:29101`，转发到该手机 node1 的 `127.0.0.1:29101`；允许客户端为当前钱包手机 `192.168.1.5` 和测试电脑 `192.168.1.4`。同一端口监听在不同的具体 IP 地址上，原有 loopback RPC 和节点间连接无需修改。连接不经过电脑转发，也不依赖 USB；两台手机仍需在可互通的同一 Wi-Fi，节点手机和 Termux 必须继续运行。

转发程序和代码保存在本测试目录的 `bin/ordexus-rpc-lan`、`lan-rpc/main.go`；PID 为 `pids/lan-rpc.pid`，日志为 `logs/lan-rpc.log`。本次已将 `start-lan.sh` 和 `stop-lan.sh` 接入手机上这条测试链的 `start.sh` / `stop.sh`，启停节点时一起管理 LAN 转发。也可在 Termux 单独执行：

```sh
bash ~/ordexus-node-test/start-lan.sh
bash ~/ordexus-node-test/stop-lan.sh
```

这是独立的手机测试链，统一创世账户不代表不同链共享交易历史；余额以当前链账本为准。Wi-Fi IP 改变时需更新 LAN 监听地址或允许的客户端 IP 并重启转发；其他新生成的测试链不会自动包含本次 LAN 转发配置。LAN 转发与四节点均从实际 Termux 终端启动，已核对单独启停与恢复。

Termux 启动脚本申请 wake lock，停止脚本在节点退出后释放它；系统检查确认 `termux:service-wakelock` 已持有。初次部署发送熄屏事件时设备仍为 `Awake`；2026-10-10 的开机测试补充验证了两次重启和短时 `Asleep` 状态下的出块。仍需保持 Termux 的后台运行条件；尚未验证数小时后台、深度休眠或厂商进程清理。未进行压力吞吐和跨 NAT 测试，四节点均位于同一手机，不能声称四台独立机器的容灾能力。

## 重新生成一条临时测试链

在 `ordexus-core` 下使用 Windows PowerShell 先构建：

```powershell
go build -trimpath -o tmp/mobile-tools/ordexus-node-identity.exe ./cmd/posnode
go build -trimpath -o tmp/mobile-tools/ordexus-wallet-cli.exe ./cmd/wallet
$taskGoos = $env:GOOS
$taskGoarch = $env:GOARCH
$taskCgo = $env:CGO_ENABLED
try {
    $env:GOOS = 'android'
    $env:GOARCH = 'arm64'
    $env:CGO_ENABLED = '0'
    go build -trimpath -o tmp/mobile-tools/ordexus-node-android-arm64 ./cmd/posnode
    if ($LASTEXITCODE -ne 0) { throw 'Android build failed' }
} finally {
    $env:GOOS = $taskGoos
    $env:GOARCH = $taskGoarch
    $env:CGO_ENABLED = $taskCgo
}
```

准备脚本要求明确设备 serial，手机已安装并打开官方 GitHub 的可调试 Termux 包以完成初始化：

```powershell
python tools/prepare_termux_test_cluster.py --device 11785d69 --deploy
```

每次调用都会生成新的链 ID 和临时验证者身份，但创世账户和初始发行量始终来自公开的统一配置。脚本输出 manifest 与手机启动命令；不会停止旧链，也不会自动执行新链启动。四节点占用相同端口，因此另起测试链前应正常停止当前测试链。脚本使用本项目已有本地测试集群的静态创世生成方式，仅用于隔离的临时链；正式网络沿用钱包配对和动态加入流程。

二进制通过 ADB push 搬运；配置通过 ASCII base64 stdin 写入 Termux 私有目录，远端文件校验和与本地逐一比对。这样避免 Windows ADB 对二进制 stdin 的文本转换。部署后执行临时公开 seed 的 peer-ID 派生，确认安卓二进制可执行。

## 钱包链上市场提示的诊断

2026-10-09 实测现有局域网入口 `http://192.168.1.4:29006/`、其 node1 `http://127.0.0.1:29016/`、历史 FRP 入口 `http://47.80.19.36/` 和默认公网入口 `http://101.35.87.31:8900/` 的 `getMarketCapabilities` 均返回 `-32601 method not found`。钱包 `src/utils/marketProtocol.js` 将此错误显示为“当前节点尚未支持链上市场，请切换到已升级的节点”。因此，仅重装钱包或刷新页面不能给这些旧节点增加市场接口。

手机节点提供 V1 市场接口，但原钱包固定模板 hash 为 `a320c314099aee2c3b95bdd0e9f09690eddd86b432b0a604cc792ac56776193d`，手机节点的模板 hash 为 `2c820982dad5807501df75177e560f9b295ae37ed94df42d8d4a29325213d8a9`，因此曾报“节点的市场交易规则与钱包版本不一致”。旧模板使用 `SVM1`，当前编译模板使用 `OVM1`；RWA、稳定币、奖励、普通代币和众筹也存在同类固定模板问题。

钱包修复采用 `src/utils/vmTemplateCompatibility.js` 统一检查：校验返回字节的完整 SHA-256、内嵌校验和及允许的 OVM1/SVM1 头；除四字节名称和随之变化的校验和外，代码、只读数据、manifest 权限和资源限制必须与本地固定模板相同。保留 V1、最终确认和各业务规则校验，拒绝修改过的合约。程序识别继续接受精确匹配的历史 SVM1 合约，普通代币的历史图标元数据也经过完整代码与校验和核验。

六类当前模板从 `ordexus-core/cmd/*-template` 导出并与旧产物逐字节核对语义，新部署统一使用 OVM1。钱包实际读取手机节点的市场、RWA、稳定币和奖励能力均已通过，市场订单列表正常返回空数据。相关 60 项测试通过；全量测试及 Android 打包结果以钱包 `tmp/ovm-compat-*` 的本次日志为准。

更新目标为无线 ADB 设备 `192.168.1.5:41135`（PLG110），使用现有 `scripts/build-install-android.ps1 -DeviceId ...` 构建并通过 `adb install -r` 覆盖安装 `network.ordexus.wallet`，保留账户和本机密钥存储。Windows junction 的逻辑目录与 Metro 的物理路径不一致会导致入口解析失败，打包命令需在实际目录中执行；对外文档和新增文件仍使用 `ordexus-wallet` 名称。

### 质押提交和合约账户修复（2026-10-09 23:32）

钱包原有提交逻辑会优先将局域网 RPC 的普通转账和质押发送到默认公网 RPC，地址和余额因此不再属于用户选择的手机链。`chainOperations.js` 已取消交易提交的隐式跨网切换；质押在选中 RPC 上获取区块哈希、模拟、提交并检查交易可见性。节点模拟的权限错误在广播前显示，区块执行错误也不再显示为成功。

`walletBusiness.js` 同时限制资产和 DPoS 查询回退：自定义 RPC 失败或验证者列表不完整时保留当前链结果及错误，不能用默认公网链补余额或仓位。已知公共 RPC 的备用结果也必须提供相同 `chain_id`，未知链 ID 不混合数据。

截图中的代币 `transfer(to, amount)` 目标是发送者自己，原钱包将同一余额公钥重复加入消息，节点在模拟时拒绝 `duplicate account keys`。钱包现已合并消息账户的签名/可写权限并重映射 ABI 索引，保留指令中重复的参数位置。节点同时修复对同一余额账户读出两份状态后重复写入的问题：自转账保持余额及供给不变，仍验证签名、账户归属、可写性、余额、NFT 数量及授权额度；授权自转账仍消耗额度。普通转账和合约字节码格式不变。

手机四节点已使用相同新二进制 SHA-256 `555461bbd856385264a43701cdc6767e2ddc5f568014722e68a7abfe44da051d` 重启，保留现有链 ID、验证者身份、账本、用户代币与隐私存款。重启后四节点高度从约 2930 继续增长至 2933，最终确认区块 2931 的区块哈希和状态根一致；升级前二进制保留在 `bin/ordexus-node.before-self-transfer`。停止脚本只等待四个节点 PID，避免因 LAN 转发 PID 尚存而提前退出。

实际验证结果：

- 钱包最后复查全量 392 项测试、`npx tsc --noEmit` 通过；Android `assembleRelease` 与实际安装包校验通过；节点的共识、账本、运行时、资产和质押测试通过。
- 使用创世钱包在本机内存中签名，仅调用手机节点模拟：委托通过；向非自己所有的验证者自质押返回 `stake: staker mismatch`；用户现有 MYT 合约自转账通过，资产余额保持不变。原始密钥没有复制到手机节点，也未广播创世钱包测试交易。
- 使用两名临时验证者测试账户，通过钱包提交函数实际广播自质押和委托各 `10,000,000` atomic（`0.01 ORDU`），分别在高度 3037、3040 最终确认，无执行错误。验证者增加 `0.01 ORDU` 待激活自质押和 `0.01 ORDU` 委托；按协议在下一 epoch 激活。
- APK 已通过无线 ADB 覆盖安装，后续布局修复的最后安装时间为 `2026-10-10 00:17:55`；手机安装包与当前 release APK 的 SHA-256 均为 `2c1106a14d17fde70f4e770f0d5804c16d4b39d31a4c9af58db86ec4169b22de`。包名 `network.ordexus.wallet`，仍为版本 `1.0.2` / code `3`，本次没有发布 Expo OTA。
- 复查发现电脑旧测试链的四个进程重新运行，已再次停止；计划任务 `Solana-FRP-Wallet-RPC` 保持 Disabled，电脑节点数量为零。

公开测试证据位于本次忽略目录中的 `self-transfer-upgrade-verification.json`、`pc-stop-recheck.json`，以及钱包 `tmp/selected-chain-live-simulation.json` 和 `tmp/selected-chain-staking-e2e.json`；这些证据只含公钥、签名、结果与状态，不含密钥。

### 大额委托后 epoch 奖励溢出

后续用户的创世钱包大额委托已经进入手机链：`2,499,999,874,860,690,439` atomic 委托给 `HoPnvTctYz4oJUsfv4uzJA1W6BjFGRtAbbyDFkvwD7p9`，激活 epoch 为 12。该笔委托并非本次临时账户的 `0.01 ORDU` 测试交易。原节点在高度 3072 后的 epoch 奖励计算中将有效质押乘投票积分存入 uint64，超过该类型范围后持续报 `consensus: uint64 multiplication overflow`；节点保持连接和质押 quorum，但不能再出块，后续合约调用滞留在 mempool。

`consensus/rewards.go` 将 epoch 奖励权重及其总和改为精确大整数；按原规则以整数比例计算并向下取整，最终奖励仍检查 uint64 范围。佣金与罚没的 basis-point 计算也使用已有精确比例函数，避免合法大额中间乘积溢出。既有可计算输入的结果不变，不修改账户余额、交易格式或已有账本。新增回归测试覆盖实际大额质押、超过 uint64 的权重、分配池上限、输入权重不被修改、epoch 结算和大额罚没；同时补齐资源上限测试的合法 epoch snapshot root，继续检查超限提案在执行前被拒绝。

当时的共识、资产执行、交易运行时和质押四组测试通过。该次手机程序 SHA-256 为 `a661c7c28905ffa6c58c3ba1da0bf051dbecd6a5efe1e8f38ce96b26f8708acb`，升级前程序另存 `bin/ordexus-node.before-large-stake`，仍使用相同四份配置及原数据目录。最终程序以以下恢复修复及本次 manifest 为准。

修复后原链从 3072 恢复增长到 3300 以上，并完整通过 epoch 12 → 13 的结算（检查时高度 3326、slot 3337、最终确认高度 3324，`ok=true`、`chain_progressing=true`、`transaction_submission_enabled=true`）。原创世钱包的大额委托已经全部进入 active bucket，pending 为零；四节点均能查询两笔临时账户测试交易的最终确认记录，用户 MYT 资产一致。公开证据为 `large-stake-next-epoch-verification.json`、`delegation-activation-after-recovery.json`、`final-network-verification.json` 与 `final-device-status.json`。升级时未重新生成链，已确认交易保留；停顿期间未确认且已过期的调用需要钱包重新签名提交。

仓位实测还发现通用 `JSON.parse` 将大额 uint64 质押舍入了 39 atomic。钱包 `chainRpc.js` 在解析响应前保留超出 JavaScript 安全整数范围的整数 token 为十进制字符串，不改 JSON 字符串、普通安全整数、小数或浮点指标；继续拒绝格式错误的 JSON。余额原有 raw-response BigInt 读取保留。钱包真实仓位聚合现在精确返回委托 `2499999874860690439` atomic，RPC 仍为手机 LAN，验证者四名，链健康。测试覆盖 uint64 最大值、负整数、嵌套委托、转义字符串与无效 JSON；公开证据为钱包 `tmp/selected-chain-portfolio-verification.json`。

### 最终确认暂停与恢复证书（2026-10-10）

后续检查发现，验证者处罚曾使冻结集合低于四人阈值，节点进入 `recovery_elder`：临时区块继续增加，但最终确认暂停。这种状态不能仅凭连接数量或链头增长判断可正常提交。`getHealth` 现对 `FinalitySuspended` 返回 `ok=false`、`transaction_submission_enabled=false` 和 `finality_suspended`。

原恢复入口存在两个认证问题：安全委员会承诺来自冻结的认证 epoch，不能改用检查点区块上已受处罚的可变集合；合法 `submitMultiValidatorActivation` 证书安装四人集合后，普通 epoch 认证及证明同步又忽略证书，重新使用旧单长老快照。现在恢复时仍要求原安全委员会严格超过 2/3 权益签名、正确链 ID、序号、账本锚点和目标集合根；已接纳证书按序号保存，兼容并迁移旧版仅存最后一份的证书。重启、下一 epoch 认证和证明同步均连接这些委员会变更，恢复前 slot 仍使用原证明。奖励 QC 按实际 slot 的认证集合验证，不能用恢复后的集合认证旧 slot。

另一处边界错误是：`ValidatorSetFromStateAtEpoch` 无条件排除 `Jailed` 状态，即使非零处罚期限已在目标 epoch 到期，也要等该 epoch 首块迁移账户才能恢复。快照必须在首块前构建，因此形成循环。现按已有 `JailUntilEpoch` 在目标 epoch 投影资格，未到期和无期限的处罚仍排除，查询不修改账户数据。未缩短处罚、放宽 quorum 或修改链上余额。

最终手机二进制 SHA-256 为 `e04fb7f74043bba924ca5ed70b7fdda1636a65da25ebf410b403aa8dcd32d30b`，四节点正常停止并使用原配置、原账本重启。临时四名验证者使用自己的测试共识密钥签署恢复证书，原始创世钱包没有签署广播测试交易，也没有向节点导出密钥。公共证书在本次忽略目录的 `multi-validator-activation-certificates.json`；恢复前程序保留在手机 `bin/ordexus-node.before-activation-proof`。

节点 `go test ./internal/posnode ./consensus ./blockchain ./programs/vm ./runtime ./programs/stake` 通过；新增验证覆盖安全委员会根、恢复后同 epoch 认证、历史 slot、重启、下一 epoch、证明同步、签名不足、QC 权重及处罚到期投影。手机旧质押交易仍在四节点最终确认，原创世钱包大额委托保持 active。

最终稳定性检查中四节点共同通过 epoch 18 → 19：高度均为 4868，最终确认高度均为 4866，共同最终确认 slot 为 4877；区块哈希为 `DknkqPrS7ArzvtupamVeAQFEsvcwrdTiY7M6Y7339onP`，状态根为 `CD3cUwK7tpBth31WYy7PUtDbfFijkqzdMaG1Rzt7wyXf`。全部保持 `multi_validator`、四名验证者，四个健康接口均允许提交。证据为本次忽略目录的 `recovery-next-epoch-verification.json`；这是当时检查结果，后续在线状态应重新查询。

另使用临时验证者账户发行 `C4TEST` 并调用 `transfer` 自转账，分别在高度 4275、4279 最终确认。四节点可查询两笔收据，代币余额保持 `10,000,000` atomic，供给不变。合约为 `6JwUz5TcLE52nfKA6Woz8EF9FUB83er6sq2mB3xzNdD9`；公开证据在钱包 `tmp/selected-chain-contract-e2e.json`。用户 MYT 合约自转账的本地签名模拟也再次通过；自转账本来不增加余额，普通转账及非所有者自质押的权限检查保留。

本次恢复测试面向现有四节点；没有验证全新节点从零同步跨越恢复证书的完整引导流程，也没有验证长时间后台运行。停顿期间未确认且过期的交易需要钱包重新签名，不能将“已广播”当作“已最终确认”。

### Shielded V2 审计授权的实际限制

钱包 `PrivacyOperationScreens.tsx` 的审计授权提交入口目前明确阻止旧版授权；当前 Shielded V2 没有定义旧 `audit_record` 对应的链上审计授权指令和记录格式。这是尚未实现的协议功能，不属于本次 SVM1/OVM1 模板兼容修复。重装 APK 不能启用它，需要另外实现节点授权、有效期、撤销和验证规则以及钱包签名流程，不通过关闭校验开放旧协议。

来源：[Termux 官方 release](https://github.com/termux/termux-app/releases/tag/v0.118.3)、[安卓 Doze 运行限制](https://developer.android.com/training/monitoring-device-state/doze-standby)。

## 2026-10-10 大额委托收益分账修复

用户截图的 `2,499,999,874,860,690,439` atomic units 是约 25 亿 ORDU，累计委托奖励当时仅为 50 atomic units。手机只读查询证实这是真实链上数据。epoch 通胀池和验证者权重已经使用精确整数，但 `distributeStakePayout` 的自质押／委托份额仍先进行 uint64 乘法，溢出后将绝大部分奖励当作余数分给自质押所有者。修复复用精确比例函数，保留佣金、整数向下取整、余数归属、奖励事件和供应总量约束；未调整年化通胀参数。

新增网络升级参数 `stake_reward_exact_ratio_epoch`。新链默认从 0 使用正确分账；现有链在生效 epoch 前保留原算术，保证历史状态根与奖励根可验证。出块、普通验块和历史同步统一读取配置；首次升级已有账本必须约定未来 epoch，该值落库后重启不能更改或遗漏。旧余额不会被这个参数重新分配。

本次节点 `go test ./consensus ./internal/posnode ./blockchain ./programs/stake` 通过；回归覆盖截图中的实际大额委托、自质押为零和非零、佣金、奖励池守恒、实际账户入账、重复结算、历史分账和激活边界。Android ARM64 二进制 SHA-256 为 `962cee5f62471d66f6da48bb1e5d6c3dc58224401c75c893d052377384578a71`。

用户明确要求由 AI 执行后，已在实际 Termux 终端运行 `upgrade-stake-payout.sh`，正常停止并升级四节点。链 ID、创世、身份和数据路径保持原值；四份配置均约定 epoch 32 生效。备份在手机链目录 `backups/stake-payout-20261009T234503Z/`，含 `state.tar`（账本、配置、日志和 PID）及旧节点程序；UTC 目录时间对应北京时间 2026-10-10 07:45:03。当前新节点 PID 为 `15175/15177/15182/15186`，随后在 epoch 31、slot 7985 共同继续出块。脚本原始代码在 `tools/ordexus_termux_stake_payout_upgrade.sh`。最终生效验证应以本次忽略目录 `stake-payout-final-verification.json` 为准，不能将“升级进程启动”直接当作“新分账已结算”。

2026-10-10 07:55:33 实测新分账已经生效：四节点共同最终确认 slot 8199，区块哈希、状态根、交易根、收据根和奖励根一致，均为 `multi_validator` / 四名验证者，健康接口均为 `ok=true`、`chain_progressing=true`、`transaction_submission_enabled=true`。原委托累计奖励从升级后观察起点的 56 atomic 增至 `1,217,662,292,744` atomic；epoch 32 这一轮实际增加 `1,217.662292688 ORDU`。委托本金仍为 `2,499,999,874,860,690,439` atomic，pending 和 unlocking 均为 0。共同状态根为 `BTduFovYFVkGMCwNVR1Kaitst6MzEgNaR58vqxnvYR4W`。

钱包实际查询函数通过手机 LAN RPC 再次核验，余额为 `7,500,000,842,237,919,563` atomic。相对升级前 epoch 30 的余额增量，恰好等于同期累计委托奖励增量（包括 epoch 31 的 4 atomic 和修复后奖励），证明奖励自动进入可用余额。新钱包总资产计算未重复累加该奖励，证据为钱包 `tmp/stake-payout-20261010/readonly-after-upgrade-verification.json`。本电脑未运行链节点。上述为当时检查结果；这次金额不保证后续每个 epoch 相同，历史错分未补偿。

钱包的“领取委托”实际上签署 `InstructionWithdrawDelegation`，只赎回到期的 unlocking 本金；收益已在结算时转入余额，无需领取交易。用户该笔委托的 pending 与 unlocking 均为 0，全部 active。钱包已更正操作文案、签名前的解锁检查、当前 RPC 模拟与提交、赎回记录金额／入账方向，并避免把已到账的累计收益再次加入总资产。用户创世私钥未搬运或使用，历史错分收益尚未补偿。
