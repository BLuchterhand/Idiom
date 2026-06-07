package planexecute

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/recovery"
)

type RunResult struct {
	ExitCode        int
	SessionID       string
	ResumeStatePath string
	FailureKind     string
	FailureReason   string
	PlannerAnswer   string
	Completion      *PlanExecuteTerminalCompletionRecord
	CompletionPath  string
}

const (
	RunFailurePlannerBlocked                     = "planner_blocked"
	RunFailureSourceSanityFailed                 = "source_sanity_failed"
	RunFailureSourceSatisfiedUnsupportedEvidence = "source_satisfied_unsupported_evidence"
	RunFailureVerificationRequired               = "verification_failed"
	RunFailureMissingEditPathWithoutNewFileAuth  = "missing_edit_path_without_new_file_authorization"
)

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

	scopeContract := planExecuteScopeContract(opts, prompt)
	requiredEditPaths := scopeContract.RequiredCoveragePaths
	allowedEditPaths := scopeContract.AllowedCandidatePaths
	writeAllowedPaths := scopeContract.WriteAllowedPaths
	writeAllowedExplicit := scopeContract.WriteAllowedExplicit
	editSetObligations := scopeContract.EditSetObligations
	verificationPlan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: cfg.Workspace.Root,
		Prompt:        prompt,
	})
	verificationCommands := verificationPlan.CommandStrings()
	opts.ScopeContract = scopeContract
	opts.EditSetObligations = editSetObligations
	promptRequiredEditPaths := RequiredEditPathsFromPrompt(prompt)
	explicitRequiredEditPaths := planExecuteUniqueRequiredPaths([]string(opts.RequiredEditPaths))
	logCLIPhase(rec, "plan_execute_required_edit_paths", map[string]any{
		"count":                     len(requiredEditPaths),
		"explicit_count":            len(explicitRequiredEditPaths),
		"prompt_count":              len(promptRequiredEditPaths),
		"allowed_count":             len(allowedEditPaths),
		"scope_contract_id":         scopeContract.ContractID,
		"edit_set_obligation_count": len(editSetObligations),
	})
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "required_edit_paths_resolved", map[string]any{
		"count":                     len(requiredEditPaths),
		"explicit_count":            len(explicitRequiredEditPaths),
		"prompt_count":              len(promptRequiredEditPaths),
		"allowed_count":             len(allowedEditPaths),
		"required_edit_paths":       requiredEditPaths,
		"allowed_edit_paths":        allowedEditPaths,
		"verification_commands":     verificationCommands,
		"scope_contract_id":         scopeContract.ContractID,
		"edit_set_obligations":      editSetObligations,
		"edit_set_obligation_count": len(editSetObligations),
	})
	decision := planExecuteWriterRouteDecision(cfg, requiredEditPaths)
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "writer_route_decision", decision.EventFields())
	if resumeState != nil {
		return runResumeReviewRepair(cfg, opts, prompt, *resumeState, resumeStatePath, promptRun, rec, stdout, stderr)
	}

	progress := NewProgressState(sessionID, prompt, opts.PromptFile, requiredEditPaths, allowedEditPaths, time.Now().UTC())
	progress.SetWriteAllowedEnvelope(writeAllowedPaths, writeAllowedExplicit)
	progress.EditSetObligations = editSetObligations
	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}

	plannerBrief := appendPlanExecuteScopeToBriefBody(prompt, requiredEditPaths, allowedEditPaths)
	contextPackSection := BuildPromptContextPackSection(cfg, PromptContextPackOptions{
		Mode:              "plan",
		Title:             prompt,
		Brief:             plannerBrief,
		ProjectRoot:       cfg.Workspace.Root,
		RequiredEditPaths: requiredEditPaths,
	}, stderr)
	plannerBrief = ComposePlannerPromptWithContextPack(plannerBrief, contextPackSection)
	plannerPrompt := planExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, editSetObligations) + "\n\n" + plannerBrief
	plannerMaxTokens := 16384
	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}
	answer := ""
	emitPhase := opts.OnPhase
	executorRequiredEditPaths := requiredEditPaths
	autoExactScopePaths := requiredEditPaths
	if len(autoExactScopePaths) == 0 {
		autoExactScopePaths = allowedEditPaths
	}
	autoExact := planExecuteTryAutoExactPlan(cfg, prompt, autoExactScopePaths)
	if autoExact.SourceSatisfied {
		if reason := editSetObligations.RejectionReason(autoExact.AppliedPaths); reason != "" {
			message := EditSetObligationFailureMessage(reason)
			progress.MarkPlanningFailed(reason, message)
			_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied_rejected", map[string]any{
				"reason":                          reason,
				"applied_paths":                   autoExact.AppliedPaths,
				"source_satisfied_evidence_paths": autoExact.AppliedPaths,
				"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
				"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(planExecuteAutoSourceSatisfiedAnswer(autoExactScopePaths), requiredEditPaths),
				"edit_set_obligations":            editSetObligations,
				"edit_set_obligation_count":       len(editSetObligations),
			})
			fmt.Fprintln(stderr, message)
			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: reason, FailureReason: sanitizeProgressReason(message)})
		}
		answer = planExecuteAutoSourceSatisfiedAnswer(autoExactScopePaths)
		progress.Phase = ProgressPhaseComplete
		progress.Status = ProgressStatusCompleted
		_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_source_satisfied", map[string]any{
			"edits":                           autoExact.EditCount,
			"applied":                         autoExact.AppliedCount,
			"required_edit_paths":             requiredEditPaths,
			"allowed_edit_paths":              allowedEditPaths,
			"edit_set_obligations":            editSetObligations,
			"edit_set_obligation_count":       len(editSetObligations),
			"applied_paths":                   autoExact.AppliedPaths,
			"source_satisfied_evidence_paths": autoExact.AppliedPaths,
			"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
			"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths),
			"reason":                          autoExact.Reason,
		})
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied", map[string]any{
			"required_edit_paths":             requiredEditPaths,
			"allowed_edit_paths":              allowedEditPaths,
			"source_satisfied_evidence_paths": autoExact.AppliedPaths,
			"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(autoExact.AppliedPaths),
			"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths),
			"edit_set_obligations":            editSetObligations,
			"edit_set_obligation_count":       len(editSetObligations),
			"reason":                          autoExact.Reason,
		})
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
			fmt.Fprintln(stdout, "source-satisfied: executor skipped because every exact edit already matched the current workspace.")
		}
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 0, SourceSatisfied: true, SourceSatisfiedEvidencePaths: autoExact.AppliedPaths})
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
			TaskFamily:               string(workerruntime.TaskFamilyPlanThenExecutePlanner),
			PlannerOwnedVerification: true,
			MaxTokensOverride:        plannerMaxTokens,
			DisableThinking:          opts.DisableThinking,
			UseActiveModel:           opts.UseActiveModel,
			Telemetry:                rec,
			ScopeContract:            scopeContract,
		}, stdout)
		if err != nil {
			progress.MarkPlanningFailed("planner_failed", err.Error())
			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}
				fmt.Fprintln(stderr, saveErr)
			}
			fmt.Fprintln(stderr, err)
			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: "planner_failed", FailureReason: sanitizeProgressReason(err.Error())})
		}
	}

	planPath, err := FindLatestPlanArtifact(cfg.Workspace.Root, sessionStart, sessionID)
	if err != nil {
		sourceSatisfiedPaths := planExecuteSourceSatisfiedEvidencePaths(answer, requiredEditPaths, allowedEditPaths)
		if plancontract.SourceSatisfiedAnswerCoversPaths(answer, sourceSatisfiedPaths) {
			if marker := planExecuteSourceSatisfiedUnsupportedEvidenceMarker(answer, sourceSatisfiedPaths); marker != "" {
				message := fmt.Sprintf("%s: source-satisfied evidence includes unsupported inference marker %q; submit a grounded plan or report a typed blocker", RunFailureSourceSatisfiedUnsupportedEvidence, marker)
				progress.MarkPlanningFailed(RunFailureSourceSatisfiedUnsupportedEvidence, message)
				if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
					if savedPath != "" {
						progressPath = savedPath
					}
					fmt.Fprintln(stderr, saveErr)
				}
				emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied_rejected", map[string]any{
					"reason":                          RunFailureSourceSatisfiedUnsupportedEvidence,
					"unsupported_evidence_marker":     marker,
					"required_edit_paths":             requiredEditPaths,
					"allowed_edit_paths":              allowedEditPaths,
					"source_satisfied_evidence_paths": sourceSatisfiedPaths,
					"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(sourceSatisfiedPaths),
					"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths),
					"edit_set_obligations":            editSetObligations,
					"edit_set_obligation_count":       len(editSetObligations),
				})
				fmt.Fprintln(stderr, message)
				return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: RunFailureSourceSatisfiedUnsupportedEvidence, FailureReason: sanitizeProgressReason(message), SourceSatisfiedEvidencePaths: sourceSatisfiedPaths})
			}
			if reason := editSetObligations.RejectionReason(sourceSatisfiedPaths); reason != "" {
				message := EditSetObligationFailureMessage(reason)
				progress.MarkPlanningFailed(reason, message)
				if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
					if savedPath != "" {
						progressPath = savedPath
					}
					fmt.Fprintln(stderr, saveErr)
				}
				emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied_rejected", map[string]any{
					"reason":                          reason,
					"required_edit_paths":             requiredEditPaths,
					"allowed_edit_paths":              allowedEditPaths,
					"source_satisfied_evidence_paths": sourceSatisfiedPaths,
					"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(sourceSatisfiedPaths),
					"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths),
					"edit_set_obligations":            editSetObligations,
					"edit_set_obligation_count":       len(editSetObligations),
				})
				fmt.Fprintln(stderr, message)
				return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: reason, FailureReason: sanitizeProgressReason(message), SourceSatisfiedEvidencePaths: sourceSatisfiedPaths})
			}
			progress.Phase = ProgressPhaseComplete
			progress.Status = ProgressStatusCompleted
			_, _ = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "source_satisfied", map[string]any{
				"required_edit_paths":             requiredEditPaths,
				"allowed_edit_paths":              allowedEditPaths,
				"source_satisfied_evidence_paths": sourceSatisfiedPaths,
				"missing_edit_set_obligations":    editSetObligations.MissingOpenObligations(sourceSatisfiedPaths),
				"missing_required_edit_paths":     planExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths),
				"edit_set_obligations":            editSetObligations,
				"edit_set_obligation_count":       len(editSetObligations),
				"reason":                          "planner reported requested final state already present",
			})
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
				fmt.Fprintln(stdout, "")
				fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
				fmt.Fprintln(stdout, "source-satisfied: executor skipped because the planner reported the requested final state already present in every required path.")
			}
			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 0, SourceSatisfied: true, SourceSatisfiedEvidencePaths: sourceSatisfiedPaths})
		}
		if planExecuteAnswerDeclaresBlocker(answer) {
			progress.MarkPlanningFailed(RunFailurePlannerBlocked, planExecuteBlockerSummary(answer))
			if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
				if savedPath != "" {
					progressPath = savedPath
				}
				fmt.Fprintln(stderr, saveErr)
			}
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_blocked", map[string]any{
				"reason":  err.Error(),
				"blocker": planExecuteBlockerSummary(answer),
			})
			fmt.Fprintln(stderr, fmt.Errorf("planner phase reported a blocker before submitting a plan: %s", planExecuteBlockerSummary(answer)))
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
			}
			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: RunFailurePlannerBlocked, FailureReason: planExecuteBlockerSummary(answer), PlannerAnswer: answer})
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_missing", map[string]any{
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}
		progress.MarkPlanningFailed("plan_artifact_missing", err.Error())
		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}
			fmt.Fprintln(stderr, saveErr)
		}
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: "plan_artifact_missing", FailureReason: sanitizeProgressReason(err.Error()), PlannerAnswer: answer})
	}
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_invalid", map[string]any{
			"path":   planPath,
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		progress.MarkPlanningFailed("plan_artifact_invalid", err.Error())
		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}
			fmt.Fprintln(stderr, saveErr)
		}
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: "plan_artifact_invalid", FailureReason: sanitizeProgressReason(err.Error())})
	}
	if inheritedPackID := inheritPlanExecutePromptPackID(&plan, plannerPrompt); inheritedPackID != "" {
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
	changedPaths := planChangedPaths(plan)
	verificationPlan = PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: cfg.Workspace.Root,
		Prompt:        prompt,
		ChangedPaths:  changedPaths,
	})
	verificationCommands = verificationPlan.CommandStrings()
	if reason := editSetObligations.RejectionReason(changedPaths); reason != "" {
		message := EditSetObligationFailureMessage(reason)
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_plan_rejected", map[string]any{
			"path":                      planPath,
			"reason":                    reason,
			"changed_paths":             changedPaths,
			"edit_set_obligations":      editSetObligations,
			"edit_set_obligation_count": len(editSetObligations),
		})
		progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, ReviewRepairBaseline{})
		progress.Status = ProgressStatusFailed
		progress.FailureKind = reason
		progress.FailureReason = sanitizeProgressReason(message)
		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}
			fmt.Fprintln(stderr, saveErr)
		}
		fmt.Fprintln(stderr, message)
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: reason, FailureReason: sanitizeProgressReason(message), PlannerAnswer: answer})
	}
	if emitPhase != nil {
		emitPhase("planning", fmt.Sprintf("Plan created with %d edit(s)", len(plan.Edits)))
	}
	if mismatch := preflightPlanMissingPathAuthorization(cfg.Workspace.Root, prompt, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_plan_rejected", map[string]any{
			"path":   planPath,
			"reason": mismatch.Reason,
			"edit":   mismatch.Path,
		})
		progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, ReviewRepairBaseline{})
		progress.Status = ProgressStatusFailed
		progress.FailureKind = RunFailureMissingEditPathWithoutNewFileAuth
		progress.FailureReason = sanitizeProgressReason(mismatch.Reason)
		if savedPath, saveErr := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			if savedPath != "" {
				progressPath = savedPath
			}
			fmt.Fprintln(stderr, saveErr)
		}
		fmt.Fprintln(stderr, mismatch.Reason)
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: progress.FailureKind, FailureReason: sanitizeProgressReason(mismatch.Reason), PlannerAnswer: answer})
	}
	reviewBaseline := CaptureReviewRepairBaseline(cfg.Workspace.Root, changedPaths)
	progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, reviewBaseline)
	_, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
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
	report, runErr := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(cfg, plan, executorRequiredEditPaths, allowedEditPaths, writeAllowedPaths, writeAllowedExplicit, verificationPlan, opts.JSONEvents, stdout, progressCallback)
	if runErr == nil && progressSaveErr != nil {
		runErr = progressSaveErr
	}
	sourceSanityRepairRequired := ExecutorSourceSanityFailed(report, runErr)
	verificationRepairRequired := ExecutorVerificationFailed(report, runErr)
	progress.MarkExecutorComplete(report, runErr)
	progressPath, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved", (sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair)
	if err != nil && runErr == nil {
		runErr = err
	}
	if sourceSanityRepairRequired {
		if emitPhase != nil {
			emitPhase("reviewing", "Source sanity failed; recovery evidence is being prepared...")
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "executor_verification_repair_required", map[string]any{
			"failure_kind": RunFailureSourceSanityFailed,
			"reason":       sanitizeProgressReason(runErr.Error()),
		})
	} else if verificationRepairRequired {
		if emitPhase != nil {
			emitPhase("reviewing", "Verification failed; recovery evidence is being prepared...")
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "executor_verification_repair_required", map[string]any{
			"failure_kind":          RunFailureVerificationRequired,
			"reason":                sanitizeProgressReason(runErr.Error()),
			"verification_commands": verificationCommands,
		})
	}
	_ = sessionStart
	reviewReport := ReviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}
	var reviewErr error
	if (runErr == nil || sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair {
		if emitPhase != nil {
			emitPhase("reviewing", "Verifying changes...")
		}
		reviewSessionID := NewPlanExecuteSessionID()
		progress.MarkReviewStarted(reviewSessionID)
		_, err = saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil {
			reviewErr = err
		} else {
			reviewOpts := opts
			reviewOpts.ReviewSessionID = reviewSessionID
			reviewReport, reviewErr = RunReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec)
		}
		progress.MarkReviewComplete(reviewReport, reviewErr)
		if (sourceSanityRepairRequired || verificationRepairRequired) && reviewErr == nil {
			if ReviewRepairResolvedVerificationFailure(reviewReport) {
				if verificationRepairRequired {
					_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(cfg.Workspace.Root, verificationPlan, opts.JSONEvents, stdout)
					report.Verification = verificationEvidence
					progress.Executor.Verification = append([]PhaseEvidenceCheck(nil), verificationEvidence...)
					if verificationErr != nil {
						runErr = verificationErr
						progress.Status = ProgressStatusFailed
						progress.FailureKind = RunFailureVerificationRequired
						progress.FailureReason = sanitizeProgressReason(verificationErr.Error())
					} else {
						runErr = nil
						verificationRepairRequired = false
					}
				} else {
					runErr = nil
				}
				sourceSanityRepairRequired = false
			} else {
				progress.Status = ProgressStatusFailed
				if verificationRepairRequired {
					progress.FailureKind = RunFailureVerificationRequired
				} else {
					progress.FailureKind = RunFailureSourceSanityFailed
				}
				progress.FailureReason = sanitizeProgressReason(runErr.Error())
			}
		}
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
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: progress.FailureKind, FailureReason: progress.FailureReason})
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: progress.FailureKind, FailureReason: progress.FailureReason})
	}
	if progress.Status == ProgressStatusBlocked {
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: progress.FailureKind, FailureReason: progress.FailureReason})
	}
	exitCode := 0
	if progress.Status == ProgressStatusDegraded {
		exitCode = 1
	}
	return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: exitCode, FailureKind: progress.FailureKind, FailureReason: progress.FailureReason})
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
	allowedEditPaths := mergeProgressPaths(state.AllowedEditPaths, planExecuteAllowedEditPaths(opts, prompt))
	if len(state.EditSetObligations) == 0 {
		state.EditSetObligations = planExecuteEditSetObligations(opts, prompt)
	}
	state.EditSetObligations = state.EditSetObligations.Normalized()
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "resume_state_loaded", ProgressStateEventFields(state, statePath))
	if opts.OnPhase != nil {
		opts.OnPhase("reviewing", "Resuming review-repair from executor progress state...")
	}
	reviewSessionID := NewPlanExecuteSessionID()
	state.MarkReviewStarted(reviewSessionID)
	_, err = saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved")
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
	reviewScopeContract := NewPlanScopeContract(requiredEditPaths, allowedEditPaths, state.EditSetObligations)
	if writeAllowedExplicit {
		reviewScopeContract = reviewScopeContract.WithWriteAllowedPaths(writeAllowedPaths)
	}
	reviewOpts.ScopeContract = reviewScopeContract
	report := state.ExecutorPhaseReport()
	reviewBaseline := state.ReviewBaseline
	reviewReport, reviewErr := RunReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec)
	state.MarkReviewComplete(reviewReport, reviewErr)
	if reviewErr == nil && ExecutorVerificationFailed(report, fmt.Errorf("verification failed")) && ReviewRepairResolvedVerificationFailure(reviewReport) {
		verificationPlan := PlanVerification(VerificationPlannerInput{
			WorkspaceRoot: cfg.Workspace.Root,
			Prompt:        prompt,
			ChangedPaths:  planChangedPaths(plan),
		})
		_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(cfg.Workspace.Root, verificationPlan, opts.JSONEvents, stdout)
		state.Executor.Verification = append([]PhaseEvidenceCheck(nil), verificationEvidence...)
		if verificationErr != nil {
			reviewErr = verificationErr
			state.Status = ProgressStatusFailed
			state.FailureKind = RunFailureVerificationRequired
			state.FailureReason = sanitizeProgressReason(verificationErr.Error())
		}
	}
	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved")
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
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: state.FailureKind, FailureReason: state.FailureReason})
	}
	if state.Status == ProgressStatusBlocked {
		return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: 1, FailureKind: state.FailureKind, FailureReason: state.FailureReason})
	}
	resumeExitCode := 0
	if state.Status == ProgressStatusDegraded {
		resumeExitCode = 1
	}
	return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: resumeExitCode, FailureKind: state.FailureKind, FailureReason: state.FailureReason})
}

