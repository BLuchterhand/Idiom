package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestReadOnlyCloseoutRejectsLowValueSourceInventoryAcrossConsumers(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "what would it take to enable up and down arrow keys to iterate through the user's chat history?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/app.go lines 1-8",
		Output: strings.Join([]string{
			"1:package app",
			"2:",
			"3:func truncateText(value string, max int) string {",
			"4:\treturn value",
			"5:}",
			"6:func normalizeHistoryRole(value string) string {",
			"7:\treturn strings.ToUpper(value[:1]) + value[1:]",
			"8:}",
		}, "\n"),
	}}
	answer := "For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/surfaces/tui/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`."

	closeout := readOnlyClosureDecisionForResults(brief, results)
	if closeout.AllowsSourceInventoryAnswer(answer, true) {
		t.Fatal("closeout contract must not allow low-value source inventory as an accepted answer")
	}

	runner := &Runner{RuntimeBrief: brief}
	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)

	if acceptance.Decision != finalAnswerAcceptanceRejected {
		t.Fatalf("acceptance should reject low-value source inventory through closeout contract: %#v", acceptance)
	}

	presented, ok := closeout.PresentableFallbackCandidate("main", answer)
	if !ok {
		t.Fatal("expected closeout contract to provide a user-facing replacement")
	}

	for _, forbidden := range []string{"truncateText", "normalizeHistoryRole"} {
		if strings.Contains(presented, forbidden) {
			t.Fatalf("replacement leaked low-value source inventory %q:\n%s", forbidden, presented)
		}
	}

	if !strings.Contains(presented, "not have enough supported evidence") {
		t.Fatalf("replacement should be an evidence-bounded uncertainty answer:\n%s", presented)
	}
}

func TestReadOnlyCloseoutDoesNotProduceHostAnswerFromLocalizationEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Why did the latest run fail?",
	}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "logs/conversations/20260604-195321.801.jsonl:20: completion_gate_repeated_rejection",
	}}
	if answer, ok := (&Runner{RuntimeBrief: brief}).hostOwnedReadOnlyCloseoutAnswer(results); ok {
		t.Fatalf("read-only localization evidence must not produce a host-authored answer:\n%s", answer)
	}
}

func TestReadOnlyCloseoutHostOwnedAnswerRejectsSynthesizedDiscoveryInventory(t *testing.T) {
	answer := "Symbol index: Indexed symbols under internal/runtime/agent with ctags. This is grounded in `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_child_tasks.go`."
	if hostOwnedCloseoutAnswerIsPresentable(answer) {
		t.Fatalf("host-owned closeout must reject synthesized discovery inventory:\n%s", answer)
	}
}

func TestReadOnlyDirectAnswerHostCloseoutDoesNotUseInventoryWhenAnswerable(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "What is the role of evidence in the harness?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/source_evidence_state.go lines 1-9",
		Output: strings.Join([]string{
			"1:package agent",
			"2:// Evidence records inspected paths and source-body support for harness readiness.",
			"3:type SourceEvidenceState struct { SourceBodyPaths []string }",
			"4:func evidenceReadiness() {}",
		}, "\n"),
	}}

	closeout := readOnlyClosureDecisionForResults(brief, results)
	if !closeout.Answerable() {
		t.Fatalf("test must exercise answerable direct-answer evidence: %#v", closeout)
	}

	if answer, ok := closeout.HostOwnedAnswer("main"); ok {
		t.Fatalf("direct-answer host closeout must not turn answerable evidence into an inventory fallback:\n%s", answer)
	}
}

