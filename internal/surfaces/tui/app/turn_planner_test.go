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
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/implementationdispatch"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
	"idiom/internal/workflows/recovery"
)

func disableQueueDaemonForTurnPlannerTest(t *testing.T, root string) {
	t.Helper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
}

func stubImplementationDispatchPlanDocumentPreflight(t *testing.T, stub func(context.Context, config.Config, implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult) {
	t.Helper()

	orig := planDocumentPreflightRunner

	planDocumentPreflightRunner = func(ctx context.Context, input implementationdispatch.PlanDocumentPreflightInput) implementationdispatch.PlanDocumentPreflightResult {
		cfg := config.Config{}
		cfg.Workspace.Root = input.WorkspaceRoot
		cfg.Workspace.CWD = input.WorkspaceCWD

		return stub(ctx, cfg, input.Document)
	}

	t.Cleanup(func() { planDocumentPreflightRunner = orig })
}

func allowImplementationDispatchPlanDocumentPreflight(t *testing.T) {
	t.Helper()
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentPreflightResult {
		return implementationdispatch.PlanDocumentPreflightResult{Path: doc.Path, OK: true}
	})
}

func stubImplementationDispatchPlanDocumentRefine(t *testing.T, stub func(context.Context, config.Config, implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult) {
	t.Helper()

	orig := planDocumentRefineRunner

	planDocumentRefineRunner = stub

	t.Cleanup(func() { planDocumentRefineRunner = orig })
}

func TestImplementationDispatchSkipsRefineScratchpadMetaBriefs(t *testing.T) {
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
	decision := m.writerRouteDecisionForPrompt(prompt, plan)

	if decision.Lane != "implement" {
		t.Fatalf("refine scratchpad meta-brief should stay on implement lane, got %#v", decision)
	}
}

func TestImplementationDispatchPlanDocumentSkipsRefineIterArtifactsButAllowsAcceptedBrief(t *testing.T) {
	root := t.TempDir()
	iterDir := filepath.Join(root, ".idiom", "refine", "example", "iter-1")

	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		t.Fatalf("mkdir iter dir: %v", err)
	}

	draftPath := filepath.Join(iterDir, "draft.md")
	if err := os.WriteFile(draftPath, []byte("# Draft\n\n## Files To Modify\n\n- target.txt\n"), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	acceptedPath := filepath.Join(root, ".idiom", "refine", "example", "accepted.md")
	if err := os.WriteFile(acceptedPath, []byte("# Accepted\n\n## Files To Modify\n\n- target.txt\n"), 0o644); err != nil {
		t.Fatalf("write accepted: %v", err)
	}

	if _, ok := implementationdispatch.ReadPlanDocument(root, ".idiom/refine/example/iter-1/draft.md"); ok {
		t.Fatalf("refine iter draft should not be treated as a plan document for recursive TUI refine")
	}

	if _, ok := implementationdispatch.ReadPlanDocument(root, ".idiom/refine/example/accepted.md"); !ok {
		t.Fatalf("accepted refine output should remain readable as a plan document")
	}
}

func TestImplementationDispatchRefineTitleIsBounded(t *testing.T) {
	longPath := ".idiom/refine/" + strings.Repeat("nested-title-", 40) + "iter-1/draft.md"
	title := implementationdispatch.DocumentOutputTitle(longPath)

	if !strings.HasPrefix(title, "tui-") {
		t.Fatalf("title %q missing tui prefix", title)
	}

	if len(title) > 84 {
		t.Fatalf("title length = %d, want <= 84: %s", len(title), title)
	}
}

func TestImplementationDispatchNonzeroRefineCandidateCarriesPreflightBlockers(t *testing.T) {
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

	if err := os.MkdirAll(filepath.Dir(acceptedAbs), 0o755); err != nil {
		t.Fatalf("mkdir refine dir: %v", err)
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
	if err := os.WriteFile(acceptedAbs, []byte(content), 0o644); err != nil {
		t.Fatalf("write accepted refine output: %v", err)
	}

	candidate, candidatePath, blockers, ok := materializeImplementationDispatchNonzeroRefineAcceptedCandidate(context.Background(), cfg, acceptedRel)
	if ok {
		t.Fatalf("candidate should remain blocked by preflight")
	}

	if candidatePath != ".idiom/refine/example/accepted.dispatch.md" {
		t.Fatalf("candidate path = %q", candidatePath)
	}

	if candidate.Path != candidatePath {
		t.Fatalf("candidate document path = %q, want %q", candidate.Path, candidatePath)
	}

	if len(blockers) != 1 || !strings.Contains(strings.Join(blockers[0].Blockers, "\n"), "dirty scoped paths: target.txt") {
		t.Fatalf("expected dirty-path blocker to survive materialization, got %#v", blockers)
	}
}

func TestImplementationDispatchDirtyAllowanceMarksOnlyNamedScopedDocument(t *testing.T) {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"implement atom and explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go for this run",
		docs,
	)

	if !got[0].AllowDirtyAtomScope {
		t.Fatalf("expected named dirty scoped path to allow matching document: %#v", got)
	}

	if got[1].AllowDirtyAtomScope {
		t.Fatalf("dirty scoped path allowance must not apply to unrelated document: %#v", got)
	}
}

func TestImplementationDispatchDirtyAllowanceIgnoresQuotedNextAction(t *testing.T) {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake said next: finish, commit, or explicitly allow the dirty scoped paths before dispatch",
		docs,
	)

	if got[0].AllowDirtyAtomScope {
		t.Fatalf("quoted blocker next-action must not grant dirty scoped path allowance: %#v", got)
	}

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake state=blocked-dirty-files - existing scoped paths have uncommitted changes - dirty paths: internal/contracts/writerroute/decision_test.go - next: finish, commit, or explicitly allow the dirty scoped paths before dispatch",
		docs,
	)
	if got[0].AllowDirtyAtomScope {
		t.Fatalf("pasted dirty-path blocker must not grant dirty scoped path allowance: %#v", got)
	}

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"explicitly allow the dirty scoped paths before dispatch. Stay inside internal/contracts/writerroute/decision_test.go.",
		docs,
	)
	if got[0].AllowDirtyAtomScope {
		t.Fatalf("scope path in a later sentence must not grant dirty scoped path allowance: %#v", got)
	}

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"do not explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		docs,
	)
	if got[0].AllowDirtyAtomScope {
		t.Fatalf("negated dirty scoped path allowance must not grant authority: %#v", got)
	}

	for _, prompt := range []string{
		"don’t explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		"dont explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		"cannot allow dirty path internal/contracts/writerroute/decision_test.go",
		"can't allow dirty path internal/contracts/writerroute/decision_test.go",
		"can’t allow dirty path internal/contracts/writerroute/decision_test.go",
	} {
		got = implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, docs)
		if got[0].AllowDirtyAtomScope {
			t.Fatalf("negated dirty scoped path allowance %q must not grant authority: %#v", prompt, got)
		}
	}

	got = implementationdispatch.PlanDocumentsWithDirtyAllowance(
		"atom intake blocked; next: allow dirty path internal/contracts/writerroute/decision_test.go",
		docs,
	)
	if got[0].AllowDirtyAtomScope {
		t.Fatalf("pasted next-action dirty scoped path allowance must not grant authority: %#v", got)
	}
}

func TestImplementationDispatchDirtyAllowanceBroadFlagAllowsAllScopedDocuments(t *testing.T) {
	docs := []implementationdispatch.PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := implementationdispatch.PlanDocumentsWithDirtyAllowance("rerun with --allow-dirty for these atom scopes", docs)

	if !got[0].AllowDirtyAtomScope || !got[1].AllowDirtyAtomScope {
		t.Fatalf("--allow-dirty should explicitly allow all atom documents in the turn: %#v", got)
	}
}

func TestImplementationDispatchDirtyAllowanceBroadFlagRequiresAffirmativeContext(t *testing.T) {
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
		if got[0].AllowDirtyAtomScope || got[1].AllowDirtyAtomScope {
			t.Fatalf("non-affirmative broad dirty allowance %q must not allow documents: %#v", prompt, got)
		}
	}

	got := implementationdispatch.PlanDocumentsWithDirtyAllowance("rerun with `--allow-dirty` for these atom scopes", docs)
	if !got[0].AllowDirtyAtomScope || !got[1].AllowDirtyAtomScope {
		t.Fatalf("affirmative broad dirty allowance should allow all atom documents: %#v", got)
	}
}

func TestImplementationDispatchAtomMarksMissingPathAsNewFile(t *testing.T) {
	root := t.TempDir()
	atom := implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:   "internal/contracts/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})

	if !strings.HasPrefix(atom.Change, "NEW FILE:") {
		t.Fatalf("missing path atom change = %q, want NEW FILE marker", atom.Change)
	}

	if err := os.MkdirAll(filepath.Join(root, "internal", "contracts", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "contracts", "writerroute", "decision.go"), []byte("package writerroute\n"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	atom = implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:   "internal/contracts/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})
	if strings.HasPrefix(atom.Change, "NEW FILE:") {
		t.Fatalf("existing path atom change = %q, should not add NEW FILE marker", atom.Change)
	}

	if len(atom.Anchors) == 0 {
		t.Fatalf("existing path atom should carry host current-state anchors")
	}

	if !strings.Contains(atom.Anchors[0], "internal/contracts/writerroute/decision.go:1") ||
		!strings.Contains(atom.Anchors[0], "package writerroute") {

		t.Fatalf("unexpected existing path anchor: %#v", atom.Anchors)
	}
}

func TestImplementationDispatchAtomAnchorsPreserveCompleteSourceLines(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("internal", "contracts", "writerroute", "decision.go"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	longLine := "func NewDecision(mode WriterMode, lane string, plannerModel string, executorModel string, reviewerModel string, classifierModel string, riskTier string, costTier string, scopeTier string, verificationOwner string, reviewPolicy string, requiredEditPaths []string, evidencePacketIDs []string, reasonCodes []string, escalationTriggers []string, userOverride bool, taskFamily string) Decision {"
	source := "package writerroute\n\n" + longLine + "\n\treturn Decision{}\n}\n"

	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	atom := implementationdispatch.PlanDocumentAtomWithWorkspaceShape(root, implementationdispatch.PlanDocumentAtom{
		Path:    relPath,
		Change:  "Preserve the NewDecision constructor shape",
		Excerpt: "NewDecision constructor shape",
	})
	anchors := strings.Join(atom.Anchors, "\n")

	if !strings.Contains(anchors, longLine) {
		t.Fatalf("anchor lost complete source line:\n%s", anchors)
	}

	if strings.Contains(anchors, "...") {
		t.Fatalf("source anchors must not truncate actionable snippets:\n%s", anchors)
	}
}

func TestImplementationDispatchMaterializedAtomsCarryDesignDocTestObligations(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"pkg/source.go", "pkg/source_test.go"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}

		if err := os.WriteFile(full, []byte("package pkg\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
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
	if err != nil {
		t.Fatalf("materialize atoms: %v", err)
	}

	var testAtom implementationdispatch.PlanDocument

	for _, atom := range atoms {
		if slices.Contains(atom.EditPaths, "pkg/source_test.go") {
			testAtom = atom
			break
		}
	}

	if strings.TrimSpace(testAtom.Path) == "" {
		t.Fatalf("expected test obligation to materialize a test atom, got %#v", atoms)
	}

	if !strings.Contains(testAtom.Content, "## Design Doc Test Obligations") ||
		!strings.Contains(testAtom.Content, "TestRouteDecisionShape") ||
		!strings.Contains(testAtom.Content, "go test ./pkg") {

		t.Fatalf("materialized test atom missing design-doc test obligations:\n%s", testAtom.Content)
	}
}

func TestImplementationDispatchMaterializedAtomsPreserveTerminalAtomState(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "pkg", "source.go")

	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
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

	if err := os.MkdirAll(filepath.Dir(atomAbs), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
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
	if err := os.WriteFile(atomAbs, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing atom: %v", err)
	}

	atoms, err := implementationdispatch.MaterializePlanDocumentAtoms(root, []implementationdispatch.PlanDocument{doc})
	if err != nil {
		t.Fatalf("materialize atoms: %v", err)
	}

	if len(atoms) != 1 || atoms[0].Path != atomRel {
		t.Fatalf("expected existing atom to be returned, got %#v", atoms)
	}

	status, ok := implementationdispatch.PlanDocumentTerminalAtomStatus(atoms[0])
	if !ok || status != "completed" {
		t.Fatalf("existing atom should stay terminal, status=%q ok=%v", status, ok)
	}

	updated, err := os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read atom: %v", err)
	}

	if string(updated) != existing {
		t.Fatalf("materialization overwrote terminal atom:\n%s", string(updated))
	}
}

func TestRunAtomizedImplementationPlanDocumentsSkipsTerminalAtoms(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	if err := os.MkdirAll(filepath.Dir(atomAbs), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
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
	if err := os.WriteFile(atomAbs, []byte(body), 0o644); err != nil {
		t.Fatalf("write atom: %v", err)
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	if !ok {
		t.Fatalf("atom doc should be readable")
	}

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()

	answer, err := model.runAtomizedImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	if err != nil {
		t.Fatalf("run atomized plan documents: %v", err)
	}

	if !strings.Contains(answer, "already closed") || !strings.Contains(answer, "skipped: completed") {
		t.Fatalf("closed atom should be skipped without dispatch:\n%s", answer)
	}

	updated, err := os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read atom: %v", err)
	}

	if string(updated) != body {
		t.Fatalf("skipping terminal atom should not rewrite it:\n%s", string(updated))
	}
}

func TestImplementationDispatchSourceSatisfiedRequiresTestObligationEvidence(t *testing.T) {
	root := t.TempDir()
	testPath := "pkg/source_test.go"
	full := filepath.Join(root, filepath.FromSlash(testPath))

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir source test: %v", err)
	}

	if err := os.WriteFile(full, []byte("package pkg\n\nvar routeDecisionHarness = true\n"), 0o644); err != nil {
		t.Fatalf("write source test: %v", err)
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

	if records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc}); len(records) != 0 {
		t.Fatalf("source-satisfied must not close without matching test evidence: %#v", records)
	}

	if err := os.WriteFile(full, []byte("package pkg\n\nvar routeDecisionHarness = true\n\nfunc TestRouteDecisionShape(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write source test with evidence: %v", err)
	}

	closeout, ok := implementationdispatch.TryPlanDocumentsSourceSatisfiedCloseout(root, []implementationdispatch.PlanDocument{doc})
	if !ok {
		t.Fatalf("expected source-satisfied closeout")
	}

	message := closeout.Message
	if !strings.Contains(message, "Test obligations") || !strings.Contains(message, "TestRouteDecisionShape") {
		t.Fatalf("source-satisfied message should report test obligations and evidence:\n%s", message)
	}
}

func TestImplementationDispatchSourceSatisfiedRequiresVerificationCommandEvidence(t *testing.T) {
	root := t.TempDir()
	sourcePath := "pkg/source.go"
	full := filepath.Join(root, filepath.FromSlash(sourcePath))

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}

	if err := os.WriteFile(full, []byte("package pkg\n\nfunc routeDecisionHarness() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
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

	if records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc}); len(records) != 0 {
		t.Fatalf("source-satisfied must not treat an unrun verification command as evidence: %#v", records)
	}
}

func TestImplementationDispatchBlockedQuestionDoesNotMarkAtomCompleted(t *testing.T) {
	root := t.TempDir()
	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	if err := os.MkdirAll(filepath.Dir(atomAbs), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
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
	if err := os.WriteFile(atomAbs, []byte(body), 0o644); err != nil {
		t.Fatalf("write atom: %v", err)
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	if !ok {
		t.Fatalf("atom doc should be readable")
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	answer := "<<QUESTION>>: Plan-execute could not produce a plan artifact.\n\nRequired edit paths still need a plan:\n- pkg/source.go"
	if !implementationdispatch.AnswerLooksBlocked(answer) {
		t.Fatalf("question answer should be classified as blocked")
	}

	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, answer, nil)

	updated, err := os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read atom: %v", err)
	}

	if strings.Contains(string(updated), "Status: completed") || strings.Contains(string(updated), "## Result") {
		t.Fatalf("blocked question must not mark atom completed:\n%s", string(updated))
	}
}

func TestImplementationDispatchCommandObligationMustBeProvenBeforeAtomComplete(t *testing.T) {
	root := t.TempDir()
	atomRel := ".idiom/atoms/design/atom-001.md"
	atomAbs := filepath.Join(root, filepath.FromSlash(atomRel))

	if err := os.MkdirAll(filepath.Dir(atomAbs), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
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
	if err := os.WriteFile(atomAbs, []byte(body), 0o644); err != nil {
		t.Fatalf("write atom: %v", err)
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	if !ok {
		t.Fatalf("atom doc should be readable")
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	unprovenAnswer := "Decision: source-satisfied\n\n`pkg/source.go` already contains the requested source."
	if pending := implementationdispatch.UnprovenVerificationCommands(doc, unprovenAnswer); len(pending) != 1 {
		t.Fatalf("expected pending command, got %#v", pending)
	}

	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, unprovenAnswer, nil)

	updated, err := os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read atom: %v", err)
	}

	if strings.Contains(string(updated), "Status: completed") || strings.Contains(string(updated), "## Result") {
		t.Fatalf("unproven command must not mark atom complete:\n%s", string(updated))
	}

	provenAnswer := "Completed source work.\n\nVerification: `go test ./pkg` passed with exit 0."
	implementationdispatch.MarkCompletedAtomDocumentForResult(root, doc, provenAnswer, nil)

	updated, err = os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read atom after proven answer: %v", err)
	}

	if !strings.Contains(string(updated), "Status: completed") || !strings.Contains(string(updated), "go test ./pkg") {
		t.Fatalf("proven command should allow completion:\n%s", string(updated))
	}
}

func TestWriterRouteDecisionForPromptReportsGuardedImplementForExactEditPacket(t *testing.T) {
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
	decision := m.writerRouteDecisionForPrompt(prompt, plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("unexpected writer route decision: %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}

	if decision.PlannerModel != "planner-model" || decision.ExecutorModel != "executor-model" || decision.ClassifierModel != "classifier-model" {
		t.Fatalf("unexpected model fields: %#v", decision)
	}

	if !slices.Contains(decision.RequiredEditPaths, "internal/surfaces/tui/app/writer_route.go") {
		t.Fatalf("required paths missing from writer route decision: %#v", decision.RequiredEditPaths)
	}
}

func TestWriterRouteDecisionForPromptDoesNotCreateSemanticTDDScopeFromProse(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("Implement a TDD coverage-gate fix and add regression tests. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("semantic TDD source+test implementation should stay on guarded implement until semantic plan handoff exists, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}

	if slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor") {
		t.Fatalf("prompt prose must not create semantic source/test executor reason: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptDoesNotUseTDDProductKeywordForSemanticScope(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("Implement a TDD product workflow coverage-gate fix. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("implementation prompt should stay on guarded implement lane, got %#v", decision)
	}

	if slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor") {
		t.Fatalf("TDD/product wording alone must not create semantic source/test executor reason: %#v", decision.ReasonCodes)
	}

	if !slices.Contains(decision.ReasonCodes, "route.local_model_default") {
		t.Fatalf("direct implementation reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptDoesNotUsePlanExecutePhraseForSemanticTDDScope(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("Use the plan-execute lane to implement a TDD coverage-gate fix and add regression tests. Required edit paths: internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("plan-execute prose mention should not route to plan-execute, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}

	if slices.Contains(decision.ReasonCodes, "route.semantic_source_test_executor") {
		t.Fatalf("plan-execute prose mention must not create semantic source/test executor reason: %#v", decision.ReasonCodes)
	}
}

func TestPlanExecuteSlashCommandExecutionPlanUsesPlannerMetadata(t *testing.T) {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	m := &Model{cfg: cfg}
	objective := "Implement the typed TUI command. Required edit paths only: internal/workflows/planexecute/prompt.go, internal/workflows/planexecute/context_pack_test.go, internal/surfaces/tui/app/update_handlers.go, internal/surfaces/tui/app/lifecycle.go."

	plan := m.planExecuteCommandExecutionPlan(objective)
	decision := m.writerRouteDecisionForPrompt(objective, plan)
	wantPaths := []string{
		"internal/workflows/planexecute/prompt.go",
		"internal/workflows/planexecute/context_pack_test.go",
		"internal/surfaces/tui/app/update_handlers.go",
		"internal/surfaces/tui/app/lifecycle.go",
	}

	if plan.mode.Name != "build" {
		t.Fatalf("mode = %q, want build", plan.mode.Name)
	}

	if plan.route.Family != agent.PlanThenExecutePlannerTaskFamily || plan.runtimeBrief.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("typed command should set plan-execute planner family, route=%#v brief=%#v", plan.route, plan.runtimeBrief)
	}

	if !slices.Equal(plan.runtimeBrief.RequiredEditPaths, wantPaths) {
		t.Fatalf("required edit paths = %#v, want %#v", plan.runtimeBrief.RequiredEditPaths, wantPaths)
	}

	if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
		t.Fatalf("typed command should report plan-execute writer metadata, got %#v", decision)
	}

	if decision.TaskFamily != agent.PlanThenExecutePlannerTaskFamily || !slices.Contains(decision.ReasonCodes, "route.plan_execute_planner") {
		t.Fatalf("typed command writer metadata missing planner route, got %#v", decision)
	}
}

func TestClassifierSelectedPlanExecuteFamilyActivatesPlanSubmission(t *testing.T) {
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
		t.Run(family, func(t *testing.T) {
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

			decision := m.writerRouteDecisionForPrompt(objective, plan)

			if route.Family != agent.PlanThenExecutePlannerTaskFamily {
				t.Fatalf("normalized family = %q, want %s", route.Family, agent.PlanThenExecutePlannerTaskFamily)
			}

			if route.InitialLane != "implement" || route.Contract != "implementation_result" {
				t.Fatalf("normalized route lost planner execution contract: %#v", route)
			}

			if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
				t.Fatalf("classifier-selected planner family should activate plan submission, got %#v", decision)
			}

			if decision.TaskFamily != agent.PlanThenExecutePlannerTaskFamily || !slices.Contains(decision.ReasonCodes, "route.plan_execute_planner") {
				t.Fatalf("classifier-selected planner route missing plan submission metadata, got %#v", decision)
			}
		})
	}
}

func TestRouteProfileSemanticsCharacterizeFamiliesForRuntimeProfileExtraction(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			route := normalizeTurnRoutePlan(turnRoutePlan{Family: tc.family}, modepolicy.BuildMode())
			if route.Family != tc.wantFamily || route.InitialLane != tc.wantInitialLane || route.FollowupLane != tc.wantFollowupLane || route.Contract != tc.wantContract {
				t.Fatalf("normalized route = %#v, want family=%q initial=%q followup=%q contract=%q", route, tc.wantFamily, tc.wantInitialLane, tc.wantFollowupLane, tc.wantContract)
			}

			projection := projectRouteContract(route, modepolicy.BuildMode())
			if projection.MutationCapable != tc.wantMutationCapable {
				t.Fatalf("route contract mutation_capable = %t, want %t for %#v", projection.MutationCapable, tc.wantMutationCapable, projection)
			}

			rendered := renderRouteContractProjection(projection)
			var payload map[string]any
			if err := json.Unmarshal([]byte(rendered), &payload); err != nil {
				t.Fatalf("route contract projection is not valid JSON: %v\n%s", err, rendered)
			}
			if payload["family"] != tc.wantFamily || payload["lane"] != tc.wantInitialLane || payload["contract"] != tc.wantContract {
				t.Fatalf("rendered route projection = %#v", payload)
			}

			decision := m.writerRouteDecisionForPrompt("route profile characterization", promptExecutionPlan{
				mode:         modepolicy.BuildMode(),
				route:        route,
				runtimeBrief: turnRoutePlanRuntimeBrief(route, modepolicy.BuildMode(), nil),
			})
			if decision.WriterMode != string(tc.wantWriterMode) || decision.Lane != tc.wantWriterLane {
				t.Fatalf("writer decision = %#v, want mode=%q lane=%q", decision, tc.wantWriterMode, tc.wantWriterLane)
			}
			if tc.wantVerificationOwnr != "" && decision.VerificationOwner != tc.wantVerificationOwnr {
				t.Fatalf("writer verification owner = %q, want %q in %#v", decision.VerificationOwner, tc.wantVerificationOwnr, decision)
			}
		})
	}
}

