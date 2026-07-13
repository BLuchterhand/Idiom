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
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/workerruntime"
)

const implementationTargetWriteReadySourceProbeToolsPhrase = "read_file/search_content/file_stats"

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
	case workspace.ToolNameSearchContent:
		if paths := sourceLikePathsFromText(result.Output, 6); len(paths) > 0 {
			return "Search hits included " + formatInlinePathList(paths, 6)
		}

		return summary
	case workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameCodeNavigate, workspace.ToolNameReadFile, workspace.ToolNamePrefetch, workspace.ToolNameSearchFiles:
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

func presentableExactProbeResult(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(result.Tool)
	if workspace.IsLanguageSourceBodyTool(tool) {
		return true
	}

	switch tool {
	case workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		return len(newToolResultEvidenceRecord(result).SourceWindowPaths()) > 0
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

func (r *runExecution) beginReadOnlyChildLifecycle(agentName string, opts llama.ChatOptions, policy RunPolicy) childLifecycleContext {
	var client llama.LLMClient
	if r != nil {
		client = r.runner.Client
	}
	return r.beginChildLifecycle("child_read_only:"+agentName, "child_read_only", autonomy.ChildWorkspaceNoWrite, opts, policy, false, client)
}

func (r *runExecution) beginWorkbranchChildLifecycle(agentName string, opts llama.ChatOptions, policy RunPolicy, client llama.LLMClient) childLifecycleContext {
	return r.beginChildLifecycle("child_write:"+agentName, "child_write", autonomy.ChildWorkspaceWorkbranch, opts, policy, false, client)
}

func (r *runExecution) beginChildLifecycle(role string, phase string, mode autonomy.ChildWorkspaceMode, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) childLifecycleContext {
	now := time.Now().UTC()
	brief := workerruntime.WorkerRuntimeBrief{}
	if r != nil {
		brief = r.state.runtimeBrief
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

func (r *runExecution) emitReadOnlyChildTerminal(ctx childLifecycleContext, result workspace.ToolResult, childResults []workspace.ToolResult, status autonomy.ChildTerminalStatus) {
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

func (r *runExecution) emitWorkbranchChildTerminal(ctx childLifecycleContext, checkout Checkout, result workspace.ToolResult, diff Diff, status autonomy.ChildTerminalStatus) {
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

func (r *runExecution) emitChildTerminalAcceptance(ctx childLifecycleContext, terminal autonomy.ChildTerminalRecord, decision autonomy.ChildAcceptanceDecision, reasonCode string) {
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

func (r *runExecution) emitChildLifecycleRecord(role string, record childLifecycleRecord) bool {
	if r == nil || r.runner.OnEvent == nil || strings.TrimSpace(role) == "" || record == nil {
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

func (r *runExecution) childLifecycleParentContractID() string {
	if r != nil && r.state.autonomyRecorder != nil {
		if contractID := strings.TrimSpace(r.state.autonomyRecorder.contract.ContractID); contractID != "" {
			return contractID
		}
	}

	lane := ""
	objective := ""
	if r != nil {
		lane = r.state.runtimeBrief.Lane
		objective = r.state.runtimeBrief.Objective
	}
	return childLifecycleID("parent_contract", lane, objective)
}

func (r *runExecution) childLifecycleChildContractID(parentContractID string, lane string, phase string, role string, brief workerruntime.WorkerRuntimeBrief) string {
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

func (r *runExecution) childRuntimeEnvelope(role string, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) autonomy.ChildRuntimeEnvelope {
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

func (r *runExecution) evaluateFinalAnswerAcceptance(answer string, results []workspace.ToolResult) finalAnswerAcceptance {
	acceptance := newFinalAnswerAcceptance()
	if r != nil {
		acceptance.EvidencePaths = acceptedFinalAnswerEvidencePaths(answer, r.state.runtimeBrief, results)
	}

	var directAnswerRelevance readOnlyDirectAnswerRelevance

	if r != nil {
		projection := newToolResultEvidenceProjection(results)
		directAnswerRelevance = readOnlyClosureDecisionForProjection(
			readOnlyDirectAnswerAcceptanceLedgerBrief(r.state.runtimeBrief),
			projection,
		).DirectAnswerRelevance(answer)
		acceptance = finalAnswerAcceptanceWithDirectAnswerRelevance(acceptance, directAnswerRelevance)
	}

	completionState := r.implementationCompletionStateForTerminalAnswer(answer, results)
	typedDeliverableReady := completionState.CloseoutReady() &&
		(completionState.TerminalDisposition == implementationTerminalDispositionPlanSubmitted ||
			completionState.TerminalDisposition == implementationTerminalDispositionSourceSatisfied)

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
	if strings.TrimSpace(answer) == "" {
		return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonNonAnswerProse)
	}

	if r != nil && runnerBriefRequiresMutation(r.state.runtimeBrief) {
		completionState := r.implementationCompletionStateForTerminalAnswer(answer, results)
		typedBlockerReady := completionState.TerminalDisposition == implementationTerminalDispositionExactBlocker && completionState.CloseoutReady()
		if len(completionState.MissingRequiredPaths) > 0 && !typedBlockerReady {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEditPathsRemaining, turnReasonRequiredEditPathsRemaining)
		}

		if len(completionState.MissingRequiredSnippetIDs) > 0 && !typedBlockerReady {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}

		if len(completionState.OpenTaskLedgerObligationIDs) > 0 && !typedBlockerReady &&
			(r.taskLedger == nil || !r.taskLedger.terminalAnswerSatisfies(answer, r.state.runtimeBrief)) {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}

		if len(completionState.UnresolvedRejectedMutationPaths) > 0 && !typedBlockerReady {
			acceptance.EvidencePaths = textutil.Merge(acceptance.EvidencePaths, completionState.UnresolvedRejectedMutationPaths)
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRejectedMutationPathsRemaining, turnReasonRejectedMutationPathsRemaining)
		}
	}

	if questionProtocolAnswer(answer) {
		if r != nil && readOnlyDirectAnswerLane(r.state.runtimeBrief) && !runnerBriefRequiresMutation(r.state.runtimeBrief) {
			// Accept the blocker if the model has gathered source-body
			// evidence and the blocker names an exact path or claim.
			// This prevents the dead-end loop where the model can't
			// proceed and can't say "I'm stuck" either.
			if len(newToolResultEvidenceProjection(results).SourceWindowPaths()) > 0 && isSpecificQuestionBlocker(answer) {
				return rejectFinalAnswerAcceptance(acceptance, turnViolationUserVisibleBlocker, turnReasonUserVisibleBlocker)
			}
			return rejectFinalAnswerAcceptance(acceptance, turnViolationReadOnlyProtocolBlocker, turnReasonReadOnlyProtocolBlocker)
		}

		return rejectFinalAnswerAcceptance(acceptance, turnViolationUserVisibleBlocker, turnReasonUserVisibleBlocker)
	}

	if r != nil &&
		readOnlyAnswerReportsInsufficientEvidence(answer) &&
		readOnlyClosureDecisionForResults(r.state.runtimeBrief, results).Answerable() {

		return rejectFinalAnswerAcceptance(acceptance, turnViolationNonAnswerProse, turnReasonAnswerableEvidenceInsufficient)
	}

	if r != nil &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(r.state.runtimeBrief.PermissionProfile) &&
		workerruntime.RuntimeContractIsArchitectureOrReview(r.state.runtimeBrief.Contract) &&
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

	// A typed terminal disposition satisfies the deliverable, but never
	// bypasses the universal user-facing answer-shape and meta-answer checks
	// above. The executor phase owns file changes after submit_plan.
	if typedDeliverableReady {
		return finalAnswerAcceptanceWithPolicy(acceptance)
	}

	if r != nil &&
		runnerBriefRequiresMutation(r.state.runtimeBrief) &&
		workerruntime.RuntimeContractMatches(r.state.runtimeBrief.Contract, workerruntime.ContractImplementationResult) &&
		!completionState.HasChanges() {

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
		if runnerBriefRequiresMutation(r.state.runtimeBrief) &&
			workerruntime.RuntimeContractMatches(r.state.runtimeBrief.Contract, workerruntime.ContractImplementationResult) &&
			(completionState.VerificationPending || completionState.SemanticVerificationPending) {

			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}

		missing := missingRequiredEvidence(r.state.runtimeBrief.RequiredEvidence, r.finalEvidenceProjection(answer, results))
		if len(missing) > 0 {
			return rejectFinalAnswerAcceptance(acceptance, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)
		}
	}

	return finalAnswerAcceptanceWithPolicy(acceptance)
}

func finalAnswerAcceptanceAllowsObjectiveDiagnosticNonAnswer(acceptance finalAnswerAcceptance, answer string, r *runExecution) bool {
	return r != nil &&
		readOnlyDirectAnswerLane(r.state.runtimeBrief) &&
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
	mentioned := readOnlySourceLikePaths(analyzeSourceCitations(answer).BodyReadablePaths(), 16)
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
		projection := newToolResultEvidenceProjection(results)
		diagnosticPaths := projection.ClaimedDiagnosticEvidencePaths(answer, 8)
		diagnosticPaths = intersectRunnerPaths(diagnosticPaths, readOnlyDiagnosticEvidencePaths(ledger.DiagnosticPaths, 0))
		paths = textutil.Merge(paths, diagnosticPaths, projection.ClaimedDiagnosticLogPaths(answer, 8))
	}

	return readOnlyEvidenceDisplayPaths(paths, 8)
}

func acceptedFeaturePlannerPlanArtifactPaths(answer string, ledger readOnlyAnswerabilityLedger) []string {
	mentioned := analyzeSourceCitations(answer).BodyReadablePaths()
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
	mentioned := analyzeSourceCitations(answer).BodyReadablePaths()
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
	rightSet := runnerArtifactPathSet(right)
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

func runnerArtifactPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		if runnerPathEscapesWorkspace(raw) {
			continue
		}
		path := NormalizeRunnerPath(raw)
		if path == "" || path == "." || path == ".." {
			continue
		}
		set[path] = struct{}{}
	}
	return set
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

func (r *runExecution) planThenExecuteAnchorRefreshFinalAnswerPending(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	state := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
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

func (r *runExecution) decorateFinalAnswer(input FinalAnswerDecorateInput) string {
	answer := strings.TrimSpace(input.Answer)
	if answer == "" || !input.UserFacing {
		return answer
	}

	if r != nil {
		completionState := r.implementationCompletionStateForTerminalAnswer(answer, input.Results)
		if completionState.TerminalDisposition == implementationTerminalDispositionExactBlocker && completionState.CloseoutReady() {
			return composeTypedTaskLedgerBlockerStatus(completionState.BlockedTaskLedgerItems)
		}
	}

	return answer
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

		if paths := closurePromptEvidencePaths(closeout.AcceptedAnswerEvidencePaths(""), max); len(paths) > 0 {
			return "Inspected evidence", paths
		}

		projection := newToolResultEvidenceProjection(results)
		if paths := projection.promptEvidencePaths(max); len(paths) > 0 {
			return "Inspected evidence", paths
		}

		return "", nil
	}

	projection := newToolResultEvidenceProjection(results)
	paths := closurePromptEvidencePaths(acceptedFinalAnswerEvidencePaths("", brief, results), max)
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

func (r *runExecution) userFacingFinalAnswer() bool {
	return userFacingAgentName(r.agentName())
}

func (r *runExecution) emitFinalAnswerDecorationEvent(stopReason string, before string, after string) {
	if r == nil || r.runner.OnEvent == nil || strings.TrimSpace(before) == strings.TrimSpace(after) {
		return
	}

	payload := map[string]any{
		"agent":        r.agentName(),
		"stop_reason":  strings.TrimSpace(stopReason),
		"before_chars": len(strings.TrimSpace(before)),
		"after_chars":  len(strings.TrimSpace(after)),
	}
	if data, err := json.Marshal(payload); err == nil {
		r.runner.OnEvent(RunEvent{Role: "answer_decorated", Content: string(data)})
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
	path = cleanRelativeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !workspace.ToolSurfacePhaseAllowsTool(result.Tool, workspace.ToolSurfacePhasePlanExecuteHistoryPending) {
			continue
		}

		if result.Facts == nil || cleanRelativeRunnerPath(result.Facts.GitHistoryPath) != path {
			continue
		}

		switch result.Facts.GitHistoryStatus {
		case toolfacts.GitHistoryStatusCommitDescriptions, toolfacts.GitHistoryStatusNotApplicable:
			return true
		}
	}

	return false
}

func (r *runExecution) submitPlanPathsMissingGitHistory(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	paths, ok := deferredPlanPathsForRequest(req)
	if !ok {
		return nil
	}

	return r.pathsMissingGitHistory(paths, results)
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

func (r *runExecution) workspaceRootForGitHistoryRequirement() string {
	if r == nil || r.runner.Workspace == nil {
		return ""
	}

	return strings.TrimSpace(r.runner.Workspace.Root())
}

func (r *runExecution) pathsMissingGitHistory(paths []string, results []workspace.ToolResult) []string {
	canonical, ok := canonicalRunnerWorkspaceRelativePaths(paths)
	if !ok {
		return nil
	}

	var missing []string

	for _, path := range canonical {
		if !r.existingFileNeedsGitHistory(path, results) {
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

func (r *runExecution) existingFileNeedsGitHistory(path string, results []workspace.ToolResult) bool {
	path = cleanRelativeRunnerPath(path)
	if path == "" {
		return false
	}

	if r == nil || r.runner.Workspace == nil {
		return true
	}

	if !gitstate.HasMetadataAncestor(r.runner.Workspace.Root()) {
		return false
	}

	return newToolResultEvidenceProjection(results).HasSourceWindow(path)
}

func gitHistoryEvidenceRequiredMessage(paths []string) string {
	target := "the intended logic"
	if len(paths) > 0 {
		target = quotedRunnerStrings(paths)
	}

	return fmt.Sprintf("git history rationale is required before changing established logic for %s. Use `history_range` or `history_symbol` on the intended logic, read the commit descriptions to understand why and when the current behavior was introduced, then make the write only if that history supports the proposed change. If history shows the change is unsafe, answer `<<QUESTION>>:` with the blocker; genuinely new files do not require history evidence.", target)
}

func planThenExecuteUnavailableHistoryToolNudge(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, allowedTools []string, authority planThenExecutePathAuthority) (string, string, bool) {
	tool := strings.TrimSpace(req.Tool)
	if !workspace.ToolSurfacePhaseAllowsTool(tool, workspace.ToolSurfacePhasePlanExecuteHistoryPending) {
		return "", "", false
	}

	if slices.Contains(allowedTools, tool) || !mutationHistoryEvidenceRequired(brief) {
		return "", "", false
	}

	completed := modelProtocolCompletedGitHistoryPaths(brief, results)
	pending := modelProtocolPendingGitHistoryPaths(brief, results, authority)
	sourcePending := modelProtocolSourceBodyObligations(brief, results, TurnPhaseContract{PhaseID: contractcompile.TurnPhaseRequiredSource}, authority)

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

func (r *runExecution) preflightPlanThenExecuteSubmitPlanHistoryGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	missing, ok := r.submitPlanHistoryPendingForRequest(req, results)
	if !ok {
		return verificationInterceptResult{}
	}

	return submitPlanHistoryGuardForMissing(missing)
}

func (r *runExecution) submitPlanHistoryPendingForRequest(req workspace.ToolRequest, results []workspace.ToolResult) ([]string, bool) {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return nil, false
	}

	if !mutationHistoryEvidenceRequired(r.state.runtimeBrief) {
		return nil, false
	}

	if len(r.planThenExecutePathAuthority().requiredCoveragePaths(r.state.runtimeBrief)) > 0 &&
		len(planThenExecuteUnreadRequiredPaths(r.state.runtimeBrief, results, r.planThenExecutePathAuthority())) > 0 {

		return nil, false
	}

	if planThenExecuteSubmitPlanHasRequiredPathCoverageProblem(r.state.runtimeBrief.RequiredEditPaths, req) ||
		len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {

		return nil, false
	}
	if r.preflightPlanThenExecuteSubmitPlanCoverageGuard(req).Intercepted ||
		r.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, results).Intercepted {

		return nil, false
	}

	missing := r.submitPlanPathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		return nil, false
	}

	return missing, true
}

func submitPlanHistoryGuardForMissing(missing []string) verificationInterceptResult {
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

const planThenExecuteSubmitPlanHistoryDeferredCode = "submit_plan.deferred_by_history_prerequisite"

func (r *runExecution) submitPlanHistoryDeferralResult(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	pending, ok := r.submitPlanHistoryPendingForRequest(req, results)
	if !ok {
		return workspace.ToolResult{}, false
	}

	guard := submitPlanHistoryGuardForMissing(pending)

	guard.Result.Code = planThenExecuteSubmitPlanHistoryDeferredCode
	guard.Result.Facts = &toolfacts.ToolResultFacts{
		DeferredPlanPaths:    append([]string(nil), pending...),
		PendingHistoryPaths:  append([]string(nil), pending...),
		RejectionDisposition: toolfacts.ToolRejectionDeferredByPrerequisite,
	}

	return guard.Result, true
}

func deferredPlanPathsForRequest(req workspace.ToolRequest) ([]string, bool) {
	paths := make([]string, 0, len(planThenExecuteEffectiveSubmitPlanEdits(req)))
	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		paths = append(paths, edit.Path)
	}

	return canonicalRunnerWorkspaceRelativePaths(paths)
}

func deferredPlanPrerequisitesSatisfied(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	requestPaths, ok := deferredPlanPathsForRequest(req)
	if !ok {
		return false
	}
	requestPathSet := make(map[string]struct{}, len(requestPaths))
	for _, path := range requestPaths {
		requestPathSet[path] = struct{}{}
	}

	var pendingPaths []string
	for _, result := range results {
		if result.Tool != workspace.ToolNameSubmitPlan || result.Facts == nil || result.Facts.RejectionDisposition != toolfacts.ToolRejectionDeferredByPrerequisite {
			continue
		}
		candidate, ok := canonicalRunnerWorkspaceRelativePaths(result.Facts.DeferredPlanPaths)
		if !ok {
			continue
		}
		candidateMatchesRequest := true
		for _, path := range candidate {
			if _, ok := requestPathSet[path]; !ok {
				candidateMatchesRequest = false
				break
			}
		}
		if candidateMatchesRequest {
			pendingPaths = textutil.Merge(pendingPaths, candidate)
		}
	}
	if len(pendingPaths) == 0 {
		return false
	}

	for _, path := range pendingPaths {
		if !successfulGitHistoryResultForPath(path, results) {
			return false
		}
	}

	return true
}

func planThenExecuteSubmitPlanHistoryMissing(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) bool {
	if !mutationHistoryEvidenceRequired(brief) {
		return false
	}

	var paths []string

	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if newToolResultEvidenceProjection(results).HasSourceWindow(path) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true}, authority)
		for _, path := range state.SelectedEditPaths {
			if newToolResultEvidenceProjection(results).HasSourceWindow(path) {
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

func (r *runExecution) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Content)
	}

	if prompt == "" {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameTask,
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
			Tool:    workspace.ToolNameTask,
			Summary: "Child task rejected",
			Error:   "only read_only child tasks are currently supported",
		}
	}

	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "child"
	}

	prompt = strings.TrimSpace(prompt)

	childPolicy := RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3, MaxModelCalls: 6}

	if workerruntime.RuntimeContractIsDirectAnswer(r.state.runtimeBrief.Contract) {
		childPolicy.MaxToolSteps = 4
		childPolicy.MaxModelCalls = 6
	}

	if effective := r.effectivePolicy(); effective.MaxContextMessages > 0 || effective.MaxContextChars > 0 {
		childPolicy.MaxContextMessages = effective.MaxContextMessages
		childPolicy.MaxContextChars = effective.MaxContextChars
	}

	lifecycle := r.beginReadOnlyChildLifecycle(agentName, opts, childPolicy)

	childBrief := workerruntime.RuntimeBriefForRole("child_read_only", "child:"+agentName, prompt)

	child := &Runner{
		Client:           r.runner.Client,
		Workspace:        r.runner.Workspace,
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

	childRun := newRunExecution(
		child,
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
	)
	answer, err := childRun.runSessionLoop(
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
			Tool:    workspace.ToolNameTask,
			Summary: "Child task failed",
			Error:   err.Error(),
		}
		r.emitReadOnlyChildTerminal(lifecycle, result, childResults, autonomy.ChildTerminalFailed)
		return result
	}

	answer = normalizeDelegatedReadOnlyPacket(answer)

	result := workspace.ToolResult{
		Tool:    workspace.ToolNameTask,
		Summary: "Child task completed",
		Output:  strings.TrimSpace(answer),
	}

	if packet, ok := buildReadOnlyEvidencePacket(prompt, answer, childResults); ok {
		result.Output = renderReadOnlyEvidencePacketForToolOutput(packet, answer)
		result = readOnlyEvidencePacketToolResult(result, packet)
	}

	status := autonomy.ChildTerminalCompleted
	if blockerKind, _ := childRun.finalAnswerBlockerKind(answer, childResults); strings.TrimSpace(answer) == "" || questionProtocolAnswer(answer) || blockerKind != "" {
		status = autonomy.ChildTerminalBlocked
		result.Summary = "Child task blocked"
	}

	r.emitReadOnlyChildTerminal(lifecycle, result, childResults, status)

	return result
}

func (r *runExecution) prepareReadOnlyChildPrompt(prompt string) string {
	return strings.TrimSpace(prompt)
}

func isWriteChildMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "write", "workbranch", "workbranch_write", "isolated_write", "patch":
		return true
	default:
		return false
	}
}

func (r *runExecution) runWorkbranchChildTask(ctx context.Context, req workspace.ToolRequest, prompt string, opts llama.ChatOptions) workspace.ToolResult {
	if r == nil || r.runner.Workspace == nil || strings.TrimSpace(r.runner.Workspace.Root()) == "" {
		return workspace.ToolResult{Tool: workspace.ToolNameTask, Summary: "Workbranch child task rejected", Error: "parent workspace root is required for isolated write child tasks"}
	}

	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "worker"
	}

	manager := Manager{Root: r.runner.Workspace.Root()}

	checkout, err := manager.Create(ctx, agentName)
	if err != nil {
		return workspace.ToolResult{Tool: workspace.ToolNameTask, Summary: "Workbranch child task failed", Error: err.Error()}
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

	childClient := r.runner.Client
	if r.runner.ImplementerClient != nil {
		childClient = r.runner.ImplementerClient
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
		result := workspace.ToolResult{Tool: workspace.ToolNameTask, Summary: "Workbranch child task failed", Error: runErr.Error(), Output: renderWorkbranchChildOutput(checkout, diff, answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	if diffErr != nil {
		result := workspace.ToolResult{Tool: workspace.ToolNameTask, Summary: "Workbranch child diff failed", Error: diffErr.Error(), Output: strings.TrimSpace(answer)}
		r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalFailed)
		return result
	}

	result := workspace.ToolResult{
		Tool:    workspace.ToolNameTask,
		Summary: fmt.Sprintf("Workbranch child task completed with %d changed path(s)", len(diff.ChangedPaths)),
		Output:  renderWorkbranchChildOutput(checkout, diff, answer),
	}
	r.emitWorkbranchChildTerminal(lifecycle, checkout, result, diff, autonomy.ChildTerminalCompleted)
	return result
}

func (r *runExecution) runWorkbranchChildModel(ctx context.Context, child *Runner, lifecycle childLifecycleContext, childWorkspaceDescription string, childWorkspaceInstructions string, prompt string, opts llama.ChatOptions) (answer string, err error) {
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

func allowsDocumentationOnlyPacket(objective string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	return ContainsAny(lower, " readme ", " doc ", " docs ", " documentation ", " markdown ")
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

func exactAnchorPreWriteSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !exactAnchorImplementationActive(brief) {
		return false
	}

	return !mutationCommitted
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
		"resolve the required-path mutation obligation with an authorized edit or exact blocker; permission-authorized inspection remains callable for missing evidence",
	}, brief.RequiredNext)
	brief.SearchPolicy = textutil.Merge([]string{
		"required edit paths define the implementation scope",
		"permission-authorized read, search, context, and project discovery capabilities remain callable while the mutation obligation is open",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetWriteReadyRuntimeBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, mutationCommitted bool) workerruntime.WorkerRuntimeBrief {
	sourceRefresh := implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, brief.AllowedTools, mutationCommitted)
	sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3)
	allowed := implementationTargetWriteReadyAllowedToolsForBrief(brief.AllowedTools, brief, results, mutationCommitted)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch}
	brief.Constraints = textutil.Merge([]string{"implementation_target_write_ready"}, brief.Constraints)
	brief.RequiredNext = textutil.Merge([]string{
		"the sourced target is write-ready; an authorized write or exact blocker can resolve it, and permission-authorized inspection remains callable",
		"for delete/remove-file objectives, use apply_patch with `*** Delete File: <path>` after source evidence; do not use write_file with empty content as deletion",
	}, brief.RequiredNext)

	if sourceRefresh {
		brief.RequiredNext = textutil.Merge([]string{
			"the sourced target is write-ready; permission-authorized inspection remains callable when another edit-safety fact is needed",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"permission-authorized inspection remains callable while the write obligation is open",
		}, brief.SearchPolicy)
		brief.Constraints = textutil.Merge([]string{"bounded_source_refresh_allowance"}, brief.Constraints)
	} else {
		brief.InvalidActions = textutil.Merge([]string{
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"permission-authorized inspection remains callable while the write obligation is open",
		}, brief.SearchPolicy)
	}

	if sourcedSummary != "" {
		brief.RequiredNext = textutil.Merge([]string{
			"current source-body evidence is available for: " + sourcedSummary,
		}, brief.RequiredNext)
		brief.SearchPolicy = textutil.Merge([]string{
			"already-sourced path(s) remain available to permission-authorized inspection: " + sourcedSummary,
		}, brief.SearchPolicy)
		brief.Constraints = textutil.Merge([]string{"source_body_evidence_paths: " + sourcedSummary}, brief.Constraints)
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
		"permission-authorized inspection remains callable while source and mutation obligations are open",
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
	brief.InvalidActions = textutil.Merge([]string{
		"verification before first exact-anchor write",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"target-packet exact anchors count as current-state path and source evidence for the first write",
		"permission-authorized inspection remains callable before the first write",
	}, brief.SearchPolicy)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetWriteReadyRecoveryInstruction(results []workspace.ToolResult) string {
	sourceSentence := "Source-body evidence is already available"
	if sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3); sourcedSummary != "" {
		sourceSentence += " for: " + sourcedSummary
	}

	return sourceSentence + ". The active request is the workspace write follow-up, not another prose answer to the earlier identification question. Make a parser-readable write when the edit-safety facts are known; otherwise take a bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " probe with `named_gap` for the exact missing fact, or answer <<QUESTION>> with the exact blocker/no-op evidence."
}

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

	return newToolResultEvidenceProjection(results).HasSourceWindow(path)
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

func planThenExecuteGroundingRejectedSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
			return true
		}
	}

	return false
}

func contextPackCandidateReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	target := NormalizeRunnerPath(req.Path)
	if target == "" || !pathparse.HasWorkspaceFileExtension(target) {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, candidate := range toolResultFactsCandidatePaths(result.Facts, 32) {
			if candidate == target {
				return true
			}
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

func (r *runExecution) planThenExecuteReviewRepairCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || strings.TrimSpace(r.state.runtimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return workspace.ToolResult{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolResult{}, false
	}

	completionState := r.implementationCompletionStateForTerminalAnswer(reply, results)
	if completionState.TerminalDisposition == implementationTerminalDispositionExactBlocker && completionState.CloseoutReady() {
		return workspace.ToolResult{}, false
	}

	if plancontract.ReviewRepairAnswerDeclaresPass(reply) && plancontract.ReviewRepairTextHasChecklist(reply) {
		if failed := planThenExecuteReviewRepairFailedPhaseObligations(r.state.runtimeBrief); len(failed) > 0 {
			return completionGateRejectionWithRecovery(
				"Review-repair completion gate rejected contradictory pass",
				"review-repair phase evidence still has failed obligations "+quotedRunnerStrings(failed)+"; choose a corrective submit_plan inside allowed paths or `<<QUESTION>>:` with the exact blocker instead of `Decision: pass`.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "review_repair_contradictory_pass", completionGateViolationPlanSubmission, nil),
			), true
		}

		return workspace.ToolResult{}, false
	}

	return completionGateRejectionWithRecovery(
		"Review-repair completion gate rejected final answer",
		strings.Join([]string{
			"review-repair must close with exactly one of: a successful submit_plan corrective plan,",
			"`Decision: pass` with a `Verification Checklist`,",
			"or `<<QUESTION>>:` naming the exact blocker.",
			"Do not stop with process narration or an intent to inspect.",
		}, " "),
		newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "review_repair_closeout", completionGateViolationPlanSubmission, nil),
	), true
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

func reviewRepairPhaseEvidencePacket(objective string) []string {
	evidence := reviewRepairPhaseEvidenceFromObjective(objective)
	if evidence == "" {
		return nil
	}

	return []string{"phase_evidence_bundle:\n" + evidence}
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

func canonicalRunnerWorkspaceRelativePaths(paths []string) ([]string, bool) {
	if len(paths) == 0 {
		return nil, false
	}

	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := cleanRelativeRunnerPath(raw)
		if path == "" {
			return nil, false
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil, false
	}

	sort.Strings(out)

	return out, true
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
	return reviewRepairCompletionGateViolation(result)
}

func reviewRepairTerminalValidNextActions() []string {
	return []string{
		"`Decision: pass` with a `Verification Checklist`",
		"a corrective `submit_plan` whose rationale starts with `Verification Checklist:`",
		"`<<QUESTION>>:` naming the exact blocker that prevents a terminal review-repair outcome",
		"a permission-authorized inspection request for one concrete missing review fact",
	}
}

func reviewRepairTerminalInvalidNextActions() []string {
	return []string{
		"a prose-only final response that omits the required terminal review outcome",
	}
}

type taskLedgerState struct {
	manager *workspace.Manager
	policy  TaskLedgerPolicy
	created bool
	items   []workspace.TaskLedgerItem
}

func newTaskLedgerState(policy TaskLedgerPolicy, manager *workspace.Manager) *taskLedgerState {
	return &taskLedgerState{manager: manager, policy: policy}
}

func (s *taskLedgerState) apply(req workspace.ToolRequest, receiptBatches ...[]workspace.ToolResult) workspace.ToolResult {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameTaskLedger,
			Summary: "Task ledger rejected",
			Error:   "task_ledger is disabled for this runner",
		}
	}
	if s.manager == nil {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameTaskLedger,
			Summary: "Task ledger rejected",
			Error:   "task_ledger requires the active workspace authority",
		}
	}

	items := taskLedgerItemsFromRequest(req)
	if len(items) == 0 {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameTaskLedger,
			Summary: "Task ledger rejected",
			Error:   "provide a non-empty todos array with content and status for each item",
		}
	}

	normalized := make([]workspace.TaskLedgerItem, 0, len(items))
	seenIDs := make(map[string]struct{}, len(items))
	active := 0

	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		status, validStatus := taskledger.Parse(string(item.Status))
		item.Status = status
		item.Evidence = strings.TrimSpace(item.Evidence)

		if item.ID == "" {
			item.ID = fmt.Sprintf("item-%d", i+1)
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo ID %q is duplicated; every task ledger item must have a unique ID", item.ID),
			}
		}
		seenIDs[item.ID] = struct{}{}

		if item.Content == "" {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s is missing content", item.ID),
			}
		}

		if !validStatus {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s has unsupported status %q; use pending, in_progress, completed, blocked, or cancelled", item.ID, item.Status),
			}
		}

		blocker, err := s.manager.NormalizeTaskLedgerBlocker(item.Status, item.Blocker)
		if err != nil {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s has invalid blocker: %v", item.ID, err),
			}
		}
		item.Blocker = blocker

		if item.Status == taskledger.StatusBlocked && item.Evidence != "" {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s is blocked; use blocker.kind, blocker.subject, and blocker.reason instead of evidence prose", item.ID),
			}
		}

		if item.Status == taskledger.StatusCompleted && item.Evidence == "" {
			if previous, ok := s.previousItem(item.ID); ok && previous.Status == taskledger.StatusCompleted {
				item.Evidence = strings.TrimSpace(previous.Evidence)
			}
		}

		if item.Status == taskledger.StatusInProgress {
			active++
		}

		normalized = append(normalized, item)
	}

	if active > 1 {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameTaskLedger,
			Summary: "Task ledger rejected",
			Error:   "only one task ledger item may be in_progress at a time",
		}
	}

	receipts := flattenTaskLedgerReceiptBatches(receiptBatches)
	if s.policy.RequireEvidence {
		if err := s.validateCompletedEvidence(normalized, receipts); err != "" {
			return workspace.ToolResult{
				Tool:    workspace.ToolNameTaskLedger,
				Summary: "Task ledger rejected",
				Error:   err,
			}
		}
	}

	s.created = true
	s.items = normalized

	return workspace.ToolResult{
		Tool:    workspace.ToolNameTaskLedger,
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
		if item.Status != taskledger.StatusCompleted {
			continue
		}

		if strings.TrimSpace(item.Evidence) == "" {
			return fmt.Sprintf("todo %s is marked completed but has no evidence; use evidence refs such as write:path, read:path, verify:command, or artifact:id backed by prior tool results", item.ID)
		}

		refs := parseTaskLedgerEvidenceRefs(item.Evidence)
		if len(refs) == 0 {
			return fmt.Sprintf("todo %s evidence must contain at least one typed write:, read:, verify:, or artifact: reference", item.ID)
		}

		for _, ref := range refs {
			if !taskLedgerEvidenceRefSatisfied(ref, receipts) {
				return fmt.Sprintf("todo %s evidence %q is not backed by prior successful tool results", item.ID, ref)
			}
		}
	}

	return ""
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
		return taskLedgerReadEvidenceSatisfied(value, receipts)
	case "verify":
		return taskLedgerVerificationEvidenceSatisfied(value, receipts)
	case "artifact":
		return taskLedgerArtifactEvidenceSatisfied(value, receipts)
	default:
		return false
	}
}

