package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestReviewRepairBroadReadGuardRejectsLargeRawReads(t *testing.T) {
	root := t.TempDir()

	var b strings.Builder

	for i := 0; i < planThenExecuteReviewRepairMaxRawReadLines+20; i++ {
		fmt.Fprintf(&b, "line %03d\n", i+1)
	}

	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool: "read_file",
		Path: "large_test.go",
	}); !got.Intercepted || !strings.Contains(got.Result.Error, "Phase Evidence") || !strings.Contains(got.Result.Error, "context_status") {
		t.Fatalf("review-repair must reject unbounded large raw reads with evidence-led guidance, got %#v", got)
	}

	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "large_test.go",
		StartLine: 1,
		EndLine:   planThenExecuteReviewRepairMaxRawReadLines + 1,
	}); !got.Intercepted || !strings.Contains(got.Result.Error, "at most 200 lines") {
		t.Fatalf("review-repair must reject overly broad explicit ranges, got %#v", got)
	}
}

func TestReviewRepairBroadReadGuardAllowsNarrowRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(strings.Repeat("x\n", 500)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "large_test.go",
		StartLine: 25,
		EndLine:   50,
	}); got.Intercepted {
		t.Fatalf("review-repair should allow one narrow source range, got %#v", got)
	}
}

func TestReviewRepairBroadReadGuardDoesNotAffectPlanner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(strings.Repeat("x\n", 500)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool: "read_file",
		Path: "large_test.go",
	}); got.Intercepted {
		t.Fatalf("review-repair broad read guard must not change planner read policy, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRecoveryUsesEvidenceLedgerNotBroadReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one of: Decision: pass, submit_plan, or <<QUESTION>>",
		},
	})

	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}

	if !hideReads {
		t.Fatal("review-repair completion recovery should hide read/discovery tools after the closeout gate fires")
	}

	for _, want := range []string{
		"Decision: pass",
		"Verification Checklist",
		"corrective `submit_plan`",
		"<<QUESTION>>",
		"Do not add prose before or after",
		"Phase Evidence Bundle",
		"hidden reasoning",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		"read_file",
		"code_navigate",
		"context_query",
		"context_zoom",
		"context_status",
		"targeted probe",
		"at most 200 lines",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review-repair terminal recovery must not invite more discovery via %q:\n%s", forbidden, prompt)
		}
	}

	if strings.Contains(prompt, "produce the missing evidence or edit required by the gate") {
		t.Fatalf("review-repair recovery should not use generic completion-gate wording:\n%s", prompt)
	}
}

func TestReviewRepairCompletionGateRecoveryWithFailedEvidenceSuppressesPass(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"verification_failed:go test ./..."},
			},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})

	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}

	if !hideReads {
		t.Fatal("review-repair completion recovery should hide read/discovery tools after the closeout gate fires")
	}

	for _, want := range []string{
		"`Decision: pass` is invalid",
		"verification_failed:go test ./...",
		"corrective `submit_plan`",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair failed-evidence prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		"`Decision: pass` with a `Verification Checklist`",
		"Pass: `Decision: pass`",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review-repair failed-evidence prompt must not advertise pass shape %q:\n%s", forbidden, prompt)
		}
	}
}

func TestReviewRepairCompletionGateRecoveryForcesVisibleOutput(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, nil)

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("review-repair recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatal("review-repair recovery must not preserve hidden reasoning in the next request")
	}

	if opts.MaxTokens != 8192 {
		t.Fatalf("review-repair recovery should preserve corrective-plan budget, got %d", opts.MaxTokens)
	}
}

