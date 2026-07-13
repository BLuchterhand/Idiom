package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("tdd obligation plans", func() {
	ginkgo.It("TDDObligationPlanSynthesizesFromAcceptedPRDAndADR", func() {
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
		gomega.Expect(len(plan.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings, got %#v", plan.Findings))
		gomega.Expect(len(plan.Obligations) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one obligation plan item, got %d", len(plan.Obligations)))

		got := plan.Obligations[0]
		gomega.Expect(got.PRDRequirementRef).To(gomega.Equal("REQ-1"), fmt.Sprintf("unexpected requirement/adr refs: %#v", got))
		gomega.Expect(got.ADRRef).To(gomega.Equal("ADR-1"), fmt.Sprintf("unexpected requirement/adr refs: %#v", got))
		gomega.Expect(got.OwnerPath != "adrs/ADR-1.md").To(gomega.BeFalse(), fmt.Sprintf("expected owner_path from ADR map, got %q", got.OwnerPath))
		gomega.Expect(got.Command).To(gomega.Equal("git diff --check"), fmt.Sprintf("expected documentation-tier audit command, got %#v", got))
		gomega.Expect(got.SuggestedType).To(gomega.Equal("audit"), fmt.Sprintf("expected documentation-tier audit command, got %#v", got))

	})
	ginkgo.It("TDDObligationPlanReusesExistingLedgerCommand", func() {
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
		gomega.Expect(len(plan.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings, got %#v", plan.Findings))
		gomega.Expect(len(plan.Obligations) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one obligation, got %#v", plan.Obligations))

		got := plan.Obligations[0]
		gomega.Expect(got.Command != "npm test").To(gomega.BeFalse(), fmt.Sprintf("expected ledger command to win, got %#v", got))
		gomega.Expect(got.SuggestedType != "acceptance").To(gomega.BeFalse(), fmt.Sprintf("expected ledger type to win, got %#v", got))

	})
	ginkgo.It("TDDObligationPlanAddsBlockerWhenADRMappingUnavailable", func() {
		prd := PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}},
			},
		}
		plan := PlanTDDObligations(prd, nil, TDDLedger{CoverageTier: "unit"}, nil)
		gomega.Expect(len(plan.Obligations) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one obligation, got %d", len(plan.Obligations)))
		gomega.Expect(plan.Obligations[0].BlockerReason == "").To(gomega.BeFalse(), fmt.Sprintf("expected blocker reason, got %#v", plan.Obligations[0]))
		gomega.Expect(!TDDObligationPlanHasBlockers(plan)).To(gomega.BeFalse(), fmt.Sprintf("expected plan blocker, got %#v", plan))
		gomega.Expect(len(plan.Findings) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected typed finding for missing accepted ADR mapping"))

	})
	ginkgo.It("TDDObligationPlanFindingsWhenPRDOrADRNotAccepted", func() {
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
		gomega.Expect(len(plan.Findings) == 0).To(gomega.BeFalse(), fmt.Sprint("expected findings for unaccepted PRD/ADR"))

	})
	ginkgo.It("TDDObligationPlanBlocksContractTierWhenMissingTDDRefs", func() {
		for _, coverageTier := range []string{"contract_high", "formal_optional"} {
			ginkgo.By(coverageTier, func() {
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
				gomega.Expect(len(plan.Findings) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected finding for missing TDD obligation references"))
				gomega.Expect(plan.Findings[0].Kind != "missing_tdd_obligation_reference").To(gomega.BeFalse(), fmt.Sprintf("expected missing_tdd_obligation_reference finding, got %q", plan.Findings[0].Kind))
				gomega.Expect(plan.Findings[0].Message).To(gomega.ContainSubstring("REQ-1"), fmt.Sprintf("expected finding message to name PRD and ADR refs, got %q", plan.Findings[0].Message))
				gomega.Expect(plan.Findings[0].Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("expected finding message to name PRD and ADR refs, got %q", plan.Findings[0].Message))
				gomega.Expect(len(plan.Obligations) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected obligation with blocker reason"))

				obligation := plan.Obligations[0]
				gomega.Expect(obligation.BlockerReason == "").To(gomega.BeFalse(), fmt.Sprintf("expected blocker reason for contract-tier obligation"))
				gomega.Expect(obligation.SuggestedType != "contract").To(gomega.BeFalse(), fmt.Sprintf("expected contract-tier suggested type, got %q", obligation.SuggestedType))
				gomega.Expect(!TDDObligationPlanHasBlockers(plan)).To(gomega.BeFalse(), fmt.Sprintf("expected plan to be blocked"))

			})
		}
	})
	ginkgo.It("TDDObligationPlanDoesNotBlockBehaviorStandardWhenMissingTDDRefs", func() {
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
		gomega.Expect(len(plan.Findings) > 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings for behavior_standard tier"))
		gomega.Expect(len(plan.Obligations) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected obligation for behavior_standard tier"))

		obligation := plan.Obligations[0]
		gomega.Expect(obligation.BlockerReason != "").To(gomega.BeFalse(), fmt.Sprintf("expected no blocker reason for behavior_standard tier"))
		gomega.Expect(TDDObligationPlanHasBlockers(plan)).To(gomega.BeFalse(), fmt.Sprintf("expected plan to not be blocked for behavior_standard tier"))

	})
	ginkgo.It("TDDObligationPlanDoesNotBlockContractTierWhenTDDRefsExist", func() {
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
		gomega.Expect(len(plan.Findings) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no findings when contract-tier TDD refs exist, got %#v", plan.Findings))
		gomega.Expect(len(plan.Obligations) != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one obligation, got %#v", plan.Obligations))

		obligation := plan.Obligations[0]
		gomega.Expect(obligation.BlockerReason != "").To(gomega.BeFalse(), fmt.Sprintf("expected no blocker when contract-tier TDD refs exist, got %#v", obligation))
		gomega.Expect(obligation.Command != "go test ./internal/workflows/productworkflow -run TestContract").To(gomega.BeFalse(), fmt.Sprintf("expected existing ledger command to win, got %#v", obligation))
		gomega.Expect(obligation.SuggestedType != "property_based").To(gomega.BeFalse(), fmt.Sprintf("expected existing ledger type to win, got %#v", obligation))

	})
})
