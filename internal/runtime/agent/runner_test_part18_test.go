package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestDelegatedReadOnlyChildModelAnswersAfterAnswerReadyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/runtime/agent: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte(strings.Join([]string{
		"package agent",
		"",
		"func dispatch() {",
		"\t// Initial orientation only; the delegated mechanism is elsewhere.",
		"}",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner_child_tasks.go"), []byte(strings.Join([]string{
		"package agent",
		"",
		"func runChildTask() {",
		"\t// The task tool launches a subagent child runner for delegated read-only evidence.",
		"}",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write runner_child_tasks.go: %v", err)
	}

	replies := []string{
		`{"tool":"search_files","pattern":"runner_child_tasks","path":"internal/runtime/agent"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner_child_tasks.go","start_line":1,"end_line":20}`,
		"The task tool launches a subagent child runner for delegated read-only evidence in internal/runtime/agent/runner_child_tasks.go. Confidence: high.",
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

	var roles []string

	runner := &Runner{
		AgentName: "child:explorer",
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDelegatedReadOnly),
			Contract:          string(workerruntime.ContractGroundedEvidence),
			Objective:         "Can you tell me how this harness uses subagents?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "read_file"},
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can you tell me how this harness uses subagents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 3 {
		t.Fatalf("expected child model synthesis after readiness, used %d requests; roles=%v answer=%q", requests, roles, answer)
	}

	for _, want := range []string{"task", "subagent", "internal/runtime/agent/runner_child_tasks.go"} {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
			t.Fatalf("expected answer to contain %q, got:\n%s", want, answer)
		}
	}

	if !slices.Contains(roles, "delegated_read_only_answer_ready") {
		t.Fatalf("expected delegated readiness event before host closeout, got roles %v", roles)
	}
}

// TestArchitectureLaneChildDiscoveryDoesNotBlockReadAfterDiscoveryQuery
// reproduces the 2026-05-30 regression where the architecture lane delegated
// to a child explorer, the child ran discovery_query, and then all subsequent
// read_file calls were blocked because readiness fired prematurely.
// The fix requires at least one source body read after discovery_query before
// declaring readiness.
// TestChildDiscoveryReadAllowedAfterDiscoveryQuery verifies that
// after a discovery_query produces a composite packet, the child explorer
// is still allowed to read source files before readiness fires.
// Regression test for the 2026-05-30 incident where readiness was declared
// immediately after discovery_query, blocking all subsequent reads.
func TestChildDiscoveryReadAllowedAfterDiscoveryQuery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "runtime", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "internal", "runtime", "agent", "runner.go"), []byte("package agent\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"discovery_query","purpose":"architecture_boundary","path":".","pattern":"runner"}`,
		`{"tool":"read_file","path":"internal/runtime/agent/runner.go"}`,
		"`internal/runtime/agent/runner.go` contains the `Run` function.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d (max %d)", requests, len(replies))
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
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain what internal/runtime/agent/runner.go contains.",
			AllowedTools:      []string{"read_file", "search_content", "search_files"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain what internal/runtime/agent/runner.go contains."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The read_file should NOT be blocked by premature readiness after
	// discovery_query. The answer should reflect the source read.
	if !strings.Contains(answer, "runner.go") && !strings.Contains(answer, "Run function") {
		t.Fatalf("expected answer to reference runner.go or Run function, got: %q", answer)
	}

	// The child should have used all 3 replies (discovery + read + answer).
	if requests < 3 {
		t.Fatalf("expected at least 3 model calls, got %d (child may have been blocked prematurely)", requests)
	}
}

func TestReadOnlyAnalysisAnswerReadyAfterExactArchitectureProbes(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found subagent flow.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:16 runChildTask\ninternal/capabilities/workspace/tool_schemas.go:85 task"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runWorkbranchChildTask in internal/runtime/agent/runner_child_tasks.go lines 326-422",
			Output:  "func (r *Runner) runWorkbranchChildTask(...) {}",
		},
	}
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.\nKey terms: child task, runner",
			PermissionProfile: "read_only_workspace",
		},
	}

	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "go_read_symbol"}, results[2], results) {
		t.Fatalf("expected architecture lane to become answer-ready after exact semantic probes")
	}
}

