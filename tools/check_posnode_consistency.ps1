[CmdletBinding()]
param(
    [string[]]$RpcUrls = @(),
    [string]$RpcUrlFile = "",
    [int]$Rounds = 3,
    [int]$IntervalSeconds = 5,
    [int]$TimeoutSeconds = 5,
    [int]$MaxHeightDrift = 2,
    [int]$MinNodeCount = 0,
    [string]$OutputPath = "",
    [switch]$RequireSecureP2P,
    [switch]$Help,
    [switch]$SelfTest,
    [switch]$FailFast
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Show-Usage {
    Write-Host "Usage:"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -RpcUrls http://127.0.0.1:5300,http://127.0.0.1:5301 -Rounds 12 -IntervalSeconds 5"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -RpcUrlFile .\rpc_urls.txt -OutputPath .\report\node-consistency.json"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -RpcUrlFile .\rpc_urls.txt -MinNodeCount 30"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -RpcUrlFile .\rpc_urls.txt -FailFast"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -RpcUrlFile .\rpc_urls.txt -RequireSecureP2P"
    Write-Host "  powershell -ExecutionPolicy Bypass -File .\tools\check_posnode_consistency.ps1 -SelfTest"
}

function Stop-WithUsageError {
    param([string]$Message)
    [Console]::Error.WriteLine($Message)
    exit 2
}

function Split-RpcUrlValue {
    param([string]$Value)
    $items = @()
    foreach ($item in ($Value -split ",")) {
        $trimmed = $item.Trim()
        if ($trimmed.Length -gt 0) {
            $items += $trimmed
        }
    }
    return $items
}

function Normalize-RpcUrl {
    param([string]$Value)
    $trimmed = $Value.Trim()
    $uri = $null
    if (-not [System.Uri]::TryCreate($trimmed, [System.UriKind]::Absolute, [ref]$uri)) {
        Stop-WithUsageError "Invalid RpcUrl: $trimmed"
    }
    if ($uri.Scheme -ne "http" -and $uri.Scheme -ne "https") {
        Stop-WithUsageError "RpcUrl must use http or https: $trimmed"
    }
    return $uri.AbsoluteUri
}

function Remove-InlineComment {
    param([string]$Value)
    $index = $Value.IndexOf("#")
    if ($index -lt 0) {
        return $Value
    }
    return $Value.Substring(0, $index)
}

function Assert-ValidOptions {
    if ($Rounds -lt 1) {
        Stop-WithUsageError "Rounds must be greater than or equal to 1"
    }
    if ($IntervalSeconds -lt 0) {
        Stop-WithUsageError "IntervalSeconds must be greater than or equal to 0"
    }
    if ($TimeoutSeconds -lt 1) {
        Stop-WithUsageError "TimeoutSeconds must be greater than or equal to 1"
    }
    if ($MaxHeightDrift -lt 0) {
        Stop-WithUsageError "MaxHeightDrift must be greater than or equal to 0"
    }
    if ($MinNodeCount -lt 0) {
        Stop-WithUsageError "MinNodeCount must be greater than or equal to 0"
    }
}

function Get-HashText {
    param([string]$Text)
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Text)
        $hash = $sha256.ComputeHash($bytes)
        return ([BitConverter]::ToString($hash)).Replace("-", "").ToLowerInvariant()
    } finally {
        $sha256.Dispose()
    }
}

function ConvertTo-CompactJson {
    param([object]$Value)
    return ($Value | ConvertTo-Json -Depth 64 -Compress)
}

function Get-PropertyValue {
    param(
        [object]$InputObject,
        [string]$Name,
        [object]$Default = $null
    )
    if ($null -eq $InputObject) {
        return $Default
    }
    $property = $InputObject.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) {
        return $Default
    }
    return $property.Value
}

function Get-ArrayValue {
    param([object]$Value)
    if ($null -eq $Value) {
        return ,@()
    }
    if (($Value -is [System.Collections.IEnumerable]) -and ($Value -isnot [string])) {
        return ,@($Value)
    }
    return ,@($Value)
}

function Get-StringProperty {
    param(
        [object]$InputObject,
        [string]$Name,
        [string]$Default = ""
    )
    return [string](Get-PropertyValue -InputObject $InputObject -Name $Name -Default $Default)
}

