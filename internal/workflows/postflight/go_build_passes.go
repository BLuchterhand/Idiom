package postflight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"idiom/internal/workflows/commandcheck"
)

// GoBuildPasses runs `go build ./...` against the project root
// when the diff touches any .go file. Compiler errors are surfaced
// as one Finding per `<path>:<line>:<col>: <message>` line so a
// missing import or undeclared identifier fails postflight the
// same way a hand-written check would.
type GoBuildPasses struct{}

func (GoBuildPasses) Name() string { return "go_build_passes" }

func (c GoBuildPasses) Run(d Diff, ctx RunContext) ([]Finding, error) {
	hasGo := false

	for _, p := range d.ModifiedPaths() {
		if strings.HasSuffix(p, ".go") {
			hasGo = true
			break
		}
	}

	if !hasGo {
		return nil, nil
	}

	if strings.TrimSpace(ctx.ProjectRoot) == "" {
		return nil, nil
	}

	// Skip when the project root has no go.mod. Fixture-based tests and
	// non-module Go trees ("GOPATH-style", or unrelated diffs that happen
	// to touch .go files) would otherwise trip Go's "directory does not
	// contain main module" error and surface it as a false-positive build
	// failure.
	if _, err := os.Stat(filepath.Join(ctx.ProjectRoot, "go.mod")); err != nil {
		return nil, nil
	}

	result := commandcheck.Run(context.TODO(), commandcheck.Request{
		Argv:         []string{"go", "build", "./..."},
		Dir:          ctx.ProjectRoot,
		CaptureBytes: commandcheck.DiagnosticCaptureBytes,
		MergeOutput:  true,
	})
	outcome := commandcheck.ProjectOutcome(result, commandcheck.OutcomePolicy{})
	if outcome.Failure == commandcheck.FailureNone {
		return nil, nil
	}

	if outcome.Failure != commandcheck.FailureExit {
		return nil, outcome.Err
	}

	out := outcome.Output
	re := regexp.MustCompile(`(?m)^([^:\s]+):(\d+):\d+:\s*(.+)$`)
	matches := re.FindAllStringSubmatch(out, -1)

	if len(matches) == 0 {
		return []Finding{{
			Check:    "go_build_passes",
			Severity: SeverityError,
			Message:  strings.TrimSpace(out),
		}}, nil
	}

	var findings []Finding

	for _, m := range matches {
		var line int

		fmt.Sscanf(m[2], "%d", &line)

		findings = append(findings, Finding{
			Check:    "go_build_passes",
			Severity: SeverityError,
			Path:     m[1],
			Line:     line,
			Message:  "go build: " + m[3],
		})
	}

	return findings, nil
}
