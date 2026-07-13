package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("implementation handoff", func() {
	ginkgo.It("BuildImplementationHandoffUsesGeneratedDispatchSemantics", func() {
		handoff := BuildImplementationHandoff(implementationHandoffInput(ginkgo.GinkgoT().TempDir()))
		gomega.Expect(handoff.EffortID).To(gomega.Equal("effort-handoff"), fmt.Sprintf("unexpected identity: %#v", handoff))
		gomega.Expect(handoff.TaskID).To(gomega.Equal("impl-handoff"), fmt.Sprintf("unexpected identity: %#v", handoff))
		gomega.Expect(handoff.CoverageGatePassed).To(gomega.BeTrue(), fmt.Sprintf("unexpected coverage fields: passed=%t tier=%q blockers=%#v", handoff.CoverageGatePassed, handoff.CoverageTier, handoff.CoverageBlockers))
		gomega.Expect(handoff.CoverageTier).To(gomega.Equal("unit"), fmt.Sprintf("unexpected coverage fields: passed=%t tier=%q blockers=%#v", handoff.CoverageGatePassed, handoff.CoverageTier, handoff.CoverageBlockers))
		gomega.Expect(!handoffTestStringSliceContains(handoff.FilesToModify, "internal/workflows/productworkflow/implementation_handoff.go")).To(gomega.BeFalse(), fmt.Sprintf("handoff files_to_modify should come from generated brief scope, got %#v", handoff.FilesToModify))
		{

			got := handoff.FileActions["internal/workflows/productworkflow/implementation_handoff.go"]
			gomega.Expect(!strings.Contains(got, "Modify existing file")).To(gomega.BeFalse(), fmt.Sprintf("handoff file_actions should come from generated brief scope, got %#v", handoff.FileActions))
		}
		gomega.Expect(!handoffTestStringSliceContains(handoff.AuthorizedNewPackageRoots, "internal/newpkg")).To(gomega.BeFalse(), fmt.Sprintf("handoff should carry typed authorized new package roots, got %#v", handoff.AuthorizedNewPackageRoots))
		gomega.Expect(handoffTestStringSliceContains(handoff.Tests, "go test ./internal/workflows/productworkflow")).To(gomega.BeTrue(), fmt.Sprintf("handoff tests should include TDD and planned verification, got %#v", handoff.Tests))
		gomega.Expect(handoffTestStringSliceContains(handoff.Tests, "go test ./internal/cmd/featureplanner")).To(gomega.BeTrue(), fmt.Sprintf("handoff tests should include TDD and planned verification, got %#v", handoff.Tests))
		gomega.Expect(!handoffTestStringSliceContains(handoff.RequiredBehavior, "REQ-HANDOFF: Carry product workflow implementation handoff context")).To(gomega.BeFalse(), fmt.Sprintf("handoff required behavior should come from generated brief, got %#v", handoff.RequiredBehavior))
		gomega.Expect(!handoffTestStringSliceContains(handoff.Decisions, "ADR-HANDOFF is accepted and must be preserved unless superseded by a new accepted ADR.")).To(gomega.BeFalse(), fmt.Sprintf("handoff decisions should come from generated brief, got %#v", handoff.Decisions))
		gomega.Expect(!handoffTestStringSliceContains(handoff.Decisions, "ADR-HANDOFF decision: Keep source path authority in productworkflow.")).To(gomega.BeFalse(), fmt.Sprintf("handoff decisions should preserve ADR decision body, got %#v", handoff.Decisions))
		gomega.Expect(handoffTestStringSliceContains(handoff.Acceptance, "REQ-HANDOFF testability: go test ./internal/workflows/productworkflow")).To(gomega.BeTrue(), fmt.Sprintf("handoff acceptance should preserve PRD testability and metric, got %#v", handoff.Acceptance))
		gomega.Expect(handoffTestStringSliceContains(handoff.Acceptance, "REQ-HANDOFF metric: featureplanner context contains product artifacts")).To(gomega.BeTrue(), fmt.Sprintf("handoff acceptance should preserve PRD testability and metric, got %#v", handoff.Acceptance))
		gomega.Expect(handoff.PRDRequirements).To(gomega.HaveLen(1), fmt.Sprintf("handoff should preserve PRD requirement facts, got %#v", handoff.PRDRequirements))
		gomega.Expect(handoff.PRDRequirements[0].Testability).To(gomega.Equal("go test ./internal/workflows/productworkflow"), fmt.Sprintf("handoff should preserve PRD requirement facts, got %#v", handoff.PRDRequirements))
		gomega.Expect(handoff.TDDObligations).To(gomega.HaveLen(1), fmt.Sprintf("handoff should preserve TDD obligation facts, got %#v", handoff.TDDObligations))
		gomega.Expect(handoff.TDDObligations[0].RequiredBefore).To(gomega.Equal("implementation"), fmt.Sprintf("handoff should preserve TDD obligation facts, got %#v", handoff.TDDObligations))
		gomega.Expect(handoff.AcceptedADRIDs).To(gomega.HaveLen(1), fmt.Sprintf("handoff should preserve accepted ADR IDs, got %#v", handoff.AcceptedADRIDs))
		gomega.Expect(handoff.AcceptedADRIDs[0]).To(gomega.Equal("ADR-HANDOFF"), fmt.Sprintf("handoff should preserve accepted ADR IDs, got %#v", handoff.AcceptedADRIDs))
		gomega.Expect(handoff.AcceptedADRPaths).To(gomega.HaveLen(1), fmt.Sprintf("handoff should preserve accepted ADR paths, got %#v", handoff.AcceptedADRPaths))
		gomega.Expect(handoff.AcceptedADRPaths[0]).To(gomega.Equal("adrs/001-handoff.md"), fmt.Sprintf("handoff should preserve accepted ADR paths, got %#v", handoff.AcceptedADRPaths))
		gomega.Expect(!handoffTestStringSliceContains(handoff.Constraints, "Do not edit files outside the Files To Modify allowlist.")).To(gomega.BeFalse(), fmt.Sprintf("handoff constraints should come from generated brief, got %#v", handoff.Constraints))

	})
	ginkgo.It("LoadCoverageGateEvaluationReportsMissingWorkflowAuthorityAsStructuredInputError", func() {
		loaded := LoadCoverageGateEvaluation(ginkgo.GinkgoT().TempDir())
		gomega.Expect(loaded.Evaluation.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("empty effort coverage evaluation should be blocked, got %#v", loaded.Evaluation))

		gomega.Expect(loaded.Findings).To(gomega.ContainElement(gomega.SatisfyAll(
			gomega.HaveField("Kind", "input_error"),
			gomega.HaveField("File", ""),
			gomega.HaveField("Message", gomega.ContainSubstring("read workflow_state.json")),
		)), fmt.Sprintf("expected structured missing workflow authority finding, got %#v", loaded.Findings))
	})
	ginkgo.It("BuildImplementationHandoffCarriesCoverageBlockers", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())

		input.CoverageEvaluation = CoverageGateEvaluation{
			Status:       "blocked",
			CoverageTier: "unit",
			Blockers: []CoverageGateBlocker{{
				Message: "coverage evidence is missing",
			}},
		}

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageGatePassed).To(gomega.BeFalse(), "blocked coverage gate must not be marked passed")
		gomega.Expect(!handoffTestStringSliceContains(handoff.CoverageBlockers, "coverage evidence is missing")).To(gomega.BeFalse(), fmt.Sprintf("expected coverage blocker in handoff, got %#v", handoff.CoverageBlockers))

	})
	ginkgo.It("BuildImplementationHandoffAllowsPendingTDDEvidenceFeaturePlannerDispatch", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())

		input.ImplementationRecords = input.ImplementationRecords[:1]
		input.CoverageEvaluation = CoverageGateEvaluation{
			Status:       "blocked",
			CoverageTier: "unit",
			Blockers: []CoverageGateBlocker{{
				Kind:      "coverage_tdd_evidence_missing",
				Criterion: "tdd_evidence",
				Message:   "expected all 1 TDD obligations to have verification evidence",
				File:      "tdd.json",
			}},
		}

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageGatePassed).To(gomega.BeFalse(), "missing TDD evidence should keep the coverage gate status blocked")
		gomega.Expect(!handoffTestStringSliceContains(handoff.CoverageBlockerKinds, "coverage_tdd_evidence_missing")).To(gomega.BeFalse(), fmt.Sprintf("expected typed coverage blocker kind in handoff, got %#v", handoff.CoverageBlockerKinds))
		gomega.Expect(handoffTestStringSliceContains(handoff.Blockers, "coverage gate is blocked; run `idiom product tdd --effort-root <path> --evaluate-coverage --json` and resolve blockers")).To(gomega.BeFalse(), fmt.Sprintf("TDD-only pending evidence must not become a generic product blocker: %#v", handoff.Blockers))
		gomega.Expect(handoff.TDDObligations).To(gomega.HaveLen(1), fmt.Sprintf("handoff should preserve implementation-pending TDD obligation state: %#v", handoff.TDDObligations))
		gomega.Expect(handoff.TDDObligations[0].Status).To(gomega.Equal("planned"), fmt.Sprintf("handoff should preserve implementation-pending TDD obligation state: %#v", handoff.TDDObligations))

	})
	ginkgo.It("BuildImplementationHandoffKeepsAcceptedCoverageSeparateFromPendingTDDEvidence", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())
		contract := BehavioralCoverageContract{
			ContractID:   "cov-handoff",
			EffortID:     "effort-handoff",
			TaskID:       "impl-handoff",
			CoverageTier: "behavior_standard",
			EventID:      "evt-coverage",
			EARS: []EARSRequirementCoverage{{
				ID:         "REQ-HANDOFF",
				Statement:  "When accepted coverage exists, the system shall carry it into feature planning.",
				MappedRefs: []string{"BDD-HANDOFF"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-HANDOFF",
				RequirementID:    "REQ-HANDOFF",
				Given:            []string{"accepted behavioral coverage exists"},
				When:             []string{"handoff is built"},
				Then:             []string{"accepted coverage is available to feature planning"},
				ConcreteDataRefs: []string{"fixtures/handoff.json"},
				Obligations:      []string{"TDD-HANDOFF"},
			}},
			DesignContracts: []DesignContractCoverage{{
				ID:             "DBC-HANDOFF",
				Boundary:       "productworkflow/featureplanner",
				Preconditions:  []string{"behavioral coverage validates"},
				Postconditions: []string{"coverage contract ref is accepted"},
				Invariants:     []string{"coverage gate status remains separate"},
				TestRefs:       []string{"TestBuildImplementationHandoffKeepsAcceptedCoverageSeparateFromPendingTDDEvidence"},
				Obligations:    []string{"TDD-HANDOFF"},
			}},
			TestObligations: []TestObligationCoverage{{
				ID:             "TDD-HANDOFF",
				Type:           "contract",
				Behavior:       "accepted coverage handoff remains available while implementation evidence is pending",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "implementation",
			}},
		}
		input.TDD.CoverageTier = "behavior_standard"
		input.TDD.Coverage = ProjectBehavioralCoverageContract(contract)
		input.TDD.BehavioralCoverage = &contract
		input.PromotionEvents = []BehavioralCoveragePromotionEvent{{
			Event:   "behavioral_coverage_accepted",
			EventID: "evt-coverage",
			State:   BehavioralCoveragePromotionStateAccepted,
			Digest:  BehavioralCoverageContractDigest(contract),
		}}
		input.CoverageEvaluation = CoverageGateEvaluation{
			Status:       ProductWorkflowStatusBlocked,
			CoverageTier: "behavior_standard",
			Blockers: []CoverageGateBlocker{{
				Kind:      "coverage_tdd_evidence_missing",
				Criterion: "tdd_evidence",
				Message:   "expected all 1 TDD obligations to have verification evidence",
				File:      "tdd.json",
			}},
		}

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageGatePassed).To(gomega.BeFalse(), "overall coverage gate should remain blocked while implementation TDD evidence is pending")
		gomega.Expect(handoff.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("accepted behavioral contract should not inherit pending TDD gate status: %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.Status).To(gomega.Equal(BehavioralCoverageStatusAccepted), fmt.Sprintf("accepted behavioral contract should not inherit pending TDD gate status: %+v", handoff.CoverageContract))
		gomega.Expect(handoff.AcceptedCoverage).NotTo(gomega.BeNil(), fmt.Sprintf("accepted behavioral coverage should be available to feature planning: %+v", handoff.AcceptedCoverage))
		gomega.Expect(handoff.AcceptedCoverage.Status).To(gomega.Equal(BehavioralCoverageStatusAccepted), fmt.Sprintf("accepted behavioral coverage should be available to feature planning: %+v", handoff.AcceptedCoverage))
		gomega.Expect(handoff.AcceptedCoverage.Contract.Status).To(gomega.Equal(BehavioralCoverageStatusAccepted), fmt.Sprintf("accepted behavioral coverage should be available to feature planning: %+v", handoff.AcceptedCoverage))
		gomega.Expect(handoff.AcceptedCoverage.Behavioral.BDDScenarios).To(gomega.HaveLen(1), fmt.Sprintf("accepted behavioral coverage should be available to feature planning: %+v", handoff.AcceptedCoverage))

	})
	ginkgo.It("BuildImplementationHandoffRequiresAcceptedPromotionEvent", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())
		contract := BehavioralCoverageContract{
			ContractID:   "cov-derived",
			EffortID:     "effort-handoff",
			TaskID:       "impl-handoff",
			CoverageTier: "behavior_standard",
			EARS: []EARSRequirementCoverage{{
				ID:         "REQ-HANDOFF",
				Statement:  "When coverage is accepted, the system shall require a durable accepted promotion event.",
				MappedRefs: []string{"BDD-HANDOFF"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-HANDOFF",
				RequirementID:    "REQ-HANDOFF",
				Given:            []string{"accepted behavioral coverage exists"},
				When:             []string{"handoff is built without an accepted promotion event"},
				Then:             []string{"coverage contract ref remains blocked"},
				ConcreteDataRefs: []string{"fixtures/handoff.json"},
			}},
			DesignContracts: []DesignContractCoverage{{
				ID:             "DBC-HANDOFF",
				Boundary:       "productworkflow/featureplanner",
				Preconditions:  []string{"behavioral coverage validates"},
				Postconditions: []string{"accepted event is durable"},
				Invariants:     []string{"accepted coverage is digest-bound to events.jsonl"},
				TestRefs:       []string{"TestBuildImplementationHandoffRequiresAcceptedPromotionEvent"},
			}},
		}
		input.WorkflowState.LastEventID = "evt-workflow-not-accepted"
		contract.EventID = "evt-contract-not-accepted"
		input.TDD.CoverageTier = "behavior_standard"
		input.TDD.Coverage = ProjectBehavioralCoverageContract(contract)
		input.TDD.BehavioralCoverage = &contract
		input.PromotionEvents = []BehavioralCoveragePromotionEvent{{
			Event:        "behavioral_coverage_prepared",
			EventID:      "evt-prepared-not-accepted",
			State:        BehavioralCoveragePromotionStatePrepared,
			CommitStatus: BehavioralCoveragePromotionCommitStatusPrepared,
			Digest:       BehavioralCoverageContractDigest(contract),
		}}
		input.CoverageEvaluation = CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"}

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing promotion event to block accepted coverage, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.Status).To(gomega.Equal(string(ProductWorkflowStatusBlocked)), fmt.Sprintf("expected missing promotion event to block accepted coverage, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.EventID != "").To(gomega.BeFalse(), fmt.Sprintf("blocked coverage contract must not expose non-authoritative event id, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.AcceptedCoverage != nil).To(gomega.BeFalse(), fmt.Sprintf("coverage without accepted promotion event must not be exposed as accepted: %+v", handoff.AcceptedCoverage))

	})
	ginkgo.It("BuildImplementationHandoffBlocksAcceptedCoverageWithOpenQuestionSurface", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())
		contract := BehavioralCoverageContract{
			ContractID:   "cov-open-surface",
			EffortID:     "effort-handoff",
			TaskID:       "impl-handoff",
			CoverageTier: "behavior_standard",
			EARS: []EARSRequirementCoverage{{
				ID:          "REQ-HANDOFF",
				Statement:   "When coverage is accepted, unresolved surface questions shall block accepted handoff.",
				MappedRefs:  []string{"BDD-HANDOFF"},
				Obligations: []string{"TDD-HANDOFF"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-HANDOFF",
				RequirementID:    "REQ-HANDOFF",
				Given:            []string{"an accepted promotion event exists"},
				When:             []string{"surface inventory still has an open question"},
				Then:             []string{"accepted coverage is not exposed to feature planning"},
				ConcreteDataRefs: []string{"fixtures/handoff.json"},
				Obligations:      []string{"TDD-HANDOFF"},
			}},
			SurfaceInventory: []BehavioralSurfaceCoverage{{
				ID:           "SURF-API",
				Surface:      "api_transport",
				Status:       "open_question",
				Rationale:    "operator must decide whether transport is in scope",
				QuestionRefs: []string{"Q-API"},
			}},
			TestObligations: []TestObligationCoverage{{
				ID:             "TDD-HANDOFF",
				Type:           "contract",
				Behavior:       "open surface questions block accepted handoff",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "implementation",
			}},
		}
		input.TDD.CoverageTier = "behavior_standard"
		input.TDD.Coverage = ProjectBehavioralCoverageContract(contract)
		input.TDD.BehavioralCoverage = &contract
		input.PromotionEvents = []BehavioralCoveragePromotionEvent{{
			Event:   "behavioral_coverage_accepted",
			EventID: "evt-open-surface",
			State:   BehavioralCoveragePromotionStateAccepted,
			Digest:  BehavioralCoverageContractDigest(contract),
		}}
		input.CoverageEvaluation = CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"}
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open_question surface should be valid structured contract state: %v", err))
		}
		gomega.Expect(!containsString(input.TDD.Coverage.GatesBlocked, "surface_inventory")).To(gomega.BeFalse(), fmt.Sprintf("open_question surface should block surface_inventory gate: %+v", input.TDD.Coverage))

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("open surface question should block accepted coverage contract, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.Status).To(gomega.Equal(string(ProductWorkflowStatusBlocked)), fmt.Sprintf("open surface question should block accepted coverage contract, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.AcceptedCoverage != nil).To(gomega.BeFalse(), fmt.Sprintf("open surface question must not be filtered out into accepted coverage: %+v", handoff.AcceptedCoverage))

	})
	ginkgo.It("BuildImplementationHandoffAllowsAcceptedCoverageWithWaivedMutationUnavailableTool", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())
		contract := BehavioralCoverageContract{
			ContractID:   "cov-waived-mutation",
			EffortID:     "effort-handoff",
			TaskID:       "impl-handoff",
			CoverageTier: "behavior_standard",
			EARS: []EARSRequirementCoverage{{
				ID:          "REQ-HANDOFF",
				Statement:   "When mutation tooling is unavailable, the system shall preserve accepted coverage with an explicit waiver.",
				MappedRefs:  []string{"BDD-HANDOFF"},
				Obligations: []string{"TDD-HANDOFF"},
			}},
			BDDScenarios: []BDDScenarioCoverage{{
				ID:               "BDD-HANDOFF",
				RequirementID:    "REQ-HANDOFF",
				Given:            []string{"mutation tooling is unavailable"},
				When:             []string{"coverage has an accepted waiver"},
				Then:             []string{"accepted coverage remains available to feature planning"},
				ConcreteDataRefs: []string{"fixtures/handoff.json"},
				Obligations:      []string{"TDD-HANDOFF"},
			}},
			SurfaceInventory: []BehavioralSurfaceCoverage{{
				ID:          "SURF-DOMAIN",
				Surface:     "domain_logic",
				Status:      "in_scope",
				Rationale:   "handoff behavior is in scope",
				Obligations: []string{"TDD-HANDOFF"},
			}},
			TestObligations: []TestObligationCoverage{{
				ID:             "TDD-HANDOFF",
				Type:           "contract",
				Behavior:       "waived mutation unavailable-tool coverage remains accepted",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "implementation",
			}},
			Mutation: BehavioralMutationCoverage{
				Requirement:  BehavioralMutationRequirementWaived,
				MinScore:     80,
				WaiverReason: "mutation tool unavailable in this environment",
			},
		}
		input.TDD.CoverageTier = "behavior_standard"
		input.TDD.Coverage = ProjectBehavioralCoverageContract(contract)
		input.TDD.BehavioralCoverage = &contract
		input.PromotionEvents = []BehavioralCoveragePromotionEvent{{
			Event:   "behavioral_coverage_accepted",
			EventID: "evt-waived-mutation",
			State:   BehavioralCoveragePromotionStateAccepted,
			Digest:  BehavioralCoverageContractDigest(contract),
		}}
		input.CoverageEvaluation = CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"}
		gomega.Expect(!containsString(input.TDD.Coverage.GatesBlocked, "mutation_unavailable_tool")).To(gomega.BeFalse(), fmt.Sprintf("waived mutation should project unavailable-tool token: %+v", input.TDD.Coverage))

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("waived mutation unavailable-tool token should not block accepted coverage, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.Status).To(gomega.Equal(BehavioralCoverageStatusAccepted), fmt.Sprintf("waived mutation unavailable-tool token should not block accepted coverage, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.AcceptedCoverage).NotTo(gomega.BeNil(), fmt.Sprintf("waived mutation accepted coverage should remain exposed, got %+v", handoff.AcceptedCoverage))
		gomega.Expect(handoff.AcceptedCoverage.Status).To(gomega.Equal(BehavioralCoverageStatusAccepted), fmt.Sprintf("waived mutation accepted coverage should remain exposed, got %+v", handoff.AcceptedCoverage))

	})
	ginkgo.It("BuildImplementationHandoffDoesNotExposeBlockedBehavioralCoverageAsAccepted", func() {
		input := implementationHandoffInput(ginkgo.GinkgoT().TempDir())
		contract := BehavioralCoverageContract{
			ContractID:   "cov-blocked",
			EffortID:     "effort-handoff",
			TaskID:       "impl-handoff",
			CoverageTier: "behavior_standard",
			BDDScenarios: []BDDScenarioCoverage{{
				ID:     "BDD-INCOMPLETE",
				Given:  []string{"accepted behavioral coverage is incomplete"},
				When:   []string{"handoff is built"},
				Then:   []string{"coverage must not be exposed as accepted"},
				Status: BehavioralCoverageStatusAccepted,
			}},
		}
		input.TDD.CoverageTier = "behavior_standard"
		input.TDD.Coverage = TDDCoverage{BDDTotal: 1}
		input.TDD.BehavioralCoverage = &contract
		input.CoverageEvaluation = CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"}

		handoff := BuildImplementationHandoff(input)
		gomega.Expect(handoff.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("blocked behavioral coverage should still expose a non-accepted diagnostic ref, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.CoverageContract.Status).To(gomega.Equal(string(ProductWorkflowStatusBlocked)), fmt.Sprintf("blocked behavioral coverage should still expose a non-accepted diagnostic ref, got %+v", handoff.CoverageContract))
		gomega.Expect(handoff.AcceptedCoverage != nil).To(gomega.BeFalse(), fmt.Sprintf("blocked behavioral coverage must not be exposed as accepted coverage: %+v", handoff.AcceptedCoverage))

	})
	ginkgo.It("LoadImplementationHandoffReadsEffortArtifacts", ginkgo.Label("integration"), func() {
		root := ginkgo.GinkgoT().TempDir()
		writeImplementationHandoffEffort(root)

		handoff, err := LoadImplementationHandoff(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadImplementationHandoff: %v", err))
		gomega.Expect(handoff.EffortRoot).To(gomega.Equal(root), fmt.Sprintf("unexpected roots: effort=%q artifact=%q", handoff.EffortRoot, handoff.ArtifactRootPath))
		gomega.Expect(handoff.ArtifactRootPath).To(gomega.Equal(root), fmt.Sprintf("unexpected roots: effort=%q artifact=%q", handoff.EffortRoot, handoff.ArtifactRootPath))
		gomega.Expect(!handoff.CoverageGatePassed).To(gomega.BeFalse(), fmt.Sprintf("expected coverage gate pass, blockers=%#v brief blockers=%#v", handoff.CoverageBlockers, handoff.Blockers))
		gomega.Expect(!handoffTestStringSliceContains(handoff.ContextEvidencePaths, "internal/workflows/productworkflow/brief_generation.go")).To(gomega.BeFalse(), fmt.Sprintf("expected workflow referenced path, got %#v", handoff.ContextEvidencePaths))
		gomega.Expect(!handoffTestStringSliceContains(handoff.FilesToModify, "internal/workflows/productworkflow/implementation_handoff.go")).To(gomega.BeFalse(), fmt.Sprintf("expected implementation ledger scope file, got %#v", handoff.FilesToModify))
		gomega.Expect(!handoffTestStringSliceContains(handoff.AuthorizedNewPackageRoots, "internal/newpkg")).To(gomega.BeFalse(), fmt.Sprintf("loaded handoff should preserve missing-file package root authority, got %#v", handoff.AuthorizedNewPackageRoots))

	})
})

func implementationHandoffInput(root string) BriefGenerationInput {
	return BriefGenerationInput{
		EffortRoot:       root,
		ArtifactRootPath: root,
		WorkflowState: WorkflowState{
			SchemaVersion:    "idiom.product_workflow_state.v1",
			EffortID:         "effort-handoff",
			TaskID:           "impl-handoff",
			Title:            "Implementation handoff",
			StatusBucket:     "started",
			CurrentPhase:     string(PhaseTDDSynthesis),
			CurrentState:     WorkflowStatePlanned,
			ArtifactPaths:    map[string]string{"events": "events.jsonl"},
			Coverage:         CoverageState{CoverageTier: "unit"},
			Privacy:          PrivacyState{RedactionStatus: "applied"},
			FolderPath:       root,
			NextValidActions: []string{"finalize"},
			DurabilityLevel:  "source_controlled",
			Context: WorkflowContext{
				ContextPackID:   "pack-handoff",
				FreshnessState:  "current",
				ReferencedPaths: []string{"internal/workflows/productworkflow/brief_generation.go"},
				PathAliases: map[string]string{
					"internal/workflows/productworkflow/types_reports.go": "internal/workflows/productworkflow/implementation_handoff.go",
				},
				RefreshPolicy: "on_demand",
			},
		},
		PRD: PRDDocument{
			FrontMatter: PRDFrontMatter{
				SchemaVersion:     "idiom.prd.v1",
				PRDID:             "PRD-HANDOFF",
				Status:            "accepted",
				TaskID:            "impl-handoff",
				CreatedAt:         "2026-06-03T00:00:00Z",
				AcceptedAt:        "2026-06-03T00:01:00Z",
				AcceptedBy:        "test",
				AcceptanceProfile: "human-review",
			},
			Requirements: []PRDRequirement{{
				ID:             "REQ-HANDOFF",
				Priority:       "must",
				Statement:      "Carry product workflow implementation handoff context",
				EvidenceRefs:   []string{"EV-HANDOFF"},
				TDDObligations: []string{"TDD-HANDOFF"},
				OwnerAlignment: "operator-approved",
				Testability:    "go test ./internal/workflows/productworkflow",
				Metric:         "featureplanner context contains product artifacts",
			}},
		},
		ADRIndex: ADRIndex{SchemaVersion: "idiom.adr_index.v1", EffortID: "effort-handoff", ADRs: []ADRIndexEntry{{
			ADRID:        "ADR-HANDOFF",
			Status:       "accepted",
			Path:         "001-handoff.md",
			CoverageTier: "unit",
			PRDRefs:      []string{"REQ-HANDOFF"},
		}}},
		ADRs: []ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					SchemaVersion:     "idiom.adr.v1",
					ADRID:             "ADR-HANDOFF",
					Status:            "accepted",
					PRDRefs:           []string{"REQ-HANDOFF"},
					EvidenceRefs:      []string{"EV-HANDOFF"},
					TDDObligations:    []string{"TDD-HANDOFF"},
					CoverageTier:      "unit",
					AcceptedAt:        "2026-06-03T00:02:00Z",
					AcceptedBy:        "test",
					AcceptanceProfile: "human-review",
				},
				BodyMarkdown: strings.Join([]string{
					"## Context",
					"Product workflow needs one handoff authority.",
					"## Decision",
					"Keep source path authority in productworkflow.",
					"## Alternatives Considered",
					"Keep CLI-local mapping.",
					"## Evidence",
					"EV-HANDOFF",
					"## Consequences",
					"Featureplanner consumes an adapter.",
					"## Compatibility and Migration",
					"Legacy DTO remains.",
					"## TDD Obligations",
					"TDD-HANDOFF",
					"## Rollback",
					"Restore CLI-local mapping.",
					"## Open Questions",
					"None.",
				}, "\n\n"),
			},
			RelPath:     "adrs/001-handoff.md",
			RawMarkdown: "## Decision\n\nKeep source path authority in productworkflow.\n\n## Open Questions\n\nNone.\n",
		}},
		TDD: TDDLedger{
			SchemaVersion: "idiom.tdd_ledger.v1",
			EffortID:      "effort-handoff",
			TaskID:        "impl-handoff",
			CoverageTier:  "unit",
			Coverage:      TDDCoverage{MutationMinScore: 1},
			Obligations: []TDDObligation{{
				ID:             "TDD-HANDOFF",
				Type:           "regression",
				Behavior:       "Implementation handoff maps to planner context",
				Owner:          "internal/workflows/productworkflow/implementation_handoff_test.go",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "implementation",
				Status:         "planned",
			}},
		},
		Traceability: TraceabilityMatrix{
			SchemaVersion: "idiom.traceability_matrix.v1",
			EffortID:      "effort-handoff",
			TaskID:        "impl-handoff",
			CreatedAt:     "2026-06-03T00:03:00Z",
			Matrix: []TraceabilityRow{{
				EvidenceCard:   "EV-HANDOFF",
				PRDRequirement: "REQ-HANDOFF",
				ADR:            "ADR-HANDOFF",
				TDDObligation:  "TDD-HANDOFF",
				Brief:          "briefs/impl-handoff.md",
				Report:         "reports/impl-handoff.json",
			}},
		},
		ImplementationRecords: []ImplementationLedgerRecord{
			{
				RecordType:           LedgerRecordAtomPlanned,
				TaskID:               "impl-handoff",
				EffortID:             "effort-handoff",
				Timestamp:            "2026-06-03T00:04:00Z",
				ComplexityTier:       "C1",
				ContextPackID:        "pack-handoff",
				Scope:                &AtomScope{ExpectedFiles: []string{"internal/workflows/productworkflow/types_reports.go", "internal/newpkg/new_file.go"}, Forbidden: []string{"internal/surfaces/tui/app/feature_planner_product.go"}},
				RequiredVerification: []string{"go test ./internal/cmd/featureplanner"},
				ReportPaths:          &ReportPaths{Implementation: "reports/impl.json", Review: "reports/review.json", Investigation: "reports/investigation.json"},
				Implementor:          &ImplementorAssignment{ID: "impl", Runtime: "codex", Model: "test", Command: "run"},
				Reviewer:             &ReviewerAssignment{ID: "rev", Runtime: "codex", Model: "test", ReasoningEffort: "medium"},
			},
			{
				RecordType:              LedgerRecordVerificationCompleted,
				TaskID:                  "impl-handoff",
				EffortID:                "effort-handoff",
				Timestamp:               "2026-06-03T00:05:00Z",
				TDDObligationsSatisfied: []string{"TDD-HANDOFF"},
				VerificationStatus:      "pass",
				VerificationCommandsRun: []string{"go test ./internal/workflows/productworkflow"},
			},
		},
		CoverageEvaluation:    CoverageGateEvaluation{Status: "pass", CoverageTier: "unit"},
		ExistingWorkspacePath: map[string]bool{"internal/workflows/productworkflow/implementation_handoff.go": true},
	}
}