func TestBuildPromptExecutionPlanPreservesClassifierSelectedPlanExecuteFamily(t *testing.T) {
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

	plan := m.buildPromptExecutionPlan(context.Background(), objective)
	decision := m.writerRouteDecisionForPrompt(objective, plan)

	if calls.Load() != 1 {
		t.Fatalf("classifier calls = %d, want 1", calls.Load())
	}

	if plan.route.Family != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("route family = %q, want %s", plan.route.Family, agent.PlanThenExecutePlannerTaskFamily)
	}

	if plan.runtimeBrief.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("runtime brief task family = %q, want %s", plan.runtimeBrief.TaskFamily, agent.PlanThenExecutePlannerTaskFamily)
	}

	if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
		t.Fatalf("classifier-selected planner should activate plan submission, got %#v", decision)
	}

	if !slices.Contains(decision.ReasonCodes, "route.plan_execute_planner") {
		t.Fatalf("plan submission reason missing: %#v", decision.ReasonCodes)
	}
}

func TestImplementSlashCommandExecutionPlanUsesImplementMetadata(t *testing.T) {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	objective := "Create a small fixture file. Required edit paths only: tmp/typed-implement.txt."

	plan := m.implementCommandExecutionPlan(objective)
	decision := m.writerRouteDecisionForPrompt(objective, plan)

	if plan.mode.Name != "build" {
		t.Fatalf("mode = %q, want build", plan.mode.Name)
	}

	if plan.route.Family != "implementation" || plan.route.Reason != "typed_implement_command" {
		t.Fatalf("typed command should set implementation route, got %#v", plan.route)
	}

	if plan.runtimeBrief.TaskFamily != "implementation" {
		t.Fatalf("brief task family = %q, want implementation", plan.runtimeBrief.TaskFamily)
	}

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("typed command should report implement writer metadata, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" || !slices.Contains(decision.ReasonCodes, "route.typed_implement_command") {
		t.Fatalf("typed command writer metadata missing implement route, got %#v", decision)
	}
}

func TestWriterRouteDecisionForPromptReportsReviewRepairAsPlanSubmission(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("review repair", plan)

	if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
		t.Fatalf("review-repair should report plan-submission authority under plan-execute, got %#v", decision)
	}

	if decision.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
		t.Fatalf("task family = %q, want %s", decision.TaskFamily, agent.PlanThenExecuteReviewRepairTaskFamily)
	}

	if decision.VerificationOwner != "planner_owned" {
		t.Fatalf("review-repair should preserve planner-owned verification, got %#v", decision)
	}

	if !slices.Contains(decision.ReasonCodes, "route.plan_execute_review_repair") {
		t.Fatalf("review-repair route reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptDoesNotUsePlanExecutePhraseAsContract(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("Plan-execute the widget behavior. Required edit paths: pkg/widget.go.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("plan-execute wording alone should not select plan-submission contract, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}
}

func TestWriterRouteDecisionForPromptKeepsSimpleUnscopedImplementationOnGuardedLane(t *testing.T) {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:         modepolicy.BuildMode(),
		route:        turnRoutePlan{Family: "implementation"},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{},
	}

	decision := m.writerRouteDecisionForPrompt("Update the small label text in the active file.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("simple unscoped implementation should stay on guarded implement lane, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}

	if !slices.Contains(decision.ReasonCodes, "route.local_model_default") {
		t.Fatalf("direct implementation reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptKeepsSimpleScopedSourceTestImplementationOnGuardedLane(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("Fix the workflow label test expectation. Required edit paths: pkg/widget.go, pkg/widget_test.go.", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("simple scoped source+test implementation should stay on guarded implement lane, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}

	if !slices.Contains(decision.ReasonCodes, "route.local_model_default") {
		t.Fatalf("direct implementation reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptKeepsExactEditPacketOnGuardedLane(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt(prompt, plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("exact edit packet should stay on guarded implement lane, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}
}

func TestWriterRouteDecisionForPromptDoesNotUsePlanExecutePhraseForExactEditPacket(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt(prompt, plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("plan-execute prose mention should not route exact edit packet to plan-execute, got %#v", decision)
	}

	if decision.TaskFamily != "implementation" {
		t.Fatalf("task family = %q, want implementation", decision.TaskFamily)
	}
}

func TestWriterRouteDecisionForPromptReportsPlanExecutePlannerFamily(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("planner phase", plan)

	if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
		t.Fatalf("plan-execute planner should report plan-submission route, got %#v", decision)
	}

	if decision.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("task family = %q, want %s", decision.TaskFamily, agent.PlanThenExecutePlannerTaskFamily)
	}

	if !slices.Contains(decision.ReasonCodes, "route.plan_execute_planner") {
		t.Fatalf("plan-execute planner reason missing: %#v", decision.ReasonCodes)
	}
}

func TestImplementationDispatchFallbackSearchRanksUIRenderPath(t *testing.T) {
	output := strings.Join([]string{
		`f11 internal/runtime/agent/runner.go:5174: return containsAny(lowerObjective, "banner")`,
		`f6 internal/surfaces/tui/app/approval.go:51:// invariant that the banner always matches a real request`,
		`f12 internal/surfaces/tui/app/model_state.go:1375: func (m *Model) errorBanner() string {`,
		`f10 internal/surfaces/tui/app/rendering.go:50: if banner := m.errorBanner(); banner != "" {`,
		`f10 internal/surfaces/tui/app/rendering.go:51: footerLines = append(footerLines, errorStyle.Render(banner))`,
		`f5 internal/frontier/locate.go:169: if ContainsAny(normalized, "top", "header", "banner") {`,
	}, "\n")

	paths := implementationDispatchRankSearchPaths("replace the model path in the top-right banner with the actual model alias", output, 3)
	if len(paths) == 0 || paths[0] != "internal/surfaces/tui/app/rendering.go" {
		t.Fatalf("expected rendering.go first, got %#v", paths)
	}
}

func TestImplementationDispatchFallbackSearchKeepsNonGoSourcePaths(t *testing.T) {
	output := strings.Join([]string{
		`f1 web/src/components/Banner.tsx:42: <HeaderBanner modelAlias={activeAlias} />`,
		`f2 docs/banner.md:4: banner model alias notes`,
	}, "\n")

	paths := implementationDispatchRankSearchPaths("replace the banner model path with the actual model alias", output, 3)
	if len(paths) == 0 || paths[0] != "web/src/components/Banner.tsx" {
		t.Fatalf("expected TSX source path to remain eligible, got %#v", paths)
	}
}

func TestAppendDiscoveredCandidateScopeStaysInsideBriefBody(t *testing.T) {
	prompt := pathparse.WrapBriefBody("# Objective\n\nUpdate the banner.")
	got := recovery.AppendDiscoveredCandidateScopeAddendum(prompt, []string{"internal/surfaces/tui/app/rendering.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	if !strings.Contains(scoped, "allowed_edit_paths:") || !strings.Contains(scoped, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("discovered candidate scope was not inserted inside brief body:\n%s", got)
	}

	if allowed := plancontract.AllowedEditPathsFromPrompt(got); len(allowed) != 1 || allowed[0] != "internal/surfaces/tui/app/rendering.go" {
		t.Fatalf("discovered candidate scope is not parseable as allowed paths: %#v\n%s", allowed, got)
	}
}

func TestImplementationDispatchContextPackScopeUsesCandidateFiles(t *testing.T) {
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
	if !slices.Equal(paths, want) {
		t.Fatalf("context-pack scope = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchContextPackTestPathsUsesCandidateTests(t *testing.T) {
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
	if !slices.Equal(paths, want) {
		t.Fatalf("context-pack test paths = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchContextPackScopeUsesCandidateFiles_CompactFormat(t *testing.T) {
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
	if !slices.Equal(paths, want) {
		t.Fatalf("context-pack scope (compact format) = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchContextPackTestPathsUsesCandidateTests_CompactFormat(t *testing.T) {
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
	if !slices.Equal(paths, want) {
		t.Fatalf("context-pack test paths (compact format) = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchPreloadedContextPackBecomesInitialAllowedScope_CompactFormat(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling test: %v", err)
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

	paths := model.implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must reject temp symlinks and add regression tests proving no partial mutation",
		contextPack,
	)
	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}

	if !slices.Equal(paths, want) {
		t.Fatalf("preloaded context-pack scope (compact format) = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchPreloadedContextPackRejectsIrrelevantCandidateScope_CompactFormat(t *testing.T) {
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

	paths := model.implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must harden predictable temp-path writes against pre-existing symlinks and add regression tests",
		contextPack,
	)
	if len(paths) != 0 {
		t.Fatalf("irrelevant preloaded context-pack scope (compact format) should be ignored, got %#v", paths)
	}
}

func TestImplementationDispatchAllowedPathsFromBuildContextPackSection_Integration(t *testing.T) {
	// Create a minimal Go workspace so BuildPromptContextPackSection can produce
	// a real compact-projection output, then verify the TUI parsers extract the
	// expected paths from it.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ctxpack\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	editPath := filepath.Join(root, "pkg", "target.go")
	if err := os.WriteFile(editPath, []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "noise.go"), []byte("package pkg\n\nfunc Noise() string { return \"noise\" }\n"), 0o644); err != nil {
		t.Fatal(err)
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

	if strings.TrimSpace(section) == "" {
		t.Fatalf("BuildPromptContextPackSection returned empty; stderr=%s", stderr.String())
	}

	// Verify the section uses the new compact format markers.
	if !strings.Contains(section, "candidate_files:") {
		t.Fatalf("expected candidate_files: header in section, got:\n%s\nstderr=%s", section, stderr.String())
	}

	// Parse via the same path used in production: AllowedPathsFromContextPack + TestPaths.
	paths := implementationDispatchAllowedPathsFromContextPack(section, 8)
	testPaths := implementationDispatchTestPathsFromContextPack(section, 8)
	allPaths := append(paths, testPaths...)

	if len(allPaths) == 0 {
		t.Fatalf("parsers returned zero paths from real BuildPromptContextPackSection output; section:\n%s\nstderr=%s", section, stderr.String())
	}

	// At minimum, pkg/target.go should be found since it was a required edit path.
	foundTarget := false

	for _, p := range allPaths {
		if strings.Contains(p, "pkg/target.go") {
			foundTarget = true
			break
		}
	}

	if !foundTarget {
		t.Fatalf("expected pkg/target.go in parsed paths, got %v; section:\n%s\nstderr=%s", allPaths, section, stderr.String())
	}
}

func TestImplementationDispatchPreloadedContextPackBecomesInitialAllowedScope(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling test: %v", err)
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

	paths := model.implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must reject temp symlinks and add regression tests proving no partial mutation",
		contextPack,
	)
	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}

	if !slices.Equal(paths, want) {
		t.Fatalf("preloaded context-pack scope = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchPreloadedContextPackRejectsIrrelevantCandidateScope(t *testing.T) {
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

	paths := model.implementationDispatchAllowedPathsFromPreloadedContextPack(
		"status migration apply must harden predictable temp-path writes against pre-existing symlinks and add regression tests",
		contextPack,
	)
	if len(paths) != 0 {
		t.Fatalf("irrelevant preloaded context-pack scope should be ignored, got %#v", paths)
	}
}

func TestImplementationDispatchFallbackSearchPatternSkipsProcessWords(t *testing.T) {
	pattern := implementationDispatchFallbackSearchPattern("We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: status migration apply must harden predictable temp-path writes against pre-existing symlinks.")
	want := "status|migration|apply|harden"

	if pattern != want {
		t.Fatalf("fallback pattern = %q, want %q", pattern, want)
	}

	patterns := implementationDispatchFallbackFilePatterns("status migration apply must harden temp path writes")
	if len(patterns) < 2 || patterns[0] != "status_migration_apply" || patterns[1] != "migration_apply_harden" {
		t.Fatalf("fallback file patterns = %#v", patterns)
	}

	paths := implementationDispatchPathsFromSearchFilesOutput("f1 internal/workflows/productworkflow/migration_apply.go\nf2 internal/workflows/productworkflow/migration_apply_test.go\n", 4)
	wantPaths := []string{"internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"}

	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("search_files paths = %#v, want %#v", paths, wantPaths)
	}
}

func TestImplementationDispatchMissingTestRetryPreservesCandidateScope(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling test: %v", err)
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "missing-test-retry",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/workflows/productworkflow/migration_apply.go"},
	}

	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(stateDir, "missing-test-retry.json"), body, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	paths := model.implementationDispatchAllowedPathsForMissingRequestedTestEdit(implementationDispatchPlanExecuteFailure{
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

	want := []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/capabilities/workspace/capabilities_test.go",
		"internal/surfaces/tui/app/lifecycle.go",
		"internal/workflows/productworkflow/migration_apply_test.go",
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("missing-test retry scope = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchMissingImplementationRetryPreservesCandidateScope(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go")
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling source: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling test: %v", err)
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "missing-implementation-retry",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/workflows/productworkflow/migration_apply_test.go"},
	}

	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(stateDir, "missing-implementation-retry.json"), body, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	paths := model.implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(implementationDispatchPlanExecuteFailure{
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

	want := []string{
		"internal/workflows/productworkflow/migration_apply_test.go",
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/tui/app/lifecycle.go",
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("missing-implementation retry scope = %#v, want %#v", paths, want)
	}
}

func TestImplementationDispatchMissingImplementationRetryKeepsSubmittedSiblingAndContextCandidatesAllowed(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "internal", "surfaces", "cli", "product", "product_workflow.go")
	testPath := filepath.Join(root, "internal", "surfaces", "cli", "product", "product_workflow_test.go")
	statusPath := filepath.Join(root, "internal", "workflows", "productworkflow", "status.go")

	for _, path := range []string{sourcePath, testPath, statusPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}

		if err := os.WriteFile(path, []byte("package productworkflow\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	stateDir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	state := planexecute.ProgressState{
		SchemaVersion: planexecute.ProgressStateSchemaVersion,
		SessionID:     "product-workflow-missing-implementation",
		Phase:         plancontract.PlanExecuteProgressPhaseComplete,
		Status:        plancontract.PlanExecuteProgressStatusCompleted,
		ChangedPaths:  []string{"internal/surfaces/cli/product/product_workflow_test.go"},
	}

	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(stateDir, "product-workflow-missing-implementation.json"), body, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
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
	allowedPaths := model.implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure)

	for _, want := range []string{
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/cli/product/product_workflow.go",
		"internal/workflows/productworkflow/status.go",
	} {
		if !slices.Contains(allowedPaths, want) {
			t.Fatalf("missing-implementation candidate scope missing %q, got %#v", want, allowedPaths)
		}
	}

	retryOptions := implementationDispatchRetryOptionsWithCandidateScope(failure.Options, allowedPaths)

	retryOptions = implementationDispatchRetryOptionsWithEditSetObligation(retryOptions, plancontract.EditSetObligationKindImplementationEdit)

	if len(retryOptions.RequiredEditPaths) != 0 {
		t.Fatalf("retry required paths must remain empty for candidate-only recovery, got %#v", []string(retryOptions.RequiredEditPaths))
	}

	for _, want := range []string{
		"internal/surfaces/cli/product/product_workflow_test.go",
		"internal/surfaces/cli/product/product_workflow.go",
		"internal/workflows/productworkflow/status.go",
	} {
		if !slices.Contains([]string(retryOptions.AllowedEditPaths), want) {
			t.Fatalf("retry allowed paths missing %q, got %#v", want, []string(retryOptions.AllowedEditPaths))
		}
	}

	if !retryOptions.EditSetObligations.Has(plancontract.EditSetObligationKindImplementationEdit) {
		t.Fatalf("retry options missing implementation edit obligation: %#v", retryOptions.EditSetObligations)
	}
}

func TestImplementationDispatchMissingEditRetryKeepsCandidateScopeAllowed(t *testing.T) {
	opts := implementationDispatchRetryOptionsWithCandidateScope(planexecute.Options{
		RequiredEditPaths: []string{"pkg/explicit.go"},
		AllowedEditPaths:  []string{"pkg/existing_candidate.go"},
	}, []string{
		"pkg/feature_test.go",
		"pkg/feature.go",
	})

	wantRequired := []string{"pkg/explicit.go"}
	if !slices.Equal([]string(opts.RequiredEditPaths), wantRequired) {
		t.Fatalf("retry required paths = %#v, want %#v", []string(opts.RequiredEditPaths), wantRequired)
	}

	wantAllowed := []string{"pkg/existing_candidate.go", "pkg/feature_test.go", "pkg/feature.go"}
	if !slices.Equal([]string(opts.AllowedEditPaths), wantAllowed) {
		t.Fatalf("retry allowed paths = %#v, want %#v", []string(opts.AllowedEditPaths), wantAllowed)
	}

	if !slices.Equal(opts.ScopeContract.RequiredCoveragePaths, wantRequired) {
		t.Fatalf("retry scope required coverage = %#v, want %#v", opts.ScopeContract.RequiredCoveragePaths, wantRequired)
	}

	if !slices.Equal(opts.ScopeContract.AllowedCandidatePaths, wantAllowed) {
		t.Fatalf("retry scope allowed candidates = %#v, want %#v", opts.ScopeContract.AllowedCandidatePaths, wantAllowed)
	}
}

func TestImplementationDispatchRecoveryLoopGuardIgnoresPromptObligations(t *testing.T) {
	promptOpts := planexecute.Options{
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "prompt"),
	}
	if implementationDispatchOptionsAlreadyCarriedEditSetObligation(promptOpts, plancontract.EditSetObligationKindTestEdit) {
		t.Fatalf("initial prompt-derived obligations should still allow one recovery retry")
	}

	retryOpts := implementationDispatchRetryOptionsWithEditSetObligation(promptOpts, plancontract.EditSetObligationKindTestEdit)
	if !implementationDispatchOptionsAlreadyCarriedEditSetObligation(retryOpts, plancontract.EditSetObligationKindTestEdit) {
		t.Fatalf("recovery-added obligations should trip the loop guard")
	}
}

func TestMissingRequestedImplementationAddendumMarksPathsAsCandidateScope(t *testing.T) {
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
		if !strings.Contains(got, want) {
			t.Fatalf("implementation recovery addendum missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "required_edit_paths:") {
		t.Fatalf("implementation recovery addendum must not promote candidates to required paths:\n%s", got)
	}
}

func TestMissingRequestedTestAddendumMarksPathsAsCandidateScope(t *testing.T) {
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
		if !strings.Contains(got, want) {
			t.Fatalf("test recovery addendum missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "required_edit_paths:") {
		t.Fatalf("test recovery addendum must not promote candidates to required paths:\n%s", got)
	}
}

func TestAppendMissingRequestedTestEditRecoveryAddendumStaysInsideBriefBody(t *testing.T) {
	prompt := pathparse.WrapBriefBody("# Objective\n\nUpdate the banner and add regression tests.")
	got := recovery.AppendMissingRequestedTestEditRecoveryAddendum(prompt, []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering_test.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	if !strings.Contains(scoped, "Missing Requested Test Edit Recovery") || !strings.Contains(scoped, "internal/surfaces/tui/app/rendering_test.go") {
		t.Fatalf("missing-test recovery scope was not inserted inside brief body:\n%s", got)
	}

	if allowed := plancontract.AllowedEditPathsFromPrompt(got); !slices.Contains(allowed, "internal/surfaces/tui/app/rendering_test.go") {
		t.Fatalf("missing-test recovery scope is not parseable as allowed paths: %#v\n%s", allowed, got)
	}
}

func TestWriterRouteDecisionForPromptReportsDirectImplementation(t *testing.T) {
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

	decision := m.writerRouteDecisionForPrompt("modify internal/surfaces/tui/app/writer_route.go", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("unexpected direct implementation writer route decision: %#v", decision)
	}

	if decision.VerificationOwner != "executor" {
		t.Fatalf("direct implementation verification owner = %q, want executor", decision.VerificationOwner)
	}

	if !slices.Contains(decision.ReasonCodes, "route.local_model_default") {
		t.Fatalf("direct implementation reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForClassifierImplementationUsesGuardedWriter(t *testing.T) {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = ""

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.BuildMode(),
		route: turnRoutePlan{Family: "implementation", RouteSource: routeSourceClassifier},
	}

	decision := m.writerRouteDecisionForPrompt("update target.txt", plan)

	if decision.WriterMode != string(writerroute.ModeAgenticGuarded) || decision.Lane != "implement" {
		t.Fatalf("classifier implementation should use guarded writer, got %#v", decision)
	}

	if !slices.Contains(decision.ReasonCodes, "route.local_model_default") {
		t.Fatalf("guarded writer reason missing: %#v", decision.ReasonCodes)
	}
}

func TestWriterRouteDecisionForPromptReportsReadOnlyRoute(t *testing.T) {
	cfg := config.Default()

	cfg.Workspace.EnableWrites = true
	cfg.Chat.ImplementationDispatch = "plan_execute"

	m := &Model{cfg: cfg}
	plan := promptExecutionPlan{
		mode:  modepolicy.PlanMode(),
		route: turnRoutePlan{Family: "architecture"},
	}

	decision := m.writerRouteDecisionForPrompt("explain the route decision design", plan)

	if decision.WriterMode != string(writerroute.ModeReadOnly) || decision.Lane != "main" {
		t.Fatalf("unexpected read-only writer route decision: %#v", decision)
	}

	if decision.VerificationOwner != "" {
		t.Fatalf("read-only route should not claim verification ownership: %#v", decision)
	}

	if !slices.Contains(decision.ReasonCodes, "route.read_only_question") {
		t.Fatalf("read-only route reason missing: %#v", decision.ReasonCodes)
	}
}

func TestRunPromptOnceUnscopedImplementationUsesDirectImplementWhenDispatchDisabled(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("request path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))

		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			if strings.Contains(requestText, "Plan-then-execute (planner phase)") {
				t.Fatalf("unscoped TUI implementation should not enter plan-execute planner:\n%s", requestText)
			}

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

	answer, err := model.runImplementCommandOnce(context.Background(), "update target.txt")
	if err != nil {
		t.Fatalf("runImplementCommandOnce: %v\nanswer: %s", err, answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(got) != "new\n" {
		t.Fatalf("target content = %q, want new", string(got))
	}
}

func TestDirectImplementationContextPreloadAppliesToNaturalUnscopedEdits(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	sourcePath := filepath.Join(root, "internal", "surfaces", "tui", "app", "rendering.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("package app\n\nfunc renderBanner() string { return \"top-right model path banner\" }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
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

	plan = model.recoverDirectImplementationContext("change the model string in the top-right banner from the path to the alias", plan)

	if !slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:direct_implementation_preload") {
		t.Fatalf("expected direct implementation context preload marker, got %#v", plan.runtimeBrief.TargetPacket)
	}

	packet := strings.Join(plan.runtimeBrief.TargetPacket, "\n")
	if !strings.Contains(packet, "Context Pack") && !strings.Contains(packet, "Context Discovery Fallback") {
		t.Fatalf("expected context preload or fallback packet, got:\n%s", packet)
	}

	if !slices.Contains(plan.runtimeBrief.SearchPolicy, "direct_implement_context_preloaded_before_model_turn") {
		t.Fatalf("expected direct context search policy, got %#v", plan.runtimeBrief.SearchPolicy)
	}
}

func TestDirectImplementationContextPreloadSkipsScopedPrompts(t *testing.T) {
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
	if model.shouldPreloadDirectImplementationContext("update target.txt", plan) {
		t.Fatal("prompt with an explicit workspace path should not need unscoped direct context preload")
	}

	plan.runtimeBrief.RequiredEditPaths = []string{"pkg/feature.go"}
	if model.shouldPreloadDirectImplementationContext("fix the feature behavior", plan) {
		t.Fatal("required edit paths already provide direct implementation scope")
	}
}

func TestRunPromptOnceSemanticRequiredPathsUsesDirectImplementWhenDispatchConfigured(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst Covered = false\n"), 0o644); err != nil {
		t.Fatalf("write feature test: %v", err)
	}

	var plannerChatRequests atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			plannerChatRequests.Add(1)
			t.Fatalf("semantic source/test implementation should not enter exact plan-execute planner")
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChatRequests atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "executor-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			if !strings.Contains(requestText, `"model":"executor-model"`) {
				t.Fatalf("direct implementation should send executor model id, got request:\n%s", requestText)
			}

			if strings.Contains(requestText, "Plan-then-execute (planner phase)") {
				t.Fatalf("semantic direct implementation should not include plan-execute planner prompt:\n%s", requestText)
			}

			reply := ""

			switch executorChatRequests.Add(1) {
			case 1:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature.go","old_text":"return false","new_text":"return true","description":"enable feature behavior"}`
			case 2:
				reply = `<tool_call>
{"tool":"edit_file","path":"pkg/feature_test.go","old_text":"Covered = false","new_text":"Covered = true","description":"add regression coverage marker"}`
			default:
				reply = "Updated pkg/feature.go and pkg/feature_test.go."
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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
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

	answer, err := model.runImplementCommandOnce(context.Background(), "Implement a semantic TDD feature fix and add regression tests. Required edit paths: pkg/feature.go, pkg/feature_test.go.")
	if err != nil {
		t.Fatalf("runImplementCommandOnce: %v\nanswer: %s", err, answer)
	}

	if plannerChatRequests.Load() != 0 {
		t.Fatalf("semantic implementation should not use exact plan-execute planner, got %d request(s)", plannerChatRequests.Load())
	}

	if executorChatRequests.Load() == 0 {
		t.Fatalf("expected direct implementation to use executor chat")
	}

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	if !strings.Contains(string(gotSource), "return true") {
		t.Fatalf("source was not updated after %d executor request(s), answer=%q:\n%s", executorChatRequests.Load(), answer, string(gotSource))
	}

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	if err != nil {
		t.Fatalf("read test: %v", err)
	}

	if !strings.Contains(string(gotTest), "Covered = true") {
		t.Fatalf("test was not updated after %d executor request(s), answer=%q:\n%s", executorChatRequests.Load(), answer, string(gotTest))
	}
}

func TestRunPromptOnceNaturalLanguageTDDFeatureUsesClassifierImplementLane(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	for _, dir := range []string{".idiom", "docs", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "docs", "tdd-feature.md"), []byte(strings.Join([]string{
		"# TDD Feature Atom",
		"",
		"Implement the feature by changing pkg/feature.go and pkg/feature_test.go.",
		"Acceptance: feature returns true and regression coverage marker is true.",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write tdd feature atom: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst Covered = false\n"), 0o644); err != nil {
		t.Fatalf("write feature test: %v", err)
	}

	var (
		classifierRequests        atomic.Int64
		unexpectedPlannerRequests atomic.Int64
	)

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read planner request: %v", err)
			}

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
			t.Fatalf("natural-language TDD request should not enter planner/plan-execute path:\n%s", requestText)
		default:
			http.NotFound(w, r)
		}
	}))

	defer plannerServer.Close()

	var executorChatRequests atomic.Int64

	executorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "executor-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read executor request: %v", err)
			}

			requestText := string(body)
			if strings.Contains(requestText, "Plan-then-execute (planner phase)") {
				t.Fatalf("natural-language implementation should not enter plan-execute planner:\n%s", requestText)
			}

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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
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
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	if classifierRequests.Load() != 1 {
		t.Fatalf("expected one classifier request, got %d", classifierRequests.Load())
	}

	if unexpectedPlannerRequests.Load() != 0 {
		t.Fatalf("unexpected planner requests: %d", unexpectedPlannerRequests.Load())
	}

	if executorChatRequests.Load() == 0 {
		t.Fatalf("expected executor chat for classifier-routed implementation")
	}

	if strings.Contains(answer, "Do you want me to switch") || strings.Contains(answer, "/implement") {
		t.Fatalf("answer should not ask for implementation confirmation, got %q", answer)
	}

	if !strings.Contains(eventBuf.String(), `"writer_mode":"agentic_guarded"`) {
		t.Fatalf("natural-language implementation should reach guarded writer route, answer=%q events=%s", answer, eventBuf.String())
	}
}

func TestRunPromptOnceSemanticRequiredPathsFallsBackToActiveModelWhenExecutorUnavailable(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var plannerChatRequests atomic.Int64

	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read planner request: %v", err)
			}

			if !strings.Contains(string(body), `"model":"planner-model"`) {
				t.Fatalf("active-model fallback should send planner model id, got request:\n%s", string(body))
			}

			reply := "Updated target.txt."
			if plannerChatRequests.Add(1) == 1 {
				reply = `<tool_call>
{"tool":"write_file","path":"target.txt","content":"new\n","description":"update target through active-model fallback"}`
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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
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

	answer, err := model.runImplementCommandOnce(context.Background(), "Implement a semantic fix. Required edit paths: target.txt.")
	if err != nil {
		t.Fatalf("runImplementCommandOnce: %v\nanswer: %s", err, answer)
	}

	if plannerChatRequests.Load() == 0 {
		t.Fatalf("expected active-model fallback to use planner chat")
	}

	if executorChatRequests.Load() != 0 {
		t.Fatalf("unreachable executor should not receive chat, got %d request(s)", executorChatRequests.Load())
	}

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(got) != "new\n" {
		t.Fatalf("target content = %q, want new", string(got))
	}
}

func TestPlanExecutePlannerBlockedNoScopeSurfacesWithoutRetry(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)

	defer model.Close()

	answer, handled, err := model.handlePlanExecutePlannerBlocked(context.Background(), implementationDispatchPlanExecuteFailure{
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
	if err != nil || !handled {
		t.Fatalf("expected handled no-scope blocker, handled=%t err=%v answer=%q", handled, err, answer)
	}

	for _, want := range []string{
		"Plan-execute dispatch blocked before retry",
		"concrete edit scope",
		"completion gate rejected final answer",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("no-scope blocker missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanExecutePlannerBlockedMessageHidesInternalCompletionGatePrompt(t *testing.T) {
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
		if !strings.Contains(answer, want) {
			t.Fatalf("planner blocked message missing %q:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"Completion gate rejected",
		"Missing completion evidence",
		"gate retry",
		"final answer repeatedly",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("planner blocked message leaked internal completion-gate detail %q:\n%s", forbidden, answer)
		}
	}
}

func TestPlanExecutePlannerBlockedMessageCompactsCanonicalPlannerQuestion(t *testing.T) {
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

	if !strings.Contains(answer, "Planner detail:") {
		t.Fatalf("expected compact planner detail section:\n%s", answer)
	}

	if strings.Contains(answer, "Planner answer:") {
		t.Fatalf("canonical blocker should not repeat the full planner answer:\n%s", answer)
	}

	if strings.Count(answer, "Required edit paths not changed:") != 0 {
		t.Fatalf("full planner changed-path list should be omitted because required paths are already listed:\n%s", answer)
	}

	if !strings.Contains(answer, "read source for internal/workflows/productworkflow/coverage_gate.go") {
		t.Fatalf("compact detail should preserve the actionable blocker:\n%s", answer)
	}
}

func TestPlanExecutePlannerBlockedMessageNamesExecutorRejectedSubmittedPlan(t *testing.T) {
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
		if !strings.Contains(answer, want) {
			t.Fatalf("executor failure message missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanExecuteRepeatedCompletionGateSurfacesDeterministicMessage(t *testing.T) {
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
		if !strings.Contains(answer, want) {
			t.Fatalf("deterministic repeated-gate message missing %q:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"Manually invoke", "system rejected", "submit_plan tool"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("deterministic repeated-gate message leaked %q:\n%s", forbidden, answer)
		}
	}
}

func TestPlanExecutePlannerBlockedCanRetryOnActiveModel(t *testing.T) {
	failure := implementationDispatchPlanExecuteFailure{
		RequiredPaths: []string{"internal/surfaces/cli/product/product_workflow.go"},
		Result: planexecute.RunResult{
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: "<<QUESTION>>: completion_gate_required_paths_remaining_repeated",
			PlannerAnswer: "Missing completion evidence: plan-execute planner phase requires a successful submit_plan tool result before final answer.",
		},
	}
	if !recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		t.Fatalf("expected repeated submit_plan completion failure with exact scope to retry on active model")
	}

	failure.Result.FailureReason = "<<QUESTION>>: required_edit_paths_remaining"
	failure.Result.PlannerAnswer = "Planner failed after submit_plan rejected: required edit path coverage mismatch; missing required edit path(s): `internal/surfaces/cli/product/product_workflow_test.go`; invalid edit anchor(s): `internal/surfaces/cli/product/product_workflow.go: old_text not found in current source`."

	if !recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		t.Fatalf("expected invalid submit_plan with exact scope to retry on active model")
	}

	failure.Result.FailureReason = "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged."
	failure.Result.PlannerAnswer = ""
	failure.Stdout = "Blocker:\n- Plan submission required: every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request whose `edits[]` coverage includes every required path."

	if !recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		t.Fatalf("expected post-readiness discovery loop with exact scope to retry on active model")
	}

	failure.Options.UseActiveModel = true
	if recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		t.Fatalf("active-model retry must not recursively retry on active model")
	}

	failure.Options.UseActiveModel = false
	failure.RequiredPaths = nil

	if recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		t.Fatalf("unscoped planner blocker should not use active-model fallback")
	}
}

func TestPlanExecutePlannerBlockedMessageDoesNotClaimUnreadSourcesAfterSourceSatisfied(t *testing.T) {
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

	if strings.Contains(strings.ToLower(answer), "has not read") || strings.Contains(strings.ToLower(answer), "please allow the planner to read") {
		t.Fatalf("source-satisfied failure must not surface contradictory unread-source guidance:\n%s", answer)
	}

	if !strings.Contains(answer, "kept choosing discovery instead of emitting a `submit_plan`") {
		t.Fatalf("expected source-satisfied blocker summary, got:\n%s", answer)
	}
}

func TestPlanExecuteActiveModelFallbackAddendumInsertedBeforeBriefEnd(t *testing.T) {
	prompt := "# Objective\n\nUpdate files.\n\n" + pathparse.BriefBodyEndMarker + "\n\n# Tooling"
	got := recovery.AppendPlanExecuteActiveModelFallbackAddendum(prompt)

	if !strings.Contains(got, "## Active Model Fallback") {
		t.Fatalf("fallback addendum missing:\n%s", got)
	}

	if strings.Index(got, "## Active Model Fallback") > strings.Index(got, pathparse.BriefBodyEndMarker) {
		t.Fatalf("fallback addendum should stay inside brief body:\n%s", got)
	}

	if !strings.Contains(got, "fresh tool transcript") || !strings.Contains(got, "source-body evidence from the previous attempt is not executable evidence") {
		t.Fatalf("fallback addendum should make source evidence freshness explicit:\n%s", got)
	}

	for _, forbidden := range []string{"already provided", "executor-role planner"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("fallback addendum must not use confusing stale wording %q:\n%s", forbidden, got)
		}
	}

	if !strings.Contains(got, "previous plan-execute planner attempt") {
		t.Fatalf("fallback addendum should name the planner attempt without executor-role wording:\n%s", got)
	}
}

func TestPlanExecutePlannerRecoveryAddendumUsesHarnessOwnedRetryContract(t *testing.T) {
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
		if !strings.Contains(got, want) {
			t.Fatalf("recovery addendum missing %q:\n%s", want, got)
		}
	}

	for _, forbidden := range []string{
		"Each entry must have `Decision: source-satisfied`",
		"include the full file content",
		"Recovery controller observation",
		"Produce one valid `submit_plan` call whose edits include at least one real edit for each required path",
		"After this retry has satisfied source-body obligations for every existing required path and any runtime-required git history evidence",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("recovery addendum leaked controller contract text %q:\n%s", forbidden, got)
		}
	}
}

func TestPlanExecutePlannerRecoveryAddendumPrioritizesRequiredSourceReadBlocker(t *testing.T) {
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

	if !ok || path != "internal/surfaces/cli/product/product_workflow_test.go" {
		t.Fatalf("source-read blocker path = %q, %v", path, ok)
	}

	got := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, recovery.PlanExecuteRecoveryDecision{
		Decision:      "retry",
		Reason:        "source read blocker",
		RetryAddendum: "First read source for `internal/surfaces/cli/product/product_workflow_test.go`.",
	}, "")
	if !strings.Contains(got, "First read source for `internal/surfaces/cli/product/product_workflow_test.go`") {
		t.Fatalf("recovery addendum should prioritize the required source read:\n%s", got)
	}

	idxRead := strings.Index(got, "First read source")
	idxHistory := strings.Index(got, "then satisfy runtime-required git history evidence")
	idxSubmit := strings.Index(got, "then produce one valid `submit_plan`")

	if idxRead < 0 || idxHistory < 0 || idxSubmit < 0 || idxRead > idxHistory || idxHistory > idxSubmit {
		t.Fatalf("source read instruction should precede history evidence, then submit_plan:\n%s", got)
	}
}

func TestPlanExecutePlannerRecoveryDetectsAnchorRefreshBlocker(t *testing.T) {
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
	if !ok || path != "native/src/core/colony_sim_tick.cpp" {
		t.Fatalf("anchor-refresh blocker path = %q, %v", path, ok)
	}

	got := planExecutePlannerRecoveryRetryInstruction(failure)
	if !strings.Contains(got, "First read source for `native/src/core/colony_sim_tick.cpp`") {
		t.Fatalf("recovery instruction should prioritize anchor-refresh path:\n%s", got)
	}
}

func TestPlanExecutePlannerRecoveryDetectsTruncatedAnchorRefreshBlocker(t *testing.T) {
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
	if !ok {
		t.Fatal("expected truncated anchor-refresh blocker to be machine-actionable")
	}

	if !slices.Equal(refresh.Paths, []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"}) {
		t.Fatalf("refresh paths = %#v", refresh.Paths)
	}

	got := planExecutePlannerRecoveryRetryInstruction(failure)
	for _, want := range []string{
		"First refresh source-body evidence for each candidate anchor path",
		"native/src/core/colony_sim_tick.cpp",
		"native/src/simulation/world/collapse_event.h",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recovery instruction missing %q:\n%s", want, got)
		}
	}
}

func TestFeaturePlannerAtomPlanExecuteRetriesTypedAnchorRefreshCompletion(t *testing.T) {
	var calls int
	var retryPrompt string
	model := &Model{cfg: config.Default()}

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

	completion, err := model.runFeaturePlannerAtomPlanExecuteWithRunner(context.Background(), "implement collapse helper", nil, false, runOnce)
	if err != nil {
		t.Fatalf("runFeaturePlannerAtomPlanExecuteWithWriteEnvelope: %v", err)
	}

	if calls != 2 {
		t.Fatalf("plan-execute calls = %d, want 2", calls)
	}

	for _, want := range []string{
		"## Plan-execute Recovery Addendum",
		"native/src/simulation/world/collapse_event.h",
		"First read source for `native/src/simulation/world/collapse_event.h`",
	} {
		if !strings.Contains(retryPrompt, want) {
			t.Fatalf("retry prompt missing %q:\n%s", want, retryPrompt)
		}
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(completion), &fields); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if got := fmt.Sprint(fields["status"]); got != "completed" {
		t.Fatalf("status = %q, want completed; fields=%+v", got, fields)
	}
	if got := strings.TrimSpace(fmt.Sprint(fields["failure_kind"])); got != "" && got != "<nil>" {
		t.Fatalf("stale first-run failure leaked into final completion: fields=%+v", fields)
	}
}

func TestFeaturePlannerAtomPlanExecuteDoesNotRetryHumanBlocker(t *testing.T) {
	var calls int
	model := &Model{cfg: config.Default()}

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
	if err != nil {
		t.Fatalf("runFeaturePlannerAtomPlanExecuteWithRunner: %v", err)
	}

	if calls != 1 {
		t.Fatalf("plan-execute calls = %d, want no retry", calls)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(completion), &fields); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if got := fmt.Sprint(fields["next_valid_action"]); got != string(recoverycontract.NextActionAnswerBlocker) {
		t.Fatalf("next_valid_action = %q, want answer_blocker; fields=%+v", got, fields)
	}
}

func TestFeaturePlannerAtomPlanExecuteReturnsSecondFailureAfterRetry(t *testing.T) {
	var calls int
	model := &Model{cfg: config.Default()}

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
	if err != nil {
		t.Fatalf("runFeaturePlannerAtomPlanExecuteWithRunner: %v", err)
	}

	if calls != 2 {
		t.Fatalf("plan-execute calls = %d, want retry then final failure", calls)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(completion), &fields); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if got := fmt.Sprint(fields["failure_kind"]); got != "verification_failed" {
		t.Fatalf("failure_kind = %q, want second-run failure; fields=%+v", got, fields)
	}
	if strings.Contains(fmt.Sprint(fields["failure_reason"]), "anchor refresh") {
		t.Fatalf("stale first-run failure leaked into second-run completion: fields=%+v", fields)
	}
}

func TestPlanExecutePlannerRecoveryInstructionKeepsAllowedCandidatesNonMandatory(t *testing.T) {
	got := planExecutePlannerRecoveryRetryInstruction(implementationDispatchPlanExecuteFailure{
		AllowedPaths: []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering_test.go"},
	})
	for _, want := range []string{
		"allowed candidate paths",
		"edit allowlist",
		"do not treat every candidate path as mandatory coverage",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("allowed-scope retry instruction missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "whose edits include at least one real edit for each required path") {
		t.Fatalf("allowed candidate retry must not use required-path coverage wording:\n%s", got)
	}
}

func TestPlanExecutePlannerRecoveryInstructionPreservesHistoryGate(t *testing.T) {
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
		if !strings.Contains(got, want) {
			t.Fatalf("required-scope retry instruction missing %q:\n%s", want, got)
		}
	}

	if idxHistory, idxSubmit := strings.Index(got, "runtime-required git history evidence"), strings.Index(got, "one valid `submit_plan` call"); idxHistory < 0 || idxSubmit < 0 || idxHistory > idxSubmit {
		t.Fatalf("history evidence instruction should precede submit_plan instruction:\n%s", got)
	}
}

func TestPlanExecuteMissingEditRecoveryUsesTypedFailureKindOnly(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			if got := implementationDispatchShouldChainMissingEditRecovery(tt.result); got != tt.wantChain {
				t.Fatalf("chain missing-edit recovery = %t, want %t", got, tt.wantChain)
			}

			gotObligation, ok := plancontract.PlanExecuteFailureKindEditSetObligation(implementationDispatchPlanExecuteRunFailureKind(tt.result))
			if tt.wantObligation == "" {
				if ok {
					t.Fatalf("unexpected obligation %q from failure kind %q", gotObligation, tt.result.FailureKind)
				}

				return
			}

			if !ok || gotObligation != tt.wantObligation {
				t.Fatalf("obligation = %q, %t; want %q, true", gotObligation, ok, tt.wantObligation)
			}
		})
	}
}

func TestImplementationDispatchRecoveryMessageIsInternalWithQuotedSubmitPlanToolCall(t *testing.T) {
	msg := "Respond with a valid `submit_plan` tool call only; do not narrate completion."
	if !implementationDispatchRecoveryMessageIsInternal(msg) {
		t.Fatalf("expected quoted submit_plan tool-call instruction to be internal")
	}

	if observation := planExecutePlannerRecoveryObservation(msg); observation != "" {
		t.Fatalf("expected internal retry addendum to be suppressed, got %q", observation)
	}
}

func TestPlanExecuteTerminalOutcomeMatrixForTUIWrapper(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			if got := recovery.PlanExecuteFailureCanUseRecovery(implementationDispatchPlanExecuteRunFailureKind(tt.failure.Result)); got != tt.recovery {
				t.Fatalf("recovery eligibility = %t, want %t", got, tt.recovery)
			}

			answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(tt.failure)
			if ok != tt.sourceMismatch {
				t.Fatalf("source-satisfied mismatch = %t, want %t; answer=%q", ok, tt.sourceMismatch, answer)
			}

			for _, want := range tt.want {
				if !strings.Contains(answer, want) {
					t.Fatalf("answer missing %q:\n%s", want, answer)
				}
			}

			for _, forbidden := range tt.forbidden {
				if strings.Contains(answer, forbidden) {
					t.Fatalf("answer leaked %q:\n%s", forbidden, answer)
				}
			}
		})
	}
}

