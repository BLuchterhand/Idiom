package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"slices"
)

var _ = ginkgo.Describe("tool requirements", func() {
	ginkgo.It("concrete required tools projects symbolic workspace requirements", func() {
		historyTools := ConcreteRequiredTools(RequiredToolGitHistoryLookup)
		gomega.Expect(slices.Equal(historyTools, []string{ToolNameHistoryRange, ToolNameHistorySymbol})).To(gomega.BeTrue(), fmt.Sprintf("history required-tool projection = %#v", historyTools))

		writeTools := ConcreteRequiredTools(RequiredToolEditFileOrApplyPatch)
		wantWriteTools := WorkspacePerEditWriteToolNames()
		gomega.Expect(slices.Equal(writeTools, wantWriteTools)).To(gomega.BeTrue(), fmt.Sprintf("edit required-tool projection = %#v, want per-edit workspace writes %#v", writeTools, wantWriteTools))

		verifyTools := ConcreteRequiredTools(RequiredToolSourceSanityOrVerificationShell)
		gomega.Expect(slices.Equal(verifyTools, []string{ToolNameSourceSanity, ToolNameShell})).To(gomega.BeTrue(), fmt.Sprintf("verification required-tool projection = %#v", verifyTools))

		boundedEvidenceTools := ConcreteRequiredTools(RequiredToolBoundedWorkspaceEvidence)
		wantBoundedEvidenceTools := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource)
		gomega.Expect(slices.Equal(boundedEvidenceTools, wantBoundedEvidenceTools)).To(gomega.BeTrue(), fmt.Sprintf("bounded evidence projection = %#v, want candidate-source phase %#v", boundedEvidenceTools, wantBoundedEvidenceTools))

	})
	ginkgo.It("concrete required tools passes concrete tool names through", func() {
		if got := ConcreteRequiredTools(ToolNameSubmitPlan); !slices.Equal(got, []string{ToolNameSubmitPlan}) {
			ginkgo.Fail(fmt.Sprintf("concrete submit_plan projection = %#v", got))
		}

		if got := ConcreteRequiredTools(" "); len(got) != 0 {
			ginkgo.Fail(fmt.Sprintf("blank required tool projection = %#v", got))
		}
	})
})
