# Live TUI Document Viewer Workflow: Approved Fix Proposals

Date: 2026-05-31

## Scope

This report consolidates issues observed while exercising the live TUI
`/feature-planner` workflow with the request for a proper document viewer panel.
It covers the product workflow, TUI operator experience, plan-execute handoff,
generated artifacts, and implementation acceptance. It does not implement the
fixes.

Primary evidence:

- Conversation log:
  `logs/conversations/20260531-090911.072.jsonl`
- Product workflow artifacts:
  `docs/product-workflows/efforts/started/as-a-product-manager-i-want-the-live-tui-to-have-a-proper/`
- Generated files:
  `tui/layout/document_panel.go`
  `tui/viewer/state_machine.go`
  `tui/viewer/metadata.go`
  `tui/viewer/error_handler.go`
- Narrow verification:
  `go test ./tui/...` failed because `tui/layout/document_panel.go` imports
  `github.com/charmbracelet/idiom/tui/viewer` while `go.mod` declares module
  `idiom`.

Reviewers consulted:

- Incident Root-Cause Reviewer
- Harness / Go Reviewer
- Prompt / Protocol Reviewer
- TDD / Acceptance Reviewer
- North-Star / Architecture Reviewer

Consensus:

- The fix should not be document-viewer-specific.
- The fix should not be another prompt reminder.
- The durable repair is typed: ground implementation scope before dispatch,
  distinguish new-file body authority from existing-file source-body evidence,
  keep parent workflow status tied to child session truth, and block acceptance
  when tests/traceability are not executable.

## Approved Proposals

### P0. Add A Repo-Grounding Gate Before Feature-Plan Acceptance

Problem:

The planner accepted and dispatched atoms with invented top-level `tui/...`
paths even though the existing live TUI architecture is under `internal/surfaces/tui/app`.
The product workflow prompt contained accepted PRD/ADR/TDD summaries but no
concrete existing TUI source paths or context packet.

Approved change:

Before accepting `submit_feature_plan`, require every atom path to be grounded
by one of these typed authorities:

- present in a repo localization/context packet
- present in explicit `FilesToModify` or equivalent human-approved scope
- under an existing package root with explicit new-file intent
- authorized by an accepted ADR/operator decision that explicitly creates a new
  package boundary

If an atom cannot meet that bar, the planner must return
`ask_planner_question` or a typed planning blocker instead of dispatching.

Reviewer approval:

- Incident Root-Cause: approved as the first broken invariant.
- North-Star / Architecture: approved as the model-strategy/harness-contract
  boundary.
- Prompt / Protocol: approved; planner must not invent paths from schema
  examples.

Regression tests:

- Reject a product workflow plan containing ungrounded `tui/...` paths when no
  context packet or explicit new package boundary exists.
- Verify product workflow planning includes localization evidence or asks a
  scope question.
- Verify path validation rejects atom required paths outside known package roots
  unless explicitly authorized.

### P0. Stop Parent Product Workflow Completion On Failed Or Running Child Atoms

Problem:

The parent flow progressed while child plan-execute sessions were failed,
blocked, or still running. Reviewers found failed renderer atoms, a failed
review-repair session, and a final metadata/error-handler session left
`phase: executor`, `status: running` despite edits being marked completed.

Approved change:

Gate `CompleteEffort` and feature-planner phase clearing on structured child
session statuses, not on the absence of a Go error or string markers in a model
answer. All dispatched atoms must be terminal `complete`; otherwise the TUI
must remain in `recovery_open` and surface exact failed/running session IDs,
required paths, and next valid actions.

Reviewer approval:

- Harness / Go: approved as an orchestration status fix.
- Incident Root-Cause: approved as necessary after grounding.
- North-Star / Architecture: approved; parent status must not smooth over child
  contract failures.

Regression tests:

- Product approval does not call `CompleteEffort` or clear product phase when
  any child plan-execute session is failed, blocked, or running.