func taskLedgerWriteEvidenceSatisfied(path string, receipts []workspace.ToolResult) bool {
	want := pathparse.NormalizeWorkspaceRelativePath(path)
	if want == "" {
		return false
	}

	for _, receipt := range receipts {
		for _, got := range committedMutationEffectIDsForResult(receipt) {
			if pathparse.NormalizeWorkspaceRelativePath(got) == want {
				return true
			}
		}
	}

	return false
}

func taskLedgerReadEvidenceSatisfied(path string, receipts []workspace.ToolResult) bool {
	want := pathparse.NormalizeWorkspaceRelativePath(path)
	if want == "" {
		return false
	}

	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Error) != "" || receipt.Facts == nil || taskLedgerControlResult(receipt.Tool) {
			continue
		}
		record := newToolResultEvidenceRecord(receipt)
		for _, window := range record.sourceWindowFacts {
			if pathparse.NormalizeWorkspaceRelativePath(window.CanonicalID) == want {
				return true
			}
		}
		for _, logRead := range record.DiagnosticLogReads() {
			if logRead.CanonicalID == want || logRead.PolicySubject == want {
				return true
			}
		}
	}

	return false
}

func taskLedgerControlResult(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameTaskLedger, completionGateToolName:
		return true
	default:
		return false
	}
}

