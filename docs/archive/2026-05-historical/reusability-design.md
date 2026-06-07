# Reusability Design — Lift the Plan-Execute Workflow into Idiom

**Status:** Design draft, 2026-05-09 (Phases 1-5 + 4b + 4c + 5b + 6 + 6.5 + 7 shipped 2026-05-09 / 2026-05-10; Phase 3 rules-embed backfill shipped 2026-05-10 after the post-Phase-6 audit below; Phase 8 pending; plug-and-play Phase 9 partial ship 2026-05-10 — `idiom version`, goreleaser config, release workflow, install.sh, three-tier Docker install test (`make test-install-docker`: 14-subcommand surface + 3 static-functionality tests + stub-LLM lane test, all green), and the `internal/platform/autodetect` library landed parallel to canonical Phase 7/8; Phase 11 partial ship — `idiom doctor` landed; remaining 9/10/11 deliverables and Phase 12 unscheduled)
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
in `internal/capabilities/workspace/manager.go`). The change is making the
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
  "default_profile": "qwen-pair",
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

`idiom backend proxy` is a full Go port of `mlx_alias_proxy.py` that
routes a single client port to multiple backends by model alias,
rewrites the `model` field to the absolute path mlx_lm.server expects,
merges multiple system messages into one leading entry, and streams
responses chunk-by-chunk for SSE compatibility.

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
| 5b | `idiom lane <mode> --postflight` auto-invokes Phase 5 against `git diff` after the lane returns | 1-2h | low | wires QC into the loop |
| 6 | `idiom probe lane` + `idiom audit enum-mirrors` + `idiom brief preload` | 2-3h | low | covers remaining scripts |
| 7 | `idiom init` + `idiom skills install` + canonical `prompts/profiles/{coding,claude-qwen-worker}.md` templates + `prompts/skills/orchestrator-workflow/SKILL.md` (deferred from Phase 3) | 2-3h | low | greenfield onboarding |
| 8 | Colony shim cleanup — delete generic rules + scripts that idiom now provides; deletion gated on `idiom probe lane` plus a full plan-execute lane against a known-good fixture brief | 2-3h | low | proves the migration |
| **Total** | | **18-26h** | | |

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

## Holes identified in 2026-05-10 audit

A review against the actual code state (`cmd/idiom/lane.go` + the
embedded files under `cmd/idiom/lanes/`) found gaps between what the
design described as "shipped" and what the completed phases actually
delivered. This section enumerates them so the roadmap reflects
reality.

### Phase 3 — partially shipped (rules-embed backfill)

The Phase 3 architecture diagram lists both `prompts/lanes/<mode>/...`
AND `prompts/rules/{loop-protocol, brief-skeleton,
known-traps-generic, orchestrator-workflow}.md` as embedded resources.
Only the lane preambles/postambles actually shipped — the canonical
rules were never embedded.

**Backfill (this session):** embed three canonical generic rule files
(`loop-protocol`, `brief-skeleton`, `known-traps-generic`) at
`cmd/idiom/lanes/rules/<name>.md` and prepend them to the lane
wrapper's composed prompt. Project overlay at
`<root>/.idiom/prompts/rules/<name>.md` with same-name semantics:
matching project files replace the canonical entry, extras append after
the canonical block in alphabetical order. **Status: shipped 2026-05-10.**

**Deferred — `orchestrator-workflow.md`:** the design's own
"What about the dual-architect orchestrator loop?" answer recommends
keeping the orchestrator out of idiom and sharing via skill templates.
Embedding orchestrator-workflow as an executor-facing rule contradicts
that recommendation — the executor doesn't need it. Move this content
to `prompts/skills/orchestrator-workflow/SKILL.md` when Phase 7 ships
skill templates (now listed under Phase 7 in the table).

### Phase 5b — lane → postflight integration (NEW)

`idiom lane postflight` ships as a standalone subcommand. There is no
flag on `idiom lane <mode>` that auto-runs postflight after the lane
returns; the success-criteria onboarding flow doesn't show postflight
at all. Result: project orchestrators have to wire postflight
separately, and many won't.

**Proposed:** a new Phase 5b adds `idiom lane <mode> --postflight` that
runs the lane normally, then invokes `runLanePostflight` against
`git diff` (or a configurable base ref) before returning. Final exit
is the worse of (lane exit, postflight exit). Pending.

