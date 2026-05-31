package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestExtractAnswerProseFromReasoning(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains string
		wantNot      string
	}{
		{
			name: "answer with code fences stripped",
			input: "Let me think about this...\n\n" +
				"```\ncode block here\n```\n\n" +
				"The user asked about git operations in evidence. " +
				"Adding git operations like diff checks would help reduce churn " +
				"by validating intended changes before applying edits.\n\n" +
				"```\nanother code block\n```\n\n" +
				"final notes",
			wantContains: "The user asked about git operations in evidence",
		},
		{
			name: "tool call lines stripped",
			input: "Looking at the code...\n\n" +
				"<tool_call>read_file</tool_call>\n\n" +
				"The evidence struct is used for tracking tool usage. " +
				"Adding git operations to evidence would provide a useful " +
				"pre-edit validation layer for intended changes.",
			wantContains: "The evidence struct is used for tracking",
		},
		{
			name: "Qwen XML tool calls stripped",
			input: "Let me check the evidence struct.\n\n" +
				"<read_file path=\"internal/architect/packet.go\" start_line=\"1\" end_line=\"50\"/>\n\n" +
				"The evidence system should integrate git operations " +
				"because it would provide pre-edit validation. You could " +
				"add a git diff check before each write to reduce churn.",
			wantContains: "The evidence system should integrate git operations",
		},
		{
			name: "scores answer-like paragraph over monologue",
			input: "Now I need to understand the question about the label.\n\n" +
				"I should check the current implementation first.\n\n" +
				"The save label currently returns Save changes but " +
				"you could shorten it to just Save because the context " +
				"makes the meaning clear in all callers.\n\n" +
				"Let me also check git operations with evidence integration.",
			wantContains: "The save label currently returns Save changes",
			wantNot:      "Now I need to understand",
		},
		{
			name:         "empty reasoning",
			input:        "",
			wantContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAnswerProseFromReasoning(tt.input)
			if tt.wantContains == "" {
				if got != "" {
					t.Errorf("expected empty result, got %q", got)
				}
			} else if !strings.Contains(got, tt.wantContains) {
				t.Errorf("result does not contain expected text:\n  got:  %q\n  want contains: %q", got, tt.wantContains)
			}
			if tt.wantNot != "" && strings.Contains(got, tt.wantNot) {
				t.Errorf("result contains forbidden text %q:\n  got: %q", tt.wantNot, got)
			}
		})
	}
}

func TestSalvageAnswerFromReasoning(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		reasoning    string
		mutation     bool
		wantOk       bool
		wantContains string
	}{
		{
			name:    "empty content with substantial reasoning extracts answer",
			content: "",
			reasoning: "Let me think about this design question carefully...\n\n" +
				"I should examine the evidence structure first.\n\n" +
				"<read_file path=\"internal/architect/packet.go\" start_line=\"1\" end_line=\"50\"/>\n\n" +
				"The evidence system could benefit from integrating git operations " +
				"like diff checks to validate intended changes before applying edits. " +
				"You would reduce churn by catching unintended modifications early " +
				"and providing a clear audit trail for what the model intends to change. " +
				"However, you should keep it read-only so it does not add latency.",
			mutation:     false,
			wantOk:       true,
			wantContains: "evidence system could benefit",
		},
		{
			name:      "non-empty content skips salvage",
			content:   "Direct answer here.",
			reasoning: "Some reasoning text that is quite long and would otherwise be salvageable if content were empty",
			mutation:  false,
			wantOk:    false,
		},
		{
			name:      "short reasoning skipped",
			content:   "",
			reasoning: "short",
			mutation:  false,
			wantOk:    false,
		},
		{
			name:      "mutation lane skipped",
			content:   "",
			reasoning: "This is a substantial answer about git operations and how they could be integrated into the evidence system for better change tracking and validation in the repository. The approach would provide pre-edit safety checks.",
			mutation:  true,
			wantOk:    false,
		},
		{
			name:      "internal current state json skipped",
			content:   "",
			reasoning: `{"current_state":{"lane":"main","task_family":"conversation","contract":"direct_answer","allowed_tools":["context_pack","code_navigate"],"tool_manifest":[{"tool":"read_file"}]},"next_action_state":{"phase":"evidence","evidence_paths":["internal/app/model_state.go"]}}`,
			mutation:  false,
			wantOk:    false,
		},
		{
			name:    "adjacent internal json blocks skipped",
			content: "",
			reasoning: `{"current_state":{"lane":"main","task_family":"conversation"}}
{"tool_manifest":[{"tool":"read_file"}],"allowed_tools":["context_pack"]}`,
			mutation: false,
			wantOk:   false,
		},
		{
			name:      "labeled internal state block skipped",
			content:   "",
			reasoning: `current_state: {"lane":"main","task_family":"conversation","allowed_tools":["context_pack"]}`,
			mutation:  false,
			wantOk:    false,
		},
		{
			name:    "unfinished task-framing monologue skipped",
			content: "",
			reasoning: strings.Join([]string{
				"The user wants me to debug the TUI resize issue and provide structured evidence.",
				"",
				"I need to find the Update method and inspect how resize messages are handled before answering. I should call code_navigate on Model.Update and then compare it with the render path so I can identify the root cause accurately.",
			}, "\n"),
			mutation: false,
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &Runner{
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					PermissionProfile: func() string {
						if tt.mutation {
							return "read_write_workspace"
						}
						return "read_only_workspace"
					}(),
				},
			}
			reply := llama.ChatMessage{
				Content:          tt.content,
				ReasoningContent: tt.reasoning,
			}
			got, ok := runner.salvageAnswerFromReasoning(reply, nil)
			if ok != tt.wantOk {
				t.Errorf("salvageAnswerFromReasoning ok=%v, want=%v (got=%q)", ok, tt.wantOk, got)
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("salvageAnswerFromReasoning result missing %q:\n  got: %q", tt.wantContains, got)
			}
		})
	}
}

