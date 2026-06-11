[2m  ▎ thinking[0m
  · 16900 tok · in 16819 (16768 cached / 51 new) · out 81 (27 reasoning) · ¥0.0005
  -> read_file {"path": "docs/design-hosted-cache-cost-metrics.md"}
[2m  ▎ thinking[0m
**Review: docs/design-hosted-cache-cost-metrics.md** — Role: TDD / Acceptance Reviewer  
**Result: PASS_WITH_RISKS**

---

## Summary

The design separates provider facts from pricing cleanly, names a clear JSON field contract, and states backwards compatibility as a principle. But the test plan is too abstract to drive TDD: it references test *patterns* (`-run 'Usage|Cache|Reasoning'`) and wildcard files (`*test.go`) rather than naming concrete test files, fixtures, or invariant assertions. The benchmark rerun acceptance lacks any pass/fail criterion.

---

## Findings

### 1. No concrete test files or fixture references

Every atom names only `-run` regex patterns and vague file globs:
- *"focused runner tests in `internal/runtime/agent/*test.go`"* (A5.2)
- *"focused tests beside those packages"* (A5.3)
- *"Use captured JSONL fixtures or rerun the DeepSeek Flash benchmark"* (A5.4 — an **or** choice, not a deterministic test)

A TDD review needs exact test file names (e.g. `deepseek_cache_test.go`, `runner_cache_test.go`) and JSON fixtures for DeepSeek responses with/without cache fields.

### 2. Stated invariants lack test assertions

| Stated invariant (source facts / design) | Test required but missing |
|---|---|
| `prompt_tokens == prompt_cache_hit_tokens + prompt_cache_miss_tokens` when present | No assertion validates this sum or flags provider drift |
| `addUsage` must reject/skip negative values (design: "sum non-negative values") | No negative-value test case |
| `prompt_cache_hit_rate` emitted only when `hit + miss > 0` | No guard-against-zero-division test |
| `cache_adjusted_prompt_tokens` falls back to `prompt_tokens` when cache unknown | No fallback-path test |

### 3. Backwards compatibility is stated, not tested

Acceptance criteria say *"existing usage tests still pass"* and *"existing raw token counters remain unchanged"*, but there are no explicit backwards-compat tests for:
- Parsing an old (pre-change) JSONL log that lacks the new fields
- TUI rendering an old session summary with no cache fields
- `post_analyze` / `explain` readers receiving the old-shaped JSONL

The doc relies on the existing test suite continuing to pass, which is necessary but not sufficient — you also need tests that prove old-format data survives the new code.

### 4. Benchmark rerun acceptance has no pass/fail criterion

The Verification Plan step 4 says:

> *Final DeepSeek Flash benchmark rerun with `reasonix run --model deepseek-flash` and rebuilt local Idiom binary.*

Two problems:
- **`reasonix`** is the wrong binary name (should be `idiom` or the benchmark tool).
- No threshold or comparison methodology: what makes this rerun a pass? Must hit rate exceed 50%? Must cache-adjusted cost be reported? Must old and new raw-token totals match within a tolerance?

Without a clear acceptance gate the rerun cannot fail — it's a demonstration, not a verification.

### 5. No end-to-end integration test

Focused package tests cover parsing (A5.1), aggregation (A5.2), and display (A5.3) in isolation, but no test feeds a recorded DeepSeek response through the full pipeline and asserts the cache fields survive into the final JSONL and TUI summary. That's the main risk of silent truncation or serialization loss.

---

## Recommendations (actionable before implementation)

1. **Name at least one concrete test file per atom** — e.g. `llama/deepseek_cache_test.go`, `agent/runner_cache_test.go`, `tui/session_cache_test.go`.
2. **Add JSON fixture files** for: a DeepSeek response with full cache fields, one without cache fields, one with only reasoning tokens.
3. **Add invariant assertions** for: `prompt_tokens == hit + miss` (or diagnostic flag), `addUsage` non-negative, hit-rate zero-division guard, `cache_adjusted_prompt_tokens` fallback.
4. **Add an explicit backwards-compat test** that parses an old-format JSONL and asserts no error + zero-valued new fields.
5. **Define a verifiable benchmark rerun gate** — e.g. "the JSONL output contains `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens`" and "raw token totals match pre-change totals within 0.1%".
  · 23076 tok · in 20847 (17152 cached / 3695 new) · out 2229 (1218 reasoning) · ¥0.0085 · cache prefix changed: tools
