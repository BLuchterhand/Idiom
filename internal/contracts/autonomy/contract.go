// Package autonomy defines the host-owned contract data shared by lanes,
// phases, and sidecar calls.
package autonomy

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const SchemaVersion = "idiom.autonomy.contract.v1"

type InvocationKind string

const (
	InvocationLane        InvocationKind = "lane"
	InvocationPhase       InvocationKind = "phase"
	InvocationSidecarCall InvocationKind = "sidecar_call"
)

type PathRoleKind string

const (
	PathRoleRequiredEdit      PathRoleKind = "required_edit"
	PathRoleAllowedEdit       PathRoleKind = "allowed_edit"
	PathRoleEvidenceReadonly  PathRoleKind = "evidence_readonly"
	PathRoleGeneratedArtifact PathRoleKind = "generated_artifact"
	PathRoleProtected         PathRoleKind = "protected"
	PathRoleForbidden         PathRoleKind = "forbidden"
	PathRoleVerificationOut   PathRoleKind = "verification_output"
	PathRoleExternalEffect    PathRoleKind = "external_side_effect"
	PathRolePublicWorld       PathRoleKind = "public_world"
)

type Capability string

const (
	CapabilitySemanticDiscovery Capability = "semantic_discovery"
	CapabilityLiteralSearch     Capability = "literal_search"
	CapabilityBoundedRead       Capability = "bounded_read"
	CapabilityUnboundedRead     Capability = "unbounded_read"
	CapabilitySourceEdit        Capability = "source_edit"
	CapabilityApplyPatch        Capability = "apply_patch"
	CapabilityShellVerification Capability = "shell_verification"
	CapabilityMutatingShell     Capability = "mutating_shell"
	CapabilityPlanSubmission    Capability = "plan_submission"
	CapabilityExternalRead      Capability = "external_read"
	CapabilityExternalWrite     Capability = "external_write"
	CapabilityDelegation        Capability = "delegation"
)

type SideEffectClass string

const (
	SideEffectReadOnly    SideEffectClass = "read_only"
	SideEffectAdditive    SideEffectClass = "additive"
	SideEffectDestructive SideEffectClass = "destructive"
	SideEffectOpenWorld   SideEffectClass = "open_world"
	SideEffectUnknown     SideEffectClass = "unknown"
)

type ToolPolicyDecision string

const (
	PolicyAllow   ToolPolicyDecision = "allow"
	PolicyAsk     ToolPolicyDecision = "ask"
	PolicyDeny    ToolPolicyDecision = "deny"
	PolicyNudge   ToolPolicyDecision = "nudge"
	PolicyObserve ToolPolicyDecision = "observe_only"
	PolicyReject  ToolPolicyDecision = "reject"
	PolicyStop    ToolPolicyDecision = "stop"
)

type ObligationKind string

const (
	ObligationEvidence    ObligationKind = "evidence"
	ObligationWrite       ObligationKind = "write"
	ObligationVerify      ObligationKind = "verify"
	ObligationFinalAnswer ObligationKind = "final_answer"
	ObligationApproval    ObligationKind = "approval"
	ObligationBudget      ObligationKind = "budget"
	ObligationLoop        ObligationKind = "loop"
)

type GateKind string

const (
	GateWrite        GateKind = "write_gate"
	GateVerification GateKind = "verification_gate"
	GateCompletion   GateKind = "completion_gate"
	GateLoop         GateKind = "loop_gate"
	GateBudget       GateKind = "budget_gate"
	GateReadOnly     GateKind = "read_only_gate"
)

type ContractPackID string

const (
	ContractPackCompletionReadiness   ContractPackID = "completion/readiness"
	ContractPackWriteRequiredPaths    ContractPackID = "write/required_paths"
	ContractPackVerificationPostWrite ContractPackID = "verification/post_write"
	ContractPackLoopRepeatedCall      ContractPackID = "loop/repeated_call"
	ContractPackBudgetRunPolicy       ContractPackID = "budget/run_policy"
	ContractPackPlanExecuteProtocol   ContractPackID = "plan_execute/protocol"
	ContractPackSelfModifyGuardFiles  ContractPackID = "self_modify/guard_files"
)

type EnforcementMode string

const (
	EnforcementModeEnforce     EnforcementMode = "enforce"
	EnforcementModeObserveOnly EnforcementMode = "observe_only"
)

type ObservedFactKind string

const (
	ObservedFactCount         ObservedFactKind = "count"
	ObservedFactStatus        ObservedFactKind = "status"
	ObservedFactReasonCode    ObservedFactKind = "reason_code"
	ObservedFactPathRef       ObservedFactKind = "path_ref"
	ObservedFactObligationRef ObservedFactKind = "obligation_ref"
	ObservedFactToolName      ObservedFactKind = "tool_name"
	ObservedFactProtocolHash  ObservedFactKind = "protocol_hash"
)

type ContractObservedFact string

type ObligationStatus string

const (
	ObligationOpen     ObligationStatus = "open"
	ObligationResolved ObligationStatus = "resolved"
	ObligationWaived   ObligationStatus = "waived"
)

type ObligationOwner string

