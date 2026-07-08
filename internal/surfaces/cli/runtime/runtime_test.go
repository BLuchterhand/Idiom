package cmdruntime

import (
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestRunTUIRejectsNonTerminalBeforeBubbleTeaStartup(t *testing.T) {
	err := RunTUI(config.Default(), RunOptions{})
	if err == nil {
		t.Fatal("RunTUI should reject non-terminal execution in tests")
	}
	if !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("unexpected TUI guard error: %v", err)
	}
}
