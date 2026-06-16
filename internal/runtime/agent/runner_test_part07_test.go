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
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestConversationEvidenceAvailableForObjectiveWaitsForNamedSourcePaths(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain internal/labels/labels.go and internal/labels/labels_test.go.",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
		},
	}

	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("evidence availability fired before all named source paths had concrete source evidence")
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels_test.go"},
		Results: results,
	})
	if decision.ForceAnswer != nil || decision.RejectSummary != "" {
		t.Fatalf("second named source read should remain available, got %#v", decision)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/labels/labels_test.go lines 1-9 (total lines: 9)",
		Output:  "1:package labels\n3:func TestShortLabel(t *testing.T) {\n4:\tif got := ShortLabel(); got != \"Save\" {\n",
	})
	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("expected evidence availability after all named source paths had concrete source evidence")
	}
}

func TestConversationPreflightRejectsRepeatedBroadDiscoveryWithoutForcingAnswer(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"context_pack", "context_query", "read_file"},
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected unresolved process narration",
			Error:   "the reply describes an intended future search/read/inspection instead of completing the answer",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/example.json with 4 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
		},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_query", Query: "bounded read scoping", Resolution: "symbol"},
		Results: results,
	})
	if decision.RejectSummary != "Conversation contract rejected repeated broad discovery after bounded evidence" {
		t.Fatalf("expected repeated broad discovery rejection, got %#v", decision)
	}

	if decision.ForceAnswer != nil {
		t.Fatalf("repeated broad discovery must not force final synthesis, got %#v", decision.ForceAnswer)
	}

	if !strings.Contains(decision.RejectError, "recovery probe") {
		t.Fatalf("expected recovery-probe explanation, got %q", decision.RejectError)
	}

	if !strings.Contains(decision.RejectError, "narrow read/search") {
		t.Fatalf("expected narrow follow-up to remain available, got %q", decision.RejectError)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go", StartLine: 1, EndLine: 80},
		Results: results,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected narrow read to pass preflight (nudge fires post-result), got %#v", decision)
	}

	if decision.ForceAnswer != nil {
		t.Fatalf("expected no force answer on preflight for conversation evidence (nudge replaces force), got %#v", decision.ForceAnswer)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{
			Tool:            "context_query",
			Query:           "callers of Runner.runChildTask",
			Resolution:      "symbol",
			NamedGap:        "which caller triggers child task orchestration",
			WhyNotAnswering: "without caller evidence the control flow is inferred",
		},
		Results: results,
	})
	if decision.RejectSummary != "" || decision.ForceAnswer != nil {
		t.Fatalf("targeted context_query with a named gap should remain available, got %#v", decision)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{
			Tool:            "context_pack",
			Query:           "restart broad localization",
			NamedGap:        "need more context",
			WhyNotAnswering: "more context could help",
		},
		Results: results,
	})
	if decision.RejectSummary != "Conversation contract rejected repeated broad discovery after bounded evidence" {
		t.Fatalf("named_gap must not reopen broad context_pack discovery, got %#v", decision)
	}
}

