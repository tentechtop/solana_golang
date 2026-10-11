[CmdletBinding()]
param(
    [string]$OutputRoot = "",
    [int]$BaseP2PPort = 15301,
    [int]$BaseRpcPort = 18991,
    [int]$SlotMillis = 500,
    [int]$PhaseTimeoutSeconds = 45,
    [ValidateRange(4, 64)]
    [int]$NodeCount = 8,
    [switch]$Adversarial,
    [switch]$KeepRunning
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$nodeCount = $NodeCount
$quorumRequired = [int][Math]::Floor((2.0 * $nodeCount) / 3.0) + 1
$quorumBoundaryOfflineCount = $nodeCount - $quorumRequired
$belowQuorumOfflineCount = $quorumBoundaryOfflineCount + 1
$businessUserSeeds = [string[]](1..4 | ForEach-Object { "$nodeCount-node-business-user-$_" })
$nextBusinessTransactionIndex = [uint64]0

function Get-TrailingNodeIndexes {
    param([int]$Count)

    if ($Count -le 0) {
        return [int[]]@()
    }
    return [int[]](($nodeCount - $Count + 1)..$nodeCount)
}

$availabilityScenarios = @(
    [pscustomobject]@{ Name = "$($nodeCount - 1)-online-bootnode-offline"; Offline = [int[]]@(1); ExpectProgress = (($nodeCount - 1) -ge $quorumRequired) },
    [pscustomobject]@{ Name = "$quorumRequired-online-quorum-boundary"; Offline = @(Get-TrailingNodeIndexes -Count $quorumBoundaryOfflineCount); ExpectProgress = $true },
    [pscustomobject]@{ Name = "$($quorumRequired - 1)-online-below-quorum"; Offline = @(Get-TrailingNodeIndexes -Count $belowQuorumOfflineCount); ExpectProgress = $false },
    [pscustomobject]@{ Name = "$([int][Math]::Floor($nodeCount / 2))-online-even-split"; Offline = @(Get-TrailingNodeIndexes -Count ($nodeCount - [int][Math]::Floor($nodeCount / 2))); ExpectProgress = $false },
    [pscustomobject]@{ Name = "one-online-isolation"; Offline = @(Get-TrailingNodeIndexes -Count ($nodeCount - 1)); ExpectProgress = $false },
    [pscustomobject]@{ Name = "zero-online-complete-outage"; Offline = [int[]](1..$nodeCount); ExpectProgress = $false }
)
$halfPartitionSize = [int][Math]::Floor($nodeCount / 2)
$symmetricPartitionA = [int[]](1..$halfPartitionSize)
$symmetricPartitionB = [int[]](($halfPartitionSize + 1)..$nodeCount)
$quorumPartitionA = [int[]](1..$quorumRequired)
$quorumPartitionB = [int[]](($quorumRequired + 1)..$nodeCount)
$partitionScenarios = @(
    [pscustomobject]@{ Name = "symmetric-$($symmetricPartitionA.Count)-$($symmetricPartitionB.Count)"; GroupA = $symmetricPartitionA; GroupB = $symmetricPartitionB },
    [pscustomobject]@{ Name = "quorum-minority-$($quorumPartitionA.Count)-$($quorumPartitionB.Count)"; GroupA = $quorumPartitionA; GroupB = $quorumPartitionB },
    [pscustomobject]@{ Name = "symmetric-$($symmetricPartitionA.Count)-$($symmetricPartitionB.Count)-repeat"; GroupA = $symmetricPartitionA; GroupB = $symmetricPartitionB }
)
$processes = @{}
$phaseResults = [System.Collections.Generic.List[object]]::new()

function Resolve-TestRoot {
    param([string]$RepositoryRoot, [string]$RequestedRoot)

    $distRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot "dist"))
    if ([string]::IsNullOrWhiteSpace($RequestedRoot)) {
        $suffix = (Get-Date).ToString("yyyyMMdd-HHmmss")
        return [System.IO.Path]::GetFullPath((Join-Path $distRoot "$nodeCount-node-fault-recovery-$suffix"))
    }
    $resolved = [System.IO.Path]::GetFullPath($RequestedRoot)
    $distPrefix = $distRoot.TrimEnd('\') + '\'
    if (-not ($resolved.TrimEnd('\') + '\').StartsWith($distPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputRoot must stay under $distRoot"
    }
    if (Test-Path -LiteralPath $resolved) {
        throw "OutputRoot already exists: $resolved"
    }
    return $resolved
}

function Write-Utf8Json {
    param([string]$Path, [object]$Value)

    $json = $Value | ConvertTo-Json -Depth 32
    [System.IO.File]::WriteAllText(
        [System.IO.Path]::GetFullPath($Path),
        $json + [Environment]::NewLine,
        [System.Text.UTF8Encoding]::new($false)
    )
}

function Invoke-NodeRpc {
    param([int]$NodeIndex, [string]$Method, [object[]]$Params = @())

    $body = @{
        jsonrpc = "2.0"
        id = [Guid]::NewGuid().ToString()
        method = $Method
        params = $Params
    } | ConvertTo-Json -Depth 8 -Compress
    $lastError = ""
    foreach ($attempt in 1..3) {
        try {
            $response = Invoke-RestMethod `
                -Uri "http://127.0.0.1:$($BaseRpcPort + $NodeIndex - 1)/" `
                -Method Post `
                -ContentType "application/json" `
                -Body $body `
                -TimeoutSec 3
            $errorProperty = $response.PSObject.Properties["error"]
            if ($null -ne $errorProperty -and $null -ne $errorProperty.Value) {
                throw "node $NodeIndex RPC $Method failed: $($errorProperty.Value.message)"
            }
            $resultProperty = $response.PSObject.Properties["result"]
            if ($null -eq $resultProperty) {
                throw "node $NodeIndex RPC $Method response has no result"
            }
            return $resultProperty.Value
        } catch {
            $lastError = $_.Exception.Message
            if ($attempt -lt 3) {
                Start-Sleep -Milliseconds 200
            }
        }
    }
    throw "node $NodeIndex RPC $Method failed after 3 attempts: $lastError"
}

function Get-NodeStatuses {
    param([int[]]$NodeIndexes)

    $statuses = [System.Collections.Generic.List[object]]::new()
    foreach ($nodeIndex in $NodeIndexes) {
        $status = Invoke-NodeRpc -NodeIndex $nodeIndex -Method "getNodeStatus"
        $elderValidatorID = ""
        $elderProperty = $status.consensus.PSObject.Properties["elder_validator_id"]
        if ($null -ne $elderProperty) {
            $elderValidatorID = [string]$elderProperty.Value
        }
        $statuses.Add([pscustomobject]@{
            NodeIndex = $nodeIndex
            HeadHeight = [uint64]$status.head_height
            HeadSlot = [uint64]$status.head_slot
            HeadHash = [string]$status.head_hash
            FinalizedHeight = [uint64]$status.finalized_height
            FinalizedSlot = [uint64]$status.finalized_slot
            FinalizedHash = [string]$status.finalized_hash
            ValidatorCount = [int]$status.validator_count
            ConsensusMode = [string]$status.consensus_mode
            ProvisionalHead = [bool]$status.provisional_head
            EpochID = [uint64]$status.epoch_id
            ElderValidatorID = $elderValidatorID
            QuorumReady = [bool]$status.liveness.quorum_ready
            ProductionEnabled = [bool]$status.liveness.production_enabled
            ReachableValidators = [int]$status.liveness.reachable_validator_count
            ReachableStake = [uint64]$status.liveness.reachable_stake_lamports
            RequiredStake = [uint64]$status.liveness.required_stake_lamports
        }) | Out-Null
    }
    return $statuses.ToArray()
}

function Wait-ForStatuses {
    param(
        [int[]]$NodeIndexes,
        [scriptblock]$Predicate,
        [string]$Description,
        [int]$TimeoutSeconds = $PhaseTimeoutSeconds
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $lastError = ""
    while ((Get-Date) -lt $deadline) {
        try {
            $statuses = @(Get-NodeStatuses -NodeIndexes $NodeIndexes)
            if (& $Predicate $statuses) {
                return $statuses
            }
            $lastError = "predicate not satisfied"
        } catch {
            $lastError = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out waiting for $Description. Last observation: $lastError"
}

function Wait-ForHealthyProgress {
    param(
        [uint64]$MinimumHeight,
        [string]$Phase,
        [int]$ExpectedValidatorCount = $nodeCount,
        [int]$TimeoutSeconds = $PhaseTimeoutSeconds
    )

    $indexes = 1..$nodeCount
    $statuses = Wait-ForStatuses -NodeIndexes $indexes -Description "$Phase healthy progress" -TimeoutSeconds $TimeoutSeconds -Predicate {
        param($values)
        if ($values.Count -ne $nodeCount) {
            return $false
        }
        $minimumObservedHeight = [uint64](($values | Measure-Object -Property HeadHeight -Minimum).Minimum)
        $maximumObservedHeight = [uint64](($values | Measure-Object -Property HeadHeight -Maximum).Maximum)
        if ($maximumObservedHeight - $minimumObservedHeight -gt 2) {
            return $false
        }
        foreach ($value in $values) {
            if ($value.ValidatorCount -ne $ExpectedValidatorCount -or
                $value.ConsensusMode -ne "multi_validator" -or
                -not $value.QuorumReady -or
                -not $value.ProductionEnabled -or
                $value.HeadHeight -lt $MinimumHeight) {
                return $false
            }
        }
        return $true
    }
    Assert-CommonFinalizedBlock -Statuses $statuses -Phase $Phase
    $phaseResults.Add([pscustomobject]@{ Phase = $Phase; Statuses = $statuses }) | Out-Null
    return $statuses
}

function Wait-ForQuorumProgress {
    param(
        [int[]]$OnlineNodeIndexes,
        [uint64]$MinimumHeight,
        [string]$Phase,
        [switch]$SkipRecord
    )

    $minimumReachable = $OnlineNodeIndexes.Count
    $statuses = Wait-ForStatuses -NodeIndexes $OnlineNodeIndexes -Description "$Phase quorum progress" -Predicate {
        param($values)
        if ($values.Count -ne $minimumReachable) {
            return $false
        }
        $minimumObservedHeight = [uint64](($values | Measure-Object -Property HeadHeight -Minimum).Minimum)
        $maximumObservedHeight = [uint64](($values | Measure-Object -Property HeadHeight -Maximum).Maximum)
        if ($maximumObservedHeight - $minimumObservedHeight -gt 2) {
            return $false
        }
        foreach ($value in $values) {
            if ($value.ValidatorCount -ne $nodeCount -or
                $value.ConsensusMode -ne "multi_validator" -or
                -not $value.QuorumReady -or
                -not $value.ProductionEnabled -or
                $value.ReachableValidators -lt $minimumReachable -or
                $value.HeadHeight -lt $MinimumHeight) {
                return $false
            }
        }
        return $true
    }
    Assert-CommonFinalizedBlock -Statuses $statuses -Phase $Phase
    if (-not $SkipRecord) {
        $phaseResults.Add([pscustomobject]@{ Phase = $Phase; Expected = "progress"; Statuses = $statuses }) | Out-Null
    }
    return $statuses
}

function Wait-ForFaultHalt {
    param([int[]]$OnlineNodeIndexes, [string]$Phase, [switch]$SkipRecord)

    $degraded = Wait-ForStatuses -NodeIndexes $OnlineNodeIndexes -Description "$Phase quorum degradation" -Predicate {
        param($values)
        foreach ($value in $values) {
            if ($value.QuorumReady -or $value.ProductionEnabled -or
                $value.ReachableValidators -gt $OnlineNodeIndexes.Count) {
                return $false
            }
        }
        return $true
    }

    $stableKey = ""
    $stableCount = 0
    $deadline = (Get-Date).AddSeconds($PhaseTimeoutSeconds)
    $stableStatuses = $degraded
    while ((Get-Date) -lt $deadline) {
        $statuses = @(Get-NodeStatuses -NodeIndexes $OnlineNodeIndexes)
        $key = (($statuses | Sort-Object NodeIndex | ForEach-Object { "$($_.NodeIndex):$($_.HeadHeight):$($_.HeadHash)" }) -join "|")
        if ($key -eq $stableKey) {
            $stableCount++
        } else {
            $stableKey = $key
            $stableCount = 1
        }
        $stableStatuses = $statuses
        $commonHeadCount = @($statuses | Group-Object HeadHeight, HeadHash).Count
        if ($stableCount -ge 4 -and $commonHeadCount -eq 1) {
            Assert-CommonFinalizedBlock -Statuses $stableStatuses -Phase $Phase
            if (-not $SkipRecord) {
                $phaseResults.Add([pscustomobject]@{ Phase = $Phase; Expected = "safe_halt"; Statuses = $stableStatuses }) | Out-Null
            }
            return $stableStatuses
        }
        Start-Sleep -Seconds 1
    }
    throw "Timed out waiting for stable chain head during $Phase"
}

function Assert-CommonFinalizedBlock {
    param([object[]]$Statuses, [string]$Phase)

    $reference = $Statuses | Sort-Object FinalizedHeight | Select-Object -First 1
    if ($reference.FinalizedSlot -eq 0) {
        return
    }
    foreach ($status in $Statuses) {
        $block = Invoke-NodeRpc -NodeIndex $status.NodeIndex -Method "getBlock" -Params @([uint64]$reference.FinalizedSlot)
        if ([string]$block.blockhash -ne $reference.FinalizedHash) {
            throw "$Phase fork detected at finalized slot $($reference.FinalizedSlot): node $($status.NodeIndex) has $($block.blockhash), expected $($reference.FinalizedHash)"
        }
    }
}

function Invoke-FixedTransferBatch {
    param(
        [int[]]$NodeIndexes,
        [int]$TransactionCount,
        [string]$Phase
    )

    $manifestValidators = @($NodeIndexes | ForEach-Object {
        [ordered]@{
            name = "fault-node-$_"
            staker_seed = "$nodeCount-node-fault-staker-$_"
            validator_address = ""
            rpc_url = "http://127.0.0.1:$($BaseRpcPort + [int]$_ - 1)/"
        }
    })
    $manifestPath = Join-Path $businessReportDirectory "$Phase-manifest.json"
    $businessReportPath = Join-Path $businessReportDirectory "$Phase-report.json"
    Write-Utf8Json -Path $manifestPath -Value ([ordered]@{
        validators = $manifestValidators
        user_seeds = $businessUserSeeds
        rpc_urls = @($manifestValidators | ForEach-Object { $_.rpc_url })
    })
    $startIndex = $script:nextBusinessTransactionIndex
    $workerCount = [Math]::Min(4, $TransactionCount)
    $stressOutput = @(& $stressBinary `
        -manifest $manifestPath `
        -transactions $TransactionCount `
        -start-index $startIndex `
        -workers $workerCount `
        -duration 30s `
        -output $businessReportPath)
    if ($LASTEXITCODE -ne 0) {
        throw "$Phase fixed transfer command failed"
    }
    foreach ($outputLine in $stressOutput) {
        Write-Host $outputLine
    }
    $businessReport = Get-Content -LiteralPath $businessReportPath -Raw | ConvertFrom-Json
    if ([uint64]$businessReport.transfer_succeeded -ne [uint64]$TransactionCount -or [uint64]$businessReport.failed -ne 0) {
        throw "$Phase fixed transfers accepted=$($businessReport.transfer_succeeded) failed=$($businessReport.failed), expected $TransactionCount/0"
    }
    if (@($businessReport.transaction_signatures).Count -ne $TransactionCount) {
        throw "$Phase returned $(@($businessReport.transaction_signatures).Count) transaction signatures, expected $TransactionCount"
    }
    $script:nextBusinessTransactionIndex += [uint64]$TransactionCount
    return $businessReport
}

function Wait-ForFinalizedTransactions {
    param(
        [int[]]$NodeIndexes,
        [string[]]$Signatures,
        [string]$Phase
    )

    $expectedCount = $Signatures.Count
    $deadline = (Get-Date).AddSeconds($PhaseTimeoutSeconds)
    $lastCounts = @{}
    while ((Get-Date) -lt $deadline) {
        $allFinalized = $true
        try {
            foreach ($nodeIndex in $NodeIndexes) {
                $count = 0
                foreach ($signature in $Signatures) {
                    $transaction = Invoke-NodeRpc -NodeIndex $nodeIndex -Method "getTransaction" -Params @($signature)
                    if ([bool]$transaction.found -and [bool]$transaction.finalized -and [string]$transaction.status -eq "finalized") {
                        $count++
                    }
                }
                $lastCounts[[string]$nodeIndex] = $count
                if ($count -lt $expectedCount) {
                    $allFinalized = $false
                }
            }
            if ($allFinalized) {
                return $lastCounts
            }
        } catch {
            $allFinalized = $false
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out waiting for $expectedCount finalized transactions during $Phase. Last counts: $($lastCounts | ConvertTo-Json -Compress)"
}

function Start-Node {
    param([int]$NodeIndex, [string]$Phase)

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $stdoutPath = Join-Path $logDirectory "node$NodeIndex-$Phase.out.log"
    $stderrPath = Join-Path $logDirectory "node$NodeIndex-$Phase.err.log"
    $process = Start-Process `
        -FilePath $posNodeBinary `
        -ArgumentList @("-config", ('"{0}"' -f $configPath)) `
        -RedirectStandardOutput $stdoutPath `
        -RedirectStandardError $stderrPath `
        -WindowStyle Hidden `
        -PassThru
    $processes[$NodeIndex] = $process
}

function Stop-Node {
    param([int]$NodeIndex)

    if (-not $processes.ContainsKey($NodeIndex)) {
        return
    }
    $process = $processes[$NodeIndex]
    if ($null -ne $process -and -not $process.HasExited) {
        $processID = $process.Id
        $expectedPath = [System.IO.Path]::GetFullPath($posNodeBinary)
        Stop-Process -Id $processID -Force -ErrorAction SilentlyContinue
        $deadline = (Get-Date).AddSeconds(15)
        while ((Get-Date) -lt $deadline) {
            $running = Get-Process -Id $processID -ErrorAction SilentlyContinue
            if ($null -eq $running) {
                break
            }
            $runningPath = [string]$running.Path
            if (-not [string]::IsNullOrWhiteSpace($runningPath)) {
                $resolvedRunningPath = [System.IO.Path]::GetFullPath($runningPath)
                if (-not $resolvedRunningPath.Equals($expectedPath, [System.StringComparison]::OrdinalIgnoreCase)) {
                    throw "node $NodeIndex process id $processID was reused before cleanup completed"
                }
            }
            Stop-Process -Id $processID -Force -ErrorAction SilentlyContinue
            Start-Sleep -Milliseconds 200
        }
        if ($null -ne (Get-Process -Id $processID -ErrorAction SilentlyContinue)) {
            throw "node $NodeIndex did not exit within 15 seconds"
        }
    }
    if ($null -ne $process) {
        $process.Dispose()
    }
    $processes.Remove($NodeIndex)
}

function Stop-AllNodes {
    foreach ($nodeIndex in @($processes.Keys)) {
        Stop-Node -NodeIndex ([int]$nodeIndex)
    }
}

function Set-NodeProposalDelay {
    param([int]$NodeIndex, [int64]$DelayMillis)

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    if ($DelayMillis -gt 0) {
        $config | Add-Member -NotePropertyName "fault_injection" -NotePropertyValue ([pscustomobject]@{
            proposal_delay_millis = $DelayMillis
        }) -Force
    } else {
        $config.PSObject.Properties.Remove("fault_injection")
    }
    Write-Utf8Json -Path $configPath -Value $config
}

function Set-NodeByzantineFault {
    param(
        [int]$NodeIndex,
        [bool]$DoubleVote,
        [bool]$DoubleProposal
    )

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    if ($null -eq $config.PSObject.Properties["fault_injection"]) {
        $config | Add-Member -NotePropertyName "fault_injection" -NotePropertyValue ([pscustomobject]@{})
    }
    if ($DoubleVote) {
        $config.fault_injection | Add-Member -NotePropertyName "double_vote_once" -NotePropertyValue $true -Force
    } else {
        $config.fault_injection.PSObject.Properties.Remove("double_vote_once")
    }
    if ($DoubleProposal) {
        $config.fault_injection | Add-Member -NotePropertyName "double_proposal_once" -NotePropertyValue $true -Force
    } else {
        $config.fault_injection.PSObject.Properties.Remove("double_proposal_once")
    }
    if (@($config.fault_injection.PSObject.Properties).Count -eq 0) {
        $config.PSObject.Properties.Remove("fault_injection")
    }
    Write-Utf8Json -Path $configPath -Value $config
}

function Get-ValidatorStateByNodeIndex {
    param([object]$Status, [int]$NodeIndex)

    $peerID = [string]$peerIDs[$NodeIndex]
    return @($Status.consensus.validators | Where-Object { [string]$_.p2p_peer_id -eq $peerID } | Select-Object -First 1)
}

function Wait-ForAttackLogsAndSlashing {
    param(
        [int[]]$AttackerNodeIndexes,
        [string]$Phase,
        [string]$ExpectedLogMessage
    )

    $deadline = (Get-Date).AddSeconds($PhaseTimeoutSeconds)
    $lastObservation = ""
    while ((Get-Date) -lt $deadline) {
        try {
            $allLogsObserved = $true
            foreach ($nodeIndex in $AttackerNodeIndexes) {
                $logPath = Join-Path $logDirectory "node$nodeIndex-$Phase.out.log"
                if (-not (Test-Path -LiteralPath $logPath) -or
                    -not (Select-String -LiteralPath $logPath -SimpleMatch $ExpectedLogMessage -Quiet)) {
                    $allLogsObserved = $false
                    break
                }
            }
            $status = Invoke-NodeRpc -NodeIndex 1 -Method "getNodeStatus"
            $slashedCount = 0
            foreach ($nodeIndex in $AttackerNodeIndexes) {
                $validator = @(Get-ValidatorStateByNodeIndex -Status $status -NodeIndex $nodeIndex)
                if ($validator.Count -eq 1 -and
                    [uint64]$validator[0].last_slashed_slot -gt 0 -and
                    [string]$validator[0].status -eq "jailed") {
                    $slashedCount++
                }
            }
            $lastObservation = "logs=$allLogsObserved slashed=$slashedCount/$($AttackerNodeIndexes.Count) head=$($status.head_height)"
            if ($allLogsObserved -and $slashedCount -eq $AttackerNodeIndexes.Count) {
                return $status
            }
        } catch {
            $lastObservation = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out waiting for $Phase evidence and slashing. Last observation: $lastObservation"
}

function Wait-ForRecoveryElderProgress {
    param([int[]]$NodeIndexes, [string]$Phase)

    $deadline = (Get-Date).AddSeconds($PhaseTimeoutSeconds)
    $checkpointHeight = [uint64]0
    $checkpointHash = ""
    $elderValidatorID = ""
    $initialMinimumHeadHeight = [uint64]0
    $recoveryObserved = $false
    while ((Get-Date) -lt $deadline) {
        $statuses = @(Get-NodeStatuses -NodeIndexes $NodeIndexes)
        $minimumHeight = [uint64](($statuses | Measure-Object -Property HeadHeight -Minimum).Minimum)
        $maximumHeight = [uint64](($statuses | Measure-Object -Property HeadHeight -Maximum).Maximum)
        $finalizedGroups = @($statuses | Group-Object FinalizedHeight, FinalizedHash)
        $elderGroups = @($statuses | Group-Object ElderValidatorID)
        $allRecovery = $true
        foreach ($status in $statuses) {
            if ($status.ConsensusMode -ne "recovery_elder" -or
                -not $status.ProvisionalHead -or
                $status.ValidatorCount -ne 1 -or
                -not $status.ProductionEnabled) {
                $allRecovery = $false
                break
            }
        }
        if (-not $recoveryObserved) {
            if ($allRecovery -and $finalizedGroups.Count -eq 1 -and $elderGroups.Count -eq 1 -and
                -not [string]::IsNullOrWhiteSpace([string]$statuses[0].ElderValidatorID)) {
                $checkpointHeight = [uint64]$statuses[0].FinalizedHeight
                $checkpointHash = [string]$statuses[0].FinalizedHash
                $elderValidatorID = [string]$statuses[0].ElderValidatorID
                $initialMinimumHeadHeight = $minimumHeight
                $recoveryObserved = $true
            }
        } elseif ($allRecovery -and
            $finalizedGroups.Count -eq 1 -and
            [uint64]$statuses[0].FinalizedHeight -eq $checkpointHeight -and
            [string]$statuses[0].FinalizedHash -eq $checkpointHash -and
            $elderGroups.Count -eq 1 -and
            [string]$statuses[0].ElderValidatorID -eq $elderValidatorID -and
            $minimumHeight -ge $initialMinimumHeadHeight + 5 -and
            $maximumHeight - $minimumHeight -le 8) {
            Assert-CommonFinalizedBlock -Statuses $statuses -Phase $Phase
            $reference = $statuses | Sort-Object HeadSlot | Select-Object -First 1
            foreach ($status in $statuses) {
                $block = Invoke-NodeRpc -NodeIndex $status.NodeIndex -Method "getBlock" -Params @([uint64]$reference.HeadSlot)
                if ([string]$block.blockhash -ne [string]$reference.HeadHash) {
                    throw "$Phase provisional fork detected at slot $($reference.HeadSlot): node $($status.NodeIndex) has $($block.blockhash), expected $($reference.HeadHash)"
                }
            }
            return $statuses
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out waiting for deterministic recovery elder progress during $Phase"
}

function Set-NodeBlockedPeers {
    param([int]$NodeIndex, [string[]]$BlockedPeerIDs)

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $faultInjectionProperty = $config.PSObject.Properties["fault_injection"]
    if ($BlockedPeerIDs.Count -gt 0) {
        if ($null -eq $faultInjectionProperty) {
            $config | Add-Member -NotePropertyName "fault_injection" -NotePropertyValue ([pscustomobject]@{})
        }
        $config.fault_injection | Add-Member `
            -NotePropertyName "blocked_peer_ids" `
            -NotePropertyValue ([string[]]$BlockedPeerIDs) `
            -Force
    } elseif ($null -ne $faultInjectionProperty) {
        $config.fault_injection.PSObject.Properties.Remove("blocked_peer_ids")
        if (@($config.fault_injection.PSObject.Properties).Count -eq 0) {
            $config.PSObject.Properties.Remove("fault_injection")
        }
    }
    Write-Utf8Json -Path $configPath -Value $config
}

function New-TestBootstrapPeer {
    param([int]$PeerNodeIndex)

    return [ordered]@{
        peer_id = [string]$peerIDs[$PeerNodeIndex]
        ip = "127.0.0.1"
        port = $BaseP2PPort + $PeerNodeIndex - 1
        network = "tcp"
        role = "validator"
        roles = @("validator")
        capabilities = @("validator", "relay", "state_sync", "dht")
        rpc_url = "http://127.0.0.1:$($BaseRpcPort + $PeerNodeIndex - 1)/"
    }
}

function Set-NodeBootstrapGroup {
    param([int]$NodeIndex, [int[]]$Group)

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $bootstrapPeers = @()
    foreach ($peerNodeIndex in $Group) {
        if ($peerNodeIndex -eq $NodeIndex) {
            continue
        }
        $bootstrapPeers += New-TestBootstrapPeer -PeerNodeIndex $peerNodeIndex
    }
    $config.bootstrap_peers = $bootstrapPeers
    Write-Utf8Json -Path $configPath -Value $config
}

function Restore-NodeBootstrapPeer {
    param([int]$NodeIndex)

    $configPath = Join-Path $configDirectory "node$NodeIndex.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    if ($NodeIndex -eq 1) {
        $config.bootstrap_peers = @()
    } else {
        $config.bootstrap_peers = @(New-TestBootstrapPeer -PeerNodeIndex 1)
        $config.bootstrap_peers[0].role = "bootnode"
        $config.bootstrap_peers[0].roles = @("bootnode")
        $config.bootstrap_peers[0].capabilities = @("relay", "dht")
    }
    Write-Utf8Json -Path $configPath -Value $config
}

function Set-NetworkPartition {
    param([int[]]$GroupA, [int[]]$GroupB)

    foreach ($nodeIndex in $GroupA) {
        $blockedPeerIDs = [string[]]@($GroupB | ForEach-Object { [string]$peerIDs[[int]$_] })
        Set-NodeBlockedPeers -NodeIndex $nodeIndex -BlockedPeerIDs $blockedPeerIDs
        Set-NodeBootstrapGroup -NodeIndex $nodeIndex -Group $GroupA
    }
    foreach ($nodeIndex in $GroupB) {
        $blockedPeerIDs = [string[]]@($GroupA | ForEach-Object { [string]$peerIDs[[int]$_] })
        Set-NodeBlockedPeers -NodeIndex $nodeIndex -BlockedPeerIDs $blockedPeerIDs
        Set-NodeBootstrapGroup -NodeIndex $nodeIndex -Group $GroupB
    }
}

function Clear-NetworkPartition {
    foreach ($nodeIndex in 1..$nodeCount) {
        Set-NodeBlockedPeers -NodeIndex $nodeIndex -BlockedPeerIDs ([string[]]@())
        Restore-NodeBootstrapPeer -NodeIndex $nodeIndex
    }
}

function Wait-ForExactPartitionReachability {
    param([int[]]$NodeIndexes, [string]$Phase)

    $expectedReachableCount = $NodeIndexes.Count
    return @(Wait-ForStatuses -NodeIndexes $NodeIndexes -Description "$Phase exact partition reachability" -Predicate {
        param($values)
        foreach ($value in $values) {
            if ($value.ReachableValidators -ne $expectedReachableCount) {
                return $false
            }
            if ($expectedReachableCount -ge $quorumRequired) {
                if (-not $value.QuorumReady -or -not $value.ProductionEnabled) {
                    return $false
                }
            } elseif ($value.QuorumReady -or $value.ProductionEnabled) {
                return $false
            }
        }
        return $true
    })
}

function Wait-ForPartitionGroup {
    param(
        [int[]]$NodeIndexes,
        [uint64]$MinimumHeight,
        [string]$Phase
    )

    if ($NodeIndexes.Count -ge $quorumRequired) {
        [void](Wait-ForQuorumProgress `
            -OnlineNodeIndexes $NodeIndexes `
            -MinimumHeight ($MinimumHeight + 3) `
            -Phase $Phase `
            -SkipRecord)
        $statuses = @(Wait-ForExactPartitionReachability -NodeIndexes $NodeIndexes -Phase $Phase)
        $expected = "progress"
    } else {
        [void](Wait-ForFaultHalt -OnlineNodeIndexes $NodeIndexes -Phase $Phase -SkipRecord)
        $statuses = @(Wait-ForExactPartitionReachability -NodeIndexes $NodeIndexes -Phase $Phase)
        $expected = "safe_halt"
    }
    Assert-CommonFinalizedBlock -Statuses $statuses -Phase $Phase
    $phaseResults.Add([pscustomobject]@{ Phase = $Phase; Expected = $expected; Statuses = $statuses }) | Out-Null
    return $statuses
}

function Invoke-NetworkPartitionScenario {
    param(
        [object]$Scenario,
        [uint64]$MinimumHealthyHeight
    )

    $groupA = [int[]]$Scenario.GroupA
    $groupB = [int[]]$Scenario.GroupB
    Stop-AllNodes
    Set-NetworkPartition -GroupA $groupA -GroupB $groupB
    foreach ($nodeIndex in 1..$nodeCount) {
        Start-Node -NodeIndex $nodeIndex -Phase ([string]$Scenario.Name)
    }

    $groupAStatuses = @(Wait-ForPartitionGroup `
        -NodeIndexes $groupA `
        -MinimumHeight $MinimumHealthyHeight `
        -Phase "$($Scenario.Name)-group-a")
    $groupBStatuses = @(Wait-ForPartitionGroup `
        -NodeIndexes $groupB `
        -MinimumHeight $MinimumHealthyHeight `
        -Phase "$($Scenario.Name)-group-b")
    $partitionStatuses = @($groupAStatuses) + @($groupBStatuses)
    Assert-CommonFinalizedBlock -Statuses $partitionStatuses -Phase ([string]$Scenario.Name)
    $observedHeight = [uint64](($partitionStatuses | Measure-Object -Property HeadHeight -Maximum).Maximum)

    $quorumGroup = [int[]]@()
    $quorumStatuses = @()
    if ($groupA.Count -ge $quorumRequired) {
        $quorumGroup = $groupA
        $quorumStatuses = $groupAStatuses
    } elseif ($groupB.Count -ge $quorumRequired) {
        $quorumGroup = $groupB
        $quorumStatuses = $groupBStatuses
    }
    if ($quorumGroup.Count -gt 0) {
        $businessReport = Invoke-FixedTransferBatch `
            -NodeIndexes $quorumGroup `
            -TransactionCount 8 `
            -Phase "$($Scenario.Name)-majority-transfers"
        [void](Wait-ForFinalizedTransactions `
            -NodeIndexes $quorumGroup `
            -Signatures ([string[]]$businessReport.transaction_signatures) `
            -Phase "$($Scenario.Name)-majority-transfers")
        $postBusinessStatuses = @(Wait-ForQuorumProgress `
            -OnlineNodeIndexes $quorumGroup `
            -MinimumHeight ($observedHeight + 2) `
            -Phase "$($Scenario.Name)-majority-transfers" `
            -SkipRecord)
        $phaseResults.Add([pscustomobject]@{
            Phase = "$($Scenario.Name)-majority-transfers"
            Expected = "transactions_finalized_on_quorum_partition"
            TransferSucceeded = [uint64]$businessReport.transfer_succeeded
            Statuses = $postBusinessStatuses
        }) | Out-Null
        $observedHeight = [uint64](($postBusinessStatuses | Measure-Object -Property HeadHeight -Maximum).Maximum)
    }

    Stop-AllNodes
    Clear-NetworkPartition
    foreach ($nodeIndex in 1..$nodeCount) {
        Start-Node -NodeIndex $nodeIndex -Phase "recovery-$($Scenario.Name)"
    }
    return @(Wait-ForHealthyProgress `
        -MinimumHeight ($observedHeight + 3) `
        -Phase "recovery-$($Scenario.Name)")
}

function Invoke-LeaderTimeoutScenario {
    param([object[]]$HealthyStatuses)

    $routingStatus = Invoke-NodeRpc -NodeIndex 1 -Method "getNodeStatus"
    $minimumTargetSlot = [uint64]$routingStatus.head_slot + 8
    # Fault injection applies to every proposal from the restarted node. Pick the first safe future slot;
    # selecting the last entry needlessly delays several earlier slots from the same leader and can consume the phase deadline.
    $targetLeader = @($routingStatus.upcoming_leaders |
        Where-Object { [uint64]$_.slot -ge $minimumTargetSlot } |
        Sort-Object { [uint64]$_.slot } |
        Select-Object -First 1)
    if ($targetLeader.Count -ne 1) {
        throw "unable to select a future leader for timeout injection"
    }
    $targetNodeIndex = 0
    foreach ($nodeIndex in 1..$nodeCount) {
        if ([string]$peerIDs[$nodeIndex] -eq [string]$targetLeader[0].peer_id) {
            $targetNodeIndex = $nodeIndex
            break
        }
    }
    if ($targetNodeIndex -eq 0) {
        throw "future leader peer ID is not mapped to a local node"
    }

    $beforeMaximumGap = [int64](($HealthyStatuses | ForEach-Object {
        [int64]$_.HeadSlot - [int64]$_.HeadHeight
    } | Measure-Object -Maximum).Maximum)
    $beforeMinimumHeight = [uint64](($HealthyStatuses | Measure-Object -Property HeadHeight -Minimum).Minimum)
    $targetSlot = [uint64]$targetLeader[0].slot
    # Keep the injected delay well beyond the eight-slot leader timeout so the test must form TimeoutQC.
    $delayMillis = [int64]$SlotMillis * 12

    Stop-Node -NodeIndex $targetNodeIndex
    Set-NodeProposalDelay -NodeIndex $targetNodeIndex -DelayMillis $delayMillis
    Start-Node -NodeIndex $targetNodeIndex -Phase "leader-timeout"

    $statuses = Wait-ForStatuses -NodeIndexes (1..$nodeCount) -Description "leader timeout QC and recovery" -Predicate {
        param($values)
        if ($values.Count -ne $nodeCount) {
            return $false
        }
        foreach ($value in $values) {
            $gap = [int64]$value.HeadSlot - [int64]$value.HeadHeight
            if (-not $value.QuorumReady -or -not $value.ProductionEnabled -or
                $value.HeadSlot -le $targetSlot -or
                $value.HeadHeight -lt ($beforeMinimumHeight + 2) -or
                $gap -le $beforeMaximumGap) {
                return $false
            }
        }
        return $true
    }
    Assert-CommonFinalizedBlock -Statuses $statuses -Phase "leader-timeout"

    $targetLog = Join-Path $logDirectory "node$targetNodeIndex-leader-timeout.out.log"
    $targetErrorLog = Join-Path $logDirectory "node$targetNodeIndex-leader-timeout.err.log"
    $delayObserved = (Test-Path -LiteralPath $targetLog) -and
        (Select-String -LiteralPath $targetLog -SimpleMatch "fault injection proposal delay" -Quiet)
    if (-not $delayObserved -and (Test-Path -LiteralPath $targetErrorLog)) {
        $delayObserved = Select-String -LiteralPath $targetErrorLog -SimpleMatch "fault injection proposal delay" -Quiet
    }
    if (-not $delayObserved) {
        throw "leader timeout phase advanced without observing the injected proposal delay"
    }

    $phaseResults.Add([pscustomobject]@{
        Phase = "leader-timeout"
        Expected = "timeout_qc_then_progress"
        TargetNodeIndex = $targetNodeIndex
        TargetSlot = $targetSlot
        ProposalDelayMillis = $delayMillis
        Statuses = $statuses
    }) | Out-Null

    Stop-Node -NodeIndex $targetNodeIndex
    Set-NodeProposalDelay -NodeIndex $targetNodeIndex -DelayMillis 0
    Start-Node -NodeIndex $targetNodeIndex -Phase "leader-timeout-recovery"
    $minimumHeight = [uint64](($statuses | Measure-Object -Property HeadHeight -Maximum).Maximum) + 2
    return @(Wait-ForHealthyProgress -MinimumHeight $minimumHeight -Phase "leader-timeout-recovery")
}

function Invoke-AdversarialScenarios {
    param([object[]]$HealthyStatuses)

    if ($nodeCount -lt 7) {
        throw "adversarial matrix requires at least 7 validators"
    }
    $baselineHeight = [uint64](($HealthyStatuses | Measure-Object -Property HeadHeight -Minimum).Minimum)
    $routingStatus = Invoke-NodeRpc -NodeIndex 1 -Method "getNodeStatus"
    $minimumTargetSlot = [uint64]$routingStatus.head_slot + 4
    $doubleProposalNodeIndex = 0
    $doubleProposalTargetSlot = [uint64]0
    foreach ($leader in @($routingStatus.upcoming_leaders | Sort-Object { [uint64]$_.slot })) {
        if ([uint64]$leader.slot -lt $minimumTargetSlot) {
            continue
        }
        foreach ($nodeIndex in 2..$nodeCount) {
            if ([string]$peerIDs[$nodeIndex] -eq [string]$leader.peer_id) {
                $doubleProposalNodeIndex = $nodeIndex
                $doubleProposalTargetSlot = [uint64]$leader.slot
                break
            }
        }
        if ($doubleProposalNodeIndex -gt 0) {
            break
        }
    }
    if ($doubleProposalNodeIndex -eq 0) {
        throw "unable to select non-bootstrap leader for double-proposal attack"
    }

    Stop-Node -NodeIndex $doubleProposalNodeIndex
    Set-NodeByzantineFault -NodeIndex $doubleProposalNodeIndex -DoubleVote $false -DoubleProposal $true
    Start-Node -NodeIndex $doubleProposalNodeIndex -Phase "attacker-double-proposal"
    $doubleProposalSlashStatus = Wait-ForAttackLogsAndSlashing `
        -AttackerNodeIndexes ([int[]]@($doubleProposalNodeIndex)) `
        -Phase "attacker-double-proposal" `
        -ExpectedLogMessage "fault injection double proposal emitted"
    $healthy = @(Wait-ForHealthyProgress `
        -MinimumHeight ([Math]::Max($baselineHeight + 4, [uint64]$doubleProposalSlashStatus.head_height + 2)) `
        -Phase "attacker-double-proposal-finalized")
    $phaseResults.Add([pscustomobject]@{
        Phase = "attacker-double-proposal"
        Expected = "evidence_finalized_attacker_jailed_progress_continues"
        AttackerNodeIndexes = @($doubleProposalNodeIndex)
        TargetSlot = $doubleProposalTargetSlot
        Statuses = $healthy
    }) | Out-Null

    $doubleVoteNodeIndexes = [int[]]@(2..$nodeCount | Where-Object { $_ -ne $doubleProposalNodeIndex } | Select-Object -First 4)
    foreach ($nodeIndex in $doubleVoteNodeIndexes) {
        Stop-Node -NodeIndex $nodeIndex
        Set-NodeByzantineFault -NodeIndex $nodeIndex -DoubleVote $true -DoubleProposal $false
        Start-Node -NodeIndex $nodeIndex -Phase "attackers-four-double-vote"
    }
    $beforeDoubleVoteHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)
    $doubleVoteSlashStatus = Wait-ForAttackLogsAndSlashing `
        -AttackerNodeIndexes $doubleVoteNodeIndexes `
        -Phase "attackers-four-double-vote" `
        -ExpectedLogMessage "fault injection double vote emitted"

    $fiveAttackers = [int[]]@($doubleProposalNodeIndex) + $doubleVoteNodeIndexes
    $honestNodeIndexes = [int[]]@(1..$nodeCount | Where-Object { $fiveAttackers -notcontains $_ })
    $healthy = @(Wait-ForHealthyProgress `
        -MinimumHeight ([Math]::Max($beforeDoubleVoteHeight + 4, [uint64]$doubleVoteSlashStatus.head_height + 2)) `
        -Phase "attackers-five-total-finalized" `
        -ExpectedValidatorCount $honestNodeIndexes.Count `
        -TimeoutSeconds ($PhaseTimeoutSeconds * 2))

    $businessReport = Invoke-FixedTransferBatch `
        -NodeIndexes $honestNodeIndexes `
        -TransactionCount 8 `
        -Phase "attackers-five-total-transfers"
    [void](Wait-ForFinalizedTransactions `
        -NodeIndexes $honestNodeIndexes `
        -Signatures ([string[]]$businessReport.transaction_signatures) `
        -Phase "attackers-five-total-transfers")
    $healthy = @(Wait-ForHealthyProgress `
        -MinimumHeight ([uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum) + 2) `
        -Phase "attackers-five-total-progress" `
        -ExpectedValidatorCount $honestNodeIndexes.Count)
    $phaseResults.Add([pscustomobject]@{
        Phase = "attackers-five-total"
        Expected = "strict_quorum_progress_and_transactions_finalize"
        AttackerNodeIndexes = $fiveAttackers
        HonestValidatorCount = $honestNodeIndexes.Count
        TransferSucceeded = [uint64]$businessReport.transfer_succeeded
        Statuses = $healthy
    }) | Out-Null

    $sixthAttacker = [int](1..$nodeCount | Where-Object { $fiveAttackers -notcontains $_ } | Select-Object -First 1)
    Stop-Node -NodeIndex $sixthAttacker
    Set-NodeByzantineFault -NodeIndex $sixthAttacker -DoubleVote $true -DoubleProposal $false
    Start-Node -NodeIndex $sixthAttacker -Phase "attacker-sixth-double-vote"
    [void](Wait-ForAttackLogsAndSlashing `
        -AttackerNodeIndexes ([int[]]@($sixthAttacker)) `
        -Phase "attacker-sixth-double-vote" `
        -ExpectedLogMessage "fault injection double vote emitted")
    $stableStatuses = @(Wait-ForRecoveryElderProgress -NodeIndexes (1..$nodeCount) -Phase "attackers-six-total-recovery-elder")
    $phaseResults.Add([pscustomobject]@{
        Phase = "attackers-six-total-recovery-elder"
        Expected = "single_elder_provisional_progress_with_frozen_common_finality"
        AttackerNodeIndexes = [int[]]@($fiveAttackers) + @($sixthAttacker)
        Statuses = $stableStatuses
    }) | Out-Null
    return $stableStatuses
}

