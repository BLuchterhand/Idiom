package localops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
)

type QuickstartOptions struct {
	ConfigPath string
	Profile    string
	Task       string
	Cleanup    string
	Version    string
	Commit     string
}

type ToolExecutor interface {
	Execute(ctx context.Context, req workspace.ToolRequest) workspace.ToolResult
}

type QuickstartDeps struct {
	LoadConfig func(path string) (config.Config, error)
	Getwd      func() (string, error)
	NewManager func(cfg config.Workspace) ToolExecutor
}

func BuildQuickstartSmokeReport(opts QuickstartOptions, deps QuickstartDeps) QuickstartSmoke {
	deps = deps.withDefaults()
	task := strings.TrimSpace(opts.Task)
	if task == "" {
		task = "localize first useful Idiom quickstart packet"
	}
	smoke := QuickstartSmoke{
		SmokeID:              StableID(strings.Join([]string{"p7.7", task, opts.Cleanup}, "|")),
		Status:               "pass",
		ClientClassification: "open-source",
		EnvironmentClass:     "source-build",
		AssistanceClass:      "none",
		DurationSource:       "deterministic_command_step_seconds",
		SetupInstallReadiness: ActivationSetupEvidence{
			Status:              "pass",
			CommandID:           "p77-setup-readiness",
			InstallSetupPath:    "source-build-or-local-binary",
			BinaryVersion:       opts.Version,
			GitCommit:           opts.Commit,
			OSArch:              runtime.GOOS + "/" + runtime.GOARCH,
			ReleaseInstallProof: "not_claimed",
			ReadinessProfile:    firstNonEmpty(opts.Profile, "all"),
		},
		SuggestedVerification: ActivationVerification{
			Status:       "pass",
			CommandID:    "p77-suggested-verification",
			WasRun:       false,
			NotRunReason: "quickstart smoke records suggested verification; execution belongs to the caller or release gate",
		},
		ProductEvidenceSafeOutput: ActivationProductEvidence{
			Status:                 "pending",
			ForbiddenFieldCheck:    "pending",
			RedactionStatus:        "pending",
			P7_11Linkage:           "deferred_to_p7.11_export",
			P9_6Linkage:            "activation_report_input",
			HostedTelemetryDefault: false,
			FieldList: []string{
				"setup_install_readiness",
				"first_useful_packet",
				"freshness_check",
				"audit_report",
				"suggested_verification",
				"product_evidence_safe_output",
				"failure_reason",
				"docs_gap",
				"privacy_redaction_status",
				"cleanup",
				"time_to_first_packet_seconds",
				"time_to_trust_evidence_seconds",
			},
		},
		FailureReason: ActivationFailureReason{Status: "none", PrimaryClass: "none", RetryAllowed: false},
		DocsGap:       ActivationDocsGap{Status: "none", GapRecorded: false},
		PrivacyRedactionStatus: ActivationPrivacyEvidence{
			Status:                  "pass",
			RedactionReviewer:       "automated_local_ops_smoke",
			ForbiddenFieldCheck:     "pass",
			RawPromptCaptured:       false,
			RawFileBodyCaptured:     false,
			SecretCaptured:          false,
			PrivatePathCaptured:     false,
			PrivateRepoURLCaptured:  false,
			UnredactedLogCaptured:   false,
			HostedTelemetryRequired: false,
			RedactedFields:          []string{"workspace_root", "artifact_path", "config_path", "telemetry_path", "raw_output", "quickstart_task"},
		},
	}
	smoke.Commands = append(smoke.Commands, ActivationCommand{
		CommandID:      "p77-setup-readiness",
		Label:          "install/setup readiness",
		ExitStatus:     "pass",
		ElapsedSeconds: 1,
	})

	cfg, err := deps.LoadConfig(opts.ConfigPath)
	if err != nil {
		RecordQuickstartFailure(&smoke, "setup", "load_config_failed", err)
		smoke.Cleanup = QuickstartCleanupEvidence("", opts.Cleanup, "", cfg.Workspace.Root, false)
		return smoke
	}
	root := cfg.Workspace.Root
	if strings.TrimSpace(root) == "" {
		if wd, wdErr := deps.Getwd(); wdErr == nil {
			root = wd
		}
	}
	mgr := deps.NewManager(cfg.Workspace)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	packetReq := workspace.ToolRequest{Tool: "context_pack", Task: task, Mode: "plan", Limit: 4}
	packetResult := mgr.Execute(ctx, packetReq)
	smoke.Commands = append(smoke.Commands, CommandEvidence("p77-context-pack", "first useful context_pack", packetResult, 2))
	packetOutput := DecodeJSONObject(packetResult.Output)
	smoke.FirstUsefulPacket = PacketEvidence(packetResult, packetOutput, task)
	if smoke.FirstUsefulPacket.Status != "pass" {
		RecordQuickstartFailure(&smoke, "context_pack", smoke.FirstUsefulPacket.BlockerReason, nil)
	}
	smoke.TimeToFirstPacketSeconds = 2

	statusResult := mgr.Execute(ctx, workspace.ToolRequest{Tool: "context_status", Limit: 20})
	smoke.Commands = append(smoke.Commands, CommandEvidence("p77-freshness-check", "freshness check", statusResult, 3))
	statusOutput := DecodeJSONObject(statusResult.Output)
	smoke.FreshnessCheck = FreshnessEvidence(statusResult, statusOutput)
	if smoke.FreshnessCheck.Status != "pass" {
		RecordQuickstartFailure(&smoke, "freshness", smoke.FreshnessCheck.BlockerReason, nil)
	}

	auditResult := mgr.Execute(ctx, workspace.ToolRequest{Tool: "harness_audit", Purpose: "behavior", Limit: 5})
	smoke.Commands = append(smoke.Commands, CommandEvidence("p77-harness-audit", "audit report", auditResult, 4))
	auditOutput := DecodeJSONObject(auditResult.Output)
	smoke.AuditReport = AuditEvidence(auditResult, auditOutput)
	if smoke.AuditReport.Status != "pass" {
		RecordQuickstartFailure(&smoke, "audit", smoke.AuditReport.BlockerReason, nil)
	}

	smoke.SuggestedVerification.SuggestedCommands = SuggestedVerificationCommands(packetOutput)
	if len(smoke.SuggestedVerification.SuggestedCommands) == 0 {
		smoke.SuggestedVerification.SuggestedCommands = []string{"go test ./..."}
	}
	for _, command := range smoke.SuggestedVerification.SuggestedCommands {
		smoke.SuggestedVerification.SuggestedLabels = append(smoke.SuggestedVerification.SuggestedLabels, VerificationLabel(command))
	}
	smoke.Commands = append(smoke.Commands, ActivationCommand{
		CommandID:      "p77-suggested-verification",
		Label:          "suggested verification",
		ExitStatus:     "not_run",
		ElapsedSeconds: 5,
	})

	smoke.Cleanup = QuickstartCleanupEvidence(StringField(packetOutput, "artifact_path"), opts.Cleanup, smoke.FirstUsefulPacket.PacketID, root, true)
	smoke.Commands = append(smoke.Commands, ActivationCommand{
		CommandID:      smoke.Cleanup.CommandID,
		Label:          "cleanup / clean-uninstall evidence",
		ExitStatus:     smoke.Cleanup.ExitStatus,
		ElapsedSeconds: 6,
	})
	if smoke.Cleanup.Status == "blocked" {
		RecordQuickstartFailure(&smoke, "cleanup", smoke.Cleanup.BlockerReason, nil)
	}
	smoke.TimeToTrustEvidenceSeconds = 6
	if smoke.Status == "pass" && (smoke.FirstUsefulPacket.Status != "pass" || smoke.FreshnessCheck.Status != "pass" || smoke.AuditReport.Status != "pass" || smoke.Cleanup.Status == "blocked") {
		RecordQuickstartFailure(&smoke, "insufficient-evidence", "quickstart evidence incomplete", nil)
	}
	return smoke
}

