package planexecute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	planartifact "idiom/internal/contracts/planartifact"
	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/repository/planartifactstore"
	"idiom/internal/runtime/queueadmission"
	"idiom/internal/runtime/writerrouting"
)

func planExecuteTestDependencies() Dependencies {
	return Dependencies{
		AcquireQueueSlot:     acquireQueueSlotForTest,
		BuildWriterRoute:     buildWriterRouteDecisionForTest,
		RunPromptContextTool: runPromptContextToolForTest,
		ApplyExecutorEdit:    applyExecutorEditForTest,
		LoadPlanArtifact:     workspace.LoadPlanArtifact,
		SubmitAutoPlan:       submitAutoPlanForTest,
		SourceSanity:         runSourceSanityForTest,
		SourceSanityCovers:   sourcekind.CoversSourceSanity,
		EditPreflight:        editPreflightPolicyForTest(),
		FindPlanArtifact:     findLatestPlanArtifactForTest,
		CleanupPlanArtifacts: func(string, time.Duration, io.Writer) {},
	}
}

func runForTest(cfg config.Config, opts Options, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) RunResult {
	return RunWithDependencies(cfg, opts, promptRun, rec, stdout, stderr, planExecuteTestDependencies())
}

func runExecutorPhaseForTest(cfg config.Config, plan planartifact.PlanArtifact, opts ExecutorPhaseOptions) (ExecutorPhaseReport, error) {
	return RunExecutorPhaseWithDependencies(cfg, plan, opts, planExecuteTestDependencies())
}

func runReviewRepairPhaseForTest(
	cfg config.Config,
	originalPrompt string,
	plan planartifact.PlanArtifact,
	requiredEditPaths []string,
	baseline *ReviewRepairBaseline,
	executorReport ExecutorPhaseReport,
	opts Options,
	stdout io.Writer,
	promptRun PromptRunner,
	rec *telemetry.Recorder,
) (ReviewRepairPhaseReport, error) {
	return RunReviewRepairPhaseWithDependencies(cfg, originalPrompt, plan, requiredEditPaths, baseline, executorReport, opts, stdout, promptRun, rec, planExecuteTestDependencies())
}

func buildPromptContextPackSectionForTest(cfg config.Config, opts PromptContextPackOptions, stderr io.Writer) string {
	return BuildPromptContextPackSectionWithRunner(cfg, opts, stderr, planExecuteTestDependencies().RunPromptContextTool)
}

func acquireQueueSlotForTest(cfg config.Config, opts QueueAcquireOptions, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) QueueSlot {
	label := strings.TrimSpace(opts.Resume)
	if label == "" {
		label = "plan-execute:" + strings.TrimSpace(opts.SessionID)
	}

	result := queueadmission.AcquireWithResult(cfg, queueadmission.Options{
		Command:        "plan_execute",
		EventPrefix:    queuecontract.CommandEventPrefixPlanExecute,
		TelemetryEvent: "plan_execute_queue",
		Label:          label,
		Client:         fmt.Sprintf("idiom-plan-execute-test/%d", os.Getpid()),
		JSONEvents:     opts.JSONEvents,
		Mode:           queuecontract.AdmissionModeRequired,
	}, rec, stdout, stderr)

	return QueueSlot{
		Allowed:     result.Allowed,
		State:       string(result.State),
		Pool:        result.Pool,
		Reason:      result.Reason,
		ReleaseFunc: result.Release,
	}
}

func buildWriterRouteDecisionForTest(input WriterRouteDecisionInput) writerroute.Decision {
	cfg := input.Config
	opts := input.Options

	aliases := writerrouting.RoleAliases(cfg.Workspace.Root, "")
	userOverride := strings.TrimSpace(opts.Model) != ""
	if userOverride {
		aliases = nil
	}

	overrideModel := ""
	if userOverride {
		overrideModel = strings.TrimSpace(opts.Model)
	}

	return writerrouting.BuildDecision(cfg, writerrouting.DecisionOptions{
		Mode:              writerroute.ModePlanSubmission,
		Lane:              string(lanecontract.LanePlanExecute),
		PlannerModel:      overrideModel,
		ExecutorModel:     overrideModel,
		ReviewerModel:     overrideModel,
		ClassifierModel:   overrideModel,
		RoleAliases:       aliases,
		VerificationOwner: writerroute.VerificationOwnerPlannerOwned,
		RequiredEditPaths: input.RequiredEditPaths,
		ReasonCodes:       []writerroute.ReasonCode{writerroute.ReasonMultiFilePlanExecute},
		UserOverride:      userOverride,
		TaskFamily:        string(autonomy.TaskFamilyPlanThenExecutePlanner),
	})
}

