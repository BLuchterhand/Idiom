package planexecute

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	planartifact "idiom/internal/contracts/planartifact"
)

func TestApprovePlanForExecutionAppliesArtifactApproval(t *testing.T) {
	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "a.go", OldText: "old", NewText: "new"}},
	}

	got, err := approvePlanForExecution(context.Background(), "plan.json", plan, PlanApprovalPhaseInitial, Options{
		OnPlanApproval: func(_ context.Context, req PlanApprovalRequest) PlanApprovalDecision {
			if req.Phase != PlanApprovalPhaseInitial {
				t.Fatalf("phase = %q, want initial", req.Phase)
			}
			if len(req.Paths) != 1 || req.Paths[0] != "a.go" {
				t.Fatalf("paths = %#v, want a.go", req.Paths)
			}

			return PlanApprovalDecision{Approved: true}
		},
	}, []string{"a.go"})
	if err != nil {
		t.Fatalf("approvePlanForExecution: %v", err)
	}

	if !got.Approval.Required {
		t.Fatal("approval should be required after explicit TUI approval")
	}
	if got.Approval.Status != planartifact.ApprovalStatusApproved {
		t.Fatalf("status = %q, want approved", got.Approval.Status)
	}
	if got.Approval.ExecutionPermission != planartifact.ExecutionPermissionExecuteAll {
		t.Fatalf("permission = %q, want execute_all", got.Approval.ExecutionPermission)
	}
	if len(got.Approval.ApprovedPaths) != 1 || got.Approval.ApprovedPaths[0] != "a.go" {
		t.Fatalf("approved paths = %#v, want a.go", got.Approval.ApprovedPaths)
	}
}

func TestApprovedPlanAuthorizedNewFilesAddsOnlyApprovedMissingPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("seed existing.go: %v", err)
	}

	plan := planartifact.PlanArtifact{
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              planartifact.ApprovalStatusApproved,
			ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
			ApprovedPaths:       []string{"existing.go", "new.go", "bad_shape.go"},
		},
		Edits: []planartifact.PlanEdit{
			{Path: "existing.go", OldText: "package app\n", NewText: "package app\n\nconst Existing = true\n"},
			{Path: "new.go", ReplaceAll: true, NewText: "package app\n"},
			{Path: "bad_shape.go", NewText: "package app\n"},
			{Path: "unapproved.go", ReplaceAll: true, NewText: "package app\n"},
		},
	}

	got := approvedPlanAuthorizedNewFiles(root, []string{"already_authorized.go"}, plan)
	want := []string{"already_authorized.go", "new.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("authorized new files = %#v, want %#v", got, want)
	}
}

func TestApprovedPlanAuthorizedNewFilesRequiresSatisfiedApproval(t *testing.T) {
	root := t.TempDir()
	plan := planartifact.PlanArtifact{
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              planartifact.ApprovalStatusPending,
			ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
			ApprovedPaths:       []string{"new.go"},
		},
		Edits: []planartifact.PlanEdit{{Path: "new.go", ReplaceAll: true, NewText: "package app\n"}},
	}

	got := approvedPlanAuthorizedNewFiles(root, []string{"already_authorized.go"}, plan)
	want := []string{"already_authorized.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("authorized new files = %#v, want %#v", got, want)
	}
}

func TestApprovePlanForExecutionRejectsDeniedDecision(t *testing.T) {
	_, err := approvePlanForExecution(context.Background(), "plan.json", planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "a.go", OldText: "old", NewText: "new"}},
	}, PlanApprovalPhaseInitial, Options{
		OnPlanApproval: func(context.Context, PlanApprovalRequest) PlanApprovalDecision {
			return PlanApprovalDecision{Reason: "operator denied"}
		},
	}, []string{"a.go"})

	if err == nil || !strings.Contains(err.Error(), "operator denied") {
		t.Fatalf("denial error = %v, want operator denied", err)
	}
}

func TestApprovePlanForExecutionRejectsPartialDecision(t *testing.T) {
	_, err := approvePlanForExecution(context.Background(), "plan.json", planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", OldText: "old", NewText: "new"},
			{Path: "b.go", OldText: "old", NewText: "new"},
		},
	}, PlanApprovalPhaseInitial, Options{
		OnPlanApproval: func(context.Context, PlanApprovalRequest) PlanApprovalDecision {
			return PlanApprovalDecision{Approved: true, ApprovedPaths: []string{"a.go"}}
		},
	}, []string{"a.go", "b.go"})

	if err == nil || !strings.Contains(err.Error(), "partial plan edit approval is not supported") {
		t.Fatalf("partial approval error = %v", err)
	}
}
