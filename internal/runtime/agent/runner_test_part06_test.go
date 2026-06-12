package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestWriteReadyPacketRejectsReadOutsideRequiredPathsAfterFirstWrite(t *testing.T) {
	// The relaxation only widens the read scope to the brief's full
	// required_edit_paths set. Reads on paths outside that set must still be
	// rejected — broad codebase exploration is the actual concern.
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt", "unrelated.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"unrelated.txt","start_line":1,"end_line":1,"named_gap":"peek at unrelated.txt","why_not_answering":"curious"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
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

	var rejectedReads int

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt", "b.txt"},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error != "" {
				rejectedReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rejectedReads == 0 {
		t.Fatalf("expected the read on unrelated.txt (outside required_edit_paths) to be rejected even with named_gap")
	}
}

func TestWriteReadyPacketAllowsReadOnExplicitEvidencePath(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt", "evidence.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"evidence.txt","start_line":1,"end_line":1,"named_gap":"verify the explicitly supplied evidence line before updating b.txt","why_not_answering":"b.txt depends on the supplied read-only evidence"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
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

	var (
		rejectedReads   int
		successfulReads int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"evidence.txt"},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt using evidence.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				if event.Result.Error == "" {
					successfulReads++
				} else {
					rejectedReads++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rejectedReads != 0 {
		t.Fatalf("expected explicit evidence-path read to pass, got %d rejected reads", rejectedReads)
	}

	if successfulReads != 1 {
		t.Fatalf("expected one successful evidence-path read, got %d", successfulReads)
	}
}

func TestScopedImplementationAllowsPackageDirectorySemanticDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/workflows/productworkflow/coverage_gate.go", "package productworkflow\n\nfunc EvaluateCoverageGate() bool { return ValidateADRSupersessionLinks() }\n"); err != nil {
		t.Fatalf("write coverage gate: %v", err)
	}

	if err := writeTestFile(root, "internal/workflows/productworkflow/adr_supersession.go", "package productworkflow\n\nfunc ValidateADRSupersessionLinks() bool { return true }\n"); err != nil {
		t.Fatalf("write supersession validator: %v", err)
	}

	replies := []string{
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/workflows/productworkflow","pattern":"ValidateADRSupersessionLinks","named_gap":"body of ValidateADRSupersessionLinks before wiring coverage gate","why_not_answering":"the implementation depends on the existing canonical validator"}`,
		`{"tool":"write_file","path":"internal/workflows/productworkflow/coverage_gate.go","content":"package productworkflow\n\nfunc EvaluateCoverageGate() bool { return ValidateADRSupersessionLinks() }\n","description":"wire canonical supersession validator"}`,
		"Changed paths:\n- internal/workflows/productworkflow/coverage_gate.go\nVerification: not required for this test fixture.",
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

	var (
		scopedReadRejections     int
		semanticDiscoveryResults int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "wire coverage gate to canonical ADR supersession validators",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"internal/workflows/productworkflow/coverage_gate.go"},
			EvidencePaths:        []string{"internal/workflows/productworkflow/coverage_gate.go"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "wire coverage gate to canonical ADR supersession validators"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "code_navigate" {
				if event.Result.Summary == "Scoped implementation read rejected" {
					scopedReadRejections++
				}

				if event.Result.Error == "" {
					semanticDiscoveryResults++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if scopedReadRejections != 0 {
		t.Fatalf("package-directory semantic discovery should not be scoped-read rejected, got %d rejection(s)", scopedReadRejections)
	}

	if semanticDiscoveryResults != 1 {
		t.Fatalf("expected one successful semantic discovery result, got %d", semanticDiscoveryResults)
	}

	if !strings.Contains(answer, "internal/workflows/productworkflow/coverage_gate.go") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestRunnerForcesSynthesisOnFirstPostReadinessNoGapFollowup(t *testing.T) {
	root := t.TempDir()
	source := `package agent

type Runner struct{}

func (r *Runner) runChildTask() string {
	child := "child"
	return child
}
`

	if err := writeTestFile(root, "internal/runtime/agent/runner_child_tasks.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"go_read_symbol","path":"internal/runtime/agent/runner_child_tasks.go","pattern":"Runner.runChildTask"}`,
		`{"tool":"go_read_symbol","path":"internal/runtime/agent/runner_child_tasks.go","pattern":"Runner.runWorkbranchChildTask"}`,
		"`Runner.runChildTask` creates the child runner path in `internal/runtime/agent/runner_child_tasks.go` and returns its result.",
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

	var roles []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how `Runner.runChildTask` works.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"go_read_symbol"},
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how `Runner.runChildTask` works."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "runChildTask") {
		t.Fatalf("expected forced synthesis answer, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected first no-gap follow-up to force synthesis, used %d/%d replies", requests, len(replies))
	}

	if slices.Contains(roles, "tool_rejected") {
		t.Fatalf("did not expect a rejected-tool turn before forced synthesis, got roles %v", roles)
	}
}

func TestRunnerAllowsPostReadinessSourceToolWithoutExactProbeContract(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner_child_tasks.go", "package agent\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"go_read_symbol","path":"internal/runtime/agent/runner_child_tasks.go","pattern":"runChildTask"}`,
		"runChildTask runs as the child-task entry point inspected in `internal/runtime/agent/runner_child_tasks.go`; in this fixture, the function returns no additional behavior because its body is empty.",
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

	var (
		events      []RunEvent
		toolResults []workspace.ToolResult
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how `Runner.runChildTask` works.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"go_read_symbol"},
			Constraints:       []string{"answer_readiness_reached"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how `Runner.runChildTask` works."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			toolResults = append(toolResults, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "runChildTask") {
		t.Fatalf("expected final answer after honoring follow-up read, got %q; events=%s", answer, formatRunEventsForTest(events))
	}

	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("read-only readiness is advisory; expected no post-readiness terminal gate, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	successfulProbe := false

	for _, result := range toolResults {
		if result.Tool == "go_read_symbol" && strings.TrimSpace(result.Error) == "" {
			successfulProbe = true
		}
	}

	if !successfulProbe {
		t.Fatalf("gapless post-readiness source probe should dispatch under advisory readiness, got results=%#v events=%s", toolResults, formatRunEventsForTest(events))
	}
}

func TestRunnerPostReadinessRepeatedReadDoesNotForceHostCloseout(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		"`ShortLabel` returns `Save changes` in `internal/labels/labels.go`.",
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

	var events []RunEvent

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Can we shorten the save label in internal/labels/labels.go? Answer from the codebase.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can we shorten the save label in internal/labels/labels.go? Answer from the codebase."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 3 {
		t.Fatalf("expected repeated read then model closeout, used %d replies", requests)
	}

	if !strings.Contains(answer, "Save changes") || !strings.Contains(answer, "internal/labels/labels.go") {
		t.Fatalf("expected model answer from gathered evidence, got %q", answer)
	}

	if got := countEventsByRole(events, "failure_feedback"); got != 0 {
		t.Fatalf("advisory readiness should not reject repeated reads, got %d failure feedback event(s); events=%s", got, formatRunEventsForTest(events))
	}

	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("advisory readiness should not emit post-readiness terminal gate, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	for _, event := range events {
		if event.Role == "budget_ledger" {
			if !strings.Contains(event.Content, `"rejected_actions":0`) || !strings.Contains(event.Content, `"stop_reason":"model_final"`) {
				t.Fatalf("post-readiness repeated read should remain advisory before model final: %s", event.Content)
			}

			return
		}
	}

	t.Fatalf("expected budget_ledger event; events=%s", formatRunEventsForTest(events))
}

func TestRunnerReadOnlyReviewClosesAfterReplayedSourceEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\n\nfunc applyLanePreflight() {}\n"); err != nil {
		t.Fatalf("write runner seed: %v", err)
	}

	if err := writeTestFile(root, "internal/runtime/agent/final_answer_decorator.go", "package agent\n\nfunc composedFinalAnswerFromEvidence() {}\n"); err != nil {
		t.Fatalf("write decorator seed: %v", err)
	}

	replies := []string{
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/runtime/agent/runner.go","pattern":"applyLanePreflight"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/runtime/agent/final_answer_decorator.go","pattern":"composedFinalAnswerFromEvidence"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/runtime/agent/final_answer_decorator.go","pattern":"composedFinalAnswerFromEvidence"}`,
		"Review finding: the inspected runner and final-answer decorator paths already establish the replay-loop risk; confidence is high, with no writes made.",
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

	var events []RunEvent

	executed := 0
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilySecurityReview),
			Contract:          string(workerruntime.ContractReviewFindings),
			Objective:         "Audit runner recovery and final answer contracts for repeated source-probe drift.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"code_navigate"},
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool != "code_navigate" {
				return nil
			}

			executed++

			return &workspace.ToolResult{
				Tool:    "code_navigate",
				Summary: "Code navigation routed read_symbol via go_read_symbol",
				Output: strings.Join([]string{
					"capability: read_symbol",
					"routed_tool: go_read_symbol",
					"route_summary: Read " + req.Pattern + " in " + req.Path + " lines 1-12",
					"1:func " + req.Pattern + "() {",
					"2:\t// runner recovery and final answer contracts are inspected here",
					"3:}",
				}, "\n"),
			}
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "No writes. Audit runner recovery and final answer contracts for repeated source-probe drift."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.TrimSpace(answer) == "" {
		t.Fatal("expected model-owned review answer after replayed evidence")
	}

	if executed != 2 {
		t.Fatalf("replayed request should not execute a third code_navigate call, executed=%d", executed)
	}

	if requests != 4 {
		t.Fatalf("expected three tool-decision calls and one model final answer call, got %d", requests)
	}

	for _, event := range events {
		if event.Role != "budget_ledger" {
			continue
		}

		var ledger runnerBudgetLedger
		if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
			t.Fatalf("decode budget ledger %q: %v", event.Content, err)
		}

		if ledger.StopReason == "read_only_replayed_evidence_closeout" ||
			ledger.StopReason == "host_read_only_closeout" ||
			ledger.StopReason == "read_only_prefetch_host_closeout" {

			t.Fatalf("replayed evidence must not use read-only closeout stop reason: %#v", ledger)
		}

		if ledger.StopReason != "" {
			return
		}
	}

	t.Fatalf("expected budget ledger after replayed evidence; events=%s", formatRunEventsForTest(events))
}

