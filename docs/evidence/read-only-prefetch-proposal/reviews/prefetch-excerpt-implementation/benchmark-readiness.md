# Benchmark Readiness Review: Prefetch Excerpt Implementation

Status: approve for one-shot validation; metric aggregation gap remains for
ongoing regression tracking.

## Verified Evidence

- `readOnlyPrefetchEvent` records eligibility, selected/skipped paths, bytes,
  lines, truncation count, duplicate count, and estimated round trips saved.
- The runner emits one `read_only_named_path_prefetch` event for every attempt,
  including guarded misses.
- Benchmark `summary.json` records aggregate model calls, tool calls, prompt
  tokens, peak prompt tokens, completion tokens, duration, success state, and
  answer text.
- The benchmark summary does not currently aggregate prefetch hit/skip/byte
  metrics or per-tool call counts.

## Criteria Assessment

| Criterion | Assessment |
| --- | --- |
| First-call prompt tokens drop by at least 40% | Partially measurable. `peak_prompt_tokens` is a likely proxy for a single-call prefetch run, but exact first-call tokens require conversation JSONL parsing. |
| Model calls remain one to three | Directly measurable from `summary.json`. |
| Answer still names the required concepts | Measurable from recorded answer text, but not automated by a benchmark oracle. |
| Follow-up `read_file` calls are bounded and fewer than five | Requires conversation JSONL parsing because summary output has only total tool calls. |

## Gaps

- `summary.json` lacks prefetch-specific aggregate fields. This blocks
  transcript-free regression attribution across repeated benchmark runs.
- `summary.json` lacks a first-call prompt-token field.
- `summary.json` lacks per-tool call counts, so bounded `read_file` follow-ups
  cannot be checked from the aggregate alone.
- Required-concept answer scoring is manual.

## Verdict

The benchmark can validate the implementation for a one-shot run if the
conversation JSONL is preserved and reviewed alongside the summary. For ongoing
benchmark regression detection, prefetch metrics and per-tool call counts should
be added to the benchmark summary before relying on aggregate-only comparisons.
