package eval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/util"
)

func legacyDiscoveryProfile(tool string) (class string, scope string, cost string) {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "pwd":
		return "workspace_context", "targeted", "low"
	case "list_files":
		return "workspace_listing", "broad", "low"
	case "search_files":
		return "file_search", "targeted", "low"
	case "search_content", "rg":
		return "text_search", "targeted", "low"
	case "read_file":
		return "file_read", "targeted", "low"
	case "code_navigate":
		return "language_symbol", "targeted", "low"
	case "source_sanity":
		return "diagnostics", "targeted", "medium"
	case "discovery_query":
		return "composite_query", "targeted", "medium"
	case "structural_search":
		return "ast_search", "targeted", "medium"
	case "dependency_graph":
		return "dependency_graph", "broad", "medium"
	case "history_range", "history_symbol", "blame_range":
		return "history", "targeted", "medium"
	case "ctags_symbols":
		return "symbol_index", "targeted", "medium"
	case "semgrep_scan", "joern_scan":
		return "security_scan", "broad", "high"
	case "task":
		return "delegated_discovery", "broad", "high"
	case "shell":
		return "other", "", ""
	default:
		return legacyLanguageDiscoveryProfile(tool)
	}
}

func legacyLanguageDiscoveryProfile(tool string) (class string, scope string, cost string) {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			suffix := strings.TrimPrefix(tool, prefix)
			switch suffix {
			case "overview", "entrypoints":
				return "language_overview", "broad", "medium"
			case "find_symbol", "find_function", "find_type", "find_const", "find_package", "workspace_symbol", "file_outline", "read_symbol":
				return "language_symbol", "targeted", "low"
			case "definition", "declaration", "references", "implementations", "calls_from", "calls_into", "extract_body", "related_files", "importers", "includes", "who_includes", "blast_radius", "dead_symbol":
				return "language_relationship", "targeted", "medium"
			case "diagnostics":
				return "diagnostics", "targeted", "medium"
			}
		}
	}
	return "other", "", ""
}

func Analyze(records []Record) Metrics {
	metrics := Metrics{
		Tools:            make(map[string]int),
		SuccessfulTools:  make(map[string]int),
		RejectedTools:    make(map[string]int),
		DiscoveryTools:   make(map[string]int),
		DiscoveryClasses: make(map[string]int),
		DiscoveryScopes:  make(map[string]int),
		DiscoveryCosts:   make(map[string]int),
	}
	verifyAgentSeen := false
	verifyHasFailure := false
	verifyHasInsufficient := false
	laneSummaryTurns := 0
	lastLaneSummary := ""
	writeChangedFiles := []string(nil)
	rollbackChangedFiles := []string(nil)
	rollbackEvidenceSeen := false
	autonomyEvents := []autonomy.Event(nil)
	requestedWritePaths := make(map[string]string)
	requestedToolEventKeys := make(map[string]struct{})
	countedToolEventKeys := make(map[string]struct{})
	countedErrorEventKeys := make(map[string]struct{})
	var firstTS time.Time
	var lastTS time.Time
	for _, record := range records {
		if ts := timestampField(record.Data, "ts"); !ts.IsZero() {
			if firstTS.IsZero() || ts.Before(firstTS) {
				firstTS = ts
			}
			if lastTS.IsZero() || ts.After(lastTS) {
				lastTS = ts
			}
		}
		countMetricsRecordType(&metrics, record.Type)
		if event, ok := autonomyEventFromRecord(record); ok {
			autonomyEvents = append(autonomyEvents, event)
			countMetricsRecordType(&metrics, string(event.Type))
		}
		switch record.Type {
		case "session_start":
			if root := strings.TrimSpace(stringField(record.Data, "workspace_root")); root != "" {
				metrics.WorkspaceRoot = root
			}
		case "message":
			role := stringField(record.Data, "role")
			content := stringField(record.Data, "content")
			countMetricsRole(&metrics, role)
			if role == "assistant" {
				recordTerminalOutcomeSignals(&metrics, content)
			}
			if strings.Contains(strings.ToLower(content), "summary rejected") {
				metrics.SummaryRejections++
				if lane := summaryRejectionLane(role); lane != "" {
					metrics.SummaryRejectionLanes = append(metrics.SummaryRejectionLanes, lane)
				}
			}
			switch role {
			case "user":
				metrics.LastUser = content
				metrics.UserPrompts = append(metrics.UserPrompts, content)
				metrics.UserTurns++
			case "assistant":
				metrics.FinalAssistant = content
				metrics.AssistantTurns++
			case "locate_summary", "explore_summary", "review_summary", "architecture_summary":
				if strings.TrimSpace(content) != "" {
					lastLaneSummary = content
					laneSummaryTurns++
				}
			case "route_plan", "route_plan_final":
				if plan := parseRoutePlan(content); plan.Raw != "" {
					metrics.RoutePlans = append(metrics.RoutePlans, plan)
				}
			case "replan_recommendation":
				if replan := parseReplanRecommendation(content); replan.Raw != "" {
					metrics.Replans = append(metrics.Replans, replan)
				}
			case "verification_plan":
				if plan := parseVerificationPlan(content); plan.Raw != "" {
					metrics.VerificationPlans = append(metrics.VerificationPlans, plan)
				}
			case "readiness_check":
				if readiness := parseReadinessCheck(content); readiness.Raw != "" {
					metrics.ReadinessChecks = append(metrics.ReadinessChecks, readiness)
					metrics.EvidencePaths = append(metrics.EvidencePaths, readiness.EvidencePaths...)
				}
			case "deepening_attempt":
				if deepening := parseDeepeningAttempt(content); deepening.Raw != "" {
					metrics.DeepeningAttempts = append(metrics.DeepeningAttempts, deepening)
				}
			case "final_answer_critic":
				if critic := parseFinalAnswerCritic(content); critic.Raw != "" {
					metrics.FinalAnswerCritics = append(metrics.FinalAnswerCritics, critic)
					metrics.EvidencePaths = append(metrics.EvidencePaths, critic.EvidencePaths...)
				}
			case "effort_policy":
				if policy := parseEffortPolicy(content); policy.Raw != "" {
					metrics.EffortPolicies = append(metrics.EffortPolicies, policy)
				}
			case "long_horizon_state":
				if state := parseLongHorizonState(content); state.Raw != "" {
					metrics.LongHorizonStates = append(metrics.LongHorizonStates, state)
				}
			case "workflow_state":
				if state := parseWorkflowState(content); state.Raw != "" {
					metrics.WorkflowStates = append(metrics.WorkflowStates, state)
				}
			case "budget_ledger":
				if ledger := parseBudgetLedger(content); ledger.Raw != "" {
					metrics.BudgetLedgers = append(metrics.BudgetLedgers, ledger)
				}
			case "evidence_delta":
				if delta := parseEvidenceDelta(content); delta.Raw != "" {
					metrics.EvidenceDeltas = append(metrics.EvidenceDeltas, delta)
				}
			case "claim_confidence":
				if confidence := parseClaimConfidence(content); confidence.Raw != "" {
					metrics.ClaimConfidences = append(metrics.ClaimConfidences, confidence)
				}
			case "context_compaction":
				if compaction := parseContextCompaction(content); compaction.Raw != "" {
					metrics.ContextCompactions = append(metrics.ContextCompactions, compaction)
				}
			case "route_ambiguity":
				if ambiguity := parseRouteAmbiguity(content); ambiguity.Raw != "" {
					metrics.RouteAmbiguities = append(metrics.RouteAmbiguities, ambiguity)
				}
			case "verification_feedback":
				if feedback := parseVerificationFeedback(content); feedback.Raw != "" {
					metrics.VerificationFeedback = append(metrics.VerificationFeedback, feedback)
				}
			case "tool_failure_confidence":
				if failure := parseToolFailureConfidence(content); failure.Raw != "" {
					metrics.ToolFailureConfidence = append(metrics.ToolFailureConfidence, failure)
				}
			case "control_rails":
				if rails := parseControlRails(content); rails.Raw != "" {
					metrics.ControlRails = append(metrics.ControlRails, rails)
				}
			}
			if caps := parseLanguageCapabilities(content); len(caps) > 0 {
				metrics.LanguageCapabilities = mergeLanguageCapabilities(metrics.LanguageCapabilities, caps)
			}
			metrics.EvidencePaths = append(metrics.EvidencePaths, evidencePathsFromJSONContent(content)...)
		case "run_event":
			role := stringField(record.Data, "role")
			content := stringField(record.Data, "content")
			countMetricsRole(&metrics, role)
			if role == "plan_then_execute_low_value_action" {
				metrics.LowValueActions++
			}
			if runnerRejected, completionGateRejected := runnerRejectionSignalsFromRunEvent(role, content); runnerRejected || completionGateRejected {
				if runnerRejected && metrics.RejectedActions < 1 {
					metrics.RejectedActions = 1
				}
				if completionGateRejected && metrics.CompletionGateRejections < 1 {
					metrics.CompletionGateRejections = 1
				}
			}
			switch role {
			case "readiness_check":
				if readiness := parseReadinessCheck(content); readiness.Raw != "" {
					metrics.ReadinessChecks = append(metrics.ReadinessChecks, readiness)
					metrics.EvidencePaths = append(metrics.EvidencePaths, readiness.EvidencePaths...)
				}
			case "deepening_attempt":
				if deepening := parseDeepeningAttempt(content); deepening.Raw != "" {
					metrics.DeepeningAttempts = append(metrics.DeepeningAttempts, deepening)
				}
			case "final_answer_critic":
				if critic := parseFinalAnswerCritic(content); critic.Raw != "" {
					metrics.FinalAnswerCritics = append(metrics.FinalAnswerCritics, critic)
					metrics.EvidencePaths = append(metrics.EvidencePaths, critic.EvidencePaths...)
				}
			case "effort_policy":
				if policy := parseEffortPolicy(content); policy.Raw != "" {
					metrics.EffortPolicies = append(metrics.EffortPolicies, policy)
				}
			case "long_horizon_state":
				if state := parseLongHorizonState(content); state.Raw != "" {
					metrics.LongHorizonStates = append(metrics.LongHorizonStates, state)
				}
			case "workflow_state":
				if state := parseWorkflowState(content); state.Raw != "" {
					metrics.WorkflowStates = append(metrics.WorkflowStates, state)
				}
			case "budget_ledger":
				if ledger := parseBudgetLedger(content); ledger.Raw != "" {
					metrics.BudgetLedgers = append(metrics.BudgetLedgers, ledger)
				}
			case "evidence_delta":
				if delta := parseEvidenceDelta(content); delta.Raw != "" {
					metrics.EvidenceDeltas = append(metrics.EvidenceDeltas, delta)
				}
			case "claim_confidence":
				if confidence := parseClaimConfidence(content); confidence.Raw != "" {
					metrics.ClaimConfidences = append(metrics.ClaimConfidences, confidence)
				}
			case "context_compaction":
				if compaction := parseContextCompaction(content); compaction.Raw != "" {
					metrics.ContextCompactions = append(metrics.ContextCompactions, compaction)
				}
			case "verification_feedback":
				if feedback := parseVerificationFeedback(content); feedback.Raw != "" {
					metrics.VerificationFeedback = append(metrics.VerificationFeedback, feedback)
				}
			case "tool_failure_confidence":
				if failure := parseToolFailureConfidence(content); failure.Raw != "" {
					metrics.ToolFailureConfidence = append(metrics.ToolFailureConfidence, failure)
				}
			case "control_rails":
				if rails := parseControlRails(content); rails.Raw != "" {
					metrics.ControlRails = append(metrics.ControlRails, rails)
				}
			}
			if caps := parseLanguageCapabilities(content); len(caps) > 0 {
				metrics.LanguageCapabilities = mergeLanguageCapabilities(metrics.LanguageCapabilities, caps)
			}
			metrics.EvidencePaths = append(metrics.EvidencePaths, evidencePathsFromJSONContent(content)...)
		case "tool_request":
			if key := toolAttemptEventKey(record.Data); key != "" {
				requestedToolEventKeys[key] = struct{}{}
				if path := writePathFromToolRequest(record.Data); path != "" {
					requestedWritePaths[key] = path
				}
				if _, ok := countedToolEventKeys[key]; ok {
					continue
				}
				countedToolEventKeys[key] = struct{}{}
			}
			recordToolAttemptMetrics(&metrics, record.Data)
		case "tool_result":
			summary := strings.ToLower(stringField(record.Data, "summary"))
			tool := strings.TrimSpace(stringField(record.Data, "tool"))
			errText := strings.TrimSpace(stringField(record.Data, "error"))
			metrics.EvidencePaths = append(metrics.EvidencePaths, evidencePathsFromText(stringField(record.Data, "summary"))...)
			if tool != "" && errText == "" {
				metrics.SuccessfulTools[tool]++
			}
			if path := changedPathFromSuccessfulWriteResult(record.Data, requestedWritePaths); path != "" {
				writeChangedFiles = append(writeChangedFiles, path)
			}
			if tool == "completion_gate" && strings.TrimSpace(stringField(record.Data, "error")) != "" {
				if metrics.CompletionGateRejections < 1 {
					metrics.CompletionGateRejections = 1
				}
			}
			if toolResultCountsAsRejectedAttempt(record.Data) {
				if tool != "" {
					metrics.RejectedTools[tool]++
				}
				if metrics.RejectedActions < 1 {
					metrics.RejectedActions = 1
				}
			}
			if errText != "" && !toolResultAttemptWasRequested(record.Data, requestedToolEventKeys, metrics.Tools) {
				if key := toolAttemptEventKey(record.Data); key != "" {
					if _, ok := countedToolEventKeys[key]; ok {
						// Duplicate runner/telemetry records should not
						// inflate logical tool-attempt counts.
					} else {
						countedToolEventKeys[key] = struct{}{}
						recordToolAttemptMetrics(&metrics, record.Data)
					}
				} else {
					recordToolAttemptMetrics(&metrics, record.Data)
				}
			}
			if strings.Contains(summary, "repeated dead-end action rejected") {
				metrics.RepeatedDeadEndRejections++
			}
			if strings.Contains(summary, "repeated tool request rejected") {
				metrics.RepeatedToolRejections++
			}
			if errText != "" {
				if key := toolErrorEventKey(record.Data); key == "" {
					metrics.ErrorCount++
					metrics.ErrorEvents = append(metrics.ErrorEvents, errorEventSnapshot(record))
				} else if _, ok := countedErrorEventKeys[key]; !ok {
					countedErrorEventKeys[key] = struct{}{}
					metrics.ErrorCount++
					metrics.ErrorEvents = append(metrics.ErrorEvents, errorEventSnapshot(record))
				}
				metrics.Errors = append(metrics.Errors, errText)
			}
			if stringField(record.Data, "agent") == "implement_verify" {
				verifyAgentSeen = true
				metrics.VerificationChecks++
				switch verificationToolResultState(record.Data) {
				case "failed":
					verifyHasFailure = true
				case "insufficient":
					verifyHasInsufficient = true
				}
			}
		case "phase":
			metrics.Phases = append(metrics.Phases, PhaseSnapshot{
				Lane:   stringField(record.Data, "lane"),
				Phase:  stringField(record.Data, "phase"),
				State:  stringField(record.Data, "state"),
				Detail: stringField(record.Data, "detail"),
			})
		case "idiom.plan_execute.executor_handoff_packet":
			metrics.PlanExecutionHandoffs = append(metrics.PlanExecutionHandoffs, PlanExecutionHandoffSnapshot{
				PlanID:              stringField(record.Data, "plan_id"),
				HandoffID:           stringField(record.Data, "handoff_id"),
				HandoffCreated:      boolField(record.Data, "handoff_created") || strings.TrimSpace(stringField(record.Data, "handoff_id")) != "",
				ApprovalScopeID:     stringField(record.Data, "approval_scope_id"),
				ApprovalStatus:      stringField(record.Data, "approval_status"),
				ExecutionPermission: stringField(record.Data, "approval_execution_permission"),
				PackID:              stringField(record.Data, "pack_id"),
				PacketState:         stringField(record.Data, "packet_state"),
				PacketPresent:       boolField(record.Data, "packet_present"),
				PacketNotUsed:       boolField(record.Data, "packet_not_used_by_design"),
				PacketFreshness:     stringField(record.Data, "packet_freshness_state"),
				Reason:              stringField(record.Data, "reason"),
			})
		case "idiom.plan_execute.writer_route_decision":
			if plan := routePlanFromWriterRouteDecision(record.Data); plan.Raw != "" {
				metrics.RoutePlans = append(metrics.RoutePlans, plan)
			}
		case "idiom.plan_execute.planner_blocked":
			metrics.TypedBlockers++
		case "idiom.plan_execute.source_satisfied", "idiom.plan_execute.auto_exact_plan_source_satisfied":
			metrics.SourceSatisfiedOutcomes++
		case "idiom.live_contract.timeout":
			metrics.LiveContractTimeouts++
		case "idiom.plan_execute.plan_scope_drift":
			metrics.PlanExecutionDrift = append(metrics.PlanExecutionDrift, PlanExecutionDriftSnapshot{
				Path:            stringField(record.Data, "path"),
				Reason:          stringField(record.Data, "reason"),
				HandoffID:       stringField(record.Data, "handoff_id"),
				HandoffCreated:  boolField(record.Data, "handoff_created") || strings.TrimSpace(stringField(record.Data, "handoff_id")) != "",
				ApprovalScopeID: stringField(record.Data, "approval_scope_id"),
				PacketState:     stringField(record.Data, "packet_state"),
				PacketPresent:   boolField(record.Data, "packet_present"),
				PacketNotUsed:   boolField(record.Data, "packet_not_used_by_design"),
				PacketFreshness: stringField(record.Data, "packet_freshness_state"),
			})
		case "idiom.plan_execute.verification":
			metrics.VerificationChecks++
			recordVerificationStatus(&metrics, stringField(record.Data, "status"))
		case "idiom.plan_execute.executor_complete":
			metrics.ExecutorCompletes++
			if checked := intField(record.Data, "verification_checked"); checked > metrics.VerificationChecks {
				metrics.VerificationChecks = checked
			}
			if metrics.VerificationChecks > 0 {
				recordVerificationStatus(&metrics, stringField(record.Data, "verification_status"))
			}
		case "error":
			if message := strings.TrimSpace(stringField(record.Data, "message")); message != "" {
				metrics.ErrorCount++
				metrics.Errors = append(metrics.Errors, message)
				metrics.ErrorEvents = append(metrics.ErrorEvents, errorEventSnapshot(record))
			}
		case "turn_summary":
			metrics.TurnSummarySeen = true
			if rejected := intField(record.Data, "rejected_actions"); rejected > metrics.RejectedActions {
				metrics.RejectedActions = rejected
			}
			if rejected := intField(record.Data, "completion_gate_rejections"); rejected > metrics.CompletionGateRejections {
				metrics.CompletionGateRejections = rejected
			}
		case "idiom.live_contract.rollback":
			rollback := LiveContractRollbackSnapshot{
				Status:             strings.TrimSpace(stringField(record.Data, "status")),
				BaselineTree:       strings.TrimSpace(stringField(record.Data, "baseline_tree")),
				PostResetTree:      strings.TrimSpace(stringField(record.Data, "post_reset_tree")),
				ChangedBeforeReset: stringSliceField(record.Data, "changed_before_reset"),
				ChangedAfterReset:  stringSliceField(record.Data, "changed_after_reset"),
			}
			metrics.LiveContractRollbacks = append(metrics.LiveContractRollbacks, rollback)
			rollbackEvidenceSeen = true
			rollbackChangedFiles = append(rollbackChangedFiles, rollback.ChangedBeforeReset...)
		}

		if strings.Contains(strings.ToLower(stringField(record.Data, "summary")), "write approval required") ||
			strings.Contains(strings.ToLower(stringField(record.Data, "error")), "write approval required") {
			metrics.ApprovalRequired = true
		}
	}
	if metrics.AssistantTurns == 0 && lastLaneSummary != "" {
		metrics.FinalAssistant = lastLaneSummary
		metrics.AssistantTurns = laneSummaryTurns
	} else if metrics.AssistantTurns < metrics.UserTurns && laneSummaryTurns > 0 {
		metrics.AssistantTurns = min(metrics.UserTurns, metrics.AssistantTurns+laneSummaryTurns)
	}
	if hasPhaseState(metrics.Phases, "implement", "verify", "done") {
		metrics.VerificationPassed = true
	}
	if hasPhaseState(metrics.Phases, "implement", "verify", "failed") {
		metrics.VerificationFailed = true
	}
	if !metrics.VerificationPassed && !metrics.VerificationFailed && verifyAgentSeen {
		metrics.VerificationFailed = verifyHasFailure
		metrics.VerificationPassed = !verifyHasFailure && !verifyHasInsufficient && metrics.VerificationChecks > 0
	}
	if rollbackEvidenceSeen {
		metrics.ChangedFiles = util.UniqueNonEmpty(rollbackChangedFiles)
	} else {
		metrics.ChangedFiles = util.UniqueNonEmpty(writeChangedFiles)
	}
	if !firstTS.IsZero() && !lastTS.IsZero() && !lastTS.Before(firstTS) {
		metrics.DurationMillis = lastTS.Sub(firstTS).Milliseconds()
	}
	metrics.Errors = util.UniqueNonEmpty(metrics.Errors)
	metrics.EvidencePaths = util.UniqueNonEmpty(metrics.EvidencePaths)
	metrics.SummaryRejectionLanes = util.UniqueNonEmpty(metrics.SummaryRejectionLanes)
	metrics.LanguageCapabilities = mergeLanguageCapabilities(nil, metrics.LanguageCapabilities)
	if len(autonomyEvents) > 0 {
		metrics.ContractReplay = replayAutonomyEvents(autonomyEvents)
	}
	return metrics
}

