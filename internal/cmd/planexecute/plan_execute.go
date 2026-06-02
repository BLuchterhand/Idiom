package planexecute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/backends"
	atomcmd "idiom/internal/cmd/atom"
	"idiom/internal/cmdruntime"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/plancontract"
	"idiom/internal/planexecute"
	"idiom/internal/recovery"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
	"idiom/internal/writerroute"
)

// planExecuteOptions configures `idiom plan-execute`.
type planExecuteOptions struct {
	ConfigPath         string
	Prompt             string
	PromptFile         string
	Model              string
	Resume             string
	ReviewSessionID    string
	JSONEvents         bool
	DisableThinking    bool
	MaxTokens          int
	RequiredEditPaths  planexecute.MultiFlag
	AllowedEditPaths   planexecute.MultiFlag
	EditSetObligations planexecute.EditSetObligations
	ScopeContract      planexecute.PlanScopeContract
	// PlannerOwnedVerification is accepted for compatibility with
	// `idiom implement` invocations that pass the flag. Plan-execute
	// always treats verification as planner-owned (the executor phase
	// is deterministic dispatch, not a verifier), so the flag is a
	// no-op here — keeping it lets existing lane scripts swap
	// `implement` for `plan-execute` without flag rewrites.
	PlannerOwnedVerification bool
	// AllowBuildTests is accepted for compatibility. Plan-execute extracts
	// explicit go test acceptance commands from the prompt and runs those
	// bounded commands after deterministic executor writes; this flag does
	// not expand that surface.
	AllowBuildTests bool
	// SkipReviewRepair disables the post-execute review-repair gate.
	// The default is to run one generic, action-capable review pass
	// after deterministic executor writes so obvious behavioral or
	// diff-level defects can be corrected before the lane reports done.
	SkipReviewRepair bool
}

type cliWriterRouteOptions struct {
	Mode              writerroute.WriterMode
	Lane              string
	TaskFamily        string
	PlannerModel      string
	ExecutorModel     string
	ReviewerModel     string
	ClassifierModel   string
	RequiredEditPaths []string
	ReasonCodes       []string
	VerificationOwner string
	RoleAliases       map[string]string
}

// RunMain is the CLI entry point for `idiom plan-execute`.
//
// Plan-then-execute is the third lane shape (alongside `idiom implement`
// and the harness-intercept lane): the planner runs FIRST with no
// per-edit write tools, emits a structured plan via submit_plan, and
// the orchestrator's executor phase iterates that plan and dispatches
// each edit deterministically against the workspace. The planner owns
// WHAT to change; the harness owns the plan contract and deterministic
// handoff to the executor.
func RunMain(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner) int {
	return RunMainWithTelemetry(args, stdout, stderr, promptRun, nil)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner, rec *telemetry.Recorder) int {
	opts, err := parsePlanExecuteOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer planexecute.CleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
	}
	planexecute.CleanupStalePlanArtifacts(cfg.Workspace.Root, 7*24*time.Hour, stderr)
	prompt, err := planExecutePrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var resumeState *planexecute.ProgressState
	resumeStatePath := ""
	if strings.TrimSpace(opts.Resume) != "" {
		state, statePath, found, err := planexecute.TryLoadProgressState(cfg.Workspace.Root, opts.Resume)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !found {
			fmt.Fprintln(stderr, fmt.Errorf("plan-execute progress state %s does not exist", statePath))
			return 1
		}
		if found {
			resumeState = &state
			resumeStatePath = statePath
		}
	}
	// Capture session start time + a fresh session UUID so the
	// executor phase only loads plan artifacts produced by THIS
	// planner run. Without these guards, a planner failure (budget
	// exhaustion, EOF, etc.) silently falls back to the most recent
	// plan on disk — which may be from an unrelated prior session
	// (or a CONCURRENT session running in the same workspace) and
	// dispatches the wrong edits. The sessionStart filter is a
	// belt-and-suspenders backup; the SessionID filter is the
	// strict attribution.
	sessionStart := time.Now().Add(-1 * time.Second)
	sessionID := newPlanExecuteSessionID()
	if resumeState != nil && strings.TrimSpace(resumeState.SessionID) != "" {
		sessionID = resumeState.SessionID
	}
	cfg.Workspace.SessionID = sessionID
	planexecute.LogCLIPhase(rec, "plan_execute_start", map[string]any{
		"json_events": opts.JSONEvents,
		"session_id":  sessionID,
		"resume":      resumeState != nil,
	})

	// Queue gate: if a `idiom queue serve` daemon is reachable, acquire
	// a lease for the model's pool BEFORE the planner phase starts so
	// concurrent dispatches don't both hammer the MLX backend at once
	// (Metal command-buffer pressure crashes 47781 under simultaneous
	// load — observed on 2026-05-10). The gate is best-effort: when no
	// daemon is running, MaybeAcquire returns a no-op release function
	// and we proceed un-gated, matching legacy behavior.
	queueRelease := planExecuteAcquireQueueSlot(cfg, opts, sessionID, rec, stdout, stderr)
	defer queueRelease()

	scopeContract := planExecuteScopeContract(opts, prompt)
	requiredEditPaths := scopeContract.RequiredCoveragePaths
	allowedEditPaths := scopeContract.AllowedCandidatePaths
	editSetObligations := scopeContract.EditSetObligations
	verificationPlan := planexecute.PlanVerification(planexecute.VerificationPlannerInput{
		WorkspaceRoot: cfg.Workspace.Root,
		Prompt:        prompt,
	})
	verificationCommands := verificationPlan.CommandStrings()
	opts.ScopeContract = scopeContract
	opts.EditSetObligations = editSetObligations
	promptRequiredEditPaths := planexecute.RequiredEditPathsFromPrompt(prompt)
	explicitRequiredEditPaths := planExecuteUniqueRequiredPaths([]string(opts.RequiredEditPaths))
	planexecute.LogCLIPhase(rec, "plan_execute_required_edit_paths", map[string]any{
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
	decision := buildCLIWriterRouteDecision(cfg, cliWriterRouteOptions{
		Mode:              writerroute.ModePlanSubmission,
		Lane:              "plan-execute",
		TaskFamily:        agent.PlanThenExecutePlannerTaskFamily,
		PlannerModel:      cfg.LlamaServer.Model,
		ExecutorModel:     cfg.LlamaServer.Model,
		ReviewerModel:     cfg.LlamaServer.Model,
		RequiredEditPaths: requiredEditPaths,
		ReasonCodes:       []string{"route.multi_file_plan_execute"},
		VerificationOwner: "planner_owned",
	})
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "writer_route_decision", decision.EventFields())
	if resumeState != nil {
		return runPlanExecuteResumeReviewRepair(cfg, opts, prompt, requiredEditPaths, *resumeState, resumeStatePath, stdout, stderr, promptRun, rec)
	}

	progress := planexecute.NewProgressState(sessionID, prompt, opts.PromptFile, requiredEditPaths, allowedEditPaths, time.Now().UTC())
	progress.EditSetObligations = editSetObligations
	if _, err := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// Phase 1: planner. The planner runs as an implementation lane
	// with the plan-then-execute system addendum that swaps per-edit
	// writes for submit_plan. We reuse the existing implement runner
	// because that lane already has the right read-side guards and
	// brief audit; only the tool surface differs.
	plannerBrief := planExecuteScopeToBriefBody(prompt, requiredEditPaths, allowedEditPaths)
	plannerPrompt := planExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, editSetObligations) + "\n\n" + plannerBrief
	plannerMaxTokens := planExecutePlannerMaxTokens(cfg.Workspace.Root, prompt, requiredEditPaths, allowedEditPaths)
	if opts.MaxTokens > 0 {
		plannerMaxTokens = opts.MaxTokens
	}
	answer := ""
	executorRequiredEditPaths := requiredEditPaths
	autoExactScopePaths := requiredEditPaths
	if len(autoExactScopePaths) == 0 {
		autoExactScopePaths = allowedEditPaths
	}
	autoExact := planExecuteTryAutoExactPlan(cfg, prompt, autoExactScopePaths)
	if autoExact.SourceSatisfied {
		if reason := editSetObligations.RejectionReason(autoExact.AppliedPaths); reason != "" {
			message := planexecute.EditSetObligationFailureMessage(reason)
			progress.Status = planexecute.ProgressStatusFailed
			progress.FailureKind = reason
			progress.FailureReason = truncatePlanExecuteProgressReason(message)
			_, _ = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
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
			return 1
		}
		answer = planExecuteAutoSourceSatisfiedAnswer(autoExactScopePaths)
		progress.Phase = planexecute.ProgressPhaseComplete
		progress.Status = planexecute.ProgressStatusCompleted
		_, _ = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
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
		return 0
	}
	if autoExact.Submitted {
		answer = autoExact.Result.Summary
		if len(autoExact.PendingPaths) > 0 {
			executorRequiredEditPaths = autoExact.PendingPaths
		}
		emitPlanExecuteSyntheticTurnSummary(stdout, opts.JSONEvents, "auto_exact_plan_submitted")
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
		answer, err = promptRun(cfg, plannerPrompt, cmdruntime.RunOptions{
			JSONEvents:               opts.JSONEvents,
			Resume:                   opts.Resume,
			TaskFamily:               agent.PlanThenExecutePlannerTaskFamily,
			PlannerOwnedVerification: true,
			// max_tokens for the planner phase is sized by edit shape.
			//
			// Originally 4096 ("planner emits one submit_plan + short answer,
			// 4096 is plenty"). That assumption broke when submit_plan's
			// `new_text` field carried the full content of NEW FILE edits —
			// a 400-line Go file plus structure overhead easily exceeds
			// 4096 tokens, and the response truncates silently mid-JSON
			// (manifesting as "completion gate rejected, no write present"
			// because the truncated JSON failed to parse as a valid tool
			// call). See task-64b for the failure repro.
			//
			// submit_plan now spills large persisted bodies into sidecar refs.
			// Existing-file plans can still require enough visible budget for a
			// multi-file executable JSON artifact before submit_plan can spill
			// large bodies. Keep the budget aligned with the shared
			// internal/planexecute runner; prompt/validation contracts, not a
			// smaller token ceiling, keep edit bodies bounded.
			MaxTokensOverride: plannerMaxTokens,
			DisableThinking:   opts.DisableThinking,
			Telemetry:         rec,
			ScopeContract:     scopeContract,
		}, stdout)
		if err != nil {
			progress.Status = planexecute.ProgressStatusFailed
			progress.FailureKind = "planner_failed"
			progress.FailureReason = truncatePlanExecuteProgressReason(err.Error())
			if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
				fmt.Fprintln(stderr, saveErr)
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	// Phase 2: executor. Locate the plan artifact written by THIS
	// session's planner phase and dispatch its edits in order.
	planPath, err := findLatestPlanArtifact(cfg.Workspace.Root, sessionStart, sessionID)
	if err != nil {
		sourceSatisfiedPaths := planExecuteSourceSatisfiedEvidencePaths(answer, requiredEditPaths, allowedEditPaths)
		if plancontract.SourceSatisfiedAnswerCoversPaths(answer, sourceSatisfiedPaths) {
			if reason := editSetObligations.RejectionReason(sourceSatisfiedPaths); reason != "" {
				message := planexecute.EditSetObligationFailureMessage(reason)
				progress.Status = planexecute.ProgressStatusFailed
				progress.FailureKind = reason
				progress.FailureReason = truncatePlanExecuteProgressReason(message)
				if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
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
				return 1
			}
			progress.Phase = planexecute.ProgressPhaseComplete
			progress.Status = planexecute.ProgressStatusCompleted
			_, _ = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
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
			return 0
		}
		if planExecuteAnswerDeclaresBlocker(answer) {
			progress.Status = planexecute.ProgressStatusFailed
			progress.FailureKind = planexecute.RunFailurePlannerBlocked
			progress.FailureReason = planExecuteBlockerSummary(answer)
			if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
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
			return 1
		}
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_missing", map[string]any{
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("planner phase did not produce a plan artifact: %w", err))
		// Still print the planner answer so the caller can see what
		// happened — this is a recoverable failure (the planner may
		// have stopped with a typed blocker).
		if !opts.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}
		progress.Status = planexecute.ProgressStatusFailed
		progress.FailureKind = "plan_artifact_missing"
		progress.FailureReason = truncatePlanExecuteProgressReason(err.Error())
		if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)
		}
		return 1
	}
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "plan_artifact_invalid", map[string]any{
			"path":   planPath,
			"reason": err.Error(),
		})
		fmt.Fprintln(stderr, fmt.Errorf("read plan artifact %q: %w", planPath, err))
		progress.Status = planexecute.ProgressStatusFailed
		progress.FailureKind = "plan_artifact_invalid"
		progress.FailureReason = truncatePlanExecuteProgressReason(err.Error())
		if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)
		}
		return 1
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
	changedPaths := planChangedPaths(plan)
	verificationPlan = planexecute.PlanVerification(planexecute.VerificationPlannerInput{
		WorkspaceRoot: cfg.Workspace.Root,
		Prompt:        prompt,
		ChangedPaths:  changedPaths,
	})
	verificationCommands = verificationPlan.CommandStrings()
	if reason := editSetObligations.RejectionReason(changedPaths); reason != "" {
		message := planexecute.EditSetObligationFailureMessage(reason)
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "planner_plan_rejected", map[string]any{
			"path":                      planPath,
			"reason":                    reason,
			"changed_paths":             changedPaths,
			"edit_set_obligations":      editSetObligations,
			"edit_set_obligation_count": len(editSetObligations),
		})
		progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, planexecute.ReviewRepairBaseline{})
		progress.Status = planexecute.ProgressStatusFailed
		progress.FailureKind = reason
		progress.FailureReason = truncatePlanExecuteProgressReason(message)
		if _, saveErr := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)
		}
		fmt.Fprintln(stderr, message)
		return 1
	}
	reviewBaseline := captureReviewRepairBaseline(cfg.Workspace.Root, changedPaths)
	progress.MarkPlanLoaded(cfg.Workspace.Root, planPath, plan, toSharedReviewRepairBaseline(reviewBaseline))
	if _, err = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var progressSaveErr error
	progressCallback := func(step, total int, path string, succeeded bool) {
		progress.MarkEditProgress(step, total, path, succeeded)
		if _, err := savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved"); err != nil && progressSaveErr == nil {
			progressSaveErr = err
		}
	}
	report, runErr := runExecutorPhaseScopedWithVerificationPlan(cfg, plan, executorRequiredEditPaths, allowedEditPaths, verificationPlan, opts.JSONEvents, stdout, progressCallback)
	if runErr == nil && progressSaveErr != nil {
		runErr = progressSaveErr
	}
	sourceSanityRepairRequired := planexecute.ExecutorSourceSanityFailed(toSharedExecutorPhaseReport(report), runErr)
	verificationRepairRequired := planexecute.ExecutorVerificationFailed(toSharedExecutorPhaseReport(report), runErr)
	progress.MarkExecutorComplete(toSharedExecutorPhaseReport(report), runErr)
	_, err = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved", (sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair)
	if err != nil && runErr == nil {
		runErr = err
	}
	if sourceSanityRepairRequired {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "executor_verification_repair_required", map[string]any{
			"failure_kind": planexecute.RunFailureSourceSanityFailed,
			"reason":       truncatePlanExecuteProgressReason(runErr.Error()),
		})
	} else if verificationRepairRequired {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "executor_verification_repair_required", map[string]any{
			"failure_kind":          planexecute.RunFailureVerificationRequired,
			"reason":                truncatePlanExecuteProgressReason(runErr.Error()),
			"verification_commands": verificationCommands,
		})
	}
	_ = sessionStart // findLatestPlanArtifact already filtered; keep referenced.
	reviewReport := reviewRepairPhaseReport{Skipped: opts.SkipReviewRepair}
	var reviewErr error
	if (runErr == nil || sourceSanityRepairRequired || verificationRepairRequired) && !opts.SkipReviewRepair {
		reviewSessionID := newPlanExecuteSessionID()
		progress.MarkReviewStarted(reviewSessionID)
		_, err = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil {
			reviewErr = err
		} else {
			reviewOpts := opts
			reviewOpts.ReviewSessionID = reviewSessionID
			reviewReport, reviewErr = runReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, report, reviewOpts, stdout, promptRun, rec)
		}
		progress.MarkReviewComplete(toSharedReviewRepairPhaseReport(reviewReport), reviewErr)
		if (sourceSanityRepairRequired || verificationRepairRequired) && reviewErr == nil {
			if planexecute.ReviewRepairResolvedVerificationFailure(toSharedReviewRepairPhaseReport(reviewReport)) {
				if verificationRepairRequired {
					_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(cfg.Workspace.Root, verificationPlan, opts.JSONEvents, stdout)
					report.Verification = verificationEvidence
					progress.Executor.Verification = toSharedPhaseEvidenceChecks(verificationEvidence)
					if verificationErr != nil {
						runErr = verificationErr
						progress.Status = planexecute.ProgressStatusFailed
						progress.FailureKind = planexecute.RunFailureVerificationRequired
						progress.FailureReason = truncatePlanExecuteProgressReason(verificationErr.Error())
					} else {
						runErr = nil
						verificationRepairRequired = false
					}
				} else {
					runErr = nil
				}
				sourceSanityRepairRequired = false
			} else {
				progress.Status = planexecute.ProgressStatusFailed
				if verificationRepairRequired {
					progress.FailureKind = planexecute.RunFailureVerificationRequired
				} else {
					progress.FailureKind = planexecute.RunFailureSourceSanityFailed
				}
				progress.FailureReason = truncatePlanExecuteProgressReason(runErr.Error())
			}
		}
		_, err = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil && reviewErr == nil {
			reviewErr = err
		}
	} else if runErr == nil && opts.SkipReviewRepair {
		progress.MarkSkippedReview()
		_, err = savePlanExecuteProgressState(cfg.Workspace.Root, &progress, opts.JSONEvents, stdout, "resume_state_saved")
		if err != nil {
			runErr = err
		}
	}
	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, report)
		fmt.Fprintln(stdout, reviewReport)
	}
	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
		return 1
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return 1
	}
	return 0
}