const (
	OwnerHost    ObligationOwner = "host"
	OwnerLane    ObligationOwner = "lane"
	OwnerPhase   ObligationOwner = "phase"
	OwnerPlanner ObligationOwner = "planner"
	OwnerSidecar ObligationOwner = "sidecar"
	OwnerUser    ObligationOwner = "user"
)

type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

type ClientClassification string

const (
	ClientFinance        ClientClassification = "finance"
	ClientHealthcare     ClientClassification = "healthcare"
	ClientPublicSector   ClientClassification = "public-sector"
	ClientEnterpriseSaaS ClientClassification = "enterprise-saas"
	ClientToolingVendor  ClientClassification = "tooling-vendor"
	ClientLocalModel     ClientClassification = "local-model"
	ClientOpenSource     ClientClassification = "open-source"
)

type ProductSignal string

const (
	ProductICP                 ProductSignal = "icp"
	ProductActivation          ProductSignal = "activation"
	ProductRetainedUse         ProductSignal = "retained_use"
	ProductIntegrationProof    ProductSignal = "integration_proof"
	ProductSecurityPrivacy     ProductSignal = "security_privacy"
	ProductCompetitiveProof    ProductSignal = "competitive_proof"
	ProductClientDemoProof     ProductSignal = "client_demo_proof"
	ProductPackagingSupport    ProductSignal = "packaging_support"
	ProductLocalEvidenceExport ProductSignal = "local_product_evidence_export"
)

func DefaultClientClassifications() []ClientClassification {
	return []ClientClassification{
		ClientFinance,
		ClientHealthcare,
		ClientPublicSector,
		ClientEnterpriseSaaS,
		ClientToolingVendor,
		ClientLocalModel,
		ClientOpenSource,
	}
}

func DefaultProductSignals() []ProductSignal {
	return []ProductSignal{
		ProductICP,
		ProductActivation,
		ProductRetainedUse,
		ProductIntegrationProof,
		ProductSecurityPrivacy,
		ProductCompetitiveProof,
		ProductClientDemoProof,
		ProductPackagingSupport,
		ProductLocalEvidenceExport,
	}
}

func DefaultContractMetadata(tags ...string) ContractMetadata {
	return ContractMetadata{
		ClientClassifications: DefaultClientClassifications(),
		ProductSignals:        DefaultProductSignals(),
		Tags:                  compactStrings(tags),
	}
}

type AutonomyContract struct {
	SchemaVersion   string                  `json:"schema_version"`
	ContractID      string                  `json:"contract_id"`
	ParentID        string                  `json:"parent_id,omitempty"`
	Scope           ContractScope           `json:"scope"`
	Authority       []AuthoritySource       `json:"authority_sources,omitempty"`
	PathRoles       []PathRole              `json:"path_roles,omitempty"`
	Capabilities    CapabilityPolicy        `json:"capabilities,omitempty"`
	ToolPolicy      []ToolPolicyRule        `json:"tool_policy,omitempty"`
	Obligations     []Obligation            `json:"obligations,omitempty"`
	Budget          BudgetState             `json:"budget,omitempty"`
	Verification    VerificationRequirement `json:"verification,omitempty"`
	Completion      CompletionRequirement   `json:"completion,omitempty"`
	Readiness       ReadinessRequirement    `json:"readiness,omitempty"`
	PolicyDecisions []PolicyDecision        `json:"policy_decisions,omitempty"`
	Evidence        []EvidenceRef           `json:"evidence_refs,omitempty"`
	Metadata        ContractMetadata        `json:"metadata,omitempty"`
}

type ContractScope struct {
	InvocationKind    InvocationKind   `json:"invocation_kind"`
	Lane              string           `json:"lane,omitempty"`
	LaneRunID         string           `json:"lane_run_id,omitempty"`
	Phase             string           `json:"phase,omitempty"`
	PhaseRunID        string           `json:"phase_run_id,omitempty"`
	TaskFamily        string           `json:"task_family,omitempty"`
	RuntimeContract   string           `json:"runtime_contract,omitempty"`
	PermissionProfile string           `json:"permission_profile,omitempty"`
	Sidecar           *SidecarContext  `json:"sidecar,omitempty"`
	ToolCall          *ToolCallContext `json:"tool_call,omitempty"`
}

type SidecarContext struct {
	Transport string `json:"transport,omitempty"`
	Caller    string `json:"caller,omitempty"`
	Profile   string `json:"profile,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Tool      string `json:"tool,omitempty"`
}

type ToolCallContext struct {
	ToolCallID      string          `json:"tool_call_id,omitempty"`
	ToolName        string          `json:"tool_name,omitempty"`
	Capability      Capability      `json:"capability,omitempty"`
	SideEffect      SideEffectClass `json:"side_effect,omitempty"`
	ArgumentsDigest string          `json:"arguments_digest,omitempty"`
}

type AuthoritySource struct {
	Kind        string    `json:"kind,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
	Freshness   Freshness `json:"freshness,omitempty"`
	Description string    `json:"description,omitempty"`
}

type PathRole struct {
	Role         PathRoleKind `json:"role"`
	PathPattern  string       `json:"path_pattern,omitempty"`
	SymbolHandle string       `json:"symbol_handle,omitempty"`
	PacketID     string       `json:"packet_id,omitempty"`
	Freshness    Freshness    `json:"freshness,omitempty"`
	Reason       string       `json:"reason,omitempty"`
}

