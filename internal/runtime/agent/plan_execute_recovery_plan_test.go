package agent

import (
	"slices"
	"strings"
	"testing"
)

func TestPlanExecuteLowValueRecoveryPlanFieldsDoNotDependOnPromptProse(t *testing.T) {
	plan := planThenExecuteLowValueActionRecoveryPlan(PlanExecutePlanningState{
		Mode:              PlanExecutePlanningModeAllowedCandidate,
		SelectedEditPaths: []string{"pkg/feature.go"},
	})

	if plan.Kind != PlanExecuteRecoveryKindLowValueCandidateAction {
		t.Fatalf("Kind = %q", plan.Kind)
	}

	if !slices.Equal(plan.AllowedTools, []string{"submit_plan"}) {
		t.Fatalf("AllowedTools = %#v", plan.AllowedTools)
	}

	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}) {
		t.Fatalf("TerminalOptions = %#v", plan.TerminalOptions)
	}

	prompt := RenderPlanExecuteRecoveryPlan(plan)
	for _, want := range []string{"Current typed plan-execute state", "pkg/feature.go", "Valid terminal actions now"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteLowValueRecoveryPlanWithoutSelectedPathDoesNotAdvertiseSubmitPlan(t *testing.T) {
	plan := planThenExecuteLowValueActionRecoveryPlan(PlanExecutePlanningState{
		Mode: PlanExecutePlanningModeOpenCandidate,
	})

	if len(plan.AllowedTools) != 0 {
		t.Fatalf("AllowedTools = %#v, want none", plan.AllowedTools)
	}

	if slices.Contains(plan.TerminalOptions, "submit_plan") {
		t.Fatalf("TerminalOptions must not include submit_plan without selected edit paths: %#v", plan.TerminalOptions)
	}

	prompt := RenderPlanExecuteRecoveryPlan(plan)
	if strings.Contains(prompt, "path/from/selected") {
		t.Fatalf("rendered prompt must not include unsafe selected-path placeholder:\n%s", prompt)
	}

	if !strings.Contains(prompt, "Do not emit `submit_plan`") {
		t.Fatalf("rendered prompt should explicitly hold submit_plan until selected path exists:\n%s", prompt)
	}
}

func TestPlanExecuteGroundingRequiredRecoveryPlanFieldsDoNotDependOnPromptProse(t *testing.T) {
	plan := planThenExecuteGroundingRequiredRecoveryPlan()

	if plan.Kind != PlanExecuteRecoveryKindGroundingRequired {
		t.Fatalf("Kind = %q", plan.Kind)
	}

	for _, tool := range []string{"context_pack", "search_content", "read_file", "code_navigate"} {
		if !slices.Contains(plan.AllowedTools, tool) {
			t.Fatalf("AllowedTools missing %q: %#v", tool, plan.AllowedTools)
		}
	}

	if slices.Contains(plan.AllowedTools, "submit_plan") {
		t.Fatalf("grounding recovery should not allow repeating submit_plan: %#v", plan.AllowedTools)
	}

	if !slices.Equal(plan.TerminalOptions, []string{"Decision: source-satisfied", "<<QUESTION>>:"}) {
		t.Fatalf("TerminalOptions = %#v", plan.TerminalOptions)
	}
}

func TestPlanExecuteEmptySubmitPlanRecoveryReopensCandidateTools(t *testing.T) {
	plan := planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan()
	if plan.Kind != PlanExecuteRecoveryKindEmptySubmitPlan {
		t.Fatalf("Kind = %q", plan.Kind)
	}

	for _, tool := range []string{"context_pack", "read_file", "code_navigate", "submit_plan"} {
		if !slices.Contains(plan.AllowedTools, tool) {
			t.Fatalf("AllowedTools missing %q: %#v", tool, plan.AllowedTools)
		}
	}

	if !slices.Contains(plan.TerminalOptions, "submit_plan") ||
		!slices.Contains(plan.TerminalOptions, "<<QUESTION>>:") {

		t.Fatalf("TerminalOptions = %#v", plan.TerminalOptions)
	}

	prompt := RenderPlanExecuteRecoveryPlan(plan)
	for _, want := range []string{"no executable edits", "Do not repeat an empty `submit_plan`", "one bounded localization/source request", "not an example or placeholder path"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "candidate/path/from/evidence") {
		t.Fatalf("rendered prompt must not include copyable placeholder paths:\n%s", prompt)
	}
}

func TestPlanExecuteSubmitPlanParseRecoveryPlanFieldsDoNotDependOnPromptProse(t *testing.T) {
	plan := planThenExecuteSubmitPlanParseRecoveryPlan(true, []string{"a.go", "b.go"})

	if plan.Kind != PlanExecuteRecoveryKindSubmitPlanParse {
		t.Fatalf("Kind = %q", plan.Kind)
	}

	if !slices.Equal(plan.AllowedTools, []string{"submit_plan"}) {
		t.Fatalf("AllowedTools = %#v", plan.AllowedTools)
	}

	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}) {
		t.Fatalf("TerminalOptions = %#v", plan.TerminalOptions)
	}

	prompt := RenderPlanExecuteRecoveryPlan(plan)
	if !strings.HasPrefix(prompt, "SUBMIT_PLAN PARSE RECOVERY:") {
		t.Fatalf("parse recovery prompt should keep compact-history prefix:\n%s", prompt)
	}

	for _, want := range []string{"quoted JSON string", "Required edit paths: a.go, b.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
	}
}
