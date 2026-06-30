package postflight

import (
	"context"
	"fmt"
	"strings"

	rulescontract "idiom/internal/contracts/codingrules"
	rulesworkflow "idiom/internal/workflows/codingrules"
)

type CodingRulesCheck struct{}

func (CodingRulesCheck) Name() string { return "coding_rules" }

func (c CodingRulesCheck) Run(d Diff, ctx RunContext) ([]Finding, error) {
	if strings.TrimSpace(ctx.ProjectRoot) == "" {
		return nil, nil
	}
	if !rulesworkflow.PolicyExists(ctx.ProjectRoot, "") {
		return nil, nil
	}

	paths := changedGoPaths(d)
	if len(paths) == 0 {
		return nil, nil
	}

	result, err := rulesworkflow.CheckWorkspace(context.Background(), rulesworkflow.CheckOptions{
		ProjectRoot: ctx.ProjectRoot,
		Paths:       paths,
	})
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, 0, len(result.Report.Findings))
	for _, finding := range result.Report.Findings {
		findings = append(findings, Finding{
			Check:    c.Name(),
			Severity: postflightSeverity(finding),
			Path:     finding.Path,
			Line:     finding.Line,
			Message:  codingRulesMessage(finding),
		})
	}

	return findings, nil
}

func changedGoPaths(d Diff) []string {
	var paths []string
	for _, path := range d.ModifiedPaths() {
		if strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
	}

	return paths
}

func postflightSeverity(finding rulescontract.Finding) Severity {
	if finding.Enforcement == rulescontract.EnforcementBlocking {
		return SeverityError
	}

	switch finding.Severity {
	case rulescontract.SeverityInfo:
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

func codingRulesMessage(finding rulescontract.Finding) string {
	if finding.Symbol == "" {
		return fmt.Sprintf("%s: %s", finding.RuleID, finding.Message)
	}

	return fmt.Sprintf("%s %s: %s", finding.RuleID, finding.Symbol, finding.Message)
}
