# Idiom Efficiency Optimization Ledger

Effort id: `idiom-efficiency-optimization`

Source design: `docs/design-direct-answer-efficiency.md`

Status: partially implemented; blocked on A1 and A2; A5 reopened and implemented

## Checklist

- [x] Full pre-implementation reviewer bench complete
- [x] Reviewer outputs stored durably
- [x] Design revised or accepted risks recorded
- [x] Phases atomized into dispatchable units
- [x] Every atom has expected files, forbidden files, tier, evidence, TDD,
      tests, audit signal, rollback, implementor evidence, reviewer evidence,
      and investigation paths
- [ ] Parallel implementation agents complete
- [ ] Per-atom reviewer evidence complete
- [x] Focused tests complete for retained A3/A4/A6 changes
- [ ] Docs freshness note complete
- [ ] Final full-bench review complete

## Implementation Status

| Atom | Status | Evidence |
| --- | --- | --- |
| A6 lightweight direct-answer surface | implemented; A6 reviewer `PASS_WITH_RISKS` | `agents/A6-implementation.md`, `agents/A6-review.md` |
| A3 hosted DeepSeek reasoning protocol | implemented; focused tests pass | `agents/A3-implementation.md` |
| A4 effective model telemetry | implemented; focused tests pass | `agents/A4-implementation.md` |
| A1 exact-path prefetch | blocked; partial agent edits reverted | `agents/A1-implementation.md` |
| A2 citation classifier | blocked; partial agent edits reverted after final-answer acceptance regressions | `agents/A2-implementation.md` |
| A5 hosted cache/cost metrics | implemented after redesign and full review alignment | `agents/A5.4-benchmark-adapter.md`, `reviews/a5-cache-cost/final-alignment.md` |

Retained verification:

- `go test ./internal/runtime/llama ./internal/contracts/writerroute ./internal/surfaces/cli/route ./internal/surfaces/tui/app -run 'DeepSeek|WriterRoute|Decision|EffectiveModel' -count=1`
- `go test ./internal/runtime/agent -run 'DirectAnswerSurface|DirectAnswer|Lane' -count=1`
- `go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/tui/app ./internal/evidence/transcript ./internal/surfaces/cli/lane -run 'Usage|Cache|Reasoning|SessionSummary|ModelCall|BudgetLedger|Explain|PostAnalyze' -count=1`
- `go test ./internal/evidence/transcript -run 'DebugLogger|SessionTelemetry' -count=1`
- `go build ./cmd/idiom`

## Drift-Risk Closeout

The 2026-06-11 final drift rerun returned `PASS_WITH_RISKS` with three risks.
They are handled as follows:

- Runner-level A1/A6 disabled-gate coverage:
  `TestRunnerDirectAnswerSurfaceKeepsPrefetchDisabled` now runs a direct-answer
  session, verifies the emitted `direct_answer_surface` event does not
  advertise `source_body_prefetch`, and verifies no prefetch events are emitted
  before A1 activation.
- A6 non-trivial read-only investigation oracle:
  `TestRunnerDirectAnswerSurfaceAllowsSearchThenBoundedRead` now runs a
  direct-answer session through `search_content` followed by bounded
  `read_file`, proving the lightweight surface still permits model-led
  investigation before a grounded answer.
- Dirty-diff noise:
  retained effort files remain limited to A3/A4/A6 paths listed above. Existing
  dirty files outside the effort are preserved and explicitly treated as
  independent pre-existing work; no unrelated revert was attempted.

Additional verification after risk closeout:

- `go test ./internal/runtime/agent -run 'DirectAnswerSurface|DirectAnswer|Lane' -count=1`

The first benchmark rerun after risk closeout exposed one additional telemetry
drift risk: direct `idiom -config ... -prompt ...` runs requested
`llama_server.model=deepseek-v4-flash`, but `writer_route_decision.effective_model`
reported the repo role alias `deepseek-v4-pro`. The broken invariant was
telemetry authority: role aliases are routing context, while explicit runtime
config/specs are the actual direct-run model selection. The fix keeps aliases
visible in `role_aliases` but makes CLI and TUI writer-route model resolution
prefer concrete runtime config/specs before falling back to aliases.

Additional verification after telemetry drift closeout:

- `go test ./internal/surfaces/cli/route ./internal/surfaces/tui/app ./internal/contracts/writerroute ./internal/runtime/llama -run 'WriterRoute|Decision|EffectiveModel|DeepSeek' -count=1`
- `go test ./internal/runtime/agent -run 'DirectAnswerSurface|DirectAnswer|Lane' -count=1`

Known blockers:

- A1 requires a completed A6/A1 source-body prefetch activation design. The
  current A6 surface intentionally does not advertise `source_body_prefetch`,
  so A1 must not activate yet.
- A2 requires a narrower shared citation-authority integration point. The first
  attempt caused existing final-answer acceptance regressions, so it was
  reverted.
- A5 requires a complete usage/cost contract across `llama.Usage`, runner
  aggregation, and session/lane summaries. The first attempt only partially
  edited `client.go` and agent metrics, so it was reverted.

