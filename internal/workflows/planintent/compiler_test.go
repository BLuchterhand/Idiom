package planintent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("Plan intent compilation", func() {
	ginkgo.It("compiles exact-text replacement into a submit-plan edit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writePlanIntentFixture(root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")
		artifact := validReplaceArtifact()
		artifact.Intents[0].OldText = "return \"old\""
		artifact.Intents[0].NewText = "return \"next\""

		edits := compilePlanIntent(root, artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})
		gomega.Expect(edits).To(gomega.Equal([]planartifact.PlanEdit{{
			Path: "internal/a.go", OldText: "return \"old\"", NewText: "return \"next\"", Purpose: "replace old label",
		}}))
		expectSubmitPlanAccepts(root, edits)
	})

	ginkgo.It("compiles a line anchor into an insert-after-line edit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writePlanIntentFixture(root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")
		artifact := Artifact{
			SchemaVersion: SchemaVersion,
			Source:        SourcePlanner,
			Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
			Intents: []Intent{{
				Path: "internal/a.go", Operation: OperationInsertAfterLineAnchor,
				LineAnchor: "func label() string {", Content: "\tvalue := \"next\"", Purpose: "insert local value",
			}},
		}

		edits := compilePlanIntent(root, artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})
		gomega.Expect(edits).To(gomega.HaveLen(1))
		gomega.Expect(edits[0].InsertAfterLine).To(gomega.Equal(3))
		gomega.Expect(edits[0].Content).To(gomega.Equal("\tvalue := \"next\""))
		expectSubmitPlanAccepts(root, edits)
	})

	ginkgo.It("compiles a new-file intent into a whole-file replacement", func() {
		root := ginkgo.GinkgoT().TempDir()
		artifact := Artifact{
			SchemaVersion: SchemaVersion,
			Source:        SourcePlanner,
			Scope:         Scope{RequiredEditPaths: []string{"internal/new.go"}},
			Intents: []Intent{{
				Path: "internal/new.go", Operation: OperationAddNewFile,
				NewFileContent: "package internal\n\nfunc NewLabel() string { return \"new\" }\n", Purpose: "add new label file",
			}},
		}

		edits := compilePlanIntent(root, artifact, Contract{RequiredEditPaths: []string{"internal/new.go"}})
		gomega.Expect(edits).To(gomega.HaveLen(1))
		gomega.Expect(edits[0].ReplaceAll).To(gomega.BeTrue())
		gomega.Expect(edits[0].NewText).NotTo(gomega.BeEmpty())
		expectSubmitPlanAccepts(root, edits)
	})

	ginkgo.It("blocks a non-unique exact anchor", func() {
		root := ginkgo.GinkgoT().TempDir()
		writePlanIntentFixture(root, "internal/a.go", "old\nold\n")
		artifact := validReplaceArtifact()
		artifact.Intents[0].OldText = "old"
		artifact.Intents[0].NewText = "new"
		expectCompileError(root, artifact, CodeNonUniqueAnchor)
	})

	ginkgo.It("blocks replacement without current source evidence", func() {
		artifact := validReplaceArtifact()
		artifact.Intents[0].OldText = "old"
		artifact.Intents[0].NewText = "new"
		expectCompileError(ginkgo.GinkgoT().TempDir(), artifact, CodeMissingSourceEvidence)
	})

	ginkgo.It("blocks an insert whose line anchor is missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		writePlanIntentFixture(root, "internal/a.go", "package internal\n")
		artifact := Artifact{
			SchemaVersion: SchemaVersion,
			Source:        SourcePlanner,
			Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
			Intents:       []Intent{{Path: "internal/a.go", Operation: OperationInsertAfterLineAnchor, LineAnchor: "func label() string {", Content: "\treturn \"next\""}},
		}
		expectCompileError(root, artifact, CodeAmbiguousAnchor)
	})

	ginkgo.It("blocks vocabulary the compiler does not own", func() {
		root := ginkgo.GinkgoT().TempDir()
		writePlanIntentFixture(root, "internal/a.go", "package internal\n")
		artifact := validReplaceArtifact()
		artifact.Intents[0].Operation = OperationReplaceSymbolBody
		artifact.Intents[0].Symbol = "label"
		artifact.Intents[0].DesiredBehavior = "return next label"
		expectCompileError(root, artifact, CodeUnsupportedOperation)
	})

	ginkgo.It("preserves a typed blocker instead of compiling an empty plan", func() {
		artifact := Artifact{
			SchemaVersion: SchemaVersion,
			Source:        SourcePlanner,
			Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
			Blockers:      []Blocker{{Code: "needs_source_evidence", Message: "current source body is missing", Paths: []string{"internal/a.go"}}},
		}
		expectCompileError(ginkgo.GinkgoT().TempDir(), artifact, CodeIntentBlocker)
	})
})

func compilePlanIntent(root string, artifact Artifact, contract Contract) []planartifact.PlanEdit {
	edits, err := Compile(artifact, CompileOptions{WorkspaceRoot: root, Contract: contract})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return edits
}

func expectCompileError(root string, artifact Artifact, want string) {
	_, err := Compile(artifact, CompileOptions{WorkspaceRoot: root, Contract: Contract{RequiredEditPaths: []string{"internal/a.go"}}})
	var compileErr *CompileError
	gomega.Expect(errors.As(err, &compileErr)).To(gomega.BeTrue())
	gomega.Expect(compileErr.Code).To(gomega.Equal(want))
}

func expectSubmitPlanAccepts(root string, edits []planartifact.PlanEdit) {
	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool: "submit_plan", Rationale: "candidate compiled from non-executable edit intent", Edits: edits,
	})
	gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.BeEmpty())
}

func writePlanIntentFixture(root, path, content string) {
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(fullPath, []byte(content), 0o644)).To(gomega.Succeed())
}