func TestPlanExecuteSourceSatisfiedCoverageIgnoresAllowedCandidatePaths(t *testing.T) {
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
	if ok {
		t.Fatalf("allowed candidate paths must not be promoted to source-satisfied coverage obligations:\n%s", answer)
	}
}

func TestPlanExecuteSourceSatisfiedCoverageBlockerUsesExactMissingPaths(t *testing.T) {
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
	if !ok {
		t.Fatalf("expected exact source-satisfied mismatch blocker")
	}

	if !strings.Contains(answer, "\n- pkg/parser.go") {
		t.Fatalf("blocker should name the exact missing path, got:\n%s", answer)
	}
}

func TestPlanExecuteSourceSatisfiedCoverageBlockerReportsExtensionlessCoveredPath(t *testing.T) {
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
	if !ok {
		t.Fatalf("expected extensionless source-satisfied mismatch blocker")
	}

	for _, want := range []string{"Covered source-satisfied evidence paths:\n- Dockerfile", "Missing required edit paths:\n- BUILD"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("blocker missing %q:\n%s", want, answer)
		}
	}
}

func TestHandleStreamDoneClearsPlanPhaseOnTerminalPlannerFailure(t *testing.T) {
	m := &Model{
		streaming:     true,
		planPhaseText: "Planning: Analyzing objective and creating plan...",
	}

	m.handleStreamDone(streamDoneMsg{err: errors.New("planner phase reported a blocker before submitting a plan")})

	if m.streaming {
		t.Fatal("streaming should be false after terminal planner failure")
	}

	if m.planPhaseText != "" {
		t.Fatalf("terminal planner failure must not leave active plan phase text, got %q", m.planPhaseText)
	}

	if !strings.Contains(m.status, "Chat failed: planner phase reported a blocker") {
		t.Fatalf("expected terminal error status, got %q", m.status)
	}
}