func saveRunProgressState(root string, state *ProgressState, jsonEvents bool, stdout io.Writer, eventType string, suppressRecovery ...bool) (string, error) {
	statePath, err := SaveProgressState(root, state)
	if err != nil {
		return statePath, err
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, ProgressStateEventFields(*state, statePath))
	if planExecuteProgressRequiresRecovery(*state) && !(len(suppressRecovery) > 0 && suppressRecovery[0]) {
		now := time.Now().UTC()
		record, recordPath, err := recovery.WritePlanExecuteFailureRecord(root, RecoveryInputFromProgress(*state, statePath), now)
		if err != nil {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "recovery_failure_record_failed", map[string]any{
				"session_id": state.SessionID,
				"state_path": statePath,
				"reason":     err.Error(),
			})
			return statePath, err
		}
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "recovery_failure_record_written", map[string]any{
			"session_id":        state.SessionID,
			"failure_id":        record.FailureID,
			"failure_class":     record.Failure.Class,
			"failure_kind":      record.Failure.Kind,
			"record_path":       recordPath,
			"next_valid_action": record.NextAction,
		})
		packet, packetPath, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, now)
		if err != nil {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "recovery_decomposition_packet_failed", map[string]any{
				"session_id":  state.SessionID,
				"failure_id":  record.FailureID,
				"record_path": recordPath,
				"reason":      err.Error(),
			})
			return statePath, err
		}
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "recovery_decomposition_packet_written", map[string]any{
			"session_id":        state.SessionID,
			"failure_id":        record.FailureID,
			"packet_id":         packet.PacketID,
			"packet_path":       packetPath,
			"candidate_atoms":   len(packet.CandidateAtoms),
			"next_valid_action": packet.NextAction,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "recovery: failure_record=%s packet=%s next_valid_action=%s\n", recordPath, packetPath, packet.NextAction)
		}
	}
	return statePath, nil
}

