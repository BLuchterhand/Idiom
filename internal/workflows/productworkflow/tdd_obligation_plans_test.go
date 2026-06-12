package productworkflow

import (
	"strings"
	"testing"
)

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

func TestTDDObligationPlanReusesExistingLedgerCommand(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-NPM"}},
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
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-NPM"},
					CoverageTier:   "behavior_standard",
				},
			},
		},
	}
	ledger := TDDLedger{
		CoverageTier: "behavior_standard",
		Obligations: []TDDObligation{
			{ID: "TDD-NPM", Type: "acceptance", Owner: "greeting.test.js", Command: "npm test"},
		},
	}
	plan := PlanTDDObligations(prd, adrs, ledger, nil)

	if len(plan.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", plan.Findings)
	}

	if len(plan.Obligations) != 1 {
		t.Fatalf("expected one obligation, got %#v", plan.Obligations)
	}

	got := plan.Obligations[0]
	if got.Command != "npm test" {
		t.Fatalf("expected ledger command to win, got %#v", got)
	}

	if got.SuggestedType != "acceptance" {
		t.Fatalf("expected ledger type to win, got %#v", got)
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

func TestTDDObligationPlanBlocksContractTierWhenMissingTDDRefs(t *testing.T) {
	for _, coverageTier := range []string{"contract_high", "formal_optional"} {
		t.Run(coverageTier, func(t *testing.T) {
			prd := PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{" "}},
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
							TDDObligations: []string{"\t"},
							CoverageTier:   coverageTier,
						},
					},
				},
			}
			plan := PlanTDDObligations(prd, adrs, TDDLedger{CoverageTier: "unit"}, map[string]string{"ADR-1": "adrs/ADR-1.md"})

			if len(plan.Findings) == 0 {
				t.Fatalf("expected finding for missing TDD obligation references")
			}

			if plan.Findings[0].Kind != "missing_tdd_obligation_reference" {
				t.Fatalf("expected missing_tdd_obligation_reference finding, got %q", plan.Findings[0].Kind)
			}

			if !strings.Contains(plan.Findings[0].Message, "REQ-1") || !strings.Contains(plan.Findings[0].Message, "ADR-1") {
				t.Fatalf("expected finding message to name PRD and ADR refs, got %q", plan.Findings[0].Message)
			}

			if len(plan.Obligations) == 0 {
				t.Fatalf("expected obligation with blocker reason")
			}

			obligation := plan.Obligations[0]
			if obligation.BlockerReason == "" {
				t.Fatalf("expected blocker reason for contract-tier obligation")
			}

			if obligation.SuggestedType != "contract" {
				t.Fatalf("expected contract-tier suggested type, got %q", obligation.SuggestedType)
			}

			if !TDDObligationPlanHasBlockers(plan) {
				t.Fatalf("expected plan to be blocked")
			}
		})
	}
}

func TestTDDObligationPlanDoesNotBlockBehaviorStandardWhenMissingTDDRefs(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}},
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
					TDDObligations: []string{}, // No TDD obligations in ADR
					CoverageTier:   "behavior_standard",
				},
			},
		},
	}
	plan := PlanTDDObligations(prd, adrs, TDDLedger{CoverageTier: "unit"}, map[string]string{"ADR-1": "adrs/ADR-1.md"})

	if len(plan.Findings) > 0 {
		t.Fatalf("expected no findings for behavior_standard tier")
	}

	if len(plan.Obligations) == 0 {
		t.Fatalf("expected obligation for behavior_standard tier")
	}

	obligation := plan.Obligations[0]
	if obligation.BlockerReason != "" {
		t.Fatalf("expected no blocker reason for behavior_standard tier")
	}

	if TDDObligationPlanHasBlockers(plan) {
		t.Fatalf("expected plan to not be blocked for behavior_standard tier")
	}
}

func TestTDDObligationPlanDoesNotBlockContractTierWhenTDDRefsExist(t *testing.T) {
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{
			{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
		},
	}
	adrs := []ParsedADR{
		{
			RelPath: "adrs/ADR-1.md",
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					PRDRefs:      []string{"REQ-1"},
					EvidenceRefs: []string{"EV-2"},
					CoverageTier: "contract_high",
				},
			},
		},
	}
	ledger := TDDLedger{
		CoverageTier: "contract_high",
		Obligations: []TDDObligation{
			{ID: "TDD-1", Type: "property_based", Owner: "contract_test.go", Command: "go test ./internal/workflows/productworkflow -run TestContract"},
		},
	}
	plan := PlanTDDObligations(prd, adrs, ledger, map[string]string{"ADR-1": "adrs/ADR-1.md"})

	if len(plan.Findings) != 0 {
		t.Fatalf("expected no findings when contract-tier TDD refs exist, got %#v", plan.Findings)
	}

	if len(plan.Obligations) != 1 {
		t.Fatalf("expected one obligation, got %#v", plan.Obligations)
	}

	obligation := plan.Obligations[0]
	if obligation.BlockerReason != "" {
		t.Fatalf("expected no blocker when contract-tier TDD refs exist, got %#v", obligation)
	}

	if obligation.Command != "go test ./internal/workflows/productworkflow -run TestContract" {
		t.Fatalf("expected existing ledger command to win, got %#v", obligation)
	}

	if obligation.SuggestedType != "property_based" {
		t.Fatalf("expected existing ledger type to win, got %#v", obligation)
	}
}
