# Release

Idiom ships source and CLI archives under [GPL-3.0](../LICENSE). The desktop
application is an experimental source project, not part of these CLI archives.
The [README](../README.md#supported-platforms) owns the supported platform list.

## Required checks

Use the Go patch version in `go.mod` or a newer supported patch. Install the
pinned release tools used by CI:

```bash
go install github.com/zricethezav/gitleaks/v8@v8.30.0
go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
go install github.com/google/go-licenses/v2@v2.0.1
go install github.com/goreleaser/goreleaser/v2@v2.18.1
```

Run from the repository root, with Go's binary directory on `PATH`:

```bash
git diff --check
go mod verify
go vet ./...
go test ./...
go build ./cmd/idiom
make verify-mocks
make test-e2e
make test-development-scenarios
make test-race-contracts
bash scripts/check-secrets.sh
govulncheck ./...
python3 scripts/test-release-assets.py
npm --prefix desktop ci --ignore-scripts
npm --prefix desktop audit --audit-level=moderate
npm --prefix desktop run build
bash scripts/collect-release-licenses.sh
go mod vendor
goreleaser check
goreleaser release --snapshot --clean --skip=publish
python3 scripts/validate-release-assets.py dist
bash scripts/test-release-source.sh dist
```

The secret check scans the prospective non-ignored source tree and all reachable
Git history. `.gitleaks.toml` retains upstream detectors and allows only exact
reviewed fixture lines, generated Windows API declarations in vendored SQLite,
and a historical Git revision. Do not allowlist a real
credential, an entire test tree, or an entire historical commit. Revoke exposed
credentials first; deleting a current file does not remove earlier revisions.
History rewriting requires a coordinated publication decision because it
changes commit identities and other people's clones.

Generated `dist/`, `vendor/`, and `third-party-licenses/` directories are ignored. License
collection covers all five build targets and includes the linked Go runtime's
license. Review collector warnings about assembly or other non-Go dependencies.
A completed root `LICENSE`, README, binary, and dependency notices are required
in every binary archive. A matching source archive includes the exact Git
revision and vendored Go dependencies. Build previews from a committed tree so
the source archive matches the binaries; GoReleaser takes source from Git.
Validation checks actual SHA-256 digests and rejects unsafe
archive paths, duplicate entries, mixed versions, and unexpected assets.

## Public repository history

The current tree and a source archive can be clean while old commits still
contain private material. Before changing repository visibility, review all
reachable history. This repository's September 2026 review found historical
book and local evidence files; see the [dated review](release-readiness-review.md).
Keep the development repository private and seed a fresh public repository from
the reviewed source archive, or prepare a separate sanitized mirror for review
before rewriting published history. A clean secret scan alone does not clear
private manuscripts or evidence bundles for publication.

## Tag publication

`.github/workflows/verify.yml` owns the shared source checks used by pull
requests and release tags. A tag release must pass that workflow before building
archives. `release-artifacts.yml` owns packaging and the Linux, macOS, and
Windows installer matrix used by both pull requests and release tags. All
installer jobs exercise those exact archives. Only the publication job has repository write permission;
it downloads, validates, and uploads the tested archives without rebuilding.

After reviewing a clean, tested checkout, create and push a version tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Pushing a `v*` tag starts publication. Creating a GitHub Release by itself does
not trigger this workflow. Tags containing a hyphen are published as prereleases.
See [licensing](licensing.md) for corresponding-source and notice requirements.

The installers verify checksums delivered alongside the archives. This detects
corruption but is not an independent publisher signature. macOS notarization,
Windows signing, and signed provenance are not currently configured.

## Release checklist

- Public docs contain no dead internal links or unsupported capability claims.
- Private workspaces, local logs, transcripts, indexes, outputs, and model files
  are absent from the tracked tree and archives.
- Tracked configuration contains placeholders or environment-variable names.
- No unreviewed secret or reachable vulnerability findings remain.
- Tests pass from the exact commit being tagged; generated mocks are current.
- Supported-platform installer jobs pass for the archives being published.
- The corresponding source archive builds with `go build -mod=vendor ./cmd/idiom`.
- Third-party notices are complete for the supported targets; contribution and
  copied-code rights permit distribution under the chosen license.
- Release notes describe user-facing changes and known limitations.

## P9.9 security privacy gate

Decision: pending until the required checks and platform installer jobs pass
for the release tag. A local build alone is not release approval.

Threat model and data-flow: [security and privacy posture](security-privacy-posture.md).
The [control matrix](security-privacy-posture.matrix.json) owns the structured
control map. Privacy evidence should preserve status fields, hashes, and
sanitized paths rather than raw prompts, source bodies, credentials, or customer
data. Run the focused posture commands in that document alongside this checklist.
