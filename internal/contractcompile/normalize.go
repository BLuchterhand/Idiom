package contractcompile

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/autonomy"
)

func normalizePlanExecutePlannerInput(input PlanExecutePlannerInput) PlanExecutePlannerInput {
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Lane = firstNonEmpty(input.Lane, DefaultPlanExecuteLane)
	input.LaneRunID = strings.TrimSpace(input.LaneRunID)
	input.Phase = firstNonEmpty(input.Phase, DefaultPlannerPhase)
	input.PhaseRunID = strings.TrimSpace(input.PhaseRunID)
	input.TaskFamily = firstNonEmpty(input.TaskFamily, InvocationPlanExecutePlanner)
	input.RuntimeContract = strings.TrimSpace(input.RuntimeContract)
	input.PermissionProfile = strings.TrimSpace(input.PermissionProfile)
	input.TargetRefs = uniqueStrings(input.TargetRefs)
	input.RequiredEditPaths = uniquePaths(input.RequiredEditPaths)
	input.AllowedEditPaths = uniquePaths(input.AllowedEditPaths)
	input.EvidencePaths = uniquePaths(input.EvidencePaths)
	input.ChangedPaths = uniquePaths(input.ChangedPaths)
	input.UserBoundaries = uniqueStrings(input.UserBoundaries)
	input.AllowedTools = uniqueStrings(input.AllowedTools)
	input.ForbiddenTools = uniqueStrings(input.ForbiddenTools)
	input.RequiredEvidence = uniqueStrings(input.RequiredEvidence)
	input.RequiredTools = uniqueStrings(input.RequiredTools)
	input.ReadinessGate = uniqueStrings(input.ReadinessGate)
	input.VerificationContract = strings.TrimSpace(input.VerificationContract)
	input.VerificationPlan = uniqueStrings(input.VerificationPlan)
	input.StopConditions = uniqueStrings(input.StopConditions)
	input.FinalCritic = uniqueStrings(input.FinalCritic)
	input.TargetPacket = uniqueStrings(input.TargetPacket)
	input.Constraints = uniqueStrings(input.Constraints)
	input.RequiredNext = uniqueStrings(input.RequiredNext)
	input.InvalidActions = uniqueStrings(input.InvalidActions)
	normalizeReadinessInput(&input.Readiness)
	normalizeRuntimeInput(&input.Runtime)
	normalizeProtocolInput(&input.Protocol)
	input.MetadataTags = uniqueStrings(input.MetadataTags)
	return input
}

func normalizeReadinessInput(readiness *ReadinessInput) {
	readiness.ClosedClaims = uniqueStrings(readiness.ClosedClaims)
	readiness.OpenObligations = uniqueStrings(readiness.OpenObligations)
	readiness.EvidenceSources = uniqueStrings(readiness.EvidenceSources)
	readiness.FurtherReadsRequire = strings.TrimSpace(readiness.FurtherReadsRequire)
}

