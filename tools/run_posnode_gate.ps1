[CmdletBinding()]
param(
    [string]$PackagePattern = "",
    [string]$PosNodePackage = "",
    [string]$OutputPath = "",
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
    Write-GateSummary -ExitCode 2 -Message $Message
    exit 2
}

function Write-GateSummary {
    param(
        [int]$ExitCode,
        [string]$Message
    )
    if ($OutputPath.Trim().Length -eq 0) {
        return
    }
    $resolvedPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputPath)
    $parentDirectory = Split-Path -Path $resolvedPath -Parent
    if ($parentDirectory.Trim().Length -gt 0 -and -not (Test-Path -LiteralPath $parentDirectory)) {
        New-Item -ItemType Directory -Path $parentDirectory -Force | Out-Null
    }
    $summary = [pscustomobject]@{
        CheckedAt     = (Get-Date).ToString("o")
        ExitCode      = $ExitCode
        Message       = $Message
        PackagePattern = $resolvedPackagePattern
        PosNodePackage = $resolvedPosNodePackage
        RequireRace   = [bool]$RequireRace
        SkipRace      = [bool]$SkipRace
        Steps         = @($gateSteps)
    }
    $summary | ConvertTo-Json -Depth 20 | Set-Content -Path $resolvedPath -Encoding UTF8
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
    $startedAt = Get-Date
    & go @Arguments
    $exitCode = $LASTEXITCODE
    $gateSteps.Add([pscustomobject]@{
        Name       = $Name
        Arguments  = $Arguments
        ExitCode   = $exitCode
        DurationMs = [int64]((Get-Date) - $startedAt).TotalMilliseconds
        OK         = ($exitCode -eq 0)
    })
    if ($exitCode -ne 0) {
        throw "$Name failed with exit code $exitCode"
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

$resolvedPackagePattern = Resolve-GoPackagePattern $PackagePattern
$resolvedPosNodePackage = Resolve-PosNodePackage $PosNodePackage
$gateSteps = [System.Collections.Generic.List[object]]::new()

if ($Help) {
    Show-Usage
    Write-GateSummary -ExitCode 0 -Message "help"
    exit 0
}

if ($SkipRace -and $RequireRace) {
    Stop-WithUsageError "-SkipRace and -RequireRace cannot be used together"
}

try {
    Invoke-GoStep "go test $resolvedPackagePattern" @("test", $resolvedPackagePattern)
    Invoke-GoStep "go vet $resolvedPackagePattern" @("vet", $resolvedPackagePattern)
    Invoke-GoStep "go build $resolvedPosNodePackage" @("build", $resolvedPosNodePackage)
    Remove-PosNodeBuildArtifact

    if ($SkipRace) {
        $message = "race gate skipped by -SkipRace. Production release must run go test -race on Linux or macOS."
        Write-Warning $message
        Write-GateSummary -ExitCode 0 -Message $message
        exit 0
    }

    if (Test-WindowsPlatform) {
        $message = "Windows ThreadSanitizer is not a reliable production gate here. Run: go test -race $resolvedPackagePattern on Linux or macOS."
        if ($RequireRace) {
            Stop-WithUsageError $message
        }
        Write-Warning $message
        Write-GateSummary -ExitCode 0 -Message $message
        exit 0
    }

    Invoke-GoStep "go test -race $resolvedPackagePattern" @("test", "-race", $resolvedPackagePattern)
    Write-GateSummary -ExitCode 0 -Message "gate passed"
} catch {
    Write-GateSummary -ExitCode 1 -Message $_.Exception.Message
    throw
}
finally {
    Remove-PosNodeBuildArtifact
}
