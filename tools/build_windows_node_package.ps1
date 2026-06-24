param(
  [string]$Version = "",
  [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

function Resolve-RepositoryRoot {
  # 功能目的：定位仓库根目录；实现原因：打包脚本可能从任意当前目录执行。
  $scriptRoot = $PSScriptRoot
  if ([string]::IsNullOrWhiteSpace($scriptRoot) -and $null -ne $MyInvocation.MyCommand.Path) {
    $scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
  }
  if ([string]::IsNullOrWhiteSpace($scriptRoot)) {
    $scriptRoot = (Get-Location).Path
  }
  $rootPath = Join-Path $scriptRoot ".."
  return [System.IO.Path]::GetFullPath($rootPath)
}

function Reset-Directory {
  param([string]$Path)
  # 功能目的：清理旧打包输出；实现原因：避免旧二进制或旧脚本混入新产品包。
  if ([string]::IsNullOrWhiteSpace($Path)) {
    throw "Reset path is empty."
  }
  $fullPath = [System.IO.Path]::GetFullPath($Path)
  if ([string]::IsNullOrWhiteSpace($fullPath)) {
    throw "Resolved reset path is empty."
  }
  $repositoryRoot = (Resolve-RepositoryRoot)
  $distRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot "dist"))
  $distPrefix = $distRoot.TrimEnd('\') + '\'
  if (-not $fullPath.StartsWith($distPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refuse to clean path outside dist: $fullPath"
  }
  if (Test-Path -LiteralPath $fullPath) {
    Remove-Item -LiteralPath $fullPath -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $fullPath | Out-Null
}

function Copy-DirectoryContent {
  param(
    [string]$Source,
    [string]$Target
  )
  # 功能目的：复制产品脚本；实现原因：源码目录和分发目录需要分离。
  New-Item -ItemType Directory -Force -Path $Target | Out-Null
  Get-ChildItem -LiteralPath $Source | Copy-Item -Destination $Target -Recurse -Force
}

if ([string]::IsNullOrWhiteSpace($Version)) {
  $Version = Get-Date -Format "yyyyMMdd-HHmmss"
}

$repositoryRoot = (Resolve-RepositoryRoot)
$productSource = Join-Path $repositoryRoot "products\windows-node-kit"
$packageRoot = Join-Path $repositoryRoot "dist\windows-node-kit"
$zipPath = Join-Path $repositoryRoot ("dist\windows-node-kit-$Version.zip")
$binDir = Join-Path $packageRoot "bin"
$configDir = Join-Path $packageRoot "config"
$scriptsDir = Join-Path $packageRoot "scripts"

Set-Location $repositoryRoot
Reset-Directory -Path $packageRoot
New-Item -ItemType Directory -Force -Path $binDir, $configDir | Out-Null

if (-not $SkipBuild) {
  # 功能目的：构建 Windows 可执行文件；实现原因：用户部署包不能依赖本机 Go 环境。
  $posnodeBinary = Join-Path $binDir "posnode.exe"
  $walletBinary = Join-Path $binDir "wallet.exe"
  & go build -o $posnodeBinary .\cmd\posnode
  if ($LASTEXITCODE -ne 0) {
    throw "Build posnode.exe failed."
  }
  & go build -o $walletBinary .\cmd\wallet
  if ($LASTEXITCODE -ne 0) {
    throw "Build wallet.exe failed."
  }
}

Copy-Item -LiteralPath (Join-Path $repositoryRoot "cmd\posnode\configs\join-wallet-scan.json") -Destination (Join-Path $configDir "join-wallet-scan.json") -Force
Copy-Item -LiteralPath (Join-Path $productSource "README.md") -Destination (Join-Path $packageRoot "README.md") -Force
Copy-DirectoryContent -Source (Join-Path $productSource "scripts") -Target $scriptsDir

$versionText = @(
  "version=$Version"
  "built_at=$(Get-Date -Format o)"
)
$encoding = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText((Join-Path $packageRoot "VERSION.txt"), ($versionText -join [Environment]::NewLine) + [Environment]::NewLine, $encoding)

if (Test-Path -LiteralPath $zipPath) {
  Remove-Item -LiteralPath $zipPath -Force
}
Compress-Archive -Path (Join-Path $packageRoot "*") -DestinationPath $zipPath -Force

Write-Host "Windows node package directory: $packageRoot"
Write-Host "Windows node package zip: $zipPath"
