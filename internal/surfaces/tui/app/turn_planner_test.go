package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/implementationdispatch"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
	"idiom/internal/workflows/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

func disableQueueDaemonForTurnPlannerTest(t ginkgo.FullGinkgoTInterface, root string) {
	ginkgo.GinkgoHelper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
}

func initializeTurnPlannerGitRepository(t ginkgo.FullGinkgoTInterface, root string) {
	ginkgo.GinkgoHelper()

	commands := [][]string{
		{"init", "-q"},
		{"config", "user.email", "turn-planner-test@example.com"},
		{"config", "user.name", "Turn Planner Test"},
		{"add", "--all"},
		{"commit", "-q", "-m", "establish turn planner fixture"},
	}
	for _, args := range commands {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(output))
	}
}

func stubImplementationDispatchPlanDocumentPreflight(t ginkgo.FullGinkgoTInterface, stub func(context.Context, config.Config, implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult) {
	ginkgo.GinkgoHelper()

	orig := planDocumentPreflightRunner

	planDocumentPreflightRunner = func(ctx context.Context, input implementationdispatch.PlanDocumentPreflightInput) implementationdispatch.PlanDocumentPreflightResult {
		cfg := config.Config{}
		cfg.Workspace.Root = input.WorkspaceRoot
		cfg.Workspace.CWD = input.WorkspaceCWD

		return stub(ctx, cfg, input.Document)
	}

	t.Cleanup(func() { planDocumentPreflightRunner = orig })
}

func allowImplementationDispatchPlanDocumentPreflight(t ginkgo.FullGinkgoTInterface) {
	ginkgo.GinkgoHelper()
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
	})
}

func stubImplementationDispatchPlanDocumentRefine(t ginkgo.FullGinkgoTInterface, stub func(context.Context, config.Config, implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult) {
	ginkgo.GinkgoHelper()

	orig := planDocumentRefineRunner

	planDocumentRefineRunner = stub

	t.Cleanup(func() { planDocumentRefineRunner = orig })
}

var _ = ginkgo.It("implementation dispatch skips refine scratchpad meta briefs", func() {
	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.EnableWrites = true

	plan := promptExecutionPlan{
		route: turnRoutePlan{Family: "implementation"},
	}
	prompt := strings.Join([]string{
		"# META - Review brief (refine loop)",
		"",
		"Expected path: call `write_file` to create `.idiom/refine/example/iter-1/review.md`.",
		"Required reads:",
		"1. `.idiom/refine/example/iter-1/draft.md`",
	}, "\n")

	m := &Model{cfg: cfg}
	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(prompt, plan)

	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("refine scratchpad meta-brief should stay on implement lane, got %#v", decision))
})

var _ = ginkgo.It("implementation dispatch plan document skips refine iter artifacts but allows accepted brief", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	iterDir := filepath.Join(root, ".idiom", "refine", "example", "iter-1")

	{
		err := os.MkdirAll(iterDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir iter dir: %v", err))
	}

	draftPath := filepath.Join(iterDir, "draft.md")
	{
		err := os.WriteFile(draftPath, []byte("# Draft\n\n## Files To Modify\n\n- target.txt\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write draft: %v", err))
	}

	acceptedPath := filepath.Join(root, ".idiom", "refine", "example", "accepted.md")
	{
		err := os.WriteFile(acceptedPath, []byte("# Accepted\n\n## Files To Modify\n\n- target.txt\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write accepted: %v", err))
	}

	{
		_, ok := implementationdispatch.ReadPlanDocument(root, ".idiom/refine/example/iter-1/draft.md")
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("refine iter draft should not be treated as a plan document for recursive TUI refine"))
	}

	{
		_, ok := implementationdispatch.ReadPlanDocument(root, ".idiom/refine/example/accepted.md")
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("accepted refine output should remain readable as a plan document"))
	}
})

var _ = ginkgo.It("implementation dispatch refine title is bounded", func() {
	longPath := ".idiom/refine/" + strings.Repeat("nested-title-", 40) + "iter-1/draft.md"
	title := implementationdispatch.DocumentOutputTitle(longPath)

	gomega.Expect(strings.HasPrefix(title, "tui-")).To(gomega.BeTrue(), fmt.Sprintf("title %q missing tui prefix", title))

	gomega.Expect(len(title) > 84).To(gomega.BeFalse(), fmt.Sprintf("title length = %d, want <= 84: %s", len(title), title))
})

var _ = ginkgo.It("implementation dispatch nonzero refine candidate carries preflight blockers", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		return implementationdispatch.PlanDocumentPreflightResult{
			Path:      doc.Path,
			OK:        false,
			AtomState: "blocked-dirty-files",
			Blockers: []string{
				"atom intake: existing scoped paths have uncommitted changes",
				"atom intake: dirty scoped paths: target.txt",
			},
		}
	})

	acceptedRel := filepath.ToSlash(filepath.Join(".idiom", "refine", "example", "accepted.md"))
	acceptedAbs := filepath.Join(root, filepath.FromSlash(acceptedRel))

	{
		err := os.MkdirAll(filepath.Dir(acceptedAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir refine dir: %v", err))
	}

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
		"| target.txt | modify target |",
		"",
	}, "\n")
	{
		err := os.WriteFile(acceptedAbs, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write accepted refine output: %v", err))
	}

	candidate, candidatePath, blockers, ok := materializeImplementationDispatchNonzeroRefineAcceptedCandidate(context.Background(), cfg, acceptedRel)
	gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("candidate should remain blocked by preflight"))

	gomega.Expect(candidatePath).To(gomega.Equal(".idiom/refine/example/accepted.dispatch.md"), fmt.Sprintf("candidate path = %q", candidatePath))

	gomega.Expect(candidate.Path).To(gomega.Equal(candidatePath), fmt.Sprintf("candidate document path = %q, want %q", candidate.Path, candidatePath))

	gomega.Expect(len(blockers)).To(gomega.Equal(1), fmt.Sprintf("expected dirty-path blocker to survive materialization, got %#v", blockers))
	gomega.Expect(strings.Join(blockers[0].Blockers, "\n")).To(gomega.ContainSubstring("dirty scoped paths: target.txt"), fmt.Sprintf("expected dirty-path blocker to survive materialization, got %#v", blockers))
})

var _ = ginkgo.It("implementation dispatch dirty allowance marks only named scoped document", func() {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"implement atom and explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go for this run",
		docs,
	)

	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeTrue(), fmt.Sprintf("expected named dirty scoped path to allow matching document: %#v", got))

	gomega.Expect(got[1].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("dirty scoped path allowance must not apply to unrelated document: %#v", got))
})

var _ = ginkgo.It("implementation dispatch dirty allowance ignores quoted next action", func() {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake said next: finish, commit, or explicitly allow the dirty scoped paths before dispatch",
		docs,
	)

	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("quoted blocker next-action must not grant dirty scoped path allowance: %#v", got))

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake state=blocked-dirty-files - existing scoped paths have uncommitted changes - dirty paths: internal/contracts/writerroute/decision_test.go - next: finish, commit, or explicitly allow the dirty scoped paths before dispatch",
		docs,
	)
	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("pasted dirty-path blocker must not grant dirty scoped path allowance: %#v", got))

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"explicitly allow the dirty scoped paths before dispatch. Stay inside internal/contracts/writerroute/decision_test.go.",
		docs,
	)
	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("scope path in a later sentence must not grant dirty scoped path allowance: %#v", got))

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"do not explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		docs,
	)
	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("negated dirty scoped path allowance must not grant authority: %#v", got))

	for _, prompt := range []string{
		"don’t explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		"dont explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		"cannot allow dirty path internal/contracts/writerroute/decision_test.go",
		"can't allow dirty path internal/contracts/writerroute/decision_test.go",
		"can’t allow dirty path internal/contracts/writerroute/decision_test.go",
	} {
		got = implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, docs)
		gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("negated dirty scoped path allowance %q must not grant authority: %#v", prompt, got))
	}

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake blocked; next: allow dirty path internal/contracts/writerroute/decision_test.go",
		docs,
	)
	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("pasted next-action dirty scoped path allowance must not grant authority: %#v", got))
})

var _ = ginkgo.It("implementation dispatch dirty allowance broad flag allows all scoped documents", func() {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance("rerun with --allow-dirty for these atom scopes", docs)

	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeTrue(), fmt.Sprintf("--allow-dirty should explicitly allow all atom documents in the turn: %#v", got))
	gomega.Expect(got[1].AllowDirtyAtomScope).To(gomega.BeTrue(), fmt.Sprintf("--allow-dirty should explicitly allow all atom documents in the turn: %#v", got))
})

var _ = ginkgo.It("implementation dispatch dirty allowance broad flag requires affirmative context", func() {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	for _, prompt := range []string{
		"do not use `--allow-dirty` for this run",
		"rerun without --allow-dirty",
		"CLI help: --allow-dirty allows dirty atom scopes",
		"avoid --allow-dirty unless the user says so",
		"atom intake blocked; next: rerun with --allow-dirty",
	} {
		got := implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, docs)
		gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("non-affirmative broad dirty allowance %q must not allow documents: %#v", prompt, got))
		gomega.Expect(got[1].AllowDirtyAtomScope).To(gomega.BeFalse(), fmt.Sprintf("non-affirmative broad dirty allowance %q must not allow documents: %#v", prompt, got))
	}

	got := implementationdispatch.PlanDocumentsWithDirtyAllowance("rerun with `--allow-dirty` for these atom scopes", docs)
	gomega.Expect(got[0].AllowDirtyAtomScope).To(gomega.BeTrue(), fmt.Sprintf("affirmative broad dirty allowance should allow all atom documents: %#v", got))
	gomega.Expect(got[1].AllowDirtyAtomScope).To(gomega.BeTrue(), fmt.Sprintf("affirmative broad dirty allowance should allow all atom documents: %#v", got))
})

var _ = ginkgo.It("implementation dispatch atom marks missing path as new file", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	atom := implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:   "internal/contracts/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})

	gomega.Expect(strings.HasPrefix(atom.Change, "NEW FILE:")).To(gomega.BeTrue(), fmt.Sprintf("missing path atom change = %q, want NEW FILE marker", atom.Change))

	{
		err := os.MkdirAll(filepath.Join(root, "internal", "contracts", "writerroute"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir writerroute: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "internal", "contracts", "writerroute", "decision.go"), []byte("package writerroute\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write existing file: %v", err))
	}

	atom = implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:   "internal/contracts/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})
	gomega.Expect(strings.HasPrefix(atom.Change, "NEW FILE:")).To(gomega.BeFalse(), fmt.Sprintf("existing path atom change = %q, should not add NEW FILE marker", atom.Change))

	gomega.Expect(len(atom.Anchors)).NotTo(gomega.Equal(0), fmt.Sprintf("existing path atom should carry host current-state anchors"))

	gomega.Expect(atom.Anchors[0]).To(gomega.ContainSubstring("internal/contracts/writerroute/decision.go:1"), fmt.Sprintf("unexpected existing path anchor: %#v", atom.Anchors))
	gomega.Expect(atom.Anchors[0]).To(gomega.ContainSubstring("package writerroute"), fmt.Sprintf("unexpected existing path anchor: %#v", atom.Anchors))
})

var _ = ginkgo.It("implementation dispatch atom anchors preserve complete source lines", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("internal", "contracts", "writerroute", "decision.go"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))

	{
		err := os.MkdirAll(filepath.Dir(srcPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir source dir: %v", err))
	}

	longLine := "func NewDecision(mode WriterMode, lane string, plannerModel string, executorModel string, reviewerModel string, classifierModel string, riskTier string, costTier string, scopeTier string, verificationOwner string, reviewPolicy string, requiredEditPaths []string, evidencePacketIDs []string, reasonCodes []string, escalationTriggers []string, userOverride bool, taskFamily string) Decision {"
	source := "package writerroute\n\n" + longLine + "\n\treturn Decision{}\n}\n"

	{
		err := os.WriteFile(srcPath, []byte(source), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	atom := implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:    relPath,
		Change:  "Preserve the NewDecision constructor shape",
		Excerpt: "NewDecision constructor shape",
	})
	anchors := strings.Join(atom.Anchors, "\n")

	gomega.Expect(strings.Contains(anchors, longLine)).To(gomega.BeTrue(), fmt.Sprintf("anchor lost complete source line:\n%s", anchors))

	gomega.Expect(strings.Contains(anchors, "...")).To(gomega.BeFalse(), fmt.Sprintf("source anchors must not truncate actionable snippets:\n%s", anchors))
})

var _ = ginkgo.It("implementation dispatch materialized atoms carry design doc test obligations", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, path := range []string{"pkg/source.go", "pkg/source_test.go"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		{
			err := os.MkdirAll(filepath.Dir(full), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", path, err))
		}

		{
			err := os.WriteFile(full, []byte("package pkg\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
		}
	}

	doc := implementationdispatch.PlanDocument{
		Path: "docs/design.md",
		Content: strings.Join([]string{
			"# Design",
			"",
			"## Expected File Scope",
			"",
			"| File | Change |",
			"|---|---|",
			"| `pkg/source.go` | Add route decision source behavior. |",
			"",
			"## Implementation Steps",
			"",
			"6. Add tests.",
			"",
			"Minimum tests:",
			"",
			"- `pkg/source_test.go`: add `TestRouteDecisionShape`.",
			"- `go test ./pkg`",
		}, "\n"),
		EditPaths:       []string{"pkg/source.go"},
		TestObligations: []string{"`pkg/source_test.go`: add `TestRouteDecisionShape`.", "`go test ./pkg`"},
	}
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	atoms, err := implementationdispatch.MaterializePlanDocumentAtoms(root, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("materialize atoms: %v", err))

	var testAtom implementationdispatch.PlanDocument

	for _, atom := range atoms {
		if slices.Contains(atom.EditPaths, "pkg/source_test.go") {
			testAtom = atom
			break
		}
	}

	gomega.Expect(strings.TrimSpace(testAtom.Path)).NotTo(gomega.Equal(""), fmt.Sprintf("expected test obligation to materialize a test atom, got %#v", atoms))

	gomega.Expect(testAtom.Content).To(gomega.ContainSubstring("## Design Doc Test Obligations"), fmt.Sprintf("materialized test atom missing design-doc test obligations:\n%s", testAtom.Content))
	gomega.Expect(testAtom.Content).To(gomega.ContainSubstring("TestRouteDecisionShape"), fmt.Sprintf("materialized test atom missing design-doc test obligations:\n%s", testAtom.Content))
	gomega.Expect(testAtom.Content).To(gomega.ContainSubstring("go test ./pkg"), fmt.Sprintf("materialized test atom missing design-doc test obligations:\n%s", testAtom.Content))
})

var _ = ginkgo.It("implementation dispatch materialized atoms preserve terminal atom state", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "pkg", "source.go")

	{
		err := os.MkdirAll(filepath.Dir(sourcePath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir source dir: %v", err))
	}

	{
		err := os.WriteFile(sourcePath, []byte("package pkg\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	doc := implementationdispatch.PlanDocument{
		Path: "docs/design.md",
		Content: strings.Join([]string{
			"# Design",
			"",
			"## Expected File Scope",
			"",
			"| File | Change |",
			"|---|---|",
			"| `pkg/source.go` | Add source behavior. |",
		}, "\n"),
		EditPaths: []string{"pkg/source.go"},
	}
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	atomRel := ".idiom/atoms/tui-docs-design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	{
		err := os.MkdirAll(filepath.Dir(atomAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	existing := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: completed",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/source.go | Add source behavior. |",
		"",
		"## Result",
		"",
		"Status: completed",
		"",
		"Original closeout evidence.",
		"",
	}, "\n")
	{
		err := os.WriteFile(atomAbs, []byte(existing), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write existing atom: %v", err))
	}

	atoms, err := implementationdispatch.MaterializePlanDocumentAtoms(root, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("materialize atoms: %v", err))

	gomega.Expect(len(atoms)).To(gomega.Equal(1), fmt.Sprintf("expected existing atom to be returned, got %#v", atoms))
	gomega.Expect(atoms[0].Path).To(gomega.Equal(atomRel), fmt.Sprintf("expected existing atom to be returned, got %#v", atoms))

	status, ok := implementationdispatch.PlanDocumentTerminalAtomStatus(atoms[0])
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("existing atom should stay terminal, status=%q ok=%v", status, ok))
	gomega.Expect(status).To(gomega.Equal("completed"), fmt.Sprintf("existing atom should stay terminal, status=%q ok=%v", status, ok))

	updated, err := os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read atom: %v", err))

	gomega.Expect(string(updated)).To(gomega.Equal(existing), fmt.Sprintf("materialization overwrote terminal atom:\n%s", string(updated)))
})

var _ = ginkgo.It("run atomized implementation plan documents skips terminal atoms", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	{
		err := os.MkdirAll(filepath.Dir(atomAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	body := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: completed",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/source.go | Add source behavior. |",
		"",
		"## Result",
		"",
		"Status: completed",
		"",
	}, "\n")
	{
		err := os.WriteFile(atomAbs, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write atom: %v", err))
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("atom doc should be readable"))

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runAtomizedImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("run atomized plan documents: %v", err))

	gomega.Expect(answer).To(gomega.ContainSubstring("already closed"), fmt.Sprintf("closed atom should be skipped without dispatch:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("skipped: completed"), fmt.Sprintf("closed atom should be skipped without dispatch:\n%s", answer))

	updated, err := os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read atom: %v", err))

	gomega.Expect(string(updated)).To(gomega.Equal(body), fmt.Sprintf("skipping terminal atom should not rewrite it:\n%s", string(updated)))
})

var _ = ginkgo.It("implementation dispatch source satisfied requires test obligation evidence", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	testPath := "pkg/source_test.go"
	full := filepath.Join(root, filepath.FromSlash(testPath))

	{
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir source test: %v", err))
	}

	{
		err := os.WriteFile(full, []byte("package pkg\n\nvar routeDecisionHarness = true\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source test: %v", err))
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-001.md", testPath, "Add `routeDecisionHarness` coverage", []string{
		"`pkg/source_test.go:1`: `package pkg`",
		"`pkg/source_test.go:3`: `var routeDecisionHarness = true`",
	})

	doc.Content += "\n\n## Design Doc Test Obligations\n\n- `pkg/source_test.go`: add `TestRouteDecisionShape`.\n"

	doc.TestObligations = implementationdispatch.DesignDocTestObligations(doc.Content)

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	{
		records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
		gomega.Expect(len(records)).To(gomega.Equal(0), fmt.Sprintf("source-satisfied must not close without matching test evidence: %#v", records))
	}

	{
		err := os.WriteFile(full, []byte("package pkg\n\nvar routeDecisionHarness = true\n\nfunc TestRouteDecisionShape(t *testing.T) {}\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source test with evidence: %v", err))
	}

	closeout, ok := implementationdispatch.TryPlanDocumentsSourceSatisfiedCloseout(root, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied closeout"))

	message := closeout.Message
	gomega.Expect(message).To(gomega.ContainSubstring("Test obligations"), fmt.Sprintf("source-satisfied message should report test obligations and evidence:\n%s", message))
	gomega.Expect(message).To(gomega.ContainSubstring("TestRouteDecisionShape"), fmt.Sprintf("source-satisfied message should report test obligations and evidence:\n%s", message))
})

var _ = ginkgo.It("implementation dispatch source satisfied requires verification command evidence", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sourcePath := "pkg/source.go"
	full := filepath.Join(root, filepath.FromSlash(sourcePath))

	{
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir source: %v", err))
	}

	{
		err := os.WriteFile(full, []byte("package pkg\n\nfunc routeDecisionHarness() {}\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-001.md", sourcePath, "Add `routeDecisionHarness` implementation", []string{
		"`pkg/source.go:1`: `package pkg`",
		"`pkg/source.go:3`: `func routeDecisionHarness() {}`",
	})

	doc.Content += "\n\n## Design Doc Test Obligations\n\n- `go test ./pkg`\n"

	doc.TestObligations = implementationdispatch.DesignDocTestObligations(doc.Content)

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	{
		records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
		gomega.Expect(len(records)).To(gomega.Equal(0), fmt.Sprintf("source-satisfied must not treat an unrun verification command as evidence: %#v", records))
	}
})

var _ = ginkgo.It("implementation dispatch blocked question does not mark atom completed", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	{
		err := os.MkdirAll(filepath.Dir(atomAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	body := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/source.go | Add source behavior. |",
		"",
	}, "\n")
	{
		err := os.WriteFile(atomAbs, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write atom: %v", err))
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("atom doc should be readable"))

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	answer := "<<QUESTION>>: Plan-execute could not produce a plan artifact.\n\nRequired edit paths still need a plan:\n- pkg/source.go"
	gomega.Expect(implementationdispatch.AnswerLooksBlocked(answer)).To(gomega.BeTrue(), fmt.Sprintf("question answer should be classified as blocked"))

	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, answer, nil)

	updated, err := os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read atom: %v", err))

	gomega.Expect(string(updated)).To(gomega.Not(gomega.ContainSubstring("Status: completed")), fmt.Sprintf("blocked question must not mark atom completed:\n%s", string(updated)))
	gomega.Expect(string(updated)).To(gomega.Not(gomega.ContainSubstring("## Result")), fmt.Sprintf("blocked question must not mark atom completed:\n%s", string(updated)))
})

var _ = ginkgo.It("implementation dispatch command obligation must be proven before atom complete", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	{
		err := os.MkdirAll(filepath.Dir(atomAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	body := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: pending",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/source.go | Add source behavior. |",
		"",
		"## Design Doc Test Obligations",
		"",
		"- `go test ./pkg`",
		"",
	}, "\n")
	{
		err := os.WriteFile(atomAbs, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write atom: %v", err))
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("atom doc should be readable"))

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	unprovenAnswer := "Decision: source-satisfied\n\n`pkg/source.go` already contains the requested source."
	{
		pending := implementationdispatch.UnprovenVerificationCommands(doc, unprovenAnswer)
		gomega.Expect(len(pending)).To(gomega.Equal(1), fmt.Sprintf("expected pending command, got %#v", pending))
	}

	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, unprovenAnswer, nil)

	updated, err := os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read atom: %v", err))

	gomega.Expect(string(updated)).To(gomega.Not(gomega.ContainSubstring("Status: completed")), fmt.Sprintf("unproven command must not mark atom complete:\n%s", string(updated)))
	gomega.Expect(string(updated)).To(gomega.Not(gomega.ContainSubstring("## Result")), fmt.Sprintf("unproven command must not mark atom complete:\n%s", string(updated)))

	provenAnswer := "Completed source work.\n\nVerification: `go test ./pkg` passed with exit 0."
	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, provenAnswer, nil)

	updated, err = os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read atom after proven answer: %v", err))

	gomega.Expect(string(updated)).To(gomega.ContainSubstring("Status: completed"), fmt.Sprintf("proven command should allow completion:\n%s", string(updated)))
	gomega.Expect(string(updated)).To(gomega.ContainSubstring("go test ./pkg"), fmt.Sprintf("proven command should allow completion:\n%s", string(updated)))
})

var _ = ginkgo.It("writer route decision for prompt reports guarded implement for exact edit packet", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.LlamaServer.Model = "planner-model"
	cfg.RouteClassifier.Model = "classifier-model"

	m := &Model{
		cfg:          cfg,
		plannerSpec:  config.LlamaServer{Model: "planner-model"},
		executorSpec: config.LlamaServer{Model: "executor-model"},
	}
	plan := promptExecutionPlan{
		mode: modepolicy.BuildMode(),
		route: turnRoutePlan{
			Family: "implementation",
		},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/surfaces/tui/app/writer_route.go"},
		},
	}

	prompt := strings.Join([]string{
		"Apply the exact writer route edit.",
		"",
		"Required edit paths: internal/surfaces/tui/app/writer_route.go",
		"",
		"## Required Edits",
		"",
		"old_text:",
		"```text",
		"old writer route",
		"```",
		"",
		"new_text:",
		"```text",
		"new writer route",
		"```",
	}, "\n")
	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(prompt, plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("unexpected writer route decision: %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected writer route decision: %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))

	gomega.Expect(decision.PlannerModel).To(gomega.Equal("planner-model"), fmt.Sprintf("unexpected model fields: %#v", decision))
	gomega.Expect(decision.ExecutorModel).To(gomega.Equal("executor-model"), fmt.Sprintf("unexpected model fields: %#v", decision))
	gomega.Expect(decision.ClassifierModel).To(gomega.Equal("classifier-model"), fmt.Sprintf("unexpected model fields: %#v", decision))

	gomega.Expect(slices.Contains(decision.RequiredEditPaths, "internal/surfaces/tui/app/writer_route.go")).To(gomega.BeTrue(), fmt.Sprintf("required paths missing from writer route decision: %#v", decision.RequiredEditPaths))
})

var _ = ginkgo.It("writer route decision for prompt does not create semantic tdd scope from prose", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Implement a TDD coverage-gate fix and add regression tests. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("semantic TDD source+test implementation should stay on guarded implement until semantic plan handoff exists, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("semantic TDD source+test implementation should stay on guarded implement until semantic plan handoff exists, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor")).To(gomega.BeFalse(), fmt.Sprintf("prompt prose must not create semantic source/test executor reason: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt does not use tdd product keyword for semantic scope", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Implement a TDD product workflow coverage-gate fix. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("implementation prompt should stay on guarded implement lane, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("implementation prompt should stay on guarded implement lane, got %#v", decision))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor")).To(gomega.BeFalse(), fmt.Sprintf("TDD/product wording alone must not create semantic source/test executor reason: %#v", decision.ReasonCodes))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("direct implementation reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt does not use plan execute phrase for semantic tdd scope", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Use the plan-execute lane to implement a TDD coverage-gate fix and add regression tests. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("plan-execute prose mention should not route to plan-execute, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("plan-execute prose mention should not route to plan-execute, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor")).To(gomega.BeFalse(), fmt.Sprintf("plan-execute prose mention must not create semantic source/test executor reason: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("plan execute slash command execution plan uses planner metadata", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	m := &Model{cfg: cfg}
	objective := "Implement the typed TUI command. Required edit paths only: internal/workflows/planexecute/prompt.go, internal/workflows/planexecute/context_pack_test.go, internal/surfaces/tui/app/update_handlers.go, internal/surfaces/tui/app/lifecycle.go."

	execution := newDetachedPromptExecutionSession(context.Background(), m)
	plan := execution.planExecuteCommandExecutionPlan(objective)
	decision := execution.writerRouteDecisionForPrompt(objective, plan)
	wantPaths := []string{
		"internal/workflows/planexecute/prompt.go",
		"internal/workflows/planexecute/context_pack_test.go",
		"internal/surfaces/tui/app/update_handlers.go",
		"internal/surfaces/tui/app/lifecycle.go",
	}

	gomega.Expect(plan.mode.Name).To(gomega.Equal(runprofile.AgentModeBuild), fmt.Sprintf("mode = %q, want build", plan.mode.Name))

	gomega.Expect(plan.route.Family).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("typed command should set plan-execute planner family, route=%#v brief=%#v", plan.route, plan.runtimeBrief))
	gomega.Expect(plan.runtimeBrief.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("typed command should set plan-execute planner family, route=%#v brief=%#v", plan.route, plan.runtimeBrief))

	gomega.Expect(slices.Equal(plan.runtimeBrief.RequiredEditPaths, wantPaths)).To(gomega.BeTrue(), fmt.Sprintf("required edit paths = %#v, want %#v", plan.runtimeBrief.RequiredEditPaths, wantPaths))

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)), fmt.Sprintf("typed command should report plan-execute writer metadata, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("plan-execute"), fmt.Sprintf("typed command should report plan-execute writer metadata, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("typed command writer metadata missing planner route, got %#v", decision))
	gomega.Expect(decision.ReasonCodes).To(gomega.ContainElement("route.plan_execute_planner"), fmt.Sprintf("typed command writer metadata missing planner route, got %#v", decision))
})

var _ = ginkgo.It("classifier selected plan execute family activates plan submission", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	m := &Model{cfg: cfg}
	objective := "Can you send this product workflow implementation atom through the TUI plan-execute flow?"

	for _, family := range []string{
		"plan_then_execute_planner",
		"plan_execute_planner",
		"plan_execute",
		"plan_then_execute",
	} {
		ginkgo.By(fmt.Sprint(family), func() {
			route := normalizeTurnRoutePlan(turnRoutePlan{
				Family:      family,
				InitialLane: "plan-execute",
				Reason:      "classifier_atom_dispatch",
				Contract:    "implementation_result",
				RouteSource: routeSourceClassifier,
			}, modepolicy.BuildMode())
			plan := promptExecutionPlan{
				mode:  modepolicy.BuildMode(),
				route: route,
				runtimeBrief: workerruntime.WorkerRuntimeBrief{
					TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
				},
			}

			decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(objective, plan)

			gomega.Expect(route.Family).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("normalized family = %q, want %s", route.Family, agent.PlanThenExecutePlannerTaskFamily))

			gomega.Expect(route.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("normalized route lost planner execution contract: %#v", route))
			gomega.Expect(route.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("normalized route lost planner execution contract: %#v", route))

			gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)), fmt.Sprintf("classifier-selected planner family should activate plan submission, got %#v", decision))
			gomega.Expect(decision.Lane).To(gomega.Equal("plan-execute"), fmt.Sprintf("classifier-selected planner family should activate plan submission, got %#v", decision))

			gomega.Expect(decision.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("classifier-selected planner route missing plan submission metadata, got %#v", decision))
			gomega.Expect(decision.ReasonCodes).To(gomega.ContainElement("route.plan_execute_planner"), fmt.Sprintf("classifier-selected planner route missing plan submission metadata, got %#v", decision))
		})
	}
})

