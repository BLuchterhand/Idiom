package productworkflow_test

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	. "idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.Describe("Durable product workflow repair", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	ginkgo.It("repairs a rejected PRD and resumes from the accepted durable phase", func() {
		t := ginkgo.GinkgoT()
		root := filepath.Join(t.TempDir(), "repairable-effort")

		_, err := InitializeEffort(CreateEffortOptions{
			EffortRoot: root,
			EffortID:   "REPAIR-001",
			Title:      "Repair a failed verification contract",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		draftContext, err := LoadPRDDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		initial := validDevelopmentJourneyPRD()
		initial.FrontMatter.PRDID = draftContext.Identity.PRDID
		initial.FrontMatter.TaskID = draftContext.Identity.TaskID
		initial.FrontMatter.CreatedAt = draftContext.Identity.CreatedAt
		initialIntake, err := PersistPRDDraft(PRDDraftPersistenceRequest{
			EffortRoot:         root,
			Document:           initial,
			ExpectedGeneration: draftContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(initialIntake.Passed).To(gomega.BeTrue())

		// Model external damage only after the owning transaction has created the
		// durable draft. The review gate must reject the damaged artifact, and the
		// repair path must replace it through a fresh loader-issued generation.
		invalid := initial
		invalid.Requirements[0].Testability = ""
		gomega.Expect(os.WriteFile(
			filepath.Join(root, "prd.md"),
			[]byte(RenderPRDMarkdown(invalid)),
			0o644,
		)).To(gomega.Succeed())

		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		blocked, blockedIntake, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(blocked.Status).To(gomega.Equal(ProductWorkflowStatusBlocked))
		gomega.Expect(blocked.CurrentPhase).To(gomega.Equal(PhasePRDReview))
		gomega.Expect(blocked.Blockers).To(gomega.ContainElement(gomega.ContainSubstring("missing testability")))
		gomega.Expect(blockedIntake.Passed).To(gomega.BeFalse())

		reopened, err := RequestCreateChanges(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(reopened.CurrentPhase).To(gomega.Equal(PhasePRDDrafting))

		draftContext, err = LoadPRDDraftContext(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		fixed := validDevelopmentJourneyPRD()
		fixed.FrontMatter.PRDID = draftContext.Identity.PRDID
		fixed.FrontMatter.TaskID = draftContext.Identity.TaskID
		fixed.FrontMatter.CreatedAt = draftContext.Identity.CreatedAt
		fixedIntake, err := PersistPRDDraft(PRDDraftPersistenceRequest{
			EffortRoot:         root,
			Document:           fixed,
			ExpectedGeneration: draftContext.Generation,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(fixedIntake.Passed).To(gomega.BeTrue(), "failures=%+v", fixedIntake.Failures)

		_, err = SubmitPRDForReview(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		accepted, acceptedIntake, err := ApprovePRDGate(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(acceptedIntake.Passed).To(gomega.BeTrue())
		gomega.Expect(accepted.CurrentPhase).To(gomega.Equal(PhaseADRDrafting))

		resumed, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resumed.EffortID).To(gomega.Equal("REPAIR-001"))
		gomega.Expect(resumed.CurrentPhase).To(gomega.Equal(PhaseADRDrafting))

		body, err := os.ReadFile(filepath.Join(root, "prd.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		prd, err := ParsePRDMarkdown(body)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(prd.FrontMatter.Status).To(gomega.Equal(string(ArtifactStateAccepted)))
		gomega.Expect(prd.Requirements[0].Testability).NotTo(gomega.BeEmpty())
	})
})

func validDevelopmentJourneyPRD() PRDDocument {
	return PRDDocument{
		FrontMatter: PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1",
			PRDID:         "PRD-REPAIR-001",
			Status:        string(ArtifactStateDraft),
			TaskID:        "REPAIR-001",
			CreatedAt:     "2026-07-12T00:00:00Z",
		},
		Requirements: []PRDRequirement{{
			ID:                   "REQ-001",
			Priority:             "P0",
			Statement:            "A failed verification contract can be corrected without replacing durable effort authority.",
			EvidenceRefs:         []string{"E1"},
			TDDObligations:       []string{"TDD-001"},
			OwnerAlignment:       "Engineering",
			Testability:          "The corrected contract passes intake and preserves the effort identity.",
			Metric:               "One durable effort reaches ADR drafting after one rejected revision.",
			OpenQuestions:        []string{},
			OpenQuestionsPresent: true,
		}},
	}
}