func planExecuteProgressRequiresRecovery(state ProgressState) bool {
	switch strings.TrimSpace(state.Status) {
	case ProgressStatusFailed, ProgressStatusDegraded, ProgressStatusBlocked:
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
}

func finalizePlanExecuteRunResult(root string, state ProgressState, progressPath string, jsonEvents bool, stdout io.Writer, input planExecuteRunResultInput) RunResult {
	state.SessionID = strings.TrimSpace(state.SessionID)
	input.FailureKind = firstProgressNonEmptyString(input.FailureKind, state.FailureKind)
	input.FailureReason = firstProgressNonEmptyString(input.FailureReason, state.FailureReason)
	recoveryInput := planExecuteTerminalRecoveryInput(root, state.SessionID)
	record := NewPlanExecuteTerminalCompletionRecord(state, PlanExecuteTerminalCompletionInput{
		ProgressPath:                 progressPath,
		ExitCode:                     input.ExitCode,
		SourceSatisfied:              input.SourceSatisfied,
		SourceSatisfiedEvidencePaths: input.SourceSatisfiedEvidencePaths,
		FailureID:                    recoveryInput.failureID,
		RecordPath:                   recoveryInput.recordPath,
		PacketPath:                   recoveryInput.packetPath,
		NextAction:                   recoveryInput.nextAction,
		CompletedAt:                  time.Now().UTC(),
	})
	if input.FailureKind != "" {
		record.FailureKind = input.FailureKind
	}
	if input.FailureReason != "" {
		record.FailureReason = input.FailureReason
	}
	record = NormalizePlanExecuteTerminalCompletionRecord(record)
	completionPath, err := SavePlanExecuteTerminalCompletionRecord(root, record)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "terminal_completion_save_failed", map[string]any{
			"session_id": state.SessionID,
			"state_path": progressPath,
			"reason":     err.Error(),
		})
		return RunResult{
			ExitCode:        1,
			SessionID:       state.SessionID,
			ResumeStatePath: progressPath,
			FailureKind:     firstProgressNonEmptyString(input.FailureKind, "terminal_completion_save_failed"),
			FailureReason:   sanitizeProgressReason(firstProgressNonEmptyString(input.FailureReason, err.Error())),
			PlannerAnswer:   input.PlannerAnswer,
		}
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "terminal_completion", PlanExecuteTerminalCompletionEventFields(record, completionPath))
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
		nextAction: strings.TrimSpace(record.NextAction),
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

// RecoveryInputFromProgress maps plan-execute progress into the recovery failure input contract.
func RecoveryInputFromProgress(state ProgressState, statePath string) recovery.PlanExecuteFailureInput {
	return recovery.PlanExecuteFailureInput{
		SessionID:         state.SessionID,
		ProgressStatePath: statePath,
		Phase:             state.Phase,
		Status:            state.Status,
		FailureKind:       state.FailureKind,
		FailureReason:     state.FailureReason,
		PromptSHA256:      state.PromptSHA256,
		PromptFile:        state.PromptFile,
		PlanPath:          state.PlanPath,
		PlanID:            state.PlanID,
		PackID:            state.PackID,
		RequiredEditPaths: state.RequiredEditPaths,
		AllowedEditPaths:  state.AllowedEditPaths,
		ChangedPaths:      state.ChangedPaths,
		TotalEdits:        state.TotalEdits,
		ExecutorStatus:    state.Executor.Status,
		ExecutorFailures:  state.Executor.Failures,
		ReviewStatus:      state.Review.Status,
		ReviewReasonClass: state.Review.ReasonClass,
		ReviewError:       state.Review.Error,
	}
}
