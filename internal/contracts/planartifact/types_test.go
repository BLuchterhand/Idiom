package planartifact

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.Describe("Plan artifact vocabulary", func() {
	ginkgo.It("distinguishes explicit empty replacement text from a missing field", func() {
		var explicit PlanEdit
		gomega.Expect(json.Unmarshal([]byte(`{"path":"a.go","old_text":"old","new_text":""}`), &explicit)).To(gomega.Succeed())
		gomega.Expect(PlanEditHasExplicitReplacementText(explicit)).To(gomega.BeTrue())

		var missing PlanEdit
		gomega.Expect(json.Unmarshal([]byte(`{"path":"a.go","old_text":"old"}`), &missing)).To(gomega.Succeed())
		gomega.Expect(PlanEditHasExplicitReplacementText(missing)).To(gomega.BeFalse())
	})

	ginkgo.It("rejects a programmatic old-text-only edit", func() {
		edit := PlanEdit{Path: "a.go", OldText: "old"}
		gomega.Expect(PlanEditHasExplicitReplacementText(edit)).To(gomega.BeFalse())
		gomega.Expect(ValidatePlanEditShape(edit)).To(gomega.HaveOccurred())
	})

	ginkgo.It("marks legacy deletion edits as explicit", func() {
		edits := MarkLegacyDeletionEditsExplicit([]PlanEdit{{Path: "a.go", OldText: "delete me"}})
		gomega.Expect(edits).To(gomega.HaveLen(1))
		gomega.Expect(PlanEditHasExplicitReplacementText(edits[0])).To(gomega.BeTrue())
	})

	ginkgo.It("recognizes verification obligation statuses", func() {
		for _, status := range []string{"open", "pending", "passed", "complete", "closed", "satisfied", "failed", "skipped", "blocked", "not_applicable", "n_a"} {
			gomega.Expect(KnownVerificationStatus(status)).To(gomega.BeTrue())
		}
		gomega.Expect(KnownVerificationStatus("trust_me")).To(gomega.BeFalse())
	})

	ginkgo.It("recognizes partial and satisfied approval statuses", func() {
		for _, status := range []string{"partial", "partially_approved"} {
			gomega.Expect(IsApprovalStatusPartial(status)).To(gomega.BeTrue())
		}
		for _, status := range []string{"approved", "implicit", "not_required", "n_a", "partial"} {
			gomega.Expect(ApprovalStatusSatisfied(status)).To(gomega.BeTrue())
		}
		gomega.Expect(IsApprovalStatusPartial("implicit")).To(gomega.BeFalse())
		gomega.Expect(ApprovalStatusSatisfied("pending")).To(gomega.BeFalse())
	})

	ginkgo.It("recognizes execution permissions", func() {
		for _, permission := range []string{"execute_all", "execute_approved_scope", "blocked"} {
			gomega.Expect(KnownExecutionPermission(permission)).To(gomega.BeTrue())
		}
		gomega.Expect(KnownExecutionPermission("execute_everything")).To(gomega.BeFalse())
	})

	ginkgo.DescribeTable("recognizes and trims declared vocabulary",
		func(known func(string) bool, accepts []string, reject string) {
			for _, value := range accepts {
				gomega.Expect(known(value)).To(gomega.BeTrue())
				gomega.Expect(known(" " + value + " ")).To(gomega.BeTrue())
			}
			gomega.Expect(known(reject)).To(gomega.BeFalse())
		},
		ginkgo.Entry("client classification", KnownClientClassification, []string{"finance", "healthcare_life_sciences", "public_sector", "enterprise_saas", "tooling_vendor", "local_model_private_code", "open_source"}, "consumer"),
		ginkgo.Entry("contract kind", KnownContractKind, []string{"plan_then_execute", "implementation", "implementation_result"}, "design_doc"),
		ginkgo.Entry("owner", KnownOwner, []string{"model", "harness", "planner", "executor"}, "operator"),
		ginkgo.Entry("approval mode", KnownApprovalMode, []string{"plan_then_execute", "local", "harness_policy"}, "verbal"),
		ginkgo.Entry("approval expiration", KnownApprovalExpirationPolicy, []string{"workspace_fingerprint"}, "never"),
		ginkgo.Entry("packet status", KnownPacketStatus, []string{"not_provided", "provided", "available", "fresh", "not_used"}, "stale"),
		ginkgo.Entry("approval status", KnownApprovalStatus, []string{"implicit", "partial", "partially_approved", "approved", "auto_approved", "not_required", "pending", "awaiting_approval", "n_a"}, "maybe"),
		ginkgo.Entry("handoff target", KnownHandoffTarget, []string{"plan_execute_executor", "executor"}, "planner"),
	)

	ginkgo.It("promotes only current-state evidence into source-satisfied references", func() {
		refs := SourceSatisfiedEvidenceRefs(PlanArtifact{Evidence: []PlanArtifactEvidence{
			{Path: "a.go", Kind: EvidenceKindContextPacket},
			{Path: "b.go", Kind: EvidenceKindPlannerSubmission},
			{Path: "c.go", Kind: EvidenceKindHarnessObservedSource},
		}})
		gomega.Expect(refs).To(gomega.Equal([]plancontract.SourceSatisfiedEvidenceRef{{
			Path: "c.go",
			Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
		}}))
	})

	ginkgo.It("recognizes handoff statuses", func() {
		gomega.Expect(KnownHandoffStatus("ready")).To(gomega.BeTrue())
		gomega.Expect(KnownHandoffStatus("blocked")).To(gomega.BeTrue())
		gomega.Expect(KnownHandoffStatus("almost_ready")).To(gomega.BeFalse())
	})
})
