package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestCorrectedNamedGapRetryIsAllowedOnce(t *testing.T) {
	replies := []string{
		`{"tool":"discovery_query","purpose":"architecture_boundary","path":"internal/runtime/agent","pattern":"task|delegate"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","named_gap":"inspect the exact branch that launches the child task","why_not_answering":"the final answer depends on the exact child task branch"}`,
		"answer from the repaired follow-up",
	}

	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	serverRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serverRequests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", serverRequests)
		}

		reply := replies[serverRequests]
		serverRequests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 8},
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain sub-agent usage.",
		},
	}

	var events []RunEvent

	runner.OnEvent = func(event RunEvent) {
		events = append(events, event)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain sub-agent usage."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "answer from the repaired follow-up" {
		t.Fatalf("unexpected answer %q", answer)
	}

	// After the discovery_query fix: readiness no longer fires after a
	// lone discovery_query. The model must read at least one source body
	// before readiness declares. Both read_file calls should succeed.
	foundMissingGapRejection := false

	for _, ev := range events {
		if strings.Contains(ev.Content, "Post-readiness follow-up missing named gap") {
			foundMissingGapRejection = true
		}
	}

	if foundMissingGapRejection {
		t.Fatalf("discovery_query alone should not trigger readiness; read_file should not be rejected for missing named gap, roles=%v", eventRolesFor(events))
	}

	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("expected no post-readiness terminal gate decision, got %d; roles=%v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "post_readiness_gap_declared"); got != 0 {
		t.Fatalf("expected no declared post-readiness gap requirement, got %d; roles=%v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("expected no rejection_retry_blocked, got %d; roles=%v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "readiness_check"); got < 1 {
		t.Fatalf("expected at least one readiness check, got %d; roles=%v", got, eventRolesFor(events))
	}
}

func eventRolesFor(events []RunEvent) []string {
	out := make([]string, 0, len(events))
	seen := make(map[string]bool)

	for _, ev := range events {
		if !seen[ev.Role] {
			seen[ev.Role] = true
			out = append(out, ev.Role)
		}
	}

	return out
}

func formatRunEventsForTest(events []RunEvent) string {
	lines := make([]string, 0, len(events))
	for _, ev := range events {
		content := strings.TrimSpace(ev.Content)
		if len(content) > 240 {
			content = content[:240] + "..."
		}

		if content == "" {
			lines = append(lines, ev.Role)
			continue
		}

		lines = append(lines, ev.Role+": "+content)
	}

	return strings.Join(lines, "\n")
}

// TestFingerprintNudgeFiresOncePerFingerprint verifies that when the model
// calls the same tool+args three times *non-consecutively*, the runner
// emits a fingerprint_nudge exactly once — and does NOT trigger the
// DoomLoopGate (which requires consecutive triples).
func TestFingerprintNudgeFiresOncePerFingerprint(t *testing.T) {
	// Alternate A/B/A/B/A so the identical-call run never reaches 3.
	replies := []string{
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":1,"end_line":20}`,
		"done",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8, FingerprintNudgeAt: 3}
	events, _ := runNudgeScenario(t, policy, replies)

	if got := countEventsByRole(events, "fingerprint_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 fingerprint_nudge, got %d", got)
	}

	if got := countEventsByRole(events, "doom_loop_guard"); got != 0 {
		t.Fatalf("DoomLoopGate should NOT fire on non-consecutive repeats, got %d", got)
	}
}

