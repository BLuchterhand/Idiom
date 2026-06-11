[2m  ▎ thinking[0m
  · 17150 tok · in 17042 (0 cached / 17042 new) · out 108 (45 reasoning) · ¥0.0173
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
**APPROVE.** The document aligns well with all three focus areas. Here's the evidence:

### Neutral footer — ✓ strong alignment

- **Line 35-36** explicitly requires a *neutral* footer, not a strategy instruction:
  ```
  [prefetch excerpt truncated: omitted lines M+1-T; full file not included in prefetch]
  ```
  This states what happened (fact), not what to do next (strategy). The model decides whether to follow up or proceed.

- **Acceptance test (line 94-95)** explicitly validates this: *"states omitted line ranges without prescribing a next tool call."* Good — the test gates on the correct invariant.

### Model owns strategy — ✓ strong alignment

- The excerpt reports exact original line/byte counts (lines 29-30) as metadata, leaving the model to decide sufficiency.
- Normal `read_file` remains available (lines 11-12), so the model has the tool to drill deeper when it judges the excerpt insufficient.
- No prompt nudges, no "you should read more if…", no completion-gate changes — the harness just provides bounded evidence + metadata.

### Prefetch-only scope; `read_file` unchanged — ✓ strong alignment

- **Line 10-12**: *"Only synthetic prefetch results should receive a smaller model-facing source body budget. Normal read_file behavior remains unchanged."*
- **Line 40-41**: *"avoid changing read_file, search tools, child-agent exclusion, routing, or completion gates."*
- **Non-goals (lines 73-80)** reinforce this: no blind truncation, no routing changes, no child-agent prefetch, no hidden loss of normal source access.

### Minor observations (not blockers)

1. **Benchmark (line 66):** "model calls remain at one or increase to at most three" — an increase to 3× the original call count is a wide band. The scenario (one prefetch answer + up to two `read_file` follow-ups for missing evidence) is reasonable, but I'd suggest tracking the ratio of "answers with 0 follow-up reads" vs "answers with 1-2 follow-up reads" as a quality signal after landing.

2. **Line 38:** claims the `TruncatedFiles` field is "currently wired but inert because oversized files are skipped before truncation can occur" — verify this claim exists in the actual code before assuming no wiring work is needed, but that's an implementation concern, not an intent concern.

**Summary:** The document correctly limits truncation to the prefetch path, keeps `read_file` untouched, uses a neutral fact-stating footer, and leaves follow-up strategy entirely to the model. No findings that require revision.
  · 20195 tok · in 18992 (7808 cached / 11184 new) · out 1203 (631 reasoning) · ¥0.0137 · cache prefix changed: tools
