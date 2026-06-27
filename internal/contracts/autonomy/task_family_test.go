package autonomy

import "testing"

func TestPlanThenExecuteTaskFamilyVocabulary(t *testing.T) {
	if TaskFamilyPlanThenExecutePlanner != "plan_then_execute_planner" {
		t.Fatalf("planner task family = %q", TaskFamilyPlanThenExecutePlanner)
	}
	if TaskFamilyPlanThenExecuteExecutor != "plan_then_execute_executor" {
		t.Fatalf("executor task family = %q", TaskFamilyPlanThenExecuteExecutor)
	}
	if RouteReasonPlanThenExecuteRequest != "plan_then_execute_request" {
		t.Fatalf("planner route reason = %q", RouteReasonPlanThenExecuteRequest)
	}
	if RouteReasonPlanArtifactDispatch != "plan_artifact_dispatch" {
		t.Fatalf("executor route reason = %q", RouteReasonPlanArtifactDispatch)
	}
	if RuntimeContractExecutorDispatchResult != "executor_dispatch_result" {
		t.Fatalf("executor runtime contract = %q", RuntimeContractExecutorDispatchResult)
	}
}
