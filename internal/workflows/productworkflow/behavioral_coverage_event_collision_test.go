package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("behavioral coverage event identity", func() {
	ginkgo.It("does not append when another event type owns the promotion event id", func() {
		root := ginkgo.GinkgoT().TempDir()
		contract, ledger := behavioralCoverageEventFixture("effort-cross-event-collision")
		writeBehavioralCoveragePromotionEffort(root, ledger, contract)

		preview := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   root,
			ContractPath: "behavioral-coverage.json",
		})
		gomega.Expect(preview.Status).To(gomega.Equal(ProductWorkflowStatusPass), "findings=%+v", preview.Findings)
		collisionID := behavioralCoveragePromotionEventID(preview.Promotion.Digest)
		gomega.Expect(collisionID).NotTo(gomega.BeEmpty())

		prior := ProductWorkflowTransitionEvent{
			Event:     string(ProductWorkflowEventTransitionApplied),
			EventID:   collisionID,
			EffortID:  ledger.EffortID,
			TaskID:    ledger.TaskID,
			Timestamp: "2026-07-12T18:00:00Z",
			Source:    string(ProductWorkflowEventSourceTransitionApply),
			Action:    "submit_prd",
			Details: ProductWorkflowTransitionEventDetail{
				FromState:     string(PhasePRDDrafting),
				ToState:       string(PhasePRDReview),
				StateRevision: 2,
			},
		}
		priorLine, err := json.Marshal(prior)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		priorLine = append(priorLine, '\n')
		eventsPath := filepath.Join(root, productWorkflowDefaultEventsPath)
		gomega.Expect(os.WriteFile(eventsPath, priorLine, 0o644)).To(gomega.Succeed())
		beforeTDD, err := os.ReadFile(filepath.Join(root, "tdd.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		promotion := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   root,
			ContractPath: "behavioral-coverage.json",
			Write:        true,
		})
		gomega.Expect(promotion.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), "findings=%+v", promotion.Findings)
		gomega.Expect(promotion.Promotion.LedgerCommitted).To(gomega.BeFalse())
		gomega.Expect(promotion.Promotion.CommitStatus).To(gomega.Equal(BehavioralCoveragePromotionCommitStatusBlocked))
		gomega.Expect(promotion.Promotion.AuditStatus).To(gomega.Equal(BehavioralCoveragePromotionAuditStatusNotAttempted))
		gomega.Expect(promotion.Promotion.EventID).To(gomega.BeEmpty())
		gomega.Expect(promotion.Findings).To(gomega.ContainElement(gomega.HaveField(
			"Message",
			gomega.ContainSubstring("already belongs to event type \"product_workflow_transition_applied\""),
		)))
		afterTDD, err := os.ReadFile(filepath.Join(root, "tdd.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(afterTDD).To(gomega.Equal(beforeTDD), "a cross-type event collision must not commit conflicting provenance to tdd.json")

		written, err := os.ReadFile(eventsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(written).To(gomega.Equal(priorLine))
		matches, err := productWorkflowEventEnvelopesByID(written, collisionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(matches).To(gomega.HaveLen(1))
		gomega.Expect(matches[0].Event).To(gomega.Equal(ProductWorkflowEventTransitionApplied))
	})

	ginkgo.It("keeps a repeated promotion idempotent after reserving its event identity", func() {
		root := ginkgo.GinkgoT().TempDir()
		contract, ledger := behavioralCoverageEventFixture("effort-idempotent-promotion")
		writeBehavioralCoveragePromotionEffort(root, ledger, contract)

		first := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   root,
			ContractPath: "behavioral-coverage.json",
			Write:        true,
		})
		gomega.Expect(first.Status).To(gomega.Equal(ProductWorkflowStatusPass), "findings=%+v", first.Findings)
		gomega.Expect(first.Promotion.AuditStatus).To(gomega.Equal(BehavioralCoveragePromotionAuditStatusRecorded))
		gomega.Expect(first.Promotion.EventID).NotTo(gomega.BeEmpty())

		tddPath := filepath.Join(root, "tdd.json")
		eventsPath := filepath.Join(root, productWorkflowDefaultEventsPath)
		firstTDD, err := os.ReadFile(tddPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		firstEvents, err := os.ReadFile(eventsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		second := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   root,
			ContractPath: "behavioral-coverage.json",
			Write:        true,
		})
		gomega.Expect(second.Status).To(gomega.Equal(ProductWorkflowStatusPass), "findings=%+v", second.Findings)
		gomega.Expect(second.Promotion.EventID).To(gomega.Equal(first.Promotion.EventID))
		gomega.Expect(second.Promotion.AuditStatus).To(gomega.Equal(BehavioralCoveragePromotionAuditStatusRecorded))

		secondTDD, err := os.ReadFile(tddPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		secondEvents, err := os.ReadFile(eventsPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(secondTDD).To(gomega.Equal(firstTDD))
		gomega.Expect(secondEvents).To(gomega.Equal(firstEvents), "an identical event authority must not append a duplicate line")
		matches, err := productWorkflowEventEnvelopesByID(secondEvents, second.Promotion.EventID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(matches).To(gomega.HaveLen(1))
	})
})

func behavioralCoverageEventFixture(effortID string) (BehavioralCoverageContract, TDDLedger) {
	contract := promotableBehavioralCoverageContract()
	mutationScore := 80.0
	contract.CoverageTier = "unit"
	contract.Mutation = BehavioralMutationCoverage{
		Requirement: BehavioralMutationRequirementRequired,
		MinScore:    80,
		Score:       &mutationScore,
	}
	contract.TestObligations[0].Type = "contract"

	return contract, TDDLedger{
		SchemaVersion: "idiom.tdd_obligations.v2",
		EffortID:      effortID,
		TaskID:        effortID,
		CoverageTier:  "unit",
		Coverage:      TDDCoverage{MutationMinScore: 80},
		Obligations: []TDDObligation{{
			ID:             "TDD-BASE",
			Type:           "contract",
			Behavior:       "preserve the existing product-workflow obligation",
			Owner:          "productworkflow",
			Command:        "go test ./internal/workflows/productworkflow",
			RequiredBefore: "implementation",
			Status:         "pending",
		}},
	}
}
