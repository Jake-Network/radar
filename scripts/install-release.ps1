# Install a published Windows amd64 release without Go, a compiler, or administrator rights.
# Download this script from a reviewed source and run it locally; do not pipe downloads into Invoke-Expression.
[CmdletBinding()]
param([string]$Version, [string]$Directory, [switch]$Force, [switch]$Doctor)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression

function Assert-RadarInstallPath([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Refusing installation through a symlink or reparse point: $current"
            }
        }
        $parent = [IO.Directory]::GetParent($current)
        if ($null -eq $parent) { break }
        $current = $parent.FullName
    }
}

function Save-RadarDownload([string]$Url, [string]$Path) {
    # Validate every redirect ourselves so HTTPS cannot downgrade to HTTP.
    Add-Type -AssemblyName System.Net.Http
    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $client = [Net.Http.HttpClient]::new($handler)
    try {
        $uri = [Uri]$Url
        for ($i = 0; $i -lt 10; $i++) {
            if ($uri.Scheme -ne 'https') { throw 'Release download requires HTTPS.' }
            $response = $client.GetAsync($uri).GetAwaiter().GetResult()
            try {
                $status = [int]$response.StatusCode
                if ($status -in 301,302,303,307,308) {
                    if ($null -eq $response.Headers.Location) { throw 'Redirect has no location.' }
                    $uri = [Uri]::new($uri, $response.Headers.Location)
                    continue
                }
                $response.EnsureSuccessStatusCode() | Out-Null
                $stream = [IO.File]::Create($Path)
                try { $response.Content.CopyToAsync($stream).GetAwaiter().GetResult() } finally { $stream.Dispose() }
                return
            } finally { $response.Dispose() }
        }
        throw 'Too many release download redirects.'
    } finally { $client.Dispose(); $handler.Dispose() }
}

function Expand-RadarExecutable([string]$Archive, [string]$Output) {
    $stream = [IO.File]::OpenRead($Archive)
    $zip = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Read)
    try {
        $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
        $binary = $null
        [long]$total = 0
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName
            if (-not $name.StartsWith('radar-windows_amd64/', [StringComparison]::Ordinal) -or
                $name.Contains('\') -or $name.Contains(':') -or $name -match '[\x00-\x1f]' -or
                $name -match '(^|/)(\.|\.\.)(/|$)' -or $name -match '(^|/)[^/]*[. ](/|$)' -or
                $name.Contains('//') -or -not $seen.Add($name)) { throw "Unsafe or duplicate archive entry: $name" }
            # Upper 16 bits contain Unix mode; lower bits may contain Windows file attributes.
            $mode = ($entry.ExternalAttributes -shr 16) -band 0xF000
            if ($mode -notin 0,0x8000,0x4000 -or ($entry.ExternalAttributes -band 0x400) -ne 0) {
                throw "Archive contains a symlink, reparse point, or special file: $name"
            }
            $total += $entry.Length
            if ($total -gt 536870912) { throw 'Release archive exceeds the size limit.' }
            if ($name -eq 'radar-windows_amd64/radar.exe') {
                if ($mode -eq 0x4000 -or ($entry.ExternalAttributes -band 0x10) -ne 0 -or $entry.Length -le 0 -or $entry.Length -gt 134217728) {
                    throw 'Invalid executable archive member.'
                }
                $binary = $entry
            }
        }
        if ($null -eq $binary) { throw 'Release archive has no regular radar.exe member.' }
        $inputStream = $binary.Open()
        $outputStream = [IO.File]::Create($Output)
        try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose(); $inputStream.Dispose() }
    } finally { $zip.Dispose(); $stream.Dispose() }
}

