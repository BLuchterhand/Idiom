# Deterministic Control Rails Implementation Tracker

This tracker captures the explicit deterministic scaffold required for frontier-grade agent behavior. The goal is to make weak routing, unsupported claims, early stopping, unsafe writes, and unverifiable final answers difficult by construction rather than relying on the model to reason through those constraints on every turn.

## Control-Rail Principles

- Use model judgment inside deterministic host policies, not instead of them.
- Classify every user turn into an auditable lane and task family before generation.
- Require lane-specific evidence/tool preconditions before finalization.
- Treat low confidence as a control-flow state that triggers deepening, fallback, or explicit uncertainty.
- Separate read-only analysis from guarded implementation.
- Persist failures into eval-ready regression contracts.

## Phase 48: First-Class Control-Rails Contract

Status: complete

Runtime implementation:

- Added `control_rails` debug-log records for every prompt plan.
- Captured selected lane, follow-up lane, answer contract, task family, permission profile, required evidence, required conceptual tools, forbidden tool classes, readiness gates, search policy, verification contract, stop conditions, final-critic checks, and failure-to-eval capture policy.
- Added deterministic task-family classification for:
  - `direct_answer`
  - `locate`
  - `investigation`
- `debug_logs`
- `verification_only`
  - `review`
  - `security_review`
  - `architecture`
  - `implementation`
- Routed log/failure-inspection prompts such as “check the latest logs” and “why did this fail” into read-only exploration instead of direct conversation.

Eval implementation:

- Added `require_control_rails` scenario assertions.
- Added `control_rails` parsing to captured-log metrics.
- Added suggested-scenario generation from observed `control_rails` records.
- Added human-readable `qwen-eval` output for the latest control-rails snapshot.
- Added SWE-suite scenario assertion coverage for `control_rails`.
- Added live control-rail enforcement for forbidden tool classes and final-answer readiness gaps.
- Added target-reference rails so classifier-provided `target_refs` must be present in architecture evidence or final answers.
- Added `control_rails_enforcement` debug-log records for accepted or repaired final answers.

Test coverage:

- Route regression for latest-log/failure inspection.
- Runtime rail construction tests for debug-log and implementation task families.
- Runtime rail construction tests for verification-only shell checks and failure-worded implementation prompts.
- Final-answer enforcement tests for unresolved implementation verification contracts.
- Final-answer enforcement tests for architecture target drift.
- Eval parsing, matching, mismatch reporting, and suggested-scenario coverage.
- Eval scenario coverage for missing assistant turns across multi-turn conversations.
- CLI JSON and scenario-generation coverage for `control_rails`.

## Required Runtime Rails

### Deterministic Task Router

Status: complete for current lane model

Implemented as:

- Host-side lane classification through `classifyPromptExecutionPlan`.
- Follow-up route classification for analysis-to-implementation prompts.
- Route ambiguity telemetry through `route_ambiguity`.
- Control-rail task-family classification layered above lanes.

Frontier parity target:

- Add dedicated lanes later only when they need different execution machinery. For now, `debug_logs` is a task family under the read-only `explore` lane because it shares the same inspection-only execution contract.

### Readiness Gate

Status: complete and extended

Implemented as:

- Existing `readiness_check` records.
- Existing analysis final-answer critic.
- Existing implementation final-answer critic.
- New `control_rails.readiness_gate` checklist that defines what each task family must satisfy.

Required behaviors:

- Analysis lanes require evidence before strong claims.
- Implementation requires target/changelog plus verification resolution.
- Debug-log tasks require latest-log selection and log-supported failure cause.
- Low-confidence analysis triggers deterministic deepening when budget remains.

### Evidence Ledger

Status: complete and linked

Implemented as:

- Existing deterministic evidence ledger and `evidence_delta`.
- Existing `claim_confidence`.
- New `control_rails.required_evidence` field that states what evidence must exist for the selected task family.

Required evidence examples:

