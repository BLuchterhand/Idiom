# Install Coverage Tracker

This tracker is the release-readiness checklist for install scripts and external
dependency support. Cross off an item only when an executable gate covers it.

## Release Asset And Installer Smoke Coverage

- [x] `darwin/amd64` release asset exists, checksum is present, archive contains `idiom`, and `install.sh` plus `install-tools.sh` smoke in CI/release (`release-snapshot`, `install-macos`).
- [x] `darwin/arm64` release asset exists, checksum is present, archive contains `idiom`, and `install.sh` plus `install-tools.sh` smoke in CI/release (`release-snapshot`, `install-macos`).
- [x] `linux/amd64` release asset exists, checksum is present, archive contains `idiom`, and Docker install smoke covers `install.sh`, `install-tools.sh`, doctor JSON, subcommand help, and a mock lane run (`release-snapshot`, `install-linux`, `scripts/test-install-docker.sh`).
- [x] `linux/arm64` release asset exists, checksum is present, archive contains `idiom`, and Docker install smoke covers `install.sh`, `install-tools.sh`, doctor JSON, subcommand help, and a mock lane run (`release-snapshot`, `install-linux`, `scripts/test-install-docker.sh`).
- [x] `windows/amd64` release asset exists, checksum is present, archive contains `idiom.exe`, and the PowerShell sidecar installer smokes `idiom-tool.cmd list --profile discovery` (`release-snapshot`, `install-windows`).
- [ ] `windows/arm64` release asset and installer smoke. This architecture is not shipped by the current GoReleaser matrix.
- [ ] Full Windows CLI installer smoke. The current Windows gate covers sidecar tooling only, not a first-class `idiom.exe` installer profile.

## Installer Dependency Coverage

The source of truth for dependency ownership is `scripts/install/manifest.json`.
`scripts/test-install-dependencies.sh --manifest-only` fails if this tracker
script omits a manifest dependency id. The live dependency smoke runs after
`scripts/install-requirements.sh --all`.

- [x] Manifest shape, dependency ownership, installer package references, audit backend references, and current doctor scope are covered by `go test ./internal/evidence/sidecarvalidation`.
- [x] `macOS/amd64` default and `--all` dependency installation plus dependency smoke is covered in CI/release by `install-dependencies-macos` on `macos-15-intel`.
- [x] `macOS/arm64` default and `--all` dependency installation plus dependency smoke is covered in CI/release by `install-dependencies-macos` on `macos-15`.
- [x] `Linux/amd64` default and `--all` dependency installation plus dependency smoke is covered in CI/release by `install-dependencies-linux` on `ubuntu-latest`.
- [x] `Linux/arm64` default and `--all` dependency installation plus dependency smoke is covered in CI/release by `install-dependencies-linux` on `ubuntu-24.04-arm`.
- [x] `git` is verified as a host precondition for history/blame tools.
- [x] `ripgrep` / `rg` is installed and verified for fast content search.
- [x] `ast-grep` is installed and verified for structural search.
- [x] Universal Ctags is installed and verified with a Universal Ctags version probe.
- [x] `semgrep` is installed and verified for Semgrep scans.
- [x] `joern-scan` is installed and verified for Joern scans.
- [x] Go and `gopls` are installed and verified for Go discovery.
- [x] Node, npm, `pyright-langserver`, TypeScript, `typescript-language-server`, and `madge` are installed and verified for Python/TypeScript language discovery and TypeScript dependency graph precision.
- [x] `pipx` and `pydeps` are installed and verified for Python dependency graph precision.
- [x] Rust, Cargo, and `rust-analyzer` are installed and verified for Rust discovery.
- [x] Java 17+ and `jdtls` are installed or verified for Java discovery.
- [x] `clangd` is installed or verified as the C/C++ discovery backend available after the macOS/Linux `--all` path.
- [ ] Windows dependency installer for the manifest backends. Current Windows release gate covers sidecar tool installation only.

## Maintenance Rules

- Add a manifest entry before claiming support for a new external backend.
- Update `scripts/test-install-dependencies.sh` in the same change as any manifest dependency id change.
- Keep CI and release workflows in sync for shipped install paths.
- Do not check off Windows dependency installation until that platform has executable dependency installer gates comparable to `install-dependencies-macos` and `install-dependencies-linux`.
