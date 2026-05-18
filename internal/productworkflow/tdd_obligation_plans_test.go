package productworkflow

import "testing"

func TestTDDObligationPlanSynthesizesFromAcceptedPRDAndADR(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
		},
	}
	adrs := []ParsedADR{
		{
			RelPath: "adrs/ADR-1.md",
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					PRDRefs:        []string{"REQ-1"},
					EvidenceRefs:   []string{"EV-2"},
					TDDObligations: []string{"TDD-2"},
					CoverageTier:   "documentation",
				},
			},
		},
	}
	plan := PlanTDDObligations(prd, adrs, TDDLedger{CoverageTier: "unit"}, map[string]string{"ADR-1": "adrs/ADR-1.md"})
	if len(plan.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", plan.Findings)
	}
	if len(plan.Obligations) != 1 {
		t.Fatalf("expected one obligation plan item, got %d", len(plan.Obligations))
	}
	got := plan.Obligations[0]
	if got.PRDRequirementRef != "REQ-1" || got.ADRRef != "ADR-1" {
		t.Fatalf("unexpected requirement/adr refs: %#v", got)
	}
	if got.OwnerPath != "adrs/ADR-1.md" {
		t.Fatalf("expected owner_path from ADR map, got %q", got.OwnerPath)
	}
	if got.Command != "git diff --check" || got.SuggestedType != "audit" {
		t.Fatalf("expected documentation-tier audit command, got %#v", got)
	}
}

func TestTDDObligationPlanAddsBlockerWhenADRMappingUnavailable(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}},
		},
	}
	plan := PlanTDDObligations(prd, nil, TDDLedger{CoverageTier: "unit"}, nil)
	if len(plan.Obligations) != 1 {
		t.Fatalf("expected one obligation, got %d", len(plan.Obligations))
	}
	if plan.Obligations[0].BlockerReason == "" {
		t.Fatalf("expected blocker reason, got %#v", plan.Obligations[0])
	}
	if !TDDObligationPlanHasBlockers(plan) {
		t.Fatalf("expected plan blocker, got %#v", plan)
	}
	if len(plan.Findings) == 0 {
		t.Fatalf("expected typed finding for missing accepted ADR mapping")
	}
}

func TestTDDObligationPlanFindingsWhenPRDOrADRNotAccepted(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "draft"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "must"},
		},
	}
	adrs := []ParsedADR{
		{
			RelPath: "adrs/ADR-1.md",
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:   "ADR-1",
					Status:  "draft",
					PRDRefs: []string{"REQ-1"},
				},
			},
		},
	}
	plan := PlanTDDObligations(prd, adrs, TDDLedger{}, nil)
	if len(plan.Findings) == 0 {
		t.Fatal("expected findings for unaccepted PRD/ADR")
	}
}
