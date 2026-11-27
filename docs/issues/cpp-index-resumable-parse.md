# C++ index: resumable parse and partial answers (G882)

Status: implemented on pc-g882 (581907b6, 9e05bbba). Behaviour and settings:
`docs/configuration.md` (`IDIOM_CPP_INDEX_BUDGET`) and `docs/cli.md` (C++
freshness fields).

A C++ query never fails because the index ran out of time. Each call parses at
least 32 new files before it honours its deadline, answers from the partial
index with a `PARTIAL C++ index (n of m files)` summary and a
`cpp_index_partial` warning, and checkpoints parsed files by content hash so the
next call resumes. A cold index of N files completes in at most ceil(N / 32)
calls. The build budget is the caller's deadline minus a 4 s reserve and the
measured cost of this call's Git snapshot.

## G882 break checks

Each check weakened one line, ran the focused specs, then restored the line
(`git diff --quiet` clean afterwards). Every check failed as required.

1. **Parse floor removed.** `internal/repository/semantic/cpp/cpp_index.go:301`:
   `parsedThisCall >= cppMinParsedPerCall` became `parsedThisCall >= 0`.
   - "C++ index progress across calls / finishes a cold BenTech-size tree in at
     most ceil(files / cppMinParsedPerCall) calls…": "call 0 parsed 0 files,
     want 32".
   - "C++ cache retains parsed files after cancellation": "expected retained
     partial parse: &{… Files:[{Path:file00.cpp …".
2. **Previous index ignored.** `internal/repository/semantic/cpp/cpp_index.go:291`:
   `previous[file.Path] = file` became `_ = file`.
   - Cold-tree progress spec: "call 1 reparsed finished files: reused 0 of 32".
   - "reparses only the file whose content changed": "parsed 41 reused 0, want
     1 and 40".
   - "retains parsed files after cancellation": "resume: {… Files:80 …
     Parsed:80 Reused:0}".
3. **ChangedPaths ignored.** `internal/repository/semantic/cpp/cpp_index.go:314`:
   `if file, ok := previous[rel]; ok && !opts.ChangedPaths[rel] {` became
   `if file, ok := previous[rel]; ok {`.
   - "reparses only the file whose content changed": "parsed 0 reused 41, want
     1 and 40".
   - "retains parsed files after cancellation": "stale changed file: &{…
     Path:file00.cpp … Symbols:[{Name:f0 …" (the old symbol survived the edit).
4. **Snapshot-cost reservation dropped.**
   `internal/repository/semantic/languagetools/cpp_tools.go:51`:
   `time.Until(deadline)-cppIndexReserve-snapshotCost` became
   `time.Until(deadline)-cppIndexReserve`.
   - "C++ cache budget follows the caller's deadline and the snapshot cost":
     "30 s deadline, 3 s snapshot: got 26s want about 23s".
