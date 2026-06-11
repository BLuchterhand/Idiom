[2m  ▎ thinking[0m
  · 17090 tok · in 16889 (16640 cached / 249 new) · out 201 (67 reasoning) · ¥0.0010
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
  -> read_file {"path": "docs/decisioning-north-star.md"}
  -> read_file {"path": "docs/idiom-maintenance-best-practices.md"}
[2m  ▎ thinking[0m
---

# North-Star / Architecture Review

**Review of:** `docs/proposal-read-only-named-path-evidence-prefetch.md`
**Against:** `docs/decisioning-north-star.md`, `docs/idiom-maintenance-best-practices.md`

---

## Verified Facts

1. The proposal separates two distinct root causes from the benchmark: (a) diagnostic-log contract drift (already fixed), and (b) slow serial evidence transport. It does not conflate them into one harness change.

2. The slow-run data is concrete and measurable: Idiom 44.47s / 15 model calls / 340k prompt tokens vs. Reasonix 19.43s / 53k prompt tokens. The gap is dominated by serial `read_file` turns (9 reads, 4 navigates, 1 search) replayed into every subsequent model call.

3. The diagnostic-log fix is listed as a separately-landed prerequisite (Rollout §1). The prefetch is a follow-on optimization for the transport class of failures only.

4. The proposal explicitly forbids the harness from deciding evidence sufficiency, synthesizing an answer, or blocking follow-up tools (§"Proposed Solution", lines 63–75).

5. Caps are explicit: 6 files max, per-file byte cap (reuse existing), aggregate byte cap, truncation-marking required.

6. Out-of-scope list is explicit and includes mutation/plan/review/recovery lanes, prompt additions, global caching, and safety-policy weakening.

---

## Inferences

1. **Path extraction will be deterministic.** The spec requires exact existing workspace-relative or absolute-under-workspace file references. Plain words, symbols, patterns, and session ids are excluded. This places extraction in the "deterministic invariant" category, not the "heuristic strategy" category.

2. **Route detection already exists.** The "read-only direct-answer or conversation-style source explanation" route is assumed to be classifiable by existing route logic. No new classifier or phrase-matching is proposed.

3. **Evidence authority parity is intentional.** Prefetched paths go through the same workspace read policy, protection checks, and evidence ledger as normal `read_file`. The proposal treats them as equivalent in authority class.

4. **No prompt-layer changes are needed.** The proposal explicitly excludes prompt reminders, strategy instructions, or magic phrases (§"Out of scope"). The transport improvement is invisible to the model's decisioning.

---

## Root Cause (as stated by proposal)

> When a read-only prompt explicitly names local source or design files, the harness should make permission-safe evidence transport efficient without deciding the model's answer or strategy.

The proposal correctly identifies this as a *transport* failure, not a *model competence* or *contract* failure. The model's strategy (read named files) was reasonable; the harness charged one model round trip per file plus replayed all evidence into every subsequent call.

---

## Contributing Factors

1. No existing mechanism to satisfy an obvious file read request before the first model turn, even when the user has already named every candidate path.
2. The final-answer prompt accumulated all prior tool output (~71k chars), creating gratuitous context cost.
3. The diagnostic-log misclassification (the other benchmark issue) obscured the transport problem during initial analysis, leading to a risk of conflating the two fixes.

---

## Non-Causes

- Model capability or prompt alignment. The model read the correct files and produced a correct answer; it just took too many turns and too many tokens doing so.
- Completion-gate or recovery-loop pathology. No loop, no ejection, no malformed output.
- Tool surface gaps or missing capabilities. All needed tools existed and worked correctly.
- Safety or permission policy. All reads were to legitimate source files within workspace scope.

---

## Fix Recommendation

**Approve with changes.** The proposal is structurally sound and well-aligned with the north star. Four concretions needed before implementation:

### 1. Verbatim excerpts only — no AI summaries

Line 67 says "bounded file excerpts or summaries." Remove "or summaries." A harness-generated summary is a strategy-level judgment about what content matters, which crosses the model/harness boundary. Prefetched content must be verbatim source excerpts (truncated with an explicit marker). If the model wants a summary, it can request one through the normal tool surface.

The drift check section (§"Drift Checks") already aligns with this — add an explicit line under "Safety Rules" that prefetched content is verbatim, not summarized.

### 2. Handle backtick- and line-number-suffixed paths explicitly

The path extractor must handle common user patterns: `` `file.go` ``, `` `path/to/file.go:42` ``, and `path/to/file.go` (bare). The current spec says "exact workspace-relative or absolute-under-workspace file reference that exists on disk" — this is ambiguous about backtick stripping and line-number suffixes. Add a rule that strips common quoting (backticks, single quotes on the path portion) and trailing `:N` / `#L` line suffixes before checking existence. This is still deterministic — no interpretation, just syntactic cleanup.

### 3. Distinguish prefetched evidence in audit from model-initiated reads

The proposal says prefetched evidence uses "the same ledger path as normal source reads." This is correct for evidence acceptance but risks conflating the two sources in audit. Add a `source: "prefetch"` discriminator to the evidence ledger entry so that:
- Audit can distinguish `read_file`-sourced evidence from prefetch-sourced evidence.
- Benchmark summaries include a `prefetch_hit_bytes` field (already in §Rollout).
- A future regression caused by missing prefetch is diagnosable without transcript reconstruction.

### 4. Document the route-stability assumption

If a conversation starts as read-only (triggering prefetch) and later transitions to a mutation sub-phase, the prefetched context should not grant any additional authority. The proposal is correct that prefetched data is just file excerpts with no write authority, but this assumption should be documented explicitly in the implementation boundary so a future change to context persistence does not accidentally carry prefetched evidence into a write lane as de facto strategy context.

---

## Regression Tests

### Minimum additions (beyond §Test Plan)

| Test | What it guards |
|------|---------------|
| Backtick-quoted path extraction | `\`file.go\`` → `file.go` stripped before existence check |
| Line-number-suffixed path extraction | `file.go:42` → `file.go` stripped before existence check |
| Path on disk but inside `.git/` is excluded | Protected-path invariant bypass |
| Prefetch + normal read: audit distinguishes sources | `source: "prefetch"` vs `source: "read_file"` in ledger |
| Prefetch occurs, model still calls `read_file` on same path | No double-counting, no suppression of model choice |
| Read-only route that does *not* name explicit paths | No prefetch — zero context injected |
| Prefetch hit in read-only route that later transitions | No evidence-authority bleed into mutation phase |
| Large file truncation marker reaches model | Model sees `[... truncated to X bytes ...]` tag in context |

---

## North-Star Risk Assessment

| Risk | Assessment |
|------|-----------|
| Harness decides strategy | None. The model still chooses when to answer, what to read next, and how to synthesize. |
| Prompt magic or scenario hardcoding | None. Deterministic path extraction, no classifier keywords, no scenario-specific nudges. |
| Tool surface degraded | None. All tools remain available. Prefetch is additive context, not a tool restriction. |
| Evidence sufficiency decided by harness | None. Prefetch is pre-first-turn context. The model can request more or answer immediately. |
| Evidence authority class mismatch | **Low risk after change #1.** With verbatim excerpts only, authority parity holds. |
| Route classification false positive | Low. A read-classified mutation prompt would get harmless file excerpts, not an answer. |
| Context budget pressure | Low. Caps (6 files, bounded bytes) are explicit and conservative. |

---

## Confidence

**High** — the proposal's design is conservative, precisely scoped, and well-separated from other fixes. The four changes requested are clarifications and hardening, not structural redesign. The north-star alignment is the strongest signal: the proposal explicitly calls out what the harness may not do, and those prohibitions match the decisioning standard's boundaries.

---

## Final Verdict

**APPROVE WITH CHANGES**

Land after:
1. Replace "excerpts or summaries" with "verbatim excerpts" only.
2. Add backtick and line-number-stripping rules to the path extractor spec.
3. Add `source: "prefetch"` discriminator to evidence ledger entries.
4. Document the route-stability assumption (read-only route stays read-only for prefetch scope).
  · 36579 tok · in 32646 (17152 cached / 15494 new) · out 3933 (2038 reasoning) · ¥0.0237 · cache prefix changed: tools
