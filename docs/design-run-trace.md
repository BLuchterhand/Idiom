# Run Trace and Replay

Status: implemented in-memory trace foundation.

[`internal/evidence/trace`](../internal/evidence/trace) owns trace IDs, spans,
serialization, and structural replay. The runtime emits model, tool, and related
events into a recorder. `Recorder.MarshalJSON` exports a document; the caller
that owns run artifacts remains responsible for persistence.

## Current Contract

A document contains a schema version, trace/run identity, and spans with parent
IDs, kinds, status, timing, and attributes. Supported kinds include run, turn,
model call, tool request/result, policy, evidence, verification, compaction, and
resume. The recorder rejects additions under a terminal parent.

Replay validates trace structure and tool-result relationships without running
commands or writing the workspace. It does not replay model behavior or prove
approval, source support, verification sufficiency, and retry budgets. Those
remain the responsibilities of runtime contracts and evaluation checks.

## Privacy Boundary

Runtime trace producers should supply only metadata such as IDs, hashes, counts,
status, and policy codes. A trace is not an approved channel for arbitrary prompt,
source, command output, or credential values. Review exported artifacts before
sharing them; the trace does not replace the
[security and privacy posture](security-privacy-posture.md).

## Verification

```bash
go test ./internal/evidence/trace
```

Tests cover recorder relationships, serialization, exclusion of raw run-event
payloads, and rejection of malformed relationships. Durable trace export and
full task-trajectory verification require evidence from their owning callers.
