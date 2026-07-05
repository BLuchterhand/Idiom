package contractcompile

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
)

func normalizePlanExecutePlannerInput(input PlanExecutePlannerInput) PlanExecutePlannerInput {
	trimStrings(
		&input.ParentID,
		&input.LaneRunID,
		&input.PhaseRunID,
		&input.RuntimeContract,
		&input.PermissionProfile,
		&input.VerificationContract,
	)
	input.Lane = firstNonEmptyString(input.Lane, DefaultPlanExecuteLane)
	input.Phase = firstNonEmptyString(input.Phase, DefaultPlannerPhase)
	input.TaskFamily = firstNonEmptyString(input.TaskFamily, InvocationPlanExecutePlanner)
	normalizeStringSets(
		&input.TargetRefs,
		&input.UserBoundaries,
		&input.AllowedTools,
		&input.ForbiddenTools,
		&input.RequiredEvidence,
		&input.RequiredTools,
		&input.ReadinessGate,
		&input.VerificationPlan,
		&input.StopConditions,
		&input.FinalCritic,
		&input.TargetPacket,
		&input.Constraints,
		&input.RequiredNext,
		&input.InvalidActions,
		&input.MetadataTags,
	)
	normalizePathSets(&input.RequiredEditPaths, &input.AllowedEditPaths, &input.EvidencePaths, &input.ChangedPaths)
	normalizeReadinessInput(&input.Readiness)
	normalizeRuntimeInput(&input.Runtime)
	normalizeProtocolInput(&input.Protocol)

	return input
}

func normalizeReadinessInput(readiness *ReadinessInput) {
	normalizeStringSets(&readiness.ClosedClaims, &readiness.OpenObligations, &readiness.EvidenceSources)
	trimStrings(&readiness.FurtherReadsRequire)
}

func normalizeRuntimeInput(runtime *RuntimeEnvelopeInput) {
	trimStrings(
		&runtime.BackendKind,
		&runtime.ModelAlias,
		&runtime.ModelRole,
		&runtime.ParserTransportMode,
		&runtime.ToolTransportMode,
		&runtime.ParserContract,
		&runtime.NativeToolPolicy,
		&runtime.ToolChoice,
		&runtime.ResponseFormat,
		&runtime.ThinkingPolicy,
		&runtime.ReadinessProfile,
		&runtime.EnforcementMode,
	)
	clampNonNegative(&runtime.ThinkingBudget, &runtime.EffectiveMaxTokens)

	runtime.ContextBudget = normalizeContextBudget(runtime.ContextBudget)
	normalizeQueueState(&runtime.QueueState)

	degradedInvariants := append([]string(nil), runtime.DegradedInvariants...)
	authorizedFallbacks := append([]string(nil), runtime.AuthorizedFallback...)

	runtime.DegradedInvariants = sortedStrings(degradedInvariants)
	runtime.AuthorizedFallback = sortedStrings(authorizedFallbacks)
	runtime.DegradationDecisions = normalizeDegradationDecisions(runtime.DegradationDecisions, degradedInvariants, authorizedFallbacks)
}

func normalizeRuntimeEnvelope(runtime *RuntimeEnvelope) {
	trimStrings(
		&runtime.RuntimeContractID,
		&runtime.BackendKind,
		&runtime.ModelAlias,
		&runtime.ModelRole,
		&runtime.ParserTransportMode,
		&runtime.ToolTransportMode,
		&runtime.ParserContract,
		&runtime.NativeToolPolicy,
		&runtime.ToolChoice,
		&runtime.ResponseFormat,
		&runtime.ThinkingPolicy,
		&runtime.ReadinessProfile,
	)
	clampNonNegative(&runtime.ThinkingBudget, &runtime.EffectiveMaxTokens)

	runtime.ContextBudget = normalizeContextBudget(runtime.ContextBudget)
	normalizeQueueState(&runtime.QueueState)

	runtime.EnforcementMode = firstNonEmptyString(runtime.EnforcementMode, "report_only")
	runtime.DegradationDecisions = normalizeDegradationDecisions(runtime.DegradationDecisions, nil, nil)
	runtime.ActiveInvariantID = firstNonEmptyString(runtime.ActiveInvariantID, InvariantActiveAuthority)
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
	invariantIDs := uniqueStrings(degradedInvariants)
	fallbacks := uniqueStrings(authorizedFallbacks)
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
	decision.ReasonCode = firstNonEmptyString(decision.ReasonCode, "declared_runtime_degradation")
	decision.AuthorizedFallbacks = sortedStrings(decision.AuthorizedFallbacks)

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
		existing.AuthorizedFallbacks = sortedStrings(append(existing.AuthorizedFallbacks, decision.AuthorizedFallbacks...))
		merged[k] = existing
	}

	out := make([]DegradationDecision, 0, len(merged))
	for _, decision := range merged {
		out = append(out, decision)
	}

	return out
}

func normalizeContextBudget(budget ContextBudget) ContextBudget {
	clampNonNegative(&budget.MaxContextMessages, &budget.MaxContextChars)

	return budget
}

