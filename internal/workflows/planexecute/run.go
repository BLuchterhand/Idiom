package planexecute

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	planartifact "idiom/internal/contracts/planartifact"

	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/sessionid"
	"idiom/internal/workflows/recovery"
)

type RunResult struct {
	ExitCode        int
	SessionID       string
	ResumeStatePath string
	FailureKind     string
	FailureReason   string
	PlannerAnswer   string
	Completion      *plancontract.PlanExecuteTerminalCompletionRecord
	CompletionPath  string
}

func planExecuteJSONEventFailureResult(sessionID string, progressPath string, plannerAnswer string, err error) RunResult {
	reason := ""
	if isPlanExecuteJSONEventWriteFailure(err) {
		reason = err.Error()
	} else {
		reason = planExecuteJSONEventWriteError(err).Error()
	}
	return RunResult{
		ExitCode:        1,
		SessionID:       strings.TrimSpace(sessionID),
		ResumeStatePath: strings.TrimSpace(progressPath),
		FailureKind:     string(plancontract.PlanExecuteFailureJSONEventWriteFailed),
		FailureReason:   sanitizeProgressReason(reason),
		PlannerAnswer:   plannerAnswer,
	}
}

type planExecuteRunPreparation struct {
	Prompt                        string
	RequiredAcceptanceEvidenceIDs []string
	ResumeState                   *ProgressState
	ResumeStatePath               string
	SessionID                     string
}

type planExecuteRunScope struct {
	ScopeContract             plancontract.PlanScopeContract
	RequiredEditPaths         []string
	AllowedEditPaths          []string
	WriteAllowedPaths         []string
	WriteAllowedExplicit      bool
	EditSetObligations        plancontract.EditSetObligations
	CandidateRepairs          []candidatePathRepair
	PlannerPromptSource       string
	AuthorizedNewFiles        []string
	VerificationPlan          VerificationPlan
	VerificationCommands      []string
	PromptRequiredEditPaths   []string
	ExplicitRequiredEditPaths []string
}

type planExecuteRunEventEmitter func(plancontract.PlanExecuteEventType, map[string]any) (RunResult, bool)

type loadedPlanExecutionInput struct {
	Prompt                    string
	PlanPath                  string
	Plan                      planartifact.PlanArtifact
	Progress                  ProgressState
	ProgressPath              string
	Scope                     planExecuteRunScope
	ExecutorRequiredEditPaths []string
	PlannerAnswer             string
	PromptRunner              PromptRunner
	Recorder                  *telemetry.Recorder
	EmitRunJSONEvent          planExecuteRunEventEmitter
}

func preparePlanExecuteRun(cfg *config.Config, opts *Options, stderr io.Writer) (planExecuteRunPreparation, RunResult, bool) {
	if modelOverride := strings.TrimSpace(opts.Model); modelOverride != "" {
		cfg.LlamaServer.Model = modelOverride
		opts.UseActiveModel = true
	}

	prompt, err := planExecutePrompt(*opts)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return planExecuteRunPreparation{}, RunResult{ExitCode: 1}, true
	}

	requiredAcceptanceEvidenceIDs := plancontract.RequiredAcceptanceEvidenceIDsFromPrompt(prompt)
	resumeState, resumeStatePath, failedResult, failed := loadPlanExecuteResumeState(cfg.Workspace.Root, *opts, requiredAcceptanceEvidenceIDs, stderr)
	if failed {
		return planExecuteRunPreparation{}, failedResult, true
	}

	sessionID := planExecuteSessionID(*opts, resumeState)
	cfg.Workspace.SessionID = sessionID

	return planExecuteRunPreparation{
		Prompt:                        prompt,
		RequiredAcceptanceEvidenceIDs: requiredAcceptanceEvidenceIDs,
		ResumeState:                   resumeState,
		ResumeStatePath:               resumeStatePath,
		SessionID:                     sessionID,
	}, RunResult{}, false
}

func loadPlanExecuteResumeState(root string, opts Options, requiredAcceptanceEvidenceIDs []string, stderr io.Writer) (*ProgressState, string, RunResult, bool) {
	if strings.TrimSpace(opts.Resume) == "" {
		return nil, "", RunResult{}, false
	}

	state, statePath, found, err := TryLoadProgressState(root, opts.Resume)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return nil, "", RunResult{ExitCode: 1}, true
	}

	if !found {
		fmt.Fprintln(stderr, fmt.Errorf("plan-execute progress state %s does not exist", statePath))

		return nil, statePath, RunResult{ExitCode: 1, ResumeStatePath: statePath}, true
	}

	if len(state.RequiredAcceptanceEvidenceIDs) == 0 {
		state.RequiredAcceptanceEvidenceIDs = requiredAcceptanceEvidenceIDs
	}

	return &state, statePath, RunResult{}, false
}

