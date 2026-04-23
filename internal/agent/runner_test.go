package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/workspace"
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


func TestPinnedPathsFromToolResultsRejectsToolJSONFragments(t *testing.T) {
	paths := pinnedPathsFromToolResults([]workspace.ToolResult{
		{
			Tool:    "go_move_declarations",
			Summary: `Blocked go_move_declarations with path:"internal/app/analysis_agents.go" and moves: {destination:"internal/app/orchestration.go", pattern:"tag:foo"}.`,
			Output:  `{"tool":"go_move_declarations","path":"internal/app/analysis_agents.go:12:3","moves":[{"destination":"internal/app/implementation.go"}]}`,
			Error:   "internal/app/analysis_agents.go:12:3: move was outside approval scope",
		},
	}, 8)

	if !containsRunnerString(paths, "internal/app/analysis_agents.go") {
		t.Fatalf("expected source path to be pinned, got %#v", paths)
	}
	for _, forbidden := range []string{"internal/app/orchestration.go", "internal/app/implementation.go"} {
		if containsRunnerString(paths, forbidden) {
			t.Fatalf("destination path %s must not be pinned as context, got %#v", forbidden, paths)
		}
	}
	for _, path := range paths {
		if strings.ContainsAny(path, "\"'\\{}[],:") {
			t.Fatalf("pinned path should be normalized, got %#v in %#v", path, paths)
		}
	}
}

func TestBuildRunnerNextActionStateGuidesAfterBootstrappedReadBlocker(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Implementation bootstrap already inspected this target",
		Error:   "internal/app/app.go has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
		Output:  "internal/app/app.go: already inspected",
	}
	state := buildRunnerNextActionState(
		"implement",
		3,
		"pre_execute_guard",
		req,
		result,
		[]workspace.ToolResult{result},
		true,
		"",
	)
	if state.Lane != "implement" || state.Step != 3 || state.LastTool != "read_file" || state.LastTarget != "internal/app/app.go" {
		t.Fatalf("unexpected state identity: %#v", state)
	}
	if !strings.Contains(strings.Join(state.Constraints, "\n"), "declaration_moves_forbidden") {
		t.Fatalf("expected declaration move constraint, got %#v", state.Constraints)
	}
	if !strings.Contains(strings.Join(state.Constraints, "\n"), "bootstrap_evidence_available") {
		t.Fatalf("expected bootstrap evidence constraint, got %#v", state.Constraints)
	}
	if !strings.Contains(strings.Join(state.InvalidNextActions, "\n"), "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("expected duplicate read invalid action, got %#v", state.InvalidNextActions)
	}
	actions := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(actions, "edit_file on the approved target") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}
	if !strings.Contains(actions, "narrow read_file") {
		t.Fatalf("expected narrow read guidance, got %#v", state.NextValidActions)
	}
}

func TestRunnerEmitsNextActionStateAfterPreExecuteGuard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"Blocked by the read guard.",
	}
	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var states []runnerNextActionState
	var feedback []runnerFailureFeedbackPacket
	var stepInstructions []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool != "read_file" {
				return nil
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation bootstrap already inspected this target",
				Error:   "README.md has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
			}
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				var state runnerNextActionState
				if err := json.Unmarshal([]byte(event.Content), &state); err != nil {
					t.Fatalf("decode next action state %q: %v", event.Content, err)
				}
				states = append(states, state)
			case "failure_feedback":
				var packet runnerFailureFeedbackPacket
				if err := json.Unmarshal([]byte(event.Content), &packet); err != nil {
					t.Fatalf("decode failure feedback %q: %v", event.Content, err)
				}
				feedback = append(feedback, packet)
			case "step_instruction":
				stepInstructions = append(stepInstructions, event.Content)
			default:
				return
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Blocked by the read guard." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(states) != 1 {
		t.Fatalf("expected one next_action_state event, got %#v", states)
	}
	state := states[0]
	if state.Reason != "pre_execute_guard" || state.LastTool != "read_file" || state.LastTarget != "README.md" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if !strings.Contains(strings.Join(state.NextValidActions, "\n"), "edit_file on the approved target") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}
	if len(feedback) != 1 {
		t.Fatalf("expected one failure feedback packet, got %#v", feedback)
	}
	if feedback[0].FailureClass != "guard_rejection" {
		t.Fatalf("expected frontier failure class, got %#v", feedback[0])
	}
	if !containsRunnerString(feedback[0].DoNotRepeat, "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("expected duplicate read in do-not-repeat actions, got %#v", feedback[0])
	}
	if len(stepInstructions) != 0 {
		t.Fatalf("step_instruction events were removed — expected none, got %#v", stepInstructions)
	}
	if len(payloads) < 2 {
		t.Fatalf("expected second model request after guard, got %d", len(payloads))
	}
	for _, message := range payloads[1].Messages {
		if strings.Contains(message.Content, "[frontier step instruction]") {
			t.Fatalf("second model request should not contain frontier step instruction: %q", message.Content)
		}
		if strings.Contains(message.Content, "[failure feedback packet]") {
			t.Fatalf("second model request should not contain failure feedback packet: %q", message.Content)
		}
	}
}