function Invoke-RadarInstall {
    param([string]$ReleaseVersion, [string]$InstallDirectory, [switch]$Replace, [switch]$RunDoctor,
        [string]$Architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString(),
        [scriptblock]$Download = ${function:Save-RadarDownload},
        [scriptblock]$CheckVersion = { param($binary) $result = & $binary version; if ($LASTEXITCODE -ne 0) { throw 'radar version failed.' }; $result })
    if ($ReleaseVersion -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$') {
        throw 'Specify an explicit published version, for example -Version v0.2.0.'
    }
    if ($Architecture -ne 'X64') { throw "Windows architecture $Architecture is unsupported; build from source on other systems." }
    if ([string]::IsNullOrWhiteSpace($InstallDirectory)) { throw 'Installation directory cannot be empty.' }
    $InstallDirectory = [IO.Path]::GetFullPath($InstallDirectory)
    Assert-RadarInstallPath $InstallDirectory
    $destination = Join-Path $InstallDirectory 'radar.exe'
    Assert-RadarInstallPath $destination
    if (Test-Path -LiteralPath $destination) {
        if (-not $Replace) { throw "Existing installation preserved: $destination. Use -Force to replace it." }
        if ((Get-Item -LiteralPath $destination).PSIsContainer) { throw 'Refusing to replace a directory.' }
    }
    $temporary = Join-Path ([IO.Path]::GetTempPath()) ('radar-install-' + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($temporary) | Out-Null
    $staged = $null
    try {
        $name = 'radar-windows_amd64.zip'
        $archive = Join-Path $temporary $name
        $checksum = Join-Path $temporary 'checksum'
        $base = "https://github.com/Jake-Network/radar/releases/download/$ReleaseVersion"
        & $Download "$base/$name" $archive
        & $Download "$base/$name.sha256" $checksum
        $text = [IO.File]::ReadAllText($checksum)
        $pattern = '\A([a-fA-F0-9]{64}) [ *]' + [regex]::Escape($name) + '(\r?\n)?\z'
        $match = [regex]::Match($text, $pattern)
        if (-not $match.Success) { throw 'Invalid release checksum file.' }
        if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $match.Groups[1].Value) {
            throw "Checksum mismatch for $name."
        }
        $binary = Join-Path $temporary 'radar.exe'
        Expand-RadarExecutable $archive $binary
        $reported = (& $CheckVersion $binary | Out-String).Trim()
        $expected = 'radar ' + $ReleaseVersion.Substring(1)
        if ($reported -ne $expected) { throw "Release version mismatch: expected $expected, received $reported" }
        if ($RunDoctor) { & $binary doctor; if ($LASTEXITCODE -ne 0) { throw 'radar doctor failed; existing installation preserved.' } }
        Assert-RadarInstallPath $destination
        [IO.Directory]::CreateDirectory($InstallDirectory) | Out-Null
        $staged = Join-Path $InstallDirectory ('.radar-install-' + [Guid]::NewGuid().ToString('N') + '.exe')
        [IO.File]::Copy($binary, $staged)
        # Preserve all licenses and notices without extracting untrusted archive paths.
        $notices = Join-Path $InstallDirectory "radar-$ReleaseVersion-notices.zip"
        Assert-RadarInstallPath $notices
        if (Test-Path -LiteralPath $notices) {
            if ((Get-FileHash -LiteralPath $notices -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash) {
                throw 'Existing notices archive differs; existing installation preserved.'
            }
        } else { [IO.File]::Copy($archive, $notices) }
        if (Test-Path -LiteralPath $destination) {
            if (-not $Replace) { throw 'An installation appeared during download; use -Force to replace it.' }
            # Same-volume replacement is atomic and retains the old file if replacement fails.
            [IO.File]::Replace($staged, $destination, [NullString]::Value)
        } else { [IO.File]::Move($staged, $destination) }
        $staged = $null
        Write-Output "Installed $destination. Add $InstallDirectory to PATH. Notices: $notices"
    } finally {
        if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
        Remove-Item -LiteralPath $temporary -Recurse -Force
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try {
        if (-not [Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([Runtime.InteropServices.OSPlatform]::Windows)) {
            throw 'This installer requires Windows amd64.'
        }
        if (-not $Directory) { $Directory = Join-Path $env:LOCALAPPDATA 'Radar/bin' }
        Invoke-RadarInstall -ReleaseVersion $Version -InstallDirectory $Directory -Replace:$Force -RunDoctor:$Doctor
    } catch { Write-Error $_; exit 1 }
}
