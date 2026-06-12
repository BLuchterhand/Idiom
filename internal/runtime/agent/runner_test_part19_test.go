package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestCompletionGatePipelinePreservesInternalOrderBeforeExternalGate(t *testing.T) {
	externalCalled := false
	runner := &Runner{
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			externalCalled = true
			return CompletionGateResult{Summary: "external rejected", Error: "external"}
		},
	}
	result, rejected := runner.completionGateRejection(
		"<n_tool_calls>\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n</n_tool_calls>",
		nil,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected textual tool-call rejection")
	}

	if externalCalled {
		t.Fatalf("external completion gate must not run after an internal short-circuit")
	}

	if result.Summary != "Textual tool-call markup rejected as final answer" {
		t.Fatalf("unexpected first rejection: %#v", result)
	}
}

func TestCompletionGatePipelineFallsThroughToExternalGate(t *testing.T) {
	externalCalled := false
	runner := &Runner{
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			externalCalled = true
			return CompletionGateResult{Summary: "external rejected", Error: "external"}
		},
	}
	result, rejected := runner.completionGateRejection("Normal prose answer.", nil, 1, nil, false)

	if !rejected || !externalCalled {
		t.Fatalf("expected external gate rejection, called=%v result=%#v", externalCalled, result)
	}

	if result.Summary != "external rejected" || result.Error != "external" {
		t.Fatalf("external gate message changed: %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionRequiresBoundedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file"},
			Objective:         "Can we shorten internal/labels/labels.go?",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	result, rejected := runner.completionGateRejection(
		"Yes, that label can be shortened.",
		nil,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected read-only final answer without evidence to be rejected")
	}

	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{result})
	if !ok || hideReads {
		t.Fatalf("expected read-only evidence recovery prompt without hiding reads, got ok=%t hideReads=%t prompt=%q", ok, hideReads, prompt)
	}

	if !strings.Contains(prompt, `"tool":"read_file"`) || !strings.Contains(prompt, "internal/labels/labels.go") {
		t.Fatalf("expected recovery prompt to request the named source path, got:\n%s", prompt)
	}
}

func TestReadOnlyEvidenceCompletionRejectionAppliesToDirectAnswerAliasWithoutReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "Find why the TUI chat input first wraps to a newline.",
		},
	}
	result, rejected := runner.readOnlyEvidenceCompletionRejection(
		"I cannot determine from the available evidence why the TUI chat input first wraps to a newline.",
		nil,
	)

	if !rejected {
		t.Fatal("expected direct_answer alias without readiness/source evidence to be rejected")
	}

	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionSkipsConversationWithoutReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "In two short sentences, explain ordinary chat that does not ask for repository work.",
		},
	}
	_, rejected := runner.readOnlyEvidenceCompletionRejection(
		"Ordinary chat should be handled as direct conversation without workspace tools.",
		nil,
	)

	if rejected {
		t.Fatal("conversation/direct_answer without readiness should not be forced through the alias evidence gate")
	}
}

func TestReadOnlyEvidenceCompletionRejectionAllowsSourceBackedInsufficientEvidenceAlias(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "Find why the TUI chat input first wraps to a newline.",
		},
	}
	_, rejected := runner.readOnlyEvidenceCompletionRejection(
		"I cannot determine from the available evidence why the TUI chat input first wraps to a newline.",
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string { return strings.TrimSpace(rawTool) }\n",
		}},
	)

	if rejected {
		t.Fatal("source-backed insufficient-evidence direct_answer alias should reach final acceptance instead of completion-gate rejection")
	}
}

func TestReadOnlyEvidenceRejectionDoesNotAskToRereadAlreadyInspectedNamedPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "locate_targets",
			TaskFamily:        "locate",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
			Objective:         "Read-only investigation: session logs/conversations/recent.jsonl ended with a bad final answer. Identify the likely guard file and tests.",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
			Output:  "1:{\"type\":\"final\",\"content\":\"The user is asking me...\"}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Error:   "read-only workspace answers require bounded source-body or diagnostic evidence before a final answer",
		},
	}
	result, rejected := runner.readOnlyEvidenceCompletionRejection("The guard is probably in the runner.", results)

	if !rejected {
		t.Fatal("expected read-only evidence rejection before codebase evidence")
	}

	prompt := result.Error
	for _, want := range []string{"should not be reread", "different concrete code path", "source search/read/navigation"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rejection guidance missing %q:\n%s", want, prompt)
		}
	}
}

