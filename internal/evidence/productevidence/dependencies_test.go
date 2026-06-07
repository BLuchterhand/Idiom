package productevidence

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestFirstNonEmptyString(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"empty slice", nil, ""},
		{"all empty", []string{"", "  ", "\t"}, ""},
		{"first non-empty", []string{"a", "b", "c"}, "a"},
		{"skip leading empty", []string{"", "  ", "hello"}, "hello"},
		{"single value", []string{"only"}, "only"},
		{"whitespace only is empty", []string{"   "}, ""},
		{"mixed whitespace", []string{"\n", "\t", " x "}, " x "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstNonEmptyString(tt.values...)
			if got != tt.want {
				t.Fatalf("firstNonEmptyString(%#v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestWithFallback(t *testing.T) {
	fallback := Dependencies{
		Version:              "fallback-version",
		Commit:               "fallback-commit",
		Date:                 "fallback-date",
		DemoCommandRunnerID:  "fallback-runner",
		SummarizeLaneHistory: func(string) (LaneHistorySummary, error) { return LaneHistorySummary{}, nil },
		RunMainNoTelemetry:   func([]string, io.Writer, io.Writer) int { return 0 },
	}

	t.Run("empty deps gets all from fallback", func(t *testing.T) {
		result := Dependencies{}.withFallback(fallback)
		if result.Version != "fallback-version" {
			t.Fatalf("version: got %q, want fallback-version", result.Version)
		}
		if result.Commit != "fallback-commit" {
			t.Fatalf("commit: got %q, want fallback-commit", result.Commit)
		}
		if result.Date != "fallback-date" {
			t.Fatalf("date: got %q, want fallback-date", result.Date)
		}
		if result.DemoCommandRunnerID != "fallback-runner" {
			t.Fatalf("runner: got %q, want fallback-runner", result.DemoCommandRunnerID)
		}
		if result.SummarizeLaneHistory == nil {
			t.Fatal("SummarizeLaneHistory should be populated from fallback")
		}
		if result.RunMainNoTelemetry == nil {
			t.Fatal("RunMainNoTelemetry should be populated from fallback")
		}
	})

	t.Run("provided deps override fallback", func(t *testing.T) {
		deps := Dependencies{
			Version: "custom-version",
			Commit:  "custom-commit",
		}
		result := deps.withFallback(fallback)
		if result.Version != "custom-version" {
			t.Fatalf("version: got %q, want custom-version", result.Version)
		}
		if result.Commit != "custom-commit" {
			t.Fatalf("commit: got %q, want custom-commit", result.Commit)
		}
		// Unset fields still come from fallback
		if result.Date != "fallback-date" {
			t.Fatalf("date: got %q, want fallback-date", result.Date)
		}
	})

	t.Run("nil functions fall back", func(t *testing.T) {
		result := Dependencies{
			SummarizeLaneHistory: nil,
			RunMainNoTelemetry:   nil,
		}.withFallback(fallback)
		if result.SummarizeLaneHistory == nil {
			t.Fatal("SummarizeLaneHistory nil was not filled from fallback")
		}
		if result.RunMainNoTelemetry == nil {
			t.Fatal("RunMainNoTelemetry nil was not filled from fallback")
		}
	})

	t.Run("non-nil functions are kept", func(t *testing.T) {
		custom := func(string) (LaneHistorySummary, error) { return LaneHistorySummary{Total: 99}, nil }
		result := Dependencies{
			SummarizeLaneHistory: custom,
		}.withFallback(fallback)
		if result.SummarizeLaneHistory == nil {
			t.Fatal("SummarizeLaneHistory should be kept when non-nil")
		}
		summary, _ := result.SummarizeLaneHistory("")
		if summary.Total != 99 {
			t.Fatalf("custom function was replaced by fallback: got total=%d", summary.Total)
		}
	})
}

func TestSummarizeLaneHistoryNilDeps(t *testing.T) {
	// Verify the nil-guard branch: when SummarizeLaneHistory is nil in both the
	// passed deps AND the default fallback, summarizeLaneHistory returns an empty
	// summary without error. TestMain does NOT set SummarizeLaneHistory in
	// defaultDependencies, so this path is reachable.
	deps := Dependencies{SummarizeLaneHistory: nil}
	summary, err := deps.summarizeLaneHistory(".")
	if err != nil {
		t.Fatalf("expected no error with nil SummarizeLaneHistory, got %v", err)
	}
	if summary.Total != 0 {
		t.Fatalf("expected empty summary with Total=0, got %#v", summary)
	}
}

func TestLogCLIPhaseNilSafe(t *testing.T) {
	deps := Dependencies{}
	// Should not panic with nil recorder and nil fields
	deps.logCLIPhase(nil, "test_event", nil)
	deps.logCLIPhase(nil, "test_event", map[string]any{"key": "value"})
}

func TestP911RunMainNoTelemetryWithDeps(t *testing.T) {
	t.Run("sets IDIOM_TELEMETRY=0 and restores", func(t *testing.T) {
		previous := os.Getenv("IDIOM_TELEMETRY")
		var capturedTelemetry string
		runner := func(args []string, stdout, stderr io.Writer) int {
			capturedTelemetry = os.Getenv("IDIOM_TELEMETRY")
			return 0
		}
		code := p911RunMainNoTelemetryWithDeps([]string{"tool"}, nil, nil, Dependencies{
			RunMainNoTelemetry: runner,
		})
		if code != 0 {
			t.Fatalf("expected exit 0, got %d", code)
		}
		if capturedTelemetry != "0" {
			t.Fatalf("expected IDIOM_TELEMETRY=0 during call, got %q", capturedTelemetry)
		}
		after := os.Getenv("IDIOM_TELEMETRY")
		if after != previous {
			t.Fatalf("IDIOM_TELEMETRY not restored: was %q, now %q", previous, after)
		}
	})

	t.Run("passes args to RunMainNoTelemetry", func(t *testing.T) {
		var capturedArgs []string
		runner := func(args []string, stdout, stderr io.Writer) int {
			capturedArgs = append([]string(nil), args...)
			return 0
		}
		p911RunMainNoTelemetryWithDeps([]string{"tool", "context_pack"}, nil, nil, Dependencies{
			RunMainNoTelemetry: runner,
		})
		if len(capturedArgs) != 2 || capturedArgs[0] != "tool" || capturedArgs[1] != "context_pack" {
			t.Fatalf("expected args [tool context_pack], got %#v", capturedArgs)
		}
	})

	// When both the passed deps AND the default fallback have nil RunMainNoTelemetry,
	// p911RunMainNoTelemetryWithDeps returns 127 and writes an error to stderr.
	// This path is normally masked by TestMain's setDefaultDependenciesForTest,
	// so we temporarily clear the default to test the nil-guard branch.
	t.Run("returns 127 when no runner available in deps or fallback", func(t *testing.T) {
		restore := setDefaultDependenciesForTest(Dependencies{})
		defer restore()

		var stderr bytes.Buffer
		code := p911RunMainNoTelemetryWithDeps([]string{"tool"}, nil, &stderr, Dependencies{
			RunMainNoTelemetry: nil,
		})
		if code != 127 {
			t.Fatalf("expected exit code 127 when no runner in deps or fallback, got %d", code)
		}
		if !strings.Contains(stderr.String(), "unavailable") {
			t.Fatalf("expected 'unavailable' message in stderr, got %q", stderr.String())
		}
	})
}
