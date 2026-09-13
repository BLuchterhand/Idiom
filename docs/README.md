# Idiom Documentation

This directory contains user guides, current implementation references, and
reproducible validation notes. Design proposals are listed separately from
shipped behavior. Private evidence bundles and local transcripts are excluded.

## User Guides

- [Getting started](getting-started.md)
- [Configuration](configuration.md)
- [CLI reference](cli.md)
- [External caller recipes](sidecar-recipes.md)
- [Troubleshooting](troubleshooting.md)

## Maintainer Guides

- [Architecture](architecture.md)
- [Development](development.md)
- [Release](release.md)
- [Release readiness review, September 2026](release-readiness-review.md)
- [Licensing](licensing.md)
- [Security and privacy posture](security-privacy-posture.md)
- [Contributing](../CONTRIBUTING.md)
- [Reporting security issues](../SECURITY.md)

## Implementation References

- [Model capability profile](design-model-capability-profile.md)
- [Run trace and replay](design-run-trace.md)
- [Compaction and resume provenance](design-compaction-provenance.md)
- [Tool execution evidence](design-tool-surface-parity.md)
- [Optional OS process sandbox](design-os-sandbox.md)

## Validation Evidence

These guides describe synthetic fixtures and commands to reproduce their
checks. They do not establish live provider quality or deployment compliance.

- [Sidecar integration proofs](sidecar-integration-proofs.md)
- [MCP classification parity](mcp-classification-parity.md)
- [Client demo transcripts](client-demo-transcripts.md)

The repository also carries `security-privacy-posture.matrix.json` because the
security posture tests validate that structured release evidence stays in sync
with the written policy.

## Proposals and History

- [Concurrent session architecture proposal](../architect.md): future work,
  with prerequisites and acceptance criteria.
- [Rearchitecture closeout](rearchitecture-reduction-plan.md): historical
  decisions; the architecture reference describes current ownership.
