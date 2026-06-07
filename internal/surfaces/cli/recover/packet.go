package recover

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
	atomcmd "idiom/internal/surfaces/cli/atom"
	briefcmd "idiom/internal/surfaces/cli/brief"
	"idiom/internal/workflows/recoverstatus"
	"idiom/internal/workflows/recovery"
)

type atomIntakeReport = atomcmd.IntakeReport
type briefLintFinding = briefcmd.LintFinding

type recoverDecomposeOptions struct {
	ConfigPath string
	SessionID  string
	OutputPath string
	JSON       bool
}

type recoverStatusOptions struct {
	ConfigPath string
	PacketPath string
	JSON       bool
}

type recoverPromoteOptions struct {
	ConfigPath string
	PacketPath string
	AtomID     string
	OutputPath string
	AllowDirty bool
	JSON       bool
}

type recoverReconcileOptions struct {
	ConfigPath       string
	PacketPath       string
	CompletedAtomIDs string
	OutputPath       string
	JSON             bool
}

type recoverDecomposeReport struct {
	Type           string                        `json:"type"`
	Status         string                        `json:"status"`
	SessionID      string                        `json:"session_id,omitempty"`
	FailureID      string                        `json:"failure_id,omitempty"`
	PacketID       string                        `json:"packet_id,omitempty"`
	PacketPath     string                        `json:"packet_path,omitempty"`
	CandidateAtoms []recovery.CandidateAtom      `json:"candidate_atoms,omitempty"`
	NextAction     string                        `json:"next_valid_action"`
	Packet         *recovery.DecompositionPacket `json:"packet,omitempty"`
	Error          string                        `json:"error,omitempty"`
}

type recoverStatusReport = recoverstatus.Report

type recoverPromoteReport struct {
	Type       string              `json:"type"`
	Status     string              `json:"status"`
	PacketPath string              `json:"packet_path,omitempty"`
	AtomID     string              `json:"atom_id,omitempty"`
	OutputPath string              `json:"output_path,omitempty"`
	Gates      []recoverGateReport `json:"gates,omitempty"`
	NextAction string              `json:"next_valid_action"`
	Error      string              `json:"error,omitempty"`
}

type recoverReconcileReport struct {
	Type               string                   `json:"type"`
	Status             string                   `json:"status"`
	PacketPath         string                   `json:"packet_path,omitempty"`
	ReconciliationPath string                   `json:"reconciliation_path,omitempty"`
	FailureID          string                   `json:"failure_id,omitempty"`
	PacketID           string                   `json:"packet_id,omitempty"`
	CompletedAtoms     []string                 `json:"completed_atoms,omitempty"`
	RemainingAtoms     []string                 `json:"remaining_atoms,omitempty"`
	RankerLearning     *recoverRankerLearning   `json:"ranker_learning,omitempty"`
	NextAction         string                   `json:"next_valid_action"`
	Reconciliation     *recovery.Reconciliation `json:"reconciliation,omitempty"`
	Error              string                   `json:"error,omitempty"`
}

type recoverRankerLearning struct {
	Status       string             `json:"status"`
	EventID      string             `json:"event_id,omitempty"`
	Source       string             `json:"source,omitempty"`
	Action       string             `json:"action,omitempty"`
	State        string             `json:"state,omitempty"`
	Reason       string             `json:"reason,omitempty"`
	EvidenceKind string             `json:"evidence_kind,omitempty"`
	Files        []string           `json:"files,omitempty"`
	Metrics      map[string]float64 `json:"metrics,omitempty"`
	Error        string             `json:"error,omitempty"`
}

type recoverGateReport struct {
	Name       string             `json:"name"`
	Status     string             `json:"status"`
	Summary    string             `json:"summary,omitempty"`
	Findings   []briefLintFinding `json:"findings,omitempty"`
	Intake     *atomIntakeReport  `json:"intake,omitempty"`
	ScopePaths []string           `json:"scope_paths,omitempty"`
}

func runRecoverDecomposeMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := recoverDecomposeOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom recover decompose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.SessionID, "session", "", "recovery session id to decompose")
	fs.StringVar(&opts.OutputPath, "output", "", "packet output path; defaults to .idiom/recovery/<failure-id>/packet.json")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable recovery packet report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return emitRecoverDecomposeError(stdout, stderr, opts.JSON, 2, "unexpected positional argument: "+fs.Arg(0))
	}
	if strings.TrimSpace(opts.SessionID) == "" {
		return emitRecoverDecomposeError(stdout, stderr, opts.JSON, 2, "recover decompose: --session is required")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return emitRecoverDecomposeError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	report, err := buildRecoverDecomposeReport(cfg.Workspace.Root, opts)
	if err != nil {
		return emitRecoverDecomposeError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	return writeRecoverDecomposeReport(stdout, stderr, opts.JSON, report, 0)
}

func buildRecoverDecomposeReport(root string, opts recoverDecomposeOptions) (recoverDecomposeReport, error) {
	inspectReport, err := buildRecoverInspectReport(root, opts.SessionID)
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	if inspectReport.Record == nil || inspectReport.Status != "failed" {
		return recoverDecomposeReport{}, fmt.Errorf("recover decompose requires a failed recovery session")
	}
	packet, err := recovery.BuildDecompositionPacket(*inspectReport.Record, recovery.DecompositionOptions{
		Now:          time.Now().UTC(),
		PartialState: inspectReport.PartialState,
	})
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	outputPath, _, err := resolveRecoveryDecompositionOutputPath(root, packet.FailureID, opts.OutputPath)
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	_, relPath, err := recovery.WriteDecompositionPacket(root, packet, outputPath)
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	return recoverDecomposeReport{
		Type:           "idiom.recover.decompose.v1",
		Status:         string(recovery.StatusForPacket(packet)),
		SessionID:      inspectReport.SessionID,
		FailureID:      packet.FailureID,
		PacketID:       packet.PacketID,
		PacketPath:     relPath,
		CandidateAtoms: packet.CandidateAtoms,
		NextAction:     packet.NextAction,
		Packet:         &packet,
	}, nil
}

func runRecoverStatusMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := recoverStatusOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom recover status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.PacketPath, "packet", "", "recovery decomposition packet path")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable recovery packet status")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return emitRecoverStatusError(stdout, stderr, opts.JSON, 2, "unexpected positional argument: "+fs.Arg(0))
	}
	if strings.TrimSpace(opts.PacketPath) == "" {
		return emitRecoverStatusError(stdout, stderr, opts.JSON, 2, "recover status: --packet is required")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return emitRecoverStatusError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	report, err := buildRecoverStatusReport(cfg.Workspace.Root, opts.PacketPath)
	if err != nil {
		return emitRecoverStatusError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	return writeRecoverStatusReport(stdout, stderr, opts.JSON, report, 0)
}

func buildRecoverStatusReport(root string, packetPath string) (recoverStatusReport, error) {
	return recoverstatus.Build(root, packetPath)
}

func runRecoverPromoteMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := recoverPromoteOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom recover promote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.PacketPath, "packet", "", "recovery decomposition packet path")
	fs.StringVar(&opts.AtomID, "atom", "", "candidate recovery atom id")
	fs.StringVar(&opts.OutputPath, "output", "", "brief output path")
	fs.BoolVar(&opts.AllowDirty, "allow-dirty", false, "allow promotion when scoped paths are classified dirty in the packet")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable recovery promotion report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 2, "unexpected positional argument: "+fs.Arg(0))
	}
	if strings.TrimSpace(opts.PacketPath) == "" {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 2, "recover promote: --packet is required")
	}
	if strings.TrimSpace(opts.AtomID) == "" {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 2, "recover promote: --atom is required")
	}
	if strings.TrimSpace(opts.OutputPath) == "" {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 2, "recover promote: --output is required")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	report, err := buildRecoverPromoteReport(cfg.Workspace.Root, opts)
	if err != nil {
		return emitRecoverPromoteError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	code := 0
	if report.Status != "promoted" {
		code = 1
	}
	return writeRecoverPromoteReport(stdout, stderr, opts.JSON, report, code)
}

func runRecoverReconcileMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := recoverReconcileOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom recover reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.PacketPath, "packet", "", "recovery decomposition packet path")
	fs.StringVar(&opts.CompletedAtomIDs, "completed", "", "comma-separated completed recovery atom ids")
	fs.StringVar(&opts.OutputPath, "output", "", "reconciliation output path; defaults to reconciliation.json next to the packet when possible")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable recovery reconciliation report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return emitRecoverReconcileError(stdout, stderr, opts.JSON, 2, "unexpected positional argument: "+fs.Arg(0))
	}
	if strings.TrimSpace(opts.PacketPath) == "" {
		return emitRecoverReconcileError(stdout, stderr, opts.JSON, 2, "recover reconcile: --packet is required")
	}
	if strings.TrimSpace(opts.CompletedAtomIDs) == "" {
		return emitRecoverReconcileError(stdout, stderr, opts.JSON, 2, "recover reconcile: --completed is required")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return emitRecoverReconcileError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	report, err := buildRecoverReconcileReport(cfg.Workspace.Root, opts)
	if err != nil {
		return emitRecoverReconcileError(stdout, stderr, opts.JSON, 1, err.Error())
	}
	return writeRecoverReconcileReport(stdout, stderr, opts.JSON, report, 0)
}

func buildRecoverPromoteReport(root string, opts recoverPromoteOptions) (recoverPromoteReport, error) {
	packetPath := resolveRecoveryPath(root, opts.PacketPath)
	packet, relPacketPath, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil {
		return recoverPromoteReport{}, err
	}
	if !found {
		return recoverPromoteReport{}, fmt.Errorf("recovery decomposition packet not found: %s", opts.PacketPath)
	}
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		return recoverPromoteReport{
			Type:       "idiom.recover.promote.v1",
			Status:     "blocked",
			PacketPath: relDisplayPath(root, relPacketPath),
			AtomID:     strings.TrimSpace(opts.AtomID),
			NextAction: packet.NextAction,
			Error:      "packet is not ready for candidate promotion",
		}, nil
	}
	atom, ok := recovery.FindCandidateAtom(packet, opts.AtomID)
	if !ok {
		return recoverPromoteReport{}, fmt.Errorf("candidate atom %q not found in packet", opts.AtomID)
	}
	currentPartial := inspectRecoveryPartialState(root, failureRecordFromPacket(packet))
	brief := recovery.RenderRecoveryBrief(root, packet, atom)
	tmpPath, cleanup, err := writeRecoveryPromotionTempBrief(root, packet.FailureID, brief)
	if err != nil {
		return recoverPromoteReport{}, err
	}
	defer cleanup()
	allowDirty := opts.AllowDirty
	gates := []recoverGateReport{
		runRecoveryWorkspaceStateGate(currentPartial, opts.AllowDirty),
		runRecoveryBriefLintGate(root, tmpPath),
		runRecoveryAtomIntakeGate(root, tmpPath, brief, allowDirty),
		runRecoveryScopeGate(brief, atom),
		runRecoveryRequiredTestsGate(atom),
	}
	report := recoverPromoteReport{
		Type:       "idiom.recover.promote.v1",
		Status:     "blocked",
		PacketPath: relDisplayPath(root, relPacketPath),
		AtomID:     atom.ID,
		Gates:      gates,
		NextAction: recovery.NextActionInspectFailure,
	}
	outputPath, outputRelPath, err := resolveRecoveryPromotionOutputPath(root, opts.OutputPath)
	if err != nil {
		report.Error = err.Error()
		report.NextAction = recovery.NextActionHumanIntervention
		return report, nil
	}
	report.OutputPath = outputRelPath
	for _, gate := range gates {
		if gate.Status != "pass" {
			report.NextAction = "fix_promotion_gate"
			return report, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return recoverPromoteReport{}, fmt.Errorf("mkdir promotion output dir: %w", err)
	}
	if err := os.WriteFile(outputPath, []byte(brief), 0o644); err != nil {
		return recoverPromoteReport{}, fmt.Errorf("write promotion brief: %w", err)
	}
	report.Status = "promoted"
	report.NextAction = "dispatch_atom"
	return report, nil
}

