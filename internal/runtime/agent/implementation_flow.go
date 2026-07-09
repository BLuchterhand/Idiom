package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
)

// AnswerComposer is the presentation boundary between deterministic evidence
// packets and user-facing prose. Runner/discovery code can stay telemetry-rich;
// final answers should read like an explanation, not like an eval artifact.
type AnswerComposer struct{}

type AnswerComposerInput struct {
	Question      string
	EvidencePaths []string
	Summaries     []delegatedDiscoverySummary
}

const implementationTargetWriteReadySourceProbeToolsPhrase = "read_file/search_content/file_stats"

func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool) {
	question := strings.TrimSpace(input.Question)
	paths := uniqueDelegatedDiscoveryPaths(input.EvidencePaths)
	lines := decoratedDiscoveryFindingLines(input.Summaries, 4)

	if len(input.Summaries) == 0 || len(lines) == 0 {
		return "", false
	}

	return composeGenericEvidenceAnswer(question, paths, lines), true
}

func composeGenericEvidenceAnswer(question string, paths []string, lines []string) string {
	var b strings.Builder

	if len(paths) > 0 {
		b.WriteString("The strongest implementation evidence is in ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	} else {
		b.WriteString("The inspected implementation evidence is enough to give a bounded answer.")
	}

	if strings.TrimSpace(question) != "" {
		b.WriteString(" Use those findings as the source of truth for `")
		b.WriteString(strings.TrimSpace(question))
		b.WriteString("`.")
	}

	b.WriteString("\n\n")
	b.WriteString("What this establishes:\n")

	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func decoratedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	raw := delegatedDiscoveryFindingLines(summaries, limit)
	lines := make([]string, 0, len(raw))

	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		name, text, ok := strings.Cut(line, ":")
		if ok {
			line = humanDiscoveryLayerName(name) + ": " + cleanDiscoveryFindingText(text)
		} else {
			line = cleanDiscoveryFindingText(line)
		}

		if line == "" || strings.Contains(strings.ToLower(line), "open a returned handle") || decoratedDiscoveryFindingLineIsToolInventory(line) {
			continue
		}

		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			return lines
		}
	}

	return lines
}

func decoratedDiscoveryFindingLineIsToolInventory(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}

	if strings.HasPrefix(lower, "symbol index: indexed symbols ") ||
		strings.HasPrefix(lower, "symbol index: no ") ||
		strings.HasPrefix(lower, "dependency graph: dependency graph unavailable") {

		return true
	}

	if strings.HasPrefix(lower, "text anchors: found ") &&
		strings.Contains(lower, " matching line") {

		return true
	}

	if strings.HasPrefix(lower, "related files: found ") &&
		(strings.Contains(lower, " related ") || strings.Contains(lower, " file")) {

		return true
	}

	return false
}

func humanDiscoveryLayerName(name string) string {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return "Text anchors"
	case "dependency_graph":
		return "Dependency graph"
	case "related_files":
		return "Related files"
	case "go_read_symbol":
		return "Symbol read"
	case "go_workspace_symbol", "ctags_symbols":
		return "Symbol index"
	case "structural_search":
		return "Structural search"
	case "history_range", "history_symbol":
		return "History"
	case "blame_range":
		return "Blame"
	case "semgrep_scan":
		return "Static scan"
	case "joern_scan":
		return "Code property graph"
	default:
		return strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	}
}

func cleanDiscoveryFindingText(text string) string {
	text = strings.TrimSpace(text)
	for _, suffix := range []string{
		" Bounded-read capability is available on returned handles, or narrow the pattern to see more.",
		" Open a returned handle with read_file.",
		" Bounded-read capability is available on a returned handle to inspect a file.",
	} {
		text = strings.ReplaceAll(text, suffix, ".")
	}

	text = strings.ReplaceAll(text, "  ", " ")

	return strings.TrimSpace(text)
}

func formatInlinePathList(paths []string, max int) string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if max > 0 && len(paths) > max {
		paths = paths[:max]
	}

	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		path = cleanInlineEvidencePath(path)
		if path == "" {
			continue
		}

		parts = append(parts, "`"+path+"`")
	}

	return strings.Join(parts, ", ")
}

func cleanInlineEvidencePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "`", ""))
	path = strings.TrimRight(path, ".,;:")

	return strings.TrimSpace(path)
}

func routeSummaryFromToolOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "route_summary:"); ok {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func sourceLikePathsFromText(text string, max int) []string {
	if max <= 0 {
		max = 6
	}

	paths := make([]string, 0, max)
	seen := map[string]struct{}{}

	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || IsNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
			continue
		}

		if strings.Contains(path, "testdata/swe/generated-logs") {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		if len(paths) >= max {
			break
		}
	}

	return paths
}

func conversationFallbackEvidenceAnchor(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return ""
	}

	if routeSummary := routeSummaryFromToolOutput(result.Output); routeSummary != "" {
		return routeSummary
	}

	summary := strings.TrimSpace(result.Summary)
	if summary == "" || isHarnessRejectionSummary(summary) {
		return ""
	}

	switch strings.TrimSpace(result.Tool) {
	case "search_content":
		if paths := sourceLikePathsFromText(result.Output, 6); len(paths) > 0 {
			return "Search hits included " + formatInlinePathList(paths, 6)
		}

		return summary
	case "context_pack", "context_query", "context_zoom", "code_navigate", "read_file", workspace.ToolNamePrefetch, "search_files":
		record := newToolResultEvidenceRecord(result)
		if record.tool == workspace.ToolNameReadFile || record.tool == workspace.ToolNamePrefetch {
			paths := record.ReadPaths()
			if len(paths) > 0 && len(nonArtifactEvidencePaths(paths)) == 0 {
				return ""
			}
		}

		return summary
	default:
		return ""
	}
}

func isDeterministicFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	return strings.HasPrefix(lower, "partial findings from this turn")
}

func looksLikeSubstantiveFinalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	if len(answer) < 80 {
		return false
	}

	lower := strings.ToLower(answer)
	if strings.HasPrefix(lower, "for `") && strings.Contains(lower, "the gathered evidence points to these implementation surfaces") {
		return false
	}

	if strings.Contains(lower, "\nevidence:\n- text anchors:") {
		return false
	}

	return strings.Contains(answer, ".") || strings.Contains(answer, "\n- ")
}

func stripInternalEvidenceAppendix(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	cut := len(answer)
	for _, marker := range []string{
		"\n\nAdditional inspected paths:",
		"\nAdditional inspected paths:",
		"\n\nExact probes already inspected:",
		"\nExact probes already inspected:",
		"\n\nInspected paths:",
		"\nInspected paths:",
		"\n\nKey findings:",
		"\nKey findings:",
		"\n\nConfidence:",
		"\nConfidence:",
		"\n\nSource: ",
		"\nSource: ",
	} {
		if idx := strings.Index(answer, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}

	return strings.TrimSpace(answer[:cut])
}

func cleanedSubstantiveFinalAnswer(answer string) (string, bool) {
	cleaned := stripInternalEvidenceAppendix(answer)
	if looksLikeLowValueSourceInventoryNonAnswer(cleaned) || !looksLikeSubstantiveFinalAnswer(cleaned) || finalAnswerNeedsDecoration(cleaned) || looksLikeGenericEvidenceComposerAnswer(cleaned) {
		return "", false
	}

	return cleaned, true
}

func genericEvidenceAnswerIncludesLineOrientedSourceBody(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if !strings.Contains(lower, "what this establishes:") {
		return false
	}

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)

		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))

		if line == "" {
			continue
		}

		colon := strings.IndexByte(line, ':')
		if colon <= 0 || colon > 6 {
			continue
		}

		lineNumber := strings.TrimSpace(line[:colon])
		if lineNumber == "" {
			continue
		}

		allDigits := true

		for _, ch := range lineNumber {
			if ch < '0' || ch > '9' {
				allDigits = false
				break
			}
		}

		if allDigits {
			return true
		}
	}

	return false
}

func looksLikeSourceBodyFallbackAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))

	return strings.Contains(lower, "the inspected source in") &&
		strings.Contains(lower, "no files were changed")
}

func looksLikeGenericEvidenceComposerAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		strings.Contains(lower, "what this establishes:") ||
		strings.Contains(lower, "the retained source context identifies") ||
		looksLikeSourceBodyFallbackAnswer(answer)
}

func finalAnswerNeedsDecoration(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	if looksLikeRawToolResultTranscript(answer) {
		return true
	}

	if looksLikeLineOrientedSourceSnippetAnswer(answer) {
		return true
	}

	if IsTextualToolCall(answer) {
		return true
	}

	if looksLikeStructuredAnswerEnvelope(lower) {
		return true
	}

	if looksLikeLeakedImplementationBlockerTemplate(lower) {
		return true
	}

	if strings.Contains(lower, "<invoke") ||
		strings.HasPrefix(lower, "what i found:") ||
		strings.HasPrefix(lower, "partial findings from this turn") ||
		strings.HasPrefix(lower, "## grounded answer") ||
		strings.HasPrefix(lower, "question:") ||
		strings.Contains(lower, "source: delegated read-only evidence packet") ||
		strings.Contains(lower, "the gathered evidence points to these implementation surfaces") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "additional inspected paths:") ||
		strings.Contains(lower, "exact probes already inspected:") {

		return true
	}

	return ContainsAny(lower,
		"\nevidence:\n- text anchors:",
		"### evidence highlights",
		"### decision guidance",
		"discovery packet",
		"relevant source hits",
		"evidence used:",
		"inspected paths:",
		"key findings:",
		"text_anchor_search:",
		"go_read_symbol:",
		"go_workspace_symbol:",
		"dependency_graph:",
		"related_files:",
		".idiom/discovery/queries/",
	)
}

func bestObjectiveAnswerableTaskResultOutput(objective string, results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !ReadOnlyTaskResultAnswerableForObjective(result, objective) {
			continue
		}

		output := strings.TrimSpace(result.Output)
		if output == "" {
			continue
		}

		return output, true
	}

	return "", false
}

func presentableExactProbeResult(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(result.Tool)
	if workspace.IsLanguageSourceBodyTool(tool) {
		return true
	}

	switch tool {
	case "code_navigate", "read_only_capability":
		return routedSourceReadOutputHasBody(result.Output) &&
			len(newToolResultEvidenceRecord(result).SourceBodyPaths()) > 0
	default:
		return false
	}
}

func stripPresentableSourceObjectiveEcho(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "For `") {
		return trimmed
	}

	rest := strings.TrimPrefix(trimmed, "For `")
	end := strings.Index(rest, "`, ")

	if end < 0 {
		return trimmed
	}

	return strings.TrimSpace(rest[end+len("`, "):])
}

const (
	childLifecycleInvocationEventRole = "parent_child_invocation"
	childLifecycleTerminalEventRole   = "child_terminal"
	childLifecycleAcceptanceEventRole = "child_acceptance"
	childLifecyclePatchEventRole      = "candidate_patch_artifact"
)

type childLifecycleRecord interface {
	Normalize()
	Validate() error
}

type childLifecycleContext struct {
	InvocationID     string
	ParentContractID string
	ChildContractID  string
	Role             string
	Lane             string
	Phase            string
	WorkspaceMode    autonomy.ChildWorkspaceMode
	RuntimeEnvelope  autonomy.ChildRuntimeEnvelope
	CreatedAt        time.Time
}

func (r *Runner) beginReadOnlyChildLifecycle(agentName string, opts llama.ChatOptions, policy RunPolicy) childLifecycleContext {
	var client llama.LLMClient
	if r != nil {
		client = r.Client
	}
	return r.beginChildLifecycle("child_read_only:"+agentName, "child_read_only", autonomy.ChildWorkspaceNoWrite, opts, policy, false, client)
}

func (r *Runner) beginWorkbranchChildLifecycle(agentName string, opts llama.ChatOptions, policy RunPolicy, client llama.LLMClient) childLifecycleContext {
	return r.beginChildLifecycle("child_write:"+agentName, "child_write", autonomy.ChildWorkspaceWorkbranch, opts, policy, false, client)
}

func (r *Runner) beginDelegateEditLifecycle(req workspace.ToolRequest, opts llama.ChatOptions, deterministic bool, client llama.LLMClient) childLifecycleContext {
	role := "executor:" + textutil.FirstNonEmpty(strings.TrimSpace(req.Agent), "delegate_edit")
	return r.beginChildLifecycle(role, "delegate_edit", autonomy.ChildWorkspaceMain, opts, RunPolicy{}, deterministic, client)
}

func (r *Runner) beginChildLifecycle(role string, phase string, mode autonomy.ChildWorkspaceMode, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) childLifecycleContext {
	now := time.Now().UTC()
	brief := workerruntime.WorkerRuntimeBrief{}
	if r != nil {
		brief = r.RuntimeBrief
	}
	lane := textutil.FirstNonEmpty(brief.Lane, "runner")
	phase = textutil.FirstNonEmpty(phase, textutil.FirstNonEmpty(brief.TaskFamily, brief.Contract), "child")
	role = textutil.FirstNonEmpty(role, "child")
	parentContractID := r.childLifecycleParentContractID()
	childContractID := r.childLifecycleChildContractID(parentContractID, lane, phase, role, brief)
	invocationID := childLifecycleID("child_invocation", parentContractID, childContractID, lane, phase, role, now.Format(time.RFC3339Nano))
	envelope := r.childRuntimeEnvelope(role, opts, policy, deterministic, client)

	ctx := childLifecycleContext{
		InvocationID:     invocationID,
		ParentContractID: parentContractID,
		ChildContractID:  childContractID,
		Role:             role,
		Lane:             lane,
		Phase:            phase,
		WorkspaceMode:    mode,
		RuntimeEnvelope:  envelope,
		CreatedAt:        now,
	}

	record := autonomy.ParentChildInvocationRecord{
		InvocationID:     ctx.InvocationID,
		ParentContractID: ctx.ParentContractID,
		ChildContractID:  ctx.ChildContractID,
		Role:             ctx.Role,
		Lane:             ctx.Lane,
		Phase:            ctx.Phase,
		WorkspaceMode:    ctx.WorkspaceMode,
		ContractSummary: autonomy.ChildContractSummary{
			PathRoleDigest:   "sha256:" + childLifecycleHash(ctx.ChildContractID, "path_roles"),
			ToolPolicyDigest: "sha256:" + childLifecycleHash(ctx.ChildContractID, "tool_policy"),
			ObligationDigest: "sha256:" + childLifecycleHash(ctx.ChildContractID, "obligations"),
		},
		RuntimeEnvelope: ctx.RuntimeEnvelope,
		CreatedAt:       ctx.CreatedAt,
	}
	r.emitChildLifecycleRecord(childLifecycleInvocationEventRole, &record)

	return ctx
}

func (r *Runner) emitReadOnlyChildTerminal(ctx childLifecycleContext, result workspace.ToolResult, childResults []workspace.ToolResult, status autonomy.ChildTerminalStatus) {
	evidenceRefs := childLifecycleEvidenceRefs(childResults)
	if len(evidenceRefs) == 0 && strings.TrimSpace(result.Output) != "" {
		evidenceRefs = append(evidenceRefs, "tool_result:task")
	}

	record := autonomy.ChildTerminalRecord{
		TerminalID:         childLifecycleID("child_terminal", ctx.InvocationID, string(status)),
		InvocationID:       ctx.InvocationID,
		ChildContractID:    ctx.ChildContractID,
		ResultKind:         autonomy.ChildResultEvidencePacket,
		Status:             status,
		EvidenceRefs:       evidenceRefs,
		ObjectiveAlignment: childLifecycleObjectiveAlignmentForReadOnlyResult(result),
	}
	r.emitChildLifecycleRecord(childLifecycleTerminalEventRole, &record)
	r.emitChildTerminalAcceptance(ctx, record, childLifecycleAcceptanceDecisionForTerminal(record), childLifecycleAcceptanceReasonForTerminal(record))
}

func (r *Runner) emitWorkbranchChildTerminal(ctx childLifecycleContext, checkout Checkout, result workspace.ToolResult, diff Diff, status autonomy.ChildTerminalStatus) {
	patchID := childLifecycleID("candidate_patch", ctx.InvocationID, checkout.BaseSHA, strings.Join(diff.ChangedPaths, ","), childLifecycleHash(diff.Patch))
	var artifactRefs []string
	resultKind := autonomy.ChildResultCandidatePatch
	if len(diff.ChangedPaths) > 0 {
		artifactRefs = []string{"patch:" + patchID}
		artifact := autonomy.CandidatePatchArtifact{
			PatchID:         patchID,
			InvocationID:    ctx.InvocationID,
			ChildContractID: ctx.ChildContractID,
			BaseSHA:         textutil.FirstNonEmpty(checkout.BaseSHA, "unknown"),
			WorktreePathRef: "workbranch:" + childLifecycleHash(checkout.Path),
			ChangedPaths:    append([]string(nil), diff.ChangedPaths...),
			DiffStat:        childLifecycleDiffStat(diff),
			PatchPath:       "workbranch://" + childLifecycleHash(checkout.Branch, checkout.Path) + "/patch.diff",
			PatchDigest:     "sha256:" + childLifecycleHash(diff.Patch),
			CreatedAt:       time.Now().UTC(),
		}
		r.emitChildLifecycleRecord(childLifecyclePatchEventRole, &artifact)
	} else if status == autonomy.ChildTerminalCompleted {
		resultKind = autonomy.ChildResultFindings
	} else {
		resultKind = autonomy.ChildResultTypedBlocker
	}

	record := autonomy.ChildTerminalRecord{
		TerminalID:         childLifecycleID("child_terminal", ctx.InvocationID, string(status)),
		InvocationID:       ctx.InvocationID,
		ChildContractID:    ctx.ChildContractID,
		ResultKind:         resultKind,
		Status:             status,
		ArtifactRefs:       artifactRefs,
		ChangedPaths:       append([]string(nil), diff.ChangedPaths...),
		ObjectiveAlignment: childLifecycleObjectiveAlignmentForResult(result),
	}
	r.emitChildLifecycleRecord(childLifecycleTerminalEventRole, &record)
	r.emitChildTerminalAcceptance(ctx, record, childLifecycleAcceptanceDecisionForTerminal(record), childLifecycleAcceptanceReasonForTerminal(record))
}

func (r *Runner) emitDelegateEditLifecycle(ctx childLifecycleContext, req workspace.ToolRequest, result workspace.ToolResult, route autonomy.ChildExecutionRoute, reasonCode string) {
	changedPaths := newToolResultEvidenceRecord(result).ChangedPathList()
	status := autonomy.ChildTerminalCompleted
	if strings.TrimSpace(result.Error) != "" {
		status = autonomy.ChildTerminalFailed
	}

	terminalID := childLifecycleID("child_terminal", ctx.InvocationID, string(status), strings.Join(changedPaths, ","))
	metadata := autonomy.ChildExecutionMetadata{
		Route:                      route,
		ParentPlanAuthorityRef:     childLifecycleToolRef(req),
		PathGateRef:                childLifecyclePathGateRef(req),
		PathGateDecision:           autonomy.ChildPathGateAllow,
		VerificationObligationRefs: childLifecycleVerificationObligationRefs(r),
	}
	terminal := autonomy.ChildTerminalRecord{
		TerminalID:         terminalID,
		InvocationID:       ctx.InvocationID,
		ChildContractID:    ctx.ChildContractID,
		ResultKind:         autonomy.ChildResultAppliedParentEdit,
		Status:             status,
		ChangedPaths:       changedPaths,
		ExecutionMetadata:  metadata,
		ObjectiveAlignment: childLifecycleObjectiveAlignmentForResult(result),
		ParseStatus: autonomy.ChildParseStatus{
			TransportObserved: childLifecycleObservedTransport(route),
			NormalizerStatus:  autonomy.ChildNormalizerNotRequired,
			RawOutputRedacted: true,
		},
	}
	r.emitChildLifecycleRecord(childLifecycleTerminalEventRole, &terminal)

	acceptance := autonomy.ChildAcceptanceRecord{
		AcceptanceID:               childLifecycleID("child_acceptance", terminalID, string(route)),
		ParentContractID:           ctx.ParentContractID,
		ChildContractID:            ctx.ChildContractID,
		InvocationID:               ctx.InvocationID,
		TerminalID:                 terminalID,
		ResultKind:                 autonomy.ChildResultAppliedParentEdit,
		Decision:                   autonomy.ChildAcceptanceRecordedOnly,
		VerificationObligationRefs: append([]string(nil), metadata.VerificationObligationRefs...),
		ParentPlanAuthorityRef:     metadata.ParentPlanAuthorityRef,
		PathGateRef:                metadata.PathGateRef,
		PathGateDecision:           metadata.PathGateDecision,
		Route:                      route,
		ReasonCode:                 textutil.FirstNonEmpty(reasonCode, "delegate_edit_recorded"),
	}
	r.emitChildLifecycleRecord(childLifecycleAcceptanceEventRole, &acceptance)
}

func childLifecycleObservedTransport(route autonomy.ChildExecutionRoute) autonomy.ChildObservedTransport {
	switch route {
	case autonomy.ChildRouteDeterministicDirect:
		return autonomy.ChildObservedTransportDeterministic
	default:
		return autonomy.ChildObservedTransportTextual
	}
}

func childLifecycleDiffStat(diff Diff) autonomy.PatchDiffStat {
	return autonomy.PatchDiffStat{
		Files:      len(diff.ChangedPaths),
		Insertions: childLifecycleStatValue(diff.Stat, "insertions"),
		Deletions:  childLifecycleStatValue(diff.Stat, "deletions"),
	}
}

func childLifecycleStatValue(stat string, label string) int {
	fields := strings.FieldsFunc(stat, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t'
	})
	for _, field := range fields {
		parts := strings.Fields(strings.TrimSpace(field))
		if len(parts) == 2 && strings.EqualFold(parts[1], label) {
			var value int
			if _, err := fmt.Sscanf(parts[0], "%d", &value); err == nil && value >= 0 {
				return value
			}
		}
	}
	return 0
}

func (r *Runner) emitDelegateEditParseFailureLifecycle(ctx childLifecycleContext, req workspace.ToolRequest, parseErr error) {
	record := autonomy.ChildTerminalRecord{
		TerminalID:      childLifecycleID("child_terminal", ctx.InvocationID, "parse_failed"),
		InvocationID:    ctx.InvocationID,
		ChildContractID: ctx.ChildContractID,
		ResultKind:      autonomy.ChildResultAppliedParentEdit,
		Status:          autonomy.ChildTerminalParseFailed,
		ExecutionMetadata: autonomy.ChildExecutionMetadata{
			Route:                      autonomy.ChildRouteParentExecutor,
			ParentPlanAuthorityRef:     childLifecycleToolRef(req),
			PathGateRef:                childLifecyclePathGateRef(req),
			PathGateDecision:           autonomy.ChildPathGateAllow,
			VerificationObligationRefs: childLifecycleVerificationObligationRefs(r),
		},
		ObjectiveAlignment: autonomy.ChildObjectiveWeak,
		ParseStatus: autonomy.ChildParseStatus{
			TransportObserved:  autonomy.ChildObservedTransportTextual,
			NormalizerStatus:   autonomy.ChildNormalizerFailed,
			ParserFailureClass: childLifecycleFailureClass(parseErr),
			RawOutputRedacted:  true,
		},
	}
	r.emitChildLifecycleRecord(childLifecycleTerminalEventRole, &record)
	r.emitChildTerminalAcceptance(ctx, record, autonomy.ChildAcceptanceRepairRequired, "delegate_edit_parse_failed")
}

func (r *Runner) emitDelegateEditExecutorFailureLifecycle(ctx childLifecycleContext, err error) {
	record := autonomy.ChildTerminalRecord{
		TerminalID:         childLifecycleID("child_terminal", ctx.InvocationID, "executor_failed"),
		InvocationID:       ctx.InvocationID,
		ChildContractID:    ctx.ChildContractID,
		ResultKind:         autonomy.ChildResultTypedBlocker,
		Status:             autonomy.ChildTerminalFailed,
		ObjectiveAlignment: autonomy.ChildObjectiveWeak,
		EvidenceRefs:       []string{"executor_error:" + childLifecycleFailureClass(err)},
	}
	r.emitChildLifecycleRecord(childLifecycleTerminalEventRole, &record)
	r.emitChildTerminalAcceptance(ctx, record, autonomy.ChildAcceptanceBlocked, "delegate_edit_executor_failed")
}

func (r *Runner) emitChildTerminalAcceptance(ctx childLifecycleContext, terminal autonomy.ChildTerminalRecord, decision autonomy.ChildAcceptanceDecision, reasonCode string) {
	acceptedEvidenceRefs := []string(nil)
	acceptedPatchRefs := []string(nil)
	if decision == autonomy.ChildAcceptanceAccepted {
		acceptedEvidenceRefs = append([]string(nil), terminal.EvidenceRefs...)
		acceptedPatchRefs = append([]string(nil), terminal.ArtifactRefs...)
		if len(acceptedEvidenceRefs) == 0 && len(acceptedPatchRefs) == 0 {
			decision = autonomy.ChildAcceptanceRecordedOnly
			reasonCode = textutil.FirstNonEmpty(reasonCode, "accepted_refs_missing")
		}
	}

	acceptance := autonomy.ChildAcceptanceRecord{
		AcceptanceID:         childLifecycleID("child_acceptance", terminal.TerminalID, string(decision)),
		ParentContractID:     ctx.ParentContractID,
		ChildContractID:      ctx.ChildContractID,
		InvocationID:         ctx.InvocationID,
		TerminalID:           terminal.TerminalID,
		ResultKind:           terminal.ResultKind,
		Decision:             decision,
		PackID:               terminal.PackID,
		AcceptedEvidenceRefs: acceptedEvidenceRefs,
		AcceptedPatchRefs:    acceptedPatchRefs,
		RequiredRepairPaths:  append([]string(nil), terminal.ChangedPaths...),
		RuntimeEnvelopeRef:   "runtime_envelope:" + ctx.InvocationID,
		AlignmentBasis:       childLifecycleAlignmentBasis(ctx, terminal, acceptedEvidenceRefs, acceptedPatchRefs),
		ReasonCode:           textutil.FirstNonEmpty(reasonCode, "child_terminal_recorded"),
		PathGateDecision:     autonomy.ChildPathGateNotApplicable,
		Route:                autonomy.ChildRouteChildModel,
		ExecutionMetadataRef: "terminal_execution:" + terminal.TerminalID,
	}
	if terminal.ResultKind == autonomy.ChildResultAppliedParentEdit {
		acceptance.Route = terminal.ExecutionMetadata.Route
		acceptance.ParentPlanAuthorityRef = terminal.ExecutionMetadata.ParentPlanAuthorityRef
		acceptance.PathGateRef = terminal.ExecutionMetadata.PathGateRef
		acceptance.PathGateDecision = terminal.ExecutionMetadata.PathGateDecision
		acceptance.VerificationObligationRefs = append([]string(nil), terminal.ExecutionMetadata.VerificationObligationRefs...)
	}
	r.emitChildLifecycleRecord(childLifecycleAcceptanceEventRole, &acceptance)
}

func childLifecycleAcceptanceDecisionForTerminal(terminal autonomy.ChildTerminalRecord) autonomy.ChildAcceptanceDecision {
	if terminal.Status != autonomy.ChildTerminalCompleted {
		if terminal.Status == autonomy.ChildTerminalParseFailed {
			return autonomy.ChildAcceptanceRepairRequired
		}
		return autonomy.ChildAcceptanceBlocked
	}
	if terminal.ObjectiveAlignment != autonomy.ChildObjectiveAccepted {
		return autonomy.ChildAcceptanceRepairRequired
	}
	return autonomy.ChildAcceptanceAccepted
}

func childLifecycleAcceptanceReasonForTerminal(terminal autonomy.ChildTerminalRecord) string {
	if terminal.Status != autonomy.ChildTerminalCompleted {
		return "child_terminal_" + string(terminal.Status)
	}
	if terminal.ObjectiveAlignment != autonomy.ChildObjectiveAccepted {
		return "objective_alignment_" + string(terminal.ObjectiveAlignment)
	}
	return "objective_aligned"
}

func childLifecycleAlignmentBasis(ctx childLifecycleContext, terminal autonomy.ChildTerminalRecord, evidenceRefs []string, patchRefs []string) autonomy.ChildAlignmentBasis {
	sourceRefs := append([]string(nil), evidenceRefs...)
	sourceRefs = append(sourceRefs, patchRefs...)
	for _, path := range terminal.ChangedPaths {
		sourceRefs = append(sourceRefs, "changed_path:"+NormalizeRunnerPath(path))
	}

	return autonomy.ChildAlignmentBasis{
		DelegatedObjectiveRef:  "objective:" + childLifecycleHash(ctx.Role, ctx.Lane, ctx.Phase),
		EffectiveScopeRefs:     []string{"lane:" + childLifecycleHash(ctx.Lane), "phase:" + childLifecycleHash(ctx.Phase)},
		AcceptedSourceBodyRefs: childLifecycleCompactRefs(sourceRefs),
		Evaluator:              autonomy.AlignmentEvaluatorHostContract,
	}
}

func childLifecycleCompactRefs(values []string) []string {
	seen := map[string]struct{}{}
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
	return out
}

func (r *Runner) emitChildLifecycleRecord(role string, record childLifecycleRecord) bool {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(role) == "" || record == nil {
		return false
	}

	record.Normalize()
	if err := record.Validate(); err != nil {
		r.emitEvent(marshalRunEvent("child_lifecycle_record_invalid", map[string]string{
			"role":  role,
			"error": err.Error(),
		}))
		return false
	}

	r.emitEvent(marshalRunEvent(role, record))
	return true
}

func (r *Runner) childLifecycleParentContractID() string {
	if r != nil && r.activeAutonomy != nil {
		if contractID := strings.TrimSpace(r.activeAutonomy.contract.ContractID); contractID != "" {
			return contractID
		}
	}

	lane := ""
	objective := ""
	if r != nil {
		lane = r.RuntimeBrief.Lane
		objective = r.RuntimeBrief.Objective
	}
	return childLifecycleID("parent_contract", lane, objective)
}

func (r *Runner) childLifecycleChildContractID(parentContractID string, lane string, phase string, role string, brief workerruntime.WorkerRuntimeBrief) string {
	input := autonomy.ContractInput{
		ParentID: parentContractID,
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationChild,
			Lane:              lane,
			Phase:             phase,
			TaskFamily:        textutil.FirstNonEmpty(brief.TaskFamily, phase),
			RuntimeContract:   brief.Contract,
			PermissionProfile: brief.PermissionProfile,
		},
		Metadata: autonomy.DefaultContractMetadata("child_lifecycle", role),
	}
	if contract, err := autonomy.NewContract(input); err == nil && strings.TrimSpace(contract.ContractID) != "" {
		return contract.ContractID
	}

	return childLifecycleID("child_contract", parentContractID, lane, phase, role)
}

func (r *Runner) childRuntimeEnvelope(role string, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) autonomy.ChildRuntimeEnvelope {
	modelAlias := textutil.FirstNonEmpty(safeModelAliasForTelemetry(opts.Model), "default")
	backendKind := "llm_client"
	if deterministic {
		modelAlias = "deterministic"
		backendKind = "deterministic"
	}

	supportsNativeTools := false
	if !deterministic {
		supportsNativeTools = childLifecycleClientSupportsNativeTools(client)
	}
	transport := autonomy.ChildTransportTextual
	if deterministic {
		transport = autonomy.ChildTransportNone
	} else if supportsNativeTools && !childLifecycleRoleUsesTextualTransport(role) {
		transport = autonomy.ChildTransportNative
	}

	admissionState := autonomy.CapacityAdmissionAcquired
	reasonClass := ""
	if !deterministic && client == nil {
		admissionState = autonomy.CapacityAdmissionDegraded
		reasonClass = "client_not_bound"
	}

	return autonomy.ChildRuntimeEnvelope{
		BackendKind: backendKind,
		ModelAlias:  modelAlias,
		ModelRole:   textutil.FirstNonEmpty(role, "child"),
		RoleResolution: autonomy.ChildRoleResolution{
			RequestedRole: textutil.FirstNonEmpty(role, "child"),
			ResolvedRole:  textutil.FirstNonEmpty(role, "child"),
			Alias:         modelAlias,
			Source:        autonomy.RoleResolutionSourceRoleTag,
			OK:            true,
		},
		CapacityAdmission: autonomy.ChildCapacityAdmission{
			State:       admissionState,
			ModelAlias:  modelAlias,
			ReasonClass: reasonClass,
			Enforcement: autonomy.CapacityAdmissionOpportunistic,
		},
		ToolTransportMode:   transport,
		ParserTransportMode: autonomy.ChildTransportTextual,
		ParserContract:      "workspace_tool_request",
		ResponseFormat:      childLifecycleResponseFormat(opts),
		ToolChoice:          strings.TrimSpace(opts.ToolChoice),
		SupportsNativeTools: supportsNativeTools,
		ForceNativeTools:    opts.ForceNativeTools,
		MaxTokens:           opts.MaxTokens,
		ContextBudget: autonomy.ChildContextBudget{
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		},
	}
}

func childLifecycleClientSupportsNativeTools(client llama.LLMClient) bool {
	if client == nil {
		return false
	}
	return client.SupportsNativeTools()
}

func childLifecycleRoleUsesTextualTransport(role string) bool {
	return strings.HasPrefix(strings.TrimSpace(role), "executor:")
}

func childLifecycleEvidenceRefs(results []workspace.ToolResult) []string {
	refs := make([]string, 0, len(results))
	for i, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := textutil.FirstNonEmpty(strings.TrimSpace(result.Tool), "tool")
		refs = append(refs, fmt.Sprintf("tool_result:%s:%d", tool, i+1))
	}
	return refs
}

