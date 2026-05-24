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
				Decision:            "authorized_fallback",
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
	decision.Decision = firstNonEmpty(decision.Decision, "authorized_fallback")
	decision.AuthorizedFallbacks = uniqueStrings(decision.AuthorizedFallbacks)
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
	protocol.ExpectedVisibleOutput = strings.TrimSpace(protocol.ExpectedVisibleOutput)
	protocol.VisibleTools = uniqueStrings(protocol.VisibleTools)
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

func capabilitiesForTool(tool string) []autonomy.Capability {
	switch strings.TrimSpace(tool) {
	case "read_file":
		return []autonomy.Capability{autonomy.CapabilityBoundedRead}
	case "search_files", "search_content", "rg", "grep":
		return []autonomy.Capability{autonomy.CapabilityLiteralSearch}
	case "context_pack", "context_query", "context_zoom", "discovery_query", "go_read_symbol", "go_find_symbol", "cpp_read_symbol", "cpp_find_symbol":
		return []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}
	case "edit_file", "write_file", "append_file", "mkdir":
		return []autonomy.Capability{autonomy.CapabilitySourceEdit}
	case "apply_patch":
		return []autonomy.Capability{autonomy.CapabilityApplyPatch}
	case "shell", "source_sanity", "go_diagnostics", "cpp_diagnostics":
		return []autonomy.Capability{autonomy.CapabilityShellVerification}
	case "task":
		return []autonomy.Capability{autonomy.CapabilityDelegation}
	case "submit_plan":
		return []autonomy.Capability{autonomy.CapabilityPlanSubmission}
	default:
		return nil
	}
}

func knownTool(tool string) bool {
	return len(capabilitiesForTool(tool)) > 0
}

func sideEffectForTool(tool string) autonomy.SideEffectClass {
	switch strings.TrimSpace(tool) {
	case "edit_file", "write_file", "append_file", "mkdir", "apply_patch":
		return autonomy.SideEffectAdditive
	case "shell":
		return autonomy.SideEffectUnknown
	default:
		return autonomy.SideEffectReadOnly
	}
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