function Get-UInt64Property {
    param(
        [object]$InputObject,
        [string]$Name,
        [uint64]$Default = 0
    )
    $value = Get-PropertyValue -InputObject $InputObject -Name $Name -Default $null
    if ($null -eq $value) {
        return $Default
    }
    try {
        return [uint64]$value
    } catch {
        return $Default
    }
}

function Get-Int64Property {
    param(
        [object]$InputObject,
        [string]$Name,
        [int64]$Default = 0
    )
    $value = Get-PropertyValue -InputObject $InputObject -Name $Name -Default $null
    if ($null -eq $value) {
        return $Default
    }
    try {
        return [int64]$value
    } catch {
        return $Default
    }
}

function Write-JsonReport {
    param(
        [object]$Value,
        [string]$Path
    )
    if ($Path.Trim().Length -eq 0) {
        return
    }
    $resolvedPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    $parentDirectory = Split-Path -Path $resolvedPath -Parent
    if ($parentDirectory.Trim().Length -gt 0 -and -not (Test-Path -LiteralPath $parentDirectory)) {
        New-Item -ItemType Directory -Path $parentDirectory -Force | Out-Null
    }
    $Value | ConvertTo-Json -Depth 80 | Set-Content -Path $resolvedPath -Encoding UTF8
}

function Invoke-PosNodeRpc {
    param(
        [string]$Url,
        [string]$Method
    )
    $requestBody = @{
        jsonrpc = "2.0"
        id      = [Guid]::NewGuid().ToString()
        method  = $Method
        params  = @()
    } | ConvertTo-Json -Depth 8 -Compress
    $response = Invoke-RestMethod -Uri $Url -Method Post -ContentType "application/json" -Body $requestBody -TimeoutSec $TimeoutSeconds
    $rpcError = Get-PropertyValue -InputObject $response -Name "error" -Default $null
    if ($null -ne $rpcError) {
        $errorMessage = Get-StringProperty -InputObject $rpcError -Name "message" -Default "unknown JSON-RPC error"
        throw "$Method returned JSON-RPC error: $errorMessage"
    }
    return (Get-PropertyValue -InputObject $response -Name "result" -Default $null)
}

function Read-RpcUrls {
    $urls = @()
    foreach ($url in $RpcUrls) {
        foreach ($urlItem in (Split-RpcUrlValue $url)) {
            $urls += Normalize-RpcUrl $urlItem
        }
    }
    if ($RpcUrlFile.Trim().Length -gt 0) {
        if (-not (Test-Path -LiteralPath $RpcUrlFile)) {
            Stop-WithUsageError "RpcUrlFile does not exist: $RpcUrlFile"
        }
        $fileUrls = Get-Content -Path $RpcUrlFile | ForEach-Object { Remove-InlineComment $_ } | Where-Object { $_.Trim().Length -gt 0 }
        foreach ($url in $fileUrls) {
            foreach ($urlItem in (Split-RpcUrlValue $url)) {
                $urls += Normalize-RpcUrl $urlItem
            }
        }
    }
    $uniqueUrls = @($urls | Select-Object -Unique)
    if ($MinNodeCount -gt 0 -and $uniqueUrls.Count -lt $MinNodeCount) {
        Stop-WithUsageError "RpcUrl count $($uniqueUrls.Count) is less than MinNodeCount $MinNodeCount"
    }
    return $uniqueUrls
}

function Get-ValidatorFingerprint {
    param([object]$Consensus)
    if ($null -eq $Consensus) {
        return ""
    }
    $validators = Get-ArrayValue (Get-PropertyValue -InputObject $Consensus -Name "validators" -Default @())
    $items = @()
    foreach ($validator in ($validators | Sort-Object { Get-StringProperty -InputObject $_ -Name "validator_id" })) {
        $items += [ordered]@{
            validator_id     = Get-StringProperty -InputObject $validator -Name "validator_id"
            effective_stake  = Get-UInt64Property -InputObject $validator -Name "effective_stake_lamports"
            weight_bps       = Get-UInt64Property -InputObject $validator -Name "weight_bps"
            active_stake     = Get-UInt64Property -InputObject $validator -Name "active_stake_lamports"
            pending_stake    = Get-UInt64Property -InputObject $validator -Name "pending_stake_lamports"
            activation_epoch = Get-UInt64Property -InputObject $validator -Name "activation_epoch"
            status           = Get-StringProperty -InputObject $validator -Name "status"
        }
    }
    return Get-HashText (ConvertTo-CompactJson $items)
}

