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
)

type RunResult struct {
	ExitCode      int
	SessionID     string
	FailureKind   string
	FailureReason string
	PlannerAnswer string
}

const RunFailurePlannerBlocked = "planner_blocked"

func Run(cfg config.Config, opts Options, promptRun PromptRunner, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) RunResult {
	defer cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	cleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	sessionStart := time.Now().Add(-1 * time.Second)
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		sessionID = newPlanExecuteSessionID()
	}
	cfg.Workspace.SessionID = sessionID
	logCLIPhase(rec, "plan_execute_start", map[string]any{
		"json_events": opts.JSONEvents,
		"session_id":  sessionID,
	})
	queueRelease := AcquireQueueSlot(cfg, QueueAcquireOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		SessionID:  sessionID,
	}, rec, stdout, stderr)
	defer queueRelease()

	prompt, err := planExecutePrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}
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

	plannerPrompt := planExecutePlannerSystemAddendum(requiredEditPaths, allowedEditPaths) + "\n\n" + prompt
	plannerMaxTokens := 16384
	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}
	answer := ""
	emitPhase := opts.OnPhase
	if submitted, result, reason, editCount := planExecuteTryAutoExactPlan(cfg, prompt, requiredEditPaths); submitted {
		answer = result.Summary
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_submitted", map[string]any{
			"edits":  editCount,
			"reason": reason,
		})
		if emitPhase != nil {
			emitPhase("executing", fmt.Sprintf("Applied %d auto-exact edit(s)", editCount))
		}
	} else {
		if strings.TrimSpace(reason) != "" {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "auto_exact_plan_skipped", map[string]any{
				"reason": reason,
				"error":  result.Error,
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
			fmt.Fprintln(stderr, err)
			return RunResult{ExitCode: 1, SessionID: sessionID}
		}
	}

	planPath, err := FindLatestPlanArtifact(cfg.Workspace.Root, sessionStart, sessionID)
	if err != nil {
		if agent.PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, requiredEditPaths) {
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
			return RunResult{ExitCode: 0, SessionID: sessionID}
		}
		if planExecuteAnswerDeclaresBlocker(answer) {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_blocked", map[string]any{
				"reason":  err.Error(),
				"blocker": planExecuteBlockerSummary(answer),
			})
			fmt.Fprintln(stderr, fmt.Errorf("planner phase reported a blocker before submitting a plan: %s", planExecuteBlockerSummary(answer)))
			if !opts.JSONEvents {
				fmt.Fprintln(stdout, answer)
			}
			return RunResult{
				ExitCode:      1,
				SessionID:     sessionID,
				FailureKind:   RunFailurePlannerBlocked,
				FailureReason: planExecuteBlockerSummary(answer),
				PlannerAnswer: answer,
			}
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_missing", map[string]any{
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_invalid", map[string]any{
			"path":   planPath,
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		return RunResult{ExitCode: 1, SessionID: sessionID}
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
	if emitPhase != nil {
		emitPhase("executing", fmt.Sprintf("Applying %d edit(s)...", len(plan.Edits)))
	}
	report, runErr := RunExecutorPhaseScoped(cfg, plan, requiredEditPaths, allowedEditPaths, opts.JSONEvents, stdout, opts.OnEditProgress)
	_ = sessionStart
	reviewReport := ReviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}
	var reviewErr error
	if runErr == nil && !opts.SkipReviewRepair {
		if emitPhase != nil {
			emitPhase("reviewing", "Verifying changes...")
		}
		reviewReport, reviewErr = RunReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, opts, stdout, promptRun, rec)
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
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return RunResult{ExitCode: 1, SessionID: sessionID}
	}
	return RunResult{ExitCode: 0, SessionID: sessionID}
}
