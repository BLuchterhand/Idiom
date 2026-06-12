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

func TestBuildRunnerNextActionStateGuidesAfterBootstrappedReadBlocker(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Implementation bootstrap already inspected this target",
		Error:   "internal/surfaces/tui/app/app.go has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
		Output:  "internal/surfaces/tui/app/app.go: already inspected",
	}
	state := buildRunnerNextActionState(
		"implement",
		3,
		"pre_execute_guard",
		req,
		result,
		[]workspace.ToolResult{result},
		true,
		workerruntime.WorkerRuntimeBrief{},
	)

	if state.Lane != "implement" || state.Step != 3 || state.LastTool != "read_file" || state.LastTarget != "internal/surfaces/tui/app/app.go" {
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
	if !strings.Contains(actions, "approved-target write capability") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}

	if !strings.Contains(actions, "narrow range read") {
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

	var (
		states           []runnerNextActionState
		feedback         []runnerFailureFeedbackPacket
		stepInstructions []string
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableShell:  true,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool != "read_file" {
				return nil
			}

			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation bootstrap already inspected this target",
				Error:   "README.md has already been read in the implementation bootstrap; approved-target write capability or a narrow line range remains available",
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

	if !strings.Contains(strings.Join(state.NextValidActions, "\n"), "approved-target write capability") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}

	if len(feedback) != 1 {
		t.Fatalf("expected one failure feedback packet, got %#v", feedback)
	}

	if feedback[0].FailureClass != "guard_rejection" {
		t.Fatalf("expected frontier failure class, got %#v", feedback[0])
	}

	if !slices.Contains(feedback[0].DoNotRepeat, "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("expected duplicate read in do-not-repeat actions, got %#v", feedback[0])
	}

	if len(stepInstructions) != 0 {
		t.Fatalf("step_instruction events were removed — expected none, got %#v", stepInstructions)
	}

	if len(payloads) < 2 {
		t.Fatalf("expected second model request after guard, got %d", len(payloads))
	}

	var stepInstructionCount int

	for _, message := range payloads[1].Messages {
		if strings.Contains(message.Content, "[frontier step instruction]") {
			stepInstructionCount++
		}

		if strings.Contains(message.Content, "[failure feedback packet]") {
			t.Fatalf("second model request should not contain failure feedback packet: %q", message.Content)
		}
	}

	if stepInstructionCount > 1 {
		t.Fatalf("expected at most one persisted frontier step instruction, got %d", stepInstructionCount)
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

	var (
		replayedBootstrapReminder bool
		narrowReadSucceeded       bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableShell:  true,
			EnableWrites: true,
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

func TestRunnerDropsReasoningContentBetweenToolTurnsWhenNotPreserving(t *testing.T) {
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
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: false, MaxTokens: 128},
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

	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected second request to strip prior reasoning_content, got %#v", payloads[1].Messages)
		}
	}
}

func TestRunnerForcesVisibleOutputForEmptyReplyRetry(t *testing.T) {
	root := t.TempDir()

	type requestPayload struct {
		Messages           []llama.ChatMessage `json:"messages"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
			Preserve       bool  `json:"preserve_thinking"`
		} `json:"chat_template_kwargs,omitempty"`
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

	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected empty-answer retry to strip prior reasoning_content, got %#v", payloads[1].Messages)
		}
	}

	if payloads[1].ChatTemplateKwargs == nil ||
		payloads[1].ChatTemplateKwargs.EnableThinking == nil ||
		*payloads[1].ChatTemplateKwargs.EnableThinking ||
		payloads[1].ChatTemplateKwargs.Preserve {

		t.Fatalf("expected empty-answer retry to force visible output, got %#v", payloads[1].ChatTemplateKwargs)
	}
}

func TestRunnerDropsEmptyReplyReasoningForRetryWhenNotPreserving(t *testing.T) {
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
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: false, MaxTokens: 128},
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

	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected retry request to strip empty reply reasoning_content, got %#v", payloads[1].Messages)
		}
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

func TestReadOnlyChildDoesNotAdvertiseNestedTaskOrWrites(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "route evidence\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"inspect README"}`,
		"Child evidence: README.md mentions route evidence.",
		"Final: child evidence found in README.md.",
	}

	type requestPayload struct {
		Tools []llama.Tool `json:"tools"`
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate focused inspection"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(payloads) < 2 {
		t.Fatalf("expected parent and child requests, got %d", len(payloads))
	}

	childTools := payloads[1].Tools
	for _, tool := range childTools {
		switch tool.Function.Name {
		case "task", "task_ledger", "edit_file", "write_file", "append_file", "apply_patch", "mkdir":
			t.Fatalf("read-only child must not advertise %q; tools: %#v", tool.Function.Name, childTools)
		}
	}
}

