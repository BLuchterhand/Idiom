package productworkflow

import "testing"

func TestContextFreshnessCurrentHelpersMatchVocabulary(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "current", want: true},
		{value: "fresh", want: true},
		{value: "verified", want: true},
		{value: " VERIFIED ", want: true},
		{value: "stale"},
		{value: ""},
	}

	for _, tt := range tests {
		if got := productWorkflowContextFreshnessCurrent(tt.value); got != tt.want {
			t.Fatalf("productWorkflowContextFreshnessCurrent(%q) = %t, want %t", tt.value, got, tt.want)
		}
	}
}
