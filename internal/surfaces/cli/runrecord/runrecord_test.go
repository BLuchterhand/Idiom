package runrecord

import (
	"strings"
	"testing"
)

func TestValidateRunID(t *testing.T) {
	valid := []string{
		"20260621-120000",
		"run_ok-1",
		"run_ok_2",
	}
	for _, id := range valid {
		t.Run("valid/"+id, func(t *testing.T) {
			if err := ValidateRunID(id); err != nil {
				t.Fatalf("ValidateRunID(%q) returned error: %v", id, err)
			}
		})
	}

	invalid := []string{
		"",
		"../escape",
		"a/b",
		`a\b`,
		"run.ok_2",
		"space here",
		strings.Repeat("a", 129),
	}
	for _, id := range invalid {
		t.Run("invalid/"+id, func(t *testing.T) {
			if err := ValidateRunID(id); err == nil {
				t.Fatalf("ValidateRunID(%q) should fail", id)
			}
		})
	}
}

func TestNewRunIDIsValid(t *testing.T) {
	runID := NewRunID()
	if err := ValidateRunID(runID); err != nil {
		t.Fatalf("ValidateRunID(NewRunID()) = %v for %q", err, runID)
	}
}
