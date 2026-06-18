[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [switch]$SkipLinux,
    [switch]$SkipMac,
    [switch]$SkipLocal,
    [switch]$ResetData,
    [switch]$SkipHealth,
    [switch]$HealthOnly,
    [int]$HealthWaitSeconds = 6,
    [string]$LinuxHost = "101.35.87.31",
    [string]$LinuxUser = "root",
    [string]$MacHost = "192.168.120.223",
    [string]$MacUser = "mac",
    [string]$LocalConfig = "deploy\posnode-local-win.json"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
$repositoryRoot = Split-Path -Parent $scriptDirectory
$distDirectory = Join-Path $repositoryRoot "dist"
$localBinaryPath = Join-Path $distDirectory "posnode-windows-amd64.exe"
$linuxRpcNodeBinaryPath = Join-Path $distDirectory "rpcnode-linux-amd64"
$localStdoutPath = Join-Path $repositoryRoot "deploy\posnode-local-win.stdout.log"
$localStderrPath = Join-Path $repositoryRoot "deploy\posnode-local-win.stderr.log"

function Enter-RepositoryRoot {
    Set-Location -LiteralPath $repositoryRoot
}

function Assert-RequiredEnvironment {
    param([string]$Name)

    $value = [Environment]::GetEnvironmentVariable($Name, "Process")
    if ([string]::IsNullOrWhiteSpace($value)) {
        throw "missing required environment variable: $Name"
    }
}

function Invoke-CheckedCommand {
    param(
        [string]$StepName,
        [string]$Executable,
        [string[]]$Arguments,
        [hashtable]$Environment = @{}
    )

    Write-Host "==> $StepName"
    $startedAt = Get-Date
    $previousEnvironment = @{}

    foreach ($environmentName in $Environment.Keys) {
        $previousEnvironment[$environmentName] = [Environment]::GetEnvironmentVariable($environmentName, "Process")
        [Environment]::SetEnvironmentVariable($environmentName, [string]$Environment[$environmentName], "Process")
    }

    try {
        & $Executable @Arguments
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            throw "$StepName failed with exit code $exitCode"
        }
    }
    finally {
        foreach ($environmentName in $Environment.Keys) {
            [Environment]::SetEnvironmentVariable($environmentName, $previousEnvironment[$environmentName], "Process")
        }
    }

    $elapsedMilliseconds = [int64]((Get-Date) - $startedAt).TotalMilliseconds
    Write-Host "OK: $StepName ${elapsedMilliseconds}ms"
}

function Build-PosNodeBinary {
    param(
        [string]$TargetOS,
        [string]$TargetArch,
        [string]$OutputPath
    )

    $buildEnvironment = @{
        GOOS = $TargetOS
        GOARCH = $TargetArch
        CGO_ENABLED = "0"
    }
    Invoke-CheckedCommand "build posnode $TargetOS/$TargetArch" "go" @("build", "-o", $OutputPath, ".\cmd\posnode") $buildEnvironment
}

function Build-RpcNodeBinary {
    param(
        [string]$TargetOS,
        [string]$TargetArch,
        [string]$OutputPath
    )

    $buildEnvironment = @{
        GOOS = $TargetOS
        GOARCH = $TargetArch
        CGO_ENABLED = "0"
    }
    Invoke-CheckedCommand "build rpcnode $TargetOS/$TargetArch" "go" @("build", "-o", $OutputPath, ".\cmd\rpcnode") $buildEnvironment
}

function Build-AllBinaries {
    if (-not (Test-Path -LiteralPath $distDirectory)) {
        New-Item -ItemType Directory -Path $distDirectory -Force | Out-Null
    }

    Build-PosNodeBinary "linux" "amd64" (Join-Path $distDirectory "posnode-linux-amd64")
    Build-RpcNodeBinary "linux" "amd64" $linuxRpcNodeBinaryPath
    Build-PosNodeBinary "darwin" "arm64" (Join-Path $distDirectory "posnode-darwin-arm64")
    Build-PosNodeBinary "windows" "amd64" $localBinaryPath
}

