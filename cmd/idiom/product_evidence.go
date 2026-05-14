package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/comprehension"
	"idiom/internal/telemetry"
)

type productEvidenceExportReport struct {
	SchemaVersion       string                     `json:"schema_version"`
	GeneratedAt         string                     `json:"generated_at"`
	Status              string                     `json:"status"`
	ExportID            string                     `json:"export_id"`
	ArtifactHash        string                     `json:"artifact_hash"`
	ProjectRootID       string                     `json:"project_root_id,omitempty"`
	LaneHistory         productEvidenceLaneHistory `json:"lane_history"`
	Privacy             productEvidencePrivacy     `json:"privacy"`
	ProductSuccessGates productEvidenceGateSummary `json:"product_success_gates"`
	SignalCompleteness  p711SignalCompleteness     `json:"signal_completeness"`
	Signals             []p711SignalSummary        `json:"signals"`
	ClassCoverage       []p711ClassCoverage        `json:"class_coverage"`
	SourceReferences    []p711SourceReference      `json:"source_references"`
	ReleaseGateLinkage  []p711ReleaseGateLinkage   `json:"release_gate_linkage"`
	Limitations         []string                   `json:"limitations,omitempty"`
	SourceIDs           map[string]string          `json:"source_ids,omitempty"`
	Metadata            map[string]any             `json:"metadata,omitempty"`
}

type productEvidenceLaneHistory struct {
	Status                 string         `json:"status"`
	TotalRuns              int            `json:"total_runs"`
	ByMode                 map[string]int `json:"by_mode,omitempty"`
	ByFinalState           map[string]int `json:"by_final_state,omitempty"`
	ByExitCode             map[int]int    `json:"by_exit_code,omitempty"`
	EventsWithTokens       int            `json:"events_with_tokens,omitempty"`
	EventsWithRunMetrics   int            `json:"events_with_run_metrics,omitempty"`
	TotalModelCalls        int            `json:"total_model_calls,omitempty"`
	TotalToolCalls         int            `json:"total_tool_calls,omitempty"`
	TotalTokens            int            `json:"total_tokens,omitempty"`
	TotalDurationMillis    int            `json:"total_duration_ms,omitempty"`
	PromptBodiesIncluded   bool           `json:"prompt_bodies_included"`
	TranscriptBodiesLoaded bool           `json:"transcript_bodies_loaded"`
}

type productEvidencePrivacy struct {
	RedactionStatus        string   `json:"redaction_status"`
	ForbiddenFieldCheck    string   `json:"forbidden_field_check"`
	MetadataOnly           bool     `json:"metadata_only"`
	RawPromptCaptured      bool     `json:"raw_prompt_captured"`
	RawFileBodyCaptured    bool     `json:"raw_file_body_captured"`
	SecretCaptured         bool     `json:"secret_captured"`
	PrivatePathCaptured    bool     `json:"private_path_captured"`
	HostedTelemetryDefault bool     `json:"hosted_telemetry_default"`
	RedactedFields         []string `json:"redacted_fields,omitempty"`
}

type productEvidenceGateSummary struct {
	Activation                 string `json:"activation"`
	RetainedUse                string `json:"retained_use"`
	IntegrationProof           string `json:"integration_proof"`
	Benchmark                  string `json:"benchmark"`
	Audit                      string `json:"audit"`
	Learning                   string `json:"learning"`
	SecurityPrivacy            string `json:"security_privacy"`
	LocalProductEvidenceExport string `json:"local_product_evidence_export"`
}

type productEvidenceExportOptions struct {
	ProjectRoot       string
	OutputPath        string
	LocalOpsReport    string
	ValidationRecords string
}

type p711SignalCompleteness struct {
	Status        string   `json:"status"`
	Required      []string `json:"required"`
	Present       []string `json:"present"`
	Partial       []string `json:"partial,omitempty"`
	NotAvailable  []string `json:"not_available,omitempty"`
	Blocked       []string `json:"blocked,omitempty"`
	CompleteCount int      `json:"complete_count"`
	RequiredCount int      `json:"required_count"`
}

type p711SignalSummary struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	Completeness    string         `json:"completeness"`
	SourceRefs      []string       `json:"source_refs,omitempty"`
	EvidenceIDs     []string       `json:"evidence_ids,omitempty"`
	Counts          map[string]int `json:"counts,omitempty"`
	RedactionStatus string         `json:"redaction_status"`
	ReleaseGates    []string       `json:"release_gates,omitempty"`
	Limitations     []string       `json:"limitations,omitempty"`
}

type p711ClassCoverage struct {
	Classification          string   `json:"classification"`
	Status                  string   `json:"status"`
	Stance                  string   `json:"stance"`
	P76CommandID            string   `json:"p7_6_command_id,omitempty"`
	ClientDemoTranscriptID  string   `json:"client_demo_transcript_id,omitempty"`
	SecurityPostureCoverage bool     `json:"security_posture_coverage"`
	IntegrationProofIDs     []string `json:"integration_proof_ids,omitempty"`
	ValidationRecordCount   int      `json:"validation_record_count"`
	Limitations             []string `json:"limitations,omitempty"`
}

type p711SourceReference struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	PathID       string `json:"path_id,omitempty"`
	ArtifactHash string `json:"artifact_hash,omitempty"`
}

type p711ReleaseGateLinkage struct {
	Gate        string         `json:"gate"`
	Status      string         `json:"status"`
	Evidence    string         `json:"evidence"`
	ClaimImpact string         `json:"claim_impact"`
	Counts      map[string]int `json:"counts,omitempty"`
	Blockers    []string       `json:"blockers,omitempty"`
	EvidenceIDs []string       `json:"evidence_ids,omitempty"`
}

type p99ProductPrivacyState struct {
	ExportStatus            string
	RedactionStatus         string
	ForbiddenFieldCheck     string
	RawPromptCaptured       bool
	RawFileBodyCaptured     bool
	SecretCaptured          bool
	PrivatePathCaptured     bool
	ValidationRecordsStatus string
}

func runProductEvidenceMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 {
		printProductEvidenceUsage(stderr)
		return 2
	}
	switch args[0] {
	case "export":
		return runProductEvidenceExport(args[1:], stdout, stderr, rec)
	case "-h", "--help", "help":
		printProductEvidenceUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "product-evidence: unknown subcommand %q\n", args[0])
		printProductEvidenceUsage(stderr)
		return 2
	}
}

