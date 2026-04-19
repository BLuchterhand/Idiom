package app

import (
	"encoding/json"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/workspace"
	"strings"
)

type deterministicControlRailsSnapshot struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	Intent               string   `json:"intent,omitempty"`
	IntentConfidence     string   `json:"intent_confidence,omitempty"`
	Risk                 string   `json:"risk,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	UserBoundaries       []string `json:"user_boundaries,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
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

type dynamicToolPolicySnapshot struct {
	Lane           string   `json:"lane,omitempty"`
	TaskFamily     string   `json:"task_family,omitempty"`
	AgentMode      string   `json:"agent_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	ForbiddenTools []string `json:"forbidden_tools,omitempty"`
}

func renderDynamicToolPolicySnapshot(rails deterministicControlRailsSnapshot) string {
	payload := dynamicToolPolicySnapshot{
		Lane:           rails.Lane,
		TaskFamily:     rails.TaskFamily,
		AgentMode:      rails.AgentMode,
		AllowedTools:   append([]string(nil), rails.AllowedTools...),
		ForbiddenTools: append([]string(nil), rails.ForbiddenTools...),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildDeterministicControlRails(prompt string, plan promptExecutionPlan) deterministicControlRailsSnapshot {
	family := controlRailTaskFamily(prompt, plan)
	permissionProfile := plan.permissionProfile
	if permissionProfile == "" {
		permissionProfile = controlRailPermissionProfile(family, plan)
	}
	rails := deterministicControlRailsSnapshot{
		Lane:                 string(plan.lane),
		FollowupLane:         string(plan.followupLane),
		Contract:             string(promptPlanAnswerContract(plan)),
		AgentMode:            string(plan.agentMode),
		Intent:               plan.intent.Intent,
		IntentConfidence:     plan.intent.Confidence,
		Risk:                 plan.intent.Risk,
		TaskFamily:           family,
		PermissionProfile:    permissionProfile,
		UserBoundaries:       append([]string(nil), plan.intent.UserBoundaries...),
		TargetRefs:           append([]string(nil), plan.intent.TargetRefs...),
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
	rails.AllowedTools = controlRailAllowedTools(rails)
	rails.UserBoundaries = uniqueNonEmptyStrings(rails.UserBoundaries)
	rails.TargetRefs = uniqueNormalizedPaths(rails.TargetRefs)
	rails.ReadinessGate = uniqueNonEmptyStrings(rails.ReadinessGate)
	rails.SearchPolicy = uniqueNonEmptyStrings(rails.SearchPolicy)
	rails.StopConditions = uniqueNonEmptyStrings(rails.StopConditions)
	rails.FinalCritic = uniqueNonEmptyStrings(rails.FinalCritic)
	return rails
}

func controlRailTaskFamily(prompt string, plan promptExecutionPlan) string {
	if plan.taskFamily != "" {
		return plan.taskFamily
	}
	return canonicalPromptTaskFamily(buildPromptRouteProfile(prompt), plan)
}

func canonicalPromptTaskFamily(profile promptRouteProfile, plan promptExecutionPlan) string {
	normalized := strings.ToLower(strings.TrimSpace(profile.normalized))
	switch {
	case plan.lane == analysisLaneImplement:
		return "implementation"
	case plan.lane == analysisLaneMain && profile.wantsVerificationOnly:
		return "verification_only"
	case plan.lane != analysisLaneImplement && profile.wantsDebugLogs:
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

func controlRailAllowedTools(rails deterministicControlRailsSnapshot) []string {
	all := []string{
		"task_ledger",
		"task",
		"pwd",
		"list_files",
		"search_files",
		"search_content",
		"read_file",
		"go_overview",
		"go_find_symbol",
		"go_find_function",
		"go_find_type",
		"go_references",
		"go_definition",
		"go_implementations",
		"go_read_symbol",
		"go_extract_body",
		"go_file_outline",
		"go_move_declarations",
		"cpp_overview",
		"cpp_find_symbol",
		"cpp_references",
		"cpp_definition",
		"mkdir",
		"edit_file",
		"write_file",
		"append_file",
		"shell",
	}
	forbidden := make(map[string]struct{}, len(rails.ForbiddenTools))
	for _, tool := range rails.ForbiddenTools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		forbidden[tool] = struct{}{}
	}
	if rails.PermissionProfile == "conversation_only" {
		return nil
	}
	allowed := make([]string, 0, len(all))
	for _, tool := range all {
		if _, blocked := forbidden[tool]; blocked {
			continue
		}
		if _, blocked := forbidden["workspace_edit"]; blocked {
			switch tool {
			case "mkdir", "edit_file", "write_file", "append_file", "go_move_declarations":
				continue
			}
		}
		allowed = append(allowed, tool)
	}
	return uniqueNonEmptyStrings(allowed)
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
	if plan.intent.Confidence == "low" || plan.intent.Risk == "high" {
		gates = append(gates, "uncertainty_or_permission_boundary_declared")
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
	if plan.intent.Risk == "high" || len(plan.intent.UserBoundaries) > 0 {
		conditions = append(conditions, "high_risk_or_user_boundary_requires_explicit_permission")
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
	if controlRailBoundaryForbidsTool(rails, req) {
		return true
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

func controlRailBoundaryForbidsTool(rails deterministicControlRailsSnapshot, req workspace.ToolRequest) bool {
	command := strings.ToLower(strings.TrimSpace(req.Command))
	if strings.TrimSpace(req.Tool) != "shell" || command == "" {
		return false
	}
	for _, boundary := range rails.UserBoundaries {
		switch boundary {
		case "no_push":
			if containsAny(command, "git push", "gh pr merge", "gh release", "npm publish") {
				return true
			}
		case "no_deploy", "wait_for_review":
			if containsAny(command, "deploy", "release", "publish", "git push", "gh pr merge") {
				return true
			}
		}
	}
	return false
}

func controlRailRuntimeInstruction(rails deterministicControlRailsSnapshot) string {
	switch rails.TaskFamily {
	case "verification_only":
		return strings.TrimSpace(strings.Join([]string{
			"Deterministic completion contract:",
			"- This is a verification-only task.",
			"- Before the final answer, run one read-only shell verification command that directly checks the requested state.",
			"- The final answer must name the verification command and report pass/fail/skipped status.",
			"- Include failure output if the command fails; if verification cannot run, state the concrete blocker.",
			"- Do not edit files.",
		}, "\n"))
	default:
		return ""
	}
}

func controlRailCompletionGate(rails deterministicControlRailsSnapshot) agent.CompletionGate {
	switch rails.TaskFamily {
	case "verification_only":
		return func(input agent.CompletionGateInput) agent.CompletionGateResult {
			if !controlRailHasShellVerification(input.Results) {
				return agent.CompletionGateResult{
					Accept:  false,
					Summary: "Verification completion gate rejected final answer",
					Error:   "verification-only control rails require a read-only shell verification command before the final answer. Run the requested check now, then report the command and pass/fail/skipped status.",
				}
			}
			if !controlRailAnswerReportsVerificationStatus(input.Reply) {
				return agent.CompletionGateResult{
					Accept:  false,
					Summary: "Verification completion gate rejected final answer",
					Error:   "a shell verification command has run, but the final answer must explicitly report pass/fail/skipped status and include failure output if any.",
				}
			}
			return agent.CompletionGateResult{Accept: true}
		}
	default:
		return nil
	}
}

func controlRailHasShellVerification(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "shell" {
			continue
		}
		output := strings.TrimSpace(result.Output)
		if strings.Contains(output, "$ ") {
			return true
		}
	}
	return false
}

func controlRailAnswerReportsVerificationStatus(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	return containsAny(normalized, "pass", "passed", "fail", "failed", "failure", "error", "success", "succeeded", "not run", "skipped")
}

func controlRailPermissionPolicy(rails deterministicControlRailsSnapshot) agent.ToolPermissionPolicy {
	policy := agent.ToolPermissionPolicy{Default: agent.PermissionAllow}
	if strings.TrimSpace(rails.PermissionProfile) == "conversation_only" {
		policy.Default = agent.PermissionDeny
	}
	deny := func(tool string) {
		policy.Rules = append(policy.Rules, agent.ToolPermissionRule{
			Tool:     tool,
			Decision: agent.PermissionDeny,
		})
	}
	askShellPrefix := func(prefix string) {
		policy.Rules = append(policy.Rules, agent.ToolPermissionRule{
			Tool:          "shell",
			CommandPrefix: prefix,
			Decision:      agent.PermissionAsk,
		})
	}
	denyShellPrefix := func(prefix string) {
		policy.Rules = append(policy.Rules, agent.ToolPermissionRule{
			Tool:          "shell",
			CommandPrefix: prefix,
			Decision:      agent.PermissionDeny,
		})
	}
	for _, forbidden := range rails.ForbiddenTools {
		switch forbidden {
		case "edit_file", "write_file", "append_file", "mkdir", "shell":
			deny(forbidden)
		}
	}
	for _, boundary := range rails.UserBoundaries {
		switch boundary {
		case "no_push":
			denyShellPrefix("git push")
			denyShellPrefix("gh pr merge")
			denyShellPrefix("gh release")
			denyShellPrefix("npm publish")
		case "no_deploy", "wait_for_review":
			denyShellPrefix("git push")
			denyShellPrefix("gh pr merge")
			denyShellPrefix("deploy")
			denyShellPrefix("release")
			denyShellPrefix("publish")
		}
	}
	if rails.Risk == "high" {
		askShellPrefix("rm ")
		askShellPrefix("rm -")
		askShellPrefix("sudo ")
		askShellPrefix("curl ")
		askShellPrefix("wget ")
	}
	return policy
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
	if rails.TaskFamily == "verification_only" && !controlRailAnswerReportsVerificationStatus(answer) {
		addViolation("verification_status_missing")
	}
	if rails.TaskFamily == "architecture" {
		for _, target := range controlRailMissingTargetRefs(rails.TargetRefs, answer, evidencePaths) {
			addViolation("target_ref_missing_from_evidence_or_answer:" + target)
		}
		if containsAny(normalized, "did not complete cleanly", "final blocker", "budget_exhausted", "budget exhausted") &&
			!containsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addViolation("architecture_recommendations_missing_after_budget_failure")
		}
		if !containsAny(normalized, "structure", "architecture", "boundary", "package", "ownership", "hotspot") {
			addViolation("architecture_structural_framing_missing")
		}
		if !containsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addViolation("architecture_recommendation_or_hotspot_missing")
		}
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

func controlRailMissingTargetRefs(targetRefs []string, answer string, evidencePaths []string) []string {
	targetRefs = uniqueNormalizedPaths(targetRefs)
	if len(targetRefs) == 0 {
		return nil
	}
	answerLower := strings.ToLower(answer)
	evidenceSet := make(map[string]struct{}, len(evidencePaths))
	for _, path := range uniqueNormalizedPaths(evidencePaths) {
		evidenceSet[strings.ToLower(path)] = struct{}{}
	}
	missing := make([]string, 0, len(targetRefs))
	for _, target := range targetRefs {
		normalizedTarget := strings.ToLower(strings.TrimSpace(target))
		if normalizedTarget == "" {
			continue
		}
		if _, ok := evidenceSet[normalizedTarget]; ok {
			continue
		}
		if strings.Contains(answerLower, normalizedTarget) {
			continue
		}
		missing = append(missing, target)
	}
	return missing
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
	if looksLikeLogOrFailureInspectionPrompt(normalized) {
		return false
	}
	if promptRequestsImplementation(normalized) {
		return false
	}
	if containsAny(normalized, "add ", "create ", "write ", "update ", "implement ", "fix ", "patch ", "edit ") {
		return false
	}
	hasVerificationVerb := containsAny(normalized, "run ", "verify", "check", "test ", "report")
	hasVerificationTarget := containsAny(normalized, "test", "tests", "suite", "build", "lint", "check", "verification")
	return hasVerificationVerb && hasVerificationTarget
}