func TestArchitectureHostOwnedAnswerRejectsOffObjectiveSourceInventoryAfterChildTask(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
		Contract:          string(workerruntime.ContractArchitecture),
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "task",
			Summary: "Child task completed",
		},
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 11 layer(s); artifact: .idiom/discovery/queries/5b255b668a9eb910.json",
			Output: strings.Join([]string{
				"internal/capabilities/workspace/tool_manifest.go",
				"internal/capabilities/workspace/manager.go",
				"internal/capabilities/workspace/tool_schema_export.go",
				"internal/capabilities/workspace/task_tool.go",
				"internal/runtime/agent/runner.go",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f54 internal/capabilities/workspace/tool_manifest.go lines 140-200 (total lines: 312)",
			Output: strings.Join([]string{
				"140:func IsWorkspaceWriteTool(tool string) bool {",
				"141:\treturn slices.Contains(WorkspaceWriteToolNames(), strings.TrimSpace(tool))",
				"142:}",
				"143:func BuildToolManifest(allowedTools []string) []ToolManifestEntry {",
				"144:\tentries := []ToolManifestEntry{",
				"145:\t\t{Tool: \"task_ledger\", Purpose: \"track complex multi-step work\"},",
				"146:\t\t{Tool: \"task\", Purpose: \"delegate broad evidence gathering to a bounded read-only explorer\"},",
				"147:\t\t{Tool: \"search_files\", Purpose: \"find files by name\"},",
				"148:\t}",
				"149:}",
			}, "\n"),
		},
	}

	closeout := readOnlyClosureDecisionForResults(brief, results)
	if !closeout.Answerable() {
		t.Fatalf("test must exercise host closeout over answerable-but-off-objective source evidence: %#v", closeout)
	}

	if answer, ok := closeout.HostOwnedAnswer("main"); ok {
		t.Fatalf("architecture host closeout must not synthesize an off-objective source inventory:\n%s", answer)
	}
}

func TestArchitectureHostOwnedDiscoveryUsefulnessRejectsSourceInventoryFallback(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output: strings.Join([]string{
				"internal/capabilities/workspace/discovery_query.go: discovery query recipes return source-oriented evidence.",
				"internal/capabilities/workspace/discovery_profile.go: profiles shape discovery evidence.",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f11 internal/capabilities/workspace/discovery_tools.go lines 1-160 (total lines: 598)",
			Output: strings.Join([]string{
				"1:package workspace",
				"2:// Discovery tools produce source and profile evidence for architecture questions.",
				"3:func discoveryToolSurface() {}",
			}, "\n"),
		},
	}

	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)
	if ok {
		t.Fatalf("host closeout must not convert one weak source-body read into source inventory:\n%s", answer)
	}
}

func TestReadOnlyPrefetchSourceBodyMakesEvidenceAnswerableWithoutHostAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Can we shorten the save label in internal/labels/labels.go? Answer the question from the codebase. Do not edit files.",
	}
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNamePrefetch,
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5; source: prefetch)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}

	closeout := readOnlyClosureDecisionForResults(brief, results)
	if !closeout.Answerable() {
		t.Fatalf("prefetch source body should make the model's answer valid, got %#v", closeout)
	}

	if answer, ok := (&Runner{RuntimeBrief: brief}).hostOwnedReadOnlyCloseoutAnswer(results); ok {
		t.Fatalf("prefetch source body must not produce a host-authored answer:\n%s", answer)
	}
}

func TestReadOnlyCloseoutDoesNotHostSynthesizeDelegatedChildAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(workerruntime.TaskFamilyDelegatedReadOnly),
		Contract:          string(workerruntime.ContractGroundedEvidence),
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output: strings.Join([]string{
				"internal/runtime/agent/runner.go: parent task tool dispatch",
				"internal/runtime/agent/runner_child_tasks.go: read-only child runner setup",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner.go lines 1-20",
			Output: strings.Join([]string{
				"1:package agent",
				"2:func runChildTask() {",
				"3:\t// The task tool launches a subagent child runner.",
				"4:}",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 1-20",
			Output: strings.Join([]string{
				"1:package agent",
				"2:func prepareReadOnlyChildPrompt() {",
				"3:\t// Child subagent prompts constrain delegated read-only evidence gathering.",
				"4:}",
			}, "\n"),
		},
	}

	closeout := readOnlyClosureDecisionForResults(brief, results)
	if !closeout.Answerable() {
		t.Fatalf("delegated child source evidence should be answerable for the model, got %#v", closeout)
	}

	if answer, ok := closeout.HostOwnedAnswer("child:explorer"); ok {
		t.Fatalf("delegated child evidence must not produce a host-authored answer:\n%s", answer)
	}
}
