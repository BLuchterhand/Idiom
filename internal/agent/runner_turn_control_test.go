package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestToolParseErrorFeedbackExplainsSubmitPlanEditsArrayShape(t *testing.T) {
	got := toolParseErrorFeedback(`{"edits":"[]"}`, errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits of type []workspace.PlanEdit"))
	for _, want := range []string{
		"submit_plan.edits was emitted as a quoted string",
		"JSON array value, not a string",
		`"edits":[`,
		`"start_line":10`,
		"Prefer start_line/end_line+content",
		"complete executable source text",
		"not placeholders or prose",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("feedback missing %q:\n%s", want, got)
		}
	}
}

func TestRunnerTurnControllerRejectToolRecordsRejectionState(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""
	var lastToolResult *workspace.ToolResult
	consecutiveRejected := 0
	consecutiveNoProgress := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	metrics := &runnerMetrics{}
	requestBudget := newRunnerRequestBudgetState(2)
	currentFingerprint := "read_file|internal/agent/runner.go"
	rejectedActions := map[string]int{}
	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}
	controller := runnerTurnController{
		runner:                     &Runner{},
		ctx:                        context.Background(),
		metrics:                    metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collected,
		rejectedActions:            rejectedActions,
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejected,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}

	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}
	answer, err, done := controller.rejectTool(2, nil, req, "Read rejected", "bad path")
	if err != nil {
		t.Fatalf("rejectTool err = %v", err)
	}
	if done {
		t.Fatalf("rejectTool done = true, want false")
	}
	if answer != "" {
		t.Fatalf("rejectTool answer = %q, want empty", answer)
	}
	if len(collected) != 1 {
		t.Fatalf("collected results = %d, want 1", len(collected))
	}
	if got := metrics.RejectedActions; got != 1 {
		t.Fatalf("RejectedActions = %d, want 1", got)
	}
	if got := consecutiveRejected; got != 1 {
		t.Fatalf("consecutiveRejected = %d, want 1", got)
	}
	if got := rejectedActions[toolActionClass(req)]; got != 1 {
		t.Fatalf("rejectedActions[%q] = %d, want 1", toolActionClass(req), got)
	}
	if got := requestBudget.fingerprint.rejected[currentFingerprint]; got != "Read rejected" {
		t.Fatalf("rejected fingerprint = %q, want %q", got, "Read rejected")
	}
	if lastToolKey == "" {
		t.Fatalf("lastToolKey was not recorded")
	}
	if lastToolResult == nil || lastToolResult.Error != "bad path" {
		t.Fatalf("lastToolResult = %#v, want recorded rejection", lastToolResult)
	}
	if len(turnMessages) != 1 {
		t.Fatalf("turn messages = %d, want 1", len(turnMessages))
	}
}

func TestRunnerTurnControllerRecordRejectedResultRecordsRejectionFingerprint(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""
	var lastToolResult *workspace.ToolResult
	consecutiveRejected := 0
	consecutiveNoProgress := 0
	metrics := &runnerMetrics{}
	requestBudget := newRunnerRequestBudgetState(2)
	currentFingerprint := "harness_feedback|{}"
	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}
	controller := runnerTurnController{
		requestBudget:      &requestBudget,
		recorder:           &recorder,
		currentFingerprint: &currentFingerprint,
	}

	req := workspace.ToolRequest{Tool: "harness_feedback"}
	controller.recordRejectedResult(3, nil, req, workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Tool unavailable on current surface",
		Error:   "harness_feedback is not available now",
	}, "tool_rejected")

	if got := requestBudget.fingerprint.rejected[currentFingerprint]; got != "Tool unavailable on current surface" {
		t.Fatalf("rejected fingerprint = %q, want recorded summary", got)
	}
}

func TestRunnerTurnControllerRejectToolDoesNotMarkMissingNamedGapAsDeadEnd(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""
	var lastToolResult *workspace.ToolResult
	consecutiveRejected := 0
	consecutiveNoProgress := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	metrics := &runnerMetrics{}
	requestBudget := newRunnerRequestBudgetState(2)
	currentFingerprint := "read_file|internal/agent/runner.go"
	rejectedActions := map[string]int{}
	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}
	controller := runnerTurnController{
		runner:                     &Runner{},
		ctx:                        context.Background(),
		metrics:                    metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collected,
		rejectedActions:            rejectedActions,
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejected,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}

	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}
	answer, err, done := controller.rejectTool(2, nil, req, "Post-readiness follow-up missing named gap", "name the exact unresolved gap first")
	if err != nil {
		t.Fatalf("rejectTool err = %v", err)
	}
	if done {
		t.Fatalf("rejectTool done = true, want false")
	}
	if answer != "" {
		t.Fatalf("rejectTool answer = %q, want empty", answer)
	}
	if got := metrics.RejectedActions; got != 1 {
		t.Fatalf("RejectedActions = %d, want 1", got)
	}
	if got := consecutiveRejected; got != 0 {
		t.Fatalf("consecutiveRejected = %d, want 0 for recoverable legibility rejection", got)
	}
	if got := rejectedActions[toolActionClass(req)]; got != 0 {
		t.Fatalf("rejectedActions[%q] = %d, want 0 for recoverable legibility rejection", toolActionClass(req), got)
	}
	if got := requestBudget.fingerprint.rejected[currentFingerprint]; got != "Post-readiness follow-up missing named gap" {
		t.Fatalf("rejected fingerprint = %q, want %q", got, "Post-readiness follow-up missing named gap")
	}
}