func changedPathFromSuccessfulWriteResult(data map[string]any, requestedWritePaths map[string]string) string {
	if strings.TrimSpace(stringField(data, "error")) != "" {
		return ""
	}
	tool := strings.TrimSpace(stringField(data, "tool"))
	path := strings.TrimSpace(stringField(data, "path"))
	if path == "" {
		if key := toolAttemptEventKey(data); key != "" {
			path = strings.TrimSpace(requestedWritePaths[key])
		}
	}
	if tool == "" && path != "" {
		tool = writeToolFromSummary(stringField(data, "summary"))
	}
	if path == "" || !writeToolCanChangeFile(tool) {
		return ""
	}
	summary := strings.TrimSpace(stringField(data, "summary"))
	lowerSummary := strings.ToLower(summary)
	if strings.Contains(lowerSummary, "no-op") ||
		strings.Contains(lowerSummary, "no changes") ||
		strings.Contains(lowerSummary, "unchanged") ||
		strings.Contains(lowerSummary, "rejected") {
		return ""
	}
	return path
}

func errorEventSnapshot(record Record) ErrorEventSnapshot {
	errText := strings.TrimSpace(stringField(record.Data, "error"))
	if errText == "" {
		errText = strings.TrimSpace(stringField(record.Data, "message"))
	}
	runnerRejected := record.Type == "tool_result" && toolResultCountsAsRejectedAttempt(record.Data)
	if _, ok := record.Data["runner_rejected"]; ok {
		runnerRejected = boolField(record.Data, "runner_rejected")
	}
	return ErrorEventSnapshot{
		EventType:              strings.TrimSpace(record.Type),
		Tool:                   strings.TrimSpace(stringField(record.Data, "tool")),
		Summary:                strings.TrimSpace(stringField(record.Data, "summary")),
		Error:                  errText,
		Reason:                 strings.TrimSpace(stringField(record.Data, "reason")),
		RunnerRejected:         runnerRejected,
		CompletionGateRejected: record.Type == "tool_result" && strings.TrimSpace(stringField(record.Data, "tool")) == "completion_gate" && errText != "",
	}
}