var _ = ginkgo.It("route profile semantics characterize families for runtime profile extraction", func() {
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true

	m := &Model{cfg: cfg}
	cases := []struct {
		name                 string
		family               string
		wantFamily           string
		wantInitialLane      string
		wantFollowupLane     string
		wantContract         string
		wantMutationCapable  bool
		wantWriterMode       writerroute.WriterMode
		wantWriterLane       string
		wantVerificationOwnr string
	}{
		{name: "direct question alias", family: "direct_question", wantFamily: "conversation", wantInitialLane: "main", wantContract: string(workerruntime.ContractDirectAnswer), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "main"},
		{name: "debug logs collapse", family: "debug_logs", wantFamily: "conversation", wantInitialLane: "main", wantContract: string(workerruntime.ContractDirectAnswer), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "main"},
		{name: "conversation", family: "conversation", wantFamily: "conversation", wantInitialLane: "main", wantContract: string(workerruntime.ContractDirectAnswer), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "main"},
		{name: "verification only", family: "verification_only", wantFamily: "verification_only", wantInitialLane: "main", wantContract: string(workerruntime.ContractDirectAnswer), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "main"},
		{name: "locate", family: "locate", wantFamily: "locate", wantInitialLane: "locate", wantContract: string(workerruntime.ContractLocateTargets), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "locate"},
		{name: "locate then implement", family: "locate_then_implement", wantFamily: "locate_then_implement", wantInitialLane: "locate", wantFollowupLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "explore then implement", family: "explore_then_implement", wantFamily: "explore_then_implement", wantInitialLane: "explore", wantFollowupLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "review then implement", family: "review_then_implement", wantFamily: "review_then_implement", wantInitialLane: "review", wantFollowupLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "architecture", family: "architecture", wantFamily: "architecture", wantInitialLane: "architecture", wantContract: string(workerruntime.ContractArchitecture), wantWriterMode: writerroute.ModeReadOnly, wantWriterLane: "architecture"},
		{name: "architecture then implement", family: "architecture_then_implement", wantFamily: "architecture_then_implement", wantInitialLane: "architecture", wantFollowupLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "implementation", family: "implementation", wantFamily: "implementation", wantInitialLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "docs edit", family: "docs_edit", wantFamily: "docs_edit", wantInitialLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModeAgenticGuarded, wantWriterLane: "implement", wantVerificationOwnr: "executor"},
		{name: "plan execute alias", family: "plan_execute", wantFamily: agent.PlanThenExecutePlannerTaskFamily, wantInitialLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModePlanSubmission, wantWriterLane: "plan-execute", wantVerificationOwnr: "planner_owned"},
		{name: "plan then execute alias", family: "plan_then_execute", wantFamily: agent.PlanThenExecutePlannerTaskFamily, wantInitialLane: "implement", wantContract: string(workerruntime.ContractImplementationResult), wantMutationCapable: true, wantWriterMode: writerroute.ModePlanSubmission, wantWriterLane: "plan-execute", wantVerificationOwnr: "planner_owned"},
		{name: "plan execute review repair", family: agent.PlanThenExecuteReviewRepairTaskFamily, wantFamily: agent.PlanThenExecuteReviewRepairTaskFamily, wantInitialLane: "review", wantContract: string(workerruntime.ContractReviewFindings), wantWriterMode: writerroute.ModePlanSubmission, wantWriterLane: "plan-execute", wantVerificationOwnr: "planner_owned"},
	}

	for _, tc := range cases {
		ginkgo.By(fmt.Sprint(tc.name), func() {
			route := normalizeTurnRoutePlan(turnRoutePlan{Family: tc.family}, modepolicy.BuildMode())
			gomega.Expect(route.Family).To(gomega.Equal(tc.wantFamily), fmt.Sprintf("normalized route = %#v, want family=%q initial=%q followup=%q contract=%q", route, tc.wantFamily, tc.wantInitialLane, tc.wantFollowupLane, tc.wantContract))
			gomega.Expect(route.InitialLane).To(gomega.Equal(tc.wantInitialLane), fmt.Sprintf("normalized route = %#v, want family=%q initial=%q followup=%q contract=%q", route, tc.wantFamily, tc.wantInitialLane, tc.wantFollowupLane, tc.wantContract))
			gomega.Expect(route.FollowupLane).To(gomega.Equal(tc.wantFollowupLane), fmt.Sprintf("normalized route = %#v, want family=%q initial=%q followup=%q contract=%q", route, tc.wantFamily, tc.wantInitialLane, tc.wantFollowupLane, tc.wantContract))
			gomega.Expect(route.Contract).To(gomega.Equal(tc.wantContract), fmt.Sprintf("normalized route = %#v, want family=%q initial=%q followup=%q contract=%q", route, tc.wantFamily, tc.wantInitialLane, tc.wantFollowupLane, tc.wantContract))

			projection := projectRouteContract(route, modepolicy.BuildMode())
			gomega.Expect(projection.MutationCapable).To(gomega.Equal(tc.wantMutationCapable), fmt.Sprintf("route contract mutation_capable = %t, want %t for %#v", projection.MutationCapable, tc.wantMutationCapable, projection))

			rendered := renderRouteContractProjection(projection)
			var payload map[string]any
			{
				err := json.Unmarshal([]byte(rendered), &payload)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("route contract projection is not valid JSON: %v\n%s", err, rendered))
			}
			gomega.Expect(payload["family"]).To(gomega.Equal(tc.wantFamily), fmt.Sprintf("rendered route projection = %#v", payload))
			gomega.Expect(payload["lane"]).To(gomega.Equal(tc.wantInitialLane), fmt.Sprintf("rendered route projection = %#v", payload))
			gomega.Expect(payload["contract"]).To(gomega.Equal(tc.wantContract), fmt.Sprintf("rendered route projection = %#v", payload))

			decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("route profile characterization", promptExecutionPlan{
				mode:         modepolicy.BuildMode(),
				route:        route,
				runtimeBrief: turnRoutePlanRuntimeBrief(route, modepolicy.BuildMode(), nil),
			})
			gomega.Expect(decision.WriterMode).To(gomega.Equal(string(tc.wantWriterMode)), fmt.Sprintf("writer decision = %#v, want mode=%q lane=%q", decision, tc.wantWriterMode, tc.wantWriterLane))
			gomega.Expect(decision.Lane).To(gomega.Equal(tc.wantWriterLane), fmt.Sprintf("writer decision = %#v, want mode=%q lane=%q", decision, tc.wantWriterMode, tc.wantWriterLane))
			if tc.wantVerificationOwnr != "" {
				gomega.Expect(decision.VerificationOwner).To(gomega.Equal(tc.wantVerificationOwnr), fmt.Sprintf("writer verification owner = %q, want %q in %#v", decision.VerificationOwner, tc.wantVerificationOwnr, decision))
			}
		})
	}
})

var _ = ginkgo.It("build prompt execution plan preserves classifier selected plan execute family", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"plan_then_execute_planner","initial_lane":"implement","reason":"classifier_atom_dispatch","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.RouteClassifier.Enabled = true

	m := &Model{
		cfg:    cfg,
		client: llama.NewClient(server.URL, time.Second),
	}
	objective := "Can you send this product workflow implementation atom through the TUI plan-execute flow?"

	plan := newDetachedPromptExecutionSession(context.Background(), m).buildPromptExecutionPlan(context.Background(), objective)
	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(objective, plan)

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("classifier calls = %d, want 1", calls.Load()))

	gomega.Expect(plan.route.Family).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("route family = %q, want %s", plan.route.Family, agent.PlanThenExecutePlannerTaskFamily))

	gomega.Expect(plan.runtimeBrief.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("runtime brief task family = %q, want %s", plan.runtimeBrief.TaskFamily, agent.PlanThenExecutePlannerTaskFamily))

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)), fmt.Sprintf("classifier-selected planner should activate plan submission, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("plan-execute"), fmt.Sprintf("classifier-selected planner should activate plan submission, got %#v", decision))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.plan_execute_planner")).To(gomega.BeTrue(), fmt.Sprintf("plan submission reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("implement slash command execution plan uses implement metadata", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	objective := "Create a small fixture file. Required edit paths only: tmp/typed-implement.txt."

	execution := newDetachedPromptExecutionSession(context.Background(), m)
	plan := execution.implementCommandExecutionPlan(objective)
	decision := execution.writerRouteDecisionForPrompt(objective, plan)

	gomega.Expect(plan.mode.Name).To(gomega.Equal(runprofile.AgentModeBuild), fmt.Sprintf("mode = %q, want build", plan.mode.Name))

	gomega.Expect(plan.route.Family).To(gomega.Equal("implementation"), fmt.Sprintf("typed command should set implementation route, got %#v", plan.route))
	gomega.Expect(plan.route.Reason).To(gomega.Equal("typed_implement_command"), fmt.Sprintf("typed command should set implementation route, got %#v", plan.route))

	gomega.Expect(plan.runtimeBrief.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("brief task family = %q, want implementation", plan.runtimeBrief.TaskFamily))

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("typed command should report implement writer metadata, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("typed command should report implement writer metadata, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("typed command writer metadata missing implement route, got %#v", decision))
	gomega.Expect(decision.ReasonCodes).To(gomega.ContainElement("route.typed_implement_command"), fmt.Sprintf("typed command writer metadata missing implement route, got %#v", decision))
})

var _ = ginkgo.It("writer route decision for prompt reports review repair as plan submission", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode: modepolicy.BuildMode(),
		route: turnRoutePlan{
			Family:      agent.PlanThenExecuteReviewRepairTaskFamily,
			InitialLane: "review",
			Contract:    "review_findings",
		},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        agent.PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("review repair", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)), fmt.Sprintf("review-repair should report plan-submission authority under plan-execute, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("plan-execute"), fmt.Sprintf("review-repair should report plan-submission authority under plan-execute, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal(agent.PlanThenExecuteReviewRepairTaskFamily), fmt.Sprintf("task family = %q, want %s", decision.TaskFamily, agent.PlanThenExecuteReviewRepairTaskFamily))

	gomega.Expect(decision.VerificationOwner).To(gomega.Equal("planner_owned"), fmt.Sprintf("review-repair should preserve planner-owned verification, got %#v", decision))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.plan_execute_review_repair")).To(gomega.BeTrue(), fmt.Sprintf("review-repair route reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt does not use plan execute phrase as contract", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"pkg/widget.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Plan-execute the widget behavior. Required edit paths: pkg/widget.go.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("plan-execute wording alone should not select plan-submission contract, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("plan-execute wording alone should not select plan-submission contract, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))
})

var _ = ginkgo.It("writer route decision for prompt keeps simple unscoped implementation on guarded lane", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:         modepolicy.BuildMode(),
		route:        turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Update the small label text in the active file.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("simple unscoped implementation should stay on guarded implement lane, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("simple unscoped implementation should stay on guarded implement lane, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("direct implementation reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt keeps simple scoped source test implementation on guarded lane", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"pkg/widget.go", "pkg/widget_test.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("Fix the workflow label test expectation. Required edit paths: pkg/widget.go, pkg/widget_test.go.", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("simple scoped source+test implementation should stay on guarded implement lane, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("simple scoped source+test implementation should stay on guarded implement lane, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("direct implementation reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt keeps exact edit packet on guarded lane", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/surfaces/tui/app/writer_route.go"},
		},
	}
	prompt := strings.Join([]string{
		"Apply the exact edit.",
		"",
		"Required edit paths: internal/surfaces/tui/app/writer_route.go",
		"",
		"## Required Edits",
		"",
		"old_text:",
		"```text",
		"old route",
		"```",
		"",
		"new_text:",
		"```text",
		"new route",
		"```",
	}, "\n")

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(prompt, plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("exact edit packet should stay on guarded implement lane, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("exact edit packet should stay on guarded implement lane, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))
})

var _ = ginkgo.It("writer route decision for prompt does not use plan execute phrase for exact edit packet", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"pkg/widget.go", "pkg/widget_test.go"},
		},
	}
	prompt := strings.Join([]string{
		"Use the plan-execute lane to apply these exact edits.",
		"",
		"Required edit paths: pkg/widget.go, pkg/widget_test.go",
		"",
		"## Required Edits",
		"",
		"Path: pkg/widget.go",
		"old_text:",
		"```text",
		"old source",
		"```",
		"new_text:",
		"```text",
		"new source",
		"```",
		"",
		"Path: pkg/widget_test.go",
		"old_text:",
		"```text",
		"old test",
		"```",
		"new_text:",
		"```text",
		"new test",
		"```",
	}, "\n")

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt(prompt, plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("plan-execute prose mention should not route exact edit packet to plan-execute, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("plan-execute prose mention should not route exact edit packet to plan-execute, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal("implementation"), fmt.Sprintf("task family = %q, want implementation", decision.TaskFamily))
})