func TestConversationDiscoveryConsumptionAllowsFollowupSearchAfterContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 2 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/surfaces/tui/app/model.go"],"reasons":["related_evidence_without_source_owner"]},"candidate_files":[{"path":"internal/surfaces/tui/app/model.go"},{"path":"internal/surfaces/tui/app/view.go"}]}`,
		},
		"",
		nil,
		nil,
	)

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("follow-up file search should stay available after weak context_pack localization")
	}

	targetedFileSearch := workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "**/tui*.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedFileSearch, brief) {
		t.Fatal("concrete file/glob search should stay available after weak context_pack localization")
	}

	targetedGlobFieldSearch := workspace.ToolRequest{
		Tool: "search_files",
		Glob: "**/tui/**/*.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedGlobFieldSearch, brief) {
		t.Fatal("concrete glob-field file search should stay available after weak context_pack localization")
	}

	targetedContent := workspace.ToolRequest{
		Tool:     "search_content",
		Pattern:  "chat input newline textarea",
		Path:     ".",
		NamedGap: "find the concrete text anchor for the chat input newline rendering path",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedContent, brief) {
		t.Fatal("content search with a concrete named gap should stay available after weak context_pack localization")
	}

	broadMultiTermContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chat input newline textarea",
		Path:    ".",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(broadMultiTermContent, brief) {
		t.Fatal("repo-root follow-up content search should stay available after weak context_pack localization")
	}

	fileScopedContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "textarea",
		Path:    "internal/surfaces/tui/app/lifecycle.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(fileScopedContent, brief) {
		t.Fatal("file-scoped content search should stay available after weak context_pack localization")
	}

	genericContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "scroll",
		Path:    ".",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(genericContent, brief) {
		t.Fatal("single-token follow-up content search should stay available after weak context_pack localization")
	}

	for _, req := range []workspace.ToolRequest{
		{Tool: "search_files", Pattern: "*.go"},
		{Tool: "search_files", Pattern: "**/*.go"},
		{Tool: "search_files", Glob: "internal/**/*.go"},
	} {
		if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
			t.Fatalf("follow-up file search should stay available after weak context_pack localization: %#v", req)
		}
	}

	targeted := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "callers of app model viewport update",
		Resolution:      "symbol",
		NamedGap:        "which caller mutates viewport height after terminal resize",
		WhyNotAnswering: "need a caller relationship before choosing a source read",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatalf("targeted graph query with a concrete named gap should remain available")
	}

	zoom := workspace.ToolRequest{
		Tool:          "context_zoom",
		Handle:        "symbol://internal/surfaces/tui/app/model.go#Update",
		Resolution:    "symbol",
		IncludeSource: true,
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(zoom, brief) {
		t.Fatalf("targeted context_zoom on a concrete handle should consume discovery, not be rejected as broad discovery")
	}

	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/model.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/model.go lines 1-80", Output: "package app\n"},
		"",
		nil,
		nil,
	)

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("successful source-body read should clear pending discovery consumption")
	}
}

func TestConversationDiscoveryConsumptionRejectsBroadSearchAfterStrongContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 2 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/surfaces/tui/app/model.go"],"reasons":["source_owner_token_overlap_high"]},"candidate_files":[{"path":"internal/surfaces/tui/app/model.go"},{"path":"internal/surfaces/tui/app/view.go"}]}`,
		},
		"",
		nil,
		nil,
	)

	for _, req := range []workspace.ToolRequest{
		{Tool: "search_files", Path: ".", Pattern: "scroll"},
		{Tool: "search_files", Path: ".", Pattern: "**/*.go"},
		{Tool: "search_content", Path: ".", Pattern: "scroll"},
	} {
		if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
			t.Fatalf("strong packet should require candidate consumption before broad rediscovery: %#v", req)
		}
	}

	graphRefinement := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what handles TUI key input for conversation messages",
		Resolution:      "symbol",
		IncludeEvidence: true,
		NamedGap:        "source owner for TUI key input conversation message handling",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(graphRefinement, brief) {
		t.Fatal("strong packet should still allow a targeted source-owner graph refinement")
	}

	targeted := workspace.ToolRequest{
		Tool:     "search_content",
		Path:     ".",
		Pattern:  "scroll viewport update",
		NamedGap: "find the concrete source anchor for viewport scrolling",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatal("strong packet should still allow a named-gap targeted probe")
	}

	errorText := state.discoveryConsumptionError()
	for _, want := range []string{"source-body evidence", "internal/surfaces/tui/app/model.go", "answer from the evidence", "exact blocker"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("discovery consumption error missing %q:\n%s", want, errorText)
		}
	}

	if strings.Contains(strings.ToLower(errorText), "resize") {
		t.Fatalf("generic discovery guard must not hardcode the live incident shape:\n%s", errorText)
	}
}

