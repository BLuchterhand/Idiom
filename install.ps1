param(
  [string] $Version = $env:IDIOM_VERSION,
  [string] $InstallDir = $env:IDIOM_INSTALL_DIR,
  [string] $Repo = $(if ($env:IDIOM_REPO) { $env:IDIOM_REPO } else { "BLuchterhand/idiom" }),
  [string] $ReleaseUrlBase = $env:IDIOM_RELEASE_URL_BASE,
  [ValidateSet("Cli", "Tools", "Full", "DiscoveryDeps", "DoctorOnly")]
  [string] $Profile = $(if ($env:IDIOM_INSTALL_PROFILE) { $env:IDIOM_INSTALL_PROFILE } else { "Cli" }),
  [switch] $WithDependencies,
  [switch] $AllDependencies,
  [switch] $NoDependencies,
  [switch] $NoDependencyAudit
)

$ErrorActionPreference = "Stop"

function Write-Log([string] $Message) {
  Write-Host $Message
}

function Fail([string] $Message) {
  throw "error: $Message"
}

function Resolve-IdiomVersion {
  if (-not [string]::IsNullOrWhiteSpace($Version)) {
    return $Version
  }
  try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "idiom-installer" }
    if (-not [string]::IsNullOrWhiteSpace($release.tag_name)) {
      return [string] $release.tag_name
    }
  } catch {
    $location = ""
    try {
      $latest = Invoke-WebRequest -UseBasicParsing -MaximumRedirection 0 -Uri "https://github.com/$Repo/releases/latest" -ErrorAction Stop
      $location = $latest.Headers.Location
    } catch {
      if ($_.Exception.Response -and $_.Exception.Response.Headers) {
        $location = $_.Exception.Response.Headers.Location
      }
    }
    if (-not [string]::IsNullOrWhiteSpace($location)) {
      return ($location -split "/tag/")[-1]
    }
  }
  Fail "could not resolve latest release tag from GitHub"
}

function Resolve-InstallDir {
  if (-not [string]::IsNullOrWhiteSpace($InstallDir)) {
    return $InstallDir
  }
  $localAppData = [Environment]::GetFolderPath("LocalApplicationData")
  if ([string]::IsNullOrWhiteSpace($localAppData)) {
    $localAppData = Join-Path $HOME "AppData\Local"
  }
  return Join-Path $localAppData "Programs\idiom\bin"
}

function Add-InstallDirToPath([string] $Directory) {
  $currentSegments = @($env:Path -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  if ($currentSegments -notcontains $Directory) {
    $env:Path = "$Directory;$env:Path"
  }

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $userSegments = @($userPath -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  if ($userSegments -contains $Directory) {
    return
  }
  try {
    $newUserPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $Directory } else { "$Directory;$userPath" }
    [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    Write-Log "added to user PATH: $Directory"
  } catch {
    Write-Log "warning: could not update user PATH; run directly from $Directory"
  }
}

function Read-Checksum([string] $SumsPath, [string] $AssetName) {
  foreach ($line in Get-Content -Path $SumsPath) {
    $parts = $line -split "\s+", 2
    if ($parts.Length -ge 2 -and $parts[1].Trim() -eq $AssetName) {
      return $parts[0].Trim().ToLowerInvariant()
    }
  }
  Fail "$AssetName not listed in SHA256SUMS"
}

function Resolve-DependencyScriptRef {
  if (-not [string]::IsNullOrWhiteSpace($env:IDIOM_INSTALL_SCRIPT_REF)) {
    return $env:IDIOM_INSTALL_SCRIPT_REF
  }
  if ($resolvedVersion.StartsWith("v")) {
    return $resolvedVersion
  }
  return "main"
}

function Resolve-DependencyInstaller([string] $TempDirectory) {
  $scriptRoot = $PSScriptRoot
  if ([string]::IsNullOrWhiteSpace($scriptRoot)) {
    $scriptRoot = (Get-Location).Path
  }
  $local = Join-Path $scriptRoot "scripts\install-requirements.ps1"
  if (Test-Path $local) {
    return $local
  }

  $ref = Resolve-DependencyScriptRef
  $url = "https://raw.githubusercontent.com/$Repo/$ref/scripts/install-requirements.ps1"
  $helper = Join-Path $TempDirectory "install-requirements.ps1"
  Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $helper
  return $helper
}

function Install-ToolLaunchers([string] $Directory, [string] $TargetBinary) {
  $toolLauncher = Join-Path $Directory "idiom-tool.cmd"
  $mcpLauncher = Join-Path $Directory "idiom-mcp-discovery.cmd"
  Set-Content -Encoding ASCII -Path $toolLauncher -Value @(
    "@echo off",
    "setlocal",
    "`"%~dp0$TargetBinary`" tool %*"
  )
  Set-Content -Encoding ASCII -Path $mcpLauncher -Value @(
    "@echo off",
    "setlocal",
    "`"%~dp0$TargetBinary`" mcp serve --stdio --profile discovery %*"
  )

  & $toolLauncher list --profile discovery | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Fail "install verification failed: idiom-tool.cmd did not run"
  }

  Write-Log "installed: $toolLauncher"
  Write-Log "installed: $mcpLauncher"
  Write-Log "verified: idiom-tool list --profile discovery"
}

function Initialize-GlobalConfig([string] $Binary) {
  & $Binary init --global --merge | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Fail "global config initialization failed"
  }
  Write-Log "verified: global config initialized under ~/.idiom"
}

