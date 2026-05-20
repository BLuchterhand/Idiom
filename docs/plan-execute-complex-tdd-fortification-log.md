# Plan-Execute Complex TDD Fortification Log

Date: 2026-05-20

## Purpose

Track the concrete code locations and failure modes for fortifying
`plan-execute` on complex multi-file TDD work. This log is intentionally
implementation-facing: it records where the contract is weak, what owns the
fix, and how regressions should prove the fix.

## Findings

1. Planner readiness is too coarse for complex TDD.
   - Location: `internal/agent/runner_plan_then_execute_guard.go`
   - Current failure: `code_navigate` `route_summary: Read ...` text could mark
     a required path source-ready even when the result contained no source body.
   - Fix: source readiness now requires real routed source-body output for
     `code_navigate` / `read_only_capability`; route summaries alone remain
     orientation evidence only.

2. Rejected `submit_plan` recovery is missing typed coverage details.
   - Locations:
     - `internal/agent/runner_plan_then_execute_guard.go`
     - `internal/agent/runner.go`
   - Current failure: recovery inferred missing paths and invalid anchors from
     prose. This allowed invalid-anchor refreshes but initially blocked bounded
     reads for required paths omitted from a rejected plan.
   - Fix: the plan-execute coverage guard now emits structured JSON diagnostics
     in the tool result output; recovery consumes those fields first and keeps
     prose parsing only as legacy fallback.

3. Placeholder edit bodies are not classified as first-class plan failures.
   - Location: `internal/agent/runner_plan_then_execute_guard.go`
   - Current failure: placeholders such as `// ... existing code ...`, TODOs,
     pseudo-code, or schema examples could surface as invalid anchors or generic
     incomplete plan coverage.
   - Fix: placeholder edit bodies are now detected in the preflight coverage
     guard, reported per path, and included in typed diagnostics.

4. Submit-plan synthesis must not disable local-model visible output.
   - Location: `internal/agent/runner.go`
   - Current failure: recovery states inherited or forced `EnableThinking=false`,
     which can cause local Qwen backends to return empty visible answers after
     readiness. That made the completion gate loop instead of receiving a
     visible/native `submit_plan`.
   - Needed fix: keep current-response thinking available for submit-plan
     synthesis while still setting `PreserveThinking=false` so prior hidden
     reasoning does not stack.

5. Post-rejection read recovery needs a narrow allow-list.
   - Locations:
     - `internal/agent/runner_plan_then_execute_guard.go`
     - `internal/agent/runner.go`
   - Current failure: after a partial plan, missing required paths need a narrow
     refocus route instead of repeatedly forcing the same corrected-plan error.
   - Fix: allow exactly one bounded source-body refresh for invalid-anchor paths
     and omitted required paths that only had symbol/body-fragment evidence;
     if the omitted path already had a concrete `read_file` body before the
     rejected plan, force a corrected `submit_plan`. Broad search, unbounded
     reads, repeated reads, and out-of-scope paths stay rejected.

6. TUI/fallback surfaces can amplify internal recovery text.
   - Locations:
     - `internal/app/implementation_dispatch.go`
     - `internal/app/model_state.go`
     - `internal/app/prompt_routing.go`
     - `internal/app/prompt_turn_test.go`
   - Current failure: nested in-process plan-execute prompt runs logged their
     internal planner prompt and blocker answer as ordinary transcript messages,
     then the outer TUI surfaced a second sanitized blocker.
   - Fix: nested prompt runs now suppress transcript user/assistant messages
     and log redacted run events instead. Required-source recovery also runs
     before active-model fallback so fallback does not replay a known source
     blocker.

7. Submit-plan failures still have broader typed-error gaps outside the
   plan-execute coverage guard.
   - Locations:
     - `internal/workspace/submit_plan.go`
     - `internal/workspace/plan_artifact_schema.go`
     - `internal/workspace/tool_sidecar_envelope.go`
     - `cmd/idiom/plan_execute.go`
   - Current failure: workspace validation, artifact schema validation, audit
     findings, executor preflight, and parse errors still collapse some typed
     facts into prose.
   - Status: logged for the next typed-error tranche. The live required-path
     blocker is covered by the structured coverage guard, but the same design
     should be extended to the workspace/tool-result envelope.

