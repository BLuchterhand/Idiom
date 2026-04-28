package app

import (
	"context"
	"strings"
	"unicode"

	"idiom/internal/agent"
	"idiom/internal/frontier/workerruntime"
)

type turnRoutePlan struct {
	InitialLane  string `json:"initial_lane,omitempty"`
	FollowupLane string `json:"followup_lane,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Contract     string `json:"contract,omitempty"`
	Family       string `json:"family,omitempty"`
	Thoroughness string `json:"thoroughness,omitempty"`
}

// classifyTurnRoute routes a turn to a workflow family using the
// deterministic prompt-shape fallback. The previous OpenCode-shaped
// shape included an LLM-classifier call that proposed a family and was
// corrected against the fallback; that path was deleted because the
// fallback was authoritative in every corrective branch and the extra
// model call only added latency, an extra failure mode, and a
// route_classifier server dependency.
//
// Callers still pass ctx so the signature can grow back if a future
// router needs network access.
func (m *Model) classifyTurnRoute(_ context.Context, prompt string, mode agent.Mode) turnRoutePlan {
	return fallbackTurnRoute(strings.TrimSpace(prompt), mode)
}

func normalizeTurnRoutePlan(plan turnRoutePlan, mode agent.Mode) turnRoutePlan {
	plan.InitialLane = normalizeRouteValue(plan.InitialLane)
	plan.FollowupLane = normalizeRouteValue(plan.FollowupLane)
	plan.Reason = normalizeRouteValue(plan.Reason)
	plan.Contract = normalizeRouteValue(plan.Contract)
	plan.Family = normalizeRouteValue(plan.Family)
	plan.Thoroughness = normalizeRouteValue(plan.Thoroughness)
	if plan.Family == "" {
		return fallbackTurnRoute("", mode)
	}
	switch plan.Family {
	case "direct_question":
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = firstNonEmptyString(plan.Reason, "direct_conversation")
	case "debug_logs":
		plan.InitialLane = "explore"
		plan.FollowupLane = ""
		plan.Contract = "grounded_explanation"
		plan.Reason = firstNonEmptyString(plan.Reason, "logs_failure_analysis")
	case "verification_only":
		plan.InitialLane = "main"
		plan.FollowupLane = ""
		plan.Contract = "direct_answer"
		plan.Reason = firstNonEmptyString(plan.Reason, "verification_request")
	case "locate":
		plan.InitialLane = "locate"
		plan.FollowupLane = ""
		plan.Contract = "locate_targets"
		plan.Reason = firstNonEmptyString(plan.Reason, "locate_request")
	case "locate_then_implement":
		plan.InitialLane = "locate"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "locate_request")
	case "explore_then_implement", "investigate_then_implement":
		plan.InitialLane = "explore"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "explore_request")
	case "review_then_implement":
		plan.InitialLane = "review"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "review_request")
	case "security_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = firstNonEmptyString(plan.Reason, "security_review")
	case "performance_review":
		plan.InitialLane = "review"
		plan.FollowupLane = ""
		plan.Contract = "review_findings"
		plan.Reason = firstNonEmptyString(plan.Reason, "performance_review")
	case "architecture":
		plan.InitialLane = "architecture"
		plan.FollowupLane = ""
		plan.Contract = "architecture_assessment"
		plan.Reason = firstNonEmptyString(plan.Reason, "architecture_request")
	case "architecture_then_implement":
		plan.InitialLane = "architecture"
		plan.FollowupLane = "implement"
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "architecture_request")
	case "implementation", "test_authoring", "config_edit", "script_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		plan.InitialLane = "implement"
		plan.FollowupLane = ""
		plan.Contract = "implementation_result"
		plan.Reason = firstNonEmptyString(plan.Reason, "direct_implementation")
	case "multi_turn_state":
		plan.InitialLane = "main"
		plan.Contract = "direct_answer"
		plan.Reason = firstNonEmptyString(plan.Reason, "context_followup")
	default:
		return fallbackTurnRoute("", mode)
	}
	return plan
}

func fallbackTurnRoute(prompt string, mode agent.Mode) turnRoutePlan {
	lower := " " + strings.ToLower(strings.TrimSpace(prompt)) + " "
	words := plannerWordText(prompt)
	switch {
	case containsAny(words, " log ", " logs ", " jsonl ", " trace ") && containsAny(words, " fail ", " failed ", " error ", " why ", " latest ", " recent "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "debug_logs"}, mode)
	case containsAny(words, " go test ", " test suite ", " run tests ", " run the tests ", " report failure ", " report failures ", " compile failure "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "verification_only"}, mode)
	case containsAny(words, " find where ", " locate ", " where is ", " where the ") && containsAny(words, " remove ", " fix ", " update ", " edit ", " change ", " add "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate_then_implement"}, mode)
	case containsAny(words, " find where ", " locate ", " where is ", " where the "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "locate"}, mode)
	case containsAny(words, " architecture ", " boundary ", " boundaries ", " coupling ", " refactor stage "):
		if containsAny(words, " fix ", " change ", " implement ", " refactor ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case isExplanationOnlyCodeQuestion(words):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	case containsAny(words, " security ", " prompt injection ", " adversarial "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "security_review"}, mode)
	case containsAny(words, " performance ", " perf ", " timeout ", " timed out ", " regression ", " slow "):
		if containsAny(words, " fix ", " change ", " implement ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case containsAny(words, " review ", " investigate "):
		if containsAny(words, " fix ", " change ", " implement ", " update ", " edit ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "review_then_implement"}, mode)
		}
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "performance_review"}, mode)
	case containsAny(words, " write test ", " add regression test ", " add a test "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "test_authoring"}, mode)
	case containsAny(words, " readme ", " docs ", " documentation ", " doc "):
		if containsAny(words, " update ", " add ", " edit ", " write ", " change ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "docs_edit"}, mode)
		}
	case containsAny(lower, " config ", ".json", ".yaml", ".toml"):
		if containsAny(words, " update ", " edit ", " change ", " add ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "config_edit"}, mode)
		}
	case containsAny(lower, " script ", ".sh", " shell "):
		if containsAny(words, " update ", " edit ", " change ", " add ", " fix ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "script_edit"}, mode)
		}
	case containsAny(lower, " prompt ", " prompts "):
		if containsAny(words, " update ", " edit ", " change ", " add ", " fix ") {
			return normalizeTurnRoutePlan(turnRoutePlan{Family: "prompt_edit_security"}, mode)
		}
	case containsAny(words, " fix ", " change ", " update ", " edit ", " add ", " remove ", " delete ", " implement ", " refactor ", " rename ", " replace ", " create "):
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "implementation"}, mode)
	}
	if strings.EqualFold(mode.Name, "plan") {
		return normalizeTurnRoutePlan(turnRoutePlan{Family: "architecture"}, mode)
	}
	return normalizeTurnRoutePlan(turnRoutePlan{Family: "direct_question"}, mode)
}

func isImplementationLikeFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "implementation",
		"test_authoring",
		"config_edit",
		"script_edit",
		"docs_edit",
		"prompt_edit_security",
		"failure_injection",
		"locate_then_implement",
		"explore_then_implement",
		"investigate_then_implement",
		"review_then_implement",
		"architecture_then_implement":
		return true
	default:
		return false
	}
}

func isExplanationOnlyCodeQuestion(words string) bool {
	if hasEditIntent(words) || containsAny(words,
		" review ",
		" performance ",
		" perf ",
		" security ",
		" latest logs ",
		" recent logs ",
		" run tests ",
		" verify ",
		" where is ",
		" locate ",
	) {
		return false
	}
	if !containsAny(words,
		" how does ",
		" how do ",
		" how is ",
		" how are ",
		" explain how ",
		" explain what ",
		" what does ",
		" how this ",
	) {
		return false
	}
	return containsAny(words,
		" harness ",
		" codebase ",
		" repo ",
		" repository ",
		" runner ",
		" tool ",
		" tools ",
		" agent ",
		" agents ",
		" subagent ",
		" subagents ",
		" sub agent ",
		" workflow ",
		" component ",
		" system ",
		" this ",
		" mode ",
		" feature ",
		" behavior ",
		" debug log ",
		" redacted ",
	)
}

func hasEditIntent(words string) bool {
	return containsAny(words,
		" fix ",
		" change ",
		" update ",
		" edit ",
		" add ",
		" remove ",
		" delete ",
		" implement ",
		" refactor ",
		" rename ",
		" replace ",
		" create ",
		" patch ",
	)
}

func plannerWordText(prompt string) string {
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	if prompt == "" {
		return " "
	}
	var b strings.Builder
	b.Grow(len(prompt) + 2)
	b.WriteByte(' ')
	lastSpace := true
	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	if !lastSpace {
		b.WriteByte(' ')
	}
	return b.String()
}

func renderTurnRoutePlan(plan turnRoutePlan) string {
	var parts []string
	if plan.InitialLane != "" {
		parts = append(parts, "initial="+plan.InitialLane)
	}
	if plan.FollowupLane != "" {
		parts = append(parts, "followup="+plan.FollowupLane)
	}
	if plan.Reason != "" {
		parts = append(parts, "reason="+plan.Reason)
	}
	if plan.Contract != "" {
		parts = append(parts, "contract="+plan.Contract)
	}
	if plan.Thoroughness != "" {
		parts = append(parts, "thoroughness="+plan.Thoroughness)
	}
	return strings.Join(parts, " ")
}

func turnRoutePlanRuntimeBrief(plan turnRoutePlan, mode agent.Mode, workspaceAllowed []string) workerruntime.WorkerRuntimeBrief {
	plan = normalizeTurnRoutePlan(plan, mode)
	brief := workerruntime.RuntimeBriefForTaskFamily(plan.Family, firstNonEmptyString(plan.InitialLane, "main"), workspaceAllowed)
	if plan.FollowupLane != "" {
		brief.Constraints = append(brief.Constraints, "followup_lane="+plan.FollowupLane)
	}
	if plan.Thoroughness != "" {
		brief.Constraints = append(brief.Constraints, "thoroughness="+plan.Thoroughness)
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func (m *Model) workspaceAllowedTools() []string {
	if m == nil || m.workspace == nil {
		return nil
	}
	return m.workspace.AllowedTools()
}

func appendRunnerStateStrings(values []string, extra ...string) []string {
	out := append([]string(nil), values...)
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range out {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			out = append(out, value)
		}
	}
	return out
}

func normalizeRouteValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
