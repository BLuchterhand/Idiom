package app

import "testing"

func TestDesiredInputHeightAccountsForPromptColumns(t *testing.T) {
	got := desiredInputHeight(10, "> ", "123456789")
	if got != 2 {
		t.Fatalf("desiredInputHeight() = %d, want 2", got)
	}
}
