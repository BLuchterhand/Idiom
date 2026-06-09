# Subagent Lane Contracts — Reviewed Intent

**Status:** intent document, not implementation spec. Captures the reviewed
consensus for how idiom should project role-bound subagents (read-only
discovery, bounded write executor, reviewer) through existing lane contracts
rather than introducing a bespoke subagent lane.

Audience: future implementation review, contract design, and authority-boundary
verification.

---

## 1. Problem

The `task` tool and `delegate_edit` already spawn child runners. The
`plan-execute` orchestrator already has a review-repair gate. But these pieces
are not yet bound by a consistent authority model:

- The read-only child task returns an evidence packet, but the parent has no
  typed record of which contract the child ran under.
- The workbranch child task returns a diff, but the parent has no typed
  acceptance record — the parent synthesizes acceptance evidence from required
  edit paths rather than receiving a structured authorization.
- The review-repair gate in plan-execute produces findings and repair loops,
  but the reviewer phase has no formal boundary between "findings" (evidence)
  and "release authority" (orchestrator-owned).
- The planner tool profile forbids writes but allows `submit_plan`, which the
  harness must then enforce outside the model — yet the child-runner contracts
  do not inherit or narrow the parent contract explicitly.

Without a consistent authority model, each new subagent role requires bespoke
tool profiles, permission policies, and result handling — the opposite of a
maintainable contract system.

---

## 2. Design Decision

**Do not add a bespoke plan/implement/review lane.** Subagents should be
role-bound workers projected through existing feature-planner/plan-execute
contracts. The parent harness owns dispatch, scope, artifact acceptance,
verification, release, and final readiness. Subagent output is evidence or a
candidate patch, never completion authority by itself.

The existing contract types (`AutonomyContract`, `RuntimeBrief`, `RoleSpec`,
`ToolPermissionPolicy`) can represent every subagent role. The missing piece is
a **parent-child contract record** that explicitly ties the child's authority
to a narrowed or inherited parent contract, and a typed parent acceptance
record for child results.

### 2.1 Authority Owners

| Concern | Owner | Mechanism |
|---|---|---|
| Scope | Parent harness compilation | Contract fields: `TargetRole`, `RequiredEditPaths`, `ForbiddenPaths` |
| Accepted plan | `submit_plan` + plan artifact | Planner writes; harness validates and stores |
| Patch acceptance | Parent harness after child returns diff | Typed acceptance record (follow-up); not synthesized from required IDs |
| Verification evidence | Independent verification runner or gate | `VerificationObligation` resolve/reject |
| Release / readiness | Parent harness | Completion gate + readiness contract |

No child or model actor holds more than one of these authorities. The parent
harness is the canonical contract owner, but it must keep the decisions
typed and separate: dispatch/scope, plan acceptance, patch acceptance,
verification evidence, and release/readiness are distinct records or gates.
The child model cannot release its own work. The parent model cannot
self-approve its own verification.

### 2.2 Role-to-Contract Projection

Each subagent role maps to an existing contract shape:

| Role | Contract | Allowed Tools | Output Authority |
|---|---|---|---|
| Read-only discoverer | `child_read_only` (existing) | Discovery tools, read/search, shell verification prefixes | Evidence packet only |
| Bounded write executor | `child_write` / workbranch (existing) | Edit tools + verification, git within worktree | Diff + candidate patch |
| Reviewer | plan-execute review-repair gate (existing) | None (or read-only file inspection) | Findings/review-repair evidence |
| Planner (subagent) | `planner-only` (existing via `submit_plan` tool profile) | Read/discovery + `submit_plan` | Plan artifact |

The reviewer role is special: it has no write authority at all. It produces
findings evidence, not approval or release. The orchestrator decides whether
findings require repair, block release, or are advisory.

---

## 3. Invariants

### 3.1 No Duplicate Authority

- **I-1 (dispatch authority):** Only the parent harness (via feature
  orchestrator or lane dispatch gate) may decide that a subagent should run.
  Subagent output does not self-authorize a subsequent phase.
- **I-2 (acceptance authority):** Only the parent harness may accept or reject
  a child's result. The child's own final answer is never treated as
  acceptance.
- **I-3 (release authority):** Only the parent harness completion gate may
  declare readiness. No subagent role may release its own output.

### 3.2 Evidence vs. Authority

- **I-4 (subagent output is evidence):** A child runner returns `ToolResult`
  with `Tool: "task"` (read-only) or `Tool: "task"` with a diff payload
  (workbranch). The parent model receives this as one turn's tool result — it
  is material for the parent's final answer, not a substitute for it.
- **I-5 (reviewer findings are not release):** The reviewer phase produces
  findings and repair evidence. The orchestrator (not the reviewer model)
  decides whether the findings are resolved, waived, or block release.
- **I-6 (verification evidence is not acceptance):** A passing verification
  command resolves a `VerificationObligation`, but the harness still evaluates
  the obligation closure against the contract, not the command's exit code
  alone.

### 3.3 Boundary Ownership

- **I-7 (parent owns scope):** The parent compilation produces the child's
  `RuntimeBrief` — including required edit paths, forbidden paths, allowed
  tools, and evidence obligations. The child does not expand its own scope.
