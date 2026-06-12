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

func TestDirectAnswerSurfaceForBriefFiltersProtocolAndMutationTools(t *testing.T) {
	surface, ok := DirectAnswerSurfaceForBrief(testDirectAnswerSurfaceBrief())
	if !ok {
		t.Fatal("expected direct-answer surface")
	}

	tools := map[string]bool{}

	for _, tool := range surface.ToolNames {
		if strings.TrimSpace(tool) == "" {
			t.Fatalf("surface contains blank tool name: %#v", surface.ToolNames)
		}

		if tools[tool] {
			t.Fatalf("surface contains duplicate tool %q: %#v", tool, surface.ToolNames)
		}

		tools[tool] = true
	}

	for _, tool := range workspace.WorkspaceWriteToolNames() {
		if tools[tool] {
			t.Fatalf("direct-answer surface exposed write tool %q", tool)
		}
	}

	for _, tool := range []string{"submit_plan", "task_ledger", "note", "shell"} {
		if tools[tool] {
			t.Fatalf("direct-answer surface exposed protocol tool %q", tool)
		}
	}

	for _, tool := range []string{"read_file", "search_content", "context_pack", "code_navigate", "harness_audit", "task"} {
		if !tools[tool] {
			t.Fatalf("direct-answer surface did not expose read-only investigation tool %q; got %#v", tool, surface.ToolNames)
		}
	}

	if !DirectAnswerSurfaceSupportsReadOnlyInvestigation(surface) {
		t.Fatal("direct-answer surface should advertise read-only investigation")
	}
}

func TestDirectAnswerSurfaceNotReturnedForWriteBrief(t *testing.T) {
	brief := testDirectAnswerSurfaceBrief()

	brief.PermissionProfile = string(workerruntime.PermissionGuardedWorkspaceWrite)

	if _, ok := DirectAnswerSurfaceForBrief(brief); ok {
		t.Fatal("write-capable direct-answer brief must not receive lightweight direct-answer surface")
	}

	brief = testDirectAnswerSurfaceBrief()
	brief.TaskFamily = string(workerruntime.TaskFamilyImplementation)

	if _, ok := DirectAnswerSurfaceForBrief(brief); ok {
		t.Fatal("non-direct-answer task family must not receive lightweight direct-answer surface")
	}
}

func TestDirectAnswerSurfacePrefetchEligibilityTypedMisses(t *testing.T) {
	tests := []struct {
		name       string
		surface    DirectAnswerSurface
		wantPrefix string
	}{
		{
			name:       "missing",
			surface:    DirectAnswerSurface{},
			wantPrefix: "direct_answer_surface_missing",
		},
		{
			name: "stale",
			surface: DirectAnswerSurface{
				SchemaVersion: DirectAnswerSurfaceSchemaVersion,
				Version:       "old",
				Capabilities:  []string{DirectAnswerSurfaceCapSourceBodyPrefetch},
			},
			wantPrefix: "direct_answer_surface_stale:",
		},
		{
			name: "capability absent",
			surface: DirectAnswerSurface{
				SchemaVersion: DirectAnswerSurfaceSchemaVersion,
				Version:       DirectAnswerSurfaceVersion,
				Capabilities:  []string{DirectAnswerSurfaceCapReadOnlyInvestigation},
			},
			wantPrefix: "direct_answer_surface_no_source_body_prefetch_capability",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eligible, missReason := DirectAnswerSurfacePrefetchEligibility(tt.surface)
			if eligible {
				t.Fatal("prefetch should not be eligible")
			}

			if !strings.HasPrefix(missReason, tt.wantPrefix) {
				t.Fatalf("miss reason = %q, want prefix %q", missReason, tt.wantPrefix)
			}
		})
	}
}