func TestConversationSamePathFixationNudgesOnVariedRanges(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":1,"end_line":50}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":51,"end_line":100}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":101,"end_line":150}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":151,"end_line":200}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":201,"end_line":250}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go","start_line":251,"end_line":300}`,
		"The input handling path is internal/surfaces/tui/app/app.go.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	var events []RunEvent

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 12},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Where is input handling?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "The input handling path is internal/surfaces/tui/app/app.go." {
		t.Fatalf("unexpected answer after same-path nudge: %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected nudge to send the model back for final answer, used %d/%d replies", requests, len(replies))
	}

	if got := countEventsByRole(events, "same_path_fixation_nudge"); got != 1 {
		t.Fatalf("expected one same_path_fixation_nudge, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	if got := countEventsByRole(events, "same_path_fixation_tool_hidden"); got != 1 {
		t.Fatalf("expected one same_path_fixation_tool_hidden, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	if got := countEventsByRole(events, "tool_hidden"); got != 1 {
		t.Fatalf("expected read_file to be hidden after ignored same-path nudge, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	if got := countEventsByRole(events, "doom_loop_guard"); got != 0 {
		t.Fatalf("varied ranges in conversation should not trip doom loop guard, got %d; events=%s", got, formatRunEventsForTest(events))
	}
}

// TestSynthesisNudgeFiresOnceAtThreshold verifies that once the tool-call
// count crosses SynthesisNudgeAt, the runner emits exactly one
// synthesis_nudge for the whole turn.
func TestSynthesisNudgeFiresOnceAtThreshold(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/surfaces/tui/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/runtime/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		`{"tool":"list_files","path":"internal","recursive":false}`,
		"final answer",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8, SynthesisNudgeAt: 3}
	events, _ := runNudgeScenario(t, policy, replies)

	if got := countEventsByRole(events, "synthesis_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 synthesis_nudge, got %d", got)
	}
}

func TestSynthesisNudgeDoesNotFireForPlanExecutePlanner(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package a\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":1}`,
		"Decision: source-satisfied\n- a.go: already implemented; no changes needed.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	var events []RunEvent

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 6, SynthesisNudgeAt: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan execute nudge scenario"}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "synthesis_nudge"); got != 0 {
		t.Fatalf("plan-execute planner should rely on typed source/readiness nudges, got %d generic synthesis nudge(s)", got)
	}
}

// TestNudgesDoNotFireWhenDisabled verifies that zero-valued thresholds
// keep the nudges dormant — the feature is opt-in per mode.
func TestNudgesDoNotFireWhenDisabled(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/surfaces/tui/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/runtime/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		`{"tool":"list_files","path":"internal","recursive":false}`,
		"done",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8}
	events, _ := runNudgeScenario(t, policy, replies)

	for _, role := range []string{"fingerprint_nudge", "synthesis_nudge"} {
		if got := countEventsByRole(events, role); got != 0 {
			t.Fatalf("expected no %s events when disabled, got %d", role, got)
		}
	}
}

// TestModePolicyNudgeDefaults documents the ModePolicy defaults so future
// edits to the numbers are an explicit, reviewed change.
func TestModePolicyNudgeDefaults(t *testing.T) {
	plan := ModePolicy(PlanMode())
	if plan.FingerprintNudgeAt != 3 {
		t.Fatalf("plan FingerprintNudgeAt: want 3, got %d", plan.FingerprintNudgeAt)
	}

	if plan.SynthesisNudgeAt != 10 {
		t.Fatalf("plan SynthesisNudgeAt: want 10, got %d", plan.SynthesisNudgeAt)
	}

	build := ModePolicy(BuildMode())
	if build.FingerprintNudgeAt != 3 || build.SynthesisNudgeAt != 10 {
		t.Fatalf("build nudge thresholds: want (3, 10), got (%d, %d)", build.FingerprintNudgeAt, build.SynthesisNudgeAt)
	}
}

// runRedirectScenario stands up a tiny Go workspace (go.mod + one .go file)
// so the language detector activates, then drives a canned transcript.
func runRedirectScenario(t *testing.T, replies []string) (events []RunEvent, requests int) {
	t.Helper()

	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// Pad past the small-file redirect threshold so these redirect
	// scenarios actually exercise the redirect path. Files below the
	// threshold are left to execute directly because the outline detour
	// isn't worth its cost on tiny files.
	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {}\n"+strings.Repeat("\n", 500)); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "redirect scenario"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return events, requests
}

// TestDiscoveryRedirectOnReadFileCodePath asserts that a read_file on a
// Go source file (no line range) is rewritten to code_navigate file_outline
// so the turn gets executable source evidence instead of control guidance.
func TestDiscoveryRedirectOnReadFileCodePath(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		"final answer",
	}
	events, _ := runRedirectScenario(t, replies)

	if got := countEventsByRole(events, "tool_request_rewrite"); got != 1 {
		t.Fatalf("expected exactly 1 tool_request_rewrite event, got %d", got)
	}

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured read rewrite should avoid control redirect events, got %d", got)
	}
}

// TestDiscoveryRedirectFallsBackOnSecondCall asserts repeated read-only
// source reads stay on the executable structured route instead of falling
// back to control-only redirect prose.
func TestDiscoveryRedirectFallsBackOnSecondCall(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)

	if got := countEventsByRole(events, "tool_request_rewrite"); got != 3 {
		t.Fatalf("expected repeated read to rewrite through outline then symbol, got %d", got)
	}

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured read rewrite should avoid control redirect events, got %d", got)
	}
}

