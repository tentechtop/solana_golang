param(
    [string]$PackagePattern = "",
    [string]$PosNodePackage = "",
    [switch]$RequireRace,
    [switch]$SkipRace,
    [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Show-Usage {
    Write-Host "Usage:"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\run_posnode_gate.ps1 [-SkipRace]"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\run_posnode_gate.ps1 -RequireRace"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\run_posnode_gate.ps1 -PackagePattern .\... -PosNodePackage .\cmd\posnode"
}

function Stop-WithUsageError {
    param([string]$Message)
    [Console]::Error.WriteLine($Message)
    exit 2
}

function Test-WindowsPlatform {
    return [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)
}

function Resolve-GoPackagePattern {
    param([string]$Value)
    if ($Value.Trim().Length -gt 0) {
        return $Value
    }
    if (Test-WindowsPlatform) {
        return ".\..."
    }
    return "./..."
}

function Resolve-PosNodePackage {
    param([string]$Value)
    if ($Value.Trim().Length -gt 0) {
        return $Value
    }
    if (Test-WindowsPlatform) {
        return ".\cmd\posnode"
    }
    return "./cmd/posnode"
}

function Invoke-GoStep {
    param(
        [string]$Name,
        [string[]]$Arguments
    )
    Write-Host "==> $Name"
    & go @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
}

function Remove-PosNodeBuildArtifact {
    $artifactNames = @("posnode.exe", "posnode")
    foreach ($artifactName in $artifactNames) {
        $artifact = Join-Path (Get-Location) $artifactName
        if (Test-Path -LiteralPath $artifact) {
            Remove-Item -LiteralPath $artifact
        }
    }
}

if ($Help) {
    Show-Usage
    exit 0
}

if ($SkipRace -and $RequireRace) {
    Stop-WithUsageError "-SkipRace and -RequireRace cannot be used together"
}

$resolvedPackagePattern = Resolve-GoPackagePattern $PackagePattern
$resolvedPosNodePackage = Resolve-PosNodePackage $PosNodePackage

try {
    Invoke-GoStep "go test $resolvedPackagePattern" @("test", $resolvedPackagePattern)
    Invoke-GoStep "go vet $resolvedPackagePattern" @("vet", $resolvedPackagePattern)
    Invoke-GoStep "go build $resolvedPosNodePackage" @("build", $resolvedPosNodePackage)
    Remove-PosNodeBuildArtifact

    if ($SkipRace) {
        Write-Warning "race gate skipped by -SkipRace. Production release must run go test -race on Linux or macOS."
        exit 0
    }

    if (Test-WindowsPlatform) {
        $message = "Windows ThreadSanitizer is not a reliable production gate here. Run: go test -race $resolvedPackagePattern on Linux or macOS."
        if ($RequireRace) {
            Stop-WithUsageError $message
        }
        Write-Warning $message
        exit 0
    }

    Invoke-GoStep "go test -race $resolvedPackagePattern" @("test", "-race", $resolvedPackagePattern)
}
finally {
    Remove-PosNodeBuildArtifact
}
