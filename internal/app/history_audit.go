package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

type historyAuditMsg struct {
	event historyAuditEvent
}

func (m *Model) enqueueHistoryAuditEvent(event historyAuditEvent) {
	event = cleanHistoryAuditEvent(event)
	if event.label == "" {
		return
	}
	if m.events == nil {
		m.recordHistoryAuditEvent(event)
		return
	}
	select {
	case m.events <- historyAuditMsg{event: event}:
	default:
	}
}

func (m *Model) historyPrivacyAuditEvent() historyAuditEvent {
	if m == nil || m.debugLogger == nil {
		return historyAuditEvent{}
	}
	mode := strings.TrimSpace(m.cfg.Chat.DebugLogMode)
	if mode == "" {
		mode = "redacted"
	}
	if !strings.EqualFold(mode, "redacted") {
		return historyAuditEvent{}
	}
	return historyAuditEvent{
		kind:   "privacy",
		label:  "debug log redaction active",
		detail: "raw prompts, shell commands, and large payloads are sanitized in persisted logs",
		status: "active",
	}
}

func historyToolAuditKind(event agent.ToolRequestEvent) string {
	req := event.Request
	tool := strings.TrimSpace(req.Tool)
	profile := workspace.DiscoveryProfileForRequest(req)
	if event.DiscoveryClass != "" {
		profile.Class = event.DiscoveryClass
	}
	if event.DiscoveryScope != "" {
		profile.Scope = event.DiscoveryScope
	}
	if event.DiscoveryCost != "" {
		profile.Cost = event.DiscoveryCost
	}
	if profile.Discovery {
		parts := []string{"discovery"}
		if strings.TrimSpace(profile.Class) != "" {
			parts = append(parts, profile.Class)
		}
		if strings.TrimSpace(profile.Scope) != "" {
			parts = append(parts, profile.Scope)
		}
		if strings.TrimSpace(profile.Cost) != "" {
			parts = append(parts, profile.Cost)
		}
		return strings.Join(parts, " / ")
	}
	switch {
	case workspace.IsWorkspaceWriteTool(tool):
		return "write"
	case tool == "submit_plan":
		return "contract"
	case tool == "task_ledger":
		return "plan"
	case tool == "task" || tool == "child_task":
		return "delegation"
	case tool == "shell":
		if historyShellCommandLooksLikeVerification(req.Command) {
			return "verification"
		}
		return "shell"
	case strings.HasPrefix(tool, "context_"):
		return "context"
	case strings.Contains(tool, "read") || strings.Contains(tool, "outline") || tool == "file_stats":
		return "read"
	default:
		return "tool"
	}
}

func historyToolAuditOutcome(event agent.ToolResultEvent) string {
	result := event.Result
	if strings.TrimSpace(result.Error) != "" {
		return "failed: " + truncateRunes(collapseWhitespace(result.Error), 120)
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "completed"
	}
	chars := len(result.Output)
	lines := 0
	if chars > 0 {
		lines = strings.Count(result.Output, "\n")
		if !strings.HasSuffix(result.Output, "\n") {
			lines++
		}
	}
	if lines > 0 {
		return fmt.Sprintf("ok: %s (%d line(s), %d char(s))", truncateRunes(collapseWhitespace(summary), 100), lines, chars)
	}
	return "ok: " + truncateRunes(collapseWhitespace(summary), 120)
}

