# Sidecar Integration Proofs

P7.8 records three integration paths that consume the same sidecar profile,
schema, freshness, audit, and contract fields. The records live in
[`testdata/sidecar/integration-proofs.json`](../testdata/sidecar/integration-proofs.json)
and are validated by `go test ./internal/evidence/sidecarvalidation -run TestP78IntegrationProofRecords`.

## Proof Paths

- `shell_ci_script`: `scripts/sidecar-ci-proof.sh` and the
  `context-status-report-only` / `context-status-refresh-denied` transcript
  steps prove a CI caller can parse `idiom.tool.v1`, discovery profile,
  caller metadata, report-only freshness, audit ID arrays, and typed profile
  denials without matching prose.
- `external_coding_agent_workflow`: the
  `external-harness-context-query` transcript step proves a Codex, Claude Code,
  OpenCode, Aider-style, or custom caller can consume graph evidence through
  explicit CLI JSON input without prompt interpolation. Idiom reports findings;
  the caller decides what to do with them.
- `mcp_stdio`: the
  [`mcp-stdio-integration-proof.json`](../testdata/sidecar/mcp-stdio-integration-proof.json)
  replay fixture proves protocol initialize, `tools/list`, allowed
  `tools/call`, structured denial, `roots/list`, `resources/list/read`, caller
  metadata, `transport:"mcp_stdio"`, protected-resource denial, report-only
  freshness, and P7.11 product-evidence export linkage.

MCP resources remain metadata-only. File bodies are not returned until resource
body redaction is implemented safely.

The records are generic by design: they expose local evidence contracts for
platform, tooling, open-source, and public-sector review without adding
vertical-specific branches or exporting raw prompts, file bodies, credentials,
private paths, or private repository/customer identifiers.
