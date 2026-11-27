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

# Reads a text file with the encoding its byte order mark declares. A file
# without a BOM is decoded with the engine default, which is what Get-Content
# would use; the encoding instance is returned so a rewrite cannot silently
# change the file.
function Read-EnverTextFile {
  param([string]$Path)

  $Bytes = [System.IO.File]::ReadAllBytes($Path)
  $Offset = 0
  if ($Bytes.Length -ge 3 -and $Bytes[0] -eq 0xEF -and $Bytes[1] -eq 0xBB -and $Bytes[2] -eq 0xBF) {
    $Encoding = [System.Text.UTF8Encoding]::new($true)
    $Offset = 3
  } elseif ($Bytes.Length -ge 2 -and $Bytes[0] -eq 0xFF -and $Bytes[1] -eq 0xFE) {
    $Encoding = [System.Text.UnicodeEncoding]::new($false, $true)
    $Offset = 2
  } elseif ($Bytes.Length -ge 2 -and $Bytes[0] -eq 0xFE -and $Bytes[1] -eq 0xFF) {
    $Encoding = [System.Text.UnicodeEncoding]::new($true, $true)
    $Offset = 2
  } else {
    $Encoding = [System.Text.Encoding]::Default
  }

  return @{
    Text = $Encoding.GetString($Bytes, $Offset, $Bytes.Length - $Offset)
    Encoding = $Encoding
    HasBom = ($Offset -gt 0)
  }
}

# Replaces a text file while keeping the encoding and BOM that
# Read-EnverTextFile detected.
function Write-EnverTextFile {
  param(
    [string]$Path,
    [string]$Text,
    $Encoding,
    [bool]$HasBom
  )

  $Body = $Encoding.GetBytes($Text)
  if ($HasBom) {
    $Preamble = $Encoding.GetPreamble()
    $Bytes = New-Object byte[] ($Preamble.Length + $Body.Length)
    [Array]::Copy($Preamble, 0, $Bytes, 0, $Preamble.Length)
    [Array]::Copy($Body, 0, $Bytes, $Preamble.Length, $Body.Length)
  } else {
    $Bytes = $Body
  }
  [System.IO.File]::WriteAllBytes($Path, $Bytes)
}

# Appends in the detected encoding without touching the bytes already there.
function Add-EnverTextFile {
  param(
    [string]$Path,
    [string]$Text,
    $Encoding
  )

  $Bytes = $Encoding.GetBytes($Text)
  $Stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write)
  try {
    $Stream.Write($Bytes, 0, $Bytes.Length)
  } finally {
    $Stream.Dispose()
  }
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

  $File = Read-EnverTextFile -Path $ProfilePath
  $Content = $File.Text

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
    Add-EnverTextFile -Path $ProfilePath -Text $Text -Encoding $File.Encoding
  }

  $Policy = Get-ExecutionPolicy
  if ($Policy -eq "Restricted" -or $Policy -eq "AllSigned") {
    Write-Host "enver: PowerShell execution policy is $Policy, so completions will not load. Run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
  }
}

