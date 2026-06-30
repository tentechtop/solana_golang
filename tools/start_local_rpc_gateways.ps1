[CmdletBinding()]
param(
    [int[]]$RpcPorts = @(9110, 9111, 9112, 9113),
    [int]$BaseP2PPort = 5110,
    [string]$AdvertisedIP = "192.168.121.225",
    [string]$SourceRoot = "",
    [string]$OutputRoot = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-RepoRoot {
    # 功能目的：定位仓库根目录；实现原因：脚本需要从任意工作目录稳定执行
    $repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
    return $repoRoot
}

function Resolve-DefaultPath {
    param([string]$RepoRoot, [string]$Value, [string]$DefaultRelativePath)
    # 功能目的：解析可选路径参数；实现原因：默认产物目录和自定义目录都要支持
    if (-not ([string]::IsNullOrWhiteSpace($Value))) {
        $customPath = [System.IO.Path]::GetFullPath($Value)
        return $customPath
    }
    $defaultPath = [System.IO.Path]::GetFullPath((Join-Path $RepoRoot $DefaultRelativePath))
    return $defaultPath
}

function Assert-RpcInput {
    param([int[]]$Ports, [int]$P2PPortBase, [string]$HostIP)
    # 功能目的：校验端口和地址边界；实现原因：提前拒绝非法配置避免启动半成功
    if ($Ports.Count -lt 1 -or $Ports.Count -gt 16) {
        throw "RpcPorts count must be 1..16"
    }

    $uniquePorts = @($Ports | Select-Object -Unique)
    if ($uniquePorts.Count -ne $Ports.Count) {
        throw "RpcPorts contains duplicates"
    }

    foreach ($port in $Ports) {
        if ($port -lt 1 -or $port -gt 65535) {
            throw "RPC port is invalid: $port"
        }
    }

    if ($P2PPortBase -lt 1 -or ($P2PPortBase + $Ports.Count - 1) -gt 65535) {
        throw "P2P port range is invalid"
    }

    $parsedIP = [System.Net.IPAddress]$null
    if (-not ([System.Net.IPAddress]::TryParse($HostIP, [ref]$parsedIP))) {
        throw "AdvertisedIP must be a valid IP address"
    }
}

function Get-ListeningPortOwner {
    param([int]$Port)
    # 功能目的：查询监听端口归属；实现原因：启动前必须避免端口冲突
    return Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
        Where-Object { $_.LocalPort -eq $Port } |
        Select-Object -First 1
}

function Write-Utf8NoBomFile {
    param([string]$Path, [string]$Text)
    # 功能目的：写入无 BOM 文本；实现原因：Go JSON 配置跨平台读取更稳定
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function New-GatewayConfig {
    param([object]$TemplateConfig, [string]$NodeName, [string]$HostIP, [int]$P2PPort, [int]$RpcPort)
    # 功能目的：生成独立 RPC 网关配置；实现原因：每个网关必须独立节点名、端口和 peer seed
    $config = $TemplateConfig | ConvertTo-Json -Depth 32 | ConvertFrom-Json
    $config.node_name = $NodeName
    $config.advertised_ip = $HostIP
    $config.listen_port = $P2PPort
    $config.peer_seed = "$NodeName-peer"
    $config.rpc_port = $RpcPort
    return $config
}

function Start-RpcGateway {
    param([string]$BinaryPath, [string]$ConfigPath, [string]$StdoutPath, [string]$StderrPath)
    # 功能目的：后台启动 rpcnode；实现原因：RPC 网关需要独立进程长期监听
    return Start-Process `
        -FilePath $BinaryPath `
        -ArgumentList @("-config", $ConfigPath) `
        -RedirectStandardOutput $StdoutPath `
        -RedirectStandardError $StderrPath `
        -WindowStyle Hidden `
        -PassThru
}

$repoRoot = Resolve-RepoRoot
$sourceRootPath = Resolve-DefaultPath -RepoRoot $repoRoot -Value $SourceRoot -DefaultRelativePath "dist\local-rpc-9110"
$outputRootPath = Resolve-DefaultPath -RepoRoot $repoRoot -Value $OutputRoot -DefaultRelativePath "dist"
Assert-RpcInput -Ports $RpcPorts -P2PPortBase $BaseP2PPort -HostIP $AdvertisedIP

$sourceConfigPath = Join-Path $sourceRootPath "config\local-rpc-9110.json"
$sourceBinaryPath = Join-Path $sourceRootPath "bin\rpcnode.exe"
if (-not (Test-Path -LiteralPath $sourceConfigPath)) {
    throw "Source config not found: $sourceConfigPath"
}
if (-not (Test-Path -LiteralPath $sourceBinaryPath)) {
    throw "Source binary not found: $sourceBinaryPath"
}

$templateConfig = Get-Content -Path $sourceConfigPath -Raw -Encoding UTF8 | ConvertFrom-Json

for ($index = 0; $index -lt $RpcPorts.Count; $index++) {
    $rpcPort = $RpcPorts[$index]
    $p2pPort = $BaseP2PPort + $index
    $rpcOwner = Get-ListeningPortOwner -Port $rpcPort
    if ($null -ne $rpcOwner) {
        Write-Host "skip rpc $rpcPort already listening by pid $($rpcOwner.OwningProcess)"
        continue
    }

    $p2pOwner = Get-ListeningPortOwner -Port $p2pPort
    if ($null -ne $p2pOwner) {
        throw "P2P port $p2pPort already listening by pid $($p2pOwner.OwningProcess)"
    }

    $nodeName = "local-rpc-gateway-$rpcPort"
    $targetRoot = Join-Path $outputRootPath "local-rpc-$rpcPort"
    $binDir = Join-Path $targetRoot "bin"
    $configDir = Join-Path $targetRoot "config"
    $logDir = Join-Path $targetRoot "logs"
    $runDir = Join-Path $targetRoot "run"
    New-Item -ItemType Directory -Path $binDir, $configDir, $logDir, $runDir -Force | Out-Null

    $binaryPath = Join-Path $binDir "rpcnode.exe"
    Copy-Item -LiteralPath $sourceBinaryPath -Destination $binaryPath -Force

    $config = New-GatewayConfig -TemplateConfig $templateConfig -NodeName $nodeName -HostIP $AdvertisedIP -P2PPort $p2pPort -RpcPort $rpcPort
    $configPath = Join-Path $configDir "$nodeName.json"
    Write-Utf8NoBomFile -Path $configPath -Text ($config | ConvertTo-Json -Depth 32)

    $stdoutPath = Join-Path $logDir "$nodeName.out.log"
    $stderrPath = Join-Path $logDir "$nodeName.err.log"
    $process = Start-RpcGateway -BinaryPath $binaryPath -ConfigPath $configPath -StdoutPath $stdoutPath -StderrPath $stderrPath
    Write-Utf8NoBomFile -Path (Join-Path $runDir "$nodeName.pid") -Text ([string]$process.Id)
    Write-Host "started $nodeName pid=$($process.Id) rpc=$rpcPort p2p=$p2pPort"
}