function Get-TurbineFingerprint {
    param([object]$Consensus)
    if ($null -eq $Consensus) {
        return ""
    }
    $validators = Get-ArrayValue (Get-PropertyValue -InputObject $Consensus -Name "validators" -Default @())
    $items = @()
    foreach ($validator in ($validators | Sort-Object { Get-StringProperty -InputObject $_ -Name "validator_id" })) {
        $items += [ordered]@{
            validator_id = Get-StringProperty -InputObject $validator -Name "validator_id"
            layer        = Get-Int64Property -InputObject $validator -Name "turbine_layer"
            parent       = Get-StringProperty -InputObject $validator -Name "turbine_parent_validator_id"
            children     = Get-ArrayValue (Get-PropertyValue -InputObject $validator -Name "turbine_child_validator_ids" -Default @())
        }
    }
    return Get-HashText (ConvertTo-CompactJson $items)
}

function Get-FastLeaderFingerprint {
    param([object]$Status)
    $fastPath = Get-PropertyValue -InputObject $Status -Name "transaction_fast_path" -Default $null
    $leaderSlots = Get-ArrayValue (Get-PropertyValue -InputObject $fastPath -Name "leader_slots" -Default @())
    if ($leaderSlots.Count -eq 0) {
        return ""
    }
    $items = @()
    foreach ($leader in ($leaderSlots | Sort-Object { Get-UInt64Property -InputObject $_ -Name "slot" })) {
        $items += [ordered]@{
            slot         = Get-UInt64Property -InputObject $leader -Name "slot"
            validator_id = Get-StringProperty -InputObject $leader -Name "validator_id"
            peer_id      = Get-StringProperty -InputObject $leader -Name "peer_id"
        }
    }
    return Get-HashText (ConvertTo-CompactJson $items)
}

function Assert-TransactionFastPath {
    param(
        [object]$Snapshot,
        [System.Collections.Generic.List[string]]$Errors
    )
    $fastPath = Get-PropertyValue -InputObject $Snapshot.Status -Name "transaction_fast_path" -Default $null
    if ($null -eq $fastPath) {
        $Errors.Add("$($Snapshot.Url) missing transaction_fast_path")
        return
    }
    $preferred = Get-ArrayValue (Get-PropertyValue -InputObject $fastPath -Name "preferred_peer_ids" -Default @())
    $leaderSlots = Get-ArrayValue (Get-PropertyValue -InputObject $fastPath -Name "leader_slots" -Default @())
    foreach ($leader in $leaderSlots) {
        $leaderPeerID = Get-StringProperty -InputObject $leader -Name "peer_id"
        if ($leaderPeerID -eq $Snapshot.PeerID) {
            continue
        }
        if ($preferred -notcontains $leaderPeerID) {
            $Errors.Add("$($Snapshot.Url) fast path misses leader peer $leaderPeerID")
        }
    }
    if ($preferred -contains $Snapshot.PeerID) {
        $Errors.Add("$($Snapshot.Url) fast path contains local peer $($Snapshot.PeerID)")
    }
}