func TestRunnerBootstrappedReadReminderDoesNotPoisonNarrowRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "line 1\nline 2\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1}\n```",
		"Done after narrow read.",
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

	var replayedBootstrapReminder bool
	var narrowReadSucceeded bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 4},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool == "read_file" && req.Path == "README.md" && req.StartLine == 0 && req.EndLine == 0 {
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Implementation bootstrap already inspected this target",
					Error:   "README.md has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
				}
			}
			return nil
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "use the bootstrapped README evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "replayed same blocker") {
				replayedBootstrapReminder = true
			}
			if event.Result.Tool == "read_file" && event.Result.Error == "" && strings.Contains(event.Result.Summary, "lines 1-1") {
				narrowReadSucceeded = true
			}
			if strings.Contains(event.Result.Summary, "Repeated dead-end action rejected") {
				t.Fatalf("bootstrapped read reminder should not become a repeated dead-end: %#v", event.Result)
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after narrow read." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !replayedBootstrapReminder {
		t.Fatal("expected repeated full read to replay the bootstrap reminder once")
	}
	if !narrowReadSucceeded {
		t.Fatal("expected narrow read to execute after bootstrapped-read reminder")
	}
}



func containsRunnerString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRunnerPreservesReasoningContentBetweenToolTurns(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	replies := []string{
		"{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"tool-call reasoning\",\"content\":\"```tool\\n{\\\"tool\\\":\\\"read_file\\\",\\\"path\\\":\\\"README.md\\\"}\\n```\"}}]}",
		`{"choices":[{"message":{"role":"assistant","content":"Done."}}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) > len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		_, _ = w.Write([]byte(replies[len(payloads)-1]))
	}))
	defer server.Close()

	enableThinking := true
	var thinkingEvents []runnerThinkingTrace
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		OnEvent: func(event RunEvent) {
			if event.Role != "thinking_trace" {
				return
			}
			var trace runnerThinkingTrace
			if err := json.Unmarshal([]byte(event.Content), &trace); err != nil {
				t.Fatalf("decode thinking trace %q: %v", event.Content, err)
			}
			thinkingEvents = append(thinkingEvents, trace)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: true, MaxTokens: 128},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	var foundPreserved bool
	for _, message := range payloads[1].Messages {
		if message.Role == "assistant" && message.ReasoningContent == "tool-call reasoning" {
			foundPreserved = true
			break
		}
	}
	if !foundPreserved {
		t.Fatalf("expected second request to include prior assistant reasoning_content, got %#v", payloads[1].Messages)
	}
	var sawPreservedTrace bool
	for _, trace := range thinkingEvents {
		if trace.ReasoningChars > 0 && trace.PreservedInHistory {
			sawPreservedTrace = true
			break
		}
	}
	if !sawPreservedTrace {
		t.Fatalf("expected thinking_trace to report preserved reasoning, got %#v", thinkingEvents)
	}
}

func TestRunnerPreservesEmptyReplyReasoningForRetry(t *testing.T) {
	root := t.TempDir()
	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","reasoning_content":"unfinished reasoning","content":""}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"Done."}}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) > len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		_, _ = w.Write([]byte(replies[len(payloads)-1]))
	}))
	defer server.Close()

	enableThinking := true
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
		[]llama.ChatMessage{{Role: "user", Content: "answer"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: true, MaxTokens: 128},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	var foundPreserved bool
	for _, message := range payloads[1].Messages {
		if message.Role == "assistant" && message.ReasoningContent == "unfinished reasoning" {
			foundPreserved = true
			break
		}
	}
	if !foundPreserved {
		t.Fatalf("expected retry request to include empty reply reasoning_content, got %#v", payloads[1].Messages)
	}
}

func writeTestFile(root string, path string, content string) error {
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func TestRunnerExecutesReadOnlyChildTask(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "route evidence\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"explorer\",\"mode\":\"read_only\",\"prompt\":\"inspect README\"}\n```",
		"Child evidence: README.md mentions route evidence.",
		"Final: child evidence found in README.md.",
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
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}
	var sawTask bool
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate focused inspection"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "task" && strings.Contains(event.Result.Output, "Child evidence") {
				sawTask = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawTask {
		t.Fatal("expected task tool result with child output")
	}
	if !strings.Contains(answer, "Final") {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestReadOnlyChildTaskPermissionPolicyDeniesWritesAndAllowsReads(t *testing.T) {
	policy := readOnlyChildTaskPermissionPolicy()
	if decision := policy.Decide(workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}); decision.Decision != PermissionDeny {
		t.Fatalf("edit_file decision = %q, want deny", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "read_file", Path: "README.md"}); decision.Decision != PermissionAllow {
		t.Fatalf("read_file decision = %q, want allow", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("task decision = %q, want deny", decision.Decision)
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesDiffHidingAndReleaseCommands(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	deniedShell := []string{
		"git add README.md",
		"git commit -m child",
		"git reset --hard",
		"git push origin HEAD",
		"gh pr create",
		"gh pr merge 1",
		"gh release create v1",
		"npm publish",
		"pnpm publish",
		"docker push image",
		"kubectl apply -f deploy.yaml",
	}
	for _, command := range deniedShell {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("task decision = %q, want deny", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}); decision.Decision != PermissionAllow {
		t.Fatalf("edit_file decision = %q, want allow", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "go test ./..."}); decision.Decision != PermissionAllow {
		t.Fatalf("go test decision = %q, want allow", decision.Decision)
	}
}

func TestRunnerExecutesWorkbranchWriteChildTaskWithoutMutatingParent(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	replies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change README greeting\"}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting with child-authored version\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
		"Parent inspected child patch.",
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
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	var childPatch string
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate isolated write"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "task" {
				childPatch = event.Result.Output
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read parent README: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("parent workspace was mutated: %q", data)
	}
	if !strings.Contains(childPatch, "README.md") || !strings.Contains(childPatch, "hello from child") {
		t.Fatalf("expected child patch output, got:\n%s", childPatch)
	}
}

func TestRunnerRoutesWorkbranchChildOntoImplementerClient(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	// Planner replies: delegate, then final summary after child returns.
	plannerReplies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change greeting\"}\n```",
		"Parent inspected child patch.",
	}
	plannerHits := 0
	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if plannerHits >= len(plannerReplies) {
			t.Fatalf("planner received unexpected extra request")
		}
		reply := plannerReplies[plannerHits]
		plannerHits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer plannerServer.Close()

	// Implementer replies: drive the child through ledger, read, edit, final.
	implReplies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
	}
	implHits := 0
	implServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if implHits >= len(implReplies) {
			t.Fatalf("implementer received unexpected extra request")
		}
		reply := implReplies[implHits]
		implHits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer implServer.Close()

	runner := &Runner{
		Client:            llama.NewClient(plannerServer.URL, 2*time.Second),
		ImplementerClient: llama.NewClient(implServer.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if plannerHits != len(plannerReplies) {
		t.Fatalf("planner hit count = %d, want %d", plannerHits, len(plannerReplies))
	}
	if implHits == 0 {
		t.Fatal("expected implementer client to receive child-task requests, got none")
	}
}

func TestRunnerReplaysFirstRepeatedToolRequestThenRejectsExactLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"Stopped after repeated loops.",
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

	var replayedSummary string
	var rejectedSummary string
	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 2,
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "budget_ledger":
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayedSummary = event.Result.Summary
			}
			if strings.Contains(event.Result.Summary, "Repeated tool request rejected") {
				rejectedSummary = event.Result.Summary
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// DOOM_LOOP guard (threshold=3) trips on the third identical pwd call
	// and terminates the loop with a doom_loop reason. The test seed
	// sends pwd three times and then a plaintext final reply; we should
	// never reach the final reply because the guard fires first.
	if requests < 3 {
		t.Fatalf("expected at least 3 model requests before doom-loop guard, got %d", requests)
	}
	if replayedSummary == "" {
		t.Fatal("expected second identical pwd to replay prior evidence before the guard trips")
	}
	_ = rejectedSummary
	_ = answer
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %#v", ledger)
	}
}

func TestRunnerRepeatedBroadInspectionStillHitsExploreGuard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"Stopped after broad-inspection guard.",
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 4,
		},
	}

	var broadGuarded bool
	var replayed bool
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(event.Result.Summary, "Additional broad inspection rejected") {
				broadGuarded = true
			}
			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Stopped after broad-inspection guard." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !broadGuarded {
		t.Fatal("expected repeated broad inspection to hit the explore guard")
	}
	if replayed {
		t.Fatal("repeated broad inspection must not bypass explore guard through replay")
	}
}

