package implementationdispatch

import "strings"

func SanitizeNonzeroRefineAcceptedContent(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	skippingLeadingWarning := true

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skippingLeadingWarning {
			if trimmed == "" {
				if changed {
					continue
				}

				out = append(out, line)

				continue
			}

			if strings.HasPrefix(trimmed, "> WARNING: refine exited non-zero") {
				changed = true
				continue
			}

			skippingLeadingWarning = false
		}

		if strings.HasPrefix(trimmed, "<!-- idiom:refine-drafted") && !strings.Contains(trimmed, "reason=ship-as-is") {
			out = append(out, "<!-- idiom:tui-refine-accepted-after-structural-preflight -->")
			changed = true

			continue
		}

		out = append(out, line)
	}

	return strings.TrimLeft(strings.Join(out, "\n"), "\n"), changed
}

func ReplacePlanDocument(planDocuments []PlanDocument, originalPath string, refined PlanDocument) []PlanDocument {
	var out []PlanDocument

	replaced := false

	for _, doc := range planDocuments {
		if doc.Path == originalPath {
			if len(refined.AuthorizedNewFiles) == 0 {
				refined.AuthorizedNewFiles = append([]string{}, doc.AuthorizedNewFiles...)
			}

			out = append(out, refined)
			replaced = true

			continue
		}

		out = append(out, doc)
	}

	if !replaced {
		out = append(out, refined)
	}

	return out
}
