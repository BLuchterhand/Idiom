package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/workflows/recovery"
)

const recoveryToolReportVersion = "idiom.recovery.tool_report.v1"

type recoveryInspectToolReport struct {
	SchemaVersion string                  `json:"schema_version"`
	Status        string                  `json:"status"`
	FailureID     string                  `json:"failure_id,omitempty"`
	SessionID     string                  `json:"session_id,omitempty"`
	RecordPath    string                  `json:"record_path,omitempty"`
	FailureClass  string                  `json:"failure_class,omitempty"`
	FailureKind   string                  `json:"failure_kind,omitempty"`
	PartialState  recovery.PartialState   `json:"partial_state"`
	NextAction    string                  `json:"next_valid_action"`
	Record        *recovery.FailureRecord `json:"record,omitempty"`
	Blockers      []string                `json:"blockers,omitempty"`
}

type recoveryStatusToolReport struct {
	SchemaVersion      string                        `json:"schema_version"`
	Status             string                        `json:"status"`
	PacketPath         string                        `json:"packet_path,omitempty"`
	ReconciliationPath string                        `json:"reconciliation_path,omitempty"`
	FailureID          string                        `json:"failure_id,omitempty"`
	PacketID           string                        `json:"packet_id,omitempty"`
	PartialState       recovery.PartialState         `json:"partial_state"`
	ProductLinks       recovery.ProductLinks         `json:"product_links,omitempty"`
	Blockers           []string                      `json:"blockers,omitempty"`
	Gates              []string                      `json:"gates,omitempty"`
	CandidateAtoms     []recovery.CandidateAtom      `json:"candidate_atoms,omitempty"`
	Reconciliation     *recovery.Reconciliation      `json:"reconciliation,omitempty"`
	NextAction         string                        `json:"next_valid_action"`
	Packet             *recovery.DecompositionPacket `json:"packet,omitempty"`
}

type recoveryDecomposeToolReport struct {
	SchemaVersion  string                        `json:"schema_version"`
	Status         string                        `json:"status"`
	FailureID      string                        `json:"failure_id,omitempty"`
	PacketID       string                        `json:"packet_id,omitempty"`
	PacketPath     string                        `json:"packet_path,omitempty"`
	PacketSHA256   string                        `json:"packet_sha256,omitempty"`
	CandidateAtoms []recovery.CandidateAtom      `json:"candidate_atoms,omitempty"`
	NextAction     string                        `json:"next_valid_action"`
	Packet         *recovery.DecompositionPacket `json:"packet,omitempty"`
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
		FailureClass:  record.Failure.Class,
		FailureKind:   record.Failure.Kind,
		PartialState:  partial,
		NextAction:    record.NextAction,
		Record:        &record,
		Blockers:      recoveryPartialStateBlockers(partial),
	}
	return jsonToolResult("recovery_inspect", "Read recovery failure record", report)
}

func (m *Manager) recoveryStatus(ctx context.Context, req ToolRequest) ToolResult {
	packetPath, err := m.recoveryPacketPathForRequest(req)
	if err != nil {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery status rejected", Error: err.Error()}
	}
	packet, loadedPath, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery status failed", Error: err.Error()}
	}
	if !found {
		return ToolResult{Tool: "recovery_status", Summary: "Recovery decomposition packet not found", Error: "recovery decomposition packet not found"}
	}
	currentPartial := m.recoveryPartialState(ctx, failureRecordFromRecoveryPacket(packet))
	if currentPartial.DirtyStatus != "" && currentPartial.DirtyStatus != recovery.DirtyStatusClean {
		packet.PartialState = currentPartial
		packet.NextAction = recovery.NextActionForPartialState(failureRecordFromRecoveryPacket(packet), currentPartial)
	}
	status := recoveryPacketStatus(packet)
	nextAction := packet.NextAction
	candidateAtoms := packet.CandidateAtoms
	statusPacket := sanitizeRecoveryStatusPacket(packet)
	blockers := recoveryPacketBlockers(packet)
	var reconciliationRef *recovery.Reconciliation
	reconciliationPath := ""
	if reconciliation, relPath, found, reconciliationBlockers := m.recoveryReconciliationForStatus(loadedPath, packet); len(reconciliationBlockers) > 0 {
		status = "blocked"
		nextAction = recovery.NextActionInspectFailure
		reconciliationPath = relPath
		blockers = append(blockers, reconciliationBlockers...)
	} else if found {
		reconciliationRef = &reconciliation
		reconciliationPath = relPath
		status = recoveryStatusWithReconciliation(packet, reconciliation)
		nextAction = reconciliation.NextAction
		candidateAtoms = recoveryRemainingCandidateAtoms(packet, reconciliation)
		if reconciliation.Status == recovery.ReconciliationStatusRecovered {
			blockers = nil
			statusPacket.ResidualWork = nil
		}
	}
	report := recoveryStatusToolReport{
		SchemaVersion:      recoveryToolReportVersion,
		Status:             status,
		PacketPath:         m.recoveryDisplayPath(loadedPath),
		ReconciliationPath: reconciliationPath,
		FailureID:          packet.FailureID,
		PacketID:           packet.PacketID,
		PartialState:       packet.PartialState,
		ProductLinks:       packet.ProductLinks,
		Blockers:           compactRecoveryStrings(blockers),
		Gates:              recoveryPacketGates(packet),
		CandidateAtoms:     candidateAtoms,
		Reconciliation:     reconciliationRef,
		NextAction:         nextAction,
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
		Status:         recoveryPacketStatus(packet),
		FailureID:      packet.FailureID,
		PacketID:       packet.PacketID,
		PacketPath:     relPath,
		PacketSHA256:   hash,
		CandidateAtoms: packet.CandidateAtoms,
		NextAction:     packet.NextAction,
		Packet:         &packet,
	}
	return jsonToolResult("recovery_decompose", "Wrote recovery decomposition packet", report)
}

