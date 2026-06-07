# Generic Harness Architecture — Design Sketch

**Status**: design sketch, not yet scheduled.
**Owner**: TBD.
**Last touched**: 2026-05-15.

Captures two threads that converge on the same north-star: idiom should be a generic local-LLM harness, not a Qwen3/Qwen-coder-bound one, and `idiom lane refine` + `idiom lane plan-execute` should form a closed brief→ship loop with no orchestrator hand-edits between them.

Sibling docs: `north-star-remediation.md`, `reusability-design.md`, `parity-tasks.md`, `lane-architecture.md`.

## 1. Motivation

Two pressures point the same direction:

1. **Generic-harness pressure.** The refine pipeline still falls back to Qwen3.6-35B routing constants today because that's what's wired. Draft materialization is now artifact-owned, and patch/review use request-level no-thinking where needed, but projects with DeepSeek R1, GLM-4.6, future Qwens, or any reasoning-capable local model still can't fully control refine roles without idiom config support. The role/pool abstractions in `backends.json` are 80% there; consumers (refine + pool routing) bypass them.

2. **End-to-end qwen-parity pressure** (`FEATURE_REQUEST_END_TO_END_QWEN_PARITY.md`). Refine produces a brief; the orchestrator manually adds HARD PROHIBITIONS, inlines project traps, and runs `idiom brief lint` before dispatching through plan-execute. That ~10–20 min/atom hand-step is the parity gap.

Same underlying fix: stop hardcoding, drive everything from project-side config + project-side overlays.

## 2. Current hardcoding inventory

Audited 2026-05-11 with `grep -rn 'qwen' --include='*.go' cmd/ internal/` filtered to production code:

| Layer | Site | Hardcoded value | Severity |
|---|---|---|---|
| Refine routing | `cmd/idiom/lane_refine.go:96-97` | `const refineDefaultPool = "qwen-35b"`, `const refineDefaultModel = "qwen3.6-35b"` | **High** — blocks non-Qwen adopters |
| Pool→model heuristic | `internal/runtime/runner/gate.go:155-175` (`PoolForModelAlias`) | String-match: contains "coder"/"nothink"/"35b"/"planner" → fixed pool name | **High** — naming-based, fragile |
| Bootstrap defaults | `cmd/idiom/init.go:123-155`, `internal/platform/config/config.go:271` | `qwen3.6-35b` baked into generated config | **Low** — projects override |
| Chat presets | `internal/platform/config/config.go:854-902` | 5 presets named `qwen3-*` | **Low** — presets are reference impls |

Only the first two are real architectural leaks. The rest are sensible defaults projects override.

## 3. The role abstraction (already 80% there)

`.idiom/backends.json` already declares:

- Per-model `"roles": ["planner", "executor", "classifier"]`
- `proxy.default_planner` / `default_executor` / `default_coder` / `default_classifier`

That's the contract. Refine doesn't consult it; `PoolForModelAlias` regex-matches the alias name instead of reading a per-backend `pool` field. Finishing the abstraction is the work.

## 4. Proposed schema additions

Three new optional fields. Existing configs keep working — every new field has a fallback chain.

### 4.1 Per-backend `pool` field

`.idiom/backends.json`, per model entry:

```json
{
  "alias": "qwen3.6-35b",
  "pool": "qwen-35b",
  "thinking": false,
  ...
}
```

`PoolForModelAlias(alias)` resolution chain:
1. If the active profile declares `pool` on the matching backend → use it.
2. Else apply the existing regex heuristic (`coder` / `nothink` / `35b` / `planner`).
3. Else return empty → caller uses `default` pool.

Projects with `granite-coder` / `mistral-planner` / etc. set `pool` explicitly. Qwen-pair projects don't touch their config — heuristic still matches.

### 4.2 Per-backend `thinking` field

Replaces "nothink" magic-string with an explicit `"thinking": false` boolean.

When the launcher composes `--chat-template-args`, it merges the model's `thinking` field into the JSON before passing it to mlx-lm/llama.cpp. Today projects do this by hand in `extra_args: ["--chat-template-args", "{\"enable_thinking\":false}"]`. The new field makes the intent legible to idiom (so refine can ask "give me a thinking-off variant of the planner") without idiom parsing freeform extra_args.

Fallback: if `thinking` is unset, current behavior — idiom passes `extra_args` through unmodified.

### 4.3 Refine role keys in `proxy` block

`.idiom/backends.json`, proxy block:

```json
"proxy": {
  ...
  "default_refine_draft":  "qwen3.6-35b",
  "default_refine_review": "qwen3.6-35b",
  "default_refine_patch":  "qwen3.6-35b"
}
```

Or, for projects where all three phases share a model:

```json
"default_refine": "qwen3.6-35b"
```

`refinePhaseRouting(metaBriefPath)` resolution chain:
1. Phase-specific key (`default_refine_<phase>`) → use it.
2. `default_refine` (single shared model) → use it.
3. `default_planner` → use it (sane fallback; thinking-off variant is preferable but optional).
4. The current `refineDefaultModel` constant → use it (preserves existing behavior).

The constants become final fallback, not the source of truth. Projects with no refine config still get refine routing; projects with custom config control it.

## 5. End-to-end parity loop

Once §4 lands, the four gaps from `FEATURE_REQUEST_END_TO_END_QWEN_PARITY.md` get clearer scope.

### 5.1 Gap 1 — Refine silent-termination bug

**Symptom**: `idiom lane refine` exits 0 with no `draft.md` produced. The meta-draft says "Do not read files" but the deliverable demands `<file>:LINE` anchors; the model picks reads, exits without writing, wrapper reports success.

