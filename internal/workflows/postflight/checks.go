package postflight

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/contracts/plancontract"
)

// Severity classifies how seriously a Finding should be treated.
// `error` fails the postflight (non-zero exit); `warning` reports
// but doesn't fail; `info` is observational.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding captures one issue raised by a Check. Path may be empty
// for whole-diff findings (e.g. "no diff produced any changes");
// Line is 1-indexed and may be 0 when the finding is path-scoped
// rather than line-scoped.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
}

// Check is the contract every generic and project-defined check
// implements. Run is given the parsed diff, the project root, and
// the resolved RunContext (for things like required_edit_paths).
// It returns zero or more Findings; an error return is reserved
// for IO failures that prevent the check from running at all
// (project misconfiguration, unreadable file in the working tree).
type Check interface {
	Name() string
	Run(d Diff, ctx RunContext) ([]Finding, error)
}

// RunContext threads in the optional inputs a check might need.
// Everything is optional — a check that doesn't need a brief or
// project root just ignores those fields. DiffPath and BriefPath
// are surfaced so project-defined shell checks can reference them
// via `{diff}` and `{brief}` placeholders.
type RunContext struct {
	ProjectRoot       string
	RequiredEditPaths []string
	BriefPath         string
	DiffPath          string
}

// GenericChecks returns the always-on set Phase 5 ships. Project
// authors can add to this list via .idiom/postflight/checks/.
func GenericChecks() []Check {
	return []Check{
		UnresolvedQuestionMarker{},
		FileAllowlistMismatch{},
		NarratingTestComments{},
		GoBuildPasses{},
	}
}

// UnresolvedQuestionMarker fails when a diff introduces
// `<<QUESTION>>:`. The lane runner uses that marker to stop with a
// typed blocker; the marker should NEVER survive into committed
// code. Scoping to added diff lines keeps the check tied to the
// current lane output and avoids false positives from old docs or
// examples that happen to live in a touched file.
type UnresolvedQuestionMarker struct{}

func (UnresolvedQuestionMarker) Name() string { return "unresolved_question_marker" }

func (c UnresolvedQuestionMarker) Run(d Diff, _ RunContext) ([]Finding, error) {
	const marker = "<<QUESTION>>:"
	var findings []Finding
	for _, f := range d.Files {
		if f.Deleted {
			continue
		}
		for _, added := range f.AddedLines {
			if strings.Contains(added.Body, marker) {
				findings = append(findings, Finding{
					Check:    c.Name(),
					Severity: SeverityError,
					Path:     f.Path,
					Line:     added.Line,
					Message:  "unresolved <<QUESTION>>: marker introduced by diff",
				})
			}
		}
	}
	return findings, nil
}

// FileAllowlistMismatch fails when the diff modifies a path that
// isn't in the brief's required_edit_paths. The brief defines the
// scope of the lane; writes outside it almost always indicate the
// model strayed. When RequiredEditPaths is empty the check no-ops
// (we can't enforce a scope we don't know).
type FileAllowlistMismatch struct{}

func (FileAllowlistMismatch) Name() string { return "file_allowlist_mismatch" }

func (c FileAllowlistMismatch) Run(d Diff, ctx RunContext) ([]Finding, error) {
	if len(ctx.RequiredEditPaths) == 0 {
		return nil, nil
	}
	allow := make(map[string]struct{}, len(ctx.RequiredEditPaths))
	for _, p := range ctx.RequiredEditPaths {
		allow[normalizePath(p)] = struct{}{}
	}
	var findings []Finding
	for _, p := range d.ModifiedPaths() {
		if _, ok := allow[normalizePath(p)]; !ok {
			findings = append(findings, Finding{
				Check:    c.Name(),
				Severity: SeverityError,
				Path:     p,
				Message:  "modified path is not in the brief's required_edit_paths allowlist",
			})
		}
	}
	return findings, nil
}

// NarratingTestComments flags comments in newly-added test code
// that just narrate what the assertion already shows. The signal:
// comments matching one of the patterns in narratingTestPhrases
// inside a file the heuristic recognizes as a test file. Phase 5
// keeps the heuristic conservative — false positives are worse
// than misses here, since this fires on a final-product diff.
type NarratingTestComments struct{}

func (NarratingTestComments) Name() string { return "narrating_test_comments" }

func (c NarratingTestComments) Run(d Diff, _ RunContext) ([]Finding, error) {
	var findings []Finding
	for _, f := range d.Files {
		if !looksLikeTestFile(f.Path) {
			continue
		}
		// Only freshly-added lines are interesting — we can't ask
		// the model to fix something that was already in the file
		// before the lane ran.
		for _, added := range f.AddedLines {
			trimmed := commentBody(added.Body)
			if trimmed == "" {
				continue
			}
			lower := strings.ToLower(trimmed)
			if !narratingPhrase.MatchString(lower) {
				continue
			}
			findings = append(findings, Finding{
				Check:    c.Name(),
				Severity: SeverityWarning,
				Path:     f.Path,
				Line:     added.Line,
				Message:  fmt.Sprintf("narrating test comment %q — assertions and test names should carry the intent", trimmed),
			})
		}
	}
	return findings, nil
}

// narratingPhrase matches the leading clauses we want to flag. It's
// deliberately strict (must be at-or-near the start of the comment)
// so a stray "make sure" buried inside legitimate prose doesn't
// trigger.
var narratingPhrase = regexp.MustCompile(`^(this test|the test|we test|tests that|verifies that|verify that|make sure|makes sure|ensures that|confirms that|asserts that|checks that)\b`)

// commentBody extracts the prose portion of a comment line for the
// languages we care about (Go, C, C++, Python, JS/TS, shell). If
// the line isn't a comment, returns "". Handles `//`, `#`, and `/*`
// styles; trims surrounding whitespace from what's left.
func commentBody(line string) string {
	s := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(s, "//"):
		return strings.TrimSpace(strings.TrimPrefix(s, "//"))
	case strings.HasPrefix(s, "#"):
		return strings.TrimSpace(strings.TrimPrefix(s, "#"))
	case strings.HasPrefix(s, "/*"):
		s = strings.TrimPrefix(s, "/*")
		s = strings.TrimSuffix(s, "*/")
		return strings.TrimSpace(s)
	case strings.HasPrefix(s, "*"):
		// continuation line of a /* ... */ block
		return strings.TrimSpace(strings.TrimPrefix(s, "*"))
	}
	return ""
}

// looksLikeTestFile uses path heuristics to decide whether a file
// is test code for the languages idiom commonly touches. The set
// is intentionally narrow — when in doubt the file isn't tagged
// as test, which means the narrating-comment check no-ops on it
// rather than firing a false positive.
func looksLikeTestFile(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasPrefix(base, "test_") && (strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".cpp") || strings.HasSuffix(base, ".c") || strings.HasSuffix(base, ".cc")):
		return true
	case strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") || strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.tsx") || strings.HasSuffix(base, ".spec.js"):
		return true
	case strings.HasSuffix(base, "_test.cpp") || strings.HasSuffix(base, "_test.cc") || strings.HasSuffix(base, "_test.py"):
		return true
	}
	return false
}

// normalizePath canonicalizes a path for allowlist comparison —
// strips a leading `./`, drops trailing slashes, and replaces `\`
// with `/` so allowlist entries don't have to anticipate Windows-
// flavored input.
func normalizePath(p string) string {
	return plancontract.NormalizeWorkspacePath(p)
}