func (deps QuickstartDeps) withDefaults() QuickstartDeps {
	if deps.LoadConfig == nil {
		deps.LoadConfig = loadConfig
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.NewManager == nil {
		deps.NewManager = func(cfg config.Workspace) ToolExecutor {
			return workspace.NewManager(cfg)
		}
	}
	return deps
}

func loadConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			wd = config.CanonicalizeExistingPath(wd)
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}

func RecordQuickstartFailure(smoke *QuickstartSmoke, class string, reason string, err error) {
	if smoke.Status == "pass" {
		smoke.Status = "blocked"
	}
	if strings.TrimSpace(reason) == "" && err != nil {
		reason = err.Error()
	}
	reason = SafeText(firstNonEmpty(reason, "quickstart evidence unavailable"))
	smoke.FailureReason = ActivationFailureReason{
		Status:       "blocked",
		PrimaryClass: class,
		Reason:       reason,
		RetryAllowed: false,
	}
	smoke.DocsGap = ActivationDocsGap{
		Status:            "recorded",
		GapRecorded:       true,
		DocPath:           "README.md#quick-start",
		CommandLabel:      class,
		ExpectedOutput:    "machine-readable quickstart activation evidence",
		ActualOutput:      reason,
		Severity:          "medium",
		BlockedActivation: true,
	}
}