func writePathFromToolRequest(data map[string]any) string {
	tool := strings.TrimSpace(stringField(data, "tool"))
	if !writeToolCanChangeFile(tool) {
		return ""
	}
	if path := strings.TrimSpace(stringField(data, "path")); path != "" {
		return path
	}
	if path := pathFromRawToolRequest(stringField(data, "raw")); path != "" {
		return path
	}
	action := strings.TrimSpace(stringField(data, "action"))
	if _, path, ok := strings.Cut(action, ":"); ok {
		return strings.TrimSpace(path)
	}
	return ""
}

func pathFromRawToolRequest(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Path)
}

func writeToolCanChangeFile(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "edit_file", "write_file", "append_file", "apply_patch":
		return true
	default:
		return false
	}
}

func writeToolFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	switch {
	case strings.HasPrefix(summary, "Wrote "):
		return "write_file"
	case strings.HasPrefix(summary, "Edited "):
		return "edit_file"
	case strings.HasPrefix(summary, "Appended "):
		return "append_file"
	default:
		return ""
	}
}

func autonomyEventFromRecord(record Record) (autonomy.Event, bool) {
	switch record.Type {
	case "message", "run_event":
		if strings.TrimSpace(stringField(record.Data, "role")) != "autonomy_event" {
			return autonomy.Event{}, false
		}
		return autonomyEventFromContent(stringField(record.Data, "content"))
	case "autonomy_event":
		if content := strings.TrimSpace(stringField(record.Data, "content")); content != "" {
			return autonomyEventFromContent(content)
		}
		return autonomyEventFromData(record.Data)
	default:
		return autonomy.Event{}, false
	}
}

func recordToolAttemptMetrics(metrics *Metrics, data map[string]any) {
	if metrics == nil {
		return
	}
	metrics.ToolRequests++
	tool := strings.TrimSpace(stringField(data, "tool"))
	if tool != "" {
		metrics.Tools[tool]++
	}
	class := strings.TrimSpace(stringField(data, "discovery_class"))
	scope := strings.TrimSpace(stringField(data, "discovery_scope"))
	cost := strings.TrimSpace(stringField(data, "discovery_cost"))
	if class == "shell" {
		class, scope, cost = "other", "", ""
	}
	if class == "" || scope == "" || cost == "" {
		fallbackClass, fallbackScope, fallbackCost := legacyDiscoveryProfile(tool)
		if class == "" {
			class = fallbackClass
		}
		if scope == "" {
			scope = fallbackScope
		}
		if cost == "" {
			cost = fallbackCost
		}
	}
	if class != "" && class != "other" {
		metrics.DiscoveryTools[tool]++
		metrics.DiscoveryClasses[class]++
		if scope != "" {
			metrics.DiscoveryScopes[scope]++
		}
		if cost != "" {
			metrics.DiscoveryCosts[cost]++
			if cost == "high" {
				metrics.HeavyDiscoveryRequests++
			}
		}
	}
	if boolField(data, "broad") {
		metrics.BroadToolRequests++
	}
}

func toolResultCountsAsRejectedAttempt(data map[string]any) bool {
	errText := strings.ToLower(strings.TrimSpace(stringField(data, "error")))
	if errText == "" {
		return false
	}
	summary := strings.ToLower(stringField(data, "summary"))
	return strings.Contains(summary, "rejected") ||
		strings.Contains(summary, "retry blocked") ||
		strings.Contains(summary, "preempted") ||
		(strings.Contains(summary, "follow-up missing named gap") && strings.Contains(errText, "answer readiness has already been reached")) ||
		strings.Contains(summary, "not available in current runtime contract") ||
		strings.Contains(errText, "not in the current allowed tool surface")
}

func toolResultAttemptWasRequested(data map[string]any, requestedKeys map[string]struct{}, tools map[string]int) bool {
	if key := toolAttemptEventKey(data); key != "" {
		_, ok := requestedKeys[key]
		return ok
	}
	tool := strings.TrimSpace(stringField(data, "tool"))
	return tool != "" && tools[tool] > 0
}

func toolAttemptEventKey(data map[string]any) string {
	tool := strings.TrimSpace(stringField(data, "tool"))
	if tool == "" {
		return ""
	}
	if step := intField(data, "step"); step > 0 {
		return fmt.Sprintf("step:%d:%s", step, tool)
	}
	if action := strings.TrimSpace(stringField(data, "action")); action != "" {
		return "action:" + tool + ":" + action
	}
	if raw := strings.TrimSpace(stringField(data, "raw")); raw != "" {
		return "raw:" + tool + ":" + raw
	}
	return ""
}

func toolErrorEventKey(data map[string]any) string {
	tool := strings.TrimSpace(stringField(data, "tool"))
	errText := strings.TrimSpace(stringField(data, "error"))
	if tool == "" || errText == "" {
		return ""
	}
	if step := intField(data, "step"); step > 0 {
		return fmt.Sprintf("step:%d:%s:%s", step, tool, errText)
	}
	if action := strings.TrimSpace(stringField(data, "action")); action != "" {
		return "action:" + tool + ":" + action + ":" + errText
	}
	if raw := strings.TrimSpace(stringField(data, "raw")); raw != "" {
		return "raw:" + tool + ":" + raw + ":" + errText
	}
	return ""
}

func autonomyEventFromContent(content string) (autonomy.Event, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return autonomy.Event{}, false
	}
	var event autonomy.Event
	if err := json.Unmarshal([]byte(content), &event); err != nil {
		return autonomy.Event{}, false
	}
	return normalizedAutonomyEvent(event)
}

func autonomyEventFromData(data map[string]any) (autonomy.Event, bool) {
	if data == nil {
		return autonomy.Event{}, false
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return autonomy.Event{}, false
	}
	var event autonomy.Event
	if err := json.Unmarshal(encoded, &event); err != nil {
		return autonomy.Event{}, false
	}
	return normalizedAutonomyEvent(event)
}

func normalizedAutonomyEvent(event autonomy.Event) (autonomy.Event, bool) {
	event.Normalize()
	if strings.TrimSpace(string(event.Type)) == "" {
		return autonomy.Event{}, false
	}
	return event, true
}

func replayAutonomyEvents(events []autonomy.Event) *ContractReplayReport {
	replay := autonomy.ReplayEvents(events, autonomy.ReplayOptions{})
	audit := autonomy.AuditEvents(events, autonomy.EventAuditOptions{})
	report := &ContractReplayReport{
		SchemaVersion:    "idiom.eval.contract_replay.v1",
		TotalEvents:      replay.TotalEvents,
		AllClear:         len(replay.Violations) == 0 && audit.AllClear,
		Packs:            copyStringIntMap(replay.PackCounts),
		EnforcementModes: copyStringIntMap(replay.EnforcementModeCounts),
		Atoms:            copyStringIntMap(replay.AtomCounts),
		Violations:       contractReplayViolations(replay, audit),
		Replay:           replay,
		Audit:            audit,
	}
	if len(audit.TurnPhaseCoverage.EnforcementModeCounts) > 0 {
		report.EnforcementModes = mergeStringIntMaps(report.EnforcementModes, audit.TurnPhaseCoverage.EnforcementModeCounts)
	}
	if len(report.Packs) == 0 {
		report.Packs = nil
	}
	if len(report.EnforcementModes) == 0 {
		report.EnforcementModes = nil
	}
	if len(report.Atoms) == 0 {
		report.Atoms = nil
	}
	if len(report.Violations) == 0 {
		report.Violations = nil
	}
	return report
}

func contractReplayViolations(replay autonomy.ReplayReport, audit autonomy.EventAuditReport) []ContractReplayViolation {
	violations := make([]ContractReplayViolation, 0, len(replay.Violations)+len(audit.Findings)+len(audit.ObligationCoverage.UnresolvedObligations))
	for _, violation := range replay.Violations {
		violations = append(violations, ContractReplayViolation{
			ID:          violation.ID,
			Severity:    "medium",
			Evidence:    violation.Evidence,
			EventIDs:    util.UniqueNonEmpty([]string{violation.EventID}),
			ContractIDs: util.UniqueNonEmpty([]string{violation.ContractID}),
		})
	}
	for _, finding := range audit.Findings {
		violations = append(violations, ContractReplayViolation{
			ID:          finding.ID,
			Severity:    finding.Severity,
			Evidence:    finding.Evidence,
			EventIDs:    append([]string(nil), finding.EventIDs...),
			ContractIDs: append([]string(nil), finding.ContractIDs...),
		})
	}
	for _, obligation := range audit.ObligationCoverage.UnresolvedObligations {
		violations = append(violations, ContractReplayViolation{
			ID:          "contract_event_unresolved_obligation",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("obligation %q remained open", obligation.ObligationID),
			EventIDs:    util.UniqueNonEmpty([]string{obligation.EventID}),
			ContractIDs: util.UniqueNonEmpty([]string{obligation.ContractID}),
		})
	}
	return violations
}

func copyStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		if key = strings.TrimSpace(key); key != "" && value > 0 {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringIntMaps(maps ...map[string]int) map[string]int {
	out := make(map[string]int)
	for _, in := range maps {
		for key, value := range in {
			if key = strings.TrimSpace(key); key != "" && value > 0 {
				out[key] += value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func incrementReplayCount(counts map[string]int, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		counts[value]++
	}
}

func recordTerminalOutcomeSignals(metrics *Metrics, content string) {
	if metrics == nil {
		return
	}
	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(trimmed, "<<QUESTION>>"), strings.HasPrefix(lower, "decision: block"), strings.HasPrefix(lower, "decision: blocked"):
		metrics.TypedBlockers++
	case strings.HasPrefix(lower, "decision: source-satisfied"), strings.HasPrefix(lower, "decision: source satisfied"), strings.HasPrefix(lower, "source-satisfied:"):
		metrics.SourceSatisfiedOutcomes++
	}
}

func recordVerificationStatus(metrics *Metrics, status string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "pass", "passed", "success", "succeeded":
		if !metrics.VerificationFailed {
			metrics.VerificationPassed = true
		}
	case "fail", "failed", "error", "errored":
		metrics.VerificationFailed = true
		metrics.VerificationPassed = false
	}
}

func verificationToolResultState(data map[string]any) string {
	if strings.TrimSpace(stringField(data, "error")) != "" {
		return "failed"
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		stringField(data, "tool"),
		stringField(data, "summary"),
		stringField(data, "output"),
	}, "\n")))
	if (strings.Contains(text, "source_sanity") || strings.Contains(text, "source sanity")) &&
		(strings.Contains(text, "generic fallback") ||
			strings.Contains(text, "generic_source_sanity") ||
			strings.Contains(text, "generic delimiter") ||
			strings.Contains(text, "generic source sanity")) {
		return "insufficient"
	}
	if strings.Contains(text, "git diff --check") {
		return "insufficient"
	}
	return "passed"
}

func runnerRejectionSignalsFromRunEvent(role string, content string) (runnerRejected bool, completionGateRejected bool) {
	role = strings.TrimSpace(role)
	content = strings.TrimSpace(content)
	if content == "" {
		return false, false
	}
	if role == "next_action_state" || role == "failure_feedback" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err == nil {
			reason := strings.ToLower(strings.TrimSpace(stringField(payload, "reason")))
			failureClass := strings.ToLower(strings.TrimSpace(stringField(payload, "failure_class")))
			lastTool := strings.ToLower(strings.TrimSpace(stringField(payload, "last_tool")))
			failedTool := strings.ToLower(strings.TrimSpace(stringField(payload, "failed_tool")))
			if strings.Contains(reason, "rejected") || strings.Contains(failureClass, "completion_gate") || lastTool == "completion_gate" || failedTool == "completion_gate" {
				return true, lastTool == "completion_gate" || failedTool == "completion_gate" || strings.Contains(failureClass, "completion_gate")
			}
		}
	}
	if role != "autonomy_event" {
		return false, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false, false
	}
	if strings.ToLower(strings.TrimSpace(stringField(payload, "type"))) != "gate_decision" &&
		strings.ToLower(strings.TrimSpace(stringField(payload, "type"))) != "completion_gate_decision" {
		return false, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, false
	}
	lower := strings.ToLower(string(encoded))
	if !strings.Contains(lower, `"decision":"reject"`) {
		return false, false
	}
	return true, strings.Contains(lower, `"completion_gate"`)
}

