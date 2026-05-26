package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type staleCleanupPredicate func(dir string, entry os.DirEntry, cutoff time.Time) (path string, stale bool, err error)

type staleCleanupConfig struct {
	root         string
	subdir       string
	label        string
	threshold    time.Duration
	logOut       io.Writer
	shouldRemove staleCleanupPredicate
	remove       func(string) error
}

func cleanupStaleIDIOMEntries(cfg staleCleanupConfig) {
	dir := filepath.Join(strings.TrimSpace(cfg.root), ".idiom", cfg.subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(cfg.logOut, "%s cleanup: read %s: %v\n", cfg.label, dir, err)
		}
		return
	}

	cutoff := time.Now().Add(-cfg.threshold)
	for _, entry := range entries {
		path, stale, err := cfg.shouldRemove(dir, entry, cutoff)
		if err != nil {
			fmt.Fprintf(cfg.logOut, "%s cleanup: scan %s: %v\n", cfg.label, path, err)
			continue
		}
		if !stale {
			continue
		}
		if err := cfg.remove(path); err != nil {
			fmt.Fprintf(cfg.logOut, "%s cleanup: remove %s: %v\n", cfg.label, path, err)
		}
	}
}
