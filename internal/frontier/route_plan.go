package frontier

import (
	"encoding/json"
	"strings"
)

type PromptPlan struct {
	Lane              Lane
	FollowupLane      Lane
	Thoroughness      string
	Reason            RouteReason
	Contract          AnswerContract
	TaskFamily        string
	PermissionProfile string
	AgentMode         string
	WorkflowKind      string
	WorkflowPhase     string
	Intent            IntentDecision
}

type PromptPlanInput struct {
	Prompt                     string
	WorkspaceEnabled           bool
	WorkspaceNormal            bool
	DefaultThoroughness        string
	Recent                     string
	HasRecentReplanToImplement bool
	AgentMode                  string
	Workflow                   PromptWorkflowState
}

type PromptWorkflowState struct {
	Active           bool
	Kind             string
	Phase            string
	HasApprovalScope bool
}

type RouteAmbiguitySnapshot struct {
	SelectedLane string   `json:"selected_lane,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Intent       string   `json:"intent,omitempty"`
	Risk         string   `json:"risk,omitempty"`
	Confidence   string   `json:"confidence,omitempty"`
	Signals      []string `json:"signals,omitempty"`
	Vetoes       []string `json:"vetoes,omitempty"`
	Ambiguous    bool     `json:"ambiguous"`
	TieBreak     string   `json:"tie_break,omitempty"`
}

type IntentVerifierTaskPromptInput struct {
	Prompt     string
	History    string
	Plan       PromptPlan
	Rails      ControlRailsSnapshot
	Reasons    []string
	TaskMemory string
}

type IntentReconciliationResult struct {
	Plan           PromptPlan
	Changed        bool
	SafetyOverride bool
	Notes          []string
}

func ClassifyPromptPlan(input PromptPlanInput) PromptPlan {
	if !input.WorkspaceEnabled || !input.WorkspaceNormal {
		return PromptPlan{}
	}
	profile := BuildRouteProfile(input.Prompt)
	lane, reason := InitialRoute(profile)
	followupLane := FollowupRoute(profile, lane)
	plan := PromptPlan{
		Lane:         lane,
		FollowupLane: followupLane,
		Reason:       reason,
		Contract:     AnswerContractForLane(lane),
	}
	plan.Intent = BuildIntentDecision(input.Prompt, profile, "", IntentPlanFromPromptPlan(plan))
	plan = ApplyPromptPlanIntentVetoes(plan, profile)
	plan.Intent = BuildIntentDecision(input.Prompt, profile, "", IntentPlanFromPromptPlan(plan))
	thoroughness := strings.TrimSpace(profile.Thoroughness)
	if LaneUsesThoroughness(plan.Lane) && thoroughness == "" {
		thoroughness = strings.TrimSpace(input.DefaultThoroughness)
	}
	plan.Thoroughness = thoroughness
	plan = FinalizePromptPlan(input.Prompt, profile, plan)
	if plan.Lane == LaneMain && plan.FollowupLane == "" {
		if contextual, ok := contextualPromptPlan(input); ok {
			plan = contextual
		}
	}
	if strings.TrimSpace(input.Recent) != "" {
		plan.Intent = BuildIntentDecision(input.Prompt, BuildRouteProfile(input.Prompt), input.Recent, IntentPlanFromPromptPlan(plan))
		plan = FinalizePromptPlan(input.Prompt, BuildRouteProfile(input.Prompt), plan)
	}
	plan = ApplyWorkflowStateToPromptPlan(input.Prompt, plan, input.Workflow)
	plan = ApplyAgentModeToPromptPlan(input.Prompt, plan, input.AgentMode, input.DefaultThoroughness)
	return plan
}

func contextualPromptPlan(input PromptPlanInput) (PromptPlan, bool) {
	decision, ok := ContextualRoute(ContextualRouteInput{
		Prompt:                     input.Prompt,
		Recent:                     input.Recent,
		DefaultThoroughness:        input.DefaultThoroughness,
		HasRecentReplanToImplement: input.HasRecentReplanToImplement,
	})
	if !ok {
		return PromptPlan{}, false
	}
	plan := PromptPlan{
		Lane:         decision.Plan.Lane,
		FollowupLane: decision.Plan.FollowupLane,
		Thoroughness: decision.Thoroughness,
		Reason:       decision.Plan.Reason,
		Contract:     AnswerContractForLane(decision.Plan.Lane),
	}
	plan.Intent = BuildIntentDecision(input.Prompt, BuildRouteProfile(input.Prompt), input.Recent, IntentPlanFromPromptPlan(plan))
	return FinalizePromptPlan(input.Prompt, BuildRouteProfile(input.Prompt), plan), true
}

func IntentPlanFromPromptPlan(plan PromptPlan) IntentPlan {
	return IntentPlan{
		Lane:         plan.Lane,
		FollowupLane: plan.FollowupLane,
		Reason:       plan.Reason,
	}
}

func ApplyPromptPlanIntentVetoes(plan PromptPlan, profile RouteProfile) PromptPlan {
	frontierPlan := ApplyIntentVetoes(IntentPlanFromPromptPlan(plan), profile)
	plan.Lane = frontierPlan.Lane
	plan.FollowupLane = frontierPlan.FollowupLane
	plan.Reason = frontierPlan.Reason
	return plan
}

func FinalizePromptPlan(prompt string, profile RouteProfile, plan PromptPlan) PromptPlan {
	plan.Contract = AnswerContractForLane(plan.Lane)
	plan.TaskFamily = ControlRailTaskFamily(prompt, profile, ControlRailPlan{
		Lane:         plan.Lane,
		FollowupLane: plan.FollowupLane,
		TaskFamily:   plan.TaskFamily,
	})
	plan.PermissionProfile = PermissionProfile(plan.TaskFamily, ControlRailPlan{
		Lane:              plan.Lane,
		FollowupLane:      plan.FollowupLane,
		PermissionProfile: plan.PermissionProfile,
	})
	if plan.Intent.SelectedLane != string(plan.Lane) || plan.Intent.FollowupLane != string(plan.FollowupLane) {
		plan.Intent.SelectedLane = string(plan.Lane)
		plan.Intent.FollowupLane = string(plan.FollowupLane)
	}
	if plan.Lane == LaneImplement && !plan.Intent.WantsPlanOnly && !plan.Intent.WantsVerify {
		plan.Intent.WantsEdits = true
	}
	if strings.TrimSpace(plan.AgentMode) == "" {
		plan.AgentMode = "auto"
	}
	plan.Intent.AgentMode = plan.AgentMode
	return plan
}

func ApplyWorkflowStateToPromptPlan(prompt string, plan PromptPlan, state PromptWorkflowState) PromptPlan {
	state.Kind = strings.TrimSpace(state.Kind)
	state.Phase = strings.TrimSpace(state.Phase)
	if !workflowStateResumable(state) {
		return plan
	}
	profile := BuildRouteProfile(prompt)
	if WorkflowApprovalDetected(prompt, state) {
		plan.Lane = LaneImplement
		plan.FollowupLane = ""
		plan.Reason = RouteReasonDirectImplementation
		plan.Contract = AnswerContractForLane(LaneImplement)
		plan.WorkflowKind = state.Kind
		plan.WorkflowPhase = "building"
		plan.Intent.WantsEdits = true
		plan.Intent.WantsPlanOnly = false
		plan.Intent.Vetoes = RemovePromptIntentVetoes(plan.Intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
		plan.Intent = BuildIntentDecision(prompt, profile, "", IntentPlanFromPromptPlan(plan))
		plan.Intent.WantsEdits = true
		plan.Intent.WantsPlanOnly = false
		plan.Intent.Vetoes = RemovePromptIntentVetoes(plan.Intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
		return FinalizePromptPlan(prompt, profile, plan)
	}
	if WorkflowPlanningFollowupPrompt(prompt, state) {
		plan.Lane = LaneArchitecture
		plan.FollowupLane = ""
		plan.Reason = RouteReasonArchitectureRequest
		plan.Contract = AnswerContractForLane(LaneArchitecture)
		plan.WorkflowKind = state.Kind
		plan.WorkflowPhase = "planning"
		plan.Intent.WantsEdits = false
		plan.Intent.WantsPlanOnly = true
		plan.Intent.Vetoes = UniqueNonEmptyStrings(append(plan.Intent.Vetoes, "workflow_plan_phase_blocks_edits"))
		plan.Intent = BuildIntentDecision(prompt, profile, "", IntentPlanFromPromptPlan(plan))
		plan.Intent.WantsEdits = false
		plan.Intent.WantsPlanOnly = true
		plan.Intent.Vetoes = UniqueNonEmptyStrings(append(plan.Intent.Vetoes, "workflow_plan_phase_blocks_edits"))
		return FinalizePromptPlan(prompt, profile, plan)
	}
	plan.WorkflowKind = state.Kind
	plan.WorkflowPhase = state.Phase
	return FinalizePromptPlan(prompt, profile, plan)
}

func workflowStateResumable(state PromptWorkflowState) bool {
	if !state.Active || strings.TrimSpace(state.Kind) == "" {
		return false
	}
	switch strings.TrimSpace(state.Phase) {
	case "", "complete":
		return false
	default:
		return true
	}
}

func WorkflowApprovalDetected(prompt string, state PromptWorkflowState) bool {
	if !workflowStateResumable(state) {
		return false
	}
	if strings.TrimSpace(state.Phase) != "awaiting_approval" {
		return false
	}
	if !state.HasApprovalScope {
		return false
	}
	return LooksLikeWorkflowApprovalPrompt(prompt)
}

func WorkflowPlanningFollowupPrompt(prompt string, state PromptWorkflowState) bool {
	if !workflowStateResumable(state) {
		return false
	}
	phase := strings.TrimSpace(state.Phase)
	if phase != "discovered" && phase != "planning" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return LooksLikePlanOnlyPrompt(normalized) &&
		ContainsAny(normalized, "refactor", "refactoring", "optimize", "optimization", "architecture", "architectural", "this file", "this one", "the chosen", "selected")
}

func ApplyAgentModeToPromptPlan(prompt string, plan PromptPlan, mode string, defaultThoroughness string) PromptPlan {
	mode = NormalizeAgentMode(mode)
	plan.AgentMode = mode
	if mode == "" {
		plan.AgentMode = "auto"
		mode = "auto"
	}
	if mode == "auto" || plan.Lane == "" {
		return FinalizePromptPlan(prompt, BuildRouteProfile(prompt), plan)
	}
	profile := BuildRouteProfile(prompt)
	switch mode {
	case "plan":
		plan.Lane = LaneArchitecture
		plan.FollowupLane = ""
		plan.Reason = RouteReasonArchitectureRequest
		plan.Contract = AnswerContractForLane(plan.Lane)
		plan.Intent.WantsPlanOnly = true
		plan.Intent.WantsEdits = false
		plan.Intent.Vetoes = UniqueNonEmptyStrings(append(plan.Intent.Vetoes, "agent_plan_mode_blocks_edits"))
	case "build":
		if profile.WantsVerificationOnly || profile.HasReadOnlyBoundary || plan.Intent.WantsPlanOnly {
			plan.Intent.Vetoes = UniqueNonEmptyStrings(append(plan.Intent.Vetoes, "agent_build_mode_preserved_user_read_only_boundary"))
			break
		}
		plan.Lane = LaneImplement
		plan.FollowupLane = ""
		plan.Reason = RouteReasonDirectImplementation
		plan.Contract = AnswerContractForLane(plan.Lane)
		plan.Intent.WantsEdits = true
		plan.Intent.WantsPlanOnly = false
	case "review":
		plan.Lane = LaneReview
		plan.FollowupLane = ""
		plan.Reason = RouteReasonReviewRequest
		plan.Contract = AnswerContractForLane(plan.Lane)
		plan.Intent.WantsEdits = false
	case "explore":
		plan.Lane = LaneExplore
		plan.FollowupLane = ""
		plan.Reason = RouteReasonExploreRequest
		plan.Contract = AnswerContractForLane(plan.Lane)
		plan.Intent.WantsEdits = false
	}
	if LaneUsesThoroughness(plan.Lane) && strings.TrimSpace(plan.Thoroughness) == "" {
		plan.Thoroughness = strings.TrimSpace(defaultThoroughness)
		if plan.Thoroughness == "" {
			plan.Thoroughness = "medium"
		}
	}
	plan.Intent.SelectedLane = string(plan.Lane)
	plan.Intent.FollowupLane = string(plan.FollowupLane)
	plan.Intent.AgentMode = mode
	source := strings.TrimSpace(plan.Intent.Source)
	if source == "" {
		source = "host_structured_intent"
	}
	plan.Intent.Source = source + "+agent_mode"
	return FinalizePromptPlan(prompt, profile, plan)
}

func NormalizeAgentMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plan", "build", "review", "explore":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func BuildRouteAmbiguitySnapshot(prompt string, plan PromptPlan) RouteAmbiguitySnapshot {
	profile := BuildRouteProfile(prompt)
	signals := append([]string(nil), plan.Intent.Signals...)
	if len(signals) == 0 {
		signals = IntentSignals(profile, profile.Normalized)
	}
	signals = RouteAmbiguitySignals(signals)
	ambiguous := CountRouteIntentSignals(signals) > 1
	tieBreak := ""
	if ambiguous {
		tieBreak = "ordered frontier route precedence selected " + string(plan.Lane)
	}
	return RouteAmbiguitySnapshot{
		SelectedLane: string(plan.Lane),
		Reason:       string(plan.Reason),
		Intent:       plan.Intent.Intent,
		Risk:         plan.Intent.Risk,
		Confidence:   plan.Intent.Confidence,
		Signals:      signals,
		Vetoes:       append([]string(nil), plan.Intent.Vetoes...),
		Ambiguous:    ambiguous,
		TieBreak:     tieBreak,
	}
}

func RouteAmbiguitySignals(signals []string) []string {
	out := make([]string, 0, len(signals))
	for _, signal := range signals {
		if signal == "implementation" {
			signal = "implement"
		}
		out = append(out, signal)
	}
	return UniqueNonEmptyStrings(out)
}

func PromptIntentVerifierReasons(plan PromptPlan, rails ControlRailsSnapshot) []string {
	reasons := make([]string, 0, 6)
	if plan.Intent.Confidence != "" && plan.Intent.Confidence != "high" {
		reasons = append(reasons, "host_intent_confidence_"+plan.Intent.Confidence)
	}
	if plan.Intent.Risk == "high" {
		reasons = append(reasons, "high_risk_intent")
	}
	if len(plan.Intent.UserBoundaries) > 0 {
		reasons = append(reasons, "user_boundary_present")
	}
	if len(plan.Intent.Vetoes) > 0 {
		reasons = append(reasons, "host_veto_present")
	}
	if CountRouteIntentSignals(plan.Intent.Signals) > 1 {
		reasons = append(reasons, "multiple_route_signals")
	}
	if rails.TaskFamily == "debug_logs" && plan.Lane == LaneImplement {
		reasons = append(reasons, "lane_task_family_conflict")
	}
	if rails.TaskFamily == "implementation" && !plan.Intent.WantsEdits && plan.Lane == LaneImplement {
		reasons = append(reasons, "implementation_without_edit_intent")
	}
	return UniqueNonEmptyStrings(reasons)
}

func CountRouteIntentSignals(signals []string) int {
	count := 0
	for _, signal := range UniqueNonEmptyStrings(signals) {
		switch signal {
		case "locate", "explore", "review", "architecture", "implementation", "implement", "verification_only", "debug_logs":
			count++
		}
	}
	return count
}

func BuildPromptIntentVerifierSystemPrompt() string {
	return strings.Join([]string{
		"You are a narrow route classifier for a coding-agent harness.",
		"Your job is semantic classification only. Do not solve the task. Do not call tools. Do not grant risky permission.",
		"Return exactly one JSON object and no markdown, prose, code fences, XML, or thinking text.",
		"Required JSON shape:",
		`{"primary_intent":"","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"","recommended_lane":"main","recommended_followup_lane":"","recommended_contract":"direct_answer","task_family":"","permission_profile":"","required_boundaries":[],"target_refs":[]}`,
		"Allowed primary_intent values: direct_answer, locate_code, explore_codebase, review_code, plan_architecture, implement, verify, debug_logs.",
		"Allowed recommended_lane values: main, locate, explore, review, architecture, implement.",
		"Allowed recommended_followup_lane values: implement or empty string.",
		"Allowed recommended_contract values: direct_answer, locate_result, exploration_result, review_findings, architecture_assessment, implementation_result.",
		"Allowed risk values: low, medium, high. Allowed confidence values: low, medium, high.",
		"Routing rules:",
		"- Classify by the requested deliverable and answer contract, not by the evidence-gathering method; needing to inspect or search files does not by itself make a request explore_codebase.",
		"- Maintainability, structural optimization, refactor opportunity, package-boundary, modularity, or long-term architecture prompts should use primary_intent=plan_architecture, recommended_lane=architecture, and recommended_contract=architecture_assessment unless the user asks only for repo inventory or file location.",
		"- Use explore_codebase for repo inventory, orientation, or factual codebase exploration where the requested deliverable is an exploration summary rather than an architecture recommendation or plan.",
		"- Plan-only requests must set wants_plan_only=true, wants_edits=false, and use a read-only lane such as architecture, explore, review, locate, or main.",
		"- Explicit edit/fix/update/implement requests must set wants_edits=true and recommended_lane=implement unless the user explicitly says not to edit.",
		"- Failure wording inside a fix request is context, not a reason to downgrade away from implementation.",
		"- Verification-only requests must set needs_verification=true and should not set wants_edits unless the user also asks to fix failures.",
		"- Latest-log/debug inspection requests without an edit request should use primary_intent=debug_logs and a read-only lane.",
		"- Durable-plan approvals such as do it, begin, proceed, go ahead, or commence must set approval_detected=true and recommended_lane=implement when active durable-plan memory is present.",
		"- User boundaries such as don't push, don't deploy, don't commit, wait for review, or ask before writing must be copied into required_boundaries.",
		"- Put mentioned file paths, symbols, packages, commands, or logs in target_refs when present. Do not invent refs or include generic nouns like project files.",
		"Examples:",
		`Prompt: "check the latest log and explain why it failed" -> {"primary_intent":"debug_logs","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for failure inspection, not edits.","recommended_lane":"main","recommended_followup_lane":"","recommended_contract":"direct_answer","task_family":"debug_logs","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":["latest log"]}`,
		`Prompt: "check the latest log and fix it" -> {"primary_intent":"implement","secondary_intents":["debug_logs"],"wants_edits":true,"wants_plan_only":false,"needs_verification":true,"approval_detected":false,"risk":"medium","confidence":"high","reason":"The user asks to inspect failure context and apply a fix.","recommended_lane":"implement","recommended_followup_lane":"","recommended_contract":"implementation_result","task_family":"implementation","permission_profile":"guarded_workspace_writes","required_boundaries":[],"target_refs":["latest log"]}`,
		`Prompt: "prepare a refactor plan for src/server/router.go" -> {"primary_intent":"plan_architecture","secondary_intents":[],"wants_edits":false,"wants_plan_only":true,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for a plan artifact only.","recommended_lane":"architecture","recommended_followup_lane":"","recommended_contract":"architecture_assessment","task_family":"planning","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":["src/server/router.go"]}`,
		`Prompt: "Can you identify an opportunity for us to optimize any of our project files for future maintainability?" -> {"primary_intent":"plan_architecture","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for a maintainability recommendation, not a repo inventory.","recommended_lane":"architecture","recommended_followup_lane":"","recommended_contract":"architecture_assessment","task_family":"architecture","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":[]}`,
		`Prompt: "look around this repo and explain what it contains" -> {"primary_intent":"explore_codebase","secondary_intents":[],"wants_edits":false,"wants_plan_only":false,"needs_verification":false,"approval_detected":false,"risk":"low","confidence":"high","reason":"The user asks for a general codebase orientation summary.","recommended_lane":"explore","recommended_followup_lane":"","recommended_contract":"exploration_result","task_family":"investigation","permission_profile":"read_only_evidence","required_boundaries":[],"target_refs":[]}`,
	}, "\n")
}

