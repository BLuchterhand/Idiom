package autonomy

import (
	"fmt"
	"strings"
	"time"

	lanecontract "idiom/internal/contracts/lane"
)

const (
	ParentChildInvocationSchemaVersion  = "idiom.parent_child_invocation.v1"
	ChildTerminalSchemaVersion          = "idiom.child_terminal.v1"
	ChildAcceptanceSchemaVersion        = "idiom.child_acceptance.v1"
	CandidatePatchArtifactSchemaVersion = "idiom.candidate_patch_artifact.v1"
	VerificationAcceptanceSchemaVersion = "idiom.verification_acceptance.v1"
)

type ChildWorkspaceMode string

const (
	ChildWorkspaceMain       ChildWorkspaceMode = "main"
	ChildWorkspaceWorkbranch ChildWorkspaceMode = "workbranch"
	ChildWorkspaceNoWrite    ChildWorkspaceMode = "no_write"
)

type RoleResolutionSource string

const (
	RoleResolutionSourceProxyDefault           RoleResolutionSource = "proxy_default"
	RoleResolutionSourceRoleTag                RoleResolutionSource = "role_tag"
	RoleResolutionSourceLoneModel              RoleResolutionSource = "lone_model"
	RoleResolutionSourceExecutorCoderHeuristic RoleResolutionSource = "executor_coder_heuristic"
	RoleResolutionSourceUnresolved             RoleResolutionSource = "unresolved"
)

type CapacityAdmissionState string

const (
	CapacityAdmissionAcquired    CapacityAdmissionState = "acquired"
	CapacityAdmissionUnavailable CapacityAdmissionState = "unavailable"
	CapacityAdmissionDisabled    CapacityAdmissionState = "disabled"
	CapacityAdmissionDegraded    CapacityAdmissionState = "degraded"
	CapacityAdmissionError       CapacityAdmissionState = "error"
)

type CapacityAdmissionEnforcement string

const (
	CapacityAdmissionBlocking      CapacityAdmissionEnforcement = "blocking"
	CapacityAdmissionOpportunistic CapacityAdmissionEnforcement = "opportunistic"
)

type ChildTransportMode string

const (
	ChildTransportNative  ChildTransportMode = "native"
	ChildTransportTextual ChildTransportMode = "textual"
	ChildTransportNone    ChildTransportMode = "none"
)

type ChildObservedTransport string

const (
	ChildObservedTransportNative        ChildObservedTransport = "native"
	ChildObservedTransportTextual       ChildObservedTransport = "textual"
	ChildObservedTransportDeterministic ChildObservedTransport = "deterministic"
)

type ChildNormalizerStatus string

const (
	ChildNormalizerNotRequired ChildNormalizerStatus = "not_required"
	ChildNormalizerNormalized  ChildNormalizerStatus = "normalized"
	ChildNormalizerFailed      ChildNormalizerStatus = "failed"
)

type ChildResultKind string

const (
	ChildResultEvidencePacket    ChildResultKind = "evidence_packet"
	ChildResultPlanArtifact      ChildResultKind = "plan_artifact"
	ChildResultCandidatePatch    ChildResultKind = "candidate_patch"
	ChildResultFindings          ChildResultKind = "findings"
	ChildResultSourceSatisfied   ChildResultKind = "source_satisfied"
	ChildResultTypedBlocker      ChildResultKind = "typed_blocker"
	ChildResultAppliedParentEdit ChildResultKind = "applied_parent_edit"
)

type ChildTerminalStatus string

const (
	ChildTerminalCompleted       ChildTerminalStatus = "completed"
	ChildTerminalBlocked         ChildTerminalStatus = "blocked"
	ChildTerminalFailed          ChildTerminalStatus = "failed"
	ChildTerminalBudgetExhausted ChildTerminalStatus = "budget_exhausted"
	ChildTerminalParseFailed     ChildTerminalStatus = "parse_failed"
)

type ChildObjectiveAlignment string

const (
	ChildObjectiveAccepted      ChildObjectiveAlignment = "accepted"
	ChildObjectiveWeak          ChildObjectiveAlignment = "weak"
	ChildObjectiveOffObjective  ChildObjectiveAlignment = "off_objective"
	ChildObjectiveNotApplicable ChildObjectiveAlignment = "not_applicable"
)

type ChildExecutionRoute string

const (
	ChildRouteChildModel          ChildExecutionRoute = "child_model"
	ChildRouteDeterministicDirect ChildExecutionRoute = "deterministic_direct"
	ChildRouteParentExecutor      ChildExecutionRoute = "parent_executor"
	ChildRouteNotApplicable       ChildExecutionRoute = "not_applicable"
)

type ChildPathGateDecision string

const (
	ChildPathGateAllow         ChildPathGateDecision = "allow"
	ChildPathGateDeny          ChildPathGateDecision = "deny"
	ChildPathGateNudge         ChildPathGateDecision = "nudge"
	ChildPathGateReject        ChildPathGateDecision = "reject"
	ChildPathGateNotApplicable ChildPathGateDecision = "not_applicable"
)

type ChildAcceptanceDecision string

const (
	ChildAcceptanceAccepted       ChildAcceptanceDecision = "accepted"
	ChildAcceptanceRejected       ChildAcceptanceDecision = "rejected"
	ChildAcceptanceRepairRequired ChildAcceptanceDecision = "repair_required"
	ChildAcceptanceBlocked        ChildAcceptanceDecision = "blocked"
	ChildAcceptanceRecordedOnly   ChildAcceptanceDecision = "recorded_only"
)

type AlignmentEvaluator string

const (
	AlignmentEvaluatorHostContract      AlignmentEvaluator = "host_contract"
	AlignmentEvaluatorReviewerFinding   AlignmentEvaluator = "reviewer_finding"
	AlignmentEvaluatorDeterministicRule AlignmentEvaluator = "deterministic_rule"
)

