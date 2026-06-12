package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerInjectsPlanExecuteModelProtocolProjectionCard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\nfunc A() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "<<QUESTION>>: exact blocker"},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxModelCalls: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:                 "plan-execute",
			TaskFamily:           PlanThenExecutePlannerTaskFamily,
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "code_navigate", "submit_plan"},
			RequiredTools:        []string{"submit_plan"},
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			VerificationPlan:     []string{"go test ./..."},
		},
	}

	_, _ = runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{
		ResponseFormatJSON: true,
	}, nil, nil, nil)

	var sawBrief, sawProtocolCard bool

	for i, msg := range payload.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[active contract protocol]") {
			sawProtocolCard = true

			if !strings.Contains(msg.Content, "protocol_hash=") ||
				!strings.Contains(msg.Content, "Visible tools:") ||
				!strings.Contains(msg.Content, "Callable tools now:") ||
				!strings.Contains(msg.Content, "Path roles:") ||
				!strings.Contains(msg.Content, "Safe alternatives:") {

				t.Fatalf("protocol card missing required fields at message %d: %s", i, msg.Content)
			}
		}

		if msg.Role == "system" && strings.Contains(msg.Content, "[frontier step instruction]") {
			sawBrief = true
		}
	}

	if !sawProtocolCard {
		t.Fatalf("expected model-visible protocol card in request messages: %#v", payload.Messages)
	}

	if !sawBrief {
		t.Fatalf("expected existing runtime brief to remain in request messages: %#v", payload.Messages)
	}
}

func TestRunnerInjectsVerificationBootstrapInstruction(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
	}
	bootstrap := runner.initialBootstrapInstruction()

	if !strings.Contains(bootstrap, "[verification bootstrap]") || !strings.Contains(bootstrap, "Configured verification command: go test ./...") {
		t.Fatalf("expected verification bootstrap instruction, got %q", bootstrap)
	}
}

func TestVerificationOnlyAutoRunsShellWithoutModelRoundTrip(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient("http://127.0.0.1:1", time.Millisecond),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			CommandTimeoutSeconds: 2,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell", "read_file", "search_files", "search_content"},
			VerificationPlan:  []string{"go test ./..."},
		},
	}

	var requested []workspace.ToolRequest

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "run tests"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			requested = append(requested, event.Request)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(requested) != 1 || requested[0].Tool != "shell" || requested[0].Command != "go test ./..." {
		t.Fatalf("expected deterministic verification shell request, got %#v", requested)
	}

	if !strings.Contains(answer, "Verification failed.") || !strings.Contains(answer, "No files were edited.") {
		t.Fatalf("expected synthesized verification failure answer, got:\n%s", answer)
	}
}

func TestVerificationOnlyRecoveryNudgeSuggestsVerificationCommand(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "shell", Command: "pwd"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
		"main",
		false,
		false,
	)
	if !strings.Contains(nudge, "configured plan starts with `go test ./...`") {
		t.Fatalf("expected verification command in recovery nudge, got:\n%s", nudge)
	}

	if strings.Contains(nudge, `{"tool":"search_files"`) || strings.Contains(nudge, `{"tool":"search_content"`) {
		t.Fatalf("verification recovery nudge should not suggest discovery first, got:\n%s", nudge)
	}
}

func TestImplementationRecoveryNudgeSuggestsVerificationAfterWrites(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read main.go lines 1-3", Output: "one\ntwo\nthree"},
		{Tool: "edit_file", Summary: "Edited main.go lines 1-1"},
		{Tool: "edit_file", Summary: "Edited main.go lines 2-2"},
		{
			Tool:    "read_file",
			Summary: "Verification required after workspace edits",
			Error:   "the workspace has 2 successful write(s) since the last verification.",
		},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "main.go"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
		"main",
		false,
		false,
	)

	if !strings.Contains(nudge, "pending verification") ||
		!strings.Contains(nudge, "git diff --check") {

		t.Fatalf("expected verification-first nudge, got:\n%s", nudge)
	}

	if strings.Contains(nudge, "smaller window") || strings.Contains(nudge, "structured probe") {
		t.Fatalf("verification recovery nudge should not suggest more inspection, got:\n%s", nudge)
	}
}

