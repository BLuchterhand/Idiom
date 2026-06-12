package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestReadOnlyEvidencePacketParsesTypedPayloadWithoutProseHeadings(t *testing.T) {
	payload, err := json.Marshal(ReadOnlyEvidencePacket{
		SchemaVersion:   ReadOnlyEvidencePacketSchema,
		Objective:       "Explain runner readiness.",
		Question:        "How does readiness work?",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go"},
		CandidatePaths:  []string{"internal/runtime/agent/read_only_answerability.go"},
		Findings:        []ReadOnlyEvidenceFinding{{Path: "internal/runtime/agent/runner.go", Text: "runner.go computes read-only readiness from grounded source evidence.", Source: "read_file"}},
		Confidence:      "medium",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := workspace.ToolResult{
		Tool:        "task",
		Summary:     "Child task completed",
		Output:      "Plain child output with no inspected-paths or key-findings headings.",
		PayloadType: ReadOnlyEvidencePacketSchema,
		Payload:     payload,
	}

	packet, ok := readOnlyEvidencePacketFromResult(result)
	if !ok {
		t.Fatal("expected typed payload to parse without relying on prose headings")
	}

	if !readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("expected source-backed typed packet to be answerable: %#v", packet)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain runner readiness.",
		PermissionProfile: "read_only_workspace",
	}, []workspace.ToolResult{result})
	if !ledger.Answerable() {
		t.Fatalf("typed task packet should satisfy read-only answerability: answerable=%t terms=%#v ledger=%#v",
			readOnlyEvidencePacketAnswerableForObjective(packet, "Explain runner readiness."),
			readOnlyObjectivePromotionTerms("Explain runner readiness."),
			ledger)
	}

	rendered := renderReadOnlyEvidencePacketForToolOutput(packet, result.Output)
	if !strings.Contains(rendered, "`internal/runtime/agent/runner.go`") || !strings.Contains(rendered, "runner.go computes") {
		t.Fatalf("rendered typed packet should expose paths and findings, got:\n%s", rendered)
	}
}

func TestReadOnlyEvidencePacketFiltersAndNormalizesSourceBodyPaths(t *testing.T) {
	packet := normalizeReadOnlyEvidencePacket(ReadOnlyEvidencePacket{
		SchemaVersion: ReadOnlyEvidencePacketSchema,
		Objective:     "Explain runner readiness.",
		SourceBodyPaths: []string{
			"./internal/runtime/agent/runner.go",
			"internal/runtime/agent/runner.go",
			".idiom/logs/conversations/session.jsonl",
			"logs/conversations/session.jsonl",
			".idiom/comprehension/views/pack.json",
		},
		InspectedPaths: []string{
			"./internal/runtime/agent/read_only_answerability.go",
			"transcripts/session.md",
			"internal/runtime/agent/read_only_answerability.go",
		},
		CandidatePaths: []string{
			"internal/capabilities/workspace/manager.go",
			".codex/discovery/query.json",
		},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: ".idiom/comprehension/views/pack.json",
			Text: "Generated context was inspected but not promoted as source-body evidence.",
		}},
	})

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner.go"}; !slices.Equal(got, want) {
		t.Fatalf("SourceBodyPaths = %#v, want %#v", got, want)
	}

	if got, want := packet.InspectedPaths, []string{"internal/runtime/agent/read_only_answerability.go"}; !slices.Equal(got, want) {
		t.Fatalf("InspectedPaths = %#v, want %#v", got, want)
	}

	if got, want := packet.CandidatePaths, []string{"internal/capabilities/workspace/manager.go"}; !slices.Equal(got, want) {
		t.Fatalf("CandidatePaths = %#v, want %#v", got, want)
	}

	if packet.Findings[0].Path != "" {
		t.Fatalf("artifact finding path should be cleared during normalization: %#v", packet.Findings[0])
	}
}

func TestReadOnlyEvidencePacketArtifactOnlyIsNotAnswerableAtPacketLevel(t *testing.T) {
	packet := normalizeReadOnlyEvidencePacket(ReadOnlyEvidencePacket{
		SchemaVersion:   ReadOnlyEvidencePacketSchema,
		SourceBodyPaths: []string{".idiom/comprehension/views/pack.json", "logs/conversations/session.jsonl"},
		InspectedPaths:  []string{".idiom/discovery/queries/query.json"},
		Findings:        []ReadOnlyEvidenceFinding{{Text: "Only generated context artifacts were inspected."}},
		Confidence:      "medium",
	})

	if readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("artifact-only packet must not be answerable: %#v", packet)
	}

	if len(packet.SourceBodyPaths) != 0 || len(packet.InspectedPaths) != 0 {
		t.Fatalf("artifact paths should be filtered out at packet level: %#v", packet)
	}
}

