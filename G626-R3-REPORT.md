# G626 round 3 report

Date: 2026-11-24  
Branch: `g626-warm`  
Final binary: `bin/idiom.exe`  
BenTech HEAD: `3dc88c4100788ede69709b90f0d4e041fa5f07ae`

## Outcome

The raised 15-second C++ budget was reverted to the required 10 seconds. The
syntax index is now reused inside a workspace manager under a provenance key,
with dirty, untracked, ignored, changed, and deleted C++ content represented in
that key. Cold parsing is parallel and deterministic. On the final binary, a
fresh `cpp_overview` built 1922 files in 5023 ms, and a fresh semantic reference
call built the same index and returned in 6490 ms. Warm reference calls reused
the index and measured 1174–1397 ms.

When clangd cannot prove cross-file readiness within its bounded 1.5-second
optional window, the answer is explicitly marked partial and falls back to
name-based text evidence. The syntax result itself remains fresh and reports
the memoized index key. No sidecar deadline was changed.

## Issues, causes, kinds, and changes

| Issue | Cause | Kind | Change |
| --- | --- | --- | --- |
| 15-second syntax budget | Round 2 raised the limit instead of removing the cold cost | Wrong assumption | Reverted `0192243f`; `cppIndexBudget` is 10 seconds |
| Cold C++ index cost | Each call reparsed the full workspace; parsing was serial | Gap | Added manager-local provenance-keyed reuse and deterministic parallel file parsing |
| Variable cold parse utilization | The effective Go CPU count underused independent rooted reads on this workstation | Gap | Added a 64-worker floor, capped by the number of indexed paths |
| Stale reused index | The cache identity did not cover every working-tree state | Gap | Snapshot hashes dirty, untracked, ignored, changed, and deleted C++ paths; cache stores the exact key |
| FIFO/special-file hang | `openat` could block before regular-file validation | Gap | Shared rooted open path now Lstats before open, uses POSIX `O_NONBLOCK`, and re-Stats with `SameFile`; FIFO regression has a timeout guard |
| False clangd readiness | A symbol in the just-opened file did not prove the cross-file index was ready | Wrong assumption | Readiness requires a using-file reference; failed readiness is bounded, marked partial, and suppressed until a watched-file change |
| Missing compile database | Discovery only checked a few fixed locations | Gap | Deterministically discovers `build/*/compile_commands.json` and reports the selected directory; missing databases produce partial answers |
| Warm edited documents | Releasing/reacquiring the lease could resend `didOpen` version 1 or leave disk state stale | Gap | Uses incrementing `didChange`, `didClose`, and `didChangeWatchedFiles`; caller-edit coverage is tested |
| Lease deadlock | The manager mutex could remain held while waiting for a lease that never closed | Gap | Uses a semaphore token and releases the manager mutex before waiting; bounded second-lease test added |
| `--diff-filter` missed untracked type changes | The filter omitted `U` | Gap | Uses `ACMRDTU` |
| Existing message-reader leak | `readMessageWithin` can strand its reader goroutine after a timeout and steal the next pylsp message | Existing issue | Listed under “Other existing issues” in `G626-WINDOWS-TEST-FAILURES.md`; not changed in this round |

## Research sources

- [clangd indexing design](https://clangd.llvm.org/design/indexing): background
  indexing, dynamic file indexes, and index shards/caches.
- [clangd configuration](https://clangd.llvm.org/config.html): compilation
  database and workspace configuration behavior.
- [clangd design](https://clangd.llvm.org/design/): language-server lifecycle
  and background-work model.
- [gopls design](https://go.dev/gopls/design/design): long-lived workspace
  views, invalidation, and shared analysis state.

These sources informed the choice to retain a workspace-local identity and
invalidate it from content provenance, while keeping clangd's optional answer
separate from the harness-owned syntax index contract.

## Performance evidence

### Cold

| Final-binary measurement | Result |
| --- | ---: |
| Fresh `cpp_overview` syntax-index build | 5023 ms |
| Fresh `cpp_references chamfer_pass` including index build and bounded fallback | 6490 ms |
| Syntax-index budget | 10000 ms |
| Cold margin on first semantic call | 3510 ms |

The first semantic call returned partial text evidence because clangd did not
prove cross-file readiness within 1.5 seconds. This is intentional: it keeps
the first call under the 10-second cold budget and does not present an
incomplete clangd answer as clean semantic evidence.

### Warm ten-query pass

The exact result sets and normalized paths are in
[`G626-R3-RESULTS.txt`](G626-R3-RESULTS.txt). All ten calls completed with
`index_build=memoized`, 0 stalls, and the following query times:

| Query | ms |
| --- | ---: |
| `chamfer_pass` | 1240 |
| `water_runs` | 1206 |
| `is_coast_land_cell` | 1249 |
| `laplacian_mm` | 1213 |
| `rock_unit_below_ground` | 1348 |
| `flank_height_mm` | 1174 |
| `volcano_height_mm` | 1282 |
| `make_layers` | 1262 |
| `make_weather_layers` | 1247 |
| `pool_shape` | 1397 |

The final result sets contain the Mac-required cross-file/header evidence:
`coast.h:65`, `hydrology.cpp:385`, `relief_shapes.h:42`,
`volcanoes.cpp:31`, and `small_waters.h:49`.

### Exact 50-record replay

The selection was the first 50 failing `cpp_references`/`context_query` records
from the G615 duplicate-logic replay whose reason contained `timed out` or
`index refresh failed`: 36 `cpp_references` and 14 `context_query` records.
This Windows run did not complete that mixed replay. The first selected broad
`context_query` stalled; it was stopped safely, with 1 observed stall before
stop. No cold/warm aggregate or zero-stall claim is made for the incomplete
mixed replay. The ten-query final-binary replay independently completed with 0
stalls.

### Benchmarks

| Benchmark | Result |
| --- | ---: |
| `BenchmarkBuildCppIndex` over BenTech, 1 iteration | 8452 ms/op |
| `BenchmarkCppSnapshot` over BenTech, 3 iterations | 720 ms/op |

The build benchmark reported approximately 284.6 MB/op and 1.66 million
allocations/op; the snapshot benchmark reported approximately 2.7 MB/op and
31,370 allocations/op. Snapshot timing varies with Windows filesystem state; its
identity remains content-based and is tested independently.

## Tests and break checks

- Focused clangd lifecycle, cross-file readiness, lease, precision, and warm
  document specs: **11 passed, 0 failed**.
- `go test ./internal/repository/semantic/cpp -count=1`: **passed**.
- `go test ./internal/platform/safepath`: 31 specs; **18 passed, 4 failed,
  9 skipped**. The four failures are existing Windows
  `ERROR_PRIVILEGE_NOT_HELD` symlink-fixture setup failures; the new FIFO test
  passed or skipped cleanly according to platform.
- `go build -o bin/idiom.exe ./cmd/idiom`: **passed**.
- `go test ./...`: attempted, produced no completion after several minutes,
  then stopped safely. No `idiom`, `clangd`, `rg`, Go, or test processes were
  left running.

Break checks intentionally proved the new guards matter:

- Returning from readiness as soon as the target-file symbol appeared made the
  cross-file readiness spec fail because it completed in about 15 ms instead of
  waiting for the delayed using-file evidence.
- Removing `M`, `D`, or `U` from the provenance diff filter made the respective
  dirty tracked, deleted tracked, or new untracked invalidation assertion fail.

## Guardrails

BenTech was read-only throughout the run. The sidecar deadline in
`internal/capabilities/workspace/tool_dispatch.go` was not changed, and
`~/.idiom/bin/idiom.exe` was not touched. The final process inventory was clean.
