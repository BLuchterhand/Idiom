package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
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

func TestModelErrorAfterReadOnlyEvidenceDoesNotSynthesizeDeterministicFallback(t *testing.T) {
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
				"route_summary: Read Model.renderMessages in internal/surfaces/tui/app/rendering.go lines 459-520",
				"459:func (m *Model) renderMessages() {",
				"460:\tm.messagesViewport.SetContent(rendered)",
				"461:}",
			}, "\n"),
		}},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "debug the resize issue and present findings"}}, llama.ChatOptions{}, nil, nil, nil)
	if err == nil {
		t.Fatalf("model transport error must not be masked by host synthesis:\n%s", answer)
	}

	if strings.TrimSpace(answer) != "" {
		t.Fatalf("model transport error should not produce a host-authored answer: %q", answer)
	}
}

func TestModelErrorAfterReadOnlyMarkdownEvidenceDoesNotSynthesizeAnswer(t *testing.T) {
	runner := &Runner{
		Client: modelErrorFallbackTestClient{err: errors.New("chat request transport failed: unexpected EOF")},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What is the role of evidence in this harness? Where does it come from and what is it used for?",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 docs/idiom-strategic-thesis.md lines 1-320",
			Output: strings.Join([]string{
				"1:# Idiom Strategic Thesis",
				"5:Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
				"20:Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
				"21:The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
				"146:The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
				"287:Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
			}, "\n"),
		}},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}}, llama.ChatOptions{}, nil, nil, nil)
	if err == nil {
		t.Fatalf("model transport error must not be masked by host synthesis:\n%s", answer)
	}

	if strings.TrimSpace(answer) != "" {
		t.Fatalf("answerable evidence should remain evidence for the model, not host-authored prose: %q", answer)
	}
}

func TestModelErrorFallbackDoesNotCloseOutCandidateOnlyEvidence(t *testing.T) {
	runner := &Runner{
		Client: modelErrorFallbackTestClient{err: errors.New("chat request transport failed: unexpected EOF")},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses evidence.",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "context_pack",
			Summary: "Context packet produced candidate files",
			Output:  "Candidate files:\n- `docs/idiom-strategic-thesis.md`\n- `internal/runtime/agent/runner.go`\n",
		}},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}}, llama.ChatOptions{}, nil, nil, nil)
	if err == nil {
		t.Fatalf("candidate-only evidence must not produce host closeout answer:\n%s", answer)
	}

	if strings.TrimSpace(answer) != "" {
		t.Fatalf("candidate-only model error should not synthesize an answer: %q", answer)
	}
}

func TestModelErrorFallbackDoesNotOverrideMutationOrVerificationContracts(t *testing.T) {
	cases := []struct {
		name  string
		brief workerruntime.WorkerRuntimeBrief
	}{
		{
			name: "mutation",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation",
				PermissionProfile: "guarded_workspace_write",
				Objective:         "Update docs/idiom-strategic-thesis.md",
				RequiredEditPaths: []string{"docs/idiom-strategic-thesis.md"},
				AllowedEditPaths:  []string{"docs/idiom-strategic-thesis.md"},
			},
		},
		{
			name: "verification-required",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:           "conversation",
				Contract:             "direct_answer",
				PermissionProfile:    "read_only_workspace",
				Objective:            "What is the role of evidence in this harness?",
				VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			runner := &Runner{
				Client:       modelErrorFallbackTestClient{err: errors.New("chat request transport failed: unexpected EOF")},
				RuntimeBrief: tt.brief,
				SeedResults: []workspace.ToolResult{{
					Tool:    "read_file",
					Summary: "Read f1 docs/idiom-strategic-thesis.md lines 1-320",
					Output: strings.Join([]string{
						"20:Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
						"21:The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
					}, "\n"),
				}},
			}

			answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: tt.brief.Objective}}, llama.ChatOptions{}, nil, nil, nil)
			if err == nil {
				t.Fatalf("%s contract must not be masked by read-only closeout answer:\n%s", tt.name, answer)
			}

			if strings.TrimSpace(answer) != "" {
				t.Fatalf("%s contract should not synthesize read-only answer: %q", tt.name, answer)
			}
		})
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
				"route_summary: Read Model.renderMessages in internal/surfaces/tui/app/rendering.go lines 459-520",
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