func countMetricsRole(metrics *Metrics, role string) {
	if metrics == nil {
		return
	}
	trimmedRole := strings.TrimSpace(role)
	if trimmedRole == "" {
		return
	}
	if metrics.MessageRoles == nil {
		metrics.MessageRoles = map[string]int{}
	}
	metrics.MessageRoles[trimmedRole]++
}

func countMetricsRecordType(metrics *Metrics, recordType string) {
	if metrics == nil {
		return
	}
	trimmedType := strings.TrimSpace(recordType)
	if trimmedType == "" {
		return
	}
	if metrics.RecordTypes == nil {
		metrics.RecordTypes = map[string]int{}
	}
	metrics.RecordTypes[trimmedType]++
}

func Evaluate(logPath string, records []Record, scenario Scenario) Report {
	metrics := Analyze(records)
	report := Report{
		Scenario: scenario.Name,
		LogPath:  logPath,
		Passed:   true,
		Metrics:  metrics,
	}
	addFinding := func(format string, args ...any) {
		report.Passed = false
		report.Findings = append(report.Findings, fmt.Sprintf(format, args...))
	}

	lastUserLower := strings.ToLower(metrics.LastUser)
	for _, required := range scenario.PromptContains {
		if !strings.Contains(lastUserLower, strings.ToLower(strings.TrimSpace(required))) {
			addFinding("last user prompt does not contain %q", required)
		}
	}
	promptHistoryCorpus := strings.Join(metrics.UserPrompts, "\n")
	if strings.TrimSpace(promptHistoryCorpus) == "" {
		promptHistoryCorpus = metrics.LastUser
	}
	promptHistoryLower := strings.ToLower(promptHistoryCorpus)
	for _, required := range scenario.PromptHistoryContains {
		if !strings.Contains(promptHistoryLower, strings.ToLower(strings.TrimSpace(required))) {
			addFinding("user prompt history does not contain %q", required)
		}
	}
	finalAssistantLower := strings.ToLower(metrics.FinalAssistant)
	for _, required := range scenario.FinalAssistantContains {
		if !strings.Contains(finalAssistantLower, strings.ToLower(strings.TrimSpace(required))) {
			addFinding("final assistant response does not contain %q", required)
		}
	}
	for _, forbidden := range scenario.ForbidFinalAssistantContains {
		if strings.Contains(finalAssistantLower, strings.ToLower(strings.TrimSpace(forbidden))) {
			addFinding("final assistant response unexpectedly contains %q", forbidden)
		}
	}
	if scenario.RequireAssistantForEachUser && metrics.AssistantTurns < metrics.UserTurns {
		addFinding("assistant turns fewer than user turns: got %d assistant for %d user", metrics.AssistantTurns, metrics.UserTurns)
	}
	for _, tool := range scenario.RequireTools {
		if metrics.Tools[strings.TrimSpace(tool)] == 0 {
			addFinding("required tool %q was not used", tool)
		}
	}
	for _, tool := range scenario.RequireRejectedTools {
		if metrics.RejectedTools[strings.TrimSpace(tool)] == 0 {
			addFinding("required rejected tool %q was not observed", tool)
		}
	}
	for _, tool := range scenario.ForbidTools {
		if metrics.Tools[strings.TrimSpace(tool)] > 0 {
			addFinding("forbidden tool %q was used", tool)
		}
	}
	for _, tool := range scenario.RequireDiscoveryTools {
		if metrics.DiscoveryTools[strings.TrimSpace(tool)] == 0 {
			addFinding("required discovery tool %q was not used", tool)
		}
	}
	for _, class := range scenario.RequireDiscoveryClasses {
		if metrics.DiscoveryClasses[strings.TrimSpace(class)] == 0 {
			addFinding("required discovery class %q was not used", class)
		}
	}
	if scenario.MaxHeavyDiscoveryRequests != nil && metrics.HeavyDiscoveryRequests > *scenario.MaxHeavyDiscoveryRequests {
		addFinding("heavy discovery requests exceeded limit: got %d want <= %d", metrics.HeavyDiscoveryRequests, *scenario.MaxHeavyDiscoveryRequests)
	}
	for _, phase := range scenario.RequirePhases {
		if !containsPhase(metrics.Phases, phase) {
			addFinding("required phase was not observed: lane=%q phase=%q state=%q", phase.Lane, phase.Phase, phase.State)
		}
	}
	if scenario.RequireRoutePlan != nil {
		if len(metrics.RoutePlans) == 0 {
			addFinding("required route plan was not observed")
		} else if !routePlanMatches(metrics.RoutePlans[len(metrics.RoutePlans)-1], *scenario.RequireRoutePlan) {
			latest := metrics.RoutePlans[len(metrics.RoutePlans)-1]
			addFinding("route plan mismatch: got initial=%q followup=%q reason=%q contract=%q thoroughness=%q",
				latest.InitialLane,
				latest.FollowupLane,
				latest.Reason,
				latest.Contract,
				latest.Thoroughness,
			)
		}
	}
	for _, role := range scenario.RequireRunEvents {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			continue
		}
		// Match either a type:"message" role (runner-emitted RunEvents) or a
		// top-level record type (orchestrator-emitted events such as
		// idiom.plan_execute.preflight_failed). One field, two valid sources.
		if metrics.MessageRoles[trimmed] == 0 && metrics.RecordTypes[trimmed] == 0 {
			addFinding("required run event %q was not observed in log", trimmed)
		}
	}
	if scenario.RequireReplan != nil {
		if len(metrics.Replans) == 0 {
			addFinding("required replan recommendation was not observed")
		} else if !replanMatches(metrics.Replans[len(metrics.Replans)-1], *scenario.RequireReplan) {
			latest := metrics.Replans[len(metrics.Replans)-1]
			addFinding("replan recommendation mismatch: got from=%q to=%q reason=%q", latest.FromLane, latest.ToLane, latest.Reason)
		}
	}
	if scenario.MaxBroadToolRequests != nil && metrics.BroadToolRequests > *scenario.MaxBroadToolRequests {
		addFinding("broad tool requests exceeded limit: got %d want <= %d", metrics.BroadToolRequests, *scenario.MaxBroadToolRequests)
	}
	totalDeadEnds := metrics.RepeatedDeadEndRejections + metrics.RepeatedToolRejections
	if scenario.MaxRepeatedDeadEnds != nil && totalDeadEnds > *scenario.MaxRepeatedDeadEnds {
		addFinding("repeated dead-end requests exceeded limit: got %d want <= %d", totalDeadEnds, *scenario.MaxRepeatedDeadEnds)
	}
	if scenario.MaxSummaryRejections != nil && metrics.SummaryRejections > *scenario.MaxSummaryRejections {
		addFinding("summary rejections exceeded limit: got %d want <= %d", metrics.SummaryRejections, *scenario.MaxSummaryRejections)
	}
	switch strings.ToLower(strings.TrimSpace(scenario.RequireVerification)) {
	case "", "any":
	case "passed":
		if !metrics.VerificationPassed {
			addFinding("verification did not pass")
		}
	case "failed":
		if !metrics.VerificationFailed {
			addFinding("verification did not fail")
		}
	case "none":
		if metrics.VerificationPassed || metrics.VerificationFailed || metrics.VerificationChecks > 0 {
			addFinding("verification unexpectedly ran")
		}
	default:
		addFinding("unsupported require_verification value %q", scenario.RequireVerification)
	}
	if scenario.RequireVerificationPlan != nil {
		if len(metrics.VerificationPlans) == 0 {
			addFinding("required verification plan was not observed")
		} else if !verificationPlanMatches(metrics.VerificationPlans[len(metrics.VerificationPlans)-1], *scenario.RequireVerificationPlan) {
			latest := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
			addFinding("verification plan mismatch: got coverage=%q confidence=%q risk=%q full_suite=%t commands=%v expected_failures=%v",
				latest.Coverage,
				latest.Confidence,
				latest.RiskLevel,
				latest.UsesFullSuite,
				latest.Commands,
				latest.ExpectedFailures,
			)
		}
	}
	if scenario.RequireReadiness != nil {
		if len(metrics.ReadinessChecks) == 0 {
			addFinding("required readiness check was not observed")
		} else if !readinessMatches(metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1], *scenario.RequireReadiness) {
			latest := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
			addFinding("readiness check mismatch: got lane=%q ready=%t confidence=%q action=%q evidence_paths=%d reason=%q",
				latest.Lane,
				latest.Ready,
				latest.Confidence,
				latest.Action,
				len(latest.EvidencePaths),
				latest.Reason,
			)
		}
	}
	if scenario.RequireDeepening != nil {
		if len(metrics.DeepeningAttempts) == 0 {
			addFinding("required deepening attempt was not observed")
		} else if !deepeningMatches(metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1], *scenario.RequireDeepening) {
			latest := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
			addFinding("deepening attempt mismatch: got lane=%q attempted=%t exhausted=%t action=%q evidence_paths=%d reason=%q",
				latest.Lane,
				latest.Attempted,
				latest.Exhausted,
				latest.Action,
				len(latest.EvidencePaths),
				latest.Reason,
			)
		}
	}
	if scenario.RequireFinalAnswerCritic != nil {
		if len(metrics.FinalAnswerCritics) == 0 {
			addFinding("required final answer critic was not observed")
		} else if !finalAnswerCriticMatches(metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1], *scenario.RequireFinalAnswerCritic) {
			latest := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
			addFinding("final answer critic mismatch: got contract=%q passed=%t action=%q repaired=%t findings=%v",
				latest.Contract,
				latest.Passed,
				latest.Action,
				latest.Repaired,
				latest.Findings,
			)
		}
	}
	if scenario.RequireEffortPolicy != nil {
		if len(metrics.EffortPolicies) == 0 {
			addFinding("required effort policy was not observed")
		} else if !effortPolicyMatches(metrics.EffortPolicies[len(metrics.EffortPolicies)-1], *scenario.RequireEffortPolicy) {
			latest := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
			addFinding("effort policy mismatch: got lane=%q level=%q analysis_budget=%d implement_budget=%d deepening=%d confidence_floor=%q exhaustion=%q",
				latest.Lane,
				latest.Level,
				latest.AnalysisToolBudget,
				latest.ImplementToolBudget,
				latest.DeepeningPasses,
				latest.ConfidenceFloor,
				latest.BudgetExhaustionMode,
			)
		}
	}
	if scenario.RequireLongHorizonState != nil {
		if len(metrics.LongHorizonStates) == 0 {
			addFinding("required long-horizon state was not observed")
		} else if !longHorizonStateMatches(metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1], *scenario.RequireLongHorizonState) {
			latest := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
			addFinding("long-horizon state mismatch: got action=%q kind=%q source_lane=%q stage=%d focus=%q remaining=%v targets=%v resumable=%t next=%q",
				latest.Action,
				latest.MemoryKind,
				latest.SourceLane,
				latest.Stage,
				latest.FocusPath,
				latest.Remaining,
				latest.ChosenTargets,
				latest.Resumable,
				latest.NextStep,
			)
		}
	}
	if scenario.RequireWorkflowState != nil {
		if len(metrics.WorkflowStates) == 0 {
			addFinding("required workflow state was not observed")
		} else if !workflowStateMatches(metrics.WorkflowStates[len(metrics.WorkflowStates)-1], *scenario.RequireWorkflowState) {
			latest := metrics.WorkflowStates[len(metrics.WorkflowStates)-1]
			addFinding("workflow state mismatch: got action=%q kind=%q phase=%q source_lane=%q targets=%v editable=%v evidence=%v read_only=%v resumable=%t",
				latest.Action,
				latest.Kind,
				latest.Phase,
				latest.SourceLane,
				latest.TargetRefs,
				latest.EditableTargets,
				latest.EvidencePaths,
				latest.ReadOnlyEvidence,
				latest.Resumable,
			)
		}
	}
	if scenario.RequireBudgetLedger != nil {
		if len(metrics.BudgetLedgers) == 0 {
			addFinding("required budget ledger was not observed")
		} else if !budgetLedgerMatches(metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1], *scenario.RequireBudgetLedger) {
			latest := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
			addFinding("budget ledger mismatch: got lane=%q stop=%q model_calls=%d evidence=%d exhausted=%t",
				latest.Lane, latest.StopReason, latest.UsedModelCalls, latest.TotalEvidencePaths, latest.BudgetExhausted)
		}
	}
	if scenario.RequireEvidenceDelta != nil {
		if len(metrics.EvidenceDeltas) == 0 {
			addFinding("required evidence delta was not observed")
		} else if !evidenceDeltaMatches(metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1], *scenario.RequireEvidenceDelta) {
			latest := metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1]
			addFinding("evidence delta mismatch: got lane=%q new_paths=%d marginal=%q tools=%d failed=%d",
				latest.Lane, len(latest.NewEvidencePaths), latest.MarginalValue, latest.ToolResults, latest.FailedTools)
		}
	}
	if scenario.RequireClaimConfidence != nil {
		if len(metrics.ClaimConfidences) == 0 {
			addFinding("required claim confidence was not observed")
		} else if !claimConfidenceMatches(metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1], *scenario.RequireClaimConfidence) {
			latest := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
			addFinding("claim confidence mismatch: got lane=%q confidence=%q evidence=%d unresolved=%d basis=%q",
				latest.Lane, latest.Confidence, len(latest.EvidencePaths), len(latest.UnresolvedUncertainty), latest.CalibrationBasis)
		}
	}
	if scenario.RequireContextCompaction != nil {
		if len(metrics.ContextCompactions) == 0 {
			addFinding("required context compaction was not observed")
		} else if !contextCompactionMatches(metrics.ContextCompactions[len(metrics.ContextCompactions)-1], *scenario.RequireContextCompaction) {
			latest := metrics.ContextCompactions[len(metrics.ContextCompactions)-1]
			addFinding("context compaction mismatch: got dropped=%d retained=%d max_history=%d pinned=%v memory=%q strategy=%q",
				latest.DroppedMessages,
				latest.RetainedMessages,
				latest.MaxHistory,
				latest.PinnedPaths,
				latest.PinnedMemoryKind,
				latest.Strategy,
			)
		}
	}
	if scenario.RequireRouteAmbiguity != nil {
		if len(metrics.RouteAmbiguities) == 0 {
			addFinding("required route ambiguity was not observed")
		} else if !routeAmbiguityMatches(metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1], *scenario.RequireRouteAmbiguity) {
			latest := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
			addFinding("route ambiguity mismatch: got selected=%q ambiguous=%t signals=%v tie_break=%q",
				latest.SelectedLane, latest.Ambiguous, latest.Signals, latest.TieBreak)
		}
	}
	if scenario.RequireVerificationFeedback != nil {
		if len(metrics.VerificationFeedback) == 0 {
			addFinding("required verification feedback was not observed")
		} else if !verificationFeedbackMatches(metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1], *scenario.RequireVerificationFeedback) {
			latest := metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1]
			addFinding("verification feedback mismatch: got confidence=%q failed=%v recommendation=%q",
				latest.Confidence, latest.FailedCommands, latest.LearnedRecommendation)
		}
	}
	if scenario.RequireToolFailureConfidence != nil {
		if len(metrics.ToolFailureConfidence) == 0 {
			addFinding("required tool-failure confidence was not observed")
		} else if !toolFailureConfidenceMatches(metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1], *scenario.RequireToolFailureConfidence) {
			latest := metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1]
			addFinding("tool-failure confidence mismatch: got lane=%q tool=%q impact=%q",
				latest.Lane, latest.Tool, latest.ConfidenceImpact)
		}
	}
	if scenario.RequireControlRails != nil {
		if len(metrics.ControlRails) == 0 {
			addFinding("required control rails were not observed")
		} else if !controlRailsMatches(metrics.ControlRails[len(metrics.ControlRails)-1], *scenario.RequireControlRails) {
			latest := metrics.ControlRails[len(metrics.ControlRails)-1]
			addFinding("control rails mismatch: got lane=%q followup=%q contract=%q family=%q permission=%q targets=%v evidence=%v tools=%v verification=%q failure_to_eval=%t",
				latest.Lane,
				latest.FollowupLane,
				latest.Contract,
				latest.TaskFamily,
				latest.PermissionProfile,
				latest.TargetRefs,
				latest.RequiredEvidence,
				latest.RequiredTools,
				latest.VerificationContract,
				latest.FailureToEval,
			)
		}
	}
	if scenario.RequireContractReplay != nil {
		if metrics.ContractReplay == nil {
			addFinding("required contract replay was not observed")
		} else {
			for _, pack := range scenario.RequireContractReplay.RequirePacks {
				if mapCountFold(metrics.ContractReplay.Packs, pack) == 0 {
					addFinding("contract replay pack %q was not observed", pack)
				}
			}
			for _, mode := range scenario.RequireContractReplay.RequireEnforcementModes {
				if mapCountFold(metrics.ContractReplay.EnforcementModes, mode) == 0 {
					addFinding("contract replay enforcement mode %q was not observed", mode)
				}
			}
			for _, atom := range scenario.RequireContractReplay.RequireAtoms {
				if mapCountFold(metrics.ContractReplay.Atoms, atom) == 0 {
					addFinding("contract replay atom %q was not observed", atom)
				}
			}
			if scenario.RequireContractReplay.MaxViolations != nil && len(metrics.ContractReplay.Replay.Violations) > *scenario.RequireContractReplay.MaxViolations {
				addFinding("contract replay violations exceeded limit: got %d want <= %d", len(metrics.ContractReplay.Replay.Violations), *scenario.RequireContractReplay.MaxViolations)
			}
			for _, id := range scenario.RequireContractReplay.ForbidViolationIDs {
				if contractReplayHasViolation(metrics.ContractReplay.Violations, id) {
					addFinding("contract replay violation %q was forbidden", id)
				}
			}
		}
	}
	for _, required := range scenario.RequireChangedFiles {
		if !containsStringFold(metrics.ChangedFiles, required) {
			addFinding("changed files do not include %q", required)
		}
	}
	for _, required := range scenario.RequireEvidencePaths {
		if !containsStringFold(metrics.EvidencePaths, required) {
			addFinding("evidence paths do not include %q", required)
		}
	}
	for _, required := range scenario.RequireLanguageCapabilities {
		if !languageCapabilityMatches(metrics.LanguageCapabilities, required) {
			addFinding("language capabilities do not include language=%q tool=%q signal=%q", required.Language, required.Tool, required.Signal)
		}
	}
	if scenario.RequireApproval != nil && metrics.ApprovalRequired != *scenario.RequireApproval {
		addFinding("approval requirement mismatch: got %t want %t", metrics.ApprovalRequired, *scenario.RequireApproval)
	}
	if scenario.RequirePlanExecution != nil {
		for _, finding := range evaluatePlanExecutionExpectation(metrics, *scenario.RequirePlanExecution) {
			addFinding("%s", finding)
		}
	}
	return report
}

