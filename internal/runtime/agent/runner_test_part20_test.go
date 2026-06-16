package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestMutationCompletionRejectsUnresolvedRejectedWriteTarget(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "implementation_result",
			TaskFamily:        "implementation",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"edit_file", "shell"},
			Objective:         "Add terminal-style input history navigation.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/surfaces/tui/app/model_state.go",
		Output:  "Changed paths:\n- internal/surfaces/tui/app/model_state.go\n",
	}, {
		Tool:    "edit_file",
		Summary: "Tool not available in current runtime contract",
		Output:  "Rejected write target: `internal/surfaces/tui/app/update_handlers.go`",
		Error:   "edit_file is not in the current allowed tool surface.",
	}, {
		Tool:    "shell",
		Summary: "Ran shell command in .",
		Output:  "ok\tidiom/internal/surfaces/tui/app\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"Implemented the requested change.\n\nChanged paths:\n- internal/surfaces/tui/app/model_state.go\n\nVerification: go test ./internal/surfaces/tui/app passed",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "rejected_mutation_paths_remaining" {
		t.Fatalf("expected unresolved rejected write target to block completion, got %#v", acceptance)
	}

	if len(acceptance.EvidencePaths) == 0 || !slices.Contains(acceptance.EvidencePaths, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected unresolved rejected path in evidence paths, got %#v", acceptance.EvidencePaths)
	}
}

func TestMutationCompletionIgnoresRecoveredOutOfScopeRejectedWriteTarget(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "implementation_result",
			TaskFamily:        "implementation",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"edit_file", "shell"},
			RequiredEditPaths: []string{"internal/labels/labels.go"},
			Objective:         "Update ShortLabel without editing tests.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edit request rejected",
		Output:  "Rejected write target: `internal/labels/labels_test.go`",
		Error:   "edit_file requires source evidence before editing.",
	}, {
		Tool:    "edit_file",
		Summary: "Edited internal/labels/labels.go",
		Output:  "Changed paths:\n- internal/labels/labels.go\n",
	}, {
		Tool:    "shell",
		Summary: "Ran shell command in .",
		Output:  "ok\texample.com/frozen-label-repo/internal/labels\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"Implemented the requested change.\n\nChanged paths:\n- internal/labels/labels.go\n\nVerification: go test ./internal/labels passed",
		results,
	)

	if acceptance.Decision != finalAnswerAcceptanceAccepted {
		t.Fatalf("out-of-scope rejected write should not keep required mutation open, got %#v", acceptance)
	}
}

func TestRejectedWriteResultRetainsRequestTarget(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "internal/surfaces/tui/app/update_handlers.go"}
	result := resultWithRejectedRequestTarget(req, workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Tool not available in current runtime contract",
		Error:   "edit_file is not in the current allowed tool surface.",
	})

	if !strings.Contains(result.Output, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected rejected write result to retain target path, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionAllowsAnswerAfterEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file"},
			Objective:         "What does ShortLabel return in internal/labels/labels.go?",
		},
	}
	_, rejected := runner.completionGateRejection(
		"The inspected source shows `ShortLabel` returns \"Save changes\".",
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"}},
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("did not expect read-only final answer to be rejected after source evidence")
	}
}

func TestReadOnlyEvidenceCompletionRejectionRejectsNonSubstantiveAnswerAfterEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
			Objective:         "What does internal/labels/labels.go ShortLabel return?",
		},
	}
	result, rejected := runner.completionGateRejection(
		"tool",
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"}},
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected non-substantive read-only answer to be rejected")
	}

	if !strings.Contains(result.Error, "substantive user-facing answer") {
		t.Fatalf("expected substantive-answer rejection, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionDoesNotCountEmptyRoutedSymbolMiss(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"code_navigate", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	_, rejected := runner.completionGateRejection(
		"No Go symbols matched `save`.",
		[]workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"save\"",
		}},
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("symbol miss without source body must not satisfy read-only evidence")
	}
}

func TestReadOnlyExplanationCompletionRejectionRejectsUnsupportedExtraDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
		Output:  "func (r *Runner) runChildTask(...) workspace.ToolResult { return workspace.ToolResult{} }",
	}}
	result, rejected := runner.completionGateRejection(
		"`Runner.runChildTask` creates the read-only child runner, and `Runner.runWorkbranchChildTask` applies the write-enabled variant.",
		results,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected unsupported extra symbol detail to be rejected")
	}

	if !strings.Contains(result.Error, "runWorkbranchChildTask") {
		t.Fatalf("expected rejection to name unsupported detail, got %#v", result)
	}
}