func TestRunnerAnswersAfterRepeatedPostReadinessNoGapFollowupsWhenEvidenceSufficient(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package agent",
		"",
		"type Runner struct{}",
		"",
		"func (r *Runner) Run() {}",
		"",
		"func (r *Runner) finish() {}",
	}, "\n")

	if err := writeTestFile(root, "internal/runtime/agent/runner.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":21,"end_line":40}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":41,"end_line":60}`,
		"`Runner.Run` is defined in `internal/runtime/agent/runner.go`; the inspected source shows the runner struct and run/finish functions used by the control flow.",
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

	var (
		rejected   int
		budgetStop bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain internal/runtime/agent/runner.go Runner.Run.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" && strings.Contains(event.Content, `"rejected_actions":2`) {
				rejected++
			}

			if event.Role == "budget_ledger" && strings.Contains(event.Content, "post_readiness_gap_required_budget_exhausted") {
				budgetStop = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain internal/runtime/agent/runner.go Runner.Run."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "internal/runtime/agent/runner.go") {
		t.Fatalf("answer = %q", answer)
	}

	if !strings.Contains(answer, "Runner.Run") {
		t.Fatalf("expected no-tools model synthesis answer, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow scripted follow-up reads through final answer, used %d/%d replies", requests, len(replies))
	}

	if rejected != 0 || budgetStop {
		t.Fatalf("advisory readiness should not reject unscoped follow-ups: rejected=%d budgetStop=%v", rejected, budgetStop)
	}
}

