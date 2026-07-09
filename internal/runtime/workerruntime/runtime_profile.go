package workerruntime

import "strings"

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
	spec, ok := runtimeContractSpecForRouteFamily(family)
	if !ok || strings.TrimSpace(spec.route.InitialLane) == "" {
		return RuntimeProfile{}, false
	}

	return RuntimeProfile{
		TaskFamily:            strings.TrimSpace(string(spec.TaskFamily)),
		InitialLane:           strings.TrimSpace(spec.route.InitialLane),
		FollowupLane:          strings.TrimSpace(spec.route.FollowupLane),
		Contract:              spec.Contract,
		DefaultReason:         strings.TrimSpace(spec.route.DefaultReason),
		MutationCapable:       spec.route.MutationCapable,
		EvidenceFirstMutation: spec.route.EvidenceFirstMutation,
		PlanSubmission:        spec.route.PlanSubmission,
		DirectAnswerReadOnly:  spec.route.DirectAnswerReadOnly,
		VerificationOwner:     spec.route.VerificationOwner,
	}, true
}

func TaskFamilyMutationCapable(family string) bool {
	profile, ok := RuntimeProfileForTaskFamily(family)
	return ok && profile.MutationCapable
}

func TaskFamilyEvidenceFirstMutation(family string) bool {
	profile, ok := RuntimeProfileForTaskFamily(family)
	return ok && profile.EvidenceFirstMutation
}
