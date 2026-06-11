[2m  ▎ thinking[0m
  · 17198 tok · in 16921 (13312 cached / 3609 new) · out 277 (29 reasoning) · ¥0.0044
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/incident-root-cause.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/north-star-architecture.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/model-runtime-config.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/harness-go-focused.md"}
[2m  ▎ thinking[0m
Now let me cross-reference each required change from all four reviews against the current proposal text.
  · 41361 tok · in 38445 (17152 cached / 21293 new) · out 2916 (2164 reasoning) · ¥0.0275 · cache prefix changed: tools
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "answer_succeeded|succeeded:false|asymmet...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "base\\.md|confounding|baseline.prompt"}
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "verbatim|summariz|summaries"}
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "backtick|line.suffix|StripPath|:N|#L"}
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "source.*prefetch|prefetch.*source|discri...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "round.trip.*eliminat|eliminat.*round|mod...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "ToolResult|synthetic|prefetch tool|prefe...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "DirectAnswerSurfaceCap|surface.gate|capa...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "resolveSafeReadPath|policy adapter|reusa...
  -> grep {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md", "pattern": "route.stability|phase.transition|mutatio...
[2m  ▎ thinking[0m
All evidence gathered. Here is the drift review.

---

## Final Drift Review: Read-Only Named-Path Evidence Prefetch

Reviewed against:
- `docs/evidence/read-only-prefetch-proposal/reviews/incident-root-cause.md`
- `docs/evidence/read-only-prefetch-proposal/reviews/north-star-architecture.md`
- `docs/evidence/read-only-prefetch-proposal/reviews/model-runtime-config.md`
- `docs/evidence/read-only-prefetch-proposal/reviews/harness-go-focused.md`

---

### Verified Facts

| Fact | Source |
|---|---|
| All four reviewers returned **approve-with-changes** | Each review's final verdict |
| Proposed status header reads "reviewer-approved design, pending implementation" | Proposal line 3 |
| 12 distinct required changes were requested across the four reviews | Aggregated from verdict sections |
| All 12 required changes are present in the current proposal text | Cross-referenced below |

**Cross-reference: every required change against current proposal text**

| # | Requested Change (Reviewer) | Proposal Location | Status |
|---|---|---|---|
| 1 | Acknowledge Reasonix `answer_succeeded: false` asymmetry (Incident) | Line 19: "That fixcheck trial was marked `answer_succeeded:false`, so the raw latency comparison is not a clean correctness-equivalent win." | ✅ |
| 2 | Tighten root cause to invariant level (Incident) | Lines 33–37: "When a read-only prompt explicitly names… the harness should be able to inject known, permission-safe source evidence before the first model turn without deciding the model's answer or strategy." | ✅ |
| 3 | Note baseline-prompt load failure as confounding factor (Incident) | Lines 27–29: "That is a confounding setup issue and should be fixed independently before treating benchmark deltas as stable." | ✅ |
| 4 | Add dedup semantics to evidence ledger (Incident) | Lines 176–183 (dedicated "Deduplication" section); lines 198–200 (test) | ✅ |
| 5 | Add "thin answer" regression test — model gathers beyond prefetch (Incident) | Lines 205–208: "The model can still request a normal follow-up read/search after prefetch" + "A prompt whose correct answer requires evidence outside the prefetched set still allows the model to gather that additional evidence before answering" | ✅ |
| 6 | Remove unsupported diagnostic-log misclassification claim (Incident) | No diagnostic-log misclassification claim appears in the current proposal text. Only the baseline-prompt failure is cited as a setup issue. | ✅ (absent = resolved) |
| 7 | Replace "excerpts or summaries" with "verbatim excerpts" — no AI summaries (North-Star Arch) | Lines 67–69: "bounded **verbatim** file excerpts as model context. The harness must **not summarize**, rank, or interpret the file contents." | ✅ |
| 8 | Add backtick and line-number-stripping rules to path extractor (North-Star Arch) | Lines 155–157: "strip surrounding backticks or quotes from the path token; strip a trailing `:N`, `:N-M`, `#L<N>`, or `#L<N>-L<M>` line suffix" | ✅ |
| 9 | Add `source: "prefetch"` discriminator to evidence ledger (North-Star Arch) | Line 66: "with `source:"prefetch"` so audit can distinguish host-prefetched evidence from model-initiated tool reads." | ✅ |
| 10 | Document route-stability assumption (read-only → no authority bleed to mutation phase) (North-Star Arch) | Lines 182–185: "If a later conversation turn or sub-phase becomes mutation-capable, the prefetched packet grants no write authority and must not expand required or allowed edit scope." | ✅ |
| 11 | Add `model_calls_saved` / `round_trips_saved` field to telemetry schema (Model-Runtime Config) | Line 72: "estimated eliminated round trips" in structured event; line 227: "estimated eliminated round trips" in benchmark summary; line 238: same in rollout schema | ✅ |
| 12a | Extract `resolveSafeReadPath` on `Manager` — no ad hoc validation (Harness/Go) | Lines 90–93: "A policy adapter extracted from workspace.Manager read-path checks, for example resolveSafeReadPath, so prefetch and read_file share one path safety authority." | ✅ |
| 12b | Use synthetic `workspace.ToolResult` — not separate evidence pipeline (Harness/Go) | Lines 95–99: "Synthetic workspace.ToolResult entries for prefetched content so existing answerability and final-answer acceptance consume the evidence through the normal result pipeline." | ✅ |
| 12c | Activate `source_body_prefetch` surface gate in same commit (Harness/Go) | Lines 100–103: "Activation of the existing direct-answer surface gate by advertising DirectAnswerSurfaceCapSourceBodyPrefetch and bumping the surface version in the same implementation change." | ✅ |
| 12d | Add recommended regression tests: policy parity, acceptance pipeline, mutation-lane isolation, dedup+audit (Harness/Go) | Lines 210–213 (policy parity), 195–198 (acceptance pipeline), 201–202 (mutation-lane isolation), 198–200 (dedup+audit) | ✅ |

**Summary: 15/15 individual requirements from 4 reviews — all addressed.**

---

### Remaining Drift Risks

1. **Default cap of 6 files vs the Go reviewer's recommendation to start at 4.** The reviewer explicitly noted this "is a conservative recommendation, not a required change" — so keeping 6 is not drift. Low risk; can be tightened in implementation if benchmark evidence warrants it.

2. **The `estimated eliminated round trips` metric is descriptive, not directly named `model_calls_saved`.** The model-runtime reviewer suggested `model_calls_saved` or `round_trips_saved`. The proposal uses the phrase "estimated eliminated round trips" in three distinct locations (lines 72, 227, 238). The naming difference is cosmetic — the concept is present and auditable.

3. **No explicit "Route-Stability Assumption" section heading.** The concept is addressed inline (lines 182–185) rather than a dedicated titled section. This is an organization preference, not a content gap.

None of these are blocking. All actionable review feedback is incorporated.

---

### Required Changes Before Implementation

**None.** The proposal satisfies all review requirements. No drift remains.

---

### Final Verdict: **approve**

The proposal is ready for implementation. All four reviews returned approve-with-changes; every specific required change is present in the current document. The design remains well-localized to read-only evidence preparation and workspace read policy, with no scope creep into mutation lanes, prompt layers, or safety policy. The 12 distinct requirements are each directly addressable by the proposal's text as written.

Proceed to implementation following the rollout sequence in §Rollout (lines 231–239), with the in-scope items listed in §Locality Boundary (lines 87–104).
  · 44837 tok · in 42336 (41344 cached / 992 new) · out 2501 (938 reasoning) · ¥0.0068
