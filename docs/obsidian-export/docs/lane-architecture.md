# Lane Architecture

This document describes the implementation lane shape harness provides
and how project repos overlay it. It is the canonical reference for
"what does harness give me, and what do I bring."

## What is a lane?

A **lane** is one round-trip from a brief to a set of completed
edits, optionally with a verification pass. Agent Harness provides
three lane shapes:

- **`implement`** — write-capable lane where the model interleaves
  reads and edits. Use this when the brief is small (1-3 files,
  tightly coupled) and the planner can hold the work in one
  trajectory. Driven by `harness implement`.
- **`plan-execute`** — two-phase lane: a planner phase emits a
  single structured `submit_plan` tool call enumerating every edit;
  an executor phase dispatches each edit deterministically. Use this
  when the brief is multi-file (3+ files) or when atomic
  preflight/per-edit telemetry matters more than per-edit
  decision-making. Driven by `harness plan-execute`.
  The planner phase has a dedicated plan-submission tool profile:
  bounded read/discovery tools plus `submit_plan`. It does not
  advertise shell, git, task/task_ledger, source_sanity, or per-edit
  write tools; those are executor/orchestrator concerns.
- **`atom-spec`** — drafting lane: model produces a brief artifact
  (a planner target packet) without touching the workspace, then
  exits. Used when the orchestrator needs a clean spec to feed into
  a downstream `implement` or `plan-execute` lane. Driven by
  `harness atom-spec draft` or the equivalent artifact-generation
  command in the CLI package.

Every lane consumes a **brief** (markdown describing the work) and
produces some combination of: changed files, a plan artifact, a
final answer, structured telemetry events.

## What harness provides

```
┌─ Brief (project) ────────────────────────────────────────┐
│   .planning/<atom>.md                                    │
│   - Files To Modify table → required_edit_paths          │
│   - Verified Current State + Edit Anchors                │
│   - Required Behavior + Implementation Shape             │
│   - HARD PROHIBITIONS                                    │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌─ harness CLI surface ──────────────────────────────────────┐
│                                                          │
│   harness plan-execute / implement / artifact              │
│         │                                                │
│         ▼                                                │
│   prompt routing → workerruntime catalog                 │
│         │                                                │
│         ▼                                                │
│   runtime brief (task_family, contract, allowed_tools,   │
│                  required_edit_paths, …)                 │
│         │                                                │
│         ▼                                                │
│   runner loop — model calls + tool dispatch              │
│         │                                                │
│         ├── pre-dispatch guards                          │
│         │     • verification intercept                   │
│         │     • plan-then-execute write guard            │
│         │     • apply_patch preflight                    │
│         │     • permission policy                        │
│         │                                                │
│         ├── workspace.Manager dispatch                   │
│         │     • read-class tools                         │
│         │     • write tools (edit_file, apply_patch,     │
│         │       delegate_edit, submit_plan, …)           │
│         │     • language-family tools (cpp_*, go_*, …)   │
│         │     • external tool registry (.harness/*)        │
│         │     • MCP tools                                │
│         │                                                │
│         ├── post-result hooks                            │
│         │     • short-circuit gates                      │
│         │     • required_edit_paths progress             │
│         │     • plan_then_execute readiness nudge        │
│         │     • doom-loop guard                          │
│         │                                                │
│         └── completion gates                             │
│               • mutation completion gate                 │
│               • final-answer blocker check               │
│               • final-answer decorator                   │
│                                                          │
│   plan-execute orchestrator (after planner exits)        │
│         │                                                │
│         ├── preflight: scope, anchors, proof contracts   │
│         ├── executor: dispatch edits in order            │
│         ├── post-write source_sanity on source edits     │
│         ├── phase_evidence bundle for review-repair      │
│         ├── review-repair gate with scoped repairs       │
│         └── JSON telemetry stream (executor_edit, …)     │
│                                                          │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌─ Outputs ────────────────────────────────────────────────┐
│   File edits on disk                                     │
│   .harness/plans/<timestamp>.plan.json                     │
│   .harness/logs/conversations/<session>.jsonl              │
│   logs/telemetry/<session>-*.jsonl                       │
│   stdout JSONL stream (when --json is set)               │
└──────────────────────────────────────────────────────────┘
```

