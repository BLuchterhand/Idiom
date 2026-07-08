package planexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/atomicfile"
)

func approvePlanForExecution(ctx context.Context, planPath string, plan planartifact.PlanArtifact, phase PlanApprovalPhase, opts Options, paths []string) (planartifact.PlanArtifact, error) {
	if opts.OnPlanApproval == nil {
		return plan, nil
	}
	ctx = planExecuteContext(ctx)

	paths = mergePlanExecutePaths(paths)
	if len(paths) == 0 {
		paths = planChangedPaths(plan)
	}

	decision := opts.OnPlanApproval(ctx, PlanApprovalRequest{
		Phase:                   phase,
		PlanPath:                planPath,
		Plan:                    plan,
		Paths:                   append([]string(nil), paths...),
		PreviouslyApprovedPaths: append([]string(nil), plan.Approval.ApprovedPaths...),
	})
	if !decision.Approved {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "plan edit approval denied"
		}

		return plan, fmt.Errorf("%s", reason)
	}

	approvedPaths := mergePlanExecutePaths(decision.ApprovedPaths)
	if len(approvedPaths) == 0 {
		approvedPaths = paths
	}
	if !slices.Equal(approvedPaths, paths) {
		return plan, fmt.Errorf("partial plan edit approval is not supported; approve all planned paths or deny the plan")
	}

	status := planartifact.ApprovalStatusApproved
	if decision.Blanket {
		status = planartifact.ApprovalStatusAutoApproved
	}

	approvedBy := strings.TrimSpace(decision.ApprovedBy)
	if approvedBy == "" {
		approvedBy = "operator"
	}

	plan.Approval = planartifact.PlanArtifactApproval{
		Required:            true,
		Status:              status,
		ScopeID:             strings.TrimSpace(plan.Approval.ScopeID),
		Mode:                planartifact.ApprovalModePlanThenExecute,
		ApprovedBy:          approvedBy,
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		ExpirationPolicy:    planartifact.ApprovalExpirationWorkspaceDigest,
		ApprovedPaths:       approvedPaths,
		ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
	}

	return planartifact.NormalizeLoadedPlanArtifact(plan), nil
}

func approvedPlanAuthorizedNewFiles(root string, base []string, plan planartifact.PlanArtifact) []string {
	authorized := mergePlanExecutePaths(base)
	root = strings.TrimSpace(root)
	if root == "" {
		return authorized
	}

	plan = planartifact.NormalizeLoadedPlanArtifact(plan)
	if !plan.Approval.Required ||
		!planartifact.ApprovalStatusSatisfied(string(plan.Approval.Status)) ||
		plan.Approval.ExecutionPermission == planartifact.ExecutionPermissionBlocked {
		return authorized
	}

	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	if len(approved) == 0 && plan.Approval.ExecutionPermission == planartifact.ExecutionPermissionExecuteAll {
		approved = planExecutePathSet(planChangedPaths(plan))
	}

	for _, edit := range plan.Edits {
		if !planEditCreatesMissingFile(edit) {
			continue
		}

		path := pathparse.NormalizeWorkspaceRelativePath(edit.Path)
		if path == "" {
			continue
		}
		if _, ok := approved[path]; !ok {
			continue
		}

		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); os.IsNotExist(err) {
			authorized = append(authorized, path)
		}
	}

	return mergePlanExecutePaths(authorized)
}

func planEditCreatesMissingFile(edit planartifact.PlanEdit) bool {
	return edit.ReplaceAll &&
		strings.TrimSpace(edit.OldText) == "" &&
		strings.TrimSpace(edit.NewText) != ""
}

func persistApprovedPlanForExecution(planPath string, plan planartifact.PlanArtifact) error {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		return fmt.Errorf("plan artifact path is empty")
	}

	updated, err := patchPlanArtifactApproval(planPath, plan.Approval)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(planPath, updated, 0o644); err != nil {
		return fmt.Errorf("write approved plan artifact %q: %w", planPath, err)
	}

	if filepath.Base(planPath) == "last.plan.json" {
		return nil
	}

	// ProgressState.PlanPath points at the timestamped artifact above. The
	// latest pointer is a convenience for humans and tests, not execution
	// authority, so a pointer refresh failure must not turn an approved
	// timestamped artifact into a failed approval.
	lastPath := filepath.Join(filepath.Dir(planPath), "last.plan.json")
	_ = atomicfile.WriteFile(lastPath, updated, 0o644)

	return nil
}

func patchPlanArtifactApproval(planPath string, approval planartifact.PlanArtifactApproval) ([]byte, error) {
	body, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("read plan artifact %q: %w", planPath, err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode plan artifact %q: %w", planPath, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("decode plan artifact %q: empty object", planPath)
	}

	approvalBody, err := json.Marshal(approval)
	if err != nil {
		return nil, fmt.Errorf("encode plan approval: %w", err)
	}
	raw["approval"] = approvalBody

	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode approved plan artifact %q: %w", planPath, err)
	}

	return append(updated, '\n'), nil
}

func rejectPlanExecuteApproval(root string, progress ProgressState, progressPath string, opts Options, stdout io.Writer, stderr io.Writer, failureReason string) RunResult {
	progress.Status = plancontract.PlanExecuteProgressStatusFailed
	progress.FailureKind = plancontract.PlanExecuteFailurePlanApprovalDenied
	progress.FailureReason = sanitizeProgressReason(failureReason)

	if savedPath, saveErr := saveRunProgressState(root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
		if savedPath != "" {
			progressPath = savedPath
		}

		fmt.Fprintln(stderr, saveErr)
	}

	fmt.Fprintln(stderr, failureReason)

	return finalizePlanExecuteRunResult(root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(progress.FailureKind), FailureReason: sanitizeProgressReason(failureReason)})
}
