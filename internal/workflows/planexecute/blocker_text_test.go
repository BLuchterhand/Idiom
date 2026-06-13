package planexecute

import (
	"strings"
	"testing"
)

func TestPlanExecuteBlockerSummaryPrefersExplicitQuestionOverPreamble(t *testing.T) {
	answer := strings.Join([]string{
		"I now have all the evidence I need. Let me analyze what's happening.",
		"",
		"The implementation needs a header declaration that is not in repair scope.",
		"<<QUESTION>>: required header changes are outside the allowed repair scope.",
	}, "\n")

	got := planExecuteBlockerSummary(answer)
	want := "<<QUESTION>>: required header changes are outside the allowed repair scope."
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestPlanExecuteBlockerSummaryPrefersBlockerLineOverPreamble(t *testing.T) {
	answer := "Analysis complete.\nBlocker: source refresh still required for native/src/core/colony_sim_tick.cpp"

	got := planExecuteBlockerSummary(answer)
	want := "Blocker: source refresh still required for native/src/core/colony_sim_tick.cpp"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
