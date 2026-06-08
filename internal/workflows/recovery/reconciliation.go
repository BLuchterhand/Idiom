package recovery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ReconciliationSchemaVersion = "idiom.recovery.reconciliation.v1"

const (
	ReconciliationStatusPartial   = "partial"
	ReconciliationStatusRecovered = "recovered"
)

const (
	ReconciliationVerificationPass        = "pass"
	ReconciliationVerificationFail        = "fail"
	ReconciliationVerificationNotRecorded = "not_recorded"
)

type ReconciliationOptions struct {
	Now              time.Time
	CompletedAtomIDs []string
	Verification     ReconciliationVerification
	AuthorityDeltas  []ScopeAuthorityDelta
}

type Reconciliation struct {
	SchemaVersion   string                     `json:"schema_version"`
	FailureID       string                     `json:"failure_id"`
	PacketID        string                     `json:"packet_id"`
	PacketDigest    string                     `json:"packet_digest"`
	CreatedAt       string                     `json:"created_at"`
	UpdatedAt       string                     `json:"updated_at"`
	LockedContract  *ContractSnapshot          `json:"locked_contract,omitempty"`
	AuthorityDeltas []ScopeAuthorityDelta      `json:"authority_deltas,omitempty"`
	Status          string                     `json:"status"`
	CompletedAtoms  []ReconciledAtom           `json:"completed_atoms,omitempty"`
	RemainingAtoms  []string                   `json:"remaining_atoms,omitempty"`
	Blockers        []string                   `json:"blockers,omitempty"`
	Verification    ReconciliationVerification `json:"verification"`
	NextAction      string                     `json:"next_valid_action"`
}

type ReconciledAtom struct {
	ID              string   `json:"id"`
	Title           string   `json:"title,omitempty"`
	ResidualWorkIDs []string `json:"residual_work_ids,omitempty"`
	RequiredPaths   []string `json:"required_paths,omitempty"`
	RequiredTests   []string `json:"required_tests,omitempty"`
	InvariantIDs    []string `json:"invariant_ids,omitempty"`
}