The boxes above are all harness-side. The runner gates, the workspace
dispatch, the orchestrator, the JSON telemetry — every piece is in
the binary. Project repos do not own this code.

## What projects bring

A project's "harness seam" is a small, well-defined set of files
under `<repo>/.harness/` plus a few related directories. Agent Harness looks
for these at well-known paths.

### `.harness/config.json` (required)

Project endpoint, workspace root, model alias, write enable,
optional MCP server list, optional language hints. See
`internal/platform/config/config.go` for the schema.

Minimum for a fresh project:

```json
{
  "app_name": "MyProject",
  "llama_server": {
    "kind": "mlx",
    "host": "127.0.0.1",
    "port": 47680,
    "model": "coder-model"
  },
  "workspace": {
    "root": "/abs/path/to/project",
    "cwd": "/abs/path/to/project",
    "shell": "/bin/bash",
    "command_timeout_seconds": 60,
    "max_output_bytes": 65536,
    "enable_writes": true
  }
}
```

### `.harness/external_tools/<name>.json` (optional, requires config opt-in)

Project-specific tool adapters. Each JSON file describes a `command`
+ `args` + `input_schema` that harness exposes as a workspace tool.
The schema is JSON-Schema for the parameters; the command is invoked
with the tool call's arguments rendered via `{placeholder}` substitution.

**Important:** the per-file directory is only scanned when
`workspace.external_tools_dir` is set in `.harness/config.json`. Without
that key, JSON files in `.harness/external_tools/` are silently ignored.
Inline definitions also work via the `workspace.external_tools` array
in config (no directory scan required). Recommended `config.json`
addition for the per-file pattern:

```json
{
  "workspace": {
    "external_tools_dir": ".harness/external_tools"
  }
}
```

This is how a project should expose domain-specific tools such as
`lookup`, `trace_code`, `fixture_inventory`, `id_ranges`, or
`state_machine`: they can shell out to project-side scripts while appearing to
the model as first-class workspace tools with typed schemas.

The on-disk JSON schema mirrors `config.ExternalToolDef`
(`internal/platform/config/config.go`); harness maps it to `workspace.ExternalTool`
at load time (`internal/capabilities/workspace/external_tool.go`). The JSON field
for the parameter schema is `input_schema`.

Set both `read_only: true` and `discovery: true` for external tools
that only return lookup, trace, context, impact, or validation evidence.
Scoped implementation lanes keep those declared project discovery tools
available alongside context/language tools. Leave either flag unset for
queue runners, generators, formatters, installers, or any tool that can
mutate source, artifacts, daemon state, network state, or caches; harness
does not infer safety from a command or tool name.

### `.harness/discovery/*` (auto-managed)

Agent Harness's discovery cache for this workspace: `context.md`,
`glossary.json`, `decisions.jsonl`, `modules.jsonl`, `manifest.json`,
`workflows.json`, `impact/*`. Generated by `harness context build`;
read by `harness context query/zoom/pack/impact`. Project doesn't edit
these by hand.

### `.harness/comprehension/*` (auto-managed)

Agent Harness's context-graph database: `graph.json`, `graph.sqlite`,
`graph.lock`, `manifest.json`. Persistent across runs. Generated and
maintained by `harness context build` / `--refresh`.

`graph.sqlite` is the active backend. It carries four tables:

- `graph_nodes`, `graph_edges`, `graph_metadata` — the codebase index
  (files, packages, symbols, imports, calls, tests).
