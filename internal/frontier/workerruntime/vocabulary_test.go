package workerruntime

import (
	"slices"
	"testing"
)

func TestRuntimeContractSpecUsesTypedVocabularyAndRendersBriefStrings(t *testing.T) {
	spec := RuntimeContractForTaskFamily("plan_then_execute_planner")
	if spec.TaskFamily != TaskFamilyPlanThenExecutePlanner {
		t.Fatalf("TaskFamily = %q, want %q", spec.TaskFamily, TaskFamilyPlanThenExecutePlanner)
	}
	if spec.PermissionProfile != PermissionGuardedWorkspaceWrite {
		t.Fatalf("PermissionProfile = %q, want %q", spec.PermissionProfile, PermissionGuardedWorkspaceWrite)
	}
	if spec.VerificationContract != VerificationNotRequiredTextMutation {
		t.Fatalf("VerificationContract = %q, want %q", spec.VerificationContract, VerificationNotRequiredTextMutation)
	}
	if !slices.Contains(spec.RequiredEvidence, RequiredEvidenceGitHistoryRationale) ||
		!slices.Contains(spec.RequiredEvidence, RequiredEvidencePlanSubmittedOrSourceSatisfied) {
		t.Fatalf("planner required evidence missing typed IDs: %#v", spec.RequiredEvidence)
	}

	brief := RuntimeBriefForTaskFamily("plan_then_execute_planner", "planner", []string{"read_file", "submit_plan"})
	if brief.PermissionProfile != string(PermissionGuardedWorkspaceWrite) {
		t.Fatalf("brief PermissionProfile = %q, want rendered typed value", brief.PermissionProfile)
	}
	if brief.VerificationContract != string(VerificationNotRequiredTextMutation) {
		t.Fatalf("brief VerificationContract = %q, want rendered typed value", brief.VerificationContract)
	}
	if !slices.Contains(brief.RequiredEvidence, string(RequiredEvidenceGitHistoryRationale)) ||
		!slices.Contains(brief.RequiredEvidence, string(RequiredEvidencePlanSubmittedOrSourceSatisfied)) {
		t.Fatalf("brief required evidence missing rendered IDs: %#v", brief.RequiredEvidence)
	}
}

func TestFeaturePlannerTaskFamiliesUseTypedVocabulary(t *testing.T) {
	planner := RuntimeContractForTaskFamily(string(TaskFamilyFeaturePlannerPlanner))
	if planner.TaskFamily != TaskFamilyFeaturePlannerPlanner {
		t.Fatalf("planner TaskFamily = %q, want %q", planner.TaskFamily, TaskFamilyFeaturePlannerPlanner)
	}
	if !TaskFamilyIsFeaturePlannerPlanner(string(TaskFamilyFeaturePlannerPlanner)) {
		t.Fatalf("feature planner planner helper should match canonical family")
	}

	implementor := RuntimeContractForTaskFamily(string(TaskFamilyFeaturePlannerImplementor))
	if implementor.TaskFamily != TaskFamilyFeaturePlannerImplementor {
		t.Fatalf("implementor TaskFamily = %q, want %q", implementor.TaskFamily, TaskFamilyFeaturePlannerImplementor)
	}
	if !TaskFamilyIsFeaturePlannerImplementor(string(TaskFamilyFeaturePlannerImplementor)) {
		t.Fatalf("feature planner implementor helper should match canonical family")
	}
}

func TestRoleSpecUsesTypedVocabularyAndRendersBriefStrings(t *testing.T) {
	spec := RoleSpecFor("workbranch_write")
	if spec.Name != RoleChildWrite {
		t.Fatalf("Name = %q, want %q", spec.Name, RoleChildWrite)
	}
	if spec.PermissionProfile != PermissionIsolatedWorkbranchWrite {
		t.Fatalf("PermissionProfile = %q, want %q", spec.PermissionProfile, PermissionIsolatedWorkbranchWrite)
	}
	if spec.VerificationContract != VerificationTestsPassedFailedSkipped {
		t.Fatalf("VerificationContract = %q, want %q", spec.VerificationContract, VerificationTestsPassedFailedSkipped)
	}

	brief := RuntimeBriefForRole("workbranch_write", "", "fix delegated task")
	if brief.Lane != string(RoleChildWrite) {
		t.Fatalf("Lane = %q, want role name", brief.Lane)
	}
	if brief.PermissionProfile != string(PermissionIsolatedWorkbranchWrite) {
		t.Fatalf("brief PermissionProfile = %q, want rendered typed value", brief.PermissionProfile)
	}
	if !slices.Contains(brief.RequiredEvidence, string(RequiredEvidenceChangedPathsOrNoopReason)) {
		t.Fatalf("brief required evidence missing changed paths label: %#v", brief.RequiredEvidence)
	}
}

func TestVerificationContractAndPermissionHelpers(t *testing.T) {
	if !VerificationContractSkipsGate(string(VerificationNotRequiredTextMutation)) {
		t.Fatalf("text mutation verification contract should skip gate")
	}
	if VerificationContractSkipsGate(string(VerificationTestsPassedFailedSkipped)) {
		t.Fatalf("test verification contract should not skip gate")
	}
	if VerificationContractSkipsGate("not_required_for_typo") {
		t.Fatalf("unknown not_required_for_* labels must not skip verification")
	}
	if !PermissionProfileIsReadOnlyWorkspace(" read_only_workspace ") {
		t.Fatalf("read-only workspace helper should trim and match")
	}
	if PermissionProfileIsConversationOnly(string(PermissionReadOnlyWorkspace)) {
		t.Fatalf("conversation-only helper should not match read-only workspace")
	}
	if !ConversationDirectAnswerReadOnly("conversation", "direct_answer", "read_only_workspace") {
		t.Fatalf("conversation direct-answer read-only helper should match canonical labels")
	}
	if !RuntimeContractRequiresInitialAction(string(ContractPatchSummary)) {
		t.Fatalf("patch-summary contract should require initial action")
	}
	if !TaskFamilyRequiresInitialActionContract(string(TaskFamilyDelegatedIsolatedImpl)) {
		t.Fatalf("delegated isolated implementation family should require initial action")
	}
}
