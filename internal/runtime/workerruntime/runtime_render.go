package workerruntime

import (
	"encoding/json"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/textutil"
)

func RenderStepInstruction(instruction StepInstruction) string {
	if strings.TrimSpace(instruction.Directive) == "" {
		instruction.Directive = "Use this current-state packet for the next action. Do not repeat invalid actions."
	}

	data, err := json.Marshal(instruction)
	if err != nil {
		return ""
	}

	return strings.Join([]string{
		"[frontier step instruction]",
		instruction.Directive,
		string(data),
	}, "\n")
}

func RenderRunState(state RunState) string {
	return RenderStepInstruction(BuildStepInstruction(state))
}

func RenderInitialRuntimeBrief(brief WorkerRuntimeBrief, isFollowUp ...bool) string {
	brief = NormalizeRuntimeBrief(brief)
	brief = withDeterministicDiscoveryExamples(brief)

	if !RuntimeBriefHasContent(brief) {
		return ""
	}

	return RenderStepInstruction(BuildInitialStepInstruction(brief, isFollowUp...))
}

func withDeterministicDiscoveryExamples(brief WorkerRuntimeBrief) WorkerRuntimeBrief {
	if !toolAllowedInBrief(brief.AllowedTools, "discovery_query") {
		return brief
	}

	if discoveryExamplesWouldCompeteWithWriteReadyPacket(brief) {
		return brief
	}

	brief.DiscoveryExamples = textutil.UniqueNonEmpty(append(brief.DiscoveryExamples,
		`For broad architecture, impact, tracing, security, or history questions, the bounded discovery-packet capability is available before ad hoc list/search/read; choose purpose from the tool schema and follow with targeted reads only for named gaps.`,
	))
	if toolAllowedInBrief(brief.AllowedTools, workspace.ToolNameContextPack) && !discoveryExamplesWouldCompeteWithWriteReadyPacket(brief) {
		brief.DiscoveryExamples = textutil.UniqueNonEmpty(append(brief.DiscoveryExamples,
			`For repo-localization questions, call context_pack with the task string first to get a ranked cached packet of candidate files, symbols, and tests.`,
		))
	}

	if toolAllowedInBrief(brief.AllowedTools, workspace.ToolNameContextQuery) {
		brief.DiscoveryExamples = textutil.UniqueNonEmpty(append(brief.DiscoveryExamples,
			`For graph-shaped questions (what calls X? what tests cover Y? what depends on Z?), use context_query — it answers structured questions without dumping raw files. Prefer context_query over search_content for codebase questions.`,
		))
	}

	// Capability guidance adapts to which tools the lane actually has without
	// forcing a universal investigation sequence.
	if toolAllowedInBrief(brief.AllowedTools, workspace.ToolNameContextPack) {
		brief.DiscoveryExamples = textutil.UniqueNonEmpty(append(brief.DiscoveryExamples,
			`For workspace investigation, choose the capability that matches the named gap: context_pack for localization, context_query for graph questions, code_navigate for symbol bodies, read_file for bounded reads, and search_content for exact literal text.`,
		))
	} else if toolAllowedInBrief(brief.AllowedTools, workspace.ToolNameContextQuery) {
		brief.DiscoveryExamples = textutil.UniqueNonEmpty(append(brief.DiscoveryExamples,
			`For workspace investigation, choose the capability that matches the named gap: context_query for graph and symbol questions, code_navigate for symbol bodies, read_file for bounded reads, and search_content for exact literal text.`,
		))
	}

	return brief
}

func discoveryExamplesWouldCompeteWithWriteReadyPacket(brief WorkerRuntimeBrief) bool {
	state := MergeRuntimeBriefIntoState(brief, RunState{})
	if !initialActionContractApplies(state) {
		return false
	}

	if len(state.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 {
		return true
	}

	if state.Readiness != nil && state.Readiness.ReadyToWrite {
		return true
	}

	if ConstraintPresent(state.Constraints, ConstraintImplementationPacketWriteReady) ||
		ConstraintPresent(state.Constraints, ConstraintExactAnchorImplementation) ||
		ConstraintPresent(state.Constraints, ConstraintRequiredEditPathsDefineWriteScope) {

		return true
	}

	return len(state.TargetPacket) > 0 && (len(state.TargetRefs) > 0 || len(state.EvidencePaths) > 0)
}

func toolAllowedInBrief(tools []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}

	for _, tool := range tools {
		if textutil.EqualTrimFold(tool, want) {
			return true
		}
	}

	return false
}

type evidenceCapabilitySummaryInfo struct {
	classes []string
}

func compactEvidenceCapabilitySummary(manifest []ToolManifestEntry) []string {
	byCapability := make(map[string]*evidenceCapabilitySummaryInfo)

	for _, entry := range manifest {
		tool := strings.TrimSpace(entry.Tool)
		if tool == "" || len(entry.EvidenceCapabilities) == 0 {
			continue
		}

		classification := strings.TrimSpace(entry.CapabilityClassification)
		for _, capability := range entry.EvidenceCapabilities {
			capability = strings.TrimSpace(capability)
			if capability == "" {
				continue
			}

			info := byCapability[capability]
			if info == nil {
				info = &evidenceCapabilitySummaryInfo{}
				byCapability[capability] = info
			}

			if classification != "" {
				info.classes = appendUnique(info.classes, classification)
			}
		}
	}

	if len(byCapability) == 0 {
		return nil
	}

	var summary []string

	for _, capability := range evidenceCapabilityDisplayOrder() {
		info := byCapability[capability]
		if info == nil {
			continue
		}

		summary = append(summary, formatEvidenceCapabilitySummary(capability, *info))
		delete(byCapability, capability)
	}

	for capability, info := range byCapability {
		summary = append(summary, formatEvidenceCapabilitySummary(capability, *info))
	}

	return textutil.UniqueNonEmpty(summary)
}

