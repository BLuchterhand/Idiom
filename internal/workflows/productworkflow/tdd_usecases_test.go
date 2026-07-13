package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("tdd usecases", func() {
	ginkgo.It("PlanTDDObligationsFromEffortReportsMissingWorkflowGeneration", func() {
		result := PlanTDDObligationsFromEffort(ginkgo.GinkgoT().TempDir())

		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked))
		gomega.Expect(result.Plan.Obligations).NotTo(gomega.BeNil())
		gomega.Expect(result.Plan.Findings).To(gomega.ContainElement(gomega.SatisfyAll(
			gomega.HaveField("Kind", "tdd_generation_error"),
			gomega.HaveField("File", "tdd.json"),
			gomega.HaveField("Message", gomega.ContainSubstring(WorkflowStateFileName)),
		)))
	})

	ginkgo.It("PlanTDDObligationsFromEffortReportsMissingDocuments", func() {
		root := ginkgo.GinkgoT().TempDir()
		_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		result := PlanTDDObligationsFromEffort(root)
		gomega.Expect(result.Status != ProductWorkflowStatusBlocked).To(gomega.BeFalse(), fmt.Sprintf("expected blocked status, got %q", result.Status))
		gomega.Expect(result.Plan.Obligations).NotTo(gomega.BeNil(), fmt.Sprintf("expected stable non-nil result slices, got %#v", result.Plan))
		gomega.Expect(result.Plan.Findings).NotTo(gomega.BeNil(), fmt.Sprintf("expected stable non-nil result slices, got %#v", result.Plan))

		wantFiles := map[string]bool{"prd.md": false, "adrs/index.json": false, "tdd.json": false}
		for _, finding := range result.Plan.Findings {
			if finding.Kind == "missing_file" {
				if _, ok := wantFiles[finding.File]; ok {
					wantFiles[finding.File] = true
				}
			}
		}
		for file, found := range wantFiles {
			gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected missing_file finding for %s, got %#v", file, result.Plan.Findings))

		}
	})
	ginkgo.It("PromoteBehavioralCoverageFromEffortRejectsTraversalPath", func() {
		result := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   ginkgo.GinkgoT().TempDir(),
			ContractPath: "../coverage.json",
		})

		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked))
		gomega.Expect(result.ContractPath).To(gomega.BeEmpty())
		gomega.Expect(result.Findings).To(gomega.ContainElement(gomega.SatisfyAll(
			gomega.HaveField("Kind", "invalid_promote_coverage_path"),
			gomega.HaveField("File", "contract_path"),
		)))
	})
})
