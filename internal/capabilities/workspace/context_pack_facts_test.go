package workspace

import (
	"slices"
	"testing"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/comprehension"
)

func TestContextPackFactsCandidatePathsPreferPacketQualityEvidence(t *testing.T) {
	facts := contextPackToolResultFacts(comprehension.PackResult{
		Task: "chat input text box newline wrapping first line invisible",
		Quality: comprehension.PackQuality{
			LocalizationConfidence: "low",
			SourceOwnerFound:       false,
			EvidencePaths:          []string{"internal/surfaces/tui/app/rendering_test.go"},
			Reasons:                []string{"related_evidence_without_source_owner"},
		},
		CandidateFiles: []comprehension.PackFile{
			{Path: "internal/evidence/transcript/debuglog.go", Reason: "matched task terms"},
			{Path: "internal/surfaces/tui/app/plan.go", Reason: "matched task terms"},
			{Path: "internal/surfaces/tui/app/theme_style.go", Reason: "matched task terms"},
		},
		CandidateSymbols: []comprehension.PackSymbol{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount"},
		},
		Tests: []comprehension.PackTest{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
		},
	})

	if facts == nil {
		t.Fatal("expected context pack facts")
	}

	if len(facts.CandidatePaths) == 0 || facts.CandidatePaths[0] != "internal/surfaces/tui/app/rendering_test.go" {
		t.Fatalf("expected quality evidence path first, got %#v", facts.CandidatePaths)
	}

	if facts.CandidatePaths[0] == "internal/evidence/transcript/debuglog.go" {
		t.Fatalf("candidate files must not saturate recovery ahead of stronger packet evidence: %#v", facts.CandidatePaths)
	}
}

func TestContextPackFactsCandidateSymbolsPreferTopLevelQualityEvidence(t *testing.T) {
	facts := contextPackToolResultFacts(comprehension.PackResult{
		Task: "chat input text box newline wrapping first line invisible",
		Quality: comprehension.PackQuality{
			LocalizationConfidence: "low",
			SourceOwnerFound:       false,
			EvidencePaths:          []string{"internal/surfaces/tui/app/rendering_test.go"},
			Reasons:                []string{"related_evidence_without_source_owner"},
		},
		CandidateFiles: []comprehension.PackFile{
			{Path: "internal/evidence/transcript/debuglog.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/evidence/transcript/debuglog.go#DebugLogger.LogContext"}},
			{Path: "internal/surfaces/tui/app/mocha_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/mocha_style.go#mochaText"}},
		},
		CandidateSymbols: []comprehension.PackSymbol{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount", Reason: "matched task terms"},
			{Path: "internal/runtime/agent/runner_core.go", Kind: "func", QualifiedName: "textOnlyChatOptions", Reason: "matched task terms"},
		},
		Tests: []comprehension.PackTest{
			{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
		},
	})

	if facts == nil {
		t.Fatal("expected context pack facts")
	}

	if len(facts.CandidateSymbols) == 0 || facts.CandidateSymbols[0] != "internal/surfaces/tui/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount" {
		t.Fatalf("expected top-level quality evidence symbol first, got %#v", facts.CandidateSymbols)
	}

	if facts.CandidateSymbols[0] == "internal/evidence/transcript/debuglog.go#DebugLogger.LogContext" {
		t.Fatalf("candidate-file handles must not outrank top-level quality evidence: %#v", facts.CandidateSymbols)
	}
}

func TestContextQueryFactsCarryCandidatePathsAndSymbols(t *testing.T) {
	facts := contextQueryToolResultFacts(comprehension.QueryResult{
		Nodes: []comprehension.NodeSummary{
			{Path: "internal/runtime/agent/runner_core.go", QualifiedName: "Runner.Run"},
			{Path: "internal/runtime/agent/runner_core.go", QualifiedName: "Runner.Run"},
			{Path: "README.md"},
		},
	})

	if facts == nil {
		t.Fatal("expected context query facts")
	}
	if got, want := facts.CandidatePaths, []string{"internal/runtime/agent/runner_core.go", "README.md"}; !slices.Equal(got, want) {
		t.Fatalf("candidate paths = %#v, want %#v", got, want)
	}
	if got, want := facts.CandidateSymbols, []string{"internal/runtime/agent/runner_core.go#Runner.Run"}; !slices.Equal(got, want) {
		t.Fatalf("candidate symbols = %#v, want %#v", got, want)
	}
	if !facts.HasDiscoveryTargets || facts.DiscoveryStrength != toolfacts.DiscoveryStrengthCandidateOnly || facts.SourceOwnerFound {
		t.Fatalf("unexpected context query discovery facts: %#v", facts)
	}
}