func TestEmitDelegatedReadOnlyAnswerReadyEventCanonicalizesEvidencePaths(t *testing.T) {
	var events []RunEvent

	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Discovery query complete",
		Output:  `{"artifact_path":".codex/discovery/impact-analysis.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"runner.go:648 runChildTask\ninternal/runtime/agent/runner_child_tasks.go:16 runChildTask\ninternal/runtime/agent/answer_composer.go:73 synthesizeDelegatedReadOnlyAnswer"}]}`,
	}}
	runner.emitDelegatedReadOnlyAnswerReadyEvent(workspace.ToolRequest{Tool: "discovery_query"}, results)

	if len(events) != 1 {
		t.Fatalf("expected one answer-ready event, got %d", len(events))
	}

	var payload struct {
		EvidencePaths []string `json:"evidence_paths"`
	}

	if err := json.Unmarshal([]byte(events[0].Content), &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}

	for _, want := range []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/answer_composer.go",
	} {
		if !slices.Contains(payload.EvidencePaths, want) {
			t.Fatalf("expected answer-ready event to include %q, got %#v", want, payload.EvidencePaths)
		}
	}

	for _, forbidden := range []string{"runner.go", "runner_child_tasks.go"} {
		if slices.Contains(payload.EvidencePaths, forbidden) {
			t.Fatalf("expected answer-ready event to avoid bare duplicate path %q, got %#v", forbidden, payload.EvidencePaths)
		}
	}
}

func TestAugmentDelegatedReadOnlyPacketPreservesAdditionalPaths(t *testing.T) {
	answer := "Question: Explain how subagents work.\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/runtime/agent/runner_child_tasks.go`\n"
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/runtime/agent/runner.go:648 runChildTask\ninternal/runtime/agent/runner_child_tasks.go:16 runChildTask\ninternal/runtime/agent/answer_composer.go:18 Compose"}]}`,
		},
	}
	augmented := augmentDelegatedReadOnlyPacket(answer, "Explain how subagents work.", results)

	for _, want := range []string{
		"Additional inspected paths:",
		"`internal/runtime/agent/runner.go`",
		"`internal/runtime/agent/answer_composer.go`",
		"Exact probes already inspected:",
		"`runChildTask`",
	} {
		if !strings.Contains(augmented, want) {
			t.Fatalf("expected augmented delegated packet to contain %q, got %q", want, augmented)
		}
	}
}

func TestAugmentDelegatedReadOnlyPacketDoesNotInjectDiscoveryUsefulnessExpectedPaths(t *testing.T) {
	answer := "For `What can we add to our discovery tools in an effort at increasing their overall usefulness?`, this is grounded in `internal/capabilities/workspace/discovery_query.go`, `internal/runtime/agent/runner.go`."
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/capabilities/workspace/discovery_query.go:338: func runDiscoveryQuery\ninternal/runtime/agent/runner.go:648: runChildTask"}]}`,
		},
	}
	augmented := augmentDelegatedReadOnlyPacket(answer, "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)

	for _, forbidden := range []string{
		"Additional inspected paths:",
		"`internal/runtime/agent/answer_composer.go`",
		"`internal/runtime/agent/runner_lane_policy.go`",
	} {
		if strings.Contains(augmented, forbidden) {
			t.Fatalf("discovery usefulness augmentation should not inject topic-specific path %q, got %q", forbidden, augmented)
		}
	}

	for _, want := range []string{"`internal/capabilities/workspace/discovery_query.go`", "`internal/runtime/agent/runner.go`"} {
		if !strings.Contains(augmented, want) {
			t.Fatalf("expected augmented discovery packet to preserve actual evidence path %q, got %q", want, augmented)
		}
	}
}