func historyAuditEventsForToolResult(event agent.ToolResultEvent) []historyAuditEvent {
	fields := workspace.ToolTelemetryFields(event.Result.Tool, event.Result.Output)
	if len(fields) == 0 {
		return nil
	}
	events := make([]historyAuditEvent, 0, 3)
	if state := stringMapValue(fields, "context_cache_state"); state != "" {
		detail := joinNonEmpty("; ",
			keyValue("source", stringMapValue(fields, "context_cache_source")),
			keyValue("reason", stringMapValue(fields, "context_cache_reason")),
			keyValue("pack", stringMapValue(fields, "context_cache_pack_id")),
		)
		events = append(events, historyAuditEvent{kind: "cache", label: "context cache " + state, detail: detail, status: state})
	}
	if refreshAttempted := boolMapValue(fields, "context_auto_refresh_attempted") || boolMapValue(fields, "context_freshness_refresh_attempted"); refreshAttempted {
		files := intMapValue(fields, "context_auto_refresh_files_indexed")
		if files == 0 {
			files = intMapValue(fields, "context_freshness_refresh_files_indexed")
		}
		reason := util.FirstNonEmpty(stringMapValue(fields, "context_auto_refresh_reason"), stringMapValue(fields, "context_freshness_refresh_reason"))
		label := "context freshness refresh"
		if files > 0 {
			label = fmt.Sprintf("context freshness refresh indexed %d file(s)", files)
		}
		events = append(events, historyAuditEvent{kind: "cache", label: label, detail: keyValue("reason", reason), status: "refresh"})
	}
	if state := stringMapValue(fields, "context_freshness_state"); state != "" {
		detail := joinNonEmpty("; ",
			keyValue("policy", stringMapValue(fields, "context_freshness_policy")),
			keyValue("reason", stringMapValue(fields, "context_freshness_reason")),
		)
		events = append(events, historyAuditEvent{kind: "freshness", label: "context evidence " + state, detail: detail, status: state})
	}
	return events
}

func historyAuditEventFromRunEvent(event agent.RunEvent) (historyAuditEvent, bool) {
	role := strings.TrimSpace(event.Role)
	if role == "" {
		return historyAuditEvent{}, false
	}
	switch role {
	case "model_call":
		return historyAuditModelCall(event.Content)
	case "phase":
		return historyAuditPhase(event.Content)
	case "completion_status":
		return historyAuditCompletionStatus(event.Content)
	case "budget_ledger":
		return historyAuditBudget(event.Content)
	case "permission_decision":
		return historyAuditPermissionDecision(event.Content)
	case "tool_request_rewrite":
		return historyAuditToolRewrite(event.Content)
	case "readiness_check":
		return historyAuditReadiness(event.Content)
	case "final_answer_critic", "control_rails_enforcement":
		return historyAuditGate(role, event.Content)
	case "post_readiness_gap_declared":
		return historyAuditPostReadinessGap(event.Content)
	case "context_compaction":
		return historyAuditJSONOrText("context", "context compacted", event.Content), true
	case "autonomy_event":
		return historyAuditAutonomyEvent(event.Content)
	case "child_tool_request":
		return historyAuditChildToolRequest(event.Content)
	case "child_tool_result":
		return historyAuditChildToolResult(event.Content)
	}
	if audit, ok := historyAuditTextEvent(role, event.Content); ok {
		return audit, true
	}
	return historyAuditEvent{}, false
}

func historyAuditChildToolRequest(content string) (historyAuditEvent, bool) {
	var payload agent.ToolRequestEvent
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	tool := strings.TrimSpace(payload.Request.Tool)
	if tool == "" {
		return historyAuditEvent{}, false
	}
	label := "child tool requested " + tool
	if payload.Agent != "" {
		label = payload.Agent + " requested " + tool
	}
	detail := joinNonEmpty("; ", keyValue("type", historyToolAuditKind(payload)), keyValue("reason", toolRequestIntent(payload)))
	return historyAuditEvent{kind: "delegation", label: label, detail: detail, status: "requested", step: payload.Step}, true
}

func historyAuditChildToolResult(content string) (historyAuditEvent, bool) {
	var payload agent.ToolResultEvent
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	tool := strings.TrimSpace(util.FirstNonEmpty(payload.Result.Tool, payload.Request.Tool))
	if tool == "" {
		return historyAuditEvent{}, false
	}
	label := "child tool completed " + tool
	if payload.Agent != "" {
		label = payload.Agent + " completed " + tool
	}
	status := "ok"
	if strings.TrimSpace(payload.Result.Error) != "" {
		status = "failed"
	}
	return historyAuditEvent{kind: "delegation", label: label, detail: historyToolAuditOutcome(payload), status: status, step: payload.Step}, true
}

