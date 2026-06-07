# Writer Route Decision Report-Only Implementation Plan

Status: implementation handoff for first slice, drafted 2026-05-16

Design source:

- [design-prd-adr-brief-research-workflow.md](design-prd-adr-brief-research-workflow.md), section `11. Writer Routing And Lane Dispatch`
- [design-contract-first-local-autonomy.md](design-contract-first-local-autonomy.md)
- [design-local-model-operations.md](design-local-model-operations.md)
- [lane-architecture.md](lane-architecture.md)

North-star rule:

- The model owns strategy.
- The harness owns contracts.

## Goal

Implement the first writer-routing slice: compute and emit a passive
`WriterRouteDecision` for mutation-capable execution paths.

This slice is observability-first. It must not change:

- which lane runs
- which model is called
- which tools are visible
- queue behavior
- prompt text
- plan validation
- executor behavior
- completion gates

If route emission fails, execution continues.

## Why This Slice First

The broader PRD-to-ADR workflow now ends in a cost-aware writer route before
dispatch. This first slice makes that route visible without enforcing it. That
gives the harness, TUI, audit, and evals a stable object to observe before later
slices add code drafts, exact artifact apply, hosted escalation, or product
workflow commands.

There is active TUI plan-execute integration work in this worktree. Preserve it.
The TUI may display route information, but it must not validate plans, apply
edits, synthesize `submit_plan`, decide source-satisfied state, or own
required-path completion.

## Entrypoints

Emit a route decision when JSON/events are enabled for:

- `idiom lane implement --json ...`
- `idiom lane plan-execute --json ...`
- direct `idiom implement --json ...`
- direct `idiom plan-execute --json ...`
- TUI prompt execution through `internal/surfaces/tui/app`

## Domain Type

Add a small internal package, for example `internal/contracts/writerroute`, with no TUI
dependency and no runner transcript dependency.

Suggested type:

```go
type WriterMode string

const (
    ModeAgenticGuarded             WriterMode = "agentic_guarded"
    ModePlanSubmission             WriterMode = "plan_submission"
    ModeDeterministicArtifactApply WriterMode = "deterministic_artifact_apply"
    ModeHostMaterializedArtifact   WriterMode = "host_materialized_artifact"
    ModeReadOnly                   WriterMode = "read_only"
)

type Decision struct {
    SchemaVersion      string   `json:"schema_version"`
    RouteID            string   `json:"route_id"`
    TaskFamily         string   `json:"task_family,omitempty"`
    WriterMode         string   `json:"writer_mode"`
    Lane               string   `json:"lane,omitempty"`
    PlannerModel       string   `json:"planner_model,omitempty"`
    ExecutorModel      string   `json:"executor_model,omitempty"`
    ReviewerModel      string   `json:"reviewer_model,omitempty"`
    ClassifierModel    string   `json:"classifier_model,omitempty"`
    RiskTier           string   `json:"risk_tier,omitempty"`
    CostTier           string   `json:"cost_tier,omitempty"`
    ScopeTier          string   `json:"scope_tier,omitempty"`
    VerificationOwner  string   `json:"verification_owner,omitempty"`
    ReviewPolicy       string   `json:"review_policy,omitempty"`
    RequiredEditPaths  []string `json:"required_edit_paths,omitempty"`
    EvidencePacketIDs  []string `json:"evidence_packet_ids,omitempty"`
    ReasonCodes        []string `json:"reason_codes,omitempty"`
    EscalationTriggers []string `json:"escalation_triggers,omitempty"`
    UserOverride       bool     `json:"user_override,omitempty"`
}
```

Implementation rules:

- Use schema version `idiom.writer_route.v1`.
- Normalize strings and sort set-like slices before computing IDs.
- Compute `RouteID` as a stable hash of the normalized decision, excluding
  `RouteID` itself.
- Provide `EventFields()` or equivalent so CLI/TUI event code does not
  duplicate JSON-field mapping.
