[CmdletBinding()]
param(
    [string]$OutputRoot = "",
    [int]$BaseP2PPort = 16301,
    [int]$BaseRpcPort = 19991,
    [int]$SlotMillis = 350,
    [int]$EpochSlots = 8,
    [int]$TimeoutSeconds = 120,
    [switch]$KeepRunning
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$validatorNodeCount = 8
$joiningNodeIndex = 9
$processes = @{}

function Resolve-TestRoot {
    param([string]$RepositoryRoot, [string]$RequestedRoot)

    $distRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot "dist"))
    if ([string]::IsNullOrWhiteSpace($RequestedRoot)) {
        $suffix = (Get-Date).ToString("yyyyMMdd-HHmmss")
        return [System.IO.Path]::GetFullPath((Join-Path $distRoot "new-node-sync-$suffix"))
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
}

function Get-NodeStatus {
    param([int]$NodeIndex)

    return Invoke-NodeRpc -NodeIndex $NodeIndex -Method "getNodeStatus"
}

function Wait-Until {
    param([scriptblock]$Predicate, [string]$Description)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $lastError = ""
    while ((Get-Date) -lt $deadline) {
        try {
            $value = & $Predicate
            if ($null -ne $value -and $value -ne $false) {
                return $value
            }
            $lastError = "predicate not satisfied"
        } catch {
            $lastError = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Timed out waiting for $Description. Last observation: $lastError"
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
        Stop-Process -Id $process.Id -Force
        $process.WaitForExit(5000) | Out-Null
    }
    $processes.Remove($NodeIndex)
}

function Stop-AllNodes {
    foreach ($nodeIndex in @($processes.Keys)) {
        Stop-Node -NodeIndex ([int]$nodeIndex)
    }
}

if ($BaseP2PPort -lt 1 -or $BaseP2PPort + $joiningNodeIndex - 1 -gt 65535) {
    throw "invalid P2P port range"
}
if ($BaseRpcPort -lt 1 -or $BaseRpcPort + $joiningNodeIndex - 1 -gt 65535) {
    throw "invalid RPC port range"
}
if ($EpochSlots -lt 4) {
    throw "EpochSlots must be at least 4"
}

$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$testRoot = Resolve-TestRoot -RepositoryRoot $repositoryRoot -RequestedRoot $OutputRoot
$binDirectory = Join-Path $testRoot "bin"
$configDirectory = Join-Path $testRoot "config"
$dataDirectory = Join-Path $testRoot "data"
$logDirectory = Join-Path $testRoot "logs"
New-Item -ItemType Directory -Path $binDirectory, $configDirectory, $dataDirectory, $logDirectory -Force | Out-Null
$posNodeBinary = Join-Path $binDirectory "posnode.exe"
$reportPath = Join-Path $testRoot "new-node-sync-report.json"

Push-Location $repositoryRoot
try {
    & go build -o $posNodeBinary .\cmd\posnode
    if ($LASTEXITCODE -ne 0) {
        throw "go build .\cmd\posnode failed"
    }
} finally {
    Pop-Location
}

$peerIDs = @{}
foreach ($nodeIndex in 1..$joiningNodeIndex) {
    $peerSeed = "new-node-sync-peer-$nodeIndex"
    $peerID = (& $posNodeBinary -print-peer-id $peerSeed).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($peerID)) {
        throw "failed to derive peer ID for node $nodeIndex"
    }
    $peerIDs[$nodeIndex] = $peerID
}

$chainID = "local-new-node-sync"
$genesisStartUnixMillis = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$genesisValidators = @(
    [ordered]@{
        staker_seed = "new-node-sync-staker-1"
        validator_seed = "new-node-sync-validator-1"
        consensus_seed = "new-node-sync-consensus-1"
        peer_id = $peerIDs[1]
        stake_lamports = 10000000
        commission_bps = 0
    }
)
$fundedAccounts = @(
    [ordered]@{ seed = "new-node-sync-founder"; lamports = 1000000000 }
)
foreach ($nodeIndex in 2..$validatorNodeCount) {
    $fundedAccounts += [ordered]@{
        seed = "new-node-sync-staker-$nodeIndex"
        lamports = 100000000
    }
}

