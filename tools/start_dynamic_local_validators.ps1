[CmdletBinding()]
param(
    [string]$BootstrapHost = "101.35.87.31",
    [int]$BootstrapRpcPort = 8899,
    [int]$ValidatorCount = 6,
    [int]$BaseP2PPort = 5201,
    [int]$BaseRpcPort = 8901,
    [string]$AdvertisedIP = "127.0.0.1",
    [UInt64]$StakeLamports = 10000000,
    [int]$SlotMillis = 2000,
    [string]$OutputRoot = "",
    [switch]$ResetLocalData,
    [switch]$SkipBootstrapCheck
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-RepoRoot {
    # 功能目的：定位仓库根目录；实现原因：脚本可从任意目录稳定构建

    return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Resolve-OutputRoot {
    param([string]$RepoRoot, [string]$Value)
    # 功能目的：解析输出目录；实现原因：隔离临时数据和源码

    if (-not [string]::IsNullOrWhiteSpace($Value)) {
        return [System.IO.Path]::GetFullPath($Value)
    }
    return [System.IO.Path]::GetFullPath((Join-Path $RepoRoot "dist\dynamic-local"))
}

function Assert-PathUnderRoot {
    param([string]$RootPath, [string]$TargetPath)
    # 功能目的：校验删除边界；实现原因：递归清理前防止路径越界

    $root = [System.IO.Path]::GetFullPath($RootPath).TrimEnd('\') + '\'
    $target = [System.IO.Path]::GetFullPath($TargetPath).TrimEnd('\') + '\'
    if ($target.Equals($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputRoot cannot be the dist root"
    }
    if (-not $target.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputRoot must be under $root"
    }
}

function Assert-ClusterInput {
    # 功能目的：校验启动参数；实现原因：提前拒绝非法端口和异常规模

    if ($ValidatorCount -lt 1 -or $ValidatorCount -gt 32) {
        throw "ValidatorCount must be 1..32"
    }
    if ($BootstrapRpcPort -lt 1 -or $BootstrapRpcPort -gt 65535) {
        throw "BootstrapRpcPort is invalid"
    }
    if ($BaseP2PPort -lt 1 -or ($BaseP2PPort + $ValidatorCount - 1) -gt 65535) {
        throw "P2P port range is invalid"
    }
    if ($BaseRpcPort -lt 1 -or ($BaseRpcPort + $ValidatorCount - 1) -gt 65535) {
        throw "RPC port range is invalid"
    }
    if ($StakeLamports -lt 10000000) {
        throw "StakeLamports must be >= 10000000"
    }
    if ($SlotMillis -lt 1000 -or $SlotMillis -gt 10000) {
        throw "SlotMillis must be 1000..10000"
    }
    $parsedAdvertisedIP = [System.Net.IPAddress]$null
    if ([System.Net.IPAddress]::TryParse($AdvertisedIP, [ref]$parsedAdvertisedIP) -eq $false) {
        throw "AdvertisedIP must be a valid IP address"
    }
}

function Test-TcpPort {
    param([string]$HostName, [int]$Port)
    # 功能目的：检测远端端口；实现原因：公网 bootnode 未就绪会导致本地节点等待

    $result = Test-NetConnection -ComputerName $HostName -Port $Port -InformationLevel Quiet
    return [bool]$result
}

function Assert-LocalPortsFree {
    param([int[]]$Ports)
    # 功能目的：检查本机端口占用；实现原因：端口冲突会导致节点启动失败

    $usedPorts = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
        Where-Object { $Ports -contains $_.LocalPort } |
        Select-Object -ExpandProperty LocalPort -Unique
    if (@($usedPorts).Count -gt 0) {
        throw "Local ports already in use: $($usedPorts -join ', ')"
    }
}

function Invoke-GoBuild {
    param([string]$PackagePath, [string]$OutputPath)
    # 功能目的：构建节点二进制；实现原因：启动前固定使用当前源码版本

    & go build -o $OutputPath $PackagePath
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed: $PackagePath"
    }
    if (-not (Test-Path -LiteralPath $OutputPath)) {
        throw "build output not found: $OutputPath"
    }
}

function Invoke-Wallet {
    param([string]$WalletBinary, [string[]]$Arguments)
    # 功能目的：执行钱包命令；实现原因：配对和密钥生成复用项目标准入口

    & $WalletBinary @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "wallet command failed: $($Arguments -join ' ')"
    }
}

function New-ValidatorConfig {
    param(
        [string]$NodeName,
        [string]$DataPath,
        [string]$PeerKeyPath,
        [int]$P2PPort,
        [int]$RpcPort,
        [string]$BootstrapRpcUrl,
        [string]$AdvertiseIP,
        [int]$SlotMillisValue
    )
    # 功能目的：生成加入节点配置；实现原因：每个验证者必须独立身份和端口

    return [ordered]@{
        node_mode = "posnode"
        environment = "stage"
        production = $false
        node_name = $NodeName
        data_path = $DataPath
        listen_ip = "0.0.0.0"
        listen_port = $P2PPort
        network = "tcp"
        advertised_ip = $AdvertiseIP
        advertised_port = $P2PPort
        rpc_enabled = $true
        rpc_listen_ip = "0.0.0.0"
        rpc_port = $RpcPort
        allow_insecure_p2p = $false
        node_role = "full"
        node_roles = @("full")
        node_capabilities = @("relay", "state_sync", "dht")
        validator_enabled = $false
        consensus_enabled = $false
        peer_key_path = $PeerKeyPath
        slot_millis = $SlotMillisValue
        epoch_slots = 600
        finality_depth = 2
        turbine_fanout = 2
        auto_register = $false
        mempool_max_transactions = 5000
        mempool_transaction_ttl_millis = 180000
        transaction_leader_forward_slots = 4
        transaction_forward_validators = $true
        bootstrap_join = [ordered]@{
            rpc_url = $BootstrapRpcUrl
            poll_interval_millis = 2000
            timeout_millis = 0
        }
        validator_pairing = [ordered]@{
            enabled = $true
            token_ttl_millis = 86400000
            auto_write_config = $true
        }
        genesis = [ordered]@{
            initial_supply_lamports = 100000000000000000
            funded_accounts = @()
            initial_validators = @()
        }
    }
}

function Write-JsonFile {
    param([string]$Path, [object]$Value)
    # 功能目的：写入 UTF-8 JSON；实现原因：配置文件必须保持跨平台可读

    $parent = Split-Path -Path $Path -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    Write-Utf8NoBomFile -Path $Path -Text ($Value | ConvertTo-Json -Depth 20)
}

function Write-Utf8NoBomFile {
    param([string]$Path, [string]$Text)
    # 功能目的：写入无 BOM 文本；实现原因：Go JSON 和 payload 前缀不接受 BOM

    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function Wait-PairingPayload {
    param([string]$LogPath, [int]$TimeoutSeconds)
    # 功能目的：从节点日志提取配对载荷；实现原因：RPC 状态接口不会泄露一次性 token

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-Path -LiteralPath $LogPath) {
            $content = Get-Content -Path $LogPath -Raw -ErrorAction SilentlyContinue
            if ([string]::IsNullOrEmpty($content)) {
                Start-Sleep -Milliseconds 500
                continue
            }
            $match = [regex]::Match($content, "posvalpair:[A-Za-z0-9_-]+")
            if ($match.Success) {
                return $match.Value
            }
        }
        Start-Sleep -Milliseconds 500
    }
    throw "pairing payload not found in $LogPath"
}

function Stop-NodeProcess {
    param([System.Diagnostics.Process]$Process)
    # 功能目的：停止临时配对进程；实现原因：配置回写后必须重启

    if ($null -eq $Process -or $Process.HasExited) {
        return
    }
    Stop-Process -Id $Process.Id -Force
    $Process.WaitForExit(5000) | Out-Null
}

Assert-ClusterInput
$repoRoot = Resolve-RepoRoot
$outputRootPath = Resolve-OutputRoot -RepoRoot $repoRoot -Value $OutputRoot
$distRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot "dist"))
Assert-PathUnderRoot -RootPath $distRoot -TargetPath $outputRootPath

