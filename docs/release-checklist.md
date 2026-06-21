# Release Checklist

Use this checklist before cutting a public `v*` release. It covers cleanup,
fixup, correctness, packaging, documentation, dependency, and release-evidence
topics. Items marked `Blocker` must be fixed or explicitly waived in the release
notes before tagging.

Prepared from the repository audit on 2026-06-21. Current review status:
**not ready to tag**. Core Go verification passed in the audit, and this
release is scoped to the CLI. Release metadata, portable configuration, and
license/patent posture, packaging evidence, and audit findings still need
decisions or fixes.

## Status Legend

- `[ ]` Open
- `[~]` Needs release-owner decision
- `[x]` Done for the current release candidate
- `Blocker` Must be closed or waived before tag

## 1. Release Scope

- [x] Release artifact scope: CLI-only.
  - This release ships the `idiom` CLI archives built by GoReleaser.
  - Desktop/Tauri sources remain in the repository but are not release-supported
    for this tag.
  - Do not advertise desktop installation, desktop runtime support, Tauri app
    packaging, notarization, or Cargo-level release readiness.
- [x] Release version: `v0.1.0`.
  There are no local `v*` tags in the audited checkout, so the installer cannot
  resolve a latest release until `v0.1.0` is pushed and release assets exist.
- [x] Release claims: `v0.1.0` is a CLI developer preview.
  - Do not make commercial, production, enterprise, or hosted-support claims.
  - Existing P9 evidence gates remain blockers for those claims, not blockers
    for a bounded CLI developer-preview binary release.
- [x] Release-note baseline:
  `v0.1.0 is a developer-preview release of the idiom CLI for local
  experimentation with agent harness workflows, repository context, evidence
  capture, and release/readiness tooling. It is not a production, hosted,
  enterprise-support, or desktop application release.`

## 2. Blocking Fixups

- [ ] Blocker: Decide patent filing posture before publishing a broader source
  release or final root license.
  - Review the local ignored `.patent/` package.
  - Either file a provisional application, or explicitly waive patent filing for
    `v0.1.0`.
  - Do not use "patent pending" wording unless counsel approves it after filing.
- [ ] Blocker: Add the chosen root license file or correct package metadata.
  The Tauri package declares `MIT`, while the root archive currently has no
  tracked `LICENSE` file.
- [ ] Blocker: Include the license in release archives once the license file is
  added.
- [x] Fixed the missing GoReleaser release-notes link. `.goreleaser.yaml`
  now links to the current design roadmap.
- [x] Removed machine-local model paths from tracked configuration.
  `.idiom/config.json` and `.idiom/backends.json` no longer contain user-local
  absolute model paths or private DeepSeek local-model paths.
- [x] Removed local model artifacts from the repository workspace.
  The ignored Qwen GGUF files were moved to a user-local model directory
  outside the repository. `models/` is not tracked in the current index, and no
  historical `models/` paths were found in Git history during this cleanup.
- [x] Checked-in config policy: `.idiom/config.json` is a portable
  developer-preview config. Machine-local overrides belong in ignored
  `config.json` files or another `--config` / `IDIOM_CONFIG` target.
- [x] Checked-in backend examples include one hosted profile and one local
  placeholder profile.
  - Hosted example: `deepseek-v4-flash` using `DEEPSEEK_API_KEY`.
  - Local example: `local-llama-example` using `models/local-model.gguf`.
- [x] Checked-in local-model defaults are placeholders, not real model artifact
  names.
  `Makefile`, `config.example.json`, `.idiom/config.json`,
  `internal/platform/config`, and `opencode.json` use neutral local model
  names or `models/local-model.gguf`.
- [ ] Blocker: Resolve or waive high `harness_audit` findings before making
  trust, audit, or contract-readiness claims.
  - `turn_phase_contract_contradiction`
  - `contract_event_privacy_forbidden_raw_content`
  - degraded or unavailable language/discovery backends that affect claimed
    coverage
- [x] Desktop/Tauri is out of scope for this release.
  - Record the known desktop blockers as post-release work:
    missing `desktop/src-tauri/icons/icon.png` and missing `Clone` support for
    `IdiomEvent`.
  - Release notes must exclude desktop install/build claims.

## 3. Documentation Freshness

- [x] Updated README installer guidance for Linux/apt support.
- [x] Updated README configuration claims for the portable tracked config and
  local classifier overrides.
- [x] Aligned `install.sh` comments with the raw GitHub install path used by
  README.
