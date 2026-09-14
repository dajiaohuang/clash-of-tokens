#requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Version = 'latest',
    [string]$Repository = 'dajiaohuang/clash-of-tokens',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ClashOfTokens\bin'),
    [switch]$FromSource,
    [string]$Binary,
    [string]$Sha256,
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
if ([Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'Use install.sh on Linux/macOS.' }
if ($FromSource) {
    if ($Binary -or $Version -ne 'latest') { throw 'Source mode cannot be combined with a binary or release version.' }
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'build.ps1'))) { throw 'Source mode requires a repository checkout.' }
    & (Join-Path $PSScriptRoot 'build.ps1') -Install -InstallDir $InstallDir -NoPath:$NoPath
    return
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) { throw 'Install directory cannot be empty.' }
$architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
switch ($architecture.ToUpperInvariant()) {
    'X64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { throw 'Supported architectures: amd64, arm64.' }
}
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
[IO.Directory]::CreateDirectory($InstallDir) | Out-Null
$stage = Join-Path $InstallDir ('.cot-install-' + [guid]::NewGuid().ToString('N') + '.exe')
try {
    if ($Binary) {
        Copy-Item -LiteralPath $Binary -Destination $stage
    } else {
        if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' -or $Version -notmatch '^[A-Za-z0-9_.-]+$') { throw 'Invalid repository or version.' }
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $endpoint = "https://api.github.com/repos/$Repository/releases/latest"
        if ($Version -ne 'latest') { $endpoint = "https://api.github.com/repos/$Repository/releases/tags/$Version" }
        try { $release = Invoke-RestMethod -Uri $endpoint -TimeoutSec 60 -Headers @{Accept='application/vnd.github+json'; 'User-Agent'='clash-tokens-installer'} }
        catch { throw "Release unavailable for $Repository ($Version). Use -FromSource in a checkout. $($_.Exception.Message)" }
        if ($release.draft) { throw 'Draft releases cannot be installed.' }
        $tag = [string]$release.tag_name
        if ($tag -notmatch '^[A-Za-z0-9_.-]+$') { throw 'Unsupported release tag.' }
        $assetName = "clash-tokens-windows-$arch.exe"
        foreach ($name in @($assetName, "$assetName.sha256")) {
            if (@($release.assets | Where-Object { $_.name -eq $name }).Count -ne 1) { throw "Release $tag has no unique asset $name. Use -FromSource until a compatible release is published." }
        }
        $base = "https://github.com/$Repository/releases/download/$tag"
        $checksum = Invoke-WebRequest -UseBasicParsing -Uri "$base/$assetName.sha256" -TimeoutSec 60
        $content = $checksum.Content
        if ($content -is [byte[]]) { $content = [Text.Encoding]::UTF8.GetString($content) }
        $lines = @(([string]$content -split '\r?\n') | Where-Object { $_ -match ('^([a-fA-F0-9]{64})\s+' + [regex]::Escape($assetName) + '$') })
        if ($lines.Count -ne 1) { throw 'Release checksum missing or ambiguous.' }
        $Sha256 = ($lines[0] -split '\s+')[0]
        Write-Host "Downloading $Repository $tag (windows/$arch)"
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$assetName" -OutFile $stage -TimeoutSec 600
    }
    if ($Sha256 -notmatch '^[a-fA-F0-9]{64}$') { throw 'A SHA-256 checksum is required, including for local binaries.' }
    if ((Get-Sha256 $stage) -ne $Sha256) { throw 'Checksum mismatch. Installed binary was not changed.' }
    & $stage version
    if ($LASTEXITCODE -ne 0) { throw 'Binary validation failed. Installed binary was not changed.' }
    $target = Join-Path $InstallDir 'clash-tokens.exe'
    if (Test-Path -LiteralPath $target) {
        # Replace atomically; a running/locked executable produces an error without deleting it.
        [IO.File]::Replace($stage, $target, [NullString]::Value)
    } else { [IO.File]::Move($stage, $target) }
    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
            [Environment]::SetEnvironmentVariable('Path', (($entries + $InstallDir) -join ';'), 'User')
        }
        if (-not (($env:PATH -split ';') | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) { $env:PATH = "$InstallDir;$env:PATH" }
    }
    Write-Host "Installed: $target"
    Write-Host 'Next: clash-tokens init -config config.json'
    if (-not $NoPath) { Write-Host 'Open a new terminal to use the updated user PATH.' }
} finally {
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Force }
}