function New-NodeSnapshot {
    param([string]$Url)
    $status = Invoke-PosNodeRpc -Url $Url -Method "getNodeStatus"
    $metrics = Invoke-PosNodeRpc -Url $Url -Method "getMetrics"
    $consensus = Get-PropertyValue -InputObject $status -Name "consensus" -Default $null
    if ($null -eq $consensus) {
        $consensus = Invoke-PosNodeRpc -Url $Url -Method "getConsensusStatus"
    }
    $metricsHeadHeight = Get-UInt64Property -InputObject $metrics -Name "head_height"
    $metricsHeadSlot = Get-UInt64Property -InputObject $metrics -Name "head_slot"
    $metricsHeadHash = Get-StringProperty -InputObject $metrics -Name "block_hash"
    $metricsFinalizedHeight = Get-UInt64Property -InputObject $metrics -Name "finalized_height"
    $metricsFinalizedHash = Get-StringProperty -InputObject $metrics -Name "finalized_hash"
    $metricsFinalityDepth = Get-UInt64Property -InputObject $metrics -Name "finality_depth"
    $useMetricsHead = $metricsHeadHash.Trim().Length -gt 0
    return [pscustomobject]@{
        Url                    = $Url
        NodeName               = Get-StringProperty -InputObject $status -Name "node_name"
        PeerID                 = Get-StringProperty -InputObject $status -Name "peer_id"
        HeadHeight             = if ($useMetricsHead) { $metricsHeadHeight } else { Get-UInt64Property -InputObject $status -Name "head_height" }
        HeadSlot               = if ($useMetricsHead) { $metricsHeadSlot } else { Get-UInt64Property -InputObject $status -Name "head_slot" }
        HeadHash               = if ($useMetricsHead) { $metricsHeadHash } else { Get-StringProperty -InputObject $status -Name "head_hash" }
        FinalizedHeight        = if ($useMetricsHead) { $metricsFinalizedHeight } else { Get-UInt64Property -InputObject $status -Name "finalized_height" }
        FinalizedHash          = if ($useMetricsHead) { $metricsFinalizedHash } else { Get-StringProperty -InputObject $status -Name "finalized_hash" }
        FinalityDepth          = if ($useMetricsHead) { $metricsFinalityDepth } else { Get-UInt64Property -InputObject $status -Name "finality_depth" }
        P2PSecure              = [bool](Get-PropertyValue -InputObject $status -Name "p2p_secure_session" -Default $false)
        P2PInsecure            = [bool](Get-PropertyValue -InputObject $status -Name "p2p_insecure_allowed" -Default $true)
        StateRecovery          = [bool](Get-PropertyValue -InputObject $status -Name "state_recovery_enabled" -Default $false)
        StateRoot              = Get-StringProperty -InputObject $metrics -Name "state_root"
        QCHash                 = Get-StringProperty -InputObject $metrics -Name "qc_hash"
        QCHeight               = Get-UInt64Property -InputObject $metrics -Name "qc_height"
        CurrentLeader          = Get-StringProperty -InputObject $status -Name "current_leader"
        ConsensusSlot          = Get-UInt64Property -InputObject $consensus -Name "slot"
        ConsensusEpoch         = Get-UInt64Property -InputObject $consensus -Name "epoch_id"
        TotalActiveStake       = Get-UInt64Property -InputObject $consensus -Name "total_active_stake_lamports"
        ValidatorFingerprint   = Get-ValidatorFingerprint $consensus
        TurbineFingerprint     = Get-TurbineFingerprint $consensus
        FastLeaderFingerprint  = Get-FastLeaderFingerprint $status
        Status                 = $status
        Metrics                = $metrics
        Consensus              = $consensus
    }
}

function Add-GroupMismatchErrors {
    param(
        [object[]]$Snapshots,
        [string]$Property,
        [string]$Label,
        [System.Collections.Generic.List[string]]$Errors
    )
    $values = @($Snapshots | ForEach-Object { $_.$Property } | Select-Object -Unique)
    if ($values.Count -gt 1) {
        $Errors.Add("$Label mismatch: $($values -join ', ')")
    }
}

