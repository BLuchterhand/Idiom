package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"

	"idiom/internal/workflows/recovery"
)

const recoveryToolReportVersion = "idiom.recovery.tool_report.v1"

type recoveryInspectToolReport struct {
	SchemaVersion string                          `json:"schema_version"`
	Status        string                          `json:"status"`
	FailureID     string                          `json:"failure_id,omitempty"`
	SessionID     string                          `json:"session_id,omitempty"`
	RecordPath    string                          `json:"record_path,omitempty"`
	FailureClass  string                          `json:"failure_class,omitempty"`
	FailureKind   string                          `json:"failure_kind,omitempty"`
	PartialState  recoverycontract.PartialState   `json:"partial_state"`
	NextAction    string                          `json:"next_valid_action"`
	Record        *recoverycontract.FailureRecord `json:"record,omitempty"`
	Blockers      []string                        `json:"blockers,omitempty"`
}

type recoveryStatusToolReport struct {
	SchemaVersion      string                                `json:"schema_version"`
	Status             string                                `json:"status"`
	PacketPath         string                                `json:"packet_path,omitempty"`
	ReconciliationPath string                                `json:"reconciliation_path,omitempty"`
	FailureID          string                                `json:"failure_id,omitempty"`
	PacketID           string                                `json:"packet_id,omitempty"`
	PartialState       recoverycontract.PartialState         `json:"partial_state"`
	ProductLinks       recoverycontract.ProductLinks         `json:"product_links,omitempty"`
	Blockers           []string                              `json:"blockers,omitempty"`
	Gates              []string                              `json:"gates,omitempty"`
	CandidateAtoms     []recoverycontract.CandidateAtom      `json:"candidate_atoms,omitempty"`
	Reconciliation     *recoverycontract.Reconciliation      `json:"reconciliation,omitempty"`
	NextAction         string                                `json:"next_valid_action"`
	Packet             *recoverycontract.DecompositionPacket `json:"packet,omitempty"`
}

type recoveryDecomposeToolReport struct {
	SchemaVersion  string                                `json:"schema_version"`
	Status         string                                `json:"status"`
	FailureID      string                                `json:"failure_id,omitempty"`
	PacketID       string                                `json:"packet_id,omitempty"`
	PacketPath     string                                `json:"packet_path,omitempty"`
	PacketSHA256   string                                `json:"packet_sha256,omitempty"`
	CandidateAtoms []recoverycontract.CandidateAtom      `json:"candidate_atoms,omitempty"`
	NextAction     string                                `json:"next_valid_action"`
	Packet         *recoverycontract.DecompositionPacket `json:"packet,omitempty"`
}

func (m *Manager) recoveryInspect(ctx context.Context, req ToolRequest) ToolResult {
	record, recordPath, found, err := m.loadRecoveryFailureRecord(req)
	if err != nil {
		return ToolResult{Tool: "recovery_inspect", Summary: "Recovery inspection rejected", Error: err.Error()}
	}

	if !found {
		return ToolResult{Tool: "recovery_inspect", Summary: "Recovery failure record not found", Error: "recovery failure record not found"}
	}

	partial := m.recoveryPartialState(ctx, record)

	record.PartialState = partial
	record.NextAction = recovery.NextActionForPartialState(record, partial)

	report := recoveryInspectToolReport{
		SchemaVersion: recoveryToolReportVersion,
		Status:        "failed",
		FailureID:     record.FailureID,
		SessionID:     record.SourceRun.SessionID,
		RecordPath:    recordPath,
		FailureClass:  string(record.Failure.Class),
		FailureKind:   record.Failure.Kind,
		PartialState:  partial,
		NextAction:    string(record.NextAction),
		Record:        &record,
		Blockers:      recovery.PartialStateBlockers(partial),
	}

	return jsonToolResult("recovery_inspect", "Read recovery failure record", report)
}

