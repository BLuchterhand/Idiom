package autonomy

import (
	"slices"
	"testing"
)

func TestCanonicalizeToolIntentNormalizesTransportRepairAndVocabulary(t *testing.T) {
	intent := CanonicalizeToolIntent(ToolIntent{
		ToolName:      " Read-File ",
		Capability:    Capability(" read "),
		SideEffect:    SideEffectClass("readonly"),
		ActionClass:   ActionClass("Inspect"),
		Transport:     ToolTransport("tool_call"),
		ParserFailure: ParserFailureClass("json"),
		RepairSource:  RepairSource("normalizer"),
		TargetRefs:    []string{" internal/autonomy/action_intent.go ", "internal/autonomy/action_intent.go", "docs/design.md"},
		PathRoles: []PathRoleMatch{
			{Path: " docs/design.md ", Role: PathRoleEvidenceReadonly, MatchedPattern: "docs/**"},
			{Path: "docs/design.md", Role: PathRoleEvidenceReadonly, MatchedPattern: "docs/**"},
		},
		ArgumentsDigest: " abc123 ",
		Source:          " Runner Tool Intent ",
	})

	if intent.ToolName != "read_file" {
		t.Fatalf("ToolName = %q", intent.ToolName)
	}
	if intent.Capability != CapabilityBoundedRead {
		t.Fatalf("Capability = %q", intent.Capability)
	}
	if intent.SideEffect != SideEffectReadOnly {
		t.Fatalf("SideEffect = %q", intent.SideEffect)
	}
	if intent.ActionClass != ActionClassRead {
		t.Fatalf("ActionClass = %q", intent.ActionClass)
	}
	if intent.Transport != ToolTransportNativeCall {
		t.Fatalf("Transport = %q", intent.Transport)
	}
	if intent.ParserFailure != ParserFailureMalformedJSON {
		t.Fatalf("ParserFailure = %q", intent.ParserFailure)
	}
	if intent.RepairSource != RepairSourceHostNormalizer || !intent.Repaired {
		t.Fatalf("repair = %q repaired=%v", intent.RepairSource, intent.Repaired)
	}
	if !slices.Equal(intent.TargetRefs, []string{"docs/design.md", "internal/autonomy/action_intent.go"}) {
		t.Fatalf("TargetRefs = %#v", intent.TargetRefs)
	}
	if len(intent.PathRoles) != 1 || intent.PathRoles[0].Path != "docs/design.md" {
		t.Fatalf("PathRoles = %#v", intent.PathRoles)
	}
	if intent.ArgumentsDigest != "abc123" || intent.Source != "runner_tool_intent" {
		t.Fatalf("digest/source = %q/%q", intent.ArgumentsDigest, intent.Source)
	}
	if err := intent.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestToolIntentValidationRejectsParserFailureWithoutKnownTransport(t *testing.T) {
	intent := ToolIntent{
		ActionClass:   ActionClassRead,
		Transport:     ToolTransportUnknown,
		ParserFailure: ParserFailureMalformedJSON,
	}

	if err := intent.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want parser failure transport error")
	}
}

func TestToolIntentValidationRequiresRepairSourceWhenMarkedRepaired(t *testing.T) {
	intent := ToolIntent{
		ActionClass: ActionClassRead,
		Transport:   ToolTransportNativeCall,
		Repaired:    true,
	}

	if err := intent.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing repair source error")
	}
}

func TestActionFromIntentBuildsStableNormalizedAction(t *testing.T) {
	action := ActionFromIntent(ToolIntent{
		ToolName:    "code_navigate",
		Capability:  Capability("bounded_read"),
		SideEffect:  SideEffectReadOnly,
		ActionClass: ActionClass("read"),
		TargetRefs:  []string{"internal/autonomy/action_intent.go", "docs/ignored.md"},
		Source:      "runner",
	})

	if action.ActionClass != ActionClassRead {
		t.Fatalf("ActionClass = %q", action.ActionClass)
	}
	if action.ActionKey != "read:code_navigate:bounded_read:internal/autonomy/action_intent.go" {
		t.Fatalf("ActionKey = %q", action.ActionKey)
	}
	if action.TargetRef != "internal/autonomy/action_intent.go" {
		t.Fatalf("TargetRef = %q", action.TargetRef)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCanonicalizeNormalizedActionPreservesExplicitActionKey(t *testing.T) {
	action := CanonicalizeNormalizedAction(NormalizedAction{
		ActionClass: ActionClass("shell_verification"),
		ActionKey:   "  GO TEST ./INTERNAL/AUTONOMY   ",
		ToolName:    " shell ",
		Capability:  Capability("verify"),
		SideEffect:  SideEffectClass("read"),
		TargetRef:   "go test ./internal/autonomy",
	})

	if action.ActionClass != ActionClassVerify {
		t.Fatalf("ActionClass = %q", action.ActionClass)
	}
	if action.ActionKey != "go test ./internal/autonomy" {
		t.Fatalf("ActionKey = %q", action.ActionKey)
	}
	if action.Capability != CapabilityShellVerification {
		t.Fatalf("Capability = %q", action.Capability)
	}
	if action.SideEffect != SideEffectReadOnly {
		t.Fatalf("SideEffect = %q", action.SideEffect)
	}
}

func TestActionDecisionValidationUsesPolicyVocabulary(t *testing.T) {
	decision := CanonicalizeActionDecision(ActionDecision{
		Decision:         ToolPolicyDecision(" ask "),
		ActionClass:      ActionClass("write"),
		Capability:       Capability("patch"),
		SideEffect:       SideEffectClass("mutating"),
		InvariantID:      " Path.Required-Edit ",
		ReasonCode:       " Needs Approval ",
		ApprovalRequired: true,
		Source:           " Write Gate ",
	})

	if decision.Decision != PolicyAsk {
		t.Fatalf("Decision = %q", decision.Decision)
	}
	if decision.ActionClass != ActionClassPerEditWrite {
		t.Fatalf("ActionClass = %q", decision.ActionClass)
	}
	if decision.Capability != CapabilityApplyPatch {
		t.Fatalf("Capability = %q", decision.Capability)
	}
	if decision.SideEffect != SideEffectAdditive {
		t.Fatalf("SideEffect = %q", decision.SideEffect)
	}
	if decision.InvariantID != "path.required.edit" || decision.ReasonCode != "needs.approval" {
		t.Fatalf("invariant/reason = %q/%q", decision.InvariantID, decision.ReasonCode)
	}
	if decision.Source != "write_gate" {
		t.Fatalf("Source = %q", decision.Source)
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestActionDecisionValidationRejectsApprovalRequiredWithoutAsk(t *testing.T) {
	decision := ActionDecision{
		Decision:         PolicyDeny,
		ActionClass:      ActionClassPerEditWrite,
		ApprovalRequired: true,
	}

	if err := decision.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want approval decision error")
	}
}

func TestVocabularyPredicatesRejectUnknownValues(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{name: "action", ok: ValidActionClass(ActionClass("invented"))},
		{name: "transport", ok: ValidToolTransport(ToolTransport("invented"))},
		{name: "parser", ok: ValidParserFailureClass(ParserFailureClass("invented"))},
		{name: "repair", ok: ValidRepairSource(RepairSource("invented"))},
		{name: "capability", ok: ValidCapability(Capability("invented"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ok {
				t.Fatal("predicate accepted invented value")
			}
		})
	}
}
