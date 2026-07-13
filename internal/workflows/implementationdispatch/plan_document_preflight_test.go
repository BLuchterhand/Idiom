package implementationdispatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/workflows/brieflint"
)

var _ = ginkgo.Describe("Plan-document dispatch preflight", func() {
	ginkgo.It("rejects a missing workspace root", func(ctx ginkgo.SpecContext) {
		result := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{Document: PlanDocument{Path: "docs/atom.md"}})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.Blockers).To(gomega.ContainElement("workspace root is unavailable; cannot run dispatch preflight"))
	})

	ginkgo.It("rejects a plan-document path outside the workspace", func(ctx ginkgo.SpecContext) {
		result := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{
			WorkspaceRoot: ginkgo.GinkgoT().TempDir(),
			Document:      PlanDocument{Path: "../outside.md"},
		})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.Blockers).To(gomega.ContainElement("referenced plan document path is outside the workspace"))
	})

	ginkgo.It("stops before brief lint when atom intake reports source-satisfied", func(ctx ginkgo.SpecContext) {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchPreflightFile(root, "atom.md", "# Atom\n\nStatus: source-satisfied - do not dispatch\n")
		result := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{WorkspaceRoot: root, Document: PlanDocument{Path: "atom.md"}})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.AtomState).To(gomega.Equal(atomintakecontract.StateSourceSatisfied))
		gomega.Expect(result.LintBlockers).To(gomega.BeEmpty())
	})

	ginkgo.It("closes out a set of source-satisfied atoms and persists the evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		atomRel := ".idiom/atoms/design/atom-001.md"
		writeImplementationDispatchPreflightFile(root, atomRel, "# Atom\n\nStatus: pending\n\n## Scope\n\n- `pkg/source.go`\n")

		closeout, ok := TryPreflightSourceSatisfiedCloseout(root, []PlanDocumentPreflightResult{{
			Path:         atomRel,
			AtomState:    atomintakecontract.StateSourceSatisfied,
			Blockers:     []string{"source-satisfied atom should not dispatch"},
			LintBlockers: []string{"brief lint blocker retained as closeout evidence"},
		}})
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(closeout.Count).To(gomega.Equal(1))
		gomega.Expect(closeout.Message).To(gomega.And(
			gomega.ContainSubstring("Decision: source-satisfied"),
			gomega.ContainSubstring("source-satisfied atom should not dispatch"),
		))

		updated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(atomRel)))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(updated)).To(gomega.And(
			gomega.ContainSubstring("Status: source-satisfied - do not dispatch"),
			gomega.ContainSubstring("Atom intake reported this atom as source-satisfied before plan-execute dispatch."),
			gomega.ContainSubstring("source-satisfied atom should not dispatch"),
			gomega.ContainSubstring("brief lint blocker retained as closeout evidence"),
		))
	})

	ginkgo.It("does not close out or mutate mixed or empty preflight results", func() {
		root := ginkgo.GinkgoT().TempDir()
		atomRel := ".idiom/atoms/design/atom-001.md"
		original := "# Atom\n\nStatus: pending\n"
		writeImplementationDispatchPreflightFile(root, atomRel, original)

		_, ok := TryPreflightSourceSatisfiedCloseout(root, nil)
		gomega.Expect(ok).To(gomega.BeFalse())
		_, ok = TryPreflightSourceSatisfiedCloseout(root, []PlanDocumentPreflightResult{
			{Path: atomRel, AtomState: atomintakecontract.StateSourceSatisfied, Blockers: []string{"source-satisfied"}},
			{Path: ".idiom/atoms/design/atom-002.md", AtomState: atomintakecontract.StateMissingScope, Blockers: []string{"missing scope"}},
		})
		gomega.Expect(ok).To(gomega.BeFalse())
		updated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(atomRel)))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(updated)).To(gomega.Equal(original))
	})

	ginkgo.It("continues to brief lint after atom intake reports missing scope", func(ctx ginkgo.SpecContext) {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchPreflightFile(root, "atom.md", "# Atom\n\nStatus: planned\n")
		result := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{WorkspaceRoot: root, Document: PlanDocument{Path: "atom.md"}})
		gomega.Expect(result.AtomState).To(gomega.Equal(atomintakecontract.StateMissingScope))
		gomega.Expect(result.LintBlockers).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("passes explicit dirty-scope authority to atom intake", ginkgo.Label("development-scenario", "e2e", "integration"), func(ctx ginkgo.SpecContext) {
		if _, err := exec.LookPath("git"); err != nil {
			ginkgo.Skip("git not available on PATH")
		}
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}
		root := initImplementationDispatchPreflightGitRepo()
		writeImplementationDispatchPreflightFile(root, "foo.go", "package main\n")
		runImplementationDispatchPreflightGit(root, "add", "foo.go")
		runImplementationDispatchPreflightGit(root, "commit", "-m", "initial")
		writeImplementationDispatchPreflightFile(root, "foo.go", "package main\n\nfunc dirty() {}\n")
		writeImplementationDispatchPreflightFile(root, "atom.md", dispatchableBriefBody("foo.go"))

		prompt := "Implement atom.md with --allow-dirty."
		resolution := ResolveScope(ScopeInput{
			Prompt:          prompt,
			WorkspaceRoot:   root,
			TargetRefs:      []string{"atom.md"},
			RequiredTargets: []string{"foo.go"},
			MutationCapable: true,
			RouteReason:     "typed_plan_execute_command",
		})
		gomega.Expect(resolution.Blockers).To(gomega.BeEmpty())
		gomega.Expect(resolution.PlanDocuments).To(gomega.HaveLen(1))
		gomega.Expect(resolution.RequiredPaths).To(gomega.Equal([]string{"foo.go"}))
		gomega.Expect(resolution.AllowedPaths).To(gomega.BeEmpty())

		blocked := PreflightPlanDocuments(ctx, PlanDocumentsPreflightInput{
			WorkspaceRoot: root,
			PlanDocuments: resolution.PlanDocuments,
		})
		gomega.Expect(blocked).To(gomega.ConsistOf(gomega.HaveField("AtomState", atomintakecontract.StateBlockedDirty)))

		authorizedDocuments := PlanDocumentsWithDirtyAllowance(prompt, resolution.PlanDocuments)
		gomega.Expect(authorizedDocuments).To(gomega.ConsistOf(gomega.HaveField("AllowDirtyAtomScope", true)))
		gomega.Expect(PreflightPlanDocuments(ctx, PlanDocumentsPreflightInput{
			WorkspaceRoot: root,
			PlanDocuments: authorizedDocuments,
		})).To(gomega.BeEmpty())

		allowed := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{
			WorkspaceRoot: root,
			Document:      authorizedDocuments[0],
		})
		gomega.Expect(allowed.AtomState).To(gomega.BeEmpty())
		gomega.Expect(allowed.Blockers).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps brief-lint warnings nonblocking", func(ctx ginkgo.SpecContext) {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 not available on PATH")
		}
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationDispatchPreflightFile(root, ".git/HEAD", "ref: refs/heads/main\n")
		writeImplementationDispatchPreflightFile(root, "internal/foo/bar.go", "package foo\nfunc Existing() {}\n")
		writeImplementationDispatchPreflightFile(root, "atom.md", warningBriefBody("internal/foo/bar.go"))

		result := PreflightPlanDocument(ctx, PlanDocumentPreflightInput{WorkspaceRoot: root, Document: PlanDocument{Path: "atom.md"}})
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(result.Blockers).To(gomega.BeEmpty())
		gomega.Expect(result.LintBlockers).To(gomega.BeEmpty())
		gomega.Expect(result.Warnings).To(gomega.ContainElement(gomega.ContainSubstring("branching language in Required Edit")))
	})

	ginkgo.It("preserves a long actionable brief-lint blocker", func() {
		longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
		report := brieflint.Report{
			Exit: 1,
			Findings: []brieflint.Finding{{
				Severity: "BLOCK",
				Category: "anchor-specificity",
				Message:  longBlocker,
			}},
		}
		joined := strings.Join(briefLintBlockers(report), "\n")
		gomega.Expect(joined).To(gomega.ContainSubstring(longBlocker))
		gomega.Expect(joined).NotTo(gomega.ContainSubstring("[truncated"))
	})
})

