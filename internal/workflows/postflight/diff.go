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
	"regexp"
	"strconv"
	"strings"
)

// AddedLine is one `+`-prefixed line from a diff hunk, paired with
// its 1-based post-image line number. Tracking the line number is
// load-bearing for findings: the CLI prints `<path>:<line>` and
// operators click those to jump in their editor — index-in-block
// would point at the wrong line in a multi-hunk file.
type AddedLine struct {
	Line int
	Body string
}

// FileChange captures one path's section of a unified diff. Paths
// are stripped of the conventional `a/` and `b/` prefixes git uses
// so callers can match them against project paths directly.
type FileChange struct {
	// Path is the post-image path (the `+++ b/<path>` side). For
	// pure deletions where the post-image is /dev/null, Path holds
	// the pre-image path so callers can identify the file that was
	// removed without separately consulting OldPath.
	Path string
	// OldPath is the pre-image path (the `--- a/<path>` side). For
	// modifies and adds it usually equals Path; for renames (rare
	// in idiom-produced diffs) it differs.
	OldPath string
	// Deleted is true when the post-image is /dev/null.
	Deleted bool
	// Added is true when the pre-image is /dev/null.
	Added bool
	// AddedLines holds the lines this diff added (without the leading
	// `+`), each paired with its 1-based post-image line number.
	// Lines from `+++` headers are NOT included.
	AddedLines []AddedLine
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

// hunkHeaderRE matches `@@ -A,B +C,D @@` where the comma+count is
// optional (single-line hunks omit it: `@@ -1 +1 @@`). The capture
// group used downstream is index 2 — the post-image starting line.
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// ParseDiff parses a unified-diff string. The grammar this
// recognizes:
//
//	(diff --git a/X b/X)?      ← optional, ignored if present
//	(index ...)?               ← ignored
//	(--- a/X | --- /dev/null)
//	(+++ b/X | +++ /dev/null)
//	(@@ -A,B +C,D @@)+         ← hunk header, parsed for line numbers
//	([ +-] ...)*               ← actual diff body
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
	// nextNewLine tracks the 1-based post-image line counter inside
	// the current hunk. Reset whenever a new `@@` header is seen.
	// When no hunk header has appeared yet, it's 0 — `+` lines in
	// that state get Line=0 to signal "no positional info."
	var nextNewLine int
	flush := func() {
		if current != nil {
			// For pure deletions the post-image path is /dev/null;
			// fall back to the pre-image path so Path identifies
			// the file the operator removed.
			if current.Deleted && strings.TrimSpace(current.Path) == "" {
				current.Path = current.OldPath
			}
			d.Files = append(d.Files, *current)
			current = nil
		}
		nextNewLine = 0
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
			} else {
				current.OldPath = stripGitPrefix(oldPath)
			}
		case strings.HasPrefix(line, "+++ "):
			// A `+++` outside a `---` pair is malformed; ignore it
			// rather than synthesize a half-populated FileChange that
			// would make downstream checks misreport.
			if current == nil {
				continue
			}
			newPath := strings.TrimPrefix(line, "+++ ")
			if newPath == "/dev/null" {
				current.Deleted = true
			} else {
				current.Path = stripGitPrefix(newPath)
			}
		case strings.HasPrefix(line, "@@"):
			if m := hunkHeaderRE.FindStringSubmatch(line); m != nil {
				if n, err := strconv.Atoi(m[2]); err == nil {
					nextNewLine = n
				}
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if current != nil {
				current.AddedLines = append(current.AddedLines, AddedLine{
					Line: nextNewLine,
					Body: line[1:],
				})
				if nextNewLine > 0 {
					nextNewLine++
				}
			}
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			// Deletion line: no post-image impact, no counter update.
		case strings.HasPrefix(line, " "):
			// Context line: present in both pre and post images, so
			// it advances the post-image counter.
			if nextNewLine > 0 {
				nextNewLine++
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
