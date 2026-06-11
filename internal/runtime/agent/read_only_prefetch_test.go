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

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestReadOnlyNamedPathPrefetchEventGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("prefetch fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	base := func() *Runner {
		return &Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
				Contract:          string(workerruntime.ContractArchitecture),
				Objective:         "Read README.md and summarize it.",
				PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			},
		}
	}

	tests := []struct {
		name             string
		mutate           func(*Runner)
		collectedResults []workspace.ToolResult
		envDisabled      bool
		wantEligible     bool
		wantMissReason   string
	}{
		{
			name:         "eligible top-level read-only",
			wantEligible: true,
		},
		{
			name: "delegated child",
			mutate: func(r *Runner) {
				r.AgentName = "child:explorer"
			},
			wantMissReason: "delegated_child_agent",
		},
		{
			name:           "disabled by env",
			envDisabled:    true,
			wantMissReason: "disabled_by_env",
		},
		{
			name: "existing evidence",
			collectedResults: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read README.md"},
			},
			wantMissReason: "existing_evidence_present",
		},
		{
			name: "write permission",
			mutate: func(r *Runner) {
				r.RuntimeBrief.PermissionProfile = string(workerruntime.PermissionGuardedWorkspaceWrite)
			},
			wantMissReason: "not_read_only_workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := base()
			if tt.mutate != nil {
				tt.mutate(runner)
			}
			if tt.envDisabled {
				t.Setenv("IDIOM_DISABLE_READONLY_PREFETCH", "1")
			}

			event := runner.readOnlyNamedPathPrefetchEvent(tt.collectedResults)
			if event.Eligible != tt.wantEligible || event.MissReason != tt.wantMissReason {
				t.Fatalf("event = %#v, want eligible=%v miss=%q", event, tt.wantEligible, tt.wantMissReason)
			}
		})
	}
}

func TestRunnerTruncatedPrefetchCanAnswerWithoutReadFileFollowup(t *testing.T) {
	root := t.TempDir()
	body := "The large fixture uses prefetch excerpts to expose answer token.\n" + strings.Repeat("padding\n", 2000)
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": "The large fixture uses prefetch excerpts to expose answer token. Source: large.md.",
				},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Read large.md and explain what the large fixture uses.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}

	var prefetchEvent *readOnlyPrefetchEvent
	var readFileResults int
	runner.OnEvent = func(event RunEvent) {
		if event.Role != readOnlyPrefetchEventRole {
			return
		}
		var payload readOnlyPrefetchEvent
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			prefetchEvent = &payload
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == workspace.ToolNameReadFile {
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "prefetch excerpts") || !strings.Contains(answer, "large.md") {
		t.Fatalf("expected grounded answer from prefetched excerpt, got %q", answer)
	}
	if prefetchEvent == nil || !prefetchEvent.Eligible || prefetchEvent.TruncatedFiles != 1 {
		t.Fatalf("expected eligible truncated prefetch event, got %#v", prefetchEvent)
	}
	if readFileResults != 0 {
		t.Fatalf("expected no model-initiated read_file follow-up, got %d", readFileResults)
	}
	if requests != 0 {
		t.Fatalf("expected prefetch closeout before model request, got %d requests", requests)
	}
}

func TestRunnerPrefetchHostCloseoutSkipsModelToolRetry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "labels"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	source := "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "labels", "labels.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": `{"tool":"code_navigate","capability":"find_references","path":"internal/labels/labels.go","pattern":"ShortLabel"}`,
				},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Can we shorten the save label in internal/labels/labels.go? Answer the question from the codebase. Do not edit files.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "code_navigate", "search_content"},
		},
	}

	var events []RunEvent
	runner.OnEvent = func(event RunEvent) {
		events = append(events, event)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected prefetch closeout before model call, got %d requests", requests)
	}
	if !strings.Contains(answer, "Save changes") || !strings.Contains(answer, "internal/labels/labels.go") {
		t.Fatalf("expected grounded prefetch closeout answer, got:\n%s", answer)
	}
	for _, event := range events {
		if event.Role == "tool_result" && strings.Contains(event.Content, "code_navigate") {
			t.Fatalf("prefetch closeout should not execute model retry, saw event %#v", event)
		}
	}
}

func TestReadOnlyNamedPathPrefetchNoPathMissIsReported(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Summarize the relevant behavior.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
	}

	var events []RunEvent
	runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			events = append(events, event)
		}
	}
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	recorder := runnerTurnRecorder{
		runner:           runner,
		collectedResults: &collected,
		turnMessages:     &turnMessages,
	}

	got := runner.maybeApplyReadOnlyNamedPathPrefetch(recorder, collected)
	if len(got) != 0 {
		t.Fatalf("expected no prefetched results, got %#v", got)
	}
	if len(events) != 1 {
		t.Fatalf("expected one prefetch event, got %#v", events)
	}
	var payload readOnlyPrefetchEvent
	if err := json.Unmarshal([]byte(events[0].Content), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if payload.Eligible || payload.MissReason != "no_explicit_named_paths" {
		t.Fatalf("unexpected no-path event: %#v", payload)
	}
}