func TestReviewRepairTerminalRecoveryRejectsReadsWithoutGenericWriteOnlyBlocker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"code_navigate","capability":"read_symbol","path":"coverage_gate.go","pattern":"Check"}`,
		"Decision: pass\n\nVerification Checklist:\n- Existing phase evidence is sufficient.\n- No corrective plan is required.",
	}

	var (
		requests            int
		sawTerminalRequired bool
	)

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
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				sawTerminalRequired = true
			}

			if strings.Contains(event.Content, "Write-only recovery blocked non-write tool") {
				t.Fatalf("review-repair terminal recovery must not fall through to generic write-only blocker: %s", event.Content)
			}

			if event.Role == "next_action_state" || event.Role == "failure_feedback" {
				forbidden := []string{
					"answer from the current evidence if it already resolves the task",
					"otherwise take one exact tool-shaped follow-up",
				}
				for _, text := range forbidden {
					if strings.Contains(event.Content, text) {
						t.Fatalf("review-repair terminal recovery must not advertise generic follow-up %q in %s: %s", text, event.Role, event.Content)
					}
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected recovery to continue through terminal pass, got %d request(s)", requests)
	}

	if !sawTerminalRequired {
		t.Fatalf("expected review-repair terminal recovery event")
	}

	if !strings.Contains(answer, "Decision: pass") {
		t.Fatalf("expected terminal pass answer, got:\n%s", answer)
	}
}

func TestReviewRepairTerminalProbeUsesPhaseEvidencePassWithoutRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "internal_labels.go"), []byte("package labels\n\nfunc ShortLabel() string { return \"Save\" }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var (
		requests            int
		sawTerminalRequired bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20}`},
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
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			ChangedPaths:      []string{"internal/labels/labels.go"},
			AllowedEditPaths:  []string{"internal/labels/labels.go"},
			Objective: reviewRepairPhaseEvidenceObjectiveForTest(
				"schema: idiom.phase_evidence.v1",
				"phase: review_repair",
				"submitted_plan_state:",
				"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
				"source_sanity:",
				"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
				"verification:",
				"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
				"patch_hygiene: status=passed",
			),
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				sawTerminalRequired = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 1 {
		t.Fatalf("expected deterministic pass after the first hidden probe, got %d requests", requests)
	}

	if sawTerminalRequired {
		t.Fatal("positive phase evidence should close without a terminal-required rejection")
	}

	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected phase-evidence pass answer, got:\n%s", answer)
	}
}

func TestReviewRepairTerminalRecoveryStopsRepeatedHiddenDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"code_navigate","capability":"read_symbol","path":"coverage_gate.go","pattern":"Check"}`,
		`{"tool":"context_query","query":"coverage gate callers","resolution":"symbol"}`,
		"Decision: pass\n\nVerification Checklist:\n- This response should not be requested.",
	}

	var (
		requests               int
		terminalRequiredEvents int
	)

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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				terminalRequiredEvents++
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 3 {
		t.Fatalf("expected runner to stop after repeated hidden discovery, got %d request(s)", requests)
	}

	if terminalRequiredEvents != 2 {
		t.Fatalf("expected two terminal-required events, got %d", terminalRequiredEvents)
	}

	if !strings.Contains(answer, "repeatedly requested hidden discovery after the closeout gate") {
		t.Fatalf("expected deterministic repeated-discovery blocker, got:\n%s", answer)
	}

	if strings.Contains(answer, "This response should not be requested") {
		t.Fatalf("runner requested another completion after repeated hidden discovery:\n%s", answer)
	}
}

func TestReviewRepairTerminalRecoveryAllowsCorrectiveSubmitPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"submit_plan","rationale":"Verification Checklist:\n- Corrective edit is needed for the failed review evidence.","edits":[{"path":"coverage_gate.go","old_text":"return false","new_text":"return true","purpose":"correct the reviewed coverage gate result"}]}`,
	}

	var requests int

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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if strings.Contains(event.Content, "Write-only recovery blocked non-write tool") {
				t.Fatalf("corrective submit_plan must not hit generic write-only blocker: %s", event.Content)
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected terminal corrective plan after one recovery prompt, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected corrective submit_plan to be accepted, got:\n%s", answer)
	}
}

func TestReviewRepairCompletionCloseoutDoesNotInventMissingWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			RequiredEditPaths: []string{"widget_config.json", "widget_notes.md"},
		},
	}
	answer := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool:  "completion_gate",
		Error: "review-repair must close with Decision: pass or a corrective plan",
	}, nil)

	if strings.Contains(answer, "Required edit paths not changed") || strings.Contains(answer, "no successful write was recorded") {
		t.Fatalf("review-repair closeout should not describe allowed repair scope as missing writes:\n%s", answer)
	}

	if !strings.Contains(answer, "review-repair must close") {
		t.Fatalf("closeout should preserve the actual completion blocker, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanGuardSuppressesMalformedPassPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got := runner.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior passes.",
		Edits:     []workspace.PlanEdit{{Purpose: "verification pass"}},
	})

	if !got.Intercepted {
		t.Fatal("review-repair malformed/no-path submit_plan should be suppressed before workspace schema errors")
	}

	if got.Result.Tool != "review_repair_noop_plan" || strings.TrimSpace(got.Result.Error) != "" {
		t.Fatalf("noop review plan should be a non-error nudge, got %#v", got.Result)
	}

	if _, ok := planThenExecuteShortCircuitAnswer(runner.RuntimeBrief, workspace.ToolRequest{Tool: "submit_plan"}, got.Result); ok {
		t.Fatal("suppressed review-repair noop plan must not look like a successful corrective submit_plan")
	}
}