- Keep the package pure: no filesystem reads, no model calls, no UI messages.

## Slice Policy

Keep routing simple and report-only.

| Current path | Writer mode | Lane | Main reason code |
| --- | --- | --- | --- |
| `implement` lane or direct `idiom implement` | `agentic_guarded` | `implement` | `route.single_file_guarded_write` when required paths are small, otherwise `route.local_model_default` |
| `plan-execute` lane or direct `idiom plan-execute` | `plan_submission` | `plan-execute` | `route.multi_file_plan_execute` |
| `artifact --no-tools`, if included opportunistically | `host_materialized_artifact` | `artifact` | `route.generated_artifact` |
| read-only TUI route families | `read_only` | current route lane | `route.read_only_question` |
| TUI implementation dispatch to plan-execute | `plan_submission` | `plan-execute` | `route.multi_file_plan_execute` |
| TUI direct implementation path | `agentic_guarded` | `implement` | `route.local_model_default` |

Default fields:

- `cost_tier`: `local_first`
- `review_policy`: `local_first`
- `scope_tier`: `single_atom` when required paths are present; otherwise omit
  or `unknown`
- `verification_owner`: `executor` for `implement`; `planner_owned` for
  current `plan-execute`
- `risk_tier`: omit, `unknown`, or `low` unless current code already knows more

Initial reason codes:

- `route.read_only_question`
- `route.single_file_guarded_write`
- `route.multi_file_plan_execute`
- `route.exact_artifact_apply`
- `route.generated_artifact`
- `route.local_model_default`
- `route.hosted_strategy_escalation`
- `route.hosted_review_escalation`
- `route.local_repair_retry`
- `route.replan_required_stale_anchor`
- `route.user_forced_lane`
- `route.queue_degraded`
- `route.context_budget_risk`

Only use reason codes supported by current evidence. For this slice, most
events should use `route.local_model_default`,
`route.single_file_guarded_write`, or `route.multi_file_plan_execute`.

## Expected File Scope

| File | Purpose |
| --- | --- |
| `internal/contracts/writerroute/decision.go` | Domain type, normalization, stable ID, event fields |
| `internal/contracts/writerroute/decision_test.go` | Unit tests for normalization, stable ID, JSON/event fields |
| `cmd/idiom/lane.go` | Emit `idiom.lane.writer_route_decision` for lane wrapper runs |
| `cmd/idiom/implement.go` | Emit `idiom.implement.writer_route_decision` for direct implement runs |
| `cmd/idiom/plan_execute.go` | Emit `idiom.plan_execute.writer_route_decision` for direct plan-execute runs |
| `internal/surfaces/tui/app/prompt_routing.go` or new `internal/surfaces/tui/app/writer_route.go` | Build and emit TUI route decision after `buildPromptExecutionPlan` |
| `internal/surfaces/tui/app/implementation_dispatch.go` | If needed, pass or annotate dispatch route without changing behavior |
| Relevant tests under `cmd/idiom` and `internal/surfaces/tui/app` | Event and route-shape coverage |

If the current TUI integration has better route display hooks, use them. Do not
move semantic checks into the TUI.

## Implementation Steps

1. Add `internal/contracts/writerroute`.

   Include constructors or helper functions that accept explicit inputs. Do not
   make this package read `.idiom/backends.json` or inspect prompts.

2. Add CLI helpers that map existing options/config into a decision.

   Direct `implement` and direct `plan-execute` can initially use
   `cfg.LlamaServer.Model` for model fields. The lane wrapper can include more
   role detail because it already resolves aliases from `.idiom/backends.json`.

3. Emit the lane-wrapper event.

   In `cmd/idiom/lane.go`, emit `idiom.lane.writer_route_decision` when
   `--json` is enabled, after project root/model role resolution and before
   forwarding into `implement` or `plan-execute`.

