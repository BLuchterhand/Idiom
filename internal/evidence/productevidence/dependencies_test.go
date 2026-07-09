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

func TestSummarizeLaneHistoryNilDeps(t *testing.T) {
	deps := Dependencies{SummarizeLaneHistory: nil}

	summary, err := deps.summarizeLaneHistory(".")
	if err != nil {
		t.Fatalf("expected no error with nil SummarizeLaneHistory, got %v", err)
	}

	if summary.Total != 0 {
		t.Fatalf("expected empty summary with Total=0, got %#v", summary)
	}
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

	t.Run("returns 127 when no runner available", func(t *testing.T) {
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
