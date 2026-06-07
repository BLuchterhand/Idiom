package localops

import "idiom/internal/evidence/eval"

const BenchmarkSchemaVersion = "idiom.local_ops_benchmark.v1"

type BenchmarkReport struct {
	SchemaVersion string           `json:"schema_version"`
	GeneratedAt   string           `json:"generated_at"`
	Command       string           `json:"command"`
	ConfigPath    string           `json:"config_path"`
	Profiles      []ProfileReport  `json:"profiles"`
	Summary       BenchmarkSummary `json:"summary"`
	Replay        ReplayReport     `json:"replay"`
	Quickstart    *QuickstartSmoke `json:"quickstart_smoke,omitempty"`
	Privacy       PrivacyReport    `json:"privacy"`
	Telemetry     *TelemetryReport `json:"telemetry,omitempty"`
	Blockers      []Blocker        `json:"blockers,omitempty"`
}

type ProfileReport struct {
	Name      string                   `json:"name"`
	State     string                   `json:"state"`
	Smoke     SmokeReport              `json:"smoke"`
	Capacity  CapacityReport           `json:"capacity"`
	Readiness ReadinessProfileEvidence `json:"readiness"`
	Blockers  []Blocker                `json:"blockers,omitempty"`
}

type ReadinessProfileEvidence struct {
	Enforcement          string            `json:"enforcement"`
	RequiredForLaunch    bool              `json:"required_for_launch"`
	TaskFamily           string            `json:"task_family"`
	Contract             string            `json:"contract"`
	PermissionProfile    string            `json:"permission_profile,omitempty"`
	RoleAliases          map[string]string `json:"role_aliases,omitempty"`
	HardFailures         int               `json:"hard_failures"`
	SoftDegradations     int               `json:"soft_degradations"`
	UnavailableChecks    int               `json:"unavailable_checks"`
	BackendState         string            `json:"backend_state,omitempty"`
	QueueState           string            `json:"queue_state,omitempty"`
	ContextBudgetState   string            `json:"context_budget_state,omitempty"`
	RouteClassifierState string            `json:"route_classifier_state,omitempty"`
}

type SmokeReport struct {
	Status       string            `json:"status"`
	Passed       int               `json:"passed"`
	Total        int               `json:"total"`
	PassRate     float64           `json:"pass_rate"`
	Signals      map[string]string `json:"signals,omitempty"`
	Unsupported  []Blocker         `json:"unsupported,omitempty"`
	Degradations []Blocker         `json:"degradations,omitempty"`
}

type CapacityReport struct {
	Status             string    `json:"status"`
	ContextTokens      int       `json:"context_tokens,omitempty"`
	MaxOutputTokens    int       `json:"max_output_tokens,omitempty"`
	PromptBudgetTokens int       `json:"prompt_budget_tokens,omitempty"`
	QueuePools         int       `json:"queue_pools,omitempty"`
	QueueInFlight      int       `json:"queue_in_flight,omitempty"`
	QueueWaiting       int       `json:"queue_waiting,omitempty"`
	QueueIdle          int       `json:"queue_idle,omitempty"`
	Limits             []Limit   `json:"limits,omitempty"`
	Blockers           []Blocker `json:"blockers,omitempty"`
}

