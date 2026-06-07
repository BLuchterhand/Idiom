package productevidence

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/runtime/localops"
	"idiom/internal/workflows/recovery"
)

type productEvidenceRecovery struct {
	Status                   string         `json:"status"`
	RedactionStatus          string         `json:"redaction_status"`
	ForbiddenFieldCheck      string         `json:"forbidden_field_check"`
	MetadataOnly             bool           `json:"metadata_only"`
	FailureRecordCount       int            `json:"failure_record_count"`
	DecompositionPacketCount int            `json:"decomposition_packet_count"`
	ReconciliationCount      int            `json:"reconciliation_count"`
	OpenPacketCount          int            `json:"open_packet_count"`
	RecoveredPacketCount     int            `json:"recovered_packet_count"`
	PartialPacketCount       int            `json:"partial_packet_count"`
	BlockedPacketCount       int            `json:"blocked_packet_count"`
	CandidateAtomCount       int            `json:"candidate_atom_count"`
	CompletedAtomCount       int            `json:"completed_atom_count"`
	RemainingAtomCount       int            `json:"remaining_atom_count"`
	UnresolvedBlockerCount   int            `json:"unresolved_blocker_count"`
	MalformedArtifactCount   int            `json:"malformed_artifact_count"`
	FailureClasses           map[string]int `json:"failure_classes,omitempty"`
	NextActions              map[string]int `json:"next_valid_actions,omitempty"`
	ArtifactIDs              []string       `json:"artifact_ids,omitempty"`
	ReleaseImpact            string         `json:"release_impact"`
	Blockers                 []string       `json:"blockers,omitempty"`
}

func loadProductEvidenceRecovery(root string) (productEvidenceRecovery, productEvidenceSourceReference) {
	recoveryDir := filepath.Join(root, ".idiom", "recovery")
	ref := productEvidenceSourceReference{
		ID:     "recovery_artifacts",
		Kind:   "recovery",
		Status: "not_available",
		PathID: localops.PathEvidence(recoveryDir),
	}
	summary := productEvidenceRecovery{
		Status:              "not_available",
		RedactionStatus:     "redacted",
		ForbiddenFieldCheck: "pass",
		MetadataOnly:        true,
		FailureClasses:      map[string]int{},
		NextActions:         map[string]int{},
		ReleaseImpact:       "no local recovery artifacts found",
	}
	entries, err := os.ReadDir(recoveryDir)
	if err != nil {
		return summary, ref
	}
	ref.Status = "present"
	var aggregateHashParts []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		failureID := entry.Name()
		record, recordRel, found, err := recovery.TryLoadFailureRecord(root, failureID)
		recordRel = firstNonEmptyString(recordRel, filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "failure.json")))
		aggregateHashParts = append(aggregateHashParts, recoveryEvidenceArtifactHashPart(root, recordRel))
		if err != nil {
			summary.addMalformedRecoveryArtifact("failure_record_decode_failed", recordRel)
			continue
		}
		if !found {
			continue
		}
		summary.FailureRecordCount++
		summary.ArtifactIDs = append(summary.ArtifactIDs, recoveryEvidenceArtifactID("failure_record", recordRel))
		summary.FailureClasses[localops.SafeToken(record.Failure.Class)]++

		packetFull, packetRel, err := recovery.DecompositionPacketPath(root, record.FailureID, "")
		if err != nil {
			summary.addMalformedRecoveryArtifact("packet_path_invalid", recordRel)
			continue
		}
		packet, _, packetFound, err := recovery.TryLoadDecompositionPacket(packetFull)
		aggregateHashParts = append(aggregateHashParts, recoveryEvidenceArtifactHashPart(root, packetRel))
		if err != nil {
			summary.addMalformedRecoveryArtifact("decomposition_packet_decode_failed", packetRel)
			continue
		}
		if !packetFound {
			summary.BlockedPacketCount++
			summary.Blockers = append(summary.Blockers, "missing decomposition packet:"+recoveryEvidenceArtifactID("failure_record", recordRel))
			summary.addNextAction(record.NextAction)
			continue
		}
		summary.DecompositionPacketCount++
		summary.CandidateAtomCount += len(packet.CandidateAtoms)
		summary.ArtifactIDs = append(summary.ArtifactIDs, recoveryEvidenceArtifactID("decomposition_packet", packetRel))
		summary.summarizeRecoveryPacket(root, packet)
	}
	summary.finishRecoveryEvidenceStatus()
	if len(aggregateHashParts) > 0 {
		slices.Sort(aggregateHashParts)
		ref.ArtifactHash = localops.ContentHash([]byte(strings.Join(aggregateHashParts, "\n")))
	}
	return summary, ref
}

