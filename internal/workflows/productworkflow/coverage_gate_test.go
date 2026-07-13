package productworkflow

import (
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("coverage gate", func() {
	ginkgo.It("CoverageGatePassesBehaviorStandardWithCompleteCoverage", func() {
		mutation := 90.0
		contract := BehavioralCoverageContract{
			EARS: []EARSRequirementCoverage{{
				ID:         "REQ-1",
				Statement:  "When behavior coverage exists, the system shall require typed behavioral coverage.",
				MappedRefs: []string{"BDD-1"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-1",
				RequirementID:    "REQ-1",
				Given:            []string{"behavior coverage exists"},
				When:             []string{"gate evaluates"},
				Then:             []string{"typed behavioral contract is present"},
				ConcreteDataRefs: []string{"fixtures/behavior.json"},
			}},
			DesignContracts: []DesignContractCoverage{{
				ID:             "DBC-1",
				Boundary:       "coverage gate",
				Preconditions:  []string{"behavior tier requested"},
				Postconditions: []string{"typed coverage exists"},
				Invariants:     []string{"counters are projection only"},
				TestRefs:       []string{"TestCoverageGatePassesBehaviorStandardWithCompleteCoverage"},
			}},
			Mutation: BehavioralMutationCoverage{Requirement: "required", MinScore: 80, Score: &mutation},
		}
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:        "ADR-1",
						Status:       "accepted",
						EvidenceRefs: []string{"EV-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier:       "behavior_standard",
				Coverage:           ProjectBehavioralCoverageContract(contract),
				BehavioralCoverage: &contract,
				Obligations:        []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Findings))
		gomega.Expect(CoverageGateHasBlockers(eval)).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %#v", eval.Blockers))

	})
	ginkgo.It("CoverageGateBlocksInvalidBehavioralCoverageContract", func() {
		contract := BehavioralCoverageContract{
			EARS: []EARSRequirementCoverage{{ID: "REQ-1", Statement: "should be accepted", MappedRefs: []string{"BDD-1"}}},
		}
		eval := EvaluateCoverageGate(
			PRDDocument{FrontMatter: PRDFrontMatter{Status: "accepted"}, Requirements: []PRDRequirement{{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}}}},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted", EvidenceRefs: []string{"EV-1"}}}}},
			TDDLedger{
				CoverageTier:       "behavior_standard",
				Coverage:           TDDCoverage{EARSTotal: 1, EARSMapped: 1, BDDTotal: 1, BDDWithConcreteData: 1, GatesPassed: []string{"contract"}, MutationMinScore: 80},
				BehavioralCoverage: &contract,
				Obligations:        []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}}},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("expected invalid behavioral coverage to block, got %q", eval.Status))
		gomega.Expect(!coverageGateTestHasFinding(eval, "coverage_behavioral_contract_invalid")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid contract finding, got %#v", eval.Findings))

	})
	ginkgo.It("CoverageGateBlocksBehavioralCoverageProjectionMismatch", func() {
		contract := coverageGateTestBehavioralCoverageContract("behavior_standard", TDDCoverage{})
		coverage := ProjectBehavioralCoverageContract(contract)
		coverage.BDDWithConcreteData = 0
		eval := EvaluateCoverageGate(
			PRDDocument{FrontMatter: PRDFrontMatter{Status: "accepted"}, Requirements: []PRDRequirement{{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}}}},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted", EvidenceRefs: []string{"EV-1"}}}}},
			TDDLedger{
				CoverageTier:       "behavior_standard",
				Coverage:           coverage,
				BehavioralCoverage: &contract,
				Obligations:        []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}}},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("expected projection mismatch to block, got %q", eval.Status))
		gomega.Expect(!coverageGateTestHasFinding(eval, "coverage_behavioral_projection_mismatch")).To(gomega.BeFalse(), fmt.Sprintf("expected projection mismatch finding, got %#v", eval.Findings))

	})
	ginkgo.It("CoverageGateBlocksBehaviorTierWithoutBehavioralCoverageContract", func() {
		mutation := 90.0
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:        "ADR-1",
						Status:       "accepted",
						EvidenceRefs: []string{"EV-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "behavior_standard",
				Coverage: TDDCoverage{
					EARSTotal:           1,
					EARSMapped:          1,
					BDDTotal:            1,
					BDDWithConcreteData: 1,
					GatesPassed:         []string{"contract"},
					MutationScore:       &mutation,
					MutationMinScore:    80,
				},
				Obligations: []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("expected counter-only behavioral coverage to block, got %q", eval.Status))
		gomega.Expect(!coverageGateTestHasBlocker(eval, "coverage_behavioral_contract_missing")).To(gomega.BeFalse(), fmt.Sprintf("expected behavioral contract blocker, got %#v", eval.Blockers))

	})
	ginkgo.It("CoverageGateBlocksIncompleteDeclaredProductionReadinessCategories", func() {
		baseCoverage := TDDCoverage{
			EARSTotal:            1,
			EARSMapped:           1,
			BDDTotal:             1,
			BDDWithConcreteData:  1,
			GatesPassed:          []string{"contract"},
			InvariantsTotal:      1,
			InvariantsWithPBT:    1,
			StateMachinesTotal:   1,
			StateMachinesCovered: 1,
		}
		contract := BehavioralCoverageContract{
			EARS: []EARSRequirementCoverage{{
				ID:         "REQ-1",
				Statement:  "When declared coverage exists, the system shall require complete production-readiness evidence.",
				MappedRefs: []string{"BDD-1"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-1",
				Given:            []string{"coverage is declared"},
				When:             []string{"gate evaluates"},
				Then:             []string{"declared categories are complete"},
				ConcreteDataRefs: []string{"fixtures/coverage.json"},
			}},
			DesignContracts: []DesignContractCoverage{{
				ID:             "DBC-1",
				Boundary:       "coverage gate",
				Preconditions:  []string{"contract tier requested"},
				Postconditions: []string{"incomplete declared coverage blocks"},
				Invariants:     []string{"declared totals match tested totals"},
				TestRefs:       []string{"TestCoverageGateBlocksIncompleteDeclaredProductionReadinessCategories"},
			}},
		}
		prd := PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		}
		adrIndex := ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}}
		adrs := []ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}}
		traceability := TraceabilityMatrix{Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}}}

		tests := []struct {
			name string
			set  func(*TDDCoverage)
			kind string
		}{
			{"fuzz", func(c *TDDCoverage) { c.FuzzTargetsTotal = 1 }, "coverage_fuzz_incomplete"},
			{"rag eval", func(c *TDDCoverage) { c.RAGEvalsTotal = 1 }, "coverage_rag_eval_incomplete"},
			{"security", func(c *TDDCoverage) { c.SecurityCasesTotal = 1 }, "coverage_llm_security_incomplete"},
			{"privacy", func(c *TDDCoverage) { c.PrivacyControlsTotal = 1 }, "coverage_privacy_incomplete"},
			{"provider contract", func(c *TDDCoverage) { c.ProviderContractsTotal = 1 }, "coverage_provider_contract_incomplete"},
			{"observability", func(c *TDDCoverage) { c.ObservabilityTotal = 1 }, "coverage_observability_incomplete"},
			{"performance", func(c *TDDCoverage) { c.PerformanceBoundsTotal = 1 }, "coverage_performance_incomplete"},
			{"scripted e2e", func(c *TDDCoverage) { c.E2EJourneysTotal = 1 }, "coverage_scripted_e2e_incomplete"},
		}

		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				coverage := baseCoverage
				tt.set(&coverage)
				eval := EvaluateCoverageGate(
					prd,
					adrIndex,
					adrs,
					TDDLedger{
						CoverageTier:       "contract_high",
						Coverage:           coverage,
						BehavioralCoverage: &contract,
						Obligations:        []TDDObligation{{ID: "TDD-1"}},
					},
					traceability,
					map[string]bool{"TDD-1": true},
				)
				gomega.Expect(eval.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("expected blocked status for %s, got %q", tt.name, eval.Status))
				gomega.Expect(!coverageGateTestHasBlocker(eval, tt.kind)).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %q, got %#v", tt.kind, eval.Blockers))

			})
		}
	})
	ginkgo.It("BuildCoverageGateEvaluatedEventReportsCoveragePercentages", func() {
		mutation := 85.0
		event := BuildCoverageGateEvaluatedEvent(
			CoverageGateEvaluation{
				EffortID: "effort-1",
				TaskID:   "task-1",
				Status:   "pass",
			},
			TDDLedger{
				Coverage: TDDCoverage{
					EARSTotal:                4,
					EARSMapped:               2,
					BDDTotal:                 2,
					BDDWithConcreteData:      1,
					FMEAModesTotal:           4,
					FMEAModesMitigated:       3,
					HAZOPDeviationsTotal:     4,
					HAZOPDeviationsTested:    2,
					InvariantsTotal:          2,
					InvariantsWithPBT:        1,
					StateMachinesTotal:       4,
					StateMachinesCovered:     3,
					DecisionTablesTotal:      4,
					DecisionTablesEnumerated: 1,
					BoundaryValuesTotal:      5,
					BoundaryValuesTested:     4,
					FaultTreeLeavesTotal:     5,
					FaultTreeLeavesTested:    3,
					PostMortemIncidentsTotal: 5,
					PostMortemCatchingTests:  2,
					FormalModelsTotal:        2,
					FormalModelsVerified:     1,
					FuzzTargetsTotal:         4,
					FuzzTargetsCovered:       3,
					RAGEvalsTotal:            2,
					RAGEvalsMeasured:         1,
					SecurityCasesTotal:       5,
					SecurityCasesTested:      4,
					PrivacyControlsTotal:     4,
					PrivacyControlsTested:    2,
					ProviderContractsTotal:   3,
					ProviderContractsTested:  2,
					ObservabilityTotal:       2,
					ObservabilityTested:      1,
					PerformanceBoundsTotal:   4,
					PerformanceBoundsTested:  3,
					E2EJourneysTotal:         5,
					E2EJourneysScripted:      4,
					MutationScore:            &mutation,
				},
			},
		)
		gomega.Expect(event.Event).To(gomega.Equal("coverage_gate_evaluated"), fmt.Sprintf("unexpected event identity: %#v", event))
		gomega.Expect(event.EffortID).To(gomega.Equal("effort-1"), fmt.Sprintf("unexpected event identity: %#v", event))
		gomega.Expect(event.TaskID).To(gomega.Equal("task-1"), fmt.Sprintf("unexpected event identity: %#v", event))
		gomega.Expect(event.GateResult).To(gomega.Equal("pass"), fmt.Sprintf("unexpected event identity: %#v", event))

		assertCoveragePct("ears", event.Coverage.EARSCoveragePct, 50)
		assertCoveragePct("bdd", event.Coverage.BDDCoveragePct, 50)
		assertCoveragePct("fmea", event.Coverage.FMEAMitigationPct, 75)
		assertCoveragePct("hazop", event.Coverage.HAZOPTestedPct, 50)
		assertCoveragePct("pbt", event.Coverage.InvariantPBTPct, 50)
		assertCoveragePct("state_machine", event.Coverage.StateMachinePct, 75)
		assertCoveragePct("decision_table", event.Coverage.DecisionTablePct, 25)
		assertCoveragePct("boundary_value", event.Coverage.BoundaryValuePct, 80)
		assertCoveragePct("fault_tree", event.Coverage.FaultTreeCoveragePct, 60)
		assertCoveragePct("post_mortem", event.Coverage.PostMortemCoveragePct, 40)
		assertCoveragePct("formal_verification", event.Coverage.FormalVerificationPct, 50)
		assertCoveragePct("fuzz", event.Coverage.FuzzCoveragePct, 75)
		assertCoveragePct("rag_eval", event.Coverage.RAGEvalCoveragePct, 50)
		assertCoveragePct("security", event.Coverage.SecurityCoveragePct, 80)
		assertCoveragePct("privacy", event.Coverage.PrivacyCoveragePct, 50)
		assertCoveragePct("provider_contract", event.Coverage.ProviderContractPct, 66.66666666666666)
		assertCoveragePct("observability", event.Coverage.ObservabilityPct, 50)
		assertCoveragePct("performance", event.Coverage.PerformancePct, 75)
		assertCoveragePct("scripted_e2e", event.Coverage.ScriptedE2EPct, 80)
		assertCoveragePct("mutation", event.Coverage.MutationScore, 85)
		gomega.Expect(len(event.BlockedReasons) != 0).To(gomega.BeFalse(), fmt.Sprintf("passing event should have no blocked reasons, got %#v", event.BlockedReasons))
		gomega.Expect(event.Waivers == nil).To(gomega.BeFalse(), fmt.Sprint("waivers should be an empty list, not nil"))

	})
	ginkgo.It("BuildCoverageGateEvaluatedEventReportsBlockedReasons", func() {
		event := BuildCoverageGateEvaluatedEvent(
			CoverageGateEvaluation{
				Status: "blocked",
				Blockers: []CoverageGateBlocker{
					{Criterion: "mutation", Kind: "coverage_mutation_incomplete"},
					{Criterion: "authority_preflight", Kind: "parse_error"},
					{Criterion: "mutation", Kind: "coverage_mutation_incomplete"},
				},
			},
			TDDLedger{EffortID: "ledger-effort", TaskID: "ledger-task"},
		)
		gomega.Expect(event.EffortID).To(gomega.Equal("ledger-effort"), fmt.Sprintf("expected ledger identity fallback, got %#v", event))
		gomega.Expect(event.TaskID).To(gomega.Equal("ledger-task"), fmt.Sprintf("expected ledger identity fallback, got %#v", event))

		want := []string{"authority_preflight:parse_error", "mutation:coverage_mutation_incomplete"}
		gomega.Expect(len(event.BlockedReasons) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected blocked reasons %#v, got %#v", want, event.BlockedReasons))

		for i := range want {
			gomega.Expect(event.BlockedReasons[i] != want[i]).To(gomega.BeFalse(), fmt.Sprintf("expected blocked reasons %#v, got %#v", want, event.BlockedReasons))

		}
	})
	ginkgo.It("CoverageGateBlocksContractHighWhenMutationBelowMin", func() {
		mutation := 50.0
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:        "ADR-1",
						Status:       "accepted",
						EvidenceRefs: []string{"EV-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "contract_high",
				Coverage: TDDCoverage{
					EARSTotal:                1,
					EARSMapped:               1,
					BDDTotal:                 1,
					BDDWithConcreteData:      1,
					FMEAModesTotal:           1,
					FMEAModesMitigated:       1,
					HAZOPDeviationsTotal:     1,
					HAZOPDeviationsTested:    1,
					InvariantsTotal:          1,
					InvariantsWithPBT:        1,
					DecisionTablesTotal:      1,
					DecisionTablesEnumerated: 1,
					GatesPassed:              []string{"contract"},
					MutationScore:            &mutation,
					MutationMinScore:         80,
				},
				Obligations: []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		foundMutation := false

		for _, blocker := range eval.Blockers {
			if blocker.Criterion == "mutation" {
				foundMutation = true
				break
			}
		}
		gomega.Expect(!foundMutation).To(gomega.BeFalse(), fmt.Sprintf("expected mutation blocker, got %#v", eval.Blockers))

	})
	ginkgo.It("CoverageGateBlocksWhenEvidenceAcceptanceCoverageMissing", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:        "ADR-1",
						Status:       "accepted",
						EvidenceRefs: []string{"EV-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{Matrix: []TraceabilityRow{}},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		found := false

		for _, blocker := range eval.Blockers {
			if blocker.Criterion == "evidence_acceptance" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected evidence_acceptance blocker, got %#v", eval.Blockers))

	})
	ginkgo.It("CoverageGateBlocksWhenPRDOrADRReferencesUnknownTDDObligation", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				RelPath: "adrs/ADR-1.md",
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-MISSING"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
			},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

	})
	ginkgo.It("CoverageGateDoesNotBlockWhenNonMustPRDReferencesUnknownTDDObligation", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
			},
			map[string]bool{"TDD-1": true},
		)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q", eval.Status))

		for _, blocker := range eval.Blockers {
			gomega.Expect(blocker.Kind == "coverage_prd_tdd_obligation_unknown").To(gomega.BeFalse(), fmt.Sprintf("non-must PRD unknown TDD ref must not produce PRD TDD blocker: %#v", eval.Blockers))

		}
	})
	ginkgo.It("CoverageGateBlocksWhenMustPRDReferencesUnknownTDDObligation", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
			},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		found := false

		for _, blocker := range eval.Blockers {
			if blocker.Kind == "coverage_prd_tdd_obligation_unknown" &&
				strings.Contains(blocker.Message, "REQ-1") &&
				strings.Contains(blocker.Message, "TDD-MISSING") {

				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected typed PRD TDD blocker naming requirement and obligation, got %#v", eval.Blockers))

		found = false

		for _, finding := range eval.Findings {
			if finding.Kind == "coverage_prd_tdd_obligation_unknown" &&
				strings.Contains(finding.Message, "REQ-1") &&
				strings.Contains(finding.Message, "TDD-MISSING") {

				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected typed PRD TDD finding naming requirement and obligation, got %#v", eval.Findings))

	})
	ginkgo.It("CoverageGateBlocksWhenAcceptedADRReferencesUnknownTDDObligation", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-MISSING"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		foundBlocker := false
		foundFinding := false

		for _, blocker := range eval.Blockers {
			if blocker.Kind == "coverage_adr_tdd_obligation_unknown" {
				foundBlocker = true
				gomega.Expect(blocker.Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message))
				gomega.Expect(blocker.Message).To(gomega.ContainSubstring("TDD-MISSING"), fmt.Sprintf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message))

				break
			}
		}
		gomega.Expect(!foundBlocker).To(gomega.BeFalse(), fmt.Sprintf("expected typed ADR TDD blocker, got %#v", eval.Blockers))

		for _, finding := range eval.Findings {
			if finding.Kind == "coverage_adr_tdd_obligation_unknown" {
				foundFinding = true
				gomega.Expect(finding.Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("finding message should contain ADR and TDD ID, got: %s", finding.Message))
				gomega.Expect(finding.Message).To(gomega.ContainSubstring("TDD-MISSING"), fmt.Sprintf("finding message should contain ADR and TDD ID, got: %s", finding.Message))

				break
			}
		}
		gomega.Expect(!foundFinding).To(gomega.BeFalse(), fmt.Sprintf("expected typed ADR TDD finding, got %#v", eval.Findings))

	})
	// CoverageGateRegressionForADRWithoutRelPath tests that ADRs without RelPath
	// but with ADRID still generate typed coverage blockers/findings with both IDs in the message.
	ginkgo.It("CoverageGateRegressionForADRWithoutRelPath", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-MISSING"},
					},
					// Note: No RelPath field (this would be the regression case)
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations:  []TDDObligation{{ID: "TDD-1"}},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		foundBlocker := false
		foundFinding := false

		for _, blocker := range eval.Blockers {
			if blocker.Kind == "coverage_adr_tdd_obligation_unknown" {
				foundBlocker = true
				gomega.Expect(blocker.Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message))
				gomega.Expect(blocker.Message).To(gomega.ContainSubstring("TDD-MISSING"), fmt.Sprintf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message))

				break
			}
		}
		gomega.Expect(!foundBlocker).To(gomega.BeFalse(), fmt.Sprintf("expected typed ADR TDD blocker, got %#v", eval.Blockers))

		for _, finding := range eval.Findings {
			if finding.Kind == "coverage_adr_tdd_obligation_unknown" {
				foundFinding = true
				gomega.Expect(finding.Message).To(gomega.ContainSubstring("ADR-1"), fmt.Sprintf("finding message should contain ADR and TDD ID, got: %s", finding.Message))
				gomega.Expect(finding.Message).To(gomega.ContainSubstring("TDD-MISSING"), fmt.Sprintf("finding message should contain ADR and TDD ID, got: %s", finding.Message))

				break
			}
		}
		gomega.Expect(!foundFinding).To(gomega.BeFalse(), fmt.Sprintf("expected typed ADR TDD finding, got %#v", eval.Findings))

	})
	ginkgo.It("CoverageGateEvidenceAcceptanceDoesNotRequireEveryTDDObligationTrace", func() {
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1", "TDD-2"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-1", "TDD-2"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations: []TDDObligation{
					{ID: "TDD-1"},
					{ID: "TDD-2"},
					{ID: "TDD-IMPLEMENTATION-LOCAL"},
				},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
		)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Findings))

	})
	ginkgo.It("CoverageGateBlocksWhenTDDEvidenceMissing", func() {
		// Create a scenario where TDD obligations exist but no verification records satisfy them.
		// knownTDDIDs is empty, so evaluateTDDEvidenceCoverage should return false.
		eval := EvaluateCoverageGate(
			PRDDocument{
				FrontMatter: PRDFrontMatter{Status: "accepted"},
				Requirements: []PRDRequirement{
					{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
				},
			},
			ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
			[]ParsedADR{{
				Doc: ADRDocument{
					FrontMatter: ADRFrontMatter{
						ADRID:          "ADR-1",
						Status:         "accepted",
						EvidenceRefs:   []string{"EV-1"},
						TDDObligations: []string{"TDD-1"},
					},
				},
			}},
			TDDLedger{
				CoverageTier: "unit",
				Coverage:     TDDCoverage{MutationMinScore: 80},
				Obligations: []TDDObligation{
					{ID: "TDD-1"},
				},
			},
			TraceabilityMatrix{
				Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
			},
			// No knownTDDIDs provided, so TDD-1 is not satisfied.
		)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q: %#v", eval.Status, eval.Findings))

		found := false

		for _, f := range eval.Findings {
			if f.Criterion == "tdd_evidence" && f.Kind == "coverage_tdd_evidence_missing" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected tdd_evidence finding, got: %#v", eval.Findings))

	})
	ginkgo.It("BuildKnownTDDObligationIDsScopesHistoricalRecords", func() {
		ids := BuildKnownTDDObligationIDs(
			TDDLedger{
				EffortID:    "effort",
				Obligations: []TDDObligation{{ID: "TDD-CURRENT"}},
			},
			[]ImplementationLedgerRecord{
				{RecordType: LedgerRecordVerificationCompleted, EffortID: "effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-HIST"}},
				{RecordType: LedgerRecordVerificationCompleted, EffortID: "effort", TaskID: "other-task", TDDObligationsSatisfied: []string{"TDD-OTHER"}},
				{RecordType: LedgerRecordImplementationCompleted, EffortID: "effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-WRONG-RECORD"}},
				{RecordType: LedgerRecordVerificationCompleted, EffortID: "other-effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-WRONG-EFFORT"}},
			},
			"artifact-task",
		)
		for _, want := range []string{"TDD-CURRENT", "TDD-HIST"} {
			gomega.Expect(!ids[want]).To(gomega.BeFalse(), fmt.Sprintf("expected known ID %q in %#v", want, ids))

		}

		for _, unexpected := range []string{"TDD-OTHER", "TDD-WRONG-RECORD", "TDD-WRONG-EFFORT"} {
			gomega.Expect(ids[unexpected]).To(gomega.BeFalse(), fmt.Sprintf("did not expect known ID %q in %#v", unexpected, ids))

		}
	})
	ginkgo.It("CoverageGateAuthorityPreflightCarriesLintFindings", func() {
		eval := CoverageGateEvaluation{Status: "pass"}

		eval = ApplyCoverageGateAuthorityPreflight(eval, []LintSummaryFinding{
			{Code: "adr_supersession_non_reciprocal", Path: "adrs/ADR-001-test.md", Message: "ADR-001 supersedes ADR-002 without reciprocal metadata"},
			{Code: "adr_index_frontmatter_mismatch", Path: "adrs/index.json", Message: "ADR index entry ADR-001 field prd_refs diverges from ADR markdown"},
			{Code: "obsidian_stale_status", Path: ObsidianProductWorkflowPath, Message: "not coverage-gate authority"},
		})
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked status, got %q", eval.Status))

		foundAuthorityCriterion := false

		for _, criterion := range eval.Criteria {
			if criterion.Key == "authority_preflight" && criterion.Status == "blocked" {
				foundAuthorityCriterion = true
				break
			}
		}
		gomega.Expect(!foundAuthorityCriterion).To(gomega.BeFalse(), fmt.Sprintf("expected blocked authority_preflight criterion, got %#v", eval.Criteria))
		gomega.Expect(len(eval.Blockers) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected only ADR authority blockers, got %#v", eval.Blockers))

		kinds := map[string]bool{}
		for _, blocker := range eval.Blockers {
			kinds[blocker.Kind] = true
		}

		for _, want := range []string{"adr_supersession_non_reciprocal", "adr_index_frontmatter_mismatch"} {
			gomega.Expect(!kinds[want]).To(gomega.BeFalse(), fmt.Sprintf("expected authority blocker %q, got %#v", want, eval.Blockers))

		}
	})
	ginkgo.It("CoverageGateAuthorityFindingCoversCanonicalLintMatrix", func() {
		authority := []LintSummaryFinding{
			{Code: "parse_error", Path: "prd.md"},
			{Code: "broken_link", Path: "prd.md"},
			{Code: "parse_error", Path: "adrs/ADR-001-test.md"},
			{Code: "broken_link", Path: "adrs/ADR-001-test.md"},
			{Code: "missing_file", Path: "evidence/traceability_matrix.json"},
			{Code: "parse_error", Path: "evidence/traceability_matrix.json"},
			{Code: "broken_link", Path: "evidence/traceability_matrix.json"},
			{Code: "parse_error", Path: "tdd.json"},
			{Code: "broken_link", Path: "tdd.json"},
			{Code: "adr_supersession_non_reciprocal", Path: "adrs/ADR-001-test.md"},
			{Code: "adr_index_frontmatter_mismatch", Path: "adrs/index.json"},
		}
		for _, finding := range authority {
			gomega.Expect(!coverageGateAuthorityFinding(finding)).To(gomega.BeFalse(), fmt.Sprintf("expected authority finding for %#v", finding))

		}

		nonAuthority := []LintSummaryFinding{
			{Code: "obsidian_stale_status", Path: ObsidianProductWorkflowPath},
			{Code: "missing_file", Path: "prd.md"},
			{Code: "read_error", Path: "implementation_ledger.jsonl"},
		}
		for _, finding := range nonAuthority {
			gomega.Expect(coverageGateAuthorityFinding(finding)).To(gomega.BeFalse(), fmt.Sprintf("did not expect authority finding for %#v", finding))

		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenPBTInvariantTotalIsZero", func() {
		for _, tier := range []string{"contract_high", "formal_optional"} {
			ginkgo.By(tier, func() {
				eval := coverageGateEvalWithZeroInvariants(tier)
				gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

				foundPBT := false

				for _, blocker := range eval.Blockers {
					if blocker.Criterion == "pbt" {
						foundPBT = true
						gomega.Expect(!strings.Contains(blocker.Message, "invariants_total is zero")).To(gomega.BeFalse(), fmt.Sprintf("blocker message should mention invariants_total is zero, got: %s", blocker.Message))

						break
					}
				}
				gomega.Expect(!foundPBT).To(gomega.BeFalse(), fmt.Sprintf("expected pbt blocker, got %#v", eval.Blockers))

			})
		}
	})
	ginkgo.It("CoverageGateDoesNotBlockLowerTiersWhenPBTInvariantTotalIsZero", func() {
		for _, tier := range []string{"documentation", "local_low", "unit"} {
			ginkgo.By(tier, func() {
				eval := coverageGateEvalWithZeroInvariants(tier)
				gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q", eval.Status))

				for _, blocker := range eval.Blockers {
					gomega.Expect(blocker.Criterion == "pbt").To(gomega.BeFalse(), fmt.Sprintf("expected no pbt blocker in %s tier, got %#v", tier, eval.Blockers))

				}
			})
		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenStateMachineInventoryMissing", func() {
		for _, tier := range []string{"contract_high", "formal_optional"} {
			ginkgo.By(tier, func() {
				eval := coverageGateEvalWithStateMachineCoverage(tier, 0, 0)
				gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

				assertCoverageGateBlocker(eval, "state_machine", "coverage_state_machine_incomplete", "state_machines_total is zero")
			})
		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenStateMachineCoveragePartial", func() {
		eval := coverageGateEvalWithStateMachineCoverage("contract_high", 3, 2)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "state_machine", "coverage_state_machine_incomplete", "state_machines_covered == state_machines_total (2/3)")
	})
	ginkgo.It("CoverageGateDoesNotRequireStateMachineCoverageBelowContractTiers", func() {
		for _, tier := range []string{"documentation", "local_low", "unit"} {
			ginkgo.By(tier, func() {
				eval := coverageGateEvalWithStateMachineCoverage(tier, 0, 0)
				gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Blockers))

				for _, blocker := range eval.Blockers {
					gomega.Expect(blocker.Criterion == "state_machine").To(gomega.BeFalse(), fmt.Sprintf("expected no state_machine blocker in %s tier, got %#v", tier, eval.Blockers))

				}
			})
		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenBoundaryValueCoveragePartial", func() {
		eval := coverageGateEvalWithBoundaryCoverage("contract_high", 4, 3, nil)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "boundary_value", "coverage_boundary_value_incomplete", "boundary_values_tested == boundary_values_total (3/4)")
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenBoundaryValueGateConfiguredWithoutCounters", func() {
		eval := coverageGateEvalWithBoundaryCoverage("contract_high", 0, 0, []string{"boundary_value"})
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "boundary_value", "coverage_boundary_value_incomplete", "boundary_values_total is zero")
	})
	ginkgo.It("CoverageGateDoesNotRequireBoundaryValueCoverageWhenNoNumericBoundsDeclared", func() {
		eval := coverageGateEvalWithBoundaryCoverage("contract_high", 0, 0, nil)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Blockers))

		for _, blocker := range eval.Blockers {
			gomega.Expect(blocker.Criterion == "boundary_value").To(gomega.BeFalse(), fmt.Sprintf("expected no boundary_value blocker, got %#v", eval.Blockers))

		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenFaultTreeCoveragePartial", func() {
		eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 4, 3, nil)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "fault_tree", "coverage_fault_tree_incomplete", "fault_tree_leaves_tested == fault_tree_leaves_total (3/4)")
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenFaultTreeGateConfiguredWithoutCounters", func() {
		eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 0, 0, []string{"fault_tree"})
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "fault_tree", "coverage_fault_tree_incomplete", "fault_tree_leaves_total is zero")
	})
	ginkgo.It("CoverageGateDoesNotRequireFaultTreeCoverageWhenNoFaultTreeDeclared", func() {
		eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 0, 0, nil)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Blockers))

		for _, blocker := range eval.Blockers {
			gomega.Expect(blocker.Criterion == "fault_tree").To(gomega.BeFalse(), fmt.Sprintf("expected no fault_tree blocker, got %#v", eval.Blockers))

		}
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenPostMortemCoveragePartial", func() {
		eval := coverageGateEvalWithPostMortemCoverage("contract_high", 4, 3, nil)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "post_mortem", "coverage_post_mortem_incomplete", "post_mortem_catching_tests == post_mortem_incidents_total (3/4)")
	})
	ginkgo.It("CoverageGateBlocksContractTiersWhenPostMortemGateConfiguredWithoutCounters", func() {
		eval := coverageGateEvalWithPostMortemCoverage("contract_high", 0, 0, []string{"post_mortem"})
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "post_mortem", "coverage_post_mortem_incomplete", "post_mortem_incidents_total is zero")
	})
	ginkgo.It("CoverageGateDoesNotRequirePostMortemCoverageWhenNoIncidentsDeclared", func() {
		eval := coverageGateEvalWithPostMortemCoverage("contract_high", 0, 0, nil)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Blockers))

		for _, blocker := range eval.Blockers {
			gomega.Expect(blocker.Criterion == "post_mortem").To(gomega.BeFalse(), fmt.Sprintf("expected no post_mortem blocker, got %#v", eval.Blockers))

		}
	})
	ginkgo.It("CoverageGateBlocksFormalOptionalWhenFormalCoveragePartial", func() {
		eval := coverageGateEvalWithFormalCoverage("formal_optional", 3, 2, nil)
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "formal_verification", "coverage_formal_verification_incomplete", "formal_models_verified == formal_models_total (2/3)")
	})
	ginkgo.It("CoverageGateBlocksFormalOptionalWhenFormalGateConfiguredWithoutCounters", func() {
		eval := coverageGateEvalWithFormalCoverage("formal_optional", 0, 0, []string{"formal_verification"})
		gomega.Expect(eval.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", eval.Status))

		assertCoverageGateBlocker(eval, "formal_verification", "coverage_formal_verification_incomplete", "formal_models_total is zero")
	})
	ginkgo.It("CoverageGateDoesNotRequireFormalCoverageForOrdinaryContractHigh", func() {
		eval := coverageGateEvalWithFormalCoverage("contract_high", 0, 0, nil)
		gomega.Expect(eval.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q: %#v", eval.Status, eval.Blockers))

		for _, blocker := range eval.Blockers {
			gomega.Expect(blocker.Criterion == "formal_verification").To(gomega.BeFalse(), fmt.Sprintf("expected no formal_verification blocker, got %#v", eval.Blockers))

		}
	})
})

