package planexecute

import (
	"context"
	"errors"
	"io"
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
)

type Dependencies struct {
	AcquireQueueSlot     QueueAcquirer
	BuildWriterRoute     WriterRouteDecider
	RunPromptContextTool PromptContextToolRunner
	ApplyExecutorEdit    ExecutorEditApplier
	LoadPlanArtifact     PlanArtifactLoader
	SubmitAutoPlan       AutoPlanSubmitter
	SourceSanity         SourceSanityRunner
	SourceSanityCovers   SourceSanityCoverage
	EditPreflight        EditPreflightPolicy
	FindPlanArtifact     PlanArtifactFinder
	CleanupPlanArtifacts PlanArtifactCleaner
}

type PlanArtifactLoader func(string) (planartifact.PlanArtifact, error)

var errPlanExecuteDependencyMissing = errors.New("plan-execute dependency missing")

func normalizeDependencies(deps Dependencies) Dependencies {
	if deps.AcquireQueueSlot == nil {
		deps.AcquireQueueSlot = missingQueueAcquirer
	}
	if deps.BuildWriterRoute == nil {
		deps.BuildWriterRoute = missingWriterRouteDecider
	}
	if deps.RunPromptContextTool == nil {
		deps.RunPromptContextTool = missingPromptContextToolRunner
	}
	if deps.ApplyExecutorEdit == nil {
		deps.ApplyExecutorEdit = missingExecutorEditApplier
	}
	if deps.LoadPlanArtifact == nil {
		deps.LoadPlanArtifact = missingPlanArtifactLoader
	}
	if deps.SubmitAutoPlan == nil {
		deps.SubmitAutoPlan = missingAutoPlanSubmitter
	}
	if deps.SourceSanity == nil {
		deps.SourceSanity = missingSourceSanityRunner
	}
	if deps.SourceSanityCovers == nil {
		deps.SourceSanityCovers = missingSourceSanityCoverage
	}
	if !editPreflightPolicyComplete(deps.EditPreflight) {
		deps.EditPreflight = missingEditPreflightPolicy()
	}
	if deps.FindPlanArtifact == nil {
		deps.FindPlanArtifact = missingPlanArtifactFinder
	}
	if deps.CleanupPlanArtifacts == nil {
		deps.CleanupPlanArtifacts = missingPlanArtifactCleaner
	}
	return deps
}

func missingQueueAcquirer(config.Config, QueueAcquireOptions, *telemetry.Recorder, io.Writer, io.Writer) QueueSlot {
	return QueueSlot{Allowed: false, Reason: errPlanExecuteDependencyMissing.Error() + ": queue acquirer"}
}

func missingWriterRouteDecider(WriterRouteDecisionInput) writerroute.Decision {
	return writerroute.Decision{}
}

func missingPromptContextToolRunner(context.Context, config.Workspace, PromptContextToolRequest) PromptContextToolResult {
	return PromptContextToolResult{Error: errPlanExecuteDependencyMissing.Error() + ": prompt context tool runner"}
}

func missingExecutorEditApplier(context.Context, config.Workspace, ExecutorEditRequest) ExecutorEditResult {
	return ExecutorEditResult{Error: errPlanExecuteDependencyMissing.Error() + ": executor edit applier"}
}

func missingPlanArtifactLoader(string) (planartifact.PlanArtifact, error) {
	return planartifact.PlanArtifact{}, errPlanExecuteDependencyMissing
}

func missingAutoPlanSubmitter(context.Context, config.Workspace, AutoPlanSubmitRequest) AutoPlanSubmitResult {
	return AutoPlanSubmitResult{Error: errPlanExecuteDependencyMissing.Error() + ": auto plan submitter"}
}

func missingSourceSanityRunner(context.Context, config.Workspace, string) SourceSanityResult {
	return SourceSanityResult{Error: errPlanExecuteDependencyMissing.Error() + ": source sanity runner"}
}

func missingSourceSanityCoverage(string) bool {
	return false
}

func missingEditPreflightPolicy() EditPreflightPolicy {
	errText := errPlanExecuteDependencyMissing.Error() + ": edit preflight policy"
	return EditPreflightPolicy{
		UnsupportedProofClaimReason: func(planartifact.PlanEdit) string { return errText },
		RejectExistingSourceWholeFileRewrite: func(string, []byte, planartifact.PlanEdit) error {
			return errors.New(errText)
		},
		RejectExistingSourceDestructiveLineRangeRewrite: func(string, string, planartifact.PlanEdit) error {
			return errors.New(errText)
		},
		RejectSourceLineRangeTopLevelConstructDrift: func(string, string, planartifact.PlanEdit) error {
			return errors.New(errText)
		},
		NeedsAnchorProvenance:              func(planartifact.PlanEdit) bool { return true },
		RequiresSubmitPlanAnchorProvenance: func(planartifact.PlanArtifact) bool { return true },
		ValidateAnchorProvenance:           func(string, planartifact.PlanEdit) error { return errors.New(errText) },
		IsCompletedPlanningArtifact:        func(string, []byte) bool { return false },
		PreviewEditContent: func(current string, edit planartifact.PlanEdit) (string, bool, error) {
			return current, false, errors.New(errText)
		},
		SuspiciousAdjacentDuplicateCodeLine: func(string, []byte, []byte) (int, string, bool) {
			return 0, "", false
		},
	}
}

func editPreflightPolicyComplete(policy EditPreflightPolicy) bool {
	return policy.UnsupportedProofClaimReason != nil &&
		policy.RejectExistingSourceWholeFileRewrite != nil &&
		policy.RejectExistingSourceDestructiveLineRangeRewrite != nil &&
		policy.RejectSourceLineRangeTopLevelConstructDrift != nil &&
		policy.NeedsAnchorProvenance != nil &&
		policy.RequiresSubmitPlanAnchorProvenance != nil &&
		policy.ValidateAnchorProvenance != nil &&
		policy.IsCompletedPlanningArtifact != nil &&
		policy.PreviewEditContent != nil &&
		policy.SuspiciousAdjacentDuplicateCodeLine != nil
}

type PlanArtifactFinder func(root string, notBefore time.Time, sessionID string) (string, error)
type PlanArtifactCleaner func(root string, threshold time.Duration, logOut io.Writer)

func missingPlanArtifactFinder(string, time.Time, string) (string, error) {
	return "", errPlanExecuteDependencyMissing
}

func missingPlanArtifactCleaner(string, time.Duration, io.Writer) {}
