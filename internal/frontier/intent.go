package frontier

import "strings"

type IntentDecision struct {
	Intent         string   `json:"intent,omitempty"`
	SelectedLane   string   `json:"selected_lane,omitempty"`
	FollowupLane   string   `json:"followup_lane,omitempty"`
	WantsEdits     bool     `json:"wants_edits"`
	WantsPlanOnly  bool     `json:"wants_plan_only"`
	WantsVerify    bool     `json:"wants_verify"`
	Risk           string   `json:"risk,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
	Signals        []string `json:"signals,omitempty"`
	Vetoes         []string `json:"vetoes,omitempty"`
	UserBoundaries []string `json:"user_boundaries,omitempty"`
	TargetRefs     []string `json:"target_refs,omitempty"`
	AgentMode      string   `json:"agent_mode,omitempty"`
	Fallback       string   `json:"fallback,omitempty"`
	Source         string   `json:"source,omitempty"`
}

type IntentVerifierResult struct {
	PrimaryIntent       string   `json:"primary_intent,omitempty"`
	SecondaryIntents    []string `json:"secondary_intents,omitempty"`
	WantsEdits          bool     `json:"wants_edits"`
	WantsPlanOnly       bool     `json:"wants_plan_only"`
	NeedsVerification   bool     `json:"needs_verification"`
	ApprovalDetected    bool     `json:"approval_detected"`
	Risk                string   `json:"risk,omitempty"`
	Confidence          string   `json:"confidence,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	RecommendedLane     string   `json:"recommended_lane,omitempty"`
	RecommendedFollowup string   `json:"recommended_followup_lane,omitempty"`
	RecommendedContract string   `json:"recommended_contract,omitempty"`
	TaskFamily          string   `json:"task_family,omitempty"`
	PermissionProfile   string   `json:"permission_profile,omitempty"`
	RequiredBoundaries  []string `json:"required_boundaries,omitempty"`
	TargetRefs          []string `json:"target_refs,omitempty"`
}

type IntentReconciliationSnapshot struct {
	Triggered        bool     `json:"triggered"`
	Reasons          []string `json:"reasons,omitempty"`
	HostLane         string   `json:"host_lane,omitempty"`
	HostFollowupLane string   `json:"host_followup_lane,omitempty"`
	VerifierLane     string   `json:"verifier_lane,omitempty"`
	VerifierFollowup string   `json:"verifier_followup_lane,omitempty"`
	FinalLane        string   `json:"final_lane,omitempty"`
	FinalFollowup    string   `json:"final_followup_lane,omitempty"`
	Changed          bool     `json:"changed"`
	SafetyOverride   bool     `json:"safety_override"`
	Notes            []string `json:"notes,omitempty"`
}

type IntentPlan struct {
	Lane         Lane
	FollowupLane Lane
	Reason       RouteReason
}

func BuildIntentDecision(prompt string, profile RouteProfile, recent string, plan IntentPlan) IntentDecision {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	signals := IntentSignals(profile, normalized)
	boundaries := UserBoundaries(normalized, recent)
	vetoes := make([]string, 0, 4)
	if profile.WantsPlanOnly {
		vetoes = append(vetoes, "plan_only_blocks_edits")
	}
	if profile.HasReadOnlyBoundary {
		vetoes = append(vetoes, "read_only_boundary_blocks_edits")
	}
	if profile.WantsVerificationOnly {
		vetoes = append(vetoes, "verification_only_blocks_edits")
	}
	for _, boundary := range boundaries {
		switch boundary {
		case "no_push":
			vetoes = append(vetoes, "no_push_boundary_blocks_git_push")
		case "no_deploy":
			vetoes = append(vetoes, "no_deploy_boundary_blocks_deploy")
		case "wait_for_review":
			vetoes = append(vetoes, "wait_for_review_blocks_external_release")
		}
	}
	intent := IntentForPlan(profile, plan, normalized)
	risk := IntentRisk(normalized, profile, boundaries)
	confidence := IntentConfidence(profile, plan, signals, vetoes)
	fallback := ""
	if confidence == "low" || len(vetoes) > 0 {
		fallback = "prefer the safest read-only lane or ask for explicit authorization before mutating state"
	}
	return IntentDecision{
		Intent:         intent,
		SelectedLane:   string(plan.Lane),
		FollowupLane:   string(plan.FollowupLane),
		WantsEdits:     ProfileWantsEditIntent(profile) && !profile.WantsPlanOnly && !profile.HasReadOnlyBoundary && !profile.WantsVerificationOnly,
		WantsPlanOnly:  profile.WantsPlanOnly,
		WantsVerify:    profile.WantsVerificationOnly,
		Risk:           risk,
		Confidence:     confidence,
		Signals:        UniqueNonEmptyStrings(signals),
		Vetoes:         UniqueNonEmptyStrings(vetoes),
		UserBoundaries: UniqueNonEmptyStrings(boundaries),
		TargetRefs:     UniqueNormalizedPaths(PromptMentionedWorkspacePaths(prompt)),
		Fallback:       fallback,
		Source:         "frontier_structured_intent",
	}
}

