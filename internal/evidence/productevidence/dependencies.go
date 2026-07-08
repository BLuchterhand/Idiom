package productevidence

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

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

var defaultDependencies = struct {
	sync.RWMutex
	value Dependencies
}{}

func currentDependencies(deps Dependencies) Dependencies {
	defaultDependencies.RLock()

	fallback := defaultDependencies.value
	defaultDependencies.RUnlock()

	return deps.withFallback(fallback)
}

func setDefaultDependenciesForTest(deps Dependencies) func() {
	defaultDependencies.Lock()

	previous := defaultDependencies.value

	defaultDependencies.value = deps
	defaultDependencies.Unlock()

	return func() {
		defaultDependencies.Lock()

		defaultDependencies.value = previous
		defaultDependencies.Unlock()
	}
}

func (deps Dependencies) withFallback(fallback Dependencies) Dependencies {
	if deps.SummarizeLaneHistory == nil {
		deps.SummarizeLaneHistory = fallback.SummarizeLaneHistory
	}

	if deps.RunMainNoTelemetry == nil {
		deps.RunMainNoTelemetry = fallback.RunMainNoTelemetry
	}

	if deps.DemoCommandRunnerID == "" {
		deps.DemoCommandRunnerID = fallback.DemoCommandRunnerID
	}

	if deps.Version == "" {
		deps.Version = fallback.Version
	}

	if deps.Commit == "" {
		deps.Commit = fallback.Commit
	}

	if deps.Date == "" {
		deps.Date = fallback.Date
	}

	return deps
}

func (deps Dependencies) summarizeLaneHistory(projectRoot string) (lanehistory.Summary, error) {
	deps = currentDependencies(deps)
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
	deps = currentDependencies(deps)
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
