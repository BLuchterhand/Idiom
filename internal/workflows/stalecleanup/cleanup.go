package stalecleanup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Predicate func(dir string, entry os.DirEntry, cutoff time.Time) (path string, stale bool, err error)

type Config struct {
	Root         string
	Subdir       string
	Label        string
	Threshold    time.Duration
	LogOut       io.Writer
	ShouldRemove Predicate
	Remove       func(string) error
}

func Cleanup(cfg Config) {
	dir := filepath.Join(strings.TrimSpace(cfg.Root), ".idiom", cfg.Subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(cfg.LogOut, "%s cleanup: read %s: %v\n", cfg.Label, dir, err)
		}
		return
	}

	cutoff := time.Now().Add(-cfg.Threshold)
	for _, entry := range entries {
		path, stale, err := cfg.ShouldRemove(dir, entry, cutoff)
		if err != nil {
			fmt.Fprintf(cfg.LogOut, "%s cleanup: scan %s: %v\n", cfg.Label, path, err)
			continue
		}
		if !stale {
			continue
		}
		if err := cfg.Remove(path); err != nil {
			fmt.Fprintf(cfg.LogOut, "%s cleanup: remove %s: %v\n", cfg.Label, path, err)
		}
	}
}
