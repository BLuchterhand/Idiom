# User-Facing Governance Controls

Status: draft inventory & gap analysis
Evidence snapshot: repository inspection of `.idiom/config.json`, `.idiom/backends.json`, `internal/contracts/autonomy/`, `internal/capabilities/workspace/tool_profile_policy.go`, `internal/runtime/workerruntime/catalog.go`, and `docs/design-contract-first-local-autonomy.md` on the session date.

## Purpose

Idiom sells trust in local coding agents. Governance — what a user can configure, inspect, and override about their agent's authority — is the primary mechanism for earning that trust. This document inventories every governance lever currently available to users, then catalogs the ten most significant gaps between what the harness internally supports and what users can actually control.

The audience is implementation teams deciding which governance gaps to close next, and reviewers evaluating whether a patch expands or contracts the user's control surface.

## Current Governance Levers (What Exists Today)

### 1. Backend Model Selection (`idiom backend` / `.idiom/backends.json`)

Users declare which model(s) power their agent. They can:

- Choose local inference (llama.cpp, MLX) or hosted APIs (DeepSeek, MiniMax M2.7, GLM, OpenAI).
- Split roles across models: one model for route classification, another for planning, another for execution.
- Configure per-model: temperature, top-p, top-k, context window size, cache size, request timeout, and whether the model uses a local secure endpoint (`use_local_secure_endpoint`).

This is the most mature governance surface. The `idiom backend` CLI and TUI panels provide create, edit, list, status, and switch operations.

### 2. Permission Profiles (`.idiom/config.json` → `workspace.tool_rules`)

Users set per-tool allow/deny rules in `config.json`:

```json
"tool_rules": {
  "edit_file": "allow",
  "shell": "deny",
  "search_content": "allow"
}
```

Additional top-level toggles in `config.json` control `enable_shell`, `enable_writes`, `allow_private_network`, `max_output_size`, and `max_read_size`.

The harness enforces these through `ToolProfile` objects (`internal/capabilities/workspace/tool_profile_policy.go`). Three profiles ship by default: `default` (all tools), `discovery` (read-only tools), and `mcp_discovery` (read-only + MCP tools). The profile system correctly masks tools from the model — denied tools are not visible in the tool manifest.

### 3. Runtime Budgets (`.idiom/config.json` → `chat`)

Users configure safety fuses:

- `max_tool_steps` (0 = unlimited)
- `max_model_calls`
- `max_duration_seconds`
- `max_context_messages`
- `max_context_chars`

These are hard upper bounds enforced by the run loop. When exceeded, the run terminates with a budget-exhausted stop reason.

### 4. Lane / Phase Selection

Users choose the lane at session start: `conversation`, `implement`, `plan-execute`, `review`, `architecture`, `locate`. Each lane carries a typed contract that determines:

- Permission profile (default, discovery, etc.)
- Tool profile (which tools are visible)
- Readiness gates (what evidence must exist before writing)
- Verification contract (what must pass after writing)
- Completion gates (what constitutes a valid final answer)

This is the most consequential governance choice a user makes, but it is implicit — users select a lane name, not a contract.

### 5. Required Edit Paths (via Runtime Briefs)

In implementation lanes, users can enumerate `required_edit_paths` in their prompt or brief file. The harness enforces:

- Every write must fall within a required edit path.
- Every required edit path must be either edited or explicitly source-satisfied (proven already correct).
- The final answer must report changed paths.

This is a write-scope constraint only; it does not block reads, discovery, or verification outside the required paths.

### 6. Route Classifier

Users enable/disable the route classifier and configure its model, port, and timeout. The classifier determines which lane or phase handles the user's input. This is an internal optimization but has governance implications: disabling the classifier forces the user's lane choice directly.

### 7. MCP Tool Integration

Users configure MCP servers (e.g., Exa web search, filesystem) in `.idiom/config.json` under the `mcp` array. Each entry specifies the server name, transport type, command, args, and environment. The harness surfaces MCP tools alongside workspace tools, subject to the active tool profile.

---

## What Is Missing (Ten Governance Gaps)