- **I-8 (child owns edits, not release):** The workbranch child produces a
  candidate diff. The parent must inspect and explicitly apply or reject it.
  The parent workspace is never mutated by the child without explicit parent
  action.
- **I-9 (no synthesis from required IDs):** The harness must not synthesize
  acceptance evidence from required edit path IDs. A path being in the required
  set does not imply the edit was valid, tested, or reviewed.

---

## 4. Happy Paths and Bad Paths

### 4.1 Read-only Child (Happy)

1. Parent model calls `task(mode="read_only", prompt="...")`.
2. Harness compiles narrowed `child_read_only` contract from parent contract.
3. Child runner executes with discovery-only tool profile.
4. Child returns evidence packet + answer text.
5. Parent model receives result as one tool output turn.
6. Parent model continues with evidence, possibly calling narrow read/search
   tools for one remaining gap before final answer.

### 4.2 Read-only Child (Bad)

- **Empty or gated child answer:** Child returns `<<QUESTION>>:` or empty or
  blocked answer. Parent harness synthesizes fallback from child tool results
  and source-body fallback reads (current behavior, preserved). If synthesis
  also fails, parent sees `Error: child task failed` and must choose a
  different strategy or report inability.
- **Child tries to write:** Blocked by `readOnlyChildTaskPermissionPolicy()`
  (default-deny for all write tools).
- **Parent attempts write from child evidence without required scope:** Blocked
  by path-role enforcement (path not in `RequiredEditPaths`).

### 4.3 Workbranch Write Child (Happy)

1. Parent model calls `task(mode="workbranch", prompt="...")`.
2. Harness creates git worktree, compiles `child_write` contract.
3. Child runs with structured write tools plus a default-deny shell policy
   that only allows narrow read-only and verification commands.
4. Child produces diff in worktree.
5. Worktree is removed after child exits.
6. Parent receives diff summary + patch. Parent must explicitly apply or
   reject; main workspace is untouched.

### 4.4 Workbranch Write Child (Bad)

- **Child pushes or deploys:** Blocked by `workbranchDeniedShellPrefixes()`
  and by the default-deny shell policy (deny `git push`, `npm publish`,
  `deploy`, etc.).
- **Child exits non-zero:** Harness records error, returns child failure to
  parent with partial diff if available.
- **Worktree cleanup fails:** Harness emits `child_workbranch_cleanup_error`
  event but does not block parent continuation — orphan worktree is a
  diagnostic signal.
- **Harness synthesizes acceptance from required paths:** Forbidden by I-9.

### 4.5 Reviewer (Happy — via plan-execute review-repair)

1. Planner phase produces plan artifact.
2. Executor phase dispatches edits.
3. Post-write verification runs.
4. Review-repair gate presents findings to a reviewer model (or deterministic
   audit).
5. Findings are evidence: pass/repair/block.
6. Orchestrator decides: if repair needed, smaller repair atoms dispatch; if
   block, feature state transitions to blocked.
7. Release only after orchestrator's completion gate passes.

### 4.6 Reviewer (Bad)

- **Reviewer claims release:** Reviewer model outputs "approved" or
  "completed." Harness ignores — release authority belongs to the orchestrator
  completion gate, not the reviewer model's text.
- **Reviewer findings ignored by self-repairing model:** The model may try to
  self-repair without a separate reviewer phase. The plan-execute orchestrator
  enforces a separate review-repair gate; direct model edits after the gate
  fire are blocked by the normal write guard.

---

## 5. Non-Goals

- **No bespoke subagent lane.** Idiom will not add `idiom lane subagent-review`
  or similar mode-specific wrapper. Existing `plan-execute` and `implement`
  lanes, plus the `task` tool and review-repair gate, cover every subagent
  role.
- **No child self-release.** Child output is never completion authority. The
  parent harness always owns final readiness.
- **No parent-acceptance synthesis from required IDs.** The harness must not
  fabricate an "accepted" record because all required edit paths are ticked.
- **No generic multi-agent scheduler.** This document addresses role-bound
  subagent projection, not a general agent coordination framework.
- **No new permission profile schema.** Existing `ToolPermissionPolicy` with
  `Default: Allow|Deny` plus command-prefix rules covers all known subagent
  roles. The workbranch policy should be hardened (see 6.2), not redesigned.
- **No change to planner/executor contract shape.** Planner owns
  `submit_plan`/plan artifact; executor owns bounded edits. Review findings are
  evidence, not a new artifact type.

---

## 6. Implemented Contract Fixes

These are concrete changes identified during review and implemented with this
intent anchor.

### 6.1 Stop Synthesizing Acceptance Evidence from Required IDs

The harness must not treat a required acceptance evidence ID as observed
acceptance evidence. Acceptance is a parent-harness decision recorded as an
explicit evidence value, not derived from required obligations or path-level
progress.

Implemented fix:
- `finalizePlanExecuteRunResult` carries explicit `AcceptanceEvidenceIDs`
  from input or progress state.
- It no longer maps `RequiredAcceptanceEvidenceIDs` into
  `AcceptanceEvidenceIDs` because changed paths or source-satisfied evidence
  exist.
