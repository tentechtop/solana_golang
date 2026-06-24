param(
  [string]$TaskName = "SolanaGolangPosNode",
  [string]$ConfigPath = "",
  [switch]$RunNow
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：计划任务需要绝对脚本路径。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Test-Administrator {
  # 功能目的：检查管理员权限；实现原因：注册高权限计划任务需要提升权限。
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-Administrator)) {
  throw "Run this script from an elevated Administrator PowerShell."
}

$packageRoot = Resolve-PackageRoot
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
  $ConfigPath = Join-Path $packageRoot "config\join-wallet-scan.json"
}
$ConfigPath = [System.IO.Path]::GetFullPath($ConfigPath)
$startScript = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "start-node.ps1"))

if (-not (Test-Path -LiteralPath $ConfigPath)) {
  throw "Config file not found: $ConfigPath"
}

$argument = "-NoProfile -ExecutionPolicy Bypass -File `"$startScript`" -ConfigPath `"$ConfigPath`""
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $argument -WorkingDirectory $packageRoot
$trigger = New-ScheduledTaskTrigger -AtLogOn
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Highest

# 功能目的：注册开机自启任务；实现原因：Windows 原生没有直接安装服务的轻量入口。
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Description "SolanaGolang validator node" -Force | Out-Null

if ($RunNow) {
  Start-ScheduledTask -TaskName $TaskName
}

Write-Host "Scheduled task installed: $TaskName"
