package autonomy

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tool action intent", func() {
	It("normalizes transport repair metadata and vocabulary", func() {
		intent := CanonicalizeToolIntent(ToolIntent{
			ToolName:      " Read-File ",
			Capability:    Capability(" read "),
			SideEffect:    SideEffectClass("readonly"),
			ActionClass:   ActionClass("Inspect"),
			Transport:     ToolTransport("tool_call"),
			ParserFailure: ParserFailureClass("json"),
			RepairSource:  RepairSource("normalizer"),
			TargetRefs:    []string{" internal/contracts/autonomy/action_intent.go ", "internal/contracts/autonomy/action_intent.go", "docs/design.md"},
			PathRoles: []PathRoleMatch{
				{Path: " docs/design.md ", Role: PathRoleEvidenceReadonly, MatchedPattern: "docs/**"},
				{Path: "docs/design.md", Role: PathRoleEvidenceReadonly, MatchedPattern: "docs/**"},
			},
			ArgumentsDigest: " abc123 ",
			Source:          " Runner Tool Intent ",
		})

		Expect(intent.ToolName).To(Equal("read_file"))
		Expect(intent.Capability).To(Equal(CapabilityBoundedRead))
		Expect(intent.SideEffect).To(Equal(SideEffectReadOnly))
		Expect(intent.ActionClass).To(Equal(ActionClassRead))
		Expect(intent.Transport).To(Equal(ToolTransportNativeCall))
		Expect(intent.ParserFailure).To(Equal(ParserFailureMalformedJSON))
		Expect(intent.RepairSource).To(Equal(RepairSourceHostNormalizer))
		Expect(intent.Repaired).To(BeTrue())
		Expect(intent.TargetRefs).To(Equal([]string{"docs/design.md", "internal/contracts/autonomy/action_intent.go"}))
		Expect(intent.PathRoles).To(HaveLen(1))
		Expect(intent.PathRoles[0].Path).To(Equal("docs/design.md"))
		Expect(intent.ArgumentsDigest).To(Equal("abc123"))
		Expect(intent.Source).To(Equal("runner_tool_intent"))
	})

	It("canonicalizes transport mismatch parser failures", func() {
		intent := ToolIntent{
			ActionClass:   ActionClassRead,
			Transport:     ToolTransportNativeCall,
			ParserFailure: ParserFailureClass("native_tool_mismatch"),
		}
		Expect(CanonicalizeToolIntent(intent).ParserFailure).To(Equal(ParserFailureTransportMismatch))
	})

	It("uses canonical action-class tokens", func() {
		Expect(ActionClassToken(ActionClass("decision_pass"))).To(Equal("review_pass"))
		Expect(ActionClassToken(ActionClass("source-satisfaction"))).To(Equal("source_satisfied"))
		Expect(ActionClassToken(ActionClass(" "))).To(BeEmpty())
	})

	It("separates tool-backed action classes from terminal classes", func() {
		for _, class := range []ActionClass{
			ActionClassRead,
			ActionClassSearch,
			ActionClassSubmitPlan,
			ActionClassPerEditWrite,
			ActionClassDeliverableWrite,
			ActionClassVerify,
			ActionClassGenericProbe,
			ActionClassDelegate,
		} {
			Expect(IsToolActionClass(class)).To(BeTrue(), "class %q", class)
		}

		for _, class := range []ActionClass{
			ActionClassSourceSatisfied,
			ActionClassBlocker,
			ActionClassReviewPass,
			ActionClassFinalAnswer,
			ActionClassProseFinal,
		} {
			Expect(IsToolActionClass(class)).To(BeFalse(), "class %q", class)
		}
	})

	It("builds a stable normalized action from an intent", func() {
		action := ActionFromIntent(ToolIntent{
			ToolName:    "code_navigate",
			Capability:  Capability("bounded_read"),
			SideEffect:  SideEffectReadOnly,
			ActionClass: ActionClass("read"),
			TargetRefs:  []string{"internal/contracts/autonomy/action_intent.go", "docs/ignored.md"},
			Source:      "runner",
		})
		Expect(action.ActionClass).To(Equal(ActionClassRead))
		Expect(action.ActionKey).To(Equal("read:code_navigate:bounded_read:internal/contracts/autonomy/action_intent.go"))
		Expect(action.TargetRef).To(Equal("internal/contracts/autonomy/action_intent.go"))
	})

	It("rejects unknown vocabulary values", func() {
		Expect(ValidActionClass(ActionClass("invented"))).To(BeFalse())
	})
})