- `graph_views` — the `context_pack` cache, keyed by content
  fingerprint (active graph `index_id` + canonical task/mode/query
  inputs). `Pack()` consults this row before recomputing; identical
  requests are served from the cache. Scoped refresh marks only the
  rows whose referenced paths changed; full rebuilds blanket-stale
  the table.

`views/<pack_id>.json` are human-readable exports of `graph_views`
rows, written alongside the SQLite row for inspection. The SQLite row
is the source of truth — deleting a `views/*.json` file does not
invalidate the cache.

When the planner uses `context_pack` to localize a plan, it can pass
the `pack_id` back through `submit_plan`. The executor finds it as
`PlanArtifact.PackID` and can rehydrate the cached view via
`comprehension.LoadViewByID(root, pack_id)` instead of re-discovering
the same files.

`harness_audit` reports cache state under `context_cache` (row count,
fresh/stale split, bytes stored, oldest/newest timestamps, plus
session-local hit/miss/write counters).

### `.harness/prompts/` (canonical lane resources plus project overlays)

The chat-side base prompt and profiles are project-owned. The lane
framing, generic rules, recovery language, readiness criteria, and
completion criteria are canonical harness resources embedded in the
binary. Projects customize them through overlays at well-known paths
rather than by editing canonical prompt files.

The chat-side prompt stack is configured through two `chat` keys:

- `chat.system_prompt_files` — ordered list of project prompt files
  concatenated into the system prompt at runtime, resolved relative to
  the config file's directory.
- `chat.profiles_dir` — directory of named project profile files; the
  runtime loads the matching one when it selects a profile.

`harness lane <mode>` wraps the brief with canonical mode-specific text
from the harness binary. Projects extend that wrapper via
`<repo>/.harness/prompts/lanes/<mode>/{preamble,postamble}.md`, inserted
between canonical text and the brief body.

The lane wrapper also prepends a canonical generic rules block. Project
overrides at `<repo>/.harness/prompts/rules/<name>.md` use same-name
semantics: a project file matching a canonical name **replaces** that
entry in place; project files with names not in the canonical set
**append** after the canonical block in alphabetical order. This lets a
project add `cpp-safety.md`, `ui-accessibility.md`, or other local
rules without editing canonical files. The full layer order in the
composed prompt is:

1. canonical generic rules + project rule replacements/extras
2. canonical lane preamble (mode-specific)
3. project lane preamble overlay
4. brief body
5. project lane postamble overlay
6. canonical lane postamble (mode-specific)

For a fresh project starting from `harness init`, the recommended
seed is:

```json
{
  "chat": {
    "system_prompt_files": [".harness/prompts/base.md"],
    "profiles_dir": ".harness/prompts/profiles"
  }
}
```

with a minimal `.harness/prompts/base.md` that the project can grow.

### Brief files (anywhere — by convention `.planning/`)

The actual atom briefs. Agent Harness doesn't care where they live; the
lane subcommand reads via `--prompt-file <path>`. Convention:
`<repo>/.planning/<atom-id>.md`.

## Portability Bootstrap Features

The lane system should include these reusable project-onboarding
features:

- `harness lane <mode>` composes canonical lane framing, project
  overlays, backend routing, JSON event forwarding, and telemetry.
- `.harness/backends.json` defines role-based profiles such as planner,
  coder, reviewer, verifier, summarizer, and sidecar-discovery.
- `harness backend start|stop|status|list` manages configured local
  inference backends and reports health, ports, queues, and degraded
  mode.
- `harness backend proxy` provides alias-routing for OpenAI-compatible
  local servers when a project needs stable model names over changing
  local model paths.
- `harness lane postflight` runs generic and project-specific quality
  checks against the unified diff and evidence bundle.
- `harness probe lane [--mode implement|plan-execute|all]` exercises
  synthetic briefs and verifies route shape, contract selection, and
  allowed-tool surfaces.
- `harness brief preload --kind <name>` emits canonical brief-authoring
  guidance with project overlays for domain-specific brief types.
