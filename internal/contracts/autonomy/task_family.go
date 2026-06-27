package autonomy

type TaskFamilyID string

const (
	TaskFamilyArtifactFinalAnswer       TaskFamilyID = "artifact_final_answer"
	TaskFamilyArtifactDistiller         TaskFamilyID = "artifact_distiller"
	TaskFamilyConversation              TaskFamilyID = "conversation"
	TaskFamilyDebugLogs                 TaskFamilyID = "debug_logs"
	TaskFamilyVerificationOnly          TaskFamilyID = "verification_only"
	TaskFamilyLocate                    TaskFamilyID = "locate"
	TaskFamilyLocateThenImplement       TaskFamilyID = "locate_then_implement"
	TaskFamilyExploreThenImplement      TaskFamilyID = "explore_then_implement"
	TaskFamilyInvestigateThenImplement  TaskFamilyID = "investigate_then_implement"
	TaskFamilyReviewThenImplement       TaskFamilyID = "review_then_implement"
	TaskFamilyArchitectureThenImplement TaskFamilyID = "architecture_then_implement"
	TaskFamilySecurityReview            TaskFamilyID = "security_review"
	TaskFamilyPerformanceReview         TaskFamilyID = "performance_review"
	TaskFamilyArchitecture              TaskFamilyID = "architecture"
	TaskFamilyFeaturePlannerPlanner     TaskFamilyID = "feature_planner_planner"
	TaskFamilyFeaturePlannerImplementor TaskFamilyID = "feature_planner_implementor"
	TaskFamilyPlanThenExecutePlanner    TaskFamilyID = "plan_then_execute_planner"
	TaskFamilyPlanThenExecuteExecutor   TaskFamilyID = "plan_then_execute_executor"
	TaskFamilyPlanThenExecuteReview     TaskFamilyID = "plan_then_execute_review_repair"
	TaskFamilyImplementation            TaskFamilyID = "implementation"
	TaskFamilyTestAuthoring             TaskFamilyID = "test_authoring"
	TaskFamilyConfigEdit                TaskFamilyID = "config_edit"
	TaskFamilyScriptEdit                TaskFamilyID = "script_edit"
	TaskFamilyGDScriptEdit              TaskFamilyID = "gdscript_edit"
	TaskFamilyDocsEdit                  TaskFamilyID = "docs_edit"
	TaskFamilyPromptEditSecurity        TaskFamilyID = "prompt_edit_security"
	TaskFamilyFailureInjection          TaskFamilyID = "failure_injection"
	TaskFamilyDirectQuestion            TaskFamilyID = "direct_question"
	TaskFamilyDirectAnswer              TaskFamilyID = "direct_answer"
	TaskFamilyDelegatedIsolatedImpl     TaskFamilyID = "delegated_isolated_implementation"
	TaskFamilyDelegatedReadOnly         TaskFamilyID = "delegated_read_only"
	TaskFamilyImplementationRepair      TaskFamilyID = "implementation_repair"

	RouteReasonPlanThenExecuteRequest TaskFamilyRouteReason = "plan_then_execute_request"
	RouteReasonPlanArtifactDispatch   TaskFamilyRouteReason = "plan_artifact_dispatch"

	RuntimeContractExecutorDispatchResult TaskFamilyRuntimeContract = "executor_dispatch_result"
)

type TaskFamilyRouteReason string
type TaskFamilyRuntimeContract string

func TaskFamilyUsesImplementationLane(value TaskFamilyID) bool {
	switch value {
	case TaskFamilyImplementation,
		TaskFamilyTestAuthoring,
		TaskFamilyConfigEdit,
		TaskFamilyScriptEdit,
		TaskFamilyGDScriptEdit,
		TaskFamilyDocsEdit,
		TaskFamilyPromptEditSecurity,
		TaskFamilyFailureInjection,
		TaskFamilyPlanThenExecutePlanner:
		return true
	default:
		return false
	}
}