type CapabilityPolicy struct {
	Allowed          []Capability      `json:"allowed,omitempty"`
	Forbidden        []Capability      `json:"forbidden,omitempty"`
	HiddenTools      []string          `json:"hidden_tools,omitempty"`
	AllowedEffects   []SideEffectClass `json:"allowed_effects,omitempty"`
	ForbiddenEffects []SideEffectClass `json:"forbidden_effects,omitempty"`
}

type ToolPolicyRule struct {
	ToolPattern string             `json:"tool_pattern,omitempty"`
	Decision    ToolPolicyDecision `json:"decision,omitempty"`
	Capability  Capability         `json:"capability,omitempty"`
	SideEffect  SideEffectClass    `json:"side_effect,omitempty"`
	Source      string             `json:"source,omitempty"`
	ReasonCode  string             `json:"reason_code,omitempty"`
}

type Obligation struct {
	ObligationID           string           `json:"obligation_id"`
	Kind                   ObligationKind   `json:"kind"`
	Status                 ObligationStatus `json:"status"`
	Owner                  ObligationOwner  `json:"owner,omitempty"`
	TargetRef              string           `json:"target_ref,omitempty"`
	RequiredBy             string           `json:"required_by,omitempty"`
	SourceEvent            string           `json:"source_event,omitempty"`
	EvidenceRef            string           `json:"evidence_ref,omitempty"`
	Command                string           `json:"command,omitempty"`
	WaiverReason           string           `json:"waiver_reason,omitempty"`
	FinalAnswerRequirement string           `json:"final_answer_requirement,omitempty"`
}

type BudgetState struct {
	MaxToolSteps       int    `json:"max_tool_steps,omitempty"`
	UsedToolSteps      int    `json:"used_tool_steps,omitempty"`
	MaxModelCalls      int    `json:"max_model_calls,omitempty"`
	UsedModelCalls     int    `json:"used_model_calls,omitempty"`
	MaxContextMessages int    `json:"max_context_messages,omitempty"`
	MaxContextChars    int    `json:"max_context_chars,omitempty"`
	Exhausted          bool   `json:"exhausted,omitempty"`
	ExhaustionReason   string `json:"exhaustion_reason,omitempty"`
}

type VerificationRequirement struct {
	Required               bool     `json:"required,omitempty"`
	Contract               string   `json:"contract,omitempty"`
	RequiredAfterWrite     bool     `json:"required_after_write,omitempty"`
	SourceAwareRequired    bool     `json:"source_aware_required,omitempty"`
	AcceptedCommands       []string `json:"accepted_commands,omitempty"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	SkippedRequiresBlocker bool     `json:"skipped_requires_blocker,omitempty"`
}

type CompletionRequirement struct {
	Required               bool     `json:"required,omitempty"`
	Criteria               []string `json:"criteria,omitempty"`
	BlockOnOpenObligations bool     `json:"block_on_open_obligations,omitempty"`
	AllowedNoopReasons     []string `json:"allowed_noop_reasons,omitempty"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
}

type ReadinessRequirement struct {
	ReadyToAnswer       bool     `json:"ready_to_answer,omitempty"`
	ReadyToWrite        bool     `json:"ready_to_write,omitempty"`
	ReadyToVerify       bool     `json:"ready_to_verify,omitempty"`
	ClosedClaims        []string `json:"closed_claims,omitempty"`
	OpenObligationRefs  []string `json:"open_obligation_refs,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs,omitempty"`
	FurtherReadsRequire string   `json:"further_reads_require,omitempty"`
}

type PolicyDecision struct {
	DecisionID       string             `json:"decision_id,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
	ToolName         string             `json:"tool_name,omitempty"`
	Decision         ToolPolicyDecision `json:"decision"`
	InvariantID      string             `json:"invariant_id,omitempty"`
	ReasonCode       string             `json:"reason_code,omitempty"`
	Capability       Capability         `json:"capability,omitempty"`
	SideEffect       SideEffectClass    `json:"side_effect,omitempty"`
	PathRefs         []string           `json:"path_refs,omitempty"`
	ObligationRefs   []string           `json:"obligation_refs,omitempty"`
	EvidenceRefs     []string           `json:"evidence_refs,omitempty"`
	ApprovalRequired bool               `json:"approval_required,omitempty"`
	Source           string             `json:"source,omitempty"`
}

type EvidenceRef struct {
	EvidenceID string    `json:"evidence_id"`
	Kind       string    `json:"kind,omitempty"`
	Path       string    `json:"path,omitempty"`
	Handle     string    `json:"handle,omitempty"`
	Freshness  Freshness `json:"freshness,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
	Source     string    `json:"source,omitempty"`
	Summary    string    `json:"summary,omitempty"`
}

type ContractMetadata struct {
	ClientClassifications []ClientClassification `json:"client_classifications,omitempty"`
	ProductSignals        []ProductSignal        `json:"product_signals,omitempty"`
	Tags                  []string               `json:"tags,omitempty"`
}