func (m *Manager) recoveryStatus(ctx context.Context, req ToolRequest) ToolResult {
	packetPath, err := m.recoveryPacketPathForRequest(req)
	if err != nil {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery status rejected", Error: err.Error()}
	}

	packet, loadedPath, found, err := recovery.TryLoadSafeDecompositionPacket(m.cfg.Root, packetPath)
	if err != nil {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery status failed", Error: err.Error()}
	}

	if !found {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery decomposition packet not found", Error: "recovery decomposition packet not found"}
	}

	record := recoverycontract.FailureRecordFromDecompositionPacket(packet)
	currentPartial := m.recoveryPartialState(ctx, record)
	if currentPartial.DirtyStatus != "" && currentPartial.DirtyStatus != recoverycontract.DirtyStatusClean {
		packet.PartialState = currentPartial
		packet.NextAction = recovery.NextActionForPartialState(record, currentPartial)
	}

	projection := recovery.ProjectPacketStatus(packet, nil)
	statusPacket := sanitizeRecoveryStatusPacket(packet)

	var reconciliationRef *recoverycontract.Reconciliation

	reconciliationPath := ""

	if reconciliation, relPath, found, reconciliationBlockers := m.recoveryReconciliationForStatus(loadedPath, packet); len(reconciliationBlockers) > 0 {
		projection.Status = recoverycontract.PacketStatusBlocked
		projection.NextAction = recoverycontract.NextActionInspectFailure
		projection.Blockers = textutil.Unique(append(projection.Blockers, reconciliationBlockers...))
		reconciliationPath = relPath
	} else if found {
		reconciliationRef = &reconciliation
		reconciliationPath = relPath
		projection = recovery.ProjectPacketStatus(packet, &reconciliation)
	}
	statusPacket.ResidualWork = projection.ResidualWork
	statusPacket.CandidateAtoms = projection.CandidateAtoms
	statusPacket = sanitizeRecoveryStatusPacket(statusPacket)

	report := recoveryStatusToolReport{
		SchemaVersion:      recoveryToolReportVersion,
		Status:             string(projection.Status),
		PacketPath:         m.recoveryDisplayPath(loadedPath),
		ReconciliationPath: reconciliationPath,
		FailureID:          packet.FailureID,
		PacketID:           packet.PacketID,
		PartialState:       packet.PartialState,
		ProductLinks:       packet.ProductLinks,
		Blockers:           textutil.Unique(projection.Blockers),
		Gates:              recoveryPacketGates(packet),
		CandidateAtoms:     projection.CandidateAtoms,
		Reconciliation:     reconciliationRef,
		NextAction:         string(projection.NextAction),
		Packet:             &statusPacket,
	}

	return jsonToolResult("recovery_status", "Read recovery decomposition packet status", report)
}

func (m *Manager) recoveryDecompose(ctx context.Context, req ToolRequest) ToolResult {
	record, _, found, err := m.loadRecoveryFailureRecord(req)
	if err != nil {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery decomposition rejected", Error: err.Error()}
	}

	if !found {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery failure record not found", Error: "recovery failure record not found"}
	}

	outputPath, err := m.recoveryDecomposeOutputPath(record.FailureID, req.OutputPath)
	if err != nil {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery decomposition rejected", Error: err.Error()}
	}

	partial := m.recoveryPartialState(ctx, record)

	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{
		Now:          time.Now().UTC(),
		PartialState: partial,
	})
	if err != nil {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery decomposition failed", Error: err.Error()}
	}

	fullPath, relPath, err := recovery.WriteDecompositionPacket(m.cfg.Root, packet, outputPath)
	if err != nil {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery decomposition failed", Error: err.Error()}
	}

	hash, err := fileSHA256(fullPath)
	if err != nil {
		return ToolResult{Tool: "recovery_decompose", Summary: "Recovery decomposition failed", Error: err.Error()}
	}

	report := recoveryDecomposeToolReport{
		SchemaVersion:  recoveryToolReportVersion,
		Status:         string(recovery.StatusForPacket(packet)),
		FailureID:      packet.FailureID,
		PacketID:       packet.PacketID,
		PacketPath:     relPath,
		PacketSHA256:   hash,
		CandidateAtoms: packet.CandidateAtoms,
		NextAction:     string(packet.NextAction),
		Packet:         &packet,
	}

	return jsonToolResult("recovery_decompose", "Wrote recovery decomposition packet", report)
}

func (m *Manager) loadRecoveryFailureRecord(req ToolRequest) (recoverycontract.FailureRecord, string, bool, error) {
	failureID := strings.TrimSpace(req.FailureID)
	sessionID := strings.TrimSpace(req.SessionID)

	if failureID == "" && sessionID == "" {
		if isRecoveryFailureID(strings.TrimSpace(req.ID)) {
			failureID = strings.TrimSpace(req.ID)
		} else {
			sessionID = strings.TrimSpace(req.ID)
		}
	}

	if failureID != "" {
		return recovery.TryLoadFailureRecord(m.cfg.Root, failureID)
	}

	if sessionID == "" {
		return recoverycontract.FailureRecord{}, "", false, fmt.Errorf("session_id or failure_id is required")
	}

	return recovery.TryLoadFailureRecordForSession(m.cfg.Root, sessionID)
}

func (m *Manager) recoveryPacketPathForRequest(req ToolRequest) (string, error) {
	packetPath := strings.TrimSpace(req.PacketPath)
	if packetPath == "" {
		packetPath = strings.TrimSpace(req.Path)
	}

	if packetPath != "" {
		return packetPath, nil
	}

	failureID := strings.TrimSpace(req.FailureID)
	if failureID == "" {
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(req.ID)
		}

		if sessionID == "" {
			return "", fmt.Errorf("packet_path, session_id, or failure_id is required")
		}

		candidates := recovery.FailureIDCandidatesForSession(sessionID)
		for _, candidate := range candidates {
			fullPath, _, err := recovery.DecompositionPacketPath(m.cfg.Root, candidate, "")
			if err != nil {
				return "", err
			}

			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}

		if len(candidates) > 0 {
			failureID = candidates[0]
		} else {
			failureID = recovery.PlanExecuteFailureID(sessionID)
		}
	}

	fullPath, _, err := recovery.DecompositionPacketPath(m.cfg.Root, failureID, "")

	return fullPath, err
}