func TestReadOnlyExplanationCompletionRejectionRejectsContradictoryBinaryVerdict(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "review_findings",
			TaskFamily:        "security_review",
			PermissionProfile: "read_only_workspace",
			Objective:         "Review whether Unicode before a file extension can panic.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"route_summary: Read normalizeDelegatedDiscoveryEvidencePath in internal/runtime/agent/runner_delegated_synthesis.go lines 1305-1331",
			"1305:func normalizeDelegatedDiscoveryEvidencePath(path string) string {",
			"1315:\t\tif idx := indexFold(path, ext); idx >= 0 {",
			"1331:}",
		}, "\n"),
	}}

	result, rejected := runner.completionGateRejection(strings.Join([]string{
		"**Finding: Yes, Unicode before a file extension can cause a panic via `indexFold`.**",
		"",
		"**Verdict: No panic.** Go string slicing at arbitrary byte offsets does not panic.",
	}, "\n"), results, 1, nil, false)
	if !rejected {
		t.Fatal("expected contradictory binary verdict to be rejected")
	}
	if !strings.Contains(result.Error, "contradictory yes/no conclusions") {
		t.Fatalf("expected contradiction error, got %#v", result)
	}

	if _, rejected := runner.completionGateRejection("**Finding: No.** The inspected source can corrupt Unicode boundaries, but Go string slicing does not panic.", results, 1, nil, false); rejected {
		t.Fatal("consistent binary finding should not be rejected")
	}
}

func TestReadOnlyExplanationCompletionRejectionAcceptsComplexSourceBackedDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "go_read_symbol",
			Summary: "Read Foo in internal/surfaces/tui/app/foo.go lines 10-20",
			Output:  "func Foo() { return }",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/bar.go lines 1-8",
			Output: strings.Join([]string{
				"1:package app",
				"2:type BarHandler struct{}",
				"3:type DebugLoggerOptions struct{}",
				"4:const system_prompt_digest = \"sha256:abc123\"",
			}, "\n"),
		},
	}
	_, rejected := runner.completionGateRejection(
		"`Foo` is the inspected entry point, and the same inspected source body also defines `BarHandler`, `DebugLoggerOptions`, `system_prompt_digest`, and the `sha256:` digest marker.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("complex source-backed answer was rejected")
	}
}

func TestReadOnlyExplanationCompletionRejectionAcceptsRedactedModeDetailsFromInspectedBodies(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-70",
			Output: strings.Join([]string{
				"19:DebugLogModeRedacted DebugLogMode = \"redacted\"",
				"20:DebugLogModeFull DebugLogMode = \"full\"",
				"23:type DebugLoggerOptions struct {",
				"44:type DebugLogger struct {",
				"59:SystemPrompt string `json:\"system_prompt,omitempty\"`",
				"60:SystemPromptDigest string `json:\"system_prompt_digest,omitempty\"`",
				"61:WorkspaceRoot string `json:\"workspace_root,omitempty\"`",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/evidence/transcript/debuglog_test.go lines 40-76",
			Output: strings.Join([]string{
				"40:if err := logger.LogMessage(\"user\", \"api_key=secret123 path=/tmp/workspace/config.json\"); err != nil {",
				"51:if digest := records[0][\"system_prompt_digest\"]; digest == nil || !strings.HasPrefix(digest.(string), \"sha256:\") {",
				"54:if records[0][\"workspace_root\"] != \".\" {",
			}, "\n"),
		},
	}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` is the redacted `DebugLogger` mode; the inspected source and tests show `DebugLoggerOptions`, `SystemPrompt`, `system_prompt`, `system_prompt_digest`, the `sha256:` marker, `workspace_root`, `secret123`, and the contrasting `DebugLogModeFull` mode.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("redacted-mode details from inspected source bodies were rejected")
	}
}

func TestReadOnlyExplanationCompletionRejectionAcceptsOutlineDeclarationNames(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 4 top-level declaration(s) in internal/evidence/transcript/debuglog.go.",
			"declarations:",
			"- DebugLogMode lines 16-16 tags: core",
			"- DebugLogModeRedacted, DebugLogModeFull lines 18-21 tags: core",
		}, "\n"),
	}}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` is the redacted debug log mode declaration shown in the inspected outline.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("outline declaration names should support declaration mentions")
	}
}