function Remove-EnverCompletion {
  param([string]$ProfilePath)

  if (-not (Test-Path -Path $ProfilePath)) { return }

  $File = Read-EnverTextFile -Path $ProfilePath

  # Keep each line terminator alongside its line, so surviving lines are
  # written back byte for byte instead of in the engine default newline style.
  $Parts = [regex]::Split($File.Text, "(\r\n|\r|\n)")
  $Lines = @()
  $Separators = @()
  for ($i = 0; $i -lt $Parts.Length; $i++) {
    if ($i % 2 -eq 0) { $Lines += $Parts[$i] } else { $Separators += $Parts[$i] }
  }

  $Kept = ""
  $Removed = $false
  for ($i = 0; $i -lt $Lines.Length; $i++) {
    if ($Lines[$i].Trim() -eq $EnverCompletionMarker) {
      $Next = $i + 1
      if ($Next -lt $Lines.Length -and $Lines[$Next].Trim().StartsWith("if (Test-Path")) {
        $i = $Next
      }
      $Removed = $true
      continue
    }
    $Kept += $Lines[$i]
    if ($i -lt $Separators.Length) { $Kept += $Separators[$i] }
  }

  if ($Removed) {
    Write-EnverTextFile -Path $ProfilePath -Text $Kept -Encoding $File.Encoding -HasBom $File.HasBom
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

function Invoke-EnverUninstall {
  param(
    [string]$InstallDir,
    [string]$ProfilePath,
    [string]$UserPath
  )

  $Removed = $false

  # The exe locks itself while running, so the delete comes first: a locked
  # binary aborts before anything else has been changed.
  $Exe = Join-Path $InstallDir "enver.exe"
  if (Test-Path -Path $Exe) {
    try {
      Remove-Item -Path $Exe -Force -ErrorAction Stop
    } catch {
      throw "enver: enver.exe is in use - close terminals running enver and rerun"
    }
    Write-Host "enver: removed $Exe"
    $Removed = $true
  }

  $CompletionScript = Get-EnverCompletionScriptPath -InstallDir $InstallDir
  $CompletionDir = Split-Path -Parent $CompletionScript
  if (Test-Path -Path $CompletionScript) {
    Remove-Item -Path $CompletionScript -Force -ErrorAction Stop
    Write-Host "enver: removed $CompletionScript"
    $Removed = $true
  }
  if ((Test-Path -Path $CompletionDir) -and -not (Get-ChildItem -Path $CompletionDir -Force)) {
    Remove-Item -Path $CompletionDir -Force -ErrorAction Stop
    Write-Host "enver: removed $CompletionDir"
    $Removed = $true
  }

  $HasProfileBlock = $false
  if (Test-Path -Path $ProfilePath) {
    foreach ($Line in @(Get-Content -Path $ProfilePath)) {
      if ($Line.Trim() -eq $EnverCompletionMarker) { $HasProfileBlock = $true; break }
    }
  }
  if ($HasProfileBlock) {
    Remove-EnverCompletion -ProfilePath $ProfilePath
    Write-Host "enver: removed the enver completion from $ProfilePath"
    $Removed = $true
  }

  $CleanedPath = $null
  if ($UserPath) {
    $Candidate = Remove-EnverPathSegment -PathValue $UserPath -Segment $InstallDir
    if ($Candidate -ne $UserPath) {
      $CleanedPath = $Candidate
      Write-Host "enver: removed $InstallDir from your user PATH"
      $Removed = $true
    }
  }

  # The install root holds only our bin/ and completions/ trees. Anything else
  # is foreign: keep the whole tree rather than delete user files.
  $InstallRoot = Split-Path -Parent $InstallDir
  if (Test-Path -Path $InstallRoot) {
    $Foreign = @(Get-ChildItem -Path $InstallRoot -Recurse -Force -File)
    foreach ($Item in @(Get-ChildItem -Path $InstallRoot -Recurse -Force -Directory)) {
      if ($Item.FullName -ne $InstallDir -and $Item.FullName -ne $CompletionDir) {
        $Foreign += $Item
      }
    }
    if ($Foreign.Count -eq 0) {
      Remove-Item -Path $InstallRoot -Recurse -Force -ErrorAction Stop
      Write-Host "enver: removed $InstallRoot"
      $Removed = $true
    } else {
      Write-Host "enver: kept $InstallRoot because it holds files enver did not install"
      $Removed = $true
    }
  }

  if ($Removed) {
    $ConfigDir = $null
    if ($env:XDG_CONFIG_HOME) {
      $ConfigDir = Join-Path $env:XDG_CONFIG_HOME "enver"
    } elseif ($HOME) {
      $ConfigDir = Join-Path (Join-Path $HOME ".config") "enver"
    } elseif ($env:USERPROFILE) {
      $ConfigDir = Join-Path (Join-Path $env:USERPROFILE ".config") "enver"
    }
    if ($ConfigDir) {
      Write-Host "enver: user data at $ConfigDir was kept - remove it with: Remove-Item -Recurse -Force `"$ConfigDir`""
    }
  }

  return $CleanedPath
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
    $UninstallDir = $env:ENVER_INSTALL_DIR
    if (-not $UninstallDir) { $UninstallDir = Join-Path $env:LOCALAPPDATA "enver\bin" }
    $UserPathValue = [Environment]::GetEnvironmentVariable("Path", "User")
    $CleanedPath = Invoke-EnverUninstall -InstallDir $UninstallDir -ProfilePath $PROFILE -UserPath $UserPathValue
    if ($null -ne $CleanedPath) {
      [Environment]::SetEnvironmentVariable("Path", $CleanedPath, "User")
    }
  } else {
    Invoke-EnverInstall
  }
}
