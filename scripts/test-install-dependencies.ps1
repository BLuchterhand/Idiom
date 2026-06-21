param(
  [switch] $ManifestOnly
)

$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $repoRoot

function Add-PathEntry([string] $PathEntry) {
  if (-not [string]::IsNullOrWhiteSpace($PathEntry) -and (Test-Path $PathEntry)) {
    $parts = @($env:Path -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($parts -notcontains $PathEntry) {
      $env:Path = "$PathEntry;$env:Path"
    }
  }
}

Add-PathEntry "$env:ProgramData\chocolatey\bin"
Add-PathEntry "$env:ProgramFiles\Git\cmd"
Add-PathEntry "$env:ProgramFiles\Git\bin"
Add-PathEntry "$env:ProgramFiles\nodejs"
Add-PathEntry "$env:ProgramFiles\Go\bin"
Add-PathEntry "$env:USERPROFILE\go\bin"
Add-PathEntry "$env:APPDATA\npm"
Add-PathEntry "$env:USERPROFILE\.cargo\bin"
Add-PathEntry "$env:ProgramFiles\LLVM\bin"
Add-PathEntry "$env:USERPROFILE\.local\bin"

if (Get-Command python -ErrorAction SilentlyContinue) {
  $pythonUserScripts = python -c "import site; print(site.USER_BASE + r'\Scripts')" 2>$null
  Add-PathEntry $pythonUserScripts.Trim()
}

$expectedIds = @(
  "ast_grep",
  "clangd",
  "git",
  "go",
  "java",
  "joern",
  "madge",
  "pydeps",
  "python_lsp",
  "ripgrep",
  "rust",
  "semgrep",
  "typescript_language_server",
  "universal_ctags"
) | Sort-Object

$manifest = Get-Content -Raw -Path "scripts/install/manifest.json" | ConvertFrom-Json
$manifestIds = @($manifest.dependencies | ForEach-Object { $_.id }) | Sort-Object

if (($manifestIds -join "`n") -ne ($expectedIds -join "`n")) {
  throw "dependency coverage list does not match scripts/install/manifest.json"
}

if ($ManifestOnly) {
  Write-Host "ok: install dependency coverage list matches manifest"
  exit 0
}

function Require-Command([string] $Binary, [string] $Dependency) {
  if (-not (Get-Command $Binary -ErrorAction SilentlyContinue)) {
    throw "${Dependency}: expected ${Binary} on PATH after scripts/install-requirements.ps1 -All"
  }
  Write-Host "ok: $Dependency ($Binary)"
}

function Require-Java17 {
  Require-Command java java
  $versionText = (& java -version 2>&1 | Out-String)
  if ($versionText -notmatch 'version "([^"]+)"') {
    throw "java: could not parse Java version"
  }
  $version = $Matches[1]
  $major = ($version -split "\.")[0]
  if ($major -eq "1") {
    $major = ($version -split "\.")[1]
  }
  if ([int]$major -lt 17) {
    throw "java: expected Java 17+ after scripts/install-requirements.ps1 -All, found $version"
  }
  Write-Host "ok: java version $version"
}

Require-Command git git
Require-Command rg ripgrep
Require-Command ast-grep ast_grep
Require-Command semgrep semgrep
Require-Command joern-scan joern
Require-Command go go
Require-Command gopls go
Require-Command pyright-langserver python_lsp
Require-Command node typescript_language_server
Require-Command npm typescript_language_server
Require-Command typescript-language-server typescript_language_server
Require-Command tsc typescript_language_server
Require-Command cargo rust
Require-Command rust-analyzer rust
Require-Java17
Require-Command jdtls java
Require-Command clangd clangd
Require-Command madge madge
Require-Command pydeps pydeps

if ((ctags --version | Out-String).ToLowerInvariant() -notmatch "universal ctags") {
  throw "universal_ctags: expected ctags to be Universal Ctags"
}
Write-Host "ok: universal_ctags (ctags)"

Write-Host "ok: install dependency smoke covered all manifest dependencies"
