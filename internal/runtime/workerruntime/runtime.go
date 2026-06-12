package workerruntime

import (
	"idiom/internal/capabilities/workspace"
)

type ToolResultSummary struct {
	Tool        string `json:"tool,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
	OutputChars int    `json:"output_chars,omitempty"`
}

type ToolManifestEntry = workspace.ToolManifestEntry

type BudgetBrief struct {
	MaxToolSteps       int `json:"max_tool_steps,omitempty"`
	MaxModelCalls      int `json:"max_model_calls,omitempty"`
	MaxContextMessages int `json:"max_context_messages,omitempty"`
	MaxContextChars    int `json:"max_context_chars,omitempty"`
}

type WorkerRuntimeBrief struct {
	Lane                 string                `json:"lane,omitempty"`
	TaskFamily           string                `json:"task_family,omitempty"`
	Contract             string                `json:"contract,omitempty"`
	Objective            string                `json:"objective,omitempty"`
	RequestedOutcome     string                `json:"requested_outcome,omitempty"`
	WriteIntent          string                `json:"write_intent,omitempty"`
	ReadOnlyConstraint   bool                  `json:"read_only_constraint,omitempty"`
	EvidenceIntent       string                `json:"evidence_intent,omitempty"`
	PermissionProfile    string                `json:"permission_profile,omitempty"`
	TargetRefs           []string              `json:"target_refs,omitempty"`
	RequiredEditPaths    []string              `json:"required_edit_paths,omitempty"`
	AllowedEditPaths     []string              `json:"allowed_edit_paths,omitempty"`
	RequiredEditSnippets []RequiredEditSnippet `json:"required_edit_snippets,omitempty"`
	EvidencePaths        []string              `json:"evidence_paths,omitempty"`
	ChangedPaths         []string              `json:"changed_paths,omitempty"`
	UserBoundaries       []string              `json:"user_boundaries,omitempty"`
	AllowedTools         []string              `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string              `json:"forbidden_tools,omitempty"`
	ToolManifest         []ToolManifestEntry   `json:"tool_manifest,omitempty"`
	EvidenceCapabilities []string              `json:"available_evidence_capabilities,omitempty"`
	RequiredEvidence     []string              `json:"required_evidence,omitempty"`
	RequiredTools        []string              `json:"required_tools,omitempty"`
	ReadinessGate        []string              `json:"readiness_gate,omitempty"`
	SearchPolicy         []string              `json:"search_policy,omitempty"`
	DiscoveryExamples    []string              `json:"discovery_examples,omitempty"`
	VerificationContract string                `json:"verification_contract,omitempty"`
	VerificationPlan     []string              `json:"verification_plan,omitempty"`
	StopConditions       []string              `json:"stop_conditions,omitempty"`
	FinalCritic          []string              `json:"final_critic,omitempty"`
	TargetPacket         []string              `json:"target_packet,omitempty"`
	Constraints          []string              `json:"constraints,omitempty"`
	RequiredNext         []string              `json:"required_next,omitempty"`
	InvalidActions       []string              `json:"invalid_actions,omitempty"`
	Readiness            *ReadinessState       `json:"readiness,omitempty"`
	Budget               BudgetBrief           `json:"budget,omitempty"`
}

type RequiredEditSnippet struct {
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
}

type ReadinessState struct {
	ReadyToAnswer       bool     `json:"ready_to_answer"`
	ReadyToWrite        bool     `json:"ready_to_write"`
	ReadyToVerify       bool     `json:"ready_to_verify"`
	ClosedClaims        []string `json:"closed_claims,omitempty"`
	OpenObligations     []string `json:"open_obligations,omitempty"`
	EvidenceSources     []string `json:"evidence_sources,omitempty"`
	FurtherReadsRequire string   `json:"further_reads_require,omitempty"`
}