func TestDiscoveryRedirectSkipsScopedImplementationRequiredPathRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	source := "package main\n\nfunc main() {}\n" + strings.Repeat("// pad\n", 500)
	if err := writeTestFile(root, "main.go", source); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"write_file","path":"main.go","content":"package main\n\nfunc main() { println(\"ok\") }\n","description":"update required path"}`,
		"Changed paths:\n- main.go\nVerification: not required for this test fixture.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var (
		events          []RunEvent
		readFileResults int
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update main.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" && strings.Contains(event.Result.Output, "func main()") {
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("required-path source reads should not redirect in scoped implementation, got %d", got)
	}

	if readFileResults != 1 {
		t.Fatalf("expected direct read_file result for required path, got %d", readFileResults)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationLargeRequiredPathReadReturnsBoundedSourcePacket(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	lines := make([]string, 1200)

	lines[0] = "package main"
	lines[1] = ""

	for i := 2; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}

	lines[909] = "func ProductWorkflowCoverageGate() bool { return true }"

	source := strings.Join(lines, "\n")

	if err := writeTestFile(root, "main.go", source); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go","start_line":900,"end_line":920}`,
		`{"tool":"edit_file","path":"main.go","old_text":"func ProductWorkflowCoverageGate() bool { return true }","new_text":"func ProductWorkflowCoverageGate() bool { return false }","description":"update required path"}`,
		"Changed paths:\n- main.go\nVerification: not required for this test fixture.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var (
		events      []RunEvent
		readResults []workspace.ToolResult
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "update ProductWorkflowCoverageGate behavior",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update ProductWorkflowCoverageGate behavior"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				readResults = append(readResults, event.Result)
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("large required-path source read should not use discovery redirect, got %d", got)
	}

	if got := countEventsByRole(events, "scoped_bounded_source_read"); got != 1 {
		t.Fatalf("expected one bounded source event, got %d", got)
	}

	if len(readResults) < 2 {
		t.Fatalf("expected bounded read and explicit range read, got %d", len(readResults))
	}

	first := readResults[0]
	if !strings.Contains(first.Summary, "bounded large required-path source") {
		t.Fatalf("expected bounded source summary, got %q", first.Summary)
	}

	if strings.Contains(first.Output, "1200:// filler 1200") {
		t.Fatalf("bounded read leaked tail of large file:\n%s", first.Output)
	}

	if !strings.Contains(first.Output, "ProductWorkflowCoverageGate") {
		t.Fatalf("bounded read should prioritize task-relevant source over generic file header:\n%s", first.Output)
	}

	if !strings.Contains(first.Output, `"start_line"`) || !strings.Contains(first.Output, `"end_line"`) {
		t.Fatalf("bounded read should include concrete follow-up range guidance:\n%s", first.Output)
	}

	if !strings.Contains(readResults[1].Summary, "lines 900-920") {
		t.Fatalf("explicit bounded range should execute normally, summary=%q", readResults[1].Summary)
	}

	if !runner.Workspace.HasObservedPath("main.go") {
		t.Fatalf("bounded source packet should mark the path observed for subsequent edits")
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationRejectsLargeExistingWriteFileReplacement(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 1200)

	lines[0] = "package main"

	for i := 1; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}

	original := strings.Join(lines, "\n")
	if err := writeTestFile(root, "main.go", original); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}

	intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "main.go",
		Content:     "package main\n\nfunc main() {}\n",
		Description: "replace large file",
	}, nil)
	if !intercept.Intercepted {
		t.Fatalf("expected large existing source replacement to be rejected")
	}

	if !strings.Contains(intercept.Result.Error, "Use apply_patch or edit_file") {
		t.Fatalf("expected bounded edit guidance, got %q", intercept.Result.Error)
	}

	got, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	if string(got) != original {
		t.Fatalf("preflight must not mutate large file")
	}
}

