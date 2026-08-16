# Compaction and Resume Provenance

Status: accepted for implementation

## Purpose

Context compaction must preserve the facts required to continue safely. A
summary that merely fits the context window is not sufficient evidence that a
run can resume correctly.

## Authority and boundaries

- `internal/runtime/agent` owns compaction selection and context assembly.
- `internal/evidence` owns the durable provenance record.
- Resume uses the existing invocation and workflow recovery authorities; this
  feature does not create a second persistence system.

## Contract

Each compaction produces a `ContextCompactionProvenance` record containing:

- source message IDs and retained message IDs;
- evicted message count and character/token estimates;
- retained evidence paths, obligations, approvals, and verification facts;
- summary hash and strategy;
- reason, budget before/after, and profile fingerprint;
- reconstruction status: `verified`, `degraded`, or `rejected`.

Required facts are selected from the active run contract. A compaction is
rejected if it cannot retain or accurately summarize required paths, pending
approvals, active violations, or verification obligations.

## Resume semantics

Resume must load the latest accepted provenance record before accepting a new
model turn. A stale or rejected record blocks resume and requires fresh context
assembly. Compaction records are append-only and tied to the invocation ID.

## Tests and end-to-end acceptance

- Unit tests cover retention selection, required-fact loss, hashes, and status
  transitions.
- Existing compaction tests gain assertions for provenance and reconstruction.
- A fake model run forced below its context budget must compact, persist a
  provenance event, resume, and complete verification.
- A fixture that drops a required path must fail closed and must not permit a
  terminal completion.
