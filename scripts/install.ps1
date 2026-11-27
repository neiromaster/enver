# One-line installer for enver (Windows). Review this script before piping it:
#   https://github.com/neiromaster/enver/blob/main/scripts/install.ps1
#
# Usage:
#   irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
#   $env:ENVER_VERSION = "v0.9.1"; irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
#   powershell -ExecutionPolicy Bypass -File install.ps1 -Version v0.9.1
#
# Env:
#   ENVER_VERSION        tag to install (default: latest release)
#   ENVER_INSTALL_DIR    install dir (default: $env:LOCALAPPDATA\enver\bin)
#   ENVER_NO_COMPLETION  set to 1 to skip PowerShell completion setup
#
# Requires PowerShell 5.0+ (Expand-Archive). No admin rights needed.

param(
  [string]$Version,
  [switch]$Uninstall
)

$ErrorActionPreference = "Stop"

$EnverCompletionMarker = "# enver completions (added by install.ps1)"

function Get-EnverCompletionScriptPath {
  param([string]$InstallDir)
  return Join-Path (Join-Path (Split-Path -Parent $InstallDir) "completions") "enver.ps1"
}

function Add-EnverCompletion {
  param(
    [string]$CompletionScript,
    [string]$ProfilePath
  )

  $Dir = Split-Path -Parent $ProfilePath
  if ($Dir -and -not (Test-Path -Path $Dir)) {
    New-Item -ItemType Directory -Path $Dir -Force | Out-Null
  }
  if (-not (Test-Path -Path $ProfilePath)) {
    New-Item -ItemType File -Path $ProfilePath -Force | Out-Null
  }

  $Content = Get-Content -Path $ProfilePath -Raw
  if ($null -eq $Content) { $Content = "" }

  if ($Content -notmatch [regex]::Escape($EnverCompletionMarker)) {
    # The line is executed by the profile, so the path must survive both the
    # double quotes and PowerShell expansion.
    $Quoted = $CompletionScript.Replace('`', '``').Replace('"', '`"').Replace('$', '`$')
    $NewLine = [Environment]::NewLine
    $Text = ""
    if ($Content.Length -gt 0 -and -not $Content.EndsWith("`n")) {
      $Text = $NewLine
    }
    $Text += $EnverCompletionMarker + $NewLine
    $Text += "if (Test-Path `"$Quoted`") { . `"$Quoted`" }" + $NewLine
    [System.IO.File]::AppendAllText($ProfilePath, $Text)
  }

  $Policy = Get-ExecutionPolicy
  if ($Policy -eq "Restricted" -or $Policy -eq "AllSigned") {
    Write-Host "enver: PowerShell execution policy is $Policy, so completions will not load. Run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
  }
}

function Remove-EnverCompletion {
  param([string]$ProfilePath)

  if (-not (Test-Path -Path $ProfilePath)) { return }

  $Lines = @(Get-Content -Path $ProfilePath)
  $Kept = @()
  $Removed = $false
  for ($i = 0; $i -lt $Lines.Count; $i++) {
    if ($Lines[$i].Trim() -eq $EnverCompletionMarker) {
      $Next = $i + 1
      if ($Next -lt $Lines.Count -and $Lines[$Next].Trim().StartsWith("if (Test-Path")) {
        $i = $Next
      }
      $Removed = $true
      continue
    }
    $Kept += $Lines[$i]
  }

  if ($Removed) {
    if ($Kept.Count -eq 0) {
      Clear-Content -Path $ProfilePath
    } else {
      Set-Content -Path $ProfilePath -Value $Kept
    }
  }
}

function Remove-EnverPathSegment {
  param(
    [string]$PathValue,
    [string]$Segment
  )
  $Segments = @($PathValue -split ";" | Where-Object { $_ -ne $Segment })
  return ($Segments -join ";")
}

function Invoke-EnverInstall {
  $Repo = "neiromaster/enver"
  $Base = "https://github.com/$Repo/releases/download"

  switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $GoArch = "amd64" }
    "ARM64" { $GoArch = "arm64" }
    default {
      throw "enver: unsupported architecture $env:PROCESSOR_ARCHITECTURE - use npm install -g @enver-go/enver or a release zip"
    }
  }

  $TargetVersion = $Version
  if (-not $TargetVersion) { $TargetVersion = $env:ENVER_VERSION }
  if (-not $TargetVersion) {
    Write-Host "enver: resolving the latest release..." -ForegroundColor DarkGray
    $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
    $TargetVersion = $Release.tag_name
  }
  if (-not $TargetVersion.StartsWith("v")) { $TargetVersion = "v$TargetVersion" }

  $InstallDir = $env:ENVER_INSTALL_DIR
  if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "enver\bin" }

  $Work = Join-Path $env:TEMP ("enver-install-" + [guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Path $Work | Out-Null

  try {
    $Archive = "enver_" + $TargetVersion.Substring(1) + "_windows_" + $GoArch + ".zip"
    $Zip = Join-Path $Work $Archive
    $ChecksumFile = Join-Path $Work "checksums.txt"

    Write-Host "enver: downloading $Base/$TargetVersion/$Archive..." -ForegroundColor DarkGray
    Invoke-WebRequest -Uri "$Base/$TargetVersion/$Archive" -OutFile $Zip
    Invoke-WebRequest -Uri "$Base/$TargetVersion/checksums.txt" -OutFile $ChecksumFile

    $Expected = $null
    foreach ($Line in Get-Content $ChecksumFile) {
      $Fields = $Line -split "\s+"
      if ($Fields.Count -ge 2 -and $Fields[1] -eq $Archive) { $Expected = $Fields[0]; break }
    }
    if (-not $Expected) { throw "enver: checksums.txt has no entry for $Archive" }
    $Actual = (Get-FileHash -Algorithm SHA256 -Path $Zip).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) { throw "enver: checksum mismatch for $Archive" }

    $Unpacked = Join-Path $Work "unpacked"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Expand-Archive -Path $Zip -DestinationPath $Unpacked -Force
    Copy-Item -Path (Join-Path $Unpacked "enver.exe") -Destination (Join-Path $InstallDir "enver.exe") -Force

    $CompletionScript = Get-EnverCompletionScriptPath -InstallDir $InstallDir
    $BundledCompletion = Join-Path (Join-Path $Unpacked "completions") "enver.ps1"
    if (Test-Path -Path $BundledCompletion) {
      New-Item -ItemType Directory -Path (Split-Path -Parent $CompletionScript) -Force | Out-Null
      Copy-Item -Path $BundledCompletion -Destination $CompletionScript -Force
      if ($env:ENVER_NO_COMPLETION -ne "1") {
        Add-EnverCompletion -CompletionScript $CompletionScript -ProfilePath $PROFILE
      }
    } else {
      Write-Host "enver: this release ships no PowerShell completion script - skipping completion setup" -ForegroundColor DarkGray
    }

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
}

# The guard exists so tests can dot-source this file for its helpers without
# running the install flow.
if ($env:ENVER_SKIP_INSTALL_FLOW -ne "1") {
  if ($Uninstall -or $env:ENVER_UNINSTALL -eq "1") {
    Invoke-EnverUninstall
  } else {
    Invoke-EnverInstall
  }
}