### Gap 1: No User-Facing Contract Inspection

The `AutonomyContract` type exists in `internal/contracts/autonomy/contract.go` with rich fields for scope, path roles, capabilities, tool policy, obligations, budget, verification, and completion criteria. The harness compiles one per run. But there is **no CLI command or TUI panel** for a user to inspect the active contract before or during a run.

Users cannot answer:
- "What contract is active right now?"
- "What invariants will block my final answer?"
- "What tool policy is in effect for this lane?"

The contract is emitted as structured events into telemetry, but that is a developer debugging surface, not a user governance surface.

**Design target:** `idiom contract inspect` or a TUI contract panel showing active scope, path roles, tool policy, open obligations, completion gates, and budget limits — all derived from the compiled contract object.

### Gap 2: No Per-Session Permission Overrides

Users cannot say "for this session only, allow writes to this extra path" or "for this session, block shell entirely" without editing `.idiom/config.json`. Permission profiles are static config, not per-invocation overrides.

**Design target:** `idiom run --allow-write-to docs/ --deny-tool shell "refactor docs structure"` or equivalent TUI affordance for one-off policy overrides that merge with (but do not persist) the base config.

### Gap 3: No Approval Policy Configuration

The design doc `docs/design-contract-first-local-autonomy.md` envisions an `approval_policy` field with values: `interactive`, `non_interactive`, `auto_review`, `never`, `unavailable`. It also envisions `trust_level`: `trusted_project`, `untrusted_project`, `explicit_full_access`.

Neither field exists in the config schema, the contract compiler, or the runtime enforcement layer today. Users cannot configure:
- Whether the agent should ask before destructive actions.
- Whether writes outside normal scope require approval.
- Whether open-world side effects (web search, network access) require confirmation.

**Design target:** Add `approval_policy` and `trust_level` to the config schema, the contract compilation, and the runtime enforcement (approval gates that pause the run loop and surface a prompt to the user via TUI).

### Gap 4: No Protected Path Configuration

The harness has hardcoded protected paths (`/etc`, `/usr`, etc.) and internal protected path logic in `internal/capabilities/workspace/path_role.go`. Users cannot add their own protected paths (e.g., `secrets/`, `vendor/`, `node_modules/`, `.env`) through config.

**Design target:** A `protected_paths` config block in `.idiom/config.json` that merges custom paths with the system defaults. The path role resolver should consult both sets when classifying a write target.

### Gap 5: No Side-Effect Policy for External/MCP Tools

MCP and external tools have no user-configurable side-effect policy. The design doc envisions ingesting `readOnlyHint`, `destructiveHint`, and `openWorldHint` from tool schemas, then combining those hints with user trust level to decide whether to auto-allow, require approval, or deny.

Today this is unimplemented. All MCP tools are surfaced with the same default rules as workspace tools. Users cannot say "this MCP tool is read-only, allow it unconditionally" or "this external tool is destructive, require approval."

**Design target:** Side-effect annotations on MCP tool entries in config, plus contract-level policy that maps annotation + trust level → auto-allow | require-approval | deny. Emit the policy decision as a structured event so users can audit why a tool was or was not allowed.

### Gap 6: No Obligation Visibility

The contract tracks open obligations: evidence obligations (source-body reads before writes), write obligations (every required edit path must be touched or source-satisfied), verification obligations (tests must pass), final-answer obligations (report changed paths, verification status), and approval obligations (pending user approval).

The obligation ledger exists in `internal/contracts/autonomy/obligation_ledger.go` with open/fulfilled/waived states and timestamps. Users have no way to see:
- "What obligations are still open?"
- "Why did the completion gate reject my final answer?"
- "Which obligation fired the nudge I just received?"

**Design target:** `idiom contract obligations` or a TUI obligations panel showing each obligation's name, type, state, fulfillment evidence, and the invariant that created it. Completion gate rejections should cite the specific open obligation ref.

### Gap 7: No User-Defined Completion Criteria

