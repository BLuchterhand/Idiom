package recovery

import (
	"strings"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
)

func ProjectPacketStatus(packet recoverycontract.DecompositionPacket, reconciliation *recoverycontract.Reconciliation) recoverycontract.PacketStatusProjection {
	projection := recoverycontract.PacketStatusProjection{
		Status:         StatusForPacket(packet),
		NextAction:     packet.NextAction,
		Blockers:       PacketBlockers(packet),
		ResidualWork:   cloneResidualWork(packet.ResidualWork),
		CandidateAtoms: cloneCandidateAtoms(packet.CandidateAtoms),
	}
	if reconciliation == nil {
		projection = classifyPacketStatusProjection(packet, projection)
		return projection
	}

	reconciliationCopy := *reconciliation

	projection.Reconciliation = &reconciliationCopy

	findings := ValidateReconciliation(packet, reconciliationCopy)

	projection.ReconciliationFindings = cloneReconciliationFindings(findings)

	if len(findings) > 0 {
		projection.Status = recoverycontract.PacketStatusBlocked
		projection.NextAction = recoverycontract.NextActionInspectFailure
		projection.Blockers = append(projection.Blockers, reconciliationFindingBlockers(findings)...)
		projection.Blockers = compactStatusStrings(append(projection.Blockers, reconciliationCopy.Blockers...))
		projection = classifyPacketStatusProjection(packet, projection)

		return projection
	}

	projection.Status = statusWithReconciliation(packet, reconciliationCopy)
	projection.NextAction = reconciliationCopy.NextAction
	projection.CandidateAtoms = remainingCandidateAtoms(packet, reconciliationCopy)
	projection.ResidualWork = remainingResidualWork(packet, projection.CandidateAtoms)

	if reconciliationCopy.Status == recoverycontract.ReconciliationStatusRecovered {
		projection.Blockers = nil
	} else {
		projection.Blockers = compactStatusStrings(append(projection.Blockers, reconciliationCopy.Blockers...))
	}

	projection = classifyPacketStatusProjection(packet, projection)

	return projection
}

func classifyPacketStatusProjection(packet recoverycontract.DecompositionPacket, projection recoverycontract.PacketStatusProjection) recoverycontract.PacketStatusProjection {
	projection.WorkflowID = workflowstate.WorkflowRecovery
	projection.Dispatchable = false

	switch {
	case projection.Status == recoverycontract.PacketStatusRecovered:
		projection.WorkflowState = workflowstate.StateTerminal
		projection.WorkflowTerminalState = workflowstate.TerminalStateComplete
	case packet.PartialState.DirtyStatus == recoverycontract.DirtyStatusDirtyOutScope:
		projection.WorkflowState = workflowstate.StateTerminal
		projection.WorkflowTerminalState = workflowstate.TerminalStateDirtyScope
	case packet.PartialState.ScopeStatus == recoverycontract.ScopeStatusOutOfScope:
		projection.WorkflowState = workflowstate.StateTerminal
		projection.WorkflowTerminalState = workflowstate.TerminalStateInvalidScope
	case projection.Status == recoverycontract.PacketStatusReady:
		projection.WorkflowState = workflowstate.StateDecomposing
	case projection.Status == recoverycontract.PacketStatusPartial:
		projection.WorkflowState = workflowstate.StateReconciling
	case projection.Status == recoverycontract.PacketStatusNeedsInspection:
		projection.WorkflowState = workflowstate.StateInspecting
	default:
		projection.WorkflowState = workflowstate.StateTerminal
		projection.WorkflowTerminalState = workflowstate.TerminalStateBlocked
	}

	if err := workflowstate.ValidateStateProjection(projection.WorkflowID, projection.WorkflowState, projection.WorkflowTerminalState, projection.Dispatchable); err != nil {
		projection = blockedPacketStatusProjection(projection, err)
	}

	return projection
}

func blockedPacketStatusProjection(projection recoverycontract.PacketStatusProjection, err error) recoverycontract.PacketStatusProjection {
	projection.Status = recoverycontract.PacketStatusBlocked
	projection.WorkflowID = workflowstate.WorkflowRecovery
	projection.WorkflowState = workflowstate.StateTerminal
	projection.WorkflowTerminalState = workflowstate.TerminalStateBlocked
	projection.Dispatchable = false
	projection.NextAction = recoverycontract.NextActionInspectFailure
	if err != nil {
		projection.Blockers = compactStatusStrings(append(projection.Blockers, "workflow projection invalid: "+err.Error()))
	}
	return projection
}