func (m *Manager) loadRecoveryFailureRecord(req ToolRequest) (recovery.FailureRecord, string, bool, error) {
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
		return recovery.FailureRecord{}, "", false, fmt.Errorf("session_id or failure_id is required")
	}
	return recovery.TryLoadFailureRecordForSession(m.cfg.Root, sessionID)
}

func (m *Manager) recoveryPacketPathForRequest(req ToolRequest) (string, error) {
	packetPath := strings.TrimSpace(req.PacketPath)
	if packetPath == "" {
		packetPath = strings.TrimSpace(req.Path)
	}
	if packetPath != "" {
		return m.workspaceContainedPath(packetPath)
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
	outputPath = strings.TrimSpace(outputPath)
	fullPath := outputPath
	if outputPath == "" {
		var err error
		fullPath, _, err = recovery.DecompositionPacketPath(m.cfg.Root, failureID, "")
		if err != nil {
			return "", err
		}
	} else {
		var err error
		fullPath, err = m.workspaceContainedPath(outputPath)
		if err != nil {
			return "", err
		}
	}
	fullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve recovery output path: %w", err)
	}
	absRoot, err := filepath.Abs(m.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	rel, err := filepath.Rel(absRoot, fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve recovery output path: %w", err)
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == ".idiom/recovery" || strings.HasPrefix(rel, ".idiom/recovery/") {
		if isWorkspaceRecoveryControlArtifactPath(rel) {
			return "", fmt.Errorf("recovery_decompose output_path would overwrite a recovery control artifact")
		}
		if workspacePathHasSymlinkAncestor(m.cfg.Root, fullPath) {
			return "", fmt.Errorf("recovery_decompose output_path must not traverse symlinked directories")
		}
		return rel, nil
	}
	return "", fmt.Errorf("recovery_decompose output_path must stay under .idiom/recovery")
}

func (m *Manager) workspaceContainedPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(m.cfg.Root, fullPath)
	}
	absRoot, err := filepath.Abs(m.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must stay under workspace root")
	}
	return absPath, nil
}

func workspacePathHasSymlinkAncestor(root string, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return true
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	absRoot = filepath.Clean(absRoot)
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return true
	}
	parentRel := filepath.Dir(rel)
	if parentRel == "." {
		return false
	}
	current := absRoot
	for _, part := range strings.Split(parentRel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return false
		}
		if err != nil {
			return true
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func isWorkspaceRecoveryControlArtifactPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	canonical := strings.ToLower(rel)
	if canonical == "" || canonical == "." {
		return true
	}
	if canonical != ".idiom/recovery" && !strings.HasPrefix(canonical, ".idiom/recovery/") {
		return false
	}
	switch path.Base(canonical) {
	case "failure.json", "reconciliation.json":
		return true
	default:
		return false
	}
}

func (m *Manager) recoveryPartialState(ctx context.Context, record recovery.FailureRecord) recovery.PartialState {
	dirtyPaths, err := gitDirtyPathsForRecovery(ctx, m.cfg.Root)
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

func gitDirtyPathsForRecovery(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseRecoveryGitPorcelainPaths(string(out)), nil
}

func parseRecoveryGitPorcelainPaths(output string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = parts[len(parts)-1]
		}
		path = filepath.ToSlash(strings.Trim(path, `"`))
		if path == "" || strings.HasPrefix(path, ".idiom/recovery/") || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func recoveryPacketStatus(packet recovery.DecompositionPacket) string {
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		return "blocked"
	}
	if len(packet.CandidateAtoms) == 0 {
		return "needs_inspection"
	}
	return "ready"
}

