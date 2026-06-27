package adapters

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	planartifact "idiom/internal/contracts/planartifact"
	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/queueadmission"
	"idiom/internal/runtime/writerrouting"
	"idiom/internal/workflows/planexecute"
)

func Dependencies() planexecute.Dependencies {
	return planexecute.Dependencies{
		AcquireQueueSlot:     AcquireQueueSlot,
		BuildWriterRoute:     BuildWriterRouteDecision,
		RunPromptContextTool: RunPromptContextTool,
		ApplyExecutorEdit:    ApplyExecutorEdit,
		LoadPlanArtifact:     LoadPlanArtifact,
		SubmitAutoPlan:       SubmitAutoPlan,
		SourceSanity:         RunSourceSanity,
		SourceSanityCovers:   workspace.SourceSanityCoversPath,
		EditPreflight:        EditPreflightPolicy(),
		FindPlanArtifact:     FindLatestPlanArtifact,
		CleanupPlanArtifacts: CleanupStalePlanArtifacts,
	}
}

func AcquireQueueSlot(cfg config.Config, opts planexecute.QueueAcquireOptions, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) planexecute.QueueSlot {
	label := strings.TrimSpace(opts.Resume)
	if label == "" {
		label = "plan-execute:" + strings.TrimSpace(opts.SessionID)
	}

	client := fmt.Sprintf("idiom-plan-execute/%d", os.Getpid())

	result := queueadmission.AcquireWithResult(cfg, queueadmission.Options{
		Command:        "plan_execute",
		EventPrefix:    queuecontract.CommandEventPrefixPlanExecute,
		TelemetryEvent: "plan_execute_queue",
		Label:          label,
		Client:         client,
		JSONEvents:     opts.JSONEvents,
		Mode:           queuecontract.AdmissionModeRequired,
	}, rec, stdout, stderr)

	return planexecute.QueueSlot{
		Allowed:     result.Allowed,
		State:       string(result.State),
		Pool:        result.Pool,
		Reason:      result.Reason,
		ReleaseFunc: result.Release,
	}
}

func BuildWriterRouteDecision(input planexecute.WriterRouteDecisionInput) writerroute.Decision {
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

func RunPromptContextTool(ctx context.Context, wsCfg config.Workspace, req planexecute.PromptContextToolRequest) planexecute.PromptContextToolResult {
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

	return planexecute.PromptContextToolResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Output:  result.Output,
		Error:   result.Error,
	}
}

func ApplyExecutorEdit(ctx context.Context, wsCfg config.Workspace, req planexecute.ExecutorEditRequest) planexecute.ExecutorEditResult {
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

	return planexecute.ExecutorEditResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func LoadPlanArtifact(path string) (planartifact.PlanArtifact, error) {
	return workspace.LoadPlanArtifact(path)
}

func SubmitAutoPlan(ctx context.Context, wsCfg config.Workspace, req planexecute.AutoPlanSubmitRequest) planexecute.AutoPlanSubmitResult {
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{
		Tool:      workspace.ToolNameSubmitPlan,
		Rationale: req.Rationale,
		Edits:     append([]planartifact.PlanEdit(nil), req.Edits...),
	})

	return planexecute.AutoPlanSubmitResult{
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func RunSourceSanity(ctx context.Context, wsCfg config.Workspace, path string) planexecute.SourceSanityResult {
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: path})
	return planexecute.SourceSanityResult{
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func EditPreflightPolicy() planexecute.EditPreflightPolicy {
	return planexecute.EditPreflightPolicy{
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
