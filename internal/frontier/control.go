package frontier

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier/workerruntime"
	"qwen-bubbles/internal/workspace"
)

type PromptProfile = RouteProfile

type ControlRailPlan struct {
	Lane              Lane
	FollowupLane      Lane
	Contract          AnswerContract
	TaskFamily        string
	PermissionProfile string
	AgentMode         string
	WorkflowKind      string
	WorkflowPhase     string
	Intent            IntentDecision
	UsesThoroughness  bool
}

type ControlRailInput struct {
	Prompt  string
	Plan    ControlRailPlan
	Profile PromptProfile
}

type WorkerRuntimeBriefInput struct {
	Objective        string
	Rails            ControlRailsSnapshot
	TargetPacket     TargetPacket
	TargetPacketText string
	VerificationPlan []string
	Effort           EffortPolicySnapshot
	RunBudget        workerruntime.BudgetBrief
	ExtraConstraints []string
	RequiredNext     []string
	InvalidActions   []string
}

func RenderDynamicToolPolicySnapshot(rails ControlRailsSnapshot) string {
	payload := DynamicToolPolicySnapshot{
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

func BuildWorkerRuntimeBrief(input WorkerRuntimeBriefInput) workerruntime.WorkerRuntimeBrief {
	rails := input.Rails
	targetPacket := input.TargetPacket
	targetRefs := append([]string(nil), rails.TargetRefs...)
	targetRefs = append(targetRefs, targetPacket.PrimaryPaths...)
	targetRefs = append(targetRefs, targetPacket.EvidencePaths...)
	var targetPacketLines []string
	switch {
	case strings.TrimSpace(input.TargetPacketText) != "":
		targetPacketLines = linesFromText(input.TargetPacketText)
	case targetPacketHasContent(targetPacket):
		targetPacketLines = linesFromText(RenderTargetPacket(targetPacket))
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:                 strings.TrimSpace(rails.Lane),
		TaskFamily:           strings.TrimSpace(rails.TaskFamily),
		Contract:             strings.TrimSpace(rails.Contract),
		Objective:            strings.TrimSpace(input.Objective),
		PermissionProfile:    strings.TrimSpace(rails.PermissionProfile),
		TargetRefs:           UniqueNormalizedPaths(targetRefs),
		EvidencePaths:        UniqueNormalizedPaths(targetPacket.EvidencePaths),
		UserBoundaries:       UniqueNonEmptyStrings(rails.UserBoundaries),
		AllowedTools:         UniqueNonEmptyStrings(rails.AllowedTools),
		ForbiddenTools:       UniqueNonEmptyStrings(rails.ForbiddenTools),
		RequiredEvidence:     UniqueNonEmptyStrings(rails.RequiredEvidence),
		RequiredTools:        UniqueNonEmptyStrings(rails.RequiredTools),
		ReadinessGate:        UniqueNonEmptyStrings(rails.ReadinessGate),
		SearchPolicy:         UniqueNonEmptyStrings(rails.SearchPolicy),
		VerificationContract: strings.TrimSpace(rails.VerificationContract),
		VerificationPlan:     UniqueNonEmptyStrings(input.VerificationPlan),
		StopConditions:       UniqueNonEmptyStrings(rails.StopConditions),
		FinalCritic:          UniqueNonEmptyStrings(rails.FinalCritic),
		TargetPacket:         targetPacketLines,
		Constraints:          UniqueNonEmptyStrings(input.ExtraConstraints),
		RequiredNext:         UniqueNonEmptyStrings(input.RequiredNext),
		InvalidActions:       UniqueNonEmptyStrings(input.InvalidActions),
		Budget:               input.RunBudget,
	}
	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps: input.Effort.MaxToolSteps,
		}
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func BuildControlRails(input ControlRailInput) ControlRailsSnapshot {
	family := ControlRailTaskFamily(input.Prompt, input.Profile, input.Plan)
	permissionProfile := input.Plan.PermissionProfile
	if permissionProfile == "" {
		permissionProfile = PermissionProfile(family, input.Plan)
	}
	contract := input.Plan.Contract
	if contract == "" {
		contract = AnswerContractForLane(input.Plan.Lane)
	}
	rails := ControlRailsSnapshot{
		Lane:                 string(input.Plan.Lane),
		FollowupLane:         string(input.Plan.FollowupLane),
		Contract:             string(contract),
		AgentMode:            input.Plan.AgentMode,
		WorkflowKind:         input.Plan.WorkflowKind,
		WorkflowPhase:        input.Plan.WorkflowPhase,
		Intent:               input.Plan.Intent.Intent,
		IntentConfidence:     input.Plan.Intent.Confidence,
		Risk:                 input.Plan.Intent.Risk,
		TaskFamily:           family,
		PermissionProfile:    permissionProfile,
		UserBoundaries:       append([]string(nil), input.Plan.Intent.UserBoundaries...),
		TargetRefs:           append([]string(nil), input.Plan.Intent.TargetRefs...),
		RequiredEvidence:     RequiredEvidence(family, input.Plan),
		RequiredTools:        RequiredTools(family, input.Plan),
		ForbiddenTools:       ForbiddenTools(family, input.Plan),
		ReadinessGate:        ReadinessGate(family, input.Plan),
		SearchPolicy:         SearchPolicy(family, input.Plan),
		VerificationContract: VerificationContract(input.Plan),
		StopConditions:       StopConditions(family, input.Plan),
		FinalCritic:          FinalCritic(input.Plan),
		FailureToEval:        FailureToEval(input.Prompt, family),
	}
	rails.RequiredEvidence = UniqueNonEmptyStrings(rails.RequiredEvidence)
	rails.RequiredTools = UniqueNonEmptyStrings(rails.RequiredTools)
	rails.ForbiddenTools = UniqueNonEmptyStrings(rails.ForbiddenTools)
	rails.AllowedTools = AllowedTools(rails)
	rails.UserBoundaries = UniqueNonEmptyStrings(rails.UserBoundaries)
	rails.TargetRefs = UniqueNormalizedPaths(rails.TargetRefs)
	rails.ReadinessGate = UniqueNonEmptyStrings(rails.ReadinessGate)
	rails.SearchPolicy = UniqueNonEmptyStrings(rails.SearchPolicy)
	rails.StopConditions = UniqueNonEmptyStrings(rails.StopConditions)
	rails.FinalCritic = UniqueNonEmptyStrings(rails.FinalCritic)
	return rails
}

func ControlRailTaskFamily(prompt string, profile PromptProfile, plan ControlRailPlan) string {
	if plan.TaskFamily != "" {
		return plan.TaskFamily
	}
	return CanonicalTaskFamily(profile, plan)
}

func CanonicalTaskFamily(profile PromptProfile, plan ControlRailPlan) string {
	normalized := strings.ToLower(strings.TrimSpace(profile.Normalized))
	switch {
	case plan.Lane == LaneImplement:
		return "implementation"
	case plan.Lane == LaneMain && profile.WantsVerificationOnly:
		return "verification_only"
	case plan.Lane != LaneImplement && profile.WantsDebugLogs:
		return "debug_logs"
	case plan.Lane == LaneLocate:
		return "locate"
	case plan.Lane == LaneReview:
		if ContainsAny(normalized, "security", "vulnerab", "prompt injection", "secret", "credential") {
			return "security_review"
		}
		return "review"
	case plan.Lane == LaneArchitecture:
		return "architecture"
	case plan.Lane == LaneExplore:
		if profile.WantsArchitecture {
			return "architecture"
		}
		return "investigation"
	case plan.Lane == LaneMain:
		return "direct_answer"
	default:
		return "unknown"
	}
}

func PermissionProfile(family string, plan ControlRailPlan) string {
	if family == "verification_only" {
		return "read_only_verification_shell"
	}
	if plan.FollowupLane == LaneImplement {
		return "analysis_read_only_then_guarded_writes"
	}
	switch plan.Lane {
	case LaneImplement:
		return "guarded_workspace_writes"
	case LaneLocate, LaneExplore, LaneReview, LaneArchitecture:
		return "read_only_workspace"
	case LaneMain:
		return "conversation_only"
	default:
		return "none"
	}
}

func RequiredEvidence(family string, plan ControlRailPlan) []string {
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
		if plan.FollowupLane == LaneImplement {
			return []string{"handoff_target", "implementation_preconditions"}
		}
		return nil
	}
}

func RequiredTools(family string, plan ControlRailPlan) []string {
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
	if plan.FollowupLane == LaneImplement {
		tools = append(tools, "implementation_handoff")
	}
	return tools
}

func AllowedTools(rails ControlRailsSnapshot) []string {
	all := workspace.ToolManifestTools(workspace.ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
	})
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
		if _, blocked := forbidden["workspace_edit"]; blocked && workspace.IsWorkspaceWriteTool(tool) {
			continue
		}
		allowed = append(allowed, tool)
	}
	return UniqueNonEmptyStrings(allowed)
}

