package atomintake

import (
	"os"
	"path/filepath"
	"strings"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/contracts/pathparse"
)

type IntakeOptions struct {
	SpecPath    string
	ProjectRoot string
	JSON        bool
	AllowDirty  bool
}

type IntakeReport struct {
	Type           string                         `json:"type"`
	SpecPath       string                         `json:"spec_path"`
	ProjectRoot    string                         `json:"project_root"`
	Status         string                         `json:"status,omitempty"`
	State          atomintakecontract.IntakeState `json:"state"`
	Dispatchable   bool                           `json:"dispatchable"`
	ScopePaths     []string                       `json:"scope_paths,omitempty"`
	Existing       []string                       `json:"existing_paths,omitempty"`
	Missing        []string                       `json:"missing_paths,omitempty"`
	Invalid        []string                       `json:"invalid_paths,omitempty"`
	InvalidReasons map[string]string              `json:"invalid_path_reasons,omitempty"`
	Dirty          []string                       `json:"dirty_paths,omitempty"`
	Reasons        []string                       `json:"reasons,omitempty"`
	NextActions    []string                       `json:"next_actions,omitempty"`
}

type Dependencies struct {
	ExtractScopePaths func(specBody string) []string
}

func (d Dependencies) scopePaths(specBody string) []string {
	if d.ExtractScopePaths != nil {
		return d.ExtractScopePaths(specBody)
	}

	return DefaultScopePaths(specBody)
}

func DefaultScopePaths(specBody string) []string {
	return pathparse.ExtractDeliverableTargets(specBody, pathparse.DetectMode(specBody))
}

func RunIntake(opts IntakeOptions, deps Dependencies) (IntakeReport, error) {
	root := strings.TrimSpace(opts.ProjectRoot)
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return IntakeReport{}, err
		}

		root = wd
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return IntakeReport{}, err
	}

	specPath := strings.TrimSpace(opts.SpecPath)
	if !filepath.IsAbs(specPath) {
		specPath = filepath.Join(absRoot, specPath)
	}

	body, err := os.ReadFile(specPath)
	if err != nil {
		return IntakeReport{}, err
	}

	return EvaluateIntake(string(body), specPath, absRoot, opts.AllowDirty, deps.scopePaths(string(body))), nil
}

func EvaluateIntake(specBody, specPath, projectRoot string, allowDirty bool, scopePaths []string) IntakeReport {
	report := IntakeReport{
		Type:        "idiom.atom.intake",
		SpecPath:    specPath,
		ProjectRoot: projectRoot,
		ScopePaths:  scopePaths,
	}
	if status, ok := SpecStatus(specBody); ok {
		report.Status = status
	}

	if state, reason := atomStatusTerminalState(report.Status); state != "" {
		report.State = state
		report.Reasons = append(report.Reasons, reason)
		report.NextActions = append(report.NextActions, "do not dispatch this atom; update the tracker or choose another atom")

		return report
	}

	if state, reason := atomFailedRefineState(specBody); state != "" {
		report.State = state
		report.Reasons = append(report.Reasons, reason)
		report.NextActions = append(report.NextActions, "inspect the refine review, rerun refine from the source spec, or write a corrected dispatchable brief")

		return report
	}

	report.Existing, report.Missing, report.Invalid, report.InvalidReasons = classifyAtomScopePaths(projectRoot, scopePaths)
	report.ScopePaths = append(append([]string(nil), report.Existing...), report.Missing...)

	if len(scopePaths) == 0 {
		report.State = atomintakecontract.StateMissingScope
		report.Reasons = append(report.Reasons, "spec does not expose any scoped file paths")
		report.NextActions = append(report.NextActions, "rebase the atom/spec with an explicit Scope or Files To Modify section")

		return report
	}

	if len(report.Invalid) > 0 {
		report.State = atomintakecontract.StateInvalidScope
		report.Reasons = append(report.Reasons, atomInvalidScopeSummary(report.InvalidReasons))
		report.NextActions = append(report.NextActions, atomInvalidScopeNextAction(report.InvalidReasons))

		return report
	}

	if SpecHasAcceptedResult(specBody) {
		report.State = atomintakecontract.StateStaleNeedsRebase
		report.Reasons = append(report.Reasons, "spec has a dispatchable status but also records an accepted Result")
		report.NextActions = append(report.NextActions, "refresh the atom status/tracker before dispatch")

		return report
	}

	editEval := atomEvaluateRequiredEditSnippets(specBody, projectRoot, report.Existing, report.ScopePaths)
	if editEval.SourceSatisfied() {
		report.State = atomintakecontract.StateSourceSatisfied
		report.Reasons = append(report.Reasons, "all required edit snippets already appear in scoped source files")
		report.Reasons = append(report.Reasons, editEval.Evidence...)
		report.NextActions = append(report.NextActions, "do not dispatch this atom; mark it source-satisfied/accepted or choose another atom")

		return report
	}

	if editEval.StaleNeedsRebase() {
		report.State = atomintakecontract.StateStaleNeedsRebase
		report.Reasons = append(report.Reasons, "required edit snippets partially match or appear superseded in scoped source files")
		report.Reasons = append(report.Reasons, editEval.Evidence...)
		report.NextActions = append(report.NextActions, "refresh/rebase the atom so Required Edits reflect current source before dispatch")

		return report
	}

	if editEval.SnippetCount == 0 {
		currentStateEval := atomEvaluateCurrentStateSourceSatisfied(specBody, projectRoot, report.Existing, report.ScopePaths)
		if currentStateEval.SourceSatisfied() {
			report.State = atomintakecontract.StateSourceSatisfied
			report.Reasons = append(report.Reasons, "current source covers the atom's current-state anchored scope")
			report.Reasons = append(report.Reasons, currentStateEval.Evidence...)
			report.NextActions = append(report.NextActions, "do not dispatch this atom; mark it source-satisfied/accepted or choose another atom")

			return report
		}
	}

	if !allowDirty {
		report.Dirty = dirtyAtomScopePaths(projectRoot, report.ScopePaths)
		if len(report.Dirty) > 0 {
			report.State = atomintakecontract.StateBlockedDirty
			report.Reasons = append(report.Reasons, "existing scoped paths have uncommitted changes")
			report.NextActions = append(report.NextActions, "finish, commit, or explicitly allow the dirty scoped paths before dispatch")

			return report
		}
	}

	report.State = atomintakecontract.StateDispatchable
	report.Dispatchable = true
	report.Reasons = append(report.Reasons, "status and scoped paths are dispatchable")

	return report
}

func SpecDispatchBlocker(specBody string) string {
	state, reason := atomStatusTerminalStateFromBody(specBody)
	if state != "" {
		return reason
	}

	if _, reason := atomFailedRefineState(specBody); reason != "" {
		return reason
	}

	return ""
}