8. Multi-file submit-plan synthesis can monopolize the TUI.
   - Location: `internal/agent/runner.go`
   - Current failure: a live TUI run reached readiness cleanly, then spent
     several minutes in the local model call because ordinary multi-file plans
     inherited a 16k token generation budget.
   - Fix: ordinary multi-file ready-to-submit recovery now uses a short 1k
     responsiveness budget; the 16k budget is preserved only when the
     objective/evidence explicitly indicates new-file, whole-file, replace-all,
     or large body work.

9. Stringified `submit_plan.edits` repair was too brittle after rejection.
   - Locations:
     - `internal/workspace/manager.go`
     - `internal/workspace/tool_request_test.go`
     - `internal/agent/runner_plan_then_execute_guard.go`
     - `internal/agent/runner_plan_then_execute_guard_test.go`
   - Current failure: a live TUI run produced a first repaired-but-invalid plan,
     then recovery consumed the one permitted source refresh and hid read tools.
     The subsequent corrected plan attempts emitted `edits` as a quoted string;
     safe trailing prose around a valid array could still collapse into a parse
     error, and the post-rejection message still advertised source reads after
     the refresh had already been spent.
   - Fix: stringified structured-field repair now extracts a valid leading edit
     array when the only tail is clearly explanatory text, while still rejecting
     malformed code-like tails that could drop edit content. Post-rejection
     recovery now states whether a refresh exception remains or whether the only
     valid next action is corrected `submit_plan` / source-satisfied / blocker.

10. Correction-only multi-file plan synthesis can still monopolize the TUI.
    - Location: `internal/agent/runner.go`
    - Current failure: after a rejected placeholder plan and the one permitted
      source refresh, the local planner entered a long submit-plan-only
      synthesis call. At that point no more discovery is allowed, so a long
      local generation no longer buys new evidence and only blocks outer TUI
      recovery.
    - Fix: rejected-plan correction-only recovery now uses a short 1k token
    budget. This lets a valid corrected `submit_plan` through when the model
    can produce it quickly, but prevents the TUI from being monopolized by a
    brittle exact multi-file JSON synthesis attempt.

11. Budget and completion status can mislabel invalid-plan loops as source-read
    loops.
    - Locations:
      - `internal/agent/runner.go`
      - `internal/agent/runner_plan_then_execute_guard_test.go`
    - Current failure: after every required path had source-body evidence, two
      rejected `submit_plan` attempts could still surface as
      `completion_gate_required_paths_remaining_repeated`. That made the live
      TUI failure look like another source-read/readiness bug when the actual
      issue was invalid plan synthesis.
    - Fix: plan-execute completion statuses now distinguish unread source-body
      obligations, missing visible `submit_plan`, and repeated invalid
      `submit_plan` attempts.

## Remaining Structural Issue

The latest live TUI run still did not complete a complex multi-file TDD task.
The local planner read the required paths, but continued to produce either
empty visible answers at source-read gates or invalid exact-edit plans with
placeholder/incorrect anchors. The harness now rejects and labels those failures
accurately; it does not yet change the core contract.

The deeper mismatch is architectural: today the plan-execute planner is expected
to produce exact executable multi-file edits, while the executor is a deterministic
edit applier. For broad TDD work, the planner should instead be allowed to
submit a typed work-order artifact: objective, required paths, acceptance
criteria, evidence, and approved execution scope. A separate implementation
actor should then own normal source editing and verification under that approved
scope. Exact-edit `submit_plan` remains appropriate for small anchored changes,
but it is the wrong mandatory artifact for complex multi-file implementation.

## Regression Targets

- `internal/agent`: readiness, recovery, placeholder detection, missing-path
  refresh, invalid-anchor refresh, thinking-option inheritance.
- `internal/workspace`: submit_plan decoding and malformed/stringified edit
  repair behavior.
- `internal/app`: TUI-visible blocker/fallback rendering and duplicate
  internal-message suppression.
- `cmd/idiom`: plan-execute/route behavior for required edit paths and complex
  TDD dispatch.
