package featureplanner

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.It("product workflow brief JSON round trip", func() {
	brief := ProductWorkflowBrief{
		EffortID: "test-effort-1",
		TaskID:   "impl-0001",
		PRDRequirements: []PRDRequirementBrief{
			{ID: "REQ-001", Priority: "P0", Statement: "Do the thing", Testability: "automated", Obligations: []string{"TDD-1"}},
		},
		AcceptedADRIDs:   []string{"ADR-001"},
		AcceptedADRPaths: []string{"adrs/001-test.md"},
		TDDObligations: []TDDObligationBrief{
			{ID: "TDD-1", Behavior: "test passes", Command: "go test ./...", RequiredBefore: "implementation", Status: "open"},
		},
		CoverageTier:       "behavior_standard",
		CoverageGatePassed: true,
		CoverageContract: &CoverageContractRef{
			ContractID:   "cov-1",
			Digest:       "sha256:abc",
			EventID:      "evt-cov-1",
			Status:       "pass",
			EffortID:     "test-effort-1",
			CoverageTier: "behavior_standard",
		},
		AcceptedCoverage: &AcceptedBehavioralCoverage{
			Authority: "product_workflow",
			Status:    "pass",
			Tier:      "behavior_standard",
			Contract: CoverageContractRef{
				ContractID:   "cov-1",
				Digest:       "sha256:abc",
				EventID:      "evt-cov-1",
				Status:       "pass",
				EffortID:     "test-effort-1",
				CoverageTier: "behavior_standard",
			},
			Scenarios: []AcceptedCoverageScenario{{
				ID:               "BDD-1",
				Given:            []string{"accepted product coverage exists"},
				When:             []string{"brief is serialized"},
				Then:             []string{"accepted coverage survives"},
				ConcreteDataRefs: []string{"fixtures/accepted.json"},
				Obligations:      []string{"TDD-1"},
			}},
			TestObligations: []AcceptedCoverageTestObligation{{
				ID:             "TDD-1",
				Type:           "contract",
				Behavior:       "accepted coverage round-trips",
				Command:        "go test ./internal/workflows/featureplanner",
				RequiredBefore: "implementation",
			}},
		},
		CoverageBlockers:        []string{},
		SourceSatisfiedDelivery: validProductSourceSatisfiedDeliveryCloseout("test-effort-1", "impl-0001"),
		FilesToModify:           []string{"internal/example/thing.go"},
		SourceFacts: []SourceFactBrief{{
			Path:         "internal/example/thing.go",
			Symbol:       "DoThing",
			Signature:    "func DoThing() error",
			AuthorityRef: "accepted-source",
		}},
		RequiredBehavior: []string{"thing must work"},
		Tests:            []string{"go test ./internal/example/..."},
		Acceptance:       []string{"Given thing, when used, then it works"},
		Constraints:      []string{"no new dependencies"},
		Decisions:        []string{"use map not slice"},
		Blockers:         []string{},
		ArtifactRootPath: "started/test-effort",
	}

	data, err := json.Marshal(brief)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	var roundTripped ProductWorkflowBrief
	{

		err := json.Unmarshal(data, &roundTripped)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal: %v", err))
	}
	gomega.Expect(roundTripped.EffortID).To(gomega.Equal(brief.EffortID), fmt.Sprintf("EffortID: got %q want %q", roundTripped.EffortID, brief.EffortID))
	gomega.Expect(roundTripped.TaskID).To(gomega.Equal(brief.TaskID), fmt.Sprintf("TaskID: got %q want %q", roundTripped.TaskID, brief.TaskID))
	gomega.Expect(len(roundTripped.PRDRequirements)).To(gomega.Equal(len(brief.PRDRequirements)), fmt.Sprintf("PRDRequirements length: got %d want %d", len(roundTripped.PRDRequirements), len(brief.PRDRequirements)))
	gomega.Expect(roundTripped.PRDRequirements[0].ID).To(gomega.Equal("REQ-001"), fmt.Sprintf("PRDRequirements[0].ID: got %q want REQ-001", roundTripped.PRDRequirements[0].ID))
	gomega.Expect(len(roundTripped.TDDObligations)).To(gomega.Equal(1), fmt.Sprintf("TDDObligations length: got %d want 1", len(roundTripped.TDDObligations)))
	gomega.Expect(roundTripped.TDDObligations[0].ID).To(gomega.Equal("TDD-1"), fmt.Sprintf("TDDObligations[0].ID: got %q want TDD-1", roundTripped.TDDObligations[0].ID))
	gomega.Expect(roundTripped.CoverageGatePassed).To(gomega.BeTrue(), fmt.Sprint("CoverageGatePassed should be true"))
	gomega.Expect(roundTripped.CoverageContract).NotTo(gomega.BeNil(), fmt.Sprintf("CoverageContract did not round-trip: %+v", roundTripped.CoverageContract))
	gomega.Expect(roundTripped.CoverageContract.ContractID).To(gomega.Equal("cov-1"), fmt.Sprintf("CoverageContract did not round-trip: %+v", roundTripped.CoverageContract))
	gomega.Expect(roundTripped.AcceptedCoverage).NotTo(gomega.BeNil(), fmt.Sprintf("AcceptedCoverage did not round-trip: %+v", roundTripped.AcceptedCoverage))
	gomega.Expect(roundTripped.AcceptedCoverage.Authority).To(gomega.Equal("product_workflow"), fmt.Sprintf("AcceptedCoverage did not round-trip: %+v", roundTripped.AcceptedCoverage))
	gomega.Expect(roundTripped.AcceptedCoverage.Scenarios).To(gomega.HaveLen(1), fmt.Sprintf("AcceptedCoverage did not round-trip: %+v", roundTripped.AcceptedCoverage))
	gomega.Expect(roundTripped.AcceptedCoverage.Scenarios[0].Then[0]).To(gomega.Equal("accepted coverage survives"), fmt.Sprintf("AcceptedCoverage did not round-trip: %+v", roundTripped.AcceptedCoverage))
	gomega.Expect(roundTripped.AcceptedCoverage.TestObligations).To(gomega.HaveLen(1), fmt.Sprintf("AcceptedCoverage did not round-trip: %+v", roundTripped.AcceptedCoverage))
	gomega.Expect(roundTripped.SourceFacts).To(gomega.HaveLen(1), fmt.Sprintf("SourceFacts did not round-trip: %+v", roundTripped.SourceFacts))
	gomega.Expect(roundTripped.SourceFacts[0].Signature).To(gomega.Equal("func DoThing() error"), fmt.Sprintf("SourceFacts did not round-trip: %+v", roundTripped.SourceFacts))
	gomega.Expect(roundTripped.SourceSatisfiedDelivery).To(gomega.Equal(brief.SourceSatisfiedDelivery))

})
var _ = ginkgo.It("product workflow brief minimal round trip", func() {
	brief := ProductWorkflowBrief{
		EffortID:     "minimal",
		CoverageTier: "local_low",
	}

	data, err := json.Marshal(brief)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal minimal: %v", err))

	var roundTripped ProductWorkflowBrief
	{

		err := json.Unmarshal(data, &roundTripped)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal minimal: %v", err))
	}
	gomega.Expect(roundTripped.EffortID).To(gomega.Equal("minimal"), fmt.Sprintf("EffortID: got %q want minimal", roundTripped.EffortID))

})
var _ = ginkgo.It("product workflow brief allows dispatch for pending implementation TDD evidence", func() {
	brief := ProductWorkflowBrief{
		CoverageGatePassed: false,
		CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
		CoverageBlockerKinds: []string{
			"coverage_tdd_evidence_missing",
		},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}
	gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue(), fmt.Sprint("expected pending implementation TDD verification to allow feature plan dispatch"))

})
var _ = ginkgo.It("product workflow brief closed state is not implicit source satisfied", func() {
	brief := ProductWorkflowBrief{
		StatusBucket:       "closed",
		CurrentState:       "closed",
		CurrentPhase:       "failed",
		CoverageGatePassed: true,
	}
	gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeFalse(), fmt.Sprint("generic closed state must not be treated as source-satisfied"))
	gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue(), fmt.Sprint("closed non-source-satisfied product context should not short-circuit dispatch as source-satisfied"))

})
var _ = ginkgo.It("product workflow brief explicit source satisfied closeout", func() {
	brief := ProductWorkflowBrief{
		EffortID:                "source-effort",
		TaskID:                  "source-task",
		StatusBucket:            "closed",
		CurrentState:            "closed",
		CurrentPhase:            "complete",
		CoverageGatePassed:      true,
		SourceSatisfiedDelivery: validProductSourceSatisfiedDeliveryCloseout("source-effort", "source-task"),
	}
	gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeTrue(), fmt.Sprint("typed source-satisfied delivery should close out source-satisfied"))
	gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeFalse(), fmt.Sprint("source-satisfied product context should not dispatch implementation"))

})
var _ = ginkgo.DescribeTable(
	"product workflow brief rejects manufactured source-satisfied closeout authority",
	func(mutate func(*ProductWorkflowBrief), label string) {
		brief := ProductWorkflowBrief{
			EffortID:                "source-effort",
			TaskID:                  "source-task",
			StatusBucket:            "closed",
			CurrentState:            "closed",
			CurrentPhase:            "complete",
			CoverageGatePassed:      true,
			SourceSatisfiedDelivery: validProductSourceSatisfiedDeliveryCloseout("source-effort", "source-task"),
		}
		mutate(&brief)

		gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeFalse(), label)
		gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue(), label)
	},
	ginkgo.Entry("without exact terminal lifecycle", func(brief *ProductWorkflowBrief) {
		brief.CurrentPhase = "tdd_synthesis"
	}, "nonterminal lifecycle must not suppress dispatch"),
	ginkgo.Entry("without session provenance", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.SessionID = ""
	}, "missing session provenance must not suppress dispatch"),
	ginkgo.Entry("with a mismatched effort identity", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.EffortID = "other-effort"
	}, "mismatched effort identity must not suppress dispatch"),
	ginkgo.Entry("with a mismatched task identity", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.TaskID = "other-task"
	}, "mismatched task identity must not suppress dispatch"),
	ginkgo.Entry("with a forged digest", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.CompletionDigest = "sha256:forged"
	}, "invalid completion digest must not suppress dispatch"),
	ginkgo.Entry("with mismatched evidence path", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.Evidence[0].Path = "internal/other.go"
	}, "mismatched current-state evidence must not suppress dispatch"),
	ginkgo.Entry("with an unsupported evidence kind", func(brief *ProductWorkflowBrief) {
		brief.SourceSatisfiedDelivery.Evidence[0].Kind = plancontract.SourceSatisfiedEvidenceKind("model_asserted")
	}, "unsupported current-state evidence must not suppress dispatch"),
)
var _ = ginkgo.It("product workflow brief rejects source satisfied looking lifecycle text without typed delivery authority", func() {
	brief := ProductWorkflowBrief{
		StatusBucket:       "closed",
		CurrentState:       "source_satisfied",
		CurrentPhase:       "source-satisfied-closeout",
		CoverageGatePassed: true,
	}
	gomega.Expect(brief.IsClosedSourceSatisfied()).To(gomega.BeFalse(), fmt.Sprint("lifecycle text must not substitute for typed source-satisfied delivery authority"))
	gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeTrue(), fmt.Sprint("untyped lifecycle text must not suppress feature-plan dispatch"))

})
var _ = ginkgo.It("product workflow brief typed coverage blocker kinds override prose", func() {
	brief := ProductWorkflowBrief{
		CoverageGatePassed: false,
		CoverageBlockers:   []string{"TDD verification evidence text in an unrelated blocker"},
		CoverageBlockerKinds: []string{
			"coverage_evidence_acceptance_incomplete",
		},
		TDDObligations: []TDDObligationBrief{{
			ID:             "TDD-1",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}
	gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeFalse(), fmt.Sprint("typed non-TDD coverage blocker must keep feature plan dispatch blocked"))

})
var _ = ginkgo.Describe("product workflow brief rejects ambiguous TDD evidence dispatch", func() {
	tests := []struct {
		name       string
		obligation TDDObligationBrief
	}{
		{
			name:       "missing command",
			obligation: TDDObligationBrief{ID: "TDD-1", RequiredBefore: "implementation", Status: "planned"},
		},
		{
			name:       "close only obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "close", Status: "planned"},
		},
		{
			name:       "failed obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "failed"},
		},
		{
			name:       "blocked obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "blocked"},
		},
		{
			name:       "already passed obligation",
			obligation: TDDObligationBrief{ID: "TDD-1", Command: "go test ./...", RequiredBefore: "implementation", Status: "passed"},
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			brief := ProductWorkflowBrief{
				CoverageGatePassed: false,
				CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
				TDDObligations:     []TDDObligationBrief{tt.obligation},
			}
			gomega.Expect(brief.AllowsFeaturePlanDispatch()).To(gomega.BeFalse(), fmt.Sprintf("expected %s to keep feature plan dispatch blocked", tt.name))

		})
	}
})

