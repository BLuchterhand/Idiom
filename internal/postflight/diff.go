// Package postflight runs configurable QC checks against a unified
// diff produced by an `idiom lane` invocation. Phase 5 of the
// reusability migration ships three generic checks (always on) and
// a loader for project-defined checks at
// <project-root>/.idiom/postflight/checks/<name>.json.
//
// The package owns:
//   - A minimal unified-diff parser that extracts changed paths and
//     added lines (this isn't trying to be a general patch library —
//     the idiom lane writes diffs that match the conventions
//     `git diff` produces).
//   - The Check interface and three generic implementations.
//   - The project-checks loader and the shell-kind runner.
//   - The Run() pipeline that aggregates everything into one Result.
//
// The CLI surface (`idiom lane postflight`) is wired in cmd/idiom.
package postflight

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// FileChange captures one path's section of a unified diff. Paths
// are stripped of the conventional `a/` and `b/` prefixes git uses
// so callers can match them against project paths directly.
type FileChange struct {
	// Path is the post-image path (the `+++ b/<path>` side); for
	// pure deletions Path is the `--- a/<path>` side.
	Path string
	// Deleted is true when the file is gone in the post-image. It's
	// computed from the diff header (`+++ /dev/null`) rather than
	// inferred so a bug in line accounting can't lie about it.
	Deleted bool
	// Added is true when the file did not exist in the pre-image
	// (`--- /dev/null`). Used by checks that only care about new
	// files.
	Added bool
	// AddedLines holds the lines this diff added (without the leading
	// `+`). Order is preserved. Lines from `+++` headers are NOT
	// included — only the actual content additions.
	AddedLines []string
}

// Diff is the parsed representation of a unified diff: one
// FileChange per `diff --git` (or `--- / +++`) hunk pair.
type Diff struct {
	Files []FileChange
}

// Paths returns the sorted-by-input-order list of post-image paths
// in the diff, including added and modified files. Deleted files
// are intentionally omitted: the postflight allowlist check cares
// about *which paths were written* and a deletion is a removal,
// not a write to a new location.
func (d Diff) ModifiedPaths() []string {
	out := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		if f.Deleted {
			continue
		}
		out = append(out, f.Path)
	}
	return out
}

// ParseDiffFile reads a unified diff from disk and parses it. Any
// IO error is wrapped so callers can distinguish "file missing"
// (use ParseDiff with the body) from "parse error."
func ParseDiffFile(path string) (Diff, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Diff{}, fmt.Errorf("postflight: read diff %q: %w", path, err)
	}
	return ParseDiff(string(body)), nil
}

// ParseDiff parses a unified-diff string. The grammar this
// recognizes:
//
//	(diff --git a/X b/X)?      ← optional, ignored if present
//	(index ...)?               ← ignored
//	(--- a/X | --- /dev/null)
//	(+++ b/X | +++ /dev/null)
//	(@@ ... @@)?               ← hunk header, ignored
//	([ +-] ...)*              ← actual diff body
//
// Unknown header lines outside a `--- / +++` pair are silently
// skipped so the parser is forgiving of `git diff`'s extra
// metadata. Any malformed-looking input still produces a valid
// (possibly empty) Diff — the postflight pipeline treats "I parsed
// nothing" as "no checkable changes" rather than a hard error so a
// weird diff doesn't poison the whole run.
func ParseDiff(body string) Diff {
	var d Diff
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var current *FileChange
	flush := func() {
		if current != nil {
			d.Files = append(d.Files, *current)
			current = nil
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "--- "):
			flush()
			current = &FileChange{}
			oldPath := strings.TrimPrefix(line, "--- ")
			if oldPath == "/dev/null" {
				current.Added = true
			}
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &FileChange{}
			}
			newPath := strings.TrimPrefix(line, "+++ ")
			if newPath == "/dev/null" {
				current.Deleted = true
			} else {
				current.Path = stripGitPrefix(newPath)
			}
			// If we never saw a `--- /dev/null` but the file was
			// previously empty (purely-added), the `+++` carries the
			// post-image path. We rely on the `--- /dev/null` flag
			// for the Added bit.
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if current != nil {
				current.AddedLines = append(current.AddedLines, line[1:])
			}
		case strings.HasPrefix(line, "diff --git "):
			// `diff --git a/X b/X` headers can appear before the
			// `--- / +++` pair; flush any in-progress file so the
			// next pair starts clean.
			flush()
		}
	}
	flush()
	return d
}

// stripGitPrefix removes the conventional `a/` or `b/` prefix that
// git diff prepends to paths. Strips a leading tab+timestamp the
// stock `diff -u` tool sometimes appends after the path.
func stripGitPrefix(p string) string {
	// Drop everything from the first tab onward (timestamp metadata
	// from POSIX diff). git diff doesn't emit this, but POSIX diff
	// does, and the parser shouldn't be picky.
	if idx := strings.IndexByte(p, '\t'); idx >= 0 {
		p = p[:idx]
	}
	switch {
	case strings.HasPrefix(p, "a/"):
		return p[2:]
	case strings.HasPrefix(p, "b/"):
		return p[2:]
	}
	return p
}