- `debug_logs`: `log_record`, `failure_output`, `timestamp_or_latest_selection`.
- `review`: `candidate_path`, `bounded_finding_evidence`, `confidence`.
- `architecture`: `hotspot_path`, `structural_shape`, `boundary_rationale`, `confidence`.
- `implementation`: `changed_files_or_noop_reason`, `verification_result`, `risk_contract`.
- `verification_only`: `verification_command`, `pass_fail_status`, `failure_output_if_any`.

### Tool-Use Preconditions

Status: complete at policy and enforcement level

Implemented as:

- `control_rails.required_tools` for conceptual preconditions.
- `controlRailPreExecute` enforcement for forbidden concrete tools and mutating shell commands.
- Existing actual tool-request metrics and scenario `require_tools`.
- Existing broad-tool and repeated-dead-end budgets.

Important distinction:

- `required_tools` inside `control_rails` describes the deterministic precondition class, such as `log_inspection` or `workspace_inspection`.
- Scenario `require_tools` describes concrete tools observed in captured logs, such as `read_file` or `search_content`.

### Budget And Search Expansion

Status: complete and extended

Implemented as:

- Existing `effort_policy`.
- Existing `budget_ledger`.
- Existing `deepening_attempt`.
- Existing `evidence_delta`.
- New `control_rails.search_policy`.

Search policies now explicitly encode:

- Start with the most recent log for failure/log tasks.
- Rank candidates and deepen if no bounded finding exists for review tasks.
- Map entrypoints and rank hotspots for architecture tasks.
- Consume implementation target packets before broad discovery.

### Failure-Driven Eval Generation

Status: complete at capture-policy level

Implemented as:

- Existing suggested-scenario generation from captured logs.
- New `control_rails.failure_to_eval` flag for prompts that should become durable regression candidates, especially failure/log investigations.

Remaining future expansion:

- Added `qwen-swe-eval -write-failing-scenarios` to extract failed captured-log evaluations into a named SWE-suite task skeleton.

### Permission And Write Rails

Status: complete at lane/profile level

Implemented as:

- Existing guarded writes and protected-path approvals.
- New `control_rails.permission_profile`.
- New `control_rails.forbidden_tools`.

Permission profiles:

- `conversation_only`
- `read_only_verification_shell`
- `read_only_workspace`
- `analysis_read_only_then_guarded_writes`
- `guarded_workspace_writes`

### Verification Contract

Status: complete and extended

Implemented as:

- Existing implementation verification planner.
- Existing verification feedback memory.
- New `control_rails.verification_contract`.

Contracts:

- Read-only lanes: `not_required_for_read_only_lane`.
- Implementation or analysis-to-implementation: `tests_passed_or_failed_or_skipped_with_blocker`.

### Stop Condition Policy

Status: complete and extended

Implemented as:

- Existing readiness/deepening/budget chain.
- Existing explicit uncertainty policy in budget ledgers.
- New `control_rails.stop_conditions`.

Allowed stop conditions:

- Required evidence is satisfied.
- Missing permission or missing information blocks progress.
- Budget is exhausted and uncertainty is declared.
- Implementation is verified or verification failure is reported.
- Log cause is identified or log gap is declared.

### Critic/Verifier Pass

Status: complete and extended

Implemented as:

- Existing analysis critic.
- Existing implementation critic and repair summary.
- New `control_rails.final_critic` field that makes expected critic checks explicit per lane/task family.
- New `control_rails_enforcement` records and deterministic repair text when final answers violate required evidence, verification, or changed-file/no-op disclosure.

Examples:

- Review: findings first, bounded finding or no finding stated.
- Architecture: structural framing and hotspot paths.
- Implementation: changed-files section and verification section.
- Locate: exact target or no-match statement.

## Recently Completed Frontier Follow-Ups

- Added a `qwen-swe-eval -write-failing-scenarios` mode that converts failed captured-log reports into task skeletons.
- Added a control-rails dashboard summary in `qwen-swe-eval` grouped by task family and permission profile.
- Add stricter lint rules requiring `require_control_rails` for all new representative tasks.
- Add dedicated execution machinery for `debug_logs` if logs become a first-class workspace artifact rather than a task family under exploration.
