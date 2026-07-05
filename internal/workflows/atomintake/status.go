package atomintake

import (
	"fmt"
	"strings"

	atomintakecontract "idiom/internal/contracts/atomintake"
)

func atomStatusTerminalStateFromBody(specBody string) (atomintakecontract.IntakeState, string) {
	status, ok := SpecStatus(specBody)
	if !ok {
		return "", ""
	}

	return atomStatusTerminalState(status)
}

func SpecTerminalStatus(specBody string) (string, bool) {
	status, ok := SpecStatus(specBody)
	if !ok {
		return "", false
	}

	return TerminalStatus(status)
}

func TerminalStatus(status string) (string, bool) {
	status = strings.TrimSpace(status)
	if status == "" {
		return "", false
	}

	state, _ := atomStatusTerminalState(status)
	return status, state != ""
}

func atomStatusTerminalState(status string) (atomintakecontract.IntakeState, string) {
	if strings.TrimSpace(status) == "" {
		return "", ""
	}

	lower := strings.ToLower(status)
	if strings.Contains(lower, "do not dispatch") || strings.Contains(lower, "source-satisfied") || strings.Contains(lower, "source satisfied") {
		return atomintakecontract.StateSourceSatisfied, fmt.Sprintf("status %q marks the atom as already satisfied or non-dispatchable", status)
	}

	switch firstAtomStatusToken(lower) {
	case "accepted", "complete", "completed", "closed":
		return atomintakecontract.StateComplete, fmt.Sprintf("status %q marks the atom as already complete", status)
	default:
		return "", ""
	}
}

func atomFailedRefineState(specBody string) (atomintakecontract.IntakeState, string) {
	reason, ok := RefineDraftReason(specBody)
	if !ok || reason == "" || reason == "ship-as-is" {
		return "", ""
	}

	return atomintakecontract.StateFailedRefine, fmt.Sprintf("refine draft reason %q is non-dispatchable until review findings are resolved", reason)
}

func RefineDraftReason(specBody string) (string, bool) {
	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "> WARNING: refine exited non-zero") {
			if idx := strings.Index(trimmed, "reason="); idx >= 0 {
				return atomRefineReasonValue(trimmed[idx+len("reason="):]), true
			}

			return "non-zero", true
		}

		if strings.HasPrefix(trimmed, "<!-- idiom:refine-drafted") {
			fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(trimmed, "<!--"), "-->"))
			for _, field := range fields {
				if strings.HasPrefix(field, "reason=") {
					return atomRefineReasonValue(strings.TrimPrefix(field, "reason=")), true
				}
			}

			return "", true
		}

		break
	}

	return "", false
}

func atomRefineReasonValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	for i, r := range raw {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '`' || r == '>' {
			raw = raw[:i]
			break
		}
	}

	raw = strings.Trim(raw, ".,;:)")

	return strings.ToLower(raw)
}

func SpecStatus(specBody string) (string, bool) {
	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		normalized := strings.Trim(trimmed, "* ")
		if !strings.HasPrefix(strings.ToLower(normalized), "status") {
			continue
		}

		parts := strings.SplitN(normalized, ":", 2)
		if len(parts) != 2 {
			continue
		}

		label := strings.Trim(strings.TrimSpace(parts[0]), "* ")
		if !strings.EqualFold(label, "status") {
			continue
		}

		status := strings.Trim(strings.TrimSpace(parts[1]), "* ")
		if status == "" {
			continue
		}

		return status, true
	}

	return "", false
}

func firstAtomStatusToken(status string) string {
	tokens := strings.FieldsFunc(status, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '-' || r == '_' || r == '.' || r == ',' || r == ':' || r == ';'
	})
	if len(tokens) == 0 {
		return ""
	}

	return strings.TrimSpace(tokens[0])
}

func SpecHasAcceptedResult(specBody string) bool {
	lower := strings.ToLower(specBody)
	hasResultHeading := strings.HasPrefix(lower, "result:") ||
		strings.HasPrefix(lower, "## result") ||
		strings.HasPrefix(lower, "### result") ||
		strings.Contains(lower, "\nresult:") ||
		strings.Contains(lower, "\n## result") ||
		strings.Contains(lower, "\n### result")

	return hasResultHeading && strings.Contains(lower, "accepted")
}