func TestForcedCompletionDisablesToolAdvertisements(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"Done from gathered evidence.",
	}

	type requestPayload struct {
		Tools      []llama.Tool `json:"tools,omitempty"`
		ToolChoice string       `json:"tool_choice,omitempty"`
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
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Done from gathered evidence." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected initial plus forced completion request, got %d", len(payloads))
	}

	if len(payloads[1].Tools) != 0 || payloads[1].ToolChoice != "" {
		t.Fatalf("forced completion must be text-only, got tools=%d tool_choice=%q", len(payloads[1].Tools), payloads[1].ToolChoice)
	}
}

func TestLegacyParsedToolResultUsesToolChannel(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"Read README.md.",
	}

	type requestMessage struct {
		Role       string           `json:"role"`
		Content    string           `json:"content,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []llama.ToolCall `json:"tool_calls,omitempty"`
	}

	type requestPayload struct {
		Messages []requestMessage `json:"messages"`
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 4},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Read README.md." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}

	messages := payloads[1].Messages

	var (
		assistantToolCallID string
		sawToolMessage      bool
	)

	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			assistantToolCallID = message.ToolCalls[0].ID
			if message.Content != "" {
				t.Fatalf("legacy tool-call assistant message should not preserve JSON content, got %q", message.Content)
			}
		}

		if message.Role == "tool" {
			sawToolMessage = true

			if assistantToolCallID == "" {
				t.Fatalf("tool message appeared before assistant tool_call: %#v", messages)
			}

			if message.ToolCallID != assistantToolCallID {
				t.Fatalf("tool_call_id = %q, want %q", message.ToolCallID, assistantToolCallID)
			}

			if !strings.Contains(message.Content, "README.md") {
				t.Fatalf("tool message did not contain read result, got %q", message.Content)
			}
		}

		if message.Role == "system" && strings.Contains(message.Content, "README.md") && strings.Contains(message.Content, "Tool") {
			t.Fatalf("legacy tool result leaked through system role: %#v", message)
		}
	}

	if !sawToolMessage {
		t.Fatalf("expected legacy parsed tool result to use role=tool, messages=%#v", messages)
	}
}

func TestLegacyMalformedToolRequestUsesToolChannel(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	malformed := `{"tool":"edit_file","path":"README.md","old_text":`
	replies := []string{
		malformed,
		"Blocked by malformed request.",
	}

	type requestMessage struct {
		Role       string           `json:"role"`
		Content    string           `json:"content,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []llama.ToolCall `json:"tool_calls,omitempty"`
	}

	type requestPayload struct {
		Messages []requestMessage `json:"messages"`
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 4},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit README"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Blocked by malformed request." {
		t.Fatalf("unexpected answer %q", answer)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}

	messages := payloads[1].Messages

	var (
		assistantToolCallID string
		sawToolMessage      bool
	)

	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			assistantToolCallID = message.ToolCalls[0].ID
			if message.ToolCalls[0].Function.Name != "tool_parse" {
				t.Fatalf("malformed legacy request should be recorded as tool_parse, got %#v", message.ToolCalls[0])
			}

			var args map[string]string

			if err := json.Unmarshal([]byte(message.ToolCalls[0].Function.Arguments), &args); err != nil {
				t.Fatalf("tool_parse synthetic arguments should be valid JSON, got %q: %v", message.ToolCalls[0].Function.Arguments, err)
			}

			if !strings.Contains(args["raw"], "edit_file") {
				t.Fatalf("tool_parse synthetic arguments should retain raw malformed request, got %#v", args)
			}

			if message.Content != "" {
				t.Fatalf("legacy parse-failure assistant message should not preserve malformed content, got %q", message.Content)
			}
		}

		if message.Role == "tool" {
			sawToolMessage = true

			if assistantToolCallID == "" {
				t.Fatalf("tool message appeared before assistant tool_call: %#v", messages)
			}

			if message.ToolCallID != assistantToolCallID {
				t.Fatalf("tool_call_id = %q, want %q", message.ToolCallID, assistantToolCallID)
			}

			if !strings.Contains(message.Content, "Malformed tool request rejected") {
				t.Fatalf("tool message did not contain parse failure, got %q", message.Content)
			}
		}

		if message.Role == "system" && strings.Contains(message.Content, "edit_file") {
			t.Fatalf("malformed tool request leaked through system role: %#v", message)
		}
	}

	if !sawToolMessage {
		t.Fatalf("expected legacy parse failure to use role=tool, messages=%#v", messages)
	}
}

func TestRunnerInjectsInitialRuntimeBriefIntoModelMessages(t *testing.T) {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type requestPayload struct {
		Messages []chatMessage `json:"messages"`
	}

	var payload requestPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "update README",
			PermissionProfile: "read_only_workspace",
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "update readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Done." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	var sawBrief bool

	for _, msg := range payload.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[frontier step instruction]") && strings.Contains(msg.Content, `"objective":"update README"`) {
			sawBrief = true
			break
		}
	}

	if !sawBrief {
		t.Fatalf("expected initial runtime brief in request messages, got %#v", payload.Messages)
	}
}
