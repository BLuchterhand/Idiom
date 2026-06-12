package agent

import (
	"context"
	"encoding/json"
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

func TestRunnerEmitsRecoveryNudgeOnRepeatedSameStemSoftFailures(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/llm/llm.go"}`,
		`{"tool":"read_file","path":"internal/llm/openai.go"}`,
		`{"tool":"read_file","path":"internal/llm/qwen.go"}`,
		"Stopped after recovery nudge.",
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

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 128,
	})

	var nudgeEmitted bool

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "rejection_recovery_nudge" {
				nudgeEmitted = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "find the llm package"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !nudgeEmitted {
		t.Fatal("expected rejection_recovery_nudge after 3 same-stem soft failures")
	}

	if answer != "Stopped after recovery nudge." {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

// TestDoomLoopGateDenyPathStops verifies that when a DoomLoopGate is wired and
// returns false, the runner stops with stop_reason=doom_loop_guard after
// forcing a no-tools closeout from gathered evidence. Proves the gate is
// reachable and its deny path does not return an empty answer.
func TestDoomLoopGateDenyPathStops(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Answered after doom-loop denial.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	gateCalls := 0
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 6},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			gateCalls++

			if count < 3 {
				t.Fatalf("gate invoked with count=%d, expected >=3", count)
			}

			return false
		},
	}

	var stopReason string

	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger

			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "loop on broad listing"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gateCalls != 1 {
		t.Fatalf("expected DoomLoopGate to be called exactly once, got %d", gateCalls)
	}

	if stopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %q", stopReason)
	}

	if answer != "Answered after doom-loop denial." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

// TestDoomLoopGateApprovePathContinues verifies the approve branch: gate
// returns true, the streak is reset, and execution continues past the
// 3-identical-calls threshold. After approval the model emits a non-tool
// answer and the turn completes normally (no doom_loop_guard stop).
func TestDoomLoopGateApprovePathContinues(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Proceeded past approved loop; answering now.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	gateCalls := 0
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 6},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			gateCalls++
			return true
		},
	}

	var stopReason string

	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger

			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "loop on broad listing"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gateCalls < 1 {
		t.Fatalf("expected DoomLoopGate to be called at least once, got %d", gateCalls)
	}

	if stopReason == "doom_loop_guard" {
		t.Fatalf("approve path should NOT stop with doom_loop_guard, got stop_reason=%q", stopReason)
	}

	if answer != "Proceeded past approved loop; answering now." {
		t.Fatalf("unexpected answer after approve path: %q", answer)
	}

	if requests != 4 {
		t.Fatalf("expected 4 model requests (3 tool loops + 1 final answer), got %d", requests)
	}
}

// runNudgeScenario drives a canned model transcript against Runner.Run and
// returns the set of emitted event roles. Used by the nudge tests below to
// assert that soft-budget nudges fire the expected number of times without
// recreating the httptest scaffold each time.
func runNudgeScenario(t *testing.T, policy RunPolicy, replies []string) (events []RunEvent, requests int) {
	t.Helper()

	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("write seed: %v", err)
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
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    policy,
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "nudge scenario"}},
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

func countEventsByRole(events []RunEvent, role string) int {
	n := 0

	for _, ev := range events {
		if ev.Role == role {
			n++
		}
	}

	return n
}

