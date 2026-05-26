# Auditable Planning And Handoff

Status: design draft, 2026-05-12

This is one pillar in the coordinated roadmap:
[idiom-design-roadmap.md](idiom-design-roadmap.md). It turns contract,
repository-memory, capability, local-model, and verification evidence into a
durable handoff between planning and execution.

This document defines Idiom's auditable planning and handoff pillar. It turns
planning from transient model prose into a host-owned sequence of packets,
durable plan artifacts, deterministic audits, and executor handoff contracts.

The core policy remains the one in `docs/decisioning-north-star.md`:

- The model owns strategy.
- The harness owns contracts.

For this pillar, that means the model can choose a solution shape, decompose
work, name blockers, and revise the plan. The harness owns the planning
envelope, evidence freshness, target roles, approval scope, replayability,
executor preflight, telemetry, and acceptance gates.

## Summary

Idiom already has the pieces of this pillar:

- `context_pack` and graph-backed views localize task evidence and persist a
  `pack_id`.
- `architect packet --schema planning_v2` enriches planning packets with
  contracts, ranked scope, freshness, language capabilities, handoff, and
  context budget.
- `architect plan-audit`, `budget`, `packet-compare`, and `replay-compare` are
  deterministic packet/plan review surfaces.
- `plan-execute` separates a planner phase from a deterministic executor phase.
  The planner emits `submit_plan`; the executor loads a `PlanArtifact`, checks
  `required_edit_paths`, preflights edit anchors, and emits per-edit events.
- Durable plan approval scope and typed workflow state are already tracked in
  the autonomy roadmap and eval parsing.

The missing product shape is a single auditable lifecycle that connects those
pieces:

```text
task -> context packet -> durable plan -> plan audit -> approval scope
     -> executor handoff -> verified execution -> replay/eval feedback
```

The proposed design standardizes that lifecycle without turning the harness
into a strategy oracle. The harness can say "this plan cites stale evidence" or
"this handoff would edit a read-only evidence file." It should not say "call
tool X next" unless it is protecting a real invariant.

## Planning Problem

Repository-level work fails differently from single-file edits.

Large tasks need:

- repository context that fits a local model's budget
- dependency and may-impact evidence
- clear separation between editable targets and read-only evidence
- a plan that can survive context compaction and turn boundaries
- a handoff that preserves intent without widening scope
- deterministic checks before any executor mutates files
- replayable records for audit, evals, and ranker improvement

Without this structure, "plan first" is only a prompt style. It does not answer:

- Which repository facts did the planner rely on?
- Were those facts fresh?
- Which files were approved for edits?
- Which files were evidence only?
- Which validation commands were expected?
- Did execution match the plan or drift?
- Would a new packet or prompt have produced a better plan on the same task?

The planning problem is therefore a contract problem, not only a reasoning
problem. Idiom should make planning inspectable before execution, enforceable
during execution, and measurable after execution.

## External Patterns

The current ecosystem points in the same direction: effective coding agents are
increasingly shaped by host-owned workflow state, permissions, repository
memory, and audit surfaces.

- CodePlan's Microsoft Research publication page frames repository-level coding
  as a planning problem because
  interdependent repositories are too large for direct prompting. It synthesizes
  a multi-step chain of edits using repository context, previous code changes,
  dependency analysis, change may-impact analysis, and adaptive planning:
  https://www.microsoft.com/en-us/research/publication/codeplan-repository-level-coding-using-llms-and-planning/
- SWE-agent's 2024 arXiv preprint argues that the agent-computer interface
  materially changes agent performance for repository navigation, editing, and
  test execution:
  https://arxiv.org/abs/2405.15793
- RepoGraph's 2024 arXiv preprint reports that repository-level graph structure
  improves AI software engineering agents across SWE-bench and
  CrossCodeEval-style tasks:
  https://arxiv.org/abs/2410.14684
- Codebase-Memory's 2026 arXiv preprint reports a persistent Tree-sitter
  knowledge graph exposed over MCP, with large reductions in tokens and tool
  calls while preserving most answer quality:
  https://arxiv.org/abs/2603.27277
- The 2026 arXiv preprint on agentic-tool configuration reports that
  repository-level artifacts such as Markdown and JSON files are now a standard
  way developers configure coding agents, with `AGENTS.md` emerging as an
  interoperable starting point:
  https://arxiv.org/abs/2602.14690

The product tools show similar workflow patterns:

- Claude Code plan mode is a read-first mode: Claude researches and proposes
  changes, but does not edit source until a plan is approved. The approval can
  transition to auto, accept-edits, or manual review modes:
  https://code.claude.com/docs/en/permission-modes
