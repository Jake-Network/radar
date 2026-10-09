# Standalone deterministic installer regressions: no network, Pester, or executable fixture required.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'install-release.ps1')
$root = Join-Path ([IO.Path]::GetTempPath()) ('radar-install-tests-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$script:checks = 0
function Assert-True($Condition, [string]$Message) {
    if (-not $Condition) { throw "Assertion failed: $Message" }
    $script:checks++
}
function Assert-Fails([scriptblock]$Operation, [string]$Pattern) {
    $failure = $null
    try { & $Operation | Out-Null } catch { $failure = $_.Exception.Message }
    Assert-True ($null -ne $failure -and $failure -match $Pattern) "Expected failure matching $Pattern, received $failure"
}
function New-TestZip([string]$Path, [string[]]$Names, [int]$Attributes = 0) {
    $file = [IO.File]::Create($Path)
    $zip = [IO.Compression.ZipArchive]::new($file, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in $Names) {
            $entry = $zip.CreateEntry($name)
            $entry.ExternalAttributes = $Attributes
            $stream = $entry.Open()
            try { $bytes = [Text.Encoding]::UTF8.GetBytes('test executable'); $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
        }
    } finally { $zip.Dispose(); $file.Dispose() }
}
try {
    $script:asset = Join-Path $root 'release.zip'
    # Same Unix regular-file mode as release.py's native Windows zip artifacts.
    New-TestZip $script:asset @('radar-windows_amd64/radar.exe', 'radar-windows_amd64/LICENSE') -2119958528
    $script:digest = (Get-FileHash -LiteralPath $script:asset -Algorithm SHA256).Hash
    $download = {
        param($url, $path)
        if ($url.EndsWith('.sha256')) { [IO.File]::WriteAllText($path, "$script:digest  radar-windows_amd64.zip`n") }
        else { [IO.File]::Copy($script:asset, $path) }
    }
    $versionCheck = { param($binary) 'radar 1.2.3' }
    $install = Join-Path $root 'install'
    Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Architecture X64 -Download $download -CheckVersion $versionCheck | Out-Null
    $exe = Join-Path $install 'radar.exe'
    Assert-True ([IO.File]::ReadAllText($exe) -eq 'test executable') 'successful installation'
    Assert-True (Test-Path -LiteralPath (Join-Path $install 'radar-v1.2.3-notices.zip')) 'license archive preserved'
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Architecture X64 -Download $download -CheckVersion $versionCheck } 'Existing installation preserved'
    [IO.File]::WriteAllText($exe, 'old binary')
    $script:digest = '0' * 64
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Replace -Architecture X64 -Download $download -CheckVersion $versionCheck } 'Checksum mismatch'
    Assert-True ([IO.File]::ReadAllText($exe) -eq 'old binary') 'checksum failure preserves old binary'
    $script:digest = (Get-FileHash -LiteralPath $script:asset -Algorithm SHA256).Hash
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Replace -Architecture X64 -Download $download -CheckVersion { 'radar 9.9.9' } } 'Release version mismatch'
    Assert-True ([IO.File]::ReadAllText($exe) -eq 'old binary') 'version failure preserves old binary'
    Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Replace -Architecture X64 -Download $download -CheckVersion $versionCheck | Out-Null
    Assert-True ([IO.File]::ReadAllText($exe) -eq 'test executable') 'explicit replacement'
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion latest -InstallDirectory $install -Architecture X64 } 'explicit published version'
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Architecture Arm64 } 'unsupported'
    $badChecksum = { param($url, $path) if ($url.EndsWith('.sha256')) { [IO.File]::WriteAllText($path, "$script:digest  wrong.zip`n") } else { [IO.File]::Copy($script:asset, $path) } }
    Assert-Fails { Invoke-RadarInstall -ReleaseVersion v1.2.3 -InstallDirectory $install -Replace -Architecture X64 -Download $badChecksum -CheckVersion $versionCheck } 'Invalid release checksum'
    $badZip = Join-Path $root 'unsafe.zip'
    $output = Join-Path $root 'extracted.exe'
    foreach ($names in @(
        @('radar-windows_amd64/radar.exe', 'radar-windows_amd64/radar.exe'),
        @('radar-windows_amd64/radar.exe', 'radar-windows_amd64/../escape'),
        @('radar-windows_amd64/radar.exe', '/absolute'),
        @('radar-windows_amd64/radar.exe', 'radar-windows_amd64\escape'),
        @('radar-windows_amd64/radar.exe', 'radar-windows_amd64/radar.exe:stream')
    )) {
        New-TestZip $badZip $names
        Assert-Fails { Expand-RadarExecutable $badZip $output } 'Unsafe or duplicate'
        Assert-True (-not (Test-Path -LiteralPath $output)) 'unsafe archives do not extract'
    }
    # Unix symlink mode and Windows reparse attributes are both forbidden.
    foreach ($attributes in @(-1610612736, 0x400)) {
        New-TestZip $badZip @('radar-windows_amd64/radar.exe') $attributes
        Assert-Fails { Expand-RadarExecutable $badZip $output } 'symlink, reparse point'
    }
    New-TestZip $badZip @('radar-windows_amd64/LICENSE')
    Assert-Fails { Expand-RadarExecutable $badZip $output } 'no regular radar.exe'
    New-TestZip $badZip @('radar-windows_amd64/radar.exe') 0x10
    Assert-Fails { Expand-RadarExecutable $badZip $output } 'Invalid executable'
    if ([Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([Runtime.InteropServices.OSPlatform]::Windows)) {
        $junction = Join-Path $root 'junction'
        New-Item -ItemType Junction -Path $junction -Target $install | Out-Null
        try { Assert-Fails { Assert-RadarInstallPath (Join-Path $junction 'radar.exe') } 'symlink or reparse point' }
        finally { [IO.Directory]::Delete($junction) }
    }
    Assert-True (@(Get-ChildItem -LiteralPath $install -Filter '.radar-install-*').Count -eq 0) 'staging cleanup'
    Write-Output "Windows installer regressions passed ($script:checks assertions)."
} finally { Remove-Item -LiteralPath $root -Recurse -Force }
