package app

import (
	"fmt"
	"regexp"
	"strings"

	lanecontract "idiom/internal/contracts/lane"
)

// Lane switch recommendation constants. These are the lane command keys the
// popup dispatches via the existing runImplementCommandOnce /
// runPlanExecuteCommandOnce / runFeaturePlannerCommandOnce entrypoints. The
// "none" sentinel means "stay in the current read-only lane".
const (
	laneSwitchImplement      = "implement"
	laneSwitchPlanExecute    = string(lanecontract.LanePlanExecute)
	laneSwitchFeaturePlanner = "feature-planner"
	laneSwitchNone           = "none"
)

// laneSwitchReferralPatterns are the conservative substring signals that
// indicate an assistant reply is a lane-refusal: the model recognised a
// workspace change is wanted but explained it cannot act because the active
// route is read-only. Matched case-insensitively against the final reply.
//
// These are deliberately multi-word phrases to avoid false positives on
// legitimate read-only answers (status reports, architecture explanations
// that happen to mention "read-only"). Single words like "implement" are
// NOT signals on their own.
//
// The detection sniffs the MODEL'S OWN refusal language, never the user's
// prompt — this respects the repo invariant at turn_planner.go:1077 that
// human-facing lane changes must never be inferred from prompt wording.
var laneSwitchReferralPatterns = []*regexp.Regexp{
	// Direct lane-refusal phrasings the model emits when its runtime brief
	// forbids writes.
	regexp.MustCompile(`(?i)read[- ]only (mode|lane|conversation|route)`),
	regexp.MustCompile(`(?i)read[- ]only;`),
	regexp.MustCompile(`(?i)\bnot in (the )?right lane\b`),
	regexp.MustCompile(`(?i)\bwrong lane\b`),
	regexp.MustCompile(`(?i)\bnot the right lane\b`),
	regexp.MustCompile(`(?i)\bswitch to (the )?(write|implementation|implement|plan-execute|feature-planner|write-capable) (lane|mode)\b`),
	regexp.MustCompile(`(?i)\buse /(implement|plan-execute|feature-planner)\b`),
	regexp.MustCompile(`(?i)\breply with /(implement|plan-execute|feature-planner)\b`),
	regexp.MustCompile(`(?i)\bcannot (edit|write|modify|change|create|delete|update) files?\b.*\bread[- ]only\b`),
	regexp.MustCompile(`(?i)\bcan't (edit|write|modify|change|create|delete|update) files?\b.*\bread[- ]only\b`),
	regexp.MustCompile(`(?i)\bwrite-capable lane\b`),
}

// laneSwitchRecommendationPatterns maps a lane-specific signal in the reply
// to the recommended lane command. Order matters: feature-planner and
// plan-execute are checked before implement so a specific referral wins over
// the generic default.
var laneSwitchRecommendationPatterns = []struct {
	key     string
	pattern *regexp.Regexp
}{
	{
		key:     laneSwitchPlanExecute,
		pattern: regexp.MustCompile(`(?i)\b(?:plan[- ]execute|/plan-execute)\b`),
	},
	{
		key:     laneSwitchFeaturePlanner,
		pattern: regexp.MustCompile(`(?i)\b(?:feature[- ]planner|/feature-planner)\b`),
	},
	{
		key:     laneSwitchImplement,
		pattern: regexp.MustCompile(`(?i)\b(?:/implement|implementation lane|implement lane)\b`),
	},
}

// laneSwitchReferralFromReply scans an assistant reply for lane-refusal
// language and returns (recommended, true) when one of the conservative
// patterns matches. The recommended key is derived from any lane-specific
// signal in the reply (e.g. "use /plan-execute" recommends plan-execute);
// when no specific signal is present it defaults to implement.
//
// Returns ("", false) when the reply does not look like a lane-refusal.
func laneSwitchReferralFromReply(reply string) (string, bool) {
	if strings.TrimSpace(reply) == "" {
		return "", false
	}

	matched := false
	for _, p := range laneSwitchReferralPatterns {
		if p.MatchString(reply) {
			matched = true
			break
		}
	}

	if !matched {
		return "", false
	}

	for _, rec := range laneSwitchRecommendationPatterns {
		if rec.pattern.MatchString(reply) {
			return rec.key, true
		}
	}

	return laneSwitchImplement, true
}

// laneSwitchHeadlessReply renders the numbered text block appended to the
// assistant reply in headless mode (-prompt / -json / artifact runners).
// The user re-dispatches by typing the listed slash command on the next
// turn; the headless stream cannot accept interactive input mid-turn.
func laneSwitchHeadlessReply(objective, recommended string) string {
	objective = strings.TrimSpace(objective)

	var quoted string
	if objective != "" {
		quoted = " " + objective
	}

	recommended = laneSwitchCanonicalKey(recommended)

	lines := []string{
		"Would you like to switch to the appropriate lane?",
		"",
		fmt.Sprintf("1. /implement%s", quoted),
		fmt.Sprintf("2. /plan-execute%s", quoted),
		fmt.Sprintf("3. /feature-planner%s", quoted),
		"4. No, keep the read-only answer above.",
		"",
		fmt.Sprintf("Recommended: %s. Reply with the command above to dispatch.", laneSwitchLabelForHeadless(recommended)),
	}

	return strings.Join(lines, "\n")
}

// laneSwitchCanonicalKey clamps an arbitrary input to a known lane key so a
// bad caller can never produce an "Recommended: " line with an unknown lane.
func laneSwitchCanonicalKey(key string) string {
	switch strings.TrimSpace(key) {
	case laneSwitchImplement, laneSwitchPlanExecute, laneSwitchFeaturePlanner, laneSwitchNone:
		return key
	default:
		return laneSwitchImplement
	}
}

func laneSwitchLabelForHeadless(key string) string {
	switch key {
	case laneSwitchPlanExecute:
		return "/plan-execute"
	case laneSwitchFeaturePlanner:
		return "/feature-planner"
	case laneSwitchNone:
		return "stay read-only"
	default:
		return "/implement"
	}
}
