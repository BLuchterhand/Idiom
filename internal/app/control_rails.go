package app

import (
	"encoding/json"
	"qwen-bubbles/internal/workspace"
	"strings"
)

type deterministicControlRailsSnapshot struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

type controlRailsEnforcementSnapshot struct {
	Lane       string   `json:"lane,omitempty"`
	TaskFamily string   `json:"task_family,omitempty"`
	Passed     bool     `json:"passed"`
	Action     string   `json:"action,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

func buildDeterministicControlRails(prompt string, plan promptExecutionPlan) deterministicControlRailsSnapshot {
	family := controlRailTaskFamily(prompt, plan)
	rails := deterministicControlRailsSnapshot{
		Lane:                 string(plan.lane),
		FollowupLane:         string(plan.followupLane),
		Contract:             string(promptPlanAnswerContract(plan)),
		TaskFamily:           family,
		PermissionProfile:    controlRailPermissionProfile(family, plan),
		RequiredEvidence:     controlRailRequiredEvidence(family, plan),
		RequiredTools:        controlRailRequiredTools(family, plan),
		ForbiddenTools:       controlRailForbiddenTools(family, plan),
		ReadinessGate:        controlRailReadinessGate(family, plan),
		SearchPolicy:         controlRailSearchPolicy(family, plan),
		VerificationContract: controlRailVerificationContract(plan),
		StopConditions:       controlRailStopConditions(family, plan),
		FinalCritic:          controlRailFinalCritic(plan),
		FailureToEval:        controlRailFailureToEval(prompt, family),
	}
	rails.RequiredEvidence = uniqueNonEmptyStrings(rails.RequiredEvidence)
	rails.RequiredTools = uniqueNonEmptyStrings(rails.RequiredTools)
	rails.ForbiddenTools = uniqueNonEmptyStrings(rails.ForbiddenTools)
	rails.ReadinessGate = uniqueNonEmptyStrings(rails.ReadinessGate)
	rails.SearchPolicy = uniqueNonEmptyStrings(rails.SearchPolicy)
	rails.StopConditions = uniqueNonEmptyStrings(rails.StopConditions)
	rails.FinalCritic = uniqueNonEmptyStrings(rails.FinalCritic)
	return rails
}

func controlRailTaskFamily(prompt string, plan promptExecutionPlan) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	switch {
	case plan.lane == analysisLaneImplement:
		return "implementation"
	case plan.lane == analysisLaneMain && looksLikeVerificationOnlyPrompt(normalized):
		return "verification_only"
	case plan.lane != analysisLaneImplement && looksLikeLogOrFailureInspectionPrompt(normalized):
		return "debug_logs"
	case plan.lane == analysisLaneLocate:
		return "locate"
	case plan.lane == analysisLaneReview:
		if containsAny(normalized, "security", "vulnerab", "prompt injection", "secret", "credential") {
			return "security_review"
		}
		return "review"
	case plan.lane == analysisLaneArchitecture:
		return "architecture"
	case plan.lane == analysisLaneExplore:
		return "investigation"
	case plan.lane == analysisLaneMain:
		return "direct_answer"
	default:
		return "unknown"
	}
}

func controlRailPermissionProfile(family string, plan promptExecutionPlan) string {
	if family == "verification_only" {
		return "read_only_verification_shell"
	}
	if plan.followupLane == analysisLaneImplement {
		return "analysis_read_only_then_guarded_writes"
	}
	switch plan.lane {
	case analysisLaneImplement:
		return "guarded_workspace_writes"
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return "read_only_workspace"
	case analysisLaneMain:
		return "conversation_only"
	default:
		return "none"
	}
}

func controlRailRequiredEvidence(family string, plan promptExecutionPlan) []string {
	switch family {
	case "verification_only":
		return []string{"verification_command", "pass_fail_status", "failure_output_if_any"}
	case "debug_logs":
		return []string{"log_record", "failure_output", "timestamp_or_latest_selection"}
	case "locate":
		return []string{"exact_workspace_path", "line_or_snippet", "target_match_reason"}
	case "review", "security_review":
		return []string{"candidate_path", "bounded_finding_evidence", "confidence"}
	case "architecture":
		return []string{"hotspot_path", "structural_shape", "boundary_rationale", "confidence"}
	case "implementation":
		return []string{"changed_files_or_noop_reason", "verification_result", "risk_contract"}
	case "investigation":
		return []string{"inspected_paths", "what_was_verified", "confidence", "next_validation_step"}
	case "direct_answer":
		return []string{"answer_addresses_prompt"}
	default:
		if plan.followupLane == analysisLaneImplement {
			return []string{"handoff_target", "implementation_preconditions"}
		}
		return nil
	}
}

func controlRailRequiredTools(family string, plan promptExecutionPlan) []string {
	tools := make([]string, 0, 4)
	switch family {
	case "verification_only":
		tools = append(tools, "shell")
	case "debug_logs":
		tools = append(tools, "log_inspection")
	case "locate":
		tools = append(tools, "search_content", "read_file")
	case "review", "security_review", "architecture", "investigation":
		tools = append(tools, "workspace_inspection")
	case "implementation":
		tools = append(tools, "workspace_edit", "verification")
	}
	if plan.followupLane == analysisLaneImplement {
		tools = append(tools, "implementation_handoff")
	}
	return tools
}

func controlRailForbiddenTools(family string, plan promptExecutionPlan) []string {
	switch plan.lane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		return []string{"edit_file", "write_file", "append_file", "mkdir", "mutating_shell"}
	case analysisLaneMain:
		if family == "verification_only" {
			return []string{"edit_file", "write_file", "append_file", "mkdir", "mutating_shell"}
		}
		return []string{"edit_file", "write_file", "append_file", "mkdir", "shell"}
	case analysisLaneImplement:
		return []string{"unbounded_write", "unapproved_protected_write"}
	default:
		return nil
	}
}

func controlRailReadinessGate(family string, plan promptExecutionPlan) []string {
	gates := []string{"answer_contract_satisfied", "required_evidence_present", "forbidden_tools_absent"}
	if family == "verification_only" {
		gates = append(gates, "verification_status_reported")
	}
	if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
		gates = append(gates, "target_packet_or_changed_files_present", "verification_contract_resolved")
	}
	if family == "debug_logs" {
		gates = append(gates, "latest_log_selected", "failure_cause_supported_by_log")
	}
	if analysisLaneUsesThoroughness(plan.lane) {
		gates = append(gates, "low_confidence_triggers_deepening")
	}
	return gates
}

func controlRailSearchPolicy(family string, plan promptExecutionPlan) []string {
	switch family {
	case "verification_only":
		return []string{"run_requested_check", "capture_failure_output", "do_not_edit"}
	case "debug_logs":
		return []string{"start_with_most_recent_log", "extract_failure_window", "broaden_only_if_cause_absent"}
	case "review", "security_review":
		return []string{"rank_candidates", "read_top_candidate", "deepen_if_no_bounded_finding"}
	case "architecture":
		return []string{"map_entrypoints", "rank_hotspots", "deepen_if_structural_claims_are_ungrounded"}
	case "investigation":
		return []string{"orient_once", "read_concrete_files", "broaden_after_failed_narrow_search"}
	case "locate":
		return []string{"literal_search_first", "fallback_pattern_search", "read_best_candidate"}
	case "implementation":
		return []string{"consume_target_packet_first", "avoid_broad_discovery_after_target", "verify_changed_surface"}
	default:
		if analysisLaneUsesThoroughness(plan.lane) {
			return []string{"deepen_until_ready_or_budget_exhausted"}
		}
		return nil
	}
}

func controlRailVerificationContract(plan promptExecutionPlan) string {
	if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
		return "tests_passed_or_failed_or_skipped_with_blocker"
	}
	return "not_required_for_read_only_lane"
}

func controlRailStopConditions(family string, plan promptExecutionPlan) []string {
	conditions := []string{"required_evidence_satisfied", "blocked_by_missing_permission_or_information", "budget_exhausted_with_uncertainty_declared"}
	if plan.lane == analysisLaneImplement || plan.followupLane == analysisLaneImplement {
		conditions = append(conditions, "implementation_verified_or_failure_reported")
	}
	if family == "debug_logs" {
		conditions = append(conditions, "log_cause_identified_or_log_gap_declared")
	}
	return conditions
}

func controlRailFinalCritic(plan promptExecutionPlan) []string {
	checks := []string{"answers_actual_user_request", "required_evidence_cited", "no_overconfident_claims"}
	switch plan.lane {
	case analysisLaneReview:
		checks = append(checks, "findings_first", "bounded_finding_or_no_finding_stated")
	case analysisLaneArchitecture:
		checks = append(checks, "structural_not_bug_triage", "hotspot_paths_present")
	case analysisLaneImplement:
		checks = append(checks, "changed_files_section", "verification_section")
	case analysisLaneLocate:
		checks = append(checks, "exact_target_or_no_match")
	}
	return checks
}

func controlRailPreExecute(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) *workspace.ToolResult {
	if !controlRailToolForbidden(rails, req) {
		return nil
	}
	return &workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Control rails blocked forbidden tool",
		Error:   "the deterministic control-rails contract for " + rails.TaskFamily + " forbids this tool before the task is rerouted or explicitly authorized.",
	}
}

func controlRailToolForbidden(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return false
	}
	for _, forbidden := range rails.ForbiddenTools {
		switch forbidden {
		case tool:
			return true
		case "mutating_shell":
			if tool == "shell" && !isGuardedImplementShellAllowed(req.Command) {
				return true
			}
		case "unbounded_write":
			if (tool == "write_file" || tool == "append_file" || tool == "mkdir") && strings.TrimSpace(req.Path) == "" {
				return true
			}
		}
	}
	return false
}

func controlRailsEnforcementForAnswer(rails deterministicControlRailsSnapshot, answer string, evidencePaths []string, changedPaths []string, verificationResolved bool) controlRailsEnforcementSnapshot {
	result := controlRailsEnforcementSnapshot{
		Lane:       rails.Lane,
		TaskFamily: rails.TaskFamily,
		Passed:     true,
		Action:     "accept",
	}
	addViolation := func(violation string) {
		result.Passed = false
		result.Action = "repair"
		result.Violations = append(result.Violations, violation)
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized == "" {
		addViolation("final_answer_empty")
	}
	if requiresEvidencePaths(rails) && len(evidencePaths) == 0 {
		addViolation("required_evidence_paths_missing")
	}
	if rails.TaskFamily == "verification_only" && !containsAny(normalized, "passed", "failed", "failure", "error", "not run", "skipped") {
		addViolation("verification_status_missing")
	}
	if rails.TaskFamily == "implementation" {
		if len(changedPaths) == 0 && !containsAny(normalized, "no changes", "no workspace changes", "no file edits") {
			addViolation("changed_files_or_noop_missing")
		}
		if !verificationResolved {
			addViolation("verification_contract_unresolved")
		}
	}
	result.Violations = uniqueNonEmptyStrings(result.Violations)
	return result
}

func requiresEvidencePaths(rails deterministicControlRailsSnapshot) bool {
	switch rails.TaskFamily {
	case "locate", "review", "security_review", "architecture", "investigation", "debug_logs":
		return true
	default:
		return false
	}
}

func (m *Model) applyControlRailsFinalAnswer(rails deterministicControlRailsSnapshot, answer string, evidencePaths []string, changedPaths []string, verificationResolved bool) string {
	enforcement := controlRailsEnforcementForAnswer(rails, answer, evidencePaths, changedPaths, verificationResolved)
	m.logControlRailsEnforcement(enforcement)
	if enforcement.Passed {
		return answer
	}
	return repairControlRailsAnswer(answer, enforcement)
}

func repairControlRailsAnswer(answer string, enforcement controlRailsEnforcementSnapshot) string {
	base := strings.TrimSpace(answer)
	if base == "" {
		base = "I cannot produce a complete answer under the deterministic control-rails contract."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nControl-rail gaps:\n")
	for _, violation := range enforcement.Violations {
		b.WriteString("- ")
		b.WriteString(violation)
		b.WriteString("\n")
	}
	b.WriteString("\nRequired next step:\n- Continue with the missing evidence, verification, or permission step before treating this as complete.")
	return strings.TrimSpace(b.String())
}

func controlRailFailureToEval(prompt string, family string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return family == "debug_logs" ||
		containsAny(normalized, "it failed", "this failed", "failed again", "regression", "why did this fail", "where else could we likely have bugs")
}

func looksLikeLogOrFailureInspectionPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	if containsAny(normalized, "latest log", "recent log", "most recent log", "check the logs", "read the logs", "logs?") {
		return true
	}
	if containsAny(normalized, "why did this fail", "why this failed", "it failed", "this failed", "failed again") {
		return true
	}
	return containsAny(normalized, "failure output", "stack trace", "traceback") &&
		containsAny(normalized, "check", "read", "inspect", "debug", "diagnose", "why")
}

func (m *Model) logDeterministicControlRails(rails deterministicControlRailsSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(rails)
	if err != nil {
		return
	}
	m.logMessage("control_rails", string(data))
}

func (m *Model) logControlRailsEnforcement(enforcement controlRailsEnforcementSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(enforcement)
	if err != nil {
		return
	}
	m.logMessage("control_rails_enforcement", string(data))
}

func looksLikeVerificationOnlyPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	if promptRequestsImplementation(normalized) {
		return false
	}
	hasVerificationVerb := containsAny(normalized, "run ", "verify", "check", "test", "report")
	hasVerificationTarget := containsAny(normalized, "test", "tests", "suite", "build", "lint", "check", "verification")
	return hasVerificationVerb && hasVerificationTarget
}
