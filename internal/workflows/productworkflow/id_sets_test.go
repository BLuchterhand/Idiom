package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("id sets", func() {
	ginkgo.It("ProductWorkflowIDSetsTrimAndSkipBlankIDs", func() {
		evidence := EvidenceCardIDSet([]EvidenceCard{{CardID: " EV-1 "}, {CardID: ""}})
		gomega.Expect(evidence["EV-1"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected evidence IDs: %#v", evidence))
		gomega.Expect(evidence).To(gomega.HaveLen(1), fmt.Sprintf("unexpected evidence IDs: %#v", evidence))

		requirements := PRDRequirementIDSet([]PRDRequirement{{ID: " REQ-1 "}, {ID: ""}})
		gomega.Expect(requirements["REQ-1"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected requirement IDs: %#v", requirements))
		gomega.Expect(requirements).To(gomega.HaveLen(1), fmt.Sprintf("unexpected requirement IDs: %#v", requirements))

		adrs := ADRIndexIDSet(ADRIndex{ADRs: []ADRIndexEntry{{ADRID: " ADR-1 "}, {ADRID: ""}}})
		gomega.Expect(adrs["ADR-1"]).To(gomega.BeTrue(), fmt.Sprintf("unexpected ADR IDs: %#v", adrs))
		gomega.Expect(adrs).To(gomega.HaveLen(1), fmt.Sprintf("unexpected ADR IDs: %#v", adrs))

	})
})
