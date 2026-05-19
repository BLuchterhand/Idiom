param(
  [string] $Version = $env:IDIOM_VERSION,
  [string] $InstallDir = $env:IDIOM_INSTALL_DIR,
  [string] $Repo = $(if ($env:IDIOM_REPO) { $env:IDIOM_REPO } else { "BLuchterhand/idiom" }),
  [string] $ReleaseUrlBase = $env:IDIOM_RELEASE_URL_BASE
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
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "idiom-tools-installer" }
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
  return Join-Path $localAppData "Programs\idiom-tools\bin"
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

$resolvedVersion = Resolve-IdiomVersion
$resolvedInstallDir = Resolve-InstallDir
$assetVersion = $resolvedVersion.TrimStart("v")
$asset = "idiom_${assetVersion}_windows_amd64.zip"

if (-not [string]::IsNullOrWhiteSpace($ReleaseUrlBase)) {
  $base = $ReleaseUrlBase.TrimEnd("/")
  $assetUrl = "$base/$asset"
  $sumsUrl = "$base/SHA256SUMS"
} else {
  $assetUrl = "https://github.com/$Repo/releases/download/$resolvedVersion/$asset"
  $sumsUrl = "https://github.com/$Repo/releases/download/$resolvedVersion/SHA256SUMS"
}

Write-Log "downloading idiom tool sidecar $resolvedVersion for windows/amd64"
Write-Log "  asset:  $assetUrl"
Write-Log "  install: $resolvedInstallDir\idiom-tool.cmd"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("idiom-tools-install-" + [Guid]::NewGuid().ToString("N"))
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
  $installedBinary = Join-Path $resolvedInstallDir "idiom-tool-bin.exe"
  Copy-Item -Force -Path $binary.FullName -Destination $installedBinary

  $toolLauncher = Join-Path $resolvedInstallDir "idiom-tool.cmd"
  $mcpLauncher = Join-Path $resolvedInstallDir "idiom-mcp-discovery.cmd"
  Set-Content -Encoding ASCII -Path $toolLauncher -Value @(
    "@echo off",
    'setlocal',
    '"%~dp0idiom-tool-bin.exe" tool %*'
  )
  Set-Content -Encoding ASCII -Path $mcpLauncher -Value @(
    "@echo off",
    'setlocal',
    '"%~dp0idiom-tool-bin.exe" mcp serve --stdio --profile discovery %*'
  )

  Add-InstallDirToPath $resolvedInstallDir

  & $toolLauncher list --profile discovery | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Fail "install verification failed: idiom-tool.cmd did not run"
  }

  Write-Log "installed: $toolLauncher"
  Write-Log "installed: $mcpLauncher"
  Write-Log "verified: idiom-tool list --profile discovery"
} finally {
  Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}
