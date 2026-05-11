# Refine End-to-End Loop — Design Sketch

**Status**: design sketch, implementation in progress.
**Last touched**: 2026-05-11.
**Supersedes (partial)**: section 5 of `generic-harness-architecture.md` (the four end-to-end-parity gaps re-factored here with corrected scoping).

Closes the brief→ship loop so `idiom lane refine` produces a dispatchable brief with zero orchestrator hand-edits between phases. Generic for any project; Colony-friendly without Colony-specific flags.

Sibling docs: `generic-harness-architecture.md` (broader harness genericization), `FEATURE_REQUEST_END_TO_END_QWEN_PARITY.md` (originating filing), `lane-architecture.md`.

## 1. Problem

Today's flow:

```
1-paragraph spec
   ↓
idiom lane refine             produces a brief
   ↓
orchestrator manual step      ~10–20 min/atom:
                                - add HARD PROHIBITIONS block
                                - inline project-specific traps
                                - run `idiom brief lint` and fix BLOCKs/WARNs
   ↓
idiom lane plan-execute       ships
```

Two failure modes, one architectural cause:

1. **Refine wedges silently on small specs.** Colony's `idiom-evolution-C.1-spec.md` asks for a NEW markdown file (postflight-check inventory). Refine's meta-draft is hardcoded for the "anchored-edits" deliverable shape — `Verified Current State` demands `<file>:LINE` anchors, `Required Behavior` demands `old_text`/`new_text` blocks. A NEW markdown file has neither. The model picks reads (forbidden by meta-draft), gives up, wrapper reports exit 0.

2. **Hand-step in the middle.** Prohibitions/traps/lint can't be a wrapper concern because refine doesn't know they exist. Today the orchestrator owns them.

The single underlying cause: **refine assumes one deliverable shape**. Different specs need different brief shapes; the loop must pick.

## 2. Proposed design — four changes

### 2.1 Honest exit codes (Gap 1, simpler half)

Refine returns non-zero when `--output` is missing or empty after the loop completes. Today silent-failure paths (subprocess exits 0 with no draft, finalizes with 0-byte content) exit 0.

**Concrete changes** (`cmd/idiom/lane_refine.go`):

- After each iter's draft dispatch, check both `os.Stat(draftPath)` AND `len(content) > minDraftBytes` (~200 bytes — enough to require at least a Problem + Files block).
- Treat 0-byte and template-fragment drafts as `draft-missing` failsoft.
- `finalizeRefineWithTitle` validates the final output is non-trivial before exiting 0.
- Exit-code propagation: every error path returns non-zero unless a real `ship-as-is` or non-trivial failsoft-saved draft landed.

Independent of every other change. Smallest blast radius. Ships first.

### 2.2 Two-mode refine

Refine detects deliverable shape from the spec and routes to one of two internal templates:

| Mode | Trigger | Template |
|---|---|---|
| **anchored-edits** (current) | Spec references source files (`*.go`/`*.cpp`/`*.py`/`*.rs`/`*.ts`/`*.java`) or implies edits to existing files | Today's meta-draft: Problem / Files / Verified Current State (line anchors) / Required Edits (old_text/new_text) / Acceptance |
| **markdown-deliverable** (new) | Spec asks for a NEW `.md`/`.txt`/`.json` file outside source trees (e.g. `.planning/`, `docs/`) AND no existing-file edits | New meta-draft: Problem / Deliverable Path / Inline Content / Acceptance (`test -f <path>`) |

**Detection logic** (deterministic, local):

1. Run the existing spec-parse step to extract any path mentions.
2. Classify each path:
   - Path ends in `.md`/`.txt`/`.json`/`.yaml` AND lives under `.planning/`, `docs/`, `notes/`, `research/`, or top-level → markdown candidate.
   - Path matches a known source-tree extension → source candidate.
3. If all candidates are markdown AND no source candidates → markdown-deliverable mode.
4. Mixed or any source → anchored-edits mode.
5. No path mentions at all → anchored-edits mode (safer default; the brief asks for clarifications).

Heuristic, not perfect — the override `--mode anchored|markdown` exists as escape hatch for ambiguous specs.

