package agent

import (
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// briefAuditEvent is the structured payload for the per-turn `brief_audit`
// event. The control_rails event already shows the brief's outward-facing
// shape (lane, allowed tools, search_policy). brief_audit fills in the
// fields control_rails omits — Constraints, RequiredEditSnippets,
// TargetPacket size, ChangedPaths, and the full Readiness block — so a
// post-hoc audit can answer "what evidence and contract did the model
// actually receive?" without re-deriving from the prompt text.
type briefAuditEvent struct {
	Lane                         string   `json:"lane,omitempty"`
	TaskFamily                   string   `json:"task_family,omitempty"`
	Contract                     string   `json:"contract,omitempty"`
	PermissionProfile            string   `json:"permission_profile,omitempty"`
	Constraints                  []string `json:"constraints,omitempty"`
	RequiredEditPaths            []string `json:"required_edit_paths,omitempty"`
	RequiredEditSnippetsCount    int      `json:"required_edit_snippets_count"`
	RequiredEditSnippetPaths     []string `json:"required_edit_snippet_paths,omitempty"`
	EvidencePaths                []string `json:"evidence_paths,omitempty"`
	ChangedPaths                 []string `json:"changed_paths,omitempty"`
	TargetPacketLines            int      `json:"target_packet_lines"`
	TargetPacketBytes            int      `json:"target_packet_bytes"`
	ReadinessReadyToAnswer       bool     `json:"readiness_ready_to_answer"`
	ReadinessReadyToWrite        bool     `json:"readiness_ready_to_write"`
	ReadinessReadyToVerify       bool     `json:"readiness_ready_to_verify"`
	ReadinessClosedClaims        []string `json:"readiness_closed_claims,omitempty"`
	ReadinessOpenObligations     []string `json:"readiness_open_obligations,omitempty"`
	ReadinessFurtherReadsRequire string   `json:"readiness_further_reads_require,omitempty"`
	ExactAnchorImplActive        bool     `json:"exact_anchor_implementation_active"`
	TargetPacketSupportsWrite    bool     `json:"target_packet_supports_write"`
	WriteReadyReadContract       bool     `json:"write_ready_read_contract"`
	AllowedToolsCount            int      `json:"allowed_tools_count"`
}

// readBudgetDiagEvent is emitted for every read-class request after the
// runner picks the read budget for that call. It explains which clamp
// branch in readBudgetPolicyForCurrentState produced the budget and which
// brief signals drove that branch — so a session log can answer "why was
// the budget that low for this read?" without rereading the source.
type readBudgetDiagEvent struct {
	Step                      int    `json:"step"`
	Tool                      string `json:"tool"`
	Path                      string `json:"path,omitempty"`
	EffectiveReadBudget       int    `json:"effective_read_budget"`
	ClampReason               string `json:"clamp_reason"`
	ExactAnchorActive         bool   `json:"exact_anchor_active"`
	TargetPacketSupportsWrite bool   `json:"target_packet_supports_write"`
	WriteReadyReadContract    bool   `json:"write_ready_read_contract"`
	RequiredEditSnippetsCount int    `json:"required_edit_snippets_count"`
	RequiredEditPathsCount    int    `json:"required_edit_paths_count"`
	ChangedPathsCount         int    `json:"changed_paths_count"`
	ReadCallsSinceWrite       int    `json:"read_calls_since_write"`
	ReadCallCount             int    `json:"read_call_count"`
}

// readBudgetHardBlockEvent is the structured payload that replaces the
// existing free-text content for `read_budget_hard_block`. It pairs the
// model's stated reason for reading (named_gap, why_not_answering) with
// the runner's view of the contract (remaining required edit paths,
// whether the requested path is one of them) so an audit can correlate
// "what the model claimed it needed" with "what the brief already had".
type readBudgetHardBlockEvent struct {
	Tool                       string   `json:"tool"`
	Path                       string   `json:"path,omitempty"`
	StartLine                  int      `json:"start_line,omitempty"`
	EndLine                    int      `json:"end_line,omitempty"`
	NamedGap                   string   `json:"named_gap,omitempty"`
	WhyNotAnswering            string   `json:"why_not_answering,omitempty"`
	EffectiveReadBudget        int      `json:"effective_read_budget"`
	ReadCallsSinceWrite        int      `json:"read_calls_since_write"`
	ReadCallCount              int      `json:"read_call_count"`
	RequiredEditPaths          []string `json:"required_edit_paths,omitempty"`
	RemainingRequiredEditPaths []string `json:"remaining_required_edit_paths,omitempty"`
	RequestPathInRequiredPaths bool     `json:"request_path_in_required_paths"`
	Message                    string   `json:"message"`
}

// emitBriefAudit emits a one-shot brief_audit event at turn start so an
// audit can see the brief shape that drove the lane (constraints,
// snippets, packet size, readiness) without re-deriving from the prompt.
func (r *Runner) emitBriefAudit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	event := briefAuditEvent{
		Lane:                      strings.TrimSpace(brief.Lane),
		TaskFamily:                strings.TrimSpace(brief.TaskFamily),
		Contract:                  strings.TrimSpace(brief.Contract),
		PermissionProfile:         strings.TrimSpace(brief.PermissionProfile),
		Constraints:               append([]string(nil), brief.Constraints...),
		RequiredEditPaths:         append([]string(nil), brief.RequiredEditPaths...),
		RequiredEditSnippetsCount: len(brief.RequiredEditSnippets),
		EvidencePaths:             append([]string(nil), brief.EvidencePaths...),
		ChangedPaths:              append([]string(nil), brief.ChangedPaths...),
		TargetPacketLines:         len(brief.TargetPacket),
		TargetPacketBytes:         targetPacketByteCount(brief.TargetPacket),
		ExactAnchorImplActive:     exactAnchorImplementationActive(brief),
		TargetPacketSupportsWrite: targetPacketSupportsWrite(brief),
		WriteReadyReadContract:    writeReadyPacketReadContractActive(brief, results),
		AllowedToolsCount:         len(brief.AllowedTools),
	}
	if len(brief.RequiredEditSnippets) > 0 {
		event.RequiredEditSnippetPaths = make([]string, 0, len(brief.RequiredEditSnippets))
		seen := make(map[string]struct{}, len(brief.RequiredEditSnippets))
		for _, snippet := range brief.RequiredEditSnippets {
			path := strings.TrimSpace(snippet.Path)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			event.RequiredEditSnippetPaths = append(event.RequiredEditSnippetPaths, path)
		}
	}
	if brief.Readiness != nil {
		event.ReadinessReadyToAnswer = brief.Readiness.ReadyToAnswer
		event.ReadinessReadyToWrite = brief.Readiness.ReadyToWrite
		event.ReadinessReadyToVerify = brief.Readiness.ReadyToVerify
		event.ReadinessClosedClaims = append([]string(nil), brief.Readiness.ClosedClaims...)
		event.ReadinessOpenObligations = append([]string(nil), brief.Readiness.OpenObligations...)
		event.ReadinessFurtherReadsRequire = strings.TrimSpace(brief.Readiness.FurtherReadsRequire)
	}
	r.emitEvent(marshalRunEvent("brief_audit", event))
}

