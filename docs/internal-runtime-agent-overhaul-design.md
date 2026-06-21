# Internal Runtime Agent Overhaul Design

Status: implemented and verified
Created: 2026-06-18
Scope: `internal/runtime/agent`

## Purpose

`internal/runtime/agent` had grown into a flat, hard-to-review runtime surface.
The issue was not only the raw file count. The deeper problem was that many
tiny files and shallow subpackages described narrow implementation details while
sharing authority over the same runtime contracts: tool visibility, recovery,
source evidence, plan-execute planning, implementation safety, verification,
and final-answer acceptance.

This overhaul keeps the package behavior-oriented instead of file-oriented. A
reader should be able to answer two questions quickly:

- What runtime contract owns this behavior?
- Which small set of files must be reviewed when that contract changes?

The north-star rule remains unchanged: the model owns strategy; the harness
owns contracts.

## Implemented Shape

The package now has 15 Go files total under `internal/runtime/agent`, including
tests, with zero nested Go package directories. Production code is consolidated
into 9 files:

- six runtime surfaces
- three small root-level helper surfaces

The previously retained one-file-or-two-file agent subpackages are gone:

- `contract`, `evidence`, and `planexecute` were folded into root package files.
- `instructions` and `modepolicy` moved out to `internal/runtime/instructions`
  and `internal/runtime/modepolicy` because they are runtime-level concerns, not
  agent-private subpackages.
- all other helper packages were folded into the root runtime surfaces below.

## Runtime Surfaces

### Runner Core

`runner_core.go` is the composition root. It owns `Runner`, run/session state,
model call orchestration, recording, autonomy/budget control, terminal handling,
state strings, debug logging, child task dispatch, and the main run loop.

This file should coordinate runtime decisions, not become the owner of every
policy. New behavior belongs here only when it is session lifecycle or turn
orchestration.

### Contracts And Recovery

`contracts_recovery.go` owns phase contracts, runtime-call policy, malformed
tool policy, recovery cards, completion-gate plumbing, final-answer acceptance,
direct-answer surfaces, protocol projection, error types, and phase failure
feedback.

This is the authority boundary for "what is currently allowed" and "how the
model is told to recover after a rejected action."

### Tool Surface

`tools_surface.go` owns tool definitions, parsing, normalization, tokenization,
authorization, hidden/visible tool projection, discovery redirects, request
rewrites, and salvage of parser-safe calls from prose.

This is the authority boundary for translating model output into a typed runtime
intent and checking that intent against the active surface.

### Evidence And Read-Only Closure

`evidence_readonly.go` owns root-level evidence state, source-body and
source-owner tracking, read-only answerability, read-only closure, source
grounding, answer-quality checks, and evidence packet projection.

This is the authority boundary for deciding whether the harness has enough
source-backed evidence to accept read-only work or final-answer claims.

### Plan-Execute Runtime

`plan_execute.go` owns planner-phase state, submit-plan recovery, plan-execute
terminal shapes, plan intent bridging, runtime policy, and write-transport
recovery specific to plan-execute flows.

Root-level plan-execute behavior remains in this file because it depends on
live runner state, tool surfaces, recovery cards, and phase transitions. Pure
submit-plan decision and coverage helpers live in `plan_execute_contracts.go`
inside the same flat package.

### Implementation Flow

`implementation_flow.go` owns implementation-lane instructions, scoped write
behavior, write guards, safety/preimage checks, verification interception,
workbranch helpers, scratch notes, git-history requirements, lane policy, and
delegated implementation flow.

This is the authority boundary for write-side runtime behavior after the model
is allowed to mutate the workspace.

## File Movement Table

| Current production file | Runtime surface |
| --- | --- |
| `contract.go` | runtime contract projection and parity |
| `contracts_recovery.go` | contracts and recovery |
| `evidence_readonly.go` | evidence and read-only closure |
| `evidence_registry.go` | path evidence registry, snapshots, and session evidence |
| `implementation_flow.go` | implementation flow |
| `plan_execute.go` | plan-execute runtime |
| `plan_execute_contracts.go` | plan-execute decision, coverage, and terminal-shape helpers |
| `read_approval.go` | read-approval gate bridging the post-readiness rejection to the shared readapproval contract |
| `runner_core.go` | runner core |
| `tools_surface.go` | tool surface |

There are no nested Go files or Go package directories under
`internal/runtime/agent`. Runtime-wide mode policy and project-instruction
attachment moved to `internal/runtime/modepolicy` and
`internal/runtime/instructions`; agent-specific contract, evidence, and
plan-execute helper code is folded into the flat agent package.

## Folded Packages

The following packages were folded into the root runtime surfaces because they
were not strong package boundaries on their own:

- `agenterrors`
- `agentutil`
- `children`
- `completion`
- `completiongate`
- `implementation`
- `lanes`
- `loop`
- `recovery`
- `runtimecall`
- `safety`
- `scratchnotes`
- `surface`
- `tooling`
- `verification`
- `workbranch`
- `writeguard`
- `internal/evidenceadapter`

The fold was mechanical where possible: package-qualified call sites were
converted to root symbols, compatibility namespace values were used only when a
large call-site rewrite would have obscured the consolidation, and behavioral
changes were avoided unless required to restore broken contracts.

## Test Layout

Root package tests are consolidated by review surface:

- `runner_tests_test.go` covers runner behavior, runtime contracts, recovery,
  implementation flow, delegated read-only behavior, and regression cases that
  need package-private access.
- `contract_surface_tests_test.go` covers contract, surface, design-contract,
  and small pure policy regressions.
- `contract_test.go`, `evidence_registry_test.go`, and
  `plan_execute_contracts_test.go` cover the pure helper surfaces folded into
  the root package.
- `qwen_e2e_test.go` keeps the Qwen parser/end-to-end cases isolated.

## Review Rules For Future Changes

Use the surface owner before adding a new file:

- Add to `runner_core.go` only for run-loop lifecycle and session orchestration.
- Add to `contracts_recovery.go` for phase contract, lifecycle, completion,
  malformed-output, or recovery behavior.
- Add to `tools_surface.go` for parsing, tool visibility, authorization,
  redirects, rewrites, or tool-call salvage.
- Add to `evidence_readonly.go` for evidence state, source grounding,
  read-only answerability, or answer-quality checks.
- Add to `plan_execute.go` for planner runtime behavior that depends on live
  runner state.
- Add to `implementation_flow.go` for mutation, safety, verification,
  workbranch, scratch-note, or implementation-lane behavior.

A new subfolder is acceptable only when it introduces a reusable boundary with
more than one likely consumer, a stable public API, and tests that can run
without root runner internals. A new one-file package should be treated as a
design smell and justified in the change that introduces it.

## Verification Plan

Verification run for this overhaul:

- `go test ./internal/runtime/agent ./internal/runtime/instructions ./internal/runtime/modepolicy`
- `go test ./internal/surfaces/tui/app ./internal/surfaces/cli/e2e ./internal/surfaces/cli/probe`
- `go test ./...`
- `go build ./cmd/idiom`
- `git diff --check`
- `idiom tool harness_audit --input '{"purpose":"behavior"}'`
- `find internal/runtime/agent -maxdepth 2 -type f -name '*.go'` reports 15 files
- `find internal/runtime/agent -mindepth 1 -type d` reports zero directories

The package-level design contract enforces that every production file under
`internal/runtime/agent` appears exactly once in the movement table above.