type ContractInput struct {
	ParentID        string
	Scope           ContractScope
	Authority       []AuthoritySource
	PathRoles       []PathRole
	Capabilities    CapabilityPolicy
	ToolPolicy      []ToolPolicyRule
	Obligations     []Obligation
	Budget          BudgetState
	Verification    VerificationRequirement
	Completion      CompletionRequirement
	Readiness       ReadinessRequirement
	PolicyDecisions []PolicyDecision
	Evidence        []EvidenceRef
	Metadata        ContractMetadata
}

func NewContract(input ContractInput) (AutonomyContract, error) {
	contract := AutonomyContract{
		SchemaVersion:   SchemaVersion,
		ParentID:        strings.TrimSpace(input.ParentID),
		Scope:           copyContractScope(input.Scope),
		Authority:       append([]AuthoritySource(nil), input.Authority...),
		PathRoles:       append([]PathRole(nil), input.PathRoles...),
		Capabilities:    copyCapabilityPolicy(input.Capabilities),
		ToolPolicy:      append([]ToolPolicyRule(nil), input.ToolPolicy...),
		Obligations:     append([]Obligation(nil), input.Obligations...),
		Budget:          input.Budget,
		Verification:    copyVerification(input.Verification),
		Completion:      copyCompletion(input.Completion),
		Readiness:       copyReadiness(input.Readiness),
		PolicyDecisions: append([]PolicyDecision(nil), input.PolicyDecisions...),
		Evidence:        append([]EvidenceRef(nil), input.Evidence...),
		Metadata:        copyMetadata(input.Metadata),
	}
	contract.Normalize()

	if err := contract.Validate(); err != nil {
		return AutonomyContract{}, err
	}

	contract.ContractID = contract.stableID()

	return contract, nil
}

func (c *AutonomyContract) Normalize() {
	if c.SchemaVersion == "" {
		c.SchemaVersion = SchemaVersion
	}

	c.ContractID = strings.TrimSpace(c.ContractID)
	c.ParentID = strings.TrimSpace(c.ParentID)
	c.Scope.normalize()

	for i := range c.Authority {
		c.Authority[i].normalize()
	}

	for i := range c.PathRoles {
		c.PathRoles[i].normalize()
	}

	for i := range c.ToolPolicy {
		c.ToolPolicy[i].normalize()
	}

	for i := range c.Obligations {
		c.Obligations[i].normalize()
	}

	for i := range c.PolicyDecisions {
		c.PolicyDecisions[i].normalize()
	}

	for i := range c.Evidence {
		c.Evidence[i].normalize()
	}

	c.Capabilities.normalize()
	c.Verification.normalize()
	c.Completion.normalize()
	c.Readiness.normalize()
	c.Metadata.normalize()
	slices.SortFunc(c.Authority, func(a, b AuthoritySource) int { return cmp.Compare(a.sortKey(), b.sortKey()) })
	slices.SortFunc(c.PathRoles, func(a, b PathRole) int { return cmp.Compare(a.sortKey(), b.sortKey()) })
	slices.SortFunc(c.ToolPolicy, func(a, b ToolPolicyRule) int { return cmp.Compare(a.sortKey(), b.sortKey()) })
	slices.SortFunc(c.Obligations, func(a, b Obligation) int { return cmp.Compare(a.sortKey(), b.sortKey()) })
	slices.SortFunc(c.PolicyDecisions, func(a, b PolicyDecision) int { return cmp.Compare(a.sortKey(), b.sortKey()) })
	slices.SortFunc(c.Evidence, func(a, b EvidenceRef) int { return cmp.Compare(a.EvidenceID, b.EvidenceID) })
}

