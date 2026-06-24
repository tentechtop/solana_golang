param(
  [string]$ConfigPath = ""
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：状态脚本需要默认配置路径。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Invoke-NodeRPC {
  param(
    [string]$RPCURL,
    [string]$MethodName
  )
  # 功能目的：调用节点 JSON-RPC；实现原因：部署后需要用统一入口检查状态。
  $body = @{
    jsonrpc = "2.0"
    id = 1
    method = $MethodName
    params = @()
  } | ConvertTo-Json -Depth 8
  return Invoke-RestMethod -Uri $RPCURL -Method Post -ContentType "application/json" -Body $body -TimeoutSec 5
}

$packageRoot = Resolve-PackageRoot
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
  $ConfigPath = Join-Path $packageRoot "config\join-wallet-scan.json"
}
$ConfigPath = [System.IO.Path]::GetFullPath($ConfigPath)
if (-not (Test-Path -LiteralPath $ConfigPath)) {
  throw "Config file not found: $ConfigPath"
}

$config = Get-Content -LiteralPath $ConfigPath -Raw -Encoding UTF8 | ConvertFrom-Json
$rpcURL = "http://127.0.0.1:$($config.rpc_port)/"

Write-Host "RPC: $rpcURL"

try {
  $health = Invoke-NodeRPC -RPCURL $rpcURL -MethodName "getHealth"
  Write-Host "getHealth:"
  $health | ConvertTo-Json -Depth 16
} catch {
  Write-Warning "getHealth failed: $($_.Exception.Message)"
}

try {
  $pairing = Invoke-NodeRPC -RPCURL $rpcURL -MethodName "getValidatorPairing"
  Write-Host "getValidatorPairing:"
  $pairing | ConvertTo-Json -Depth 16
} catch {
  Write-Warning "getValidatorPairing failed: $($_.Exception.Message)"
}
