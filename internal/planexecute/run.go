package planexecute

import (
	"fmt"
	"io"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
	"idiom/internal/writerroute"
)

type RunResult struct {
	ExitCode        int
	SessionID       string
	ResumeStatePath string
	FailureKind     string
	FailureReason   string
	PlannerAnswer   string
}

const RunFailurePlannerBlocked = "planner_blocked"

func Run(cfg config.Config, opts Options, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) RunResult {
	defer cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	sessionStart := time.Now().Add(-1 * time.Second)
	prompt, err := planExecutePrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1}
	}
	var resumeState *ProgressState
	resumeStatePath := ""
	if strings.TrimSpace(opts.Resume) != "" {
		state, statePath, found, err := TryLoadProgressState(cfg.Workspace.Root, opts.Resume)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return RunResult{ExitCode: 1}
		}
		if !found {
			fmt.Fprintln(stderr, fmt.Errorf("plan-execute progress state %s does not exist", statePath))
			return RunResult{ExitCode: 1, ResumeStatePath: statePath}
		}
		if found {
			resumeState = &state
			resumeStatePath = statePath
		}
	}
	sessionID := strings.TrimSpace(opts.SessionID)
	if resumeState != nil && strings.TrimSpace(resumeState.SessionID) != "" {
		sessionID = resumeState.SessionID
	}
	if sessionID == "" {
		sessionID = newPlanExecuteSessionID()
	}
	cfg.Workspace.SessionID = sessionID
	logCLIPhase(rec, "plan_execute_start", map[string]any{
		"json_events": opts.JSONEvents,
		"session_id":  sessionID,
		"resume":      resumeState != nil,
	})
	queueRelease := AcquireQueueSlot(cfg, QueueAcquireOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		SessionID:  sessionID,
	}, rec, stdout, stderr)
	defer queueRelease()

	requiredEditPaths := planExecuteRequiredEditPaths(opts, prompt)
	allowedEditPaths := planExecuteAllowedEditPaths(opts)
	promptRequiredEditPaths := RequiredEditPathsFromPrompt(prompt)
	explicitRequiredEditPaths := planExecuteUniqueRequiredPaths([]string(opts.RequiredEditPaths))
	explicitAllowedEditPaths := planExecuteUniqueRequiredPaths([]string(opts.AllowedEditPaths))
	logCLIPhase(rec, "plan_execute_required_edit_paths", map[string]any{
		"count":          len(requiredEditPaths),
		"explicit_count": len(explicitRequiredEditPaths),
		"prompt_count":   len(promptRequiredEditPaths),
		"allowed_count":  len(allowedEditPaths),
	})
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "required_edit_paths_resolved", map[string]any{
		"count":               len(requiredEditPaths),
		"explicit_count":      len(explicitRequiredEditPaths),
		"prompt_count":        len(promptRequiredEditPaths),
		"allowed_count":       len(explicitAllowedEditPaths),
		"required_edit_paths": requiredEditPaths,
		"allowed_edit_paths":  allowedEditPaths,
	})
	decision := writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              writerroute.ModePlanSubmission,
		Lane:              "plan-execute",
		PlannerModel:      cfg.LlamaServer.Model,
		ExecutorModel:     cfg.LlamaServer.Model,
		ReviewerModel:     cfg.LlamaServer.Model,
		ClassifierModel:   cfg.RouteClassifier.Model,
		VerificationOwner: "planner_owned",
		RequiredEditPaths: requiredEditPaths,
		ReasonCodes:       []string{"route.multi_file_plan_execute"},
		TaskFamily:        agent.PlanThenExecutePlannerTaskFamily,
	})
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "writer_route_decision", decision.EventFields())
	if resumeState != nil {
		return runResumeReviewRepair(cfg, opts, prompt, *resumeState, resumeStatePath, promptRun, rec, stdout, stderr)
	}

	progress := NewProgressState(sessionID, prompt, opts.PromptFile, requiredEditPaths, allowedEditPaths, time.Now().UTC())
	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}

	plannerPrompt := planExecutePlannerSystemAddendum(requiredEditPaths, allowedEditPaths) + "\n\n" + prompt
	plannerMaxTokens := 16384
	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}
	answer := ""
	emitPhase := opts.OnPhase
	executorRequiredEditPaths := requiredEditPaths
	autoExact := planExecuteTryAutoExactPlan(cfg, prompt, requiredEditPaths)
	if autoExact.SourceSatisfied {
		answer = planExecuteAutoSourceSatisfiedAnswer(requiredEditPaths)
		progress.Phase = ProgressPhaseComplete
		progress.Status = ProgressStatusCompleted
		_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_source_satisfied", map[string]any{
			"edits":               autoExact.EditCount,
			"applied":             autoExact.AppliedCount,
			"required_edit_paths": requiredEditPaths,
			"applied_paths":       autoExact.AppliedPaths,
			"reason":              autoExact.Reason,
		})
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied", map[string]any{
			"required_edit_paths": requiredEditPaths,
			"reason":              autoExact.Reason,
		})
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
			fmt.Fprintln(stdout, "source-satisfied: executor skipped because every exact edit already matched the current workspace.")
		}
		return RunResult{ExitCode: 0, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	if autoExact.Submitted {
		answer = autoExact.Result.Summary
		if len(autoExact.PendingPaths) > 0 {
			executorRequiredEditPaths = autoExact.PendingPaths
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_submitted", map[string]any{
			"edits":         autoExact.EditCount,
			"applied":       autoExact.AppliedCount,
			"pending":       autoExact.PendingCount,
			"applied_paths": autoExact.AppliedPaths,
			"pending_paths": autoExact.PendingPaths,
			"reason":        autoExact.Reason,
		})
		if autoExact.AppliedCount > 0 {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_partially_satisfied", map[string]any{
				"applied":       autoExact.AppliedCount,
				"pending":       autoExact.PendingCount,
				"applied_paths": autoExact.AppliedPaths,
				"pending_paths": autoExact.PendingPaths,
			})
		}
		if emitPhase != nil {
			emitPhase("executing", fmt.Sprintf("Applied %d auto-exact edit(s)", autoExact.EditCount))
		}
	} else {
		if strings.TrimSpace(autoExact.Reason) != "" {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_skipped", map[string]any{
				"reason":        autoExact.Reason,
				"error":         autoExact.Result.Error,
				"applied":       autoExact.AppliedCount,
				"pending":       autoExact.PendingCount,
				"unknown":       autoExact.UnknownCount,
				"applied_paths": autoExact.AppliedPaths,
				"pending_paths": autoExact.PendingPaths,
				"unknown_paths": autoExact.UnknownPaths,
			})
		}
		if emitPhase != nil {
			emitPhase("planning", "Analyzing objective and creating plan...")
		}
		answer, err = promptRun(cfg, plannerPrompt, RunOptions{
			JSONEvents:               opts.JSONEvents,
			Resume:                   opts.Resume,
			TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
			PlannerOwnedVerification: true,
			MaxTokensOverride:        plannerMaxTokens,
			DisableThinking:          opts.DisableThinking,
			Telemetry:                rec,
		}, stdout)
		if err != nil {
			progress.Status = ProgressStatusFailed
			progress.FailureKind = "planner_failed"
			progress.FailureReason = truncateProgressReason(err.Error())
			_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
			fmt.Fprintln(stderr, err)
			return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
		}
	}

	planPath, err := FindLatestPlanArtifact(cfg.Workspace.Root, sessionStart, sessionID)
	if err != nil {
		if agent.PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, requiredEditPaths) {
			progress.Phase = ProgressPhaseComplete
			progress.Status = ProgressStatusCompleted
			_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied", map[string]any{
				"required_edit_paths": requiredEditPaths,
				"reason":              "planner reported requested final state already present",
			})
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
				fmt.Fprintln(stdout, "")
				fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
				fmt.Fprintln(stdout, "source-satisfied: executor skipped because the planner reported the requested final state already present in every required path.")
			}
			return RunResult{ExitCode: 0, SessionID: sessionID, ResumeStatePath: progressPath}
		}
		if planExecuteAnswerDeclaresBlocker(answer) {
			progress.Status = ProgressStatusFailed
			progress.FailureKind = RunFailurePlannerBlocked
			progress.FailureReason = planExecuteBlockerSummary(answer)
			_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_blocked", map[string]any{
				"reason":  err.Error(),
				"blocker": planExecuteBlockerSummary(answer),
			})
			fmt.Fprintln(stderr, fmt.Errorf("planner phase reported a blocker before submitting a plan: %s", planExecuteBlockerSummary(answer)))
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
			}
			return RunResult{
				ExitCode:        1,
				SessionID:       sessionID,
				ResumeStatePath: progressPath,
				FailureKind:     RunFailurePlannerBlocked,
				FailureReason:   planExecuteBlockerSummary(answer),
				PlannerAnswer:   answer,
			}
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_missing", map[string]any{
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}
		progress.Status = ProgressStatusFailed
		progress.FailureKind = "plan_artifact_missing"
		progress.FailureReason = truncateProgressReason(err.Error())
		_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_invalid", map[string]any{
			"path":   planPath,
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		progress.Status = ProgressStatusFailed
		progress.FailureKind = "plan_artifact_invalid"
		progress.FailureReason = truncateProgressReason(err.Error())
		_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	if inheritedPackID := inheritPlanExecutePromptPackID(&plan, prompt); inheritedPackID != "" {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_pack_inherited", map[string]any{
			"path":    planPath,
			"pack_id": inheritedPackID,
		})
		if err := patchPlanArtifactPackID(planPath, inheritedPackID); err != nil {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_pack_inherit_persist_failed", map[string]any{
				"path":    planPath,
				"pack_id": inheritedPackID,
				"reason":  err.Error(),
			})
		}
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_loaded", map[string]any{
		"path":    planPath,
		"edits":   len(plan.Edits),
		"pack_id": strings.TrimSpace(plan.PackID),
	})
	if emitPhase != nil {
		emitPhase("planning", fmt.Sprintf("Plan created with %d edit(s)", len(plan.Edits)))
	}
	reviewBaseline := CaptureReviewRepairBaseline(cfg.Workspace.Root, planChangedPaths(plan))
	progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, reviewBaseline)
	progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	if emitPhase != nil {
		emitPhase("executing", fmt.Sprintf("Applying %d edit(s)...", len(plan.Edits)))
	}
	var progressSaveErr error
	progressCallback := func(step, total int, path string, succeeded bool) {
		progress.MarkEditProgress(step, total, path, succeeded)
		if _, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); err != nil && progressSaveErr == nil {
			progressSaveErr = err
		}
		if opts.OnEditProgress != nil {
			opts.OnEditProgress(step, total, path, succeeded)
		}
	}
	report, runErr := RunExecutorPhaseScoped(cfg, plan, executorRequiredEditPaths, allowedEditPaths, opts.JSONEvents, stdout, progressCallback)
	if runErr == nil && progressSaveErr != nil {
		runErr = progressSaveErr
	}
	progress.MarkExecutorComplete(report, runErr)
	progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil && runErr == nil {
		runErr = err
	}
	_ = sessionStart
	reviewReport := ReviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}
	var reviewErr error
	if runErr == nil && !opts.SkipReviewRepair {
		if emitPhase != nil {
			emitPhase("reviewing", "Verifying changes...")
		}
		reviewSessionID := NewPlanExecuteSessionID()
		progress.MarkReviewStarted(reviewSessionID)
		progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil {
			reviewErr = err
		} else {
			reviewOpts := opts
			reviewOpts.ReviewSessionID = reviewSessionID
			reviewReport, reviewErr = RunReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec)
		}
		progress.MarkReviewComplete(reviewReport, reviewErr)
		progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil && reviewErr == nil {
			reviewErr = err
		}
	} else if runErr == nil && opts.SkipReviewRepair {
		progress.MarkSkippedReview()
		progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
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
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	return RunResult{ExitCode: 0, SessionID: sessionID, ResumeStatePath: progressPath}
}

