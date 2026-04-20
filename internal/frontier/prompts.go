package frontier

import (
	"strconv"
	"strings"
)

func BuildSubagentSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	return strings.Join(sections, "\n\n")
}

func BuildExploreTaskPrompt(prompt string) string {
	return "Investigate this request in read-only mode and return only a concise exploration summary.\n\n" +
		RenderLaneContractPolicy(LaneExplore) + "\n\n" +
		"Required output:\n" +
		"- likely files\n" +
		"- suspected issue or relevant finding\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Investigation rules:\n" +
		"- Do not ask for permission to edit.\n" +
		"- Do not propose broad directory listing loops.\n" +
		"- The harness may already provide an overview and bootstrap evidence. Use that first instead of repeating the same discovery step.\n" +
		"- If a top-level listing shows source directories or concrete files, inspect at least two concrete files before concluding the repo is empty or claiming there is nothing actionable.\n" +
		"- For open-ended bug hunts, prefer targeted content search after the first orientation step, then read concrete source files.\n" +
		"- Ignore repo metadata or generated-output files such as .git state, caches, logs, and model artifacts unless the request is explicitly about them.\n" +
		"- Never send both a file handle id and a path unless they refer to the same discovered file.\n" +
		"- In Go projects, prioritize README.md, go.mod, cmd/, internal/, and scripts/ before less relevant files.\n" +
		"- If you conclude there is no actionable issue, cite the concrete files you inspected.\n" +
		"Use the available tools to inspect, then summarize evidence clearly.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func BuildReviewSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in REVIEW mode.",
		"This is a read-only summary lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and pick the single best issue or cleanup candidate.",
		"Mention at least one exact candidate file path from the supplied evidence.",
		"Prefer one concrete, defensible finding over a vague sweep of multiple weak ideas.",
	)
	return strings.Join(sections, "\n\n")
}

func BuildArchitectureSystemPrompt(basePrompt string) string {
	sections := make([]string, 0, 2)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in ARCHITECTURE mode.",
		"This is a read-only structure-analysis lane. No further tool calls are available.",
		"Base your answer only on the supplied evidence and identify how the system is organized, where the main structural pressure points are, and what refactor direction is most defensible.",
		"Mention at least one exact hotspot file path from the supplied evidence.",
		"Prefer a small number of high-confidence structural opportunities over an ungrounded sweep.",
	)
	return strings.Join(sections, "\n\n")
}

func BuildReviewTaskPrompt(prompt string) string {
	return "Review this request in read-only mode and return one concrete finding.\n\n" +
		RenderLaneContractPolicy(LaneReview) + "\n\n" +
		"Required output:\n" +
		"- finding title\n" +
		"- file or files involved\n" +
		"- why it is a real issue or worthwhile cleanup\n" +
		"- the specific evidence you relied on\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Review rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Do not hedge with a broad list unless the evidence is genuinely inconclusive.\n" +
		"- Cite at least one exact candidate file path from the provided evidence.\n" +
		"- Prefer the strongest single issue supported by the evidence over multiple weak candidates.\n" +
		"- If the evidence is inconclusive, say that directly and identify the best next file to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func BuildArchitectureTaskPrompt(prompt string) string {
	return "Analyze this request in read-only mode and return a grounded architecture summary.\n\n" +
		RenderLaneContractPolicy(LaneArchitecture) + "\n\n" +
		"Required output:\n" +
		"- current structural shape or extension path\n" +
		"- top 2-3 structural opportunities or pressure points\n" +
		"- the exact hotspot files involved\n" +
		"- why each hotspot matters\n" +
		"- suggested refactor or boundary direction\n" +
		"- confidence\n" +
		"- recommended next step\n\n" +
		"Architecture rules:\n" +
		"- Do not emit tool requests.\n" +
		"- Ground the answer in the repository profile and hotspot evidence provided by the harness.\n" +
		"- Cite at least one exact hotspot file path from the provided evidence.\n" +
		"- Focus on structure, boundaries, ownership, extension points, and coupling instead of bug triage.\n" +
		"- If the evidence is inconclusive, say that directly and identify the next structural hotspot to inspect.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func BuildImplementSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 3)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in IMPLEMENT mode.",
		"Make the smallest correct code change that satisfies the request.",
		"The harness may already provide an implementation target packet with exact files, lines, @file anchors, analogous references, or edit context. Use that first instead of restarting discovery.",
		"For complex multi-step implementation work, start with task_ledger and keep it current. Do not answer done while pending or in_progress ledger items remain.",
		"Prefer edit_file for localized changes and use start_line/end_line/content when quotes or indentation make exact text replacement brittle.",
		"For large Go-file refactors, inspect declaration boundaries with go_file_outline and move model-chosen declaration groups with go_move_declarations, preferably using pattern:\"tag:<responsibility>\" from the outline; if splitting one file into several cohesive files, prefer a single batch go_move_declarations call with moves instead of sequential partial moves.",
		"Do not keep relisting directories once a target file or line is already known. Consume the target packet before any broader search.",
		"Once the edit is complete, answer normally. The harness will run verification automatically after your changes and may feed failing verification output back for a bounded repair pass.",
	)
	return strings.Join(sections, "\n\n")
}

