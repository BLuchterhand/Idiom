package autonomy

import (
	"fmt"
	"slices"
	"strings"
)

type ToolTransport string

const (
	ToolTransportUnknown       ToolTransport = "unknown"
	ToolTransportNativeCall    ToolTransport = "native_tool_call"
	ToolTransportTextualCall   ToolTransport = "textual_tool_call"
	ToolTransportSidecarCall   ToolTransport = "sidecar_call"
	ToolTransportShellCommand  ToolTransport = "shell_command"
	ToolTransportVisibleAnswer ToolTransport = "visible_answer"
)

type ParserFailureClass string

const (
	ParserFailureNone                 ParserFailureClass = "none"
	ParserFailureMalformedJSON        ParserFailureClass = "malformed_json"
	ParserFailureSchemaMismatch       ParserFailureClass = "schema_mismatch"
	ParserFailureMissingToolName      ParserFailureClass = "missing_tool_name"
	ParserFailureUnsupportedTransport ParserFailureClass = "unsupported_transport"
	ParserFailureAmbiguousToolCall    ParserFailureClass = "ambiguous_tool_call"
)

type RepairSource string

const (
	RepairSourceNone                  RepairSource = "none"
	RepairSourceHostNormalizer        RepairSource = "host_normalizer"
	RepairSourceParserRepair          RepairSource = "parser_repair"
	RepairSourceRecoveryPrompt        RepairSource = "recovery_prompt"
	RepairSourceDeterministicFallback RepairSource = "deterministic_fallback"
	RepairSourceModelSelfRepair       RepairSource = "model_self_repair"
)

type ActionClass string

const (
	ActionClassUnknown          ActionClass = "unknown"
	ActionClassRead             ActionClass = "read"
	ActionClassSearch           ActionClass = "search"
	ActionClassSubmitPlan       ActionClass = "submit_plan"
	ActionClassSourceSatisfied  ActionClass = "source_satisfied"
	ActionClassBlocker          ActionClass = "blocker"
	ActionClassReviewPass       ActionClass = "review_pass"
	ActionClassPerEditWrite     ActionClass = "per_edit_write"
	ActionClassDeliverableWrite ActionClass = "deliverable_write"
	ActionClassVerify           ActionClass = "verify"
	ActionClassFinalAnswer      ActionClass = "final_answer"
	ActionClassProseFinal       ActionClass = "prose_final"
	ActionClassGenericProbe     ActionClass = "generic_probe"
	ActionClassDelegate         ActionClass = "delegate"
)

func ActionClassToken(value ActionClass) string {
	if strings.TrimSpace(string(value)) == "" {
		return ""
	}
	value = CanonicalActionClass(value)
	if value == "" {
		return ""
	}
	return string(value)
}

func ActionClassTokens(values ...ActionClass) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		token := ActionClassToken(value)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func IsToolActionClass(value ActionClass) bool {
	switch CanonicalActionClass(value) {
	case ActionClassRead,
		ActionClassSearch,
		ActionClassSubmitPlan,
		ActionClassPerEditWrite,
		ActionClassDeliverableWrite,
		ActionClassVerify,
		ActionClassGenericProbe,
		ActionClassDelegate:
		return true
	default:
		return false
	}
}

// ToolIntent is the canonical, passive description of what a model or sidecar
// tried to do before policy enforcement. It records transport and repair facts
// separately from the action class so parser recovery never masquerades as user
// intent.
type ToolIntent struct {
	ToolName        string             `json:"tool_name,omitempty"`
	Capability      Capability         `json:"capability,omitempty"`
	SideEffect      SideEffectClass    `json:"side_effect,omitempty"`
	ActionClass     ActionClass        `json:"action_class,omitempty"`
	Transport       ToolTransport      `json:"transport,omitempty"`
	ParserFailure   ParserFailureClass `json:"parser_failure,omitempty"`
	RepairSource    RepairSource       `json:"repair_source,omitempty"`
	Repaired        bool               `json:"repaired,omitempty"`
	TargetRefs      []string           `json:"target_refs,omitempty"`
	PathRoles       []PathRoleMatch    `json:"path_roles,omitempty"`
	ArgumentsDigest string             `json:"arguments_digest,omitempty"`
	Source          string             `json:"source,omitempty"`
}

