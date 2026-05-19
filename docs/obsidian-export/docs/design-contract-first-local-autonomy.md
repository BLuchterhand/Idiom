# Contract-First Local Autonomy

Status: draft design pillar
Evidence snapshot: use current local evidence when adapting this design.

This is one pillar in the coordinated roadmap:
[harness-design-roadmap.md](harness-design-roadmap.md). It supplies the authority
boundary consumed by persistent memory, capability routing, auditable handoff,
local-model operations, the eval/audit flywheel, and sidecar APIs.

## Decision

Agent Harness should make **contract-first local autonomy** a first-class design pillar: every autonomous run, lane, phase, or delegated worker should execute under a typed, locally enforced, auditable `AutonomyContract`. The contract is compiled by the harness before the model acts. It is the single source of truth for what the model may read, write, execute, delegate, verify, and claim complete.

The model still owns strategy. The harness owns contracts. This is the difference between an autonomous coding agent and an unbounded chat model with tools.

The contract must drive four surfaces from the same object:

- Model-facing guidance: the compact control rails, task brief, available capabilities, and recovery nudges.
- Runtime enforcement: tool visibility, path roles, permission decisions, protected path checks, side-effect approval, verification obligations, loop and budget fuses, and completion gates.
- Audit and replay: structured events that explain which contract existed, which invariant was evaluated, and why a tool call or final answer was allowed, nudged, rejected, or blocked.
- Eval: assertions over contract coverage, enforcement accuracy, verification follow-through, autonomy efficiency, and regression absence.

This pillar does not mean "tell the model to be careful." It means the harness has a typed contract object that can be shown to the model, enforced outside the model, replayed from logs, and evaluated without reading prose transcripts by hand.

## Problem Statement

Local coding agents fail in two opposite ways.

First, unconstrained autonomy is unsafe and noisy. A model with shell, edit, search, MCP, and external tools can wander across the repository, overwrite the wrong files, follow prompt injection from web or tool output, skip verification, reopen discovery after it already has enough evidence, or claim completion without satisfying the user's constraints. Prompt reminders reduce the frequency of these failures, but they are not a reliable boundary because context pressure, tool results, recovery text, and delegated subagents can all dilute or contradict prose rules.

Second, over-constrained autonomy is weak. If the harness hard-picks the next tool call, hardcodes scenario templates, or forces a rigid plan after every failure, it turns the model into a low-agency script runner. That undermines Agent Harness's core thesis: use local models for useful autonomous engineering by giving them strong evidence surfaces and letting them decide tactics inside real boundaries.

Agent Harness already has pieces of the right answer: runtime briefs, permission profiles, task families, control rails, hidden tools, scoped write checks, readiness states, plan-then-execute guards, completion gates, harness audit, eval expectations, repository context packets, and graph-backed memory. But these pieces are not yet one canonical contract lifecycle. Several checks reconstruct similar authority from different local inputs. Some constraints are model-facing but not enforced. Some enforcement exists but is not emitted as stable contract decisions. Some audit findings identify behavior drift but cannot yet tie every drift to a specific contract ID and invariant.

The design problem is to make local autonomy both more capable and more bounded without collapsing into either prompt magic or command-and-control scripting.

Contract-first local autonomy solves this by making the run contract the harness-owned domain object. The model can decide how to solve the task, but every consequential action passes through a contract that is explicit, scoped, evented, and locally enforceable.

## External Landscape

The external agent landscape is converging on a common lesson: capable coding agents need policy and interface design outside the model. The useful patterns are permissions, sandboxing, tool annotations, hooks, subagents, repo maps, specialized agent-computer interfaces, and post-edit verification. The gap is that most systems expose those patterns as settings, modes, or hooks rather than as one typed task contract that drives guidance, enforcement, audit, and eval together.

### external coding agent