func TestScopedImplementationRejectsLargeWriteFileReplacementInRunnerLoop(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 1200)

	lines[0] = "package main"

	for i := 1; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}

	original := strings.Join(lines, "\n")
	if err := writeTestFile(root, "main.go", original); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"write_file","path":"main.go","content":"package main\n\nfunc main() {}\n","description":"replace large file"}`,
		"Blocked by large write_file guard.",
		"Still blocked by large write_file guard.",
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
		guardEvents     int
		rejectedResults int
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "scoped_large_write_file_guard" {
				guardEvents++
			}
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "replace main.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "write_file" && strings.Contains(event.Result.Error, "write_file would replace existing large scoped source path") {
				rejectedResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if guardEvents != 1 {
		t.Fatalf("expected one scoped_large_write_file_guard event, got %d", guardEvents)
	}

	if rejectedResults != 1 {
		t.Fatalf("expected one rejected write_file result, got %d", rejectedResults)
	}

	got, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	if string(got) != original {
		t.Fatalf("large write_file rejection must leave file unchanged")
	}

	if requests < 2 || requests > len(replies) {
		t.Fatalf("expected runner to stop after recovery without extra requests, got %d request(s)", requests)
	}
}

func TestScopedImplementationLargeWriteFileReplacementGuardAllowsSmallNewAndDocFiles(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "small.go", "package main\n"); err != nil {
		t.Fatalf("write small.go: %v", err)
	}

	largeDoc := strings.Repeat("paragraph\n", scopedImplementationLargeReadLineThreshold+1)
	if err := writeTestFile(root, "notes.md", largeDoc); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"small.go", "missing.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	for _, req := range []workspace.ToolRequest{
		{Tool: "write_file", Path: "small.go", Content: "package main\n\nfunc main() {}\n", Description: "replace small file"},
		{Tool: "write_file", Path: "missing.go", Content: "package main\n", Description: "create new file"},
	} {
		if intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(req, nil); intercept.Intercepted {
			t.Fatalf("expected %s to pass guard, got %q", req.Path, intercept.Result.Error)
		}
	}

	docRunner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"notes.md"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	docReq := workspace.ToolRequest{Tool: "write_file", Path: "notes.md", Content: "# Notes\n", Description: "replace doc deliverable"}

	if intercept := docRunner.preflightScopedImplementationLargeWriteFileReplacement(docReq, nil); intercept.Intercepted {
		t.Fatalf("expected doc deliverable to pass guard, got %q", intercept.Result.Error)
	}
}

func TestScopedImplementationRejectsByteLargeWriteFileReplacementWithoutReadingBody(t *testing.T) {
	root := t.TempDir()
	largeLine := strings.Repeat("x", scopedImplementationLargeReadByteThreshold+1024)

	if err := writeTestFile(root, "main.go", "package main\nconst large = \""+largeLine+"\"\n"); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "main.go",
		Content:     "package main\n",
		Description: "replace byte-large file",
	}, nil)

	if !intercept.Intercepted {
		t.Fatalf("expected byte-large existing source replacement to be rejected")
	}

	if strings.Contains(intercept.Result.Error, "lines") {
		t.Fatalf("byte-large rejection should use stat detail without reading line count, got %q", intercept.Result.Error)
	}
}

func TestScopedImplementationBoundedSourcePacketCapsByteLargeLines(t *testing.T) {
	root := t.TempDir()
	longLine := strings.Repeat("x", scopedImplementationLargeReadByteThreshold+1024)

	if err := writeTestFile(root, "main.go", strings.Join([]string{
		"package main",
		"func ByteLargeTarget() string {",
		longLine,
		"}",
	}, "\n")); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "update ByteLargeTarget behavior",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationPlan:     nil,
			VerificationContract: "not_required_for_text_mutations",
		},
	}

	intercept := runner.preflightScopedImplementationBoundedSourceRead(workspace.ToolRequest{Tool: "read_file", Path: "main.go"}, nil)
	if !intercept.Intercepted {
		t.Fatalf("expected byte-large range-less read to be bounded")
	}

	if !strings.Contains(intercept.Result.Output, "[truncated ") {
		t.Fatalf("expected long source line to be capped, output length=%d", len(intercept.Result.Output))
	}

	if strings.Contains(intercept.Result.Output, longLine) {
		t.Fatalf("bounded source packet leaked the full long line")
	}
}

// TestDiscoveryRedirectSkipsRangedReads ensures a read_file with an
// explicit start_line/end_line is treated as targeted and NOT redirected.
func TestDiscoveryRedirectSkipsRangedReads(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go","start_line":1,"end_line":3}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("ranged reads should not redirect, got %d", got)
	}
}

// TestDiscoveryRedirectSkipsStructuredTool ensures a language-specific
// tool is never redirected to itself or another structured tool.
func TestDiscoveryRedirectSkipsStructuredTool(t *testing.T) {
	replies := []string{
		`{"tool":"go_overview"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)

	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured tools should not redirect, got %d", got)
	}
}

// TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage confirms read-only
// source reads rewrite to executable structured reads for each target without
// injecting language decision-guide prose.
func TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage(t *testing.T) {
	// Two distinct read_file targets should both execute through the
	// structured route in the same step.
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"other.go"}`,
		"final",
	}
	// Ensure the second target exists.
	events, _ := func() ([]RunEvent, int) {
		root := t.TempDir()
		if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
			t.Fatalf("go.mod: %v", err)
		}

		// Pad both files past the small-file redirect threshold so the
		// outline redirect fires for each.
		if err := writeTestFile(root, "main.go", "package main\nfunc main(){}\n"+strings.Repeat("\n", 500)); err != nil {
			t.Fatalf("main.go: %v", err)
		}

		if err := writeTestFile(root, "other.go", "package main\nfunc other(){}\n"+strings.Repeat("\n", 500)); err != nil {
			t.Fatalf("other.go: %v", err)
		}

		requests := 0

		var events []RunEvent

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reply := replies[requests]
			requests++

			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
			})
		}))
		t.Cleanup(server.Close)

		mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
		runner := &Runner{
			Client:    llama.NewClient(server.URL, time.Second),
			Workspace: mgr,
			Policy:    RunPolicy{MaxNoProgressTurns: 8},
			OnEvent:   func(ev RunEvent) { events = append(events, ev) },
		}

		_, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: "multi redirect"}},
			llama.ChatOptions{},
			nil, nil, nil,
		)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		return events, requests
	}()
	if got := countEventsByRole(events, "tool_request_rewrite"); got != 2 {
		t.Fatalf("expected 2 tool_request_rewrite events, got %d", got)
	}

	if got := countEventsByRole(events, "discovery_decision_guide"); got != 0 {
		t.Fatalf("structured read rewrites should not inject decision guides, got %d", got)
	}
}

func TestRunnerClosureGuidanceActiveSuppressesDecisionGuideAfterPostChildClosure(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:    "architecture",
		Objective:     "Explain how this harness uses subagents.",
		Constraints:   []string{"child_packet_available"},
		EvidencePaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
	}
	if !runnerClosureGuidanceActive("main", brief, []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Source-body evidence paths:\n- internal/runtime/agent/runner_child_tasks.go\n\nKey findings:\n- The harness uses subagents by delegating to child runners in the source-body implementation.",
	}}, false, false) {

		t.Fatal("expected durable child-packet closure to suppress discovery decision-guide reinjection")
	}
}

func TestRunnerRejectsDuplicateReadOnlyTaskWithoutForcingAnswer(t *testing.T) {
	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect discovery tool usefulness and summarize evidence."}`,
		`<<QUESTION>>: source-body evidence for discovery tool usefulness was not available from this delegated pass.`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect discovery tool usefulness again."}`,
		`{"tool":"read_file","path":"internal/capabilities/workspace/discovery_query.go","start_line":1,"end_line":80}`,
		`The duplicate child task was rejected, then the direct source read grounded the answer in ` + "`internal/capabilities/workspace/discovery_query.go`" + `. Discovery tool usefulness depends on source-body evidence rather than another delegated search.`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var (
		hiddenTaskResults       int
		childTaskResults        int
		duplicateTaskRejections int
		directReads             int
	)

	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "internal/capabilities/workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace fixture: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal/capabilities/workspace/discovery_query.go"), []byte("package workspace\n\nfunc RunDiscoveryQuery() {}\n"), 0o644); err != nil {
		t.Fatalf("write discovery fixture: %v", err)
	}

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "read_file", "search_content"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool != "task" {
				if event.Result.Tool == "read_file" && strings.TrimSpace(event.Result.Error) == "" {
					directReads++
				}

				return
			}

			if event.Result.Summary == "Child task completed" {
				childTaskResults++
			}

			if event.Result.Summary == "Duplicate child task rejected" {
				duplicateTaskRejections++
			}

			if event.Result.Summary == "Tool hidden by active contract" {
				hiddenTaskResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if childTaskResults != 1 {
		t.Fatalf("expected exactly one child task result, got %d", childTaskResults)
	}

	if hiddenTaskResults != 0 {
		t.Fatalf("duplicate post-child task should use the specific duplicate-task rejection, got %d generic hidden task result(s)", hiddenTaskResults)
	}

	if duplicateTaskRejections != 1 {
		t.Fatalf("expected one duplicate task rejection, got %d", duplicateTaskRejections)
	}

	if directReads != 1 {
		t.Fatalf("expected model to retain bounded read strategy after duplicate task rejection, got %d direct reads", directReads)
	}

	if strings.TrimSpace(answer) == "" || strings.Contains(answer, "hidden for the rest of this turn") {
		t.Fatalf("expected clean answer after direct evidence, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected duplicate task rejection plus direct read path with %d model requests, got %d", len(replies), requests)
	}
}

// TestRecoverySearchStopWordsDoesNotFilterModel verifies that "model"
// is not in the search stop words list — it's domain-significant for
// path→objective matching (e.g., model_popup.go matching /model task).
