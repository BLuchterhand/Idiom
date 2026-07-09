package lane

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/gitstate"
)

// checkBriefStaleness fails when any required edit path has git
// commits newer than the brief's mtime. The brief was written
// against an older view of the code, so any new commit on a target
// path means the brief's anchors / current-state quotes may no
// longer match reality. allowStale=true downgrades the failure
// to a stderr WARN (the caller has explicitly acknowledged risk).
//
// Returns nil when the brief is fresh, an error when it's stale
// and allowStale=false. New files (no commits yet) are exempt.
// git failures are non-fatal — log and skip the path.
func checkBriefStaleness(briefPath, projectRoot string, allowStale bool, stderr io.Writer) error {
	info, err := os.Stat(briefPath)
	if err != nil {
		return nil
	}

	briefMtime := info.ModTime()

	body, err := os.ReadFile(briefPath)
	if err != nil {
		return nil
	}

	scoped := pathparse.ScopeToBriefBody(string(body))
	paths := pathparse.MentionedWorkspacePathsUnderHeadings(scoped, "Files To Modify", "Files You May Edit")
	root := strings.TrimSpace(projectRoot)

	if root == "" {
		root = filepath.Dir(briefPath)
	}

	var stale []string

	for _, p := range paths {
		changeTime, ok := mostRecentChangeTime(root, p)
		if !ok {
			continue
		}

		if changeTime.After(briefMtime) {
			stale = append(stale, fmt.Sprintf("%s (last change %s, brief mtime %s)", p, changeTime.Format(time.RFC3339), briefMtime.Format(time.RFC3339)))
		}
	}

	if len(stale) == 0 {
		return nil
	}

	if allowStale {
		fmt.Fprintf(stderr, "lane: WARN brief is stale (allowed by --allow-stale-brief):\n  %s\n", strings.Join(stale, "\n  "))

		return nil
	}

	return fmt.Errorf("brief is stale relative to %d required path(s); pass --allow-stale-brief to override:\n  %s", len(stale), strings.Join(stale, "\n  "))
}

// mostRecentChangeTime returns the more-recent of the path's last
// git commit time and its on-disk mtime. Either signal alone misses
// half the staleness cases: commit-only misses uncommitted work,
// mtime-only misses commits that don't touch the working tree (e.g.
// a pull that updates without rewriting). The path is exempt
// (returns ok=false) only when BOTH signals are unavailable
// (no commits AND file doesn't exist on disk yet — i.e. a new file
// the brief is about to create).
func mostRecentChangeTime(repoRoot, path string) (time.Time, bool) {
	var newest time.Time

	if commitTime, ok := gitstate.LastCommitTime(context.Background(), repoRoot, path); ok {
		newest = commitTime
	}

	if info, err := os.Stat(filepath.Join(repoRoot, path)); err == nil {
		if mtime := info.ModTime(); mtime.After(newest) {
			newest = mtime
		}
	}

	if newest.IsZero() {
		return time.Time{}, false
	}

	return newest, true
}
