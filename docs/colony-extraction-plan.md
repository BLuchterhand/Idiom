# Colony → Idiom Extraction Plan

The plan-execute lane is currently driven by a mix of:

- **Idiom-side machinery** (already generic): `idiom plan-execute`,
  `idiom implement`, `idiom brief lint`, `idiom architect`, the runner,
  workspace tools, manifest, gates.
- **Colony-side scripts and configs** (project-specific today, but
  much of it should be generic): the lane wrapper, postflight checker,
  backend launcher, MLX proxy, route probe, prompt profiles, brief
  skeleton/traps docs, atom-spec drafting lane.

This document inventories what's where, classifies each piece by how
generic it is, and proposes a phased extraction so the workflow is
reusable across projects with minimal copy-paste.

## Inventory

### Already in idiom (no change needed)

These are first-class subcommands; any project pointing `.idiom/config.json`
at idiom can use them today:

- `idiom plan-execute` — planner phase + executor phase orchestration
- `idiom implement` — implement-lane runner
- `idiom brief lint` — brief preflight
- `idiom architect` — packet/lint/prompt envelope
- `idiom artifact` — no-tools answer generator
- `idiom tool <name>` — per-tool dispatch
- `idiom context build|status|query|zoom|pack|impact|trace_pack` — context graph
- `idiom audit` / `idiom ranker` — telemetry-side analysis
- Workspace tools (read/edit/search/code-navigate/discovery/external-tool registry)
- MCP integration (server discovery, tool routing)

### Colony scripts that should move into idiom (generic potential)

These are scripts in `Colony/scripts/` that aren't actually
Colony-specific — they orchestrate idiom or provide LLM infrastructure
that any project would want. The migration target is in parens.

| Colony file | What it does | Migration target |
|---|---|---|
| `run_idiom_impl.sh` | Lane wrapper: builds prompt from brief, picks mode/model, invokes `idiom implement` or `idiom plan-execute`, captures run log, runs harness audit/feedback on failure | `idiom lane <mode> <brief>` (new subcommand wrapping the existing flow + brief-preamble injection + post-run telemetry) |
| `run_idiom_atom_spec.sh` | Atom-spec drafting lane: tells idiom to draft a planner brief without tools, lints with `atom_spec_lint.py` | `idiom atom-spec draft <prompt-context>` |
| `idiom_route_probe.sh` | Sanity probe: runs a synthetic brief through the implement lane and asserts routing produces `initial=implement reason=explicit_task_family` | `idiom probe lane [--mode plan-execute\|implement]` |
| `lane_postflight.sh` | Validates a returned diff: scans for `<<QUESTION>>:` in modified files, runs ast-grep rules, checks helper visibility, warns on narrating comments | `idiom lane postflight --diff <patch>` (rules pluggable per project via `.idiom/postflight.json`) |
| `start_local_qwen.sh` / `start_local_minimax.sh` | Launches `mlx_lm.server` for local models with the right ports/cache/template args | `idiom backend start [--profile <name>]` reading `.idiom/backends.json` |
| `mlx_alias_proxy.py` | Routes a single client port to multiple MLX backends by model alias | `idiom backend proxy` (Go port; current Python keeps working) |
| `atom_spec_lint.py` | Lints atom-spec drafts for required sections, anchors, etc. | Already partially in `cmd/idiom/brief-preflight.py` — merge the rules |
| `qwen_brief_preload.sh` / `atom_spec_preload.sh` | Inline preloaded discovery output into a brief's "Verified current state" section | `idiom brief preload --kind <kind> --out <brief.md>` |
| `enum_mirror_audit.sh` | Walks a C++ enum's references and lists every mirror site (array indices, switch cases, name tables) that breaks if the enum changes | `idiom audit enum-mirrors --enum <Name>` (generic over the language tools idiom already has) |

### Colony scripts that stay in Colony (project-specific)

These wrap Colony-specific scripts that produce Colony-specific output.
The wrapping pattern (`.idiom/external_tools/<name>.json`) is already
generic — the implementations belong to Colony.

- `lookup.sh`, `trace_code.sh`, `ast_grep.sh`, `boundary.sh`,
  `state_machine.sh`, `struct_shape.sh`, `affected_suites.py`,
  `fixture_inventory.sh`, `gdscript_trace.sh`, `id_ranges.sh`,
  `save_coverage.sh`, `spec_trace.sh`, `validate_discovery_tools.py`,
  `api_access.sh`, `ask.sh`, `dead_symbols.py`, `clangd_query.py`,
  `extract_body.py`, `include_graph.py`, `compress-discovery.sh`,
  `codebase_digest.sh`, `generate_*.py`, `sim_*.py` — domain-specific
  to Colony's C++ / Godot / scenario simulation work.