func TestReadOnlyExplanationCompletionRejectionAllowsToolSurfaceVocabulary(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-8",
		Output:  "1:package agent\n2:// Runner coordinates workspace tools.\n3:type Runner struct {}\n",
	}}
	_, rejected := runner.completionGateRejection(
		"The discovery tool surface should distinguish `structural_search`, `context_query`, `code_navigate`, `context_pack`, `context_impact`, `history_range`, `history_symbol`, `discovery_query`, `context_trace`, `search_content`, `read_file`, `file_stats`, `harness_audit`, `discovery_state`, `context_merge`, and `progressive_refinement` as tool or proposal vocabulary rather than inspected source declarations.",
		results,
		1,
		nil,
		false,
	)

	if rejected {
		t.Fatalf("architecture tool/proposal vocabulary should not be rejected as unsupported source symbols")
	}
}

func TestReadOnlyExplanationCompletionRejectionDoesNotLetUncertaintyWaiveOtherUnsupportedDetails(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
		Output:  "func (r *Runner) runChildTask(...) workspace.ToolResult { return workspace.ToolResult{} }",
	}}
	result, rejected := runner.completionGateRejection(
		"`Runner.runChildTask` may include other setup. Separately, `Runner.runWorkbranchChildTask` applies the write-enabled variant.",
		results,
		1,
		nil,
		false,
	)

	if !rejected {
		t.Fatalf("expected unsupported detail outside the uncertain claim to be rejected")
	}

	if !strings.Contains(result.Error, "runWorkbranchChildTask") {
		t.Fatalf("expected rejection to name unsupported detail, got %#v", result)
	}
}

func TestClosurePromptEvidencePathsDropsArtifacts(t *testing.T) {
	got := closurePromptEvidencePaths([]string{
		".idiom/discovery/queries/query.json",
		"start_line/end_line",
		"search/read",
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go:93-94",
		"idiom/internal/frontier",
	}, 0)
	want := []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected prompt evidence paths %#v, got %#v", want, got)
	}
}

func TestGeneratedEvidenceArtifactReadRedirectNamesSourcePaths(t *testing.T) {
	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: ".idiom/discovery/queries/query.json",
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture discovery",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths","output_excerpt":"internal/runtime/agent/runner.go:128: finish\ninternal/runtime/agent/runner_child_tasks.go:16: runChildTask"}]}`,
	}}
	result, ok := generatedEvidenceArtifactReadRedirectResult(req, results)

	if !ok {
		t.Fatal("expected generated evidence artifact read to redirect to source paths")
	}

	if result.Error != "" {
		t.Fatalf("soft redirect should not be a rejection, got error %q", result.Error)
	}

	for _, want := range []string{"generated discovery/context JSON is not source-body evidence", "internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("redirect output missing %q:\n%s", want, result.Output)
		}
	}

	if strings.Contains(result.Output, "- .idiom/discovery/queries/query.json") {
		t.Fatalf("redirect must not list artifact as source evidence:\n%s", result.Output)
	}

	redirected, raw, reason, ok := generatedEvidenceArtifactReadRedirectRequest(req, results)
	if !ok {
		t.Fatal("expected generated evidence artifact read to rewrite to source request")
	}

	if redirected.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected redirect to first source path, got %#v", redirected)
	}

	if !strings.Contains(raw, `"path":"internal/runtime/agent/runner.go"`) || reason == "" {
		t.Fatalf("expected raw rewritten request and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestStructuredSourceReadRewriteRunsCodeNavigateInsteadOfControlRedirect(t *testing.T) {
	ws := newGoTestWorkspace(t)
	brief := workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "main.go",
		NamedGap:        "verify the file declaration outline before answering",
		WhyNotAnswering: "without the declaration boundary the answer may cite the wrong symbol",
	}

	rewritten, raw, reason, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file", "code_navigate"}, brief)
	if !ok {
		t.Fatal("expected read-only source read to rewrite to structured navigation")
	}

	if rewritten.Tool != "code_navigate" || rewritten.Capability != "file_outline" || rewritten.Path != "main.go" {
		t.Fatalf("unexpected rewrite: %#v", rewritten)
	}

	if rewritten.NamedGap != req.NamedGap || rewritten.WhyNotAnswering != req.WhyNotAnswering {
		t.Fatalf("rewrite should preserve post-readiness contract fields: %#v", rewritten)
	}

	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"file_outline"`) {
		t.Fatalf("rewritten raw should execute code_navigate file_outline, got %s", raw)
	}

	if reason != "source read redirected to structured navigation" {
		t.Fatalf("unexpected reason %q", reason)
	}

	if _, _, _, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file"}, brief); ok {
		t.Fatal("rewrite must not fire when code_navigate is not visible")
	}

	mutationBrief := brief

	mutationBrief.PermissionProfile = "workspace_write"
	mutationBrief.Contract = "plan_execute"

	if _, _, _, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file", "code_navigate"}, mutationBrief); ok {
		t.Fatal("rewrite must not override mutation-lane source-read policy")
	}
}