func TestHostOwnedReadOnlyCloseoutDoesNotSynthesizeAnswerableArchitectureChildPacket(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{
		typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
			SchemaVersion:   ReadOnlyEvidencePacketSchema,
			Question:        "Explain how this harness uses sub-agents.",
			SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
			InspectedPaths:  []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
			Findings: []ReadOnlyEvidenceFinding{{
				Path: "internal/runtime/agent/runner_child_tasks.go",
				Text: "The harness uses subagents through the task tool: the parent runner invokes child task execution and receives the child evidence packet.",
			}},
			Confidence: "high",
		}),
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "internal/runtime/agent/runner_child_tasks.go: runChildTask is the child task handoff.\ninternal/runtime/agent/runner.go: Runner drives the main task loop.",
		},
	}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)

	if ok {
		t.Fatalf("answerable child packet must remain model-owned, got host answer:\n%s", answer)
	}
}

func TestHostOwnedReadOnlyCloseoutUsesDebugLogArchitectureFallbackFromChildPacket(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/runtime/agent/read_only_closure.go`",
			"",
			"Key findings:",
			"- `internal/evidence/transcript/debuglog.go` owns debug log redacted mode and records secret-safe redacted log metadata instead of raw prompt content.",
			"- The implementation keeps path metadata sanitized and uses digest summaries for large raw values.",
			"Confidence: high",
		}, "\n"),
	}}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)

	if ok {
		t.Fatalf("debug-log child packet must remain model-owned, got host answer:\n%s", answer)
	}
}

func TestHostOwnedReadOnlyCloseoutDoesNotSynthesizeDiscoveryToolRecommendationFromChildPacket(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/capabilities/workspace/discovery_query.go`",
			"- `internal/capabilities/workspace/discovery_profile.go`",
			"",
			"Key findings:",
			"- `internal/capabilities/workspace/discovery_query.go` should increase discovery tool usefulness by returning source-oriented discovery evidence.",
			"- Discovery tools can increase overall usefulness when the closeout path turns profile/source evidence into final answers instead of tool inventories.",
			"Confidence: high",
		}, "\n"),
	}}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)

	if ok {
		t.Fatalf("recommendation-oriented architecture packet should remain model-synthesis evidence, not host-authored answer:\n%s", answer)
	}
}

func TestHostOwnedReadOnlyCloseoutDoesNotUseCandidateOnlyEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context packet produced candidate files",
		Output:  "Candidate files:\n- `internal/runtime/agent/runner.go`\n- `internal/runtime/agent/runner_child_tasks.go`\n",
	}}

	if answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results); ok {
		t.Fatalf("candidate-only evidence must not host-closeout, got %q", answer)
	}
}

func TestHostOwnedReadOnlyCloseoutDisabledForNoisyMentionedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "go_read_symbol",
			Summary: "Read runChildTask in internal/runtime/agent/runner_child_tasks.go lines 10-30",
			Output:  "10:func runChildTask() {}\n",
		},
		{
			Tool:    "discovery_query",
			Summary: "Discovery packet also mentioned .idiom/discovery/queries/query.json and testdata/swe/generated-logs/direct.jsonl.",
			Output:  "candidate artifacts: .idiom/discovery/queries/query.json\ntestdata/swe/generated-logs/direct.jsonl\n",
		},
	}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)

	if ok {
		t.Fatalf("host-owned read-only closeout must not synthesize final answers from noisy evidence, got %q", answer)
	}
}

func TestReadOnlyAnalysisAnswerReadyRejectsIrrelevantArchitectureSourceReads(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools to increase their usefulness?\nKey terms: query, context graph",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  "internal/runtime/agent/answer_composer.go: answer shaping\ninternal/runtime/agent/runner.go: runner loop\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/answer_composer.go lines 1-80",
			Output:  "func ComposeAnswer() string { return \"answer\" }\n",
		},
	}

	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("irrelevant answer-composer source read must not satisfy discovery-tool readiness")
	}
}

