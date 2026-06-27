package planexecute

import (
	"fmt"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

func PlanExecuteReviewRepairPrompt(originalPrompt string, plan planartifact.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence ReviewRepairEvidence) string {
	var b strings.Builder

	b.WriteString("# Plan-then-execute review-repair phase\n\n")
	b.WriteString("You are the REVIEW-REPAIR planner for a plan-execute run that already applied executor edits. Review the actual diff and behavioral contract, then either submit a narrow corrective plan or explicitly pass.\n\n")
	b.WriteString("## Required Review Contract\n\n")
	b.WriteString("Use the Phase Evidence Bundle, Diff Evidence, submitted_plan_state, source_sanity, verification entries, and inherited context packet as the primary evidence ledger. Do not restart discovery as if this were a fresh review.\n\n")

	if reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence); reason != "" {
		fmt.Fprintf(&b, "Phase Evidence already has failed or open obligations (`%s`). Treat that obligation as sufficient post-edit evidence: do not perform additional reads, searches, or context refreshes before choosing the terminal outcome. Submit a corrective plan if it can be repaired inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` naming the exact failed or open obligation.\n\n", reason)
	} else {
		b.WriteString("If exact post-edit evidence is missing, make one targeted probe: refresh/check context state with `context_status` (`refresh=true`) or use `context_query`, `context_zoom`, `code_navigate`, or one bounded `read_file` range for one named checklist fact. Do not sweep large files with unbounded or multi-hundred-line raw reads; broad raw reads waste the review budget and duplicate the sidecar evidence.\n\n")
	}

	b.WriteString("Create a `Verification Checklist` before deciding. This checklist must verify behavior, not just literal code correctness. Cover:\n\n")
	b.WriteString("- Required behavior from the original task/spec.\n")
	b.WriteString("- Default/non-regression behavior that should remain equivalent.\n")
	b.WriteString("- Data, lifecycle, state, persistence, concurrency/threading, and performance invariants when relevant.\n")
	b.WriteString("- Edge cases and error/empty/default paths implied by the change.\n")
	b.WriteString("- Scope safety: changed paths, out-of-scope writes, duplicate/bespoke logic, and stale-artifact risk.\n")
	b.WriteString("- Test or command evidence available now, plus any planner-owned verification that still must run outside this lane.\n\n")
	b.WriteString("Outcomes:\n\n")
	b.WriteString("- If any checklist item fails and the repair is inside Allowed Repair Paths, call `submit_plan` once with corrective edits only. The `submit_plan.rationale` must start with `Verification Checklist:` and include the checklist plus the failing item each edit addresses.\n")
	b.WriteString("- Corrective `submit_plan.edits[]` entries must be executable final content, not review prose: no placeholders, ellipses, `TODO` markers, `existing code` comments, omitted bodies, pseudo-code, or invented symbols. Use `insert_after_line`+`content` only for small inline insertions after an existing line. Use line-range `content` for non-trivial source/test replacements and large additions; it is the complete replacement for the inclusive range and may include unchanged anchor line(s) plus inserted content. Use `old_text` only for tiny exact snippets copied from diff/source evidence. Use `replace_all:true` with empty `old_text` and complete `new_text` only for missing/new files; existing source/test files must use bounded anchored edits. The top-level `edits` value is a JSON array, not a quoted JSON string.\n")
	b.WriteString("- The submitted plan is pre-executor intent. If `submitted_plan_state` says those edits are already applied, do not re-submit them; submit only new corrective edits for defects found by the checklist.\n")
	b.WriteString("- If the diff satisfies the checklist, do not call `submit_plan`; answer with `Decision: pass` and include the `Verification Checklist`. Empty corrective plans are invalid.\n")
	b.WriteString("- If the issue is ambiguous or outside Allowed Repair Paths, stop with `<<QUESTION>>:` and name the blocker. Do not improvise out-of-scope writes.\n\n")
	b.WriteString(plancontract.ReviewRepairTerminalTemplates())
	b.WriteString("\n")
	b.WriteString("If Phase Evidence lists `requested_test_edit_missing`, `Decision: pass` is invalid: a typed test-edit obligation is still unsatisfied because this run changed no test path. Submit a corrective test edit only if it is inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the missing test-edit scope.\n\n")
	b.WriteString("If Phase Evidence lists `requested_implementation_edit_missing`, `Decision: pass` is invalid: a typed implementation-edit obligation is still unsatisfied because this run changed only test paths. Submit a corrective implementation edit if it is inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the missing source-edit scope.\n\n")
	b.WriteString("If Phase Evidence lists `required_path_uncovered:`, `Decision: pass` is invalid: a required path was neither changed nor covered by typed current-state source-satisfied evidence. Submit a corrective edit if it is inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the uncovered required path.\n\n")
	b.WriteString("If Phase Evidence lists `verification_failed:` or any verification/source_sanity check with status `failed`, `Decision: pass` is invalid. Submit a corrective plan if the failing checklist item can be repaired inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the exact failed check.\n\n")
	b.WriteString("Planner-owned build/test/scenario acceptance commands are pending outside this lane unless Phase Evidence names the exact command as successful. Do not mark them checked/pass or say they ran.\n\n")
	b.WriteString("Patch hygiene and source_sanity may be marked checked only when Phase Evidence shows those results. Unsupported or absent source_sanity is not a failure for non-source files.\n\n")
	b.WriteString("Do not call `ask` or external question tools in this phase. If blocked, return `<<QUESTION>>:` with the blocker.\n\n")
	b.WriteString("A repair that replaces missing behavioral evidence with a tautology (`assert true`, `CHECK(true)`, `_assert_true(true)`) or with comments admitting behavior is not inspected is not a valid repair. Submit a concrete state assertion or stop with `<<QUESTION>>:`.\n\n")
	b.WriteString("## Allowed Repair Paths\n\n")

	for _, path := range allowedRepairPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}

	b.WriteString("\n## Changed Paths\n\n")

	for _, path := range changedPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}

	b.WriteString("\n## Submitted Plan\n\n")

	if strings.TrimSpace(plan.Rationale) != "" {
		b.WriteString("Rationale:\n")
		b.WriteString(truncatePlanExecuteText(plan.Rationale, 4000))
		b.WriteString("\n\n")
	}

	for i, edit := range plan.Edits {
		fmt.Fprintf(&b, "%d. `%s`: %s\n", i+1, edit.Path, firstNonEmptyString(edit.Purpose, "planned edit"))
	}

	b.WriteString("\n## Phase Evidence Bundle\n\n")
	b.WriteString("Use this bundle as the current evidence ledger for the review decision. Treat open obligations as items that must be resolved by the checklist, a narrow repair, or a blocker.\n\n")
	b.WriteString("```text\n")
	b.WriteString(renderPlanExecutePhaseEvidence(evidence.PhaseEvidence))
	b.WriteString("```\n")
	b.WriteString("\n## Diff Evidence\n\n")

	if strings.TrimSpace(evidence.Diff) == "" {
		b.WriteString("(No git diff available or changed paths are not tracked by git.)\n")
	} else {
		b.WriteString("```diff\n")
		b.WriteString(truncatePlanExecuteText(evidence.Diff, 60000))
		b.WriteString("\n```\n")
	}

	b.WriteString("\n## Patch Hygiene Evidence\n\n")
	fmt.Fprintf(&b, "current-run whitespace check exit: %d\n\n", evidence.DiffCheckExit)

	if strings.TrimSpace(evidence.DiffCheck) != "" {
		b.WriteString("```text\n")
		b.WriteString(truncatePlanExecuteText(evidence.DiffCheck, 12000))
		b.WriteString("\n```\n")
	}

	b.WriteString("\n## Original Task / Spec\n\n")
	b.WriteString("Reference only. The original spec is quoted so its file allowlist does not become this review pass's active write contract.\n\n")
	b.WriteString(quotePlanExecuteReference(truncatePlanExecuteText(originalPrompt, 40000)))

	return b.String()
}