func writeImplementationHandoffEffort(root string) {
	ginkgo.GinkgoHelper()

	input := implementationHandoffInput(root)
	writeImplementationHandoffJSON(filepath.Join(root, "workflow_state.json"), input.WorkflowState)
	eventsPath, err := productWorkflowEventsPath(input.WorkflowState)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(filepath.Join(root, eventsPath), nil, 0o644)).To(gomega.Succeed())
	{

		err := os.WriteFile(filepath.Join(root, "prd.md"), []byte(RenderPRDMarkdown(input.PRD)), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write prd.md: %v", err))
	}

	writeImplementationHandoffJSON(filepath.Join(root, "tdd.json"), input.TDD)
	{

		err := os.MkdirAll(filepath.Join(root, "adrs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir adrs: %v", err))
	}

	writeImplementationHandoffJSON(filepath.Join(root, "adrs", "index.json"), input.ADRIndex)
	{

		err := os.WriteFile(filepath.Join(root, "adrs", "001-handoff.md"), []byte(RenderADRMarkdown(input.ADRs[0].Doc)), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write adr: %v", err))
	}
	{

		err := os.MkdirAll(filepath.Join(root, "evidence"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir evidence: %v", err))
	}

	writeImplementationHandoffJSON(filepath.Join(root, "evidence", "traceability_matrix.json"), input.Traceability)
	{

		err := os.MkdirAll(filepath.Join(root, "briefs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir briefs: %v", err))
	}
	{

		err := os.WriteFile(filepath.Join(root, "briefs", "impl-handoff.md"), []byte("# Brief\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write traceability brief: %v", err))
	}
	{

		err := os.MkdirAll(filepath.Join(root, "reports"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir reports: %v", err))
	}

	writeImplementationHandoffJSON(filepath.Join(root, "reports", "impl-handoff.json"), map[string]any{"status": "pass"})

	var ledgerLines []byte

	for _, record := range input.ImplementationRecords {
		recordData, err := json.Marshal(record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal implementation ledger: %v", err))

		ledgerLines = append(ledgerLines, recordData...)
		ledgerLines = append(ledgerLines, '\n')
	}
	{

		err := os.WriteFile(filepath.Join(root, "implementation_ledger.jsonl"), ledgerLines, 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write implementation ledger: %v", err))
	}

}

func writeImplementationHandoffJSON(path string, value any) {
	ginkgo.GinkgoHelper()

	data, err := json.MarshalIndent(value, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal %s: %v", path, err))
	{

		err := os.WriteFile(path, append(data, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}

}

func handoffTestStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}

	return false
}