- Claude Code subagents separate exploration, planning, and general-purpose
  work. Its built-in Plan subagent is read-only and cannot spawn nested
  subagents, which keeps planning research bounded:
	  https://code.claude.com/docs/en/sub-agents
- Claude Code hooks expose deterministic lifecycle control. `PreToolUse` can
  block tool calls, `Stop` and `SubagentStop` hooks can prevent stopping with a
  required reason, and agent hooks can inspect real files or test output before
  returning a structured decision:
  https://code.claude.com/docs/en/hooks
- OpenCode has primary Build and Plan agents plus subagents. Its permissions are
  explicit `ask`, `allow`, or `deny` controls over read, edit, bash, task,
  web, LSP, and other tool classes, with per-agent overrides:
  https://opencode.ai/docs/agents/
- Codex exposes `/plan`, `/review`, `/status`, `/permissions`, subagent thread
  switching, MCP inspection, and context compaction as first-class CLI
  workflows:
  https://developers.openai.com/codex/cli/slash-commands
- Codex sandboxing and approvals are configured with durable settings such as
  `sandbox_mode`, `approval_policy`, writable roots, permission profiles, and
  command rules:
  https://developers.openai.com/codex/concepts/sandboxing
- Codex customization recommends moving from repository instructions to
  plugins, MCP, and subagents as workflows become reusable:
  https://developers.openai.com/codex/concepts/customization
- OpenAI's Agents SDK documentation treats agent definitions, runtime loop and
  state, sandboxing, handoffs, guardrails/human review, results/state, tools,
  observability, and evals as separate design surfaces:
  https://developers.openai.com/api/docs/guides/agents

Idiom's differentiator should not be copying any one UI. The differential is
that the local harness can produce and enforce a task-specific planning
envelope, then replay it from local logs.

## Current Idiom Lanes And Packets

Verified current state from the requested docs and code:

- `implement` is a write-capable lane where the model interleaves reads and
  edits. It is appropriate for narrow work where one trajectory is enough.
- `plan-execute` is a two-phase lane. The planner runs with per-edit write
  tools blocked and `submit_plan` exposed. The executor then dispatches the
  submitted edits deterministically.
- `atom-spec` is a drafting lane that produces a brief artifact without
  touching the workspace.
- `idiom lane implement` and `idiom lane plan-execute` preload a compact ranked
  `context_pack` by default, unless the caller passes `--no-context-pack`.
- `context_pack` writes a cached graph view under
  `.idiom/comprehension/graph.sqlite` and a human-readable mirror under
  `.idiom/comprehension/views/<pack_id>.json`.
- The `pack_id` can round-trip through `submit_plan` into
  `workspace.PlanArtifact.PackID`. The executor can call
  `comprehension.LoadViewByID(root, pack_id)` and emits
  `executor_inherits_pack` or `executor_pack_miss`.
- `architect packet --schema planning_v2` adds typed planning sections on top
  of the v1 packet: `contracts`, `repository_view`, `candidate_scope`,
  `freshness`, `language_capabilities`, `handoff`, and `context_budget`.
- `architect plan-audit` deterministically checks a model plan for missing file
  references, unresolved handles, validation gaps, stale context use, protected
  path edits, and vague "inspect more" deferrals.
- `architect budget` estimates packet budget tiers and placement probes.
- `architect packet-compare` and `architect replay-compare` compare packet and
  model-plan variants.
- `submit_plan` currently persists `idiom.plan.v1` with `schema_version`,
  `created`, `rationale`, optional `pack_id`, optional `session_id`, and
  ordered `edits`.
- The executor preflights `required_edit_paths` and substring anchors before
  any edit lands, then emits `idiom.plan_execute.*` events for artifact load,
  preflight, executor start, each edit, and completion.
- The autonomy tracker marks durable plan approval scope, plan exit semantics,
  build-switch reminders, typed workflow state, target-role packets, and
  workflow-state eval parsing as implemented capabilities or tracked
  requirements.

This design builds on those facts. It does not require a new model loop before
it becomes useful.

## Design Principles

1. Plans are artifacts, not just answers.
2. Packets are evidence envelopes, not strategy scripts.
3. Approval scope is host-owned and machine-readable.
4. Handoff contracts must separate editable targets, read-only evidence,
   analogy paths, allowed new destinations, and forbidden paths.
5. Deterministic audit should reject invariant violations and warn on weak
   planning evidence. It should not rank the "best" strategy as if that were a
   fact.
6. Replay should compare packet and plan variants on the same task before
   execution when possible, and compare plan-to-execution drift after execution.
7. Telemetry must connect packet IDs, plan IDs, approval IDs, executor IDs,
   verification results, and final answers.
8. Local-model constraints are first-class: prefer compact structured evidence,
   stable handles, and cheap deterministic gates over repeated prompt bloat.