// NormalizedAction is the stable audit key for repeat detection and turn-phase
// policy checks. It intentionally contains only canonical vocabulary and caller
// supplied refs; it does not inspect tool arguments itself.
type NormalizedAction struct {
	ActionClass ActionClass     `json:"action_class"`
	ActionKey   string          `json:"action_key,omitempty"`
	ToolName    string          `json:"tool_name,omitempty"`
	Capability  Capability      `json:"capability,omitempty"`
	SideEffect  SideEffectClass `json:"side_effect,omitempty"`
	TargetRef   string          `json:"target_ref,omitempty"`
	PathRoles   []PathRoleMatch `json:"path_roles,omitempty"`
	Source      string          `json:"source,omitempty"`
}

// ActionDecision is the normalized policy result for a classified action. It
// uses the existing ToolPolicyDecision vocabulary and keeps reason/invariant
// fields factual, so callers can render recovery without embedding strategy.
type ActionDecision struct {
	Decision         ToolPolicyDecision `json:"decision"`
	ActionClass      ActionClass        `json:"action_class,omitempty"`
	Capability       Capability         `json:"capability,omitempty"`
	SideEffect       SideEffectClass    `json:"side_effect,omitempty"`
	InvariantID      string             `json:"invariant_id,omitempty"`
	ReasonCode       string             `json:"reason_code,omitempty"`
	ApprovalRequired bool               `json:"approval_required,omitempty"`
	Source           string             `json:"source,omitempty"`
}

func CanonicalizeToolIntent(in ToolIntent) ToolIntent {
	out := in
	out.ToolName = canonicalToken(in.ToolName)
	out.Capability = CanonicalCapability(in.Capability)
	out.SideEffect = CanonicalSideEffect(in.SideEffect)
	out.ActionClass = CanonicalActionClass(in.ActionClass)
	out.Transport = CanonicalToolTransport(in.Transport)
	out.ParserFailure = CanonicalParserFailureClass(in.ParserFailure)
	out.RepairSource = CanonicalRepairSource(in.RepairSource)
	out.TargetRefs = canonicalStringSet(in.TargetRefs)
	out.PathRoles = canonicalPathRoleMatches(in.PathRoles)
	out.ArgumentsDigest = strings.TrimSpace(in.ArgumentsDigest)
	out.Source = canonicalToken(in.Source)
	if out.RepairSource != "" && out.RepairSource != RepairSourceNone {
		out.Repaired = true
	}
	if out.ParserFailure == "" {
		out.ParserFailure = ParserFailureNone
	}
	if out.RepairSource == "" {
		out.RepairSource = RepairSourceNone
	}
	if out.Transport == "" {
		out.Transport = ToolTransportUnknown
	}
	if out.ActionClass == "" {
		out.ActionClass = ActionClassUnknown
	}
	return out
}

func (i ToolIntent) Validate() error {
	i = CanonicalizeToolIntent(i)
	if i.Transport != "" && !ValidToolTransport(i.Transport) {
		return fmt.Errorf("invalid tool transport %q", i.Transport)
	}
	if i.ParserFailure != "" && !ValidParserFailureClass(i.ParserFailure) {
		return fmt.Errorf("invalid parser failure class %q", i.ParserFailure)
	}
	if i.RepairSource != "" && !ValidRepairSource(i.RepairSource) {
		return fmt.Errorf("invalid repair source %q", i.RepairSource)
	}
	if i.ActionClass != "" && !ValidActionClass(i.ActionClass) {
		return fmt.Errorf("invalid action class %q", i.ActionClass)
	}
	if i.Capability != "" && !ValidCapability(i.Capability) {
		return fmt.Errorf("invalid capability %q", i.Capability)
	}
	if i.SideEffect != "" && !validSideEffect(i.SideEffect) {
		return fmt.Errorf("invalid side effect %q", i.SideEffect)
	}
	if i.Repaired && i.RepairSource == RepairSourceNone {
		return fmt.Errorf("repaired intent requires repair source")
	}
	if i.ParserFailure != ParserFailureNone && i.Transport == ToolTransportUnknown {
		return fmt.Errorf("parser failure requires known transport")
	}
	return nil
}

