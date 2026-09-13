# Concurrent Session Architecture Proposal

Status: proposal, not a release commitment. Originally proposed on 2026-07-16.
The [current architecture](docs/architecture.md) describes implemented ownership.

This proposal explores concurrent CLI, TUI, desktop, and automation sessions.
The original comparative assessment and detailed protocol sketches remain in
Git history. They depended on a separate local repository and are not evidence
of Idiom's current capabilities.

## Candidate Changes

| Change | Reason to consider it | Required evidence before adoption |
| --- | --- | --- |
| Composite tool execution | Reduce model round trips for independent operations. | Matched-task measurements, per-step admission, bounded output, ordered results, and cancellation tests. |
| Provider request boundary | Contain protocol-specific request, stream, usage, and retry behavior. | Adapter parity tests and preservation of tool, reasoning, and capability semantics. Keep local server lifecycle separate. |
| Single-writer session store | Coordinate durable state when multiple surfaces share a session. | Transaction, recovery, idempotency, migration, and crash tests. Compare against existing persistence before adding SQLite or a service. |
| Turn router and worker scopes | Make concurrent turn admission and process ownership explicit. | One owner per process and session, no overlapping admitted turn, process-tree cancellation, restart recovery, and a viable one-shot CLI path. |
| Richer desktop and media surfaces | Support additional interaction modes on stable backend contracts. | User workflows and privacy boundaries proved independently of UI presentation. |

## Implementation Order

First measure the current CLI on representative tasks, including errors,
interruptions, timeouts, and recovery. Choose one bounded change only when those
measurements establish its value. Preserve workspace permission and evidence
contracts at every transport boundary.

A composite execution tool must classify and admit every step through workspace
policy. Parallel execution requires independent steps and an explicit policy
for failures; batching must not hide a write from approval or verification.

A session store or router should follow a concrete concurrent-session need.
Neither is required merely to extract a provider adapter or improve the TUI.
Surface packages must consume session state and events without becoming a second
persistence or process-lifecycle authority.

## Acceptance Criteria

- Existing CLI, lane, MCP, permission, evidence, and verification contracts pass.
- Canceled work cannot mutate state or grant access after losing authority.
- Retries and reconnects cannot duplicate admitted effects.
- Trace and audit output preserve metadata without exposing source or secrets.
- A failed migration preserves recoverable state and has a documented rollback.
- Performance claims use matched tasks and recorded configuration.

Implement from Idiom requirements and independently written tests. Do not copy
source, prompts, or fixtures from another project without reviewing its license
and documenting permission. The original comparison did not authorize copying.
