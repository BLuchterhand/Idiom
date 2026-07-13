package workerruntime

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
)

var _ = ginkgo.Describe("runtime profile route semantics", func() {
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
		tc := tc
		ginkgo.It(tc.name, func() {
			profile, ok := RuntimeProfileForTaskFamily(tc.family)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("RuntimeProfileForTaskFamily(%q) returned !ok", tc.family))
			gomega.Expect(profile.TaskFamily).To(gomega.Equal(tc.wantFamily), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.InitialLane).To(gomega.Equal(tc.wantInitialLane), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.FollowupLane).To(gomega.Equal(tc.wantFollowupLane), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.Contract).To(gomega.Equal(tc.wantContract), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.DefaultReason).To(gomega.Equal(tc.wantReason), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.MutationCapable).To(gomega.Equal(tc.wantMutationCapable), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.EvidenceFirstMutation).To(gomega.Equal(tc.wantEvidenceFirst), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.PlanSubmission).To(gomega.Equal(tc.wantPlanSubmission), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.DirectAnswerReadOnly).To(gomega.Equal(tc.wantDirectAnswer), fmt.Sprintf("profile = %#v", profile))
			gomega.Expect(profile.VerificationOwner).To(gomega.Equal(tc.wantVerificationOwner), fmt.Sprintf("profile = %#v", profile))
		})
	}
})

var _ = ginkgo.It("runtime profile rejects unknown task family", func() {
	if profile, ok := RuntimeProfileForTaskFamily("unknown_family"); ok {
		ginkgo.Fail(fmt.Sprintf("unknown family returned profile: %#v", profile))
	}
})

var _ = ginkgo.Describe("runtime profile projection from the canonical contract catalog", func() {
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
		family := family
		ginkgo.It(family, func() {
			profile, ok := RuntimeProfileForTaskFamily(family)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("RuntimeProfileForTaskFamily(%q) returned !ok", family))

			spec := RuntimeContractForTaskFamily(profile.TaskFamily)
			gomega.Expect(profile.Contract != spec.Contract).To(gomega.BeFalse(), fmt.Sprintf("profile contract = %q, catalog contract for canonical family %q = %q", profile.Contract, profile.TaskFamily, spec.Contract))
		})
	}
})

var _ = ginkgo.Describe("route-only runtime profile aliases", func() {
	for _, alias := range []string{"plan_execute", "plan_execute_planner", "plan_then_execute", "multi_turn_state"} {
		alias := alias
		ginkgo.It(alias+" does not broaden contract lookup", func() {
			if _, ok := RuntimeProfileForTaskFamily(alias); !ok {
				ginkgo.Fail(fmt.Sprintf("route alias %q should remain accepted by RuntimeProfileForTaskFamily", alias))
			}

			spec := RuntimeContractForTaskFamily(alias)
			gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyConversation), fmt.Sprintf("RuntimeContractForTaskFamily(%q) = %#v, want conversation fallback", alias, spec))
			gomega.Expect(spec.Contract).To(gomega.Equal(ContractDirectAnswer), fmt.Sprintf("RuntimeContractForTaskFamily(%q) = %#v, want conversation fallback", alias, spec))
		})
	}
})
