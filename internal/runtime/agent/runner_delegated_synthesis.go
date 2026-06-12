package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/stringset"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"sort"
	"strings"
)

func childPacketContainsGroundedAnswerEvidence(objective string, output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	lower := strings.ToLower(output)
	if readOnlyToolControlGuidanceText(lower) {
		return false
	}

	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}

	if !containsAny(lower,
		"grounded answer",
		"core files:",
		"primary files:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"important flow:",
		"key findings:",
		"\n- read ",
		"\n- found ",
		"\n- located ",
		"\n- calls ",
		"\n- uses ",
		"\n- references ",
	) {

		return false
	}

	if strings.TrimSpace(objective) == "" {
		return true
	}

	return delegatedPacketAddressesObjective(objective, output)
}

func delegatedSourceBodyPacketHasGroundedFinding(output string) bool {
	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}

	return delegatedTaskOutputHasFindingEvidence(output)
}

func delegatedPacketAddressesObjective(objective string, output string) bool {
	objective = strings.TrimSpace(objective)
	output = strings.TrimSpace(output)

	if objective == "" || output == "" {
		return false
	}

	body := substantiveAnswerWithoutEchoedObjective(stripDelegatedReadOnlyInternalPacket(output), objective)
	if strings.TrimSpace(body) == "" {
		return false
	}

	objectiveText := answerTextWithoutPathCitations(body)
	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return true
	}

	if delegatedPacketNamesObjectiveSource(objective, output) &&
		readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(objectiveText, objective) {

		return true
	}

	return false
}

func delegatedPacketNamesObjectiveSource(objective string, output string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return false
	}

	for _, path := range sourceBodyEvidencePathsFromDelegatedPacket(output) {
		normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if normalized == "" {
			continue
		}

		base := strings.ToLower(filepath.Base(normalized))
		if strings.Contains(objective, normalized) || (base != "" && strings.Contains(objective, base)) {
			return true
		}
	}

	return false
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

func delegatedReadOnlySourceBodyReadinessPaths(objective string, results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
				paths = append(paths, packet.SourceBodyPaths...)
			}

			continue
		}

		sourcePaths := sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
		if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
			sourcePaths = groundedChildPacketEvidencePaths(objective, result.Output)
		}

		if len(sourcePaths) == 0 {
			continue
		}

		if childPacketContainsGroundedAnswerEvidence(objective, result.Output) || delegatedPacketAddressesObjective(objective, result.Output) {
			paths = append(paths, sourcePaths...)
		}
	}

	return readOnlySourceLikePaths(paths, 16)
}

func sourceBodyEvidencePathsFromDelegatedPacket(output string) []string {
	var paths []string

	inSection := false

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			if inSection {
				break
			}

			continue
		}

		lower := strings.ToLower(strings.TrimRight(line, ":"))
		switch lower {
		case "source-body evidence", "source-body evidence paths", "source body evidence", "source body evidence paths":
			inSection = true
			continue
		}

		if !inSection {
			continue
		}

		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "- ") {
			break
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)) {
			if isNonSourceEvidenceArtifactPath(path) {
				continue
			}

			paths = append(paths, path)
		}
	}

	return uniqueDelegatedDiscoveryPaths(paths)
}

func groundedChildPacketEvidencePaths(objective string, output string) []string {
	rawPaths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(output))
	if len(rawPaths) == 0 {
		return nil
	}

	paths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath(objective, path) {
			continue
		}

		paths = append(paths, path)
	}

	return uniqueDelegatedDiscoveryPaths(paths)
}