func TestReadOnlyAnalysisAnswerReadyAcceptsRelevantDiscoveryToolSourceBody(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to internal/repository/discovery/query.go to increase discovery tool usefulness?",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/repository/discovery/query.go","summary":"discovery tool surface"},{"path":"internal/repository/comprehension/trace.go","summary":"context graph trace"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read RunDiscoveryTool in internal/repository/discovery/query.go lines 1-80",
			Output:  "func RunDiscoveryTool() string { return \"discovery tool context graph\" }\n",
		},
	}

	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "go_read_symbol"}, results[1], results) {
		t.Fatal("objective-relevant discovery source body should satisfy read-only architecture readiness")
	}
}

func TestAcceptedAnswerEvidencePathsIncludeMentionedToolOutputPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "What can we add to our discovery tools to increase their usefulness?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture boundary query",
			Output: strings.Join([]string{
				"internal/runtime/agent/answer_composer.go: answer composition owns final response shaping.",
				"internal/runtime/agent/runner.go: runner owns control rails and tool orchestration.",
			}, "\n"),
		},
	}
	paths := acceptedAnswerEvidencePaths("Discovery tool improvements are grounded in the inspected evidence.", results, brief)

	for _, want := range []string{"internal/runtime/agent/answer_composer.go", "internal/runtime/agent/runner.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("accepted evidence paths missing %q: %#v", want, paths)
		}
	}
}

func TestReadFileResultHasConcreteSourceBodyExcludesDiagnosticLogs(t *testing.T) {
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
		Output:  "1:{\"type\":\"final\",\"content\":\"The user is asking me...\"}\n",
	}) {

		t.Fatal("diagnostic conversation logs must not satisfy codebase source-body evidence")
	}

	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-80",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}\n",
	}) {

		t.Fatal("generated .idiom context artifacts must not satisfy codebase source-body evidence")
	}

	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 .codex/discovery/packet.json lines 1-80",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}\n",
	}) {

		t.Fatal("generated discovery artifacts must not satisfy codebase source-body evidence")
	}

	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 transcripts/session.jsonl lines 1-80",
		Output:  "1:{\"role\":\"assistant\",\"content\":\"...\"}\n",
	}) {

		t.Fatal("top-level transcript artifacts must not satisfy codebase source-body evidence")
	}

	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 testdata/swe/generated-logs/direct.jsonl lines 1-80",
		Output:  "1:{\"type\":\"tool_result\"}\n",
	}) {

		t.Fatal("generated eval logs must not satisfy codebase source-body evidence")
	}

	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80",
		Output:  "1:package agent\n2:\n3:func example() {}\n",
	}) {

		t.Fatal("source file read should satisfy codebase source-body evidence")
	}

	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 testdata/events.jsonl lines 1-20",
		Output:  "1:{\"event\":\"fixture\"}\n",
	}) {

		t.Fatal("checked-in jsonl fixtures outside diagnostic log directories should remain source-body evidence")
	}

	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/log/reader.go lines 1-20",
		Output:  "1:package log\n2:func reader() {}\n",
	}) {

		t.Fatal("source files under internal/log must remain source-body evidence")
	}

	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/traces/reader.go lines 1-20",
		Output:  "1:package traces\n2:func reader() {}\n",
	}) {

		t.Fatal("source files under internal/traces must remain source-body evidence")
	}

	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-20",
		Output:  "1:package transcript\n2:func debuglog() {}\n",
	}) {

		t.Fatal("source files under internal/evidence/transcript must remain source-body evidence")
	}
}

