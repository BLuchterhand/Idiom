# Feature Request: `idiom brief lint` — Detect prose anchors, branching, and discovery mandates

**Date**: 2026-05-10
**Reporter**: Faultline orchestrator (Claude Code)
**Severity**: medium — these patterns wedge `plan-execute` lanes today; lint would catch them at draft time
**Related**: `BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md` (resolved); `idiom-qwen-lessons.md`

## Why now

Two recent `plan-execute` dispatches against the same brief wedged for distinct reasons:

1. **Read-budget exhaustion** (16 discovery steps, no `submit_plan`) — resolved by your recent build removing the read budget.
2. **`search_content` anti-chain gate** — after ~11 reads + 1 `search_content`, the planner's 2nd `search_content` was rejected ("you already have a successful search result with concrete hits") and the 3rd was hidden by active runtime contract ("Tool hidden by active contract: search_content is hidden for the rest of this turn"). The planner surfaced this as a `Blocker` and ended without writing `submit_plan`.

**Both wedges traced to the same brief defects** that `idiom brief lint` did not catch:

- Six "Files to Modify" rows used prose anchors ("near the top", "alongside existing simulation/world includes", "around line N") instead of literal `old_text` blocks.
- One section had **two options** ("either add a `#ifdef COLONY_TESTING` accessor OR use a different mechanism — if neither works, surface `<<QUESTION>>:`") that forced the planner to design at run time.
- 12 distinct edits across 3 files — at the upper edge of what plan-execute handles, with extra verification load piled on by the vague anchors.

The brief passed `idiom brief preflight: OK` with `(0 file-allowlist row(s), 0 file(s))` — but it had real defects that any one of the proposed checks below would have caught.

## Proposed checks (in priority order)

### 1. Block prose-anchor language in Required Edits

Scan every `### Edit N` block (or section identified as a Required Edit) for these patterns and BLOCK on hit. Suggested regex:

```
\b(near (the )?top|near (the )?bottom|alongside( existing)?|around line \d+|somewhere|the relevant section|at the end of the file|after the existing .* includes?)\b
```

Justification: every prose anchor forces the planner to spend a discovery call (read + verify) finding the actual insertion site. `plan-execute`'s `submit_plan` schema is built around exact `old_text`+`new_text` substrings or `start_line`+`end_line` ranges; prose anchors are not edit-safe and trigger anti-chain gate when the planner search-loops them.

Suggested error message:
```
Required Edit N uses prose anchor "near the top". Replace with an explicit
`old_text` block containing 2+ lines of literal file content, or with
`start_line`/`end_line` coordinates. Prose anchors force the planner to
search at run time, which exhausts discovery budgets and may trip the
search_content anti-chain gate.
```

### 2. Warn on branching language in Required Edits

Scan for:

```
\b(Option \d+|Alternatively|If .* doesn'?t|If neither|Either .* or|otherwise (try|use|add))\b
```

Required Edits should be unconditional. Branching turns the planner into a designer, which costs reads and breaks the "planner thinks once" contract.

Severity: WARN (not BLOCK) — there are legitimate uses inside HARD PROHIBITIONS or test sections; only flag in Required Edit bodies.

### 3. Block discovery-mandate phrasing

