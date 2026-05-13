package agent

import (
	"context"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

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
	if got := requestBudget.rejectedFingerprints[currentFingerprint]; got != "Read rejected" {
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
	if got := requestBudget.rejectedFingerprints[currentFingerprint]; got != "Post-readiness follow-up missing named gap" {
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