func TestRepeatedDiagnosticLogReadFileBackfillsPreviousWindow(t *testing.T) {
	rewritten, raw, ok := repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total; defaulted to recent log tail; pass start_line/end_line for a different window)",
			Output:  "132:{\"type\":\"message\"}\n",
		}},
	)
	if !ok {
		t.Fatal("expected repeated unbounded diagnostic log read to become previous bounded window")
	}

	if rewritten.StartLine != 52 || rewritten.EndLine != 131 {
		t.Fatalf("window: want 52-131, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}

	for _, want := range []string{`"path":"logs/conversations/recent.jsonl"`, `"start_line":52`, `"end_line":131`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw rewrite missing %q:\n%s", want, raw)
		}
	}

	rewritten, raw, ok = repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (total lines: 211)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (total lines: 211)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
		},
	)
	if !ok {
		t.Fatal("expected repeated unbounded diagnostic log read to continue before earliest prior window")
	}

	if rewritten.StartLine != 1 || rewritten.EndLine != 51 {
		t.Fatalf("window: want 1-51, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}

	for _, want := range []string{`"start_line":1`, `"end_line":51`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw rewrite missing %q:\n%s", want, raw)
		}
	}

	rewritten, _, ok = repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	)
	if !ok {
		t.Fatal("expected middle gap before the tail to be backfilled")
	}

	if rewritten.StartLine != 52 || rewritten.EndLine != 131 {
		t.Fatalf("window: want 52-131, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}

	if _, _, ok := repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl", StartLine: 1, EndLine: 51},
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)"}},
	); ok {
		t.Fatal("explicit bounded diagnostic log reads should execute as requested")
	}
}

func TestDiagnosticLogUnboundedReadCoverageRejection(t *testing.T) {
	summary, errorText, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	)
	if !reject {
		t.Fatal("expected unbounded diagnostic log read to be rejected after beginning and tail coverage")
	}

	for _, want := range []string{"already been read back to line 1", "current tail window", "Answer from the gathered log evidence"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
			Output:  "1:{\"type\":\"message\"}\n",
		}},
	); reject {
		t.Fatal("beginning-only coverage should not block an unbounded read that can still gather the tail")
	}

	if _, _, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	); reject {
		t.Fatal("beginning plus tail with an unread middle gap should not be treated as exhausted coverage")
	}
}

func TestRepeatedDiagnosticLogReadFilePreviouslyReadRejection(t *testing.T) {
	summary, errorText, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl", StartLine: 52, EndLine: 131},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
		},
	)
	if !reject {
		t.Fatal("expected previously read diagnostic log window to be rejected")
	}

	for _, want := range []string{"bounded diagnostic log window already returned evidence", "different bounded window", "answer from the gathered evidence"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/log/reader.go", StartLine: 52, EndLine: 131},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/log/reader.go lines 52-131 (file has 211 lines total)",
			Output:  "52:func reader() {}\n",
		}},
	); reject {
		t.Fatal("source files under internal/log must not use diagnostic log repeated-window rejection")
	}
}

func TestRepeatedDiagnosticLogReadFileExactRejection(t *testing.T) {
	summary, errorText, reject := repeatedDiagnosticLogReadFileExactRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211",
			Output:  "132:{\"type\":\"message\"}\n",
		},
	)
	if !reject {
		t.Fatal("expected exact repeated diagnostic log read to be rejected")
	}

	for _, want := range []string{"Repeated diagnostic log read rejected", "different bounded `start_line`/`end_line` window", "otherwise answer"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := repeatedDiagnosticLogReadFileExactRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		workspace.ToolResult{Tool: "read_file", Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80"},
	); reject {
		t.Fatal("source-file repeated reads should keep existing replay/redirect behavior")
	}
}