- `.idiom/external_tools/*.json` — JSON adapters that bind those
  scripts to idiom's tool surface. The schema is generic; the rows
  are Colony-specific.

### Colony rules / prompts (mixed: extract generic core, leave project layer)

| File | Status | Plan |
|---|---|---|
| `.claude/rules/qwen-loop-protocol.md` | Mostly generic | Move canonical version to `idiom/prompts/lanes/`. Project layer adds project-specific traps. |
| `.claude/rules/qwen-brief-skeleton.md` | Mostly generic (we just edited this) | Same — canonical in idiom. |
| `.claude/rules/qwen-known-traps.md` | Mixed | Split into `idiom/prompts/lanes/known-traps-generic.md` (anchor mismatch, structure-describing anchors, decomposition rules, etc.) and a project-side `known-traps-colony.md` (doctest CHECK<<, fixture binding, GDScript patterns). |
| `.claude/rules/orchestrator-workflow.md` | Generic state machine | Move to idiom as `prompts/orchestrator-workflow.md` if it describes the dual-architect loop generically. |
| `.claude/rules/coding-standards.md`, `cpp-safety.md`, `ears-specs.md`, `gdscript-patterns.md`, `save-load-safety.md`, `test-helpers-ref.md`, `e2e-binding-tests.md`, `north-star.md`, `glm-roadmap-execution.md`, `long-play-safety.md`, `integration-test-philosophy.md`, `workflow.md`, `workflow-planning.md`, `tooling.md`, `opencode-mcp-guide.md` | Project-specific | Stay in Colony. |
| `.claude/skills/orchestrate/SKILL.md` | Generic shape with project-specific bindings | Idiom ships a template; projects copy and customize. |
| `.codex/*` | Codex-side mirrors of `.claude/*` | Same split — generic mirrors live in idiom, project overrides in Colony. |
| `.idiom/prompts/base.md` | Project-side override of idiom's base prompt | Stay in Colony (project owns its base prompt). |
| `.idiom/prompts/profiles/coding.md`, `claude-qwen-worker.md` | Mixed — some Colony-specific tool guidance, most generic | Idiom ships canonical generic versions; projects override only what's actually different. |

### Colony state / data (stays in Colony)

- `.idiom/discovery/*` — workspace-specific discovery cache
- `.idiom/comprehension/*` — context graph for Colony
- `.idiom/plans/*` — generated plan artifacts
- `.idiom/logs/conversations/*` — conversation logs
- `.planning/idiom-briefs/*` — atom briefs (project-specific)
- `logs/telemetry/*` — telemetry stream

## Phased extraction

### Phase 1 — Document the seam (1-2 hours)

Write `idiom/docs/lane-architecture.md` describing the lane shape,
gate sequence, and project-side hooks (`.idiom/config.json`,
`.idiom/external_tools/*.json`, `.idiom/prompts/`). This is just
docs; nothing moves yet. The point is to have one canonical answer to
"what does idiom give me, what do I bring."

### Phase 2 — Lift the lane wrapper into idiom (3-4 hours)