func runLoadedPlanExecution(ctx context.Context, cfg config.Config, opts Options, input loadedPlanExecutionInput, stdout io.Writer, stderr io.Writer, deps Dependencies) RunResult {
	deps = normalizeDependencies(deps)
	ctx = planExecuteContext(ctx)

	progress := input.Progress
	progressPath := strings.TrimSpace(input.ProgressPath)
	scope := input.Scope
	plan := planartifact.NormalizeLoadedPlanArtifact(input.Plan)
	planPath := strings.TrimSpace(input.PlanPath)
	answer := input.PlannerAnswer
	emitPhase := opts.OnPhase
	emitRunJSONEvent := input.EmitRunJSONEvent
	if emitRunJSONEvent == nil {
		emitRunJSONEvent = func(plancontract.PlanExecuteEventType, map[string]any) (RunResult, bool) {
			return RunResult{}, false
		}
	}

	changedPaths := planChangedPaths(plan)
	planSourceSatisfiedPaths, planSourceSatisfiedEvidence := planExecutePlanSourceSatisfiedTerminalEvidence(scope.RequiredEditPaths, plan)

	scope.VerificationPlan = NewVerificationPlanner(cfg.Workspace.Root).Plan(VerificationPlannerInput{
		WorkspaceRoot:    cfg.Workspace.Root,
		Prompt:           input.Prompt,
		ExplicitCommands: opts.VerificationPlanExplicitCommands,
		ChangedPaths:     changedPaths,
	})
	scope.VerificationCommands = scope.VerificationPlan.CommandStrings()

	if reason := scope.EditSetObligations.RejectionReason(changedPaths); reason != "" {
		message := plancontract.EditSetObligationFailureMessage(reason)
		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlannerPlanRejected, map[string]any{
			plancontract.PlanExecuteEventFieldPath:                   planPath,
			plancontract.PlanExecuteEventFieldReason:                 string(reason),
			plancontract.PlanExecuteEventFieldChangedPaths:           changedPaths,
			plancontract.PlanExecuteEventFieldEditSetObligations:     scope.EditSetObligations,
			plancontract.PlanExecuteEventFieldEditSetObligationCount: len(scope.EditSetObligations),
		}); failed {
			return result
		}
		progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, ReviewRepairBaseline{})

		progress.Status = plancontract.PlanExecuteProgressStatusFailed
		progress.FailureKind = reason
		progress.FailureReason = sanitizeProgressReason(message)

		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}

			fmt.Fprintln(stderr, saveErr)
		}

		fmt.Fprintln(stderr, message)

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(reason), FailureReason: sanitizeProgressReason(message), PlannerAnswer: answer, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	if emitPhase != nil {
		emitPhase("planning", fmt.Sprintf("Plan created with %d edit(s)", len(plan.Edits)))
	}

	if opts.OnPlanApproval == nil {
		if mismatch := preflightPlanMissingPathAuthorization(cfg.Workspace.Root, scope.AuthorizedNewFiles, plan); mismatch != nil {
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlannerPlanRejected, map[string]any{
				plancontract.PlanExecuteEventFieldPath:   planPath,
				plancontract.PlanExecuteEventFieldReason: mismatch.Reason,
				plancontract.PlanExecuteEventFieldEdit:   mismatch.Path,
			}); failed {
				return result
			}
			progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, ReviewRepairBaseline{})

			progress.Status = plancontract.PlanExecuteProgressStatusFailed
			progress.FailureKind = plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth
			progress.FailureReason = sanitizeProgressReason(mismatch.Reason)

			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}

				fmt.Fprintln(stderr, saveErr)
			}

			fmt.Fprintln(stderr, mismatch.Reason)

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(progress.FailureKind), FailureReason: sanitizeProgressReason(mismatch.Reason), PlannerAnswer: answer, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
		}
	}

	reviewBaseline := CaptureReviewRepairBaseline(cfg.Workspace.Root, changedPaths)
	progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, reviewBaseline)

	if savedPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); err != nil {
		if savedPath != "" {
			progressPath = savedPath
		}

		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: progress.SessionID, ResumeStatePath: progressPath, FailureReason: err.Error()}
	} else if savedPath != "" {
		progressPath = savedPath
	}

	if emitPhase != nil {
		emitPhase("approval", fmt.Sprintf("Awaiting approval for %d planned edit path(s)", len(changedPaths)))
	}

	approvedPlan, approvalErr := approvePlanForExecution(ctx, planPath, plan, PlanApprovalPhaseInitial, opts, changedPaths)
	if approvalErr != nil {
		return rejectPlanExecuteApproval(cfg.Workspace.Root, progress, progressPath, opts, stdout, stderr, approvalErr.Error())
	}
	if err := persistApprovedPlanForExecution(planPath, approvedPlan); err != nil {
		return rejectPlanExecuteApproval(cfg.Workspace.Root, progress, progressPath, opts, stdout, stderr, "plan edit approval unavailable: "+err.Error())
	}

	plan = approvedPlan
	executorAuthorizedNewFiles := approvedPlanAuthorizedNewFiles(cfg.Workspace.Root, scope.AuthorizedNewFiles, plan)

	savedPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
	if err != nil {
		if savedPath != "" {
			progressPath = savedPath
		}
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: progress.SessionID, ResumeStatePath: progressPath}
	}
	if emitPhase != nil {
		emitPhase("executing", fmt.Sprintf("Applying %d edit(s)...", len(plan.Edits)))
	}

	var progressSaveErr error

	progressCallback := func(step, total int, path string, succeeded bool) {
		progress.MarkEditProgress(step, total, path, succeeded)

		if _, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); err != nil && progressSaveErr == nil {
			progressSaveErr = err
		}

		if opts.OnEditProgress != nil {
			opts.OnEditProgress(step, total, path, succeeded)
		}
	}
	report, runErr := RunExecutorPhaseWithDependencies(cfg, plan, ExecutorPhaseOptions{
		Context:              ctx,
		RequiredEditPaths:    input.ExecutorRequiredEditPaths,
		AllowedEditPaths:     scope.AllowedEditPaths,
		WriteAllowedPaths:    scope.WriteAllowedPaths,
		WriteAllowedExplicit: scope.WriteAllowedExplicit,
		AuthorizedNewFiles:   executorAuthorizedNewFiles,
		VerificationPlan:     &scope.VerificationPlan,
		JSONEvents:           opts.JSONEvents,
		Stdout:               stdout,
		Progress:             []EditProgressFunc{progressCallback},
	}, deps)

	if runErr == nil && progressSaveErr != nil {
		runErr = progressSaveErr
	}

	sourceSanityRepairRequired := ExecutorSourceSanityFailed(report, runErr)
	verificationRepairRequired := ExecutorVerificationFailed(report, runErr)
	progress.MarkExecutorComplete(report, runErr)

	progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved, (sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair)
	if err != nil && runErr == nil {
		runErr = err
	}

	if sourceSanityRepairRequired {
		if emitPhase != nil {
			emitPhase("reviewing", "Source sanity failed; recovery evidence is being prepared...")
		}

		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventExecutorVerificationRepairNeeded, map[string]any{
			plancontract.PlanExecuteEventFieldFailureKind: string(plancontract.PlanExecuteFailureSourceSanityFailed),
			plancontract.PlanExecuteEventFieldReason:      sanitizeProgressReason(runErr.Error()),
		}); failed {
			return result
		}
	} else if verificationRepairRequired {
		if emitPhase != nil {
			emitPhase("reviewing", "Verification failed; recovery evidence is being prepared...")
		}

		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventExecutorVerificationRepairNeeded, map[string]any{
			plancontract.PlanExecuteEventFieldFailureKind:             string(plancontract.PlanExecuteFailureVerificationFailed),
			plancontract.PlanExecuteEventFieldReason:                  sanitizeProgressReason(runErr.Error()),
			plancontract.PlanExecuteTerminalFieldVerificationCommands: scope.VerificationCommands,
		}); failed {
			return result
		}
	}

	reviewReport := ReviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}

	var reviewErr error

	if (runErr == nil || sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair {
		if emitPhase != nil {
			emitPhase("reviewing", "Verifying changes...")
		}

		reviewSessionID := sessionid.New()
		progress.MarkReviewStarted(reviewSessionID)

		_, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
		if err != nil {
			reviewErr = err
		} else {
			reviewOpts := opts

			reviewOpts.ReviewSessionID = reviewSessionID
			reviewReport, reviewErr = RunReviewRepairPhaseWithDependencies(cfg, input.Prompt, plan, scope.RequiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, input.PromptRunner, input.Recorder, deps)
		}

		progress.MarkReviewComplete(reviewReport, reviewErr)

		if (sourceSanityRepairRequired || verificationRepairRequired) && reviewErr == nil {
			if ReviewRepairResolvedVerificationFailure(reviewReport) {
				failureKind, refreshErr := refreshPlanExecuteExecutorEvidenceAfterReviewRepair(ctx, cfg, plan, scope.VerificationPlan, &progress, &report, opts.JSONEvents, stdout, deps)
				runErr = refreshErr
				markPlanExecuteEvidenceRefreshOutcome(&progress, failureKind, refreshErr)
				if refreshErr == nil {
					verificationRepairRequired = false
					sourceSanityRepairRequired = false
				}
			} else {
				progress.Status = plancontract.PlanExecuteProgressStatusFailed
				if verificationRepairRequired {
					progress.FailureKind = plancontract.PlanExecuteFailureVerificationFailed
				} else {
					progress.FailureKind = plancontract.PlanExecuteFailureSourceSanityFailed
				}

				progress.FailureReason = sanitizeProgressReason(runErr.Error())
			}
		}

		progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
		if err != nil && reviewErr == nil {
			reviewErr = err
		}
	} else if runErr == nil && opts.SkipReviewRepair {
		progress.MarkSkippedReview()

		progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
		if err != nil {
			runErr = err
		}
	}

	if emitPhase != nil {
		emitPhase("complete", "")
	}

	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, report)
		fmt.Fprintln(stdout, reviewReport)
	}

	if runErr != nil {
		fmt.Fprintln(stderr, runErr)

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(progress.FailureKind), FailureReason: progress.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(progress.FailureKind), FailureReason: progress.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	if progress.Status == plancontract.PlanExecuteProgressStatusBlocked {
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(progress.FailureKind), FailureReason: progress.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	exitCode := 0
	if progress.Status == plancontract.PlanExecuteProgressStatusDegraded {
		exitCode = 1
	}

	return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: exitCode, FailureKind: string(progress.FailureKind), FailureReason: progress.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence, AcceptanceEvidenceIDs: planExecuteHarnessAcceptanceEvidenceIDs(progress)})
}

func planExecuteSessionID(opts Options, resumeState *ProgressState) string {
	sessionID := strings.TrimSpace(opts.SessionID)
	if resumeState != nil && strings.TrimSpace(resumeState.SessionID) != "" {
		sessionID = resumeState.SessionID
	}

	if sessionID == "" {
		sessionID = sessionid.New()
	}

	return sessionID
}

func preparePlanExecuteRunScope(root string, opts *Options, prompt string) planExecuteRunScope {
	scopeContract := planExecuteScopeContract(*opts, prompt)
	requiredEditPaths := scopeContract.RequiredCoveragePaths
	allowedEditPaths := scopeContract.AllowedCandidatePaths
	writeAllowedPaths := scopeContract.WriteAllowedPaths
	writeAllowedExplicit := scopeContract.WriteAllowedExplicit
	editSetObligations := scopeContract.EditSetObligations
	allowedEditPaths, candidateRepairs := repairMissingAllowedCandidatePaths(root, allowedEditPaths)
	if len(candidateRepairs) > 0 {
		scopeContract = planExecuteContractWithWriteEnvelope(plancontract.NewPlanScopeContract(requiredEditPaths, allowedEditPaths, editSetObligations), writeAllowedPaths, writeAllowedExplicit)
	}

	plannerPromptSource := applyCandidatePathRepairsToPrompt(prompt, candidateRepairs)
	authorizedNewFiles := mergePlanExecutePaths(opts.AuthorizedNewFiles)
	verificationPlan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot:    root,
		Prompt:           plannerPromptSource,
		ExplicitCommands: opts.VerificationPlanExplicitCommands,
	})

	opts.ScopeContract = scopeContract
	opts.EditSetObligations = editSetObligations
	opts.AuthorizedNewFiles = authorizedNewFiles

	return planExecuteRunScope{
		ScopeContract:             scopeContract,
		RequiredEditPaths:         requiredEditPaths,
		AllowedEditPaths:          allowedEditPaths,
		WriteAllowedPaths:         writeAllowedPaths,
		WriteAllowedExplicit:      writeAllowedExplicit,
		EditSetObligations:        editSetObligations,
		CandidateRepairs:          candidateRepairs,
		PlannerPromptSource:       plannerPromptSource,
		AuthorizedNewFiles:        authorizedNewFiles,
		VerificationPlan:          verificationPlan,
		VerificationCommands:      verificationPlan.CommandStrings(),
		PromptRequiredEditPaths:   plancontract.RequiredEditPathsFromPrompt(prompt),
		ExplicitRequiredEditPaths: plancontract.UniqueScopePaths([]string(opts.RequiredEditPaths)),
	}
}

