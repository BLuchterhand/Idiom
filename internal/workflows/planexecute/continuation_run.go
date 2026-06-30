package planexecute

import (
	"fmt"
	"io"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
)

type executorContinuation struct {
	State                ProgressState
	StatePath            string
	PlanPath             string
	Plan                 planartifact.PlanArtifact
	RequiredEditPaths    []string
	AllowedEditPaths     []string
	WriteAllowedPaths    []string
	WriteAllowedExplicit bool
	AuthorizedNewFiles   []string
}

func loadExecutorContinuation(root string, sessionID string, matches func(ProgressState) bool, deps Dependencies) (executorContinuation, bool, error) {
	deps = normalizeDependencies(deps)

	var (
		state     ProgressState
		statePath string
		found     bool
		err       error
	)

	if strings.TrimSpace(sessionID) == "" {
		state, statePath, found, err = findLatestContinuationProgressState(root, matches)
	} else {
		state, statePath, found, err = TryLoadProgressState(root, sessionID)
		if found && !continuationProgressStateEligible(state) {
			found = false
		}
		if found && matches != nil && !matches(state) {
			found = false
		}
	}
	if err != nil || !found {
		return executorContinuation{}, false, err
	}

	if continuationProgressStateHasSucceededEdit(state) {
		return executorContinuation{}, false, nil
	}

	planPath := state.ResolvedPlanPath(root)
	if strings.TrimSpace(planPath) == "" {
		return executorContinuation{}, false, nil
	}

	plan, err := deps.LoadPlanArtifact(planPath)
	if err != nil {
		return executorContinuation{}, false, err
	}

	required := continuationScopeRequiredEditPaths(state, plan)
	if len(required) == 0 {
		return executorContinuation{}, false, nil
	}

	if mismatch := preflightPlanApprovalAndScope(plan, required); mismatch != nil {
		return executorContinuation{}, false, nil
	}

	writeAllowed := mergePlanExecutePaths(state.WriteAllowedPaths)
	writeAllowedExplicit := state.WriteAllowedExplicit
	if len(writeAllowed) == 0 {
		writeAllowed = required
	}

	return executorContinuation{
		State:                state,
		StatePath:            statePath,
		PlanPath:             planPath,
		Plan:                 plan,
		RequiredEditPaths:    required,
		AllowedEditPaths:     mergePlanExecutePaths(state.AllowedEditPaths),
		WriteAllowedPaths:    writeAllowed,
		WriteAllowedExplicit: writeAllowedExplicit,
		AuthorizedNewFiles:   approvedPlanAuthorizedNewFiles(root, nil, plan),
	}, true, nil
}

func continuationProgressStateHasSucceededEdit(state ProgressState) bool {
	if state.Executor.Succeeded > 0 {
		return true
	}
	for _, edit := range state.Edits {
		if edit.Status == plancontract.PlanExecuteProgressStatusCompleted {
			return true
		}
	}
	return false
}