func TestPromptEvidencePathsFromToolResultsDropsPromptArtifacts(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Inspected paths:\n- `.idiom/discovery/queries/query.json`\n- `internal/runtime/agent/runner.go`\n- `internal/runtime/agent/runner_child_tasks.go:93-94`\n- `search/read`\n",
	}}, 6)
	want := []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected sanitized prompt evidence paths %#v, got %#v", want, got)
	}
}

func TestPromptEvidencePathsFromToolResultsPrefersExactProbePathsOverEarlyScaffolding(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{
		{
			Tool:   "discovery_query",
			Output: "codex/README.md\ncodex/project-context.md\ncodex/AGENTS.md\ninternal/runtime/agent/runner_child_tasks.go\ninternal/capabilities/workspace/tool_schemas.go\n",
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
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line at internal/capabilities/workspace/tool_schemas.go:85.",
			Output:  "internal/capabilities/workspace/tool_schemas.go:85: Delegate a bounded evidence-gathering question to a sub-agent.",
		},
	}, 4)
	if len(got) == 0 || got[0] != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected exact semantic probe path to be prioritized, got %#v", got)
	}

	for _, forbidden := range []string{"codex/README.md", "codex/project-context.md", "codex/AGENTS.md"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("expected prompt scaffolding path %q to be deprioritized, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyDirectAnswerPresentationUsesLedgerRelevantSourcePaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
		EvidencePaths:     []string{"internal/surfaces/tui/app/prompt_candidate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/surfaces/tui/app/broad_candidate.go\ninternal/surfaces/tui/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	}

	got := runnerEvidencePathsForBrief(brief, results, 8)
	if !slices.Contains(got, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("expected direct-answer evidence paths to contain ledger source path, got %#v", got)
	}

	for _, forbidden := range []string{"internal/surfaces/tui/app/broad_candidate.go", "internal/surfaces/tui/app/other_candidate.go", "internal/surfaces/tui/app/prompt_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("direct-answer presentation must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}

	live := (&Runner{RuntimeBrief: brief}).runtimeBriefWithLiveState(RunPolicy{}, nil, results)
	if slices.Contains(live.EvidencePaths, "internal/surfaces/tui/app/prompt_candidate.go") {
		t.Fatalf("direct-answer live brief must replace broad prompt evidence paths, got %#v", live.EvidencePaths)
	}
}

func TestRuntimeBriefDirectAnswerPreservesAdvisoryEvidenceBeforeAnswerability(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
		EvidencePaths:     []string{"internal/surfaces/tui/app/prompt_candidate.go"},
	}

	live := (&Runner{RuntimeBrief: brief}).runtimeBriefWithLiveState(RunPolicy{}, nil, nil)
	if !slices.Contains(live.EvidencePaths, "internal/surfaces/tui/app/prompt_candidate.go") {
		t.Fatalf("direct-answer live brief should preserve advisory evidence before answerability, got %#v", live.EvidencePaths)
	}
}

func TestRunnerEvidencePathsPreservesBroadCandidatesForArchitecture(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/surfaces/tui/app/broad_candidate.go\ninternal/surfaces/tui/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	}

	got := runnerEvidencePathsForBrief(brief, results, 8)
	if !slices.Contains(got, "internal/surfaces/tui/app/broad_candidate.go") {
		t.Fatalf("non-direct-answer presentation should retain broad candidate paths, got %#v", got)
	}
}