func TestArchitectureCompletionRejectionRequiresEvidenceWithoutReadinessState(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "search_content", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	result, rejected := runner.completionGateRejection(
		"The harness uses subagents through delegated task calls.",
		nil,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected architecture final answer without evidence to be rejected")
	}

	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionRejectsEchoedQuestionSourceSnippet(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/capabilities/workspace/tool_schema_export.go lines 1-80",
		Output:  "1:package workspace\n2:\n3:import (\n4:\t\"encoding/json\"\n",
	}}
	result, rejected := runner.completionGateRejection(
		"For `What can we add to our discovery tools in an effort at increasing their overall usefulness?`, 1:package workspace. 2:. 3:import (. 4:\t\"encoding/json\". This is grounded in `internal/capabilities/workspace/tool_schema_export.go`.",
		results,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatal("expected echoed-question source snippet to be rejected")
	}

	if !strings.Contains(result.Error, "does not explain what the inspected code does") {
		t.Fatalf("expected meta explanation rejection, got %#v", result)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsLineOrientedSourceSnippet(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80",
		Output:  "1:package agent\n2:\n3:import (\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"For `Can you tell me how this harness uses subagents?`, 1:package agent. 2:. 3:import (. This is grounded in `internal/runtime/agent/runner.go`.",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("line-oriented source snippet should be rejected as non-answer prose, got %#v", acceptance)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsCompactCallLineFragments(t *testing.T) {
	answer := "690:\t\t(hasPath(\"internal/runtime/agent/runner_child_tasks.go\") || hasPath(\"internal/runtime/agent/runner.go\")) &&. 694:\t\thasPath(\"internal/evidence/transcript/debuglog.go\") &&. ... This is grounded in `internal/runtime/agent/read_only_closure.go`, `internal/evidence/transcript/debuglog.go`, `internal/runtime/agent/agenterrors/errors.go`, `internal/runtime/agent/answer_composer.go`."
	if !looksLikeLineOrientedSourceSnippetAnswer(answer) {
		t.Fatalf("expected compact call-like source fragments to be detected:\n%s", answer)
	}

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f49 internal/runtime/agent/read_only_closure.go lines 685-710",
		Output:  "690:\t\t(hasPath(\"internal/runtime/agent/runner_child_tasks.go\") || hasPath(\"internal/runtime/agent/runner.go\")) &&\n694:\t\thasPath(\"internal/evidence/transcript/debuglog.go\") &&\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("compact call fragments should be rejected as non-answer prose, got %#v", acceptance)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsLeadingEllipsisLineFragments(t *testing.T) {
	answer := "... 4:\treceipts   []EvidenceReceipt. 5:\tnormalizer TargetNormalizer. f3 internal/evidence/answergrounding/evidence_ledger.go. This is grounded in `internal/evidence/answergrounding/evidence_ledger.go`, `internal/capabilities/workspace/harness_audit.go`."
	if !looksLikeLineOrientedSourceSnippetAnswer(answer) {
		t.Fatalf("expected leading-ellipsis source fragments to be detected:\n%s", answer)
	}

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "read_file"},
			Objective:         "What is the role of evidence in this harness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/evidence/answergrounding/evidence_ledger.go lines 1-20",
		Output:  "1:package answergrounding\n4:\treceipts   []EvidenceReceipt\n5:\tnormalizer TargetNormalizer\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("leading-ellipsis source fragments should be rejected as non-answer prose, got %#v", acceptance)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsGroundedPacketSourceInventory(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-80",
		Output:  "type DebugLogMode string\nconst DebugLogModeRedacted DebugLogMode = \"redacted\"\ntype DebugLoggerOptions struct{}\ntype DebugLogger struct{}\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"The answer is grounded in `internal/evidence/transcript/debuglog.go`. Use those findings as the source of truth for `What does the debug log redacted mode do?`.\n\nWhat this establishes:\n- the answer is grounded in `internal/evidence/transcript/debuglog.go`: it shows defines `DebugLogMode`, references `\"redacted\"`, defines `DebugLoggerOptions`, and defines `DebugLogger`.",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected {
		t.Fatalf("grounded packet source inventory should be rejected, got %#v", acceptance)
	}
}