func CanonicalizeNormalizedAction(in NormalizedAction) NormalizedAction {
	out := in
	out.ActionClass = CanonicalActionClass(in.ActionClass)
	out.ActionKey = canonicalActionKey(in.ActionKey)
	out.ToolName = canonicalToken(in.ToolName)
	out.Capability = CanonicalCapability(in.Capability)
	out.SideEffect = CanonicalSideEffect(in.SideEffect)
	out.TargetRef = strings.TrimSpace(in.TargetRef)
	out.PathRoles = canonicalPathRoleMatches(in.PathRoles)
	out.Source = canonicalToken(in.Source)
	if out.ActionClass == "" {
		out.ActionClass = ActionClassUnknown
	}
	if out.ActionKey == "" {
		out.ActionKey = buildActionKey(out.ActionClass, out.ToolName, out.Capability, out.TargetRef)
	}
	return out
}

func (a NormalizedAction) Validate() error {
	a = CanonicalizeNormalizedAction(a)
	if !ValidActionClass(a.ActionClass) {
		return fmt.Errorf("invalid action class %q", a.ActionClass)
	}
	if a.Capability != "" && !ValidCapability(a.Capability) {
		return fmt.Errorf("invalid capability %q", a.Capability)
	}
	if a.SideEffect != "" && !validSideEffect(a.SideEffect) {
		return fmt.Errorf("invalid side effect %q", a.SideEffect)
	}
	if strings.TrimSpace(a.ActionKey) == "" {
		return fmt.Errorf("normalized action requires action key")
	}
	return nil
}

func CanonicalizeActionDecision(in ActionDecision) ActionDecision {
	out := in
	out.Decision = ToolPolicyDecision(canonicalToken(string(in.Decision)))
	out.ActionClass = CanonicalActionClass(in.ActionClass)
	out.Capability = CanonicalCapability(in.Capability)
	out.SideEffect = CanonicalSideEffect(in.SideEffect)
	out.InvariantID = canonicalDottedToken(in.InvariantID)
	out.ReasonCode = canonicalDottedToken(in.ReasonCode)
	out.Source = canonicalToken(in.Source)
	return out
}

func (d ActionDecision) Validate() error {
	d = CanonicalizeActionDecision(d)
	if !validDecision(d.Decision) {
		return fmt.Errorf("invalid policy decision %q", d.Decision)
	}
	if d.ActionClass != "" && !ValidActionClass(d.ActionClass) {
		return fmt.Errorf("invalid action class %q", d.ActionClass)
	}
	if d.Capability != "" && !ValidCapability(d.Capability) {
		return fmt.Errorf("invalid capability %q", d.Capability)
	}
	if d.SideEffect != "" && !validSideEffect(d.SideEffect) {
		return fmt.Errorf("invalid side effect %q", d.SideEffect)
	}
	if d.ApprovalRequired && d.Decision != PolicyAsk {
		return fmt.Errorf("approval required decisions must use %q", PolicyAsk)
	}
	return nil
}

func ActionFromIntent(intent ToolIntent) NormalizedAction {
	target := firstTargetRef(intent.TargetRefs)
	intent = CanonicalizeToolIntent(intent)
	return CanonicalizeNormalizedAction(NormalizedAction{
		ActionClass: intent.ActionClass,
		ToolName:    intent.ToolName,
		Capability:  intent.Capability,
		SideEffect:  intent.SideEffect,
		TargetRef:   target,
		PathRoles:   intent.PathRoles,
		Source:      intent.Source,
	})
}

func CanonicalToolTransport(value ToolTransport) ToolTransport {
	switch canonicalToken(string(value)) {
	case "", "unknown":
		return ToolTransportUnknown
	case "native", "native_call", "native_tool_call", "tool_call":
		return ToolTransportNativeCall
	case "text", "textual", "textual_call", "textual_tool_call", "raw_text":
		return ToolTransportTextualCall
	case "sidecar", "sidecar_call":
		return ToolTransportSidecarCall
	case "shell", "shell_command", "command":
		return ToolTransportShellCommand
	case "answer", "visible_answer", "final":
		return ToolTransportVisibleAnswer
	default:
		return ToolTransport(canonicalToken(string(value)))
	}
}

