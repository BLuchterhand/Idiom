package implementationdispatch

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Plan document preflight aggregation", func() {
	ginkgo.It("skips empty and reference-only documents", func() {
		var visited []string
		blocked := PreflightPlanDocuments(context.Background(), PlanDocumentsPreflightInput{
			PlanDocuments: []PlanDocument{{Path: ""}, {Path: "docs/reference.md", ReferenceOnly: true}, {Path: "docs/dispatch.md"}},
			Runner: func(_ context.Context, input PlanDocumentPreflightInput) PlanDocumentPreflightResult {
				visited = append(visited, input.Document.Path)
				return PlanDocumentPreflightResult{Path: input.Document.Path, OK: true}
			},
		})
		gomega.Expect(blocked).To(gomega.BeEmpty())
		gomega.Expect(visited).To(gomega.Equal([]string{"docs/dispatch.md"}))
	})

	ginkgo.It("reports blocking results and backfills the document path", func() {
		blocked := PreflightPlanDocuments(context.Background(), PlanDocumentsPreflightInput{
			PlanDocuments: []PlanDocument{{Path: "docs/dispatch.md"}},
			Runner: func(_ context.Context, _ PlanDocumentPreflightInput) PlanDocumentPreflightResult {
				return PlanDocumentPreflightResult{OK: true, Blockers: []string{"brief lint: missing exact anchor"}}
			},
		})
		gomega.Expect(blocked).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Path", "docs/dispatch.md"),
			gomega.HaveField("Blockers", []string{"brief lint: missing exact anchor"}),
		)))
	})
})