if ($BaseP2PPort -lt 1 -or $BaseP2PPort + $nodeCount - 1 -gt 65535) {
    throw "invalid P2P port range"
}
if ($BaseRpcPort -lt 1 -or $BaseRpcPort + $nodeCount - 1 -gt 65535) {
    throw "invalid RPC port range"
}
if ($SlotMillis -lt 200) {
    throw "SlotMillis must be at least 200"
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$testRoot = Resolve-TestRoot -RepositoryRoot $repositoryRoot -RequestedRoot $OutputRoot
$binDirectory = Join-Path $testRoot "bin"
$configDirectory = Join-Path $testRoot "config"
$dataDirectory = Join-Path $testRoot "data"
$logDirectory = Join-Path $testRoot "logs"
$businessReportDirectory = Join-Path $testRoot "business"
New-Item -ItemType Directory -Path $binDirectory, $configDirectory, $dataDirectory, $logDirectory, $businessReportDirectory -Force | Out-Null
$posNodeBinary = Join-Path $binDirectory "posnode.exe"
$stressBinary = Join-Path $binDirectory "posnode-cluster-stress.exe"
$reportPath = Join-Path $testRoot "fault-recovery-report.json"

Push-Location $repositoryRoot
try {
    & go build -o $posNodeBinary .\cmd\posnode
    if ($LASTEXITCODE -ne 0) {
        throw "go build .\cmd\posnode failed"
    }
    & go build -o $stressBinary .\tools\posnode_cluster_stress
    if ($LASTEXITCODE -ne 0) {
        throw "go build .\tools\posnode_cluster_stress failed"
    }
} finally {
    Pop-Location
}

$peerIDs = @{}
for ($nodeIndex = 1; $nodeIndex -le $nodeCount; $nodeIndex++) {
    $peerSeed = "$nodeCount-node-fault-peer-$nodeIndex"
    $peerID = (& $posNodeBinary -print-peer-id $peerSeed).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($peerID)) {
        throw "failed to derive peer ID for node $nodeIndex"
    }
    $peerIDs[$nodeIndex] = $peerID
}