var _ = ginkgo.It("writer route decision for prompt reports plan execute planner family", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/surfaces/tui/app/app.go", "internal/runtime/agent/runner.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("planner phase", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModePlanSubmission)), fmt.Sprintf("plan-execute planner should report plan-submission route, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("plan-execute"), fmt.Sprintf("plan-execute planner should report plan-submission route, got %#v", decision))

	gomega.Expect(decision.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("task family = %q, want %s", decision.TaskFamily, agent.PlanThenExecutePlannerTaskFamily))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.plan_execute_planner")).To(gomega.BeTrue(), fmt.Sprintf("plan-execute planner reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("implementation dispatch fallback search ranks ui render path", func() {
	output := strings.Join([]string{
		`f11 internal/runtime/agent/runner.go:5174: return containsAny(lowerObjective, "banner")`,
		`f6 internal/surfaces/tui/app/approval.go:51:// invariant that the banner always matches a real request`,
		`f12 internal/surfaces/tui/app/model_state.go:1375: func (m *Model) errorBanner() string {`,
		`f10 internal/surfaces/tui/app/rendering.go:50: if banner := m.errorBanner(); banner != "" {`,
		`f10 internal/surfaces/tui/app/rendering.go:51: footerLines = append(footerLines, errorStyle.Render(banner))`,
		`f5 internal/frontier/locate.go:169: if ContainsAny(normalized, "top", "header", "banner") {`,
	}, "\n")

	paths := implementationDispatchRankSearchPaths("replace the model path in the top-right banner with the actual model alias", output, 3)
	gomega.Expect(len(paths)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected rendering.go first, got %#v", paths))
	gomega.Expect(paths[0]).To(gomega.Equal("internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("expected rendering.go first, got %#v", paths))
})

var _ = ginkgo.It("implementation dispatch fallback search keeps non go source paths", func() {
	output := strings.Join([]string{
		`f1 web/src/components/Banner.tsx:42: <HeaderBanner modelAlias={activeAlias} />`,
		`f2 docs/banner.md:4: banner model alias notes`,
	}, "\n")

	paths := implementationDispatchRankSearchPaths("replace the banner model path with the actual model alias", output, 3)
	gomega.Expect(len(paths)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected TSX source path to remain eligible, got %#v", paths))
	gomega.Expect(paths[0]).To(gomega.Equal("web/src/components/Banner.tsx"), fmt.Sprintf("expected TSX source path to remain eligible, got %#v", paths))
})

var _ = ginkgo.It("append discovered candidate scope stays inside brief body", func() {
	prompt := pathparse.WrapBriefBody("# Objective\n\nUpdate the banner.")
	got := recovery.AppendDiscoveredCandidateScopeAddendum(prompt, []string{"internal/surfaces/tui/app/rendering.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	gomega.Expect(scoped).To(gomega.ContainSubstring("allowed_edit_paths:"), fmt.Sprintf("discovered candidate scope was not inserted inside brief body:\n%s", got))
	gomega.Expect(scoped).To(gomega.ContainSubstring("internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("discovered candidate scope was not inserted inside brief body:\n%s", got))

	{
		allowed := plancontract.AllowedEditPathsFromPrompt(got)
		gomega.Expect(len(allowed)).To(gomega.Equal(1), fmt.Sprintf("discovered candidate scope is not parseable as allowed paths: %#v\n%s", allowed, got))
		gomega.Expect(allowed[0]).To(gomega.Equal("internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("discovered candidate scope is not parseable as allowed paths: %#v\n%s", allowed, got))
	}
})

var _ = ginkgo.It("implementation dispatch context pack scope uses candidate files", func() {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"### Candidate Files",
		"- `internal/workflows/productworkflow/migration_apply.go` (editable, matched task terms)",
		"- `docs/product-status.md` (read_only_evidence, related docs)",
		"- `internal/workflows/productworkflow/migration_apply_test.go` (test, inferred from Go candidate file)",
		"",
		"### Candidate Symbols",
		"- `ignored.go` (editable)",
	}, "\n")

	paths := implementationDispatchAllowedPathsFromContextPack(prompt, 8)

	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("context-pack scope = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch context pack test paths uses candidate tests", func() {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"### Candidate Tests",
		"- `go test ./internal/workflows/productworkflow` - internal/workflows/productworkflow/migration_apply_test.go",
		"- `go test ./internal/workflows/productworkflow` - internal/workflows/productworkflow/efforts_test.go",
		"",
		"### Context Gaps",
		"- ignored",
	}, "\n")

	paths := implementationDispatchTestPathsFromContextPack(prompt, 8)

	want := []string{
		"internal/workflows/productworkflow/migration_apply_test.go",
		"internal/workflows/productworkflow/efforts_test.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("context-pack test paths = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch context pack scope uses candidate files compact format", func() {
	// New compact projection format: candidate_files: header, plain paths with "path: role" format.
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Source: idiom `context_pack` automatic preload.",
		"",
		"- Summary: 3 files, 0 symbols, 1 test",
		"",
		"context_pack_summary:",
		"pack_id: abc123",
		"fresh: true",
		"packet_quality:",
		"- localization_confidence: high",
		"- source_owner_found: true",
		"",
		"candidate_files:",
		"- internal/workflows/productworkflow/migration_apply.go: editable, matched task terms",
		"- docs/product-status.md: read_only_evidence, related docs",
		"- internal/workflows/productworkflow/migration_apply_test.go: test, inferred from Go candidate file",
		"",
		"candidate_symbols:",
		"- ignored.go: editable",
	}, "\n")

	paths := implementationDispatchAllowedPathsFromContextPack(prompt, 8)

	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("context-pack scope (compact format) = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch context pack test paths uses candidate tests compact format", func() {
	// New compact projection format: tests: header, plain paths with "path: detail" format.
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Source: idiom `context_pack` automatic preload.",
		"",
		"- Summary: 2 tests",
		"",
		"context_pack_summary:",
		"fresh: true",
		"packet_quality:",
		"- localization_confidence: high",
		"- source_owner_found: true",
		"",
		"tests:",
		"- internal/workflows/productworkflow/migration_apply_test.go: go test ./internal/workflows/productworkflow",
		"- internal/workflows/productworkflow/efforts_test.go: go test ./internal/workflows/productworkflow",
		"",
		"context_gaps:",
		"- ignored",
	}, "\n")

	paths := implementationDispatchTestPathsFromContextPack(prompt, 8)

	want := []string{
		"internal/workflows/productworkflow/migration_apply_test.go",
		"internal/workflows/productworkflow/efforts_test.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("context-pack test paths (compact format) = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch preloaded context pack becomes initial allowed scope compact format", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	{
		err := os.MkdirAll(filepath.Dir(testPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir test dir: %v", err))
	}

	{
		err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write sibling test: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	// New compact projection format.
	contextPack := strings.Join([]string{
		"## Context Pack",
		"",
		"Source: idiom `context_pack` automatic preload.",
		"",
		"- Summary: 2 files, 1 test",
		"",
		"context_pack_summary:",
		"fresh: true",
		"packet_quality:",
		"- localization_confidence: high",
		"- source_owner_found: true",
		"",
		"candidate_files:",
		"- internal/workflows/productworkflow/migration_apply.go: editable, matched task terms",
		"",
		"tests:",
		"- internal/workflows/productworkflow/migration_apply_test.go: go test ./internal/workflows/productworkflow",
	}, "\n")

	paths := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must reject temp symlinks and add regression tests proving no partial mutation",
		contextPack,
	)
	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}

	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("preloaded context-pack scope (compact format) = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch preloaded context pack rejects irrelevant candidate scope compact format", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root

	model := New(cfg)

	defer model.Close()

	// New compact projection format with irrelevant paths.
	contextPack := strings.Join([]string{
		"## Context Pack",
		"",
		"Source: idiom `context_pack` automatic preload.",
		"",
		"- Summary: 3 files, 1 test",
		"",
		"context_pack_summary:",
		"fresh: true",
		"packet_quality:",
		"- localization_confidence: medium",
		"- source_owner_found: false",
		"",
		"candidate_files:",
		"- internal/surfaces/cli/product/product_workflow_test.go: editable, matched task terms",
		"- internal/capabilities/workspace/capabilities_test.go: test, matched task terms",
		"- internal/surfaces/tui/app/prompt_runtime_test.go: test, matched task terms",
		"",
		"tests:",
		"- internal/surfaces/cli/product/product_workflow_test.go: go test ./internal/surfaces/cli/product",
	}, "\n")

	paths := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must harden predictable temp-path writes against pre-existing symlinks and add regression tests",
		contextPack,
	)
	gomega.Expect(len(paths)).To(gomega.Equal(0), fmt.Sprintf("irrelevant preloaded context-pack scope (compact format) should be ignored, got %#v", paths))
})

var _ = ginkgo.It("implementation dispatch allowed paths from build context pack section integration", func() {
	t := ginkgo.GinkgoT()
	// Create a minimal Go workspace so BuildPromptContextPackSection can produce
	// a real compact-projection output, then verify the TUI parsers extract the
	// expected paths from it.
	root := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ctxpack\n\ngo 1.22\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
	}

	editPath := filepath.Join(root, "pkg", "target.go")
	{
		err := os.WriteFile(editPath, []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "noise.go"), []byte("package pkg\n\nfunc Noise() string { return \"noise\" }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
	}

	brief := "# Plan-execute atom\n\n## Task\n\nPlan a change to Target.\n\n## Files To Modify\n\n- pkg/target.go\n"

	var stderr bytes.Buffer

	section := planexecute.BuildPromptContextPackSectionWithRunner(config.Config{Workspace: config.Workspace{Root: root, CWD: root}}, planexecute.PromptContextPackOptions{
		Mode:              "plan",
		Title:             "Plan-execute atom",
		Brief:             brief,
		ProjectRoot:       root,
		RequiredEditPaths: []string{"pkg/target.go"},
	}, &stderr, planexecuteadapters.Dependencies().RunPromptContextTool)

	gomega.Expect(strings.TrimSpace(section)).NotTo(gomega.Equal(""), fmt.Sprintf("BuildPromptContextPackSection returned empty; stderr=%s", stderr.String()))

	// Verify the section uses the new compact format markers.
	gomega.Expect(strings.Contains(section, "candidate_files:")).To(gomega.BeTrue(), fmt.Sprintf("expected candidate_files: header in section, got:\n%s\nstderr=%s", section, stderr.String()))

	// Parse via the same path used in production: AllowedPathsFromContextPack + TestPaths.
	paths := implementationDispatchAllowedPathsFromContextPack(section, 8)
	testPaths := implementationDispatchTestPathsFromContextPack(section, 8)
	allPaths := append(paths, testPaths...)

	gomega.Expect(len(allPaths)).NotTo(gomega.Equal(0), fmt.Sprintf("parsers returned zero paths from real BuildPromptContextPackSection output; section:\n%s\nstderr=%s", section, stderr.String()))

	// At minimum, pkg/target.go should be found since it was a required edit path.
	foundTarget := false

	for _, p := range allPaths {
		if strings.Contains(p, "pkg/target.go") {
			foundTarget = true
			break
		}
	}

	gomega.Expect(foundTarget).To(gomega.BeTrue(), fmt.Sprintf("expected pkg/target.go in parsed paths, got %v; section:\n%s\nstderr=%s", allPaths, section, stderr.String()))
})

var _ = ginkgo.It("implementation dispatch preloaded context pack becomes initial allowed scope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	{
		err := os.MkdirAll(filepath.Dir(testPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir test dir: %v", err))
	}

	{
		err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write sibling test: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	contextPack := strings.Join([]string{
		"## Context Pack",
		"",
		"### Candidate Files",
		"- `internal/workflows/productworkflow/migration_apply.go` (editable, matched task terms)",
		"",
		"### Candidate Tests",
		"- `go test ./internal/workflows/productworkflow` - internal/workflows/productworkflow/migration_apply_test.go",
	}, "\n")

	paths := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must reject temp symlinks and add regression tests proving no partial mutation",
		contextPack,
	)
	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}

	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("preloaded context-pack scope = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch preloaded context pack rejects irrelevant candidate scope", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root

	model := New(cfg)

	defer model.Close()

	contextPack := strings.Join([]string{
		"## Context Pack",
		"",
		"### Candidate Files",
		"- `internal/surfaces/cli/product/product_workflow_test.go` (editable, matched task terms)",
		"- `internal/capabilities/workspace/capabilities_test.go` (test, matched task terms)",
		"- `internal/surfaces/tui/app/prompt_runtime_test.go` (test, matched task terms)",
		"",
		"### Candidate Tests",
		"- `go test ./internal/surfaces/cli/product` - internal/surfaces/cli/product/product_workflow_test.go",
	}, "\n")

	paths := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must harden predictable temp-path writes against pre-existing symlinks and add regression tests",
		contextPack,
	)
	gomega.Expect(len(paths)).To(gomega.Equal(0), fmt.Sprintf("irrelevant preloaded context-pack scope should be ignored, got %#v", paths))
})

var _ = ginkgo.It("implementation dispatch fallback search pattern skips process words", func() {
	pattern := implementationDispatchFallbackSearchPattern("We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: status migration apply must harden predictable temp-path writes against pre-existing symlinks.")
	want := "status|migration|apply|harden"

	gomega.Expect(pattern).To(gomega.Equal(want), fmt.Sprintf("fallback pattern = %q, want %q", pattern, want))

	patterns := implementationDispatchFallbackFilePatterns("status migration apply must harden temp path writes")
	gomega.Expect(len(patterns)).To(gomega.BeNumerically(">=", 2), fmt.Sprintf("fallback file patterns = %#v", patterns))
	gomega.Expect(patterns[0]).To(gomega.Equal("status_migration_apply"), fmt.Sprintf("fallback file patterns = %#v", patterns))
	gomega.Expect(patterns[1]).To(gomega.Equal("migration_apply_harden"), fmt.Sprintf("fallback file patterns = %#v", patterns))

	paths := implementationDispatchPathsFromSearchFilesOutput("f1 internal/workflows/productworkflow/migration_apply.go\nf2 internal/workflows/productworkflow/migration_apply_test.go\n", 4)
	wantPaths := []string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"}

	gomega.Expect(slices.Equal(paths, wantPaths)).To(gomega.BeTrue(), fmt.Sprintf("search_files paths = %#v, want %#v", paths, wantPaths))
})

var _ = ginkgo.It("implementation dispatch missing test retry preserves candidate scope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	{
		err := os.MkdirAll(filepath.Dir(testPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir test dir: %v", err))
	}

	{
		err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write sibling test: %v", err))
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	{
		err := os.MkdirAll(stateDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir state dir: %v", err))
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "missing-test-retry",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/workflows/productworkflow/migration_apply.go"},
	}

	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal state: %v", err))

	{
		err := os.WriteFile(filepath.Join(stateDir, "missing-test-retry.json"), body, 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write state: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	paths, err := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsForMissingRequestedTestEdit(implementationDispatchPlanExecuteFailure{
		Prompt: "Add regression tests proving pre-existing workflow/index/events temp symlinks are rejected without partial durable mutation.",
		AllowedPaths: []string{
			"internal/surfaces/cli/product/product_workflow_test.go",
			"internal/capabilities/workspace/capabilities_test.go",
			"internal/surfaces/tui/app/lifecycle.go",
		},
		Options: planexecute.Options{
			Prompt: strings.Join([]string{
				"## Candidate Edit Scope From Target References",
				"- internal/surfaces/cli/product/product_workflow_test.go",
				"- internal/capabilities/workspace/capabilities_test.go",
				"- internal/surfaces/tui/app/lifecycle.go",
				"",
				"### Candidate Tests",
				"- `go test ./internal/workflows/productworkflow` - internal/workflows/productworkflow/migration_apply_test.go",
			}, "\n"),
		},
		Result: planexecute.RunResult{
			SessionID:   "missing-test-retry",
			FailureKind: string(plancontract.PlanExecuteFailureMissingRequestedTestEdit),
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/capabilities/workspace/capabilities_test.go",
		"internal/surfaces/tui/app/lifecycle.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("missing-test retry scope = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch missing implementation retry preserves candidate scope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go")
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	{
		err := os.MkdirAll(filepath.Dir(testPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir test dir: %v", err))
	}

	{
		err := os.WriteFile(sourcePath, []byte("package productworkflow\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write sibling source: %v", err))
	}

	{
		err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write sibling test: %v", err))
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	{
		err := os.MkdirAll(stateDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir state dir: %v", err))
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "missing-implementation-retry",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/workflows/productworkflow/migration_apply_test.go"},
	}

	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal state: %v", err))

	{
		err := os.WriteFile(filepath.Join(stateDir, "missing-implementation-retry.json"), body, 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write state: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	paths, err := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(implementationDispatchPlanExecuteFailure{
		Prompt: "Implement migration apply behavior. Start by adding failing tests, then implement.",
		AllowedPaths: []string{
			"internal/surfaces/cli/product/product_workflow_test.go",
			"internal/surfaces/tui/app/lifecycle.go",
		},
		Result: planexecute.RunResult{
			SessionID:   "missing-implementation-retry",
			FailureKind: string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit),
		},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	want := []string{
		"internal/workflows/productworkflow/migration_apply_test.go",
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/tui/app/lifecycle.go",
	}
	gomega.Expect(slices.Equal(paths, want)).To(gomega.BeTrue(), fmt.Sprintf("missing-implementation retry scope = %#v, want %#v", paths, want))
})

var _ = ginkgo.It("implementation dispatch missing implementation retry keeps submitted sibling and context candidates allowed", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "internal", "surfaces", "cli", "product", "product_workflow.go")
	testPath := filepath.Join(root, "internal", "surfaces", "cli", "product", "product_workflow_test.go")
	statusPath := filepath.Join(root, "internal", "workflows", "productworkflow", "status.go")

	for _, path := range []string{sourcePath, testPath, statusPath} {
		{
			err := os.MkdirAll(filepath.Dir(path), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", path, err))
		}

		{
			err := os.WriteFile(path, []byte("package productworkflow\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
		}
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	{
		err := os.MkdirAll(stateDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir state dir: %v", err))
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "product-workflow-missing-implementation",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/surfaces/cli/product/product_workflow_test.go"},
	}

	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal state: %v", err))

	{
		err := os.WriteFile(filepath.Join(stateDir, "product-workflow-missing-implementation.json"), body, 0o600)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write state: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	failure := implementationDispatchPlanExecuteFailure{
		Prompt: "Implement product workflow status behavior after adding tests.",
		Options: planexecute.Options{
			Prompt: strings.Join([]string{
				"## Context Pack",
				"",
				"### Candidate Files",
				"",
				"- `internal/workflows/productworkflow/status.go` (editable, score=4.20): matched task terms",
			}, "\n"),
		},
		Result: planexecute.RunResult{
			SessionID:   "product-workflow-missing-implementation",
			FailureKind: string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit),
		},
	}
	allowedPaths, err := newDetachedPromptExecutionSession(context.Background(), model).implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	for _, want := range []string{
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/cli/product/product_workflow.go",
		"internal/workflows/productworkflow/status.go",
	} {
		gomega.Expect(slices.Contains(allowedPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("missing-implementation candidate scope missing %q, got %#v", want, allowedPaths))
	}

	retryOptions := implementationDispatchRetryOptionsWithCandidateScope(failure.Options, allowedPaths)

	retryOptions = implementationDispatchRetryOptionsWithEditSetObligation(retryOptions, plancontract.EditSetObligationKindImplementationEdit)

	gomega.Expect(len(retryOptions.RequiredEditPaths)).To(gomega.Equal(0), fmt.Sprintf("retry required paths must remain empty for candidate-only recovery, got %#v", []string(retryOptions.RequiredEditPaths)))

	for _, want := range []string{
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/cli/product/product_workflow.go",
		"internal/workflows/productworkflow/status.go",
	} {
		gomega.Expect(slices.Contains([]string(retryOptions.AllowedEditPaths), want)).To(gomega.BeTrue(), fmt.Sprintf("retry allowed paths missing %q, got %#v", want, []string(retryOptions.AllowedEditPaths)))
	}

	gomega.Expect(retryOptions.EditSetObligations.Has(plancontract.EditSetObligationKindImplementationEdit)).To(gomega.BeTrue(), fmt.Sprintf("retry options missing implementation edit obligation: %#v", retryOptions.EditSetObligations))
})

var _ = ginkgo.It("implementation dispatch missing edit retry keeps candidate scope allowed", func() {
	opts := implementationDispatchRetryOptionsWithCandidateScope(planexecute.Options{
		RequiredEditPaths: []string{"pkg/explicit.go"},
		AllowedEditPaths:  []string{"pkg/existing_candidate.go"},
	}, []string{
		"pkg/feature_test.go",
		"pkg/feature.go",
	})

	wantRequired := []string{"pkg/explicit.go"}
	gomega.Expect(slices.Equal([]string(opts.RequiredEditPaths), wantRequired)).To(gomega.BeTrue(), fmt.Sprintf("retry required paths = %#v, want %#v", []string(opts.RequiredEditPaths), wantRequired))

	wantAllowed := []string{"pkg/existing_candidate.go", "pkg/feature_test.go", "pkg/feature.go"}
	gomega.Expect(slices.Equal([]string(opts.AllowedEditPaths), wantAllowed)).To(gomega.BeTrue(), fmt.Sprintf("retry allowed paths = %#v, want %#v", []string(opts.AllowedEditPaths), wantAllowed))

	gomega.Expect(slices.Equal(opts.ScopeContract.RequiredCoveragePaths, wantRequired)).To(gomega.BeTrue(), fmt.Sprintf("retry scope required coverage = %#v, want %#v", opts.ScopeContract.RequiredCoveragePaths, wantRequired))

	gomega.Expect(slices.Equal(opts.ScopeContract.AllowedCandidatePaths, wantAllowed)).To(gomega.BeTrue(), fmt.Sprintf("retry scope allowed candidates = %#v, want %#v", opts.ScopeContract.AllowedCandidatePaths, wantAllowed))
})

var _ = ginkgo.It("implementation dispatch recovery loop guard ignores prompt obligations", func() {
	promptOpts := planexecute.Options{
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "prompt"),
	}
	gomega.Expect(implementationDispatchOptionsAlreadyCarriedEditSetObligation(promptOpts, plancontract.EditSetObligationKindTestEdit)).To(gomega.BeFalse(), fmt.Sprintf("initial prompt-derived obligations should still allow one recovery retry"))

	retryOpts := implementationDispatchRetryOptionsWithEditSetObligation(promptOpts, plancontract.EditSetObligationKindTestEdit)
	gomega.Expect(implementationDispatchOptionsAlreadyCarriedEditSetObligation(retryOpts, plancontract.EditSetObligationKindTestEdit)).To(gomega.BeTrue(), fmt.Sprintf("recovery-added obligations should trip the loop guard"))
})

var _ = ginkgo.It("missing requested implementation addendum marks paths as candidate scope", func() {
	got := recovery.AppendMissingRequestedImplementationEditRecoveryAddendum("Implement feature.", []string{
		"pkg/feature_test.go",
		"pkg/feature.go",
	})
	for _, want := range []string{
		"candidate scope, not mandatory coverage",
		"typed obligation is at least one real non-test implementation edit",
		"allowed_edit_paths:",
		"pkg/feature_test.go",
		"pkg/feature.go",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("implementation recovery addendum missing %q:\n%s", want, got))
	}

	gomega.Expect(strings.Contains(got, "required_edit_paths:")).To(gomega.BeFalse(), fmt.Sprintf("implementation recovery addendum must not promote candidates to required paths:\n%s", got))
})

var _ = ginkgo.It("missing requested test addendum marks paths as candidate scope", func() {
	got := recovery.AppendMissingRequestedTestEditRecoveryAddendum("Implement feature and add tests.", []string{
		"pkg/feature.go",
		"pkg/feature_test.go",
	})
	for _, want := range []string{
		"candidate scope, not mandatory coverage",
		"typed obligation is at least one real test edit",
		"allowed_edit_paths:",
		"pkg/feature.go",
		"pkg/feature_test.go",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("test recovery addendum missing %q:\n%s", want, got))
	}

	gomega.Expect(strings.Contains(got, "required_edit_paths:")).To(gomega.BeFalse(), fmt.Sprintf("test recovery addendum must not promote candidates to required paths:\n%s", got))
})

var _ = ginkgo.It("append missing requested test edit recovery addendum stays inside brief body", func() {
	prompt := pathparse.WrapBriefBody("# Objective\n\nUpdate the banner and add regression tests.")
	got := recovery.AppendMissingRequestedTestEditRecoveryAddendum(prompt, []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering_test.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	gomega.Expect(scoped).To(gomega.ContainSubstring("Missing Requested Test Edit Recovery"), fmt.Sprintf("missing-test recovery scope was not inserted inside brief body:\n%s", got))
	gomega.Expect(scoped).To(gomega.ContainSubstring("internal/surfaces/tui/app/rendering_test.go"), fmt.Sprintf("missing-test recovery scope was not inserted inside brief body:\n%s", got))

	{
		allowed := plancontract.AllowedEditPathsFromPrompt(got)
		gomega.Expect(slices.Contains(allowed, "internal/surfaces/tui/app/rendering_test.go")).To(gomega.BeTrue(), fmt.Sprintf("missing-test recovery scope is not parseable as allowed paths: %#v\n%s", allowed, got))
	}
})

var _ = ginkgo.It("writer route decision for prompt reports direct implementation", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = ""
	cfg.LlamaServer.Model = "default-model"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/surfaces/tui/app/writer_route.go"},
		},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("modify internal/surfaces/tui/app/writer_route.go", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("unexpected direct implementation writer route decision: %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected direct implementation writer route decision: %#v", decision))

	gomega.Expect(decision.VerificationOwner).To(gomega.Equal("executor"), fmt.Sprintf("direct implementation verification owner = %q, want executor", decision.VerificationOwner))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("direct implementation reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for classifier implementation uses guarded writer", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = ""

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation", RouteSource: routeSourceClassifier},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("update target.txt", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeAgenticGuarded)), fmt.Sprintf("classifier implementation should use guarded writer, got %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("implement"), fmt.Sprintf("classifier implementation should use guarded writer, got %#v", decision))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.local_model_default")).To(gomega.BeTrue(), fmt.Sprintf("guarded writer reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("writer route decision for prompt reports read only route", func() {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.PlanMode(),
		route: turnRoutePlan{Family: "architecture"},
	}

	decision := newDetachedPromptExecutionSession(context.Background(), m).writerRouteDecisionForPrompt("explain the route decision design", plan)

	gomega.Expect(decision.WriterMode).To(gomega.Equal(string(writerroute.ModeReadOnly)), fmt.Sprintf("unexpected read-only writer route decision: %#v", decision))
	gomega.Expect(decision.Lane).To(gomega.Equal("main"), fmt.Sprintf("unexpected read-only writer route decision: %#v", decision))

	gomega.Expect(decision.VerificationOwner).To(gomega.Equal(""), fmt.Sprintf("read-only route should not claim verification ownership: %#v", decision))

	gomega.Expect(slices.Contains(decision.ReasonCodes, "route.read_only_question")).To(gomega.BeTrue(), fmt.Sprintf("read-only route reason missing: %#v", decision.ReasonCodes))
})

var _ = ginkgo.It("run prompt once unscoped implementation uses direct implement when dispatch disabled", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		fmt.Fprintf(ginkgo.GinkgoWriter, "request path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))

		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			gomega.Expect(strings.Contains(requestText, "Plan-then-execute (planner phase)")).To(gomega.BeFalse(), fmt.Sprintf("unscoped TUI implementation should not enter plan-execute planner:\n%s", requestText))

			switch chatRequests.Add(1) {
			case 1:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{
						"message": map[string]string{"content": `<tool_call>
{"tool":"read_file","path":"target.txt","named_gap":"current target content before replacing it","why_not_answering":"need source-body evidence before writing existing file"}`},
					}},
				})
			case 2:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{
						"message": map[string]string{"content": `<tool_call>
{"tool":"write_file","path":"target.txt","content":"new\n","description":"update target through implement route"}`},
					}},
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{
						"message": map[string]string{"content": "Updated target.txt."},
					}},
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = ""
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runImplementCommandOnce(context.Background(), "update target.txt")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementCommandOnce: %v\nanswer: %s", err, answer))

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("new\n"), fmt.Sprintf("target content = %q, want new", string(got)))
})

var _ = ginkgo.It("direct implementation context preload applies to natural unscoped edits", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	sourcePath := filepath.Join(root, "internal", "surfaces", "tui", "app", "rendering.go")
	{
		err := os.MkdirAll(filepath.Dir(sourcePath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir source dir: %v", err))
	}

	{
		err := os.WriteFile(sourcePath, []byte("package app\n\nfunc renderBanner() string { return \"top-right model path banner\" }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()

	plan := promptExecutionPlan{
		mode:         modepolicy.BuildMode(),
		route:        turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"},
	}

	plan = newDetachedPromptExecutionSession(context.Background(), model).recoverDirectImplementationContext("change the model string in the top-right banner from the path to the alias", plan)

	gomega.Expect(slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:direct_implementation_preload")).To(gomega.BeTrue(), fmt.Sprintf("expected direct implementation context preload marker, got %#v", plan.runtimeBrief.TargetPacket))

	packet := strings.Join(plan.runtimeBrief.TargetPacket, "\n")
	gomega.Expect(packet).To(gomega.Or(
		gomega.ContainSubstring("Context Pack"),
		gomega.ContainSubstring("Context Discovery Fallback"),
	), fmt.Sprintf("expected context preload or fallback packet, got:\n%s", packet))

	gomega.Expect(slices.Contains(plan.runtimeBrief.SearchPolicy, "direct_implement_context_preloaded_before_model_turn")).To(gomega.BeTrue(), fmt.Sprintf("expected direct context search policy, got %#v", plan.runtimeBrief.SearchPolicy))
})

var _ = ginkgo.It("direct implementation context preload skips scoped prompts", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()

	plan := promptExecutionPlan{
		mode:         modepolicy.BuildMode(),
		route:        turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"},
	}
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	gomega.Expect(execution.shouldPreloadDirectImplementationContext("update target.txt", plan)).To(gomega.BeFalse(), fmt.Sprint("prompt with an explicit workspace path should not need unscoped direct context preload"))

	plan.runtimeBrief.RequiredEditPaths = []string{"pkg/feature.go"}
	gomega.Expect(execution.shouldPreloadDirectImplementationContext("fix the feature behavior", plan)).To(gomega.BeFalse(), fmt.Sprint("required edit paths already provide direct implementation scope"))
})

var _ = ginkgo.It("run prompt once semantic required paths uses direct implement when dispatch configured", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}
	{
		err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/turnplanner\n\ngo 1.22\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write go.mod: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Enabled() bool { return false }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write feature: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst Covered = false\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write feature test: %v", err))
	}

	var plannerChatRequests atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			plannerChatRequests.Add(1)
			ginkgo.Fail("semantic source/test implementation should not enter exact plan-execute planner", 1)
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChatRequests atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "executor-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			gomega.Expect(strings.Contains(requestText, `"model":"executor-model"`)).To(gomega.BeTrue(), fmt.Sprintf("direct implementation should send executor model id, got request:\n%s", requestText))

			gomega.Expect(strings.Contains(requestText, "Plan-then-execute (planner phase)")).To(gomega.BeFalse(), fmt.Sprintf("semantic direct implementation should not include plan-execute planner prompt:\n%s", requestText))

			var reply string

			switch executorChatRequests.Add(1) {
			case 1:
				reply = `<tool_call>
{"tool":"read_file","path":"pkg/feature.go","start_line":1,"end_line":3,"named_gap":"current feature implementation before editing"}`
			case 2:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature.go","old_text":"return false","new_text":"return true","description":"enable feature behavior"}`
			case 3:
				reply = `<tool_call>
{"tool":"read_file","path":"pkg/feature_test.go","start_line":1,"end_line":3,"named_gap":"current regression coverage after the source mutation"}`
			case 4:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature_test.go","old_text":"Covered = false","new_text":"Covered = true","description":"add regression coverage marker"}`
			case 5:
				reply = `<tool_call>
{"tool":"source_sanity","path":"pkg/feature.go"}`
			case 6:
				reply = `<tool_call>
{"tool":"source_sanity","path":"pkg/feature_test.go"}`
			case 7:
				reply = "Updated pkg/feature.go and pkg/feature_test.go. Source sanity passed for both changed paths."
			default:
				ginkgo.Fail(fmt.Sprintf("unexpected executor request %d after the model-authored final answer:\n%s", executorChatRequests.Load(), requestText), 1)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer executorServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"planner-model","model":"planner-model","base_url":%q,"roles":["planner","classifier"]},
		{"alias":"executor-model","model":"executor-model","base_url":%q,"roles":["executor"]}
	]}}}`, plannerServer.URL, executorServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = plannerServer.URL
	cfg.LlamaServer.Model = "planner-model"
	cfg.RouteClassifier.Enabled = false
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runImplementCommandOnce(context.Background(), "Implement a semantic TDD feature fix and add regression tests. Required edit paths: pkg/feature.go, pkg/feature_test.go.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementCommandOnce: %v\nanswer: %s", err, answer))

	gomega.Expect(plannerChatRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("semantic implementation should not use exact plan-execute planner, got %d request(s)", plannerChatRequests.Load()))

	gomega.Expect(executorChatRequests.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected direct implementation to use executor chat"))
	gomega.Expect(executorChatRequests.Load()).To(gomega.BeNumerically("==", 7), fmt.Sprintf("direct implementation requests = %d, want read/read/write/write/verify/verify/final", executorChatRequests.Load()))

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read source: %v", err))

	gomega.Expect(strings.Contains(string(gotSource), "return true")).To(gomega.BeTrue(), fmt.Sprintf("source was not updated after %d executor request(s), answer=%q:\n%s", executorChatRequests.Load(), answer, string(gotSource)))

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read test: %v", err))

	gomega.Expect(strings.Contains(string(gotTest), "Covered = true")).To(gomega.BeTrue(), fmt.Sprintf("test was not updated after %d executor request(s), answer=%q:\n%s", executorChatRequests.Load(), answer, string(gotTest)))
})

var _ = ginkgo.It("run prompt once natural language tdd feature uses classifier implement lane", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	for _, dir := range []string{".idiom", "docs", "pkg"} {
		{
			err := os.MkdirAll(filepath.Join(root, dir), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", dir, err))
		}
	}

	{
		err := os.WriteFile(filepath.Join(root, "docs", "tdd-feature.md"), []byte(strings.Join([]string{
			"# TDD Feature Atom",
			"",
			"Implement the feature by changing pkg/feature.go and pkg/feature_test.go.",
			"Acceptance: feature returns true and regression coverage marker is true.",
			"",
		}, "\n")), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write tdd feature atom: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Enabled() bool { return false }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write feature: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst Covered = false\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write feature test: %v", err))
	}

	var (
		classifierRequests        atomic.Int64
		unexpectedPlannerRequests atomic.Int64
	)

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read planner request: %v", err))

			requestText := string(body)
			if strings.Contains(requestText, "Current user prompt:") && strings.Contains(requestText, "Return JSON now.") {
				classifierRequests.Add(1)

				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{
						"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
					}},
				})

				return
			}

			unexpectedPlannerRequests.Add(1)
			ginkgo.Fail(fmt.Sprintf("natural-language TDD request should not enter planner/plan-execute path:\n%s", requestText), 1)
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChatRequests atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "executor-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read executor request: %v", err))

			requestText := string(body)
			gomega.Expect(strings.Contains(requestText, "Plan-then-execute (planner phase)")).To(gomega.BeFalse(), fmt.Sprintf("natural-language implementation should not enter plan-execute planner:\n%s", requestText))

			reply := "Updated pkg/feature.go and pkg/feature_test.go."
			switch executorChatRequests.Add(1) {
			case 1:
				reply = `<tool_call>
{"tool":"read_file","path":"docs/tdd-feature.md","named_gap":"feature atom requirements before editing","why_not_answering":"need source-body evidence from referenced atom"}`
			case 2:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature.go","old_text":"return false","new_text":"return true","description":"enable feature behavior"}`
			case 3:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature_test.go","old_text":"Covered = false","new_text":"Covered = true","description":"add regression coverage marker"}`
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer executorServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"planner-model","model":"planner-model","base_url":%q,"roles":["planner","classifier"]},
		{"alias":"executor-model","model":"executor-model","base_url":%q,"roles":["executor"]}
	]}}}`, plannerServer.URL, executorServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = plannerServer.URL
	cfg.LlamaServer.Model = "planner-model"
	cfg.RouteClassifier.Enabled = true
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	var eventBuf bytes.Buffer

	model.SetJSONEventStream(&eventBuf, "natural-tdd-test")

	answer, err := model.RunPromptOnce(context.Background(), "Can you implement this feature: docs/tdd-feature.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	gomega.Expect(classifierRequests.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one classifier request, got %d", classifierRequests.Load()))

	gomega.Expect(unexpectedPlannerRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected planner requests: %d", unexpectedPlannerRequests.Load()))

	gomega.Expect(executorChatRequests.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected executor chat for classifier-routed implementation"))

	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("Do you want me to switch")), fmt.Sprintf("answer should not ask for implementation confirmation, got %q", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("/implement")), fmt.Sprintf("answer should not ask for implementation confirmation, got %q", answer))

	gomega.Expect(strings.Contains(eventBuf.String(), `"writer_mode":"agentic_guarded"`)).To(gomega.BeTrue(), fmt.Sprintf("natural-language implementation should reach guarded writer route, answer=%q events=%s", answer, eventBuf.String()))
})

var _ = ginkgo.It("run prompt once semantic required paths falls back to active model when executor unavailable", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}
	initializeTurnPlannerGitRepository(t, root)

	var plannerChatRequests atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read planner request: %v", err))

			gomega.Expect(strings.Contains(string(body), `"model":"planner-model"`)).To(gomega.BeTrue(), fmt.Sprintf("active-model fallback should send planner model id, got request:\n%s", string(body)))

			var reply string

			switch plannerChatRequests.Add(1) {
			case 1:
				reply = `<tool_call>
{"tool":"read_file","path":"target.txt","start_line":1,"end_line":1,"named_gap":"current target content before replacing it"}`
			case 2:
				reply = `<tool_call>
{"tool":"history_range","path":"target.txt","start_line":1,"end_line":1,"named_gap":"history rationale for the established target content"}`
			case 3:
				reply = `<tool_call>
{"tool":"write_file","path":"target.txt","content":"new\n","description":"update target through active-model fallback"}`
			case 4:
				reply = `<tool_call>
{"tool":"read_file","path":"target.txt","start_line":1,"end_line":1,"named_gap":"fresh post-write target content for the final report"}`
			case 5:
				reply = `<tool_call>
{"tool":"shell","command":"git diff --check -- target.txt","workdir":"."}`
			case 6:
				reply = "Updated target.txt. Verification: git diff --check -- target.txt passed."
			default:
				ginkgo.Fail(fmt.Sprintf("unexpected active-model request %d after the model-authored final answer:\n%s", plannerChatRequests.Load(), string(body)), 1)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChatRequests atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			http.Error(w, "executor not ready", http.StatusServiceUnavailable)
		case "/v1/chat/completions":
			executorChatRequests.Add(1)
			http.Error(w, "executor should not receive chat", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	defer executorServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"planner-model","model":"planner-model","base_url":%q,"roles":["planner","classifier"]},
		{"alias":"executor-model","model":"executor-model","base_url":%q,"roles":["executor"]}
	]}}}`, plannerServer.URL, executorServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = plannerServer.URL
	cfg.LlamaServer.Model = "planner-model"
	cfg.RouteClassifier.Enabled = false
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = true
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runImplementCommandOnce(context.Background(), "Implement a semantic fix. Required edit paths: target.txt.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementCommandOnce: %v\nanswer: %s", err, answer))

	gomega.Expect(plannerChatRequests.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected active-model fallback to use planner chat"))
	gomega.Expect(plannerChatRequests.Load()).To(gomega.BeNumerically("==", 6), fmt.Sprintf("active-model requests = %d, want read/history/write/fresh-read/verify/final", plannerChatRequests.Load()))

	gomega.Expect(executorChatRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("unreachable executor should not receive chat, got %d request(s)", executorChatRequests.Load()))

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("new\n"), fmt.Sprintf("target content = %q, want new", string(got)))
})

var _ = ginkgo.It("plan execute planner blocked no scope surfaces without retry", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	answer, handled, err := newDetachedPromptExecutionSession(context.Background(), model).handlePlanExecutePlannerBlocked(context.Background(), implementationDispatchPlanExecuteFailure{
		Prompt: "remove the busy mode trace ctx footer from the TUI input area",
		Plan: promptExecutionPlan{
			mode:         modepolicy.BuildMode(),
			route:        turnRoutePlan{Family: "implementation"},
			runtimeBrief: workerruntime.WorkerRuntimeBrief{},
		},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "completion gate rejected final answer before submit_plan",
		},
	})
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected handled no-scope blocker, handled=%t err=%v answer=%q", handled, err, answer))
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprintf("expected handled no-scope blocker, handled=%t err=%v answer=%q", handled, err, answer))

	for _, want := range []string{
		"Plan-execute dispatch blocked before retry",
		"concrete edit scope",
		"completion gate rejected final answer",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("no-scope blocker missing %q:\n%s", want, answer))
	}
})

var _ = ginkgo.It("plan execute planner blocked message hides internal completion gate prompt", func() {
	answer := humanPlanExecutePlannerBlockedMessage(implementationDispatchPlanExecuteFailure{
		AllowedPaths: []string{"internal/surfaces/tui/app/rendering.go"},
		Result: planexecute.RunResult{
			FailureReason: "<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			PlannerAnswer: "Missing completion evidence: [gate retry 3/3] plan-execute planner phase requires a successful submit_plan tool result before final answer.",
		},
	}, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   "automatic discovered-scope retry did not produce a plan",
	})

	for _, want := range []string{
		"Plan-execute could not produce a plan artifact",
		"automatic discovered-scope retry did not produce a plan",
		"planner stopped before emitting a `submit_plan` tool call for the discovered candidate scope",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("planner blocked message missing %q:\n%s", want, answer))
	}

	for _, forbidden := range []string{
		"Completion gate rejected",
		"Missing completion evidence",
		"gate retry",
		"final answer repeatedly",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("planner blocked message leaked internal completion-gate detail %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("plan execute planner blocked message compacts canonical planner question", func() {
	plannerAnswer := strings.Join([]string{
		"<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.",
		"",
		"Changed paths: none",
		"",
		"Required edit paths not changed:",
		"- internal/surfaces/cli/product/product_workflow.go",
		"- internal/workflows/productworkflow/coverage_gate.go",
		"",
		"Verification: not run or not detected.",
		"",
		"Blocker:",
		"- Required edit path source read required: read source for internal/workflows/productworkflow/coverage_gate.go.",
	}, "\n")
	answer := humanPlanExecutePlannerBlockedMessage(implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		Result: planexecute.RunResult{
			FailureReason: "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.",
			PlannerAnswer: plannerAnswer,
		},
	}, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   "automatic retry did not produce a plan",
	})

	gomega.Expect(strings.Contains(answer, "Planner detail:")).To(gomega.BeTrue(), fmt.Sprintf("expected compact planner detail section:\n%s", answer))

	gomega.Expect(strings.Contains(answer, "Planner answer:")).To(gomega.BeFalse(), fmt.Sprintf("canonical blocker should not repeat the full planner answer:\n%s", answer))

	gomega.Expect(strings.Count(answer, "Required edit paths not changed:")).To(gomega.Equal(0), fmt.Sprintf("full planner changed-path list should be omitted because required paths are already listed:\n%s", answer))

	gomega.Expect(strings.Contains(answer, "read source for internal/workflows/productworkflow/coverage_gate.go")).To(gomega.BeTrue(), fmt.Sprintf("compact detail should preserve the actionable blocker:\n%s", answer))
})

var _ = ginkgo.It("plan execute planner blocked message names executor rejected submitted plan", func() {
	answer := humanPlanExecutePlannerBlockedMessage(implementationDispatchPlanExecuteFailure{
		Result: planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailureExecutorFailed),
			FailureReason: "executor phase aborted on edit 0 (pkg/feature_test.go): preflight syntax validation failed",
		},
	}, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   "automatic missing-test retry submitted a plan, but the executor rejected an edit",
	})

	for _, want := range []string{
		"automatic missing-test retry submitted a plan, but the executor rejected an edit",
		"executor phase aborted on edit 0",
		"pkg/feature_test.go",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("executor failure message missing %q:\n%s", want, answer))
	}
})

