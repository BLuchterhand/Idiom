package recoverstatus

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

const ReportType = string(recoverycontract.ReportTypeStatus)

type Report struct {
	Type                   string                                             `json:"type"`
	Status                 recoverycontract.PacketStatus                      `json:"status"`
	WorkflowID             workflowstate.WorkflowID                           `json:"workflow_id,omitempty"`
	WorkflowState          workflowstate.StateID                              `json:"workflow_state,omitempty"`
	WorkflowTerminalState  workflowstate.TerminalState                        `json:"workflow_terminal_state,omitempty"`
	Dispatchable           bool                                               `json:"dispatchable"`
	PacketPath             string                                             `json:"packet_path,omitempty"`
	ReconciliationPath     string                                             `json:"reconciliation_path,omitempty"`
	FailureID              string                                             `json:"failure_id,omitempty"`
	PacketID               string                                             `json:"packet_id,omitempty"`
	FailureClass           string                                             `json:"failure_class,omitempty"`
	FailureKind            string                                             `json:"failure_kind,omitempty"`
	SourceSessionID        string                                             `json:"source_session_id,omitempty"`
	CandidateAtomCount     int                                                `json:"candidate_atom_count,omitempty"`
	CompletedAtomCount     int                                                `json:"completed_atom_count,omitempty"`
	RemainingAtomCount     int                                                `json:"remaining_atom_count,omitempty"`
	ResidualWorkCount      int                                                `json:"residual_work_count,omitempty"`
	Gates                  []string                                           `json:"gates,omitempty"`
	RequiredTests          []string                                           `json:"required_tests,omitempty"`
	PartialState           recoverycontract.PartialState                      `json:"partial_state"`
	ProductLinks           recoverycontract.ProductLinks                      `json:"product_links,omitempty"`
	Blockers               []string                                           `json:"blockers,omitempty"`
	ResidualWork           []recoverycontract.ResidualWork                    `json:"residual_work,omitempty"`
	CandidateAtoms         []recoverycontract.CandidateAtom                   `json:"candidate_atoms,omitempty"`
	Reconciliation         *recoverycontract.Reconciliation                   `json:"reconciliation,omitempty"`
	ReconciliationFindings []recoverycontract.ReconciliationValidationFinding `json:"reconciliation_findings,omitempty"`
	NextAction             recoverycontract.NextAction                        `json:"next_valid_action"`
	Error                  string                                             `json:"error,omitempty"`
}

type BuildOptions struct {
	ReconciliationPath string
}

func Build(root string, packetPath string) (Report, error) {
	return BuildWithOptions(root, packetPath, BuildOptions{})
}

func BuildWithOptions(root string, packetPath string, opts BuildOptions) (Report, error) {
	safePacketPath, _, err := recovery.SafePacketPath(root, packetPath)
	if err != nil {
		return Report{}, err
	}

	packet, relPath, found, err := recovery.TryLoadSafeDecompositionPacket(root, packetPath)
	if err != nil {
		return Report{}, err
	}

	if !found {
		return Report{}, fmt.Errorf("recovery decomposition packet not found: %s", packetPath)
	}

	reconciliation, reconciliationPath, reconciliationFound, err := tryLoadReconciliation(root, safePacketPath, packet, opts.ReconciliationPath)
	if err != nil {
		return Report{}, err
	}

	var reconciliationRef *recoverycontract.Reconciliation

	reconciliationDisplayPath := ""

	if reconciliationFound {
		reconciliationRef = &reconciliation
		reconciliationDisplayPath = relDisplayPath(root, reconciliationPath)
	}

	projection := recovery.ProjectPacketStatus(packet, reconciliationRef)
	completedAtomCount := 0
	if reconciliationRef != nil {
		completedAtomCount = len(reconciliationRef.CompletedAtoms)
	}

	return Report{
		Type:                   ReportType,
		Status:                 projection.Status,
		WorkflowID:             projection.WorkflowID,
		WorkflowState:          projection.WorkflowState,
		WorkflowTerminalState:  projection.WorkflowTerminalState,
		Dispatchable:           projection.Dispatchable,
		PacketPath:             relDisplayPath(root, relPath),
		ReconciliationPath:     reconciliationDisplayPath,
		FailureID:              packet.FailureID,
		PacketID:               packet.PacketID,
		FailureClass:           string(packet.Failure.Class),
		FailureKind:            packet.Failure.Kind,
		SourceSessionID:        packet.SourceRun.SessionID,
		CandidateAtomCount:     len(packet.CandidateAtoms),
		CompletedAtomCount:     completedAtomCount,
		RemainingAtomCount:     len(projection.CandidateAtoms),
		ResidualWorkCount:      len(projection.ResidualWork),
		Gates:                  packetGates(packet),
		RequiredTests:          packetRequiredTests(packet),
		PartialState:           packet.PartialState,
		ProductLinks:           packet.ProductLinks,
		Blockers:               projection.Blockers,
		ResidualWork:           sanitizeResidualWork(projection.ResidualWork),
		CandidateAtoms:         projection.CandidateAtoms,
		Reconciliation:         projection.Reconciliation,
		ReconciliationFindings: append([]recoverycontract.ReconciliationValidationFinding(nil), projection.ReconciliationFindings...),
		NextAction:             projection.NextAction,
	}, nil
}