- [x] Updated `docs/obsidian/13 - Package Guide.md` package paths to the
  current `internal/surfaces/cli/...` layout.
- [x] Removed tracked generated Obsidian export output and added the export path
  to `.gitignore`.
- [x] Confirmed release metadata links current docs. GoReleaser now links the
  active design roadmap instead of an archived/missing reusability design.
- [ ] Update known limitations in README/release notes for any waived blocker.

## 4. Cleanup Before Tag

- [ ] Run `git status --short` and confirm the tree is clean except for
  intentional release edits.
- [ ] Run `git clean -Xdn` and review ignored cleanup candidates. Do not delete
  ignored model files, logs, or generated work directories without explicit
  operator confirmation.
- [ ] Remove local build/test binaries if present, such as root `idiom`,
  `idiom-analyze`, `agent.test`, `refine.test`, `bin/`, `desktop/dist/`, and
  `desktop/src-tauri/target/`, when preparing a handoff or packaging workspace.
- [ ] Confirm no generated `desktop/src-tauri/Cargo.lock` or
  `desktop/src-tauri/gen/` was unintentionally created by a failed Tauri check.
- [ ] Confirm tracked source size and generated documentation exports are
  intentional. The audit found tracked source is small relative to local ignored
  model artifacts, but generated docs need a clear policy.
- [x] Confirm actual local model files are outside the release tree. Public
  examples may mention model identifiers or placeholder paths, but checked-in
  configs must not reference private local model files.

## 5. Correctness Gates

Run these from the repository root and paste the command result or artifact path
into the release issue.

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
- [ ] `go mod tidy -diff`
- [ ] `git diff --check`
- [ ] `go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json`
- [ ] `idiom tool context_status --input '{"refresh":true,"limit":40}'`
  - Expected: fresh index, zero stale paths, zero missing paths.
- [ ] `idiom tool harness_audit --input '{"purpose":"release readiness"}'`
  - Expected: no unresolved high findings, or a written waiver with release
    impact.
- [ ] `go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-product-evidence.json`
- [ ] `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/release-candidate.json`
- [ ] Review release evidence fields used by the P9 gate docs:
  `release_gate_linkage`, `release_readiness`, `blockers`, `evidence_ids`,
  product-evidence counts, and classification coverage.
- [~] If a live `idiom run --prompt ... --json` smoke test is required, run it
  in an environment with a real TTY. The audited noninteractive run failed with
  `/dev/tty` unavailable.

## 6. Packaging And Installer Gates

- [x] Confirm CI remains aligned with CLI-only scope.
  - `.github/workflows/ci.yml` and `.github/workflows/release.yml` currently
    build GoReleaser CLI archives for darwin, linux, and windows.
  - They do not run npm, Cargo, Tauri, desktop packaging, macOS notarization, or
    app installer jobs.
  - Do not add desktop/Tauri gates to this release unless the release scope is
    reopened.
- [x] Add a full-system discovery smoke gate before making
  "all language discovery tools work after install" claims.
  - CI verifies release archive install on Linux, macOS, and Windows.
  - CI installs and PATH-checks discovery dependencies through the public full
    installer profiles: `install.sh --profile full` on Linux/macOS and
    `install.ps1 -Profile Full` on Windows, plus the matching
    `scripts/test-install-dependencies.*` verifier.
  - Installers initialize missing global config artifacts with
    `idiom init --global --merge`; existing `~/.idiom` files are preserved.
  - Docker install smoke and cross-platform discovery E2E smoke assert that
    `~/.idiom/config.json`, `backends.json`, prompt templates, external-tool
    placeholders, and postflight placeholders exist after install.
  - CI and release install jobs run `scripts/test-post-install-harness.py`
    against the installed binary. The smoke temporarily routes
    installer-created `~/.idiom/config.json` to a local mock model, runs
    `idiom run` without `--config` from a clean workspace, verifies the mock
    receives `/v1/chat/completions`, and verifies the run completion artifact.
  - macOS release-installer proof is centralized in
    `scripts/test-install-macos.sh`, which runs on both configured macOS
    runners with a clean per-test `HOME` and the installed snapshot binary.
  - CI now runs the installed snapshot CLI against real Go, Python,
    TypeScript, Rust, Java, C/C++, structural-search, ctags, Semgrep, Joern,
    `madge`, and `pydeps` fixtures through
    `scripts/test-discovery-tools-e2e.sh`.
  - The smoke script self-checks that it exercises every discovery tool listed
    in `scripts/install/manifest.json`, including definition, reference,
    diagnostics, call graph, git status/diff/history/blame, dependency graph,
    security scan, ctags, and structural search adapters.
  - Windows has full CLI, sidecar, dependency installation, and discovery E2E
    smoke coverage for `windows/amd64`.