func TestExactAnchorPreWriteRecoveryNudgeDoesNotSuggestReadRefinement(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "scripts/ui/depot_panel.gd"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
			TargetRefs:        []string{"scripts/ui/depot_panel.gd"},
		},
		"main",
		false,
		false,
	)
	for _, want := range []string{"exact-anchor implementation pre-write", "Exact-anchor write capability", "<<QUESTION>>"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected exact-anchor recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	for _, forbidden := range []string{"smaller window", "structured probe"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("exact-anchor recovery nudge should not suggest %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestRequiredPathRecoveryNudgeDoesNotAnswerFirst(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/energy.cpp"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_energy.cpp"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{
				"native/src/energy.cpp",
				"native/tests/test_energy.cpp",
			},
		},
		"main",
		false,
		false,
	)

	for _, want := range []string{"required edit path", "native/tests/test_energy.cpp", "allowed write outcome", "bounded `edit_file`", "`apply_patch`"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected required-path recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	for _, forbidden := range []string{"Answer with the evidence already gathered", "Only take one narrow next step", "smaller window", "structured probe", "Required outcomes are an allowed write"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("required-path recovery nudge should not suggest %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestPlanExecuteRequiredPathRecoveryNudgeNamesSubmitPlan(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_energy.cpp"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{
				"native/src/energy.cpp",
				"native/tests/test_energy.cpp",
			},
		},
		"main",
		false,
		false,
	)
	for _, want := range []string{"plan-execute planner", "successful `submit_plan` coverage", "put paths needing changes in `edits[]`", "top-level `source_satisfied_paths`"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected plan-execute recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	for _, forbidden := range []string{"allowed write", "successful write:"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("plan-execute recovery nudge should not use generic implementation write wording %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestPlanExecuteSubmitCorrectionRecoveryNudgeIsSubmitPlanOnly(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/energy.cpp lines 1-10 (total lines: 10)", Output: "1:int x;\n"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_energy.cpp lines 1-10 (total lines: 10)", Output: "1:int y;\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `native/tests/test_energy.cpp`.",
		},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_energy.cpp"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{
				"native/src/energy.cpp",
				"native/tests/test_energy.cpp",
			},
		},
		"main",
		false,
		false,
	)

	if !strings.Contains(nudge, "Submit-correction mode has exactly one valid outcome") ||
		!strings.Contains(nudge, "parser-readable `submit_plan`") {

		t.Fatalf("expected strict submit-correction recovery nudge, got:\n%s", nudge)
	}

	for _, forbidden := range []string{"current-state evidence", "<<QUESTION>> naming"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("submit-correction nudge advertised stale exit %q:\n%s", forbidden, nudge)
		}
	}
}

func TestTargetInspectionRecoveryNudgeStaysInsideTarget(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "go_file_outline", Summary: "Outlined 15 top-level declaration(s) in internal/surfaces/tui/app/turn_planner.go.", Output: "package app"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/surfaces/tui/app/turn_planner.go", Pattern: "turnRoutePlan"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		},
		"main",
		false,
		false,
	)

	if !strings.Contains(nudge, "already has source evidence and target-file evidence") {
		t.Fatalf("expected target-inspection guidance, got:\n%s", nudge)
	}

	if !strings.Contains(nudge, "Relevant target window: docs/frontier-routing-implementation-plan.md lines 340-359") {
		t.Fatalf("expected narrow target window, got:\n%s", nudge)
	}

	if strings.Contains(nudge, `{"tool":"search_files"`) || strings.Contains(nudge, `{"tool":"search_content","pattern":"TODO|FIXME"`) {
		t.Fatalf("target-inspection recovery nudge should not suggest broad discovery, got:\n%s", nudge)
	}
}

func TestPrematureTargetReadRecoveryNudgeSuggestsSourceEvidence(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols with gopls", Output: "internal/surfaces/tui/app/turn_planner.go:4 turnRoutePlan\ninternal/surfaces/tui/app/model_state.go:10 routeState\n"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		},
		"main",
		false,
		false,
	)

	if !strings.Contains(nudge, "This turn still needs source evidence before the named target file.") {
		t.Fatalf("expected source-evidence guidance, got:\n%s", nudge)
	}

	if !strings.Contains(nudge, "internal/surfaces/tui/app/turn_planner.go") {
		t.Fatalf("expected non-target source path, got:\n%s", nudge)
	}

	if strings.Contains(nudge, `{"tool":"read_file","path":"docs/frontier-routing-implementation-plan.md"`) {
		t.Fatalf("premature target recovery nudge should not suggest reading the target again, got:\n%s", nudge)
	}
}