func childLifecycleObjectiveAlignmentForResult(result workspace.ToolResult) autonomy.ChildObjectiveAlignment {
	if strings.TrimSpace(result.Error) != "" {
		return autonomy.ChildObjectiveWeak
	}
	if fact, ok := readOnlyDelegatedEvidenceFactFromResult(result); ok && fact.HasPacket {
		packet := fact.Packet
		if len(packet.Gaps) > 0 {
			return autonomy.ChildObjectiveWeak
		}
		if !readOnlyEvidencePacketAnswerable(packet) {
			return autonomy.ChildObjectiveWeak
		}
		if ObjectiveAsksImplementationShape(packet.Objective) && !readOnlyEvidencePacketAnswerableForObjective(packet, packet.Objective) {
			return autonomy.ChildObjectiveWeak
		}
		switch strings.ToLower(strings.TrimSpace(packet.Confidence)) {
		case "high", "medium":
			return autonomy.ChildObjectiveAccepted
		default:
			return autonomy.ChildObjectiveWeak
		}
	}
	return autonomy.ChildObjectiveAccepted
}

func childLifecycleObjectiveAlignmentForReadOnlyResult(result workspace.ToolResult) autonomy.ChildObjectiveAlignment {
	if strings.TrimSpace(result.Error) != "" {
		return autonomy.ChildObjectiveWeak
	}
	fact, ok := readOnlyDelegatedEvidenceFactFromResult(result)
	if !ok || !fact.HasPacket {
		return autonomy.ChildObjectiveWeak
	}
	return childLifecycleObjectiveAlignmentForResult(result)
}

func childLifecycleVerificationObligationRefs(r *Runner) []string {
	if r == nil {
		return []string{"obligation:post_write_verification"}
	}
	for _, required := range r.RuntimeBrief.RequiredEvidence {
		if strings.Contains(strings.ToLower(required), "verification") {
			return []string{"required_evidence:" + required}
		}
	}
	return []string{"obligation:post_write_verification"}
}

func childLifecycleToolRef(req workspace.ToolRequest) string {
	return textutil.FirstNonEmpty("tool_request:"+strings.TrimSpace(req.Tool)+":"+NormalizeRunnerPath(req.Path), "tool_request:delegate_edit")
}

func childLifecyclePathGateRef(req workspace.ToolRequest) string {
	return textutil.FirstNonEmpty("path_gate:"+NormalizeRunnerPath(req.Path), "path_gate:delegate_edit")
}

func childLifecycleFailureClass(err error) string {
	if err == nil {
		return "unknown"
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "json"):
		return "invalid_json"
	case strings.Contains(text, "empty"):
		return "empty_output"
	case strings.Contains(text, "disallowed"):
		return "disallowed_tool"
	default:
		return "unparseable_output"
	}
}

func childLifecycleResponseFormat(opts llama.ChatOptions) string {
	if opts.ResponseFormatJSON {
		return "json"
	}
	return ""
}

func childLifecycleID(prefix string, parts ...string) string {
	return prefix + "_" + childLifecycleHash(parts...)
}

func childLifecycleHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

// DirectAnswerSurfaceSchemaVersion identifies the schema for the surface
// inventory JSON payload in telemetry events.
const DirectAnswerSurfaceSchemaVersion = "idiom.direct_answer_surface.v1"

// DirectAnswerSurfaceVersion identifies the current surface inventory version.
// Bump when tools, capabilities, or terminal states change. A1 compares this
// against its expected minimum version before engaging prefetch.
const DirectAnswerSurfaceVersion = "2026-06-12"

// DirectAnswerSurfaceCapReadOnlyInvestigation indicates the surface supports
// non-trivial read-only investigation (search followed by bounded source read).
const DirectAnswerSurfaceCapReadOnlyInvestigation = "read_only_investigation"

// DirectAnswerSurfaceCapSourceBodyPrefetch indicates the direct-answer surface
// can receive source-body evidence that was prefetched before the first model
// turn. The prefetch gate itself is owned by the read-only named-path evidence
// boundary, not by this direct-answer inventory.
const DirectAnswerSurfaceCapSourceBodyPrefetch = "source_body_prefetch"

// DirectAnswerSurfaceTerminalUncertaintyProse is the terminal state for
// normal uncertainty prose when evidence is insufficient.
const DirectAnswerSurfaceTerminalUncertaintyProse = "uncertainty_prose"

// DirectAnswerSurfaceTerminalAnswerableEvidence is the terminal state for
// evidence-sufficient answers.
const DirectAnswerSurfaceTerminalAnswerableEvidence = "answerable_evidence"

// DirectAnswerSurface is the typed inventory for the lightweight direct-answer
// surface. It defines exactly what the model-facing protocol projects in a
// direct-answer lane.
type DirectAnswerSurface struct {
	// SchemaVersion identifies the JSON schema for this payload.
	SchemaVersion string `json:"schema_version"`
	// Version is the surface inventory version string.
	Version string `json:"version"`
	// Capabilities lists the direct-answer capabilities this surface
	// advertises. Consumers (A1, telemetry) check this before engaging.
	Capabilities []string `json:"capabilities"`
	// ToolNames is the canonical set of tools visible on this surface.
	ToolNames []string `json:"tool_names"`
	// TerminalStates lists valid terminal outcomes for this surface.
	TerminalStates []string `json:"terminal_states"`
}

// DirectAnswerSurfaceForBrief returns the surface inventory when the brief
// identifies a direct-answer lane. Returns (inventory, true) for valid
// direct-answer lanes, (DirectAnswerSurface{}, false) otherwise.
//
// The returned surface includes read-only investigation tools and the
// session-scoped request_system_search_access approval tool. It excludes source
// writes, shell, submit_plan, and plan-execute executor surfaces. Non-trivial
// read-only investigation (search + bounded source read) remains available.
func DirectAnswerSurfaceForBrief(brief workerruntime.WorkerRuntimeBrief) (DirectAnswerSurface, bool) {
	if !readOnlyDirectAnswerLane(brief) {
		return DirectAnswerSurface{}, false
	}

	return DirectAnswerSurface{
		SchemaVersion: DirectAnswerSurfaceSchemaVersion,
		Version:       DirectAnswerSurfaceVersion,
		Capabilities:  directAnswerSurfaceCapabilities(),
		ToolNames:     workspace.DirectAnswerSurfaceToolNames(),
		TerminalStates: []string{
			DirectAnswerSurfaceTerminalAnswerableEvidence,
			DirectAnswerSurfaceTerminalUncertaintyProse,
		},
	}, true
}

// directAnswerSurfaceCapabilities returns the capabilities the current
// direct-answer surface advertises. This is the single source of truth;
// add new capabilities here as they are implemented.
func directAnswerSurfaceCapabilities() []string {
	return []string{
		DirectAnswerSurfaceCapReadOnlyInvestigation,
		DirectAnswerSurfaceCapSourceBodyPrefetch,
	}
}

// EmitDirectAnswerSurfaceEvent creates and emits a telemetry event with the
// surface inventory metadata. Called once per session when the surface is
// first activated. The runner passes emitEvent because it's the same package.
func EmitDirectAnswerSurfaceEvent(emitFn func(event RunEvent), surface DirectAnswerSurface) {
	if emitFn == nil {
		return
	}

	emitFn(marshalRunEvent("direct_answer_surface", map[string]any{
		"schema_version":  surface.SchemaVersion,
		"version":         surface.Version,
		"capabilities":    surface.Capabilities,
		"tool_count":      len(surface.ToolNames),
		"terminal_states": surface.TerminalStates,
	}))
}

func (r *Runner) evaluateFinalAnswerAcceptance(answer string, results []workspace.ToolResult) finalAnswerAcceptance {
	acceptance := newFinalAnswerAcceptance()
	if r != nil {
		acceptance.EvidencePaths = newToolResultEvidenceProjection(results).acceptedAnswerEvidencePaths(answer, r.RuntimeBrief)
	}

	var directAnswerRelevance readOnlyDirectAnswerRelevance

	if r != nil {
		directAnswerRelevance = r.readOnlyDirectAnswerRelevance(answer, results)
		acceptance = finalAnswerAcceptanceWithDirectAnswerRelevance(acceptance, directAnswerRelevance)
	}

	// Plan-then-execute planner phase: a successful submit_plan IS the
	// planner's deliverable; the executor phase (run by the orchestrator
	// after this runner exits) is what produces the actual file changes.
	if r != nil && r.planThenExecutePlannerPhase() && successfulSubmitPlanResultCount(results) > 0 {
		return finalAnswerAcceptanceWithPolicy(acceptance)
	}

	if r != nil && r.planThenExecutePlannerPhase() && r.planThenExecuteSourceSatisfiedAnswerCovers(answer, results) {
		return finalAnswerAcceptanceWithPolicy(acceptance)
	}

	if r != nil && r.planThenExecuteAnchorRefreshFinalAnswerPending(results) {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationPlanExecuteAnchorRefreshPending, turnReasonPlanExecuteAnchorRefreshPending)
	}

	if looksLikePriorAnswerReferenceFinalAnswer(answer) {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationProtocolMetaFinalAnswer, turnReasonNonSelfContainedFinalAnswer)
	}

	if looksLikeGateSatisfactionMetaFinalAnswer(answer) {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationProtocolMetaFinalAnswer, turnReasonGateSatisfactionMetaFinalAnswer)
	}

	if looksLikeEmbeddedQuestionProtocolFinalAnswer(answer) {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationProtocolMetaFinalAnswer, turnReasonEmbeddedQuestionProtocolMarker)
	}

	if looksLikeUnexecutedToolRequestAnswer(answer) {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationUnexecutedToolRequestAnswer, turnReasonUnexecutedToolRequestAnswer)
	}

	if r != nil && runnerBriefRequiresMutation(r.RuntimeBrief) {
		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet()); len(missing) > 0 {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEditPathsRemaining, turnReasonRequiredEditPathsRemaining)
		}

		if missing := unresolvedRejectedMutationPaths(results, r.RuntimeBrief.RequiredEditPaths); len(missing) > 0 {
			acceptance.EvidencePaths = textutil.Merge(acceptance.EvidencePaths, missing)
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRejectedMutationPathsRemaining, turnReasonRejectedMutationPathsRemaining)
		}
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		if r != nil && readOnlyDirectAnswerLane(r.RuntimeBrief) && !runnerBriefRequiresMutation(r.RuntimeBrief) {
			// Accept the blocker if the model has gathered source-body
			// evidence and the blocker names an exact path or claim.
			// This prevents the dead-end loop where the model can't
			// proceed and can't say "I'm stuck" either.
			if len(newToolResultEvidenceProjection(results).SourceBodyPaths()) > 0 && isSpecificQuestionBlocker(answer) {
				return rejectFinalAnswerAcceptance(acceptance, turnViolationUserVisibleBlocker, turnReasonUserVisibleBlocker)
			}
			return rejectFinalAnswerAcceptance(acceptance, turnViolationReadOnlyProtocolBlocker, turnReasonReadOnlyProtocolBlocker)
		}

		return rejectFinalAnswerAcceptance(acceptance, turnViolationUserVisibleBlocker, turnReasonUserVisibleBlocker)
	}

	if r != nil &&
		readOnlyAnswerReportsInsufficientEvidence(answer) &&
		readOnlyClosureDecisionForResults(r.RuntimeBrief, results).Answerable() {

		return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonAnswerableEvidenceInsufficient)
	}

	if r != nil &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) &&
		workerruntime.RuntimeContractIsArchitectureOrReview(r.RuntimeBrief.Contract) &&
		unsupportedReadOnlyCloseoutAnswer(answer) {

		return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonInsufficientEvidenceAnswer)
	}

	if looksLikeNonAnswerProse(answer) {
		// Leaked implementation blocker templates are protocol artifacts,
		// not valid answers — always reject them for user-facing agents
		// regardless of source inventory policy.
		if r != nil && r.userFacingFinalAnswer() && looksLikeLeakedImplementationBlockerTemplate(strings.ToLower(strings.TrimSpace(answer))) {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonLeakedImplementationBlocker)
		}

		if !finalAnswerAcceptanceAllowsObjectiveDiagnosticNonAnswer(acceptance, answer, r) {

			return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonNonAnswerProse)
		}
	}

	if r != nil &&
		runnerBriefRequiresMutation(r.RuntimeBrief) &&
		workerruntime.RuntimeContractMatches(r.RuntimeBrief.Contract, workerruntime.ContractImplementationResult) &&
		len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {

		return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
	}

	if r != nil {
		if reason := r.validationStatusAnswerRejectionReason(answer, results); reason != "" {
			return rejectFinalAnswerAcceptanceWithMessage(acceptance, turnViolationValidationStatusContradictsEvidence, turnReasonValidationStatusContradictsEvidence, reason)
		}
	}

	if r != nil {
		if assessment := r.assessSourceGrounding(answer, results); assessment.BlocksFinalAnswer() {
			if len(acceptance.EvidencePaths) == 0 {
				acceptance.EvidencePaths = closurePromptEvidencePaths(directAnswerRelevance.RelevantSourceBodyPaths, 8)
			}

			return rejectFinalAnswerAcceptanceWithSourceGrounding(acceptance, assessment)
		}
	}

	if r != nil {
		if runnerBriefRequiresMutation(r.RuntimeBrief) &&
			workerruntime.RuntimeContractMatches(r.RuntimeBrief.Contract, workerruntime.ContractImplementationResult) &&
			hasPendingVerificationAfterWrites(results, r.RuntimeBrief) {

			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}

		missing := missingRequiredEvidence(r.RuntimeBrief.RequiredEvidence, answer, results)
		if len(missing) > 0 {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}
	}

	return finalAnswerAcceptanceWithPolicy(acceptance)
}

func finalAnswerAcceptanceAllowsObjectiveDiagnosticNonAnswer(acceptance finalAnswerAcceptance, answer string, r *Runner) bool {
	return r != nil &&
		readOnlyDirectAnswerLane(r.RuntimeBrief) &&
		acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) &&
		readOnlyAnswerReportsInsufficientEvidence(answer)
}

type readOnlyDirectAnswerRelevance struct {
	Status                  string
	BlockerKind             string
	Reason                  string
	RelevantSourceBodyPaths []string
	AnswerObjectiveTerms    []string
}

func (r *Runner) readOnlyDirectAnswerRelevance(answer string, results []workspace.ToolResult) readOnlyDirectAnswerRelevance {
	if r == nil {
		return readOnlyDirectAnswerRelevance{}
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(r.RuntimeBrief), results).DirectAnswerRelevance(answer)
}

func readOnlyDirectAnswerAcceptanceApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return readOnlyDirectAnswerLane(brief)
}

func readOnlyDirectAnswerLane(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	return workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		readOnlyDirectAnswerTaskFamily(brief.TaskFamily) &&
		workerruntime.RuntimeContractIsDirectAnswer(brief.Contract)
}

func readOnlyDirectAnswerTaskFamily(family string) bool {
	return workerruntime.TaskFamilyIsConversation(family) ||
		workerruntime.TaskFamilyMatches(family, autonomy.TaskFamilyDirectAnswer)
}

// isSpecificQuestionBlocker reports whether a <<QUESTION>>: blocker message
// names an exact path, file, function, or claim rather than being a generic
// "I can't answer" message. Specific blockers should be accepted as
// user_visible_blocker even in read-only lanes.
func isSpecificQuestionBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if !strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return false
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:"))
	if body == "" {
		return false
	}
	lower := strings.ToLower(body)

	// Generic / hedge-word blockers: "i can't answer", "i don't know",
	// "i'm not sure", "i need more", "insufficient", "blocked"
	generic := []string{"i can't", "i cannot", "i don't know", "i'm not", "i am not",
		"i need more", "insufficient", "blocked", "not sure", "uncertain"}
	for _, phrase := range generic {
		if strings.Contains(lower, phrase) {
			return false
		}
	}

	// Check for code-like tokens: path separators, file extensions,
	// function names, type names, or quoted terms
	return strings.Contains(body, "/") ||
		strings.Contains(body, ".") ||
		strings.Contains(body, "`") ||
		strings.Contains(body, "\"") ||
		strings.Contains(body, "()") ||
		strings.Contains(body, "type ") ||
		strings.Contains(body, "func ")
}

func readOnlyDirectAnswerAcceptanceLedgerBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDirectAnswer) {
		brief.TaskFamily = string(autonomy.TaskFamilyConversation)
	}

	return brief
}

func readOnlyAcceptedDirectAnswerEvidencePaths(answer string, ledger readOnlyAnswerabilityLedger, results []workspace.ToolResult) []string {
	mentioned := readOnlySourceLikePaths(analyzeSourceCitations(answer).MentionedPaths, 16)
	relevant := ledger.RelevantSourceBodyPaths
	objectivePaths := ledger.SourceBodyObjectivePaths

	if ledger.directAnswerConversation() && ObjectiveAsksImplementationShape(ledger.Objective) {
		relevant = ledger.ImplementationOwnerPaths()
		objectivePaths = relevant
	}

	paths := intersectRunnerPaths(mentioned, relevant)

	paths = textutil.Merge(paths, objectivePaths, relevant)
	paths = textutil.Merge(paths, ledger.RelevantExternalDocumentPaths)
	paths = textutil.Merge(paths, answerCitedCollectiveSourceBodySupportPaths(answer, ledger))
	if ledger.planArtifactAnswerable() {
		paths = textutil.Merge(paths, acceptedFeaturePlannerPlanArtifactPaths(answer, ledger))
	}

	if len(paths) == 0 && ledger.DiagnosticRelevant {
		paths = textutil.Merge(paths, newToolResultEvidenceProjection(results).MentionedEvidencePathsForObjective("", 8))
	}

	return readOnlyEvidenceDisplayPaths(paths, 8)
}

func acceptedFeaturePlannerPlanArtifactPaths(answer string, ledger readOnlyAnswerabilityLedger) []string {
	mentioned := analyzeSourceCitations(answer).MentionedPaths
	if len(mentioned) == 0 {
		return ledger.PlanArtifactPaths
	}

	var paths []string
	ctx := featurePlannerPlanArtifactCitationContextForAnswer(answer, ledger.Objective, ledger.PlanArtifactPaths, true)
	for _, path := range readOnlySourceLikePaths(mentioned, 16) {
		authority := featurePlannerPlanArtifactPathCitationAuthority(answer, path, ctx)
		if authority.Kind == featurePlannerPlanCitationArtifactProposed {
			paths = textutil.Merge(paths, []string{authority.ResolvedPlanPath})
		}
	}

	if len(paths) == 0 && futurePlanRecommendationCitationsAreRelevant(answer, readOnlySourceLikePaths(mentioned, 16), ledger) {
		return ledger.PlanArtifactPaths
	}

	if len(paths) == 0 && answerMentionsFeaturePlannerPlan(answer) {
		return ledger.PlanArtifactPaths
	}

	return paths
}

func answerCitedCollectiveSourceBodySupportPaths(answer string, ledger readOnlyAnswerabilityLedger) []string {
	mentioned := analyzeSourceCitations(answer).MentionedPaths
	mentionedSet := readOnlyPathSet(mentioned)
	if len(mentionedSet) == 0 {
		return nil
	}

	if ledger.directAnswerConversation() && ObjectiveAsksImplementationShape(ledger.Objective) {
		return intersectRunnerPaths(ledger.ImplementationOwnerPaths(), mentioned)
	}

	sourceSet := readOnlyPathSet(ledger.SourceBodyPaths)
	requiredTerms := ledger.requiredSourceBodyObjectiveTerms()

	if len(requiredTerms) < 2 {
		return nil
	}

	termCovered := map[string]struct{}{}
	pathTermCount := map[string]int{}

	for _, support := range ledger.SourceBodyObjectiveTerms {
		path := NormalizeRunnerPath(support.Path)
		if path == "" {
			continue
		}

		if _, cited := mentionedSet[path]; !cited {
			continue
		}

		if _, read := sourceSet[path]; !read {
			continue
		}

		seenTermsForPath := map[string]struct{}{}

		for _, term := range support.Terms {
			term = ReadOnlyObjectiveSupportTerm(term)
			if term == "" {
				continue
			}

			termCovered[term] = struct{}{}
			if _, seen := seenTermsForPath[term]; seen {
				continue
			}

			seenTermsForPath[term] = struct{}{}
			pathTermCount[path]++
		}
	}

	if len(termCovered) < collectiveSourceBodyObjectiveTermThreshold(len(requiredTerms)) {
		return nil
	}

	var paths []string

	for path, count := range pathTermCount {
		if count < 2 {
			continue
		}

		paths = append(paths, path)
	}

	return closurePromptEvidencePaths(paths, 8)
}

func intersectRunnerPaths(left []string, right []string) []string {
	rightSet := readOnlyPathSet(right)
	if len(rightSet) == 0 {
		return nil
	}

	var out []string

	for _, raw := range left {
		path := NormalizeRunnerPath(raw)
		if path == "" {
			continue
		}

		if _, ok := rightSet[path]; ok {
			out = append(out, path)
		}
	}

	return uniqueSortedRunnerPaths(out)
}

func finalAnswerAcceptanceEventFields(acceptance finalAnswerAcceptance) map[string]any {
	fields := map[string]any{
		"acceptance_contract": acceptance.SchemaVersion,
		"acceptance_decision": string(acceptance.Decision),
	}
	if acceptance.PackID != "" {
		fields["policy_pack_id"] = acceptance.PackID
	}
	if acceptance.InvariantID != "" {
		fields["invariant_id"] = acceptance.InvariantID
	}
	if acceptance.PolicyDecision != "" {
		fields["policy_decision"] = acceptance.PolicyDecision
	}
	if acceptance.ParityReport.ContractID != "" {
		fields["parity_contract_id"] = acceptance.ParityReport.ContractID
	}
	if acceptance.ParityReport.ProtocolHash != "" {
		fields["parity_protocol_hash"] = acceptance.ParityReport.ProtocolHash
	}
	if acceptance.ParityReport.SurfaceID != "" {
		fields["parity_surface_id"] = acceptance.ParityReport.SurfaceID
	}
	if acceptance.ParityReport.InvariantID != "" {
		fields["parity_invariant_id"] = acceptance.ParityReport.InvariantID
	}
	if acceptance.ParityReport.OldDecision != "" {
		fields["parity_old_decision"] = acceptance.ParityReport.OldDecision
	}
	if acceptance.ParityReport.NewDecision != "" {
		fields["parity_new_decision"] = acceptance.ParityReport.NewDecision
	}
	fields["parity_diverged"] = acceptance.ParityReport.Diverged
	if acceptance.ObjectiveRelevanceStatus != "" {
		fields["objective_relevance"] = acceptance.ObjectiveRelevanceStatus
	}
	for key, value := range turnContractAssessmentEventFields(acceptance.ContractAssessment) {
		fields[key] = value
	}

	if len(acceptance.RelevantSourceBodyPaths) > 0 {
		fields["relevant_source_body_paths"] = textutil.LimitSlice(acceptance.RelevantSourceBodyPaths, 8)
	}

	return fields
}

func (r *Runner) planThenExecuteAnchorRefreshFinalAnswerPending(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	return state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0
}

func planThenExecuteAnchorRefreshFinalAnswerRejection(acceptance finalAnswerAcceptance) bool {
	return strings.TrimSpace(acceptance.BlockerKind) == string(turnViolationPlanExecuteAnchorRefreshPending)
}

type FinalAnswerDecorateInput struct {
	Answer     string
	StopReason string
	Brief      workerruntime.WorkerRuntimeBrief
	Results    []workspace.ToolResult
	UserFacing bool
}

func (r *Runner) decorateFinalAnswer(input FinalAnswerDecorateInput) string {
	answer := strings.TrimSpace(input.Answer)
	if answer == "" || !input.UserFacing {
		return answer
	}

	present := func(candidate string) string {
		return presentableFinalAnswerForUser(input.Brief, input.Results, candidate)
	}
	// Strip self-inflicted "Blocker:" prose when the harness has
	// objective evidence the contract is satisfied: all required edit
	// paths changed, planner owns verification, and the last failed
	// tool was a post-completion write probe (e.g., apply_patch with
	// stale context after the same file was already updated through
	// edit_file). The completion gate already accepted this answer —
	// the rewrite keeps downstream consumers from reading the model's
	// no-op blocker note as a run-level failure. Runs heading toward a
	// real <<QUESTION>> blocker still pass through untouched.
	if cleaned, ok := stripCompletedMutationSelfBlocker(input.Brief, input.Results, answer); ok {
		return present(cleaned)
	}

	if !strings.HasPrefix(answer, "<<QUESTION>>") && !mutationReplyReportsConcreteNoopOrBlocker(strings.ToLower(answer)) {
		if blocker, ok := canonicalMutationBlockerFinalAnswer(input.Brief, input.Results, answer); ok {
			return present(blocker)
		}
	}

	if looksLikeUnexecutedToolRequestAnswer(answer) || IsTextualToolCall(answer) {
		if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
			return present(presentable)
		}
	}

	if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) || looksLikeLineOrientedSourceSnippetAnswer(answer) {
		if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
			return present(composed)
		}

		if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
			if checked, ok := readOnlyClosureDecisionForResults(r.RuntimeBrief, input.Results).PresentableOrReadOnlyNonAnswerReplacement(r.agentName(), presentable); ok {
				return present(checked)
			}
		}

		if conversationFallbackShouldAvoidSourceBody(input.Brief) {
			if checked, ok := readOnlyClosureDecisionForResults(r.RuntimeBrief, input.Results).PresentableBriefConversationFallbackCandidate(r.agentName()); ok {
				return present(checked)
			}
		}
	}

	if finalAnswerStopReasonPreservesUncertainty(input.StopReason) &&
		!finalAnswerNeedsDecoration(answer) &&
		!looksLikeGenericEvidenceComposerAnswer(answer) {

		return present(answer)
	}

	if !finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer) {
		return present(answer)
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
		return present(cleaned)
	}

	if composed, ok := r.composedFinalAnswerFromEvidence(input); ok {
		return present(composed)
	}

	if presentable, ok := presentableNoToolsFallbackAnswer(input.Brief, input.Results, answer); ok {
		if checked, ok := readOnlyClosureDecisionForResults(r.RuntimeBrief, input.Results).PresentableOrReadOnlyNonAnswerReplacement(r.agentName(), presentable); ok {
			return present(checked)
		}
	}

	return present(answer)
}

func presentableFinalAnswerForUser(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) string {
	answer = strings.TrimSpace(answer)
	if presentable, ok := presentableMutationQuestionBlocker(brief, results, answer); ok {
		return presentable
	}

	if presentable, ok := presentableNonMutationQuestionBlocker(brief, results, answer); ok {
		return presentable
	}

	return answer
}

func presentableMutationQuestionBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, blocker string) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !runnerBriefRequiresMutation(brief) {
		return "", false
	}

	reason := strings.TrimSpace(blocker)
	if !strings.HasPrefix(reason, "<<QUESTION>>") {
		return "", false
	}

	reason = strings.TrimPrefix(reason, "<<QUESTION>>")
	reason = strings.TrimLeft(strings.TrimSpace(reason), ": ")
	lowerReason := strings.ToLower(reason)
	if reason == "" ||
		(!strings.Contains(lowerReason, "completion gate") &&
			!strings.Contains(lowerReason, "missing completion evidence") &&
			!strings.Contains(lowerReason, "required completion evidence")) ||
		!questionBlockerReasonLooksInternal(reason) {

		return "", false
	}

	presentable, ok := synthesizeNoToolsFallbackAnswer("main", brief, results)
	presentable = strings.TrimSpace(presentable)
	if !ok || presentable == "" || strings.Contains(strings.ToLower(presentable), "completion gate") {
		return "", false
	}

	return presentable, true
}

func presentableNonMutationQuestionBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, blocker string) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) ||
		workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) ||
		workerruntime.TaskFamilyIsPlanThenExecuteReview(brief.TaskFamily) {

		return "", false
	}

	reason := strings.TrimSpace(blocker)
	if !strings.HasPrefix(reason, "<<QUESTION>>") {
		return "", false
	}

	if answer, ok := deterministicNoToolsAnswer("main", brief, results); ok {
		answer = strings.TrimSpace(answer)
		if answer != "" &&
			!strings.HasPrefix(answer, "<<QUESTION>>") &&
			!readOnlyAnswerReportsInsufficientEvidence(answer) &&
			readOnlyClosureDecisionForResults(brief, results).SupportsPresentableQuestionBlockerAnswer(answer) {

			return answer, true
		}
	}

	reason = strings.TrimPrefix(reason, "<<QUESTION>>")
	reason = strings.TrimLeft(strings.TrimSpace(reason), ": ")

	gateReason := presentableCompletionGateQuestionBlockerReason(reason)
	internalReason := questionBlockerReasonLooksInternal(reason)

	reason = presentableQuestionBlockerReason(reason)

	if reason == "" {
		reason = "the run did not retain a supported answer."
	}

	if !internalReason && !questionBlockerReasonLooksInternal(reason) && !strings.Contains(reason, "\n") {
		return ensureTrailingPeriod(reason), true
	}

	evidenceLabel, paths := questionBlockerContextPaths(brief, results, 4)

	var b strings.Builder

	if gateReason != "" {
		b.WriteString(presentableUnsupportedFinalAnswerLead())
		b.WriteString(" ")
		b.WriteString(uppercaseFirstSentence(gateReason))
		if !strings.HasSuffix(strings.TrimSpace(b.String()), ".") {
			b.WriteString(".")
		}
		if len(paths) > 0 && !presentableGateReasonSuppressesContextPaths(gateReason) {
			b.WriteString(" ")
			b.WriteString(evidenceLabel)
			b.WriteString(": ")
			b.WriteString(formatInlinePathList(paths, 4))
			b.WriteString(".")
		}
		return strings.TrimSpace(b.String()), true
	}

	b.WriteString(presentableUnsupportedFinalAnswerLead())

	if reason != "" {
		b.WriteString(" The unresolved evidence gap was: ")
		b.WriteString(lowercaseFirstSentence(reason))

		if !strings.HasSuffix(strings.TrimSpace(b.String()), ".") {
			b.WriteString(".")
		}
	}

	if len(paths) > 0 {
		b.WriteString(" ")
		b.WriteString(evidenceLabel)
		b.WriteString(": ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	}

	return strings.TrimSpace(b.String()), true
}

func presentableGateReasonSuppressesContextPaths(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "run did not retain a supported answer from inspected evidence")
}

func questionBlockerContextPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) (string, []string) {
	if max <= 0 {
		max = 4
	}

	if readOnlyDirectAnswerLane(brief) {
		closeout := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)

		if paths := closurePromptEvidencePaths(closeout.Evidence.CandidatePaths, max); closeout.CandidateOnlyEvidence() && len(paths) > 0 {
			return "Candidate paths surfaced", paths
		}

		paths := textutil.Merge(closeout.Evidence.RelevantSourceBodyPaths, closeout.Evidence.SourceBodyObjectivePaths)
		if closeout.Evidence.DiagnosticRelevant {
			paths = textutil.Merge(paths, closeout.Evidence.DiagnosticPaths)
		}
		if paths = closurePromptEvidencePaths(paths, max); len(paths) > 0 {
			return "Inspected evidence", paths
		}

		if paths := closurePromptEvidencePaths(closeout.FinalAnswerDecision("").AcceptedEvidencePaths, max); len(paths) > 0 {
			return "Inspected evidence", paths
		}

		projection := newToolResultEvidenceProjection(results)
		if paths := projection.promptEvidencePaths(max); len(paths) > 0 {
			return "Inspected evidence", paths
		}

		return "", nil
	}

	projection := newToolResultEvidenceProjection(results)
	paths := closurePromptEvidencePaths(projection.acceptedAnswerEvidencePaths("", brief), max)
	if len(paths) == 0 {
		paths = projection.delegatedClosureEvidencePaths(brief.Objective, max)
	}

	if len(paths) == 0 {
		return "", nil
	}

	return "Inspected evidence", paths
}

func presentableQuestionBlockerReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if gateReason := presentableCompletionGateQuestionBlockerReason(reason); gateReason != "" {
		return gateReason
	}

	if questionBlockerReasonLooksInternal(reason) {
		if malformed := extractMalformedWriteBlockerReason(reason); malformed != "" {
			return malformed
		}

		return "the run did not retain enough supported evidence for the requested answer"
	}

	replacements := []struct {
		old string
		new string
	}{
		{"`<<QUESTION>>:`", "a blocker"},
		{"`<<QUESTION>>`", "a blocker"},
		{"<<QUESTION>>:", "a blocker"},
		{"<<QUESTION>>", "a blocker"},
	}
	for _, replacement := range replacements {
		reason = strings.ReplaceAll(reason, replacement.old, replacement.new)
	}

	return strings.TrimSpace(reason)
}

func presentableCompletionGateQuestionBlockerReason(reason string) string {
	reason = strings.TrimSpace(reason)
	lower := strings.ToLower(reason)
	if reason == "" || !strings.Contains(lower, "completion gate") {
		return ""
	}

	detail := reason
	if idx := strings.Index(strings.ToLower(detail), "missing completion evidence:"); idx >= 0 {
		detail = strings.TrimSpace(detail[idx+len("missing completion evidence:"):])
	} else if idx := strings.Index(detail, ":"); idx >= 0 {
		detail = strings.TrimSpace(detail[idx+1:])
	}

	detail = sourceGroundingStripGateRecoveryPrefix(detail)
	if sourceGroundingDetail := presentableSourceGroundingGateReason(detail); sourceGroundingDetail != "" {
		return sourceGroundingDetail
	}

	detail = firstSentenceBeforeAny(detail,
		" — repeated rejections indicate",
		" Either anchor that detail",
		" Either issue ONE concrete tool call",
		" Answer with the exact",
		" Read the first unread path",
		" Use one exact read",
		" If the path was",
		" state that the current evidence",
		" or use a blocker",
		" or use <<QUESTION>>",
		"\n\nEvidence paths observed before rejection:",
	)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	lowerDetail := strings.ToLower(detail)
	if strings.Contains(lowerDetail, "the final answer uses inspected source evidence") &&
		strings.Contains(lowerDetail, "does not establish") {

		return "the inspected evidence did not establish the requested claim"
	}
	if strings.Contains(lowerDetail, "the answer names additional implementation detail") ||
		strings.Contains(lowerDetail, "without an exact inspected probe") {

		return "the run did not retain a supported answer from inspected evidence"
	}

	return "the run did not retain a supported answer: " + lowercaseFirstSentence(detail)
}

