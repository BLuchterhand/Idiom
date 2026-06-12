package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"slices"
	"strings"
)

func childPacketBoundaryNudge(objective string, results []workspace.ToolResult, evidencePaths []string, probeBudget int) string {
	evidencePaths = closurePromptEvidencePaths(evidencePaths, 4)

	validity, _ := closureDecisionRationale(objective, results, evidencePaths)
	lines := []string{
		"The delegated read_only child result is now available as an evidence packet.",
	}

	if validity != "" {
		lines = append(lines, "Why a final answer is now valid: "+validity)
	}

	if len(evidencePaths) > 0 {
		lines = append(lines, "Packet-named paths: "+summarizePostChildEvidencePaths(evidencePaths, 3))
	}

	if suggestion := closureProbeSuggestionLine(results, 3); suggestion != "" {
		lines = append(lines, suggestion)
	}

	lines = append(lines, postReadinessContinuationContract("child_packet_available", probeBudget))
	lines = append(lines,
		"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
		"If you answer now, answer from the packet and inspected source evidence.",
	)

	return strings.Join(lines, "\n")
}

type postReadinessToolContract struct {
	RequireSourceRefreshContract bool
	ReadinessAdvisory            bool
	Reason                       string
}

func postReadinessToolContractForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) postReadinessToolContract {
	if writeReadyPacketReadContractActive(brief, results) || implementationScopedEditSurfaceActive(brief, results) {
		return postReadinessToolContract{
			RequireSourceRefreshContract: true,
			Reason:                       "mutation_source_refresh_contract",
		}
	}

	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		return postReadinessToolContract{
			ReadinessAdvisory: true,
			Reason:            "read_only_readiness_advisory",
		}
	}

	return postReadinessToolContract{Reason: "none"}
}

func postReadinessGap(req workspace.ToolRequest) string {
	return util.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering)
}

func delegatedReadOnlyAnswerReadyNudge(objective string, results []workspace.ToolResult, probeBudget int) string {
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"The delegated read-only evidence is sufficient for a grounded answer to this question.",
	}

	if validity != "" {
		lines = append(lines, "Why a final answer is now valid: "+validity)
	}

	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}

	lines = append(lines, postReadinessContinuationContract("delegated_read_only_answer_ready", probeBudget))
	lines = append(lines,
		"Carry forward the inspected paths above and keep the answer minimal; avoid side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
	)

	return strings.Join(lines, "\n")
}

func readOnlyAnswerReadyNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, probeBudget int) string {
	objective := strings.TrimSpace(brief.Objective)
	paths := closurePromptEvidencePaths(readOnlyPresentationEvidencePathsForBrief(brief, results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"The read-only evidence is sufficient for a grounded answer to this question.",
	}

	if validity != "" {
		lines = append(lines, "Why a final answer is now valid: "+validity)
	}

	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}

	lines = append(lines, postReadinessContinuationContract("answer_readiness_reached", probeBudget))
	lines = append(lines,
		"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit.",
	)

	return strings.Join(lines, "\n")
}

func postReadinessProbeBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) {
		return 1
	}

	return delegatedReadOnlyProbeBudget
}

// postReadinessContinuationContract returns advisory closeout guidance after
// readiness or a child packet. It must not describe read-only readiness as a
// permission gate; explicit source-refresh gates are selected by
// postReadinessToolContractForBrief.
func postReadinessContinuationContract(scope string, budget int) string {
	anchor := "prefer a symbol, branch, body, or narrow line-range probe over file-level raw reads"
	if strings.TrimSpace(scope) == "child_packet_available" {
		anchor = "anchor to a named path, symbol, discovered handle, branch, body, or narrow line-range probe"
	} else if strings.TrimSpace(scope) == "answer_readiness_reached" {
		anchor = "target a symbol, call/reference, branch, body, or narrow line-range probe tied to inspected paths"
	}

	probeBudgetLine := "one targeted probe"

	switch {
	case budget <= 0:
		probeBudgetLine = "answer unless the missing claim is exact and material"
	case budget == 1:
	default:
		probeBudgetLine = fmt.Sprintf("at most %d targeted probes", budget)
	}

	lines := []string{
		"A final answer is available unless one exact unresolved claim remains.",
		"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy; " + anchor + "; " + probeBudgetLine + " before answering.",
		"The follow-up should target a code-shaped token (symbol name, file path, or narrow line range) and should not paraphrase the original question.",
	}

	return strings.Join(lines, "\n")
}

func delegatedReadOnlyDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}

	// The readiness nudge already disclosed the advisory continuation shape.
	// Repeating that prose burns context for no behavior change. Emit a
	// factual probe-budget note instead and let the model decide.
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 3)
	line := fmt.Sprintf("Post-readiness probes used: %d. You have already used the post-readiness follow-up budget disclosed for this lane. A final answer is available from the evidence above; take another tool call only for an exact material missing claim.", followups)

	if len(paths) > 0 {
		line += " Inspected paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}

	return line
}

