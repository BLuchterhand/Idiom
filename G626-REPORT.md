# G626 C++ reference stall report

Date: 2026-11-23
Branch: `g626-references`
Built executable: `C:\Users\benlu\Documents\GitHub\idiom-g626\bin\idiom.exe`

## Outcome

`cpp_references` no longer stalls on the BenTech Windows worktree. The fixed
binary returned all ten representative area3 queries successfully, and an
exact replay of the 50 G615 records produced zero stalls and zero non-zero
exits. The workspace tool deadline in
`internal/capabilities/workspace/tool_dispatch.go` was not changed.

## Cause and proof

There were three sequential costs on the cold reference path:

1. C++ provenance used `git status --untracked-files=all` and
   `git ls-files -o -i --exclude-standard` over the worktree. On Windows, the
   ignored worktree forest made the latter take about 15.690 s and return
   about 81,488 paths; the status call took about 11.315 s.
2. After provenance was made bounded, every reference query still started a
   fresh clangd. The installed clangd was 23.1.0 and its background-index
   wait was configured for 30 s, so a cold semantic reference lookup consumed
   the outer tool deadline.
3. The generic Go content-search walker was also too slow for a root-scoped
   Windows query. A direct ripgrep search over the same scope took about
   155 ms, so the query needed an argv-based search fast path.

The kind gap was that the query path treated repository provenance, a full
syntax index, cold clangd startup, and the generic secure search walker as
affordable for every C++ workspace. The wrong assumption was that all of
those costs fit beneath the existing 30 s harness deadline. Raising that
deadline would hide the cause and was not done.

## Changes

- C++ provenance now uses bounded Git operations (`diff`, cached tracked
  paths, and non-ignored untracked paths) plus a discovery-scoped walk for
  the untracked files the syntax index can actually see. It no longer asks
  Git to enumerate the ignored worktree forest. Ignored C++ files remain in
  the index identity and freshness counts.
- The syntax index opens one established safe root handle and reads indexed
  files through it, avoiding repeated root setup during parsing.
- Provenance runs before the 10 s index-build context is created, so the
  parse budget measures the index build rather than slow Git identity work.
- On Windows, a bounded C++ preflight sends workspaces with more than 512
  C++ files directly to ripgrep-backed name search. This path emits explicit
  warnings and tool facts because it is not semantic owner resolution.
- Smaller workspaces retain semantic resolution; their optional clangd
  reference lookup is independently capped at 3 s before falling back to
  name search.
- Added a safe-root regression test and C++ snapshot/index benchmarks.

## Representative query measurements

These are the same ten `pattern=<name> path=core/worldgen limit=5` queries,
run from the BenTech main checkout through the fixed local binary. Before the
fix, all ten hit `cpp_index_budget_exceeded` during the 10 s index budget and
had no result set. After the fix, every query exited 0 and returned the
name-search result set.

| Query | Before wall ms | After wall ms | After output hash |
| --- | ---: | ---: | --- |
| `chamfer_pass` | 21,089 | 5,235 | `3fb16d6d28514a8c` |
| `water_runs` | 13,804 | 6,076 | `f8ec3112c10be8b` |
| `is_coast_land_cell` | 13,675 | 6,215 | `c8b29bf83b79f08c` |
| `laplacian_mm` | 13,817 | 5,973 | `3c1f4dfd6f903376` |
| `rock_unit_below_ground` | 13,841 | 6,886 | `ddd3850ccc2b09fe` |
| `flank_height_mm` | 13,791 | 5,196 | `33ef3439253687bb` |
| `volcano_height_mm` | 13,762 | 6,425 | `25c4a627df42f930` |
| `make_layers` | 13,782 | 7,041 | `bae752038650b40f` |
| `make_weather_layers` | 13,984 | 8,543 | `91cc54520bbdb8d4` |
| `pool_shape` | 14,112 | 7,286 | `06122e7b57f89d5c` |

Because the pre-fix calls failed before producing output, there is no honest
before-result hash to compare. Post-fix output was compared with direct
ripgrep over the same C++ scope: the matching line counts were respectively
3, 3, 3, 2, 6, 2, 3, 2, 3, and 6, and the displayed line sets matched.

## Exact G615 replay

The replay selected the exact first 50 records matching
`exit 1` plus `timed out` or `index refresh failed` from
`BenTech/.claude/worktrees/pc-dup-review/docs/engineering/duplicate-logic-review/core-sim-pass2.txt`.
It contained 36 `cpp_references` records and 14 `context_query` records.

| Metric | Result |
| --- | ---: |
| Records replayed | 50 |
| Stalls (`did not return within 30s` / `Tool timed out`) | 0 |
| Non-zero exits | 0 |
| Maximum wall time | 16,120 ms |

All 36 `cpp_references` records returned exit 0. The replay was run from the
BenTech main checkout in four-worker batches with the local `bin\idiom.exe`;
no BenTech build or suite was run.

## Profiling evidence

| Operation | Observed cost |
| --- | ---: |
| `git ls-files -o -i --exclude-standard` over C++ globs | ~15,690 ms / ~81,488 lines |
| `git status --porcelain=v1 --ignored --untracked-files=all` | ~11,315 ms |
| `git diff --name-only ... HEAD` | ~50 ms |
| `git ls-files --cached` | ~40 ms |
| `git ls-files -o --exclude-standard` | ~65 ms |
| Direct ripgrep reference search | ~155 ms |
| `BenchmarkCppSnapshot` on BenTech | ~2.153 s/op |
| `BenchmarkBuildCppIndex` on BenTech | ~9.291 s/op |

## Verification

- `go build -o bin/idiom.exe ./cmd/idiom`: passed.
- Focused C++ package tests: `internal/repository/semantic/cpp` passed.
- The focused `languagetools` run reached the new C++ coverage; one run also
  exposed an unrelated installed-Python-LSP 30 s timeout. The later default
  suite run completed the languagetools package successfully.
- `go test ./...`: exit 1 because unrelated Windows/environment-sensitive
  failures remain in existing TUI, product-workflow, workspace, comprehension,
  and safe-path tests, including symlink privilege and fixture/lock failures.
  The changed C++ semantic package passed in that run.
- `git diff --check`: passed.

## Open items

The large-workspace Windows path intentionally reports name-based evidence,
not semantic owner resolution, and carries an explicit warning. Other C++
tools still use the semantic index. Existing default-suite failures should be
triaged separately; they are not caused by the C++ reference changes.
