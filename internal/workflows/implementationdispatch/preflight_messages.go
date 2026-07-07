package implementationdispatch

import (
	"fmt"
	"strings"
)

func HumanPlanDocumentPreflightBlockedMessage(results []PlanDocumentPreflightResult) string {
	var b strings.Builder

	b.WriteString("Plan-execute preflight blocked dispatch before the planner ran.\n\n")
	b.WriteString("The referenced document is not a dispatch-ready plan-execute brief yet. Convert it through refine or rewrite it with the canonical `## Files To Modify`, verified current state, exact `old_text`/`new_text` for existing files or missing/new-file `replace_all` edit shape, and acceptance commands before retrying.\n")

	for _, result := range results {
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = "(unknown plan document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(path)
		b.WriteString("`")

		if len(result.Blockers) == 0 {
			b.WriteString(": preflight failed without a structured blocker")

			continue
		}

		b.WriteString(":\n")

		for _, blocker := range result.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("  - ")
			b.WriteString(blocker)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func HumanPlanDocumentResolutionBlockedMessage(blockers []ResolutionBlocker) string {
	var b strings.Builder

	b.WriteString("Plan-execute dispatch blocked before the planner ran.\n\n")
	b.WriteString("The TUI must resolve referenced design documents into a concrete brief and edit scope before plan-execute. Resolve the document path, then retry.\n")

	for _, blocker := range blockers {
		ref := strings.TrimSpace(blocker.Ref)
		if ref == "" {
			ref = "(unknown design document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(ref)
		b.WriteString("`")

		switch strings.TrimSpace(blocker.Reason) {
		case "ambiguous":
			b.WriteString(": ambiguous shorthand matched multiple markdown files:")

			for _, candidate := range blocker.Candidates {
				candidate = strings.TrimSpace(candidate)
				if candidate == "" {
					continue
				}

				b.WriteString("\n  - `")
				b.WriteString(candidate)
				b.WriteString("`")
			}
		default:
			b.WriteString(": no readable markdown design document matched this reference")
		}
	}

	return strings.TrimSpace(b.String())
}

func PreflightCanRefine(results []PlanDocumentPreflightResult) bool {
	for _, result := range results {
		if len(result.LintBlockers) > 0 {
			return true
		}

		switch strings.TrimSpace(result.AtomState) {
		case "missing-scope":
			return true
		}
	}

	return false
}

func HumanPlanDocumentRefineFailedMessage(preflight []PlanDocumentPreflightResult, refineResults []PlanDocumentRefineResult) string {
	var b strings.Builder

	b.WriteString("Plan-execute preflight blocked dispatch, and automatic refine did not produce an accepted brief.\n\n")
	b.WriteString("The TUI tried the Colony-style refine step, but the refined artifact still needs human review before plan-execute dispatch.\n")

	for _, result := range refineResults {
		path := strings.TrimSpace(result.OriginalPath)
		if path == "" {
			path = "(unknown plan document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(path)
		b.WriteString("`")

		if strings.TrimSpace(result.OutputPath) != "" {
			b.WriteString(" -> `")
			b.WriteString(result.OutputPath)
			b.WriteString("`")
		}

		if result.ExitCode != 0 {
			b.WriteString(fmt.Sprintf(": refine exited %d", result.ExitCode))
		} else {
			b.WriteString(": refine did not return a readable accepted brief")
		}

		if result.Err != nil {
			b.WriteString(" (")
			b.WriteString(result.Err.Error())
			b.WriteString(")")
		}

		for _, diag := range RefineDiagnostics(result) {
			diag = strings.TrimSpace(diag)
			if diag == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(diag)
		}
	}

	if len(preflight) > 0 {
		b.WriteString("\n\nOriginal preflight blockers:")

		for _, result := range preflight {
			for _, blocker := range result.Blockers {
				blocker = strings.TrimSpace(blocker)
				if blocker == "" {
					continue
				}

				b.WriteString("\n- ")
				b.WriteString(blocker)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func RefineDiagnostics(result PlanDocumentRefineResult) []string {
	var diagnostics []string

	for _, text := range []string{result.Stderr, result.Stdout} {
		for _, line := range strings.Split(text, "\n") {
			line = compactWhitespace(strings.TrimSpace(line))
			if line == "" {
				continue
			}

			lower := strings.ToLower(line)
			switch {
			case strings.Contains(lower, "refine:"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "brief lint:"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "single-purpose decomposition failed"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "exact-anchor specificity failed"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "plan-execute new-file body missing"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "accepted.md"):
				diagnostics = append(diagnostics, line)
			}
		}
	}

	return appendUniqueStrings(nil, diagnostics...)
}

func HumanPlanDocumentRefinePostPreflightBlockedMessage(refineResults []PlanDocumentRefineResult, blocked []PlanDocumentPreflightResult) string {
	var b strings.Builder

	b.WriteString("Automatic refine produced a brief, but intake/lint still blocked plan-execute dispatch.\n")

	for _, result := range refineResults {
		if strings.TrimSpace(result.OutputPath) != "" {
			b.WriteString("\nRefined brief: `")
			b.WriteString(result.OutputPath)
			b.WriteString("`")
		}
	}

	b.WriteString("\n\nRemaining blockers:")

	for _, result := range blocked {
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = "(unknown brief)"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`")

		for _, blocker := range result.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(blocker)
		}
	}

	return strings.TrimSpace(b.String())
}

func PreflightNeedsAtomization(results []PlanDocumentPreflightResult) bool {
	for _, result := range results {
		diagnostics := strings.ToLower(strings.Join(append(append([]string{}, result.Blockers...), result.LintBlockers...), "\n"))
		if strings.Contains(diagnostics, "single-purpose decomposition failed") ||
			(strings.Contains(diagnostics, "decompose into") && strings.Contains(diagnostics, "sub-atoms")) {

			return true
		}
	}

	return false
}

func PreflightSourceSatisfiedRecords(results []PlanDocumentPreflightResult) []PreflightSourceSatisfiedRecord {
	records := make([]PreflightSourceSatisfiedRecord, 0, len(results))
	for _, result := range results {
		records = append(records, PreflightSourceSatisfiedRecord{
			Path:         result.Path,
			AtomState:    result.AtomState,
			Blockers:     append([]string(nil), result.Blockers...),
			LintBlockers: append([]string(nil), result.LintBlockers...),
		})
	}

	return records
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
