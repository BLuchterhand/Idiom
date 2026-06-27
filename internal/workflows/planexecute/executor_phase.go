package planexecute

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
)

func RunExecutorPhaseWithDependencies(cfg config.Config, plan planartifact.PlanArtifact, opts ExecutorPhaseOptions, deps Dependencies) (ExecutorPhaseReport, error) {
	deps = normalizeDependencies(deps)
	plan = planartifact.NormalizeLoadedPlanArtifact(plan)

	var verificationPlan VerificationPlan
	if opts.VerificationPlan != nil {
		verificationPlan = *opts.VerificationPlan
	} else {
		verificationPlan = PlanVerification(VerificationPlannerInput{
			WorkspaceRoot:    cfg.Workspace.Root,
			ExplicitCommands: opts.VerificationCommands,
			ChangedPaths:     planChangedPaths(plan),
		})
	}

	return runExecutorPhaseScopedWithDependencies(cfg, plan, ExecutorScopeFromOptions(opts), verificationPlan, executorPhaseOutput{
		Context:    opts.Context,
		JSONEvents: opts.JSONEvents,
		Stdout:     opts.Stdout,
	}, deps, opts.Progress...)
}

type executorPhaseOutput struct {
	Context    context.Context
	JSONEvents bool
	Stdout     io.Writer
}

type ExecutorEditRequest struct {
	Tool        string
	Path        string
	OldText     string
	NewText     string
	StartLine   int
	EndLine     int
	Content     string
	ReplaceAll  bool
	Description string
}

type ExecutorEditResult struct {
	Tool    string
	Summary string
	Error   string
}

type ExecutorEditApplier func(context.Context, config.Workspace, ExecutorEditRequest) ExecutorEditResult

const (
	executorToolEditFile   = "edit_file"
	executorToolSubmitPlan = "submit_plan"
	executorToolWriteFile  = "write_file"
)

func runExecutorPhaseScopedWithVerificationPlan(cfg config.Config, plan planartifact.PlanArtifact, scope ExecutorScope, verificationPlan VerificationPlan, output executorPhaseOutput, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	return runExecutorPhaseScopedWithDependencies(cfg, plan, scope, verificationPlan, output, Dependencies{}, progress...)
}