func ValidToolTransport(value ToolTransport) bool {
	switch value {
	case ToolTransportUnknown, ToolTransportNativeCall, ToolTransportTextualCall, ToolTransportSidecarCall, ToolTransportShellCommand, ToolTransportVisibleAnswer:
		return true
	default:
		return false
	}
}

func CanonicalParserFailureClass(value ParserFailureClass) ParserFailureClass {
	switch canonicalToken(string(value)) {
	case "", "none", "ok":
		return ParserFailureNone
	case "malformed", "malformed_json", "json":
		return ParserFailureMalformedJSON
	case "schema", "schema_mismatch", "invalid_schema", "submit_plan_edits_string":
		return ParserFailureSchemaMismatch
	case "missing_tool", "missing_tool_name":
		return ParserFailureMissingToolName
	case "unsupported", "unsupported_transport":
		return ParserFailureUnsupportedTransport
	case "ambiguous", "ambiguous_tool_call":
		return ParserFailureAmbiguousToolCall
	default:
		return ParserFailureClass(canonicalToken(string(value)))
	}
}

func ValidParserFailureClass(value ParserFailureClass) bool {
	switch value {
	case ParserFailureNone, ParserFailureMalformedJSON, ParserFailureSchemaMismatch, ParserFailureMissingToolName, ParserFailureUnsupportedTransport, ParserFailureAmbiguousToolCall:
		return true
	default:
		return false
	}
}

func CanonicalRepairSource(value RepairSource) RepairSource {
	switch canonicalToken(string(value)) {
	case "", "none":
		return RepairSourceNone
	case "host", "host_normalizer", "normalizer":
		return RepairSourceHostNormalizer
	case "parser", "parser_repair":
		return RepairSourceParserRepair
	case "recovery", "recovery_prompt":
		return RepairSourceRecoveryPrompt
	case "deterministic", "deterministic_fallback", "fallback":
		return RepairSourceDeterministicFallback
	case "model", "model_self_repair", "self_repair":
		return RepairSourceModelSelfRepair
	default:
		return RepairSource(canonicalToken(string(value)))
	}
}

func ValidRepairSource(value RepairSource) bool {
	switch value {
	case RepairSourceNone, RepairSourceHostNormalizer, RepairSourceParserRepair, RepairSourceRecoveryPrompt, RepairSourceDeterministicFallback, RepairSourceModelSelfRepair:
		return true
	default:
		return false
	}
}

func CanonicalActionClass(value ActionClass) ActionClass {
	switch canonicalToken(string(value)) {
	case "", "unknown":
		return ActionClassUnknown
	case "read", "inspect", "inspection":
		return ActionClassRead
	case "search", "discover", "discovery", "broad_inspection", "narrow_search":
		return ActionClassSearch
	case "submit_plan", "plan_submission":
		return ActionClassSubmitPlan
	case "source_satisfied", "source_satisfaction":
		return ActionClassSourceSatisfied
	case "blocker", "blocked":
		return ActionClassBlocker
	case "review_pass", "review_passed", "decision_pass":
		return ActionClassReviewPass
	case "write", "edit", "per_edit_write", "source_edit":
		return ActionClassPerEditWrite
	case "deliverable_write", "artifact_write":
		return ActionClassDeliverableWrite
	case "verify", "verification", "shell_verification":
		return ActionClassVerify
	case "final", "final_answer", "answer":
		return ActionClassFinalAnswer
	case "prose_final":
		return ActionClassProseFinal
	case "probe", "generic_probe":
		return ActionClassGenericProbe
	case "delegate", "delegation", "child_task":
		return ActionClassDelegate
	default:
		return ActionClass(canonicalToken(string(value)))
	}
}

