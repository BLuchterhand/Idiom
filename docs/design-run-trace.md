# Redacted Run Trace and Replay

Status: accepted for implementation

## Purpose

Idiom has model-call traces, tool telemetry, evidence, and contract replay. This
feature gives them one stable, privacy-safe causal graph so a run can be
diagnosed and replayed without reconstructing order from unrelated logs.

## Authority and boundaries

- `internal/evidence/trace` owns trace IDs, span lifecycle, redaction, and
  serialization.
- Runtime packages emit semantic events but do not format trace JSON.
- `internal/evidence/eval` consumes traces for trajectory assertions.
- Raw prompts, source bodies, secrets, and unredacted tool payloads are never
  part of the default trace schema.

## Contract

The trace is a versioned sequence of immutable spans. Each span has:

`trace_id`, `span_id`, `parent_span_id`, `run_id`, `kind`, `status`, start and
duration, and metadata-only attributes.

Span kinds are `run`, `turn`, `model_call`, `tool_request`, `policy_decision`,
`tool_result`, `evidence`, `verification`, `compaction`, and `resume`.

Relationships must form one rooted acyclic graph. A child cannot be written
without an active parent. Terminal spans cannot receive children.

## Privacy and durability

Trace attributes use the existing evidence vocabulary: IDs, paths, hashes,
counts, status, policy codes, command labels, token counts, cache counts, and
durations. Values are redacted before serialization. `Recorder.MarshalJSON`
provides the stable document boundary; the current runner keeps the recorder
in memory and leaves durable artifact ownership to the caller that already
owns the run-event/audit sink. This avoids creating a second persistence
authority while retaining a factually inspectable trace.

## Replay

Replay consumes a trace plus deterministic fake backend/tool fixtures. Replay
does not rerun shell commands or write files. It checks trajectory contracts:

- every tool request has a policy decision and result;
- every admitted mutation has approval or an explicit non-approval policy;
- every terminal answer has the required evidence and verification;
- retries, compactions, and cancellations obey their budgets;
- no span has an invalid parent or duplicate terminal transition.

## Tests and end-to-end acceptance

- Unit tests cover IDs, parent validation, terminal lifecycle, redaction, and
  deterministic serialization.
- Contract tests cover incomplete and malformed traces.
- A CLI fake-backend run must produce a trace containing model, tool, policy,
  evidence, and verification spans.
- The same trace must replay successfully without touching the workspace.
- A deliberately unapproved write and an orphan tool result must fail replay.