function Test-SnapshotRound {
    param(
        [object[]]$Snapshots,
        [int]$Round
    )
    $errors = [System.Collections.Generic.List[string]]::new()
    $warnings = [System.Collections.Generic.List[string]]::new()
    if ($Snapshots.Count -eq 0) {
        $errors.Add("round $Round has no successful node snapshot")
        return [pscustomobject]@{ Errors = $errors; Warnings = $warnings }
    }

    $heights = @($Snapshots | ForEach-Object { [uint64]$_.HeadHeight })
    $maxHeight = ($heights | Measure-Object -Maximum).Maximum
    $minHeight = ($heights | Measure-Object -Minimum).Minimum
    $heightDrift = [uint64]($maxHeight - $minHeight)
    $consensusSlots = @($Snapshots | ForEach-Object { [uint64]$_.ConsensusSlot })
    $maxConsensusSlot = ($consensusSlots | Measure-Object -Maximum).Maximum
    $minConsensusSlot = ($consensusSlots | Measure-Object -Minimum).Minimum
    $consensusSlotDrift = [uint64]($maxConsensusSlot - $minConsensusSlot)
    if ($heightDrift -gt [uint64]$MaxHeightDrift) {
        $allowedHeightDrift = [uint64]($consensusSlotDrift + [uint64]$MaxHeightDrift)
        if ($consensusSlotDrift -eq 0 -or $heightDrift -gt $allowedHeightDrift) {
            $errors.Add("head height drift $heightDrift exceeds $MaxHeightDrift")
        } else {
            $warnings.Add("head height drift $heightDrift is within sampled consensus slot drift $consensusSlotDrift")
        }
    }

    $epochValues = @($Snapshots | ForEach-Object { $_.ConsensusEpoch } | Select-Object -Unique)
    if ($epochValues.Count -gt 1) {
        if ($consensusSlotDrift -eq 0) {
            $errors.Add("epoch mismatch: $($epochValues -join ', ')")
        } else {
            $warnings.Add("nodes sampled across epoch boundary: $($epochValues -join ', ')")
        }
    }
    foreach ($epochGroup in ($Snapshots | Group-Object ConsensusEpoch)) {
        Add-GroupMismatchErrors -Snapshots @($epochGroup.Group) -Property "TotalActiveStake" -Label "total active stake at epoch $($epochGroup.Name)" -Errors $errors
        Add-GroupMismatchErrors -Snapshots @($epochGroup.Group) -Property "ValidatorFingerprint" -Label "validator stake fingerprint at epoch $($epochGroup.Name)" -Errors $errors
    }
    Add-GroupMismatchErrors -Snapshots $Snapshots -Property "FinalityDepth" -Label "finality depth" -Errors $errors
    Add-GroupMismatchErrors -Snapshots $Snapshots -Property "StateRecovery" -Label "state recovery enabled" -Errors $errors

    foreach ($group in ($Snapshots | Group-Object HeadHeight)) {
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "HeadHash" -Label "head hash at height $($group.Name)" -Errors $errors
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "StateRoot" -Label "state root at height $($group.Name)" -Errors $errors
    }

    foreach ($group in ($Snapshots | Where-Object { $_.QCHeight -gt 0 } | Group-Object QCHeight)) {
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "QCHash" -Label "qc hash at qc height $($group.Name)" -Errors $errors
    }

    foreach ($group in ($Snapshots | Group-Object FinalizedHeight)) {
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "FinalizedHash" -Label "finalized hash at height $($group.Name)" -Errors $errors
    }

    foreach ($group in ($Snapshots | Group-Object ConsensusSlot)) {
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "TurbineFingerprint" -Label "turbine route at slot $($group.Name)" -Errors $errors
    }
    if (($Snapshots | Group-Object ConsensusSlot).Count -gt 1) {
        $warnings.Add("nodes sampled at different consensus slots")
    }

    foreach ($group in ($Snapshots | Group-Object { Get-UInt64Property -InputObject (Get-PropertyValue -InputObject $_.Status -Name "transaction_fast_path" -Default $null) -Name "start_slot" })) {
        Add-GroupMismatchErrors -Snapshots @($group.Group) -Property "FastLeaderFingerprint" -Label "fast path leaders at slot $($group.Name)" -Errors $errors
    }

    foreach ($snapshot in $Snapshots) {
        Assert-TransactionFastPath -Snapshot $snapshot -Errors $errors
        if ($RequireSecureP2P -and (-not $snapshot.P2PSecure -or $snapshot.P2PInsecure)) {
            $errors.Add("$($snapshot.Url) p2p secure session is not enforced")
        }
    }
    return [pscustomobject]@{ Errors = $errors; Warnings = $warnings }
}

function Assert-SelfTest {
    param(
        [bool]$Condition,
        [string]$Message
    )
    if (-not $Condition) {
        throw $Message
    }
}