$chainID = "local-$nodeCount-node-fault-recovery"
$genesisStartUnixMillis = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$genesisValidators = @()
for ($nodeIndex = 1; $nodeIndex -le $nodeCount; $nodeIndex++) {
    $genesisValidators += [ordered]@{
        staker_seed = "$nodeCount-node-fault-staker-$nodeIndex"
        validator_seed = "$nodeCount-node-fault-validator-$nodeIndex"
        consensus_seed = "$nodeCount-node-fault-consensus-$nodeIndex"
        peer_id = $peerIDs[$nodeIndex]
        stake_lamports = 10000000
        commission_bps = 0
    }
}

$fundedAccounts = @(
    [ordered]@{ seed = "$nodeCount-node-fault-founder"; lamports = 1000000000 }
)
foreach ($businessUserSeed in $businessUserSeeds) {
    $fundedAccounts += [ordered]@{ seed = $businessUserSeed; lamports = 1000000000 }
}

for ($nodeIndex = 1; $nodeIndex -le $nodeCount; $nodeIndex++) {
    $bootstrapPeers = @()
    if ($nodeIndex -ne 1) {
        $bootstrapPeers += [ordered]@{
            peer_id = $peerIDs[1]
            ip = "127.0.0.1"
            port = $BaseP2PPort
            network = "tcp"
            role = "bootnode"
            roles = @("bootnode")
            capabilities = @("relay", "dht")
            rpc_url = "http://127.0.0.1:$BaseRpcPort/"
        }
    }
    $config = [ordered]@{
        node_mode = "fullnode"
        chain_id = $chainID
        environment = "local"
        production = $false
        node_name = "fault-node-$nodeIndex"
        data_path = (Join-Path $dataDirectory "node$nodeIndex")
        listen_ip = "127.0.0.1"
        listen_port = $BaseP2PPort + $nodeIndex - 1
        network = "tcp"
        advertised_ip = "127.0.0.1"
        advertised_port = $BaseP2PPort + $nodeIndex - 1
        rpc_enabled = $true
        rpc_listen_ip = "127.0.0.1"
        rpc_port = $BaseRpcPort + $nodeIndex - 1
        allow_insecure_p2p = $false
        peer_seed = "$nodeCount-node-fault-peer-$nodeIndex"
        staker_seed = "$nodeCount-node-fault-staker-$nodeIndex"
        validator_seed = "$nodeCount-node-fault-validator-$nodeIndex"
        consensus_seed = "$nodeCount-node-fault-consensus-$nodeIndex"
        node_features = [ordered]@{
            bootnode_enabled = ($nodeIndex -eq 1)
            wallet_pairing_enabled = $false
        }
        validator_enabled = $true
        consensus_enabled = $true
        auto_register = $false
        stake_lamports = 10000000
        slot_millis = $SlotMillis
        epoch_slots = 64
        finality_depth = 2
        turbine_fanout = 2
        transaction_leader_forward_slots = 32
        genesis_start_unix_millis = $genesisStartUnixMillis
        consensus_policy = [ordered]@{
            multi_validator_threshold = $quorumRequired
        }
        emergency_recovery = [ordered]@{
            enabled = $true
        }
        bootstrap_peers = $bootstrapPeers
        genesis = [ordered]@{
            initial_supply_lamports = 100000000000000000
            funded_accounts = $fundedAccounts
            initial_validators = $genesisValidators
        }
    }
    Write-Utf8Json -Path (Join-Path $configDirectory "node$nodeIndex.json") -Value $config
}