Users cannot say "this task is complete when `TestFoo` passes" or "this task is complete when these three files are edited and the linter passes." The completion gate is harness-owned and checks generic invariants (no open obligations, writes observed, verification attempted). There is no user-extensible completion contract.

**Design target:** A `completion` block in the runtime brief or config that lets users specify custom criteria: `require_tests`, `require_lint`, `require_files_edited: ["path1", "path2"]`, or a `completion_hook` command that exits zero when the task is done.

### Gap 8: No Audit / History Browser

Conversation logs and telemetry exist as JSONL files under `.idiom/logs/conversations/` and `.idiom/logs/telemetry/`. There is no user-facing command to:
- Browse past runs and their outcomes.
- Compare contracts across runs.
- See what tool calls were rejected and why.
- Replay a session's policy decisions.

The `harness_audit` tool exists but is a developer diagnostic, not a user governance surface. It emits raw JSON, not a browsable history.

**Design target:** `idiom history list` and `idiom history inspect <session-id>` showing contract, tool calls (allowed/rejected), verification results, and final answer for each past session. The TUI could surface this as a session browser panel.

### Gap 9: No Delegation Contract Inheritance Visibility

When the agent delegates work to a subagent (via the `delegate_edit` tool or future `task` tool), the subagent's contract is compiled internally but is not visible to the user. The design doc envisions parent/child contract IDs with result summaries propagated back to the parent contract.

Today users cannot answer:
- "Did the subagent operate under the same policy I approved?"
- "What did the subagent change and why did it stop?"

**Design target:** Delegated subagent contracts inherit the parent's policy but carry their own obligation ledger. The parent contract tracks child contract IDs and their terminal outcomes. A `idiom contract children <session-id>` command surfaces the delegation tree.

### Gap 10: No "Why Was That Decision Made?" Surface

The policy decision engine emits structured events as the run proceeds:
- `tool_intent_classified` — why the tool was classified as read/write/destructive
- `path_roles_resolved` — which path roles matched an edit target
- `tool_policy_decision` — whether a tool call was allowed, nudged, or rejected

All of this information exists in telemetry logs. None of it is queryable by the user during or after a run.

Users cannot ask:
- "Why was that shell command rejected?"
- "Why did the completion gate block my answer?"
- "What evidence was I missing that slowed me down?"

**Design target:** A `idiom decision why --session <id> --tool-call <n>` command that retrieves the policy decision chain for a specific tool call, citing the contract invariant, path role, tool profile, and obligation state that produced the decision.

---

## Migration Notes

These ten gaps are independent enough to address in any order. The recommended sequencing, based on user impact and internal dependency:

1. **Gap 1 (contract inspection)** — no new enforcement, just surface the contract object that already exists. Highest leverage.
2. **Gap 10 (decision explanation)** — reuses the same structured events. High user trust impact.
3. **Gap 3 (approval policy)** — unlocks interactive trust scenarios. Requires TUI support for approval prompts.
4. **Gap 6 (obligation visibility)** — completes the contract inspection story.
5. **Gap 2 (per-session overrides)** — power user feature, straightforward CLI flag parsing.
6. **Gap 4 (protected path config)** — config schema change, low risk.
7. **Gap 5 (MCP side-effect policy)** — requires tool annotation ingestion, medium complexity.
8. **Gap 8 (audit browser)** — read-only, large surface area but low risk.
9. **Gap 7 (user completion criteria)** — changes the completion gate contract, needs careful testing.
10. **Gap 9 (delegation visibility)** — depends on subagent contract maturity.

Each gap should be tracked as a separate implementation atom with its own design, acceptance criteria, and eval fixtures. Do not attempt all ten in a single broad change.

## Verification

Each gap fix should be verified by:
1. **Config/CLI test**: the new flag, config key, or panel renders and round-trips correctly.
2. **Enforcement test**: a session with the new policy in effect produces the expected allow/deny/approval/nudge behavior.
3. **Audit test**: the structured events emitted contain the contract ID, invariant name, and decision detail needed for replay analysis.
4. **Eval regression**: existing eval fixtures pass unchanged, proving the new governance lever does not alter behavior when not engaged.