func emitPlanExecuteRunScopeResolved(scope planExecuteRunScope, rec *telemetry.Recorder, emit planExecuteRunEventEmitter) (RunResult, bool) {
	telemetry.LogPhase(rec, "plan_execute_required_edit_paths", map[string]any{
		plancontract.PlanExecuteEventFieldCount:                  len(scope.RequiredEditPaths),
		plancontract.PlanExecuteEventFieldExplicitCount:          len(scope.ExplicitRequiredEditPaths),
		plancontract.PlanExecuteEventFieldPromptCount:            len(scope.PromptRequiredEditPaths),
		plancontract.PlanExecuteEventFieldAllowedCount:           len(scope.AllowedEditPaths),
		plancontract.PlanExecuteEventFieldCandidateRepairs:       scope.CandidateRepairs,
		plancontract.PlanExecuteEventFieldScopeContractID:        scope.ScopeContract.ContractID,
		plancontract.PlanExecuteEventFieldEditSetObligationCount: len(scope.EditSetObligations),
	})

	return emit(plancontract.PlanExecuteEventRequiredEditPathsResolved, map[string]any{
		plancontract.PlanExecuteEventFieldCount:                   len(scope.RequiredEditPaths),
		plancontract.PlanExecuteEventFieldExplicitCount:           len(scope.ExplicitRequiredEditPaths),
		plancontract.PlanExecuteEventFieldPromptCount:             len(scope.PromptRequiredEditPaths),
		plancontract.PlanExecuteEventFieldAllowedCount:            len(scope.AllowedEditPaths),
		plancontract.PlanExecuteEventFieldRequiredEditPaths:       scope.RequiredEditPaths,
		plancontract.PlanExecuteEventFieldAllowedEditPaths:        scope.AllowedEditPaths,
		plancontract.PlanExecuteEventFieldCandidateRepairs:        scope.CandidateRepairs,
		plancontract.PlanExecuteTerminalFieldVerificationCommands: scope.VerificationCommands,
		plancontract.PlanExecuteEventFieldScopeContractID:         scope.ScopeContract.ContractID,
		plancontract.PlanExecuteEventFieldEditSetObligations:      scope.EditSetObligations,
		plancontract.PlanExecuteEventFieldEditSetObligationCount:  len(scope.EditSetObligations),
	})
}

func refreshPlanExecuteExecutorEvidenceAfterReviewRepair(ctx context.Context, cfg config.Config, plan planartifact.PlanArtifact, verificationPlan VerificationPlan, state *ProgressState, report *ExecutorPhaseReport, jsonEvents bool, stdout io.Writer, deps Dependencies) (string, error) {
	deps = normalizeDependencies(deps)
	ctx = planExecuteContext(ctx)
	_, sourceEvidence, sourceErr := runPlanExecuteSourceSanity(ctx, cfg.Workspace, cfg.Workspace.Root, planChangedPaths(plan), jsonEvents, stdout, deps.SourceSanity, deps.SourceSanityCovers)

	report.SourceSanity = sourceEvidence
	state.Executor.SourceSanity = append([]plancontract.PhaseEvidenceCheck(nil), sourceEvidence...)

	if sourceErr != nil {
		return string(plancontract.PlanExecuteFailureSourceSanityFailed), sourceErr
	}

	_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlanWithParentContract(ctx, cfg.Workspace.Root, verificationPlan, state.ScopeContractID, jsonEvents, stdout)

	report.Verification = verificationEvidence
	state.Executor.Verification = append([]plancontract.PhaseEvidenceCheck(nil), verificationEvidence...)
	report.VerificationCommands = append([]plancontract.VerificationCommand(nil), verificationPlan.Commands...)
	report.VerificationSkips = append([]plancontract.VerificationSkip(nil), verificationPlan.Skips...)
	report.VerificationRejects = append([]plancontract.VerificationReject(nil), verificationPlan.Rejects...)
	state.Executor.VerificationCommands = append([]plancontract.VerificationCommand(nil), verificationPlan.Commands...)
	state.Executor.VerificationSkips = append([]plancontract.VerificationSkip(nil), verificationPlan.Skips...)
	state.Executor.VerificationRejects = append([]plancontract.VerificationReject(nil), verificationPlan.Rejects...)

	if verificationErr != nil {
		return string(plancontract.PlanExecuteFailureVerificationFailed), verificationErr
	}

	return "", nil
}

func markPlanExecuteEvidenceRefreshOutcome(state *ProgressState, failureKind string, err error) {
	if err != nil {
		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = plancontract.PlanExecuteFailureKind(failureKind)
		state.FailureReason = sanitizeProgressReason(err.Error())
		return
	}

	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.FailureKind = ""
	state.FailureReason = ""
}

type planExecuteSourceSatisfiedRejectionInput struct {
	FailureKind                  string
	FailureReason                string
	UnsupportedEvidenceMarker    string
	SourceSatisfiedEvidencePaths []string
	MissingRequiredEditPaths     []string
}

func rejectPlanExecuteSourceSatisfied(root string, progress *ProgressState, progressPath *string, jsonEvents bool, stdout io.Writer, stderr io.Writer, emit planExecuteRunEventEmitter, scope planExecuteRunScope, input planExecuteSourceSatisfiedRejectionInput) RunResult {
	progress.MarkPlanningFailed(input.FailureKind, input.FailureReason)

	if savedPath, saveErr := saveRunProgressState(root, progress, jsonEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
		if savedPath != "" && progressPath != nil {
			*progressPath = savedPath
		}

		fmt.Fprintln(stderr, saveErr)
	}

	fields := map[string]any{
		plancontract.PlanExecuteEventFieldReason:                          input.FailureKind,
		plancontract.PlanExecuteEventFieldRequiredEditPaths:               scope.RequiredEditPaths,
		plancontract.PlanExecuteEventFieldAllowedEditPaths:                scope.AllowedEditPaths,
		plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths: input.SourceSatisfiedEvidencePaths,
		plancontract.PlanExecuteEventFieldMissingEditSetObligations:       scope.EditSetObligations.MissingOpenObligations(input.SourceSatisfiedEvidencePaths),
		plancontract.PlanExecuteEventFieldMissingRequiredEditPaths:        input.MissingRequiredEditPaths,
		plancontract.PlanExecuteEventFieldEditSetObligations:              scope.EditSetObligations,
		plancontract.PlanExecuteEventFieldEditSetObligationCount:          len(scope.EditSetObligations),
	}
	if input.UnsupportedEvidenceMarker != "" {
		fields["unsupported_evidence_marker"] = input.UnsupportedEvidenceMarker
	}

	if result, failed := emit(plancontract.PlanExecuteEventSourceSatisfiedRejected, fields); failed {
		return result
	}

	fmt.Fprintln(stderr, input.FailureReason)

	progressPathValue := ""
	if progressPath != nil {
		progressPathValue = *progressPath
	}

	return finalizePlanExecuteRunResult(root, *progress, progressPathValue, jsonEvents, stdout, planExecuteRunResultInput{
		ExitCode:                     1,
		FailureKind:                  input.FailureKind,
		FailureReason:                sanitizeProgressReason(input.FailureReason),
		SourceSatisfiedEvidencePaths: input.SourceSatisfiedEvidencePaths,
	})
}