func (summary *productEvidenceRecovery) summarizeRecoveryPacket(root string, packet recovery.DecompositionPacket) {
	reconciliation, reconciliationRel, found, err := recovery.TryLoadReconciliationForPacket(root, packet)
	if err != nil {
		summary.addMalformedRecoveryArtifact("reconciliation_decode_failed", reconciliationRel)
		return
	}
	if !found {
		summary.OpenPacketCount++
		summary.RemainingAtomCount += len(packet.CandidateAtoms)
		summary.addNextAction(packet.NextAction)
		if blockers := recoveryEvidencePacketBlockers(packet); len(blockers) > 0 {
			summary.BlockedPacketCount++
			summary.UnresolvedBlockerCount += len(blockers)
			summary.Blockers = append(summary.Blockers, blockers...)
		}
		return
	}
	summary.ArtifactIDs = append(summary.ArtifactIDs, recoveryEvidenceArtifactID("reconciliation", reconciliationRel))
	summary.ReconciliationCount++
	findings := recovery.ValidateReconciliation(packet, reconciliation)
	if len(findings) > 0 {
		summary.BlockedPacketCount++
		for _, finding := range findings {
			summary.Blockers = append(summary.Blockers, "invalid reconciliation:"+localops.SafeToken(finding.Code))
		}
		return
	}
	summary.CompletedAtomCount += len(reconciliation.CompletedAtoms)
	summary.RemainingAtomCount += len(reconciliation.RemainingAtoms)
	summary.addNextAction(reconciliation.NextAction)
	switch reconciliation.Status {
	case recovery.ReconciliationStatusRecovered:
		summary.RecoveredPacketCount++
	case recovery.ReconciliationStatusPartial:
		summary.PartialPacketCount++
		summary.OpenPacketCount++
	default:
		summary.BlockedPacketCount++
		summary.Blockers = append(summary.Blockers, "unsupported reconciliation status:"+localops.SafeToken(reconciliation.Status))
	}
}

func (summary *productEvidenceRecovery) addMalformedRecoveryArtifact(code string, relPath string) {
	summary.MalformedArtifactCount++
	summary.BlockedPacketCount++
	summary.Blockers = append(summary.Blockers, localops.SafeToken(code)+":"+localops.PathEvidence(relPath))
}

func (summary *productEvidenceRecovery) addNextAction(action string) {
	action = localops.SafeToken(action)
	if action == "" {
		action = "unknown"
	}
	summary.NextActions[action]++
}

func (summary *productEvidenceRecovery) finishRecoveryEvidenceStatus() {
	summary.ArtifactIDs = localops.CompactStrings(summary.ArtifactIDs...)
	summary.Blockers = localops.CompactStrings(summary.Blockers...)
	if len(summary.FailureClasses) == 0 {
		summary.FailureClasses = nil
	}
	if len(summary.NextActions) == 0 {
		summary.NextActions = nil
	}
	switch {
	case summary.MalformedArtifactCount > 0 || summary.BlockedPacketCount > 0:
		summary.Status = "blocked"
		summary.ReleaseImpact = "recovery evidence is blocked by malformed, missing, or stale artifacts"
	case summary.OpenPacketCount > 0 || summary.PartialPacketCount > 0:
		summary.Status = "partial"
		summary.ReleaseImpact = fmt.Sprintf("open recovery packets:%d remaining_atoms:%d", summary.OpenPacketCount, summary.RemainingAtomCount)
	case summary.FailureRecordCount > 0:
		summary.Status = "pass"
		summary.ReleaseImpact = "all local recovery packets with evidence are reconciled"
	default:
		summary.Status = "not_available"
		summary.ReleaseImpact = "no local recovery artifacts found"
	}
}

func recoveryEvidenceArtifactHashPart(root string, relPath string) string {
	if strings.TrimSpace(relPath) == "" {
		return ""
	}
	fullPath := relPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, filepath.FromSlash(relPath))
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return relPath + ":missing"
	}
	return relPath + ":" + localops.ContentHash(data)
}

func recoveryEvidenceArtifactID(kind string, relPath string) string {
	return kind + ":" + localops.StableID(relPath)
}

func recoveryEvidencePacketBlockers(packet recovery.DecompositionPacket) []string {
	var blockers []string
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		blockers = append(blockers, "packet next action requires operator:"+localops.SafeToken(packet.NextAction))
	}
	if len(packet.CandidateAtoms) == 0 {
		blockers = append(blockers, "packet has no candidate recovery atoms")
	}
	if packet.PartialState.DirtyStatus == recovery.DirtyStatusDirtyOutScope {
		blockers = append(blockers, "partial state includes out-of-scope dirty paths")
	}
	if packet.PartialState.ScopeStatus == recovery.ScopeStatusOutOfScope {
		blockers = append(blockers, "partial state is out of recovery scope")
	}
	return localops.CompactStrings(blockers...)
}