func TestImplementationDispatchRefineDiagnosticsDoNotTruncateActionableLine(t *testing.T) {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
	diagnostics := implementationdispatch.RefineDiagnostics(implementationdispatch.PlanDocumentRefineResult{
		Stderr: "noise\nrefine: iter 1\n" + longBlocker + "\n",
	})
	joined := strings.Join(diagnostics, "\n")

	if !strings.Contains(joined, longBlocker) {
		t.Fatalf("diagnostics lost actionable blocker:\n%s", joined)
	}

	if strings.Contains(joined, "[truncated") {
		t.Fatalf("diagnostics should not include truncation marker:\n%s", joined)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeDebugLogsFromHeuristics(t *testing.T) {
	plan := fallbackTurnRoute("Check our most recent logs and explain why it failed.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestRouteClassifierChatClientFallsBackToPlannerWhenClassifierUnreachable(t *testing.T) {
	planner := &reachableListModelsClient{models: []llama.ModelInfo{{ID: "planner"}}}
	classifier := &failingListModelsClient{err: errors.New("classifier down")}
	model := &Model{
		client:        planner,
		summaryClient: classifier,
	}

	if got := model.routeClassifierChatClient(); got != planner {
		t.Fatalf("expected active planner client fallback, got %#v", got)
	}
}

func TestCorrectTurnRoutePlanCorrectsConceptualDebugLogQuestionToArchitecture(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "What does the debug log redacted mode do?", modepolicy.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}

	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsExplicitLogArtifactRequestsOnStandardReadLane(t *testing.T) {
	prompts := []string{
		"Check our most recent logs and explain why it failed.",
		"Read logs/conversations/20260531-124928.794.jsonl and explain the failure.",
		"Search the telemetry logs for the latest panic.",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, prompt, modepolicy.BuildMode())
		if plan.Family != "conversation" {
			t.Fatalf("family for %q = %q, want conversation", prompt, plan.Family)
		}

		if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
			t.Fatalf("unexpected plan for %q: %#v", prompt, plan)
		}
	}
}

func TestCorrectTurnRoutePlanKeepsSourceDebugPromptOnStandardReadLane(t *testing.T) {
	prompt := "you are a debugging assistant to another agent. you are tasked with finding the cause of terminal resizing causing conversation window to become unscrollable. Please provide your proof with evidence, files, symbols, and line numbers."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, prompt, modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("source-debug prompt should use standard read lane, got %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsArchitectureForProjectFeatureQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "architecture"}, "What does the debug log redacted mode do?", modepolicy.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}

	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanCorrectsRepoInternalQuestionsToArchitecture(t *testing.T) {
	prompts := []string{
		"What does the debug log redacted mode do?",
		"Can you tell me how this harness uses subagents?",
		"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
	}
	for _, prompt := range prompts {
		plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, prompt, modepolicy.BuildMode())
		if plan.Family != "architecture" {
			t.Fatalf("family for %q = %q, want architecture", prompt, plan.Family)
		}

		if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
			t.Fatalf("unexpected plan for %q: %#v", prompt, plan)
		}
	}
}

func TestCorrectTurnRoutePlanKeepsOperationalHowToQuestionReadOnly(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, "How do I run plan-execute without changing files?", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsQuestionShapedUILayoutConversation(t *testing.T) {
	prompt := "The current model string in the top-right portion of the banner shows a path with what seems to be a stripped filename. Can we shorten this to be just the model's alias?"
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "direct_question"}, prompt, modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsSourceDiagnosisOnStandardReadLane(t *testing.T) {
	prompt := "you are a research assistant for another agentic process; please provide structured evidence in the form of files, line numbers, and relevant symbols. We are trying to debug an issue where resizing the TUI mid-answer caused the conversation to become non-scrollable. Please debug and present your findings."
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{
		Family:             "debug_logs",
		InitialLane:        "explore",
		Contract:           "grounded_explanation",
		RequestedOutcome:   "diagnose",
		WriteIntent:        "no",
		ReadOnlyConstraint: true,
	}, prompt, modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestNormalizeTurnRoutePlanDiagnoseDefaultsToConversation(t *testing.T) {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		RequestedOutcome: "diagnose",
		WriteIntent:      "no",
	}, modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanDoesNotUsePromptFallbackHeuristics(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "Run the tests and report failures.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesCodeExplanationAsConversation(t *testing.T) {
	plan := fallbackTurnRoute("How does this harness use sub-agents?", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteNeverActivatesLanesFromPromptHeuristics(t *testing.T) {
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
		if plan.Family != "conversation" {
			t.Fatalf("fallback heuristic activated lane for %q: %#v", prompt, plan)
		}

		if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
			t.Fatalf("unexpected conservative fallback for %q: %#v", prompt, plan)
		}
	}
}

func TestFallbackTurnRouteRecognizesExplicitFileDeleteFollowup(t *testing.T) {
	plan := fallbackTurnRoute("can you delete that new file now?", modepolicy.BuildMode())
	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation: %#v", plan.Family, plan)
	}

	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" || plan.WriteIntent != "yes" {
		t.Fatalf("unexpected delete follow-up route: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotTreatContentRemovalAsFileDeletion(t *testing.T) {
	plan := fallbackTurnRoute("remove obsolete text from temp.md", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("content removal should stay conservative, got %#v", plan)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected conservative fallback: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeVerificationOnlyFromHeuristics(t *testing.T) {
	plan := fallbackTurnRoute("Run the tests in the seeded failing fixture and report the failure without editing files.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeSuggestionQuestionAsArchitecture(t *testing.T) {
	plan := fallbackTurnRoute("What can we add to our discovery tools in an effort at increasing their overall usefulness?", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteKeepsQuestionShapedUILayoutRequestReadOnly(t *testing.T) {
	prompt := "can you re-align the text input field to be flush against the bottom of the terminal? When it needs to wrap to a new line, it should simply increase in height to avoid going off-screen"
	plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeLocateFromHeuristics(t *testing.T) {
	plan := fallbackTurnRoute("Find where the nonexistent banner text is rendered. If it is not present, say that clearly and do not edit files.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.FollowupLane != "" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeLocateThenRemoveAsImplementation(t *testing.T) {
	plan := fallbackTurnRoute("Find where the banner text is rendered, then remove it.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.FollowupLane != "" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeGDScriptEditsFromHeuristics(t *testing.T) {
	plan := fallbackTurnRoute("Update the panel script in ui/status_panel.gd.", modepolicy.BuildMode())
	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotRecognizeStartWorkingOnNamedDocAsDocsEdit(t *testing.T) {
	for _, prompt := range []string{
		"can you please start working on docs/writer-route-decision-report-only-plan.md?",
		"can you start woring on docs/writer-route-decision-report-only-plan.md?",
		"can you start implementing docs/writer-route-decision-report-only-plan.md?",
	} {
		plan := fallbackTurnRoute(prompt, modepolicy.BuildMode())
		if plan.Family != "conversation" {
			t.Fatalf("family for %q = %q, want conversation", prompt, plan.Family)
		}

		if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
			t.Fatalf("unexpected plan for %q: %#v", prompt, plan)
		}
	}
}

func TestCorrectTurnRoutePlanDoesNotDowngradeClassifierImplementationRoute(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Explain how this harness uses sub-agents.", modepolicy.BuildMode())
	if plan.Family != "explore_then_implement" {
		t.Fatalf("family = %q, want explore_then_implement", plan.Family)
	}

	if plan.InitialLane != "explore" || plan.FollowupLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestRunPromptOnceDispatchesStartImplementingPlanDocToPlanExecute(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/dispatch\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nconst Value = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var (
		chatRequests    atomic.Int64
		sawReviewPrompt atomic.Bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			chatRequests.Add(1)

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				if !strings.Contains(requestText, `"enable_thinking":false`) {
					t.Fatalf("review request should force visible terminal output with thinking disabled:\n%s", requestText)
				}

				sawReviewPrompt.Store(true)

				reply = "Decision: pass\n\nVerification Checklist:\n- pkg/target.go contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				t.Fatalf("complete exact plan-doc packet should auto-submit before planner model call:\n%s", requestText)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "pkg", "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(got) != "package pkg\n\nconst Value = \"new\"\n" {
		t.Fatalf("target content = %q, want updated Go file", string(got))
	}

	planArtifact := loadOnlyTimestampedPlanArtifactForTest(t, root)
	if planArtifact.SchemaVersion != planartifact.SchemaVersion {
		t.Fatalf("plan artifact schema = %q, want %q", planArtifact.SchemaVersion, planartifact.SchemaVersion)
	}

	if planArtifact.SessionID == "" {
		t.Fatalf("plan artifact should carry the plan-execute session id")
	}

	if len(planArtifact.Edits) != 1 || planArtifact.Edits[0].Path != "pkg/target.go" {
		t.Fatalf("plan artifact edits = %#v, want one pkg/target.go edit", planArtifact.Edits)
	}

	if !strings.Contains(answer, "plan-then-execute summary") {
		t.Fatalf("expected plan-execute summary answer, got %q", answer)
	}

	if !sawReviewPrompt.Load() {
		t.Fatalf("expected review-repair prompt after executor phase")
	}

	if chatRequests.Load() != 1 {
		t.Fatalf("expected only review chat request after auto-exact planning, got %d", chatRequests.Load())
	}
}

func TestRunPromptOnceBlocksAmbiguousPlanDocBasenameBeforePlanExecute(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	for _, dir := range []string{"docs", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}

		body := "# Plan\n\n## Files To Modify\n\n- target.txt\n"
		if err := os.WriteFile(filepath.Join(root, dir, "plan.md"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s plan doc: %v", dir, err)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce should return a typed ambiguity blocker, got err=%v answer=%q", err, answer)
	}

	for _, want := range []string{
		"Plan-execute dispatch blocked before the planner ran",
		"ambiguous shorthand",
		"docs/plan.md",
		"notes/plan.md",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("ambiguity answer missing %q:\n%s", want, answer)
		}
	}

	if chatRequests.Load() != 0 {
		t.Fatalf("planner should not be called for ambiguous design-doc shorthand, got %d chat request(s)", chatRequests.Load())
	}
}

func TestRunPromptOnceBlocksUnlintedPlanDocBeforePlanExecute(t *testing.T) {
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

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce should surface preflight blocker as an assistant answer, got err=%v answer=%q", err, answer)
	}

	for _, want := range []string{
		"automatic refine did not produce an accepted brief",
		".idiom/refine/tui-docs-plan/accepted.md",
		"brief shape is unknown",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("preflight answer missing %q:\n%s", want, answer)
		}
	}

	if chatRequests.Load() != 0 {
		t.Fatalf("planner should not be called, got %d chat request(s)", chatRequests.Load())
	}
}

func TestRunPromptOnceSurfacesRefinedCandidatePreflightBlocker(t *testing.T) {
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

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce should surface refined candidate blocker as an assistant answer, got err=%v answer=%q", err, answer)
	}

	for _, want := range []string{
		"Automatic refine produced a brief, but intake/lint still blocked plan-execute dispatch",
		refinedPath,
		"existing scoped paths have uncommitted changes",
		"dirty scoped paths: target.txt",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("refined preflight answer missing %q:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "automatic refine did not produce an accepted brief") {
		t.Fatalf("answer should name the refined candidate blocker instead of generic refine failure:\n%s", answer)
	}

	if chatRequests.Load() != 0 {
		t.Fatalf("planner should not be called, got %d chat request(s)", chatRequests.Load())
	}
}

func TestRunPromptOnceRefinesPlanDocBeforePlanExecuteDispatch(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	const refinedPath = ".idiom/refine/tui-docs-plan/accepted.md"

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

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- target.txt contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				t.Fatalf("refined exact dispatch brief should auto-submit before planner model call:\n%s", requestText)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(got) != "new\n" {
		t.Fatalf("target content = %q, want new line", string(got))
	}

	if !strings.Contains(answer, "plan-then-execute summary") {
		t.Fatalf("expected plan-execute summary answer, got %q", answer)
	}

	planArtifact := loadOnlyTimestampedPlanArtifactForTest(t, root)
	if len(planArtifact.Edits) != 1 || planArtifact.Edits[0].Path != "target.txt" {
		t.Fatalf("plan artifact edits = %#v, want one target.txt edit", planArtifact.Edits)
	}
}

func TestImplementationDispatchWorkspaceFileRejectsEscapesAndSymlinkAncestors(t *testing.T) {
	root := t.TempDir()

	if _, ok := implementationdispatch.WorkspaceFile(root, "../outside.md"); ok {
		t.Fatal("parent traversal path should be rejected")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	if _, ok := implementationdispatch.WorkspaceFile(root, "linked/escape.md"); ok {
		t.Fatal("path through symlinked workspace ancestor should be rejected")
	}

	if _, ok := implementationdispatch.WorkspaceFile(root, "docs/new.md"); !ok {
		t.Fatal("missing in-scope file should remain valid for new-file dispatch")
	}
}

func TestRunPromptOnceAtomizesDesignDocRefinesAtomsAndDispatchesPlanExecute(t *testing.T) {
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

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/atomize\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n\nconst A = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "b.go"), []byte("package pkg\n\nconst B = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte(designDoc), 0o644); err != nil {
		t.Fatalf("write design doc: %v", err)
	}

	var (
		plannerPromptRequests atomic.Int64
		sawAtomPrompt         atomic.Bool
	)

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Plan-Execute Planner Blocked Recovery"):
				reply = `{"decision":"surface","reason":"test planner should have received one scoped atom path","message":"planner recovery was reached unexpectedly"}`
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- Scoped fixture file contains the requested replacement.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			default:
				plannerPromptRequests.Add(1)

				if !strings.Contains(requestText, "### .idiom/refine/") ||
					!strings.Contains(requestText, "Required path coverage for this run") {

					t.Fatalf("planner request did not receive refined atom brief with required scope:\n%s", requestText)
				}

				sawAtomPrompt.Store(true)

				readPaths := []string{}

				switch {
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/a.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/a.go\n"):
					readPaths = []string{"pkg/a.go"}
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/b.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/b.go\n"):
					readPaths = []string{"pkg/b.go"}
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/c.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/c.go\n"):
					readPaths = []string{"pkg/c.go"}
				}

				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, readPaths...); ok {
					reply = readReply
					break
				}

				switch {
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/a.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/a.go\n"):
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/a.go.","edits":[{"path":"pkg/a.go","old_text":"const A = \"old\"\n","new_text":"const A = \"new\"\n","purpose":"replace A fixture text"}]}`
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/b.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/b.go\n"):
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/b.go.","edits":[{"path":"pkg/b.go","old_text":"const B = \"old\"\n","new_text":"const B = \"new\"\n","purpose":"replace B fixture text"}]}`
				case strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\\n- pkg/c.go\\n") ||
					strings.Contains(requestText, "Required path coverage for this run: 1 required path(s).\n- pkg/c.go\n"):
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/c.go.","edits":[{"path":"pkg/c.go","replace_all":true,"old_text":"","new_text":"package pkg\n\nconst C = \"new\"\n","purpose":"create C fixture text"}]}`
				default:
					t.Fatalf("planner request missing atom required path:\n%s", requestText)
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "design.md")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	gotA, err := os.ReadFile(filepath.Join(root, "pkg", "a.go"))
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}

	gotB, err := os.ReadFile(filepath.Join(root, "pkg", "b.go"))
	if err != nil {
		t.Fatalf("read b.go: %v", err)
	}

	if string(gotA) != "package pkg\n\nconst A = \"new\"\n" {
		t.Fatalf("a.go content = %q, want updated fixture", string(gotA))
	}

	if string(gotB) != "package pkg\n\nconst B = \"new\"\n" {
		t.Fatalf("b.go content = %q, want updated fixture", string(gotB))
	}

	gotC, err := os.ReadFile(filepath.Join(root, "pkg", "c.go"))
	if err != nil {
		t.Fatalf("read c.go: %v", err)
	}

	if string(gotC) != "package pkg\n\nconst C = \"new\"\n" {
		t.Fatalf("c.go content = %q, want created fixture", string(gotC))
	}

	atomDir := filepath.Join(root, ".idiom", "atoms", implementationdispatch.DocumentOutputTitle("docs/design.md"))
	for _, name := range []string{"atom-001.md", "atom-002.md", "atom-003.md"} {
		if _, err := os.Stat(filepath.Join(atomDir, name)); err != nil {
			t.Fatalf("expected materialized atom %s: %v", name, err)
		}
	}

	firstAtom, err := os.ReadFile(filepath.Join(atomDir, "atom-001.md"))
	if err != nil {
		t.Fatalf("read first atom: %v", err)
	}

	if !strings.Contains(string(firstAtom), "Host current-state anchors captured before dispatch") ||
		!strings.Contains(string(firstAtom), "pkg/a.go:3") ||
		!strings.Contains(string(firstAtom), `const A = "old"`) {

		t.Fatalf("existing-path atom should carry host exact anchors in Verified Current State:\n%s", string(firstAtom))
	}

	for _, want := range []string{
		"Status: source-satisfied - do not dispatch",
		"exact `old_text`/`new_text` blocks",
		"Do not use prose-only mutation sites",
	} {
		if !strings.Contains(string(firstAtom), want) {
			t.Fatalf("existing-path atom should carry dispatch contract %q:\n%s", want, string(firstAtom))
		}
	}

	thirdAtom, err := os.ReadFile(filepath.Join(atomDir, "atom-003.md"))
	if err != nil {
		t.Fatalf("read third atom: %v", err)
	}

	if !strings.Contains(string(thirdAtom), "NEW FILE:") {
		t.Fatalf("missing-path atom should be marked NEW FILE:\n%s", string(thirdAtom))
	}

	if !strings.Contains(string(thirdAtom), "Typed plan execution contract:") ||
		!strings.Contains(string(thirdAtom), `"authorized_new_files":[{"path":"pkg/c.go"}]`) {

		t.Fatalf("missing-path atom should carry typed new-file authority from parent doc:\n%s", string(thirdAtom))
	}

	if !strings.Contains(answer, "Design document implementation completed through 3 atom(s)") {
		t.Fatalf("expected atomized completion answer, got %q", answer)
	}

	if !sawAtomPrompt.Load() {
		t.Fatalf("expected planner to receive atom prompt")
	}

	if plannerPromptRequests.Load() != 6 {
		t.Fatalf("expected source-read and submit request per atom, got %d", plannerPromptRequests.Load())
	}
}

func TestRunImplementationPlanDocumentsSourceSatisfiedAtomSkipsRefineAndPlanExecute(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "events.go"), []byte(strings.Join([]string{
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
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
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

	answer, err := model.runImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	if err != nil {
		t.Fatalf("runImplementationPlanDocumentsViaPlanExecute: %v", err)
	}

	if !strings.Contains(answer, "Decision: source-satisfied") ||
		!strings.Contains(answer, "writer_route_decision") ||
		!strings.Contains(answer, "idiom.pkg.") {

		t.Fatalf("expected source-satisfied evidence answer, got:\n%s", answer)
	}

	if refineCalls.Load() != 0 {
		t.Fatalf("refine should not run for source-satisfied atom, got %d call(s)", refineCalls.Load())
	}

	if preflightCalls.Load() != 0 {
		t.Fatalf("preflight should not run before source-satisfied short-circuit, got %d call(s)", preflightCalls.Load())
	}
}

func TestRunImplementationPlanDocumentsSourceSatisfiedAtomMarksAtomComplete(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "events.go"), []byte(strings.Join([]string{
		"package pkg",
		"",
		"func emitRoute(stdout any, fields map[string]any) {",
		"	emitJSONEvent(stdout, \"writer_route_decision\", fields)",
		"}",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
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

	if err := os.MkdirAll(filepath.Dir(atomAbs), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
	}

	if err := os.WriteFile(atomAbs, []byte(atomContent), 0o644); err != nil {
		t.Fatalf("write atom: %v", err)
	}

	doc, ok := implementationdispatch.ReadPlanDocument(root, atomRel)
	if !ok {
		t.Fatalf("atom doc should be readable")
	}

	cfg := config.Default()

	cfg.Chat.ImplementationDispatch = "plan_execute"
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.MCP = nil

	model := New(cfg)

	defer model.Close()

	answer, err := model.runImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationdispatch.PlanDocument{doc})
	if err != nil {
		t.Fatalf("runImplementationPlanDocumentsViaPlanExecute: %v", err)
	}

	if !strings.Contains(answer, "Decision: source-satisfied") {
		t.Fatalf("expected source-satisfied answer, got:\n%s", answer)
	}

	updatedBody, err := os.ReadFile(atomAbs)
	if err != nil {
		t.Fatalf("read updated atom: %v", err)
	}

	updated := string(updatedBody)
	if !strings.Contains(updated, "Status: source-satisfied - do not dispatch") ||
		!strings.Contains(updated, "## Result") ||
		!strings.Contains(updated, "writer_route_decision") {

		t.Fatalf("source-satisfied atom doc should be closed with result evidence:\n%s", updated)
	}
}

func TestImplementationDispatchSourceSatisfiedUsesCurrentStateAnchorConcepts(t *testing.T) {
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
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
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
	if len(records) != len(docs) {
		t.Fatalf("records = %#v, want one source-satisfied record per atom", records)
	}

	for _, record := range records {
		if record.EditPath == "" || len(record.Evidence) == 0 {
			t.Fatalf("record should carry path evidence: %#v", record)
		}
	}
}

func TestImplementationDispatchSourceSatisfiedUsesCurrentSourceBodyWhenAnchorsAreStale(t *testing.T) {
	root := t.TempDir()
	path := "internal/contracts/writerroute/decision_test.go"
	full := filepath.Join(root, filepath.FromSlash(path))

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
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
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-002.md", path, "Unit tests for normalization, stable ID, JSON/event fields", []string{
		"`internal/contracts/writerroute/decision_test.go:8`: `// stale comment anchor no longer present`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc})
	if len(records) != 1 {
		t.Fatalf("records = %#v, want source-satisfied from current source body", records)
	}

	if !strings.Contains(strings.Join(records[0].Evidence, "\n"), "current source covers requested source-scope term") {
		t.Fatalf("expected source-body evidence, got %#v", records[0].Evidence)
	}
}

func TestImplementationDispatchSourceSatisfiedDoesNotTreatFallbackAnchorsAsDone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n\nconst A = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write pkg/a.go: %v", err)
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-001.md", "pkg/a.go", "Replace A's fixture value with new", []string{
		"`pkg/a.go:1`: `package pkg`",
		"`pkg/a.go:3`: `const A = \"old\"`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	if records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc}); len(records) != 0 {
		t.Fatalf("fallback/current old anchors must not source-satisfy pending work: %#v", records)
	}
}

func TestImplementationDispatchOptionalSourceSatisfiedRequiresLiveAnchor(t *testing.T) {
	root := t.TempDir()
	path := "internal/surfaces/tui/app/implementation_dispatch.go"
	full := filepath.Join(root, filepath.FromSlash(path))

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	if err := os.WriteFile(full, []byte(strings.Join([]string{
		"package app",
		"",
		"type planDocumentFixture struct {",
		"	Path string",
		"}",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	doc := sourceSatisfiedAtomDoc(".idiom/atoms/design/atom-008.md", path, "If needed, pass or annotate dispatch route without changing behavior", []string{
		"`internal/surfaces/tui/app/implementation_dispatch.go:3`: `func staleAnchor() {}`",
	})
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	if records := implementationdispatch.PlanDocumentsSourceSatisfied(root, []implementationdispatch.PlanDocument{doc}); len(records) != 0 {
		t.Fatalf("optional source-satisfied must require a live current-state anchor, got %#v", records)
	}
}

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

func TestPlanExecutePromptRunnerIsolatesInteractiveHistoryAndPinnedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "parent-only.txt"), []byte("PARENT_PINNED_SENTINEL_8f32f6\n"), 0o644); err != nil {
		t.Fatalf("write parent-only: %v", err)
	}

	var chatRequests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)

			chatRequests.Add(1)

			for _, forbidden := range []string{
				"PARENT_HISTORY_SENTINEL_24d4f8",
				"PARENT_ASSISTANT_SENTINEL_977aae",
				"PARENT_PINNED_SENTINEL_8f32f6",
				pinnedFileSnapshotHeaderTag,
			} {
				if strings.Contains(requestText, forbidden) {
					t.Fatalf("isolated plan-execute planner request leaked parent TUI state %q:\n%s", forbidden, requestText)
				}
			}

			if !strings.Contains(requestText, "PLANNER_PROMPT_SENTINEL_d66863") {
				t.Fatalf("planner request missing isolated prompt sentinel:\n%s", requestText)
			}

			reply := "<tool_call>\n" +
				`{"tool":"submit_plan","rationale":"Update target.txt from the isolated planner prompt.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace the target fixture text"}]}`

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

	model.messages = []llama.ChatMessage{
		{Role: "user", Content: "PARENT_HISTORY_SENTINEL_24d4f8"},
		{Role: "assistant", Content: "PARENT_ASSISTANT_SENTINEL_977aae"},
	}
	model.lastAssistantReasoningContent = "parent reasoning"

	parentPins := model.pinnedFilesRegistry()
	parentPins.Pin("parent-only.txt")

	runner := model.newPlanExecutePromptRunner(context.Background())

	answer, err := runner(cfg, "PLANNER_PROMPT_SENTINEL_d66863 update target.txt", planexecute.RunOptions{
		TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
		PlannerOwnedVerification: true,
		MaxTokensOverride:        1024,
	}, io.Discard)
	if err != nil {
		t.Fatalf("prompt runner: %v\nanswer: %s", err, answer)
	}

	if chatRequests.Load() == 0 {
		t.Fatalf("expected planner chat request")
	}

	if len(model.messages) != 2 || model.messages[0].Content != "PARENT_HISTORY_SENTINEL_24d4f8" || model.messages[1].Content != "PARENT_ASSISTANT_SENTINEL_977aae" {
		t.Fatalf("parent messages were not restored: %#v", model.messages)
	}

	if model.lastAssistantReasoningContent != "parent reasoning" {
		t.Fatalf("parent reasoning was not restored: %q", model.lastAssistantReasoningContent)
	}

	if model.pinnedFiles != parentPins {
		t.Fatalf("parent pinned-file registry was not restored")
	}
}

func TestPlanExecutePlannerThinkingPolicyDisablesLocalThinkingOnly(t *testing.T) {
	thinkingOn := true
	qwen := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		modelID: "qwen3.6-35b",
	}

	if !qwen.shouldDisableThinkingForPlanExecutePlanner() {
		t.Fatalf("local Qwen planner should disable thinking for strict plan-execute tool emission")
	}

	hosted := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		modelID:            "glm-5.1",
		modelThinkingFloor: true,
	}
	if hosted.shouldDisableThinkingForPlanExecutePlanner() {
		t.Fatalf("thinking-floor hosted planner should preserve thinking")
	}
}

