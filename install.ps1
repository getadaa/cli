# Installs the adaa CLI from the latest GitHub release, after checking it
# against the release's checksums.txt.
#
#   irm https://raw.githubusercontent.com/getadaa/cli/main/install.ps1 | iex
#
# $env:ADAA_VERSION picks a release (e.g. 0.3.0) and $env:ADAA_INSTALL_DIR where
# the binary goes (default %LOCALAPPDATA%\Programs\adaa). `adaa update` keeps an
# install made this way current.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'getadaa/cli'
$dir = if ($env:ADAA_INSTALL_DIR) { $env:ADAA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\adaa' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "adaa install: unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
}

$version = $env:ADAA_VERSION
if (-not $version) {
    $latest = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $version = $latest.tag_name
}
$version = $version.TrimStart('v')

$archive = "adaa_${version}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/v$version"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("adaa-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading adaa $version for windows/$arch..."
    Invoke-WebRequest "$base/$archive" -OutFile (Join-Path $tmp $archive)
    Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

    $want = (Get-Content (Join-Path $tmp 'checksums.txt') |
        Where-Object { ($_ -split '\s+')[1] -eq $archive } |
        ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
    if (-not $want) { throw "adaa install: $archive is not listed in checksums.txt" }
    $got = (Get-FileHash (Join-Path $tmp $archive) -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want.ToLower()) { throw "adaa install: checksum mismatch for ${archive}: refusing to install it" }

    Expand-Archive (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x')
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item (Join-Path $tmp 'x\adaa.exe') (Join-Path $dir 'adaa.exe') -Force
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$dir"), 'User')
    $env:Path += ";$dir"
    Write-Host "Added $dir to your PATH (new terminals pick it up)."
}
Write-Host "Installed $(& (Join-Path $dir 'adaa.exe') --version)"
Write-Host 'Next: adaa login'