- `harness skills install <name>` installs optional orchestrator-side
  templates without making those templates part of the core harness
  contract.

These features are specified in more detail in
[portability-bootstrap-design.md](portability-bootstrap-design.md).

## Always-on language support

Every language tool family in harness is loaded unconditionally:
`cpp_*`, `go_*`, `python_*`, `ts_*`, `rust_*`, `java_*`. The
workspace auto-detects which families are *active* for a given
repo by walking the workspace root and matching against language-
specific markers in `internal/repository/semantic/language/registry.go`. The
exact rules differ per language; representative examples:

- Go — `go.mod` at any depth, or `*.go` source files
- C/C++ — `compile_commands.json`, or `.c`/`.cc`/`.cpp`/`.h`/`.hpp`
  source files (note: `CMakeLists.txt` alone is *not* sufficient —
  harness relies on built sources or a compilation database)
- Python — `pyproject.toml`/`setup.py`, or `*.py` source files
- TypeScript / JavaScript — `package.json`, or `*.ts`/`*.tsx`/`*.js`
  source files
- Rust — `Cargo.toml`, or `*.rs` source files
- Java — `pom.xml`/`build.gradle`/`build.gradle.kts`, or `*.java`
  source files

Multi-language workspaces are supported transparently: a repo with
both Go and C++ source files exposes both tool families. There is no
project-level "install language X" step — harness always carries every
language it supports and lights up whichever applies at runtime. When
harness adds support for a new language, every project that updates the
binary picks it up automatically.

## The seam, summarized

| Concern | Lives in harness? | Lives in project? |
|---|---|---|
| Lane logic (planner/executor, gates, runner) | ✓ | |
| Tool dispatch + workspace operations | ✓ | |
| Lane preambles/postambles | ✓ (embedded) | overlay at `.harness/prompts/lanes/<mode>/{preamble,postamble}.md` |
| Generic rules (loop-protocol, brief-skeleton, known-traps-generic) | ✓ (embedded) | overlay at `.harness/prompts/rules/<name>.md` (replace by name; extras append) |
| Chat-side base prompt + profiles | (not — open question) | ✓ (project-owned at `.harness/prompts/base.md` + `prompts/profiles/`) |
| Language tool families | ✓ | (auto-detect) |
| Endpoint + workspace config | | ✓ |
| Project-specific tool adapters | | ✓ |
| Project-specific postflight rules | (generic checks ✓) | ✓ at `.harness/postflight/checks/<name>.json` |
| Discovery cache + context graph | | ✓ (auto) |
| Brief files | | ✓ |
| Project-specific rules + skills (e.g. cpp-safety, ears-specs) | | ✓ |

## Lane Invocation

The `harness lane` subcommand is the recommended entry point. It
composes the prompt overlay, routes plan-execute to the configured
coder, forwards JSON events, and emits a `lane.start` telemetry record:

```bash
# Plan-execute — multi-file atoms with atomic preflight
harness lane plan-execute --title atom-001 .planning/atom-001.md

# Implement — small / single-file atoms
harness lane implement --title atom-001 .planning/atom-001.md

# Force a non-default model (overrides plan-execute's coder auto-route)
harness lane plan-execute --model planner-model .planning/atom-001.md
```

The same effect can still be reached by invoking the lower-level
subcommands directly when finer control is needed:

```bash
# Plan-execute — multi-file atoms with atomic preflight
harness plan-execute \
  --config .harness/config.json \
  --model coder-model \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Implement — small / single-file atoms
harness implement \
  --config .harness/config.json \
  --model planner-model \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Atom-spec drafting (no tools, just a brief artifact)
harness artifact \
  --config .harness/config.json \
  --model planner-model \
  --prompt-file <planner-context.md> \
  --no-tools
```

Canonical lane preambles/postambles are defined in the binary, with a
project overlay seam at
`<repo>/.harness/prompts/lanes/<mode>/{preamble,postamble}.md`.