- A plan-execute session with all edits completed must end in a terminal
  completed/failed state, never `executor/running`.
- TUI shows failed child session IDs and recovery actions instead of a generic
  completion state.

### P0. Separate Planner Coverage From Workspace Mutation Obligations

Problem:

Telemetry showed `plan_submitted_short_circuit` with `changed_path_count=0`,
`verification_status=pending`, and required path statuses still `pending`, while
write obligations were resolved. That conflates “a valid plan artifact was
submitted” with “workspace mutations happened and verified.”

Approved change:

Introduce separate typed obligations:

- `plan_entry_coverage`: the planner included a valid edit entry for each
  required path.
- `new_file_body_authority`: a new file has enough body/package/test authority
  to be created.
- `workspace_mutation`: executor successfully applied edits to the workspace.
- `verification`: required commands/tests ran or are explicitly blocked.

Planner short-circuit may resolve `plan_entry_coverage`, but not
`workspace_mutation` or implementation completion.

Reviewer approval:

- Harness / Go: approved.
- North-Star / Architecture: approved.
- TDD / Acceptance: approved because acceptance cannot treat pending mutation as
  implemented behavior.

Regression tests:

- Planner short-circuit telemetry must not report write obligations resolved
  while required path status is pending.
- Completion gate rejects implementation success when only plan coverage exists.
- Audit output distinguishes plan artifact coverage from changed paths.

### P0. Classify Required Paths By State Before Source-Body Recovery

Problem:

Missing required new files were routed through a read/source-body progression.
The tool result said “this is a NEW FILE,” but next-action state still advanced
through reading the next missing required path. Larger all-new atoms exhausted
recovery instead of moving directly to submit-ready or blocker.

Approved change:

Add typed path states before recovery and next-action construction:

- `existing_source_body_required`
- `new_file_body_required`
- `source_satisfied`
- `stale_anchor`
- `blocked`

For `new_file_body_required`, do not keep scheduling source-body reads after the
first missing-file proof. The valid outcomes are a whole-file `submit_plan`
with body authority or `<<QUESTION>>` naming missing body/package evidence.

Reviewer approval:

- Prompt / Protocol: approved.
- Harness / Go: approved.
- North-Star / Architecture: approved as typed contract repair.

Regression tests:

- Five missing required new-file paths transition to submit-ready/blocker
  without exhausting reads.
- Existing required paths still require source-body evidence before
  `submit_plan`.
- Valid new-file atoms with explicit body authority still dispatch.

### P0. Align Visible Tool Surface With Gate Order

Problem:

The model protocol and recovery cards exposed mixed signals. In one phase,
`submit_plan` was visible while also marked invalid. Review-repair recovery
required one terminal outcome but the surrounding surface still allowed
read/navigation attempts.

Approved change:

Derive visible tools directly from the typed next valid action:

- If `submit_plan` is invalid, do not advertise it as visible/callable.
- If submit-ready, hide read/search/navigation tools and expose only the
  terminal submit/blocker path.
- In terminal review-repair recovery, do not advertise broad read/search/code
  navigation tools unless the typed contract explicitly allows a bounded
  evidence read.

Reviewer approval:

- Prompt / Protocol: approved.
- Harness / Go: approved.

Regression tests:

- Model protocol projection never includes a tool in `visible_tools` when that
  same action is invalid.
- After review-repair terminal recovery, read/search/code-navigation tools are
  not advertised as callable.

### P1. Require Product Artifacts To Ground Platform And Technology Claims

Problem:

After terminal-native refinement, product artifacts still contained unsupported
or web-specific assumptions such as `axe-core`, ARIA-like annotations, CSS-like
or browser concepts, `FocusTrap`, `LayoutStore`, and broad PDF/DOCX renderer
commitments. These became implementation pressure without source evidence.

Approved change:

Add product-artifact linting/validation for implementation-affecting platform
claims. The gate should not ban specific terms by scenario. Instead it should
require one of:

- source evidence that the target platform supports the claim
- an accepted ADR marking the claim as a deliberate new dependency/boundary
- an open question/advisory classification that prevents dispatch authority

Reviewer approval:

- Prompt / Protocol: approved.
- TDD / Acceptance: approved.
- North-Star / Architecture: approved because the fix is generic and
  evidence-shaped.

Regression tests:

- PRD/ADR refinement rejects or moves unsupported web/browser/accessibility
  assumptions to open questions for terminal TUI work.
- Inference-only implementation claims cannot become required dispatch
  authority.

### P1. Make TDD Ledger IDs Traceable And Executable Before Dispatch

Problem:

The PRD referenced `TDD-REQ-001`, ADRs referenced `TDD-ADR-001`, and the ledger
defined `TDD-001`. The ledger had zero mapped coverage and all obligations were
`planned`, even though each obligation said it was required before
implementation.

Approved change:

Add TDD traceability and readiness gates:

- PRD, ADR, and `tdd.json` obligation IDs must match or map through an explicit
  ledger mapping.
- Required-before-implementation obligations must have executable test commands
  and test artifact paths before implementation dispatch.
- Zero mapped coverage blocks implementation unless explicitly waived with a
  typed blocker.

Reviewer approval:

- TDD / Acceptance: approved.
- Incident Root-Cause: approved.
- North-Star / Architecture: approved as harness-owned acceptance contract.

Regression tests:

- Reject dispatch when TDD obligations remain only `planned` with no executable
  tests.
- Reject mismatched PRD/ADR/TDD IDs unless explicit mapping exists.
- Require behavior-level test obligations instead of generic “Verify ADR
  behavior” placeholders.

### P1. Add Compile/Source-Sanity Verification To Generated Implementation Acceptance

Problem:

The generated code did not compile. `go test ./tui/...` failed immediately due
to a wrong import path, and plan-execute session evidence also identified
missing Bubble Tea `Init` and invalid viewport field access.

Approved change:

Add a narrow compile/source-sanity gate for generated source before the workflow
can present implementation success. For TUI/package-generating atoms, run or
record the relevant package compile command, then require either a pass or a
typed verification blocker.

Reviewer approval:

- TDD / Acceptance: approved.
- Harness / Go: approved.

Regression tests:

- `go list ./...` or targeted package compile failure blocks implementation
  success.
- Generated package import paths must match `go.mod`.
- Source sanity failures must surface in the parent TUI status/recovery output.

### P1. Stop Dispatching Sibling Atoms After A Blocker

Problem:

The flow continued after earlier atom failures, leaving a partial workspace:
two files from the first atom, failed renderer atoms, and two files from the
metadata/error atom. That makes the user-facing result harder to understand and
the workspace harder to recover.

Approved change:

On the first plan-execute blocker/question/review-repair failure, stop sibling
atom dispatch and surface the blocker. Allow continuation only after explicit
planner/operator resolution.

Reviewer approval:

- Incident Root-Cause: approved.
- Harness / Go: approved.

Regression tests:

- A blocked atom prevents subsequent sibling atom dispatch.
- The product TUI remains in `recovery_open` and lists the blocked atom and
  next valid actions.

### P1. Replace String-Marker Outcome Detection With Structured Child Results

Problem:

`runFeaturePlannerAtomPlanExecute` classifies outcomes using answer text markers
such as `question_for_planner` rather than the structured plan-execute session
state. Reviewers found this made parent status too weak.

Approved change:

Have feature-planner implementor dispatch read and propagate structured
plan-execute progress/result objects: phase, status, changed paths, source
sanity status, verification status, blocker kind, and recovery packet path.

Reviewer approval:

- Harness / Go: approved.

Regression tests:

- Textual answer markers alone cannot mark an atom complete.
- Structured `failed`, `blocked`, or `running` child states propagate to parent
  workflow status.

### P1. Improve TUI Long-Running Status And Final-State Rendering

Problem:

The live TUI produced large volumes of spinner redraw frames and often showed
generic or stale status text. It stayed on
`Executing: Edit 2/2 error_handler.go (succeeded)` even after logs indicated the
turn had completed, and plain `q` did not exit while execution status remained
active.

Approved change:

Make long-running TUI status stateful and terminal:

- show current phase, child session ID, elapsed time, active artifact path, and
  last durable event
- use phase-specific status labels such as `Revising PRD`, `Updating ADRs`,
  `Planning atoms`, `Executing atom 2/4`, `Review failed`
- stop spinner animation once the backing workflow/session reaches a terminal
  state
- keep a bounded redraw strategy for PTY/automation friendliness
- expose a clear interrupt/cancel hint while work is active

Reviewer approval:

- Harness / Go: approved in status lifecycle terms.
- Incident Root-Cause: approved as operator-facing recovery evidence.

Regression tests:

- TUI status changes to terminal complete/failed/recovery state when child
  session is terminal.
- Spinner does not continue after terminal state.
- Status rendering includes exact artifact/session path for active work.

### P2. Make Artifact Paths Explicit In TUI Messages

Problem:

The TUI said `Saved: prd.md`, but the real path was under the product workflow
effort folder. Operators had to search for the artifact.

Approved change:

Display repo-relative artifact paths in all product workflow status messages:

- `Saved: docs/product-workflows/efforts/.../prd.md`
- `Saved: docs/product-workflows/efforts/.../adrs/index.json`
- `State: docs/product-workflows/efforts/.../workflow_state.json`

Reviewer approval:

- Incident Root-Cause: approved as evidence clarity.

Regression tests:

- Product workflow formatter includes repo-relative paths for generated
  artifacts.

### P2. Improve `/help` And Command Discoverability

Problem:

In the live PTY, `/help` cleared the input but did not render visible help in
the captured pane. `/feature-planner` was usable only by prior knowledge.

Approved change:

Make `/help` durable and visible in the transcript/pane, and include
`/feature-planner`, `/product-approve`, active product workflow phase, and next
valid product command. Do not replay operator help into model context.

Reviewer approval:

- Incident Root-Cause: approved as UX/operator issue.

Regression tests:

- `/help` renders visible command help and does not enter model context.
- During product workflow phases, help includes the current phase and next
  valid action.

### P2. Handle Retryable Model Transport Failures As Typed Runtime Events

Problem:

The first PRD refinement failed with a model transport EOF and the UI surfaced a
generic chat failure. The retry succeeded, so this was not the implementation
root cause, but the operator experience was weak.

Approved change:

Represent retryable runtime transport failures as typed events:

- preserve the previous artifact unchanged
- show retryable/non-retryable classification
- show endpoint/backend/session ID
- offer the next safe action, usually retry or cancel

Reviewer approval:

- Incident Root-Cause: marked as a non-root-cause but valid UX/runtime
  improvement.

Regression tests:

- PRD/ADR refinement EOF keeps existing artifact unchanged and returns a typed
  retryable error state.

## Explicit Non-Proposals

These were rejected by reviewers:

- Do not add a document-viewer-specific prompt recipe.
- Do not weaken required-path coverage or completion gates.
- Do not manually patch the generated document viewer as the fix for this live
  workflow failure.
- Do not blame local model quality before typed contract, path grounding,
  runtime state, and tool-surface alignment are checked.
- Do not encode banned terms like `axe-core` as one-off string rules; use
  generic evidence/authority classification.

## Suggested Implementation Order

1. Add repo-grounding gate and path-state classification.
2. Fix child-session status propagation and parent completion gating.
3. Separate plan coverage, mutation, and verification obligations.
4. Align visible tool surfaces with typed next valid actions.
5. Add TDD traceability/readiness gates.
6. Add source-sanity/compile acceptance and parent recovery surfacing.
7. Improve long-running TUI status, artifact paths, help, and retryable runtime
   error display.