func runProductEvidenceExport(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom product-evidence export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := productEvidenceExportOptions{}
	fs.StringVar(&opts.ProjectRoot, "project-root", ".", "workspace root whose local evidence should be summarized")
	fs.StringVar(&opts.OutputPath, "output", "", "optional path to write the JSON export")
	fs.StringVar(&opts.LocalOpsReport, "local-ops-report", "", "optional local-ops benchmark JSON report to fold into the export")
	fs.StringVar(&opts.ValidationRecords, "validation-records", "", "optional privacy-safe P0.8 validation records JSON or JSONL")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	root, err := filepath.Abs(opts.ProjectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "product-evidence export: resolve project root: %v\n", err)
		return 1
	}
	summary, err := summarizeLaneHistory(root)
	if err != nil {
		fmt.Fprintf(stderr, "product-evidence export: summarize lane history: %v\n", err)
		return 1
	}
	report := buildProductEvidenceExportReport(root, summary, opts)
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "product-evidence export: marshal report: %v\n", err)
		return 1
	}
	if forbidden := localOpsForbiddenEvidenceLeak(string(body)); forbidden != "" {
		report.Status = "blocked"
		report.Privacy.RedactionStatus = "blocked"
		report.Privacy.ForbiddenFieldCheck = "failed:" + forbidden
		refreshProductEvidenceP99Gate(root, &report)
		finalizeProductEvidenceIdentity(&report)
		body, err = json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "product-evidence export: marshal blocked report: %v\n", err)
			return 1
		}
	}
	body = append(body, '\n')
	if strings.TrimSpace(opts.OutputPath) != "" {
		if err := os.MkdirAll(filepath.Dir(filepath.Clean(opts.OutputPath)), 0o755); err != nil {
			fmt.Fprintf(stderr, "product-evidence export: create output directory: %v\n", err)
			return 1
		}
		if err := os.WriteFile(opts.OutputPath, body, 0o644); err != nil {
			fmt.Fprintf(stderr, "product-evidence export: write output: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "product-evidence export wrote output_id:%s\n", localOpsStableID(opts.OutputPath))
	} else {
		_, _ = stdout.Write(body)
	}
	logCLIPhase(rec, "product_evidence_export", map[string]any{
		"status":                        report.Status,
		"export_id":                     report.ExportID,
		"artifact_hash":                 report.ArtifactHash,
		"lane_history_total":            report.LaneHistory.TotalRuns,
		"privacy_redaction_status":      report.Privacy.RedactionStatus,
		"privacy_forbidden_field_check": report.Privacy.ForbiddenFieldCheck,
		"local_product_evidence_export": report.ProductSuccessGates.LocalProductEvidenceExport,
		"hosted_telemetry_default":      report.Privacy.HostedTelemetryDefault,
		"raw_prompt_captured":           report.Privacy.RawPromptCaptured,
		"raw_file_body_captured":        report.Privacy.RawFileBodyCaptured,
		"transcript_bodies_loaded":      report.LaneHistory.TranscriptBodiesLoaded,
		"lane_history_prompt_included":  report.LaneHistory.PromptBodiesIncluded,
	})
	return 0
}

func buildProductEvidenceExportReport(root string, summary laneHistorySummary, opts productEvidenceExportOptions) productEvidenceExportReport {
	laneStatus := "not_available"
	if summary.Total > 0 {
		laneStatus = "available"
	}
	localOps, localOpsRef := loadP711LocalOpsReport(root, opts.LocalOpsReport)
	integration, integrationRef := loadP711IntegrationProofs(root)
	security, securityRef := loadP711SecurityPosture(root)
	demos, demosRef := loadP711ClientDemoTranscripts(root)
	records, recordsRef := loadP711ValidationRecords(opts.ValidationRecords)
	learning, learningRef := loadP711LearningSummary(root)
	signals := buildP711Signals(localOps, records, integration, security, learning)
	classCoverage := buildP711ClassCoverage(demos, security, integration, records)
	completeness := summarizeP711Signals(signals)
	status := p711ExportStatus(completeness)
	privacy := productEvidencePrivacy{
		RedactionStatus:        "redacted",
		ForbiddenFieldCheck:    "pass",
		MetadataOnly:           true,
		RawPromptCaptured:      false,
		RawFileBodyCaptured:    false,
		SecretCaptured:         false,
		PrivatePathCaptured:    false,
		HostedTelemetryDefault: false,
		RedactedFields:         []string{"project_root", "lane_history_path", "output_path"},
	}
	if recordsRef.Status == "blocked" {
		status = "blocked"
		privacy.RedactionStatus = "blocked"
		privacy.ForbiddenFieldCheck = "failed:validation_records"
	}
	p99 := p99SecurityPrivacyGate(root, p99ProductPrivacyState{
		ExportStatus:            status,
		RedactionStatus:         privacy.RedactionStatus,
		ForbiddenFieldCheck:     privacy.ForbiddenFieldCheck,
		RawPromptCaptured:       privacy.RawPromptCaptured,
		RawFileBodyCaptured:     privacy.RawFileBodyCaptured,
		SecretCaptured:          privacy.SecretCaptured,
		PrivatePathCaptured:     privacy.PrivatePathCaptured,
		ValidationRecordsStatus: recordsRef.Status,
	})
	report := productEvidenceExportReport{
		SchemaVersion: "idiom.product_evidence_export.v1",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Status:        status,
		ProjectRootID: "root_id:" + localOpsStableID(root),
		SourceIDs:     map[string]string{"lane_history": "artifact_id:" + localOpsStableID(filepath.Join(root, laneHistoryRelPath))},
		LaneHistory: productEvidenceLaneHistory{
			Status:                 laneStatus,
			TotalRuns:              summary.Total,
			ByMode:                 summary.ByMode,
			ByFinalState:           summary.ByFinalState,
			ByExitCode:             summary.ByExitCode,
			EventsWithTokens:       summary.EventsWithTokens,
			EventsWithRunMetrics:   summary.EventsWithRunMetrics,
			TotalModelCalls:        summary.TotalModelCalls,
			TotalToolCalls:         summary.TotalToolCalls,
			TotalTokens:            summary.TotalTokens,
			TotalDurationMillis:    summary.TotalDurationMS,
			PromptBodiesIncluded:   false,
			TranscriptBodiesLoaded: false,
		},
		Privacy: privacy,
		ProductSuccessGates: productEvidenceGateSummary{
			Activation:                 p711GateFromSignal(signals, "activation"),
			RetainedUse:                p711GateFromSignal(signals, "retained_use"),
			IntegrationProof:           p711GateFromSignal(signals, "integration_proof"),
			Benchmark:                  p711GateFromSignal(signals, "benchmark"),
			Audit:                      p711GateFromSignal(signals, "audit"),
			Learning:                   p711GateFromSignal(signals, "learning"),
			SecurityPrivacy:            p99.Status,
			LocalProductEvidenceExport: "pass",
		},
		SignalCompleteness: completeness,
		Signals:            signals,
		ClassCoverage:      classCoverage,
		SourceReferences: []p711SourceReference{
			localOpsRef,
			integrationRef,
			securityRef,
			demosRef,
			recordsRef,
			learningRef,
		},
		ReleaseGateLinkage: buildP711ReleaseGateLinkage(signals, classCoverage, localOps, records, p99),
		Limitations:        productEvidenceExportLimitations(integration),
		Metadata: map[string]any{
			"generated_by": "idiom product-evidence export",
		},
	}
	finalizeProductEvidenceIdentity(&report)
	return report
}

func productEvidenceAvailabilityGate(available bool) string {
	if available {
		return "partial"
	}
	return "not_available"
}

func productEvidenceExportLimitations(integration p711IntegrationProofSet) []string {
	limitations := []string{
		"export summarizes bounded local metadata only",
		"prompt bodies, file bodies, and transcript bodies are not loaded",
		"retained-use and adoption signals are not claimed unless validation records are supplied",
	}
	if p711MCPStdioParityPresent(integration) {
		return append(limitations, "MCP resources are metadata-only; resource file bodies are not exported")
	}
	return append(limitations, "MCP stdio parity remains P8.6 scope when only the P7.8 placeholder is present")
}

func p711MCPStdioParityPresent(integration p711IntegrationProofSet) bool {
	validity := p711ProofValidityFor(integration)
	for _, proof := range integration.Proofs {
		if p711MCPStdioProofHasParity(proof, validity) {
			return true
		}
	}
	return false
}

const p711ValidatedIntegrationProofStatus = "test_validated"

var p711RequiredIntegrationPathTypes = []string{
	"shell_ci_script",
	"external_coding_agent_workflow",
	"mcp_stdio",
}

var p711RequiredIntegrationPrivacyFields = []string{
	"raw_prompts",
	"file_bodies",
	"private_paths",
	"credentials",
	"private_repo_or_customer_ids",
}

var p98RequiredMCPParityReadinessFields = []string{
	"protocol_implemented",
	"initialize_present",
	"tools_list_present",
	"allowed_tools_call_present",
	"denied_tools_call_present",
	"roots_resources_metadata_present",
	"caller_metadata_present",
	"transport_attribution_present",
	"protected_resource_denial_present",
	"redaction_denial_present",
	"report_only_freshness_present",
	"product_evidence_export_parity",
}

type p711IntegrationProofSet struct {
	SchemaVersion string                 `json:"schema_version"`
	Name          string                 `json:"name"`
	Proofs        []p711IntegrationProof `json:"proofs"`
}

type p711IntegrationProofPrivacy struct {
	RawPrompts               bool `json:"raw_prompts"`
	FileBodies               bool `json:"file_bodies"`
	PrivatePaths             bool `json:"private_paths"`
	Credentials              bool `json:"credentials"`
	PrivateRepoOrCustomerIDs bool `json:"private_repo_or_customer_ids"`
}

type p711IntegrationProofPrivacySemantics struct {
	objectPresent bool
	malformed     bool
	fieldsPresent map[string]bool
}

type p711IntegrationProof struct {
	ID                          string                      `json:"id"`
	Path                        string                      `json:"path"`
	Status                      string                      `json:"status"`
	ReplayFixture               string                      `json:"replay_fixture,omitempty"`
	ProductEvidenceExportParity bool                        `json:"product_evidence_export_parity,omitempty"`
	Readiness                   map[string]bool             `json:"readiness"`
	Privacy                     p711IntegrationProofPrivacy `json:"privacy"`
	privacySemantics            p711IntegrationProofPrivacySemantics
}

func (proof *p711IntegrationProof) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID                          string          `json:"id"`
		Path                        string          `json:"path"`
		Status                      string          `json:"status"`
		ReplayFixture               string          `json:"replay_fixture,omitempty"`
		ProductEvidenceExportParity bool            `json:"product_evidence_export_parity,omitempty"`
		Readiness                   map[string]bool `json:"readiness"`
		Privacy                     json.RawMessage `json:"privacy"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	proof.ID = raw.ID
	proof.Path = raw.Path
	proof.Status = raw.Status
	proof.ReplayFixture = raw.ReplayFixture
	proof.ProductEvidenceExportParity = raw.ProductEvidenceExportParity
	proof.Readiness = raw.Readiness
	proof.Privacy, proof.privacySemantics = p711IntegrationProofPrivacyFromRaw(raw.Privacy)
	return nil
}

func p711IntegrationProofPrivacyFromRaw(raw json.RawMessage) (p711IntegrationProofPrivacy, p711IntegrationProofPrivacySemantics) {
	semantics := p711IntegrationProofPrivacySemantics{fieldsPresent: map[string]bool{}}
	var privacy p711IntegrationProofPrivacy
	if len(raw) == 0 {
		return privacy, semantics
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		semantics.malformed = true
		return privacy, semantics
	}
	semantics.objectPresent = true
	for _, field := range p711RequiredIntegrationPrivacyFields {
		value, ok := fields[field]
		if !ok {
			continue
		}
		semantics.fieldsPresent[field] = true
		var decoded bool
		if err := json.Unmarshal(value, &decoded); err != nil {
			semantics.malformed = true
			continue
		}
		p711SetIntegrationProofPrivacyField(&privacy, field, decoded)
	}
	return privacy, semantics
}

func p711SetIntegrationProofPrivacyField(privacy *p711IntegrationProofPrivacy, field string, value bool) {
	switch field {
	case "raw_prompts":
		privacy.RawPrompts = value
	case "file_bodies":
		privacy.FileBodies = value
	case "private_paths":
		privacy.PrivatePaths = value
	case "credentials":
		privacy.Credentials = value
	case "private_repo_or_customer_ids":
		privacy.PrivateRepoOrCustomerIDs = value
	}
}

type p711SecurityPosture struct {
	SchemaVersion                string                 `json:"schema_version"`
	ArtifactID                   string                 `json:"artifact_id"`
	RequiredControls             []p99PostureControl    `json:"required_controls"`
	ClientClassificationCoverage []p711SecurityClassRow `json:"client_classification_coverage"`
	ForbiddenEvidenceFields      []string               `json:"forbidden_evidence_fields"`
	ReleaseGateTiebacks          []string               `json:"release_gate_tiebacks"`
}

type p711SecurityClassRow struct {
	Classification    string   `json:"classification"`
	Stance            string   `json:"stance"`
	P76CommandID      string   `json:"p7_6_command_id"`
	CoveredControlIDs []string `json:"covered_control_ids"`
	Limitation        string   `json:"limitation"`
}

type p99PostureControl struct {
	ID             string               `json:"id"`
	Claim          string               `json:"claim"`
	MVPLimitations string               `json:"mvp_limitations"`
	Evidence       []p99PostureEvidence `json:"evidence"`
}

type p99PostureEvidence struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Path    string `json:"path"`
	CheckID string `json:"check_id"`
}

type p711DemoTranscriptSet struct {
	Transcripts []p711DemoTranscript `json:"transcripts"`
}

type p711DemoTranscript struct {
	ID             string `json:"id"`
	Classification string `json:"classification"`
	Stance         string `json:"stance"`
	P76CommandID   string `json:"p7_6_command_id"`
	MVPLimitation  string `json:"mvp_limitation"`
}

type p711ValidationRecord struct {
	RecordID                  string `json:"record_id"`
	ClientClassification      string `json:"client_classification"`
	EvidenceStatus            string `json:"evidence_status"`
	RetainedUseStatus         string `json:"retained_use_status"`
	AdoptionSignalType        string `json:"adoption_signal_type"`
	RedactionStatus           string `json:"redaction_status"`
	ParticipantContextID      string `json:"participant_context_id,omitempty"`
	PrimaryICPFit             string `json:"primary_icp_fit,omitempty"`
	ActiveAgentUse            string `json:"active_agent_use,omitempty"`
	RepoWorkflowAvailable     string `json:"repo_workflow_available,omitempty"`
	BlockedWorkflowStatus     string `json:"blocked_workflow_status,omitempty"`
	DailyUserActivationStatus string `json:"daily_user_activation_status,omitempty"`
	RetainedUseEvidence       string `json:"retained_use_evidence,omitempty"`
	SubstrateValueStatus      string `json:"substrate_value_status,omitempty"`
	BuyerBudgetSignal         string `json:"buyer_budget_signal,omitempty"`
	DisqualifierStatus        string `json:"disqualifier_status,omitempty"`
	ActivationScore           int    `json:"activation_score,omitempty"`
	TrustSecurityScore        int    `json:"trust_security_score,omitempty"`
	WorkflowValueScore        int    `json:"workflow_value_score,omitempty"`
	AdoptPaySponsorScore      int    `json:"adopt_pay_sponsor_score,omitempty"`
}

func loadP711LocalOpsReport(root string, path string) (*localOpsBenchmarkReport, p711SourceReference) {
	ref := p711SourceReference{ID: "local_ops_benchmark", Kind: "local_ops", Status: "not_available"}
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(root, ".idiom", "local-ops", "report.json")
	}
	ref.PathID = localOpsPathEvidence(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ref
	}
	ref.ArtifactHash = p711Hash(data)
	var report localOpsBenchmarkReport
	if err := json.Unmarshal(data, &report); err != nil {
		ref.Status = "blocked"
		return nil, ref
	}
	ref.Status = "present"
	return &report, ref
}

func loadP711IntegrationProofs(root string) (p711IntegrationProofSet, p711SourceReference) {
	path := filepath.Join(root, "testdata", "sidecar", "integration-proofs.json")
	ref := p711FileRef("p7.8_integration_proofs", "integration_proofs", path)
	var out p711IntegrationProofSet
	if p711ReadJSON(path, &out) != nil {
		return out, ref
	}
	ref.Status = "present"
	return out, ref
}

func loadP711SecurityPosture(root string) (p711SecurityPosture, p711SourceReference) {
	path := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	ref := p711FileRef("p7.9_security_privacy_posture", "security_privacy", path)
	var out p711SecurityPosture
	if p711ReadJSON(path, &out) != nil {
		return out, ref
	}
	ref.Status = "present"
	if out.ArtifactID != "" {
		ref.ID = out.ArtifactID
	}
	return out, ref
}

func loadP711ClientDemoTranscripts(root string) (p711DemoTranscriptSet, p711SourceReference) {
	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")
	ref := p711FileRef("p7.10_client_demo_transcripts", "client_demo_transcripts", path)
	var out p711DemoTranscriptSet
	if p711ReadJSON(path, &out) != nil {
		return out, ref
	}
	ref.Status = "present"
	return out, ref
}

func loadP711ValidationRecords(path string) ([]p711ValidationRecord, p711SourceReference) {
	ref := p711SourceReference{ID: "p0.8_validation_records", Kind: "validation_records", Status: "not_available"}
	if strings.TrimSpace(path) == "" {
		return nil, ref
	}
	ref.PathID = localOpsPathEvidence(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ref
	}
	ref.ArtifactHash = p711Hash(data)
	if forbidden := p711ForbiddenValidationEvidence(data); forbidden != "" {
		ref.Status = "blocked"
		return nil, ref
	}
	records, err := decodeP711ValidationRecords(data)
	if err != nil {
		ref.Status = "blocked"
		return nil, ref
	}
	ref.Status = "present"
	for i := range records {
		records[i].RecordID = p711SafeID(records[i].RecordID, "record")
		records[i].ClientClassification = p711ApprovedClassification(records[i].ClientClassification)
		records[i].EvidenceStatus = p711SafeStatus(records[i].EvidenceStatus)
		records[i].RetainedUseStatus = p711SafeStatus(records[i].RetainedUseStatus)
		records[i].AdoptionSignalType = p711SafeToken(records[i].AdoptionSignalType)
		records[i].RedactionStatus = p711SafeStatus(records[i].RedactionStatus)
		records[i].ParticipantContextID = p711SafeID(records[i].ParticipantContextID, "context")
		records[i].PrimaryICPFit = p711SafeToken(records[i].PrimaryICPFit)
		records[i].ActiveAgentUse = p711SafeToken(records[i].ActiveAgentUse)
		records[i].RepoWorkflowAvailable = p711SafeToken(records[i].RepoWorkflowAvailable)
		records[i].BlockedWorkflowStatus = p711SafeToken(records[i].BlockedWorkflowStatus)
		records[i].DailyUserActivationStatus = p711SafeToken(records[i].DailyUserActivationStatus)
		records[i].RetainedUseEvidence = p711SafeToken(records[i].RetainedUseEvidence)
		records[i].SubstrateValueStatus = p711SafeToken(records[i].SubstrateValueStatus)
		records[i].BuyerBudgetSignal = p711SafeToken(records[i].BuyerBudgetSignal)
		records[i].DisqualifierStatus = p711SafeToken(records[i].DisqualifierStatus)
	}
	return records, ref
}

func loadP711LearningSummary(root string) (*comprehension.LearningLogSummary, p711SourceReference) {
	path := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")
	ref := p711SourceReference{ID: "ranker_learning_log", Kind: "learning", Status: "not_available", PathID: localOpsPathEvidence(path)}
	entries, err := comprehension.ReadLearningLog(root)
	if err != nil || len(entries) == 0 {
		return nil, ref
	}
	summary := comprehension.SummarizeLearningLog(entries)
	ref.Status = "present"
	ref.ArtifactHash = localOpsStableID(fmt.Sprintf("%d:%d:%d", summary.TotalEntries, summary.AcceptedCount, summary.RejectedCount))
	return &summary, ref
}

func buildP711Signals(localOps *localOpsBenchmarkReport, records []p711ValidationRecord, integration p711IntegrationProofSet, security p711SecurityPosture, learning *comprehension.LearningLogSummary) []p711SignalSummary {
	return []p711SignalSummary{
		p711ActivationSignal(localOps),
		p711RetainedUseSignal(records),
		p711IntegrationSignal(integration),
		p711BenchmarkSignal(localOps),
		p711AuditSignal(localOps, security),
		p711LearningSignal(learning),
	}
}

func p711ActivationSignal(localOps *localOpsBenchmarkReport) p711SignalSummary {
	s := p711SignalSummary{ID: "activation", Status: "not_available", Completeness: "missing", SourceRefs: []string{"local_ops_benchmark"}, RedactionStatus: "redacted", ReleaseGates: []string{"P0.10", "P7.7", "P7.16", "P9.6"}, Counts: map[string]int{}}
	if localOps == nil || localOps.Quickstart == nil {
		s.Limitations = []string{"quickstart activation smoke not supplied"}
		return s
	}
	q := localOps.Quickstart
	s.Status = q.Status
	s.Completeness = p711Completeness(q.Status)
	s.EvidenceIDs = p711Compact(q.SmokeID, q.ProductEvidenceSafeOutput.ExportID, q.FirstUsefulPacket.PacketID, q.FirstUsefulPacket.IndexID)
	s.Counts["candidate_files"] = q.FirstUsefulPacket.CandidateFileCount
	s.Counts["candidate_symbols"] = q.FirstUsefulPacket.CandidateSymbolCount
	s.Counts["candidate_tests"] = q.FirstUsefulPacket.CandidateTestCount
	s.Counts["useful_evidence_count"] = localOpsActivationPacketUsefulEvidenceCount(q.FirstUsefulPacket)
	if !localOpsActivationPacketHasUsefulEvidence(q.FirstUsefulPacket) {
		s.Status = "partial"
		s.Completeness = "partial"
		s.Limitations = []string{"first useful packet evidence has no ranked files, symbols, tests, or equivalent useful-evidence counts"}
	}
	if q.ProductEvidenceSafeOutput.ForbiddenFieldCheck != "pass" {
		s.Status = "blocked"
		s.Completeness = "blocked"
	}
	return s
}

func localOpsActivationPacketHasUsefulEvidence(packet localOpsActivationPacketEvidence) bool {
	return localOpsActivationPacketUsefulEvidenceCount(packet) > 0
}

func localOpsActivationPacketUsefulEvidenceCount(packet localOpsActivationPacketEvidence) int {
	total := 0
	for _, count := range []int{packet.CandidateFileCount, packet.CandidateSymbolCount, packet.CandidateTestCount} {
		if count > 0 {
			total += count
		}
	}
	for _, evidence := range packet.UsefulEvidence {
		if count, ok := localOpsUsefulEvidencePositiveCount(evidence); ok {
			total += count
		}
	}
	return total
}

func localOpsUsefulEvidencePositiveCount(evidence string) (int, bool) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return 0, false
	}
	for _, separator := range []string{":", "="} {
		_, rawCount, ok := strings.Cut(evidence, separator)
		if !ok {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(rawCount))
		return count, err == nil && count > 0
	}
	return 0, false
}

func p711RetainedUseSignal(records []p711ValidationRecord) p711SignalSummary {
	s := p711SignalSummary{ID: "retained_use", Status: "not_available", Completeness: "missing", SourceRefs: []string{"p0.8_validation_records"}, RedactionStatus: "redacted", ReleaseGates: []string{"P0.8", "P7.11", "P9.7"}, Counts: map[string]int{"validation_records": len(records), "qualifying_records": 0}}
	for _, record := range records {
		if !p711ValidationRecordQualifies(record) {
			s.Counts["non_qualifying_records"]++
			continue
		}
		s.Counts["qualifying_records"]++
		if record.RecordID != "" {
			s.EvidenceIDs = append(s.EvidenceIDs, record.RecordID)
		}
		if p97RecordHasRetainedSubstrateUse(record) {
			s.Counts["retained_use_observed"]++
		}
		if p711HasAdoptionSignal(record.AdoptionSignalType) {
			s.Counts["adoption_signals"]++
		}
	}
	if s.Counts["retained_use_observed"] > 0 {
		s.Status = "pass"
		s.Completeness = "complete"
	} else if s.Counts["qualifying_records"] > 0 {
		s.Status = "partial"
		s.Completeness = "partial"
	} else if len(records) > 0 {
		s.Limitations = []string{"validation records supplied but none qualified for retained-use counting"}
	}
	return s
}

type p97ValidationGateResult struct {
	Status      string
	Evidence    string
	ClaimImpact string
	Counts      map[string]int
	EvidenceIDs []string
	Blockers    []string
}

type p98IntegrationGateResult struct {
	Status      string
	Evidence    string
	ClaimImpact string
	Counts      map[string]int
	EvidenceIDs []string
	Blockers    []string
}

func p97ValidationGate(records []p711ValidationRecord) p97ValidationGateResult {
	out := p97ValidationGateResult{
		Status:      "needs_evidence",
		Evidence:    "privacy-safe P0.8 validation records missing",
		ClaimImpact: "paid or internal-platform demand remains unclaimed until primary-ICP retained-use and adoption evidence pass",
		Counts: map[string]int{
			"validation_records":                len(records),
			"complete_records":                  0,
			"primary_icp_records":               0,
			"primary_contexts":                  0,
			"primary_scored_records":            0,
			"daily_user_activation_records":     0,
			"retained_use_records":              0,
			"credible_primary_adoption_signals": 0,
			"open_source_records":               0,
			"non_qualifying_records":            0,
		},
	}
	contexts := map[string]bool{}
	for _, record := range records {
		if record.ClientClassification == "open-source" {
			out.Counts["open_source_records"]++
		}
		if !p711ValidationRecordQualifies(record) {
			out.Counts["non_qualifying_records"]++
			switch record.EvidenceStatus {
			case "disqualified":
				out.Counts["disqualified_records"]++
			case "withdrawn":
				out.Counts["withdrawn_records"]++
			case "draft":
				out.Counts["draft_records"]++
			}
			continue
		}
		out.Counts["complete_records"]++
		if p97RecordHasRetainedSubstrateUse(record) {
			out.Counts["retained_use_records"]++
		}
		if !p97PrimaryICPRecord(record) {
			continue
		}
		out.Counts["primary_icp_records"]++
		out.EvidenceIDs = append(out.EvidenceIDs, record.RecordID)
		if record.ParticipantContextID != "" {
			contexts[record.ParticipantContextID] = true
		}
		if p97PrimaryScoresPass(record) {
			out.Counts["primary_scored_records"]++
		}
		if p97DailyUserActivationQualifies(record.DailyUserActivationStatus) {
			out.Counts["daily_user_activation_records"]++
		}
		if p97CrediblePrimaryAdoptionSignal(record) {
			out.Counts["credible_primary_adoption_signals"]++
		}
	}
	out.Counts["primary_contexts"] = len(contexts)

	var blockers []string
	if out.Counts["complete_records"] < 5 {
		blockers = append(blockers, "fewer than five complete privacy-safe P0.8 validation records")
	}
	if out.Counts["primary_icp_records"] < 3 {
		blockers = append(blockers, "fewer than three complete primary enterprise-saas records")
	}
	if out.Counts["primary_contexts"] < 2 {
		blockers = append(blockers, "primary records do not span at least two contexts")
	}
	if out.Counts["primary_scored_records"] < 2 {
		blockers = append(blockers, "fewer than two primary records meet activation, trust/security, and workflow-value score thresholds")
	}
	if out.Counts["daily_user_activation_records"] < 2 {
		blockers = append(blockers, "fewer than two primary records include daily-user activation attempts")
	}
	if out.Counts["retained_use_records"] < 1 {
		blockers = append(blockers, "retained use is missing or not tied to substrate value")
	}
	if out.Counts["credible_primary_adoption_signals"] < 1 {
		blockers = append(blockers, "credible paid, budget-owner, or internal-platform adoption signal is missing")
	}
	if out.Counts["open_source_records"] > 0 && out.Counts["primary_icp_records"] < 3 {
		blockers = append(blockers, "open-source/supporting records do not replace primary enterprise-saas demand")
	}

	out.Blockers = blockers
	if len(blockers) == 0 {
		out.Status = "evidence_present"
		out.Evidence = p97EvidenceSummary(out.Counts)
		out.ClaimImpact = "P9.7 can cite primary-ICP design-partner validation, retained use, and credible paid/internal-platform adoption evidence"
		return out
	}
	out.Evidence = p97EvidenceSummary(out.Counts) + "; blockers:" + strings.Join(blockers, "|")
	return out
}

func p97EvidenceSummary(counts map[string]int) string {
	return fmt.Sprintf(
		"records:%d complete:%d primary:%d contexts:%d retained:%d adoption:%d",
		counts["validation_records"],
		counts["complete_records"],
		counts["primary_icp_records"],
		counts["primary_contexts"],
		counts["retained_use_records"],
		counts["credible_primary_adoption_signals"],
	)
}

func p97PrimaryICPRecord(record p711ValidationRecord) bool {
	return record.ClientClassification == "enterprise-saas" &&
		p711ValidationRecordQualifies(record) &&
		p97TokenIsYes(record.PrimaryICPFit) &&
		p97TokenIsYes(record.ActiveAgentUse) &&
		p97RepoWorkflowQualifies(record.RepoWorkflowAvailable) &&
		p97TokenIsPresent(record.BlockedWorkflowStatus) &&
		!p97RecordDisqualified(record)
}

func p97PrimaryScoresPass(record p711ValidationRecord) bool {
	return record.ActivationScore >= 2 &&
		record.TrustSecurityScore >= 2 &&
		record.WorkflowValueScore >= 2
}

func p97RecordHasRetainedSubstrateUse(record p711ValidationRecord) bool {
	return p711ValidationRecordQualifies(record) &&
		(record.RetainedUseStatus == "observed" || record.RetainedUseStatus == "pass") &&
		p97RetainedSubstrateToken(record.RetainedUseEvidence) &&
		p97RetainedSubstrateToken(record.SubstrateValueStatus)
}

func p97RetainedSubstrateToken(value string) bool {
	switch p711SafeToken(value) {
	case "saved_packet", "saved_packets", "saved_packet_reused", "packet_reused", "packet_reuse",
		"freshness", "freshness_reused", "freshness_check", "freshness_value",
		"audit", "audit_reused", "audit_report", "audit_history",
		"verification", "suggested_verification", "verification_reused", "verification_review_reused",
		"integration", "integration_transcript", "integration_transcript_reused", "external_agent_handoff",
		"evidence_export", "product_evidence_export", "release_evidence_export", "export_reused",
		"rediscovery_value", "rediscovery_reduction", "review_value", "review_ambiguity_reduction",
		"substrate_value", "substrate_evidence":
		return true
	default:
		return false
	}
}

func p97DailyUserActivationQualifies(value string) bool {
	switch p711SafeToken(value) {
	case "pass", "observed", "attempted", "completed", "succeeded":
		return true
	default:
		return false
	}
}

func p97CrediblePrimaryAdoptionSignal(record p711ValidationRecord) bool {
	if !p97PrimaryICPRecord(record) || record.AdoptPaySponsorScore < 2 {
		return false
	}
	if p97CredibleAdoptionToken(record.AdoptionSignalType) {
		return true
	}
	return p97CredibleAdoptionToken(record.BuyerBudgetSignal)
}

func p97CredibleAdoptionToken(value string) bool {
	switch p711SafeToken(value) {
	case "paid_pilot", "budget_owner", "budget_discussion", "procurement_path",
		"platform_roadmap", "internal_platform_sponsor", "internal_sponsor",
		"security_platform_approver", "platform_owner", "buyer_signal",
		"committed_internal_platform_rollout":
		return true
	default:
		return false
	}
}

func p97TokenIsYes(value string) bool {
	switch p711SafeToken(value) {
	case "yes", "true", "pass", "observed":
		return true
	default:
		return false
	}
}

func p97TokenIsPresent(value string) bool {
	switch p711SafeToken(value) {
	case "", "none", "not_available", "not_applicable", "n_a", "na", "no_signal", "absent", "unavailable", "missing", "no":
		return false
	default:
		return true
	}
}

func p97RepoWorkflowQualifies(value string) bool {
	switch p711SafeToken(value) {
	case "real", "representative":
		return true
	default:
		return false
	}
}

func p97RecordDisqualified(record p711ValidationRecord) bool {
	switch p711SafeToken(record.DisqualifierStatus) {
	case "", "none", "not_available", "not_applicable", "n_a", "na", "absent", "no":
		return false
	default:
		return true
	}
}

func p711IntegrationSignal(proofs p711IntegrationProofSet) p711SignalSummary {
	s := p711SignalSummary{ID: "integration_proof", Status: "not_available", Completeness: "missing", SourceRefs: []string{"p7.8_integration_proofs"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.8", "P8.6", "P9.8"}, Counts: map[string]int{}}
	validity := p711ProofValidityFor(proofs)
	requiredExecutable := map[string]bool{}
	for _, proof := range proofs.Proofs {
		s.Counts["proof_rows"]++
		if proof.Readiness["reviewed_placeholder"] {
			s.Counts["reviewed_placeholders"]++
		}
		if p711IntegrationProofHasPrivacyBlocker(proof) {
			s.Counts["privacy_blockers"]++
		}
		if !p711IntegrationProofPrivacyObjectPresent(proof) {
			s.Counts["missing_privacy"]++
		}
		if !p711IntegrationProofPrivacyFieldsPresent(proof) {
			s.Counts["incomplete_privacy"]++
		}
		if proof.privacySemantics.malformed {
			s.Counts["malformed_privacy"]++
		}
		if strings.TrimSpace(proof.ID) == "" {
			s.Counts["missing_ids"]++
		}
		if validity.duplicateID(proof.ID) {
			s.Counts["duplicate_ids"]++
		}
		if !p711IntegrationProofHasValidatedStatus(proof) {
			s.Counts["non_validated_statuses"]++
		}
		if !validity.validProof(proof) {
			continue
		}
		s.EvidenceIDs = append(s.EvidenceIDs, proof.ID)
		s.Counts["proof_paths"]++
		if proof.Path != "" {
			s.Counts["path_"+p711SafeToken(proof.Path)]++
		}
		s.Counts["executable_paths"]++
		if p711IsRequiredIntegrationPath(proof.Path) {
			requiredExecutable[proof.Path] = true
			s.Counts["executable_required_paths"] = len(requiredExecutable)
			s.Counts["executable_required_path_"+p711SafeToken(proof.Path)] = 1
		}
		if p711MCPStdioProofHasParity(proof, validity) {
			s.Counts["mcp_stdio_parity_paths"]++
		}
	}
	if s.Counts["privacy_blockers"] > 0 {
		s.Status = "blocked"
		s.Completeness = "blocked"
	} else if p711AllRequiredIntegrationPathsExecutable(requiredExecutable) &&
		s.Counts["mcp_stdio_parity_paths"] > 0 {
		s.Status = "pass"
		s.Completeness = "complete"
		s.Limitations = []string{"MCP resources are metadata-only; resource file bodies are not exported."}
	} else if s.Counts["proof_paths"] > 0 {
		s.Status = "partial"
		s.Completeness = "partial"
		s.Limitations = []string{"MCP stdio remains incomplete until P8.6 supplies replayable parity evidence"}
	}
	if s.Counts["reviewed_placeholders"] > 0 {
		s.Limitations = p711Compact(append(s.Limitations, "one or more integration paths are reviewed placeholders")...)
	}
	return s
}

func p98IntegrationEvidenceSummary(counts map[string]int) string {
	return fmt.Sprintf(
		"paths:%d required:%d shell_ci:%d external_agent:%d mcp_stdio:%d mcp_parity:%d",
		counts["proof_paths"],
		counts["executable_required_paths"],
		counts["executable_required_path_shell_ci_script"],
		counts["executable_required_path_external_coding_agent_workflow"],
		counts["executable_required_path_mcp_stdio"],
		counts["mcp_stdio_parity_paths"],
	)
}

type p711ProofValidityIndex struct {
	duplicateIDs map[string]bool
}

func p711ProofValidityFor(proofs p711IntegrationProofSet) p711ProofValidityIndex {
	counts := map[string]int{}
	for _, proof := range proofs.Proofs {
		id := strings.TrimSpace(proof.ID)
		if id == "" {
			continue
		}
		counts[id]++
	}
	duplicates := map[string]bool{}
	for id, count := range counts {
		if count > 1 {
			duplicates[id] = true
		}
	}
	return p711ProofValidityIndex{duplicateIDs: duplicates}
}

func (idx p711ProofValidityIndex) validProof(proof p711IntegrationProof) bool {
	id := strings.TrimSpace(proof.ID)
	return id != "" &&
		!idx.duplicateIDs[id] &&
		p711IntegrationProofHasValidatedStatus(proof) &&
		proof.Readiness["executable_evidence"] &&
		p711IntegrationProofHasSafePrivacySemantics(proof)
}

func (idx p711ProofValidityIndex) duplicateID(id string) bool {
	return idx.duplicateIDs[strings.TrimSpace(id)]
}

func p711IntegrationProofHasValidatedStatus(proof p711IntegrationProof) bool {
	return strings.TrimSpace(proof.Status) == p711ValidatedIntegrationProofStatus
}

func p711IntegrationProofHasPrivacyBlocker(proof p711IntegrationProof) bool {
	return proof.Privacy.RawPrompts ||
		proof.Privacy.FileBodies ||
		proof.Privacy.PrivatePaths ||
		proof.Privacy.Credentials ||
		proof.Privacy.PrivateRepoOrCustomerIDs
}

func p711IntegrationProofHasSafePrivacySemantics(proof p711IntegrationProof) bool {
	return p711IntegrationProofPrivacyObjectPresent(proof) &&
		p711IntegrationProofPrivacyFieldsPresent(proof) &&
		!proof.privacySemantics.malformed &&
		!p711IntegrationProofHasPrivacyBlocker(proof)
}

func p711IntegrationProofPrivacyObjectPresent(proof p711IntegrationProof) bool {
	return proof.privacySemantics.objectPresent
}

func p711IntegrationProofPrivacyFieldsPresent(proof p711IntegrationProof) bool {
	for _, field := range p711RequiredIntegrationPrivacyFields {
		if !proof.privacySemantics.fieldsPresent[field] {
			return false
		}
	}
	return true
}

func p711MCPStdioProofHasParity(proof p711IntegrationProof, validity p711ProofValidityIndex) bool {
	if !validity.validProof(proof) ||
		proof.Path != "mcp_stdio" ||
		!proof.ProductEvidenceExportParity ||
		proof.ReplayFixture == "" {
		return false
	}
	for _, field := range p98RequiredMCPParityReadinessFields {
		if !proof.Readiness[field] {
			return false
		}
	}
	return true
}

func p711IsRequiredIntegrationPath(path string) bool {
	for _, required := range p711RequiredIntegrationPathTypes {
		if path == required {
			return true
		}
	}
	return false
}

func p711AllRequiredIntegrationPathsExecutable(paths map[string]bool) bool {
	for _, required := range p711RequiredIntegrationPathTypes {
		if !paths[required] {
			return false
		}
	}
	return true
}

func p711BenchmarkSignal(localOps *localOpsBenchmarkReport) p711SignalSummary {
	s := p711SignalSummary{ID: "benchmark", Status: "not_available", Completeness: "missing", SourceRefs: []string{"local_ops_benchmark"}, RedactionStatus: "redacted", ReleaseGates: []string{"P6.5", "P7.11", "P9.1", "P9.10"}, Counts: map[string]int{}, Limitations: []string{"local-ops benchmark is readiness/replay evidence; external competitive baselines remain P9.10 scope"}}
	if localOps == nil {
		return s
	}
	s.Status = "partial"
	s.Completeness = "partial"
	s.Counts["profiles"] = localOps.Summary.ProfileCount
	s.Counts["smoke_passed"] = localOps.Summary.SmokePassed
	s.Counts["smoke_total"] = localOps.Summary.SmokeTotal
	s.Counts["replay_logs"] = localOps.Summary.ReplayLogCount
	s.Counts["blockers"] = localOps.Summary.BlockerCount
	if localOps.Replay.Status == "pass" {
		s.Status = "pass"
		s.Completeness = "complete"
	}
	return s
}

func p711AuditSignal(localOps *localOpsBenchmarkReport, security p711SecurityPosture) p711SignalSummary {
	s := p711SignalSummary{ID: "audit", Status: "partial", Completeness: "partial", SourceRefs: []string{"local_ops_benchmark", "p7.9_security_privacy_posture"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.9", "P9.1", "P9.3", "P9.9"}, Counts: map[string]int{"security_controls": len(security.RequiredControls)}}
	if localOps != nil && localOps.Quickstart != nil {
		s.EvidenceIDs = append(s.EvidenceIDs, localOps.Quickstart.AuditReport.AuditReportID)
		s.Counts["quickstart_audit_findings"] = localOps.Quickstart.AuditReport.FindingCount
	}
	if len(security.RequiredControls) > 0 {
		s.Status = "pass"
		s.Completeness = "complete"
	}
	return s
}

func p711LearningSignal(summary *comprehension.LearningLogSummary) p711SignalSummary {
	s := p711SignalSummary{ID: "learning", Status: "not_available", Completeness: "missing", SourceRefs: []string{"ranker_learning_log"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.11", "P9.1"}, Counts: map[string]int{}}
	if summary == nil {
		return s
	}
	s.Status = "pass"
	s.Completeness = "complete"
	s.Counts["total_entries"] = summary.TotalEntries
	s.Counts["accepted"] = summary.AcceptedCount
	s.Counts["rejected"] = summary.RejectedCount
	s.Counts["sources"] = len(summary.BySource)
	return s
}

func buildP711ClassCoverage(demos p711DemoTranscriptSet, security p711SecurityPosture, integration p711IntegrationProofSet, records []p711ValidationRecord) []p711ClassCoverage {
	classes := []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"}
	demoByClass := map[string]p711DemoTranscript{}
	for _, demo := range demos.Transcripts {
		demoByClass[demo.Classification] = demo
	}
	securityByClass := map[string]p711SecurityClassRow{}
	for _, row := range security.ClientClassificationCoverage {
		securityByClass[row.Classification] = row
	}
	recordCounts := map[string]int{}
	for _, record := range records {
		if !p711ValidationRecordQualifies(record) {
			continue
		}
		recordCounts[record.ClientClassification]++
	}
	proofIDs := p711IntegrationProofIDs(integration)
	rows := make([]p711ClassCoverage, 0, len(classes))
	for _, class := range classes {
		demo := demoByClass[class]
		sec := securityByClass[class]
		status := "partial"
		if demo.ID != "" && sec.Classification != "" {
			status = "pass"
		}
		rows = append(rows, p711ClassCoverage{
			Classification:          class,
			Status:                  status,
			Stance:                  localOpsSafeText(firstNonEmptyCLIString(demo.Stance, sec.Stance, p711DefaultStance(class))),
			P76CommandID:            firstNonEmptyCLIString(demo.P76CommandID, sec.P76CommandID),
			ClientDemoTranscriptID:  demo.ID,
			SecurityPostureCoverage: sec.Classification != "",
			IntegrationProofIDs:     proofIDs,
			ValidationRecordCount:   recordCounts[class],
			Limitations:             p711Compact(localOpsSafeText(demo.MVPLimitation), localOpsSafeText(sec.Limitation)),
		})
	}
	return rows
}

func buildP711ReleaseGateLinkage(signals []p711SignalSummary, classes []p711ClassCoverage, localOps *localOpsBenchmarkReport, records []p711ValidationRecord, p99 p711ReleaseGateLinkage) []p711ReleaseGateLinkage {
	classStatus := "evidence_present"
	for _, row := range classes {
		if row.Status != "pass" {
			classStatus = "needs_evidence"
			break
		}
	}
	p97 := p97ValidationGate(records)
	p98 := p98IntegrationGateFromSignals(signals)
	return []p711ReleaseGateLinkage{
		{Gate: "P9.1", Status: p711GateFromSignal(signals, "benchmark"), Evidence: "benchmark,audit,learning summaries", ClaimImpact: "release bundle input; not a release decision"},
		{Gate: "P9.6", Status: p96ActivationReleaseGateStatus(localOps), Evidence: "standalone product export cannot close P9.6 without current release validation evidence", ClaimImpact: "release activation claims are no-go until the release bundle consumes activation plus validation evidence"},
		{Gate: "P9.7", Status: p97.Status, Evidence: p97.Evidence, ClaimImpact: p97.ClaimImpact, Counts: p97.Counts, Blockers: p97.Blockers, EvidenceIDs: p97.EvidenceIDs},
		{Gate: "P9.8", Status: p98.Status, Evidence: p98.Evidence, ClaimImpact: p98.ClaimImpact, Counts: p98.Counts, Blockers: p98.Blockers, EvidenceIDs: p98.EvidenceIDs},
		{Gate: "P9.9", Status: p99.Status, Evidence: p99.Evidence, ClaimImpact: p99.ClaimImpact, Counts: p99.Counts, Blockers: p99.Blockers, EvidenceIDs: p99.EvidenceIDs},
		{Gate: "P9.12", Status: classStatus, Evidence: "classification coverage rows and limitations", ClaimImpact: "packaging/support thesis must cite statuses"},
	}
}

func p99SecurityPrivacyGate(root string, privacy p99ProductPrivacyState) p711ReleaseGateLinkage {
	counts := map[string]int{
		"required_sections":        10,
		"required_controls":        len(p99RequiredControlIDs()),
		"required_client_rows":     len(p711Classifications()),
		"required_gate_tiebacks":   len(p99RequiredTiebacks()),
		"required_forbidden_terms": len(p99ForbiddenEvidenceFields()),
	}
	var blockers []string
	var evidenceIDs []string
	addBlocker := func(reason string) {
		if strings.TrimSpace(reason) != "" {
			blockers = append(blockers, reason)
		}
	}

	matrixPath := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	matrixData, err := os.ReadFile(matrixPath)
	var matrix p711SecurityPosture
	if err != nil {
		addBlocker("missing posture matrix: docs/security-privacy-posture.matrix.json")
	} else if err := json.Unmarshal(matrixData, &matrix); err != nil {
		addBlocker("malformed posture matrix JSON")
	} else {
		evidenceIDs = append(evidenceIDs, firstNonEmptyCLIString(matrix.ArtifactID, "p7.9-security-privacy-posture"))
		p99EvaluateMatrix(matrix, counts, addBlocker)
	}

	postureText, postureOK := p99ReadText(filepath.Join(root, "docs", "security-privacy-posture.md"))
	if !postureOK {
		addBlocker("missing posture artifact: docs/security-privacy-posture.md")
	}
	planText, planOK := p99ReadText(filepath.Join(root, "docs", "idiom-implementation-plan.md"))
	if !planOK {
		addBlocker("missing P0.9 threat model/data-flow/control mapping artifact")
	}
	p09Text := p99P09Section(planText)
	reportText, reportOK := p99ReadText(filepath.Join(root, "docs", "release-gate-p9.9.md"))
	if !reportOK {
		addBlocker("missing P9.9 release-gate report")
	} else {
		evidenceIDs = append(evidenceIDs, "release-gate-p9.9")
	}

	for label, text := range map[string]string{
		"security/privacy posture": postureText,
		"P0.9 threat model":        p09Text,
		"P9.9 report":              reportText,
	} {
		if overclaim := p99ComplianceOverclaim(text); overclaim != "" {
			addBlocker(label + " contains regulated/compliance overclaim language: " + overclaim)
		}
	}
	if planOK {
		p99EvaluateP09Plan(p09Text, counts, addBlocker)
	}
	if postureOK {
		if strings.Contains(postureText, "Reviewer Command") {
			counts["repro_evidence_sections"]++
		} else {
			addBlocker("posture artifact missing reviewer command/repro evidence")
		}
	}
	if reportOK {
		p99EvaluateReviewReport(reportText, counts, addBlocker)
	}
	p99EvaluateDependentGateDoc(root, "P9.3", "docs/release-gate-p9.3.md", counts, addBlocker)
	p99EvaluateDependentGateDoc(root, "P9.8", "docs/release-gate-p9.8.md", counts, addBlocker)
	p99EvaluateDependentGateDoc(root, "P9.1", "docs/release-decision-p9.2.md", counts, addBlocker)
	p99EvaluateProductPrivacy(privacy, counts, addBlocker)

	status := "evidence_present"
	if len(blockers) > 0 {
		status = "needs_evidence"
	}
	counts["blockers"] = len(blockers)
	return p711ReleaseGateLinkage{
		Gate:        "P9.9",
		Status:      status,
		Evidence:    p99EvidenceSummary(counts),
		ClaimImpact: p99ClaimImpact(status),
		Counts:      counts,
		Blockers:    p711Compact(blockers...),
		EvidenceIDs: p711Compact(evidenceIDs...),
	}
}

func p99EvaluateProductPrivacy(privacy p99ProductPrivacyState, counts map[string]int, addBlocker func(string)) {
	addPrivacyBlocker := func(reason string) {
		counts["product_privacy_blockers"]++
		addBlocker(reason)
	}
	if privacy.ExportStatus == "blocked" {
		addPrivacyBlocker("product evidence export status is privacy-blocked")
	}
	if privacy.RedactionStatus == "blocked" {
		addPrivacyBlocker("product evidence redaction status is blocked")
	}
	if privacy.ForbiddenFieldCheck != "" && privacy.ForbiddenFieldCheck != "pass" {
		addPrivacyBlocker("product evidence forbidden-field check failed: " + privacy.ForbiddenFieldCheck)
	}
	if privacy.RawPromptCaptured {
		addPrivacyBlocker("product evidence raw prompt capture flag is true")
	}
	if privacy.RawFileBodyCaptured {
		addPrivacyBlocker("product evidence raw file body capture flag is true")
	}
	if privacy.SecretCaptured {
		addPrivacyBlocker("product evidence secret capture flag is true")
	}
	if privacy.PrivatePathCaptured {
		addPrivacyBlocker("product evidence private path capture flag is true")
	}
	if privacy.ValidationRecordsStatus == "blocked" {
		addPrivacyBlocker("P0.8 validation records failed privacy validation")
	}
}

func p99EvaluateMatrix(matrix p711SecurityPosture, counts map[string]int, addBlocker func(string)) {
	if matrix.SchemaVersion != "idiom.security_privacy_posture.v1" {
		addBlocker("posture matrix schema_version is missing or unexpected")
	}
	if matrix.ArtifactID != "p7.9-security-privacy-posture" {
		addBlocker("posture matrix artifact_id is missing or unexpected")
	}
	controlIDs := map[string]bool{}
	for i, control := range matrix.RequiredControls {
		controlIDs[control.ID] = true
		p99CheckPostureFieldOverclaim(fmt.Sprintf("required_controls[%d].claim", i), control.Claim, counts, addBlocker)
		p99CheckPostureFieldOverclaim(fmt.Sprintf("required_controls[%d].mvp_limitations", i), control.MVPLimitations, counts, addBlocker)
		if strings.TrimSpace(control.Claim) == "" || strings.TrimSpace(control.MVPLimitations) == "" {
			addBlocker("control row missing claim or limitation: " + control.ID)
		}
		if len(control.Evidence) == 0 {
			addBlocker("control row missing executable evidence: " + control.ID)
			continue
		}
		for _, evidence := range control.Evidence {
			if evidence.Kind != "go_test" || !strings.HasPrefix(evidence.Command, "go test ") ||
				strings.TrimSpace(evidence.Path) == "" || strings.TrimSpace(evidence.CheckID) == "" ||
				filepath.IsAbs(evidence.Path) || strings.Contains(evidence.Path, "/Users/") {
				addBlocker("control row has invalid or privacy-unsafe evidence: " + control.ID)
				break
			}
		}
	}
	counts["control_rows"] = len(matrix.RequiredControls)
	for _, id := range p99RequiredControlIDs() {
		if controlIDs[id] {
			counts["mapped_required_controls"]++
			continue
		}
		addBlocker("posture matrix missing required control: " + id)
	}

	rows := map[string]p711SecurityClassRow{}
	for i, row := range matrix.ClientClassificationCoverage {
		rows[row.Classification] = row
		p99CheckPostureFieldOverclaim(fmt.Sprintf("client_classification_coverage[%d].stance", i), row.Stance, counts, addBlocker)
		p99CheckPostureFieldOverclaim(fmt.Sprintf("client_classification_coverage[%d].limitation", i), row.Limitation, counts, addBlocker)
		if strings.TrimSpace(row.P76CommandID) == "" || len(row.CoveredControlIDs) == 0 {
			addBlocker("client row missing P7.6 command or control mapping: " + row.Classification)
		}
		for _, controlID := range row.CoveredControlIDs {
			if !controlIDs[controlID] {
				addBlocker("client row maps unknown control: " + row.Classification + "/" + controlID)
			}
		}
		if p99RegulatedClass(row.Classification) && !p99RegulatedRowProofOnly(row) {
			addBlocker("regulated client row is not explicitly proof-only/deferred: " + row.Classification)
		}
	}
	counts["client_rows"] = len(matrix.ClientClassificationCoverage)
	for _, class := range p711Classifications() {
		if _, ok := rows[class]; ok {
			counts["mapped_client_rows"]++
			continue
		}
		addBlocker("posture matrix missing client row: " + class)
	}

	tiebacks := map[string]bool{}
	for _, tieback := range matrix.ReleaseGateTiebacks {
		tiebacks[strings.TrimSpace(tieback)] = true
	}
	for _, tieback := range p99RequiredTiebacks() {
		if tiebacks[tieback] {
			counts["mapped_gate_tiebacks"]++
			continue
		}
		addBlocker("posture matrix missing release-gate tieback: " + tieback)
	}
	forbidden := map[string]bool{}
	for _, field := range matrix.ForbiddenEvidenceFields {
		forbidden[field] = true
	}
	for _, field := range p99ForbiddenEvidenceFields() {
		if forbidden[field] {
			counts["mapped_forbidden_terms"]++
			continue
		}
		addBlocker("posture matrix missing forbidden evidence field: " + field)
	}
}

func p99CheckPostureFieldOverclaim(fieldPath string, text string, counts map[string]int, addBlocker func(string)) {
	if overclaim := p99ComplianceOverclaim(text); overclaim != "" {
		counts["matrix_overclaim_blockers"]++
		addBlocker("posture matrix field " + fieldPath + " contains regulated/compliance overclaim language: " + overclaim)
	}
}

func p99EvaluateP09Plan(text string, counts map[string]int, addBlocker func(string)) {
	required := map[string]string{
		"threat_model":             "Product threat model:",
		"data_flow_map":            "Data-flow map:",
		"privacy_retention":        "Privacy and retention posture:",
		"control_mapping":          "Control mapping evidence:",
		"source_dates":             "Source anchors checked on",
		"downstream_gate_tiebacks": "Downstream go/no-go gates:",
	}
	for key, marker := range required {
		if strings.Contains(text, marker) {
			counts["present_sections"]++
			counts[key] = 1
			continue
		}
		addBlocker("P0.9 artifact missing section: " + key)
	}
	for _, marker := range []string{"P7.9", "P8.4", "P9.1", "P9.3", "P9.9"} {
		if !strings.Contains(text, marker) {
			addBlocker("P0.9 artifact missing dependent gate reference: " + marker)
		}
	}
	p99EvaluateP09SourceVerification(text, counts, addBlocker)
}

func p99EvaluateReviewReport(text string, counts map[string]int, addBlocker func(string)) {
	lower := strings.ToLower(text)
	hasGo := strings.Contains(lower, "reviewer signoff: go") || strings.Contains(lower, "decision: go")
	hasNoGo := strings.Contains(lower, "decision: no-go") || strings.Contains(lower, "reviewer signoff: no-go")
	if hasGo {
		counts["reviewer_go"] = 1
	}
	if hasNoGo {
		counts["reviewer_no_go"] = 1
	}
	switch {
	case hasGo && hasNoGo:
		addBlocker("P9.9 report has conflicting GO/no-go markers")
	case hasNoGo:
		addBlocker("P9.9 report records explicit no-go")
	case hasGo:
		// Reviewer GO is necessary but not sufficient; dependent evidence still gates readiness.
	default:
		addBlocker("P9.9 report lacks reviewer GO or explicit no-go")
	}
	for _, marker := range []string{"Threat model", "Data-flow", "Privacy", "Control mapping", "Reproduction Commands"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			counts["report_sections"]++
			continue
		}
		addBlocker("P9.9 report missing section marker: " + marker)
	}
}

func p99EvaluateP09SourceVerification(text string, counts map[string]int, addBlocker func(string)) {
	records, ok := p99ParseSourceVerificationRecords(text)
	if !ok {
		addBlocker("P0.9 artifact missing structured source verification evidence")
		return
	}
	if len(records) == 0 {
		addBlocker("P0.9 source verification evidence missing source_id records")
		return
	}

	for _, record := range records {
		recordOK := true
		label := firstNonEmptyCLIString(record.SourceID, "unlabeled")
		if strings.TrimSpace(record.SourceID) == "" {
			addBlocker("P0.9 source verification record source_id is empty")
			recordOK = false
		}
		checkedDate := strings.TrimSpace(record.CheckedDate)
		switch {
		case checkedDate == "":
			addBlocker("P0.9 source verification record checked_date is empty: " + label)
			recordOK = false
		default:
			parsed, err := time.Parse("2006-01-02", checkedDate)
			if err != nil || parsed.Format("2006-01-02") != checkedDate {
				addBlocker("P0.9 source verification record checked_date must be YYYY-MM-DD: " + label)
				recordOK = false
			}
		}
		if strings.TrimSpace(record.VerificationMethod) == "" {
			addBlocker("P0.9 source verification record verification_method is empty: " + label)
			recordOK = false
		}
		if record.unreachable() {
			if strings.TrimSpace(record.UnreachableSourceVerificationMethod) == "" {
				addBlocker("P0.9 unreachable source evidence missing verification method for source_id: " + label)
				recordOK = false
			} else {
				counts["unreachable_source_verification_methods"]++
			}
		}
		if recordOK {
			counts["source_verification_records"]++
		}
	}
}

type p99SourceVerificationRecord struct {
	SourceID                            string
	CheckedDate                         string
	VerificationMethod                  string
	SourceStatus                        string
	UnreachableSourceVerificationMethod string
	raw                                 string
}

func (record p99SourceVerificationRecord) unreachable() bool {
	return p99MentionsUnreachableSource(strings.ToLower(record.SourceID + " " + record.SourceStatus + " " + record.raw))
}

func p99ParseSourceVerificationRecords(text string) ([]p99SourceVerificationRecord, bool) {
	marker := "source verification evidence:"
	lower := strings.ToLower(text)
	start := strings.Index(lower, marker)
	if start < 0 {
		return nil, false
	}
	body := text[start+len(marker):]
	var records []p99SourceVerificationRecord
	var current *p99SourceVerificationRecord
	flush := func() {
		if current != nil {
			records = append(records, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		}
		key, value, ok := p99SourceVerificationField(trimmed)
		if !ok {
			if current != nil {
				current.raw += "\n" + trimmed
			}
			continue
		}
		if key == "source_id" {
			flush()
			current = &p99SourceVerificationRecord{SourceID: value, raw: trimmed}
			continue
		}
		if current == nil {
			continue
		}
		current.raw += "\n" + trimmed
		switch key {
		case "checked_date":
			current.CheckedDate = value
		case "verification_method":
			current.VerificationMethod = value
		case "source_status":
			current.SourceStatus = value
		case "unreachable_source_verification_method":
			current.UnreachableSourceVerificationMethod = value
		}
	}
	flush()
	return records, true
}

func p99SourceVerificationField(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "source_id", "checked_date", "verification_method", "source_status", "unreachable_source_verification_method":
		return key, strings.TrimSpace(value), true
	default:
		return "", "", false
	}
}

func p99MentionsUnreachableSource(lower string) bool {
	for _, marker := range []string{"403", "unreachable", "could not verify", "access denied", "forbidden", "host returned"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func p99EvaluateDependentGateDoc(root, gate, rel string, counts map[string]int, addBlocker func(string)) {
	text, ok := p99ReadText(filepath.Join(root, filepath.FromSlash(rel)))
	if !ok {
		addBlocker(gate + " dependent evidence missing: " + rel)
		return
	}
	counts["dependent_gate_docs"]++
	lower := strings.ToLower(text)
	if strings.Contains(lower, "decision: no-go") || strings.Contains(lower, "status:partial") {
		addBlocker(gate + " dependent evidence is not GO/complete")
	}
}

func p99ReadText(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func p99P09Section(text string) string {
	start := strings.Index(text, "### P0.9 Product Security And Privacy Posture")
	if start < 0 {
		return p99P09LedgerRow(text)
	}
	section := text[start:]
	if next := strings.Index(section[len("### "):], "\n### "); next >= 0 {
		return section[:len("### ")+next]
	}
	return section
}

func p99P09LedgerRow(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| P0.9:") {
			return trimmed
		}
	}
	return text
}

func p99RequiredControlIDs() []string {
	return []string{
		"protected_path_denial",
		"redaction_no_raw_prompt_or_file_body",
		"retention_local_artifact_scope",
		"telemetry_minimization_no_hosted_default",
		"side_effect_policy_read_only_discovery",
		"mcp_resource_containment",
	}
}

func p99RequiredTiebacks() []string {
	return []string{"P7.9", "P8.4", "P9.1", "P9.3", "P9.9"}
}

func p99ForbiddenEvidenceFields() []string {
	return []string{"raw_prompt", "raw_file_body", "secret_value", "private_repo_url", "unredacted_log_excerpt", "customer_identifier", "patient_name", "account_number"}
}

func p711Classifications() []string {
	return []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"}
}

func p99RegulatedClass(class string) bool {
	switch class {
	case "finance", "healthcare", "public-sector":
		return true
	default:
		return false
	}
}

func p99RegulatedRowProofOnly(row p711SecurityClassRow) bool {
	text := strings.ToLower(row.Stance + " " + row.Limitation)
	if strings.Contains(text, "sales ready") || strings.Contains(text, "sales-ready") ||
		strings.Contains(text, "regulated sales readiness") || strings.Contains(text, "release ready") ||
		strings.Contains(text, "commercially ready") {
		return false
	}
	return strings.Contains(text, "proof-only") || strings.Contains(text, "proof only") ||
		strings.Contains(text, "deferred") || strings.Contains(text, "no ") && strings.Contains(text, "compliance")
}

func p99ComplianceOverclaim(text string) string {
	patterns := []string{
		"hipaa compliant",
		"hipaa compliance",
		"hipaa-certified",
		"hipaa certified",
		"hipaa certification",
		"hipaa attestation",
		"hipaa conformance",
		"finra compliant",
		"finra compliance",
		"finra-certified",
		"finra certified",
		"finra certification",
		"finra attestation",
		"finra conformance",
		"nist compliant",
		"nist compliance",
		"nist-certified",
		"nist certified",
		"nist certification",
		"nist attestation",
		"nist conformance",
		"owasp compliant",
		"owasp compliance",
		"owasp-certified",
		"owasp certified",
		"owasp certification",
		"owasp attestation",
		"owasp conformance",
		"mcp compliant",
		"mcp compliance",
		"mcp-certified",
		"mcp certified",
		"mcp certification",
		"mcp attestation",
		"mcp conformance",
		"fedramp compliant",
		"fedramp compliance",
		"fedramp-certified",
		"fedramp certified",
		"fedramp certification",
		"fedramp attestation",
		"formal approval",
		"formally approved",
		"third-party approval",
		"regulated sales ready",
		"regulated-sales-ready",
		"regulated sales readiness",
		"sales ready",
		"sales readiness",
		"sales-ready",
		"release ready",
		"commercially ready",
	}
	for _, assertion := range p99ClaimAssertions(text) {
		lower := strings.ToLower(assertion)
		for _, pattern := range patterns {
			if strings.Contains(lower, pattern) && !p99NegatesPattern(lower, pattern) {
				return pattern
			}
		}
	}
	return ""
}

func p99ClaimAssertions(text string) []string {
	text = strings.ReplaceAll(text, "\n", " ")
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '.', ';', '!', '?':
			return true
		default:
			return false
		}
	})
	var out []string
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func p99NegatesPattern(assertion string, pattern string) bool {
	idx := strings.Index(assertion, pattern)
	if idx < 0 {
		return false
	}
	prefix := strings.TrimSpace(assertion[:idx])
	suffix := strings.TrimSpace(assertion[idx+len(pattern):])
	return p99LocalNegationPrefix(prefix) || p99LocalNegationSuffix(suffix)
}

func p99LocalNegationPrefix(prefix string) bool {
	for _, negator := range []string{
		"no",
		"not",
		"not a",
		"not an",
		"does not prove",
		"does not support",
		"doesn't prove",
		"doesn't support",
		"do not claim",
		"does not claim",
		"must not claim",
		"cannot claim",
		"can't claim",
	} {
		if p99EndsWithPhrase(prefix, negator) {
			return true
		}
	}
	for _, negator := range []string{"no", "not", "not a", "not an", "does not prove", "does not support", "doesn't prove", "doesn't support", "do not claim", "does not claim", "must not claim", "cannot claim", "can't claim"} {
		if p99ContainsLocalListNegation(prefix, negator) {
			return true
		}
	}
	if p99ContainsNoAssertionNegation(prefix) {
		return true
	}
	return false
}

func p99ContainsLocalListNegation(prefix string, negator string) bool {
	idx := p99LastPhraseIndex(prefix, negator)
	if idx < 0 {
		return false
	}
	afterNegator := strings.TrimSpace(prefix[idx+len(negator):])
	if len(afterNegator) > 180 || p99ContainsPositiveClaimCue(afterNegator) {
		return false
	}
	return true
}

func p99ContainsNoAssertionNegation(prefix string) bool {
	idx := p99LastPhraseIndex(prefix, "no")
	if idx < 0 {
		return false
	}
	afterNo := strings.TrimSpace(prefix[idx:])
	if len(afterNo) > 180 {
		return false
	}
	padded := " " + afterNo + " "
	return strings.Contains(padded, " asserts ") || strings.Contains(padded, " assert ") ||
		strings.Contains(padded, " claims ") || strings.Contains(padded, " claim ")
}

func p99LastPhraseIndex(text string, phrase string) int {
	for idx := strings.LastIndex(text, phrase); idx >= 0; idx = strings.LastIndex(text[:idx], phrase) {
		beforeOK := idx == 0 || p99PhraseBoundary(text[idx-1])
		after := idx + len(phrase)
		afterOK := after == len(text) || p99PhraseBoundary(text[after])
		if beforeOK && afterOK {
			return idx
		}
	}
	return -1
}

func p99PhraseBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', ',', ';', ':', '(', ')', '[', ']', '{', '}', '/', '-':
		return true
	default:
		return false
	}
}

func p99ContainsPositiveClaimCue(text string) bool {
	padded := " " + strings.TrimSpace(text) + " "
	for _, cue := range []string{
		" has ",
		" have ",
		" had ",
		" provides ",
		" provide ",
		" provided ",
		" asserts ",
		" assert ",
		" claims ",
		" claim ",
		" offers ",
		" offer ",
		" proves ",
		" prove ",
		" supports ",
		" support ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

func p99LocalNegationSuffix(suffix string) bool {
	suffix = strings.TrimLeft(suffix, " \t,")
	for _, allowed := range []string{
		"claim",
		"claims",
		"is claimed",
		"are claimed",
		"remains no-go",
		"remain no-go",
		"remains a no-go",
		"remain a no-go",
	} {
		if suffix == allowed || strings.HasPrefix(suffix, allowed+" ") || strings.HasPrefix(suffix, allowed+".") {
			return true
		}
	}
	return false
}

func p99EndsWithPhrase(text string, phrase string) bool {
	text = strings.TrimRight(text, " \t,;:")
	if text == phrase {
		return true
	}
	return strings.HasSuffix(text, " "+phrase)
}

func p99EvidenceSummary(counts map[string]int) string {
	return fmt.Sprintf("controls:%d/%d client_rows:%d/%d tiebacks:%d/%d blockers:%d",
		counts["mapped_required_controls"], counts["required_controls"],
		counts["mapped_client_rows"], counts["required_client_rows"],
		counts["mapped_gate_tiebacks"], counts["required_gate_tiebacks"],
		counts["blockers"])
}

func p99ClaimImpact(status string) string {
	if status == "evidence_present" {
		return "security/privacy posture may be cited as reviewed control-mapping evidence, not compliance, certification, attestation, or regulated sales readiness"
	}
	return "regulated, compliance-adjacent, and security/privacy readiness claims remain proof-only/no-go until P9.9 blockers close"
}

func p98IntegrationGateFromSignals(signals []p711SignalSummary) p98IntegrationGateResult {
	for _, signal := range signals {
		if signal.ID != "integration_proof" {
			continue
		}
		out := p98IntegrationGateResult{
			Status:      p711GateFromSignal(signals, "integration_proof"),
			Evidence:    p98IntegrationEvidenceSummary(signal.Counts),
			ClaimImpact: "substrate integration claim depends on shell/CI, external coding-agent, and MCP stdio proof parity",
			Counts:      signal.Counts,
			EvidenceIDs: append([]string(nil), signal.EvidenceIDs...),
		}
		if out.Status == "evidence_present" {
			out.ClaimImpact = "P9.8 can cite three executable integration proof paths with MCP stdio parity"
			return out
		}
		out.Blockers = p98IntegrationBlockersFromSignal(signal)
		out.Evidence = out.Evidence + "; blockers:" + strings.Join(out.Blockers, "|")
		return out
	}
	return p98IntegrationGateResult{
		Status:      "needs_evidence",
		Evidence:    "integration proof signal missing",
		ClaimImpact: "substrate integration claims are no-go until all three proof paths pass",
		Counts:      map[string]int{},
		Blockers:    []string{"integration proof signal missing"},
	}
}

func p98IntegrationBlockersFromSignal(signal p711SignalSummary) []string {
	var blockers []string
	if signal.Counts["executable_required_path_shell_ci_script"] == 0 {
		blockers = append(blockers, "missing executable integration proof path: shell_ci_script")
	}
	if signal.Counts["executable_required_path_external_coding_agent_workflow"] == 0 {
		blockers = append(blockers, "missing executable integration proof path: external_coding_agent_workflow")
	}
	if signal.Counts["executable_required_path_mcp_stdio"] == 0 {
		blockers = append(blockers, "missing executable integration proof path: mcp_stdio")
	}
	if signal.Counts["mcp_stdio_parity_paths"] == 0 {
		blockers = append(blockers, "MCP stdio proof is missing required parity fields on the mcp_stdio path")
	}
	if signal.Counts["duplicate_ids"] > 0 {
		blockers = append(blockers, "duplicate proof IDs cannot satisfy required integration paths")
	}
	if signal.Counts["missing_ids"] > 0 {
		blockers = append(blockers, "proof rows without IDs cannot satisfy required integration paths")
	}
	if signal.Counts["non_validated_statuses"] > 0 {
		blockers = append(blockers, "non-test-validated proof rows cannot satisfy required integration paths")
	}
	if signal.Counts["missing_privacy"] > 0 || signal.Counts["incomplete_privacy"] > 0 || signal.Counts["malformed_privacy"] > 0 {
		blockers = append(blockers, "proof rows require explicit safe privacy semantics")
	}
	if signal.Counts["privacy_blockers"] > 0 {
		blockers = append(blockers, "proof rows report forbidden privacy leakage")
	}
	if len(blockers) == 0 && signal.Completeness != "complete" {
		blockers = append(blockers, "integration proof signal is incomplete")
	}
	return p711Compact(blockers...)
}

func summarizeP711Signals(signals []p711SignalSummary) p711SignalCompleteness {
	out := p711SignalCompleteness{Required: []string{"activation", "retained_use", "integration_proof", "benchmark", "audit", "learning"}, RequiredCount: 6}
	for _, signal := range signals {
		switch signal.Completeness {
		case "complete":
			out.Present = append(out.Present, signal.ID)
		case "blocked":
			out.Blocked = append(out.Blocked, signal.ID)
		case "partial":
			out.Partial = append(out.Partial, signal.ID)
		default:
			out.NotAvailable = append(out.NotAvailable, signal.ID)
		}
	}
	out.CompleteCount = len(out.Present)
	if len(out.Blocked) > 0 {
		out.Status = "blocked"
	} else if out.CompleteCount == out.RequiredCount {
		out.Status = "complete"
	} else {
		out.Status = "partial"
	}
	return out
}

func p711ExportStatus(completeness p711SignalCompleteness) string {
	switch completeness.Status {
	case "complete":
		return "pass"
	case "blocked":
		return "blocked"
	default:
		return "partial"
	}
}

func p711ReadJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func p711FileRef(id string, kind string, path string) p711SourceReference {
	ref := p711SourceReference{ID: id, Kind: kind, Status: "not_available", PathID: localOpsPathEvidence(path)}
	if data, err := os.ReadFile(path); err == nil {
		ref.ArtifactHash = p711Hash(data)
	}
	return ref
}

func decodeP711ValidationRecords(data []byte) ([]p711ValidationRecord, error) {
	var records []p711ValidationRecord
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&records); err != nil {
			return nil, err
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("validation records contain trailing JSON values")
			}
			return nil, err
		}
		return records, nil
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record p711ValidationRecord
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&record); err != nil {
			return nil, err
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("validation record contains trailing JSON values")
			}
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func p711ForbiddenValidationEvidence(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "\n") {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if forbidden := p711ForbiddenValidationJSONValue([]byte(line)); forbidden != "" {
				return forbidden
			}
		}
		return localOpsForbiddenStringEvidence(trimmed)
	}
	return p711ForbiddenValidationJSONValue([]byte(trimmed))
}

func p711ForbiddenValidationJSONValue(data []byte) string {
	var value any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&value); err == nil {
		return p711ForbiddenValidationValue(value)
	}
	return localOpsForbiddenStringEvidence(string(data))
}

func p711ForbiddenValidationValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if forbidden := p711ForbiddenValidationKey(key); forbidden != "" {
				return forbidden
			}
			if boolValue, ok := typed[key].(bool); ok && boolValue && localOpsForbiddenBooleanEvidenceKey(key) {
				return key
			}
			if forbidden := p711ForbiddenValidationValue(typed[key]); forbidden != "" {
				return forbidden
			}
		}
	case []any:
		for _, item := range typed {
			if forbidden := p711ForbiddenValidationValue(item); forbidden != "" {
				return forbidden
			}
		}
	case string:
		return p711ForbiddenValidationString(typed)
	}
	return ""
}

func p711ForbiddenValidationKey(key string) string {
	normalized := p711SafeToken(key)
	switch normalized {
	case "raw_prompt", "raw_prompts", "raw_prompt_body", "prompt_body",
		"raw_file_body", "raw_file_bodies", "file_body", "source_body",
		"raw_transcript", "raw_chat_transcript", "chat_transcript",
		"secret", "secrets", "credential", "credentials", "api_key", "token",
		"private_path", "private_paths", "private_repo_url", "customer", "customer_name",
		"customer_identifier", "private_identifier", "unredacted_log", "unredacted_logs",
		"telemetry_path", "log_path", "raw_capture", "capture_raw_prompt", "capture_file_body":
		return normalized
	default:
		return ""
	}
}

func p711ForbiddenValidationString(value string) string {
	if forbidden := localOpsForbiddenStringEvidence(value); forbidden != "" {
		return forbidden
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "raw_prompt"), strings.Contains(lower, "raw prompt"):
		return "raw_prompt"
	case strings.Contains(lower, "raw_file_body"), strings.Contains(lower, "raw file body"):
		return "raw_file_body"
	case strings.Contains(lower, "logs/telemetry/"),
		strings.Contains(lower, "logs/conversations/"),
		strings.Contains(lower, ".idiom/logs/"),
		strings.Contains(lower, ".idiom/telemetry/"):
		return "unredacted_log_path"
	default:
		return ""
	}
}

func p711ValidationRecordQualifies(record p711ValidationRecord) bool {
	if record.RecordID == "" || record.ClientClassification == "" || record.ClientClassification == "unclassified" {
		return false
	}
	if record.EvidenceStatus != "complete" {
		return false
	}
	switch record.RedactionStatus {
	case "pass", "redacted":
		return true
	default:
		return false
	}
}

func p711HasAdoptionSignal(value string) bool {
	switch p711SafeToken(value) {
	case "", "none", "not_available", "not_applicable", "n_a", "na", "no_signal", "absent", "unavailable", "missing":
		return false
	default:
		return true
	}
}

func p711ApprovedClassification(value string) string {
	value = strings.TrimSpace(value)
	for _, allowed := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
		if value == allowed {
			return value
		}
	}
	return "unclassified"
}

func p711SafeID(value string, prefix string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	token := p711SafeToken(value)
	if token != "" && len(token) <= 80 && !localOpsTokenNeedsRedaction(token) {
		return token
	}
	return prefix + "_id:" + localOpsStableID(value)
}

func p711SafeStatus(value string) string {
	value = p711SafeToken(value)
	switch value {
	case "pass", "fail", "blocked", "partial", "complete", "observed", "missing", "not_available", "insufficient_evidence", "no_go", "draft", "withdrawn", "disqualified", "redacted", "retained_by_policy", "none":
		return value
	default:
		if value == "" {
			return "not_available"
		}
		return "redacted"
	}
}

func p711SafeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 80 {
		return out[:80]
	}
	return out
}

func p711Completeness(status string) string {
	switch status {
	case "pass", "complete", "observed":
		return "complete"
	case "blocked", "no_go":
		return "blocked"
	case "", "not_available":
		return "missing"
	default:
		return "partial"
	}
}

func p711GateFromSignal(signals []p711SignalSummary, id string) string {
	for _, signal := range signals {
		if signal.ID != id {
			continue
		}
		switch signal.Completeness {
		case "complete":
			return "evidence_present"
		case "blocked":
			return "blocked"
		default:
			return "needs_evidence"
		}
	}
	return "needs_evidence"
}

func p711IntegrationProofIDs(proofs p711IntegrationProofSet) []string {
	validity := p711ProofValidityFor(proofs)
	var ids []string
	for _, proof := range proofs.Proofs {
		if validity.validProof(proof) {
			ids = append(ids, proof.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func p711DefaultStance(class string) string {
	switch class {
	case "enterprise-saas":
		return "primary MVP buyer posture"
	case "open-source":
		return "activation and adoption wedge"
	case "tooling-vendor":
		return "secondary integration path"
	case "local-model":
		return "local readiness proof"
	default:
		return "regulated proof-only"
	}
}

func p711Compact(values ...string) []string {
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

func p711Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func productEvidenceReportHash(report productEvidenceExportReport) string {
	report.GeneratedAt = ""
	report.ExportID = ""
	report.ArtifactHash = ""
	body, err := json.Marshal(report)
	if err != nil {
		return localOpsStableID("product-evidence-report")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

func finalizeProductEvidenceIdentity(report *productEvidenceExportReport) {
	report.ArtifactHash = productEvidenceReportHash(*report)
	report.ExportID = "product-evidence-" + report.ArtifactHash
}

func refreshProductEvidenceP99Gate(root string, report *productEvidenceExportReport) {
	p99 := p99SecurityPrivacyGate(root, p99ProductPrivacyStateFromReport(*report))
	report.ProductSuccessGates.SecurityPrivacy = p99.Status
	for i, gate := range report.ReleaseGateLinkage {
		if gate.Gate == "P9.9" {
			report.ReleaseGateLinkage[i] = p99
			return
		}
	}
	report.ReleaseGateLinkage = append(report.ReleaseGateLinkage, p99)
}

func p99ProductPrivacyStateFromReport(report productEvidenceExportReport) p99ProductPrivacyState {
	state := p99ProductPrivacyState{
		ExportStatus:        report.Status,
		RedactionStatus:     report.Privacy.RedactionStatus,
		ForbiddenFieldCheck: report.Privacy.ForbiddenFieldCheck,
		RawPromptCaptured:   report.Privacy.RawPromptCaptured,
		RawFileBodyCaptured: report.Privacy.RawFileBodyCaptured,
		SecretCaptured:      report.Privacy.SecretCaptured,
		PrivatePathCaptured: report.Privacy.PrivatePathCaptured,
	}
	for _, ref := range report.SourceReferences {
		if ref.ID == "p0.8_validation_records" {
			state.ValidationRecordsStatus = ref.Status
			break
		}
	}
	return state
}

func sanitizeProductEvidenceTelemetryArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		if flag, value, ok := strings.Cut(arg, "="); ok {
			switch flag {
			case "--project-root", "--output", "--local-ops-report", "--validation-records":
				out[i] = flag + "=" + redactedProductEvidencePathArg(value)
			}
			continue
		}
		switch arg {
		case "--project-root", "--output", "--local-ops-report", "--validation-records":
			if i+1 < len(out) {
				i++
				out[i] = redactedProductEvidencePathArg(out[i])
			}
		}
	}
	return out
}

func redactedProductEvidencePathArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	return "<redacted_product_evidence_path id=" + localOpsStableID(value) + ">"
}

func printProductEvidenceUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: idiom product-evidence export [--project-root <root>] [--local-ops-report <report.json>] [--validation-records <records.json>] [--output <path>]")
}