func runPlanExecuteResumeReviewRepair(
	cfg config.Config,
	opts planExecuteOptions,
	prompt string,
	requiredEditPaths []string,
	state planexecute.ProgressState,
	statePath string,
	stdout io.Writer,
	stderr io.Writer,
	promptRun cmdruntime.PromptRunner,
	rec *telemetry.Recorder,
) int {
	sessionID := strings.TrimSpace(state.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(opts.Resume)
	}
	if !state.PromptMatches(prompt) {
		fmt.Fprintln(stderr, fmt.Errorf("plan-execute resume state %s was created for a different prompt; pass the same --prompt or --prompt-file used by session %s", statePath, sessionID))
		return 1
	}
	if !state.ReviewResumeReady() {
		fmt.Fprintln(stderr, fmt.Errorf("plan-execute resume state %s is not review-resumable (phase=%s status=%s executor_status=%s)", statePath, state.Phase, state.Status, state.Executor.Status))
		return 1
	}
	planPath := state.ResolvedPlanPath(cfg.Workspace.Root)
	plan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		fmt.Fprintln(stderr, fmt.Errorf("read resumed plan artifact %q: %w", planPath, err))
		return 1
	}
	if len(state.RequiredEditPaths) > 0 {
		requiredEditPaths = append([]string(nil), state.RequiredEditPaths...)
	}
	allowedEditPaths := mergePlanExecutePaths(state.AllowedEditPaths, planExecuteAllowedEditPaths(opts, prompt))
	if len(state.EditSetObligations) == 0 {
		state.EditSetObligations = planExecuteScopeContract(opts, prompt).EditSetObligations
	}
	state.EditSetObligations = state.EditSetObligations.Normalized()
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "resume_state_loaded", planexecute.ProgressStateEventFields(state, statePath))
	reviewSessionID := newPlanExecuteSessionID()
	state.MarkReviewStarted(reviewSessionID)
	if _, err := savePlanExecuteProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reviewOpts := opts
	reviewOpts.ReviewSessionID = reviewSessionID
	reviewOpts.RequiredEditPaths = planexecute.MultiFlag(requiredEditPaths)
	reviewOpts.AllowedEditPaths = planexecute.MultiFlag(allowedEditPaths)
	reviewOpts.EditSetObligations = state.EditSetObligations
	reviewOpts.ScopeContract = planexecute.NewPlanScopeContract(requiredEditPaths, allowedEditPaths, state.EditSetObligations)
	reviewBaseline := fromSharedReviewRepairBaseline(state.ReviewBaseline)
	executorReport := fromSharedExecutorPhaseReport(state.ExecutorPhaseReport())
	reviewReport, reviewErr := runReviewRepairPhase(cfg, prompt, plan, requiredEditPaths, &reviewBaseline, executorReport, reviewOpts, stdout, promptRun, rec)
	state.MarkReviewComplete(toSharedReviewRepairPhaseReport(reviewReport), reviewErr)
	if reviewErr == nil && planexecute.ExecutorVerificationFailed(state.ExecutorPhaseReport(), fmt.Errorf("verification failed")) && planexecute.ReviewRepairResolvedVerificationFailure(toSharedReviewRepairPhaseReport(reviewReport)) {
		verificationPlan := planexecute.PlanVerification(planexecute.VerificationPlannerInput{
			WorkspaceRoot: cfg.Workspace.Root,
			Prompt:        prompt,
			ChangedPaths:  planChangedPaths(plan),
		})
		_, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(cfg.Workspace.Root, verificationPlan, opts.JSONEvents, stdout)
		state.Executor.Verification = toSharedPhaseEvidenceChecks(verificationEvidence)
		if verificationErr != nil {
			reviewErr = verificationErr
			state.Status = planexecute.ProgressStatusFailed
			state.FailureKind = planexecute.RunFailureVerificationRequired
			state.FailureReason = truncatePlanExecuteProgressReason(verificationErr.Error())
		}
	}
	_, err = savePlanExecuteProgressState(cfg.Workspace.Root, &state, opts.JSONEvents, stdout, "resume_state_saved")
	if err != nil && reviewErr == nil {
		reviewErr = err
	}
	if !opts.JSONEvents {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "--- plan-then-execute summary ---")
		fmt.Fprintln(stdout, executorReport)
		fmt.Fprintln(stdout, reviewReport)
	}
	if reviewErr != nil {
		fmt.Fprintln(stderr, reviewErr)
		return 1
	}
	return 0
}