func TestSalvageAnswerFromReasoningRejectsUngroundedSourceCitation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "app"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	reply := llama.ChatMessage{
		ReasoningContent: strings.Join([]string{
			"Let me synthesize the answer from what I think I saw.",
			"",
			"The root cause is in `internal/app/conversation_view.go` lines 417-433 where `recalculateVisibleLines` resets `scrollY` after a resize. This means the terminal resize path is forcing the conversation viewport into an invalid state and the fix is to clamp the scroll position after resize.",
		}, "\n"),
	}

	if got, ok := runner.salvageAnswerFromReasoning(reply, nil); ok {
		t.Fatalf("hidden reasoning answer with nonexistent source citation should not be salvaged: %q", got)
	}
}

func TestNormalizeToolRequestSyntaxEmptyContentWithReasoning(t *testing.T) {
	msg := llama.ChatMessage{
		Content:          "",
		ReasoningContent: "substantial reasoning text here that has enough characters to be meaningful as an answer",
	}
	result := normalizeToolRequestSyntax(msg)
	if result.EventOutcome == toolRequestNormalizerOutcomeUnsafeToInfer {
		t.Fatalf("empty content must not classify as unsafe_to_infer after Fix 1")
	}
	if result.OK {
		t.Fatalf("empty content must not parse as a valid tool request")
	}
}

func TestLooksLikeQwenThinkingToolCall(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{`<read_file path="foo.go"/>`, true},
		{`<search_content pattern="git"/>`, true},
		{`<code_navigate capability="find_references" path="foo.go"/>`, true},
		{`</read_file>`, true},
		{`<context_pack task="wire cache"/>`, true},
		{`<submit_plan>`, true},
		{`The evidence struct is defined in`, false},
		{`  <not_a_tool_tag>`, false},
		{``, false},
	}
	for _, tt := range tests {
		got := looksLikeQwenThinkingToolCall(tt.line)
		if got != tt.want {
			t.Errorf("looksLikeQwenThinkingToolCall(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestLooksLikeInternalMonologue(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"Now I need to check the file", true},
		{"Let me look at the evidence struct", true},
		{"I should examine the code first", true},
		{"I think this is correct", true},
		{"I'll search for git operations", true},
		{"I'm going to read the source", true},
		{"I can see the struct definition", true},
		{"Looking at the implementation", true},
		{"First, let me understand the question", true},
		{"So the evidence system works like this", true},
		{"The evidence struct is defined in internal/architect/packet.go", false},
		{"Adding git operations would reduce churn", false},
		{"You could add a diff check before writes", false},
		{"", true},
	}
	for _, tt := range tests {
		got := looksLikeInternalMonologue(tt.line)
		if got != tt.want {
			t.Errorf("looksLikeInternalMonologue(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
