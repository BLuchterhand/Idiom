package implementationdispatch

import (
	"strings"
	"testing"
)

func TestSanitizeNonzeroRefineAcceptedContent(t *testing.T) {
	content := strings.Join([]string{
		"> WARNING: refine exited non-zero - reason=max-iters-exhausted after iter 3.",
		"",
		"<!-- idiom:refine-drafted title=tui-example iter=3 reason=max-iters-exhausted -->",
		"# Accepted",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| target.txt | NEW FILE: write target |",
		"",
	}, "\n")
	sanitized, changed := SanitizeNonzeroRefineAcceptedContent(content)

	if !changed {
		t.Fatalf("expected sanitize to report changed content")
	}

	for _, forbidden := range []string{
		"refine exited non-zero",
		"idiom:refine-drafted",
		"reason=max-iters-exhausted",
	} {
		if strings.Contains(sanitized, forbidden) {
			t.Fatalf("sanitized content still contains %q:\n%s", forbidden, sanitized)
		}
	}

	if !strings.Contains(sanitized, "idiom:tui-refine-accepted-after-structural-preflight") ||
		!strings.Contains(sanitized, "## Files To Modify") {

		t.Fatalf("sanitized content lost expected dispatchable brief shape:\n%s", sanitized)
	}
}

func TestReplacePlanDocumentPreservesAuthorizedNewFiles(t *testing.T) {
	docs := []PlanDocument{
		{
			Path:               "docs/source.md",
			AuthorizedNewFiles: []string{"target.txt"},
		},
	}

	got := ReplacePlanDocument(docs, "docs/source.md", PlanDocument{Path: ".idiom/refine/example/accepted.md"})
	if len(got) != 1 {
		t.Fatalf("replaced documents = %#v, want one document", got)
	}

	if got[0].Path != ".idiom/refine/example/accepted.md" {
		t.Fatalf("document path = %q", got[0].Path)
	}

	if len(got[0].AuthorizedNewFiles) != 1 || got[0].AuthorizedNewFiles[0] != "target.txt" {
		t.Fatalf("authorized new files were not preserved: %#v", got[0].AuthorizedNewFiles)
	}
}