func runExecutorPhaseScopedWithDependencies(cfg config.Config, plan planartifact.PlanArtifact, scope ExecutorScope, verificationPlan VerificationPlan, output executorPhaseOutput, deps Dependencies, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	deps = normalizeDependencies(deps)
	report := ExecutorPhaseReport{Total: len(plan.Edits)}
	jsonEvents := output.JSONEvents
	stdout := output.Stdout

	report.VerificationCommands = append([]plancontract.VerificationCommand(nil), verificationPlan.Commands...)
	report.VerificationSkips = append([]plancontract.VerificationSkip(nil), verificationPlan.Skips...)
	report.VerificationRejects = append([]plancontract.VerificationReject(nil), verificationPlan.Rejects...)

	if len(plan.Edits) == 0 {
		return report, fmt.Errorf("plan has no edits")
	}

	ctx := planExecuteContext(output.Context)
	handoff := ClassifyExecutorHandoffPacket(cfg.Workspace.Root, plan)

	report.Handoff = handoff
	if err := emitExecutorHandoffPacketEvents(stdout, jsonEvents, handoff); err != nil {
		return report, err
	}

	if mismatch := preflightPlanApprovalAndScope(plan, scope.RequiredEditPaths); mismatch != nil {
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventPlanScopeDrift, planScopeDriftEventFields(handoff, mismatch)); err != nil {
			return report, planExecuteJSONEventWriteError(err)
		}
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("approval/scope %s", mismatch.Path), map[string]any{
			"edit_index":        mismatch.Index,
			"path":              sanitizePlanExecuteEventPath(mismatch.Path),
			"reason":            mismatch.Reason,
			"handoff_id":        handoff.HandoffID,
			"approval_scope_id": handoff.ApprovalScopeID,
		}); err != nil {
			return report, err
		}

		return report, fmt.Errorf("plan approval/scope preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}

	if mismatch := preflightPlanWriteAllowedPaths(scope.WriteAllowedPaths, scope.WriteAllowedExplicit, plan); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("write_allowed_paths %s", mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan write_allowed_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}

	if mismatch := preflightPlanRequiredEditPaths(scope.RequiredEditPaths, plan); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("required_edit_paths %s", mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan required_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}

	if len(scope.RequiredEditPaths) == 0 {
		if mismatch := preflightPlanAllowedEditPaths(scope.AllowedEditPaths, plan); mismatch != nil {
			if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("allowed_edit_paths %s", mismatch.Path), nil); err != nil {
				return report, err
			}
			return report, fmt.Errorf("plan allowed_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
		}
	}

	if mismatch := preflightPlanMissingPathAuthorization(cfg.Workspace.Root, scope.AuthorizedNewFiles, plan); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("authorized_new_files %s", mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan authorized_new_files preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}

	if mismatch := preflightPlanEdits(cfg.Workspace.Root, plan, deps.EditPreflight); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("edit %d/%d %s", mismatch.Index+1, len(plan.Edits), mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}

	if mismatch := preflightPlanExistingSourceDestructiveRewrites(cfg.Workspace.Root, plan, deps.EditPreflight); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("edit %d/%d %s", mismatch.Index+1, len(plan.Edits), mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}

	if mismatch := preflightPlanUnsupportedProofClaims(plan, deps.EditPreflight); mismatch != nil {
		if err := emitExecutorPreflightFailure(stdout, jsonEvents, mismatch, fmt.Sprintf("edit %d/%d %s", mismatch.Index+1, len(plan.Edits), mismatch.Path), nil); err != nil {
			return report, err
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventPreflightOK, map[string]any{
		"edits": len(plan.Edits),
	}); err != nil {
		return report, planExecuteJSONEventWriteError(err)
	}

	executorStartedAt := time.Now()

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorStart, map[string]any{
		"edits":        len(plan.Edits),
		"packet_state": handoff.State,
	}); err != nil {
		return report, planExecuteJSONEventWriteError(err)
	}

	for i, edit := range plan.Edits {
		req, err := executorToolRequestForPlanEdit(cfg.Workspace.Root, edit, deps.EditPreflight)
		if err != nil {
			result := ExecutorEditResult{Tool: executorToolSubmitPlan, Summary: "Executor insert preflight failed", Error: err.Error()}
			status := plancontract.PhaseEvidenceStatusFailed
			if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorEdit, map[string]any{
				"step":   i + 1,
				"total":  len(plan.Edits),
				"path":   edit.Path,
				"status": string(status),
				"error":  strings.TrimSpace(result.Error),
			}); err != nil {
				return report, planExecuteJSONEventWriteError(err)
			}

			if !jsonEvents {
				fmt.Fprintf(stdout, "  edit %d/%d %s ... %s\n", i+1, len(plan.Edits), edit.Path, executorEditOutcome(result))
			}

			report.Failed++

			report.Failures = append(report.Failures, fmt.Sprintf("edits[%d] %s: %s", i, edit.Path, result.Error))
			if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorComplete, map[string]any{
				"total":       report.Total,
				"succeeded":   report.Succeeded,
				"failed":      report.Failed,
				"duration_ms": time.Since(executorStartedAt).Milliseconds(),
			}); err != nil {
				return report, planExecuteJSONEventWriteError(err)
			}

			return report, fmt.Errorf("executor phase aborted on edit %d (%s): %s", i, edit.Path, result.Error)
		}

		result := deps.ApplyExecutorEdit(ctx, cfg.Workspace, req)
		status := plancontract.PhaseEvidenceStatusOK

		if strings.TrimSpace(result.Error) != "" {
			status = plancontract.PhaseEvidenceStatusFailed
		}

		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorEdit, map[string]any{
			"step":   i + 1,
			"total":  len(plan.Edits),
			"path":   edit.Path,
			"status": string(status),
			"error":  strings.TrimSpace(result.Error),
		}); err != nil {
			return report, planExecuteJSONEventWriteError(err)
		}

		if !jsonEvents {
			fmt.Fprintf(stdout, "  edit %d/%d %s ... %s\n", i+1, len(plan.Edits), edit.Path, executorEditOutcome(result))
		}

		if len(progress) > 0 && progress[0] != nil {
			progress[0](i+1, len(plan.Edits), edit.Path, status == plancontract.PhaseEvidenceStatusOK)
		}

		if status == plancontract.PhaseEvidenceStatusFailed {
			report.Failed++

			report.Failures = append(report.Failures, fmt.Sprintf("edits[%d] %s: %s", i, edit.Path, result.Error))
			if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorComplete, map[string]any{
				"total":       report.Total,
				"succeeded":   report.Succeeded,
				"failed":      report.Failed,
				"duration_ms": time.Since(executorStartedAt).Milliseconds(),
			}); err != nil {
				return report, planExecuteJSONEventWriteError(err)
			}

			return report, fmt.Errorf("executor phase aborted on edit %d (%s): %s", i, edit.Path, result.Error)
		}

		report.Succeeded++
	}

	sourceSanityChecked, sourceSanityEvidence, err := runPlanExecuteSourceSanity(ctx, cfg.Workspace, cfg.Workspace.Root, planChangedPaths(plan), jsonEvents, stdout, deps.SourceSanity, deps.SourceSanityCovers)

	report.SourceSanity = sourceSanityEvidence

	if err != nil {
		if emitErr := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorComplete, map[string]any{
			"total":                 report.Total,
			"succeeded":             report.Succeeded,
			"failed":                report.Failed,
			"source_sanity_checked": sourceSanityChecked,
			"source_sanity_status":  string(plancontract.PhaseEvidenceStatusFailed),
			"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
		}); emitErr != nil {
			return report, planExecuteJSONEventWriteError(emitErr)
		}

		return report, err
	}

	verificationChecked, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(ctx, cfg.Workspace.Root, verificationPlan, jsonEvents, stdout)

	report.Verification = verificationEvidence

	if verificationErr != nil {
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorComplete, map[string]any{
			"total":                    report.Total,
			"succeeded":                report.Succeeded,
			"failed":                   report.Failed,
			"source_sanity_checked":    sourceSanityChecked,
			"source_sanity_status":     string(plancontract.PhaseEvidenceStatusPassed),
			"verification_checked":     verificationChecked,
			"verification_status":      string(plancontract.PhaseEvidenceStatusFailed),
			"verification_command_cnt": len(verificationEvidence),
			"duration_ms":              time.Since(executorStartedAt).Milliseconds(),
		}); err != nil {
			return report, planExecuteJSONEventWriteError(err)
		}

		return report, verificationErr
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventExecutorComplete, map[string]any{
		"total":                 report.Total,
		"succeeded":             report.Succeeded,
		"failed":                report.Failed,
		"source_sanity_checked": sourceSanityChecked,
		"source_sanity_status":  string(plancontract.PhaseEvidenceStatusPassed),
		"verification_checked":  verificationChecked,
		"verification_status":   VerificationStatus(verificationEvidence),
		"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
	}); err != nil {
		return report, planExecuteJSONEventWriteError(err)
	}

	return report, nil
}