## Proposed Packet Lifecycle

### 1. Task Intake

Input can arrive from a user prompt, a brief file, a workflow resume, an eval
scenario, or an external agent calling `idiom tool`.

The harness assigns:

- `task_id`
- `session_id`
- workspace root and cwd
- current git head and dirty fingerprint
- requested lane or mode
- explicit user constraints
- initial target roles, if any
- verification expectations, if any

The intake layer should not infer a complete edit plan. It normalizes the task
and records user boundaries.

### 2. Evidence Collection

The harness runs the narrowest useful discovery path:

1. `context_status` when freshness is unknown or stale context is suspected.
2. `context_pack` with the same task string across related calls.
3. `context_impact` or `discovery_query` when blast radius or history matters.
4. `context_trace_pack` when a failing test, stack trace, or runtime log is
   decision-critical.
5. Language-aware zoom/query tools for exact symbol or test questions.

The output is a set of evidence handles. Evidence reports findings,
uncertainty, paths, symbols, tests, counts, and freshness. It does not prescribe
the next tool call.

### 3. Planning Packet

The harness builds a planning packet, currently through:

```bash
idiom architect packet \
  --schema planning_v2 \
  --task-file <task.md> \
  --context-view <context-view.json> \
  --output <packet.json>
```

The packet should carry:

- task text
- profile rules and project constraints
- evidence handles and summaries
- candidate scope ranked by evidence signals
- freshness and language capability reports
- validation requirements
- target roles and protected paths
- context budget and placement risk
- handoff readiness fields

The packet is host-authored. The model may critique it, but it may not silently
rewrite the contract fields.

### 4. Plan Draft

The model drafts a plan from the packet using a read-only surface:

```bash
idiom architect plan --packet <packet.json> --output <plan.prompt.md>
idiom artifact --prompt-file <plan.prompt.md> --output <plan.md> --no-tools
```

Later CLI sugar can hide those two steps, but the artifact boundary should stay
visible. The model-authored plan may contain prose, but the harness should also
parse or request a durable JSON plan shape.

### 5. Durable Plan Parse

The durable plan is a machine-readable artifact derived from the draft. It
contains model-owned intent fields and host-owned contract fields.

Host-owned fields include:

- source packet IDs and fingerprints
- approval scope
- audit result
- accepted handoff target
- replay metadata

Model-owned fields include:

- objective
- assumptions
- open questions
- phases or atoms
- claimed evidence references
- proposed verification
- blockers

If the model fails to emit a valid durable shape, the harness can either ask for
a schema repair or store the prose plan with an `unparsed` status. It should
not pretend an unparsed plan is executable.

### 6. Deterministic Plan Audit

`architect plan-audit` runs before approval. Audit errors block handoff.
Warnings remain reviewable but do not always block.

Minimum audit categories:

- schema validity
- packet handle resolution
- cited path existence or explicit new-file status
- protected path and forbidden path edits
- read-only evidence accidentally proposed as editable
- stale context reliance without refresh or labeling
- validation command coverage
- vague research deferrals without exact missing claims
- context budget risk
- unsupported claims not tied to packet evidence
- target-role completeness for every atom

### 7. Approval Scope

Approval does not mean "do whatever the plan says." It produces a separate
host-owned scope:

- approved plan atoms
- editable targets
- allowed new destinations
- read-only evidence paths
- analogy paths
- forbidden paths
- allowed tool classes
- command policy
- verification expectations
- expiration/fingerprint policy

Approval can be full, partial, or denied. Partial approval creates a narrower
handoff with only selected atoms.

### 8. Executor Handoff

The handoff projects the approved durable plan into the lane-specific contract:

- `plan-execute`: an executor handoff allows a planner to emit a structured
  `submit_plan` whose edits must remain inside `required_edit_paths` and the
  approved new-destination list.
- `implement`: the runtime brief and implementation target packet expose
  editable primary paths, read-only evidence, forbidden paths, allowed new
  destinations, and verification intent.
- external sidecar: `idiom tool` returns the packet, plan, audit, and approval
  scope for another agent to consume, while Idiom remains the verifier/auditor.

The executor must re-check the handoff immediately before writing. If the
workspace fingerprint, target file fingerprint, or packet freshness has expired,
the executor stops with a replan-required status.

### 9. Execution And Verification

The executor runs only inside the approved scope. It emits:

- preflight result
- inherited packet/cache result
- per-edit status
- actual changed paths
- validation commands attempted
- verification status
- blockers

Execution can finish as:

- `completed_verified`
- `completed_planner_owned_verification_pending`
- `blocked_scope`
- `blocked_stale_plan`
- `blocked_anchor_mismatch`
- `blocked_verification_unavailable`
- `failed_executor`

### 10. Closeout And Feedback

