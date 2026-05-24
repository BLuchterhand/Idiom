// Package contractcompile turns plain lane and runtime inputs into passive
// autonomy contracts and protocol projections.
package contractcompile

import "idiom/internal/autonomy"

const (
	CompilerSchemaVersion = "idiom.contractcompile.result.v1"

	InvariantActiveAuthority = "contract.active_authority"

	InvocationPlanExecutePlanner = "plan_execute_planner"
	DefaultPlanExecuteLane       = "plan-execute"
	DefaultPlannerPhase          = "planner"
)

type CompileResult struct {
	SchemaVersion string                    `json:"schema_version"`
	Contract      autonomy.AutonomyContract `json:"contract"`
	Runtime       RuntimeEnvelope           `json:"runtime_envelope"`
	Protocol      ModelProtocolProjection   `json:"model_protocol"`
}

type PlanExecutePlannerInput struct {
	ParentID             string
	Lane                 string
	LaneRunID            string
	Phase                string
	PhaseRunID           string
	TaskFamily           string
	RuntimeContract      string
	PermissionProfile    string
	TargetRefs           []string
	RequiredEditPaths    []string
	AllowedEditPaths     []string
	EvidencePaths        []string
	ChangedPaths         []string
	UserBoundaries       []string
	AllowedTools         []string
	ForbiddenTools       []string
	RequiredEvidence     []string
	RequiredTools        []string
	ReadinessGate        []string
	VerificationContract string
	VerificationPlan     []string
	StopConditions       []string
	FinalCritic          []string
	TargetPacket         []string
	Constraints          []string
	RequiredNext         []string
	InvalidActions       []string
	Readiness            ReadinessInput
	Budget               BudgetInput
	Runtime              RuntimeEnvelopeInput
	Protocol             ProtocolInput
	MetadataTags         []string
}

type ReadinessInput struct {
	ReadyToAnswer       bool
	ReadyToWrite        bool
	ReadyToVerify       bool
	ClosedClaims        []string
	OpenObligations     []string
	EvidenceSources     []string
	FurtherReadsRequire string
}

type BudgetInput struct {
	MaxToolSteps       int
	MaxModelCalls      int
	MaxContextMessages int
	MaxContextChars    int
}

type RuntimeEnvelopeInput struct {
	BackendKind         string
	ModelAlias          string
	ModelRole           string
	ParserTransportMode string
	ToolTransportMode   string
	ParserContract      string
	NativeToolPolicy    string
	SupportsNativeTools bool
	ForceNativeTools    bool
	ToolChoice          string
	ResponseFormat      string
	ThinkingPolicy      string
	ThinkingBudget      int
	EffectiveMaxTokens  int
	ContextBudget       ContextBudget
	QueueState          QueueState
	ReadinessProfile    string
	EnforcementMode     string
	// Deprecated compatibility inputs. Prefer DegradationDecisions so each
	// degraded invariant carries its own authorized fallback decision.
	DegradedInvariants   []string
	AuthorizedFallback   []string
	DegradationDecisions []DegradationDecision
}

type RuntimeEnvelope struct {
	RuntimeContractID    string                `json:"runtime_contract_id"`
	BackendKind          string                `json:"backend_kind,omitempty"`
	ModelAlias           string                `json:"model_alias,omitempty"`
	ModelRole            string                `json:"model_role,omitempty"`
	ParserTransportMode  string                `json:"parser_transport_mode,omitempty"`
	ToolTransportMode    string                `json:"tool_transport_mode,omitempty"`
	ParserContract       string                `json:"parser_contract,omitempty"`
	NativeToolPolicy     string                `json:"native_tool_policy,omitempty"`
	SupportsNativeTools  bool                  `json:"supports_native_tools,omitempty"`
	ForceNativeTools     bool                  `json:"force_native_tools,omitempty"`
	ToolChoice           string                `json:"tool_choice,omitempty"`
	ResponseFormat       string                `json:"response_format,omitempty"`
	ThinkingPolicy       string                `json:"thinking_policy,omitempty"`
	ThinkingBudget       int                   `json:"thinking_budget,omitempty"`
	EffectiveMaxTokens   int                   `json:"effective_max_tokens,omitempty"`
	ContextBudget        ContextBudget         `json:"context_budget,omitempty"`
	QueueState           QueueState            `json:"queue_state,omitempty"`
	ReadinessProfile     string                `json:"readiness_profile,omitempty"`
	EnforcementMode      string                `json:"enforcement_mode,omitempty"`
	DegradationDecisions []DegradationDecision `json:"degradation_decisions,omitempty"`
	ActiveInvariantID    string                `json:"active_invariant_id,omitempty"`
}