func presentableSourceGroundingGateReason(detail string) string {
	detail = strings.TrimSpace(sourceGroundingStripGateRecoveryPrefix(detail))
	if detail == "" {
		return ""
	}
	lower := strings.ToLower(detail)
	if (strings.Contains(lower, "source-body evidence") || strings.Contains(lower, "source-body read")) &&
		(strings.Contains(lower, "candidate/search/context paths") ||
			strings.Contains(lower, "answer cites path") ||
			strings.Contains(lower, "not backed by a source-body read") ||
			strings.Contains(lower, "without an exact inspected probe")) {

		return "the run did not retain a supported answer from inspected evidence"
	}

	return ""
}

func firstSentenceBeforeAny(text string, markers ...string) string {
	end := len(text)
	lower := strings.ToLower(text)
	for _, marker := range markers {
		marker = strings.ToLower(marker)
		if marker == "" {
			continue
		}

		if idx := strings.Index(lower, marker); idx >= 0 && idx < end {
			end = idx
		}
	}

	return strings.TrimSpace(text[:end])
}

func questionBlockerReasonLooksInternal(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}

	return ContainsAny(lower,
		"completion gate",
		"missing completion evidence",
		"required next evidence",
		"answer with the exact",
		"current read-only answer has",
		"current read-only answer is",
		"current read-only answer uses",
		"no concrete user-facing finding was retained",
		"state the supported claim from the inspected source",
		"state the supported concrete finding from",
		"name the exact missing claim",
		"only localization evidence",
		"candidate paths but no relevant source-body evidence",
		"not backed by relevant source-body or diagnostic evidence",
		"missing source-body evidence for named path",
		"retained enough source-body evidence for a grounded answer",
		"read one objective-relevant source candidate",
		"gather one source-body read",
	)
}

func extractMalformedWriteBlockerReason(reason string) string {
	lower := strings.ToLower(reason)
	idx := strings.Index(lower, "blocked by malformed write request")

	if idx < 0 {
		return ""
	}

	end := idx + len("blocked by malformed write request")
	if end < len(reason) {
		if dot := strings.Index(reason[end:], "."); dot >= 0 {
			end += dot + 1
		}
	}

	return ensureTrailingPeriod(strings.TrimSpace(reason[idx:end]))
}

func ensureTrailingPeriod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}

	return s + "."
}

func canonicalMutationBlockerFinalAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !runnerBriefRequiresMutation(brief) {
		return "", false
	}

	// Plan-then-execute planner phase: a successful submit_plan IS the
	// planner's deliverable; the executor phase (run by the orchestrator
	// after this runner exits) produces the file changes. Without this
	// exemption the decorator rewrites the model's clean "plan
	// submitted" answer into a "Required edit paths remain unchanged"
	// blocker because the planner phase has no changed_paths yet, even
	// though the plan was submitted correctly.
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) && successfulSubmitPlanResultCount(results) > 0 {
		return "", false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)

	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(brief.TaskFamily) &&
		plancontract.SourceSatisfiedAnswerCoversPaths(answer, missing) &&
		planThenExecuteSourceSatisfiedResultsCoverPaths(missing, results) {

		return "", false
	}

	if len(missing) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Required edit path")

	if len(missing) > 1 {
		b.WriteString("s")
	}

	if len(missing) == 1 {
		b.WriteString(" remains unchanged: ")
	} else {
		b.WriteString(" remain unchanged: ")
	}

	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".")

	if len(changed) > 0 {
		b.WriteString(" Changed path")

		if len(changed) > 1 {
			b.WriteString("s")
		}

		b.WriteString(" recorded: ")
		b.WriteString(strings.Join(mapKeys(changed), ", "))
		b.WriteString(".")
	} else {
		b.WriteString(" No required workspace edit was completed.")
	}

	b.WriteString(" Provide current source anchors for the remaining path")

	if len(missing) > 1 {
		b.WriteString("s")
	}

	b.WriteString(", or use a bounded required-path read to refresh stale anchors before retrying.")

	return strings.TrimSpace(b.String()), true
}

func (r *Runner) composedFinalAnswerFromEvidence(input FinalAnswerDecorateInput) (string, bool) {
	if exact, ok := presentableExactProbeAnswer(input.Brief, input.Results); ok {
		return exact, true
	}

	delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(input.Brief, input.Results)
	if delegatedOwnerReady {
		if answer, ok := synthesizeReadOnlyDelegatedEvidenceAnswer(input.Brief.Objective, newToolResultEvidenceProjection(input.Results).ReadOnlyDelegatedEvidenceFacts()); ok {
			return answer, true
		}
	}

	if answer, ok := presentableProseEvidenceAnswer(input.Brief, input.Results); ok {
		return answer, true
	}

	if workerruntime.RuntimeContractIsArchitecture(input.Brief.Contract) {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok {
			if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) {
				return "", false
			}

			if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
				return cleaned, true
			}

			if !finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer) {
				return answer, true
			}
		}
	}

	if delegatedOwnerReady {
		if output, ok := bestObjectiveAnswerableTaskResultOutput(input.Brief.Objective, input.Results); ok {
			if cleaned, ok := cleanedSubstantiveFinalAnswer(output); ok {
				return cleaned, true
			}

			if !finalAnswerNeedsDecoration(output) && !looksLikeGenericEvidenceComposerAnswer(output) {
				return output, true
			}
		}
	}

	if delegatedOwnerReady {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(input.Brief.Objective, input.Results); ok {
			if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) {
				return "", false
			}

			if cleaned, ok := cleanedSubstantiveFinalAnswer(answer); ok {
				return cleaned, true
			}

			if !finalAnswerNeedsDecoration(answer) && !looksLikeGenericEvidenceComposerAnswer(answer) {
				return answer, true
			}
		}
	}

	return "", false
}

func delegatedImplementationShapeSynthesisAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !implementationShapeOwnerContractApplies(brief) {
		return true
	}

	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)

	return len(ledger.ImplementationOwnerPaths()) > 0
}

func implementationShapeOwnerContractApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	return readOnlyDirectAnswerLane(brief) && ObjectiveAsksImplementationShape(brief.Objective)
}

func presentableNoToolsFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, internalAnswer string) (string, bool) {
	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false
	}

	if !finalAnswerNeedsDecoration(internalAnswer) && !looksLikeGenericEvidenceComposerAnswer(internalAnswer) {
		return "", false
	}

	if answer, ok := presentableGroundedAnswerFromRenderedPacket(internalAnswer); ok {
		return answer, true
	}

	closure := readOnlyClosureDecisionForResults(brief, results)
	if looksLikeUnexecutedToolRequestAnswer(internalAnswer) || IsTextualToolCall(internalAnswer) {
		if exact, ok := presentableExactProbeAnswer(brief, results); ok {
			return exact, true
		}

		if externalDocument, ok := presentableExternalDocumentEvidenceAnswer(brief, results); ok {
			return externalDocument, true
		}

		if sourceBody, ok := presentableReadFileSourceAnswer(brief, results); ok {
			return sourceBody, true
		}

		if prose, ok := presentableProseEvidenceAnswer(brief, results); ok {
			return prose, true
		}
	}

	if readOnlyFallbackNeedsAnswerableEvidence(brief, internalAnswer) && !closure.Answerable() {
		return closure.UserFacingUncertainty(), true
	}

	if conversationFallbackShouldAvoidSourceBody(brief) && isDeterministicFallbackAnswer(internalAnswer) {
		if concrete, ok := closure.ConcreteFallbackAnswer(); ok {
			return concrete, true
		}

		return closure.ConversationFallback(), true
	}

	if conversationFallbackShouldAvoidSourceBody(brief) && looksLikeSourceBodyFallbackAnswer(internalAnswer) {
		return closure.ConversationFallback(), true
	}

	lineOrientedGenericAnswer := genericEvidenceAnswerIncludesLineOrientedSourceBody(internalAnswer) ||
		looksLikeLineOrientedSourceSnippetAnswer(internalAnswer)
	if !lineOrientedGenericAnswer {
		if cleaned, ok := cleanedSubstantiveFinalAnswer(internalAnswer); ok {
			return cleaned, true
		}
	}

	// For non-mutation turns where the model produced a genuine substantive
	// answer, prefer it over source-body fact extraction. Source-body extraction
	// from read_file results produces nonsense like "The inspected source
	// contains `"strings"`, `)` and `)`. No files were changed." for
	// conversation questions. But only apply this when the answer doesn't look
	// like a budget-closure or post-readiness stub — those need composition.
	if !lineOrientedGenericAnswer &&
		!finalAnswerNeedsDecoration(internalAnswer) &&
		!looksLikeGenericEvidenceComposerAnswer(internalAnswer) &&
		!runnerBriefRequiresMutation(brief) &&
		looksLikeSubstantiveFinalAnswer(internalAnswer) {

		lower := strings.ToLower(internalAnswer)
		if !strings.Contains(lower, "partial findings from this turn") &&
			!strings.Contains(lower, "budget was reached") &&
			!strings.Contains(lower, "post-readiness") {

			substantive := stripInternalEvidenceAppendix(internalAnswer)
			if !finalAnswerNeedsDecoration(substantive) && len(strings.TrimSpace(substantive)) >= 120 {
				return substantive, true
			}
		}
	}

	if exact, ok := presentableExactProbeAnswer(brief, results); ok {
		return exact, true
	}

	if externalDocument, ok := presentableExternalDocumentEvidenceAnswer(brief, results); ok {
		return externalDocument, true
	}

	// When the internal answer is a deterministic fallback ("Partial findings
	// from this turn...") and we are in a read-only conversation lane, skip
	// source-body extraction. It produces nonsense like "The inspected source
	// contains `type Evidence struct {`..." that does not answer the question.
	// Only block for direct user-facing conversation lanes, not for delegated
	// read-only investigation lanes that benefit from source-body composition.
	if isDeterministicFallbackAnswer(internalAnswer) && conversationFallbackShouldAvoidSourceBody(brief) {
		return closure.ConversationFallback(), true
	}

	if sourceBody, ok := presentableReadFileSourceAnswer(brief, results); ok {
		return sourceBody, true
	}

	if prose, ok := presentableProseEvidenceAnswer(brief, results); ok {
		return prose, true
	}

	projection := newToolResultEvidenceProjection(results)
	paths := projection.delegatedClosureEvidencePaths(brief.Objective, 4)
	if len(paths) == 0 {
		paths = projection.postChildClosureEvidencePaths(brief.Objective, 4)
	}

	if len(paths) == 0 {
		paths = projection.promptEvidencePaths(4)
	}

	paths = closurePromptEvidencePaths(paths, 4)

	findings := presentableFindingsFromInternalAnswer(internalAnswer, 2)
	if len(findings) > 0 {
		var b strings.Builder

		b.WriteString(strings.Join(findings, " "))

		if len(paths) > 0 {
			b.WriteString(" The relevant source files are ")
			b.WriteString(formatInlinePathList(paths, 4))
			b.WriteString(".")
		}

		return strings.TrimSpace(b.String()), true
	}

	if len(paths) == 0 {
		return "", false
	}

	if !closure.Answerable() {
		return closure.UserFacingUncertainty(), true
	}

	return presentableUnsupportedFinalAnswerLead() + " The current read-only answer has relevant evidence in " +
		formatInlinePathList(paths, 4) +
		", but no concrete user-facing finding was retained.", true
}

func presentableGroundedAnswerFromRenderedPacket(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	lower := strings.ToLower(answer)
	marker := "grounded answer:\n"
	idx := strings.Index(lower, marker)

	if idx < 0 {
		return "", false
	}

	rest := strings.TrimSpace(answer[idx+len(marker):])
	if rest == "" {
		return "", false
	}

	cut := len(rest)
	for _, section := range []string{
		"\nGrounded findings:",
		"\nSource body paths:",
		"\nCandidate paths:",
		"\nConfidence:",
	} {
		if next := strings.Index(rest, section); next >= 0 && next < cut {
			cut = next
		}
	}

	grounded := strings.TrimSpace(rest[:cut])
	if grounded == "" ||
		finalAnswerNeedsDecoration(grounded) ||
		looksLikeGenericEvidenceComposerAnswer(grounded) ||
		looksLikeLowValueSourceInventoryNonAnswer(grounded) {

		return "", false
	}

	return grounded, true
}

type externalDocumentEvidenceAnswerPart struct {
	path  string
	lines []string
}

func presentableExternalDocumentEvidenceAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || briefPrefersDelegatedPacketFallback(brief, results) {
		return "", false
	}

	objective := strings.TrimSpace(delegatedDisplayQuestion(brief.Objective))
	if objective == "" {
		return "", false
	}

	var parts []externalDocumentEvidenceAnswerPart
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		record := newToolResultEvidenceRecord(result)
		if record.tool != workspace.ToolNameReadFile && record.tool != workspace.ToolNamePrefetch {
			continue
		}

		lines := objectiveRelevantExternalDocumentEvidenceLines(objective, result.Output, 2)
		if len(lines) == 0 {
			continue
		}

		for _, observation := range record.ReadOnlyEvidenceObservations() {
			if observation.Kind != readOnlyEvidenceExternalDocument {
				continue
			}

			parts = append(parts, externalDocumentEvidenceAnswerPart{
				path:  observation.Path,
				lines: lines,
			})

			break
		}
	}

	if len(parts) == 0 {
		return "", false
	}

	if len(parts) > 4 {
		parts = parts[:4]
	}

	var b strings.Builder
	b.WriteString("For `")
	b.WriteString(objective)
	b.WriteString("`, the inspected external documents support this answer: ")

	for i, part := range parts {
		if i > 0 {
			b.WriteString(" ")
		}

		b.WriteString("`")
		b.WriteString(part.path)
		b.WriteString("` says ")
		b.WriteString(joinExternalDocumentEvidenceLines(part.lines))
	}

	return strings.TrimSpace(b.String()), true
}

func objectiveRelevantExternalDocumentEvidenceLines(objective string, output string, max int) []string {
	if max <= 0 {
		max = 2
	}

	rawLines := objectiveRelevantProseEvidenceLines(objective, output, max*2)
	if len(rawLines) == 0 {
		return nil
	}

	var lines []string
	for _, raw := range rawLines {
		line := externalDocumentEvidenceLineText(raw)
		if line == "" || slices.Contains(lines, line) {
			continue
		}

		lines = append(lines, line)
		if len(lines) >= max {
			break
		}
	}

	return lines
}

func externalDocumentEvidenceLineText(line string) string {
	line = normalizeProseEvidenceLine(line)
	if line == "" {
		return ""
	}

	lhs, rhs, ok := strings.Cut(line, "=")
	if !ok {
		return line
	}

	key := strings.TrimSpace(lhs)
	if key == "" || strings.ContainsAny(key, " \t/\\") {
		return line
	}

	value := strings.TrimSpace(rhs)
	value = strings.TrimSuffix(value, ";")
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}

	if len(value) < 16 {
		return line
	}

	return value
}

func joinExternalDocumentEvidenceLines(lines []string) string {
	var parts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimRight(line, ".!?")
		if line == "" {
			continue
		}

		parts = append(parts, line)
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ". ") + "."
}

func presentableReadFileSourceAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || briefPrefersDelegatedPacketFallback(brief, results) {
		return "", false
	}

	var ownerPathSet map[string]struct{}

	if implementationShapeOwnerContractApplies(brief) {
		ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)

		ownerPathSet = readOnlyPathSet(ledger.ImplementationOwnerPaths())

		if len(ownerPathSet) == 0 {
			return "", false
		}
	}

	bestAnswer := ""
	bestScore := -1

	for _, body := range combinedReadFileSourceBodies(results) {
		if answer, ok := presentableSourceBodyFactsAnswer(brief.Objective, body.path, body.output); ok {
			if ownerPathSet != nil {
				if _, ok := ownerPathSet[NormalizeRunnerPath(body.path)]; !ok {
					continue
				}
			}

			if implementationShape, ok := presentableImplementationShapeAnswerFromClaim(brief.Objective, body.path, "", stripPresentableSourceObjectiveEcho(answer)); ok {
				return implementationShape, true
			}

			score := presentableSourceAnswerObjectiveScore(answer, brief.Objective, body.path)
			if looksLikeSourceEchoOnlyAnswer(answer) && looksLikeLowValueSourceInventoryFallback(answer) {
				continue
			}

			if bestAnswer == "" || score > bestScore {
				bestScore = score
				bestAnswer = answer
			}
		}
	}

	return bestAnswer, strings.TrimSpace(bestAnswer) != ""
}

type readFileSourceBody struct {
	path   string
	output string
}

func combinedReadFileSourceBodies(results []workspace.ToolResult) []readFileSourceBody {
	type aggregate struct {
		path      string
		outputs   []string
		lastIndex int
	}

	byPath := make(map[string]*aggregate)
	order := make([]string, 0, len(results))

	for i, result := range results {
		record := newToolResultEvidenceRecord(result)
		if strings.TrimSpace(result.Error) != "" || (record.tool != workspace.ToolNameReadFile && record.tool != workspace.ToolNamePrefetch) {

			continue
		}

		paths := record.SourceBodyPaths()
		if len(paths) == 0 {
			continue
		}

		path := NormalizeRunnerPath(paths[0])
		if path == "" {
			continue
		}

		agg := byPath[path]
		if agg == nil {
			agg = &aggregate{path: path}
			byPath[path] = agg
			order = append(order, path)
		}

		agg.outputs = append(agg.outputs, strings.TrimSpace(result.Output))
		agg.lastIndex = i
	}

	bodies := make([]readFileSourceBody, 0, len(order))
	for _, path := range order {
		agg := byPath[path]
		if agg == nil {
			continue
		}

		bodies = append(bodies, readFileSourceBody{
			path:   agg.path,
			output: strings.Join(agg.outputs, "\n"),
		})
	}

	slices.SortFunc(bodies, func(a, B readFileSourceBody) int {
		return byPath[B.path].lastIndex - byPath[a.path].lastIndex
	})

	return bodies
}

func presentableSourceAnswerObjectiveScore(answer string, objective string, path string) int {
	claim := stripPresentableSourceObjectiveEcho(answer)
	score := sourceEvidenceLineObjectiveScore(claim, sourceEvidenceObjectiveLineTerms(objective))

	score += len(answerObjectiveTerms(claim, objective)) * 2
	score += sourceEvidenceLineObjectiveScore(path, sourceEvidenceObjectiveLineTerms(objective)) * 3

	lowerClaim := strings.ToLower(claim)
	lowerObjective := strings.ToLower(objective)

	if ContainsAny(lowerObjective, "redact", "secret", "debug log") && strings.Contains(lowerClaim, "metadata") {
		score += 3
	}

	if ContainsAny(lowerObjective, "redact", "secret", "debug log") && strings.Contains(lowerClaim, "path") {
		score += 2
	}

	if strings.Contains(strings.ToLower(claim), "defines `") {
		score--
	}

	return score
}

func presentableProseEvidenceAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || briefPrefersDelegatedPacketFallback(brief, results) {
		return "", false
	}

	objective := strings.TrimSpace(delegatedDisplayQuestion(brief.Objective))
	if objective == "" {
		return "", false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		record := newToolResultEvidenceRecord(result)
		if record.tool != workspace.ToolNameReadFile && record.tool != workspace.ToolNamePrefetch {
			continue
		}

		paths := record.ReadPaths()
		if len(paths) == 0 {
			continue
		}

		path := strings.TrimSpace(paths[0])
		if !presentableProseEvidencePath(path) {
			continue
		}

		lines := objectiveRelevantProseEvidenceLines(objective, result.Output, 4)
		if len(lines) == 0 {
			continue
		}

		return synthesizeProseEvidenceAnswer(objective, path, lines), true
	}

	return "", false
}

func presentableProseEvidencePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))

	lower = strings.TrimPrefix(lower, "./")

	if lower == "" || IsNonSourceEvidenceArtifactPath(lower) {
		return false
	}

	if strings.HasPrefix(lower, "docs/") || strings.HasPrefix(lower, ".codex/") {
		return true
	}

	if lower == "readme" || strings.HasPrefix(lower, "readme.") {
		return true
	}

	for _, suffix := range []string{".md", ".markdown", ".txt", ".rst", ".adoc"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

type proseEvidenceScoredLine struct {
	line  string
	score int
	index int
}

func objectiveRelevantProseEvidenceLines(objective string, output string, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := RecoveryObjectiveSearchWords(objective, 32)
	if len(terms) == 0 {
		return nil
	}

	termSet := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		termSet[SingularObjectiveWord(term)] = struct{}{}
	}

	var candidates []proseEvidenceScoredLine

	for index, raw := range strings.Split(output, "\n") {
		line := normalizeProseEvidenceLine(raw)
		if line == "" {
			continue
		}

		score := proseEvidenceLineObjectiveScore(line, termSet)
		if score <= 0 {
			continue
		}

		candidates = append(candidates, proseEvidenceScoredLine{line: line, score: score, index: index})
	}

	if len(candidates) == 0 {
		return nil
	}

	slices.SortFunc(candidates, func(a, B proseEvidenceScoredLine) int {
		if a.score != B.score {
			return B.score - a.score
		}

		return a.index - B.index
	})

	var selected []proseEvidenceScoredLine

	for _, candidate := range candidates {
		if proseEvidenceLineDuplicates(selected, candidate.line) {
			continue
		}

		selected = append(selected, candidate)
		if len(selected) >= max {
			break
		}
	}

	slices.SortFunc(selected, func(a, B proseEvidenceScoredLine) int {
		return a.index - B.index
	})

	lines := make([]string, 0, len(selected))
	for _, candidate := range selected {
		lines = append(lines, candidate.line)
	}

	return lines
}

func normalizeProseEvidenceLine(line string) string {
	line = stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
	if line == "" {
		return ""
	}

	if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "|") {
		return ""
	}

	line = strings.TrimLeft(line, "#")
	line = strings.TrimSpace(line)

	for {
		trimmed := strings.TrimSpace(line)
		next := strings.TrimPrefix(trimmed, "- ")

		next = strings.TrimPrefix(next, "* ")
		next = strings.TrimPrefix(next, "+ ")
		next = strings.TrimPrefix(next, "> ")

		if next == trimmed {
			break
		}

		line = next
	}

	line = strings.TrimSpace(line)
	if line == "" || len(line) < 24 {
		return ""
	}

	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "package ") ||
		strings.HasPrefix(lower, "import ") ||
		strings.HasPrefix(lower, "func ") ||
		strings.HasPrefix(lower, "type ") ||
		strings.HasPrefix(lower, "const ") ||
		strings.HasPrefix(lower, "var ") ||
		strings.HasPrefix(lower, "return ") {

		return ""
	}

	if len(line) > 220 {
		line = strings.TrimSpace(line[:220])
	}

	return strings.Trim(line, "`*_ ")
}

func proseEvidenceLineObjectiveScore(line string, termSet map[string]struct{}) int {
	lower := strings.ToLower(line)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	score := 0
	seen := map[string]struct{}{}

	for _, word := range words {
		word = SingularObjectiveWord(word)
		if len(word) < 4 {
			continue
		}

		if _, ok := termSet[word]; ok {
			if _, dup := seen[word]; !dup {
				score += 2

				seen[word] = struct{}{}
			}
		}
	}

	if score > 0 && ContainsAny(lower, "evidence", "contract", "source", "repo", "repository", "audit", "eval", "verification", "runtime brief", "harness") {
		score++
	}

	return score
}

func proseEvidenceLineDuplicates(selected []proseEvidenceScoredLine, line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	for _, candidate := range selected {
		existing := strings.ToLower(strings.TrimSpace(candidate.line))
		if existing == normalized || strings.Contains(existing, normalized) || strings.Contains(normalized, existing) {
			return true
		}
	}

	return false
}

func synthesizeProseEvidenceAnswer(objective string, path string, lines []string) string {
	var b strings.Builder

	b.WriteString("For `")
	b.WriteString(strings.TrimSpace(objective))
	b.WriteString("`, `")
	b.WriteString(strings.TrimSpace(path))
	b.WriteString("` provides the grounded answer: ")

	for i, line := range lines {
		if i > 0 {
			b.WriteString(" ")
		}

		b.WriteString(strings.TrimRight(strings.TrimSpace(line), "."))
		b.WriteString(".")
	}

	return strings.TrimSpace(b.String())
}

func briefPrefersDelegatedPacketFallback(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if slices.Contains(brief.Constraints, "child_packet_available") {
		return true
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "task" && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func presentableSourceBodyFactsAnswer(objective string, path string, output string) (string, bool) {
	path = strings.TrimSpace(path)

	lines := sourceEvidenceAnswerLinesForObjective(output, objective, 24)

	if path == "" || len(lines) == 0 {
		return "", false
	}

	facts := sourceBodySemanticFacts(lines, objective, 5)
	if len(facts) == 0 {
		return "", false
	}

	var b strings.Builder

	if prefix := presentableSourceBodyObjectivePrefix(objective, lines); prefix != "" {
		b.WriteString(prefix)
		b.WriteString(" ")
	}

	if objective = strings.TrimSpace(objective); objective != "" {
		b.WriteString("For `")
		b.WriteString(objective)
		b.WriteString("`, the answer is grounded in ")
	}

	b.WriteString("`")
	b.WriteString(path)

	if objective != "" {
		b.WriteString("`: it shows ")
	} else {
		b.WriteString("` shows ")
	}

	factText := joinFallbackFacts(facts)
	b.WriteString(factText)
	b.WriteString(".")

	answer := strings.TrimSpace(b.String())

	if objectiveAsksBehaviorSummary(objective) && looksLikeLowValueSourceInventoryFallback(factText) {
		return "", false
	}

	return answer, true
}

func sourceBodySemanticFacts(lines []string, objective string, max int) []string {
	if max <= 0 {
		max = 3
	}

	var (
		behaviorFacts   []string
		definitionFacts []string
	)

	addTo := func(dst *[]string, fact string) {
		fact = strings.TrimSpace(fact)
		if fact == "" || slices.Contains(*dst, fact) {
			return
		}

		*dst = append(*dst, fact)
	}
	currentFunction := ""

	for _, line := range lines {
		functionLine := false

		if name := sourceBodyFunctionName(line); name != "" {
			currentFunction = name
			addTo(&definitionFacts, "defines `"+name+"`")

			functionLine = true
		}

		if fact := sourceBodyObjectiveLiteralFact(currentFunction, line, objective); fact != "" {
			addTo(&behaviorFacts, fact)
		}

		if !functionLine {
			if fact := sourceBodyReturnFact(currentFunction, line); fact != "" {
				addTo(&behaviorFacts, fact)
			} else if fact := sourceBodyDefinitionFact(line); fact != "" {
				addTo(&definitionFacts, fact)
			} else if fact := sourceBodyCaseFact(currentFunction, line); fact != "" {
				addTo(&behaviorFacts, fact)
			} else if fact := sourceBodyRecordAssignmentFact(line); fact != "" {
				addTo(&behaviorFacts, fact)
			} else if fact := sourceBodyStructFieldFact(line); fact != "" {
				addTo(&behaviorFacts, fact)
			} else if fact := exactProbeAssignmentObservation(line); fact != "" {
				addTo(&behaviorFacts, fact)
			} else if fact := sourceBodyCallFact(currentFunction, line); fact != "" {
				addTo(&behaviorFacts, fact)
			}
		}
	}

	facts := make([]string, 0, max)
	add := func(fact string) {
		fact = strings.TrimSpace(fact)
		if fact == "" || slices.Contains(facts, fact) || len(facts) >= max {
			return
		}

		facts = append(facts, fact)
	}

	for _, fact := range behaviorFacts {
		add(fact)
	}

	for _, fact := range definitionFacts {
		add(fact)
	}

	if len(facts) > 1 && currentFunction != "" && facts[0] == "defines `"+currentFunction+"`" {
		return facts[1:]
	}

	return facts
}

func sourceBodyObjectiveLiteralFact(functionName string, line string, objective string) string {
	literals := sourceBodyObjectiveStringLiterals(line, objective, 2)
	if len(literals) == 0 {
		return ""
	}

	if len(literals) == 1 && strings.EqualFold(strings.Trim(literals[0], "`\""), "redacted") {
		if functionName != "" {
			return "`" + functionName + "` references secret-safe redaction value " + literals[0]
		}

		return "references secret-safe redaction value " + literals[0]
	}

	if functionName != "" {
		return "`" + functionName + "` references " + joinFallbackFacts(literals)
	}

	return "references " + joinFallbackFacts(literals)
}

func sourceBodyObjectiveStringLiterals(line string, objective string, limit int) []string {
	if limit <= 0 {
		limit = 2
	}

	objective = strings.TrimSpace(objective)

	var literals []string

	inQuote := false
	escaped := false

	var current strings.Builder

	for _, r := range line {
		if !inQuote {
			if r == '"' {
				inQuote = true
				escaped = false

				current.Reset()
			}

			continue
		}

		if escaped {
			current.WriteRune(r)

			escaped = false

			continue
		}

		switch r {
		case '\\':
			escaped = true
		case '"':
			value := strings.TrimSpace(current.String())

			inQuote = false

			if sourceBodyLiteralSupportsObjective(value, objective) {
				literals = textutil.AppendUnique(literals, "`\""+value+"\"`")
				if len(literals) >= limit {
					return literals
				}
			}
		default:
			current.WriteRune(r)
		}
	}

	return literals
}

func sourceBodyLiteralSupportsObjective(value string, objective string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 4 || len(value) > 120 {
		return false
	}

	if strings.ContainsAny(value, "\n\r\t") {
		return false
	}

	if objective == "" {
		return codeShapedTokenPattern.MatchString(value)
	}

	return TextSharesObjectiveTerm(value, objective)
}

func sourceBodyCaseFact(functionName string, line string) string {
	line = strings.TrimSpace(line)

	rest, ok := strings.CutPrefix(line, "case ")

	if !ok {
		return ""
	}

	head, _, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}

	var cases []string

	for _, raw := range strings.Split(head, ",") {
		value := strings.TrimSpace(raw)
		if value == "" || strings.Contains(value, `"`) {
			continue
		}

		if !strings.Contains(value, ".") && !codeShapedTokenPattern.MatchString(value) {
			continue
		}

		cases = append(cases, "`"+value+"`")
	}

	if len(cases) == 0 {
		return ""
	}

	if functionName != "" {
		return "`" + functionName + "` handles " + joinFallbackFacts(cases)
	}

	return "handles " + joinFallbackFacts(cases)
}

func sourceBodyRecordAssignmentFact(line string) string {
	line = strings.TrimSpace(line)
	for _, op := range []string{":=", "="} {
		lhs, rhs, ok := strings.Cut(line, op)
		if !ok {
			continue
		}

		key := sourceBodyMapStringKey(lhs)
		value := exactProbeTrimExpression(rhs)

		if key == "" || value == "" {
			continue
		}

		kind := "metadata field"
		lowerKey := strings.ToLower(key)
		lowerValue := strings.ToLower(value)

		if ContainsAny(lowerKey, "path", "source", "root", "cwd") {
			kind = "path metadata field"
		}

		if ContainsAny(lowerKey, "prompt", "digest", "redact", "secret") ||
			ContainsAny(lowerValue, "sanitize", "digest", "redact", "secret") {

			kind = "secret-safe " + kind
		}

		if op == ":=" {
			return "assigns " + kind + " `" + key + "` from `" + value + "`"
		}

		return "sets " + kind + " `" + key + "` from `" + value + "`"
	}

	return ""
}

func sourceBodyMapStringKey(lhs string) string {
	lhs = strings.TrimSpace(lhs)

	open := strings.IndexByte(lhs, '[')
	close := strings.LastIndexByte(lhs, ']')

	if open < 0 || close <= open {
		return ""
	}

	key := strings.TrimSpace(lhs[open+1 : close])
	if len(key) < 2 || key[0] != '"' || key[len(key)-1] != '"' {
		return ""
	}

	key = strings.TrimSpace(key[1 : len(key)-1])
	if key == "" || strings.ContainsAny(key, "\n\r\t") {
		return ""
	}

	return key
}

func sourceBodyStructFieldFact(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "(){}=") {
		return ""
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}

	name := strings.TrimSpace(fields[0])
	typ := strings.TrimLeft(strings.TrimSpace(fields[1]), "*[]")

	if name == "" || typ == "" || name[0] < 'A' || name[0] > 'Z' {
		return ""
	}

	if !codeShapedTokenPattern.MatchString(name) || !codeShapedTokenPattern.MatchString(typ) {
		return ""
	}

	lower := strings.ToLower(name)
	kind := "field"

	if ContainsAny(lower, "path", "source", "root", "cwd") {
		kind = "path metadata field"
	}

	return "defines " + kind + " `" + name + "`"
}

func sourceBodyCallFact(functionName string, line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	var calls []string

	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) <= 1 {
			continue
		}

		candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(match[1])
		if !ok {
			continue
		}

		if strings.EqualFold(candidate, "String") || strings.EqualFold(candidate, "Error") {
			continue
		}

		calls = textutil.AppendUnique(calls, "`"+candidate+"`")
		if len(calls) >= 2 {
			break
		}
	}

	if len(calls) == 0 {
		return ""
	}

	if functionName != "" {
		return "`" + functionName + "` calls " + joinFallbackFacts(calls)
	}

	return "calls " + joinFallbackFacts(calls)
}

func sourceBodyFunctionName(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "func ") {
		return ""
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, "func "))
	receiver := ""

	if strings.HasPrefix(rest, "(") {
		if idx := strings.Index(rest, ")"); idx >= 0 {
			receiver = sourceBodyReceiverType(rest[1:idx])
			rest = strings.TrimSpace(rest[idx+1:])
		}
	}

	if rest == "" {
		return ""
	}

	end := len(rest)
	for _, marker := range []string{"(", "[", " ", "\t"} {
		if idx := strings.Index(rest, marker); idx >= 0 && idx < end {
			end = idx
		}
	}

	name := strings.TrimSpace(rest[:end])
	if name == "" {
		return ""
	}

	if receiver != "" {
		return receiver + "." + name
	}

	return name
}

func sourceBodyReceiverType(receiver string) string {
	fields := strings.Fields(strings.TrimSpace(receiver))
	if len(fields) == 0 {
		return ""
	}

	typ := fields[len(fields)-1]

	typ = strings.TrimLeft(strings.TrimSpace(typ), "*")
	typ = strings.TrimPrefix(typ, "[]")
	typ = strings.TrimSpace(typ)

	if typ == "" || strings.ContainsAny(typ, " \t,(){}") {
		return ""
	}

	return typ
}

