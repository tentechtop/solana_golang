param(
    [string]$ManifestPath = "deploy/generated-4/manifest.json",
    [string[]]$NodeName = @(),
    [string]$HostGroup = "",
    [Int64]$ProposalDelayMillis = 0,
    [switch]$DoubleVoteOnce,
    [switch]$DoubleProposalOnce,
    [switch]$Clear
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

function Write-Utf8Json {
    param(
        [Parameter(Mandatory = $true)] [string]$Path,
        [Parameter(Mandatory = $true)] $Value
    )
    $resolvedPath = [System.IO.Path]::GetFullPath($Path)
    $json = $Value | ConvertTo-Json -Depth 100
    [System.IO.File]::WriteAllText($resolvedPath, $json + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
}

function Remove-JsonPropertyIfExists {
    param(
        [Parameter(Mandatory = $true)] $Object,
        [Parameter(Mandatory = $true)] [string]$Name
    )
    if ($Object.PSObject.Properties.Name -contains $Name) {
        $Object.PSObject.Properties.Remove($Name)
    }
}

if ($ProposalDelayMillis -lt 0) {
    throw "ProposalDelayMillis must be non-negative"
}
if ($NodeName.Count -eq 0 -and [string]::IsNullOrWhiteSpace($HostGroup)) {
    throw "NodeName or HostGroup is required"
}

$manifestFullPath = [System.IO.Path]::GetFullPath($ManifestPath)
$manifest = Get-Content -LiteralPath $manifestFullPath -Raw -Encoding UTF8 | ConvertFrom-Json
$manifestRoot = Split-Path -Parent $manifestFullPath
$selectedNodes = @($manifest.validators | Where-Object {
    $nameMatched = $NodeName.Count -gt 0 -and $NodeName -contains $_.name
    $hostMatched = -not [string]::IsNullOrWhiteSpace($HostGroup) -and $_.host_group -eq $HostGroup
    $nameMatched -or $hostMatched
})
if ($selectedNodes.Count -eq 0) {
    throw "No validator matched the requested fault target"
}

foreach ($node in $selectedNodes) {
    $configPath = [string]$node.config_path
    if (-not [System.IO.Path]::IsPathRooted($configPath)) {
        $configPath = Join-Path $manifestRoot (Split-Path -Leaf $configPath)
    }
    $configFullPath = [System.IO.Path]::GetFullPath($configPath)
    $config = Get-Content -LiteralPath $configFullPath -Raw -Encoding UTF8 | ConvertFrom-Json

    if ($Clear) {
        Remove-JsonPropertyIfExists -Object $config -Name "fault_injection"
        Write-Utf8Json -Path $configFullPath -Value $config
        Write-Host "cleared fault_injection for $($node.name)"
        continue
    }

    $fault = [ordered]@{}
    if ($ProposalDelayMillis -gt 0) {
        $fault["proposal_delay_millis"] = $ProposalDelayMillis
    }
    if ($DoubleVoteOnce) {
        $fault["double_vote_once"] = $true
    }
    if ($DoubleProposalOnce) {
        $fault["double_proposal_once"] = $true
    }
    if ($fault.Count -eq 0) {
        Remove-JsonPropertyIfExists -Object $config -Name "fault_injection"
    } elseif ($config.PSObject.Properties.Name -contains "fault_injection") {
        $config.fault_injection = [pscustomobject]$fault
    } else {
        $config | Add-Member -NotePropertyName "fault_injection" -NotePropertyValue ([pscustomobject]$fault)
    }
    Write-Utf8Json -Path $configFullPath -Value $config
    Write-Host "updated fault_injection for $($node.name)"
}