func RunWithDependencies(cfg config.Config, opts Options, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, deps Dependencies) RunResult {
	deps = normalizeDependencies(deps)
	ctx := planExecuteContext(opts.Context)
	defer deps.CleanupPlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)

	deps.CleanupPlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)

	sessionStart := time.Now().Add(-1 * time.Second)

	prep, failedResult, failed := preparePlanExecuteRun(&cfg, &opts, stderr)
	if failed {
		return failedResult
	}

	emitRunJSONEvent := func(eventType plancontract.PlanExecuteEventType, fields map[string]any) (RunResult, bool) {
		if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, eventType, fields); err != nil {
			result := planExecuteJSONEventFailureResult(prep.SessionID, prep.ResumeStatePath, "", err)
			if stderr != nil {
				fmt.Fprintln(stderr, result.FailureReason)
			}
			return result, true
		}
		return RunResult{}, false
	}

	telemetry.LogPhase(rec, "plan_execute_start", map[string]any{
		"json_events": opts.JSONEvents,
		plancontract.PlanExecuteEventFieldSessionID: prep.SessionID,
		"resume": prep.ResumeState != nil,
	})

	queueResult := deps.AcquireQueueSlot(cfg, QueueAcquireOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		SessionID:  prep.SessionID,
	}, rec, stdout, stderr)
	defer queueResult.Release()

	if !queueResult.Allowed {
		reason := sanitizeProgressReason(fmt.Sprintf("queue admission required but unavailable: state=%s pool=%s reason=%s", queueResult.State, queueResult.Pool, queueResult.Reason))
		if stderr != nil {
			fmt.Fprintln(stderr, reason)
		}
		return RunResult{
			ExitCode:      1,
			SessionID:     prep.SessionID,
			FailureKind:   string(plancontract.PlanExecuteFailurePlannerBlocked),
			FailureReason: reason,
		}
	}

	scope := preparePlanExecuteRunScope(cfg.Workspace.Root, &opts, prep.Prompt)
	if result, failed := emitPlanExecuteRunScopeResolved(scope, rec, emitRunJSONEvent); failed {
		return result
	}

	decision := deps.BuildWriterRoute(WriterRouteDecisionInput{
		Config:            cfg,
		Options:           opts,
		RequiredEditPaths: scope.RequiredEditPaths,
	})
	if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventWriterRouteDecision, decision.EventFields()); failed {
		return result
	}

	if prep.ResumeState != nil {
		return runResumeReviewRepair(cfg, opts, prep.Prompt, *prep.ResumeState, prep.ResumeStatePath, promptRun, rec, stdout, stderr, deps)
	}

	progress := NewProgressState(prep.SessionID, prep.Prompt, opts.PromptFile, scope.RequiredEditPaths, scope.AllowedEditPaths, time.Now().UTC())

	progress.RequiredAcceptanceEvidenceIDs = prep.RequiredAcceptanceEvidenceIDs
	progress.SetWriteAllowedEnvelope(scope.WriteAllowedPaths, scope.WriteAllowedExplicit)
	progress.SetScopeContractID(scope.ScopeContract.ContractID)

	progress.EditSetObligations = scope.EditSetObligations

	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: prep.SessionID}
	}

	plannerBrief := appendPlanExecuteScopeToBriefBody(scope.PlannerPromptSource, scope.RequiredEditPaths, scope.AllowedEditPaths)
	contextPackSection := BuildPromptContextPackSectionWithRunner(cfg, PromptContextPackOptions{
		Context:           ctx,
		Mode:              "plan",
		Title:             scope.PlannerPromptSource,
		Brief:             plannerBrief,
		ProjectRoot:       cfg.Workspace.Root,
		RequiredEditPaths: scope.RequiredEditPaths,
	}, stderr, deps.RunPromptContextTool)

	plannerBrief = ComposePlannerPromptWithContextPack(plannerBrief, contextPackSection)

	plannerPrompt := PlanExecutePlannerSystemAddendumWithObligations(scope.RequiredEditPaths, scope.AllowedEditPaths, scope.EditSetObligations) + "\n\n" + plannerBrief
	plannerMaxTokens := 16384

	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}

	answer := ""
	emitPhase := opts.OnPhase
	executorRequiredEditPaths := scope.RequiredEditPaths
	autoExactScopePaths := scope.RequiredEditPaths

	if len(autoExactScopePaths) == 0 {
		autoExactScopePaths = scope.AllowedEditPaths
	}

	autoExact := planExecuteTryAutoExactPlan(ctx, cfg, scope.PlannerPromptSource, autoExactScopePaths, deps.SubmitAutoPlan)
	if autoExact.SourceSatisfied {
		if reason := scope.EditSetObligations.RejectionReason(autoExact.AppliedPaths); reason != "" {
			message := plancontract.EditSetObligationFailureMessage(reason)
			progress.MarkPlanningFailed(string(reason), message)

			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}
				fmt.Fprintln(stderr, saveErr)
				return planExecuteJSONEventFailureResult(prep.SessionID, progressPath, answer, saveErr)
			}
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventSourceSatisfiedRejected, map[string]any{
				plancontract.PlanExecuteEventFieldReason:                          string(reason),
				plancontract.PlanExecuteEventFieldAppliedPaths:                    autoExact.AppliedPaths,
				plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths: autoExact.AppliedPaths,
				plancontract.PlanExecuteEventFieldMissingEditSetObligations:       scope.EditSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
				plancontract.PlanExecuteEventFieldMissingRequiredEditPaths:        plancontract.SourceSatisfiedMissingCoveragePaths(planExecuteAutoSourceSatisfiedAnswer(autoExactScopePaths), scope.RequiredEditPaths),
				plancontract.PlanExecuteEventFieldEditSetObligations:              scope.EditSetObligations,
				plancontract.PlanExecuteEventFieldEditSetObligationCount:          len(scope.EditSetObligations),
			}); failed {
				return result
			}
			fmt.Fprintln(stderr, message)

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(reason), FailureReason: sanitizeProgressReason(message)})
		}

		answer = planExecuteAutoSourceSatisfiedAnswer(autoExactScopePaths)
		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}
			fmt.Fprintln(stderr, saveErr)
			return planExecuteJSONEventFailureResult(prep.SessionID, progressPath, answer, saveErr)
		}
		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventAutoExactPlanSourceSatisfied, map[string]any{
			plancontract.PlanExecuteEventFieldEdits:                           autoExact.EditCount,
			plancontract.PlanExecuteEventFieldApplied:                         autoExact.AppliedCount,
			plancontract.PlanExecuteEventFieldRequiredEditPaths:               scope.RequiredEditPaths,
			plancontract.PlanExecuteEventFieldAllowedEditPaths:                scope.AllowedEditPaths,
			plancontract.PlanExecuteEventFieldEditSetObligations:              scope.EditSetObligations,
			plancontract.PlanExecuteEventFieldEditSetObligationCount:          len(scope.EditSetObligations),
			plancontract.PlanExecuteEventFieldAppliedPaths:                    autoExact.AppliedPaths,
			plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths: autoExact.AppliedPaths,
			plancontract.PlanExecuteEventFieldMissingEditSetObligations:       scope.EditSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
			plancontract.PlanExecuteEventFieldMissingRequiredEditPaths:        plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
			plancontract.PlanExecuteEventFieldReason:                          autoExact.Reason,
		}); failed {
			return result
		}
		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventSourceSatisfied, map[string]any{
			plancontract.PlanExecuteEventFieldRequiredEditPaths:               scope.RequiredEditPaths,
			plancontract.PlanExecuteEventFieldAllowedEditPaths:                scope.AllowedEditPaths,
			plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths: autoExact.AppliedPaths,
			plancontract.PlanExecuteEventFieldMissingEditSetObligations:       scope.EditSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
			plancontract.PlanExecuteEventFieldMissingRequiredEditPaths:        plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
			plancontract.PlanExecuteEventFieldEditSetObligations:              scope.EditSetObligations,
			plancontract.PlanExecuteEventFieldEditSetObligationCount:          len(scope.EditSetObligations),
			plancontract.PlanExecuteEventFieldReason:                          autoExact.Reason,
		}); failed {
			return result
		}

		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
			fmt.Fprintln(stdout, "source-satisfied: executor skipped because every exact edit already matched the current workspace.")
		}

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{
			ExitCode:                     0,
			SourceSatisfied:              true,
			SourceSatisfiedEvidencePaths: autoExact.AppliedPaths,
			SourceSatisfiedEvidence:      planExecuteHarnessObservedSourceEvidence(autoExact.AppliedPaths),
			AcceptanceEvidenceIDs:        planExecuteHarnessAcceptanceEvidenceIDs(progress),
		})
	}

	if autoExact.Submitted {
		answer = autoExact.Result.Summary
		if len(autoExact.PendingPaths) > 0 {
			executorRequiredEditPaths = autoExact.PendingPaths
		}

		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventAutoExactPlanSubmitted, map[string]any{
			plancontract.PlanExecuteEventFieldEdits:        autoExact.EditCount,
			plancontract.PlanExecuteEventFieldApplied:      autoExact.AppliedCount,
			plancontract.PlanExecuteEventFieldPending:      autoExact.PendingCount,
			plancontract.PlanExecuteEventFieldAppliedPaths: autoExact.AppliedPaths,
			plancontract.PlanExecuteEventFieldPendingPaths: autoExact.PendingPaths,
			plancontract.PlanExecuteEventFieldReason:       autoExact.Reason,
		}); failed {
			return result
		}

		if autoExact.AppliedCount > 0 {
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventAutoExactPlanPartiallySatisfied, map[string]any{
				plancontract.PlanExecuteEventFieldApplied:      autoExact.AppliedCount,
				plancontract.PlanExecuteEventFieldPending:      autoExact.PendingCount,
				plancontract.PlanExecuteEventFieldAppliedPaths: autoExact.AppliedPaths,
				plancontract.PlanExecuteEventFieldPendingPaths: autoExact.PendingPaths,
			}); failed {
				return result
			}
		}

		if emitPhase != nil {
			emitPhase("executing", fmt.Sprintf("Applied %d auto-exact edit(s)", autoExact.EditCount))
		}
	} else {
		if strings.TrimSpace(autoExact.Reason) != "" {
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventAutoExactPlanSkipped, map[string]any{
				plancontract.PlanExecuteEventFieldReason:       autoExact.Reason,
				plancontract.PlanExecuteEventFieldError:        autoExact.Result.Error,
				plancontract.PlanExecuteEventFieldApplied:      autoExact.AppliedCount,
				plancontract.PlanExecuteEventFieldPending:      autoExact.PendingCount,
				plancontract.PlanExecuteEventFieldUnknown:      autoExact.UnknownCount,
				plancontract.PlanExecuteEventFieldAppliedPaths: autoExact.AppliedPaths,
				plancontract.PlanExecuteEventFieldPendingPaths: autoExact.PendingPaths,
				plancontract.PlanExecuteEventFieldUnknownPaths: autoExact.UnknownPaths,
			}); failed {
				return result
			}
		}

		if emitPhase != nil {
			emitPhase("planning", "Analyzing objective and creating plan...")
		}

		answer, err = promptRun(cfg, plannerPrompt, RunOptions{
			JSONEvents:               opts.JSONEvents,
			Resume:                   opts.Resume,
			TaskFamily:               string(autonomy.TaskFamilyPlanThenExecutePlanner),
			PlannerOwnedVerification: true,
			MaxTokensOverride:        plannerMaxTokens,
			DisableThinking:          opts.DisableThinking,
			UseActiveModel:           opts.UseActiveModel,
			Telemetry:                rec,
			ScopeContract:            scope.ScopeContract,
			AuthorizedNewFiles:       append([]string(nil), scope.AuthorizedNewFiles...),
		}, stdout)
		if err != nil {
			progress.MarkPlanningFailed(string(plancontract.PlanExecuteFailurePlannerFailed), err.Error())

			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}

				fmt.Fprintln(stderr, saveErr)
			}

			fmt.Fprintln(stderr, err)

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(plancontract.PlanExecuteFailurePlannerFailed), FailureReason: sanitizeProgressReason(err.Error())})
		}
	}

	planPath, err := deps.FindPlanArtifact(cfg.Workspace.Root, sessionStart, prep.SessionID)
	if err != nil {
		sourceSatisfiedPaths := planExecuteSourceSatisfiedEvidencePaths(answer, scope.RequiredEditPaths, scope.AllowedEditPaths)
		if marker := planExecuteSourceSatisfiedUnsupportedEvidenceMarker(answer, sourceSatisfiedPaths); marker != "" && plancontract.SourceSatisfiedAnswerDeclaresDecision(answer) {
			message := fmt.Sprintf("%s: source-satisfied evidence includes unsupported inference marker %q; submit a grounded plan or report a typed blocker", string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence), marker)
			return rejectPlanExecuteSourceSatisfied(cfg.Workspace.Root, &progress, &progressPath, opts.JSONEvents, stdout, stderr, emitRunJSONEvent, scope, planExecuteSourceSatisfiedRejectionInput{
				FailureKind:                  string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence),
				FailureReason:                message,
				UnsupportedEvidenceMarker:    marker,
				SourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
				MissingRequiredEditPaths:     plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
			})
		}
		if plancontract.SourceSatisfiedAnswerCoversPaths(answer, sourceSatisfiedPaths) {
			if marker := planExecuteSourceSatisfiedUnsupportedEvidenceMarker(answer, sourceSatisfiedPaths); marker != "" {
				message := fmt.Sprintf("%s: source-satisfied evidence includes unsupported inference marker %q; submit a grounded plan or report a typed blocker", string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence), marker)
				return rejectPlanExecuteSourceSatisfied(cfg.Workspace.Root, &progress, &progressPath, opts.JSONEvents, stdout, stderr, emitRunJSONEvent, scope, planExecuteSourceSatisfiedRejectionInput{
					FailureKind:                  string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence),
					FailureReason:                message,
					UnsupportedEvidenceMarker:    marker,
					SourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
					MissingRequiredEditPaths:     plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
				})
			}

			if reason := scope.EditSetObligations.RejectionReason(sourceSatisfiedPaths); reason != "" {
				message := plancontract.EditSetObligationFailureMessage(reason)
				return rejectPlanExecuteSourceSatisfied(cfg.Workspace.Root, &progress, &progressPath, opts.JSONEvents, stdout, stderr, emitRunJSONEvent, scope, planExecuteSourceSatisfiedRejectionInput{
					FailureKind:                  string(reason),
					FailureReason:                message,
					SourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
					MissingRequiredEditPaths:     plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
				})
			}

			progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
			progress.Status = plancontract.PlanExecuteProgressStatusCompleted
			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}
				fmt.Fprintln(stderr, saveErr)
				return RunResult{ExitCode: 1, SessionID: prep.SessionID, ResumeStatePath: progressPath, FailureReason: saveErr.Error()}
			}
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventSourceSatisfied, map[string]any{
				plancontract.PlanExecuteEventFieldRequiredEditPaths:               scope.RequiredEditPaths,
				plancontract.PlanExecuteEventFieldAllowedEditPaths:                scope.AllowedEditPaths,
				plancontract.PlanExecuteTerminalFieldSourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
				plancontract.PlanExecuteEventFieldMissingEditSetObligations:       scope.EditSetObligations.MissingOpenObligations(sourceSatisfiedPaths),
				plancontract.PlanExecuteEventFieldMissingRequiredEditPaths:        plancontract.SourceSatisfiedMissingCoveragePaths(answer, scope.RequiredEditPaths),
				plancontract.PlanExecuteEventFieldEditSetObligations:              scope.EditSetObligations,
				plancontract.PlanExecuteEventFieldEditSetObligationCount:          len(scope.EditSetObligations),
				plancontract.PlanExecuteEventFieldReason:                          "planner reported requested final state already present",
			}); failed {
				return result
			}

			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
				fmt.Fprintln(stdout, "")
				fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
				fmt.Fprintln(stdout, "source-satisfied: executor skipped because the planner reported the requested final state already present in every required path.")
			}

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{
				ExitCode:                     0,
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
				AcceptanceEvidenceIDs:        planExecuteHarnessAcceptanceEvidenceIDs(progress),
			})
		}

		if planExecuteAnswerDeclaresBlocker(answer) {
			progress.MarkPlanningFailed(string(plancontract.PlanExecuteFailurePlannerBlocked), planExecuteBlockerFailureReason(answer))

			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}

				fmt.Fprintln(stderr, saveErr)
			}

			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlannerBlocked, map[string]any{
				plancontract.PlanExecuteEventFieldReason:  err.Error(),
				plancontract.PlanExecuteEventFieldBlocker: planExecuteBlockerSummary(answer),
			}); failed {
				return result
			}
			fmt.Fprintln(stderr, fmt.Errorf("planner phase reported a blocker before submitting a plan: %s", planExecuteBlockerSummary(answer)))

			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
			}

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(plancontract.PlanExecuteFailurePlannerBlocked), FailureReason: planExecuteBlockerFailureReason(answer), PlannerAnswer: answer})
		}

		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactMissing, map[string]any{
			plancontract.PlanExecuteEventFieldReason: err.Error(),
		}); failed {
			return result
		}
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))

		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}

		progress.MarkPlanningFailed(string(plancontract.PlanExecuteFailurePlanArtifactMissing), err.Error())

		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}

			fmt.Fprintln(stderr, saveErr)
		}

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(plancontract.PlanExecuteFailurePlanArtifactMissing), FailureReason: sanitizeProgressReason(err.Error()), PlannerAnswer: answer})
	}

	plan, err := deps.LoadPlanArtifact(planPath)
	if err != nil {
		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactInvalid, map[string]any{
			plancontract.PlanExecuteEventFieldPath:   planPath,
			plancontract.PlanExecuteEventFieldReason: err.Error(),
		}); failed {
			return result
		}
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		progress.MarkPlanningFailed(string(plancontract.PlanExecuteFailurePlanArtifactInvalid), err.Error())

		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}

			fmt.Fprintln(stderr, saveErr)
		}

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(plancontract.PlanExecuteFailurePlanArtifactInvalid), FailureReason: sanitizeProgressReason(err.Error())})
	}

	if inheritedPackID := inheritPlanExecutePromptPackID(&plan, plannerPrompt); inheritedPackID != "" {
		if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactPackInherited, map[string]any{
			plancontract.PlanExecuteEventFieldPath:   planPath,
			plancontract.PlanExecuteEventFieldPackID: inheritedPackID,
		}); failed {
			return result
		}

		if err := patchPlanArtifactPackID(planPath, inheritedPackID); err != nil {
			if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactPackInheritFailed, map[string]any{
				plancontract.PlanExecuteEventFieldPath:   planPath,
				plancontract.PlanExecuteEventFieldPackID: inheritedPackID,
				plancontract.PlanExecuteEventFieldReason: err.Error(),
			}); failed {
				return result
			}
		}
	}

	if result, failed := emitRunJSONEvent(plancontract.PlanExecuteEventPlanArtifactLoaded, map[string]any{
		plancontract.PlanExecuteEventFieldPath:   planPath,
		plancontract.PlanExecuteEventFieldEdits:  len(plan.Edits),
		plancontract.PlanExecuteEventFieldPackID: strings.TrimSpace(plan.PackID),
	}); failed {
		return result
	}

	_ = sessionStart

	return runLoadedPlanExecution(ctx, cfg, opts, loadedPlanExecutionInput{
		Prompt:                    prep.Prompt,
		PlanPath:                  planPath,
		Plan:                      plan,
		Progress:                  progress,
		ProgressPath:              progressPath,
		Scope:                     scope,
		ExecutorRequiredEditPaths: executorRequiredEditPaths,
		PlannerAnswer:             answer,
		PromptRunner:              promptRun,
		Recorder:                  rec,
		EmitRunJSONEvent:          emitRunJSONEvent,
	}, stdout, stderr, deps)
}