function Invoke-SelfTest {
    $missing = [pscustomobject]@{ node_name = "mock" }
    Assert-SelfTest ((Get-StringProperty -InputObject $missing -Name "peer_id") -eq "") "missing string property should return default"
    Assert-SelfTest ((Get-UInt64Property -InputObject ([pscustomobject]@{ value = "bad" }) -Name "value" -Default 7) -eq 7) "invalid uint64 should return default"
    Assert-SelfTest ((Get-Int64Property -InputObject ([pscustomobject]@{ layer = -1 }) -Name "layer") -eq -1) "signed turbine layer should keep -1"
    Assert-SelfTest (((Split-RpcUrlValue "http://a:1,http://b:2").Count) -eq 2) "comma separated rpc urls should split"
    Assert-SelfTest ((Normalize-RpcUrl " http://127.0.0.1:1 ") -eq "http://127.0.0.1:1/") "rpc url should be normalized"
    Assert-SelfTest ((Remove-InlineComment "http://127.0.0.1:1 # local node").Trim() -eq "http://127.0.0.1:1") "rpc url file inline comment should be stripped"

    $validatorsA = [pscustomobject]@{
        validators = @(
            [pscustomobject]@{ validator_id = "b"; effective_stake_lamports = 2; weight_bps = 2000; active_stake_lamports = 2; pending_stake_lamports = 0; activation_epoch = 1; status = "active" },
            [pscustomobject]@{ validator_id = "a"; effective_stake_lamports = 8; weight_bps = 8000; active_stake_lamports = 8; pending_stake_lamports = 0; activation_epoch = 1; status = "active" }
        )
    }
    $validatorsB = [pscustomobject]@{
        validators = @(
            [pscustomobject]@{ validator_id = "a"; effective_stake_lamports = 8; weight_bps = 8000; active_stake_lamports = 8; pending_stake_lamports = 0; activation_epoch = 1; status = "active" },
            [pscustomobject]@{ validator_id = "b"; effective_stake_lamports = 2; weight_bps = 2000; active_stake_lamports = 2; pending_stake_lamports = 0; activation_epoch = 1; status = "active" }
        )
    }
    Assert-SelfTest ((Get-ValidatorFingerprint $validatorsA) -eq (Get-ValidatorFingerprint $validatorsB)) "validator fingerprint should be stable after sorting"

    $errors = [System.Collections.Generic.List[string]]::new()
    $snapshot = [pscustomobject]@{ Url = "mock://node"; PeerID = "peer-a"; Status = [pscustomobject]@{} }
    Assert-TransactionFastPath -Snapshot $snapshot -Errors $errors
    Assert-SelfTest (($errors.Count -eq 1) -and ($errors[0].Contains("missing transaction_fast_path"))) "missing fast path should be a business error"

    $fastPathStatus = [pscustomobject]@{ transaction_fast_path = [pscustomobject]@{ leader_slots = @(); preferred_peer_ids = @() } }
    $snapshotA = [pscustomobject]@{
        Url = "mock://a"; PeerID = "peer-a"; HeadHeight = 10; HeadHash = "h10"; FinalizedHeight = 8; FinalizedHash = "f8"
        FinalityDepth = 2; StateRecovery = $true; StateRoot = "s10"; QCHeight = 0; QCHash = ""; ConsensusSlot = 100
        ConsensusEpoch = 1; TotalActiveStake = 2; ValidatorFingerprint = "v"; TurbineFingerprint = "t"; FastLeaderFingerprint = ""; Status = $fastPathStatus
    }
    $snapshotB = [pscustomobject]@{
        Url = "mock://b"; PeerID = "peer-b"; HeadHeight = 16; HeadHash = "h16"; FinalizedHeight = 14; FinalizedHash = "f14"
        FinalityDepth = 2; StateRecovery = $true; StateRoot = "s16"; QCHeight = 0; QCHash = ""; ConsensusSlot = 106
        ConsensusEpoch = 2; TotalActiveStake = 2; ValidatorFingerprint = "v"; TurbineFingerprint = "t"; FastLeaderFingerprint = ""; Status = $fastPathStatus
    }
    $driftRound = Test-SnapshotRound -Snapshots @($snapshotA, $snapshotB) -Round 1
    Assert-SelfTest ($driftRound.Errors.Count -eq 0) "slot drift should not fail height drift inside the sampling window"
    $heightDriftWarnings = @($driftRound.Warnings | Where-Object { $_.Contains("head height drift") })
    Assert-SelfTest ($heightDriftWarnings.Count -eq 1) "slot drift should keep a warning"
    $epochDriftWarnings = @($driftRound.Warnings | Where-Object { $_.Contains("epoch boundary") })
    Assert-SelfTest ($epochDriftWarnings.Count -eq 1) "epoch boundary drift should keep a warning"

    Write-Host "consistency self-test passed"
}

