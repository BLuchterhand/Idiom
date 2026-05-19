# Lane Architecture

This document describes the implementation lane shape harness provides
and how project repos overlay it. It is the canonical reference for
"what does harness give me, and what do I bring."

For the migration plan that lifts more of today's project-side
plumbing into harness, see `docs/reusability-design.md`.

## What is a lane?

A **lane** is one round-trip from a brief to a set of completed
edits, optionally with a verification pass. Agent Harness currently provides
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
  `harness artifact --no-tools --mechanical-repair --json-out ... --report-out ...`
  today; will be `harness atom-spec draft` after Phase 2 of the
  reusability migration.

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
`internal/config/config.go` for the schema.

Minimum for a fresh project:

```json
{
  "app_name": "MyProject",
  "llama_server": {
    "kind": "mlx",
    "host": "127.0.0.1",
    "port": 47680,
    "model": "local-model3-coder-30b"
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

This is how original implementation exposes `lookup`, `trace_code`, `ast_grep`,
`fixture_inventory`, `id_ranges`, `state_machine`, etc. — they all
shell out to project-side scripts but appear to the model as
first-class workspace tools.

The on-disk JSON schema mirrors `config.ExternalToolDef`
(`internal/config/config.go`); harness maps it to `workspace.ExternalTool`
at load time (`internal/workspace/external_tool.go`). The JSON field
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

### `.harness/prompts/` (mixed — chat side project-owned; lane wrapper has canonical + overlay)

The chat-side base prompt is fully project-owned today; lane
preambles/postambles and the generic rules block ship as
canonical-in-harness (embedded), with project overlay at well-known
paths. See the seam table at the end of this doc.

Today the chat-side prompt stack is fully config-driven via two
`chat`-section keys (`internal/config/config.go`):

- `chat.system_prompt_files` — ordered list of files concatenated
  into the system prompt at runtime. Resolved relative to the config
  file's directory. original implementation's config sets this to
  `["prompts/base.md", "prompts/profiles/coding.md",
  "prompts/profiles/external-agent-worker.md"]`, which expands to files
  under `.harness/prompts/`.
- `chat.profiles_dir` — directory of named profile files; the runtime
  loads the matching one when it selects a profile.

The lane runner concatenates only what `system_prompt_files` lists
for the chat-side system prompt; that part still has no embedded
canonical base. Projects own the full text via
`.harness/prompts/base.md`.

Lane preambles/postambles are different. As of Phase 3 of the
reusability migration, `harness lane <mode>` wraps the brief with
canonical-in-harness mode-specific text loaded via `go:embed` from
`cmd/harness/lanes/<mode>/{preamble,postamble}.md`. A binary-only
deployment carries the canonical lane wrapper without needing a
co-located prompts directory. Projects extend (never replace) that
wrapper via `<repo>/.harness/prompts/lanes/<mode>/{preamble,postamble}.md`,
inserted between the canonical text and the brief body.

The lane wrapper also prepends a **canonical generic rules** block to
every composed prompt, loaded via `go:embed` from
`cmd/harness/lanes/rules/{loop-protocol, brief-skeleton,
known-traps-generic}.md` (Phase 3 backfill, YYYY-MM-DD). Project
overrides at `<repo>/.harness/prompts/rules/<name>.md` use same-name
semantics: a project file matching a canonical name **replaces** that
entry in place; project files with names NOT in the canonical set
**append** after the canonical block in alphabetical order. This lets
a project drop in `cpp-safety.md`, `gdscript-patterns.md`, etc. as
extras without editing the canonical files. The full layer order in
the composed prompt is:

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

## What's coming after the reusability migration

Phases shipped (`docs/reusability-design.md`):

- Phase 1-2 — `harness lane <mode>` wrapper with overlay seam
- Phase 3 — canonical lane preambles/postambles via `go:embed`,
  project overlay at `.harness/prompts/lanes/<mode>/{preamble,postamble}.md`
- Phase 4 — `.harness/backends.json` schema with `default_profile` /
  `default_coder` resolution, plus the `harness backend list`
  introspection command. The lane
  wrapper's `--coder` shortcut and plan-execute auto-route both pull
  the alias from this file when present, falling back to the legacy
  hardcoded `local-model3-coder-30b` so existing setups keep working.
- Phase 4b — `harness backend start|stop|status` launcher port. Argv
  is constructed from the schema (`binary` + `--model` + `--port` +
  optional cache/token flags + `extra_args`), so any local-LLM
  launcher whose CLI matches the MLX shape works without project
  scripts. Detached spawn (Setpgid) so backends survive harness exit;
  ready-check is a TCP connect to the listening port; stop sends
  SIGTERM with a configurable grace window before SIGKILL.
- Phase 4c — `harness backend proxy` alias-routing port. Listens on
  `profile.proxy.listen`, picks a backend by the request's `model`
  field (falling back to `default_alias`), rewrites that field to
  the absolute model path mlx_lm.server expects, merges multiple
  system messages into one leading entry for chat-completion
  compatibility, and streams responses chunk-by-chunk to preserve
  SSE for `stream: true`. `--listen` and `--default` flags override
  the schema for ad-hoc runs; SIGINT/SIGTERM trigger graceful
  shutdown. Replaces original implementation's `mlx_alias_proxy.py`.

- Phase 5 — `harness lane postflight` runs a configurable QC pipeline
  against a unified diff. Three always-on generic checks
  (unresolved `<<QUESTION>>:` markers in modified files, write
  outside `required_edit_paths`, narrating comments in newly-added
  test code) plus project rules under
  `.harness/postflight/checks/<name>.json` (`kind: shell` runner;
  `kind: ast_grep` reserved). Exit codes: `0` pass, `1` IO/setup,
  `2` flag error, `3` checks fired — separate so callers can tell
  "ran and found bugs" apart from "couldn't run."
- Phase 6 — three project-onboarding helpers:
  - `harness probe lane [--mode implement|plan-execute|all]` runs a
    synthetic-brief route probe in-process and asserts route shape
    (initial=implement, contract=implementation_result,
    reason=explicit_task_family) and a non-empty allowed-tools
    surface. Replaces `harness_route_probe.sh`.
  - `harness audit enum-mirrors --enum <Name> [--language cpp|go|rust|ts|any]`
    walks the workspace, lists files referencing the symbol, and
    scans each for high-signal mirror patterns (count assertions,
    name tables, switch cases). Each --language profile bundles a
    pattern set + extension filter; `--pattern <regex with {ENUM}>`
    and `--ext <extension>` are escape hatches.

    **Implementation note:** the audit uses token-bounded regex grep,
    not the LSP-aware `*_references` tools the design doc imagined.
    Trade-off: grep is language-agnostic and runs without an LSP
    setup (cpp_references requires clangd) but produces lower-
    precision results — string literals and comments containing
    the symbol show up as references. Operators should treat the
    file list as a starting point for a brief, not a precise
    references query. A future `--lsp` mode could call into the
    language-aware tools when the workspace has them wired.
  - `harness brief preload --kind <name>` (or `--list`) emits the
    canonical atom-spec drafting guidance, with project overrides
    at `<repo>/.harness/prompts/brief/preload/<kind>.md`. Agent Harness ships
    only `generic`; project-specific kinds live as overlays.

- Phase 3 rules-embed backfill (YYYY-MM-DD) — canonical generic
  rules `loop-protocol.md`, `brief-skeleton.md`, and
  `known-traps-generic.md` are embedded under `cmd/harness/lanes/rules/`
  and prepended to every composed lane prompt. Project overlay at
  `<repo>/.harness/prompts/rules/<name>.md` uses same-name semantics:
  matching project files **replace** the canonical entry in place;
  files with names NOT in the canonical set **append** alphabetically
  after the canonical block as project-specific extras (e.g.
  `cpp-safety.md`, `gdscript-patterns.md`).

- Phase 6.5 (YYYY-MM-DD) — `harness lane <mode>` absorbs the wrapper-
  side scaffolding original implementation's `run_harness_impl.sh` carried so the
  shell wrapper can shrink from ~419 lines to ~15:
  - **Brief lint preflight** runs by default; `--no-lint` opt-out
    for callers that already lint upstream. Lint exit code
    propagates to the lane wrapper and aborts dispatch on failure.
  - **`--slot <key>`** opt-in concurrency mutex via mkdir on
    `$TMPDIR/harness-lane-<key>`. Empty default = no mutex. Caller
    chooses the granularity (per-model, per-workspace, per-brief).
    Held slot exits 3 with a clear "already held" message.
  - **Post-analyze** scans the subcommand's stdout after dispatch
    for two regression classes: write-capable brief routed to a
    read-only contract (`read_only_lane`), and clean-exit run
    that never attempted an edit / blocker (`no_edit_attempt`).
    Each emits a structured `harness.lane.degraded` JSONL event;
    wrapper exit code is 4 when any degradation fires (and the
    subcommand exited 0). The `no_edit_attempt` check is
    suppressed when the subcommand exits non-zero — the failure
    code already carries that signal.
  - **Audit-feedback dispatch stays project-side.** Agent Harness emits
    the `harness.lane.degraded` event; projects subscribe (e.g.
    original implementation's wrapper pipes the event into `harness_feedback`).
    This keeps harness from carrying any single project's audit-
    ledger schema.

Phase 7 adds these project-side hooks (not yet present today;
placeholder paths shown so projects can plan ahead):

- canonical profile templates copied during `harness init` (e.g.
  `prompts/profiles/coding.md`, `prompts/profiles/external-agent-worker.md`)
- `harness skills install <name>` for orchestrator-side skill
  templates (including `orchestrator-workflow`, deferred from
  Phase 3 per the design's own recommendation)

Until those phases ship, projects use today's overlay points
(`.harness/prompts/base.md` + the lane overlay seam + the rules
overlay seam + project-side scripts wrapping the existing
`harness plan-execute` / `harness implement`).

## Always-on language support

Every language tool family in harness is loaded unconditionally:
`cpp_*`, `go_*`, `python_*`, `ts_*`, `rust_*`, `java_*`. The
workspace auto-detects which families are *active* for a given
repo by walking the workspace root and matching against language-
specific markers in `internal/workspace/language_registry.go`. The
exact rules differ per language; representative ones today:

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
| Chat-side base prompt + profiles | (not yet — open question) | ✓ (project-owned at `.harness/prompts/base.md` + `prompts/profiles/`) |
| Language tool families | ✓ | (auto-detect) |
| Endpoint + workspace config | | ✓ |
| Project-specific tool adapters | | ✓ |
| Project-specific postflight rules | (generic checks ✓) | ✓ at `.harness/postflight/checks/<name>.json` |
| Discovery cache + context graph | | ✓ (auto) |
| Brief files | | ✓ |
| Project-specific rules + skills (e.g. cpp-safety, ears-specs) | | ✓ |

## Lane invocation, today

The `harness lane` subcommand (Phase 1 of the reusability migration)
is the recommended entry point. It composes the prompt overlay,
auto-routes plan-execute to the configured coder, forwards JSON
events, and emits a `lane.start` telemetry record:

```bash
# Plan-execute — multi-file atoms with atomic preflight
harness lane plan-execute --title atom-001 .planning/atom-001.md

