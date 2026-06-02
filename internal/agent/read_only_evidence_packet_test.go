package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestReadOnlyEvidencePacketParsesTypedPayloadWithoutProseHeadings(t *testing.T) {
	payload, err := json.Marshal(ReadOnlyEvidencePacket{
		SchemaVersion:   ReadOnlyEvidencePacketSchema,
		Objective:       "Explain runner readiness.",
		Question:        "How does readiness work?",
		SourceBodyPaths: []string{"internal/agent/runner.go"},
		InspectedPaths:  []string{"internal/agent/runner.go"},
		CandidatePaths:  []string{"internal/agent/read_only_answerability.go"},
		Findings:        []ReadOnlyEvidenceFinding{{Path: "internal/agent/runner.go", Text: "runner.go computes read-only readiness from grounded source evidence.", Source: "read_file"}},
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
		t.Fatalf("typed task packet should satisfy read-only answerability: %#v", ledger)
	}
	rendered := renderReadOnlyEvidencePacketForToolOutput(packet, result.Output)
	if !strings.Contains(rendered, "`internal/agent/runner.go`") || !strings.Contains(rendered, "runner.go computes") {
		t.Fatalf("rendered typed packet should expose paths and findings, got:\n%s", rendered)
	}
}

func TestReadOnlyEvidencePacketFiltersAndNormalizesSourceBodyPaths(t *testing.T) {
	packet := normalizeReadOnlyEvidencePacket(ReadOnlyEvidencePacket{
		SchemaVersion: ReadOnlyEvidencePacketSchema,
		Objective:     "Explain runner readiness.",
		SourceBodyPaths: []string{
			"./internal/agent/runner.go",
			"internal/agent/runner.go",
			".idiom/logs/conversations/session.jsonl",
			"logs/conversations/session.jsonl",
			".idiom/comprehension/views/pack.json",
		},
		InspectedPaths: []string{
			"./internal/agent/read_only_answerability.go",
			"transcripts/session.md",
			"internal/agent/read_only_answerability.go",
		},
		CandidatePaths: []string{
			"internal/workspace/manager.go",
			".codex/discovery/query.json",
		},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: ".idiom/comprehension/views/pack.json",
			Text: "Generated context was inspected but not promoted as source-body evidence.",
		}},
	})

	if got, want := packet.SourceBodyPaths, []string{"internal/agent/runner.go"}; !slices.Equal(got, want) {
		t.Fatalf("SourceBodyPaths = %#v, want %#v", got, want)
	}
	if got, want := packet.InspectedPaths, []string{"internal/agent/read_only_answerability.go"}; !slices.Equal(got, want) {
		t.Fatalf("InspectedPaths = %#v, want %#v", got, want)
	}
	if got, want := packet.CandidatePaths, []string{"internal/workspace/manager.go"}; !slices.Equal(got, want) {
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

func TestBuildReadOnlyEvidencePacketFromChildResultsAndAnswerText(t *testing.T) {
	childResults := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read ./internal/agent/runner.go lines 4310-4370",
			Output:  "package agent\nfunc readOnlyAnalysisReadyFromEvidence() bool { return true }\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read .idiom/comprehension/views/pack.json lines 1-20",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line(s).",
			Output:  "internal/agent/read_only_answerability.go:55: func readOnlyAnswerabilityForResults\n",
		},
	}
	answer := "- `internal/agent/runner.go` checks whether read-only analysis has grounded source evidence.\n"

	packet, ok := buildReadOnlyEvidencePacket("Explain runner readiness.", answer, childResults)
	if !ok {
		t.Fatal("expected child result packet to be built")
	}
	if packet.SchemaVersion != ReadOnlyEvidencePacketSchema {
		t.Fatalf("schema version = %q", packet.SchemaVersion)
	}
	if got, want := packet.SourceBodyPaths, []string{"internal/agent/runner.go"}; !slices.Equal(got, want) {
		t.Fatalf("SourceBodyPaths = %#v, want %#v", got, want)
	}
	if slices.Contains(packet.SourceBodyPaths, ".idiom/comprehension/views/pack.json") {
		t.Fatalf("artifact path leaked into source-body paths: %#v", packet.SourceBodyPaths)
	}
	if !slices.Contains(packet.CandidatePaths, "internal/agent/read_only_answerability.go") {
		t.Fatalf("expected search candidate path, got %#v", packet.CandidatePaths)
	}
	if !readOnlyEvidencePacketAnswerable(packet) {
		t.Fatalf("source-backed built packet should be answerable: %#v", packet)
	}
}