func CommandEvidence(commandID string, label string, result workspace.ToolResult, elapsedSeconds float64) ActivationCommand {
	status := "pass"
	errorClass := ""
	if strings.TrimSpace(result.Error) != "" {
		status = "blocked"
		errorClass = "tool_error"
	}
	return ActivationCommand{
		CommandID:      commandID,
		Label:          label,
		ExitStatus:     status,
		ElapsedSeconds: elapsedSeconds,
		ErrorClass:     errorClass,
	}
}

func PacketEvidence(result workspace.ToolResult, output map[string]any, task string) ActivationPacketEvidence {
	packet := ActivationPacketEvidence{
		Status:    "pass",
		CommandID: "p77-context-pack",
		Tool:      "context_pack",
		Task:      TaskEvidence(task),
		PacketID:  StringField(output, "pack_id"),
		IndexID:   StringField(output, "index_id"),
	}
	packet.CandidateFileCount = len(ArrayField(output, "candidate_files"))
	packet.CandidateSymbolCount = len(ArrayField(output, "candidate_symbols"))
	packet.CandidateTestCount = len(ArrayField(output, "tests"))
	packet.FreshnessState = NestedString(output, "stale_evidence", "state")
	if packet.FreshnessState == "" && BoolField(output, "fresh") {
		packet.FreshnessState = "fresh"
	}
	if packet.PacketID == "" || packet.IndexID == "" || packet.FreshnessState == "" {
		packet.Status = "blocked"
		packet.BlockerReason = "packet missing packet_id, index_id, or freshness state"
	}
	if strings.TrimSpace(result.Error) != "" {
		packet.Status = "blocked"
		packet.BlockerReason = SafeText(result.Error)
	}
	packet.UsefulEvidence = append(packet.UsefulEvidence,
		fmt.Sprintf("candidate_files:%d", packet.CandidateFileCount),
		fmt.Sprintf("candidate_symbols:%d", packet.CandidateSymbolCount),
		fmt.Sprintf("candidate_tests:%d", packet.CandidateTestCount),
	)
	if packet.CandidateFileCount == 0 && packet.CandidateSymbolCount == 0 && packet.CandidateTestCount == 0 {
		packet.Status = "blocked"
		packet.BlockerReason = "packet returned no ranked files, symbols, or tests"
	}
	return packet
}