func closureProbeSuggestionLine(results []workspace.ToolResult, max int) string {
	candidates := closureProbeCandidates(results, max)
	if len(candidates) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		quoted = append(quoted, "`"+candidate+"`")
	}

	return "Evidence-grounded exact probes to prefer: " + strings.Join(quoted, ", ") + "."
}

func closureProbeCandidates(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 3
	}

	candidates := make([]string, 0, max)
	add := func(raw string) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw); ok {
			candidates = util.AppendUnique(candidates, symbol)
		}
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathLineSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}

			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[2])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}

			for _, line := range strings.Split(text, "\n") {
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) > 1 {
						add(match[1])

						if len(candidates) >= max {
							return candidates[:max]
						}
					}
				}
			}

			for _, match := range goFuncDeclPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])

					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
		}
	}

	return candidates
}

func closureProbeCandidatesByPath(results []workspace.ToolResult, maxPerPath int) map[string][]string {
	if maxPerPath <= 0 {
		maxPerPath = 3
	}

	byPath := make(map[string][]string)
	add := func(path string, raw string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			return
		}

		symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
		if !ok {
			return
		}

		current := byPath[path]
		if containsExactProbeCandidate(current, symbol) {
			return
		}

		if len(current) >= maxPerPath {
			return
		}

		byPath[path] = append(current, symbol)
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathScopedSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}

			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}

			for _, line := range strings.Split(text, "\n") {
				paths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
				if len(paths) == 0 {
					continue
				}

				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) <= 1 {
						continue
					}

					for _, path := range paths {
						add(path, match[1])
					}
				}
			}
		}
	}

	return byPath
}

func containsExactProbeCandidate(candidates []string, pattern string) bool {
	pattern, ok := delegatedReadOnlyLiteralSymbolCandidate(pattern)
	if !ok {
		pattern = strings.TrimSpace(pattern)
	}

	pattern = strings.ToLower(pattern)
	for _, candidate := range candidates {
		if strings.ToLower(strings.TrimSpace(candidate)) == pattern {
			return true
		}
	}

	return false
}

func quotedRunnerStrings(values []string) string {
	if len(values) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		quoted = append(quoted, "`"+value+"`")
	}

	return strings.Join(quoted, ", ")
}

func postChildDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}

	// The child-packet boundary nudge already disclosed the continuation
	// contract; this is a factual probe-budget note, not another prose
	// block telling the model what was already said.
	paths := postChildClosureEvidencePaths(objective, results, 3)
	line := fmt.Sprintf("Post-child probes used: %d. You have already used the post-child follow-up budget disclosed for this lane. A final answer is available from the packet above; take another tool call only for an exact material missing claim.", followups)

	if len(paths) > 0 {
		line += " Packet paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}

	return line
}

func delegatedClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max*2)
	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		paths = append(paths, packet.SourceBodyPaths...)
		paths = append(paths, packet.InspectedPaths...)
		paths = append(paths, packet.CandidatePaths...)
	}

	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)

	return closurePromptEvidencePaths(paths, max)
}

func closureDecisionRationale(objective string, results []workspace.ToolResult, fallbackPaths []string) (string, string) {
	paths := closurePromptEvidencePaths(fallbackPaths, 4)
	if len(paths) == 0 {
		paths = delegatedClosureEvidencePaths(objective, results, 4)
	}

	switch {
	case len(paths) > 0 && closureHasExactProbeEvidence(results):
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3) + " and includes at least one exact inspected probe", "another follow-up is only likely to help if it changes one named claim; otherwise it will mostly restate adjacent detail"
	case len(paths) > 0:
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3), "another follow-up is only likely to help if it closes one named gap; otherwise additional reads mostly restate nearby files"
	default:
		return "the current evidence already covers the named scope well enough to support a grounded answer", "another follow-up is only likely to help if it closes one named gap"
	}
}

func closureHasExactProbeEvidence(results []workspace.ToolResult) bool {
	return len(closureProbeCandidates(results, 1)) > 0
}

func applyClosurePromptRefinement(state runnerNextActionState) runnerNextActionState {
	scope := ""

	switch {
	case slices.Contains(state.Constraints, "child_packet_available"):
		scope = "child_packet_available"
	case slices.Contains(state.Constraints, "answer_readiness_reached"):
		scope = "answer_readiness_reached"
	default:
		return state
	}

	state.NextValidActions = prependRunnerStateStrings(closurePromptNextActions(scope), filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, workspace.ToolNameSearchFiles),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "produce the missing evidence or edit required by the gate"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "gather or edit the missing evidence"),
			strings.Contains(lower, workspace.ToolNameEditFile),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, workspace.ToolNameGoMoveDeclarations),
			strings.Contains(lower, workspace.ToolNameListFiles):
			return true
		default:
			return false
		}
	}))
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, closurePromptInvalidActions(scope))

	return state
}

func closurePromptNextActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"final answer from the child packet evidence is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	default:
		return []string{
			"final answer from the evidence already gathered is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	}
}

func closurePromptInvalidActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return nil
	default:
		return []string{
			"broad rediscovery after answer readiness without one exact missing claim and one exact probe",
			"README/docs or repo-wide search outside named implementation paths after answer readiness",
		}
	}
}
