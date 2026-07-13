package productevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/sensitivity"
	idiomeval "idiom/internal/evidence/eval"
	"idiom/internal/evidence/lanehistory"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/jsonfile"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/localops"
)

type productEvidenceExportReport struct {
	SchemaVersion       string                              `json:"schema_version"`
	GeneratedAt         string                              `json:"generated_at"`
	Status              string                              `json:"status"`
	ExportID            string                              `json:"export_id"`
	ArtifactHash        string                              `json:"artifact_hash"`
	ProjectRootID       string                              `json:"project_root_id,omitempty"`
	LaneHistory         productEvidenceLaneHistory          `json:"lane_history"`
	Privacy             productEvidencePrivacy              `json:"privacy"`
	ProductSuccessGates productEvidenceGateSummary          `json:"product_success_gates"`
	SignalCompleteness  productEvidenceSignalCompleteness   `json:"signal_completeness"`
	Signals             []productEvidenceSignalSummary      `json:"signals"`
	ClassCoverage       []productEvidenceClassCoverage      `json:"class_coverage"`
	Recovery            productEvidenceRecovery             `json:"recovery"`
	SourceReferences    []productEvidenceSourceReference    `json:"source_references"`
	ReleaseGateLinkage  []productEvidenceReleaseGateLinkage `json:"release_gate_linkage"`
	Limitations         []string                            `json:"limitations,omitempty"`
	SourceIDs           map[string]string                   `json:"source_ids,omitempty"`
	Metadata            map[string]any                      `json:"metadata,omitempty"`
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
	BenchmarkReport   string
}

type productEvidenceSignalCompleteness struct {
	Status        string   `json:"status"`
	Required      []string `json:"required"`
	Present       []string `json:"present"`
	Partial       []string `json:"partial,omitempty"`
	NotAvailable  []string `json:"not_available,omitempty"`
	Blocked       []string `json:"blocked,omitempty"`
	CompleteCount int      `json:"complete_count"`
	RequiredCount int      `json:"required_count"`
}

type productEvidenceSignalSummary struct {
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

type productEvidenceClassCoverage struct {
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

type productEvidenceSourceReference struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	PathID       string `json:"path_id,omitempty"`
	ArtifactHash string `json:"artifact_hash,omitempty"`
}

type productEvidenceReleaseGateLinkage struct {
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

func RunProductEvidenceMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Dependencies) int {
	if len(args) == 0 {
		printProductEvidenceUsage(stderr)

		return 2
	}

	switch args[0] {
	case "export":
		return runProductEvidenceExportWithDeps(args[1:], stdout, stderr, rec, deps)
	case "-h", "--help", "help":
		printProductEvidenceUsage(stdout)

		return 0
	default:
		fmt.Fprintf(stderr, "product-evidence: unknown subcommand %q\n", args[0])
		printProductEvidenceUsage(stderr)

		return 2
	}
}

func runProductEvidenceExportWithDeps(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Dependencies) int {
	fs := flag.NewFlagSet("idiom product-evidence export", flag.ContinueOnError)
	fs.SetOutput(stderr)

	opts := productEvidenceExportOptions{}
	fs.StringVar(&opts.ProjectRoot, "project-root", ".", "workspace root whose local evidence should be summarized")
	fs.StringVar(&opts.OutputPath, "output", "", "optional path to write the JSON export")
	fs.StringVar(&opts.LocalOpsReport, "local-ops-report", "", "optional local-ops benchmark JSON report to fold into the export")
	fs.StringVar(&opts.ValidationRecords, "validation-records", "", "optional privacy-safe P0.8 validation records JSON or JSONL")
	fs.StringVar(&opts.BenchmarkReport, "benchmark-report", "", "optional localization benchmark or corpus JSON report to fold into P9.10")

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

	summary, err := deps.summarizeLaneHistory(root)
	if err != nil {
		fmt.Fprintf(stderr, "product-evidence export: summarize lane history: %v\n", err)

		return 1
	}

	report := buildProductEvidenceExportReportWithDeps(root, summary, opts, deps)

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "product-evidence export: marshal report: %v\n", err)

		return 1
	}

	if forbidden := localops.ForbiddenEvidenceLeak(string(body)); forbidden != "" {
		localops.RedactForbiddenStrings(&report)

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

		fmt.Fprintf(stdout, "product-evidence export wrote output_id:%s\n", localops.StableID(opts.OutputPath))
	} else {
		_, _ = stdout.Write(body)
	}

	telemetry.LogPhase(rec, "product_evidence_export", map[string]any{
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
		"recovery_status":               report.Recovery.Status,
		"recovery_open":                 report.Recovery.OpenPacketCount,
	})

	return 0
}

func buildProductEvidenceExportReportWithDeps(root string, summary lanehistory.Summary, opts productEvidenceExportOptions, deps Dependencies) productEvidenceExportReport {
	laneStatus := "not_available"

	if summary.Total > 0 {
		laneStatus = "available"
	}

	localOps, localOpsRef := loadP711LocalOpsReport(root, opts.LocalOpsReport)
	integration, integrationRef := loadP711IntegrationProofs(root)
	security, securityRef := loadP711SecurityPosture(root)
	demos, demosRef := loadP711ClientDemoTranscripts(root)
	p911 := p911ClientDemoGateWithDeps(demos, demosRef, deps)
	records, recordsRef := loadP711ValidationRecords(opts.ValidationRecords)
	learning, learningRef := loadP711LearningSummary(root)
	competitive, competitiveRef := loadP910CompetitiveBenchmarkEvidence(root, opts.BenchmarkReport)
	packaging, packagingRef := loadP912PackagingSupportThesis(root)
	recoverySummary, recoveryRef := loadProductEvidenceRecovery(root)
	signals := buildP711Signals(localOps, records, integration, security, learning)
	classCoverage := buildP711ClassCoverage(demos, security, integration, records)
	completeness := summarizeP711Signals(signals)
	status := p711ExportStatus(completeness)

	status = productEvidenceStatusWithRecovery(status, recoverySummary.Status)

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
		ProjectRootID: "root_id:" + localops.StableID(root),
		SourceIDs:     map[string]string{"lane_history": "artifact_id:" + localops.StableID(filepath.Join(root, lanehistory.RelPath))},
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
		SourceReferences: []productEvidenceSourceReference{
			localOpsRef,
			integrationRef,
			securityRef,
			demosRef,
			recordsRef,
			learningRef,
			competitiveRef,
			packagingRef,
			recoveryRef,
		},
		ReleaseGateLinkage: buildP711ReleaseGateLinkage(signals, classCoverage, localOps, records, p99, p910CompetitiveBenchmarkGate(competitive, competitiveRef), p911, packaging, packagingRef),
		Recovery:           recoverySummary,
		Limitations:        productEvidenceExportLimitations(integration, recoverySummary),
		Metadata: map[string]any{
			"generated_by": "idiom product-evidence export",
		},
	}
	finalizeProductEvidenceIdentity(&report)

	return report
}

func productEvidenceStatusWithRecovery(status string, recoveryStatus string) string {
	switch recoveryStatus {
	case "blocked":
		return "blocked"
	case "partial":
		if status == "pass" {
			return "partial"
		}
	}

	return status
}

func productEvidenceExportLimitations(integration p711IntegrationProofSet, recovery productEvidenceRecovery) []string {
	limitations := []string{
		"export summarizes bounded local metadata only",
		"prompt bodies, file bodies, and transcript bodies are not loaded",
		"retained-use and adoption signals are not claimed unless validation records are supplied",
	}

	switch recovery.Status {
	case "partial":
		limitations = append(limitations, "open recovery packets are release-impact evidence until reconciliation closes them")
	case "blocked":
		limitations = append(limitations, "recovery artifacts are malformed or stale; release impact is blocked until recovery evidence is repaired")
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
	SchemaVersion string                 `json:"schema_version"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Shared        p711SharedDemoContract `json:"shared_contract,omitempty"`
	Transcripts   []p711DemoTranscript   `json:"transcripts"`
}

type p711SharedDemoContract struct {
	SidecarSchemaVersion    string   `json:"sidecar_schema_version,omitempty"`
	DecisionBoundary        string   `json:"decision_boundary,omitempty"`
	GenericPrimitives       []string `json:"generic_primitives,omitempty"`
	SharedSecurityContracts []string `json:"shared_security_contracts,omitempty"`
	ForbiddenEvidenceFields []string `json:"forbidden_evidence_fields,omitempty"`
}

type p711DemoTranscript struct {
	ID                      string                   `json:"id"`
	Classification          string                   `json:"classification"`
	ClassificationLabel     string                   `json:"classification_label,omitempty"`
	Stance                  string                   `json:"stance"`
	P76CommandID            string                   `json:"p7_6_command_id"`
	ValueClaim              string                   `json:"value_claim,omitempty"`
	SellingPoint            string                   `json:"selling_point,omitempty"`
	GenericPrimitives       []string                 `json:"generic_primitives,omitempty"`
	SharedSecurityContracts []string                 `json:"shared_security_contracts,omitempty"`
	EvidenceFields          []string                 `json:"evidence_fields,omitempty"`
	MVPLimitation           string                   `json:"mvp_limitation"`
	FinalStateExpansion     string                   `json:"final_state_expansion,omitempty"`
	PrivacyPosture          string                   `json:"privacy_posture,omitempty"`
	PassNoGoCriteria        p711DemoPassNoGoCriteria `json:"pass_no_go_criteria,omitempty"`
	ReleaseClaimImpact      string                   `json:"release_claim_impact,omitempty"`
	Commands                []p711DemoCommand        `json:"commands,omitempty"`
}

type p711DemoPassNoGoCriteria struct {
	Pass []string `json:"pass,omitempty"`
	NoGo []string `json:"no_go,omitempty"`
}

type p711DemoCommand struct {
	ID                  string                  `json:"id"`
	Command             string                  `json:"command,omitempty"`
	Argv                []string                `json:"argv,omitempty"`
	ExitCode            int                     `json:"exit_code"`
	JSON                bool                    `json:"json,omitempty"`
	ExpectedOutputShape p711ExpectedOutputShape `json:"expected_output_shape,omitempty"`
}

type p711ExpectedOutputShape struct {
	StableFields   []string          `json:"stable_fields,omitempty"`
	JSONString     map[string]string `json:"json_string,omitempty"`
	JSONPresent    []string          `json:"json_present,omitempty"`
	JSONNonEmpty   []string          `json:"json_non_empty,omitempty"`
	JSONNull       []string          `json:"json_null,omitempty"`
	StdoutIncludes []string          `json:"stdout_includes,omitempty"`
	StdoutExcludes []string          `json:"stdout_excludes,omitempty"`
}

type p911RunnableCommandEvidence struct {
	Runnable             bool
	CommandID            string
	DeclaredCommandCount int
	SafeCommandCount     int
	Blockers             []string
}

var p911DemoCommandValidationCache = struct {
	sync.Mutex
	rows map[string]p911RunnableCommandEvidence
}{rows: map[string]p911RunnableCommandEvidence{}}

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

type p912PackagingSupportThesis struct {
	SchemaVersion        string                        `json:"schema_version"`
	ArtifactID           string                        `json:"artifact_id"`
	ICP                  p912ICPThesis                 `json:"icp"`
	Packaging            p912EvidenceText              `json:"packaging"`
	Pricing              p912PricingThesis             `json:"pricing"`
	Support              p912SupportThesis             `json:"support"`
	Admin                p912AdminThesis               `json:"admin"`
	LocalEvidenceExport  p912LocalEvidenceExportThesis `json:"local_evidence_export"`
	ClassificationStance []p912ClassificationStance    `json:"classification_stances"`
	Decision             p912PackagingSupportDecision  `json:"decision"`
}

type p912ICPThesis struct {
	PrimaryBuyer      string   `json:"primary_buyer"`
	AdoptionWedge     string   `json:"adoption_wedge"`
	Secondary         []string `json:"secondary"`
	DeferredProofOnly []string `json:"deferred_proof_only"`
	Buyer             string   `json:"buyer"`
	DailyUser         string   `json:"daily_user"`
	EvidenceIDs       []string `json:"evidence_ids"`
}

type p912EvidenceText struct {
	MVP         string   `json:"mvp"`
	FinalState  string   `json:"final_state"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type p912PricingThesis struct {
	Hypothesis    string   `json:"hypothesis"`
	SignalQuality string   `json:"signal_quality"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type p912SupportThesis struct {
	Boundary    string   `json:"boundary"`
	Level       string   `json:"level"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type p912AdminThesis struct {
	NonGoals                []string `json:"non_goals"`
	EnterpriseSurfaceLimits []string `json:"enterprise_surface_limits"`
	UnsupportedRequests     []string `json:"unsupported_requests"`
	UnsupportedRequestCount int      `json:"unsupported_request_count"`
	EvidenceIDs             []string `json:"evidence_ids"`
}

type p912LocalEvidenceExportThesis struct {
	Status         string   `json:"status"`
	PrivacyPosture string   `json:"privacy_posture"`
	EvidenceIDs    []string `json:"evidence_ids"`
}

type p912ClassificationStance struct {
	Classification       string   `json:"classification"`
	PackagingStance      string   `json:"packaging_stance"`
	MVPPackagingStance   string   `json:"mvp_packaging_stance"`
	FinalStateHypothesis string   `json:"final_state_hypothesis"`
	SupportAdminBoundary string   `json:"support_admin_boundary"`
	GoNoGoEvidence       string   `json:"go_no_go_evidence"`
	EvidenceIDs          []string `json:"evidence_ids"`
}

type p912PackagingSupportDecision struct {
	Outcome     string   `json:"outcome"`
	Trigger     string   `json:"trigger"`
	ClaimImpact string   `json:"claim_impact"`
	EvidenceIDs []string `json:"evidence_ids"`
}

func loadP711LocalOpsReport(root string, path string) (*localops.BenchmarkReport, productEvidenceSourceReference) {
	ref := productEvidenceSourceReference{ID: "local_ops_benchmark", Kind: "local_ops", Status: "not_available"}

	if strings.TrimSpace(path) == "" {
		path = filepath.Join(root, ".idiom", "local-ops", "report.json")
	}

	ref.PathID = localops.PathEvidence(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ref
	}

	ref.ArtifactHash = localops.ContentHash(data)

	var report localops.BenchmarkReport

	if err := json.Unmarshal(data, &report); err != nil {
		ref.Status = "blocked"
		return nil, ref
	}

	ref.Status = "present"

	return &report, ref
}

func loadP711IntegrationProofs(root string) (p711IntegrationProofSet, productEvidenceSourceReference) {
	path := filepath.Join(root, "testdata", "sidecar", "integration-proofs.json")
	ref := p711FileRef("p7.8_integration_proofs", "integration_proofs", path)

	var out p711IntegrationProofSet

	if jsonfile.ReadJSON(path, &out) != nil {
		return out, ref
	}

	ref.Status = "present"

	return out, ref
}

func loadP711SecurityPosture(root string) (p711SecurityPosture, productEvidenceSourceReference) {
	path := filepath.Join(root, "docs", "security-privacy-posture.matrix.json")
	ref := p711FileRef("p7.9_security_privacy_posture", "security_privacy", path)

	var out p711SecurityPosture

	if jsonfile.ReadJSON(path, &out) != nil {
		return out, ref
	}

	ref.Status = "present"
	if out.ArtifactID != "" {
		ref.ID = out.ArtifactID
	}

	return out, ref
}

func loadP711ClientDemoTranscripts(root string) (p711DemoTranscriptSet, productEvidenceSourceReference) {
	path := filepath.Join(root, "testdata", "sidecar", "client-demo-transcripts.json")
	ref := p711FileRef("p7.10_client_demo_transcripts", "client_demo_transcripts", path)

	var out p711DemoTranscriptSet

	data, err := os.ReadFile(path)
	if err != nil {
		return out, ref
	}

	ref.ArtifactHash = localops.ContentHash(data)

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		ref.Status = "blocked"
		return out, ref
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		ref.Status = "blocked"
		return out, ref
	}

	ref.Status = "present"

	return out, ref
}

func loadP711ValidationRecords(path string) ([]p711ValidationRecord, productEvidenceSourceReference) {
	ref := productEvidenceSourceReference{ID: "p0.8_validation_records", Kind: "validation_records", Status: "not_available"}
	if strings.TrimSpace(path) == "" {
		return nil, ref
	}

	ref.PathID = localops.PathEvidence(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ref
	}

	ref.ArtifactHash = localops.ContentHash(data)
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
		records[i].AdoptionSignalType = localops.SafeToken(records[i].AdoptionSignalType)
		records[i].RedactionStatus = p711SafeStatus(records[i].RedactionStatus)
		records[i].ParticipantContextID = p711SafeID(records[i].ParticipantContextID, "context")
		records[i].PrimaryICPFit = localops.SafeToken(records[i].PrimaryICPFit)
		records[i].ActiveAgentUse = localops.SafeToken(records[i].ActiveAgentUse)
		records[i].RepoWorkflowAvailable = localops.SafeToken(records[i].RepoWorkflowAvailable)
		records[i].BlockedWorkflowStatus = localops.SafeToken(records[i].BlockedWorkflowStatus)
		records[i].DailyUserActivationStatus = localops.SafeToken(records[i].DailyUserActivationStatus)
		records[i].RetainedUseEvidence = localops.SafeToken(records[i].RetainedUseEvidence)
		records[i].SubstrateValueStatus = localops.SafeToken(records[i].SubstrateValueStatus)
		records[i].BuyerBudgetSignal = localops.SafeToken(records[i].BuyerBudgetSignal)
		records[i].DisqualifierStatus = localops.SafeToken(records[i].DisqualifierStatus)
	}

	return records, ref
}

func loadP711LearningSummary(root string) (*comprehension.LearningLogSummary, productEvidenceSourceReference) {
	path := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")
	ref := productEvidenceSourceReference{ID: "ranker_learning_log", Kind: "learning", Status: "not_available", PathID: localops.PathEvidence(path)}
	entries, err := comprehension.ReadLearningLog(context.Background(), root)

	if err != nil || len(entries) == 0 {
		return nil, ref
	}

	summary := comprehension.SummarizeLearningLog(entries)

	ref.Status = "present"
	ref.ArtifactHash = localops.StableID(fmt.Sprintf("%d:%d:%d", summary.TotalEntries, summary.AcceptedCount, summary.RejectedCount))

	return &summary, ref
}

func loadP912PackagingSupportThesis(root string) (p912PackagingSupportThesis, productEvidenceSourceReference) {
	path := filepath.Join(root, "docs", "packaging-support-thesis.json")
	ref := p711FileRef("p0.11_packaging_support_thesis", "packaging_support_thesis", path)

	var out p912PackagingSupportThesis

	data, err := os.ReadFile(path)
	if err != nil {
		return out, ref
	}

	ref.ArtifactHash = localops.ContentHash(data)

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		ref.Status = "blocked"
		return out, ref
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		ref.Status = "blocked"
		return out, ref
	}

	ref.Status = "present"
	if strings.TrimSpace(out.ArtifactID) != "" {
		ref.ID = out.ArtifactID
	}

	return out, ref
}

