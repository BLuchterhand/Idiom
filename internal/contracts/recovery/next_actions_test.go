package recovery

import "testing"

func TestParseNextActionAcceptsExactAndLegacyHyphenatedSpelling(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want NextAction
	}{
		{name: "exact", in: "retry_plan_execute", want: NextActionRetryPlanExecute},
		{name: "legacy hyphen", in: "retry-plan-execute", want: NextActionRetryPlanExecute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseNextAction(tc.in)
			if !ok || got != tc.want {
				t.Fatalf("ParseNextAction(%q) = %q %v, want %q true", tc.in, got, ok, tc.want)
			}
		})
	}

	if got, ok := ParseNextAction("retry-plan-typo"); ok {
		t.Fatalf("ParseNextAction accepted unknown action %q", got)
	}
}

func TestNextActionsAllParse(t *testing.T) {
	for _, action := range NextActions() {
		got, ok := ParseNextAction(string(action))
		if !ok || got != action {
			t.Fatalf("ParseNextAction(%q) = %q %v, want %q true", action, got, ok, action)
		}
	}
}

func TestParseNextActionCanonicalizesTrimCaseAndHyphens(t *testing.T) {
	for _, raw := range []string{
		" \tRETRY_PLAN_EXECUTE\n",
		" \tretry-plan-execute\n",
		" \tRetry-Plan-Execute\n",
	} {
		got, ok := ParseNextAction(raw)
		if !ok || got != NextActionRetryPlanExecute {
			t.Fatalf("ParseNextAction(%q) = %q %v, want %q true", raw, got, ok, NextActionRetryPlanExecute)
		}
	}
}