func TestStrongContextPackStillRejectsBroadSearchAfterFailedGuessedRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/surfaces/tui/app/model.go"],"reasons":["source_owner_token_overlap_high"]},"candidate_files":[{"path":"internal/surfaces/tui/app/model.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/tui.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Failed to resolve file target",
			Error:   "file not found: internal/surfaces/tui/app/tui.go",
		},
		"",
		nil,
		nil,
	)

	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Path: ".", Pattern: "scroll"}, brief) {
		t.Fatal("failed guessed read after strong context_pack must not reopen broad file search")
	}

	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "scroll"}, brief) {
		t.Fatal("failed guessed read after strong context_pack must not reopen broad context_pack")
	}

	targeted := workspace.ToolRequest{
		Tool:     "search_content",
		Path:     ".",
		Pattern:  "scroll viewport update",
		NamedGap: "find the concrete source anchor after guessed path failed",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatal("named-gap corrective search should remain available after failed guessed read")
	}
}

func TestWeakContextPackDoesNotBlockRefinedContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Task: "chat input text box newline wrapping first line invisible"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 3 candidate file(s)",
			Output: `{
				"task":"chat input text box newline wrapping first line invisible",
				"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/surfaces/tui/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
				"candidate_files":[
					{"path":"internal/evidence/transcript/debuglog.go","reason":"matched task terms"},
					{"path":"internal/surfaces/tui/app/plan.go","reason":"matched task terms"},
					{"path":"internal/surfaces/tui/app/theme_style.go","reason":"matched task terms"}
				],
				"tests":[{"path":"internal/surfaces/tui/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/surfaces/tui/app"}]
			}`,
		},
		"",
		nil,
		nil,
	)

	refined := workspace.ToolRequest{
		Tool: "context_pack",
		Task: "textarea syncInputHeight input height wraps prompt visible top line",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(refined, brief) {
		t.Fatal("weak packet must not force consumption before a materially refined context_pack")
	}
}

func TestContextPackCandidatePathsPreferPacketQualityEvidence(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with weak source owner",
		Output: `{
			"task":"chat input text box newline wrapping first line invisible",
			"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/surfaces/tui/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
			"candidate_files":[
				{"path":"internal/evidence/transcript/debuglog.go","reason":"matched task terms"},
				{"path":"internal/surfaces/tui/app/plan.go","reason":"matched task terms"},
				{"path":"internal/surfaces/tui/app/theme_style.go","reason":"matched task terms"}
			],
			"candidate_symbols":[
				{"path":"internal/surfaces/tui/app/rendering_test.go","kind":"test","qualified_name":"TestInputHeightUsesTextareaWrappedLineCount"}
			],
			"tests":[{"path":"internal/surfaces/tui/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/surfaces/tui/app"}]
		}`,
	}

	paths := contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, 4)
	if len(paths) == 0 || paths[0] != "internal/surfaces/tui/app/rendering_test.go" {
		t.Fatalf("expected quality evidence path first, got %#v", paths)
	}

	if paths[0] == "internal/evidence/transcript/debuglog.go" {
		t.Fatalf("candidate files must not saturate recovery ahead of stronger packet evidence: %#v", paths)
	}
}

func TestContextPackCandidateSymbolsPreferTopLevelQualityEvidence(t *testing.T) {
	output := `{
		"task":"chat input text box newline wrapping first line invisible",
		"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/surfaces/tui/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
		"candidate_files":[
			{"path":"internal/evidence/transcript/debuglog.go","role":"editable","reason":"matched task terms","handles":["symbol://internal/evidence/transcript/debuglog.go#DebugLogger.LogContext"]},
			{"path":"internal/surfaces/tui/app/mocha_style.go","role":"editable","reason":"matched task terms","handles":["symbol://internal/surfaces/tui/app/mocha_style.go#mochaText"]}
		],
		"candidate_symbols":[
			{"path":"internal/surfaces/tui/app/rendering_test.go","kind":"test","qualified_name":"TestInputHeightUsesTextareaWrappedLineCount","reason":"matched task terms"},
			{"path":"internal/runtime/agent/runner.go","kind":"func","qualified_name":"textOnlyChatOptions","reason":"matched task terms"}
		],
		"tests":[{"path":"internal/surfaces/tui/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/surfaces/tui/app"}]
	}`

	symbols := contextPackCandidateSymbols(output, 4)
	if len(symbols) == 0 || symbols[0] != "internal/surfaces/tui/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount" {
		t.Fatalf("expected top-level quality evidence symbol first, got %#v", symbols)
	}

	if symbols[0] == "internal/evidence/transcript/debuglog.go#DebugLogger.LogContext" {
		t.Fatalf("candidate-file handles must not outrank top-level quality evidence: %#v", symbols)
	}
}