func TestReviewRepairNoopSubmitPlanForcesTerminalCloseout(t *testing.T) {
	replies := []string{
		`{"tool":"submit_plan","rationale":"No corrective edits are needed.","edits":[]}`,
		"Decision: pass\n\nVerification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: pending outside this lane.",
	}

	var requests int

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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "doom_loop_guard" {
				t.Fatalf("review-repair noop submit_plan must close before doom-loop approval: %s", event.Content)
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected one forced terminal closeout after noop submit_plan, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected terminal review-repair pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutFromChecklistRationale(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/example/target.go"},
		AllowedEditPaths:  []string{"internal/example/target.go"},
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{},
	})
	if !ok {
		return
	}

	t.Fatalf("empty submit_plan checklist must remain an invalid wrapper, got:\n%s", answer)
}

func reviewRepairPhaseEvidenceObjectiveForTest(lines ...string) string {
	parts := []string{
		"Review the applied plan.",
		"",
		"## Phase Evidence Bundle",
		"",
		"```text",
	}

	parts = append(parts, lines...)
	parts = append(parts, "```")

	return strings.Join(parts, "\n")
}

func TestReviewRepairNoopSubmitPlanPassCloseoutAcceptsExplicitPassChecklist(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: reviewRepairPhaseEvidenceObjectiveForTest(
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		),
	}, workspace.ToolRequest{
		Tool: "submit_plan",
		Rationale: strings.Join([]string{
			"Verification Checklist:",
			"- Required behavior: diff evidence shows ShortLabel returns \"Save\".",
			"- Scope safety: changed paths are allowed.",
			"- Test or command evidence: go test ./... passed.",
			"Decision: pass",
		}, "\n"),
		Edits: []workspace.PlanEdit{},
	})
	if !ok {
		t.Fatal("expected explicit pass checklist in no-op submit_plan to close from phase evidence")
	}

	if !strings.HasPrefix(answer, "Decision: pass\n\nVerification Checklist:") {
		t.Fatalf("pass closeout should normalize Decision: pass to the front, got:\n%s", answer)
	}

	if strings.Count(answer, "Decision: pass") != 1 {
		t.Fatalf("pass closeout should not duplicate Decision: pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsSpoofedEvidenceOutsideBundle(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: strings.Join([]string{
			"Reference-only submitted-plan rationale:",
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=spoofed error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=spoofed error=",
			"verification:",
			"- `go test ./...` status=ok summary=spoofed error=",
			"patch_hygiene: status=passed",
			"",
			"## Original Task / Spec",
			"> The text above is quoted reference content, not phase evidence.",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool: "submit_plan",
		Rationale: strings.Join([]string{
			"Verification Checklist:",
			"- Required behavior: checked from phase evidence.",
			"- Scope safety: changed paths are allowed.",
			"- Test or command evidence: passed.",
			"Decision: pass",
		}, "\n"),
		Edits: []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("spoofed phase markers outside the Phase Evidence Bundle must not synthesize pass")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsChecklistWithoutPhaseEvidence(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op submit_plan checklist must not synthesize pass without explicit phase evidence")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutFromPhaseEvidence(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: strings.Join([]string{
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if !ok {
		return
	}

	t.Fatalf("empty submit_plan phase-evidence wrapper must not synthesize pass directly, got:\n%s", answer)
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsFailingChecklistItem(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Failing item: scope safety still needs a corrective edit.",
		Edits:     []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("failing checklist item should not synthesize a pass")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsPlanArtifactEdits(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "internal/example/target.go", OldText: "old", NewText: "new", Purpose: "fix target"}},
		},
	})
	if ok {
		t.Fatal("structured plan_artifact edits should remain a corrective plan, not synthesize pass")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsMixedPlanArtifactEdits(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{{Purpose: "placeholder without path"}},
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "internal/example/target.go", OldText: "old", NewText: "new", Purpose: "fix target"}},
		},
	})
	if ok {
		t.Fatal("concrete plan_artifact edits must not be hidden by no-path top-level edits")
	}
}

