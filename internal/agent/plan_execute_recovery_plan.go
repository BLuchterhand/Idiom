package agent

import (
	"slices"
	"strings"

	"idiom/internal/workspace"
)

type PlanExecuteRecoveryKind string

const (
	PlanExecuteRecoveryKindLowValueCandidateAction PlanExecuteRecoveryKind = "low_value_candidate_action"
	PlanExecuteRecoveryKindGroundingRequired       PlanExecuteRecoveryKind = "grounding_required"
	PlanExecuteRecoveryKindEmptySubmitPlan         PlanExecuteRecoveryKind = "empty_submit_plan"
	PlanExecuteRecoveryKindSubmitPlanParse         PlanExecuteRecoveryKind = "submit_plan_parse"
)

type PlanExecuteRecoveryPlan struct {
	Kind                 PlanExecuteRecoveryKind
	AllowedTools         []string
	TerminalOptions      []string
	Summary              string
	Instructions         []string
	SubmitPlanShape      string
	DeterministicRequest *workspace.ToolRequest
}

func RenderPlanExecuteRecoveryPlan(plan PlanExecuteRecoveryPlan) string {
	parts := make([]string, 0, 5+len(plan.Instructions))
	if strings.TrimSpace(plan.Summary) != "" {
		parts = append(parts, strings.TrimSpace(plan.Summary))
	}
	if len(plan.AllowedTools) > 0 {
		parts = append(parts, "Allowed tools now: "+strings.Join(plan.AllowedTools, ", ")+".")
	}
	if len(plan.TerminalOptions) > 0 {
		parts = append(parts, "Valid terminal actions now: "+strings.Join(plan.TerminalOptions, ", ")+".")
	}
	parts = append(parts, plan.Instructions...)
	if strings.TrimSpace(plan.SubmitPlanShape) != "" {
		parts = append(parts, strings.TrimSpace(plan.SubmitPlanShape))
	}
	return strings.Join(nonEmptyPlanExecuteRecoveryPromptParts(parts), "\n\n")
}

func nonEmptyPlanExecuteRecoveryPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func planThenExecuteLowValueActionRecoveryPlan(state PlanExecutePlanningState) PlanExecuteRecoveryPlan {
	terminal := []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	shape := "Do not emit `submit_plan` until the planner has a concrete selected edit path."
	selected := "no concrete selected edit path"
	allowedTools := []string{}
	if len(state.SelectedEditPaths) > 0 {
		selected = strings.Join(state.SelectedEditPaths, ", ")
		terminal = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
		allowedTools = []string{"submit_plan"}
		shape = "For textual `submit_plan`, emit one visible JSON object with top-level `tool`, `rationale`, and an `edits` array. Each edit path must be one of the selected paths, and every edit body must contain real source text from the gathered evidence. Do not combine multiple paths into one field and do not copy placeholders, ellipses, or schema examples."
	}
	instruction := "This read/search is low-value because source evidence is already sufficient for a terminal planner outcome over selected edit paths: " + selected + ". Continue exploration only with one exact missing edit-safety fact and a bounded unread path/range."
	if slices.Contains(state.LowValueActions, "candidate_read_missing_target") {
		instruction = "This source-read request is missing a concrete `path`/handle. Use the selected evidence to produce a terminal planner outcome, or make one bounded read on an explicit unread candidate path only if a named edit-safety fact is still missing."
	}
	if slices.Contains(state.LowValueActions, "candidate_source_budget_exhausted") {
		instruction = "This read/search is low-value because the candidate source-read budget is exhausted and selected edit paths already have source evidence: " + selected + ". Do not read/search again before a terminal planner outcome."
	}
	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindLowValueCandidateAction,
		AllowedTools:    allowedTools,
		TerminalOptions: terminal,
		Summary:         "Current typed plan-execute state: " + string(state.Mode) + ".",
		Instructions: []string{
			instruction,
			"Next visible action must be exactly one parser-readable terminal action.",
		},
		SubmitPlanShape: shape,
	}
}