func validProductSourceSatisfiedDeliveryCloseout(effortID string, taskID string) *productworkflow.SourceSatisfiedDeliveryCloseout {
	closeout := &productworkflow.SourceSatisfiedDeliveryCloseout{
		EffortID:      effortID,
		TaskID:        taskID,
		Outcome:       plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
		SessionID:     "source-satisfied-session",
		EvidencePaths: []string{"internal/example.go"},
		Evidence: []plancontract.SourceSatisfiedEvidenceRef{{
			Path: "internal/example.go",
			Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
		}},
		RecordedAt: "2026-07-12T12:00:00Z",
	}
	authority := struct {
		EffortID      string                                    `json:"effort_id"`
		TaskID        string                                    `json:"task_id"`
		Outcome       plancontract.PlanExecuteTerminalOutcome   `json:"outcome"`
		SessionID     string                                    `json:"session_id"`
		EvidencePaths []string                                  `json:"evidence_paths"`
		Evidence      []plancontract.SourceSatisfiedEvidenceRef `json:"evidence"`
		RecordedAt    string                                    `json:"recorded_at"`
	}{
		EffortID:      closeout.EffortID,
		TaskID:        closeout.TaskID,
		Outcome:       closeout.Outcome,
		SessionID:     closeout.SessionID,
		EvidencePaths: closeout.EvidencePaths,
		Evidence:      closeout.Evidence,
		RecordedAt:    closeout.RecordedAt,
	}
	body, err := json.Marshal(authority)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	closeout.CompletionDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))

	return closeout
}
