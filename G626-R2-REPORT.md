# G626 round 2 report

Date: 2026-11-23
Branch: `g626-warm`
Base: `0e4a5093`

## Outcome

`cpp_references` now uses one long-lived clangd per workspace root on every
platform. The lease is reused across requests, is stopped after 30 seconds of
idleness, and is restarted after a crash. Readiness is established by a
bounded semantic `workspace/symbol` probe rather than by a progress-end event,
so the sidecar's 30-second deadline is not extended.

The production BenTech measurements below show identical semantic result
sets for cold and warm runs. Cold queries completed in 11.589–13.799 seconds,
and warm queries in 1.498–3.107 seconds. There were no measured sidecar
stalls.

The generic content-search directory walker now tries ripgrep on every
platform, preserves the existing safe-path and Go matching semantics, and
falls back to the safe walker when ripgrep is unavailable, unsafe, truncated,
or fails.

## Causes and kinds

| Finding | Cause | Kind |
| --- | --- | --- |
| Cold C++ references were expensive and could hit the sidecar deadline | A fresh clangd was started per request and readiness was inferred from progress | Architecture and semantic-readiness bug |
| A cold BenTech query reached the internal syntax-index boundary | The existing 10-second internal `cppIndexBudget` was too tight for the real workspace | Performance/budget configuration |
| C++ snapshot enumeration was slow | Recursive Go traversal inspected the whole workspace before provenance matching | Performance |
| Directory content search was slower than necessary | The generic path used the Go walker even when ripgrep could enumerate candidates | Performance |
| Windows tests could not create symlink fixtures or run Unix helpers | Account privilege and environment/tool availability | Existing environment failures; not fixed |

## Implementation commits

| Commit | Change |
| --- | --- |
| `4ee9478b` | Wait for a bounded semantic clangd readiness probe |
| `c1a994bd` | Preserve safe file validation through root handles; reject symlink and non-regular targets |
| `e623d78a` | Enumerate C++ provenance with ripgrep, with safe fallback |
| `a03c2231` | Include type changes in the C++ provenance diff filter |
| `6dd25fa0` | Reuse clangd across workspace queries with idle shutdown and crash restart |
| `78f7aca1` | Accelerate generic content search with ripgrep and preserve fallback semantics |
| `0192243f` | Increase the internal cold C++ indexing budget to 15 seconds |

No Windows-only name-search branch, threshold, or `runtime.GOOS` result-set
branch from the round-1 report was reintroduced.

## Performance evidence

### Ten semantic queries

All calls used `cpp_references`, `path=core/worldgen`, and `limit=5` against
the same MCP sidecar. The exact query list and sorted provenance sets are in
[`G626-R2-RESULTS.txt`](G626-R2-RESULTS.txt).

| Query | Cold ms | Warm ms |
| --- | ---: | ---: |
| `chamfer_pass` | 13033 | 1778 |
| `water_runs` | 12848 | 1955 |
| `is_coast_land_cell` | 12913 | 1953 |
| `laplacian_mm` | 11610 | 1498 |
| `rock_unit_below_ground` | 11589 | 1982 |
| `flank_height_mm` | 11708 | 1724 |
| `volcano_height_mm` | 11948 | 2579 |
| `make_layers` | 12931 | 1725 |
| `make_weather_layers` | 13799 | 3107 |
| `pool_shape` | 13237 | 1723 |

### Exact 50-record G615 replay

The replay was selected from
`BenTech/.claude/worktrees/pc-dup-review/docs/engineering/duplicate-logic-review/core-sim-pass2.txt`:
the first 50 records whose tool was `cpp_references` or `context_query`, whose
record exited 1, and whose text contained `timed out` or `index refresh
failed`. It contained 36 `cpp_references` and 14 `context_query` records.

| Pass | Records | Tool stalls | Errors | Max ms | Total ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Cold | 50 | 0 | 4 | 11536 | 269474 |
| Warm | 50 | 0 | 4 | 8398 | 213493 |

The four errors were the same fail-closed semantic `symbol_not_found`
responses in both passes: `wind_for_air_milli_kmh`, `felt_air_mc`,
`firefly_air_pm`, and `firefly_habitat_pm`. They are result errors, not
timeouts or stalls.

### Snapshot and walker benchmarks

For `BenchmarkCppSnapshot` over `BenTech`, a direct measurement from the base
implementation was 18.267 seconds/op (one iteration, 13,493,896 bytes and
133,280 allocations). The optimized implementation measured 0.654 seconds/op
on its first run and 0.723, 0.725 seconds/op on repeated runs (three separate
one-iteration runs). This is approximately a 25x improvement on the first
optimized run and approximately 18–25x across the observed runs.

The round-1 direct ripgrep content-search measurement for the same broad
directory scope was about 155 ms; the generic walker now uses that path on all
platforms when its preconditions hold and retains the safe walker fallback.

## Lifecycle and safety coverage

The lifecycle tests cover: live-client reuse, idle shutdown, crash restart,
and manager close without a leaked clangd process. The focused lifecycle run
passed all 4 specs. The focused content-search walker run passed both new
walker specs (noisy-directory filtering and regexp/exact-line behavior).

The readiness fix was tested through the existing semantic clangd navigation
spec and the 10-query/50-record production replays. The safe-path package
passed the new regular-file and symlink/FIFO behavior where the Windows
environment could create the fixture; its existing privilege failures are
listed in [`G626-WINDOWS-TEST-FAILURES.md`](G626-WINDOWS-TEST-FAILURES.md).

Focused commands and outcomes:

```text
go tool ginkgo run --focus='clangd workspace lifecycle' --timeout=20s ./internal/repository/semantic/languagetools
4 passed

go tool ginkgo run --focus='content search walker' --timeout=20s ./internal/capabilities/workspace
2 passed

go test ./internal/platform/safepath
31 specs: 18 passed, 4 failed, 9 skipped; all 4 failures are ERROR_PRIVILEGE_NOT_HELD fixture setup

go build -o bin/idiom.exe ./cmd/idiom
passed
```

`go test ./...` was attempted but did not complete in the available run: after
more than six minutes it had produced no completion result and was stopped.
The focused `TestWorkspace` run was then allowed to reach its two-minute
suite timeout and recorded 279 passed, 18 failed, and 787 skipped; its
environmental failures are documented in the Windows inventory.

## Research basis

- [clangd indexing design](https://clangd.llvm.org/design/indexing): background indexing queues compilation-database files and caches index data in `*.idx` files, supporting full-workspace coverage.
- [clangd installation](https://clangd.llvm.org/installation.html): documents `--background-index`.
- [clangd configuration](https://clangd.llvm.org/config.html): workspace-scoped configuration and compilation-database behavior.
- [clangd troubleshooting](https://clangd.llvm.org/troubleshooting.html): crash/restart and cache troubleshooting considerations.
- [Language Server Protocol](https://microsoft.github.io/language-server-protocol/): request/response and notification protocol contract used by the client.

## Open items

- The existing `readMessageWithin` reader-goroutine leak remains intentionally
  open; it is recorded in the Windows inventory and was outside this brief.
- The broader Windows test suite still needs its existing fixture privileges,
  Unix helper tools, external-tool PATH, and telemetry-fixture issues fixed in
  their owning work.