func ForbiddenTools(family string, plan ControlRailPlan) []string {
	switch plan.Lane {
	case LaneLocate, LaneExplore, LaneReview, LaneArchitecture:
		return append(workspace.WorkspaceWriteToolNames(), "mutating_shell")
	case LaneMain:
		if family == "verification_only" {
			return append(workspace.WorkspaceWriteToolNames(), "mutating_shell")
		}
		return append(workspace.WorkspaceWriteToolNames(), "shell")
	case LaneImplement:
		return []string{"unbounded_write", "unapproved_protected_write"}
	default:
		return nil
	}
}

func ReadinessGate(family string, plan ControlRailPlan) []string {
	gates := []string{"answer_contract_satisfied", "required_evidence_present", "forbidden_tools_absent"}
	if family == "verification_only" {
		gates = append(gates, "verification_status_reported")
	}
	if plan.Lane == LaneImplement || plan.FollowupLane == LaneImplement {
		gates = append(gates, "target_packet_or_changed_files_present", "verification_contract_resolved")
	}
	if family == "debug_logs" {
		gates = append(gates, "latest_log_selected", "failure_cause_supported_by_log")
	}
	if plan.UsesThoroughness {
		gates = append(gates, "low_confidence_triggers_deepening")
	}
	if plan.Intent.Confidence == "low" || plan.Intent.Risk == "high" {
		gates = append(gates, "uncertainty_or_permission_boundary_declared")
	}
	return gates
}

