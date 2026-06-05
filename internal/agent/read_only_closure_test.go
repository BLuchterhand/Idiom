package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
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
		Summary: "Read internal/app/app.go lines 1-8",
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
	answer := "For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`."

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

func TestReadOnlyCloseoutHostAcceptanceRequiresContractProducedAnswer(t *testing.T) {
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
	runner := &Runner{RuntimeBrief: brief}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)
	if !ok {
		t.Fatal("expected host-owned closeout to produce a bounded uncertainty answer")
	}
	accepted := runner.classifyTerminalAnswer(answer, "host_read_only_closeout", results)
	if accepted.Outcome != terminalAnswerAccepted || accepted.StopReason != "host_read_only_closeout" {
		t.Fatalf("contract-produced host closeout should be accepted, got %#v", accepted)
	}

	mutated := answer + " The root cause was definitely in the runner."
	rejected := runner.classifyTerminalAnswer(mutated, "host_read_only_closeout", results)
	if rejected.Outcome == terminalAnswerAccepted && rejected.StopReason == "host_read_only_closeout" {
		t.Fatalf("host closeout acceptance must not accept answers not produced by the closeout contract:\n%s", mutated)
	}
}

func TestReadOnlyCloseoutHostOwnedAnswerRejectsSynthesizedDiscoveryInventory(t *testing.T) {
	answer := "Symbol index: Indexed symbols under internal/agent with ctags. This is grounded in `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`."
	if hostOwnedCloseoutAnswerIsPresentable(answer) {
		t.Fatalf("host-owned closeout must reject synthesized discovery inventory:\n%s", answer)
	}
}

func TestReadOnlyCloseoutHostSynthesizesDelegatedChildAnswer(t *testing.T) {
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
				"internal/agent/runner.go: parent task tool dispatch",
				"internal/agent/runner_child_tasks.go: read-only child runner setup",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner.go lines 1-20",
			Output: strings.Join([]string{
				"1:package agent",
				"2:func runChildTask() {",
				"3:\t// The task tool launches a subagent child runner.",
				"4:}",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 1-20",
			Output: strings.Join([]string{
				"1:package agent",
				"2:func prepareReadOnlyChildPrompt() {",
				"3:\t// Child subagent prompts constrain delegated read-only evidence gathering.",
				"4:}",
			}, "\n"),
		},
	}

	closeout := readOnlyClosureDecisionForResults(brief, results)
	answer, ok := closeout.HostOwnedAnswer("child:explorer")
	if !ok {
		t.Fatalf("expected delegated child host closeout from answerable evidence: %#v", closeout)
	}
	for _, want := range []string{"task", "subagent", "internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
			t.Fatalf("expected delegated host answer to contain %q, got:\n%s", want, answer)
		}
	}
	if _, ok := closeout.HostOwnedAnswer("main"); ok {
		t.Fatal("delegated child host closeout must stay child-only")
	}
}