func isGroundedChildPacketEvidencePath(objective string, path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(normalizeDelegatedDiscoveryEvidencePath(path))))
	if path == "" {
		return false
	}

	switch {
	case strings.HasPrefix(path, ".idiom/"),
		strings.HasPrefix(path, "idiom/"),
		strings.HasPrefix(path, "logs/"),
		strings.HasPrefix(path, "transcripts/"),
		strings.HasPrefix(path, "testdata/swe/fixtures/"),
		strings.Contains(path, "/testdata/swe/fixtures/"):
		return false
	}

	base := filepath.Base(path)
	if allowsDocumentationOnlyPacket(objective) && (strings.HasSuffix(path, ".md") || base == "readme.md") {
		return true
	}

	switch base {
	case "go.mod", "go.sum", "go.work", "makefile", "package.json", "cargo.toml", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	}

	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".rs"),
		strings.HasSuffix(path, ".py"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".java"),
		strings.HasSuffix(path, ".c"),
		strings.HasSuffix(path, ".cc"),
		strings.HasSuffix(path, ".cpp"),
		strings.HasSuffix(path, ".cxx"),
		strings.HasSuffix(path, ".h"),
		strings.HasSuffix(path, ".hpp"),
		strings.HasSuffix(path, ".m"),
		strings.HasSuffix(path, ".mm"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".toml"),
		strings.HasSuffix(path, ".sh"),
		strings.HasSuffix(path, ".sql"),
		strings.HasSuffix(path, ".proto"):
		return true
	}

	return strings.HasPrefix(path, "internal/") ||
		strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "pkg/") ||
		strings.HasPrefix(path, "src/") ||
		strings.HasPrefix(path, "scripts/")
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

func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(normalizeWorkspacePathScanText(answer))
	if answer == "" {
		return ""
	}

	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathLinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathQuotePattern.ReplaceAllString(answer, "$1")

	return answer
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

func sanitizeDelegatedReadOnlyResultOutput(result workspace.ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return ""
	}

	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
		return output
	}

	return stripDelegatedReadOnlyInternalPacket(output)
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

func delegatedReadOnlyFindingLines(results []workspace.ToolResult, supportedPaths []string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	supported := readOnlyPathSet(supportedPaths)

	var lines []string

	seen := map[string]struct{}{}

	for _, result := range results {
		output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool))
		for _, raw := range strings.Split(output, "\n") {
			line := cleanDelegatedReadOnlyFindingLine(strings.TrimSpace(strings.TrimPrefix(raw, "- ")))
			if !delegatedReadOnlyFindingLineIsUseful(line) {
				continue
			}

			if !delegatedReadOnlyFindingLineSupportedByPaths(line, supported) {
				continue
			}

			if _, ok := seen[line]; ok {
				continue
			}

			seen[line] = struct{}{}
			lines = append(lines, line)

			if len(lines) >= limit {
				return lines
			}
		}
	}

	return lines
}

func cleanDelegatedReadOnlyFindingLine(line string) string {
	line = strings.TrimSpace(line)
	for {
		lower := strings.ToLower(line)
		stripped := false

		for _, label := range delegatedReadOnlyPacketLineLabels() {
			if !strings.HasPrefix(lower, label) {
				continue
			}

			line = strings.TrimSpace(line[len(label):])
			stripped = true

			break
		}

		if !stripped {
			return line
		}
	}
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

func delegatedReadOnlyFindingLineSupportedByPaths(line string, supported map[string]struct{}) bool {
	mentionedAny := false
	supportedAny := false

	for _, path := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)), 16) {
		mentionedAny = true

		if _, ok := supported[path]; ok {
			supportedAny = true
		}
	}

	return !mentionedAny || supportedAny
}

func delegatedReadOnlyFindingLineIsUseful(line string) bool {
	if _, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return false
	}

	if line == "" ||
		delegatedDiscoveryLineIsBarePath(line) ||
		looksLikeLineOrientedSourceSnippetLine(line) ||
		sourceEvidenceLineIsBoilerplate(stripSourceEvidenceLinePrefix(line)) {

		return false
	}

	lower := strings.ToLower(line)
	if readOnlyToolControlGuidanceText(lower) ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "completion gate") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "child task completed") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "enough for a bounded answer") ||
		strings.Contains(lower, "safely expand beyond the inspected anchors") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		lower == "context:" ||
		lower == "context" ||
		strings.Contains(lower, "top-level declaration") ||
		strings.HasPrefix(lower, "source-body evidence paths:") ||
		strings.HasPrefix(lower, "source-body evidence:") ||
		strings.HasPrefix(lower, "source body evidence paths:") ||
		strings.HasPrefix(lower, "source body evidence:") ||
		strings.HasPrefix(lower, "what this establishes:") ||
		strings.HasPrefix(lower, "outlined ") ||
		strings.HasPrefix(lower, "source:") ||
		strings.HasPrefix(lower, "question:") ||
		strings.HasPrefix(lower, "confidence:") {

		return false
	}

	return strings.Contains(line, ":") || strings.Contains(line, "`") || strings.Contains(line, ".")
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