type ChildLifecycleRole string
type ChildLifecycleLane string
type ChildLifecyclePhase string
type ChildBackendKind string
type ChildModelRole string
type ChildParserContract string

const (
	ChildLifecycleRoleReviewRepair  ChildLifecycleRole  = "review_repair"
	ChildLifecycleLanePlanExecute   ChildLifecycleLane  = ChildLifecycleLane(lanecontract.LanePlanExecute)
	ChildLifecyclePhaseReviewRepair ChildLifecyclePhase = "review_repair"

	ChildBackendKindPromptRunner ChildBackendKind = "prompt_runner"
	ChildModelRoleReviewRepair   ChildModelRole   = "review_repair"

	ChildParserContractReviewRepairTerminal ChildParserContract = "review_repair_terminal"

	ChildLifecycleReasonReviewRepairAccepted = "review_repair_accepted"
	ChildScopeRefLanePlanExecute             = lanecontract.ScopeRefPlanExecute
	ChildScopeRefPhaseReviewRepair           = "phase:review_repair"
)

type VerificationAcceptanceDecision string

const (
	VerificationAcceptanceAccepted       VerificationAcceptanceDecision = "accepted"
	VerificationAcceptanceRejected       VerificationAcceptanceDecision = "rejected"
	VerificationAcceptanceRepairRequired VerificationAcceptanceDecision = "repair_required"
	VerificationAcceptanceBlocked        VerificationAcceptanceDecision = "blocked"
)

type ParentChildInvocationRecord struct {
	SchemaVersion    string               `json:"schema_version"`
	InvocationID     string               `json:"invocation_id"`
	ParentContractID string               `json:"parent_contract_id"`
	ChildContractID  string               `json:"child_contract_id"`
	Role             string               `json:"role"`
	Lane             string               `json:"lane"`
	Phase            string               `json:"phase"`
	WorkspaceMode    ChildWorkspaceMode   `json:"workspace_mode"`
	PackID           string               `json:"pack_id,omitempty"`
	ContractSummary  ChildContractSummary `json:"contract_summary,omitempty"`
	RuntimeEnvelope  ChildRuntimeEnvelope `json:"runtime_envelope"`
	CreatedAt        time.Time            `json:"created_at"`
}

type ChildContractSummary struct {
	PathRoleDigest   string `json:"path_role_digest,omitempty"`
	ToolPolicyDigest string `json:"tool_policy_digest,omitempty"`
	ObligationDigest string `json:"obligation_digest,omitempty"`
}

type ChildRuntimeEnvelope struct {
	BackendKind         string                 `json:"backend_kind"`
	ModelAlias          string                 `json:"model_alias"`
	ModelRole           string                 `json:"model_role"`
	RoleResolution      ChildRoleResolution    `json:"role_resolution"`
	CapacityAdmission   ChildCapacityAdmission `json:"capacity_admission"`
	ToolTransportMode   ChildTransportMode     `json:"tool_transport_mode"`
	ParserTransportMode ChildTransportMode     `json:"parser_transport_mode"`
	ParserContract      string                 `json:"parser_contract,omitempty"`
	ResponseFormat      string                 `json:"response_format,omitempty"`
	ToolChoice          string                 `json:"tool_choice,omitempty"`
	SupportsNativeTools bool                   `json:"supports_native_tools"`
	ForceNativeTools    bool                   `json:"force_native_tools"`
	ThinkingPolicy      string                 `json:"thinking_policy,omitempty"`
	MaxTokens           int                    `json:"max_tokens,omitempty"`
	ContextBudget       ChildContextBudget     `json:"context_budget,omitempty"`
	ToolSchemaHash      string                 `json:"tool_schema_hash,omitempty"`
	RequestTimeoutMS    int                    `json:"request_timeout_ms,omitempty"`
}

type ChildRoleResolution struct {
	RequestedRole string               `json:"requested_role"`
	ResolvedRole  string               `json:"resolved_role,omitempty"`
	Alias         string               `json:"alias,omitempty"`
	Source        RoleResolutionSource `json:"source"`
	OK            bool                 `json:"ok"`
}

type ChildCapacityAdmission struct {
	State       CapacityAdmissionState       `json:"state"`
	Pool        string                       `json:"pool,omitempty"`
	ModelAlias  string                       `json:"model_alias,omitempty"`
	WaitMS      int                          `json:"wait_ms,omitempty"`
	Degraded    bool                         `json:"degraded,omitempty"`
	ReasonClass string                       `json:"reason_class,omitempty"`
	Enforcement CapacityAdmissionEnforcement `json:"enforcement"`
}

type ChildContextBudget struct {
	MaxContextMessages int `json:"max_context_messages,omitempty"`
	MaxContextChars    int `json:"max_context_chars,omitempty"`
}

type ChildTerminalRecord struct {
	SchemaVersion      string                  `json:"schema_version"`
	TerminalID         string                  `json:"terminal_id"`
	InvocationID       string                  `json:"invocation_id"`
	ChildContractID    string                  `json:"child_contract_id"`
	ResultKind         ChildResultKind         `json:"result_kind"`
	Status             ChildTerminalStatus     `json:"status"`
	ArtifactRefs       []string                `json:"artifact_refs,omitempty"`
	EvidenceRefs       []string                `json:"evidence_refs,omitempty"`
	ChangedPaths       []string                `json:"changed_paths,omitempty"`
	PackID             string                  `json:"pack_id,omitempty"`
	ExecutionMetadata  ChildExecutionMetadata  `json:"execution_metadata,omitempty"`
	ObjectiveAlignment ChildObjectiveAlignment `json:"objective_alignment"`
	ParseStatus        ChildParseStatus        `json:"parse_status,omitempty"`
}

type ChildExecutionMetadata struct {
	Route                      ChildExecutionRoute   `json:"route,omitempty"`
	ParentPlanAuthorityRef     string                `json:"parent_plan_authority_ref,omitempty"`
	PathGateRef                string                `json:"path_gate_ref,omitempty"`
	PathGateDecision           ChildPathGateDecision `json:"path_gate_decision,omitempty"`
	VerificationObligationRefs []string              `json:"verification_obligation_refs,omitempty"`
}