if ((Test-Path -LiteralPath $outputRootPath) -and $ResetLocalData) {
    Remove-Item -LiteralPath $outputRootPath -Recurse -Force
}
if ((Test-Path -LiteralPath $outputRootPath) -and -not $ResetLocalData) {
    throw "OutputRoot already exists. Use -ResetLocalData to recreate it: $outputRootPath"
}

$binDir = Join-Path $outputRootPath "bin"
$configDir = Join-Path $outputRootPath "config"
$dataDir = Join-Path $outputRootPath "data"
$keyDir = Join-Path $outputRootPath "keys"
$logDir = Join-Path $outputRootPath "logs"
$payloadDir = Join-Path $outputRootPath "payloads"
$runDir = Join-Path $outputRootPath "run"
New-Item -ItemType Directory -Path $binDir, $configDir, $dataDir, $keyDir, $logDir, $payloadDir, $runDir -Force | Out-Null

$posNodeBinary = Join-Path $binDir "posnode.exe"
$walletBinary = Join-Path $binDir "wallet.exe"
Push-Location $repoRoot
try {
    Invoke-GoBuild -PackagePath ".\cmd\posnode" -OutputPath $posNodeBinary
    Invoke-GoBuild -PackagePath ".\cmd\wallet" -OutputPath $walletBinary
}
finally {
    Pop-Location
}

