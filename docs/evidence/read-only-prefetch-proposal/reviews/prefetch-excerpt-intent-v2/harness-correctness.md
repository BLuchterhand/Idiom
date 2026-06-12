[2m  ▎ thinking[0m
  · 17043 tok · in 16865 (16640 cached / 225 new) · out 178 (34 reasoning) · ¥0.0009
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
Let me also check for existing tests in this area to verify the "no test changes needed" claim.
  · 26775 tok · in 21973 (17152 cached / 4821 new) · out 4802 (4567 reasoning) · ¥0.0148 · cache prefix changed: tools
  -> grep {"pattern": "prefetch", "path": "internal/capabilities/workspace"}
  -> grep {"pattern": "TruncatedFiles|too_large|MaxBytesPerFile|truncated", "path": "internal/capabilities/workspace"}
  -> glob {"pattern": "internal/capabilities/workspace/*prefetch*test*"}
  -> glob {"pattern": "internal/runtime/agent/*prefetch*test*"}
[2m  ▎ thinking[0m
  · 29803 tok · in 29687 (26752 cached / 2935 new) · out 116 (19 reasoning) · ¥0.0037
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch_test.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch_test.go"}
[2m  ▎ thinking[0m
Now I have a complete picture. Let me also quickly check for any runner-layer `read_file` integration that might conflict.
  · 34247 tok · in 32468 (29696 cached / 2772 new) · out 1779 (1627 reasoning) · ¥0.0069
  -> grep {"pattern": "read_file.*footer|read_file.*suffix|bounded.*read_file|omitted.*range", "path": "internal/runtime/agent"}
  -> grep {"pattern": "MaxExcerptBytes|excerpt.*budget|excerpt.*footer"}
  · tool output truncated: 75727 of 108495 bytes elided
[2m  ▎ thinking[0m
Let me also check if the prior reviews (v2) raised issues that should have been addressed in the current intent revision.
  · 44740 tok · in 44494 (34176 cached / 10318 new) · out 246 (81 reasoning) · ¥0.0115
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent-v2/drift-risk.md"}
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/reviews/prefetch-excerpt-intent-v2/harness-correctness.md", "limit":...
[2m  ▎ thinking[0m
Let me check whether the intent document was actually revised from the v2 review, or whether it's still the same document.
  · 47790 tok · in 46975 (44672 cached / 2303 new) · out 815 (577 reasoning) · ¥0.0048
  -> bash {"command": "cd /workspace/idiom && git log --oneline -5 -- docs/evidence/read-only-prefetch-proposal/p...
  -> bash {"command": "cd /workspace/idiom && git log --oneline --all -1 -- docs/evidence/read-only-prefetch-prop...
  -> ls {"path": "docs/evidence/read-only-prefetch-proposal/"}
[2m  ▎ thinking[0m
  · 48009 tok · in 47834 (47744 cached / 90 new) · out 175 (79 reasoning) · ¥0.0014
  -> ls {"path": "docs/evidence/read-only-prefetch-proposal/implementation"}
  -> ls {"path": "docs/evidence/read-only-prefetch-proposal/reviews"}
[2m  ▎ thinking[0m
  · 48507 tok · in 48290 (48000 cached / 290 new) · out 217 (92 reasoning) · ¥0.0017
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/implementation/scope-notes.md"}
  -> read_file {"limit": 40, "path": "docs/evidence/read-only-prefetch-proposal/implementation/final-drift-review.md"}
[2m  ▎ thinking[0m
Now I have the full picture. Let me compare the current code, the v2 drift-risk findings, and whether the intent was actually revised. Let me also check if there's a separate "revised" version.
  · 52538 tok · in 50238 (48384 cached / 1854 new) · out 2300 (2042 reasoning) · ¥0.0074
  -> bash {"command": "cd /workspace/idiom && git log --all --oneline -- docs/evidence/read-only-prefetch-proposa...
[2m  ▎ thinking[0m
  · 52787 tok · in 52571 (52480 cached / 91 new) · out 216 (85 reasoning) · ¥0.0016
  -> bash {"command": "cd /workspace/idiom && git diff HEAD -- docs/evidence/read-only-prefetch-proposal/prefetch...
[2m  ▎ thinking[0m
  · 53024 tok · in 52801 (52736 cached / 65 new) · out 223 (62 reasoning) · ¥0.0016
  -> bash {"command": "cd /workspace/idiom && git status docs/evidence/read-only-prefetch-proposal/prefetch-model...
[2m  ▎ thinking[0m
  · 53312 tok · in 53115 (52992 cached / 123 new) · out 197 (88 reasoning) · ¥0.0016
  -> bash {"command": "cd /workspace/idiom && git log --all --oneline --diff-filter=A -- docs/evidence/read-only-...
[2m  ▎ thinking[0m
Here is my full re-review, grounded in the three source files, the existing tests, and the prior v2 drift-risk findings.

---

## Review Verdict: **FINDINGS — do not APPROVE for implementation yet**

The intent is directionally sound and the six prior-requirement items are named in the text, but **the same 12 issues identified in the v2 drift-risk review remain unaddressed**, and I found **2 additional code-specific issues** that will cause incorrect behavior or test breakage if the intent is implemented as written.

---

### Prior 6 Requirements — Status

| # | Requirement | In intent? | Adequately specified? |
|---|-------------|-----------|----------------------|
| 1 | Large files excerpted not skipped | ✅ Lines 19-20 | Yes, explicit. |
| 2 | `TruncatedFiles` becomes live | ✅ Lines 34-36 | Yes, explains why currently inert. |
| 3 | Neutral `read_file` footer | ✅ Line 32 | Partially — "neutral" is asserted but the described wording is still a strategy-adjacent instruction (see Finding B below). |
| 4 | Exact summary tests | ⚠️ Line 58 | Says "exact, stable format" but never specifies the format template. Without an exact template, test assertions cannot be written and different implementers will produce different strings. |
| 5 | Follow-up read test | ✅ Line 63 | Acceptance test #4 covers this. |
| 6 | No child/routing changes | ✅ Lines 43-44 | Explicit non-goals. |

---

### Unresolved v2 Drift-Risk Findings (none addressed)

I compared the intent document against the 12 findings in `prefetch-excerpt-intent-v2/drift-risk.md`. The intent document is identical in content to what v2 reviewed — **none of the 12 findings were addressed**:

| v2 Finding | Status | Impact |
|------------|--------|--------|
| #1 `MaxExcerptBytesPerFile` config vs derived constant | ❌ Unresolved | Unnecessary config surface, no default specified. |
| #2 "Single truncation regime" ambiguous | ❌ Unresolved | Describes or prescribes consolidation. |
| #3 Model receives preamble instead of `too_large`; vague "gratuitous" test | ❌ Unresolved | No concrete oracle for acceptance test #5. |
| #4 Footer is still an instruction | ❌ Unresolved | Not truly neutral (see Finding B). |
| #5 `TruncatedFiles` live = breaking schema change | ❌ Unresolved | No downstream consumer check. |
| #6 Safeguard ordering unspecified | ❌ Unresolved | Binary sniff vs excerpt ordering matters (see Finding D). |
| #7 No telemetry acceptance test | ❌ Unresolved | No test that `TruncatedFiles` increments. |
| #8 No sub-budget file test | ❌ Unresolved | No negative regression for unchanged small files. |
| #9 No multi-file prefetch test | ❌ Unresolved | No test for two oversized files in one batch. |
| #10 No error-recovery test | ❌ Unresolved | What happens on excerpt computation failure? |
| #11 No zero/edge-file test | ❌ Unresolved | Empty, whitespace-only, single-line > budget. |
| #12 `MaxExcerptBytesPerFile` boundary test | ❌ Unresolved | What if > `MaxBytesPerFile` or 0? |

---

### New Findings from Code Analysis

#### Finding A — Existing test asserts `too_large` as a skip reason

**`internal/capabilities/workspace/read_prefetch_test.go` lines 58-62:**
```go
for _, want := range []string{"excluded_logs", "binary", "too_large"} {
    if !reasons[want] { t.Fatalf(...) }
}
```
This assertion **must be removed** when excerpt replaces the skip. The test's `large.md` fixture (128 bytes) currently exceeds the 64-byte per-file budget and is skipped. After the change it should be excerpted instead. `"too_large"` also needs to go from the skip-reason assertion. The intent does not mention this required test change.

**Severity: Blocking.** The implementation will fail this test without a planned change to the fixture or assertion.

#### Finding B — The "neutral" footer is still a strategy-adjacent instruction

The intent says: "saying that omitted ranges can be requested with a bounded `read_file` call if needed."

A *neutral* footer states what happened, not what the model should do. Compare:

- **Strategy-adjacent (current wording):** `"Omitted ranges can be requested with a bounded read_file call if needed."`
- **Neutral:** `"Lines 51-832 omitted (file has 832 lines, 43 KB total)."`

The model-facing output of a *workspace tool* should not embed usage instructions. The north star says "Evidence tools and summaries should report findings, paths, symbols, counts, and uncertainty. They should not prescribe the next tool call" (AGENTS.md). The footer as proposed prescribes the next tool call.

**Severity: Should fix.** Not a blocker for correctness, but violates north-star guidance and the spirit of "neutral."

#### Finding C — Original line count is not reliably computable from the excerpt

The intent requires "original line count" in the summary (line 29). To get the exact original line count for a file larger than `MaxExcerptBytesPerFile`, you must either:
- Read the entire file up to `MaxBytesPerFile` (64 KB) + count newlines in that portion → you know lines up to `MaxBytesPerFile` bytes, but for files > `MaxBytesPerFile` you have no line count beyond that point.
- Read the full file (defeats the purpose).

The `info.Size()` call at line 128 of `prefetchReadFile` gives the total byte count, but not line count. Counting newlines in a bounded read gives `"at least N lines"`, not `"exactly N lines"`.

**Severity: Blocking.** The intent must clarify what "original line count" means. Options:
- Use `info.Size()` for byte count only; report "N lines (excerpt) of >N lines (total)" or similar.
- Accept that exact original line count is unavailable without full-file read and use `>= N` notation.
- Read up to `MaxBytesPerFile` (not just `MaxExcerptBytesPerFile`) to count lines, then show only `MaxExcerptBytesPerFile` — but this wastes IO.

#### Finding D — Safeguard ordering ambiguous with excerpt

The current `prefetchReadFile` order is:
1. `resolveReadTarget` → path validation
2. `classifySearchFilePath` → log/generated exclusion
3. `os.Stat` → isDir check
4. **`info.Size() > limit` → `too_large` skip** ← this is where excerpt is inserted
5. `os.ReadFile` → read full file
6. `bytes.IndexByte(data, 0)` → binary sniff
7. Truncation within read data if `outputBytes > limit`

After the change, if the excerpt budget reads only the first N bytes, the binary sniff on line 149 checks those N bytes for a null. For a binary file with non-null bytes in the first N bytes, the binary check would *pass* and the model would see binary content as an excerpt. The current behavior correctly reads the full file before binary-sniffing.

**Severity: Should fix.** The binary sniff must either (a) still read the full file header regardless of excerpt budget, or (b) be moved before the excerpt read to stat-based heuristics (e.g., MIME type from extension). The intent should specify which.

---

### Summary

| Category | Count |
|----------|-------|
| Previous v2 findings still unresolved | 12 |
| New blocking findings (A, C) | 2 |
| New should-fix findings (B, D) | 2 |

**Recommended path forward:** This intent needs a focused revision pass addressing the 4 blocking items (the `too_large` test change, original-line-count ambiguity, the "gratuitous" test oracle from v2 #3, and safeguard ordering) plus the `MaxExcerptBytesPerFile` design decision (v2 #1). Without these, the implementation will either break existing tests or produce incorrect/inconsistent output.
  · 55395 tok · in 53387 (53248 cached / 139 new) · out 2008 (161 reasoning) · ¥0.0052
