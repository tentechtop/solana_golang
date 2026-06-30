[CmdletBinding()]
param(
    [string]$ChainID = "pos-dynamic-testnet",
    [string]$AdvertisedIP = "101.35.87.31",
    [int]$P2PPort = 5101,
    [int]$RpcPort = 8899,
    [int]$MinValidators = 6,
    [int]$SlotMillis = 2000,
    [string]$OutputRoot = "",
    [switch]$ResetPackage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-RepoRoot {
    # 功能目的：定位仓库根目录；实现原因：脚本可从任意目录执行

    return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Resolve-OutputRoot {
    param([string]$RepoRoot, [string]$Value)
    # 功能目的：解析打包目录；实现原因：产物集中放入 dist 便于拷贝

    if (-not [string]::IsNullOrWhiteSpace($Value)) {
        return [System.IO.Path]::GetFullPath($Value)
    }
    return [System.IO.Path]::GetFullPath((Join-Path $RepoRoot "dist\public-bootstrap-linux"))
}

function Assert-PathUnderRoot {
    param([string]$RootPath, [string]$TargetPath)
    # 功能目的：校验清理边界；实现原因：递归删除前必须确认目录归属

    $root = [System.IO.Path]::GetFullPath($RootPath).TrimEnd('\') + '\'
    $target = [System.IO.Path]::GetFullPath($TargetPath).TrimEnd('\') + '\'
    if ($target.Equals($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputRoot cannot be the dist root"
    }
    if (-not $target.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputRoot must be under $root"
    }
}

function Assert-PackageInput {
    # 功能目的：校验公网配置；实现原因：错误端口或阈值会导致集群无法形成

    if ([string]::IsNullOrWhiteSpace($ChainID) -or $ChainID -match '[\x00-\x20]') {
        throw "ChainID must be non-empty and must not contain whitespace"
    }
    $parsedIP = [System.Net.IPAddress]$null
    if ([System.Net.IPAddress]::TryParse($AdvertisedIP, [ref]$parsedIP) -eq $false) {
        throw "AdvertisedIP must be a valid IP address"
    }
    if ($P2PPort -lt 1 -or $P2PPort -gt 65535) {
        throw "P2PPort is invalid"
    }
    if ($RpcPort -lt 1 -or $RpcPort -gt 65535) {
        throw "RpcPort is invalid"
    }
    if ($MinValidators -lt 1 -or $MinValidators -gt 128) {
        throw "MinValidators must be 1..128"
    }
    if ($SlotMillis -lt 1000 -or $SlotMillis -gt 10000) {
        throw "SlotMillis must be 1000..10000"
    }
}

function Write-JsonFile {
    param([string]$Path, [object]$Value)
    # 功能目的：写入 UTF-8 JSON；实现原因：Linux 节点直接读取该配置

    $parent = Split-Path -Path $Path -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    Write-Utf8NoBomFile -Path $Path -Text ($Value | ConvertTo-Json -Depth 20)
}

function Write-Utf8NoBomFile {
    param([string]$Path, [string]$Text)
    # 功能目的：写入无 BOM 文本；实现原因：Go JSON 和 Linux shell 不接受 BOM 前缀

    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function Invoke-LinuxBuild {
    param([string]$OutputPath)
    # 功能目的：交叉编译 Linux 节点；实现原因：公网主机通常运行 Linux

    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    $oldCgo = $env:CGO_ENABLED
    try {
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        $env:CGO_ENABLED = "0"
        & go build -o $OutputPath ".\cmd\posnode"
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed: .\cmd\posnode"
        }
    }
    finally {
        $env:GOOS = $oldGoos
        $env:GOARCH = $oldGoarch
        $env:CGO_ENABLED = $oldCgo
    }
}

Assert-PackageInput
$repoRoot = Resolve-RepoRoot
$outputRootPath = Resolve-OutputRoot -RepoRoot $repoRoot -Value $OutputRoot
$distRoot = [System.IO.Path]::GetFullPath((Join-Path $repoRoot "dist"))
Assert-PathUnderRoot -RootPath $distRoot -TargetPath $outputRootPath

if ((Test-Path -LiteralPath $outputRootPath) -and $ResetPackage) {
    Remove-Item -LiteralPath $outputRootPath -Recurse -Force
}
if ((Test-Path -LiteralPath $outputRootPath) -and -not $ResetPackage) {
    throw "OutputRoot already exists. Use -ResetPackage to recreate it: $outputRootPath"
}

$binDir = Join-Path $outputRootPath "bin"
$configDir = Join-Path $outputRootPath "config"
$scriptsDir = Join-Path $outputRootPath "scripts"
New-Item -ItemType Directory -Path $binDir, $configDir, $scriptsDir -Force | Out-Null

Push-Location $repoRoot
try {
    Invoke-LinuxBuild -OutputPath (Join-Path $binDir "posnode")
}
finally {
    Pop-Location
}

$config = [ordered]@{
    node_mode = "posnode"
    chain_id = $ChainID
    environment = "stage"
    production = $false
    node_name = "public-bootstrap"
    data_path = "/opt/solana_golang/data/public-bootstrap"
    listen_ip = "0.0.0.0"
    listen_port = $P2PPort
    network = "tcp"
    advertised_ip = $AdvertisedIP
    advertised_port = $P2PPort
    rpc_enabled = $true
    rpc_listen_ip = "0.0.0.0"
    rpc_port = $RpcPort
    allow_insecure_p2p = $false
    node_role = "bootnode"
    node_roles = @("bootnode")
    node_capabilities = @("relay", "dht")
    validator_enabled = $false
    consensus_enabled = $false
    peer_seed = "public-bootstrap-peer"
    slot_millis = $SlotMillis
    epoch_slots = 600
    finality_depth = 2
    turbine_fanout = 2
    auto_register = $false
    mempool_max_transactions = 5000
    mempool_transaction_ttl_millis = 180000
    transaction_leader_forward_slots = 4
    transaction_forward_validators = $true
    bootstrap_coordinator = [ordered]@{
        enabled = $true
        min_validators = $MinValidators
        genesis_start_delay_millis = 30000
        registry_path = "/opt/solana_golang/data/public-bootstrap/bootstrap-registry.json"
    }
    genesis = [ordered]@{
        initial_supply_lamports = 100000000000000000
        funded_accounts = @()
        initial_validators = @()
    }
}
Write-JsonFile -Path (Join-Path $configDir "public-bootstrap.json") -Value $config

$startScript = @'
#!/usr/bin/env sh
set -eu
cd /opt/solana_golang
mkdir -p data/public-bootstrap logs run
chmod 700 data
chmod +x bin/posnode
nohup ./bin/posnode -config ./config/public-bootstrap.json > ./logs/public-bootstrap.out.log 2> ./logs/public-bootstrap.err.log &
echo $! > ./run/public-bootstrap.pid
echo "public bootstrap pid: $(cat ./run/public-bootstrap.pid)"
'@
Write-Utf8NoBomFile -Path (Join-Path $scriptsDir "start-public-bootstrap.sh") -Text $startScript

$readme = @"
Copy this directory to the public host as /opt/solana_golang.

Required inbound TCP ports:
- $P2PPort for P2P
- $RpcPort for JSON-RPC bootstrap registration

Start command on the public host:
  cd /opt/solana_golang
  sh ./scripts/start-public-bootstrap.sh

Health check from local Windows:
  Test-NetConnection -ComputerName $AdvertisedIP -Port $RpcPort
  Test-NetConnection -ComputerName $AdvertisedIP -Port $P2PPort
"@
Write-Utf8NoBomFile -Path (Join-Path $outputRootPath "README.txt") -Text $readme

Write-Host "Public bootstrap package: $outputRootPath"
Write-Host "Config chain_id: $ChainID"
Write-Host "Config min_validators: $MinValidators"
Write-Host "Config slot_millis: $SlotMillis"
