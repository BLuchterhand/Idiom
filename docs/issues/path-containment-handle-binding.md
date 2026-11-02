# Bind workspace authorization to opened filesystem handles

Status: rounds 6-8 bind workspace file reads, directory walks, and Git metadata
updates to opened handles. Round 10 routes the remaining workspace search walks
through the shared handle-bound walker and fixes parent-relative plan artifact
loading. The reviewed path races are closed.

The issue predates this branch on `main` (`08bd82ec`). Path normalization did
not close the operation-boundary race described below.

## Reproduction

1. Create a workspace containing a directory junction `workspace/link` that
   points to another directory inside the workspace.
2. Resolve `link/secret.txt` with `PathAccess.ResolvePath` or
   `ResolveReadPath`. The resolved pathname passes containment validation.
3. Replace `workspace/link` with a junction to a directory outside the
   workspace before the caller opens the returned pathname.
4. A later pathname-based open can reach the outside target even though the
   earlier check succeeded.

Repeated pathname checks only narrow this race. The authorization must remain
bound to the object used for I/O.

## Round 6 implementation

`internal/platform/safepath` now owns the opened-handle boundary. It opens
targets relative to an `os.Root`, compares the opened object's identity with
the identity resolved during preparation, and validates the opened handle
before returning it. Reads consume that same handle. On Windows, validation
uses `GetFinalPathNameByHandleW`, checks final-path containment, and rejects an
opened reparse-point object. On Unix, `os.Root` confines root-relative
resolution and descriptor `Stat`/`os.SameFile` checks reject replacement with
a different object.

All seven semantic indexers use the shared `indexsupport.ReadFileUnderRoot`.
The Go indexer passes the bytes read through that handle to `parser.ParseFile`,
so parsing never reopens the pathname.

Workspace reads now use the same primitive through the read capability and
direct source readers. This includes language-tool source reads and directory
outlines, coding-rules inputs, plan artifact files and references, discovery
inputs, recovery and audit artifacts, directory listings, and the workspace
history and external-tool configuration readers. Plan and discovery artifact
writes use `os.Root`-relative atomic replacement. Workspace write, edit,
delete, rename, and rollback paths already use rooted mutation scopes and
atomic writers; their initial roots now use the validated root opener.

The deterministic swap tests stage the real preparation and root-open steps,
then replace a parent directory with an external symlink (Unix) or junction
(Windows) before opening the file. Windows coverage also attempts direct file
symlink and junction reads; file-symlink coverage skips when the host does not
grant symlink creation. Existing containment tests remain unchanged.

## Round 8 implementation

`safepath.WalkDirUnderRoot` enumerates each directory through its opened root,
captures entry identity with root-relative `Lstat`, and opens every child
directory relative to its opened parent. It rejects symlink/reparse traversal
and child identity changes before descending. Workspace audit discovery, path
suggestions, source discovery, semantic indexes and language detection,
comprehension, coding rules, plan scope recovery, project artifact summaries,
prompt routing, audit watch scans, and workbranch snapshots now use the shared
walk. A deterministic swap spec replaces a listed directory with an external
symlink or junction before descent and confirms the outside entry is never
visited.

The audit capability-mix pass now parses its already validated log handle.
Plan artifacts load from their containing directory and basename, including
relative workspace paths. Round 10 also resolves paths that include the
workspace folder from its parent directory relative to the inferred workspace
root. Git's selected metadata directory is opened as a root; `info/exclude`
reads and atomic replacements are relative to its opened `info` directory.

## Round 10 correction

The `search_files`, `file_stats`, and `search_content` directory scans still
used `fs.WalkDir` over an opened root filesystem, whose Unix `DirEntry.Info`
could resolve metadata by pathname. They now use `safepath.WalkDirUnderRoot`;
entry information comes from `Lstat` relative to the opened parent directory.
`search_files` uses that metadata for size and modification time; `file_stats`
uses the opened file's `Stat` when its read succeeds and falls back to the
walker metadata if the read fails.

`LoadPlanArtifact` now passes workspace artifact paths relative to the root
inferred from `.idiom/plans`, including paths supplied from the workspace's
parent directory.

## Remaining work

None of the path-containment findings covered by this issue remain open.
