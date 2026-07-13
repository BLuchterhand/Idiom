package implementationdispatch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Implementation plan documents", func() {
	ginkgo.It("reads edit paths from scope headings while omitting the source document", func() {
		body := `
# Design

Mention docs/notes.md outside scope.

## Files to Modify

- docs/design.md
- internal/workflows/implementationdispatch/documents.go
- internal/workflows/implementationdispatch/documents.go
- README.md
`
		gomega.Expect(PlanDocumentPathsFromText(body, "docs/design.md")).To(gomega.Equal([]string{
			"internal/workflows/implementationdispatch/documents.go",
			"README.md",
		}))
	})

	ginkgo.It("skips refine iteration artifacts but reads the accepted brief", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchTestFile(root, ".idiom/refine/example/iter-1/draft.md", "# Draft\n\n## Files To Modify\n\n- target.txt\n")
		writeImplementationDispatchTestFile(root, ".idiom/refine/example/accepted.md", "# Accepted\n\n## Files To Modify\n\n- target.txt\n")

		_, ok := ReadPlanDocument(root, ".idiom/refine/example/iter-1/draft.md")
		gomega.Expect(ok).To(gomega.BeFalse())
		_, ok = ReadPlanDocument(root, ".idiom/refine/example/accepted.md")
		gomega.Expect(ok).To(gomega.BeTrue())
	})

	ginkgo.It("resolves a unique plan-document basename into candidate scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchTestFile(root, "docs/design.md", "# Design\n\n## Files To Modify\n\n- internal/example.go\n")
		writeImplementationDispatchTestFile(root, "internal/example.go", "package internal\n")
		got := ResolveScope(ScopeInput{Prompt: "implement design.md", WorkspaceRoot: root, MutationCapable: true})
		gomega.Expect(got.Blockers).To(gomega.BeEmpty())
		gomega.Expect(got.PlanDocuments).To(gomega.ConsistOf(gomega.HaveField("Path", "docs/design.md")))
		gomega.Expect(got.AllowedPaths).To(gomega.Equal([]string{"internal/example.go"}))
	})

	ginkgo.It("keeps an explicit required path out of document candidate scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchTestFile(root, "docs/design.md", "# Design\n\n## Files To Modify\n\n- internal/example.go\n")
		writeImplementationDispatchTestFile(root, "internal/example.go", "package internal\n")

		got := ResolveScope(ScopeInput{
			Prompt:          "implement docs/design.md",
			WorkspaceRoot:   root,
			RequiredTargets: []string{"internal/example.go"},
			MutationCapable: true,
		})

		gomega.Expect(got.Blockers).To(gomega.BeEmpty())
		gomega.Expect(got.RequiredPaths).To(gomega.Equal([]string{"internal/example.go"}))
		gomega.Expect(got.AllowedPaths).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps recovered document candidates disjoint from required authority", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchTestFile(root, "internal/required.go", "package internal\n")
		writeImplementationDispatchTestFile(root, "internal/candidate.go", "package internal\n")

		got := ResolvePlanDocumentEditScope(PlanDocumentEditScopeInput{
			WorkspaceRoot: root,
			RequiredPaths: []string{"./internal/required.go"},
			AllowedPaths:  []string{"internal/allowed.go", `internal\required.go`},
			PlanDocuments: []PlanDocument{{
				Path:      "docs/recovered-plan.md",
				EditPaths: []string{"internal/required.go", `internal\candidate.go`},
			}},
		})

		gomega.Expect(got.RequiredPaths).To(gomega.Equal([]string{"internal/required.go"}))
		gomega.Expect(got.AllowedPaths).To(gomega.Equal([]string{"internal/allowed.go", "internal/candidate.go"}))
	})

	ginkgo.It("blocks a missing plan document for a mutation request", func() {
		got := ResolveScope(ScopeInput{
			Prompt:          "implement missing-design.md",
			WorkspaceRoot:   ginkgo.GinkgoT().TempDir(),
			MutationCapable: true,
		})
		gomega.Expect(got.Blockers).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Ref", "missing-design.md"),
			gomega.HaveField("Reason", "missing"),
		)))
	})

	ginkgo.It("does not interpret an explicit edit target as a referenced plan document", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchTestFile(root, "docs/design.md", "# Design\n\n## Files To Modify\n\n- internal/example.go\n")
		got := ResolveScope(ScopeInput{
			Prompt:          "update docs/design.md",
			WorkspaceRoot:   root,
			RequiredTargets: []string{"docs/design.md"},
			MutationCapable: true,
		})
		gomega.Expect(got.PlanDocuments).To(gomega.BeEmpty())
		gomega.Expect(got.Blockers).To(gomega.BeEmpty())
		gomega.Expect(got.RequiredPaths).To(gomega.Equal([]string{"docs/design.md"}))
	})

	ginkgo.It("projects dispatch scope and referenced plan documents into a brief", func() {
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
			gomega.Expect(brief).To(gomega.ContainSubstring(want))
		}
	})

	ginkgo.It("compacts a long plan document around objective-relevant excerpts", func() {
		var doc strings.Builder
		doc.WriteString("# Design\n\nStatus migration apply must harden predictable temp-path writes.\n\n")
		for i := 0; i < 120; i++ {
			doc.WriteString("## Unrelated Section\n\nThis section contains unrelated operator notes that should not dominate the brief projection.\n\n")
		}
		doc.WriteString("## Relevant Regression Coverage\n\nAdd migration regression tests proving temp symlink writes are rejected without partial durable mutation.\n\n")
		got := CompactPlanDocumentReference("implement status migration temp symlink regression coverage", doc.String(), 3000)
		gomega.Expect(got).To(gomega.And(
			gomega.ContainSubstring("[excerpted from long referenced document"),
			gomega.ContainSubstring("Status migration apply must harden predictable temp-path writes."),
			gomega.ContainSubstring("Add migration regression tests proving temp symlink writes are rejected"),
		))
		gomega.Expect(got).NotTo(gomega.ContainSubstring("Unrelated Section"))
	})

	ginkgo.It("quotes blank lines and empty reference content", func() {
		gomega.Expect(QuoteReference("")).To(gomega.Equal("> (empty)\n"))
		gomega.Expect(QuoteReference("alpha\n\nbeta")).To(gomega.Equal("> alpha\n>\n> beta\n"))
	})
})

func writeImplementationDispatchTestFile(root string, rel string, body string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}