func TestClosureRecoveryNudgeAvoidsBroadRediscoveryAfterReadiness(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/runtime/agent/runner.go","excerpt":"result := r.runChildTask(ctx, req, opts)"},{"path":"internal/runtime/agent/runner_child_tasks.go","excerpt":"You are a read-only delegated child task."}]}`,
	}, {
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 1-80",
		Output:  "package agent\nfunc runChildTask() {}\n",
	}}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
		"child:explorer",
		true,
		false,
	)

	for _, want := range []string{"Answer with the evidence already gathered", "one narrow next step", "exact missing claim"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected closure recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	for _, forbidden := range []string{`{"tool":"search_files"`, `{"tool":"search_content","pattern":"TODO|FIXME"`} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("closure recovery nudge should not reopen broad discovery with %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestFailureFeedbackPacketKeepsRepeatedActionClosureBiased(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/runtime/agent/runner.go","excerpt":"result := r.runChildTask(ctx, req, opts)"},{"path":"internal/runtime/agent/runner_child_tasks.go","excerpt":"You are a read-only delegated child task."}]}`,
	}}
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/answer_composer.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Repeated tool request rejected",
		Error:   "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes.",
	}
	packet := buildRunnerFailureFeedbackPacket(
		"child:explorer",
		3,
		"tool_rejected",
		req,
		result,
		results,
		false,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
	)
	joined := strings.Join(append(append([]string{}, packet.RequiredNext...), packet.ValidNext...), "\n")

	for _, want := range []string{
		"answer from the current evidence",
		"one exact tool-shaped follow-up",
		"concrete missing claim",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure-biased failure feedback to contain %q, got %#v", want, packet)
		}
	}

	for _, forbidden := range []string{"search_files on a discovered path or pattern", "use a targeted read-only shell search"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("failure feedback should not reintroduce broad fallback %q, got %#v", forbidden, packet)
		}
	}
}

func TestVerificationOnlyRedirectsPreCheckExplorationToVerificationCommand(t *testing.T) {
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"search_files","pattern":"*.go","path":"."}`},
			}},
		})
	}))

	defer server.Close()

	var requested []workspace.ToolRequest

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			CommandTimeoutSeconds: 2,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell", "read_file", "search_files", "search_content"},
			VerificationPlan:  []string{"go test ./..."},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "run tests and report failure"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			requested = append(requested, event.Request)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 0 {
		t.Fatalf("verification-only run should not ask the model before running shell, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Verification failed.") {
		t.Fatalf("expected synthesized verification answer, got %q", answer)
	}

	if len(requested) == 0 {
		t.Fatalf("expected at least one tool request")
	}

	if requested[0].Tool != "shell" || requested[0].Command != "go test ./..." || requested[0].Workdir != "." {
		t.Fatalf("expected first tool request to be redirected verification command, got %#v", requested[0])
	}
}

func TestDebugLogEvidenceSatisfiedAfterSingleHitAndReadWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line at logs/llama-server.log:247005.",
			Output:  "f3 logs/llama-server.log:247005:request exceeds context\ncontext:\n247004:foo",
		},
		{
			Tool:    "read_file",
			Summary: "Read f3 logs/llama-server.log lines 247000-247079 (file has 268859 lines total; open-ended log range capped to 80 lines; pass an explicit end_line for a different bounded window)",
			Output:  "247000:...\n247005:request exceeds context",
		},
	}

	if !debugLogEvidenceSatisfied(brief, results) {
		t.Fatalf("expected debug-log evidence to be satisfied for %#v", results)
	}
}

