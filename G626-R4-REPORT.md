# G626 round 4 report

Date: 2026-11-24  
Branch: `g626-warm`  
Implementation commit: `2fbfc0f882a612d78357a545fca9bbae6ccb87e5`  
Final binary: `bin/idiom.exe`  
BenTech HEAD: `3dc88c4100788ede69709b90f0d4e041fa5f07ae`

## Outcome

Round 4 is implemented and committed locally. The POSIX build defect is fixed,
C++ worker selection is measurement-derived, syntax-index reuse now has a
metadata probe, compile-database selection is deterministic, and C++ reference
answers name the selected database. The final binary completed the selected
50-record replay twice with zero tool stalls.

BenTech and `~/.idiom/bin/idiom.exe` were not modified. The sidecar deadline
and `cppIndexBudget` remain unchanged at 10 seconds.

## Issues: cause, kind, change

| Issue | Cause | Kind | Change |
| --- | --- | --- | --- |
| Darwin/Linux build failure | POSIX `O_NONBLOCK` was referenced through `os`, which does not define it | Wrong assumption | Use `syscall.O_NONBLOCK` in `open_flags_unix.go` |
| First broad-query stall | The round-3 fixed high worker floor could oversubscribe the host while the graph/index work was cold; the worker curve shows the 64-worker end is slower than the measured middle of the curve | Gap | Calibrate rooted I/O versus parser CPU, derive the worker count, cap the factor at 2, and serialize C++ builds per manager |
| Snapshot work on a memoized C++ index | The old cache path recomputed the full Git/content snapshot before every lookup | Gap | Store Git identity, indexed paths, directory stamps, ignore-rule stamps, and file stamps; perform a parallel metadata-only probe and rebuild only after invalidation |
| Invalidation coverage | New, deleted, ignored, tracked, and ignore-rule changes were not all represented by the fast path | Gap | Probe tracked dirty paths, indexed/working-tree file stamps, directories, and `.gitignore`; regression test covers each case |
| C++ database choice | Candidate directories were selected by discovery order/sort rather than database coverage | Gap | Prefer the database with the most unique workspace-relative `core/` entries, then newest mtime, then lexical path; deterministic fixture test covers ties |
| C++ answer provenance | Partial and successful reference answers did not consistently identify the database | Gap | `cpp_references` summaries and warnings now say `compile database: <relative path>` or `compile database: none` |
| POSIX FIFO safety review | The previous round used the wrong constant on Unix builds | Gap | Corrected the flag and retained the FIFO/safepath regression coverage |

## Profiling and cause evidence

The first broad query was measured against the clean duplicate BenTech worktree
`pc-dup-review`. A fresh `context_status` report took 4,800 ms and reported a
fresh graph (2,672 files, 36,187 symbols). A report-only `context_query` took
11,280 ms and a read-through query took 11,301 ms; both returned
`fresh_for_scope=true`, `global_fresh=true`, zero stale/missing paths, and no
refresh. This distinguishes graph-read/query work from a repeated snapshot
rebuild on a fresh graph.

The final C++ build benchmark reported 24 derived workers in its one-shot cold
run. The measured parser curve was:

| Workers | Total ms/op | Parse ms |
| ---: | ---: | ---: |
| 1 | 11,126.7 | 9,343 |
| 2 | 6,250.1 | 4,779 |
| 4 | 5,811.3 | 4,197 |
| 8 | 5,399.0 | 3,749 |
| 16 | 4,822.3 | 3,230 |
| 32 | 5,123.8 | 3,452 |
| 48 | 4,354.4 | 2,901 |
| 64 | 4,805.9 | 3,163 |

The repeated default benchmark (2 iterations) averaged 4,778.5 ms/op with
26 derived workers, 3,010 ms parse, 35.3 ms calibration, 26.2 ms parse I/O,
and 6.9 ms parse CPU. The old 8.45 s benchmark and the earlier 5.0 s fresh
tool call were not contradictory: the old benchmark included a different
cold/warm filesystem state and the old high-worker choice; the repeated final
benchmark is near the fresh tool timing. The final probe and replay evidence
also show no concurrent callers in the sequential replay, so the old stall is
not explained by a contended build mutex.

## Snapshot cache evidence