// TestToolFailureStreakTerminatesAfterRecoveryNudge verifies the bail
// path: if a path-stem tool-failure streak continues after the soft
// recovery nudge has already fired once, the runner terminates the
// turn instead of letting the model churn indefinitely. Observed in a
// live doc-edit task where the model burned 60+ tool calls retrying
// edit_file with progressively broken arg shapes.
func TestToolFailureStreakTerminatesAfterRecoveryNudge(t *testing.T) {
	// All reads target paths inside the same top-level dir (same path
	// stem) but each path is non-existent, so workspace.Execute fails
	// per call — not a guard rejection, a tool-execution error.
	// Three failures fires the soft recovery nudge. Three more should
	// trip the new hard bail. start_line/end_line are set so the
	// redirect-to-outline doesn't intercept.
	replies := []string{
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_a.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_b.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_c.go","start_line":1,"end_line":2}`,
		// 3rd failure on same stem → softFailureNudge fires, counter resets.
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_d.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_e.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/surfaces/tui/app/missing_f.go","start_line":1,"end_line":2}`,
		// 3rd failure of the 2nd streak → runner should bail with
		// tool_failure_streak_exhausted. The model never gets to emit
		// "final answer".
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 20}, replies)

	seenRecovery := false
	seenBail := false

	for _, ev := range events {
		if ev.Role == "rejection_recovery_nudge" {
			seenRecovery = true
		}

		if strings.Contains(ev.Content, "tool_failure_streak_exhausted") {
			seenBail = true
		}
	}

	if !seenRecovery {
		t.Fatalf("expected recovery nudge to fire on first 3-failure streak, events: %v", eventRolesFor(events))
	}

	// Fallback for bail: check the budget_ledger stop_reason.
	for _, ev := range events {
		if ev.Role == "budget_ledger" && strings.Contains(ev.Content, "tool_failure_streak_exhausted") {
			seenBail = true
			break
		}
	}

	if !seenBail {
		t.Fatalf("expected tool_failure_streak_exhausted stop reason after second streak, events: %v", eventRolesFor(events))
	}
}

// TestZeroHitRetryBlocked verifies that a second search_files call with
// a pattern that returned no matches on the first call is rejected with
// the "Zero-hit retry blocked" guard — the model doesn't get to burn
// another budget slot on a call the search space guarantees will again
// return nothing.
func TestZeroHitRetryBlocked(t *testing.T) {
	replies := []string{
		`{"tool":"search_files","pattern":"nonexistent_xyz_token"}`,
		`{"tool":"search_files","pattern":"nonexistent_xyz_token"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	found := false

	for _, ev := range events {
		if strings.Contains(ev.Content, "Zero-hit retry blocked") {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected zero-hit retry guard to fire, events: %v", eventRolesFor(events))
	}

	assertRecoveryCardEvent(t, events, "evidence.zero_hit_not_absence", "Zero-hit retry blocked")
}

func TestRepeatedDeadEndReadEmitsRecoveryCard(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","start_line":1}`,
		`{"tool":"read_file","end_line":1}`,
		"blocked",
	}
	root := t.TempDir()
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

	var events []RunEvent

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if step == 1 && req.Tool == "read_file" {
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Read rejected",
					Error:   "synthetic read guard",
				}
			}

			return nil
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect files"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertRecoveryCardEvent(t, events, "loop.repeated_dead_end", "Repeated dead-end action rejected")
	assertRecoveryCardRole(t, events, "next_action_state", "loop.repeated_dead_end", "currently callable tools:", "read_file", "currently callable action classes:", "read")
	assertRecoveryCardRole(t, events, "failure_feedback", "loop.repeated_dead_end", "currently callable tools:", "read_file", "currently callable action classes:", "read")
}

func TestRejectedToolResultMessageIncludesRecoveryFeedback(t *testing.T) {
	root := t.TempDir()
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/model_state.go lines 1400-1500",
			Output:  "1400:func handleHistoryPanelKey() {}\n",
		},
	}

	var (
		messages   []llama.ChatMessage
		toolEvents []ToolResultEvent
	)

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain internal/surfaces/tui/app/model_state.go history key handling.",
		},
		currentAllowedTools: []string{"read_file", "search_content", "code_navigate"},
	}
	recorder := runnerTurnRecorder{
		runner:           runner,
		onToolResult:     func(event ToolResultEvent) { toolEvents = append(toolEvents, event) },
		collectedResults: &results,
		turnMessages:     &messages,
	}

	recorder.recordWithOptions(3, workspace.ToolRequest{
		Tool:    "search_content",
		Path:    ".",
		Pattern: "history.*key",
	}, workspace.ToolResult{
		Tool:    "search_content",
		Summary: "Repeated dead-end action rejected",
		Error:   "this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
	}, "tool_rejected", runnerRecordOptions{SetLastToolKey: true})

	if len(messages) != 1 {
		t.Fatalf("expected one model-facing tool result message, got %d", len(messages))
	}

	if len(results) != 2 {
		t.Fatalf("expected raw collected results to include the rejection, got %d", len(results))
	}

	if strings.Contains(results[1].Error, "Recovery feedback") || strings.Contains(results[1].Error, "Recovery card") {
		t.Fatalf("raw collected result must remain undecorated, got:\n%s", results[1].Error)
	}

	if len(toolEvents) != 1 {
		t.Fatalf("expected one raw tool result event, got %d", len(toolEvents))
	}

	if strings.Contains(toolEvents[0].Result.Error, "Recovery feedback") || strings.Contains(toolEvents[0].Result.Error, "Recovery card") {
		t.Fatalf("raw tool result event must remain undecorated, got:\n%s", toolEvents[0].Result.Error)
	}

	content := messages[0].Content
	for _, want := range []string{
		"Recovery feedback",
		"do_not_repeat:",
		"valid_next:",
		"Recovery card",
		"loop.repeated_dead_end",
		"internal/surfaces/tui/app/model_state.go",
		"currently callable tools:",
		"read_file",
		"search_content",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected model-facing rejection to contain %q, got:\n%s", want, content)
		}
	}
}

