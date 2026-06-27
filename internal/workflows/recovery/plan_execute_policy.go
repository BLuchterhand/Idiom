package recovery

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
)

type PlanExecutePlannerBlockedRetryInput struct {
	FailureKind    plancontract.PlanExecuteFailureKind
	FailureReason  string
	PlannerAnswer  string
	Stdout         string
	Stderr         string
	RequiredPaths  []string
	UseActiveModel bool
	RetrySignals   []PlanExecuteRetrySignal
}

type PlanExecuteRecoveryDecision struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	RetryAddendum string `json:"retry_addendum"`
}

func ParsePlanExecuteRecoveryDecision(raw string) (PlanExecuteRecoveryDecision, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlanExecuteRecoveryDecision{}, false
	}

	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return PlanExecuteRecoveryDecision{}, false
	}

	var decision PlanExecuteRecoveryDecision
	if err := json.Unmarshal([]byte(raw[start:end+1]), &decision); err != nil {
		return PlanExecuteRecoveryDecision{}, false
	}

	return decision, true
}

func PlanExecuteFailureCanUseRecovery(kind plancontract.PlanExecuteFailureKind) bool {
	switch kind {
	case plancontract.PlanExecuteFailurePlannerBlocked,
		plancontract.PlanExecuteFailurePlanArtifactMissing,
		plancontract.PlanExecuteFailureMissingRequestedTestEdit,
		plancontract.PlanExecuteFailureMissingRequestedImplementationEdit:
		return true
	default:
		return false
	}
}

func PlanExecutePlannerBlockedCanRetryOnActiveModel(input PlanExecutePlannerBlockedRetryInput) bool {
	if input.UseActiveModel || len(input.RequiredPaths) == 0 {
		return false
	}

	switch input.FailureKind {
	case plancontract.PlanExecuteFailurePlannerBlocked, plancontract.PlanExecuteFailurePlanArtifactMissing:
	default:
		return false
	}

	return len(input.RetrySignals) > 0
}

func PlanExecuteFailureShowsRequiredSourcesSatisfied(input PlanExecutePlannerBlockedRetryInput) bool {
	for _, signal := range input.RetrySignals {
		if signal == PlanExecuteRetrySignalRequiredSourcesSatisfied {
			return true
		}
	}
	return false
}

func AppendPlanExecuteActiveModelFallbackAddendum(prompt string) string {
	section := strings.Join([]string{
		"## Active Model Fallback",
		"",
		"The previous plan-execute planner attempt failed to produce a valid `submit_plan` artifact. It may have omitted required edit paths, used stale anchors, or emitted prose instead of a valid tool call. This retry is running on the active/default model with a fresh tool transcript. The required edit scope is still authoritative, but source-body evidence from the previous attempt is not executable evidence in this retry. Read or re-read any required path the gate reports as unread, then submit one `submit_plan` whose `edits[]` coverage includes every required edit path, answer `Decision: source-satisfied` with per-path current-state evidence, or answer `<<QUESTION>>:` with the exact path-specific blocker. Do not repeat a prose final answer.",
	}, "\n")
	return appendPlanExecuteRecoverySection(prompt, section)
}

func AppendDiscoveredCandidateScopeAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = plancontract.UniqueScopePaths(allowedPaths)
	if len(allowedPaths) == 0 {
		return strings.TrimSpace(prompt)
	}

	return appendPlanExecuteRecoverySection(prompt, DiscoveredCandidateScopeSection(allowedPaths))
}

func DiscoveredCandidateScopeSection(allowedPaths []string) string {
	allowedPaths = plancontract.UniqueScopePaths(allowedPaths)

	var b strings.Builder

	b.WriteString("## Discovered Candidate Edit Scope\n\n")
	b.WriteString("The first planner attempt stopped without `submit_plan` and the original TUI request had no explicit edit path. A bounded fallback discovery pass found these candidate source paths. Use them as the candidate edit scope; read an exact current anchor if needed, then submit a plan or name the concrete blocker.\n")
	b.WriteString("\nallowed_edit_paths:\n")

	for _, path := range allowedPaths {
		fmt.Fprintf(&b, "- %s\n", path)
	}

	return strings.TrimSpace(b.String())
}

func AppendMissingRequestedTestEditRecoveryAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = plancontract.UniqueScopePaths(allowedPaths)

	var b strings.Builder

	b.WriteString("## Missing Requested Test Edit Recovery\n\n")
	b.WriteString("The previous planner attempt submitted a production-only plan while a typed test-edit obligation remained unsatisfied. This retry is allowed exactly once. The first successful `submit_plan.edits` array must include at least one real edit to a test path and may include source edits still needed for the objective. The paths below are candidate scope, not mandatory coverage for every path; the typed obligation is at least one real test edit. If current source and test files already contain the requested final state, answer exactly `Decision: source-satisfied` and name both source and test evidence paths instead of submitting stale edits. Do not submit another production-only plan.\n")

	appendAllowedEditPaths(&b, allowedPaths)

	return appendPlanExecuteRecoverySection(prompt, b.String())
}

func AppendMissingRequestedImplementationEditRecoveryAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = plancontract.UniqueScopePaths(allowedPaths)

	var b strings.Builder

	b.WriteString("## Missing Requested Implementation Edit Recovery\n\n")
	b.WriteString("The previous planner attempt submitted a test-only plan while a typed implementation-edit obligation remained unsatisfied. This retry is allowed exactly once. The first successful `submit_plan.edits` array must include at least one real non-test implementation edit and may include test edits still needed for the objective. The paths below are candidate scope, not mandatory coverage for every path; the typed obligation is at least one real non-test implementation edit. If current source and test files already contain the requested final state, answer exactly `Decision: source-satisfied` and name both implementation and test evidence paths instead of submitting stale edits. Do not submit another test-only plan.\n")

	appendAllowedEditPaths(&b, allowedPaths)

	return appendPlanExecuteRecoverySection(prompt, b.String())
}

func appendAllowedEditPaths(b *strings.Builder, allowedPaths []string) {
	if b == nil || len(allowedPaths) == 0 {
		return
	}

	b.WriteString("\nallowed_edit_paths:\n")
	for _, path := range allowedPaths {
		fmt.Fprintf(b, "- %s\n", path)
	}
}

func appendPlanExecuteRecoverySection(prompt string, section string) string {
	prompt = strings.TrimSpace(prompt)
	section = strings.TrimSpace(section)
	if section == "" {
		return prompt
	}

	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return prompt + "\n\n" + section
}