func TestContextZoomSourceBodyClearsPendingDiscoveryConsumption(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug source evidence"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/model.go","handles":["symbol://internal/surfaces/tui/app/model.go#Update"]}]}`,
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_zoom", Handle: "symbol://internal/surfaces/tui/app/model.go#Update", Resolution: "symbol", IncludeSource: true},
		workspace.ToolResult{
			Tool:    "context_zoom",
			Summary: "Context zoom returned source body for internal/surfaces/tui/app/model.go#Update",
			Output:  `{"path":"internal/surfaces/tui/app/model.go","source":"func Update() {}"}`,
		},
		"",
		nil,
		nil,
	)

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("context_zoom with source body should clear pending discovery consumption")
	}
}

func TestCandidateAnchorMissDoesNotCountAsSourceBodyEvidence(t *testing.T) {
	direct := workspace.ToolResult{
		Tool:    "go_read_symbol",
		Summary: `Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
		Output: strings.Join([]string{
			"requested_symbol: Model.Update",
			"requested_path: internal/surfaces/tui/app/model_state.go",
			"path_filter_status: no source-body match in requested path",
			"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
			"candidate_anchors:",
			"- internal/surfaces/tui/app/lifecycle.go:409 method Model.Update",
			"candidate_requests:",
			`- {"tool":"code_navigate","capability":"read_symbol","path":"internal/surfaces/tui/app/lifecycle.go","pattern":"Model.Update"}`,
		}, "\n"),
	}
	routed := workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			`route_summary: Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
			"",
			direct.Output,
		}, "\n"),
	}

	for _, result := range []workspace.ToolResult{direct, routed} {
		if concreteSourceEvidenceResult(result) {
			t.Fatalf("%s candidate-anchor miss counted as concrete source evidence", result.Tool)
		}

		if paths := agentutil.ReadPathsFromToolResult(result); len(paths) != 0 {
			t.Fatalf("%s candidate-anchor miss returned read paths: %#v", result.Tool, paths)
		}
	}

	if got := readOnlyAnalysisExactProbeCount([]workspace.ToolResult{direct, routed}); got != 0 {
		t.Fatalf("candidate-anchor miss counted as exact probe: %d", got)
	}
}

func TestSearchEvidenceSurvivesFailedGuessedReadWithoutBlockingFollowupSearch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "search_files", Pattern: "view"},
		workspace.ToolResult{
			Tool:    "search_files",
			Summary: "Found 2 matching file(s).",
			Output:  "internal/surfaces/tui/app/model.go\ninternal/surfaces/tui/app/view.go",
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/tui.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Failed to resolve file target",
			Error:   "file not found: internal/surfaces/tui/app/tui.go",
		},
		"",
		nil,
		nil,
	)

	req := workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}
	if state.shouldConsumeSearchHitBeforeMoreSearch(req) {
		t.Fatal("failed guessed read must not force a follow-up search rejection")
	}

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
		t.Fatal("failed guessed read must not force a follow-up search discovery-consumption rejection")
	}

	if got := state.discoveryConsumptionError(); !strings.Contains(got, "internal/surfaces/tui/app/model.go") {
		t.Fatalf("expected recovery to preserve returned candidates, got:\n%s", got)
	}
}

func TestConversationFollowupDoesNotCollapseAfterRecoveryBroadSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("bounded scope approval works by approving a candidate read set and requiring explicit approval when a later read falls outside that set.\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		"I should search the repo before answering this.",
		`{"tool":"search_content","pattern":"bounded scope","path":"."}`,
		`{"tool":"context_query","query":"bounded read scoping implementation surface","resolution":"symbol"}`,
		"Bounded read scoping works by approving an initial candidate read set, then treating any later file outside that set as a new approval decision. It cannot predict every relevant file with perfect accuracy, so the contract is enforced at read time: allow scoped reads, require explicit approval for out-of-scope reads, and keep an audit trail of what was actually exposed.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected model request %d", requests+1)
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

	var results []ToolResultEvent

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxModelCalls: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "the bounded read scoping, how would that be possible?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "context_query", "read_file"},
			Budget: workerruntime.BudgetBrief{
				MaxToolSteps:  4,
				MaxModelCalls: 6,
			},
			Readiness: &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "the bounded read scoping, how would that be possible?"}},
		llama.ChatOptions{MaxTokens: 1024},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 4 {
		t.Fatalf("expected answer on the fourth model request after broad-search rejection, got %d request(s)", requests)
	}

	if !strings.Contains(answer, "approving an initial candidate read set") || !strings.Contains(answer, "out-of-scope reads") {
		t.Fatalf("answer did not explain bounded read scoping:\n%s", answer)
	}

	for _, forbidden := range []string{"retained source context", "not enough exact body-level detail", "Context pack wrote"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer should not collapse to fallback phrase %q:\n%s", forbidden, answer)
		}
	}

	for _, result := range results {
		if result.Result.Tool == "context_query" && strings.TrimSpace(result.Result.Error) == "" {
			t.Fatalf("context_query should have been intercepted before execution, got result %#v", result.Result)
		}
	}
}

func TestConversationEvidenceSatisfiedContextPackAloneFiresGate(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 4 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"},{"path":"internal/runtime/agent/runner_lane_policy.go"}]}`,
		},
	}
	// context_pack alone is no longer sufficient — the model should read
	// at least one source body before the nudge fires. This prevents the
	// nudge from triggering after a single discovery call for questions
	// that genuinely need source-body inspection.
	if conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("context_pack alone should not fire evidence-satisfied gate — wait for at least one source-body read")
	}

	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("context_pack alone should not trigger evidence availability")
	}

	// Add a source-body read — now evidence should be satisfied.
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-80",
		Output:  "package agent\n\nfunc (r *Runner) Run(...",
	})
	if !conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("context_pack + source-body read should fire evidence-satisfied gate")
	}

	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("context_pack + source-body read should trigger evidence availability")
	}
}

