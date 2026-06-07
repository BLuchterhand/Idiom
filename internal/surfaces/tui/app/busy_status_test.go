package app

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent"
)

func TestBusyStatusForPromptGenericizesRequest(t *testing.T) {
	got := busyStatusForPrompt("Can we change the Qwen is working on your request affordance?")
	if got != "Qwen is orienting around your request" {
		t.Fatalf("unexpected prompt status: %q", got)
	}
	if strings.Contains(got, "working on your request") {
		t.Fatalf("prompt status should not use the old static busy text: %q", got)
	}
}

func TestBusyStatusForSearchRequestUsesGenericTopic(t *testing.T) {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: "currentTurnBusyStatus",
			Glob:    "internal/surfaces/tui/app/*.go",
		},
	}, "")
	if got != "Qwen is searching for code related to current turn busy status" {
		t.Fatalf("unexpected search status: %q", got)
	}
}

func TestBusyStatusForReadRequestUsesPathTopic(t *testing.T) {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool: "read_file",
			Path: "internal/surfaces/tui/app/model_state.go",
		},
	}, "")
	if got != "Qwen is reading code related to model state" {
		t.Fatalf("unexpected read status: %q", got)
	}
}

func TestBusyStatusForRelationshipRequest(t *testing.T) {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:  "context_query",
			Query: "callers of runAgentTurn and handleToolTrace",
		},
	}, "")
	if got != "Qwen is finding intersections between run agent turn and handle tool trace" {
		t.Fatalf("unexpected relationship status: %q", got)
	}
}

func TestBusyStatusForVerificationCommand(t *testing.T) {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool:    "shell",
			Command: "go test ./internal/surfaces/tui/app -count=1",
		},
	}, "")
	if got != "Qwen is running tests" {
		t.Fatalf("unexpected shell status: %q", got)
	}
}

func TestBusyStatusDoesNotExposeFullPaths(t *testing.T) {
	got := busyStatusForToolRequest(agent.ToolRequestEvent{
		Request: workspace.ToolRequest{
			Tool: "read_file",
			Path: "internal/surfaces/tui/app/update_handlers.go",
		},
	}, "")
	if strings.Contains(got, "internal/surfaces/tui/app") || strings.Contains(got, ".go") {
		t.Fatalf("status should genericize paths, got %q", got)
	}
}