type Limit struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Value  int    `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ReplayReport struct {
	Status    string                 `json:"status"`
	LogCount  int                    `json:"log_count"`
	LogIDs    []string               `json:"log_ids,omitempty"`
	Aggregate *ReplayAggregateReport `json:"aggregate,omitempty"`
	Signals   ReplaySignals          `json:"signals,omitempty"`
	Blockers  []Blocker              `json:"blockers,omitempty"`
}

type ReplaySignals struct {
	ToolCalls            int `json:"tool_calls,omitempty"`
	RepeatedToolSignals  int `json:"repeated_tool_signals,omitempty"`
	MalformedToolSignals int `json:"malformed_tool_signals,omitempty"`
	BudgetExhausted      int `json:"budget_exhausted,omitempty"`
	QueueDegraded        int `json:"queue_degraded,omitempty"`
	BackendEvents        int `json:"backend_events,omitempty"`
}

type ReplayAggregateReport struct {
	LogCount             int                   `json:"log_count"`
	MedianToolResults    int                   `json:"median_tool_results"`
	MaxToolResults       int                   `json:"max_tool_results"`
	MedianPromptTokens   int                   `json:"median_prompt_tokens"`
	MaxPromptTokens      int                   `json:"max_prompt_tokens"`
	MedianDurationMillis int                   `json:"median_duration_millis"`
	TotalRejectedActions int                   `json:"total_rejected_actions"`
	PatternHitRate       []eval.PerfPatternHit `json:"pattern_hit_rate,omitempty"`
	ToolMix              []eval.PerfToolMix    `json:"tool_mix,omitempty"`
	Context              *eval.ContextStats    `json:"context,omitempty"`
	Feedback             *eval.FeedbackStats   `json:"feedback,omitempty"`
	LocalOps             *eval.LocalOpsStats   `json:"local_ops,omitempty"`
	StopReasonCounts     map[string]int        `json:"stop_reason_counts,omitempty"`
	WorstOffenders       []WorstOffender       `json:"worst_offenders,omitempty"`
}

type WorstOffender struct {
	LogID              string `json:"log_id"`
	AntiPatternScore   int    `json:"anti_pattern_score"`
	ToolResults        int    `json:"tool_results"`
	LogExcerptRedacted bool   `json:"log_excerpt_redacted,omitempty"`
}

type BenchmarkSummary struct {
	ProfileCount     int     `json:"profile_count"`
	SmokePassRate    float64 `json:"smoke_pass_rate"`
	SmokePassed      int     `json:"smoke_passed"`
	SmokeTotal       int     `json:"smoke_total"`
	CapacityBlocked  int     `json:"capacity_blocked"`
	ReplayLogCount   int     `json:"replay_log_count"`
	BlockerCount     int     `json:"blocker_count"`
	DegradationCount int     `json:"degradation_count"`
}

type PrivacyReport struct {
	MetadataOnly           bool     `json:"metadata_only"`
	RawPromptCaptured      bool     `json:"raw_prompt_captured"`
	RawFileBodyCaptured    bool     `json:"raw_file_body_captured"`
	SecretCaptureAllowed   bool     `json:"secret_capture_allowed"`
	RedactedFields         []string `json:"redacted_fields"`
	LocalArtifactDirectory string   `json:"local_artifact_directory,omitempty"`
}

type TelemetryReport struct {
	Path string `json:"path,omitempty"`
	ID   string `json:"id,omitempty"`
}

type QuickstartSmoke struct {
	SmokeID                    string                      `json:"smoke_id"`
	Status                     string                      `json:"status"`
	ClientClassification       string                      `json:"client_classification"`
	EnvironmentClass           string                      `json:"environment_class"`
	AssistanceClass            string                      `json:"assistance_class"`
	DurationSource             string                      `json:"duration_source"`
	TimeToFirstPacketSeconds   float64                     `json:"time_to_first_packet_seconds"`
	TimeToTrustEvidenceSeconds float64                     `json:"time_to_trust_evidence_seconds"`
	SetupInstallReadiness      ActivationSetupEvidence     `json:"setup_install_readiness"`
	FirstUsefulPacket          ActivationPacketEvidence    `json:"first_useful_packet"`
	FreshnessCheck             ActivationFreshnessEvidence `json:"freshness_check"`
	AuditReport                ActivationAuditEvidence     `json:"audit_report"`
	SuggestedVerification      ActivationVerification      `json:"suggested_verification"`
	ProductEvidenceSafeOutput  ActivationProductEvidence   `json:"product_evidence_safe_output"`
	FailureReason              ActivationFailureReason     `json:"failure_reason"`
	DocsGap                    ActivationDocsGap           `json:"docs_gap"`
	PrivacyRedactionStatus     ActivationPrivacyEvidence   `json:"privacy_redaction_status"`
	Cleanup                    ActivationCleanupEvidence   `json:"cleanup"`
	Commands                   []ActivationCommand         `json:"commands"`
}

type ActivationCommand struct {
	CommandID      string  `json:"command_id"`
	Label          string  `json:"label"`
	ExitStatus     string  `json:"exit_status"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	ErrorClass     string  `json:"error_class,omitempty"`
}

type ActivationSetupEvidence struct {
	Status              string `json:"status"`
	CommandID           string `json:"command_id"`
	InstallSetupPath    string `json:"install_setup_path"`
	BinaryVersion       string `json:"binary_version"`
	GitCommit           string `json:"git_commit,omitempty"`
	OSArch              string `json:"os_arch"`
	ReleaseInstallProof string `json:"release_install_proof"`
	ReadinessProfile    string `json:"readiness_profile"`
}

type ActivationPacketEvidence struct {
	Status               string                 `json:"status"`
	CommandID            string                 `json:"command_id"`
	Tool                 string                 `json:"tool"`
	Task                 ActivationTaskEvidence `json:"task"`
	PacketID             string                 `json:"packet_id,omitempty"`
	IndexID              string                 `json:"index_id,omitempty"`
	FreshnessState       string                 `json:"freshness_state,omitempty"`
	CandidateFileCount   int                    `json:"candidate_file_count"`
	CandidateSymbolCount int                    `json:"candidate_symbol_count"`
	CandidateTestCount   int                    `json:"candidate_test_count"`
	UsefulEvidence       []string               `json:"useful_evidence,omitempty"`
	BlockerReason        string                 `json:"blocker_reason,omitempty"`
}

