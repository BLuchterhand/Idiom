package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/textutil"
)

func (r *Runner) implementationLifecycleSnapshot(results []workspace.ToolResult, stopReason string, writeReadyNudges int) (recoverycontract.ImplementationState, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return recoverycontract.ImplementationState{}, false
	}

	snapshot := recoverycontract.ImplementationState{
		Phase:                      r.implementationLifecyclePhase(results),
		StopReason:                 strings.TrimSpace(stopReason),
		RemainingRequiredEditPaths: uniqueSortedRunnerPaths(r.remainingRequiredEditPaths(results)),
		ChangedPaths:               uniqueSortedRunnerPaths(mapKeys(changedPathsFromToolResults(results))),
		SourcedEvidencePaths:       uniqueSortedRunnerPaths(sourceEvidenceBodyPaths(results)),
		PendingRedirects:           implementationPendingRedirectSnapshots(r.evidenceEngine().PendingRedirectsForBrief(r.RuntimeBrief, results)),
		WriteReadyNudgeCount:       writeReadyNudges,
	}

	return snapshot, recoverycontract.ImplementationStatePresent(snapshot)
}

func (r *Runner) implementationLifecyclePhase(results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          results,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(r.currentAllowedTools, r.RuntimeBrief.AllowedTools),
	})
	if strings.TrimSpace(contract.PhaseID) == "" || contract.PhaseID == turnPhaseGeneric {
		return ""
	}

	return strings.TrimSpace(contract.PhaseID)
}

func implementationPendingRedirectSnapshots(pending []sourceEvidenceObligation) []recoverycontract.ImplementationPendingRedirect {
	if len(pending) == 0 {
		return nil
	}

	out := make([]recoverycontract.ImplementationPendingRedirect, 0, len(pending))
	for _, obligation := range pending {
		path := agentutil.NormalizeRunnerPath(obligation.Path)
		if path == "" {
			continue
		}
		out = append(out, recoverycontract.ImplementationPendingRedirect{
			Path:        path,
			PendingTool: strings.TrimSpace(obligation.PendingTool),
			Source:      strings.TrimSpace(obligation.Source),
			Detail:      textutil.Truncate(strings.TrimSpace(obligation.Detail), 240),
		})
	}

	return out
}

func (r *Runner) emitImplementationLifecycleState(results []workspace.ToolResult, stopReason string, writeReadyNudges int) {
	if r == nil || r.OnEvent == nil {
		return
	}

	snapshot, ok := r.implementationLifecycleSnapshot(results, stopReason, writeReadyNudges)
	if !ok {
		return
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "implementation_lifecycle_state", Content: string(body)})
}

func (r *Runner) attachImplementationLifecycleStatus(payload *runnerCompletionStatus, results []workspace.ToolResult, stopReason string, writeReadyNudges int) {
	if payload == nil {
		return
	}

	snapshot, ok := r.implementationLifecycleSnapshot(results, stopReason, writeReadyNudges)
	if !ok {
		return
	}

	payload.StopReason = snapshot.StopReason
	payload.Phase = snapshot.Phase
	payload.RemainingRequiredEditPaths = snapshot.RemainingRequiredEditPaths
	payload.ChangedPaths = snapshot.ChangedPaths
	payload.SourcedEvidencePaths = snapshot.SourcedEvidencePaths
	payload.PendingRedirects = snapshot.PendingRedirects
	payload.WriteReadyNudgeCount = snapshot.WriteReadyNudgeCount
}