func taskLedgerVerificationEvidenceSatisfied(commandOrKind string, receipts []workspace.ToolResult) bool {
	wantCommand := plancontract.CanonicalVerificationCommand(commandOrKind, nil)
	wantKind := toolfacts.VerificationEvidenceKind(strings.TrimSpace(commandOrKind))
	if wantCommand == "" && wantKind == "" {
		return false
	}

	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Error) != "" || receipt.Facts == nil || taskLedgerControlResult(receipt.Tool) || !workspace.AnalyzeVerificationResult(receipt).Successful {
			continue
		}
		if actual := plancontract.CanonicalVerificationCommand(receipt.Facts.ShellCommand, nil); actual != "" && actual == wantCommand {
			return true
		}
		for _, evidence := range receipt.Facts.VerificationEvidence {
			if evidence.Kind != "" && evidence.Kind == wantKind {
				return true
			}
		}
	}

	return false
}

func taskLedgerArtifactEvidenceSatisfied(id string, receipts []workspace.ToolResult) bool {
	want := strings.TrimSpace(id)
	if want == "" {
		return false
	}

	for _, receipt := range receipts {
		if strings.TrimSpace(receipt.Error) != "" || receipt.Facts == nil || taskLedgerControlResult(receipt.Tool) {
			continue
		}
		for _, artifactID := range receipt.Facts.ArtifactIDs {
			if strings.TrimSpace(artifactID) == want {
				return true
			}
		}
	}

	return false
}