func TestDirectAnswerSurfacePrefetchEligibilityRequiresAdvertisedCapability(t *testing.T) {
	surface := DirectAnswerSurface{
		SchemaVersion: DirectAnswerSurfaceSchemaVersion,
		Version:       DirectAnswerSurfaceVersion,
		Capabilities: []string{
			DirectAnswerSurfaceCapReadOnlyInvestigation,
			DirectAnswerSurfaceCapSourceBodyPrefetch,
		},
	}

	eligible, missReason := DirectAnswerSurfacePrefetchEligibility(surface)
	if !eligible {
		t.Fatalf("prefetch should be eligible when surface advertises capability, miss reason %q", missReason)
	}

	if missReason != "" {
		t.Fatalf("eligible surface returned miss reason %q", missReason)
	}
}

func TestEmitDirectAnswerSurfaceEvent(t *testing.T) {
	surface, ok := DirectAnswerSurfaceForBrief(testDirectAnswerSurfaceBrief())
	if !ok {
		t.Fatal("expected direct-answer surface")
	}

	var got []RunEvent

	EmitDirectAnswerSurfaceEvent(func(event RunEvent) {
		got = append(got, event)
	}, surface)

	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}

	if got[0].Role != "direct_answer_surface" {
		t.Fatalf("event role = %q, want direct_answer_surface", got[0].Role)
	}

	var payload struct {
		SchemaVersion  string   `json:"schema_version"`
		Version        string   `json:"version"`
		Capabilities   []string `json:"capabilities"`
		ToolCount      int      `json:"tool_count"`
		TerminalStates []string `json:"terminal_states"`
	}

	if err := json.Unmarshal([]byte(got[0].Content), &payload); err != nil {
		t.Fatalf("unmarshal surface event: %v", err)
	}

	if payload.SchemaVersion != DirectAnswerSurfaceSchemaVersion {
		t.Fatalf("schema version = %q", payload.SchemaVersion)
	}

	if payload.Version != DirectAnswerSurfaceVersion {
		t.Fatalf("version = %q", payload.Version)
	}

	if payload.ToolCount != len(surface.ToolNames) {
		t.Fatalf("tool count = %d, want %d", payload.ToolCount, len(surface.ToolNames))
	}

	if len(payload.TerminalStates) == 0 {
		t.Fatal("expected terminal states in surface event")
	}
}

func TestRunnerDirectAnswerSurfacePrefetchesNamedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom is a local coding-agent harness.\n"), 0o644); err != nil {
		t.Fatalf("write README fixture: %v", err)
	}

	replies := []string{
		"Idiom is a local coding-agent harness. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "Read README.md and answer what Idiom is.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "write_file", "submit_plan"},
		},
	}

	var (
		surfaceEvent    RunEvent
		prefetchEvents  []RunEvent
		readFileResults int
	)

	runner.OnEvent = func(event RunEvent) {
		if event.Role == "direct_answer_surface" {
			surfaceEvent = event
		}

		if strings.Contains(event.Role, "prefetch") {
			prefetchEvents = append(prefetchEvents, event)
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "read_file" {
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom is a local coding-agent harness") {
		t.Fatalf("expected grounded README answer citing source and fixture content, got %q", answer)
	}

	if surfaceEvent.Role == "" {
		t.Fatal("runner did not emit direct_answer_surface event")
	}

	var payload struct {
		Capabilities []string `json:"capabilities"`
	}

	if err := json.Unmarshal([]byte(surfaceEvent.Content), &payload); err != nil {
		t.Fatalf("unmarshal direct-answer surface event: %v", err)
	}

	if !slicesContainsString(payload.Capabilities, DirectAnswerSurfaceCapSourceBodyPrefetch) {
		t.Fatalf("runner surface did not advertise source_body_prefetch: %#v", payload.Capabilities)
	}

	if len(prefetchEvents) != 1 {
		t.Fatalf("expected one prefetch event, got %#v", prefetchEvents)
	}

	if readFileResults != 0 {
		t.Fatalf("prefetch should avoid model-initiated read_file before final answer, got %d", readFileResults)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestRunnerNamedPathPrefetchDoesNotRunForWriteBrief(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom is a local coding-agent harness.\n"), 0o644); err != nil {
		t.Fatalf("write README fixture: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Done."}}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyImplementation),
			Contract:          string(workerruntime.ContractImplementationResult),
			Objective:         "Read README.md and update it.",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			AllowedTools:      []string{"read_file", "edit_file", "write_file"},
		},
	}

	var (
		prefetchResults int
		prefetchEvent   *RunEvent
	)

	runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			copy := event

			prefetchEvent = &copy
		}
	}
	_, _ = runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == workspace.ToolNamePrefetch {
				prefetchResults++
			}
		},
	)

	if prefetchResults != 0 {
		t.Fatalf("write brief must not receive prefetch result, got %d", prefetchResults)
	}

	if prefetchEvent == nil {
		t.Fatal("expected metadata prefetch event")
	}

	var payload struct {
		Eligible   bool   `json:"eligible"`
		MissReason string `json:"miss_reason"`
	}

	if err := json.Unmarshal([]byte(prefetchEvent.Content), &payload); err != nil {
		t.Fatalf("unmarshal prefetch event: %v", err)
	}

	if payload.Eligible || payload.MissReason != "not_read_only_workspace" {
		t.Fatalf("unexpected prefetch metadata: %#v", payload)
	}

	if requests == 0 {
		t.Fatal("expected the runner to reach the model")
	}
}