func BuildPromptIntentVerifierTaskPrompt(input IntentVerifierTaskPromptInput) string {
	route := map[string]any{
		"lane":          input.Plan.Lane,
		"followup_lane": input.Plan.FollowupLane,
		"reason":        input.Plan.Reason,
		"contract":      input.Plan.Contract,
		"intent":        input.Plan.Intent,
		"rails":         input.Rails,
		"reasons":       input.Reasons,
	}
	data, _ := json.Marshal(route)
	return strings.Join([]string{
		"Verify the prompt-routing decision below.",
		"Return exactly one JSON object with these keys: primary_intent, secondary_intents, wants_edits, wants_plan_only, needs_verification, approval_detected, risk, confidence, reason, recommended_lane, recommended_followup_lane, recommended_contract, task_family, permission_profile, required_boundaries, target_refs.",
		"If the host route is safe and semantically correct, return the same lane/contract with high confidence. If the user request is ambiguous, classify conservatively and preserve user-stated boundaries.",
		"Verifier focus:",
		"- Decide the lane from the user's requested deliverable. File inspection is evidence gathering, not the intent.",
		"- Preserve host architecture routes for maintainability, refactor, or optimization-opportunity prompts unless the host route is unsafe.",
		"Recent conversation excerpt:",
		truncatePromptPlanText(input.History, 1600),
		"Active task memory:",
		truncatePromptPlanText(input.TaskMemory, 1200),
		"User prompt:",
		strings.TrimSpace(input.Prompt),
		"Host route snapshot:",
		string(data),
	}, "\n\n")
}

