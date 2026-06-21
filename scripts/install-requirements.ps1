param(
  [switch] $All,
  [switch] $NoBuild,
  [switch] $NoAudit,
  [switch] $NoQueue
)

$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $repoRoot

function Add-PathEntry([string] $PathEntry) {
  if ([string]::IsNullOrWhiteSpace($PathEntry)) {
    return
  }
  if (Test-Path $PathEntry) {
    $parts = @($env:Path -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($parts -notcontains $PathEntry) {
      $env:Path = "$PathEntry;$env:Path"
    }
  }
}

function Refresh-InstallerPath {
  Add-PathEntry "$env:ProgramData\chocolatey\bin"
  Add-PathEntry "$env:ProgramFiles\Git\cmd"
  Add-PathEntry "$env:ProgramFiles\Git\bin"
  Add-PathEntry "$env:ProgramFiles\nodejs"
  Add-PathEntry "$env:ProgramFiles\Go\bin"
  Add-PathEntry "$env:USERPROFILE\go\bin"
  Add-PathEntry "$env:APPDATA\npm"
  Add-PathEntry "$env:USERPROFILE\.cargo\bin"
  Add-PathEntry "$env:USERPROFILE\.local\bin"
  Add-PathEntry "$env:ProgramFiles\LLVM\bin"

  if (Get-Command python -ErrorAction SilentlyContinue) {
    $pythonUserScripts = python -c "import site; print(site.USER_BASE + r'\Scripts')" 2>$null
    if (-not [string]::IsNullOrWhiteSpace($pythonUserScripts)) {
      Add-PathEntry $pythonUserScripts.Trim()
    }
  }

  Get-ChildItem "$env:ProgramFiles\Eclipse Adoptium" -Directory -Filter "jdk-17*" -ErrorAction SilentlyContinue |
    ForEach-Object { Add-PathEntry (Join-Path $_.FullName "bin") }
}

function Require-Command([string] $Name, [string] $Message) {
  Refresh-InstallerPath
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw $Message
  }
}

function Install-ChocoPackage([string] $Command, [string] $Package) {
  Refresh-InstallerPath
  if (Get-Command $Command -ErrorAction SilentlyContinue) {
    Write-Host "ok: $Command already installed"
    return
  }
  Require-Command choco "Chocolatey is required for Windows dependency installation."
  Write-Host "install: $Package"
  choco install -y --no-progress $Package
  Refresh-InstallerPath
}

function Install-NpmGlobal([string[]] $Packages) {
  Require-Command npm "npm is required before installing Node-backed discovery dependencies."
  $npmArgs = @("install", "-g") + $Packages
  & npm @npmArgs
  Refresh-InstallerPath
}

function Install-PipUser([string[]] $Packages) {
  Require-Command python "python is required before installing Python-backed discovery dependencies."
  python -m pip install --upgrade pip
  $pipArgs = @("-m", "pip", "install", "--user") + $Packages
  & python @pipArgs
  Refresh-InstallerPath
}

function Install-Gopls {
  Require-Command go "go is required before installing gopls."
  if (-not (Get-Command gopls -ErrorAction SilentlyContinue)) {
    go install golang.org/x/tools/gopls@latest
    Refresh-InstallerPath
  }
}

function Install-RustAnalyzer {
  Refresh-InstallerPath
  if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) {
    Install-ChocoPackage cargo rustup.install
  }
  Refresh-InstallerPath
  if (Get-Command rustup -ErrorAction SilentlyContinue) {
    rustup component add rust-analyzer
  }
  Refresh-InstallerPath
}