func sourceBodyReturnFact(functionName string, line string) string {
	expr, ok := strings.CutPrefix(strings.TrimSpace(line), "return ")
	if !ok {
		return ""
	}

	expr = exactProbeTrimExpression(expr)
	if expr == "" {
		return ""
	}

	if functionName != "" {
		return "`" + functionName + "` returns `" + expr + "`"
	}

	return "returns `" + expr + "`"
}

func sourceBodyDefinitionFact(line string) string {
	line = strings.TrimSpace(line)
	for _, keyword := range []string{"const", "var"} {
		rest, ok := strings.CutPrefix(line, keyword+" ")
		if !ok {
			continue
		}

		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "(") {
			return ""
		}

		nameEnd := len(rest)
		for _, marker := range []string{"=", " ", "\t"} {
			if idx := strings.Index(rest, marker); idx >= 0 && idx < nameEnd {
				nameEnd = idx
			}
		}

		name := strings.TrimSpace(rest[:nameEnd])
		if name == "" {
			return ""
		}

		if _, rhs, ok := strings.Cut(rest, "="); ok {
			value := exactProbeTrimExpression(rhs)
			if value != "" {
				return "defines `" + name + "` as `" + value + "`"
			}
		}

		return "defines `" + name + "`"
	}

	if rest, ok := strings.CutPrefix(line, "type "); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "(") {
			return ""
		}

		nameEnd := len(rest)
		for _, marker := range []string{" ", "\t", "[", "{"} {
			if idx := strings.Index(rest, marker); idx >= 0 && idx < nameEnd {
				nameEnd = idx
			}
		}

		name := strings.TrimSpace(rest[:nameEnd])
		if name != "" {
			return "defines `" + name + "`"
		}
	}

	return ""
}

func presentableSourceBodyObjectivePrefix(objective string, lines []string) string {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if !strings.Contains(lower, "can ") && !strings.Contains(lower, "could ") {
		return ""
	}

	if !ContainsAny(lower, "shorten", "rename", "change", "update") {
		return ""
	}

	if !ContainsAny(lower, "label", "text", "string", "copy") {
		return ""
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"`) || strings.Contains(line, "`") {
			return "At the source level, yes."
		}
	}

	return ""
}

func sourceEvidenceAnswerLinesForObjective(output string, objective string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 8
	}

	all := sourceEvidenceAnswerLines(output, 256)
	if len(all) == 0 {
		return nil
	}

	if strings.TrimSpace(objective) == "" || len(all) <= maxLines {
		return textutil.Prefix(all, maxLines)
	}

	terms := sourceEvidenceObjectiveLineTerms(objective)
	if len(terms) == 0 {
		return textutil.Prefix(all, maxLines)
	}

	selected := make([]string, 0, maxLines)
	add := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || slices.Contains(selected, line) {
			return
		}

		selected = append(selected, line)
	}

	for _, line := range all {
		if sourceBodyFunctionName(line) != "" {
			add(line)

			break
		}
	}

	for _, line := range all {
		if sourceEvidenceLineObjectiveScore(line, terms) <= 0 {
			continue
		}

		add(line)

		if len(selected) >= maxLines {
			return selected
		}
	}

	if len(selected) > 0 {
		for _, line := range all {
			if !sourceEvidenceLineCarriesAnswerContext(line, terms) {
				continue
			}

			add(line)

			if len(selected) >= maxLines {
				return selected
			}
		}
	}

	if len(selected) == 0 {
		return textutil.Prefix(all, maxLines)
	}

	return selected
}

func sourceEvidenceLineCarriesAnswerContext(line string, terms []string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}

	if sourceEvidenceLineObjectiveScore(line, terms) > 0 {
		return true
	}

	return ContainsAny(lower,
		"record[",
		"sanitize",
		"digest",
		"metadata",
		"redact",
		"prompt",
		"path",
		"permission",
		"policy",
		"delegate",
		"child",
		"task",
		"compose",
		"answer",
	)
}

func sourceEvidenceAnswerLines(output string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 3
	}

	var lines []string

	for _, line := range strings.Split(output, "\n") {
		trimmed := stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
		if trimmed == "" || sourceEvidenceLineIsBoilerplate(trimmed) {
			continue
		}

		if len(trimmed) > 160 {
			trimmed = strings.TrimSpace(trimmed[:160])
		}

		if slices.Contains(lines, trimmed) {
			continue
		}

		lines = append(lines, trimmed)
		if len(lines) >= maxLines {
			break
		}
	}

	return lines
}

func presentableExactProbeAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}

	anchor, ok := bestPresentableExactProbeAnchor(brief, results)
	if !ok {
		return "", false
	}

	facts := exactProbeObservationsForObjective(anchor.symbol, anchor.output, brief.Objective)
	if len(facts) == 0 {
		return "", false
	}

	if implementationShape, ok := presentableImplementationShapeAnswerFromClaim(brief.Objective, anchor.path, anchor.symbol, strings.Join(facts, ", ")); ok {
		return implementationShape, true
	}

	var b strings.Builder

	switch {
	case anchor.symbol != "" && anchor.path != "":
		b.WriteString("`")
		b.WriteString(anchor.symbol)
		b.WriteString("` in `")
		b.WriteString(anchor.path)
		b.WriteString("` is the clearest inspected implementation anchor.")
	case anchor.path != "":
		b.WriteString("`")
		b.WriteString(anchor.path)
		b.WriteString("` is the clearest inspected implementation anchor.")
	default:
		b.WriteString("The clearest inspected implementation anchor is the exact probe that was already read.")
	}

	b.WriteString(" The inspected body shows that it ")
	b.WriteString(joinFallbackFacts(facts))
	b.WriteString(".")

	answer := strings.TrimSpace(b.String())
	if objectiveAsksBehaviorSummary(brief.Objective) && looksLikeLowValueSourceInventoryNonAnswer(answer) {
		return "", false
	}

	return answer, true
}

func presentableImplementationShapeAnswerFromClaim(objective string, path string, symbol string, claim string) (string, bool) {
	if !ObjectiveAsksImplementationShape(objective) {
		return "", false
	}

	path = strings.TrimSpace(path)
	claim = strings.TrimSpace(claim)

	if path == "" || claim == "" {
		return "", false
	}

	lowerClaim := " " + strings.ToLower(strings.Join(strings.Fields(claim), " ")) + " "
	if !ContainsAny(lowerClaim, " handles ", " calls ", " sets ") {
		return "", false
	}

	anchor := "`" + path + "`"
	if symbol = strings.TrimSpace(symbol); symbol != "" {
		anchor = "`" + symbol + "` in " + anchor
	} else if inferred := firstSourceInventoryCodeToken(claim); inferred != "" {
		anchor = "`" + inferred + "` in " + anchor
	}

	claim = presentableImplementationShapeClaim(path, claim)

	var parts []string

	parts = append(parts, "The code evidence points to "+anchor+" as the inspected implementation owner.")
	parts = append(parts, "In that source, "+ensureTrailingPeriod(strings.TrimRight(claim, ".")))

	return strings.Join(parts, " "), true
}

func presentableImplementationShapeClaim(path string, claim string) string {
	claim = strings.TrimSpace(claim)
	path = strings.TrimSpace(path)

	if path != "" {
		prefix := "`" + path + "` shows "
		if strings.HasPrefix(claim, prefix) {
			claim = strings.TrimSpace(strings.TrimPrefix(claim, prefix))
		}
	}

	claim = strings.TrimPrefix(claim, "defines ")

	return strings.TrimSpace(claim)
}

func firstSourceInventoryCodeToken(claim string) string {
	for _, part := range strings.Split(claim, "`") {
		token := strings.TrimSpace(part)
		if token == "" || strings.ContainsAny(token, " \t\n\r") {
			continue
		}

		lower := strings.ToLower(token)
		if lower == "true" || lower == "false" || strings.HasPrefix(lower, "tea.key") {
			continue
		}

		if strings.Contains(token, "/") || strings.Contains(lower, ".go") || strings.Contains(lower, ".md") {
			continue
		}

		return token
	}

	return ""
}

type presentableExactProbeAnchor struct {
	path   string
	symbol string
	output string
}

func bestPresentableExactProbeAnchor(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (presentableExactProbeAnchor, bool) {
	objective := strings.TrimSpace(brief.Objective)
	candidates := make([]presentableExactProbeAnchor, 0, len(results))

	var best presentableExactProbeAnchor

	requiresOwner := implementationShapeOwnerContractApplies(brief)
	ledger := readOnlyAnswerabilityLedger{}
	ownerPathSet := map[string]struct{}{}

	if requiresOwner {
		ledger = readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		ownerPathSet = readOnlyPathSet(ledger.ImplementationOwnerPaths())

		if len(ownerPathSet) == 0 {
			return presentableExactProbeAnchor{}, false
		}
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if newToolResultEvidenceRecord(result).candidateAnchorOnly {
			continue
		}

		if presentableExactProbeResult(result) {
			path, symbol := extractExactProbeAnchor(result)

			candidates = append(candidates, presentableExactProbeAnchor{
				path:   path,
				symbol: symbol,
				output: strings.TrimSpace(result.Output),
			})
		}
	}

	for _, candidate := range candidates {
		var owner readOnlySourceOwnerEvidence

		if requiresOwner {
			if _, ok := ownerPathSet[NormalizeRunnerPath(candidate.path)]; !ok {
				continue
			}

			owner, _ = ledger.SourceOwnerEvidenceForPath(candidate.path)
		}

		if len(exactProbeObservationsForObjective(candidate.symbol, candidate.output, objective)) == 0 {
			continue
		}

		if best.path == "" || presentableExactProbeAnchorObjectiveScore(candidate, objective, owner) > presentableExactProbeAnchorObjectiveScore(best, objective, sourceOwnerEvidenceForBestAnchor(ledger, best)) {
			best = candidate
		}
	}

	if best.path != "" {
		return best, true
	}

	if requiresOwner {
		return presentableExactProbeAnchor{}, false
	}

	if len(candidates) == 0 {
		return presentableExactProbeAnchor{}, false
	}

	return candidates[0], true
}

func sourceOwnerEvidenceForBestAnchor(ledger readOnlyAnswerabilityLedger, anchor presentableExactProbeAnchor) readOnlySourceOwnerEvidence {
	evidence, _ := ledger.SourceOwnerEvidenceForPath(anchor.path)
	return evidence
}

func presentableExactProbeAnchorObjectiveScore(candidate presentableExactProbeAnchor, objective string, owner readOnlySourceOwnerEvidence) int {
	text := strings.Join([]string{candidate.symbol, candidate.output}, "\n")
	score := sourceEvidenceLineObjectiveScore(text, sourceEvidenceObjectiveLineTerms(objective))

	score += len(answerObjectiveTerms(text, objective)) * 2

	if owner.OwnerEligible() {
		score += 20 + owner.Score
	} else if owner.Role == readOnlySourceOwnerConflictingNavigation {
		score -= 20
	}

	if sourceBodyHasStrongObjectiveToken(text, objective) {
		score += 3
	}

	return score
}

func exactProbeObservationsForObjective(symbol string, output string, objective string) []string {
	lines := sourceEvidenceAnswerLinesForObjective(output, objective, 24)
	if len(lines) > 0 {
		if facts := sourceBodySemanticFacts(lines, objective, 5); len(facts) > 0 {
			return facts
		}
	}

	return exactProbeObservations(symbol, output)
}

func extractReadExactProbeAnchor(summary string) (string, string) {
	summary = strings.TrimSpace(summary)
	if rest, ok := strings.CutPrefix(summary, "Read "); ok {
		if symbol, remainder, found := strings.Cut(rest, " in "); found {
			return extractExactProbePath(remainder), strings.TrimSpace(symbol)
		}

		return extractExactProbePath(rest), ""
	}

	if rest, ok := strings.CutPrefix(summary, "Extracted "); ok {
		if symbol, remainder, found := strings.Cut(rest, " from "); found {
			return extractExactProbePath(remainder), strings.TrimSpace(symbol)
		}
	}

	return "", ""
}

func extractExactProbePath(remainder string) string {
	remainder = strings.TrimSpace(remainder)
	for _, marker := range []string{" lines ", " bytes ", " (", ":"} {
		if head, _, ok := strings.Cut(remainder, marker); ok {
			return strings.TrimSpace(head)
		}
	}

	return strings.TrimSpace(remainder)
}

func exactProbeObservations(symbol string, output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	observations := make([]string, 0, 5)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(observations, value) {
			return
		}

		observations = append(observations, value)
	}

	for _, line := range exactProbeSourceLines(output) {
		if observation := exactProbeReturnObservation(line); observation != "" {
			add(observation)
		}

		if len(observations) >= 3 {
			return observations
		}

		if observation := exactProbeAssignmentObservation(line); observation != "" {
			add(observation)
		}

		if len(observations) >= 3 {
			return observations
		}
	}

	calls := exactProbeCallNames(symbol, output, 4)
	if len(calls) > 0 {
		add("calls " + quotedFallbackCallNames(calls))
	}

	return observations
}

func exactProbeSourceLines(output string) []string {
	var lines []string

	for _, line := range strings.Split(output, "\n") {
		line = stripSourceEvidenceLinePrefix(strings.TrimSpace(line))
		if line == "" || sourceEvidenceLineIsBoilerplate(line) {
			continue
		}

		if strings.HasPrefix(line, "capability:") ||
			strings.HasPrefix(line, "language:") ||
			strings.HasPrefix(line, "routed_tool:") ||
			strings.HasPrefix(line, "route_summary:") {

			continue
		}

		lines = append(lines, line)
	}

	return lines
}

func exactProbeReturnObservation(line string) string {
	expr, ok := strings.CutPrefix(strings.TrimSpace(line), "return ")
	if !ok {
		return ""
	}

	expr = exactProbeTrimExpression(expr)
	if expr == "" {
		return ""
	}

	return "returns `" + expr + "`"
}

func exactProbeAssignmentObservation(line string) string {
	line = strings.TrimSpace(line)
	for _, op := range []string{":=", "="} {
		lhs, rhs, ok := strings.Cut(line, op)
		if !ok {
			continue
		}

		name := exactProbeAssignmentName(lhs)
		value := exactProbeTrimExpression(rhs)

		if name == "" || value == "" {
			return ""
		}

		if op == "=" {
			return "sets `" + name + "` to `" + value + "`"
		}

		return "assigns `" + name + "` from `" + value + "`"
	}

	return ""
}

func exactProbeAssignmentName(lhs string) string {
	lhs = strings.TrimSpace(lhs)
	if strings.ContainsAny(lhs, " \t,(){}[]") {
		return ""
	}

	if lhs == "" || strings.HasPrefix(lhs, "if") || strings.HasPrefix(lhs, "for") || strings.HasPrefix(lhs, "switch") {
		return ""
	}

	return lhs
}

func exactProbeTrimExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	if idx := strings.Index(expr, "//"); idx >= 0 {
		expr = strings.TrimSpace(expr[:idx])
	}

	expr = strings.TrimRight(strings.TrimSpace(expr), ",")
	if len(expr) > 120 {
		expr = strings.TrimSpace(expr[:120])
	}

	return expr
}

func quotedFallbackCallNames(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		quoted = append(quoted, "`"+value+"`")
	}

	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
}

func joinFallbackFacts(facts []string) string {
	filtered := make([]string, 0, len(facts))
	for _, fact := range facts {
		fact = strings.TrimSpace(fact)
		if fact == "" {
			continue
		}

		filtered = append(filtered, fact)
	}

	switch len(filtered) {
	case 0:
		return ""
	case 1:
		return filtered[0]
	case 2:
		return filtered[0] + " and " + filtered[1]
	default:
		return strings.Join(filtered[:len(filtered)-1], ", ") + ", and " + filtered[len(filtered)-1]
	}
}

func presentableFindingsFromInternalAnswer(answer string, max int) []string {
	if max <= 0 {
		max = 2
	}

	inKeyFindings := false
	findings := make([]string, 0, max)

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)

		switch {
		case lower == "key findings:":
			inKeyFindings = true
			continue
		case inKeyFindings && strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "- "):
			return findings
		case !inKeyFindings || !strings.HasPrefix(line, "- "):
			continue
		}

		finding := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if !presentableInternalFinding(finding) {
			continue
		}

		findings = append(findings, finding)
		if len(findings) >= max {
			return findings
		}
	}

	return findings
}

func presentableInternalFinding(finding string) bool {
	finding = strings.TrimSpace(finding)
	if len(finding) < 40 {
		return false
	}

	lower := strings.ToLower(finding)

	return !ContainsAny(lower,
		"child task completed",
		"outlined ",
		"ran ",
		"read ",
		"found ",
		"text anchors:",
		"symbol read:",
		"related files:",
		"strongest implementation evidence",
		"what this establishes",
	)
}

func looksLikeStructuredAnswerEnvelope(lower string) bool {
	if !strings.HasPrefix(strings.TrimSpace(lower), "{") {
		return false
	}

	return strings.Contains(lower, `"answer"`) &&
		(strings.Contains(lower, `"verification"`) ||
			strings.Contains(lower, `"evidence"`) ||
			strings.Contains(lower, `"sources"`))
}

func finalAnswerStopReasonPreservesUncertainty(stopReason string) bool {
	switch strings.TrimSpace(stopReason) {
	case "post_child_read_budget_exhausted",
		"post_readiness_probe_budget_exhausted",
		"post_readiness_no_gap_repeated",
		"closure_synthesis_after_ignored_nudge":
		return false
	}

	return strings.HasSuffix(strings.TrimSpace(stopReason), "_budget_exhausted")
}

func userFacingAgentName(name string) bool {
	return !strings.HasPrefix(name, "child:") && !strings.HasPrefix(name, "child_write:")
}

func (r *Runner) userFacingFinalAnswer() bool {
	return userFacingAgentName(r.agentName())
}

// stripCompletedMutationSelfBlocker rewrites a mutation-lane final
// answer when the model surfaced a self-inflicted "Blocker:" line even
// though the harness can see the work is done. Returns the cleaned
// answer and true on a rewrite; otherwise empty and false.
//
// Preconditions:
//   - Brief requires mutation.
//   - All required_edit_paths show up in changed paths.
//   - Answer is not already a <<QUESTION>>.
//   - The visible blocker is attributable to the harness loop after the
//     requested write completed: either a stale write probe for a changed
//     path, or an internal tool-surface/recovery rejection after no
//     post-write verification obligation remains.
func stripCompletedMutationSelfBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	if !runnerBriefRequiresMutation(brief) {
		return "", false
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return "", false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) > 0 {
		return "", false
	}

	if !completedMutationSelfBlockerIsHarnessOwned(brief, results, changed) {
		return "", false
	}

	cleaned, stripped := stripBlockerSection(answer)
	if !stripped || cleaned == "" {
		return "", false
	}

	return cleaned, true
}

func completedMutationSelfBlockerIsHarnessOwned(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, changed map[string]struct{}) bool {
	if lastFailureIsPostCompletionWriteProbe(results, changed) && plannerOwnsVerification(brief) {
		return true
	}

	if hasPendingVerificationAfterWrites(results, brief) || sourceEditsNeedSemanticVerification(brief, results) {
		return false
	}

	return lastFailureIsHarnessRejectionAfterCompletedMutation(results)
}

// lastFailureIsPostCompletionWriteProbe reports whether the most recent
// failed tool result is a write-class call targeting a path that was
// already changed earlier in the run. Non-write failures and writes
// against fresh paths return false — those may still represent real
// blockers.
func lastFailureIsPostCompletionWriteProbe(results []workspace.ToolResult, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case "edit_file", "apply_patch", "write_file", "append_file":
		default:
			return false
		}

		path := NormalizeRunnerPath(writeFailureTargetPath(result))
		if path == "" {
			return false
		}

		_, ok := changed[path]

		return ok
	}

	return false
}

func lastFailureIsHarnessRejectionAfterCompletedMutation(results []workspace.ToolResult) bool {
	latestProgressIndex := -1

	for i, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) || workspace.AnalyzeVerificationResult(result).Successful {
			latestProgressIndex = i
		}
	}

	if latestProgressIndex < 0 {
		return false
	}

	for i := len(results) - 1; i > latestProgressIndex; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		return isHarnessInternalRejectionResult(result)
	}

	return false
}

// writeFailureTargetPath extracts the workspace path the failed write
// was aimed at, when the tool's error or summary surfaces it. Returns
// the empty string when no path is recoverable.
func writeFailureTargetPath(result workspace.ToolResult) string {
	switch strings.TrimSpace(result.Tool) {
	case "apply_patch":
		// "Update File <path>: hunk 1 context did not match the file"
		// "Add File <path>: …", etc.
		errText := strings.TrimSpace(result.Error)
		for _, prefix := range []string{"Update File ", "Add File ", "Delete File ", "Move File ", "Update+Move File "} {
			if rest, ok := strings.CutPrefix(errText, prefix); ok {
				if path, _, found := strings.Cut(rest, ":"); found {
					return strings.TrimSpace(path)
				}

				return strings.TrimSpace(rest)
			}
		}
	case "edit_file", "write_file", "append_file":
		// edit_file rejection summaries don't always carry the path,
		// but successful summaries are formatted "Edited <path>" /
		// "Wrote <path>" / "Appended <path>". Fail closed when neither
		// the summary nor a parsed error yields a path.
		summary := strings.TrimSpace(result.Summary)
		fields := strings.Fields(summary)

		if len(fields) >= 2 {
			switch fields[0] {
			case "Edited", "Wrote", "Appended":
				return fields[1]
			}
		}
	}

	return ""
}

// stripBlockerSection removes a trailing "Blocker:" / "Blockers:"
// section (and its bullet body) from the answer, returning the
// cleaned text and true when something was stripped. Other prose
// (Changed paths, Verification) is preserved.
func stripBlockerSection(answer string) (string, bool) {
	lines := strings.Split(answer, "\n")
	cut := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		lower := strings.ToLower(trimmed)
		if lower == "blocker:" ||
			lower == "blockers:" ||
			strings.HasPrefix(lower, "blocker:") ||
			strings.HasPrefix(lower, "blockers:") ||
			strings.HasPrefix(lower, "blocker -") ||
			strings.HasPrefix(lower, "blocker—") {

			cut = i
			break
		}
	}

	if cut < 0 {
		return "", false
	}

	head := strings.TrimRightFunc(strings.Join(lines[:cut], "\n"), func(r rune) bool {
		return r == '\n' || r == ' ' || r == '\t'
	})

	return head, true
}

func (r *Runner) emitFinalAnswerDecorationEvent(stopReason string, before string, after string) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(before) == strings.TrimSpace(after) {
		return
	}

	payload := map[string]any{
		"agent":        r.agentName(),
		"stop_reason":  strings.TrimSpace(stopReason),
		"before_chars": len(strings.TrimSpace(before)),
		"after_chars":  len(strings.TrimSpace(after)),
	}
	if data, err := json.Marshal(payload); err == nil {
		r.OnEvent(RunEvent{Role: "answer_decorated", Content: string(data)})
	}
}

// isConversationLikeLane returns true when the task family is a direct
// user-facing conversation lane where source-body extraction would produce
// incomprehensible code-fragment answers rather than useful findings.
func isConversationLikeLane(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	switch family {
	case "conversation", "direct_question", "information_request", "coding_help", "general_chat":
		return true
	default:
		return false
	}
}

func conversationFallbackShouldAvoidSourceBody(brief workerruntime.WorkerRuntimeBrief) bool {
	if !isConversationLikeLane(brief) {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(brief.Objective))
	if ContainsAny(lower, "shorten", "rename", "change", "update") &&
		ContainsAny(lower, ".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", "label", "function", "source") {

		return false
	}

	return true
}

// isDeterministicFallbackAnswer returns true when the internal answer is
// the deterministic fallback produced by synthesizeFallbackAnswer — the
// "Partial findings from this turn..." text that the harness generates when
// the model fails to produce a substantive answer.

func mutationHistoryEvidenceRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	for _, label := range brief.RequiredEvidence {
		if strings.TrimSpace(label) == string(workerruntime.RequiredEvidenceGitHistoryRationale) {
			return true
		}
	}

	return false
}

func successfulGitHistoryEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" &&
			workspace.ToolSurfacePhaseAllowsTool(result.Tool, workspace.ToolSurfacePhasePlanExecuteHistoryPending) &&
			result.Facts != nil &&
			result.Facts.GitHistoryStatus == toolfacts.GitHistoryStatusCommitDescriptions {

			return true
		}
	}

	return false
}

func successfulGitHistoryResultForPath(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !workspace.ToolSurfacePhaseAllowsTool(result.Tool, workspace.ToolSurfacePhasePlanExecuteHistoryPending) {
			continue
		}

		if result.Facts == nil || NormalizeRunnerPath(result.Facts.GitHistoryPath) != path {
			continue
		}

		switch result.Facts.GitHistoryStatus {
		case toolfacts.GitHistoryStatusCommitDescriptions, toolfacts.GitHistoryStatusNotApplicable:
			return true
		}
	}

	return false
}

func (r *Runner) writeBeforeGitHistoryEvidenceRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return "", "", false
	}

	if CheckDescription(req) != nil {
		return "", "", false
	}

	missing := r.writePathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		return "", "", false
	}

	return "Write rejected before git history evidence", gitHistoryEvidenceRequiredMessage(missing), true
}

func (r *Runner) writeBeforeGitHistoryEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}

	if CheckDescription(req) != nil {
		return workspace.ToolRequest{}, "", false
	}

	missing := r.writePathsMissingGitHistory(req, results)

	return gitHistoryEvidenceRedirectRequest(missing, results, allowedTools)
}

func (r *Runner) writePathsMissingGitHistory(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	return pathsMissingGitHistory(r.workspaceRootForGitHistoryRequirement(), writeRequestPaths(req), results)
}

func (r *Runner) submitPlanPathsMissingGitHistory(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	paths := make([]string, 0, len(edits))

	for _, edit := range edits {
		paths = append(paths, edit.Path)
	}

	return pathsMissingGitHistory(r.workspaceRootForGitHistoryRequirement(), paths, results)
}

func (r *Runner) submitPlanBeforeGitHistoryEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}

	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) > 0 &&
		len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {

		return workspace.ToolRequest{}, "", false
	}

	if planThenExecuteSubmitPlanHasRequiredPathCoverageProblem(r.RuntimeBrief.RequiredEditPaths, req) {
		return workspace.ToolRequest{}, "", false
	}

	if len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	missing := r.submitPlanPathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		missing = planThenExecuteHistoryPendingPaths(r.RuntimeBrief, results, nil)
	}

	return gitHistoryEvidenceRedirectRequest(missing, results, allowedTools)
}

func (r *Runner) sourceReadBeforeGitHistoryEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}

	if !workspace.IsReadTool(strings.TrimSpace(req.Tool)) || slices.Contains([]string{workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol}, strings.TrimSpace(req.Tool)) {
		return workspace.ToolRequest{}, "", false
	}

	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) == 0 ||
		len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {

		return workspace.ToolRequest{}, "", false
	}

	pending := planThenExecuteHistoryPendingPaths(r.RuntimeBrief, results, nil)
	if len(pending) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req)); target != "" {
		for _, path := range pending {
			if NormalizeRunnerPath(path) == target {
				return gitHistoryEvidenceRedirectRequest([]string{target}, results, allowedTools)
			}
		}
	}

	return gitHistoryEvidenceRedirectRequest(pending, results, allowedTools)
}

func planThenExecuteSubmitPlanHasRequiredPathCoverageProblem(requiredPaths []string, req workspace.ToolRequest) bool {
	required := make([]string, 0, len(requiredPaths))
	for _, raw := range requiredPaths {
		if path := NormalizeRunnerPath(raw); path != "" {
			required = append(required, path)
		}
	}

	if len(required) == 0 {
		return false
	}

	requiredSet := make(map[string]struct{}, len(required))
	for _, path := range required {
		requiredSet[path] = struct{}{}
	}

	covered := make(map[string]struct{}, len(required))

	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}

		if _, ok := requiredSet[path]; !ok {
			return true
		}

		covered[path] = struct{}{}
	}

	for _, path := range required {
		if _, ok := covered[path]; !ok {
			return true
		}
	}

	return false
}

func gitHistoryEvidenceRedirectRequest(paths []string, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if len(paths) == 0 || !slices.Contains(allowedTools, workspace.ToolNameHistoryRange) {
		return workspace.ToolRequest{}, "", false
	}

	path := NormalizeRunnerPath(paths[0])
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}

	start, end := gitHistoryRedirectRange(path, results)
	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameHistoryRange,
		Path:            path,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        "git history rationale before changing established logic",
		WhyNotAnswering: "need commit history evidence before write or submit_plan",
	}

	return req, marshalToolRequestRaw(req), true
}

func gitHistoryRedirectRange(path string, results []workspace.ToolResult) (int, int) {
	path = NormalizeRunnerPath(path)
	for _, readRange := range readRangesForPathFromResults(results, path) {
		if readRange.start > 0 && readRange.end >= readRange.start {
			return readRange.start, readRange.end
		}
	}

	return 1, 80
}

func (r *Runner) workspaceRootForGitHistoryRequirement() string {
	if r == nil || r.Workspace == nil {
		return ""
	}

	return strings.TrimSpace(r.Workspace.Root())
}

func pathsMissingGitHistory(root string, paths []string, results []workspace.ToolResult) []string {
	seen := make(map[string]struct{}, len(paths))

	var missing []string

	for _, rawPath := range paths {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		if !existingFileNeedsGitHistory(root, path) {
			continue
		}

		if successfulGitHistoryResultForPath(path, results) {
			continue
		}

		missing = append(missing, path)
	}

	sort.Strings(missing)

	return missing
}

func existingFileNeedsGitHistory(root string, path string) bool {
	path = cleanRelativeRunnerPath(path)
	if path == "" {
		return false
	}

	if strings.TrimSpace(root) == "" {
		return true
	}

	if !gitstate.HasMetadataAncestor(root) {
		return false
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))

	return err == nil && !info.IsDir()
}

func gitHistoryEvidenceRequiredMessage(paths []string) string {
	target := "the intended logic"
	if len(paths) > 0 {
		target = quotedRunnerStrings(paths)
	}

	return fmt.Sprintf("git history rationale is required before changing established logic for %s. Use `history_range` or `history_symbol` on the intended logic, read the commit descriptions to understand why and when the current behavior was introduced, then make the write only if that history supports the proposed change. If history shows the change is unsafe, answer `<<QUESTION>>:` with the blocker; genuinely new files do not require history evidence.", target)
}

func planThenExecuteUnavailableHistoryToolNudge(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, allowedTools []string) (string, string, bool) {
	tool := strings.TrimSpace(req.Tool)
	if !workspace.ToolSurfacePhaseAllowsTool(tool, workspace.ToolSurfacePhasePlanExecuteHistoryPending) {
		return "", "", false
	}

	if slices.Contains(allowedTools, tool) || !mutationHistoryEvidenceRequired(brief) {
		return "", "", false
	}

	completed := modelProtocolCompletedGitHistoryPaths(brief, results)
	pending := modelProtocolPendingGitHistoryPaths(brief, results)
	sourcePending := modelProtocolSourceBodyObligations(brief, results, TurnPhaseContract{PhaseID: turnPhaseRequiredSource})

	if len(completed) == 0 && len(pending) > 0 {
		return "", "", false
	}

	summary := "Git history tool not available in current plan-execute phase"
	switch {
	case len(pending) == 0 && len(sourcePending) > 0:
		return summary, fmt.Sprintf("%s is not callable because git history evidence is already satisfied for completed paths %s. The current pending obligation is source-body evidence for %s; use an available source read tool instead of repeating history_range/history_symbol.", tool, quotedRunnerStrings(completed), quotedRunnerStrings(sourcePending)), true
	case len(pending) == 0:
		return summary, fmt.Sprintf("%s is not callable because required git history evidence is already satisfied for completed paths %s. Continue with the current allowed tool surface or submit the next valid terminal action.", tool, quotedRunnerStrings(completed)), true
	default:
		return summary, fmt.Sprintf("%s is not callable in the current phase. Completed git history paths: %s. Pending git history paths: %s. Use the current allowed tool surface or wait for the history_pending phase before calling history tools.", tool, quotedRunnerStrings(completed), quotedRunnerStrings(pending)), true
	}
}

func (r *Runner) preflightPlanThenExecuteSubmitPlanHistoryGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return verificationInterceptResult{}
	}

	if !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return verificationInterceptResult{}
	}

	missing := r.submitPlanPathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		return verificationInterceptResult{}
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    workspace.ToolNameSubmitPlan,
			Summary: "submit_plan rejected: edit paths lack git history evidence",
			Error: "submit_plan edits must be backed by git history rationale before executor handoff; missing `history_range`/`history_symbol` commit-description evidence for " + quotedRunnerStrings(missing) + ". " +
				"Read the history for the intended logic, use the commit descriptions to decide why and when the current behavior exists, then submit a corrected plan only if the proposed change is safe. If not safe, answer `<<QUESTION>>:` with the path-specific blocker.",
		},
		Reason: "plan-then-execute planner phase: submit_plan lacked git history evidence",
	}
}

func (r *Runner) implementationTargetWriteReadyHistoryReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !mutationHistoryEvidenceRequired(r.RuntimeBrief) || !slices.Contains(workspace.RuntimeHistoryInspectionToolNames(), strings.TrimSpace(req.Tool)) {
		return false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if successfulGitHistoryResultForPath(path, results) {
		return false
	}

	if !implementationTargetWriteReadySourcePath(path) && !newToolResultEvidenceProjection(results).HasSourceBody(path) {
		return false
	}

	return true
}

func planThenExecuteSubmitPlanHistoryMissing(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !mutationHistoryEvidenceRequired(brief) {
		return false
	}

	var paths []string

	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if newToolResultEvidenceProjection(results).HasSourceBody(path) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true})
		for _, path := range state.SelectedEditPaths {
			if newToolResultEvidenceProjection(results).HasSourceBody(path) {
				paths = append(paths, path)
			}
		}
	}

	paths = textutil.Unique(paths)
	for _, path := range paths {
		if !successfulGitHistoryResultForPath(path, results) {
			return true
		}
	}

	return false
}

func answerHasGitHistoryNoopOrBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return true
	}

	lower := strings.ToLower(trimmed)

	return strings.Contains(lower, "source-satisfied") ||
		strings.Contains(lower, "no files were changed") ||
		strings.Contains(lower, "changed paths: none") ||
		strings.Contains(lower, "no changes") ||
		strings.Contains(lower, "noop") ||
		strings.Contains(lower, "new file")
}

type LanePolicyID string

const (
	lanePolicyDefault               LanePolicyID = "default"
	lanePolicyConversation          LanePolicyID = "conversation"
	lanePolicyReadOnlyInvestigation LanePolicyID = "read_only_investigation"
	lanePolicyDelegatedReadOnly     LanePolicyID = "delegated_read_only"
)

// LanePolicy is the lane-specific behavioral policy derived from the runtime
// brief. It centralizes decisions that were previously guarded by scattered
// TaskFamily/Contract string comparisons.
type LanePolicy struct {
	name                           LanePolicyID
	skipFingerprintHardBlock       bool
	skipSynthesisNudge             bool
	skipExplorationDelegationNudge bool
	forceAnswerAfterRejection      bool
	autoCaptureFindings            bool
	noteTool                       bool
	skipRecoveryNudge              bool
	confidenceCap                  string
	callCountMilestones            func(int) (int, int)
	shouldHaveNoteTool             func(string) bool
}

func lanePolicyName(policy *LanePolicy) string {
	if policy == nil {
		return ""
	}
	return string(policy.name)
}

func lanePolicyCallCountMilestones(policy *LanePolicy, ctxSize int) (int, int) {
	if policy == nil || policy.callCountMilestones == nil {
		return 0, 0
	}
	return policy.callCountMilestones(ctxSize)
}

func lanePolicyShouldHaveNoteTool(policy *LanePolicy, contract string) bool {
	if policy == nil {
		return false
	}
	if policy.shouldHaveNoteTool != nil {
		return policy.shouldHaveNoteTool(contract)
	}
	return policy.noteTool
}

func fixedLanePolicyMilestones(first, interval int) func(int) (int, int) {
	return func(int) (int, int) {
		return first, interval
	}
}

var (
	defaultLanePolicy = LanePolicy{
		name:                      lanePolicyDefault,
		forceAnswerAfterRejection: true,
	}
	conversationLanePolicy = LanePolicy{
		name:                           lanePolicyConversation,
		skipFingerprintHardBlock:       true,
		skipSynthesisNudge:             true,
		skipExplorationDelegationNudge: true,
		autoCaptureFindings:            true,
		noteTool:                       true,
		skipRecoveryNudge:              true,
		confidenceCap:                  "medium",
		callCountMilestones:            conversationMilestones,
	}
	// Read-only investigation lanes keep loop detection and advisory nudges, but
	// do not force-complete merely because the harness thinks the model has
	// enough evidence. The model owns the investigation stop decision.
	readOnlyInvestigationLanePolicy = LanePolicy{
		name:                      lanePolicyReadOnlyInvestigation,
		forceAnswerAfterRejection: false,
		autoCaptureFindings:       true,
		noteTool:                  true,
		confidenceCap:             "medium",
		callCountMilestones:       fixedLanePolicyMilestones(10, 10),
	}
	delegatedReadOnlyLanePolicy = LanePolicy{
		name:                      lanePolicyDelegatedReadOnly,
		forceAnswerAfterRejection: true,
		shouldHaveNoteTool: func(contract string) bool {
			return strings.EqualFold(contract, "grounded_evidence_summary")
		},
	}
)

func lanePolicyHasName(policy *LanePolicy, name LanePolicyID) bool {
	return strings.TrimSpace(lanePolicyName(policy)) == string(name)
}

// --- LanePolicyForBrief selects the appropriate policy from a RuntimeBrief ---

func LanePolicyForBrief(brief workerruntime.WorkerRuntimeBrief) *LanePolicy {
	if workerruntime.TaskFamilyIsConversation(brief.TaskFamily) || readOnlyDirectAnswerLane(brief) {
		return &conversationLanePolicy
	}

	if workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDelegatedReadOnly) {
		return &delegatedReadOnlyLanePolicy
	}

	// Read-only investigation lanes (architecture, review, locate, etc.)
	// that do not require mutation use the read-only investigation policy so
	// budget enforcement and loop detection remain active (the model cannot
	// read forever) while force-answer-after-rejection is suppressed.
	//
	// Without this explicit routing, the LLM route classifier's
	// non-determinism (temp 0.1) causes similar evidence-based questions to
	// land in different lane policies with inconsistent force-answer
	// behavior, violating the north-star principle of preferring nudges and
	// bounded recovery over hard-picking the model's next step.
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!runnerBriefRequiresMutation(brief) {

		return &readOnlyInvestigationLanePolicy
	}

	return &defaultLanePolicy
}

// ModePolicy returns tool-economy nudge defaults for the given Mode. These
// are soft budgets -- they inject a one-shot system message when crossed,
// never hard-stop a turn.
func ModePolicy(_ modepolicy.Mode) RunPolicy {
	return RunPolicy{
		FingerprintNudgeAt: 3,
		SynthesisNudgeAt:   10,
	}
}

func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Content)
	}

	if prompt == "" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "task prompt is required",
		}
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "read_only"
	}

	if isWriteChildMode(mode) {
		return r.runWorkbranchChildTask(ctx, req, prompt, opts)
	}

	if mode != "read_only" && mode != "readonly" && mode != "explore" {
		return workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task rejected",
			Error:   "only read_only child tasks are currently supported",
		}
	}

	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "child"
	}

	prompt = r.prepareReadOnlyChildPrompt(prompt)

	childPolicy := RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3, MaxModelCalls: 6}

	if workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) {
		childPolicy.MaxToolSteps = 4
		childPolicy.MaxModelCalls = 6
	}

	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}

	lifecycle := r.beginReadOnlyChildLifecycle(agentName, opts, childPolicy)

	childBrief := workerruntime.RuntimeBriefForRole("child_read_only", "child:"+agentName, prompt)
	if len(pathparse.MentionedWorkspacePaths(prompt)) > 0 {
		childBrief.AllowedTools = filterOutRunnerTools(childBrief.AllowedTools,
			"go_overview", "go_entrypoints",
			"cpp_overview", "cpp_entrypoints",
			"python_overview", "python_entrypoints",
			"ts_overview", "ts_entrypoints",
			"rust_overview", "rust_entrypoints",
			"java_overview", "java_entrypoints",
		)
		childBrief.SearchPolicy = textutil.AppendUnique(childBrief.SearchPolicy, "start_from_named_paths_before_workspace_overview")
		childBrief.Constraints = textutil.AppendUnique(childBrief.Constraints, "named_paths_scoped")
	}

	child := &Runner{
		Client:           r.Client,
		Workspace:        r.Workspace,
		Policy:           childPolicy,
		AgentName:        "child:" + agentName,
		PermissionPolicy: readOnlyChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Disabled: true},
		DisableTaskTool:  true,
		RuntimeBrief:     childBrief,
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}

			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	childResults := make([]workspace.ToolResult, 0, 8)

	answer, err := child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a read-only delegated child task.",
					"Answer the delegated question directly using concise grounded evidence.",
					"For broad architecture, impact, tracing, security, or history questions, a layered discovery evidence packet satisfies orientation only; read at least one relevant source body before answering.",
					"After one sufficient source-body probe, answer unless one exact missing claim would materially change the answer.",
					"Prefer the minimal implementation surface needed to answer the question. Do not expand into side-path comparison, mode-by-mode breakdowns, or policy tables unless the question explicitly requires that detail.",
					"After readiness, treat packet-named files or symbols as evidence, not an allowlist. Either answer or take one exact follow-up that resolves a named missing claim.",
					"Prefer structured symbol/body tools, discovered handles, or narrow line ranges over raw path-wide rereads.",
					"Do not edit files, create files, install packages, push, publish, deploy, or spawn more tasks.",
					"Use repository-relative paths and cite the files you inspected.",
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			childResults = append(childResults, event.Result)
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
	if err != nil {
		result := workspace.ToolResult{
			Tool:    "task",
			Summary: "Child task failed",
			Error:   err.Error(),
		}
		r.emitReadOnlyChildTerminal(lifecycle, result, childResults, autonomy.ChildTerminalFailed)
		return result
	}

	fallbackSourceReads := 0
	maxFallbackSourceReads := 1

	for r.Workspace != nil && fallbackSourceReads < maxFallbackSourceReads && delegatedChildFallbackSourceReadNeeded(prompt, childResults) {
		if req, result, ok := r.delegatedChildFallbackSourceRead(ctx, prompt, childResults); ok {
			step := len(childResults) + 1
			profile := workspace.DiscoveryProfileForRequest(req)
			r.emitEvent(marshalRunEvent("child_tool_request", ToolRequestEvent{
				Step:              step,
				Agent:             child.agentName(),
				ChildInvocationID: lifecycle.InvocationID,
				ChildContractID:   lifecycle.ChildContractID,
				Raw:               marshalToolRequestRaw(req),
				Request:           req,
				Mode:              r.Workspace.Mode(),
				Broad:             workspace.IsBroadInspectionRequest(req),
				Narrowing:         workspace.IsNarrowingRequest(req),
				Action:            toolActionClass(req),
				DiscoveryClass:    profile.Class,
				DiscoveryScope:    profile.Scope,
				DiscoveryCost:     profile.Cost,
			}))
			r.emitEvent(marshalRunEvent("child_tool_result", ToolResultEvent{
				Step:              step,
				Agent:             child.agentName(),
				ChildInvocationID: lifecycle.InvocationID,
				ChildContractID:   lifecycle.ChildContractID,
				Result:            result,
			}))

			childResults = append(childResults, result)
			fallbackSourceReads++

			continue
		}

		break
	}

	if blockerKind, _ := child.finalAnswerBlockerKind(answer, childResults); strings.TrimSpace(answer) == "" || strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") || blockerKind != "" {
		if synthesized, ok := synthesizeDelegatedReadOnlyAnswer(prompt, childResults); ok {
			if blockerKind, _ := child.finalAnswerBlockerKind(synthesized, childResults); blockerKind == "" {
				answer = synthesized
			}
		} else if fallback, ok := deterministicNoToolsAnswer(child.agentName(), child.RuntimeBrief, childResults); ok {
			if blockerKind, _ := child.finalAnswerBlockerKind(fallback, childResults); blockerKind == "" {
				answer = fallback
			}
		}
	}

	answer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)
	answer = normalizeDelegatedReadOnlyPacket(answer)

	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  strings.TrimSpace(answer),
	}

	if packet, ok := buildReadOnlyEvidencePacket(prompt, answer, childResults); ok {
		result.Output = renderReadOnlyEvidencePacketForToolOutput(packet, answer)
		result = readOnlyEvidencePacketToolResult(result, packet)
	}

	r.emitReadOnlyChildTerminal(lifecycle, result, childResults, autonomy.ChildTerminalCompleted)

	return result
}

func (r *Runner) delegatedChildFallbackSourceRead(ctx context.Context, prompt string, results []workspace.ToolResult) (workspace.ToolRequest, workspace.ToolResult, bool) {
	if r == nil || r.Workspace == nil {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	paths := newDelegatedDiscoveryPlan(prompt).consumableSourceBodyCandidatePaths(results, 6)
	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" || IsNonSourceEvidenceArtifactPath(path) || newToolResultEvidenceProjection(results).HasSourceBody(path) {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         160,
			NamedGap:        "source-body evidence for delegated read-only synthesis",
			WhyNotAnswering: "the child has discovery evidence but needs one source-body read before a grounded answer",
		}
		result := r.Workspace.Execute(ctx, req)

		if strings.TrimSpace(result.Error) != "" || len(newToolResultEvidenceRecord(result).SourceBodyPaths()) == 0 {
			continue
		}

		return req, result, true
	}

	return workspace.ToolRequest{}, workspace.ToolResult{}, false
}

func delegatedChildFallbackSourceReadNeeded(prompt string, results []workspace.ToolResult) bool {
	return len(newToolResultEvidenceProjection(results).ReadOnlySourceBodyEvidencePaths()) == 0
}

func normalizeReadOnlyChildPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}

	lines := strings.Split(prompt, "\n")
	kept := make([]string, 0, len(lines))

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if len(kept) == 0 {
			line = normalizeReadOnlyChildLead(line)
			kept = append(kept, line)

			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "answer ") ||
			strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") {

			kept = textutil.AppendUnique(kept, line)
		}
	}

	if namedPaths := pathparse.MentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		kept = textutil.AppendUnique(kept, "Named workspace scope: "+strings.Join(namedPaths, ", "))
	}

	if keyTerms := readOnlyChildKeyTerms(prompt); len(keyTerms) > 0 {
		kept = textutil.AppendUnique(kept, "Key terms: "+strings.Join(keyTerms, ", "))
	}

	if len(kept) == 0 {
		return prompt
	}

	return strings.Join(kept, "\n")
}

func (r *Runner) prepareReadOnlyChildPrompt(prompt string) string {
	prompt = normalizeReadOnlyChildPrompt(prompt)
	if prompt == "" {
		return ""
	}

	if !readOnlyChildPromptHasPrimaryNamedScope(prompt) && r != nil && r.Workspace != nil {
		if scope := delegatedReadOnlyLikelySourceScope(prompt, r.Workspace.Root()); scope != "" {
			prompt += "\nNamed workspace scope: " + scope
		}
	}

	return r.augmentReadOnlyChildPrompt(prompt)
}

func readOnlyChildPromptHasPrimaryNamedScope(prompt string) bool {
	return newDelegatedDiscoveryPlan(prompt).hasPrimaryScope
}

func normalizeReadOnlyChildLead(line string) string {
	line = strings.TrimSpace(line)

	lower := strings.ToLower(line)

	for _, prefix := range []string{
		"explore this codebase and ",
		"explore the codebase and ",
		"inspect this codebase and ",
		"inspect the codebase and ",
	} {
		if strings.HasPrefix(lower, prefix) {
			remainder := strings.TrimSpace(line[len(prefix):])
			if remainder == "" {
				return line
			}

			return strings.ToUpper(remainder[:1]) + remainder[1:]
		}
	}

	if idx := strings.Index(line, "?"); idx >= 0 {
		question := strings.TrimSpace(line[:idx+1])
		if question != "" {
			return question
		}
	}

	if narrowed := rewriteBroadReadOnlyChildLead(line); narrowed != "" {
		return narrowed
	}

	if trimmed := trimReadOnlyChildLeadAtBroadMarker(line); trimmed != "" {
		return trimmed
	}

	return line
}

func rewriteBroadReadOnlyChildLead(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !ContainsAny(lower,
		"cmd directory",
		"entry point",
		"entrypoint",
		"main entry",
		"look for pattern",
		"focus on ",
		"explore ",
	) {

		return ""
	}

	for _, marker := range []string{"explain ", "describe ", "summarize ", "how ", "what ", "why "} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			rewritten := strings.TrimSpace(line[idx:])
			if rewritten == "" {
				continue
			}

			return strings.ToUpper(rewritten[:1]) + rewritten[1:]
		}
	}

	return ""
}

func trimReadOnlyChildLeadAtBroadMarker(line string) string {
	lower := strings.ToLower(line)
	cut := -1

	for _, marker := range []string{
		" explore the ",
		" explore ",
		" look for ",
		" focus on ",
		" specifically ",
		" emphasize ",
		" start from ",
		" trace through ",
	} {
		if idx := strings.Index(lower, marker); idx > 0 && (cut == -1 || idx < cut) {
			cut = idx
		}
	}

	if cut <= 0 {
		return ""
	}

	trimmed := strings.TrimSpace(strings.TrimRight(line[:cut], ",;:- "))
	if trimmed == "" {
		return ""
	}

	return trimmed
}

func readOnlyChildKeyTerms(prompt string) []string {
	return newDelegatedDiscoveryPlan(prompt).promptKeyTerms
}

func isWriteChildMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "write", "workbranch", "workbranch_write", "isolated_write", "patch":
		return true
	default:
		return false
	}
}

func (r *Runner) runWorkbranchChildTask(ctx context.Context, req workspace.ToolRequest, prompt string, opts llama.ChatOptions) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(r.Workspace.Root()) == "" {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task rejected", Error: "parent workspace root is required for isolated write child tasks"}
	}

	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "worker"
	}

	manager := Manager{Root: r.Workspace.Root()}

	checkout, err := manager.Create(ctx, agentName)
	if err != nil {
		return workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: err.Error()}
	}

	defer func() {
		if err := manager.Remove(context.Background(), checkout); err != nil {
			r.emitEvent(marshalRunEvent("child_workbranch_cleanup_error", map[string]string{
				"path":  checkout.Path,
				"error": err.Error(),
			}))
		}
	}()

	r.emitEvent(marshalRunEvent("child_workbranch", checkout))

	childWorkspaceDescription := "temporary git worktree"
	if strings.TrimSpace(checkout.Mode) == "filesystem-copy" {
		childWorkspaceDescription = "temporary isolated filesystem copy"
	}

	childWorkspace := workspace.NewManager(config.Workspace{
		Root:                  checkout.Path,
		CWD:                   checkout.Path,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 120,
		MaxOutputBytes:        131072,
		MaxFileBytes:          262144,
		MaxDirectoryEntries:   2000,
		EnableShell:           true,
		EnableWrites:          true,
	})
	childPolicy := RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 4, MaxModelCalls: 16}

	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}

	childClient := r.Client
	if r.ImplementerClient != nil {
		childClient = r.ImplementerClient
	}
	lifecycle := r.beginWorkbranchChildLifecycle(agentName, opts, childPolicy, childClient)

	child := &Runner{
		Client:           childClient,
		Workspace:        childWorkspace,
		Policy:           childPolicy,
		AgentName:        "child_write:" + agentName,
		PermissionPolicy: workbranchChildTaskPermissionPolicy(),
		TaskLedger:       TaskLedgerPolicy{Required: true},
		DisableTaskTool:  true,
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		RuntimeBrief:     workerruntime.RuntimeBriefForRole("child_write", "child_write:"+agentName, prompt),
		OnEvent: func(event RunEvent) {
			if strings.TrimSpace(event.Role) == "" {
				return
			}

			r.emitEvent(RunEvent{Role: "child_" + event.Role, Content: event.Content})
		},
	}
	answer, runErr := r.runWorkbranchChildModel(ctx, child, lifecycle, childWorkspaceDescription, childWorkspace.Instructions(), prompt, opts)
	diff, diffErr := manager.Diff(ctx, checkout)

	if runErr != nil {
		result := workspace.ToolResult{Tool: "task", Summary: "Workbranch child task failed", Error: runErr.Error(), Output: renderWorkbranchChildOutput(checkout, diff, answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	if diffErr != nil {
		result := workspace.ToolResult{Tool: "task", Summary: "Workbranch child diff failed", Error: diffErr.Error(), Output: strings.TrimSpace(answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	result := workspace.ToolResult{
		Tool:    "task",
		Summary: fmt.Sprintf("Workbranch child task completed with %d changed path(s)", len(diff.ChangedPaths)),
		Output:  renderWorkbranchChildOutput(checkout, diff, answer),
	}
	r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalCompleted)
	return result
}

func (r *Runner) runWorkbranchChildModel(ctx context.Context, child *Runner, lifecycle childLifecycleContext, childWorkspaceDescription string, childWorkspaceInstructions string, prompt string, opts llama.ChatOptions) (answer string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("workbranch child task panic: %v", recovered)
		}
	}()

	return child.Run(
		ctx,
		[]llama.ChatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a bounded isolated implementation child task running in a " + childWorkspaceDescription + ".",
					"Make only the delegated change. Do not push, publish, deploy, or spawn more tasks.",
					"Keep task_ledger current. Verify the changed surface when practical.",
					"Final answer must include changed files, verification status, and confidence.",
					childWorkspaceInstructions,
				}, "\n"),
			},
			{Role: "user", Content: prompt},
		},
		opts,
		nil,
		func(event ToolRequestEvent) {
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_request", event))
		},
		func(event ToolResultEvent) {
			event.ChildInvocationID = lifecycle.InvocationID
			event.ChildContractID = lifecycle.ChildContractID
			r.emitEvent(marshalRunEvent("child_tool_result", event))
		},
	)
}

func renderWorkbranchChildOutput(checkout Checkout, diff Diff, answer string) string {
	var b strings.Builder

	b.WriteString("Isolated workbranch result\n")
	b.WriteString("branch: " + checkout.Branch + "\n")
	b.WriteString("base: " + checkout.BaseSHA + "\n")

	if len(diff.ChangedPaths) > 0 {
		b.WriteString("changed paths:\n")

		for _, path := range diff.ChangedPaths {
			b.WriteString("- " + path + "\n")
		}
	}

	if strings.TrimSpace(diff.Stat) != "" {
		b.WriteString("\nstat:\n")
		b.WriteString(diff.Stat + "\n")
	}

	if strings.TrimSpace(answer) != "" {
		b.WriteString("\nchild answer:\n")
		b.WriteString(strings.TrimSpace(answer) + "\n")
	}

	if strings.TrimSpace(diff.Patch) != "" {
		b.WriteString("\npatch:\n```diff\n")
		b.WriteString(strings.TrimSpace(diff.Patch))
		b.WriteString("\n```\n")
	}

	if len(diff.ChangedPaths) > 0 {
		b.WriteString("\nParent must inspect this diff and explicitly apply or reject it; the main workspace was not mutated by the child.\n")
	}

	return strings.TrimSpace(b.String())
}

func readOnlyChildTaskPermissionPolicy() toolpolicy.Policy {
	return childTaskPermissionPolicy(childAllowedTools(), workspace.ReadOnlyChildShellPolicy())
}

func workbranchChildTaskPermissionPolicy() toolpolicy.Policy {
	spec := workerruntime.RoleSpecFor("child_write")
	return childTaskPermissionPolicy(spec.AllowedTools, workspace.WorkbranchChildShellPolicy(spec.ForbiddenTools), workspace.ToolNameTask)
}

func childTaskPermissionPolicy(allowedTools []string, shellPolicy workspace.ChildShellPolicy, deniedTools ...string) toolpolicy.Policy {
	rules := make([]toolpolicy.Rule, 0, len(allowedTools)+len(shellPolicy.AllowedPrefixes)+len(shellPolicy.DeniedPrefixes)+len(shellPolicy.DeniedFragments)+len(deniedTools))

	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool == "" || tool == workspace.ToolNameShell {
			continue
		}

		rules = append(rules, toolpolicy.Rule{Tool: tool, Decision: toolpolicy.Allow})
	}

	for _, prefix := range shellPolicy.AllowedPrefixes {
		rules = append(rules, toolpolicy.Rule{Tool: workspace.ToolNameShell, CommandPrefix: prefix, Decision: toolpolicy.Allow})
	}

	for _, tool := range deniedTools {
		rules = append(rules, toolpolicy.Rule{Tool: tool, Decision: toolpolicy.Deny})
	}

	for _, prefix := range shellPolicy.DeniedPrefixes {
		rules = append(rules, toolpolicy.Rule{Tool: workspace.ToolNameShell, CommandPrefix: prefix, Decision: toolpolicy.Deny})
	}

	for _, value := range shellPolicy.DeniedFragments {
		rules = append(rules, toolpolicy.Rule{Tool: workspace.ToolNameShell, CommandContains: value, Decision: toolpolicy.Deny})
	}

	return toolpolicy.Policy{
		Default: toolpolicy.Deny,
		Rules:   rules,
	}
}

func childAllowedTools() []string {
	return workerruntime.RoleSpecFor("child_read_only").AllowedTools
}

// runDelegateEdit handles a delegate_edit tool call from the planner.
//
// The point of delegate_edit is to give the planner a separate mental
// hook for "the actual edit is someone else's job — I just specify what
// changes." That break alone helps reduce the planner's verification
// urge: once the planner stops thinking of itself as the executor, it
// stops feeling responsible for confirming each landed edit.
//
// The implementation routes the planner's edit spec through a single
// chat completion against ImplementerClient (the configured executor-
// role backend, sourced from .idiom/backends.json or cfg.Implementer).
// The executor's job is narrow: read the spec, emit a single edit_file
// or apply_patch tool-call JSON, and stop. The runner parses that JSON, dispatches
// the underlying edit through Workspace.Execute, and returns the real
// edit result back to the planner. If the executor fails for any
// reason — model error, malformed reply, missing client — the runner
// falls back to dispatching the planner's spec directly so the lane
// still makes progress.
//
// Returned ToolResult carries Tool="delegate_edit" so the audit trail
// and required_edit_paths_progress treat it as the model's own edit
// (it IS, semantically — the planner asked for it, the executor
// rubber-stamped or refined it).
func (r *Runner) runDelegateEdit(ctx context.Context, req workspace.ToolRequest) workspace.ToolResult {
	if reject := validateDelegateEditRequest(req); reject != nil {
		return *reject
	}

	executor := r.delegateEditExecutorClient()
	if executor == nil {
		// No executor available — direct dispatch keeps the lane
		// honest. The planner still sees a delegate_edit-shaped result.
		return r.dispatchDelegatedEditDirectly(ctx, req, "no executor client configured")
	}

	systemPrompt, userPrompt := delegateEditPrompts(req)
	executorOpts := llama.ChatOptions{
		Temperature: 0.2,
		TopP:        0.95,
		MaxTokens:   2048,
	}
	lifecycle := r.beginDelegateEditLifecycle(req, executorOpts, false, executor)

	reply, err := executor.CompleteChatMessage(ctx, []llama.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, executorOpts)
	if err != nil {
		r.emitEvent(RunEvent{Role: "delegate_edit_executor_error", Content: err.Error()})
		r.emitDelegateEditExecutorFailureLifecycle(lifecycle, err)

		return r.dispatchDelegatedEditDirectly(ctx, req, "executor error: "+err.Error())
	}

	childReq, parseErr := parseDelegateEditReply(reply.Content, req)
	if parseErr != nil {
		r.emitEvent(RunEvent{Role: "delegate_edit_parse_error", Content: parseErr.Error()})
		r.emitDelegateEditParseFailureLifecycle(lifecycle, req, parseErr)

		return r.dispatchDelegatedEditDirectly(ctx, req, "executor reply unparseable: "+parseErr.Error())
	}

	r.emitEvent(RunEvent{
		Role:    "delegate_edit_executor_call",
		Content: fmt.Sprintf("executor produced %s for %s", childReq.Tool, childReq.Path),
	})

	result := r.Workspace.Execute(ctx, childReq)

	wrapped := wrapDelegateEditResult(result, "executor: "+childReq.Tool)
	r.emitDelegateEditLifecycle(lifecycle, req, wrapped, autonomy.ChildRouteParentExecutor, "executor_route")
	return wrapped
}

// validateDelegateEditRequest rejects malformed delegate_edit calls
// before the executor round-trip. The contract: a path is required,
// and the spec must be either old/new substring shape or
// start/end/content range shape.
func validateDelegateEditRequest(req workspace.ToolRequest) *workspace.ToolResult {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return &workspace.ToolResult{
			Tool:    "delegate_edit",
			Summary: "delegate_edit rejected",
			Error:   "delegate_edit requires a path",
		}
	}

	hasSubstringShape := strings.TrimSpace(req.OldText) != "" && strings.TrimSpace(req.NewText) != ""
	hasRangeShape := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""
	hasReplaceShape := strings.TrimSpace(req.NewText) != "" && req.ReplaceAll

	if !hasSubstringShape && !hasRangeShape && !hasReplaceShape {
		return &workspace.ToolResult{
			Tool:    "delegate_edit",
			Summary: "delegate_edit rejected",
			Error:   "delegate_edit requires either old_text+new_text (substring shape) or start_line+end_line+content (range shape)",
		}
	}

	return nil
}

// delegateEditExecutorClient picks the executor for a delegate_edit
// call. ImplementerClient is the canonical executor (configured to
// point at the 30B-coder backend in production). When unset the
// runner has no separate executor — see runDelegateEdit's fallback.
func (r *Runner) delegateEditExecutorClient() llama.LLMClient {
	if r == nil {
		return nil
	}

	if r.ImplementerClient != nil {
		return r.ImplementerClient
	}

	// Deliberately do NOT fall back to r.Client here. The whole point
	// of delegate_edit is to break out of the planner's context; if
	// the planner and executor share a client, we lose that benefit.
	// The caller's fallback path will dispatch the spec directly.
	return nil
}

// delegateEditPrompts renders the two-message executor prompt. The
// system message anchors the executor's role; the user message is
// the literal edit spec.
func delegateEditPrompts(req workspace.ToolRequest) (string, string) {
	system := strings.Join([]string{
		"You are the local executor for a single file edit.",
		"The planner has already decided what to change and where; your only job is to emit the workspace tool call that performs that edit.",
		"Reply with one and ONLY one JSON tool call, on its own. No prose, no commentary, no markdown fences.",
		"Use edit_file when given an old_text/new_text or start_line/end_line/content spec.",
		"Use apply_patch only if the spec carries a `patch` field with a *** Begin Patch envelope.",
		"Do not invent fields, do not change paths, do not rephrase old_text. Output the exact JSON the workspace expects.",
	}, " ")
	specBytes, _ := json.MarshalIndent(map[string]any{
		"path":        strings.TrimSpace(req.Path),
		"old_text":    req.OldText,
		"new_text":    req.NewText,
		"start_line":  req.StartLine,
		"end_line":    req.EndLine,
		"content":     req.Content,
		"replace_all": req.ReplaceAll,
		"description": strings.TrimSpace(req.Purpose),
	}, "", "  ")
	user := "Apply this edit by emitting the workspace tool call. Spec:\n" + string(specBytes)

	return system, user
}

// parseDelegateEditReply extracts a workspace.ToolRequest from the
// executor's reply. The reply is expected to be a single JSON object
// shaped like an edit_file or apply_patch ToolRequest. Anything else
// (fenced blocks, prose, multiple tool calls) is rejected so the
// fallback path takes over.
func parseDelegateEditReply(content string, planSpec workspace.ToolRequest) (workspace.ToolRequest, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return workspace.ToolRequest{}, fmt.Errorf("executor returned empty content")
	}

	if fence := stripJSONFence(trimmed); fence != "" {
		trimmed = fence
	}

	var req workspace.ToolRequest

	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return workspace.ToolRequest{}, fmt.Errorf("executor reply is not valid JSON: %w", err)
	}

	switch strings.TrimSpace(req.Tool) {
	case "edit_file", "apply_patch", "write_file", "append_file":
		// OK — these are write tools the workspace knows how to
		// dispatch.
	default:
		return workspace.ToolRequest{}, fmt.Errorf("executor selected disallowed tool %q (must be edit_file/apply_patch/write_file/append_file)", req.Tool)
	}

	if req.Tool == "edit_file" {
		// Defensive: the executor should not change the path — the
		// planner controls that. If it did, fall back to the planner
		// spec's path.
		if strings.TrimSpace(req.Path) == "" {
			req.Path = strings.TrimSpace(planSpec.Path)
		}
	}

	return req, nil
}

// stripJSONFence handles executors that wrap JSON in ```json fences
// despite the system instruction. Returns "" if no fence pattern is
// detected (caller falls back to the original string).
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)

	rest, ok := strings.CutPrefix(s, "```")

	if !ok {
		return ""
	}

	rest = strings.TrimPrefix(rest, "json")
	rest = strings.TrimLeft(rest, "\r\n ")

	end := strings.LastIndex(rest, "```")

	if end < 0 {
		return ""
	}

	return strings.TrimSpace(rest[:end])
}

// dispatchDelegatedEditDirectly translates the planner's spec into a
// real edit_file ToolRequest and dispatches it without going through
// the executor. Used as the fallback path when the executor is
// unavailable, errors out, or returns garbage.
func (r *Runner) dispatchDelegatedEditDirectly(ctx context.Context, req workspace.ToolRequest, reason string) workspace.ToolResult {
	r.emitEvent(RunEvent{
		Role:    "delegate_edit_direct_dispatch",
		Content: fmt.Sprintf("dispatching planner spec directly (%s)", reason),
	})
	lifecycle := r.beginDelegateEditLifecycle(req, llama.ChatOptions{}, true, nil)

	// edit_file's contract requires Description (the autodoc reads it
	// for the plan-panel autodoc). Forward whichever of the planner's
	// purpose/description fields is non-empty so the executor sees
	// the same intent the planner wrote.
	intent := strings.TrimSpace(req.Purpose)
	if intent == "" {
		intent = strings.TrimSpace(req.Description)
	}

	if intent == "" {
		intent = "delegate_edit fallback: planner spec dispatched directly"
	}

	direct := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        req.Path,
		OldText:     req.OldText,
		NewText:     req.NewText,
		StartLine:   req.StartLine,
		EndLine:     req.EndLine,
		Content:     req.Content,
		ReplaceAll:  req.ReplaceAll,
		Description: intent,
	}
	result := r.Workspace.Execute(ctx, direct)

	wrapped := wrapDelegateEditResult(result, "direct: "+reason)
	r.emitDelegateEditLifecycle(lifecycle, req, wrapped, autonomy.ChildRouteDeterministicDirect, "direct_dispatch")
	return wrapped
}

// wrapDelegateEditResult re-labels the underlying edit's ToolResult so
// audit trails see delegate_edit consistently. The Summary keeps the
// underlying message so debugging is still straightforward, and the
// rendered Output shows which path the request took (executor vs
// direct).
func wrapDelegateEditResult(result workspace.ToolResult, route string) workspace.ToolResult {
	wrapped := result

	wrapped.Tool = "delegate_edit"

	if strings.TrimSpace(wrapped.Summary) == "" {
		wrapped.Summary = "delegate_edit completed"
	}

	header := fmt.Sprintf("delegated_via: %s", route)
	if strings.TrimSpace(wrapped.Output) == "" {
		wrapped.Output = header
	} else {
		wrapped.Output = header + "\n\n" + wrapped.Output
	}

	return wrapped
}