func TestPlanExecutePromptRunnerRoutePreservesHostedExecutorThinkingFloor(t *testing.T) {
	executorSpec := config.LlamaServer{Kind: config.BackendZAI, Model: "glm-5.1", BaseURL: "https://api.z.ai/api/paas/v4"}
	m := &Model{
		client:            llama.NewClient("http://planner.invalid", time.Second),
		implementerClient: &reachableListModelsClient{models: []llama.ModelInfo{{ID: "glm-5.1"}}},
		plannerSpec:       config.LlamaServer{Model: "local-planner", BaseURL: "http://planner.invalid"},
		executorSpec:      executorSpec,
		modelID:           "local-planner",
	}
	restore := m.applyPlanExecutePromptRunnerRoute(context.Background(), planexecute.RunOptions{})

	if !m.modelThinkingFloor {
		t.Fatalf("hosted executor route should set modelThinkingFloor during in-process plan-execute")
	}

	restore()

	if m.modelThinkingFloor {
		t.Fatalf("modelThinkingFloor should restore after in-process plan-execute route")
	}
}

func TestPlanExecutePromptRunnerStatePreservesThinkingForHostedExecutorRoute(t *testing.T) {
	executorSpec := config.LlamaServer{Kind: config.BackendZAI, Model: "glm-5.1", BaseURL: "https://api.z.ai/api/paas/v4"}
	m := &Model{
		client:            llama.NewClient("http://planner.invalid", time.Second),
		implementerClient: &reachableListModelsClient{models: []llama.ModelInfo{{ID: "glm-5.1"}}},
		plannerSpec:       config.LlamaServer{Model: "local-planner", BaseURL: "http://planner.invalid"},
		executorSpec:      executorSpec,
		modelID:           "local-planner",
	}
	restoreRoute := m.applyPlanExecutePromptRunnerRoute(context.Background(), planexecute.RunOptions{})
	restoreState := m.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
	}, io.Discard)

	if m.disableThinkingOverride {
		t.Fatalf("hosted executor route must not inherit local planner no-thinking override")
	}

	restoreState()
	restoreRoute()

	if m.disableThinkingOverride || m.modelThinkingFloor {
		t.Fatalf("plan-execute state/route should restore, disableThinking=%t floor=%t", m.disableThinkingOverride, m.modelThinkingFloor)
	}
}