func TestReadOnlyEvidencePacketDoesNotRenderGeneratedArtifactRedirectAsAnswer(t *testing.T) {
	answer := strings.Join([]string{
		"Generated evidence artifact .idiom/discovery/queries/query.json was not read because generated discovery/context JSON is not source-body evidence.",
		"Use the source paths already named by the current discovery packet instead:",
		"- internal/runtime/agent/runner.go",
		"Next exact source probe example: {\"tool\":\"read_file\",\"path\":\"internal/runtime/agent/runner.go\"}",
	}, "\n")
	childResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output:  "package agent\nfunc (r *Runner) Run() {}\n",
	}}

	packet, ok := buildReadOnlyEvidencePacket("Explain the runner.", answer, childResults)
	if !ok {
		t.Fatal("expected packet to retain source paths even when redirect answer text is stripped")
	}

	rendered := renderReadOnlyEvidencePacketForToolOutput(packet, answer)
	for _, forbidden := range []string{"Grounded answer:", "Generated evidence artifact", "Next exact source probe example"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("redirect guidance should not render as grounded answer %q:\n%s", forbidden, rendered)
		}
	}

	if readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("redirect-only packet must not be answerable: %#v", packet)
	}
}

func TestBuildReadOnlyEvidencePacketFromChildResultsAndAnswerText(t *testing.T) {
	childResults := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read ./internal/runtime/agent/runner.go lines 4310-4370",
			Output:  "package agent\nfunc readOnlyAnalysisReadyFromEvidence() bool { return true }\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read .idiom/comprehension/views/pack.json lines 1-20",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line(s).",
			Output:  "internal/runtime/agent/read_only_answerability.go:55: func readOnlyAnswerabilityForResults\n",
		},
	}
	answer := "- `internal/runtime/agent/runner.go` checks whether read-only analysis has grounded source evidence.\n"

	packet, ok := buildReadOnlyEvidencePacket("Explain runner readiness.", answer, childResults)
	if !ok {
		t.Fatal("expected child result packet to be built")
	}

	if packet.SchemaVersion != ReadOnlyEvidencePacketSchema {
		t.Fatalf("schema version = %q", packet.SchemaVersion)
	}

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner.go"}; !slices.Equal(got, want) {
		t.Fatalf("SourceBodyPaths = %#v, want %#v", got, want)
	}

	if slices.Contains(packet.SourceBodyPaths, ".idiom/comprehension/views/pack.json") {
		t.Fatalf("artifact path leaked into source-body paths: %#v", packet.SourceBodyPaths)
	}

	if !slices.Contains(packet.CandidatePaths, "internal/runtime/agent/read_only_answerability.go") {
		t.Fatalf("expected search candidate path, got %#v", packet.CandidatePaths)
	}

	if !readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("source-backed built packet should be answerable: %#v", packet)
	}
}

func TestBuildReadOnlyEvidencePacketKeepsFoundFindingBullets(t *testing.T) {
	answer := strings.Join([]string{
		"Partial findings from this turn (the budget was reached before a full synthesis):",
		"",
		"Verified context:",
		"- Read internal/runtime/agent/runner_child_tasks.go lines 1-20",
		"- Found task child runner creation in internal/runtime/agent/runner_child_tasks.go",
	}, "\n")
	childResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 1-20",
		Output:  "package agent\n",
	}}

	packet, ok := buildReadOnlyEvidencePacket("Explain how delegated child runners are created.", answer, childResults)
	if !ok {
		t.Fatal("expected packet from child answer")
	}

	if !readOnlyEvidencePacketAnswerableForObjective(packet, "Explain how delegated child runners are created in this harness.") {
		t.Fatalf("expected found finding bullet to support objective answerability: %#v", packet)
	}
}

func TestBuildReadOnlyEvidencePacketDropsLineOrientedSourceFindings(t *testing.T) {
	answer := strings.Join([]string{
		"Grounded findings:",
		"- 1:package agent",
		"- 2:",
		"- 3:import (",
	}, "\n")
	childResults := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output:  "1:package agent\n2:\n3:import (\n",
	}}

	packet, ok := buildReadOnlyEvidencePacket("Explain runner behavior.", answer, childResults)
	if !ok {
		t.Fatal("expected packet to retain source-body path context")
	}

	if readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("raw line-oriented source snippets must not become answerable findings: %#v", packet)
	}

	if len(packet.Findings) != 0 {
		t.Fatalf("line-oriented source snippets leaked into packet findings: %#v", packet.Findings)
	}

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner.go"}; !slices.Equal(got, want) {
		t.Fatalf("SourceBodyPaths = %#v, want %#v", got, want)
	}
}

