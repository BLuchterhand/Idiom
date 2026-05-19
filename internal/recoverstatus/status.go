package recoverstatus

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/recovery"
)

const ReportType = "idiom.recover.status.v1"

type Report struct {
	Type               string                   `json:"type"`
	Status             string                   `json:"status"`
	PacketPath         string                   `json:"packet_path,omitempty"`
	ReconciliationPath string                   `json:"reconciliation_path,omitempty"`
	FailureID          string                   `json:"failure_id,omitempty"`
	PacketID           string                   `json:"packet_id,omitempty"`
	PartialState       recovery.PartialState    `json:"partial_state"`
	ProductLinks       recovery.ProductLinks    `json:"product_links,omitempty"`
	Blockers           []string                 `json:"blockers,omitempty"`
	ResidualWork       []recovery.ResidualWork  `json:"residual_work,omitempty"`
	CandidateAtoms     []recovery.CandidateAtom `json:"candidate_atoms,omitempty"`
	Reconciliation     *recovery.Reconciliation `json:"reconciliation,omitempty"`
	NextAction         string                   `json:"next_valid_action"`
	Error              string                   `json:"error,omitempty"`
}

func Build(root string, packetPath string) (Report, error) {
	packetPath, err := resolvePacketPath(root, packetPath)
	if err != nil {
		return Report{}, err
	}
	packet, relPath, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil {
		return Report{}, err
	}
	if !found {
		return Report{}, fmt.Errorf("recovery decomposition packet not found: %s", packetPath)
	}
	reconciliation, reconciliationPath, reconciliationFound, err := tryLoadReconciliation(root, packetPath, packet)
	if err != nil {
		return Report{}, err
	}
	status := PacketStatus(packet)
	nextAction := packet.NextAction
	residualWork := sanitizeResidualWork(packet.ResidualWork)
	candidateAtoms := packet.CandidateAtoms
	var reconciliationRef *recovery.Reconciliation
	var reconciliationFindings []recovery.ReconciliationValidationFinding
	reconciliationDisplayPath := ""
	if reconciliationFound {
		reconciliationRef = &reconciliation
		reconciliationDisplayPath = relDisplayPath(root, reconciliationPath)
		reconciliationFindings = recovery.ValidateReconciliation(packet, reconciliation)
		if len(reconciliationFindings) == 0 {
			status = packetStatusWithReconciliation(packet, reconciliation)
			nextAction = reconciliation.NextAction
			candidateAtoms = remainingCandidateAtoms(packet, reconciliation)
			residualWork = sanitizeResidualWork(remainingResidualWork(packet, candidateAtoms))
		} else {
			status = "blocked"
			nextAction = recovery.NextActionInspectFailure
		}
	}
	return Report{
		Type:               ReportType,
		Status:             status,
		PacketPath:         relDisplayPath(root, relPath),
		ReconciliationPath: reconciliationDisplayPath,
		FailureID:          packet.FailureID,
		PacketID:           packet.PacketID,
		PartialState:       packet.PartialState,
		ProductLinks:       packet.ProductLinks,
		Blockers:           packetBlockersWithReconciliation(packet, reconciliationRef, reconciliationFindings),
		ResidualWork:       residualWork,
		CandidateAtoms:     candidateAtoms,
		Reconciliation:     reconciliationRef,
		NextAction:         nextAction,
	}, nil
}

func ErrorReport(message string) Report {
	return Report{
		Type:       ReportType,
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}
}

func FormatText(report Report) string {
	var buf bytes.Buffer
	WriteText(&buf, report)
	return buf.String()
}

