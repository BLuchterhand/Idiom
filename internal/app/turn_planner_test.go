package app

import (
	"context"
	"encoding/json"
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

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/planexecute"
	"idiom/internal/workspace"
	"idiom/internal/writerroute"
)

func disableQueueDaemonForTurnPlannerTest(t *testing.T, root string) {
	t.Helper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
}

func stubImplementationDispatchPlanDocumentPreflight(t *testing.T, stub func(context.Context, config.Config, implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult) {
	t.Helper()
	orig := implementationDispatchPlanDocumentPreflightRunner
	implementationDispatchPlanDocumentPreflightRunner = stub
	t.Cleanup(func() { implementationDispatchPlanDocumentPreflightRunner = orig })
}

func allowImplementationDispatchPlanDocumentPreflight(t *testing.T) {
	t.Helper()
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		return implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
	})
}

func stubImplementationDispatchPlanDocumentRefine(t *testing.T, stub func(context.Context, config.Config, implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult) {
	t.Helper()
	orig := implementationDispatchPlanDocumentRefineRunner
	implementationDispatchPlanDocumentRefineRunner = stub
	t.Cleanup(func() { implementationDispatchPlanDocumentRefineRunner = orig })
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

	if shouldDispatchImplementationToPlanExecute(plan, agent.BuildMode(), cfg, prompt) {
		t.Fatalf("refine scratchpad meta-brief should stay on implement lane instead of recursive plan-execute dispatch")
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

	if _, ok := readImplementationDispatchPlanDocument(root, ".idiom/refine/example/iter-1/draft.md"); ok {
		t.Fatalf("refine iter draft should not be treated as a plan document for recursive TUI refine")
	}
	if _, ok := readImplementationDispatchPlanDocument(root, ".idiom/refine/example/accepted.md"); !ok {
		t.Fatalf("accepted refine output should remain readable as a plan document")
	}
}

func TestImplementationDispatchRefineTitleIsBounded(t *testing.T) {
	longPath := ".idiom/refine/" + strings.Repeat("nested-title-", 40) + "iter-1/draft.md"
	title := implementationDispatchRefineTitle(longPath)
	if !strings.HasPrefix(title, "tui-") {
		t.Fatalf("title %q missing tui prefix", title)
	}
	if len(title) > 84 {
		t.Fatalf("title length = %d, want <= 84: %s", len(title), title)
	}
}

func TestImplementationDispatchSanitizesNonzeroRefineAcceptedContent(t *testing.T) {
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
	sanitized, changed := sanitizeImplementationDispatchNonzeroRefineAcceptedContent(content)
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

func TestImplementationDispatchNonzeroRefineCandidateCarriesPreflightBlockers(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		return implementationDispatchPlanDocumentPreflightResult{
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

func TestImplementationDispatchAtomMarksMissingPathAsNewFile(t *testing.T) {
	root := t.TempDir()
	atom := implementationDispatchPlanDocumentAtomWithWorkspaceShape(root, implementationDispatchPlanDocumentAtom{
		Path:   "internal/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})
	if !strings.HasPrefix(atom.Change, "NEW FILE:") {
		t.Fatalf("missing path atom change = %q, want NEW FILE marker", atom.Change)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "writerroute", "decision.go"), []byte("package writerroute\n"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	atom = implementationDispatchPlanDocumentAtomWithWorkspaceShape(root, implementationDispatchPlanDocumentAtom{
		Path:   "internal/writerroute/decision.go",
		Change: "Domain type, normalization, stable ID, event fields",
	})
	if strings.HasPrefix(atom.Change, "NEW FILE:") {
		t.Fatalf("existing path atom change = %q, should not add NEW FILE marker", atom.Change)
	}
	if len(atom.Anchors) == 0 {
		t.Fatalf("existing path atom should carry host current-state anchors")
	}
	if !strings.Contains(atom.Anchors[0], "internal/writerroute/decision.go:1") ||
		!strings.Contains(atom.Anchors[0], "package writerroute") {
		t.Fatalf("unexpected existing path anchor: %#v", atom.Anchors)
	}
}

func TestImplementationDispatchAtomAnchorsPreserveCompleteSourceLines(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("internal", "writerroute", "decision.go"))
	srcPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	longLine := "func NewDecision(mode WriterMode, lane string, plannerModel string, executorModel string, reviewerModel string, classifierModel string, riskTier string, costTier string, scopeTier string, verificationOwner string, reviewPolicy string, requiredEditPaths []string, evidencePacketIDs []string, reasonCodes []string, escalationTriggers []string, userOverride bool, taskFamily string) Decision {"
	source := "package writerroute\n\n" + longLine + "\n\treturn Decision{}\n}\n"
	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	atom := implementationDispatchPlanDocumentAtomWithWorkspaceShape(root, implementationDispatchPlanDocumentAtom{
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

func TestWriterRouteDecisionForPromptReportsPlanExecuteDispatch(t *testing.T) {
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
		mode: agent.BuildMode(),
		route: turnRoutePlan{
			Family: "implementation",
		},
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"internal/app/writer_route.go"},
		},
	}

	decision := m.writerRouteDecisionForPrompt("modify internal/app/writer_route.go", plan)

	if decision.WriterMode != string(writerroute.ModePlanSubmission) || decision.Lane != "plan-execute" {
		t.Fatalf("unexpected writer route decision: %#v", decision)
	}
	if decision.PlannerModel != "planner-model" || decision.ExecutorModel != "executor-model" || decision.ClassifierModel != "classifier-model" {
		t.Fatalf("unexpected model fields: %#v", decision)
	}
	if !slices.Contains(decision.RequiredEditPaths, "internal/app/writer_route.go") {
		t.Fatalf("required paths missing from writer route decision: %#v", decision.RequiredEditPaths)
	}
}

func TestImplementationDispatchRefineDiagnosticsDoNotTruncateActionableLine(t *testing.T) {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
	diagnostics := implementationDispatchRefineDiagnostics(implementationDispatchPlanDocumentRefineResult{
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

func TestImplementationDispatchBriefLintBlockersDoNotTruncateActionableLine(t *testing.T) {
	longBlocker := "exact-anchor specificity failed for required edit path(s): " + strings.Repeat("internal/pkg/path.go ", 40) + "Each required edit path must carry at least one `<path>:<line>` reference."
	payload, err := json.Marshal(map[string]any{
		"exit": 1,
		"findings": []map[string]string{
			{
				"severity": "BLOCK",
				"category": "anchor-specificity",
				"message":  longBlocker,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal lint report: %v", err)
	}
	blockers := implementationDispatchBriefLintBlockers(implementationDispatchPreflightCommandResult{
		ExitCode: 1,
		Stdout:   string(payload),
	})
	joined := strings.Join(blockers, "\n")
	if !strings.Contains(joined, longBlocker) {
		t.Fatalf("lint blockers lost actionable blocker:\n%s", joined)
	}
	if strings.Contains(joined, "[truncated") {
		t.Fatalf("lint blockers should not include truncation marker:\n%s", joined)
	}
}

func TestFallbackTurnRouteRecognizesDebugLogs(t *testing.T) {
	plan := fallbackTurnRoute("Check our most recent logs and explain why it failed.", agent.BuildMode())
	if plan.Family != "debug_logs" {
		t.Fatalf("family = %q, want debug_logs", plan.Family)
	}
	if plan.InitialLane != "explore" || plan.Contract != "grounded_explanation" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanRoutesNonForensicDebugLogFeatureQuestionToArchitecture(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "What does the debug log redacted mode do?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsArchitectureForProjectFeatureQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "architecture"}, "What does the debug log redacted mode do?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanUsesFallbackInsteadOfHardcodedDirectQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "debug_logs"}, "Run the tests and report failures.", agent.BuildMode())
	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}
	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesCodeExplanationAsArchitecture(t *testing.T) {
	plan := fallbackTurnRoute("How does this harness use sub-agents?", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesGDScriptEdits(t *testing.T) {
	plan := fallbackTurnRoute("Update the panel script in ui/status_panel.gd.", agent.BuildMode())
	if plan.Family != "gdscript_edit" {
		t.Fatalf("family = %q, want gdscript_edit", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteRecognizesStartWorkingOnNamedDocAsDocsEdit(t *testing.T) {
	for _, prompt := range []string{
		"can you please start working on docs/writer-route-decision-report-only-plan.md?",
		"can you start woring on docs/writer-route-decision-report-only-plan.md?",
		"can you start implementing docs/writer-route-decision-report-only-plan.md?",
	} {
		plan := fallbackTurnRoute(prompt, agent.BuildMode())
		if plan.Family != "docs_edit" {
			t.Fatalf("family for %q = %q, want docs_edit", prompt, plan.Family)
		}
		if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
			t.Fatalf("unexpected plan for %q: %#v", prompt, plan)
		}
	}
}

func TestCorrectTurnRoutePlanDowngradesImplementationLikeExplanationQuestion(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Explain how this harness uses sub-agents.", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.Contract != "architecture_assessment" {
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

	var chatRequests atomic.Int64
	var sawReviewPrompt atomic.Bool
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
				if strings.Contains(requestText, `"enable_thinking":false`) {
					t.Fatalf("review request should not inherit candidate-scope recovery thinking override:\n%s", requestText)
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing plan.md?")
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
	if planArtifact.SchemaVersion != workspace.PlanArtifactSchemaVersion {
		t.Fatalf("plan artifact schema = %q, want %q", planArtifact.SchemaVersion, workspace.PlanArtifactSchemaVersion)
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing plan.md?")
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
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		return implementationDispatchPlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers: []string{
				"brief lint: brief shape is unknown: no `## Files To Modify` and no markdown deliverable shape",
				"brief lint: unknown fence tag ```go",
			},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
		return implementationDispatchPlanDocumentRefineResult{
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing docs/plan.md?")
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
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		return implementationDispatchPlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers:     []string{"brief lint: brief shape is unknown"},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
		return implementationDispatchPlanDocumentRefineResult{
			OriginalPath: doc.Path,
			OutputPath:   refinedPath,
			ExitCode:     1,
			Stderr:       "refine: max iters reached without ship-as-is verdict",
			Document: implementationDispatchPlanDocument{
				Path:      refinedPath,
				Content:   "# Refined\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| target.txt | modify target |\n",
				EditPaths: []string{"target.txt"},
			},
			PostPreflightBlockers: []implementationDispatchPlanDocumentPreflightResult{{
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing docs/plan.md?")
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
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		if doc.Path == refinedPath {
			return implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
		}
		return implementationDispatchPlanDocumentPreflightResult{
			Path:         doc.Path,
			OK:           false,
			LintBlockers: []string{"brief lint: brief shape is unknown"},
			Blockers:     []string{"brief lint: brief shape is unknown"},
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
		root := implementationDispatchWorkspaceRoot(cfg)
		outAbs, ok := implementationDispatchWorkspaceFile(root, refinedPath)
		if !ok {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 2}
		}
		if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1, Err: err}
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
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1, Err: err}
		}
		refinedDoc, ok := readImplementationDispatchPlanDocument(root, refinedPath)
		if !ok {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: refinedPath, ExitCode: 1}
		}
		return implementationDispatchPlanDocumentRefineResult{
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing docs/plan.md?")
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

func TestRunPromptOnceAtomizesDesignDocRefinesAtomsAndDispatchesPlanExecute(t *testing.T) {
	root := t.TempDir()
	disableQueueDaemonForTurnPlannerTest(t, root)
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		switch {
		case doc.Path == "docs/design.md":
			return implementationDispatchPlanDocumentPreflightResult{
				Path:         doc.Path,
				OK:           false,
				LintBlockers: []string{"brief lint: single-purpose decomposition failed: 3 file-allowlist row(s) across 3 files. Decompose into ADD-only / MODIFY-only / TEST-only sub-atoms before dispatch."},
				Blockers:     []string{"brief lint: single-purpose decomposition failed: 3 file-allowlist row(s) across 3 files. Decompose into ADD-only / MODIFY-only / TEST-only sub-atoms before dispatch."},
			}
		case implementationDispatchIsAtomizedPlanDocumentPath(doc.Path):
			return implementationDispatchPlanDocumentPreflightResult{
				Path: doc.Path,
				OK:   true,
			}
		case strings.HasPrefix(doc.Path, ".idiom/refine/") && strings.HasSuffix(doc.Path, "/accepted.md"):
			return implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
		default:
			return implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
		}
	})
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
		target := ""
		if len(doc.EditPaths) > 0 {
			target = doc.EditPaths[0]
		}
		if target == "" {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, ExitCode: 1, Stderr: "missing atom edit path"}
		}
		root := implementationDispatchWorkspaceRoot(cfg)
		outRel := filepath.ToSlash(filepath.Join(".idiom", "refine", implementationDispatchRefineTitle(doc.Path), "accepted.md"))
		outAbs, ok := implementationDispatchWorkspaceFile(root, outRel)
		if !ok {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 2}
		}
		if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1, Err: err}
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
			"| " + target + " | Apply this atom's design-doc slice. |",
			"",
			"## Verified Current State",
			"",
			"`" + target + "` exists in the test fixture workspace.",
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
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1, Err: err}
		}
		refinedDoc, ok := readImplementationDispatchPlanDocument(root, outRel)
		if !ok {
			return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, OutputPath: outRel, ExitCode: 1}
		}
		return implementationDispatchPlanDocumentRefineResult{
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
		"## Design Intent",
		"",
		"Fixture constants should move from old to new, but each file is a separate dispatch atom.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte(designDoc), 0o644); err != nil {
		t.Fatalf("write design doc: %v", err)
	}

	var plannerPromptRequests atomic.Int64
	var sawAtomPrompt atomic.Bool
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
					!strings.Contains(requestText, "Required `submit_plan` coverage for this run") {
					t.Fatalf("planner request did not receive refined atom brief with required scope:\n%s", requestText)
				}
				sawAtomPrompt.Store(true)
				switch {
				case strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\\n- pkg/a.go\\n") ||
					strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\n- pkg/a.go\n"):
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/a.go.","edits":[{"path":"pkg/a.go","old_text":"const A = \"old\"\n","new_text":"const A = \"new\"\n","purpose":"replace A fixture text"}]}`
				case strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\\n- pkg/b.go\\n") ||
					strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\n- pkg/b.go\n"):
					reply = "<tool_call>\n" +
						`{"tool":"submit_plan","rationale":"Apply atom for pkg/b.go.","edits":[{"path":"pkg/b.go","old_text":"const B = \"old\"\n","new_text":"const B = \"new\"\n","purpose":"replace B fixture text"}]}`
				case strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\\n- pkg/c.go\\n") ||
					strings.Contains(requestText, "Required `submit_plan` coverage for this run: 1 required path(s).\n- pkg/c.go\n"):
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing design.md?")
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
	atomDir := filepath.Join(root, ".idiom", "atoms", implementationDispatchRefineTitle("docs/design.md"))
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
	if !strings.Contains(answer, "Design document implementation completed through 3 atom(s)") {
		t.Fatalf("expected atomized completion answer, got %q", answer)
	}
	if !sawAtomPrompt.Load() {
		t.Fatalf("expected planner to receive atom prompt")
	}
	if plannerPromptRequests.Load() != 3 {
		t.Fatalf("expected one planner request per atom, got %d", plannerPromptRequests.Load())
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
	stubImplementationDispatchPlanDocumentRefine(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
		refineCalls.Add(1)
		return implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path, ExitCode: 1, Stderr: "refine should not run"}
	})
	var preflightCalls atomic.Int64
	stubImplementationDispatchPlanDocumentPreflight(t, func(_ context.Context, _ config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		preflightCalls.Add(1)
		return implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
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
	doc := implementationDispatchPlanDocument{
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
	answer, err := model.runImplementationPlanDocumentsViaPlanExecute(context.Background(), "implement docs/design.md", promptExecutionPlan{}, []implementationDispatchPlanDocument{doc})
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

func TestPlanExecutePlannerThinkingPolicy(t *testing.T) {
	thinkingOn := true
	qwen := &Model{
		cfg: config.Config{
			Chat:        config.Chat{EnableThinking: &thinkingOn, ModelFamily: "qwen"},
			LlamaServer: config.LlamaServer{Model: "Qwen/Qwen3.6-35B"},
		},
		modelID: "qwen3.6-35b",
	}
	if !qwen.shouldDisableThinkingForPlanExecutePlanner() {
		t.Fatalf("local Qwen planner should disable thinking for plan-execute")
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

func TestRunPromptOncePlanExecuteRecoversFromMissingPackageOutlineForNewRequiredFile(t *testing.T) {
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
		"## Implementation Notes",
		"",
		"Create a Go package named newpkg with a Decision function returning ok.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	var sawNewFileNudge atomic.Bool
	var initialPlannerRequests atomic.Int64
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
			case strings.Contains(requestText, "Required new-file directory does not exist yet"):
				sawNewFileNudge.Store(true)
				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Create the new package file from the referenced plan document.","edits":[{"path":"internal/newpkg/decision.go","replace_all":true,"old_text":"","new_text":"package newpkg\n\nfunc Decision() string {\n\treturn \"ok\"\n}\n","purpose":"create the decision helper"}]}`
			default:
				initialPlannerRequests.Add(1)
				if !strings.Contains(requestText, "Required `submit_plan` coverage for this run") ||
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

	answer, err := model.RunPromptOnce(context.Background(), "can you please start working on docs/plan.md?")
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
	if !sawNewFileNudge.Load() {
		t.Fatalf("expected missing package outline to be rewritten into a new-file submit_plan nudge")
	}
	if initialPlannerRequests.Load() != 1 {
		t.Fatalf("expected one initial planner request, got %d", initialPlannerRequests.Load())
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

	answer, err := model.RunPromptOnce(context.Background(), "can you please start working on docs/plan.md?")
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

	var sawRecoveryDecision atomic.Bool
	var sawRetryPrompt atomic.Bool
	var firstPlannerReplies atomic.Int64
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
				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Update target.txt after recovery retry.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace target text"}]}`
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

	answer, err := model.RunPromptOnce(context.Background(), "can you please start working on docs/plan.md?")
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
		t.Fatalf("expected one failed initial planner reply before retry, got %d", firstPlannerReplies.Load())
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

func loadOnlyTimestampedPlanArtifactForTest(t *testing.T, root string) workspace.PlanArtifact {
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

func TestComposeImplementationBriefPromotesPlanDocumentScopeToRequiredPaths(t *testing.T) {
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

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{"docs/handoff.md"}),
	}
	brief := composeImplementationBrief("start working on docs/handoff.md", plan, cfg)
	if !strings.Contains(brief, "## Files to Modify\n\n- target.txt") {
		t.Fatalf("brief missing active required edit scope:\n%s", brief)
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
	if got := requiredEditPathsFromPrompt(brief); !slices.Contains(got, "target.txt") {
		t.Fatalf("plan document scope must become active required edit paths, got %#v\n%s", got, brief)
	}
}

func TestComposeImplementationBriefResolvesUniquePlanDocumentBasename(t *testing.T) {
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
		"| internal/app/writer_route.go | Add writer route decision support |",
		"",
	}, "\n")
	docName := "writer-route-decision-report-only-plan.md"
	if err := os.WriteFile(filepath.Join(root, "docs", docName), []byte(planDoc), 0o644); err != nil {
		t.Fatalf("write plan doc: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	plan := promptExecutionPlan{
		route:        turnRoutePlan{Family: "docs_edit"},
		runtimeBrief: runtimeBriefForTest([]string{docName}),
	}
	brief := composeImplementationBrief("start working on "+docName, plan, cfg)
	for _, want := range []string{
		"## Files to Modify\n\n- internal/app/writer_route.go",
		"### docs/" + docName,
		"> | internal/app/writer_route.go | Add writer route decision support |",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	if got := requiredEditPathsFromPrompt(brief); !slices.Contains(got, "internal/app/writer_route.go") {
		t.Fatalf("resolved plan document scope must become active required edit paths, got %#v\n%s", got, brief)
	}
}

func runtimeBriefForTest(targetRefs []string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{TargetRefs: targetRefs}
}

func TestCorrectTurnRoutePlanUpgradesVerificationOnlyWithEditIntent(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "verification_only"}, "Modify exactly one file and do not run tests.", agent.BuildMode())
	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestFallbackTurnRouteKeepsEditAtomWithAcceptanceCommandsAsImplementation(t *testing.T) {
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
	plan := fallbackTurnRoute(prompt, agent.BuildMode())
	if plan.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.Family)
	}
	if plan.InitialLane != "implement" || plan.Contract != "implementation_result" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestCorrectTurnRoutePlanKeepsAnalysisContinuationReadOnly(t *testing.T) {
	plan := correctTurnRoutePlanForPrompt(turnRoutePlan{Family: "explore_then_implement"}, "Continue your investigation until you have a confident response.", agent.BuildMode())
	if plan.Family != "architecture" {
		t.Fatalf("family = %q, want architecture", plan.Family)
	}
	if plan.InitialLane != "architecture" || plan.FollowupLane != "" || plan.Contract != "architecture_assessment" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestShouldPreferFallbackRoutePreventsModelDowngradeToDirectQuestion(t *testing.T) {
	fallback := fallbackTurnRoute("Ok, check the most recent logs. Lots of weird decisions in there. Can this be made smarter?", agent.BuildMode())
	if fallback.Family != "debug_logs" {
		t.Fatalf("fallback family = %q, want debug_logs", fallback.Family)
	}
	planned := normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, agent.BuildMode())
	if !shouldPreferFallbackRoute(fallback, planned) {
		t.Fatal("expected specialized fallback to override a generic direct-question model route")
	}
}

func TestClassifyTurnRouteLetsModelUpgradeDirectQuestionFallback(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Can you verify that this still builds?", agent.BuildMode())
	if plan.Family != "verification_only" {
		t.Fatalf("family = %q, want verification_only", plan.Family)
	}
	if plan.InitialLane != "main" || plan.Contract != "direct_answer" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestClassifyTurnRouteSkipsModelForDecisiveFallback(t *testing.T) {
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
	plan := m.classifyTurnRoute(context.Background(), "Update the README docs for the new workflow.", agent.BuildMode())
	if plan.Family != "docs_edit" {
		t.Fatalf("family = %q, want docs_edit", plan.Family)
	}
	if calls.Load() != 0 {
		t.Fatalf("deterministic route should skip classifier model call, got %d calls", calls.Load())
	}
}

func TestTurnRoutePlanRuntimeBriefForDirectQuestionUsesReadOnlyWorkspace(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "edit_file", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "direct_question"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	if len(brief.AllowedTools) == 0 {
		t.Fatalf("direct question should advertise read-only tools, got %#v", brief.AllowedTools)
	}
	if len(brief.ReadinessGate) != 0 || !slices.Contains(brief.FinalCritic, "answers_actual_user_request") {
		t.Fatalf("expected direct question to use no readiness gate and a final answer critic, got %#v", brief)
	}
}

func TestTurnRoutePlanRuntimeBriefForVerificationOnly(t *testing.T) {
	available := []string{"task", "pwd", "read_file", "search_files", "search_content", "shell", "edit_file", "write_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "verification_only"}, agent.BuildMode(), available)
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

func TestTurnRoutePlanRuntimeBriefForDebugLogsOmitsPwd(t *testing.T) {
	available := []string{"pwd", "read_file", "search_files", "search_content", "edit_file"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "debug_logs"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	for _, tool := range brief.AllowedTools {
		if tool == "pwd" || tool == "edit_file" {
			t.Fatalf("unexpected tool %q in debug_logs allowed tools %#v", tool, brief.AllowedTools)
		}
	}
	if !strings.Contains(strings.Join(brief.AllowedTools, ","), "search_files") {
		t.Fatalf("expected search_files in allowed tools, got %#v", brief.AllowedTools)
	}
	if len(brief.ReadinessGate) == 0 || len(brief.FinalCritic) == 0 {
		t.Fatalf("expected debug logs to keep operational gates, got %#v", brief)
	}
}

func TestTurnRoutePlanRuntimeBriefForArchitectureUsesShapingOnly(t *testing.T) {
	available := []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol", "shell"}
	brief := turnRoutePlanRuntimeBrief(turnRoutePlan{Family: "architecture"}, agent.BuildMode(), available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	if len(brief.ReadinessGate) == 0 || len(brief.FinalCritic) == 0 {
		t.Fatalf("expected architecture analysis to keep a closure gate and final critic, got %#v", brief)
	}
}