func (s *taskLedgerState) completionRejection(reply string, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolResult, bool) {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{}, false
	}

	if s.policy.Required && !s.created && !s.terminalAnswerSatisfies(reply, brief) {
		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantTaskLedger, "task_ledger_missing", completionGateViolationTaskLedger, nil)
		envelope.PendingObligationIDs = []string{"task_ledger:create"}
		return completionGateRejectionWithRecovery(
			"Task ledger completion gate rejected final answer",
			"this is complex multi-step work and no task_ledger exists. Create the ledger with the current item states and typed blockers, or if the user asked only for a plan, finish with an explicit planning-exit phrase.",
			envelope,
		), true
	}

	if !s.created {
		return workspace.ToolResult{}, false
	}

	if unfinished := s.unfinishedItems(); len(unfinished) > 0 && !s.terminalAnswerSatisfies(reply, brief) {
		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantTaskLedger, "task_ledger_unfinished", completionGateViolationTaskLedger, nil)
		envelope.PendingObligationIDs = s.unfinishedItemIDs()
		return completionGateRejectionWithRecovery(
			"Task ledger completion gate rejected final answer",
			"task ledger still has unfinished required work: "+strings.Join(unfinished, "; ")+". Continue the work, mark the ledger item with a concrete blocker, or if the user asked only for a plan, finish with an explicit planning-exit phrase (e.g. \"plan ready - awaiting approval\").",
			envelope,
		), true
	}

	if blocked := s.blockedItems(); len(blocked) > 0 && !s.terminalAnswerSatisfies(reply, brief) {
		envelope := newCompletionGateRecoveryEnvelope(completionGateInvariantTaskLedger, "task_ledger_blocked", completionGateViolationTaskLedger, nil)
		envelope.PendingObligationIDs = s.blockedItemIDs()
		return completionGateRejectionWithRecovery(
			"Task ledger completion gate rejected final answer",
			"task ledger contains blocked work: "+strings.Join(blocked, "; ")+". The final answer must state the concrete blocker.",
			envelope,
		), true
	}

	return workspace.ToolResult{}, false
}