func allowsDocumentationOnlyPacket(objective string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	return ContainsAny(lower, " readme ", " doc ", " docs ", " documentation ", " markdown ")
}

func augmentDelegatedReadOnlyPacket(answer string, objective string, results []workspace.ToolResult) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	projection := newToolResultEvidenceProjection(results)
	var sourceBodyPaths []string
	for _, fact := range projection.ReadOnlyDelegatedEvidenceFacts() {
		decision := readOnlyDelegatedEvidenceDecisionForObjective(objective, fact)
		sourceBodyPaths = append(sourceBodyPaths, decision.ReadinessSourcePaths...)
	}
	sourceBodyPaths = readOnlySourceLikePaths(sourceBodyPaths, 16)
	paths := projection.delegatedClosureEvidencePaths(objective, 8)
	mentioned := normalizePostChildEvidencePaths(pathparse.MentionedWorkspacePaths(answer))
	missing := make([]string, 0, len(paths))
	mentionedSet := make(map[string]struct{}, len(mentioned))

	for _, path := range mentioned {
		mentionedSet[path] = struct{}{}
	}

	for _, path := range paths {
		if _, ok := mentionedSet[path]; ok {
			continue
		}

		missing = append(missing, path)
	}

	candidates := projection.closureProbeCandidates(4)
	missingCandidates := make([]string, 0, len(candidates))
	lowerAnswer := strings.ToLower(answer)
	hasSourceBodyEvidenceSection := strings.Contains(lowerAnswer, "source-body evidence paths:") ||
		strings.Contains(lowerAnswer, "source body evidence paths:")

	for _, candidate := range candidates {
		if strings.Contains(lowerAnswer, strings.ToLower(candidate)) {
			continue
		}

		missingCandidates = append(missingCandidates, candidate)
	}

	if len(missing) == 0 && len(missingCandidates) == 0 {
		if len(sourceBodyPaths) == 0 || hasSourceBodyEvidenceSection {
			return answer
		}
	}

	var b strings.Builder

	b.WriteString(answer)

	if len(sourceBodyPaths) > 0 && !hasSourceBodyEvidenceSection {
		b.WriteString("\n\nSource-body evidence paths:\n")

		for _, path := range textutil.Prefix(sourceBodyPaths, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(missing) > 0 {
		b.WriteString("\n\nAdditional inspected paths:\n")

		for _, path := range textutil.Prefix(missing, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(missingCandidates) > 0 {
		b.WriteString("\nExact probes already inspected:\n")

		for _, candidate := range textutil.Prefix(missingCandidates, 3) {
			b.WriteString("- `")
			b.WriteString(candidate)
			b.WriteString("`\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func synthesizeDelegatedReadOnlyAnswer(objective string, results []workspace.ToolResult) (string, bool) {
	objective = strings.TrimSpace(objective)

	useful := make([]workspace.ToolResult, 0, len(results))
	answerabilityResults := make([]workspace.ToolResult, 0, len(results))
	sanitizedResults := make([]workspace.ToolResult, 0, len(results))

	for _, result := range results {
		result = normalizeFallbackResult(result)
		answerabilityResults = append(answerabilityResults, result)

		if strings.TrimSpace(result.Tool) == workspace.ToolNameTask {
			if fact, ok := readOnlyDelegatedEvidenceFactFromResult(result); ok && fact.HasPacket &&
				!readOnlyDelegatedEvidenceDecisionForObjective(objective, fact).TaskResultAnswerable {

				continue
			}
		}

		sanitizedResult := result

		sanitizedResult.Output = sanitizeDelegatedReadOnlyResultOutput(result)
		sanitizedResults = append(sanitizedResults, sanitizedResult)

		if strings.TrimSpace(sanitizedResult.Error) != "" || strings.TrimSpace(sanitizedResult.Output) == "" || !isResultUsefulForFallback(sanitizedResult) {
			continue
		}

		useful = append(useful, sanitizedResult)
	}

	if answer, ok := synthesizeReadOnlyDelegatedEvidenceAnswer(objective, newToolResultEvidenceProjection(answerabilityResults).ReadOnlyDelegatedEvidenceFacts()); ok {
		return answer, true
	}

	answerability := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         objective,
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}, answerabilityResults)
	sourceBodyPaths := answerability.RelevantSourceBodyPaths

	if len(sourceBodyPaths) == 0 {
		return "", false
	}

	projection := newToolResultEvidenceProjection(sanitizedResults)
	paths := projection.delegatedReadOnlyFallbackEvidencePaths(answerability, 8)

	if len(paths) == 0 {
		return "", false
	}

	if answer, ok := synthesizeDelegatedDiscoveryAnswer(objective, sanitizedResults, paths); ok {
		if !looksLikeNonAnswerProse(answer) {
			return answer, true
		}
	}

	if answer, ok := synthesizeDelegatedSourceBodyFactsAnswer(objective, sanitizedResults, sourceBodyPaths, paths); ok {
		return answer, true
	}

	lines := delegatedReadOnlyFindingLines(useful, sourceBodyPaths, 4)
	if len(lines) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("The answer is grounded in ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")

	if question := delegatedDisplayQuestion(objective); question != "" {
		b.WriteString(" Use those findings as the source of truth for `")
		b.WriteString(question)
		b.WriteString("`.")
	}

	b.WriteString("\n\nWhat this establishes:\n")

	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String()), true
}

func synthesizeDelegatedSourceBodyFactsAnswer(objective string, results []workspace.ToolResult, sourceBodyPaths []string, evidencePaths []string) (string, bool) {
	sourceBodySet := readOnlyPathSet(sourceBodyPaths)

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile {
			continue
		}

		paths := newToolResultEvidenceRecord(result).ReadPaths()
		if len(paths) == 0 {
			continue
		}

		path := NormalizeRunnerPath(paths[0])
		if _, ok := sourceBodySet[path]; !ok {
			continue
		}

		factAnswer, ok := presentableSourceBodyFactsAnswer(objective, path, result.Output)
		if !ok || looksLikeNonAnswerProse(factAnswer) {
			continue
		}

		factText := stripPresentableSourceObjectiveEcho(factAnswer)
		if looksLikeLowValueSourceInventoryFallback(factText) {
			continue
		}

		if strings.TrimSpace(objective) != "" && !delegatedSourceFactAddressesObjective(factText, objective) {
			continue
		}

		var b strings.Builder

		b.WriteString("The answer is grounded in ")
		b.WriteString(formatInlinePathList(evidencePaths, 4))
		b.WriteString(".")

		if question := delegatedDisplayQuestion(objective); question != "" {
			b.WriteString(" Use those findings as the source of truth for `")
			b.WriteString(question)
			b.WriteString("`.")
		}

		b.WriteString("\n\nWhat this establishes:\n")
		b.WriteString("- ")
		b.WriteString(factText)
		b.WriteString("\n")

		return strings.TrimSpace(b.String()), true
	}

	return "", false
}

func delegatedSourceFactAddressesObjective(factText string, objective string) bool {
	factText = strings.ToLower(strings.TrimSpace(factText))
	if factText == "" {
		return false
	}

	for _, term := range RecoveryObjectiveSearchWords(objective, 32) {
		term = SingularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 || delegatedSourceFactGenericObjectiveTerm(term) {
			continue
		}

		if strings.Contains(factText, term) {
			return true
		}
	}

	return false
}

func delegatedSourceFactGenericObjectiveTerm(term string) bool {
	switch term {
	case "internal", "agent", "runner", "source", "evidence", "inspect", "inspected", "path", "paths", "report", "file", "code", "explain", "harness":
		return true
	default:
		return false
	}
}

func stripDelegatedReadOnlyInternalPacket(output string) string {
	var kept []string

	skipList := false

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			skipList = false

			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}

			continue
		}

		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "question:"),
			strings.HasPrefix(lower, "source:"),
			strings.HasPrefix(lower, "confidence:"):
			skipList = false
			continue
		case lower == "inspected paths:" ||
			lower == "additional inspected paths:" ||
			lower == "exact probes already inspected:":
			skipList = true
			continue
		case lower == "key findings:":
			skipList = false
			continue
		case skipList && strings.HasPrefix(line, "- "):
			continue
		case strings.HasPrefix(line, "<<QUESTION>>:"),
			strings.Contains(lower, "completion gate rejected"),
			strings.Contains(lower, "missing completion evidence"),
			strings.Contains(lower, "missing: inspected_paths"),
			strings.Contains(lower, "final answer looks like transport-level noise"):
			skipList = false
			continue
		}

		kept = append(kept, line)
	}

	return strings.TrimSpace(strings.Join(collapseBlankRunnerLines(kept), "\n"))
}

func collapseBlankRunnerLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank || len(out) == 0 {
				continue
			}

			blank = true

			out = append(out, "")

			continue
		}

		blank = false

		out = append(out, line)
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	return out
}

func delegatedReadOnlyPacketLineLabels() []string {
	return []string{
		"source-body evidence paths:",
		"source body evidence paths:",
		"source body paths:",
		"grounded findings:",
		"grounded answer:",
		"key findings:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"core files:",
		"primary files:",
		"important flow:",
	}
}

func readOnlyToolControlGuidanceText(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}

	return ContainsAny(lower,
		"generated evidence artifact",
		"generated discovery/context json is not source-body evidence",
		"not source-body evidence",
		"use the source paths already named by the current discovery packet",
		"next exact source probe example",
		"after one source-body read resolves the claim",
		"redirected to ",
		"try: {\"tool\":",
		"retry your original request and it will execute",
	)
}

func synthesizeDelegatedDiscoveryAnswer(objective string, results []workspace.ToolResult, paths []string) (string, bool) {
	summaries := delegatedDiscoverySummaries(results)
	question := delegatedDisplayQuestion(objective)
	evidencePaths := delegatedDiscoveryAnswerEvidencePaths(question, paths)

	return AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      question,
		EvidencePaths: evidencePaths,
		Summaries:     summaries,
	})
}

func delegatedDiscoverySummaries(results []workspace.ToolResult) []delegatedDiscoverySummary {
	var summaries []delegatedDiscoverySummary

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameDiscoveryQuery || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		var summary delegatedDiscoverySummary

		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}

		if summary.ArtifactPath == "" && len(summary.Evidence) == 0 {
			continue
		}

		summaries = append(summaries, summary)
	}

	return summaries
}

func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}

	return prioritizedDelegatedEvidencePaths(paths, len(paths))
}

func prioritizedDelegatedEvidencePaths(paths []string, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)

	if len(paths) <= 1 {
		return paths
	}

	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])

		return pi < pj
	})

	return textutil.Prefix(paths, max)
}

func delegatedDiscoverySummaryIsBroadCountOnly(lower string) bool {
	if !strings.Contains(lower, "matching line") {
		return false
	}

	if strings.Contains(lower, "implementation path") ||
		strings.Contains(lower, "implementation anchor") ||
		strings.Contains(lower, "source anchor") {

		return false
	}

	return true
}

func closurePromptEvidencePaths(paths []string, max int) []string {
	return filteredClosurePromptEvidencePaths(preferNonTestEvidencePaths(uniqueDelegatedDiscoveryPaths(paths)), max)
}

func filteredClosurePromptEvidencePaths(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}

		cleaned = append(cleaned, path)
	}

	return textutil.Prefix(cleaned, max)
}

func isDelegatedDiscoveryContextDigestLayer(name string) bool {
	switch strings.TrimSpace(name) {
	case "project_behavior_digest", "project_context_digest":
		return true
	default:
		return false
	}
}

func exactAnchorPreWriteSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !exactAnchorImplementationActive(brief) {
		return false
	}

	return len(brief.ChangedPaths) == 0 && len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0
}

func implementationScopedEditRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(brief.AllowedTools, nil)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSemanticDiscoveryOrNarrowReadForNamedGap}
	brief.Constraints = textutil.Merge([]string{"implementation_required_path_scoped_surface"}, brief.Constraints)
	brief.RequiredNext = textutil.Merge([]string{
		"required-path edit, semantic/context/project discovery or narrow required-path read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = textutil.Merge([]string{
		"task_ledger or broad discovery before resolving the required-path contract",
		"raw read/search outside required edit paths before resolving the required-path contract",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"required edit paths define the implementation scope",
		"prefer context, language, and declared read-only project discovery tools for named edit-safety gaps before raw source reads",
		"raw source reads are only for exact anchor confirmation on required paths or explicit evidence paths",
		"large range-less raw source reads on required/evidence paths are bounded by the harness; request start_line/end_line or a symbol for follow-up",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetWriteReadyRuntimeBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	pendingTools := sourceEvidencePendingRedirectTools(brief.AllowedTools, pending)
	sourceRefresh := len(pending) == 0 && implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, brief.AllowedTools)
	sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3)
	allowed := implementationTargetWriteReadyAllowedToolsForBrief(brief.AllowedTools, brief, results)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch}
	brief.Constraints = textutil.Merge([]string{"implementation_target_write_ready"}, brief.Constraints)
	brief.RequiredNext = textutil.Merge([]string{
		"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		"for delete/remove-file objectives, use apply_patch with `*** Delete File: <path>` after source evidence; do not use write_file with empty content as deletion",
	}, brief.RequiredNext)

	if sourceRefresh {
		brief.RequiredNext = textutil.Merge([]string{
			"parser-readable write to the sourced target path, bounded read_file/source-scoped narrow search_content source refresh for a precise edit-safety fact, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"broad read/search after source-body evidence",
			"read/search for already-sourced paths unless the request adds a bounded missing range or exact named_gap",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"use bounded source refresh only when an exact missing edit-safety fact remains; otherwise write or block",
			"read_file refreshes should use a bounded range; search_content refreshes must stay scoped to source code with a narrow pattern, and unscoped searches must name the gap",
		}, brief.SearchPolicy)
		brief.Constraints = textutil.Merge([]string{"bounded_source_refresh_allowance"}, brief.Constraints)
	} else {
		brief.InvalidActions = textutil.Merge([]string{
			"broad or redundant read/search after source-body evidence",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"write when the edit-safety facts are known; otherwise use only a bounded probe that names the missing fact",
		}, brief.SearchPolicy)
	}

	if sourcedSummary != "" {
		brief.RequiredNext = textutil.Merge([]string{
			"write using the already-read source-body evidence for: " + sourcedSummary,
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"redundant read/search/navigation for already-sourced path(s) without a bounded missing range or exact named_gap: " + sourcedSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"already-sourced path(s) should be written from current evidence unless a bounded missing range or exact named_gap remains: " + sourcedSummary,
		}, brief.SearchPolicy)
		brief.Constraints = textutil.Merge([]string{"source_body_evidence_paths: " + sourcedSummary}, brief.Constraints)
	}

	if len(pendingTools) > 0 {
		pendingSummary := sourceEvidencePendingRedirectSummary(pending, 3)
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)

		brief.RequiredTools = []string{workspace.RequiredToolExactPendingSourceBodyFollowupOrEdit}
		brief.RequiredNext = textutil.Merge([]string{
			"resolve the pending redirected source-body obligation (" + pendingActionSummary + "), then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"broad read/search while a redirected source-body obligation is pending",
			"read/navigation for any path except the exact pending redirected source-body follow-up: " + pendingActionSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"one redirected source-body obligation remains open: " + pendingActionSummary,
			"use only the pending redirect's symbol/body follow-up, with at most one same-path outline first when the symbol is unknown; do not use references, broad discovery, or unrelated navigation",
		}, brief.SearchPolicy)
		brief.Constraints = textutil.Merge([]string{"pending_redirected_source_body_obligation: " + pendingSummary}, brief.Constraints)
	}

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetSourceRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := workspace.ToolSurfacePhaseAllowedTools(brief.AllowedTools, workspace.ToolSurfacePhaseImplementationTargetSource)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = []string{workspace.RequiredToolBoundedWorkspaceEvidence, workspace.RequiredToolEditFileOrApplyPatch}
	brief.Constraints = textutil.Merge([]string{"implementation_target_source_pending"}, brief.Constraints)
	brief.RequiredNext = textutil.Merge([]string{
		"localize the concrete edit target, read bounded source-body evidence with named_gap for the missing edit-safety fact, write only after exact source-body evidence and replacement shape are known, or <<QUESTION>> with the exact blocker/no-op evidence",
		"for a genuinely new (non-existent) file, write_file directly with full content; source-body evidence and replacement shape are not required for an additive new-file write",
		"for delete/remove-file objectives, read bounded existence/source evidence, then use apply_patch with `*** Delete File: <path>`; do not use write_file with empty content as deletion",
	}, brief.RequiredNext)
	brief.InvalidActions = textutil.Merge([]string{
		"final answer before a successful write, no-op, or blocker",
		"write that modifies existing source before exact source-body evidence and replacement shape",
		"write_file with empty content to simulate deleting a file",
		"verification before a successful write",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"do not run verification before the first write",
		"use bounded read/search/context only for a named missing target or edit-safety fact",
	}, brief.SearchPolicy)
	brief.ForbiddenTools = textutil.Merge(brief.ForbiddenTools, []string{workspace.ToolNameSourceSanity, workspace.ToolNameShell, workspace.ToolNameGitDiff, workspace.ToolNameGitStatus})

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func exactAnchorPreWriteRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := workspace.ToolSurfacePhaseAllowedTools(brief.AllowedTools, workspace.ToolSurfacePhaseImplementationPreWrite)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch}
	brief.Constraints = textutil.Merge([]string{"exact_anchor_prewrite_write_first"}, brief.Constraints)
	brief.RequiredNext = textutil.Merge([]string{
		"exact-anchor write capability with the supplied target-packet anchor",
		"answer <<QUESTION>> only if the supplied anchor is missing or stale",
	}, brief.RequiredNext)
	brief.InvalidActions = textutil.Merge([]string{
		"read/search/shell before first exact-anchor write",
		"verification before first exact-anchor write",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"target-packet exact anchors count as current-state path and source evidence for the first write",
		"do not inspect before the first exact-anchor write",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func (r *Runner) implementationTargetWriteReadyNonWritePrompt(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	if r == nil || !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", false
	}

	if pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results); len(pending) > 0 {
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)
		tool := strings.TrimSpace(req.Tool)

		if tool == "" {
			tool = "requested tool"
		}

		return fmt.Sprintf("%s is outside the pending redirected source-body obligation. The implementation lane is target-write-ready, but a pending source-body obligation remains: %s. Prefer that exact pending source-body follow-up, make one parser-readable write from already gathered source evidence, or answer <<QUESTION>> with the exact blocker/no-op evidence.", tool, pendingActionSummary), true
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	return fmt.Sprintf("%s was requested while the implementation target-write-ready state is active. %s", tool, implementationTargetWriteReadyRecoveryInstruction(results)), true
}

func implementationTargetWriteReadyRecoveryInstruction(results []workspace.ToolResult) string {
	sourceSentence := "Source-body evidence is already available"
	if sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3); sourcedSummary != "" {
		sourceSentence += " for: " + sourcedSummary
	}

	return sourceSentence + ". The active request is the workspace write follow-up, not another prose answer to the earlier identification question. Make a parser-readable write when the edit-safety facts are known; otherwise take a bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " probe with `named_gap` for the exact missing fact, or answer <<QUESTION>> with the exact blocker/no-op evidence."
}

func (r *Runner) implementationTargetWriteReadySourceReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if r == nil || workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	if !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		return false
	}

	if r.implementationTargetWriteReadyPendingRedirectProgressAllowed(req, results) {
		return true
	}

	if len(sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results)) > 0 {
		return false
	}

	if r.implementationTargetWriteReadyHistoryReadAllowed(req, results) {
		return true
	}

	if implementationTargetWriteReadyTargetedDiscoveryAllowed(req, results) {
		return true
	}

	path := NormalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	if newToolResultEvidenceProjection(results).HasSourceBody(path) {
		if implementationTargetWriteReadySamePathContinuationReadAllowed(req, results) {
			return true
		}

		if refreshes > 0 {
			return false
		}

		return !fullSourceReadFileSeen(path, results)
	}

	return implementationTargetWriteReadyNewTargetRead(req)
}

func implementationTargetWriteReadyTargetedDiscoveryAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchContent:
		return implementationTargetWriteReadyTargetedSearchContent(req, results)
	case workspace.ToolNameFileStats:
		return implementationTargetWriteReadyTargetedFileStats(req)
	default:
		if implementationTargetWriteReadySourceSymbolTool(req.Tool) {
			return implementationTargetWriteReadyTargetedCodeNavigate(req, results)
		}

		return false
	}
}

func implementationTargetWriteReadyTargetedFileStats(req workspace.ToolRequest) bool {
	path := NormalizeRunnerPath(req.Path)
	return implementationTargetWriteReadySourcePath(path)
}

func implementationTargetWriteReadySourceSymbolTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
}

// targetWriteReadyStagedNudge builds a progressively stronger nudge message
// as the model accumulates non-write calls after source evidence is available.
// The nudge is advisory; the model owns whether another bounded read/search is
// needed before writing.
func targetWriteReadyStagedNudge(basePrompt string, req workspace.ToolRequest, nudgeCount int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	suffix := fmt.Sprintf("\n\nYour %s request will execute. Nudge %d.", tool, nudgeCount)

	evidencePrefix := strings.TrimSpace(basePrompt)
	if evidencePrefix == "" {
		evidencePrefix = "Source evidence is available for sourced target path(s)."
	}

	if nudgeCount <= 6 {
		return fmt.Sprintf("%s Either write to a sourced target path, or name the one exact missing claim and one exact probe that would resolve it, then take that probe.%s", evidencePrefix, suffix)
	}

	return fmt.Sprintf("You have taken %d additional non-write calls without a write. %s Name the one exact missing claim and one exact probe that would resolve it, then take that probe; if no such claim exists, write to a sourced target path or answer <<QUESTION>> with the exact blocker.%s", nudgeCount, evidencePrefix, suffix)
}
func implementationTargetWriteReadyTargetedCodeNavigate(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := NormalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if !newToolResultEvidenceProjection(results).HasSourceBody(path) {
		return false
	}

	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		return false
	}

	switch capability {
	case "find_definition", "read_symbol", "extract_body", "outline":
		return true
	default:
		return strings.TrimSpace(req.Pattern) != "" && strings.Contains(strings.ToLower(capability), "symbol")
	}
}

func implementationTargetWriteReadyTargetedSearchContent(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	pattern := strings.TrimSpace(req.Pattern)
	if len(pattern) < 4 {
		return false
	}

	if !implementationTargetWriteReadySearchPatternLooksNarrow(pattern) {
		return false
	}

	if implementationTargetWriteReadySearchPatternAlreadySourced(pattern, results) {
		return false
	}

	path := NormalizeRunnerPath(req.Path)
	if path != "" {
		return implementationTargetWriteReadySearchPathLooksScoped(path)
	}

	if strings.TrimSpace(req.NamedGap) == "" && strings.TrimSpace(req.WhyNotAnswering) == "" {
		return false
	}

	return true
}

func implementationTargetWriteReadySearchPathLooksScoped(path string) bool {
	path = strings.Trim(NormalizeRunnerPath(path), "/")
	if path == "" || path == "." {
		return false
	}

	switch path {
	case "internal", "cmd", "pkg", "src", "lib", "app", "apps", "packages", "test", "tests":
		return false
	default:
		return strings.Contains(path, "/") || recoverySearchPathLooksEditableSource(path)
	}
}

func implementationTargetWriteReadySearchPatternAlreadySourced(pattern string, results []workspace.ToolResult) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		tool := strings.TrimSpace(result.Tool)
		if tool != workspace.ToolNameReadFile && tool != workspace.ToolNameCodeNavigate &&
			!strings.HasSuffix(tool, "_read_symbol") &&
			!strings.HasSuffix(tool, "_extract_body") {

			continue
		}

		if strings.Contains(strings.ToLower(result.Output), pattern) {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadySearchPatternLooksNarrow(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) < 8 {
		return false
	}

	if strings.Contains(pattern, " ") || strings.ContainsAny(pattern, ".:_-(){}[]") {
		return true
	}

	hasUpper := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0
	hasLower := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0

	return hasUpper && hasLower
}

func (r *Runner) implementationTargetWriteReadyPendingRedirectProgressAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil {
		return false
	}

	path := NormalizeRunnerPath(req.Path)
	tool := strings.TrimSpace(req.Tool)
	if path == "" || tool == "" {
		return false
	}

	for _, obligation := range sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results) {
		if obligation.Path != path {
			continue
		}
		if sourceEvidencePendingRedirectRequestAllowed(obligation, req) {
			return true
		}
		if sourceEvidencePendingRedirectOutlineProgressAllowed(obligation, req, results) {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadyRedirectedReadPath(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return ""
	}

	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)

	if !strings.HasPrefix(lower, "redirected ") {
		return ""
	}

	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")

	if idx < 0 {
		return ""
	}

	return NormalizeRunnerPath(strings.TrimSpace(tail[:idx]))
}

func implementationTargetWriteReadySamePathContinuationReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := NormalizeRunnerPath(req.Path)
	if path == "" || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	if req.StartLine <= 0 || req.EndLine < req.StartLine || req.EndLine-req.StartLine+1 > 400 {
		return false
	}

	ranges := readRangesForPathFromResults(results, path)
	if len(ranges) == 0 || len(ranges) >= implementationTargetWriteReadySamePathContinuationLimit {
		return false
	}

	return implementationTargetWriteReadyRequestedRangeAddsSource(req.StartLine, req.EndLine, ranges)
}

type implementationTargetWriteReadyReadProgress string

const (
	implementationTargetWriteReadyReadProgressNotApplicable implementationTargetWriteReadyReadProgress = ""
	implementationTargetWriteReadyReadProgressAddsSource    implementationTargetWriteReadyReadProgress = "adds_source"
	implementationTargetWriteReadyReadProgressDuplicate     implementationTargetWriteReadyReadProgress = "duplicate"
)

func implementationTargetWriteReadyReadProgressForResult(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, priorResults []workspace.ToolResult) implementationTargetWriteReadyReadProgress {
	if !implementationTargetWriteReadySurfaceActive(brief, priorResults) {
		return implementationTargetWriteReadyReadProgressNotApplicable
	}

	if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return implementationTargetWriteReadyReadProgressNotApplicable
	}

	path := implementationTargetWriteReadyReadProgressPath(req, result)
	if path == "" {
		return implementationTargetWriteReadyReadProgressDuplicate
	}

	if !slices.Contains(newToolResultEvidenceRecord(result).SourceBodyPaths(), path) {
		return implementationTargetWriteReadyReadProgressDuplicate
	}

	if !newToolResultEvidenceProjection(priorResults).HasSourceBody(path) {
		return implementationTargetWriteReadyReadProgressAddsSource
	}

	if req.StartLine > 0 && req.EndLine >= req.StartLine &&
		implementationTargetWriteReadyRequestedRangeAddsSource(req.StartLine, req.EndLine, readRangesForPathFromResults(priorResults, path)) {

		return implementationTargetWriteReadyReadProgressAddsSource
	}

	return implementationTargetWriteReadyReadProgressDuplicate
}

func implementationTargetWriteReadyReadProgressPath(req workspace.ToolRequest, result workspace.ToolResult) string {
	if path := NormalizeRunnerPath(req.Path); path != "" {
		return path
	}

	for _, candidate := range newToolResultEvidenceRecord(result).SourceBodyPaths() {
		if path := NormalizeRunnerPath(candidate); path != "" {
			return path
		}
	}

	return ""
}

// Caps total same-path read_file ranges, including the range already read
// before continuations. New-target source expansions use their own turn-level
// allowance; this cap prevents one admitted path from becoming open discovery.
const implementationTargetWriteReadySamePathContinuationLimit = 3

func implementationTargetWriteReadyRequestedRangeAddsSource(start int, end int, ranges []planThenExecuteReadRange) bool {
	if start <= 0 || end < start {
		return false
	}

	if total, ok := implementationTargetWriteReadyKnownTotalLines(ranges); ok {
		if start > total {
			return false
		}

		if end > total {
			end = total
		}
	}

	for line := start; line <= end; line++ {
		covered := false

		for _, readRange := range ranges {
			rangeStart := readRange.start
			rangeEnd := readRange.end

			if rangeStart <= 0 || rangeEnd < rangeStart {
				continue
			}

			if readRange.total > 0 && rangeEnd > readRange.total {
				rangeEnd = readRange.total
			}

			if line >= rangeStart && line <= rangeEnd {
				covered = true
				break
			}
		}

		if !covered {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadyKnownTotalLines(ranges []planThenExecuteReadRange) (int, bool) {
	total := 0
	for _, readRange := range ranges {
		if readRange.total <= 0 {
			continue
		}

		if total == 0 || readRange.total < total {
			total = readRange.total
		}
	}

	return total, total > 0
}

func (r *Runner) implementationTargetWriteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if !r.implementationTargetWriteReadySourceReadAllowed(req, results, refreshes) {
		return false
	}

	return newToolResultEvidenceProjection(results).HasSourceBody(req.Path)
}

func implementationTargetWriteReadyNewTargetRead(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Path) != ""
}

const implementationTargetWriteReadySourceReadAllowanceLimit = 3

func implementationTargetSourceReadEstablishedWriteReady(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !implementationTargetWriteReadySourcePath(path) {
		return false
	}

	if !runnerStringSliceContainsPath(brief.RequiredEditPaths, path) &&
		!runnerStringSliceContainsPath(brief.EvidencePaths, path) &&
		!runnerStringSliceContainsPath(brief.AllowedEditPaths, path) {

		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate:
	default:
		if !strings.HasSuffix(strings.TrimSpace(req.Tool), "_read_symbol") &&
			!strings.HasSuffix(strings.TrimSpace(req.Tool), "_extract_body") {

			return false
		}
	}

	return newToolResultEvidenceProjection(results).HasSourceBody(path)
}

func runnerStringSliceContainsPath(values []string, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, value := range values {
		if NormalizeRunnerPath(value) == path {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadySourcePath(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	lower := strings.ToLower(path)

	return !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".mdx")
}

func (r *Runner) implementationPreWriteNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, results) || r.writeOnlyRecoveryToolIsWrite(req.Tool) {
		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "non-write tool"
	}

	summary = "Write-ready implementation non-write tool rejected"
	errorText = fmt.Sprintf("the target packet and required_edit_paths already establish write readiness for %s. %s does not satisfy the pre-write implementation contract; available outcomes are an allowed write capability or <<QUESTION>> with the exact path-specific blocker.", target, tool)
	prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s. Non-write tools are now hidden for this turn. The available contract outcome is the smallest safe write capability for each remaining required path. If the supplied anchor is stale or missing, answer with <<QUESTION>> and name that exact blocker; do not perform additional discovery or task planning.", target)

	return summary, errorText, prompt, true
}

func (r *Runner) unscopedContextPackCandidateReadRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) || !contextPackSeen(results) || len(concreteRecoveryEvidencePaths(results, 1)) > 0 {
		return "", "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", "", false
	}

	if contextPackCandidateReadAllowed(req, results) {
		return "", "", "", false
	}

	summary = "Context packet candidate read rejected"
	errorText = fmt.Sprintf("context_pack has not established an exact edit target yet. %s is only a packet candidate, so recovery must first gather literal or semantic evidence tied to the user's requested behavior.", strings.TrimSpace(req.Path))
	prompt = "The context packet did not establish an exact edit target. Do not read or write packet candidates yet. Run one bounded fallback discovery action tied to literal words, symbols, or behavior from the user's actual request, and include a named gap explaining that the missing fact is a grounded source hit. After a concrete hit, read the highest-confidence path before writing."

	return summary, errorText, prompt, true
}

func (r *Runner) deterministicPlanExecuteGroundingRecoveryRequest(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if planThenExecuteTerminalReplyShape(reply) {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
			return workspace.ToolRequest{}, "", false
		}

		rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
		if !ok || !submitPlanRejectionHasInvalidAnchor(rejected) {
			return workspace.ToolRequest{}, "", false
		}
	}

	rejectedAt := -1

	for i := len(results) - 1; i >= 0; i-- {
		if planThenExecuteSubmitPlanGroundingRejectedResult(results[i]) {
			rejectedAt = i
			break
		}
	}

	if rejectedAt < 0 {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if rejected, fallbackRejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok &&
			submitPlanRejectionHasInvalidAnchor(rejected) &&
			state.Mode == PlanExecutePlanningModeAnchorRefresh {

			rejectedAt = fallbackRejectedAt
		}
	}

	if rejectedAt < 0 {
		return workspace.ToolRequest{}, "", false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		for _, path := range state.RefreshPaths {
			normalized := NormalizeRunnerPath(path)
			if normalized == "" {
				continue
			}

			start, end := planThenExecuteAnchorRefreshFallbackRange(normalized, results, rejected)
			req := workspace.ToolRequest{
				Tool:            workspace.ToolNameReadFile,
				Path:            normalized,
				StartLine:       start,
				EndLine:         end,
				NamedGap:        "refresh current source evidence for the rejected submit_plan edit path",
				WhyNotAnswering: "need bounded source evidence before resubmitting the corrected plan",
			}

			return req, marshalToolRequestRaw(req), true
		}

		return workspace.ToolRequest{}, "", false
	}

	if !successfulToolAfter(results, rejectedAt, workspace.ToolNameContextPack) {
		task := strings.TrimSpace(r.RuntimeBrief.Objective)
		if task == "" {
			task = "localize candidate edit paths for the unscoped plan-execute atom"
		}

		req := workspace.ToolRequest{Tool: workspace.ToolNameContextPack, Task: task, Mode: "plan", Limit: 8}

		return req, marshalToolRequestRaw(req), true
	}

	if !successfulToolAfter(results, rejectedAt, workspace.ToolNameSearchContent) {
		if pattern := r.planExecuteGroundingSearchPattern(); pattern != "" {
			req := workspace.ToolRequest{
				Tool:            workspace.ToolNameSearchContent,
				Path:            ".",
				Pattern:         pattern,
				NamedGap:        "locate exact current source for the requested plan-execute atom before submit_plan",
				WhyNotAnswering: "need grounded source hits before plan-execute can submit edits or source-satisfied",
			}

			return req, marshalToolRequestRaw(req), true
		}
	}

	candidates := append([]string{}, candidateRecoveryScopePaths(r.RuntimeBrief, results[rejectedAt+1:], 8)...)

	candidates = appendUniqueRunnerPaths(candidates, contextPackCandidatePathsFromResults(results[rejectedAt+1:], 8)...)

	read := newToolResultEvidenceProjection(results[rejectedAt+1:]).ReadPaths()

	if len(read) >= 4 {
		return workspace.ToolRequest{}, "", false
	}

	for _, path := range candidates {
		if _, ok := read[NormalizeRunnerPath(path)]; ok {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            path,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "current source evidence for the candidate edit path before submit_plan",
			WhyNotAnswering: "need source-body evidence before plan-execute can submit edits or source-satisfied",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func planThenExecuteAnchorRefreshFallbackRange(path string, results []workspace.ToolResult, rejected workspace.ToolResult) (int, int) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 1, 180
	}

	if line, ok := submitPlanSyntaxValidationLineForPath(rejected, path); ok {
		start := line - 40
		if start < 1 {
			start = 1
		}

		return planThenExecuteClampAnchorRefreshRange(start, line+40)
	}

	ranges := readRangesForPathFromResults(results, path)
	for i := len(ranges) - 1; i >= 0; i-- {
		if ranges[i].start <= 0 || ranges[i].end < ranges[i].start {
			continue
		}

		end := ranges[i].end
		if ranges[i].total > 0 && end > ranges[i].total {
			end = ranges[i].total
		}

		if ranges[i].total <= 0 || end < ranges[i].total {
			return planThenExecuteClampAnchorRefreshRange(end+1, end+180)
		}

		return planThenExecuteClampAnchorRefreshRange(ranges[i].start, end)
	}

	return 1, 180
}

