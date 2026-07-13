package planexecute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
)

var _ = ginkgo.It("approve plan for execution applies artifact approval", func() {
	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "a.go", OldText: "old", NewText: "new"}},
	}

	got, err := approvePlanForExecution(context.Background(), "plan.json", plan, PlanApprovalPhaseInitial, Options{
		OnPlanApproval: func(_ context.Context, req PlanApprovalRequest) PlanApprovalDecision {
			gomega.Expect(req.Phase != PlanApprovalPhaseInitial).To(gomega.BeFalse(), fmt.Sprintf("phase = %q, want initial", req.Phase))
			gomega.Expect(req.Paths).To(gomega.HaveLen(1), fmt.Sprintf("paths = %#v, want a.go", req.Paths))
			gomega.Expect(req.Paths[0]).To(gomega.Equal("a.go"), fmt.Sprintf("paths = %#v, want a.go", req.Paths))

			return PlanApprovalDecision{Approved: true}
		},
	}, []string{"a.go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("approvePlanForExecution: %v", err))
	gomega.Expect(got.Approval.Required).To(gomega.BeTrue(), fmt.Sprint("approval should be required after explicit TUI approval"))
	gomega.Expect(got.Approval.Status != planartifact.ApprovalStatusApproved).To(gomega.BeFalse(), fmt.Sprintf("status = %q, want approved", got.Approval.Status))
	gomega.Expect(got.Approval.ExecutionPermission != planartifact.ExecutionPermissionExecuteAll).To(gomega.BeFalse(), fmt.Sprintf("permission = %q, want execute_all", got.Approval.ExecutionPermission))
	gomega.Expect(got.Approval.ApprovedPaths).To(gomega.HaveLen(1), fmt.Sprintf("approved paths = %#v, want a.go", got.Approval.ApprovedPaths))
	gomega.Expect(got.Approval.ApprovedPaths[0]).To(gomega.Equal("a.go"), fmt.Sprintf("approved paths = %#v, want a.go", got.Approval.ApprovedPaths))
})

var _ = ginkgo.It("approved plan authorized new files adds only approved missing paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package app\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed existing.go: %v", err))
	}

	plan := planartifact.PlanArtifact{
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              planartifact.ApprovalStatusApproved,
			ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
			ApprovedPaths:       []string{"existing.go", "new.go", "bad_shape.go", `C:\repo\outside.go`},
		},
		Edits: []planartifact.PlanEdit{
			{Path: "existing.go", OldText: "package app\n", NewText: "package app\n\nconst Existing = true\n"},
			{Path: "new.go", ReplaceAll: true, NewText: "package app\n"},
			{Path: "bad_shape.go", NewText: "package app\n"},
			{Path: "unapproved.go", ReplaceAll: true, NewText: "package app\n"},
			{Path: `C:\repo\outside.go`, ReplaceAll: true, NewText: "package app\n"},
		},
	}

	got := approvedPlanAuthorizedNewFiles(root, []string{"already_authorized.go"}, plan)
	want := []string{"already_authorized.go", "new.go"}
	gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want %#v", got, want))
})

var _ = ginkgo.It("approved plan authorized new files requires satisfied approval", func() {
	root := ginkgo.GinkgoT().TempDir()
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
	gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want %#v", got, want))
})

var _ = ginkgo.It("approve plan for execution rejects denied decision", func() {
	_, err := approvePlanForExecution(context.Background(), "plan.json", planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{Path: "a.go", OldText: "old", NewText: "new"}},
	}, PlanApprovalPhaseInitial, Options{
		OnPlanApproval: func(context.Context, PlanApprovalRequest) PlanApprovalDecision {
			return PlanApprovalDecision{Reason: "operator denied"}
		},
	}, []string{"a.go"})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("denial error = %v, want operator denied", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("operator denied"), fmt.Sprintf("denial error = %v, want operator denied", err))
})

var _ = ginkgo.It("approve plan for execution rejects partial decision", func() {
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
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("partial approval error = %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("partial plan edit approval is not supported"), fmt.Sprintf("partial approval error = %v", err))
})