func TestRunnerTurnControllerRejectCompletionPreservesReplyAndRecordsGate(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""
	var lastToolResult *workspace.ToolResult
	consecutiveRejected := 0
	consecutiveNoProgress := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	metrics := &runnerMetrics{}
	requestBudget := newRunnerRequestBudgetState(2)
	currentFingerprint := ""
	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}
	controller := runnerTurnController{
		runner:                     &Runner{},
		ctx:                        context.Background(),
		metrics:                    metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collected,
		rejectedActions:            map[string]int{},
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejected,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}

	reply := llama.ChatMessage{Role: "assistant", Content: "partial answer"}
	result := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Final answer rejected",
		Error:   "need more evidence",
	}

	controller.rejectCompletion(3, nil, reply, result)

	if completionGateRejections != 1 {
		t.Fatalf("completionGateRejections = %d, want 1", completionGateRejections)
	}
	if metrics.CompletionGateRejections != 1 {
		t.Fatalf("metrics.CompletionGateRejections = %d, want 1", metrics.CompletionGateRejections)
	}
	if consecutiveNoProgress != 1 {
		t.Fatalf("consecutiveNoProgress = %d, want 1", consecutiveNoProgress)
	}
	if lastToolKey != "" {
		t.Fatalf("lastToolKey = %q, want empty", lastToolKey)
	}
	if lastToolResult == nil || lastToolResult.Error != "need more evidence" {
		t.Fatalf("lastToolResult = %#v, want completion gate result", lastToolResult)
	}
	if len(collected) != 1 {
		t.Fatalf("collected results = %d, want 1", len(collected))
	}
	if len(turnMessages) != 2 {
		t.Fatalf("turn messages = %d, want 2", len(turnMessages))
	}
	if turnMessages[0].Role != "assistant" || turnMessages[0].Content != "partial answer" {
		t.Fatalf("preserved reply = %#v, want assistant partial answer", turnMessages[0])
	}
}

func TestRunnerTurnControllerRejectCompletionDropsReasoningWhenNotPreserving(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	metrics := &runnerMetrics{}
	opts := llama.ChatOptions{PreserveThinking: false}
	recorder := runnerTurnRecorder{
		collectedResults:      &collected,
		turnMessages:          &turnMessages,
		metrics:               metrics,
		consecutiveNoProgress: new(int),
	}
	controller := runnerTurnController{
		runner:                   &Runner{},
		metrics:                  metrics,
		recorder:                 &recorder,
		opts:                     &opts,
		turnMessages:             &turnMessages,
		collectedResults:         &collected,
		completionGateRejections: new(int),
	}

	controller.rejectCompletion(1, nil, llama.ChatMessage{
		Role:             "assistant",
		Content:          "",
		ReasoningContent: "hidden-only failed action",
	}, workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Final answer rejected",
		Error:   "missing submit_plan",
	})

	if len(turnMessages) == 0 {
		t.Fatalf("expected rejected reply to be preserved in history")
	}
	if turnMessages[0].ReasoningContent != "" {
		t.Fatalf("rejected reasoning should not be preserved when PreserveThinking=false: %#v", turnMessages[0])
	}
}

func TestRunnerTurnControllerRejectCompletionCompactsLargeUnwrittenMutationAnswer(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""
	var lastToolResult *workspace.ToolResult
	consecutiveRejected := 0
	consecutiveNoProgress := 0
	rejectionRecoveryNudgeUsed := false
	completionGateRejections := 0
	metrics := &runnerMetrics{}
	requestBudget := newRunnerRequestBudgetState(2)
	currentFingerprint := ""
	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}
	controller := runnerTurnController{
		runner: &Runner{
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				RequiredEditPaths: []string{"scratchpad.md"},
			},
		},
		ctx:                        context.Background(),
		metrics:                    metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collected,
		rejectedActions:            map[string]int{},
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejected,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}

	largeReply := "UNWRITTEN DELIVERABLE ANALYSIS " + strings.Repeat("detail ", 900)
	result := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "no successful write tool result is present",
	}

	controller.rejectCompletion(3, nil, llama.ChatMessage{
		Role:             "assistant",
		Content:          largeReply,
		ReasoningContent: "hidden reasoning",
	}, result)

	if len(turnMessages) != 2 {
		t.Fatalf("turn messages = %d, want 2", len(turnMessages))
	}
	if !strings.Contains(turnMessages[0].Content, "omitted rejected final answer") {
		t.Fatalf("expected compact rejection marker, got %q", turnMessages[0].Content)
	}
	if len(turnMessages[0].Content) >= len(largeReply) {
		t.Fatalf("expected compacted content shorter than rejected reply")
	}
	if turnMessages[0].ReasoningContent != "" {
		t.Fatalf("expected rejected reasoning content to be dropped, got %q", turnMessages[0].ReasoningContent)
	}
}