func ParseIntentVerifierResult(reply string) (IntentVerifierResult, error) {
	payload := strings.TrimSpace(reply)
	if object, ok := extractFirstJSONObject(payload); ok {
		payload = object
	}
	var raw struct {
		PrimaryIntent       string          `json:"primary_intent,omitempty"`
		SecondaryIntents    json.RawMessage `json:"secondary_intents,omitempty"`
		WantsEdits          bool            `json:"wants_edits"`
		WantsPlanOnly       bool            `json:"wants_plan_only"`
		NeedsVerification   bool            `json:"needs_verification"`
		ApprovalDetected    bool            `json:"approval_detected"`
		Risk                string          `json:"risk,omitempty"`
		Confidence          string          `json:"confidence,omitempty"`
		Reason              string          `json:"reason,omitempty"`
		RecommendedLane     string          `json:"recommended_lane,omitempty"`
		RecommendedFollowup string          `json:"recommended_followup_lane,omitempty"`
		RecommendedContract string          `json:"recommended_contract,omitempty"`
		TaskFamily          string          `json:"task_family,omitempty"`
		PermissionProfile   string          `json:"permission_profile,omitempty"`
		RequiredBoundaries  json.RawMessage `json:"required_boundaries,omitempty"`
		TargetRefs          json.RawMessage `json:"target_refs,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return IntentVerifierResult{}, err
	}
	return IntentVerifierResult{
		PrimaryIntent:       raw.PrimaryIntent,
		SecondaryIntents:    decodeFlexibleStringSlice(raw.SecondaryIntents),
		WantsEdits:          raw.WantsEdits,
		WantsPlanOnly:       raw.WantsPlanOnly,
		NeedsVerification:   raw.NeedsVerification,
		ApprovalDetected:    raw.ApprovalDetected,
		Risk:                raw.Risk,
		Confidence:          raw.Confidence,
		Reason:              raw.Reason,
		RecommendedLane:     raw.RecommendedLane,
		RecommendedFollowup: raw.RecommendedFollowup,
		RecommendedContract: raw.RecommendedContract,
		TaskFamily:          raw.TaskFamily,
		PermissionProfile:   raw.PermissionProfile,
		RequiredBoundaries:  decodeFlexibleStringSlice(raw.RequiredBoundaries),
		TargetRefs:          decodeFlexibleStringSlice(raw.TargetRefs),
	}, nil
}

func NormalizeIntentVerifierResult(result IntentVerifierResult) IntentVerifierResult {
	result.PrimaryIntent = NormalizeIntentToken(result.PrimaryIntent)
	result.RecommendedLane = NormalizeIntentLane(result.RecommendedLane)
	result.RecommendedFollowup = NormalizeIntentLane(result.RecommendedFollowup)
	if result.RecommendedFollowup != string(LaneImplement) {
		result.RecommendedFollowup = ""
	}
	result.RecommendedContract = NormalizeRecommendedContract(result.RecommendedContract)
	result.Risk = NormalizeIntentRisk(result.Risk)
	result.Confidence = NormalizeIntentConfidence(result.Confidence)
	for i, intent := range result.SecondaryIntents {
		result.SecondaryIntents[i] = NormalizeIntentToken(intent)
	}
	result.SecondaryIntents = UniqueNonEmptyStrings(result.SecondaryIntents)
	result.RequiredBoundaries = NormalizeIntentBoundaries(result.RequiredBoundaries)
	result.TargetRefs = UniqueNormalizedPaths(result.TargetRefs)
	result.TaskFamily = NormalizeIntentToken(result.TaskFamily)
	result.PermissionProfile = NormalizeIntentToken(result.PermissionProfile)
	result.Reason = strings.TrimSpace(result.Reason)
	return result
}

func NormalizeRecommendedContract(contract string) string {
	contract = strings.TrimSpace(contract)
	switch AnswerContract(contract) {
	case AnswerContractDirectConversation, AnswerContractLocateTargets, AnswerContractGroundedExplanation, AnswerContractReviewFindings, AnswerContractArchitecture, AnswerContractImplementation:
		return contract
	default:
		return ""
	}
}

func ReconcilePromptPlanWithVerifier(prompt string, plan PromptPlan, verifier IntentVerifierResult, activeMemoryKind string) IntentReconciliationResult {
	original := plan
	notes := make([]string, 0, 4)
	safetyOverride := false
	hostProfile := BuildRouteProfile(prompt)
	durablePlanApproval := verifier.ApprovalDetected && strings.TrimSpace(activeMemoryKind) == "durable_plan"
	if durablePlanApproval {
		verifier.WantsEdits = true
		verifier.WantsPlanOnly = false
		if verifier.RecommendedLane == "" || verifier.RecommendedLane == string(LaneMain) {
			verifier.RecommendedLane = string(LaneImplement)
		}
		plan.Intent.WantsPlanOnly = false
		plan.Intent.Vetoes = RemovePromptIntentVetoes(plan.Intent.Vetoes, "plan_only_blocks_edits", "read_only_boundary_blocks_edits")
		notes = append(notes, "accepted classifier durable-plan approval signal")
	}
	hostBlocksEdits := plan.Intent.WantsPlanOnly || hostProfile.HasReadOnlyBoundary || hostProfile.WantsVerificationOnly
	if durablePlanApproval && !hostProfile.WantsVerificationOnly {
		hostBlocksEdits = false
	}
	if verifier.WantsPlanOnly {
		hostBlocksEdits = true
		plan.Intent.Vetoes = append(plan.Intent.Vetoes, "verifier_plan_only_blocks_edits")
	}
	supportedVerifierBoundaries := SupportedPromptVerifierBoundaries(prompt, plan.Intent.UserBoundaries, verifier.RequiredBoundaries)
	if len(supportedVerifierBoundaries) > 0 {
		plan.Intent.UserBoundaries = UniqueNonEmptyStrings(append(plan.Intent.UserBoundaries, supportedVerifierBoundaries...))
		for _, boundary := range supportedVerifierBoundaries {
			switch boundary {
			case "no_push":
				plan.Intent.Vetoes = append(plan.Intent.Vetoes, "verifier_no_push_boundary_blocks_git_push")
			case "no_deploy":
				plan.Intent.Vetoes = append(plan.Intent.Vetoes, "verifier_no_deploy_boundary_blocks_deploy")
			case "wait_for_review":
				plan.Intent.Vetoes = append(plan.Intent.Vetoes, "verifier_wait_for_review_blocks_external_release")
			}
		}
	}
	if len(verifier.RequiredBoundaries) > len(supportedVerifierBoundaries) {
		notes = append(notes, "ignored verifier boundary not supported by prompt or host context")
	}
	if verifier.Risk != "" {
		plan.Intent.Risk = verifier.Risk
	}
	if verifier.Confidence != "" {
		plan.Intent.Confidence = verifier.Confidence
	}
	if verifier.PrimaryIntent != "" {
		plan.Intent.Intent = verifier.PrimaryIntent
	}
	if verifier.NeedsVerification {
		plan.Intent.WantsVerify = true
	}
	if len(verifier.TargetRefs) > 0 && !ShouldIgnoreVerifierTargetRefsForPrompt(prompt, verifier.TargetRefs) {
		plan.Intent.TargetRefs = UniqueNormalizedPaths(append(plan.Intent.TargetRefs, verifier.TargetRefs...))
	} else if len(verifier.TargetRefs) > 0 {
		notes = append(notes, "ignored verifier target refs for open-ended opportunity discovery")
	}
	plan.Intent.Source = "frontier_structured_intent+model_verifier"
	plan.Intent.Vetoes = UniqueNonEmptyStrings(plan.Intent.Vetoes)
	recommendedLane, inferredLane := VerifierRecommendedLane(verifier)
	if recommendedLane != "" {
		recommended := recommendedLane
		if hostBlocksEdits && recommended == LaneImplement {
			safetyOverride = true
			notes = append(notes, "ignored verifier implementation recommendation because host/verifier veto blocks edits")
		} else if shouldPreserveHostArchitectureLane(original, verifier, recommended, hostProfile) {
			if plan.Intent.Intent == "explore_codebase" || plan.Intent.Intent == string(LaneExplore) {
				plan.Intent.Intent = "plan_architecture"
			}
			notes = append(notes, "preserved host architecture lane because verifier still identified architecture task family")
		} else if ShouldAcceptVerifierLane(original, verifier, recommended, hostProfile) {
			plan.Lane = recommended
			plan.Reason = RouteReasonForLane(recommended)
			note := "accepted verifier lane " + string(recommended)
			if inferredLane {
				note = "accepted verifier intent lane " + string(recommended)
			}
			notes = append(notes, note)
		}
	}
	if hostBlocksEdits {
		if plan.Lane == LaneImplement {
			plan.Lane = SafestReadOnlyLaneForIntent(hostProfile, verifier, plan)
			plan.Reason = RouteReasonForLane(plan.Lane)
			safetyOverride = true
			notes = append(notes, "downgraded implementation to read-only due to edit veto")
		} else if plan.Lane == LaneMain {
			if lane := SafestReadOnlyLaneForIntent(hostProfile, verifier, plan); lane != LaneMain {
				plan.Lane = lane
				plan.Reason = RouteReasonForLane(plan.Lane)
				notes = append(notes, "routed plan-only/read-only intent to "+string(lane))
			}
		}
		plan.FollowupLane = ""
	} else if verifier.RecommendedFollowup == string(LaneImplement) && plan.Lane != LaneImplement {
		plan.FollowupLane = LaneImplement
		notes = append(notes, "accepted verifier implementation follow-up")
	} else if verifier.WantsEdits && plan.Lane != LaneImplement && verifier.RecommendedLane == string(LaneImplement) {
		plan.Lane = LaneImplement
		plan.FollowupLane = ""
		plan.Reason = RouteReasonDirectImplementation
		notes = append(notes, "accepted verifier direct implementation")
	}
	plan.Contract = AnswerContractForLane(plan.Lane)
	plan.Intent.SelectedLane = string(plan.Lane)
	plan.Intent.FollowupLane = string(plan.FollowupLane)
	plan.Intent.WantsEdits = (original.Intent.WantsEdits || verifier.WantsEdits) && !hostBlocksEdits
	plan.Intent.WantsPlanOnly = plan.Intent.WantsPlanOnly || verifier.WantsPlanOnly
	if durablePlanApproval {
		plan.Intent.WantsPlanOnly = false
	}
	plan = FinalizePromptPlan(prompt, hostProfile, plan)
	changed := plan.Lane != original.Lane ||
		plan.FollowupLane != original.FollowupLane ||
		plan.TaskFamily != original.TaskFamily ||
		plan.PermissionProfile != original.PermissionProfile ||
		plan.Intent.WantsEdits != original.Intent.WantsEdits ||
		plan.Intent.WantsPlanOnly != original.Intent.WantsPlanOnly ||
		plan.Intent.WantsVerify != original.Intent.WantsVerify ||
		plan.Intent.Intent != original.Intent.Intent ||
		plan.Intent.Risk != original.Intent.Risk ||
		plan.Intent.Confidence != original.Intent.Confidence ||
		strings.Join(plan.Intent.TargetRefs, "\x00") != strings.Join(original.Intent.TargetRefs, "\x00") ||
		strings.Join(plan.Intent.UserBoundaries, "\x00") != strings.Join(original.Intent.UserBoundaries, "\x00") ||
		strings.Join(plan.Intent.Vetoes, "\x00") != strings.Join(original.Intent.Vetoes, "\x00")
	return IntentReconciliationResult{
		Plan:           plan,
		Changed:        changed,
		SafetyOverride: safetyOverride,
		Notes:          UniqueNonEmptyStrings(notes),
	}
}

func shouldPreserveHostArchitectureLane(plan PromptPlan, verifier IntentVerifierResult, recommended Lane, profile RouteProfile) bool {
	if plan.Lane != LaneArchitecture || !profile.WantsArchitecture {
		return false
	}
	if recommended == LaneArchitecture || recommended == LaneImplement {
		return false
	}
	if recommended == LaneExplore && !verifier.WantsEdits && verifierExploreOnlyIntent(verifier) {
		return true
	}
	return VerifierIndicatesArchitecture(verifier)
}

func verifierExploreOnlyIntent(verifier IntentVerifierResult) bool {
	switch NormalizeIntentToken(verifier.PrimaryIntent) {
	case "explore_codebase", "explore", "investigate", "investigation":
		return true
	}
	return NormalizeIntentToken(verifier.TaskFamily) == "explore_codebase" || NormalizeIntentToken(verifier.TaskFamily) == "investigation"
}

func VerifierIndicatesArchitecture(verifier IntentVerifierResult) bool {
	if AnswerContract(verifier.RecommendedContract) == AnswerContractArchitecture {
		return true
	}
	switch NormalizeIntentToken(verifier.TaskFamily) {
	case "architecture", "architectural", "planning", "plan_architecture":
		return true
	}
	for _, intent := range append([]string{verifier.PrimaryIntent}, verifier.SecondaryIntents...) {
		switch NormalizeIntentToken(intent) {
		case "plan_architecture", "architecture", "architectural":
			return true
		}
	}
	return false
}

func RemovePromptIntentVetoes(vetoes []string, remove ...string) []string {
	if len(vetoes) == 0 || len(remove) == 0 {
		return UniqueNonEmptyStrings(vetoes)
	}
	removeSet := make(map[string]struct{}, len(remove))
	for _, veto := range remove {
		removeSet[strings.TrimSpace(veto)] = struct{}{}
	}
	out := make([]string, 0, len(vetoes))
	for _, veto := range vetoes {
		veto = strings.TrimSpace(veto)
		if veto == "" {
			continue
		}
		if _, ok := removeSet[veto]; ok {
			continue
		}
		out = append(out, veto)
	}
	return UniqueNonEmptyStrings(out)
}

func VerifierRecommendedLane(verifier IntentVerifierResult) (Lane, bool) {
	if verifier.RecommendedLane != "" && verifier.RecommendedLane != string(LaneMain) {
		return Lane(verifier.RecommendedLane), false
	}
	if lane := ReadOnlyLaneForVerifierIntent(verifier); lane != "" {
		return lane, true
	}
	if verifier.RecommendedLane == string(LaneMain) {
		return LaneMain, false
	}
	return "", false
}

func SupportedPromptVerifierBoundaries(prompt string, existing []string, proposed []string) []string {
	supported := make(map[string]struct{})
	for _, boundary := range NormalizeIntentBoundaries(existing) {
		supported[boundary] = struct{}{}
	}
	for _, boundary := range UserBoundaries(prompt, "") {
		supported[boundary] = struct{}{}
	}
	out := make([]string, 0, len(proposed))
	for _, boundary := range NormalizeIntentBoundaries(proposed) {
		if _, ok := supported[boundary]; ok {
			out = append(out, boundary)
		}
	}
	return UniqueNonEmptyStrings(out)
}

func ShouldAcceptVerifierLane(plan PromptPlan, verifier IntentVerifierResult, recommended Lane, profile RouteProfile) bool {
	if !ValidRouteLane(recommended) {
		return false
	}
	if recommended == plan.Lane {
		return false
	}
	if verifier.Confidence == "low" {
		return false
	}
	if plan.Lane == LaneImplement && profile.WantsImplementation && !verifier.WantsPlanOnly && verifier.WantsEdits {
		return false
	}
	if recommended == LaneImplement {
		return verifier.WantsEdits
	}
	if plan.Lane == LaneMain && recommended != LaneMain && VerifierIntentSupportsLane(verifier, recommended) {
		return true
	}
	if plan.Intent.Confidence != "high" || CountRouteIntentSignals(plan.Intent.Signals) > 1 {
		return true
	}
	return false
}

func SafestReadOnlyLaneForIntent(profile RouteProfile, verifier IntentVerifierResult, plan PromptPlan) Lane {
	if profile.WantsArchitecture || VerifierIndicatesArchitecture(verifier) {
		return LaneArchitecture
	}
	if verifier.RecommendedLane != "" && verifier.RecommendedLane != string(LaneImplement) && verifier.RecommendedLane != string(LaneMain) {
		lane := Lane(verifier.RecommendedLane)
		if ValidRouteLane(lane) {
			return lane
		}
	}
	if lane := ReadOnlyLaneForVerifierIntent(verifier); lane != "" {
		return lane
	}
	switch {
	case profile.WantsReview:
		return LaneReview
	case profile.WantsDebugLogs || profile.WantsExploration:
		return LaneExplore
	case profile.WantsLocate:
		return LaneLocate
	case plan.Lane != LaneImplement && ValidRouteLane(plan.Lane):
		return plan.Lane
	default:
		return LaneMain
	}
}

func ReadOnlyLaneForVerifierIntent(verifier IntentVerifierResult) Lane {
	for _, intent := range append([]string{verifier.PrimaryIntent}, verifier.SecondaryIntents...) {
		switch NormalizeIntentToken(intent) {
		case "plan_architecture", "architecture", "architectural":
			return LaneArchitecture
		case "debug_logs", "debug", "investigate", "investigation", "explore":
			return LaneExplore
		case "review", "security_review", "performance_review":
			return LaneReview
		case "locate":
			return LaneLocate
		}
	}
	return ""
}

func VerifierIntentSupportsLane(verifier IntentVerifierResult, lane Lane) bool {
	return ReadOnlyLaneForVerifierIntent(verifier) == lane
}

func ValidRouteLane(lane Lane) bool {
	switch lane {
	case LaneMain, LaneLocate, LaneExplore, LaneReview, LaneArchitecture, LaneImplement:
		return true
	default:
		return false
	}
}

func NormalizeIntentLane(value string) string {
	value = NormalizeIntentToken(value)
	switch value {
	case "main", "direct", "conversation", "direct_answer":
		return string(LaneMain)
	case "locate":
		return string(LaneLocate)
	case "explore", "investigate", "debug_logs", "debug":
		return string(LaneExplore)
	case "review", "security_review":
		return string(LaneReview)
	case "architecture", "architectural", "plan_architecture":
		return string(LaneArchitecture)
	case "implement", "implementation", "edit", "fix":
		return string(LaneImplement)
	default:
		return ""
	}
}

func NormalizeIntentToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func NormalizeIntentRisk(value string) string {
	switch NormalizeIntentToken(value) {
	case "high":
		return "high"
	case "medium", "med":
		return "medium"
	default:
		return "low"
	}
}

func NormalizeIntentConfidence(value string) string {
	switch NormalizeIntentToken(value) {
	case "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

func NormalizeIntentBoundaries(boundaries []string) []string {
	normalized := make([]string, 0, len(boundaries))
	for _, boundary := range boundaries {
		switch NormalizeIntentToken(boundary) {
		case "no_push", "dont_push", "do_not_push":
			normalized = append(normalized, "no_push")
		case "no_deploy", "dont_deploy", "do_not_deploy":
			normalized = append(normalized, "no_deploy")
		case "wait_for_review", "review_required", "requires_review":
			normalized = append(normalized, "wait_for_review")
		}
	}
	return UniqueNonEmptyStrings(normalized)
}

func ShouldIgnoreVerifierTargetRefsForPrompt(prompt string, targetRefs []string) bool {
	if len(targetRefs) == 0 || len(PromptMentionedWorkspacePaths(prompt)) > 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if ContainsAny(normalized, "identify one bounded optimization", "identify one bounded", "optimization/refactor opportunity", "optimization opportunity", "optimization opportunities", "refactor opportunity", "refactor opportunities") &&
		ContainsAny(normalized, "explore", "discover", "identify") {
		return true
	}
	return ContainsAny(normalized, "opportunity", "opportunities", "recommend", "suggest") &&
		ContainsAny(normalized, "optimize", "optimization", "maintainability", "refactor", "future") &&
		ContainsAny(normalized, "any of", "any project", "project files", "codebase", "repo", "repository", "harness", "can you identify", "identify")
}

func extractFirstJSONObject(text string) (string, bool) {
	start := strings.Index(text, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}

func decodeFlexibleStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
		return []string{value}
	}
	return nil
}

func truncatePromptPlanText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