func mapCountFold(values map[string]int, target string) int {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0
	}
	for key, count := range values {
		if strings.EqualFold(strings.TrimSpace(key), target) {
			return count
		}
	}
	return 0
}

func contractReplayHasViolation(violations []ContractReplayViolation, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, violation := range violations {
		if strings.EqualFold(strings.TrimSpace(violation.ID), id) {
			return true
		}
	}
	return false
}

func evaluatePlanExecutionExpectation(metrics Metrics, expected PlanExecutionExpectation) []string {
	var findings []string
	if expected.RequireHandoffID && !planExecutionHasHandoffID(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution handoff id was required but not observed")
	}
	if expected.RequireHandoffCreated && !planExecutionHasHandoffCreated(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution handoff creation was required but not observed")
	}
	if expected.RequireApprovalScopeID && !planExecutionHasApprovalScopeID(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution approval scope id was required but not observed")
	}
	if expected.RequirePacketPresent && !planExecutionHasPacketPresent(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution packet presence was required but not observed")
	}
	if expected.RequirePacketFresh && !planExecutionHasPacketFresh(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution fresh packet was required but not observed")
	}
	if expected.RequirePacketNotUsed && !planExecutionHasPacketNotUsed(metrics.PlanExecutionHandoffs) {
		findings = append(findings, "plan execution packet non-use by design was required but not observed")
	}
	if expected.RequireNoDrift && len(metrics.PlanExecutionDrift) > 0 {
		findings = append(findings, fmt.Sprintf("plan execution drift was forbidden but observed %d drift event(s)", len(metrics.PlanExecutionDrift)))
	}
	if expected.RequireDrift && len(metrics.PlanExecutionDrift) == 0 {
		findings = append(findings, "plan execution drift was required but not observed")
	}
	for _, reason := range expected.RequireDriftReasons {
		reason = normalizedCoverageKey(reason)
		if reason == "" {
			continue
		}
		if !planExecutionHasDriftReason(metrics.PlanExecutionDrift, reason) {
			findings = append(findings, fmt.Sprintf("plan execution drift reason %q was not observed", reason))
		}
	}
	for _, state := range expected.RequirePacketStates {
		state = normalizedCoverageKey(state)
		if state == "" {
			continue
		}
		if !planExecutionHasPacketState(metrics.PlanExecutionHandoffs, state) {
			findings = append(findings, fmt.Sprintf("plan execution packet state %q was not observed", state))
		}
	}
	for _, state := range expected.RequirePacketFreshnessState {
		state = normalizedCoverageKey(state)
		if state == "" {
			continue
		}
		if !planExecutionHasPacketFreshness(metrics.PlanExecutionHandoffs, state) {
			findings = append(findings, fmt.Sprintf("plan execution packet freshness state %q was not observed", state))
		}
	}
	for _, permission := range expected.RequireExecutionPermissions {
		permission = normalizedCoverageKey(permission)
		if permission == "" {
			continue
		}
		if !planExecutionHasPermission(metrics.PlanExecutionHandoffs, permission) {
			findings = append(findings, fmt.Sprintf("plan execution permission %q was not observed", permission))
		}
	}
	return findings
}

func planExecutionHasHandoffID(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if strings.TrimSpace(handoff.HandoffID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasHandoffCreated(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if handoff.HandoffCreated || strings.TrimSpace(handoff.HandoffID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasApprovalScopeID(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if strings.TrimSpace(handoff.ApprovalScopeID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasDriftReason(drifts []PlanExecutionDriftSnapshot, reason string) bool {
	for _, drift := range drifts {
		if normalizedCoverageKey(drift.Reason) == reason {
			return true
		}
	}
	return false
}

func planExecutionHasPacketState(handoffs []PlanExecutionHandoffSnapshot, state string) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.PacketState) == state {
			return true
		}
	}
	return false
}

func planExecutionHasPacketPresent(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if handoff.PacketPresent || planExecutionPacketStateImpliesPresence(handoff.PacketState) && strings.TrimSpace(handoff.PackID) != "" {
			return true
		}
	}
	return false
}

func planExecutionHasPacketFresh(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.PacketFreshness) == "fresh" {
			return true
		}
	}
	return false
}

func planExecutionHasPacketNotUsed(handoffs []PlanExecutionHandoffSnapshot) bool {
	for _, handoff := range handoffs {
		if handoff.PacketNotUsed || normalizedCoverageKey(handoff.PacketFreshness) == "not_used_by_design" {
			return true
		}
	}
	return false
}

func planExecutionHasPacketFreshness(handoffs []PlanExecutionHandoffSnapshot, state string) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.PacketFreshness) == state {
			return true
		}
	}
	return false
}

func planExecutionPacketStateImpliesPresence(state string) bool {
	switch normalizedCoverageKey(state) {
	case "inherited", "stale", "overridden":
		return true
	default:
		return false
	}
}

func planExecutionHasPermission(handoffs []PlanExecutionHandoffSnapshot, permission string) bool {
	for _, handoff := range handoffs {
		if normalizedCoverageKey(handoff.ExecutionPermission) == permission {
			return true
		}
	}
	return false
}

