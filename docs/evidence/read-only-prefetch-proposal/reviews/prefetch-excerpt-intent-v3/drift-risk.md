[2m  ▎ thinking[0m
  · 17137 tok · in 17038 (0 cached / 17038 new) · out 99 (36 reasoning) · ¥0.0172
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
**APPROVE with minor findings.**

The proposal is well-scoped, the contract changes are precisely bounded, the non-goals are explicit, and the acceptance tests cover the risk surface. I found no scope creep or hidden behavior change. Below are two clarifications worth resolving before implementation — neither is a blocker.

## Findings

**1. Relationship between `MaxBytesPerFile` and `MaxExcerptBytesPerFile` is underspecified.**

The doc states the existing `MaxBytesPerFile` is the "hard prefetch sampling budget" and a new internal `MaxExcerptBytesPerFile` is the model-facing cap. The natural reading is `min(MaxBytesPerFile, MaxExcerptBytesPerFile)` governs the excerpt output, but the text doesn't say so explicitly. If `MaxExcerptBytesPerFile > MaxBytesPerFile`, the excerpt budget is meaningless because the sample was already capped at the smaller value. Add a clarifying sentence in the "Proposed Contract" or "Safeguard Order" section:

> *The excerpt output is bounded by `min(MaxBytesPerFile, MaxExcerptBytesPerFile)`. The smaller of the two wins.*

**2. Test for binary detection ordering should be explicit.**

Safeguard step 5 (reject binary via NUL detection) runs *after* sampling up to `MaxBytesPerFile` but *before* excerpt construction. This ordering is correct, but the acceptance tests don't include a case where a large binary file passes the `MaxBytesPerFile` sample, is detected as binary, and is rejected — verifying that excerpt truncation doesn't accidentally expose binary content. Add one line to the edge-case cluster:

> *A large binary file detected in the first `MaxBytesPerFile` sample is rejected before excerpt construction, even though it would have exceeded the excerpt budget.*

These are small precision improvements, not design flaws. The non-negotiable boundaries (no `read_file` changes, no new config knob, no routing/gate changes, no child-agent prefetch, no blind global truncation, live-only existing telemetry fields) are all honored.
  · 20881 tok · in 18979 (7808 cached / 11171 new) · out 1902 (1460 reasoning) · ¥0.0151 · cache prefix changed: tools
