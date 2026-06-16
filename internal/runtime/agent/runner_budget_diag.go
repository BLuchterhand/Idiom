package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
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


// emitBriefAudit emits a one-shot brief_audit event at turn start so an
// audit can see the brief shape that drove the lane (constraints,
// snippets, packet size, readiness) without re-deriving from the prompt.
func (r *Runner) emitBriefAudit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}

	allowedToolsCount := len(brief.AllowedTools)
	if surfaceTools := DirectAnswerSurfaceForBriefTools(brief); surfaceTools != nil {
		allowedToolsCount = len(surfaceTools)
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
		AllowedToolsCount:         allowedToolsCount,
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