func TestPlanExecuteAnchorRefreshRecoveryCardAdvertisesDerivedReadTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 1-180 (total lines: 1593)"},
		rejected,
	}

	state := buildRunnerNextActionStateWithAllowedTools("main", 2, "tool_result", workspace.ToolRequest{Tool: "submit_plan"}, rejected, results, false, brief, []string{"submit_plan"})
	if !strings.Contains(state.RecoveryCard, "currently callable tools:") ||
		!strings.Contains(state.RecoveryCard, "read_file") ||
		!strings.Contains(state.RecoveryCard, "code_navigate") {

		t.Fatalf("anchor-refresh recovery card should advertise derived read tools, got:\n%s", state.RecoveryCard)
	}

	if strings.Contains(state.RecoveryCard, "currently callable tools: none") ||
		strings.Contains(state.RecoveryCard, "currently callable tools: submit_plan") {

		t.Fatalf("anchor-refresh recovery card advertised stale callable tools, got:\n%s", state.RecoveryCard)
	}
}

func TestPlanExecuteAnchorRefreshAutonomyRecoveryCardAdvertisesDerivedReadTools(t *testing.T) {
	var events []RunEvent

	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 native/src/simulation/production/production_execution.cpp lines 1-180 (total lines: 1593)"},
		rejected,
	}
	runner := &Runner{
		AgentName:           "main",
		RuntimeBrief:        brief,
		currentAllowedTools: []string{"submit_plan"},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	runner.activeAutonomy = newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, results)

	if runner.activeAutonomy == nil {
		t.Fatal("expected autonomy recorder")
	}

	runner.emitNextActionState(2, "tool_result", workspace.ToolRequest{Tool: "submit_plan"}, rejected, results, false)

	autonomyEvents := decodeRunnerAutonomyEventsForTest(t, events)
	for _, event := range autonomyEvents {
		if event.Lifecycle == nil {
			continue
		}

		for _, surface := range event.Lifecycle.SurfaceSummaries {
			if surface.SurfaceID != "recovery_card" {
				continue
			}

			if !slices.Contains(surface.CallableTools, "read_file") ||
				!slices.Contains(surface.CallableTools, "code_navigate") ||
				slices.Contains(surface.CallableTools, "submit_plan") {

				t.Fatalf("autonomy recovery card callable tools = %#v, want read_file/code_navigate only", surface.CallableTools)
			}

			return
		}
	}

	t.Fatalf("expected autonomy recovery_card surface, events=%#v", autonomyEvents)
}

func assertRecoveryCardEvent(t *testing.T, events []RunEvent, invariantID string, summary string) {
	t.Helper()

	cardsByRole := recoveryCardsByRole(t, events)

	var cards []string

	for _, roleCards := range cardsByRole {
		cards = append(cards, roleCards...)
	}

	for _, card := range cards {
		if strings.Contains(card, invariantID) && strings.Contains(card, summary) {
			assertRecoveryCardOmitsProjectWorkflowTerms(t, card)

			return
		}
	}

	t.Fatalf("expected emitted recovery card with invariant %q and summary %q, cards=%#v roles=%v", invariantID, summary, cards, eventRolesFor(events))
}