func normalizeRuntimeInput(runtime *RuntimeEnvelopeInput) {
	runtime.BackendKind = strings.TrimSpace(runtime.BackendKind)
	runtime.ModelAlias = strings.TrimSpace(runtime.ModelAlias)
	runtime.ModelRole = strings.TrimSpace(runtime.ModelRole)
	runtime.ParserTransportMode = strings.TrimSpace(runtime.ParserTransportMode)
	runtime.ToolTransportMode = strings.TrimSpace(runtime.ToolTransportMode)
	runtime.ParserContract = strings.TrimSpace(runtime.ParserContract)
	runtime.NativeToolPolicy = strings.TrimSpace(runtime.NativeToolPolicy)
	runtime.ToolChoice = strings.TrimSpace(runtime.ToolChoice)
	runtime.ResponseFormat = strings.TrimSpace(runtime.ResponseFormat)
	runtime.ThinkingPolicy = strings.TrimSpace(runtime.ThinkingPolicy)
	if runtime.ThinkingBudget < 0 {
		runtime.ThinkingBudget = 0
	}
	if runtime.EffectiveMaxTokens < 0 {
		runtime.EffectiveMaxTokens = 0
	}
	runtime.ContextBudget = normalizeContextBudget(runtime.ContextBudget)
	normalizeQueueState(&runtime.QueueState)
	runtime.ReadinessProfile = strings.TrimSpace(runtime.ReadinessProfile)
	runtime.EnforcementMode = strings.TrimSpace(runtime.EnforcementMode)
	degradedInvariants := append([]string(nil), runtime.DegradedInvariants...)
	authorizedFallbacks := append([]string(nil), runtime.AuthorizedFallback...)
	runtime.DegradedInvariants = uniqueStrings(degradedInvariants)
	runtime.AuthorizedFallback = uniqueStrings(authorizedFallbacks)
	runtime.DegradationDecisions = normalizeDegradationDecisions(runtime.DegradationDecisions, degradedInvariants, authorizedFallbacks)
}

func normalizeRuntimeEnvelope(runtime *RuntimeEnvelope) {
	runtime.RuntimeContractID = strings.TrimSpace(runtime.RuntimeContractID)
	runtime.BackendKind = strings.TrimSpace(runtime.BackendKind)
	runtime.ModelAlias = strings.TrimSpace(runtime.ModelAlias)
	runtime.ModelRole = strings.TrimSpace(runtime.ModelRole)
	runtime.ParserTransportMode = strings.TrimSpace(runtime.ParserTransportMode)
	runtime.ToolTransportMode = strings.TrimSpace(runtime.ToolTransportMode)
	runtime.ParserContract = strings.TrimSpace(runtime.ParserContract)
	runtime.NativeToolPolicy = strings.TrimSpace(runtime.NativeToolPolicy)
	runtime.ToolChoice = strings.TrimSpace(runtime.ToolChoice)
	runtime.ResponseFormat = strings.TrimSpace(runtime.ResponseFormat)
	runtime.ThinkingPolicy = strings.TrimSpace(runtime.ThinkingPolicy)
	if runtime.ThinkingBudget < 0 {
		runtime.ThinkingBudget = 0
	}
	if runtime.EffectiveMaxTokens < 0 {
		runtime.EffectiveMaxTokens = 0
	}
	runtime.ContextBudget = normalizeContextBudget(runtime.ContextBudget)
	normalizeQueueState(&runtime.QueueState)
	runtime.ReadinessProfile = strings.TrimSpace(runtime.ReadinessProfile)
	runtime.EnforcementMode = firstNonEmpty(runtime.EnforcementMode, "report_only")
	runtime.DegradationDecisions = normalizeDegradationDecisions(runtime.DegradationDecisions, nil, nil)
	runtime.ActiveInvariantID = firstNonEmpty(runtime.ActiveInvariantID, InvariantActiveAuthority)
}

func normalizeDegradationDecisions(decisions []DegradationDecision, degradedInvariants []string, authorizedFallbacks []string) []DegradationDecision {
	out := make([]DegradationDecision, 0, len(decisions)+len(degradedInvariants))
	for _, decision := range decisions {
		normalized := normalizeDegradationDecision(decision)
		if normalized.InvariantID == "" {
			continue
		}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		for _, decision := range legacyDegradationDecisions(degradedInvariants, authorizedFallbacks) {
			out = append(out, DegradationDecision{
				InvariantID:         decision.InvariantID,
				ReasonCode:          "declared_degraded_runtime_invariant",
				Decision:            degradationDecisionForFallbacks(decision.AuthorizedFallbacks),
				AuthorizedFallbacks: append([]string(nil), decision.AuthorizedFallbacks...),
			})
		}
	}
	out = mergeDegradationDecisions(out)
	slices.SortFunc(out, func(a, b DegradationDecision) int {
		return cmp.Or(
			cmp.Compare(a.InvariantID, b.InvariantID),
			cmp.Compare(a.ReasonCode, b.ReasonCode),
			cmp.Compare(a.Decision, b.Decision),
			cmp.Compare(strings.Join(a.AuthorizedFallbacks, "\x00"), strings.Join(b.AuthorizedFallbacks, "\x00")),
		)
	})
	return out
}