- [ ] Install or provide GoReleaser locally, then run `goreleaser check`.
  The audited machine did not have `goreleaser` on `PATH`.
- [ ] Run `goreleaser release --snapshot --clean --skip=publish`.
- [ ] Verify release archives contain the binary, README, license, and expected
  docs only.
- [ ] Run the Linux installer test path used by CI.
- [ ] Run the macOS installer path or document why the release is Linux-only.
- [ ] Run the Windows installer path if Windows release assets are advertised.
- [ ] Confirm `.github/workflows/release.yml` will run on the chosen `v*` tag.
- [ ] Confirm `.github/workflows/ci.yml` passes on the release commit.
- [ ] After publish, verify `install.sh` can resolve the latest release asset.
- [ ] Verify pinned install with `IDIOM_VERSION=<tag>`.
- [ ] Verify fork/custom base install knobs if release notes mention them:
  `IDIOM_REPO`, `IDIOM_INSTALL_DIR`, and `IDIOM_RELEASE_URL_BASE`.

## 7. Dependency And Security Review

- [ ] Run `go list -m -u all` and triage available updates.
  - Security and compatibility updates should block release until reviewed.
  - Broad major upgrades can be deferred if no security or compatibility issue
    is present.
- [ ] Run `govulncheck ./...` or document why it is unavailable.
- [ ] Run `npm audit --prefix desktop --json` if desktop sources stay in the
  release tree.
- [ ] Run `npm outdated --prefix desktop --json` and decide whether to take
  patch updates. The audit found desktop dependencies with newer patch and major
  versions available.
- [~] If desktop is in scope, run `cargo check` and a Rust vulnerability check
  such as `cargo audit` or `cargo deny`, and decide whether the Tauri
  `Cargo.lock` should be tracked for the shipped app.

## 8. Bad-Practice And Maintainability Review

- [ ] Review the largest runtime and test files before release notes claim
  maintainability improvements. The audit found several very large files in
  `internal/runtime/agent` and `internal/capabilities/workspace`.
- [ ] Do not block a narrow CLI release on broad refactors unless the changed
  release surface touches those files. Instead, file post-release refactor work
  with owner, risk, and verification scope.
- [ ] Before changing prompts, routing, readiness, recovery, audit, or control
  rails, record the harness-owned invariant and evidence that justifies the
  change.
- [ ] Add regression tests for any release-blocking bug fix. Prefer behavior
  assertions over implementation-detail assertions.
- [ ] Confirm stale docs and generated exports do not contradict the public
  release story.

## 9. Final Release Review

- [ ] All blockers are closed or waived with owner, reason, release impact, and
  follow-up issue.
- [ ] CI is green on the exact release commit.
- [ ] Release evidence bundle is generated and reviewed.
- [ ] Release notes include:
  - supported platforms
  - install command
  - version pinning command
  - known limitations
  - evidence-backed claims only
  - upgrade or rollback notes
- [ ] Create an annotated tag: `git tag -a vX.Y.Z -m "idiom vX.Y.Z"`.
- [ ] Push the tag and watch the release workflow.
- [ ] After assets publish, test fresh install from GitHub Releases.
- [ ] Archive the final checklist results with the release evidence bundle.

## Current Audit Snapshot

Passed during the 2026-06-21 audit:

- `go test ./...`
- `go vet ./...`
- `go build ./...`
- `go mod tidy -diff`
- `git diff --check`
- Representative SWE suite: 44 tasks passed
- `npm run --prefix desktop build`
- `npm audit --prefix desktop --json` with zero reported vulnerabilities
- `idiom tool context_status --input '{"refresh":true,"limit":40}'` refreshed
  the index with zero stale or missing paths

Failed or unavailable during the audit:

- `goreleaser check`: `goreleaser` was not installed.
- `idiom run --prompt ... --json`: failed in the noninteractive environment
  because `/dev/tty` was unavailable.
- `cargo check` in `desktop/src-tauri`: failed on missing icon and `IdiomEvent`
  clone support.

Release-owner recommendation:

1. Close license, missing link, portable configuration, and documentation
   freshness blockers first.
2. Decide whether desktop is in scope.
3. Re-run correctness and release evidence gates.
4. Install GoReleaser and run the snapshot packaging path.
5. Tag only after CI and installer verification pass on the release commit.
