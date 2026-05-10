package postflight

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
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
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = ctx.ProjectRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil, nil
	}
	if _, ok := err.(*exec.ExitError); !ok {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^([^:\s]+):(\d+):\d+:\s*(.+)$`)
	matches := re.FindAllStringSubmatch(string(out), -1)
	if len(matches) == 0 {
		return []Finding{{
			Check:    "go_build_passes",
			Severity: SeverityError,
			Message:  strings.TrimSpace(string(out)),
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