func (s *taskLedgerState) unfinishedItems() []string {
	if s == nil {
		return nil
	}

	var unfinished []string

	for _, item := range s.items {
		if item.Status.Open() {
			unfinished = append(unfinished, item.ID+": "+item.Content+" ["+string(item.Status)+"]")
		}
	}

	return unfinished
}

func (s *taskLedgerState) unfinishedItemIDs() []string {
	if s == nil {
		return nil
	}
	var ids []string
	for _, item := range s.items {
		if item.Status.Open() {
			ids = append(ids, item.ID)
		}
	}
	return normalizeCompletionGateObligationIDs(ids)
}

func (s *taskLedgerState) blockedItems() []string {
	if s == nil {
		return nil
	}

	var blocked []string

	for _, item := range s.items {
		if item.Status != taskledger.StatusBlocked {
			continue
		}

		text := item.ID + ": " + item.Content
		if item.Blocker != nil {
			text += fmt.Sprintf(
				" (kind=%q; subject_namespace=%q; subject_id=%q; reason=%q)",
				item.Blocker.Kind,
				item.Blocker.Subject.Namespace,
				item.Blocker.Subject.ID,
				item.Blocker.Reason,
			)
		}

		blocked = append(blocked, text)
	}

	return blocked
}

func (s *taskLedgerState) blockedItemIDs() []string {
	if s == nil {
		return nil
	}
	var ids []string
	for _, item := range s.items {
		if item.Status == taskledger.StatusBlocked {
			ids = append(ids, item.ID)
		}
	}
	return normalizeCompletionGateObligationIDs(ids)
}