type DegradationDecision struct {
	InvariantID         string   `json:"invariant_id,omitempty"`
	ReasonCode          string   `json:"reason_code,omitempty"`
	Decision            string   `json:"decision,omitempty"`
	AuthorizedFallbacks []string `json:"authorized_fallbacks,omitempty"`
}

type ContextBudget struct {
	MaxContextMessages int `json:"max_context_messages,omitempty"`
	MaxContextChars    int `json:"max_context_chars,omitempty"`
}

type QueueState struct {
	Pool    string `json:"pool,omitempty"`
	LeaseID string `json:"lease_id,omitempty"`
	State   string `json:"state,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type ProtocolInput struct {
	PhaseID                string
	ParserMode             string
	ExpectedVisibleOutput  string
	VisibleTools           []string
	CallableTools          []string
	CallableToolsSpecified bool
	CallableActionClasses  []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
	StructuredSchemaNames  []string
	MalformedOutputPolicy  string
	StaleAnchorPolicy      string
	SourceBodyObligations  []string
	RecoveryBudget         int
}

type ModelProtocolProjection struct {
	ContractID              string   `json:"contract_id"`
	ContractVersion         string   `json:"contract_version"`
	ProtocolHash            string   `json:"protocol_hash"`
	PhaseID                 string   `json:"phase_id,omitempty"`
	ParserMode              string   `json:"parser_mode,omitempty"`
	ToolTransportMode       string   `json:"tool_transport_mode,omitempty"`
	ParserTransportMode     string   `json:"parser_transport_mode,omitempty"`
	ParserContract          string   `json:"parser_contract,omitempty"`
	NativeToolPolicy        string   `json:"native_tool_policy,omitempty"`
	SupportsNativeTools     bool     `json:"supports_native_tools,omitempty"`
	ForceNativeTools        bool     `json:"force_native_tools,omitempty"`
	ToolChoice              string   `json:"tool_choice,omitempty"`
	ResponseFormat          string   `json:"response_format,omitempty"`
	ExpectedVisibleOutput   string   `json:"expected_visible_output,omitempty"`
	VisibleTools            []string `json:"visible_tools,omitempty"`
	CallableTools           []string `json:"callable_tools,omitempty"`
	CallableActionClasses   []string `json:"callable_action_classes,omitempty"`
	InvalidActionClasses    []string `json:"invalid_action_classes,omitempty"`
	TerminalResponseShapes  []string `json:"terminal_response_shapes,omitempty"`
	StructuredSchemaNames   []string `json:"structured_schema_names,omitempty"`
	MalformedOutputPolicy   string   `json:"malformed_output_policy,omitempty"`
	StaleAnchorPolicy       string   `json:"stale_anchor_policy,omitempty"`
	SourceBodyObligations   []string `json:"source_body_obligations,omitempty"`
	RequiredEditCoverage    []string `json:"required_edit_coverage,omitempty"`
	PathRoleSummary         []string `json:"path_role_summary,omitempty"`
	VerificationObligations []string `json:"verification_obligations,omitempty"`
	SafeAlternatives        []string `json:"safe_alternatives,omitempty"`
	RequiredTools           []string `json:"required_tools,omitempty"`
	ReadinessGate           []string `json:"readiness_gate,omitempty"`
	Constraints             []string `json:"constraints,omitempty"`
	InvalidActions          []string `json:"invalid_actions,omitempty"`
	RecoveryBudget          int      `json:"recovery_budget,omitempty"`
	ActiveInvariantID       string   `json:"active_invariant_id,omitempty"`
}
