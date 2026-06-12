package agent

import (
	"context"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
)

type runnerVerificationBootstrapInput struct {
	ctx              context.Context
	collectedResults []workspace.ToolResult
	metrics          *runnerMetrics
	recorder         *runnerTurnRecorder
	executionState   *runnerExecutionState
	requestBudget    *runnerRequestBudgetState
	autonomyRecorder *runnerAutonomyRecorder
	onToolRequest    func(ToolRequestEvent)
}

func (r *Runner) runVerificationBootstrap(input runnerVerificationBootstrapInput) (string, bool) {
	if r.Workspace == nil || len(input.collectedResults) != 0 {
		return "", false
	}
	req, ok := r.verificationBootstrapRequest()
	if !ok {
		return "", false
	}

	raw := marshalToolRequestRaw(req)
	if input.metrics != nil {
		input.metrics.ToolCalls++
	}
	if input.onToolRequest != nil {
		profile := workspace.DiscoveryProfileForRequest(req)
		input.onToolRequest(ToolRequestEvent{
			Step:           1,
			Agent:          r.agentName(),
			Raw:            raw,
			Request:        req,
			Mode:           r.Workspace.Mode(),
			Broad:          workspace.IsBroadInspectionRequest(req),
			Narrowing:      workspace.IsNarrowingRequest(req),
			Action:         toolActionClass(req),
			DiscoveryClass: profile.Class,
			DiscoveryScope: profile.Scope,
			DiscoveryCost:  profile.Cost,
		})
	}

	result := r.Workspace.Execute(input.ctx, req)
	if input.recorder != nil {
		input.recorder.recordWithOptions(1, req, result, "verification_auto_bootstrap", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
	}
	if input.executionState != nil && input.requestBudget != nil {
		update := input.executionState.applyResult(req, result, toolRequestKey(req), r.Workspace, input.requestBudget)
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		if input.autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}
			input.autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}
	}

	return synthesizeVerificationOnlyAnswer(req, result), true
}