func FreshnessEvidence(result workspace.ToolResult, output map[string]any) ActivationFreshnessEvidence {
	freshness := ActivationFreshnessEvidence{
		Status:         "pass",
		CommandID:      "p77-freshness-check",
		Tool:           "context_status",
		IndexID:        StringField(output, "index_id"),
		State:          StringField(output, "state"),
		FreshForScope:  BoolField(output, "fresh_for_scope"),
		GlobalFresh:    BoolField(output, "global_fresh"),
		StalePathCount: IntField(output, "stale_path_count"),
	}
	if freshness.State == "" && BoolField(output, "fresh") {
		freshness.State = "fresh"
	}
	if strings.TrimSpace(result.Error) != "" {
		freshness.Status = "blocked"
		freshness.BlockerReason = SafeText(result.Error)
		return freshness
	}
	if freshness.State == "" {
		freshness.Status = "blocked"
		freshness.BlockerReason = "freshness state unavailable"
	}
	return freshness
}

func AuditEvidence(result workspace.ToolResult, output map[string]any) ActivationAuditEvidence {
	audit := ActivationAuditEvidence{
		Status:        "pass",
		CommandID:     "p77-harness-audit",
		Tool:          "harness_audit",
		Purpose:       firstNonEmpty(StringField(output, "purpose"), "behavior"),
		AuditReportID: StableID(result.Output),
	}
	findings := ArrayField(output, "findings")
	audit.FindingCount = len(findings)
	for i, finding := range findings {
		item, ok := finding.(map[string]any)
		if !ok {
			continue
		}
		if id := StringField(item, "id"); id != "" && len(audit.AuditEventIDs) < 8 {
			audit.AuditEventIDs = append(audit.AuditEventIDs, StableID(id))
		} else if len(audit.AuditEventIDs) < 8 {
			audit.AuditEventIDs = append(audit.AuditEventIDs, fmt.Sprintf("finding_%d", i+1))
		}
		switch strings.ToLower(StringField(item, "severity")) {
		case "high":
			audit.HighFindingCount++
		case "medium":
			audit.MediumFindingCount++
		}
	}
	if strings.TrimSpace(result.Error) != "" {
		audit.Status = "blocked"
		audit.BlockerReason = SafeText(result.Error)
	}
	return audit
}

func SuggestedVerificationCommands(output map[string]any) []string {
	var commands []string
	for _, item := range ArrayField(output, "tests") {
		test, ok := item.(map[string]any)
		if !ok {
			continue
		}
		command := SafeCommand(StringField(test, "command"))
		if command == "" {
			continue
		}
		commands = append(commands, command)
		if len(commands) == 3 {
			break
		}
	}
	return commands
}

func VerificationLabel(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "verification"
	}
	if len(fields) >= 3 && fields[0] == "go" && fields[1] == "test" {
		return "go-test"
	}
	return strings.ReplaceAll(fields[0], "-", "_")
}

func QuickstartCleanupEvidence(artifactPath string, mode string, packetID string, root string, allowRemove bool) ActivationCleanupEvidence {
	cleanup := ActivationCleanupEvidence{
		Status:         "pass",
		CommandID:      "p77-cleanup",
		Mode:           firstNonEmpty(strings.TrimSpace(mode), "report-only"),
		ExitStatus:     "pass",
		RemainingState: "explicitly_retained_or_absent",
	}
	switch cleanup.Mode {
	case "report-only":
		cleanup.Status = "retained_by_policy"
		cleanup.ExitStatus = "not_run"
		cleanup.RetainedStatePolicy = "quickstart smoke records cleanup command evidence without deleting existing local repo memory"
		if packetID != "" {
			cleanup.EvidenceIDs = append(cleanup.EvidenceIDs, StableID(packetID))
		}
	case "remove-smoke-artifacts":
		if !allowRemove {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "cleanup requested before smoke artifact was created"
			return cleanup
		}
		if err := RemoveContextViewArtifact(root, artifactPath); err != nil {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = SafeText(err.Error())
			return cleanup
		}
		cleanup.RemovedArtifactCount = 1
		cleanup.RemainingState = "context_graph_retained_by_policy"
	case "remove-local-state":
		if !allowRemove {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "cleanup requested before local state was created"
			return cleanup
		}
		if strings.TrimSpace(root) == "" {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = "workspace root unavailable for local-state cleanup"
			return cleanup
		}
		if err := os.RemoveAll(filepath.Join(root, ".idiom", "comprehension")); err != nil {
			cleanup.Status = "blocked"
			cleanup.ExitStatus = "blocked"
			cleanup.BlockerReason = SafeText(err.Error())
			return cleanup
		}
		cleanup.RemovedArtifactCount = 1
		cleanup.RemainingState = "comprehension_state_absent"
		cleanup.EvidenceIDs = append(cleanup.EvidenceIDs, StableID(filepath.Join(root, ".idiom", "comprehension")))
	default:
		cleanup.Status = "blocked"
		cleanup.ExitStatus = "blocked"
		cleanup.BlockerReason = "unknown cleanup mode"
	}
	return cleanup
}