func assertRecoveryCardRole(t *testing.T, events []RunEvent, role string, wants ...string) {
	t.Helper()

	cardsByRole := recoveryCardsByRole(t, events)
	for _, card := range cardsByRole[role] {
		matches := true

		for _, want := range wants {
			if !strings.Contains(card, want) {
				matches = false
				break
			}
		}

		if matches {
			assertRecoveryCardOmitsProjectWorkflowTerms(t, card)

			return
		}
	}

	t.Fatalf("expected %s recovery card containing %#v, cards=%#v roles=%v", role, wants, cardsByRole[role], eventRolesFor(events))
}

func recoveryCardsByRole(t *testing.T, events []RunEvent) map[string][]string {
	t.Helper()

	cards := make(map[string][]string)

	for _, event := range events {
		switch event.Role {
		case "next_action_state":
			var state runnerNextActionState

			if err := json.Unmarshal([]byte(event.Content), &state); err == nil && strings.TrimSpace(state.RecoveryCard) != "" {
				cards[event.Role] = append(cards[event.Role], state.RecoveryCard)
			}
		case "failure_feedback":
			var packet runnerFailureFeedbackPacket

			if err := json.Unmarshal([]byte(event.Content), &packet); err == nil && strings.TrimSpace(packet.RecoveryCard) != "" {
				cards[event.Role] = append(cards[event.Role], packet.RecoveryCard)
			}
		}
	}

	return cards
}