**Markdown-deliverable template** (skeleton, ~30 lines vs anchored-edits' ~200):

```markdown
# <title>

## Problem

<2–3 sentence framing — what the deliverable answers>

## Deliverable

Path: `<output path>`
Format: markdown / json / etc.

## Inline content

<the actual content of the file, ready to write verbatim>

## Acceptance

- `test -f <path>` returns 0
- (optional) `wc -l <path>` returns ≥ N lines
- (optional) one structural check (`grep -q '^## <expected-section>'`)
```

No anchors, no Verified Current State, no old_text/new_text. The deliverable IS the inline content; refine's job is to compose it.

### 2.2.1 Project-extensible modes (added 2026-05-11)

The two canonical modes (anchored, markdown) cover ~90% of software development. The remaining ~10% (hardware-in-loop, notebooks, runbooks, regulated-industry briefs, etc.) needs *different brief shapes*, not just project-specific augmentations of the existing shape. Rather than embedding every conceivable mode into idiom's binary, modes are **project-extensible** via a convention-based override path.

**Resolution chain** for any mode name `<M>`:

1. **Project override**: `<projectRoot>/.idiom/prompts/refine/draft-<M>.md` — read at runtime if present.
2. **Embedded canonical**: the `anchored` and `markdown` templates that ship with the binary.
3. **Unknown mode**: fail with an actionable error pointing at the override convention.

Same pattern applies to review and patch templates (`review-<M>.md`, `patch-<M>.md`) — both optional; missing override falls back to the mode-agnostic canonical review/patch.

**Override invocation:**

```bash
# Custom mode (project must have draft-notebook.md)
idiom lane refine --mode notebook --spec ... --output ...

# Canonical mode override (project re-skins anchored)
idiom lane refine --mode anchored --spec ... --output ...
# → uses <project>/.idiom/prompts/refine/draft-anchored.md if present,
#   else embedded anchored template
```

**Discipline line — what projects can and can't do:**

| Knob | Project can | Project can't |
|---|---|---|
| Mode body template | Override or add | Replace the canonical envelope (Problem / Files / Acceptance headings stay) |
| Mode detection rules | Pass `--mode <name>` explicitly | Extend auto-detection (Phase 2 if needed; not now) |
| Lint output schema | Read canonical JSON | Define new lint categories |
| Completion gate | Inherit canonical | Bypass |
| Overlay injection (§2.4) | Add prohibitions + traps | Skip them |

The harness machinery (lint, completion gate, overlay injection, honest exit codes) operates on the canonical envelope. Projects swap the body section; the envelope stays.

**Safety lint on overrides** (loaded at runtime, before substitution):

Reject malformed override templates early. Required markers per template type:

- `draft-<M>.md`: must contain `{{DRAFT_PATH}}` placeholder + at least one `## ` heading + a `write_file` instruction.
- `review-<M>.md`: must contain `{{REVIEW_PATH}}` placeholder + `{{LINT_OUTPUT}}` placeholder.
- `patch-<M>.md`: must contain `{{NEW_DRAFT_PATH}}` placeholder.

A missing placeholder is a hard error: `"override template draft-notebook.md is missing required placeholder {{DRAFT_PATH}}"`. Prevents the silent template-fragment-shipping failure mode.

**Directory layout:**

```
<project>/.idiom/prompts/
├── brief/                  # §2.4 overlay (prohibitions + traps)
│   ├── hard-prohibitions.md
│   └── traps/
└── refine/                 # mode templates (this section)
    ├── draft-notebook.md
    ├── review-notebook.md  # optional
    ├── patch-notebook.md   # optional
    └── draft-hardware-loop.md
```

Separation of concerns: `brief/` augments existing modes; `refine/` adds new modes. A project can use both, either, or neither.

### 2.3 Refine review runs `idiom brief lint`

The review phase prompt becomes:

1. Read `draft.md` once.
2. Invoke `idiom brief lint <draft.md>`; inline output verbatim in the review.
3. Verdict criteria:
   - `lint exit 0` + reviewer finds no bugs → `ship-as-is`
   - `lint exit non-zero` OR reviewer finds bugs → `fix-bugs-then-ship` (patch phase consumes both lint findings and reviewer bugs)
4. Reviewer may waive lint WARNs (not BLOCKs) with explicit rationale per warning.

Patch phase prompt becomes:

1. Read draft + review.
2. For each lint BLOCK and each reviewer bug, fix the named issue.
3. Re-emit the full draft (not a diff).

This is the **convergence guarantee**: every ship-as-is brief is lint-passing by construction. The orchestrator's "run idiom brief lint after refine" hand-step disappears.

Schema additions to allow the review subprocess to invoke `idiom brief lint`:

- Review meta-brief grants shell access scoped to one binary (`idiom`) with one subcommand (`brief lint`).
- Or simpler: the wrapper runs `idiom brief lint` itself between phases and inlines the output into the review meta-brief as preloaded evidence. No new shell permissions needed; reviewer just reads what the wrapper already collected.

Lean toward the wrapper-side approach — simpler, no permission surface, deterministic.

### 2.4 Convention-based project overlay

No new flags in the common case. Refine auto-discovers overlay paths in the project:

```
<project>/.idiom/prompts/brief/
├── hard-prohibitions.md       # appended verbatim to every brief
└── traps/
    ├── test-code.md           # frontmatter: applies_to: [test-code]
    ├── save-load.md           # applies_to: [save-load]
    └── multi-file-cpp.md      # applies_to: [multi-file, c++-system]
```

Trap files use markdown frontmatter:

```markdown
---
applies_to: [test-code, c++-system]
---

# Trap title

<trap body — inlined verbatim if the brief's tags overlap applies_to>
```

**Atom-shape classifier** (deterministic, local — no LLM):

- Examines the brief's Files-to-Modify allowlist after the draft phase.
- Emits tags based on path patterns:
  - `*_test.{cpp,go,py,rs,ts,java}`, `tests/`, `__tests__/` → `test-code`
  - `internal/`, `cmd/`, `src/` Go files spanning ≥ 2 directories → `multi-file-go`
  - Save-load schema files (project-defined glob — see below) → `save-load`
  - `.cpp`/`.h`/`.hpp` files → `c++-system`
  - `.md`/`.txt` under `.planning/`/`docs/` → `markdown-deliverable`
  - Default catch-all → `generic`

Refine includes trap files whose `applies_to` intersects the brief's tags. Multiple traps may match; all matching traps are inlined.

**Project-defined tag patterns**: `<project>/.idiom/prompts/brief/atom-shapes.yaml` (optional):

```yaml
save-load:
  paths:
    - "include/save_*.h"
    - "src/serialization/**"
```

Projects extend the canonical tag set. Missing yaml → canonical tags only.

**Overrides**: `--harden-from <path>` and `--traps-from <dir>` exist as escape hatches for non-conventional layouts, but they're not in the common dev path.

**Behavior on bare repos**: no `.idiom/prompts/brief/` directory → canonical brief, no overlay. Same dev command, same result quality minus the prohibitions/traps section.

## 3. The unified dev workflow

After all four changes:

```bash
# Universal — same command everywhere
idiom lane refine --spec foo-spec.md --output foo-brief.md
idiom lane plan-execute --brief foo-brief.md
```

What happens internally, transparent to the dev:

| Step | If project has overlay | If bare repo |
|---|---|---|
| Refine detects spec shape | both: anchored-edits or markdown-deliverable | same |
| Refine drafts using matching template | both | same |
| Wrapper runs `idiom brief lint` between draft and review | both | same |
| Review consumes lint output + draft, emits verdict | both | same |
| Patch phase fixes lint BLOCKs + reviewer bugs | both | same |
| Hardening appended to final brief | overlay present | no overlay |
| Project traps inlined by atom-shape | overlay present | no overlay |
| Exit code reflects reality | both | same |

Two commands. No flags to remember. Generic adopters get a canonical brief; Colony gets a hardened brief; both use the same workflow.

## 4. Migration & compatibility

All four changes are strictly additive:

- **§2.1** (exit codes): tightens existing behavior. Configs that used to silently succeed-with-no-output now fail loudly. The only "breaking" path is one that was broken anyway.
- **§2.2** (two-mode): mode detection runs on every refine invocation. Existing anchored-edits flow unchanged. New markdown-deliverable flow only activates when the spec is unambiguously a NEW markdown file.
- **§2.2.1** (project-extensible modes): resolution chain falls through to embedded templates unless a project explicitly drops files at `.idiom/prompts/refine/`. Bare repos see zero difference; opting-in projects gain new modes without recompiling idiom.
- **§2.3** (review runs lint): adds lint output as preloaded evidence; reviewer must respect it. Existing reviewer prompts still produce verdicts — the new criteria is `lint must pass`. Briefs that already pass lint ship the same; briefs that fail lint shipped before and won't now (correct behavior).
- **§2.4** (overlay): bare projects (no `.idiom/prompts/brief/`) get exactly today's behavior. Projects that opt in get the new injections.

Existing tests in `cmd/idiom/lane_refine_test.go` need updates for §2.1 (exit codes) and §2.3 (lint integration); §2.2 and §2.4 add new tests rather than changing existing ones.

## 5. Sequencing

1. **§2.1 — Honest exit codes**. ~2-hour change. Standalone bug fix. Lands first.
2. **§2.3 — Wrapper runs `idiom brief lint`**. ~half-day. Convergence guarantee. Doesn't depend on anything.
3. **§2.2 — Two-mode refine**. ~1-day. Mode detection + new markdown-deliverable template + tests. Unblocks Colony's small atoms.
4. **§2.2.1 — Project-extensible modes**. ~half-day. Refactor `writeRefineDraftMeta` to a resolution chain + override safety lint + tests. Future-proofs the mode dimension without committing to a config-file schema.
5. **§2.4 — Convention overlay**. ~1-day. Discovery + classifier + injection. Last; benefits most from previous steps in place.

Each step ships independently and provides standalone value.

## 6. Open questions

- **Lint output format for review preload**. Markdown table vs. raw JSON vs. one-line summary? Review needs structured findings, not just exit code. Lean: structured JSON (`idiom brief lint --json`) inlined as a fenced block.
- **Mode detection ambiguity**. A spec that says "update README.md and add docs/foo.md" mentions both an existing-file edit AND a new-markdown-file. Current detection routes to anchored-edits (any source-shaped path wins). Probably correct — anchored-edits handles mixed shapes; markdown-deliverable only handles pure-markdown. Confirm with a few real specs.
- **Atom-shape tag taxonomy default set**. How many tags should idiom ship out of the box? Lean: 6 (test-code, multi-file-go, c++-system, save-load, markdown-deliverable, generic). Projects extend.
- **Failsoft vs. fail-loud for lint failures**. If iter-N's lint output has BLOCKs and patch phase fails to converge, do we ship the best-bug-count draft (current failsoft) or fail with non-zero? Lean: fail-loud — a lint-failing brief is worse than no brief, since plan-execute will reject it anyway.

## 7. Non-goals

- Replacing the meta-draft contents directly. The meta-draft is the *contract* refine offers the dispatched model; we're adding a mode dimension, not rewriting the contract.
- Custom lint rules per project. Lint stays idiom-canonical; projects don't extend it. (Project traps cover project-specific concerns, separately from lint.)
- A general-purpose convention-overlay framework. We're solving for refine's three injection points (prohibitions, traps, lint). Other lanes (`plan-execute`, `implement`) keep their existing prompt-stack.

## 8. Faultline-orchestrator opinions on §6 open questions (added 2026-05-11)

Captured at the request of the user reviewing this design alongside the design-author agent's view. Independent read; ship-direction agreement on all four, with refinements.

### Q1 — Lint output format for review preload

Agree on JSON, but the **schema matters more than the format**. The patch phase is a model consuming structured findings; it needs stable fields (`severity`, `category`, `file`, `line`, `message`) and a documented `category` taxonomy stable enough to map findings → patch strategies. Without that, the patch phase invents groupings on the fly and the convergence guarantee weakens.

Concrete recommendation: ship `idiom brief lint --json` with the schema documented in the same place lint rules live (so adding a new rule means adding its category at the same commit — keeps the schema canonical).

Filter what reaches the review meta-draft: BLOCKs + WARNs only, drop INFO. Real Colony briefs can produce 30+ findings; the review needs the load-bearing ones, not noise. The full JSON stays available on disk for orchestrator inspection at `.idiom/refine/<title>/iter-N/lint.json`.

### Q2 — Mode detection ambiguity

Anchored-edits-as-default is correct, but the reasoning is *asymmetric* in a way the section undersells:

- anchored-edits **degrades** to markdown-deliverable cleanly (empty Verified Current State, one Required Edit whose `new_text` is the full file content)
- markdown-deliverable **can't grow upward** — it has no anchor section, no `old_text` shape, so it can't represent any source edit

So "any source-shaped path → anchored-edits" is safe in the loose direction. But there's a third case the section doesn't address: **a NEW markdown deliverable that cites source anchors**. The validation spec for this filing (`.planning/idiom-evolution-C.1-spec.md`) is exactly that — produce a NEW markdown inventory of `lane_postflight.sh`, requires reading line anchors from a source file to cite them in the deliverable. Pure markdown-deliverable mode (no Verified Current State section) cannot handle it; anchored-edits mode handles it correctly because Verified Current State is exactly the right home for `lane_postflight.sh:296` citations.

Recommendation: tighten the detection rule to **"spec mentions any source path → anchored-edits, regardless of where the output goes"**. Markdown-deliverable mode reserves itself for specs that mention zero source paths (pure design docs, tracker updates, planning notes). Narrower trigger, but it cleanly handles inventories-of-source-files.

### Q3 — Atom-shape tag taxonomy default set

Six is too many for idiom's canonical surface, and the proposed set leaks project-specifics:

- `save-load` is Faultline-specific (other projects may not have a serialization layer at all)
- `multi-file-go` is language-narrow when the underlying trap ("coordinate edits across files") is language-agnostic
- `c++-system` is Colony-specific

Idiom's stated north star is generic-first with project overlays for specifics. The canonical taxonomy should be minimal and language-agnostic:

- `test-code` — universal; every project has tests, trap shape is stable (fixture wiring, framework-specific gates, ODR-like risks)
- `multi-file` — coordination/ordering traps, language-agnostic
- `markdown-deliverable` — pairs with the new mode
- `generic` — catch-all

Four canonical tags. Projects extend via `<project>/.idiom/prompts/brief/atom-shapes.yaml` — Faultline adds `save-load`, `c++-system`, `data-additions`, `cross-system`, etc. Keeps the canonical surface honest and forces each project to own its specific taxonomy where the project-specific knowledge lives.

### Q4 — Failsoft vs fail-loud on lint failures

Fail-loud is right, but the section's framing creates a false dichotomy. The current "failsoft" does two things: (a) saves the best draft to `--output`, (b) exits 0. **(b) is the dishonest part, not (a).**

The combination to ship: **save the best draft AND exit non-zero.**

- Non-zero exit forces the orchestrator to inspect rather than auto-pipe to plan-execute (and aligns with §2.1).
- Saved draft survives for debugging — orchestrator can read it and decide: rewrite spec, extend overlay traps, or accept that this atom isn't refine-shippable. Losing the draft on convergence failure throws away the only signal about *why* refine couldn't converge.

UX polish: write the draft with a sentinel header (e.g. `> WARNING: refine exited non-zero — lint had N BLOCKs unresolved after iter-M. See .idiom/refine/<title>/iter-M/{lint.json,review.md} for findings.`). Makes the artifact self-documenting without changing the exit-code contract.

### Cross-cutting

§2.3 (wrapper runs lint between draft and review) is the highest-leverage of the four §2 changes. Of the four open questions, Q1 (lint output format) is the one §2.3 most depends on — get the JSON schema right early because every future review-phase prompt will consume it.

§2.2 (two-mode) is the lowest-priority of the four because the markdown-deliverable case is genuinely narrower than the anchored-edits case (per Q2 above). Once detection is tightened to "any source path → anchored-edits", markdown-deliverable mode handles only a few specific shapes (pure design docs, planning notes). Still worth shipping, but anchored-edits will cover the long tail.