func historyAuditModelCall(content string) (historyAuditEvent, bool) {
	var payload struct {
		Step        int    `json:"step"`
		Agent       string `json:"agent"`
		Label       string `json:"label"`
		ChosenTool  string `json:"chosen_tool"`
		TotalTokens int    `json:"total_tokens"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	label := "model call"
	if payload.ChosenTool != "" {
		label = "model selected tool " + payload.ChosenTool
	}
	detail := joinNonEmpty("; ", keyValue("agent", util.FirstNonEmpty(payload.Agent, payload.Label)), intKeyValue("tokens", payload.TotalTokens))
	return historyAuditEvent{kind: "model", label: label, detail: detail, step: payload.Step}, true
}

func historyAuditPhase(content string) (historyAuditEvent, bool) {
	var payload struct {
		Lane   string `json:"lane"`
		Phase  string `json:"phase"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	label := joinNonEmpty(" ", payload.Lane, payload.Phase, payload.State)
	if label == "" {
		label = "phase changed"
	}
	return historyAuditEvent{kind: "lane", label: label, detail: payload.Detail, status: payload.State}, true
}

func historyAuditCompletionStatus(content string) (historyAuditEvent, bool) {
	var payload struct {
		Lane             string `json:"lane"`
		Contract         string `json:"contract"`
		Status           string `json:"status"`
		BlockerKind      string `json:"blocker_kind"`
		StopReason       string `json:"stop_reason"`
		EditAttempted    bool   `json:"edit_attempted"`
		VerificationSeen bool   `json:"verification_seen"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	status := util.FirstNonEmpty(payload.Status, "updated")
	label := "contract " + status
	if payload.Contract != "" {
		label += ": " + payload.Contract
	}
	detail := joinNonEmpty("; ",
		keyValue("lane", payload.Lane),
		keyValue("blocker", payload.BlockerKind),
		keyValue("stop", payload.StopReason),
		boolKeyValue("edit_attempted", payload.EditAttempted),
		boolKeyValue("verification_seen", payload.VerificationSeen),
	)
	return historyAuditEvent{kind: "contract", label: label, detail: detail, status: status}, true
}

func historyAuditBudget(content string) (historyAuditEvent, bool) {
	var payload struct {
		Lane            string `json:"lane"`
		UsedModelCalls  int    `json:"used_model_calls"`
		MaxModelCalls   int    `json:"max_model_calls"`
		UsedToolResults int    `json:"used_tool_results"`
		MaxToolSteps    int    `json:"max_tool_steps"`
		StopReason      string `json:"stop_reason"`
		BudgetExhausted bool   `json:"budget_exhausted"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	label := fmt.Sprintf("budget used %d/%d tool step(s)", payload.UsedToolResults, payload.MaxToolSteps)
	detail := joinNonEmpty("; ",
		keyValue("lane", payload.Lane),
		fmt.Sprintf("model_calls=%d/%d", payload.UsedModelCalls, payload.MaxModelCalls),
		keyValue("stop", payload.StopReason),
		boolKeyValue("exhausted", payload.BudgetExhausted),
	)
	return historyAuditEvent{kind: "budget", label: label, detail: detail}, true
}

func historyAuditPermissionDecision(content string) (historyAuditEvent, bool) {
	var payload struct {
		Tool       string `json:"tool"`
		Decision   string `json:"decision"`
		ReasonCode string `json:"reason_code"`
		Source     string `json:"source"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	label := joinNonEmpty(" ", payload.Decision, payload.Tool)
	if label == "" {
		label = "permission decision"
	}
	return historyAuditEvent{kind: "permission", label: label, detail: joinNonEmpty("; ", keyValue("reason", payload.ReasonCode), keyValue("source", payload.Source)), status: payload.Decision}, true
}

func historyAuditToolRewrite(content string) (historyAuditEvent, bool) {
	var payload struct {
		OriginalTool  string `json:"original_tool"`
		RewrittenTool string `json:"rewritten_tool"`
		Agent         string `json:"agent"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	label := "tool redirected"
	if payload.OriginalTool != "" || payload.RewrittenTool != "" {
		label = fmt.Sprintf("tool redirected %s -> %s", payload.OriginalTool, payload.RewrittenTool)
	}
	return historyAuditEvent{kind: "routing", label: label, detail: keyValue("agent", payload.Agent), status: "redirected"}, true
}

func historyAuditReadiness(content string) (historyAuditEvent, bool) {
	var payload struct {
		Lane          string   `json:"lane"`
		Ready         bool     `json:"ready"`
		Action        string   `json:"action"`
		Reason        string   `json:"reason"`
		Confidence    string   `json:"confidence"`
		EvidencePaths []string `json:"evidence_paths"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	status := "blocked"
	if payload.Ready {
		status = "satisfied"
	}
	label := "evidence gate " + status
	detail := joinNonEmpty("; ", keyValue("lane", payload.Lane), keyValue("confidence", payload.Confidence), keyValue("action", payload.Action), keyValue("reason", payload.Reason), countKeyValue("evidence_paths", len(payload.EvidencePaths)))
	return historyAuditEvent{kind: "evidence", label: label, detail: detail, status: status}, true
}

func historyAuditGate(role string, content string) (historyAuditEvent, bool) {
	var payload struct {
		Lane        string   `json:"lane"`
		Contract    string   `json:"contract"`
		Passed      bool     `json:"passed"`
		Action      string   `json:"action"`
		Reason      string   `json:"reason"`
		BlockerKind string   `json:"blocker_kind"`
		Evidence    []string `json:"evidence_paths"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	status := "blocked"
	if payload.Passed {
		status = "accepted"
	}
	kind := "contract"
	if role == "control_rails_enforcement" {
		kind = "control rails"
	}
	label := kind + " " + status
	detail := joinNonEmpty("; ", keyValue("lane", payload.Lane), keyValue("contract", payload.Contract), keyValue("action", payload.Action), keyValue("reason", payload.Reason), keyValue("blocker", payload.BlockerKind), countKeyValue("evidence_paths", len(payload.Evidence)))
	return historyAuditEvent{kind: kind, label: label, detail: detail, status: status}, true
}

func historyAuditPostReadinessGap(content string) (historyAuditEvent, bool) {
	var payload struct {
		NamedGap        string `json:"named_gap"`
		WhyNotAnswering string `json:"why_not_answering"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	return historyAuditEvent{kind: "evidence", label: "post-readiness gap declared", detail: joinNonEmpty("; ", keyValue("gap", payload.NamedGap), keyValue("why", payload.WhyNotAnswering)), status: "open"}, true
}

func historyAuditAutonomyEvent(content string) (historyAuditEvent, bool) {
	var payload struct {
		Type        string `json:"type"`
		ContractID  string `json:"contract_id"`
		Correlation struct {
			Lane            string `json:"lane"`
			Phase           string `json:"phase"`
			RuntimeContract string `json:"runtime_contract"`
			ToolName        string `json:"tool_name"`
		} `json:"correlation"`
		Policy *struct {
			ToolName   string `json:"tool_name"`
			Decision   string `json:"decision"`
			ReasonCode string `json:"reason_code"`
			Result     string `json:"result"`
		} `json:"policy"`
		Obligation *struct {
			ObligationID string `json:"obligation_id"`
			Kind         string `json:"kind"`
			Status       string `json:"status"`
			TargetRef    string `json:"target_ref"`
			Resolution   string `json:"resolution"`
		} `json:"obligation"`
		Readiness *struct {
			ReadyToAnswer       bool     `json:"ready_to_answer"`
			ReadyToWrite        bool     `json:"ready_to_write"`
			ReadyToVerify       bool     `json:"ready_to_verify"`
			OpenObligations     []string `json:"open_obligations"`
			FurtherReadsRequire string   `json:"further_reads_require"`
		} `json:"readiness"`
		Gate *struct {
			Gate            string   `json:"gate"`
			Decision        string   `json:"decision"`
			ReasonCode      string   `json:"reason_code"`
			OpenObligations []string `json:"open_obligations"`
		} `json:"gate"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return historyAuditEvent{}, false
	}
	switch payload.Type {
	case "contract_compiled", "contract_presented", "contract_recompiled", "contract_stopped":
		label := strings.ReplaceAll(payload.Type, "_", " ")
		detail := joinNonEmpty("; ", keyValue("lane", payload.Correlation.Lane), keyValue("phase", payload.Correlation.Phase), keyValue("contract", payload.Correlation.RuntimeContract), keyValue("id", payload.ContractID))
		return historyAuditEvent{kind: "contract", label: label, detail: detail}, true
	case "tool_policy_decision", "tool_policy_result":
		if payload.Policy == nil {
			return historyAuditEvent{}, false
		}
		tool := util.FirstNonEmpty(payload.Policy.ToolName, payload.Correlation.ToolName)
		label := joinNonEmpty(" ", "tool policy", payload.Policy.Decision, tool)
		detail := joinNonEmpty("; ", keyValue("reason", payload.Policy.ReasonCode), keyValue("result", payload.Policy.Result))
		return historyAuditEvent{kind: "policy", label: label, detail: detail, status: payload.Policy.Decision}, true
	case "obligation_opened", "obligation_resolved", "obligation_waived":
		if payload.Obligation == nil {
			return historyAuditEvent{}, false
		}
		label := strings.ReplaceAll(payload.Type, "_", " ") + ": " + util.FirstNonEmpty(payload.Obligation.Kind, payload.Obligation.ObligationID)
		detail := joinNonEmpty("; ", keyValue("target", payload.Obligation.TargetRef), keyValue("resolution", payload.Obligation.Resolution))
		return historyAuditEvent{kind: "obligation", label: label, detail: detail, status: payload.Obligation.Status}, true
	case "readiness_state_updated":
		if payload.Readiness == nil {
			return historyAuditEvent{}, false
		}
		label := "contract readiness updated"
		detail := joinNonEmpty("; ",
			boolKeyValue("answer", payload.Readiness.ReadyToAnswer),
			boolKeyValue("write", payload.Readiness.ReadyToWrite),
			boolKeyValue("verify", payload.Readiness.ReadyToVerify),
			countKeyValue("open_obligations", len(payload.Readiness.OpenObligations)),
			keyValue("further_reads_require", payload.Readiness.FurtherReadsRequire),
		)
		return historyAuditEvent{kind: "evidence", label: label, detail: detail}, true
	case "completion_gate_decision", "gate_decision":
		if payload.Gate == nil {
			return historyAuditEvent{}, false
		}
		label := joinNonEmpty(" ", payload.Gate.Gate, payload.Gate.Decision)
		detail := joinNonEmpty("; ", keyValue("reason", payload.Gate.ReasonCode), countKeyValue("open_obligations", len(payload.Gate.OpenObligations)))
		return historyAuditEvent{kind: "gate", label: label, detail: detail, status: payload.Gate.Decision}, true
	default:
		return historyAuditEvent{kind: "event", label: strings.ReplaceAll(payload.Type, "_", " "), detail: keyValue("contract", payload.ContractID)}, payload.Type != ""
	}
}

func historyAuditTextEvent(role string, content string) (historyAuditEvent, bool) {
	switch role {
	case "route_plan":
		detail := safeHistoryAuditDetail(content)
		return historyAuditEvent{kind: "lane", label: "route selected", detail: detail, status: auditDetailValue(detail, "initial")}, true
	case "control_rails":
		return historyAuditEvent{kind: "control rails", label: "runtime contract visible", detail: safeHistoryAuditDetail(content)}, true
	case "discovery_redirect":
		return historyAuditEvent{kind: "routing", label: "discovery redirected", detail: safeHistoryAuditDetail(content), status: "redirected"}, true
	case "discovery_decision_guide":
		return historyAuditEvent{kind: "discovery", label: "discovery guidance attached", detail: safeHistoryAuditDetail(content)}, true
	case "context_pack_freshness_recovery":
		return historyAuditEvent{kind: "cache", label: "context freshness recovery", detail: safeHistoryAuditDetail(content), status: "refresh"}, true
	case "context_pack_read_recovery":
		return historyAuditEvent{kind: "cache", label: "context pack read recovery", detail: "recovered by reading bounded context evidence", status: "recovery"}, true
	case "completion_gate_recovery":
		return historyAuditEvent{kind: "contract", label: "completion gate recovery", detail: "model output did not yet satisfy the completion contract", status: "recovery"}, true
	case "rejection_recovery_nudge":
		return historyAuditEvent{kind: "recovery", label: "rejection recovery nudge", detail: "tool or answer attempt was rejected and recovery guidance was attached", status: "recovery"}, true
	case "implementation_scoped_tool_recovery", "implementation_write_only_recovery", "required_edit_paths_visible_write_mode", "mutation_visible_write_mode":
		return historyAuditEvent{kind: "recovery", label: strings.ReplaceAll(role, "_", " "), detail: "implementation contract recovery adjusted the next allowed action", status: "recovery"}, true
	case "plan_then_execute_source_followup_redirect":
		return historyAuditEvent{kind: "routing", label: "source evidence follow-up redirected", detail: safeHistoryAuditDetail(content), status: "redirected"}, true
	case "plan_then_execute_submit_plan_required", "plan_then_execute_submit_plan_parse_recovery", "plan_then_execute_submit_plan_after_refresh":
		return historyAuditEvent{kind: "contract", label: strings.ReplaceAll(role, "_", " "), detail: "planner contract requires submit_plan, source-satisfied evidence, or a typed blocker", status: "required"}, true
	case "plan_then_execute_source_satisfied_short_circuit":
		return historyAuditEvent{kind: "contract", label: "source-satisfied short circuit", detail: safeHistoryAuditDetail(content), status: "satisfied"}, true
	case "post_write_verification_required":
		return historyAuditEvent{kind: "verification", label: "post-write verification required", detail: "writes completed before required verification evidence", status: "required"}, true
	case "required_edit_paths_progress":
		return historyAuditEvent{kind: "contract", label: "required edit path progress updated", detail: safeHistoryAuditDetail(content), status: "progress"}, true
	case "final_answer_blocker_override":
		return historyAuditEvent{kind: "contract", label: "final answer blocked and replaced", detail: safeHistoryAuditDetail(content), status: "blocked"}, true
	case "post_completion_verification_cutoff":
		return historyAuditEvent{kind: "verification", label: "post-completion verification cutoff", detail: safeHistoryAuditDetail(content), status: "cutoff"}, true
	case "tool_hidden":
		return historyAuditEvent{kind: "tool surface", label: "tool visibility changed", detail: safeHistoryAuditDetail(content)}, true
	case "doom_loop_guard":
		return historyAuditEvent{kind: "recovery", label: "repeated tool loop guard", detail: safeHistoryAuditDetail(content), status: "approval_required"}, true
	case "auto_salvage", "visible_tool_salvage", "reasoning_tool_salvage":
		return historyAuditEvent{kind: "recovery", label: strings.ReplaceAll(role, "_", " "), detail: safeHistoryAuditDetail(content), status: "salvaged"}, true
	}
	return historyAuditEvent{}, false
}

func historyAuditJSONOrText(kind, label, content string) historyAuditEvent {
	return historyAuditEvent{kind: kind, label: label, detail: safeHistoryAuditDetail(content)}
}

func cleanHistoryAuditEvent(event historyAuditEvent) historyAuditEvent {
	event.kind = truncateRunes(collapseWhitespace(strings.TrimSpace(event.kind)), 40)
	event.label = truncateRunes(collapseWhitespace(strings.TrimSpace(event.label)), 120)
	event.detail = safeHistoryAuditDetail(event.detail)
	event.status = truncateRunes(collapseWhitespace(strings.TrimSpace(event.status)), 40)
	if event.kind == "" && event.label != "" {
		event.kind = "event"
	}
	return event
}

func (event historyAuditEvent) auditLine() string {
	event = cleanHistoryAuditEvent(event)
	if event.label == "" {
		return ""
	}
	prefix := event.kind
	if event.status != "" {
		prefix = joinNonEmpty(" ", prefix, "["+event.status+"]")
	}
	line := joinNonEmpty(": ", prefix, event.label)
	if event.detail != "" {
		line += " - " + event.detail
	}
	return line
}

func historyAuditDigestLines(entry historyEntry) []string {
	lines := make([]string, 0, 4)
	if route := historyAuditRouteDigest(entry.auditEvents); route != "" {
		lines = append(lines, route)
	}
	if tools := historyToolDigest(entry.toolCalls); tools != "" {
		lines = append(lines, tools)
	}
	if latest := historyLatestToolDigest(entry.toolCalls); latest != "" {
		lines = append(lines, latest)
	}
	if contracts := historyContractDigest(entry.auditEvents); contracts != "" {
		lines = append(lines, contracts)
	}
	if signals := historySignalDigest(entry.auditEvents); signals != "" {
		lines = append(lines, signals)
	}
	return lines
}

func historyAuditRouteDigest(events []historyAuditEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		event := cleanHistoryAuditEvent(events[i])
		if event.kind != "lane" {
			continue
		}
		if strings.EqualFold(event.label, "route selected") {
			initial := util.FirstNonEmpty(auditDetailValue(event.detail, "initial"), event.status)
			followup := auditDetailValue(event.detail, "followup")
			contract := auditDetailValue(event.detail, "contract")
			route := initial
			if followup != "" {
				route = joinNonEmpty(" -> ", route, followup)
			}
			if route == "" {
				route = event.label
			}
			if contract != "" {
				return "Route: " + route + " (" + contract + ")"
			}
			return "Route: " + route
		}
		if strings.HasPrefix(event.label, "plan-execute ") {
			return "Plan-execute: " + strings.TrimPrefix(event.label, "plan-execute ")
		}
		if event.status != "" {
			return "Lane: " + event.status
		}
		return "Lane: " + event.label
	}
	return ""
}

func historyToolDigest(toolCalls []historyToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	counts := make(map[string]int)
	running := 0
	failed := 0
	for _, tc := range toolCalls {
		kind := primaryHistoryToolKind(tc.kind)
		if kind == "" {
			kind = "tool"
		}
		counts[kind]++
		if tc.running {
			running++
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tc.outcome)), "failed") {
			failed++
		}
	}
	parts := make([]string, 0, len(counts))
	for _, kind := range []string{"discovery", "read", "write", "verification", "contract", "delegation", "recovery", "shell", "context", "tool"} {
		if count := counts[kind]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, kind))
			delete(counts, kind)
		}
	}
	for kind, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count, kind))
	}
	line := fmt.Sprintf("Tools: %d call(s)", len(toolCalls))
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	if running > 0 {
		line += fmt.Sprintf("; %d running", running)
	}
	if failed > 0 {
		line += fmt.Sprintf("; %d failed", failed)
	}
	return line
}