func degradationDecisionForFallbacks(fallbacks []string) string {
	if len(fallbacks) > 0 {
		return "authorized_fallback"
	}
	return "degraded_no_fallback"
}

func legacyDegradationDecisions(degradedInvariants []string, authorizedFallbacks []string) []DegradationDecision {
	invariantIDs := uniqueStringsInOrder(degradedInvariants)
	fallbacks := uniqueStringsInOrder(authorizedFallbacks)
	out := make([]DegradationDecision, 0, len(invariantIDs))
	for i, invariantID := range invariantIDs {
		out = append(out, DegradationDecision{
			InvariantID:         invariantID,
			AuthorizedFallbacks: legacyFallbacksForInvariant(i, len(invariantIDs), fallbacks),
		})
	}
	return out
}

func legacyFallbacksForInvariant(index int, invariantCount int, fallbacks []string) []string {
	switch {
	case len(fallbacks) == 0:
		return nil
	case len(fallbacks) == 1:
		return append([]string(nil), fallbacks...)
	case len(fallbacks) == invariantCount:
		return []string{fallbacks[index]}
	case index < len(fallbacks):
		return []string{fallbacks[index]}
	default:
		return nil
	}
}

func normalizeDegradationDecision(decision DegradationDecision) DegradationDecision {
	decision.InvariantID = strings.TrimSpace(decision.InvariantID)
	decision.ReasonCode = firstNonEmpty(decision.ReasonCode, "declared_runtime_degradation")
	decision.AuthorizedFallbacks = uniqueStrings(decision.AuthorizedFallbacks)
	if strings.TrimSpace(decision.Decision) == "" {
		if len(decision.AuthorizedFallbacks) > 0 {
			decision.Decision = "authorized_fallback"
		} else {
			decision.Decision = "degraded_no_fallback"
		}
	} else {
		decision.Decision = strings.TrimSpace(decision.Decision)
	}
	return decision
}

func mergeDegradationDecisions(decisions []DegradationDecision) []DegradationDecision {
	type key struct {
		invariantID string
		reasonCode  string
		decision    string
	}
	merged := make(map[key]DegradationDecision, len(decisions))
	for _, decision := range decisions {
		decision = normalizeDegradationDecision(decision)
		if decision.InvariantID == "" {
			continue
		}
		k := key{
			invariantID: decision.InvariantID,
			reasonCode:  decision.ReasonCode,
			decision:    decision.Decision,
		}
		existing := merged[k]
		existing.InvariantID = decision.InvariantID
		existing.ReasonCode = decision.ReasonCode
		existing.Decision = decision.Decision
		existing.AuthorizedFallbacks = uniqueStrings(append(existing.AuthorizedFallbacks, decision.AuthorizedFallbacks...))
		merged[k] = existing
	}
	out := make([]DegradationDecision, 0, len(merged))
	for _, decision := range merged {
		out = append(out, decision)
	}
	return out
}

func normalizeContextBudget(budget ContextBudget) ContextBudget {
	if budget.MaxContextMessages < 0 {
		budget.MaxContextMessages = 0
	}
	if budget.MaxContextChars < 0 {
		budget.MaxContextChars = 0
	}
	return budget
}

func normalizeQueueState(queue *QueueState) {
	queue.Pool = strings.TrimSpace(queue.Pool)
	queue.LeaseID = strings.TrimSpace(queue.LeaseID)
	queue.State = strings.TrimSpace(queue.State)
	queue.Reason = strings.TrimSpace(queue.Reason)
}

