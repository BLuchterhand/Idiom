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
	Gate        string `json:"gate"`
	Status      string `json:"status"`
	Evidence    string `json:"evidence"`
	ClaimImpact string `json:"claim_impact"`
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
			SecurityPrivacy:            p711GateFromSignal(signals, "audit"),
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
		ReleaseGateLinkage: buildP711ReleaseGateLinkage(signals, classCoverage),
		Limitations: []string{
			"export summarizes bounded local metadata only",
			"prompt bodies, file bodies, and transcript bodies are not loaded",
			"retained-use and adoption signals are not claimed unless validation records are supplied",
			"MCP stdio parity remains P8.6 scope when only the P7.8 placeholder is present",
		},
		Metadata: map[string]any{
			"generated_by": "idiom product-evidence export",
		},
	}
	report.ArtifactHash = productEvidenceReportHash(report)
	report.ExportID = "product-evidence-" + report.ArtifactHash
	return report
}

func productEvidenceAvailabilityGate(available bool) string {
	if available {
		return "partial"
	}
	return "not_available"
}

type p711IntegrationProofSet struct {
	SchemaVersion string                 `json:"schema_version"`
	Name          string                 `json:"name"`
	Proofs        []p711IntegrationProof `json:"proofs"`
}

type p711IntegrationProof struct {
	ID        string          `json:"id"`
	Path      string          `json:"path"`
	Status    string          `json:"status"`
	Readiness map[string]bool `json:"readiness"`
	Privacy   struct {
		RawPrompts               bool `json:"raw_prompts"`
		FileBodies               bool `json:"file_bodies"`
		PrivatePaths             bool `json:"private_paths"`
		Credentials              bool `json:"credentials"`
		PrivateRepoOrCustomerIDs bool `json:"private_repo_or_customer_ids"`
	} `json:"privacy"`
}

type p711SecurityPosture struct {
	ArtifactID                   string                 `json:"artifact_id"`
	RequiredControls             []struct{ ID string }  `json:"required_controls"`
	ClientClassificationCoverage []p711SecurityClassRow `json:"client_classification_coverage"`
}

type p711SecurityClassRow struct {
	Classification string `json:"classification"`
	Stance         string `json:"stance"`
	P76CommandID   string `json:"p7_6_command_id"`
	Limitation     string `json:"limitation"`
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
	RecordID             string `json:"record_id"`
	ClientClassification string `json:"client_classification"`
	EvidenceStatus       string `json:"evidence_status"`
	RetainedUseStatus    string `json:"retained_use_status"`
	AdoptionSignalType   string `json:"adoption_signal_type"`
	RedactionStatus      string `json:"redaction_status"`
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
	if q.ProductEvidenceSafeOutput.ForbiddenFieldCheck != "pass" {
		s.Status = "blocked"
		s.Completeness = "blocked"
	}
	return s
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
		if record.RetainedUseStatus == "observed" || record.RetainedUseStatus == "pass" {
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

func p711IntegrationSignal(proofs p711IntegrationProofSet) p711SignalSummary {
	s := p711SignalSummary{ID: "integration_proof", Status: "not_available", Completeness: "missing", SourceRefs: []string{"p7.8_integration_proofs"}, RedactionStatus: "redacted", ReleaseGates: []string{"P7.8", "P8.6", "P9.8"}, Counts: map[string]int{}, Limitations: []string{"MCP stdio remains a reviewed placeholder until P8.6 supplies replayable parity evidence"}}
	for _, proof := range proofs.Proofs {
		s.EvidenceIDs = append(s.EvidenceIDs, proof.ID)
		s.Counts["proof_paths"]++
		if proof.Readiness["executable_evidence"] {
			s.Counts["executable_paths"]++
		}
		if proof.Readiness["reviewed_placeholder"] {
			s.Counts["reviewed_placeholders"]++
		}
		if proof.Privacy.RawPrompts || proof.Privacy.FileBodies || proof.Privacy.PrivatePaths || proof.Privacy.Credentials || proof.Privacy.PrivateRepoOrCustomerIDs {
			s.Counts["privacy_blockers"]++
		}
	}
	if s.Counts["privacy_blockers"] > 0 {
		s.Status = "blocked"
		s.Completeness = "blocked"
	} else if s.Counts["proof_paths"] > 0 {
		s.Status = "partial"
		s.Completeness = "partial"
	}
	return s
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

func buildP711ReleaseGateLinkage(signals []p711SignalSummary, classes []p711ClassCoverage) []p711ReleaseGateLinkage {
	classStatus := "evidence_present"
	for _, row := range classes {
		if row.Status != "pass" {
			classStatus = "needs_evidence"
			break
		}
	}
	return []p711ReleaseGateLinkage{
		{Gate: "P9.1", Status: p711GateFromSignal(signals, "benchmark"), Evidence: "benchmark,audit,learning summaries", ClaimImpact: "release bundle input; not a release decision"},
		{Gate: "P9.6", Status: p711GateFromSignal(signals, "activation"), Evidence: "activation quickstart smoke or blocker summary", ClaimImpact: "activation claims depend on pass status"},
		{Gate: "P9.7", Status: p711GateFromSignal(signals, "retained_use"), Evidence: "retained-use validation records and adoption-signal counts", ClaimImpact: "demand remains unclaimed without record-backed signal"},
		{Gate: "P9.12", Status: classStatus, Evidence: "classification coverage rows and limitations", ClaimImpact: "packaging/support thesis must cite statuses"},
	}
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
	var ids []string
	for _, proof := range proofs.Proofs {
		if proof.ID != "" {
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
