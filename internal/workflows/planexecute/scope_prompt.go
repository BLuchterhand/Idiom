package planexecute

import (
	"fmt"
	"strings"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
)

func appendPlanExecuteScopeToBriefBody(prompt string, requiredEditPaths []string, allowedEditPaths []string) string {
	section := planExecuteScopeSection(requiredEditPaths, allowedEditPaths)
	if section == "" {
		return prompt
	}

	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func planExecuteScopeSection(requiredEditPaths []string, allowedEditPaths []string) string {
	required := plancontract.UniqueScopePaths(requiredEditPaths)
	allowed := plancontract.UniqueScopePaths(allowedEditPaths)

	allowed = plancontract.AllowedScopePathsExcludingRequired(allowed, required)

	if len(required) == 0 && len(allowed) == 0 {
		return ""
	}

	var b strings.Builder

	if len(required) > 0 {
		b.WriteString("## Files to Modify\n\n")
		b.WriteString("required_edit_paths:\n")

		for _, path := range required {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}

	if len(allowed) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}

		b.WriteString("## Candidate Edit Scope From Target References\n\n")
		b.WriteString("allowed_edit_paths:\n")

		for _, path := range allowed {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

func planExecuteRequiredPathChecklist(requiredEditPaths []string) string {
	paths := plancontract.UniqueScopePaths(requiredEditPaths)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Required path coverage for this run: %d required path(s).\n", len(paths))

	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}

	b.WriteString("After every existing required path has satisfied its source-body obligation, do not perform additional source reads. Read runtime-required git history evidence for every listed existing required path (new files satisfy this automatically). If at least one required path needs a change, your first successful `submit_plan` must cover every path above with real executable edits for changed paths and top-level `source_satisfied_paths` only for already-final paths. If every required path already contains the requested final state, do not call `submit_plan`; answer `Decision: source-satisfied` with per-path current-state evidence. Do not submit no-op edits for source-satisfied paths. Do not treat a source read or history read as plan coverage. Do not submit a partial plan and do not answer in prose while any listed path remains uncovered.")

	return strings.TrimSpace(b.String())
}

func planExecuteAllowedPathChecklist(allowedEditPaths []string) string {
	paths := plancontract.UniqueScopePaths(allowedEditPaths)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Allowed `submit_plan` edit scope for this run: %d candidate path(s).\n", len(paths))

	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}

	b.WriteString("These paths are an allowlist, not coverage obligations. Submit edits only for the paths needed by the objective, and do not submit outside paths.")

	return strings.TrimSpace(b.String())
}
