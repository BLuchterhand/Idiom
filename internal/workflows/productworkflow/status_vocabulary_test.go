package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("status vocabulary", func() {
	ginkgo.It("ProductWorkflowStatusPredicates", func() {
		gomega.Expect(!ProductWorkflowStatusIsPass(string(ProductWorkflowStatusPass))).To(gomega.BeFalse(), fmt.Sprintf("ProductWorkflowStatusIsPass(%q) = false", ProductWorkflowStatusPass))
		gomega.Expect(ProductWorkflowStatusIsPass(string(ProductWorkflowStatusAccepted))).To(gomega.BeFalse(), fmt.Sprintf("accepted status must not satisfy pass-only predicate"))

		for _, status := range []ProductWorkflowStatus{ProductWorkflowStatusAccepted, ProductWorkflowStatusPass} {
			gomega.Expect(!ProductWorkflowStatusIsAccepted(string(status))).To(gomega.BeFalse(), fmt.Sprintf("ProductWorkflowStatusIsAccepted(%q) = false", status))

		}
		gomega.Expect(ProductWorkflowStatusIsAccepted(string(ProductWorkflowStatusBlocked))).To(gomega.BeFalse(), fmt.Sprintf("blocked status must not satisfy accepted predicate"))

	})
})