$p2pPorts = 0..($ValidatorCount - 1) | ForEach-Object { $BaseP2PPort + $_ }
$rpcPorts = 0..($ValidatorCount - 1) | ForEach-Object { $BaseRpcPort + $_ }
Assert-LocalPortsFree -Ports @($p2pPorts + $rpcPorts)

if (-not $SkipBootstrapCheck) {
    if (-not (Test-TcpPort -HostName $BootstrapHost -Port $BootstrapRpcPort)) {
        throw "Bootstrap RPC is unreachable: $BootstrapHost`:$BootstrapRpcPort"
    }
}

$bootstrapRpcUrl = "http://$BootstrapHost`:$BootstrapRpcPort/"
$startedNodes = New-Object System.Collections.Generic.List[object]

for ($index = 1; $index -le $ValidatorCount; $index++) {
    $nodeName = "local-validator-$index"
    $nodeKeyDir = Join-Path $keyDir $nodeName
    $nodeDataDir = Join-Path $dataDir $nodeName
    $peerKeyPath = Join-Path $nodeKeyDir "peer.json"
    $stakerKeyPath = Join-Path $nodeKeyDir "staker.json"
    $configPath = Join-Path $configDir "$nodeName.json"
    New-Item -ItemType Directory -Path $nodeKeyDir, $nodeDataDir -Force | Out-Null

    Invoke-Wallet -WalletBinary $walletBinary -Arguments @("new-key", "-out", $peerKeyPath)
    Invoke-Wallet -WalletBinary $walletBinary -Arguments @("new-key", "-out", $stakerKeyPath)

    $config = New-ValidatorConfig `
        -NodeName $nodeName `
        -DataPath $nodeDataDir `
        -PeerKeyPath $peerKeyPath `
        -P2PPort ($BaseP2PPort + $index - 1) `
        -RpcPort ($BaseRpcPort + $index - 1) `
        -BootstrapRpcUrl $bootstrapRpcUrl `
        -AdvertiseIP $AdvertisedIP `
        -SlotMillisValue $SlotMillis
    Write-JsonFile -Path $configPath -Value $config

    $pairingStdout = Join-Path $logDir "$nodeName.pairing.out.log"
    $pairingStderr = Join-Path $logDir "$nodeName.pairing.err.log"
    $pairingProcess = Start-Process -FilePath $posNodeBinary `
        -ArgumentList @("-config", $configPath) `
        -RedirectStandardOutput $pairingStdout `
        -RedirectStandardError $pairingStderr `
        -PassThru `
        -WindowStyle Hidden
    try {
        $payload = Wait-PairingPayload -LogPath $pairingStdout -TimeoutSeconds 30
        $payloadPath = Join-Path $payloadDir "$nodeName.txt"
        Write-Utf8NoBomFile -Path $payloadPath -Text $payload
        Invoke-Wallet -WalletBinary $walletBinary -Arguments @(
            "validator-pair",
            "-payload-file", $payloadPath,
            "-staker-key", $stakerKeyPath,
            "-lamports", ([string]$StakeLamports)
        )
    }
    finally {
        Stop-NodeProcess -Process $pairingProcess
    }

    $nodeStdout = Join-Path $logDir "$nodeName.out.log"
    $nodeStderr = Join-Path $logDir "$nodeName.err.log"
    $nodeProcess = Start-Process -FilePath $posNodeBinary `
        -ArgumentList @("-config", $configPath) `
        -RedirectStandardOutput $nodeStdout `
        -RedirectStandardError $nodeStderr `
        -PassThru `
        -WindowStyle Hidden

    $startedNodes.Add([pscustomobject]@{
        NodeName = $nodeName
        ProcessId = $nodeProcess.Id
        ConfigPath = $configPath
        DataPath = $nodeDataDir
        P2PPort = $BaseP2PPort + $index - 1
        RpcPort = $BaseRpcPort + $index - 1
        StdoutLog = $nodeStdout
        StderrLog = $nodeStderr
    }) | Out-Null
}

$statusPath = Join-Path $runDir "cluster-status.json"
$summary = [pscustomobject]@{
    StartedAt = (Get-Date).ToString("o")
    BootstrapRpcUrl = $bootstrapRpcUrl
    AdvertisedIP = $AdvertisedIP
    ValidatorCount = $ValidatorCount
    StakeLamports = $StakeLamports
    SlotMillis = $SlotMillis
    Nodes = $startedNodes.ToArray()
}
Write-Utf8NoBomFile -Path $statusPath -Text ($summary | ConvertTo-Json -Depth 20)

Write-Host "Started $ValidatorCount local validators."
Write-Host "SlotMillis: $SlotMillis"
Write-Host "Status: $statusPath"
Write-Host "Logs: $logDir"
exit 0