func RunExecutorContinuationWithDependencies(cfg config.Config, opts Options, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, deps Dependencies) RunResult {
	deps = normalizeDependencies(deps)
	ctx := planExecuteContext(opts.Context)
	defer deps.CleanupPlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)

	deps.CleanupPlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)

	if modelOverride := strings.TrimSpace(opts.Model); modelOverride != "" {
		cfg.LlamaServer.Model = modelOverride
		opts.UseActiveModel = true
	}

	prompt, err := planExecutePrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1}
	}

	continuation, ok, err := loadExecutorContinuation(cfg.Workspace.Root, opts.Resume, nil, deps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, FailureReason: sanitizeProgressReason(err.Error())}
	}
	if !ok {
		reason := "no executable plan-execute continuation is available"
		fmt.Fprintln(stderr, reason)
		return RunResult{ExitCode: 1, FailureReason: reason}
	}

	cfg.Workspace.SessionID = continuation.State.SessionID

	emitRunJSONEvent := func(eventType plancontract.PlanExecuteEventType, fields map[string]any) (RunResult, bool) {
		if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, eventType, fields); err != nil {
			result := planExecuteJSONEventFailureResult(continuation.State.SessionID, continuation.StatePath, "", err)
			if stderr != nil {
				fmt.Fprintln(stderr, result.FailureReason)
			}
			return result, true
		}
		return RunResult{}, false
	}

	queueResult := deps.AcquireQueueSlot(cfg, QueueAcquireOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		SessionID:  continuation.State.SessionID,
	}, rec, stdout, stderr)
	defer queueResult.Release()

	if !queueResult.Allowed {
		reason := sanitizeProgressReason(fmt.Sprintf("queue admission required but unavailable: state=%s pool=%s reason=%s", queueResult.State, queueResult.Pool, queueResult.Reason))
		if stderr != nil {
			fmt.Fprintln(stderr, reason)
		}
		return RunResult{
			ExitCode:      1,
			SessionID:     continuation.State.SessionID,
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: reason,
		}
	}

	state := continuation.State
	state.RequiredEditPaths = mergePlanExecutePaths(state.RequiredEditPaths, continuation.RequiredEditPaths)
	state.AllowedEditPaths = mergePlanExecutePaths(state.AllowedEditPaths, continuation.AllowedEditPaths)
	state.SetWriteAllowedEnvelope(continuation.WriteAllowedPaths, continuation.WriteAllowedExplicit)

	scopeContract := plancontract.NewPlanScopeContract(continuation.RequiredEditPaths, continuation.AllowedEditPaths, state.EditSetObligations)
	if len(continuation.WriteAllowedPaths) > 0 || continuation.WriteAllowedExplicit {
		scopeContract = scopeContract.WithWriteAllowedPaths(continuation.WriteAllowedPaths)
	}
	state.SetScopeContractID(scopeContract.ContractID)

	opts.RequiredEditPaths = MultiFlag(continuation.RequiredEditPaths)
	opts.AllowedEditPaths = MultiFlag(continuation.AllowedEditPaths)
	opts.WriteAllowedPaths = MultiFlag(continuation.WriteAllowedPaths)
	opts.WriteAllowedPathsExplicit = continuation.WriteAllowedExplicit
	opts.ScopeContract = scopeContract
	opts.EditSetObligations = state.EditSetObligations
	opts.AuthorizedNewFiles = mergePlanExecutePaths(opts.AuthorizedNewFiles, continuation.AuthorizedNewFiles)

	scope := planExecuteRunScope{
		ScopeContract:        scopeContract,
		RequiredEditPaths:    continuation.RequiredEditPaths,
		AllowedEditPaths:     continuation.AllowedEditPaths,
		WriteAllowedPaths:    continuation.WriteAllowedPaths,
		WriteAllowedExplicit: continuation.WriteAllowedExplicit,
		EditSetObligations:   state.EditSetObligations,
		AuthorizedNewFiles:   opts.AuthorizedNewFiles,
	}

	if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventResumeStateLoaded, ProgressStateEventFields(state, continuation.StatePath)); failed {
		return result
	}
	if result, failed := emitPlanExecuteRunScopeResolved(scope, rec, emitRunJSONEvent); failed {
		return result
	}
	if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactLoaded, map[string]any{
		plancontract.PlanExecuteEventFieldPath:   continuation.PlanPath,
		plancontract.PlanExecuteEventFieldEdits:  len(continuation.Plan.Edits),
		plancontract.PlanExecuteEventFieldPackID: strings.TrimSpace(continuation.Plan.PackID),
	}); failed {
		return result
	}

	return runLoadedPlanExecution(ctx, cfg, opts, loadedPlanExecutionInput{
		Prompt:                    prompt,
		PlanPath:                  continuation.PlanPath,
		Plan:                      continuation.Plan,
		Progress:                  state,
		ProgressPath:              continuation.StatePath,
		Scope:                     scope,
		ExecutorRequiredEditPaths: continuation.RequiredEditPaths,
		PromptRunner:              promptRun,
		Recorder:                  rec,
		EmitRunJSONEvent:          emitRunJSONEvent,
	}, stdout, stderr, deps)
}