func TestConversationEvidenceSatisfiedFileOutlineFindSymbolProbesOnlyLocalize(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "File outline for internal/runtime/agent/runner_lane_policy.go",
			Output:  "route_summary:\ncapability: file_outline\nlanguage: go\nrouted_tool: go_file_outline\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Found symbol conversationEvidenceSatisfied in internal/runtime/agent/runner_lane_policy.go",
			Output:  "route_summary:\ncapability: find_symbol\nlanguage: go\nrouted_tool: go_find_symbol\n",
		},
	}

	if conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("file_outline + find_symbol probes should localize, not satisfy direct-answer evidence")
	}

	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("outline/find-symbol localization should not trigger evidence availability")
	}
}

func TestReadOnlyAnalysisAnswerReadyAfterConversationSourceEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain why the debug investigation failed.",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
			Output:  "132:{\"type\":\"message\"}\n",
		},
	}

	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[0], results) {
		t.Fatal("diagnostic logs alone should not trigger conversation answer-readiness")
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
		Output:  "52:{\"type\":\"message\"}\n",
	})
	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("multiple diagnostic log windows alone should not trigger conversation answer-readiness")
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/runtime/agent/runner.go lines 1-50 (total lines: 200)",
		Output:  "1:package agent\n2:func (r *Runner) Run() {}\n",
	})
	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[2], results) {
		t.Fatal("conversation source evidence should enter the generic post-readiness contract")
	}
}

func TestVisibleBriefForToolSurfaceNarrowsTargetWriteReadyManifest(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:         t.TempDir(),
			EnableWrites: true,
		}),
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "func ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
	}}

	visible := runner.visibleBriefForToolSurface(brief, results)
	for _, want := range []string{"edit_file", "apply_patch", "read_file", "search_content"} {
		if !slices.Contains(visible.AllowedTools, want) {
			t.Fatalf("target-write-ready visible surface missing %q: %#v", want, visible.AllowedTools)
		}
	}

	if !toolManifestHasTool(visible.ToolManifest, "read_file") || !toolManifestHasTool(visible.ToolManifest, "search_content") {
		t.Fatalf("target-write-ready tool manifest should include bounded source refresh tools: %#v", visible.ToolManifest)
	}

	if !strings.Contains(strings.Join(visible.SearchPolicy, "\n"), "bounded source refresh") {
		t.Fatalf("target-write-ready visible brief did not explain bounded source refresh: %#v", visible.SearchPolicy)
	}
}