external coding agent is a terminal pair-programming agent for local git repositories (<https://vendor.example/external-agent-docs>). Its strongest contract-adjacent ideas are context ranking and edit verification:

- The repo map gives the model repository-wide symbolic awareness by showing classes, functions, and signatures, then selects the most relevant parts of the map using graph ranking under a token budget (<https://vendor.example/external-agent-docs>).
- Chat modes separate ask, architect, code, and help workflows, and external coding agent explicitly recommends moving from discussion to edit mode once the plan is understood (<https://vendor.example/external-agent-docs>).
- external coding agent can automatically lint and test edited files, using non-zero exits as repair signals after AI edits (<https://vendor.example/external-agent-docs>).

The lesson for Agent Harness: context ranking and verification should be harness surfaces, not just conversation advice. external coding agent proves that a smaller, more relevant interface can improve coding behavior. Agent Harness should take that further by binding relevance and verification to a task contract: which packet made a path relevant, which paths are writable, which tests satisfy the contract, and which claims remain open.

### external coding agent

external coding agent exposes a mature separation between instructions and enforcement. Its permissions documentation says permission rules are enforced by the host, not by prompts or project instructions; prompts shape what the model tries, but permissions decide what is allowed (<https://code.agent-config.com/docs/en/permissions>). It provides tool and path rules for Bash, Read, Edit, WebFetch, MCP, and subagents; supports modes such as plan and accept-edits; and documents that permissions and sandboxing are complementary layers (<https://code.agent-config.com/docs/en/permissions>).

external coding agent hooks add deterministic lifecycle interception. `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStop`, `SessionStart`, and related events can block, add context, or provide structured decisions to the agent (<https://code.agent-config.com/docs/en/hooks>). Subagents run in separate contexts with specific tool access and independent permissions (<https://code.agent-config.com/docs/en/sub-agents>).

The lesson for Agent Harness: host-level enforcement and lifecycle hooks are the right shape. But Agent Harness should not bolt policy on as a parallel hook ecosystem only. It should compile a contract once per phase, then have hook-like enforcement points consume that same object.

### external coding agent

external coding agent exposes agent-specific permissions and modes. Its agent documentation supports primary agents and subagents, with per-agent permissions for read, edit, bash, search, LSP, task delegation, external directories, and related tools; permissions can be `ask`, `allow`, or `deny`, and task permissions can hide denied subagents from the model's task tool description (<https://external-agent.ai/docs/agents/>). Its tools documentation describes built-in tools and permission keys, including that `edit` covers write, edit, and apply_patch (<https://external-agent.ai/docs/tools/>).

external coding agent's plugin system gives another hook-like extension surface: plugins can subscribe to events including `tool.execute.before`, `tool.execute.after`, permission events, session events, file events, and compaction events (<https://external-agent.ai/docs/plugins/>). Official examples include blocking reads of `.env` files before tool execution and injecting environment variables into shell execution.

The lesson for Agent Harness: permission granularity must be tool-type aware, path aware, and agent/phase aware. Plugin and hook events are useful, but a project-defining autonomy pillar needs a canonical policy object so user config, project config, route plans, runtime briefs, and audit all agree.

### external coding agent CLI And App

external coding agent's public documentation separates sandboxing from approvals. Sandbox mode controls what commands can technically touch; approval policy controls when external coding agent must ask before taking an action (<https://vendor.example/agent-docs>). Defaults include no network access and writes limited to the active workspace for local CLI/IDE runs; read-only mode is intended for chat or planning without changes (<https://vendor.example/agent-docs>).

external coding agent also protects key writable-root paths in the default workspace-write sandbox: `.git`, `.agents`, and `.agent-config` are recursively read-only even when their parent workspace is writable (<https://vendor.example/agent-docs>). Its config reference exposes `sandbox_mode`, `approval_policy`, granular approval categories, named filesystem and network permission profiles, app tool destructive/open-world controls, project trust, and external coding agent hooks (<https://vendor.example/agent-docs>). API provider's app review guidance also treats `readOnlyHint`, `destructiveHint`, and `openWorldHint` as side-effect metadata that must match tool behavior (<https://vendor.example/agent-docs>).

The lesson for Agent Harness: autonomy has at least three layers: technical sandbox capability, approval policy, and task contract. Agent Harness is currently a local harness, not an OS sandbox, so it must be honest about what it can enforce directly and where it needs an external sandbox, container, or user approval boundary.

### SWE-agent And Agent-Computer Interfaces

SWE-agent's 2024 arXiv preprint argues that language-model agents need purpose-built interfaces for software engineering, and that interface design materially affects agent behavior and performance (<https://arxiv.org/abs/2405.15793>). Its ACI documentation describes LM-centric commands and feedback formats for browsing, editing, running code, and receiving concise outputs; the interface includes syntax checks on edit, a bounded file viewer, succinct repository search, and explicit successful-empty-output feedback (<https://swe-agent.com/0.7/background/aci/>). Its architecture runs actions through an environment that manages a shell session, repository setup, history compression, and custom commands (<https://swe-agent.com/0.7/background/architecture/>).

The lesson for Agent Harness: the tool interface is not neutral. Smaller local models are especially sensitive to affordance shape, output size, and feedback clarity. Contract-first autonomy should therefore constrain not only dangerous operations, but also the shape and timing of discovery, edit, verification, and completion signals.

### MCP Tool Annotations And Hook Literature

The Model Context Protocol schema includes tool annotations: `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` (<https://modelcontextprotocol.io/specification/YYYY-MM-DD/schema>). The spec explicitly treats these as hints, not guarantees, and warns clients not to make tool-use decisions from untrusted server annotations alone (<https://modelcontextprotocol.io/specification/YYYY-MM-DD/schema>).

The official MCP blog frames annotations as a risk vocabulary rather than enforcement. They can feed policy engines, but they do not make a model resist prompt injection, cannot stop an untrusted server from lying, and do not replace sandboxing or deterministic controls (<https://blog.modelcontextprotocol.io/posts/YYYY-MM-DD-tool-annotations/>).

The lesson for Agent Harness: annotations are useful inputs to a contract compiler. They are not the contract. Agent Harness should ingest trusted tool metadata and treat untrusted metadata conservatively, then enforce actual decisions in the harness and workspace boundary.

### Landscape Conclusion

external coding agent demonstrates repository context and repair loops. external coding agent demonstrates host-enforced permissions, lifecycle hooks, and independently permissioned subagents. external coding agent demonstrates agent-specific allow/ask/deny permissions and plugin-level event interception. external coding agent demonstrates the need to separate sandbox capability, approvals, protected paths, and tool side-effect hints. SWE-agent demonstrates that the agent-computer interface itself is a performance lever. MCP demonstrates that tool annotations are a vocabulary, not enforcement.

Agent Harness's opportunity is to combine these into a local, evidence-first, contract-shaped runtime: not just "which tools are enabled," but "what task contract is this phase executing, what evidence made it valid, what invariants are active, what obligations are open, what action was decided, and what audit/eval signal did that produce?"

## Agent Harness-Specific Thesis

Agent Harness is not trying to win by wrapping a frontier model in convenience UX. Its strategic thesis is narrower and more durable: build a local, evidence-first coding-agent harness where smaller local models can work by leaning on contracts, repository memory, verification, auditability, and learning loops. The core boundary is already stated in the project: the model owns strategy; the harness owns contracts.

Contract-first local autonomy is the pillar that makes that boundary real.

### What The Harness Owns

The harness owns the objective boundary, not the solution path:

- Permission profile: read-only, guarded write, verification-only, planner-only, no-tools, or explicit full-access mode.
- Workspace trust and roots: what local filesystem roots are in scope, which roots are writable, which are read-only, and which are forbidden.
- Target roles: required edit paths, allowed edit paths, evidence-only paths, protected paths, generated artifacts, external side-effect tools, and public-world tools.
- Tool capabilities: semantic discovery, bounded file reads, structural search, shell verification, source edits, plan submission, external tools, MCP tools, delegated workers, web search, and app connectors.
- Readiness gates: what evidence is sufficient, what exact obligations remain, and what further reads require.
- Verification contract: what counts as verification, which commands or source-aware checks are acceptable, and what the final answer must report if verification cannot run.
- Recovery policy: when to nudge, when to reject, when to force a stop, and when to ask the user.
- Audit identity: stable contract IDs, invariant IDs, policy decisions, and replayable event state.

The model owns how to localize the bug, which narrow tool to use next, how to edit the code, how to interpret test failures, and what final explanation best answers the user, as long as those choices satisfy the active contract.

### What Contract-First Means

Contract-first means the contract exists before the model action it governs. It is not inferred after the fact from the model's prose.

The contract is compiled from:

- User request, explicit boundaries, and current lane.
- Route plan, task family, workflow phase, and runtime brief.
- Project config, trust settings, workspace mode, tool rules, and protected path policy.
- Context packet handles, target refs, required edit paths, required evidence, and repo memory freshness.
- Tool manifest schemas, MCP/app annotations, and side-effect classifications.
- Eval fixture expectations and non-interactive execution settings where present.
- Current run state: writes already made, verification obligations, loop fuses, budget, readiness, and pending approvals.

The same compiled object then creates the prompt-facing `control_rails`, filters visible tools, evaluates tool calls, opens and resolves obligations, and emits audit events.

### Why Local Autonomy Needs Contracts More Than Cloud Autonomy

Local autonomy has advantages: privacy, offline operation, user-owned models, direct repository access, cheap iteration, and no remote worktree handoff. It also has risks:

- Local commands can touch real user files unless an OS sandbox or container is configured.
- Local models may be more brittle under long context, repeated errors, or dense tool manifests.
- Users may run non-interactively, so "ask me later" is not always available.
- Tooling can span shell, MCP, local language servers, browser surfaces, app connectors, and external agents.
- Logs and repository memory are local artifacts whose freshness and retention must be clear.

The answer is not to remove autonomy. The answer is to make autonomy explicit enough that local execution can be trusted, replayed, measured, and improved.

## Current Repo Evidence And Capabilities

The current repository already contains many of the primitives needed for contract-first local autonomy. The design should consolidate them rather than replace them.

### Strategic And Policy Grounding

The strategic docs already define the north star:

- `docs/harness-strategic-thesis.md` describes Agent Harness as a local, evidence-first coding-agent harness for smaller/local models where contracts, repository memory, verification, auditability, and learning loops are the differentiators.
- `docs/decisioning-north-star.md` states that the model owns strategy and the harness owns contracts. It also says to force only real invariants: permissions, protected paths, protocol correctness, verification, completion readiness, loop breaks, and exhausted budgets.
- `.agent-config/project-context.md` frames the system as a local local model/llama.cpp harness with routing, workspace tools, readiness gates, and eval infrastructure.

This design doc does not introduce a new philosophy. It makes the existing philosophy implementable as one runtime contract.

### Runtime Brief As Contract Skeleton

`internal/frontier/workerruntime/runtime.go` already defines `WorkerRuntimeBrief`, which is close to a contract input object. It includes lane, task family, contract, objective, permission profile, target refs, required edit paths, required snippets, evidence paths, changed paths, user boundaries, allowed and forbidden tools, tool manifest, required evidence, required tools, readiness gate, search policy, discovery examples, verification contract, verification plan, stop conditions, final critic, target packet, constraints, required next actions, invalid actions, readiness state, and budget.

`ReadinessState` already tracks readiness to answer, write, and verify, plus closed claims, open obligations, evidence sources, and the condition under which further reads are justified.

This is the right domain vocabulary. The missing step is to compile it into an immutable, versioned `AutonomyContract` that enforcement and audit consume directly.

### Task-Family Runtime Catalog

`internal/frontier/workerruntime/catalog.go` maps task families to contracts, permission profiles, tool profiles, readiness gates, search policies, required evidence, and verification contracts.

Examples in the current catalog:

- Direct questions get `conversation_only` with write tools forbidden and no verification required for read-only lanes.
- Debug log tasks get `read_only_workspace` with required log evidence.
- Verification-only tasks get `read_only_verification_shell` with shell required and edit tools forbidden.
- Locate-then-implement and implementation-style tasks get guarded writes, required changed-path evidence, and verification status.
- Plan-then-execute planner phases are guarded so the planner must submit a plan rather than edit files directly.

This is already a contract taxonomy. Contract-first autonomy should preserve the catalog but make it one input to the compiler, not the only policy source.

### Control Rails Event

`internal/agent/runner.go` already builds `runnerControlRails` and emits a `control_rails` event. The event carries lane, follow-up lane, contract, agent mode, workflow kind and phase, task family, permission profile, target refs, required edit paths, evidence paths, required tools, allowed and forbidden tools, readiness gate, search policy, verification contract, stop conditions, final critic, dynamic instructions, and failure-to-eval state.

The current event is exactly where contract-first autonomy can begin. It should evolve from "rails reconstructed for presentation" into "presentation view of the compiled contract."

### Tool Surface And Capability Shaping

The runner already hides tools based on phase and contract state:

- `workspaceAllowedTools` in `internal/app/turn_planner.go` starts from workspace `AllowedTools`.
- `internal/workspace/manager.go` hides write tools in explore/read-only mode and appends external and MCP tool names deterministically.
- `internal/workspace/manager.go` builds schemas for built-in tools, external tools, and MCP tools, and filters by allowed tool names.
- `internal/agent/runner.go` hides implementation, prewrite, scoped-edit, and exact-anchor read tools in several states.
- The scoped semantic discovery set prefers graph, semantic, structural, dependency, symbol, and diagnostics tools while excluding broad list/search/shell surfaces.

This is strong evidence that Agent Harness already treats the tool list as a policy surface. Contract-first autonomy should replace scattered allow-list construction with a capability resolver backed by the active contract.

### Write Scope And Plan-Then-Execute Guards

The runner already distinguishes read contracts from write scopes:

- A required edit path is treated as a write allowlist, not as proof that the model has read enough evidence.
- Pre-write and scoped edit surfaces activate when mutation is required and required edit paths remain.
- `requiredEditPathScopeRejection` rejects workspace writes outside required edit paths.
- `internal/agent/runner_plan_then_execute_guard.go` hard-rejects per-edit write tools in planner phases and directs the model to `submit_plan`.
- The same guard checks that submitted plans cover every required edit path and do not include outside paths.

These checks are contract invariants already. They need stable invariant IDs, contract IDs, and shared path-role resolution.

### Completion And Verification Gates

`internal/agent/runner.go` already rejects final answers when mutable work is incomplete:

- No successful write after an implementation request, except for plan-then-execute planning phases.
- Claimed changed paths unsupported by observed writes.
- Missing required edit paths or required snippets.
- Pending verification after writes.
- Source edits without source-aware verification.

The eval code already includes expectations such as required run events and route-plan snapshots (`internal/eval/eval.go`), and performance analysis detects anti-patterns like broad rediscovery after the completion gate (`internal/eval/perf.go`).

These gates should become contract obligations and contract completion decisions, not standalone runner checks.

### Tool Rules And Tool Manifests

`internal/config/config.go` exposes workspace shell/write settings and `ToolRules`, with per-tool allow/ask/deny decisions. `internal/workspace/tool_rules.go` implements external coding agent-style first-match rules with tool names, shell command patterns, path patterns, and MCP catch-all support. `internal/workspace/tool_manifest.go` defines workspace write tools and `IsWorkspaceWriteTool`.

This gives Agent Harness the low-level permission vocabulary needed for the contract compiler. The compiler should not remove these rules. It should normalize them into a per-phase policy decision graph.

### Harness Audit Baseline

A current `harness_audit` run reported:

- Tool surface alignment: 129 tools in manifest, schema, and suggestion surfaces, with 0 drift.
- Logs scanned: 86.
- Findings: 11.
- Questions: 9.
- Behavior findings include zero-hit searches, redirect storms, same-tool repeats, truncated searches, doc wander, and tool error streaks.
- One high-severity incomplete implementation session lifecycle finding: an implement session missing `session_summary`.
- Ranker learning idle: no ranker weights or learning log found.
- Context cache mostly stale: 32 stale rows out of 35, about 91 percent stale.
- Top observed logged tools included `read_file` at 224 calls, shell at 58, and `search_content` at 57.

This baseline matters because it shows both maturity and opportunity. Tool schemas are aligned, but behavior still drifts into repeated reads, broad search, redirect storms, stale cache, and incomplete lifecycle records. Contract-first autonomy should give audit a stable contract lens for these findings.

### Current Gaps

The key gaps are not missing features; they are missing ownership and normalization:

- No single immutable `AutonomyContract` object owns the relationship between route plan, runtime brief, tool rules, workspace config, context packet, verification plan, and completion criteria.
- Control rails are evented, but not yet clearly tied to a contract ID and invariant set that also drives tool policy decisions.
- Path concepts are spread across required edit paths, evidence paths, target refs, workspace roots, protected paths, and tool arguments. They need a common target-role model.
- Side-effect classes from MCP/app annotations are not yet normalized into the same policy engine as shell/edit/read actions.
- Several checks reject invalid behavior, but audit cannot always reconstruct which invariant fired and what safe alternatives were offered.
- Readiness state exists, but its obligations should be the source for verification and final-answer gates.
- Broad reads and searches remain common enough that scoped discovery needs better contract-backed incentives and metrics.

## Proposed Architecture

Contract-first local autonomy should be implemented as a small domain model with clear boundaries, not as another prompt layer.

### Core Flow

The runtime flow should be:

1. Classify the user request into lane, task family, workflow kind, and phase.
2. Gather contract inputs: route plan, runtime catalog entry, workspace config, tool rules, project trust, context packet, user boundaries, target refs, current run state, tool manifests, external/MCP annotations, eval expectations, and approval mode.
3. Compile an immutable `AutonomyContract`.
4. Emit `contract_compiled`.
5. Derive model-facing control rails and tool visibility from the contract.
6. For each model action, classify the tool intent against the contract.
7. Resolve paths, targets, side effects, and capability roles.
8. Allow, ask, nudge, reject, or stop based on stable invariants.
9. Emit policy, obligation, readiness, verification, and completion events.
10. Recompile the contract only at phase boundaries or state transitions that materially change obligations: successful write, approval result, verification result, context freshness change, delegated worker result, route-plan transition, or user instruction update.

### Boundary Placement

Use a layered boundary that matches the existing repo:

- `internal/frontier/workerruntime`: owns task-family defaults, runtime brief, and contract input vocabulary.
- A new internal contract/policy package, such as `internal/agent/contract` or `internal/frontier/autonomy`, owns `AutonomyContract`, target roles, invariants, and policy decision types. This package should not depend on the runner's transcript mechanics.
- `internal/agent`: compiles the contract for each model turn/phase, presents control rails, handles runner state transitions, and calls the policy engine before executing tools or accepting final answers.
- `internal/workspace`: remains the adapter for actual tool execution, but receives normalized policy decisions and path-role checks. It should enforce workspace and path decisions as close to execution as possible.
- `internal/eval` and `internal/workspace/harness_audit.go`: consume structured events and summarize coverage, drift, invariants, and regressions.

This keeps the contract as a domain object rather than runner glue.

### Autonomy Contract

An `AutonomyContract` should be immutable within a phase. Recompilation creates a new version with a parent ID.

Suggested shape:

```yaml
AutonomyContract:
  id: content-addressed string
  schema_version: integer
  parent_id: optional string
  run:
    session_id: string
    turn_id: string
    lane: string
    task_family: string
    workflow_kind: string
    workflow_phase: string
    agent_mode: string
  authority:
    contract_kind: conversation_only | read_only_workspace | guarded_workspace_write | planner_submit_only | verification_only | full_access
    permission_profile: string
    approval_policy: interactive | non_interactive | auto_review | never | unavailable
    trust_level: trusted_project | untrusted_project | explicit_full_access
    source_precedence: [user, project, runtime_catalog, route_plan, tool_rules, eval_fixture]
  workspace:
    cwd: string
    roots:
      - path: string
        role: read_only | writable | forbidden
    protected_paths:
      - pattern: string
        reason: git_metadata | agent_config | external-agent_config | secret | user_boundary
    network:
      shell_network: denied | allowlist | full | unknown
      web_search: disabled | cached | live | unknown
  targets:
    - ref: string
      role: required_edit | allowed_edit | evidence_readonly | generated_artifact | forbidden | protected | verification_output
      path_pattern: optional string
      symbol_handle: optional string
      packet_id: optional string
      freshness: fresh | stale | unknown
      reason: string
  capabilities:
    allowed:
      - semantic_discovery
      - bounded_read
      - structural_search
      - source_edit
      - apply_patch
      - shell_verification
      - submit_plan
    forbidden:
      - unbounded_write
      - mutating_shell
      - public_world_side_effect
    hidden_tools:
      - string
  tool_policy:
    rules:
      - tool_pattern: string
        decision: allow | ask | deny
        capability: string
        side_effect: read_only | additive | destructive | open_world | unknown
        source: string
  obligations:
    required_evidence: [string]
    required_tools: [string]
    required_edit_paths: [string]
    required_snippets: [string]
    verification_contract: string
    readiness_gate: string
    open:
      - obligation_id: string
        kind: evidence | write | verify | final_answer | approval
        status: open | resolved | waived
  recovery:
    invalid_actions:
      - invariant_id: string
        action: string
        response: nudge | reject | stop | ask
    loop_budget:
      max_same_tool_repeats: integer
      max_redirects: integer
      max_zero_hit_searches: integer
  audit:
    expected_events: [string]
    eval_expectations: [string]
    redaction_profile: string
```

Not every field has to ship in phase 1. The important design rule is that each field has one owner and each enforcement decision points back to the contract.

### Target Roles

Path strings alone are too weak. The contract needs target roles:

- `required_edit`: must be written, or the final answer must include a contract-approved no-op reason.
- `allowed_edit`: may be written if evidence supports it.
- `evidence_readonly`: may be read and cited but not modified.
- `generated_artifact`: may be regenerated but should not be hand-edited unless the source contract says so.
- `protected`: cannot be written even if under a writable root.
- `forbidden`: cannot be read or written.
- `verification_output`: logs, test output, or trace files that can satisfy evidence but should not become instructions.
- `external_side_effect`: tool/action can affect state outside the local repo.
- `public_world`: tool/action can affect publicly visible internet or third-party state.

Each tool call should resolve zero or more target roles before execution. For shell commands, this requires command classification plus path extraction where practical. For MCP/app tools, it requires tool metadata plus argument inspection. Unknown should default conservatively.

### Capability Router

Tools are implementation details. Contracts should reason in capabilities:

- `semantic_discovery`: `context_pack`, `context_query`, `context_zoom`, language symbol tools.
- `literal_search`: `search_content`, grep-like tools.
- `bounded_read`: file reads with ranges, packet zooms, symbol reads.
- `unbounded_read`: large dumps, repository-wide raw listing.
- `source_edit`: `edit_file`, `apply_patch`, `write_file`, language-aware moves.
- `plan_submission`: `submit_plan`.
- `shell_verification`: `go test`, `go build`, make targets, linters, source sanity.
- `mutating_shell`: shell commands with filesystem, network, package manager, git, or process side effects.
- `external_read`: web search, web fetch, MCP reads, app reads.
- `external_write`: emails, issues, PRs, public posts, remote mutations.
- `delegation`: subagents, workers, external agents.

The runner can still expose tool names to the model, but enforcement should happen at the capability level so that new tools do not bypass old policy.

### Invariants And Nudges

The contract should distinguish invariants from nudges.

Hard invariants:

- Protected paths cannot be written.
- Forbidden paths cannot be read or written.
- Planner-only phases cannot call per-edit write tools.
- Required edit path scopes cannot be bypassed.
- Mutating shell commands require the matching permission profile.
- Destructive or open-world external side effects require approval unless explicitly pre-approved.
- A final answer cannot close a mutable task while write, snippet, verification, or evidence obligations remain open.
- Repeated tool loops and exhausted budgets must stop or redirect.

Nudges:

- Prefer `context_pack` before raw broad search for localization.
- Prefer semantic discovery over repeated literal search when the index is fresh.
- Use the narrowest relevant test before the full suite.
- After sufficient evidence, act or answer rather than keep reading.
- If a generated artifact is encountered, inspect the source before editing the artifact.

Nudges should be model-facing and recoverable. Invariants should be enforced outside the model and evented.

## Data Model And Events

Structured events are the difference between a contract and a prompt. Every event should carry `contract_id`, `contract_version`, `session_id`, `turn_id`, and `phase` where available. Tool-related events should also carry `tool_call_id`.

### Contract Lifecycle Events

| Event | Purpose | Key fields |
| --- | --- | --- |
| `contract_compiled` | A new contract version exists. | `contract_id`, `parent_id`, `schema_version`, `lane`, `task_family`, `phase`, `permission_profile`, `authority_sources`, `target_role_counts`, `capabilities_allowed`, `capabilities_forbidden`, `obligation_counts`, `protected_path_count`, `tool_policy_count` |
| `contract_presented` | Model-facing rails were derived from the contract. Existing `control_rails` can either become this or reference it. | `contract_id`, `rails_hash`, `allowed_tools`, `hidden_tools`, `required_evidence`, `readiness_gate`, `verification_contract` |
| `contract_recompiled` | State changed enough to require a new version. | `old_contract_id`, `new_contract_id`, `reason`, `state_delta` |
| `contract_stopped` | The contract forced a stop, pause, or user handoff. | `contract_id`, `invariant_id`, `reason`, `remaining_obligations` |

### Tool Policy Events

| Event | Purpose | Key fields |
| --- | --- | --- |
| `tool_intent_classified` | Model requested a tool; harness mapped it to capabilities and side effects. | `tool_call_id`, `tool_name`, `capability`, `side_effect_class`, `confidence`, `arguments_summary` |
| `path_roles_resolved` | Tool arguments were resolved to target roles. | `tool_call_id`, `paths`, `roles`, `protected_matches`, `required_edit_matches`, `outside_workspace` |
| `tool_policy_decision` | The contract decided allow, ask, nudge, reject, or stop. | `tool_call_id`, `decision`, `invariant_id`, `reason_code`, `approval_required`, `safe_alternatives` |
| `tool_policy_result` | The execution result updated contract state. | `tool_call_id`, `tool_name`, `success`, `error_class`, `changed_paths`, `evidence_paths`, `obligations_opened`, `obligations_resolved` |

### Obligation And Readiness Events

| Event | Purpose | Key fields |
| --- | --- | --- |
| `obligation_opened` | A write, evidence, verification, approval, or final-answer obligation was created. | `obligation_id`, `kind`, `source_event`, `target_ref`, `required_by` |
| `obligation_resolved` | An obligation was satisfied. | `obligation_id`, `kind`, `resolution`, `evidence_event`, `evidence_path`, `command` |
| `obligation_waived` | An obligation was intentionally skipped. | `obligation_id`, `kind`, `waiver_reason`, `approved_by`, `final_answer_required_text` |
| `readiness_state_updated` | Readiness changed after evidence, writes, verification, or failures. | `ready_to_answer`, `ready_to_write`, `ready_to_verify`, `closed_claims`, `open_obligations`, `further_reads_require` |
| `completion_gate_decision` | Final answer was allowed or rejected. | `decision`, `invariant_id`, `remaining_obligations`, `verification_status`, `changed_paths`, `required_paths_status` |

### Audit And Eval Events

| Event | Purpose | Key fields |
| --- | --- | --- |
| `contract_audit_summary` | Per-session summary for harness audit. | `contract_count`, `tool_decision_count`, `reject_count`, `nudge_count`, `approval_count`, `unresolved_obligation_count`, `loop_break_count` |
| `contract_eval_assertion` | Eval assertion over contract behavior. | `expectation_id`, `passed`, `contract_id`, `actual`, `expected`, `evidence_event_ids` |
| `contract_replay_gap` | Logs were insufficient to reconstruct a decision. | `contract_id`, `missing_event`, `tool_call_id`, `severity` |

### Policy Decision Type

Policy decisions should be explicit:

```yaml
PolicyDecision:
  id: string
  contract_id: string
  tool_call_id: string
  tool_name: string
  capability: string
  decision: allow | ask | nudge | reject | stop
  invariant_id: string
  reason_code: string
  reason: string
  target_roles:
    - path: string
      role: string
      matched_pattern: string
  side_effect:
    class: read_only | additive | destructive | open_world | unknown
    trusted_annotation: boolean
  safe_alternatives:
    - capability: string
      tool_names: [string]
      note: string
```

The model-facing text can be short. The event should be complete enough for audit and replay.

## Runner And Control-Rail Changes

The runner should migrate from rail reconstruction to contract consumption in stages.

### Compile A Contract Before Each Phase

Before each model turn that can use tools, the runner should compile an `AutonomyContract` from:

- Route plan and lane.
- Runtime catalog entry.
- Current `WorkerRuntimeBrief`.
- Workspace config and `ToolRules`.
- Tool manifest and tool annotations.
- Context packet and freshness state.
- Current runner state: successful writes, changed paths, verification results, loop counters, open obligations, and user boundaries.
- Eval fixtures or non-interactive execution constraints.

Emit `contract_compiled` before the model sees any contract-derived guidance.

### Derive Control Rails From The Contract

Existing `control_rails` should become a view over `AutonomyContract`. Its fields should include `contract_id` and `contract_version`. Fields that are currently reconstructed independently should either be copied from the contract or computed by a pure presenter.

This prevents a common class of drift: the model sees one set of rails while the tool executor enforces another.

### Replace Tool Hiding With Capability Resolution

The current tool-hiding logic is useful but scattered. Replace it gradually with:

1. Map every tool to one or more capabilities and side-effect classes.
2. For each contract, compute allowed, ask, denied, and hidden capabilities.
3. Materialize tool visibility from capability decisions.
4. Keep tool-specific overrides for known exceptions.

This lets new MCP or external tools default to conservative handling rather than silently inheriting broad access.

### Add A Path-Role Resolver

Introduce a shared resolver that can answer:

- Is this path inside an allowed root?
- Is this path protected even though its root is writable?
- Does this path match a required edit, allowed edit, evidence-only, forbidden, or generated artifact role?
- Is this path being accessed through a symlink or move?
- Does this shell command imply writes outside the explicit file arguments?
- Does this patch add, update, move, or delete paths outside scope?

The resolver should be used by workspace writes, reads when forbidden paths exist, shell policy, apply_patch, language-aware moves, MCP filesystem tools, and final-answer changed-path validation.

### Turn Existing Guards Into Named Invariants

Existing runner checks should get stable invariant IDs. Examples:

- `planner.submit_plan_only`: planner phases cannot call edit/write/apply_patch/delegate_edit/mkdir/go_move_declarations, shell/git/source-sanity commands, or task/task_ledger delegation.
- `planner.required_path_coverage`: submitted plans must cover all required edit paths and no outside paths.
- `write.required_path_scope`: writes must be within required edit paths when that scope is active.
- `write.protected_path`: protected paths are read-only.
- `verify.after_write`: mutable tasks need post-write verification or a blocker.
- `verify.source_aware`: source edits require source-aware verification, not only patch hygiene.
- `complete.changed_paths_observed`: final claimed paths must match observed writes or no-op reasons.
- `complete.no_open_obligations`: final answer cannot close with open required obligations.
- `loop.same_tool_repeat`: repeated same-tool failures trigger redirect or stop.
- `discovery.after_ready`: broad rediscovery after readiness is rejected unless a named missing claim exists.

The event stream should name the invariant every time it fires.

### Integrate Side-Effect Hints Conservatively

For app, MCP, and external tools:

- Treat trusted `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` as inputs.
- Treat untrusted or absent annotations as `unknown` and potentially state-changing/open-world.
- Combine annotations with local tool registry knowledge and project policy.
- Require approval or denial for destructive/open-world actions unless the contract explicitly permits them.
- Mark sessions that consume open-world/untrusted output so subsequent tool decisions can tighten if private workspace data is also in context.

This matches the MCP lesson: hints inform a policy engine, but enforcement remains in the harness.

### Recompile On State Transitions

Recompile contracts only when state changes materially:

- Phase changes: explore -> implement, planner -> executor, write -> verify, verify -> final.
- User grants or denies approval.
- First successful write opens verification obligations.
- Verification passes, fails, or is skipped with blocker.
- Context packet freshness changes.
- Required path coverage changes.
- Delegated worker result returns changed paths, evidence paths, or failures.
- The user changes scope or explicitly grants broader access.

Avoid recompiling on every read or search unless the event changes readiness or obligations. Recompilation should be stable enough for audit and cheap enough for local runs.

### Keep Recovery Text Generic And Evidence-Based

Policy rejection should give the model enough information to recover without prescribing the next tool when not necessary:

- Bad: "Call `context_pack` now."
- Better: "This phase is read-only and broad search already repeated twice. Name the exact missing claim before further discovery, or answer from the current evidence."
- Good when invariant-specific: "Planner phases cannot edit files. Submit a plan covering `docs/design-contract-first-local-autonomy.md`, or explain why no edit is needed."

The harness may name safe capabilities and tool categories, but it should not seize strategy unless an invariant demands a specific protocol step such as `submit_plan`.

## Audit And Eval Metrics

Contract-first autonomy should be measured. The point is not to add more rails; it is to produce fewer unsafe actions, fewer loops, faster useful edits, and more replayable completions.

### Coverage Metrics

- `contract_compiled_rate`: percent of tool-capable runs with at least one `contract_compiled`.
- `control_rails_contract_link_rate`: percent of `control_rails` or `contract_presented` events with a valid `contract_id`.
- `tool_decision_coverage`: percent of tool calls with `tool_intent_classified` and `tool_policy_decision`.
- `path_role_coverage`: percent of read/write/shell/patch/MCP filesystem calls with path-role resolution where paths are relevant.
- `completion_decision_coverage`: percent of final answers with a `completion_gate_decision`.
- `replay_gap_rate`: percent of policy decisions audit cannot reconstruct from structured events.

### Enforcement Metrics

- `outside_scope_write_blocked`: count and rate of attempted writes outside required or allowed edit roles.
- `protected_path_attempts`: attempted reads/writes to protected paths, decision, and outcome.
- `planner_write_rejects`: per-edit write attempts during planner-only phases.
- `destructive_or_open_world_approval_coverage`: destructive/open-world external actions with approval, denial, or explicit pre-approval.
- `false_reject_rate`: cases where a valid tool call was rejected and later had to be manually worked around.
- `false_accept_rate`: cases where audit/eval identifies a tool action that should have been rejected.

### Verification Metrics

- `post_write_verification_rate`: mutable tasks with verification after the last successful write.
- `source_aware_verification_rate`: source edits with source-aware verification, not only diff inspection.
- `verification_skip_quality`: skipped verification with concrete blocker and user-visible final report.
- `verification_after_final_rejects`: final answers rejected because verification obligations were open.
- `test_scope_efficiency`: narrow relevant tests before full suite when applicable.

### Autonomy Efficiency Metrics

- `tool_calls_to_first_edit`: implementation tasks only.
- `raw_read_search_share_before_first_edit`: share of `read_file`, raw shell reads, and literal search before first edit.
- `semantic_discovery_first_rate`: tasks where semantic/context tools preceded raw broad search when index was fresh.
- `zero_hit_search_streaks`: zero-hit searches before redirect or strategy change.
- `same_tool_repeat_streaks`: repeated same tool and similar arguments.
- `broad_rediscovery_after_readiness`: broad discovery after readiness without a named missing claim.
- `completion_gate_reopened_discovery`: existing eval anti-pattern, now tied to contract ID.

### Memory And Context Metrics

- `context_pack_cache_hit_rate`: fresh cache hit rate by task string.
- `context_pack_stale_bypass_rate`: rate of stale bypasses and reasons.
- `packet_to_edit_precision`: percent of edits in paths surfaced by relevant context packets or explicitly added by evidence.
- `ranker_feedback_event_rate`: successful edits and audit outcomes feeding ranker learning.
- `stale_context_used_for_write`: write attempts based on stale packets without refresh.

### Current Baselines To Track

Use the YYYY-MM-DD audit output as initial baselines:

- Tool surface drift: 0 across 129 manifest/schema/suggestion tools.
- Zero-hit search finding present: 11 hits across 3 logs.
- Redirect storm finding present: 9 hits across 3 logs.
- Same-tool repeat finding present: 7 hits across 2 logs.
- Context cache stale ratio: about 91 percent.
- Ranker learning idle: no weights/log found.
- Top logged tools skew toward raw reads and shell: `read_file` 224 calls, shell 58, `search_content` 57.

Acceptance should include reducing the behavior findings in focused evals, not only adding event plumbing.

## Phased Implementation Plan

### Phase 0: Design Inventory And Invariant Map

Deliverables:

- This design doc.
- An invariant inventory mapping existing runner checks to stable invariant IDs.
- A contract input inventory mapping `WorkerRuntimeBrief`, runtime catalog, route plan, workspace config, tool rules, context packet, tool manifest, and eval fixtures to contract fields.
- A path-role vocabulary agreed across runner, workspace, audit, and eval.

Verification:

- Doc review against `docs/decisioning-north-star.md`.
- No behavior change.

### Phase 1: Contract Types And Passive Events

Deliverables:

- Add `AutonomyContract`, `TargetRole`, `Capability`, `ToolPolicy`, `Obligation`, and `PolicyDecision` types.
- Compile contracts passively before model turns using existing data.
- Emit `contract_compiled` and link existing `control_rails` to `contract_id`.
- Add passive `tool_intent_classified` and `tool_policy_decision` events with decision `observe_only` or equivalent.
- Add audit summary for contract coverage and replay gaps.

Verification:

- Unit tests for contract compilation from representative task families.
- Golden tests for event shape.
- Existing tests unchanged.
- `harness_audit` reports contract coverage without affecting behavior.

### Phase 2: Path Roles And Tool Policy Enforcement

Deliverables:

- Implement shared path-role resolver.
- Migrate required edit path scope rejection to contract invariant `write.required_path_scope`.
- Add protected path policy for `.git`, `.harness`, `.agent-config`, `.agents`, secrets, and project-configured protected paths. The exact list should be project-configurable and conservative by default.
- Normalize workspace write tools, shell commands, MCP filesystem tools, and apply_patch through path-role decisions.
- Migrate plan-then-execute write rejection and plan coverage checks to named invariants.

Verification:

- Tests for required edit path allow/deny, path normalization, symlink handling where supported, apply_patch path extraction, move/delete cases, and planner-only write rejection.
- Eval fixture where a model attempts to edit an evidence-only path and is rejected.
- Audit reports policy decisions for every write-capable call.

### Phase 3: Obligations, Verification, And Completion

Deliverables:

- Represent writes, required paths, snippets, verification, and final-answer requirements as obligations.
- Open verification obligations after successful writes.
- Resolve obligations from source-aware verification results.
- Migrate mutation completion gates to `completion_gate_decision`.
- Ensure final answer shaping reports skipped verification blockers from obligation state.

Verification:

- Tests for no-write final rejection, missing required path rejection, missing snippet rejection, unsupported changed path claim, pending verification rejection, and source-aware verification requirement.
- Focused eval for "edits then tries final without test" and "verification impossible with concrete blocker".

### Phase 4: Side Effects, External Tools, And Approval Policy

Deliverables:

- Add trusted/untrusted tool annotation ingestion for MCP/app/external tools.
- Classify destructive and open-world side effects.
- Add approval-required policy decisions for destructive/open-world actions unless the contract pre-approves them.
- Mark open-world/untrusted output in contract state so later decisions can account for trust-boundary mixing.
- Add non-interactive behavior: reject or skip with explicit blocker when approval cannot be surfaced.

Verification:

- Tests with trusted read-only MCP tool, untrusted unannotated tool, destructive tool, open-world tool, and external write attempt.
- Audit metric for destructive/open-world approval coverage.

### Phase 5: Audit, Eval, And Replay

Deliverables:

- Extend `harness_audit` to summarize contract coverage, top invariant violations, false accepts/reject candidates, unresolved obligations, replay gaps, stale context use, and autonomy efficiency.
- Extend eval expectations to assert `contract_compiled`, `tool_policy_decision`, `obligation_*`, and `completion_gate_decision` events.
- Add a replay utility or audit mode that reconstructs why a final answer was allowed or blocked.
- Add focused regression suites for current behavior findings: redirect storms, zero-hit search streaks, same-tool repeats, broad rediscovery after readiness, and incomplete implement lifecycle.

Verification:

- Focused eval suite passes.
- `harness_audit` can explain at least one full implementation session from contract compile to final answer without raw transcript reconstruction.

### Phase 6: Learning Loop And External Agent Contract Bridge

Deliverables:

- Feed successful contract decisions, violations, and edit outcomes into ranker learning.
- Include contract IDs and target roles in context packet feedback.
- Expose minimal `harness tool contract_*` commands for external agents to inspect active or historical contract decisions where useful.
- Define how delegated agents inherit or narrow contracts, including parent/child contract IDs and result summaries.

Verification:

- Ranker learning no longer idle in harness audit after representative runs.
- Delegated worker logs show contract inheritance or explicit narrowing.

## Risks

### Overconstraint

If the contract encodes strategy rather than boundaries, Agent Harness will become brittle. Mitigation: keep invariants narrow, use capabilities instead of exact tool calls, and require invariant IDs for hard rejections.

### Duplicate Authority

If prompts, runtime briefs, tool filters, workspace rules, and completion gates all keep independent versions of the same policy, drift will continue. Mitigation: control rails, tool policy, and completion gates must reference the same `contract_id`.

### False Sense Of Sandboxing

Agent Harness can reject tool calls it sees, but a local shell command may still execute with the user's OS permissions unless an OS sandbox, container, or process-level restriction exists. Mitigation: document this clearly, classify shell/network risk conservatively, and integrate external sandbox profiles rather than implying harness checks are a kernel boundary.

### Path Matching Bugs

Path policy is easy to get wrong: relative paths, symlinks, generated files, moves, deletes, glob semantics, patch headers, and shell commands all create edge cases. Mitigation: centralize path-role resolution and test it heavily.

### Annotation Trust

MCP and app annotations can be wrong or missing. Mitigation: trust only configured servers; default untrusted annotations to conservative side-effect classes.

### Local Model Overload

Too much contract detail in the prompt can hurt local models. Mitigation: compile a rich internal contract but present only compact rails, current obligations, and safe recovery text.

### Audit Volume

More events can create noisy logs. Mitigation: use structured summaries, stable reason codes, redaction profiles, and event sampling only for non-decision details. Never sample hard policy decisions.

### Stale Repository Memory

Context packets and graph caches can become stale. Mitigation: include freshness in target roles, bypass stale packets for write decisions, and emit stale-context events.

### Compatibility With External Agents

External agents and MCP tools may not understand Agent Harness contracts. Mitigation: enforce at the harness boundary and provide a small bridge surface rather than requiring every external tool to become contract-aware.

## Non-Goals

- Replace OS sandboxing, containers, or filesystem permissions.
- Build a general security policy engine for all applications.
- Clone external coding agent, external coding agent, external coding agent, or external coding agent UI features.
- Hardcode recurring task templates into prompts.
- Force the model's next tool call except where protocol correctness requires it.
- Guarantee correctness of untrusted MCP/app tool annotations.
- Rewrite all workspace tools before proving the contract model.
- Automatically commit, push, publish, email, or create public tickets.
- Treat audit metrics as user-facing product claims before eval coverage exists.
- Solve multi-agent scheduling beyond parent/child contract inheritance and delegated result contracts.

## Acceptance Criteria

The pillar is real when the following are true:

- Every tool-capable run emits `contract_compiled` before the model acts.
- Every `control_rails` event references a valid `contract_id` and is derived from that contract.
- Every write-capable tool call emits `tool_intent_classified`, `path_roles_resolved` where paths apply, and `tool_policy_decision`.
- Planner-only phases are represented as submit-plan-only contracts and enforce that invariant.
- Required edit paths, allowed edit paths, evidence-only paths, forbidden paths, and protected paths are enforced by the same path-role resolver.
- Successful writes open verification obligations, and verification results resolve or leave those obligations open with evidence.
- Final answers for mutable tasks are allowed only through `completion_gate_decision` with no unresolved required obligations, or with explicit waived/skipped obligations and user-visible blockers.
- Destructive and open-world external tools require approval or an explicit contract allowance; untrusted annotations do not bypass enforcement.
- `harness_audit` can summarize contract coverage, top invariant violations, replay gaps, unresolved obligations, and current autonomy efficiency metrics.
- Eval fixtures can assert contract lifecycle events and invariant behavior without hand-parsing transcripts.
- Focused regressions cover absence of known bad behaviors: planner writes, outside-scope writes, protected-path writes, source edits without verification, final answers with missing required paths, broad rediscovery after readiness, same-tool repeat loops, and incomplete implementation lifecycle.
- The model-facing instructions remain generic and evidence-based. No acceptance criterion depends on a prompt phrase being remembered by the model.

## First Implementation Slice

The first slice should be intentionally small:

1. Add contract types and passive `contract_compiled` events.
2. Link existing `control_rails` to the contract ID.
3. Map current task-family runtime catalog entries into contract fields.
4. Add stable invariant IDs to the existing planner write guard, required edit path scope guard, and mutation completion gate.
5. Extend `harness_audit` to report contract event coverage.

That slice creates a spine without changing behavior. Once the spine exists, enforcement can migrate one invariant at a time while audit proves that guidance, policy, and completion all point to the same contract.