- `idiom lane <mode> [--title T] [--coder] [--model M] <brief.md>`
  absorbs `run_idiom_impl.sh`. It:
  - Wraps the brief with the mode-specific preamble/postamble
    (loaded from `idiom/prompts/lanes/<mode>/{preamble,postamble}.md`,
    overridable via project's `.idiom/prompts/lanes/<mode>/`).
  - Auto-routes plan-execute to coder by default.
  - Invokes `idiom plan-execute` or `idiom implement` with the
    composed prompt.
  - Captures the run log, post-run grep, harness feedback on failure.
- Migrate `run_idiom_atom_spec.sh` → `idiom atom-spec draft`.
- Migrate `idiom_route_probe.sh` → `idiom probe lane`.

Colony's `run_idiom_impl.sh` becomes a 3-line shim that calls
`idiom lane plan-execute "$@"`.

### Phase 3 — Backend launcher + proxy (2-3 hours)

- `idiom backend start [--profile <name>]` reads
  `.idiom/backends.json` (a list of `{model, port, args, cache}` entries)
  and launches each `mlx_lm.server` instance. Defaults shipped in
  idiom; projects can extend.
- `idiom backend proxy` runs the alias router (or just keeps using
  the Python proxy). Optional Go port.
- Move `start_local_qwen.sh` and `start_local_minimax.sh` content
  into the launcher's defaults; keep the shell scripts as 1-liners
  for backward compat.

### Phase 4 — Postflight + audit (2-3 hours)

- `idiom lane postflight --diff <patch>` runs a configurable check
  pipeline. Generic checks (unresolved `<<QUESTION>>:` markers in
  diffs, narrating-comment heuristic, file-allowlist mismatch) ship
  in idiom. Project-specific checks (doctest CHECK<<, fixture binding,
  duplicate-order detection) live in
  `.idiom/postflight/checks/<name>.json` with `command` + `match`
  fields, similar to `external_tools`.
- `idiom audit enum-mirrors --enum <Name>` generalizes
  `enum_mirror_audit.sh` using idiom's existing language tools
  (`cpp_references`, `cpp_workspace_symbol`, etc.). Works for any
  language with reference support.

### Phase 5 — Prompt-stack reorganization (3-4 hours)

- Idiom ships canonical lane prompts under `idiom/prompts/lanes/`:
  - `plan-execute/{preamble,postamble,addendum}.md`
  - `implement/{preamble,postamble,addendum}.md`
  - `atom-spec/{preamble,postamble}.md`
- Idiom ships canonical generic rules under `idiom/prompts/rules/`:
  - `loop-protocol.md`, `brief-skeleton.md`, `known-traps-generic.md`,
    `orchestrator-workflow.md`.
- Loader logic: idiom's lane runner reads
  `<idiom-install>/prompts/lanes/<mode>/preamble.md` first, then
  appends `<repo>/.idiom/prompts/lanes/<mode>/preamble.md` if
  present (project-side override or extension).
- Same for rules (`prompts/rules/...` + project-side
  `.idiom/prompts/rules/...`).
- Colony deletes the generic-equivalent rules from `.claude/rules/`
  and `.codex/rules/` and keeps only project-specific traps + skills.

### Phase 6 — Init scaffolding (1 hour)

- `idiom init [<dir>]` creates a default `.idiom/` layout in the
  given project:
  - Stub `config.json` with placeholder model + workspace root.
  - Empty `external_tools/` directory with a README pointing at the
    JSON schema.
  - Empty `prompts/lanes/` and `prompts/rules/` directories with
    READMEs explaining the override pattern.
  - Empty `postflight/checks/` directory.
- Optional flag `--language go|cpp|ts|...` seeds language-specific
  defaults.

### Phase 7 — Skills templating (1-2 hours)

- Idiom ships `prompts/skills/<name>/SKILL.md` templates for the
  generic skills (orchestrate, dual-architect-workflow, fallback,
  triage-issues, session-handoff, update-config, update-docs).
- `idiom skills install <name>` copies a template into
  `<project>/.claude/skills/<name>/` (or `.codex/skills/`).
- Projects fork the SKILL.md and customize.

## Reusability outcome

After all phases, a fresh project's onboarding looks like:

```
cd ~/projects/new-project
idiom init --language cpp
# fill in .idiom/config.json with the workspace path + LLM endpoint
idiom backend start              # launches local LLM(s)
idiom skills install orchestrate # copies the orchestrator SKILL.md
# author a brief at .planning/atom-001.md
idiom brief lint .planning/atom-001.md
idiom lane plan-execute --title atom-001 .planning/atom-001.md
```

Project-side files reduce to:
- `.idiom/config.json` — endpoint, workspace root, language hints
- `.idiom/external_tools/*.json` — project-specific tool adapters
- `.idiom/postflight/checks/*.json` — project-specific lint rules
- `.idiom/prompts/{lanes,rules}/*.md` — overrides for canonical prompts
- `.claude/rules/*.md` — project-only rules (coding standards, etc.)
- `.claude/skills/*/SKILL.md` — project-customized skill copies

Everything else lives in idiom.

## Effort estimate

| Phase | Effort | Risk | Value |
|---|---|---|---|
| 1 (docs) | 1-2h | none | high (unblocks everything else) |
| 2 (lane wrapper) | 3-4h | low | high (most-used surface) |
| 3 (backend) | 2-3h | low | medium (only matters for new projects) |
| 4 (postflight) | 2-3h | medium | medium (configurable rules need careful design) |
| 5 (prompts) | 3-4h | medium | high (this is where most copy-paste happens today) |
| 6 (init) | 1h | none | medium (only matters for new projects) |
| 7 (skills) | 1-2h | low | low (skills are usually project-specific anyway) |
| **Total** | **13-19h** | | |

## What I'd do first

Phase 1 (docs) and Phase 5 (prompts) deliver most of the
reusability. The lane wrapper (Phase 2) is the most visible win for
day-to-day use. Phase 3/4/6/7 are nice-to-have for greenfield projects
but the existing Colony plumbing keeps working in the meantime.

Recommended order: 1 → 5 → 2 → 4 → 3 → 6 → 7.
