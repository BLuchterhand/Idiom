# Install Coverage Tracker

This tracker is the release-readiness checklist for install scripts and external
dependency support. Cross off an item only when an executable gate covers it.

## Release Asset And Installer Smoke Coverage

- [x] `darwin/amd64` release asset exists, checksum is present, archive contains `idiom`, and `scripts/test-install-macos.sh` verifies `install.sh`, installer-created global config, a post-install mock model call through `~/.idiom/config.json`, and `install-tools.sh` in CI/release (`release-snapshot`, `install-macos`).
- [x] `darwin/arm64` release asset exists, checksum is present, archive contains `idiom`, and `scripts/test-install-macos.sh` verifies `install.sh`, installer-created global config, a post-install mock model call through `~/.idiom/config.json`, and `install-tools.sh` in CI/release (`release-snapshot`, `install-macos`).
- [x] `linux/amd64` release asset exists, checksum is present, archive contains `idiom`, and Docker install smoke covers `install.sh`, `install-tools.sh`, doctor JSON, subcommand help, a global-config post-install mock run, and a mock lane run (`release-snapshot`, `install-linux`, `scripts/test-install-docker.sh`).
- [x] `linux/arm64` release asset exists, checksum is present, archive contains `idiom`, and Docker install smoke covers `install.sh`, `install-tools.sh`, doctor JSON, subcommand help, a global-config post-install mock run, and a mock lane run (`release-snapshot`, `install-linux`, `scripts/test-install-docker.sh`).
- [x] `windows/amd64` release asset exists, checksum is present, archive contains `idiom.exe`, and the PowerShell installers smoke `idiom.exe version`, `idiom-tool.cmd list --profile discovery`, and a post-install mock model call through installer-created global config (`release-snapshot`, `install-windows`).
- [ ] `windows/arm64` release asset and installer smoke. This architecture is not shipped by the current GoReleaser matrix.

## Installer Dependency Coverage

The source of truth for dependency ownership is `scripts/install/manifest.json`.
`scripts/test-install-dependencies.sh --manifest-only` and
`scripts/test-install-dependencies.ps1 -ManifestOnly` fail if dependency command
coverage omits a manifest dependency id.
`scripts/test-discovery-tools-e2e.sh` fails if its exercised tool list omits any
tool declared by `scripts/install/manifest.json`. The live dependency smoke runs
after the public full installer profiles, `install.sh --profile full` or
`install.ps1 -Profile Full`, and uses the installed snapshot CLI, not the source
checkout binary.
The same E2E smoke fails if installer-initialized global config artifacts under
`~/.idiom` are missing.

- [x] Manifest shape, dependency ownership, installer package references, audit backend references, and current doctor scope are covered by `go test ./internal/evidence/sidecarvalidation`.
- [x] Release and source installers run `idiom init --global --merge`, creating missing `~/.idiom` config, backend, prompt, external-tool, and postflight placeholders while preserving existing files.
- [x] Linux Docker install smoke and cross-platform discovery E2E smoke assert that global config bootstrap artifacts exist after install.
- [x] macOS install smoke runs through `scripts/test-install-macos.sh` on both `macos-15-intel` and `macos-15`, using a clean per-test `HOME` and the installed snapshot binary.
- [x] `scripts/test-post-install-harness.py` runs the installed CLI from a clean workspace without `--config`, temporarily patches installer-created `~/.idiom/config.json` to a local OpenAI-compatible mock, verifies the mock receives chat completions, and verifies `.idiom/runs/<session>.completion.json` is written.
- [x] `macOS/amd64` full installer dependency installation plus installed-CLI discovery smoke is covered in CI/release by `install-dependencies-macos` on `macos-15-intel`.
- [x] `macOS/arm64` full installer dependency installation plus installed-CLI discovery smoke is covered in CI/release by `install-dependencies-macos` on `macos-15`.
- [x] `Linux/amd64` full installer dependency installation plus installed-CLI discovery smoke is covered in CI/release by `install-dependencies-linux` on `ubuntu-latest`.
- [x] `Linux/arm64` full installer dependency installation plus installed-CLI discovery smoke is covered in CI/release by `install-dependencies-linux` on `ubuntu-24.04-arm`.
- [x] `Windows/amd64` full installer dependency installation plus installed-CLI discovery smoke is covered in CI/release by `install-windows` on `windows-latest`.
- [x] `git` is installed and exercised through `history_range`, `history_symbol`, and `blame_range`.
- [x] `ripgrep` / `rg` is installed and exercised through `search_content`.
- [x] `ast-grep` is installed and exercised through `structural_search`.
- [x] Universal Ctags is installed, version-checked, and exercised through `ctags_symbols`.
- [x] `semgrep` is installed and exercised through `semgrep_scan`.
- [x] `joern-scan` is installed and exercised through `joern_scan`.
- [x] Go and `gopls` are installed and exercised through Go symbol discovery and `dependency_graph`.
- [x] Node, npm, `pyright-langserver`, TypeScript, `typescript-language-server`, and `madge` are installed and exercised through Python/TypeScript symbol discovery and TypeScript dependency graph precision.
- [x] `pipx` and `pydeps` are installed and exercised through Python dependency graph precision.
- [x] Rust, Cargo, and `rust-analyzer` are installed and exercised through Rust symbol discovery and `dependency_graph`.
- [x] Java 17+ and `jdtls` are installed or verified and exercised through Java symbol discovery.
- [x] `clangd` is installed or verified and exercised through C/C++ symbol discovery.
- [x] Every discovery tool listed in `scripts/install/manifest.json` is
  exercised by `scripts/test-discovery-tools-e2e.sh`; the script self-checks
  that list against the manifest before running tool calls.
## Maintenance Rules

- Add a manifest entry before claiming support for a new external backend.
- Update `scripts/test-install-dependencies.sh` and
  `scripts/test-install-dependencies.ps1` in the same change as any manifest
  dependency id change.
- Keep CI and release workflows in sync for shipped install paths.