## Baseline Evidence

- Benchmark artifacts: `.idiom/benchmark/20260611-122151`
- Context packet: `edc474559a0d4803`
- Context packet artifact: `.idiom/comprehension/views/edc474559a0d4803.json`
- Freshness: fresh after read-through refresh on 2026-06-11

## Reviewer Bench

| Reviewer | Status | Evidence |
| --- | --- | --- |
| Incident Root-Cause | PASS_WITH_RISKS | `reviews/incident-root-cause.md` |
| Model Runtime / Config | PASS_WITH_RISKS | `reviews/model-runtime-config.md` |
| Harness / Go | PASS_WITH_RISKS | `reviews/harness-go.md` |
| Prompt / Protocol | PASS_WITH_RISKS | `reviews/prompt-protocol.md` |
| TDD / Acceptance | PASS_WITH_RISKS | `reviews/tdd-acceptance.md` |
| North-Star / Architecture | PASS_WITH_RISKS | `reviews/north-star-architecture.md` |
| Model-Agnostic / Cross-Backend | PASS_WITH_RISKS | `reviews/model-agnostic-cross-backend.md` |

## Alignment Review

All seven alignment reviewers passed after conditions were folded into the
design:

- `reviews/alignment/incident-root-cause.md`
- `reviews/alignment/model-runtime-config.md`
- `reviews/alignment/harness-go.md`
- `reviews/alignment/prompt-protocol.md`
- `reviews/alignment/tdd-acceptance.md`
- `reviews/alignment/north-star-architecture.md`
- `reviews/alignment/model-agnostic-cross-backend.md`

## Implementation Dispatch Allowance

Implementation agents were initially held after alignment because the first
required atom, A6, overlaps dirty existing work in `internal/runtime/agent/**`.
On 2026-06-11, the user explicitly requested parallelized implementation and
full review approval. That request authorizes a dirty-scope allowance with the
following guardrails:

- implementation agents must preserve existing uncommitted work;
- implementation agents must block rather than overwrite on unsafe overlap;
- A6 remains the first dependency boundary for direct-answer surface inventory;
- later atoms may run in parallel only when their file scope is disjoint from
  dirty or active A6 edits, or when they record a typed blocker instead of
  editing.

Observed dirty overlap before dispatch:

- `internal/runtime/agent/runner.go`
- `internal/runtime/agent/runner_plan_then_execute_guard.go`
- `internal/runtime/agent/lane_policy_test.go`
- `internal/runtime/agent/runner_runstate.go`
- `prompts/assets.go`

Dispatch plan: run A6 first, review A6, then run disjoint atoms in parallel
subject to the dirty-scope guardrails. If A6 blocks on existing work, stop and
record the blocker instead of broadening ownership.

## Folded Reviewer Conditions

- A6 must publish a concrete direct-answer surface inventory before
  implementation.
- A6 must run before A1; A1 consumes the A1/A6 prefetch data contract.
- A1 and A6 must share an integration replay test before either atom is marked
  complete.
- A3 must include a synthetic `reasoning_content` fixture.
- A4 must propagate a typed resolved model identity from route to telemetry.
- A5 is a session-summary contract repair; cache/cost fields must be present
  with explicit unknown/zero state when unsupported.
- A2 and final-answer acceptance must share one citation-authority helper.
- TDD alignment requires A1/A6 negative fixtures, an A6 inventory enforcement
  test, parameterized A3 reasoning fixtures, concrete A5 sentinel states, and a
  cross-condition regression suite before the related gates are accepted.

## Atom Drafts

