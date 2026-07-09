package workerruntime

import (
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestRuntimeProfileForTaskFamilyCharacterizesRouteSemantics(t *testing.T) {
	cases := []struct {
		name                  string
		family                string
		wantFamily            string
		wantInitialLane       string
		wantFollowupLane      string
		wantContract          RuntimeContractID
		wantReason            string
		wantMutationCapable   bool
		wantEvidenceFirst     bool
		wantPlanSubmission    bool
		wantDirectAnswer      bool
		wantVerificationOwner RuntimeVerificationOwner
	}{
		{name: "direct question alias", family: "direct_question", wantFamily: "conversation", wantInitialLane: "main", wantContract: ContractDirectAnswer, wantReason: "conversation", wantDirectAnswer: true},
		{name: "debug logs collapse", family: "debug_logs", wantFamily: "conversation", wantInitialLane: "main", wantContract: ContractDirectAnswer, wantReason: "legacy_debug_logs_collapsed_to_conversation", wantDirectAnswer: true},
		{name: "conversation", family: "conversation", wantFamily: "conversation", wantInitialLane: "main", wantContract: ContractDirectAnswer, wantReason: "conversation", wantDirectAnswer: true},
		{name: "verification only", family: "verification_only", wantFamily: "verification_only", wantInitialLane: "main", wantContract: ContractDirectAnswer, wantReason: "verification_request", wantDirectAnswer: true},
		{name: "locate", family: "locate", wantFamily: "locate", wantInitialLane: "locate", wantContract: ContractLocateTargets, wantReason: "locate_request"},
		{name: "locate then implement", family: "locate_then_implement", wantFamily: "locate_then_implement", wantInitialLane: "locate", wantFollowupLane: "implement", wantContract: ContractImplementationResult, wantReason: "locate_request", wantMutationCapable: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "explore then implement", family: "explore_then_implement", wantFamily: "explore_then_implement", wantInitialLane: "explore", wantFollowupLane: "implement", wantContract: ContractImplementationResult, wantReason: "explore_request", wantMutationCapable: true, wantEvidenceFirst: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "investigate then implement", family: "investigate_then_implement", wantFamily: "investigate_then_implement", wantInitialLane: "explore", wantFollowupLane: "implement", wantContract: ContractImplementationResult, wantReason: "explore_request", wantMutationCapable: true, wantEvidenceFirst: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "review then implement", family: "review_then_implement", wantFamily: "review_then_implement", wantInitialLane: "review", wantFollowupLane: "implement", wantContract: ContractImplementationResult, wantReason: "review_request", wantMutationCapable: true, wantEvidenceFirst: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "architecture", family: "architecture", wantFamily: "architecture", wantInitialLane: "architecture", wantContract: ContractArchitecture, wantReason: "architecture_request"},
		{name: "architecture then implement", family: "architecture_then_implement", wantFamily: "architecture_then_implement", wantInitialLane: "architecture", wantFollowupLane: "implement", wantContract: ContractImplementationResult, wantReason: "architecture_request", wantMutationCapable: true, wantEvidenceFirst: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "implementation", family: "implementation", wantFamily: "implementation", wantInitialLane: "implement", wantContract: ContractImplementationResult, wantReason: "direct_implementation", wantMutationCapable: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "docs edit", family: "docs_edit", wantFamily: "docs_edit", wantInitialLane: "implement", wantContract: ContractImplementationResult, wantReason: "direct_implementation", wantMutationCapable: true, wantVerificationOwner: RuntimeVerificationOwnerExecutor},
		{name: "plan execute alias", family: "plan_execute", wantFamily: string(autonomy.TaskFamilyPlanThenExecutePlanner), wantInitialLane: "implement", wantContract: ContractImplementationResult, wantReason: string(autonomy.TaskFamilyPlanThenExecutePlanner), wantMutationCapable: true, wantPlanSubmission: true, wantVerificationOwner: RuntimeVerificationOwnerPlannerOwned},
		{name: "plan then execute alias", family: "plan_then_execute", wantFamily: string(autonomy.TaskFamilyPlanThenExecutePlanner), wantInitialLane: "implement", wantContract: ContractImplementationResult, wantReason: string(autonomy.TaskFamilyPlanThenExecutePlanner), wantMutationCapable: true, wantPlanSubmission: true, wantVerificationOwner: RuntimeVerificationOwnerPlannerOwned},
		{name: "plan execute review", family: string(autonomy.TaskFamilyPlanThenExecuteReview), wantFamily: string(autonomy.TaskFamilyPlanThenExecuteReview), wantInitialLane: "review", wantContract: ContractReviewFindings, wantReason: string(autonomy.TaskFamilyPlanThenExecuteReview), wantPlanSubmission: true, wantVerificationOwner: RuntimeVerificationOwnerPlannerOwned},
		{name: "multi turn state", family: "multi_turn_state", wantFamily: "conversation", wantInitialLane: "main", wantContract: ContractDirectAnswer, wantReason: "context_followup", wantDirectAnswer: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, ok := RuntimeProfileForTaskFamily(tc.family)
			if !ok {
				t.Fatalf("RuntimeProfileForTaskFamily(%q) returned !ok", tc.family)
			}
			if profile.TaskFamily != tc.wantFamily ||
				profile.InitialLane != tc.wantInitialLane ||
				profile.FollowupLane != tc.wantFollowupLane ||
				profile.Contract != tc.wantContract ||
				profile.DefaultReason != tc.wantReason ||
				profile.MutationCapable != tc.wantMutationCapable ||
				profile.EvidenceFirstMutation != tc.wantEvidenceFirst ||
				profile.PlanSubmission != tc.wantPlanSubmission ||
				profile.DirectAnswerReadOnly != tc.wantDirectAnswer ||
				profile.VerificationOwner != tc.wantVerificationOwner {
				t.Fatalf("profile = %#v", profile)
			}
		})
	}
}