function Invoke-LinuxDeploy {
    Assert-RequiredEnvironment "POSNODE_DEPLOY_PASSWORD"

    $deployEnvironment = @{
        RPCNODE_DEPLOY_HOST = $LinuxHost
        RPCNODE_DEPLOY_USER = $LinuxUser
        RPCNODE_DEPLOY_CONFIG = "deploy/rpcnode-101.json"
        RPCNODE_REMOTE_CONFIG = "/opt/solana_golang/config/rpcnode-101.json"
        RPCNODE_SERVICE_NAME = "rpcnode.service"
        RPCNODE_DEPLOY_PASSWORD = [Environment]::GetEnvironmentVariable("POSNODE_DEPLOY_PASSWORD", "Process")
    }
    Invoke-CheckedCommand "deploy linux rpcnode $LinuxHost" "go" @("run", ".\tools\deploy_rpcnode_ssh") $deployEnvironment
}

function Invoke-MacDeploy {
    Assert-RequiredEnvironment "POSNODE_MAC_DEPLOY_PASSWORD"

    $deployEnvironment = @{
        POSNODE_MAC_DEPLOY_HOST = $MacHost
        POSNODE_MAC_DEPLOY_USER = $MacUser
        POSNODE_MAC_DEPLOY_CONFIG = "deploy/posnode-223-mac.json"
        POSNODE_MAC_REMOTE_CONFIG = "/Users/mac/solana_golang/config/posnode-223.json"
        POSNODE_MAC_REMOTE_DATA_PATH = "/Users/mac/solana_golang/data/posnode-223-stage"
        POSNODE_MAC_DEPLOY_RESET_DATA = [string]([bool]$ResetData).ToString().ToLowerInvariant()
    }
    Invoke-CheckedCommand "deploy mac posnode $MacHost" "go" @("run", ".\tools\deploy_posnode_macos_ssh") $deployEnvironment
}

function Get-LocalPosNodeProcess {
    $localBinaryFullPath = [System.IO.Path]::GetFullPath($localBinaryPath)
    Get-CimInstance Win32_Process | Where-Object {
        $commandLine = [string]$_.CommandLine
        $executablePath = [string]$_.ExecutablePath
        ($commandLine -like "*posnode-local-win.json*") -or ($executablePath -ieq $localBinaryFullPath)
    }
}

function Stop-LocalPosNode {
    $processes = @(Get-LocalPosNodeProcess)
    foreach ($process in $processes) {
        Write-Host "stop local posnode pid=$($process.ProcessId)"
        Stop-Process -Id $process.ProcessId -Force -ErrorAction Stop
    }
}