func (c AutonomyContract) Validate() error {
	var problems []string

	if c.SchemaVersion != SchemaVersion {
		problems = append(problems, "schema_version must be "+SchemaVersion)
	}

	if !validInvocationKind(c.Scope.InvocationKind) {
		problems = append(problems, "scope.invocation_kind is required and must be lane, phase, or sidecar_call")
	}

	switch c.Scope.InvocationKind {
	case InvocationLane:
		if c.Scope.Lane == "" {
			problems = append(problems, "scope.lane is required for lane contracts")
		}
	case InvocationPhase:
		if c.Scope.Lane == "" || c.Scope.Phase == "" {
			problems = append(problems, "scope.lane and scope.phase are required for phase contracts")
		}
	case InvocationSidecarCall:
		if c.Scope.Sidecar == nil {
			problems = append(problems, "scope.sidecar is required for sidecar_call contracts")
		} else if c.Scope.Sidecar.Tool == "" {
			problems = append(problems, "scope.sidecar.tool is required for sidecar_call contracts")
		}
	}

	if c.Scope.ToolCall != nil && c.Scope.ToolCall.SideEffect != "" && !validSideEffect(c.Scope.ToolCall.SideEffect) {
		problems = append(problems, "scope.tool_call.side_effect is invalid")
	}

	for i, source := range c.Authority {
		if source.Freshness != "" && !validFreshness(source.Freshness) {
			problems = append(problems, fmt.Sprintf("authority_sources[%d].freshness is invalid", i))
		}
	}

	for i, role := range c.PathRoles {
		if !validPathRole(role.Role) {
			problems = append(problems, fmt.Sprintf("path_roles[%d].role is invalid", i))
		}

		if role.PathPattern == "" && role.SymbolHandle == "" && role.PacketID == "" {
			problems = append(problems, fmt.Sprintf("path_roles[%d] must identify a path_pattern, symbol_handle, or packet_id", i))
		}

		if role.Freshness != "" && !validFreshness(role.Freshness) {
			problems = append(problems, fmt.Sprintf("path_roles[%d].freshness is invalid", i))
		}
	}

	validateCapabilityPolicy(c.Capabilities, &problems)

	for i, rule := range c.ToolPolicy {
		if rule.Decision != "" && !validDecision(rule.Decision) {
			problems = append(problems, fmt.Sprintf("tool_policy[%d].decision is invalid", i))
		}

		if rule.SideEffect != "" && !validSideEffect(rule.SideEffect) {
			problems = append(problems, fmt.Sprintf("tool_policy[%d].side_effect is invalid", i))
		}
	}

	for i, obligation := range c.Obligations {
		if obligation.ObligationID == "" {
			problems = append(problems, fmt.Sprintf("obligations[%d].obligation_id is required", i))
		}

		if !validObligationKind(obligation.Kind) {
			problems = append(problems, fmt.Sprintf("obligations[%d].kind is invalid", i))
		}

		if !validObligationStatus(obligation.Status) {
			problems = append(problems, fmt.Sprintf("obligations[%d].status is invalid", i))
		}

		if obligation.Owner != "" && !validObligationOwner(obligation.Owner) {
			problems = append(problems, fmt.Sprintf("obligations[%d].owner is invalid", i))
		}
	}

	validateBudget(c.Budget, &problems)

	for i, effect := range c.Capabilities.AllowedEffects {
		if !validSideEffect(effect) {
			problems = append(problems, fmt.Sprintf("capabilities.allowed_effects[%d] is invalid", i))
		}
	}

	for i, effect := range c.Capabilities.ForbiddenEffects {
		if !validSideEffect(effect) {
			problems = append(problems, fmt.Sprintf("capabilities.forbidden_effects[%d] is invalid", i))
		}
	}

	for i, decision := range c.PolicyDecisions {
		if !validDecision(decision.Decision) {
			problems = append(problems, fmt.Sprintf("policy_decisions[%d].decision is invalid", i))
		}

		if decision.SideEffect != "" && !validSideEffect(decision.SideEffect) {
			problems = append(problems, fmt.Sprintf("policy_decisions[%d].side_effect is invalid", i))
		}
	}

	for i, evidence := range c.Evidence {
		if evidence.EvidenceID == "" {
			problems = append(problems, fmt.Sprintf("evidence_refs[%d].evidence_id is required", i))
		}

		if evidence.Freshness != "" && !validFreshness(evidence.Freshness) {
			problems = append(problems, fmt.Sprintf("evidence_refs[%d].freshness is invalid", i))
		}
	}

	for i, client := range c.Metadata.ClientClassifications {
		if !validClientClassification(client) {
			problems = append(problems, fmt.Sprintf("metadata.client_classifications[%d] is invalid", i))
		}
	}

	for i, signal := range c.Metadata.ProductSignals {
		if !validProductSignal(signal) {
			problems = append(problems, fmt.Sprintf("metadata.product_signals[%d] is invalid", i))
		}
	}

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}

	return nil
}

type ContractSummary struct {
	ContractID             string         `json:"contract_id"`
	ParentID               string         `json:"parent_id,omitempty"`
	SchemaVersion          string         `json:"schema_version"`
	Lane                   string         `json:"lane,omitempty"`
	TaskFamily             string         `json:"task_family,omitempty"`
	Phase                  string         `json:"phase,omitempty"`
	PermissionProfile      string         `json:"permission_profile,omitempty"`
	AuthoritySources       []string       `json:"authority_sources,omitempty"`
	TargetRoleCounts       map[string]int `json:"target_role_counts,omitempty"`
	CapabilitiesAllowed    []Capability   `json:"capabilities_allowed,omitempty"`
	CapabilitiesForbidden  []Capability   `json:"capabilities_forbidden,omitempty"`
	ObligationCounts       map[string]int `json:"obligation_counts,omitempty"`
	ProtectedPathCount     int            `json:"protected_path_count,omitempty"`
	ToolPolicyCount        int            `json:"tool_policy_count,omitempty"`
	PolicyDecisionCount    int            `json:"policy_decision_count,omitempty"`
	EvidenceReferenceCount int            `json:"evidence_reference_count,omitempty"`
}

func (c AutonomyContract) Summary() ContractSummary {
	roleCounts := make(map[string]int)
	protectedCount := 0

	for _, role := range c.PathRoles {
		roleCounts[string(role.Role)]++
		if role.Role == PathRoleProtected {
			protectedCount++
		}
	}

	obligationCounts := make(map[string]int)

	for _, obligation := range c.Obligations {
		key := string(obligation.Status) + ":" + string(obligation.Kind)
		obligationCounts[key]++
	}

	authority := make([]string, 0, len(c.Authority))
	for _, source := range c.Authority {
		authority = append(authority, source.Ref)
	}

	return ContractSummary{
		ContractID:             c.ContractID,
		ParentID:               c.ParentID,
		SchemaVersion:          c.SchemaVersion,
		Lane:                   c.Scope.Lane,
		TaskFamily:             c.Scope.TaskFamily,
		Phase:                  c.Scope.Phase,
		PermissionProfile:      c.Scope.PermissionProfile,
		AuthoritySources:       compactStrings(authority),
		TargetRoleCounts:       emptyMapAsNil(roleCounts),
		CapabilitiesAllowed:    append([]Capability(nil), c.Capabilities.Allowed...),
		CapabilitiesForbidden:  append([]Capability(nil), c.Capabilities.Forbidden...),
		ObligationCounts:       emptyMapAsNil(obligationCounts),
		ProtectedPathCount:     protectedCount,
		ToolPolicyCount:        len(c.ToolPolicy),
		PolicyDecisionCount:    len(c.PolicyDecisions),
		EvidenceReferenceCount: len(c.Evidence),
	}
}