var _ = ginkgo.It("plan execute repeated completion gate surfaces deterministic message", func() {
	failure := implementationDispatchPlanExecuteFailure{
		Result: planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			PlannerAnswer: "Missing completion evidence: gate retry 3/3 requires a successful submit_plan tool result",
		},
	}
	answer := humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   "planner stopped before emitting `submit_plan`, `Decision: source-satisfied`, or a concrete blocker after repeated completion-gate rejection",
		Message:  "The system rejected the response 3 times. Manually invoke the submit_plan tool.",
	})
	for _, want := range []string{
		"Plan-execute could not produce a plan artifact",
		"planner stopped before emitting",
		"planner stopped before emitting a `submit_plan` tool call",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("deterministic repeated-gate message missing %q:\n%s", want, answer))
	}

	for _, forbidden := range []string{"Manually invoke", "system rejected", "submit_plan tool"} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("deterministic repeated-gate message leaked %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("plan execute planner blocked can retry on active model", func() {
	failure := implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go"},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: completion_gate_required_paths_remaining_repeated",
			PlannerAnswer: "Missing completion evidence: plan-execute planner phase requires a successful submit_plan tool result before final answer.",
		},
	}
	gomega.Expect(recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure))).To(gomega.BeTrue(), fmt.Sprintf("expected repeated submit_plan completion failure with exact scope to retry on active model"))

	failure.Result.FailureReason = "<<QUESTION>>: required_edit_paths_remaining"
	failure.Result.PlannerAnswer = "Planner failed after submit_plan rejected: required edit path coverage mismatch; missing required edit path(s): `internal/surfaces/cli/product/product_workflow_test.go`; invalid edit anchor(s): `internal/surfaces/cli/product/product_workflow.go: old_text not found in current source`."

	gomega.Expect(recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure))).To(gomega.BeTrue(), fmt.Sprintf("expected invalid submit_plan with exact scope to retry on active model"))

	failure.Result.FailureReason = "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged."
	failure.Result.PlannerAnswer = ""
	failure.Stdout = "Blocker:\n- Plan submission required: every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request whose `edits[]` coverage includes every required path."

	gomega.Expect(recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure))).To(gomega.BeTrue(), fmt.Sprintf("expected post-readiness discovery loop with exact scope to retry on active model"))

	failure.Options.UseActiveModel = true
	gomega.Expect(recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure))).To(gomega.BeFalse(), fmt.Sprintf("active-model retry must not recursively retry on active model"))

	failure.Options.UseActiveModel = false
	failure.RequiredPaths = nil

	gomega.Expect(recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure))).To(gomega.BeFalse(), fmt.Sprintf("unscoped planner blocker should not use active-model fallback"))
})

var _ = ginkgo.It("plan execute planner blocked message does not claim unread sources after source satisfied", func() {
	failure := implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go"},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.",
		},
		Stdout: "Blocker:\n- Plan submission required: every required_edit_path has satisfied its source-body obligation. Submit one parser-readable `submit_plan` request.",
	}
	answer := humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   "the planner failed because it lacks the current source code for the required edit paths",
		Message:  "Planner blocked: the planner cannot generate a valid `submit_plan` because it has not read the current state of the required files. Please allow the planner to read these files.",
	})

	gomega.Expect(strings.ToLower(answer)).To(gomega.Not(gomega.ContainSubstring("has not read")), fmt.Sprintf("source-satisfied failure must not surface contradictory unread-source guidance:\n%s", answer))
	gomega.Expect(strings.ToLower(answer)).To(gomega.Not(gomega.ContainSubstring("please allow the planner to read")), fmt.Sprintf("source-satisfied failure must not surface contradictory unread-source guidance:\n%s", answer))

	gomega.Expect(strings.Contains(answer, "kept choosing discovery instead of emitting a `submit_plan`")).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied blocker summary, got:\n%s", answer))
})

var _ = ginkgo.It("plan execute active model fallback addendum inserted before brief end", func() {
	prompt := "# Objective\n\nUpdate files.\n\n" + pathparse.BriefBodyEndMarker + "\n\n# Tooling"
	got := recovery.AppendPlanExecuteActiveModelFallbackAddendum(prompt)

	gomega.Expect(strings.Contains(got, "## Active Model Fallback")).To(gomega.BeTrue(), fmt.Sprintf("fallback addendum missing:\n%s", got))

	gomega.Expect(strings.Index(got, "## Active Model Fallback") > strings.Index(got, pathparse.BriefBodyEndMarker)).To(gomega.BeFalse(), fmt.Sprintf("fallback addendum should stay inside brief body:\n%s", got))

	gomega.Expect(got).To(gomega.ContainSubstring("fresh tool transcript"), fmt.Sprintf("fallback addendum should make source evidence freshness explicit:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("source-body evidence from the previous attempt is not executable evidence"), fmt.Sprintf("fallback addendum should make source evidence freshness explicit:\n%s", got))

	for _, forbidden := range []string{"already provided", "executor-role planner"} {
		gomega.Expect(strings.Contains(got, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("fallback addendum must not use confusing stale wording %q:\n%s", forbidden, got))
	}

	gomega.Expect(strings.Contains(got, "previous plan-execute planner attempt")).To(gomega.BeTrue(), fmt.Sprintf("fallback addendum should name the planner attempt without executor-role wording:\n%s", got))
})

var _ = ginkgo.It("plan execute planner recovery addendum uses harness owned retry contract", func() {
	failure := implementationDispatchPlanExecuteFailure{
		Brief:         "# Brief\n\nImplement typed JSON usage errors.",
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go"},
		Options: planexecute.Options{
			RequiredEditPaths: []string{"internal/surfaces/cli/product/product_workflow_test.go"},
		},
		Result: planexecute.RunResult{
			FailureReason: "completion gate rejected final answer before submit_plan",
		},
	}
	decision := recovery.PlanExecuteRecoveryDecision{
		Decision: "retry",
		Reason:   "the brief has exact scope",
		RetryAddendum: "CRITICAL: You MUST produce a valid `submit_plan` tool call. " +
			"It must contain exactly two edit entries. Each entry must have `Decision: source-satisfied` and include the full file content.",
	}

	got := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, `{"decision":"retry"}`)
	for _, want := range []string{
		"After this retry has satisfied source-body obligations and runtime-required git history evidence for every existing required path",
		"produce one valid `submit_plan` call",
		"internal/surfaces/cli/product/product_workflow.go",
		"internal/surfaces/cli/product/product_workflow_test.go",
		"`old_text`+`new_text`",
		"`Decision: source-satisfied` with current-state evidence",
		"`<<QUESTION>>:` with the exact path-specific blocker",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("recovery addendum missing %q:\n%s", want, got))
	}

	for _, forbidden := range []string{
		"Each entry must have `Decision: source-satisfied`",
		"include the full file content",
		"Recovery controller observation",
		"Produce one valid `submit_plan` call whose edits include at least one real edit for each required path",
		"After this retry has satisfied source-body obligations for every existing required path and any runtime-required git history evidence",
	} {
		gomega.Expect(strings.Contains(got, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("recovery addendum leaked controller contract text %q:\n%s", forbidden, got))
	}
})

var _ = ginkgo.It("plan execute planner recovery addendum prioritizes required source read blocker", func() {
	failure := implementationDispatchPlanExecuteFailure{
		Brief:         "# Brief\n\nImplement coverage gate.",
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"},
		Options: planexecute.Options{
			RequiredEditPaths: []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"},
		},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "Required edit path source read required: read source for internal/surfaces/cli/product/product_workflow_test.go before attempting to submit_plan.",
		},
	}
	path, ok := planExecuteRequiredSourceReadBlockerPath(failure)

	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("source-read blocker path = %q, %v", path, ok))
	gomega.Expect(path).To(gomega.Equal("internal/surfaces/cli/product/product_workflow_test.go"), fmt.Sprintf("source-read blocker path = %q, %v", path, ok))

	got := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, recovery.PlanExecuteRecoveryDecision{
		Decision:      "retry",
		Reason:        "source read blocker",
		RetryAddendum: "First read source for `internal/surfaces/cli/product/product_workflow_test.go`.",
	}, "")
	gomega.Expect(strings.Contains(got, "First read source for `internal/surfaces/cli/product/product_workflow_test.go`")).To(gomega.BeTrue(), fmt.Sprintf("recovery addendum should prioritize the required source read:\n%s", got))

	idxRead := strings.Index(got, "First read source")
	idxHistory := strings.Index(got, "then satisfy runtime-required git history evidence")
	idxSubmit := strings.Index(got, "then produce one valid `submit_plan`")

	gomega.Expect(idxRead).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("source read instruction should precede history evidence, then submit_plan:\n%s", got))
	gomega.Expect(idxHistory).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("source read instruction should precede history evidence, then submit_plan:\n%s", got))
	gomega.Expect(idxSubmit).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("source read instruction should precede history evidence, then submit_plan:\n%s", got))
	gomega.Expect(idxRead).To(gomega.BeNumerically("<=", idxHistory), fmt.Sprintf("source read instruction should precede history evidence, then submit_plan:\n%s", got))
	gomega.Expect(idxHistory).To(gomega.BeNumerically("<=", idxSubmit), fmt.Sprintf("source read instruction should precede history evidence, then submit_plan:\n%s", got))
})

var _ = ginkgo.It("plan execute planner recovery detects anchor refresh blocker", func() {
	failure := implementationDispatchPlanExecuteFailure{
		Brief:         "# Brief\n\nImplement the collapse dispatch helper.",
		RequiredPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
		Options: planexecute.Options{
			RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
		},
		Result: planexecute.RunResult{
			FailureKind: string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: strings.Join([]string{
				"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
				"",
				"Remaining bounded refresh paths:",
				"- native/src/core/colony_sim_tick.cpp",
				"Next valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path.",
			}, "\n"),
		},
	}

	path, ok := planExecuteRequiredSourceReadBlockerPath(failure)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("anchor-refresh blocker path = %q, %v", path, ok))
	gomega.Expect(path).To(gomega.Equal("native/src/core/colony_sim_tick.cpp"), fmt.Sprintf("anchor-refresh blocker path = %q, %v", path, ok))

	got := planExecutePlannerRecoveryRetryInstruction(failure)
	gomega.Expect(strings.Contains(got, "First read source for `native/src/core/colony_sim_tick.cpp`")).To(gomega.BeTrue(), fmt.Sprintf("recovery instruction should prioritize anchor-refresh path:\n%s", got))
})

var _ = ginkgo.It("plan execute planner recovery detects truncated anchor refresh blocker", func() {
	failure := implementationDispatchPlanExecuteFailure{
		Brief:         "# Brief\n\nImplement the collapse dispatch helper.",
		RequiredPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
		Options: planexecute.Options{
			RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"},
		},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
			Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
				NextAction: recoverycontract.NextActionAnswerBlocker,
			},
		},
	}

	refresh, ok := planExecuteRequiredSourceReadBlocker(failure)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected truncated anchor-refresh blocker to be machine-actionable"))

	gomega.Expect(slices.Equal(refresh.Paths, []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"})).To(gomega.BeTrue(), fmt.Sprintf("refresh paths = %#v", refresh.Paths))

	got := planExecutePlannerRecoveryRetryInstruction(failure)
	for _, want := range []string{
		"First refresh source-body evidence for each candidate anchor path",
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.h",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("recovery instruction missing %q:\n%s", want, got))
	}
})

var _ = ginkgo.It("feature planner atom plan execute retries typed anchor refresh completion", func() {
	var calls int
	var retryPrompt string
	model := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: config.Default()})

	runOnce := func(_ context.Context, prompt string) (string, error) {
		calls++
		model.lastPlanExecuteRunResult = nil
		if calls == 1 {
			model.lastPlanExecuteRunResult = &planexecute.RunResult{
				ExitCode:      1,
				FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
				FailureReason: "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h\nNext valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path; do not answer source-satisfied or blocker before that refresh returns.",
				Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
					Status:            "failed",
					Phase:             plancontract.PlanExecuteProgressPhasePlanning,
					FailureKind:       plancontract.PlanExecuteFailurePlannerBlocked,
					FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h\nNext valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path; do not answer source-satisfied or blocker before that refresh returns.",
					NextAction:        recoverycontract.NextActionRetryPlanExecute,
					RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.cpp", "native/src/simulation/world/collapse_event.h", "native/tests/test_collapse_event.cpp"},
				},
			}
			return "", fmt.Errorf("plan-execute exited 1")
		}

		retryPrompt = prompt
		model.lastPlanExecuteRunResult = &planexecute.RunResult{
			ExitCode: 0,
			Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
				Status:       plancontract.PlanExecuteTerminalStatusCompleted,
				Phase:        plancontract.PlanExecuteProgressPhaseComplete,
				ReviewStatus: plancontract.PlanExecuteReviewStatusCompleted,
			},
		}
		return `{"type":"idiom.plan_execute.completed","status":"completed","phase":"complete","review_status":"passed","state_path":".idiom/plan-execute/sessions/atom-1-run-1.json"}`, nil
	}

	prompt := strings.Join([]string{
		"Implement one feature atom.",
		"Plan-execute session id: atom-1-run-1",
		"Active plan contract id: feature/atom-1/scope",
		"Active plan contract snapshot hash: sha256:abc123",
		"This atom brief is consumed by plan-execute. Plan-execute owns the terminal response protocol.",
		"implement collapse helper",
	}, "\n")
	completion, err := model.runFeaturePlannerAtomPlanExecuteWithRunner(context.Background(), prompt, nil, false, runOnce)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerAtomPlanExecuteWithWriteEnvelope: %v", err))

	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("plan-execute calls = %d, want 2", calls))

	for _, want := range []string{
		"## Plan-execute Recovery Addendum",
		"native/src/simulation/world/collapse_event.h",
		"First read source for `native/src/simulation/world/collapse_event.h`",
	} {
		gomega.Expect(strings.Contains(retryPrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("retry prompt missing %q:\n%s", want, retryPrompt))
	}

	var fields map[string]any
	{
		err := json.Unmarshal([]byte(completion), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion: %v", err))
	}
	{
		got := fmt.Sprint(fields["status"])
		gomega.Expect(got).To(gomega.Equal("completed"), fmt.Sprintf("status = %q, want completed; fields=%+v", got, fields))
	}
	{
		got := strings.TrimSpace(fmt.Sprint(fields["failure_kind"]))
		gomega.Expect(got).To(gomega.Or(gomega.BeEmpty(), gomega.Equal("<nil>")), fmt.Sprintf("stale first-run failure leaked into final completion: fields=%+v", fields))
	}
	gomega.Expect(fields["contract_id"]).To(gomega.Equal("feature/atom-1/scope"))
	gomega.Expect(fields["snapshot_hash"]).To(gomega.Equal("sha256:abc123"))
})

var _ = ginkgo.It("feature planner atom plan execute does not retry human blocker", func() {
	var calls int
	model := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: config.Default()})

	runOnce := func(_ context.Context, _ string) (string, error) {
		calls++
		model.lastPlanExecuteRunResult = &planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: product owner must clarify scope",
			Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
				Status:            "failed",
				Phase:             plancontract.PlanExecuteProgressPhasePlanning,
				FailureKind:       plancontract.PlanExecuteFailurePlannerBlocked,
				FailureReason:     "<<QUESTION>>: product owner must clarify scope",
				NextAction:        recoverycontract.NextActionAnswerBlocker,
				RequiredEditPaths: []string{"docs/feature.md"},
			},
		}
		return "", fmt.Errorf("plan-execute exited 1")
	}

	completion, err := model.runFeaturePlannerAtomPlanExecuteWithRunner(context.Background(), "implement docs atom", nil, false, runOnce)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerAtomPlanExecuteWithRunner: %v", err))

	gomega.Expect(calls).To(gomega.Equal(1), fmt.Sprintf("plan-execute calls = %d, want no retry", calls))

	var fields map[string]any
	{
		err := json.Unmarshal([]byte(completion), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion: %v", err))
	}
	{
		got := fmt.Sprint(fields["next_valid_action"])
		gomega.Expect(got).To(gomega.Equal(string(recoverycontract.NextActionAnswerBlocker)), fmt.Sprintf("next_valid_action = %q, want answer_blocker; fields=%+v", got, fields))
	}
})

var _ = ginkgo.It("feature planner atom plan execute returns second failure after retry", func() {
	var calls int
	model := newDetachedPromptExecutionSession(context.Background(), &Model{cfg: config.Default()})

	runOnce := func(_ context.Context, _ string) (string, error) {
		calls++
		model.lastPlanExecuteRunResult = nil
		if calls == 1 {
			model.lastPlanExecuteRunResult = &planexecute.RunResult{
				ExitCode:      1,
				FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
				FailureReason: "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h",
				Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
					Status:            "failed",
					Phase:             plancontract.PlanExecuteProgressPhasePlanning,
					FailureKind:       plancontract.PlanExecuteFailurePlannerBlocked,
					FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\n\nRemaining bounded refresh paths:\n- native/src/simulation/world/collapse_event.h",
					NextAction:        recoverycontract.NextActionRetryPlanExecute,
					RequiredEditPaths: []string{"native/src/simulation/world/collapse_event.h"},
				},
			}
			return "", fmt.Errorf("plan-execute exited 1")
		}

		model.lastPlanExecuteRunResult = &planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   "verification_failed",
			FailureReason: "collapse_event tests failed",
			Completion: &plancontract.PlanExecuteTerminalCompletionRecord{
				Status:        "failed",
				Phase:         plancontract.PlanExecuteProgressPhasePlanning,
				FailureKind:   "verification_failed",
				FailureReason: "collapse_event tests failed",
			},
		}
		return "", fmt.Errorf("plan-execute exited 1")
	}

	completion, err := model.runFeaturePlannerAtomPlanExecuteWithRunner(context.Background(), "implement collapse helper", nil, false, runOnce)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerAtomPlanExecuteWithRunner: %v", err))

	gomega.Expect(calls).To(gomega.Equal(2), fmt.Sprintf("plan-execute calls = %d, want retry then final failure", calls))

	var fields map[string]any
	{
		err := json.Unmarshal([]byte(completion), &fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode completion: %v", err))
	}
	{
		got := fmt.Sprint(fields["failure_kind"])
		gomega.Expect(got).To(gomega.Equal("verification_failed"), fmt.Sprintf("failure_kind = %q, want second-run failure; fields=%+v", got, fields))
	}
	gomega.Expect(strings.Contains(fmt.Sprint(fields["failure_reason"]), "anchor refresh")).To(gomega.BeFalse(), fmt.Sprintf("stale first-run failure leaked into second-run completion: fields=%+v", fields))
})

var _ = ginkgo.It("plan execute planner recovery instruction keeps allowed candidates non mandatory", func() {
	got := planExecutePlannerRecoveryRetryInstruction(implementationDispatchPlanExecuteFailure{
		AllowedPaths: []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering_test.go"},
	})
	for _, want := range []string{
		"allowed candidate paths",
		"edit allowlist",
		"do not treat every candidate path as mandatory coverage",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("allowed-scope retry instruction missing %q:\n%s", want, got))
	}

	gomega.Expect(strings.Contains(got, "whose edits include at least one real edit for each required path")).To(gomega.BeFalse(), fmt.Sprintf("allowed candidate retry must not use required-path coverage wording:\n%s", got))
})

var _ = ginkgo.It("plan execute planner recovery instruction preserves history gate", func() {
	got := planExecutePlannerRecoveryRetryInstruction(implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"internal/surfaces/tui/app/rendering.go"},
		Options: planexecute.Options{
			RequiredEditPaths: []string{"internal/surfaces/tui/app/rendering.go"},
		},
	})
	for _, want := range []string{
		"runtime-required git history evidence",
		"Do not treat a source read or history read as plan coverage",
		"one valid `submit_plan` call",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("required-scope retry instruction missing %q:\n%s", want, got))
	}

	{
		idxHistory, idxSubmit := strings.Index(got, "runtime-required git history evidence"), strings.Index(got, "one valid `submit_plan` call")
		gomega.Expect(idxHistory).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("history evidence instruction should precede submit_plan instruction:\n%s", got))
		gomega.Expect(idxSubmit).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("history evidence instruction should precede submit_plan instruction:\n%s", got))
		gomega.Expect(idxHistory).To(gomega.BeNumerically("<=", idxSubmit), fmt.Sprintf("history evidence instruction should precede submit_plan instruction:\n%s", got))
	}
})

var _ = ginkgo.It("plan execute missing edit recovery uses typed failure kind only", func() {
	tests := []struct {
		name           string
		result         planexecute.RunResult
		wantChain      bool
		wantObligation plancontract.EditSetObligationKind
	}{
		{
			name: "typed missing test edit chains without prompt wording",
			result: planexecute.RunResult{
				FailureKind:   string(plancontract.PlanExecuteFailureMissingRequestedTestEdit),
				FailureReason: "edit-set contract rejected the submitted artifact",
			},
			wantChain:      true,
			wantObligation: plancontract.EditSetObligationKindTestEdit,
		},
		{
			name: "typed missing implementation edit chains without prompt wording",
			result: planexecute.RunResult{
				FailureKind:   "  " + string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit) + "  ",
				FailureReason: "edit-set contract rejected the submitted artifact",
			},
			wantChain:      true,
			wantObligation: plancontract.EditSetObligationKindImplementationEdit,
		},
		{
			name: "prompt-like wording does not chain without typed failure kind",
			result: planexecute.RunResult{
				FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
				FailureReason: "the original prompt said to add regression tests and implement the product workflow",
				PlannerAnswer: "missing_requested_test_edit",
			},
			wantChain: false,
		},
		{
			name: "executor failure does not chain from wording",
			result: planexecute.RunResult{
				FailureKind:   string(plancontract.PlanExecuteFailureExecutorFailed),
				FailureReason: "executor failed after a request to add tests and implementation edits",
			},
			wantChain: false,
		},
	}
	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			{
				got := implementationDispatchShouldChainMissingEditRecovery(tt.result)
				gomega.Expect(got).To(gomega.Equal(tt.wantChain), fmt.Sprintf("chain missing-edit recovery = %t, want %t", got, tt.wantChain))
			}

			gotObligation, ok := plancontract.PlanExecuteFailureKindEditSetObligation(implementationDispatchPlanExecuteRunFailureKind(tt.result))
			if tt.wantObligation == "" {
				gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("unexpected obligation %q from failure kind %q", gotObligation, tt.result.FailureKind))

				return
			}

			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("obligation = %q, %t; want %q, true", gotObligation, ok, tt.wantObligation))
			gomega.Expect(gotObligation).To(gomega.Equal(tt.wantObligation), fmt.Sprintf("obligation = %q, %t; want %q, true", gotObligation, ok, tt.wantObligation))
		})
	}
})

var _ = ginkgo.It("implementation dispatch recovery message is internal with quoted submit plan tool call", func() {
	msg := "Respond with a valid `submit_plan` tool call only; do not narrate completion."
	gomega.Expect(implementationDispatchRecoveryMessageIsInternal(msg)).To(gomega.BeTrue(), fmt.Sprintf("expected quoted submit_plan tool-call instruction to be internal"))

	{
		observation := planExecutePlannerRecoveryObservation(msg)
		gomega.Expect(observation).To(gomega.Equal(""), fmt.Sprintf("expected internal retry addendum to be suppressed, got %q", observation))
	}
})

var _ = ginkgo.It("plan execute terminal outcome matrix for tui wrapper", func() {
	tests := []struct {
		name           string
		failure        implementationDispatchPlanExecuteFailure
		recovery       bool
		sourceMismatch bool
		want           []string
		forbidden      []string
	}{
		{
			name: "planner source-satisfied missing required test path surfaces without recovery model",
			failure: implementationDispatchPlanExecuteFailure{
				RequiredPaths: []string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"},
				Result: planexecute.RunResult{
					ExitCode:      1,
					FailureKind:   string(plancontract.PlanExecuteFailurePlanArtifactMissing),
					FailureReason: "source-satisfied evidence named no test path",
					PlannerAnswer: "Decision: source-satisfied\n- internal/workflows/productworkflow/migration_apply.go: no changes needed.",
				},
			},
			recovery:       true,
			sourceMismatch: true,
			want: []string{
				"terminal evidence mismatch",
				"internal/workflows/productworkflow/migration_apply.go",
				"internal/workflows/productworkflow/migration_apply_test.go",
				"will not stack another recovery prompt",
			},
			forbidden: []string{"planner_blocked decision", "automatic retry"},
		},
		{
			name: "planner blocked with discovered scope remains recoverable",
			failure: implementationDispatchPlanExecuteFailure{
				AllowedPaths: []string{"internal/surfaces/tui/app/rendering.go"},
				Result: planexecute.RunResult{
					ExitCode:      1,
					FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
					FailureReason: "completion gate rejected final answer before submit_plan",
				},
			},
			recovery:       true,
			sourceMismatch: false,
		},
		{
			name: "executor failed is not a planner recovery case",
			failure: implementationDispatchPlanExecuteFailure{
				Result: planexecute.RunResult{
					ExitCode:      1,
					FailureKind:   string(plancontract.PlanExecuteFailureExecutorFailed),
					FailureReason: "edit failed",
				},
			},
			recovery:       false,
			sourceMismatch: false,
		},
		{
			name: "missing requested test edit is recoverable",
			failure: implementationDispatchPlanExecuteFailure{
				Result: planexecute.RunResult{
					ExitCode:      1,
					FailureKind:   string(plancontract.PlanExecuteFailureMissingRequestedTestEdit),
					FailureReason: "typed test-edit obligation is unsatisfied because plan named no test path",
				},
			},
			recovery:       true,
			sourceMismatch: false,
		},
		{
			name: "missing requested implementation edit is recoverable",
			failure: implementationDispatchPlanExecuteFailure{
				Result: planexecute.RunResult{
					ExitCode:      1,
					FailureKind:   string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit),
					FailureReason: "typed implementation-edit obligation is unsatisfied because plan named only test paths",
				},
			},
			recovery:       true,
			sourceMismatch: false,
		},
	}
	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			{
				got := recovery.PlanExecuteFailureCanUseRecovery(implementationDispatchPlanExecuteRunFailureKind(tt.failure.Result))
				gomega.Expect(got).To(gomega.Equal(tt.recovery), fmt.Sprintf("recovery eligibility = %t, want %t", got, tt.recovery))
			}

			answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(tt.failure)
			gomega.Expect(ok).To(gomega.Equal(tt.sourceMismatch), fmt.Sprintf("source-satisfied mismatch = %t, want %t; answer=%q", ok, tt.sourceMismatch, answer))

			for _, want := range tt.want {
				gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("answer missing %q:\n%s", want, answer))
			}

			for _, forbidden := range tt.forbidden {
				gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked %q:\n%s", forbidden, answer))
			}
		})
	}
})