func TestRunnerEmitsRecoveryNudgeBeforeGivingUpOnRepeatedBroadInspection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Answered from prior evidence after nudge.",
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
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
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

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "identify small refactor opportunities"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// DOOM_LOOP guard (threshold=3) catches the third identical list_files
	// call before the rejection_recovery_nudge path is reached. The
	// fourth scripted reply is unused — we should not see it.
	if stopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %q", stopReason)
	}
	if requests > 3 {
		t.Fatalf("expected guard to fire on the third call; model was invoked %d times", requests)
	}
}

func TestRunnerStopsExploreAfterRepeatedMissingPathProbes(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Use README.md and the bootstrapped evidence; no more path probes are needed.",
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	var ledger runnerBudgetLedger
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Use README.md and the bootstrapped evidence; no more path probes are needed." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 3 {
		t.Fatalf("expected forced completion after two missing path probes, got %d requests", requests)
	}
	if ledger.StopReason != "missing_path_probe_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("expected missing-path budget ledger, got %#v", ledger)
	}
}

func TestRunnerAllowsMissingPathProbeAfterProgress(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":20}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Completed without forced missing-path stop.",
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

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	var ledger runnerBudgetLedger
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Completed without forced missing-path stop." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected all scripted model requests, got %d", requests)
	}
	if ledger.StopReason != "model_final" || ledger.BudgetExhausted {
		t.Fatalf("expected ordinary model final after non-consecutive missing path probes, got %#v", ledger)
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

func TestRunnerTaskLedgerBlocksFinalUntilRequiredItemsComplete(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"Done too early.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after ledger completion.",
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

	var gateRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform a complex refactor"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "unfinished required work") {
				gateRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after ledger completion." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
	if gateRejections != 1 {
		t.Fatalf("expected one task-ledger completion rejection, got %d", gateRejections)
	}
}

func TestRunnerTaskLedgerRequiredBeforeFinalForComplexWork(t *testing.T) {
	replies := []string{
		"Done without a ledger.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"blocked\",\"content\":\"Apply the requested change\",\"status\":\"blocked\",\"evidence\":\"write approval is required\"}]}\n```",
		"Blocked: write approval is required before I can apply the requested change.",
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

	var missingLedgerRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform complex work"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "no task_ledger has been created") {
				missingLedgerRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked: write approval") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if missingLedgerRejections != 1 {
		t.Fatalf("expected missing-ledger rejection, got %d", missingLedgerRejections)
	}
}

// TestTaskLedgerCompletionGatePlanningExit locks in the planning-mode
// escape hatch: when the user asked only for a plan, the model
// should be able to submit the ledger and reply with an
// explicit planning-exit phrase without the gate forcing it to
// execute every item. Before this fix the model would get stuck
// in a retry loop (or worse, start executing items it was never
// asked to execute).
func TestTaskLedgerCompletionGatePlanningExit(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "pending"},
			{ID: "b", Content: "add tests for X", Status: "pending"},
		},
	})
	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	// Plain final answer with unfinished items → gate rejects.
	if _, rejected := state.completionRejection("here is what I'd change"); !rejected {
		t.Fatalf("gate should reject a final answer with unfinished items and no planning exit")
	}

	// Planning-exit phrase → gate allows the final answer through.
	planningReplies := []string{
		"Plan ready — awaiting approval before I execute the items.",
		"Plan complete. Let me know if you'd like me to proceed.",
		"Planning only — please confirm which items to execute.",
		"Awaiting approval on the above plan.",
	}
	for _, reply := range planningReplies {
		if _, rejected := state.completionRejection(reply); rejected {
			t.Errorf("planning-exit reply should pass the gate, rejected: %q", reply)
		}
	}
}