The closeout links:

- packet ID
- durable plan ID
- approval scope ID
- handoff ID
- executor session ID
- verification result
- final answer
- replay/eval outcome
- `harness_feedback` category, when applicable

This gives `harness_audit` enough structure to identify planning failures
without reconstructing them from raw prose.

## Durable Plan Schema

The durable plan schema should be explicit about field ownership. Proposed
schema version: `idiom.durable_plan.v1`.

```json
{
  "schema_version": "idiom.durable_plan.v1",
  "plan_id": "plan_20260512_235959_abcd1234",
  "task_id": "task_abcd1234",
  "created_at": "2026-05-12T23:59:59Z",
  "source": {
    "packet_id": "packet_...",
    "packet_schema": "idiom.architect.planning_packet.v2",
    "pack_id": "811c2a5c2d387cb1",
    "context_index_id": "eb4f08f4874da419",
    "git_head": "abc123",
    "dirty_fingerprint": "sha256:...",
    "prompt_hash": "sha256:...",
    "model": "qwen3-coder-30b"
  },
  "objective": "Implement the approved refactor without changing prompt semantics.",
  "constraints": [
    "model owns strategy; harness owns contracts",
    "do not edit read-only evidence paths"
  ],
  "assumptions": [
    {
      "id": "assumption-1",
      "text": "The existing plan-execute executor remains the execution path.",
      "evidence": ["file://cmd/idiom/plan_execute.go"]
    }
  ],
  "open_questions": [
    {
      "id": "question-1",
      "claim": "Which package owns the new audit parser?",
      "probe": "context_query for callers/tests of architect plan-audit",
      "required_before_handoff": false
    }
  ],
  "evidence": [
    {
      "handle": "context_pack://811c2a5c2d387cb1",
      "role": "planning_context",
      "fresh": true,
      "confidence": "exact_tool_output"
    },
    {
      "handle": "file://internal/architect/plan_audit.go",
      "role": "read_only_evidence",
      "fresh": true,
      "confidence": "syntax"
    }
  ],
  "atoms": [
    {
      "id": "atom-1",
      "title": "Add durable plan schema parser",
      "intent": "Persist model-authored plan atoms and host-owned approval scope separately.",
      "editable_targets": ["internal/architect/durable_plan.go"],
      "allowed_new_destinations": ["internal/architect/durable_plan.go"],
      "read_only_evidence": [
        "internal/architect/packet.go",
        "internal/workspace/submit_plan.go"
      ],
      "forbidden_paths": [".git", ".idiom/comprehension/graph.sqlite"],
      "dependencies": [],
      "expected_change": "Add schema structs and validation helpers.",
      "anchors": [
        {
          "path": "internal/architect/packet.go",
          "handle": "symbol://internal/architect/packet.go#Packet"
        }
      ],
      "risk_level": "medium",
      "verification": ["go test ./internal/architect"],
      "completion_evidence": [
        "schema validates",
        "tests pass",
        "no read-only evidence file edited"
      ]
    }
  ],
  "verification": {
    "commands": ["go test ./internal/architect", "go test ./internal/rootcmd ./internal/lanecmd ./internal/planexecutecmd"],
    "source_sanity_paths": ["internal/architect/durable_plan.go"],
    "expected_failures": [],
    "blocked_if_unavailable": true,
    "verification_owner": "executor"
  },
  "approval_scope": {
    "status": "awaiting_approval",
    "editable_targets": ["internal/architect/durable_plan.go"],
    "read_only_evidence": [
      "internal/architect/packet.go",
      "internal/workspace/submit_plan.go"
    ],
    "allowed_new_destinations": ["internal/architect/durable_plan.go"],
    "analogy_paths": [],
    "forbidden_paths": [".git", ".idiom/comprehension/graph.sqlite"],
    "allowed_tool_classes": ["read", "semantic_read", "edit", "verification"],
    "command_policy": {
      "allow": ["go test ./internal/architect", "go test ./internal/rootcmd ./internal/lanecmd ./internal/planexecutecmd"],
      "ask": [],
      "deny": ["git push", "git reset --hard"]
    },
    "expires_on_fingerprint_change": true
  },
  "plan_exit": {
    "state": "awaiting_approval",
    "handoff_ready": false,
    "build_switch_reminder": "Restate the objective, approved targets, and validation before writing.",
    "question_required": false,
    "blocker": ""
  },
  "audit": {
    "status": "not_run",
    "tool": "architect plan-audit",
    "findings": []
  },
  "handoff": {
    "target_lane": "plan-execute",
    "ready": false,
    "required_edit_paths": ["internal/architect/durable_plan.go"],
    "pack_id": "811c2a5c2d387cb1"
  }
}
```

Ownership rules:

- The planner may author `objective`, `assumptions`, `open_questions`,
  `atoms`, and proposed `verification`.
- The harness stamps `source`, `approval_scope`, `audit`, and `handoff`.
- The user or calling orchestrator changes `approval_scope.status`.
- The executor reads `approval_scope` and `handoff`; it does not trust prose.

## Plan Audit

Plan audit is deterministic and should be callable before any model is asked to
execute:

```bash
idiom architect plan-audit \
  --packet packet.json \
  --plan-file plan.md \
  --output audit.json
```

Near-term audit should extend the existing checks with durable-plan awareness:

| Category | Severity | Check |
| --- | --- | --- |
| `schema_invalid` | error | JSON durable plan cannot be parsed or has an unsupported schema. |
| `unknown_packet_handle` | warning | Plan cites a handle not present in packet evidence or candidate scope. |
| `missing_file` | error | Existing path reference does not exist and is not marked as new. |
| `protected_path` | error | Plan proposes editing a protected or forbidden path. |
| `role_widening` | error | Path listed as read-only evidence appears under editable targets. |
| `stale_fact` | warning or error | Plan relies on stale context without refresh or explicit uncertainty. |
| `validation_gap` | warning | Packet validation requirements are missing from plan verification. |
| `unsupported_claim` | warning | Plan asserts a code fact without evidence handle, path, symbol, or command. |
| `missing_exact_gap` | warning | Plan says to inspect more without naming a claim and probe. |
| `handoff_not_ready` | error | Required approval-scope fields are absent for execution. |
| `budget_risk` | warning | Packet or plan exceeds configured local-model budget tier. |

Audit output should remain a fact report:

```json
{
  "ok": false,
  "findings": [
    {
      "severity": "error",
      "kind": "role_widening",
      "message": "read-only evidence path internal/workspace/submit_plan.go is proposed as editable",
      "evidence": ["internal/workspace/submit_plan.go"],
      "suggested_resolution": "Remove it from editable targets or request explicit approval."
    }
  ]
}
```

The audit may suggest a resolution, but it must not choose an implementation
strategy beyond enforcing the violated contract.

## Replay Compare

Replay compare answers two different questions.

Pre-execution replay:

- Given the same task and model, does packet V2 produce a more auditable plan
  than packet V1?
- Did a context budget or ranked-scope change reduce missing evidence, stale
  facts, validation gaps, or unsupported claims?

Post-execution replay:

- Did execution mutate only approved paths?
- Did actual changed paths match planned atoms?
- Did verification match the plan's verification section?
- Would the old packet have failed earlier or produced a broader plan?

Current `architect replay-compare` already compares two packets and two plan
texts through deterministic plan audit. Extend it with durable plan and
execution fields:

```json
{
  "task_id": "task_abcd1234",
  "base": {
    "packet_id": "packet_v1",
    "plan_id": "plan_base",
    "audit_errors": 2,
    "audit_warnings": 5
  },
  "candidate": {
    "packet_id": "packet_v2",
    "plan_id": "plan_candidate",
    "audit_errors": 0,
    "audit_warnings": 1
  },
  "execution_compare": {
    "planned_paths": ["internal/architect/durable_plan.go"],
    "changed_paths": ["internal/architect/durable_plan.go"],
    "scope_drift": [],
    "verification": "passed"
  },
  "preferred_variant": "candidate",
  "caveats": []
}
```

Replay input stability matters. Store:

- task text hash
- packet JSON hash
- prompt renderer version
- model identifier
- inference settings when available
- context index ID
- git head and dirty fingerprint
- local tool versions relevant to deterministic audit

Replay compare should be allowed to say "not comparable" when these differ.

## Executor Handoff

The executor handoff is the bridge from approved plan to write-capable lane.

Current `PlanArtifact` is edit-list oriented:

```json
{
  "schema_version": "idiom.plan.v1",
  "created": "...",
  "rationale": "...",
  "pack_id": "...",
  "session_id": "...",
  "edits": []
}
```

Keep that shape for backwards compatibility, but add an adjacent handoff
artifact rather than overloading every `PlanEdit` immediately.

Proposed schema version: `idiom.executor_handoff.v1`.

```json
{
  "schema_version": "idiom.executor_handoff.v1",
  "handoff_id": "handoff_...",
  "plan_id": "plan_...",
  "approval_scope_id": "approval_...",
  "packet_id": "packet_...",
  "pack_id": "811c2a5c2d387cb1",
  "target_lane": "plan-execute",
  "required_edit_paths": ["internal/architect/durable_plan.go"],
  "allowed_new_destinations": ["internal/architect/durable_plan.go"],
  "read_only_evidence": ["internal/workspace/submit_plan.go"],
  "forbidden_paths": [".git", ".idiom/comprehension/graph.sqlite"],
  "validation_commands": ["go test ./internal/architect"],
  "verification_owner": "executor",
  "atoms": [
    {
      "atom_id": "atom-1",
      "editable_targets": ["internal/architect/durable_plan.go"],
      "verification": ["go test ./internal/architect"]
    }
  ],
  "fingerprints": {
    "git_head": "abc123",
    "dirty_fingerprint": "sha256:...",
    "target_files": {
      "internal/architect/packet.go": "sha256:..."
    }
  }
}
```

