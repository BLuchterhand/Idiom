package workerruntime

import (
	"strings"

	"idiom/internal/contracts/autonomy"
)

type RuntimeVerificationOwner string

const (
	RuntimeVerificationOwnerExecutor     RuntimeVerificationOwner = "executor"
	RuntimeVerificationOwnerPlannerOwned RuntimeVerificationOwner = "planner_owned"
)

type RuntimeProfile struct {
	TaskFamily            string
	InitialLane           string
	FollowupLane          string
	Contract              RuntimeContractID
	DefaultReason         string
	MutationCapable       bool
	EvidenceFirstMutation bool
	PlanSubmission        bool
	DirectAnswerReadOnly  bool
	VerificationOwner     RuntimeVerificationOwner
}

func RuntimeProfileForTaskFamily(family string) (RuntimeProfile, bool) {
	switch strings.TrimSpace(family) {
	case ArtifactFinalAnswerTaskFamily:
		return runtimeProfile(ArtifactFinalAnswerTaskFamily, "main", "", ContractArtifactFinalAnswer, "artifact_final_answer", false), true
	case string(autonomy.TaskFamilyDirectQuestion), string(autonomy.TaskFamilyConversation):
		profile := runtimeProfile(string(autonomy.TaskFamilyConversation), "main", "", ContractDirectAnswer, "conversation", false)
		profile.DirectAnswerReadOnly = true
		return profile, true
	case string(autonomy.TaskFamilyDebugLogs):
		profile := runtimeProfile(string(autonomy.TaskFamilyConversation), "main", "", ContractDirectAnswer, "legacy_debug_logs_collapsed_to_conversation", false)
		profile.DirectAnswerReadOnly = true
		return profile, true
	case "verification_only":
		profile := runtimeProfile("verification_only", "main", "", ContractDirectAnswer, "verification_request", false)
		profile.DirectAnswerReadOnly = true
		return profile, true
	case "locate":
		return runtimeProfile("locate", "locate", "", ContractLocateTargets, "locate_request", false), true
	case "locate_then_implement":
		return runtimeProfile("locate_then_implement", "locate", "implement", ContractImplementationResult, "locate_request", true), true
	case "explore_then_implement", "investigate_then_implement":
		profile := runtimeProfile(strings.TrimSpace(family), "explore", "implement", ContractImplementationResult, "explore_request", true)
		profile.EvidenceFirstMutation = true
		return profile, true
	case "review_then_implement":
		profile := runtimeProfile("review_then_implement", "review", "implement", ContractImplementationResult, "review_request", true)
		profile.EvidenceFirstMutation = true
		return profile, true
	case "security_review":
		return runtimeProfile("security_review", "review", "", ContractReviewFindings, "security_review", false), true
	case "performance_review":
		return runtimeProfile("performance_review", "review", "", ContractReviewFindings, "performance_review", false), true
	case "architecture":
		return runtimeProfile("architecture", "architecture", "", ContractArchitecture, "architecture_request", false), true
	case "architecture_then_implement":
		profile := runtimeProfile("architecture_then_implement", "architecture", "implement", ContractImplementationResult, "architecture_request", true)
		profile.EvidenceFirstMutation = true
		return profile, true
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		return runtimeProfile(strings.TrimSpace(family), "implement", "", ContractImplementationResult, "direct_implementation", true), true
	case "plan_execute", "plan_execute_planner", "plan_then_execute", string(autonomy.TaskFamilyPlanThenExecutePlanner):
		profile := runtimeProfile(string(autonomy.TaskFamilyPlanThenExecutePlanner), "implement", "", ContractImplementationResult, string(autonomy.TaskFamilyPlanThenExecutePlanner), true)
		profile.PlanSubmission = true
		profile.VerificationOwner = RuntimeVerificationOwnerPlannerOwned
		return profile, true
	case string(autonomy.TaskFamilyPlanThenExecuteReview):
		profile := runtimeProfile(string(autonomy.TaskFamilyPlanThenExecuteReview), "review", "", ContractReviewFindings, string(autonomy.TaskFamilyPlanThenExecuteReview), false)
		profile.PlanSubmission = true
		profile.VerificationOwner = RuntimeVerificationOwnerPlannerOwned
		return profile, true
	case "multi_turn_state":
		profile := runtimeProfile(string(autonomy.TaskFamilyConversation), "main", "", ContractDirectAnswer, "context_followup", false)
		profile.DirectAnswerReadOnly = true
		return profile, true
	default:
		return RuntimeProfile{}, false
	}
}

func runtimeProfile(family, initialLane, followupLane string, contract RuntimeContractID, reason string, mutationCapable bool) RuntimeProfile {
	profile := RuntimeProfile{
		TaskFamily:      strings.TrimSpace(family),
		InitialLane:     strings.TrimSpace(initialLane),
		FollowupLane:    strings.TrimSpace(followupLane),
		Contract:        contract,
		DefaultReason:   strings.TrimSpace(reason),
		MutationCapable: mutationCapable,
	}
	if mutationCapable {
		profile.VerificationOwner = RuntimeVerificationOwnerExecutor
	}
	return profile
}

func TaskFamilyMutationCapable(family string) bool {
	profile, ok := RuntimeProfileForTaskFamily(family)
	return ok && profile.MutationCapable
}

func TaskFamilyEvidenceFirstMutation(family string) bool {
	profile, ok := RuntimeProfileForTaskFamily(family)
	return ok && profile.EvidenceFirstMutation
}