func containsPhase(phases []PhaseSnapshot, expected PhaseExpectation) bool {
	for _, phase := range phases {
		if expected.Lane != "" && !strings.EqualFold(phase.Lane, expected.Lane) {
			continue
		}
		if expected.Phase != "" && !strings.EqualFold(phase.Phase, expected.Phase) {
			continue
		}
		if expected.State != "" && !strings.EqualFold(phase.State, expected.State) {
			continue
		}
		return true
	}
	return false
}

func parseRoutePlan(content string) RoutePlanSnapshot {
	content = strings.TrimSpace(content)
	plan := RoutePlanSnapshot{Raw: content}
	if content == "" {
		return RoutePlanSnapshot{}
	}
	for _, field := range strings.Fields(content) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "initial":
			plan.InitialLane = value
		case "followup":
			plan.FollowupLane = value
		case "reason":
			plan.Reason = value
		case "contract":
			plan.Contract = value
		case "mode":
			plan.AgentMode = value
		case "workflow":
			plan.WorkflowKind = value
		case "phase":
			plan.WorkflowPhase = value
		case "thoroughness":
			plan.Thoroughness = value
		}
	}
	return plan
}

func routePlanFromWriterRouteDecision(data map[string]any) RoutePlanSnapshot {
	taskFamily := strings.TrimSpace(stringField(data, "task_family"))
	if taskFamily == "" {
		return RoutePlanSnapshot{}
	}
	plan := RoutePlanSnapshot{
		Raw:         "writer_route_decision:" + taskFamily,
		InitialLane: taskFamily,
	}
	switch taskFamily {
	case "plan_then_execute_planner":
		plan.Reason = "plan_then_execute_request"
		plan.Contract = "implementation_result"
	case "plan_then_execute_executor":
		plan.Reason = "plan_artifact_dispatch"
		plan.Contract = "executor_dispatch_result"
	default:
		reasonCodes := stringSliceField(data, "reason_codes")
		if len(reasonCodes) > 0 {
			plan.Reason = routePlanReasonFromWriterRouteReason(reasonCodes[0])
		}
	}
	return plan
}

func routePlanReasonFromWriterRouteReason(reason string) string {
	reason = strings.TrimSpace(reason)
	reason = strings.TrimPrefix(reason, "route.")
	return strings.ReplaceAll(reason, "-", "_")
}

func routePlanMatches(actual RoutePlanSnapshot, expected RoutePlanExpectation) bool {
	if expected.InitialLane != "" && !strings.EqualFold(actual.InitialLane, expected.InitialLane) {
		return false
	}
	if expected.FollowupLane != "" && !strings.EqualFold(actual.FollowupLane, expected.FollowupLane) {
		return false
	}
	if expected.Reason != "" && !strings.EqualFold(actual.Reason, expected.Reason) {
		return false
	}
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.AgentMode != "" && !strings.EqualFold(actual.AgentMode, expected.AgentMode) {
		return false
	}
	if expected.WorkflowKind != "" && !strings.EqualFold(actual.WorkflowKind, expected.WorkflowKind) {
		return false
	}
	if expected.WorkflowPhase != "" && !strings.EqualFold(actual.WorkflowPhase, expected.WorkflowPhase) {
		return false
	}
	if expected.Thoroughness != "" && !strings.EqualFold(actual.Thoroughness, expected.Thoroughness) {
		return false
	}
	return true
}

func parseReplanRecommendation(content string) ReplanSnapshot {
	content = strings.TrimSpace(content)
	replan := ReplanSnapshot{Raw: content}
	if content == "" {
		return ReplanSnapshot{}
	}
	head, reason, hasReason := strings.Cut(content, ":")
	if hasReason {
		replan.Reason = strings.TrimSpace(reason)
	} else {
		head = content
	}
	from, to, ok := strings.Cut(strings.TrimSpace(head), "->")
	if ok {
		replan.FromLane = strings.TrimSpace(from)
		replan.ToLane = strings.TrimSpace(to)
	}
	return replan
}

func replanMatches(actual ReplanSnapshot, expected ReplanExpectation) bool {
	if expected.FromLane != "" && !strings.EqualFold(actual.FromLane, expected.FromLane) {
		return false
	}
	if expected.ToLane != "" && !strings.EqualFold(actual.ToLane, expected.ToLane) {
		return false
	}
	if expected.Reason != "" && !strings.Contains(strings.ToLower(actual.Reason), strings.ToLower(strings.TrimSpace(expected.Reason))) {
		return false
	}
	return true
}

func parseVerificationPlan(content string) VerificationPlanSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return VerificationPlanSnapshot{}
	}
	var plan VerificationPlanSnapshot
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return VerificationPlanSnapshot{}
	}
	plan.Raw = content
	plan.Commands = util.UniqueNonEmpty(plan.Commands)
	plan.Reasons = util.UniqueNonEmpty(plan.Reasons)
	plan.ExpectedFailures = util.UniqueNonEmpty(plan.ExpectedFailures)
	plan.Coverage = strings.TrimSpace(plan.Coverage)
	plan.Confidence = strings.TrimSpace(plan.Confidence)
	plan.RiskLevel = strings.TrimSpace(plan.RiskLevel)
	plan.RepairPolicy = strings.TrimSpace(plan.RepairPolicy)
	return plan
}

func verificationPlanMatches(actual VerificationPlanSnapshot, expected VerificationPlanExpectation) bool {
	if expected.Coverage != "" && !strings.EqualFold(actual.Coverage, expected.Coverage) {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.RiskLevel != "" && !strings.EqualFold(actual.RiskLevel, expected.RiskLevel) {
		return false
	}
	if expected.UsesFullSuite != nil && actual.UsesFullSuite != *expected.UsesFullSuite {
		return false
	}
	for _, command := range expected.RequireCommands {
		if !containsStringFold(actual.Commands, command) {
			return false
		}
	}
	for _, failure := range expected.RequireExpectedFailures {
		if !containsStringFold(actual.ExpectedFailures, failure) {
			return false
		}
	}
	return true
}

func parseReadinessCheck(content string) ReadinessSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ReadinessSnapshot{}
	}
	var readiness ReadinessSnapshot
	if err := json.Unmarshal([]byte(content), &readiness); err != nil {
		return ReadinessSnapshot{}
	}
	readiness.Raw = content
	readiness.Lane = strings.TrimSpace(readiness.Lane)
	readiness.Confidence = strings.TrimSpace(readiness.Confidence)
	readiness.Action = strings.TrimSpace(readiness.Action)
	readiness.Reason = strings.TrimSpace(readiness.Reason)
	readiness.EvidencePaths = util.UniqueNonEmpty(readiness.EvidencePaths)
	return readiness
}

func readinessMatches(actual ReadinessSnapshot, expected ReadinessExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Ready != nil && actual.Ready != *expected.Ready {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	reason := strings.ToLower(actual.Reason)
	for _, required := range expected.ReasonContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(reason, required) {
			return false
		}
	}
	return true
}

func parseDeepeningAttempt(content string) DeepeningSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return DeepeningSnapshot{}
	}
	var deepening DeepeningSnapshot
	if err := json.Unmarshal([]byte(content), &deepening); err != nil {
		return DeepeningSnapshot{}
	}
	deepening.Raw = content
	deepening.Lane = strings.TrimSpace(deepening.Lane)
	deepening.Action = strings.TrimSpace(deepening.Action)
	deepening.Reason = strings.TrimSpace(deepening.Reason)
	deepening.EvidencePaths = util.UniqueNonEmpty(deepening.EvidencePaths)
	return deepening
}

func deepeningMatches(actual DeepeningSnapshot, expected DeepeningExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Attempted != nil && actual.Attempted != *expected.Attempted {
		return false
	}
	if expected.Exhausted != nil && actual.Exhausted != *expected.Exhausted {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	reason := strings.ToLower(actual.Reason)
	for _, required := range expected.ReasonContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(reason, required) {
			return false
		}
	}
	return true
}

func parseFinalAnswerCritic(content string) FinalAnswerCriticSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return FinalAnswerCriticSnapshot{}
	}
	var critic FinalAnswerCriticSnapshot
	if err := json.Unmarshal([]byte(content), &critic); err != nil {
		return FinalAnswerCriticSnapshot{}
	}
	critic.Raw = content
	critic.Contract = strings.TrimSpace(critic.Contract)
	critic.Action = strings.TrimSpace(critic.Action)
	critic.Findings = util.UniqueNonEmpty(critic.Findings)
	critic.EvidencePaths = util.UniqueNonEmpty(critic.EvidencePaths)
	return critic
}

func finalAnswerCriticMatches(actual FinalAnswerCriticSnapshot, expected FinalAnswerCriticExpectation) bool {
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.Passed != nil && actual.Passed != *expected.Passed {
		return false
	}
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.Repaired != nil && actual.Repaired != *expected.Repaired {
		return false
	}
	findings := strings.ToLower(strings.Join(actual.Findings, "\n"))
	for _, required := range expected.RequireFindings {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(findings, required) {
			return false
		}
	}
	return true
}

func parseEffortPolicy(content string) EffortPolicySnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return EffortPolicySnapshot{}
	}
	var policy EffortPolicySnapshot
	if err := json.Unmarshal([]byte(content), &policy); err != nil {
		return EffortPolicySnapshot{}
	}
	policy.Raw = content
	policy.Lane = strings.TrimSpace(policy.Lane)
	policy.Level = strings.TrimSpace(policy.Level)
	policy.ConfidenceFloor = strings.TrimSpace(policy.ConfidenceFloor)
	policy.BudgetExhaustionMode = strings.TrimSpace(policy.BudgetExhaustionMode)
	return policy
}

func effortPolicyMatches(actual EffortPolicySnapshot, expected EffortPolicyExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Level != "" && !strings.EqualFold(actual.Level, expected.Level) {
		return false
	}
	if expected.MinAnalysisToolBudget != nil && actual.AnalysisToolBudget < *expected.MinAnalysisToolBudget {
		return false
	}
	if expected.MinImplementToolBudget != nil && actual.ImplementToolBudget < *expected.MinImplementToolBudget {
		return false
	}
	if expected.MinDeepeningPasses != nil && actual.DeepeningPasses < *expected.MinDeepeningPasses {
		return false
	}
	if expected.MinMaxOutputTokens != nil && actual.MaxOutputTokens < *expected.MinMaxOutputTokens {
		return false
	}
	if expected.ConfidenceFloor != "" && !strings.EqualFold(actual.ConfidenceFloor, expected.ConfidenceFloor) {
		return false
	}
	if expected.BudgetExhaustionMode != "" && !strings.EqualFold(actual.BudgetExhaustionMode, expected.BudgetExhaustionMode) {
		return false
	}
	return true
}

func parseLongHorizonState(content string) LongHorizonStateSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return LongHorizonStateSnapshot{}
	}
	var state LongHorizonStateSnapshot
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return LongHorizonStateSnapshot{}
	}
	state.Raw = content
	state.Action = strings.TrimSpace(state.Action)
	state.MemoryKind = strings.TrimSpace(state.MemoryKind)
	state.SourceLane = strings.TrimSpace(state.SourceLane)
	state.FocusPath = strings.TrimSpace(state.FocusPath)
	state.Changed = util.UniqueNonEmpty(state.Changed)
	state.Remaining = util.UniqueNonEmpty(state.Remaining)
	state.OpenGaps = util.UniqueNonEmpty(state.OpenGaps)
	state.AttemptedSearches = util.UniqueNonEmpty(state.AttemptedSearches)
	state.ChosenTargets = util.UniqueNonEmpty(state.ChosenTargets)
	state.VerificationHistory = util.UniqueNonEmpty(state.VerificationHistory)
	state.NextActions = util.UniqueNonEmpty(state.NextActions)
	state.NextStep = strings.TrimSpace(state.NextStep)
	state.Verification = strings.TrimSpace(state.Verification)
	state.Reason = strings.TrimSpace(state.Reason)
	return state
}