### Phase 7 — additional sub-items

The original Phase 7 description covered only `idiom init` +
`idiom skills install`. The audit identified two additional
Phase-7-shaped items, now folded into the table row:

1. **Canonical profile templates** — Colony's
   `prompts/profiles/coding.md` and `prompts/profiles/claude-qwen-worker.md`
   have no canonical idiom equivalent. New projects pointing
   `chat.system_prompt_files` at `.idiom/prompts/profiles/coding.md`
   today are pointing at a file they have to author from scratch.
   Embed them as templates copied during `idiom init`.

2. **Skill templates expanded** — alongside whatever skills ship with
   `idiom skills install`, include the canonical
   `orchestrator-workflow` deferred from Phase 3.

### Phase 8 — verification gate (NEW sub-item)

Phase 8 says "delete generic rules + scripts that idiom now provides
and confirm the lane still runs clean." That confirmation was
informal. Before deletion, the gate is:

1. `idiom probe lane --mode plan-execute` and `--mode implement`
   return clean.
2. A full plan-execute lane against a known-good fixture brief (e.g.
   the donkey-atom replay under `testdata/swe/`) completes green.
3. Each canonical rule referenced in the design (3 embedded + 1
   deferred to skills, see Phase 3 entry above) is surfaced where
   expected: rules appear in the composed lane prompt;
   orchestrator-workflow is reachable via `idiom skills install`.

Recorded here so a future Colony-deletion PR can't run ahead of the
canonical replacement.

### Open question — base prompt ownership

`docs/lane-architecture.md` documents that the chat-side base prompt
(loaded via `chat.system_prompt_files`) is fully project-owned today: a
project's `.idiom/prompts/base.md` is the entire base, with no
canonical idiom layer. The north-star drift recommendation says
"Base/profile prompts own general working style," which implies a
centralized authority. These two views aren't reconciled.

Options:

1. **Status quo** — projects own `base.md` fully; idiom updates
   working-style guidance only via canonical rules (now embedded) and
   lane preambles. Pro: clean ownership, no new overlay slot. Con:
   projects drift independently on stable working-style guidance.
2. **Canonical base + project overlay** — symmetric to lanes/rules:
   embed `prompts/base.md` in the binary, project file at
   `.idiom/prompts/base.md` extends or replaces. Pro: idiom updates
   propagate. Con: another overlay slot to design and test.

**Recommendation pending.** The rules embed (above) covers most of
the "shared working style" need; if real drift problems emerge after
Phase 7 ships profile templates, revisit option 2.

### Deferred — `brief-preflight.py` Go port

`idiom brief lint` shells out to an embedded Python script at runtime
(`cmd/idiom/brief-preflight.py`). The "single Go binary distribution"
pitch under the "Backend launcher" open question doesn't account for
this Python dependency. Schedule a Go port in a later phase; not
blocking for project onboarding.

### Deferred — atom-spec lane subcommand

`docs/lane-architecture.md` lists three lanes (`implement`,
`plan-execute`, `atom-spec`), but the reusability plan only schedules
the first two. `atom-spec` drafting still rides on the low-level
`idiom artifact --no-tools` command. Schedule `idiom atom-spec draft`
(with the same canonical preamble/postamble + project overlay seam)
when Phase 7 brings the rest of the brief-authoring loop into idiom.

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

## Beyond Phase 8 — Plug-and-Play Roadmap

Phases 1-8 + 5b finish the "lift Colony plumbing into idiom" story: a
project that authors `.idiom/config.json`, `.idiom/backends.json`, and
a brief can run plan-execute lanes against any local LLM stack without
copying scripts. That's a developer-grade onboarding — appropriate for
engineers who already know what idiom is.

To get to a true install-and-run experience — `brew install idiom`,
`cd ~/projects/foo && idiom start`, run lanes — three more layers are
needed: distribution, zero-config init, and lifecycle. Operator
observability rounds out the experience. An optional thin orchestrator
closes the brief-authoring blank-page gap for fresh-install users who
don't have an orchestrator yet.

These phases are explicitly *outside* the "lift Colony plumbing"
charter of Phases 1-8; they are the path from "harness for engineers
who already know what idiom is" to "tool a fresh user can install and
use." Schedule them only after Phase 8 closes the migration loop and
Colony validates the lifted plumbing.