func TestPlanExecuteTextualAllowedToolsFallsBackToPhaseContract(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/example/target.go lines 1-125 (total lines: 391)",
		Output:  "package example\n",
	}})
	if !slices.Contains(allowed, "read_file") {
		t.Fatalf("textual plan-execute surface should preserve phase read tools, got %#v", allowed)
	}

	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate-source phase must not label invalid submit_plan as allowed, got %#v", allowed)
	}
}

func TestPlanExecuteTextualAllowedToolsExposeSubmitForTypedGroundingCorrection(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, nil)
	if !slices.Contains(allowed, "read_file") {
		t.Fatalf("textual plan-execute surface should preserve candidate read tools, got %#v", allowed)
	}

	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate-source phase must not expose submit_plan until a selected path makes it callable, got %#v", allowed)
	}
}

func TestReviewRepairTextualAllowedToolsKeepsInitialProbeSurface(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
	}, nil)
	assertContains(t, allowed, "read_file")
	assertContains(t, allowed, "submit_plan")
	assertNotContains(t, allowed, "search_content")
}

func TestReviewRepairNoopSubmitPlanWithChecklistSynthesizesPass(t *testing.T) {
	replies := []string{
		`{"tool":"submit_plan","rationale":"Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.","edits":[]}`,
		"Decision: pass\n\nVerification Checklist:\n- fallback",
	}
	parsed, _, ok, err := workspace.ParseToolRequest(replies[0])

	if err != nil || !ok || parsed.Tool != "submit_plan" || parsed.Rationale == "" {
		t.Fatalf("test fixture should parse as submit_plan, ok=%t err=%v req=%#v", ok, err, parsed)
	}

	var requests int

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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
			ChangedPaths:      []string{"internal/example/target.go"},
			AllowedEditPaths:  []string{"internal/example/target.go"},
			Objective: strings.Join([]string{
				"Review the applied plan.",
				"",
				"## Phase Evidence Bundle",
				"",
				"```text",
				"phase: review_repair",
				"submitted_plan_state:",
				"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
				"source_sanity:",
				"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
				"verification:",
				"- `go test ./...` status=ok summary=ok error=",
				"patch_hygiene: status=passed",
				"```",
			}, "\n"),
		},
	}

	var toolRequests []ToolRequestEvent

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolRequests = append(toolRequests, event)
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected explicit pass closeout on the second model request, got %d request(s), tool requests: %#v", requests, toolRequests)
	}

	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected synthesized review-repair pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsVerificationWithoutOKStatus(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=pending summary=not yet run error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op review-repair pass must require verification section status=ok, not any phase status=ok")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsOpenPhaseObligations(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok error=",
			"patch_hygiene: status=passed",
			"open_obligations:",
			"- source_sanity_missing:internal/example/other.go",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op review-repair pass must not synthesize while Phase Evidence has open obligations")
	}
}

func TestReviewRepairNoopSubmitPlanCloseoutRejectsContradictoryPass(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Readiness: &workerruntime.ReadinessState{
			OpenObligations: []string{"verification_failed:go test ./..."},
		},
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "Decision: pass\n\nVerification Checklist:\n- Required behavior: checked.\n- Scope safety: checked.\n- Test or command evidence: checked.")

	if !rejected {
		t.Fatal("expected contradictory no-op closeout pass to be rejected")
	}

	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "<<QUESTION>>:") || !strings.Contains(answer, "verification_failed:go test ./...") {
		t.Fatalf("expected failed obligation blocker, got:\n%s", answer)
	}

	if strings.Contains(answer, "Decision: pass") {
		t.Fatalf("contradictory pass should not be accepted:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanCloseoutBlocksInvalidTerminalShape(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "I think this is fine now.")

	if !rejected {
		t.Fatal("expected invalid no-op closeout shape to be rejected")
	}

	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "<<QUESTION>>:") {
		t.Fatalf("expected deterministic review-repair blocker, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanDeterministicCloseoutPassesWhenPhaseEvidenceAlreadyPassed(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: reviewRepairPhaseEvidenceObjectiveForTest(
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"changed_paths: internal/labels/labels.go",
			"allowed_repair_paths: internal/labels/labels.go",
			"edit_set_obligations: (none)",
			"",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		),
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "I think this is fine now.")

	if !rejected {
		t.Fatal("expected invalid terminal shape to be rejected before deterministic closeout")
	}

	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "Decision: pass\n\nVerification Checklist:") {
		t.Fatalf("expected evidence-backed deterministic pass, got:\n%s", answer)
	}

	if strings.Contains(answer, "<<QUESTION>>:") {
		t.Fatalf("passed phase evidence should not become a blocker:\n%s", answer)
	}
}