func TestDebugLogFollowupRedirectsSearchToHitWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line at logs/llama-server.log:247005.",
		Output:  "f3 logs/llama-server.log:247005:request exceeds context",
	}}
	redirected, ok := debugLogFollowupRequest(brief, results, workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "send_error",
		Path:    "logs",
		Glob:    "llama-server.log",
	})

	if !ok {
		t.Fatal("expected redirect for repeated search after concrete log hit")
	}

	if redirected.Tool != "read_file" || redirected.ID != "f3" {
		t.Fatalf("unexpected redirect target: %#v", redirected)
	}

	if redirected.StartLine > 247005 || redirected.EndLine < 247005 {
		t.Fatalf("redirected window must cover hit line, got %#v", redirected)
	}
}

func TestDebugLogPreflightRedirectsGenericSearchToNewestLogDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", "{\"event\":\"x\"}\n"); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
	}
	redirected, ok := runner.debugLogPreflightRequest(nil, workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "*log*",
	})

	if !ok {
		t.Fatal("expected redirect to newest-log discovery")
	}

	if redirected.Tool != "search_files" || redirected.Pattern != "logs/**/*" {
		t.Fatalf("unexpected redirect request: %#v", redirected)
	}
}

func TestDiagnosticLogIntentDistinguishesSourceFeatureQuestions(t *testing.T) {
	sourceQuestion := strings.Join([]string{
		"Investigate what \"debug log redacted mode\" does in this codebase.",
		"Where is redacted mode defined for debug logging?",
		"What configuration or flag controls it?",
		"What does it do when enabled and where is the redaction logic applied?",
		"Search broadly in Go files and debug log configuration types.",
	}, "\n")
	if objectiveRequestsDiagnosticLogInspection(sourceQuestion) {
		t.Fatal("debug-log mode source feature question must not activate diagnostic-log artifact scope")
	}

	if !objectiveRequestsDiagnosticLogInspection("Inspect the newest debug log for why the run failed.") {
		t.Fatal("newest debug log failure request should still activate diagnostic-log artifact scope")
	}
}

func TestDebugLogPreflightRedirectsToSelectedLogTail(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", "{\"event\":\"x\"}\n"); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})

	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}

	runner := &Runner{
		Workspace:    mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
	}
	redirected, ok := runner.debugLogPreflightRequest([]workspace.ToolResult{search}, workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chosen_tool",
		Glob:    "*.go",
	})

	if !ok {
		t.Fatal("expected redirect to selected-log tail read")
	}

	if redirected.Tool != "read_file" {
		t.Fatalf("expected read_file redirect, got %#v", redirected)
	}

	if redirected.ID == "" && redirected.Path != "logs/conversations/recent.jsonl" {
		t.Fatalf("expected selected log target, got %#v", redirected)
	}
}

func TestConversationLogInvestigationPreflightSkipsCurrentSessionLog(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/20260607-132011.621.jsonl", "{\"event\":\"prior\"}\n"); err != nil {
		t.Fatalf("write prior log file: %v", err)
	}

	if err := writeTestFile(root, "logs/conversations/20260607-132703.112.jsonl", "{\"event\":\"active\"}\n"); err != nil {
		t.Fatalf("write active log file: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
		SessionID:           "20260607-132703.112",
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})

	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}

	stable, ok := latestStableDebugLogSelection([]workspace.ToolResult{search}, mgr.SessionID())
	if !ok {
		t.Fatalf("expected a stable prior log selection in %#v", search)
	}

	if currentSessionDebugLogPath(stable.Path, mgr.SessionID()) {
		t.Fatalf("stable selection should not be the active session log: %#v", stable)
	}

	runner := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:         "conversation",
			Contract:           "direct_answer",
			PermissionProfile:  "read_only_workspace",
			Objective:          "Investigate the newest recent conversation/log failure involving a read attempt.",
			ReadOnlyConstraint: true,
		},
	}
	redirected, ok := runner.debugLogPreflightRequest([]workspace.ToolResult{search}, workspace.ToolRequest{
		Tool: "read_file",
		Path: "logs/conversations/20260607-132703.112.jsonl",
	})

	if !ok {
		t.Fatal("expected conversation log investigation to redirect away from the active session log")
	}

	if !debugLogRequestTargetsSelection(redirected, stable) {
		t.Fatalf("redirect should target stable prior log %#v, got %#v", stable, redirected)
	}
}

