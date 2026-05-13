# Recent Implementation Localization Corpus

This directory holds experimental localization tasks derived from recent Idiom implementation logs. The corpus is meant to benchmark whether an agent can find the right files, symbols, and tests for repo-memory, routing, prompt recovery, audit, and work-package tracking changes.

The fixture intentionally stores sanitized metadata only:

- source type, timestamp, local log reference, and short SHA-256 digest
- concise user-like localization summary
- expected files, symbols, and tests
- optional sanitized benchmark evidence, including context packet candidates
  and reviewed trajectory counters
- privacy status and sensitive-content exclusion notes
- client/product area and theme coverage

It does not include transcript text, prompt text, tool payloads, source file bodies, secrets, protected content, or customer identifiers. The raw local logs remain outside this fixture and are used only as evidence during corpus construction or private review.

Benchmark reporting is experimental and requires manual corpus review. The
report defines these metrics in JSON and text output:

- `top_k_edit_path_coverage`: numerator is expected file edit paths present in
  the first `k` sanitized context packet file candidates for the matching task;
  denominator is unique expected file edit paths across corpus tasks.
- `broad_read_count`: numerator is broad read or broad discovery events from
  sanitized trajectory evidence; denominator is tasks with reviewed trajectory
  evidence for that counter.
- `zero_hit_search_count`: numerator is zero-hit search events from sanitized
  trajectory evidence; denominator is tasks with reviewed trajectory evidence
  for that counter.
- `candidate_path_waste`: candidate waste counts sanitized file, symbol, and
  test candidates that do not match expected anchors; path waste counts
  sanitized file candidates that do not match expected edit paths.

When context packet candidates or trajectory counters are not present, the
report keeps the metric fields parseable and emits failure reasons such as
`context_packet_candidates_missing` or `trajectory_input_missing`.

Validate the corpus with:

```sh
go test ./internal/eval -count=1
go test ./cmd/idiom-eval -count=1
go run ./cmd/idiom-eval -localization-corpus testdata/localization/recent-implementation-corpus.json
```