func WriteText(w io.Writer, report Report) {
	if report.Error != "" {
		fmt.Fprintln(w, report.Error)
		fmt.Fprintf(w, "next_valid_action: %s\n", report.NextAction)
		return
	}
	fmt.Fprintf(w, "recover status: %s\n", report.Status)
	fmt.Fprintf(w, "packet: %s\n", report.PacketPath)
	if report.ReconciliationPath != "" {
		fmt.Fprintf(w, "reconciliation: %s\n", report.ReconciliationPath)
	}
	fmt.Fprintf(w, "failure_id: %s\n", report.FailureID)
	fmt.Fprintf(w, "dirty_status: %s\n", report.PartialState.DirtyStatus)
	for _, blocker := range report.Blockers {
		fmt.Fprintf(w, "blocker: %s\n", blocker)
	}
	printProductLinks(w, report.ProductLinks)
	fmt.Fprintf(w, "next_valid_action: %s\n", report.NextAction)
	for _, residual := range report.ResidualWork {
		fmt.Fprintf(w, "- %s: %s\n", residual.ID, residual.Title)
		if residual.Reason != "" {
			fmt.Fprintf(w, "  reason: %s\n", residual.Reason)
		}
		if len(residual.Paths) > 0 {
			fmt.Fprintf(w, "  paths: %s\n", strings.Join(residual.Paths, ", "))
		}
		if len(residual.EvidenceRefs) > 0 {
			fmt.Fprintf(w, "  evidence_refs: %s\n", strings.Join(EvidenceRefLabels(residual.EvidenceRefs), "; "))
		}
	}
	for _, atom := range report.CandidateAtoms {
		fmt.Fprintf(w, "- %s: %s\n", atom.ID, atom.Title)
		if len(atom.Gates) > 0 {
			fmt.Fprintf(w, "  gates: %s\n", strings.Join(atom.Gates, ", "))
		}
		if len(atom.RequiredTests) > 0 {
			fmt.Fprintf(w, "  required_tests: %s\n", strings.Join(atom.RequiredTests, "; "))
		}
	}
}

func EvidenceRefLabels(refs []recovery.EvidenceRef) []string {
	labels := make([]string, 0, len(refs))
	for _, ref := range refs {
		kind := strings.TrimSpace(ref.Kind)
		if kind == "" {
			continue
		}
		label := kind
		if path := strings.TrimSpace(ref.Path); path != "" {
			label += ":" + path
		} else if id := strings.TrimSpace(ref.ID); id != "" {
			label += ":" + id
		}
		labels = append(labels, label)
	}
	return labels
}

func PacketStatus(packet recovery.DecompositionPacket) string {
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		return "blocked"
	}
	if len(packet.CandidateAtoms) == 0 {
		return "needs_inspection"
	}
	return "ready"
}

func packetStatusWithReconciliation(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) string {
	if reconciliation.Status == recovery.ReconciliationStatusRecovered && reconciliation.NextAction == recovery.NextActionNone {
		return "recovered"
	}
	if reconciliation.Status == recovery.ReconciliationStatusPartial {
		return "partial"
	}
	return PacketStatus(packet)
}

func packetBlockers(packet recovery.DecompositionPacket) []string {
	var blockers []string
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		blockers = append(blockers, "packet next action is "+packet.NextAction)
	}
	if len(packet.CandidateAtoms) == 0 {
		blockers = append(blockers, "packet has no candidate recovery atoms")
	}
	if packet.PartialState.DirtyStatus == recovery.DirtyStatusDirtyOutScope {
		blockers = append(blockers, "workspace has out-of-scope dirty state")
	}
	return blockers
}

func packetBlockersWithReconciliation(packet recovery.DecompositionPacket, reconciliation *recovery.Reconciliation, findings []recovery.ReconciliationValidationFinding) []string {
	if reconciliation != nil && len(findings) == 0 && reconciliation.Status == recovery.ReconciliationStatusRecovered {
		return nil
	}
	blockers := packetBlockers(packet)
	for _, finding := range findings {
		blockers = append(blockers, finding.Code+": "+finding.Message)
	}
	if reconciliation != nil {
		blockers = append(blockers, reconciliation.Blockers...)
	}
	return blockers
}

