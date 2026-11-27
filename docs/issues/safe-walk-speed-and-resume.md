# Safe walk: one validation per directory, resumable C++ walk (G907)

Status: implemented on pc-g907. Goal: a C++ index call never fails because the
file walk is slow. The walk must be fast, honour the caller's deadline and
resume where it stopped, without weakening any `safepath` guarantee.

## Guarantees `safepath` keeps

Read from `internal/platform/safepath/walk.go`, `open.go` and `open_windows.go`.
The faster walk keeps every one.

- **G1 Containment.** The walk never lists anything outside the canonical root.
  It never descends into a symlink, a junction or another reparse point.
- **G2 No swap between check and open.** Each child directory is opened relative
  to its parent's handle, never by path. The opened handle's file ID must match
  the Lstat taken through that same parent handle without following reparse
  points.
- **G3 Handle path inside the root.** The normalized final path of each opened
  directory handle (`GetFinalPathNameByHandleW`) is the root or lies under it.
- **G4 No reparse handles.** A directory handle whose attributes include
  `FILE_ATTRIBUTE_REPARSE_POINT` is refused.
- **G5 Root identity.** The opened root is the object that was resolved.
- **G6 Start path.** The walk's start path, and every resume cursor, is opened one
  component at a time; each component must be a real directory.
- **G7 `os.Root` rules.** Relative opens use `O_NOFOLLOW_ANY`, and symlinks
  resolve only inside the root.
- **G8 Exact metadata.** `Info()` is Lstat-exact and never resolves a link.

## Cause

On 256 directories and 4352 entries the walk took 3.6-4.0 s (loaded machine;
1.6 s idle in G882) against 27 ms for `filepath.WalkDir`. A CPU profile put 62%
of the time in `CleanPath` → `GetLongPathNameW`, which reopens every path
component. It ran about five times per handle check, on paths that
`GetFinalPathNameByHandleW(FILE_NAME_NORMALIZED)` had already returned with long
component names. Kind: wrong assumption (a normalized path treated as needing
normalization). Each directory was also validated twice (`validateOpenedRoot`,
then again in `readDirectoryEntries`), and every entry had its own Lstat (20%).

Measured per call on this machine: `GetFinalPathNameByHandleW` (DOS form) 51 µs,
NT form 17 µs, `Root.OpenRoot` 55 µs, `Root.Lstat` 55 µs, `Root.Stat(".")`
50 µs, `CanonicalExistingPath` 1.26 ms.

## Design

- **Validate once per directory, cheaply (G1-G7 unchanged).**
  `normalizedPathWithin` compares the two normalized paths as text,
  case-insensitively, as `samePath` and `Rel` did. Both inputs come from
  `GetFinalPathNameByHandleW` (every `validateOpenedFile` caller passes a root
  from `CanonicalExistingPath`), so the compare is exact. `readValidatedDirectory`
  opens `"."` once, runs `validateOpenedFile` (identity, final path, reparse
  attribute) and lists the directory from that same handle.
- **Types from the listing (G2, G8 kept).** Directories are still Lstat'ed
  through the parent handle, because that Lstat is G2's identity source; on
  filesystems without `FILE_ID_BOTH_DIR_INFO` a listed entry has no file ID.
  Other entries report the type from the listing (the same `mode()` as Lstat;
  since Go 1.23 a junction is `ModeIrregular`, not a directory). `Info()` runs a
  lazy Lstat through the parent handle, as `fs.DirEntry` allows, because the
  audit watch reads `ModTime` and `Size` of logs still open for writing, which
  the directory index may hold stale.
- **Resumable walk.** `WalkDirUnderRootResumable(root, pending, label, stop, fn)`
  shares one walker with `WalkDirUnderRoot` and keeps its pre-order. It asks
  `stop` before each descent and returns `WalkCursor{Dir, After}` values
  deepest first. A resumed directory is reopened from the root through G6, so a
  cursor grants nothing a fresh walk would not.
