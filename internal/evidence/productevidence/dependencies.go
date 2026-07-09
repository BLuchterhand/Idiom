package productevidence

import (
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/evidence/lanehistory"
)

type Dependencies struct {
	SummarizeLaneHistory func(projectRoot string) (lanehistory.Summary, error)
	RunMainNoTelemetry   func(args []string, stdout io.Writer, stderr io.Writer) int
	DemoCommandRunnerID  string
	Version              string
	Commit               string
	Date                 string
}

func (deps Dependencies) summarizeLaneHistory(projectRoot string) (lanehistory.Summary, error) {
	if deps.SummarizeLaneHistory == nil {
		return lanehistory.Summary{}, nil
	}

	return deps.SummarizeLaneHistory(projectRoot)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func p911RunMainNoTelemetryWithDeps(args []string, stdout io.Writer, stderr io.Writer, deps Dependencies) int {
	if deps.RunMainNoTelemetry == nil {
		fmt.Fprintln(stderr, "demo command runner unavailable")

		return 127
	}

	previous, hadPrevious := os.LookupEnv("IDIOM_TELEMETRY")

	_ = os.Setenv("IDIOM_TELEMETRY", "0")

	defer func() {
		if hadPrevious {
			_ = os.Setenv("IDIOM_TELEMETRY", previous)
		} else {
			_ = os.Unsetenv("IDIOM_TELEMETRY")
		}
	}()

	return deps.RunMainNoTelemetry(args, stdout, stderr)
}