func TestPlanExecutePromptRunnerStateDisablesLocalThinkingForActiveModelFallback(t *testing.T) {
	thinkingOn := true
	m := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		modelID: "qwen3.6-35b",
	}
	restoreState := m.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily:     agent.PlanThenExecutePlannerTaskFamily,
		UseActiveModel: true,
	}, io.Discard)

	if !m.disableThinkingOverride {
		t.Fatalf("active-model fallback should still disable local thinking for strict plan-execute tool emission")
	}

	restoreState()

	if m.disableThinkingOverride {
		t.Fatalf("plan-execute state should restore disableThinking=false")
	}
}

func TestPlanExecutePromptRunnerUsesExecutorRouteWhenAvailable(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends: %v", err)
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

	answer, err := model.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
	}, io.Discard)
	if err != nil {
		t.Fatalf("prompt runner: %v", err)
	}

	if strings.TrimSpace(answer) != "executor route" {
		t.Fatalf("answer = %q, want executor route", answer)
	}

	if executorChat.Load() == 0 {
		t.Fatalf("expected executor chat request")
	}

	if plannerChat.Load() != 0 {
		t.Fatalf("plan-execute prompt runner should not use planner chat when executor is reachable, got %d", plannerChat.Load())
	}

	if model.modelID != "planner-model" {
		t.Fatalf("modelID was not restored after prompt runner: %q", model.modelID)
	}
}

func TestPlanExecutePromptRunnerUseActiveModelSkipsExecutorRoute(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends: %v", err)
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

	answer, err := model.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
		UseActiveModel:    true,
	}, io.Discard)
	if err != nil {
		t.Fatalf("prompt runner: %v", err)
	}

	if strings.TrimSpace(answer) != "planner route" {
		t.Fatalf("answer = %q, want planner route", answer)
	}

	if plannerChat.Load() == 0 {
		t.Fatalf("expected planner chat request")
	}

	if executorChat.Load() != 0 {
		t.Fatalf("active-model fallback should not use executor chat, got %d", executorChat.Load())
	}

	if model.modelID != "planner-model" {
		t.Fatalf("modelID was not restored after prompt runner: %q", model.modelID)
	}
}

func TestImplementationDispatchPlanExecuteHostedModelOverride(t *testing.T) {
	model := &Model{
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
		modelID: "deepseek-v4-flash",
	}

	if got := model.implementationDispatchPlanExecuteModelOverride(); got != "deepseek-v4-flash" {
		t.Fatalf("hosted model override = %q, want deepseek-v4-flash", got)
	}

	model.plannerSpec = config.LlamaServer{Kind: "llama.cpp", Model: "qwen3.6-35b"}
	model.modelID = "qwen3.6-35b"
	if got := model.implementationDispatchPlanExecuteModelOverride(); got != "" {
		t.Fatalf("local model should preserve plan-execute role routing, got override %q", got)
	}
}

func TestUseConfigPlannerModelBypassesPlannerRoleAlias(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
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
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-v4-flash"}}})
		case "/v1/chat/completions":
			configPlannerChat.Add(1)

			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat body: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends: %v", err)
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

	answer, err := model.newPlanExecutePromptRunner(context.Background())(cfg, "answer directly", planexecute.RunOptions{
		TaskFamily:        "direct_question",
		MaxTokensOverride: 128,
		UseActiveModel:    true,
	}, io.Discard)
	if err != nil {
		t.Fatalf("prompt runner: %v", err)
	}

	if strings.TrimSpace(answer) != "config route" {
		t.Fatalf("answer = %q, want config route", answer)
	}

	if configPlannerChat.Load() == 0 {
		t.Fatalf("expected config planner chat request")
	}

	if aliasPlannerChat.Load() != 0 {
		t.Fatalf("explicit planner model should bypass role alias, got %d alias chats", aliasPlannerChat.Load())
	}

	if got, _ := configPlannerModel.Load().(string); got != "deepseek-v4-flash" {
		t.Fatalf("config planner request model = %q, want deepseek-v4-flash", got)
	}
}