func TestEmitAcceptedAnswerSignalsDirectAnswerBlockedUsesLedgerRelevantSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain chat input newline rendering.",
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
		"The issue is in `internal/surfaces/tui/app/broad_candidate.go`.",
		[]workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "Context pack wrote packet",
				Output:  "internal/surfaces/tui/app/broad_candidate.go\ninternal/surfaces/tui/app/other_candidate.go\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-6",
				Output: strings.Join([]string{
					"1:package app",
					"2:",
					"3:// chat input newline rendering is handled by this function.",
					"4:func renderChatInputNewline() {}",
				}, "\n"),
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

	if !slices.Contains(got, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("blocked direct-answer readiness should name ledger source path, got %#v", got)
	}

	for _, forbidden := range []string{"internal/surfaces/tui/app/broad_candidate.go", "internal/surfaces/tui/app/other_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("blocked direct-answer readiness must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}
}

func TestEmitReadOnlyAnswerReadyEventDirectAnswerUsesLedgerRelevantSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain chat input newline rendering.",
		},
	}

	var payload map[string]any

	runner.OnEvent = func(event RunEvent) {
		if event.Role != "read_only_answer_ready" {
			return
		}

		_ = json.Unmarshal([]byte(event.Content), &payload)
	}
	runner.emitReadOnlyAnswerReadyEvent(workspace.ToolRequest{Tool: "read_file"}, []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/surfaces/tui/app/broad_candidate.go\ninternal/surfaces/tui/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	})

	rawPaths, _ := payload["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))

	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}

	if !slices.Contains(got, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("direct-answer ready event should name ledger source path, got %#v", got)
	}

	for _, forbidden := range []string{"internal/surfaces/tui/app/broad_candidate.go", "internal/surfaces/tui/app/other_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("direct-answer ready event must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyAnswerReadyPresentationUsesImplementationOwnerPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "what would it take to enable the up and down arrows to fill the chat input with history entries?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output: strings.Join([]string{
				"internal/runtime/agent/runner.go",
				"internal/runtime/agent/git_history_requirement.go",
				"internal/runtime/agent/final_answer_decorator_test.go",
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180",
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"23:\tswitch msg.Type {",
				"24:\tcase tea.KeyUp:",
				"25:\t\tm.history.selected--",
				"26:\tcase tea.KeyDown:",
				"27:\t\tm.history.selected++",
				"28:\t\tm.input.SetValue(m.history.entries[m.history.selected].content)",
				"29:\t}",
				"30:\treturn nil, true",
				"31:}",
			}, "\n"),
		},
	}

	paths := readOnlyDirectAnswerPresentationEvidencePaths(brief, results, 8)
	if !slices.Contains(paths, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected implementation owner path in presentation evidence, got %#v", paths)
	}

	for _, forbidden := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/git_history_requirement.go", "internal/runtime/agent/final_answer_decorator_test.go"} {
		if slices.Contains(paths, forbidden) {
			t.Fatalf("presentation evidence must not substitute candidate path %q, got %#v", forbidden, paths)
		}
	}

	nudge := buildReadinessNudge(ReadinessNudge{
		Headline:          "The read-only evidence is sufficient for a grounded answer to this question.",
		EvidencePathLabel: "Core evidence paths: ",
		EvidencePaths:     closurePromptEvidencePaths(readOnlyPresentationEvidencePathsForBrief(brief, results, 4), 4),
		Objective:         strings.TrimSpace(brief.Objective),
		Results:           results,
		ProbeBudget:       2,
		ContinuationScope: "answer_readiness_reached",
		FooterLines: []string{
			"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit.",
		},
	})
	if !strings.Contains(nudge, "`internal/surfaces/tui/app/update_handlers.go`") {
		t.Fatalf("expected readiness nudge to cite implementation owner path, got:\n%s", nudge)
	}

	if strings.Contains(nudge, "`internal/runtime/agent/runner.go`") || strings.Contains(nudge, "`internal/runtime/agent/git_history_requirement.go`") {
		t.Fatalf("readiness nudge must not fall back to generic discovery candidates, got:\n%s", nudge)
	}

	if strings.Contains(nudge, "core implementation paths") {
		t.Fatalf("readiness nudge must not overclaim implementation ownership, got:\n%s", nudge)
	}
}

