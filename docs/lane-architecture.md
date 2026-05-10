# Lane Architecture

This document describes the implementation lane shape idiom provides
and how project repos overlay it. It is the canonical reference for
"what does idiom give me, and what do I bring."

For the migration plan that lifts more of today's project-side
plumbing into idiom, see `docs/reusability-design.md`.

## What is a lane?

A **lane** is one round-trip from a brief to a set of completed
edits, optionally with a verification pass. Idiom currently provides
three lane shapes:

- **`implement`** — write-capable lane where the model interleaves
  reads and edits. Use this when the brief is small (1-3 files,
  tightly coupled) and the planner can hold the work in one
  trajectory. Driven by `idiom implement`.
- **`plan-execute`** — two-phase lane: a planner phase emits a
  single structured `submit_plan` tool call enumerating every edit;
  an executor phase dispatches each edit deterministically. Use this
  when the brief is multi-file (3+ files) or when atomic
  preflight/per-edit telemetry matters more than per-edit
  decision-making. Driven by `idiom plan-execute`.
- **`atom-spec`** — drafting lane: model produces a brief artifact
  (a planner target packet) without touching the workspace, then
  exits. Used when the orchestrator needs a clean spec to feed into
  a downstream `implement` or `plan-execute` lane. Driven by
  `idiom artifact --no-tools` today; will be `idiom atom-spec draft`
  after Phase 2 of the reusability migration.

Every lane consumes a **brief** (markdown describing the work) and
produces some combination of: changed files, a plan artifact, a
final answer, structured telemetry events.