func runResumeReviewRepair(cfg config.Config, opts Options, prompt string, state ProgressState, statePath string, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, deps Dependencies) RunResult {
	deps = normalizeDependencies(deps)
	sessionID := strings.TrimSpace(state.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(opts.Resume)
	}

	if !state.PromptMatches(prompt) {
		err := fmt.Errorf("plan-execute resume state %s was created for a different prompt; pass the same --prompt or --prompt-file used by session %s", statePath, sessionID)
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}

	if !state.ReviewResumeReady() {
		err := fmt.Errorf("plan-execute resume state %s is not review-resumable (phase=%s status=%s executor_status=%s)", statePath, state.Phase, state.Status, state.Executor.Status)
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}

	planPath := state.ResolvedPlanPath(cfg.Workspace.Root)

	plan, err := deps.LoadPlanArtifact(planPath)
	if err != nil {
		fmt.Fprintln(stderr, fmt.Errorf("read resumed plan artifact %q: %w", planPath, err))

		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}

	scopeContract := planExecuteScopeContract(opts, prompt)
	requiredEditPaths := mergeProgressPaths(state.RequiredEditPaths, scopeContract.RequiredCoveragePaths)
	allowedEditPaths := mergeProgressPaths(state.AllowedEditPaths, scopeContract.AllowedCandidatePaths)
	planSourceSatisfiedPaths, planSourceSatisfiedEvidence := planExecutePlanSourceSatisfiedTerminalEvidence(requiredEditPaths, plan)

	if len(state.EditSetObligations) == 0 {
		state.EditSetObligations = scopeContract.EditSetObligations
	}

	state.EditSetObligations = state.EditSetObligations.Normalized()
	if strings.TrimSpace(state.ScopeContractID) == "" {
		state.SetScopeContractID(plancontract.NewPlanScopeContract(requiredEditPaths, allowedEditPaths, state.EditSetObligations).ContractID)
	}

	if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventResumeStateLoaded, ProgressStateEventFields(state, statePath)); err != nil {
		result := planExecuteJSONEventFailureResult(sessionID, statePath, "", err)
		fmt.Fprintln(stderr, result.FailureReason)
		return result
	}

	if opts.OnPhase != nil {
		opts.OnPhase("reviewing", "Resuming review-repair from executor progress state...")
	}

	reviewSessionID := sessionid.New()
	state.MarkReviewStarted(reviewSessionID)

	_, err = saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}

	reviewOpts := opts

	reviewOpts.ReviewSessionID = reviewSessionID
	reviewOpts.RequiredEditPaths = MultiFlag(requiredEditPaths)
	reviewOpts.AllowedEditPaths = MultiFlag(allowedEditPaths)
	reviewOpts.EditSetObligations = state.EditSetObligations

	hostWriteAllowedPaths, hostWriteAllowedExplicit := planExecuteHostWriteAllowedPaths(opts)
	writeAllowedPaths := mergeProgressPaths(state.WriteAllowedPaths, hostWriteAllowedPaths)
	writeAllowedExplicit := state.WriteAllowedExplicit || hostWriteAllowedExplicit
	reviewScopeContract := plancontract.NewPlanScopeContract(requiredEditPaths, allowedEditPaths, state.EditSetObligations)

	if writeAllowedExplicit {
		reviewScopeContract = reviewScopeContract.WithWriteAllowedPaths(writeAllowedPaths)
	}

	reviewOpts.ScopeContract = reviewScopeContract

	report := state.ExecutorPhaseReport()
	reviewBaseline := state.ReviewBaseline
	reviewReport, reviewErr := RunReviewRepairPhaseWithDependencies(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec, deps)
	state.MarkReviewComplete(reviewReport, reviewErr)

	sourceSanityResumeRepairRequired := ExecutorSourceSanityFailed(report, fmt.Errorf("source_sanity failed"))
	verificationResumeRepairRequired := ExecutorVerificationFailed(report, fmt.Errorf("verification failed"))

	if reviewErr == nil && (sourceSanityResumeRepairRequired || verificationResumeRepairRequired) && ReviewRepairResolvedVerificationFailure(reviewReport) {
		verificationPlan := NewVerificationPlanner(cfg.Workspace.Root).Plan(VerificationPlannerInput{
			WorkspaceRoot:    cfg.Workspace.Root,
			Prompt:           prompt,
			ExplicitCommands: opts.VerificationPlanExplicitCommands,
			ChangedPaths:     planChangedPaths(plan),
		})
		failureKind, refreshErr := refreshPlanExecuteExecutorEvidenceAfterReviewRepair(planExecuteContext(opts.Context), cfg, plan, verificationPlan, &state, &report, opts.JSONEvents, stdout, deps)
		if refreshErr != nil {
			reviewErr = refreshErr
		}
		markPlanExecuteEvidenceRefreshOutcome(&state, failureKind, refreshErr)

		if reviewErr != nil && state.FailureKind == "" {
			state.Status = plancontract.PlanExecuteProgressStatusFailed
			state.FailureKind = plancontract.PlanExecuteFailureVerificationFailed
			state.FailureReason = sanitizeProgressReason(reviewErr.Error())
		}
	}

	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, plancontract.PlanExecuteEventResumeStateSaved)
	if err != nil && reviewErr == nil {
		reviewErr = err
	}

	if opts.OnPhase != nil {
		opts.OnPhase("complete", "")
	}

	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, report)
		fmt.Fprintln(stdout, reviewReport)
	}

	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)

		return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(state.FailureKind), FailureReason: state.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	if state.Status == plancontract.PlanExecuteProgressStatusBlocked {
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: string(state.FailureKind), FailureReason: state.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence})
	}

	resumeExitCode := 0
	if state.Status == plancontract.PlanExecuteProgressStatusDegraded {
		resumeExitCode = 1
	}

	return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: resumeExitCode, FailureKind: string(state.FailureKind), FailureReason: state.FailureReason, SourceSatisfiedEvidencePaths: planSourceSatisfiedPaths, SourceSatisfiedEvidence: planSourceSatisfiedEvidence, AcceptanceEvidenceIDs: planExecuteHarnessAcceptanceEvidenceIDs(state)})
}

