package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
)

// reasoningFixture is a single case for the parameterized DeepSeek
// reasoning_content history repair test. Each fixture is a JSON response
// body the test server returns; the test verifies that a follow-up call
// includes the expected reasoning_content in the history.
type reasoningFixture struct {
	name     string
	response string // server response JSON
	history  string // expected reasoning_content in follow-up assistant message
}

func TestDeepSeekReasoningContentPreservedInHistory(t *testing.T) {
	enableThinking := true
	fixtures := []reasoningFixture{
		{
			name:     "omitted",
			response: `{"choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "",
		},
		{
			name:     "empty",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "",
		},
		{
			name:     "normal",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"The user asked a simple question.","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "The user asked a simple question.",
		},
		{
			name:     "multiline",
			response: "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"Let me think about this.\\nFirst, I need to parse the request.\\nThen, I can formulate a response.\",\"content\":\"Hello\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}",
			history:  "Let me think about this.\nFirst, I need to parse the request.\nThen, I can formulate a response.",
		},
		{
			name:     "special_chars",
			response: "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"Special: \\\"quotes\\\" \\\\backslash\\\\ <tag> unicode: ñ ∑ ∞\",\"content\":\"Hello\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}",
			history:  "Special: \"quotes\" \\backslash\\ <tag> unicode: ñ ∑ ∞",
		},
		{
			name:     "truncated",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"This reasoning was cut off because the model hit","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "This reasoning was cut off because the model hit",
		},
		{
			name:     "long_reasoning",
			response: `{"choices":[{"message":{"role":"assistant","reasoning_content":"Step 1: Analyze. Step 2: Think deeper. Step 3: Consider alternatives. Step 4: Evaluate trade-offs. Step 5: Select best approach. Step 6: Verify correctness. Step 7: Prepare response.","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			history:  "Step 1: Analyze. Step 2: Think deeper. Step 3: Consider alternatives. Step 4: Evaluate trade-offs. Step 5: Select best approach. Step 6: Verify correctness. Step 7: Prepare response.",
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			var gotRequest map[string]any

			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++

				if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
					t.Fatalf("decode request: %v", err)
				}

				// First call returns the fixture response (with reasoning_content)
				// Second call simulates a follow-up so we can inspect history
				if callCount == 1 {
					_, _ = w.Write([]byte(tc.response))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"follow-up"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
				}
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

			reply, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			if err != nil {
				t.Fatalf("first chat call: %v", err)
			}

			// Verify the response preserved reasoning_content from the fixture
			if reply.ReasoningContent != tc.history {
				t.Fatalf("reasoning_content: got %q, want %q", reply.ReasoningContent, tc.history)
			}

			// Now issue a follow-up call that includes the prior assistant message.
			// DeepSeek requires reasoning_content to be preserved in history.
			_, err = client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "prior"},
				{ // This is the assistant reply we just received, going into history
					Role:             "assistant",
					Content:          reply.Content,
					ReasoningContent: reply.ReasoningContent,
				},
				{Role: "user", Content: "follow-up"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			if err != nil {
				t.Fatalf("follow-up chat call: %v", err)
			}

			// Verify the follow-up request preserved reasoning_content in
			// the assistant history message
			messages, ok := gotRequest["messages"].([]any)
			if !ok || len(messages) < 2 {
				t.Fatalf("expected messages in follow-up request, got %#v", gotRequest["messages"])
			}

			// Find the assistant message in the follow-up request history
			var foundAsst bool

			for _, raw := range messages {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				if msg["role"] == "assistant" {
					foundAsst = true

					gotRC, hasRC := msg["reasoning_content"]

					if tc.history == "" {
						// When there's no reasoning_content, DeepSeek may still
						// include the field as empty string; that's acceptable
						if hasRC && gotRC != "" {
							t.Fatalf("expected no reasoning_content (empty), got %q", gotRC)
						}
					} else {
						if !hasRC {
							t.Fatal("expected reasoning_content in follow-up assistant message, but field is missing")
						}

						if gotRC != tc.history {
							t.Fatalf("reasoning_content in history: got %q, want %q", gotRC, tc.history)
						}
					}

					break
				}
			}

			if !foundAsst {
				t.Fatal("no assistant message found in follow-up request messages")
			}
		})
	}
}

func TestDeepSeekReasoningContentStreamed(t *testing.T) {
	enableThinking := true
	fixtures := []reasoningFixture{
		{
			name:     "streamed_normal",
			response: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Thinking step by step\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
			history:  "",
		},
		{
			name:     "streamed_multiline",
			response: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Line one\\n\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Line two\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
			history:  "",
		},
		{
			name:     "streamed_empty_reasoning",
			response: "data: {\"choices\":[{\"delta\":{\"content\":\"No reasoning\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n",
			history:  "",
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			var gotRequest map[string]any

			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++

				if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
					t.Fatalf("decode request: %v", err)
				}

				if callCount == 1 {
					w.Header().Set("Content-Type", "text/event-stream")

					_, _ = w.Write([]byte(tc.response))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"follow-up"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
				}
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

			var streamedContent strings.Builder

			err := client.StreamChat(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			}, func(delta string) error {
				streamedContent.WriteString(delta)

				return nil
			})
			if err != nil {
				t.Fatalf("stream chat call: %v", err)
			}

			// Follow-up call to verify history
			_, err = client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "prior"},
				{Role: "assistant", Content: streamedContent.String(), ReasoningContent: ""},
				{Role: "user", Content: "follow-up"},
			}, ChatOptions{
				Model:          "deepseek-v4-flash",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			if err != nil {
				t.Fatalf("follow-up after stream: %v", err)
			}

			// Verify follow-up request preserves the assistant message
			messages, ok := gotRequest["messages"].([]any)
			if !ok {
				t.Fatalf("expected messages in follow-up request")
			}

			var foundAsst bool

			for _, raw := range messages {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				if msg["role"] == "assistant" {
					foundAsst = true
					// Streaming doesn't produce reasoning_content on the response,
					// so the history assistant message should not have it
					if rc, hasRC := msg["reasoning_content"]; hasRC && rc != "" {
						t.Fatalf("streamed assistant message should not have reasoning_content, got %q", rc)
					}

					break
				}
			}

			if !foundAsst {
				t.Fatal("no assistant message in follow-up request messages")
			}
		})
	}
}

