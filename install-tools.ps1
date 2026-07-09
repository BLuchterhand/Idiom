param(
  [string] $Version = $env:IDIOM_VERSION,
  [string] $InstallDir = $env:IDIOM_INSTALL_DIR,
  [string] $Repo = $(if ($env:IDIOM_REPO) { $env:IDIOM_REPO } else { "BLuchterhand/idiom" }),
  [string] $ReleaseUrlBase = $env:IDIOM_RELEASE_URL_BASE,
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]] $InstallerArgs
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
  $localAppData = [Environment]::GetFolderPath("LocalApplicationData")
  if ([string]::IsNullOrWhiteSpace($localAppData)) {
    $localAppData = Join-Path $HOME "AppData\Local"
  }
  $InstallDir = Join-Path $localAppData "Programs\idiom-tools\bin"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("idiom-tools-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  $scriptRoot = $PSScriptRoot
  if ([string]::IsNullOrWhiteSpace($scriptRoot)) {
    $scriptRoot = (Get-Location).Path
  }

  $installer = Join-Path $scriptRoot "install.ps1"
  if (-not (Test-Path $installer)) {
    $ref = "main"
    if (-not [string]::IsNullOrWhiteSpace($Version)) {
      $ref = $Version
    }
    $installer = Join-Path $tmp "install.ps1"
    Invoke-WebRequest -UseBasicParsing -Uri "https://raw.githubusercontent.com/$Repo/$ref/install.ps1" -OutFile $installer
  }

  $forwarded = @("-Profile", "Tools", "-InstallDir", $InstallDir, "-Repo", $Repo)
  if (-not [string]::IsNullOrWhiteSpace($Version)) {
    $forwarded += @("-Version", $Version)
  }
  if (-not [string]::IsNullOrWhiteSpace($ReleaseUrlBase)) {
    $forwarded += @("-ReleaseUrlBase", $ReleaseUrlBase)
  }
  if ($InstallerArgs) {
    $forwarded += $InstallerArgs
  }

  & $installer @forwarded
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
} finally {
  Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}