func runResumeReviewRepair(cfg config.Config, opts Options, prompt string, state ProgressState, statePath string, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) RunResult {
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
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		fmt.Fprintln(stderr, fmt.Errorf("read resumed plan artifact %q: %w", planPath, err))
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}
	requiredEditPaths := mergeProgressPaths(state.RequiredEditPaths, planExecuteRequiredEditPaths(opts, prompt))
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "resume_state_loaded", ProgressStateEventFields(state, statePath))
	if opts.OnPhase != nil {
		opts.OnPhase("reviewing", "Resuming review-repair from executor progress state...")
	}
	reviewSessionID := NewPlanExecuteSessionID()
	state.MarkReviewStarted(reviewSessionID)
	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: statePath}
	}
	reviewOpts := opts
	reviewOpts.ReviewSessionID = reviewSessionID
	report := state.ExecutorPhaseReport()
	reviewBaseline := state.ReviewBaseline
	reviewReport, reviewErr := RunReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec)
	state.MarkReviewComplete(reviewReport, reviewErr)
	progressPath, err = saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved")
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
		return RunResult{ExitCode: 1, SessionID: sessionID, ResumeStatePath: progressPath}
	}
	return RunResult{ExitCode: 0, SessionID: sessionID, ResumeStatePath: progressPath}
}

func saveRunProgressState(root string, state *ProgressState, jsonEvents bool, stdout io.Writer, eventType string) (string, error) {
	statePath, err := SaveProgressState(root, state)
	if err != nil {
		return statePath, err
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, ProgressStateEventFields(*state, statePath))
	return statePath, nil
}
