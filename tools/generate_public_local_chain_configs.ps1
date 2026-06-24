param(
    [string]$OutputDirectory = ".\deploy\generated-4-public",
    [string]$LocalTemplateConfigPath = ".\deploy\wallet-pairing-fullnode-win.json",
    [string]$PublicHost = "101.35.87.31",
    [ValidateSet("tcp", "quic")]
    [string]$Network = "quic",
    [string]$RemoteRoot = "/opt/solana_golang"
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

function Read-JsonFile {
    param([string]$PathText)
    return Get-Content -Path (Resolve-ProjectPath -PathText $PathText) -Raw -Encoding UTF8 | ConvertFrom-Json
}

function Write-JsonFile {
    param(
        [string]$PathText,
        [object]$Value
    )
    $jsonText = $Value | ConvertTo-Json -Depth 100
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    [System.IO.File]::WriteAllText((Resolve-ProjectPath -PathText $PathText), $jsonText + [Environment]::NewLine, $utf8NoBom)
}

function Copy-JsonObject {
    param([object]$Value)
    return $Value | ConvertTo-Json -Depth 100 | ConvertFrom-Json
}

function Set-JsonProperty {
    param(
        [object]$Target,
        [string]$Name,
        [object]$Value
    )
    if ($Target.PSObject.Properties.Name -contains $Name) {
        $Target.$Name = $Value
        return
    }
    $Target | Add-Member -NotePropertyName $Name -NotePropertyValue $Value
}

function New-PeerConfig {
    param([object]$Node)
    $rpcUrl = "http://$PublicHost`:$($Node.rpc_port)/"
    return [pscustomobject]([ordered]@{
        peer_id = $Node.peer_id
        ip = $PublicHost
        port = [int]$Node.p2p_port
        network = $Network
        role = $Node.role
        roles = @($Node.roles)
        capabilities = @($Node.capabilities)
        rpc_url = $rpcUrl
    })
}

function New-NodeConfig {
    param(
        [object]$Template,
        [object]$Node,
        [object[]]$Peers
    )
    $config = Copy-JsonObject -Value $Template
    $config.node_name = $Node.name
    $config.data_path = $Node.data_path
    $config.listen_ip = "0.0.0.0"
    $config.listen_port = [int]$Node.p2p_port
    $config.advertised_ip = $PublicHost
    $config.advertised_port = [int]$Node.p2p_port
    $config.rpc_enabled = $true
    $config.rpc_listen_ip = "0.0.0.0"
    $config.rpc_port = [int]$Node.rpc_port
    Set-JsonProperty -Target $config -Name "network" -Value $Network
    $config.node_role = $Node.role
    $config.node_roles = @($Node.roles)
    $config.node_capabilities = @($Node.capabilities)
    $config.validator_enabled = [bool]$Node.validator_enabled
    $config.consensus_enabled = [bool]$Node.consensus_enabled
    $config.peer_seed = $Node.peer_seed
    Set-JsonProperty -Target $config -Name "treasury_key_path" -Value "$RemoteRoot/config/genesis-access-treasury.json"
    $config.bootstrap_peers = @($Peers | Where-Object { $_.peer_id -ne $Node.peer_id } | ForEach-Object {
        Copy-JsonObject -Value $_
    })

    if ($Node.validator_enabled) {
        Set-JsonProperty -Target $config -Name "staker_seed" -Value $Node.staker_seed
        Set-JsonProperty -Target $config -Name "validator_seed" -Value $Node.validator_seed
        Set-JsonProperty -Target $config -Name "consensus_seed" -Value $Node.consensus_seed
        Set-JsonProperty -Target $config -Name "stake_lamports" -Value ([int64]$Node.stake_lamports)
        return $config
    }

    foreach ($name in @("staker_seed", "validator_seed", "consensus_seed", "stake_lamports", "validator_pairing")) {
        if ($config.PSObject.Properties.Name -contains $name) {
            $config.PSObject.Properties.Remove($name)
        }
    }
    return $config
}

$template = Read-JsonFile -PathText $LocalTemplateConfigPath
$outputPath = Resolve-ProjectPath -PathText $OutputDirectory
New-Item -Path $outputPath -ItemType Directory -Force | Out-Null

$nodeRows = @(
    [pscustomobject]([ordered]@{
        name = "node-4v-01"
        role = "validator"
        roles = @("validator", "full")
        capabilities = @("validator", "relay", "state_sync", "dht")
        validator_enabled = $true
        consensus_enabled = $true
        peer_seed = "node-101"
        peer_id = "7w8LNHyGG77Mpue6JrkuEXpPqjcwQys8UvBM8cFJrm5A"
        staker_seed = "staker-101"
        validator_seed = "validator-101"
        consensus_seed = "consensus-101"
        stake_lamports = 10000000
        p2p_port = 5210
        rpc_port = 8910
        data_path = "$RemoteRoot/data/posnode-local-public-01"
    }),
    [pscustomobject]([ordered]@{
        name = "node-4v-02"
        role = "validator"
        roles = @("validator", "full")
        capabilities = @("validator", "relay", "state_sync", "dht")
        validator_enabled = $true
        consensus_enabled = $true
        peer_seed = "node-223"
        peer_id = "EPqwecxfEdeeB3ZtKCVVjEohLYZXQ1XHZapdkeBYirB1"
        staker_seed = "staker-223"
        validator_seed = "validator-223"
        consensus_seed = "consensus-223"
        stake_lamports = 10000000
        p2p_port = 5211
        rpc_port = 8911
        data_path = "$RemoteRoot/data/posnode-local-public-02"
    }),
    [pscustomobject]([ordered]@{
        name = "node-4v-03"
        role = "validator"
        roles = @("validator", "full")
        capabilities = @("validator", "relay", "state_sync", "dht")
        validator_enabled = $true
        consensus_enabled = $true
        peer_seed = "node-local-win"
        peer_id = "GVbQfQwJK1M1q1wVV6LnzLq9Nwvu3XSBvsJLBJAC935y"
        staker_seed = "staker-local-win"
        validator_seed = "validator-local-win"
        consensus_seed = "consensus-local-win"
        stake_lamports = 10000000
        p2p_port = 5212
        rpc_port = 8912
        data_path = "$RemoteRoot/data/posnode-local-public-03"
    }),
    [pscustomobject]([ordered]@{
        name = "node-4v-04"
        role = "full"
        roles = @("full")
        capabilities = @("relay", "state_sync", "dht")
        validator_enabled = $false
        consensus_enabled = $false
        peer_seed = "public-rpc-fullnode-04"
        peer_id = "8YLq7tpRgTvv7dp31pCc4XLXjXJRc46yg5TB1SXAUnkr"
        staker_seed = ""
        validator_seed = ""
        consensus_seed = ""
        stake_lamports = 0
        p2p_port = 5213
        rpc_port = 8913
        data_path = "$RemoteRoot/data/posnode-local-public-04"
    })
)

$peerRows = @($nodeRows | ForEach-Object { New-PeerConfig -Node $_ })
$manifestNodes = @()
for ($index = 0; $index -lt $nodeRows.Count; $index++) {
    $node = $nodeRows[$index]
    $nodeNumber = $index + 1
    $configName = "posnode-public-{0:D2}.json" -f $nodeNumber
    $configPath = Join-Path -Path $OutputDirectory -ChildPath $configName
    $remoteConfigPath = "$RemoteRoot/config/$configName"
    $rpcUrl = "http://$PublicHost`:$($node.rpc_port)/"
    $p2pAddress = "/ip4/$PublicHost/$Network/$($node.p2p_port)/p2p/$($node.peer_id)"
    $config = New-NodeConfig -Template $template -Node $node -Peers $peerRows
    Write-JsonFile -PathText $configPath -Value $config

    $manifestNodes += [pscustomobject]([ordered]@{
        name = $node.name
        host_group = "linux-public"
        config_path = $configPath.Replace("\", "/").TrimStart(".", "/")
        remote_config = $remoteConfigPath
        data_path = $node.data_path
        peer_seed = $node.peer_seed
        peer_id = $node.peer_id
        staker_seed = $node.staker_seed
        validator_seed = $node.validator_seed
        consensus_seed = $node.consensus_seed
        advertised_ip = $PublicHost
        p2p_port = [int]$node.p2p_port
        rpc_url = $rpcUrl
        p2p_multiaddress = $p2pAddress
        role = $node.role
    })
}

$manifest = [pscustomobject]([ordered]@{
    generated_at_unix_millis = ([DateTimeOffset]::UtcNow).ToUnixTimeMilliseconds()
    chain_id = $template.chain_id
    genesis_start_unix_millis = $template.genesis_start_unix_millis
    slot_millis = $template.slot_millis
    epoch_slots = $template.epoch_slots
    validator_stake_lamports = 10000000
    public_host = $PublicHost
    network = $Network
    local_template_config = $LocalTemplateConfigPath.Replace("\", "/")
    validators = @($manifestNodes)
    rpc_urls = @($manifestNodes | ForEach-Object { $_.rpc_url })
    p2p_multiaddresses = @($manifestNodes | ForEach-Object { $_.p2p_multiaddress })
})

Write-JsonFile -PathText (Join-Path -Path $OutputDirectory -ChildPath "manifest.json") -Value $manifest
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
[System.IO.File]::WriteAllLines((Resolve-ProjectPath -PathText (Join-Path -Path $OutputDirectory -ChildPath "rpc_urls.txt")), @($manifest.rpc_urls), $utf8NoBom)
[System.IO.File]::WriteAllLines((Resolve-ProjectPath -PathText (Join-Path -Path $OutputDirectory -ChildPath "p2p_multiaddresses.txt")), @($manifest.p2p_multiaddresses), $utf8NoBom)

Write-Host "generated public local-chain configs: $OutputDirectory"