func runPromptContextToolForTest(ctx context.Context, wsCfg config.Workspace, req PromptContextToolRequest) PromptContextToolResult {
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{
		Tool:              req.Tool,
		Task:              req.Task,
		Mode:              req.Mode,
		Language:          req.Language,
		Refresh:           req.Refresh,
		Limit:             req.Limit,
		FreshnessMaxFiles: req.FreshnessMaxFiles,
		BudgetTokens:      req.BudgetTokens,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	})

	return PromptContextToolResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Output:  result.Output,
		Error:   result.Error,
	}
}

func applyExecutorEditForTest(ctx context.Context, wsCfg config.Workspace, req ExecutorEditRequest) ExecutorEditResult {
	mgr := workspace.NewManager(wsCfg)
	mgr.SetBypassReadBeforeEdit(true)

	result := mgr.Execute(ctx, workspace.ToolRequest{
		Tool:        req.Tool,
		Path:        req.Path,
		OldText:     req.OldText,
		NewText:     req.NewText,
		StartLine:   req.StartLine,
		EndLine:     req.EndLine,
		Content:     req.Content,
		ReplaceAll:  req.ReplaceAll,
		Description: req.Description,
	})

	return ExecutorEditResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func submitAutoPlanForTest(ctx context.Context, wsCfg config.Workspace, req AutoPlanSubmitRequest) AutoPlanSubmitResult {
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{
		Tool:      workspace.ToolNameSubmitPlan,
		Rationale: req.Rationale,
		Edits:     append([]planartifact.PlanEdit(nil), req.Edits...),
	})

	return AutoPlanSubmitResult{
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func runSourceSanityForTest(ctx context.Context, wsCfg config.Workspace, path string) SourceSanityResult {
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: path})
	return SourceSanityResult{
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func editPreflightPolicyForTest() EditPreflightPolicy {
	return EditPreflightPolicy{
		UnsupportedProofClaimReason:                     workspace.PlanEditUnsupportedProofClaimReason,
		RejectExistingSourceWholeFileRewrite:            workspace.RejectExistingSourceWholeFileRewrite,
		RejectExistingSourceDestructiveLineRangeRewrite: workspace.RejectExistingSourceDestructiveLineRangeRewrite,
		RejectSourceLineRangeTopLevelConstructDrift:     workspace.RejectSourceLineRangeTopLevelConstructDrift,
		NeedsAnchorProvenance:                           workspace.PlanEditNeedsAnchorProvenance,
		RequiresSubmitPlanAnchorProvenance:              workspace.PlanArtifactRequiresSubmitPlanAnchorProvenance,
		ValidateAnchorProvenance:                        workspace.ValidatePlanEditAnchorProvenance,
		IsCompletedPlanningArtifact:                     workspace.IsCompletedPlanningArtifact,
		PreviewEditContent:                              workspace.PreviewPlanEditContent,
		SuspiciousAdjacentDuplicateCodeLine:             workspace.NewSuspiciousAdjacentDuplicateCodeLine,
	}
}

func findLatestPlanArtifactForTest(root string, notBefore time.Time, sessionID string) (string, error) {
	dir, _, err := planartifactstore.StoreDir(root)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrPlanArtifactNotFound
		}
		return "", err
	}

	sessionID = strings.TrimSpace(sessionID)
	var latestPath string
	var latestMod time.Time

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "last.plan.json" || !strings.HasSuffix(entry.Name(), ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || (!notBefore.IsZero() && info.ModTime().Before(notBefore)) {
			continue
		}
		path, _, err := planartifactstore.StoreFilePath(root, entry.Name())
		if err != nil {
			continue
		}
		if sessionID != "" {
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var head struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(body, &head); err != nil || strings.TrimSpace(head.SessionID) != sessionID {
				continue
			}
		}
		if latestPath == "" || info.ModTime().After(latestMod) {
			latestPath = path
			latestMod = info.ModTime()
		}
	}

	if latestPath == "" {
		return "", ErrPlanArtifactNotFound
	}
	return latestPath, nil
}

func cleanupStalePlanArtifacts(root string, threshold time.Duration, logOut io.Writer) {
	dir, _, err := planartifactstore.StoreDir(root)
	if err != nil {
		fmt.Fprintf(logOut, "plan-execute cleanup: resolve plan dir: %v\n", err)
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logOut, "plan-execute cleanup: read %s: %v\n", dir, err)
		}
		return
	}

	cutoff := time.Now().Add(-threshold)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "last.plan.json" || !strings.HasSuffix(entry.Name(), ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path, _, err := planartifactstore.StoreFilePath(root, entry.Name())
		if err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: resolve %s: %v\n", entry.Name(), err)
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: remove %s: %v\n", path, err)
		}
	}
}
