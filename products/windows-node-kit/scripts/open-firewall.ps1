param(
  [int]$ListenPort = 5102,
  [int]$RPCPort = 8899
)

$ErrorActionPreference = "Stop"

function Test-Administrator {
  # 功能目的：检查管理员权限；实现原因：Windows 防火墙规则写入需要提升权限。
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-ValidPort {
  param([int]$Port)
  # 功能目的：校验端口边界；实现原因：避免创建无效防火墙规则。
  return $Port -ge 1 -and $Port -le 65535
}

function Add-PortRule {
  param(
    [string]$Name,
    [int]$Port,
    [string]$Profile
  )
  # 功能目的：创建幂等入站规则；实现原因：重复执行部署脚本不应产生重复规则。
  Get-NetFirewallRule -DisplayName $Name -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  New-NetFirewallRule -DisplayName $Name -Direction Inbound -Action Allow -Protocol TCP -LocalPort $Port -Profile $Profile | Out-Null
}

if (-not (Test-Administrator)) {
  throw "Run this script from an elevated Administrator PowerShell."
}
if (-not (Test-ValidPort $ListenPort)) {
  throw "ListenPort must be 1..65535."
}
if (-not (Test-ValidPort $RPCPort)) {
  throw "RPCPort must be 1..65535."
}

Add-PortRule -Name "SolanaGolang PosNode P2P $ListenPort" -Port $ListenPort -Profile "Any"
Add-PortRule -Name "SolanaGolang PosNode RPC $RPCPort" -Port $RPCPort -Profile "Private"

Write-Host "Firewall P2P port opened: $ListenPort"
Write-Host "Firewall RPC port opened for Private profile: $RPCPort"
