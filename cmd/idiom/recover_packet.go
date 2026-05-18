package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/recovery"
)

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

type recoverStatusReport struct {
	Type           string                   `json:"type"`
	Status         string                   `json:"status"`
	PacketPath     string                   `json:"packet_path,omitempty"`
	FailureID      string                   `json:"failure_id,omitempty"`
	PacketID       string                   `json:"packet_id,omitempty"`
	PartialState   recovery.PartialState    `json:"partial_state"`
	ProductLinks   recovery.ProductLinks    `json:"product_links,omitempty"`
	CandidateAtoms []recovery.CandidateAtom `json:"candidate_atoms,omitempty"`
	NextAction     string                   `json:"next_valid_action"`
	Error          string                   `json:"error,omitempty"`
}

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
	fs.StringVar(&opts.SessionID, "session", "", "plan-execute session id to decompose")
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
		return recoverDecomposeReport{}, fmt.Errorf("recover decompose requires a failed plan-execute session")
	}
	packet, err := recovery.BuildDecompositionPacket(*inspectReport.Record, recovery.DecompositionOptions{
		Now:          time.Now().UTC(),
		PartialState: inspectReport.PartialState,
	})
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	_, relPath, err := recovery.WriteDecompositionPacket(root, packet, opts.OutputPath)
	if err != nil {
		return recoverDecomposeReport{}, err
	}
	return recoverDecomposeReport{
		Type:           "idiom.recover.decompose.v1",
		Status:         recoverPacketStatus(packet),
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
	packetPath = resolveRecoveryPath(root, packetPath)
	packet, relPath, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil {
		return recoverStatusReport{}, err
	}
	if !found {
		return recoverStatusReport{}, fmt.Errorf("recovery decomposition packet not found: %s", packetPath)
	}
	return recoverStatusReport{
		Type:           "idiom.recover.status.v1",
		Status:         recoverPacketStatus(packet),
		PacketPath:     relDisplayPath(root, relPath),
		FailureID:      packet.FailureID,
		PacketID:       packet.PacketID,
		PartialState:   packet.PartialState,
		ProductLinks:   packet.ProductLinks,
		CandidateAtoms: packet.CandidateAtoms,
		NextAction:     packet.NextAction,
	}, nil
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
	brief := recovery.RenderRecoveryBrief(root, packet, atom)
	tmpPath, cleanup, err := writeRecoveryPromotionTempBrief(root, packet.FailureID, brief)
	if err != nil {
		return recoverPromoteReport{}, err
	}
	defer cleanup()
	allowDirty := opts.AllowDirty || packet.PartialState.DirtyStatus == recovery.DirtyStatusDirtyInScope
	gates := []recoverGateReport{
		runRecoveryBriefLintGate(root, tmpPath),
		runRecoveryAtomIntakeGate(root, tmpPath, brief, allowDirty),
		runRecoveryScopeGate(brief, atom),
	}
	report := recoverPromoteReport{
		Type:       "idiom.recover.promote.v1",
		Status:     "blocked",
		PacketPath: relDisplayPath(root, relPacketPath),
		AtomID:     atom.ID,
		OutputPath: relDisplayPath(root, resolveRecoveryPath(root, opts.OutputPath)),
		Gates:      gates,
		NextAction: recovery.NextActionInspectFailure,
	}
	for _, gate := range gates {
		if gate.Status != "pass" {
			report.NextAction = "fix_promotion_gate"
			return report, nil
		}
	}
	outputPath := resolveRecoveryPath(root, opts.OutputPath)
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

func runRecoveryBriefLintGate(root string, briefPath string) recoverGateReport {
	scriptPath, cleanup, err := writeEmbeddedBriefPreflight()
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
	findings := parseBriefLintFindings(briefPath, outBuf.String()+errBuf.String())
	status := "pass"
	if exit != 0 {
		status = "block"
	}
	return recoverGateReport{Name: recovery.GateBriefLint, Status: status, Findings: findings, Summary: fmt.Sprintf("brief lint exit %d", exit)}
}

func runRecoveryAtomIntakeGate(root string, specPath string, body string, allowDirty bool) recoverGateReport {
	report := evaluateAtomIntake(body, specPath, root, allowDirty)
	status := "pass"
	if !report.Dispatchable {
		status = "block"
	}
	return recoverGateReport{Name: recovery.GateAtomIntake, Status: status, Intake: &report, Summary: string(report.State)}
}

func runRecoveryScopeGate(body string, atom recovery.CandidateAtom) recoverGateReport {
	scopePaths := extractDeliverableTargets(body, refineDetectMode(body))
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

func recoverPacketStatus(packet recovery.DecompositionPacket) string {
	if packet.NextAction != recovery.NextActionReviewDecomposition {
		return "blocked"
	}
	if len(packet.CandidateAtoms) == 0 {
		return "needs_inspection"
	}
	return "ready"
}

func resolveRecoveryPath(root string, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
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
	report := recoverStatusReport{
		Type:       "idiom.recover.status.v1",
		Status:     "error",
		NextAction: recovery.NextActionInspectFailure,
		Error:      message,
	}
	return writeRecoverStatusReport(stdout, stderr, jsonOut, report, code)
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

func writeRecoverDecomposeReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverDecomposeReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
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
		fmt.Fprintln(stderr, report.Error)
		return code
	}
	fmt.Fprintf(stdout, "recover status: %s\n", report.Status)
	fmt.Fprintf(stdout, "packet: %s\n", report.PacketPath)
	fmt.Fprintf(stdout, "failure_id: %s\n", report.FailureID)
	fmt.Fprintf(stdout, "dirty_status: %s\n", report.PartialState.DirtyStatus)
	fmt.Fprintf(stdout, "next_valid_action: %s\n", report.NextAction)
	for _, atom := range report.CandidateAtoms {
		fmt.Fprintf(stdout, "- %s: %s\n", atom.ID, atom.Title)
	}
	return code
}

func writeRecoverPromoteReport(stdout io.Writer, stderr io.Writer, jsonOut bool, report recoverPromoteReport, code int) int {
	if jsonOut {
		return encodeRecoverJSON(stdout, stderr, report, code)
	}
	if report.Error != "" {
		fmt.Fprintln(stderr, report.Error)
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

func encodeRecoverJSON(stdout io.Writer, stderr io.Writer, report any, code int) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(stderr, "recover: encode report: %v\n", err)
		return 1
	}
	return code
}
