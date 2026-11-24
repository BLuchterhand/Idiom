package languagetools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
)

// cppSnapshotProbe is the cheap second-and-later look at the provenance inputs
// used by the syntax index. Git reports tracked changes; directory stamps find
// new or removed untracked/ignored files; file stamps find edits to paths that
// were already part of the previous snapshot. The content hash is recomputed
// only after one of these predicates changes.
type cppSnapshotProbe struct {
	Root                  string
	GitCommonDir          string
	Commit                string
	Tree                  string
	TrackedDirtyPaths     []string
	TrackWorkingTreeFiles bool
	Files                 []cppPathStamp
	Directories           []cppPathStamp
	IgnoreFiles           []cppPathStamp
}

type cppPathStamp struct {
	Path    string
	Size    int64
	ModTime int64
	Mode    uint32
}

func newCppSnapshotProbe(ctx context.Context, root string, info cppIndexInfo) (*cppSnapshotProbe, error) {
	probe := &cppSnapshotProbe{
		Root:                  root,
		GitCommonDir:          info.GitCommonDir,
		Commit:                info.Commit,
		Tree:                  info.Tree,
		TrackedDirtyPaths:     append([]string(nil), info.TrackedDirtyPaths...),
		TrackWorkingTreeFiles: len(info.IndexedPaths) > 0,
	}

	paths := append(append([]string(nil), info.IndexedPaths...), info.DirtyPaths...)
	paths = append(paths, info.IgnoredPaths...)
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(path))
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		stamp, err := cppPathStampFor(root, path)
		if err != nil {
			// A deleted input is still a valid provenance state. The next fast
			// check will observe the missing path and force a full snapshot.
			continue
		}
		probe.Files = append(probe.Files, stamp)
	}

	err := safepath.WalkDirUnderRoot(root, root, "C++ snapshot probe", func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && discovery.ShouldSkipDirName(entry.Name()) {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			stamp, err := cppPathStampFor(root, filepath.ToSlash(filepath.Clean(rel)))
			if err != nil {
				return err
			}
			probe.Directories = append(probe.Directories, stamp)

			return nil
		}
		if entry.Name() != ".gitignore" {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		stamp, err := cppPathStampFor(root, filepath.ToSlash(filepath.Clean(rel)))
		if err != nil {
			return err
		}
		probe.IgnoreFiles = append(probe.IgnoreFiles, stamp)

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(probe.Files, compareCppPathStamps)
	slices.SortFunc(probe.Directories, compareCppPathStamps)
	slices.SortFunc(probe.IgnoreFiles, compareCppPathStamps)

	return probe, nil
}

func cppPathStampFor(root string, rel string) (cppPathStamp, error) {
	if rel == "." || rel == "" {
		rel = "."
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(path)
	if err != nil {
		return cppPathStamp{}, err
	}

	return cppPathStamp{
		Path:    filepath.ToSlash(filepath.Clean(rel)),
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
		Mode:    uint32(info.Mode()),
	}, nil
}

func compareCppPathStamps(left cppPathStamp, right cppPathStamp) int {
	if left.Path < right.Path {
		return -1
	}
	if left.Path > right.Path {
		return 1
	}

	return 0
}

func cppSnapshotFastUnchanged(ctx context.Context, root string, probe *cppSnapshotProbe) (bool, time.Duration, error) {
	started := time.Now()
	if probe == nil || probe.Root != root {
		return false, time.Since(started), nil
	}

	output, err := gitstate.Output(ctx, root, "rev-parse", "--git-common-dir", "HEAD", "HEAD^{tree}")
	if err != nil {
		return false, time.Since(started), err
	}
	lines := strings.Fields(output)
	if len(lines) < 3 {
		return false, time.Since(started), nil
	}
	common := lines[0]
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(common); resolveErr == nil {
		common = resolved
	}
	common = filepath.ToSlash(common)
	if common != probe.GitCommonDir || lines[1] != probe.Commit || lines[2] != probe.Tree {
		return false, time.Since(started), nil
	}

	if !probe.TrackWorkingTreeFiles {
		dirty, err := cppChangedTrackedPaths(ctx, root)
		if err != nil {
			return false, time.Since(started), err
		}
		if !slices.Equal(dirty, probe.TrackedDirtyPaths) {
			return false, time.Since(started), nil
		}
	}

	stamps := make([]cppPathStamp, 0, len(probe.Files)+len(probe.Directories)+len(probe.IgnoreFiles))
	stamps = append(stamps, probe.Files...)
	stamps = append(stamps, probe.Directories...)
	stamps = append(stamps, probe.IgnoreFiles...)
	if unchanged, probeErr := cppProbeStampsUnchanged(ctx, root, stamps); probeErr != nil {
		return false, time.Since(started), probeErr
	} else if !unchanged {
		return false, time.Since(started), nil
	}

	return true, time.Since(started), nil
}

func cppProbeStampsUnchanged(ctx context.Context, root string, expected []cppPathStamp) (bool, error) {
	if len(expected) == 0 {
		return true, nil
	}

	workerCount := min(len(expected), max(1, min(runtime.GOMAXPROCS(0), 8)))
	jobs := make(chan cppPathStamp, len(expected))
	for _, stamp := range expected {
		jobs <- stamp
	}
	close(jobs)

	var failed atomic.Bool
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()

			for stamp := range jobs {
				if failed.Load() {
					return
				}
				if err := ctx.Err(); err != nil {
					return
				}

				actual, err := cppPathStampFor(root, stamp.Path)
				if err != nil || actual != stamp {
					failed.Store(true)

					return
				}
			}
		}()
	}
	workers.Wait()

	if err := ctx.Err(); err != nil {
		return false, err
	}

	return !failed.Load(), nil
}
