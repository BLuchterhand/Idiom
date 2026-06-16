package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/stringset"
	"idiom/internal/runtime/workerruntime"
	"sort"
	"strings"
)


func delegatedSourceBodyPacketHasGroundedFinding(output string) bool {
	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}

	return delegatedTaskOutputHasFindingEvidence(output)
}



func delegatedReadOnlySourceBodyEvidencePaths(results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)

	paths = append(paths, concreteSourceEvidencePaths(results)...)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			paths = append(paths, packet.SourceBodyPaths...)
			continue
		}

		paths = append(paths, sourceBodyEvidencePathsFromDelegatedPacket(result.Output)...)
	}

	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath("", path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return uniqueDelegatedDiscoveryPaths(filtered)
}





func allowsDocumentationOnlyPacket(objective string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	return containsAny(lower, " readme ", " doc ", " docs ", " documentation ", " markdown ")
}

func annotateScopedFallbackAnswer(answer string, objective string) string {
	answer = strings.TrimSpace(answer)
	objective = strings.TrimSpace(objective)

	if objective == "" || answer == "" || !strings.HasPrefix(answer, "Partial findings from this turn") {
		return answer
	}

	return "Question in scope: " + objective + "\nSource: delegated read-only evidence packet.\n\n" + answer
}


func augmentDelegatedReadOnlyPacket(answer string, objective string, results []workspace.ToolResult) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	sourceBodyPaths := delegatedReadOnlySourceBodyReadinessPaths(objective, results)
	paths := delegatedClosureEvidencePaths(objective, results, 8)
	mentioned := normalizePostChildEvidencePaths(pathparse.MentionedWorkspacePaths(answer))
	missing := make([]string, 0, len(paths))
	mentionedSet := make(map[string]struct{}, len(mentioned))

	for _, path := range mentioned {
		mentionedSet[path] = struct{}{}
	}

	for _, path := range paths {
		if _, ok := mentionedSet[path]; ok {
			continue
		}

		missing = append(missing, path)
	}

	candidates := closureProbeCandidates(results, 4)
	missingCandidates := make([]string, 0, len(candidates))
	lowerAnswer := strings.ToLower(answer)

	for _, candidate := range candidates {
		if strings.Contains(lowerAnswer, strings.ToLower(candidate)) {
			continue
		}

		missingCandidates = append(missingCandidates, candidate)
	}

	if len(missing) == 0 && len(missingCandidates) == 0 {
		if len(sourceBodyPaths) == 0 || len(sourceBodyEvidencePathsFromDelegatedPacket(answer)) > 0 {
			return answer
		}
	}

	var b strings.Builder

	b.WriteString(answer)

	if len(sourceBodyPaths) > 0 && len(sourceBodyEvidencePathsFromDelegatedPacket(answer)) == 0 {
		b.WriteString("\n\nSource-body evidence paths:\n")

		for _, path := range limitRunnerStrings(sourceBodyPaths, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(missing) > 0 {
		b.WriteString("\n\nAdditional inspected paths:\n")

		for _, path := range limitRunnerStrings(missing, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}

	if len(missingCandidates) > 0 {
		b.WriteString("\nExact probes already inspected:\n")

		for _, candidate := range limitRunnerStrings(missingCandidates, 3) {
			b.WriteString("- `")
			b.WriteString(candidate)
			b.WriteString("`\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func synthesizeDelegatedReadOnlyAnswer(objective string, results []workspace.ToolResult) (string, bool) {
	objective = strings.TrimSpace(objective)

	useful := make([]workspace.ToolResult, 0, len(results))
	answerabilityResults := make([]workspace.ToolResult, 0, len(results))
	sanitizedResults := make([]workspace.ToolResult, 0, len(results))

	for _, result := range results {
		result = normalizeFallbackResult(result)
		answerabilityResults = append(answerabilityResults, result)

		if strings.TrimSpace(result.Tool) == workspace.ToolNameTask {
			if packet, ok := readOnlyEvidencePacketFromResult(result); ok && !readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
				continue
			}
		}

		sanitizedResult := result

		sanitizedResult.Output = sanitizeDelegatedReadOnlyResultOutput(result)
		sanitizedResults = append(sanitizedResults, sanitizedResult)

		if strings.TrimSpace(sanitizedResult.Error) != "" || strings.TrimSpace(sanitizedResult.Output) == "" || !isResultUsefulForFallback(sanitizedResult) {
			continue
		}

		useful = append(useful, sanitizedResult)
	}

	if answer, ok := synthesizeReadOnlyEvidencePacketAnswer(objective, readOnlyEvidencePacketsFromResults(answerabilityResults)); ok {
		return answer, true
	}

	answerability := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         objective,
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}, answerabilityResults)
	sourceBodyPaths := answerability.RelevantSourceBodyPaths

	if len(sourceBodyPaths) == 0 {
		return "", false
	}

	paths := append([]string{}, sourceBodyPaths...)

	paths = append(paths, answerability.CandidatePaths...)
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(sanitizedResults)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(sanitizedResults, 8)...)
	paths = append(paths, pinnedPathsFromToolResults(sanitizedResults, 8)...)
	paths = prioritizedClosureEvidencePaths(paths, sanitizedResults, 8)
	paths = closurePromptEvidencePaths(paths, 8)

	if len(paths) == 0 {
		return "", false
	}

	if answer, ok := synthesizeDelegatedDiscoveryAnswer(objective, sanitizedResults, paths); ok {
		if !looksLikeNonAnswerProse(answer) {
			return answer, true
		}
	}

	if answer, ok := synthesizeDelegatedSourceBodyFactsAnswer(objective, sanitizedResults, sourceBodyPaths, paths); ok {
		return answer, true
	}

	lines := delegatedReadOnlyFindingLines(useful, sourceBodyPaths, 4)
	if len(lines) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("The answer is grounded in ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")

	if question := delegatedDisplayQuestion(objective); question != "" {
		b.WriteString(" Use those findings as the source of truth for `")
		b.WriteString(question)
		b.WriteString("`.")
	}

	b.WriteString("\n\nWhat this establishes:\n")

	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String()), true
}