type ChildParseStatus struct {
	SchemaVersion          string                 `json:"schema_version,omitempty"`
	TransportObserved      ChildObservedTransport `json:"transport_observed,omitempty"`
	NormalizerStatus       ChildNormalizerStatus  `json:"normalizer_status,omitempty"`
	NormalizedOutputSchema string                 `json:"normalized_output_schema,omitempty"`
	ParserFailureClass     string                 `json:"parser_failure_class,omitempty"`
	RawOutputRedacted      bool                   `json:"raw_output_redacted,omitempty"`
}

type CandidatePatchArtifact struct {
	SchemaVersion         string        `json:"schema_version"`
	PatchID               string        `json:"patch_id"`
	InvocationID          string        `json:"invocation_id"`
	ChildContractID       string        `json:"child_contract_id"`
	BaseSHA               string        `json:"base_sha"`
	WorktreePathRef       string        `json:"worktree_path_ref,omitempty"`
	ChangedPaths          []string      `json:"changed_paths"`
	DiffStat              PatchDiffStat `json:"diff_stat"`
	PatchPath             string        `json:"patch_path"`
	PatchDigest           string        `json:"patch_digest"`
	VerificationClaimRefs []string      `json:"verification_claim_refs,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
}

type PatchDiffStat struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

type ChildAcceptanceRecord struct {
	SchemaVersion              string                  `json:"schema_version"`
	AcceptanceID               string                  `json:"acceptance_id"`
	ParentContractID           string                  `json:"parent_contract_id"`
	ChildContractID            string                  `json:"child_contract_id"`
	InvocationID               string                  `json:"invocation_id"`
	TerminalID                 string                  `json:"terminal_id"`
	ResultKind                 ChildResultKind         `json:"result_kind"`
	Decision                   ChildAcceptanceDecision `json:"decision"`
	RuntimeEnvelopeRef         string                  `json:"runtime_envelope_ref,omitempty"`
	PackID                     string                  `json:"pack_id,omitempty"`
	PackFreshness              Freshness               `json:"pack_freshness,omitempty"`
	AcceptedEvidenceRefs       []string                `json:"accepted_evidence_refs,omitempty"`
	AcceptedPatchRefs          []string                `json:"accepted_patch_refs,omitempty"`
	RequiredRepairPaths        []string                `json:"required_repair_paths,omitempty"`
	VerificationObligationRefs []string                `json:"verification_obligation_refs,omitempty"`
	ExecutionMetadataRef       string                  `json:"execution_metadata_ref,omitempty"`
	ParentPlanAuthorityRef     string                  `json:"parent_plan_authority_ref,omitempty"`
	PathGateRef                string                  `json:"path_gate_ref,omitempty"`
	PathGateDecision           ChildPathGateDecision   `json:"path_gate_decision,omitempty"`
	Route                      ChildExecutionRoute     `json:"route,omitempty"`
	AlignmentBasis             ChildAlignmentBasis     `json:"alignment_basis,omitempty"`
	ReasonCode                 string                  `json:"reason_code"`
}

type ChildAlignmentBasis struct {
	DelegatedObjectiveRef  string             `json:"delegated_objective_ref,omitempty"`
	EffectiveScopeRefs     []string           `json:"effective_scope_refs,omitempty"`
	AcceptedSourceBodyRefs []string           `json:"accepted_source_body_refs,omitempty"`
	CandidateOnlyRefs      []string           `json:"candidate_only_refs,omitempty"`
	MismatchReasons        []string           `json:"mismatch_reasons,omitempty"`
	Evaluator              AlignmentEvaluator `json:"evaluator,omitempty"`
}

type VerificationAcceptanceRecord struct {
	SchemaVersion            string                         `json:"schema_version"`
	VerificationAcceptanceID string                         `json:"verification_acceptance_id"`
	ParentContractID         string                         `json:"parent_contract_id"`
	ObligationRefs           []string                       `json:"obligation_refs"`
	CommandRef               string                         `json:"command_ref"`
	CommandFingerprint       string                         `json:"command_fingerprint"`
	WorkspaceRef             string                         `json:"workspace_ref"`
	BaseSHA                  string                         `json:"base_sha"`
	HeadSHA                  string                         `json:"head_sha"`
	ExitStatus               int                            `json:"exit_status"`
	ResultRef                string                         `json:"result_ref"`
	ScopeRefs                []string                       `json:"scope_refs,omitempty"`
	Decision                 VerificationAcceptanceDecision `json:"decision"`
	ReasonCode               string                         `json:"reason_code"`
}

func (r *ParentChildInvocationRecord) Normalize() {
	r.SchemaVersion = firstNonEmptyChildValue(r.SchemaVersion, ParentChildInvocationSchemaVersion)
	r.InvocationID = strings.TrimSpace(r.InvocationID)
	r.ParentContractID = strings.TrimSpace(r.ParentContractID)
	r.ChildContractID = strings.TrimSpace(r.ChildContractID)
	r.Role = strings.TrimSpace(r.Role)
	r.Lane = strings.TrimSpace(r.Lane)
	r.Phase = strings.TrimSpace(r.Phase)
	r.PackID = strings.TrimSpace(r.PackID)
	r.ContractSummary.normalize()
	r.RuntimeEnvelope.normalize()
	r.CreatedAt = normalizeChildTime(r.CreatedAt)
}

func (r ParentChildInvocationRecord) Validate() error {
	record := r
	record.Normalize()

	var problems validationProblems
	if record.SchemaVersion != ParentChildInvocationSchemaVersion {
		problems.Add("schema_version must be " + ParentChildInvocationSchemaVersion)
	}
	requireChildField(&problems, record.InvocationID, "invocation_id")
	requireChildField(&problems, record.ParentContractID, "parent_contract_id")
	requireChildField(&problems, record.ChildContractID, "child_contract_id")
	requireChildField(&problems, record.Role, "role")
	requireChildField(&problems, record.Lane, "lane")
	requireChildField(&problems, record.Phase, "phase")
	if !validChildWorkspaceMode(record.WorkspaceMode) {
		problems.Add("workspace_mode is required and must be main, workbranch, or no_write")
	}
	if record.CreatedAt.IsZero() {
		problems.Add("created_at is required")
	}
	record.RuntimeEnvelope.validate("runtime_envelope", &problems)
	validateChildSafeFields(&problems, "parent_child_invocation", record.InvocationID, record.ParentContractID, record.ChildContractID, record.Role, record.Lane, record.Phase, record.PackID, string(record.WorkspaceMode))
	record.ContractSummary.validate(&problems)
	return childRecordError("parent_child_invocation", problems)
}

func (r *ChildTerminalRecord) Normalize() {
	r.SchemaVersion = firstNonEmptyChildValue(r.SchemaVersion, ChildTerminalSchemaVersion)
	r.TerminalID = strings.TrimSpace(r.TerminalID)
	r.InvocationID = strings.TrimSpace(r.InvocationID)
	r.ChildContractID = strings.TrimSpace(r.ChildContractID)
	r.ArtifactRefs = compactStrings(r.ArtifactRefs)
	r.EvidenceRefs = compactStrings(r.EvidenceRefs)
	r.ChangedPaths = compactStrings(r.ChangedPaths)
	r.PackID = strings.TrimSpace(r.PackID)
	r.ExecutionMetadata.normalize()
	r.ParseStatus.normalize()
}

func (r ChildTerminalRecord) Validate() error {
	record := r
	record.Normalize()

	var problems validationProblems
	if record.SchemaVersion != ChildTerminalSchemaVersion {
		problems.Add("schema_version must be " + ChildTerminalSchemaVersion)
	}
	requireChildField(&problems, record.TerminalID, "terminal_id")
	requireChildField(&problems, record.InvocationID, "invocation_id")
	requireChildField(&problems, record.ChildContractID, "child_contract_id")
	if !validChildResultKind(record.ResultKind) {
		problems.Add("result_kind is required and must be a known child result kind")
	}
	if !validChildTerminalStatus(record.Status) {
		problems.Add("status is required and must be a known child terminal status")
	}
	if !validChildObjectiveAlignment(record.ObjectiveAlignment) {
		problems.Add("objective_alignment is required and must be accepted, weak, off_objective, or not_applicable")
	}
	if record.ResultKind == ChildResultCandidatePatch && len(record.ArtifactRefs) == 0 {
		problems.Add("artifact_refs are required for candidate_patch terminals")
	}
	if record.ResultKind == ChildResultAppliedParentEdit {
		record.ExecutionMetadata.validateAppliedParentEdit("execution_metadata", &problems)
		record.ParseStatus.validateAppliedParentEdit("parse_status", &problems)
	}
	if record.Status == ChildTerminalParseFailed {
		record.ParseStatus.validateParseFailure("parse_status", &problems)
	}
	record.ExecutionMetadata.validate("execution_metadata", &problems)
	record.ParseStatus.validate("parse_status", &problems)
	validateChildSafeFields(&problems, "child_terminal", record.TerminalID, record.InvocationID, record.ChildContractID, record.PackID)
	validateChildSafeSlice(&problems, "child_terminal.artifact_refs", record.ArtifactRefs)
	validateChildSafeSlice(&problems, "child_terminal.evidence_refs", record.EvidenceRefs)
	validateChildSafeSlice(&problems, "child_terminal.changed_paths", record.ChangedPaths)
	return childRecordError("child_terminal", problems)
}

func (r *CandidatePatchArtifact) Normalize() {
	r.SchemaVersion = firstNonEmptyChildValue(r.SchemaVersion, CandidatePatchArtifactSchemaVersion)
	r.PatchID = strings.TrimSpace(r.PatchID)
	r.InvocationID = strings.TrimSpace(r.InvocationID)
	r.ChildContractID = strings.TrimSpace(r.ChildContractID)
	r.BaseSHA = strings.TrimSpace(r.BaseSHA)
	r.WorktreePathRef = strings.TrimSpace(r.WorktreePathRef)
	r.ChangedPaths = compactStrings(r.ChangedPaths)
	r.PatchPath = strings.TrimSpace(r.PatchPath)
	r.PatchDigest = strings.TrimSpace(r.PatchDigest)
	r.VerificationClaimRefs = compactStrings(r.VerificationClaimRefs)
	r.CreatedAt = normalizeChildTime(r.CreatedAt)
}

func (r CandidatePatchArtifact) Validate() error {
	record := r
	record.Normalize()

	var problems validationProblems
	if record.SchemaVersion != CandidatePatchArtifactSchemaVersion {
		problems.Add("schema_version must be " + CandidatePatchArtifactSchemaVersion)
	}
	requireChildField(&problems, record.PatchID, "patch_id")
	requireChildField(&problems, record.InvocationID, "invocation_id")
	requireChildField(&problems, record.ChildContractID, "child_contract_id")
	requireChildField(&problems, record.BaseSHA, "base_sha")
	requireChildField(&problems, record.PatchPath, "patch_path")
	requireChildField(&problems, record.PatchDigest, "patch_digest")
	if len(record.ChangedPaths) == 0 {
		problems.Add("changed_paths are required")
	}
	if record.DiffStat.Files < 0 || record.DiffStat.Insertions < 0 || record.DiffStat.Deletions < 0 {
		problems.Add("diff_stat values cannot be negative")
	}
	if record.CreatedAt.IsZero() {
		problems.Add("created_at is required")
	}
	validateChildSafeFields(&problems, "candidate_patch_artifact", record.PatchID, record.InvocationID, record.ChildContractID, record.BaseSHA, record.WorktreePathRef, record.PatchPath, record.PatchDigest)
	validateChildSafeSlice(&problems, "candidate_patch_artifact.changed_paths", record.ChangedPaths)
	validateChildSafeSlice(&problems, "candidate_patch_artifact.verification_claim_refs", record.VerificationClaimRefs)
	return childRecordError("candidate_patch_artifact", problems)
}

func (r *ChildAcceptanceRecord) Normalize() {
	r.SchemaVersion = firstNonEmptyChildValue(r.SchemaVersion, ChildAcceptanceSchemaVersion)
	r.AcceptanceID = strings.TrimSpace(r.AcceptanceID)
	r.ParentContractID = strings.TrimSpace(r.ParentContractID)
	r.ChildContractID = strings.TrimSpace(r.ChildContractID)
	r.InvocationID = strings.TrimSpace(r.InvocationID)
	r.TerminalID = strings.TrimSpace(r.TerminalID)
	r.RuntimeEnvelopeRef = strings.TrimSpace(r.RuntimeEnvelopeRef)
	r.PackID = strings.TrimSpace(r.PackID)
	r.AcceptedEvidenceRefs = compactStrings(r.AcceptedEvidenceRefs)
	r.AcceptedPatchRefs = compactStrings(r.AcceptedPatchRefs)
	r.RequiredRepairPaths = compactStrings(r.RequiredRepairPaths)
	r.VerificationObligationRefs = compactStrings(r.VerificationObligationRefs)
	r.ExecutionMetadataRef = strings.TrimSpace(r.ExecutionMetadataRef)
	r.ParentPlanAuthorityRef = strings.TrimSpace(r.ParentPlanAuthorityRef)
	r.PathGateRef = strings.TrimSpace(r.PathGateRef)
	r.AlignmentBasis.normalize()
	r.ReasonCode = strings.TrimSpace(r.ReasonCode)
}

func (r ChildAcceptanceRecord) Validate() error {
	record := r
	record.Normalize()

	var problems validationProblems
	if record.SchemaVersion != ChildAcceptanceSchemaVersion {
		problems.Add("schema_version must be " + ChildAcceptanceSchemaVersion)
	}
	requireChildField(&problems, record.AcceptanceID, "acceptance_id")
	requireChildField(&problems, record.ParentContractID, "parent_contract_id")
	requireChildField(&problems, record.ChildContractID, "child_contract_id")
	requireChildField(&problems, record.InvocationID, "invocation_id")
	requireChildField(&problems, record.TerminalID, "terminal_id")
	requireChildField(&problems, record.ReasonCode, "reason_code")
	if !validChildResultKind(record.ResultKind) {
		problems.Add("result_kind is required and must be a known child result kind")
	}
	if !validChildAcceptanceDecision(record.Decision) {
		problems.Add("decision is required and must be a known child acceptance decision")
	}
	if record.PackFreshness != "" && !validFreshness(record.PackFreshness) {
		problems.Add("pack_freshness is invalid")
	}
	if record.PathGateDecision != "" && !validChildPathGateDecision(record.PathGateDecision) {
		problems.Add("path_gate_decision is invalid")
	}
	if record.Route != "" && !validChildExecutionRoute(record.Route) {
		problems.Add("route is invalid")
	}
	if record.Decision == ChildAcceptanceAccepted && record.ReasonCode != "non_authoritative" {
		record.AlignmentBasis.validateAcceptedObjective(&problems)
	}
	if record.Decision == ChildAcceptanceAccepted &&
		len(record.AcceptedEvidenceRefs) == 0 &&
		len(record.AcceptedPatchRefs) == 0 &&
		len(record.VerificationObligationRefs) == 0 {

		problems.Add("accepted child output requires accepted_evidence_refs, accepted_patch_refs, or verification_obligation_refs")
	}
	if record.ResultKind == ChildResultAppliedParentEdit {
		if record.Route == "" || record.ParentPlanAuthorityRef == "" || record.PathGateRef == "" || record.PathGateDecision == "" || len(record.VerificationObligationRefs) == 0 {
			problems.Add("applied_parent_edit acceptance requires route, parent_plan_authority_ref, path_gate_ref, path_gate_decision, and verification_obligation_refs")
		}
	}
	validateChildSafeFields(&problems, "child_acceptance", record.AcceptanceID, record.ParentContractID, record.ChildContractID, record.InvocationID, record.TerminalID, record.RuntimeEnvelopeRef, record.PackID, record.ExecutionMetadataRef, record.ParentPlanAuthorityRef, record.PathGateRef, string(record.Route), record.ReasonCode)
	validateChildSafeSlice(&problems, "child_acceptance.accepted_evidence_refs", record.AcceptedEvidenceRefs)
	validateChildSafeSlice(&problems, "child_acceptance.accepted_patch_refs", record.AcceptedPatchRefs)
	validateChildSafeSlice(&problems, "child_acceptance.required_repair_paths", record.RequiredRepairPaths)
	validateChildSafeSlice(&problems, "child_acceptance.verification_obligation_refs", record.VerificationObligationRefs)
	record.AlignmentBasis.validateSafe(&problems)
	return childRecordError("child_acceptance", problems)
}

func (r *VerificationAcceptanceRecord) Normalize() {
	r.SchemaVersion = firstNonEmptyChildValue(r.SchemaVersion, VerificationAcceptanceSchemaVersion)
	r.VerificationAcceptanceID = strings.TrimSpace(r.VerificationAcceptanceID)
	r.ParentContractID = strings.TrimSpace(r.ParentContractID)
	r.ObligationRefs = compactStrings(r.ObligationRefs)
	r.CommandRef = strings.TrimSpace(r.CommandRef)
	r.CommandFingerprint = strings.TrimSpace(r.CommandFingerprint)
	r.WorkspaceRef = strings.TrimSpace(r.WorkspaceRef)
	r.BaseSHA = strings.TrimSpace(r.BaseSHA)
	r.HeadSHA = strings.TrimSpace(r.HeadSHA)
	r.ResultRef = strings.TrimSpace(r.ResultRef)
	r.ScopeRefs = compactStrings(r.ScopeRefs)
	r.ReasonCode = strings.TrimSpace(r.ReasonCode)
}

func (r VerificationAcceptanceRecord) Validate() error {
	record := r
	record.Normalize()

	var problems validationProblems
	if record.SchemaVersion != VerificationAcceptanceSchemaVersion {
		problems.Add("schema_version must be " + VerificationAcceptanceSchemaVersion)
	}
	requireChildField(&problems, record.VerificationAcceptanceID, "verification_acceptance_id")
	requireChildField(&problems, record.ParentContractID, "parent_contract_id")
	requireChildField(&problems, record.CommandRef, "command_ref")
	requireChildField(&problems, record.CommandFingerprint, "command_fingerprint")
	requireChildField(&problems, record.WorkspaceRef, "workspace_ref")
	requireChildField(&problems, record.BaseSHA, "base_sha")
	requireChildField(&problems, record.HeadSHA, "head_sha")
	requireChildField(&problems, record.ResultRef, "result_ref")
	requireChildField(&problems, record.ReasonCode, "reason_code")
	if len(record.ObligationRefs) == 0 {
		problems.Add("obligation_refs are required")
	}
	if !validVerificationAcceptanceDecision(record.Decision) {
		problems.Add("decision is required and must be a known verification acceptance decision")
	}
	if record.Decision == VerificationAcceptanceAccepted && record.ExitStatus != 0 {
		problems.Add("accepted verification requires exit_status 0")
	}
	validateChildSafeFields(&problems, "verification_acceptance", record.VerificationAcceptanceID, record.ParentContractID, record.CommandRef, record.CommandFingerprint, record.WorkspaceRef, record.BaseSHA, record.HeadSHA, record.ResultRef, record.ReasonCode)
	validateChildSafeSlice(&problems, "verification_acceptance.obligation_refs", record.ObligationRefs)
	validateChildSafeSlice(&problems, "verification_acceptance.scope_refs", record.ScopeRefs)
	return childRecordError("verification_acceptance", problems)
}

func (s *ChildContractSummary) normalize() {
	s.PathRoleDigest = strings.TrimSpace(s.PathRoleDigest)
	s.ToolPolicyDigest = strings.TrimSpace(s.ToolPolicyDigest)
	s.ObligationDigest = strings.TrimSpace(s.ObligationDigest)
}

func (s ChildContractSummary) validate(problems *validationProblems) {
	requireChildField(problems, s.PathRoleDigest, "contract_summary.path_role_digest")
	requireChildField(problems, s.ToolPolicyDigest, "contract_summary.tool_policy_digest")
	requireChildField(problems, s.ObligationDigest, "contract_summary.obligation_digest")
	validateChildSafeFields(problems, "contract_summary", s.PathRoleDigest, s.ToolPolicyDigest, s.ObligationDigest)
}

func (e *ChildRuntimeEnvelope) normalize() {
	e.BackendKind = strings.TrimSpace(e.BackendKind)
	e.ModelAlias = strings.TrimSpace(e.ModelAlias)
	e.ModelRole = strings.TrimSpace(e.ModelRole)
	e.RoleResolution.normalize()
	e.CapacityAdmission.normalize()
	e.ParserContract = strings.TrimSpace(e.ParserContract)
	e.ResponseFormat = strings.TrimSpace(e.ResponseFormat)
	e.ToolChoice = strings.TrimSpace(e.ToolChoice)
	e.ThinkingPolicy = strings.TrimSpace(e.ThinkingPolicy)
	e.ToolSchemaHash = strings.TrimSpace(e.ToolSchemaHash)
}

func (e ChildRuntimeEnvelope) validate(prefix string, problems *validationProblems) {
	requireChildField(problems, e.BackendKind, prefix+".backend_kind")
	requireChildField(problems, e.ModelAlias, prefix+".model_alias")
	requireChildField(problems, e.ModelRole, prefix+".model_role")
	if !validChildTransportMode(e.ToolTransportMode) {
		problems.Add(prefix + ".tool_transport_mode is required and must be native, textual, or none")
	}
	if !validChildTransportMode(e.ParserTransportMode) {
		problems.Add(prefix + ".parser_transport_mode is required and must be native, textual, or none")
	}
	if e.MaxTokens < 0 || e.ContextBudget.MaxContextMessages < 0 || e.ContextBudget.MaxContextChars < 0 || e.RequestTimeoutMS < 0 {
		problems.Add(prefix + " budget and timeout values cannot be negative")
	}
	e.RoleResolution.validate(prefix+".role_resolution", problems)
	e.CapacityAdmission.validate(prefix+".capacity_admission", problems)
	validateChildSafeFields(problems, prefix, e.BackendKind, e.ModelAlias, e.ModelRole, e.ParserContract, e.ResponseFormat, e.ToolChoice, e.ThinkingPolicy, e.ToolSchemaHash)
}

func (r *ChildRoleResolution) normalize() {
	r.RequestedRole = strings.TrimSpace(r.RequestedRole)
	r.ResolvedRole = strings.TrimSpace(r.ResolvedRole)
	r.Alias = strings.TrimSpace(r.Alias)
}

func (r ChildRoleResolution) validate(prefix string, problems *validationProblems) {
	requireChildField(problems, r.RequestedRole, prefix+".requested_role")
	if !validRoleResolutionSource(r.Source) {
		problems.Add(prefix + ".source is invalid")
	}
	if r.OK {
		requireChildField(problems, r.ResolvedRole, prefix+".resolved_role")
		requireChildField(problems, r.Alias, prefix+".alias")
	} else if r.Source != RoleResolutionSourceUnresolved {
		problems.Add(prefix + ".source must be unresolved when ok is false")
	}
	validateChildSafeFields(problems, prefix, r.RequestedRole, r.ResolvedRole, r.Alias, string(r.Source))
}

func (a *ChildCapacityAdmission) normalize() {
	a.Pool = strings.TrimSpace(a.Pool)
	a.ModelAlias = strings.TrimSpace(a.ModelAlias)
	a.ReasonClass = strings.TrimSpace(a.ReasonClass)
}

func (a ChildCapacityAdmission) validate(prefix string, problems *validationProblems) {
	if !validCapacityAdmissionState(a.State) {
		problems.Add(prefix + ".state is required and must be acquired, unavailable, disabled, degraded, or error")
	}
	if !validCapacityAdmissionEnforcement(a.Enforcement) {
		problems.Add(prefix + ".enforcement is required and must be blocking or opportunistic")
	}
	if a.WaitMS < 0 {
		problems.Add(prefix + ".wait_ms cannot be negative")
	}
	if a.Enforcement == CapacityAdmissionBlocking && a.State != CapacityAdmissionAcquired && a.State != CapacityAdmissionDegraded {
		problems.Add(prefix + ".state must be acquired or degraded for blocking dispatch")
	}
	validateChildSafeFields(problems, prefix, string(a.State), a.Pool, a.ModelAlias, a.ReasonClass, string(a.Enforcement))
}

func (m *ChildExecutionMetadata) normalize() {
	m.ParentPlanAuthorityRef = strings.TrimSpace(m.ParentPlanAuthorityRef)
	m.PathGateRef = strings.TrimSpace(m.PathGateRef)
	m.VerificationObligationRefs = compactStrings(m.VerificationObligationRefs)
}

func (m ChildExecutionMetadata) validate(prefix string, problems *validationProblems) {
	if m.Route != "" && !validChildExecutionRoute(m.Route) {
		problems.Add(prefix + ".route is invalid")
	}
	if m.PathGateDecision != "" && !validChildPathGateDecision(m.PathGateDecision) {
		problems.Add(prefix + ".path_gate_decision is invalid")
	}
	validateChildSafeFields(problems, prefix, string(m.Route), m.ParentPlanAuthorityRef, m.PathGateRef, string(m.PathGateDecision))
	validateChildSafeSlice(problems, prefix+".verification_obligation_refs", m.VerificationObligationRefs)
}

func (m ChildExecutionMetadata) validateAppliedParentEdit(prefix string, problems *validationProblems) {
	if m.Route == "" || m.ParentPlanAuthorityRef == "" || m.PathGateRef == "" || m.PathGateDecision == "" || len(m.VerificationObligationRefs) == 0 {
		problems.Add(prefix + " requires route, parent_plan_authority_ref, path_gate_ref, path_gate_decision, and verification_obligation_refs for applied_parent_edit")
	}
}

func (s *ChildParseStatus) normalize() {
	s.SchemaVersion = strings.TrimSpace(s.SchemaVersion)
	s.NormalizedOutputSchema = strings.TrimSpace(s.NormalizedOutputSchema)
	s.ParserFailureClass = strings.TrimSpace(s.ParserFailureClass)
}

func (s ChildParseStatus) validate(prefix string, problems *validationProblems) {
	if s.TransportObserved != "" && !validChildObservedTransport(s.TransportObserved) {
		problems.Add(prefix + ".transport_observed is invalid")
	}
	if s.NormalizerStatus != "" && !validChildNormalizerStatus(s.NormalizerStatus) {
		problems.Add(prefix + ".normalizer_status is invalid")
	}
	if s.ParserFailureClass != "" && !s.RawOutputRedacted {
		problems.Add(prefix + ".raw_output_redacted is required when parser_failure_class is present")
	}
	validateChildSafeFields(problems, prefix, s.SchemaVersion, string(s.TransportObserved), string(s.NormalizerStatus), s.NormalizedOutputSchema, s.ParserFailureClass)
}

func (s ChildParseStatus) validateParseFailure(prefix string, problems *validationProblems) {
	if s.TransportObserved == "" {
		problems.Add(prefix + ".transport_observed is required for parse_failed terminals")
	}
	if s.NormalizerStatus == "" {
		problems.Add(prefix + ".normalizer_status is required for parse_failed terminals")
	}
	if s.ParserFailureClass == "" {
		problems.Add(prefix + ".parser_failure_class is required for parse_failed terminals")
	}
	if !s.RawOutputRedacted {
		problems.Add(prefix + ".raw_output_redacted is required for parse_failed terminals")
	}
}

func (s ChildParseStatus) validateAppliedParentEdit(prefix string, problems *validationProblems) {
	if s.TransportObserved == "" {
		problems.Add(prefix + ".transport_observed is required for applied_parent_edit terminals")
	}
	if s.NormalizerStatus == "" {
		problems.Add(prefix + ".normalizer_status is required for applied_parent_edit terminals")
	}
}

func (b *ChildAlignmentBasis) normalize() {
	b.DelegatedObjectiveRef = strings.TrimSpace(b.DelegatedObjectiveRef)
	b.EffectiveScopeRefs = compactStrings(b.EffectiveScopeRefs)
	b.AcceptedSourceBodyRefs = compactStrings(b.AcceptedSourceBodyRefs)
	b.CandidateOnlyRefs = compactStrings(b.CandidateOnlyRefs)
	b.MismatchReasons = compactStrings(b.MismatchReasons)
}

func (b ChildAlignmentBasis) validateAcceptedObjective(problems *validationProblems) {
	requireChildField(problems, b.DelegatedObjectiveRef, "alignment_basis.delegated_objective_ref")
	if len(b.EffectiveScopeRefs) == 0 {
		problems.Add("alignment_basis.effective_scope_refs are required for objective_aligned acceptance")
	}
	if len(b.AcceptedSourceBodyRefs) == 0 {
		problems.Add("alignment_basis.accepted_source_body_refs are required for objective_aligned acceptance")
	}
	if !validAlignmentEvaluator(b.Evaluator) {
		problems.Add("alignment_basis.evaluator is required for objective_aligned acceptance")
	}
}

func (b ChildAlignmentBasis) validateSafe(problems *validationProblems) {
	if b.Evaluator != "" && !validAlignmentEvaluator(b.Evaluator) {
		problems.Add("alignment_basis.evaluator is invalid")
	}
	validateChildSafeFields(problems, "alignment_basis", b.DelegatedObjectiveRef, string(b.Evaluator))
	validateChildSafeSlice(problems, "alignment_basis.effective_scope_refs", b.EffectiveScopeRefs)
	validateChildSafeSlice(problems, "alignment_basis.accepted_source_body_refs", b.AcceptedSourceBodyRefs)
	validateChildSafeSlice(problems, "alignment_basis.candidate_only_refs", b.CandidateOnlyRefs)
	validateChildSafeSlice(problems, "alignment_basis.mismatch_reasons", b.MismatchReasons)
}

func firstNonEmptyChildValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeChildTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

func requireChildField(problems *validationProblems, value string, field string) {
	if strings.TrimSpace(value) == "" {
		problems.Add(field + " is required")
	}
}

func childRecordError(record string, problems validationProblems) error {
	if !problems.Any() {
		return nil
	}
	return fmt.Errorf("invalid %s record: %s", record, strings.Join(problems.Messages(), "; "))
}

func validateChildSafeFields(problems *validationProblems, field string, values ...string) {
	for _, value := range values {
		if looksLikeRawContractEventContent(value) {
			problems.Add(field + " fields must contain redaction-safe metadata only")
			return
		}
	}
}

func validateChildSafeSlice(problems *validationProblems, field string, values []string) {
	for _, value := range values {
		if looksLikeRawContractEventContent(value) {
			problems.Add(field + " must contain redaction-safe metadata only")
			return
		}
	}
}

func validChildWorkspaceMode(mode ChildWorkspaceMode) bool {
	switch mode {
	case ChildWorkspaceMain, ChildWorkspaceWorkbranch, ChildWorkspaceNoWrite:
		return true
	default:
		return false
	}
}

func validRoleResolutionSource(source RoleResolutionSource) bool {
	switch source {
	case RoleResolutionSourceProxyDefault, RoleResolutionSourceRoleTag, RoleResolutionSourceLoneModel, RoleResolutionSourceExecutorCoderHeuristic, RoleResolutionSourceUnresolved:
		return true
	default:
		return false
	}
}

func validCapacityAdmissionState(state CapacityAdmissionState) bool {
	switch state {
	case CapacityAdmissionAcquired, CapacityAdmissionUnavailable, CapacityAdmissionDisabled, CapacityAdmissionDegraded, CapacityAdmissionError:
		return true
	default:
		return false
	}
}

func validCapacityAdmissionEnforcement(enforcement CapacityAdmissionEnforcement) bool {
	switch enforcement {
	case CapacityAdmissionBlocking, CapacityAdmissionOpportunistic:
		return true
	default:
		return false
	}
}

func validChildTransportMode(mode ChildTransportMode) bool {
	switch mode {
	case ChildTransportNative, ChildTransportTextual, ChildTransportNone:
		return true
	default:
		return false
	}
}

func validChildObservedTransport(mode ChildObservedTransport) bool {
	switch mode {
	case ChildObservedTransportNative, ChildObservedTransportTextual, ChildObservedTransportDeterministic:
		return true
	default:
		return false
	}
}

func validChildNormalizerStatus(status ChildNormalizerStatus) bool {
	switch status {
	case ChildNormalizerNotRequired, ChildNormalizerNormalized, ChildNormalizerFailed:
		return true
	default:
		return false
	}
}

func validChildResultKind(kind ChildResultKind) bool {
	switch kind {
	case ChildResultEvidencePacket, ChildResultPlanArtifact, ChildResultCandidatePatch, ChildResultFindings, ChildResultSourceSatisfied, ChildResultTypedBlocker, ChildResultAppliedParentEdit:
		return true
	default:
		return false
	}
}

func validChildTerminalStatus(status ChildTerminalStatus) bool {
	switch status {
	case ChildTerminalCompleted, ChildTerminalBlocked, ChildTerminalFailed, ChildTerminalBudgetExhausted, ChildTerminalParseFailed:
		return true
	default:
		return false
	}
}

func validChildObjectiveAlignment(alignment ChildObjectiveAlignment) bool {
	switch alignment {
	case ChildObjectiveAccepted, ChildObjectiveWeak, ChildObjectiveOffObjective, ChildObjectiveNotApplicable:
		return true
	default:
		return false
	}
}

func validChildExecutionRoute(route ChildExecutionRoute) bool {
	switch route {
	case ChildRouteChildModel, ChildRouteDeterministicDirect, ChildRouteParentExecutor, ChildRouteNotApplicable:
		return true
	default:
		return false
	}
}

func validChildPathGateDecision(decision ChildPathGateDecision) bool {
	switch decision {
	case ChildPathGateAllow, ChildPathGateDeny, ChildPathGateNudge, ChildPathGateReject, ChildPathGateNotApplicable:
		return true
	default:
		return false
	}
}

func validChildAcceptanceDecision(decision ChildAcceptanceDecision) bool {
	switch decision {
	case ChildAcceptanceAccepted, ChildAcceptanceRejected, ChildAcceptanceRepairRequired, ChildAcceptanceBlocked, ChildAcceptanceRecordedOnly:
		return true
	default:
		return false
	}
}

func validAlignmentEvaluator(evaluator AlignmentEvaluator) bool {
	switch evaluator {
	case AlignmentEvaluatorHostContract, AlignmentEvaluatorReviewerFinding, AlignmentEvaluatorDeterministicRule:
		return true
	default:
		return false
	}
}

func validVerificationAcceptanceDecision(decision VerificationAcceptanceDecision) bool {
	switch decision {
	case VerificationAcceptanceAccepted, VerificationAcceptanceRejected, VerificationAcceptanceRepairRequired, VerificationAcceptanceBlocked:
		return true
	default:
		return false
	}
}