**Fix**: align meta-draft contract with what the deliverable requires. Two options:
- **5.1a**: Allow capped reads (one read per `Files To Modify` entry, framed as "verify anchors, do not explore"). The completion gate enforces `required_edit_paths_satisfied = [draft.md]` — silent-no-write becomes a non-zero exit.
- **5.1b**: Run draft phase under a no-read sub-contract (`task_family: planner_draft`, `allowed_tools` excludes read/search), and require the spec to inline enough context. More restrictive but eliminates the contradiction.

Recommendation: 5.1a — the meta-draft already has the anchor structure; just match the contract.

Independent of §4 — can land first.

### 5.2 Gap 2 — One format

Refine's "design-brief" is already implementation-brief shape. Drop the distinction in docs, normalize section headers ("Required Behavior" ↔ "Required Edits"), update Colony's `qwen-loop-protocol.md` and `qwen-brief-skeleton.md` to stop calling refine output non-dispatchable.

Pure docs/naming. Can land alongside Gap 1.

### 5.3 Gap 3 — Refine review runs `idiom brief lint`

The reviewer prompt becomes:

1. Read `draft.md` once.
2. Invoke `idiom brief lint <draft.md>`; inline its output in the review.
3. Verdict criteria: `ship-as-is` requires `lint exit 0` (or warnings explicitly waived with rationale).
4. Patch phase consumes lint findings as concrete edit targets — BLOCKs become required edits, WARNs become optional.

This is the highest-leverage change: refine converges on a *lint-passing* brief by construction. The orchestrator's "run idiom brief lint after refine" hand-step disappears.

Schema: review phase needs shell access to run `idiom brief lint`. Today refine review is read-only; granting shell to a single named binary is the minimal extension.

### 5.4 Gap 4 — Project-overlay injection

Two new optional flags on `idiom lane refine`:

```
--harden-from <path>   # appends project HARD PROHIBITIONS block
--traps-from  <dir>    # inlines matching project traps by atom shape
```

Both default off; the canonical embed stays empty. Project layers its own.

**Hardening overlay**: `<project>/.idiom/prompts/brief/hard-prohibitions.md` is a project-invariant markdown block (allowlist enforcement, working-tree-collision guard, etc.). Refine appends verbatim after the final patch step.

**Trap library**: `<project>/.idiom/prompts/brief/traps/*.md` with frontmatter:

```markdown
---
applies_to: [test-code, c++-system]
---
# Trap title
...
```

Atom-shape classifier (local, deterministic) tags each brief by Files-to-Modify paths and extensions:
- `test_*.cpp` / `*_test.go` / etc. → `test-code`
- `save_fields.h` → `save-load`
- multi-file across `internal/` → `multi-file-go-scope`
- etc.

Refine includes traps whose `applies_to` overlaps the brief's tags.

Generic across projects. Colony defines its overlay; bare idiom-repo dispatches see no overlay.

## 6. Migration & compatibility

Every change in §4 and §5.4 is **strictly additive with sensible fallbacks**:

- No `pool` field on a backend → regex heuristic (current behavior).
- No `thinking` field → `extra_args` passes through unmodified.
- No `default_refine*` keys → fall through to `default_planner` → constant.
- No `--harden-from` / `--traps-from` flags → canonical refine output.

Existing configs (idiom's, Colony's, downstream adopters') need zero edits to keep working. Projects that want generic-harness benefits opt in field by field.

§5.1 (silent-termination fix) is a bug fix — no migration. §5.2 is doc-only. §5.3 changes review behavior but the new verdict criteria are stricter; iterations that would have shipped before still ship (lint pass is an additional gate, not a replacement).

## 7. Sequencing

Suggested rollout in dependency order:

1. **Gap 1** (silent-termination bug). Standalone bug fix. Unblocks refine on small specs today.
2. **§4.1 + §4.2** (per-backend `pool` + `thinking` fields). Schema-only changes; consumers added later.
3. **§4.3** (refine role keys). Refine reads from config; constants become final fallback.
4. **Gap 2** (one-format docs). Pure docs, can interleave anywhere.
5. **Gap 3** (review runs lint). Highest-leverage feature; depends on nothing.
6. **Gap 4** (project overlays). Last because it's the most project-specific and benefits most from the previous steps being in place.

Gaps 1, 2, 5 are independently shippable. §4 is independently shippable. Gap 6 wants the rest.

## 8. Non-goals

- Replacing chat presets. The 5 `qwen3-*` presets in `config.go` are reference implementations projects can copy/extend. Renaming them adds churn without solving anything; new presets can be named project-style (`granite-reasoning`, `mistral-coding`).
- Multi-backend orchestration (e.g. routing within a single lane to different models per step). Out of scope — current single-backend-per-lane is enough for the parity loop.
- Replacing the queue daemon's pool model. Pools stay external; only the *mapping* from model alias to pool name becomes config-driven.

## 9. Open questions

- Should refine's lint gate (`ship-as-is requires lint exit 0`) be configurable per-project, or is "lint must pass" universal? Lean: universal — projects that want to ship lint-failing briefs can use `--no-lint` on plan-execute and skip refine.
- For the `--traps-from` classifier, do we want idiom to ship a default set of atom-shape tags (test-code, save-load, multi-file, single-file-mech) or leave the taxonomy entirely project-defined? Lean: ship a small canonical set as a starting kit; projects extend.
- Does the `thinking` field belong on the backend model entry, or on the role assignment (so the same model can be marked thinking-on as planner, thinking-off as refine target)? Probably on the model — different model entries for different thinking modes is cleaner than per-role overrides, even if it duplicates the model_path.
