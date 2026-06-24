# Dynamic bootstrap startup

现状：旧配置把公网、Mac、本地验证者直接写进 `genesis.initial_validators`，会导致“所有验证者”里长期残留旧机器身份。

目标：只先启动公网引导节点；其他节点启动后显示钱包配对二维码；钱包扫码授权后重启节点，节点自动向引导节点注册。达到 `min_validators` 后，引导节点冻结创世 manifest，加入节点拉取 manifest 并自动出块。

启动顺序：

1. 公网机器启动 `bootstrap-public.json`。
2. 每台待加入机器复制 `join-wallet-scan.json`，至少修改 `node_name`、`data_path`、`peer_seed`、`listen_port`、`rpc_port`，公网部署还要写正确 `advertised_ip`。
3. 启动加入节点后，用钱包扫码。CLI 回退命令是 `wallet validator-pair -payload <二维码payload> -staker-key <钱包key> -lamports 10000000`。
4. 扫码完成后节点配置会写入 `validator_key_path`、`consensus_key_path`、`bls_key_path`、`staker_address`、`bootstrap_join.staker_signature`。
5. 重启加入节点。节点会自动注册到公网引导节点，达到门限后自动进入出块。

边界：

- 不要再手写 `genesis.initial_validators`。
- 不要复用历史静态验证者身份。
- 同一台机器起多个节点时，`data_path`、P2P/RPC 端口必须不同。
