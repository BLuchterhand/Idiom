package postflight

import (
	"fmt"
	"strings"
)

// Result aggregates findings from one Run. Pass tracks whether the
// pipeline as a whole succeeded (no error-severity findings); the
// CLI uses this to pick its exit code.
type Result struct {
	Findings []Finding
	Pass     bool
}

// Run executes the supplied checks against the diff and returns an
// aggregated Result. Checks that error out are themselves recorded
// as Findings (severity=error, no path) so a partial-failure
// pipeline still produces a useful report rather than a single
// terse error line.
func Run(d Diff, ctx RunContext, checks []Check) Result {
	var all []Finding
	for _, c := range checks {
		findings, err := c.Run(d, ctx)
		if err != nil {
			all = append(all, Finding{
				Check:    c.Name(),
				Severity: SeverityError,
				Message:  fmt.Sprintf("check failed to run: %v", err),
			})
			continue
		}
		all = append(all, findings...)
	}
	return Result{
		Findings: all,
		Pass:     !hasErrorSeverity(all),
	}
}

// FilterChecks returns the subset of checks whose Name() is NOT in
// the skip set. Empty skip → return all. Used by the CLI's
// `--skip <name,name>` flag so an operator can defer one noisy
// check without disabling the whole pipeline.
func FilterChecks(checks []Check, skip []string) []Check {
	if len(skip) == 0 {
		return checks
	}
	skipSet := make(map[string]struct{}, len(skip))
	for _, s := range skip {
		skipSet[strings.TrimSpace(s)] = struct{}{}
	}
	out := make([]Check, 0, len(checks))
	for _, c := range checks {
		if _, drop := skipSet[c.Name()]; drop {
			continue
		}
		out = append(out, c)
	}
	return out
}

// hasErrorSeverity reports whether any finding fails the pipeline.
// Warnings and infos don't fail; the operator decides whether to
// act on them.
func hasErrorSeverity(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}
