[CmdletBinding()]
param(
    [string]$OutputRoot = "",
    [string]$BootstrapRPC = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$deployerRepo = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
    $OutputRoot = Join-Path $deployerRepo 'dist\ordexus-node-deployer'
}
$deployerOutput = [System.IO.Path]::GetFullPath($OutputRoot)
$deployerBin = Join-Path $deployerOutput 'bin'
$deployerConfig = Join-Path $deployerOutput 'config'
New-Item -ItemType Directory -Path $deployerOutput,$deployerBin,$deployerConfig -Force | Out-Null
$deployerGo = Get-Command go -ErrorAction Stop
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED
Push-Location $deployerRepo
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    foreach ($deployerArch in @('amd64','arm64')) {
        $env:GOARCH = $deployerArch
        Write-Output "Building Linux $deployerArch node..."
        & $deployerGo.Source build -trimpath -ldflags '-s -w' -o (Join-Path $deployerBin "ordexus-node-linux-$deployerArch") './cmd/posnode'
        if ($LASTEXITCODE -ne 0) { throw "Linux $deployerArch node build failed" }
    }
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    Write-Output 'Building Windows deployer...'
    & $deployerGo.Source build -trimpath -ldflags '-s -w' -o (Join-Path $deployerOutput 'OrdexusNodeDeployer.exe') './cmd/node-deployer'
    if ($LASTEXITCODE -ne 0) { throw 'Windows deployer build failed' }
    Copy-Item -LiteralPath (Join-Path $deployerRepo 'config\local\genesis-account.json') -Destination (Join-Path $deployerConfig 'genesis-account.json')
    Copy-Item -LiteralPath (Join-Path $deployerRepo 'products\node-deployer\README.md') -Destination (Join-Path $deployerOutput 'README.md')
    # Preserve a user's configured network when rebuilding the package.
    $deployerNetwork = Join-Path $deployerConfig 'network.json'
    if (-not (Test-Path -LiteralPath $deployerNetwork) -or -not [string]::IsNullOrWhiteSpace($BootstrapRPC)) {
        $networkJson = @{ bootstrap_rpc=$BootstrapRPC } | ConvertTo-Json
        [System.IO.File]::WriteAllText($deployerNetwork,$networkJson,[System.Text.UTF8Encoding]::new($false))
    }
    Get-ChildItem -LiteralPath $deployerOutput -File -Recurse | Where-Object { $_.Name -ne 'SHA256SUMS.txt' } | ForEach-Object {
        $deployerHash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
        "$($deployerHash.Hash.ToLowerInvariant())  $($_.FullName.Substring($deployerOutput.Length+1).Replace('\','/'))"
    } | Set-Content -LiteralPath (Join-Path $deployerOutput 'SHA256SUMS.txt') -Encoding utf8
    Write-Output "Deployment package: $deployerOutput"
} finally {
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
    $env:CGO_ENABLED = $previousCgo
    Pop-Location
}
