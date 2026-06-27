package planexecute

import (
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

func ReviewAnswerPassContradictionReason(answer string) string {
	for _, rawLine := range strings.Split(answer, "\n") {
		line := normalizeReviewAnswerLine(rawLine)
		if line == "" || strings.Contains(line, "decision: pass") {
			continue
		}

		if reviewLineHasPassNegation(line) {
			continue
		}

		switch {
		case plancontract.ReviewRepairLineIsPlaceholderChecklist(line):
			return "placeholder_checklist_item"
		case strings.Contains(line, "❌"), strings.Contains(line, "[ ]"):
			if reviewLineIsPlannerOwnedPending(line) {
				continue
			}

			return "failed_checklist_item"
		case reviewLineContainsAny(line,
			"appears to be missing",
			"is missing",
			"are missing",
			"missing from",
			"missing the",
			"not present",
			"not implemented",
			"does not implement",
			"doesn't implement",
			"does not satisfy",
			"doesn't satisfy",
			"fails the checklist",
			"checklist item fails",
			"required behavior fails",
			"not visible",
			"not shown",
			"not available in the diff",
			"not visible in the diff",
			"cannot verify",
			"can't verify",
			"could not verify",
			"unable to verify",
			"not enough evidence",
			"insufficient evidence",
			"no evidence",
		):
			return "unresolved_failure_claim"
		case reviewLineContainsProjectedVerificationClaim(line):
			return "unsupported_verification_projection"
		case reviewLineContainsMissingCommandEvidenceClaim(line):
			if reviewLineIsPlannerOwnedPending(line) {
				continue
			}

			return "missing_command_evidence_claim"
		}
	}

	return ""
}

func reviewLineContainsMissingCommandEvidenceClaim(line string) bool {
	return reviewLineContainsAny(line,
		"didn't run ",
		"did not run ",
		"haven't run ",
		"hasn't run ",
		"wasn't run",
		"weren't run",
		"was not run",
		"were not run",
		"not run ",
		"no test command was run",
		"no verification command was run",
	)
}

func reviewLineIsPlannerOwnedPending(line string) bool {
	return reviewLineContainsAny(line,
		"planner-owned",
		"planner owned",
		"pending outside this lane",
		"pending outside lane",
		"must run outside this lane",
		"not run by this lane",
	)
}

func normalizeReviewAnswerLine(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.TrimLeft(line, "-*0123456789. )\t")

	return strings.Join(strings.Fields(line), " ")
}

func reviewLineHasPassNegation(line string) bool {
	return reviewLineContainsAny(line,
		"no missing",
		"not missing",
		"nothing missing",
		"without missing",
		"no issues",
		"no issue",
		"no failures",
		"no failure",
		"not failed",
		"not a failure",
		"no failed",
		"no unresolved",
		"no blocker",
		"no blockers",
	)
}

func reviewLineContainsProjectedVerificationClaim(line string) bool {
	if !reviewLineContainsAny(line, "should pass", "should still pass", "should pass with", "would pass") {
		return false
	}

	return reviewLineContainsAny(line,
		"test",
		"tests",
		"scenario",
		"scenarios",
		"command",
		"verification",
		"acceptance",
		"git diff",
		"diff --check",
		"go test",
		"pytest",
		"run_scenarios",
	)
}

func reviewLineContainsAny(line string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}

	return false
}
