# MCP Classification Parity

P8.5 records which client classification examples need MCP-visible proof for
resources, caller metadata, redaction, or freshness semantics.

The parseable report is
`testdata/sidecar/mcp-classification-parity.json`, validated by
`TestP85MCPClassificationParityReport` and exercised by
`TestP85MCPClassificationParityExamplesUseGenericPrimitives`.

The report has one row for each default classification. Covered rows map to
generic primitives such as caller metadata attribution, metadata-only resources,
protected-path denial, redaction, discovery-profile policy, and report-only
freshness. The local-model row is explicitly not applicable for P8.5 because
the current MCP discovery surface does not expose local-ops readiness checks.

P8.5 is not the full replayable MCP integration proof. P8.6 owns protocol,
caller metadata, roots, resources, redaction, freshness, and product-evidence
export parity in one replayable integration artifact.