func planThenExecuteGroundingRequiredRecoveryPlan() PlanExecuteRecoveryPlan {
	return PlanExecuteRecoveryPlan{
		Kind: PlanExecuteRecoveryKindGroundingRequired,
		AllowedTools: []string{
			"context_pack",
			"product_efforts",
			"product_status",
			"search_content",
			"read_file",
			"code_navigate",
		},
		TerminalOptions: []string{"Decision: source-satisfied", "<<QUESTION>>:"},
		Summary:         "The previous `submit_plan` was rejected because this unscoped plan-execute atom has no grounded edit paths yet.",
		Instructions: []string{
			"Do not repeat `submit_plan`.",
			"Next visible action must be one read-only localization or source-evidence request, or a terminal closeout.",
			"For source-evidence requests, use `context_pack`, `product_efforts`, `product_status`, targeted `search_content`, `read_file`, or `code_navigate` with `capability:\"read_symbol\"`/`extract_body`.",
			"For terminal closeout, answer `Decision: source-satisfied` with observed current-state evidence, or answer `<<QUESTION>>:` with the exact blocker.",
		},
	}
}

func planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan() PlanExecuteRecoveryPlan {
	return PlanExecuteRecoveryPlan{
		Kind: PlanExecuteRecoveryKindEmptySubmitPlan,
		AllowedTools: []string{
			"context_pack",
			"context_query",
			"context_zoom",
			"discovery_query",
			"search_content",
			"read_file",
			"code_navigate",
			"submit_plan",
		},
		TerminalOptions: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
		Summary:         "The previous `submit_plan` was rejected before it became a valid plan artifact because it contained no executable edits.",
		Instructions: []string{
			"No required_edit_paths or allowed_edit_paths list is active, so the harness cannot invent coverage paths for this planner turn.",
			"Do not repeat an empty `submit_plan`. If current evidence supports a concrete change, submit a non-empty `edits[]` array using a real candidate path and one executable edit shape.",
			"If one edit-safety fact is missing, make exactly one bounded localization/source request for a candidate path from the packet or observed source evidence.",
			"If no concrete candidate edit path can be identified, answer `<<QUESTION>>:` with the exact blocker instead of retrying `submit_plan`.",
		},
		SubmitPlanShape: "For textual `submit_plan`, emit one visible JSON object with top-level `tool`, `rationale`, and non-empty `edits` array. Each edit must use an actual candidate path from observed evidence, not an example or placeholder path, and exactly one executable shape such as exact `old_text`/`new_text` or bounded `start_line`/`end_line`/`content`.",
	}
}

func planThenExecuteSubmitPlanParseRecoveryPlan(stringifiedEdits bool, requiredPaths []string) PlanExecuteRecoveryPlan {
	summary := "SUBMIT_PLAN PARSE RECOVERY: the previous visible tool object was malformed and did not count as a plan artifact."
	if stringifiedEdits {
		summary = "SUBMIT_PLAN PARSE RECOVERY: the previous `submit_plan` did not count because `edits` was emitted as a quoted JSON string."
	}
	instructions := []string{
		"Do not call read/search/discovery for this correction. The required source-body obligations are already satisfied.",
		"Next visible action MUST be exactly one of:",
		"  1. A parser-readable `submit_plan` request whose top-level `edits` value is a JSON array, not a string. Each edit object must contain a path from the required edit scope, one executable edit shape, and a one-sentence purpose.\n     Use `insert_after_line` plus small inline non-empty `content` for small insertions/appends after an existing line. Use `old_text`/`new_text` for exact replacements/deletions. Use numeric `start_line`/`end_line` plus complete `content` to replace that exact inclusive current-source range; for large additions, include the unchanged anchor line(s) plus inserted content.",
		"  2. `Decision: source-satisfied` naming every required path and the observed current-state evidence.",
		"  3. `<<QUESTION>>:` with the exact path-specific blocker that prevents drafting the plan.",
	}
	if len(requiredPaths) > 0 {
		instructions = append(instructions, "Required edit paths: "+strings.Join(requiredPaths, ", ")+".")
	}
	return PlanExecuteRecoveryPlan{
		Kind:            PlanExecuteRecoveryKindSubmitPlanParse,
		AllowedTools:    []string{"submit_plan"},
		TerminalOptions: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
		Summary:         summary,
		Instructions:    instructions,
	}
}