func isRecoveryFailureID(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "plan-execute-") || strings.HasPrefix(value, "implement-")
}

func (m *Manager) recoveryDecomposeOutputPath(failureID string, outputPath string) (string, error) {
	_, rel, err := recovery.SafeDecompositionOutputPath(m.cfg.Root, failureID, outputPath)
	if err != nil {
		return "", err
	}

	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == ".idiom/recovery" || strings.HasPrefix(rel, ".idiom/recovery/") {
		return rel, nil
	}

	return "", fmt.Errorf("recovery_decompose output_path must stay under .idiom/recovery")
}

func (m *Manager) recoveryPartialState(ctx context.Context, record recoverycontract.FailureRecord) recoverycontract.PartialState {
	dirtyPaths, err := gitstate.DirtyPaths(ctx, m.cfg.Root, gitstate.DirtyPathOptions{
		IgnorePrefixes: []string{".idiom/recovery/"},
		Sort:           true,
	})
	if err != nil {
		return record.PartialState
	}

	return recovery.PartialStateForDirtyPaths(
		dirtyPaths,
		record.PartialState.ChangedPaths,
		record.SourceRun.RequiredEditPaths,
		record.SourceRun.AllowedEditPaths,
		record.PartialState.TotalEdits,
	)
}

func (m *Manager) recoveryReconciliationForStatus(packetPath string, packet recoverycontract.DecompositionPacket) (recoverycontract.Reconciliation, string, bool, []string) {
	if siblingPath, siblingRel, ok := recovery.SafeSiblingReconciliationPath(m.cfg.Root, packetPath); ok {
		reconciliation, _, found, err := recovery.TryLoadReconciliation(siblingPath)
		if err != nil {
			return recoverycontract.Reconciliation{}, siblingRel, false, []string{"invalid_recovery_reconciliation: recovery reconciliation could not be decoded"}
		}

		if found {
			return reconciliation, siblingRel, true, recoveryReconciliationValidationBlockers(packet, reconciliation)
		}
	}

	fullPath, relPath, err := recovery.SafeDefaultReconciliationPath(m.cfg.Root, packet.FailureID)
	if err != nil {
		return recoverycontract.Reconciliation{}, "", false, nil
	}

	reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
	if err != nil {
		return recoverycontract.Reconciliation{}, relPath, false, []string{"invalid_recovery_reconciliation: recovery reconciliation could not be decoded"}
	}

	if !found {
		return recoverycontract.Reconciliation{}, "", false, nil
	}

	return reconciliation, relPath, true, recoveryReconciliationValidationBlockers(packet, reconciliation)
}

func recoveryReconciliationValidationBlockers(packet recoverycontract.DecompositionPacket, reconciliation recoverycontract.Reconciliation) []string {
	findings := recovery.ValidateReconciliation(packet, reconciliation)
	if len(findings) == 0 {
		return nil
	}

	blockers := make([]string, 0, len(findings))
	for _, finding := range findings {
		blockers = append(blockers, finding.Code+": "+finding.Message)
	}

	return blockers
}

func recoveryPacketGates(packet recoverycontract.DecompositionPacket) []string {
	seen := map[string]bool{}

	var gates []string

	for _, atom := range packet.CandidateAtoms {
		for _, gate := range atom.Gates {
			gate = strings.TrimSpace(gate)
			if gate == "" || seen[gate] {
				continue
			}

			seen[gate] = true
			gates = append(gates, gate)
		}
	}

	sort.Strings(gates)

	return gates
}

func sanitizeRecoveryStatusPacket(packet recoverycontract.DecompositionPacket) recoverycontract.DecompositionPacket {
	for i := range packet.ResidualWork {
		packet.ResidualWork[i].EvidenceRefs = sanitizeRecoveryEvidenceRefs(packet.ResidualWork[i].EvidenceRefs)
	}

	return packet
}

func sanitizeRecoveryEvidenceRefs(refs []recoverycontract.EvidenceRef) []recoverycontract.EvidenceRef {
	if len(refs) == 0 {
		return nil
	}

	out := make([]recoverycontract.EvidenceRef, 0, len(refs))
	seen := map[recoverycontract.EvidenceRef]bool{}

	for _, ref := range refs {
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.Path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(ref.Path)))

		if ref.Path == "." {
			ref.Path = ""
		}

		ref.ID = strings.TrimSpace(ref.ID)
		ref.Summary = ""

		if ref.Kind == "" || seen[ref] {
			continue
		}

		seen[ref] = true
		out = append(out, ref)
	}

	return out
}

func (m *Manager) recoveryDisplayPath(path string) string {
	if rel, err := filepath.Rel(m.cfg.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}

	return filepath.ToSlash(path)
}

func fileSHA256(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read recovery artifact for hash: %w", err)
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:]), nil
}

func jsonToolResult(tool string, summary string, report any) ToolResult {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: tool, Summary: summary + " failed", Error: fmt.Sprintf("marshal recovery report: %v", err)}
	}

	return ToolResult{Tool: tool, Summary: summary, Output: string(body)}
}
