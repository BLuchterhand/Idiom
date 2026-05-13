# Feature Request: end-to-end qwen brief→ship parity

**Date**: 2026-05-11 (revised after closer read of refine's meta-draft)
**Reporter**: Faultline orchestrator (Claude Code)
**Severity**: feature — closing four small gaps in `idiom lane refine` gives full end-to-end qwen autonomy (1-paragraph spec → ship, no orchestrator hand-edits between phases)
**Related**: `FEATURE_REQUEST_BRIEF_LINT_HARDENING.md`, `BUG_PLAN_EXECUTE_WRITE_READY_CONTRACT_COLLISION.md`, `BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md` (resolved)

## Correction up front

An earlier draft of this filing claimed a major "design-brief vs implementation-brief format mismatch." Reading refine's actual meta-draft template (`.idiom/refine/<title>/iter-1/meta-draft.md`) closely, that framing was wrong. The meta-draft already targets the implementation-brief shape:

- "Files To Modify" with **one action per row** (NEW FILE rows exempt)
- "Verified Current State" with **`<file>:LINE` anchors**, quoted code
- "Required Behavior" steps containing **"EXACT old_text/new_text or full new-file content in ```text fences"**
- "Acceptance" with **runnable shell commands**

That's the same content `plan-execute`'s `submit_plan` schema consumes. The naming difference between "Required Behavior" (refine) and "Required Edits" (plan-execute) is cosmetic. Refine is **already aiming at impl-brief shape** — there's no two-format gap to bridge, just polish on a single format.

This filing recommends collapsing the design/implementation distinction and shipping a handful of small fixes.

## Goal

`idiom lane refine` + `idiom lane plan-execute` form a closed loop. 1-paragraph spec in, shipped atom out, no orchestrator hand-editing between the two lanes.

## Today's flow (Colony's `orchestrator-workflow.md`, 2026-05-11)

```
1-paragraph spec
   ↓
idiom lane refine             (produces "design-brief" — actually impl-brief shape)
   ↓
orchestrator manual step:     add HARD PROHIBITIONS + inline project traps + idiom brief lint
   ↓
idiom lane plan-execute       (ships atom)
```

The manual step in the middle is the parity gap. ~10-20 min per atom today.

## Real gaps, in priority order

### 1. Refine-lane silent-termination bug (validation 2026-05-11)

Validating on a small Colony spec (`.planning/idiom-evolution-C.1-spec.md`, ~10 lines, asks for a postflight-check inventory markdown file):

```bash
idiom lane refine \
  --spec   .planning/idiom-evolution-C.1-spec.md \
  --output .planning/idiom-evolution-C.1-brief.md \
  --title  idiom-evolution-C.1 \
  --max-iters 3
```

**Wrapper exited code 0. No `draft.md` produced.** Final state:

- `.idiom/refine/idiom-evolution-C.1/iter-1/meta-draft.md` exists (the prompt template).
- `.idiom/refine/idiom-evolution-C.1/iter-1/draft.md` does NOT exist.
- No `iter-2` directory, no `metrics.json`.
- JSON event log shows 2 `read_file` requests on `scripts/_archive/2026-05-10-phase8/lane_postflight.sh`, then termination. No `write_file` for `draft.md`.

**Two compounding issues**:

- **Meta-draft contradicts the lane contract**. The meta-draft says *"Do not read files. Do not search."* but the lane runs under `task_family: implementation` which permits reads. The deliverable requires reading (Verified Current State demands byte-accurate `<file>:LINE` anchors — you can't write those without reading the file). Faced with a contradiction, the model picks reads.
- **Silent termination presented as success**. Wrapper exits 0 with no `draft.md` written. The completion gate evidently doesn't enforce `required_edit_paths_satisfied`, or exit codes don't propagate.

Suggested fixes:

- Align meta-draft with lane contract: either run draft phase under a no-read sub-contract (e.g., `task_family: planner_draft` with `allowed_tools` excluding read/search), OR allow reads but cap them tightly and frame them as anchor verification ("read each file in Files To Modify exactly once to verify line anchors; do not explore").
- Wrapper exits non-zero when `--output` is empty after `--max-iters`. Today exit 0 is dishonest signal.

### 2. One format, not two

Drop the "design-brief vs implementation-brief" distinction in docs and code. There's just **the lane brief** (or **the brief**). One format, one lint, one preload taxonomy.

Concrete changes:

- Refine's docs (`docs/refine.md` if it exists; the wrapper output's prose) stop calling its output a "design-brief". Same artifact `plan-execute` consumes.
- Section headers harmonized: "Required Behavior" → "Required Edits" (or vice versa, but consistent across refine's meta-draft and plan-execute's preamble). The brief-lint regex already treats them as near-equivalents.
- Colony's `qwen-loop-protocol.md` and `qwen-brief-skeleton.md` collapse the section that calls out "refine outputs aren't dispatchable" — they will be, after these changes.

### 3. Refine review runs `idiom brief lint` internally

Today the review→patch loop converges on a reviewer verdict ("ship-as-is" / "needs work"). The reviewer prompt judges whether the draft is good; it doesn't run the deterministic lint.

This is a missed lever. `idiom brief lint` is the same lint that gates dispatch. If refine's review runs it on each iter's draft and the patch phase fixes BLOCKs/WARNs, refine converges on a lint-passing brief by construction. Today's hand-off ("orchestrator runs idiom brief lint after refine") becomes unnecessary.

Suggested:

- Review phase invokes `idiom brief lint <draft.md>` and includes the output in the review's evidence.
- Verdict criteria: `ship-as-is` requires lint exit 0 (or warnings only that the reviewer waives with rationale).
- Patch phase consumes lint findings as concrete edit targets.

### 4. Project-overlay injection

Two pieces of project-invariant content the orchestrator adds today, both of which fit the "canonical surface generic, project specifics via overlay" north star from `colony-extraction-plan.md`:

**HARD PROHIBITIONS**: Colony's prohibitions block (no out-of-allowlist edits, no working-tree-collision-revert, no interactive question tool, no builds/tests, etc.) is project-invariant. Same across every atom. Suggested: `idiom lane refine --harden-from <path>` flag reads from `<project>/.idiom/prompts/brief/hard-prohibitions.md` and appends the block to the final brief. The canonical embed stays empty; project layers its own.

**Project trap inlining**: Colony's `qwen-known-traps.md` has ~30 entries organized by domain (Doctest / C++ public API / C++ runtime / save-load / Multi-file scope / etc.). Today the orchestrator picks which traps apply by atom shape and inlines them. Suggested: a project-defined trap library with frontmatter (`applies_to: [test-code, c++-system, save-load, ...]`) and an atom-shape classifier that tags each brief, then includes matching traps. Atom-shape inference is local: look at Files To Modify paths and extensions (`test_*.cpp` → test code, `save_fields.h` → save-load, etc.).

Both flags are optional. Project that doesn't define overlays gets the canonical brief. Project that does (Colony) gets a hardened brief.

## What full parity looks like

```
1-paragraph spec
   ↓
idiom lane refine \
  --spec ... --output ... --title ... --max-iters 3 \
  --harden-from .idiom/prompts/brief/hard-prohibitions.md \
  --traps-from  .idiom/prompts/brief/traps/
   ↓ (refine internally: draft → review (runs idiom brief lint) → patch loop;
      converges on lint-passing brief that includes HARD PROHIBITIONS + traps)
   ↓
idiom lane plan-execute       (ships atom)
```

Orchestrator role becomes: write the 1-paragraph spec, review the diff post-ship. Middle is qwen end-to-end.

## Scope discussion

- **Gap 1 (refine bug)** is a clean isolated fix and should land first. It's the only thing blocking refine from producing output at all on the Colony spec.
- **Gap 2 (one format)** is mostly doc + naming. Code-wise it's section-header normalization.
- **Gap 3 (review runs lint)** is the highest-leverage feature change. Without it, refine's review is judgment-based; with it, refine converges on dispatch-ready briefs deterministically.
- **Gap 4 (overlay injection)** is two flags + a project-side prompts directory convention. Generic across projects.

## Empirical baseline

The CET 3.2c-impl-damage atom shipped today (commit `bc7bc739` on Colony's `phase8/idiom-migration` branch) used the brief-builder workflow's predecessor (`run_idiom_atom_spec.sh` → `atom-spec` → hand-normalize → self-implement from spec content). The brief-builder caught real defects (max_tokens=1024 truncation, lint false-positives on `<float>`) and was useful as a design tool — but the final atom was self-implemented because the orchestrator's hand-step still cost ~20 min. End-to-end qwen would have eliminated that cost.

Validation of refine on the same kind of small atom (idiom-evolution-C.1, a single new-markdown-file deliverable) surfaced gap #1 — refine doesn't produce output for that spec today. Once gap #1 is fixed, gaps #2-4 are the remaining polish to full parity.