The lane subcommand owns prompt composition, default model selection,
JSON event capture, post-run telemetry, and postflight hooks so a
greenfield project does not need repository-specific wrapper scripts.

## When to use which lane

| Atom shape | Recommended lane | Why |
|---|---|---|
| 1 file, ≤2 edits | `implement` | overhead of plan-execute isn't worth it |
| 2-3 files, tightly coupled (e.g. header + impl + test) | `implement` or `plan-execute` | either works; plan-execute is more deterministic |
| 4+ files | `plan-execute` | atomic preflight and per-edit telemetry pay off |
| Pure-add (new file, write_file shape) | `implement` | model holds the whole content in one turn |
| Refine first-draft scratchpad | `refine` | internally uses `artifact --no-tools` so the harness writes `draft.md` from final-answer content |
| Mirror-site updates (enum reference, name table, ordinal check) | `plan-execute` | preflight catches anchor mismatches before partial-apply |
| Drafting a planner target packet | `atom-spec` | model writes the brief, no workspace touched |

Plan-execute briefs must be executable edit packets, not instructions
to rediscover or regenerate the patch at run time. `harness brief lint`
blocks required-edit contracts that ask the planner to run a mutating
command or append/insert from prose without explicit
`old_text`/`new_text`, `start_line`/`end_line`+`content`, or whole-file
replacement content. This is the pre-dispatch stale-anchor boundary:
if a command must generate the file, run it before dispatch and inline
the exact resulting edit shape.

## Lane telemetry reference

Three different streams carry lane telemetry, each with a different
audience and content:

- **Conversation log** (`<repo>/.harness/logs/conversations/<session>.jsonl`)
  — the runner's per-message debug log: every model call,
  thinking trace, tool request/result, role event (`route_plan`,
  `control_rails`, `brief_audit`, `next_action_state`, etc.), and
  the model's final answer. This is the canonical place to read
  what the lane actually did.
- **Stdout JSONL** (only when `--json` is set) — a curated subset
  of the conversation log plus the orchestrator's
  `harness.plan_execute.*` events. Designed for downstream
  consumers (CI dashboards, lane-postflight tooling) that want a
  single stream without parsing the conversation log.
- **Lane run ledger** (`<repo>/.harness/lane-history.jsonl`) — one compact
  append-only event per `harness lane` dispatch. This is the first
  diagnostic surface for repeated atom loops: it records the brief,
  final state, exit code, selected queue pool when visible, session id,
  model/tool calls, token totals, peak prompt size, duration, stop reason,
  and degradation classes without storing raw prompts or file bodies.
- **Telemetry stream** (`<repo>/logs/telemetry/<ts>-<cmd>-<pid>.jsonl`)
  — process-lifecycle records emitted by `internal/evidence/telemetry`:
  `session_start`, `process_start`, `process_exit`, `model_session`,
  `command_start`/`command_finished`, plus the per-tool wrapper
  records when invoking subcommands like `harness tool`. **Per-event
  runner roles and per-edit executor events do NOT land here** —
  read the conversation log or stdout JSONL for those.

Plan-execute orchestrator events (harness-side, after the planner phase
exits — emitted to stdout JSONL only when `--json` is set):

| Type | Fields | Meaning |
|---|---|---|
| `harness.plan_execute.plan_artifact_loaded` | `path`, `edits` | orchestrator picked up the planner's plan artifact |
| `harness.plan_execute.plan_artifact_missing` | `reason` | planner phase didn't produce an artifact (typically a typed `<<QUESTION>>:` blocker) |
| `harness.plan_execute.plan_artifact_invalid` | `path`, `reason` | artifact failed schema or version checks |
| `harness.plan_execute.preflight_ok` | `edits` | every edit's `old_text` matched the file before any dispatch |
| `harness.plan_execute.preflight_failed` | `edit_index`, `path`, `reason` | first mismatched edit; aborts before any edit lands |
| `harness.plan_execute.executor_start` | `edits` | dispatching begins |
| `harness.plan_execute.executor_edit` | `step`, `total`, `path`, `status`, `error` | one edit dispatched (per edit) |
| `harness.plan_execute.executor_complete` | `total`, `succeeded`, `failed`, `duration_ms` | dispatch finished |