func synthesizeDelegatedSourceBodyFactsAnswer(objective string, results []workspace.ToolResult, sourceBodyPaths []string, evidencePaths []string) (string, bool) {
	sourceBodySet := readOnlyPathSet(sourceBodyPaths)

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile {
			continue
		}

		paths := readPathsFromToolResult(result)
		if len(paths) == 0 {
			continue
		}

		path := normalizeRunnerPath(paths[0])
		if _, ok := sourceBodySet[path]; !ok {
			continue
		}

		factAnswer, ok := presentableSourceBodyFactsAnswer(objective, path, result.Output)
		if !ok || looksLikeNonAnswerProse(factAnswer) {
			continue
		}

		factText := stripPresentableSourceObjectiveEcho(factAnswer)
		if looksLikeLowValueSourceInventoryFallback(factText) {
			continue
		}

		if strings.TrimSpace(objective) != "" && !delegatedSourceFactAddressesObjective(factText, objective) {
			continue
		}

		var b strings.Builder

		b.WriteString("The answer is grounded in ")
		b.WriteString(formatInlinePathList(evidencePaths, 4))
		b.WriteString(".")

		if question := delegatedDisplayQuestion(objective); question != "" {
			b.WriteString(" Use those findings as the source of truth for `")
			b.WriteString(question)
			b.WriteString("`.")
		}

		b.WriteString("\n\nWhat this establishes:\n")
		b.WriteString("- ")
		b.WriteString(factText)
		b.WriteString("\n")

		return strings.TrimSpace(b.String()), true
	}

	return "", false
}

func delegatedSourceFactAddressesObjective(factText string, objective string) bool {
	factText = strings.ToLower(strings.TrimSpace(factText))
	if factText == "" {
		return false
	}

	for _, term := range recoveryObjectiveSearchWords(objective, 32) {
		term = singularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 || delegatedSourceFactGenericObjectiveTerm(term) {
			continue
		}

		if strings.Contains(factText, term) {
			return true
		}
	}

	return false
}

func delegatedSourceFactGenericObjectiveTerm(term string) bool {
	switch term {
	case "internal", "agent", "runner", "source", "evidence", "inspect", "inspected", "path", "paths", "report", "file", "code", "explain", "harness":
		return true
	default:
		return false
	}
}


func stripDelegatedReadOnlyInternalPacket(output string) string {
	var kept []string

	skipList := false

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			skipList = false

			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}

			continue
		}

		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "question:"),
			strings.HasPrefix(lower, "source:"),
			strings.HasPrefix(lower, "confidence:"):
			skipList = false
			continue
		case lower == "inspected paths:" ||
			lower == "additional inspected paths:" ||
			lower == "exact probes already inspected:":
			skipList = true
			continue
		case lower == "key findings:":
			skipList = false
			continue
		case skipList && strings.HasPrefix(line, "- "):
			continue
		case strings.HasPrefix(line, "<<QUESTION>>:"),
			strings.Contains(lower, "completion gate rejected"),
			strings.Contains(lower, "missing completion evidence"),
			strings.Contains(lower, "missing: inspected_paths"),
			strings.Contains(lower, "final answer looks like transport-level noise"):
			skipList = false
			continue
		}

		kept = append(kept, line)
	}

	return strings.TrimSpace(strings.Join(collapseBlankRunnerLines(kept), "\n"))
}

func collapseBlankRunnerLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank || len(out) == 0 {
				continue
			}

			blank = true

			out = append(out, "")

			continue
		}

		blank = false

		out = append(out, line)
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	return out
}



func delegatedReadOnlyPacketLineLabels() []string {
	return []string{
		"source-body evidence paths:",
		"source body evidence paths:",
		"source body paths:",
		"grounded findings:",
		"grounded answer:",
		"key findings:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"core files:",
		"primary files:",
		"important flow:",
	}
}



func readOnlyToolControlGuidanceText(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}

	return containsAny(lower,
		"generated evidence artifact",
		"generated discovery/context json is not source-body evidence",
		"not source-body evidence",
		"use the source paths already named by the current discovery packet",
		"next exact source probe example",
		"after one source-body read resolves the claim",
		"redirected to ",
		"try: {\"tool\":",
		"retry your original request and it will execute",
	)
}


func synthesizeDelegatedDiscoveryAnswer(objective string, results []workspace.ToolResult, paths []string) (string, bool) {
	summaries := delegatedDiscoverySummaries(results)
	question := delegatedDisplayQuestion(objective)
	evidencePaths := delegatedDiscoveryAnswerEvidencePaths(question, paths)

	return AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      question,
		EvidencePaths: evidencePaths,
		Summaries:     summaries,
	})
}

func delegatedDiscoverySummaries(results []workspace.ToolResult) []delegatedDiscoverySummary {
	var summaries []delegatedDiscoverySummary

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameDiscoveryQuery || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		var summary delegatedDiscoverySummary

		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}

		if summary.ArtifactPath == "" && len(summary.Evidence) == 0 {
			continue
		}

		summaries = append(summaries, summary)
	}

	return summaries
}


func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}

	return prioritizedDelegatedEvidencePaths(paths, len(paths))
}


func prioritizedDelegatedEvidencePaths(paths []string, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)

	if len(paths) <= 1 {
		return paths
	}

	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])

		return pi < pj
	})

	return limitRunnerStrings(paths, max)
}

func prioritizedClosureEvidencePaths(paths []string, results []workspace.ToolResult, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)

	if len(paths) <= 1 {
		return paths
	}

	sharedCounts, candidateCounts := closureEvidenceProbeScores(results)

	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])

		if pi != pj {
			return pi < pj
		}

		ni := normalizeDelegatedDiscoveryEvidencePath(paths[i])
		nj := normalizeDelegatedDiscoveryEvidencePath(paths[j])

		if sharedCounts[ni] != sharedCounts[nj] {
			return sharedCounts[ni] > sharedCounts[nj]
		}

		if candidateCounts[ni] != candidateCounts[nj] {
			return candidateCounts[ni] > candidateCounts[nj]
		}

		return false
	})

	return limitRunnerStrings(paths, max)
}



func delegatedDiscoverySummaryIsBroadCountOnly(lower string) bool {
	if !strings.Contains(lower, "matching line") {
		return false
	}

	if strings.Contains(lower, "implementation path") ||
		strings.Contains(lower, "implementation anchor") ||
		strings.Contains(lower, "source anchor") {

		return false
	}

	return true
}


func limitRunnerStrings(values []string, max int) []string {
	return stringset.Prefix(values, max)
}

func closurePromptEvidencePaths(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range preferNonTestEvidencePaths(uniqueDelegatedDiscoveryPaths(paths)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}

		cleaned = append(cleaned, path)
	}

	return limitRunnerStrings(cleaned, max)
}

func closureEvidenceProbeScores(results []workspace.ToolResult) (map[string]int, map[string]int) {
	byPath := closureProbeCandidatesByPath(results, 6)
	probeFrequency := make(map[string]int)

	for _, candidates := range byPath {
		seen := make(map[string]struct{})

		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}

			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			probeFrequency[key]++
		}
	}

	sharedCounts := make(map[string]int, len(byPath))
	candidateCounts := make(map[string]int, len(byPath))

	for rawPath, candidates := range byPath {
		path := normalizeDelegatedDiscoveryEvidencePath(rawPath)
		if path == "" {
			continue
		}

		seen := make(map[string]struct{})

		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}

			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			candidateCounts[path]++

			if probeFrequency[key] > 1 {
				sharedCounts[path] += probeFrequency[key]
			}
		}
	}

	return sharedCounts, candidateCounts
}









func isDelegatedDiscoveryContextDigestLayer(name string) bool {
	switch strings.TrimSpace(name) {
	case "project_behavior_digest", "project_context_digest":
		return true
	default:
		return false
	}
}