func buildP711Signals(localOps *localops.BenchmarkReport, records []p711ValidationRecord, integration p711IntegrationProofSet, security p711SecurityPosture, learning *comprehension.LearningLogSummary) []productEvidenceSignalSummary {
	return []productEvidenceSignalSummary{
		p711ActivationSignal(localOps),
		p711RetainedUseSignal(records),
		p711IntegrationSignal(integration),
		p711BenchmarkSignal(localOps),
		p711AuditSignal(localOps, security),
		p711LearningSignal(learning),
	}
}

func p711ActivationSignal(localOps *localops.BenchmarkReport) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "activation", Status: "not_available", Completeness: "missing", SourceRefs: []string{"local_ops_benchmark"}, RedactionStatus: "redacted", ReleaseGates: []string{"P0.10", "P7.7", "P7.16", "P9.6"}, Counts: map[string]int{}}
	if localOps == nil || localOps.Quickstart == nil {
		s.Limitations = []string{"quickstart activation smoke not supplied"}
		return s
	}

	q := localOps.Quickstart

	s.Status = q.Status
	s.Completeness = p711Completeness(q.Status)
	s.EvidenceIDs = localops.CompactStrings(q.SmokeID, q.ProductEvidenceSafeOutput.ExportID, q.FirstUsefulPacket.PacketID, q.FirstUsefulPacket.IndexID)
	s.Counts["candidate_files"] = q.FirstUsefulPacket.CandidateFileCount
	s.Counts["candidate_symbols"] = q.FirstUsefulPacket.CandidateSymbolCount
	s.Counts["candidate_tests"] = q.FirstUsefulPacket.CandidateTestCount
	s.Counts["useful_evidence_count"] = localops.ActivationPacketUsefulEvidenceCount(q.FirstUsefulPacket)

	if !localops.ActivationPacketHasUsefulEvidence(q.FirstUsefulPacket) {
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

func p711RetainedUseSignal(records []p711ValidationRecord) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "retained_use", Status: "not_available", Completeness: "missing", SourceRefs: []string{"p0.8_validation_records"}, RedactionStatus: "redacted", ReleaseGates: []string{"P0.8", "P7.11", "P9.7"}, Counts: map[string]int{"validation_records": len(records), "qualifying_records": 0}}
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
	switch localops.SafeToken(value) {
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
	switch localops.SafeToken(value) {
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
	switch localops.SafeToken(value) {
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
	switch localops.SafeToken(value) {
	case "yes", "true", "pass", "observed":
		return true
	default:
		return false
	}
}

func p97TokenIsPresent(value string) bool {
	switch localops.SafeToken(value) {
	case "", "none", "not_available", "not_applicable", "n_a", "na", "no_signal", "absent", "unavailable", "missing", "no":
		return false
	default:
		return true
	}
}

func p97RepoWorkflowQualifies(value string) bool {
	switch localops.SafeToken(value) {
	case "real", "representative":
		return true
	default:
		return false
	}
}

func p97RecordDisqualified(record p711ValidationRecord) bool {
	switch localops.SafeToken(record.DisqualifierStatus) {
	case "", "none", "not_available", "not_applicable", "n_a", "na", "absent", "no":
		return false
	default:
		return true
	}
}

func p711IntegrationSignal(proofs p711IntegrationProofSet) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "integration_proof", Status: "not_available", Completeness: "missing", SourceRefs: []string{"p7.8_integration_proofs"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.8", "P8.6", "P9.8"}, Counts: map[string]int{}}
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
			s.Counts["path_"+localops.SafeToken(proof.Path)]++
		}

		s.Counts["executable_paths"]++

		if p711IsRequiredIntegrationPath(proof.Path) {
			requiredExecutable[proof.Path] = true
			s.Counts["executable_required_paths"] = len(requiredExecutable)
			s.Counts["executable_required_path_"+localops.SafeToken(proof.Path)] = 1
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
		s.Limitations = localops.CompactStrings(append(s.Limitations, "one or more integration paths are reviewed placeholders")...)
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

func p711BenchmarkSignal(localOps *localops.BenchmarkReport) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "benchmark", Status: "not_available", Completeness: "missing", SourceRefs: []string{"local_ops_benchmark"}, RedactionStatus: "redacted", ReleaseGates: []string{"P6.5", "P7.11", "P9.1", "P9.10"}, Counts: map[string]int{}, Limitations: []string{"local-ops benchmark is readiness/replay evidence; external competitive baselines remain P9.10 scope"}}
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

type p910CompetitiveBenchmarkEvidence struct {
	Conditions []idiomeval.LocalizationCompetitiveConditionMetric
	Findings   []string
}

func loadP910CompetitiveBenchmarkEvidence(root string, path string) (p910CompetitiveBenchmarkEvidence, productEvidenceSourceReference) {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(root, "testdata", "localization", "recent-implementation-corpus.json")
	} else {
		path = resolveReleaseEvidencePath(root, path)
	}

	ref := p711FileRef("p4.6_localization_competitive_benchmark", "competitive_benchmark", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return p910CompetitiveBenchmarkEvidence{}, ref
	}

	ref.Status = "present"
	ref.ArtifactHash = localops.ContentHash(data)

	evidence, err := decodeP910CompetitiveBenchmarkEvidence(data)
	if err != nil {
		ref.Status = "blocked"

		evidence.Findings = append(evidence.Findings, err.Error())
	}

	return evidence, ref
}

func decodeP910CompetitiveBenchmarkEvidence(data []byte) (p910CompetitiveBenchmarkEvidence, error) {
	var corpus idiomeval.LocalizationCorpus

	if err := json.Unmarshal(data, &corpus); err == nil && strings.TrimSpace(corpus.Name) != "" && len(corpus.Tasks) > 0 {
		report := idiomeval.AnalyzeLocalizationCorpusWithOptions(corpus, idiomeval.LocalizationBenchmarkOptions{TopK: []int{1}})

		return p910CompetitiveBenchmarkEvidence{
			Conditions: report.Benchmark.CompetitiveConditions,
			Findings:   idiomeval.LintLocalizationCorpus(corpus),
		}, nil
	}

	var report struct {
		Status                string                                             `json:"status"`
		SchemaVersion         string                                             `json:"schema_version"`
		CompetitiveConditions []idiomeval.LocalizationCompetitiveConditionMetric `json:"competitive_conditions"`
		Outcome               struct {
			CompetitiveConditions []idiomeval.LocalizationCompetitiveConditionMetric `json:"competitive_conditions"`
		} `json:"outcome"`
		Benchmark struct {
			CompetitiveConditions []idiomeval.LocalizationCompetitiveConditionMetric `json:"competitive_conditions"`
		} `json:"benchmark"`
	}

	if err := json.Unmarshal(data, &report); err != nil {
		return p910CompetitiveBenchmarkEvidence{}, fmt.Errorf("malformed competitive benchmark JSON")
	}

	conditions := report.Outcome.CompetitiveConditions
	if len(conditions) == 0 {
		conditions = report.Benchmark.CompetitiveConditions
	}

	if len(conditions) == 0 {
		conditions = report.CompetitiveConditions
	}

	evidence := p910CompetitiveBenchmarkEvidence{Conditions: conditions}

	switch localops.SafeToken(report.Status) {
	case "stale", "no_go", "nogo", "incomplete", "blocked", "fail", "failed":
		evidence.Findings = append(evidence.Findings, "competitive benchmark result status is "+localops.SafeToken(report.Status))
	}

	if len(conditions) == 0 {
		evidence.Findings = append(evidence.Findings, "competitive benchmark conditions missing")
	}

	return evidence, nil
}

func p910CompetitiveBenchmarkGate(evidence p910CompetitiveBenchmarkEvidence, ref productEvidenceSourceReference) productEvidenceReleaseGateLinkage {
	required := idiomeval.RequiredLocalizationExternalCompetitiveBaselineIDs()
	known := map[string]bool{}

	for _, baseline := range idiomeval.LocalizationCompetitiveBaselineIDs() {
		known[baseline] = true
	}

	counts := map[string]int{
		"required_external_baselines":              len(required),
		"baseline_rows":                            len(evidence.Conditions),
		"covered_external_baselines":               0,
		"measured_external_baselines":              0,
		"documented_infeasible_external_baselines": 0,
		"missing_external_baselines":               0,
		"malformed_baseline_rows":                  0,
		"self_referential_baselines":               0,
		"external_baseline_rows":                   0,
		"external_measured_tasks":                  0,
		"external_infeasible_tasks":                0,
		"external_missing_tasks":                   0,
		"external_invalid_client_classifications":  0,
		"target_client_coverage_baselines":         0,
	}

	var (
		blockers    []string
		evidenceIDs []string
	)

	if ref.Status == "present" {
		evidenceIDs = append(evidenceIDs, ref.ID)
	} else if ref.Status == "blocked" {
		blockers = append(blockers, "competitive benchmark artifact is malformed")
	} else {
		blockers = append(blockers, "competitive benchmark artifact not available")
	}

	for _, finding := range evidence.Findings {
		blockers = append(blockers, "benchmark evidence finding: "+localops.SafeText(finding))
	}

	byBaseline := map[string]idiomeval.LocalizationCompetitiveConditionMetric{}
	selfReferentialEvidence := 0

	for _, condition := range evidence.Conditions {
		baseline := localops.SafeToken(condition.Baseline)
		if baseline == "" || !known[baseline] {
			counts["malformed_baseline_rows"]++

			blockers = append(blockers, "malformed or unsupported competitive baseline row")

			continue
		}

		if !p910RequiredBaseline(required, baseline) {
			if condition.MeasuredTasks+condition.InfeasibleTasks > 0 {
				selfReferentialEvidence++
			}

			continue
		}

		byBaseline[baseline] = condition
	}

	counts["self_referential_baselines"] = selfReferentialEvidence

	for _, baseline := range required {
		condition, ok := byBaseline[baseline]
		if !ok {
			counts["missing_external_baselines"]++

			blockers = append(blockers, "missing external competitive baseline: "+baseline)

			continue
		}

		counts["external_baseline_rows"]++

		counts["external_measured_tasks"] += condition.MeasuredTasks
		counts["external_infeasible_tasks"] += condition.InfeasibleTasks
		counts["external_missing_tasks"] += condition.MissingTasks
		counts["external_invalid_client_classifications"] += condition.InvalidClientClasses + p910UnsupportedClientCoverageCount(condition.ClientCoverage)

		if p910CompetitiveConditionHasTargetClientCoverage(condition) {
			counts["target_client_coverage_baselines"]++
		}

		if condition.MeasuredTasks > 0 {
			counts["measured_external_baselines"]++
		}

		if condition.InfeasibleTasks > 0 {
			counts["documented_infeasible_external_baselines"]++
		}

		if p910CompetitiveConditionCovered(condition) {
			counts["covered_external_baselines"]++

			evidenceIDs = append(evidenceIDs, "competitive_baseline:"+baseline)

			continue
		}

		blockers = append(blockers, p910CompetitiveConditionBlocker(condition))
	}

	if counts["covered_external_baselines"] == 0 && selfReferentialEvidence > 0 {
		blockers = append(blockers, "competitive benchmark evidence is self-referential only; external baselines are required")
	}

	status := "needs_evidence"
	claimImpact := "competitive readiness and productivity claims remain no-go until each external baseline is measured or documented infeasible with claim impact"

	if len(blockers) == 0 && counts["covered_external_baselines"] == len(required) {
		status = "evidence_present"
		claimImpact = "competitive claims may cite only the covered external-baseline evidence and documented limitations"
	}

	return productEvidenceReleaseGateLinkage{
		Gate:        "P9.10",
		Status:      status,
		Evidence:    fmt.Sprintf("required_external_baselines=%d; covered_external_baselines=%d; measured_external_baselines=%d; documented_infeasible_external_baselines=%d", counts["required_external_baselines"], counts["covered_external_baselines"], counts["measured_external_baselines"], counts["documented_infeasible_external_baselines"]),
		ClaimImpact: claimImpact,
		Counts:      counts,
		Blockers:    localops.CompactStrings(blockers...),
		EvidenceIDs: localops.CompactStrings(evidenceIDs...),
	}
}

func p910CompetitiveConditionCovered(condition idiomeval.LocalizationCompetitiveConditionMetric) bool {
	covered := condition.MeasuredTasks + condition.InfeasibleTasks
	if covered == 0 || condition.TotalTasks == 0 || condition.MissingTasks > 0 || condition.InvalidClientClasses > 0 || p910UnsupportedClientCoverageCount(condition.ClientCoverage) > 0 {
		return false
	}

	if !p910CompetitiveConditionHasTargetClientCoverage(condition) {
		return false
	}

	if condition.CommandedTasks < covered || condition.ArtifactedTasks < covered || condition.VerifiedTasks < covered || condition.ReproducibleTasks < covered {
		return false
	}

	if condition.MeasuredTasks > 0 {
		if condition.BudgetedTasks < condition.MeasuredTasks || len(condition.TopKEditPathCoverage) == 0 {
			return false
		}

		for _, coverage := range condition.TopKEditPathCoverage {
			if !coverage.Available {
				return false
			}
		}
	}

	if condition.InfeasibleTasks > 0 && (len(condition.InfeasibilityReasons) == 0 || len(condition.ClaimImpacts) == 0) {
		return false
	}

	return len(condition.FailureReasons) == 0 && p910CompetitiveStatusCovers(condition.Status)
}

func p910CompetitiveConditionBlocker(condition idiomeval.LocalizationCompetitiveConditionMetric) string {
	baseline := localops.SafeToken(condition.Baseline)
	covered := condition.MeasuredTasks + condition.InfeasibleTasks

	switch {
	case covered == 0:
		return "external competitive baseline has no measured result or documented infeasibility: " + baseline
	case condition.MissingTasks > 0:
		return "external competitive baseline is missing task coverage: " + baseline
	case !p910CompetitiveStatusCovers(condition.Status):
		return "external competitive baseline has stale, no-go, incomplete, or unsupported status: " + baseline
	case condition.InvalidClientClasses > 0 || p910UnsupportedClientCoverageCount(condition.ClientCoverage) > 0:
		return "external competitive baseline has unsupported client classifications: " + baseline
	case !p910CompetitiveConditionHasTargetClientCoverage(condition):
		return "external competitive baseline is missing enterprise-saas/open-source/tooling-vendor client coverage: " + baseline
	case condition.CommandedTasks < covered:
		return "external competitive baseline is missing command ID evidence: " + baseline
	case condition.ArtifactedTasks < covered:
		return "external competitive baseline is missing artifact evidence: " + baseline
	case condition.VerifiedTasks < covered:
		return "external competitive baseline is missing verification method evidence: " + baseline
	case condition.ReproducibleTasks < covered:
		return "external competitive baseline is missing reproducibility evidence: " + baseline
	case condition.MeasuredTasks > 0 && condition.BudgetedTasks < condition.MeasuredTasks:
		return "external competitive baseline is missing fair budget evidence: " + baseline
	case condition.MeasuredTasks > 0 && len(condition.TopKEditPathCoverage) == 0:
		return "external competitive baseline measured result is incomplete: " + baseline
	case condition.InfeasibleTasks > 0 && len(condition.InfeasibilityReasons) == 0:
		return "external competitive baseline infeasibility reason missing: " + baseline
	case condition.InfeasibleTasks > 0 && len(condition.ClaimImpacts) == 0:
		return "external competitive baseline claim impact missing: " + baseline
	case len(condition.FailureReasons) > 0:
		return "external competitive baseline has benchmark failure reasons: " + baseline + ":" + strings.Join(condition.FailureReasons, ",")
	default:
		return "external competitive baseline needs evidence: " + baseline
	}
}

func p910CompetitiveStatusCovers(status string) bool {
	switch localops.SafeToken(status) {
	case "measured", "documented_infeasible", "mixed_measured_infeasible":
		return true
	default:
		return false
	}
}

func p910CompetitiveConditionHasTargetClientCoverage(condition idiomeval.LocalizationCompetitiveConditionMetric) bool {
	for _, class := range []string{"enterprise_saas", "open_source", "tooling_vendor"} {
		if condition.ClientCoverage[class] > 0 {
			return true
		}
	}

	return false
}

func p910UnsupportedClientCoverageCount(coverage map[string]int) int {
	allowed := map[string]bool{
		"finance":                  true,
		"healthcare":               true,
		"healthcare_life_sciences": true,
		"public_sector":            true,
		"enterprise_saas":          true,
		"tooling_vendor":           true,
		"local_model":              true,
		"local_model_private_code": true,
		"open_source":              true,
	}
	unsupported := 0

	for class := range coverage {
		if !allowed[localops.SafeToken(class)] {
			unsupported++
		}
	}

	return unsupported
}

func p910RequiredBaseline(required []string, baseline string) bool {
	for _, value := range required {
		if value == baseline {
			return true
		}
	}

	return false
}

func p911ClientDemoGateWithDeps(demos p711DemoTranscriptSet, ref productEvidenceSourceReference, deps Dependencies) productEvidenceReleaseGateLinkage {
	required := p711Classifications()
	counts := map[string]int{
		"required_classifications":           len(required),
		"transcript_rows":                    len(demos.Transcripts),
		"covered_classifications":            0,
		"missing_classifications":            0,
		"duplicate_classifications":          0,
		"unsupported_classifications":        0,
		"malformed_transcripts":              0,
		"runnable_command_rows":              0,
		"expected_output_rows":               0,
		"evidence_field_rows":                0,
		"mvp_limitation_rows":                0,
		"final_state_expansion_rows":         0,
		"privacy_posture_rows":               0,
		"go_no_go_criteria_rows":             0,
		"release_claim_impact_rows":          0,
		"value_claim_rows":                   0,
		"regulated_proof_only_rows":          0,
		"primary_enterprise_saas_rows":       0,
		"declared_command_rows":              0,
		"safe_replayed_command_rows":         0,
		"command_id_mismatch_rows":           0,
		"command_execution_blockers":         0,
		"privacy_or_forbidden_blockers":      0,
		"regulated_or_commercial_overclaims": 0,
	}

	var (
		blockers    []string
		evidenceIDs []string
	)

	addBlocker := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}

		blockers = append(blockers, reason)
	}

	if ref.Status == "present" {
		evidenceIDs = append(evidenceIDs, ref.ID)
	} else if ref.Status == "blocked" {
		addBlocker("client demo transcript artifact is malformed")

		counts["malformed_transcripts"]++
	} else {
		addBlocker("client demo transcript artifact not available")
	}

	if demos.SchemaVersion != "idiom.client_demo_transcripts.v1" || demos.Name != "p7.10-client-demo-transcripts" {
		addBlocker("client demo transcript identity is missing or unexpected")

		counts["malformed_transcripts"]++
	}

	if demos.Shared.SidecarSchemaVersion != "" && demos.Shared.SidecarSchemaVersion != "idiom.tool.v1" {
		addBlocker("client demo shared sidecar schema version is unexpected")

		counts["malformed_transcripts"]++
	}

	requiredSet := map[string]bool{}
	for _, class := range required {
		requiredSet[class] = true
	}

	seen := map[string]int{}

	for _, demo := range demos.Transcripts {
		class := strings.TrimSpace(demo.Classification)
		if !requiredSet[class] {
			counts["unsupported_classifications"]++

			addBlocker("unsupported client demo classification: " + firstNonEmptyString(class, "missing"))

			continue
		}

		seen[class]++
		if seen[class] > 1 {
			counts["duplicate_classifications"]++

			addBlocker("duplicate client demo classification: " + class)

			continue
		}

		evidenceIDs = append(evidenceIDs, demo.ID, demo.P76CommandID)
		p911EvaluateDemoTranscript(demo, counts, addBlocker, deps)
	}

	for _, class := range required {
		if seen[class] == 0 {
			counts["missing_classifications"]++

			addBlocker("missing client demo classification: " + class)

			continue
		}

		counts["covered_classifications"]++
	}

	status := "needs_evidence"
	claimImpact := "client demo and release claims remain no-go until every classification has runnable, privacy-safe transcript evidence with MVP and final-state limits"

	if len(blockers) == 0 && counts["covered_classifications"] == counts["required_classifications"] {
		status = "evidence_present"
		claimImpact = "client demo claims may cite runnable transcript evidence with explicit MVP limits and final-state expansion; regulated and commercial readiness claims remain bounded by their proof-only or primary-ICP gates"
	}

	counts["blockers"] = len(blockers)

	return productEvidenceReleaseGateLinkage{
		Gate:        "P9.11",
		Status:      status,
		Evidence:    fmt.Sprintf("classes:%d/%d runnable:%d expected_output:%d blockers:%d", counts["covered_classifications"], counts["required_classifications"], counts["runnable_command_rows"], counts["expected_output_rows"], counts["blockers"]),
		ClaimImpact: claimImpact,
		Counts:      counts,
		Blockers:    localops.CompactStrings(blockers...),
		EvidenceIDs: localops.CompactStrings(evidenceIDs...),
	}
}