func TestEmitAcceptedAnswerSignalsSanitizesEvidencePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:       "architecture",
			Contract:   "architecture_assessment",
			TaskFamily: "architecture",
			Objective:  "Explain how this harness uses subagents.",
		},
	}

	var readiness map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}

		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}
	runner.emitAcceptedAnswerSignals(
		"The harness delegates through `internal/runtime/agent/runner.go` and `internal/runtime/agent/runner_child_tasks.go` rather than `symbol/body/branch` markers or `.idiom/discovery/queries/query.json` artifacts.",
		[]workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/runtime/agent/runner.go`\n- `.idiom/discovery/queries/query.json`\n- `internal/runtime/agent/runner_child_tasks.go`\n",
		}},
	)

	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))

	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}

	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected sanitized readiness paths to contain %q, got %#v", want, got)
		}
	}

	for _, forbidden := range []string{".idiom/discovery/queries/query.json", "symbol/body/branch"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect sanitized readiness paths to retain %q, got %#v", forbidden, got)
		}
	}
}

func TestEmitAcceptedAnswerSignalsUsesTypedPacketEvidencePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:       "architecture",
			Contract:   "architecture_assessment",
			TaskFamily: "architecture",
			Objective:  "Explain how this harness uses subagents.",
		},
	}

	var readiness map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}

		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}

	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       runner.RuntimeBrief.Objective,
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go", ".idiom/discovery/queries/query.json", "internal/runtime/agent/runner_child_tasks.go"},
		Findings:        []ReadOnlyEvidenceFinding{{Path: "internal/runtime/agent/runner.go", Text: "The runner delegates subagent work via the task tool."}},
		Confidence:      "high",
	})
	runner.emitAcceptedAnswerSignals("The harness delegates via task.", []workspace.ToolResult{result})

	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))

	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}

	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected typed readiness paths to contain %q, got %#v", want, got)
		}
	}

	if slices.Contains(got, ".idiom/discovery/queries/query.json") {
		t.Fatalf("did not expect typed readiness to retain artifact path, got %#v", got)
	}
}

func TestEmitAcceptedAnswerSignalsPrefersExactProbePathsOverNoisyPinnedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "architecture",
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			Objective:         "Explain how this harness uses subagents.",
			PermissionProfile: "read_only_workspace",
		},
	}

	var readiness map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}

		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}
	runner.emitAcceptedAnswerSignals(
		"`Runner.runChildTask` creates the bounded child runner in `internal/runtime/agent/runner_child_tasks.go` and returns the child result to the parent as evidence.",
		[]workspace.ToolResult{
			{
				Tool:    "task",
				Summary: "Child task completed",
				Output:  "Primary files: `internal/runtime/agent/instructions.go`, `internal/runtime/agent/modes.go`.",
			},
			{
				Tool:    "go_read_symbol",
				Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
				Output:  "func (r *Runner) runChildTask(...) workspace.ToolResult { return workspace.ToolResult{} }",
			},
		},
	)

	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))

	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}

	if !slices.Contains(got, "internal/runtime/agent/runner_child_tasks.go") {
		t.Fatalf("expected exact probe path in readiness paths, got %#v", got)
	}

	for _, forbidden := range []string{"internal/runtime/agent/instructions.go", "internal/runtime/agent/modes.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect noisy pinned path %q in readiness paths, got %#v", forbidden, got)
		}
	}
}

func TestEmitAcceptedAnswerSignalsDirectAnswerAliasBlocksUnrelatedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.",
		},
	}
	events := map[string]map[string]any{}

	runner.OnEvent = func(event RunEvent) {
		switch event.Role {
		case "readiness_check", "final_answer_critic":
			var fields map[string]any

			_ = json.Unmarshal([]byte(event.Content), &fields)
			events[event.Role] = fields
		}
	}
	runner.emitAcceptedAnswerSignals(
		"The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display.",
		[]workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "Context pack wrote packet",
				Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
			},
			{
				Tool:    "read_file",
				Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
	)

	readiness := events["readiness_check"]
	if readiness["acceptance_contract"] != finalAnswerAcceptanceSchema {
		t.Fatalf("expected acceptance contract field, got %#v", readiness)
	}

	if readiness["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		readiness["action"] != "block" ||
		readiness["blocker_kind"] != "answer_objective_unmet" ||
		readiness["objective_relevance"] != "checked" {

		t.Fatalf("expected blocked objective relevance readiness, got %#v", readiness)
	}

	if paths, _ := readiness["evidence_paths"].([]any); len(paths) != 0 {
		t.Fatalf("direct_answer alias blocked telemetry must not fall back to unrelated evidence paths, got %#v", paths)
	}

	critic := events["final_answer_critic"]
	if critic["acceptance_contract"] != finalAnswerAcceptanceSchema ||
		critic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		critic["blocker_kind"] != "answer_objective_unmet" {

		t.Fatalf("expected final critic to carry rejected acceptance fields, got %#v", critic)
	}
}

func TestFinalAnswerOverrideLogsOriginalObjectiveRejection(t *testing.T) {
	rawRejectedAnswer := "The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": rawRejectedAnswer},
			}},
		})
	}))

	defer server.Close()

	var (
		override       map[string]any
		acceptedCritic bool
		blockedCritic  map[string]any
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: true},
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "final_answer_blocker_override":
				if err := json.Unmarshal([]byte(event.Content), &override); err != nil {
					t.Fatalf("decode override event: %v", err)
				}
			case "final_answer_critic":
				if strings.Contains(event.Content, `"action":"accept"`) {
					acceptedCritic = true
				}

				if strings.Contains(event.Content, `"action":"block"`) {
					if err := json.Unmarshal([]byte(event.Content), &blockedCritic); err != nil {
						t.Fatalf("decode blocked critic: %v", err)
					}
				}
			}
		},
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

	if strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("user-facing answer should be sanitized, got:\n%s", answer)
	}

	if strings.Contains(answer, "evidenceDisplayName") {
		t.Fatalf("user-facing answer leaked rejected model answer:\n%s", answer)
	}

	if override == nil {
		t.Fatal("expected final_answer_blocker_override event")
	}

	if acceptedCritic {
		t.Fatal("rejected final answer replacement must not emit accepted final-answer telemetry")
	}

	if blockedCritic["blocker_kind"] != "answer_objective_unmet" ||
		blockedCritic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		blockedCritic["objective_relevance"] != "checked" {

		t.Fatalf("blocked telemetry should consume the original raw-answer acceptance, got %#v", blockedCritic)
	}

	if override["original_blocker_kind"] != "answer_objective_unmet" ||
		override["replacement_stop_reason"] != "answer_objective_unmet" ||
		override["objective_relevance"] != "checked" ||
		override["original_acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		override["raw_answer_logged"] != false {

		t.Fatalf("override event lost original rejection metadata: %#v", override)
	}

	if reason, _ := override["original_reason"].(string); !strings.Contains(reason, "does not establish the user's objective") {
		t.Fatalf("override event should carry sanitized contract reason, got %#v", override)
	}

	if paths, _ := override["original_evidence_paths"].([]any); len(paths) != 0 {
		t.Fatalf("override event must not promote unrelated evidence paths, got %#v", paths)
	}

	if strings.Contains(fmt.Sprint(override), "evidenceDisplayName") {
		t.Fatalf("override event leaked raw rejected answer: %#v", override)
	}
}

func TestTerminalAnswerDecisionBlocksTransportProtocolOutput(t *testing.T) {
	rawAnswer := `<n_tool name="read_file"><path>internal/runtime/agent/runner.go</path></n_tool>`
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain the finish boundary.",
		},
	}
	decision := runner.classifyTerminalAnswer(rawAnswer, "model_final", nil)

	if decision.Outcome != terminalAnswerTransportBlocked {
		t.Fatalf("transport markup got outcome %q, want %q", decision.Outcome, terminalAnswerTransportBlocked)
	}

	if decision.Acceptance.BlockerKind != "transport_protocol_output" ||
		decision.Acceptance.Decision != finalAnswerAcceptanceRejected ||
		decision.StopReason != "transport_protocol_output" {

		t.Fatalf("transport decision should be blocked protocol output, got %#v", decision)
	}

	if strings.Contains(decision.PresentedAnswer, "<n_tool") || strings.TrimSpace(decision.PresentedAnswer) == "" {
		t.Fatalf("presented transport blocker should be clean user-facing prose, got %q", decision.PresentedAnswer)
	}

	var critic map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "final_answer_critic" {
			return
		}

		if err := json.Unmarshal([]byte(event.Content), &critic); err != nil {
			t.Fatalf("decode critic: %v", err)
		}
	}
	runner.emitFinalAnswerTelemetry(decision.RawAnswer, nil, decision.Acceptance)

	if critic["action"] != "block" ||
		critic["blocker_kind"] != "transport_protocol_output" ||
		critic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) {

		t.Fatalf("transport telemetry should be blocked from the typed decision, got %#v", critic)
	}
}

func TestReadOnlySourceGroundingRejectionRetriesEvidenceLoop(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package app",
		"",
		"func previousMessageIndex(current int) int {",
		"\treturn current - 1",
		"}",
	}, "\n")

	if err := writeTestFile(root, "internal/surfaces/tui/app/model_state.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"The message navigation lives in `internal/surfaces/tui/app/model_state_test.go` lines 1-5.",
		`{"tool":"read_file","path":"internal/surfaces/tui/app/model_state.go","start_line":1,"end_line":5,"named_gap":"source body for message navigation after missing test-path citation","why_not_answering":"the previous final answer cited a current-workspace path that does not exist"}`,
		"The current source evidence is `internal/surfaces/tui/app/model_state.go` lines 1-5: `previousMessageIndex` returns the previous index.",
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

	var (
		sawCompletionGateRejection bool
		sawOverride                bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain where up/down previous-message navigation would live.",
			AllowedTools:      []string{"read_file", "search_files", "search_content", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				if strings.Contains(event.Content, "completion_gate_rejected") {
					sawCompletionGateRejection = true
				}
			case "final_answer_blocker_override":
				sawOverride = true
			}
		},
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

	if requests != len(replies) {
		t.Fatalf("expected rejected final answer, recovery read, and corrected final answer; used %d/%d replies", requests, len(replies))
	}

	if !sawCompletionGateRejection {
		t.Fatal("expected source-grounding rejection to be recorded as completion_gate_rejected")
	}

	if sawOverride {
		t.Fatal("first recoverable source-grounding rejection should not be replaced with final_answer_blocker_override")
	}

	if !strings.Contains(answer, "internal/surfaces/tui/app/model_state.go") || !strings.Contains(answer, "previousMessageIndex") {
		t.Fatalf("expected corrected answer from source-body evidence, got:\n%s", answer)
	}

	if strings.Contains(answer, "model_state_test.go") {
		t.Fatalf("corrected answer should not retain missing-path citation:\n%s", answer)
	}
}

func TestReadOnlyAnswerObjectiveRejectionRetriesEvidenceLoop(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package app",
		"",
		"func explainChatInputWrap() string {",
		"\treturn \"chat input first wraps to a newline when measured height grows and the viewport shifts upward out of view\"",
		"}",
	}, "\n")

	if err := writeTestFile(root, "internal/surfaces/tui/app/model.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	objective := "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view."
	replies := []string{
		"The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display.",
		`{"tool":"read_file","path":"internal/surfaces/tui/app/model.go","start_line":1,"end_line":5,"named_gap":"objective-relevant source body for TUI chat input wrapping upward","why_not_answering":"the previous final answer used inspected source that did not establish the user's objective"}`,
		"The objective-relevant source is `internal/surfaces/tui/app/model.go` lines 3-5: `explainChatInputWrap` says the chat input first wraps to a newline when measured height grows and the viewport shifts upward out of view.",
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

	var (
		sawCompletionGateRejection bool
		sawOverride                bool
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         objective,
			AllowedTools:      []string{"read_file", "search_files", "search_content", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				if strings.Contains(event.Content, "completion_gate_rejected") {
					sawCompletionGateRejection = true
				}
			case "final_answer_blocker_override":
				sawOverride = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected objective rejection, recovery read, and corrected answer; used %d/%d replies", requests, len(replies))
	}

	if !sawCompletionGateRejection {
		t.Fatal("expected objective rejection to be recorded as completion_gate_rejected")
	}

	if sawOverride {
		t.Fatal("recoverable objective rejection should not be replaced with final_answer_blocker_override")
	}

	if !strings.Contains(answer, "internal/surfaces/tui/app/model.go") || !strings.Contains(answer, "explainChatInputWrap") {
		t.Fatalf("expected corrected answer from objective-relevant source evidence, got:\n%s", answer)
	}

	if strings.Contains(answer, "evidenceDisplayName") {
		t.Fatalf("corrected answer should not retain unrelated source claim:\n%s", answer)
	}
}

func TestFinalAnswerAcceptanceRetryPolicyKeepsNonRetryableBoundaries(t *testing.T) {
	readOnlyRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}
	retryable := []string{"source_grounding_unmet", "answer_objective_unmet", "read_only_protocol_blocker"}

	for _, kind := range retryable {
		if !readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
			t.Fatalf("%s should be retryable for read-only direct-answer evidence repair", kind)
		}
	}

	nonRetryable := []string{
		"non_answer_prose",
		"unexecuted_tool_request_final_answer",
		"required_evidence_unmet",
		"user_visible_blocker",
		"required_edit_paths_remaining",
	}
	for _, kind := range nonRetryable {
		if readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
			t.Fatalf("%s must not enter read-only final-answer retry policy", kind)
		}
	}
}

func TestDirectAnswerQuestionProtocolIsRetryableNotUserVisibleBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}
	kind, _ := runner.finalAnswerBlockerKind("<<QUESTION>>: I need one more source read.", nil)

	if kind != "read_only_protocol_blocker" {
		t.Fatalf("read-only direct-answer QUESTION protocol should be retryable protocol drift, got %q", kind)
	}

	if !runner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
		t.Fatalf("%s should enter read-only final-answer retry policy", kind)
	}
}

func TestHostReadOnlyCloseoutIsDisabledForReadOnlyEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Why did the latest run fail?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "logs/conversations/20260604-195321.801.jsonl:20: completion_gate_repeated_rejection",
	}}
	if answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results); ok {
		t.Fatalf("read-only evidence must not produce a host-authored closeout:\n%s", answer)
	}
}

func TestFinalAnswerAcceptanceRetryPolicyRequiresToolsAndNoMutation(t *testing.T) {
	noToolsRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
		},
	}
	if noToolsRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "answer_objective_unmet"}, nil, 0, nil) {
		t.Fatal("answer_objective_unmet must remain terminal when no evidence tools are available")
	}

	readOnlyRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	if !readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 1, nil) {
		t.Fatal("source_grounding_unmet should remain retryable before the retry limit is exhausted")
	}

	if !readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 2, nil) {
		t.Fatal("source_grounding_unmet must remain retryable when prior rejections (2) are within the read-only retry budget (3)")
	}

	if readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 3, nil) {
		t.Fatal("source_grounding_unmet must become terminal once the retry budget (3) is exhausted")
	}

	architectureRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "context_query"},
		},
	}
	if !architectureRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{
		BlockerKind: "non_answer_prose",
		Reason:      "insufficient_evidence_answer",
	}, nil, 0, nil) {
		t.Fatal("read-only architecture insufficient-evidence non-answer should get a recovery turn")
	}

	if architectureRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{
		BlockerKind: "non_answer_prose",
		Reason:      "generic_non_answer",
	}, nil, 0, nil) {
		t.Fatal("generic architecture non-answer prose should remain terminal")
	}

	malformedRequiredPathRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/model.go"},
			AllowedTools:      []string{"read_file"},
		},
	}
	if malformedRequiredPathRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 0, nil) {
		t.Fatal("required_edit_paths must keep malformed direct-answer/read-only briefs out of final-answer retry policy")
	}

	mutationRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyImplementation),
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/model.go"},
			AllowedTools:      []string{"read_file", "apply_patch"},
		},
	}
	if mutationRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "required_edit_paths_remaining"}, nil, 0, nil) {
		t.Fatal("mutation required_edit_paths_remaining must not enter read-only final-answer retry policy")
	}

	if mutationRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "answer_objective_unmet"}, nil, 0, nil) {
		t.Fatal("read-only answer retry policy must not apply to mutation lanes")
	}
}

func TestEmitAcceptedAnswerSignalsImplementationEvidenceUsesChangedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "implement",
			Contract:          "implementation_result",
			TaskFamily:        "implementation",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Update TDD obligation planning.",
		},
	}

	var readiness map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}

		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}
	runner.emitAcceptedAnswerSignals(
		"Changed `internal/workflows/productworkflow/tdd_obligation_plans.go` and tests.",
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f2 internal/workflows/productworkflow/tdd_obligation_plans_test.go lines 1-220",
				Output:  "RelPath: \"adrs/ADR-1.md\"\nFile: \"adrs/prd.md\"\nOwner: \"adrs/greeting.test.js\"",
			},
			{
				Tool:    "edit_file",
				Summary: "Edited internal/workflows/productworkflow/tdd_obligation_plans.go",
			},
			{
				Tool:    "edit_file",
				Summary: "Edited internal/workflows/productworkflow/tdd_obligation_plans_test.go",
			},
		},
	)

	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))

	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}

	for _, want := range []string{"internal/workflows/productworkflow/tdd_obligation_plans.go", "internal/workflows/productworkflow/tdd_obligation_plans_test.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected implementation readiness paths to contain changed path %q, got %#v", want, got)
		}
	}

	for _, forbidden := range []string{"adrs/ADR-1.md", "adrs/prd.md", "adrs/greeting.test.js"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect fixture path %q in implementation readiness evidence, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyExplanationCompletionRejectionRejectsMetaScopeSummary(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	result, rejected := runner.completionGateRejection(
		"I have sufficient evidence to answer. The strongest implementation evidence is in `internal/runtime/agent/runner.go`.",
		nil,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected meta scope summary to be rejected")
	}

	if !strings.Contains(result.Error, "implementation surface") && !strings.Contains(result.Error, "concrete behavior") {
		t.Fatalf("expected explanatory rejection, got %#v", result)
	}
}

func TestReadOnlySourceGroundingUnmetRecoveryPromptMentionsPathsAndOptions(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			Objective:         "How does the TUI navigation work?",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
	}

	// Simulate a source_grounding_unmet rejection error that cites paths
	// not backed by body evidence.
	runner.lastCompletionGateError = "the answer cites source path/line/symbol detail not backed by a source-body read: `internal/surfaces/tui/app/model_state_test.go:1-5`, `internal/surfaces/tui/app/view.go:42`. Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."

	// Evidence results are not needed because the source-grounding check
	// returns the recovery prompt before the evidence-readiness check.
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatal("expected source-grounding unmet to produce a recovery prompt, got none")
	}
	if hideReads {
		t.Fatal("read-only source-grounding recovery should not hide reads")
	}
	if !strings.Contains(prompt, "internal/surfaces/tui/app/model_state_test.go") {
		t.Fatalf("expected recovery prompt to mention the first unsupported path, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "internal/surfaces/tui/app/view.go") {
		t.Fatalf("expected recovery prompt to mention the second unsupported path, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "read") && !strings.Contains(prompt, "read_file") {
		t.Fatalf("expected recovery prompt to suggest reading paths, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "rewrite") && !strings.Contains(prompt, "qualify") {
		t.Fatalf("expected recovery prompt to suggest rewriting or qualifying, got:\n%s", prompt)
	}
}

func TestReadOnlySourceGroundingUnmetRecoveryPromptSkippedWhenNoLastError(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			Objective:         "Find the longest function in internal/labels/labels.go.",
			// Set OpenObligations so NormalizeReadinessState does not
			// collapse Readiness to nil, allowing the normal no-evidence
			// recovery prompt to fire.
			Readiness: &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}

	// No source-grounding error set — recovery prompt should not fire.
	runner.lastCompletionGateError = ""

	// Without evidence and without a source-grounding error, the normal
	// "no evidence" recovery path should produce a prompt.
	prompt, _, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatal("expected a recovery prompt from the normal no-evidence path when there is no source-grounding error")
	}
	if strings.Contains(prompt, "rewrite") {
		t.Fatal("normal no-evidence prompt should not mention rewrite/qualify options")
	}
}

// TestSourceGroundingProseRetriesLeftSetToTwo verifies that the
// source-grounding prose retry counter is bumped to 2 (not 1).
func TestSourceGroundingProseRetriesLeftSetToTwo(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content"},
		},
	}

	hidden := map[string]bool{}
	rejections := 0
	turnMessages := []llama.ChatMessage{}

	sourceGroundingErr := "the answer cites source path/line/symbol detail not backed by a source-body read: `src/graphics/renderer.rs`. Use one exact read/code_navigate body probe."
	retried := runner.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
		stepNumber: 1,
		assistantMessage: llama.ChatMessage{
			Role:    "assistant",
			Content: "The renderer uses src/graphics/renderer.rs.",
		},
		acceptance:               finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"},
		result:                   workspace.ToolResult{Tool: "completion_gate", Summary: "Rejected", Error: sourceGroundingErr},
		collectedResults:         nil,
		currentAllowedTools:      []string{"read_file", "code_navigate", "search_content"},
		completionGateRejections: &rejections,
		turnMessages:             &turnMessages,
		turnHiddenTools:          hidden,
		controller:               &runnerTurnController{},
	})
	if !retried {
		t.Fatal("source-grounding rejection should be retryable")
	}
	if runner.sourceGroundingProseRetriesLeft != 2 {
		t.Fatalf("sourceGroundingProseRetriesLeft should be 2, got %d", runner.sourceGroundingProseRetriesLeft)
	}
	// search tools should be hidden; read tools should not.
	if !hidden["search_content"] {
		t.Fatal("search_content should be hidden after source-grounding rejection")
	}
	if hidden["read_file"] {
		t.Fatal("read_file should NOT be hidden after source-grounding rejection")
	}
	if hidden["code_navigate"] {
		t.Fatal("code_navigate should NOT be hidden after source-grounding rejection")
	}
}

// TestReadOnlyRecoveryPromptMentionsToolRestriction verifies the recovery
// prompt explicitly states the tool restriction.
func TestReadOnlyRecoveryPromptMentionsToolRestriction(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			Objective:         "How does the rendering pipeline work?",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
	}
	runner.lastCompletionGateError = "the answer cites source path/line/symbol detail not backed by a source-body read: `src/graphics/renderer.rs:42`. Use one exact read/code_navigate body probe."

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatal("expected source-grounding unmet to produce a recovery prompt")
	}
	if hideReads {
		t.Fatal("read-only source-grounding recovery should not hide reads")
	}
	if !strings.Contains(prompt, "restricted to `read_file` and `code_navigate`") {
		t.Fatalf("expected recovery prompt to mention tool restriction, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<<QUESTION>>") {
		t.Fatalf("expected recovery prompt to mention <<QUESTION>>: escape hatch, got:\n%s", prompt)
	}
}

// TestCompoundRejectionReasonIncludesObjectiveUnmet verifies that when
// both source-grounding AND objective-unmet apply, the rejection reason
// surfaces both problems in a compound message.
func TestCompoundRejectionReasonIncludesObjectiveUnmet(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			Objective:         "How does the rendering pipeline use vertex shaders?",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read src/graphics/pipelines.rs lines 1-100", Output: "// Pipeline module -- render pass configuration"},
	}
	answer := "The rendering pipeline uses `src/graphics/renderer.rs` to draw the scene and `src/shaders/vertex.wgsl` for shaders."

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected {
		t.Fatal("expected compound rejection to be rejected")
	}
	if !strings.Contains(acceptance.Reason, "Additionally") {
		t.Fatalf("expected compound rejection to include 'Additionally', got:\n%s", acceptance.Reason)
	}
	if !strings.Contains(acceptance.Reason, "not read as source-body evidence") {
		t.Fatalf("expected compound rejection to include source-grounding reason, got:\n%s", acceptance.Reason)
	}
}