// emitReadBudgetDiag emits a per-call diagnostic explaining the read
// budget the runner just chose for this read-class request. It pairs the
// effective budget with the predicates that drove the clamp branch so an
// audit can answer "why was this read rejected after only N calls?".
func (r *Runner) emitReadBudgetDiag(stepNumber int, req workspace.ToolRequest, policy RunPolicy, results []workspace.ToolResult, requestBudget runnerRequestBudgetState) {
	if r == nil || r.OnEvent == nil {
		return
	}
	changed := changedPathsFromToolResults(results)
	event := readBudgetDiagEvent{
		Step:                      stepNumber,
		Tool:                      strings.TrimSpace(req.Tool),
		Path:                      strings.TrimSpace(req.Path),
		EffectiveReadBudget:       policy.ReadBudgetHard,
		ClampReason:               readBudgetClampReason(r.RuntimeBrief, results),
		ExactAnchorActive:         exactAnchorImplementationActive(r.RuntimeBrief),
		TargetPacketSupportsWrite: targetPacketSupportsWrite(r.RuntimeBrief),
		WriteReadyReadContract:    writeReadyPacketReadContractActive(r.RuntimeBrief, results),
		RequiredEditSnippetsCount: len(r.RuntimeBrief.RequiredEditSnippets),
		RequiredEditPathsCount:    len(r.RuntimeBrief.RequiredEditPaths),
		ChangedPathsCount:         len(changed),
		ReadCallsSinceWrite:       requestBudget.readCallsSinceWrite,
		ReadCallCount:             requestBudget.readCallCount,
	}
	r.emitEvent(marshalRunEvent("read_budget_diag", event))
}

