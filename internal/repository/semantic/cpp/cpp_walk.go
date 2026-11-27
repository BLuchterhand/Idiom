package cpp

import (
	"os"
	"path/filepath"
	"time"

	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/internal/indexsupport"
)

// cppMinWalkedDirsPerCall is the directories a walk enters before it honours
// its deadline, so a call that arrives with its deadline spent still makes
// progress. Listing a directory takes about 0.35 ms idle (G907 bench: 256
// directories in 94 ms) and about ten times that under load (G882: 1.6 s
// became 18 s), so the overrun stays near 0.1 s.
const cppMinWalkedDirsPerCall = 32

// walkCppPaths appends the C/C++ files under root to paths, continuing from
// pending when it is not nil. It stops at the deadline once it has entered
// cppMinWalkedDirsPerCall directories and returns where to carry on.
func walkCppPaths(root string, scope string, opts BuildOptions, paths []string, pending []safepath.WalkCursor) ([]string, []safepath.WalkCursor, error) {
	entered := 0
	stop := func() bool {
		entered++
		return entered > cppMinWalkedDirsPerCall && indexsupport.ContextErr(opts.Context) != nil
	}

	left, err := safepath.WalkDirUnderRootResumable(root, pending, "C++ index", stop, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if !indexsupport.ShouldVisitDir(root, path, scope) {
				return filepath.SkipDir
			}

			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		if !isCppPath(path) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) || !indexsupport.PathInScope(rel, scope) {
			return nil
		}

		paths = append(paths, rel)

		if opts.MaxFiles > 0 && len(paths) >= opts.MaxFiles {
			return filepath.SkipAll
		}

		return nil
	})

	return paths, left, err
}

// stoppedCppWalk returns the checkpoint of a walk its deadline stopped: the
// files found so far and where to carry on, plus the unchanged files of the
// previous index so a resumed build still reuses them.
func stoppedCppWalk(index *CppIndex, paths []string, pending []safepath.WalkCursor, walkStarted time.Time, opts BuildOptions) (*CppIndex, error) {
	index.Timings.Walk = time.Since(walkStarted)
	index.Paths, index.WalkPending = paths, pending

	if opts.Previous != nil {
		for _, file := range opts.Previous.Files {
			if !opts.ChangedPaths[file.Path] {
				index.Files = append(index.Files, file)
			}
		}
	}

	return index, &BudgetError{Phase: "walk", Timings: index.Timings, Cause: indexsupport.ContextErr(opts.Context)}
}
