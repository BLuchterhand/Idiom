package workerruntime

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("constraint vocabulary", func() {
	for _, id := range []ConstraintID{
		ConstraintPlanThenExecuteSubmitPlanReady,
		ConstraintRequiredEditPathsDefineWriteScope,
		ConstraintExactAnchorImplementation,
		ConstraintImplementationPacketWriteReady,
		ConstraintAnswerReadinessReached,
		ConstraintChildPacketAvailable,
	} {
		gomega.Expect(ConstraintPresent([]string{" " + string(id) + " "}, id)).To(gomega.BeTrue(), fmt.Sprintf("ConstraintPresent did not find %q", id))
	}
})
