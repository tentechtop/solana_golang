[CmdletBinding()]
param(
    [int]$BaseRpcPort = 28991,
    [int]$BaseP2PPort = 19301,
    [ValidateRange(1000, 10000)][int]$SlotMillis = 1000,
    [ValidateRange(64, 100000)][int]$EpochSlots = 64
)

# Local, disposable identities only. Never joins or modifies an existing network.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$genesisProfile = Get-Content -LiteralPath (Join-Path $repositoryRoot 'config/local/genesis-account.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$genesisAddress = [string]$genesisProfile.raw_public_key
$genesisSupply = [uint64]$genesisProfile.initial_supply_lamports
$clusterId = 'ordexus-wallet-local-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
$clusterRoot = Join-Path $repositoryRoot "tmp/$clusterId"
$nodeCount = 4
foreach ($basePort in @($BaseRpcPort, $BaseP2PPort)) {
    if ($basePort -lt 1024 -or $basePort + $nodeCount - 1 -gt 65535) { throw 'Invalid port range' }
}
$ports = @($BaseRpcPort..($BaseRpcPort + 3)) + @($BaseP2PPort..($BaseP2PPort + 3))
if (@($ports | Select-Object -Unique).Count -ne 8) { throw 'RPC and P2P ranges overlap' }
$occupied = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $ports -contains $_.LocalPort })
if ($occupied.Count -gt 0) { throw "Ports already in use: $($occupied.LocalPort -join ', ')" }
if (Test-Path -LiteralPath $clusterRoot) { throw 'Cluster directory already exists' }
foreach ($name in @('bin', 'config', 'data', 'logs')) { New-Item -ItemType Directory -Path (Join-Path $clusterRoot $name) | Out-Null }
function Write-ClusterJson([string]$Path, [object]$Value) {
    [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 32) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}
$binary = Join-Path $clusterRoot 'bin/ordexus-node.exe'
$walletBinary = Join-Path $clusterRoot 'bin/wallet.exe'
$identityBinary = Join-Path $clusterRoot 'bin/posnode-identity.exe'
Push-Location $repositoryRoot
try {
    & go build -o $binary ./cmd/main.go ./cmd/node_identity.go
    if ($LASTEXITCODE -ne 0) { throw 'Node build failed' }
    & go build -o $walletBinary ./cmd/wallet
    if ($LASTEXITCODE -ne 0) { throw 'Wallet CLI build failed' }
    & go build -o $identityBinary ./cmd/posnode
    if ($LASTEXITCODE -ne 0) { throw 'Identity helper build failed' }
} finally { Pop-Location }
$peerIds = @{}
foreach ($index in 1..4) {
    $peerIdOutput = & $identityBinary -print-peer-id "$clusterId-peer-$index"
    if ($LASTEXITCODE -ne 0 -or -not $peerIdOutput) { throw 'Peer identity generation failed' }
    $peerIds[$index] = $peerIdOutput.Trim()
}
$validators = @(foreach ($index in 1..4) {
    [ordered]@{
        staker_seed = "$clusterId-staker-$index"; validator_seed = "$clusterId-validator-$index"
        consensus_seed = "$clusterId-consensus-$index"; peer_id = $peerIds[$index]
        stake_lamports = 100000000; commission_bps = 0
    }
})
$genesisTime = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
foreach ($index in 1..4) {
    $peers = @(foreach ($other in 1..4) {
        if ($other -eq $index) { continue }
        [ordered]@{
            peer_id = $peerIds[$other]; ip = '127.0.0.1'; port = $BaseP2PPort + $other - 1
            network = 'tcp'; role = 'bootnode'; roles = @('bootnode'); capabilities = @('relay', 'dht')
            rpc_url = "http://127.0.0.1:$($BaseRpcPort + $other - 1)/"
        }
    })
    $config = [ordered]@{
        node_mode = 'fullnode'; chain_id = $clusterId; environment = 'local'; production = $false
        node_name = "$clusterId-$index"; data_path = (Join-Path $clusterRoot "data/node$index")
        listen_ip = '127.0.0.1'; listen_port = $BaseP2PPort + $index - 1; network = 'tcp'
        advertised_ip = '127.0.0.1'; advertised_port = $BaseP2PPort + $index - 1
        rpc_enabled = $true; rpc_listen_ip = '127.0.0.1'; rpc_port = $BaseRpcPort + $index - 1
        allow_insecure_p2p = $false; peer_seed = "$clusterId-peer-$index"
        staker_seed = "$clusterId-staker-$index"; validator_seed = "$clusterId-validator-$index"
        consensus_seed = "$clusterId-consensus-$index"
        node_features = @{ bootnode_enabled = $true; wallet_pairing_enabled = $false }
        validator_enabled = $true; consensus_enabled = $true; auto_register = $false
        stake_lamports = 100000000; slot_millis = $SlotMillis; epoch_slots = $EpochSlots; finality_depth = 2
        turbine_fanout = 2; transaction_leader_forward_slots = 32
        genesis_start_unix_millis = $genesisTime
        consensus_policy = @{ multi_validator_threshold = 4 }
        emergency_recovery = @{ enabled = $true }; bootstrap_peers = $peers
        genesis = @{
            initial_supply_lamports = $genesisSupply
            treasury_address = $genesisAddress
            funded_accounts = @(@{ address = $genesisAddress; lamports = 1000000000000000 })
            initial_validators = $validators
        }
    }
    Write-ClusterJson (Join-Path $clusterRoot "config/node$index.json") $config
}
$started = @()
try {
    foreach ($index in 1..4) {
        $configPath = Join-Path $clusterRoot "config/node$index.json"
        $process = Start-Process -FilePath $binary -ArgumentList @('-config', ('"{0}"' -f $configPath)) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $clusterRoot "logs/node$index.out.log") -RedirectStandardError (Join-Path $clusterRoot "logs/node$index.err.log")
        $started += $process
    }
    $manifest = [ordered]@{
        chain_id = $clusterId; root = $clusterRoot; created_at = (Get-Date).ToString('o')
        node_binary = $binary; wallet_binary = $walletBinary
        genesis_wallet_address = [string]$genesisProfile.wallet_address; treasury_address = $genesisAddress
        initial_supply_lamports = [string]$genesisSupply
        nodes = @(foreach ($index in 1..4) { @{ index = $index; pid = $started[$index - 1].Id; rpc = "http://127.0.0.1:$($BaseRpcPort + $index - 1)/"; config = (Join-Path $clusterRoot "config/node$index.json") } })
    }
    Write-ClusterJson (Join-Path $clusterRoot 'manifest.json') $manifest
    Write-Output "Cluster manifest: $(Join-Path $clusterRoot 'manifest.json')"
    $manifest.nodes | Select-Object index, pid, rpc | Format-Table
} catch {
    foreach ($process in $started) { if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force } }
    throw
}