4. Emit direct subcommand events.

   In `cmd/idiom/implement.go`, emit `idiom.implement.writer_route_decision`
   after config/model override resolution and before queue acquisition.

   In `cmd/idiom/plan_execute.go`, emit
   `idiom.plan_execute.writer_route_decision` after config/model override
   resolution and session creation, before queue acquisition and planner call.

5. Add TUI route decision construction.

   In `internal/surfaces/tui/app`, compute a decision from `promptExecutionPlan`,
   `turnRoutePlan`, `WorkerRuntimeBrief`, current mode, config, and role specs.
   Emit it through `emitJSONEvent` when a JSON event stream is configured and
   log it as a compact message for conversation/audit visibility.

   For TUI implementation dispatch:

   - if `shouldDispatchImplementationToPlanExecute(...)` returns true, route
     mode should be `plan_submission` and lane should be `plan-execute`.
   - otherwise implementation-like families should be `agentic_guarded` and
     lane `implement`.

   Do not change `shouldDispatchImplementationToPlanExecute` behavior.

6. Add tests.

   Minimum tests:

   - `internal/contracts/writerroute`: stable ID does not change when set-like slices are
     provided in different order; JSON includes expected schema and fields.
   - `cmd/idiom/lane_test.go`: `lane plan-execute --json` emits
     `idiom.lane.writer_route_decision` with `writer_mode=plan_submission`.
   - `cmd/idiom/lane_test.go`: `lane implement --json` emits
     `idiom.lane.writer_route_decision` with `writer_mode=agentic_guarded`.
   - `cmd/idiom/implement_test.go`: direct `implement --json` emits
     `idiom.implement.writer_route_decision`.
   - `cmd/idiom/plan_execute_test.go`: direct `plan-execute --json` emits
     `idiom.plan_execute.writer_route_decision`.
   - `internal/surfaces/tui/app`: route decision for implementation dispatch to
     plan-execute is `plan_submission`; direct implementation is
     `agentic_guarded`; read-only architecture/direct-answer route is
     `read_only`.

7. Review for behavior drift.

   The diff should not change lane selection, model alias selection, queue
   acquisition, prompt text, tool visibility, plan validation, executor
   behavior, or completion gates.

## Non-Goals

- Do not add `idiom product route`.
- Do not add `idiom.code_draft.v1` implementation.
- Do not add exact artifact apply.
- Do not remove or change plan-execute auto-submit in this slice.
- Do not add hosted escalation logic.
- Do not change queue admission.
- Do not modify TUI behavior beyond displaying/logging route information.
- Do not make route decisions enforce tool permissions or completion rules.

## Acceptance Criteria

- Mutation-capable CLI paths emit a writer route decision when JSON is enabled.
- TUI prompt execution emits/logs a writer route decision without changing
  routing behavior.
- The decision includes role aliases when the current code can resolve them.
- Local-first cost policy is visible as `cost_tier=local_first`.
- No existing lane behavior changes.
- No new model calls are introduced by route computation.
- The route decision can be reconstructed from structured logs without raw
  prompt or transcript inspection.

## Verification

Run focused tests first:

```bash
go test ./internal/contracts/writerroute
go test ./cmd/idiom -run 'Lane|Implement|PlanExecute' -count=1
go test ./internal/surfaces/tui/app -run 'Route|Dispatch|ModelSwap|PromptRouting' -count=1
```

Then run broader checks if the touched files are shared:

```bash
go test ./cmd/idiom ./internal/surfaces/tui/app ./internal/runtime/backends
git diff --check
```

Run `go test ./...` only if the change touches shared config/backends/app
lifecycle behavior beyond passive event emission.

## Review Checklist

- `WriterRouteDecision` is a report-only artifact.
- Event names are stable and namespaced:
  - `idiom.lane.writer_route_decision`
  - `idiom.implement.writer_route_decision`
  - `idiom.plan_execute.writer_route_decision`
  - TUI JSON stream type `writer_route_decision`
- Route IDs are deterministic.
- TUI does not own plan/execute semantics.
- Existing user changes in the worktree are preserved.
- No raw prompt or file bodies are added to route events.