func (m *Manager) recoveryReconciliationForStatus(packetPath string, packet recovery.DecompositionPacket) (recovery.Reconciliation, string, bool, []string) {
	if siblingPath, siblingRel, ok := recoverySiblingReconciliationPath(m.cfg.Root, packetPath); ok {
		reconciliation, _, found, err := recovery.TryLoadReconciliation(siblingPath)
		if err != nil {
			return recovery.Reconciliation{}, siblingRel, false, []string{"invalid_recovery_reconciliation: recovery reconciliation could not be decoded"}
		}
		if found {
			return reconciliation, siblingRel, true, recoveryReconciliationValidationBlockers(packet, reconciliation)
		}
	}
	fullPath, relPath, err := recovery.ReconciliationPath(m.cfg.Root, packet.FailureID, "")
	if err != nil {
		return recovery.Reconciliation{}, "", false, nil
	}
	reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
	if err != nil {
		return recovery.Reconciliation{}, relPath, false, []string{"invalid_recovery_reconciliation: recovery reconciliation could not be decoded"}
	}
	if !found {
		return recovery.Reconciliation{}, "", false, nil
	}
	return reconciliation, relPath, true, recoveryReconciliationValidationBlockers(packet, reconciliation)
}

func recoverySiblingReconciliationPath(root string, packetPath string) (string, string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", false
	}
	absPacketPath, err := filepath.Abs(packetPath)
	if err != nil {
		return "", "", false
	}
	relPacketPath, err := filepath.Rel(absRoot, absPacketPath)
	if err != nil || relPacketPath == "." || relPacketPath == ".." || strings.HasPrefix(relPacketPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPacketPath) {
		return "", "", false
	}
	fullPath := filepath.Join(filepath.Dir(absPacketPath), "reconciliation.json")
	relPath, err := filepath.Rel(absRoot, fullPath)
	if err != nil {
		return "", "", false
	}
	return fullPath, filepath.ToSlash(filepath.Clean(relPath)), true
}

func recoveryReconciliationValidationBlockers(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) []string {
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

func recoveryStatusWithReconciliation(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) string {
	switch reconciliation.Status {
	case recovery.ReconciliationStatusRecovered:
		return "recovered"
	case recovery.ReconciliationStatusPartial:
		return "partial"
	default:
		return recoveryPacketStatus(packet)
	}
}

func recoveryRemainingCandidateAtoms(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) []recovery.CandidateAtom {
	completed := map[string]bool{}
	for _, atom := range reconciliation.CompletedAtoms {
		completed[atom.ID] = true
	}
	remaining := make([]recovery.CandidateAtom, 0, len(packet.CandidateAtoms))
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			remaining = append(remaining, atom)
		}
	}
	return remaining
}

func recoveryPacketBlockers(packet recovery.DecompositionPacket) []string {
	var blockers []string
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		blockers = append(blockers, "packet next action is "+packet.NextAction)
	}
	if len(packet.CandidateAtoms) == 0 {
		blockers = append(blockers, "packet has no candidate recovery atoms")
	}
	blockers = append(blockers, recoveryPartialStateBlockers(packet.PartialState)...)
	return compactRecoveryStrings(blockers)
}

func recoveryPartialStateBlockers(partial recovery.PartialState) []string {
	var blockers []string
	if partial.DirtyStatus == recovery.DirtyStatusDirtyOutScope {
		blockers = append(blockers, "workspace has out-of-scope dirty state")
	}
	if partial.ScopeStatus == recovery.ScopeStatusOutOfScope {
		blockers = append(blockers, "workspace scope status is out_of_scope")
	}
	return blockers
}

func recoveryPacketGates(packet recovery.DecompositionPacket) []string {
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

func failureRecordFromRecoveryPacket(packet recovery.DecompositionPacket) recovery.FailureRecord {
	return recovery.FailureRecord{
		SchemaVersion: recovery.FailureRecordSchemaVersion,
		FailureID:     packet.FailureID,
		CreatedAt:     packet.CreatedAt,
		UpdatedAt:     packet.UpdatedAt,
		SourceRun:     packet.SourceRun,
		Failure:       packet.Failure,
		PartialState:  packet.PartialState,
		NextAction:    packet.NextAction,
	}
}

func sanitizeRecoveryStatusPacket(packet recovery.DecompositionPacket) recovery.DecompositionPacket {
	for i := range packet.ResidualWork {
		packet.ResidualWork[i].EvidenceRefs = sanitizeRecoveryEvidenceRefs(packet.ResidualWork[i].EvidenceRefs)
	}
	return packet
}

func sanitizeRecoveryEvidenceRefs(refs []recovery.EvidenceRef) []recovery.EvidenceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]recovery.EvidenceRef, 0, len(refs))
	seen := map[recovery.EvidenceRef]bool{}
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

func compactRecoveryStrings(values []string) []string {
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