var _ = ginkgo.It("plan execute source satisfied coverage ignores allowed candidate paths", func() {
	failure := implementationDispatchPlanExecuteFailure{
		AllowedPaths: []string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"},
		Result: planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailurePlanArtifactMissing),
			FailureReason: "source-satisfied evidence named one candidate path",
			PlannerAnswer: "Decision: source-satisfied\n- internal/workflows/productworkflow/migration_apply.go: no changes needed.",
		},
	}

	answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure)
	gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("allowed candidate paths must not be promoted to source-satisfied coverage obligations:\n%s", answer))
})

var _ = ginkgo.It("plan execute source satisfied coverage blocker uses exact missing paths", func() {
	failure := implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"pkg/parser.go", "pkg/parser_test.go"},
		Result: planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailurePlanArtifactMissing),
			FailureReason: "source-satisfied evidence missed one required path",
			PlannerAnswer: "Decision: source-satisfied\n- pkg/parser.go.bak: already present.\n- pkg/parser_test.go: already present.",
		},
	}

	answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected exact source-satisfied mismatch blocker"))

	gomega.Expect(strings.Contains(answer, "\n- pkg/parser.go")).To(gomega.BeTrue(), fmt.Sprintf("blocker should name the exact missing path, got:\n%s", answer))
})

var _ = ginkgo.It("plan execute source satisfied coverage blocker reports extensionless covered path", func() {
	failure := implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"BUILD", "Dockerfile"},
		Result: planexecute.RunResult{
			ExitCode:      1,
			FailureKind:   string(plancontract.PlanExecuteFailurePlanArtifactMissing),
			FailureReason: "source-satisfied evidence missed one required path",
			PlannerAnswer: "Decision: source-satisfied\n- Dockerfile: already present.",
		},
	}

	answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected extensionless source-satisfied mismatch blocker"))

	for _, want := range []string{"Covered source-satisfied evidence paths:\n- Dockerfile", "Missing required edit paths:\n- BUILD"} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("blocker missing %q:\n%s", want, answer))
	}
})

var _ = ginkgo.It("handle stream done clears plan phase on terminal planner failure", func() {
	m := &Model{
		streaming:     true,
		planPhaseText: "Planning: Analyzing objective and creating plan...",
	}

	m.handleStreamDone(streamDoneMsg{err: errors.New("planner phase reported a blocker before submitting a plan")})

	gomega.Expect(m.streaming).To(gomega.BeFalse(), fmt.Sprint("streaming should be false after terminal planner failure"))

	gomega.Expect(m.planPhaseText).To(gomega.Equal(""), fmt.Sprintf("terminal planner failure must not leave active plan phase text, got %q", m.planPhaseText))

	gomega.Expect(strings.Contains(m.status, "Chat failed: planner phase reported a blocker")).To(gomega.BeTrue(), fmt.Sprintf("expected terminal error status, got %q", m.status))
})

var _ = ginkgo.It("implementation dispatch refine diagnostics do not truncate actionable line", func() {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
	diagnostics := implementationdispatch.RefineDiagnostics(implementationdispatch.PlanDocumentRefineResult{
		Stderr: "noise\nrefine: iter 1\n" + longBlocker + "\n",
	})
	joined := strings.Join(diagnostics, "\n")

	gomega.Expect(strings.Contains(joined, longBlocker)).To(gomega.BeTrue(), fmt.Sprintf("diagnostics lost actionable blocker:\n%s", joined))

	gomega.Expect(strings.Contains(joined, "[truncated")).To(gomega.BeFalse(), fmt.Sprintf("diagnostics should not include truncation marker:\n%s", joined))
})

var _ = ginkgo.It("fallback turn route does not recognize debug logs from heuristics", func() {
	plan := fallbackTurnRoute("Check our most recent logs and explain why it failed.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("route classifier chat client falls back to planner when classifier unreachable", func() {
	controller := gomock.NewController(ginkgo.GinkgoT())
	planner := llamamocks.NewMockLLMClient(controller)
	classifier := llamamocks.NewMockLLMClient(controller)
	classifier.EXPECT().ListModels(gomock.Any()).Return(nil, errors.New("classifier down")).Times(1)
	model := &Model{
		client:        planner,
		summaryClient: classifier,
	}

	{
		got := newDetachedPromptExecutionSession(context.Background(), model).routeClassifierChatClient()
		gomega.Expect(got).To(gomega.Equal(planner), fmt.Sprintf("expected active planner client fallback, got %#v", got))
	}
})

var _ = ginkgo.It("correct turn route plan corrects conceptual debug log question to architecture", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "What does the debug log redacted mode do?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("architecture"), fmt.Sprintf("family = %q, want architecture", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("architecture"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("architecture_assessment"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan keeps explicit log artifact requests on standard read lane", func() {
	prompts := []string{
		"Check our most recent logs and explain why it failed.",
		"Read logs/conversations/20260531-124928.794.jsonl and explain the failure.",
		"Search the telemetry logs for the latest panic.",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, prompt, modepolicy.BuildMode())
		gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family for %q = %q, want conversation", prompt, plan.Family))

		gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
	}
})

var _ = ginkgo.It("correct turn route plan keeps source debug prompt on standard read lane", func() {
	prompt := "you are a debugging assistant to another agent. you are tasked with finding the cause of terminal resizing causing conversation window to become unscrollable. Please provide your proof with evidence, files, symbols, and line numbers."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("source-debug prompt should use standard read lane, got %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("source-debug prompt should use standard read lane, got %#v", plan))
})

var _ = ginkgo.It("correct turn route plan keeps architecture for project feature question", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "architecture"}, "What does the debug log redacted mode do?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("architecture"), fmt.Sprintf("family = %q, want architecture", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("architecture"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("architecture_assessment"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan corrects repo internal questions to architecture", func() {
	prompts := []string{
		"What does the debug log redacted mode do?",
		"Can you tell me how this harness uses subagents?",
		"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, prompt, modepolicy.BuildMode())
		gomega.Expect(plan.Family).To(gomega.Equal("architecture"), fmt.Sprintf("family for %q = %q, want architecture", prompt, plan.Family))

		gomega.Expect(plan.InitialLane).To(gomega.Equal("architecture"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("architecture_assessment"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
	}
})

var _ = ginkgo.It("correct turn route plan keeps operational how to question read only", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, "How do I run plan-execute without changing files?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan keeps question shaped ui layout conversation", func() {
	prompt := "The current model string in the top-right portion of the banner shows a path with what seems to be a stripped filename. Can we shorten this to be just the model's alias?"
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan keeps source diagnosis on standard read lane", func() {
	prompt := "you are a research assistant for another agentic process; please provide structured evidence in the form of files, line numbers, and relevant symbols. We are trying to debug an issue where resizing the TUI mid-answer caused the conversation to become non-scrollable. Please debug and present your findings."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
		Family:             "debug_logs",
		InitialLane:        "explore",
		Contract:           "grounded_explanation",
		RequestedOutcome:   "diagnose",
		WriteIntent:        "no",
		ReadOnlyConstraint: true,
	}, prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("normalize turn route plan diagnose defaults to conversation", func() {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		RequestedOutcome: "diagnose",
		WriteIntent:      "no",
	}, modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan does not use prompt fallback heuristics", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "Run the tests and report failures.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route recognizes code explanation as conversation", func() {
	plan := fallbackTurnRoute("How does this harness use sub-agents?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route never activates lanes from prompt heuristics", func() {
	prompts := []string{
		"Run the tests and report failures.",
		"Find where the banner text is rendered, then remove it.",
		"Update the panel script in ui/status_panel.gd.",
		"Use the plan-execute lane to apply these exact edits.",
		"I want to test the implementation lane.",
		"Implement docs/plan.md and add regression tests.",
		"Check the most recent logs and explain why it failed.",
	}
	for _, prompt := range prompts {
		plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())
		gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("fallback heuristic activated lane for %q: %#v", prompt, plan))

		gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected conservative fallback for %q: %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected conservative fallback for %q: %#v", prompt, plan))
	}
})

var _ = ginkgo.It("fallback turn route recognizes explicit file delete followup", func() {
	plan := fallbackTurnRoute("can you delete that new file now?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("family = %q, want implementation: %#v", plan.Family, plan))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected delete follow-up route: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected delete follow-up route: %#v", plan))
	gomega.Expect(plan.WriteIntent).To(gomega.Equal("yes"), fmt.Sprintf("unexpected delete follow-up route: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not treat content removal as file deletion", func() {
	plan := fallbackTurnRoute("remove obsolete text from temp.md", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("content removal should stay conservative, got %#v", plan))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected conservative fallback: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected conservative fallback: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize verification only from heuristics", func() {
	plan := fallbackTurnRoute("Run the tests in the seeded failing fixture and report the failure without editing files.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize suggestion question as architecture", func() {
	plan := fallbackTurnRoute("What can we add to our discovery tools in an effort at increasing their overall usefulness?", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route keeps question shaped ui layout request read only", func() {
	prompt := "can you re-align the text input field to be flush against the bottom of the terminal? When it needs to wrap to a new line, it should simply increase in height to avoid going off-screen"
	plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize locate from heuristics", func() {
	plan := fallbackTurnRoute("Find where the nonexistent banner text is rendered. If it is not present, say that clearly and do not edit files.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.Equal(""), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize locate then remove as implementation", func() {
	plan := fallbackTurnRoute("Find where the banner text is rendered, then remove it.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.Equal(""), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize gd script edits from heuristics", func() {
	plan := fallbackTurnRoute("Update the panel script in ui/status_panel.gd.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not recognize start working on named doc as docs edit", func() {
	for _, prompt := range []string{
		"can you please start working on docs/writer-route-decision-report-only-plan.md?",
		"can you start woring on docs/writer-route-decision-report-only-plan.md?",
		"can you start implementing docs/writer-route-decision-report-only-plan.md?",
	} {
		plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())
		gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family for %q = %q, want conversation", prompt, plan.Family))

		gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
		gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan for %q: %#v", prompt, plan))
	}
})

var _ = ginkgo.It("correct turn route plan does not downgrade classifier implementation route", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Explain how this harness uses sub-agents.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("explore_then_implement"), fmt.Sprintf("family = %q, want explore_then_implement", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("explore"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("run prompt once dispatches start implementing plan doc to plan execute", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/dispatch\n\ngo 1.22\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write go.mod: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nconst Value = \"old\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/target.go | replace fixture text |",
		"",
		"## Verified Current State",
		"",
		"`pkg/target.go` currently contains the old fixture value.",
		"",
		"## Required Edits",
		"",
		"old_text:",
		"",
		"```text",
		"const Value = \"old\"",
		"```",
		"",
		"new_text:",
		"",
		"```text",
		"const Value = \"new\"",
		"```",
		"",
		"## Acceptance",
		"",
		"- `go test ./...`",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	var (
		chatRequests    atomic.Int64
		sawReviewPrompt atomic.Bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			chatRequests.Add(1)

			requestText := string(body)
			reply := ""
			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				gomega.Expect(strings.Contains(requestText, `"enable_thinking":false`)).To(gomega.BeTrue(), fmt.Sprintf("review request should force visible terminal output with thinking disabled:\n%s", requestText))

				sawReviewPrompt.Store(true)

				reply = "Decision: pass\n\nVerification Checklist:\n- pkg/target.go contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				ginkgo.Fail(fmt.Sprintf("complete exact plan-doc packet should auto-submit before planner model call:\n%s", requestText), 1)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"

	thinkingOn := true

	cfg.Chat.EnableThinking = &thinkingOn
	cfg.Chat.PreserveThinking = true
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	got, err := os.ReadFile(filepath.Join(root, "pkg", "target.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("package pkg\n\nconst Value = \"new\"\n"), fmt.Sprintf("target content = %q, want updated Go file", string(got)))

	planArtifact := loadOnlyTimestampedPlanArtifactForTest(t, root)
	gomega.Expect(planArtifact.SchemaVersion).To(gomega.Equal(planartifact.SchemaVersion), fmt.Sprintf("plan artifact schema = %q, want %q", planArtifact.SchemaVersion, planartifact.SchemaVersion))

	gomega.Expect(planArtifact.SessionID).NotTo(gomega.Equal(""), fmt.Sprintf("plan artifact should carry the plan-execute session id"))

	gomega.Expect(len(planArtifact.Edits)).To(gomega.Equal(1), fmt.Sprintf("plan artifact edits = %#v, want one pkg/target.go edit", planArtifact.Edits))
	gomega.Expect(planArtifact.Edits[0].Path).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("plan artifact edits = %#v, want one pkg/target.go edit", planArtifact.Edits))

	gomega.Expect(strings.Contains(answer, "plan-then-execute summary")).To(gomega.BeTrue(), fmt.Sprintf("expected plan-execute summary answer, got %q", answer))

	gomega.Expect(sawReviewPrompt.Load()).To(gomega.BeTrue(), fmt.Sprintf("expected review-repair prompt after executor phase"))

	gomega.Expect(chatRequests.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected only review chat request after auto-exact planning, got %d", chatRequests.Load()))
})

var _ = ginkgo.It("run prompt once blocks ambiguous plan doc basename before plan execute", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	for _, dir := range []string{"docs", "notes"} {
		{
			err := os.MkdirAll(filepath.Join(root, dir), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", dir, err))
		}

		body := "# Plan\n\n## Files To Modify\n\n- target.txt\n"
		{
			err := os.WriteFile(filepath.Join(root, dir, "plan.md"), []byte(body), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s plan doc: %v", dir, err))
		}
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			chatRequests.Add(1)
			http.Error(w, "planner should not be called for ambiguous design-doc shorthand", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce should return a typed ambiguity blocker, got err=%v answer=%q", err, answer))

	for _, want := range []string{
		"Plan-execute dispatch blocked before the planner ran",
		"ambiguous shorthand",
		"docs/plan.md",
		"notes/plan.md",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("ambiguity answer missing %q:\n%s", want, answer))
	}

	gomega.Expect(chatRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("planner should not be called for ambiguous design-doc shorthand, got %d chat request(s)", chatRequests.Load()))
})

var _ = ginkgo.It("run prompt once blocks unlinted plan doc before plan execute", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		return implementationdispatch.PlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers: []string{
				"brief lint: brief shape is unknown: no `## Files To Modify` and no markdown deliverable shape",
				"brief lint: unknown fence tag ```go",
			},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		return implementationdispatch.PlanDocumentRefineResult{
			OriginalPath: doc.Path,
			OutputPath:   ".idiom/refine/tui-docs-plan/accepted.md",
			ExitCode:     1,
			Stderr:       "refine: max iters reached without ship-as-is verdict",
		}
	})

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"Status: implementation handoff",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | update target text |",
		"",
		"```go",
		"package demo",
		"```",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			chatRequests.Add(1)
			http.Error(w, "planner should not be called when plan-document preflight blocks dispatch", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce should surface preflight blocker as an assistant answer, got err=%v answer=%q", err, answer))

	for _, want := range []string{
		"automatic refine did not produce an accepted brief",
		".idiom/refine/tui-docs-plan/accepted.md",
		"brief shape is unknown",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("preflight answer missing %q:\n%s", want, answer))
	}

	gomega.Expect(chatRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("planner should not be called, got %d chat request(s)", chatRequests.Load()))
})

var _ = ginkgo.It("run prompt once surfaces refined candidate preflight blocker", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	const refinedPath = ".idiom/refine/tui-docs-plan/accepted.dispatch.md"

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		return implementationdispatch.PlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers:     []string{"brief lint: brief shape is unknown"},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		return implementationdispatch.PlanDocumentRefineResult{
			OriginalPath: doc.Path,
			OutputPath:   refinedPath,
			ExitCode:     1,
			Stderr:       "refine: max iters reached without ship-as-is verdict",
			Document: implementationdispatch.PlanDocument{
				Path:      refinedPath,
				Content:   "# Refined\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| target.txt | modify target |\n",
				EditPaths: []string{"target.txt"},
			},
			PostPreflightBlockers: []implementationdispatch.PlanDocumentPreflightResult{{
				Path:      refinedPath,
				OK:        false,
				AtomState: "blocked-dirty-files",
				Blockers: []string{
					"atom intake: existing scoped paths have uncommitted changes",
					"atom intake: dirty scoped paths: target.txt",
				},
			}},
		}
	})

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"Status: implementation handoff",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | update target text |",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("dirty\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			chatRequests.Add(1)
			http.Error(w, "planner should not be called when refined candidate preflight blocks dispatch", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce should surface refined candidate blocker as an assistant answer, got err=%v answer=%q", err, answer))

	for _, want := range []string{
		"Automatic refine produced a brief, but intake/lint still blocked plan-execute dispatch",
		refinedPath,
		"existing scoped paths have uncommitted changes",
		"dirty scoped paths: target.txt",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("refined preflight answer missing %q:\n%s", want, answer))
	}

	gomega.Expect(strings.Contains(answer, "automatic refine did not produce an accepted brief")).To(gomega.BeFalse(), fmt.Sprintf("answer should name the refined candidate blocker instead of generic refine failure:\n%s", answer))

	gomega.Expect(chatRequests.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("planner should not be called, got %d chat request(s)", chatRequests.Load()))
})

var _ = ginkgo.It("run prompt once keeps required scope authoritative after recovered plan refinement", ginkgo.Label("development-scenario", "integration", "contract", "race-contract"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	const refinedPath = "docs/recovered-plan.md"

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		if doc.Path == refinedPath {
			return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
		}

		return implementationdispatch.PlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers:     []string{"brief lint: brief shape is unknown"},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, cfg config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)
		outAbs, ok := implementationdispatch.WorkspaceFile(root, refinedPath)

		if !ok {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 2}
		}

		if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1, Err: err}
		}

		refinedBody := strings.Join([]string{
			"# Refined Plan",
			"",
			"<!-- idiom:refine-drafted title=tui-docs-plan iter=1 reason=ship-as-is -->",
			"",
			"## Files To Modify",
			"",
			"| File | Change |",
			"|---|---|",
			"| `target.txt` | Replace old text with new text. |",
			"",
			"## Verified Current State",
			"",
			"`target.txt` contains `old`.",
			"",
			"## Required Edits",
			"",
			"old_text:",
			"",
			"```text",
			"old",
			"```",
			"",
			"new_text:",
			"",
			"```text",
			"new",
			"```",
			"",
			"## Acceptance",
			"",
			"- `go test ./...`",
			"",
		}, "\n")
		if err := os.WriteFile(outAbs, []byte(refinedBody), 0o644); err != nil {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1, Err: err}
		}

		refinedDoc, ok := implementationdispatch.ReadPlanDocument(root, refinedPath)
		if !ok {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1}
		}

		return implementationdispatch.PlanDocumentRefineResult{
			OriginalPath: doc.Path,
			OutputPath:   refinedPath,
			OK:           true,
			Document:     refinedDoc,
		}
	})

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"Status: implementation handoff",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | update target text |",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""
			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- target.txt contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				ginkgo.Fail(fmt.Sprintf("refined exact dispatch brief should auto-submit before planner model call:\n%s", requestText), 1)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runPlanExecuteCommandOnce(context.Background(), "Implement docs/plan.md. Required edit paths: target.txt")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("new\n"), fmt.Sprintf("target content = %q, want new line", string(got)))

	gomega.Expect(strings.Contains(answer, "plan-then-execute summary")).To(gomega.BeTrue(), fmt.Sprintf("expected plan-execute summary answer, got %q", answer))

	planArtifact := loadOnlyTimestampedPlanArtifactForTest(t, root)
	gomega.Expect(len(planArtifact.Edits)).To(gomega.Equal(1), fmt.Sprintf("plan artifact edits = %#v, want one target.txt edit", planArtifact.Edits))
	gomega.Expect(planArtifact.Edits[0].Path).To(gomega.Equal("target.txt"), fmt.Sprintf("plan artifact edits = %#v, want one target.txt edit", planArtifact.Edits))

	gomega.Expect(execution.lastPlanExecuteRunResult).NotTo(gomega.BeNil())
	state, _, found, err := planexecute.TryLoadProgressState(root, execution.lastPlanExecuteRunResult.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(found).To(gomega.BeTrue())
	gomega.Expect(state.RequiredEditPaths).To(gomega.Equal([]string{"target.txt"}))
	gomega.Expect(state.AllowedEditPaths).To(gomega.BeEmpty(), "required paths must never survive as optional recovery candidates")
})

var _ = ginkgo.It("implementation dispatch workspace file rejects escapes and symlink ancestors", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	{
		_, ok := implementationdispatch.WorkspaceFile(root, "../outside.md")
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprint("parent traversal path should be rejected"))
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable on this platform: %v", err))
	}

	{
		_, ok := implementationdispatch.WorkspaceFile(root, "linked/escape.md")
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprint("path through symlinked workspace ancestor should be rejected"))
	}

	{
		_, ok := implementationdispatch.WorkspaceFile(root, "docs/new.md")
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("missing in-scope file should remain valid for new-file dispatch"))
	}
})

var _ = ginkgo.It("run prompt once atomizes design doc refines atoms and dispatches plan execute", ginkgo.SpecTimeout(90*time.Second), func(specCtx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		switch {
		case doc.Path == "docs/design.md":
			return implementationdispatch.PlanDocumentPreflightResult{
				Path:         doc.Path,
				OK:           false,
				LintBlockers: []string{"brief lint: single-purpose decomposition failed: 3 file-allowlist row(s) across 3 files. Decompose into ADD-only / MODIFY-only / TEST-only sub-atoms before dispatch."},
				Blockers:     []string{"brief lint: single-purpose decomposition failed: 3 file-allowlist row(s) across 3 files. Decompose into ADD-only / MODIFY-only / TEST-only sub-atoms before dispatch."},
			}
		case implementationdispatch.IsAtomizedPlanDocumentPath(doc.Path):
			return implementationdispatch.PlanDocumentPreflightResult{
				Path: doc.Path,
				OK:   true,
			}
		case strings.HasPrefix(doc.Path, ".idiom/refine/") && strings.HasSuffix(doc.Path, "/accepted.md"):
			return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
		default:
			return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, cfg config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		target := ""
		if len(doc.EditPaths) > 0 {
			target = doc.EditPaths[0]
		}

		if target == "" {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, ExitCode: 1, Stderr: "missing atom edit path"}
		}

		root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)
		outRel := filepath.ToSlash(filepath.Join(".idiom", "refine", implementationdispatch.DocumentOutputTitle(doc.Path), "accepted.md"))
		outAbs, ok := implementationdispatch.WorkspaceFile(root, outRel)

		if !ok {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 2}
		}

		if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1, Err: err}
		}

		change := "Apply this atom's design-doc slice."
		currentState := "`" + target + "` exists in the test fixture workspace."

		if target == "pkg/c.go" {
			change = "Create this atom's design-doc slice as a new file."
			currentState = "`" + target + "` is a new file target in the test fixture workspace."
		}

		refinedBody := strings.Join([]string{
			"# Refined Atom",
			"",
			"<!-- idiom:refine-drafted reason=ship-as-is -->",
			"",
			"## Files To Modify",
			"",
			"| File | Change |",
			"|---|---|",
			"| " + target + " | " + change + " |",
			"",
			"## Verified Current State",
			"",
			currentState,
			"",
			"## Required Behavior",
			"",
			"Implement only the bounded design-doc atom for `" + target + "`.",
			"",
			"## Acceptance",
			"",
			"- `" + target + "` contains the requested fixture replacement.",
			"",
		}, "\n")
		if err := os.WriteFile(outAbs, []byte(refinedBody), 0o644); err != nil {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1, Err: err}
		}

		refinedDoc, ok := implementationdispatch.ReadPlanDocument(root, outRel)
		if !ok {
			return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1}
		}

		return implementationdispatch.PlanDocumentRefineResult{
			OriginalPath: doc.Path,
			OutputPath:   outRel,
			OK:           false,
			ExitCode:     1,
			Stderr:       "refine: max iters reached after producing a lint-clean accepted brief",
			Document:     refinedDoc,
		}
	})

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/atomize\n\ngo 1.22\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write go.mod: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n\nconst A = \"old\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write a.go: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "b.go"), []byte("package pkg\n\nconst B = \"old\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write b.go: %v", err))
	}

	designDoc := strings.Join([]string{
		"# Design",
		"",
		"Status: implementation handoff",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| pkg/a.go | Replace A's fixture value with new. |",
		"| pkg/b.go | Replace B's fixture value with new. |",
		"| pkg/c.go | Create C's fixture value as new. |",
		"",
		"Typed plan execution contract:",
		`{"authorized_new_files":[{"path":"pkg/c.go"}]}`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-3"}`,
		"",
		"## Design Intent",
		"",
		"Fixture constants should move from old to new, but each file is a separate dispatch atom.",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte(designDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write design doc: %v", err))
	}
	initializeTurnPlannerGitRepository(t, root)

	var (
		plannerPromptRequests    atomic.Int64
		sawAtomPrompt            atomic.Bool
		sawUnexpectedPlannerCall atomic.Bool
		sawUnexpectedRecovery    atomic.Bool
	)

	sourceReadReplies := map[string]bool{}
	historyReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))
			var request struct {
				Messages []llama.ChatMessage `json:"messages"`
			}
			gomega.Expect(json.Unmarshal(body, &request)).To(gomega.Succeed())

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Plan-Execute Planner Blocked Recovery"):
				sawUnexpectedRecovery.Store(true)
				reply = `{"decision":"surface","reason":"test planner should have received one scoped atom path","message":"planner recovery was reached unexpectedly"}`
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- Scoped fixture file contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				requestNumber := plannerPromptRequests.Add(1)
				sawAtomPrompt.Store(true)

				expectedPath := ""
				switch requestNumber {
				case 1:
					expectedPath = "pkg/a.go"
					sourceReadReplies[expectedPath] = true
					reply = "<tool_call>\n" + `{"tool":"read_file","path":"pkg/a.go","start_line":1,"end_line":80}`
				case 2:
					expectedPath = "pkg/a.go"
					historyReadReplies[expectedPath] = true
					reply = "<tool_call>\n" + `{"tool":"history_range","path":"pkg/a.go"}`
				case 3:
					expectedPath = "pkg/a.go"
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/a.go.","edits":[{"path":"pkg/a.go","old_text":"const A = \"old\"","new_text":"const A = \"new\"","purpose":"replace A fixture text"}]}`
				case 4:
					expectedPath = "pkg/b.go"
					sourceReadReplies[expectedPath] = true
					reply = "<tool_call>\n" + `{"tool":"read_file","path":"pkg/b.go","start_line":1,"end_line":80}`
				case 5:
					expectedPath = "pkg/b.go"
					historyReadReplies[expectedPath] = true
					reply = "<tool_call>\n" + `{"tool":"history_range","path":"pkg/b.go"}`
				case 6:
					expectedPath = "pkg/b.go"
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/b.go.","edits":[{"path":"pkg/b.go","old_text":"const B = \"old\"","new_text":"const B = \"new\"","purpose":"replace B fixture text"}]}`
				case 7:
					expectedPath = "pkg/c.go"
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/c.go.","edits":[{"path":"pkg/c.go","replace_all":true,"old_text":"","new_text":"package pkg\n\nconst C = \"new\"\n","purpose":"create C fixture text"}]}`
				default:
					sawUnexpectedPlannerCall.Store(true)
					reply = "<<QUESTION>>: the atomized planner fixture exceeded its seven model-selected strategy requests."
				}

				if expectedPath != "" {
					gomega.Expect(requestText).To(gomega.ContainSubstring("### .idiom/refine/"), fmt.Sprintf("planner request did not receive a refined atom brief:\n%s", requestText))
					requiredPaths, scopeErr := modelRequestRequiredEditPaths(request.Messages)
					gomega.Expect(scopeErr).NotTo(gomega.HaveOccurred(), fmt.Sprintf("planner request did not receive a typed frontier state:\n%s", requestText))
					gomega.Expect(requiredPaths).To(gomega.Equal([]string{expectedPath}), fmt.Sprintf("planner request mixed atom scope: got %#v, want [%s]", requiredPaths, expectedPath))
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	execution := newDetachedPromptExecutionSession(specCtx, model)
	drainCtx, stopDrain := context.WithCancel(specCtx)
	defer stopDrain()
	go func() {
		for {
			select {
			case <-drainCtx.Done():
				return
			case _, ok := <-execution.events:
				if !ok {
					return
				}
			}
		}
	}()

	answer, err := execution.runPlanExecuteCommandOnce(specCtx, "design.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	gotA, err := os.ReadFile(filepath.Join(root, "pkg", "a.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read a.go: %v", err))

	gotB, err := os.ReadFile(filepath.Join(root, "pkg", "b.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read b.go: %v", err))

	gomega.Expect(string(gotA)).To(gomega.Equal("package pkg\n\nconst A = \"new\"\n"), fmt.Sprintf("a.go content = %q, want updated fixture", string(gotA)))

	gomega.Expect(string(gotB)).To(gomega.Equal("package pkg\n\nconst B = \"new\"\n"), fmt.Sprintf("b.go content = %q, want updated fixture", string(gotB)))

	gotC, err := os.ReadFile(filepath.Join(root, "pkg", "c.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read c.go: %v", err))

	gomega.Expect(string(gotC)).To(gomega.Equal("package pkg\n\nconst C = \"new\"\n"), fmt.Sprintf("c.go content = %q, want created fixture", string(gotC)))

	atomDir := filepath.Join(root, ".idiom", "atoms", implementationdispatch.DocumentOutputTitle("docs/design.md"))
	for _, name := range []string{"atom-001.md", "atom-002.md", "atom-003.md"} {
		{
			_, err := os.Stat(filepath.Join(atomDir, name))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected materialized atom %s: %v", name, err))
		}
	}

	firstAtom, err := os.ReadFile(filepath.Join(atomDir, "atom-001.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read first atom: %v", err))

	gomega.Expect(string(firstAtom)).To(gomega.ContainSubstring("Host current-state anchors captured before dispatch"), fmt.Sprintf("existing-path atom should carry host exact anchors in Verified Current State:\n%s", string(firstAtom)))
	gomega.Expect(string(firstAtom)).To(gomega.ContainSubstring("pkg/a.go:3"), fmt.Sprintf("existing-path atom should carry host exact anchors in Verified Current State:\n%s", string(firstAtom)))
	gomega.Expect(string(firstAtom)).To(gomega.ContainSubstring(`const A = "old"`), fmt.Sprintf("existing-path atom should carry host exact anchors in Verified Current State:\n%s", string(firstAtom)))

	for _, want := range []string{
		"Status: source-satisfied - do not dispatch",
		"exact `old_text`/`new_text` blocks",
		"Do not use prose-only mutation sites",
	} {
		gomega.Expect(strings.Contains(string(firstAtom), want)).To(gomega.BeTrue(), fmt.Sprintf("existing-path atom should carry dispatch contract %q:\n%s", want, string(firstAtom)))
	}

	thirdAtom, err := os.ReadFile(filepath.Join(atomDir, "atom-003.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read third atom: %v", err))

	gomega.Expect(strings.Contains(string(thirdAtom), "NEW FILE:")).To(gomega.BeTrue(), fmt.Sprintf("missing-path atom should be marked NEW FILE:\n%s", string(thirdAtom)))

	gomega.Expect(string(thirdAtom)).To(gomega.ContainSubstring("Typed plan execution contract:"), fmt.Sprintf("missing-path atom should carry typed new-file authority from parent doc:\n%s", string(thirdAtom)))
	gomega.Expect(string(thirdAtom)).To(gomega.ContainSubstring(`"authorized_new_files":[{"path":"pkg/c.go"}]`), fmt.Sprintf("missing-path atom should carry typed new-file authority from parent doc:\n%s", string(thirdAtom)))

	gomega.Expect(strings.Contains(answer, "Design document implementation completed through 3 atom(s)")).To(gomega.BeTrue(), fmt.Sprintf("expected atomized completion answer, got %q", answer))

	gomega.Expect(sawAtomPrompt.Load()).To(gomega.BeTrue(), fmt.Sprintf("expected planner to receive atom prompt"))
	gomega.Expect(sawUnexpectedPlannerCall.Load()).To(gomega.BeFalse(), fmt.Sprintf("atomized planner exceeded its finite model-selected trajectory"))
	gomega.Expect(sawUnexpectedRecovery.Load()).To(gomega.BeFalse(), fmt.Sprintf("atomized planner unexpectedly entered outer recovery"))

	gomega.Expect(sourceReadReplies).To(gomega.Equal(map[string]bool{"pkg/a.go": true, "pkg/b.go": true}), fmt.Sprintf("expected source evidence only for established atom paths, got %#v", sourceReadReplies))
	gomega.Expect(historyReadReplies).To(gomega.Equal(map[string]bool{"pkg/a.go": true, "pkg/b.go": true}), fmt.Sprintf("expected history evidence only for established atom paths, got %#v", historyReadReplies))
	gomega.Expect(plannerPromptRequests.Load()).To(gomega.BeNumerically("==", 7), fmt.Sprintf("expected source-read, history, and submit requests for established atoms plus one new-file submit, got %d", plannerPromptRequests.Load()))
})

var _ = ginkgo.It("run implementation plan documents source satisfied atom skips refine and plan execute", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "events.go"), []byte(strings.Join([]string{
			"package pkg",
			"",
			"func emitRoute(stdout any, fields map[string]any) {",
			"	emitJSONEvent(stdout, \"writer_route_decision\", fields)",
			"}",
			"",
			"func eventType(eventType string) string {",
			"	return \"idiom.pkg.\" + eventType",
			"}",
			"",
		}, "\n")), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write events: %v", err))
	}

	var refineCalls atomic.Int64

	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		refineCalls.Add(1)

		return implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path, ExitCode: 1, Stderr: "refine should not run"}
	})

	var preflightCalls atomic.Int64

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		preflightCalls.Add(1)

		return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
	})

	atomContent := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: pending",
		"",
		"<!-- idiom:design-doc-atom source=\"docs/design.md\" index=1 total=1 -->",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/events.go | Emit `idiom.pkg.writer_route_decision` for direct runs |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `pkg/events.go:4`: `emitJSONEvent(stdout, \"writer_route_decision\", fields)`",
		"  - `pkg/events.go:8`: `return \"idiom.pkg.\" + eventType`",
		"",
		"## Required Behavior",
		"",
		"Implement only this atom's bounded slice for `pkg/events.go`.",
		"",
		"Source scope row:",
		"",
		"> | `pkg/events.go` | Emit `idiom.pkg.writer_route_decision` for direct runs |",
		"",
	}, "\n")
	doc := implementationdispatch.PlanDocument{
		Path:      ".idiom/atoms/design/atom-001.md",
		Content:   atomContent,
		EditPaths: []string{"pkg/events.go"},
	}

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementationPlanDocumentsViaPlanExecute: %v", err))

	gomega.Expect(answer).To(gomega.ContainSubstring("Decision: source-satisfied"), fmt.Sprintf("expected source-satisfied evidence answer, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("writer_route_decision"), fmt.Sprintf("expected source-satisfied evidence answer, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("idiom.pkg."), fmt.Sprintf("expected source-satisfied evidence answer, got:\n%s", answer))

	gomega.Expect(refineCalls.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("refine should not run for source-satisfied atom, got %d call(s)", refineCalls.Load()))

	gomega.Expect(preflightCalls.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("preflight should not run before source-satisfied short-circuit, got %d call(s)", preflightCalls.Load()))
})