func TestReadOnlyEvidencePacketAnswerableForObjectiveAcceptsSubagentWorkPacket(t *testing.T) {
	packet := ReadOnlyEvidencePacket{
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The parent runner delegates subagent read-only exploration through the `task` tool"},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` constructs the subagent child runner and returns the typed evidence packet"},
		},
		Confidence: "high",
	}
	objective := "Explain how subagents work."
	body := strings.Join([]string{packet.Findings[0].Text, packet.Findings[1].Text}, "\n")
	objectiveText := answerTextWithoutPathCitations(substantiveAnswerWithoutEchoedObjective(body, objective))
	mechanismText := strings.ToLower(answerProseForObjectiveMatching(objectiveText))

	if !readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
		t.Fatalf("expected subagent packet to support objective answerability: terms=%#v objectiveText=%q mechanism=%t packet=%#v",
			readOnlyObjectivePromotionTerms(objective),
			objectiveText,
			readOnlyExplanationHasMechanismVerb(mechanismText),
			packet)
	}

	answer, ok := synthesizeReadOnlyEvidencePacketAnswer(objective, []ReadOnlyEvidencePacket{packet})
	if !ok {
		t.Fatal("expected synthesis from subagent packet")
	}

	for _, want := range []string{"delegates subagent read-only exploration", "returns the typed evidence packet"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestReadOnlyObjectiveAnswerEvidenceSupportedAcceptsRunnerReadiness(t *testing.T) {
	text := "runner.go computes read-only readiness from grounded source evidence."
	objective := "Explain runner readiness."

	if readOnlyObjectiveAnswerEvidenceSupported(text, objective) {
		t.Fatal("path-like source name alone should not satisfy generic objective support")
	}

	if !readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(text, objective) {
		t.Fatalf("expected named-source runner readiness text to support objective: terms=%#v text=%q",
			readOnlyObjectivePromotionTerms(objective), text)
	}
}

func TestReadOnlyEvidencePacketRejectsPathPrefixWithoutObjectiveSupport(t *testing.T) {
	packet := ReadOnlyEvidencePacket{
		Objective:       "Explain runner readiness.",
		Question:        "Explain runner readiness.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The source body was inspected."},
		},
		Confidence: "medium",
	}

	if readOnlyEvidencePacketAnswerableForObjective(packet, "Explain runner readiness.") {
		t.Fatalf("path prefix and source-body presence alone must not satisfy objective answerability: %#v", packet)
	}
}

func TestReadOnlyEvidencePacketRejectsOffObjectiveSubagentImplementationPacket(t *testing.T) {
	packet := ReadOnlyEvidencePacket{
		Objective:       "Can you tell me how this harness uses subagents?",
		Question:        "Can you tell me how this harness uses subagents?",
		SourceBodyPaths: []string{"cmd/idiom/main.go"},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: "cmd/idiom/main.go",
			Text: "The entrypoint imports rootcmd, calls RunMain, and defines version metadata.",
		}},
		Confidence: "medium",
	}

	if readOnlyEvidencePacketAnswerableForObjective(packet, "Can you tell me how this harness uses subagents?") {
		t.Fatalf("entrypoint-only packet must not satisfy subagent implementation-shape objective: %#v", packet)
	}
}

func TestReadOnlyTaskResultAnswerableForObjectiveUsesTypedPacketAsAuthority(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/runtime/agent/runner.go`",
			"",
			"Key findings:",
			"- The harness uses subagents through delegated child runners.",
		}, "\n"),
	}

	result = readOnlyEvidencePacketToolResult(result, ReadOnlyEvidencePacket{
		SchemaVersion:   ReadOnlyEvidencePacketSchema,
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The runner records model-call metadata."},
		},
		Confidence: "medium",
	})

	if readOnlyTaskResultAnswerableForObjective(result, "Explain how subagents work.") {
		t.Fatal("typed packet rejection must not fall back to legacy rendered text promotion")
	}

	if paths := postChildClosureEvidencePaths("Explain how subagents work.", []workspace.ToolResult{result}, 4); len(paths) != 0 {
		t.Fatalf("non-answerable typed packet must not promote closure evidence paths: %#v", paths)
	}
}