func normalizeProtocolInput(protocol *ProtocolInput) {
	protocol.PhaseID = strings.TrimSpace(protocol.PhaseID)
	protocol.ParserMode = strings.TrimSpace(protocol.ParserMode)
	protocol.ExpectedVisibleOutput = strings.TrimSpace(protocol.ExpectedVisibleOutput)
	protocol.VisibleTools = uniqueStrings(protocol.VisibleTools)
	if protocol.CallableTools != nil {
		protocol.CallableToolsSpecified = true
	}
	if protocol.CallableToolsSpecified {
		protocol.CallableTools = uniqueStrings(protocol.CallableTools)
	} else {
		protocol.CallableTools = nil
	}
	protocol.CallableActionClasses = canonicalActionClassStrings(protocol.CallableActionClasses)
	protocol.InvalidActionClasses = canonicalActionClassStrings(protocol.InvalidActionClasses)
	protocol.TerminalResponseShapes = uniqueStrings(protocol.TerminalResponseShapes)
	protocol.StructuredSchemaNames = uniqueStrings(protocol.StructuredSchemaNames)
	protocol.MalformedOutputPolicy = strings.TrimSpace(protocol.MalformedOutputPolicy)
	protocol.StaleAnchorPolicy = strings.TrimSpace(protocol.StaleAnchorPolicy)
	protocol.SourceBodyObligations = uniqueStrings(protocol.SourceBodyObligations)
	if protocol.RecoveryBudget < 0 {
		protocol.RecoveryBudget = 0
	}
}

func normalizeProtocolProjection(protocol *ModelProtocolProjection) {
	protocol.ContractID = strings.TrimSpace(protocol.ContractID)
	protocol.ContractVersion = strings.TrimSpace(protocol.ContractVersion)
	protocol.ProtocolHash = strings.TrimSpace(protocol.ProtocolHash)
	protocol.PhaseID = strings.TrimSpace(protocol.PhaseID)
	protocol.ParserMode = strings.TrimSpace(protocol.ParserMode)
	protocol.ToolTransportMode = strings.TrimSpace(protocol.ToolTransportMode)
	protocol.ParserTransportMode = strings.TrimSpace(protocol.ParserTransportMode)
	protocol.ParserContract = strings.TrimSpace(protocol.ParserContract)
	protocol.NativeToolPolicy = strings.TrimSpace(protocol.NativeToolPolicy)
	protocol.ToolChoice = strings.TrimSpace(protocol.ToolChoice)
	protocol.ResponseFormat = strings.TrimSpace(protocol.ResponseFormat)
	protocol.ExpectedVisibleOutput = strings.TrimSpace(protocol.ExpectedVisibleOutput)
	protocol.VisibleTools = uniqueStrings(protocol.VisibleTools)
	protocol.CallableTools = uniqueStrings(protocol.CallableTools)
	protocol.CallableActionClasses = canonicalActionClassStrings(protocol.CallableActionClasses)
	protocol.InvalidActionClasses = canonicalActionClassStrings(protocol.InvalidActionClasses)
	protocol.TerminalResponseShapes = uniqueStrings(protocol.TerminalResponseShapes)
	protocol.StructuredSchemaNames = uniqueStrings(protocol.StructuredSchemaNames)
	protocol.MalformedOutputPolicy = strings.TrimSpace(protocol.MalformedOutputPolicy)
	protocol.StaleAnchorPolicy = strings.TrimSpace(protocol.StaleAnchorPolicy)
	protocol.SourceBodyObligations = uniqueStrings(protocol.SourceBodyObligations)
	protocol.RequiredEditCoverage = uniquePaths(protocol.RequiredEditCoverage)
	protocol.PathRoleSummary = uniqueStrings(protocol.PathRoleSummary)
	protocol.VerificationObligations = uniqueStrings(protocol.VerificationObligations)
	protocol.SafeAlternatives = uniqueStrings(protocol.SafeAlternatives)
	protocol.RequiredTools = uniqueStrings(protocol.RequiredTools)
	protocol.ReadinessGate = uniqueStrings(protocol.ReadinessGate)
	protocol.Constraints = uniqueStrings(protocol.Constraints)
	protocol.InvalidActions = uniqueStrings(protocol.InvalidActions)
	protocol.ActiveInvariantID = firstNonEmpty(protocol.ActiveInvariantID, InvariantActiveAuthority)
}

func canonicalActionClassStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		canonical := autonomy.CanonicalActionClass(autonomy.ActionClass(value))
		if canonical == "" {
			continue
		}
		out = append(out, string(canonical))
	}
	return uniqueStrings(out)
}

var planExecutePlannerToolVocabulary = map[string]ToolVocabularyEntry{
	"blame_range":      plannerTool("blame_range", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"code_navigate":    plannerTool("code_navigate", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"context_impact":   plannerTool("context_impact", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"context_pack":     plannerTool("context_pack", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"context_query":    plannerTool("context_query", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"context_status":   plannerTool("context_status", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"context_zoom":     plannerTool("context_zoom", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"discovery_query":  plannerTool("discovery_query", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"file_stats":       plannerTool("file_stats", autonomy.CapabilityBoundedRead, autonomy.SideEffectReadOnly, true),
	"harness_feedback": plannerTool("harness_feedback", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"history_range":    plannerTool("history_range", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"history_symbol":   plannerTool("history_symbol", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"product_efforts":  plannerTool("product_efforts", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"product_status":   plannerTool("product_status", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"read_file":        plannerTool("read_file", autonomy.CapabilityBoundedRead, autonomy.SideEffectReadOnly, true),
	"recovery_inspect": plannerTool("recovery_inspect", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"recovery_status":  plannerTool("recovery_status", autonomy.CapabilitySemanticDiscovery, autonomy.SideEffectReadOnly, true),
	"search_content":   plannerTool("search_content", autonomy.CapabilityLiteralSearch, autonomy.SideEffectReadOnly, true),
	"search_files":     plannerTool("search_files", autonomy.CapabilityLiteralSearch, autonomy.SideEffectReadOnly, true),
	"structural_search": plannerTool(
		"structural_search",
		autonomy.CapabilityLiteralSearch,
		autonomy.SideEffectReadOnly,
		true,
	),
	"submit_plan": plannerTool("submit_plan", autonomy.CapabilityPlanSubmission, autonomy.SideEffectReadOnly, true),

	"append_file":        plannerTool("append_file", autonomy.CapabilitySourceEdit, autonomy.SideEffectAdditive, false),
	"apply_patch":        plannerTool("apply_patch", autonomy.CapabilityApplyPatch, autonomy.SideEffectAdditive, false),
	"context_trace_pack": plannerTool("context_trace_pack", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"cpp_diagnostics":    plannerTool("cpp_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"delegate_edit":      plannerTool("delegate_edit", autonomy.CapabilityDelegation, autonomy.SideEffectAdditive, false),
	"edit_file":          plannerTool("edit_file", autonomy.CapabilitySourceEdit, autonomy.SideEffectAdditive, false),
	"go_diagnostics":     plannerTool("go_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"go_move_declarations": plannerTool(
		"go_move_declarations",
		autonomy.CapabilitySourceEdit,
		autonomy.SideEffectAdditive,
		false,
	),
	"java_diagnostics":   plannerTool("java_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"mkdir":              plannerTool("mkdir", autonomy.CapabilitySourceEdit, autonomy.SideEffectAdditive, false),
	"python_diagnostics": plannerTool("python_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"recovery_decompose": plannerTool("recovery_decompose", autonomy.CapabilityExternalWrite, autonomy.SideEffectAdditive, false),
	"rust_diagnostics":   plannerTool("rust_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"shell":              plannerTool("shell", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"source_sanity":      plannerTool("source_sanity", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"task":               plannerTool("task", autonomy.CapabilityDelegation, autonomy.SideEffectUnknown, false),
	"task_ledger":        plannerTool("task_ledger", autonomy.CapabilityExternalWrite, autonomy.SideEffectAdditive, false),
	"ts_diagnostics":     plannerTool("ts_diagnostics", autonomy.CapabilityShellVerification, autonomy.SideEffectUnknown, false),
	"write_file":         plannerTool("write_file", autonomy.CapabilitySourceEdit, autonomy.SideEffectAdditive, false),
}

func plannerTool(tool string, capability autonomy.Capability, sideEffect autonomy.SideEffectClass, plannerAllowed bool) ToolVocabularyEntry {
	return ToolVocabularyEntry{
		Tool:           tool,
		Capabilities:   []autonomy.Capability{capability},
		SideEffect:     sideEffect,
		PlannerAllowed: plannerAllowed,
	}
}

func PlanExecutePlannerToolVocabulary() []ToolVocabularyEntry {
	entries := make([]ToolVocabularyEntry, 0, len(planExecutePlannerToolVocabulary))
	for _, entry := range planExecutePlannerToolVocabulary {
		entry.Capabilities = append([]autonomy.Capability(nil), entry.Capabilities...)
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b ToolVocabularyEntry) int { return cmp.Compare(a.Tool, b.Tool) })
	return entries
}

func PlanExecutePlannerToolNames() []string {
	var tools []string
	for _, entry := range PlanExecutePlannerToolVocabulary() {
		if entry.PlannerAllowed {
			tools = append(tools, entry.Tool)
		}
	}
	return tools
}

func IsPlanExecutePlannerToolAllowed(tool string) bool {
	entry, ok := toolVocabularyEntry(tool)
	return ok && entry.PlannerAllowed
}

func toolVocabularyEntry(tool string) (ToolVocabularyEntry, bool) {
	entry, ok := planExecutePlannerToolVocabulary[strings.TrimSpace(tool)]
	if !ok {
		return ToolVocabularyEntry{}, false
	}
	entry.Capabilities = append([]autonomy.Capability(nil), entry.Capabilities...)
	return entry, true
}

func capabilitiesForTool(tool string) []autonomy.Capability {
	entry, ok := toolVocabularyEntry(tool)
	if !ok {
		return nil
	}
	return entry.Capabilities
}

func knownTool(tool string) bool {
	_, ok := toolVocabularyEntry(tool)
	return ok
}

func sideEffectForTool(tool string) autonomy.SideEffectClass {
	entry, ok := toolVocabularyEntry(tool)
	if !ok {
		return autonomy.SideEffectUnknown
	}
	return entry.SideEffect
}

func allowedEffects(input PlanExecutePlannerInput) []autonomy.SideEffectClass {
	return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
}

func forbiddenEffects(input PlanExecutePlannerInput) []autonomy.SideEffectClass {
	if strings.Contains(strings.ToLower(input.PermissionProfile), "read_only") {
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
	}
	return []autonomy.SideEffectClass{autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
}

func capabilityKeys(set map[autonomy.Capability]struct{}) []autonomy.Capability {
	out := make([]autonomy.Capability, 0, len(set))
	for capability := range set {
		out = append(out, capability)
	}
	slices.SortFunc(out, func(a, b autonomy.Capability) int { return cmp.Compare(a, b) })
	return out
}

func obligationIDs(obligations []autonomy.Obligation) []string {
	ids := make([]string, 0, len(obligations))
	for _, obligation := range obligations {
		ids = append(ids, obligation.ObligationID)
	}
	return uniqueStrings(ids)
}

func uniquePaths(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizePath(value)
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

func uniqueStrings(values []string) []string {
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

func uniqueStringsInOrder(values []string) []string {
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
	return out
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return strings.TrimPrefix(path, "./")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func stableHashID(prefix string, parts ...any) string {
	encoded, err := json.Marshal(parts)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return prefix + "_" + hex.EncodeToString(sum[:])[:16]
}