var _ = ginkgo.It("run implementation plan documents source satisfied atom marks atom complete", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "events.go"), []byte(strings.Join([]string{
			"package pkg",
			"",
			"func emitRoute(stdout any, fields map[string]any) {",
			"	emitJSONEvent(stdout, \"writer_route_decision\", fields)",
			"}",
			"",
		}, "\n")), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write events: %v", err))
	}

	atomContent := strings.Join([]string{
		"# design atom 001",
		"",
		"Status: pending",
		"",
		"<!-- idiom:design-doc-atom source=\"docs/design.md\" index=1 total=1 -->",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/events.go | Emit `writer_route_decision` for direct runs |",
		"",
		"## Verified Current State",
		"",
		"- Host current-state anchors captured before dispatch:",
		"  - `pkg/events.go:4`: `emitJSONEvent(stdout, \"writer_route_decision\", fields)`",
		"",
		"## Required Behavior",
		"",
		"Source scope row:",
		"",
		"> | `pkg/events.go` | Emit `writer_route_decision` for direct runs |",
		"",
	}, "\n")
	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	{
		err := os.MkdirAll(filepath.Dir(atomAbs), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	{
		err := os.WriteFile(atomAbs, []byte(atomContent), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write atom: %v", err))
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("atom doc should be readable"))

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.runImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runImplementationPlanDocumentsViaPlanExecute: %v", err))

	gomega.Expect(strings.Contains(answer, "Decision: source-satisfied")).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied answer, got:\n%s", answer))

	updatedBody, err := os.ReadFile(atomAbs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read updated atom: %v", err))

	updated := string(updatedBody)
	gomega.Expect(updated).To(gomega.ContainSubstring("Status: source-satisfied - do not dispatch"), fmt.Sprintf("source-satisfied atom doc should be closed with result evidence:\n%s", updated))
	gomega.Expect(updated).To(gomega.ContainSubstring("## Result"), fmt.Sprintf("source-satisfied atom doc should be closed with result evidence:\n%s", updated))
	gomega.Expect(updated).To(gomega.ContainSubstring("writer_route_decision"), fmt.Sprintf("source-satisfied atom doc should be closed with result evidence:\n%s", updated))
})

var _ = ginkgo.It("implementation dispatch source satisfied uses current state anchor concepts", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	files := map[string]string{
		"internal/contracts/writerroute/decision.go": strings.Join([]string{
			"package writerroute",
			"",
			"// SchemaVersion is the current version of the WriterRouteDecision schema.",
			"const SchemaVersion = \"idiom.writer_route.v1\"",
			"",
			"type Decision struct {",
			"	RouteID string",
			"}",
			"",
			"func (d *Decision) EventFields() map[string]interface{} {",
			"	return nil",
			"}",
			"",
		}, "\n"),
		"internal/contracts/writerroute/decision_test.go": strings.Join([]string{
			"package writerroute",
			"",
			"// TestWriterRouteDecision_NormalizationAndStableID verifies string normalization,",
			"func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}",
			"",
			"// TestWriterRouteDecision_EventFields verifies the EventFields mapping and defaults.",
			"",
		}, "\n"),
		"internal/surfaces/tui/app/writer_route.go": strings.Join([]string{
			"package app",
			"",
			"func (m *Model) writerRouteDecisionForPrompt() writerroute.Decision {",
			"	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{})",
			"}",
			"",
			"func (m *Model) emitWriterRouteDecision(decision writerroute.Decision) {",
			"	fields[\"type\"] = \"writer_route_decision\"",
			"}",
			"",
		}, "\n"),
		"internal/surfaces/tui/app/implementation_dispatch.go": strings.Join([]string{
			"package app",
			"",
			"func shouldDispatchImplementationToPlanExecute() bool { return false }",
			"func appendImplementationDispatchPlanDocuments() {}",
			"",
		}, "\n"),
	}

	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		{
			err := os.MkdirAll(filepath.Dir(full), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", path, err))
		}

		{
			err := os.WriteFile(full, []byte(content), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
		}
	}

	docs := []implementationdispatch.PlanDocument{
		sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-001.md", "internal/contracts/writerroute/decision.go", "Domain type, normalization, stable ID, event fields", []string{
			"`internal/contracts/writerroute/decision.go:3`: `// SchemaVersion is the current version of the WriterRouteDecision schema.`",
			"`internal/contracts/writerroute/decision.go:6`: `type Decision struct {`",
			"`internal/contracts/writerroute/decision.go:10`: `func (d *Decision) EventFields() map[string]interface{} {`",
		}),
		sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-002.md", "internal/contracts/writerroute/decision_test.go", "Unit tests for normalization, stable ID, JSON/event fields", []string{
			"`internal/contracts/writerroute/decision_test.go:3`: `// TestWriterRouteDecision_NormalizationAndStableID verifies string normalization,`",
			"`internal/contracts/writerroute/decision_test.go:4`: `func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}`",
			"`internal/contracts/writerroute/decision_test.go:6`: `// TestWriterRouteDecision_EventFields verifies the EventFields mapping and defaults.`",
		}),
		sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-007.md", "internal/surfaces/tui/app/writer_route.go", "Build and emit TUI route decision after `buildPromptExecutionPlan`", []string{
			"`internal/surfaces/tui/app/writer_route.go:3`: `func (m *Model) writerRouteDecisionForPrompt() writerroute.Decision {`",
			"`internal/surfaces/tui/app/writer_route.go:4`: `return writerroute.NewDecisionFromParams(writerroute.DecisionParams{})`",
			"`internal/surfaces/tui/app/writer_route.go:7`: `func (m *Model) emitWriterRouteDecision(decision writerroute.Decision) {`",
		}),
		sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-008.md", "internal/surfaces/tui/app/implementation_dispatch.go", "If needed, pass or annotate dispatch route without changing behavior", []string{
			"`internal/surfaces/tui/app/implementation_dispatch.go:3`: `func shouldDispatchImplementationToPlanExecute() bool { return false }`",
		}),
	}
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	records := implementationdispatch.PlanDocumentsSourceSatisfied(root, docs)
	gomega.Expect(len(records)).To(gomega.Equal(len(docs)), fmt.Sprintf("records = %#v, want one source-satisfied record per atom", records))

	for _, record := range records {
		gomega.Expect(record.EditPath).To(gomega.Not(gomega.Equal("")), fmt.Sprintf("record should carry path evidence: %#v", record))
		gomega.Expect(len(record.Evidence)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("record should carry path evidence: %#v", record))
	}
})

var _ = ginkgo.It("implementation dispatch source satisfied uses current source body when anchors are stale", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := "internal/contracts/writerroute/decision_test.go"
	full := filepath.Join(root, filepath.FromSlash(path))

	{
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir target: %v", err))
	}

	body := strings.Join([]string{
		"package writerroute",
		"",
		"func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {",
		"	t.Fatal(\"covers normalization stable id\")",
		"}",
		"",
		"func TestWriterRouteDecision_JSONEventFieldMapping(t *testing.T) {",
		"	t.Fatal(\"covers JSON event fields\")",
		"}",
		"",
	}, "\n")
	{
		err := os.WriteFile(full, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-002.md", path, "Unit tests for normalization, stable ID, JSON/event fields", []string{
		"`internal/contracts/writerroute/decision_test.go:8`: `// stale comment anchor no longer present`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
	gomega.Expect(len(records)).To(gomega.Equal(1), fmt.Sprintf("records = %#v, want source-satisfied from current source body", records))

	gomega.Expect(strings.Contains(strings.Join(records[0].Evidence, "\n"), "current source covers requested source-scope term")).To(gomega.BeTrue(), fmt.Sprintf("expected source-body evidence, got %#v", records[0].Evidence))
})

var _ = ginkgo.It("implementation dispatch source satisfied does not treat fallback anchors as done", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n\nconst A = \"old\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write pkg/a.go: %v", err))
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-001.md", "pkg/a.go", "Replace A's fixture value with new", []string{
		"`pkg/a.go:1`: `package pkg`",
		"`pkg/a.go:3`: `const A = \"old\"`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	{
		records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
		gomega.Expect(len(records)).To(gomega.Equal(0), fmt.Sprintf("fallback/current old anchors must not source-satisfy pending work: %#v", records))
	}
})

var _ = ginkgo.It("implementation dispatch optional source satisfied requires live anchor", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := "internal/surfaces/tui/app/implementation_dispatch.go"
	full := filepath.Join(root, filepath.FromSlash(path))

	{
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir target: %v", err))
	}

	{
		err := os.WriteFile(full, []byte(strings.Join([]string{
			"package app",
			"",
			"type planDocumentFixture struct {",
			"	Path string",
			"}",
			"",
		}, "\n")), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-008.md", path, "If needed, pass or annotate dispatch route without changing behavior", []string{
		"`internal/surfaces/tui/app/implementation_dispatch.go:3`: `func staleAnchor() {}`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	{
		records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
		gomega.Expect(len(records)).To(gomega.Equal(0), fmt.Sprintf("optional source-satisfied must require a live current-state anchor, got %#v", records))
	}
})

func sourceSatisfiedAtomDoc(docPath string, editPath string, change string, anchors []string) implementationdispatch.PlanDocument {
	var b strings.Builder

	b.WriteString("# Atom\n\n")
	b.WriteString("Status: pending\n\n")
	b.WriteString("<!-- idiom:design-doc-atom source=\"docs/design.md\" index=1 total=1 -->\n\n")
	b.WriteString("## Files To Modify\n\n")
	b.WriteString("| File | Change |\n|---|---|\n")
	b.WriteString("| ")
	b.WriteString(editPath)
	b.WriteString(" | ")
	b.WriteString(change)
	b.WriteString(" |\n\n")
	b.WriteString("## Verified Current State\n\n")
	b.WriteString("- Host current-state anchors captured before dispatch:\n")

	for _, anchor := range anchors {
		b.WriteString("  - ")
		b.WriteString(anchor)
		b.WriteString("\n")
	}

	b.WriteString("\n## Required Behavior\n\n")
	b.WriteString("Source scope row:\n\n")
	b.WriteString("> | `")
	b.WriteString(editPath)
	b.WriteString("` | ")
	b.WriteString(change)
	b.WriteString(" |\n")

	return implementationdispatch.PlanDocument{
		Path:      docPath,
		Content:   b.String(),
		EditPaths: []string{editPath},
	}
}

var _ = ginkgo.It("plan execute prompt runner isolates interactive history and pinned files", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "parent-only.txt"), []byte("PARENT_PINNED_SENTINEL_8f32f6\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write parent-only: %v", err))
	}
	initializeTurnPlannerGitRepository(t, root)

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)

			requestNumber := chatRequests.Add(1)
			var request struct {
				Messages []llama.ChatMessage `json:"messages"`
				Tools    []llama.Tool        `json:"tools"`
			}
			gomega.Expect(json.Unmarshal(body, &request)).To(gomega.Succeed())

			for _, forbidden := range []string{
				"PARENT_HISTORY_SENTINEL_24d4f8",
				"PARENT_ASSISTANT_SENTINEL_977aae",
				"PARENT_PINNED_SENTINEL_8f32f6",
				pinnedFileSnapshotHeaderTag,
			} {
				gomega.Expect(strings.Contains(requestText, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("isolated plan-execute planner request leaked parent TUI state %q:\n%s", forbidden, requestText))
			}

			gomega.Expect(strings.Contains(requestText, "PLANNER_PROMPT_SENTINEL_d66863")).To(gomega.BeTrue(), fmt.Sprintf("planner request missing isolated prompt sentinel:\n%s", requestText))

			var reply string
			toolNames := make([]string, 0, len(request.Tools))
			for _, tool := range request.Tools {
				toolNames = append(toolNames, tool.Function.Name)
			}

			switch requestNumber {
			case 1:
				gomega.Expect(toolNames).To(gomega.ContainElement(workspace.ToolNameReadFile))
				reply = `<tool_call>
{"tool":"read_file","path":"target.txt","start_line":1,"end_line":1,"named_gap":"current target content before planning the replacement"}`
			case 2:
				gomega.Expect(toolNames).To(gomega.ContainElements(workspace.ToolNameHistoryRange, workspace.ToolNameSubmitPlan, workspace.ToolNameReadFile))
				reply = `<tool_call>
{"tool":"history_range","path":"target.txt","start_line":1,"end_line":1,"named_gap":"history rationale for the established target"}`
			case 3:
				gomega.Expect(toolNames).To(gomega.ContainElements(workspace.ToolNameSubmitPlan, workspace.ToolNameReadFile))
				reply = `<tool_call>
{"tool":"submit_plan","rationale":"Update target.txt from the isolated planner prompt after reading current source and history.","edits":[{"path":"target.txt","start_line":1,"end_line":1,"content":"new","purpose":"replace the target fixture text"}]}`
			case 4:
				var transcript []string
				for _, message := range request.Messages {
					if message.Role == "assistant" || message.Role == "tool" || strings.Contains(message.Content, "tool_result") {
						transcript = append(transcript, message.Role+": "+message.Content)
					}
				}
				conversation := strings.Join(transcript, "\n")
				gomega.Expect(conversation).To(gomega.ContainSubstring("tool: submit_plan"), fmt.Sprintf("planner final-answer request should include the submit_plan result:\n%s", conversation))
				gomega.Expect(conversation).To(gomega.ContainSubstring("status: ok"), fmt.Sprintf("planner final-answer request should include a successful submit_plan result:\n%s", conversation))
				reply = "Submitted the plan to update target.txt after reading its current content."
			default:
				ginkgo.Fail(fmt.Sprintf("unexpected isolated planner request %d after the model-authored final answer:\n%s", requestNumber, requestText), 1)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.Workspace.SessionID = "test-plan-execute-isolation"
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	model.messages = []llama.ChatMessage{
		{Role: "user", Content: "PARENT_HISTORY_SENTINEL_24d4f8"},
		{Role: "assistant", Content: "PARENT_ASSISTANT_SENTINEL_977aae"},
	}
	model.lastAssistantReasoningContent = "parent reasoning"

	execution := newDetachedPromptExecutionSession(context.Background(), model)
	drainCtx, stopDrain := context.WithCancel(context.Background())
	defer stopDrain()
	go func() {
		for {
			select {
			case <-drainCtx.Done():
				return
			case _, ok := <-execution.events:
				if !ok {
					return
				}
			}
		}
	}()

	parentPins := execution.pinnedFilesRegistry()
	parentPins.Pin("parent-only.txt")

	runner := execution.newPlanExecutePromptRunner(context.Background())

	answer, err := runner(cfg, "PLANNER_PROMPT_SENTINEL_d66863 update target.txt", planexecute.RunOptions{
		TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
		PlannerOwnedVerification: true,
		MaxTokensOverride:        1024,
		ScopeContract:            plancontract.NewPlanScopeContract([]string{"target.txt"}, nil, nil),
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt runner: %v\nanswer: %s", err, answer))

	gomega.Expect(chatRequests.Load()).To(gomega.BeNumerically("==", 4), fmt.Sprintf("isolated planner requests = %d, want read/history/submit/final", chatRequests.Load()))

	gomega.Expect(len(execution.messages)).To(gomega.Equal(2), fmt.Sprintf("parent messages were not restored: %#v", execution.messages))
	gomega.Expect(execution.messages[0].Content).To(gomega.Equal("PARENT_HISTORY_SENTINEL_24d4f8"), fmt.Sprintf("parent messages were not restored: %#v", execution.messages))
	gomega.Expect(execution.messages[1].Content).To(gomega.Equal("PARENT_ASSISTANT_SENTINEL_977aae"), fmt.Sprintf("parent messages were not restored: %#v", execution.messages))

	gomega.Expect(execution.lastAssistantReasoningContent).To(gomega.Equal("parent reasoning"), fmt.Sprintf("parent reasoning was not restored: %q", execution.lastAssistantReasoningContent))

	gomega.Expect(execution.pinnedFiles).To(gomega.Equal(parentPins), fmt.Sprintf("parent pinned-file registry was not restored"))
})

var _ = ginkgo.It("plan execute planner thinking policy disables local thinking only", func() {
	thinkingOn := true
	qwen := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		backendRuntimeState: backendRuntimeState{modelID: "qwen3.6-35b"},
	}

	gomega.Expect(newDetachedPromptExecutionSession(context.Background(), qwen).shouldDisableThinkingForPlanExecutePlanner()).To(gomega.BeTrue(), fmt.Sprintf("local Qwen planner should disable thinking for strict plan-execute tool emission"))

	hosted := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		backendRuntimeState: backendRuntimeState{
			modelID:            "glm-5.1",
			modelThinkingFloor: true,
		},
	}
	gomega.Expect(newDetachedPromptExecutionSession(context.Background(), hosted).shouldDisableThinkingForPlanExecutePlanner()).To(gomega.BeFalse(), fmt.Sprintf("thinking-floor hosted planner should preserve thinking"))
})

var _ = ginkgo.It("plan execute prompt runner route preserves hosted executor thinking floor", func() {
	controller := gomock.NewController(ginkgo.GinkgoT())
	executor := llamamocks.NewMockLLMClient(controller)
	executor.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "glm-5.1"}}, nil).Times(1)
	executorSpec := config.LlamaServer{Kind: config.BackendZAI, Model: "glm-5.1", BaseURL: "https://api.z.ai/api/paas/v4"}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client:              llama.NewClient("http://planner.invalid", time.Second),
		implementerClient:   executor,
		plannerSpec:         config.LlamaServer{Model: "local-planner", BaseURL: "http://planner.invalid"},
		executorSpec:        executorSpec,
		backendRuntimeState: backendRuntimeState{modelID: "local-planner"},
	})
	restore := m.applyPlanExecutePromptRunnerRoute(context.Background(), planexecute.RunOptions{})

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprintf("hosted executor route should set modelThinkingFloor during in-process plan-execute"))

	restore()

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprintf("modelThinkingFloor should restore after in-process plan-execute route"))
})

var _ = ginkgo.It("plan execute prompt runner state preserves thinking for hosted executor route", func() {
	controller := gomock.NewController(ginkgo.GinkgoT())
	executor := llamamocks.NewMockLLMClient(controller)
	executor.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "glm-5.1"}}, nil).Times(1)
	executorSpec := config.LlamaServer{Kind: config.BackendZAI, Model: "glm-5.1", BaseURL: "https://api.z.ai/api/paas/v4"}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client:              llama.NewClient("http://planner.invalid", time.Second),
		implementerClient:   executor,
		plannerSpec:         config.LlamaServer{Model: "local-planner", BaseURL: "http://planner.invalid"},
		executorSpec:        executorSpec,
		backendRuntimeState: backendRuntimeState{modelID: "local-planner"},
	})
	restoreRoute := m.applyPlanExecutePromptRunnerRoute(context.Background(), planexecute.RunOptions{})
	restoreState := m.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
	}, io.Discard)

	gomega.Expect(m.disableThinkingOverride).To(gomega.BeFalse(), fmt.Sprintf("hosted executor route must not inherit local planner no-thinking override"))

	restoreState()
	restoreRoute()

	gomega.Expect(m.disableThinkingOverride).To(gomega.BeFalse(), fmt.Sprintf("plan-execute state/route should restore, disableThinking=%t floor=%t", m.disableThinkingOverride, m.modelThinkingFloor))
	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprintf("plan-execute state/route should restore, disableThinking=%t floor=%t", m.disableThinkingOverride, m.modelThinkingFloor))
})