| Phase | What | Effort | Risk | Value |
|---|---|---|---|---|
| 9 | Distribution: goreleaser, brew formula, install.sh, signed darwin builds, `idiom version`/`idiom upgrade`, `idiom models pull` | 6-10h | medium | gates every other plug-and-play phase |
| 10 | Zero-config `idiom init --auto`: scan backends, models, workspace; single `.idiom/idiom.toml`; user-level config at `~/.config/idiom/` | 4-6h | low | turns Phase 7 from "templated skeleton" into "first-run works" |
| 11 | Lifecycle: `idiom start`/`idiom stop`/`idiom status`/`idiom doctor`/`idiom service install`; unified `idiom logs`; `idiom usage` | 4-6h | low (medium for `doctor`) | one-command runtime, recoverable failures, broken is easy to diagnose |
| 12 | Optional thin orchestrator — `idiom run "<task>"` chaining atom-spec draft → brief lint → lane → postflight | 3-5h | medium | closes the brief-authoring gap if appetite exists; skip leaves the orchestrator-stays-consumer recommendation intact |
| **Plug-and-play subtotal** | | **17-27h** (excl. Phase 12: 14-22h) | | |

### Phase 9 — Distribution

Until the binary is reachable to non-Go-developers, nothing else
matters. Goal: `brew install idiom` on macOS, `curl
https://idiom.sh/install.sh | sh` elsewhere, and signed releases on
GitHub.

**Status (2026-05-10):** core release pipeline shipped — `.goreleaser.yaml`,
`.github/workflows/release.yml`, `install.sh`, `idiom version` with
`-ldflags`-injected build metadata. Pending: macOS signing/notarization
(needs Apple Developer credentials provisioned in CI secrets), Homebrew
tap (needs the `idiom-sh/tap` repo), `idiom upgrade` self-update
subcommand, `idiom models pull` model fetcher.

Deliverables:

- **Release pipeline** ✓ — `.goreleaser.yaml` + `.github/workflows/release.yml`
  build `darwin-{arm64,amd64}`, `linux-{amd64,arm64}`, `windows-amd64`
  on tag push. Generates `SHA256SUMS` for `install.sh` to verify.
- **Homebrew formula** — `brew install idiom` from a tap (likely
  `idiom-sh/tap`) until criteria for homebrew-core are met. Pending
  the tap repo creation; goreleaser's `brews:` section wires in once
  it exists.
- **Install script** ✓ — `install.sh` at the repo root resolves the
  latest release, downloads the right asset for the host, verifies
  the SHA256 checksum, drops the binary in `~/.local/bin` (or
  `/usr/local/bin` if writable). POSIX sh; tested syntax-clean with
  `sh -n`. Carries an `IDIOM_RELEASE_URL_BASE` override so the Docker
  install test can point it at a local server.
- **Docker install test** ✓ — `scripts/test-install-docker.sh` (host
  orchestrator) + `scripts/test-install-entrypoint.sh` (in-container)
  cross-build idiom for the docker arch, package the binary in
  goreleaser archive format, serve the tarball + SHA256SUMS via a
  local `python3 -m http.server` inside an `ubuntu:24.04` container,
  run `install.sh` against the local URL, then run a three-tier
  assurance pass:

  - **Tier 1 — surface coverage:** all 14 subcommands listed in the
    main.go dispatch table (`version`, `doctor`, `lane`, `backend`,
    `brief`, `plan-execute`, `implement`, `artifact`, `context`,
    `architect`, `audit`, `ranker`, `probe`, `tool`) respond to
    `--help` with exit 0 or 2 and non-empty output. Catches
    dispatch-table regressions and missing-flag panics.
  - **Tier 2 — static functionality (no LLM):** `idiom brief lint`
    against a fixture brief (Python brief-preflight integration),
    `idiom lane postflight` against a fixture diff (Phase 5
    pipeline), `idiom backend list` against a fixture
    `backends.json` (Phase 4 resolver). Each asserts a
    meaningful exit code and content.
  - **Tier 3 — stub-LLM lane:** a Python mock chat-completions
    server returns canonical OpenAI-shaped responses; idiom lane
    implement runs against a workspace whose `.idiom/config.json`
    points at the mock; the assertion is that the mock received at
    least one POST during the run. Proves the lane runner — config
    load, prompt composition, model dispatch — is intact end-to-end
    after install. A no-op `llama-server` stub at
    `/usr/local/bin/llama-server` satisfies the auto-start path so
    the readiness check completes against the mock's listening port.

  Wrapped by `make test-install-docker`. A passing run means a fresh
  user installing via `install.sh` gets a binary whose entire
  reachable surface — every subcommand, the static QC pipelines, and
  the lane runner up to the LLM dispatch — actually works. The
  remaining unchecked layer is real-LLM lane completion (edits land,
  postflight passes), which would require pulling a real model in CI.