func planExecuteHarnessAcceptanceEvidenceIDs(state ProgressState) []string {
	if state.Status != plancontract.PlanExecuteProgressStatusCompleted {
		return nil
	}

	return append([]string(nil), state.RequiredAcceptanceEvidenceIDs...)
}

func planExecutePlanSourceSatisfiedTerminalEvidence(requiredPaths []string, plan planartifact.PlanArtifact) ([]string, []plancontract.SourceSatisfiedEvidenceRef) {
	if len(plan.Paths.SourceSatisfied) == 0 {
		return nil, nil
	}

	requiredSet := planExecutePathSet(requiredPaths)
	if len(requiredSet) == 0 {
		requiredSet = planExecutePathSet(plan.Paths.Required)
	}

	evidence := planartifact.SourceSatisfiedEvidenceRefs(plan)
	violations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        planExecuteSetStrings(requiredSet),
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             evidence,
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	})

	paths := planExecuteSetStrings(plancontract.SourceSatisfiedCoveragePathSet(plan.Paths.SourceSatisfied, violations))
	if len(paths) == 0 {
		return nil, evidence
	}

	return paths, evidence
}

func saveRunProgressState(root string, state *ProgressState, jsonEvents bool, stdout io.Writer, eventType plancontract.PlanExecuteEventType, suppressRecovery ...bool) (string, error) {
	statePath, err := SaveProgressState(root, state)
	if err != nil {
		return statePath, err
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, ProgressStateEventFields(*state, statePath)); err != nil {
		return statePath, planExecuteJSONEventWriteError(err)
	}

	if ProgressRequiresRecovery(*state) && !(len(suppressRecovery) > 0 && suppressRecovery[0]) {
		now := time.Now().UTC()

		record, recordPath, err := recovery.WritePlanExecuteFailureRecord(root, RecoveryInputFromProgressWithInvariants(*state, statePath, nil), now)
		if err != nil {
			if emitErr := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventRecoveryFailureRecordFailed, map[string]any{
				plancontract.PlanExecuteEventFieldSessionID: state.SessionID,
				plancontract.PlanExecuteEventFieldStatePath: statePath,
				plancontract.PlanExecuteEventFieldReason:    err.Error(),
			}); emitErr != nil {
				return statePath, planExecuteJSONEventWriteError(emitErr)
			}

			return statePath, err
		}

		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventRecoveryFailureRecordWritten, map[string]any{
			plancontract.PlanExecuteEventFieldSessionID:       state.SessionID,
			plancontract.PlanExecuteEventFieldFailureID:       record.FailureID,
			plancontract.PlanExecuteEventFieldFailureClass:    record.Failure.Class,
			plancontract.PlanExecuteEventFieldFailureKind:     record.Failure.Kind,
			plancontract.PlanExecuteEventFieldRecordPath:      recordPath,
			plancontract.PlanExecuteEventFieldNextValidAction: record.NextAction,
		}); err != nil {
			return statePath, planExecuteJSONEventWriteError(err)
		}

		packet, packetPath, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, now)
		if err != nil {
			if emitErr := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventRecoveryDecompositionFailed, map[string]any{
				plancontract.PlanExecuteEventFieldSessionID:  state.SessionID,
				plancontract.PlanExecuteEventFieldFailureID:  record.FailureID,
				plancontract.PlanExecuteEventFieldRecordPath: recordPath,
				plancontract.PlanExecuteEventFieldReason:     err.Error(),
			}); emitErr != nil {
				return statePath, planExecuteJSONEventWriteError(emitErr)
			}

			return statePath, err
		}

		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventRecoveryDecompositionWritten, map[string]any{
			plancontract.PlanExecuteEventFieldSessionID:       state.SessionID,
			plancontract.PlanExecuteEventFieldFailureID:       record.FailureID,
			plancontract.PlanExecuteEventFieldPacketID:        packet.PacketID,
			plancontract.PlanExecuteEventFieldPacketPath:      packetPath,
			plancontract.PlanExecuteEventFieldCandidateAtoms:  len(packet.CandidateAtoms),
			plancontract.PlanExecuteEventFieldNextValidAction: packet.NextAction,
		}); err != nil {
			return statePath, planExecuteJSONEventWriteError(err)
		}

		if !jsonEvents {
			fmt.Fprintf(stdout, "recovery: failure_record=%s packet=%s next_valid_action=%s\n", recordPath, packetPath, packet.NextAction)
		}
	}

	return statePath, nil
}

