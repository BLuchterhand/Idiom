package plan

import (
	"path/filepath"
	"sort"
	"strings"
)

type EditSetObligationKind string

const (
	EditSetObligationKindTestEdit           EditSetObligationKind = "test_edit_required"
	EditSetObligationKindImplementationEdit EditSetObligationKind = "implementation_edit_required"
)

type EditSetOpenObligationCode string

const (
	EditSetOpenObligationRequestedTestEditMissing           EditSetOpenObligationCode = "requested_test_edit_missing"
	EditSetOpenObligationRequestedImplementationEditMissing EditSetOpenObligationCode = "requested_implementation_edit_missing"
)

type EditSetObligation struct {
	Kind   EditSetObligationKind `json:"kind"`
	Path   string                `json:"path,omitempty"`
	Source string                `json:"source,omitempty"`
}

type EditSetObligations []EditSetObligation

type EditSetOpenObligation struct {
	Code   EditSetOpenObligationCode `json:"code"`
	Kind   EditSetObligationKind     `json:"kind"`
	Path   string                    `json:"path,omitempty"`
	Source string                    `json:"source,omitempty"`
}

func (obligations EditSetObligations) Has(kind EditSetObligationKind) bool {
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind == kind {
			return true
		}
	}

	return false
}

func (obligations EditSetObligations) Kinds() []string {
	out := make([]string, 0, len(obligations))
	seen := map[EditSetObligationKind]struct{}{}
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind == "" {
			continue
		}

		if _, ok := seen[obligation.Kind]; ok {
			continue
		}

		seen[obligation.Kind] = struct{}{}
		out = append(out, string(obligation.Kind))
	}

	return out
}

func (obligations EditSetObligations) Normalized() EditSetObligations {
	return obligations.WithDefaultSource("typed")
}

func (obligations EditSetObligations) WithDefaultSource(defaultSource string) EditSetObligations {
	var out EditSetObligations

	for _, obligation := range obligations {
		kind := obligation.Kind
		switch kind {
		case EditSetObligationKindTestEdit, EditSetObligationKindImplementationEdit:
		default:
			continue
		}

		source := strings.TrimSpace(obligation.Source)
		if source == "" {
			source = strings.TrimSpace(defaultSource)
			if source == "" {
				source = "prompt"
			}
		}

		rawPath := strings.TrimSpace(obligation.Path)
		path := NormalizeScopePath(rawPath)
		if rawPath != "" && path == "" {
			continue
		}

		out = out.with(EditSetObligation{Kind: kind, Path: path, Source: source})
	}

	sortEditSetObligations(out)

	return out
}

func MergeEditSetObligations(groups ...EditSetObligations) EditSetObligations {
	var out EditSetObligations

	for _, group := range groups {
		for _, obligation := range group.Normalized() {
			out = out.with(obligation)
		}
	}

	return out.Normalized()
}

func (obligations EditSetObligations) With(kind EditSetObligationKind, source string) EditSetObligations {
	return obligations.with(EditSetObligation{Kind: kind, Source: source}).Normalized()
}

func (obligations EditSetObligations) WithPath(kind EditSetObligationKind, path string, source string) EditSetObligations {
	return obligations.with(EditSetObligation{Kind: kind, Path: path, Source: source}).Normalized()
}

func (obligations EditSetObligations) RejectionReason(changedPaths []string) PlanExecuteFailureKind {
	open := obligations.Open(changedPaths)
	if len(open) == 0 {
		return ""
	}

	switch open[0].Code {
	case EditSetOpenObligationRequestedTestEditMissing:
		return PlanExecuteFailureMissingRequestedTestEdit
	case EditSetOpenObligationRequestedImplementationEditMissing:
		return PlanExecuteFailureMissingRequestedImplementationEdit
	default:
		return ""
	}
}

func (obligations EditSetObligations) MissingOpenObligations(changedPaths []string) []string {
	open := obligations.Open(changedPaths)
	out := make([]string, 0, len(open))
	seen := map[EditSetOpenObligationCode]struct{}{}
	for _, obligation := range open {
		if _, ok := seen[obligation.Code]; ok {
			continue
		}

		seen[obligation.Code] = struct{}{}
		out = append(out, string(obligation.Code))
	}

	return out
}

func (obligations EditSetObligations) Open(changedPaths []string) []EditSetOpenObligation {
	changed := scopePathSet(changedPaths)

	var open []EditSetOpenObligation
	for _, obligation := range obligations.Normalized() {
		if editSetObligationSatisfied(obligation, changedPaths, changed) {
			continue
		}

		open = append(open, EditSetOpenObligation{
			Code:   editSetObligationMissingCode(obligation.Kind),
			Kind:   obligation.Kind,
			Path:   obligation.Path,
			Source: obligation.Source,
		})
	}

	return open
}

func EditSetObligationFailureMessage(reason PlanExecuteFailureKind) string {
	switch reason {
	case PlanExecuteFailureMissingRequestedTestEdit:
		return string(reason) + ": typed test-edit obligation is unsatisfied because submitted evidence changed no test path"
	case PlanExecuteFailureMissingRequestedImplementationEdit:
		return string(reason) + ": typed implementation-edit obligation is unsatisfied because submitted evidence changed only test paths"
	default:
		return string(reason)
	}
}