func ValidActionClass(value ActionClass) bool {
	switch value {
	case ActionClassUnknown, ActionClassRead, ActionClassSearch, ActionClassSubmitPlan, ActionClassSourceSatisfied, ActionClassBlocker, ActionClassReviewPass, ActionClassPerEditWrite, ActionClassDeliverableWrite, ActionClassVerify, ActionClassFinalAnswer, ActionClassProseFinal, ActionClassGenericProbe, ActionClassDelegate:
		return true
	default:
		return false
	}
}

func CanonicalCapability(value Capability) Capability {
	switch canonicalToken(string(value)) {
	case "":
		return ""
	case "semantic_discovery", "discovery":
		return CapabilitySemanticDiscovery
	case "literal_search", "search":
		return CapabilityLiteralSearch
	case "bounded_read", "read":
		return CapabilityBoundedRead
	case "unbounded_read":
		return CapabilityUnboundedRead
	case "source_edit", "edit":
		return CapabilitySourceEdit
	case "apply_patch", "patch":
		return CapabilityApplyPatch
	case "shell_verification", "verify":
		return CapabilityShellVerification
	case "mutating_shell":
		return CapabilityMutatingShell
	case "plan_submission", "submit_plan":
		return CapabilityPlanSubmission
	case "external_read":
		return CapabilityExternalRead
	case "external_write":
		return CapabilityExternalWrite
	case "delegation", "delegate":
		return CapabilityDelegation
	default:
		return Capability(canonicalToken(string(value)))
	}
}

func ValidCapability(value Capability) bool {
	switch value {
	case CapabilitySemanticDiscovery, CapabilityLiteralSearch, CapabilityBoundedRead, CapabilityUnboundedRead, CapabilitySourceEdit, CapabilityApplyPatch, CapabilityShellVerification, CapabilityMutatingShell, CapabilityPlanSubmission, CapabilityExternalRead, CapabilityExternalWrite, CapabilityDelegation:
		return true
	default:
		return false
	}
}

func CanonicalSideEffect(value SideEffectClass) SideEffectClass {
	switch canonicalToken(string(value)) {
	case "":
		return ""
	case "read", "read_only", "readonly":
		return SideEffectReadOnly
	case "additive", "write", "mutating":
		return SideEffectAdditive
	case "destructive", "delete":
		return SideEffectDestructive
	case "open_world", "external":
		return SideEffectOpenWorld
	case "unknown":
		return SideEffectUnknown
	default:
		return SideEffectClass(canonicalToken(string(value)))
	}
}

func canonicalPathRoleMatches(matches []PathRoleMatch) []PathRoleMatch {
	if len(matches) == 0 {
		return nil
	}
	out := make([]PathRoleMatch, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		match.Path = strings.TrimSpace(match.Path)
		match.MatchedPattern = strings.TrimSpace(match.MatchedPattern)
		match.PacketID = strings.TrimSpace(match.PacketID)
		if match.Path == "" && match.Role == "" && match.MatchedPattern == "" && match.PacketID == "" {
			continue
		}
		key := strings.Join([]string{match.Path, string(match.Role), match.MatchedPattern, match.PacketID}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, match)
	}
	slices.SortFunc(out, func(a, b PathRoleMatch) int {
		return strings.Compare(
			strings.Join([]string{a.Path, string(a.Role), a.MatchedPattern, a.PacketID}, "\x00"),
			strings.Join([]string{b.Path, string(b.Role), b.MatchedPattern, b.PacketID}, "\x00"),
		)
	})
	return out
}

func canonicalStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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

func firstTargetRef(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func buildActionKey(class ActionClass, tool string, capability Capability, target string) string {
	parts := []string{string(class)}
	if tool != "" {
		parts = append(parts, tool)
	}
	if capability != "" {
		parts = append(parts, string(capability))
	}
	if strings.TrimSpace(target) != "" {
		parts = append(parts, canonicalActionKey(target))
	}
	return strings.Join(parts, ":")
}

func canonicalActionKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func canonicalDottedToken(value string) string {
	value = canonicalToken(value)
	value = strings.ReplaceAll(value, "_", ".")
	return value
}

func canonicalToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Join(strings.Fields(value), "_")
	return value
}