func TestVisibleBriefForToolSurfaceKeepsPendingRedirectTool(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:         t.TempDir(),
			EnableWrites: true,
		}),
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Objective:         "fix session panel scrolling",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "search_content", "code_navigate", "edit_file", "apply_patch"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-20 (total lines: 80)",
			Output:  "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
		},
	}

	visible := runner.visibleBriefForToolSurface(brief, results)
	if !slices.Contains(visible.AllowedTools, "code_navigate") || !toolManifestHasTool(visible.ToolManifest, "code_navigate") {
		t.Fatalf("visible target-write-ready surface dropped pending redirect tool: tools=%#v manifest=%#v", visible.AllowedTools, visible.ToolManifest)
	}

	if slices.Contains(visible.AllowedTools, "read_file") || slices.Contains(visible.AllowedTools, "search_content") {
		t.Fatalf("pending redirected source obligation should not expose broad refresh tools: %#v", visible.AllowedTools)
	}
}

func TestVisibleBriefForToolSurfaceStatesReadOnlyEvidenceGateBeforeAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file", "search_content", "context_pack", "edit_file"},
		Objective:         "Can we shorten the save label in internal/labels/labels.go?",
		Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
	}

	visible := runnerVisibleBriefForToolSurface(brief, nil)

	if slices.Contains(visible.AllowedTools, "edit_file") {
		t.Fatalf("read-only evidence-pending surface retained write tool: %#v", visible.AllowedTools)
	}

	if !slices.Contains(visible.RequiredTools, "bounded_workspace_evidence_tool") {
		t.Fatalf("expected bounded evidence tool requirement, got %#v", visible.RequiredTools)
	}

	joined := strings.Join(append(append([]string{}, visible.RequiredNext...), visible.InvalidActions...), "\n")
	for _, want := range []string{
		"exactly one parser-readable bounded read/search/context tool call",
		"prose/final answer before a successful bounded evidence tool result",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in read-only evidence-pending guidance:\n%s", want, joined)
		}
	}
}

func TestReadOnlyEvidenceCompletionRejectionDoesNotAcceptShallowDiscovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}

	_, rejected := runner.completionGateRejection(
		"The label is probably in internal/labels/labels.go.",
		[]workspace.ToolResult{{Tool: "search_files", Summary: "Matched 1 file", Output: "internal/labels/labels.go"}},
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatal("shallow discovery must not satisfy read-only final-answer evidence")
	}
}

func TestEffectivePolicyHonorsRuntimeBriefBudget(t *testing.T) {
	runner := &Runner{
		MaxToolSteps: 12,
		Policy:       RunPolicy{MaxModelCalls: 20},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Budget: workerruntime.BudgetBrief{
				MaxToolSteps:  4,
				MaxModelCalls: 6,
			},
		},
	}
	policy := runner.effectivePolicy()

	if policy.MaxToolSteps != 4 || policy.MaxModelCalls != 6 {
		t.Fatalf("expected runtime brief budget to cap policy, got %#v", policy)
	}
}

func TestInFlightVerificationGateRespectsSkippedVerificationContract(t *testing.T) {
	state := newRunnerExecutionState(false, 0)

	state.writesSinceVerification = 2

	readReq := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}

	if !state.shouldRequireVerification(readReq, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("expected normal mutation contract to require verification after repeated writes")
	}

	if state.shouldRequireVerification(readReq, workerruntime.WorkerRuntimeBrief{VerificationContract: "not_required_for_text_mutations"}) {
		t.Fatal("text-mutation verification contract should skip in-flight verification gate")
	}

	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "task_ledger"}, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("task_ledger should remain allowed while verification is pending")
	}

	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "cpp_diagnostics"}, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("diagnostics tools should remain allowed as verification requests")
	}
}