```json
[
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A6",
    "phase": "lightweight-direct-answer-surface",
    "state": "planned",
    "complexity_tier": "C4",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/runtime/agent/**", "prompts/**"],
    "forbidden_files": ["internal/runtime/llama/client.go"],
    "blast_radius": "model-facing direct-answer protocol and visible tool surface",
    "tdd_obligations": ["direct-answer surface excludes mutation protocol", "read-only investigation remains possible", "surface inventory is emitted and test-covered", "stale or missing surface inventory disables A1 prefetch with a typed miss"],
    "required_tests": ["focused direct-answer runtime tests", "benchmark replay"],
    "audit_signal_expected_to_move": ["lower direct-answer prompt-token floor without blocker leaks"],
    "rollback_note": "restore existing build-mode direct-answer surface",
    "implementor_evidence": "agents/A6-implementation.md",
    "reviewer_evidence": "agents/A6-review.md",
    "post_effort_investigation": "agents/A6-investigation.md"
  },
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A1",
    "phase": "exact-path-prefetch-bounded-read",
    "state": "planned",
    "complexity_tier": "C3",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/runtime/agent/*read_only*", "internal/runtime/agent/runner*.go"],
    "forbidden_files": ["prompts/**", "docs/evidence/**/reviews/**"],
    "blast_radius": "read-only direct-answer evidence setup and tool-call sequence",
    "tdd_obligations": ["exact path is prefetched", "simple doc summary uses bounded read", "broad questions can still request more evidence", "A1 consumes the A6 surface prefetch contract", "missing/excluded paths produce typed misses", "malformed prefetch packets are rejected by negative fixtures"],
    "required_tests": ["go test ./internal/runtime/agent -run 'DirectAnswer|ReadOnly|Evidence'"],
    "audit_signal_expected_to_move": ["lower search-before-read count on exact-path direct-answer benchmark"],
    "rollback_note": "disable prefetch policy and return to model-selected read/search flow",
    "implementor_evidence": "agents/A1-implementation.md",
    "reviewer_evidence": "agents/A1-review.md",
    "post_effort_investigation": "agents/A1-investigation.md"
  },
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A2",
    "phase": "citation-classifier",
    "state": "planned",
    "complexity_tier": "C3",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/runtime/agent/final_answer_acceptance.go", "internal/runtime/agent/*final*test.go", "internal/runtime/agent/*evidence*test.go"],
    "forbidden_files": ["internal/runtime/llama/**"],
    "blast_radius": "read-only final-answer acceptance and source-grounding status",
    "tdd_obligations": ["command placeholders do not fail accepted evidence", "false source citations still fail", "one shared citation-authority helper is used by acceptance and completion status"],
    "required_tests": ["go test ./internal/runtime/agent -run 'FinalAnswer|Evidence|Citation|Ground'"],
    "audit_signal_expected_to_move": ["fewer source_grounding_unmet false positives"],
    "rollback_note": "remove classifier helper and restore current accepted-path extraction",
    "implementor_evidence": "agents/A2-implementation.md",
    "reviewer_evidence": "agents/A2-review.md",
    "post_effort_investigation": "agents/A2-investigation.md"
  },
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A3",
    "phase": "hosted-deepseek-reasoning-protocol",
    "state": "planned",
    "complexity_tier": "C4",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/runtime/llama/client.go", "internal/runtime/llama/*test.go"],
    "forbidden_files": ["internal/runtime/agent/final_answer_acceptance.go"],
    "blast_radius": "hosted chat protocol history and backend-specific request shaping",
    "tdd_obligations": ["DeepSeek reasoning_content history replay is valid", "synthetic reasoning_content fixture covers the failure", "parameterized reasoning_content fixtures cover omitted/empty/normal/multiline/special/truncated/streamed cases", "non-DeepSeek hosted/local behavior remains unchanged"],
    "required_tests": ["go test ./internal/runtime/llama"],
    "audit_signal_expected_to_move": ["no DeepSeek reasoning_content invalid request in replay"],
    "rollback_note": "revert backend-specific history repair at client boundary",
    "implementor_evidence": "agents/A3-implementation.md",
    "reviewer_evidence": "agents/A3-review.md",
    "post_effort_investigation": "agents/A3-investigation.md"
  },
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A4",
    "phase": "effective-model-telemetry",
    "state": "planned",
    "complexity_tier": "C2",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/surfaces/cli/route/**", "internal/runtime/agent/*telemetry*", "internal/surfaces/cli/lane/**"],
    "forbidden_files": ["internal/runtime/llama/client.go"],
    "blast_radius": "route and model-call attribution telemetry",
    "tdd_obligations": ["profile alias and effective model id are both represented", "resolved model identity is owned by route state and consumed by telemetry"],
    "required_tests": ["focused route/runtime telemetry tests"],
    "audit_signal_expected_to_move": ["benchmark JSONL reports deepseek-v4-flash as effective model"],
    "rollback_note": "remove additive telemetry fields while preserving existing alias fields",
    "implementor_evidence": "agents/A4-implementation.md",
    "reviewer_evidence": "agents/A4-review.md",
    "post_effort_investigation": "agents/A4-investigation.md"
  },
  {
    "schema_version": "idiom.design_doc_implementation_atom.v1",
    "effort_id": "idiom-efficiency-optimization",
    "task_id": "A5",
    "phase": "hosted-cache-cost-metrics",
    "state": "planned",
    "complexity_tier": "C2",
    "context_evidence": ["context_pack:edc474559a0d4803"],
    "expected_files": ["internal/runtime/llama/client.go", "internal/runtime/agent/*metrics*", "internal/surfaces/cli/lane/**"],
    "forbidden_files": ["prompts/**"],
    "blast_radius": "usage parsing and session summary metrics",
    "tdd_obligations": ["hosted cache/cost fields parse and aggregate", "missing fields remain zero/unknown without failure", "session summary always exposes cache/cost field names", "known/unknown sentinel fields are concrete and testable"],
    "required_tests": ["go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/cli/lane"],
    "audit_signal_expected_to_move": ["hosted benchmark emits cache/cost fields when provider supplies them"],
    "rollback_note": "remove additive usage fields and aggregations",
    "implementor_evidence": "agents/A5-implementation.md",
    "reviewer_evidence": "agents/A5-review.md",
    "post_effort_investigation": "agents/A5-investigation.md"
  }
]
```
