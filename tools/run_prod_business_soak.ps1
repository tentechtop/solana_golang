[CmdletBinding()]
param(
    [int]$RoundCount = 30,
    [int]$RoundMinutes = 5,
    [int]$StabilityMinutes = 20,
    [string]$OutputRoot = ".\report\prod准入长稳输出",
    [switch]$SkipStability
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$utf8 = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8

function Stop-WithUsageError {
    param([string]$Message)
    [Console]::Error.WriteLine($Message)
    exit 2
}

function Assert-PositiveInput {
    if ($RoundCount -lt 1) {
        Stop-WithUsageError "RoundCount must be greater than or equal to 1"
    }
    if ($RoundMinutes -lt 1) {
        Stop-WithUsageError "RoundMinutes must be greater than or equal to 1"
    }
    if ($StabilityMinutes -lt 1) {
        Stop-WithUsageError "StabilityMinutes must be greater than or equal to 1"
    }
}

function Resolve-OutputDirectory {
    param([string]$Path)
    $resolvedPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    New-Item -ItemType Directory -Force -Path $resolvedPath | Out-Null
    return $resolvedPath
}

function ConvertTo-JsonText {
    param([object]$Value)
    return ($Value | ConvertTo-Json -Depth 16)
}

function Write-Utf8File {
    param(
        [string]$Path,
        [string]$Content
    )
    [System.IO.File]::WriteAllText($Path, $Content, $utf8)
}

function Append-Utf8File {
    param(
        [string]$Path,
        [string]$Content
    )
    [System.IO.File]::AppendAllText($Path, $Content, $utf8)
}

function Remove-BuildArtifact {
    foreach ($artifactName in @("posnode.exe", "posnode")) {
        $artifactPath = Join-Path (Get-Location) $artifactName
        if (Test-Path -LiteralPath $artifactPath) {
            Remove-Item -LiteralPath $artifactPath
        }
    }
}

function New-GoCommand {
    param(
        [string]$StepName,
        [string[]]$GoArguments
    )
    return [pscustomobject]@{
        Name      = $StepName
        Arguments = $GoArguments
    }
}

function Write-Status {
    param(
        [string]$Phase,
        [int]$Round,
        [int]$Cycle,
        [string]$Step,
        [string]$State,
        [string]$Message
    )
    $status = [pscustomobject]@{
        UpdatedAt = (Get-Date).ToString("o")
        Phase     = $Phase
        Round     = $Round
        Cycle     = $Cycle
        Step      = $Step
        State     = $State
        Message   = $Message
    }
    Write-Utf8File -Path $script:statusPath -Content (ConvertTo-JsonText $status)
}

function Invoke-GoCommand {
    param(
        [object]$Command,
        [string]$LogPath,
        [string]$Phase,
        [int]$Round,
        [int]$Cycle
    )
    Write-Status -Phase $Phase -Round $Round -Cycle $Cycle -Step $Command.Name -State "running" -Message "start"
    $startedAt = Get-Date
    Append-Utf8File -Path $LogPath -Content "`n## $($Command.Name)`nstarted_at=$($startedAt.ToString("o"))`ncommand=go $($Command.Arguments -join " ")`n"
    $output = & go @($Command.Arguments) 2>&1
    $exitCode = $LASTEXITCODE
    $endedAt = Get-Date
    $durationSeconds = [math]::Round(($endedAt - $startedAt).TotalSeconds, 3)
    Append-Utf8File -Path $LogPath -Content "ended_at=$($endedAt.ToString("o"))`nduration_seconds=$durationSeconds`nexit_code=$exitCode`n--- output ---`n$($output | Out-String)`n"
    if ($Command.Name -like "build*") {
        Remove-BuildArtifact
    }
    $result = [pscustomobject]@{
        Phase           = $Phase
        Round           = $Round
        Cycle           = $Cycle
        Step            = $Command.Name
        Arguments       = $Command.Arguments
        StartedAt       = $startedAt.ToString("o")
        EndedAt         = $endedAt.ToString("o")
        DurationSeconds = $durationSeconds
        ExitCode        = $exitCode
        OK              = ($exitCode -eq 0)
    }
    if ($exitCode -ne 0) {
        Write-Status -Phase $Phase -Round $Round -Cycle $Cycle -Step $Command.Name -State "failed" -Message "exit=$exitCode"
        throw "go $($Command.Arguments -join " ") failed with exit code $exitCode"
    }
    Write-Status -Phase $Phase -Round $Round -Cycle $Cycle -Step $Command.Name -State "passed" -Message "duration=${durationSeconds}s"
    return $result
}

function Invoke-TimeWindow {
    param(
        [string]$Phase,
        [int]$Round,
        [int]$Minutes,
        [object[]]$Commands
    )
    $phaseStartedAt = Get-Date
    $deadline = $phaseStartedAt.AddMinutes($Minutes)
    $logName = if ($Round -gt 0) { "{0:00}_{1}.log" -f $Round, $Phase } else { "stability_$Phase.log" }
    $logPath = Join-Path $script:outputDirectory $logName
    Write-Utf8File -Path $logPath -Content "phase=$Phase`nround=$Round`nminutes=$Minutes`nstarted_at=$($phaseStartedAt.ToString("o"))`n"
    $cycle = 0
    $results = [System.Collections.Generic.List[object]]::new()
    while ((Get-Date) -lt $deadline -or $cycle -eq 0) {
        $cycle++
        foreach ($command in $Commands) {
            $result = Invoke-GoCommand -Command $command -LogPath $logPath -Phase $Phase -Round $Round -Cycle $cycle
            $results.Add($result)
        }
    }
    $phaseEndedAt = Get-Date
    $durationSeconds = [math]::Round(($phaseEndedAt - $phaseStartedAt).TotalSeconds, 3)
    Append-Utf8File -Path $logPath -Content "`nphase_ended_at=$($phaseEndedAt.ToString("o"))`nphase_duration_seconds=$durationSeconds`ncycles=$cycle`n"
    return [pscustomobject]@{
        Phase           = $Phase
        Round           = $Round
        StartedAt       = $phaseStartedAt.ToString("o")
        EndedAt         = $phaseEndedAt.ToString("o")
        DurationSeconds = $durationSeconds
        TargetMinutes   = $Minutes
        Cycles          = $cycle
        StepCount       = $results.Count
        OK              = $true
        LogPath         = $logPath
    }
}

Assert-PositiveInput
$script:outputDirectory = Resolve-OutputDirectory $OutputRoot
$script:statusPath = Join-Path $script:outputDirectory "status.json"
$summaryPath = Join-Path $script:outputDirectory "summary.json"
$businessCommands = [System.Collections.Generic.List[object]]::new()
$businessCommands.Add((New-GoCommand -StepName "consensus business stake vote reward" -GoArguments @("test", ".\consensus", "-run", "TestPoSRealAccountStakeVoteAndBlockFlow|TestValidatorJoinsOnlyAfterRegisterStakeTransaction|TestStageBusiness|TestApplyBlockRewards|TestFaultInjectionLeaderOfflineFormsSkipQC|TestBLS", "-count", "1")))
$businessCommands.Add((New-GoCommand -StepName "posnode rpc turbine sync" -GoArguments @("test", ".\cmd\posnode", "-run", "TestHTTPJSONRPC|TestStageBusinessRPC|TestConsensusStatus|TestTurbine|TestTransactionFastPath|TestFaultInjection|TestAddTransaction|TestVerifyQuorumCertificate", "-count", "1")))
$businessCommands.Add((New-GoCommand -StepName "blockchain reorg persistence" -GoArguments @("test", ".\blockchain", "-run", "TestBuildGenesisStateCreatesTreasuryAndValidators|TestLedgerCommitPersistsAndReloads|TestLedgerReorganizeToBetterFork|TestStageBusinessFinalizedBlocksRejectDeepReorg|TestFaultInjectionReorgPersistsAfterNodeRestart", "-count", "1")))
$businessCommands.Add((New-GoCommand -StepName "p2p fault network routing" -GoArguments @("test", ".\p2p", "-run", "TestFaultInjection|TestHostHeartbeatClosesExpiredConnection|TestHostRequestRetries|TestHostDialPeerBacksOff|TestHostRateLimitsInboundMessages|TestPeerProtection|TestQueuedConnection|TestConnectionMessageSequencer", "-count", "1")))
$businessCommands.Add((New-GoCommand -StepName "privacy audit confidential flow" -GoArguments @("test", ".\programs\privacy", ".\zk", ".\runtime", "-count", "1")))

$stabilityCommands = [System.Collections.Generic.List[object]]::new()
$stabilityCommands.Add((New-GoCommand -StepName "full go test" -GoArguments @("test", ".\...", "-count", "1")))
$stabilityCommands.Add((New-GoCommand -StepName "full go vet" -GoArguments @("vet", ".\...")))
$stabilityCommands.Add((New-GoCommand -StepName "build posnode" -GoArguments @("build", ".\cmd\posnode")))
$runStartedAt = Get-Date
$phaseResults = [System.Collections.Generic.List[object]]::new()
$exitCode = 0
$message = "passed"

try {
    for ($round = 1; $round -le $RoundCount; $round++) {
        $phaseResults.Add((Invoke-TimeWindow -Phase "business" -Round $round -Minutes $RoundMinutes -Commands $businessCommands))
    }
    if (-not $SkipStability) {
        $phaseResults.Add((Invoke-TimeWindow -Phase "stability" -Round 0 -Minutes $StabilityMinutes -Commands $stabilityCommands))
    }
    Write-Status -Phase "complete" -Round $RoundCount -Cycle 0 -Step "all" -State "passed" -Message "completed"
} catch {
    $exitCode = 1
    $message = $_.Exception.Message
    Write-Status -Phase "failed" -Round 0 -Cycle 0 -Step "runner" -State "failed" -Message $message
} finally {
    Remove-BuildArtifact
    $runEndedAt = Get-Date
    $summary = [pscustomobject]@{
        StartedAt       = $runStartedAt.ToString("o")
        EndedAt         = $runEndedAt.ToString("o")
        DurationSeconds = [math]::Round(($runEndedAt - $runStartedAt).TotalSeconds, 3)
        RoundCount      = $RoundCount
        RoundMinutes    = $RoundMinutes
        StabilityMinutes = $StabilityMinutes
        SkipStability   = [bool]$SkipStability
        ExitCode        = $exitCode
        Message         = $message
        Phases          = @($phaseResults)
    }
    Write-Utf8File -Path $summaryPath -Content (ConvertTo-JsonText $summary)
}

exit $exitCode