var _ = ginkgo.It("plan execute prompt runner state disables local thinking for active model fallback", func() {
	thinkingOn := true
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		backendRuntimeState: backendRuntimeState{modelID: "qwen3.6-35b"},
	})
	restoreState := m.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily:     agent.PlanThenExecutePlannerTaskFamily,
		UseActiveModel: true,
	}, io.Discard)

	gomega.Expect(m.disableThinkingOverride).To(gomega.BeTrue(), fmt.Sprintf("active-model fallback should still disable local thinking for strict plan-execute tool emission"))

	restoreState()

	gomega.Expect(m.disableThinkingOverride).To(gomega.BeFalse(), fmt.Sprintf("plan-execute state should restore disableThinking=false"))
})

var _ = ginkgo.It("plan execute prompt runner uses executor route when available", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	var plannerChat atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "planner-model"}}})
		case "/v1/chat/completions":
			plannerChat.Add(1)

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "planner route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChat atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "executor-model"}}})
		case "/v1/chat/completions":
			executorChat.Add(1)

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "executor route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))

	defer executorServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"planner-model","model":"planner-model","base_url":%q,"roles":["planner"]},
		{"alias":"executor-model","model":"executor-model","base_url":%q,"roles":["executor"]}
	]}}}`, plannerServer.URL, executorServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends: %v", err))
	}

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = plannerServer.URL
	cfg.LlamaServer.Model = "planner-model"
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	model.modelID = "planner-model"
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt runner: %v", err))

	gomega.Expect(strings.TrimSpace(answer)).To(gomega.Equal("executor route"), fmt.Sprintf("answer = %q, want executor route", answer))

	gomega.Expect(executorChat.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected executor chat request"))

	gomega.Expect(plannerChat.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("plan-execute prompt runner should not use planner chat when executor is reachable, got %d", plannerChat.Load()))

	gomega.Expect(execution.modelID).To(gomega.Equal("planner-model"), fmt.Sprintf("modelID was not restored after prompt runner: %q", execution.modelID))
})

var _ = ginkgo.It("plan execute prompt runner use active model skips executor route", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	var plannerChat atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "planner-model"}}})
		case "/v1/chat/completions":
			plannerChat.Add(1)

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "planner route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChat atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "executor-model"}}})
		case "/v1/chat/completions":
			executorChat.Add(1)

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "executor route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))

	defer executorServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"planner-model","model":"planner-model","base_url":%q,"roles":["planner"]},
		{"alias":"executor-model","model":"executor-model","base_url":%q,"roles":["executor"]}
	]}}}`, plannerServer.URL, executorServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends: %v", err))
	}

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = plannerServer.URL
	cfg.LlamaServer.Model = "planner-model"
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	model.modelID = "planner-model"
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
		UseActiveModel:    true,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt runner: %v", err))

	gomega.Expect(strings.TrimSpace(answer)).To(gomega.Equal("planner route"), fmt.Sprintf("answer = %q, want planner route", answer))

	gomega.Expect(plannerChat.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected planner chat request"))

	gomega.Expect(executorChat.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("active-model fallback should not use executor chat, got %d", executorChat.Load()))

	gomega.Expect(execution.modelID).To(gomega.Equal("planner-model"), fmt.Sprintf("modelID was not restored after prompt runner: %q", execution.modelID))
})

var _ = ginkgo.It("implementation dispatch plan execute hosted model override", func() {
	model := newDetachedPromptExecutionSession(context.Background(), &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{
				Kind:  "deepseek",
				Model: "deepseek-v4-flash",
			},
		},
		plannerSpec: config.LlamaServer{
			Kind:  "deepseek",
			Model: "deepseek-v4-flash",
		},
		backendRuntimeState: backendRuntimeState{modelID: "deepseek-v4-flash"},
	})

	{
		got := model.implementationDispatchPlanExecuteModelOverride()
		gomega.Expect(got).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("hosted model override = %q, want deepseek-v4-flash", got))
	}

	model.plannerSpec = config.LlamaServer{Kind: "llama.cpp", Model: "qwen3.6-35b"}
	model.modelID = "qwen3.6-35b"
	{
		got := model.implementationDispatchPlanExecuteModelOverride()
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("local model should preserve plan-execute role routing, got override %q", got))
	}
})

var _ = ginkgo.It("use config planner model bypasses planner role alias", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	var aliasPlannerChat atomic.Int64
	aliasPlannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "local-planner"}}})
		case "/v1/chat/completions":
			aliasPlannerChat.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "alias route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer aliasPlannerServer.Close()

	var (
		configPlannerChat  atomic.Int64
		configPlannerModel atomic.Value
	)
	configPlannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-v4-flash"}}})
		case "/v1/chat/completions":
			configPlannerChat.Add(1)

			var body struct {
				Model string `json:"model"`
			}
			{
				err := json.NewDecoder(r.Body).Decode(&body)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode chat body: %v", err))
			}
			configPlannerModel.Store(body.Model)

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "config route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer configPlannerServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"local-planner","model":"local-planner","base_url":%q,"roles":["planner"]}
	]}}}`, aliasPlannerServer.URL)
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends: %v", err))
	}

	cfg := config.Default()
	cfg.LlamaServer.BaseURL = configPlannerServer.URL
	cfg.LlamaServer.Model = "deepseek-v4-flash"
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()

	model.UseConfigPlannerModel()
	execution := newDetachedPromptExecutionSession(context.Background(), model)

	answer, err := execution.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
		UseActiveModel:    true,
	}, io.Discard)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prompt runner: %v", err))

	gomega.Expect(strings.TrimSpace(answer)).To(gomega.Equal("config route"), fmt.Sprintf("answer = %q, want config route", answer))

	gomega.Expect(configPlannerChat.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected config planner chat request"))

	gomega.Expect(aliasPlannerChat.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("explicit planner model should bypass role alias, got %d alias chats", aliasPlannerChat.Load()))

	{
		got, _ := configPlannerModel.Load().(string)
		gomega.Expect(got).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("config planner request model = %q, want deepseek-v4-flash", got))
	}
})

var _ = ginkgo.It("run prompt once plan execute creates typed authorized new file after a model-selected missing-parent inspection", ginkgo.SpecTimeout(60*time.Second), func(specCtx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| internal/newpkg/decision.go | create a small decision helper |",
		"",
		"Typed plan execution contract:",
		`{"authorized_new_files":[{"path":"internal/newpkg/decision.go"}]}`,
		"Typed plan authority projection:",
		`{"atom_id":"atom-1"}`,
		"",
		"## Implementation Notes",
		"",
		"Create a Go package named newpkg with a Decision function returning ok.",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	var (
		sawUnexpectedRetry   atomic.Bool
		sawUnexpectedRequest atomic.Bool
		plannerRequestCount  atomic.Int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Plan-Execute Planner Blocked Recovery"):
				sawUnexpectedRetry.Store(true)
				reply = `{"decision":"surface","reason":"typed authorized-new planning should not require an outer retry","message":"unexpected planner recovery"}`
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- internal/newpkg/decision.go exists with the requested helper.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				requestNumber := plannerRequestCount.Add(1)

				gomega.Expect(requestText).To(gomega.ContainSubstring("authorized_new_files"), fmt.Sprintf("planner request missing typed authorized-new contract:\n%s", requestText))
				gomega.Expect(requestText).To(gomega.ContainSubstring("- internal/newpkg/decision.go"), fmt.Sprintf("planner request missing required new-file scope:\n%s", requestText))

				switch requestNumber {
				case 1:
					reply = "<tool_call>\n" + `{"tool":"go_file_outline","path":"internal/newpkg"}`
				case 2:
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Create the new package file from the referenced plan document.","edits":[{"path":"internal/newpkg/decision.go","replace_all":true,"old_text":"","new_text":"package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n","purpose":"create the decision helper"}]}`
				default:
					sawUnexpectedRequest.Store(true)
					reply = "<<QUESTION>>: the authorized new-file planner fixture exceeded its two model-selected requests."
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"

	thinkingOn := true

	cfg.Chat.EnableThinking = &thinkingOn
	cfg.Chat.PreserveThinking = true
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(specCtx, model).runPlanExecuteCommandOnce(specCtx, "docs/plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	got, err := os.ReadFile(filepath.Join(root, "internal", "newpkg", "decision.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read generated file: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n"), fmt.Sprintf("generated file content = %q", string(got)))

	gomega.Expect(sawUnexpectedRetry.Load()).To(gomega.BeFalse(), fmt.Sprintf("typed-authorized new file should not require an outer planner retry"))
	gomega.Expect(sawUnexpectedRequest.Load()).To(gomega.BeFalse(), fmt.Sprintf("typed-authorized new-file planner exceeded its finite request trajectory"))

	gomega.Expect(plannerRequestCount.Load()).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected one model-selected inspection followed by one fresh submit request, got %d planner request(s)", plannerRequestCount.Load()))
})

var _ = ginkgo.It("plan execute missing new file failure surfaces authorization message", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	planPath := writeScopeIntakeApprovedProgressPlanArtifact(t, root, []planartifact.PlanEdit{{
		Path:       "src/pages/products.astro",
		NewText:    "products\n",
		ReplaceAll: true,
		Purpose:    "Create products page",
	}})
	body, err := os.ReadFile(planPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan artifact: %v", err))
	var artifact planartifact.PlanArtifact
	{
		err := json.Unmarshal(body, &artifact)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode plan artifact: %v", err))
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll
	updated, err := json.MarshalIndent(artifact, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("encode plan artifact: %v", err))
	{
		err := os.WriteFile(planPath, append(updated, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan artifact: %v", err))
	}

	model := &Model{cfg: cfg}
	answer, ok := newDetachedPromptExecutionSession(context.Background(), model).humanPlanExecuteMissingNewFileAuthorizationMessage(planexecute.RunResult{
		SessionID:   "scope-intake-test-session",
		FailureKind: string(plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth),
	}, nil)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected missing new-file failure to surface authorization message"))

	for _, want := range []string{
		"Plan-execute needs approval before creating new files.",
		"src/pages/products.astro",
		"authorized_new_files",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("authorization response missing %q:\n%s", want, answer))
	}
	gomega.Expect(strings.Contains(answer, "src/pages/index.astro")).To(gomega.BeFalse(), fmt.Sprintf("existing changed path should not require new-file authorization:\n%s", answer))
})

var _ = ginkgo.It("run prompt once plan execute planner blocked can surface through recovery decision", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | update target text |",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	var sawRecoveryDecision atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Plan-Execute Planner Blocked Recovery"):
				sawRecoveryDecision.Store(true)

				reply = `{"decision":"surface","reason":"same-model retry is unlikely after no submit_plan tool call","message":"Planner failed before submitting a plan. Please refine the brief into exact implementation edits or retry with a stronger planner."}`
			case strings.Contains(requestText, "The completion gate rejected a final answer before the plan-submission contract"):
				reply = "I still cannot proceed."
			default:
				reply = "I can do this."
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce should surface planner blocker as an assistant answer, got err=%v answer=%q", err, answer))

	gomega.Expect(sawRecoveryDecision.Load()).To(gomega.BeTrue(), fmt.Sprintf("expected planner_blocked failure to be consumed by recovery decision model"))

	gomega.Expect(strings.Contains(answer, "Planner failed before submitting a plan")).To(gomega.BeTrue(), fmt.Sprintf("expected human-facing recovery message, got %q", answer))

	gomega.Expect(strings.Contains(answer, "Chat failed")).To(gomega.BeFalse(), fmt.Sprintf("planner blocker should not be surfaced as transport failure, got %q", answer))
})

func modelRequestRequiredEditPaths(messages []llama.ChatMessage) ([]string, error) {
	const frontierMarker = "[frontier step instruction]"

	var parseErr error
	for i := len(messages) - 1; i >= 0; i-- {
		content := strings.TrimSpace(messages[i].Content)
		markerOffset := strings.Index(content, frontierMarker)
		if markerOffset < 0 {
			continue
		}

		frontier := content[markerOffset+len(frontierMarker):]
		jsonStart := strings.Index(frontier, "{")
		if jsonStart < 0 {
			continue
		}

		var instruction workerruntime.StepInstruction
		decoder := json.NewDecoder(strings.NewReader(frontier[jsonStart:]))
		if err := decoder.Decode(&instruction); err != nil {
			parseErr = err
			continue
		}

		return append([]string(nil), instruction.CurrentState.RequiredEditPaths...), nil
	}

	if parseErr != nil {
		return nil, fmt.Errorf("decode frontier step instruction: %w", parseErr)
	}

	return nil, fmt.Errorf("frontier step instruction missing")
}

func planExecuteSourceReadFixtureReply(requestText string, seen map[string]bool, paths ...string) (string, bool) {
	lower := strings.ToLower(requestText)
	if !strings.Contains(lower, "source-body") &&
		!strings.Contains(lower, "source body") &&
		!strings.Contains(lower, "required source read pending") &&
		!strings.Contains(lower, "candidate_source_pending") {

		return "", false
	}

	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || !strings.Contains(requestText, path) {
			continue
		}

		if seen != nil && seen[path] {
			continue
		}

		if strings.Contains(requestText, "Read f1 "+path) ||
			strings.Contains(requestText, "read_file failed for "+path) ||
			strings.Contains(requestText, "path \""+path+"\" is in your required_edit_paths but does not exist") {

			continue
		}

		if seen != nil {
			seen[path] = true
		}

		return "<tool_call>\n" + fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":80}`, path), true
	}

	return "", false
}

var _ = ginkgo.It("run prompt once plan execute planner blocked can retry after recovery decision", ginkgo.SpecTimeout(90*time.Second), func(specCtx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Plan",
		"",
		"## Expected File Scope",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | update target text |",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}
	initializeTurnPlannerGitRepository(t, root)

	var (
		sawRecoveryDecision atomic.Bool
		sawRetryPrompt      atomic.Bool
		firstPlannerReplies atomic.Int64
	)

	sourceReadReplies := map[string]bool{}
	historyRead := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- target.txt contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "Plan-Execute Planner Blocked Recovery"):
				sawRecoveryDecision.Store(true)

				reply = `{"decision":"retry","reason":"the brief has enough exact target scope and the previous attempt failed before tool use","retry_addendum":"Respond with a submit_plan tool call only; do not narrate completion."}`
			case strings.Contains(requestText, "Previous Planner Blocker Recovery"):
				sawRetryPrompt.Store(true)

				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, "target.txt"); ok {
					reply = readReply
				} else if !historyRead {
					historyRead = true
					reply = "<tool_call>\n" + `{"tool":"history_range","path":"target.txt"}`
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Update target.txt after recovery retry.","edits":[{"path":"target.txt","old_text":"old","new_text":"new","purpose":"replace target text"}]}`
				}
			case strings.Contains(requestText, "The completion gate rejected a final answer before the plan-submission contract"):
				reply = "Still no plan."
			default:
				firstPlannerReplies.Add(1)

				reply = "I can do this."
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	execution := newDetachedPromptExecutionSession(specCtx, model)
	drainCtx, stopDrain := context.WithCancel(specCtx)
	defer stopDrain()
	go func() {
		for {
			select {
			case <-drainCtx.Done():
				return
			case _, ok := <-execution.events:
				if !ok {
					return
				}
			}
		}
	}()

	answer, err := execution.runPlanExecuteCommandOnce(specCtx, "docs/plan.md")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	gomega.Expect(sawRecoveryDecision.Load()).To(gomega.BeTrue(), fmt.Sprintf("expected recovery decision prompt after planner_blocked"))

	gomega.Expect(sawRetryPrompt.Load()).To(gomega.BeTrue(), fmt.Sprintf("expected second plan-execute attempt with recovery addendum"))

	gomega.Expect(firstPlannerReplies.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected visible prose planner reply to block without hidden deterministic retry before recovery decision; got %d planner replies", firstPlannerReplies.Load()))

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read target: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("new\n"), fmt.Sprintf("target content = %q, want new line", string(got)))

	gomega.Expect(strings.Contains(answer, "plan-then-execute summary")).To(gomega.BeTrue(), fmt.Sprintf("expected successful retry summary, got %q", answer))
})

var _ = ginkgo.It("run prompt once plan execute does not infer missing requested test retry", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Hardened() bool { return false }\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst RegressionCovered = false\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write test: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Feature hardening plan",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/feature.go | Harden feature behavior. |",
		"| pkg/feature_test.go | Add regression coverage. |",
		"",
		"## Required Behavior",
		"",
		"Harden the feature behavior and add regression tests proving the edge case is rejected.",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	var (
		sawMissingTestRetry atomic.Bool
		plannerRequests     atomic.Int64
	)

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- pkg/feature.go contains the hardening.\n- pkg/feature_test.go contains regression coverage.\n- No out-of-scope paths were changed."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "Missing Requested Test Edit Recovery"):
				sawMissingTestRetry.Store(true)

				gomega.Expect(strings.Contains(requestText, "pkg/feature_test.go")).To(gomega.BeTrue(), fmt.Sprintf("missing-test retry prompt did not expose test path:\n%s", requestText))

				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Retry with the required regression test edit included.","edits":[{"path":"pkg/feature.go","old_text":"return false","new_text":"return true","purpose":"harden feature behavior"},{"path":"pkg/feature_test.go","old_text":"const RegressionCovered = false","new_text":"const RegressionCovered = true","purpose":"cover the hardening regression"}]}`
			default:
				plannerRequests.Add(1)

				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, "pkg/feature.go", "pkg/feature_test.go"); ok {
					reply = readReply
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Production-only hardening plan that should be rejected before writes.","edits":[{"path":"pkg/feature.go","old_text":"return false","new_text":"return true","purpose":"harden feature behavior"}]}`
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "Implement docs/plan.md: harden the feature behavior and add regression tests proving the edge case is rejected.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v\nanswer: %s", err, answer))

	gomega.Expect(sawMissingTestRetry.Load()).To(gomega.BeFalse(), fmt.Sprintf("prompt prose must not trigger automatic missing-test retry; answer=%s", answer))

	gomega.Expect(plannerRequests.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected initial production-only planner request"))

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read source: %v", err))

	gomega.Expect(strings.Contains(string(gotSource), "return true")).To(gomega.BeTrue(), fmt.Sprintf("source was not hardened:\n%s", string(gotSource)))

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read test: %v", err))

	gomega.Expect(strings.Contains(string(gotTest), "RegressionCovered = true")).To(gomega.BeFalse(), fmt.Sprintf("test should not be updated by a prompt-inferred retry:\n%s", string(gotTest)))

	gomega.Expect(strings.Contains(answer, "plan-then-execute summary")).To(gomega.BeTrue(), fmt.Sprintf("expected successful retry summary, got %q", answer))
})

var _ = ginkgo.It("run prompt once plan execute does not infer missing requested test source satisfied retry", ginkgo.SpecTimeout(60*time.Second), func(specCtx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	{
		err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir pkg: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	sourceBody := "package pkg\n\nfunc Hardened() bool { return true }\n"
	testBody := "package pkg\n\nconst RegressionCovered = true\n"

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte(sourceBody), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte(testBody), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write test: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Feature hardening plan",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|---|---|",
		"| pkg/feature.go | Harden feature behavior. |",
		"| pkg/feature_test.go | Add regression coverage. |",
		"",
		"## Required Behavior",
		"",
		"Harden the feature behavior and add regression tests proving the edge case is rejected.",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	var sawMissingTestRetry atomic.Bool

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "<<QUESTION>>: requested_test_edit_missing for pkg/feature_test.go because the submitted plan changed only pkg/feature.go."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "Missing Requested Test Edit Recovery"):
				sawMissingTestRetry.Store(true)
				reply = "<<QUESTION>>: unexpected prompt-inferred missing-test recovery in the source-satisfied fixture."
			default:
				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, "pkg/feature.go", "pkg/feature_test.go"); ok {
					reply = readReply
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Production-only stale plan that should be rejected before writes.","edits":[{"path":"pkg/feature.go","old_text":"return true","new_text":"return false","purpose":"stale production-only edit"}]}`
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(specCtx, model).runPlanExecuteCommandOnce(specCtx, "Implement docs/plan.md: harden the feature behavior and add regression tests proving the edge case is rejected.")
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected stale production-only plan to fail without prompt-inferred missing-test retry; answer=%s", answer))

	gomega.Expect(sawMissingTestRetry.Load()).To(gomega.BeFalse(), fmt.Sprintf("prompt prose must not trigger missing-test retry; answer=%s", answer))

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read source: %v", err))

	gomega.Expect(strings.Contains(string(gotSource), "return false")).To(gomega.BeTrue(), fmt.Sprintf("stale production-only plan should be the only applied change without inferred retry:\n%s", string(gotSource)))

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read test: %v", err))

	gomega.Expect(string(gotTest)).To(gomega.Equal(testBody), fmt.Sprintf("test changed during source-satisfied retry:\n%s", string(gotTest)))
})

func loadOnlyTimestampedPlanArtifactForTest(t ginkgo.FullGinkgoTInterface, root string) planartifact.PlanArtifact {
	ginkgo.GinkgoHelper()

	paths, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "*.plan.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob plan artifacts: %v", err))

	var stamped []string

	for _, path := range paths {
		if filepath.Base(path) == "last.plan.json" {
			continue
		}

		stamped = append(stamped, path)
	}

	gomega.Expect(len(stamped)).To(gomega.Equal(1), fmt.Sprintf("expected one timestamped plan artifact under .idiom/plans, got %d: %v", len(stamped), stamped))

	artifact, err := workspace.LoadPlanArtifact(stamped[0])
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load plan artifact %s: %v", stamped[0], err))

	return artifact
}

var _ = ginkgo.It("implementation dispatch inline only edit markdown target is not plan document", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(root, "widget_config.json"), []byte(`{"labels":["alpha"]}`+"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write config: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "widget_notes.md"), []byte("# Notes\n\nStatus: draft.\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write notes: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route: turnRoutePlan{Family: "direct_implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
			Contract:   "implementation_result",
			TargetRefs: []string{"widget_config.json", "widget_notes.md"},
		},
	}
	prompt := "Please update widget_config.json and widget_notes.md. Only edit widget_config.json and widget_notes.md."

	required, allowed, docs, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)
	gomega.Expect(len(required)).To(gomega.Equal(0), fmt.Sprintf("inline only-edit scope should not become required coverage, got %#v", required))

	for _, want := range []string{"widget_config.json", "widget_notes.md"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("allowed edit paths missing %q: %#v", want, allowed))
	}

	gomega.Expect(len(docs)).To(gomega.Equal(0), fmt.Sprintf("markdown edit target must not be treated as a referenced plan document: %#v", docs))

	gomega.Expect(len(blockers)).To(gomega.Equal(0), fmt.Sprintf("direct markdown edit target must not produce plan-document blockers: %#v", blockers))
})