func coverageGateTestHasBlocker(eval CoverageGateEvaluation, kind string) bool {
	for _, blocker := range eval.Blockers {
		if blocker.Kind == kind {
			return true
		}
	}
	return false
}

func coverageGateTestHasFinding(eval CoverageGateEvaluation, kind string) bool {
	for _, finding := range eval.Findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}

func assertCoveragePct(name string, got *float64, want float64) {
	ginkgo.GinkgoHelper()
	gomega.Expect(got == nil).To(gomega.BeFalse(), fmt.Sprintf("%s coverage should be reported", name))
	gomega.Expect(*got != want).To(gomega.BeFalse(), fmt.Sprintf("%s coverage = %v, want %v", name, *got, want))

}

func coverageGateEvalWithZeroInvariants(tier string) CoverageGateEvaluation {
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          0,
		InvariantsWithPBT:        0,
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              []string{"contract"},
	})
}

func coverageGateEvalWithStateMachineCoverage(tier string, total int, covered int) CoverageGateEvaluation {
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          1,
		InvariantsWithPBT:        1,
		StateMachinesTotal:       total,
		StateMachinesCovered:     covered,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              []string{"contract"},
	})
}

func coverageGateEvalWithBoundaryCoverage(tier string, total int, tested int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}

	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          1,
		InvariantsWithPBT:        1,
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      total,
		BoundaryValuesTested:     tested,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithFaultTreeCoverage(tier string, total int, tested int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}

	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          1,
		InvariantsWithPBT:        1,
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     total,
		FaultTreeLeavesTested:    tested,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithPostMortemCoverage(tier string, total int, catchingTests int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}

	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          1,
		InvariantsWithPBT:        1,
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: total,
		PostMortemCatchingTests:  catchingTests,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithFormalCoverage(tier string, total int, verified int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}

	return coverageGateEvalWithCoverage(tier, TDDCoverage{
		EARSTotal:                1,
		EARSMapped:               1,
		BDDTotal:                 1,
		BDDWithConcreteData:      1,
		FMEAModesTotal:           1,
		FMEAModesMitigated:       1,
		HAZOPDeviationsTotal:     1,
		HAZOPDeviationsTested:    1,
		InvariantsTotal:          1,
		InvariantsWithPBT:        1,
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        total,
		FormalModelsVerified:     verified,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithCoverage(tier string, coverage TDDCoverage) CoverageGateEvaluation {
	ledger := TDDLedger{
		CoverageTier: tier,
		Coverage:     coverage,
		Obligations:  []TDDObligation{{ID: "TDD-1"}},
	}
	if tierRequiresBehaviorCoverage(tier) {
		contract := coverageGateTestBehavioralCoverageContract(tier, coverage)
		ledger.BehavioralCoverage = &contract
	}

	return EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}},
		ledger,
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
		map[string]bool{"TDD-1": true},
	)
}