type delegatedDiscoverySummary struct {
	Purpose      string `json:"purpose"`
	ArtifactPath string `json:"artifact_path"`
	Evidence     []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Gaps []string `json:"gaps,omitempty"`
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

func delegatedDisplayQuestion(objective string) string {
	for _, line := range strings.Split(strings.TrimSpace(objective), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return inspected paths") ||
			strings.HasPrefix(lower, "- ") {

			continue
		}

		return line
	}

	return strings.TrimSpace(objective)
}

func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}

	return prioritizedDelegatedEvidencePaths(paths, len(paths))
}

func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	fullByBase := make(map[string]string)
	ambiguousBase := make(map[string]bool)
	dirCounts := make(map[string]int)

	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if path == "" {
			continue
		}

		normalized = append(normalized, path)
		if strings.Contains(path, "/") {
			base := filepath.Base(path)
			dirCounts[filepath.Dir(path)]++

			if prior, ok := fullByBase[base]; ok && prior != path {
				ambiguousBase[base] = true
				continue
			}

			fullByBase[base] = path
		}
	}

	seen := make(map[string]struct{})
	unique := make([]string, 0, len(normalized))
	dominantDir := ""
	dominantCount := 0

	for dir, count := range dirCounts {
		if count > dominantCount || (count == dominantCount && (dominantDir == "" || dir < dominantDir)) {
			dominantDir = dir
			dominantCount = count
		}
	}

	for _, path := range normalized {
		if !strings.Contains(path, "/") {
			base := filepath.Base(path)
			if full, ok := fullByBase[base]; ok && !ambiguousBase[base] {
				path = full
			} else if dominantCount >= 2 {
				path = filepath.ToSlash(filepath.Join(dominantDir, base))
			}
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		unique = append(unique, path)
	}

	return unique
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

func delegatedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	var lines []string

	for _, summary := range summaries {
		for _, evidence := range summary.Evidence {
			name := strings.TrimSpace(evidence.Name)
			if name == "" || isDelegatedDiscoveryContextDigestLayer(name) || name == "target_listing" {
				continue
			}

			text := strings.TrimSpace(evidence.Summary)
			if text == "" || strings.TrimSpace(evidence.Error) != "" {
				continue
			}

			if !delegatedDiscoveryFindingSummaryIsUseful(name, text) {
				continue
			}

			lines = append(lines, name+": "+text)
			if limit > 0 && len(lines) >= limit {
				return lines
			}
		}
	}

	return lines
}

func delegatedDiscoveryFindingSummaryIsUseful(name string, text string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lower := strings.ToLower(strings.TrimSpace(text))

	if lower == "" {
		return false
	}

	if strings.Contains(lower, "context pack wrote") ||
		strings.Contains(lower, "no related go files matched") ||
		strings.Contains(lower, "no go symbols matched") ||
		strings.Contains(lower, "no symbols matched") ||
		strings.Contains(lower, "no matching symbol") ||
		strings.Contains(lower, "semantic fallback after gopls failure") ||
		strings.Contains(lower, "collected go dependency graph") {

		return false
	}

	if lowerName == workspace.ToolNameDependencyGraph && strings.HasPrefix(lower, "collected ") {
		return false
	}

	if lowerName == "text_anchor_search" && delegatedDiscoverySummaryIsBroadCountOnly(lower) {
		return false
	}

	return true
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

func delegatedDiscoveryLineIsBarePath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}

	path := normalizeDelegatedDiscoveryEvidencePath(line)

	return path != "" && delegatedDiscoveryLooksLikeEvidencePath(path)
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