type ScopeAuthorityDelta struct {
	DeltaID           string   `json:"delta_id"`
	ContractID        string   `json:"contract_id,omitempty"`
	ContractHash      string   `json:"contract_hash,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	RequiredEditPaths []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths  []string `json:"allowed_edit_paths,omitempty"`
}

type ReconciliationVerification struct {
	Status        string                        `json:"status"`
	RequiredTests []string                      `json:"required_tests,omitempty"`
	Commands      []ReconciliationCommandResult `json:"commands,omitempty"`
}

type ReconciliationCommandResult struct {
	Command     string `json:"command"`
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type ReconciliationValidationFinding struct {
	Code    string
	Message string
}

func BuildReconciliation(packet DecompositionPacket, opts ReconciliationOptions) (Reconciliation, error) {
	if !validRecoveryComponent(packet.FailureID) {
		return Reconciliation{}, fmt.Errorf("invalid recovery failure id %q", packet.FailureID)
	}
	if strings.TrimSpace(packet.PacketID) == "" {
		return Reconciliation{}, fmt.Errorf("recovery packet id is empty")
	}
	completedIDs := cleanRecoveryAtomIDs(opts.CompletedAtomIDs)
	if len(completedIDs) == 0 {
		return Reconciliation{}, fmt.Errorf("at least one completed recovery atom is required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	remaining := make([]string, 0, len(packet.CandidateAtoms))
	completedSet := map[string]bool{}
	for _, id := range completedIDs {
		completedSet[id] = true
	}
	reconciliation := Reconciliation{
		SchemaVersion:   ReconciliationSchemaVersion,
		FailureID:       packet.FailureID,
		PacketID:        packet.PacketID,
		PacketDigest:    PacketDigest(packet),
		CreatedAt:       stamp,
		UpdatedAt:       stamp,
		LockedContract:  cleanContractSnapshot(packet.LockedContract, packet.SourceRun),
		AuthorityDeltas: cleanScopeAuthorityDeltas(opts.AuthorityDeltas),
		Status:          ReconciliationStatusRecovered,
		NextAction:      NextActionNone,
	}
	for _, atom := range packet.CandidateAtoms {
		if completedSet[atom.ID] {
			reconciliation.CompletedAtoms = append(reconciliation.CompletedAtoms, ReconciledAtom{
				ID:              atom.ID,
				Title:           atom.Title,
				ResidualWorkIDs: append([]string{}, atom.ResidualWorkIDs...),
				RequiredPaths:   append([]string{}, atom.RequiredPaths...),
				RequiredTests:   append([]string{}, atom.RequiredTests...),
				InvariantIDs:    append([]string{}, atom.InvariantIDs...),
			})
			delete(completedSet, atom.ID)
			continue
		}
		remaining = append(remaining, atom.ID)
	}
	if len(completedSet) > 0 {
		unknown := make([]string, 0, len(completedSet))
		for id := range completedSet {
			unknown = append(unknown, id)
		}
		sort.Strings(unknown)
		return Reconciliation{}, fmt.Errorf("completed recovery atom(s) not found in packet: %s", strings.Join(unknown, ", "))
	}
	if len(remaining) > 0 {
		reconciliation.Status = ReconciliationStatusPartial
		reconciliation.NextAction = NextActionReviewDecomposition
		reconciliation.RemainingAtoms = remaining
	}
	reconciliation.Verification = normalizeReconciliationVerification(opts.Verification, requiredTestsForReconciledAtoms(reconciliation.CompletedAtoms))
	return reconciliation, nil
}

func ValidateReconciliation(packet DecompositionPacket, reconciliation Reconciliation) []ReconciliationValidationFinding {
	var findings []ReconciliationValidationFinding
	add := func(code string, format string, args ...any) {
		findings = append(findings, ReconciliationValidationFinding{
			Code:    code,
			Message: fmt.Sprintf(format, args...),
		})
	}
	if reconciliation.SchemaVersion != ReconciliationSchemaVersion {
		add("recovery_reconciliation_schema_mismatch", "unsupported recovery reconciliation schema %q", reconciliation.SchemaVersion)
	}
	if reconciliation.FailureID != packet.FailureID {
		add("recovery_reconciliation_failure_mismatch", "recovery reconciliation failure_id does not match packet failure_id")
	}
	if reconciliation.PacketID != packet.PacketID {
		add("recovery_reconciliation_packet_mismatch", "recovery reconciliation packet_id does not match packet packet_id")
	}
	if reconciliation.PacketDigest == "" {
		add("recovery_reconciliation_missing_packet_digest", "recovery reconciliation is missing packet_digest")
	} else if want := PacketDigest(packet); reconciliation.PacketDigest != want {
		add("recovery_reconciliation_packet_digest_mismatch", "recovery reconciliation packet_digest does not match the current packet")
	}
	if !contractSnapshotsEqual(reconciliation.LockedContract, packet.LockedContract) && !authorityDeltasAuthorizeContractChange(packet.LockedContract, reconciliation.LockedContract, reconciliation.AuthorityDeltas) {
		add("recovery_reconciliation_contract_mismatch", "recovery reconciliation locked contract does not match packet locked contract")
	}
	switch reconciliation.Status {
	case ReconciliationStatusPartial:
		if reconciliation.NextAction != NextActionReviewDecomposition {
			add("recovery_reconciliation_invalid_next_action", "partial recovery reconciliation must use next_valid_action %q", NextActionReviewDecomposition)
		}
	case ReconciliationStatusRecovered:
		if reconciliation.NextAction != NextActionNone {
			add("recovery_reconciliation_invalid_next_action", "recovered reconciliation must use next_valid_action %q", NextActionNone)
		}
		if len(reconciliation.Blockers) > 0 {
			add("recovery_reconciliation_recovered_with_blockers", "recovered reconciliation must not carry blockers")
		}
	default:
		add("recovery_reconciliation_invalid_status", "unsupported recovery reconciliation status %q", reconciliation.Status)
	}
	if len(reconciliation.CompletedAtoms) == 0 {
		add("recovery_reconciliation_missing_completed_atoms", "recovery reconciliation must name at least one completed atom")
	}
	packetAtoms := map[string]CandidateAtom{}
	for _, atom := range packet.CandidateAtoms {
		packetAtoms[atom.ID] = atom
	}
	completed := map[string]bool{}
	for _, atom := range reconciliation.CompletedAtoms {
		if strings.TrimSpace(atom.ID) == "" {
			add("recovery_reconciliation_empty_completed_atom", "recovery reconciliation includes an empty completed atom id")
			continue
		}
		if completed[atom.ID] {
			add("recovery_reconciliation_duplicate_completed_atom", "recovery reconciliation repeats completed atom %q", atom.ID)
			continue
		}
		completed[atom.ID] = true
		packetAtom, ok := packetAtoms[atom.ID]
		if !ok {
			add("recovery_reconciliation_unknown_completed_atom", "completed atom %q is not present in the packet", atom.ID)
			continue
		}
		if !reconciledAtomMatchesPacket(atom, packetAtom, packet.LockedContract, reconciliation.AuthorityDeltas) {
			add("recovery_reconciliation_completed_atom_mismatch", "completed atom %q does not match the packet candidate atom", atom.ID)
		}
	}
	wantRemaining := make([]string, 0, len(packet.CandidateAtoms))
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			wantRemaining = append(wantRemaining, atom.ID)
		}
	}
	gotRemaining := cleanRecoveryAtomIDs(reconciliation.RemainingAtoms)
	if reconciliation.Status == ReconciliationStatusRecovered {
		if len(wantRemaining) != 0 || len(gotRemaining) != 0 {
			add("recovery_reconciliation_incomplete_recovered", "recovered reconciliation must complete every packet atom")
		}
	} else if !reconciliationStringSlicesEqual(gotRemaining, wantRemaining) {
		add("recovery_reconciliation_remaining_mismatch", "partial recovery reconciliation remaining atoms do not match packet candidates")
	}
	requiredTests := requiredTestsForReconciledAtoms(reconciliation.CompletedAtoms)
	if len(requiredTests) > 0 {
		if reconciliation.Verification.Status != ReconciliationVerificationPass {
			add("recovery_reconciliation_missing_verification", "recovery reconciliation must record passing verification for completed atoms")
		}
		if missing := missingVerifiedRecoveryCommands(requiredTests, reconciliation.Verification.Commands); len(missing) > 0 {
			add("recovery_reconciliation_missing_required_tests", "recovery reconciliation is missing passing required test(s): %s", strings.Join(missing, ", "))
		}
	}
	return findings
}

func PacketDigest(packet DecompositionPacket) string {
	input := struct {
		SchemaVersion  string            `json:"schema_version"`
		PacketID       string            `json:"packet_id"`
		FailureID      string            `json:"failure_id"`
		SourceRun      SourceRun         `json:"source_run"`
		Failure        Failure           `json:"failure"`
		LockedContract *ContractSnapshot `json:"locked_contract,omitempty"`
		PartialState   PartialState      `json:"partial_state"`
		ProductLinks   ProductLinks      `json:"product_links,omitempty"`
		ResidualWork   []ResidualWork    `json:"residual_work,omitempty"`
		CandidateAtoms []CandidateAtom   `json:"candidate_atoms,omitempty"`
		EvidenceRefs   []EvidenceRef     `json:"evidence_refs,omitempty"`
		NextAction     string            `json:"next_valid_action"`
	}{
		SchemaVersion:  packet.SchemaVersion,
		PacketID:       packet.PacketID,
		FailureID:      packet.FailureID,
		SourceRun:      packet.SourceRun,
		Failure:        packet.Failure,
		LockedContract: packet.LockedContract,
		PartialState:   packet.PartialState,
		ProductLinks:   packet.ProductLinks,
		ResidualWork:   packet.ResidualWork,
		CandidateAtoms: packet.CandidateAtoms,
		EvidenceRefs:   packet.EvidenceRefs,
		NextAction:     packet.NextAction,
	}
	body, _ := json.Marshal(input)
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func ReconciliationCompletedAtomIDs(reconciliation Reconciliation) []string {
	ids := make([]string, 0, len(reconciliation.CompletedAtoms))
	for _, atom := range reconciliation.CompletedAtoms {
		ids = append(ids, atom.ID)
	}
	return cleanRecoveryAtomIDs(ids)
}

func normalizeReconciliationVerification(verification ReconciliationVerification, requiredTests []string) ReconciliationVerification {
	verification.Status = strings.TrimSpace(verification.Status)
	if verification.Status == "" {
		verification.Status = ReconciliationVerificationNotRecorded
	}
	verification.RequiredTests = cleanRecoveryCommandList(append(verification.RequiredTests, requiredTests...))
	for i := range verification.Commands {
		verification.Commands[i].Command = strings.Join(strings.Fields(verification.Commands[i].Command), " ")
		verification.Commands[i].Status = strings.TrimSpace(verification.Commands[i].Status)
	}
	if len(requiredTests) > 0 && len(missingVerifiedRecoveryCommands(requiredTests, verification.Commands)) > 0 && verification.Status == ReconciliationVerificationPass {
		verification.Status = ReconciliationVerificationFail
	}
	return verification
}

func requiredTestsForReconciledAtoms(atoms []ReconciledAtom) []string {
	var tests []string
	for _, atom := range atoms {
		tests = append(tests, atom.RequiredTests...)
	}
	return cleanRecoveryCommandList(tests)
}

func missingVerifiedRecoveryCommands(requiredTests []string, commands []ReconciliationCommandResult) []string {
	passed := map[string]bool{}
	for _, command := range commands {
		if command.Status == ReconciliationVerificationPass && command.ExitCode == 0 {
			passed[strings.Join(strings.Fields(command.Command), " ")] = true
		}
	}
	var missing []string
	for _, test := range cleanRecoveryCommandList(requiredTests) {
		if !passed[test] {
			missing = append(missing, test)
		}
	}
	return missing
}

func WriteReconciliation(root string, reconciliation Reconciliation, outputPath string) (string, string, error) {
	fullPath, relPath, err := ReconciliationPath(root, reconciliation.FailureID, outputPath)
	if err != nil {
		return "", "", err
	}
	if err := ensureRecoveryWritePath(root, fullPath); err != nil {
		return "", "", fmt.Errorf("recovery reconciliation path rejected: %w", err)
	}
	if existing, _, found, err := TryLoadReconciliation(fullPath); err == nil && found && strings.TrimSpace(existing.CreatedAt) != "" {
		reconciliation.CreatedAt = existing.CreatedAt
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir recovery reconciliation dir: %w", err)
	}
	body, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal recovery reconciliation: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := writeRecoveryTempFileExclusive(tmpPath, append(body, '\n')); err != nil {
		return "", "", fmt.Errorf("write recovery reconciliation: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("replace recovery reconciliation: %w", err)
	}
	return fullPath, relPath, nil
}

func TryLoadReconciliation(path string) (Reconciliation, string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Reconciliation{}, "", false, fmt.Errorf("recovery reconciliation path is empty")
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Reconciliation{}, filepath.ToSlash(path), false, nil
	}
	if err != nil {
		return Reconciliation{}, filepath.ToSlash(path), false, fmt.Errorf("read recovery reconciliation %s: %w", path, err)
	}
	var reconciliation Reconciliation
	if err := json.Unmarshal(body, &reconciliation); err != nil {
		return Reconciliation{}, filepath.ToSlash(path), false, fmt.Errorf("decode recovery reconciliation %s: %w", path, err)
	}
	if reconciliation.SchemaVersion != ReconciliationSchemaVersion {
		return Reconciliation{}, filepath.ToSlash(path), false, fmt.Errorf("unsupported recovery reconciliation schema %q in %s", reconciliation.SchemaVersion, path)
	}
	return reconciliation, filepath.ToSlash(path), true, nil
}

func TryLoadReconciliationForPacket(root string, packet DecompositionPacket) (Reconciliation, string, bool, error) {
	fullPath, relPath, err := ReconciliationPath(root, packet.FailureID, "")
	if err != nil {
		return Reconciliation{}, "", false, err
	}
	reconciliation, _, found, err := TryLoadReconciliation(fullPath)
	if err != nil || !found {
		return reconciliation, relPath, found, err
	}
	return reconciliation, relPath, true, nil
}

func ReconciliationPath(root string, failureID string, outputPath string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	if !validRecoveryComponent(failureID) {
		return "", "", fmt.Errorf("invalid recovery failure id %q", failureID)
	}
	if strings.TrimSpace(outputPath) == "" {
		relPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "reconciliation.json"))
		return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
	}
	fullPath := outputPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, fullPath)
	}
	relPath, err := filepath.Rel(root, fullPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		relPath = fullPath
	}
	return fullPath, filepath.ToSlash(relPath), nil
}

func cleanRecoveryAtomIDs(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func reconciledAtomMatchesPacket(atom ReconciledAtom, packetAtom CandidateAtom, lockedContract *ContractSnapshot, deltas []ScopeAuthorityDelta) bool {
	scopeMatches := reconciliationStringSlicesEqual(cleanPathList(atom.RequiredPaths), cleanPathList(packetAtom.RequiredPaths))
	if !scopeMatches && scopeWideningAuthorized(packetAtom.RequiredPaths, atom.RequiredPaths, lockedContract, deltas) {
		scopeMatches = true
	}
	return atom.Title == packetAtom.Title &&
		reconciliationStringSlicesEqual(cleanRecoveryAtomIDs(atom.ResidualWorkIDs), cleanRecoveryAtomIDs(packetAtom.ResidualWorkIDs)) &&
		scopeMatches &&
		reconciliationStringSlicesEqual(cleanRecoveryCommandList(atom.RequiredTests), cleanRecoveryCommandList(packetAtom.RequiredTests)) &&
		reconciliationStringSlicesEqual(cleanRecoveryCommandList(atom.InvariantIDs), cleanRecoveryCommandList(packetAtom.InvariantIDs))
}

func cleanScopeAuthorityDeltas(deltas []ScopeAuthorityDelta) []ScopeAuthorityDelta {
	seen := map[string]bool{}
	var out []ScopeAuthorityDelta
	for _, delta := range deltas {
		clean := ScopeAuthorityDelta{
			DeltaID:           strings.TrimSpace(delta.DeltaID),
			ContractID:        strings.TrimSpace(delta.ContractID),
			ContractHash:      strings.TrimSpace(delta.ContractHash),
			Reason:            sanitizeAndTruncate(delta.Reason, 500),
			RequiredEditPaths: cleanPathList(delta.RequiredEditPaths),
			AllowedEditPaths:  cleanPathList(delta.AllowedEditPaths),
		}
		if clean.DeltaID == "" {
			continue
		}
		key := strings.Join([]string{clean.DeltaID, clean.ContractID, clean.ContractHash, strings.Join(clean.RequiredEditPaths, "\x00"), strings.Join(clean.AllowedEditPaths, "\x00")}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, clean)
	}
	return out
}

func authorityDeltasAuthorizeContractChange(before *ContractSnapshot, after *ContractSnapshot, deltas []ScopeAuthorityDelta) bool {
	if after == nil || len(deltas) == 0 {
		return false
	}
	var added []string
	if before == nil {
		added = cleanPathList(append(after.RequiredEditPaths, after.AllowedEditPaths...))
	} else {
		added = append(added, pathsAddedBeyond(before.RequiredEditPaths, after.RequiredEditPaths)...)
		added = append(added, pathsAddedBeyond(append(before.AllowedEditPaths, before.RequiredEditPaths...), append(after.AllowedEditPaths, after.RequiredEditPaths...))...)
		added = cleanPathList(added)
	}
	if len(added) == 0 {
		return false
	}
	for _, delta := range cleanScopeAuthorityDeltas(deltas) {
		if !authorityDeltaMatchesContract(delta, before) {
			continue
		}
		if pathsCoveredByAuthorityDelta(added, delta) {
			return true
		}
	}
	return false
}

func scopeWideningAuthorized(original []string, updated []string, lockedContract *ContractSnapshot, deltas []ScopeAuthorityDelta) bool {
	if reconciliationStringSlicesEqual(cleanPathList(original), cleanPathList(updated)) {
		return true
	}
	extra := pathsAddedBeyond(original, updated)
	if len(extra) == 0 {
		return false
	}
	for _, delta := range cleanScopeAuthorityDeltas(deltas) {
		if authorityDeltaMatchesContract(delta, lockedContract) && pathsCoveredByAuthorityDelta(extra, delta) {
			return true
		}
	}
	return false
}

func authorityDeltaMatchesContract(delta ScopeAuthorityDelta, snapshot *ContractSnapshot) bool {
	if snapshot == nil {
		return true
	}
	if delta.ContractID != "" && snapshot.ContractID != "" && delta.ContractID != snapshot.ContractID {
		return false
	}
	if delta.ContractHash != "" && snapshot.ContractHash != "" && delta.ContractHash != snapshot.ContractHash {
		return false
	}
	return delta.ContractID != "" || delta.ContractHash != ""
}

func pathsCoveredByAuthorityDelta(paths []string, delta ScopeAuthorityDelta) bool {
	authorized := pathSet(append(delta.RequiredEditPaths, delta.AllowedEditPaths...))
	for _, path := range cleanPathList(paths) {
		if !authorized[path] {
			return false
		}
	}
	return true
}

func pathsAddedBeyond(original []string, updated []string) []string {
	originalSet := pathSet(original)
	var extra []string
	for _, path := range cleanPathList(updated) {
		if !originalSet[path] {
			extra = append(extra, path)
		}
	}
	return extra
}

func cleanRecoveryCommandList(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func reconciliationStringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