if ($Help) {
    Show-Usage
    exit 0
}

if ($SelfTest) {
    Invoke-SelfTest
    exit 0
}

Assert-ValidOptions

$urls = @(Read-RpcUrls)
if ($urls.Count -eq 0) {
    Show-Usage
    Stop-WithUsageError "RpcUrls or RpcUrlFile is required"
}

$checkStartedAt = Get-Date
$allRounds = @()
$failedSnapshots = @()
$globalErrors = [System.Collections.Generic.List[string]]::new()
$globalWarnings = [System.Collections.Generic.List[string]]::new()
for ($round = 1; $round -le $Rounds; $round++) {
    Write-Host "==> consistency round $round/$Rounds"
    $roundStartedAt = Get-Date
    $snapshots = @()
    $roundFailedSnapshots = @()
    foreach ($url in $urls) {
        try {
            $snapshots += New-NodeSnapshot -Url $url
        } catch {
            $failure = [pscustomobject]@{
                Round     = $round
                Url       = $url
                Error     = $_.Exception.Message
                Timestamp = (Get-Date).ToString("o")
            }
            $failedSnapshots += $failure
            $roundFailedSnapshots += $failure
            $globalErrors.Add("$url snapshot failed: $($_.Exception.Message)")
        }
    }
    $roundResult = Test-SnapshotRound -Snapshots $snapshots -Round $round
    foreach ($errorMessage in $roundResult.Errors) {
        $globalErrors.Add("round ${round}: $errorMessage")
    }
    foreach ($warningMessage in $roundResult.Warnings) {
        $globalWarnings.Add("round ${round}: $warningMessage")
        Write-Warning "round ${round}: $warningMessage"
    }
    $allRounds += [pscustomobject]@{
        Round           = $round
        Timestamp       = (Get-Date).ToString("o")
        DurationMs      = [int64]((Get-Date) - $roundStartedAt).TotalMilliseconds
        Nodes           = $snapshots
        FailedSnapshots = $roundFailedSnapshots
        Errors          = @($roundResult.Errors)
        Warnings        = @($roundResult.Warnings)
    }
    if ($FailFast -and ($roundResult.Errors.Count -gt 0 -or $roundFailedSnapshots.Count -gt 0)) {
        $globalWarnings.Add("round ${round}: fail fast stopped remaining rounds")
        Write-Warning "round ${round}: fail fast stopped remaining rounds"
        break
    }
    if ($round -lt $Rounds) {
        Start-Sleep -Seconds $IntervalSeconds
    }
}

$successfulSnapshotCount = 0
foreach ($roundData in $allRounds) {
    $successfulSnapshotCount += @($roundData.Nodes).Count
}
$exitCode = 0
if ($globalErrors.Count -gt 0) {
    $exitCode = 1
}
$finishedAt = Get-Date
$summary = [pscustomobject]@{
    CheckedAt               = $finishedAt.ToString("o")
    StartedAt               = $checkStartedAt.ToString("o")
    FinishedAt              = $finishedAt.ToString("o")
    DurationMs              = [int64]($finishedAt - $checkStartedAt).TotalMilliseconds
    ExitCode                = $exitCode
    Urls                    = $urls
    NodeCount               = $urls.Count
    RoundCount              = $allRounds.Count
    RequireSecureP2P        = [bool]$RequireSecureP2P
    SuccessfulSnapshotCount = $successfulSnapshotCount
    FailedSnapshotCount     = $failedSnapshots.Count
    ErrorCount              = $globalErrors.Count
    WarningCount            = $globalWarnings.Count
    Rounds                  = $allRounds
    FailedSnapshots         = $failedSnapshots
    Errors                  = @($globalErrors)
    Warnings                = @($globalWarnings)
    OK                      = ($globalErrors.Count -eq 0)
}

Write-JsonReport -Value $summary -Path $OutputPath

if ($exitCode -ne 0) {
    foreach ($errorMessage in $globalErrors) {
        Write-Error $errorMessage -ErrorAction Continue
    }
    exit 1
}

Write-Host "consistency check passed"
exit 0
