[CmdletBinding()]
param(
    [string]$ManifestPath = ".\deploy\generated-4\manifest.json",
    [string]$BinaryPath = ".\dist\posnode-windows-amd64.exe",
    [switch]$ResetData,
    [switch]$StopOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$utf8 = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8

function Resolve-RequiredPath {
    param([string]$Path)
    $resolved = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    if (-not (Test-Path -LiteralPath $resolved)) {
        throw "path does not exist: $resolved"
    }
    return $resolved
}

function Stop-PosNodeProcesses {
    $processes = @(Get-CimInstance Win32_Process | Where-Object {
        $name = [string]$_.Name
        $executablePath = [string]$_.ExecutablePath
        $executableName = ""
        if (-not [string]::IsNullOrWhiteSpace($executablePath)) {
            $executableName = [System.IO.Path]::GetFileName($executablePath)
        }
        ($name -like "posnode*") -or ($executableName -like "posnode*")
    })
    foreach ($process in $processes) {
        Write-Host "stop local posnode pid=$($process.ProcessId)"
        Stop-Process -Id $process.ProcessId -Force -ErrorAction Stop
    }
}

function Assert-SafeDataPath {
    param(
        [string]$RepositoryRoot,
        [string]$DataPath
    )
    $resolvedDataPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($DataPath)
    $resolvedDataRoot = Join-Path $RepositoryRoot "data"
    $resolvedDataRoot = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($resolvedDataRoot)
    if (-not $resolvedDataPath.StartsWith($resolvedDataRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "unsafe data path outside repository data directory: $resolvedDataPath"
    }
    return $resolvedDataPath
}

function Start-WindowsNode {
    param(
        [object]$Node,
        [string]$RepositoryRoot,
        [string]$BinaryFullPath,
        [string]$ConfigFullPath
    )
    $logDirectory = Join-Path $RepositoryRoot "deploy\generated-4\logs"
    New-Item -ItemType Directory -Force -Path $logDirectory | Out-Null
    $stdout = Join-Path $logDirectory "$($Node.name).stdout.log"
    $stderr = Join-Path $logDirectory "$($Node.name).stderr.log"
    $process = Start-Process `
        -FilePath $BinaryFullPath `
        -ArgumentList @("-config", $ConfigFullPath) `
        -WorkingDirectory $RepositoryRoot `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -WindowStyle Hidden `
        -PassThru
    Write-Host "start $($Node.name) pid=$($process.Id) rpc=$($Node.rpc_url)"
}

$repositoryRoot = (Get-Location).Path
$manifestFullPath = Resolve-RequiredPath $ManifestPath
$binaryFullPath = Resolve-RequiredPath $BinaryPath
$manifest = Get-Content -Path $manifestFullPath -Raw | ConvertFrom-Json
$windowsNodes = @($manifest.validators | Where-Object { $_.host_group -eq "win" })
if ($windowsNodes.Count -eq 0) {
    throw "manifest has no windows validators"
}

Stop-PosNodeProcesses
if ($StopOnly) {
    Write-Host "windows posnode cluster stopped"
    exit 0
}

foreach ($node in $windowsNodes) {
    $configFullPath = Resolve-RequiredPath $node.config_path
    if ($ResetData) {
        $safeDataPath = Assert-SafeDataPath -RepositoryRoot $repositoryRoot -DataPath $node.data_path
        if (Test-Path -LiteralPath $safeDataPath) {
            Write-Host "reset data $safeDataPath"
            Remove-Item -LiteralPath $safeDataPath -Recurse -Force
        }
    }
    Start-WindowsNode -Node $node -RepositoryRoot $repositoryRoot -BinaryFullPath $binaryFullPath -ConfigFullPath $configFullPath
}

Write-Host "windows posnode cluster started count=$($windowsNodes.Count)"
