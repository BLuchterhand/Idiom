package planexecute

import (
	"fmt"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

func PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths []string, allowedEditPaths []string, obligations plancontract.EditSetObligations) string {
	lines := []string{
		"# Plan-then-execute (planner phase)",
		"",
		"Your job is to produce an executable plan artifact, not a narrative implementation answer. The executor implements accepted plan edits after you stop.",
		"Treat the brief body and any preloaded Context Pack below as the planner target packet.",
		"",
		"Visible response protocol — choose exactly one action per turn:",
		"1. If a required_edit_path does not exist on disk, treat it as a new file only when the brief explicitly authorizes new-file/full-body/create work. Without that authorization, do not invent the path; answer `<<QUESTION>>:` with the missing-path localization blocker.",
		"2. If an existing required_edit_path has not produced source-body evidence in this planner turn, call exactly one source-read tool for that path. Use `read_file`; if it redirects to an outline, follow with `code_navigate` `read_symbol` or bounded `read_file` with `start_line`/`end_line`. Do not narrate the read.",
		"3. Once every existing required_edit_path has satisfied its source-body obligation, call `history_range` or `history_symbol` for each selected existing edit path that still lacks required git history evidence, only when the active runtime contract requires git history evidence. Do not perform additional source reads in that phase. After history evidence is present for every selected existing edit path, the next visible action must be exactly one terminal outcome: `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:`.",
		fmt.Sprintf("4. If the requested final state is already present, answer `Decision: source-satisfied` and cite current-state evidence for every required path. Do not use unsupported inference language such as %s; if any acceptance criterion is only inferred, submit a grounded plan or answer `<<QUESTION>>:` with the blocker.", planExecuteSourceSatisfiedUnsupportedEvidenceMarkerListForPrompt()),
		"5. If the plan cannot be produced from available evidence, answer `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Plan artifact rules:",
		"1. Per-edit write capabilities are blocked in the planner phase; the available write contract is `submit_plan`.",
		"2. Each `submit_plan.edits[]` entry must be a JSON object with `path` + ONE edit shape: (`insert_after_line`+small inline non-empty `content` for insertion after an existing line | `start_line`+`end_line`+non-empty `content` for replacement | `old_text`+`new_text`; empty `new_text` deletes `old_text` | `replace_all:true`+`new_text`+empty `old_text` for missing/new files only) + one-sentence `purpose`. `edits` is an array, never a quoted JSON string. Existing source/test files must use a bounded anchored edit shape, not whole-file `replace_all`. Prefer the smallest executable edit body that satisfies the objective: use exact `old_text`/`new_text` for small unique replacements, and use numeric line-range edits only when the replacement spans more than a few lines, contains JSON-hostile syntax, or needs a large insertion anchored to unchanged source lines. Large bodies are spilled to artifact refs after submit_plan except insert_after_line content.",
		"3. Each edit body must be executable final content. Do not submit placeholders, ellipses, `TODO` markers, `existing code` comments, omitted bodies, pseudo-code, prose descriptions, invented symbols, no-op assertions, or self-disqualifying proof text such as `simplified check`, `for now we just verify`, or `verify that it exists` in `old_text`, `new_text`, or `content`.",
		"4. `insert_after_line` inserts small inline non-empty `content` after an existing 1-based line and does not replace current lines; do not use it for large generated blocks. Line-range `content` is the complete replacement for the inclusive range and may include unchanged anchor line(s) plus inserted content for large additions. `old_text` is exact current text copied from source-body evidence. Missing/new-file `new_text` is the complete file body.",
		"5. One insertion site = one edit. Don't combine non-adjacent sites into one `old_text` (use \\n\\n only when sites are literally adjacent). Same `path` in two entries is fine.",
		"6. Substring-anchor indentation must match the file byte-for-byte. Brief code blocks under markdown bullets carry 2+ extra leading spaces. Before using any multi-line substring anchor, one bounded source-read capability may verify that path and strip the rendering indent. Single-line / identifier anchors are exempt.",
		"7. Exact anchors, current-state anchors, full new-file bodies, explicit replacement edit shapes, unified patches, and ranked packet evidence can identify the intended edit, but required existing paths still need source-body obligations satisfied before submit_plan unless the run short-circuits as source-satisfied or exact-plan-ready before planner execution.",
		"8. Use source-read capability only for a new named edit-safety fact on a required or candidate path. Do not re-read for confidence; if the brief names a missing required source/test path but gives only behavior or helper-shape requirements with no substantial body/anchor, stop with `<<QUESTION>>:` and name the missing body evidence.",
		"9. Test/source edit coverage requirements are enforced only by typed `Requested edit-set obligations` attached below. Without that section, choose the complete plan, source-satisfied closeout, or exact blocker from required/candidate paths and gathered evidence; do not infer hidden edit-set obligations from objective wording.",
		"10. Submit as soon as you have source anchors plus runtime-required git history evidence for every selected existing edit path. With required_edit_paths, every required existing path is selected unless it is explicitly source-satisfied or blocked; with candidate-only scope, only the needed candidate paths are selected. If current source already contains the requested final state, answer `Decision: source-satisfied` and name every relevant path plus evidence instead of submitting stale edits. After `submit_plan` returns OK, finalize with a one-paragraph summary and stop. Rejected `submit_plan` calls are invalid attempts, not successful plan artifacts. No post-plan verification reads.",
		"11. After replacement, the resulting file must be syntactically valid. Verify that line-range `content` does not leave control flow statements (`if`, `for`, `switch`, `return`) at package scope outside any function or method body. A bare `if`, `for`, `switch`, or `return` that ends up between top-level declarations will be rejected by preflight syntax validation.",
		"",
		"Canonical `submit_plan` shape (real values, never placeholders): emit one visible JSON object with top-level `\"tool\":\"submit_plan\"`, a one-sentence `rationale`, and an `edits` array. Each edit object must use a real required or selected path from gathered evidence, plus exactly one executable edit shape such as `insert_after_line` with inline content, `start_line`/`end_line` with complete replacement `content`, exact `old_text`/`new_text`, or missing/new-file-only `replace_all:true` with full `new_text`.",
	}
	if checklist := planExecuteRequiredPathChecklist(requiredEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else if checklist := planExecuteAllowedPathChecklist(allowedEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else {
		lines = append(lines, "", "No required edit paths were provided. Use the objective's candidate/target refs as the planning scope: you still must either call `submit_plan` with real edits grounded in the objective/target refs, answer `Decision: source-satisfied` with evidence, or answer `<<QUESTION>>:` with the exact blocker.")
	}

	if checklist := planExecuteEditSetObligationChecklist(obligations); checklist != "" {
		lines = append(lines, "", checklist)
	}

	return strings.Join(lines, "\n")
}

func planExecuteEditSetObligationChecklist(obligations plancontract.EditSetObligations) string {
	obligations = obligations.Normalized()
	if len(obligations) == 0 {
		return ""
	}

	var lines []string

	lines = append(lines, fmt.Sprintf("Requested edit-set obligations for this run: %d obligation(s).", len(obligations)))

	if obligations.Has(plancontract.EditSetObligationKindTestEdit) {
		lines = append(lines, "- `test_edit_required`: `submit_plan.edits`, source-satisfied evidence, or a corrective review plan must include at least one real test edit or test-path evidence.")
	}

	if obligations.Has(plancontract.EditSetObligationKindImplementationEdit) {
		lines = append(lines, "- `implementation_edit_required`: `submit_plan.edits`, source-satisfied evidence, or a corrective review plan must include at least one real non-test implementation edit or implementation-path evidence.")
	}

	lines = append(lines, "These obligations do not add or promote `required_edit_paths`; candidate and allowed paths remain scope only.")

	return strings.Join(lines, "\n")
}
