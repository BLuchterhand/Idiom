package planartifact

import (
	"strings"
	"testing"
)

func TestNormalizeForSubmitAlignsHandoffReadiness(t *testing.T) {
	executable := validPlanArtifactForValidation()
	executable.Handoff = PlanArtifactHandoff{ID: "handoff-1", Status: HandoffStatusReady, Ready: false}

	normalized := NormalizeForSubmit(executable, NormalizeForSubmitOptions{PlanID: "plan-1", Created: "2026-06-26T00:00:00Z"})
	if normalized.Handoff.Status != HandoffStatusReady || !normalized.Handoff.Ready || len(normalized.Handoff.Blockers) != 0 {
		t.Fatalf("executable handoff normalized to %#v, want ready=true status=ready without blockers", normalized.Handoff)
	}

	blocked := validPlanArtifactForValidation()
	blocked.Approval.ExecutionPermission = ExecutionPermissionBlocked
	blocked.Handoff = PlanArtifactHandoff{ID: "handoff-2", Status: HandoffStatusReady, Ready: false}

	normalized = NormalizeForSubmit(blocked, NormalizeForSubmitOptions{PlanID: "plan-2", Created: "2026-06-26T00:00:00Z"})
	if normalized.Handoff.Status != HandoffStatusBlocked || normalized.Handoff.Ready || len(normalized.Handoff.Blockers) == 0 {
		t.Fatalf("blocked handoff normalized to %#v, want ready=false status=blocked with blockers", normalized.Handoff)
	}
}

func TestValidatePlanArtifactRejectsDivergentHandoffReadiness(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*PlanArtifact)
		wantErr string
	}{
		{
			name: "status ready with false ready flag",
			mutate: func(artifact *PlanArtifact) {
				artifact.Handoff.Status = HandoffStatusReady
				artifact.Handoff.Ready = false
			},
			wantErr: "handoff.ready:invalid_ready_flag",
		},
		{
			name: "status ready with blocked execution permission",
			mutate: func(artifact *PlanArtifact) {
				artifact.Approval.ExecutionPermission = ExecutionPermissionBlocked
				artifact.Handoff.Status = HandoffStatusReady
				artifact.Handoff.Ready = true
			},
			wantErr: "handoff.status:not_executable",
		},
		{
			name: "blocked without blocker",
			mutate: func(artifact *PlanArtifact) {
				artifact.Handoff.Status = HandoffStatusBlocked
				artifact.Handoff.Ready = false
				artifact.Handoff.Blockers = nil
			},
			wantErr: "handoff.blockers:required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			artifact := validPlanArtifactForValidation()
			tc.mutate(&artifact)

			err := ValidatePlanArtifact(artifact)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidatePlanArtifact error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestClassifyPlanEditShapeAcceptsSingleExecutableShapes(t *testing.T) {
	tests := []struct {
		name  string
		edit  PlanEdit
		shape PlanEditShape
	}{
		{
			name:  "substring",
			edit:  PlanEdit{Path: "a.go", OldText: "old", NewText: "new"},
			shape: PlanEditShapeSubstring,
		},
		{
			name:  "substring replace all",
			edit:  PlanEdit{Path: "a.go", OldText: "old", NewText: "new", ReplaceAll: true},
			shape: PlanEditShapeSubstring,
		},
		{
			name:  "line range",
			edit:  PlanEdit{Path: "a.go", StartLine: 2, EndLine: 4, Content: "replacement\n"},
			shape: PlanEditShapeLineRange,
		},
		{
			name:  "line range ref",
			edit:  PlanEdit{Path: "a.go", StartLine: 2, EndLine: 4, ContentRef: "assets/range.txt"},
			shape: PlanEditShapeLineRange,
		},
		{
			name:  "insert after line",
			edit:  PlanEdit{Path: "a.go", InsertAfterLine: 2, Content: "inserted\n"},
			shape: PlanEditShapeInsertAfterLine,
		},
		{
			name:  "missing or new file",
			edit:  PlanEdit{Path: "a.go", NewText: "package a\n", ReplaceAll: true},
			shape: PlanEditShapeReplaceAll,
		},
		{
			name:  "missing or new file ref",
			edit:  PlanEdit{Path: "a.go", NewTextRef: "assets/body.txt", ReplaceAll: true},
			shape: PlanEditShapeReplaceAll,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyPlanEditShape(tc.edit)
			if err != nil {
				t.Fatalf("ClassifyPlanEditShape returned error: %v", err)
			}
			if got != tc.shape {
				t.Fatalf("shape = %q, want %q", got, tc.shape)
			}
		})
	}
}

func TestValidatePlanEditShapeRejectsMixedExecutableShapes(t *testing.T) {
	tests := []struct {
		name string
		edit PlanEdit
		want string
	}{
		{
			name: "substring plus line range",
			edit: PlanEdit{Path: "a.go", OldText: "old", NewText: "new", StartLine: 1, EndLine: 1, Content: "replacement\n"},
			want: "mutually exclusive",
		},
		{
			name: "substring plus content without range",
			edit: PlanEdit{Path: "a.go", OldText: "old", NewText: "new", Content: "replacement\n"},
			want: "substring edits are mutually exclusive",
		},
		{
			name: "line range plus new text",
			edit: PlanEdit{Path: "a.go", StartLine: 1, EndLine: 1, Content: "replacement\n", NewText: "new"},
			want: "line-range edits are mutually exclusive",
		},
		{
			name: "line range plus replace all",
			edit: PlanEdit{Path: "a.go", StartLine: 1, EndLine: 1, Content: "replacement\n", NewText: "new", ReplaceAll: true},
			want: "mutually exclusive",
		},
		{
			name: "insert plus ref",
			edit: PlanEdit{Path: "a.go", InsertAfterLine: 1, Content: "inserted\n", ContentRef: "assets/insert.txt"},
			want: "insert_after_line edits do not support content_ref",
		},
		{
			name: "insert plus new text",
			edit: PlanEdit{Path: "a.go", InsertAfterLine: 1, Content: "inserted\n", NewText: "new"},
			want: "insert_after_line edits are mutually exclusive",
		},
		{
			name: "whole file plus content",
			edit: PlanEdit{Path: "a.go", NewText: "package a\n", ReplaceAll: true, Content: "replacement\n"},
			want: "replace_all edits with empty old_text are mutually exclusive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePlanEditShape(tc.edit)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidatePlanEditShape error = %v, want %q", err, tc.want)
			}
		})
	}
}