func ProgressRequiresRecovery(state ProgressState) bool {
	switch state.Status {
	case plancontract.PlanExecuteProgressStatusFailed, plancontract.PlanExecuteProgressStatusDegraded, plancontract.PlanExecuteProgressStatusBlocked:
		return true
	default:
		return false
	}
}

type planExecuteRunResultInput struct {
	ExitCode                     int
	FailureKind                  string
	FailureReason                string
	PlannerAnswer                string
	SourceSatisfied              bool
	SourceSatisfiedEvidencePaths []string
	SourceSatisfiedEvidence      []plancontract.SourceSatisfiedEvidenceRef
	AcceptanceEvidenceIDs        []string
}

func finalizePlanExecuteRunResult(root string, state ProgressState, progressPath string, jsonEvents bool, stdout io.Writer, input planExecuteRunResultInput) RunResult {
	state.SessionID = strings.TrimSpace(state.SessionID)
	input.FailureKind = firstProgressNonEmptyString(input.FailureKind, string(state.FailureKind))
	input.FailureReason = firstProgressNonEmptyString(input.FailureReason, state.FailureReason)

	if len(input.AcceptanceEvidenceIDs) == 0 {
		input.AcceptanceEvidenceIDs = state.AcceptanceEvidenceIDs
	}

	if len(input.AcceptanceEvidenceIDs) > 0 && len(state.AcceptanceEvidenceIDs) == 0 {
		state.AcceptanceEvidenceIDs = input.AcceptanceEvidenceIDs
	}

	var recoveryInput planExecuteTerminalRecoveryMetadata
	if input.ExitCode != 0 {
		recoveryInput = planExecuteTerminalRecoveryInput(root, state.SessionID)
	}
	record := NewPlanExecuteTerminalCompletionRecord(state, PlanExecuteTerminalCompletionInput{
		ProgressPath:                 progressPath,
		ExitCode:                     input.ExitCode,
		SourceSatisfied:              input.SourceSatisfied,
		SourceSatisfiedEvidencePaths: input.SourceSatisfiedEvidencePaths,
		SourceSatisfiedEvidence:      input.SourceSatisfiedEvidence,
		AcceptanceEvidenceIDs:        input.AcceptanceEvidenceIDs,
		FailureID:                    recoveryInput.failureID,
		RecordPath:                   recoveryInput.recordPath,
		PacketPath:                   recoveryInput.packetPath,
		NextAction:                   recoveryInput.nextAction,
		CompletedAt:                  time.Now().UTC(),
	})

	if input.FailureKind != "" {
		record.FailureKind = plancontract.PlanExecuteFailureKind(input.FailureKind)
	}

	if input.FailureReason != "" {
		record.FailureReason = input.FailureReason
	}

	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
	if record.TerminalClass != plancontract.PlanExecuteTerminalClassSuccess {
		record.AcceptanceEvidenceIDs = nil
		state.AcceptanceEvidenceIDs = nil

		if record.TerminalClass == plancontract.PlanExecuteTerminalClassFailed {
			state.Status = plancontract.PlanExecuteProgressStatusFailed
			state.FailureKind = plancontract.PlanExecuteFailureKind(firstProgressNonEmptyString(input.FailureKind, string(record.FailureKind)))
			state.FailureReason = firstProgressNonEmptyString(input.FailureReason, record.FailureReason)
		}

		record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)

		if savedPath, saveErr := SaveProgressState(root, &state); saveErr == nil && savedPath != "" {
			progressPath = savedPath
		}
	}

	if record.TerminalClass == plancontract.PlanExecuteTerminalClassSuccess && len(record.ValidationFailures) > 0 {
		validationFailures := append([]string(nil), record.ValidationFailures...)

		input.ExitCode = 1
		input.FailureKind = string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)
		input.FailureReason = sanitizeProgressReason("terminal completion success rejected: " + strings.Join(validationFailures, "; "))
		record.ExitCode = input.ExitCode
		record.FailureKind = plancontract.PlanExecuteFailureKind(input.FailureKind)
		record.FailureReason = input.FailureReason
		record.AcceptanceEvidenceIDs = nil
		record.TerminalClass = plancontract.PlanExecuteTerminalClassFailed
		record.Outcome = plancontract.PlanExecuteTerminalOutcomeFailed
		record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
		record.ValidationFailures = validationFailures
		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = plancontract.PlanExecuteFailureKind(input.FailureKind)
		state.FailureReason = input.FailureReason
		state.AcceptanceEvidenceIDs = nil

		if savedPath, saveErr := SaveProgressState(root, &state); saveErr == nil && savedPath != "" {
			progressPath = savedPath
		}

		recoveryRecord, recordPath, recoveryErr := recovery.WritePlanExecuteFailureRecord(root, RecoveryInputFromProgressWithInvariants(state, progressPath, planExecuteRecoveryInvariantFailures(record)), time.Now().UTC())
		if recoveryErr == nil {
			recoveryInput.failureID = recoveryRecord.FailureID
			recoveryInput.recordPath = recordPath
			recoveryInput.nextAction = string(recoveryRecord.NextAction)

			if _, packetPath, packetErr := recovery.WriteDecompositionPacketForFailureRecord(root, recoveryRecord, time.Now().UTC()); packetErr == nil {
				recoveryInput.packetPath = packetPath
			}

			record.FailureID = recoveryInput.failureID
			record.RecordPath = recoveryInput.recordPath
			record.PacketPath = recoveryInput.packetPath
			record.NextAction = recoverycontract.NextAction(strings.TrimSpace(recoveryInput.nextAction))
		}
	}

	completionPath, err := SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		if emitErr := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventTerminalCompletionSaveFailed, map[string]any{
			plancontract.PlanExecuteEventFieldSessionID: state.SessionID,
			plancontract.PlanExecuteEventFieldStatePath: progressPath,
			plancontract.PlanExecuteEventFieldReason:    err.Error(),
		}); emitErr != nil {
			return planExecuteJSONEventFailureResult(state.SessionID, progressPath, input.PlannerAnswer, emitErr)
		}

		return RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: progressPath,
			FailureKind:     firstProgressNonEmptyString(input.FailureKind, string(plancontract.PlanExecuteFailureTerminalCompletionSaveFailed)),
			FailureReason:   sanitizeProgressReason(firstProgressNonEmptyString(input.FailureReason, err.Error())),
			PlannerAnswer:   input.PlannerAnswer,
		}
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventTerminalCompletion, plancontract.PlanExecuteTerminalCompletionEventFields(record, completionPath)); err != nil {
		return planExecuteJSONEventFailureResult(state.SessionID, progressPath, input.PlannerAnswer, err)
	}

	return RunResult{
		ExitCode:        input.ExitCode,
		SessionID:       state.SessionID,
		ResumeStatePath: progressPath,
		FailureKind:     input.FailureKind,
		FailureReason:   input.FailureReason,
		PlannerAnswer:   input.PlannerAnswer,
		Completion:      &record,
		CompletionPath:  completionPath,
	}
}

