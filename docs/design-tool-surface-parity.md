# Tool-Surface Policy and Evidence Parity

Status: accepted for implementation

## Purpose

Workspace tools, sidecars, and MCP tools must have the same enforceable
preflight, execution, cancellation, redaction, evidence, and postflight
contracts. Transport must not change authorization semantics.

## Authority and boundaries

- `internal/capabilities/workspace` owns tool request classification and
  mutation admission.
- `internal/capabilities/mcp` translates MCP calls into the shared tool
  contract; it owns protocol lifecycle only.
- `internal/evidence` owns privacy-safe tool evidence.
- CLI/TUI/MCP surfaces format shared decisions and do not mint new policy codes.

## Contract

Every tool invocation has a `ToolExecutionEnvelope` with invocation ID, source
transport, request fingerprint, preflight decision, approval requirement,
execution status, cancellation status, result classification, evidence IDs,
and postflight verification status.

The lifecycle is:

`received -> classified -> admitted|denied|approval_required -> running ->
completed|failed|canceled -> evidenced -> verified|unverified`.

No transport may bypass classification. A denied or approval-required request
must not execute. Cancellation must remove pending MCP calls and terminate
owned subprocesses. Result evidence must use the existing metadata-only policy.

## Tests and end-to-end acceptance

- Unit tests cover lifecycle transitions and invalid transitions.
- Workspace, sidecar, and MCP fixtures must produce equivalent policy and
  evidence classifications for the same logical request.
- CLI and MCP end-to-end tests cover allowed read, denied protected read,
  approval-required write, cancellation, timeout, and redacted result output.
- An MCP transport failure must leave no pending call or active trace span.