func TestRunnerRepairsFinalAnswerAfterNoGapReadFileRanges(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package labels",
		"",
		"func ShortLabel() string {",
		"\treturn \"Save changes\"",
		"}",
	}, "\n")

	if err := writeTestFile(root, "internal/labels/labels.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":4,"named_gap":"inspect ShortLabel source","why_not_answering":"need source evidence before answering"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":5,"end_line":8}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":9,"end_line":12}`,
		"BAD_FALLBACK_NOT_USED: the model was asked for another answer after repeated no-gap reads.",
		"For `Explain what ShortLabel returns from internal/labels/labels.go.`, `internal/labels/labels.go` shows `ShortLabel` returns \"Save changes\".",
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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain what ShortLabel returns from internal/labels/labels.go.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain what ShortLabel returns from internal/labels/labels.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "Save changes"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("bad scripted final answer should be repaired into supported answer containing %q, got:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "BAD_FALLBACK_NOT_USED") {
		t.Fatalf("bad scripted final answer surfaced:\n%s", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow repeated no-gap ranges through repaired answer, used %d/%d requests", requests, len(replies))
	}
}

func TestRunnerAnswersAfterRepeatedInvalidPostReadinessFollowupsWhenEvidenceSufficient(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 220)

	lines[0] = "package agent"
	lines[1] = "func (r *Runner) Run() {}"

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			continue
		}

		lines[i] = fmt.Sprintf("// line %d", i+1)
	}

	if err := writeTestFile(root, "internal/runtime/agent/runner.go", strings.Join(lines, "\n")); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":180,"named_gap":"Runner.Run branch in internal/runtime/agent/runner.go","why_not_answering":"need a broad source refresh"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":180,"named_gap":"Runner.Run branch in internal/runtime/agent/runner.go","why_not_answering":"need a broad source refresh"}`,
		"`Runner.Run` is visible in `internal/runtime/agent/runner.go`; the inspected source anchors the harness control flow in that file.",
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

	var (
		rejected   int
		budgetStop bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain the harness control flow.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" && strings.Contains(event.Content, `"rejected_actions":2`) {
				rejected++
			}

			if event.Role == "budget_ledger" && strings.Contains(event.Content, "post_readiness_gap_invalid_budget_exhausted") {
				budgetStop = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain the harness control flow."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "internal/runtime/agent/runner.go") {
		t.Fatalf("answer = %q", answer)
	}

	if !strings.Contains(answer, "Runner.Run") {
		t.Fatalf("expected no-tools model synthesis answer, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow scripted follow-ups through final answer, used %d/%d replies", requests, len(replies))
	}

	if rejected != 0 || budgetStop {
		t.Fatalf("advisory readiness should not reject invalid post-readiness follow-ups: rejected=%d budgetStop=%v", rejected, budgetStop)
	}
}

