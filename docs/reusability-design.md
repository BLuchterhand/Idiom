# Reusability Design — Lift the Plan-Execute Workflow into Idiom

**Status:** Design draft, 2026-05-09 (Phases 1-5 + 4b shipped 2026-05-09; Phase 4c/6/7/8 pending)
**Author:** Idiom session log, Colony donkey-atom debugging
**Related:** `docs/colony-extraction-plan.md` (raw inventory),
`docs/planner-executor-delegation-design.md` (the lane shape this
generalizes), `docs/planner-executor-usage.md` (current user-facing docs).

## Background

The plan-execute lane (planner phase emits `submit_plan`, executor
phase dispatches the edits) is now production-ready end-to-end on
Colony's donkey atom: 26 seconds for a 9-file, 11-edit atom with all
gates green and structured telemetry. Six runner-side gates plus
two prompt-side fixes plus a brief-lint extension got us there.

But the actual workflow today is a mix of:

- Idiom giving the lane shape, gates, runner, workspace tools.
- Colony providing a thick layer of `bash`/`python` wrappers,
  prompt-stack overrides, and rule docs that compose a brief, route
  a model, validate a diff, and feed everything to idiom.

If you wanted to set up the same workflow in a different repo today,
you'd be copy-pasting ~25 shell scripts, two parallel rules trees
(`.claude/` and `.codex/`), and a chunk of YAML/JSON tool adapters.
That's the friction this design addresses.

## Goals

1. **Anything not project-specific lives in idiom.** A new project
   should clone idiom-provided defaults, not invent them.
2. **Project layer is small and structured.** A clear, documented
   set of files in `.idiom/` is what a project owns. Everything else
   inherits.