func TestPromptEvidencePathsFromToolResultsKeepsDirectTestProbePath(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f5 cmd/idiom/product_workflow_test.go lines 1-6721",
			Output: strings.Join([]string{
				"1:package main",
				"25:internal/workflows/productworkflow/scope.go",
				"26:internal/workflows/productworkflow/validate.go",
				"27:cmd/idiom/product_workflow.go",
			}, "\n"),
		},
	}, 4)
	if len(got) == 0 || got[0] != "cmd/idiom/product_workflow_test.go" {
		t.Fatalf("expected direct test-file probe path to stay pinned first, got %#v", got)
	}
}

func TestRunnerEvidencePathsForMutationDoNotPinOutputMentionedFixturePaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"internal/workflows/productworkflow/coverage_gate.go",
			"internal/workflows/productworkflow/coverage_gate_test.go",
			"cmd/idiom/product_workflow_test.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-338",
			Output: strings.Join([]string{
				`record("ears", requireBehavior, earsPass, "...", "tdd.json", "coverage_ears_incomplete")`,
				`record("evidence_acceptance", true, evidencePass, "...", "evidence/traceability_matrix.json", "coverage_evidence_acceptance_incomplete")`,
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"internal/workflows/productworkflow/analysis_agents.go",
				"internal/workflows/productworkflow/implementation.go",
				"cmd/idiom/implementation.go",
			}, "\n"),
		},
	}

	evidence := runnerEvidencePathsForBrief(brief, results, 8)
	if !reflect.DeepEqual(evidence, []string{"internal/workflows/productworkflow/coverage_gate.go"}) {
		t.Fatalf("expected mutation evidence to keep only direct scoped probes, got %#v", evidence)
	}

	pinned := runnerPinnedPathsForBrief(brief, results, 8)
	for _, want := range brief.RequiredEditPaths {
		if !slices.Contains(pinned, want) {
			t.Fatalf("expected pinned mutation paths to retain required path %q, got %#v", want, pinned)
		}
	}

	for _, forbidden := range []string{"internal/workflows/productworkflow/analysis_agents.go", "internal/workflows/productworkflow/implementation.go", "cmd/idiom/implementation.go", "tdd.json", "evidence/traceability_matrix.json"} {
		if slices.Contains(evidence, forbidden) || slices.Contains(pinned, forbidden) {
			t.Fatalf("did not expect output-mentioned fixture path %q in mutation evidence=%#v pinned=%#v", forbidden, evidence, pinned)
		}
	}
}

func TestPostChildClosureEvidencePathsPreservesMultiFilePacketCoverage(t *testing.T) {
	got := postChildClosureEvidencePaths("Explain how this harness uses subagents.", []workspace.ToolResult{{
		Tool: "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n" +
			"- `.idiom/discovery/queries/query.json`\n" +
			"- `runner.go`\n" +
			"- `internal/runtime/agent/runner.go`\n" +
			"- `internal/runtime/agent/runner_child_tasks.go:93-94`\n" +
			"- `internal/runtime/agent/answer_composer.go`\n" +
			"- `search/read`\n\n" +
			"Key findings:\n" +
			"- The harness uses subagents through the task runner path in internal/runtime/agent/runner.go and internal/runtime/agent/runner_child_tasks.go; internal/runtime/agent/answer_composer.go shapes the final response.\n",
	}}, 6)
	want := []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/answer_composer.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected post-child evidence paths %#v, got %#v", want, got)
	}
}

func TestPostChildClosureEvidencePathsUsesWeakPacketAnchorsForFollowup(t *testing.T) {
	got := postChildClosureEvidencePaths("Can you tell me how this harness uses subagents?", []workspace.ToolResult{{
		Tool: "task",
		Output: strings.Join([]string{
			"I could not produce a final grounded answer yet.",
			"",
			"Inspected evidence:",
			"- `internal/runtime/agent/runner_child_tasks.go`",
			"- `internal/runtime/agent/runner_task_ledger.go`",
			"- `docs/debugging-reviewer-bench.md`",
		}, "\n"),
	}}, 4)
	want := []string{
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/runner_task_ledger.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected weak child packet to constrain source follow-up paths %#v, got %#v", want, got)
	}
}

