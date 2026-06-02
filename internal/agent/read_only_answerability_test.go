package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func readOnlyConversationBriefForTest(objective string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
}

func TestReadOnlyAnswerabilityContextPacketOnlyLocalizes(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"}]}`,
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("context_pack alone must not make direct answer ready: %#v", ledger)
	}
	if !ledger.HasKind(readOnlyEvidenceContextPacket) {
		t.Fatalf("expected context packet item, got %#v", ledger.Items)
	}
	if !slices.Contains(ledger.CandidatePaths, "internal/agent/runner.go") {
		t.Fatalf("expected context candidate path, got %#v", ledger.CandidatePaths)
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness must not be satisfied by context packet localization only")
	}
}

func TestReadOnlyAnswerabilityHighCardinalitySearchRequiresReturnedCandidateRead(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain completion gate behavior.")
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 178 matching line(s) (showing first 50).",
			Output:  "internal/agent/runner.go:420: completion gate\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/app/model_state.go lines 1-80",
			Output:  "package app\nfunc unrelated() {}\n",
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.HighCardinalitySearch {
		t.Fatalf("expected high-cardinality search to be recorded: %#v", ledger)
	}
	if ledger.Answerable() {
		t.Fatalf("unrelated source body must not satisfy high-cardinality search evidence: %#v", ledger)
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness must remain false until a returned candidate is read")
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/agent/runner.go lines 400-460",
		Output:  "package agent\nfunc completionGateBehavior() {}\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("source body from returned candidate should make answer ready: %#v", ledger)
	}
	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/agent/runner.go") {
		t.Fatalf("expected candidate source body to be relevant, got %#v", ledger.RelevantSourceBodyPaths)
	}
	if !conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness should be satisfied by source body from returned candidate")
	}
}

func TestReadOnlyAnswerabilityHighCardinalitySearchRequiresObjectiveRelevantCandidateRead(t *testing.T) {
	brief := readOnlyConversationBriefForTest("What does debug log redacted mode do?")
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 400 matching line(s) (showing first 50).",
			Output:  "internal/config/config.go:390: redacted\ninternal/transcript/debuglog.go:18: DebugLogModeRedacted\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/config/config.go lines 390-400",
			Output:  "package config\nconst RedactedConfigValue = true\n",
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("one weakly related candidate from a high-cardinality search must not satisfy readiness: %#v", ledger)
	}
	closure := readOnlyClosureDecisionForResults(brief, results)
	if blocker := closure.BlockerText(); !slices.Contains(closure.PrioritizedCandidatePaths(), "internal/transcript/debuglog.go") ||
		!strings.Contains(blocker, "debug log redacted mode") ||
		!strings.Contains(blocker, "internal/transcript/debuglog.go") {
		t.Fatalf("expected blocker to prioritize objective-relevant candidate, blocker=%q candidates=%#v", blocker, closure.PrioritizedCandidatePaths())
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/app/slash_commands.go lines 120-140",
		Output:  "m.status = \"Debug log mode set to redacted\"\nm.debugLogger.SetMode(transcript.DebugLogMode(fields[2]))\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("scattered objective words in a UI/control surface must not satisfy noisy-search readiness: %#v", ledger)
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/config/config.go lines 390-400",
		Output:  "DebugLogDir: \"logs/conversations\"\nDebugLogMode: \"redacted\"\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("noisy-search source body must also be on an objective-relevant path before readiness: %#v", ledger)
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/transcript/debuglog.go lines 1-30",
		Output:  "package transcript\nconst DebugLogModeRedacted = \"redacted\"\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("candidate body with objective overlap should satisfy readiness: %#v", ledger)
	}
	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/transcript/debuglog.go") {
		t.Fatalf("expected debuglog source body to be relevant, got %#v", ledger.RelevantSourceBodyPaths)
	}
}

func TestReadOnlyAnswerabilityOutlinesAndGeneratedArtifactsDoNotAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Outlined 12 top-level declaration(s) in internal/agent/runner.go",
			Output:  "file_outline: internal/agent/runner.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read .idiom/comprehension/views/pack.json lines 1-20",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"}]}`,
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("outline plus generated artifact should not be answerable: %#v", ledger)
	}
	if !ledger.HasKind(readOnlyEvidenceOutline) {
		t.Fatalf("expected outline evidence item, got %#v", ledger.Items)
	}
	if !ledger.HasKind(readOnlyEvidenceGeneratedArtifact) {
		t.Fatalf("expected generated artifact evidence item, got %#v", ledger.Items)
	}
}

func TestReadOnlyAnswerabilityAcceptsExplicitDiagnosticEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain why diagnostics fail.")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/agent/runner.go",
		Output:  "internal/agent/runner.go:10: undefined: Foo\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("explicit diagnostic evidence should make read-only explanation answerable: %#v", ledger)
	}
	if !ledger.DiagnosticEvidence {
		t.Fatalf("expected diagnostic evidence flag: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityRejectsRetainedContextFinalWithoutSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"}]}`,
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/agent/runner.go`."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("retained-context final should be rejected, got (%q, %q)", kind, reason)
	}
	if reason == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestReadOnlyAnswerabilityRejectsScaffoldFinalEvenWithRelevantSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/agent/runner.go lines 4128-4285",
		Output:  "package agent\nfunc runnerReadinessState() {}\n",
	}}
	answer := "The strongest implementation evidence is in `internal/agent/runner.go`. Use those findings as the source of truth for `Explain runner answer readiness`.\n\nWhat this establishes:\n- The source body shows `runnerReadinessState` computes answer readiness."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("source-backed scaffold final should still be rejected, got (%q, %q)", kind, reason)
	}
	if reason == "" {
		t.Fatal("expected scaffold rejection reason")
	}
}
