package planexecute

import (
	"context"
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
	RunFailureTerminalCompletionContractFailed   = "terminal_completion_contract_failed"
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

	requiredAcceptanceEvidenceIDs := planExecuteRequiredAcceptanceEvidenceIDsFromPrompt(prompt)

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
			if len(state.RequiredAcceptanceEvidenceIDs) == 0 {
				state.RequiredAcceptanceEvidenceIDs = requiredAcceptanceEvidenceIDs
			}

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
	allowedEditPaths, candidateRepairs := repairMissingAllowedCandidatePaths(cfg.Workspace.Root, allowedEditPaths)
	if len(candidateRepairs) > 0 {
		scopeContract = planExecuteContractWithWriteEnvelope(NewPlanScopeContract(requiredEditPaths, allowedEditPaths, editSetObligations), writeAllowedPaths, writeAllowedExplicit)
	}
	plannerPromptSource := applyCandidatePathRepairsToPrompt(prompt, candidateRepairs)
	authorizedNewFiles := mergePlanExecutePaths(opts.AuthorizedNewFiles)
	verificationPlan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: cfg.Workspace.Root,
		Prompt:        plannerPromptSource,
	})
	verificationCommands := verificationPlan.CommandStrings()

	opts.ScopeContract = scopeContract
	opts.EditSetObligations = editSetObligations
	opts.AuthorizedNewFiles = authorizedNewFiles

	promptRequiredEditPaths := RequiredEditPathsFromPrompt(prompt)
	explicitRequiredEditPaths := planExecuteUniqueRequiredPaths([]string(opts.RequiredEditPaths))
	logCLIPhase(rec, "plan_execute_required_edit_paths", map[string]any{
		"count":                     len(requiredEditPaths),
		"explicit_count":            len(explicitRequiredEditPaths),
		"prompt_count":              len(promptRequiredEditPaths),
		"allowed_count":             len(allowedEditPaths),
		"candidate_repairs":         candidateRepairs,
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
		"candidate_repairs":         candidateRepairs,
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

	progress.RequiredAcceptanceEvidenceIDs = requiredAcceptanceEvidenceIDs
	progress.SetWriteAllowedEnvelope(writeAllowedPaths, writeAllowedExplicit)
	progress.SetScopeContractID(scopeContract.ContractID)

	progress.EditSetObligations = editSetObligations

	progressPath, err := saveRunProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil {
		fmt.Fprintln(stderr, err)

		return RunResult{ExitCode: 1, SessionID: sessionID}
	}

	plannerBrief := appendPlanExecuteScopeToBriefBody(plannerPromptSource, requiredEditPaths, allowedEditPaths)
	contextPackSection := BuildPromptContextPackSection(cfg, PromptContextPackOptions{
		Mode:              "plan",
		Title:             plannerPromptSource,
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

	autoExact := planExecuteTryAutoExactPlan(cfg, plannerPromptSource, autoExactScopePaths)
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

			return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{
				ExitCode:                     0,
				SourceSatisfied:              true,
				SourceSatisfiedEvidencePaths: sourceSatisfiedPaths,
				AcceptanceEvidenceIDs:        planExecuteHarnessAcceptanceEvidenceIDs(progress),
			})
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

	if mismatch := preflightPlanMissingPathAuthorization(cfg.Workspace.Root, authorizedNewFiles, plan); mismatch != nil {
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
	report, runErr := RunExecutorPhaseScopedWithVerificationPlanAndWriteScopeAuthorizedNewFiles(cfg, plan, executorRequiredEditPaths, allowedEditPaths, writeAllowedPaths, writeAllowedExplicit, authorizedNewFiles, verificationPlan, opts.JSONEvents, stdout, progressCallback)

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
				sourceMgr := workspace.NewManager(cfg.Workspace)
				_, sourceEvidence, sourceErr := runPlanExecuteSourceSanity(context.Background(), sourceMgr, cfg.Workspace.Root, planChangedPaths(plan), opts.JSONEvents, stdout)

				report.SourceSanity = sourceEvidence
				progress.Executor.SourceSanity = append([]PhaseEvidenceCheck(nil), sourceEvidence...)

				if sourceErr != nil {
					runErr = sourceErr
					progress.Status = ProgressStatusFailed
					progress.FailureKind = RunFailureSourceSanityFailed
					progress.FailureReason = sanitizeProgressReason(sourceErr.Error())
				} else {
					_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlanWithParentContract(cfg.Workspace.Root, verificationPlan, progress.ScopeContractID, opts.JSONEvents, stdout)

					report.Verification = verificationEvidence
					progress.Executor.Verification = append([]PhaseEvidenceCheck(nil), verificationEvidence...)
					report.VerificationCommands = append([]VerificationCommand(nil), verificationPlan.Commands...)
					report.VerificationSkips = append([]VerificationSkip(nil), verificationPlan.Skips...)
					report.VerificationRejects = append([]VerificationReject(nil), verificationPlan.Rejects...)
					progress.Executor.VerificationCommands = append([]VerificationCommand(nil), verificationPlan.Commands...)
					progress.Executor.VerificationSkips = append([]VerificationSkip(nil), verificationPlan.Skips...)
					progress.Executor.VerificationRejects = append([]VerificationReject(nil), verificationPlan.Rejects...)

					if verificationErr != nil {
						runErr = verificationErr
						progress.Status = ProgressStatusFailed
						progress.FailureKind = RunFailureVerificationRequired
						progress.FailureReason = sanitizeProgressReason(verificationErr.Error())
					} else {
						runErr = nil
						verificationRepairRequired = false
						sourceSanityRepairRequired = false
						progress.Status = ProgressStatusCompleted
						progress.FailureKind = ""
						progress.FailureReason = ""
					}
				}
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

	return finalizePlanExecuteRunResult(cfg.Workspace.Root, progress, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: exitCode, FailureKind: progress.FailureKind, FailureReason: progress.FailureReason, AcceptanceEvidenceIDs: planExecuteHarnessAcceptanceEvidenceIDs(progress)})
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
	if strings.TrimSpace(state.ScopeContractID) == "" {
		state.SetScopeContractID(NewPlanScopeContract(requiredEditPaths, allowedEditPaths, state.EditSetObligations).ContractID)
	}

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

	sourceSanityResumeRepairRequired := ExecutorSourceSanityFailed(report, fmt.Errorf("source_sanity failed"))
	verificationResumeRepairRequired := ExecutorVerificationFailed(report, fmt.Errorf("verification failed"))

	if reviewErr == nil && (sourceSanityResumeRepairRequired || verificationResumeRepairRequired) && ReviewRepairResolvedVerificationFailure(reviewReport) {
		verificationPlan := PlanVerification(VerificationPlannerInput{
			WorkspaceRoot: cfg.Workspace.Root,
			Prompt:        prompt,
			ChangedPaths:  planChangedPaths(plan),
		})
		sourceMgr := workspace.NewManager(cfg.Workspace)
		_, sourceEvidence, sourceErr := runPlanExecuteSourceSanity(context.Background(), sourceMgr, cfg.Workspace.Root, planChangedPaths(plan), opts.JSONEvents, stdout)

		state.Executor.SourceSanity = append([]PhaseEvidenceCheck(nil), sourceEvidence...)

		if sourceErr != nil {
			reviewErr = sourceErr
			state.Status = ProgressStatusFailed
			state.FailureKind = RunFailureSourceSanityFailed
			state.FailureReason = sanitizeProgressReason(sourceErr.Error())
		} else {
			_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlanWithParentContract(cfg.Workspace.Root, verificationPlan, state.ScopeContractID, opts.JSONEvents, stdout)

			state.Executor.Verification = append([]PhaseEvidenceCheck(nil), verificationEvidence...)
			state.Executor.VerificationCommands = append([]VerificationCommand(nil), verificationPlan.Commands...)
			state.Executor.VerificationSkips = append([]VerificationSkip(nil), verificationPlan.Skips...)
			state.Executor.VerificationRejects = append([]VerificationReject(nil), verificationPlan.Rejects...)

			if verificationErr != nil {
				reviewErr = verificationErr
				state.Status = ProgressStatusFailed
				state.FailureKind = RunFailureVerificationRequired
				state.FailureReason = sanitizeProgressReason(verificationErr.Error())
			} else {
				state.Status = ProgressStatusCompleted
				state.FailureKind = ""
				state.FailureReason = ""
			}
		}

		if reviewErr != nil && state.FailureKind == "" {
			state.Status = ProgressStatusFailed
			state.FailureKind = RunFailureVerificationRequired
			state.FailureReason = sanitizeProgressReason(reviewErr.Error())
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

	return finalizePlanExecuteRunResult(cfg.Workspace.Root, state, progressPath, opts.JSONEvents, stdout, planExecuteRunResultInput{ExitCode: resumeExitCode, FailureKind: state.FailureKind, FailureReason: state.FailureReason, AcceptanceEvidenceIDs: planExecuteHarnessAcceptanceEvidenceIDs(state)})
}

func planExecuteHarnessAcceptanceEvidenceIDs(state ProgressState) []string {
	if state.Status != ProgressStatusCompleted {
		return nil
	}

	return append([]string(nil), state.RequiredAcceptanceEvidenceIDs...)
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
	SourceSatisfiedEvidence      []plancontract.SourceSatisfiedEvidenceRef
	AcceptanceEvidenceIDs        []string
}

func finalizePlanExecuteRunResult(root string, state ProgressState, progressPath string, jsonEvents bool, stdout io.Writer, input planExecuteRunResultInput) RunResult {
	state.SessionID = strings.TrimSpace(state.SessionID)
	input.FailureKind = firstProgressNonEmptyString(input.FailureKind, state.FailureKind)
	input.FailureReason = firstProgressNonEmptyString(input.FailureReason, state.FailureReason)

	if len(input.AcceptanceEvidenceIDs) == 0 {
		input.AcceptanceEvidenceIDs = state.AcceptanceEvidenceIDs
	}

	if len(input.AcceptanceEvidenceIDs) > 0 && len(state.AcceptanceEvidenceIDs) == 0 {
		state.AcceptanceEvidenceIDs = input.AcceptanceEvidenceIDs
	}

	recoveryInput := planExecuteTerminalRecoveryInput(root, state.SessionID)
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
		record.FailureKind = input.FailureKind
	}

	if input.FailureReason != "" {
		record.FailureReason = input.FailureReason
	}

	record = NormalizePlanExecuteTerminalCompletionRecord(record)
	if record.TerminalClass != PlanExecuteTerminalClassSuccess {
		record.AcceptanceEvidenceIDs = nil
		state.AcceptanceEvidenceIDs = nil

		if record.TerminalClass == PlanExecuteTerminalClassFailed {
			state.Status = ProgressStatusFailed
			state.FailureKind = firstProgressNonEmptyString(input.FailureKind, record.FailureKind)
			state.FailureReason = firstProgressNonEmptyString(input.FailureReason, record.FailureReason)
		}

		record = NormalizePlanExecuteTerminalCompletionRecord(record)

		if savedPath, saveErr := SaveProgressState(root, &state); saveErr == nil && savedPath != "" {
			progressPath = savedPath
		}
	}

	if record.TerminalClass == PlanExecuteTerminalClassSuccess && len(record.ValidationFailures) > 0 {
		validationFailures := append([]string(nil), record.ValidationFailures...)

		input.ExitCode = 1
		input.FailureKind = RunFailureTerminalCompletionContractFailed
		input.FailureReason = sanitizeProgressReason("terminal completion success rejected: " + strings.Join(validationFailures, "; "))
		record.ExitCode = input.ExitCode
		record.FailureKind = input.FailureKind
		record.FailureReason = input.FailureReason
		record.AcceptanceEvidenceIDs = nil
		record.TerminalClass = PlanExecuteTerminalClassFailed
		record.Outcome = PlanExecuteTerminalOutcomeFailed
		record.FailureInvariants = planExecuteTerminalFailureInvariants(record)
		record.ValidationFailures = validationFailures
		state.Status = ProgressStatusFailed
		state.FailureKind = input.FailureKind
		state.FailureReason = input.FailureReason
		state.AcceptanceEvidenceIDs = nil

		if savedPath, saveErr := SaveProgressState(root, &state); saveErr == nil && savedPath != "" {
			progressPath = savedPath
		}

		recoveryRecord, recordPath, recoveryErr := recovery.WritePlanExecuteFailureRecord(root, RecoveryInputFromProgressWithInvariants(state, progressPath, planExecuteRecoveryInvariantFailures(record)), time.Now().UTC())
		if recoveryErr == nil {
			recoveryInput.failureID = recoveryRecord.FailureID
			recoveryInput.recordPath = recordPath
			recoveryInput.nextAction = recoveryRecord.NextAction

			if _, packetPath, packetErr := recovery.WriteDecompositionPacketForFailureRecord(root, recoveryRecord, time.Now().UTC()); packetErr == nil {
				recoveryInput.packetPath = packetPath
			}

			record.FailureID = recoveryInput.failureID
			record.RecordPath = recoveryInput.recordPath
			record.PacketPath = recoveryInput.packetPath
			record.NextAction = recoveryInput.nextAction
		}
	}

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
	return RecoveryInputFromProgressWithInvariants(state, statePath, nil)
}

func RecoveryInputFromProgressWithInvariants(state ProgressState, statePath string, invariantFailures []recovery.InvariantFailure) recovery.PlanExecuteFailureInput {
	if len(invariantFailures) == 0 {
		invariantFailures = progressInvariantFailures(state)
	}

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
		ParentAtomID:      state.ParentAtomID,
		ParentAtomTitle:   state.ParentAtomTitle,
		LockedContract:    progressLockedContractSnapshot(state),
		RequiredEditPaths: state.RequiredEditPaths,
		AllowedEditPaths:  state.AllowedEditPaths,
		ChangedPaths:      state.ChangedPaths,
		TotalEdits:        state.TotalEdits,
		ExecutorStatus:    state.Executor.Status,
		ExecutorFailures:  state.Executor.Failures,
		ReviewStatus:      state.Review.Status,
		ReviewReasonClass: state.Review.ReasonClass,
		ReviewError:       state.Review.Error,
		InvariantFailures: invariantFailures,
	}
}

func progressLockedContractSnapshot(state ProgressState) *recovery.ContractSnapshot {
	if strings.TrimSpace(state.ScopeContractID) == "" && strings.TrimSpace(state.ScopeContractHash) == "" && len(state.RequiredEditPaths) == 0 && len(state.AllowedEditPaths) == 0 && len(state.WriteAllowedPaths) == 0 && !state.WriteAllowedExplicit {
		return nil
	}

	return &recovery.ContractSnapshot{
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

func progressInvariantFailures(state ProgressState) []recovery.InvariantFailure {
	kind := strings.TrimSpace(state.FailureKind)
	reason := strings.TrimSpace(state.FailureReason)

	if kind == "" && reason == "" {
		return nil
	}

	return []recovery.InvariantFailure{{
		InvariantID:            firstProgressNonEmptyString(kind, "plan_execute_failed"),
		FieldPath:              "progress.failure",
		Expected:               "terminal_success_contract_satisfied",
		Actual:                 sanitizeProgressReason(firstProgressNonEmptyString(reason, kind)),
		RequiredRepairEvidence: []string{"passing_terminal_completion"},
	}}
}

func planExecuteRecoveryInvariantFailures(record PlanExecuteTerminalCompletionRecord) []recovery.InvariantFailure {
	var failures []recovery.InvariantFailure

	for _, invariant := range record.FailureInvariants {
		if strings.TrimSpace(invariant.Status) != "failed" {
			continue
		}

		failures = append(failures, recovery.InvariantFailure{
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

		failures = append(failures, recovery.InvariantFailure{
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