func savePlanExecuteProgressState(root string, state *planexecute.ProgressState, jsonEvents bool, stdout io.Writer, eventType string, suppressRecovery ...bool) (string, error) {
	statePath, err := planexecute.SaveProgressState(root, state)
	if err != nil {
		return statePath, err
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, planexecute.ProgressStateEventFields(*state, statePath))
	if state.Status == planexecute.ProgressStatusFailed && !(len(suppressRecovery) > 0 && suppressRecovery[0]) {
		now := time.Now().UTC()
		record, recordPath, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgress(*state, statePath), now)
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

func toSharedReviewRepairBaseline(baseline reviewRepairBaseline) planexecute.ReviewRepairBaseline {
	out := planexecute.ReviewRepairBaseline{Files: map[string]planexecute.ReviewRepairFileSnapshot{}}
	for path, snapshot := range baseline.Files {
		out.Files[path] = planexecute.ReviewRepairFileSnapshot{Before: snapshot.Before, Exists: snapshot.Exists}
	}
	return out
}

func fromSharedReviewRepairBaseline(baseline planexecute.ReviewRepairBaseline) reviewRepairBaseline {
	out := reviewRepairBaseline{Files: map[string]reviewRepairFileSnapshot{}}
	for path, snapshot := range baseline.Files {
		out.Files[path] = reviewRepairFileSnapshot{Before: snapshot.Before, Exists: snapshot.Exists}
	}
	return out
}

func toSharedExecutorPhaseReport(report executorPhaseReport) planexecute.ExecutorPhaseReport {
	return planexecute.ExecutorPhaseReport{
		Total:        report.Total,
		Succeeded:    report.Succeeded,
		Failed:       report.Failed,
		Failures:     append([]string(nil), report.Failures...),
		SourceSanity: toSharedPhaseEvidenceChecks(report.SourceSanity),
		Verification: toSharedPhaseEvidenceChecks(report.Verification),
		Handoff:      toSharedExecutorHandoffPacket(report.Handoff),
	}
}

func fromSharedExecutorPhaseReport(report planexecute.ExecutorPhaseReport) executorPhaseReport {
	return executorPhaseReport{
		Total:        report.Total,
		Succeeded:    report.Succeeded,
		Failed:       report.Failed,
		Failures:     append([]string(nil), report.Failures...),
		SourceSanity: fromSharedPhaseEvidenceChecks(report.SourceSanity),
		Verification: fromSharedPhaseEvidenceChecks(report.Verification),
		Handoff:      fromSharedExecutorHandoffPacket(report.Handoff),
	}
}

func toSharedPhaseEvidenceChecks(checks []phaseEvidenceCheck) []planexecute.PhaseEvidenceCheck {
	out := make([]planexecute.PhaseEvidenceCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, planexecute.PhaseEvidenceCheck{
			Kind:       check.Kind,
			Path:       check.Path,
			Command:    check.Command,
			WorkingDir: check.WorkingDir,
			Status:     check.Status,
			Summary:    check.Summary,
			Error:      check.Error,
		})
	}
	return out
}

func fromSharedPhaseEvidenceChecks(checks []planexecute.PhaseEvidenceCheck) []phaseEvidenceCheck {
	out := make([]phaseEvidenceCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, phaseEvidenceCheck{
			Kind:       check.Kind,
			Path:       check.Path,
			Command:    check.Command,
			WorkingDir: check.WorkingDir,
			Status:     check.Status,
			Summary:    check.Summary,
			Error:      check.Error,
		})
	}
	return out
}

func toSharedReviewRepairPhaseReport(report reviewRepairPhaseReport) planexecute.ReviewRepairPhaseReport {
	return planexecute.ReviewRepairPhaseReport{
		Skipped:        report.Skipped,
		Status:         report.Status,
		Repairs:        report.Repairs,
		AutoRepairs:    report.AutoRepairs,
		ReviewPlan:     report.ReviewPlan,
		ChecklistFound: report.ChecklistFound,
		ReasonClass:    report.ReasonClass,
	}
}

func toSharedExecutorHandoffPacket(handoff executorHandoffPacket) planexecute.ExecutorHandoffPacket {
	return planexecute.ExecutorHandoffPacket{
		State:                   handoff.State,
		Reason:                  handoff.Reason,
		PlanID:                  handoff.PlanID,
		HandoffID:               handoff.HandoffID,
		HandoffStatus:           handoff.HandoffStatus,
		ApprovalScopeID:         handoff.ApprovalScopeID,
		ApprovalStatus:          handoff.ApprovalStatus,
		ApprovalPermission:      handoff.ApprovalPermission,
		PackID:                  handoff.PackID,
		DeclaredPackID:          handoff.DeclaredPackID,
		ArtifactPacketStatus:    handoff.ArtifactPacketStatus,
		ArtifactPacketFreshness: handoff.ArtifactPacketFreshness,
		IndexID:                 handoff.IndexID,
		Fresh:                   handoff.Fresh,
		CandidateFiles:          handoff.CandidateFiles,
		CandidateSymbols:        handoff.CandidateSymbols,
		Tests:                   handoff.Tests,
		ApprovedPathCount:       handoff.ApprovedPathCount,
		PendingPathCount:        handoff.PendingPathCount,
	}
}

func fromSharedExecutorHandoffPacket(handoff planexecute.ExecutorHandoffPacket) executorHandoffPacket {
	return executorHandoffPacket{
		State:                   handoff.State,
		Reason:                  handoff.Reason,
		PlanID:                  handoff.PlanID,
		HandoffID:               handoff.HandoffID,
		HandoffStatus:           handoff.HandoffStatus,
		ApprovalScopeID:         handoff.ApprovalScopeID,
		ApprovalStatus:          handoff.ApprovalStatus,
		ApprovalPermission:      handoff.ApprovalPermission,
		PackID:                  handoff.PackID,
		DeclaredPackID:          handoff.DeclaredPackID,
		ArtifactPacketStatus:    handoff.ArtifactPacketStatus,
		ArtifactPacketFreshness: handoff.ArtifactPacketFreshness,
		IndexID:                 handoff.IndexID,
		Fresh:                   handoff.Fresh,
		CandidateFiles:          handoff.CandidateFiles,
		CandidateSymbols:        handoff.CandidateSymbols,
		Tests:                   handoff.Tests,
		ApprovedPathCount:       handoff.ApprovedPathCount,
		PendingPathCount:        handoff.PendingPathCount,
	}
}

func truncatePlanExecuteProgressReason(reason string) string {
	reason = strings.TrimSpace(recovery.RedactRecoveryText(reason))
	if len(reason) <= 2000 {
		return reason
	}
	for i := range reason {
		if i >= 2000 {
			return strings.TrimSpace(reason[:i]) + fmt.Sprintf(" [truncated %d bytes]", len(reason)-i)
		}
	}
	return reason
}

func parsePlanExecuteOptions(args []string, stderr io.Writer) (planExecuteOptions, error) {
	var opts planExecuteOptions
	fs := flag.NewFlagSet("idiom plan-execute", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "implementation prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read implementation prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id before running")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for this planner run")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override planner max_tokens for this run; 0 uses the shape-aware plan-execute default")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", false, "compatibility flag: plan-execute always treats verification as planner-owned; this is a no-op")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag: plan-execute does not run builds or tests; this is a no-op")
	fs.BoolVar(&opts.SkipReviewRepair, "skip-review-repair", false, "skip the post-execute review-repair planner pass")
	fs.Var(&opts.RequiredEditPaths, "required-edit-path", "workspace-relative required edit path for this plan-execute run; repeatable and augments prompt extraction")
	fs.Var(&opts.AllowedEditPaths, "allowed-edit-path", "workspace-relative candidate edit path for this plan-execute run; repeatable and augments prompt extraction without requiring coverage")
	if err := fs.Parse(args); err != nil {
		return planExecuteOptions{}, err
	}
	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return planExecuteOptions{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}
		opts.Prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return planExecuteOptions{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	return opts, nil
}

func planExecutePrompt(opts planExecuteOptions) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""
	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if hasPrompt {
		return opts.Prompt, nil
	}
	data, err := os.ReadFile(opts.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %q is empty", opts.PromptFile)
	}
	return prompt, nil
}

func planExecuteRequiredEditPaths(opts planExecuteOptions, prompt string) []string {
	return planExecuteScopeContract(opts, prompt).RequiredCoveragePaths
}

func planExecuteAllowedEditPaths(opts planExecuteOptions, prompt string) []string {
	return planExecuteScopeContract(opts, prompt).AllowedCandidatePaths
}

func planExecuteScopeContract(opts planExecuteOptions, prompt string) planexecute.PlanScopeContract {
	scope := opts.ScopeContract
	required := append([]string(nil), scope.RequiredCoveragePaths...)
	required = append(required, []string(opts.RequiredEditPaths)...)
	required = append(required, planexecute.RequiredEditPathsFromPrompt(prompt)...)
	allowed := append([]string(nil), scope.AllowedCandidatePaths...)
	allowed = append(allowed, []string(opts.AllowedEditPaths)...)
	allowed = append(allowed, planexecute.AllowedEditPathsFromPrompt(prompt)...)
	obligations := planexecute.MergeEditSetObligations(
		scope.EditSetObligations.WithDefaultSource("scope_contract"),
		opts.EditSetObligations.WithDefaultSource("options"),
	)
	return planexecute.NewPlanScopeContract(required, allowed, obligations)
}