func BuildImplementTaskPrompt(prompt string) string {
	rules := []string{
		"- Make the smallest correct change.",
		"- For complex multi-step work, first call task_ledger with todos. Keep exactly one item in_progress, mark completed work, and do not finish while required items remain pending or in_progress.",
		"- Prefer edit_file over rewriting the whole file for localized edits.",
	}
	if PromptForbidsDeclarationMoves(prompt) {
		rules = append(rules,
			"- This request forbids declaration moves. Do not use go_move_declarations; prefer read_file and edit_file within the approved target file.",
			"- Ignore any generic outline suggestion to split declarations into another file unless the user widens the approved target scope.",
		)
	} else {
		rules = append(rules,
			"- For large Go refactors, use go_file_outline first, choose cohesive responsibility groups yourself, then use go_move_declarations with pattern:\"tag:<responsibility>\" or explicit declarations to move them mechanically into destination files.",
			"- When a large Go refactor splits one file into several cohesive files, prefer one batch go_move_declarations call with moves over partial sequential moves.",
		)
	}
	rules = append(rules,
		"- If the request names a file, uses @file anchors, or the harness already supplied target lines, do not restart discovery from the repo root.",
		"- Treat analogous reference files as inspiration, not as the default write target.",
		"- For structural refactors, stay within the target packet files unless verification requires a follow-up; use batch structural tools when the requested boundary change is explicitly multi-file.",
		"- Once the required change is applied, answer briefly with what you changed.",
	)
	return "Implement this request with the available workspace tools.\n\n" +
		RenderLaneContractPolicy(LaneImplement) + "\n\n" +
		"Implementation rules:\n" +
		strings.Join(rules, "\n") + "\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func StructuredExecutionPlanRules(packet TargetPacket) string {
	lines := []string{
		"- Prefer edit_file.",
		"- Use only paths from the target packet primary files.",
		"- At most 3 operations.",
		"- Use anchored lines, symbol windows, and bootstrapped file excerpts before widening.",
		"- If you cannot propose a safe bounded plan, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if TargetPacketNeedsStructuralPolicy(packet) {
		lines[2] = "- At most 2 operations."
		lines = append(lines,
			"- This is a structural refactor request. Each operation reason must explain the boundary or responsibility change.",
			"- Treat this as the first stage of a structural refactor. Prefer editing only the first primary file, then verify before widening.",
			"- Multi-file operations are allowed only when the structural hints explicitly require a cross-file boundary move or interface extraction.",
			"- Prefer one bounded file split/extraction step over repo-wide rewrites.",
			"- Stay within the files and structural hints in the target packet unless the request explicitly demands a broader refactor.",
		)
	}
	return strings.Join(lines, "\n")
}

func FocusedStructuredExecutionPlanRules(packet TargetPacket, path string, windowStart int, windowEnd int) string {
	lines := []string{
		"- Use only edit_file operations.",
		"- Use only path " + path + ".",
		"- All line edits must stay within lines " + strconv.Itoa(windowStart) + "-" + strconv.Itoa(windowEnd) + ".",
		"- Prefer the narrowest possible line-range edit.",
		"- Use old_text/new_text only if that is safer than a line-range edit.",
		"- At most 2 operations.",
		"- If you cannot propose a safe bounded edit, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if TargetPacketNeedsStructuralPolicy(packet) {
		lines = append(lines,
			"- This focused edit is part of a structural refactor. Each operation reason must name the responsibility or boundary being tightened.",
			"- Treat this as the first stage of the structural refactor, not the whole refactor.",
			"- Preserve the rest of the file shape outside this focused boundary change.",
		)
	}
	return strings.Join(lines, "\n")
}

func BuildImplementRepairPrompt(prompt string) string {
	return "Repair the already-applied change so verification passes while preserving the user's requested behavior change.\n\n" +
		"Repair rules:\n" +
		"- Do not undo the requested behavior change unless verification proves it is impossible.\n" +
		"- Use the supplied verification errors and recent edit context first.\n" +
		"- Prefer the smallest follow-up edit that restores a clean build/test result.\n\n" +
		"Original request:\n" + strings.TrimSpace(prompt)
}
