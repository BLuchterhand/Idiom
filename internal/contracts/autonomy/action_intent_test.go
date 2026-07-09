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
		TargetRefs:    []string{" internal/contracts/autonomy/action_intent.go ", "internal/contracts/autonomy/action_intent.go", "docs/design.md"},
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

	if !slices.Equal(intent.TargetRefs, []string{"docs/design.md", "internal/contracts/autonomy/action_intent.go"}) {
		t.Fatalf("TargetRefs = %#v", intent.TargetRefs)
	}

	if len(intent.PathRoles) != 1 || intent.PathRoles[0].Path != "docs/design.md" {
		t.Fatalf("PathRoles = %#v", intent.PathRoles)
	}

	if intent.ArgumentsDigest != "abc123" || intent.Source != "runner_tool_intent" {
		t.Fatalf("digest/source = %q/%q", intent.ArgumentsDigest, intent.Source)
	}
}

func TestCanonicalizeToolIntentAcceptsTransportMismatch(t *testing.T) {
	intent := ToolIntent{
		ActionClass:   ActionClassRead,
		Transport:     ToolTransportNativeCall,
		ParserFailure: ParserFailureClass("native_tool_mismatch"),
	}

	got := CanonicalizeToolIntent(intent)
	if got.ParserFailure != ParserFailureTransportMismatch {
		t.Fatalf("ParserFailure = %q, want %q", got.ParserFailure, ParserFailureTransportMismatch)
	}
}

func TestActionClassTokenUsesCanonicalVocabulary(t *testing.T) {
	if ActionClassToken(ActionClass("decision_pass")) != "review_pass" {
		t.Fatalf("ActionClassToken(decision_pass) = %q", ActionClassToken(ActionClass("decision_pass")))
	}

	if ActionClassToken(ActionClass("source-satisfaction")) != "source_satisfied" {
		t.Fatalf("ActionClassToken(source-satisfaction) = %q", ActionClassToken(ActionClass("source-satisfaction")))
	}

	if ActionClassToken(ActionClass(" ")) != "" {
		t.Fatalf("ActionClassToken(blank) = %q, want empty", ActionClassToken(ActionClass(" ")))
	}
}

func TestIsToolActionClassSeparatesToolBackedAndTerminalClasses(t *testing.T) {
	toolBacked := []ActionClass{
		ActionClassRead,
		ActionClassSearch,
		ActionClassSubmitPlan,
		ActionClassPerEditWrite,
		ActionClassDeliverableWrite,
		ActionClassVerify,
		ActionClassGenericProbe,
		ActionClassDelegate,
	}
	for _, class := range toolBacked {
		if !IsToolActionClass(class) {
			t.Fatalf("IsToolActionClass(%q) = false, want true", class)
		}
	}

	for _, class := range []ActionClass{ActionClassSourceSatisfied, ActionClassBlocker, ActionClassReviewPass, ActionClassFinalAnswer, ActionClassProseFinal} {
		if IsToolActionClass(class) {
			t.Fatalf("IsToolActionClass(%q) = true, want false", class)
		}
	}
}

func TestActionFromIntentBuildsStableNormalizedAction(t *testing.T) {
	action := ActionFromIntent(ToolIntent{
		ToolName:    "code_navigate",
		Capability:  Capability("bounded_read"),
		SideEffect:  SideEffectReadOnly,
		ActionClass: ActionClass("read"),
		TargetRefs:  []string{"internal/contracts/autonomy/action_intent.go", "docs/ignored.md"},
		Source:      "runner",
	})

	if action.ActionClass != ActionClassRead {
		t.Fatalf("ActionClass = %q", action.ActionClass)
	}

	if action.ActionKey != "read:code_navigate:bounded_read:internal/contracts/autonomy/action_intent.go" {
		t.Fatalf("ActionKey = %q", action.ActionKey)
	}

	if action.TargetRef != "internal/contracts/autonomy/action_intent.go" {
		t.Fatalf("TargetRef = %q", action.TargetRef)
	}
}

func TestVocabularyPredicatesRejectUnknownValues(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{name: "action", ok: ValidActionClass(ActionClass("invented"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ok {
				t.Fatal("predicate accepted invented value")
			}
		})
	}
}
