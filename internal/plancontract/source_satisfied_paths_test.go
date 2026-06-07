package plancontract

import "testing"

func TestValidateSourceSatisfiedPathCoverageRequiresDeclaredScopeAndEvidence(t *testing.T) {
	got := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        []string{"a.go", "b.go"},
		SourceSatisfiedPaths: []string{"a.go"},
		RequireEvidence:      true,
	})
	if len(got) != 1 || got[0].Code != "missing_current_state_evidence" || got[0].Path != "a.go" {
		t.Fatalf("expected missing current-state evidence violation, got %#v", got)
	}

	got = ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		SourceSatisfiedPaths: []string{"a.go"},
		Evidence:             []SourceSatisfiedEvidenceRef{{Path: "a.go", Kind: "harness_observed_source"}},
		RequireEvidence:      true,
	})
	if len(got) != 1 || got[0].Code != "missing_required_scope" {
		t.Fatalf("expected missing required scope violation, got %#v", got)
	}
}

func TestValidateSourceSatisfiedPathCoverageAcceptsCurrentStateSourceEvidence(t *testing.T) {
	got := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        []string{"a.go", "b.go"},
		SourceSatisfiedPaths: []string{"a.go"},
		Evidence: []SourceSatisfiedEvidenceRef{
			{Path: "a.go", Kind: "harness_observed_source"},
			{Path: "b.go", Kind: "context_packet"},
		},
		RequireEvidence: true,
	})
	if len(got) != 0 {
		t.Fatalf("expected current source evidence to satisfy source-satisfied path, got %#v", got)
	}
}

func TestValidateSourceSatisfiedPathCoverageRejectsPendingPartialApproval(t *testing.T) {
	got := ValidateSourceSatisfiedPathCoverage(SourceSatisfiedPathCoverageInput{
		RequiredPaths:        []string{"a.go"},
		SourceSatisfiedPaths: []string{"a.go"},
		Evidence:             []SourceSatisfiedEvidenceRef{{Path: "a.go", Kind: "harness_observed_current_state"}},
		ApprovedPaths:        []string{"b.go"},
		PendingPaths:         []string{"a.go"},
		RequireEvidence:      true,
	})
	if len(got) != 2 {
		t.Fatalf("expected pending and outside-approved violations, got %#v", got)
	}
	if got[0].Code != "pending_approval" || got[1].Code != "outside_approved_scope" {
		t.Fatalf("unexpected violation ordering/content: %#v", got)
	}
}
