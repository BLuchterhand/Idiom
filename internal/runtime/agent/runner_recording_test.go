package agent

import (
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
)

func TestRunnerTurnRecorderRecordTracksProgressAndResetsRejected(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := ""

	var lastToolResult *workspace.ToolResult

	consecutiveRejected := 2
	consecutiveNoProgress := 3
	metrics := &runnerMetrics{}

	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}

	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read file",
		Output:  "package agent",
	}

	recorder.record(4, req, result, "tool_result", runnerRecordOptions{
		TrackProgress: true,
		ResetRejected: true,
	})

	if got := len(collected); got != 1 {
		t.Fatalf("collected results = %d, want 1", got)
	}

	if got := len(turnMessages); got != 1 {
		t.Fatalf("turn messages = %d, want 1", got)
	}

	if lastToolKey == "" {
		t.Fatalf("lastToolKey was not recorded")
	}

	if lastToolResult == nil || lastToolResult.Summary != "Read file" {
		t.Fatalf("lastToolResult = %#v, want recorded result", lastToolResult)
	}

	if consecutiveRejected != 0 {
		t.Fatalf("consecutiveRejected = %d, want 0", consecutiveRejected)
	}

	if consecutiveNoProgress != 0 {
		t.Fatalf("consecutiveNoProgress = %d, want 0", consecutiveNoProgress)
	}

	if metrics.ProgressTurns != 1 {
		t.Fatalf("ProgressTurns = %d, want 1", metrics.ProgressTurns)
	}
}

func TestRunnerTurnRecorderRecordWithOptionsTracksRejectedStateWithoutChangingLastToolKey(t *testing.T) {
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	lastToolKey := "keep"

	var lastToolResult *workspace.ToolResult

	consecutiveRejected := 1
	consecutiveNoProgress := 2
	metrics := &runnerMetrics{}

	recorder := runnerTurnRecorder{
		collectedResults:          &collected,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejected,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   metrics,
	}

	req := workspace.ToolRequest{Tool: "search_files", Pattern: "runner"}
	result := workspace.ToolResult{
		Tool:    "search_files",
		Summary: "Search rejected",
		Error:   "not allowed",
	}

	recorder.recordWithOptions(2, req, result, "permission_denied", runnerRecordOptions{
		CountRejected:           true,
		IncrementNoProgress:     true,
		IncrementRejectedStreak: true,
		SetLastToolKey:          false,
	})

	if got := len(collected); got != 1 {
		t.Fatalf("collected results = %d, want 1", got)
	}

	if got := len(turnMessages); got != 1 {
		t.Fatalf("turn messages = %d, want 1", got)
	}

	if lastToolKey != "keep" {
		t.Fatalf("lastToolKey = %q, want keep", lastToolKey)
	}

	if lastToolResult == nil || lastToolResult.Error != "not allowed" {
		t.Fatalf("lastToolResult = %#v, want rejected result", lastToolResult)
	}

	if consecutiveRejected != 2 {
		t.Fatalf("consecutiveRejected = %d, want 2", consecutiveRejected)
	}

	if consecutiveNoProgress != 3 {
		t.Fatalf("consecutiveNoProgress = %d, want 3", consecutiveNoProgress)
	}

	if metrics.RejectedActions != 1 {
		t.Fatalf("RejectedActions = %d, want 1", metrics.RejectedActions)
	}

	if metrics.ProgressTurns != 0 {
		t.Fatalf("ProgressTurns = %d, want 0", metrics.ProgressTurns)
	}
}