## What idiom provides

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
┌─ idiom CLI surface ──────────────────────────────────────┐
│                                                          │
│   idiom plan-execute / implement / artifact              │
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
│         │     • external tool registry (.idiom/*)        │
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
│         ├── preflight: every edit's old_text matches     │
│         ├── executor: dispatch edits in order            │
│         └── JSON telemetry stream (executor_edit, …)     │
│                                                          │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌─ Outputs ────────────────────────────────────────────────┐
│   File edits on disk                                     │
│   .idiom/plans/<timestamp>.plan.json                     │
│   .idiom/logs/conversations/<session>.jsonl              │
│   logs/telemetry/<session>-*.jsonl                       │
│   stdout JSONL stream (when --json is set)               │
└──────────────────────────────────────────────────────────┘
```

The boxes above are all idiom-side. The runner gates, the workspace
dispatch, the orchestrator, the JSON telemetry — every piece is in
the binary. Project repos do not own this code.

## What projects bring

A project's "idiom seam" is a small, well-defined set of files
under `<repo>/.idiom/` plus a few related directories. Idiom looks
for these at well-known paths.

### `.idiom/config.json` (required)

Project endpoint, workspace root, model alias, write enable,
optional MCP server list, optional language hints. See
`internal/config/config.go` for the schema.

Minimum for a fresh project:

```json
{
  "app_name": "MyProject",
  "llama_server": {
    "kind": "mlx",
    "host": "127.0.0.1",
    "port": 47680,
    "model": "qwen3-coder-30b"
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

### `.idiom/external_tools/<name>.json` (optional, requires config opt-in)

Project-specific tool adapters. Each JSON file describes a `command`
+ `args` + `input_schema` that idiom exposes as a workspace tool.
The schema is JSON-Schema for the parameters; the command is invoked
with the tool call's arguments rendered via `{placeholder}` substitution.

**Important:** the per-file directory is only scanned when
`workspace.external_tools_dir` is set in `.idiom/config.json`. Without
that key, JSON files in `.idiom/external_tools/` are silently ignored.
Inline definitions also work via the `workspace.external_tools` array
in config (no directory scan required). Recommended `config.json`
addition for the per-file pattern:

```json
{
  "workspace": {
    "external_tools_dir": ".idiom/external_tools"
  }
}
```

This is how Colony exposes `lookup`, `trace_code`, `ast_grep`,
`fixture_inventory`, `id_ranges`, `state_machine`, etc. — they all
shell out to project-side scripts but appear to the model as
first-class workspace tools.

The on-disk JSON schema mirrors `config.ExternalToolDef`
(`internal/config/config.go`); idiom maps it to `workspace.ExternalTool`
at load time (`internal/workspace/external_tool.go`). The JSON field
for the parameter schema is `input_schema`.

### `.idiom/discovery/*` (auto-managed)

Idiom's discovery cache for this workspace: `context.md`,
`glossary.json`, `decisions.jsonl`, `modules.jsonl`, `manifest.json`,
`workflows.json`, `impact/*`. Generated by `idiom context build`;
read by `idiom context query/zoom/pack/impact`. Project doesn't edit
these by hand.

### `.idiom/comprehension/*` (auto-managed)

Idiom's context-graph database: `graph.json`, `graph.sqlite`,
`graph.lock`, `manifest.json`. Persistent across runs. Generated and
maintained by `idiom context build` / `--refresh`.

### `.idiom/prompts/` (config-driven, no overlay yet)

Today the prompt stack is fully config-driven via two `chat`-section
keys (`internal/config/config.go`):

- `chat.system_prompt_files` — ordered list of files concatenated
  into the system prompt at runtime. Resolved relative to the config
  file's directory. Colony's config sets this to
  `["prompts/base.md", "prompts/profiles/coding.md",
  "prompts/profiles/claude-qwen-worker.md"]`, which expands to files
  under `.idiom/prompts/`.
- `chat.profiles_dir` — directory of named profile files; the runtime
  loads the matching one when it selects a profile.

The lane runner concatenates only what `system_prompt_files` lists
for the chat-side system prompt; that part still has no embedded
canonical base. Projects own the full text via
`.idiom/prompts/base.md`.

Lane preambles/postambles are different. As of Phase 3 of the
reusability migration, `idiom lane <mode>` wraps the brief with
canonical-in-idiom mode-specific text loaded via `go:embed` from
`cmd/idiom/lanes/<mode>/{preamble,postamble}.md`. A binary-only
deployment carries the canonical lane wrapper without needing a
co-located prompts directory. Projects extend (never replace) that
wrapper via `<repo>/.idiom/prompts/lanes/<mode>/{preamble,postamble}.md`,
inserted between the canonical text and the brief body.

For a fresh project starting from `idiom init`, the recommended
seed is:

```json
{
  "chat": {
    "system_prompt_files": [".idiom/prompts/base.md"],
    "profiles_dir": ".idiom/prompts/profiles"
  }
}
```

with a minimal `.idiom/prompts/base.md` that the project can grow.

### Brief files (anywhere — by convention `.planning/`)

The actual atom briefs. Idiom doesn't care where they live; the
lane subcommand reads via `--prompt-file <path>`. Convention:
`<repo>/.planning/<atom-id>.md`.

## What's coming after the reusability migration

Phases shipped (`docs/reusability-design.md`):

- Phase 1-2 — `idiom lane <mode>` wrapper with overlay seam
- Phase 3 — canonical lane preambles/postambles via `go:embed`,
  project overlay at `.idiom/prompts/lanes/<mode>/{preamble,postamble}.md`
- Phase 4 — `.idiom/backends.json` schema with `default_coder` resolution,
  plus the `idiom backend list` introspection command. The lane
  wrapper's `--coder` shortcut and plan-execute auto-route both pull
  the alias from this file when present, falling back to the legacy
  hardcoded `qwen3-coder-30b` so existing setups keep working.

Phases 4b/5/6/7 add these project-side hooks (not yet present today;
placeholder paths shown so projects can plan ahead):

- `idiom backend start|stop|proxy` — Phase 4b launcher port
- `.idiom/postflight/checks/<name>.json` — per-project diff lint
  rules (Phase 5)
- `.idiom/prompts/rules/<name>.md` — rule overrides for the
  canonical generic rules (later phase)

Until those phases ship, projects use today's overlay points
(`.idiom/prompts/base.md` + the lane overlay seam + project-side
scripts wrapping the existing `idiom plan-execute` /
`idiom implement`).

## Always-on language support

Every language tool family in idiom is loaded unconditionally:
`cpp_*`, `go_*`, `python_*`, `ts_*`, `rust_*`, `java_*`. The
workspace auto-detects which families are *active* for a given
repo by walking the workspace root and matching against language-
specific markers in `internal/workspace/language_registry.go`. The
exact rules differ per language; representative ones today:

- Go — `go.mod` at any depth, or `*.go` source files
- C/C++ — `compile_commands.json`, or `.c`/`.cc`/`.cpp`/`.h`/`.hpp`
  source files (note: `CMakeLists.txt` alone is *not* sufficient —
  idiom relies on built sources or a compilation database)
- Python — `pyproject.toml`/`setup.py`, or `*.py` source files
- TypeScript / JavaScript — `package.json`, or `*.ts`/`*.tsx`/`*.js`
  source files
- Rust — `Cargo.toml`, or `*.rs` source files
- Java — `pom.xml`/`build.gradle`/`build.gradle.kts`, or `*.java`
  source files

Multi-language workspaces are supported transparently: a repo with
both Go and C++ source files exposes both tool families. There is no
project-level "install language X" step — idiom always carries every
language it supports and lights up whichever applies at runtime. When
idiom adds support for a new language, every project that updates the
binary picks it up automatically.

## The seam, summarized

| Concern | Lives in idiom? | Lives in project? |
|---|---|---|
| Lane logic (planner/executor, gates, runner) | ✓ | |
| Tool dispatch + workspace operations | ✓ | |
| Generic prompts (base, addendum, lane preambles)* | ✓ | overlay |
| Generic rules (loop protocol, brief skeleton, traps)* | ✓ | overlay |
| Language tool families | ✓ | (auto-detect) |
| Endpoint + workspace config | | ✓ |
| Project-specific tool adapters | | ✓ |
| Project-specific lint / postflight rules* | | ✓ |
| Discovery cache + context graph | | ✓ (auto) |
| Brief files | | ✓ |
| Project-specific rules + skills (e.g. cpp-safety, ears-specs) | | ✓ |

\* Items marked with * become canonical-in-idiom after the
reusability migration completes (Phases 2-3, 5 of
`docs/reusability-design.md`).

## Lane invocation, today

The `idiom lane` subcommand (Phase 1 of the reusability migration)
is the recommended entry point. It composes the prompt overlay,
auto-routes plan-execute to the configured coder, forwards JSON
events, and emits a `lane.start` telemetry record:

```bash
# Plan-execute — multi-file atoms with atomic preflight
idiom lane plan-execute --title atom-001 .planning/atom-001.md

# Implement — small / single-file atoms
idiom lane implement --title atom-001 .planning/atom-001.md

# Force a non-default model (overrides plan-execute's coder auto-route)
idiom lane plan-execute --model qwen3.6-35b .planning/atom-001.md
```

The same effect can still be reached by invoking the lower-level
subcommands directly when finer control is needed:

```bash
# Plan-execute — multi-file atoms with atomic preflight
idiom plan-execute \
  --config .idiom/config.json \
  --model qwen3-coder-30b \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Implement — small / single-file atoms
idiom implement \
  --config .idiom/config.json \
  --model qwen3.6-35b \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Atom-spec drafting (no tools, just a brief artifact)
idiom artifact \
  --config .idiom/config.json \
  --model qwen3.6-35b \
  --prompt-file <planner-context.md> \
  --no-tools
```

Phase 3 of the reusability migration is shipped: canonical lane
preambles/postambles ship in the binary via `go:embed`, with a
project overlay seam at
`<repo>/.idiom/prompts/lanes/<mode>/{preamble,postamble}.md`.
Atom-spec drafting will gain its own subcommand
(`idiom atom-spec draft`) in a later phase.

The lane subcommand absorbs prompt composition, default model
selection, JSON event capture, and post-run telemetry — what
`scripts/run_idiom_impl.sh` does today in Colony.

## When to use which lane

| Atom shape | Recommended lane | Why |
|---|---|---|
| 1 file, ≤2 edits | `implement` | overhead of plan-execute isn't worth it |
| 2-3 files, tightly coupled (e.g. header + impl + test) | `implement` or `plan-execute` | either works; plan-execute is more deterministic |
| 4+ files | `plan-execute` | atomic preflight and per-edit telemetry pay off |
| Pure-add (new file, write_file shape) | `implement` | model holds the whole content in one turn |
| Mirror-site updates (enum reference, name table, ordinal check) | `plan-execute` | preflight catches anchor mismatches before partial-apply |
| Drafting a planner target packet | `atom-spec` | model writes the brief, no workspace touched |

## Lane telemetry reference

Three different streams carry lane telemetry, each with a different
audience and content:

- **Conversation log** (`<repo>/.idiom/logs/conversations/<session>.jsonl`)
  — the runner's per-message debug log: every model call,
  thinking trace, tool request/result, role event (`route_plan`,
  `control_rails`, `brief_audit`, `next_action_state`, etc.), and
  the model's final answer. This is the canonical place to read
  what the lane actually did.
- **Stdout JSONL** (only when `--json` is set) — a curated subset
  of the conversation log plus the orchestrator's
  `idiom.plan_execute.*` events. Designed for downstream
  consumers (CI dashboards, lane-postflight tooling) that want a
  single stream without parsing the conversation log.
- **Telemetry stream** (`<repo>/logs/telemetry/<ts>-<cmd>-<pid>.jsonl`)
  — process-lifecycle records emitted by `internal/telemetry`:
  `session_start`, `process_start`, `process_exit`, `model_session`,
  `command_start`/`command_finished`, plus the per-tool wrapper
  records when invoking subcommands like `idiom tool`. **Per-event
  runner roles and per-edit executor events do NOT land here** —
  read the conversation log or stdout JSONL for those.

Plan-execute orchestrator events (idiom-side, after the planner phase
exits — emitted to stdout JSONL only when `--json` is set):

| Type | Fields | Meaning |
|---|---|---|
| `idiom.plan_execute.plan_artifact_loaded` | `path`, `edits` | orchestrator picked up the planner's plan artifact |
| `idiom.plan_execute.plan_artifact_missing` | `reason` | planner phase didn't produce an artifact (typically a typed `<<QUESTION>>:` blocker) |
| `idiom.plan_execute.plan_artifact_invalid` | `path`, `reason` | artifact failed schema or version checks |
| `idiom.plan_execute.preflight_ok` | `edits` | every edit's `old_text` matched the file before any dispatch |
| `idiom.plan_execute.preflight_failed` | `edit_index`, `path`, `reason` | first mismatched edit; aborts before any edit lands |
| `idiom.plan_execute.executor_start` | `edits` | dispatching begins |
| `idiom.plan_execute.executor_edit` | `step`, `total`, `path`, `status`, `error` | one edit dispatched (per edit) |
| `idiom.plan_execute.executor_complete` | `total`, `succeeded`, `failed`, `duration_ms` | dispatch finished |

Runner-side events emitted to the conversation log (non-exhaustive
— grep `internal/agent/runner.go` for `RunEvent{Role:` and
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

- `docs/reusability-design.md` — phased plan to lift Colony-side
  plumbing into idiom.
- `docs/planner-executor-usage.md` — user guide for the three options
  (intercept, delegate_edit, plan-execute).
- `docs/planner-executor-delegation-design.md` — original design
  doc for the three options.
- `docs/colony-extraction-plan.md` — raw inventory of Colony's
  scripts/rules and migration targets.
