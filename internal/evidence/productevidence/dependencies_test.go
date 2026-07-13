package productevidence

import (
	"bytes"
	"fmt"
	"io"
	"os"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("first non empty string", func() {

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
		ginkgo.It(tt.name, func() {
			got := firstNonEmptyString(tt.values...)
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("firstNonEmptyString(%#v) = %q, want %q", tt.values, got, tt.want))

		})
	}
})
var _ = ginkgo.Describe("dependencies", func() {
	ginkgo.It("summarize lane history nil deps", func() {
		deps := Dependencies{SummarizeLaneHistory: nil}

		summary, err := deps.summarizeLaneHistory(".")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected no error with nil SummarizeLaneHistory, got %v", err))
		gomega.Expect(summary.Total).To(gomega.Equal(0), fmt.Sprintf("expected empty summary with Total=0, got %#v", summary))

	})
})
var _ = ginkgo.Describe("p911 run main no telemetry with deps", func() {
	ginkgo.It("sets IDIOM_TELEMETRY=0 and restores", func() {
		previous := os.Getenv("IDIOM_TELEMETRY")

		var capturedTelemetry string

		runner := func(args []string, stdout, stderr io.Writer) int {
			capturedTelemetry = os.Getenv("IDIOM_TELEMETRY")
			return 0
		}
		code := p911RunMainNoTelemetryWithDeps([]string{"tool"}, nil, nil, Dependencies{
			RunMainNoTelemetry: runner,
		})
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d", code))
		gomega.Expect(capturedTelemetry).To(gomega.Equal("0"), fmt.Sprintf("expected IDIOM_TELEMETRY=0 during call, got %q", capturedTelemetry))

		after := os.Getenv("IDIOM_TELEMETRY")
		gomega.Expect(after).To(gomega.Equal(previous), fmt.Sprintf("IDIOM_TELEMETRY not restored: was %q, now %q", previous, after))

	})
	ginkgo.It("passes args to RunMainNoTelemetry", func() {
		var capturedArgs []string

		runner := func(args []string, stdout, stderr io.Writer) int {
			capturedArgs = append([]string(nil), args...)
			return 0
		}
		p911RunMainNoTelemetryWithDeps([]string{"tool", "context_pack"}, nil, nil, Dependencies{
			RunMainNoTelemetry: runner,
		})
		gomega.Expect(capturedArgs).To(gomega.HaveLen(2), fmt.Sprintf("expected args [tool context_pack], got %#v", capturedArgs))
		gomega.Expect(capturedArgs[0]).To(gomega.Equal("tool"), fmt.Sprintf("expected args [tool context_pack], got %#v", capturedArgs))
		gomega.Expect(capturedArgs[1]).To(gomega.Equal("context_pack"), fmt.Sprintf("expected args [tool context_pack], got %#v", capturedArgs))

	})
	ginkgo.It("returns 127 when no runner available", func() {
		var stderr bytes.Buffer

		code := p911RunMainNoTelemetryWithDeps([]string{"tool"}, nil, &stderr, Dependencies{
			RunMainNoTelemetry: nil,
		})
		gomega.Expect(code).To(gomega.Equal(127), fmt.Sprintf("expected exit code 127 when no runner in deps or fallback, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unavailable"), fmt.Sprintf("expected 'unavailable' message in stderr, got %q", stderr.String()))

	})
})