func TestDeepSeekStripsReasoningOnNonAssistant(t *testing.T) {
	// Verify that non-assistant messages (user, system, tool) have their
	// reasoning_content stripped even for DeepSeek backend. Only assistant
	// messages should preserve it.
	enableThinking := true

	var gotRequest map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
		{Role: "system", Content: "be concise", ReasoningContent: "should be stripped"},
		{Role: "user", Content: "hello", ReasoningContent: "should be stripped too"},
		{Role: "assistant", Content: "prior answer", ReasoningContent: "preserved reasoning"},
	}, ChatOptions{
		Model:          "deepseek-v4-flash",
		EnableThinking: &enableThinking,
		MaxTokens:      32,
	})
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	messages, ok := gotRequest["messages"].([]any)
	if !ok || len(messages) < 3 {
		t.Fatalf("expected 3+ messages, got %#v", gotRequest["messages"])
	}

	tests := []struct {
		index int
		role  string
		want  string // expected reasoning_content value
	}{
		{0, "system", ""},                       // system: stripped
		{1, "user", ""},                         // user: stripped
		{2, "assistant", "preserved reasoning"}, // assistant: preserved
	}
	for _, tt := range tests {
		msg := messages[tt.index].(map[string]any)
		if msg["role"] != tt.role {
			t.Fatalf("msg[%d]: expected role %q, got %q", tt.index, tt.role, msg["role"])
		}

		gotRC, _ := msg["reasoning_content"].(string)
		if gotRC != tt.want {
			t.Fatalf("msg[%d] role=%s reasoning_content: got %q, want %q", tt.index, tt.role, gotRC, tt.want)
		}
	}
}