function Install-Jdtls {
  Refresh-InstallerPath
  if (Get-Command jdtls -ErrorAction SilentlyContinue) {
    return
  }

  Require-Command java "Java 17+ is required before installing jdtls."
  $installDir = Join-Path $env:LOCALAPPDATA "idiom\jdtls"
  $archive = Join-Path ([IO.Path]::GetTempPath()) ("jdtls-" + [Guid]::NewGuid().ToString("N") + ".tar.gz")
  Invoke-WebRequest -UseBasicParsing -Uri "https://download.eclipse.org/jdtls/snapshots/jdt-language-server-latest.tar.gz" -OutFile $archive
  Remove-Item -Recurse -Force -Path $installDir -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  tar -xzf $archive -C $installDir
  Remove-Item -Force -Path $archive -ErrorAction SilentlyContinue

  $launcher = Get-ChildItem -Path (Join-Path $installDir "plugins") -Filter "org.eclipse.equinox.launcher_*.jar" | Select-Object -First 1
  if ($null -eq $launcher) {
    throw "jdtls archive did not contain an Eclipse launcher jar."
  }

  $binDir = Join-Path $env:USERPROFILE ".local\bin"
  New-Item -ItemType Directory -Force -Path $binDir | Out-Null
  $wrapper = Join-Path $binDir "jdtls.cmd"
  Set-Content -Encoding ASCII -Path $wrapper -Value @(
    "@echo off",
    "setlocal",
    'if "%JDTLS_WORKSPACE%"=="" set "JDTLS_WORKSPACE=%TEMP%\jdtls-workspace"',
    "java -Declipse.application=org.eclipse.jdt.ls.core.id1 -Dosgi.bundles.defaultStartLevel=4 -Declipse.product=org.eclipse.jdt.ls.core.product -jar `"$($launcher.FullName)`" -configuration `"$installDir\config_win`" -data `"%JDTLS_WORKSPACE%`" %*"
  )
  Refresh-InstallerPath
}

function Install-Joern {
  Refresh-InstallerPath
  if (Get-Command joern-scan -ErrorAction SilentlyContinue) {
    return
  }

  Require-Command java "Java 17+ is required before installing Joern."
  $release = Invoke-RestMethod -Headers @{ "User-Agent" = "idiom-installer" } -Uri "https://api.github.com/repos/joernio/joern/releases/latest"
  $asset = @($release.assets | Where-Object { $_.name -eq "joern-cli.zip" -or $_.name -like "*joern-cli*.zip" } | Select-Object -First 1)
  if ($asset.Count -eq 0) {
    throw "Could not find joern-cli.zip in the latest Joern release assets."
  }

  $installDir = Join-Path $env:LOCALAPPDATA "idiom\joern"
  $archive = Join-Path ([IO.Path]::GetTempPath()) ("joern-" + [Guid]::NewGuid().ToString("N") + ".zip")
  Invoke-WebRequest -UseBasicParsing -Uri $asset[0].browser_download_url -OutFile $archive
  Remove-Item -Recurse -Force -Path $installDir -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Expand-Archive -Path $archive -DestinationPath $installDir -Force
  Remove-Item -Force -Path $archive -ErrorAction SilentlyContinue

  $scan = Get-ChildItem -Path $installDir -Recurse -File |
    Where-Object { $_.Name -in @("joern-scan.bat", "joern-scan.cmd", "joern-scan") } |
    Select-Object -First 1
  if ($null -eq $scan) {
    throw "Joern archive did not contain joern-scan."
  }

  $binDir = Join-Path $env:USERPROFILE ".local\bin"
  New-Item -ItemType Directory -Force -Path $binDir | Out-Null
  $wrapper = Join-Path $binDir "joern-scan.cmd"
  if ($scan.Extension -in @(".bat", ".cmd")) {
    Set-Content -Encoding ASCII -Path $wrapper -Value @(
      "@echo off",
      "call `"$($scan.FullName)`" %*"
    )
  } else {
    Require-Command bash "Git Bash is required to run the Joern launcher on Windows."
    Set-Content -Encoding ASCII -Path $wrapper -Value @(
      "@echo off",
      "bash `"$($scan.FullName)`" %*"
    )
  }
  Refresh-InstallerPath
}

function Require-Java17 {
  Require-Command java "Java 17+ is required for jdtls-backed Java discovery."
  $versionText = (& java -version 2>&1 | Out-String)
  if ($versionText -notmatch 'version "([^"]+)"') {
    throw "Could not parse Java version."
  }
  $version = $Matches[1]
  $major = ($version -split "\.")[0]
  if ($major -eq "1") {
    $major = ($version -split "\.")[1]
  }
  if ([int]$major -lt 17) {
    throw "Java 17+ is required for jdtls-backed Java discovery; found $version."
  }
}