func validPlanArtifactForValidation() PlanArtifact {
	return PlanArtifact{
		SchemaVersion: SchemaVersion,
		PlanID:        "plan-valid",
		Created:       "2026-06-26T00:00:00Z",
		Task:          PlanArtifactTask{ID: "task-valid"},
		Contract: PlanArtifactContract{
			Kind:              ContractKindPlanThenExecute,
			StrategyOwner:     OwnerModel,
			ContractOwner:     OwnerHarness,
			PermissionProfile: PermissionProfilePlanThenExecute,
			VerificationOwner: OwnerExecutor,
		},
		Packet:   PlanArtifactPacket{Status: PacketStatusNotProvided},
		Paths:    PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
		Evidence: []PlanArtifactEvidence{{Kind: EvidenceKindPlannerSubmission, Summary: "bounded test evidence"}},
		VerificationObligations: []PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     VerificationKindExecutorLaneSanity,
			Owner:    OwnerExecutor,
			Status:   VerificationStatusOpen,
			Required: true,
		}},
		Approval: PlanArtifactApproval{
			Required:            false,
			Status:              ApprovalStatusImplicit,
			ScopeID:             "scope-valid",
			ExecutionPermission: ExecutionPermissionExecuteAll,
		},
		Handoff: PlanArtifactHandoff{
			ID:     "handoff-valid",
			From:   OwnerPlanner,
			To:     HandoffTargetPlanExecuteExecutor,
			Status: HandoffStatusReady,
			Ready:  true,
		},
		Edits: []PlanEdit{{
			Path:    "a.txt",
			OldText: "old",
			NewText: "new",
			Purpose: "Update fixture.",
		}},
	}
}