func TestNonDeepSeekBackendsStripReasoningContent(t *testing.T) {
	// Verify that non-DeepSeek hosted backends (ZAI/GLM, MiniMax) continue to
	// strip reasoning_content from all messages. These go through the
	// hostedChatMessages codepath.
	//
	// Local backends (llama, MLX) use a separate codepath (chatRequest
	// directly, not hostedChatMessages) and preserve reasoning_content
	// as-received — that is existing, expected behavior and is not changed
	// by A3.
	enableThinking := true
	backends := []struct {
		name    string
		backend string
	}{
		{"zai", config.BackendZAI},
		{"glm", config.BackendGLM},
		{"minimax", config.BackendMiniMax},
	}

	for _, bc := range backends {
		t.Run(bc.name, func(t *testing.T) {
			var gotRequest map[string]any

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
					t.Fatalf("decode request: %v", err)
				}

				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(bc.backend)

			_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi", ReasoningContent: "should be stripped"},
				{Role: "assistant", Content: "prior", ReasoningContent: "should also be stripped"},
			}, ChatOptions{
				Model:          bc.backend + "-model",
				EnableThinking: &enableThinking,
				MaxTokens:      32,
			})
			if err != nil {
				t.Fatalf("CompleteChatMessage: %v", err)
			}

			messages, ok := gotRequest["messages"].([]any)
			if !ok || len(messages) < 2 {
				t.Fatalf("expected at least 2 messages in request, got %#v", gotRequest["messages"])
			}

			// Non-DeepSeek hosted backends must NOT preserve reasoning_content
			for _, raw := range messages {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				if rc, hasRC := msg["reasoning_content"]; hasRC && rc != "" {
					t.Fatalf("non-DeepSeek hosted backend %s should strip reasoning_content, got %q on role=%s",
						bc.name, rc, msg["role"])
				}
			}
		})
	}
}

func TestDeepSeekPreservesReasoningOnToolCallAssistant(t *testing.T) {
	// Assistant messages with tool_calls and reasoning_content should
	// preserve reasoning_content for DeepSeek
	enableThinking := true

	var gotRequest map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))

	defer server.Close()

	client := NewClient(server.URL, time.Second).WithBackend(config.BackendDeepSeek)

	_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "I should call read_file",
			ToolCalls: []ToolCall{
				{ID: "call_1", Function: ToolCallFunc{Name: "read_file", Arguments: `{"path":"x"}`}},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}, ChatOptions{
		Model:          "deepseek-v4-flash",
		EnableThinking: &enableThinking,
		MaxTokens:      32,
	})
	if err != nil {
		t.Fatalf("CompleteChatMessage: %v", err)
	}

	messages, ok := gotRequest["messages"].([]any)
	if !ok || len(messages) < 3 {
		t.Fatalf("expected 3+ messages, got %#v", gotRequest["messages"])
	}

	// The assistant message at index 1 should have reasoning_content preserved
	asstMsg := messages[1].(map[string]any)
	if asstMsg["role"] != "assistant" {
		t.Fatalf("expected role assistant at index 1, got %q", asstMsg["role"])
	}

	rc, hasRC := asstMsg["reasoning_content"]
	if !hasRC {
		t.Fatal("assistant with tool_calls should preserve reasoning_content for DeepSeek, but field is missing")
	}

	if rc != "I should call read_file" {
		t.Fatalf("reasoning_content on tool-call assistant: got %q, want %q", rc, "I should call read_file")
	}
}

func TestLocalBackendsPreserveReasoningContentUnchanged(t *testing.T) {
	// Local backends (llama, MLX) use the chatRequest code path which does
	// NOT strip reasoning_content. This is existing behavior and must remain
	// unchanged by A3.
	for _, bc := range []struct {
		name    string
		backend string
	}{
		{"llama", config.BackendLlama},
		{"mlx", config.BackendMLX},
	} {
		t.Run(bc.name, func(t *testing.T) {
			var gotRequest map[string]any

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
					t.Fatalf("decode request: %v", err)
				}

				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))

			defer server.Close()

			client := NewClient(server.URL, time.Second).WithBackend(bc.backend)

			_, err := client.CompleteChatMessage(context.Background(), []ChatMessage{
				{Role: "user", Content: "hi", ReasoningContent: "preserved"},
				{Role: "assistant", Content: "prior", ReasoningContent: "also preserved"},
			}, ChatOptions{
				Model:     bc.backend + "-model",
				MaxTokens: 32,
			})
			if err != nil {
				t.Fatalf("CompleteChatMessage: %v", err)
			}

			messages, ok := gotRequest["messages"].([]any)
			if !ok || len(messages) < 2 {
				t.Fatalf("expected 2+ messages in request, got %#v", gotRequest["messages"])
			}

			// Local backends must preserve reasoning_content as-is
			for _, raw := range messages {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				role, _ := msg["role"].(string)
				rc, hasRC := msg["reasoning_content"]

				if !hasRC {
					t.Fatalf("local backend %s should preserve reasoning_content on role=%s, but field is missing",
						bc.name, role)
				}

				if rc == "" {
					t.Fatalf("local backend %s should preserve reasoning_content on role=%s, got empty",
						bc.name, role)
				}
			}
		})
	}
}
