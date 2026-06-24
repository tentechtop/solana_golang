param(
  [string]$TaskName = "SolanaGolangPosNode"
)

$ErrorActionPreference = "Stop"

# 功能目的：删除开机自启任务；实现原因：用户需要可回滚的产品操作。
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
  Write-Host "Scheduled task removed: $TaskName"
  exit 0
}

Write-Host "Scheduled task not found: $TaskName"