func buildRecoverReconcileReport(root string, opts recoverReconcileOptions) (recoverReconcileReport, error) {
	packetPath := resolveRecoveryPath(root, opts.PacketPath)
	packet, relPacketPath, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil {
		return recoverReconcileReport{}, err
	}
	if !found {
		return recoverReconcileReport{}, fmt.Errorf("recovery decomposition packet not found: %s", opts.PacketPath)
	}
	outputPath, _, err := resolveRecoveryReconciliationOutputPath(root, packetPath, packet.FailureID, opts.OutputPath)
	if err != nil {
		return recoverReconcileReport{}, err
	}
	var relReconciliationPath string
	completedIDs := recoverCompletedAtomIDs(opts.CompletedAtomIDs)
	if existing, _, existingFound, err := recovery.TryLoadReconciliation(outputPath); err != nil {
		return recoverReconcileReport{}, err
	} else if existingFound {
		if findings := recovery.ValidateReconciliation(packet, existing); len(findings) > 0 {
			return recoverReconcileReport{}, fmt.Errorf("existing recovery reconciliation is invalid: %s", recoverReconciliationFindingMessages(findings))
		}
		completedIDs = append(completedIDs, recovery.ReconciliationCompletedAtomIDs(existing)...)
	}
	verification, err := runRecoveryReconciliationVerification(root, packet, completedIDs)
	if err != nil {
		return recoverReconcileReport{}, err
	}
	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Now().UTC(),
		CompletedAtomIDs: completedIDs,
		Verification:     verification,
	})
	if err != nil {
		return recoverReconcileReport{}, err
	}
	if findings := recovery.ValidateReconciliation(packet, reconciliation); len(findings) > 0 {
		return recoverReconcileReport{}, fmt.Errorf("recovery reconciliation is invalid: %s", recoverReconciliationFindingMessages(findings))
	}
	_, relReconciliationPath, err = recovery.WriteReconciliation(root, reconciliation, outputPath)
	if err != nil {
		return recoverReconcileReport{}, err
	}
	rankerLearning := recordRecoverReconciliationLearning(root, packet, reconciliation)
	completed := make([]string, 0, len(reconciliation.CompletedAtoms))
	for _, atom := range reconciliation.CompletedAtoms {
		completed = append(completed, atom.ID)
	}
	return recoverReconcileReport{
		Type:               "idiom.recover.reconcile.v1",
		Status:             reconciliation.Status,
		PacketPath:         relDisplayPath(root, relPacketPath),
		ReconciliationPath: relReconciliationPath,
		FailureID:          packet.FailureID,
		PacketID:           packet.PacketID,
		CompletedAtoms:     completed,
		RemainingAtoms:     append([]string{}, reconciliation.RemainingAtoms...),
		RankerLearning:     rankerLearning,
		NextAction:         reconciliation.NextAction,
		Reconciliation:     &reconciliation,
	}, nil
}

func recordRecoverReconciliationLearning(root string, packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) *recoverRankerLearning {
	files := recoverLearningCompletedPaths(reconciliation)
	metrics := recoverLearningMetrics(packet, reconciliation)
	score := metrics["packet_quality"]
	resultID := recoverLearningResultID(packet, reconciliation)
	event, err := comprehension.RecordVerifiedRankerEvidence(root, comprehension.VerifiedRankerLearningRequest{
		Source:   comprehension.LearningSourceRecoveryReconciliation,
		Action:   "no_op",
		Files:    files,
		Score:    score,
		TaskID:   packet.FailureID,
		CorpusID: packet.PacketID,
		ResultID: resultID,
		Reason:   comprehension.LearningReasonVerifiedRecovery,
		Verified: true,
		Evidence: comprehension.LearningEvidenceRef{
			Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
			Verified: true,
			TaskID:   packet.FailureID,
			CorpusID: packet.PacketID,
			ResultID: resultID,
			Files:    files,
			Metrics:  metrics,
		},
	})
	if err != nil {
		return &recoverRankerLearning{
			Status:  "error",
			Source:  comprehension.LearningSourceRecoveryReconciliation,
			Action:  "no_op",
			Files:   files,
			Metrics: metrics,
			Error:   fmt.Sprintf("record recovery ranker-learning evidence: %v", err),
		}
	}
	report := &recoverRankerLearning{
		Status:  event.State,
		EventID: event.EventID,
		Source:  event.Source,
		Action:  event.Action,
		State:   event.State,
		Reason:  event.Reason,
		Files:   append([]string{}, event.Files...),
	}
	if event.Evidence != nil {
		report.EvidenceKind = event.Evidence.Kind
		report.Metrics = event.Evidence.Metrics
	}
	return report
}