type ValidationError struct {
	Problems []string
}

func (e ValidationError) Error() string {
	return "invalid autonomy contract: " + strings.Join(e.Problems, "; ")
}

func (c AutonomyContract) stableID() string {
	copy := c

	copy.ContractID = ""

	encoded, err := json.Marshal(copy)
	if err != nil {
		panic(err)
	}

	sum := sha256.Sum256(encoded)

	return "contract_" + hex.EncodeToString(sum[:])[:16]
}

func (s *ContractScope) normalize() {
	s.Lane = strings.TrimSpace(s.Lane)
	s.LaneRunID = strings.TrimSpace(s.LaneRunID)
	s.Phase = strings.TrimSpace(s.Phase)
	s.PhaseRunID = strings.TrimSpace(s.PhaseRunID)
	s.TaskFamily = strings.TrimSpace(s.TaskFamily)
	s.RuntimeContract = strings.TrimSpace(s.RuntimeContract)
	s.PermissionProfile = strings.TrimSpace(s.PermissionProfile)

	if s.Sidecar != nil {
		s.Sidecar.normalize()
	}

	if s.ToolCall != nil {
		s.ToolCall.normalize()
	}
}

func (s *SidecarContext) normalize() {
	s.Transport = strings.TrimSpace(s.Transport)
	s.Caller = strings.TrimSpace(s.Caller)
	s.Profile = strings.TrimSpace(s.Profile)
	s.RequestID = strings.TrimSpace(s.RequestID)
	s.Tool = strings.TrimSpace(s.Tool)
}

func (t *ToolCallContext) normalize() {
	t.ToolCallID = strings.TrimSpace(t.ToolCallID)
	t.ToolName = strings.TrimSpace(t.ToolName)
	t.ArgumentsDigest = strings.TrimSpace(t.ArgumentsDigest)
}

func (a *AuthoritySource) normalize() {
	a.Kind = strings.TrimSpace(a.Kind)
	a.Ref = strings.TrimSpace(a.Ref)
	a.EvidenceRef = strings.TrimSpace(a.EvidenceRef)
	a.Description = strings.TrimSpace(a.Description)
}

func (a AuthoritySource) sortKey() string {
	return strings.Join([]string{a.Kind, a.Ref, a.EvidenceRef}, "\x00")
}

func (p *PathRole) normalize() {
	p.PathPattern = strings.TrimSpace(p.PathPattern)
	p.SymbolHandle = strings.TrimSpace(p.SymbolHandle)
	p.PacketID = strings.TrimSpace(p.PacketID)
	p.Reason = strings.TrimSpace(p.Reason)
}

func (p PathRole) sortKey() string {
	return strings.Join([]string{string(p.Role), p.PathPattern, p.SymbolHandle, p.PacketID}, "\x00")
}

func (p *CapabilityPolicy) normalize() {
	p.Allowed = uniqueCapabilities(p.Allowed)
	p.Forbidden = uniqueCapabilities(p.Forbidden)
	p.HiddenTools = compactStrings(p.HiddenTools)
	p.AllowedEffects = uniqueSideEffects(p.AllowedEffects)
	p.ForbiddenEffects = uniqueSideEffects(p.ForbiddenEffects)
}

func (t *ToolPolicyRule) normalize() {
	t.ToolPattern = strings.TrimSpace(t.ToolPattern)
	t.Source = strings.TrimSpace(t.Source)
	t.ReasonCode = strings.TrimSpace(t.ReasonCode)
}

func (t ToolPolicyRule) sortKey() string {
	return strings.Join([]string{t.ToolPattern, string(t.Decision), string(t.Capability), string(t.SideEffect)}, "\x00")
}

func (o *Obligation) normalize() {
	o.ObligationID = strings.TrimSpace(o.ObligationID)
	o.TargetRef = strings.TrimSpace(o.TargetRef)
	o.RequiredBy = strings.TrimSpace(o.RequiredBy)
	o.SourceEvent = strings.TrimSpace(o.SourceEvent)
	o.EvidenceRef = strings.TrimSpace(o.EvidenceRef)
	o.Command = strings.TrimSpace(o.Command)
	o.WaiverReason = strings.TrimSpace(o.WaiverReason)
	o.FinalAnswerRequirement = strings.TrimSpace(o.FinalAnswerRequirement)
}

func (o Obligation) sortKey() string {
	return strings.Join([]string{o.ObligationID, string(o.Kind), string(o.Status)}, "\x00")
}

