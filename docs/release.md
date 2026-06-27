# Release

This repository is being prepared for a public source release. Release work
should keep private planning material, local evidence, and generated artifacts
out of version control.

## Required Checks

Run these before cutting a release:

```bash
git diff --check
go test ./...
go build ./cmd/idiom
goreleaser release --snapshot --clean
gitleaks dir . --redact --no-banner
```

If GoReleaser is unavailable, run the closest available build and archive smoke
checks for the target platform.

## Release Checklist

- Public README and docs contain no dead internal links.
- `.book/`, `.patent/`, local logs, transcripts, indexes, build outputs, and
  model files are absent from the tracked tree.
- Config files contain placeholders or environment-variable names only.
- Secret scanning has no unreviewed findings.
- Tests pass from a clean checkout.
- Installer scripts and release assets are smoke-tested on supported platforms.
- Release notes describe user-facing changes and known limitations.

## P9.9 Security Privacy Gate

Decision: pending until required checks pass.

Threat model: see `docs/security-privacy-posture.md`.

Data-flow: see the posture document and matrix for local artifact, telemetry,
model backend, sidecar, and MCP evidence boundaries.

Privacy: public release artifacts must avoid raw prompts, raw file bodies,
secrets, private repository URLs, customer identifiers, and private local paths.

Control mapping: `docs/security-privacy-posture.matrix.json` is the structured
control map for release review.

Reproduction Commands: run the required checks above plus the focused posture
commands in `docs/security-privacy-posture.md`.

## Supported Platforms

The README is the source of truth for supported release platforms. Do not claim
support for an OS or architecture until an installer path and CI or manual
release gate exist.
