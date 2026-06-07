package plancontract

import (
	"path/filepath"
	"strings"
)

type EditSetObligationKind string

const (
	EditSetObligationKindTestEdit           EditSetObligationKind = "test_edit_required"
	EditSetObligationKindImplementationEdit EditSetObligationKind = "implementation_edit_required"

	EditSetFailureMissingRequestedTestEdit           = "missing_requested_test_edit"
	EditSetFailureMissingRequestedImplementationEdit = "missing_requested_implementation_edit"
)

type EditSetObligation struct {
	Kind   EditSetObligationKind `json:"kind"`
	Source string                `json:"source,omitempty"`
}

type EditSetObligations []EditSetObligation

func EditSetObligationsFromPrompt(prompt string) EditSetObligations {
	return nil
}

func (obligations EditSetObligations) Has(kind EditSetObligationKind) bool {
	for _, obligation := range obligations {
		if obligation.Kind == kind {
			return true
		}
	}
	return false
}

func (obligations EditSetObligations) Kinds() []string {
	out := make([]string, 0, len(obligations))
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind == "" {
			continue
		}
		out = append(out, string(obligation.Kind))
	}
	return out
}

func (obligations EditSetObligations) Normalized() EditSetObligations {
	return obligations.normalizedWithDefaultSource("typed")
}

func (obligations EditSetObligations) WithDefaultSource(defaultSource string) EditSetObligations {
	return obligations.normalizedWithDefaultSource(defaultSource)
}

func (obligations EditSetObligations) normalizedWithDefaultSource(defaultSource string) EditSetObligations {
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
		out = out.with(kind, source)
	}
	return out
}

func MergeEditSetObligations(groups ...EditSetObligations) EditSetObligations {
	var out EditSetObligations
	for _, group := range groups {
		for _, obligation := range group.Normalized() {
			out = out.with(obligation.Kind, obligation.Source)
		}
	}
	return out.Normalized()
}

func (obligations EditSetObligations) With(kind EditSetObligationKind, source string) EditSetObligations {
	return obligations.with(kind, source).Normalized()
}

func (obligations EditSetObligations) RejectionReason(changedPaths []string) string {
	if obligations.Has(EditSetObligationKindTestEdit) && !ChangedPathsIncludeTest(changedPaths) {
		return EditSetFailureMissingRequestedTestEdit
	}
	if obligations.Has(EditSetObligationKindImplementationEdit) && !ChangedPathsIncludeImplementation(changedPaths) {
		return EditSetFailureMissingRequestedImplementationEdit
	}
	return ""
}

func (obligations EditSetObligations) MissingOpenObligations(changedPaths []string) []string {
	var out []string
	if obligations.Has(EditSetObligationKindTestEdit) && !ChangedPathsIncludeTest(changedPaths) {
		out = append(out, "requested_test_edit_missing")
	}
	if obligations.Has(EditSetObligationKindImplementationEdit) && !ChangedPathsIncludeImplementation(changedPaths) {
		out = append(out, "requested_implementation_edit_missing")
	}
	return out
}

func EditSetObligationFailureMessage(reason string) string {
	switch reason {
	case EditSetFailureMissingRequestedTestEdit:
		return reason + ": typed test-edit obligation is unsatisfied because submitted evidence changed no test path"
	case EditSetFailureMissingRequestedImplementationEdit:
		return reason + ": typed implementation-edit obligation is unsatisfied because submitted evidence changed only test paths"
	default:
		return reason
	}
}

func (obligations EditSetObligations) with(kind EditSetObligationKind, source string) EditSetObligations {
	for i := range obligations {
		if obligations[i].Kind == kind {
			if strings.TrimSpace(obligations[i].Source) == "" {
				obligations[i].Source = source
			}
			return obligations
		}
	}
	return append(obligations, EditSetObligation{Kind: kind, Source: source})
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