func TestSuccessfulSearchAllowsBroadFollowupSearch(t *testing.T) {
	replies := []string{
		`{"tool":"search_files","path":"internal","pattern":"*runner*"}`,
		`{"tool":"search_files","path":".","pattern":"**/*.go"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	for _, ev := range events {
		if strings.Contains(ev.Content, "Chained search rejected") {
			t.Fatalf("follow-up search should not be rejected after a successful search hit; exact-repeat guards own loop prevention; events=%v", eventRolesFor(events))
		}
	}
}

func TestSuccessfulSearchAllowsConcreteFollowupSearch(t *testing.T) {
	replies := []string{
		`{"tool":"search_content","path":".","pattern":"package app"}`,
		`{"tool":"search_files","path":".","pattern":"**/input*.go"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	for _, ev := range events {
		if strings.Contains(ev.Content, "Chained search rejected") {
			t.Fatalf("concrete follow-up search should not be rejected after a successful search hit; events=%v", eventRolesFor(events))
		}
	}
}

func TestSuccessfulSearchCanContinueAfterConcreteInspection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/surfaces/tui/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	if err := writeTestFile(root, "internal/runtime/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	replies := []string{
		`{"tool":"search_files","path":"internal","pattern":"*runner*"}`,
		`{"tool":"search_files","path":"internal","pattern":"*task*"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go","start_line":1,"end_line":1}`,
		`{"tool":"search_files","path":"internal","pattern":"*app*"}`,
		"final answer",
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	var (
		events  []RunEvent
		results []workspace.ToolResult
	)

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
		[]llama.ChatMessage{{Role: "user", Content: "nudge scenario"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	successfulSearches := 0

	for _, result := range results {
		if result.Tool == "search_files" && result.Error == "" {
			successfulSearches++
		}

		if result.Summary == "Tool hidden by active contract" && strings.Contains(result.Error, "search_files") {
			t.Fatalf("search_files should not stay hidden after concrete inspection; results=%#v events=%v", results, eventRolesFor(events))
		}
	}

	if successfulSearches != 3 {
		t.Fatalf("expected first, concrete follow-up, and post-inspection searches to execute, got %d results=%#v", successfulSearches, results)
	}
}

// TestRejectionRetryBlocked verifies that when a pre-execute guard
// rejects a tool call, a byte-identical retry on a later step
// short-circuits the turn via rejection_retry_blocked instead of
// replaying the rejection cycle.
func TestRejectionRetryBlocked(t *testing.T) {
	// A repeated root list_files in explore mode trips the broad-inspection
	// guard without using the chained-list_files hiding path. The first retry
	// gets a nudge (rejection_retry_nudge), the second retry gets blocked
	// (rejection_retry_blocked) — matching the nudge-then-block pattern.
	rejectedArgs := `{"tool":"list_files","path":"."}`
	replies := []string{
		`{"tool":"list_files","path":"."}`,
		`{"tool":"list_files","path":"internal/runtime/agent"}`,
		rejectedArgs, // rejected by broad-inspection guard
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go"}`,
		rejectedArgs, // first retry — nudged
		`{"tool":"read_file","path":"internal/surfaces/tui/app/app.go"}`,
		rejectedArgs, // second retry — blocked
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	if got := countEventsByRole(events, "rejection_retry_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_nudge event on first retry, got %d; events: %v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_blocked event on second retry, got %d; events: %v", got, eventRolesFor(events))
	}
}

func TestMutationRepeatedFullReadStillAllowsWriteInsteadOfForcedAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string { return \"Save changes\" }\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths:\n- internal/labels/labels.go\n\nVerification: go test ./... passed.",
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

	events := []RunEvent(nil)
	results := []workspace.ToolResult(nil)
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Shorten the save label in internal/labels/labels.go."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies)-1 {
		t.Fatalf("expected recovery to close immediately after source-aware verification without a redundant final-answer request, got %d requests", requests)
	}

	if got := countEventsByRole(events, "implementation_target_write_ready_source_refresh_allowed") + countEventsByRole(events, "implementation_target_write_ready_nudge"); got == 0 {
		t.Fatalf("expected at least one target-write-ready source-refresh or nudge event, got %d; events=%v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "mutation_rejected_retry_write_recovery"); got != 0 {
		t.Fatalf("repeated full reads should not use rejected-retry recovery, got %d events; events=%v", got, eventRolesFor(events))
	}

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("mutation no-write retry must not force answer through rejection_retry_blocked, got %d; events=%v", got, eventRolesFor(events))
	}

	data, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}

	if !strings.Contains(string(data), `return "Save"`) || strings.Contains(string(data), `Save changes`) {
		t.Fatalf("expected edit to be applied, got:\n%s\nresults=%#v", string(data), results)
	}

	if !strings.Contains(answer, "internal/labels/labels.go") {
		t.Fatalf("expected final answer to report changed path, got:\n%s", answer)
	}

	if !strings.Contains(strings.ToLower(answer), "verification") {
		t.Fatalf("expected final answer to report verification status, got:\n%s", answer)
	}
}

func TestRunnerRepeatedNoWriteRecoveryRepairsBlockedNonWriteBeforeClosing(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write label source: %v", err)
	}

	replies := []string{
		"I found the label source and can make the change.",
		`{"tool":"list_files","path":"internal/labels"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths:\n- internal/labels/labels.go\n\nVerification: go test ./... passed.",
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

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		Shell:        "/bin/sh",
		EnableWrites: true,
		EnableShell:  true,
	})
	mgr.MarkPathObserved("internal/labels/labels.go")

	var (
		events             []RunEvent
		completionStatuses []string
	)

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10, MaxModelCalls: 20},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/labels/labels.go"},
			Constraints:       []string{"exact_anchor_implementation", "implementation_packet_write_ready"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
			if event.Role == "completion_status" {
				completionStatuses = append(completionStatuses, event.Content)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Shorten the save label in the session tab."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "implementation_write_only_recovery_non_write_repair"); got != 1 {
		t.Fatalf("expected one write-only non-write repair event, got %d; events=%s", got, formatRunEventsForTest(events))
	}

	for _, status := range completionStatuses {
		if strings.Contains(status, "write_only_recovery_no_edit_attempted") ||
			strings.Contains(status, "write_only_recovery_required_paths_remaining") {

			t.Fatalf("write-only non-write should get one repair turn before closeout, got status %s; events=%s", status, formatRunEventsForTest(events))
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}

	if !strings.Contains(string(data), `return "Save"`) || strings.Contains(string(data), `Save changes`) {
		t.Fatalf("expected repair turn to apply edit, got:\n%s", string(data))
	}

	if !strings.Contains(answer, "internal/labels/labels.go") || !strings.Contains(strings.ToLower(answer), "verification") {
		t.Fatalf("expected final implementation answer after repair, got:\n%s", answer)
	}
}
