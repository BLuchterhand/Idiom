package languagetools

import (
	"testing"

	"idiom/internal/repository/semantic"
)

func TestGoReadSymbolWindowUsesSymbolExtentWhenAvailable(t *testing.T) {
	// Symbol declared at lines 16-132 (e.g. Runner.runChildTask): the
	// returned window should start at 16 and run for the full body when
	// it fits under the cap. Previous bug used match.Line - 12 / +20, which
	// returned imports for any function declared past line 12.
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 0, 0)
	if start != 16 {
		t.Fatalf("expected start=16, got %d", start)
	}

	if end != 132 {
		t.Fatalf("expected end to include the full symbol body, got %d (start=%d)", end, start)
	}
}

func TestGoReadSymbolWindowHonorsExplicitRequest(t *testing.T) {
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 132}, 50, 70)
	if start != 50 || end != 70 {
		t.Fatalf("expected explicit window to be honored, got %d-%d", start, end)
	}
}

func TestGoReadSymbolWindowFallsBackToCenteredWindow(t *testing.T) {
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 200}, 0, 0)
	if start != 188 {
		t.Fatalf("expected fallback start=188 (200-12), got %d", start)
	}

	if end != 220 {
		t.Fatalf("expected fallback end=220 (200+20), got %d", end)
	}
}

func TestGoReadSymbolWindowCapsAtMaxLines(t *testing.T) {
	start, end := goReadSymbolWindow(semantic.SymbolMatch{Line: 16, StartLine: 16, EndLine: 516}, 0, 0)
	if start != 16 {
		t.Fatalf("expected start=16, got %d", start)
	}

	if end != 135 {
		t.Fatalf("expected end capped at 120-line window (16+119), got %d", end)
	}
}