// TestTaskLedgerCompletionGateStillRejectsVagueReplies guards against
// the planning-exit loophole swallowing genuinely-incomplete replies.
// A reply that doesn't contain any recognized terminal phrase must
// still be rejected when items are pending — otherwise the gate
// loses its value.
func TestTaskLedgerCompletionGateStillRejectsVagueReplies(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "in_progress"},
		},
	})
	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	vague := []string{
		"Done.",
		"Here's what I found.",
		"I've looked at the code.",
	}
	for _, reply := range vague {
		if _, rejected := state.completionRejection(reply); !rejected {
			t.Errorf("vague reply should still be rejected: %q", reply)
		}
	}
}

func TestTaskLedgerRejectsInvalidUpdates(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{})
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "First active task", Status: "in_progress"},
			{ID: "two", Content: "Second active task", Status: "in_progress"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "only one") {
		t.Fatalf("expected multiple-active rejection, got %#v", result)
	}

	result = state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "Use a bad status", Status: "started"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "unsupported status") {
		t.Fatalf("expected status rejection, got %#v", result)
	}
}

func TestRunnerCompactsLongTurnHistoryWithTaskLedgerState(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"in_progress\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after compaction.",
	}
	var requests int
	var sawCompactedPrompt bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []llama.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, msg := range payload.Messages {
			if strings.Contains(msg.Content, "[runner context compaction]") &&
				strings.Contains(msg.Content, "Task ledger:") &&
				strings.Contains(msg.Content, "verify: Run verification") {
				sawCompactedPrompt = true
			}
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

	var sawCompactionEvent bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxContextMessages: 4,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "context_compaction" {
				sawCompactionEvent = true
				if !strings.Contains(event.Content, "runner_task_state") {
					t.Fatalf("expected runner task state compaction event, got %s", event.Content)
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "perform complex work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after compaction." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !sawCompactedPrompt {
		t.Fatal("expected later model request to include compacted runner state")
	}
	if !sawCompactionEvent {
		t.Fatal("expected context_compaction run event")
	}
}

func TestRunnerEmitsBudgetLedgerForModelFinal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 4, "total_tokens": 15},
		})
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "answer"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_final" || ledger.BudgetExhausted || ledger.UsedModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
	if ledger.PromptTokens != 11 || ledger.CompletionTokens != 4 || ledger.TotalTokens != 15 {
		t.Fatalf("expected token usage in budget ledger, got %#v", ledger)
	}
}

