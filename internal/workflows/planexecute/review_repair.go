package planexecute

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	planartifact "idiom/internal/contracts/planartifact"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/sessionid"
	"idiom/internal/platform/textutil"
)

func RunReviewRepairPhaseWithDependencies(
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
	deps Dependencies,
) (report ReviewRepairPhaseReport, err error) {
	deps = normalizeDependencies(deps)

	changedPaths := planChangedPaths(plan)
	if len(changedPaths) == 0 {
		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusSkipped, StatusDetail: "no changed paths", Skipped: true}, nil
	}

	allowedRepairPaths := mergePlanExecutePaths(changedPaths, requiredEditPaths)
	evidence := CollectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)

	autoRepair, autoRepairErr := AutoRepairReviewPatchHygiene(cfg.Workspace.Root, changedPaths, evidence)
	if autoRepairErr != nil {
		if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairAutoPatchFailed, map[string]any{
			"reason": autoRepairErr.Error(),
		}); err != nil {
			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
		}

		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, fmt.Errorf("review-repair auto patch hygiene failed: %w", autoRepairErr)
	}

	if autoRepair.Repaired {
		if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairAutoPatchHygiene, map[string]any{
			"paths": autoRepair.Paths,
			"lines": autoRepair.Lines,
		}); err != nil {
			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
		}

		evidence = CollectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	}

	evidence.PhaseEvidence = collectPlanExecutePhaseEvidence(cfg.Workspace.Root, "review_repair", plan, changedPaths, requiredEditPaths, allowedRepairPaths, evidence, executorReport, deps.SourceSanityCovers)

	editSetObligations := planExecuteScopeContract(opts, originalPrompt).EditSetObligations

	evidence.PhaseEvidence.EditSetObligations = editSetObligations
	evidence.PhaseEvidence.OpenObligations = append(evidence.PhaseEvidence.OpenObligations, editSetObligations.MissingOpenObligations(changedPaths)...)

	reviewSessionID := strings.TrimSpace(opts.ReviewSessionID)

	if reviewSessionID == "" {
		reviewSessionID = sessionid.New()
	}

	reviewCfg := cfg

	reviewCfg.Workspace.SessionID = reviewSessionID

	reviewStart := time.Now().Add(-1 * time.Second)
	reviewLifecycle := beginPlanExecuteReviewRepairLifecycle(cfg, opts, reviewSessionID, changedPaths, allowedRepairPaths, stdout)
	defer func() {
		emitPlanExecuteReviewRepairLifecycleCloseout(stdout, opts.JSONEvents, reviewLifecycle, report, err)
	}()

	if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairStart, map[string]any{
		"session_id":                reviewSessionID,
		"changed_paths":             changedPaths,
		"allowed_paths":             allowedRepairPaths,
		"edit_set_obligations":      editSetObligations,
		"edit_set_obligation_count": len(editSetObligations),
		"phase_evidence":            phaseEvidenceTelemetry(evidence.PhaseEvidence),
	}); err != nil {
		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
	}

	if planExecuteDeterministicCleanReviewEnabled() {
		reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
		if reason == "" {
			reason = string(editSetObligations.RejectionReason(changedPaths))
		}

		if reason == "" && len(evidence.PhaseEvidence.OpenObligations) == 0 {
			if err := emitPlanExecuteSyntheticTurnSummary(stdout, opts.JSONEvents, "review_repair_deterministic_pass"); err != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
			}
			if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairComplete, map[string]any{
				"status":        "pass",
				"repairs":       0,
				"deterministic": true,
			}); err != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
			}

			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusPassed, StatusDetail: "deterministic evidence", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}
	}

	prompt := PlanExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
	reviewMaxTokens := opts.MaxTokens

	if reviewMaxTokens <= 0 {
		reviewMaxTokens = 8192
	}

	var (
		planPath string
		found    bool
	)

	for attempt := 0; ; attempt++ {
		answer, err := promptRun(reviewCfg, prompt, RunOptions{
			JSONEvents:                  opts.JSONEvents,
			TaskFamily:                  string(autonomy.TaskFamilyPlanThenExecuteReview),
			PlannerOwnedVerification:    true,
			MaxTokensOverride:           reviewMaxTokens,
			DisableThinking:             true,
			UseActiveModel:              opts.UseActiveModel,
			ReviewRepairOpenObligations: append([]string(nil), evidence.PhaseEvidence.OpenObligations...),
			Telemetry:                   rec,
		}, stdout)
		if err != nil {
			reasonClass := reviewRepairDegradedReasonClass(err)
			if emitErr := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairDegraded, map[string]any{
				"status":        string(plancontract.PlanExecuteReviewStatusDegraded),
				"reason_class":  reasonClass,
				"status_detail": "review unavailable",
				"changed_paths": changedPaths,
				"allowed_paths": allowedRepairPaths,
			}); emitErr != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(emitErr)
			}

			return ReviewRepairPhaseReport{
				Status:       plancontract.PlanExecuteReviewStatusDegraded,
				StatusDetail: "review unavailable",
				AutoRepairs:  autoRepair.Lines,
				ReasonClass:  reasonClass,
			}, nil
		}

		planPath, found, err = findOptionalReviewPlanArtifact(cfg.Workspace.Root, reviewStart, reviewSessionID, deps.FindPlanArtifact)
		if err != nil {
			if emitErr := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairFailed, map[string]any{
				"reason": err.Error(),
			}); emitErr != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(emitErr)
			}

			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, err
		}

		if found {
			break
		}

		if plancontract.ReviewRepairAnswerDeclaresPass(answer) && plancontract.ReviewRepairTextHasChecklist(answer) {
			reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
			if reason == "" {
				reason = ReviewAnswerPassContradictionReason(answer)
			}

			if reason == "" {
				reason = string(editSetObligations.RejectionReason(changedPaths))
			}

			if reason != "" {
				if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairPassRejected, map[string]any{
					"reason":                    reason,
					"edit_set_obligations":      editSetObligations,
					"edit_set_obligation_count": len(editSetObligations),
					"retry_available":           attempt == 0,
				}); err != nil {
					return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ChecklistFound: true}, planExecuteJSONEventWriteError(err)
				}

				if attempt == 0 {
					if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairRetry, map[string]any{
						"reason": reason,
					}); err != nil {
						return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ChecklistFound: true}, planExecuteJSONEventWriteError(err)
					}

					prompt = planExecuteReviewRepairRejectedPassRetryPrompt(prompt, reason)

					continue
				}

				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ChecklistFound: true}, fmt.Errorf("review-repair phase rejected contradictory `Decision: pass`: %s", reason)
			}

			if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairComplete, map[string]any{
				"status":  "pass",
				"repairs": 0,
			}); err != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ChecklistFound: true}, planExecuteJSONEventWriteError(err)
			}

			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusPassed, StatusDetail: "no corrective edits", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}

		if reviewAnswerDeclaresBlocker(answer) {
			blocker := reviewRepairBlockerSummary(answer)
			failureReason := reviewRepairBlockerFailureReason(answer)
			if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairBlocked, map[string]any{
				"status":         "blocked",
				"blocker":        blocker,
				"failure_reason": failureReason,
			}); err != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, FailureReason: failureReason}, planExecuteJSONEventWriteError(err)
			}

			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusBlocked, StatusDetail: blocker, FailureReason: failureReason}, nil
		}

		if attempt == 0 {
			if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairRetry, map[string]any{
				"reason": "invalid_terminal_outcome",
			}); err != nil {
				return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, planExecuteJSONEventWriteError(err)
			}

			prompt = planExecuteReviewRepairInvalidOutcomeRetryPrompt(prompt, answer)

			continue
		}

		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, StatusDetail: "inconclusive"}, fmt.Errorf("review-repair phase produced no corrective plan and no `Decision: pass` with `Verification Checklist`")
	}

	repairPlan, err := deps.LoadPlanArtifact(planPath)
	if err != nil {
		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ReviewPlan: planPath}, fmt.Errorf("read review-repair plan artifact %q: %w", planPath, err)
	}

	if !plancontract.ReviewRepairTextHasChecklist(repairPlan.Rationale) {
		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ReviewPlan: planPath}, fmt.Errorf("review-repair plan %q is missing `Verification Checklist` in submit_plan rationale", planPath)
	}

	repairPaths := planChangedPaths(repairPlan)
	combinedPaths := mergePlanExecutePaths(changedPaths, repairPaths)

	if reason := editSetObligations.RejectionReason(combinedPaths); reason != "" {
		if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairPlanRejected, map[string]any{
			"reason":                    reason,
			"review_plan":               planPath,
			"changed_paths":             changedPaths,
			"repair_paths":              repairPaths,
			"combined_paths":            combinedPaths,
			"edit_set_obligations":      editSetObligations,
			"edit_set_obligation_count": len(editSetObligations),
		}); err != nil {
			return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ReviewPlan: planPath, ChecklistFound: true}, planExecuteJSONEventWriteError(err)
		}

		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ReviewPlan: planPath, ChecklistFound: true}, fmt.Errorf("review-repair phase rejected corrective plan: %s", reason)
	}

	repairReport, err := RunExecutorPhaseWithDependencies(cfg, repairPlan, ExecutorPhaseOptions{
		Context:              opts.Context,
		AllowedEditPaths:     allowedRepairPaths,
		WriteAllowedPaths:    []string(opts.WriteAllowedPaths),
		WriteAllowedExplicit: opts.WriteAllowedPathsExplicit,
		AuthorizedNewFiles:   append([]string(nil), opts.AuthorizedNewFiles...),
		JSONEvents:           opts.JSONEvents,
		Stdout:               stdout,
	}, deps)
	if err != nil {
		return ReviewRepairPhaseReport{
			Status:               plancontract.PlanExecuteReviewStatusFailed,
			Repairs:              repairReport.Succeeded,
			AutoRepairs:          autoRepair.Lines,
			ReviewPlan:           planPath,
			RepairPaths:          repairPaths,
			CombinedChangedPaths: combinedPaths,
			ChecklistFound:       true,
		}, fmt.Errorf("review-repair executor failed: %w", err)
	}

	if err := emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, plancontract.PlanExecuteEventReviewRepairComplete, map[string]any{
		"status":        "repaired",
		"repairs":       repairReport.Succeeded,
		"review_plan":   planPath,
		"changed_paths": allowedRepairPaths,
	}); err != nil {
		return ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed, ReviewPlan: planPath, ChecklistFound: true}, planExecuteJSONEventWriteError(err)
	}

	return ReviewRepairPhaseReport{
		Status:               plancontract.PlanExecuteReviewStatusRepaired,
		Repairs:              repairReport.Succeeded,
		AutoRepairs:          autoRepair.Lines,
		ReviewPlan:           planPath,
		RepairPaths:          repairPaths,
		CombinedChangedPaths: combinedPaths,
		ChecklistFound:       true,
		SourceSanity:         append([]plancontract.PhaseEvidenceCheck(nil), repairReport.SourceSanity...),
		Verification:         append([]plancontract.PhaseEvidenceCheck(nil), repairReport.Verification...),
		VerificationCommands: append([]plancontract.VerificationCommand(nil), repairReport.VerificationCommands...),
		VerificationSkips:    append([]plancontract.VerificationSkip(nil), repairReport.VerificationSkips...),
		VerificationRejects:  append([]plancontract.VerificationReject(nil), repairReport.VerificationRejects...),
	}, nil
}