Executor invariants:

- Every write path must be in `required_edit_paths` or
  `allowed_new_destinations`.
- Read-only evidence and analogy paths remain readable but not editable.
- Forbidden paths are not readable or writable unless a more specific policy
  explicitly allows the read.
- If a fingerprint changed and the handoff requires freshness, stop with
  `blocked_stale_plan`.
- If `pack_id` exists, attempt to rehydrate it and emit the inherited/miss
  event.
- If old-text anchors fail, abort before any edit.
- If verification is executor-owned, do not claim completion without a
  verification result or a concrete blocker.
- If verification is planner-owned, completion must report lane sanity and the
  delegated verification status honestly.

The handoff does not have to execute every plan shape. It can choose the
simplest safe lane:

| Plan shape | Handoff lane |
| --- | --- |
| ordered exact edits | `plan-execute` |
| one to three tightly coupled source edits | `implement` with target-role packet |
| create full new file | `implement` or `plan-execute` whole-file write |
| pure docs/prose artifact | `artifact` or scoped `implement` |
| unresolved required question | no execution, return `<<QUESTION>>` |

## UX And CLI

Use existing commands first, then add convenience wrappers after the artifact
shape settles.

Current explicit flow:

```bash
idiom context pack --task-file task.md --mode plan --output context-view.json

idiom architect packet \
  --task-file task.md \
  --schema planning_v2 \
  --context-view context-view.json \
  --output packet.json

idiom architect lint --packet packet.json
idiom architect budget --packet packet.json
idiom architect plan --packet packet.json --output plan.prompt.md

idiom artifact \
  --prompt-file plan.prompt.md \
  --output plan.md \
  --no-tools

idiom architect plan-audit \
  --packet packet.json \
  --plan-file plan.md \
  --output audit.json
```

Proposed durable wrappers:

```bash
idiom plan draft \
  --task-file task.md \
  --packet-out packet.json \
  --plan-out .idiom/plans/<plan_id>.plan.json \
  --markdown-out .planning/<plan_id>.md

idiom plan audit \
  --plan .idiom/plans/<plan_id>.plan.json \
  --output .idiom/plans/<plan_id>.audit.json

idiom plan approve \
  --plan .idiom/plans/<plan_id>.plan.json \
  --atom atom-1 \
  --output .idiom/plans/<plan_id>.approval.json

idiom plan handoff \
  --approval .idiom/plans/<plan_id>.approval.json \
  --lane plan-execute \
  --output .idiom/plans/<plan_id>.handoff.json

idiom lane plan-execute \
  --handoff .idiom/plans/<plan_id>.handoff.json \
  --json
```

Interactive UX:

- `/plan` opens or drafts the current session plan.
- The Plan panel shows packet ID, plan ID, audit status, approval status,
  editable targets, read-only evidence, validation commands, and replay status.
- Approval prompts show only the scope delta: editable targets, new
  destinations, commands, and forbidden/read-only evidence.
- Plan acceptance can optionally clear planning context before implementation,
  but the durable plan and handoff remain pinned.
- If execution is blocked by stale fingerprints, the UI should offer "refresh
  packet and re-audit" rather than continuing with stale scope.

External-agent UX:

- `idiom tool context_pack` remains the first sidecar entry point.
- `idiom tool harness_audit` reports planning-quality feedback clusters.
- Add `idiom tool plan_audit` or expose `architect plan-audit` through the tool
  surface once the durable schema is stable.
- Return machine-readable paths and IDs so Codex, Claude Code, OpenCode, or
  Aider can use Idiom as a local planning/audit sidecar.

## Telemetry

The telemetry model should link every artifact in one traceable chain.

Conversation log events:

- `planning_packet_created`
- `planning_packet_linted`
- `planning_budget_reported`
- `durable_plan_drafted`
- `durable_plan_parse_failed`
- `plan_audit_completed`
- `plan_approval_scope_created`
- `plan_approval_scope_denied`
- `executor_handoff_created`
- `executor_handoff_rejected`
- `executor_preflight_ok`
- `executor_preflight_failed`
- `executor_inherits_pack`
- `executor_pack_miss`
- `executor_edit`
- `executor_complete`
- `verification_result`
- `plan_closeout`

