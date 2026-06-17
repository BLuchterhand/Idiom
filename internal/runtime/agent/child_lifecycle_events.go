package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"

	"idiom/internal/runtime/agent/agentutil"
)

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
	return r.beginChildLifecycle("child_read_only:"+agentName, "child_read_only", autonomy.ChildWorkspaceNoWrite, opts, policy, false, r.childLifecyclePrimaryClient())
}

func (r *Runner) beginWorkbranchChildLifecycle(agentName string, opts llama.ChatOptions, policy RunPolicy, client llama.LLMClient) childLifecycleContext {
	return r.beginChildLifecycle("child_write:"+agentName, "child_write", autonomy.ChildWorkspaceWorkbranch, opts, policy, false, client)
}

func (r *Runner) beginDelegateEditLifecycle(req workspace.ToolRequest, opts llama.ChatOptions, deterministic bool, client llama.LLMClient) childLifecycleContext {
	role := "executor:" + util.FirstNonEmpty(strings.TrimSpace(req.Agent), "delegate_edit")
	return r.beginChildLifecycle(role, "delegate_edit", autonomy.ChildWorkspaceMain, opts, RunPolicy{}, deterministic, client)
}

func (r *Runner) beginChildLifecycle(role string, phase string, mode autonomy.ChildWorkspaceMode, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) childLifecycleContext {
	now := time.Now().UTC()
	lane := util.FirstNonEmpty(r.runtimeChildLifecycleLane(), "runner")
	phase = util.FirstNonEmpty(phase, r.runtimeChildLifecyclePhase(), "child")
	role = util.FirstNonEmpty(role, "child")
	parentContractID := r.childLifecycleParentContractID()
	childContractID := r.childLifecycleChildContractID(parentContractID, lane, phase, role)
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

func (r *Runner) emitWorkbranchChildTerminal(ctx childLifecycleContext, checkout WorkbranchCheckout, result workspace.ToolResult, diff WorkbranchDiff, status autonomy.ChildTerminalStatus) {
	patchID := childLifecycleID("candidate_patch", ctx.InvocationID, checkout.BaseSHA, strings.Join(diff.ChangedPaths, ","), childLifecycleHash(diff.Patch))
	var artifactRefs []string
	resultKind := autonomy.ChildResultCandidatePatch
	if len(diff.ChangedPaths) > 0 {
		artifactRefs = []string{"patch:" + patchID}
		artifact := autonomy.CandidatePatchArtifact{
			PatchID:         patchID,
			InvocationID:    ctx.InvocationID,
			ChildContractID: ctx.ChildContractID,
			BaseSHA:         util.FirstNonEmpty(checkout.BaseSHA, "unknown"),
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
	changedPaths := changedPathsFromToolResult(result)
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
		ReasonCode:                 util.FirstNonEmpty(reasonCode, "delegate_edit_recorded"),
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

func childLifecycleDiffStat(diff WorkbranchDiff) autonomy.PatchDiffStat {
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
			reasonCode = util.FirstNonEmpty(reasonCode, "accepted_refs_missing")
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
		ReasonCode:           util.FirstNonEmpty(reasonCode, "child_terminal_recorded"),
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
		sourceRefs = append(sourceRefs, "changed_path:"+agentutil.NormalizeRunnerPath(path))
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

func (r *Runner) childLifecycleChildContractID(parentContractID string, lane string, phase string, role string) string {
	input := autonomy.ContractInput{
		ParentID: parentContractID,
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationChild,
			Lane:              lane,
			Phase:             phase,
			TaskFamily:        util.FirstNonEmpty(r.runtimeChildLifecycleTaskFamily(), phase),
			RuntimeContract:   r.runtimeChildLifecycleContract(),
			PermissionProfile: r.runtimeChildLifecyclePermissionProfile(),
		},
		Metadata: autonomy.DefaultContractMetadata("child_lifecycle", role),
	}
	if contract, err := autonomy.NewContract(input); err == nil && strings.TrimSpace(contract.ContractID) != "" {
		return contract.ContractID
	}

	return childLifecycleID("child_contract", parentContractID, lane, phase, role)
}

func (r *Runner) childRuntimeEnvelope(role string, opts llama.ChatOptions, policy RunPolicy, deterministic bool, client llama.LLMClient) autonomy.ChildRuntimeEnvelope {
	modelAlias := util.FirstNonEmpty(safeModelAliasForTelemetry(opts.Model), "default")
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
		ModelRole:   util.FirstNonEmpty(role, "child"),
		RoleResolution: autonomy.ChildRoleResolution{
			RequestedRole: util.FirstNonEmpty(role, "child"),
			ResolvedRole:  util.FirstNonEmpty(role, "child"),
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

func (r *Runner) childLifecyclePrimaryClient() llama.LLMClient {
	if r == nil {
		return nil
	}
	return r.Client
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
		tool := util.FirstNonEmpty(strings.TrimSpace(result.Tool), "tool")
		refs = append(refs, fmt.Sprintf("tool_result:%s:%d", tool, i+1))
	}
	return refs
}

func childLifecycleObjectiveAlignmentForResult(result workspace.ToolResult) autonomy.ChildObjectiveAlignment {
	if strings.TrimSpace(result.Error) != "" {
		return autonomy.ChildObjectiveWeak
	}
	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		if len(packet.Gaps) > 0 {
			return autonomy.ChildObjectiveWeak
		}
		if !readOnlyEvidencePacketAnswerable(packet) {
			return autonomy.ChildObjectiveWeak
		}
		if agentutil.ObjectiveAsksImplementationShape(packet.Objective) && !readOnlyEvidencePacketAnswerableForObjective(packet, packet.Objective) {
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
	if _, ok := readOnlyEvidencePacketFromResult(result); !ok {
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
	return util.FirstNonEmpty("tool_request:"+strings.TrimSpace(req.Tool)+":"+agentutil.NormalizeRunnerPath(req.Path), "tool_request:delegate_edit")
}

func childLifecyclePathGateRef(req workspace.ToolRequest) string {
	return util.FirstNonEmpty("path_gate:"+agentutil.NormalizeRunnerPath(req.Path), "path_gate:delegate_edit")
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

func (r *Runner) runtimeChildLifecycleLane() string {
	if r == nil {
		return ""
	}
	return r.RuntimeBrief.Lane
}

func (r *Runner) runtimeChildLifecyclePhase() string {
	if r == nil {
		return ""
	}
	return util.FirstNonEmpty(r.RuntimeBrief.TaskFamily, r.RuntimeBrief.Contract)
}

func (r *Runner) runtimeChildLifecycleTaskFamily() string {
	if r == nil {
		return ""
	}
	return r.RuntimeBrief.TaskFamily
}

func (r *Runner) runtimeChildLifecycleContract() string {
	if r == nil {
		return ""
	}
	return r.RuntimeBrief.Contract
}

func (r *Runner) runtimeChildLifecyclePermissionProfile() string {
	if r == nil {
		return ""
	}
	return r.RuntimeBrief.PermissionProfile
}


func childLifecycleID(prefix string, parts ...string) string {
	return prefix + "_" + childLifecycleHash(parts...)
}

func childLifecycleHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}