$testSucceeded = $false
try {
    foreach ($nodeIndex in 1..$nodeCount) {
        Start-Node -NodeIndex $nodeIndex -Phase "initial"
    }

    $healthy = @(Wait-ForHealthyProgress -MinimumHeight 3 -Phase "healthy-before-faults")
    $minimumHealthyHeight = ($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum

    $healthyBusinessReport = Invoke-FixedTransferBatch `
        -NodeIndexes (1..$nodeCount) `
        -TransactionCount 16 `
        -Phase "healthy-transfers"
    [void](Wait-ForFinalizedTransactions `
        -NodeIndexes (1..$nodeCount) `
        -Signatures ([string[]]$healthyBusinessReport.transaction_signatures) `
        -Phase "healthy-transfers")
    $healthy = @(Wait-ForHealthyProgress `
        -MinimumHeight ($minimumHealthyHeight + 2) `
        -Phase "healthy-transfers")
    $phaseResults.Add([pscustomobject]@{
        Phase = "healthy-transfers-result"
        Expected = "transactions_finalized_on_all_nodes"
        TransferSucceeded = [uint64]$healthyBusinessReport.transfer_succeeded
        Statuses = $healthy
    }) | Out-Null
    $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)

    $healthy = @(Invoke-LeaderTimeoutScenario -HealthyStatuses $healthy)
    $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)

    foreach ($scenario in $availabilityScenarios) {
        $faultGroup = [int[]]$scenario.Offline
        foreach ($nodeIndex in $faultGroup) {
            Stop-Node -NodeIndex $nodeIndex
        }
        $onlineNodes = @(1..$nodeCount | Where-Object { $faultGroup -notcontains $_ })
        if ($onlineNodes.Count -eq 0) {
            if ($processes.Count -ne 0) {
                throw "$($scenario.Name) still has tracked node processes"
            }
            Start-Sleep -Seconds 2
            $phaseResults.Add([pscustomobject]@{
                Phase = [string]$scenario.Name
                Expected = "complete_outage"
                PersistedHeightBeforeOutage = $minimumHealthyHeight
                Statuses = @()
            }) | Out-Null
            $observedHeight = $minimumHealthyHeight
        } elseif ([bool]$scenario.ExpectProgress) {
            $observed = @(Wait-ForQuorumProgress `
                -OnlineNodeIndexes $onlineNodes `
                -MinimumHeight ($minimumHealthyHeight + 3) `
                -Phase ([string]$scenario.Name))
            $observedHeight = [uint64](($observed | Measure-Object -Property HeadHeight -Maximum).Maximum)
        } else {
            $observed = @(Wait-ForFaultHalt `
                -OnlineNodeIndexes $onlineNodes `
                -Phase ([string]$scenario.Name))
            $observedHeight = [uint64](($observed | Measure-Object -Property HeadHeight -Maximum).Maximum)
        }

        foreach ($nodeIndex in $faultGroup) {
            Start-Node -NodeIndex $nodeIndex -Phase "recovery-$($scenario.Name)"
        }
        $healthy = @(Wait-ForHealthyProgress `
            -MinimumHeight ($observedHeight + 3) `
            -Phase "recovery-$($scenario.Name)")
        $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)
    }

    foreach ($scenario in $partitionScenarios) {
        $healthy = @(Invoke-NetworkPartitionScenario `
            -Scenario $scenario `
            -MinimumHealthyHeight $minimumHealthyHeight)
        $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)
    }

    foreach ($nodeIndex in 1..$nodeCount) {
        $beforeRestartHeight = $minimumHealthyHeight
        Stop-Node -NodeIndex $nodeIndex
        Start-Node -NodeIndex $nodeIndex -Phase "rolling-restart-$nodeIndex"
        $healthy = @(Wait-ForHealthyProgress `
            -MinimumHeight ($beforeRestartHeight + 1) `
            -Phase "rolling-restart-$nodeIndex")
        $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)
    }

    if ($Adversarial) {
        $healthy = @(Invoke-AdversarialScenarios -HealthyStatuses $healthy)
        $minimumHealthyHeight = [uint64](($healthy | Measure-Object -Property HeadHeight -Minimum).Minimum)
    }

    $report = [ordered]@{
        succeeded = $true
        node_count = $nodeCount
        adversarial = [bool]$Adversarial
        confirm_quorum_required_validators = $quorumRequired
        timeout_quorum_required_validators = $quorumRequired
        multi_validator_threshold = $quorumRequired
        availability_scenarios = $availabilityScenarios.Count
        partition_scenarios = $partitionScenarios.Count
        rolling_restarts = $nodeCount
        final_minimum_head_height = $minimumHealthyHeight
        generated_at = (Get-Date).ToString("o")
        phases = $phaseResults.ToArray()
    }
    Write-Utf8Json -Path $reportPath -Value $report
    $testSucceeded = $true
    Write-Host "$nodeCount-node fault recovery test passed."
    Write-Host "Report: $reportPath"
} catch {
    $report = [ordered]@{
        succeeded = $false
        node_count = $nodeCount
        adversarial = [bool]$Adversarial
        multi_validator_threshold = $quorumRequired
        availability_scenarios = $availabilityScenarios.Count
        partition_scenarios = $partitionScenarios.Count
        error = $_.Exception.Message
        generated_at = (Get-Date).ToString("o")
        phases = $phaseResults.ToArray()
    }
    Write-Utf8Json -Path $reportPath -Value $report
    throw
} finally {
    if (-not $KeepRunning -or -not $testSucceeded) {
        Stop-AllNodes
    }
}
