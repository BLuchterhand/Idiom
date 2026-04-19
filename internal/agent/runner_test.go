package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

func TestToolActionClassNormalizesRepeatedDeadEndKeys(t *testing.T) {
	cases := []struct {
		req  workspace.ToolRequest
		want string
	}{
		{req: workspace.ToolRequest{Tool: "list_files"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "list_files", Path: "internal"}, want: "list_files:internal"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "rg route internal/app"}, want: "shell_narrow:rg route internal/app"},
		{req: workspace.ToolRequest{Tool: "read_file", ID: "f12"}, want: "read_file:f12"},
		{req: workspace.ToolRequest{Tool: "go_find_symbol", Pattern: "classifyPromptExecutionPlan"}, want: "go_find_symbol:classifypromptexecutionplan"},
		{req: workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/app/app.go"}, want: "go_file_outline:internal/app/app.go"},
		{req: workspace.ToolRequest{Tool: "go_move_declarations", Path: "internal/app/app.go", Destination: "internal/app/rendering.go"}, want: "go_move_declarations:internal/app/app.go->internal/app/rendering.go"},
	}
	for _, tc := range cases {
		if got := toolActionClass(tc.req); got != tc.want {
			t.Fatalf("toolActionClass(%#v) = %q, want %q", tc.req, got, tc.want)
		}
	}
}

func TestShouldRejectProseAfterGoOutlineRequiresMoveBeforeAnswer(t *testing.T) {
	results := []workspace.ToolResult{{Tool: "go_file_outline", Summary: "Outlined internal/app/app.go"}}
	if !shouldRejectProseAfterGoOutline("Done.", results) {
		t.Fatal("expected normal prose after outline to be rejected before any declaration move")
	}
	if shouldRejectProseAfterGoOutline("Done.", append(results, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"})) {
		t.Fatal("expected prose to be allowed after a declaration move")
	}
	if shouldRejectProseAfterGoOutline("", results) {
		t.Fatal("empty reply should not be handled as prose-after-outline")
	}
}

func TestRunnerUnboundedPolicyLetsCompletionGateDriveDone(t *testing.T) {
	replies := []string{"premature final", "accepted final"}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
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

	rejections := 0
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 3,
		},
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			if rejections == 0 {
				rejections++
				return CompletionGateResult{
					Summary: "done gate rejected",
					Error:   "continue once",
				}
			}
			return CompletionGateResult{Accept: true}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work until done"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "accepted final" {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 2 || rejections != 1 {
		t.Fatalf("expected one gate rejection and two model calls, requests=%d rejections=%d", requests, rejections)
	}
}

func TestToolResultMadeProgressClassifiesUsefulAndStalledResults(t *testing.T) {
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "read_file"}, workspace.ToolResult{Tool: "read_file", Summary: "Read file"}) {
		t.Fatal("read_file summary should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"}) {
		t.Fatal("successful declaration move should count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "No-op declaration move"}) {
		t.Fatal("no-op declaration move should not count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "search_content"}, workspace.ToolResult{Tool: "search_content", Error: "failed"}) {
		t.Fatal("failed tools should not count as progress")
	}
}

func TestSynthesizeFallbackAnswerUsesNarrowEvidenceAndFinalBlocker(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("explore", []workspace.ToolResult{
		{Tool: "list_files", Summary: "listed files", Output: "README.md\ninternal/app/app.go"},
		{Tool: "search_content", Summary: "found route", Output: "internal/app/app.go:42: route"},
		{Tool: "shell", Summary: "bad shell", Error: "not allowed"},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}
	for _, want := range []string{"Exploration did not complete cleanly", "Verified context:", "found route", "Final blocker:", "bad shell: not allowed", "Recommended next step:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "listed files") {
		t.Fatalf("broad list_files result should not be preferred as narrowing evidence:\n%s", answer)
	}
}

func TestCompactOutputRespectsLineBudget(t *testing.T) {
	got := compactOutput("a\nb\nc", 2)
	if got != "a\nb\n..." {
		t.Fatalf("unexpected compact output %q", got)
	}
	if got := compactOutput("a\nb", 0); got != "a\nb" {
		t.Fatalf("expected zero budget to preserve output, got %q", got)
	}
}