func TestRuntimeProfileRejectsUnknownTaskFamily(t *testing.T) {
	if profile, ok := RuntimeProfileForTaskFamily("unknown_family"); ok {
		t.Fatalf("unknown family returned profile: %#v", profile)
	}
}

func TestRuntimeProfileProjectsFromCanonicalContractCatalog(t *testing.T) {
	for _, family := range []string{
		ArtifactFinalAnswerTaskFamily,
		string(autonomy.TaskFamilyConversation),
		string(autonomy.TaskFamilyDirectQuestion),
		string(autonomy.TaskFamilyDebugLogs),
		"verification_only",
		"locate",
		"locate_then_implement",
		"explore_then_implement",
		"investigate_then_implement",
		"review_then_implement",
		"security_review",
		"performance_review",
		"architecture",
		"architecture_then_implement",
		"implementation",
		"test_authoring",
		"config_edit",
		"script_edit",
		"gdscript_edit",
		"docs_edit",
		"prompt_edit_security",
		"failure_injection",
		"plan_execute",
		"plan_execute_planner",
		"plan_then_execute",
		string(autonomy.TaskFamilyPlanThenExecutePlanner),
		string(autonomy.TaskFamilyPlanThenExecuteReview),
		"multi_turn_state",
	} {
		t.Run(family, func(t *testing.T) {
			profile, ok := RuntimeProfileForTaskFamily(family)
			if !ok {
				t.Fatalf("RuntimeProfileForTaskFamily(%q) returned !ok", family)
			}

			spec := RuntimeContractForTaskFamily(profile.TaskFamily)
			if profile.Contract != spec.Contract {
				t.Fatalf("profile contract = %q, catalog contract for canonical family %q = %q", profile.Contract, profile.TaskFamily, spec.Contract)
			}
		})
	}
}

func TestRuntimeProfileRouteOnlyAliasesDoNotBroadenContractLookup(t *testing.T) {
	for _, alias := range []string{"plan_execute", "plan_execute_planner", "plan_then_execute", "multi_turn_state"} {
		t.Run(alias, func(t *testing.T) {
			if _, ok := RuntimeProfileForTaskFamily(alias); !ok {
				t.Fatalf("route alias %q should remain accepted by RuntimeProfileForTaskFamily", alias)
			}

			spec := RuntimeContractForTaskFamily(alias)
			if spec.TaskFamily != autonomy.TaskFamilyConversation || spec.Contract != ContractDirectAnswer {
				t.Fatalf("RuntimeContractForTaskFamily(%q) = %#v, want conversation fallback", alias, spec)
			}
		})
	}
}
