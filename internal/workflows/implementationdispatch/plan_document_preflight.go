package implementationdispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/atomintake"
	"idiom/internal/workflows/brieflint"
)

const defaultPlanDocumentPreflightTimeout = 45 * time.Second

type PlanDocumentsPreflightInput struct {
	WorkspaceRoot string
	WorkspaceCWD  string
	PlanDocuments []PlanDocument
	Timeout       time.Duration
	Runner        PlanDocumentPreflightRunner
}

type PlanDocumentPreflightInput struct {
	WorkspaceRoot string
	WorkspaceCWD  string
	Document      PlanDocument
	Timeout       time.Duration
}

func PreflightPlanDocument(ctx context.Context, input PlanDocumentPreflightInput) PlanDocumentPreflightResult {
	doc := input.Document
	result := PlanDocumentPreflightResult{Path: doc.Path, OK: true}
	root := WorkspaceRoot(input.WorkspaceRoot, input.WorkspaceCWD)

	if strings.TrimSpace(root) == "" {
		result.OK = false
		result.Blockers = append(result.Blockers, "workspace root is unavailable; cannot run dispatch preflight")

		return result
	}

	abs, ok := WorkspaceFile(root, doc.Path)
	if !ok {
		result.OK = false
		result.Blockers = append(result.Blockers, "referenced plan document path is outside the workspace")

		return result
	}

	atomReport, err := atomintake.RunIntake(atomintake.IntakeOptions{
		SpecPath:    abs,
		ProjectRoot: root,
		AllowDirty:  doc.AllowDirtyAtomScope,
	}, atomintake.Dependencies{})
	if err != nil {
		result.OK = false
		result.Blockers = append(result.Blockers, "atom intake failed: "+strings.TrimSpace(err.Error()))

		return result
	}

	if !atomReport.Dispatchable {
		atomState, blockers := atomIntakeBlocker(atomReport)
		result.AtomState = atomState
		result.Blockers = append(result.Blockers, blockers...)

		if atomintakecontract.StateSourceSatisfied == atomState {
			result.OK = false

			return result
		}
	}

	timeout := input.Timeout
	if timeout <= 0 {
		timeout = defaultPlanDocumentPreflightTimeout
	}

	lintReport, err := brieflint.BuildReport(ctx, abs, root, timeout)
	if err != nil {
		result.OK = false
		result.LintBlockers = append(result.LintBlockers, "brief lint failed: "+strings.TrimSpace(err.Error()))
		result.Blockers = append(result.Blockers, result.LintBlockers...)

		return result
	}

	if lintReport.Exit != 0 {
		blockers := briefLintBlockers(lintReport)
		result.LintBlockers = append(result.LintBlockers, blockers...)
		result.Blockers = append(result.Blockers, blockers...)
	} else {
		result.Warnings = append(result.Warnings, briefLintWarnings(lintReport)...)
	}

	result.OK = len(result.Blockers) == 0

	return result
}

func atomIntakeBlocker(report atomintake.IntakeReport) (atomintakecontract.IntakeState, []string) {
	state := report.State
	if state == "" {
		return "", []string{"atom intake failed: missing intake state"}
	}

	parts := []string{"atom intake state=" + string(state)}

	if len(report.Reasons) > 0 {
		parts = append(parts, strings.Join(report.Reasons, "; "))
	}

	if len(report.Dirty) > 0 {
		parts = append(parts, "dirty paths: "+strings.Join(report.Dirty, ", "))
	}

	if len(report.Invalid) > 0 {
		parts = append(parts, "invalid paths: "+strings.Join(report.Invalid, ", "))
	}

	if len(report.NextActions) > 0 {
		parts = append(parts, "next: "+strings.Join(report.NextActions, "; "))
	}

	return state, []string{strings.Join(parts, " - ")}
}

func briefLintBlockers(report brieflint.Report) []string {
	var blockers []string

	for _, finding := range report.Findings {
		if textutil.EqualTrimFold(finding.Severity, "WARN") || textutil.EqualTrimFold(finding.Severity, "INFO") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		blockers = append(blockers, "brief lint: "+message)
		if len(blockers) >= 4 {
			break
		}
	}

	if len(blockers) == 0 {
		blockers = append(blockers, fmt.Sprintf("brief lint failed with exit %d", report.Exit))
	}

	return blockers
}

func briefLintWarnings(report brieflint.Report) []string {
	var warnings []string

	for _, finding := range report.Findings {
		if !textutil.EqualTrimFold(finding.Severity, "WARN") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		warnings = append(warnings, "brief lint: "+message)
		if len(warnings) >= 3 {
			break
		}
	}

	return warnings
}
