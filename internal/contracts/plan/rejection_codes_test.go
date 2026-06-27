package plan

import "testing"

func TestPlanExecuteRejectionCodesAllParse(t *testing.T) {
	seen := map[PlanExecuteRejectionCode]struct{}{}
	for _, code := range PlanExecuteRejectionCodes() {
		if _, ok := seen[code]; ok {
			t.Fatalf("duplicate PlanExecuteRejectionCodes entry %q", code)
		}
		seen[code] = struct{}{}

		parsed, ok := ParsePlanExecuteRejectionCode(string(code))
		if !ok || parsed != code {
			t.Fatalf("ParsePlanExecuteRejectionCode(%q) = %q %v", code, parsed, ok)
		}
	}
}

func TestParsePlanExecuteRejectionCodeRejectsUnknown(t *testing.T) {
	if code, ok := ParsePlanExecuteRejectionCode("read outside allowed candidate scope"); ok {
		t.Fatalf("ParsePlanExecuteRejectionCode accepted prose summary %q", code)
	}
}
