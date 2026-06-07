package plancontract

import "strings"

func ReviewRepairAnswerDeclaresPass(answer string) bool {
	startedWithChecklist := false
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "Decision: pass") {
			return true
		}
		if !startedWithChecklist {
			if ReviewRepairLineIsChecklistHeading(line) {
				startedWithChecklist = true
				continue
			}
			return false
		}
	}
	return false
}

func ReviewRepairTextHasChecklist(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if ReviewRepairLineIsChecklistHeading(line) || strings.HasPrefix(line, "Verification Checklist:") {
			return true
		}
	}
	return false
}

func ReviewRepairLineIsChecklistHeading(line string) bool {
	return strings.EqualFold(line, "Verification Checklist") || strings.EqualFold(line, "Verification Checklist:")
}

func ReviewRepairLineIsPlaceholderChecklist(line string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.TrimLeft(line, "-*0123456789.[] x")
	line = strings.TrimSpace(line)
	return strings.Contains(line, "cite a checked fact") ||
		strings.Contains(line, "required behavior: cite") ||
		strings.Contains(line, "scope safety: cite") ||
		strings.Contains(line, "test or command evidence: cite")
}

func ReviewRepairTerminalTemplates() string {
	return strings.Join([]string{
		"## Terminal Output Templates",
		"",
		"Your visible response must use exactly one of these shapes. Do not add prose before or after the chosen shape.",
		"",
		"Pass shape:",
		"Decision: pass",
		"",
		"Verification Checklist:",
		"",
		"For the pass shape, write your own checklist bullets under `Verification Checklist:`. Each bullet must cite a concrete fact from Phase/Diff Evidence, changed paths, allowed repair paths, or an explicitly successful command. Do not copy placeholder checklist text.",
		"",
		"Corrective plan shape:",
		"Emit one parser-readable submit_plan object whose rationale starts with `Verification Checklist:` and whose edits use only real allowed repair paths plus executable source text from the evidence. Do not copy placeholder paths, snippets, ellipses, or schema examples.",
		"",
		"Blocker shape:",
		"<<QUESTION>>: followed by the exact blocker, including the path or checklist item that cannot be resolved inside Allowed Repair Paths.",
		"",
	}, "\n")
}