func planThenExecuteClampAnchorRefreshRange(start, end int) (int, int) {
	if start <= 0 {
		start = 1
	}
	if end < start {
		end = start
	}
	if end-start+1 > 400 {
		end = start + 399
	}

	return start, end
}

func (r *Runner) deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if !plancontract.SourceSatisfiedAnswerIntent(reply) || r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
		return workspace.ToolRequest{}, "", false
	}

	evidencePaths := plancontract.SourceSatisfiedEvidencePaths(reply)
	if len(evidencePaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	candidateSet := normalizedRequiredEditPathSet(candidateRecoveryScopePaths(r.RuntimeBrief, results, 16))
	requiredSet := normalizedRequiredEditPathSet(r.requiredEditCoveragePaths())

	for _, path := range evidencePaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
			continue
		}

		if len(requiredSet) > 0 {
			if _, ok := requiredSet[normalized]; !ok {
				continue
			}
		} else if len(candidateSet) > 0 {
			if _, ok := candidateSet[normalized]; !ok {
				continue
			}
		}

		if planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized).Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            normalized,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "source-satisfied current-state evidence for the named path",
			WhyNotAnswering: "need source-body evidence before accepting the plan-execute source-satisfied closeout",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func planThenExecuteGroundingRejectedSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
			return true
		}
	}

	return false
}

func (r *Runner) planExecuteGroundingSearchPattern() string {
	if r == nil {
		return ""
	}

	objectiveTerms := RecoveryObjectiveSearchWords(r.RuntimeBrief.Objective, 24)
	objectiveSet := map[string]struct{}{}

	for _, term := range objectiveTerms {
		objectiveSet[SingularObjectiveWord(term)] = struct{}{}
	}

	patterns := make([]string, 0, 12)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		patterns = append(patterns, regexp.QuoteMeta(value))
	}

	for _, tool := range r.RuntimeBrief.AllowedTools {
		tool = strings.TrimSpace(tool)
		if !toolNameOverlapsObjective(tool, objectiveSet) {
			continue
		}

		add(tool)

		if len(patterns) >= 4 {
			break
		}
	}

	if len(patterns) > 0 {
		return strings.Join(patterns, "|")
	}

	for _, term := range objectiveTerms {
		add(term)

		if len(patterns) >= 8 {
			break
		}
	}

	return strings.Join(patterns, "|")
}

func toolNameOverlapsObjective(tool string, objectiveSet map[string]struct{}) bool {
	if strings.TrimSpace(tool) == "" || len(objectiveSet) == 0 {
		return false
	}

	parts := strings.FieldsFunc(strings.ToLower(tool), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	matches := 0

	for _, part := range parts {
		if len(part) < 4 {
			continue
		}

		if _, ok := objectiveSet[SingularObjectiveWord(part)]; ok {
			matches++
		}
	}

	return matches >= 2 || (len(parts) == 1 && matches == 1)
}

func planThenExecuteTerminalReplyShape(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	return strings.HasPrefix(trimmed, "Decision: source-satisfied") ||
		strings.HasPrefix(trimmed, "Decision: block") ||
		strings.HasPrefix(trimmed, "<<QUESTION>>:")
}

func successfulToolAfter(results []workspace.ToolResult, index int, tool string) bool {
	tool = strings.TrimSpace(tool)
	for i := index + 1; i < len(results); i++ {
		if strings.TrimSpace(results[i].Tool) == tool && strings.TrimSpace(results[i].Error) == "" {
			return true
		}
	}

	return false
}

func appendUniqueRunnerPaths(paths []string, more ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths)+len(more))

	for _, path := range append(paths, more...) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func contextPackCandidateReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	target := NormalizeRunnerPath(req.Path)
	if target == "" || !pathparse.HasWorkspaceFileExtension(target) {
		return false
	}

	candidates := contextPackCandidatePathsFromResults(results, 32)
	for _, candidate := range candidates {
		if candidate == target {
			return true
		}
	}

	return false
}

func recoverySearchPathLooksEditableSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml", ".md", ".mdx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func literalRecoverySearchPattern(objective string) string {
	terms := RecoveryObjectiveSearchWords(objective, 8)
	if len(terms) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, regexp.QuoteMeta(term))
	}

	return strings.Join(quoted, "|")
}

func (r *Runner) planThenExecuteReviewRepairCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return workspace.ToolResult{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolResult{}, false
	}

	if planThenExecuteAnswerDeclaresTypedBlocker(reply) {
		return workspace.ToolResult{}, false
	}

	if plancontract.ReviewRepairAnswerDeclaresPass(reply) && plancontract.ReviewRepairTextHasChecklist(reply) {
		if failed := planThenExecuteReviewRepairFailedPhaseObligations(r.RuntimeBrief); len(failed) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Review-repair completion gate rejected contradictory pass",
				Error:   "review-repair phase evidence still has failed obligations " + quotedRunnerStrings(failed) + "; choose a corrective submit_plan inside allowed paths or `<<QUESTION>>:` with the exact blocker instead of `Decision: pass`.",
			}, true
		}

		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error: strings.Join([]string{
			"review-repair must close with exactly one of: a successful submit_plan corrective plan,",
			"`Decision: pass` with a `Verification Checklist`,",
			"or `<<QUESTION>>:` naming the exact blocker.",
			"Do not stop with process narration or an intent to inspect.",
		}, " "),
	}, true
}

func planThenExecuteReviewRepairFailedPhaseObligations(brief workerruntime.WorkerRuntimeBrief) []string {
	if brief.Readiness == nil {
		return nil
	}

	out := make([]string, 0)

	for _, obligation := range brief.Readiness.OpenObligations {
		obligation = strings.TrimSpace(obligation)
		switch {
		case obligation == "patch_hygiene_failed":
			out = append(out, obligation)
		case strings.HasPrefix(obligation, "verification_failed:"),
			strings.HasPrefix(obligation, "verification_error:"),
			strings.HasPrefix(obligation, "source_sanity_failed:"),
			strings.HasPrefix(obligation, "source_sanity_error:"),
			strings.HasPrefix(obligation, "submitted_edit_failed:"):
			out = append(out, obligation)
		}
	}

	return out
}

func (r *Runner) reviewRepairDoomLoopTerminalCloseout(results []workspace.ToolResult) (answer string, stopReason string, exhausted bool, ok bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return "", "", false, false
	}

	if reviewRepairCompletionGateRejectionCount(results) == 0 {
		return "", "", false, false
	}

	answer = reviewRepairNoopSubmitPlanDeterministicCloseout(r.RuntimeBrief, workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair repeated discovery after the terminal closeout gate instead of choosing `Decision: pass`, corrective `submit_plan`, or `<<QUESTION>>:`.",
	})
	if strings.HasPrefix(strings.TrimSpace(answer), "Decision: pass") {
		return answer, "review_repair_terminal_doom_loop_pass", false, true
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>:") {
		return answer, "review_repair_terminal_doom_loop_blocker", true, true
	}

	return "", "", false, false
}

func reviewRepairPhaseEvidencePacket(objective string) []string {
	evidence := reviewRepairPhaseEvidenceFromObjective(objective)
	if evidence == "" {
		return nil
	}

	return []string{"phase_evidence_bundle:\n" + evidence}
}

func reviewRepairTrustedPhaseEvidence(brief workerruntime.WorkerRuntimeBrief) string {
	if evidence := reviewRepairPhaseEvidenceFromObjective(brief.Objective); evidence != "" {
		return evidence
	}

	for _, packet := range brief.TargetPacket {
		packet = strings.TrimSpace(packet)
		if !strings.HasPrefix(packet, "phase_evidence_bundle:") {
			continue
		}

		evidence := strings.TrimSpace(strings.TrimPrefix(packet, "phase_evidence_bundle:"))
		if evidence == "" {
			continue
		}

		if len(evidence) > 12000 {
			evidence = evidence[:12000]
		}

		return evidence
	}

	return ""
}

func reviewRepairPhaseEvidenceFromObjective(objective string) string {
	const heading = "## Phase Evidence Bundle"

	idx := strings.Index(objective, heading)

	if idx < 0 {
		return ""
	}

	afterHeading := objective[idx+len(heading):]
	fenceStart := strings.Index(afterHeading, "```")

	if fenceStart < 0 {
		return ""
	}

	afterFence := afterHeading[fenceStart+len("```"):]
	if lineEnd := strings.Index(afterFence, "\n"); lineEnd >= 0 {
		afterFence = afterFence[lineEnd+1:]
	}

	fenceEnd := strings.Index(afterFence, "```")
	if fenceEnd < 0 {
		return ""
	}

	evidence := strings.TrimSpace(afterFence[:fenceEnd])
	if evidence == "" {
		return ""
	}

	if len(evidence) > 12000 {
		evidence = evidence[:12000]
	}

	return evidence
}

func implementationScopedReadFileRequestInExplicitScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) bool {
	if !implementationScopedEditSurfaceActive(brief, results) {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	return readRequestTargetsRequiredEditPath(req, implementationScopedExplicitReadScope(brief))
}

func (r *Runner) preflightScopedImplementationLargeWriteFileReplacement(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameWriteFile {
		return verificationInterceptResult{}
	}

	if briefIsDocDeliverableLane(r.RuntimeBrief) {
		return verificationInterceptResult{}
	}

	if !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) {
		return verificationInterceptResult{}
	}

	relPath := scopedImplementationRequiredPath(req.Path, r.RuntimeBrief.RequiredEditPaths)
	if relPath == "" || writePathIsNonSource(relPath) {
		return verificationInterceptResult{}
	}

	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)

	if err != nil || info.IsDir() {
		return verificationInterceptResult{}
	}

	lineCount := 0
	byteCount := info.Size()

	if byteCount <= scopedImplementationLargeReadByteThreshold {
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return verificationInterceptResult{}
		}

		lineCount = countTextLines(data)
		byteCount = int64(len(data))
	}

	if lineCount <= scopedImplementationLargeReadLineThreshold && byteCount <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}

	detail := fmt.Sprintf("%d bytes", byteCount)
	if lineCount > 0 {
		detail = fmt.Sprintf("%d lines, %d bytes", lineCount, byteCount)
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    workspace.ToolNameWriteFile,
			Summary: fmt.Sprintf("Rejected large existing write_file replacement for %s", relPath),
			Error: fmt.Sprintf(
				"write_file would replace existing large scoped source path %s (%s). Use apply_patch or edit_file with old_text/new_text or start_line/end_line/content for a bounded edit. write_file is reserved here for missing/new files and non-source deliverables.",
				relPath,
				detail,
			),
		},
		Reason: fmt.Sprintf("rejected range-less full-file replacement on large scoped source path %s", relPath),
	}
}

func scopedImplementationRequiredPath(path string, required []string) string {
	cleaned := cleanRelativeRunnerPath(path)
	if cleaned == "" {
		return ""
	}

	for _, candidate := range required {
		if cleanRelativeRunnerPath(candidate) == cleaned {
			return cleaned
		}
	}

	return ""
}

func cleanRelativeRunnerPath(path string) string {
	return pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
}

func countTextLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	lines := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		lines++
	}

	return lines
}

func (r *Runner) preflightScopedImplementationBoundedSourceRead(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}

	if !implementationScopedReadFileRequestInExplicitScope(r.RuntimeBrief, results, req) {
		return verificationInterceptResult{}
	}

	if req.StartLine > 0 || req.EndLine > 0 {
		return verificationInterceptResult{}
	}

	relPath := NormalizeRunnerPath(req.Path)
	if relPath == "" || strings.HasPrefix(relPath, "../") || relPath == ".." || filepath.IsAbs(relPath) {
		return verificationInterceptResult{}
	}

	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || strings.HasPrefix(relPath, "../") || relPath == ".." {
		return verificationInterceptResult{}
	}

	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))

	data, err := os.ReadFile(absPath)
	if err != nil {
		return verificationInterceptResult{}
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) <= scopedImplementationLargeReadLineThreshold && len(data) <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}

	start, end, selector := scopedImplementationBoundedReadWindow(r.RuntimeBrief, relPath, lines)
	output := scopedImplementationBoundedReadOutput(r.RuntimeBrief, relPath, lines, start, end, selector)
	r.Workspace.MarkPathObserved(relPath)

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool: workspace.ToolNameReadFile,
			Summary: fmt.Sprintf(
				"Read %s lines %d-%d (bounded large required-path source; file has %d lines total; full range-less read omitted)",
				relPath, start, end, len(lines),
			),
			Output: output,
			Paths:  []string{relPath},
			Facts: toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
				Path:       relPath,
				StartLine:  start,
				EndLine:    end,
				TotalLines: len(lines),
				Truncated:  true,
			}),
		},
		Reason: fmt.Sprintf("bounded range-less read_file on large scoped path %s to lines %d-%d of %d", relPath, start, end, len(lines)),
	}
}

func scopedImplementationBoundedReadWindow(brief workerruntime.WorkerRuntimeBrief, path string, lines []string) (int, int, string) {
	total := len(lines)
	if total <= 0 {
		return 1, 1, "empty file fallback"
	}

	terms := scopedImplementationSourceSelectionTerms(brief, path)
	if line := firstLineMatchingAnyTerm(lines, terms); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first task/path term match near line %d", line)
	}

	if line := firstDeclarationLine(lines); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first declaration fallback near line %d", line)
	}

	end := scopedImplementationBoundedReadWindowLines
	if end > total {
		end = total
	}

	return 1, end, "top-of-file fallback"
}

func scopedImplementationBoundedReadOutput(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, start int, end int, selector string) string {
	total := len(lines)

	var b strings.Builder

	fmt.Fprintf(&b, "Bounded source packet for %s\n", path)
	fmt.Fprintf(&b, "- file_lines: %d\n", total)
	fmt.Fprintf(&b, "- returned_lines: %d-%d\n", start, end)
	fmt.Fprintf(&b, "- selector: %s\n", selector)
	fmt.Fprintf(&b, "- reason: range-less read_file on a large scoped required/evidence path was bounded to preserve context.\n")
	fmt.Fprintf(&b, "- next: request a smaller read_file range or a code_navigate read_symbol for the exact edit-safety fact you still need.\n")

	for _, suggestion := range scopedImplementationSuggestedReadRanges(brief, path, lines, start, end) {
		fmt.Fprintf(&b, "- suggested_next_read: %s\n", suggestion)
	}

	b.WriteString("\nSource lines:\n")

	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, truncateScopedImplementationBoundedReadLine(lines[i]))
	}

	return strings.TrimRight(b.String(), "\n")
}

func truncateScopedImplementationBoundedReadLine(line string) string {
	if len(line) <= scopedImplementationBoundedReadLineCharCap {
		return line
	}

	return fmt.Sprintf("%s... [truncated %d chars]", line[:scopedImplementationBoundedReadLineCharCap], len(line)-scopedImplementationBoundedReadLineCharCap)
}

func scopedImplementationSuggestedReadRanges(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, returnedStart int, returnedEnd int) []string {
	terms := scopedImplementationSourceSelectionTerms(brief, path)
	total := len(lines)
	seen := map[string]struct{}{
		fmt.Sprintf("%d-%d", returnedStart, returnedEnd): {},
	}

	var suggestions []string

	add := func(line int, why string) {
		if line <= 0 || line > total || len(suggestions) >= 3 {
			return
		}

		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		key := fmt.Sprintf("%d-%d", start, end)

		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		suggestions = append(suggestions, fmt.Sprintf(
			`{"tool":"read_file","path":%q,"start_line":%d,"end_line":%d,"named_gap":%q,"why_not_answering":"need bounded current source before the edit"}`,
			path, start, end, why,
		))
	}

	for _, line := range allDeclarationLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant declaration in large scoped file")
	}

	for _, line := range allLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant source range in large scoped file")
	}

	if len(suggestions) == 0 {
		add(1, "inspect file header/imports before selecting a narrower symbol or range")
	}

	return suggestions
}

func scopedImplementationSourceSelectionTerms(brief workerruntime.WorkerRuntimeBrief, path string) []string {
	seen := map[string]struct{}{}

	var terms []string

	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		term = strings.Trim(term, "`'\".,:;()[]{}")

		if len(term) < 4 || scopedImplementationSourceSelectionStopWord(term) {
			return
		}

		if _, ok := seen[term]; ok {
			return
		}

		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	text := strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredNext, " "),
		strings.Join(brief.SearchPolicy, " "),
		strings.Join(brief.Constraints, " "),
	}, " ")

	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 && !strings.Contains(match[1], "/") {
			add(match[1])
		}
	}

	for _, token := range codeShapedTokenPattern.FindAllString(text, -1) {
		add(token)
	}

	for _, token := range codeShapedSnakeCasePattern.FindAllString(text, -1) {
		add(token)

		for _, part := range strings.Split(token, "_") {
			add(part)
		}
	}

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	add(stem)

	for _, part := range strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		add(part)
	}

	add(filepath.Base(path))

	if len(terms) > 16 {
		return terms[:16]
	}

	return terms
}

func scopedImplementationSourceSelectionStopWord(term string) bool {
	switch term {
	case "this", "that", "with", "from", "into", "file", "files", "path", "paths",
		"source", "read", "edit", "edits", "write", "writes", "scope", "scoped",
		"required", "implementation", "implement", "contract", "evidence", "exact",
		"task", "test", "tests", "need", "needs", "before", "after", "current", "main":
		return true
	default:
		return false
	}
}

func firstLineMatchingAnyTerm(lines []string, terms []string) int {
	for _, line := range allLinesMatchingAnyTerm(lines, terms, 1) {
		return line
	}

	return 0
}

func allLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}

	var out []int

	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}

		if len(out) >= max {
			return out
		}
	}

	return out
}

func firstDeclarationLine(lines []string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ") {

			return i + 1
		}
	}

	return 0
}

func allDeclarationLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}

	var out []int

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isDecl := strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ")

		if !isDecl {
			continue
		}

		lower := strings.ToLower(trimmed)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}

		if len(out) >= max {
			return out
		}
	}

	return out
}

func centeredLineWindow(line int, total int, size int) (int, int) {
	if total <= 0 {
		return 1, 1
	}

	if size <= 0 {
		size = scopedImplementationBoundedReadWindowLines
	}

	if size > total {
		size = total
	}

	start := line - size/3
	if start < 1 {
		start = 1
	}

	end := start + size - 1
	if end > total {
		end = total
		start = end - size + 1

		if start < 1 {
			start = 1
		}
	}

	return start, end
}

// maxToolStepsExhaustedReason mirrors OpenCode's max-steps.txt: when the
// per-turn tool-step budget is spent, tools are disabled and the model must
// synthesize a text-only reply now from the evidence already gathered. The
// wording is deliberately blunt about both constraints — no more tool calls,
// and no empty responses — to steer small local models away from the
// "keep probing" loop that produced the empty-answer failure in
// logs/conversations/20260421-053044.479.jsonl.

func reviewRepairTerminalOutcomeRequiredState(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	summary := strings.TrimSpace(result.Summary)

	return strings.EqualFold(summary, "Review-repair terminal outcome required") ||
		strings.HasPrefix(summary, "Review-repair completion gate rejected")
}

func reviewRepairTerminalValidNextActions() []string {
	return []string{
		"`Decision: pass` with a `Verification Checklist`",
		"a corrective `submit_plan` whose rationale starts with `Verification Checklist:`",
		"`<<QUESTION>>:` naming the exact blocker that prevents a terminal review-repair outcome",
	}
}

func reviewRepairTerminalInvalidNextActions() []string {
	return []string{
		"read/search/discovery/context tools after the review-repair closeout gate",
		"running commands after the review-repair closeout gate",
		"planning, summarizing, or adding prose before or after the terminal outcome",
	}
}

func applyScratchpadDeliverableCloseoutBias(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !scratchpadDeliverableCloseoutReady(brief, results) {
		return state
	}

	target := strings.Join(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet()), ", ")
	if target == "" {
		target = "the required deliverable"
	}

	state.Constraints = textutil.Merge(state.Constraints, []string{
		"scratchpad_deliverable_closeout_ready",
		"non_source_deliverable_path_unwritten",
	})
	state.EvidenceStatus = textutil.Merge(state.EvidenceStatus, []string{
		fmt.Sprintf("non-source deliverable still unwritten: %s; gathered evidence and accepted gaps should be recorded in the deliverable", target),
	})
	state.NextValidActions = []string{
		fmt.Sprintf("deliverable-write capability for %s with the corrected content, recording source uncertainty as Source verification pending or Scope Correction Required", target),
		"answer <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty",
	}
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
		"new source discovery after completion gate rejected a prose-only non-source deliverable",
		"repairing an optional evidence lookup instead of recording the gap in the scratchpad deliverable",
		"final answer without write_file for the required non-source deliverable",
	})

	return state
}

func scratchpadDeliverableCloseoutReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) {
		return false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())) == 0 {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}

		lower := strings.ToLower(strings.TrimSpace(result.Summary + " " + result.Error))
		if strings.Contains(lower, "no successful write") ||
			strings.Contains(lower, "no successful write tool") ||
			strings.Contains(lower, "workspace change") ||
			strings.Contains(lower, "remain unwritten") {

			return true
		}
	}

	return false
}

func (r *Runner) scratchpadDeliverableCloseoutNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, target string, reject bool) {
	if r == nil || !scratchpadDeliverableCloseoutReady(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", "", false
	}

	missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())

	target = "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	summary = "Scratchpad deliverable closeout blocked non-write tool"
	errorText = fmt.Sprintf("%s is not available after the completion gate rejected a prose-only response for non-source deliverable path(s): %s. The available contract outcomes are deliverable-write capability with full content or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The completion gate already determined that %s is still unwritten. Do not perform additional reads, searches, discovery, planning, or optional evidence repair from this recovery state. The deliverable-write contract records missing/stale/ambiguous evidence as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> if the deliverable is impossible.", target)

	return summary, errorText, prompt, target, true
}

func (r *Runner) scratchpadDeliverableSourceDiscoveryRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", false
	}

	if len(r.remainingRequiredEditPaths(results)) == 0 || !r.scopedReadClassTool(req) {
		return "", "", "", false
	}

	if scratchpadDeliverablePrewriteReadAllowed(req) {
		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}

	summary = "Scratchpad deliverable source discovery redirected to write"
	errorText = fmt.Sprintf("%s is not available before writing non-source scratchpad deliverable path(s): %s. Read required scratchpad inputs only; record missing or uncertain source evidence in the deliverable instead of reopening source discovery.", tool, target)
	prompt = fmt.Sprintf("This lane is a non-source scratchpad writer/reviewer phase for %s. Source discovery is closed here: the deliverable-write contract uses the preloaded evidence already in the brief, records missing/stale/ambiguous facts as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> and the exact blocker if the deliverable cannot be written from the scratchpad inputs plus recorded uncertainty.", target)

	return summary, errorText, prompt, true
}

func scratchpadDeliverablePrewriteReadAllowed(req workspace.ToolRequest) bool {
	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	return writePathIsNonSource(path)
}

func scratchpadFailedEvidenceFeedbackApplies(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) || strings.TrimSpace(result.Error) == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return false
	}

	return len(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())) > 0
}

type taskLedgerState struct {
	policy  TaskLedgerPolicy
	created bool
	items   []workspace.TaskLedgerItem
}

func newTaskLedgerState(policy TaskLedgerPolicy) *taskLedgerState {
	return &taskLedgerState{policy: policy}
}

func (s *taskLedgerState) apply(req workspace.ToolRequest, receiptBatches ...[]workspace.ToolResult) workspace.ToolResult {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "task_ledger is disabled for this runner",
		}
	}

	items := taskLedgerItemsFromRequest(req)
	if len(items) == 0 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "provide a non-empty todos array with content and status for each item",
		}
	}

	normalized := make([]workspace.TaskLedgerItem, 0, len(items))
	active := 0

	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		item.Status = normalizeTaskLedgerStatus(item.Status)
		item.Evidence = strings.TrimSpace(item.Evidence)

		if item.ID == "" {
			item.ID = fmt.Sprintf("item-%d", i+1)
		}

		if item.Content == "" {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s is missing content", item.ID),
			}
		}

		if !validTaskLedgerStatus(item.Status) {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s has unsupported status %q; use pending, in_progress, completed, blocked, or cancelled", item.ID, item.Status),
			}
		}

		if item.Status == "completed" && item.Evidence == "" {
			if previous, ok := s.previousItem(item.ID); ok && normalizeTaskLedgerStatus(previous.Status) == "completed" {
				item.Evidence = strings.TrimSpace(previous.Evidence)
			}
		}

		if item.Status == "in_progress" {
			active++
		}

		normalized = append(normalized, item)
	}

	if active > 1 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "only one task ledger item may be in_progress at a time",
		}
	}

	receipts := flattenTaskLedgerReceiptBatches(receiptBatches)
	if s.policy.RequireEvidence {
		if err := s.validateCompletedEvidence(normalized, receipts); err != "" {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   err,
			}
		}
	}

	s.created = true
	s.items = normalized

	return workspace.ToolResult{
		Tool:    "task_ledger",
		Summary: "Task ledger updated: " + s.statusSummary(),
		Output:  s.render(),
	}
}

func flattenTaskLedgerReceiptBatches(batches [][]workspace.ToolResult) []workspace.ToolResult {
	var receipts []workspace.ToolResult

	for _, batch := range batches {
		receipts = append(receipts, batch...)
	}

	return receipts
}

func (s *taskLedgerState) validateCompletedEvidence(items []workspace.TaskLedgerItem, receipts []workspace.ToolResult) string {
	for _, item := range items {
		if item.Status != "completed" || !s.itemNewlyCompleted(item) {
			continue
		}

		if strings.TrimSpace(item.Evidence) == "" {
			return fmt.Sprintf("todo %s is marked completed but has no evidence; use evidence refs such as write:path, read:path, verify:command, or artifact:id backed by prior tool results", item.ID)
		}

		for _, ref := range parseTaskLedgerEvidenceRefs(item.Evidence) {
			if !taskLedgerEvidenceRefSatisfied(ref, receipts) {
				return fmt.Sprintf("todo %s evidence %q is not backed by prior successful tool results", item.ID, ref)
			}
		}
	}

	return ""
}

func (s *taskLedgerState) itemNewlyCompleted(next workspace.TaskLedgerItem) bool {
	if s == nil {
		return true
	}

	if previous, ok := s.previousItem(next.ID); ok {
		return normalizeTaskLedgerStatus(previous.Status) != "completed"
	}

	return true
}

func (s *taskLedgerState) previousItem(id string) (workspace.TaskLedgerItem, bool) {
	if s == nil {
		return workspace.TaskLedgerItem{}, false
	}

	id = strings.TrimSpace(id)
	for _, previous := range s.items {
		if strings.TrimSpace(previous.ID) == id {
			return previous, true
		}
	}

	return workspace.TaskLedgerItem{}, false
}

func parseTaskLedgerEvidenceRefs(evidence string) []string {
	fields := strings.FieldsFunc(evidence, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
	refs := make([]string, 0, len(fields))

	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			refs = append(refs, field)
		}
	}

	return refs
}

func taskLedgerEvidenceRefSatisfied(ref string, receipts []workspace.ToolResult) bool {
	kind, value, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || strings.TrimSpace(value) == "" {
		return false
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	value = strings.TrimSpace(value)

	switch kind {
	case "write":
		return taskLedgerWriteEvidenceSatisfied(value, receipts)
	case "read":
		return taskLedgerToolEvidenceSatisfied(value, receipts, func(result workspace.ToolResult) bool {
			return workspace.TaskLedgerReadEvidenceTool(result.Tool)
		})
	case "verify":
		return taskLedgerToolEvidenceSatisfied(value, receipts, taskLedgerVerificationTool)
	case "artifact":
		return taskLedgerToolEvidenceSatisfied(value, receipts, taskLedgerAnySuccessfulTool)
	default:
		return false
	}
}

func taskLedgerWriteEvidenceSatisfied(path string, receipts []workspace.ToolResult) bool {
	want := NormalizeRunnerPath(path)
	if want == "" {
		return false
	}

	for got := range newToolResultEvidenceProjection(receipts).ChangedPathSet() {
		got = NormalizeRunnerPath(got)
		if got == want || strings.HasPrefix(got, want+"/") || strings.HasPrefix(want, got+"/") {
			return true
		}
	}

	return false
}

func taskLedgerToolEvidenceSatisfied(value string, receipts []workspace.ToolResult, predicate func(workspace.ToolResult) bool) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}

	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Error) != "" || !predicate(receipt) {
			continue
		}

		text := strings.ToLower(strings.Join([]string{receipt.Tool, receipt.Code, receipt.Summary, receipt.Output, string(receipt.Payload)}, "\n"))
		if strings.Contains(text, value) {
			return true
		}
	}

	return false
}

func taskLedgerVerificationTool(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(result.Tool)
	if tool == "shell" || tool == "source_sanity" || tool == "affected_suites" || tool == "context_trace_pack" {
		return true
	}

	return workspace.AnalyzeVerificationResult(result).Successful
}

func taskLedgerAnySuccessfulTool(result workspace.ToolResult) bool {
	return strings.TrimSpace(result.Error) == ""
}

func (s *taskLedgerState) completionRejection(reply string) (workspace.ToolResult, bool) {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{}, false
	}

	if s.policy.Required && !s.created && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "this is complex multi-step work and no task_ledger exists. Create a ledger or, if you are not yet acting on a write, answer with an explicit blocker or no-op reason.",
		}, true
	}

	if !s.created {
		return workspace.ToolResult{}, false
	}

	if unfinished := s.unfinishedItems(); len(unfinished) > 0 && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger still has unfinished required work: " + strings.Join(unfinished, "; ") + ". Continue the work, mark the ledger item with a concrete blocker, or if the user asked only for a plan, finish with an explicit planning-exit phrase (e.g. \"plan ready - awaiting approval\").",
		}, true
	}

	if blocked := s.blockedItems(); len(blocked) > 0 && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger contains blocked work: " + strings.Join(blocked, "; ") + ". The final answer must state the concrete blocker.",
		}, true
	}

	return workspace.ToolResult{}, false
}

func (s *taskLedgerState) unfinishedItems() []string {
	if s == nil {
		return nil
	}

	var unfinished []string

	for _, item := range s.items {
		switch item.Status {
		case "pending", "in_progress":
			unfinished = append(unfinished, item.ID+": "+item.Content+" ["+item.Status+"]")
		}
	}

	return unfinished
}

func (s *taskLedgerState) blockedItems() []string {
	if s == nil {
		return nil
	}

	var blocked []string

	for _, item := range s.items {
		if item.Status != "blocked" {
			continue
		}

		text := item.ID + ": " + item.Content
		if item.Evidence != "" {
			text += " (" + item.Evidence + ")"
		}

		blocked = append(blocked, text)
	}

	return blocked
}

func (s *taskLedgerState) statusSummary() string {
	counts := make(map[string]int)
	for _, item := range s.items {
		counts[item.Status]++
	}

	parts := make([]string, 0, 5)

	for _, status := range []string{"completed", "in_progress", "pending", "blocked", "cancelled"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}

	if len(parts) == 0 {
		return "empty"
	}

	return strings.Join(parts, ", ")
}

func (s *taskLedgerState) render() string {
	if s == nil || len(s.items) == 0 {
		return ""
	}

	var b strings.Builder

	for _, item := range s.items {
		b.WriteString("- [")
		b.WriteString(item.Status)
		b.WriteString("] ")
		b.WriteString(item.ID)
		b.WriteString(": ")
		b.WriteString(item.Content)

		if item.Evidence != "" {
			b.WriteString(" (evidence: ")
			b.WriteString(item.Evidence)
			b.WriteString(")")
		}

		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func taskLedgerItemsFromRequest(req workspace.ToolRequest) []workspace.TaskLedgerItem {
	if len(req.Todos) > 0 {
		return append([]workspace.TaskLedgerItem(nil), req.Todos...)
	}

	return append([]workspace.TaskLedgerItem(nil), req.Items...)
}

func normalizeTaskLedgerStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	status = strings.ReplaceAll(status, " ", "_")

	return status
}

func validTaskLedgerStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked", "cancelled":
		return true
	default:
		return false
	}
}

// taskLedgerFinalHasTerminalReason recognizes reply phrasings that let
// the model exit the completion gate without finishing every ledger
// item. Two categories:
//
//  1. Blocker phrases ("blocked", "cannot proceed", "permission") -
//     the work can't move forward for reasons outside the model's
//     control, and the answer should explain the blocker.
//  2. Planning-exit phrases ("plan ready", "awaiting approval") -
//     the user asked for triage only, not execution. Without this
//     escape the gate forces the model to execute every item it
//     just planned, which is the opposite of what the user wanted.
func taskLedgerFinalHasTerminalReason(reply string) bool {
	return ContainsAny(strings.ToLower(strings.TrimSpace(reply)),
		"blocked",
		"blocker",
		"cannot proceed",
		"could not proceed",
		"unable to",
		"no changes needed",
		"no change needed",
		"already complete",
		"already satisfied",
		"permission",
		"approval",
		"plan ready",
		"plan complete",
		"planning only",
	)
}

func sourceEditVerificationRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (summary string, errorText string, reject bool) {
	if !sourceVerificationGateApplies(brief, results) {
		return "", "", false
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok || !workspace.AnalyzeVerificationResult(latest).PatchHygieneOnly {
		return "", "", false
	}

	if workspace.AnalyzeVerificationRequest(req).Attempted || workspace.IsWorkspaceWriteTool(req.Tool) || sourceVerificationBoundaryAllowsTool(req.Tool) {
		return "", "", false
	}

	if !workspace.IsBroadInspectionRequest(req) && !workspace.IsNarrowingRequest(req) {
		return "", "", false
	}

	paths := newToolResultEvidenceProjection(results).ChangedSourcePathList()
	if len(paths) == 0 {
		return "", "", false
	}

	if plannerOwnsVerification(brief) {
		return "Source edit verification required",
			fmt.Sprintf("source edits are present in %s, but the latest successful check is only patch hygiene (`git diff --check`) or no compiler-backed source-aware check at all. Required evidence is compiler-backed source-sanity, language diagnostics, or the concrete lane-sanity blocker before doing more reads.", summarizeRunnerPaths(paths, 3)),
			true
	}

	return "Source edit verification required",
		fmt.Sprintf("source edits are present in %s, but the latest successful check is only patch hygiene (`git diff --check`) or no compiler-backed source-aware check at all. Required evidence is a focused language-appropriate build/test/check command, compiler-backed source-sanity, language diagnostics, or the concrete verification blocker before more reads.", summarizeRunnerPaths(paths, 3)),
		true
}

func fillSourceSanityPathFromSingleChangedSource(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameSourceSanity || strings.TrimSpace(req.Path) != "" {
		return workspace.ToolRequest{}, false
	}

	if !slices.Contains(allowedTools, workspace.ToolNameSourceSanity) || !sourceEditsNeedSemanticVerification(brief, results) {
		return workspace.ToolRequest{}, false
	}

	target, ok := newToolResultEvidenceProjection(results).SingleChangedSourcePath()
	if !ok {
		return workspace.ToolRequest{}, false
	}

	redirected := req

	redirected.Path = target

	return redirected, true
}

func postWriteSourceSanityRedirect(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, bool) {
	if !slices.Contains(allowedTools, workspace.ToolNameSourceSanity) || !sourceEditsNeedSemanticVerification(brief, results) {
		return workspace.ToolRequest{}, false
	}

	if workspace.AnalyzeVerificationRequest(req).Attempted || strings.TrimSpace(req.Tool) == workspace.ToolNameSourceSanity {
		return workspace.ToolRequest{}, false
	}

	if sourceVerificationBoundaryAllowsTool(req.Tool) {
		return workspace.ToolRequest{}, false
	}

	if postWriteVerificationReadOfChangedSource(req, results) ||
		postWriteVerificationWriteOfChangedSource(req, results) {

		return workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: NormalizeRunnerPath(req.Path)}, true
	}

	if requested := NormalizeRunnerPath(req.Path); requested != "" && requested != "." {
		if !newToolResultEvidenceProjection(results).HasChangedSourcePath(requested) {
			return workspace.ToolRequest{}, false
		}

		return workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: requested}, true
	}

	if !workspace.IsRuntimeInspectionRequest(req) {
		return workspace.ToolRequest{}, false
	}

	target, ok := newToolResultEvidenceProjection(results).SingleChangedSourcePath()
	if !ok {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: target}, true
}

func postWriteVerificationReadOfChangedSource(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}

	requested := NormalizeRunnerPath(req.Path)
	if requested == "" {
		return false
	}

	return newToolResultEvidenceProjection(results).HasChangedSourcePath(requested)
}

func postWriteVerificationWriteOfChangedSource(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	requested := NormalizeRunnerPath(req.Path)
	if requested == "" {
		return false
	}

	return newToolResultEvidenceProjection(results).HasChangedSourcePath(requested)
}

func sourceEditCloseoutPrompt(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if !sourceEditsAreSemanticallyVerified(brief, results) {
		return "", false
	}

	if textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) != "" {
		return "", false
	}

	if workspace.AnalyzeVerificationRequest(req).Attempted || workspace.IsWorkspaceWriteTool(req.Tool) || sourceVerificationBoundaryAllowsTool(req.Tool) {
		return "", false
	}

	if !workspace.IsBroadInspectionRequest(req) && !workspace.IsNarrowingRequest(req) {
		return "", false
	}

	paths := newToolResultEvidenceProjection(results).ChangedSourcePathList()
	if len(paths) == 0 {
		return "", false
	}

	return fmt.Sprintf("Successful source edits are already followed by source-aware verification for %s. The attempted read/search did not name an exact missing claim with `named_gap`, so tools are now disabled. Write the implementation result now: changed paths, verification status, and one concrete blocker only if the verification evidence shows one.", summarizeRunnerPaths(paths, 3)), true
}

func sourceEditsNeedSemanticVerification(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !sourceVerificationGateApplies(brief, results) {
		return false
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok {
		return true
	}

	return workspace.AnalyzeVerificationResult(latest).PatchHygieneOnly
}

func deterministicPostWriteVerificationRequest(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, bool) {
	if !hasPendingVerificationAfterWrites(results, brief) {
		return workspace.ToolRequest{}, false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	var command string

	for path := range changed {
		candidate := scriptSyntaxVerificationCommand(path)
		if candidate == "" {
			continue
		}

		if command != "" {
			return workspace.ToolRequest{}, false
		}

		command = candidate
	}

	if command == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: "shell", Command: command}, true
}

func scriptSyntaxVerificationCommand(path string) string {
	normalized := NormalizeRunnerPath(path)
	lower := strings.ToLower(normalized)

	switch {
	case strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm"):
		script := fmt.Sprintf("from html.parser import HTMLParser\npath = %q\nwith open(path, encoding='utf-8') as f:\n    HTMLParser().feed(f.read())\nprint('HTML parsed OK: ' + path)\n", normalized)
		return "python3 -c " + shellSingleQuote(script)
	case strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".bash"):
		return "bash -n " + normalized
	case strings.HasSuffix(lower, ".zsh"):
		return "zsh -n " + normalized
	default:
		return ""
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sourceVerificationBoundaryAllowsTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "task_ledger", "harness_feedback", "idiom_feedback":
		return true
	default:
		return false
	}
}

func sourceEditsAreSemanticallyVerified(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !sourceVerificationGateApplies(brief, results) {
		return false
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)

	return ok && !workspace.AnalyzeVerificationResult(latest).PatchHygieneOnly
}

func failedVerificationAfterLatestWrite(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolResult, bool) {
	if workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return workspace.ToolResult{}, false
	}

	docLane := briefIsDocDeliverableLane(brief)
	pending := false
	failed := false
	var pendingPaths []string

	var latestFailed workspace.ToolResult

	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" && workspace.IsWorkspaceWriteTool(result.Tool) {
			if writeResultMadeNoWorkspaceChange(result) {
				continue
			}

			if writeTouchesOnlyNonSourcePaths(result) || (docLane && mkdirTouchesOnlyRequiredPathParents(result, brief.RequiredEditPaths)) {
				continue
			}

			pending = true
			pendingPaths = textutil.AppendUniqueMulti(pendingPaths, newToolResultEvidenceRecord(result).ChangedPathList()...)
			failed = false
			latestFailed = workspace.ToolResult{}

			continue
		}

		if pending && strings.TrimSpace(result.Error) == "" && workspace.AnalyzePostWriteVerificationResult(result, pendingPaths).Successful {
			pending = false
			pendingPaths = nil
			failed = false
			latestFailed = workspace.ToolResult{}

			continue
		}

		if !pending || !workspace.AnalyzeVerificationResult(result).Attempted {
			continue
		}

		if strings.TrimSpace(result.Error) == "" && workspace.AnalyzeVerificationResult(result).Successful {
			pending = false
			pendingPaths = nil
			failed = false
			latestFailed = workspace.ToolResult{}

			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			failed = true
			latestFailed = result
		}
	}

	if !pending || !failed {
		return workspace.ToolResult{}, false
	}

	return latestFailed, true
}

func sourceVerificationGateApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !runnerBriefRequiresMutation(brief) || workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	return len(newToolResultEvidenceProjection(results).ChangedSourcePathList()) > 0
}

func latestVerificationAfterLatestSourceWrite(results []workspace.ToolResult) (workspace.ToolResult, bool) {
	var latest workspace.ToolResult

	found := false
	seenSourceWrite := false

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if len(newToolResultEvidenceRecord(result).ChangedSourcePathList()) > 0 {
			seenSourceWrite = true
			found = false
			latest = workspace.ToolResult{}

			continue
		}

		if seenSourceWrite && workspace.AnalyzeVerificationResult(result).Successful {
			latest = result
			found = true
		}
	}

	return latest, found
}

func summarizeRunnerPaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return "edited source files"
	}

	if limit <= 0 || limit > len(paths) {
		limit = len(paths)
	}

	quoted := make([]string, 0, limit+1)
	for _, path := range paths[:limit] {
		quoted = append(quoted, "`"+path+"`")
	}

	if len(paths) > limit {
		quoted = append(quoted, fmt.Sprintf("%d more", len(paths)-limit))
	}

	return strings.Join(quoted, ", ")
}

func plannerOwnedVerificationShellRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	if strings.TrimSpace(req.Tool) != "shell" || !plannerOwnsVerification(brief) {
		return "", "", false
	}

	command := strings.TrimSpace(req.Command)
	if command == "" || !workspace.AnalyzeShellCommand(command).BuildOrTest {
		return "", "", false
	}

	return "Planner-owned verification shell rejected",
		"this implementation lane delegates build/test verification to the caller. Source edits require compiler-backed source-sanity or language diagnostics; git diff --check and generic source-sanity fallback are only patch-hygiene evidence. The other contract outcome is changed paths plus the concrete planner-owned verification status.",
		true
}

func verificationRequiredAfterWritesMessage(brief workerruntime.WorkerRuntimeBrief, writesSinceVerification int) (summary string, errorText string) {
	summary = "Verification required after workspace edits"
	if plannerOwnsVerification(brief) {
		return summary, fmt.Sprintf(
			"the workspace has %d successful write(s) since the last lane sanity check. This route delegates build/test verification to the planner; do not run build or test commands. Source edits require compiler-backed source-sanity or language diagnostics; generic source-sanity fallback and git diff --check are only patch-hygiene evidence. The other contract outcome is changed paths plus the concrete planner-owned verification status.",
			writesSinceVerification,
		)
	}

	return summary, fmt.Sprintf("the workspace has %d successful write(s) since the last verification. Required evidence is a focused verification command (for example package tests, diagnostics, or the relevant build/test command) before more inspection or edits, or the concrete blocker.", writesSinceVerification)
}

func plannerOwnsVerification(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	for _, constraint := range brief.Constraints {
		switch strings.ToLower(strings.TrimSpace(constraint)) {
		case "planner_owned_verification", "planner-owned-verification", "planner_owned_acceptance", "planner-owned-acceptance":
			return true
		}
	}

	return false
}

func clearVerificationGateFingerprints(fingerprints map[string]string) {
	for fingerprint, reason := range fingerprints {
		if strings.Contains(reason, "Verification required after workspace edits") {
			delete(fingerprints, fingerprint)
		}
	}
}

func clearRunnerStringIntMap(values map[string]int) {
	for key := range values {
		delete(values, key)
	}
}

type runnerVerificationBootstrapInput struct {
	ctx              context.Context
	collectedResults []workspace.ToolResult
	metrics          *runnerMetrics
	recorder         *runnerTurnRecorder
	executionState   *runnerExecutionState
	requestBudget    *runnerRequestBudgetState
	autonomyRecorder *runnerAutonomyRecorder
	onToolRequest    func(ToolRequestEvent)
}

func (r *Runner) runVerificationBootstrap(input runnerVerificationBootstrapInput) (string, bool) {
	if r.Workspace == nil || len(input.collectedResults) != 0 {
		return "", false
	}
	req, ok := r.verificationBootstrapRequest()
	if !ok {
		return "", false
	}

	raw := marshalToolRequestRaw(req)
	if input.metrics != nil {
		input.metrics.ToolCalls++
	}
	if input.onToolRequest != nil {
		profile := workspace.DiscoveryProfileForRequest(req)
		input.onToolRequest(ToolRequestEvent{
			Step:           1,
			Agent:          r.agentName(),
			Raw:            raw,
			Request:        req,
			Mode:           r.Workspace.Mode(),
			Broad:          workspace.IsBroadInspectionRequest(req),
			Narrowing:      workspace.IsNarrowingRequest(req),
			Action:         toolActionClass(req),
			DiscoveryClass: profile.Class,
			DiscoveryScope: profile.Scope,
			DiscoveryCost:  profile.Cost,
		})
	}

	result := r.Workspace.Execute(input.ctx, req)
	if input.recorder != nil {
		input.recorder.recordWithOptions(1, req, result, "verification_auto_bootstrap", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
	}
	if input.executionState != nil && input.requestBudget != nil {
		update := input.executionState.applyResult(req, result, toolRequestKey(req), r.Workspace, input.requestBudget)
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		if input.autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}
			input.autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}
	}

	return synthesizeVerificationOnlyAnswer(req, result), true
}

// verificationInterceptResult tells the runner loop whether a read-class
// request should bypass the workspace dispatch with a synthesized
// redirect message.
type verificationInterceptResult struct {
	// Intercepted is true when the request was redirected. The runner
	// should use Result instead of calling Workspace.Execute.
	Intercepted bool
	// Result is the synthesized tool result the runner should record.
	// Carries an error string with the directive message and a summary
	// the audit log can read at a glance.
	Result workspace.ToolResult
	// Reason is a short human-readable string for telemetry events
	// (e.g. "read of already-edited path; redirected to next required").
	Reason string
}

// preflightVerificationOnAlreadyEditedPath intercepts read-class tool
// calls aimed at a required_edit_path that has already been written
// successfully when other ✗ required paths remain. The model's most
// common stall mode after the first few edits is to enter "verify each
// edit landed" mode and never return to writing the remaining paths.
// Each verification read uses up step budget without progressing the
// brief; eventually the doom-loop guard fires and the lane closes with
// half the work done.
//
// The intercept replaces the tool result with a one-line redirect that
// names the next pending path and forbids re-verification. The model
// then reads the directive on the next turn and (in practice) issues
// an edit_file against the named path.
//
// Conditions for intercept:
//   - The brief has required_edit_paths set.
//   - At least one required path is still pending (✗).
//   - The request is a read-class tool with a path that resolves to a
//     required_edit_path already on the ✓ list.
//   - The immediately preceding tool result was ALSO a read-class
//     tool. The first read after a write is left through because the
//     model legitimately uses "write a, read a, write B" to mirror
//     a freshly-landed structure into the next path. The second
//     read in a row is the verification-spree pattern.
//
// search_content and search_files are deliberately exempt: they are
// the right way to verify a literal expression inside a file, and
// blocking them would push the model back to read_symbol prose calls
// (the failure mode the read_symbol pattern validator already
// handles).
func (r *Runner) preflightVerificationOnAlreadyEditedPath(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil {
		return verificationInterceptResult{}
	}

	required := r.RuntimeBrief.RequiredEditPaths
	if len(required) == 0 {
		return verificationInterceptResult{}
	}

	if !verificationInterceptToolCovered(req) {
		return verificationInterceptResult{}
	}

	if !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return verificationInterceptResult{}
	}

	target := NormalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return verificationInterceptResult{}
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	if _, ok := changed[target]; !ok {
		return verificationInterceptResult{}
	}

	// Only intercept paths that are part of the required edit set —
	// reads of unrelated already-edited files are usually legitimate
	// (cross-file context, includes, etc.).
	if !requiredEditPathsContains(required, target) {
		return verificationInterceptResult{}
	}

	pending := missingRequiredEditPaths(required, changed)
	if len(pending) == 0 {
		// Nothing left to write; let normal completion path handle the
		// final answer. Re-verification at this stage is harmless.
		return verificationInterceptResult{}
	}

	if !precedingResultWasReadClass(results) {
		// First read after a write — could be legitimate prep for the
		// next ✗ path (mirror existing structure into the new file).
		// Let it through; only intercept when the model has stacked
		// reads in a row.
		return verificationInterceptResult{}
	}

	directive := verificationInterceptDirective(req.Tool, target, pending)
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Read of already-edited required path redirected to remaining write",
		Error:   directive,
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result:      result,
		Reason: fmt.Sprintf(
			"read on ✓ %s suppressed; %d required path(s) still pending; next: %s",
			target, len(pending), pending[0],
		),
	}
}

// verificationInterceptToolCovered restricts the intercept to the
// read-class tools the model uses for per-edit verification. We
// deliberately leave search_content / search_files / discovery_query
// alone — those are aimed at literal text or evidence gathering and
// have legitimate use cases against already-edited paths.
//
// For code_navigate, only the file_outline and read_symbol
// capabilities trip the intercept — they're the verification habits
// from the donkey-run failure pattern. find_symbol / references /
// definition / calls / implementations / related_files are lookup
// tools that have real value on already-edited paths (e.g., "where
// is X used now that I added it") and are left alone.
func verificationInterceptToolCovered(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "read_file":
		return true
	case "code_navigate":
		switch strings.ToLower(strings.TrimSpace(req.Capability)) {
		case "file_outline", "outline", "read_symbol", "inspect_symbol", "symbol_body":
			return true
		}

		return false
	}

	return languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapFileOutline, semanticlanguage.CapReadSymbol)
}

// precedingResultWasReadClass returns true if the most recent recorded
// tool result was itself a read-class call. We use this to distinguish
// "model just wrote, now reads to prep next write" (first read after
// a write — let through) from "model is stacking verification reads"
// (read after read — intercept).
func precedingResultWasReadClass(results []workspace.ToolResult) bool {
	if len(results) == 0 {
		return false
	}

	last := results[len(results)-1]

	return workspace.IsReadTool(strings.TrimSpace(last.Tool))
}

// requiredEditPathsContains checks whether the request's target path
// appears in the brief's required_edit_paths list (under a normalized
// comparison).
func requiredEditPathsContains(required []string, target string) bool {
	for _, raw := range required {
		if NormalizeRunnerPath(raw) == target {
			return true
		}
	}

	return false
}

// verificationInterceptDirective renders the redirect message that
// replaces the tool result. The text is deliberately short and
// actionable: the model needs the next path name, the rule, and the
// escape hatch in one block.
func verificationInterceptDirective(tool, target string, pending []string) string {
	next := pending[0]
	header := fmt.Sprintf(
		"%s on %q suppressed: that path is already on the ✓ list of required_edit_paths and the planner runs builds — do not re-verify completed edits.",
		strings.TrimSpace(tool), target,
	)
	body := fmt.Sprintf("Next required path (✗ pending): %s. Write it with edit_file or apply_patch, or stop with <<QUESTION>>: naming the concrete blocker for that path.", next)

	if len(pending) > 1 {
		body += fmt.Sprintf(" %d required path(s) remain in total: %s.", len(pending), strings.Join(pending, ", "))
	}

	return header + " " + body
}

func marshalRunEvent(role string, value any) RunEvent {
	data, err := json.Marshal(value)
	if err != nil {
		return RunEvent{}
	}

	return RunEvent{Role: role, Content: string(data)}
}

type PermissionDecisionSnapshot struct {
	Tool     string              `json:"tool"`
	Target   string              `json:"target,omitempty"`
	Decision toolpolicy.Decision `json:"decision"`
	Rule     string              `json:"rule,omitempty"`
}

type CheckpointPolicy struct {
	Enabled bool
}

type GitCheckpoint struct {
	Root   string `json:"root"`
	Head   string `json:"head,omitempty"`
	Dirty  bool   `json:"dirty"`
	Status string `json:"status,omitempty"`
}

func permissionPolicyDecision(policy toolpolicy.Policy, req workspace.ToolRequest) PermissionDecisionSnapshot {
	decision := policy.Decide(toolPolicyRequest(req))
	return PermissionDecisionSnapshot{
		Tool:     decision.Tool,
		Target:   decision.Target,
		Decision: decision.Decision,
		Rule:     permissionRuleRef(decision.Rule),
	}
}

func permissionRuleRef(r toolpolicy.Rule) string {
	parts := []string{}
	if r.Tool != "" {
		parts = append(parts, "tool="+r.Tool)
	}

	if r.PathPattern != "" {
		parts = append(parts, "path="+r.PathPattern)
	}

	if r.CommandPrefix != "" {
		parts = append(parts, "command="+r.CommandPrefix)
	}

	if r.CommandContains != "" {
		parts = append(parts, "command_contains="+r.CommandContains)
	}

	if r.Decision != "" {
		parts = append(parts, "decision="+string(r.Decision))
	}

	return strings.Join(parts, " ")
}

func PermissionTarget(req workspace.ToolRequest) string {
	return toolpolicy.Target(toolPolicyRequest(req))
}

func toolPolicyRequest(req workspace.ToolRequest) toolpolicy.Request {
	return toolpolicy.Request{
		Tool:        req.Tool,
		Command:     req.Command,
		Path:        req.Path,
		Destination: req.Destination,
		Workdir:     req.Workdir,
		Moves:       toolPolicyMoves(req.Moves),
		Args:        req.Args,
	}
}

func toolPolicyMoves(moves []workspace.GoDeclarationMove) []toolpolicy.Move {
	if len(moves) == 0 {
		return nil
	}

	out := make([]toolpolicy.Move, 0, len(moves))
	for _, move := range moves {
		out = append(out, toolpolicy.Move{Destination: move.Destination})
	}

	return out
}

func (s PermissionDecisionSnapshot) ToolResult() workspace.ToolResult {
	switch s.Decision {
	case toolpolicy.Deny:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Summary: "Tool permission denied",
			Error:   fmt.Sprintf("permission policy denied %s on %s", s.Tool, textutil.FirstNonEmpty(s.Target, "this target")),
		}
	case toolpolicy.Ask:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Summary: "Tool approval required",
			Error:   fmt.Sprintf("permission policy requires approval for %s on %s", s.Tool, textutil.FirstNonEmpty(s.Target, "this target")),
		}
	default:
		return workspace.ToolResult{}
	}
}

func CaptureGitCheckpoint(ctx context.Context, root string) (GitCheckpoint, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return GitCheckpoint{}, fmt.Errorf("checkpoint root is required")
	}

	snapshot, err := gitstate.Current(ctx, root)
	if err != nil {
		return GitCheckpoint{}, err
	}

	return GitCheckpoint{
		Root:   root,
		Head:   snapshot.Head,
		Dirty:  snapshot.Dirty,
		Status: snapshot.Status,
	}, nil
}

// PreimageCheckpointStore captures file contents before a workspace write.
// GitCheckpoint remains the run-level audit snapshot; this store is the
// per-path rewind seam that write execution can call before mutation.
type PreimageCheckpointStore struct {
	Root      string
	snapshots map[string]preimageSnapshot
}

type preimageSnapshot struct {
	Exists bool
	Data   []byte
	Mode   os.FileMode
}

type writePreimagePayload struct {
	Paths []writePreimagePath `json:"paths,omitempty"`
}

type writePreimagePath struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

const writePreimagePayloadType = "workspace_write_preimage"

func NewPreimageCheckpointStore(root string) *PreimageCheckpointStore {
	return &PreimageCheckpointStore{Root: root, snapshots: make(map[string]preimageSnapshot)}
}

func (s *PreimageCheckpointStore) Snapshot(path string) error {
	if s == nil {
		return fmt.Errorf("preimage checkpoint store is nil")
	}

	normalized, full, err := s.resolve(path)
	if err != nil {
		return err
	}

	if _, ok := s.snapshots[normalized]; ok {
		return nil
	}

	info, statErr := os.Stat(full)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			s.snapshots[normalized] = preimageSnapshot{Exists: false}
			return nil
		}

		return statErr
	}

	if info.IsDir() {
		return fmt.Errorf("preimage checkpoint only supports files: %s", normalized)
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}

	s.snapshots[normalized] = preimageSnapshot{Exists: true, Data: append([]byte(nil), data...), Mode: info.Mode()}

	return nil
}

func (s *PreimageCheckpointStore) Existed(path string) (bool, bool) {
	if s == nil {
		return false, false
	}

	normalized, _, err := s.resolve(path)
	if err != nil {
		return false, false
	}

	snapshot, ok := s.snapshots[normalized]
	if !ok {
		return false, false
	}

	return snapshot.Exists, true
}

func (s *PreimageCheckpointStore) resolve(path string) (string, string, error) {
	root := strings.TrimSpace(s.Root)
	if root == "" {
		return "", "", fmt.Errorf("preimage checkpoint root is required")
	}

	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return "", "", fmt.Errorf("preimage checkpoint path is required")
	}

	if filepath.IsAbs(path) {
		return "", "", fmt.Errorf("preimage checkpoint path must be workspace-relative: %s", path)
	}

	normalized := filepath.ToSlash(path)
	if strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", "", fmt.Errorf("preimage checkpoint path escapes workspace: %s", path)
	}

	full := filepath.Join(root, filepath.FromSlash(normalized))

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}

	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", "", err
	}

	rel, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil {
		return "", "", err
	}

	if rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return "", "", fmt.Errorf("preimage checkpoint path escapes workspace: %s", path)
	}

	return normalized, fullAbs, nil
}

type Manager struct {
	Root    string
	Scratch string
	Prefix  string
}

type Checkout struct {
	Name    string
	Branch  string
	Path    string
	BaseSHA string
	Mode    string
}

type Diff struct {
	ChangedPaths []string
	Stat         string
	Patch        string
}

func (m Manager) Create(ctx context.Context, name string) (Checkout, error) {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return Checkout{}, fmt.Errorf("workbranch root is required")
	}

	scratch := strings.TrimSpace(m.Scratch)
	if scratch == "" {
		scratch = os.TempDir()
	}

	prefix := strings.Trim(strings.TrimSpace(m.Prefix), "/")
	if prefix == "" {
		prefix = "codex/work"
	}

	safeName := sanitizeName(name)
	if safeName == "" {
		safeName = "task"
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	branch := prefix + "/" + safeName + "-" + suffix
	path := filepath.Join(scratch, "idiom-"+safeName+"-"+suffix)

	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return Checkout{}, err
	}

	baseSHA, err := gitstate.CombinedOutputText(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		if !workbranchCanFallbackWithoutGit(err) {
			return Checkout{}, err
		}

		if err := copyWorkbranchTree(root, path); err != nil {
			return Checkout{}, err
		}

		return Checkout{
			Name:    safeName,
			Path:    path,
			BaseSHA: "non-git",
			Mode:    "filesystem-copy",
		}, nil
	}

	if _, err := gitstate.CombinedOutputText(ctx, root, "worktree", "add", "-b", branch, path, strings.TrimSpace(baseSHA)); err != nil {
		return Checkout{}, err
	}

	return Checkout{
		Name:    safeName,
		Branch:  branch,
		Path:    path,
		BaseSHA: strings.TrimSpace(baseSHA),
		Mode:    "git-worktree",
	}, nil
}

func (m Manager) Diff(ctx context.Context, checkout Checkout) (Diff, error) {
	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return Diff{}, fmt.Errorf("workbranch path is required")
	}

	base := strings.TrimSpace(checkout.BaseSHA)
	if base == "" {
		base = "HEAD"
	}

	if strings.TrimSpace(checkout.Mode) == "filesystem-copy" || strings.TrimSpace(checkout.Branch) == "" || base == "non-git" {
		return diffWorkbranchCopy(m.Root, path)
	}

	worktreeDiff, err := gitstate.WorktreeDiffAgainst(ctx, path, base)
	if err != nil {
		return Diff{}, err
	}

	return Diff{ChangedPaths: worktreeDiff.ChangedPaths, Stat: worktreeDiff.Stat, Patch: worktreeDiff.Patch}, nil
}

func (m Manager) Remove(ctx context.Context, checkout Checkout) error {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return fmt.Errorf("workbranch root is required")
	}

	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return fmt.Errorf("workbranch path is required")
	}

	if strings.TrimSpace(checkout.Mode) == "filesystem-copy" || strings.TrimSpace(checkout.Branch) == "" || strings.TrimSpace(checkout.BaseSHA) == "non-git" {
		return os.RemoveAll(path)
	}

	return gitstate.RemoveWorktreeBranch(ctx, root, path, checkout.Branch)
}

func workbranchCanFallbackWithoutGit(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not a git repository") ||
		strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "ambiguous argument 'head'") ||
		strings.Contains(lower, "unknown revision or path not in the working tree") ||
		strings.Contains(lower, "needed a single revision")
}

func copyWorkbranchTree(srcRoot string, dstRoot string) error {
	srcRoot = strings.TrimSpace(srcRoot)
	dstRoot = strings.TrimSpace(dstRoot)
	if srcRoot == "" {
		return fmt.Errorf("workbranch source root is required")
	}
	if dstRoot == "" {
		return fmt.Errorf("workbranch copy path is required")
	}

	srcAbs, err := filepath.Abs(srcRoot)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dstRoot)
	if err != nil {
		return err
	}

	return filepath.WalkDir(srcAbs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dstAbs, 0o755)
		}
		if skipWorkbranchCopyPath(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		target := filepath.Join(dstAbs, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		return copyWorkbranchFile(path, target, info.Mode().Perm())
	})
}

func skipWorkbranchCopyPath(rel string, entry os.DirEntry) bool {
	first := strings.Split(filepath.ToSlash(rel), "/")[0]
	switch first {
	case ".git", ".idiom":
		return true
	default:
		return false
	}
}

func copyWorkbranchFile(src string, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}

	return out.Close()
}

func diffWorkbranchCopy(parentRoot string, childRoot string) (Diff, error) {
	parentFiles, err := snapshotWorkbranchFiles(parentRoot)
	if err != nil {
		return Diff{}, err
	}
	childFiles, err := snapshotWorkbranchFiles(childRoot)
	if err != nil {
		return Diff{}, err
	}

	pathSet := make(map[string]struct{}, len(parentFiles)+len(childFiles))
	for path := range parentFiles {
		pathSet[path] = struct{}{}
	}
	for path := range childFiles {
		pathSet[path] = struct{}{}
	}

	var changed []string
	for path := range pathSet {
		before, beforeOK := parentFiles[path]
		after, afterOK := childFiles[path]
		if beforeOK != afterOK || !slices.Equal(before, after) {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)

	return Diff{
		ChangedPaths: changed,
		Stat:         renderFilesystemDiffStat(changed, parentFiles, childFiles),
		Patch:        renderFilesystemDiffPatch(changed, parentFiles, childFiles),
	}, nil
}

func snapshotWorkbranchFiles(root string) (map[string][]byte, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workbranch snapshot root is required")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	files := make(map[string][]byte)
	err = filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipWorkbranchCopyPath(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

func renderFilesystemDiffStat(changed []string, parentFiles map[string][]byte, childFiles map[string][]byte) string {
	if len(changed) == 0 {
		return ""
	}

	var b strings.Builder
	for _, path := range changed {
		before := lineCount(parentFiles[path])
		after := lineCount(childFiles[path])
		b.WriteString(fmt.Sprintf(" %s | %d -> %d lines\n", path, before, after))
	}
	return strings.TrimSpace(b.String())
}

func renderFilesystemDiffPatch(changed []string, parentFiles map[string][]byte, childFiles map[string][]byte) string {
	if len(changed) == 0 {
		return ""
	}

	var b strings.Builder
	for _, path := range changed {
		b.WriteString("--- a/" + path + "\n")
		b.WriteString("+++ b/" + path + "\n")
		b.WriteString("@@ filesystem-copy diff @@\n")
		if _, ok := parentFiles[path]; !ok {
			b.WriteString(renderAddedFilePatch(childFiles[path]))
		} else if _, ok := childFiles[path]; !ok {
			b.WriteString(renderRemovedFilePatch(parentFiles[path]))
		} else {
			b.WriteString(renderReplacedFilePatch(parentFiles[path], childFiles[path]))
		}
	}
	return strings.TrimSpace(b.String())
}

func renderAddedFilePatch(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var b strings.Builder
	for _, line := range splitPatchLines(data) {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

func renderRemovedFilePatch(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var b strings.Builder
	for _, line := range splitPatchLines(data) {
		b.WriteString("-" + line + "\n")
	}
	return b.String()
}

func renderReplacedFilePatch(before []byte, after []byte) string {
	var b strings.Builder
	for _, line := range splitPatchLines(before) {
		b.WriteString("-" + line + "\n")
	}
	for _, line := range splitPatchLines(after) {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

func splitPatchLines(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func lineCount(data []byte) int {
	return len(splitPatchLines(data))
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))

	var b strings.Builder

	lastDash := false

	for _, r := range name {
		keep := unicode.IsLetter(r) || unicode.IsDigit(r)
		if keep {
			b.WriteRune(r)

			lastDash = false

			continue
		}

		if !lastDash {
			b.WriteByte('-')

			lastDash = true
		}
	}

	return strings.Trim(b.String(), "-")
}

const writeDescriptionMissingCode = "write_description_missing"

// CheckDescription returns a guard result when a described-write tool was
// invoked without a Description.
func CheckDescription(req workspace.ToolRequest) *workspace.ToolResult {
	if !workspace.IsWorkspacePerEditWriteTool(req.Tool) {
		return nil
	}

	if strings.TrimSpace(req.Description) != "" {
		return nil
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%s requires a \"description\" field before execution.\n", req.Tool)
	b.WriteString("The write was rejected without mutating files. Provide one sentence of intent on any future write request that uses this tool.")

	return &workspace.ToolResult{
		Tool:    req.Tool,
		Code:    writeDescriptionMissingCode,
		Summary: "Missing write description",
		Error:   strings.TrimSpace(b.String()),
	}
}