func writeImplementationDispatchPreflightFile(root string, rel string, body string) {
	path := filepath.Join(root, rel)
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}

func dispatchableBriefBody(path string) string {
	return "# Atom\n\n" +
		"Status: planned\n\n" +
		"## Scope\n\n" +
		"- `" + path + "`\n\n" +
		"## Files to Modify\n\n" +
		"- `" + path + "`\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `" + path + ":1` | Read current source. |\n\n" +
		"## Verified Current State\n\n" +
		"`" + path + ":1` is current source.\n\n" +
		"## Required Edits\n\n" +
		"Read the file without changing it.\n\n" +
		"## Acceptance\n\n" +
		"- `go test ./...` succeeds.\n"
}

func warningBriefBody(path string) string {
	return "# Atom\n\n" +
		"Status: planned\n\n" +
		"## Scope\n\n" +
		"- `" + path + "`\n\n" +
		"## Files to Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `" + path + "` | ADD `func Added()` at line 2. |\n\n" +
		"## Current state\n\n" +
		"Line 2 is `func Existing() {}`.\n\n" +
		"## Required Edits\n\n" +
		"### Edit 1 - add Added\n\n" +
		"Either add the function alongside Existing or define it in a new file.\n\n" +
		"## Acceptance\n\n" +
		"- `go test ./...` succeeds.\n"
}

func initImplementationDispatchPreflightGitRepo() string {
	root := ginkgo.GinkgoT().TempDir()
	runImplementationDispatchPreflightGit(root, "init")
	runImplementationDispatchPreflightGit(root, "config", "user.email", "test@example.com")
	runImplementationDispatchPreflightGit(root, "config", "user.name", "Test User")
	return root
}

func runImplementationDispatchPreflightGit(root string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %v\n%s", args, out)
}
