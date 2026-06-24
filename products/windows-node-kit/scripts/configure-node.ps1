param(
  [Parameter(Mandatory = $true)]
  [string]$NodeName,

  [Parameter(Mandatory = $true)]
  [string]$AdvertisedIP,

  [int]$ListenPort = 5102,
  [int]$RPCPort = 8899,
  [string]$BootstrapRPCURL = "http://101.35.87.31:8899/",
  [string]$DataPath = "",
  [string]$PeerSeed = "",
  [string]$ConfigPath = "",
  [switch]$Force
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：用户可能从任意目录执行脚本。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Test-ValidPort {
  param([int]$Port)
  # 功能目的：校验端口边界；实现原因：避免写入无效端口导致节点无法启动。
  return $Port -ge 1 -and $Port -le 65535
}

function Test-ValidNodeName {
  param([string]$Value)
  # 功能目的：限制节点名字符；实现原因：节点名会进入配置、日志和注册请求。
  return $Value -match '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
}

function Test-ValidIPAddress {
  param([string]$Value)
  # 功能目的：校验对外广播地址；实现原因：错误 IP 会导致其他节点无法连接。
  $parsed = $null
  return [System.Net.IPAddress]::TryParse($Value, [ref]$parsed)
}

function Write-Utf8JsonFile {
  param(
    [string]$Path,
    [object]$Value
  )
  # 功能目的：写入 UTF-8 JSON；实现原因：配置需要跨 Windows/Linux 稳定解析。
  $json = $Value | ConvertTo-Json -Depth 32
  $encoding = New-Object System.Text.UTF8Encoding($false)
  [System.IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, $encoding)
}

$packageRoot = Resolve-PackageRoot
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
  $ConfigPath = Join-Path $packageRoot "config\join-wallet-scan.json"
}
$ConfigPath = [System.IO.Path]::GetFullPath($ConfigPath)

if (-not (Test-ValidNodeName $NodeName)) {
  throw "NodeName must match [A-Za-z0-9._-] and length 1..64."
}
if (-not (Test-ValidIPAddress $AdvertisedIP)) {
  throw "AdvertisedIP must be a valid IP address."
}
if (-not (Test-ValidPort $ListenPort)) {
  throw "ListenPort must be 1..65535."
}
if (-not (Test-ValidPort $RPCPort)) {
  throw "RPCPort must be 1..65535."
}
if ($ListenPort -eq $RPCPort) {
  throw "ListenPort and RPCPort must be different."
}
if (-not (Test-Path -LiteralPath $ConfigPath)) {
  throw "Config file not found: $ConfigPath"
}

$config = Get-Content -LiteralPath $ConfigPath -Raw -Encoding UTF8 | ConvertFrom-Json
if (($config.validator_enabled -eq $true -or -not [string]::IsNullOrWhiteSpace($config.staker_address)) -and -not $Force) {
  throw "Config is already paired. Backup first, then rerun with -Force if needed."
}

if ([string]::IsNullOrWhiteSpace($DataPath)) {
  $DataPath = "data\$NodeName"
}
if ([string]::IsNullOrWhiteSpace($PeerSeed)) {
  $PeerSeed = "$NodeName-peer"
}

$config.node_name = $NodeName
$config.data_path = $DataPath
$config.peer_seed = $PeerSeed
$config.listen_ip = "0.0.0.0"
$config.listen_port = $ListenPort
$config.network = "tcp"
$config.advertised_ip = $AdvertisedIP
$config.advertised_port = $ListenPort
$config.rpc_enabled = $true
$config.rpc_listen_ip = "0.0.0.0"
$config.rpc_port = $RPCPort
$config.node_role = "full"
$config.node_roles = @("full")
$config.node_capabilities = @("relay", "state_sync", "dht")
$config.validator_enabled = $false
$config.consensus_enabled = $false
$config.auto_register = $false
$config.bootstrap_join.rpc_url = $BootstrapRPCURL
$config.validator_pairing.enabled = $true
$config.validator_pairing.auto_write_config = $true

if ($null -ne $config.validator_pairing.PSObject.Properties["keystore_dir"]) {
  $config.validator_pairing.PSObject.Properties.Remove("keystore_dir")
}

Write-Utf8JsonFile -Path $ConfigPath -Value $config

Write-Host "Node config updated: $ConfigPath"
Write-Host "Next: run .\scripts\start-node.ps1 and scan the wallet QR."
