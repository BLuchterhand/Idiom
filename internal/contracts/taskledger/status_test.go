package taskledger

import (
	"slices"
	"testing"
)

func TestParseNormalizesAndValidatesTaskLedgerStatus(t *testing.T) {
	tests := []struct {
		input string
		want  Status
		valid bool
	}{
		{input: " pending ", want: StatusPending, valid: true},
		{input: "IN-PROGRESS", want: StatusInProgress, valid: true},
		{input: "completed", want: StatusCompleted, valid: true},
		{input: "blocked", want: StatusBlocked, valid: true},
		{input: "cancelled", want: StatusCancelled, valid: true},
		{input: "waiting", want: Status("waiting"), valid: false},
	}

	for _, test := range tests {
		got, valid := Parse(test.input)
		if got != test.want || valid != test.valid {
			t.Fatalf("Parse(%q) = (%q, %t), want (%q, %t)", test.input, got, valid, test.want, test.valid)
		}
	}
}

func TestValuesAndLifecyclePredicatesStayAligned(t *testing.T) {
	want := []string{"pending", "in_progress", "completed", "blocked", "cancelled"}
	if got := Values(); !slices.Equal(got, want) {
		t.Fatalf("Values() = %v, want %v", got, want)
	}
	if !StatusPending.Open() || !StatusInProgress.Open() || StatusBlocked.Open() {
		t.Fatal("Open must identify only pending and in-progress work")
	}
	if !StatusCompleted.Finished() || !StatusCancelled.Finished() || StatusBlocked.Finished() {
		t.Fatal("Finished must identify only completed and cancelled work")
	}
}