func recoverLearningCompletedPaths(reconciliation recovery.Reconciliation) []string {
	seen := map[string]bool{}
	var files []string
	for _, atom := range reconciliation.CompletedAtoms {
		for _, path := range atom.RequiredPaths {
			path = normalizeRecoverLearningPath(path)
			if path == "" || path == "." || recoverLearningRankerStatePath(path) || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

func recoverLearningRankerStatePath(path string) bool {
	clean := normalizeRecoverLearningPath(path)
	clean = strings.ToLower(clean)
	return clean == ".idiom/ranker" || strings.HasPrefix(clean, ".idiom/ranker/")
}

func normalizeRecoverLearningPath(path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	return filepath.ToSlash(filepath.Clean(path))
}

func recoverLearningMetrics(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) map[string]float64 {
	packetQuality := 0.5
	if reconciliation.Status == recovery.ReconciliationStatusRecovered &&
		reconciliation.Verification.Status == recovery.ReconciliationVerificationPass &&
		len(reconciliation.Blockers) == 0 {
		packetQuality = 1
	}
	localizationImproved := 0.0
	if len(packet.CandidateAtoms) > 1 {
		localizationImproved = 1
	}
	return map[string]float64{
		"candidate_atom_count":       float64(len(packet.CandidateAtoms)),
		"completed_atom_count":       float64(len(reconciliation.CompletedAtoms)),
		"remaining_atom_count":       float64(len(reconciliation.RemainingAtoms)),
		"required_test_count":        float64(len(reconciliation.Verification.RequiredTests)),
		"verification_command_count": float64(len(reconciliation.Verification.Commands)),
		"localization_improved":      localizationImproved,
		"packet_quality":             packetQuality,
	}
}

func recoverLearningResultID(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) string {
	parts := []string{
		packet.FailureID,
		packet.PacketID,
		reconciliation.PacketDigest,
		reconciliation.Status,
		strings.Join(recovery.ReconciliationCompletedAtomIDs(reconciliation), ","),
		strings.Join(reconciliation.RemainingAtoms, ","),
	}
	sum := localops.StableID(strings.Join(parts, "\x00"))
	return "recovery_reconciliation_" + sum
}

func failureRecordFromPacket(packet recovery.DecompositionPacket) recovery.FailureRecord {
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

func runRecoveryWorkspaceStateGate(partial recovery.PartialState, allowDirty bool) recoverGateReport {
	report := recoverGateReport{
		Name:    recovery.GateWorkspaceState,
		Status:  "pass",
		Summary: fmt.Sprintf("dirty_status=%s scope_status=%s", partial.DirtyStatus, partial.ScopeStatus),
	}
	switch partial.DirtyStatus {
	case recovery.DirtyStatusDirtyOutScope:
		report.Status = "block"
		report.Summary = "out-of-scope dirty workspace state must be inspected before promotion"
	case recovery.DirtyStatusDirtyInScope:
		if !allowDirty {
			report.Status = "block"
			report.Summary = "in-scope dirty workspace state requires explicit --allow-dirty before promotion"
		}
	}
	if report.Status == "pass" && partial.ScopeStatus == recovery.ScopeStatusOutOfScope {
		report.Status = "block"
		report.Summary = "out-of-scope workspace state must be inspected before promotion"
	}
	return report
}

func runRecoveryBriefLintGate(root string, briefPath string) recoverGateReport {
	scriptPath, cleanup, err := briefcmd.WriteEmbeddedPreflight()
	if err != nil {
		return recoverGateReport{Name: recovery.GateBriefLint, Status: "error", Summary: err.Error()}
	}
	defer cleanup()
	cmd := exec.Command("python3", scriptPath, briefPath)
	cmd.Dir = root
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	exit := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exit = exitErr.ExitCode()
		} else {
			return recoverGateReport{Name: recovery.GateBriefLint, Status: "error", Summary: fmt.Sprintf("python3 failed: %v", runErr)}
		}
	}
	findings := briefcmd.ParseLintFindings(briefPath, outBuf.String()+errBuf.String())
	status := "pass"
	if exit != 0 {
		status = "block"
	}
	return recoverGateReport{Name: recovery.GateBriefLint, Status: status, Findings: findings, Summary: fmt.Sprintf("brief lint exit %d", exit)}
}

func runRecoveryAtomIntakeGate(root string, specPath string, body string, allowDirty bool) recoverGateReport {
	report := atomcmd.EvaluateIntake(body, specPath, root, allowDirty, atomcmd.DefaultScopePaths(body))
	status := "pass"
	if !report.Dispatchable {
		status = "block"
	}
	return recoverGateReport{Name: recovery.GateAtomIntake, Status: status, Intake: &report, Summary: string(report.State)}
}

func runRecoveryScopeGate(body string, atom recovery.CandidateAtom) recoverGateReport {
	scopePaths := atomcmd.DefaultScopePaths(body)
	missing, extra := comparePathSets(atom.RequiredPaths, scopePaths)
	var blocked []string
	for _, forbidden := range atom.ForbiddenPaths {
		for _, path := range scopePaths {
			if path == forbidden || strings.HasPrefix(path, strings.TrimSuffix(forbidden, "/")+"/") {
				blocked = append(blocked, path)
			}
		}
	}
	status := "pass"
	var parts []string
	if len(missing) > 0 {
		status = "block"
		parts = append(parts, "missing required paths: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		status = "block"
		parts = append(parts, "extra scoped paths: "+strings.Join(extra, ", "))
	}
	if len(blocked) > 0 {
		status = "block"
		parts = append(parts, "forbidden paths: "+strings.Join(blocked, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "scope matches candidate atom")
	}
	return recoverGateReport{Name: recovery.GateScope, Status: status, Summary: strings.Join(parts, "; "), ScopePaths: scopePaths}
}

func runRecoveryRequiredTestsGate(atom recovery.CandidateAtom) recoverGateReport {
	if len(atom.RequiredTests) == 0 {
		return recoverGateReport{Name: recovery.GateRequiredTests, Status: "block", Summary: "candidate atom has no required tests"}
	}
	return recoverGateReport{Name: recovery.GateRequiredTests, Status: "pass", Summary: strings.Join(atom.RequiredTests, "; ")}
}

func writeRecoveryPromotionTempBrief(root string, failureID string, content string) (string, func(), error) {
	dir := filepath.Join(root, ".idiom", "recovery", failureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, fmt.Errorf("mkdir recovery promotion temp dir: %w", err)
	}
	file, err := os.CreateTemp(dir, "promote-*.md")
	if err != nil {
		return "", func() {}, fmt.Errorf("create recovery promotion temp brief: %w", err)
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", func() {}, fmt.Errorf("write recovery promotion temp brief: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, fmt.Errorf("close recovery promotion temp brief: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func comparePathSets(required []string, actual []string) (missing []string, extra []string) {
	req := pathSet(required)
	act := pathSet(actual)
	for path := range req {
		if !act[path] {
			missing = append(missing, path)
		}
	}
	for path := range act {
		if !req[path] {
			extra = append(extra, path)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func pathSet(paths []string) map[string]bool {
	set := map[string]bool{}
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path != "" && path != "." {
			set[path] = true
		}
	}
	return set
}

func recoverCompletedAtomIDs(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func runRecoveryReconciliationVerification(root string, packet recovery.DecompositionPacket, completedIDs []string) (recovery.ReconciliationVerification, error) {
	requiredTests := recoverRequiredTestsForCompletedAtoms(packet, completedIDs)
	verification := recovery.ReconciliationVerification{
		Status:        recovery.ReconciliationVerificationPass,
		RequiredTests: requiredTests,
	}
	if len(requiredTests) == 0 {
		return verification, nil
	}
	for _, command := range requiredTests {
		result, err := runRecoveryVerificationCommand(root, command)
		verification.Commands = append(verification.Commands, result)
		if err != nil {
			verification.Status = recovery.ReconciliationVerificationFail
			return verification, err
		}
	}
	return verification, nil
}

func recoverRequiredTestsForCompletedAtoms(packet recovery.DecompositionPacket, completedIDs []string) []string {
	completed := map[string]bool{}
	for _, id := range completedIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			completed[id] = true
		}
	}
	seen := map[string]bool{}
	var tests []string
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			continue
		}
		for _, test := range atom.RequiredTests {
			test = strings.Join(strings.Fields(test), " ")
			if test == "" || seen[test] {
				continue
			}
			seen[test] = true
			tests = append(tests, test)
		}
	}
	sort.Strings(tests)
	return tests
}

func runRecoveryVerificationCommand(root string, command string) (recovery.ReconciliationCommandResult, error) {
	command = strings.Join(strings.Fields(command), " ")
	startedAt := time.Now().UTC()
	result := recovery.ReconciliationCommandResult{
		Command:   command,
		Status:    recovery.ReconciliationVerificationFail,
		ExitCode:  -1,
		StartedAt: startedAt.Format(time.RFC3339Nano),
	}
	args, err := parseRecoveryVerificationCommand(command)
	if err != nil {
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("recovery verification command timed out: %s", command)
		}
		return result, fmt.Errorf("recovery verification command failed: %s: %s", command, strings.TrimSpace(stderr.String()))
	}
	result.Status = recovery.ReconciliationVerificationPass
	result.ExitCode = 0
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result, nil
}

func parseRecoveryVerificationCommand(command string) ([]string, error) {
	if strings.ContainsAny(command, ";&|><$`\n\r") {
		return nil, fmt.Errorf("recovery verification command contains unsupported shell syntax: %s", command)
	}
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" || args[1] != "test" {
		return nil, fmt.Errorf("unsupported recovery verification command: %s", command)
	}
	if len(args) == 2 {
		return nil, fmt.Errorf("recovery verification command must name a package pattern: %s", command)
	}
	for _, arg := range args[2:] {
		if arg != "." && !strings.HasPrefix(arg, "./") {
			return nil, fmt.Errorf("unsupported recovery verification argument %q in %s", arg, command)
		}
	}
	return args, nil
}

func resolveRecoveryPath(root string, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func resolveRecoveryDecompositionOutputPath(root string, failureID string, outputPath string) (string, string, error) {
	outputPath = strings.TrimSpace(outputPath)
	fullPath := outputPath
	if outputPath == "" {
		var err error
		fullPath, _, err = recovery.DecompositionPacketPath(root, failureID, "")
		if err != nil {
			return "", "", err
		}
	} else if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, fullPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve recovery decomposition output path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("recovery decomposition output path must stay under workspace root")
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if isProtectedRecoveryReconciliationOutputPath(rel) {
		return "", "", fmt.Errorf("recovery decomposition output path is protected: %s", rel)
	}
	if isRecoveryControlArtifactPath(rel) {
		return "", "", fmt.Errorf("recovery decomposition output path would overwrite a recovery control artifact: %s", rel)
	}
	if hasRecoverySymlinkAncestor(absRoot, absPath) {
		return "", "", fmt.Errorf("recovery decomposition output path must not traverse symlinked directories")
	}
	return absPath, rel, nil
}

func resolveRecoveryPromotionOutputPath(root string, path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("promotion output path is empty")
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, fullPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve promotion output path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("promotion output path must stay under workspace root")
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if isProtectedPromotionOutputPath(rel) {
		return "", "", fmt.Errorf("promotion output path is protected: %s", rel)
	}
	return absPath, rel, nil
}

func resolveRecoveryReconciliationOutputPath(root string, packetPath string, failureID string, outputPath string) (string, string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		if fullPath, relPath, ok := defaultRecoveryReconciliationSiblingPath(root, packetPath); ok {
			return fullPath, relPath, nil
		}
		return recovery.ReconciliationPath(root, failureID, "")
	}
	fullPath := outputPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, fullPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve recovery reconciliation output path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("recovery reconciliation output path must stay under workspace root")
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if isProtectedRecoveryReconciliationOutputPath(rel) {
		return "", "", fmt.Errorf("recovery reconciliation output path is protected: %s", rel)
	}
	if hasRecoverySymlinkAncestor(absRoot, absPath) {
		return "", "", fmt.Errorf("recovery reconciliation output path must not traverse symlinked directories")
	}
	return absPath, rel, nil
}

func defaultRecoveryReconciliationSiblingPath(root string, packetPath string) (string, string, bool) {
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
	if isProtectedRecoveryReconciliationOutputPath(relPath) {
		return "", "", false
	}
	if hasRecoverySymlinkAncestor(absRoot, fullPath) {
		return "", "", false
	}
	return fullPath, relPath, true
}

func hasRecoverySymlinkAncestor(absRoot string, absPath string) bool {
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

func isProtectedRecoveryReconciliationOutputPath(rel string) bool {
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

func isProtectedPromotionOutputPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." {
		return true
	}
	canonical := strings.ToLower(rel)
	protectedPrefixes := []string{
		".git/",
		".idiom/",
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

func isRecoveryControlArtifactPath(rel string) bool {
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

func recoverReconciliationFindingMessages(findings []recovery.ReconciliationValidationFinding) string {
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Code+": "+finding.Message)
	}
	return strings.Join(messages, "; ")
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

func emitRecoverDecomposeError(stdout io.Writer, stderr io.Writer, jsonOut bool, code int, message string) int {
	report := recoverDecomposeReport{
		Type:       "idiom.recover.decompose.v1",
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}
	return writeRecoverDecomposeReport(stdout, stderr, jsonOut, report, code)
}

func emitRecoverStatusError(stdout io.Writer, stderr io.Writer, jsonOut bool, code int, message string) int {
	return writeRecoverStatusReport(stdout, stderr, jsonOut, recoverstatus.ErrorReport(message), code)
}

func emitRecoverPromoteError(stdout io.Writer, stderr io.Writer, jsonOut bool, code int, message string) int {
	report := recoverPromoteReport{
		Type:       "idiom.recover.promote.v1",
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}
	return writeRecoverPromoteReport(stdout, stderr, jsonOut, report, code)
}

func emitRecoverReconcileError(stdout io.Writer, stderr io.Writer, jsonOut bool, code int, message string) int {
	report := recoverReconcileReport{
		Type:       "idiom.recover.reconcile.v1",
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}
	return writeRecoverReconcileReport(stdout, stderr, jsonOut, report, code)
}

func writeRecoverDecomposeReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverDecomposeReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
		fmt.Fprintf(stderr, "next_valid_action: %s\n", report.NextAction)
		return code
	}
	fmt.Fprintf(stdout, "recover decompose: %s\n", report.Status)
	fmt.Fprintf(stdout, "packet: %s\n", report.PacketPath)
	fmt.Fprintf(stdout, "next_valid_action: %s\n", report.NextAction)
	for _, atom := range report.CandidateAtoms {
		fmt.Fprintf(stdout, "- %s: %s\n", atom.ID, atom.Title)
	}
	return code
}

func writeRecoverStatusReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverStatusReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		recoverstatus.WriteText(stderr, report)
		return code
	}
	recoverstatus.WriteText(stdout, report)
	return code
}

func writeRecoverPromoteReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverPromoteReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
		fmt.Fprintf(stderr, "next_valid_action: %s\n", report.NextAction)
		return code
	}
	fmt.Fprintf(stdout, "recover promote: %s\n", report.Status)
	if report.OutputPath != "" {
		fmt.Fprintf(stdout, "output: %s\n", report.OutputPath)
	}
	fmt.Fprintf(stdout, "next_valid_action: %s\n", report.NextAction)
	for _, gate := range report.Gates {
		fmt.Fprintf(stdout, "- %s: %s", gate.Name, gate.Status)
		if gate.Summary != "" {
			fmt.Fprintf(stdout, " (%s)", gate.Summary)
		}
		fmt.Fprintln(stdout)
	}
	return code
}

func writeRecoverReconcileReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverReconcileReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
		fmt.Fprintf(stderr, "next_valid_action: %s\n", report.NextAction)
		return code
	}
	fmt.Fprintf(stdout, "recover reconcile: %s\n", report.Status)
	if report.ReconciliationPath != "" {
		fmt.Fprintf(stdout, "reconciliation: %s\n", report.ReconciliationPath)
	}
	if len(report.CompletedAtoms) > 0 {
		fmt.Fprintf(stdout, "completed_atoms: %s\n", strings.Join(report.CompletedAtoms, ", "))
	}
	if len(report.RemainingAtoms) > 0 {
		fmt.Fprintf(stdout, "remaining_atoms: %s\n", strings.Join(report.RemainingAtoms, ", "))
	}
	if report.RankerLearning != nil {
		fmt.Fprintf(stdout, "ranker_learning: %s", report.RankerLearning.Status)
		if report.RankerLearning.EventID != "" {
			fmt.Fprintf(stdout, " (%s)", report.RankerLearning.EventID)
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "next_valid_action: %s\n", report.NextAction)
	return code
}

func encodeRecoverJSON(stdout io.Writer, stderr io.Writer, report any, code int) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(stderr, "recover: encode report: %v\n", err)
		return 1
	}
	return code
}
