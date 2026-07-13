# MCP Classification Parity

P8.5 records which client classification examples need MCP-visible proof for
resources, caller metadata, redaction, or freshness semantics.

The parseable report is
`testdata/sidecar/mcp-classification-parity.json`. The focused command runs the
`p85 mcp classification parity report` and
`p85 mcp classification parity examples use generic primitives` specs:

```bash
go test ./internal/surfaces/cli/mcp -ginkgo.focus='p85 mcp classification parity (report|examples use generic primitives)' -ginkgo.fail-on-empty -count=1
```

The report has one row for each default classification. Covered rows map to
generic primitives such as caller metadata attribution, metadata-only resources,
protected-path denial, redaction, discovery-profile policy, and report-only
freshness. The local-model row is explicitly not applicable for P8.5 because
the current MCP discovery surface does not expose local-ops readiness checks.

P8.5 is classification parity coverage. The P8.6 replay fixture covers the full
MCP integration proof: protocol, caller metadata, roots, resources, redaction,
freshness, and product-evidence export parity in one replayable artifact.