func TestDelegatedClosureEvidencePathsRetainsDiscoveryPacketAfterExactProbe(t *testing.T) {
	got := delegatedClosureEvidencePaths("How does this harness utilize sub-agents?", []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran function_tracing discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read flow","output_excerpt":"internal/runtime/agent/runner.go:648 runChildTask\ninternal/runtime/agent/runner_child_tasks.go:16 runChildTask\ninternal/runtime/agent/answer_composer.go:18 Compose\ninternal/runtime/agent/runner_test.go:1 package agent"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-131",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
	}, 6)
	want := []string{
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/answer_composer.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected delegated closure evidence paths %#v, got %#v", want, got)
	}
}

func TestClosurePromptEvidencePathsPrefersImplementationOverTests(t *testing.T) {
	got := closurePromptEvidencePaths([]string{
		"internal/runtime/agent/runner_test.go",
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	}, 0)
	want := []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected non-test closure evidence paths %#v, got %#v", want, got)
	}
}

func TestClosureDecisionRationaleUsesActualPacketCoverage(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/runtime/agent/runner_child_tasks.go`\n",
	}}
	validity, diminishing := closureDecisionRationale("Explain how this harness uses subagents.", results, nil)

	if !strings.Contains(validity, "internal/runtime/agent/runner_child_tasks.go") {
		t.Fatalf("expected validity text to cite actual packet path, got %q", validity)
	}

	for _, forbidden := range []string{"handoff classifier", "answer-composer path"} {
		if strings.Contains(validity, forbidden) {
			t.Fatalf("did not expect broad subagent coverage claim %q in %q", forbidden, validity)
		}
	}

	if !strings.Contains(diminishing, "named gap") {
		t.Fatalf("expected diminishing-returns text to stay gap-shaped, got %q", diminishing)
	}
}

func TestClosureDecisionRationaleDoesNotOverclaimImplementationOwnership(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: "route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 133-263\n" +
			"133:type Model struct {\n" +
			"249:\tcurrentTurnHistoryIndex int\n" +
			"258:\thistory historyState\n" +
			"259:}\n",
	}}
	validity, _ := closureDecisionRationale("what would it take to enable the up and down arrows to fill the chat input with history entries?", results, []string{"internal/surfaces/tui/app/model_state.go"})

	if !strings.Contains(validity, "inspected source paths") {
		t.Fatalf("expected inspected-source rationale, got %q", validity)
	}

	if strings.Contains(validity, "core implementation paths") {
		t.Fatalf("readiness rationale must not overclaim implementation ownership, got %q", validity)
	}
}

func TestRewriteVisibilityNoteExplainsRedirect(t *testing.T) {
	note := rewriteVisibilityNote("search_content", "task")
	if !strings.Contains(note, "`search_content`") || !strings.Contains(note, "`task`") {
		t.Fatalf("expected note to name both tools, got %q", note)
	}

	if !strings.Contains(note, "rewritten call will execute") {
		t.Fatalf("expected note to confirm execution, got %q", note)
	}
}

func TestNormalizeRunnerToolRequestIgnoresPostReadinessReasoningFields(t *testing.T) {
	a := normalizedToolRequestRaw(workspace.ToolRequest{
		Tool:            "search_content",
		Pattern:         "runner",
		Path:            "internal",
		NamedGap:        "find the exact stop-condition branch",
		WhyNotAnswering: "I still need the deciding condition",
	})
	b := normalizedToolRequestRaw(workspace.ToolRequest{
		Tool:            "search_content",
		Pattern:         "runner",
		Path:            "internal",
		NamedGap:        "a different phrasing",
		WhyNotAnswering: "different rationale text",
	})

	if a != b {
		t.Fatalf("expected normalized request fingerprint to ignore rationale fields, got %q vs %q", a, b)
	}
}

func TestRunnerReplaysEquivalentReadRequestWithNormalizedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"./README.md","start_line":1,"end_line":1}`,
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":1}`,
		"Reused prior evidence instead of rereading the same file.",
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

	var replayed bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Read README twice if needed."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != "Reused prior evidence instead of rereading the same file." {
		t.Fatalf("unexpected answer: %q", answer)
	}

	if !replayed {
		t.Fatal("expected equivalent path variant to replay prior evidence")
	}
}

// Soft-failure nudge — runner must fire rejection_recovery_nudge when three
// tool calls in a row return errors against the same path stem, even though
// none of them are "hard rejections." Mirrors the failure mode we saw in
// logs/conversations/20260420-210101.488.jsonl where the model called
// read_file on five fabricated files inside a non-existent directory.
