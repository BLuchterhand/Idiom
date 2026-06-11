# Prefetch Model-Facing Excerpt Intent

## Objective

Reduce upfront prompt cost from read-only named-path prefetch without weakening
the source-evidence contract.

## Proposed Contract

Only synthetic `prefetch` results should receive a smaller model-facing source
body budget. Normal `read_file` behavior remains unchanged, so the model can
request exact follow-up ranges when the excerpt is not enough.

The workspace prefetch adapter should:

- resolve the full named file through the existing workspace path policy;
- keep the existing file-count, total-byte, generated/log, and binary
  safeguards;
- replace the current `too_large` skip for oversized source files with a
  bounded excerpt, so large eligible files provide partial evidence instead of
  no evidence;
- keep the existing `MaxBytesPerFile` option as the hard prefetch sampling
  budget and add an internal workspace constant for the smaller model-facing
  excerpt budget; do not expose a new user-facing config knob in this change;
- bound excerpt output by `min(MaxBytesPerFile, MaxExcerptBytesPerFile)`;
- emit a line-numbered prefix excerpt capped by `MaxExcerptBytesPerFile`;
- use one model-facing truncation regime for prefetch output: when the original
  file exceeds the excerpt budget, the result is excerpt-capped and counted in
  the existing `TruncatedFiles` telemetry field;
- compute exact original line count with a streaming line counter and report it
  with original byte count in the truncated summary;
- keep the non-truncated summary format unchanged:
  `Read f1 path lines 1-N (total lines: N; source: prefetch)`;
- use this exact truncated summary format:
  `Read f1 path lines 1-M (excerpt lines: M; total lines: T; bytes: B/TB; source: prefetch; truncated=true)`;
- append a neutral model-facing footer, not a strategy instruction:
  `[prefetch excerpt truncated: omitted lines M+1-T; full file not included in prefetch]`;
- report truncation in the existing `read_only_named_path_prefetch` telemetry
  via the existing `TruncatedFiles` field, which is currently wired but inert
  because oversized files are skipped before truncation can occur;
- avoid changing `read_file`, search tools, child-agent exclusion, routing, or
  completion gates.

## Safeguard Order

The workspace prefetch adapter should evaluate safeguards in this order:

1. Resolve the requested path with the existing read target resolver.
2. Exclude generated/log paths with the existing classifier.
3. Stat the path and reject directories/stat failures.
4. Open the file and sample up to the existing hard `MaxBytesPerFile` budget for
   binary detection and excerpt construction.
5. Reject the file as binary if the sampled bytes contain NUL.
6. Count original lines with a streaming line counter for text files.
7. Emit at most the model-facing excerpt budget.

This keeps binary/log/generated safeguards ahead of model-facing output while
avoiding full-file buffering.

## Benchmark Criteria

Use the existing five-file DeepSeek cache-metrics benchmark as the first
measurement target. Success for this quick win means:

- first-call prompt tokens drop by at least 40 percent versus the full-prefetch
  rerun (`61,878` prompt tokens);
- model calls remain at one or increase to at most three;
- the answer still names the provider parse, runner aggregation, model-state
  session/debug propagation, debug-log persistence, and one limitation;
- any follow-up `read_file` calls are bounded and fewer than the five serial
  reads that prefetch replaced.

## Non-Goals

- No blind truncation for all tool outputs.
- No benchmark-only behavior.
- No routing changes.
- No child-agent prefetch.
- No new telemetry counter for truncation; make the existing field live.
- No hidden loss of normal source access; this change only reduces the
  opportunistic prefetch body and keeps normal `read_file` available.

## Safety Argument

This is lower risk than global tool-output truncation because it applies only to
opportunistic prefetch evidence. The model retains normal read tools and can
ask for exact missing ranges. The truncation metadata makes omitted evidence
visible instead of silently pretending a partial excerpt is complete.

## Acceptance Tests

- A large eligible file returns a prefetched excerpt instead of `too_large`.
- The truncated summary has an exact, stable format with original line and byte
  counts.
- The truncated output footer states omitted line ranges without prescribing a
  next tool call.
- A normal `read_file` on the same path still returns the requested full range
  after prefetch.
- A runner-level fixture with a large file whose answer is contained in the
  first excerpt produces the expected answer without any model-initiated
  `read_file` call.
- Small files below the excerpt budget retain the unchanged non-truncated
  summary and full body.
- Multi-file prefetch increments `TruncatedFiles` for each truncated result and
  still honors total/file-count budgets.
- Edge cases are covered for empty files, exactly-at-budget files, and a
  one-line file longer than the excerpt budget.
- A large binary file detected in the first `MaxBytesPerFile` sample is
  rejected before excerpt construction, even when it exceeds the excerpt budget.
