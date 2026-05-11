package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanupStaleRefineState removes per-title directories under
// <root>/.idiom/refine/ whose contents (all files, recursively)
// haven't been touched in `threshold`. A title-dir survives when ANY
// descendant has a mtime newer than the cutoff — the per-iter
// metrics.json from #79 is the most recent thing written, so the
// most-recent iter's mtime decides the whole title.
//
// Mirrors cleanupStalePlanArtifacts intentionally: best-effort, never
// returns an error, logs failures to logOut. Called at session start
// (#73 — clear leftovers from interrupted prior runs) and via defer
// at session end (#77 — companion sweep after a clean ship).
func cleanupStaleRefineState(root string, threshold time.Duration, logOut io.Writer) {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "refine")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logOut, "refine cleanup: read %s: %v\n", dir, err)
		}
		return
	}
	cutoff := time.Now().Add(-threshold)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		titleDir := filepath.Join(dir, entry.Name())
		stale, err := titleDirIsStale(titleDir, cutoff)
		if err != nil {
			fmt.Fprintf(logOut, "refine cleanup: scan %s: %v\n", titleDir, err)
			continue
		}
		if !stale {
			continue
		}
		if err := os.RemoveAll(titleDir); err != nil {
			fmt.Fprintf(logOut, "refine cleanup: remove %s: %v\n", titleDir, err)
		}
	}
}

// titleDirIsStale reports whether every file under titleDir has
// an mtime older than cutoff. An empty dir counts as stale (nothing
// active to preserve). The walk stops early on the first non-stale
// file for efficiency.
func titleDirIsStale(titleDir string, cutoff time.Time) (bool, error) {
	stale := true
	seenAny := false
	err := filepath.Walk(titleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		seenAny = true
		if !info.ModTime().Before(cutoff) {
			stale = false
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if !seenAny {
		return true, nil
	}
	return stale, nil
}