- **macOS signing + notarization** — Developer ID code signature on
  darwin assets so Gatekeeper accepts them without right-click.
  Pending Apple Developer credentials in CI secrets; wire `gon` /
  `quill` step into `release.yml` once provisioned.
- **`idiom version`** ✓ — prints version, commit, build date, Go
  toolchain version, and host os/arch. Build-time metadata via
  `-ldflags '-X main.{version,commit,date}=...'`; falls back to
  `debug.ReadBuildInfo()` for `go install` builds without ldflags.
- **`idiom upgrade`** — fetches latest release and self-replaces.
  Pending; can ship after the first tagged release exists.
- **`idiom models pull <alias>`** — wraps `huggingface-cli download`
  (or equivalent) to fetch a model file into the project-configured
  cache dir. Model files are too large to ship in the binary; this is
  the boundary. Resolves alias via `.idiom/backends.json`. Pending.
- **Backend dependency posture** — `idiom doctor` (Phase 11) verifies
  `mlx_lm.server` (or `llama-server`, `ollama`) is on `$PATH`. The
  Python dependency is acknowledged and documented. Go-porting model
  serving is non-goal: `mlx_lm.server` and `llama.cpp` are
  research-velocity codebases and shipping a parallel implementation
  is not worth tracking.

### Phase 10 — Zero-config init

Phase 7's `idiom init` ships a templated skeleton. For plug-and-play,
init must *discover*, not template. A fresh user runs `idiom init
--auto` and the `.idiom/` directory comes out wired to whatever they
actually have installed.

**Status (2026-05-10):** the `internal/platform/autodetect` library landed
parallel to canonical Phase 7. It exposes `DetectBackends()` (scans
`$PATH` for `mlx_lm.server`, `llama-server`, `ollama`) and
`DetectModels()` (walks `~/.cache/huggingface/hub`, `~/models`,
`~/.ollama/models/manifests` and surfaces normalized aliases). Phase 7
will wire this into `idiom init --auto`; until then `idiom doctor`
already consumes the library to tell users what was found.

Deliverables:

- **Auto-detect backends** — scan `$PATH` for `mlx_lm.server`,
  `llama-server` (llama.cpp), `ollama`. Generate the `backends.json`
  profile from what's actually installed; one runner means one
  profile, multiple means multiple.
- **Auto-detect models** — walk `~/.cache/huggingface/`, `~/models/`,
  `~/.ollama/models`. Pick a default executor by tags + size (smallest
  coder ≥ 7B) and a default planner if a non-coder model is present.
  Surface the picks; user can override interactively.
- **Auto-detect workspace** — `cwd` becomes `workspace.root`; the
  language registry (`internal/capabilities/workspace/language_registry.go`)
  already does the rest.
- **Single config file** — collapse `.idiom/config.json` and
  `.idiom/backends.json` into `.idiom/idiom.toml` (TOML is easier for
  hand-editing; JSON also acceptable). Maintain backward
  compatibility: the loader reads `.idiom/idiom.toml` if present, else
  falls back to the legacy two-file split. Existing two-file projects
  keep working.
- **User-level config** — lookup order:
  `<repo>/.idiom/idiom.toml` > `~/.config/idiom/idiom.toml` >
  embedded defaults. Project files override per-key, not whole-file.
  A user with one local-LLM setup configures backends *once* at the
  user level and every project inherits.
- **`idiom init --interactive`** — interactive flow for the 3-4
  things autodetect can't infer (executor model choice when multiple
  match, write enable, MCP servers, project name).
- **Sample brief** — init drops `.planning/example-atom.md` so a fresh
  user has something to lane against end-to-end.

### Phase 11 — Lifecycle + observability

Today: `idiom backend start`, then per-task `idiom lane`. Plug-and-play
needs one entry, supervision, and a clear way to diagnose failures.