func StatusForPacket(packet recoverycontract.DecompositionPacket) recoverycontract.PacketStatus {
	if packet.NextAction != recoverycontract.NextActionReviewDecomposition {
		return recoverycontract.PacketStatusBlocked
	}

	if len(packet.CandidateAtoms) == 0 {
		return recoverycontract.PacketStatusNeedsInspection
	}

	return recoverycontract.PacketStatusReady
}

func PacketBlockers(packet recoverycontract.DecompositionPacket) []string {
	var blockers []string

	if packet.NextAction != recoverycontract.NextActionReviewDecomposition {
		blockers = append(blockers, "packet next action is "+string(packet.NextAction))
	}

	if len(packet.CandidateAtoms) == 0 {
		blockers = append(blockers, "packet has no candidate recovery atoms")
	}

	blockers = append(blockers, PartialStateBlockers(packet.PartialState)...)

	return compactStatusStrings(blockers)
}

func PartialStateBlockers(partial recoverycontract.PartialState) []string {
	var blockers []string

	if partial.DirtyStatus == recoverycontract.DirtyStatusDirtyOutScope {
		blockers = append(blockers, "workspace has out-of-scope dirty state")
	}

	if partial.ScopeStatus == recoverycontract.ScopeStatusOutOfScope {
		blockers = append(blockers, "workspace scope status is out_of_scope")
	}

	return compactStatusStrings(blockers)
}

func statusWithReconciliation(packet recoverycontract.DecompositionPacket, reconciliation recoverycontract.Reconciliation) recoverycontract.PacketStatus {
	if reconciliation.Status == recoverycontract.ReconciliationStatusRecovered && reconciliation.NextAction == recoverycontract.NextActionNone {
		return recoverycontract.PacketStatusRecovered
	}

	if reconciliation.Status == recoverycontract.ReconciliationStatusPartial {
		return recoverycontract.PacketStatusPartial
	}

	return StatusForPacket(packet)
}

func remainingCandidateAtoms(packet recoverycontract.DecompositionPacket, reconciliation recoverycontract.Reconciliation) []recoverycontract.CandidateAtom {
	if len(reconciliation.CompletedAtoms) == 0 {
		return cloneCandidateAtoms(packet.CandidateAtoms)
	}

	completed := map[string]bool{}
	for _, atom := range reconciliation.CompletedAtoms {
		completed[atom.ID] = true
	}

	remaining := make([]recoverycontract.CandidateAtom, 0, len(packet.CandidateAtoms))
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			remaining = append(remaining, atom)
		}
	}

	return remaining
}

func remainingResidualWork(packet recoverycontract.DecompositionPacket, atoms []recoverycontract.CandidateAtom) []recoverycontract.ResidualWork {
	if len(atoms) == 0 {
		return nil
	}

	needed := map[string]bool{}

	for _, atom := range atoms {
		for _, id := range atom.ResidualWorkIDs {
			if id = strings.TrimSpace(id); id != "" {
				needed[id] = true
			}
		}
	}

	if len(needed) == 0 {
		return nil
	}

	remaining := make([]recoverycontract.ResidualWork, 0, len(packet.ResidualWork))
	for _, residual := range packet.ResidualWork {
		if needed[residual.ID] {
			remaining = append(remaining, residual)
		}
	}

	return remaining
}

func reconciliationFindingBlockers(findings []recoverycontract.ReconciliationValidationFinding) []string {
	blockers := make([]string, 0, len(findings))
	for _, finding := range findings {
		blockers = append(blockers, finding.Code+": "+finding.Message)
	}

	return blockers
}

func cloneResidualWork(residuals []recoverycontract.ResidualWork) []recoverycontract.ResidualWork {
	if len(residuals) == 0 {
		return nil
	}

	out := make([]recoverycontract.ResidualWork, len(residuals))
	copy(out, residuals)

	return out
}

func cloneCandidateAtoms(atoms []recoverycontract.CandidateAtom) []recoverycontract.CandidateAtom {
	if len(atoms) == 0 {
		return nil
	}

	out := make([]recoverycontract.CandidateAtom, len(atoms))
	copy(out, atoms)

	return out
}

func cloneReconciliationFindings(findings []recoverycontract.ReconciliationValidationFinding) []recoverycontract.ReconciliationValidationFinding {
	if len(findings) == 0 {
		return nil
	}

	out := make([]recoverycontract.ReconciliationValidationFinding, len(findings))
	copy(out, findings)

	return out
}

func compactStatusStrings(values []string) []string {
	seen := map[string]bool{}

	var out []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}

		seen[value] = true
		out = append(out, value)
	}

	return out
}
