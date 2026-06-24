param(
  [string]$ConfigPath = ""
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：启动脚本需要找到 bin 和 config。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

$packageRoot = Resolve-PackageRoot
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
  $ConfigPath = Join-Path $packageRoot "config\join-wallet-scan.json"
}
$ConfigPath = [System.IO.Path]::GetFullPath($ConfigPath)
$binaryPath = Join-Path $packageRoot "bin\posnode.exe"

if (-not (Test-Path -LiteralPath $binaryPath)) {
  throw "posnode.exe not found: $binaryPath"
}
if (-not (Test-Path -LiteralPath $ConfigPath)) {
  throw "Config file not found: $ConfigPath"
}

Write-Host "Starting node: $binaryPath"
Write-Host "Config file: $ConfigPath"
Write-Host "Keep this window open for the first QR wallet pairing."

& $binaryPath -config $ConfigPath
exit $LASTEXITCODE