The lesson from `idiom-qwen-lessons.md` (and Colony's `qwen-known-traps.md` rule 5) is that the orchestrator owns discovery, not the lane. Briefs that say "run `idiom tool X` and use the output" deflect work the orchestrator should have done at draft time. BLOCK on:

```
\b(run (./scripts/|idiom (tool |context ))[a-z_]+ and (use|inline|paste|incorporate))\b
\b(verify .* by (running|calling) (./scripts/|idiom (tool |context )))\b
```

### 4. Warn on high edit count

Count `### Edit N` headers (or equivalent structured edit blocks). WARN at 8+ discrete edits; recommend either decomposition into sub-atoms or switching to `start_line`/`end_line` range anchoring (which is cheaper to verify than `old_text` substring matching).

Justification: the `submit_plan` planner phase verifies each `old_text` against the actual file before constructing the plan. With N edits and `old_text` anchors, that's at minimum N `read_file` calls before submit. Plan-execute scales to 8-10 file atoms when anchors are cheap; prose anchors or 12+ edits make planner verification load the actual bottleneck.

### 5. (Stretch) Anchor-presence verification

The strongest possible check: for every `old_text` block in the brief, verify the literal text appears at least once in the named file. Catches stale anchors at draft time instead of run time.

Implementation sketch:
- Brief lint runs in a workspace (`pwd`-aware) — already true for current `idiom brief lint` invocations.
- For each `### Edit N` block, parse out the `path` (from "Files to Modify" or inline) and the `old_text` body.
- `grep -F` the `old_text` against the path. Zero hits = BLOCK; multiple hits = WARN (anchor not unique).

This requires workspace context that the linter may not have today — flag as opt-in (`--verify-anchors`) initially.

### 6. (Stretch) Vague-edit detector in Files-to-Modify table

Scan the "Files to Modify" table itself for prose change descriptions. Anti-patterns: "ADD include alongside others", "REWRITE function body". Prefer "ADD `#include "x.h"` after line N's `#include "y.h"`" / "REWRITE `apply_X()` body per Required Edit N".

This is harder to mechanize cleanly — perhaps a stylistic WARN rather than BLOCK.

## Empirical baseline

The brief that wedged is preserved at `/var/folders/h5/0qmlhm750_5c0srn1523bvgw0000gn/T/qwen_cet_atom_3.2c-impl-falldetect.md` (initial version, pre-refinement). The refined version (with all prose anchors replaced and the option-1/2 split removed) is being re-dispatched. Both versions pass `idiom brief lint` today; only the refined one is expected to ship clean.

If you want a regression suite for the new checks, the initial version is a high-quality "bad brief" example. Happy to extract it into a test fixture for idiom's brief-lint test suite.

## Notes on relationship to existing surface

- `idiom brief lint`'s current output is `0 file-allowlist row(s), 0 file(s)` even when "Files to Modify" tables are present and well-formed. The table parsing appears to be either missing or extremely strict about format — separately worth a look.
- Plan-execute's runtime guards (anti-chain, tool-hidden-by-contract) are working as intended; the request here is to move catches that currently fire at run-time (planner phase, wasted MLX cycles) to draft-time (cheap text scan).
- Colony will be shipping equivalent gates in `.claude/rules/qwen-brief-skeleton.md` for its own orchestrator workflow today; if the idiom-side checks land, those become defense-in-depth rather than primary enforcement.

## Out of scope

- Routing improvements (e.g., auto-decomposing 12-edit briefs into 3 sub-atoms) — interesting but invasive.
- Workspace-context features beyond #5 above.
- Changes to the planner-phase contract itself (the anti-chain gate is doing its job; the issue is brief-side).

## Follow-up: false-positive in anchor-presence verification (observed after this feature shipped, 2026-05-11)

The new `--verify-anchors` check (item #5 above, which shipped) appears to verify every `old_text` block against EVERY required_edit_path in the allowlist, and reports failures for paths where the old_text legitimately doesn't appear. Example from a real brief:

- Files-to-Modify lists `native/src/simulation/world/designation_completion.cpp` AND `native/tests/test_designation_completion.cpp`.
- `### Edit 8 — \`native/tests/test_designation_completion.cpp\` include` has an `old_text` block containing the test file's `#include "simulation/world/megaflora_registry.h"` line.
- That `old_text` legitimately doesn't appear in `native/src/simulation/world/designation_completion.cpp` (the source file).
- Linter reports: *"anchor verification: old_text block in 'Edit 8 — ...' does not match native/src/simulation/world/designation_completion.cpp. The literal text in the brief's old_text block was not found in the file — either the anchor is stale or the file has been edited."*

The Edit header correctly names the test file path. The match against the test file presumably succeeds (no error reported for it). But the match against the unrelated source-file path produces a BLOCK-level error.

The likely root cause is filename-suffix collision: `test_designation_completion.cpp` ends with `designation_completion.cpp`, so a fuzzy/substring path resolver picks the wrong allowlist entry. Either:

- (A) Strictly attribute each Edit's `old_text` to the path named in the Edit's header, and only verify against that file.
- (B) If the linter intentionally checks every old_text against every allowlist file (defense-in-depth), demote cross-file mismatches to WARN rather than BLOCK — they're informational, not blockers.

Today the workaround is `--no-lint` (the wrapper's error message even hints at this), but that's an opt-out of all preflight, including the genuinely-useful BLOCKs.

## Follow-up: `atom_spec_lint.py` placeholder regex matches C++ template syntax (2026-05-11)

This applies to the kind-aware spec lint (`scripts/atom_spec_lint.py` in Colony, presumably analogous logic in idiom's brief preload integration).

The placeholder-detection regex is `re.compile(r"<[^>\n]+>")` (one or more chars between `<` and `>` excluding newlines). It matches genuine placeholders like `<TBD>`, `<fill me>`, `<system>` — but also matches legitimate C++ template syntax:

- `static_cast<float>(damage)`
- `std::vector<T>`
- `std::unique_ptr<T>`
- `Ref<EntityManager>`

Real example flagged today: a generated atom spec for `CET 3.2c-impl-damage` correctly included `combat_system_->apply_damage(target, static_cast<float>(damage));` in the Changes section and `cast int damage result via static_cast<float>(damage)` in Constraints. Lint blocked with `placeholder text remains: '<float>'`.

Suggested narrowing — match only when the chevron contents look like a placeholder, not a type:

```python
PLACEHOLDER_PATTERNS = [
    re.compile(r"\.\.\."),
    re.compile(r"\bTBD\b", re.IGNORECASE),
    # Placeholder chevrons: contain whitespace, "TBD", "fill", "your", or
    # are exactly one of the common placeholder words. Excludes C++ template
    # syntax like <T>, <float>, <std::string>.
    re.compile(r"<(?:[^>\n]*\b(?:TBD|FILL|YOUR|PLACEHOLDER|REPLACE)\b[^>\n]*|[^>\n]*\s[^>\n]*)>", re.IGNORECASE),
]
```

Or more conservatively, only match chevrons that are surrounded by whitespace or punctuation that wouldn't appear inside C++ template syntax:

```python
re.compile(r"(?:^|[\s.,;!?])<[^>\n]+>(?:[\s.,;!?]|$)")
```

Either approach lets specs reference real template syntax in implementation guidance while still catching `<TBD>` / `<fill me>` style placeholders.

## Follow-up: atom-spec vs implementation-brief format gap (2026-05-11)

This is the larger systemic finding: `idiom brief preload --kind generic` produces an **atom spec** template (Problem, Fix, Files to Modify, Files to Read First, Changes, Constraints, Tests, Decisions). `atom_spec_lint.py` validates that shape. But `idiom brief lint` validates **implementation brief** shape (Files To Modify with single-action rows, Verified Current State with inlined snippets, Required Edits with old_text/new_text per edit, test-anchor preload).

A spec that passes `atom_spec_lint.py` does NOT pass `idiom brief lint` because:

- Spec's Files to Modify rows are descriptive ("Add include; in function, compute and call X"); brief expects single-action rows ("ADD `#include`", separately "EXTEND function body per Required Edit N").
- Spec's Changes section is bulleted prose with code snippets; brief expects Required Edits with `old_text` / `new_text` pairs.
- Spec doesn't have a Verified Current State section; brief expects one with inlined snippets the lane will use as anchors.
- Spec doesn't have test-anchor preload (fixture body + nearest existing TEST_CASE); brief expects it inlined.

The drafted spec is useful as a design artifact and as the seed for a brief, but it's not dispatchable. The planner has to do significant conversion work between spec and brief — converting bulleted prose into old_text/new_text edits, splitting multi-action rows, inlining current-state snippets that aren't in the spec.

Two paths to resolve:

- (A) **New `kind` for implementation briefs**: `idiom brief preload --kind impl-brief` (or `--kind implementation`) produces a different template that targets the brief format directly. The drafting lane could then produce dispatch-ready briefs in one step.
- (B) **`idiom spec-to-brief` converter**: a separate command that takes an atom spec + verified anchors and produces an implementation brief. The spec stays a planning artifact; the converter is a deterministic post-processor.

(A) is more user-friendly (one step instead of two). (B) preserves the spec's value as a separate artifact and allows reuse if specs are stored.

Without one of these, the codified Colony protocol ("use the brief-builder by default") cuts in half — the brief-builder gets us to a spec, then the planner still has to hand-write the brief. Hand-writing the brief from the spec costs nearly as much as hand-writing from scratch.

Real example: a generated spec for `CET 3.2c-impl-damage` passed `atom_spec_lint.py` but failed `idiom brief lint` with 2 BLOCKs (multi-action row, prose anchor) and 4 WARNs (4-file count, missing test-anchor preload, missing verified-current-state, structure-describing anchors). The conversion work to satisfy `idiom brief lint` was ~20-30 minutes — equivalent to writing the brief by hand from the spec's content.

`atom_spec_lint.py`'s `LINE_ANCHOR_RE = re.compile(r"\bL\d+|:\d+|\blines?\s+\d+", re.IGNORECASE)` requires `L40`, `:40`, or `line 40` syntax. Specs commonly use bare numbers in markdown tables (`| native/.../foo.cpp | 296-343 | ... |`) which are equally specific.

Suggested addition: accept `\b\d+(-\d+)?\b` as a line anchor when it appears in a column adjacent to a backtick'd path in the Files to Read First table. Or, less invasive: accept bare numeric ranges anywhere in the section as long as a path-anchor column is present.