func TestDebugLogPhaseScopeRejectsSourceDriftAfterTail(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", strings.Repeat("{\"event\":\"x\"}\n", 20)); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
		MaxFileBytes:        1 << 20,
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})

	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}

	selection, ok := parseDebugLogFileSelection(search)
	if !ok {
		t.Fatalf("expected search result to expose a log selection: %#v", search)
	}

	readReq := debugLogTailRequest(selection)
	read := mgr.Execute(context.Background(), readReq)

	if read.Error != "" {
		t.Fatalf("read_file failed: %#v", read)
	}

	results := []workspace.ToolResult{search, read}
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chosen_tool",
		Glob:    "*.go",
	}, results, brief); !reject {
		t.Fatal("expected source drift to be rejected after selected-log tail read")
	}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool: "read_file",
		ID:   selection.Handle,
	}, results, brief); !reject {
		t.Fatal("expected blind reread of selected log to be rejected")
	}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool:      "read_file",
		ID:        selection.Handle,
		Path:      selection.Path,
		StartLine: 5,
		EndLine:   20,
	}, results, brief); !reject {
		t.Fatal("expected second selected-log read without a concrete hit to be rejected")
	}
}

func TestConversationLogInvestigationReusesDebugLogScopeGate(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", strings.Repeat("{\"event\":\"x\"}\n", 20)); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
		MaxFileBytes:        1 << 20,
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})

	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}

	selection, ok := parseDebugLogFileSelection(search)
	if !ok {
		t.Fatalf("expected search result to expose a log selection: %#v", search)
	}

	read := mgr.Execute(context.Background(), debugLogTailRequest(selection))
	if read.Error != "" {
		t.Fatalf("read_file failed: %#v", read)
	}

	results := []workspace.ToolResult{search, read}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:         "conversation",
		Contract:           "direct_answer",
		PermissionProfile:  "read_only_workspace",
		Objective:          "Identify the recent conversation log failure.",
		ReadOnlyConstraint: true,
	}

	if phase := debugLogPhase(brief, results); phase != "selected_log_analysis" {
		t.Fatalf("conversation log investigation phase = %q, want selected_log_analysis", phase)
	}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chosen_tool",
		Glob:    "*.go",
	}, results, brief); !reject {
		t.Fatal("expected conversation log investigation to reject source drift after selected-log tail")
	}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool: "read_file",
		ID:   selection.Handle,
	}, results, brief); !reject {
		t.Fatal("expected conversation log investigation to reject blind selected-log reread")
	}
}

func TestConceptualDebugLogQuestionDoesNotEnterLogInvestigationContract(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/evidence/transcript/debuglog.go", "package transcript\n"); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	mgr := workspace.NewManager(config.Workspace{
		Root: root,
		CWD:  root,
	})
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:         "conversation",
		Contract:           "direct_answer",
		PermissionProfile:  "read_only_workspace",
		Objective:          "What does the debug log redacted mode alternative do?",
		ReadOnlyConstraint: true,
	}

	if debugLogContractApplies(brief, nil) {
		t.Fatal("conceptual debug-log source question should not become a diagnostic-log investigation")
	}

	runner := &Runner{Workspace: mgr, RuntimeBrief: brief}
	if redirected, ok := runner.debugLogPreflightRequest(nil, workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/evidence/transcript/debuglog.go",
	}); ok {
		t.Fatalf("conceptual source read should not redirect to log discovery: %#v", redirected)
	}
}

func TestSourcePromptMentioningDebugLogsDoesNotEnterLogInvestigationContract(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:         "conversation",
		Contract:           "direct_answer",
		PermissionProfile:  "read_only_workspace",
		Objective:          "Read these files: docs/design-hosted-cache-cost-metrics.md, internal/runtime/llama/client.go, internal/runtime/agent/runner_state.go, internal/surfaces/tui/app/model_state.go, and internal/evidence/transcript/debuglog.go. Explain how Idiom carries prompt cache hit/miss metrics from provider response to session_summary/debug logs.",
		ReadOnlyConstraint: true,
	}
	if debugLogContractApplies(brief, nil) {
		t.Fatal("source-reading task that mentions session_summary/debug logs should not become a diagnostic-log investigation")
	}
}