func RemoveContextViewArtifact(root string, artifactPath string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(artifactPath) == "" {
		return fmt.Errorf("context view artifact unavailable")
	}
	cleanRoot := filepath.Clean(root)
	cleanArtifact := filepath.Clean(artifactPath)
	if !filepath.IsAbs(cleanArtifact) {
		cleanArtifact = filepath.Join(cleanRoot, cleanArtifact)
	}
	rel, err := filepath.Rel(cleanRoot, cleanArtifact)
	if err != nil {
		return fmt.Errorf("context view artifact outside workspace")
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("context view artifact outside workspace")
	}
	relSlash := filepath.ToSlash(rel)
	if !strings.HasPrefix(relSlash, ".idiom/comprehension/views/") || !strings.HasSuffix(relSlash, ".json") {
		return fmt.Errorf("context view artifact not removable by quickstart cleanup")
	}
	if err := os.Remove(cleanArtifact); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func FinalizeQuickstartProductEvidence(smoke *QuickstartSmoke) {
	if smoke == nil {
		return
	}
	smoke.ProductEvidenceSafeOutput.ArtifactHash = ""
	smoke.ProductEvidenceSafeOutput.ExportID = ""
	body, err := quickstartProductEvidenceIdentityBody(*smoke)
	if err != nil {
		smoke.ProductEvidenceSafeOutput.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "blocked"
		return
	}
	forbidden := ForbiddenEvidenceLeak(string(body))
	if forbidden != "" {
		RedactForbiddenStrings(smoke)
		smoke.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "failed:" + forbidden
		smoke.PrivacyRedactionStatus.Status = "blocked"
		smoke.PrivacyRedactionStatus.ForbiddenFieldCheck = "failed:" + forbidden
		smoke.FailureReason = ActivationFailureReason{
			Status:       "blocked",
			PrimaryClass: "privacy_export",
			Reason:       "forbidden field detected in quickstart evidence",
			RetryAllowed: false,
		}
		finalizeQuickstartProductEvidenceIdentity(smoke)
		return
	}
	smoke.ProductEvidenceSafeOutput.Status = "pass"
	smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "pass"
	smoke.ProductEvidenceSafeOutput.RedactionStatus = ProductRedactionStatus(*smoke)
	smoke.PrivacyRedactionStatus.Status = "pass"
	smoke.PrivacyRedactionStatus.ForbiddenFieldCheck = "pass"
	finalizeQuickstartProductEvidenceIdentity(smoke)
}

func finalizeQuickstartProductEvidenceIdentity(smoke *QuickstartSmoke) {
	hash, err := quickstartProductEvidenceHash(*smoke)
	if err != nil {
		smoke.ProductEvidenceSafeOutput.Status = "blocked"
		smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck = "blocked"
		return
	}
	smoke.ProductEvidenceSafeOutput.ArtifactHash = hash
	smoke.ProductEvidenceSafeOutput.ExportID = "p77-activation-" + hash
}

func quickstartProductEvidenceHash(smoke QuickstartSmoke) (string, error) {
	body, err := quickstartProductEvidenceIdentityBody(smoke)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:8]), nil
}

func quickstartProductEvidenceIdentityBody(smoke QuickstartSmoke) ([]byte, error) {
	smoke.ProductEvidenceSafeOutput.ArtifactHash = ""
	smoke.ProductEvidenceSafeOutput.ExportID = ""
	return json.Marshal(smoke)
}

func DecodeJSONObject(raw string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}