func (s *taskLedgerState) statusSummary() string {
	counts := make(map[taskledger.Status]int)
	for _, item := range s.items {
		counts[item.Status]++
	}

	parts := make([]string, 0, 5)

	for _, status := range []taskledger.Status{taskledger.StatusCompleted, taskledger.StatusInProgress, taskledger.StatusPending, taskledger.StatusBlocked, taskledger.StatusCancelled} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], string(status)))
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
		fmt.Fprintf(&b, "- status=%q; id=%q; content=%q", item.Status, item.ID, item.Content)

		if item.Evidence != "" {
			fmt.Fprintf(&b, "; evidence=%q", item.Evidence)
		}

		if item.Blocker != nil {
			fmt.Fprintf(
				&b,
				"; blocker_kind=%q; blocker_subject_namespace=%q; blocker_subject_id=%q; blocker_reason=%q",
				item.Blocker.Kind,
				item.Blocker.Subject.Namespace,
				item.Blocker.Subject.ID,
				item.Blocker.Reason,
			)
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

func (s *taskLedgerState) terminalAnswerSatisfies(reply string, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil {
		return false
	}

	if open := s.openItems(); len(open) > 0 {
		return taskLedgerPlanningExitAnswer(reply, brief)
	}
	if s.policy.Required && !s.created {
		return taskLedgerPlanningExitAnswer(reply, brief)
	}
	if blocked := s.itemsWithStatus(taskledger.StatusBlocked); len(blocked) > 0 {
		return taskLedgerBlockedItemsAllowTerminal(reply, blocked)
	}

	return true
}

func (s *taskLedgerState) itemsWithStatus(status taskledger.Status) []workspace.TaskLedgerItem {
	if s == nil {
		return nil
	}
	var items []workspace.TaskLedgerItem
	for _, item := range s.items {
		if item.Status == status {
			items = append(items, item)
		}
	}
	return items
}

func (s *taskLedgerState) openItems() []workspace.TaskLedgerItem {
	if s == nil {
		return nil
	}
	var items []workspace.TaskLedgerItem
	for _, item := range s.items {
		if item.Status.Open() {
			items = append(items, item)
		}
	}
	return items
}

func taskLedgerExplicitBlockerAnswer(reply string) bool {
	return questionProtocolAnswer(reply)
}

func questionProtocolAnswer(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	const marker = "<<QUESTION>>"
	if !strings.HasPrefix(trimmed, marker) {
		return false
	}

	remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	if !strings.HasPrefix(remainder, ":") {
		return false
	}

	return strings.TrimSpace(strings.TrimPrefix(remainder, ":")) != ""
}

func taskLedgerBlockedItemsAllowTerminal(reply string, items []workspace.TaskLedgerItem) bool {
	return taskLedgerExplicitBlockerAnswer(reply) && len(items) > 0
}

func taskLedgerPlanningExitAnswer(reply string, brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily &&
		strings.TrimSpace(brief.Contract) != workspace.ToolNameSubmitPlan {
		return false
	}

	return ContainsAny(strings.ToLower(strings.TrimSpace(reply)),
		"plan ready",
		"plan complete",
		"planning only",
		"awaiting approval",
	)
}

func (r *runExecution) fillSourceSanityPathFromSingleChangedSource(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, bool) {
	if r == nil {
		return workspace.ToolRequest{}, false
	}
	if strings.TrimSpace(req.Tool) != workspace.ToolNameSourceSanity || strings.TrimSpace(req.Path) != "" {
		return workspace.ToolRequest{}, false
	}

	if !slices.Contains(allowedTools, workspace.ToolNameSourceSanity) {
		return workspace.ToolRequest{}, false
	}

	ledger := r.semanticVerificationLedger(results)
	if len(ledger.PendingPaths) != 1 {
		return workspace.ToolRequest{}, false
	}

	redirected := req

	redirected.Path = ledger.PendingPaths[0]

	return redirected, true
}

func sourceEditsNeedSemanticVerification(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return semanticVerificationLedgerForReceipts(brief, results, nil).Pending()
}

func (r *runExecution) sourceEditsNeedSemanticVerification(results []workspace.ToolResult) bool {
	return r.semanticVerificationLedger(results).Pending()
}

type semanticVerificationLedger struct {
	PendingPaths []string
}

func (ledger semanticVerificationLedger) Pending() bool {
	return len(ledger.PendingPaths) > 0
}

func (r *runExecution) semanticVerificationLedger(results []workspace.ToolResult) semanticVerificationLedger {
	if r == nil {
		return semanticVerificationLedger{}
	}
	return semanticVerificationLedgerForReceipts(r.state.runtimeBrief, results, r.state.mutation.commitReceipts)
}

func semanticVerificationLedgerForReceipts(
	brief workerruntime.WorkerRuntimeBrief,
	results []workspace.ToolResult,
	receipts []workspace.MutationCommitProjection,
) semanticVerificationLedger {
	if !runnerBriefRequiresMutation(brief) || workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return semanticVerificationLedger{}
	}

	pending := committedSourcePostimageIDs(receipts)
	for _, result := range results {
		if receipt := result.CommitReceipt(); receipt != nil {
			pending = textutil.AppendUniqueMulti(pending, committedSourcePostimageIDs([]workspace.MutationCommitProjection{receipt.DurableProjection()})...)
		}
		if len(pending) == 0 || strings.TrimSpace(result.Error) != "" {
			continue
		}
		verification := workspace.AnalyzePostWriteVerificationResult(result, pending)
		if verification.Successful && !verification.PatchHygieneOnly {
			pending = completionGateRemainingPaths(pending, verification.CoveredPendingPaths)
		}
	}
	return semanticVerificationLedger{PendingPaths: uniqueSortedRunnerPaths(pending)}
}

