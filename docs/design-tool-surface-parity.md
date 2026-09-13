# Tool Execution Evidence

Status: implemented shared envelope and transport parity tests.

[`internal/capabilities/workspace`](../internal/capabilities/workspace) owns
request admission, execution, and `ToolExecutionEnvelope`. CLI and MCP adapters
route requests through that owner. MCP also owns its protocol lifecycle and
cancellation of pending calls.

## Envelope Contract

An envelope records schema version, invocation ID, transport, a request
fingerprint, tool name, preflight decision, approval requirement, execution
status, cancellation, result code/failure, evidence paths, whether evidence was
produced, and verification status. It contains no request or result body.

The fingerprint covers selected routing fields. It excludes commands and edit
bodies and must not be used as proof that two mutations are identical.

Execution statuses include received, admitted, denied, approval-required,
running, completed, failed, and canceled. Evidence production and verification
are separate fields. Observed verification evidence is reported as `observed`;
it does not independently establish that the whole task passed verification.

Admission remains workspace policy's responsibility. Envelope validation checks
schema and required identity fields; it is not a separate lifecycle policy
engine. A denied or approval-required request must not execute.

## Verification

```bash
go test ./internal/capabilities/workspace ./internal/capabilities/mcp
go test ./internal/surfaces/cli/tool ./internal/surfaces/cli/mcp
```

See [sidecar integration proofs](sidecar-integration-proofs.md) for replayable
CLI/MCP fixtures and their limits.
