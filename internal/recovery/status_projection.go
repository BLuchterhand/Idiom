package recovery

import "strings"

type PacketStatus string

const (
	PacketStatusReady           PacketStatus = "ready"
	PacketStatusNeedsInspection PacketStatus = "needs_inspection"
	PacketStatusBlocked         PacketStatus = "blocked"
	PacketStatusPartial         PacketStatus = "partial"
	PacketStatusRecovered       PacketStatus = "recovered"
)

type PacketStatusProjection struct {
	Status                 PacketStatus
	NextAction             string
	Blockers               []string
	ResidualWork           []ResidualWork
	CandidateAtoms         []CandidateAtom
	Reconciliation         *Reconciliation
	ReconciliationFindings []ReconciliationValidationFinding
}

func ProjectPacketStatus(packet DecompositionPacket, reconciliation *Reconciliation) PacketStatusProjection {
	projection := PacketStatusProjection{
		Status:         StatusForPacket(packet),
		NextAction:     packet.NextAction,
		Blockers:       PacketBlockers(packet),
		ResidualWork:   cloneResidualWork(packet.ResidualWork),
		CandidateAtoms: cloneCandidateAtoms(packet.CandidateAtoms),
	}
	if reconciliation == nil {
		return projection
	}

	reconciliationCopy := *reconciliation
	projection.Reconciliation = &reconciliationCopy
	findings := ValidateReconciliation(packet, reconciliationCopy)
	projection.ReconciliationFindings = cloneReconciliationFindings(findings)
	if len(findings) > 0 {
		projection.Status = PacketStatusBlocked
		projection.NextAction = NextActionInspectFailure
		projection.Blockers = append(projection.Blockers, reconciliationFindingBlockers(findings)...)
		projection.Blockers = compactStatusStrings(append(projection.Blockers, reconciliationCopy.Blockers...))
		return projection
	}

	projection.Status = statusWithReconciliation(packet, reconciliationCopy)
	projection.NextAction = reconciliationCopy.NextAction
	projection.CandidateAtoms = remainingCandidateAtoms(packet, reconciliationCopy)
	projection.ResidualWork = remainingResidualWork(packet, projection.CandidateAtoms)
	if reconciliationCopy.Status == ReconciliationStatusRecovered {
		projection.Blockers = nil
	} else {
		projection.Blockers = compactStatusStrings(append(projection.Blockers, reconciliationCopy.Blockers...))
	}
	return projection
}

func StatusForPacket(packet DecompositionPacket) PacketStatus {
	if packet.NextAction != NextActionReviewDecomposition {
		return PacketStatusBlocked
	}
	if len(packet.CandidateAtoms) == 0 {
		return PacketStatusNeedsInspection
	}
	return PacketStatusReady
}

func PacketBlockers(packet DecompositionPacket) []string {
	var blockers []string
	if packet.NextAction != NextActionReviewDecomposition {
		blockers = append(blockers, "packet next action is "+packet.NextAction)
	}
	if len(packet.CandidateAtoms) == 0 {
		blockers = append(blockers, "packet has no candidate recovery atoms")
	}
	if packet.PartialState.DirtyStatus == DirtyStatusDirtyOutScope {
		blockers = append(blockers, "workspace has out-of-scope dirty state")
	}
	return compactStatusStrings(blockers)
}

func statusWithReconciliation(packet DecompositionPacket, reconciliation Reconciliation) PacketStatus {
	if reconciliation.Status == ReconciliationStatusRecovered && reconciliation.NextAction == NextActionNone {
		return PacketStatusRecovered
	}
	if reconciliation.Status == ReconciliationStatusPartial {
		return PacketStatusPartial
	}
	return StatusForPacket(packet)
}

func remainingCandidateAtoms(packet DecompositionPacket, reconciliation Reconciliation) []CandidateAtom {
	if len(reconciliation.CompletedAtoms) == 0 {
		return cloneCandidateAtoms(packet.CandidateAtoms)
	}
	completed := map[string]bool{}
	for _, atom := range reconciliation.CompletedAtoms {
		completed[atom.ID] = true
	}
	remaining := make([]CandidateAtom, 0, len(packet.CandidateAtoms))
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			remaining = append(remaining, atom)
		}
	}
	return remaining
}

func remainingResidualWork(packet DecompositionPacket, atoms []CandidateAtom) []ResidualWork {
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
	remaining := make([]ResidualWork, 0, len(packet.ResidualWork))
	for _, residual := range packet.ResidualWork {
		if needed[residual.ID] {
			remaining = append(remaining, residual)
		}
	}
	return remaining
}

func reconciliationFindingBlockers(findings []ReconciliationValidationFinding) []string {
	blockers := make([]string, 0, len(findings))
	for _, finding := range findings {
		blockers = append(blockers, finding.Code+": "+finding.Message)
	}
	return blockers
}

func cloneResidualWork(residuals []ResidualWork) []ResidualWork {
	if len(residuals) == 0 {
		return nil
	}
	out := make([]ResidualWork, len(residuals))
	copy(out, residuals)
	return out
}

func cloneCandidateAtoms(atoms []CandidateAtom) []CandidateAtom {
	if len(atoms) == 0 {
		return nil
	}
	out := make([]CandidateAtom, len(atoms))
	copy(out, atoms)
	return out
}

func cloneReconciliationFindings(findings []ReconciliationValidationFinding) []ReconciliationValidationFinding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]ReconciliationValidationFinding, len(findings))
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
