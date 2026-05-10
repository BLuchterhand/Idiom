---
description: |
  Orchestrator workflow for using local Qwen / Codex implementer
  models behind a Claude (or other strong-planner) Opus loop. The
  planner decomposes work into atoms, dispatches one bounded
  implementation lane per atom via `idiom lane plan-execute`, and
  reviews the diff before committing.
---

# Orchestrator Workflow — Planner Plans, Bounded Workers Execute

This skill describes the canonical pattern for running an
implementation loop where a strong planner (Claude Opus, GPT, etc.)
authors the spec and a bounded executor (local Qwen via idiom, or a
cloud overflow) does the mechanical edits.

## Role Split

| Role | Typical model | Via | Responsibility |
|------|---------------|-----|----------------|
| **Planner** | Strong reasoner (Opus, GPT-5) | The orchestrator (Claude Code, Codex, etc.) | Decompose, write atom specs, review diffs, commit |
| **Researcher** | Same as planner, or smaller explorer | Local tools or a delegated subagent | Answer specific codebase questions before spec'ing |
| **Implementer** | Local Qwen first; cloud overflow second | `idiom lane plan-execute` | Plan + apply edits inside one bounded lane |
| **Reviewer** | Same as planner, or independent reviewer | Inline or delegated | Full-scope review of the produced diff |

The planner stays in one trajectory across the whole atom; the
executor is **disposable** — every dispatch is a fresh session, no
shared state across atoms. This keeps the planner's context clean
and lets the executor be replaced or scaled without redesigning the
loop.

## Pipeline (per atom)

```
1. ANALYZE   — Planner identifies the impact / blast radius
2. RESEARCH  — Planner (or delegated explorer) answers specific code questions
3. VERIFY    — Planner spot-checks key findings (1–2 targeted reads)
4. SPEC      — Planner writes the atom brief WITH test cases (tests are the spec)
5. IMPLEMENT — `idiom lane plan-execute --title <atom> <brief>`
6. ACCEPT    — Planner builds, runs tests, spot-checks, commits
7. REVIEW    — Per project policy: inline / delegated / skip based on complexity
```

Steps 1–4 and 6–7 stay with the planner. Step 5 is the executor's
sole responsibility — when a dispatch fails, the planner first
checks whether the brief was tight enough (anchor discipline), then
whether the harness has a real gap that should be fixed.

## Brief discipline

A good atom brief is the difference between a lane that converges
in one shot and a lane that exhausts its budget. The canonical
`brief-skeleton.md` rule (embedded in idiom) enumerates the
required sections; the most-overlooked are:

- **Verified Current State** — exact source snippets the executor
  uses as `old_text` anchors. Paraphrase ≠ anchor; "the function
  that handles X" is not an anchor; the literal source line is.
- **Files To Modify** — single-purpose decomposition; ≤8–10 edits
  per atom. The runner extracts this as `required_edit_paths`.
- **Implementation Shape** — for plan-execute, name the edit shape
  (substring / line-range / new-file) so the planner doesn't have
  to infer it.

`idiom brief lint <brief>` runs the canonical preflight on a brief
before dispatch; pre-merge CI hooks should run it too.

## When the dispatch fails

Three failure classes the lane wrapper surfaces (via the
`idiom.lane.degraded` JSON event):

- `read_only_lane` — the brief was routed to a read-only contract.
  Almost always an idiom-routing regression; report via
  `harness_feedback`, do NOT rewrite the brief.
- `no_edit_attempt` — the lane completed cleanly but never tried
  to edit. Either the brief lacked an actionable Required Edits
  section or the executor model couldn't anchor the edits.
- Non-zero subcommand exit — a real failure (build broken,
  process crash). The wrapper exit propagates.

The orchestrator's loop should subscribe to these signals and
react: typically retry with a tightened brief once, then escalate
to human review or fall back to a stronger executor.

## When the executor isn't suited

Local Qwen-coder-30B handles surgical edits to existing files
well. It struggles to author large new files from spec. The
emerging pattern:

- **Existing-file surgical edits** → dispatch to local Qwen.
- **New file authoring (>~50 lines)** → planner writes the file
  directly; executor only handles the dispatch-case wiring.
- **Multi-file refactors** → decompose into per-file atoms; each
  atom stays single-purpose.

This isn't a hard rule — it's the bias to start with. As local
models improve, more of the second category will move into Qwen's
column.

## Customize this skill

The canonical version (this file) is intentionally generic. A
project should overlay project-specific guidance — preferred test
runners, build commands, atom-decomposition heuristics for the
project's domain, escalation paths for failed dispatches — by
creating `<repo>/.claude/skills/orchestrator-workflow/local.md`
(or appending to this file). Idiom doesn't read these; they're
for the orchestrator's own reference.