func p911EvaluateDemoTranscript(demo p711DemoTranscript, counts map[string]int, addBlocker func(string), deps Dependencies) {
	class := strings.TrimSpace(demo.Classification)
	malformed := false
	require := func(ok bool, countKey string, blocker string) {
		if ok {
			counts[countKey]++
			return
		}

		malformed = true

		addBlocker(blocker + ": " + class)
	}
	require(strings.TrimSpace(demo.ValueClaim) != "", "value_claim_rows", "missing value claim")
	require(p911NonBlankList(demo.EvidenceFields), "evidence_field_rows", "missing evidence fields")
	require(strings.TrimSpace(demo.MVPLimitation) != "", "mvp_limitation_rows", "missing MVP limitation")
	require(strings.TrimSpace(demo.FinalStateExpansion) != "", "final_state_expansion_rows", "missing final-state expansion")
	require(p911PrivacyPostureExplicit(demo.PrivacyPosture), "privacy_posture_rows", "missing privacy/no-forbidden-field posture")
	require(p911NonBlankList(demo.PassNoGoCriteria.Pass) && p911NonBlankList(demo.PassNoGoCriteria.NoGo), "go_no_go_criteria_rows", "missing go/no-go criteria")
	require(strings.TrimSpace(demo.ReleaseClaimImpact) != "", "release_claim_impact_rows", "missing release claim impact")

	commandEvidence := p911DemoRunnableCommandEvidenceWithDeps(demo, deps)

	counts["declared_command_rows"] += commandEvidence.DeclaredCommandCount

	if commandEvidence.Runnable {
		counts["runnable_command_rows"]++

		counts["safe_replayed_command_rows"] += commandEvidence.SafeCommandCount

		if commandEvidence.CommandID != "" {
			counts["executable_command_evidence_rows"]++
		}
	} else {
		malformed = true

		if len(commandEvidence.Blockers) == 0 {
			addBlocker("missing runnable command: " + class)
		}

		for _, blocker := range commandEvidence.Blockers {
			lower := strings.ToLower(blocker)
			if strings.Contains(lower, "command id parity") {
				counts["command_id_mismatch_rows"]++
			} else {
				counts["command_execution_blockers"]++

				blocker = "command executable validation failed: " + blocker
			}

			addBlocker("missing runnable command: " + class + ":" + blocker)
		}
	}

	if p911DemoHasExpectedOutput(demo) {
		counts["expected_output_rows"]++
	} else {
		malformed = true

		addBlocker("missing expected output: " + class)
	}

	if blank := p911BlankRequiredListItem(demo); blank != "" {
		malformed = true

		addBlocker("blank client demo required list item: " + class + ":" + blank)
	}

	combined := p911DemoText(demo)
	if forbidden := p911ForbiddenDemoEvidence(combined); forbidden != "" {
		counts["privacy_or_forbidden_blockers"]++

		malformed = true

		addBlocker("client demo contains forbidden evidence marker: " + class + ":" + forbidden)
	}

	if overclaim := p911DemoOverclaim(combined); overclaim != "" {
		counts["regulated_or_commercial_overclaims"]++

		malformed = true

		addBlocker("client demo implies unsupported regulated or commercial readiness: " + class + ":" + overclaim)
	}

	if p99RegulatedClass(class) {
		if p911RegulatedDemoProofOnly(demo) {
			counts["regulated_proof_only_rows"]++
		} else {
			malformed = true

			addBlocker("regulated client demo is not explicitly proof-only: " + class)
		}
	}

	if class == "enterprise-saas" {
		if p911EnterpriseSaaSPrimaryStory(demo) {
			counts["primary_enterprise_saas_rows"]++
		} else {
			malformed = true

			addBlocker("enterprise-saas demo does not preserve primary buyer story")
		}
	}

	if malformed {
		counts["malformed_transcripts"]++
	}
}