func TestActualConversationLogInvestigationEntersLogInvestigationContract(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:         "conversation",
		Contract:           "direct_answer",
		PermissionProfile:  "read_only_workspace",
		Objective:          "Investigate the actual conversation logs and verify why the final answer was rejected as objective unmet.",
		ReadOnlyConstraint: true,
	}
	if !debugLogContractApplies(brief, nil) {
		t.Fatal("explicit conversation-log incident investigation should use the diagnostic-log contract")
	}
}

func TestForcedCompletionTreatsXMLToolMarkupAsToolRequest(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"<tool_call>\n<function=read_file>\n<parameter=path>README.md</parameter>\n</function>\n</tool_call>",
		"README.md was inspected and contains the gathered evidence needed for this answer.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(answer, "<tool_call>") || !strings.Contains(answer, "README.md was inspected") {
		t.Fatalf("expected repaired prose answer instead of XML tool markup, got:\n%s", answer)
	}

	if strings.Contains(answer, "Partial findings") {
		t.Fatalf("expected normal evidence answer without partial-findings banner, got:\n%s", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected invalid final answer retry, got %d requests", requests)
	}
}

func TestForcedCompletionRepairsTextualJSONToolRequestBeforeFallback(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		`{"tool":"read_file","path":"README.md","named_gap":"read more before answering"}`,
		"The harness should answer from the evidence already gathered instead of requesting another tool.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != replies[2] {
		t.Fatalf("expected repaired prose answer before deterministic fallback, got:\n%s", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected invalid final answer retry, got %d requests", requests)
	}
}

func TestCompletionGateRejectsUnresolvedProcessNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("The context_pack result is polluted. I need to find the actual TUI/input rendering code. Let me search for the input field implementation.")
	if !rejected {
		t.Fatal("expected unresolved process narration to be rejected")
	}

	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}

	if _, rejected := unresolvedProcessIntentCompletionRejection("Blocked: I cannot inspect the logs because the file is missing."); rejected {
		t.Fatal("concrete blocker answer should not be rejected as process narration")
	}
}

func TestCompletionGateAllowsQuotedProcessNarrationEvidence(t *testing.T) {
	answer := strings.Join([]string{
		"Here is the diagnosis.",
		"",
		"The hidden reasoning salvage path accepted a non-answer as the final response. The extracted text was:",
		"",
		`> "The user is asking me to investigate why a prompt might be routed as write intent. I need to find the specific route-planner files/tests responsible."`,
		"",
		"The likely guard is `looksLikeNonAnswerProse` in `internal/runtime/agent/runner.go`, and the tests belong beside the live-failure cases in `internal/runtime/agent/runner_test.go`.",
	}, "\n")
	if result, rejected := unresolvedProcessIntentCompletionRejection(answer); rejected {
		t.Fatalf("quoted evidence should not make a substantive answer look like process narration: %#v", result)
	}
}

func TestCompletionGateRejectsFutureRefreshNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("Both required edit paths are stale in the graph. I'll refresh them, then read the two files to verify the TUI smoke fixture.")
	if !rejected {
		t.Fatal("expected future refresh/read narration to be rejected")
	}

	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestCompletionGateRejectsShouldCallToolNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("I should use the context_pack tool to get a good overview of the relevant files. Let me call context_pack now.")
	if !rejected {
		t.Fatal("expected tool-call intent narration to be rejected")
	}

	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestCompletionGateAllowsBenignLetMeTryExplanation(t *testing.T) {
	if result, rejected := unresolvedProcessIntentCompletionRejection("Let me try to explain the failure from the evidence we already have."); rejected {
		t.Fatalf("benign explanatory prose should not be rejected: %#v", result)
	}
}

func TestReadOnlyCompletionGateRejectsSearchHitInventory(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/evidence/transcript/debuglog.go lines 1-80",
		Output:  "19:\tDebugLogModeRedacted DebugLogMode = \"redacted\"\n",
	}}
	reply := "The inspected source in `internal/runtime/agent/runner_test.go` contains `f9 testdata/swe/live-subset.json:8: \"prompt\": \"What does the debug log redacted mode do?\"`."
	result, rejected := runner.readOnlySearchHitInventoryCompletionRejection(reply, results)

	if !rejected {
		t.Fatal("expected search-hit inventory answer to be rejected")
	}

	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "search handles") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}