function Start-LocalPosNode {
    if (-not (Test-Path -LiteralPath $localBinaryPath)) {
        throw "local binary not found: $localBinaryPath"
    }

    $localConfigPath = Join-Path $repositoryRoot $LocalConfig
    if (-not (Test-Path -LiteralPath $localConfigPath)) {
        throw "local config not found: $localConfigPath"
    }

    Stop-LocalPosNode

    $previousLogFormat = [Environment]::GetEnvironmentVariable("SG_LOG_FORMAT", "Process")
    [Environment]::SetEnvironmentVariable("SG_LOG_FORMAT", "json", "Process")
    try {
        $process = Start-Process `
            -FilePath $localBinaryPath `
            -ArgumentList @("-config", $LocalConfig) `
            -WorkingDirectory $repositoryRoot `
            -RedirectStandardOutput $localStdoutPath `
            -RedirectStandardError $localStderrPath `
            -WindowStyle Hidden `
            -PassThru
        Write-Host "start local posnode pid=$($process.Id)"
    }
    finally {
        [Environment]::SetEnvironmentVariable("SG_LOG_FORMAT", $previousLogFormat, "Process")
    }
}

function Invoke-JsonRpc {
    param(
        [string]$NodeName,
        [string]$Endpoint,
        [string]$Method
    )

    $body = @{
        jsonrpc = "2.0"
        id = 1
        method = $Method
        params = @()
    } | ConvertTo-Json -Depth 5 -Compress

    $response = Invoke-RestMethod -Uri $Endpoint -Method Post -ContentType "application/json" -Body $body -TimeoutSec 8
    $errorProperty = $response.PSObject.Properties["error"]
    if (($null -ne $errorProperty) -and ($null -ne $errorProperty.Value)) {
        throw "$NodeName $Method failed: $($errorProperty.Value.message)"
    }

    $resultProperty = $response.PSObject.Properties["result"]
    if ($null -eq $resultProperty) {
        throw "$NodeName $Method failed: missing result"
    }
    return $resultProperty.Value
}

function Test-ClusterHealth {
    Start-Sleep -Seconds $HealthWaitSeconds

    $nodes = @()
    if (-not $SkipLinux) {
        $nodes += [pscustomobject]@{ Name = "linux-101"; Endpoint = "http://${LinuxHost}:8899/" }
    }
    if (-not $SkipMac) {
        $nodes += [pscustomobject]@{ Name = "mac-223"; Endpoint = "http://${MacHost}:8899/" }
    }
    if (-not $SkipLocal) {
        $nodes += [pscustomobject]@{ Name = "local-win"; Endpoint = "http://127.0.0.1:8899/" }
    }

    foreach ($node in $nodes) {
        $health = Invoke-JsonRpc $node.Name $node.Endpoint "getHealth"
        if ($node.Name -eq "linux-101") {
            $nodeStatus = Invoke-JsonRpc $node.Name $node.Endpoint "getNodeStatus"
            if ($nodeStatus.validator_enabled -ne $false -or $nodeStatus.consensus_enabled -ne $false) {
                throw "linux-101 must be rpc-only"
            }
            Write-Host ("health {0}: ok={1} height={2} slot={3} finalized={4} role={5} forwarding={6} connected_peers={7}" -f `
                $node.Name, `
                $health.ok, `
                $health.head_height, `
                $health.head_slot, `
                $health.finalized_height, `
                $nodeStatus.node_role, `
                $nodeStatus.rpc_forwarding, `
                $nodeStatus.connected_peer_count)
            continue
        }

        $consensusStatus = Invoke-JsonRpc $node.Name $node.Endpoint "getConsensusStatus"
        Write-Host ("health {0}: ok={1} height={2} slot={3} finalized={4} mempool={5} validators={6} layer={7}" -f `
            $node.Name, `
            $health.ok, `
            $health.head_height, `
            $health.head_slot, `
            $health.finalized_height, `
            $health.mempool_size, `
            $consensusStatus.validator_count, `
            $consensusStatus.local_validator.turbine_layer)
    }
}

# 功能目的：固定部署入口；实现原因：避免每次手工拼接构建、上传、重启、健康检查命令。
Enter-RepositoryRoot

if ($HealthOnly -and $SkipHealth) {
    throw "-HealthOnly and -SkipHealth cannot be used together"
}

$shouldBuild = (-not $SkipBuild) -and (-not $HealthOnly)
$shouldDeployLinux = (-not $SkipLinux) -and (-not $HealthOnly)
$shouldDeployMac = (-not $SkipMac) -and (-not $HealthOnly)
$shouldStartLocal = (-not $SkipLocal) -and (-not $HealthOnly)

if ($shouldBuild) {
    Build-AllBinaries
}

if ($shouldDeployLinux) {
    Invoke-LinuxDeploy
}

if ($shouldDeployMac) {
    Invoke-MacDeploy
}

if ($shouldStartLocal) {
    Start-LocalPosNode
}

if (-not $SkipHealth) {
    Test-ClusterHealth
}

Write-Host "redeploy posnode cluster ok"