func p911DemoRunnableCommandEvidenceWithDeps(demo p711DemoTranscript, deps Dependencies) p911RunnableCommandEvidence {
	key := p911DemoCommandValidationCacheKey(demo, deps)

	if key != "" {
		p911DemoCommandValidationCache.Lock()

		cached, ok := p911DemoCommandValidationCache.rows[key]
		p911DemoCommandValidationCache.Unlock()

		if ok {
			return cached
		}
	}

	result := p911DemoRunnableCommandEvidenceUncached(demo, deps)

	if key != "" {
		p911DemoCommandValidationCache.Lock()

		p911DemoCommandValidationCache.rows[key] = result
		p911DemoCommandValidationCache.Unlock()
	}

	return result
}

func p911DemoCommandValidationCacheKey(demo p711DemoTranscript, deps Dependencies) string {
	data, err := json.Marshal(demo)
	if err != nil {
		return ""
	}

	runnerID := deps.DemoCommandRunnerID
	if runnerID == "" {
		runnerID = "default"
	}

	return localops.ContentHash(append(data, []byte("\nrunner:"+runnerID)...))
}

func p911DemoRunnableCommandEvidenceUncached(demo p711DemoTranscript, deps Dependencies) p911RunnableCommandEvidence {
	var (
		blockers       []string
		safeCommandIDs []string
	)

	for _, command := range demo.Commands {
		if shapeBlockers := p911DemoCommandShapeBlockers(demo, command); len(shapeBlockers) > 0 {
			blockers = append(blockers, p911DemoCommandScopedBlockers(command, shapeBlockers)...)
			continue
		}

		if runBlockers := p911ExecuteDemoCommandWithDeps(demo, command, deps); len(runBlockers) > 0 {
			blockers = append(blockers, p911DemoCommandScopedBlockers(command, runBlockers)...)
			continue
		}

		safeCommandIDs = append(safeCommandIDs, strings.TrimSpace(command.ID))
	}

	result := p911RunnableCommandEvidence{
		DeclaredCommandCount: len(demo.Commands),
		SafeCommandCount:     len(safeCommandIDs),
		Blockers:             localops.CompactStrings(blockers...),
	}
	if len(blockers) == 0 && len(safeCommandIDs) > 0 {
		result.Runnable = true
		result.CommandID = safeCommandIDs[0]
	}

	return result
}

func p911DemoCommandScopedBlockers(command p711DemoCommand, blockers []string) []string {
	commandID := firstNonEmptyString(strings.TrimSpace(command.ID), "missing_command_id")
	out := make([]string, 0, len(blockers))

	for _, blocker := range blockers {
		out = append(out, "command "+commandID+": "+blocker)
	}

	return out
}

func p911DemoCommandShapeBlockers(demo p711DemoTranscript, command p711DemoCommand) []string {
	var blockers []string

	commandID := strings.TrimSpace(command.ID)

	if commandID == "" {
		blockers = append(blockers, "command ID missing")
	}

	if want := strings.TrimSpace(demo.P76CommandID); want == "" {
		blockers = append(blockers, "p7_6 command ID missing")
	} else if commandID != "" && commandID != want {
		blockers = append(blockers, "command ID parity mismatch: command "+commandID+" does not match p7_6_command_id "+want)
	}

	if text := strings.TrimSpace(command.Command); text == "" {
		blockers = append(blockers, "command text missing")
	} else if !strings.HasPrefix(text, "idiom ") {
		blockers = append(blockers, "command text must start with idiom")
	} else if parityBlocker := p911CommandArgvParityBlocker(text, command.Argv); parityBlocker != "" {
		blockers = append(blockers, parityBlocker)
	}

	if !p911NonBlankList(command.Argv) {
		blockers = append(blockers, "argv missing or contains blank item")
	}

	if p911ExpectedOutputHasBlankItem(command.ExpectedOutputShape) {
		blockers = append(blockers, "expected output shape contains blank item")
	} else if !p911ExpectedOutputHasField(command.ExpectedOutputShape) {
		blockers = append(blockers, "expected output shape missing")
	}

	if allowBlockers := p911DemoCommandAllowlistBlockers(command); len(allowBlockers) > 0 {
		blockers = append(blockers, allowBlockers...)
	}

	return blockers
}

func p911CommandArgvParityBlocker(command string, argv []string) string {
	tokens, err := p911SplitDemoCommand(command)
	if err != nil {
		return "command/argv parity mismatch: command string is not parseable: " + err.Error()
	}

	if len(tokens) == 0 || tokens[0] != "idiom" {
		return "command/argv parity mismatch: command must start with idiom executable"
	}

	actual := tokens[1:]
	if len(actual) != len(argv) {
		return fmt.Sprintf("command/argv parity mismatch: command has %d args but argv has %d", len(actual), len(argv))
	}

	for i := range actual {
		if actual[i] != argv[i] {
			return fmt.Sprintf("command/argv parity mismatch: arg %d command=%q argv=%q", i, actual[i], argv[i])
		}
	}

	return ""
}

func p911SplitDemoCommand(command string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
	)

	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for _, r := range strings.TrimSpace(command) {
		if escaped {
			current.WriteRune(r)

			escaped = false

			continue
		}

		if quote != '\'' && r == '\\' {
			escaped = true
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}

			current.WriteRune(r)

			continue
		}

		switch {
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}

	flush()

	return tokens, nil
}

func p911DemoCommandAllowlistBlockers(command p711DemoCommand) []string {
	argv := append([]string(nil), command.Argv...)
	if len(argv) == 0 {
		return nil
	}

	switch argv[0] {
	case "tool":
		return p911ToolDemoCommandAllowlistBlockers(command, argv[1:])
	case "local-ops":
		return p911LocalOpsDemoCommandAllowlistBlockers(argv[1:])
	default:
		return []string{"command allowlist rejected unsafe top-level subcommand: " + argv[0]}
	}
}

func p911ToolDemoCommandAllowlistBlockers(command p711DemoCommand, args []string) []string {
	if len(args) == 0 {
		return []string{"command allowlist rejected missing tool name"}
	}

	tool := args[0]
	flags := args[1:]

	switch tool {
	case "schemas":
		if len(flags) != 0 {
			return []string{"command allowlist rejected schemas flags"}
		}

		return nil
	case "context_pack":
		return p911RequireExactToolFlags(tool, flags, map[string]string{
			"-config":      "$IDIOM_CONFIG",
			"--profile":    "discovery",
			"--caller-id":  "",
			"--request-id": "",
			"--input":      "",
		})
	case "source_sanity":
		return p911RequireExactToolFlags(tool, flags, map[string]string{
			"-config":      "$IDIOM_CONFIG",
			"--caller-id":  "",
			"--request-id": "",
			"--trace-id":   "",
			"--input":      "",
		})
	case "write_file", "shell":
		if command.ExitCode == 0 {
			return []string{"command allowlist rejected side-effect example without denied exit"}
		}

		blockers := p911RequireExactToolFlags(tool, flags, map[string]string{
			"-config":      "$IDIOM_CONFIG",
			"--profile":    "discovery",
			"--caller-id":  "",
			"--request-id": "",
			"--input":      "",
		})
		if p911FlagValue(flags, "--profile") != "discovery" {
			blockers = append(blockers, "command allowlist rejected "+tool+" outside discovery-profile denial")
		}

		return blockers
	default:
		return []string{"command allowlist rejected unsafe tool: " + tool}
	}
}

func p911RequireExactToolFlags(tool string, flags []string, allowed map[string]string) []string {
	var blockers []string

	seen := map[string]bool{}

	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		want, ok := allowed[flag]

		if !ok {
			blockers = append(blockers, "command allowlist rejected unsafe flag for "+tool+": "+flag)
			continue
		}

		if seen[flag] {
			blockers = append(blockers, "command allowlist rejected duplicate flag for "+tool+": "+flag)
		}

		seen[flag] = true
		if i+1 >= len(flags) {
			blockers = append(blockers, "command allowlist rejected missing value for "+tool+": "+flag)
			continue
		}

		value := flags[i+1]
		i++

		if want != "" && value != want {
			blockers = append(blockers, "command allowlist rejected unsafe value for "+tool+": "+flag)
		}
	}

	for flag, want := range allowed {
		if want != "" && !seen[flag] {
			blockers = append(blockers, "command allowlist rejected missing required flag for "+tool+": "+flag)
		}
	}

	return blockers
}

func p911LocalOpsDemoCommandAllowlistBlockers(args []string) []string {
	if len(args) == 0 || args[0] != "benchmark" {
		return []string{"command allowlist rejected unsafe local-ops subcommand"}
	}

	flags := args[1:]
	profile := p911FlagValue(flags, "--profile")

	if profile != "implement" && profile != "sidecar" {
		return []string{"command allowlist rejected local-ops without bounded P7.10 profile"}
	}

	var blockers []string

	seenBool := map[string]bool{}
	seenValue := map[string]bool{}

	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		switch flag {
		case "--json", "--quickstart-smoke":
			if seenBool[flag] {
				blockers = append(blockers, "command allowlist rejected duplicate flag for local-ops: "+flag)
			}

			seenBool[flag] = true
		case "--config", "--profile", "--quickstart-task", "--quickstart-cleanup":
			if seenValue[flag] {
				blockers = append(blockers, "command allowlist rejected duplicate flag for local-ops: "+flag)
			}

			seenValue[flag] = true
			if i+1 >= len(flags) {
				blockers = append(blockers, "command allowlist rejected missing value for local-ops: "+flag)
				continue
			}

			value := flags[i+1]
			i++

			switch flag {
			case "--config":
				if value != "$IDIOM_CONFIG" {
					blockers = append(blockers, "command allowlist rejected unsafe value for local-ops: "+flag)
				}
			case "--quickstart-cleanup":
				if value != "remove-smoke-artifacts" {
					blockers = append(blockers, "command allowlist rejected unsafe local-ops cleanup")
				}
			}
		default:
			blockers = append(blockers, "command allowlist rejected unsafe flag for local-ops: "+flag)
		}
	}

	if !seenBool["--json"] || !seenValue["--config"] || !seenValue["--profile"] {
		blockers = append(blockers, "command allowlist rejected incomplete local-ops benchmark shape")
	}

	if profile == "sidecar" && (!seenBool["--quickstart-smoke"] || !seenValue["--quickstart-task"] || !seenValue["--quickstart-cleanup"]) {
		blockers = append(blockers, "command allowlist rejected incomplete quickstart smoke shape")
	}

	return blockers
}

func p911FlagValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}

	return ""
}

