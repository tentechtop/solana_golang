param(
    [string]$ManifestPath = ".\deploy\generated-4-public\manifest.json",
    [string]$DeployHost = "101.35.87.31",
    [string]$DeployUser = "root",
    [switch]$GenerateConfigs,
    [switch]$ResetData,
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-ProjectPath {
    param([string]$PathText)
    if ([System.IO.Path]::IsPathRooted($PathText)) {
        return $PathText
    }
    return Join-Path -Path (Get-Location).Path -ChildPath $PathText
}

function Invoke-CheckedCommand {
    param(
        [string]$FilePath,
        [string[]]$Arguments
    )
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "command failed: $FilePath $($Arguments -join ' ')"
    }
}

function Build-LinuxPosNode {
    $distPath = Resolve-ProjectPath -PathText ".\dist"
    New-Item -Path $distPath -ItemType Directory -Force | Out-Null
    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    $oldCgo = $env:CGO_ENABLED
    try {
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        $env:CGO_ENABLED = "0"
        Invoke-CheckedCommand -FilePath "go" -Arguments @("build", "-o", ".\dist\posnode-linux-amd64", ".\cmd\posnode")
    } finally {
        $env:GOOS = $oldGoos
        $env:GOARCH = $oldGoarch
        $env:CGO_ENABLED = $oldCgo
    }
}

function Set-DeployEnvironment {
    param(
        [object]$Validator,
        [bool]$StopAll
    )
    $env:POSNODE_LINUX_DEPLOY_HOST = $DeployHost
    $env:POSNODE_LINUX_DEPLOY_USER = $DeployUser
    $env:POSNODE_LINUX_DEPLOY_CONFIG = $Validator.config_path
    $env:POSNODE_LINUX_REMOTE_CONFIG = $Validator.remote_config
    $env:POSNODE_LINUX_REMOTE_DATA_PATH = $Validator.data_path
    $env:POSNODE_LINUX_SERVICE_NAME = "$($Validator.name).service"
    $env:POSNODE_LINUX_DEPLOY_RESET_DATA = $ResetData.IsPresent.ToString().ToLowerInvariant()
    $env:POSNODE_LINUX_STOP_ALL = $StopAll.ToString().ToLowerInvariant()
    $env:POSNODE_LINUX_HEALTH_RPC_PORT = ([uri]$Validator.rpc_url).Port.ToString()
    $env:POSNODE_LINUX_FIREWALL_PORTS = "$(([uri]$Validator.rpc_url).Port)/tcp,$($Validator.p2p_port)/tcp,$($Validator.p2p_port)/udp"
}

if ($GenerateConfigs -or -not (Test-Path -LiteralPath (Resolve-ProjectPath -PathText $ManifestPath))) {
    & (Resolve-ProjectPath -PathText ".\tools\generate_public_local_chain_configs.ps1")
}

$password = if ([string]::IsNullOrWhiteSpace($env:POSNODE_LINUX_DEPLOY_PASSWORD)) { $env:POSNODE_DEPLOY_PASSWORD } else { $env:POSNODE_LINUX_DEPLOY_PASSWORD }
if ([string]::IsNullOrWhiteSpace($password)) {
    throw "POSNODE_LINUX_DEPLOY_PASSWORD or POSNODE_DEPLOY_PASSWORD is required"
}

if (-not $SkipBuild) {
    Build-LinuxPosNode
}

$manifest = Get-Content -Path (Resolve-ProjectPath -PathText $ManifestPath) -Raw -Encoding UTF8 | ConvertFrom-Json
for ($index = 0; $index -lt $manifest.validators.Count; $index++) {
    $validator = $manifest.validators[$index]
    Set-DeployEnvironment -Validator $validator -StopAll ($index -eq 0)
    Invoke-CheckedCommand -FilePath "go" -Arguments @("run", ".\tools\deploy_posnode_linux_ssh")
}

Write-Host "public validator cluster deploy finished: $DeployHost"
