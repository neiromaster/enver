# One-line installer for enver (Windows). Review this script before piping it:
#   https://github.com/neiromaster/enver/blob/main/scripts/install.ps1
#
# Usage:
#   irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
#   $env:ENVER_VERSION = "v0.9.1"; irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
#   powershell -ExecutionPolicy Bypass -File install.ps1 -Version v0.9.1
#
# Env:
#   ENVER_VERSION      tag to install (default: latest release)
#   ENVER_INSTALL_DIR  install dir (default: $env:LOCALAPPDATA\enver\bin)
#
# Requires PowerShell 5.0+ (Expand-Archive). No admin rights needed.

param(
  [string]$Version
)

$ErrorActionPreference = "Stop"

$Repo = "neiromaster/enver"
$Base = "https://github.com/$Repo/releases/download"

switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $GoArch = "amd64" }
  "ARM64" { $GoArch = "arm64" }
  default {
    Write-Error "enver: unsupported architecture $env:PROCESSOR_ARCHITECTURE - use npm install -g @enver-go/enver or a release zip"
  }
}

$Version = if ($Version) { $Version } else { $env:ENVER_VERSION }
if (-not $Version) {
  Write-Host "enver: resolving the latest release..." -ForegroundColor DarkGray
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
  $Version = $release.tag_name
}
if (-not $Version.StartsWith("v")) { $Version = "v$Version" }

$InstallDir = $env:ENVER_INSTALL_DIR
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "enver\bin" }

$Work = Join-Path $env:TEMP ("enver-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Work | Out-Null

try {
  $Archive = "enver_" + $Version.Substring(1) + "_windows_" + $GoArch + ".zip"
  $Zip = Join-Path $Work $Archive
  $ChecksumFile = Join-Path $Work "checksums.txt"

  Write-Host "enver: downloading $Base/$Version/$Archive..." -ForegroundColor DarkGray
  Invoke-WebRequest -Uri "$Base/$Version/$Archive" -OutFile $Zip
  Invoke-WebRequest -Uri "$Base/$Version/checksums.txt" -OutFile $ChecksumFile

  $Expected = $null
  foreach ($Line in Get-Content $ChecksumFile) {
    $Fields = $Line -split "\s+"
    if ($Fields.Count -ge 2 -and $Fields[1] -eq $Archive) { $Expected = $Fields[0]; break }
  }
  if (-not $Expected) { throw "enver: checksums.txt has no entry for $Archive" }
  $Actual = (Get-FileHash -Algorithm SHA256 -Path $Zip).Hash.ToLowerInvariant()
  if ($Actual -ne $Expected) { throw "enver: checksum mismatch for $Archive" }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Expand-Archive -Path $Zip -DestinationPath (Join-Path $Work "unpacked") -Force
  Copy-Item -Path (Join-Path $Work "unpacked\enver.exe") -Destination (Join-Path $InstallDir "enver.exe") -Force

  $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($UserPath -and $UserPath -notlike "*$InstallDir*") {
    $NewPath = $UserPath.TrimEnd(";") + ";" + $InstallDir
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Write-Host "enver: added $InstallDir to your user PATH (new terminals only)" -ForegroundColor DarkGray
  } elseif (-not $UserPath) {
    [Environment]::SetEnvironmentVariable("Path", $InstallDir, "User")
    Write-Host "enver: added $InstallDir to your user PATH (new terminals only)" -ForegroundColor DarkGray
  }
  $env:Path += ";$InstallDir"

  & (Join-Path $InstallDir "enver.exe") --version
}
finally {
  Remove-Item -Recurse -Force $Work -ErrorAction SilentlyContinue
}