function Install-DiscoveryDependencies([string] $TempDirectory, [string] $InstalledBinary) {
  $helper = Resolve-DependencyInstaller $TempDirectory
  Write-Log "installing discovery dependencies with $helper"

  $args = @()
  if ($AllDependencies -or $Profile -eq "Full") {
    $args += "-All"
  }
  $args += "-NoBuild"
  $args += "-NoQueue"
  if ($NoDependencyAudit) {
    $args += "-NoAudit"
  }

  $env:IDIOM_BIN = $InstalledBinary
  & $helper @args
  if ($LASTEXITCODE -ne 0) {
    Fail "dependency installation failed"
  }
}

$resolvedVersion = Resolve-IdiomVersion
$resolvedInstallDir = Resolve-InstallDir
$assetVersion = $resolvedVersion.TrimStart("v")
$asset = "idiom_${assetVersion}_windows_amd64.zip"
$installDependencies = $WithDependencies -or $AllDependencies -or $Profile -eq "Full" -or $Profile -eq "DiscoveryDeps"
if ($NoDependencies) {
  $installDependencies = $false
}

if (-not [string]::IsNullOrWhiteSpace($ReleaseUrlBase)) {
  $base = $ReleaseUrlBase.TrimEnd("/")
  $assetUrl = "$base/$asset"
  $sumsUrl = "$base/SHA256SUMS"
} else {
  $assetUrl = "https://github.com/$Repo/releases/download/$resolvedVersion/$asset"
  $sumsUrl = "https://github.com/$Repo/releases/download/$resolvedVersion/SHA256SUMS"
}

Write-Log "downloading idiom $resolvedVersion for windows/amd64 ($Profile profile)"
Write-Log "  asset:  $assetUrl"
Write-Log "  install: $resolvedInstallDir"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("idiom-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  $assetPath = Join-Path $tmp $asset
  $sumsPath = Join-Path $tmp "SHA256SUMS"
  Invoke-WebRequest -UseBasicParsing -Uri $assetUrl -OutFile $assetPath
  Invoke-WebRequest -UseBasicParsing -Uri $sumsUrl -OutFile $sumsPath

  Write-Log "verifying SHA256 checksum"
  $expected = Read-Checksum $sumsPath $asset
  $actual = (Get-FileHash -Algorithm SHA256 -Path $assetPath).Hash.ToLowerInvariant()
  if ($actual -ne $expected) {
    Fail "checksum mismatch (got $actual, expected $expected)"
  }

  Expand-Archive -Path $assetPath -DestinationPath $tmp -Force
  $binary = Get-ChildItem -Path $tmp -Recurse -Filter "idiom.exe" | Select-Object -First 1
  if ($null -eq $binary) {
    Fail "archive did not contain idiom.exe"
  }

  New-Item -ItemType Directory -Force -Path $resolvedInstallDir | Out-Null

  $installedBinary = Join-Path $resolvedInstallDir "idiom.exe"
  if ($Profile -eq "Tools") {
    $installedBinary = Join-Path $resolvedInstallDir "idiom-tool-bin.exe"
  }
  Copy-Item -Force -Path $binary.FullName -Destination $installedBinary
  Add-InstallDirToPath $resolvedInstallDir

  if ($Profile -ne "Tools") {
    $versionOut = & $installedBinary version
    if ($LASTEXITCODE -ne 0 -or ($versionOut -notmatch [Regex]::Escape("version: $resolvedVersion"))) {
      Fail "install verification failed: idiom.exe did not report version $resolvedVersion"
    }
    Write-Log "installed: $installedBinary"
    Write-Log "verified: idiom.exe version"
  }

  if ($Profile -eq "Tools") {
    Install-ToolLaunchers $resolvedInstallDir "idiom-tool-bin.exe"
    Initialize-GlobalConfig $installedBinary
  } elseif ($Profile -eq "Full") {
    Install-ToolLaunchers $resolvedInstallDir "idiom.exe"
    Initialize-GlobalConfig $installedBinary
  } elseif ($Profile -eq "DoctorOnly") {
    Initialize-GlobalConfig $installedBinary
    $doctorOut = & $installedBinary doctor --json
    if ($LASTEXITCODE -ne 0 -and [string]::IsNullOrWhiteSpace($doctorOut)) {
      Fail "doctor verification failed: no JSON output"
    }
    if ($doctorOut -notmatch '"checks"' -or $doctorOut -notmatch '"summary"') {
      Fail "doctor verification failed: JSON output missing checks or summary"
    }
    Write-Log "verified: idiom doctor --json"
  } else {
    Initialize-GlobalConfig $installedBinary
  }

  if ($installDependencies) {
    Install-DiscoveryDependencies $tmp (Join-Path $resolvedInstallDir "idiom.exe")
  }
} finally {
  Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}
