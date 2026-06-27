package planartifact

import (
	"encoding/json"
	"testing"

	plancontract "idiom/internal/contracts/plan"
)

func TestPlanEditExplicitReplacementTextTracksEmptyNewText(t *testing.T) {
	var edit PlanEdit
	if err := json.Unmarshal([]byte(`{"path":"a.go","old_text":"old","new_text":""}`), &edit); err != nil {
		t.Fatalf("unmarshal plan edit: %v", err)
	}

	if !PlanEditHasExplicitReplacementText(edit) {
		t.Fatalf("explicit empty new_text should count as replacement text")
	}
}

func TestPlanEditExplicitReplacementTextRejectsMissingNewText(t *testing.T) {
	var edit PlanEdit
	if err := json.Unmarshal([]byte(`{"path":"a.go","old_text":"old"}`), &edit); err != nil {
		t.Fatalf("unmarshal plan edit: %v", err)
	}

	if PlanEditHasExplicitReplacementText(edit) {
		t.Fatalf("missing new_text should not count as replacement text")
	}
}

func TestPlanEditExplicitReplacementTextRejectsProgrammaticOldTextOnly(t *testing.T) {
	edit := PlanEdit{Path: "a.go", OldText: "old"}
	if PlanEditHasExplicitReplacementText(edit) {
		t.Fatalf("programmatic old_text-only edit should not count as explicit deletion")
	}
	if err := ValidatePlanEditShape(edit); err == nil {
		t.Fatalf("programmatic old_text-only edit should be rejected")
	}
}

func TestNewDeletionEditMarksProgrammaticDeletionExplicit(t *testing.T) {
	edit := NewDeletionEdit("a.go", "delete me", "remove obsolete text")
	if !PlanEditHasExplicitReplacementText(edit) {
		t.Fatalf("NewDeletionEdit should mark empty replacement text explicit")
	}
	if err := ValidatePlanEditShape(edit); err != nil {
		t.Fatalf("NewDeletionEdit should validate: %v", err)
	}
}

func TestMarkLegacyDeletionEditsExplicit(t *testing.T) {
	edits := MarkLegacyDeletionEditsExplicit([]PlanEdit{{
		Path:    "a.go",
		OldText: "delete me",
	}})

	if len(edits) != 1 || !PlanEditHasExplicitReplacementText(edits[0]) {
		t.Fatalf("legacy deletion edit was not marked explicit: %#v", edits)
	}
}

func TestPlanArtifactVerificationObligationVocabulary(t *testing.T) {
	for _, status := range []string{"open", "pending", "passed", "complete", "closed", "satisfied", "failed", "skipped", "blocked", "not_applicable", "n_a"} {
		if !KnownVerificationStatus(status) {
			t.Fatalf("KnownVerificationStatus(%q) = false", status)
		}
	}
	if KnownVerificationStatus("trust_me") {
		t.Fatalf("unknown verification status accepted")
	}
}

func TestPlanArtifactApprovalVocabulary(t *testing.T) {
	for _, status := range []string{"partial", "partially_approved"} {
		if !IsApprovalStatusPartial(status) {
			t.Fatalf("IsApprovalStatusPartial(%q) = false", status)
		}
	}
	for _, status := range []string{"approved", "implicit", "not_required", "n_a", "partial"} {
		if !ApprovalStatusSatisfied(status) {
			t.Fatalf("ApprovalStatusSatisfied(%q) = false", status)
		}
	}
	if IsApprovalStatusPartial("implicit") {
		t.Fatalf("implicit approval must not be partial")
	}
	if ApprovalStatusSatisfied("pending") {
		t.Fatalf("pending approval must not be satisfied")
	}
}

func TestPlanArtifactExecutionPermissionVocabulary(t *testing.T) {
	for _, permission := range []string{"execute_all", "execute_approved_scope", "blocked"} {
		if !KnownExecutionPermission(permission) {
			t.Fatalf("KnownExecutionPermission(%q) = false", permission)
		}
	}
	if KnownExecutionPermission("execute_everything") {
		t.Fatalf("unknown execution permission accepted")
	}
}

func TestSourceSatisfiedEvidenceRefsOnlyPromotesCurrentStateEvidence(t *testing.T) {
	refs := SourceSatisfiedEvidenceRefs(PlanArtifact{
		Evidence: []PlanArtifactEvidence{
			{Path: "a.go", Kind: EvidenceKindContextPacket},
			{Path: "b.go", Kind: EvidenceKindPlannerSubmission},
			{Path: "c.go", Kind: EvidenceKindHarnessObservedSource},
		},
	})

	if len(refs) != 1 || refs[0].Path != "c.go" || refs[0].Kind != plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource {
		t.Fatalf("expected only current-state evidence to be promoted, got %#v", refs)
	}
}

func TestPlanArtifactHandoffStatusVocabulary(t *testing.T) {
	for _, status := range []string{"ready", "blocked"} {
		if !KnownHandoffStatus(status) {
			t.Fatalf("KnownHandoffStatus(%q) = false", status)
		}
	}
	if KnownHandoffStatus("almost_ready") {
		t.Fatalf("unknown handoff status accepted")
	}
}