func TestRunPromptOncePlanExecuteCreatesTypedAuthorizedNewFileAfterSubmitReadyRecovery(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var (
		sawNewFileNudge        atomic.Bool
		initialPlannerRequests atomic.Int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- internal/newpkg/decision.go exists with the requested helper.\n- No out-of-scope paths were changed.\n- Planner-owned build or test commands remain pending outside this mocked lane."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "PLANNER READINESS REACHED") ||
				strings.Contains(requestText, "Current Submit Plan Skeleton"):

				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Create the new package file from the referenced plan document.","edits":[{"path":"internal/newpkg/decision.go","replace_all":true,"old_text":"","new_text":"package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n","purpose":"create the decision helper"}]}`
			case strings.Contains(requestText, "Required new-file directory does not exist yet"):
				sawNewFileNudge.Store(true)

				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Create the new package file from the referenced plan document.","edits":[{"path":"internal/newpkg/decision.go","replace_all":true,"old_text":"","new_text":"package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n","purpose":"create the decision helper"}]}`
			default:
				initialPlannerRequests.Add(1)

				if !strings.Contains(requestText, "Required path coverage for this run") ||
					!strings.Contains(requestText, "- internal/newpkg/decision.go") {

					t.Fatalf("planner request missing required new-file scope:\n%s", requestText)
				}

				reply = "<tool_call>\n" + `{"tool":"go_file_outline","path":"internal/newpkg"}`
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "internal", "newpkg", "decision.go"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}

	if string(got) != "package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n" {
		t.Fatalf("generated file content = %q", string(got))
	}

	if sawNewFileNudge.Load() {
		t.Fatalf("typed-authorized new file should be submit-ready without probing the missing package directory")
	}

	if initialPlannerRequests.Load() != 1 {
		t.Fatalf("expected one initial package probe before submit-ready recovery, got %d probe request(s)", initialPlannerRequests.Load())
	}
}

func TestPlanExecuteMissingNewFileFailureSurfacesAuthorizationMessage(t *testing.T) {
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
	if err != nil {
		t.Fatalf("read plan artifact: %v", err)
	}
	var artifact planartifact.PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("decode plan artifact: %v", err)
	}
	artifact.Approval.Required = false
	artifact.Approval.Status = planartifact.ApprovalStatusImplicit
	artifact.Approval.ApprovedPaths = nil
	artifact.Approval.ExecutionPermission = planartifact.ExecutionPermissionExecuteAll
	updated, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode plan artifact: %v", err)
	}
	if err := os.WriteFile(planPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write plan artifact: %v", err)
	}

	model := &Model{cfg: cfg}
	answer, ok := model.humanPlanExecuteMissingNewFileAuthorizationMessage(planexecute.RunResult{
		SessionID:   "scope-intake-test-session",
		FailureKind: string(plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth),
	}, nil)
	if !ok {
		t.Fatal("expected missing new-file failure to surface authorization message")
	}

	for _, want := range []string{
		"Plan-execute needs approval before creating new files.",
		"src/pages/products.astro",
		"authorized_new_files",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("authorization response missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "src/pages/index.astro") {
		t.Fatalf("existing changed path should not require new-file authorization:\n%s", answer)
	}
}

func TestRunPromptOncePlanExecutePlannerBlockedCanSurfaceThroughRecoveryDecision(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var sawRecoveryDecision atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce should surface planner blocker as an assistant answer, got err=%v answer=%q", err, answer)
	}

	if !sawRecoveryDecision.Load() {
		t.Fatalf("expected planner_blocked failure to be consumed by recovery decision model")
	}

	if !strings.Contains(answer, "Planner failed before submitting a plan") {
		t.Fatalf("expected human-facing recovery message, got %q", answer)
	}

	if strings.Contains(answer, "Chat failed") {
		t.Fatalf("planner blocker should not be surfaced as transport failure, got %q", answer)
	}
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

func TestRunPromptOncePlanExecutePlannerBlockedCanRetryAfterRecoveryDecision(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var (
		sawRecoveryDecision atomic.Bool
		sawRetryPrompt      atomic.Bool
		firstPlannerReplies atomic.Int64
	)

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

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
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Update target.txt after recovery retry.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace target text"}]}`
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "docs/plan.md")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	if !sawRecoveryDecision.Load() {
		t.Fatalf("expected recovery decision prompt after planner_blocked")
	}

	if !sawRetryPrompt.Load() {
		t.Fatalf("expected second plan-execute attempt with recovery addendum")
	}

	if firstPlannerReplies.Load() != 1 {
		t.Fatalf("expected visible prose planner reply to block without hidden deterministic retry before recovery decision; got %d planner replies", firstPlannerReplies.Load())
	}

	got, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	if string(got) != "new\n" {
		t.Fatalf("target content = %q, want new line", string(got))
	}

	if !strings.Contains(answer, "plan-then-execute summary") {
		t.Fatalf("expected successful retry summary, got %q", answer)
	}
}

func TestRunPromptOncePlanExecuteDoesNotInferMissingRequestedTestRetry(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nconst RegressionCovered = false\n"), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var (
		sawMissingTestRetry atomic.Bool
		plannerRequests     atomic.Int64
	)

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Allowed Repair Paths"):
				reply = "Decision: pass\n\nVerification Checklist:\n- pkg/feature.go contains the hardening.\n- pkg/feature_test.go contains regression coverage.\n- No out-of-scope paths were changed."
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "Missing Requested Test Edit Recovery"):
				sawMissingTestRetry.Store(true)

				if !strings.Contains(requestText, "pkg/feature_test.go") {
					t.Fatalf("missing-test retry prompt did not expose test path:\n%s", requestText)
				}

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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "Implement docs/plan.md: harden the feature behavior and add regression tests proving the edge case is rejected.")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v\nanswer: %s", err, answer)
	}

	if sawMissingTestRetry.Load() {
		t.Fatalf("prompt prose must not trigger automatic missing-test retry; answer=%s", answer)
	}

	if plannerRequests.Load() == 0 {
		t.Fatalf("expected initial production-only planner request")
	}

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	if !strings.Contains(string(gotSource), "return true") {
		t.Fatalf("source was not hardened:\n%s", string(gotSource))
	}

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	if err != nil {
		t.Fatalf("read test: %v", err)
	}

	if strings.Contains(string(gotTest), "RegressionCovered = true") {
		t.Fatalf("test should not be updated by a prompt-inferred retry:\n%s", string(gotTest))
	}

	if !strings.Contains(answer, "plan-then-execute summary") {
		t.Fatalf("expected successful retry summary, got %q", answer)
	}
}

func TestRunPromptOncePlanExecuteDoesNotInferMissingRequestedTestSourceSatisfiedRetry(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	allowImplementationDispatchPlanDocumentPreflight(t)

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	sourceBody := "package pkg\n\nfunc Hardened() bool { return true }\n"
	testBody := "package pkg\n\nconst RegressionCovered = true\n"

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte(sourceBody), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte(testBody), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var (
		sawMissingTestRetry atomic.Bool
		retryTurns          atomic.Int64
	)

	sourceReadReplies := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			requestText := string(body)
			reply := ""

			switch {
			case strings.Contains(requestText, "Plan submitted with"):
				reply = "Plan submitted."
			case strings.Contains(requestText, "Missing Requested Test Edit Recovery"):
				sawMissingTestRetry.Store(true)

				for _, want := range []string{
					"Decision: source-satisfied",
					"pkg/feature.go",
					"pkg/feature_test.go",
				} {
					if !strings.Contains(requestText, want) {
						t.Fatalf("missing-test retry prompt missing %q:\n%s", want, requestText)
					}
				}

				switch retryTurns.Add(1) {
				case 1:
					reply = "<tool_call>\n" + `{"tool":"read_file","path":"pkg/feature.go","start_line":1,"end_line":4}`
				case 2:
					reply = "<tool_call>\n" + `{"tool":"read_file","path":"pkg/feature_test.go","start_line":1,"end_line":4}`
				default:
					reply = strings.Join([]string{
						"Decision: source-satisfied",
						"- pkg/feature.go: requested final state is already present.",
						"- pkg/feature_test.go: current tests already contain the requested regression evidence.",
					}, "\n")
				}
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "Implement docs/plan.md: harden the feature behavior and add regression tests proving the edge case is rejected.")
	if err == nil {
		t.Fatalf("expected stale production-only plan to fail without prompt-inferred missing-test retry; answer=%s", answer)
	}

	if sawMissingTestRetry.Load() {
		t.Fatalf("prompt prose must not trigger missing-test retry; answer=%s", answer)
	}

	gotSource, err := os.ReadFile(filepath.Join(root, "pkg", "feature.go"))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	if !strings.Contains(string(gotSource), "return false") {
		t.Fatalf("stale production-only plan should be the only applied change without inferred retry:\n%s", string(gotSource))
	}

	gotTest, err := os.ReadFile(filepath.Join(root, "pkg", "feature_test.go"))
	if err != nil {
		t.Fatalf("read test: %v", err)
	}

	if string(gotTest) != testBody {
		t.Fatalf("test changed during source-satisfied retry:\n%s", string(gotTest))
	}
}

func loadOnlyTimestampedPlanArtifactForTest(t *testing.T, root string) planartifact.PlanArtifact {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "*.plan.json"))
	if err != nil {
		t.Fatalf("glob plan artifacts: %v", err)
	}

	var stamped []string

	for _, path := range paths {
		if filepath.Base(path) == "last.plan.json" {
			continue
		}

		stamped = append(stamped, path)
	}

	if len(stamped) != 1 {
		t.Fatalf("expected one timestamped plan artifact under .idiom/plans, got %d: %v", len(stamped), stamped)
	}

	artifact, err := workspace.LoadPlanArtifact(stamped[0])
	if err != nil {
		t.Fatalf("load plan artifact %s: %v", stamped[0], err)
	}

	return artifact
}

func TestImplementationDispatchInlineOnlyEditMarkdownTargetIsNotPlanDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "widget_config.json"), []byte(`{"labels":["alpha"]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "widget_notes.md"), []byte("# Notes\n\nStatus: draft.\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
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
	if len(required) != 0 {
		t.Fatalf("inline only-edit scope should not become required coverage, got %#v", required)
	}

	for _, want := range []string{"widget_config.json", "widget_notes.md"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("allowed edit paths missing %q: %#v", want, allowed)
		}
	}

	if len(docs) != 0 {
		t.Fatalf("markdown edit target must not be treated as a referenced plan document: %#v", docs)
	}

	if len(blockers) != 0 {
		t.Fatalf("direct markdown edit target must not produce plan-document blockers: %#v", blockers)
	}
}

func TestRunPromptOnceReferenceOnlyDesignDocDispatchesPlanExecuteWithoutRefine(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "internal", "workflows", "productworkflow"), 0o755); err != nil {
		t.Fatalf("mkdir productworkflow: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go"), []byte("package productworkflow\n\nconst TempPolicy = \"weak\"\n"), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go"), []byte("package productworkflow\n\nconst TempPolicyTested = false\n"), 0o644); err != nil {
		t.Fatalf("write migration test file: %v", err)
	}

	designDoc := strings.Join([]string{
		"# Product Workflow Design",
		"",
		"Open delivery blockers from review: status migration apply must harden predictable temp-path writes against pre-existing symlinks.",
		"",
		"Phase 6 and Phase 7 also contain unrelated continuation and sidecar work.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte(designDoc), 0o644); err != nil {
		t.Fatalf("write design doc: %v", err)
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
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mock-model"}},
			})
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

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
					if !strings.Contains(requestText, want) {
						t.Fatalf("planner prompt lost reference-only design-doc context %q:\n%s", want, requestText)
					}
				}

				if readReply, ok := planExecuteSourceReadFixtureReply(requestText, sourceReadReplies, "internal/workflows/productworkflow/migration_apply.go", "internal/workflows/productworkflow/migration_apply_test.go"); ok {
					reply = readReply
				} else {
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Harden the migration temp policy requested by the referenced design document and add regression coverage.","edits":[{"path":"internal/workflows/productworkflow/migration_apply.go","old_text":"const TempPolicy = \"weak\"\n","new_text":"const TempPolicy = \"hardened\"\n","purpose":"harden migration temp path policy"},{"path":"internal/workflows/productworkflow/migration_apply_test.go","old_text":"const TempPolicyTested = false\n","new_text":"const TempPolicyTested = true\n","purpose":"cover migration temp path policy"}]}`
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

	answer, err := model.runPlanExecuteCommandOnce(context.Background(), "Implement one bounded topic from docs/design.md: status migration apply must harden predictable temp-path writes against pre-existing symlinks. Add regression tests proving temp symlinks are rejected.")
	if err != nil {
		t.Fatalf("runPlanExecuteCommandOnce: %v\nanswer: %s", err, answer)
	}

	got, err := os.ReadFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply.go"))
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}

	if string(got) != "package productworkflow\n\nconst TempPolicy = \"hardened\"\n" {
		t.Fatalf("migration file content = %q, want hardened policy", string(got))
	}

	gotTest, err := os.ReadFile(filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go"))
	if err != nil {
		t.Fatalf("read migration test file: %v", err)
	}

	if string(gotTest) != "package productworkflow\n\nconst TempPolicyTested = true\n" {
		t.Fatalf("migration test file content = %q, want regression coverage marker", string(gotTest))
	}

	if preflightCalls.Load() != 0 {
		t.Fatalf("reference-only design doc should skip plan-document preflight, got %d call(s)", preflightCalls.Load())
	}

	if plannerRequests.Load() == 0 {
		t.Fatalf("expected plan-execute planner request")
	}
}

func composeImplementationBriefForTest(prompt string, plan promptExecutionPlan, cfg config.Config) string {
	requiredPaths, allowedPaths, planDocuments, _ := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)
	return composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, cfg, requiredPaths, allowedPaths, planDocuments)
}

func TestComposeImplementationBriefTreatsDesignDocScopeAsCandidatePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, "docs", "handoff.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("existing\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{"docs/handoff.md"}),
	}
	brief := composeImplementationBriefForTest("start working on docs/handoff.md", plan, cfg)

	if strings.Contains(brief, "## Files to Modify\n\n- target.txt") {
		t.Fatalf("design-doc scope must not become active required edit scope:\n%s", brief)
	}

	if !strings.Contains(brief, "## Candidate Edit Scope From Target References\n\n- target.txt") {
		t.Fatalf("brief missing candidate edit scope:\n%s", brief)
	}

	for _, want := range []string{
		"## Referenced Plan Documents",
		"### docs/handoff.md",
		"> ## Files to Modify",
		"> | target.txt | Do the important thing |",
		"> 1. Preserve this body text for the planner.",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}

	if got := plancontract.RequiredEditPathsFromPrompt(brief); slices.Contains(got, "target.txt") {
		t.Fatalf("design-doc scope must not become required edit paths, got %#v\n%s", got, brief)
	}

	if got := plancontract.AllowedEditPathsFromPrompt(brief); !slices.Contains(got, "target.txt") {
		t.Fatalf("design-doc scope must become candidate edit paths, got %#v\n%s", got, brief)
	}
}

func TestComposeImplementationBriefExcerptsLongReferenceDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	var doc strings.Builder

	doc.WriteString("# Long Design\n\n")
	doc.WriteString("Status migration apply must harden predictable temp-path writes against pre-existing workflow/index/events temp symlinks.\n\n")

	for i := 0; i < 500; i++ {
		fmt.Fprintf(&doc, "## Unrelated Section %03d\n\nThis section discusses unrelated operator UX material and filler details that should not dominate the implementation brief.\n\n", i)
	}

	doc.WriteString("## Relevant Regression Coverage\n\nAdd regression tests proving pre-existing workflow/index/events temp symlinks are rejected without partial durable mutation.\n\n")

	if err := os.WriteFile(filepath.Join(root, "docs", "long-design.md"), []byte(doc.String()), 0o644); err != nil {
		t.Fatalf("write design doc: %v", err)
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

	if len(brief) > 18000 {
		t.Fatalf("long reference doc should be compacted, got %d bytes", len(brief))
	}

	lowerBrief := strings.ToLower(brief)
	for _, want := range []string{
		"[excerpted from long referenced document",
		"status migration apply must harden predictable temp-path writes",
		"add regression tests proving pre-existing workflow/index/events temp symlinks",
	} {
		if !strings.Contains(lowerBrief, strings.ToLower(want)) {
			t.Fatalf("compacted brief missing %q:\n%s", want, brief)
		}
	}

	if strings.Contains(brief, "Unrelated Section 001") || strings.Contains(brief, "Unrelated Section 499") {
		t.Fatalf("compacted brief retained unrelated tail:\n%s", brief)
	}
}

func TestComposeImplementationBriefResolvesUniquePlanDocumentBasenameAsCandidateScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
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

	if err := os.WriteFile(filepath.Join(root, "docs", docName), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "internal", "surfaces", "tui", "app"), 0o755); err != nil {
		t.Fatalf("mkdir internal/surfaces/tui/app: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "surfaces", "tui", "app", "writer_route.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write writer route: %v", err)
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
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}

	if got := plancontract.RequiredEditPathsFromPrompt(brief); slices.Contains(got, "internal/surfaces/tui/app/writer_route.go") {
		t.Fatalf("resolved design-doc scope must not become required edit paths, got %#v\n%s", got, brief)
	}

	if got := plancontract.AllowedEditPathsFromPrompt(brief); !slices.Contains(got, "internal/surfaces/tui/app/writer_route.go") {
		t.Fatalf("resolved design-doc scope must become candidate edit paths, got %#v\n%s", got, brief)
	}
}