func TestArchitectureCompletionRejectionRejectsOffObjectiveDebugLogDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-80",
		Output:  "1:package transcript\n2:func (l *DebugLogger) sanitizeValue(value any) any { out := make(map[string]any); return out }\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`DebugLogger.sanitizeValue` in `internal/evidence/transcript/debuglog.go` is the clearest inspected implementation anchor. The inspected body shows that it assigns `out` from `make(map[string]any, len(typed))`, returns `out`, and assigns `out` from `make([]any, 0, len(typed))`.",
		results,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatal("expected off-objective debug log implementation detail to be rejected")
	}

	if !strings.Contains(result.Error, "does not address the user's objective terms") {
		t.Fatalf("expected objective-term rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionRejectsSingleAnchorSourceInventory(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nroute_summary: Read DebugLogMode in internal/evidence/transcript/debuglog.go\n\ntype DebugLogMode string\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`DebugLogMode` in `internal/evidence/transcript/debuglog.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `DebugLogMode`.",
		results,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatal("expected single-anchor source inventory to be rejected")
	}

	if !strings.Contains(result.Error, "does not explain what the inspected code does") &&
		!strings.Contains(result.Error, "non_answer_prose") {

		t.Fatalf("expected non-answer source-inventory rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionAllowsObjectiveRelevantDebugLogMechanism(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-80",
		Output:  "1:package transcript\n2:const DebugLogModeRedacted = \"redacted\"\n3:func (l *DebugLogger) sanitizeValue(value any) any { return \"[redacted]\" }\n",
	}}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` sanitizes values before logging by replacing raw transcript fields with redacted summaries. This is grounded in `internal/evidence/transcript/debuglog.go`.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatal("objective-relevant debug log mechanism should be accepted")
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsInsufficientEvidenceAfterAnswerableRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner_child_tasks.go lines 1-80",
		Output:  "func (r *Runner) runChildTask() { _ = \"task subagent child runner\" }\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"I could not produce a supported final answer from this run: the run stopped before it retained enough evidence, so I do not have enough supported evidence to answer that fully.",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.Reason != "answerable_evidence_reported_as_insufficient" {
		t.Fatalf("answerable read should not accept insufficient-evidence prose, got %#v", acceptance)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsUnsupportedCloseoutWithoutAnswerability(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "context_query", "read_file"},
			Objective:         "What is the role of evidence in this harness? Where is it from, how is it used?",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "task",
			Summary: "Child task completed",
			Output: strings.Join([]string{
				"Partial findings from this turn:",
				"Inspected evidence:",
				"- `internal/runtime/localops/quickstart.go`",
			}, "\n"),
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/pack.json with candidate files",
			Output:  `{"candidate_files":[{"path":"internal/contracts/plan/source_satisfied.go"}]}`,
		},
	}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"I could not produce a supported final answer from this run: the run stopped before it retained enough evidence, so I do not have enough supported evidence to answer that fully. The current read-only answer has relevant evidence in `internal/runtime/agent/git_history_requirement.go`, but no concrete user-facing finding was retained.",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.Reason != "insufficient_evidence_answer" {
		t.Fatalf("unsupported architecture closeout must not be accepted as ready, got %#v", acceptance)
	}
}

func TestArchitectureCompletionRejectionAllowsObjectiveRelevantSubagentAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner_child_tasks.go lines 1-80",
		Output:  "func (r *Runner) runChildTask() { /* task tool delegates to child runner */ }\n",
	}}
	_, rejected := runner.completionGateRejection(
		"The harness uses subagents through the task tool: the parent runner delegates to a bounded child runner, then returns the child packet as evidence for the parent answer. This is grounded in `internal/runtime/agent/runner_child_tasks.go`.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatal("objective-relevant subagent explanation should be accepted")
	}
}

func TestArchitectureCompletionRejectionRejectsDiscoveryToolInventory(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "code_navigate"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/capabilities/workspace/discovery_query.go lines 1-80",
		Output:  "package workspace\n\nfunc RunDiscoveryQuery() {}\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"Related files: Found 2 related Go file(s). Symbol index: No Go workspace symbols matched \"idiom|discovery|surface|context|graph|current|discovery[-_ ]?query|recipes\". capability: file_outline. language: go. This is grounded in `internal/capabilities/workspace/discovery_query.go`, `internal/runtime/agent/runner_child_tasks.go`, `internal/runtime/agent/runner.go`, `internal/labels/labels.go`.",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected {
		t.Fatal("expected discovery/tool inventory to be rejected")
	}

	if acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("expected non-answer evidence-inventory rejection, got %#v", acceptance)
	}
}

func TestArchitectureCompletionRejectionAllowsDiscoveryUsefulnessMechanism(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"discovery_query", "read_file"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80",
		Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
	}, {
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner_lane_policy.go lines 1-80",
		Output:  "package agent\n\nfunc conversationEvidenceSatisfied() bool { return true }\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`internal/runtime/agent/runner.go` contains the runner `Run` method. `internal/runtime/agent/runner_lane_policy.go` contains `conversationEvidenceSatisfied`, the readiness check present in the inspected source. Together, those inspected anchors show the useful integration point: discovery output has to connect into runner closeout and readiness checks before it can support final synthesis.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("objective-relevant discovery usefulness mechanism should be accepted, got %#v", result)
	}
}