func SearchPolicy(family string, plan ControlRailPlan) []string {
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
		if plan.UsesThoroughness {
			return []string{"deepen_until_ready_or_budget_exhausted"}
		}
		return nil
	}
}

func VerificationContract(plan ControlRailPlan) string {
	if plan.Lane == LaneImplement || plan.FollowupLane == LaneImplement {
		return "tests_passed_or_failed_or_skipped_with_blocker"
	}
	return "not_required_for_read_only_lane"
}

func StopConditions(family string, plan ControlRailPlan) []string {
	conditions := []string{"required_evidence_satisfied", "blocked_by_missing_permission_or_information", "budget_exhausted_with_uncertainty_declared"}
	if plan.Lane == LaneImplement || plan.FollowupLane == LaneImplement {
		conditions = append(conditions, "implementation_verified_or_failure_reported")
	}
	if family == "debug_logs" {
		conditions = append(conditions, "log_cause_identified_or_log_gap_declared")
	}
	if plan.Intent.Risk == "high" || len(plan.Intent.UserBoundaries) > 0 {
		conditions = append(conditions, "high_risk_or_user_boundary_requires_explicit_permission")
	}
	return conditions
}

func FinalCritic(plan ControlRailPlan) []string {
	checks := []string{"answers_actual_user_request", "required_evidence_cited", "no_overconfident_claims"}
	switch plan.Lane {
	case LaneReview:
		checks = append(checks, "findings_first", "bounded_finding_or_no_finding_stated")
	case LaneArchitecture:
		checks = append(checks, "structural_not_bug_triage", "hotspot_paths_present")
	case LaneImplement:
		checks = append(checks, "changed_files_section", "verification_section")
	case LaneLocate:
		checks = append(checks, "exact_target_or_no_match")
	}
	return checks
}

func PreExecute(rails ControlRailsSnapshot, req workspace.ToolRequest) *workspace.ToolResult {
	if !ToolForbidden(rails, req) {
		return nil
	}
	return &workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Control rails blocked forbidden tool",
		Error:   "the deterministic control-rails contract for " + rails.TaskFamily + " forbids this tool before the task is rerouted or explicitly authorized.",
	}
}