func (v *VerificationRequirement) normalize() {
	v.Contract = strings.TrimSpace(v.Contract)
	v.AcceptedCommands = compactStrings(v.AcceptedCommands)
	v.EvidenceRefs = compactStrings(v.EvidenceRefs)
}

func (c *CompletionRequirement) normalize() {
	c.Criteria = compactStrings(c.Criteria)
	c.AllowedNoopReasons = compactStrings(c.AllowedNoopReasons)
	c.EvidenceRefs = compactStrings(c.EvidenceRefs)
}

func (r *ReadinessRequirement) normalize() {
	r.ClosedClaims = compactStrings(r.ClosedClaims)
	r.OpenObligationRefs = compactStrings(r.OpenObligationRefs)
	r.EvidenceRefs = compactStrings(r.EvidenceRefs)
	r.FurtherReadsRequire = strings.TrimSpace(r.FurtherReadsRequire)
}

func (p *PolicyDecision) normalize() {
	p.DecisionID = strings.TrimSpace(p.DecisionID)
	p.ToolCallID = strings.TrimSpace(p.ToolCallID)
	p.ToolName = strings.TrimSpace(p.ToolName)
	p.InvariantID = strings.TrimSpace(p.InvariantID)
	p.ReasonCode = strings.TrimSpace(p.ReasonCode)
	p.PathRefs = compactStrings(p.PathRefs)
	p.ObligationRefs = compactStrings(p.ObligationRefs)
	p.EvidenceRefs = compactStrings(p.EvidenceRefs)
	p.Source = strings.TrimSpace(p.Source)
}

func (p PolicyDecision) sortKey() string {
	return strings.Join([]string{p.DecisionID, p.ToolCallID, string(p.Decision), p.InvariantID}, "\x00")
}

func (e *EvidenceRef) normalize() {
	e.EvidenceID = strings.TrimSpace(e.EvidenceID)
	e.Kind = strings.TrimSpace(e.Kind)
	e.Path = strings.TrimSpace(e.Path)
	e.Handle = strings.TrimSpace(e.Handle)
	e.Confidence = strings.TrimSpace(e.Confidence)
	e.Source = strings.TrimSpace(e.Source)
	e.Summary = strings.TrimSpace(e.Summary)
}

func (m *ContractMetadata) normalize() {
	m.ClientClassifications = uniqueClientClassifications(m.ClientClassifications)
	m.ProductSignals = uniqueProductSignals(m.ProductSignals)
	m.Tags = compactStrings(m.Tags)
}

func copyContractScope(in ContractScope) ContractScope {
	out := in
	if in.Sidecar != nil {
		sidecar := *in.Sidecar

		out.Sidecar = &sidecar
	}

	if in.ToolCall != nil {
		toolCall := *in.ToolCall

		out.ToolCall = &toolCall
	}

	return out
}

func copyCapabilityPolicy(in CapabilityPolicy) CapabilityPolicy {
	return CapabilityPolicy{
		Allowed:          append([]Capability(nil), in.Allowed...),
		Forbidden:        append([]Capability(nil), in.Forbidden...),
		HiddenTools:      append([]string(nil), in.HiddenTools...),
		AllowedEffects:   append([]SideEffectClass(nil), in.AllowedEffects...),
		ForbiddenEffects: append([]SideEffectClass(nil), in.ForbiddenEffects...),
	}
}

func copyVerification(in VerificationRequirement) VerificationRequirement {
	in.AcceptedCommands = append([]string(nil), in.AcceptedCommands...)
	in.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)

	return in
}

func copyCompletion(in CompletionRequirement) CompletionRequirement {
	in.Criteria = append([]string(nil), in.Criteria...)
	in.AllowedNoopReasons = append([]string(nil), in.AllowedNoopReasons...)
	in.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)

	return in
}

func copyReadiness(in ReadinessRequirement) ReadinessRequirement {
	in.ClosedClaims = append([]string(nil), in.ClosedClaims...)
	in.OpenObligationRefs = append([]string(nil), in.OpenObligationRefs...)
	in.EvidenceRefs = append([]string(nil), in.EvidenceRefs...)

	return in
}

func copyMetadata(in ContractMetadata) ContractMetadata {
	return ContractMetadata{
		ClientClassifications: append([]ClientClassification(nil), in.ClientClassifications...),
		ProductSignals:        append([]ProductSignal(nil), in.ProductSignals...),
		Tags:                  append([]string(nil), in.Tags...),
	}
}

func validateCapabilityPolicy(policy CapabilityPolicy, problems *[]string) {
	for i, capability := range append(append([]Capability(nil), policy.Allowed...), policy.Forbidden...) {
		if strings.TrimSpace(string(capability)) == "" {
			*problems = append(*problems, fmt.Sprintf("capabilities entry %d is empty", i))
		}
	}
}

func validateBudget(b BudgetState, problems *[]string) {
	values := map[string]int{
		"budget.max_tool_steps":       b.MaxToolSteps,
		"budget.used_tool_steps":      b.UsedToolSteps,
		"budget.max_model_calls":      b.MaxModelCalls,
		"budget.used_model_calls":     b.UsedModelCalls,
		"budget.max_context_messages": b.MaxContextMessages,
		"budget.max_context_chars":    b.MaxContextChars,
	}
	for field, value := range values {
		if value < 0 {
			*problems = append(*problems, field+" cannot be negative")
		}
	}
}