**Status (2026-05-10):** `idiom doctor` shipped with checks for
backend runners, local models, project config, workspace writability,
python3 (warn), and git (warn). Each non-pass check carries an exact
remedy line; `--json` emits a machine-parseable summary for CI / dashboard
consumers. Pending: `idiom start`/`stop`/`status` umbrella subcommands,
`idiom service install` for launchd/systemd unit generation,
`idiom logs <session>` unified reader, `idiom usage` token/cost reporter.

Deliverables:

- **`idiom start`** — launches backends if not already up, warms
  caches, prints a ready signal, optionally drops into a TUI or stays
  foreground supervising. Idempotent: if backends are already running,
  it re-attaches.
- **`idiom stop`** — clean teardown of everything `idiom start`
  brought up. SIGTERM with grace, SIGKILL on timeout (already the
  Phase 4b shape; lift to umbrella).
- **`idiom status`** — live state: which backends are up on which
  ports, executor alias, last lane outcome, queue depth if a TUI is
  running.
- **`idiom doctor`** ✓ — preflight diagnostic. Checks backend
  runners on PATH, local model presence, project config validity,
  workspace writability, python3 + git availability. Each non-pass
  check carries an exact remedy line; `--json` emits machine-parseable
  output. Future expansion: port-collision detection (overlap with
  `idiom backend status`).
- **`idiom service install`** — generates `launchd` (macOS) /
  `systemd` (Linux) units for backend supervision and
  restart-on-crash. Optional; users running `idiom start` foreground
  don't need it.
- **`idiom logs <session>`** and **`idiom logs tail`** — unified
  reader over the three streams documented in
  `docs/lane-architecture.md` (conversation log, stdout JSONL,
  telemetry stream). Selects events by role/type rather than asking
  users to grep three files.
- **`idiom usage`** — tokens/cost per lane / session / day. The
  `model_session` telemetry events already carry the data; this is a
  reader on top.

### Phase 12 — Optional: thin orchestrator

The design's existing recommendation (under "What about the
dual-architect orchestrator loop?") keeps the orchestrator
consumer-side. That stays correct for projects with a real
orchestrator (Colony, etc.). But fresh-install users have no
orchestrator — they have idiom, a brief template, and a blank page.

To close that gap *without* re-coupling idiom to a specific
orchestrator shape, ship a *thin* opinionated entry:

- **`idiom run "<natural-language-task>"`** — drafts a brief via the
  atom-spec lane (currently `idiom artifact --no-tools`, scheduled in
  the audit deferrals as `idiom atom-spec draft`), lints it via
  `idiom brief lint`, runs the chosen lane (default plan-execute),
  runs `idiom lane postflight`, and reports a single result.
- The thin orchestrator does NOT carry retry / re-route / escalation
  smarts — those stay in dual-architect skill templates (Phase 7).
  Failures surface as exit codes and diagnostic output; the user
  decides what to do next.

Status: optional. Ship only if Phase 11 lands cleanly and there's
appetite for an opinionated entry. Skipping leaves the design's
existing orchestrator-stays-consumer recommendation fully intact.

### Out of scope for the plug-and-play roadmap

Listed here so they don't leak into a phase by accident:

- **IDE plugins** (VS Code, JetBrains) — value-add but a separate
  product surface; depends on a stable CLI contract that Phases 1-12
  establish first.
- **Web UI / dashboard** — the CLI + TUI cover operator UX; a web
  layer is over-investment until usage justifies it.
- **Telemetry export to Grafana/Datadog/etc.** —
  `logs/telemetry/*.jsonl` is the structured data; ship import
  recipes for popular tools as docs, not a daemon.
- **Go-porting model servers** — `mlx_lm.server`, `llama.cpp`,
  `ollama` are the right boundary; idiom shells out via the
  alias-routing proxy.
- **Secret management beyond filesystem permissions** — `.idiom/`
  config can carry endpoint tokens but idiom does not encrypt or
  vault them. Projects with stricter requirements integrate their own
  secret store and pass values via env.

### Suggested order

Phase 9 is the gate — without it, no fresh user can install, and the
remaining phases produce code paths nobody runs. Phase 10 follows
because zero-config is the second largest plug-and-play win once the
binary is reachable. Phase 11 lands next because it converts "has a
binary, has config" into "is actually running." Phase 12 is optional
and can ship later (or never) depending on whether fresh-install
users without orchestrators are a real audience.

```
9 → 10 → 11 → (12 optional)
```

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