- `TestFinalizeTerminalCompletionDoesNotSynthesizeAcceptanceIDs` locks in the
  negative regression.

A typed `child_acceptance` record remains a follow-up for child result
acceptance. This fix closes the narrower plan-execute terminal evidence leak.

### 6.2 Harden Workbranch Child Shell Policy to Default-Deny

`workbranchChildTaskPermissionPolicy()` now uses `Default: PermissionDeny`
with explicit allow rules for structured child-write tools and safe shell
verification/read commands. A new shell command is denied until explicitly
allowed. Chained shell escapes such as `&&`, `;`, pipes, redirects, command
substitution, and `git -C` parent-workspace access are denied even when the
command begins with an otherwise safe prefix.

The child is running in an isolated worktree with the same OS permissions as
the parent. Default-deny with a narrow allowlist of verification and read
commands is safer.

Current allowlist candidates for the hardened policy:
- `go test`, `go list` (verification and package inspection)
- `git status`, `git diff`, `git show`, `git log`, `git grep` (read-only git)
- `rg`, `grep`, `cat`, `head`, `tail`, `wc`, `ls`, `pwd` (read-only shell)
- `edit_file`, `write_file`, `append_file`, `mkdir`, `apply_patch`,
  `go_move_declarations` (write tools — already managed by tool schema, not
  shell policy)
- The child's own editor/write tool chain (already in `allowedTools`)

Note: this fix depends on the workbranch child's write tools being expressed
as typed workspace tools rather than shell commands. Workspace tool dispatch
already enforces path roles; shell commands do not. Default-deny ensures that
arbitrary shell commands (e.g. `curl`, `rm -rf`, `git push`) are blocked by
default and must be explicitly allowlisted.

---

## 7. Known Follow-Ups (Not Implemented Here)

These were identified as real gaps but are out of scope for this intent slice.
They are listed so future work can pick them up without re-discovery.

- **Typed parent acceptance record for child results.** A distinct schema
  (`child_acceptance` or `parent_accepts_result`) that the harness writes when
  it determines a child's output is accepted, rejected, or requires repair.
  This replaces the current implicit acceptance-from-path-progress pattern.
- **Typed parent-child invocation contract record.** The child runner currently
  receives a `RuntimeBrief` and permission policy derived from the parent, but
  there is no durable record binding parent contract ID, snapshot, child role,
  backend, allowed tools, scope, and evidence obligations for audit.
- **Typed plan-execute acceptance obligation input.** Current plan-execute
  compatibility plumbing reads acceptance obligations from typed JSON embedded
  in the prompt. That is acceptable as transitional prompt-as-transport, but
  the long-term owner should be typed run options or durable contract state,
  not prompt markers.
- **Explicit/validated backend kind for MLX/Qwen-like profiles.** The
  `child_read_only` and `child_write` roles currently use a generic backend
  client (`r.Client`). If child dispatch should route to a different queue pool
  or backend family (e.g. smaller model for review, larger model for write),
  the contract should carry a validated `backend_kind` field.
- **Full headless feature transition API** (from
  `docs/design-feature-planner-orchestration.md` Phase 3). The feature
  orchestrator dispatches atoms through `idiom feature run-next`. When that
  ships, the subagent role projection should consume the same dispatch gate,
  not a separate tool-call path.
- **Backend/transport telemetry matrix for child-result authority.** A
  structured event that records which contract the child ran under, which
  backend served it, what transport was used, what the result type was, and
  whether the parent issued an acceptance record. This is needed before
  audit can prove the authority chain end to end.

---

## 8. Relationship to Existing Architecture

| Concept | Existing Location | Relationship |
|---|---|---|
| `task` tool | `internal/runtime/agent/runner_child_tasks.go` | Entry point for read-only and workbranch child dispatch |
| `delegate_edit` tool | `internal/runtime/agent/runner_delegate_edit.go` | Separate path for single-edit delegation to executor model |
| `plan-execute` review-repair gate | `internal/workflows/planexecute/` | Reviewer role projection (findings evidence, not release) |
| `AutonomyContract` | `internal/contracts/autonomy/` | Target for typed parent-child contract inheritance |
| `RuntimeBrief` | `internal/runtime/workerruntime/` | Per-role contract shape |
| `ToolPermissionPolicy` | `internal/runtime/agent/runner_autonomy.go` | Default-deny per-role permission policy |
| Feature orchestrator | `docs/design-feature-planner-orchestration.md` | Future dispatch gate for subagent atoms |
| Evidence packet builder | `internal/runtime/agent/read_only_evidence_packet.go` | Read-only child evidence packet synthesis |

The subagent roles documented here are not new components. They are existing
components projected through consistent contract and authority boundaries. The
changes called out in §6 are limited to closing known authority gaps and
hardening permission defaults.

---

## 9. Verification

No code tests required for this doc. Run:

```bash
git diff --check docs/subagent-lane-contracts-reviewed-intent.md
```

This doc is the reviewed intent anchor. Future implementation work should
derive concrete acceptance tests from the invariants in §3 and the known fixes
in §6.