func TestComposeImplementationBriefKeepsAtomScopeAsRequiredPaths(t *testing.T) {
	root := t.TempDir()
	atomRel := filepath.Join(".idiom", "atoms", "handoff", "atom-001.md")

	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(atomRel)), 0o755); err != nil {
		t.Fatalf("mkdir atom dir: %v", err)
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
	if err := os.WriteFile(filepath.Join(root, atomRel), []byte(atomDoc), 0o644); err != nil {
		t.Fatalf("write atom: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{filepath.ToSlash(atomRel)}),
	}
	brief := composeImplementationBriefForTest("start working on "+filepath.ToSlash(atomRel), plan, cfg)

	if !strings.Contains(brief, "## Files to Modify\n\n- target.txt") {
		t.Fatalf("atom scope must remain active required edit scope:\n%s", brief)
	}

	if got := plancontract.RequiredEditPathsFromPrompt(brief); !slices.Contains(got, "target.txt") {
		t.Fatalf("atom scope must become required edit paths, got %#v\n%s", got, brief)
	}
}

func TestImplementationDispatchInlineRequiredPathsBecomePlanExecuteRequiredScope(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{runtimeBrief: runtimeBriefForTest(nil)}
	prompt := "Implement JSON usage-error parity. Required edit paths: internal/surfaces/cli/product/product_workflow.go, internal/surfaces/cli/product/product_workflow_test.go"
	required, allowed, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}

	for _, want := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		if !slices.Contains(required, want) {
			t.Fatalf("missing required path %q, got required=%#v allowed=%#v", want, required, allowed)
		}

		if slices.Contains(allowed, want) {
			t.Fatalf("inline required path %q must not be demoted to allowed scope: required=%#v allowed=%#v", want, required, allowed)
		}
	}
}

func TestImplementationDispatchRetryCandidateScopePreservesCompiledWriteEnvelope(t *testing.T) {
	opts := planexecute.Options{
		RequiredEditPaths:     planexecute.MultiFlag{"pkg/target.go"},
		ScopeContract:         plancontract.NewPlanScopeContract([]string{"pkg/target.go"}, nil, nil).WithWriteAllowedPaths([]string{"pkg/target.go"}),
		ScopeExtractionPolicy: planexecute.ScopeExtractionFrozen,
	}

	retry := implementationDispatchRetryOptionsWithCandidateScope(opts, []string{"pkg/discovered.go"})

	if got, want := retry.ScopeExtractionPolicy, planexecute.ScopeExtractionFrozen; got != want {
		t.Fatalf("scope extraction policy = %q, want %q", got, want)
	}

	if got, want := retry.ScopeContract.WriteAllowedPaths, []string{"pkg/target.go"}; !slices.Equal(got, want) {
		t.Fatalf("write envelope = %#v, want %#v", got, want)
	}

	if !retry.ScopeContract.WriteAllowedExplicit {
		t.Fatalf("write envelope should remain explicit: %#v", retry.ScopeContract)
	}

	if !slices.Contains(retry.ScopeContract.AllowedCandidatePaths, "pkg/discovered.go") {
		t.Fatalf("discovered path should remain candidate scope, contract=%#v", retry.ScopeContract)
	}
}

func TestImplementationDispatchInlineRequiredPathsIgnoreTrailingSlashProse(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	plan := promptExecutionPlan{runtimeBrief: runtimeBriefForTest(nil)}
	prompt := "Implement JSON usage-error parity. Required edit paths: `internal/surfaces/cli/product/product_workflow.go`, `internal/surfaces/cli/product/product_workflow_test.go`. Add focused tests for missing `--effort-root` and mutually exclusive/missing mode flags under JSON output."
	required, allowed, _, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}

	want := []string{"internal/surfaces/cli/product/product_workflow.go", "internal/surfaces/cli/product/product_workflow_test.go"}
	if len(required) != len(want) {
		t.Fatalf("required paths = %#v, want %#v", required, want)
	}

	for _, path := range want {
		if !slices.Contains(required, path) {
			t.Fatalf("missing required path %q, got required=%#v allowed=%#v", path, required, allowed)
		}
	}

	for _, forbidden := range []string{"exclusive/missing", "cmd/idiom"} {
		if slices.Contains(required, forbidden) || slices.Contains(allowed, forbidden) {
			t.Fatalf("slash prose or truncated directory leaked into scope: required=%#v allowed=%#v", required, allowed)
		}
	}
}

func runtimeBriefForTest(targetRefs []string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{TargetRefs: targetRefs}
}

func TestCorrectTurnRoutePlanDoesNotUpgradeVerificationOnlyWithEditIntentHeuristic(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "verification_only"}, "Modify exactly one file and do not run tests.", modepolicy.BuildMode())
	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteDoesNotUseEditAtomAsImplementationHeuristic(t *testing.T) {
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

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanDoesNotRewriteAnalysisContinuationRoute(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Continue your investigation until you have a confident response.", modepolicy.BuildMode())
	if plan.Family != "explore_then_implement" {
		t.Fatalf("family = %q, want explore_then_implement", plan.Family)
	}

	if plan.InitialLane != "explore" || plan.FollowupLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestGDScriptRouteFallsBackForNonGDScriptExplicitScope(t *testing.T) {
	prompt := "Implement the change.\n\nrequired_edit_paths:\n- internal/labels/labels.go\n"
	route := validateRouteFamilyAgainstExplicitEditScope(turnRoutePlan{Family: "gdscript_edit", Reason: "classifier"}, prompt, modepolicy.BuildMode())

	if route.Family != "implementation" {
		t.Fatalf("family = %q, want implementation for non-GDScript explicit scope", route.Family)
	}

	if route.Reason != "gdscript_scope_mismatch" {
		t.Fatalf("reason = %q, want gdscript_scope_mismatch", route.Reason)
	}
}

func TestGDScriptRouteKeepsAllGDScriptExplicitScope(t *testing.T) {
	prompt := "Implement the change.\n\nrequired_edit_paths:\n- scenes/player.gd\nallowed_edit_paths:\n- tests/player_test.gd\n"
	route := validateRouteFamilyAgainstExplicitEditScope(turnRoutePlan{Family: "gdscript_edit", Reason: "classifier"}, prompt, modepolicy.BuildMode())

	if route.Family != "gdscript_edit" {
		t.Fatalf("family = %q, want gdscript_edit for GDScript-only explicit scope", route.Family)
	}
}

func TestShouldPreferFallbackRouteDoesNotOverrideConversationWithHeuristicFallback(t *testing.T) {
	fallback := fallbackTurnRoute("Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?", modepolicy.BuildMode())
	if fallback.Family != "conversation" {
		t.Fatalf("fallback family = %q, want conversation", fallback.Family)
	}

	planned := normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, modepolicy.BuildMode())
	if planned.Family != "conversation" {
		t.Fatalf("legacy direct_question should normalize to conversation, got %#v", planned)
	}

	if shouldPreferFallbackRoute(fallback, planned, "Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?") {
		t.Fatal("conservative fallback must not override a generic conversation model route")
	}
}

func TestClassifyTurnRouteLetsModelUpgradeConversationFallback(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Can you verify that this still builds?", modepolicy.BuildMode())

	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestClassifyTurnRouteUsesClassifierBeforeImplementationFallback(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Update the README docs for the new workflow.", modepolicy.BuildMode())

	if plan.Family != "docs_edit" {
		t.Fatalf("family = %q, want docs_edit", plan.Family)
	}

	if calls.Load() != 1 {
		t.Fatalf("implementation-like prose should go through classifier first, got %d calls", calls.Load())
	}
}

func TestRouteClassifierPromptOmitsFencedCodeBodies(t *testing.T) {
	prompt := "Implement the change.\n```go\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n```\nUse the exact source."
	got := routeClassifierPromptForModel(prompt)

	if strings.Contains(got, "ShortLabel") || strings.Contains(got, "Save changes") {
		t.Fatalf("classifier prompt leaked code body:\n%s", got)
	}

	if !strings.Contains(got, "[code block omitted for route classification]") {
		t.Fatalf("classifier prompt missing omission marker:\n%s", got)
	}

	if !strings.Contains(got, "Implement the change.") || !strings.Contains(got, "Use the exact source.") {
		t.Fatalf("classifier prompt lost surrounding prose:\n%s", got)
	}
}

func TestClassifyTurnRouteRepairsInvalidJSONOnce(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Implement the label change.", modepolicy.BuildMode())

	if plan.Family != "implementation" || plan.InitialLane != "implement" {
		t.Fatalf("repaired classifier route = %#v, want implementation", plan)
	}

	if calls.Load() != 2 {
		t.Fatalf("classifier calls = %d, want initial + repair", calls.Load())
	}
}

func TestClassifyTurnRouteRepairsInvalidJSONFromFallbackClient(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Implement the label change.", modepolicy.BuildMode())

	if plan.Family != "implementation" || plan.InitialLane != "implement" {
		t.Fatalf("repaired classifier route = %#v, want implementation", plan)
	}

	if fallbackCalls.Load() != 2 {
		t.Fatalf("fallback client calls = %d, want invalid response + repair", fallbackCalls.Load())
	}
}

func TestClassifyTurnRouteDoesNotSkipClassifierForDebugLogSignal(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Check the most recent logs and explain why it failed.", modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if calls.Load() != 1 {
		t.Fatalf("debug log wording should be classified by model, got %d calls", calls.Load())
	}
}

func TestClassifyTurnRouteUsesClassifierForPlanExecuteSourceSatisfiedSmokeWriteShaped(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}

	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}

	if calls.Load() != 1 {
		t.Fatalf("source-satisfied plan-execute workflow should go through classifier, got classifier calls=%d", calls.Load())
	}
}

func TestClassifyTurnRouteKeepsPlanExecuteHowToQuestionReadOnly(t *testing.T) {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := m.classifyTurnRoute(context.Background(), "How do I run plan-execute without changing files?", modepolicy.BuildMode())

	if workerruntime.TaskFamilyMutationCapable(plan.Family) {
		t.Fatalf("plan-execute how-to question should stay read-only, got %#v", plan)
	}
}

func TestClassifyTurnRouteKeepsQuestionShapedMutationReadOnly(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	if plan.Family != "conversation" {
		t.Fatalf("family = %q, want conversation", plan.Family)
	}

	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}

	if calls.Load() == 0 {
		t.Fatal("expected classifier to be consulted before honoring the read-only route")
	}
}

func TestRouteClassifierPromptDoesNotTreatQuestionShapeAsLaneSignal(t *testing.T) {
	var requestText string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read classifier request: %v", err)
		}

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
	plan := m.classifyTurnRoute(context.Background(), "Can you implement this feature: docs/design-prd-adr-brief-research-workflow.md Phase 7?", modepolicy.BuildMode())

	if plan.Family != "implementation" {
		t.Fatalf("classifier-selected polite implementation directive should route implementation, got %#v", plan)
	}

	if !strings.Contains(requestText, `"top_p":1`) {
		t.Fatalf("classifier request must use valid deterministic top_p, got:\n%s", requestText)
	}

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
		if !strings.Contains(requestText, want) {
			t.Fatalf("classifier prompt missing %q:\n%s", want, requestText)
		}
	}
}

func TestTurnRoutePlanCarriesEvidenceIntent(t *testing.T) {
	plan := normalizeTurnRoutePlan(turnRoutePlan{
		Family:           "conversation",
		RequestedOutcome: "answer",
		WriteIntent:      "no",
		EvidenceIntent:   "calls",
	}, modepolicy.BuildMode())

	if plan.EvidenceIntent != "relation_trace" {
		t.Fatalf("evidence intent = %q, want relation_trace", plan.EvidenceIntent)
	}

	if rendered := renderTurnRoutePlan(plan); !strings.Contains(rendered, "evidence_intent=relation_trace") {
		t.Fatalf("rendered route missing evidence intent: %q", rendered)
	}

	brief := turnRoutePlanRuntimeBrief(plan, modepolicy.BuildMode(), []string{"context_pack", "code_navigate", "read_file"})
	if brief.EvidenceIntent != "relation_trace" || brief.RequestedOutcome != "answer" || brief.WriteIntent != "no" {
		t.Fatalf("runtime brief did not preserve typed route intent: %#v", brief)
	}
}

func TestClassifyTurnRouteQuestionShapedMutationFallbackIsReadOnly(t *testing.T) {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := m.classifyTurnRoute(context.Background(), "Can we shorten the displayed model label to just the alias?", modepolicy.BuildMode())

	if workerruntime.TaskFamilyMutationCapable(plan.Family) {
		t.Fatalf("question-shaped mutation should stay read-only, got %#v", plan)
	}
}

func TestClassifyTurnRouteLaneNameQuestionStaysReadOnly(t *testing.T) {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	for _, prompt := range []string{
		"Can you explain to me what the difference is between the implement and plan-execute lanes?",
		"Can you explain whether we can shorten the displayed model label to just the alias?",
		"Can we shorten the displayed model label to just the alias without changing files?",
	} {
		plan := m.classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())
		if workerruntime.TaskFamilyMutationCapable(plan.Family) {
			t.Fatalf("%q should stay read-only, got %#v", prompt, plan)
		}
	}
}

func TestClassifyTurnRouteHonorsClassifierImplementationDirective(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Shorten the displayed model label to just the alias.", modepolicy.BuildMode())

	if plan.Family != "implementation" || plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("classifier-selected directive should route implementation, got %#v", plan)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected classifier call, got %d", calls.Load())
	}
}

func TestClassifyTurnRouteImplementSlashMentionDoesNotBypassClassifier(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	if workerruntime.TaskFamilyMutationCapable(plan.Family) {
		t.Fatalf("slash mention inside prose should not deterministically route implementation, got %#v", plan)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected classifier call, got %d", calls.Load())
	}
}

func TestFallbackTurnRouteLaneExerciseDoesNotBecomeVerificationOnly(t *testing.T) {
	m := &Model{cfg: config.Config{RouteClassifier: config.RouteClassifier{Enabled: false}}}
	plan := m.classifyTurnRoute(context.Background(), "I want to test the implementation lane.", modepolicy.BuildMode())

	if plan.Family == "verification_only" {
		t.Fatalf("lane exercise wording should not auto-run tests, got %#v", plan)
	}
}

func TestClassifyTurnRouteUsesClassifierForPlanExecuteRequiredPathEditWriteShaped(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), prompt, modepolicy.BuildMode())

	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}

	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}

	if calls.Load() != 1 {
		t.Fatalf("required-path plan-execute edit workflow should go through classifier, got classifier calls=%d", calls.Load())
	}
}

func TestTurnRoutePlanRuntimeBriefForConversationUsesReadOnlyWorkspace(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "edit_file", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "conversation"}, modepolicy.BuildMode(), available)

	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}

	if len(brief.AllowedTools) == 0 {
		t.Fatalf("conversation should advertise read-only tools, got %#v", brief.AllowedTools)
	}

	if len(brief.ReadinessGate) != 0 ||
		!slices.Contains(brief.FinalCritic, "answers_actual_user_request") ||
		!slices.Contains(brief.FinalCritic, "well_formatted_informative_on_topic") {

		t.Fatalf("expected conversation to use no readiness gate and answer-quality critics, got %#v", brief)
	}
}

func TestTurnRoutePlanRuntimeBriefForVerificationOnly(t *testing.T) {
	available := []string{"task", "pwd", "read_file", "search_files", "search_content", "shell", "edit_file", "write_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "verification_only"}, modepolicy.BuildMode(), available)

	if brief.PermissionProfile != "read_only_verification_shell" {
		t.Fatalf("permission = %q, want read_only_verification_shell", brief.PermissionProfile)
	}

	if !strings.Contains(strings.Join(brief.AllowedTools, ","), "shell") {
		t.Fatalf("expected shell in allowed tools, got %#v", brief.AllowedTools)
	}

	for _, tool := range brief.AllowedTools {
		if tool == "edit_file" || tool == "write_file" || tool == "pwd" {
			t.Fatalf("unexpected write tool %q in allowed tools %#v", tool, brief.AllowedTools)
		}
	}

	if brief.VerificationContract != "not_required_for_read_only_lane" {
		t.Fatalf("verification contract = %q", brief.VerificationContract)
	}
}

func TestTurnRoutePlanRuntimeBriefForDebugLogsUsesStandardReadLane(t *testing.T) {
	available := []string{"pwd", "read_file", "search_files", "search_content", "edit_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "debug_logs"}, modepolicy.BuildMode(), available)

	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}

	if brief.TaskFamily != "conversation" || brief.Lane != "main" || brief.Contract != "direct_answer" {
		t.Fatalf("debug_logs route should collapse to standard read lane, got %#v", brief)
	}

	if !strings.Contains(strings.Join(brief.AllowedTools, ","), "search_files") {
		t.Fatalf("expected search_files in allowed tools, got %#v", brief.AllowedTools)
	}
}

func TestTurnRoutePlanRuntimeBriefForArchitectureUsesShapingOnly(t *testing.T) {
	available := []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "architecture"}, modepolicy.BuildMode(), available)

	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}

	if len(brief.ReadinessGate) == 0 || len(brief.FinalCritic) == 0 {
		t.Fatalf("expected architecture analysis to keep a closure gate and final critic, got %#v", brief)
	}
}