func delegatedDiscoveryQueryEvidencePaths(results []workspace.ToolResult, limit int) []string {
	if limit <= 0 {
		limit = 4
	}

	type discoveryPathCandidate struct {
		path  string
		score int
	}

	best := make(map[string]int)
	addCandidate := func(path string, layerName string, line string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
			return
		}

		if !delegatedDiscoveryLooksLikeEvidencePath(path) {
			return
		}

		if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
			return
		}

		score := delegatedDiscoveryLayerPriority(layerName)*100 + delegatedDiscoveryEvidenceLinePriority(line)*10 + delegatedDiscoveryPathPriority(path)
		if prior, ok := best[path]; ok && prior <= score {
			return
		}

		best[path] = score
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameDiscoveryQuery || strings.TrimSpace(result.Output) == "" {
			continue
		}

		var summary struct {
			Evidence []struct {
				Name          string `json:"name"`
				Summary       string `json:"summary"`
				OutputExcerpt string `json:"output_excerpt"`
			} `json:"evidence"`
		}

		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}

		for _, evidence := range summary.Evidence {
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				for _, path := range pathparse.MentionedWorkspacePaths(line) {
					addCandidate(path, evidence.Name, line)
				}

				for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
					addCandidate(match, evidence.Name, line)
				}
			}
		}
	}

	candidates := make([]discoveryPathCandidate, 0, len(best))
	for path, score := range best {
		candidates = append(candidates, discoveryPathCandidate{path: path, score: score})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}

		return candidates[i].path < candidates[j].path
	})

	paths := make([]string, 0, limit)
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
		if len(paths) >= limit {
			return preferNonTestEvidencePaths(paths)
		}
	}

	return preferNonTestEvidencePaths(paths)
}

func delegatedDiscoveryEvidenceLinePriority(line string) int {
	line = strings.TrimSpace(line)

	lower := strings.ToLower(line)

	switch {
	case line == "":
		return 5
	case strings.Contains(lower, "package "):
		return 5
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 0
	case strings.Contains(lower, "func "):
		return 1
	case strings.Contains(lower, "type "):
		return 2
	default:
		return 3
	}
}

func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = normalizeWorkspacePathScanText(path)
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"()[]{}<>,.;")

	if path == "" {
		return ""
	}

	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := indexFold(path, ext); idx >= 0 {
			end := idx + len(ext)
			suffix := path[end:]

			switch {
			case suffix == "":
				return path[:end]
			case strings.HasPrefix(suffix, ":"):
				return path[:end]
			case strings.Trim(suffix, "`'\"") == "":
				return path[:end]
			}
		}
	}

	return path
}

func indexFold(value string, needle string) int {
	if needle == "" || len(value) < len(needle) {
		return -1
	}

	for i := 0; i+len(needle) <= len(value); i++ {
		if strings.EqualFold(value[i:i+len(needle)], needle) {
			return i
		}
	}

	return -1
}

func normalizeWorkspacePathScanText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(text)
}

func preferNonTestEvidencePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	hasNonTestImplementation := false

	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			hasNonTestImplementation = true
			break
		}
	}

	if !hasNonTestImplementation {
		return paths
	}

	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		filtered = append(filtered, raw)
	}

	if len(filtered) == 0 {
		return paths
	}

	return filtered
}

func delegatedDiscoveryLooksLikeEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	switch {
	case strings.HasPrefix(path, "search_content/"),
		strings.HasPrefix(path, "search_files/"),
		strings.HasPrefix(path, "read/"),
		strings.HasPrefix(path, "list_files/"):
		return false
	case pathparse.HasWorkspaceFileExtension(path):
		return true
	default:
		return false
	}
}

func delegatedDiscoveryLayerPriority(name string) int {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return 0
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return 1
	case "go_references", "cpp_references", "python_references", "ts_references", "rust_references", "java_references":
		return 2
	case "go_definition", "cpp_definition", "python_definition", "ts_definition", "rust_definition", "java_definition":
		return 3
	case "go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return 4
	case "related_files":
		return 5
	case workspace.ToolNameDependencyGraph:
		return 6
	case workspace.ToolNameCtagsSymbols:
		return 7
	case "target_listing":
		return 20
	case "project_behavior_digest", "project_context_digest":
		return 30
	default:
		return 10
	}
}

func isDelegatedDiscoveryContextDigestLayer(name string) bool {
	switch strings.TrimSpace(name) {
	case "project_behavior_digest", "project_context_digest":
		return true
	default:
		return false
	}
}

func delegatedDiscoveryPathPriority(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 0
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 1
	case strings.HasPrefix(path, "internal/"):
		return 2
	case strings.HasSuffix(path, "_test.go"):
		return 3
	default:
		return 4
	}
}