func (obligations EditSetObligations) with(obligation EditSetObligation) EditSetObligations {
	obligation.Source = strings.TrimSpace(obligation.Source)

	for i := range obligations {
		if obligations[i].Kind == obligation.Kind && obligations[i].Path == obligation.Path {
			if strings.TrimSpace(obligations[i].Source) == "" {
				obligations[i].Source = obligation.Source
			}

			return obligations
		}
	}

	return append(obligations, obligation)
}

func editSetObligationSatisfied(obligation EditSetObligation, changedPaths []string, changed map[string]struct{}) bool {
	if obligation.Path != "" {
		_, ok := changed[obligation.Path]
		return ok
	}

	switch obligation.Kind {
	case EditSetObligationKindTestEdit:
		return ChangedPathsIncludeTest(changedPaths)
	case EditSetObligationKindImplementationEdit:
		return ChangedPathsIncludeImplementation(changedPaths)
	default:
		return true
	}
}

func editSetObligationMissingCode(kind EditSetObligationKind) EditSetOpenObligationCode {
	switch kind {
	case EditSetObligationKindTestEdit:
		return EditSetOpenObligationRequestedTestEditMissing
	case EditSetObligationKindImplementationEdit:
		return EditSetOpenObligationRequestedImplementationEditMissing
	default:
		return ""
	}
}

func sortEditSetObligations(obligations EditSetObligations) {
	sort.SliceStable(obligations, func(i, j int) bool {
		if editSetObligationKindRank(obligations[i].Kind) != editSetObligationKindRank(obligations[j].Kind) {
			return editSetObligationKindRank(obligations[i].Kind) < editSetObligationKindRank(obligations[j].Kind)
		}

		if obligations[i].Path != obligations[j].Path {
			return obligations[i].Path < obligations[j].Path
		}

		return obligations[i].Source < obligations[j].Source
	})
}

func editSetObligationKindRank(kind EditSetObligationKind) int {
	switch kind {
	case EditSetObligationKindTestEdit:
		return 0
	case EditSetObligationKindImplementationEdit:
		return 1
	default:
		return 2
	}
}

func ChangedPathsIncludeTest(paths []string) bool {
	for _, path := range paths {
		if PathLooksLikeTest(path) {
			return true
		}
	}

	return false
}

func ChangedPathsIncludeImplementation(paths []string) bool {
	for _, path := range paths {
		if PathLooksLikeImplementation(path) {
			return true
		}
	}

	return false
}

func PathLooksLikeImplementation(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" || PathLooksLikeTest(lower) {
		return false
	}

	base := filepath.Base(lower)
	ext := filepath.Ext(base)

	if strings.HasPrefix(lower, "docs/") ||
		strings.Contains(lower, "/docs/") ||
		strings.HasPrefix(lower, "doc/") ||
		strings.Contains(lower, "/doc/") ||
		strings.HasPrefix(lower, "examples/") ||
		strings.Contains(lower, "/examples/") {

		return false
	}

	switch strings.ToLower(base) {
	case "readme.md", "changelog.md", "license", "license.md":
		return false
	}

	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".swift",
		".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".sh", ".bash",
		".zsh", ".sql", ".proto", ".graphql", ".gd", ".mjs", ".cjs", ".kts",
		".css", ".scss", ".sass", ".html", ".vue", ".svelte":
		return true
	case ".md":
		return strings.HasPrefix(lower, "prompts/") ||
			strings.Contains(lower, "/prompts/") ||
			strings.Contains(lower, "/lanes/") ||
			strings.Contains(lower, "/profiles/") ||
			strings.Contains(lower, "/rules/")
	default:
		return false
	}
}

func PathLooksLikeTest(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	base := filepath.Base(lower)
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "_test.py"),
		strings.HasSuffix(base, ".test.py"),
		strings.HasSuffix(base, "_test.gd"),
		strings.HasSuffix(base, ".test.gd"),
		strings.HasSuffix(base, ".test.ts"),
		strings.HasSuffix(base, ".test.tsx"),
		strings.HasSuffix(base, ".test.js"),
		strings.HasSuffix(base, ".test.jsx"),
		strings.HasSuffix(base, ".spec.ts"),
		strings.HasSuffix(base, ".spec.tsx"),
		strings.HasSuffix(base, ".spec.js"),
		strings.HasSuffix(base, ".spec.jsx"):
		return true
	case strings.HasPrefix(lower, "test/"),
		strings.HasPrefix(lower, "tests/"),
		strings.HasPrefix(lower, "testdata/"),
		strings.HasPrefix(lower, "fixtures/"),
		strings.Contains(lower, "/test/"),
		strings.Contains(lower, "/tests/"),
		strings.Contains(lower, "/testdata/"),
		strings.Contains(lower, "/fixtures/"):
		return true
	default:
		return false
	}
}