func ApplyIntentVetoes(plan IntentPlan, profile RouteProfile) IntentPlan {
	if profile.WantsVerificationOnly {
		plan.Lane = LaneMain
		plan.FollowupLane = ""
		plan.Reason = RouteReasonDirectConversation
		return plan
	}
	if profile.WantsPlanOnly || profile.HasReadOnlyBoundary {
		plan.FollowupLane = ""
		if plan.Lane == LaneImplement {
			switch {
			case profile.WantsArchitecture || ContainsAny(profile.Normalized, "architecture", "architectural", "structure", "boundary", "refactor", "split"):
				plan.Lane = LaneArchitecture
				plan.Reason = RouteReasonArchitectureRequest
			case profile.WantsReview:
				plan.Lane = LaneReview
				plan.Reason = RouteReasonReviewRequest
			case profile.WantsExploration || profile.HasConcreteTarget:
				plan.Lane = LaneExplore
				plan.Reason = RouteReasonExploreRequest
			default:
				plan.Lane = LaneMain
				plan.Reason = RouteReasonDirectConversation
			}
		}
		return plan
	}
	if profile.WantsDebugLogs && !profile.WantsImplementation {
		plan.Lane = LaneExplore
		plan.FollowupLane = ""
		plan.Reason = RouteReasonExploreRequest
	}
	return plan
}

func IntentSignals(profile RouteProfile, normalized string) []string {
	signals := make([]string, 0, 10)
	if profile.WantsLocate {
		signals = append(signals, "locate")
	}
	if profile.WantsExploration {
		signals = append(signals, "explore")
	}
	if profile.WantsReview {
		signals = append(signals, "review")
	}
	if profile.WantsArchitecture {
		signals = append(signals, "architecture")
	}
	if ProfileWantsEditIntent(profile) {
		signals = append(signals, "implementation")
	}
	if profile.WantsPlanOnly {
		signals = append(signals, "plan_only")
	}
	if profile.WantsVerificationOnly {
		signals = append(signals, "verification_only")
	}
	if profile.WantsDebugLogs {
		signals = append(signals, "debug_logs")
	}
	if profile.HasConcreteTarget {
		signals = append(signals, "concrete_target")
	}
	if profile.HasReadOnlyBoundary {
		signals = append(signals, "read_only_boundary")
	}
	if ContainsAny(normalized, "push", "deploy", "delete", "remove", "rm -rf", "credential", "secret", ".env") {
		signals = append(signals, "risk_signal")
	}
	return signals
}

func IntentForPlan(profile RouteProfile, plan IntentPlan, normalized string) string {
	switch {
	case profile.WantsVerificationOnly:
		return "verification_only"
	case profile.WantsDebugLogs && !profile.WantsImplementation:
		return "debug_logs"
	case profile.WantsPlanOnly:
		if ContainsAny(normalized, "architecture", "architectural", "structure", "boundary", "refactor", "split") {
			return "plan_architecture"
		}
		return "plan_only"
	case plan.FollowupLane == LaneImplement:
		return string(plan.Lane) + "_then_implement"
	case plan.Lane == LaneImplement:
		return "implement"
	case plan.Lane != "":
		return string(plan.Lane)
	default:
		return "direct_answer"
	}
}

func IntentRisk(normalized string, profile RouteProfile, boundaries []string) string {
	switch {
	case ContainsAny(normalized, "rm -rf", "delete all", "wipe", "drop database", "production", "deploy", "credential", "secret", ".env", "git push", "push to"):
		return "high"
	case len(boundaries) > 0:
		return "medium"
	case ProfileWantsEditIntent(profile):
		return "medium"
	case profile.WantsVerificationOnly:
		return "low"
	default:
		return "low"
	}
}

func IntentConfidence(profile RouteProfile, plan IntentPlan, signals []string, vetoes []string) string {
	strongSignals := 0
	for _, signal := range signals {
		switch signal {
		case "locate", "explore", "review", "architecture", "implementation", "verification_only", "debug_logs":
			strongSignals++
		}
	}
	if len(vetoes) > 0 && plan.FollowupLane == LaneImplement {
		return "low"
	}
	if profile.WantsPlanOnly && profile.WantsImplementation {
		return "medium"
	}
	if strongSignals > 2 {
		return "medium"
	}
	if plan.Lane == LaneImplement && !profile.HasConcreteTarget && profile.WantsExploration {
		return "low"
	}
	return "high"
}

func UserBoundaries(normalized string, recent string) []string {
	combined := strings.ToLower(strings.TrimSpace(normalized + "\n" + recent))
	boundaries := make([]string, 0, 4)
	if ContainsAny(combined, "don't push", "do not push", "no push", "wait to push", "before pushing") {
		boundaries = append(boundaries, "no_push")
	}
	if ContainsAny(combined, "don't deploy", "do not deploy", "no deploy", "wait to deploy", "before deploying") {
		boundaries = append(boundaries, "no_deploy")
	}
	if ContainsAny(combined, "wait until i review", "wait for my review", "before i review", "after i approve") {
		boundaries = append(boundaries, "wait_for_review")
	}
	return boundaries
}