func TestInFlightVerificationGateSkipsPlannerOwnedVerification(t *testing.T) {
	state := newRunnerExecutionState(false, 0)

	state.writesSinceVerification = 2

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		Constraints:          []string{"planner_owned_verification"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "read_file", Path: "src/main.cpp"}, brief) {
		t.Fatal("planner-owned verification lanes should not block follow-up implementation reads after writes")
	}
}

func TestControlRailsUseRuntimeBriefForImplementation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForRole("implement", "main", "update README"),
	}
	rails := runner.controlRails(nil)

	if rails.TaskFamily != "implementation" || rails.Contract != "implementation_result" {
		t.Fatalf("unexpected rails: %#v", rails)
	}

	if rails.PermissionProfile != "guarded_workspace_write" || rails.VerificationContract == "not_required_for_read_only_lane" {
		t.Fatalf("expected write + verification rails, got %#v", rails)
	}
}

func TestControlRailsReportRequiredEditPathsAndInstructionAttachment(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"native/src/job_queue.cpp"},
		},
		InstructionAttacher: NewInstructionAttacher(t.TempDir()),
	}

	rails := runner.controlRails(nil)
	if !slices.Contains(rails.RequiredEditPaths, "native/src/job_queue.cpp") {
		t.Fatalf("expected required edit paths in control rails, got %#v", rails.RequiredEditPaths)
	}

	if !rails.DynamicInstructions {
		t.Fatalf("expected control rails to report dynamic instruction attachment")
	}
}

func TestControlRailsPreserveReadOnlyDiscoveryForDirectAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol"},
		},
	}
	rails := runner.controlRails(nil)

	if rails.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", rails.PermissionProfile)
	}

	if len(rails.AllowedTools) == 0 {
		t.Fatal("expected direct-answer rails to keep discovery tools")
	}

	if len(rails.ReadinessGate) != 0 || len(rails.FinalCritic) != 0 {
		t.Fatalf("expected direct-answer rails to avoid extra readiness/final critic scaffolding, got %#v", rails)
	}
}

func TestControlRailsConversationOnlySuppressesAllowedTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "conversation_only",
			AllowedTools:      []string{"task", "search_files", "read_file"},
			FinalCritic:       []string{"answers_actual_user_request"},
		},
	}
	rails := runner.controlRails(nil)

	if rails.PermissionProfile != "conversation_only" {
		t.Fatalf("permission = %q, want conversation_only", rails.PermissionProfile)
	}

	if len(rails.AllowedTools) != 0 {
		t.Fatalf("expected conversation-only rails to suppress allowed tools, got %#v", rails.AllowedTools)
	}

	if !slices.Contains(rails.FinalCritic, "answers_actual_user_request") {
		t.Fatalf("expected conversation-only rails to keep explicit final critic, got %#v", rails.FinalCritic)
	}
}

func TestControlRailsBlankPermissionInfersReadOnlyWorkspaceWhenToolsExist(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "direct_answer",
			Contract:     "direct_answer",
			AllowedTools: []string{"search_files", "read_file"},
		},
	}
	rails := runner.controlRails(nil)

	if rails.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", rails.PermissionProfile)
	}
}

func TestControlRailsKeepStrictCriticForDebugLogs(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "debug_logs",
			Contract:          "grounded_explanation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file"},
		},
	}
	rails := runner.controlRails(nil)

	if len(rails.ReadinessGate) == 0 || len(rails.FinalCritic) == 0 {
		t.Fatalf("expected debug_logs rails to keep strict operational guidance, got %#v", rails)
	}
}

func TestControlRailsArchitectureKeepsClosureCritic(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol"},
			RequiredEvidence:  []string{"inspected_paths", "architecture_boundary_evidence", "confidence"},
		},
	}
	rails := runner.controlRails(nil)

	if len(rails.ReadinessGate) == 0 || len(rails.FinalCritic) == 0 {
		t.Fatalf("expected architecture rails to keep closure guidance, got %#v", rails)
	}
}