type ActivationTaskEvidence struct {
	TaskID          string   `json:"task_id"`
	EvidenceType    string   `json:"evidence_type"`
	RedactionStatus string   `json:"redaction_status"`
	RawTaskCaptured bool     `json:"raw_task_captured"`
	ContentClasses  []string `json:"content_classes,omitempty"`
}

type ActivationFreshnessEvidence struct {
	Status         string `json:"status"`
	CommandID      string `json:"command_id"`
	Tool           string `json:"tool"`
	IndexID        string `json:"index_id,omitempty"`
	State          string `json:"state,omitempty"`
	FreshForScope  bool   `json:"fresh_for_scope"`
	GlobalFresh    bool   `json:"global_fresh"`
	StalePathCount int    `json:"stale_path_count"`
	BlockerReason  string `json:"blocker_reason,omitempty"`
}

type ActivationAuditEvidence struct {
	Status             string   `json:"status"`
	CommandID          string   `json:"command_id"`
	Tool               string   `json:"tool"`
	Purpose            string   `json:"purpose"`
	AuditReportID      string   `json:"audit_report_id,omitempty"`
	AuditEventIDs      []string `json:"audit_event_ids,omitempty"`
	HighFindingCount   int      `json:"high_finding_count"`
	MediumFindingCount int      `json:"medium_finding_count"`
	FindingCount       int      `json:"finding_count"`
	BlockerReason      string   `json:"blocker_reason,omitempty"`
}

type ActivationVerification struct {
	Status            string   `json:"status"`
	CommandID         string   `json:"command_id"`
	SuggestedLabels   []string `json:"suggested_labels,omitempty"`
	SuggestedCommands []string `json:"suggested_commands,omitempty"`
	WasRun            bool     `json:"was_run"`
	NotRunReason      string   `json:"not_run_reason,omitempty"`
	FailureReason     string   `json:"failure_reason,omitempty"`
}

type ActivationProductEvidence struct {
	Status                 string   `json:"status"`
	ArtifactHash           string   `json:"artifact_hash,omitempty"`
	ExportID               string   `json:"export_id,omitempty"`
	FieldList              []string `json:"field_list,omitempty"`
	ForbiddenFieldCheck    string   `json:"forbidden_field_check"`
	RedactionStatus        string   `json:"redaction_status"`
	P7_11Linkage           string   `json:"p7_11_linkage"`
	P9_6Linkage            string   `json:"p9_6_linkage"`
	HostedTelemetryDefault bool     `json:"hosted_telemetry_default"`
}

type ActivationFailureReason struct {
	Status       string `json:"status"`
	PrimaryClass string `json:"primary_class"`
	Reason       string `json:"reason,omitempty"`
	RetryAllowed bool   `json:"retry_allowed"`
}

type ActivationDocsGap struct {
	Status            string `json:"status"`
	GapRecorded       bool   `json:"gap_recorded"`
	DocPath           string `json:"doc_path,omitempty"`
	CommandLabel      string `json:"command_label,omitempty"`
	ExpectedOutput    string `json:"expected_output,omitempty"`
	ActualOutput      string `json:"actual_output,omitempty"`
	Severity          string `json:"severity,omitempty"`
	BlockedActivation bool   `json:"blocked_activation,omitempty"`
}

type ActivationPrivacyEvidence struct {
	Status                  string   `json:"status"`
	RedactionReviewer       string   `json:"redaction_reviewer"`
	ForbiddenFieldCheck     string   `json:"forbidden_field_check"`
	RawPromptCaptured       bool     `json:"raw_prompt_captured"`
	RawFileBodyCaptured     bool     `json:"raw_file_body_captured"`
	SecretCaptured          bool     `json:"secret_captured"`
	PrivatePathCaptured     bool     `json:"private_path_captured"`
	PrivateRepoURLCaptured  bool     `json:"private_repo_url_captured"`
	UnredactedLogCaptured   bool     `json:"unredacted_log_captured"`
	HostedTelemetryRequired bool     `json:"hosted_telemetry_required"`
	RedactedFields          []string `json:"redacted_fields,omitempty"`
}

type ActivationCleanupEvidence struct {
	Status               string   `json:"status"`
	CommandID            string   `json:"command_id"`
	Mode                 string   `json:"mode"`
	ExitStatus           string   `json:"exit_status"`
	RemovedArtifactCount int      `json:"removed_artifact_count"`
	RetainedStatePolicy  string   `json:"retained_state_policy,omitempty"`
	RemainingState       string   `json:"remaining_state"`
	EvidenceIDs          []string `json:"evidence_ids,omitempty"`
	BlockerReason        string   `json:"blocker_reason,omitempty"`
}

type Blocker struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}
