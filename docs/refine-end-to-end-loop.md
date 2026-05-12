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

**Two-path disambiguation (Colony bug fix, 2026-05-11):** the markdown-deliverable mode introduces a known model-confusion source. The brief contains a `Deliverable Path` field naming where plan-execute will eventually write the inline content, AND refine itself writes the brief to `{{DRAFT_PATH}}`. Without explicit disambiguation in the meta-draft, models conflate the two — writing the inline content to the deliverable path instead of writing the brief to `{{DRAFT_PATH}}`, or writing nothing at all because they can't reconcile the two-path shape.

The canonical `draft-markdown.md` template (commit follows) includes a "Where you write THIS draft.md" section right before the write step:

> Your output file (refine's scratchpad for this iteration) is `{{DRAFT_PATH}}`. This is unrelated to the brief's `Deliverable Path` field above. The downstream plan-execute lane will write `Inline content` to `Deliverable Path`; refine itself writes to `{{DRAFT_PATH}}`. Two different files, two different consumers.

**Implication for custom modes:** any project-extension mode whose brief shape includes a deliverable-path-style field (the brief names a target path that's distinct from where refine writes the brief itself) MUST include a similar two-path disambiguation block. The `TestMarkdownDraftTemplateClarifiesTwoPaths` test locks in the canonical template; custom-mode authors should mirror the wording. Documenting this here so the pattern doesn't have to be rediscovered per project.

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

- `draft-<M>.md`: must contain `{{DRAFT_PATH}}`, `{{SPEC_BODY}}`, `{{TITLE}}` placeholders + at least one `## ` heading.
- `review-<M>.md`: must contain `{{REVIEW_PATH}}` placeholder + `{{LINT_OUTPUT}}` placeholder.
- `patch-<M>.md`: must contain `{{NEW_DRAFT_PATH}}` placeholder.

A missing placeholder is a hard error: `"override template draft-notebook.md is missing required placeholder {{DRAFT_PATH}}"`. Prevents the silent template-fragment-shipping failure mode.

**Auto-append canonical write closure (UX choice, 2026-05-11):**

The override validator does NOT check for `write_file` mention. Instead, if a project override template's body doesn't contain a `write_file` directive, idiom auto-appends a canonical closure (matching the embedded templates' "STOP AND WRITE NOW" section) with the destination path substituted in. The substitution happens through the normal pipeline.

Rationale: project authors should focus on the body content of their custom mode (what the model writes). Closure boilerplate is harness concern. The author can override the closure by including their own `write_file` instruction (e.g. `submit_plan` instead) — auto-append only fires when truly missing. A one-line stderr message logs each auto-append so users know what idiom did.

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

1. **§2.1 — Honest exit codes**. ~2-hour change. Standalone bug fix. Lands first. ✅
2. **§2.3 — Wrapper runs `idiom brief lint`**. ~half-day. Convergence guarantee. Doesn't depend on anything. ✅
3. **§2.2 — Two-mode refine**. ~1-day. Mode detection + new markdown-deliverable template + tests. Unblocks Colony's small atoms. ✅
4. **§2.2.1 — Project-extensible modes**. ~half-day. Refactor `writeRefineDraftMeta` to a resolution chain + override safety lint + tests. Future-proofs the mode dimension without committing to a config-file schema. ✅
5. **§2.4 — Convention overlay**. ~1-day. Discovery + classifier + injection. Last; benefits most from previous steps in place. ✅

**Post-Colony-review corrections (2026-05-11):**

- **Q2 bug-fix**: `refineSourceExtensions` extended with `.sh`/`.bash`/`.zsh`/`.fish`/`.sql`/`.psql`/`.plsql`/`.proto`/`.tf`/`.hcl`/`.bicep`. Specs citing `lane_postflight.sh:296`-style anchors now correctly route to anchored mode.
- **Q4 failsoft semantics**: only `ship-as-is` returns exit 0. Failsoft / diverging / no-improvement / max-iters-exhausted all save the best draft AND exit non-zero, with a `> WARNING:` blockquote prepended to the artifact pointing at `.idiom/refine/<title>/iter-N/{review.md,metrics.json}` for findings. Preserves debugging signal while preventing auto-pipe-to-plan-execute.
- **Q3 taxonomy**: canonical 4 tags shipped (`test-code`, `multi-file`, `markdown-deliverable`, `generic`). Project-specific tags (`save-load`, `c++-system`, etc.) live in overlay traps' `applies_to` fields.
- **UX auto-append**: override templates without an explicit `write_file` directive get the canonical closure appended at substitution time. Project authors focus on body content; harness handles closure boilerplate.

Each step ships independently and provides standalone value.

## 6. Open questions

- **Lint output format for review preload**. Markdown table vs. raw JSON vs. one-line summary? Review needs structured findings, not just exit code. Lean: structured JSON (`idiom brief lint --json`) inlined as a fenced block.
- **Mode detection ambiguity**. A spec that says "update README.md and add docs/foo.md" mentions both an existing-file edit AND a new-markdown-file. Current detection routes to anchored-edits (any source-shaped path wins). Probably correct — anchored-edits handles mixed shapes; markdown-deliverable only handles pure-markdown. Confirm with a few real specs.
- **Atom-shape tag taxonomy default set**. How many tags should idiom ship out of the box? Lean: 6 (test-code, multi-file-go, c++-system, save-load, markdown-deliverable, generic). Projects extend.
- **Failsoft vs. fail-loud for lint failures**. If iter-N's lint output has BLOCKs and patch phase fails to converge, do we ship the best-bug-count draft (current failsoft) or fail with non-zero? Lean: fail-loud — a lint-failing brief is worse than no brief, since plan-execute will reject it anyway.

## 6.1 Deferred implementation gaps (pending real-use feedback, 2026-05-11)

After shipping §2.1, §2.2, §2.2.1, §2.3, §2.4 + Colony Q2-Q4 corrections, an honest audit surfaced four gaps the implementation did NOT close. Each is **deliberately deferred** until a real project reports hitting it, per the "don't design for hypothetical future requirements" principle. Logging them here so they're not silently forgotten — and so they can be revisited with empirical evidence rather than speculation.

Each gap below is framed as a feedback question for any project running the refine loop (Faultline/Colony, Idiom-on-Idiom, future adopters). Confirm or refute from real-use observation, not theoretical reasoning.

### Gap 1 — Patch template doesn't explicitly target lint BLOCKs

**Current behavior**: §2.3 captures lint output and inlines it into the review meta-brief. The reviewer reflects lint findings into the review's "Lint findings" section. The patch phase reads the review and addresses bugs, but the patch template only names "Bugs found" as an edit target — not "Lint findings."

**Theoretical weakening**: a patcher might fix reviewer bugs while skipping unmentioned lint BLOCKs because the prompt doesn't name them as targets. The convergence guarantee depends on the reviewer correctly transcribing every BLOCK into both sections.

**Question for projects**: have you observed a refine run where iter-N's review correctly listed lint BLOCKs but the iter-N+1 patch failed to address them? Or does the current "review-as-source-of-truth" path work reliably in practice?

**Fix when needed**: ~30 min. Update `lanes/refine/patch.md` to add a "Pre-loaded lint findings" section (same shape as review.md's) and instruct the patcher to address each BLOCK alongside reviewer bugs.

**Faultline-orchestrator comment**: ship now, do not defer. The "convergence by construction" guarantee §2.3 makes rests on the patcher reliably addressing every lint BLOCK. Routing through reviewer-transcription is exactly the indirection layer "by construction" guarantees are supposed to eliminate. The deferral's evidence trigger ("review listed BLOCKs but patch failed to address them") is a silent-failure path that requires auditing intermediate state to notice — most operators won't. 30-min fix; risk profile is asymmetric (if reviewer-transcription is reliable, this is a no-op; if it's not, we get silent convergence regressions that look like "lint passed but didn't"). See §9 for fuller reasoning.

### Gap 2 — Review and patch override paths not implemented

**Current behavior**: §2.2.1 resolution chain works for `draft-<mode>.md` overrides. For `review-<mode>.md` and `patch-<mode>.md`, the resolution chain is not wired — custom modes (e.g., `notebook`, `runbook`, `hardware-loop`) always use the embedded canonical review/patch templates.

**Theoretical limitation**: a custom mode whose review/patch logic differs from canonical (e.g., notebook reviewer should validate cell execution sanity, not anchor uniqueness) can't customize the review/patch prompts without idiom-side support.

**Question for projects**: when you add a custom mode (or stub one for an upcoming use case), do you need a different reviewer prompt? Or do the canonical review/patch templates cover the case because review-style concerns are mode-agnostic ("did the draft satisfy the spec, are there bugs, what's the verdict")?

**Fix when needed**: ~half-day. Mirror the draft resolution chain for review and patch paths; same safety lint pattern; tests.

**Faultline-orchestrator comment**: defer is fine. The argument that review concerns are mode-agnostic ("did the draft satisfy the spec, are there bugs, what's the verdict") is genuinely correct. Speculative mode-specific review needs (notebook cell execution, hardware-loop step ordering) should wait for someone to actually hit them. One subtle risk worth verifying when it bites: for markdown-deliverable mode the patch semantic is "rewrite the inline content section" not "fix specific edit anchors," and the canonical patch template is anchor-shaped. If a markdown-deliverable atom ever needs patching, the inherited patch prompt may produce confusing patches (looking for `old_text` that doesn't exist in the deliverable). Flag for the first markdown-deliverable patch-phase run.

### Gap 3 — `atom-shapes.yaml` project-extending detection not implemented

**Current behavior**: §2.4 classifier emits canonical 4 tags (`test-code`, `multi-file`, `markdown-deliverable`, `generic`). Projects can write traps with `applies_to: [save-load]`, but those traps only fire if `save-load` happens to overlap with a canonical tag — which it doesn't, so they never fire on their own.

**Theoretical limitation**: project-specific tags can't drive trap injection. Workaround today: project authors set `applies_to: [generic]` (always fires) or `applies_to: [multi-file]` (fires when 2+ dirs touched). Both are blunt — not actually atom-shape-targeted.

**Question for projects**: are you writing traps with project-specific `applies_to` tags that don't fire, OR are you working around it by using canonical tags + accepting that traps fire on every brief (generic) or only the 2+ dir case (multi-file)?

**Fix when needed**: ~1 day. Define `<project>/.idiom/prompts/brief/atom-shapes.yaml` schema (`<tag>: { paths: [glob, ...] }`), load it in `classifyBriefAtomShape`, append matching project tags to the canonical set.

**Faultline-orchestrator comment**: at minimum make it loud now; full feature can wait. Current state is silently broken: trap authors write `applies_to: [save-load]`, trust the contract the `applies_to` field implies, and the traps silently never fire. That's worse than the feature not existing — at least without it, authors wouldn't write project-specific tags. The deferral's evidence trigger requires auditing which traps fired on each brief, which operators won't do. **Minimum acceptable ship (~30 min)**: at trap-load time, emit a loud warning when an `applies_to` tag isn't in the active tag set, naming the tag and pointing at the `atom-shapes.yaml` convention. Converts silent failure into actionable signal even if the full feature waits.

### Gap 4 — `--harden-from` / `--traps-from` override flags not implemented

**Current behavior**: §2.4 overlay only discovers via convention path (`<projectRoot>/.idiom/prompts/brief/`). No CLI flag to point at an alternate location.

**Theoretical limitation**: non-conventional repo layouts (CI environments where the overlay lives elsewhere, multi-project monorepos sharing one overlay) can't redirect.

**Question for projects**: do you have any layout where the overlay can't live at `.idiom/prompts/brief/`? Or does the convention path cover every case you'd ship?

**Fix when needed**: ~hour. Add two flags + plumb to `refineApplyProjectOverlay`. Confusion risk: low (escape hatch only fires when set).

**Faultline-orchestrator comment**: defer is correct. Convention path covers every Colony case I can think of. Multi-project monorepos and CI-environment redirection are real-but-speculative — wait for someone to actually need them. The escape hatch is cheap to add when motivated, and the surface-area cost of adding flags without a real use case is non-trivial (more docs, more test cases, more "which path takes precedence" questions).

### Gap 5 — Plain-text lint output instead of structured JSON (Colony Q1 carryover)

**Current behavior**: §2.3 inlines `idiom brief lint` stdout verbatim into the review meta-brief. The lint output is human-readable text, not JSON with stable fields.

**Theoretical weakening**: Colony's Q1 recommendation (`severity`/`category`/`file`/`line`/`message` schema) is unrealized. The patch phase parses lint findings via the review's transcription, which is heuristic.

**Question for projects**: has the text-format lint output caused a transcription/parsing problem in a real iteration? Or is the reviewer reliably converting it to actionable items?

**Fix when needed**: ~half-day. Add `idiom brief lint --json` flag, switch §2.3 to capture JSON, define the schema in `cmd/idiom/brief_lint.go` alongside the rule definitions.

**Faultline-orchestrator comment**: ship now, do not defer. This is an architectural ratchet — every new lint rule shipped against the text format, and every review/patch prompt built to parse it, makes the retrofit harder. The "wait for empirical evidence" framing requires a convergence failure to fire, by which point patches will have been built against the text shape. Half-day greenfield now beats 2-3 days retrofit later. Schema can be minimal at first (`severity`/`category`/`file`/`line`/`message`); category taxonomy extends over time. Cross-reference: §8's cross-cutting note already flagged Q1 as §2.3's most important dependency; §6.1 Gap 5 is the same finding from the implementation side and the answer is the same.

### Resolution after Colony's §9 review (2026-05-11)

Three of five gaps shipped per Colony's loud-vs-quiet-failure heuristic; two stay deferred.

| Gap | Status | Commit |
|---|---|---|
| 1. Patch template targets lint BLOCKs | **Shipped** — patcher inlines lint output via new `{{LINT_OUTPUT}}` placeholder; lint re-captured at patch time | follows |
| 2. Review/patch override paths | **Deferred** — confirmed mode-agnostic per Colony review | — |
| 3. atom-shapes.yaml extending detection | **Loud-warning shipped; full feature deferred** — orphan `applies_to` tags emit stderr WARN at trap-load time naming the orphan tag + pointing at atom-shapes.yaml convention | follows |
| 4. `--harden-from` / `--traps-from` flags | **Deferred** — convention path covers all known cases | — |
| 5. JSON lint output | **Shipped** — `idiom brief lint --json` emits a stable schema (severity/category/brief/message); refine consumes JSON, drops INFO findings, falls back to plain text on malformed JSON. Raw envelope persisted to `<iterDir>/lint.json` alongside `metrics.json` (Colony §8) for orchestrator inspection. | follows |

Colony's meta-insight (Refined principle): the "wait for empirical evidence" heuristic is correct for additive features but misapplied to gaps that fail silently, undermine architectural guarantees just shipped, or have retrofit costs that grow faster than the deferred-feature cost. Loud-failure → defer is safe; quiet-failure → ship the minimum signal-emitting version now, even when the full feature waits.

### Gap 6 — deliverable-vs-state-path conflation in meta-draft (added 2026-05-11 from Colony real-use)

**Observed**: Colony ran `idiom lane refine --spec .planning/idiom-evolution-C.1-spec.md ...` on a spec that asked: *"audit `scripts/_archive/2026-05-10-phase8/lane_postflight.sh` and produce `docs/research/lane-postflight-check-inventory.md` listing each check..."* The lane converged on three iterations, exit code 0, brief produced. **But the brief was structurally wrong**: Files To Modify pointed at `.idiom/refine/idiom-evolution-C.1/iter-2/draft.md` (refine's own scratchpad) instead of `docs/research/lane-postflight-check-inventory.md` (what the spec asked for). HARD PROHIBITIONS section explicitly forbade touching the spec's target. The brief was not dispatchable.

The 16-check enumeration with line anchors that the model produced inside the brief was correct (verified by spot-checking lines 99, 117, 138, 305, 552, 620 against the archived script — all matched). So the model did the discovery work. It just packaged the work pointed at the wrong target.

**Root cause** (`lanes/refine/draft.md` — also `draft-markdown.md`): the template had TWO `## Files To Modify` sections — one inside the brief schema (describing the brief's edit target), one at meta-state level (describing where the model writes ITS draft). Same heading, different semantics. Even a perfect model would conflate them — and across three iterations, neither the reviewer nor the patcher caught it because both reviewed shape consistency, not deliverable-vs-state alignment.

**Why both reviewer and patcher missed it**:
- Reviewer caught two structural bugs in iter-1 ("self-referential Verified Current State", "section count mismatch") and prescribed `fix-bugs-then-ship`. Both were real bugs. Neither flagged the deliverable-target mistake.
- Iter-2 patched the structural bugs; deliverable target stayed wrong. Iter-3 review found no new bugs; verdict converged on `no-improvement`.
- The deliverable-vs-state conflation is not a *shape* defect — the brief is well-formed; it just targets the wrong file. Reviewer prompts focused on shape; they don't ask "does the brief's Files To Modify match what the spec asked for?"

**Fix shipped (prompt-only, this commit)**: disambiguate the meta-draft template's two semantics.

- `lanes/refine/draft.md`: removed the second `## Files To Modify` (lines 50-54) that pointed at `{{DRAFT_PATH}}`. Renamed to `## Where you write THIS draft.md` so it doesn't collide with the brief schema's `## Files To Modify`. Added a `## Critical: two file paths, two different purposes` block BEFORE the schema is presented, with an explicit example showing the deliverable target (`docs/research/inventory.md`) vs the draft-write target (`{{DRAFT_PATH}}`).
- `lanes/refine/draft-markdown.md`: same renaming. The markdown-deliverable variant didn't have the heading collision (its schema uses `## Deliverable Path` not `## Files To Modify`) but the meta-state section still benefited from the rename for consistency.

**Harness-side follow-up (NOT in this commit, recommended next)**: deterministic deliverable-target extraction. Refine scans the spec for verb+backtick-path patterns (`produce \`<path>\``, `create \`<path>\``, `write \`<path>\``, `output to \`<path>\``, `generate \`<path>\``) and passes the first non-source-extension match as `{{DELIVERABLE_TARGET}}` placeholder to the meta-draft. The template's example Files To Modify row populates from that placeholder. Spec → harness extracts target → meta-draft instantiates with target → model fills schema. No model-side spec-parsing required. Aligns with idiom's north star ("harness owns contracts, model owns strategy"). Safety lint per §2.2.1 should require `{{DELIVERABLE_TARGET}}` placeholder in `draft-<mode>.md` overrides.

**Reviewer-side follow-up (also recommended)**: extend `review.md`'s verdict criteria to check `brief['Files To Modify'][0].path` against the spec's stated deliverable path. If they don't match, reviewer verdict is `fix-bugs-then-ship` with the diagnostic *"brief Files To Modify points at X; spec asks for Y"*. This is the loud-signal that would have caught C.1's drift at iter-1.

**Where this lands in Colony's loud-vs-quiet taxonomy**: quiet failure. Refine exited code 0, produced a non-empty brief, and the brief passed §2.1's "non-trivial output" check. The defect only surfaced when an operator read the brief and noticed the target path was wrong. Per the loud-vs-quiet heuristic, this is exactly the case where shipping the minimum loud-signal version is preferable to deferring: the prompt-only fix above is ~10 lines of template edit; the harness-side and reviewer-side follow-ups are bigger but the prompt fix alone eliminates the conflation surface even without them.

### How to surface evidence

For each gap, the "real-use feedback" trigger is one of:

- A wedge or convergence failure with a clear post-mortem pointing at the gap as cause.
- A workaround that's awkward enough the project would adopt the fix.
- A new mode/use-case that genuinely needs the deferred capability.

When a gap accumulates 2+ real-use reports of the same shape, prioritize the fix above other refine work.

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

## 9. Faultline-orchestrator feedback on §6.1 deferred gaps (added 2026-05-11)

Reviewed the five deferred gaps with fresh eyes. Position-summary first, then per-gap reasoning.

| Gap | Defer? | Why |
|---|---|---|
| 1. Patch template doesn't target lint BLOCKs | **No — ship now** | 30-min fix; the convergence guarantee §2.3 promises depends on it |
| 2. Review/patch override paths | Defer is fine | Custom-mode review concerns plausibly mode-agnostic |
| 3. `atom-shapes.yaml` extending detection | **No — at minimum make it loud** | Silent failure (project tags never fire) is worse than no feature |
| 4. `--harden-from` / `--traps-from` flags | Defer is correct | Genuine escape-hatch territory; convention covers Colony |
| 5. Plain-text lint instead of JSON | **No — ship JSON now** | Architectural ratchet: schema cost compounds, retrofit is harder than greenfield |

Three of five I'd push back on. The "wait for empirical evidence before fixing" principle is sound in general, but it's being mis-applied to cases where (a) architectural cost compounds with delay (Gap 5), (b) silent failure poisons trust without an obvious telltale (Gap 3), or (c) the gap directly undermines the architectural guarantee the section just shipped (Gap 1).

### Gap 1 — patcher must explicitly target lint BLOCKs

**Recommend: ship now, not deferred.**

The convergence guarantee §2.3 makes ("every ship-as-is brief is lint-passing by construction") rests on the patcher reliably addressing every lint BLOCK. Routing lint findings through the reviewer's transcription is a layer of indirection where information can be lost — exactly the failure mode "by construction" guarantees are supposed to eliminate.

The deferral argument ("maybe the reviewer transcribes reliably") is testable, but the test is: ship a 30-min fix that removes the dependency on reviewer-transcription quality. The risk profile is asymmetric — if reviewer-transcription is reliable, the explicit-BLOCKs target is a no-op; if it's NOT reliable, we get silent convergence failures that look like "lint passed at iter-N but didn't" because the patcher never saw what the reviewer missed.

The "real-use evidence" trigger for this gap ("review correctly listed BLOCKs but patch failed to address them") requires noticing the omission — which is exactly the kind of thing operators miss unless they're auditing intermediate state. Better to remove the failure surface than to monitor for it.

### Gap 2 — review/patch override paths

**Defer is fine. Empirical-evidence framing applied correctly here.**

The argument that "review-style concerns are mode-agnostic (did the draft satisfy the spec, are there bugs, what's the verdict)" is genuinely correct. The cases where mode-specific review matters (notebook cell execution, hardware-loop step ordering) are speculative until someone hits them.

One subtle risk worth flagging in the doc: for markdown-deliverable mode, the "patch" semantic is "rewrite the inline content section" not "fix specific edit anchors". The canonical patch template is written around anchor-shaped edits. If the markdown-deliverable patcher inherits the anchor-shaped patch prompt verbatim, it may produce confusing patches (looking for `old_text` that doesn't exist in the deliverable). Worth verifying once a markdown-deliverable atom actually goes through patch phase.

### Gap 3 — project-extending atom-shape detection

**Recommend: at minimum make it loud. Ideally ship the feature.**

The current state is silently broken: trap authors write `applies_to: [save-load]`, trust the contract the schema implies, and the traps silently don't fire. That's worse than the feature not existing — at least with no feature, authors wouldn't write project-specific tags.

The "real-use evidence" trigger for this gap is "wrote traps that don't fire" — but the failure mode is silent, so the only way to notice is to audit which traps actually fired on a given brief. Most operators won't do that audit. The gap will accumulate in latent form (Colony writes 10 project-specific-tag traps, none fire, nobody notices, drift compounds).

Minimum acceptable shipping behavior: at trap-load time, warn loudly when an `applies_to` tag isn't in the canonical-or-project tag set. Something like:

```
WARN: trap save-load.md declares applies_to=[save-load] but no atom-shape
classifier emits that tag. Trap will never fire. Add 'save-load' to
.idiom/prompts/brief/atom-shapes.yaml or use a canonical tag.
```

That converts silent failure into actionable signal. Full feature (~1 day per the estimate) can wait, but the loud-warning version is ~30 min and prevents the gap from biting unnoticed.

### Gap 4 — override flags

**Defer is correct.**

Genuine escape-hatch territory. The convention path covers every Colony case. Multi-project monorepos and CI-environment redirection are real-but-speculative; wait for someone to actually need them. The escape hatch is cheap to add when motivated.

### Gap 5 — JSON lint output (Q1 carryover)

**Recommend: ship JSON now. The "wait for evidence" framing is misapplied here.**

The architectural cost of plain-text lint output isn't paid by the projects discovering it — it's paid by every future review-phase prompt that has to parse heuristic text. Each new lint rule shipped against the text format makes the eventual retrofit harder because patch-phase parsers will have been built (and tuned) against the text shape. The longer the deferral, the more lock-in to a format that's strictly worse for the consumer.

The "evidence trigger" criteria from §6.1 ("transcription/parsing problem in a real iteration") is essentially "the lint output was ambiguous enough that the patcher missed a finding." That evidence exists only after a convergence failure. By then, the cost of the retrofit is higher AND the failure has shipped.

This is the standard case of "greenfield costs less than retrofit, even if today's behavior works." Half a day to ship JSON now. Probably 2-3 days to retrofit later, after review/patch prompts have been built around the text format. Schema can be minimal (severity / category / file / line / message); category taxonomy can extend over time.

Cross-reference: the cross-cutting note in §8 already calls this out — Q1 (lint output format) is the one §2.3 most depends on. §6.1 Gap 5 is the same finding from the implementation side, and the answer is the same.

### Meta-comment on the "evidence trigger" framing

The general principle in §6.1 ("don't design for hypothetical future requirements; wait for real-use feedback") is correct for feature additions. It's mis-applied to:

- Gaps that undermine an architectural guarantee just shipped (Gap 1 — convergence-by-construction)
- Gaps with silent failure modes (Gap 3 — traps that never fire)
- Gaps where the retrofit cost grows faster than the deferred-feature cost (Gap 5 — JSON schema)

For these, the "evidence" the principle expects to trigger the fix arrives AFTER the system has accumulated damage. Better heuristic for "defer vs ship now": does the gap fail loudly when it bites, or quietly? Loud failure → defer is safe; quiet failure → ship the minimum loud-signaling version now, even if full feature waits.
