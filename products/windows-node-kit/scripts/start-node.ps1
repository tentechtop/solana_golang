param(
  [string]$ConfigPath = ""
)

$ErrorActionPreference = "Stop"

function Resolve-PackageRoot {
  # 功能目的：定位产品根目录；实现原因：启动脚本需要找到 bin 和 config。
  return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

function Resolve-PackageRelativePath {
  param(
    [string]$Path,
    [string]$PackageRoot
  )
  # 功能目的：解析配置中的相对路径；实现原因：用户通常从产品根目录运行部署脚本。
  if ([string]::IsNullOrWhiteSpace($Path)) {
    return ""
  }
  if ([System.IO.Path]::IsPathRooted($Path)) {
    return [System.IO.Path]::GetFullPath($Path)
  }
  return [System.IO.Path]::GetFullPath((Join-Path $PackageRoot $Path))
}

function Convert-PathToFileUri {
  param([string]$Path)
  # 功能目的：生成浏览器可打开地址；实现原因：控制台需要直接打印扫码入口。
  if ([string]::IsNullOrWhiteSpace($Path)) {
    return ""
  }
  $fullPath = [System.IO.Path]::GetFullPath($Path)
  $uri = New-Object System.Uri($fullPath)
  return $uri.AbsoluteUri
}

function Get-PairingQRPagePath {
  param(
    [object]$Config,
    [string]$PackageRoot
  )
  # 功能目的：计算节点绑定扫码页面路径；实现原因：浏览器展示比终端字符二维码更稳定。
  if ($null -eq $Config.validator_pairing) {
    return ""
  }
  $keystoreDir = ""
  if ($null -ne $Config.validator_pairing.PSObject.Properties["keystore_dir"]) {
    $keystoreDir = [string]$Config.validator_pairing.keystore_dir
  }
  if (-not [string]::IsNullOrWhiteSpace($keystoreDir)) {
    return Resolve-PackageRelativePath -Path (Join-Path $keystoreDir "pairing-qr.html") -PackageRoot $PackageRoot
  }
  $dataPath = [string]$Config.data_path
  if ([string]::IsNullOrWhiteSpace($dataPath)) {
    return ""
  }
  $relativePath = Join-Path (Join-Path $dataPath "validator-pairing") "pairing-qr.html"
  return Resolve-PackageRelativePath -Path $relativePath -PackageRoot $PackageRoot
}

function Start-PairingQRWatcher {
  param([string]$PagePath)
  # 功能目的：自动打开浏览器扫码页；实现原因：用户无需从终端复制路径。
  if ([string]::IsNullOrWhiteSpace($PagePath)) {
    return
  }
  $startedAt = Get-Date
  try {
    Start-Job -ScriptBlock {
      param([string]$WatchedPagePath, [datetime]$StartedAt)
      $deadline = (Get-Date).AddSeconds(60)
      while ((Get-Date) -le $deadline) {
        $pageItem = Get-Item -LiteralPath $WatchedPagePath -ErrorAction SilentlyContinue
        if ($null -ne $pageItem -and $pageItem.LastWriteTime -ge $StartedAt.AddSeconds(-2)) {
          Start-Process -FilePath $pageItem.FullName
          return
        }
        Start-Sleep -Milliseconds 500
      }
    } -ArgumentList $PagePath, $startedAt | Out-Null
  } catch {
    Write-Host "QR page auto-open watcher failed. Open manually after start: $PagePath"
  }
}

$packageRoot = Resolve-PackageRoot
# 功能目的：固定运行目录；实现原因：相对 data_path 必须落在产品包目录下。
Set-Location $packageRoot
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

$configObject = Get-Content -LiteralPath $ConfigPath -Raw -Encoding UTF8 | ConvertFrom-Json
$pairingEnabled = $false
if ($null -ne $configObject.validator_pairing -and $configObject.validator_pairing.enabled -eq $true) {
  $pairingEnabled = $true
}
if ($pairingEnabled) {
  $qrPagePath = Get-PairingQRPagePath -Config $configObject -PackageRoot $packageRoot
  if (-not [string]::IsNullOrWhiteSpace($qrPagePath)) {
    $qrPageUri = Convert-PathToFileUri -Path $qrPagePath
    Write-Host "扫码地址: $qrPageUri"
    Write-Host "扫码页面文件: $qrPagePath"
    Write-Host "节点生成二维码后会自动在浏览器打开该页面。"
    Start-PairingQRWatcher -PagePath $qrPagePath
  }
}

Write-Host "Starting node: $binaryPath"
Write-Host "Config file: $ConfigPath"
Write-Host "Keep this window open for the first wallet pairing."

& $binaryPath -config $ConfigPath
exit $LASTEXITCODE