func committedSourcePostimageIDs(receipts []workspace.MutationCommitProjection) []string {
	var paths []string
	for _, receipt := range receipts {
		for _, path := range receipt.PostimageFileIDs {
			if sourcekind.CoversSourceSanity(path) {
				paths = textutil.AppendUnique(paths, path)
			}
		}
	}
	return paths
}

func sourceEditsAreSemanticallyVerified(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !sourceVerificationGateApplies(brief, results) {
		return false
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)

	return ok && !workspace.AnalyzeVerificationResult(latest).PatchHygieneOnly
}

func (r *runExecution) sourceEditsAreSemanticallyVerified(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) || workerruntime.VerificationContractSkipsGate(r.state.runtimeBrief.VerificationContract) {
		return false
	}
	latest, ok := r.latestVerificationAfterLatestSourceWrite(results)
	return ok && !workspace.AnalyzeVerificationResult(latest).PatchHygieneOnly
}

func failedVerificationAfterLatestWrite(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolResult, bool) {
	ledger := postWriteVerificationLedgerForResults(results, brief)
	if !ledger.Pending() || !ledger.HasFailure {
		return workspace.ToolResult{}, false
	}

	return ledger.LatestFailed, true
}

func (r *runExecution) failedVerificationAfterLatestWrite(results []workspace.ToolResult) (workspace.ToolResult, bool) {
	ledger := r.postWriteVerificationLedger(results)
	if !ledger.Pending() || !ledger.HasFailure {
		return workspace.ToolResult{}, false
	}
	return ledger.LatestFailed, true
}

func sourceVerificationGateApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !runnerBriefRequiresMutation(brief) || workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	for _, result := range results {
		if receipt := result.CommitReceipt(); receipt != nil && len(committedSourcePostimageIDs([]workspace.MutationCommitProjection{receipt.DurableProjection()})) > 0 {
			return true
		}
	}
	return false
}

func latestVerificationAfterLatestSourceWrite(results []workspace.ToolResult) (workspace.ToolResult, bool) {
	return latestVerificationAfterLatestSourceWriteForReceipts(results, nil)
}

func (r *runExecution) latestVerificationAfterLatestSourceWrite(results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	return latestVerificationAfterLatestSourceWriteForReceipts(results, r.state.mutation.commitReceipts)
}

func latestVerificationAfterLatestSourceWriteForReceipts(
	results []workspace.ToolResult,
	receipts []workspace.MutationCommitProjection,
) (workspace.ToolResult, bool) {
	var latest workspace.ToolResult

	found := false
	seenSourceWrite := len(committedSourcePostimageIDs(receipts)) > 0

	for _, result := range results {
		if receipt := result.CommitReceipt(); receipt != nil && len(committedSourcePostimageIDs([]workspace.MutationCommitProjection{receipt.DurableProjection()})) > 0 {
			seenSourceWrite = true
			found = false
			latest = workspace.ToolResult{}

			continue
		}

		if strings.TrimSpace(result.Error) != "" {
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
	if strings.TrimSpace(req.Tool) != workspace.ToolNameShell || !plannerOwnsVerification(brief) {
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

func guardedImplementationShellRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameShell || !runnerBriefRequiresMutation(brief) {
		return "", "", false
	}

	analysis := workspace.AnalyzeShellCommand(req.Command)
	if analysis.SideEffect == autonomy.SideEffectReadOnly || analysis.ExploreAllowed || workspace.AnalyzeVerificationRequest(req).Attempted {
		return "", "", false
	}

	return "Mutation-capable shell rejected",
		"implementation workspace mutations must use typed write tools so path scope, authorization, preimages, commit receipts, and post-write verification remain enforceable. This shell command is not producer-classified as read-only or verification. Use an authorized typed write tool, a producer-classified read-only/verification command, or report the exact blocker.",
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

func clearRunnerStringIntMap(values map[string]int) {
	for key := range values {
		delete(values, key)
	}
}

type runnerVerificationBootstrapInput struct {
	collectedResults []workspace.ToolResult
	metrics          *runnerMetrics
	recorder         *runnerTurnRecorder
	executionState   *runnerExecutionState
	requestBudget    *runnerRequestBudgetState
	autonomyRecorder *runnerAutonomyRecorder
	onToolRequest    func(ToolRequestEvent)
}

func (r *runExecution) runVerificationBootstrap(input runnerVerificationBootstrapInput) (string, bool) {
	if r.runner.Workspace == nil || len(input.collectedResults) != 0 {
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
			Mode:           r.runner.Workspace.Mode(),
			Broad:          workspace.IsBroadInspectionRequest(req),
			Narrowing:      workspace.IsNarrowingRequest(req),
			Action:         toolActionClass(req),
			DiscoveryClass: profile.Class,
			DiscoveryScope: profile.Scope,
			DiscoveryCost:  profile.Cost,
		})
	}

	result := r.runner.Workspace.Execute(r.state.ctx, req)
	if input.recorder != nil {
		input.recorder.recordWithOptions(1, req, result, "verification_auto_bootstrap", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
	}
	if input.executionState != nil && input.requestBudget != nil {
		update := input.executionState.applyResult(req, result, toolRequestKey(req), r.runner.Workspace, input.requestBudget)
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

func permissionPolicyDecision(manager *workspace.Manager, policy toolpolicy.Policy, req workspace.ToolRequest) PermissionDecisionSnapshot {
	decision := manager.EvaluatePermissionPolicy(policy, req)
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
	return workspace.ToolPolicyInput(req).PathTarget
}

func (s PermissionDecisionSnapshot) ToolResult() workspace.ToolResult {
	target := strings.TrimSpace(s.Target)
	paths := []string(nil)
	if target != "" {
		paths = []string{target}
	}
	switch s.Decision {
	case toolpolicy.Deny:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Code:    "permission_denied",
			Summary: "Tool permission denied",
			Error:   fmt.Sprintf("permission policy denied %s on %s", s.Tool, textutil.FirstNonEmpty(s.Target, "this target")),
			Paths:   paths,
		}
	case toolpolicy.Ask:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Code:    "approval_required",
			Summary: "Tool approval required",
			Error:   fmt.Sprintf("permission policy requires approval for %s on %s", s.Tool, textutil.FirstNonEmpty(s.Target, "this target")),
			Paths:   paths,
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