var _ = ginkgo.It("run prompt once reference only design doc dispatches plan execute without refine", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "internal", "workflows", "productworkflow"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir productworkflow: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go"), []byte("package productworkflow\n\nconst TempPolicy = \"weak\"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write migration file: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go"), []byte("package productworkflow\n\nconst TempPolicyTested = false\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write migration test file: %v", err))
	}

	designDoc := strings.Join([]string{
		"# Product Workflow Design",
		"",
		"Open delivery blockers from review: status migration apply must harden predictable temp-path writes against pre-existing symlinks.",
		"",
		"Phase 6 and Phase 7 also contain unrelated continuation and sidecar work.",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte(designDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write design doc: %v", err))
	}

	var preflightCalls atomic.Int64

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		preflightCalls.Add(1)

		return implementationdispatch.PlanDocumentPreflightResult{
			Path:     doc.Path,
			OK:       false,
			Blockers: []string{"reference-only design doc must not run dispatch preflight"},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
		return implementationdispatch.PlanDocumentRefineResult{
			OriginalPath: doc.Path,
			ExitCode:     1,
			Stderr:       "reference-only design doc must not run refine",
		}
	})

	var plannerRequests atomic.Int64

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read request: %v", err))

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- internal/workflows/productworkflow/migration_apply.go contains the hardened temp policy.\n- No out-of-scope paths were changed.\n- Planner-owned focused tests remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				plannerRequests.Add(1)

				for _, want := range []string{
					"Implement one bounded topic from docs/design.md",
					"## Referenced Plan Documents",
					"status migration apply must harden predictable temp-path writes",
				} {
					gomega.Expect(strings.Contains(requestText, want)).To(gomega.BeTrue(), fmt.Sprintf("planner prompt lost reference-only design-doc context %q:\n%s", want, requestText))
				}

				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, "internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"); ok {
					reply = readReply
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Harden the migration temp policy requested by the referenced design document and add regression coverage.","edits":[{"path":"internal/workflows/productworkflow/migration_apply.go","old_text":"const TempPolicy = \"weak\"","new_text":"const TempPolicy = \"hardened\"","purpose":"harden migration temp path policy"},{"path":"internal/workflows/productworkflow/migration_apply_test.go","old_text":"const TempPolicyTested = false","new_text":"const TempPolicyTested = true","purpose":"cover migration temp path policy"}]}`
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))

	defer server.Close()

	cfg := config.Default()

	cfg.LlamaServer.BaseURL = server.URL
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.DebugLogDir = filepath.Join(root, "logs", "conversations")
	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.MCP = nil

	model := New(cfg)
	defer model.Close()
	model.planExecuteApproveAllEdits = true

	answer, err := newDetachedPromptExecutionSession(context.Background(), model).runPlanExecuteCommandOnce(context.Background(), "Implement one bounded topic from docs/design.md: status migration apply must harden predictable temp-path writes against pre-existing symlinks. Add regression tests proving temp symlinks are rejected.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runPlanExecuteCommandOnce: %v\nanswer: %s", err, answer))

	got, err := os.ReadFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read migration file: %v", err))

	gomega.Expect(string(got)).To(gomega.Equal("package productworkflow\n\nconst TempPolicy = \"hardened\"\n"), fmt.Sprintf("migration file content = %q, want hardened policy", string(got)))

	gotTest, err := os.ReadFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read migration test file: %v", err))

	gomega.Expect(string(gotTest)).To(gomega.Equal("package productworkflow\n\nconst TempPolicyTested = true\n"), fmt.Sprintf("migration test file content = %q, want regression coverage marker", string(gotTest)))

	gomega.Expect(preflightCalls.Load()).To(gomega.BeNumerically("==", 0), fmt.Sprintf("reference-only design doc should skip plan-document preflight, got %d call(s)", preflightCalls.Load()))

	gomega.Expect(plannerRequests.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected plan-execute planner request"))
})

func composeImplementationBriefForTest(prompt string, plan promptExecutionPlan, cfg config.Config) string {
	requiredPaths, allowedPaths, planDocuments, _ := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)
	return composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, cfg, requiredPaths, allowedPaths, planDocuments)
}

var _ = ginkgo.It("compose implementation brief treats design doc scope as candidate paths", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Handoff",
		"",
		"## Files to Modify",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | Do the important thing |",
		"",
		"## Implementation Steps",
		"",
		"1. Preserve this body text for the planner.",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, "docs", "handoff.md"), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write handoff: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("existing\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write target: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{"docs/handoff.md"}),
	}
	brief := composeImplementationBriefForTest("start working on docs/handoff.md", plan, cfg)

	gomega.Expect(strings.Contains(brief, "## Files to Modify\n\n- target.txt")).To(gomega.BeFalse(), fmt.Sprintf("design-doc scope must not become active required edit scope:\n%s", brief))

	gomega.Expect(strings.Contains(brief, "## Candidate Edit Scope From Target References\n\n- target.txt")).To(gomega.BeTrue(), fmt.Sprintf("brief missing candidate edit scope:\n%s", brief))

	for _, want := range []string{
		"## Referenced Plan Documents",
		"### docs/handoff.md",
		"> ## Files to Modify",
		"> | target.txt | Do the important thing |",
		"> 1. Preserve this body text for the planner.",
	} {
		gomega.Expect(strings.Contains(brief, want)).To(gomega.BeTrue(), fmt.Sprintf("brief missing %q:\n%s", want, brief))
	}

	{
		got := plancontract.RequiredEditPathsFromPrompt(brief)
		gomega.Expect(slices.Contains(got, "target.txt")).To(gomega.BeFalse(), fmt.Sprintf("design-doc scope must not become required edit paths, got %#v\n%s", got, brief))
	}

	{
		got := plancontract.AllowedEditPathsFromPrompt(brief)
		gomega.Expect(slices.Contains(got, "target.txt")).To(gomega.BeTrue(), fmt.Sprintf("design-doc scope must become candidate edit paths, got %#v\n%s", got, brief))
	}
})

var _ = ginkgo.It("compose implementation brief excerpts long reference document", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	var doc strings.Builder

	doc.WriteString("# Long Design\n\n")
	doc.WriteString("Status migration apply must harden predictable temp-path writes against pre-existing workflow/index/events temp symlinks.\n\n")

	for i := 0; i < 500; i++ {
		fmt.Fprintf(&doc, "## Unrelated Section %03d\n\nThis section discusses unrelated operator UX material and filler details that should not dominate the implementation brief.\n\n", i)
	}

	doc.WriteString("## Relevant Regression Coverage\n\nAdd regression tests proving pre-existing workflow/index/events temp symlinks are rejected without partial durable mutation.\n\n")

	{
		err := os.WriteFile(filepath.Join(root, "docs", "long-design.md"), []byte(doc.String()), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write design doc: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest(nil),
	}
	prompt := "Implement status migration apply temp symlink hardening and add regression tests."
	brief := composeImplementationBriefForTest(prompt+" docs/long-design.md", plan, cfg)

	gomega.Expect(len(brief) > 18000).To(gomega.BeFalse(), fmt.Sprintf("long reference doc should be compacted, got %d bytes", len(brief)))

	lowerBrief := strings.ToLower(brief)
	for _, want := range []string{
		"[excerpted from long referenced document",
		"status migration apply must harden predictable temp-path writes",
		"add regression tests proving pre-existing workflow/index/events temp symlinks",
	} {
		gomega.Expect(strings.Contains(lowerBrief, strings.ToLower(want))).To(gomega.BeTrue(), fmt.Sprintf("compacted brief missing %q:\n%s", want, brief))
	}

	gomega.Expect(brief).To(gomega.Not(gomega.ContainSubstring("Unrelated Section 001")), fmt.Sprintf("compacted brief retained unrelated tail:\n%s", brief))
	gomega.Expect(brief).To(gomega.Not(gomega.ContainSubstring("Unrelated Section 499")), fmt.Sprintf("compacted brief retained unrelated tail:\n%s", brief))
})

var _ = ginkgo.It("compose implementation brief resolves unique plan document basename as candidate scope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "docs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir docs: %v", err))
	}

	planDoc := strings.Join([]string{
		"# Writer Route Decision",
		"",
		"## Files To Modify",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| internal/surfaces/tui/app/writer_route.go | Add writer route decision support |",
		"",
	}, "\n")
	docName := "writer-route-decision-report-only-plan.md"

	{
		err := os.WriteFile(filepath.Join(root, "docs", docName), []byte(planDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan doc: %v", err))
	}

	{
		err := os.MkdirAll(filepath.Join(root, "internal", "surfaces", "tui", "app"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir internal/surfaces/tui/app: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "writer_route.go"), []byte("package app\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write writer route: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{docName}),
	}
	brief := composeImplementationBriefForTest("start working on "+docName, plan, cfg)

	for _, want := range []string{
		"## Candidate Edit Scope From Target References\n\n- internal/surfaces/tui/app/writer_route.go",
		"### docs/" + docName,
		"> | internal/surfaces/tui/app/writer_route.go | Add writer route decision support |",
	} {
		gomega.Expect(strings.Contains(brief, want)).To(gomega.BeTrue(), fmt.Sprintf("brief missing %q:\n%s", want, brief))
	}

	{
		got := plancontract.RequiredEditPathsFromPrompt(brief)
		gomega.Expect(slices.Contains(got, "internal/surfaces/tui/app/writer_route.go")).To(gomega.BeFalse(), fmt.Sprintf("resolved design-doc scope must not become required edit paths, got %#v\n%s", got, brief))
	}

	{
		got := plancontract.AllowedEditPathsFromPrompt(brief)
		gomega.Expect(slices.Contains(got, "internal/surfaces/tui/app/writer_route.go")).To(gomega.BeTrue(), fmt.Sprintf("resolved design-doc scope must become candidate edit paths, got %#v\n%s", got, brief))
	}
})

var _ = ginkgo.It("compose implementation brief keeps atom scope as required paths", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	atomRel := filepath.Join(".idiom", "atoms", "handoff", "atom-001.md")

	{
		err := os.MkdirAll(filepath.Join(root, filepath.Dir(atomRel)), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir atom dir: %v", err))
	}

	atomDoc := strings.Join([]string{
		"# Handoff Atom",
		"",
		"## Files To Modify",
		"",
		"| File | Purpose |",
		"|---|---|",
		"| target.txt | Do the important thing |",
		"",
	}, "\n")
	{
		err := os.WriteFile(filepath.Join(root, atomRel), []byte(atomDoc), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write atom: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{filepath.ToSlash(atomRel)}),
	}
	brief := composeImplementationBriefForTest("start working on "+filepath.ToSlash(atomRel), plan, cfg)

	gomega.Expect(strings.Contains(brief, "## Files to Modify\n\n- target.txt")).To(gomega.BeTrue(), fmt.Sprintf("atom scope must remain active required edit scope:\n%s", brief))

	{
		got := plancontract.RequiredEditPathsFromPrompt(brief)
		gomega.Expect(slices.Contains(got, "target.txt")).To(gomega.BeTrue(), fmt.Sprintf("atom scope must become required edit paths, got %#v\n%s", got, brief))
	}
})

var _ = ginkgo.It("implementation dispatch inline required paths become plan execute required scope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, rel := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		{
			err := os.MkdirAll(filepath.Dir(abs), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", rel, err))
		}

		{
			err := os.WriteFile(abs, []byte("package main\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", rel, err))
		}
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{runtimeBrief: runtimeBriefForTest(nil)}
	prompt := "Implement JSON usage-error parity. Required edit paths: internal/surfaces/cli/product/product_workflow.go, internal/surfaces/cli/product/product_workflow_test.go"
	required, allowed, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	gomega.Expect(len(blockers)).To(gomega.Equal(0), fmt.Sprintf("unexpected blockers: %#v", blockers))

	for _, want := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		gomega.Expect(slices.Contains(required, want)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got required=%#v allowed=%#v", want, required, allowed))

		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeFalse(), fmt.Sprintf("inline required path %q must not be demoted to allowed scope: required=%#v allowed=%#v", want, required, allowed))
	}
})

var _ = ginkgo.It("implementation dispatch retry candidate scope preserves compiled write envelope", func() {
	opts := planexecute.Options{
		RequiredEditPaths:     planexecute.MultiFlag{"pkg/target.go"},
		ScopeContract:         plancontract.NewPlanScopeContract([]string{"pkg/target.go"}, nil, nil).WithWriteAllowedPaths([]string{"pkg/target.go"}),
		ScopeExtractionPolicy: planexecute.ScopeExtractionFrozen,
	}

	retry := implementationDispatchRetryOptionsWithCandidateScope(opts, []string{"pkg/discovered.go"})

	{
		got, want := retry.ScopeExtractionPolicy, planexecute.ScopeExtractionFrozen
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("scope extraction policy = %q, want %q", got, want))
	}

	{
		got, want := retry.ScopeContract.WriteAllowedPaths, []string{"pkg/target.go"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("write envelope = %#v, want %#v", got, want))
	}

	gomega.Expect(retry.ScopeContract.WriteAllowedExplicit).To(gomega.BeTrue(), fmt.Sprintf("write envelope should remain explicit: %#v", retry.ScopeContract))

	gomega.Expect(slices.Contains(retry.ScopeContract.AllowedCandidatePaths, "pkg/discovered.go")).To(gomega.BeTrue(), fmt.Sprintf("discovered path should remain candidate scope, contract=%#v", retry.ScopeContract))
})

var _ = ginkgo.It("implementation dispatch inline required paths ignore trailing slash prose", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, rel := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		{
			err := os.MkdirAll(filepath.Dir(abs), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", rel, err))
		}

		{
			err := os.WriteFile(abs, []byte("package main\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", rel, err))
		}
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{runtimeBrief: runtimeBriefForTest(nil)}
	prompt := "Implement JSON usage-error parity. Required edit paths: `internal/surfaces/cli/product/product_workflow.go`, `internal/surfaces/cli/product/product_workflow_test.go`. Add focused tests for missing `--effort-root` and mutually exclusive/missing mode flags under JSON output."
	required, allowed, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	gomega.Expect(len(blockers)).To(gomega.Equal(0), fmt.Sprintf("unexpected blockers: %#v", blockers))

	want := []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"}
	gomega.Expect(len(required)).To(gomega.Equal(len(want)), fmt.Sprintf("required paths = %#v, want %#v", required, want))

	for _, path := range want {
		gomega.Expect(slices.Contains(required, path)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got required=%#v allowed=%#v", path, required, allowed))
	}

	for _, forbidden := range []string{"exclusive/missing", "cmd/idiom"} {
		gomega.Expect(required).To(gomega.Not(gomega.ContainElement(forbidden)), fmt.Sprintf("slash prose or truncated directory leaked into scope: required=%#v allowed=%#v", required, allowed))
		gomega.Expect(allowed).To(gomega.Not(gomega.ContainElement(forbidden)), fmt.Sprintf("slash prose or truncated directory leaked into scope: required=%#v allowed=%#v", required, allowed))
	}
})

func runtimeBriefForTest(targetRefs []string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{TargetRefs: targetRefs}
}

var _ = ginkgo.It("correct turn route plan does not upgrade verification only with edit intent heuristic", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "verification_only"}, "Modify exactly one file and do not run tests.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("verification_only"), fmt.Sprintf("family = %q, want verification_only", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("fallback turn route does not use edit atom as implementation heuristic", func() {
	prompt := `
# IMPLEMENTATION REQUEST - EDIT FILES

The user explicitly requests repository file edits using apply_patch, edit_file,
or write_file.

## Files To Modify

| File | What |
|---|---|
| src/simulation/job_queue.cpp | Post/cancel loader jobs. |

## Required Behavior

- Modify the implementation so loader jobs are deduplicated.

## Acceptance

` + "```bash" + `
python3 scripts/run_tests.py --build-only
./build/tests/job_queue_tests --case JobQueue
git diff --check
` + "```" + `
`
	plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("correct turn route plan does not rewrite analysis continuation route", func() {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Continue your investigation until you have a confident response.", modepolicy.BuildMode())
	gomega.Expect(plan.Family).To(gomega.Equal("explore_then_implement"), fmt.Sprintf("family = %q, want explore_then_implement", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("explore"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.FollowupLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("gd script route falls back for non gd script explicit scope", func() {
	prompt := "Implement the change.\n\nrequired_edit_paths:\n- internal/labels/labels.go\n"
	route := validateRouteFamilyAgainstExplicitEditScope(turnRoutePlan{Family: "gdscript_edit", Reason: "classifier"}, prompt, modepolicy.BuildMode())

	gomega.Expect(route.Family).To(gomega.Equal("implementation"), fmt.Sprintf("family = %q, want implementation for non-GDScript explicit scope", route.Family))

	gomega.Expect(route.Reason).To(gomega.Equal("gdscript_scope_mismatch"), fmt.Sprintf("reason = %q, want gdscript_scope_mismatch", route.Reason))
})

var _ = ginkgo.It("gd script route keeps all gd script explicit scope", func() {
	prompt := "Implement the change.\n\nrequired_edit_paths:\n- scenes/player.gd\nallowed_edit_paths:\n- tests/player_test.gd\n"
	route := validateRouteFamilyAgainstExplicitEditScope(turnRoutePlan{Family: "gdscript_edit", Reason: "classifier"}, prompt, modepolicy.BuildMode())

	gomega.Expect(route.Family).To(gomega.Equal("gdscript_edit"), fmt.Sprintf("family = %q, want gdscript_edit for GDScript-only explicit scope", route.Family))
})

var _ = ginkgo.It("should prefer fallback route does not override conversation with heuristic fallback", func() {
	fallback := fallbackTurnRoute("Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?", modepolicy.BuildMode())
	gomega.Expect(fallback.Family).To(gomega.Equal("conversation"), fmt.Sprintf("fallback family = %q, want conversation", fallback.Family))

	planned := normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, modepolicy.BuildMode())
	gomega.Expect(planned.Family).To(gomega.Equal("conversation"), fmt.Sprintf("legacy direct_question should normalize to conversation, got %#v", planned))

	gomega.Expect(shouldPreferFallbackRoute(fallback, planned, "Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?")).To(gomega.BeFalse(), fmt.Sprint("conservative fallback must not override a generic conversation model route"))
})

var _ = ginkgo.It("classify turn route lets model upgrade conversation fallback", func() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"verification_only","initial_lane":"main","reason":"verification_request","contract":"direct_answer"}`},
			}},
		})
	}))
	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Can you verify that this still builds?", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("verification_only"), fmt.Sprintf("family = %q, want verification_only", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))
})

var _ = ginkgo.It("classify turn route uses classifier before implementation fallback", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"docs_edit","initial_lane":"implement","reason":"direct_implementation","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Update the README docs for the new workflow.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("docs_edit"), fmt.Sprintf("family = %q, want docs_edit", plan.Family))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("implementation-like prose should go through classifier first, got %d calls", calls.Load()))
})

var _ = ginkgo.It("route classifier prompt omits fenced code bodies", func() {
	prompt := "Implement the change.\n```go\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n```\nUse the exact source."
	got := routeClassifierPromptForModel(prompt)

	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("ShortLabel")), fmt.Sprintf("classifier prompt leaked code body:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("Save changes")), fmt.Sprintf("classifier prompt leaked code body:\n%s", got))

	gomega.Expect(strings.Contains(got, "[code block omitted for route classification]")).To(gomega.BeTrue(), fmt.Sprintf("classifier prompt missing omission marker:\n%s", got))

	gomega.Expect(got).To(gomega.ContainSubstring("Implement the change."), fmt.Sprintf("classifier prompt lost surrounding prose:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("Use the exact source."), fmt.Sprintf("classifier prompt lost surrounding prose:\n%s", got))
})

var _ = ginkgo.It("classify turn route repairs invalid json once", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		content := `{`

		if call == 2 {
			content = `{"family":"implementation","initial_lane":"implement","reason":"classifier_repaired","contract":"implementation_result"}`
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": content},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Implement the label change.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("repaired classifier route = %#v, want implementation", plan))
	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("repaired classifier route = %#v, want implementation", plan))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 2), fmt.Sprintf("classifier calls = %d, want initial + repair", calls.Load()))
})

var _ = ginkgo.It("classify turn route repairs invalid json from fallback client", func() {
	classifierServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "classifier unavailable", http.StatusServiceUnavailable)
	}))
	defer classifierServer.Close()

	var fallbackCalls atomic.Int64

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := fallbackCalls.Add(1)
		content := `{`

		if call == 2 {
			content = `{"family":"implementation","initial_lane":"implement","reason":"fallback_repaired","contract":"implementation_result"}`
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": content},
			}},
		})
	}))

	defer fallbackServer.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client:        llama.NewClient(fallbackServer.URL, time.Second),
		summaryClient: llama.NewClient(classifierServer.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Implement the label change.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("repaired classifier route = %#v, want implementation", plan))
	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("repaired classifier route = %#v, want implementation", plan))

	gomega.Expect(fallbackCalls.Load()).To(gomega.BeNumerically("==", 2), fmt.Sprintf("fallback client calls = %d, want invalid response + repair", fallbackCalls.Load()))
})

var _ = ginkgo.It("classify turn route does not skip classifier for debug log signal", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"direct_question"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Check the most recent logs and explain why it failed.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("debug log wording should be classified by model, got %d calls", calls.Load()))
})

var _ = ginkgo.It("classify turn route uses classifier for plan execute source satisfied smoke write shaped", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	prompt := "Smoke test the default implementation lane through the full PRD-to-plan-to-execute path without changing files. Requirement: verify the durable TUI smoke fixture is already satisfied by the current repository and answer source-satisfied with evidence. Required edit paths: internal/surfaces/tui/app/app.go, internal/runtime/agent/runner.go. Do not write files unless the evidence proves a real missing fixture."
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("family = %q, want implementation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected plan: %#v", plan))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("source-satisfied plan-execute workflow should go through classifier, got classifier calls=%d", calls.Load()))
})

var _ = ginkgo.It("classify turn route keeps plan execute how to question read only", func() {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "How do I run plan-execute without changing files?", modepolicy.BuildMode())

	gomega.Expect(workerruntime.TaskFamilyMutationCapable(plan.Family)).To(gomega.BeFalse(), fmt.Sprintf("plan-execute how-to question should stay read-only, got %#v", plan))
})

var _ = ginkgo.It("classify turn route keeps question shaped mutation read only", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"direct_question","initial_lane":"main","reason":"read_only_question","contract":"direct_answer"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	prompt := "The current model string in the top-right portion of the banner shows a path with what seems to be a stripped filename. Can we shorten this to be just the model's alias?"
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("conversation"), fmt.Sprintf("family = %q, want conversation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("main"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("unexpected plan: %#v", plan))

	gomega.Expect(calls.Load()).NotTo(gomega.BeNumerically("==", 0), fmt.Sprint("expected classifier to be consulted before honoring the read-only route"))
})

var _ = ginkgo.It("route classifier prompt does not treat question shape as lane signal", func() {
	var requestText string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		body, err := io.ReadAll(r.Body)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read classifier request: %v", err))

		requestText = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Can you implement this feature: docs/design-prd-adr-brief-research-workflow.md Phase 7?", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("classifier-selected polite implementation directive should route implementation, got %#v", plan))

	gomega.Expect(strings.Contains(requestText, `"top_p":1`)).To(gomega.BeTrue(), fmt.Sprintf("classifier request must use valid deterministic top_p, got:\n%s", requestText))

	for _, want := range []string{
		"Classify the requested outcome first",
		"requested_outcome enum: answer, diagnose, change_workspace, diagnose_then_change, verify, plan, review, locate",
		"write_intent enum: yes, no, ambiguous",
		"evidence_intent enum: generic, relation_trace, refactor",
		"continuation_target enum: prior_change, current_prompt_only, none",
		"Question-shaped wording is not a lane signal by itself",
		"Classify by requested outcome",
		"Do not let can/could/would/should/how/what/why/whether/explain wording override an explicit implementation directive",
		"After a completed implementation, do not keep a sticky implementation state",
		"Can you implement this feature: docs/tdd-feature.md",
	} {
		gomega.Expect(strings.Contains(requestText, want)).To(gomega.BeTrue(), fmt.Sprintf("classifier prompt missing %q:\n%s", want, requestText))
	}
})

var _ = ginkgo.It("turn route plan carries evidence intent", func() {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family:           "conversation",
		RequestedOutcome: "answer",
		WriteIntent:      "no",
		EvidenceIntent:   "calls",
	}, modepolicy.BuildMode())

	gomega.Expect(plan.EvidenceIntent).To(gomega.Equal("relation_trace"), fmt.Sprintf("evidence intent = %q, want relation_trace", plan.EvidenceIntent))

	{
		rendered := renderTurnRoutePlan(plan)
		gomega.Expect(strings.Contains(rendered, "evidence_intent=relation_trace")).To(gomega.BeTrue(), fmt.Sprintf("rendered route missing evidence intent: %q", rendered))
	}

	brief := turnRoutePlanRuntimeBrief(plan, modepolicy.BuildMode(), []string{"context_pack", "code_navigate", "read_file"})
	gomega.Expect(brief.EvidenceIntent).To(gomega.Equal("relation_trace"), fmt.Sprintf("runtime brief did not preserve typed route intent: %#v", brief))
	gomega.Expect(brief.RequestedOutcome).To(gomega.Equal("answer"), fmt.Sprintf("runtime brief did not preserve typed route intent: %#v", brief))
	gomega.Expect(brief.WriteIntent).To(gomega.Equal("no"), fmt.Sprintf("runtime brief did not preserve typed route intent: %#v", brief))
})

var _ = ginkgo.It("classify turn route question shaped mutation fallback is read only", func() {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Can we shorten the displayed model label to just the alias?", modepolicy.BuildMode())

	gomega.Expect(workerruntime.TaskFamilyMutationCapable(plan.Family)).To(gomega.BeFalse(), fmt.Sprintf("question-shaped mutation should stay read-only, got %#v", plan))
})

var _ = ginkgo.It("classify turn route lane name question stays read only", func() {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	for _, prompt := range []string{
		"Can you explain to me what the difference is between the implement and plan-execute lanes?",
		"Can you explain whether we can shorten the displayed model label to just the alias?",
		"Can we shorten the displayed model label to just the alias without changing files?",
	} {
		plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())
		gomega.Expect(workerruntime.TaskFamilyMutationCapable(plan.Family)).To(gomega.BeFalse(), fmt.Sprintf("%q should stay read-only, got %#v", prompt, plan))
	}
})

var _ = ginkgo.It("classify turn route honors classifier implementation directive", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "Shorten the displayed model label to just the alias.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("classifier-selected directive should route implementation, got %#v", plan))
	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("classifier-selected directive should route implementation, got %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("classifier-selected directive should route implementation, got %#v", plan))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected classifier call, got %d", calls.Load()))
})

var _ = ginkgo.It("classify turn route implement slash mention does not bypass classifier", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"direct_question","initial_lane":"main","reason":"read_only_question","contract":"direct_answer"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	prompt := `I want to test the implement lane... can you use /implement to create and write a new file with the text "ben is cool"?`
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	gomega.Expect(workerruntime.TaskFamilyMutationCapable(plan.Family)).To(gomega.BeFalse(), fmt.Sprintf("slash mention inside prose should not deterministically route implementation, got %#v", plan))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected classifier call, got %d", calls.Load()))
})

var _ = ginkgo.It("fallback turn route lane exercise does not become verification only", func() {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), "I want to test the implementation lane.", modepolicy.BuildMode())

	gomega.Expect(plan.Family).NotTo(gomega.Equal("verification_only"), fmt.Sprintf("lane exercise wording should not auto-run tests, got %#v", plan))
})

var _ = ginkgo.It("classify turn route uses classifier for plan execute required path edit write shaped", func() {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"family":"implementation","initial_lane":"implement","reason":"classifier_directive","contract":"implementation_result"}`},
			}},
		})
	}))

	defer server.Close()

	m := &Model{
		cfg: config.Config{
			RouteClassifier: config.RouteClassifier{Enabled: true},
		},
		client: llama.NewClient(server.URL, time.Second),
	}
	prompt := "Implement the durable TUI smoke fixture through the full PRD-to-plan-to-execute path. Required edit paths: internal/surfaces/tui/app/app.go, internal/runtime/agent/runner.go."
	plan := newDetachedPromptExecutionSession(context.Background(), m).classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	gomega.Expect(plan.Family).To(gomega.Equal("implementation"), fmt.Sprintf("family = %q, want implementation", plan.Family))

	gomega.Expect(plan.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected plan: %#v", plan))
	gomega.Expect(plan.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected plan: %#v", plan))

	gomega.Expect(calls.Load()).To(gomega.BeNumerically("==", 1), fmt.Sprintf("required-path plan-execute edit workflow should go through classifier, got classifier calls=%d", calls.Load()))
})

var _ = ginkgo.It("turn route plan runtime brief for conversation uses read only workspace", func() {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "edit_file", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "conversation"}, modepolicy.BuildMode(), available)

	gomega.Expect(brief.PermissionProfile).To(gomega.Equal("read_only_workspace"), fmt.Sprintf("permission = %q, want read_only_workspace", brief.PermissionProfile))

	gomega.Expect(len(brief.AllowedTools)).NotTo(gomega.Equal(0), fmt.Sprintf("conversation should advertise read-only tools, got %#v", brief.AllowedTools))

	gomega.Expect(len(brief.ReadinessGate)).To(gomega.Equal(0), fmt.Sprintf("expected conversation to use no readiness gate and answer-quality critics, got %#v", brief))
	gomega.Expect(brief.FinalCritic).To(gomega.ContainElement("answers_actual_user_request"), fmt.Sprintf("expected conversation to use no readiness gate and answer-quality critics, got %#v", brief))
	gomega.Expect(brief.FinalCritic).To(gomega.ContainElement("well_formatted_informative_on_topic"), fmt.Sprintf("expected conversation to use no readiness gate and answer-quality critics, got %#v", brief))
})

var _ = ginkgo.It("turn route plan runtime brief for verification only", func() {
	available := []string{"task", "pwd", "read_file", "search_files", "search_content", "shell", "edit_file", "write_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "verification_only"}, modepolicy.BuildMode(), available)

	gomega.Expect(brief.PermissionProfile).To(gomega.Equal("read_only_verification_shell"), fmt.Sprintf("permission = %q, want read_only_verification_shell", brief.PermissionProfile))

	gomega.Expect(strings.Contains(strings.Join(brief.AllowedTools, ","), "shell")).To(gomega.BeTrue(), fmt.Sprintf("expected shell in allowed tools, got %#v", brief.AllowedTools))

	for _, tool := range brief.AllowedTools {
		gomega.Expect(tool).To(gomega.Not(gomega.Equal("edit_file")), fmt.Sprintf("unexpected write tool %q in allowed tools %#v", tool, brief.AllowedTools))
		gomega.Expect(tool).To(gomega.Not(gomega.Equal("write_file")), fmt.Sprintf("unexpected write tool %q in allowed tools %#v", tool, brief.AllowedTools))
		gomega.Expect(tool).To(gomega.Not(gomega.Equal("pwd")), fmt.Sprintf("unexpected write tool %q in allowed tools %#v", tool, brief.AllowedTools))
	}

	gomega.Expect(brief.VerificationContract).To(gomega.Equal("not_required_for_read_only_lane"), fmt.Sprintf("verification contract = %q", brief.VerificationContract))
})

var _ = ginkgo.It("turn route plan runtime brief for debug logs uses standard read lane", func() {
	available := []string{"pwd", "read_file", "search_files", "search_content", "edit_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "debug_logs"}, modepolicy.BuildMode(), available)

	gomega.Expect(brief.PermissionProfile).To(gomega.Equal("read_only_workspace"), fmt.Sprintf("permission = %q, want read_only_workspace", brief.PermissionProfile))

	gomega.Expect(brief.TaskFamily).To(gomega.Equal("conversation"), fmt.Sprintf("debug_logs route should collapse to standard read lane, got %#v", brief))
	gomega.Expect(brief.Lane).To(gomega.Equal("main"), fmt.Sprintf("debug_logs route should collapse to standard read lane, got %#v", brief))
	gomega.Expect(brief.Contract).To(gomega.Equal("direct_answer"), fmt.Sprintf("debug_logs route should collapse to standard read lane, got %#v", brief))

	gomega.Expect(strings.Contains(strings.Join(brief.AllowedTools, ","), "search_files")).To(gomega.BeTrue(), fmt.Sprintf("expected search_files in allowed tools, got %#v", brief.AllowedTools))
})

var _ = ginkgo.It("turn route plan runtime brief for architecture uses shaping only", func() {
	available := []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "architecture"}, modepolicy.BuildMode(), available)

	gomega.Expect(brief.PermissionProfile).To(gomega.Equal("read_only_workspace"), fmt.Sprintf("permission = %q, want read_only_workspace", brief.PermissionProfile))

	gomega.Expect(len(brief.ReadinessGate)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected architecture analysis to keep a closure gate and final critic, got %#v", brief))
	gomega.Expect(len(brief.FinalCritic)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected architecture analysis to keep a closure gate and final critic, got %#v", brief))
})