func executorToolRequestForPlanEdit(workspaceRoot string, edit planartifact.PlanEdit, policy EditPreflightPolicy) (ExecutorEditRequest, error) {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		return ExecutorEditRequest{
			Tool:        executorToolWriteFile,
			Path:        edit.Path,
			Content:     edit.NewText,
			Description: firstNonEmptyString(edit.Purpose, "executor phase: missing/new-file write"),
		}, nil
	}

	if edit.InsertAfterLine > 0 {
		updated, err := projectedInsertAfterLineContent(workspaceRoot, edit, policy)
		if err != nil {
			return ExecutorEditRequest{}, err
		}

		return ExecutorEditRequest{
			Tool:        executorToolWriteFile,
			Path:        edit.Path,
			Content:     updated,
			Description: firstNonEmptyString(edit.Purpose, "executor phase: insert-after-line projection"),
		}, nil
	}

	return ExecutorEditRequest{
		Tool:        executorToolEditFile,
		Path:        edit.Path,
		OldText:     edit.OldText,
		NewText:     edit.NewText,
		StartLine:   edit.StartLine,
		EndLine:     edit.EndLine,
		Content:     edit.Content,
		ReplaceAll:  edit.ReplaceAll,
		Description: firstNonEmptyString(edit.Purpose, "executor phase: planned edit"),
	}, nil
}

func emitExecutorPreflightFailure(stdout io.Writer, jsonEvents bool, mismatch *PlanEditPreflightMismatch, humanScope string, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		}
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventPreflightFailed, fields); err != nil {
		return planExecuteJSONEventWriteError(err)
	}

	if !jsonEvents {
		fmt.Fprintf(stdout, "  preflight FAILED on %s: %s\n", humanScope, mismatch.Reason)
	}

	return nil
}