func longHorizonStateMatches(actual LongHorizonStateSnapshot, expected LongHorizonStateExpectation) bool {
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.MemoryKind != "" && !strings.EqualFold(actual.MemoryKind, expected.MemoryKind) {
		return false
	}
	if expected.SourceLane != "" && !strings.EqualFold(actual.SourceLane, expected.SourceLane) {
		return false
	}
	if expected.Stage != nil && actual.Stage != *expected.Stage {
		return false
	}
	if expected.FocusPath != "" && !strings.EqualFold(actual.FocusPath, expected.FocusPath) {
		return false
	}
	for _, required := range expected.RequireRemaining {
		if !containsStringFold(actual.Remaining, required) {
			return false
		}
	}
	for _, required := range expected.RequireOpenGaps {
		if !containsStringFold(actual.OpenGaps, required) {
			return false
		}
	}
	for _, required := range expected.RequireAttemptedSearches {
		if !containsStringFold(actual.AttemptedSearches, required) {
			return false
		}
	}
	for _, required := range expected.RequireChosenTargets {
		if !containsStringFold(actual.ChosenTargets, required) {
			return false
		}
	}
	for _, required := range expected.RequireVerificationHistory {
		if !containsStringFold(actual.VerificationHistory, required) {
			return false
		}
	}
	for _, required := range expected.RequireNextActions {
		if !containsStringFold(actual.NextActions, required) {
			return false
		}
	}
	next := strings.ToLower(actual.NextStep)
	if next == "" {
		next = strings.ToLower(strings.Join(actual.NextActions, "\n"))
	}
	for _, required := range expected.NextContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(next, required) {
			return false
		}
	}
	if expected.Resumable != nil && actual.Resumable != *expected.Resumable {
		return false
	}
	return true
}

func parseWorkflowState(content string) WorkflowStateSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return WorkflowStateSnapshot{}
	}
	var state WorkflowStateSnapshot
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return WorkflowStateSnapshot{}
	}
	state.Raw = content
	state.Action = strings.TrimSpace(state.Action)
	state.Kind = strings.TrimSpace(state.Kind)
	state.Phase = strings.TrimSpace(state.Phase)
	state.SourceLane = strings.TrimSpace(state.SourceLane)
	state.Objective = strings.TrimSpace(state.Objective)
	state.TargetRefs = util.UniqueNonEmpty(state.TargetRefs)
	state.EditableTargets = util.UniqueNonEmpty(state.EditableTargets)
	state.ReadOnlyEvidence = util.UniqueNonEmpty(state.ReadOnlyEvidence)
	state.EvidencePaths = util.UniqueNonEmpty(state.EvidencePaths)
	state.AllowedDestinations = util.UniqueNonEmpty(state.AllowedDestinations)
	state.Verification = util.UniqueNonEmpty(state.Verification)
	state.NextActions = util.UniqueNonEmpty(state.NextActions)
	state.Reason = strings.TrimSpace(state.Reason)
	return state
}

func workflowStateMatches(actual WorkflowStateSnapshot, expected WorkflowStateExpectation) bool {
	if expected.Action != "" && !strings.EqualFold(actual.Action, expected.Action) {
		return false
	}
	if expected.Kind != "" && !strings.EqualFold(actual.Kind, expected.Kind) {
		return false
	}
	if expected.Phase != "" && !strings.EqualFold(actual.Phase, expected.Phase) {
		return false
	}
	if expected.SourceLane != "" && !strings.EqualFold(actual.SourceLane, expected.SourceLane) {
		return false
	}
	for _, required := range expected.RequireTargetRefs {
		if !containsStringFold(actual.TargetRefs, required) {
			return false
		}
	}
	for _, required := range expected.RequireEditable {
		if !containsStringFold(actual.EditableTargets, required) {
			return false
		}
	}
	for _, required := range expected.RequireEvidence {
		if !containsStringFold(actual.EvidencePaths, required) {
			return false
		}
	}
	for _, required := range expected.RequireReadOnly {
		if !containsStringFold(actual.ReadOnlyEvidence, required) {
			return false
		}
	}
	for _, required := range expected.RequireDestinations {
		if !containsStringFold(actual.AllowedDestinations, required) {
			return false
		}
	}
	for _, required := range expected.RequireVerification {
		if !containsStringFold(actual.Verification, required) {
			return false
		}
	}
	for _, required := range expected.RequireNextActions {
		if !containsStringFold(actual.NextActions, required) {
			return false
		}
	}
	if expected.Resumable != nil && actual.Resumable != *expected.Resumable {
		return false
	}
	return true
}

func parseBudgetLedger(content string) BudgetLedgerSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return BudgetLedgerSnapshot{}
	}
	var ledger BudgetLedgerSnapshot
	if err := json.Unmarshal([]byte(content), &ledger); err != nil {
		return BudgetLedgerSnapshot{}
	}
	ledger.Raw = content
	ledger.Lane = strings.TrimSpace(ledger.Lane)
	ledger.Level = strings.TrimSpace(ledger.Level)
	ledger.StopReason = strings.TrimSpace(ledger.StopReason)
	ledger.NextAllowedActions = util.UniqueNonEmpty(ledger.NextAllowedActions)
	ledger.MarginalEvidencePolicy = strings.TrimSpace(ledger.MarginalEvidencePolicy)
	ledger.UserVisibleUncertainty = strings.TrimSpace(ledger.UserVisibleUncertainty)
	return ledger
}

func budgetLedgerMatches(actual BudgetLedgerSnapshot, expected BudgetLedgerExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.StopReason != "" && !strings.Contains(strings.ToLower(actual.StopReason), strings.ToLower(strings.TrimSpace(expected.StopReason))) {
		return false
	}
	if expected.MinModelCalls != nil && actual.UsedModelCalls < *expected.MinModelCalls {
		return false
	}
	if expected.MinTotalTokens != nil && actual.TotalTokens < *expected.MinTotalTokens {
		return false
	}
	if expected.MinEvidencePaths != nil && actual.TotalEvidencePaths < *expected.MinEvidencePaths {
		return false
	}
	if expected.BudgetExhausted != nil && actual.BudgetExhausted != *expected.BudgetExhausted {
		return false
	}
	if expected.UncertaintyPolicy != "" && !strings.Contains(strings.ToLower(actual.UserVisibleUncertainty), strings.ToLower(strings.TrimSpace(expected.UncertaintyPolicy))) {
		return false
	}
	return true
}

func parseEvidenceDelta(content string) EvidenceDeltaSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return EvidenceDeltaSnapshot{}
	}
	var delta EvidenceDeltaSnapshot
	if err := json.Unmarshal([]byte(content), &delta); err != nil {
		return EvidenceDeltaSnapshot{}
	}
	delta.Raw = content
	delta.Lane = strings.TrimSpace(delta.Lane)
	delta.NewEvidencePaths = util.UniqueNonEmpty(delta.NewEvidencePaths)
	delta.MarginalValue = strings.TrimSpace(delta.MarginalValue)
	return delta
}

func evidenceDeltaMatches(actual EvidenceDeltaSnapshot, expected EvidenceDeltaExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.MinNewPaths != nil && len(actual.NewEvidencePaths) < *expected.MinNewPaths {
		return false
	}
	if expected.MarginalValue != "" && !strings.EqualFold(actual.MarginalValue, expected.MarginalValue) {
		return false
	}
	if expected.MinToolResults != nil && actual.ToolResults < *expected.MinToolResults {
		return false
	}
	if expected.MaxFailedTools != nil && actual.FailedTools > *expected.MaxFailedTools {
		return false
	}
	return true
}

func parseClaimConfidence(content string) ClaimConfidenceSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ClaimConfidenceSnapshot{}
	}
	var confidence ClaimConfidenceSnapshot
	if err := json.Unmarshal([]byte(content), &confidence); err != nil {
		return ClaimConfidenceSnapshot{}
	}
	confidence.Raw = content
	confidence.Lane = strings.TrimSpace(confidence.Lane)
	confidence.Confidence = strings.TrimSpace(confidence.Confidence)
	confidence.EvidencePaths = util.UniqueNonEmpty(confidence.EvidencePaths)
	confidence.UnresolvedUncertainty = util.UniqueNonEmpty(confidence.UnresolvedUncertainty)
	confidence.Contradictions = util.UniqueNonEmpty(confidence.Contradictions)
	confidence.CalibrationBasis = strings.TrimSpace(confidence.CalibrationBasis)
	return confidence
}

func claimConfidenceMatches(actual ClaimConfidenceSnapshot, expected ClaimConfidenceExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	if expected.MinEvidencePaths != nil && len(actual.EvidencePaths) < *expected.MinEvidencePaths {
		return false
	}
	if expected.MaxUnresolved != nil && len(actual.UnresolvedUncertainty) > *expected.MaxUnresolved {
		return false
	}
	if expected.CalibrationBasis != "" && !strings.Contains(strings.ToLower(actual.CalibrationBasis), strings.ToLower(strings.TrimSpace(expected.CalibrationBasis))) {
		return false
	}
	return true
}

func parseContextCompaction(content string) ContextCompactionSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ContextCompactionSnapshot{}
	}
	var compaction ContextCompactionSnapshot
	if err := json.Unmarshal([]byte(content), &compaction); err != nil {
		return ContextCompactionSnapshot{}
	}
	compaction.Raw = content
	compaction.PinnedPaths = util.UniqueNonEmpty(compaction.PinnedPaths)
	compaction.PinnedMemoryKind = strings.TrimSpace(compaction.PinnedMemoryKind)
	compaction.Strategy = strings.TrimSpace(compaction.Strategy)
	return compaction
}

func contextCompactionMatches(actual ContextCompactionSnapshot, expected ContextCompactionExpectation) bool {
	if expected.MinDroppedMessages != nil && actual.DroppedMessages < *expected.MinDroppedMessages {
		return false
	}
	if expected.MinRetainedMessages != nil && actual.RetainedMessages < *expected.MinRetainedMessages {
		return false
	}
	if expected.MaxHistory != nil && actual.MaxHistory != *expected.MaxHistory {
		return false
	}
	for _, path := range expected.RequirePinnedPaths {
		if !containsStringFold(actual.PinnedPaths, path) {
			return false
		}
	}
	if expected.PinnedMemoryKind != "" && !strings.EqualFold(actual.PinnedMemoryKind, expected.PinnedMemoryKind) {
		return false
	}
	strategy := strings.ToLower(actual.Strategy)
	for _, required := range expected.StrategyContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(strategy, required) {
			return false
		}
	}
	return true
}

func parseRouteAmbiguity(content string) RouteAmbiguitySnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return RouteAmbiguitySnapshot{}
	}
	var ambiguity RouteAmbiguitySnapshot
	if err := json.Unmarshal([]byte(content), &ambiguity); err != nil {
		return RouteAmbiguitySnapshot{}
	}
	ambiguity.Raw = content
	ambiguity.SelectedLane = strings.TrimSpace(ambiguity.SelectedLane)
	ambiguity.Reason = strings.TrimSpace(ambiguity.Reason)
	ambiguity.Signals = util.UniqueNonEmpty(ambiguity.Signals)
	ambiguity.TieBreak = strings.TrimSpace(ambiguity.TieBreak)
	return ambiguity
}

func routeAmbiguityMatches(actual RouteAmbiguitySnapshot, expected RouteAmbiguityExpectation) bool {
	if expected.SelectedLane != "" && !strings.EqualFold(actual.SelectedLane, expected.SelectedLane) {
		return false
	}
	if expected.Ambiguous != nil && actual.Ambiguous != *expected.Ambiguous {
		return false
	}
	for _, signal := range expected.RequireSignals {
		if !containsStringFold(actual.Signals, signal) {
			return false
		}
	}
	tieBreak := strings.ToLower(actual.TieBreak)
	for _, required := range expected.TieBreakContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(tieBreak, required) {
			return false
		}
	}
	return true
}