Lane wrapper events:

| Type | Fields | Meaning |
|---|---|---|
| `harness.lane.atom_intake` | `state`, `dispatchable`, `scope_paths`, `existing_paths`, `missing_paths`, `invalid_paths`, `reasons`, `next_actions` | Pre-dispatch atom classification for atom-shaped briefs. Non-dispatchable states stop before lint, context-pack preload, queue acquisition, or model calls. |
| `harness.lane.run_ledger` | `mode`, `brief`, `title`, `exit_code`, `final_state`, `session_id`, `queue_pool`, `model_calls`, `tool_calls`, `prompt_tokens`, `completion_tokens`, `total_tokens`, `peak_prompt_tokens`, `ctx_size`, `duration_ms`, `stop_reason`, `degradations` | Compact per-dispatch summary emitted when `harness lane --json` is set and persisted in `.harness/lane-history.jsonl` for every run. |
| `harness.lane.degraded` | `kind`, `details`, `mode` | Post-analysis detected a regression class such as read-only routing or no edit attempt. |

Runner-side events emitted to the conversation log (non-exhaustive
— grep `internal/runtime/agent/runner.go` for `RunEvent{Role:` and
`emitEvent` callers for the live set; the most useful for tracing a
lane are listed):

| Role | Meaning |
|---|---|
| `route_plan` | which lane the brief routed into |
| `control_rails` | task family, contract, permission profile, allowed tools |
| `brief_audit` | required_edit_paths, evidence paths, readiness state |
| `permission_decision` | per-tool allow/deny |
| `next_action_state` | runner phase + last tool + result summary |
| `read_budget_diag` | budget snapshot per read-class call |
| `readiness_check` | runtime readiness gate snapshot |
| `final_answer_critic` | critic accept/reject per turn |
| `control_rails_enforcement` | rails enforcement per turn |
| `tool_hidden` | hide/release notice for a tool surface |
| `apply_patch_changed_path_preflight` | apply_patch preflight skip/rewrite |
| `verification_intercept_already_edited` | redirect on read of ✓ required path |
| `plan_then_execute_write_guard` | per-edit write rejected during planner phase |
| `plan_then_execute_readiness_nudge` | every required path read; submit_plan now |
| `plan_then_execute_short_circuit` | submit_plan succeeded; final answer synthesized |
| `required_edit_paths_progress` | ✓/✗ checklist after each successful write |
| `completion_gate_recovery` | runner-side recovery from a rejected final answer |
| `final_answer_blocker_override` | runner overrode a fabricated final answer |
| `answer_decorated` | decorator rewrote the model's answer |
| `doom_loop_guard` | three identical tool calls; approval required |
| `discovery_redirect` | redirected discovery query to a different tool surface |
| `read_budget_hard_block` | hard read-budget block |
| `delegate_edit_*` | executor route/error/parse-error/direct dispatch events |

Note: `tool_request` and `tool_result` are typed event objects
(emitted via `recorder.record*` helpers, not via `emitEvent`), with
their own JSONL `type` field — they're separate from `RunEvent`
roles. Likewise `model_call`, `thinking_trace`, `completion_status`,
`turn_summary`, `session_summary` are typed records the runner
writes directly to the conversation log.

## Where to learn more

- `docs/portability-bootstrap-design.md` — greenfield initialization,
  doctor, backend, lane, sidecar, and packaging expectations.
- `docs/design-auditable-planning-handoff.md` — typed plan artifacts,
  packet-backed handoff, and executor obligations.
- `docs/codebase-comprehension-memory-design.md` — context packets,
  cached views, freshness, and trace-backed evidence.