3. **No language gating.** Idiom always supports every language it has
   tools for; no per-project "install" step. Workspace mode is
   detected at runtime from filesystem evidence (already true today —
   we don't undo that).
4. **No churn for existing users.** Colony's current plumbing keeps
   working through this whole migration. The end state is
   compatibility shims so old scripts call new idiom commands.

## Non-goals

- Not building a "framework" with plugin loaders. The Go binary
  loads its own canonical resources at compile time; project layers
  override via well-known paths.
- Not redesigning the plan-execute lane itself. The current shape
  (`submit_plan`, executor dispatch, JSON event stream) is what gets
  generalized — not rewritten.
- Not migrating Claude/Codex SKILL files for projects.
  `idiom skills install <name>` is a copy-templated convenience but
  the canonical author of project skills is the project.

## Architectural principles

### The seam: idiom resources, project overrides

Idiom ships canonical resources at well-known paths inside its
install (or embedded via `go:embed`). A project's `.idiom/` directory
is the override surface. The lookup order for any given resource is:

1. Project: `<repo>/.idiom/<resource-path>`
2. Idiom builtin: embedded canonical default

The project layer is **additive or replacing**, not "instead of."
For prompts the merge is concatenation (idiom's preamble first,
then project's preamble). For tool registries, the project's entries
extend idiom's (with project entries winning by name on collision).
For postflight rules, project rules extend idiom's generic rule set.

### Always-on language support

Idiom binary always carries every language tool. Workspace mode is
auto-detected from filesystem markers (`go.mod`, `package.json`,
`*.csproj`, `Cargo.toml`, `CMakeLists.txt`, `pyproject.toml`,
`pom.xml`). Multiple language matches are fine — idiom exposes all
matched language tool families to the model.

Today this is already approximately true (`m.isGoWorkspace()` etc.
in `internal/workspace/manager.go`). The change is making the
detection *exhaustive* (no opt-out, no per-project disable) and
documenting it as guaranteed.

### Configuration with sensible defaults

`.idiom/config.json` holds project-specific settings:

- LLM endpoint, model alias, prompt cache size
- Workspace root, CWD, write enable
- Optional MCP server list
- Optional language hints (only used to bias detection ties)

Idiom ships a default config baked into `idiom init`. New projects
edit a few fields and go.

### Project-specific tooling stays project-specific

Colony's `lookup.sh`, `trace_code.sh`, `ast_grep.sh`, etc. do
domain-specific work (C++ symbol queries with project-aware filters,
Godot scene tracing). Those stay in Colony. The pattern that exposes
them — `.idiom/external_tools/<name>.json` describing
`{command, args, env, schema}` — is generic and stays in idiom.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Project repo (e.g. Colony)                                  │
│                                                             │
│  .idiom/                                                    │
│    config.json              ← endpoint, workspace, MCP      │
│    backends.json            ← which local LLMs to launch    │
│    external_tools/*.json    ← project-specific tool adapters│
│    postflight/checks/*.json ← project-specific lint rules   │
│    prompts/                                                 │
│      lanes/<mode>/          ← optional preamble overrides   │
│      rules/                 ← optional rule overrides       │
│  .claude/  .codex/          ← project-only rules + skills   │
│  scripts/  src/  tests/     ← actual project code           │
│                                                             │
└─────────────────────────────────────────────────────────────┘
                              │ overlays
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Idiom binary (single Go executable)                         │
│                                                             │
│  Subcommands:                                               │
│    idiom init                                               │
│    idiom lane <mode> <brief>                                │
│    idiom plan-execute / implement   (existing low-level)    │
│    idiom probe lane                                         │
│    idiom backend start | proxy                              │
│    idiom brief lint | preload                               │
│    idiom audit enum-mirrors | …                             │
│    idiom skills install <name>                              │
│                                                             │
│  Embedded resources (go:embed):                             │
│    prompts/lanes/<mode>/{preamble,postamble,addendum}.md    │
│    prompts/rules/{loop-protocol,brief-skeleton,             │
│                   known-traps-generic,                      │
│                   orchestrator-workflow}.md                 │
│    prompts/skills/<name>/SKILL.md (templates)               │
│    postflight/rules-generic/*.json                          │
│    backends/profiles/<profile>.json                         │
│                                                             │
│  Always-loaded language tool families:                      │
│    cpp_*, go_*, python_*, ts_*, rust_*, java_*              │
│    (auto-detected per workspace; all available always)      │
└─────────────────────────────────────────────────────────────┘
```

## Components in detail

### `idiom init`

```
idiom init [<dir>]
```

Creates `.idiom/` in the target directory with:

```
.idiom/
├── config.json          (template — user fills in endpoint, root)
├── backends.json        (template — user uncomments models they have)
├── external_tools/
│   └── README.md        (explains the JSON adapter shape)
├── postflight/
│   └── checks/
│       └── README.md    (explains custom-rule format)
└── prompts/
    ├── lanes/
    │   └── README.md    (explains override pattern)
    └── rules/
        └── README.md
```

No `--language` flag, no per-language seeding. The runtime detects
languages from filesystem markers (the current `isGoWorkspace()` /
`isCppWorkspace()` paths in `manager.go`).

**Existing project that wants to upgrade:** `idiom init --merge` adds
any missing `.idiom/` files without overwriting existing config.

### `idiom lane`

```
idiom lane <mode> [--title T] [--coder] [--model M] [--mode-flag …] <brief.md>
```

Where `<mode>` is one of `plan-execute` (default), `implement`, or a
project-registered mode.

Replaces what `Colony/scripts/run_idiom_impl.sh` does today:

1. Compose the prompt: idiom's mode preamble + project preamble (if
   present) + brief body + project postamble (if present) + idiom's
   mode postamble.
2. Auto-route plan-execute to the configured coder model when
   `--model` isn't set (inheriting the rule we just shipped to
   Colony's script).
3. Invoke `idiom plan-execute` or `idiom implement` with the composed
   prompt and JSON event stream.
4. Capture the run log; on non-zero exit, run a harness audit and emit
   harness feedback automatically.
5. Forward the lane's exit code.

Colony's `run_idiom_impl.sh` becomes:

```bash
#!/usr/bin/env bash
exec idiom lane "${IDIOM_LANE_MODE:-plan-execute}" "$@"
```

A 3-line shim, kept only for backward compat with anything that calls
the script by path.

### `idiom backend`

```
idiom backend start [--profile <name>]
idiom backend proxy
idiom backend list
idiom backend stop [--profile <name>]
```

Reads `.idiom/backends.json`:

```json
{
  "profiles": {
    "qwen-pair": {
      "models": [
        {
          "alias": "qwen3.6-35b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3.6-35b",
          "port": 47780,
          "cache_size": 4,
          "cache_bytes": 21474836480,
          "max_tokens": 8192,
          "extra_args": ["--temp", "1.0", "--chat-template-args", "{\"enable_thinking\":true}"]
        },
        {
          "alias": "qwen3-coder-30b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3-coder-30b",
          "port": 47781,
          "max_tokens": 8192,
          "extra_args": ["--temp", "0.7", "--top-p", "0.8"]
        }
      ],
      "proxy": {
        "listen": 47680,
        "default_alias": "qwen3.6-35b"
      }
    }
  }
}
```

Idiom ships canonical profile templates for common stacks
(`qwen-pair`, `single-llama-cpp`, `mlx-single`). Projects can extend
with custom profiles.

`idiom backend proxy` is a Go port of `mlx_alias_proxy.py` (or shells
out to it for now); routes a single client port to multiple backends
by model alias.

### `idiom probe lane`

```
idiom probe lane [--mode plan-execute|implement] [--coder]
```

Synthetic-brief route probe. Asserts:

- `route_plan` is `initial=implement reason=explicit_task_family
  contract=implementation_result`.
- Plan-execute lanes route to `task_family=plan_then_execute_planner`.
- Implement lanes route to `task_family=implementation`.
- Allowed-tools surface is non-empty.

Replaces `Colony/scripts/idiom_route_probe.sh`.

### `idiom lane postflight`

```
idiom lane postflight [--diff <patch>] [--rules <rule-set>]
```

Runs a configurable check pipeline against a returned diff. Generic
checks ship in idiom; project-specific checks live under
`.idiom/postflight/checks/`.

Generic checks (always on):

- Unresolved `<<QUESTION>>:` markers in modified files (not just
  reports).
- File-allowlist mismatch (writes outside the brief's
  `required_edit_paths`).
- Narrating-comment heuristic on newly-added test code.

Project checks: each `<name>.json` describes a check:

```json
{
  "name": "doctest-check-stream",
  "kind": "ast_grep",
  "rule": "scripts/ast_grep_rules/doctest-check-stream.yml",
  "scope": "diff:added:test_*.cpp"
}
```

Or a shell-out:

```json
{
  "name": "fixture-binding",
  "kind": "shell",
  "command": "scripts/check_fixture_binding.sh",
  "args": ["--diff", "{diff}"]
}
```

### `idiom audit enum-mirrors`

```
idiom audit enum-mirrors --enum <Name> [--language cpp]
```

Generalizes `Colony/scripts/enum_mirror_audit.sh` using idiom's
existing language tools (`*_references`, `*_workspace_symbol`,
`*_read_symbol`). Lists every site that breaks if the enum changes:
array indices, switch cases, name tables, doctest expectations.

### Prompt stack — canonical + override

Idiom embeds canonical lane prompts under `prompts/lanes/<mode>/`:

```
prompts/lanes/plan-execute/preamble.md
prompts/lanes/plan-execute/postamble.md
prompts/lanes/plan-execute/addendum.md       (the system addendum we ship today)
prompts/lanes/implement/preamble.md
prompts/lanes/implement/postamble.md
prompts/lanes/atom-spec/preamble.md
prompts/lanes/atom-spec/postamble.md
```

And canonical rules under `prompts/rules/`:

```
prompts/rules/loop-protocol.md            (state machine)
prompts/rules/brief-skeleton.md           (drafting discipline)
prompts/rules/known-traps-generic.md      (anchor mismatch, structure-
                                           describing anchors,
                                           decomposition rules)
prompts/rules/orchestrator-workflow.md    (dual-architect loop)
```

Project overrides live at `<repo>/.idiom/prompts/{lanes,rules}/...`.
The lane runner concatenates idiom's preamble + project's preamble
(if present) + brief body. For rules, idiom's text is the base; the
project's text is appended unless a same-named project file replaces
it (declarative `replace: true` field for explicit replacement).

Colony deletes the rules that mirror idiom's canonical set, keeping
only the ones that are actually project-specific (coding standards,
cpp-safety, ears-specs, gdscript-patterns, save-load-safety,
test-helpers-ref, e2e-binding-tests, north-star,
glm-roadmap-execution, long-play-safety, integration-test-philosophy,
workflow, workflow-planning, tooling, opencode-mcp-guide).

### Skills

Idiom embeds skill templates under `prompts/skills/<name>/SKILL.md`.
`idiom skills install <name>` copies a template into
`<project>/.claude/skills/<name>/` (or `.codex/skills/<name>/` if
that's the convention the project follows).

The template contains placeholders for project context. The user is
expected to customize after install — skills are inherently
project-shaped.

## Plan / phasing

| Phase | What | Effort | Risk | Value |
|---|---|---|---|---|
| 1 | Lane architecture doc + `docs/lane-architecture.md` | 1-2h | none | unblocks everything |
| 2 | `idiom lane <mode>` subcommand absorbs `run_idiom_impl.sh` | 3-4h | low | most-used surface |
| 3 | Prompt-stack reorg — embed canonical, project override | 3-4h | medium | kills duplication |
| 4 | `idiom backend start|proxy` + `backends.json` schema | 2-3h | low | new-project unblock |
| 5 | `idiom lane postflight` + pluggable rules | 2-3h | medium | reusable QC |
| 6 | `idiom probe lane` + `idiom audit enum-mirrors` + `idiom brief preload` | 2-3h | low | covers remaining scripts |
| 7 | `idiom init` + `idiom skills install` | 1-2h | none | greenfield onboarding |
| 8 | Colony shim cleanup — delete generic rules + scripts that idiom now provides | 2h | low | proves the migration |
| **Total** | | **16-23h** | | |

### Suggested order

1 → 2 → 3 → 5 → 4 → 6 → 7 → 8

Phase 1 first because docs unblock the rest. Phase 2 because the lane
wrapper is the most-used surface. Phase 3 because that's where the
biggest copy-paste tax is today. Phase 5 because postflight ships next
on the user-facing path.

Phases 4, 6, 7 are project-onboarding wins; the existing Colony
plumbing already works for Colony so they aren't blockers.

Phase 8 (Colony cleanup) is the proof-of-migration: delete the files
in Colony that idiom now provides and confirm the lane still runs
clean.

## Open questions

### How does the prompt overlay merge work?

Two reasonable models:

1. **Strict concatenation:** idiom's preamble first, project's
   appended. Simple. Project can override by prepending an explicit
   "Ignore the above" sentence (model-side discipline) or by setting
   `replace: true` in a YAML frontmatter block.
2. **Section-keyed merge:** both files use a section format (e.g.
   `## Hard rules`, `## Closeout`); the merger interleaves matching
   sections. More elegant; harder to maintain.

**Recommendation:** start with strict concatenation; add section
keying only if real overlap problems emerge.

### Where do canonical resources live in the binary?

`go:embed` is the cleanest path: the prompts and templates ship as
embedded files in the binary. No filesystem dependency on the idiom
install location, no path-resolution bugs, no "I have two idioms,
which prompts won?" issues.

The embed live at compile time under `idiom/embed/`. The Go side has
helpers like `embedlib.LoadLanePreamble("plan-execute")` that walk
the project override path first, fall back to embed.

### Versioning

Idiom releases with a version tag. Project's `.idiom/config.json`
optionally carries a `min_idiom_version` field. The runner warns at
start when the running idiom is older than the project's pinned
minimum.

For prompt-stack changes that need migration (rare), idiom can ship
a `idiom prompts migrate` subcommand that walks the project's
`.idiom/prompts/` and either auto-applies known migrations or
flags conflicts.

### Backend launcher: shell out vs reimplement

The current Colony scripts (`start_local_qwen.sh`,
`mlx_alias_proxy.py`) are short and reliable. Phase 4's `idiom
backend` could:

- **Wrap** the existing Python proxy and the bash launcher (just
  invoke them via `os.exec`). Lowest cost, keeps ownership in the
  scripts.
- **Reimplement in Go.** Pure idiom-native, no Python dependency
  at runtime, single binary distribution.

**Recommendation:** start by wrapping. Reimplement in Go only if a
real reason emerges (Windows support, single-binary distribution
requirement).

### What about the dual-architect (Claude orchestrator) loop?

The orchestrator runs in Claude Code (or similar), feeds briefs to
idiom, watches output. That loop's "smarts" — when to dispatch,
when to retry, when to escalate — lives in the orchestrator's
SKILL.md files today. Should it move into idiom?

**Recommendation:** no, not yet. The orchestrator is the *consumer*
of idiom; idiom should stay an executor. Encoding the orchestrator's
loop in idiom would couple the two unnecessarily and re-create the
problem we just solved (Colony-side specific behavior accumulating
in idiom). Skills templates (Phase 7) are the right level of
sharing — copy-paste with project customization.

## Success criteria

After all phases, a fresh project's onboarding looks like:

```bash
cd ~/projects/new-project
idiom init                       # creates .idiom/ skeleton
$EDITOR .idiom/config.json       # set endpoint + workspace root
$EDITOR .idiom/backends.json     # uncomment models you have
idiom backend start              # launches local LLMs
# author a brief at .planning/atom-001.md
idiom brief lint .planning/atom-001.md
idiom lane plan-execute --title atom-001 .planning/atom-001.md
```

Project-side files reduce to the directory shown in the architecture
diagram. Everything else lives in idiom.

The Colony repo (Phase 8) shrinks by:

- ~25 generic shell scripts deleted
- ~6 rule docs (the canonical-equivalent set) deleted
- `run_idiom_impl.sh` → 3-line shim
- Project-specific scripts (lookup.sh, trace_code.sh, etc.) keep
  their existing JSON adapters under `.idiom/external_tools/`,
  unchanged

## Migration safety

Each phase is shippable independently. Old paths keep working:

- Phase 2 (`idiom lane`) ships alongside the existing `idiom
  plan-execute` / `idiom implement` subcommands; the wrapper just
  composes a prompt and calls them.
- Phase 3 (prompts) merges project overlays on top of idiom embeds;
  if project files are absent, idiom uses embeds; if both are
  present, behavior matches today's Colony override pattern.
- Phase 8 is the only "delete from Colony" phase, and it runs after
  every replacement is shipped and verified.

## What's already done in this session

- `idiom plan-execute` subcommand (full lane orchestration)
- Pre-execute plan validator (atomic preflight)
- JSON event stream for phase 2 (`plan_artifact_loaded`,
  `executor_edit`, `executor_complete` with `duration_ms`, etc.)
- Step-2 short-circuit for plan-execute mode
- Brief lint (`idiom brief lint`) with anchor-specificity and
  structure-describing-anchor rules
- Read-symbol pattern validator at the workspace dispatch layer
- `idiom delegate_edit` tool for tool-level model handoff
- Six runner-side gates that handle plan-execute correctly
- The mode flag in `Colony/scripts/run_idiom_impl.sh` (auto-routes
  plan-execute to the coder, mode-aware preambles)

This is roughly Phase 0 — the lane shape works end-to-end, and Colony
exercises it. The 16-23h of work above lifts the rest of the
plumbing into idiom.