type planExecuteTerminalRecoveryMetadata struct {
	failureID  string
	recordPath string
	packetPath string
	nextAction string
}

func planExecuteTerminalRecoveryInput(root string, sessionID string) planExecuteTerminalRecoveryMetadata {
	record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
	if err != nil || !found {
		return planExecuteTerminalRecoveryMetadata{}
	}

	metadata := planExecuteTerminalRecoveryMetadata{
		failureID:  strings.TrimSpace(record.FailureID),
		recordPath: strings.TrimSpace(recordPath),
		nextAction: strings.TrimSpace(string(record.NextAction)),
	}
	if metadata.failureID != "" {
		if _, packetPath, err := recovery.DecompositionPacketPath(root, metadata.failureID, ""); err == nil {
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(packetPath))); statErr == nil {
				metadata.packetPath = packetPath
			}
		}
	}

	return metadata
}

func RecoveryInputFromProgressWithInvariants(state ProgressState, statePath string, invariantFailures []recoverycontract.InvariantFailure) recovery.PlanExecuteFailureInput {
	if len(invariantFailures) == 0 {
		invariantFailures = progressInvariantFailures(state)
	}

	return recovery.PlanExecuteFailureInput{
		SessionID:         state.SessionID,
		ProgressStatePath: statePath,
		Phase:             string(state.Phase),
		Status:            string(state.Status),
		FailureKind:       string(state.FailureKind),
		FailureReason:     state.FailureReason,
		PromptSHA256:      state.PromptSHA256,
		PromptFile:        state.PromptFile,
		PlanPath:          state.PlanPath,
		PlanID:            state.PlanID,
		PackID:            state.PackID,
		ParentAtomID:      state.ParentAtomID,
		ParentAtomTitle:   state.ParentAtomTitle,
		LockedContract:    progressLockedContractSnapshot(state),
		RequiredEditPaths: state.RequiredEditPaths,
		AllowedEditPaths:  state.AllowedEditPaths,
		ChangedPaths:      state.ChangedPaths,
		TotalEdits:        state.TotalEdits,
		ExecutorStatus:    string(state.Executor.Status),
		ExecutorFailures:  state.Executor.Failures,
		ReviewStatus:      string(state.Review.Status),
		ReviewReasonClass: state.Review.ReasonClass,
		ReviewError:       state.Review.Error,
		InvariantFailures: invariantFailures,
	}
}

func progressLockedContractSnapshot(state ProgressState) *recoverycontract.ContractSnapshot {
	if strings.TrimSpace(state.ScopeContractID) == "" && strings.TrimSpace(state.ScopeContractHash) == "" && len(state.RequiredEditPaths) == 0 && len(state.AllowedEditPaths) == 0 && len(state.WriteAllowedPaths) == 0 && !state.WriteAllowedExplicit {
		return nil
	}

	return &recoverycontract.ContractSnapshot{
		ContractID:           state.ScopeContractID,
		ContractHash:         state.ScopeContractHash,
		RuntimeContractID:    state.ScopeContractID,
		ProtocolHash:         state.ScopeContractHash,
		RequiredEditPaths:    append([]string(nil), state.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), state.AllowedEditPaths...),
		WriteAllowedPaths:    append([]string(nil), state.WriteAllowedPaths...),
		WriteAllowedExplicit: state.WriteAllowedExplicit,
	}
}

func progressInvariantFailures(state ProgressState) []recoverycontract.InvariantFailure {
	kind := strings.TrimSpace(string(state.FailureKind))
	reason := strings.TrimSpace(state.FailureReason)

	if kind == "" && reason == "" {
		return nil
	}

	return []recoverycontract.InvariantFailure{{
		InvariantID:            firstProgressNonEmptyString(kind, "plan_execute_failed"),
		FieldPath:              "progress.failure",
		Expected:               "terminal_success_contract_satisfied",
		Actual:                 sanitizeProgressReason(firstProgressNonEmptyString(reason, kind)),
		RequiredRepairEvidence: []string{"passing_terminal_completion"},
	}}
}

func planExecuteRecoveryInvariantFailures(record plancontract.PlanExecuteTerminalCompletionRecord) []recoverycontract.InvariantFailure {
	var failures []recoverycontract.InvariantFailure

	for _, invariant := range record.FailureInvariants {
		if strings.TrimSpace(invariant.Status) != "failed" {
			continue
		}

		failures = append(failures, recoverycontract.InvariantFailure{
			InvariantID:            invariant.Code,
			FieldPath:              "terminal_completion." + invariant.Code,
			Path:                   planExecuteInvariantFailurePath(invariant.Code),
			Expected:               "passed",
			Actual:                 firstProgressNonEmptyString(invariant.Reason, invariant.Status),
			RequiredRepairEvidence: []string{"passing_terminal_completion"},
		})
	}

	for _, validationFailure := range record.ValidationFailures {
		validationFailure = strings.TrimSpace(validationFailure)
		if validationFailure == "" {
			continue
		}

		failures = append(failures, recoverycontract.InvariantFailure{
			InvariantID:            validationFailure,
			FieldPath:              "terminal_completion.validation_failures." + validationFailure,
			Path:                   planExecuteInvariantFailurePath(validationFailure),
			Expected:               "absent",
			Actual:                 validationFailure,
			RequiredRepairEvidence: []string{"passing_terminal_completion"},
		})
	}

	return failures
}

func planExecuteInvariantFailurePath(code string) string {
	prefix, suffix, ok := strings.Cut(strings.TrimSpace(code), ":")
	if !ok {
		return ""
	}

	switch prefix {
	case "changed_path_outside_write_envelope",
		"required_path_missing",
		"required_path_covered",
		"source_sanity_failed",
		"source_sanity_missing",
		"missing_required_scope",
		"outside_required_scope",
		"pending_approval",
		"outside_approved_scope",
		"missing_current_state_evidence",
		"source_satisfied_missing_required_scope",
		"source_satisfied_outside_required_scope",
		"source_satisfied_pending_approval",
		"source_satisfied_outside_approved_scope",
		"source_satisfied_missing_current_state_evidence":
	default:
		return ""
	}

	suffix = strings.TrimSpace(suffix)
	if suffix == "" || suffix == "(unknown)" || strings.ContainsAny(suffix, " \t\r\n") {
		return ""
	}

	return plancontract.NormalizeScopePath(suffix)
}
