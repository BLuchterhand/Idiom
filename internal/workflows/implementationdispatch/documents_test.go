package implementationdispatch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPlanDocumentPathsFromTextUsesScopeHeadingsAndSkipsSource(t *testing.T) {
	body := `
# Design

Mention docs/notes.md outside scope.

## Files to Modify

- docs/design.md
- internal/workflows/implementationdispatch/documents.go
- internal/workflows/implementationdispatch/documents.go
- README.md
`

	got := PlanDocumentPathsFromText(body, "docs/design.md")
	want := []string{"internal/workflows/implementationdispatch/documents.go", "README.md"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestReadPlanDocumentSkipsRefineIterArtifactsButAllowsAcceptedBrief(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".idiom", "refine", "example", "iter-1")

	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatalf("mkdir iter dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(iterDir, "draft.md"), []byte("# Draft\n\n## Files To Modify\n\n- target.txt\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	acceptedPath := filepath.Join(root, ".idiom", "refine", "example", "accepted.md")
	if err := os.MkdirAll(filepath.Dir(acceptedPath), 0o755); err != nil {
		t.Fatalf("mkdir accepted dir: %v", err)
	}
	if err := os.WriteFile(acceptedPath, []byte("# Accepted\n\n## Files To Modify\n\n- target.txt\n"), 0o644); err != nil {
		t.Fatalf("write accepted: %v", err)
	}

	if _, ok := ReadPlanDocument(root, ".idiom/refine/example/iter-1/draft.md"); ok {
		t.Fatalf("refine iter draft should not be treated as a plan document")
	}

	if _, ok := ReadPlanDocument(root, ".idiom/refine/example/accepted.md"); !ok {
		t.Fatalf("accepted refine output should remain readable as a plan document")
	}
}

func TestResolveScopeResolvesUniqueBasenameAsCandidateScope(t *testing.T) {
	root := t.TempDir()
	writeImplementationDispatchTestFile(t, root, "docs/design.md", "# Design\n\n## Files To Modify\n\n- internal/example.go\n")
	writeImplementationDispatchTestFile(t, root, "internal/example.go", "package internal\n")

	got := ResolveScope(ScopeInput{
		Prompt:          "implement design.md",
		WorkspaceRoot:   root,
		MutationCapable: true,
	})

	if len(got.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", got.Blockers)
	}
	if len(got.PlanDocuments) != 1 || got.PlanDocuments[0].Path != "docs/design.md" {
		t.Fatalf("plan documents = %#v, want docs/design.md", got.PlanDocuments)
	}
	wantAllowed := []string{"internal/example.go"}
	if !reflect.DeepEqual(got.AllowedPaths, wantAllowed) {
		t.Fatalf("allowed paths = %#v, want %#v", got.AllowedPaths, wantAllowed)
	}
}

func TestResolveScopeBlocksMissingPlanDocumentForMutationRequest(t *testing.T) {
	root := t.TempDir()

	got := ResolveScope(ScopeInput{
		Prompt:          "implement missing-design.md",
		WorkspaceRoot:   root,
		MutationCapable: true,
	})

	if len(got.Blockers) != 1 {
		t.Fatalf("blockers = %#v, want one blocker", got.Blockers)
	}
	if got.Blockers[0].Ref != "missing-design.md" || got.Blockers[0].Reason != "missing" {
		t.Fatalf("blocker = %#v, want missing missing-design.md", got.Blockers[0])
	}
}

func TestResolveScopeSkipsPlanDocumentWhenItIsExplicitEditTarget(t *testing.T) {
	root := t.TempDir()
	writeImplementationDispatchTestFile(t, root, "docs/design.md", "# Design\n\n## Files To Modify\n\n- internal/example.go\n")

	got := ResolveScope(ScopeInput{
		Prompt:          "update docs/design.md",
		WorkspaceRoot:   root,
		RequiredTargets: []string{"docs/design.md"},
		MutationCapable: true,
	})

	if len(got.PlanDocuments) != 0 || len(got.Blockers) != 0 {
		t.Fatalf("explicit edit target should not resolve as plan document, got docs=%#v blockers=%#v", got.PlanDocuments, got.Blockers)
	}
	if !reflect.DeepEqual(got.RequiredPaths, []string{"docs/design.md"}) {
		t.Fatalf("required paths = %#v", got.RequiredPaths)
	}
}

func TestComposeBriefProjectsDispatchScopeAndPlanDocuments(t *testing.T) {
	brief := ComposeBrief(BriefInput{
		Objective:     "implement docs/design.md",
		RequiredPaths: []string{"internal/required.go"},
		AllowedPaths:  []string{"internal/candidate.go"},
		PlanDocuments: []PlanDocument{{
			Path:    "docs/design.md",
			Content: "# Design\n\n## Files To Modify\n\n- internal/candidate.go\n\n## Steps\n\nApply the change.",
		}},
		EvidencePaths: []string{"docs/evidence.md"},
		TargetRefs:    []string{"docs/design.md"},
		RequiredEditSnippets: []RequiredEditSnippet{{
			Path:    "internal/required.go",
			Content: "func required() {}",
		}},
		VerificationPlan: []string{"go test ./..."},
		WorkspaceRoot:    "/workspace/project",
	})

	for _, want := range []string{
		"# Objective",
		"implement docs/design.md",
		"## Files to Modify\n\n- internal/required.go",
		"## Candidate Edit Scope From Target References\n\n- internal/candidate.go",
		"## Referenced Plan Documents",
		"### docs/design.md",
		"> ## Files To Modify",
		"> - internal/candidate.go",
		"## Evidence Paths\n\n- docs/evidence.md",
		"## Target References\n\n- docs/design.md",
		"## Required Edit Snippets",
		"### internal/required.go",
		"func required() {}",
		"## Verification\n\n- `go test ./...`",
		"## Workspace Root\n\n/workspace/project",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestCompactPlanDocumentReferenceSelectsObjectiveExcerpts(t *testing.T) {
	var doc strings.Builder

	doc.WriteString("# Design\n\n")
	doc.WriteString("Status migration apply must harden predictable temp-path writes.\n\n")

	for i := 0; i < 120; i++ {
		doc.WriteString("## Unrelated Section\n\n")
		doc.WriteString("This section contains unrelated operator notes that should not dominate the brief projection.\n\n")
	}

	doc.WriteString("## Relevant Regression Coverage\n\n")
	doc.WriteString("Add migration regression tests proving temp symlink writes are rejected without partial durable mutation.\n\n")

	got := CompactPlanDocumentReference(
		"implement status migration temp symlink regression coverage",
		doc.String(),
		3000,
	)

	for _, want := range []string{
		"[excerpted from long referenced document",
		"Status migration apply must harden predictable temp-path writes.",
		"Add migration regression tests proving temp symlink writes are rejected",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compacted reference missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "Unrelated Section") {
		t.Fatalf("compacted reference retained unrelated filler:\n%s", got)
	}
}

func TestQuoteReferenceFormatsBlankLinesAndEmptyContent(t *testing.T) {
	if got := QuoteReference(""); got != "> (empty)\n" {
		t.Fatalf("empty quote = %q", got)
	}

	got := QuoteReference("alpha\n\nbeta")
	want := "> alpha\n>\n> beta\n"
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func writeImplementationDispatchTestFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