func inheritPlanExecutePromptPackID(plan *workspace.PlanArtifact, prompt string) string {
	if plan == nil || strings.TrimSpace(plan.PackID) != "" {
		return ""
	}
	packID := extractPlanExecutePromptPackID(prompt)
	if packID == "" {
		return ""
	}
	plan.PackID = packID
	if strings.TrimSpace(plan.Packet.PackID) == "" {
		plan.Packet.PackID = packID
	}
	if status := strings.TrimSpace(plan.Packet.Status); status == "" || status == "not_provided" {
		plan.Packet.Status = "provided"
	}
	return packID
}

func extractPlanExecutePromptPackID(prompt string) string {
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "pack") {
			continue
		}
		for _, token := range strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9'))
		}) {
			token = strings.ToLower(strings.TrimSpace(token))
			if isPlanExecutePackID(token) {
				return token
			}
		}
	}
	return ""
}

func isPlanExecutePackID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func patchPlanArtifactPackID(path string, packID string) error {
	packID = strings.TrimSpace(packID)
	if path == "" || packID == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	encodedPackID, err := json.Marshal(packID)
	if err != nil {
		return err
	}
	raw["pack_id"] = encodedPackID
	var packet map[string]json.RawMessage
	if body, ok := raw["packet"]; ok {
		_ = json.Unmarshal(body, &packet)
	}
	if packet == nil {
		packet = map[string]json.RawMessage{}
	}
	if current, ok := packet["pack_id"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" {
		packet["pack_id"] = encodedPackID
	}
	if current, ok := packet["status"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" || strings.Trim(strings.TrimSpace(string(current)), `"`) == "not_provided" {
		encodedStatus, err := json.Marshal("provided")
		if err != nil {
			return err
		}
		packet["status"] = encodedStatus
	}
	encodedPacket, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	raw["packet"] = encodedPacket
	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return os.WriteFile(path, updated, 0o644)
}

// planExecutePlannerSystemAddendum is prepended to the brief in the
// planner phase. Kept tight by design: local text-tool backends need
// enough visible output budget to emit a parser-readable submit_plan.
// Every rule earned its place from a real failure mode; if you add
// another, compress an existing rule to make room.
func planExecutePlannerSystemAddendum(requiredEditPaths []string, allowedEditPaths []string) string {
	return planexecute.PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, nil)
}

func planExecutePlannerSystemAddendumWithObligations(requiredEditPaths []string, allowedEditPaths []string, obligations planexecute.EditSetObligations) string {
	return planexecute.PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, obligations)
}

func planExecutePlannerMaxTokens(root string, prompt string, requiredEditPaths []string, allowedEditPaths []string) int {
	return 16384
}

func planExecutePathLooksLikeTest(path string) bool {
	return planexecute.PlanExecutePathLooksLikeTest(path)
}

func planExecuteScopeToBriefBody(prompt string, requiredEditPaths []string, allowedEditPaths []string) string {
	return planexecute.AppendPlanExecuteScopeToBriefBody(prompt, requiredEditPaths, allowedEditPaths)
}

type planExecuteAutoExactPlanResult struct {
	Submitted       bool
	SourceSatisfied bool
	Result          workspace.ToolResult
	Reason          string
	EditCount       int
	AppliedCount    int
	PendingCount    int
	UnknownCount    int
	AppliedPaths    []string
	PendingPaths    []string
	UnknownPaths    []string
}

func planExecuteTryAutoExactPlan(cfg config.Config, prompt string, requiredEditPaths []string) planExecuteAutoExactPlanResult {
	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, cfg.Workspace.Root)
	if len(edits) == 0 {
		return planExecuteAutoExactPlanResult{Reason: reason}
	}
	classified := planExecuteClassifyAutoExactPlanEdits(cfg.Workspace.Root, edits)
	if classified.UnknownCount > 0 {
		classified.Reason = "exact-anchor progress unknown for required path(s): " + strings.Join(classified.UnknownPaths, ", ")
		classified.EditCount = len(edits)
		return classified
	}
	if classified.PendingCount == 0 {
		classified.SourceSatisfied = true
		classified.Reason = "all exact-anchor edits already match the current workspace"
		classified.EditCount = len(edits)
		return classified
	}
	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Deterministic exact-anchor plan synthesized from the Required Edits packet.",
		Edits:     planExecutePendingAutoExactEdits(cfg.Workspace.Root, edits),
	})
	classified.Result = result
	if strings.TrimSpace(result.Error) != "" {
		classified.Reason = "exact-anchor auto-plan rejected by submit_plan validation"
		classified.EditCount = classified.PendingCount
		return classified
	}
	classified.Submitted = true
	classified.EditCount = classified.PendingCount
	if classified.AppliedCount > 0 {
		classified.Reason = "already-applied exact edits skipped; pending exact edits submitted"
	} else {
		classified.Reason = "all required paths covered by parseable exact old_text/new_text pairs"
	}
	return classified
}

func planExecuteClassifyAutoExactPlanEdits(root string, edits []workspace.PlanEdit) planExecuteAutoExactPlanResult {
	var result planExecuteAutoExactPlanResult
	var appliedPaths []string
	var pendingPaths []string
	var unknownPaths []string
	for _, edit := range edits {
		status := planExecuteAutoExactEditStatus(root, edit)
		path := normalizePlanExecutePath(edit.Path)
		switch status {
		case "applied":
			result.AppliedCount++
			appliedPaths = append(appliedPaths, path)
		case "pending":
			result.PendingCount++
			pendingPaths = append(pendingPaths, path)
		default:
			result.UnknownCount++
			unknownPaths = append(unknownPaths, path)
		}
	}
	result.AppliedPaths = mergePlanExecutePaths(appliedPaths)
	result.PendingPaths = mergePlanExecutePaths(pendingPaths)
	result.UnknownPaths = mergePlanExecutePaths(unknownPaths)
	return result
}

func planExecutePendingAutoExactEdits(root string, edits []workspace.PlanEdit) []workspace.PlanEdit {
	var pending []workspace.PlanEdit
	for _, edit := range edits {
		if planExecuteAutoExactEditStatus(root, edit) == "pending" {
			pending = append(pending, edit)
		}
	}
	return pending
}

func planExecuteAutoExactEditStatus(root string, edit workspace.PlanEdit) string {
	path := normalizePlanExecutePath(edit.Path)
	if path == "" {
		return "unknown"
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(strings.TrimSpace(root), filepath.FromSlash(path))
	}
	body, err := os.ReadFile(filepath.Clean(fullPath))
	if err != nil {
		if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
			return "pending"
		}
		return "unknown"
	}
	status, _ := submittedPlanEditStatus(string(body), edit)
	return status
}

func planExecuteAutoSourceSatisfiedAnswer(requiredEditPaths []string) string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	var b strings.Builder
	b.WriteString("Decision: source-satisfied\n")
	b.WriteString("The requested final state is already present in the current workspace for every required edit path.")
	if len(paths) > 0 {
		b.WriteString("\n\nRequired edit paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func planExecuteSourceSatisfiedEvidencePaths(answer string, requiredEditPaths []string, allowedEditPaths []string) []string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(paths) > 0 {
		return paths
	}
	evidencePaths := planExecuteUniqueRequiredPaths(plancontract.SourceSatisfiedEvidencePaths(answer))
	allowed := planExecutePathSet(allowedEditPaths)
	if len(allowed) == 0 {
		return evidencePaths
	}
	var scoped []string
	for _, path := range evidencePaths {
		if _, ok := allowed[path]; ok {
			scoped = append(scoped, path)
		}
	}
	return scoped
}

func planExecuteMissingSourceSatisfiedCoveragePaths(answer string, requiredEditPaths []string) []string {
	return plancontract.SourceSatisfiedMissingCoveragePaths(answer, requiredEditPaths)
}

func planExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, "")
}

func planExecuteExactPlanEditsForWorkspace(prompt string, requiredEditPaths []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	required := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(required) == 0 {
		return nil, "no required_edit_paths"
	}
	section := ""
	for _, candidatePrompt := range planExecuteAutoPlanScanTexts(prompt) {
		for _, heading := range []string{"required edits", "edits to apply", "edits"} {
			if candidate := atomcmd.MarkdownSectionByHeading(candidatePrompt, heading); strings.TrimSpace(candidate) != "" {
				section = candidate
				break
			}
		}
		if strings.TrimSpace(section) != "" {
			break
		}
	}
	if strings.TrimSpace(section) == "" {
		return planExecuteImplicitNewFileBodyEdits(prompt, required, workspaceRoot)
	}

	edits, reason := planExecuteExactPlanEditsFromSection(section, required, workspaceRoot)
	if len(edits) > 0 {
		return edits, ""
	}
	if implicit, implicitReason := planExecuteImplicitNewFileBodyEdits(prompt, required, workspaceRoot); len(implicit) > 0 {
		return implicit, ""
	} else if strings.TrimSpace(implicitReason) != "" && reason == "no parseable exact old_text/new_text pairs" {
		reason = reason + "; " + implicitReason
	}
	return nil, reason
}

func planExecuteExactPlanEditsFromSection(section string, required []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	type pendingOldBlock struct {
		paths []string
		text  string
	}
	var edits []workspace.PlanEdit
	var pending pendingOldBlock
	var current []string
	var currentPaths []string
	var blockPaths []string
	blockKind := ""
	previousNonEmpty := ""
	fence := ""
	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, _, ok := atomcmd.MarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil
				blockPaths = append([]string(nil), currentPaths...)
				blockKind = planExecuteExactEditBlockKind(previousNonEmpty)
				continue
			}
			if trimmed != "" {
				if paths := atomcmd.RequiredEditPathsFromLabel(trimmed, required); len(paths) > 0 {
					currentPaths = paths
				}
				previousNonEmpty = trimmed
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			block := strings.TrimSpace(strings.Join(current, "\n"))
			switch blockKind {
			case "old":
				pending = pendingOldBlock{paths: append([]string(nil), blockPaths...), text: block}
			case "new":
				paths := pending.paths
				if len(paths) == 0 {
					paths = blockPaths
				}
				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}
				if len(paths) == 0 {
					paths = planExecuteInferPathsForOldText(workspaceRoot, required, pending.text)
				}
				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(pending.text) == "" {
						continue
					}
					edits = append(edits, workspace.PlanEdit{
						Path:    path,
						OldText: pending.text,
						NewText: block,
						Purpose: "Apply the exact old_text/new_text edit from the Required Edits packet.",
					})
				}
				pending = pendingOldBlock{}
			case "replace_all":
				paths := blockPaths
				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}
				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(block) == "" {
						continue
					}
					edits = append(edits, workspace.PlanEdit{
						Path:       path,
						OldText:    "",
						NewText:    block,
						ReplaceAll: true,
						Purpose:    "Create or replace the full file body from the Required Edits packet.",
					})
				}
			}
			fence = ""
			current = nil
			blockPaths = nil
			blockKind = ""
			previousNonEmpty = ""
			continue
		}
		current = append(current, rawLine)
	}
	if len(edits) == 0 {
		return nil, "no parseable exact old_text/new_text pairs"
	}
	missing := planExecuteMissingEditPaths(required, edits)
	if len(missing) > 0 {
		return nil, "exact old_text/new_text pairs do not cover required path(s): " + strings.Join(missing, ", ")
	}
	return edits, ""
}

func planExecuteImplicitNewFileBodyEdits(prompt string, required []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	for _, candidatePrompt := range planExecuteAutoPlanScanTexts(prompt) {
		section := atomcmd.MarkdownSectionByHeading(candidatePrompt, "required behavior")
		if strings.TrimSpace(section) == "" {
			continue
		}
		edits := planExecuteImplicitNewFileBodyEditsFromSection(section, required, workspaceRoot)
		if len(edits) == 0 {
			continue
		}
		if missing := planExecuteMissingEditPaths(required, edits); len(missing) > 0 {
			return nil, "implicit new-file bodies do not cover required path(s): " + strings.Join(missing, ", ")
		}
		return edits, ""
	}
	return nil, "no exact-edit section"
}

func planExecuteImplicitNewFileBodyEditsFromSection(section string, required []string, workspaceRoot string) []workspace.PlanEdit {
	var edits []workspace.PlanEdit
	var recent []string
	var current []string
	var blockPaths []string
	capture := false
	fence := ""
	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, _, ok := atomcmd.MarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil
				contextText := strings.Join(recent, "\n")
				blockPaths = planExecuteImplicitBodyBoundPaths(recent, required)
				capture = len(blockPaths) > 0 &&
					planExecuteImplicitNewFileBodyContext(contextText) &&
					planExecuteAllPathsMissing(workspaceRoot, blockPaths)
				continue
			}
			if trimmed != "" {
				recent = append(recent, trimmed)
				if len(recent) > 8 {
					recent = recent[len(recent)-8:]
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			block := strings.TrimSpace(strings.Join(current, "\n"))
			if capture {
				for _, path := range blockPaths {
					if planExecuteImplicitBodyLooksSafeForPath(path, block) {
						edits = append(edits, workspace.PlanEdit{
							Path:       path,
							OldText:    "",
							NewText:    block,
							ReplaceAll: true,
							Purpose:    "Create the full file body from the refined design-doc atom.",
						})
					}
				}
			}
			fence = ""
			current = nil
			blockPaths = nil
			capture = false
			continue
		}
		current = append(current, rawLine)
	}
	return edits
}

func planExecuteImplicitBodyBoundPaths(recent []string, required []string) []string {
	for i := len(recent) - 1; i >= 0; i-- {
		line := strings.TrimSpace(recent[i])
		if len(pathparse.MentionedWorkspacePaths(line)) == 0 {
			continue
		}
		return atomcmd.RequiredEditPathsFromLabel(line, required)
	}
	return atomcmd.RequiredEditPathsFromLabel(strings.Join(recent, "\n"), required)
}

func planExecuteAutoPlanScanTexts(prompt string) []string {
	texts := []string{prompt}
	unquoted := planExecuteUnquoteMarkdown(prompt)
	if unquoted != prompt {
		texts = append(texts, unquoted)
	}
	return texts
}

func planExecuteUnquoteMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, ">") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, ">")
		trimmed = strings.TrimPrefix(trimmed, " ")
		lines[i] = trimmed
		changed = true
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

func planExecuteImplicitNewFileBodyContext(contextText string) bool {
	lower := strings.ToLower(contextText)
	return strings.Contains(lower, "create ") ||
		strings.Contains(lower, "new file") ||
		strings.Contains(lower, "full file body") ||
		strings.Contains(lower, "following content")
}

func planExecuteAllPathsMissing(workspaceRoot string, paths []string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" || len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		path = normalizePlanExecutePath(path)
		if path == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(path))); err == nil {
			return false
		} else if !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func planExecuteImplicitBodyLooksSafeForPath(path string, body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return strings.HasPrefix(body, "package ")
	default:
		return true
	}
}

func planExecuteInferPathsForOldText(workspaceRoot string, required []string, oldText string) []string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	oldText = strings.TrimSpace(oldText)
	if workspaceRoot == "" || oldText == "" {
		return nil
	}
	var matches []string
	for _, requiredPath := range required {
		path := normalizePlanExecutePath(requiredPath)
		if path == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		if strings.Count(string(body), oldText) == 1 {
			matches = append(matches, path)
		}
	}
	if len(matches) != 1 {
		return nil
	}
	return matches
}

func planExecuteExactEditBlockKind(label string) string {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch {
	case strings.HasPrefix(normalized, "old_text"), strings.HasPrefix(normalized, "old"):
		return "old"
	case strings.HasPrefix(normalized, "new_text"), strings.HasPrefix(normalized, "new"):
		return "new"
	case strings.HasPrefix(normalized, "replace_all"):
		return "replace_all"
	default:
		return ""
	}
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return planexecute.PlanExecuteUniqueRequiredPaths(requiredEditPaths)
}

func UniqueRequiredPaths(requiredEditPaths []string) []string {
	return planExecuteUniqueRequiredPaths(requiredEditPaths)
}

func planExecuteMissingEditPaths(required []string, edits []workspace.PlanEdit) []string {
	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}
	var missing []string
	for _, path := range required {
		if _, ok := covered[path]; !ok {
			missing = append(missing, path)
		}
	}
	return missing
}

// findLatestPlanArtifact returns the most recently written plan
// artifact in <root>/.idiom/plans/ whose mtime is at or after
// notBefore. The notBefore guard scopes the lookup to the current
// session's planner output — without it, a planner failure silently
// falls back to a stale artifact from an unrelated prior session.
// planExecuteAcquireQueueSlot delegates to the shared plan-execute queue
// admission helper so the CLI and TUI in-process paths keep the same
// best-effort gate behavior.
func planExecuteAcquireQueueSlot(cfg config.Config, opts planExecuteOptions, sessionID string, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) func() {
	return planexecute.AcquireQueueSlot(cfg, planexecute.QueueAcquireOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		SessionID:  sessionID,
	}, rec, stdout, stderr)
}

// newPlanExecuteSessionID returns a fresh, unguessable session
// identifier for this plan-execute run. Used by the executor phase
// to filter `.idiom/plans/<stamp>.plan.json` artifacts to only those
// produced by THIS session — see findLatestPlanArtifact for the
// load-side guard. 16 hex bytes from crypto/rand: 64 bits of entropy
// is overkill for collision avoidance but trivial to generate. Falls
// back to a timestamp-derived hex on the (essentially impossible)
// rand.Read failure path so the rest of the lane still runs.
func newPlanExecuteSessionID() string {
	return planexecute.NewPlanExecuteSessionID()
}

func NewSessionID() string {
	return newPlanExecuteSessionID()
}

// findLatestPlanArtifact picks the most recent .idiom/plans/<stamp>.plan.json
// produced since notBefore. When sessionID is non-empty, the search is
// narrowed to plans whose persisted SessionID matches — otherwise two
// concurrent plan-execute runs in the same workspace race on the shared
// plans directory and the executor phase silently loads the wrong plan.
// Empty sessionID falls back to the legacy time-only filter for
// backward compatibility with callers that don't yet stamp a session.
func findLatestPlanArtifact(root string, notBefore time.Time, sessionID string) (string, error) {
	return planexecute.FindLatestPlanArtifact(root, notBefore, sessionID)
}

// executorPhaseReport summarizes what the executor phase did so the
// caller can read a one-shot status line.
type executorPhaseReport struct {
	Total        int
	Succeeded    int
	Failed       int
	Failures     []string
	SourceSanity []phaseEvidenceCheck
	Verification []phaseEvidenceCheck
	Handoff      executorHandoffPacket
}

func (r executorPhaseReport) String() string {
	header := fmt.Sprintf("executor: %d edits, %d succeeded, %d failed", r.Total, r.Succeeded, r.Failed)
	if len(r.Failures) == 0 {
		return header
	}
	return header + "\n  " + strings.Join(r.Failures, "\n  ")
}

const (
	executorHandoffStateInherited  = "inherited"
	executorHandoffStateMissing    = "missing"
	executorHandoffStateStale      = "stale"
	executorHandoffStateOverridden = "overridden"
)

type executorHandoffPacket struct {
	State                   string
	Reason                  string
	PlanID                  string
	HandoffID               string
	HandoffStatus           string
	ApprovalScopeID         string
	ApprovalStatus          string
	ApprovalPermission      string
	PackID                  string
	DeclaredPackID          string
	ArtifactPacketStatus    string
	ArtifactPacketFreshness string
	IndexID                 string
	Fresh                   bool
	CandidateFiles          int
	CandidateSymbols        int
	Tests                   int
	ApprovedPathCount       int
	PendingPathCount        int
}

func classifyExecutorHandoffPacket(root string, plan workspace.PlanArtifact) executorHandoffPacket {
	handoff := executorHandoffPacket{
		State:                   executorHandoffStateMissing,
		Reason:                  "missing_packet_id",
		PlanID:                  sanitizePlanExecuteEventID(plan.PlanID),
		HandoffID:               sanitizePlanExecuteEventID(plan.Handoff.ID),
		HandoffStatus:           sanitizePlanExecuteEventToken(plan.Handoff.Status),
		ApprovalScopeID:         sanitizePlanExecuteEventID(plan.Approval.ScopeID),
		ApprovalStatus:          sanitizePlanExecuteEventToken(plan.Approval.Status),
		ApprovalPermission:      sanitizePlanExecuteEventToken(plan.Approval.ExecutionPermission),
		ArtifactPacketStatus:    sanitizePlanExecuteEventToken(plan.Packet.Status),
		ArtifactPacketFreshness: sanitizePlanExecuteEventToken(plan.Packet.Freshness),
		IndexID:                 sanitizePlanExecuteEventToken(plan.Packet.IndexID),
		ApprovedPathCount:       len(plan.Approval.ApprovedPaths),
		PendingPathCount:        len(plan.Approval.PendingPaths),
	}
	topLevelPackID, topLevelValid := normalizePlanExecuteEventPackID(plan.PackID)
	declaredPackID, declaredValid := normalizePlanExecuteEventPackID(plan.Packet.PackID)
	rawTopLevelPackID := strings.TrimSpace(plan.PackID)
	rawDeclaredPackID := strings.TrimSpace(plan.Packet.PackID)
	if rawTopLevelPackID != "" && !topLevelValid {
		handoff.Reason = "invalid_pack_id"
		return handoff
	}
	if rawDeclaredPackID != "" && !declaredValid {
		handoff.Reason = "invalid_declared_packet_id"
		if topLevelPackID == "" {
			return handoff
		}
	}
	effectivePackID := firstNonEmptyString(topLevelPackID, declaredPackID)
	handoff.PackID = effectivePackID
	handoff.DeclaredPackID = declaredPackID
	if effectivePackID == "" {
		return handoff
	}
	overridden := topLevelPackID != "" && declaredPackID != "" && topLevelPackID != declaredPackID
	if overridden || (topLevelPackID != "" && rawDeclaredPackID != "" && !declaredValid) {
		handoff.State = executorHandoffStateOverridden
		handoff.Reason = "packet_id_override"
	}
	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, effectivePackID)
	if err != nil {
		if handoff.State != executorHandoffStateOverridden {
			handoff.State = executorHandoffStateMissing
			handoff.Reason = "cache_load_error"
		}
		return handoff
	}
	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = executorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else if view.Fresh {
			handoff.State = executorHandoffStateInherited
			handoff.Reason = "fresh_graph_view"
		} else {
			handoff.State = executorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}
		return handoff
	case comprehension.ViewLoadStateStale:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = executorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else {
			handoff.State = executorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}
		return handoff
	}
	view, artifactHit, artifactErr := loadPlanExecutePacketArtifact(root, effectivePackID)
	if artifactErr != nil {
		if handoff.State != executorHandoffStateOverridden {
			handoff.State = executorHandoffStateMissing
			handoff.Reason = "artifact_load_error"
		}
		return handoff
	}
	if artifactHit {
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = executorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
			return handoff
		}
		handoff.State = executorHandoffStateStale
		handoff.Reason = "legacy_artifact_fallback"
		handoff.Fresh = false
		return handoff
	}
	if handoff.State != executorHandoffStateOverridden {
		handoff.State = executorHandoffStateMissing
		handoff.Reason = "cache_miss"
	}
	return handoff
}

func applyExecutorHandoffView(handoff executorHandoffPacket, view comprehension.PackResult) executorHandoffPacket {
	handoff.PackID = firstNonEmptyString(normalizePlanExecutePackID(view.PackID), handoff.PackID)
	handoff.IndexID = sanitizePlanExecuteEventToken(firstNonEmptyString(view.IndexID, handoff.IndexID))
	handoff.Fresh = view.Fresh
	handoff.CandidateFiles = len(view.CandidateFiles)
	handoff.CandidateSymbols = len(view.CandidateSymbols)
	handoff.Tests = len(view.Tests)
	return handoff
}

func normalizePlanExecuteEventPackID(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	normalized := normalizePlanExecutePackID(raw)
	if normalized == "" {
		return "", raw == ""
	}
	return normalized, true
}

func normalizePlanExecutePackID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if isPlanExecutePackID(value) {
		return value
	}
	return ""
}

func sanitizePlanExecuteEventID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planExecuteEventTextLooksSensitive(value) {
		return "redacted:" + planExecuteEventHash(value)
	}
	return sanitizePlanExecuteEventToken(value)
}

func sanitizePlanExecuteEventToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || planExecuteEventTextLooksSensitive(value) {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == '.' || r == ':':
			if !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
		if b.Len() >= 120 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func planExecuteEventTextLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"raw_prompt", "raw prompt", "raw_body", "raw body", "raw_content", "raw content",
		"prompt_body", "prompt body", "transcript", "file_body", "file body", "file-body",
		"private key", "secret", "token=", "token:", "access_token", "api token",
		"secret=", "api_key", "api-key", "password=", "bearer ", "customer-",
		"customer_", "secrets/", "/secrets/", "\\secrets\\",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if token == "token" || token == "tokens" || token == "customer" || token == "customers" {
			return true
		}
	}
	normalized := strings.ReplaceAll(lower, "\\", "/")
	for _, marker := range []string{"/users/", "/home/", "/volumes/", "/private/", "c:/users/"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func planExecuteEventHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

type reviewRepairPhaseReport struct {
	Skipped        bool
	Status         string
	Repairs        int
	AutoRepairs    int
	ReviewPlan     string
	ChecklistFound bool
	ReasonClass    string
}

func (r reviewRepairPhaseReport) String() string {
	if r.Skipped {
		return "review-repair: skipped"
	}
	status := strings.TrimSpace(r.Status)
	if status == "" {
		status = "not run"
	}
	if r.Repairs == 0 {
		if r.AutoRepairs == 0 {
			return fmt.Sprintf("review-repair: %s", status)
		}
		return fmt.Sprintf("review-repair: %s, %d mechanical repair(s)", status, r.AutoRepairs)
	}
	if r.AutoRepairs == 0 {
		return fmt.Sprintf("review-repair: %s, %d corrective edit(s)", status, r.Repairs)
	}
	return fmt.Sprintf("review-repair: %s, %d corrective edit(s), %d mechanical repair(s)", status, r.Repairs, r.AutoRepairs)
}

type patchHygieneAutoRepairReport struct {
	Repaired bool
	Paths    []string
	Lines    int
}

type reviewRepairEvidence struct {
	Diff          string
	DiffCheck     string
	DiffCheckExit int
	PhaseEvidence phaseEvidenceBundle
}

type reviewRepairBaseline struct {
	Files map[string]reviewRepairFileSnapshot
}

type reviewRepairFileSnapshot struct {
	Before string
	Exists bool
}

func runReviewRepairPhase(
	cfg config.Config,
	originalPrompt string,
	plan workspace.PlanArtifact,
	requiredEditPaths []string,
	baseline *reviewRepairBaseline,
	executorReport executorPhaseReport,
	opts planExecuteOptions,
	stdout io.Writer,
	promptRun cmdruntime.PromptRunner,
	rec *telemetry.Recorder,
) (reviewRepairPhaseReport, error) {
	changedPaths := planChangedPaths(plan)
	if len(changedPaths) == 0 {
		return reviewRepairPhaseReport{Status: "skipped (no changed paths)"}, nil
	}
	allowedRepairPaths := mergePlanExecutePaths(changedPaths, requiredEditPaths)
	evidence := collectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	autoRepair, autoRepairErr := autoRepairReviewPatchHygiene(cfg.Workspace.Root, changedPaths, evidence)
	if autoRepairErr != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_auto_patch_hygiene_failed", map[string]any{
			"reason": autoRepairErr.Error(),
		})
		return reviewRepairPhaseReport{Status: "failed"}, fmt.Errorf("review-repair auto patch hygiene failed: %w", autoRepairErr)
	}
	if autoRepair.Repaired {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_auto_patch_hygiene", map[string]any{
			"paths": autoRepair.Paths,
			"lines": autoRepair.Lines,
		})
		evidence = collectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	}
	evidence.PhaseEvidence = collectPlanExecutePhaseEvidence(cfg.Workspace.Root, "review_repair", plan, changedPaths, requiredEditPaths, allowedRepairPaths, evidence, executorReport)
	editSetObligations := planExecuteScopeContract(opts, originalPrompt).EditSetObligations
	evidence.PhaseEvidence.EditSetObligations = editSetObligations
	evidence.PhaseEvidence.OpenObligations = append(evidence.PhaseEvidence.OpenObligations, editSetObligations.MissingOpenObligations(changedPaths)...)
	reviewSessionID := strings.TrimSpace(opts.ReviewSessionID)
	if reviewSessionID == "" {
		reviewSessionID = newPlanExecuteSessionID()
	}
	reviewCfg := cfg
	reviewCfg.Workspace.SessionID = reviewSessionID
	reviewStart := time.Now().Add(-1 * time.Second)
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_start", map[string]any{
		"session_id":                reviewSessionID,
		"changed_paths":             changedPaths,
		"allowed_paths":             allowedRepairPaths,
		"edit_set_obligations":      editSetObligations,
		"edit_set_obligation_count": len(editSetObligations),
		"phase_evidence":            phaseEvidenceTelemetry(evidence.PhaseEvidence),
	})
	if planExecuteDeterministicCleanReviewEnabled() {
		reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
		if reason == "" {
			reason = editSetObligations.RejectionReason(changedPaths)
		}
		if reason == "" && len(evidence.PhaseEvidence.OpenObligations) == 0 {
			emitPlanExecuteSyntheticTurnSummary(stdout, opts.JSONEvents, "review_repair_deterministic_pass")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":        "pass",
				"repairs":       0,
				"deterministic": true,
			})
			return reviewRepairPhaseReport{Status: "pass (deterministic evidence)", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}
	}
	prompt := planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
	reviewMaxTokens := opts.MaxTokens
	if reviewMaxTokens <= 0 {
		reviewMaxTokens = 8192
	}
	var planPath string
	var found bool
	for attempt := 0; ; attempt++ {
		answer, err := promptRun(reviewCfg, prompt, cmdruntime.RunOptions{
			JSONEvents:                  opts.JSONEvents,
			TaskFamily:                  agent.PlanThenExecuteReviewRepairTaskFamily,
			PlannerOwnedVerification:    true,
			MaxTokensOverride:           reviewMaxTokens,
			DisableThinking:             true,
			ReviewRepairOpenObligations: append([]string(nil), evidence.PhaseEvidence.OpenObligations...),
			Telemetry:                   rec,
		}, stdout)
		if err != nil {
			reasonClass := reviewRepairDegradedReasonClass(err)
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_degraded", map[string]any{
				"status":        "review_unavailable",
				"reason_class":  reasonClass,
				"changed_paths": changedPaths,
				"allowed_paths": allowedRepairPaths,
			})
			return reviewRepairPhaseReport{
				Status:      "degraded (review unavailable)",
				AutoRepairs: autoRepair.Lines,
				ReasonClass: reasonClass,
			}, nil
		}
		planPath, found, err = findOptionalReviewPlanArtifact(cfg.Workspace.Root, reviewStart, reviewSessionID)
		if err != nil {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_failed", map[string]any{
				"reason": err.Error(),
			})
			return reviewRepairPhaseReport{Status: "failed"}, err
		}
		if found {
			break
		}
		if plancontract.ReviewRepairAnswerDeclaresPass(answer) && plancontract.ReviewRepairTextHasChecklist(answer) {
			reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
			if reason == "" {
				reason = reviewAnswerPassContradictionReason(answer)
			}
			if reason == "" {
				reason = editSetObligations.RejectionReason(changedPaths)
			}
			if reason != "" {
				emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_pass_rejected", map[string]any{
					"reason":                    reason,
					"edit_set_obligations":      editSetObligations,
					"edit_set_obligation_count": len(editSetObligations),
					"retry_available":           attempt == 0,
				})
				if attempt == 0 {
					emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_retry", map[string]any{
						"reason": reason,
					})
					prompt = planExecuteReviewRepairRejectedPassRetryPrompt(prompt, reason)
					continue
				}
				return reviewRepairPhaseReport{Status: "failed", ChecklistFound: true}, fmt.Errorf("review-repair phase rejected contradictory `Decision: pass`: %s", reason)
			}
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":  "pass",
				"repairs": 0,
			})
			return reviewRepairPhaseReport{Status: "pass (no corrective edits)", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}
		if reviewAnswerDeclaresBlocker(answer) {
			return reviewRepairPhaseReport{Status: "blocked"}, fmt.Errorf("review-repair phase reported a blocker without a corrective plan")
		}
		return reviewRepairPhaseReport{Status: "inconclusive"}, fmt.Errorf("review-repair phase produced no corrective plan and no `Decision: pass` with `Verification Checklist`")
	}
	repairPlan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		return reviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("read review-repair plan artifact %q: %w", planPath, err)
	}
	if !plancontract.ReviewRepairTextHasChecklist(repairPlan.Rationale) {
		return reviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("review-repair plan %q is missing `Verification Checklist` in submit_plan rationale", planPath)
	}
	repairPaths := planChangedPaths(repairPlan)
	combinedPaths := mergePlanExecutePaths(changedPaths, repairPaths)
	if reason := editSetObligations.RejectionReason(combinedPaths); reason != "" {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_plan_rejected", map[string]any{
			"reason":                    reason,
			"review_plan":               planPath,
			"changed_paths":             changedPaths,
			"repair_paths":              repairPaths,
			"combined_paths":            combinedPaths,
			"edit_set_obligations":      editSetObligations,
			"edit_set_obligation_count": len(editSetObligations),
		})
		return reviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath, ChecklistFound: true}, fmt.Errorf("review-repair phase rejected corrective plan: %s", reason)
	}
	repairReport, err := runExecutorPhaseScoped(cfg, repairPlan, nil, allowedRepairPaths, opts.JSONEvents, stdout)
	if err != nil {
		return reviewRepairPhaseReport{
			Status:         "failed",
			Repairs:        repairReport.Succeeded,
			AutoRepairs:    autoRepair.Lines,
			ReviewPlan:     planPath,
			ChecklistFound: true,
		}, fmt.Errorf("review-repair executor failed: %w", err)
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
		"status":        "repaired",
		"repairs":       repairReport.Succeeded,
		"review_plan":   planPath,
		"changed_paths": allowedRepairPaths,
	})
	return reviewRepairPhaseReport{
		Status:         "repaired",
		Repairs:        repairReport.Succeeded,
		AutoRepairs:    autoRepair.Lines,
		ReviewPlan:     planPath,
		ChecklistFound: true,
	}, nil
}

func reviewRepairDegradedReasonClass(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case reason == "":
		return "review_model_error"
	case strings.Contains(reason, "eof"):
		return "backend_eof"
	case strings.Contains(reason, "connection refused"), strings.Contains(reason, "connect: connection"):
		return "backend_unreachable"
	case strings.Contains(reason, "context deadline"), strings.Contains(reason, "timeout"), strings.Contains(reason, "deadline exceeded"):
		return "backend_timeout"
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "cancelled"):
		return "backend_canceled"
	default:
		return "review_model_error"
	}
}

func findOptionalReviewPlanArtifact(root string, notBefore time.Time, sessionID string) (string, bool, error) {
	path, err := findLatestPlanArtifact(root, notBefore, sessionID)
	if err == nil {
		return path, true, nil
	}
	text := err.Error()
	if strings.Contains(text, "no plan artifacts under") || strings.Contains(text, "plan dir") {
		return "", false, nil
	}
	return "", false, err
}

func phaseEvidencePassRejectionReason(bundle phaseEvidenceBundle) string {
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
			return planexecute.RunFailureVerificationRequired
		}
	}
	return ""
}

func planExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence reviewRepairEvidence) string {
	return planexecute.PlanExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, toSharedReviewRepairEvidence(evidence))
}

func toSharedReviewRepairEvidence(evidence reviewRepairEvidence) planexecute.ReviewRepairEvidence {
	shared := planexecute.ReviewRepairEvidence{
		Diff:          evidence.Diff,
		DiffCheck:     evidence.DiffCheck,
		DiffCheckExit: evidence.DiffCheckExit,
	}
	data, err := json.Marshal(evidence.PhaseEvidence)
	if err == nil {
		_ = json.Unmarshal(data, &shared.PhaseEvidence)
	}
	return shared
}

func planExecuteReviewRepairRejectedPassRetryPrompt(basePrompt string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "phase evidence contradicted the pass decision"
	}
	return strings.Join([]string{
		basePrompt,
		"",
		"## Rejected Previous Review Outcome",
		"",
		"The previous review-repair answer used `Decision: pass`, but the phase evidence rejected that pass decision: " + truncatePlanExecuteText(reason, 2000) + ".",
		"",
		"For this retry, `Decision: pass` is not an allowed terminal outcome. Choose exactly one visible outcome:",
		"- Corrective plan: call `submit_plan` with only the executable corrective edits needed to resolve the rejected checklist item. The rationale must start with `Verification Checklist:`.",
		"- Blocker: return `<<QUESTION>>:` with the exact failed checklist item if it cannot be repaired inside Allowed Repair Paths.",
		"",
		"Do not repeat `Decision: pass`. Do not add prose before or after the corrective plan or blocker.",
	}, "\n")
}

func collectReviewRepairEvidence(root string, paths []string, baseline *reviewRepairBaseline) reviewRepairEvidence {
	if baseline != nil {
		diff := collectReviewRepairDiffFromBaseline(root, paths, *baseline)
		check, exit := reviewRepairWhitespaceCheck(diff)
		return reviewRepairEvidence{Diff: diff, DiffCheck: check, DiffCheckExit: exit}
	}
	return reviewRepairEvidence{
		Diff:          runGitEvidence(root, append([]string{"diff", "--"}, paths...)...),
		DiffCheck:     runGitEvidence(root, append([]string{"diff", "--check", "--"}, paths...)...),
		DiffCheckExit: runGitEvidenceExit(root, append([]string{"diff", "--check", "--"}, paths...)...),
	}
}

func autoRepairReviewPatchHygiene(root string, changedPaths []string, evidence reviewRepairEvidence) (patchHygieneAutoRepairReport, error) {
	if evidence.DiffCheckExit != 1 {
		return patchHygieneAutoRepairReport{}, nil
	}
	trailingWhitespace := reviewRepairTrailingWhitespaceLines(evidence.Diff)
	if len(trailingWhitespace) == 0 {
		return patchHygieneAutoRepairReport{}, nil
	}
	changedSet := planExecutePathSet(changedPaths)
	paths := make([]string, 0, len(trailingWhitespace))
	for path := range trailingWhitespace {
		normalized := normalizePlanExecutePath(path)
		if _, ok := changedSet[normalized]; !ok {
			return patchHygieneAutoRepairReport{}, nil
		}
		paths = append(paths, normalized)
	}
	slices.Sort(paths)

	report := patchHygieneAutoRepairReport{Repaired: true, Paths: paths}
	for _, path := range paths {
		fullPath, err := planExecuteWorkspaceFile(root, path)
		if err != nil {
			return patchHygieneAutoRepairReport{}, err
		}
		body, err := os.ReadFile(fullPath)
		if err != nil {
			return patchHygieneAutoRepairReport{}, fmt.Errorf("read %s for patch hygiene repair: %w", path, err)
		}
		repaired, lines := trimTrailingWhitespaceOnLines(string(body), trailingWhitespace[path])
		if lines == 0 {
			continue
		}
		if err := os.WriteFile(fullPath, []byte(repaired), 0o644); err != nil {
			return patchHygieneAutoRepairReport{}, fmt.Errorf("write %s for patch hygiene repair: %w", path, err)
		}
		report.Lines += lines
	}
	if report.Lines == 0 {
		return patchHygieneAutoRepairReport{}, nil
	}
	return report, nil
}

func captureReviewRepairBaseline(root string, paths []string) reviewRepairBaseline {
	baseline := reviewRepairBaseline{Files: map[string]reviewRepairFileSnapshot{}}
	for _, path := range mergePlanExecutePaths(paths) {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			baseline.Files[path] = reviewRepairFileSnapshot{Exists: false}
			continue
		}
		baseline.Files[path] = reviewRepairFileSnapshot{Before: string(data), Exists: true}
	}
	return baseline
}

func collectReviewRepairDiffFromBaseline(root string, paths []string, baseline reviewRepairBaseline) string {
	var b strings.Builder
	for _, path := range mergePlanExecutePaths(paths) {
		snapshot, ok := baseline.Files[path]
		if !ok {
			snapshot = reviewRepairFileSnapshot{Exists: false}
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		var after string
		afterData, err := os.ReadFile(filepath.Clean(full))
		afterExists := err == nil
		if err == nil {
			after = string(afterData)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(&b, "# %s: current content unavailable: %v\n", path, err)
			continue
		}
		if !snapshot.Exists && !afterExists {
			continue
		}
		diff := planexecute.UnifiedDiff(path, snapshot.Before, after)
		if strings.TrimSpace(diff) == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func reviewRepairWhitespaceCheck(diff string) (string, int) {
	findings := reviewRepairTrailingWhitespaceFindings(diff)
	if len(findings) == 0 {
		return "", 0
	}
	return strings.Join(findings, "\n"), 1
}

func reviewRepairTrailingWhitespaceFindings(diff string) []string {
	var findings []string
	path := ""
	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			inHunk = false
			newLine = 0
			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true
			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}
		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			content := line[1:]
			if strings.TrimRight(content, " \t") != content {
				loc := path
				if newLine > 0 {
					loc = fmt.Sprintf("%s:%d", firstNonEmptyString(path, "(unknown)"), newLine)
				}
				findings = append(findings, fmt.Sprintf("%s: trailing whitespace.\n%s", loc, line))
			}
			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
			// Removal lines do not advance the new-file cursor.
		}
	}
	return findings
}

func reviewRepairTrailingWhitespaceLines(diff string) map[string]map[int]struct{} {
	out := make(map[string]map[int]struct{})
	path := ""
	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = normalizePlanExecutePath(strings.TrimSpace(strings.TrimPrefix(line, "+++ b/")))
			inHunk = false
			newLine = 0
			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true
			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}
		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			content := line[1:]
			if path != "" && newLine > 0 && strings.TrimRight(content, " \t") != content {
				if _, ok := out[path]; !ok {
					out[path] = make(map[int]struct{})
				}
				out[path][newLine] = struct{}{}
			}
			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
			// Removal lines do not advance the new-file cursor.
		}
	}
	return out
}

func trimTrailingWhitespaceOnLines(text string, targetLines map[int]struct{}) (string, int) {
	if len(targetLines) == 0 {
		return text, 0
	}
	var b strings.Builder
	b.Grow(len(text))
	changed := 0
	lineNo := 1
	for start := 0; start < len(text); lineNo++ {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}
		line := text[start:end]
		newline := ""
		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}
		if _, ok := targetLines[lineNo]; ok {
			repaired := trimTrailingWhitespaceBeforeLineEnding(line)
			if repaired != line {
				changed++
				line = repaired
			}
		}
		b.WriteString(line)
		b.WriteString(newline)
		start = end
	}
	return b.String(), changed
}

func trimTrailingWhitespaceEveryLine(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for start := 0; start < len(text); {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}
		line := text[start:end]
		newline := ""
		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}
		b.WriteString(trimTrailingWhitespaceBeforeLineEnding(line))
		b.WriteString(newline)
		start = end
	}
	return b.String()
}

func trimTrailingWhitespaceBeforeLineEnding(line string) string {
	if strings.HasSuffix(line, "\r") {
		return strings.TrimRight(line[:len(line)-1], " \t") + "\r"
	}
	return strings.TrimRight(line, " \t")
}

func planExecuteWorkspaceFile(root string, path string) (string, error) {
	normalized := normalizePlanExecutePath(path)
	if normalized == "" {
		return "", fmt.Errorf("empty workspace path")
	}
	if filepath.IsAbs(normalized) {
		return "", fmt.Errorf("workspace path must be relative: %s", normalized)
	}
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	fullPath := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(normalized)))
	rel, err := filepath.Rel(cleanRoot, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) {
		return "", fmt.Errorf("workspace path escapes root: %s", normalized)
	}
	return fullPath, nil
}

func parseReviewRepairHunkNewStart(line string) int {
	plus := strings.Index(line, " +")
	if plus < 0 {
		return 0
	}
	rest := line[plus+2:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return value
}

func runGitEvidence(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

func runGitEvidenceExit(root string, args ...string) int {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 127
	}
	return 0
}

func planChangedPaths(plan workspace.PlanArtifact) []string {
	return planexecute.PlanChangedPaths(plan)
}

func mergePlanExecutePaths(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range groups {
		for _, path := range group {
			normalized := normalizePlanExecutePath(path)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func planExecuteAnswerDeclaresBlocker(answer string) bool {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") ||
		strings.Contains(lower, "decision: block") ||
		strings.HasPrefix(lower, "blocker:") ||
		strings.Contains(lower, "\nblocker:")
}

func planExecuteBlockerSummary(answer string) string {
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		const max = 240
		if len(line) > max {
			return line[:max] + "..."
		}
		return line
	}
	return "planner stopped with a blocker"
}

func reviewAnswerPassContradictionReason(answer string) string {
	return planexecute.ReviewAnswerPassContradictionReason(answer)
}

func truncatePlanExecuteText(text string, maxBytes int) string {
	return planexecute.TruncatePlanExecuteText(text, maxBytes)
}

// runExecutorPhase iterates the plan and dispatches each edit
// against a fresh workspace.Manager rooted at the configured repo.
// It does NOT use a separate model — the plan IS the source of
// truth. Failure of one edit aborts the phase and surfaces the
// specific failure so the caller (planner re-run, human, etc.) can
// react.
func runExecutorPhase(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	return runExecutorPhaseScoped(cfg, plan, requiredEditPaths, nil, jsonEvents, stdout, progress...)
}

func runExecutorPhaseScoped(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	return runExecutorPhaseScopedWithVerification(cfg, plan, requiredEditPaths, allowedEditPaths, nil, jsonEvents, stdout, progress...)
}

func runExecutorPhaseScopedWithVerification(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationCommands []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	shared, err := planexecute.RunExecutorPhaseScopedWithVerification(cfg, plan, requiredEditPaths, allowedEditPaths, verificationCommands, jsonEvents, stdout, progress...)
	return fromSharedExecutorPhaseReport(shared), err
}

func runExecutorPhaseScopedWithVerificationPlan(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationPlan planexecute.VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	shared, err := planexecute.RunExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, verificationPlan, jsonEvents, stdout, progress...)
	return fromSharedExecutorPhaseReport(shared), err
}

func runPlanExecuteVerificationPlan(root string, plan planexecute.VerificationPlan, jsonEvents bool, stdout io.Writer) (int, []phaseEvidenceCheck, error) {
	checked, evidence, err := planexecute.RunVerificationPlan(root, plan, jsonEvents, stdout)
	return checked, fromSharedPhaseEvidenceChecks(evidence), err
}

// planEditPreflightMismatch describes the first edit whose old_text
// does not appear in its target file. Used by preflightPlanEdits to
// abort the executor phase before any edit lands when a plan would
// otherwise partially-apply.
type planEditPreflightMismatch struct {
	Index  int
	Path   string
	Reason string
}

func planEditPreflightMismatchFromPackage(mismatch *planexecute.PlanEditPreflightMismatch) *planEditPreflightMismatch {
	if mismatch == nil {
		return nil
	}
	return &planEditPreflightMismatch{
		Index:  mismatch.Index,
		Path:   mismatch.Path,
		Reason: mismatch.Reason,
	}
}

func planExecutePathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if normalized := normalizePlanExecutePath(path); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func preflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *planEditPreflightMismatch {
	return planEditPreflightMismatchFromPackage(planexecute.PreflightPlanUnsupportedProofClaims(plan))
}

func normalizePlanExecutePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	if path == "" || path == "." || path == ".." || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return path
}

// preflightPlanEdits validates every plan edit's old_text against the
// current file content before any edit dispatches. Returns nil when
// every substring-shaped edit matches; otherwise returns a pointer to
// the first mismatch. Range-shaped edits (start_line+end_line+content,
// no old_text) are skipped — those don't depend on substring matching.
//
// The walk is deliberately read-only: it loads each unique path once
// and reuses the buffer for every edit on that path. Edits ordered K
// and K+1 on the same path are validated against the SAME file
// content (the version on disk before any executor dispatch); this
// matches the executor's per-edit substring search semantics, which
// look at the file as edited up to that point. For a plan with two
// non-overlapping edits on the same file, both anchors must appear
// in the original content; once edit K lands, edit K+1's anchor must
// still appear in the modified content. The preflight catches the
// common case where the plan's anchor is wrong from the start.
func preflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	return planEditPreflightMismatchFromPackage(planexecute.PreflightPlanEdits(workspaceRoot, plan))
}

// emitPlanExecuteJSONEvent writes one JSONL line to stdout when the
// orchestrator is running with --json. Each event carries an
// idiom.plan_execute.* type so downstream log parsers can recognize
// orchestrator-emitted events distinct from the runner's JSON stream
// (which closes when the planner phase exits).
//
// All events include an ISO-8601 timestamp so reviewers can stitch
// them into the planner-phase events using ts ordering.
func emitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type": "idiom.plan_execute." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal on a map[string]any with primitive values
		// shouldn't fail; if it does, swallow rather than crash the
		// orchestrator — the executor phase has already done the
		// real work and a missing telemetry event isn't a blocker.
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func emitPlanExecuteSyntheticTurnSummary(stdout io.Writer, jsonEvents bool, stopReason string) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type":                       "turn_summary",
		"ts":                         time.Now().UTC().Format(time.RFC3339Nano),
		"lane":                       "plan-execute",
		"used_model_calls":           0,
		"max_model_calls":            0,
		"used_tool_results":          0,
		"max_tool_steps":             0,
		"prompt_tokens":              0,
		"completion_tokens":          0,
		"total_tokens":               0,
		"progress_turns":             0,
		"rejected_actions":           0,
		"completion_gate_rejections": 0,
		"context_compactions":        0,
		"duration_ms":                0,
		"stop_reason":                strings.TrimSpace(stopReason),
		"budget_exhausted":           false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func planExecuteDeterministicCleanReviewEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("IDIOM_PLAN_EXECUTE_DETERMINISTIC_CLEAN_REVIEW")), "true")
}

func buildCLIWriterRouteDecision(cfg config.Config, opts cliWriterRouteOptions) writerroute.Decision {
	aliases := opts.RoleAliases
	if len(aliases) == 0 {
		aliases = cliWriterRouteRoleAliases(cfg.Workspace.Root, "")
	}
	plannerModel := strings.TrimSpace(opts.PlannerModel)
	if plannerModel == "" {
		plannerModel = cliWriterRouteModelForRole(cfg, aliases, backends.RolePlanner)
	}
	executorModel := strings.TrimSpace(opts.ExecutorModel)
	if executorModel == "" {
		executorModel = cliWriterRouteModelForRole(cfg, aliases, backends.RoleExecutor)
	}
	classifierModel := strings.TrimSpace(opts.ClassifierModel)
	if classifierModel == "" {
		classifierModel = cliWriterRouteModelForRole(cfg, aliases, backends.RoleClassifier)
	}
	reviewerModel := strings.TrimSpace(opts.ReviewerModel)
	if reviewerModel == "" {
		reviewerModel = executorModel
	}
	return writerroute.NewDecisionFromParams(writerroute.DecisionParams{
		Mode:              opts.Mode,
		Lane:              opts.Lane,
		PlannerModel:      plannerModel,
		ExecutorModel:     executorModel,
		ReviewerModel:     reviewerModel,
		ClassifierModel:   classifierModel,
		RoleAliases:       aliases,
		VerificationOwner: strings.TrimSpace(opts.VerificationOwner),
		RequiredEditPaths: opts.RequiredEditPaths,
		ReasonCodes:       opts.ReasonCodes,
		TaskFamily:        opts.TaskFamily,
	})
}

func cliWriterRouteRoleAliases(projectRoot string, profileName string) map[string]string {
	file, ok, err := backends.LoadFromProjectRoot(projectRoot)
	if err != nil || !ok {
		return nil
	}
	profile, found := file.Profile(profileName)
	if !found {
		return nil
	}
	aliases := make(map[string]string, 3)
	for _, role := range []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier} {
		if alias, ok := profile.AliasForRole(role); ok && strings.TrimSpace(alias) != "" {
			aliases[role] = strings.TrimSpace(alias)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func cliWriterRouteModelForRole(cfg config.Config, aliases map[string]string, role string) string {
	if alias := strings.TrimSpace(aliases[role]); alias != "" {
		return alias
	}
	switch role {
	case backends.RoleExecutor:
		if cfg.Implementer != nil && strings.TrimSpace(cfg.Implementer.Model) != "" {
			return strings.TrimSpace(cfg.Implementer.Model)
		}
		return strings.TrimSpace(cfg.LlamaServer.Model)
	case backends.RoleClassifier:
		if strings.TrimSpace(cfg.RouteClassifier.Model) != "" {
			return strings.TrimSpace(cfg.RouteClassifier.Model)
		}
		return strings.TrimSpace(cfg.LlamaServer.Model)
	default:
		return strings.TrimSpace(cfg.LlamaServer.Model)
	}
}

func firstNonEmptyString(values ...string) string {
	return planexecute.FirstNonEmptyString(values...)
}

func FirstNonEmptyString(values ...string) string {
	return firstNonEmptyString(values...)
}
