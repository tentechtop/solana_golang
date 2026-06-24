param(
    [string]$SourceManifestPath = ".\deploy\generated-4\manifest.json",
    [string]$OutputDirectory = ".\deploy\generated-4-public",
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
    $resolvedPath = Resolve-ProjectPath -PathText $PathText
    return Get-Content -Path $resolvedPath -Raw -Encoding UTF8 | ConvertFrom-Json
}

function Write-JsonFile {
    param(
        [string]$PathText,
        [object]$Value
    )
    $jsonText = $Value | ConvertTo-Json -Depth 100
    $encoding = [System.Text.UTF8Encoding]::new($false)
    [System.IO.File]::WriteAllText((Resolve-ProjectPath -PathText $PathText), $jsonText + [Environment]::NewLine, $encoding)
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

function Copy-JsonObject {
    param([object]$Value)
    return $Value | ConvertTo-Json -Depth 100 | ConvertFrom-Json
}

function New-ValidatorPeer {
    param(
        [object]$Validator,
        [string]$PeerHost,
        [string]$PeerNetwork
    )
    return [pscustomobject]([ordered]@{
        peer_id = $Validator.peer_id
        ip = $PeerHost
        port = [int]$Validator.p2p_port
        network = $PeerNetwork
        role = "validator"
        roles = @("validator", "full")
        capabilities = @("validator", "relay", "state_sync", "dht")
    })
}

function New-ValidatorDataPath {
    param([int]$Index)
    return "$RemoteRoot/data/posnode-4v-public-{0:D2}" -f $Index
}

$sourceManifest = Read-JsonFile -PathText $SourceManifestPath
$outputPath = Resolve-ProjectPath -PathText $OutputDirectory
New-Item -Path $outputPath -ItemType Directory -Force | Out-Null

$validatorPeers = @($sourceManifest.validators | ForEach-Object {
    New-ValidatorPeer -Validator $_ -PeerHost $PublicHost -PeerNetwork $Network
})

$outputValidators = @()
for ($index = 0; $index -lt $sourceManifest.validators.Count; $index++) {
    $validator = $sourceManifest.validators[$index]
    $validatorNumber = $index + 1
    $sourceConfig = Read-JsonFile -PathText $validator.config_path
    $targetConfig = Copy-JsonObject -Value $sourceConfig
    $targetConfigName = "posnode-public-{0:D2}.json" -f $validatorNumber
    $targetConfigPath = Join-Path -Path $OutputDirectory -ChildPath $targetConfigName
    $relativeTargetConfigPath = $targetConfigPath.Replace("\", "/").TrimStart(".", "/")
    $remoteConfigPath = "$RemoteRoot/config/$targetConfigName"
    $dataPath = New-ValidatorDataPath -Index $validatorNumber
    $rpcUrl = "http://$PublicHost`:$($targetConfig.rpc_port)/"

    Set-JsonProperty -Target $targetConfig -Name "data_path" -Value $dataPath
    Set-JsonProperty -Target $targetConfig -Name "network" -Value $Network
    Set-JsonProperty -Target $targetConfig -Name "advertised_ip" -Value $PublicHost
    Set-JsonProperty -Target $targetConfig -Name "advertised_port" -Value ([int]$validator.p2p_port)
    Set-JsonProperty -Target $targetConfig -Name "treasury_key_path" -Value "$RemoteRoot/config/genesis-access-treasury.json"

    $otherPeers = @($validatorPeers | Where-Object { $_.peer_id -ne $validator.peer_id } | ForEach-Object {
        Copy-JsonObject -Value $_
    })
    Set-JsonProperty -Target $targetConfig -Name "bootstrap_peers" -Value $otherPeers
    Write-JsonFile -PathText $targetConfigPath -Value $targetConfig

    $outputValidators += [pscustomobject]([ordered]@{
        name = $validator.name
        host_group = "linux-public"
        config_path = $relativeTargetConfigPath
        remote_config = $remoteConfigPath
        data_path = $dataPath
        peer_seed = $validator.peer_seed
        peer_id = $validator.peer_id
        staker_seed = $validator.staker_seed
        staker_address = $validator.staker_address
        validator_seed = $validator.validator_seed
        validator_address = $validator.validator_address
        consensus_seed = $validator.consensus_seed
        validator_id = $validator.validator_id
        advertised_ip = $PublicHost
        p2p_port = [int]$validator.p2p_port
        rpc_url = $rpcUrl
        p2p_multiaddress = "/ip4/$PublicHost/$Network/$($validator.p2p_port)/p2p/$($validator.peer_id)"
        role = "validator"
    })
}

$outputManifestFields = [ordered]@{}
$outputManifestFields["generated_at_unix_millis"] = ([DateTimeOffset]::UtcNow).ToUnixTimeMilliseconds()
$outputManifestFields["chain_id"] = $sourceManifest.chain_id
$outputManifestFields["genesis_start_unix_millis"] = $sourceManifest.genesis_start_unix_millis
$outputManifestFields["slot_millis"] = $sourceManifest.slot_millis
$outputManifestFields["epoch_slots"] = $sourceManifest.epoch_slots
$outputManifestFields["validator_stake_lamports"] = $sourceManifest.validator_stake_lamports
$outputManifestFields["public_host"] = $PublicHost
$outputManifestFields["network"] = $Network
$outputManifestFields["source_manifest"] = $SourceManifestPath.Replace("\", "/")
$outputManifestFields["validators"] = @($outputValidators)
$outputManifestFields["rpc_urls"] = @($outputValidators | ForEach-Object { $_.rpc_url })
$outputManifestFields["p2p_multiaddresses"] = @($outputValidators | ForEach-Object { $_.p2p_multiaddress })
$outputManifest = [pscustomobject]$outputManifestFields

Write-JsonFile -PathText (Join-Path -Path $OutputDirectory -ChildPath "manifest.json") -Value $outputManifest
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
[System.IO.File]::WriteAllLines((Resolve-ProjectPath -PathText (Join-Path -Path $OutputDirectory -ChildPath "rpc_urls.txt")), @($outputManifest.rpc_urls), $utf8NoBom)
[System.IO.File]::WriteAllLines((Resolve-ProjectPath -PathText (Join-Path -Path $OutputDirectory -ChildPath "p2p_multiaddresses.txt")), @($outputManifest.p2p_multiaddresses), $utf8NoBom)

Write-Host "generated public validator configs: $OutputDirectory"
