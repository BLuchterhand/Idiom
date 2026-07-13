package planexecute

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/workflowstate"
)

var _ = ginkgo.It("plan execute exact plan edits requires dispatchable state before heading scan", func() {
	prompt := "# Atom\n\n## Required Edits\n\npkg/feature.go\n\nOld text:\n```go\nreturn 1\n```\n\nNew text:\n```go\nreturn 2\n```\n"
	edits, reason := planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt, []string{"pkg/feature.go"}, "", PlanExecuteDispatchState{
		WorkflowID:       workflowstate.WorkflowPlanExecutePlanner,
		State:            workflowstate.StateTerminal,
		ExpectedArtifact: planExecuteExpectedArtifactExactPlan,
		Reason:           "missing-scope",
	})
	gomega.Expect(len(edits) != 0).To(gomega.BeFalse(), fmt.Sprintf("non-dispatchable state should not parse heading edits: %#v", edits))
	gomega.Expect(reason != "missing-scope").To(gomega.BeFalse(), fmt.Sprintf("reason = %q, want typed non-dispatch reason", reason))
})

var _ = ginkgo.It("plan execute exact plan edits uses legacy heading only when dispatchable", func() {
	prompt := "# Atom\n\n## Required Edits\n\npkg/feature.go\n\nOld text:\n```go\nreturn 1\n```\n\nNew text:\n```go\nreturn 2\n```\n"
	edits, reason := planExecuteExactPlanEditsForWorkspaceWithDispatchState(prompt, []string{"pkg/feature.go"}, "", PlanExecuteDispatchState{
		WorkflowID:            workflowstate.WorkflowPlanExecutePlanner,
		State:                 workflowstate.StateDispatchable,
		Dispatchable:          true,
		ExpectedArtifact:      planExecuteExpectedArtifactExactPlan,
		LegacyHeadingFallback: true,
	})
	gomega.Expect(reason).To(gomega.Equal(""), fmt.Sprintf("dispatchable legacy heading parse failed edits=%#v reason=%q", edits, reason))
	gomega.Expect(edits).To(gomega.HaveLen(1), fmt.Sprintf("dispatchable legacy heading parse failed edits=%#v reason=%q", edits, reason))
	gomega.Expect(edits[0].Path).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("unexpected parsed edit: %#v", edits[0]))
	gomega.Expect(edits[0].OldText).To(gomega.ContainSubstring("return 1"), fmt.Sprintf("unexpected parsed edit: %#v", edits[0]))
	gomega.Expect(edits[0].NewText).To(gomega.ContainSubstring("return 2"), fmt.Sprintf("unexpected parsed edit: %#v", edits[0]))
})

var _ = ginkgo.It("plan execute exact plan dispatch state carries terminal state for missing scope", func() {
	state := planExecuteExactPlanDispatchState(nil)
	gomega.Expect(state.State != workflowstate.StateTerminal).To(gomega.BeFalse(), fmt.Sprintf("state = %q, want terminal", state.State))
	gomega.Expect(state.TerminalState != workflowstate.TerminalStateMissingScope).To(gomega.BeFalse(), fmt.Sprintf("terminal_state = %q, want missing-scope", state.TerminalState))
	gomega.Expect(state.Dispatchable).To(gomega.BeFalse(), fmt.Sprint("missing required paths must not be dispatchable"))
})