func ErrorReport(message string) Report {
	return Report{
		Type:                  ReportType,
		Status:                recoverycontract.PacketStatusBlocked,
		WorkflowID:            workflowstate.WorkflowRecovery,
		WorkflowState:         workflowstate.StateTerminal,
		WorkflowTerminalState: workflowstate.TerminalStateBlocked,
		NextAction:            recoverycontract.NextActionInspectFailure,
		Error:                 message,
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
	fmt.Fprintf(w, "workflow_state: %s\n", report.WorkflowState)
	fmt.Fprintf(w, "workflow_terminal_state: %s\n", report.WorkflowTerminalState)
	fmt.Fprintf(w, "dispatchable: %v\n", report.Dispatchable)
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

func EvidenceRefLabels(refs []recoverycontract.EvidenceRef) []string {
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

func sanitizeResidualWork(residuals []recoverycontract.ResidualWork) []recoverycontract.ResidualWork {
	if len(residuals) == 0 {
		return nil
	}

	out := make([]recoverycontract.ResidualWork, 0, len(residuals))
	for _, residual := range residuals {
		residual.EvidenceRefs = sanitizeEvidenceRefs(residual.EvidenceRefs)
		out = append(out, residual)
	}

	return out
}

func sanitizeEvidenceRefs(refs []recoverycontract.EvidenceRef) []recoverycontract.EvidenceRef {
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

func printProductLinks(w io.Writer, links recoverycontract.ProductLinks) {
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

func tryLoadReconciliation(root string, packetPath string, packet recoverycontract.DecompositionPacket, configuredPath string) (recoverycontract.Reconciliation, string, bool, error) {
	if strings.TrimSpace(configuredPath) != "" {
		fullPath, relPath, err := recovery.SafePacketPath(root, configuredPath)
		if err != nil {
			return recoverycontract.Reconciliation{}, "", false, err
		}

		reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
		if err != nil || !found {
			return reconciliation, relPath, found, err
		}

		return reconciliation, relDisplayPath(root, fullPath), true, nil
	}

	if siblingPath, siblingRel, ok := recovery.SafeSiblingReconciliationPath(root, packetPath); ok {
		reconciliation, _, found, err := recovery.TryLoadReconciliation(siblingPath)
		if err != nil || found {
			return reconciliation, siblingRel, found, err
		}
	}

	defaultPath, defaultRel, err := recovery.SafeDefaultReconciliationPath(root, packet.FailureID)
	if err != nil {
		return recoverycontract.Reconciliation{}, "", false, err
	}

	reconciliation, _, found, err := recovery.TryLoadReconciliation(defaultPath)
	if err != nil || !found {
		return reconciliation, defaultRel, found, err
	}
	return reconciliation, defaultRel, true, nil
}

func packetGates(packet recoverycontract.DecompositionPacket) []string {
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
	return sortedStrings(gates)
}

func packetRequiredTests(packet recoverycontract.DecompositionPacket) []string {
	seen := map[string]bool{}
	var tests []string
	for _, atom := range packet.CandidateAtoms {
		for _, test := range atom.RequiredTests {
			test = strings.TrimSpace(test)
			if test == "" || seen[test] {
				continue
			}
			seen[test] = true
			tests = append(tests, test)
		}
	}
	return sortedStrings(tests)
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
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
