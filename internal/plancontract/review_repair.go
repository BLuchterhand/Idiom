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
		"- Required behavior: <checked fact from Phase/Diff Evidence>.",
		"- Scope safety: <checked fact from changed paths and allowed repair paths>.",
		"- Test or command evidence: <checked fact, or pending outside this lane>.",
		"",
		"Corrective plan shape:",
		"{\"tool\":\"submit_plan\",\"rationale\":\"Verification Checklist:\\n- <checked fact>.\\n- Failing item: <why this corrective edit is required>.\",\"edits\":[{\"path\":\"path/from/Allowed Repair Paths\",\"old_text\":\"exact current source snippet\\n\",\"new_text\":\"complete replacement snippet\\n\",\"purpose\":\"fix the failing checklist item\"}]}",
		"",
		"Blocker shape:",
		"<<QUESTION>>: <exact blocker, including the path or checklist item that cannot be resolved inside Allowed Repair Paths>",
		"",
	}, "\n")
}