function Resolve-IdiomBinary {
  if (-not [string]::IsNullOrWhiteSpace($env:IDIOM_BIN) -and (Test-Path $env:IDIOM_BIN)) {
    return $env:IDIOM_BIN
  }

  $binPath = Join-Path $repoRoot "bin\idiom.exe"
  if (Test-Path $binPath) {
    return $binPath
  }

  $rootPath = Join-Path $repoRoot "idiom.exe"
  if (Test-Path $rootPath) {
    return $rootPath
  }

  $pathCommand = Get-Command idiom.exe -ErrorAction SilentlyContinue
  if ($pathCommand) {
    return $pathCommand.Source
  }

  return ""
}

function Initialize-GlobalConfig {
  $idiom = Resolve-IdiomBinary
  if ([string]::IsNullOrWhiteSpace($idiom)) {
    throw "No idiom binary found for global config initialization. Run without -NoBuild or set IDIOM_BIN."
  }

  & $idiom init --global --merge | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "global config initialization failed"
  }
  Write-Host "Verified global config initialized under ~/.idiom"
}

Write-Host "Installing idiom Windows core discovery requirements..."
Install-ChocoPackage git git
Install-ChocoPackage rg ripgrep
Install-ChocoPackage ctags universal-ctags
Install-ChocoPackage go golang
Install-ChocoPackage node nodejs-lts
Install-ChocoPackage python python
Install-ChocoPackage clangd llvm

Install-NpmGlobal @("@ast-grep/cli")
Install-PipUser @("semgrep")
Install-Gopls
Install-Joern

if ($All) {
  Write-Host "Installing idiom Windows optional language precision backends..."
  Install-NpmGlobal @("typescript", "typescript-language-server", "pyright", "madge")
  Install-PipUser @("pydeps")
  Install-RustAnalyzer
  if (-not (Get-Command java -ErrorAction SilentlyContinue)) {
    Install-ChocoPackage java temurin17
  }
  Require-Java17
  Install-Jdtls
}

Refresh-InstallerPath

Require-Command git "git is required for history and blame tools."
Require-Command rg "ripgrep is required for fast content search."
Require-Command ast-grep "ast-grep is required for structural_search."
Require-Command ctags "Universal Ctags is required for ctags_symbols."
if ((ctags --version | Out-String).ToLowerInvariant() -notmatch "universal ctags") {
  throw "ctags is present but is not Universal Ctags."
}
Require-Command semgrep "semgrep is required for semgrep_scan."
Require-Command joern-scan "joern-scan is required for joern_scan."
Require-Command go "go is required for Go builds and Go workspace discovery."
Require-Command gopls "gopls is required for precise Go workspace tools."

if ($All) {
  Require-Command node "node is required for TypeScript, Pyright, and madge discovery helpers."
  Require-Command npm "npm is required to install TypeScript, Pyright, and madge discovery helpers."
  Require-Command typescript-language-server "typescript-language-server is required for TypeScript discovery."
  Require-Command tsc "typescript is required for TypeScript discovery."
  Require-Command pyright-langserver "pyright-langserver is required for Python discovery."
  Require-Command madge "madge is required for TypeScript dependency graph precision."
  Require-Command pydeps "pydeps is required for Python dependency graph precision."
  Require-Command cargo "cargo is required for Rust diagnostics and rust-analyzer projects."
  Require-Command rust-analyzer "rust-analyzer is required for Rust discovery."
  Require-Java17
  Require-Command jdtls "jdtls is required for Java discovery."
  Require-Command clangd "clangd is required for C/C++ discovery."
}

if (-not $NoBuild) {
  go build -o (Join-Path $repoRoot "bin\idiom.exe") ./cmd/idiom
}

Initialize-GlobalConfig

if (-not $NoAudit) {
  $idiom = Resolve-IdiomBinary
  if ([string]::IsNullOrWhiteSpace($idiom)) {
    throw "No idiom binary found for audit verification. Run without -NoBuild or install idiom first."
  }
  & $idiom tool harness_audit --input '{"purpose":"tool_surface"}'
}