func normalizeQueueState(queue *QueueState) {
	trimStrings(&queue.Pool, &queue.LeaseID, &queue.State, &queue.Reason)
}

func normalizeProtocolInput(protocol *ProtocolInput) {
	trimStrings(
		&protocol.PhaseID,
		&protocol.ParserMode,
		&protocol.ExpectedVisibleOutput,
		&protocol.MalformedOutputPolicy,
		&protocol.StaleAnchorPolicy,
	)
	normalizeStringSets(&protocol.VisibleTools)

	if protocol.CallableTools != nil {
		protocol.CallableToolsSpecified = true
	}

	if protocol.CallableToolsSpecified {
		protocol.CallableTools = sortedStrings(protocol.CallableTools)
	} else {
		protocol.CallableTools = nil
	}

	protocol.CallableActionClasses = canonicalActionClassStrings(protocol.CallableActionClasses)
	protocol.InvalidActionClasses = canonicalActionClassStrings(protocol.InvalidActionClasses)
	normalizeStringSets(
		&protocol.TerminalResponseShapes,
		&protocol.StructuredSchemaNames,
		&protocol.SourceBodyObligations,
		&protocol.CompletedGitHistoryPaths,
		&protocol.PendingGitHistoryPaths,
	)
	clampNonNegative(&protocol.RecoveryBudget)
}

func normalizeProtocolProjection(protocol *ModelProtocolProjection) {
	trimStrings(
		&protocol.ContractID,
		&protocol.ContractVersion,
		&protocol.ProtocolHash,
		&protocol.PhaseID,
		&protocol.ParserMode,
		&protocol.ToolTransportMode,
		&protocol.ParserTransportMode,
		&protocol.ParserContract,
		&protocol.NativeToolPolicy,
		&protocol.ToolChoice,
		&protocol.ResponseFormat,
		&protocol.ExpectedVisibleOutput,
		&protocol.MalformedOutputPolicy,
		&protocol.StaleAnchorPolicy,
	)
	normalizeStringSets(&protocol.SchemaNames, &protocol.VisibleTools, &protocol.CallableTools)
	protocol.CallableActionClasses = canonicalActionClassStrings(protocol.CallableActionClasses)
	protocol.InvalidActionClasses = canonicalActionClassStrings(protocol.InvalidActionClasses)
	normalizeStringSets(
		&protocol.TerminalResponseShapes,
		&protocol.StructuredSchemaNames,
		&protocol.SourceBodyObligations,
		&protocol.CompletedGitHistoryPaths,
		&protocol.PendingGitHistoryPaths,
		&protocol.PathRoleSummary,
		&protocol.VerificationObligations,
		&protocol.SafeAlternatives,
		&protocol.RequiredTools,
		&protocol.ReadinessGate,
		&protocol.Constraints,
		&protocol.InvalidActions,
	)
	normalizePathSets(&protocol.RequiredEditCoverage)
	protocol.ActiveInvariantID = firstNonEmptyString(protocol.ActiveInvariantID, InvariantActiveAuthority)
	normalizeCompactPacketProjection(&protocol.CompactPacket)
}

func normalizeCompactPacketProjection(packet *autonomy.ContextPacket) {
	trimStrings(&packet.SchemaVersion, &packet.PacketID, &packet.ContractID, &packet.ProtocolHash)
	normalizeStringSets(
		&packet.SummaryRefs,
		&packet.TargetRefs,
		&packet.SymbolRefs,
		&packet.EvidenceRefs,
		&packet.ObligationRefs,
		&packet.SchemaNames,
		&packet.RedactionMarkers,
	)
	normalizePathSets(&packet.PathRefs)
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

	return sortedStrings(out)
}

func capabilitiesForTool(tool string) []autonomy.Capability {
	entry, ok := autonomy.LookupPlanExecutePlannerToolMetadata(tool)
	if !ok {
		return nil
	}

	return entry.Capabilities
}

func knownTool(tool string) bool {
	_, ok := autonomy.LookupPlanExecutePlannerToolMetadata(tool)
	return ok
}

func sideEffectForTool(tool string) autonomy.SideEffectClass {
	entry, ok := autonomy.LookupPlanExecutePlannerToolMetadata(tool)
	if !ok {
		return autonomy.SideEffectUnknown
	}

	return entry.SideEffect
}

func allowedEffects(input PlanExecutePlannerInput) []autonomy.SideEffectClass {
	return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
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

	return sortedStrings(ids)
}

func trimStrings(fields ...*string) {
	for _, field := range fields {
		*field = strings.TrimSpace(*field)
	}
}

func normalizeStringSets(fields ...*[]string) {
	for _, field := range fields {
		*field = sortedStrings(*field)
	}
}

func normalizePathSets(fields ...*[]string) {
	for _, field := range fields {
		*field = uniquePaths(*field)
	}
}

func clampNonNegative(fields ...*int) {
	for _, field := range fields {
		if *field < 0 {
			*field = 0
		}
	}
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