func p911ExecuteDemoCommandWithDeps(demo p711DemoTranscript, command p711DemoCommand, deps Dependencies) []string {
	root, err := os.MkdirTemp("", "idiom-p911-demo-")
	if err != nil {
		return []string{"demo workspace unavailable"}
	}
	defer os.RemoveAll(root)

	return p911ExecuteDemoCommandAtRootWithDeps(root, demo, command, deps)
}

func p911ExecuteDemoCommandAtRootWithDeps(root string, demo p711DemoTranscript, command p711DemoCommand, deps Dependencies) []string {
	if err := p911StageDemoWorkspace(root); err != nil {
		return []string{"demo workspace setup failed"}
	}

	configPath, err := p911WriteDemoConfig(root, demo.Classification)
	if err != nil {
		return []string{"demo config setup failed"}
	}

	args := p911DemoArgs(command.Argv, configPath)

	var stdout, stderr bytes.Buffer

	code := p911RunMainNoTelemetryWithDeps(args, &stdout, &stderr, deps)

	var blockers []string

	if code != command.ExitCode {
		blockers = append(blockers, fmt.Sprintf("exit code %d did not match expected %d for command %s", code, command.ExitCode, command.ID))
	}

	if strings.TrimSpace(stderr.String()) != "" {
		blockers = append(blockers, "stderr was not empty for command "+command.ID)
	}

	for _, stream := range []struct {
		name string
		body string
	}{
		{name: "stdout", body: stdout.String()},
		{name: "stderr", body: stderr.String()},
	} {
		if forbidden := localops.ForbiddenEvidenceLeak(stream.body); forbidden != "" {
			blockers = append(blockers, "forbidden "+stream.name+" evidence "+forbidden+" for command "+command.ID)
		}
	}

	blockers = append(blockers, p911CommandOutputBlockers(command, stdout.Bytes())...)

	return blockers
}

func p911StageDemoWorkspace(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "pkg", "payments"), 0o755); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		return err
	}

	files := map[string]string{
		"go.mod":                "module example.com/p911\n\ngo 1.22\n",
		"pkg/payments/retry.go": "package payments\n\nfunc RetryPayment(attempts int) bool {\n\treturn attempts < 3\n}\n",
		"docs/CONTRIBUTING.md":  "# Contributing\n\nRun go test ./... before sending changes.\n",
	}
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			return err
		}
	}

	return nil
}

func p911WriteDemoConfig(root string, class string) (string, error) {
	configPath := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return "", err
	}

	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q,"enable_shell":false,"enable_writes":false,"command_timeout_seconds":10,"max_output_bytes":32768}}`, root, root)
	if strings.TrimSpace(class) == "local-model" {
		body = fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q,"enable_shell":false,"enable_writes":false,"command_timeout_seconds":10,"max_output_bytes":32768},"llama_server":{"ctx_size":4096},"chat":{"max_tokens":512}}`, root, root)
	}

	if err := os.WriteFile(configPath, []byte(body+"\n"), 0o644); err != nil {
		return "", err
	}

	return configPath, nil
}

func p911DemoArgs(args []string, configPath string) []string {
	copied := append([]string(nil), args...)
	for index, arg := range copied {
		if arg == "$IDIOM_CONFIG" {
			copied[index] = configPath
		}
	}

	return copied
}

func p911CommandOutputBlockers(command p711DemoCommand, stdout []byte) []string {
	var blockers []string

	text := string(stdout)

	for _, want := range command.ExpectedOutputShape.StdoutIncludes {
		if !strings.Contains(text, want) {
			blockers = append(blockers, "stdout missing expected text "+want+" for command "+command.ID)
		}
	}

	for _, forbidden := range command.ExpectedOutputShape.StdoutExcludes {
		if strings.Contains(text, forbidden) {
			blockers = append(blockers, "stdout contained forbidden text "+forbidden+" for command "+command.ID)
		}
	}

	if !command.JSON {
		return blockers
	}

	var payload any

	if err := json.Unmarshal(stdout, &payload); err != nil {
		return append(blockers, "stdout was not valid JSON for command "+command.ID)
	}

	for _, path := range command.ExpectedOutputShape.StableFields {
		if _, ok := p911JSONValueAtDottedPath(payload, path); !ok {
			blockers = append(blockers, "stdout missing stable field "+path+" for command "+command.ID)
		}
	}

	for path, want := range command.ExpectedOutputShape.JSONString {
		got, ok := p911JSONValueAtDottedPath(payload, path)
		if !ok {
			blockers = append(blockers, "stdout missing string field "+path+" for command "+command.ID)
			continue
		}

		if fmt.Sprint(got) != want {
			blockers = append(blockers, "stdout field "+path+" did not match expected value for command "+command.ID)
		}
	}

	for _, path := range command.ExpectedOutputShape.JSONPresent {
		if _, ok := p911JSONValueAtDottedPath(payload, path); !ok {
			blockers = append(blockers, "stdout missing JSON field "+path+" for command "+command.ID)
		}
	}

	for _, path := range command.ExpectedOutputShape.JSONNonEmpty {
		got, ok := p911JSONValueAtDottedPath(payload, path)
		if !ok {
			blockers = append(blockers, "stdout missing non-empty field "+path+" for command "+command.ID)
			continue
		}

		if p911JSONValueEmpty(got) {
			blockers = append(blockers, "stdout field "+path+" was empty for command "+command.ID)
		}
	}

	for _, path := range command.ExpectedOutputShape.JSONNull {
		got, ok := p911JSONValueAtDottedPath(payload, path)
		if !ok {
			blockers = append(blockers, "stdout missing null field "+path+" for command "+command.ID)
			continue
		}

		if got != nil {
			blockers = append(blockers, "stdout field "+path+" was not null for command "+command.ID)
		}
	}

	return blockers
}

func p911JSONValueAtDottedPath(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

func p911JSONValueEmpty(value any) bool {
	if value == nil {
		return true
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func p911DemoHasExpectedOutput(demo p711DemoTranscript) bool {
	for _, command := range demo.Commands {
		shape := command.ExpectedOutputShape
		if p911ExpectedOutputHasBlankItem(shape) {
			continue
		}

		if p911ExpectedOutputHasField(shape) {
			return true
		}
	}

	return false
}

func p911NonBlankList(values []string) bool {
	if len(values) == 0 {
		return false
	}

	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}

	return true
}

func p911BlankRequiredListItem(demo p711DemoTranscript) string {
	for _, list := range []struct {
		name   string
		values []string
	}{
		{name: "generic_primitives", values: demo.GenericPrimitives},
		{name: "shared_security_contracts", values: demo.SharedSecurityContracts},
		{name: "evidence_fields", values: demo.EvidenceFields},
		{name: "pass_no_go_criteria.pass", values: demo.PassNoGoCriteria.Pass},
		{name: "pass_no_go_criteria.no_go", values: demo.PassNoGoCriteria.NoGo},
	} {
		if p911BlankListItem(list.values) {
			return list.name
		}
	}

	for _, command := range demo.Commands {
		commandID := firstNonEmptyString(strings.TrimSpace(command.ID), "missing_command_id")
		if p911BlankListItem(command.Argv) {
			return "commands." + commandID + ".argv"
		}

		if blank := p911BlankExpectedOutputItem(command.ExpectedOutputShape); blank != "" {
			return "commands." + commandID + ".expected_output_shape." + blank
		}
	}

	return ""
}

func p911BlankListItem(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}

	return false
}

func p911ExpectedOutputHasField(shape p711ExpectedOutputShape) bool {
	return p911AnyNonBlank(shape.StableFields) ||
		p911AnyNonBlank(shape.JSONPresent) ||
		p911AnyNonBlank(shape.JSONNonEmpty) ||
		p911AnyNonBlank(shape.JSONNull) ||
		p911AnyNonBlank(shape.StdoutIncludes) ||
		p911AnyNonBlank(shape.StdoutExcludes) ||
		p911JSONStringHasField(shape.JSONString)
}

func p911AnyNonBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}

	return false
}

func p911JSONStringHasField(values map[string]string) bool {
	for key, value := range values {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			return true
		}
	}

	return false
}

func p911ExpectedOutputHasBlankItem(shape p711ExpectedOutputShape) bool {
	return p911BlankExpectedOutputItem(shape) != ""
}

func p911BlankExpectedOutputItem(shape p711ExpectedOutputShape) string {
	for _, list := range []struct {
		name   string
		values []string
	}{
		{name: "stable_fields", values: shape.StableFields},
		{name: "json_present", values: shape.JSONPresent},
		{name: "json_non_empty", values: shape.JSONNonEmpty},
		{name: "json_null", values: shape.JSONNull},
		{name: "stdout_includes", values: shape.StdoutIncludes},
		{name: "stdout_excludes", values: shape.StdoutExcludes},
	} {
		if p911BlankListItem(list.values) {
			return list.name
		}
	}

	for key, value := range shape.JSONString {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return "json_string"
		}
	}

	return ""
}

func p911PrivacyPostureExplicit(value string) bool {
	lower := strings.ToLower(value)

	return strings.TrimSpace(value) != "" &&
		strings.Contains(lower, "no raw prompt") &&
		strings.Contains(lower, "no raw file") &&
		strings.Contains(lower, "secret") &&
		strings.Contains(lower, "private")
}

func p911RegulatedDemoProofOnly(demo p711DemoTranscript) bool {
	lower := strings.ToLower(p911DemoText(demo))

	return (strings.Contains(lower, "proof-only") || strings.Contains(lower, "proof only") || strings.Contains(lower, "deferred")) &&
		(strings.Contains(lower, "no regulated") || strings.Contains(lower, "no formal") || strings.Contains(lower, "no public-sector") || strings.Contains(lower, "no hipaa") || strings.Contains(lower, "no finra"))
}

func p911EnterpriseSaaSPrimaryStory(demo p711DemoTranscript) bool {
	lower := strings.ToLower(p911DemoText(demo))
	return strings.Contains(lower, "primary") && (strings.Contains(lower, "buyer") || strings.Contains(lower, "paid beachhead") || strings.Contains(lower, "enterprise saas"))
}

func p911DemoOverclaim(text string) string {
	if overclaim := p99ComplianceOverclaim(text); overclaim != "" {
		return overclaim
	}

	for _, pattern := range []string{
		"production ready",
		"production-ready",
		"paid ready",
		"paid-ready",
		"buyer ready",
		"buyer-ready",
		"procurement ready",
		"procurement-ready",
		"support ready",
		"support-ready",
		"admin support included",
		"managed deployment included",
	} {
		for _, assertion := range p99ClaimAssertions(text) {
			lower := strings.ToLower(assertion)
			if strings.Contains(lower, pattern) && !p99NegatesPattern(lower, pattern) {
				return pattern
			}
		}
	}

	return ""
}

func p911ForbiddenDemoEvidence(text string) string {
	if localops.ContainsAbsolutePath(text) {
		return "absolute_path"
	}

	for _, marker := range []string{
		"CustomerX",
		"private-repo",
		"BEGIN PRIVATE KEY",
		"patient_name:",
		"account_number:",
		"unredacted_telemetry_path",
		"logs/telemetry/",
		"logs/conversations/",
		`"raw_prompt_captured": true`,
		`"raw_file_body_captured": true`,
		`"secret_capture_allowed": true`,
		`"secret_captured": true`,
		`"private_path_captured": true`,
		`"private_repo_url_captured": true`,
		`"unredacted_log_captured": true`,
		`"hosted_telemetry_required": true`,
		`"raw_task_captured": true`,
	} {
		if strings.Contains(text, marker) {
			return marker
		}
	}

	for _, field := range strings.Fields(text) {
		token := strings.Trim(field, " \t\r\n.,:;()[]{}\"'`")
		lower := strings.ToLower(token)

		for _, prefix := range []string{"sk-", "sk_", "ghp_", "github_pat_", "xoxb-"} {
			if strings.HasPrefix(lower, prefix) && len(token) > len(prefix) {
				return "secret_value"
			}
		}
	}

	return ""
}

func p911DemoText(demo p711DemoTranscript) string {
	var parts []string

	parts = append(parts,
		demo.ID,
		demo.Classification,
		demo.ClassificationLabel,
		demo.Stance,
		demo.P76CommandID,
		demo.ValueClaim,
		demo.SellingPoint,
		demo.MVPLimitation,
		demo.FinalStateExpansion,
		demo.PrivacyPosture,
		demo.ReleaseClaimImpact,
	)
	parts = append(parts, demo.GenericPrimitives...)
	parts = append(parts, demo.SharedSecurityContracts...)
	parts = append(parts, demo.EvidenceFields...)
	parts = append(parts, demo.PassNoGoCriteria.Pass...)
	parts = append(parts, demo.PassNoGoCriteria.NoGo...)

	for _, command := range demo.Commands {
		parts = append(parts, command.ID, command.Command)
		parts = append(parts, command.Argv...)
		parts = append(parts, command.ExpectedOutputShape.StableFields...)

		for key, value := range command.ExpectedOutputShape.JSONString {
			parts = append(parts, key, value)
		}

		parts = append(parts, command.ExpectedOutputShape.JSONPresent...)
		parts = append(parts, command.ExpectedOutputShape.JSONNonEmpty...)
		parts = append(parts, command.ExpectedOutputShape.JSONNull...)
		parts = append(parts, command.ExpectedOutputShape.StdoutIncludes...)
		parts = append(parts, command.ExpectedOutputShape.StdoutExcludes...)
	}

	return strings.Join(parts, "\n")
}

