# Release readiness review — 2026-09-13

This is a dated review of the source tree, not a claim that a public release or
all native installer jobs have run. The earlier conversation-continuity change
was reviewed, given stronger regression coverage, and pushed separately as
`08bd82ec4`.

## Changes made

| Area | Finding and repair |
| --- | --- |
| Licensing | Added GPL-3.0-only and consistent desktop metadata. GNU GPLv3 is the selected release license. |
| Repository secrets | Scanned the prospective non-ignored source and all reachable Git history with Gitleaks. The initial 13 historical findings were reviewed synthetic fixtures or a Git revision mistaken for a token; four appeared in the current tree. No live credential was identified. Exceptions require exact reviewed lines and specific paths. No live credential required removal; historical private material is covered below. |
| Evidence privacy | Shared redaction now handles quoted credentials, environment-style field names, modern provider keys, Basic authorization and URL credentials. Transcript and telemetry writers sanitize structured data at persistence. Trace output rejects unsupported compound metadata. New log files use owner-only Unix permissions; resumed debug logs are tightened. |
| Queue ownership | Granted leases survive the end of admission; waiting cancellation still works. The handshake and diagnostic round trips terminate on cancellation/time limits. Startup refuses active daemon sockets and serializes stale-socket recovery across processes. Concurrent shutdown is serialized and cannot remove a replacement daemon's socket. |
| Platform defaults | Default workspace shells now match the host: POSIX sh on Unix and PowerShell on Windows. Execution flags match the explicitly selected shell family. |
| Dependency security | Updated Go 1.26.2 to 1.26.8, goldmark 1.7.13 to 1.7.17, and x/text 0.38.0 to 0.39.0, with necessary transitive tool updates. These resolve the 14 reachable findings in the initial Go scan. Updated four flagged desktop npm dependencies. |
| Distribution | Pull requests and tags share verification and installer workflows. Tags cannot bypass source/security checks. Publication uploads the installer-tested archives. Windows native command failures propagate. Archives include complete dependency notices; GPL releases include vendored corresponding source. |
| Documentation | Removed a superseded consolidation ledger and condensed historical plans. Corrected claims about OS sandboxing, model capability discovery, trace replay, resume provenance, and tool evidence. Fixed navigation and portable setup instructions. |
| Test environment | Integration fixtures now retain the running Go compiler and module cache even when login shells change PATH or fixtures isolate the home directory. Assertions and production behavior remain unchanged. |

The Windows locale-reader dependency was advanced to
`v0.0.2-0.20220822084749-2491eb6c1c75`: comparison against v0.0.1 showed its only
addition was the missing MIT license file. Release notices cover all five
platform targets and the Go runtime (210 notice files). A second scan of the
vendored source archive found 18 generated Windows API declarations in SQLite;
these are exact, reviewed false positives, with no directory-wide exception.

## Verification

The following checks passed locally:

- `go test ./...`, focused privacy/shell/documentation tests, and repeated queue
  race tests; Linux and Windows queue test cross-compilation.
- `make test-race-contracts`, `make test-e2e`,
  `make test-development-scenarios`, and `make test-system-hermetic`.
- `go vet ./...`, `go build ./cmd/idiom`, `go mod verify`, and `make verify-mocks`.
- Gitleaks scans of source, reachable history, and the vendored source archive,
  after review of the exact exceptions; `govulncheck ./...` reported no findings.
- Desktop npm audit and production web build; Rust audit reported no
  vulnerabilities, with the informational advisories listed below.
- Actionlint, GoReleaser configuration validation, 13 archive-validator regression
  tests, five-platform snapshot builds, and checksum/notice inventory validation.
- The corresponding source archive built with vendored dependencies and
  downloads disabled. The native macOS archive ran version and discovery-tool
  listing commands.
- Public documentation local-link checks and `git diff --check`.

Integration test families were run serially: concurrent Ginkgo CLI runs share
package test-binary filenames. A transient process-fixture startup timeout under
combined load passed three focused repetitions and the subsequent full suite.

The [release guide](release.md) contains the reproducible commands. Evidence
logs remain local; this report does not copy raw conversations, tool output,
credentials, or machine-specific paths into the repository.

## Remaining release work and limits

- Run the native installer matrix on the exact release tag before publishing.
  Cross-compilation and a macOS executable smoke do not prove native Windows or
  Linux installation.
- The existing repository remains private. Its history still contains 137
  `.book` paths and 225 `docs/evidence` paths removed from the current tree.
  Do not make that history public without a separate cleanup decision. The
  source archive has no Git history; it can seed a fresh public repository while
  preserving private development history. Alternatively, review and rewrite a
  separate mirror before any coordinated force-push.
- No tagged release or repository-visibility change was performed in this
  review. Source archives come from Git and must match the committed binaries.
- OS process sandbox profiles remain an optional API without production wiring.
  Tool permissions and workspace contracts are the currently enforced boundary.
- Log redaction is best-effort. Full debug mode and explicit Markdown transcript
  exports retain content; existing unrelated logs are not retroactively purged.
  Unix mode bits do not establish Windows ACL guarantees.
- The experimental desktop is outside the CLI release archives. Its Rust audit
  reported zero vulnerabilities, with six unmaintained-dependency advisories
  and the glib 0.18.5 soundness advisory RUSTSEC-2024-0429 still present. These
  need desktop-specific dependency review before a desktop production release.
- macOS notarization, Windows signing, and signed build provenance are not
  configured. Installer checksums detect corruption but are not independent
  publisher signatures.
- Live model quality, deployment-specific MCP authorization, and regulated-use
  certification were not established by this source review.