func coverageGateTestBehavioralCoverageContract(tier string, coverage TDDCoverage) BehavioralCoverageContract {
	contract := BehavioralCoverageContract{
		CoverageTier: tier,
		EARS: []EARSRequirementCoverage{{
			ID:         "REQ-1",
			Statement:  "When coverage is evaluated, the system shall use typed behavioral coverage.",
			MappedRefs: []string{"BDD-1"},
		}},
		BDDScenarios: []BDDScenarioCoverage{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			Given:            []string{"coverage counters exist"},
			When:             []string{"the gate evaluates"},
			Then:             []string{"typed behavioral coverage is present"},
			ConcreteDataRefs: []string{"fixtures/coverage.json"},
			Obligations:      []string{"TDD-1"},
		}},
		DesignContracts: []DesignContractCoverage{{
			ID:             "DBC-1",
			Boundary:       "coverage gate",
			Preconditions:  []string{"typed coverage exists"},
			Postconditions: []string{"gate can evaluate counters"},
			Invariants:     []string{"behavioral counters are projections"},
			TestRefs:       []string{"TestCoverageGateUsesTypedBehavioralCoverage"},
			Obligations:    []string{"TDD-1"},
		}},
		TestObligations: []TestObligationCoverage{{
			ID:             "TDD-1",
			Type:           "contract",
			Behavior:       "typed behavioral coverage is evaluated",
			Command:        "go test ./internal/workflows/productworkflow",
			RequiredBefore: "implementation",
		}},
	}
	if coverage.InvariantsTotal > 0 {
		contract.Invariants = []InvariantCoverage{{
			ID:                    "INV-1",
			Statement:             "coverage projection remains stable",
			PropertyBasedTestRefs: []string{"TestCoverageProjectionStable"},
			Obligations:           []string{"TDD-1"},
		}}
	}
	if coverage.FMEAModesTotal > 0 {
		contract.FailureModes = []FailureModeCoverage{{
			ID:                 "FM-1",
			Mode:               "coverage projection mismatch",
			Mitigation:         "validate projected coverage",
			RPN:                32,
			MitigationTestRefs: []string{"TestCoverageProjectionStable"},
			Obligations:        []string{"TDD-1"},
		}}
	}
	if coverage.HAZOPDeviationsTotal > 0 {
		contract.HAZOPDeviations = []HAZOPDeviationCoverage{{
			ID:           "HAZOP-1",
			GuideWord:    "Other Than",
			WorkflowStep: "coverage gate",
			Deviation:    "counter projection differs from contract",
			TestRefs:     []string{"TestCoverageProjectionStable"},
			Obligations:  []string{"TDD-1"},
		}}
	}
	if coverage.StateMachinesTotal > 0 {
		contract.StateMachines = []StateMachineCoverage{{
			ID:     "SM-1",
			States: []string{"planned", "verified"},
			Transitions: []StateTransitionCoverage{{
				ID:       "TR-1",
				From:     "planned",
				To:       "verified",
				Event:    "coverage",
				TestRefs: []string{"TestCoverageProjectionStable"},
			}},
		}}
	}
	if coverage.DecisionTablesTotal > 0 {
		contract.DecisionTables = []DecisionTableCoverage{{
			ID:         "DT-1",
			Conditions: []string{"coverage_present"},
			Rules:      []DecisionTableRuleCoverage{{ID: "DT-1-R1", Conditions: map[string]string{"coverage_present": "true"}, Expected: "evaluate"}},
			TestRefs:   []string{"TestCoverageProjectionStable"},
		}}
	}
	if coverage.BoundaryValuesTotal > 0 {
		contract.BoundaryValues = []BoundaryValueCoverage{{
			ID:          "BV-1",
			Parameter:   "coverage_count",
			Values:      []string{"0", "1"},
			TestRefs:    []string{"TestCoverageProjectionStable"},
			Obligations: []string{"TDD-1"},
		}}
	}
	if coverage.FaultTreeLeavesTotal > 0 {
		contract.FaultTreeLeaves = []FaultTreeLeafCoverage{{
			ID:          "FTA-1",
			TopEvent:    "coverage gate accepts invalid projection",
			Cause:       "typed coverage missing",
			TestRefs:    []string{"TestCoverageProjectionStable"},
			Obligations: []string{"TDD-1"},
		}}
	}
	if coverage.PostMortemIncidentsTotal > 0 {
		contract.PostMortems = []PostMortemCoverage{{
			ID:               "PM-1",
			Incident:         "coverage projection drift",
			CatchingTestRefs: []string{"TestCoverageProjectionStable"},
			Obligations:      []string{"TDD-1"},
		}}
	}
	if coverage.FormalModelsTotal > 0 {
		contract.FormalModels = []FormalModelCoverage{{
			ID:               "FORMAL-1",
			Model:            "coverage_projection",
			VerificationRefs: []string{"models/coverage_projection.tla"},
			Obligations:      []string{"TDD-1"},
		}}
	}
	return contract
}

func assertCoverageGateBlocker(eval CoverageGateEvaluation, criterion string, kind string, messageSubstring string) {
	ginkgo.GinkgoHelper()

	var matched *CoverageGateBlocker
	for index := range eval.Blockers {
		blocker := &eval.Blockers[index]
		if blocker.Criterion != criterion || blocker.Kind != kind {
			continue
		}
		matched = blocker
		break
	}
	gomega.Expect(matched).NotTo(gomega.BeNil(), fmt.Sprintf("expected blocker criterion=%q kind=%q, got %#v", criterion, kind, eval.Blockers))
	if messageSubstring != "" {
		gomega.Expect(matched.Message).To(gomega.ContainSubstring(messageSubstring))
	}
}
