package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanupStalePlanArtifacts removes *.plan.json files in
// <root>/.idiom/plans/ whose mtime is older than threshold.
// last.plan.json (the rolling pointer) is always preserved.
// Failures are logged to logOut and never returned — cleanup
// must never abort the caller.
func cleanupStalePlanArtifacts(root string, threshold time.Duration, logOut io.Writer) {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logOut, "plan-execute cleanup: read %s: %v\n", dir, err)
		}
		return
	}
	cutoff := time.Now().Add(-threshold)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "last.plan.json" {
			continue
		}
		if !strings.HasSuffix(name, ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: remove %s: %v\n", path, err)
		}
	}
}
