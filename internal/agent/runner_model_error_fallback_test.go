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

type modelErrorFallbackTestClient struct {
	err error
}

func (c modelErrorFallbackTestClient) CompleteChat(context.Context, []llama.ChatMessage, llama.ChatOptions) (string, error) {
	return "", c.err
}

func (c modelErrorFallbackTestClient) CompleteChatMessage(context.Context, []llama.ChatMessage, llama.ChatOptions) (llama.ChatMessage, error) {
	return llama.ChatMessage{}, c.err
}

func (c modelErrorFallbackTestClient) StreamChat(context.Context, []llama.ChatMessage, llama.ChatOptions, func(string) error) error {
	return c.err
}

func (c modelErrorFallbackTestClient) ListModels(context.Context) ([]llama.ModelInfo, error) {
	return []llama.ModelInfo{{ID: "model-error-fallback-test"}}, nil
}

func (c modelErrorFallbackTestClient) Backend() string { return "test" }

func (c modelErrorFallbackTestClient) SupportsNativeTools() bool { return false }

func TestModelErrorAfterReadOnlyEvidenceReturnsDeterministicFallback(t *testing.T) {
	runner := &Runner{
		Client: modelErrorFallbackTestClient{err: errors.New("chat request transport failed: unexpected EOF")},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "debug the resize issue and present findings",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read Model.renderMessages in internal/app/rendering.go lines 459-520",
				"459:func (m *Model) renderMessages() {",
				"460:\tm.messagesViewport.SetContent(rendered)",
				"461:}",
			}, "\n"),
		}},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "debug the resize issue and present findings"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("expected deterministic fallback instead of transport error, got %v", err)
	}
	for _, want := range []string{
		"<<QUESTION>>:",
		"concrete user-facing finding",
		"internal/app/rendering.go",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("fallback answer missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "Chat failed") || strings.Contains(answer, "budget was reached") {
		t.Fatalf("fallback leaked failure UI or budget framing:\n%s", answer)
	}
}

func TestModelErrorFallbackDoesNotSwallowCancellation(t *testing.T) {
	runner := &Runner{
		Client: modelErrorFallbackTestClient{err: context.Canceled},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "debug the resize issue and present findings",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Model.renderMessages in internal/app/rendering.go lines 459-520",
				"459:func (m *Model) renderMessages() {}",
			}, "\n"),
		}},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "debug the resize issue and present findings"}}, llama.ChatOptions{}, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation to propagate, got answer=%q err=%v", answer, err)
	}
	if strings.TrimSpace(answer) != "" {
		t.Fatalf("canceled run should not synthesize fallback answer: %q", answer)
	}
}