# Implement — small / single-file atoms
harness lane implement --title atom-001 .planning/atom-001.md

# Force a non-default model (overrides plan-execute's coder auto-route)
harness lane plan-execute --model local-model3.6-35b .planning/atom-001.md
```

The same effect can still be reached by invoking the lower-level
subcommands directly when finer control is needed:

```bash
# Plan-execute — multi-file atoms with atomic preflight
harness plan-execute \
  --config .harness/config.json \
  --model local-model3-coder-30b \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Implement — small / single-file atoms
harness implement \
  --config .harness/config.json \
  --model local-model3.6-35b \
  --prompt-file .planning/atom-001.md \
  --planner-owned-verification \
  --json

# Atom-spec drafting (no tools, just a brief artifact)
harness artifact \
  --config .harness/config.json \
  --model local-model3.6-35b \
  --prompt-file <planner-context.md> \
  --no-tools
```

Phase 3 of the reusability migration is shipped: canonical lane
preambles/postambles ship in the binary via `go:embed`, with a
project overlay seam at
`<repo>/.harness/prompts/lanes/<mode>/{preamble,postamble}.md`.
Atom-spec drafting will gain its own subcommand
(`harness atom-spec draft`) in a later phase.

The lane subcommand absorbs prompt composition, default model
selection, JSON event capture, and post-run telemetry — what
`scripts/run_harness_impl.sh` does today in original implementation.

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
  — process-lifecycle records emitted by `internal/telemetry`:
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

- `docs/reusability-design.md` — phased plan to lift original implementation-side
  plumbing into harness.
- `docs/planner-executor-usage.md` — user guide for the three options
  (intercept, delegate_edit, plan-execute).
- `docs/planner-executor-delegation-design.md` — original design
  doc for the three options.
- `docs/original implementation-extraction-plan.md` — raw inventory of original implementation's
  scripts/rules and migration targets.