func p711AuditSignal(localOps *localops.BenchmarkReport, security p711SecurityPosture) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "audit", Status: "partial", Completeness: "partial", SourceRefs: []string{"local_ops_benchmark", "p7.9_security_privacy_posture"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.9", "P9.1", "P9.3", "P9.9"}, Counts: map[string]int{"security_controls": len(security.RequiredControls)}}
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

func p711LearningSignal(summary *comprehension.LearningLogSummary) productEvidenceSignalSummary {
	s := productEvidenceSignalSummary{ID: "learning", Status: "not_available", Completeness: "missing", SourceRefs: []string{"ranker_learning_log"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.11", "P9.1"}, Counts: map[string]int{}}
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

func buildP711ClassCoverage(demos p711DemoTranscriptSet, security p711SecurityPosture, integration p711IntegrationProofSet, records []p711ValidationRecord) []productEvidenceClassCoverage {
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
	rows := make([]productEvidenceClassCoverage, 0, len(classes))

	for _, class := range classes {
		demo := demoByClass[class]
		sec := securityByClass[class]
		status := "partial"

		if demo.ID != "" && sec.Classification != "" {
			status = "pass"
		}

		rows = append(rows, productEvidenceClassCoverage{
			Classification:          class,
			Status:                  status,
			Stance:                  localops.SafeText(firstNonEmptyString(demo.Stance, sec.Stance, p711DefaultStance(class))),
			P76CommandID:            firstNonEmptyString(demo.P76CommandID, sec.P76CommandID),
			ClientDemoTranscriptID:  demo.ID,
			SecurityPostureCoverage: sec.Classification != "",
			IntegrationProofIDs:     proofIDs,
			ValidationRecordCount:   recordCounts[class],
			Limitations:             localops.CompactStrings(localops.SafeText(demo.MVPLimitation), localops.SafeText(sec.Limitation)),
		})
	}

	return rows
}

func buildP711ReleaseGateLinkage(signals []productEvidenceSignalSummary, classes []productEvidenceClassCoverage, localOps *localops.BenchmarkReport, records []p711ValidationRecord, p99 productEvidenceReleaseGateLinkage, p910 productEvidenceReleaseGateLinkage, p911 productEvidenceReleaseGateLinkage, packaging p912PackagingSupportThesis, packagingRef productEvidenceSourceReference) []productEvidenceReleaseGateLinkage {
	classStatus := "evidence_present"

	for _, row := range classes {
		if row.Status != "pass" {
			classStatus = "needs_evidence"
			break
		}
	}

	if p911.Status != "evidence_present" {
		classStatus = "needs_evidence"
	}

	p97 := p97ValidationGate(records)
	p98 := p98IntegrationGateFromSignals(signals)
	p912 := p912PackagingSupportGate(packaging, packagingRef, p97, p98, p99, p910, p911)

	return []productEvidenceReleaseGateLinkage{
		{Gate: "P9.1", Status: p711GateFromSignal(signals, "benchmark"), Evidence: "benchmark,audit,learning summaries", ClaimImpact: "release bundle input; not a release decision"},
		{Gate: "P9.6", Status: p96ActivationReleaseGateStatus(localOps), Evidence: "standalone product export cannot close P9.6 without current release validation evidence", ClaimImpact: "release activation claims are no-go until the release bundle consumes activation plus validation evidence"},
		{Gate: "P9.7", Status: p97.Status, Evidence: p97.Evidence, ClaimImpact: p97.ClaimImpact, Counts: p97.Counts, Blockers: p97.Blockers, EvidenceIDs: p97.EvidenceIDs},
		{Gate: "P9.8", Status: p98.Status, Evidence: p98.Evidence, ClaimImpact: p98.ClaimImpact, Counts: p98.Counts, Blockers: p98.Blockers, EvidenceIDs: p98.EvidenceIDs},
		{Gate: "P9.9", Status: p99.Status, Evidence: p99.Evidence, ClaimImpact: p99.ClaimImpact, Counts: p99.Counts, Blockers: p99.Blockers, EvidenceIDs: p99.EvidenceIDs},
		{Gate: "P9.10", Status: p910.Status, Evidence: p910.Evidence, ClaimImpact: p910.ClaimImpact, Counts: p910.Counts, Blockers: p910.Blockers, EvidenceIDs: p910.EvidenceIDs},
		{Gate: "P9.11", Status: p911.Status, Evidence: p911.Evidence, ClaimImpact: p911.ClaimImpact, Counts: p911.Counts, Blockers: p911.Blockers, EvidenceIDs: p911.EvidenceIDs},
		p912WithClassCoverageStatus(p912, classStatus),
	}
}

func p912WithClassCoverageStatus(row productEvidenceReleaseGateLinkage, classStatus string) productEvidenceReleaseGateLinkage {
	if classStatus == "evidence_present" {
		return row
	}

	row.Status = "needs_evidence"
	row.Blockers = localops.CompactStrings(append(row.Blockers, "classification coverage rows or P9.11 demo evidence incomplete")...)

	if row.Counts == nil {
		row.Counts = map[string]int{}
	}

	row.Counts["classification_coverage_blockers"]++

	row.Counts["blockers"] = len(row.Blockers)

	return row
}

func p912PackagingSupportGate(thesis p912PackagingSupportThesis, ref productEvidenceSourceReference, p97 p97ValidationGateResult, p98 p98IntegrationGateResult, p99 productEvidenceReleaseGateLinkage, p910 productEvidenceReleaseGateLinkage, p911 productEvidenceReleaseGateLinkage) productEvidenceReleaseGateLinkage {
	counts := map[string]int{
		"required_fields":                      8,
		"required_fields_present":              0,
		"required_fields_with_evidence_ids":    0,
		"classification_stances_required":      len(p711Classifications()),
		"classification_stances_covered":       0,
		"classification_stances_with_evidence": 0,
		"packaging_support_complete":           0,
		"pricing_signal_quality_hypothesis":    0,
		"pricing_signal_quality_validated":     0,
		"unsupported_admin_request_count":      0,
		"decision_evidence_ids":                0,
		"decision_continue":                    0,
		"decision_pivot":                       0,
		"decision_no_go":                       0,
		"prior_gate_evidence_present":          0,
		"privacy_blockers":                     0,
		"overclaim_blockers":                   0,
		"contradiction_blockers":               0,
		"admin_count_blockers":                 0,
	}

	var (
		blockers    []string
		evidenceIDs []string
	)

	addBlocker := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason != "" {
			blockers = append(blockers, reason)
		}
	}

	if ref.Status == "present" {
		evidenceIDs = append(evidenceIDs, ref.ID)
	} else if ref.Status == "blocked" {
		counts["privacy_blockers"]++

		addBlocker("packaging/support thesis artifact is malformed or privacy-blocked")
	} else {
		addBlocker("packaging/support thesis artifact not available")
	}

	if thesis.SchemaVersion != "idiom.packaging_support_thesis.v1" || thesis.ArtifactID == "" {
		addBlocker("packaging/support thesis identity is missing or unexpected")
	}

	p912RequireField("icp", p912ICPComplete(thesis.ICP), p912EvidenceIDsPresent(thesis.ICP.EvidenceIDs), counts, addBlocker)
	p912RequireField("packaging", p912EvidenceTextComplete(thesis.Packaging), p912EvidenceIDsPresent(thesis.Packaging.EvidenceIDs), counts, addBlocker)
	p912RequireField("pricing", p912PricingComplete(thesis.Pricing), p912EvidenceIDsPresent(thesis.Pricing.EvidenceIDs), counts, addBlocker)
	p912RequireField("support", p912SupportComplete(thesis.Support), p912EvidenceIDsPresent(thesis.Support.EvidenceIDs), counts, addBlocker)
	p912RequireField("admin", p912AdminComplete(thesis.Admin), p912EvidenceIDsPresent(thesis.Admin.EvidenceIDs), counts, addBlocker)
	p912RequireField("local_evidence_export", p912LocalEvidenceExportComplete(thesis.LocalEvidenceExport), p912EvidenceIDsPresent(thesis.LocalEvidenceExport.EvidenceIDs), counts, addBlocker)
	p912RequireField("classification_stances", len(thesis.ClassificationStance) > 0, p912ClassificationEvidencePresent(thesis.ClassificationStance), counts, addBlocker)
	p912RequireField("decision", p912DecisionComplete(thesis.Decision), p912EvidenceIDsPresent(thesis.Decision.EvidenceIDs), counts, addBlocker)

	evidenceIDs = append(evidenceIDs, thesis.ICP.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.Packaging.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.Pricing.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.Support.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.Admin.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.LocalEvidenceExport.EvidenceIDs...)
	evidenceIDs = append(evidenceIDs, thesis.Decision.EvidenceIDs...)
	counts["decision_evidence_ids"] = len(localops.CompactStrings(thesis.Decision.EvidenceIDs...))
	counts["unsupported_admin_request_count"] = p912UnsupportedAdminRequestCount(thesis.Admin, counts, addBlocker)
	p912CountPricingQuality(thesis.Pricing.SignalQuality, counts)
	p912EvaluateNorthStarStance(thesis, counts, addBlocker)
	p912EvaluateClassificationStances(thesis.ClassificationStance, counts, &evidenceIDs, addBlocker)
	p912EvaluateLocalEvidenceExport(thesis.LocalEvidenceExport, counts, addBlocker)
	p912EvaluatePriorGateEvidence(p97, p98, p99, p910, p911, counts, addBlocker)
	p912EvaluateDecision(thesis.Decision, p97, p98, p99, p910, p911, counts, addBlocker)
	p912ScanThesisPrivacy(thesis, counts, addBlocker)
	p912ScanThesisClaims(thesis, counts, addBlocker)

	if p912PackagingSupportFieldsComplete(counts) {
		counts["packaging_support_complete"] = 1
	}

	counts["blockers"] = len(blockers)

	status := "needs_evidence"
	claimImpact := "packaging, pricing, support, admin, and ICP claims remain no-go until the P0.11 thesis is complete, evidence-linked, and aligned with prior gates"

	if len(blockers) == 0 {
		status = "evidence_present"
		claimImpact = "P9.12 may cite the evidence-linked P0.11 thesis and bounded continue decision; commercial claims remain limited to proven prior gates"

		if localops.SafeToken(thesis.Decision.Outcome) == "pivot" {
			claimImpact = "P9.12 may cite the evidence-linked pivot decision without claiming primary enterprise-SaaS paid demand"
		}

		if localops.SafeToken(thesis.Decision.Outcome) == "no_go" {
			claimImpact = "P9.12 records an evidence-linked no-go decision for commercial or production release claims"
		}
	}

	return productEvidenceReleaseGateLinkage{
		Gate:        "P9.12",
		Status:      status,
		Evidence:    p912EvidenceSummary(counts, thesis.Decision.Outcome),
		ClaimImpact: claimImpact,
		Counts:      counts,
		Blockers:    localops.CompactStrings(blockers...),
		EvidenceIDs: localops.CompactStrings(evidenceIDs...),
	}
}

func p912PackagingSupportFieldsComplete(counts map[string]int) bool {
	return counts["required_fields_present"] == counts["required_fields"] &&
		counts["required_fields_with_evidence_ids"] == counts["required_fields"] &&
		counts["classification_stances_covered"] == counts["classification_stances_required"] &&
		counts["classification_stances_with_evidence"] == counts["classification_stances_required"] &&
		counts["privacy_blockers"] == 0 &&
		counts["overclaim_blockers"] == 0 &&
		counts["contradiction_blockers"] == 0 &&
		counts["admin_count_blockers"] == 0
}

func p912RequireField(name string, present bool, evidenceLinked bool, counts map[string]int, addBlocker func(string)) {
	if present {
		counts["required_fields_present"]++
	} else {
		addBlocker("required packaging/support field missing or empty: " + name)
	}

	if evidenceLinked {
		counts["required_fields_with_evidence_ids"]++
	} else {
		addBlocker("required packaging/support field lacks evidence IDs: " + name)
	}
}

func p912ICPComplete(icp p912ICPThesis) bool {
	return strings.TrimSpace(icp.PrimaryBuyer) != "" &&
		strings.TrimSpace(icp.AdoptionWedge) != "" &&
		len(localops.CompactStrings(icp.Secondary...)) > 0 &&
		len(localops.CompactStrings(icp.DeferredProofOnly...)) > 0 &&
		strings.TrimSpace(icp.Buyer) != "" &&
		strings.TrimSpace(icp.DailyUser) != ""
}

func p912EvidenceTextComplete(value p912EvidenceText) bool {
	return strings.TrimSpace(value.MVP) != "" && strings.TrimSpace(value.FinalState) != ""
}

func p912PricingComplete(value p912PricingThesis) bool {
	return strings.TrimSpace(value.Hypothesis) != "" && strings.TrimSpace(value.SignalQuality) != ""
}

func p912SupportComplete(value p912SupportThesis) bool {
	return strings.TrimSpace(value.Boundary) != "" && strings.TrimSpace(value.Level) != ""
}

func p912AdminComplete(value p912AdminThesis) bool {
	return len(localops.CompactStrings(value.NonGoals...)) > 0 &&
		len(localops.CompactStrings(value.EnterpriseSurfaceLimits...)) > 0 &&
		len(localops.CompactStrings(value.UnsupportedRequests...)) > 0
}

func p912LocalEvidenceExportComplete(value p912LocalEvidenceExportThesis) bool {
	return strings.TrimSpace(value.Status) != "" && strings.TrimSpace(value.PrivacyPosture) != ""
}

func p912DecisionComplete(value p912PackagingSupportDecision) bool {
	return strings.TrimSpace(value.Outcome) != "" &&
		strings.TrimSpace(value.Trigger) != "" &&
		strings.TrimSpace(value.ClaimImpact) != ""
}

func p912EvidenceIDsPresent(ids []string) bool {
	return len(localops.CompactStrings(ids...)) > 0
}

func p912ClassificationEvidencePresent(rows []p912ClassificationStance) bool {
	for _, row := range rows {
		if !p912EvidenceIDsPresent(row.EvidenceIDs) {
			return false
		}
	}

	return len(rows) > 0
}

func p912UnsupportedAdminRequestCount(admin p912AdminThesis, counts map[string]int, addBlocker func(string)) int {
	actual := len(localops.CompactStrings(admin.UnsupportedRequests...))
	if admin.UnsupportedRequestCount != actual {
		counts["admin_count_blockers"]++

		addBlocker(fmt.Sprintf("unsupported_admin_request_count mismatch: thesis=%d actual=%d", admin.UnsupportedRequestCount, actual))
	}

	return actual
}

func p912CountPricingQuality(value string, counts map[string]int) {
	switch localops.SafeToken(value) {
	case "validated", "paid_signal", "budget_owner_signal", "internal_platform_signal":
		counts["pricing_signal_quality_validated"] = 1
	default:
		if strings.TrimSpace(value) != "" {
			counts["pricing_signal_quality_hypothesis"] = 1
		}
	}
}

func p912EvaluateNorthStarStance(thesis p912PackagingSupportThesis, counts map[string]int, addBlocker func(string)) {
	if thesis.ICP.PrimaryBuyer != "enterprise-saas" {
		counts["contradiction_blockers"]++

		addBlocker("ICP primary buyer must remain enterprise-saas unless a supported pivot is recorded")
	}

	if thesis.ICP.AdoptionWedge != "open-source" {
		counts["contradiction_blockers"]++

		addBlocker("ICP adoption wedge must remain open-source")
	}

	if !p912StringSetContains(thesis.ICP.Secondary, "tooling-vendor") || !p912StringSetContains(thesis.ICP.Secondary, "local-model") {
		counts["contradiction_blockers"]++

		addBlocker("ICP secondary lanes must include tooling-vendor and local-model")
	}

	for _, class := range []string{"finance", "healthcare", "public-sector"} {
		if !p912StringSetContains(thesis.ICP.DeferredProofOnly, class) {
			counts["contradiction_blockers"]++

			addBlocker("regulated ICP row must be deferred/proof-only: " + class)
		}
	}
}

func p912EvaluateClassificationStances(rows []p912ClassificationStance, counts map[string]int, evidenceIDs *[]string, addBlocker func(string)) {
	required := map[string]bool{}
	for _, class := range p711Classifications() {
		required[class] = true
	}

	seen := map[string]int{}

	for _, row := range rows {
		class := strings.TrimSpace(row.Classification)
		if !required[class] {
			counts["unsupported_classification_stances"]++

			addBlocker("unsupported packaging classification stance: " + firstNonEmptyString(class, "missing"))

			continue
		}

		seen[class]++
		if seen[class] > 1 {
			counts["duplicate_classification_stances"]++

			addBlocker("duplicate packaging classification stance: " + class)

			continue
		}

		*evidenceIDs = append(*evidenceIDs, row.EvidenceIDs...)
		if p912ClassificationStanceComplete(row) {
			counts["classification_stances_covered"]++
		} else {
			addBlocker("packaging classification stance missing required fields: " + class)
		}

		if p912EvidenceIDsPresent(row.EvidenceIDs) {
			counts["classification_stances_with_evidence"]++
		} else {
			addBlocker("packaging classification stance lacks evidence IDs: " + class)
		}

		if !p912ClassificationStanceAligned(row) {
			counts["contradiction_blockers"]++

			addBlocker("packaging classification stance contradicts north-star priority: " + class)
		}
	}

	for class := range required {
		if seen[class] == 0 {
			counts["missing_classification_stances"]++

			addBlocker("missing packaging classification stance: " + class)
		}
	}
}

func p912ClassificationStanceComplete(row p912ClassificationStance) bool {
	return strings.TrimSpace(row.Classification) != "" &&
		strings.TrimSpace(row.PackagingStance) != "" &&
		strings.TrimSpace(row.MVPPackagingStance) != "" &&
		strings.TrimSpace(row.FinalStateHypothesis) != "" &&
		strings.TrimSpace(row.SupportAdminBoundary) != "" &&
		strings.TrimSpace(row.GoNoGoEvidence) != ""
}

func p912ClassificationStanceAligned(row p912ClassificationStance) bool {
	text := strings.ToLower(strings.Join([]string{row.PackagingStance, row.MVPPackagingStance, row.SupportAdminBoundary, row.GoNoGoEvidence}, " "))
	switch row.Classification {
	case "enterprise-saas":
		return strings.Contains(text, "primary") && strings.Contains(text, "beachhead")
	case "open-source":
		return strings.Contains(text, "adoption") && strings.Contains(text, "wedge")
	case "tooling-vendor", "local-model":
		return strings.Contains(text, "secondary")
	case "finance", "healthcare", "public-sector":
		return strings.Contains(text, "proof-only") || strings.Contains(text, "proof only") || strings.Contains(text, "deferred")
	default:
		return false
	}
}

func p912EvaluateLocalEvidenceExport(value p912LocalEvidenceExportThesis, counts map[string]int, addBlocker func(string)) {
	text := strings.ToLower(value.Status + " " + value.PrivacyPosture)
	if !strings.Contains(text, "local") || !strings.Contains(text, "metadata") {
		counts["privacy_blockers"]++

		addBlocker("local evidence export must be local and metadata-only")
	}

	for _, forbidden := range []string{"raw prompt", "raw_prompt", "raw file", "raw_file", "secret", "credential", "hosted telemetry"} {
		if strings.Contains(text, forbidden) && !p99NegatesPattern(text, forbidden) {
			counts["privacy_blockers"]++

			addBlocker("local evidence export contains privacy-blocked posture: " + forbidden)

			return
		}
	}
}

type p912ThesisPrivacyField struct {
	path string
	text string
}

func p912ScanThesisPrivacy(thesis p912PackagingSupportThesis, counts map[string]int, addBlocker func(string)) {
	for _, field := range p912ThesisPrivacyFields(thesis) {
		if marker := p912PrivacyMarker(field.text); marker != "" {
			counts["privacy_blockers"]++

			addBlocker("packaging/support thesis contains privacy-blocked field " + field.path + ": " + marker)
		}
	}
}

func p912ThesisPrivacyFields(thesis p912PackagingSupportThesis) []p912ThesisPrivacyField {
	fields := []p912ThesisPrivacyField{
		{path: "schema_version", text: thesis.SchemaVersion},
		{path: "artifact_id", text: thesis.ArtifactID},
		{path: "icp.primary_buyer", text: thesis.ICP.PrimaryBuyer},
		{path: "icp.adoption_wedge", text: thesis.ICP.AdoptionWedge},
		{path: "icp.buyer", text: thesis.ICP.Buyer},
		{path: "icp.daily_user", text: thesis.ICP.DailyUser},
		{path: "packaging.mvp", text: thesis.Packaging.MVP},
		{path: "packaging.final_state", text: thesis.Packaging.FinalState},
		{path: "pricing.hypothesis", text: thesis.Pricing.Hypothesis},
		{path: "pricing.signal_quality", text: thesis.Pricing.SignalQuality},
		{path: "support.boundary", text: thesis.Support.Boundary},
		{path: "support.level", text: thesis.Support.Level},
		{path: "local_evidence_export.status", text: thesis.LocalEvidenceExport.Status},
		{path: "local_evidence_export.privacy_posture", text: thesis.LocalEvidenceExport.PrivacyPosture},
		{path: "decision.outcome", text: thesis.Decision.Outcome},
		{path: "decision.trigger", text: thesis.Decision.Trigger},
		{path: "decision.claim_impact", text: thesis.Decision.ClaimImpact},
	}

	fields = append(fields, p912SlicePrivacyFields("icp.secondary", thesis.ICP.Secondary)...)
	fields = append(fields, p912SlicePrivacyFields("icp.deferred_proof_only", thesis.ICP.DeferredProofOnly)...)
	fields = append(fields, p912SlicePrivacyFields("icp.evidence_ids", thesis.ICP.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("packaging.evidence_ids", thesis.Packaging.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("pricing.evidence_ids", thesis.Pricing.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("support.evidence_ids", thesis.Support.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("admin.non_goals", thesis.Admin.NonGoals)...)
	fields = append(fields, p912SlicePrivacyFields("admin.enterprise_surface_limits", thesis.Admin.EnterpriseSurfaceLimits)...)
	fields = append(fields, p912SlicePrivacyFields("admin.unsupported_requests", thesis.Admin.UnsupportedRequests)...)
	fields = append(fields, p912SlicePrivacyFields("admin.evidence_ids", thesis.Admin.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("local_evidence_export.evidence_ids", thesis.LocalEvidenceExport.EvidenceIDs)...)
	fields = append(fields, p912SlicePrivacyFields("decision.evidence_ids", thesis.Decision.EvidenceIDs)...)

	for i, row := range thesis.ClassificationStance {
		prefix := fmt.Sprintf("classification_stances[%d]", i)

		fields = append(fields,
			p912ThesisPrivacyField{path: prefix + ".classification", text: row.Classification},
			p912ThesisPrivacyField{path: prefix + ".packaging_stance", text: row.PackagingStance},
			p912ThesisPrivacyField{path: prefix + ".mvp_packaging_stance", text: row.MVPPackagingStance},
			p912ThesisPrivacyField{path: prefix + ".final_state_hypothesis", text: row.FinalStateHypothesis},
			p912ThesisPrivacyField{path: prefix + ".support_admin_boundary", text: row.SupportAdminBoundary},
			p912ThesisPrivacyField{path: prefix + ".go_no_go_evidence", text: row.GoNoGoEvidence},
		)
		fields = append(fields, p912SlicePrivacyFields(prefix+".evidence_ids", row.EvidenceIDs)...)
	}

	return fields
}

func p912SlicePrivacyFields(path string, values []string) []p912ThesisPrivacyField {
	fields := make([]p912ThesisPrivacyField, 0, len(values))
	for i, value := range values {
		fields = append(fields, p912ThesisPrivacyField{path: fmt.Sprintf("%s[%d]", path, i), text: value})
	}

	return fields
}

func p912PrivacyMarker(text string) string {
	for _, assertion := range p99ClaimAssertions(text) {
		if marker := p912PrivacyAssertionMarker(assertion); marker != "" {
			return marker
		}
	}

	return ""
}

func p912PrivacyAssertionMarker(assertion string) string {
	if localops.ContainsAbsolutePath(assertion) {
		return "private_path"
	}

	if p912ContainsSecretValue(assertion) {
		return "secret_value"
	}

	lower := strings.ToLower(assertion)

	for _, entry := range p912PrivacyPhrasePatterns() {
		for _, pattern := range entry.patterns {
			if strings.Contains(lower, pattern) && !p99NegatesPattern(lower, pattern) {
				return entry.marker
			}
		}
	}

	if p912NamedCustomerIdentifierPattern.MatchString(assertion) {
		return "customer_identifier"
	}

	return ""
}

func p912ContainsSecretValue(assertion string) bool {
	if !sensitivity.ContainsSecretMarker(assertion) {
		return false
	}

	lower := strings.ToLower(assertion)
	for _, pattern := range []string{"secret", "token", "credential", "password", "api key", "api_key"} {
		if strings.Contains(lower, pattern) && !p99NegatesPattern(lower, pattern) {
			return true
		}
	}

	return false
}

type p912PrivacyPattern struct {
	marker   string
	patterns []string
}

func p912PrivacyPhrasePatterns() []p912PrivacyPattern {
	return []p912PrivacyPattern{
		{marker: "raw_prompt", patterns: []string{"raw prompt", "raw_prompt", "prompt body", "prompt_body"}},
		{marker: "raw_file_body", patterns: []string{"raw file body", "raw_file_body", "raw file", "raw_file", "file body", "file_body", "source body", "source_body"}},
		{marker: "raw_task", patterns: []string{"raw task", "raw_task", "task body", "task_body", "raw capture", "raw_capture"}},
		{marker: "private_path", patterns: []string{"private path", "private_path", "private paths", "private_paths", "telemetry path", "log path", "logs/telemetry/", "logs/conversations/", ".idiom/logs/", ".idiom/telemetry/"}},
		{marker: "private_identifier", patterns: []string{"private repo", "private repository", "private workspace", "private project", "private customer"}},
		{marker: "secret", patterns: []string{"secret", "secrets", "credential", "credentials", "api key", "api_key", "password", "token"}},
		{marker: "customer_identifier", patterns: []string{"customer identifier", "customer_identifier", "customer name", "customer_name", "client identifier", "client_identifier", "private identifier", "private_identifier", "patient name", "patient_name", "account number", "account_number"}},
		{marker: "hosted_telemetry", patterns: []string{"hosted telemetry", "hosted_telemetry"}},
	}
}

var p912NamedCustomerIdentifierPattern = regexp.MustCompile(`\bCustomer[A-Z0-9][A-Za-z0-9_-]*\b|\bcustomer[_-]?[0-9]+[A-Za-z0-9_-]*\b|\b[A-Z][A-Za-z0-9_-]*Customer[A-Za-z0-9_-]*\b`)

func p912EvaluatePriorGateEvidence(p97 p97ValidationGateResult, p98 p98IntegrationGateResult, p99 productEvidenceReleaseGateLinkage, p910 productEvidenceReleaseGateLinkage, p911 productEvidenceReleaseGateLinkage, counts map[string]int, addBlocker func(string)) {
	for _, gate := range []productEvidenceReleaseGateLinkage{
		{Gate: "P9.7", Status: p97.Status, Evidence: p97.Evidence},
		{Gate: "P9.8", Status: p98.Status, Evidence: p98.Evidence},
		p99,
		p910,
		p911,
	} {
		if gate.Status == "evidence_present" {
			counts["prior_gate_evidence_present"]++
			continue
		}

		addBlocker(gate.Gate + " evidence incomplete for P9.12 packaging/support decision: " + firstNonEmptyString(gate.Status, "missing"))
	}
}

func p912EvaluateDecision(decision p912PackagingSupportDecision, p97 p97ValidationGateResult, p98 p98IntegrationGateResult, p99 productEvidenceReleaseGateLinkage, p910 productEvidenceReleaseGateLinkage, p911 productEvidenceReleaseGateLinkage, counts map[string]int, addBlocker func(string)) {
	outcome := localops.SafeToken(decision.Outcome)
	switch outcome {
	case "continue":
		counts["decision_continue"] = 1
	case "pivot":
		counts["decision_pivot"] = 1
	case "no_go", "nogo":
		counts["decision_no_go"] = 1
	default:
		addBlocker("P9.12 decision outcome must be continue, pivot, or no_go")

		return
	}

	if outcome == "pivot" && !p912DecisionEvidenceMentions(decision, "pivot") {
		addBlocker("P9.12 pivot decision must name pivot evidence and failed assumption")
	}

	if (outcome == "no_go" || outcome == "nogo") && !p912DecisionEvidenceMentions(decision, "no-go") && !p912DecisionEvidenceMentions(decision, "no_go") {
		addBlocker("P9.12 no-go decision must name no-go evidence")
	}

	if outcome == "continue" {
		for _, status := range []string{p97.Status, p98.Status, p99.Status, p910.Status, p911.Status} {
			if status != "evidence_present" {
				addBlocker("P9.12 continue decision is unsupported by prior gate evidence")

				return
			}
		}
	}
}

func p912DecisionEvidenceMentions(decision p912PackagingSupportDecision, token string) bool {
	text := strings.ToLower(decision.Trigger + " " + decision.ClaimImpact + " " + strings.Join(decision.EvidenceIDs, " "))
	return strings.Contains(text, token)
}

func p912ScanThesisClaims(thesis p912PackagingSupportThesis, counts map[string]int, addBlocker func(string)) {
	texts := []string{
		thesis.ICP.PrimaryBuyer, thesis.ICP.AdoptionWedge, thesis.ICP.Buyer, thesis.ICP.DailyUser,
		thesis.Packaging.MVP, thesis.Packaging.FinalState,
		thesis.Pricing.Hypothesis, thesis.Pricing.SignalQuality,
		thesis.Support.Boundary, thesis.Support.Level,
		strings.Join(thesis.Admin.NonGoals, " "),
		strings.Join(thesis.Admin.EnterpriseSurfaceLimits, " "),
		strings.Join(thesis.Admin.UnsupportedRequests, " "),
		thesis.LocalEvidenceExport.Status, thesis.LocalEvidenceExport.PrivacyPosture,
		thesis.Decision.Outcome, thesis.Decision.Trigger, thesis.Decision.ClaimImpact,
	}
	for _, row := range thesis.ClassificationStance {
		texts = append(texts, row.PackagingStance, row.MVPPackagingStance, row.FinalStateHypothesis, row.SupportAdminBoundary, row.GoNoGoEvidence)
	}

	for _, text := range texts {
		if overclaim := p99ComplianceOverclaim(text); overclaim != "" {
			counts["overclaim_blockers"]++

			addBlocker("packaging/support thesis contains regulated/compliance overclaim language: " + overclaim)
		}

		if overclaim := p912CommercialOverclaim(text); overclaim != "" {
			counts["overclaim_blockers"]++

			addBlocker("packaging/support thesis contains unsupported commercial/admin overclaim language: " + overclaim)
		}
	}
}

func p912CommercialOverclaim(text string) string {
	patterns := []string{
		"final-state enterprise support",
		"final state enterprise support",
		"enterprise support ready",
		"support readiness",
		"admin readiness",
		"admin ready",
		"production release ready",
		"production release readiness",
		"release readiness",
		"paid demand proven",
		"paid demand validated",
		"paid demand ready",
		"procurement ready",
		"24/7 sla",
		"dedicated support engineer",
		"regulated readiness",
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

func p912EvidenceSummary(counts map[string]int, outcome string) string {
	return fmt.Sprintf(
		"outcome:%s required_fields:%d/%d class_stances:%d/%d pricing_hypothesis:%d unsupported_admin_requests:%d blockers:%d",
		localops.SafeToken(outcome),
		counts["required_fields_present"],
		counts["required_fields"],
		counts["classification_stances_covered"],
		counts["classification_stances_required"],
		counts["pricing_signal_quality_hypothesis"],
		counts["unsupported_admin_request_count"],
		counts["blockers"],
	)
}

func p912StringSetContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}

func p99SecurityPrivacyGate(root string, privacy p99ProductPrivacyState) productEvidenceReleaseGateLinkage {
	counts := map[string]int{
		"required_sections":        10,
		"required_controls":        len(p99RequiredControlIDs()),
		"required_client_rows":     len(p711Classifications()),
		"required_gate_tiebacks":   len(p99RequiredTiebacks()),
		"required_forbidden_terms": len(p99ForbiddenEvidenceFields()),
	}

	var (
		blockers    []string
		evidenceIDs []string
	)

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
		evidenceIDs = append(evidenceIDs, firstNonEmptyString(matrix.ArtifactID, "p7.9-security-privacy-posture"))
		p99EvaluateMatrix(matrix, counts, addBlocker)
	}

	postureText, postureOK := p99ReadText(filepath.Join(root, "docs", "security-privacy-posture.md"))
	if !postureOK {
		addBlocker("missing posture artifact: docs/security-privacy-posture.md")
	}

	reportText, reportOK := p99ReadText(filepath.Join(root, "docs", "release.md"))

	if !reportOK {
		addBlocker("missing release checklist: docs/release.md")
	} else {
		evidenceIDs = append(evidenceIDs, "release-checklist")
	}

	for label, text := range map[string]string{
		"security/privacy posture": postureText,
		"release checklist":        reportText,
	} {
		if overclaim := p99ComplianceOverclaim(text); overclaim != "" {
			addBlocker(label + " contains regulated/compliance overclaim language: " + overclaim)
		}
	}

	if postureOK {
		p99EvaluateP09Plan(postureText, counts, addBlocker)

		if strings.Contains(postureText, "Reviewer Command") {
			counts["repro_evidence_sections"]++
		} else {
			addBlocker("posture artifact missing reviewer command/repro evidence")
		}
	}

	if reportOK {
		p99EvaluateReviewReport(reportText, counts, addBlocker)
	}

	p99EvaluateProductPrivacy(privacy, counts, addBlocker)

	status := "evidence_present"
	if len(blockers) > 0 {
		status = "needs_evidence"
	}

	counts["blockers"] = len(blockers)

	return productEvidenceReleaseGateLinkage{
		Gate:        "P9.9",
		Status:      status,
		Evidence:    p99EvidenceSummary(counts),
		ClaimImpact: p99ClaimImpact(status),
		Counts:      counts,
		Blockers:    localops.CompactStrings(blockers...),
		EvidenceIDs: localops.CompactStrings(evidenceIDs...),
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
		label := firstNonEmptyString(record.SourceID, "unlabeled")

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

	var (
		records []p99SourceVerificationRecord
		current *p99SourceVerificationRecord
	)

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

func p99ReadText(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	return string(data), true
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
		"hipaa ready",
		"hipaa-ready",
		"hipaa readiness",
		"hipaa-readiness",
		"hipaa approved",
		"hipaa approval",
		"hipaa authorization",
		"hipaa authorized",
		"hipaa-certified",
		"hipaa certified",
		"hipaa certification",
		"hipaa attestation",
		"hipaa conformance",
		"finra compliant",
		"finra compliance",
		"finra ready",
		"finra-ready",
		"finra readiness",
		"finra-readiness",
		"finra approved",
		"finra approval",
		"finra authorization",
		"finra authorized",
		"finra-certified",
		"finra certified",
		"finra certification",
		"finra attestation",
		"finra conformance",
		"nist compliant",
		"nist compliance",
		"nist ready",
		"nist-ready",
		"nist readiness",
		"nist-readiness",
		"nist approved",
		"nist approval",
		"nist authorization",
		"nist authorized",
		"nist 800-53 ready",
		"nist 800-53 readiness",
		"nist 800-53 approved",
		"nist 800-53 approval",
		"nist-certified",
		"nist certified",
		"nist certification",
		"nist attestation",
		"nist conformance",
		"owasp compliant",
		"owasp compliance",
		"owasp ready",
		"owasp-ready",
		"owasp readiness",
		"owasp-readiness",
		"owasp approved",
		"owasp approval",
		"owasp authorization",
		"owasp authorized",
		"owasp-certified",
		"owasp certified",
		"owasp certification",
		"owasp attestation",
		"owasp conformance",
		"mcp compliant",
		"mcp compliance",
		"mcp ready",
		"mcp-ready",
		"mcp readiness",
		"mcp-readiness",
		"mcp approved",
		"mcp approval",
		"mcp authorization",
		"mcp authorized",
		"mcp-certified",
		"mcp certified",
		"mcp certification",
		"mcp attestation",
		"mcp conformance",
		"fedramp compliant",
		"fedramp compliance",
		"fedramp ready",
		"fedramp-ready",
		"fedramp readiness",
		"fedramp-readiness",
		"fedramp approved",
		"fedramp approval",
		"fedramp authorization",
		"fedramp authorized",
		"fedramp-certified",
		"fedramp certified",
		"fedramp certification",
		"fedramp attestation",
		"dod ready",
		"dod-ready",
		"dod readiness",
		"dod-readiness",
		"dod approved",
		"dod approval",
		"dod authorization",
		"dod authorized",
		"soc 2 ready",
		"soc 2 readiness",
		"soc 2 approved",
		"soc 2 approval",
		"soc2 ready",
		"soc2 readiness",
		"soc2 approved",
		"soc2 approval",
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

	return p99ContainsNoAssertionNegation(prefix)
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

func p98IntegrationGateFromSignals(signals []productEvidenceSignalSummary) p98IntegrationGateResult {
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

func p98IntegrationBlockersFromSignal(signal productEvidenceSignalSummary) []string {
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

	return localops.CompactStrings(blockers...)
}

func summarizeP711Signals(signals []productEvidenceSignalSummary) productEvidenceSignalCompleteness {
	out := productEvidenceSignalCompleteness{Required: []string{"activation", "retained_use", "integration_proof", "benchmark", "audit", "learning"}, RequiredCount: 6}

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

func p711ExportStatus(completeness productEvidenceSignalCompleteness) string {
	switch completeness.Status {
	case "complete":
		return "pass"
	case "blocked":
		return "blocked"
	default:
		return "partial"
	}
}

func p711FileRef(id string, kind string, path string) productEvidenceSourceReference {
	ref := productEvidenceSourceReference{ID: id, Kind: kind, Status: "not_available", PathID: localops.PathEvidence(path)}
	if data, err := os.ReadFile(path); err == nil {
		ref.ArtifactHash = localops.ContentHash(data)
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

		return localops.ForbiddenStringEvidence(trimmed)
	}

	return p711ForbiddenValidationJSONValue([]byte(trimmed))
}

func p711ForbiddenValidationJSONValue(data []byte) string {
	var value any

	dec := json.NewDecoder(bytes.NewReader(data))

	if err := dec.Decode(&value); err == nil {
		return p711ForbiddenValidationValue(value)
	}

	return localops.ForbiddenStringEvidence(string(data))
}

func p711ForbiddenValidationValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		for _, key := range keys {
			if forbidden := p711ForbiddenValidationKey(key); forbidden != "" {
				return forbidden
			}

			if boolValue, ok := typed[key].(bool); ok && boolValue && localops.ForbiddenBooleanEvidenceKey(key) {
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
	normalized := localops.SafeToken(key)
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
	if forbidden := localops.ForbiddenStringEvidence(value); forbidden != "" {
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
	switch localops.SafeToken(value) {
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

	token := localops.SafeToken(value)
	if token != "" && len(token) <= 80 && !localops.TokenNeedsRedaction(token) {
		return token
	}

	return prefix + "_id:" + localops.StableID(value)
}

func p711SafeStatus(value string) string {
	value = localops.SafeToken(value)
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

func p711GateFromSignal(signals []productEvidenceSignalSummary, id string) string {
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

	slices.Sort(ids)

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

func productEvidenceReportHash(report productEvidenceExportReport) string {
	report.GeneratedAt = ""
	report.ExportID = ""
	report.ArtifactHash = ""

	body, err := json.Marshal(report)
	if err != nil {
		return localops.StableID("product-evidence-report")
	}

	return localops.ContentHash(body)
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

func SanitizeProductEvidenceTelemetryArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		if flag, value, ok := strings.Cut(arg, "="); ok {
			switch flag {
			case "--project-root", "--output", "--local-ops-report", "--validation-records", "--benchmark-report":
				out[i] = flag + "=" + redactedProductEvidencePathArg(value)
			}

			continue
		}

		switch arg {
		case "--project-root", "--output", "--local-ops-report", "--validation-records", "--benchmark-report":
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

	return "<redacted_product_evidence_path id=" + localops.StableID(value) + ">"
}

func printProductEvidenceUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: idiom product-evidence export [--project-root <root>] [--local-ops-report <report.json>] [--validation-records <records.json>] [--output <path>]")
}