- **C++ index.** The walk enters at least `cppMinWalkedDirsPerCall` (32)
  directories, then stops at the deadline with `BudgetError{Phase: "walk"}`, the
  files found so far and `CppIndex.WalkPending`. The next call of the same
  snapshot key resumes from the disk cache or from memory. Without a Git key the
  walk restarts, since nothing proves the tree is unchanged.

**Rejected:** a custom `NtCreateFile` with `FindFirstFileEx`
(`FIND_FIRST_EX_LARGE_FETCH`, `FindExInfoBasic`). It adds platform code for about
50 µs a directory; Go's handle-based listing already reads 64 KB buffers. Also
rejected: taking directory identity from the listing (breaks G2 where file IDs
are missing) and listing-only `Info()` (stale sizes for open files).

## Numbers

Benchmark `internal/platform/safepath/walk_bench_test.go`: 16 top directories ×
15 subdirectories, 16 files in each (256 directories, 4352 entries), 10
iterations, 3 runs.

| | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| `WalkDirUnderRoot` before | 3.98 s | 3.64 s | 3.91 s |
| `WalkDirUnderRoot` after | 97 ms | 91 ms | 94 ms |
| `filepath.WalkDir` | 28 ms | 27 ms | 27 ms |

A live cold `cpp_find_symbol` on a large workspace was not measured.

## Tests and break checks

New specs: `walk_resume_test.go` (resume without revisiting, cursor leaving the
root, `Info` read from the entry), `walk_reparse_windows_test.go` (junction never
entered, cursor through a junction refused, reparse-point handle refused, exact
path compare), `cpp_walk_resume_test.go` (walk checkpoint and resume across
calls with a spent deadline).

Each was weakened by one line, seen to fail, and restored:

| Spec | Weakening | Result |
|---|---|---|
| resume without revisiting | `name <= after` became `name < after` | fail |
| `Info` from the entry | `Info` returns the listing's copy | fail |
| junction never entered | `Type` with link bits cleared | fail |
| reparse handle refused | reparse check disabled | fail |
| exact path compare | separator suffix dropped | fail |
| C++ walk resume | pending cursor dropped | fail: "no finished build after 102 calls" |
| C++ walk resume | stop ignores the deadline | fail: "walk stopped 0 times" |

The two cursor-refusal specs still pass with both `cursorDirectory` and
`openWalkRoot`'s real-directory check weakened together: `os.Root` and the
identity check refuse the escape on their own.

The junction helper quoted its `cmd /c` argument, so `mklink` refused the syntax
and every junction spec skipped. It now passes the command line raw, and a
junction that cannot be made fails the spec instead of skipping it. The four
`rebase_test.go` specs need symlink privilege (Developer Mode) and fail in
`BeforeEach` on a machine without it; they are unchanged.

## Research

- Go `os.ReadDir` on Windows (`src/os/dir_windows.go`, `types_windows.go`):
  `GetFileInformationByHandleEx(FileIdBothDirectoryInfo)` gives attributes,
  reparse tag and file ID with each entry. Taken for entry types.
- `GetFinalPathNameByHandleW`:
  https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-getfinalpathnamebyhandlew
  and https://bugs.python.org/msg388754 (normalized form returns long names).
  Taken: the reason a text compare is exact.
- `FIND_FIRST_EX_LARGE_FETCH` and `FindExInfoBasic`:
  https://devblogs.microsoft.com/oldnewthing/20131024-00/?p=2843,
  https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-findfirstfileexw,
  https://cppstories.com/2024/cpp-query-file-attribs-faster. Rejected (above).
- ripgrep and fd walk through the `ignore` and walkdir crates:
  https://docs.rs/crate/walkdir/2.4.0/source/src/dent.rs,
  https://github.com/rust-lang/rust/issues/16237,
  https://doc.rust-lang.org/beta/std/fs/struct.DirEntry.html. On Windows they
  take metadata from the listing and do not follow links. Taken for types;
  `Info()` stays exact for the reason above.