func parseVerificationFeedback(content string) VerificationFeedbackSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return VerificationFeedbackSnapshot{}
	}
	var feedback VerificationFeedbackSnapshot
	if err := json.Unmarshal([]byte(content), &feedback); err != nil {
		return VerificationFeedbackSnapshot{}
	}
	feedback.Raw = content
	feedback.RiskLevel = strings.TrimSpace(feedback.RiskLevel)
	feedback.Coverage = strings.TrimSpace(feedback.Coverage)
	feedback.Confidence = strings.TrimSpace(feedback.Confidence)
	feedback.Commands = util.UniqueNonEmpty(feedback.Commands)
	feedback.FailedCommands = util.UniqueNonEmpty(feedback.FailedCommands)
	feedback.FailureClasses = util.UniqueNonEmpty(feedback.FailureClasses)
	feedback.LearnedRecommendation = strings.TrimSpace(feedback.LearnedRecommendation)
	return feedback
}

func verificationFeedbackMatches(actual VerificationFeedbackSnapshot, expected VerificationFeedbackExpectation) bool {
	if expected.Confidence != "" && !strings.EqualFold(actual.Confidence, expected.Confidence) {
		return false
	}
	for _, command := range expected.RequireFailedCommands {
		if !containsStringFold(actual.FailedCommands, command) {
			return false
		}
	}
	for _, class := range expected.RequireFailureClasses {
		if !containsStringFold(actual.FailureClasses, class) {
			return false
		}
	}
	recommendation := strings.ToLower(actual.LearnedRecommendation)
	for _, required := range expected.RecommendationContains {
		required = strings.ToLower(strings.TrimSpace(required))
		if required != "" && !strings.Contains(recommendation, required) {
			return false
		}
	}
	return true
}

func parseToolFailureConfidence(content string) ToolFailureConfidenceSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ToolFailureConfidenceSnapshot{}
	}
	var failure ToolFailureConfidenceSnapshot
	if err := json.Unmarshal([]byte(content), &failure); err != nil {
		return ToolFailureConfidenceSnapshot{}
	}
	failure.Raw = content
	failure.Lane = strings.TrimSpace(failure.Lane)
	failure.Tool = strings.TrimSpace(failure.Tool)
	failure.Action = strings.TrimSpace(failure.Action)
	failure.ConfidenceImpact = strings.TrimSpace(failure.ConfidenceImpact)
	failure.Reason = strings.TrimSpace(failure.Reason)
	return failure
}

func toolFailureConfidenceMatches(actual ToolFailureConfidenceSnapshot, expected ToolFailureConfidenceExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.Tool != "" && !strings.EqualFold(actual.Tool, expected.Tool) {
		return false
	}
	if expected.ConfidenceImpact != "" && !strings.EqualFold(actual.ConfidenceImpact, expected.ConfidenceImpact) {
		return false
	}
	return true
}

func parseControlRails(content string) ControlRailsSnapshot {
	content = strings.TrimSpace(content)
	if content == "" {
		return ControlRailsSnapshot{}
	}
	var rails ControlRailsSnapshot
	if err := json.Unmarshal([]byte(content), &rails); err != nil {
		return ControlRailsSnapshot{}
	}
	rails.Raw = content
	rails.Lane = strings.TrimSpace(rails.Lane)
	rails.FollowupLane = strings.TrimSpace(rails.FollowupLane)
	rails.Contract = strings.TrimSpace(rails.Contract)
	rails.AgentMode = strings.TrimSpace(rails.AgentMode)
	rails.WorkflowKind = strings.TrimSpace(rails.WorkflowKind)
	rails.WorkflowPhase = strings.TrimSpace(rails.WorkflowPhase)
	rails.TaskFamily = strings.TrimSpace(rails.TaskFamily)
	rails.PermissionProfile = strings.TrimSpace(rails.PermissionProfile)
	rails.TargetRefs = util.UniqueNonEmpty(rails.TargetRefs)
	rails.RequiredEditPaths = util.UniqueNonEmpty(rails.RequiredEditPaths)
	rails.RequiredEvidence = util.UniqueNonEmpty(rails.RequiredEvidence)
	rails.RequiredTools = util.UniqueNonEmpty(rails.RequiredTools)
	rails.AllowedTools = util.UniqueNonEmpty(rails.AllowedTools)
	rails.ForbiddenTools = util.UniqueNonEmpty(rails.ForbiddenTools)
	rails.ReadinessGate = util.UniqueNonEmpty(rails.ReadinessGate)
	rails.SearchPolicy = util.UniqueNonEmpty(rails.SearchPolicy)
	rails.VerificationContract = strings.TrimSpace(rails.VerificationContract)
	rails.StopConditions = util.UniqueNonEmpty(rails.StopConditions)
	rails.FinalCritic = util.UniqueNonEmpty(rails.FinalCritic)
	return rails
}

func controlRailsMatches(actual ControlRailsSnapshot, expected ControlRailsExpectation) bool {
	if expected.Lane != "" && !strings.EqualFold(actual.Lane, expected.Lane) {
		return false
	}
	if expected.FollowupLane != "" && !strings.EqualFold(actual.FollowupLane, expected.FollowupLane) {
		return false
	}
	if expected.Contract != "" && !strings.EqualFold(actual.Contract, expected.Contract) {
		return false
	}
	if expected.AgentMode != "" && !strings.EqualFold(actual.AgentMode, expected.AgentMode) {
		return false
	}
	if expected.WorkflowKind != "" && !strings.EqualFold(actual.WorkflowKind, expected.WorkflowKind) {
		return false
	}
	if expected.WorkflowPhase != "" && !strings.EqualFold(actual.WorkflowPhase, expected.WorkflowPhase) {
		return false
	}
	if expected.TaskFamily != "" && !strings.EqualFold(actual.TaskFamily, expected.TaskFamily) {
		return false
	}
	if expected.PermissionProfile != "" && !strings.EqualFold(actual.PermissionProfile, expected.PermissionProfile) {
		return false
	}
	for _, required := range expected.RequireTargetRefs {
		if !containsStringFold(actual.TargetRefs, required) {
			return false
		}
	}
	for _, required := range expected.RequireEditPaths {
		if !containsStringFold(actual.RequiredEditPaths, required) {
			return false
		}
	}
	for _, required := range expected.RequireEvidence {
		if !containsStringFold(actual.RequiredEvidence, required) {
			return false
		}
	}
	for _, required := range expected.RequireTools {
		if !containsStringFold(actual.RequiredTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireAllowedTools {
		if !containsStringFold(actual.AllowedTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireForbiddenTools {
		if !containsStringFold(actual.ForbiddenTools, required) {
			return false
		}
	}
	for _, required := range expected.RequireReadinessGate {
		if !containsStringFold(actual.ReadinessGate, required) {
			return false
		}
	}
	for _, required := range expected.RequireSearchPolicy {
		if !containsStringFold(actual.SearchPolicy, required) {
			return false
		}
	}
	if expected.VerificationContract != "" && !strings.EqualFold(actual.VerificationContract, expected.VerificationContract) {
		return false
	}
	for _, required := range expected.RequireStopConditions {
		if !containsStringFold(actual.StopConditions, required) {
			return false
		}
	}
	for _, required := range expected.RequireFinalCritic {
		if !containsStringFold(actual.FinalCritic, required) {
			return false
		}
	}
	if expected.DynamicInstructions != nil && actual.DynamicInstructions != *expected.DynamicInstructions {
		return false
	}
	if expected.FailureToEval != nil && actual.FailureToEval != *expected.FailureToEval {
		return false
	}
	return true
}

func summaryRejectionLane(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.Contains(role, "explore"):
		return "explore"
	case strings.Contains(role, "review"):
		return "review"
	case strings.Contains(role, "architecture"):
		return "architecture"
	case strings.Contains(role, "locate"):
		return "locate"
	default:
		return ""
	}
}

func parseLanguageCapabilities(content string) []LanguageCapabilitySnapshot {
	_, tail, ok := strings.Cut(content, "Language capability registry:")
	if !ok {
		return nil
	}
	caps := make([]LanguageCapabilitySnapshot, 0, 4)
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line == "" {
			continue
		}
		left := line
		signalsPart := ""
		if before, after, ok := strings.Cut(line, "|"); ok {
			left = strings.TrimSpace(before)
			signalsPart = strings.TrimSpace(after)
		}
		language := strings.TrimSpace(left)
		tools := []string(nil)
		if before, after, ok := strings.Cut(left, " tools:"); ok {
			language = strings.TrimSpace(before)
			tools = splitCommaList(after)
		}
		signals := []string(nil)
		if _, after, ok := strings.Cut(signalsPart, "signals:"); ok {
			signals = splitCommaList(after)
		}
		if language == "" {
			continue
		}
		caps = append(caps, LanguageCapabilitySnapshot{
			Language: language,
			Tools:    util.UniqueNonEmpty(tools),
			Signals:  util.UniqueNonEmpty(signals),
		})
	}
	return caps
}

func splitCommaList(text string) []string {
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func mergeLanguageCapabilities(existing []LanguageCapabilitySnapshot, incoming []LanguageCapabilitySnapshot) []LanguageCapabilitySnapshot {
	byLanguage := make(map[string]*LanguageCapabilitySnapshot)
	order := make([]string, 0, len(existing)+len(incoming))
	add := func(cap LanguageCapabilitySnapshot) {
		language := strings.TrimSpace(cap.Language)
		if language == "" {
			return
		}
		key := strings.ToLower(language)
		current, ok := byLanguage[key]
		if !ok {
			order = append(order, key)
			copy := LanguageCapabilitySnapshot{Language: language}
			byLanguage[key] = &copy
			current = &copy
		}
		current.Tools = util.UniqueNonEmpty(append(current.Tools, cap.Tools...))
		current.Signals = util.UniqueNonEmpty(append(current.Signals, cap.Signals...))
	}
	for _, cap := range existing {
		add(cap)
	}
	for _, cap := range incoming {
		add(cap)
	}
	slices.Sort(order)
	out := make([]LanguageCapabilitySnapshot, 0, len(order))
	for _, key := range order {
		cap := byLanguage[key]
		slices.Sort(cap.Tools)
		slices.Sort(cap.Signals)
		out = append(out, *cap)
	}
	return out
}

func languageCapabilityMatches(caps []LanguageCapabilitySnapshot, expected LanguageCapabilityExpectation) bool {
	language := strings.TrimSpace(expected.Language)
	tool := strings.TrimSpace(expected.Tool)
	signal := strings.TrimSpace(expected.Signal)
	for _, cap := range caps {
		if language != "" && !strings.EqualFold(cap.Language, language) {
			continue
		}
		if tool != "" && !containsStringFold(cap.Tools, tool) {
			continue
		}
		if signal != "" && !containsStringFold(cap.Signals, signal) {
			continue
		}
		return true
	}
	return false
}

func hasPhaseState(phases []PhaseSnapshot, lane string, phase string, state string) bool {
	return containsPhase(phases, PhaseExpectation{Lane: lane, Phase: phase, State: state})
}

func extractChangedFiles(text string) []string {
	lines := strings.Split(text, "\n")
	var changed []string
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inSection {
				break
			}
			continue
		}
		if strings.EqualFold(trimmed, "Changed files:") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		changed = append(changed, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}
	return util.UniqueNonEmpty(changed)
}

func evidencePathsFromText(text string) []string {
	paths := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "`'\"()[]{}<>,:;!?")
		candidate = strings.TrimSuffix(candidate, ".")
		if idx := strings.LastIndex(candidate, ":"); idx > 0 {
			suffix := candidate[idx+1:]
			allDigits := suffix != ""
			for _, r := range suffix {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				candidate = candidate[:idx]
			}
		}
		if !looksLikeEvidencePath(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

func evidencePathsFromJSONContent(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" || !strings.HasPrefix(content, "{") {
		return nil
	}
	var payload struct {
		EvidencePaths []string `json:"evidence_paths"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil
	}
	return util.UniqueNonEmpty(payload.EvidencePaths)
}

func looksLikeEvidencePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "://") {
		return false
	}
	if strings.HasSuffix(path, "/") {
		return false
	}
	if strings.Contains(path, "/") {
		return true
	}
	return strings.Contains(path, ".")
}