func reviewRepairDegradedReasonClass(err error) string {
	if err == nil {
		return ""
	}

	switch failures.CodeOf(err) {
	case failures.Canceled:
		return "backend_canceled"
	case failures.DeadlineExceeded:
		return "backend_timeout"
	case failures.Unavailable, failures.NotFound:
		return "backend_unreachable"
	case failures.Unauthenticated, failures.PermissionDenied, failures.ResourceExhausted,
		failures.InvalidArgument, failures.Internal, failures.DataLoss:
		return string(plancontract.PlanExecuteFailureReviewModelError)
	}

	reason := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case reason == "":
		return string(plancontract.PlanExecuteFailureReviewModelError)
	case strings.Contains(reason, "eof"):
		return "backend_eof"
	case strings.Contains(reason, "connection refused"), strings.Contains(reason, "connect: connection"):
		return "backend_unreachable"
	case strings.Contains(reason, "context deadline"), strings.Contains(reason, "timeout"), strings.Contains(reason, "deadline exceeded"):
		return "backend_timeout"
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "cancelled"):
		return "backend_canceled"
	default:
		return string(plancontract.PlanExecuteFailureReviewModelError)
	}
}

func phaseEvidencePassRejectionReason(bundle PhaseEvidenceBundle) string {
	for _, obligation := range bundle.OpenObligations {
		obligation = strings.TrimSpace(obligation)
		switch {
		case obligation == "patch_hygiene_failed":
			return "patch_hygiene_failed"
		case strings.HasPrefix(obligation, "source_sanity_failed:"),
			strings.HasPrefix(obligation, "source_sanity_error:"),
			strings.HasPrefix(obligation, "verification_failed:"),
			strings.HasPrefix(obligation, "verification_error:"),
			strings.HasPrefix(obligation, "submitted_edit_failed:"):
			return string(plancontract.PlanExecuteFailureVerificationFailed)
		case strings.HasPrefix(obligation, "required_path_uncovered:"):
			return obligation
		}
	}

	return ""
}

func planExecuteDeterministicCleanReviewEnabled() bool {
	return textutil.EqualTrimFold(os.Getenv("IDIOM_PLAN_EXECUTE_DETERMINISTIC_CLEAN_REVIEW"), "true")
}

func findOptionalReviewPlanArtifact(root string, notBefore time.Time, sessionID string, finder PlanArtifactFinder) (string, bool, error) {
	finder = normalizeDependencies(Dependencies{FindPlanArtifact: finder}).FindPlanArtifact
	path, err := finder(root, notBefore, sessionID)
	if err == nil {
		return path, true, nil
	}

	if errors.Is(err, ErrPlanArtifactNotFound) {
		return "", false, nil
	}

	return "", false, err
}