Minimum event fields:

- `task_id`
- `session_id`
- `packet_id`
- `pack_id`
- `plan_id`
- `approval_scope_id`
- `handoff_id`
- `schema_version`
- `git_head`
- `dirty_fingerprint`
- `fresh`
- `candidate_files`
- `candidate_symbols`
- `validation_commands`
- `editable_targets`
- `read_only_evidence`
- `forbidden_paths`
- `audit_errors`
- `audit_warnings`
- `changed_paths`
- `verification_status`
- `stop_reason`
- `duration_ms`
- `prompt_tokens`, `completion_tokens`, and `cached_tokens` when available

Telemetry stream additions:

- process-level records for `plan draft`, `plan audit`, `plan approve`, and
  `plan handoff`
- counters for packet cache hit/miss/stale prevention
- replay comparison summaries
- artifact retention cleanup counts

Privacy and size controls:

- Store hashes for large prompts and raw model outputs.
- Store full durable plans locally by default.
- Redact command output in telemetry; keep full excerpts in local artifacts when
  the user has enabled full debug logs.
- Treat paths under protected config and secrets as redaction candidates even in
  read-only evidence.

## Eval Metrics

Planning evals should measure whether the harness reduces bad handoffs, not
whether the model uses a particular planning style.

Localization metrics:

- top-1, top-3, top-5 `context_pack` hit rate against actual changed paths
- required-test suggestion hit rate
- false-positive hub inflation
- stale-context prevention count
- broad raw reads before first approved edit

Plan quality metrics:

- plan audit pass rate
- audit findings by kind
- unsupported claim rate
- stale fact reliance rate
- validation gap rate
- exact-gap quality for open questions
- plan atom count versus actual edit count
- user approval iterations before handoff

Handoff metrics:

- approval scope completeness
- read-only evidence widened into editable scope, target zero
- forbidden/protected path attempts, target zero successful writes
- stale fingerprint block rate
- old-text preflight failure rate
- `executor_inherits_pack` hit rate
- plan-to-execution path overlap
- planned atom to changed path coverage

Verification metrics:

- verification attempted after writes
- verification passed, failed, or blocked
- source-sanity coverage for source edits
- patch-hygiene-only false completion, target zero
- planner-owned verification reported honestly

Replay metrics:

- packet variant audit delta
- plan stability across identical packet/model inputs
- candidate packet preferred rate
- replay caveat rate
- post-execution drift rate

Local-model operating metrics:

- prompt tokens per planning stage
- model calls per approved handoff
- tool calls before durable plan
- compaction count and pinned-plan retention
- time to first approved edit
- budget-risk warnings by packet tier

`idiom-eval` and SWE-style scenarios should be able to assert:

- workflow phase
- editable targets
- read-only evidence
- forbidden paths
- verification commands
- audit finding kinds
- handoff status
- changed path coverage
- verification outcome

## Rollout Plan

### Phase 1: Document And Align Existing Surfaces

- Land this design doc.
- Update current planning docs to point at the lifecycle terminology.
- Keep existing commands unchanged.
- Add examples that use `context_pack`, `architect packet --schema
  planning_v2`, `architect plan-audit`, and `replay-compare`.

Exit criteria:

- The design language matches `lane-architecture.md`,
  `architect-packets.md`, and `agent-autonomy-tracker.md`.
- No command behavior changes are required.

### Phase 2: Durable Plan Artifact

- Add `idiom.durable_plan.v1` structs and JSON validation.
- Persist durable plans under `.idiom/plans/`.
- Keep a markdown mirror under `.planning/` for the Plan panel.
- Parse model-authored plan JSON when present; otherwise store unparsed prose
  with `status: unparsed`.
- Stamp packet, pack, git, prompt, model, and freshness fingerprints.

Exit criteria:

- Plan-only requests produce a durable artifact and no writes.
- Invalid durable plans fail with deterministic schema errors.

### Phase 3: Approval Scope And Plan Exit

- Add `approval_scope` as a host-owned section.
- Support full and partial approval.
- Persist plan-exit state: `awaiting_approval`, `question`, `blocked`,
  `approved`, `expired`, `executed`.
- Emit build-switch reminders at handoff.
- Extend workflow-state eval assertions to durable plan IDs and approval IDs.

Exit criteria:

- Build-mode execution can resume approved scope without prose guessing.
- Read-only evidence is never widened into editable targets.

### Phase 4: Executor Handoff

- Add `idiom.executor_handoff.v1`.
- Teach `plan-execute` to accept a handoff file.
- Add optional `atom_id` and `approval_scope_id` to `PlanEdit` or executor
  telemetry without breaking `idiom.plan.v1`.
- Enforce allowed new destinations and read-only evidence roles in executor
  preflight.
