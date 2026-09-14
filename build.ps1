#requires -Version 5.1
[CmdletBinding()]
param(
    [switch]$Install,
    [switch]$Test,
    [string]$Output,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ClashOfTokens\bin'),
    [string]$Version = '0.1.0-dev',
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
function Get-Sha256([string]$File) {
    $stream = [IO.File]::OpenRead($File)
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($algorithm.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $algorithm.Dispose(); $stream.Dispose() }
}
if ([Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'Use build.sh on Linux/macOS.' }
if ($Version -notmatch '^[A-Za-z0-9_.+-]+$') { throw 'Invalid build version.' }
$architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
switch ($architecture.ToUpperInvariant()) {
    'X64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { throw 'Supported architectures: amd64, arm64.' }
}
$savedEnvironment = @{}
foreach ($name in @('GOTOOLCHAIN','GOOS','GOARCH','CGO_ENABLED','GOFLAGS','GOROOT')) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
$temporary = $null
Push-Location $PSScriptRoot
try {
    $env:GOTOOLCHAIN = 'local'
    $env:GOROOT = $null
    $env:GOFLAGS = $null
    $required = ((Get-Content -LiteralPath 'go.mod' | Where-Object { $_ -match '^toolchain ' }) -split '\s+')[1]
    if ($required -notmatch '^go\d+\.\d+\.\d+$') { throw 'go.mod must declare a pinned toolchain.' }
    $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    $goExe = $null
    if ($goCommand) {
        $goVersion = & $goCommand.Source version
        if ($LASTEXITCODE -eq 0 -and $goVersion -like "go version $required *") { $goExe = $goCommand.Source }
    }
    if (-not $goExe) {
        $cache = Join-Path $env:LOCALAPPDATA "ClashOfTokens\toolchains\$required-windows-$arch"
        $goExe = Join-Path $cache 'go\bin\go.exe'
        if (-not (Test-Path -LiteralPath $goExe)) {
            $archive = "$required.windows-$arch.zip"
            $line = @(Get-Content -LiteralPath 'scripts/go-toolchains.txt' | Where-Object { $_ -match ('^([a-f0-9]{64})\s+' + [regex]::Escape($archive) + '$') })
            if ($line.Count -ne 1) { throw 'Pinned toolchain checksum missing. Update scripts/go-toolchains.txt.' }
            $expected = ($line[0] -split '\s+')[0]
            $download = "$cache-download-$([guid]::NewGuid().ToString('N'))"
            [IO.Directory]::CreateDirectory($download) | Out-Null
            Write-Host "Downloading verified $required to $cache"
            [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
            $zip = Join-Path $download 'archive.zip'
            Invoke-WebRequest -UseBasicParsing -Uri "https://go.dev/dl/$archive" -OutFile $zip -TimeoutSec 900
            if ((Get-Sha256 $zip) -ne $expected) { throw 'Go checksum mismatch.' }
            [Reflection.Assembly]::LoadWithPartialName('System.IO.Compression.FileSystem') | Out-Null
            [IO.Compression.ZipFile]::ExtractToDirectory($zip, $download)
            if (Test-Path -LiteralPath $cache) { throw "Toolchain directory already exists but is incomplete: $cache" }
            # Both paths are fixed children of this user's toolchain directory; preserve caches.
            [IO.Directory]::Move($download, $cache)
        }
    }
    $env:GOOS = 'windows'; $env:GOARCH = $arch; $env:CGO_ENABLED = '0'
    & $goExe version
    if ($LASTEXITCODE -ne 0) { throw 'Go could not start.' }
    & $goExe mod download
    if ($LASTEXITCODE -ne 0) { throw 'Dependency download failed.' }
    & $goExe mod verify
    if ($LASTEXITCODE -ne 0) { throw 'Dependency verification failed.' }
    if ($Test) {
        & $goExe test ./... -count=1 -timeout 180s
        if ($LASTEXITCODE -ne 0) { throw 'Tests failed; build/install stopped.' }
    }
    if (-not $Output) { $Output = Join-Path $PSScriptRoot 'dist\clash-tokens.exe' }
    $Output = [IO.Path]::GetFullPath($Output)
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Output)) | Out-Null
    $temporary = Join-Path ([IO.Path]::GetDirectoryName($Output)) ('.cot-build-' + [guid]::NewGuid().ToString('N') + '.exe')
    & $goExe build -trimpath "-ldflags=-s -w -X main.version=$Version" -o $temporary ./cmd/clash-tokens
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    & $temporary version
    if ($LASTEXITCODE -ne 0) { throw 'Built binary validation failed.' }
    if (Test-Path -LiteralPath $Output) { [IO.File]::Replace($temporary, $Output, [NullString]::Value) } else { [IO.File]::Move($temporary, $Output) }
    $hash = (Get-Sha256 $Output)
    [IO.File]::WriteAllText("$Output.sha256", "$hash  $([IO.Path]::GetFileName($Output))`n", [Text.UTF8Encoding]::new($false))
    Write-Host "Built: $Output"
    if ($Install) { & (Join-Path $PSScriptRoot 'install.ps1') -Binary $Output -Sha256 $hash -InstallDir $InstallDir -NoPath:$NoPath }
} finally {
    Pop-Location
    foreach ($name in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process') }
    if ($temporary -and (Test-Path -LiteralPath $temporary)) { Remove-Item -LiteralPath $temporary -Force }
}