func remainingCandidateAtoms(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) []recovery.CandidateAtom {
	if len(reconciliation.CompletedAtoms) == 0 {
		return packet.CandidateAtoms
	}
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

func remainingResidualWork(packet recovery.DecompositionPacket, atoms []recovery.CandidateAtom) []recovery.ResidualWork {
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
	remaining := make([]recovery.ResidualWork, 0, len(packet.ResidualWork))
	for _, residual := range packet.ResidualWork {
		if needed[residual.ID] {
			remaining = append(remaining, residual)
		}
	}
	return remaining
}

func sanitizeResidualWork(residuals []recovery.ResidualWork) []recovery.ResidualWork {
	if len(residuals) == 0 {
		return nil
	}
	out := make([]recovery.ResidualWork, 0, len(residuals))
	for _, residual := range residuals {
		residual.EvidenceRefs = sanitizeEvidenceRefs(residual.EvidenceRefs)
		out = append(out, residual)
	}
	return out
}

func sanitizeEvidenceRefs(refs []recovery.EvidenceRef) []recovery.EvidenceRef {
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

func printProductLinks(w io.Writer, links recovery.ProductLinks) {
	printProductLinkList(w, "prd", links.PRDPaths)
	printProductLinkList(w, "adr", links.ADRPaths)
	printProductLinkList(w, "tdd", links.TDDPaths)
	printProductLinkList(w, "ledger", links.LedgerPaths)
}

func printProductLinkList(w io.Writer, label string, paths []string) {
	for _, path := range paths {
		fmt.Fprintf(w, "%s: %s\n", label, path)
	}
}

func tryLoadReconciliation(root string, packetPath string, packet recovery.DecompositionPacket) (recovery.Reconciliation, string, bool, error) {
	if siblingPath, siblingRel, ok := defaultReconciliationSiblingPath(root, packetPath); ok {
		if err := rejectSymlinkedFile(siblingPath, "recovery reconciliation path"); err != nil {
			return recovery.Reconciliation{}, "", false, err
		}
		reconciliation, _, found, err := recovery.TryLoadReconciliation(siblingPath)
		if err != nil || found {
			return reconciliation, siblingRel, found, err
		}
	}
	defaultPath, defaultRel, err := safeDefaultReconciliationPath(root, packet.FailureID)
	if err != nil {
		return recovery.Reconciliation{}, "", false, err
	}
	reconciliation, _, found, err := recovery.TryLoadReconciliation(defaultPath)
	if err != nil || !found {
		return reconciliation, defaultRel, found, err
	}
	return reconciliation, defaultRel, true, nil
}

func safeDefaultReconciliationPath(root string, failureID string) (string, string, error) {
	fullPath, relPath, err := recovery.ReconciliationPath(root, failureID, "")
	if err != nil {
		return "", "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve recovery reconciliation path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("recovery reconciliation path must stay under workspace root")
	}
	if hasSymlinkAncestor(absRoot, absPath) {
		return "", "", fmt.Errorf("recovery reconciliation path must not traverse symlinked directories")
	}
	if err := rejectSymlinkedFile(absPath, "recovery reconciliation path"); err != nil {
		return "", "", err
	}
	return absPath, relPath, nil
}

func rejectSymlinkedFile(path string, label string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", label)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	return nil
}

func defaultReconciliationSiblingPath(root string, packetPath string) (string, string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", false
	}
	absPacketPath, err := filepath.Abs(packetPath)
	if err != nil {
		return "", "", false
	}
	relPacketPath, err := filepath.Rel(absRoot, absPacketPath)
	if err != nil || relPacketPath == "." || strings.HasPrefix(relPacketPath, ".."+string(filepath.Separator)) || relPacketPath == ".." || filepath.IsAbs(relPacketPath) {
		return "", "", false
	}
	fullPath := filepath.Join(filepath.Dir(absPacketPath), "reconciliation.json")
	relPath, err := filepath.Rel(absRoot, fullPath)
	if err != nil {
		return "", "", false
	}
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if isProtectedReconciliationOutputPath(relPath) {
		return "", "", false
	}
	if hasSymlinkAncestor(absRoot, fullPath) {
		return "", "", false
	}
	return fullPath, relPath, true
}

func hasSymlinkAncestor(absRoot string, absPath string) bool {
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

func isProtectedReconciliationOutputPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." {
		return true
	}
	canonical := strings.ToLower(rel)
	if canonical == ".idiom" || strings.HasPrefix(canonical, ".idiom/") {
		return canonical != ".idiom/recovery" && !strings.HasPrefix(canonical, ".idiom/recovery/")
	}
	protectedPrefixes := []string{
		".git/",
		"logs/",
	}
	for _, prefix := range protectedPrefixes {
		if canonical == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(canonical, prefix) {
			return true
		}
	}
	switch canonical {
	case ".env", "config.json":
		return true
	}
	return false
}

func resolvePacketPath(root string, path string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("recovery packet path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absRoot, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve recovery packet path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("recovery packet path must stay under workspace root")
	}
	if hasSymlinkAncestor(absRoot, absPath) {
		return "", fmt.Errorf("recovery packet path must not traverse symlinked directories")
	}
	if info, err := os.Lstat(absPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("recovery packet path must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect recovery packet path: %w", err)
	}
	return filepath.Clean(absPath), nil
}

func relDisplayPath(root string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
