param(
  [switch]$All
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：默认只停止当前部署包启动的节点。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

$packageRoot = Resolve-PackageRoot
$processes = Get-CimInstance Win32_Process -Filter "Name = 'posnode.exe'"
if (-not $All) {
  $processes = $processes | Where-Object {
    $_.CommandLine -like "*$packageRoot*"
  }
}

if ($null -eq $processes) {
  Write-Host "No posnode.exe process found."
  exit 0
}

foreach ($process in $processes) {
  # 功能目的：停止节点进程；实现原因：后台任务或旧窗口可能仍占用端口。
  Invoke-CimMethod -InputObject $process -MethodName Terminate | Out-Null
  Write-Host "Stopped posnode.exe PID=$($process.ProcessId)"
}