func evidenceCapabilityDisplayOrder() []string {
	return []string{
		"localize_change",
		"query_context",
		"inspect_file",
		"inspect_symbol",
		"find_definition",
		"find_references",
		"history_context",
		"calls_from",
		"calls_into",
		"related_files",
		"security_scan",
		"source_diagnostics",
		"verify_edit",
		"audit_behavior",
	}
}

func formatEvidenceCapabilitySummary(capability string, info evidenceCapabilitySummaryInfo) string {
	parts := []string{strings.TrimSpace(capability)}
	if len(info.classes) > 0 {
		parts = append(parts, strings.Join(textutil.UniqueNonEmpty(info.classes), "+"))
	}

	if len(parts) == 1 {
		return parts[0]
	}

	return parts[0] + " (" + strings.Join(parts[1:], "; ") + ")"
}

func RenderRuntimeBriefSummary(brief WorkerRuntimeBrief) string {
	brief = NormalizeRuntimeBrief(brief)
	if !RuntimeBriefHasContent(brief) {
		return ""
	}

	brief.ToolManifest = nil
	brief.DiscoveryExamples = nil

	data, err := json.Marshal(brief)
	if err != nil {
		return ""
	}

	return strings.Join([]string{
		"[frontier runtime brief]",
		"Preserved frontier runtime state. Continue under these same target, policy, evidence, and verification constraints.",
		string(data),
	}, "\n")
}

func RuntimeBriefHasContent(brief WorkerRuntimeBrief) bool {
	brief = NormalizeRuntimeBrief(brief)

	return brief.Lane != "" ||
		brief.TaskFamily != "" ||
		brief.Contract != "" ||
		brief.Objective != "" ||
		brief.RequestedOutcome != "" ||
		brief.WriteIntent != "" ||
		brief.ReadOnlyConstraint ||
		brief.EvidenceIntent != "" ||
		len(brief.TargetRefs) > 0 ||
		len(brief.RequiredEditPaths) > 0 ||
		len(brief.AllowedEditPaths) > 0 ||
		len(brief.RequiredEditSnippets) > 0 ||
		len(brief.AllowedTools) > 0 ||
		len(brief.ForbiddenTools) > 0 ||
		len(brief.RequiredEvidence) > 0 ||
		len(brief.ReadinessGate) > 0 ||
		len(brief.DiscoveryExamples) > 0 ||
		len(brief.TargetPacket) > 0 ||
		len(brief.VerificationPlan) > 0 ||
		NormalizeReadinessState(brief.Readiness) != nil
}

func recoveryStateSummary(reason string, tool string, target string, result workspace.ToolResult, evidencePaths []string) string {
	parts := []string{strings.TrimSpace(reason)}

	if tool != "" {
		if target != "" {
			parts = append(parts, "last tool: "+tool+" on "+target)
		} else {
			parts = append(parts, "last tool: "+tool)
		}
	}

	if summary := strings.TrimSpace(result.Summary); summary != "" {
		parts = append(parts, "summary: "+textutil.Truncate(summary, 180))
	}

	if errText := strings.TrimSpace(result.Error); errText != "" {
		parts = append(parts, "error: "+textutil.Truncate(errText, 220))
	}

	if len(evidencePaths) > 0 {
		parts = append(parts, "evidence: "+strings.Join(textutil.UniqueNonEmpty(evidencePaths), ", "))
	}

	return strings.Join(textutil.UniqueNonEmpty(parts), "; ")
}

func ToolRequestTarget(req workspace.ToolRequest) string {
	target := textutil.FirstNonEmpty(
		strings.TrimSpace(req.Path),
		strings.TrimSpace(req.ID),
		strings.TrimSpace(req.Destination),
		strings.TrimSpace(req.Pattern),
		strings.TrimSpace(req.Glob),
		strings.TrimSpace(req.Workdir),
		strings.TrimSpace(req.Agent),
		strings.TrimSpace(req.Command),
	)

	return textutil.Truncate(target, 180)
}

func combinedLower(parts ...string) string {
	return strings.ToLower(strings.Join(parts, "\n"))
}

// IsZeroHitSearchResult detects the "search found nothing" shape from a
// tool result's summary or stable routed-output preamble. Used to swap the next-action nudge from
// "read the highest-confidence hit" (useless when the hit list is empty)
// to "switch to a broader search or answer with what you have", and by
// the runner to block retries of the exact same zero-hit fingerprint.
// Summaries and routed output preambles are produced by the workspace layer
// and are stable strings; arbitrary source body output is intentionally not scanned.
func IsZeroHitSearchResult(result workspace.ToolResult) bool {
	if zeroHitText(strings.ToLower(strings.TrimSpace(result.Summary))) {
		return true
	}

	switch strings.TrimSpace(result.Tool) {
	case workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		for _, line := range strings.Split(result.Output, "\n") {
			line = strings.ToLower(strings.TrimSpace(line))
			if !strings.HasPrefix(line, "route_summary:") {
				continue
			}

			if zeroHitText(strings.TrimSpace(strings.TrimPrefix(line, "route_summary:"))) {
				return true
			}
		}
	}

	return false
}

func zeroHitText(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}

	return textutil.ContainsAny(text,
		"no file matches",
		"no content matches",
		"no matches",
		"no matching",
		"no go symbols matched",
		"no c++ symbols matched",
		"no symbols matched",
		"found 0 ",
	)
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}

	for _, existing := range values {
		if existing == value {
			return values
		}
	}

	return append(values, value)
}

func mergeStrings(base []string, extra []string) []string {
	out := append([]string(nil), base...)
	for _, value := range extra {
		out = appendUnique(out, value)
	}

	return out
}