// readBudgetHardBlockPayload returns a JSON-encoded payload for the
// read_budget_hard_block event, replacing the existing free-text content
// with a structured record of the failing request and the contract state
// at the moment of rejection. Returns the empty string when r is nil so
// the caller can fall back to the original string content.
func (r *Runner) readBudgetHardBlockPayload(req workspace.ToolRequest, results []workspace.ToolResult, policy RunPolicy, requestBudget runnerRequestBudgetState, message string) string {
	if r == nil {
		return ""
	}
	required := append([]string(nil), r.RuntimeBrief.RequiredEditPaths...)
	remaining := r.remainingRequiredEditPaths(results)
	event := readBudgetHardBlockEvent{
		Tool:                       strings.TrimSpace(req.Tool),
		Path:                       strings.TrimSpace(req.Path),
		StartLine:                  req.StartLine,
		EndLine:                    req.EndLine,
		NamedGap:                   strings.TrimSpace(req.NamedGap),
		WhyNotAnswering:            strings.TrimSpace(req.WhyNotAnswering),
		EffectiveReadBudget:        policy.ReadBudgetHard,
		ReadCallsSinceWrite:        requestBudget.readCallsSinceWrite,
		ReadCallCount:              requestBudget.readCallCount,
		RequiredEditPaths:          required,
		RemainingRequiredEditPaths: append([]string(nil), remaining...),
		RequestPathInRequiredPaths: requestPathInRequiredEditPaths(req.Path, required),
		Message:                    strings.TrimSpace(message),
	}
	rendered := marshalRunEvent("read_budget_hard_block", event)
	return rendered.Content
}

// readBudgetClampReason mirrors the branching in
// readBudgetPolicyForCurrentState so the diag event can label which
// clamp produced the budget. Kept as a separate function rather than
// returned alongside the policy so the read path is unchanged for
// non-debug call sites.
func readBudgetClampReason(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return "modeDefault"
	}
	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		if implementationPreWriteToolSurfaceActive(brief, results) {
			return "writeReadyPacketPrewrite"
		}
		if briefHasUsableEditAnchors(brief) {
			return "requiredEditPathPrewrite"
		}
		return "abstractImplementationDefault"
	}
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) > 0 {
		if exactAnchorImplementationActive(brief) {
			return "requiredEditPathRemainder"
		}
		return "abstractImplementationRemainder"
	}
	return "mutationDefault"
}

func targetPacketByteCount(packet []string) int {
	total := 0
	for _, line := range packet {
		total += len(line)
		total++
	}
	if total > 0 {
		total--
	}
	return total
}

func requestPathInRequiredEditPaths(path string, required []string) bool {
	cleaned := normalizeRunnerPath(path)
	if cleaned == "" {
		return false
	}
	for _, p := range required {
		if normalizeRunnerPath(p) == cleaned {
			return true
		}
	}
	return false
}
