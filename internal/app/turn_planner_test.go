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
	var plannerPromptRequests atomic.Int64
	var sawRequiredScope atomic.Bool
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
				if !strings.Contains(requestText, `"enable_thinking":false`) {
					t.Fatalf("initial local in-process plan-execute planner request should force thinking off:\n%s", requestText)
				}
				plannerPromptRequests.Add(1)
				if !strings.Contains(requestText, "replace fixture text") {
					t.Fatalf("planner request did not include referenced plan document body:\n%s", requestText)
				}
				for _, want := range []string{
					"Required `submit_plan` coverage for this run",
					"Treat the brief body and any preloaded Context Pack below as the planner target packet.",
					"## Context Pack",
					"IDIOM:BRIEF-BODY-START",
					"## Files to Modify",
					"- pkg/target.go",
				} {
					if !strings.Contains(requestText, want) {
						t.Fatalf("planner request missing required edit-scope contract %q:\n%s", want, requestText)
					}
				}
				for _, forbidden := range []string{
					"Allowed `submit_plan` edit scope for this run",
					"No required_edit_paths list is active",
					"## Candidate Edit Scope From Target References",
				} {
					if strings.Contains(requestText, forbidden) {
						t.Fatalf("planner request still used candidate-scope contract %q:\n%s", forbidden, requestText)
					}
				}
				sawRequiredScope.Store(true)
				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Update pkg/target.go from the referenced plan document.","edits":[{"path":"pkg/target.go","old_text":"const Value = \"old\"\n","new_text":"const Value = \"new\"\n","purpose":"replace the target fixture text"}]}`
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

	answer, err := model.RunPromptOnce(context.Background(), "can you start implementing docs/plan.md?")
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
	if plannerPromptRequests.Load() != 1 {
		t.Fatalf("expected one initial planner request, got %d", plannerPromptRequests.Load())
	}
	if !sawRequiredScope.Load() {
		t.Fatalf("expected TUI plan-doc scope to become active required_edit_paths")
	}
	if !sawReviewPrompt.Load() {
		t.Fatalf("expected review-repair prompt after executor phase")
	}
	if chatRequests.Load() < 2 {
		t.Fatalf("expected planner and review chat requests, got %d", chatRequests.Load())
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

	var sawRefinedBrief atomic.Bool
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
				if !strings.Contains(requestText, "### "+refinedPath) ||
					!strings.Contains(requestText, "## Files to Modify") ||
					!strings.Contains(requestText, "- target.txt") {
					t.Fatalf("planner request did not use refined dispatch brief:\n%s", requestText)
				}
				sawRefinedBrief.Store(true)
				reply = "<tool_call>\n" +
					`{"tool":"submit_plan","rationale":"Update target.txt from the refined plan document.","edits":[{"path":"target.txt","old_text":"old\n","new_text":"new\n","purpose":"replace target text"}]}`
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
	if !sawRefinedBrief.Load() {
		t.Fatalf("expected planner to receive refined brief")
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