func TestRunnerCountsForcedCompletionUsageInBudgetLedger(t *testing.T) {
	replies := []map[string]any{
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "```tool\n{\"tool\":\"pwd\"}\n```"},
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		},
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Forced final."},
			}},
			"usage": map[string]int{"prompt_tokens": 13, "completion_tokens": 3, "total_tokens": 16},
		},
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "pwd then stop"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Forced final." {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "tool_step_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("unexpected budget state: %#v", ledger)
	}
	if ledger.UsedModelCalls != 2 || ledger.PromptTokens != 18 || ledger.CompletionTokens != 5 || ledger.TotalTokens != 23 {
		t.Fatalf("expected forced completion usage in budget ledger, got %#v", ledger)
	}
}

func TestRunnerModelCallBudgetStopsWithLedger(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"work\",\"content\":\"Do work\",\"status\":\"in_progress\"}]}\n```",
		"Budget exhausted with work still in progress.",
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

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxModelCalls:      1,
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Budget exhausted") {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_call_budget_exhausted" || !ledger.BudgetExhausted || ledger.MaxModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
}

func TestRunnerPermissionPolicyDeniesToolBeforeExecution(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Blocked by permission policy.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var permissionEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
			Rules: []ToolPermissionRule{{
				Tool:        "edit_file",
				PathPattern: "README.md",
				Decision:    PermissionDeny,
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "permission_decision" && strings.Contains(event.Content, `"decision":"deny"`) {
				permissionEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked by permission") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if permissionEvent == "" {
		t.Fatal("expected deny permission decision event")
	}
}

func TestRunnerCreatesCheckpointBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Changed README.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var checkpointEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy:           RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "checkpoint" {
				checkpointEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Changed README") {
		t.Fatalf("unexpected answer %q", answer)
	}
	var checkpoint GitCheckpoint
	if err := json.Unmarshal([]byte(checkpointEvent), &checkpoint); err != nil {
		t.Fatalf("decode checkpoint %q: %v", checkpointEvent, err)
	}
	if checkpoint.Head == "" || checkpoint.Dirty {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}
}

func TestToolResultMadeProgressClassifiesUsefulAndStalledResults(t *testing.T) {
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "read_file"}, workspace.ToolResult{Tool: "read_file", Summary: "Read file"}) {
		t.Fatal("read_file summary should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "task_ledger"}, workspace.ToolResult{Tool: "task_ledger", Summary: "Task ledger updated", Output: "- [completed] one: done"}) {
		t.Fatal("task_ledger output should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"}) {
		t.Fatal("successful declaration move should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "apply_patch"}, workspace.ToolResult{Tool: "apply_patch", Summary: "Applied patch with 1 op(s)"}) {
		t.Fatal("successful patch should count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "No-op declaration move"}) {
		t.Fatal("no-op declaration move should not count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "search_content"}, workspace.ToolResult{Tool: "search_content", Error: "failed"}) {
		t.Fatal("failed tools should not count as progress")
	}
	patchPaths := strings.Join(changedPathsFromToolResult(workspace.ToolResult{
		Tool:   "apply_patch",
		Output: "add docs/example.md (8 bytes)\nupdate internal/app/app.go\nupdate+move old.go -> new.go",
	}), "\n")
	for _, want := range []string{"docs/example.md", "internal/app/app.go", "old.go", "new.go"} {
		if !strings.Contains(patchPaths, want) {
			t.Fatalf("expected patch changed paths to include %q, got %q", want, patchPaths)
		}
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
	for _, want := range []string{"Partial findings from this turn", "Verified context:", "found route", "Final blocker:", "bad shell: not allowed", "Recommended next step:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "listed files") {
		t.Fatalf("broad list_files result should not be preferred as narrowing evidence:\n%s", answer)
	}
}

// Adoption fix B (OpenCode alignment) — the forced-completion fallback must
// NOT prefix the reply with apologetic "did not complete cleanly" wording.
// Users read that as a system error; the same content reframed as
// "Partial findings from this turn" presents the evidence honestly.
// Guards against the failure surfaced in
// logs/conversations/20260421-053044.479.jsonl where the fallback answer
// was an apology instead of a useful partial report.
func TestSynthesizeFallbackAnswerOmitsApologeticPrefix(t *testing.T) {
	for _, agent := range []string{"explore", "main"} {
		answer, ok := synthesizeFallbackAnswer(agent, []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read internal/app/app.go lines 1-50", Output: "package app\n"},
		})
		if !ok {
			t.Fatalf("expected fallback answer for agent %q", agent)
		}
		if !strings.Contains(answer, "Partial findings from this turn") {
			t.Fatalf("expected %q fallback to lead with partial-findings framing, got:\n%s", agent, answer)
		}
		if strings.Contains(answer, "did not complete cleanly") {
			t.Fatalf("expected %q fallback to drop apologetic wording, got:\n%s", agent, answer)
		}
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

// Fix 7 (real subagent spawning via the task tool) — exercises
// docs/opencode-fixes-design.md §7. Verifies that the parent's `task` call
// launches an isolated child Runner, that the child actually drives the
// model loop (tool call plus answer), and that the child's intermediate
// messages do not pollute the parent's conversation.
func TestRunnerTaskToolSpawnsIsolatedReadOnlyChild(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "src/widget.go", "package widget\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"scout","mode":"read_only","prompt":"list .go files under src/"}`,
		`{"tool":"list_files","path":"src"}`,
		"Found src/widget.go.",
		"Parent inspected scout's report and concluded.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
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

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
	})
	var childRequests, childResults int
	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "child_tool_request":
				childRequests++
			case "child_tool_result":
				childResults++
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate a read-only probe then report"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected scout's report and concluded." {
		t.Fatalf("unexpected parent answer: %q", answer)
	}
	if childRequests == 0 || childResults == 0 {
		t.Fatalf("expected isolated child tool events to fire (requests=%d results=%d)", childRequests, childResults)
	}
	if requests != 4 {
		t.Fatalf("expected 4 chat requests (1 parent spawn + 2 child steps + 1 parent final), got %d", requests)
	}
}

// Soft-failure nudge — runner must fire rejection_recovery_nudge when three
// tool calls in a row return errors against the same path stem, even though
// none of them are "hard rejections." Mirrors the failure mode we saw in
// logs/conversations/20260420-210101.488.jsonl where the model called
// read_file on five fabricated files inside a non-existent directory.
func TestRunnerEmitsRecoveryNudgeOnRepeatedSameStemSoftFailures(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n"); err != nil {
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
// returns false, the runner stops with stop_reason=doom_loop_guard exactly as
// the nil-gate fallback would. Proves the gate is reachable and its deny path
// short-circuits.
func TestDoomLoopGateDenyPathStops(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Unused reply; the gate should stop the turn.",
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

	_, err := runner.Run(
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
	if requests > 3 {
		t.Fatalf("expected guard to fire on the third call; model invoked %d times", requests)
	}
}

// TestDoomLoopGateApprovePathContinues verifies the approve branch: gate
// returns true, the streak is reset, and execution continues past the
// 3-identical-calls threshold. After approval the model emits a non-tool
// answer and the turn completes normally (no doom_loop_guard stop).
func TestDoomLoopGateApprovePathContinues(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
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
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n"); err != nil {
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
		`{"tool":"read_file","path":"internal/app/missing_a.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_b.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_c.go","start_line":1,"end_line":2}`,
		// 3rd failure on same stem → softFailureNudge fires, counter resets.
		`{"tool":"read_file","path":"internal/app/missing_d.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_e.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_f.go","start_line":1,"end_line":2}`,
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
}

// TestRejectionRetryBlocked verifies that when a pre-execute guard
// rejects a tool call, a byte-identical retry on a later step
// short-circuits the turn via rejection_retry_blocked instead of
// replaying the rejection cycle.
func TestRejectionRetryBlocked(t *testing.T) {
	// Three list_files in a row trips the chained-list_files guard on
	// the third. A later retry of that same 3rd call's fingerprint
	// should trigger rejection_retry_blocked.
	args3 := `{"tool":"list_files","path":"internal/agent"}`
	replies := []string{
		`{"tool":"list_files","path":"internal/app"}`,
		`{"tool":"list_files","path":"internal/agent"}`,
		args3,                          // 3rd consecutive — rejected by chained-list_files guard
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		args3,                          // retry of the rejected fingerprint — blocked
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_blocked event, got %d; events: %v", got, eventRolesFor(events))
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

// TestFingerprintNudgeFiresOncePerFingerprint verifies that when the model
// calls the same tool+args three times *non-consecutively*, the runner
// emits a fingerprint_nudge exactly once — and does NOT trigger the
// DoomLoopGate (which requires consecutive triples).
func TestFingerprintNudgeFiresOncePerFingerprint(t *testing.T) {
	// Alternate A/B/A/B/A so the identical-call run never reaches 3.
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":true}`,
		`{"tool":"list_files","path":"internal/agent","recursive":true}`,
		`{"tool":"list_files","path":"internal/app","recursive":true}`,
		`{"tool":"list_files","path":"internal/agent","recursive":true}`,
		`{"tool":"list_files","path":"internal/app","recursive":true}`,
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

// TestSynthesisNudgeFiresOnceAtThreshold verifies that once the tool-call
// count crosses SynthesisNudgeAt, the runner emits exactly one
// synthesis_nudge for the whole turn.
func TestSynthesisNudgeFiresOnceAtThreshold(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/agent","recursive":false}`,
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

// TestReadBudgetNudgeFiresOnceWhenReadsExceedCeiling verifies the plan-mode
// read-ceiling nudge: once read-class calls cross the ceiling, exactly one
// read_budget_nudge is emitted per turn.
func TestReadBudgetNudgeFiresOnceWhenReadsExceedCeiling(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		"plan drafted",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8, ReadBudgetSoft: 2}
	events, _ := runNudgeScenario(t, policy, replies)

	if got := countEventsByRole(events, "read_budget_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 read_budget_nudge, got %d", got)
	}
}

// TestNudgesDoNotFireWhenDisabled verifies that zero-valued thresholds
// keep the nudges dormant — the feature is opt-in per mode.
func TestNudgesDoNotFireWhenDisabled(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		`{"tool":"list_files","path":"internal","recursive":false}`,
		"done",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8}
	events, _ := runNudgeScenario(t, policy, replies)

	for _, role := range []string{"fingerprint_nudge", "synthesis_nudge", "read_budget_nudge"} {
		if got := countEventsByRole(events, role); got != 0 {
			t.Fatalf("expected no %s events when disabled, got %d", role, got)
		}
	}
}

// TestModePolicyPlanModeHasReadBudget documents the ModePolicy defaults so
// future edits to the numbers are an explicit, reviewed change.
func TestModePolicyPlanModeHasReadBudget(t *testing.T) {
	plan := ModePolicy(PlanMode())
	if plan.ReadBudgetSoft != 8 {
		t.Fatalf("plan ReadBudgetSoft: want 8, got %d", plan.ReadBudgetSoft)
	}
	if plan.FingerprintNudgeAt != 3 {
		t.Fatalf("plan FingerprintNudgeAt: want 3, got %d", plan.FingerprintNudgeAt)
	}
	if plan.SynthesisNudgeAt != 10 {
		t.Fatalf("plan SynthesisNudgeAt: want 10, got %d", plan.SynthesisNudgeAt)
	}
	build := ModePolicy(BuildMode())
	if build.ReadBudgetSoft != 0 {
		t.Fatalf("build ReadBudgetSoft should be 0, got %d", build.ReadBudgetSoft)
	}
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
// Go source file (no line range) is intercepted and redirected to
// go_file_outline, with the decision guide appended once.
func TestDiscoveryRedirectOnReadFileCodePath(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		"final answer",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_redirect event, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_decision_guide"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_decision_guide event, got %d", got)
	}
}

// TestDiscoveryRedirectFallsBackOnSecondCall asserts the one-shot
// semantics: the second identical generic call after a redirect is
// allowed through so the model can still fall back.
func TestDiscoveryRedirectFallsBackOnSecondCall(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_redirect, got %d", got)
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

// TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage confirms the
// full decision guide is injected on the first redirect per language
// and omitted on subsequent redirects in the same turn.
func TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage(t *testing.T) {
	// Two distinct read_file targets — each fires a redirect, but only
	// the first attaches the decision guide.
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
	if got := countEventsByRole(events, "discovery_redirect"); got != 2 {
		t.Fatalf("expected 2 discovery_redirect events, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_decision_guide"); got != 1 {
		t.Fatalf("expected 1 discovery_decision_guide (one-shot per language), got %d", got)
	}
}
