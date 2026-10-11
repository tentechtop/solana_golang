param([switch]$SkipAndroid)

$ErrorActionPreference = 'Stop'
$coreRoot = Split-Path -Parent $PSScriptRoot
$outputDirectory = Join-Path $coreRoot 'dist\node-apps'
$androidProject = Join-Path $coreRoot 'products\android-node'
$mobileTools = Join-Path $coreRoot 'tmp\mobile-tools'
$androidSdk = if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { 'F:\DependencyLibrary\AndroidSdk' }
$androidNdk = if ($env:ANDROID_NDK_HOME) { $env:ANDROID_NDK_HOME } else { Join-Path $androidSdk 'ndk\27.1.12297006' }
$gradleCommand = if (Get-Command gradle -ErrorAction SilentlyContinue) { 'gradle' } else { 'F:\DependencyLibrary\gradle\gradle-8.11.1\bin\gradle.bat' }
$previousGoos, $previousGoarch, $previousCgo = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
$previousSdk, $previousNdk, $previousPath = $env:ANDROID_HOME, $env:ANDROID_NDK_HOME, $env:PATH

New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
Push-Location $coreRoot
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags '-H=windowsgui -s -w' -o (Join-Path $outputDirectory 'OrdexusNode.exe') ./cmd/node-app
    if ($LASTEXITCODE -ne 0) { throw 'Windows node application build failed' }
    $env:GOOS = $previousGoos; $env:GOARCH = $previousGoarch; $env:CGO_ENABLED = $previousCgo
    if (-not $SkipAndroid) {
        $gomobileCommand = Join-Path $mobileTools 'gomobile.exe'
        if (-not (Test-Path -LiteralPath $gomobileCommand)) { throw 'gomobile is missing; install the pinned version documented in products/android-node/README.md' }
        if (-not (Test-Path -LiteralPath $androidNdk)) { throw "Android NDK not found: $androidNdk" }
        $env:ANDROID_HOME = $androidSdk; $env:ANDROID_NDK_HOME = $androidNdk; $env:PATH = "$mobileTools;$env:PATH"
        $aarPath = Join-Path $androidProject 'app\libs\nodemobile.aar'
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $aarPath) | Out-Null
        & $gomobileCommand bind -target android/arm64 -androidapi 26 -ldflags '-extldflags=-Wl,-z,max-page-size=16384' -o $aarPath ./mobile/nodemobile
        if ($LASTEXITCODE -ne 0) { throw 'Android node library build failed' }
        $sdkProperties = 'sdk.dir=' + $androidSdk.Replace('\','/') + "`n"
        [IO.File]::WriteAllText((Join-Path $androidProject 'local.properties'), $sdkProperties, [Text.UTF8Encoding]::new($false))
        & $gradleCommand -p $androidProject assembleDebug --console=plain
        if ($LASTEXITCODE -ne 0) { throw 'Android APK build failed' }
        Copy-Item -LiteralPath (Join-Path $androidProject 'app\build\outputs\apk\debug\app-debug.apk') -Destination (Join-Path $outputDirectory 'OrdexusNode-android-arm64-preview.apk')
    }
    Get-ChildItem -LiteralPath $outputDirectory -File | Where-Object Extension -in '.exe','.apk' | ForEach-Object {
        Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256 | Select-Object Path,Hash
    }
} finally {
    $env:GOOS = $previousGoos; $env:GOARCH = $previousGoarch; $env:CGO_ENABLED = $previousCgo
    $env:ANDROID_HOME = $previousSdk; $env:ANDROID_NDK_HOME = $previousNdk; $env:PATH = $previousPath
    Pop-Location
}