- Keep `pack_id` rehydration visible.

Exit criteria:

- Executor rejects stale, out-of-scope, and read-only evidence writes before any
  file mutation.
- Per-edit events include plan and atom IDs when available.

### Phase 5: Replay And Audit Loop

- Extend `replay-compare` to durable plans and execution outcomes.
- Add `harness_audit` planning-quality summaries for durable plans.
- Route `harness_feedback` categories such as `plan_audit_gap`,
  `packet_quality`, `budget_gap`, and `replay_eval_gap` into review packets.
- Add a log-to-regression helper for failed handoffs.

Exit criteria:

- A failed handoff can become a replay/eval scenario without hand-reconstructing
  the plan from prose.

### Phase 6: CLI And Sidecar Polish

- Add top-level `idiom plan` wrappers once schemas are stable.
- Expose plan audit through `idiom tool`.
- Document sidecar workflows for Codex, Claude Code, OpenCode, and Aider.
- Add retention and cleanup controls for plan artifacts.

Exit criteria:

- External agents can call Idiom for packet, audit, handoff, and replay without
  starting an Idiom model session.

## Risks

- False confidence: a passing plan audit does not prove the plan is correct.
  Mitigation: call it "audit passed", not "plan correct", and require
  verification after writes.
- Over-constraining the model: too many hard gates can turn planning into a
  retry loop. Mitigation: errors only for invariants; warnings for weak
  evidence.
- Packet bloat: planning packets can become too large for local models.
  Mitigation: context budget tiers, placement probes, stable handles, and
  zoomable follow-up evidence.
- Stale cache reliance: cached views can become misleading. Mitigation:
  freshness gates, fingerprints, and replan-required executor blocks.
- Schema churn: durable plan fields may change while implementation is still
  exploratory. Mitigation: additive versioning and unparsed/prose fallback.
- Approval ambiguity: users may approve "the plan" without noticing scope.
  Mitigation: approval prompts show the machine scope delta, not the whole
  prose plan.
- Privacy and log volume: durable artifacts may contain sensitive paths or
  command output. Mitigation: local storage by default, redaction, and hashes
  for large raw content.
- External-agent mismatch: another agent may treat Idiom's packet as advice
  rather than a contract. Mitigation: expose explicit `ok`, `errors`, and
  `allowed_scope` fields through `idiom tool`.

## Non-Goals

- Do not expose or require hidden chain-of-thought.
- Do not make plan audit a strategy judge.
- Do not replace repository tests, builds, diagnostics, or source sanity.
- Do not build a general project-management system.
- Do not require cloud models.
- Do not force every task through plan-execute. Narrow single-file work should
  still use `implement` when appropriate.
- Do not encode project-specific recipes into reusable prompts.
- Do not make plan approval equivalent to bypassing protected paths, command
  policy, or verification gates.

## Acceptance Criteria

The auditable planning and handoff pillar is successful when:

- A non-trivial task can be traced from task ID to packet ID to plan ID to
  approval scope ID to handoff ID to executor events to verification result.
- Planning packets expose ranked scope, freshness, language capability,
  validation, handoff, and context budget data.
- Durable plans separate model-authored strategy from host-owned contracts.
- Plan audit blocks protected-path edits, role widening, missing execution
  scope, and stale required evidence before execution.
- Approval scope is machine-readable and supports partial approval.
- Executor handoff preserves editable targets, read-only evidence, allowed new
  destinations, forbidden paths, and verification expectations without prose
  parsing.
- `plan-execute` rejects out-of-scope edits and anchor mismatches before any
  mutation.
- `pack_id` inheritance is visible in executor telemetry.
- Final answers report changed paths and verification honestly.
- Replay compare can show whether a packet or plan change reduced audit
  findings on the same task.
- `idiom-eval` can assert workflow phase, approved targets, evidence roles,
  audit findings, handoff status, and verification outcome.
- `harness_audit` can summarize planning-quality failures without manual JSONL
  reconstruction.
- External agents can use Idiom as a context/audit/handoff sidecar through
  stable CLI or `idiom tool` outputs.

## Open Questions

- Should durable plan artifacts live only under `.idiom/plans/`, or should the
  markdown mirror under `.planning/` become the user-facing source of truth?
- Should `plan-execute` accept `--handoff` directly, or should `idiom lane
  plan-execute` own handoff resolution and call the lower-level subcommand?
- Should `PlanArtifactSchemaVersion` move to `idiom.plan.v2` when adding
  `atom_id`, or should those IDs remain only in executor telemetry and the
  adjacent handoff artifact?
- What retention policy should apply to plan artifacts that include raw
  evidence excerpts?
- How much of plan audit should run automatically inside `idiom lane
  plan-execute` versus remaining an explicit preflight command?
