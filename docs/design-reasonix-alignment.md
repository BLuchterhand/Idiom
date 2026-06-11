# Reasonix-Informed Idiom Hardening

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: implemented and re-reviewed

Evidence baseline:

- Reasonix `main-v2` was inspected at commit `d014949`.
- Idiom comparison used the local docs and source, especially
  `docs/decisioning-north-star.md`, `docs/idiom-design-roadmap.md`,
  `docs/design-contract-first-local-autonomy.md`,
  `docs/design-local-model-operations.md`,
  `internal/runtime/agent/runner_task_ledger.go`,
  `internal/runtime/agent/safety/policy.go`, and
  `internal/capabilities/workspace/tool_surface_metadata.go`.

## Decision

Adopt the Reasonix mechanisms that strengthen host-owned contracts, evidence,
and local-model operations, while rejecting mechanisms that would make Idiom
copy Reasonix's strategy surface or prompt recipes.

The design keeps Idiom's boundary unchanged:

- The model owns strategy.
- The harness owns contracts, evidence validation, permission decisions,
  verification readiness, audit, and replayable telemetry.

## What Reasonix Shows

Reasonix is a Go coding-agent harness with a narrower thesis than Idiom:
DeepSeek-oriented terminal and desktop operation, cache-stable sessions, MCP
plugins, permissions, checkpoints, memory docs, and a small evidence ledger. It
does not have Idiom's persistent repository memory, freshness contract, language
agnostic discovery surface, plan artifact audit, or sidecar/audit flywheel.

The useful Reasonix patterns are lower-level runtime contracts:

1. `complete_step` validates step completion against host-observed receipts.
2. `todo_write` is kept as host-rendered task state, while completion is signed
   by evidence rather than prose.
3. Edit-tool checkpoints snapshot pre-edit file contents per user turn and can
   restore code, conversation, or both without touching git.
4. Prefix/cache diagnostics hash the stable prompt and tool-schema prefix so
   cache misses can be attributed.
5. Permissions are a pure policy decision, separate from sandbox enforcement.
6. Compaction is treated as a rare cache-reset point with dropped-message
   archive and preserved task state.

## Adopted Changes

### 1. Evidence-Backed Task Ledger Completion

Current Idiom `task_ledger` validates shape and blocks final answers with
unfinished items, but a completed item can carry only free-form evidence text.
That is weaker than Idiom's own north star: completion should be a host-verified
contract fact when the active lane requires it.

Add a host receipt ledger for mutation turns and teach `task_ledger` completion
updates to validate newly completed items against successful same-turn receipts.
Accepted evidence forms:

- `write:<path>` matching a successful workspace write.
- `read:<path>` matching a successful source/body read or semantic source
  inspection.
- `verify:<command>` matching a successful shell or verification receipt.
- `artifact:<id>` matching a successful plan, recovery, or verification artifact
  emitted by the harness.
Unverified `manual:<summary>` completion was intentionally not implemented in
this pass. Manual completion would weaken the host-owned receipt invariant and
needs a separate policy decision if a future lane genuinely requires it.

The model still chooses the work sequence. The harness only rejects completion
claims that contradict observable receipts.

### 2. Edit Snapshot Rewind Substrate

Current Idiom has git checkpoint audit before writes, which records `HEAD` and
dirty status. That is not enough for non-git directories, partial undo, or
conversation/code rewind.

Add a preimage snapshot store:

- Capture pre-edit file contents before first write-tool touch of each path.
- Keep the first preimage for a path stable for the duration of the runner turn.
- Restore edited files to their original contents and delete files that did not
  exist at snapshot time.
- Do not snapshot shell side effects because the harness cannot know them.
- Restore paths only after normal workspace-root and protected-path checks.
- Keep git checkpoint events as audit evidence; do not use git as the undo
  substrate.

The first implementation provides the core store and direct write capture seam
for `edit_file`, `write_file`, `append_file`, `apply_patch`, and
`go_move_declarations`. UI commands for interactive rewind and persistent
session storage remain broader follow-ups.

### 3. Prefix Cache Diagnostics

Idiom already has context-pack cache metrics, compact packet projections, and
local backend design notes. It should also expose backend prompt/prefix cache
health when available.

Add an enriched `model_call` event containing:

- stable prefix hash covering system prompt, selected model-facing prompt
  overlays, tool schema hash, tool choice, and parser contract
- tool schema hash
- model alias and backend kind
- reason fields for prefix churn: system prompt, tool schema, tool choice, or
  parser contract
- explicit `prefix_cache_metadata_scope=telemetry_only`
- explicit absent-provider-cache signal when the backend does not report cache
  hit/miss tokens

These fields are diagnostic only. They must not decide the model's next action.

### 4. Compaction Receipt Preservation

Idiom's compaction should preserve contract state as structured facts, not only
summary prose. Any compaction path must retain:

- active task ledger state and evidence-backed completion receipts
- changed paths and required edit path coverage
- verification obligations and latest verification outcomes
- plan artifact IDs, packet IDs, and recovery packet IDs
- open blockers and source-satisfied terminal evidence

Compaction should archive or reference dropped originals where available and
emit a compact event with dropped count, retained count, pinned paths, and
preserved contract refs.

### 5. Policy/Sandbox Contract Projection

Idiom already has richer tool side-effect metadata than Reasonix. The gap is
making the active per-phase policy easy to audit from one source.

Compile a phase policy projection from:

- runtime brief and task family
- tool manifest metadata and side-effect classes
- workspace tool rules
- approval state
- sandbox/protected-path capabilities
- required/allowed/evidence/forbidden path roles

The projection should drive tool visibility, permission snapshots, and audit
findings. It should not be reconstructed differently by prompts, TUI, runner,
sidecar, and eval.

## Rejected Reasonix Patterns

- Do not make auto-plan classification a broad keyword/complexity heuristic.
  Idiom route authority belongs in typed route plans and explicit commands.
- Do not force a serial `todo_write` plus `complete_step` workflow everywhere.
  Evidence-backed completion is a lane contract, not a universal strategy.
- Do not treat `@file` or historical chat injection as repository memory.
  Idiom's durable memory is source-linked, fresh, queryable, and auditable.
- Do not let cache stability outrank evidence quality. Cache health is a local
  operations metric, not the product thesis.
- Do not resolve non-interactive approval by silently allowing unsafe work.
  Headless autonomy still needs explicit mode, scope, and audit semantics.

## Implementation Scope

This implementation pass landed the smallest contract-level slice that can be
verified without introducing a new model strategy workflow:

1. `task_ledger` validates newly completed mutation items against prior
   successful host receipts when `TaskLedgerPolicy.RequireEvidence` is active.
   Mutation briefs enable that policy automatically.
2. Focused tests cover missing evidence rejection, unsupported evidence
   rejection, valid write/read/verify receipts, and preservation of previous
   evidence on already completed items.
3. `model_call` telemetry includes stable prefix diagnostics and explicitly
   marks provider cache data as absent when no backend signal exists.
4. Prefix tests cover hash stability, hash changes for prompt/tool changes, and
   bounded telemetry fields.
5. `PreimageCheckpointStore` captures and restores file preimages, including
   files that were absent before mutation, and the runner snapshots direct write
   targets before workspace dispatch.
6. Receipt validation remains a typed predicate over observed tool results. No
   `complete_step` clone or serial workflow was added.
7. Prefix cache fields are bounded telemetry only. They do not participate in
   context-pack cache keys, prompt construction, routing, or tool selection.

The policy/sandbox projection and interactive rewind UI are broader follow-ups.
This patch may add only the durable core or seam needed for those follow-ups.

## Review Requirements

Run the full codified reviewer bench from `docs/debugging-reviewer-bench.md`:

- Incident Root-Cause Reviewer
- Model Runtime / Config Reviewer
- Harness / Go Reviewer
- Prompt / Protocol Reviewer
- TDD / Acceptance Reviewer
- North-Star / Architecture Reviewer
- Model-Agnostic / Cross-Backend Reviewer

Each reviewer must use the required shape from the bench and end with one of:

- `SIGN-OFF: yes`
- `SIGN-OFF: no` with blocking changes

No implementation should proceed until every blocking review item has either
been addressed or explicitly moved out of this patch with a north-star reason.

## Reviewer Sign-Off

Reasonix `deepseek-flash` review artifacts are stored under
`.idiom/reasonix-reviews/`.

Initial broad reviewer runs were invalid because all seven reviewers exhausted
their tool-step budget before producing a final sign-off. A valid final-only
review pass used a bounded evidence packet. After implementation, the three
reviewers that were blocked or provisional were rerun with exact test evidence
and broad-suite failure classification.

| Reviewer | Result | Required follow-up |
| --- | --- | --- |
| Incident Root-Cause Reviewer | Sign-off yes | Track unrelated read-only answerability/fallback failures separately. |
| Model Runtime / Config Reviewer | Sign-off yes | Add bounded cache/compaction telemetry and tests for absent provider cache fields. |
| Harness / Go Reviewer | Sign-off yes | Track future write tools against the preimage snapshot handler list. |
| Prompt / Protocol Reviewer | Sign-off yes with condition | Verify `task_ledger` status transitions remain the model-facing protocol. |
| TDD / Acceptance Reviewer | Sign-off yes | Keep the new focused tests with this contract slice. |
| North-Star / Architecture Reviewer | Sign-off yes | Keep every hard gate paired with a typed recovery path. |
| Model-Agnostic / Cross-Backend Reviewer | Sign-off yes with conditions | Namespace/bound conditional telemetry and keep cache metadata out of active cache keys. |
| Drift / Duplicate Logic / Consolidation Reviewer | Sign-off yes | Preimage target extraction now delegates to workspace-owned `WritePreimagePaths`; coverage test guards every `WorkspaceWriteToolNames()` entry. |

Blocking status for this Reasonix hardening slice: all codified reviewers have
signed off, plus the additional drift/consolidation reviewer signed off after
the preimage write-tool enumeration was consolidated into the workspace package.

Verification notes:

- `go test ./internal/runtime/agent/safety` passed.
- Focused `go test ./internal/runtime/agent -run
  'TestTaskLedger|TestModelCallPrefix|TestSnapshotWritePreimages|TestModelCallTelemetry'`
  passed.
- `go test ./internal/capabilities/workspace` passed.
- Broad `go test ./internal/runtime/agent` failed on unrelated existing
  read-only answerability/fallback tests:
  `TestFinalAnswerAcceptanceRejectsEchoedObjectivePathInventoryFromLiveTUIQuestion`,
  `TestFinalAnswerAcceptanceRejectsSessionsPanelAsChatRecallOwner`, and
  `TestModelErrorAfterReadOnlyEvidenceReturnsDeterministicFallback`. The
  read-only answerability files were not changed by this Reasonix slice; those
  failures remain external blockers for the broader package, not for this
  design's implemented contracts.