func TestRunnerNamedPathPrefetchRunsForReadOnlyArchitectureBrief(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom carries cache metrics through session summaries.\n"), 0o644); err != nil {
		t.Fatalf("write README fixture: %v", err)
	}

	replies := []string{
		"README.md shows that Idiom carries cache metrics through session summaries and uses that reporting flow in this fixture. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
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
			Objective:         "Read README.md and explain how cache metrics flow.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}

	var (
		prefetchEvent   *RunEvent
		prefetchResults int
		readFileResults int
	)

	runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			copy := event

			prefetchEvent = &copy
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			switch event.Result.Tool {
			case workspace.ToolNamePrefetch:
				prefetchResults++
			case "read_file":
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom carries cache metrics") {
		t.Fatalf("expected grounded README answer citing source and fixture content, got %q", answer)
	}

	if prefetchEvent == nil {
		t.Fatal("expected prefetch metadata event")
	}

	var payload struct {
		Eligible      bool     `json:"eligible"`
		SelectedPaths []string `json:"selected_paths"`
	}

	if err := json.Unmarshal([]byte(prefetchEvent.Content), &payload); err != nil {
		t.Fatalf("unmarshal prefetch event: %v", err)
	}

	if !payload.Eligible || !slicesContainsString(payload.SelectedPaths, "README.md") {
		t.Fatalf("unexpected prefetch metadata: %#v", payload)
	}

	if prefetchResults != 1 {
		t.Fatalf("expected one prefetch result, got %d", prefetchResults)
	}

	if readFileResults != 0 {
		t.Fatalf("prefetch should avoid model-initiated read_file before final answer, got %d", readFileResults)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestRunnerDirectAnswerSurfaceAllowsSearchThenBoundedRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom sells repo memory, freshness, contracts, audit, and evals.\n"), 0o644); err != nil {
		t.Fatalf("write README fixture: %v", err)
	}

	replies := []string{
		`{"tool":"search_content","path":".","pattern":"repo memory"}`,
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":1}`,
		"Idiom sells repo memory, freshness, contracts, audit, and evals. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "Find the repo-memory claim and answer what Idiom sells.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"search_content", "read_file", "write_file", "submit_plan"},
		},
	}

	var (
		searchResults int
		readResults   int
	)

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Error != "" {
				return
			}

			switch event.Result.Tool {
			case "search_content":
				searchResults++
			case "read_file":
				readResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if searchResults != 1 || readResults != 1 {
		t.Fatalf("expected one search_content and one read_file result, got search=%d read=%d", searchResults, readResults)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom sells repo memory") {
		t.Fatalf("expected final answer grounded in README.md, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func testDirectAnswerSurfaceBrief() workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}
}