| Measurement | Result |
| --- | ---: |
| Full `BenchmarkCppSnapshot`, 1 iteration | 675.7 ms/op |
| `BenchmarkCppSnapshotCachedHit`, 1 iteration | 220.9 ms/op |
| Cached probe inputs | 1,922 files, 256 directories, 1 ignore rule |

The cache hit performs Git identity and parallel `Lstat` checks only; it does
not hash file contents. The invalidation test proves that an edit or delete of
a tracked file, a new/deleted untracked file, an ignored file create/edit/delete,
and `.gitignore` changes all force a miss.

## Exact replay

The selection is the first 50 records in
`BenTech/.claude/worktrees/pc-dup-review/docs/engineering/duplicate-logic-review/core-sim-pass2.txt`
with tool `cpp_references` or `context_query`, exit 1, and reason containing
`timed out` or `index refresh failed`: 36 `cpp_references` and 14
`context_query` records. The replay ran on the clean duplicate worktree so the
BenTech checkout remained read-only. The MCP discovery schema exposes only
pattern/path for `cpp_references`, so the selected tool/pattern/path records
were replayed with that schema's result-limit default; `context_query` used
report-only freshness because the discovery profile rejects read-through
requests. A separate final-binary read-through diagnostic was fresh and also
completed without a refresh.

| Pass | Records | Total | Maximum | Stalls |
| --- | ---: | ---: | ---: | ---: |
| Cold, final binary | 50 | 495,098 ms | 39,576 ms | 0 |
| Warm, same MCP process | 50 | 456,888 ms | 32,358 ms | 0 |

The duplicate worktree has no compile database, so every C++ reference answer
in this replay explicitly reported `compile database: none`. The deterministic
selection test names the rule and selects `build/alpha` over `build/beta` on a
coverage tie. If a workspace database is present, the answer names its
workspace-relative directory.

## Readiness and navigation

- C++ workspace/lifecycle focused suite: 10 passed, 0 failed.
- LSP base protocol, delayed cross-file index, and readiness focus: 20 passed,
  0 failed.
- The existing readiness contract remains index-level and cross-file: a
  target-file symbol alone is not treated as semantic readiness; bounded
  clangd failure is explicit partial fallback.

## Verification and hand-back gates

- `go test ./internal/repository/semantic/cpp -count=1`: PASS.
- Targeted C++ provenance/compile-database/probe tests: PASS.
- `go build -o bin/idiom.exe ./cmd/idiom`: PASS.
- `git diff --check`: PASS.
- `GOOS=darwin go vet ./...`: exit 1 on the existing `lspBaseClient` copied-lock
  diagnostics in the language-tool implementations/tests; no new diagnostic
  appeared from this change.
- `GOOS=linux go vet ./...`: the same existing copied-lock diagnostics; no new
  diagnostic appeared from this change.
- Safepath suite: 18 passed, 4 failed, 9 skipped. All four failures are the
  existing Windows symlink-fixture `ERROR_PRIVILEGE_NOT_HELD` setup failures;
  FIFO coverage passed.
- `go test ./...`: safely interrupted after about two minutes without a
  completion; it had only reported command packages and cached MCP tests.
- Final process inventory after interruption and replay: no `idiom`, `clangd`,
  `rg`, `go`, or `ginkgo` process started by this run remained.

Break checks for the new behavior tests were run and restored byte-for-byte:

- `cpp_index_test.go:183`, worker-content equality assertion inverted: the
  focused Ginkgo spec failed at worker count 2 as expected.
- `cpp_provenance_test.go:163`, stable-probe assertion inverted: the focused
  Go test failed with `probe unchanged = true, want false` as expected.

The BenTech preflight command was run exactly as required:
`bash scripts/preflight.sh --since origin/main`. It reported PASS for
diff-check and drift, but failed the tree-level `test-affected` stamp check.
Running `scripts/test_affected.sh` would write BenTech build stamps/logs, so it
was not run because the brief requires BenTech to remain read-only. The same
read-only constraint prevented running the required Sonnet `code-reviewer`
agent; I performed a manual final diff review plus the focused tests and break
checks instead.

## Pushback

The exact replay was run through the schema-valid MCP discovery surface, which
does not expose `limit` on `cpp_references` and denies read-through freshness
for `context_query`. Those two transport constraints are recorded above rather
than silently presenting the run as a byte-for-byte reproduction of the old
CLI request envelope.