func historyLatestToolDigest(toolCalls []historyToolCall) string {
	for i := len(toolCalls) - 1; i >= 0; i-- {
		tc := toolCalls[i]
		name := historyToolName(tc.line)
		if name == "" {
			continue
		}
		state := "running"
		if !tc.running {
			state = "completed"
		}
		outcome := compactToolOutcome(tc.outcome)
		if outcome != "" {
			state = outcome
		}
		reason := truncateRunes(collapseWhitespace(strings.TrimSpace(tc.intent)), 100)
		if reason != "" {
			return "Latest: " + name + " " + state + " - " + reason
		}
		return "Latest: " + name + " " + state
	}
	return ""
}

func historyContractDigest(events []historyAuditEvent) string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 2)
	for i := len(events) - 1; i >= 0 && len(parts) < 2; i-- {
		event := cleanHistoryAuditEvent(events[i])
		switch event.kind {
		case "contract", "evidence", "gate", "obligation", "control rails":
		default:
			continue
		}
		text := compactAuditEventLabel(event)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return ""
	}
	reverseStrings(parts)
	return "Contracts: " + strings.Join(parts, "; ")
}

func historySignalDigest(events []historyAuditEvent) string {
	signals := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(signal string) {
		signal = strings.TrimSpace(signal)
		if signal == "" {
			return
		}
		if _, ok := seen[signal]; ok {
			return
		}
		seen[signal] = struct{}{}
		signals = append(signals, signal)
	}
	for _, event := range events {
		event = cleanHistoryAuditEvent(event)
		switch event.kind {
		case "privacy":
			add("redaction active")
		case "cache", "freshness":
			if event.status == "refresh" || strings.Contains(event.label, "refresh") {
				add("cache refreshed")
			} else if strings.Contains(event.label, "cache") {
				add("cache " + util.FirstNonEmpty(event.status, "checked"))
			} else {
				add("freshness " + util.FirstNonEmpty(event.status, "checked"))
			}
		case "routing":
			add("redirected")
		case "recovery":
			add("recovery guidance")
		case "delegation":
			add("delegated check")
		case "permission":
			add("permission " + util.FirstNonEmpty(event.status, "checked"))
		case "tool surface":
			add("tool surface changed")
		}
	}
	if len(signals) == 0 {
		return ""
	}
	if len(signals) > 4 {
		signals = signals[:4]
	}
	return "Signals: " + strings.Join(signals, ", ")
}

func primaryHistoryToolKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return ""
	}
	if idx := strings.Index(kind, "/"); idx >= 0 {
		kind = kind[:idx]
	}
	return strings.TrimSpace(kind)
}

func historyToolName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if idx := strings.Index(line, " | "); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func compactToolOutcome(outcome string) string {
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	switch {
	case outcome == "":
		return ""
	case strings.HasPrefix(outcome, "failed"):
		return "failed"
	case strings.HasPrefix(outcome, "ok"):
		return "ok"
	default:
		return truncateRunes(collapseWhitespace(outcome), 40)
	}
}

func compactAuditEventLabel(event historyAuditEvent) string {
	label := strings.TrimSpace(event.label)
	if label == "" {
		return ""
	}
	switch event.kind {
	case "evidence":
		if event.status != "" {
			return "evidence " + event.status
		}
	case "contract":
		if strings.HasPrefix(label, "contract ") {
			return label
		}
	case "obligation":
		if event.status != "" {
			return "obligation " + event.status
		}
	case "gate":
		if event.status != "" {
			return "gate " + event.status
		}
	case "control rails":
		if event.status != "" {
			return "control rails " + event.status
		}
	}
	return truncateRunes(label, 80)
}

func auditDetailValue(detail string, key string) string {
	for _, field := range strings.Fields(detail) {
		field = strings.Trim(field, ";,")
		prefix := key + "="
		if strings.HasPrefix(field, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(field, prefix))
		}
	}
	return ""
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func safeHistoryAuditDetail(text string) string {
	text = collapseWhitespace(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return truncateRunes(text, 220)
}

func historyShellCommandLooksLikeVerification(command string) bool {
	command = strings.TrimSpace(command)
	return strings.HasPrefix(command, "go test") ||
		strings.HasPrefix(command, "go build") ||
		strings.HasPrefix(command, "make test") ||
		strings.HasPrefix(command, "make build") ||
		strings.Contains(command, " test")
}

func stringMapValue(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return strings.TrimSpace(value)
}

func boolMapValue(fields map[string]any, key string) bool {
	value, _ := fields[key].(bool)
	return value
}

func intMapValue(fields map[string]any, key string) int {
	switch value := fields[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, sep)
}

func keyValue(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return key + "=" + value
}

func boolKeyValue(key string, value bool) string {
	return fmt.Sprintf("%s=%t", key, value)
}

func intKeyValue(key string, value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%s=%d", key, value)
}

func countKeyValue(key string, value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%s=%d", key, value)
}
