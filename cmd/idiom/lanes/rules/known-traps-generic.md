# Known Traps — Generic

These are recurring failure SHAPES that aren't directly owned by the
per-mode lane preamble. Each entry names a reaction pattern (anchor
mismatch, prose-anchor, stale discovery hit) that the model can fall
into regardless of mode. Project-specific traps belong in
`<repo>/.idiom/prompts/rules/known-traps-<project>.md` (extends this
file via the rules-overlay seam).

Per-mode rules — write-tool availability, build/test ownership,
plan-execute write-guard, multi-file-refactor re-routing — live in the
lane preambles, not here. This file does not restate them.

### Anchor mismatch

Symptom: a brief lists `old_text: foo()` but the file has `foo(arg)`
or `foo() // comment`. The executor's edit fails with "old_text not
found."

Remedy: do not invent a near-match. Either use semantic discovery or
one narrow `named_gap` read inside the affected required edit path to
refresh the exact anchor, then retry, or stop with `<<QUESTION>>:`
quoting the actual current text and the brief's expected text. Anchor
ground truth lives in the file, not in the brief.

### Structure-describing anchors

Symptom: brief uses prose like "right after the constructor" or "the
section that initializes the cache" instead of a literal source
snippet.

Remedy: structure-describing anchors are not edit-safe. Treat the
brief as needing semantic discovery or one narrow read to refresh the
literal anchor when the lane contract permits scoped evidence gathering.
If the brief gives no scoped path, symbol, section, or other bounded fact
that can lead to a literal anchor, stop with `<<QUESTION>>:` and ask for
that missing scope. Do not guess.

### Stale discovery evidence

Symptom: discovery output (workspace symbol search, references) shows
a function name or location that the file no longer has — renamed or
moved since the discovery cache was built.

Remedy: an anchor failure trumps a discovery hit. The file is ground
truth; discovery is a hint. If a discovery-suggested anchor fails,
report the actual current state; do not retry against the stale hit.