func validInvocationKind(kind InvocationKind) bool {
	switch kind {
	case InvocationLane, InvocationPhase, InvocationSidecarCall:
		return true
	default:
		return false
	}
}

func validPathRole(role PathRoleKind) bool {
	switch role {
	case PathRoleRequiredEdit, PathRoleAllowedEdit, PathRoleEvidenceReadonly, PathRoleGeneratedArtifact, PathRoleProtected, PathRoleForbidden, PathRoleVerificationOut, PathRoleExternalEffect, PathRolePublicWorld:
		return true
	default:
		return false
	}
}

func validSideEffect(effect SideEffectClass) bool {
	switch effect {
	case SideEffectReadOnly, SideEffectAdditive, SideEffectDestructive, SideEffectOpenWorld, SideEffectUnknown:
		return true
	default:
		return false
	}
}

func validDecision(decision ToolPolicyDecision) bool {
	switch decision {
	case PolicyAllow, PolicyAsk, PolicyDeny, PolicyNudge, PolicyObserve, PolicyReject, PolicyStop:
		return true
	default:
		return false
	}
}

func validObligationKind(kind ObligationKind) bool {
	switch kind {
	case ObligationEvidence, ObligationWrite, ObligationVerify, ObligationFinalAnswer, ObligationApproval, ObligationBudget, ObligationLoop:
		return true
	default:
		return false
	}
}

func validGateKind(kind GateKind) bool {
	switch kind {
	case GateWrite, GateVerification, GateCompletion, GateLoop, GateBudget, GateReadOnly:
		return true
	default:
		return false
	}
}

func validContractPackID(packID ContractPackID) bool {
	switch packID {
	case ContractPackCompletionReadiness, ContractPackWriteRequiredPaths, ContractPackVerificationPostWrite,
		ContractPackLoopRepeatedCall, ContractPackBudgetRunPolicy, ContractPackPlanExecuteProtocol, ContractPackSelfModifyGuardFiles:
		return true
	default:
		return strings.HasPrefix(string(packID), "pack-") && validContractLabel(string(packID), 96)
	}
}

func validEnforcementMode(mode EnforcementMode) bool {
	switch mode {
	case EnforcementModeEnforce, EnforcementModeObserveOnly:
		return true
	default:
		return false
	}
}

func validObservedFactKind(kind ObservedFactKind) bool {
	switch kind {
	case ObservedFactCount, ObservedFactStatus, ObservedFactReasonCode, ObservedFactPathRef,
		ObservedFactObligationRef, ObservedFactToolName, ObservedFactProtocolHash:
		return true
	default:
		return false
	}
}

func validContractLabel(value string, maxLen int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLen {
		return false
	}

	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			continue
		}

		if r >= 'A' && r <= 'Z' {
			continue
		}

		if r >= '0' && r <= '9' {
			continue
		}

		switch r {
		case '_', '-', '/', '.', ':', '=', '@':
			continue
		default:
			return false
		}
	}

	return true
}

func validObligationStatus(status ObligationStatus) bool {
	switch status {
	case ObligationOpen, ObligationResolved, ObligationWaived:
		return true
	default:
		return false
	}
}

func validObligationOwner(owner ObligationOwner) bool {
	switch owner {
	case OwnerHost, OwnerLane, OwnerPhase, OwnerPlanner, OwnerSidecar, OwnerUser:
		return true
	default:
		return false
	}
}

func validFreshness(freshness Freshness) bool {
	switch freshness {
	case FreshnessFresh, FreshnessStale, FreshnessUnknown:
		return true
	default:
		return false
	}
}

func validClientClassification(client ClientClassification) bool {
	switch client {
	case ClientFinance, ClientHealthcare, ClientPublicSector, ClientEnterpriseSaaS, ClientToolingVendor, ClientLocalModel, ClientOpenSource:
		return true
	default:
		return false
	}
}

func validProductSignal(signal ProductSignal) bool {
	switch signal {
	case ProductICP, ProductActivation, ProductRetainedUse, ProductIntegrationProof, ProductSecurityPrivacy, ProductCompetitiveProof, ProductClientDemoProof, ProductPackagingSupport, ProductLocalEvidenceExport:
		return true
	default:
		return false
	}
}

func uniqueCapabilities(values []Capability) []Capability {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[Capability]struct{}, len(values))
	out := make([]Capability, 0, len(values))

	for _, value := range values {
		if strings.TrimSpace(string(value)) == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func uniqueSideEffects(values []SideEffectClass) []SideEffectClass {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[SideEffectClass]struct{}, len(values))
	out := make([]SideEffectClass, 0, len(values))

	for _, value := range values {
		if strings.TrimSpace(string(value)) == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func uniqueClientClassifications(values []ClientClassification) []ClientClassification {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[ClientClassification]struct{}, len(values))
	out := make([]ClientClassification, 0, len(values))

	for _, value := range values {
		if strings.TrimSpace(string(value)) == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func uniqueProductSignals(values []ProductSignal) []ProductSignal {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[ProductSignal]struct{}, len(values))
	out := make([]ProductSignal, 0, len(values))

	for _, value := range values {
		if strings.TrimSpace(string(value)) == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func emptyMapAsNil(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}

	return values
}
