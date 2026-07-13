package autonomy

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan-then-execute task vocabulary", func() {
	It("keeps planner, executor, routing, and runtime contract tokens stable", func() {
		Expect(string(TaskFamilyPlanThenExecutePlanner)).To(Equal("plan_then_execute_planner"))
		Expect(string(TaskFamilyPlanThenExecuteExecutor)).To(Equal("plan_then_execute_executor"))
		Expect(string(RouteReasonPlanThenExecuteRequest)).To(Equal("plan_then_execute_request"))
		Expect(string(RouteReasonPlanArtifactDispatch)).To(Equal("plan_artifact_dispatch"))
		Expect(string(RuntimeContractExecutorDispatchResult)).To(Equal("executor_dispatch_result"))
	})
})