foreach ($nodeIndex in 1..$joiningNodeIndex) {
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
    if ($nodeIndex -eq $joiningNodeIndex) {
        $bootstrapPeers += [ordered]@{
            peer_id = $peerIDs[2]
            ip = "127.0.0.1"
            port = $BaseP2PPort + 1
            network = "tcp"
            role = "bootnode"
            roles = @("bootnode")
            capabilities = @("relay", "dht")
            rpc_url = "http://127.0.0.1:$($BaseRpcPort + 1)/"
        }
    }
    $isValidator = $nodeIndex -le $validatorNodeCount
    $isArchiveBootstrap = $nodeIndex -eq 1
    $config = [ordered]@{
        node_mode = "fullnode"
        chain_id = $chainID
        environment = "local"
        production = $false
        node_name = "new-node-sync-$nodeIndex"
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
        peer_seed = "new-node-sync-peer-$nodeIndex"
        node_features = [ordered]@{
            bootnode_enabled = ($nodeIndex -eq 1 -or $nodeIndex -eq 2)
            archive_enabled = $isArchiveBootstrap
            wallet_pairing_enabled = $false
        }
        validator_enabled = $isValidator
        consensus_enabled = $isValidator
        auto_register = ($nodeIndex -ge 2 -and $nodeIndex -le $validatorNodeCount)
        stake_lamports = 10000000
        slot_millis = $SlotMillis
        epoch_slots = $EpochSlots
        finality_depth = 2
        turbine_fanout = 2
        transaction_leader_forward_slots = 16
        genesis_start_unix_millis = $genesisStartUnixMillis
        consensus_policy = [ordered]@{
            multi_validator_threshold = $validatorNodeCount
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
    if ($isValidator) {
        $config.staker_seed = "new-node-sync-staker-$nodeIndex"
        $config.validator_seed = "new-node-sync-validator-$nodeIndex"
        $config.consensus_seed = "new-node-sync-consensus-$nodeIndex"
    }
    Write-Utf8Json -Path (Join-Path $configDirectory "node$nodeIndex.json") -Value $config
}

$testSucceeded = $false
try {
    foreach ($nodeIndex in 1..$validatorNodeCount) {
        Start-Node -NodeIndex $nodeIndex -Phase "initial"
    }

    $baseline = Wait-Until -Description "eight validators to activate across epochs" -Predicate {
        $status = Get-NodeStatus -NodeIndex 1
        $currentEpochStartSlot = [uint64]$status.epoch_id * [uint64]$EpochSlots + 1
        if ([int]$status.validator_count -ne $validatorNodeCount -or
            [string]$status.consensus_mode -ne "multi_validator" -or
            -not [bool]$status.liveness.quorum_ready -or
            [uint64]$status.finalized_height -lt ([uint64]($EpochSlots * 2)) -or
            [uint64]$status.finalized_slot -lt ($currentEpochStartSlot + 2)) {
            return $false
        }
        return [pscustomobject]@{
            MinimumFinalizedHeight = [uint64]$status.finalized_height
            HeadHeight = [uint64]$status.head_height
            HeadHash = [string]$status.head_hash
            FinalizedHeight = [uint64]$status.finalized_height
            FinalizedSlot = [uint64]$status.finalized_slot
            FinalizedHash = [string]$status.finalized_hash
            EpochID = [uint64]$status.epoch_id
            ValidatorCount = [int]$status.validator_count
        }
    }

    Stop-Node -NodeIndex 1
    Start-Node -NodeIndex $joiningNodeIndex -Phase "fresh-join"
    $joined = Wait-Until -Description "fresh node to import finalized state and catch up" -Predicate {
        $reference = Get-NodeStatus -NodeIndex 2
        $candidate = Get-NodeStatus -NodeIndex $joiningNodeIndex
        if ([uint64]$candidate.finalized_height -lt [uint64]$baseline.MinimumFinalizedHeight -or
            [int]$candidate.validator_count -ne $validatorNodeCount -or
            [uint64]$reference.head_height -gt [uint64]$candidate.head_height + 2) {
            return $false
        }
        $candidateBlock = Invoke-NodeRpc -NodeIndex $joiningNodeIndex -Method "getBlock" -Params @([uint64]$candidate.finalized_slot)
        $referenceBlock = Invoke-NodeRpc -NodeIndex 2 -Method "getBlock" -Params @([uint64]$candidate.finalized_slot)
        if ([string]$candidateBlock.blockhash -ne [string]$referenceBlock.blockhash) {
            throw "fresh node finalized fork at slot $($candidate.finalized_slot)"
        }
        return [pscustomobject]@{
            HeadHeight = [uint64]$candidate.head_height
            HeadHash = [string]$candidate.head_hash
            FinalizedHeight = [uint64]$candidate.finalized_height
            FinalizedHash = [string]$candidate.finalized_hash
            EpochID = [uint64]$candidate.epoch_id
            ValidatorCount = [int]$candidate.validator_count
            ReferenceHeadHeight = [uint64]$reference.head_height
        }
    }

    $joiningLogPath = Join-Path $logDirectory "node$joiningNodeIndex-fresh-join.out.log"
    $epochProofChainSynced = Select-String `
        -LiteralPath $joiningLogPath `
        -SimpleMatch "posnode epoch proof chain synced" `
        -Quiet
	$snapshotImported = Select-String `
        -LiteralPath $joiningLogPath `
        -SimpleMatch "posnode finalized snapshot synced" `
        -Quiet
    if (-not $epochProofChainSynced -or -not $snapshotImported) {
        throw "fresh node did not demonstrate epoch-proof verification followed by finalized snapshot import"
    }

    $report = [ordered]@{
        succeeded = $true
        validator_node_count = $validatorNodeCount
        joining_node_index = $joiningNodeIndex
        dynamic_validator_activation = $true
        primary_bootnode_offline_during_join = $true
		non_archive_sync_source_node_index = 2
		epoch_proof_chain_observed = $true
		verified_snapshot_import_observed = $true
        baseline = $baseline
        joined = $joined
        generated_at = (Get-Date).ToString("o")
    }
    Write-Utf8Json -Path $reportPath -Value $report
    $testSucceeded = $true
    Write-Host "Fresh node synchronization test passed."
    Write-Host "Report: $reportPath"
} catch {
    $report = [ordered]@{
        succeeded = $false
        validator_node_count = $validatorNodeCount
        joining_node_index = $joiningNodeIndex
        dynamic_validator_activation = $true
        error = $_.Exception.Message
        generated_at = (Get-Date).ToString("o")
    }
    Write-Utf8Json -Path $reportPath -Value $report
    throw
} finally {
    if (-not $KeepRunning -or -not $testSucceeded) {
        Stop-AllNodes
    }
}