func TestRunnerRequiresVerificationAfterRepeatedWrites(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "one\ntwo\nthree\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"README.md","start_line":1,"end_line":1,"content":"ONE\n","description":"update first line"}`,
		`{"tool":"edit_file","path":"README.md","start_line":2,"end_line":2,"content":"TWO\n","description":"update second line"}`,
		`{"tool":"read_file","path":"README.md"}`,
		`{"tool":"shell","command":"go test ./...","workdir":"."}`,
		`{"tool":"read_file","path":"README.md"}`,
		"Verified after edits.",
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

	var (
		sawVerificationGate     bool
		sawPostVerificationRead bool
		resultSummaries         []string
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			EnableWrites:          true,
			CommandTimeoutSeconds: 1,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit two lines and verify"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			resultSummaries = append(resultSummaries, event.Result.Summary+"|"+event.Result.Error)
			if event.Result.Summary == "Verification required after workspace edits" {
				sawVerificationGate = true
			}

			if event.Request.Tool == "read_file" && strings.HasPrefix(event.Result.Summary, "Read ") {
				sawPostVerificationRead = sawVerificationGate
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !sawVerificationGate {
		t.Fatalf("expected non-verification read to be rejected after repeated writes; results=%v", resultSummaries)
	}

	if !sawPostVerificationRead {
		t.Fatalf("expected same read to be allowed after verification attempt; results=%v", resultSummaries)
	}

	if answer != "Verified after edits." {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestRunnerRejectsPostVerificationDriftInVerificationOnlyLane(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{PermissionProfile: "read_only_verification_shell"},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request:               workspace.ToolRequest{Tool: "read_file", Path: "main.go"},
		VerificationAttempted: true,
	})

	if decision.RejectSummary != "Verification-only contract rejected post-verification drift" {
		t.Fatalf("expected post-verification drift rejection, got %#v", decision)
	}
}

func TestApplyLanePreflightRedirectsVerificationOnlyToBootstrapCommand(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
		Raw:     `{"tool":"search_files","pattern":"*.go"}`,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected redirect instead of rejection, got %#v", decision)
	}

	if decision.Request.Tool != "shell" || decision.Request.Command != "go test ./..." || decision.Request.Workdir != "." {
		t.Fatalf("expected bootstrap verification command, got %#v", decision.Request)
	}

	if !strings.Contains(decision.Raw, `"tool":"shell"`) {
		t.Fatalf("expected raw request to be rewritten for redirected verification command, got %q", decision.Raw)
	}
}

func TestApplyLanePreflightRejectsBlindDebugLogRereadWithoutForcedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{
		{
			Tool:    "search_files",
			Summary: "Found 1 matching log-like file(s), newest first.",
			Output:  "f1 logs/conversations/recent.jsonl (modified 2026-04-24T10:00:00-04:00; size 2.1 KB)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
			Output:  "{\"type\":\"tool_result\"}\n",
		},
	}

	runner := &Runner{RuntimeBrief: brief}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", ID: "f1", Path: "logs/conversations/recent.jsonl"},
		Raw:     `{"tool":"read_file","id":"f1"}`,
		Results: results,
	})

	if decision.RejectSummary != "Blind log reread rejected" {
		t.Fatalf("expected blind-reread rejection, got %#v", decision)
	}

	if decision.ForceAnswer != nil {
		t.Fatalf("blind reread must not force answer strategy, got %#v", decision.ForceAnswer)
	}

	if !strings.Contains(decision.RejectError, "already have") ||
		!strings.Contains(decision.RejectError, "A final answer is available if") {

		t.Fatalf("expected blind-reread rejection to explain repeated evidence, got %#v", decision)
	}
}

func TestApplyLanePreflightForcesAnswerAfterConversationEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily: "conversation",
		Contract:   "direct_answer",
	}}
	results := []workspace.ToolResult{
		{Tool: "search_content", Summary: "Found matching route planner lines."},
		{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/turn_planner.go."},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/prompt_routing.go"},
		Results: results,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected tool to pass through preflight (no reject), got %#v", decision)
	}

	if decision.ForceAnswer != nil {
		t.Fatalf("expected no force answer on preflight (nudge fires post-result instead), got %#v", decision.ForceAnswer)
	}
}

func TestConversationEvidenceAvailableForObjectiveUsesGatheredSource(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
		},
	}

	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("expected evidence availability after bounded conversation evidence")
	}
}