type ActionGuidanceInput struct {
	Reason                     string
	Request                    workspace.ToolRequest
	Result                     workspace.ToolResult
	Constraints                []string
	DeclarationMovesForbidden  bool
	ApprovedTargetScopePresent bool
	WriteCapable               bool
	EvidenceIntent             string
}

type ActionGuidance struct {
	Constraints []string `json:"constraints,omitempty"`
	InvalidNext []string `json:"invalid_next,omitempty"`
	ValidNext   []string `json:"valid_next,omitempty"`
}

type RunState struct {
	Lane                 string              `json:"lane,omitempty"`
	TaskFamily           string              `json:"task_family,omitempty"`
	Contract             string              `json:"contract,omitempty"`
	Phase                string              `json:"phase,omitempty"`
	Objective            string              `json:"objective,omitempty"`
	RequestedOutcome     string              `json:"requested_outcome,omitempty"`
	WriteIntent          string              `json:"write_intent,omitempty"`
	ReadOnlyConstraint   bool                `json:"read_only_constraint,omitempty"`
	EvidenceIntent       string              `json:"evidence_intent,omitempty"`
	Step                 int                 `json:"step,omitempty"`
	Permission           string              `json:"permission_profile,omitempty"`
	TargetRefs           []string            `json:"target_refs,omitempty"`
	RequiredEditPaths    []string            `json:"required_edit_paths,omitempty"`
	AllowedEditPaths     []string            `json:"allowed_edit_paths,omitempty"`
	EvidencePaths        []string            `json:"evidence_paths,omitempty"`
	ChangedPaths         []string            `json:"changed_paths,omitempty"`
	Constraints          []string            `json:"constraints,omitempty"`
	AllowedTools         []string            `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string            `json:"forbidden_tools,omitempty"`
	ToolManifest         []ToolManifestEntry `json:"tool_manifest,omitempty"`
	EvidenceCapabilities []string            `json:"available_evidence_capabilities,omitempty"`
	RequiredEvidence     []string            `json:"required_evidence,omitempty"`
	RequiredTools        []string            `json:"required_tools,omitempty"`
	ReadinessGate        []string            `json:"readiness_gate,omitempty"`
	EvidenceContract     []string            `json:"evidence_contract,omitempty"`
	SearchPolicy         []string            `json:"search_policy,omitempty"`
	DiscoveryExamples    []string            `json:"discovery_examples,omitempty"`
	VerificationContract string              `json:"verification_contract,omitempty"`
	VerificationPlan     []string            `json:"verification_plan,omitempty"`
	StopConditions       []string            `json:"stop_conditions,omitempty"`
	FinalCritic          []string            `json:"final_critic,omitempty"`
	TargetPacket         []string            `json:"target_packet,omitempty"`
	UserBoundaries       []string            `json:"user_boundaries,omitempty"`
	Budget               BudgetBrief         `json:"budget,omitempty"`
	LastTool             string              `json:"last_tool,omitempty"`
	LastResult           ToolResultSummary   `json:"last_result,omitempty"`
	FailureClass         string              `json:"failure_class,omitempty"`
	NextActions          []string            `json:"next_actions,omitempty"`
	InvalidActions       []string            `json:"invalid_actions,omitempty"`
	Readiness            *ReadinessState     `json:"readiness,omitempty"`
}

type StepInstruction struct {
	CurrentState RunState `json:"current_state"`
	Directive    string   `json:"directive"`
}

type RecoveryInput struct {
	Lane                      string
	Step                      int
	Reason                    string
	Request                   workspace.ToolRequest
	Result                    workspace.ToolResult
	EvidencePaths             []string
	Constraints               []string
	DeclarationMovesForbidden bool
	EvidenceIntent            string
	WriteCapable              bool
}

type RecoveryPlan struct {
	FailureClass       string   `json:"failure_class,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker"`
	DoNotRepeat        []string `json:"do_not_repeat,omitempty"`
	RequiredNext       []string `json:"required_next,omitempty"`
	InvalidNext        []string `json:"invalid_next,omitempty"`
	ValidNext          []string `json:"valid_next,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
}
