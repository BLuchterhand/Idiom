package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

func TestDependenciesExposesProductionAdapters(t *testing.T) {
	deps := Dependencies()

	if deps.AcquireQueueSlot == nil ||
		deps.BuildWriterRoute == nil ||
		deps.RunPromptContextTool == nil ||
		deps.ApplyExecutorEdit == nil ||
		deps.LoadPlanArtifact == nil ||
		deps.SubmitAutoPlan == nil ||
		deps.SourceSanity == nil ||
		deps.SourceSanityCovers == nil ||
		deps.FindPlanArtifact == nil ||
		deps.CleanupPlanArtifacts == nil {
		t.Fatalf("Dependencies() returned an incomplete adapter set: %#v", deps)
	}

	policy := deps.EditPreflight
	if policy.UnsupportedProofClaimReason == nil ||
		policy.RejectExistingSourceWholeFileRewrite == nil ||
		policy.RejectExistingSourceDestructiveLineRangeRewrite == nil ||
		policy.RejectSourceLineRangeTopLevelConstructDrift == nil ||
		policy.NeedsAnchorProvenance == nil ||
		policy.RequiresSubmitPlanAnchorProvenance == nil ||
		policy.ValidateAnchorProvenance == nil ||
		policy.IsCompletedPlanningArtifact == nil ||
		policy.PreviewEditContent == nil ||
		policy.SuspiciousAdjacentDuplicateCodeLine == nil {
		t.Fatalf("Dependencies() returned incomplete edit preflight policy: %#v", policy)
	}
}

func TestBuildWriterRouteDecisionHonorsModelOverride(t *testing.T) {
	root := t.TempDir()
	decision := BuildWriterRouteDecision(planexecute.WriterRouteDecisionInput{
		Config: config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
		Options: planexecute.Options{
			Model: "override-model",
		},
		RequiredEditPaths: []string{"pkg/target.go"},
	})

	if decision.WriterMode != string(writerroute.ModePlanSubmission) {
		t.Fatalf("WriterMode = %q, want %q", decision.WriterMode, writerroute.ModePlanSubmission)
	}
	if !decision.UserOverride {
		t.Fatal("UserOverride should be true when planexecute.Options.Model is set")
	}
	if decision.PlannerModel != "override-model" || decision.ExecutorModel != "override-model" || decision.ReviewerModel != "override-model" {
		t.Fatalf("route models not overridden consistently: %#v", decision)
	}
	if len(decision.RequiredEditPaths) != 1 || decision.RequiredEditPaths[0] != "pkg/target.go" {
		t.Fatalf("RequiredEditPaths = %#v", decision.RequiredEditPaths)
	}
}

func TestRunPromptContextToolForwardsRequiredEditPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	result := RunPromptContextTool(context.Background(), config.Workspace{Root: root, CWD: root}, planexecute.PromptContextToolRequest{
		Tool:              "context_pack",
		Task:              "Plan a change to Target",
		Mode:              "plan",
		Language:          "go",
		Limit:             4,
		BudgetTokens:      800,
		RequiredEditPaths: []string{"pkg/target.go"},
	})

	if result.Error != "" {
		t.Fatalf("RunPromptContextTool error = %s", result.Error)
	}
	if result.Tool != "context_pack" {
		t.Fatalf("Tool = %q, want context_pack", result.Tool)
	}
	if !strings.Contains(result.Output, "pkg/target.go") {
		t.Fatalf("context_pack output should include required edit path, output=%s", result.Output)
	}
}

func TestApplyExecutorEditUsesWorkspaceEditAdapter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	result := ApplyExecutorEdit(context.Background(), config.Workspace{Root: root, CWD: root, EnableWrites: true}, planexecute.ExecutorEditRequest{
		Tool:    "edit_file",
		Path:    "a.txt",
		OldText: "before\n",
		NewText: "after\n",
	})

	if result.Error != "" {
		t.Fatalf("ApplyExecutorEdit error = %s", result.Error)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(body) != "after\n" {
		t.Fatalf("edited body = %q", string(body))
	}
}