func quotePlanExecuteReference(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "> (empty)\n"
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}

		lines[i] = "> " + line
	}

	return strings.Join(lines, "\n") + "\n"
}

func planExecuteReviewRepairRejectedPassRetryPrompt(prompt string, reason string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n## Rejected Previous Review Outcome\n\n")
	fmt.Fprintf(&b, "`Decision: pass` is not an allowed terminal outcome while `%s` remains unresolved. Submit a corrective plan if the repair is inside Allowed Repair Paths; otherwise answer `<<QUESTION>>:` with the exact blocker. Do not repeat `Decision: pass` unless the phase evidence and checklist no longer contain the rejected condition.\n", strings.TrimSpace(reason))
	return b.String()
}

func planExecuteReviewRepairInvalidOutcomeRetryPrompt(prompt string, answer string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n## Rejected Previous Review Outcome\n\n")
	b.WriteString("The previous response did not produce a machine-readable terminal outcome. Do not answer with a read-only explanation. Submit a corrective plan with `submit_plan`, answer `Decision: pass` with a `Verification Checklist`, or answer `<<QUESTION>>:` with the exact blocker.\n\n")
	if trimmed := strings.TrimSpace(answer); trimmed != "" {
		b.WriteString("Previous response excerpt:\n")
		b.WriteString("```text\n")
		b.WriteString(truncatePlanExecuteText(trimmed, 4000))
		b.WriteString("\n```\n")
	}
	return b.String()
}

func truncatePlanExecuteText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}

	for i := range text {
		if i >= maxBytes {
			return text[:i] + fmt.Sprintf("\n\n[truncated %d bytes]\n", len(text)-i)
		}
	}

	return text
}
