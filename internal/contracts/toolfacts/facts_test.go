package toolfacts

import "testing"

func TestNormalizeSourceBodiesNormalizesAndMergesTruncatedDuplicates(t *testing.T) {
	got := NormalizeSourceBodies([]SourceBodyEvidence{
		{Path: " ./internal/app.go ", StartLine: -1, EndLine: -2, TotalLines: -3},
		{Path: "`internal/app.go`", StartLine: 10, EndLine: 20, TotalLines: 100},
		{Path: "internal/app.go", StartLine: 10, EndLine: 20, TotalLines: 100, Truncated: true},
		{Path: ""},
	})

	if len(got) != 2 {
		t.Fatalf("normalized facts = %#v, want 2 entries", got)
	}

	if got[0] != (SourceBodyEvidence{Path: "internal/app.go"}) {
		t.Fatalf("first fact = %#v, want normalized zero-range fact", got[0])
	}

	want := SourceBodyEvidence{Path: "internal/app.go", StartLine: 10, EndLine: 20, TotalLines: 100, Truncated: true}
	if got[1] != want {
		t.Fatalf("merged ranged fact = %#v, want %#v", got[1], want)
	}
}

func TestToolResultFactsCloneCopiesSlices(t *testing.T) {
	original := &ToolResultFacts{
		DiagnosticEvidencePaths: []string{"diagnostic.log"},
		SourceBodies:            []SourceBodyEvidence{{Path: "a.go", StartLine: 1, EndLine: 2}},
		CandidatePaths:          []string{"b.go"},
		CandidateSymbols:        []string{"Run"},
		RankedDiscoveryCandidates: []DiscoveryCandidateEvidence{{
			Path:         "b.go",
			LayerName:    "text_anchor_search",
			Tool:         "search_content",
			Summary:      "Found b",
			EvidenceLine: "b.go:1:func Run",
		}},
		HasDiscoveryTargets:    true,
		DiscoveryStrength:      DiscoveryStrengthSourceOwner,
		SourceOwnerFound:       true,
		LocalizationConfidence: "high",
		SearchHitCount:         12,
		SearchHighCardinality:  true,
		GitHistoryPath:         "a.go",
		GitHistoryStatus:       GitHistoryStatusCommitDescriptions,
		ShellCommand:           "go test ./...",
	}

	clone := original.Clone()
	if clone == nil {
		t.Fatal("Clone returned nil")
	}

	clone.DiagnosticEvidencePaths[0] = "changed.log"
	clone.SourceBodies[0].Path = "changed.go"
	clone.CandidatePaths[0] = "changed_candidate.go"
	clone.CandidateSymbols[0] = "Changed"
	clone.RankedDiscoveryCandidates[0].Path = "changed_ranked.go"

	if original.DiagnosticEvidencePaths[0] != "diagnostic.log" ||
		original.SourceBodies[0].Path != "a.go" ||
		original.CandidatePaths[0] != "b.go" ||
		original.CandidateSymbols[0] != "Run" ||
		original.RankedDiscoveryCandidates[0].Path != "b.go" ||
		original.SearchHitCount != 12 ||
		!original.SearchHighCardinality {

		t.Fatalf("Clone shared slice backing arrays with original: original=%#v clone=%#v", original, clone)
	}
}
