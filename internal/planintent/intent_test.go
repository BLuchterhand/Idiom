package planintent

import (
	"errors"
	"testing"
)

func TestValidateRequiresContractRequiredPathsExactly(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Scope.RequiredEditPaths = []string{"internal/b.go"}

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeRequiredScopeMismatch {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeRequiredScopeMismatch)
	}
}

func TestValidateRejectsCandidateOutsideContract(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Scope.CandidateEditPaths = []string{"internal/other.go"}

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeCandidateScopeRejected {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeCandidateScopeRejected)
	}
}

func TestValidateRejectsIntentOutsideContractScope(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Intents[0].Path = "internal/other.go"

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeIntentScopeRejected {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeIntentScopeRejected)
	}
}

func TestValidateAllowsTypedBlockerWithoutIntent(t *testing.T) {
	artifact := Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourceArtifactDistiller,
		Scope: Scope{
			RequiredEditPaths: []string{"internal/a.go"},
		},
		Blockers: []Blocker{{
			Code:    "needs_source_evidence",
			Message: "planner did not provide a current source body read",
			Paths:   []string{"internal/a.go"},
		}},
	}

	if err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsMixedIntentAndBlockerArtifact(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Blockers = []Blocker{{
		Code:    CodeInvalidBlocker,
		Message: "source evidence is missing",
		Paths:   []string{"internal/a.go"},
	}}

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeInvalidBlocker {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeInvalidBlocker)
	}
}

func TestValidateRejectsMalformedOperationShape(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Intents[0].OldText = ""

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeInvalidIntentShape {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeInvalidIntentShape)
	}
}

func TestValidateRejectsMalformedEvidenceRefs(t *testing.T) {
	artifact := validReplaceArtifact()
	artifact.Scope.EvidenceRefs = []string{"source:read_file\nraw body"}

	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validationErr.Code != CodeInvalidEvidenceRef {
		t.Fatalf("Code = %q, want %q", validationErr.Code, CodeInvalidEvidenceRef)
	}
}

func validReplaceArtifact() Artifact {
	return Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		TaskSummary:   "replace label",
		Scope: Scope{
			RequiredEditPaths: []string{"internal/a.go"},
		},
		Intents: []Intent{{
			Path:      "internal/a.go",
			Operation: OperationReplaceExactText,
			OldText:   "return old",
			NewText:   "return next",
			Purpose:   "replace old label",
		}},
	}
}