func ToolForbidden(rails ControlRailsSnapshot, req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return false
	}
	if BoundaryForbidsTool(rails, req) {
		return true
	}
	for _, forbidden := range rails.ForbiddenTools {
		switch forbidden {
		case tool:
			return true
		case "mutating_shell":
			if tool == "shell" && !IsGuardedImplementShellAllowed(req.Command) {
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

func BoundaryForbidsTool(rails ControlRailsSnapshot, req workspace.ToolRequest) bool {
	command := strings.ToLower(strings.TrimSpace(req.Command))
	if strings.TrimSpace(req.Tool) != "shell" || command == "" {
		return false
	}
	for _, boundary := range rails.UserBoundaries {
		switch boundary {
		case "no_push":
			if ContainsAny(command, "git push", "gh pr merge", "gh release", "npm publish") {
				return true
			}
		case "no_deploy", "wait_for_review":
			if ContainsAny(command, "deploy", "release", "publish", "git push", "gh pr merge") {
				return true
			}
		}
	}
	return false
}

func RuntimeInstruction(rails ControlRailsSnapshot) string {
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

func CompletionGate(rails ControlRailsSnapshot) agent.CompletionGate {
	switch rails.TaskFamily {
	case "verification_only":
		return func(input agent.CompletionGateInput) agent.CompletionGateResult {
			if !HasShellVerification(input.Results) {
				return agent.CompletionGateResult{
					Accept:  false,
					Summary: "Verification completion gate rejected final answer",
					Error:   "verification-only control rails require a read-only shell verification command before the final answer. Run the requested check now, then report the command and pass/fail/skipped status.",
				}
			}
			if !AnswerReportsVerificationStatus(input.Reply) {
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

func HasShellVerification(results []workspace.ToolResult) bool {
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

func AnswerReportsVerificationStatus(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	return ContainsAny(normalized, "pass", "passed", "fail", "failed", "failure", "error", "success", "succeeded", "not run", "skipped")
}

func PermissionPolicy(rails ControlRailsSnapshot) agent.ToolPermissionPolicy {
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
		if workspace.IsWorkspaceWriteTool(forbidden) || forbidden == "shell" {
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

func linesFromText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func targetPacketHasContent(packet TargetPacket) bool {
	return len(packet.PrimaryPaths) > 0 ||
		len(packet.Anchors) > 0 ||
		len(packet.EvidencePaths) > 0 ||
		len(packet.AnalogyPaths) > 0 ||
		len(packet.LiteralFragments) > 0 ||
		len(packet.LocationHints) > 0 ||
		len(packet.SymbolHints) > 0 ||
		len(packet.StrategyHints) > 0 ||
		packet.SourceLane != "" ||
		packet.RefactorStage > 0
}

func EnforcementForAnswer(rails ControlRailsSnapshot, answer string, evidencePaths []string, changedPaths []string, verificationResolved bool) ControlRailsEnforcementSnapshot {
	result := ControlRailsEnforcementSnapshot{
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
	if RequiresEvidencePaths(rails) && len(evidencePaths) == 0 {
		addViolation("required_evidence_paths_missing")
	}
	if rails.TaskFamily == "verification_only" && !AnswerReportsVerificationStatus(answer) {
		addViolation("verification_status_missing")
	}
	if rails.TaskFamily == "architecture" {
		for _, target := range MissingTargetRefs(rails.TargetRefs, answer, evidencePaths) {
			addViolation("target_ref_missing_from_evidence_or_answer:" + target)
		}
		if ContainsAny(normalized, "did not complete cleanly", "final blocker", "budget_exhausted", "budget exhausted") &&
			!ContainsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addViolation("architecture_recommendations_missing_after_budget_failure")
		}
		if !ContainsAny(normalized, "structure", "architecture", "boundary", "package", "ownership", "hotspot") {
			addViolation("architecture_structural_framing_missing")
		}
		if !ContainsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addViolation("architecture_recommendation_or_hotspot_missing")
		}
	}
	if rails.TaskFamily == "implementation" {
		if len(changedPaths) == 0 && !ContainsAny(normalized, "no changes", "no workspace changes", "no workspace edit", "no file edits", "no file edit") {
			addViolation("changed_files_or_noop_missing")
		}
		if !verificationResolved {
			addViolation("verification_contract_unresolved")
		}
	}
	result.Violations = UniqueNonEmptyStrings(result.Violations)
	return result
}

func MissingTargetRefs(targetRefs []string, answer string, evidencePaths []string) []string {
	targetRefs = UniqueNormalizedPaths(targetRefs)
	if len(targetRefs) == 0 {
		return nil
	}
	answerLower := strings.ToLower(answer)
	evidenceSet := make(map[string]struct{}, len(evidencePaths))
	for _, path := range UniqueNormalizedPaths(evidencePaths) {
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

func RequiresEvidencePaths(rails ControlRailsSnapshot) bool {
	switch rails.TaskFamily {
	case "locate", "review", "security_review", "architecture", "investigation", "debug_logs":
		return true
	default:
		return false
	}
}

func RepairAnswer(answer string, enforcement ControlRailsEnforcementSnapshot) string {
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

func FailureToEval(prompt string, family string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return family == "debug_logs" ||
		ContainsAny(normalized, "it failed", "this failed", "failed again", "regression", "why did this fail", "where else could we likely have bugs")
}

func IsGuardedImplementShellAllowed(command string) bool {
	segments := splitGuardedShellAndChain(command)
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !isSingleGuardedImplementShellAllowed(segment) {
			return false
		}
	}
	return true
}

func splitGuardedShellAndChain(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" || strings.Contains(command, ";") || strings.Contains(command, "||") || strings.Contains(command, "|") || strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return nil
	}
	rawSegments := strings.Split(command, "&&")
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		segment := strings.TrimSpace(raw)
		if segment == "" {
			return nil
		}
		segments = append(segments, segment)
	}
	return segments
}

func isSingleGuardedImplementShellAllowed(command string) bool {
	command = stripAllowedShellStderrRedirect(strings.TrimSpace(command))
	if command == "" || strings.ContainsAny(command, "<>") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "cd":
		return len(fields) == 2 && (fields[1] == "." || fields[1] == "./")
	case "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "ls", "find":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "diff", "show", "grep", "log":
			return true
		default:
			return false
		}
	case "go":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "vet", "list":
			return true
		case "build":
			return guardedGoBuildIsReadOnly(fields[2:])
		default:
			return false
		}
	case "cargo":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "check", "build", "fmt", "clippy":
			return true
		default:
			return false
		}
	case "pytest":
		return true
	case "make":
		if len(fields) < 2 {
			return false
		}
		return ContainsAny(strings.ToLower(fields[1]), "test", "build", "lint", "check", "verify")
	case "npm", "pnpm", "yarn":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "lint", "build", "check":
			return true
		case "run":
			if len(fields) < 3 {
				return false
			}
			return ContainsAny(strings.ToLower(fields[2]), "test", "lint", "build", "check", "verify")
		default:
			return false
		}
	case "uv":
		return strings.Contains(strings.ToLower(command), "test") || strings.Contains(strings.ToLower(command), "pytest")
	default:
		return false
	}
}

func stripAllowedShellStderrRedirect(command string) string {
	for {
		trimmed := strings.TrimSpace(command)
		if !strings.HasSuffix(trimmed, "2>&1") {
			return trimmed
		}
		command = strings.TrimSpace(strings.TrimSuffix(trimmed, "2>&1"))
	}
}

func guardedGoBuildIsReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	hasPackageArg := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" || strings.HasPrefix(arg, "-o=") {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		normalized := strings.TrimRight(arg, "/")
		if normalized == "./..." || strings.HasSuffix(normalized, "/...") ||
			strings.HasPrefix(normalized, "./internal/") || strings.HasPrefix(normalized, "internal/") {
			hasPackageArg = true
			continue
		}
		return false
	}
	return hasPackageArg
}

func ContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func UniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func UniqueNormalizedPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "./"))
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func DeniedToolResult(tool string, target string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    tool,
		Summary: "Control rails blocked forbidden tool",
		Error:   fmt.Sprintf("the deterministic control-rails contract forbids %s on %s before the task is rerouted or explicitly authorized.", tool, target),
	}
}
