package agent

import (
	"regexp"
	"strings"
)

var lineOrientedSourceSnippetPattern = regexp.MustCompile(`(?:^|[\s,;])(\d{1,5}):\s*([^\s]+)?`)

type sourceInventoryStats struct {
	MarkerCount int
	Assigns     int
	Contains    int
	Declares    int
	Definitions int
	References  int
	Returns     int
	Sets        int
	Calls       int
	Handles     int
	ListLike    bool
	Synthesized bool
}

func (stats sourceInventoryStats) LooksLikeInventory() bool {
	if stats.Synthesized {
		return false
	}

	return stats.MarkerCount >= 2 || (stats.MarkerCount >= 1 && stats.ListLike)
}

func sourceInventoryStatsForClaim(claim string) sourceInventoryStats {
	normalized := normalizeSourceInventoryClaim(claim)
	stats := sourceInventoryStats{
		Assigns:     sourceInventoryMarkerCount(normalized, "assigns"),
		Contains:    sourceInventoryMarkerCount(normalized, "contains"),
		Declares:    sourceInventoryMarkerCount(normalized, "declares"),
		Definitions: sourceInventoryMarkerCount(normalized, "defines"),
		References:  sourceInventoryMarkerCount(normalized, "references"),
		Returns:     sourceInventoryMarkerCount(normalized, "returns"),
		Sets:        sourceInventoryMarkerCount(normalized, "sets"),
		Calls:       sourceInventoryMarkerCount(normalized, "calls"),
		Handles:     sourceInventoryMarkerCount(normalized, "handles"),
		ListLike:    strings.Contains(normalized, ",") || strings.Contains(normalized, " and ") || strings.Contains(normalized, ";"),
		Synthesized: sourceInventoryClaimLooksSynthesized(normalized),
	}

	stats.MarkerCount = stats.Assigns + stats.Definitions + stats.References + stats.Returns + stats.Sets + stats.Calls + stats.Handles
	stats.MarkerCount += stats.Contains + stats.Declares

	return stats
}

func normalizeSourceInventoryClaim(claim string) string {
	return " " + strings.ToLower(strings.Join(strings.Fields(claim), " ")) + " "
}

func sourceInventoryMarkerCount(normalized string, marker string) int {
	withCode := strings.Count(normalized, " "+marker+" `")
	if withCode > 0 {
		return withCode
	}

	if strings.Contains(normalized, " "+marker+" ") {
		return 1
	}

	return 0
}

func sourceInventoryClaimLooksSynthesized(normalized string) bool {
	return containsAny(normalized,
		" because ",
		" this means ",
		" the likely cause is ",
		" the root cause is ",
		" the smallest fix ",
		" the fix is ",
		" should ",
		" would ",
		" needs ",
		" to enable ",
		" preserves ",
	)
}

func looksLikeInspectedBodySourceInventory(lower string) bool {
	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, " inspected body shows that it ")

	if idx < 0 {
		idx = strings.Index(normalized, " inspected source shows that it ")
	}

	if idx < 0 {
		return false
	}

	return sourceInventoryStatsForClaim(normalized[idx:]).LooksLikeInventory()
}

func looksLikeSingleAnchorInspectedSourceInventory(lower string) bool {
	if !strings.Contains(lower, "clearest inspected implementation anchor") {
		return false
	}

	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, " inspected body shows that it ")

	if idx < 0 {
		idx = strings.Index(normalized, " inspected source shows that it ")
	}

	if idx < 0 {
		return false
	}

	stats := sourceInventoryStatsForClaim(normalized[idx:])

	return stats.MarkerCount > 0 && !stats.Synthesized
}

func looksLikePathShowsSourceInventory(lower string) bool {
	normalized := normalizeSourceInventoryClaim(lower)
	idx := strings.Index(normalized, "` shows ")

	if idx < 0 {
		return false
	}

	return sourceInventoryStatsForClaim(normalized[idx+len("` shows "):]).LooksLikeInventory()
}

func looksLikeLowValueSourceInventoryFallback(answer string) bool {
	stats := sourceInventoryStatsForClaim(stripPresentableSourceObjectiveEcho(answer))
	if stats.Handles > 0 {
		return false
	}

	return stats.Returns >= 2 ||
		stats.Assigns >= 2 ||
		stats.Sets >= 2 ||
		stats.References >= 2 ||
		stats.Assigns+stats.Calls+stats.Returns >= 3 ||
		stats.Definitions+stats.References >= 3 ||
		(stats.Definitions >= 2 && stats.Returns >= 1) ||
		(stats.Definitions >= 1 && stats.Calls+stats.Sets >= 2)
}

func looksLikeDiscoveryEvidenceInventoryNonAnswer(answer string) bool {
	normalized := normalizeSourceInventoryClaim(answer)
	markers := 0

	for _, marker := range []string{
		" related files:",
		" symbol index:",
		" capability:",
		" language:",
		" routed_tool:",
		" route_summary:",
		" discovery packet:",
		" relevant source hits:",
		" evidence used:",
		" no go workspace symbols matched ",
	} {
		if strings.Contains(normalized, marker) {
			markers++
		}
	}

	if strings.Contains(normalized, " this is grounded in `") && markers > 0 {
		markers++
	}

	if markers >= 2 {
		return true
	}

	if sourceInventoryClaimLooksSynthesized(normalized) {
		return false
	}

	return false
}

func looksLikeLowValueSourceInventoryNonAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)

	if looksLikeLineOrientedSourceSnippetAnswer(trimmed) {
		return true
	}

	if looksLikeSingleAnchorInspectedSourceInventory(lower) {
		return true
	}

	if looksLikeDiscoveryEvidenceInventoryNonAnswer(trimmed) {
		return true
	}

	if strings.Contains(lower, "clearest inspected implementation anchor") &&
		looksLikeInspectedBodySourceInventory(lower) {

		return true
	}

	return (looksLikePathShowsSourceInventory(lower) || looksLikeInspectedBodySourceInventory(lower)) &&
		looksLikeLowValueSourceInventoryFallback(trimmed)
}

func looksLikeHostCloseoutSourceInventoryNonAnswer(answer string) bool {
	if looksLikeLowValueSourceInventoryNonAnswer(answer) {
		return true
	}

	stats := sourceInventoryStatsForClaim(stripPresentableSourceObjectiveEcho(answer))
	if stats.Synthesized {
		return false
	}

	normalized := normalizeSourceInventoryClaim(answer)
	if stats.MarkerCount >= 1 &&
		(strings.Contains(normalized, " the answer is grounded in `") ||
			strings.Contains(normalized, " this is grounded in `")) {

		return stats.Definitions+stats.References+stats.Assigns+stats.Returns+
			stats.Sets+stats.Calls+stats.Handles+stats.Contains+stats.Declares > 0
	}

	return (stats.Definitions >= 2 && stats.MarkerCount == stats.Definitions) ||
		(stats.References >= 2 && stats.MarkerCount == stats.References) ||
		(stats.Definitions+stats.References >= 3 && stats.MarkerCount == stats.Definitions+stats.References)
}

func looksLikeLineOrientedSourceSnippetAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}

	if looksLikeCompactedLineOrientedSourceSnippet(trimmed) {
		return true
	}

	fragments, sourceMarkers := lineOrientedSourceSnippetCounts(trimmed)
	if fragments < 2 || sourceMarkers == 0 {
		return false
	}

	if sourceInventoryClaimLooksSynthesized(normalizeSourceInventoryClaim(trimmed)) {
		return false
	}

	return true
}

func looksLikeLineOrientedSourceSnippetLine(line string) bool {
	line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
	line = strings.TrimSpace(strings.TrimPrefix(line, "..."))
	line = strings.TrimSpace(strings.TrimPrefix(line, "…"))
	if line == "" {
		return false
	}

	if !lineHasLeadingSourceLineNumber(line) {
		return false
	}

	colon := strings.IndexByte(line, ':')
	rest := strings.TrimSpace(line[colon+1:])
	if rest == "" || rest == "." || rest == ")" || rest == "}" || rest == "{" {
		return true
	}

	return lineOrientedSourceTokenLooksLikeCode(rest)
}

func lineHasLeadingSourceLineNumber(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 || colon > 5 {
		return false
	}

	for _, ch := range line[:colon] {
		if ch < '0' || ch > '9' {
			return false
		}
	}

	return true
}

func looksLikeCompactedLineOrientedSourceSnippet(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !(strings.HasPrefix(trimmed, "...") || strings.HasPrefix(trimmed, "…")) {
		return false
	}

	if !lineOrientedSourceSnippetPattern.MatchString(trimmed) {
		return false
	}

	lower := strings.ToLower(trimmed)
	return containsAny(lower,
		"\t",
		"[]",
		"{}",
		"func ",
		"type ",
		"const ",
		"var ",
		"package ",
		"return ",
	)
}

func lineOrientedSourceSnippetCounts(answer string) (int, int) {
	matches := lineOrientedSourceSnippetPattern.FindAllStringSubmatch(answer, -1)
	fragments := 0
	sourceMarkers := 0

	for _, match := range matches {
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}

		fragments++

		token := ""

		if len(match) > 2 {
			token = match[2]
		}

		if lineOrientedSourceTokenLooksLikeCode(token) {
			sourceMarkers++
		}
	}

	return fragments, sourceMarkers
}

func lineOrientedSourceTokenLooksLikeCode(token string) bool {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, "`*_.,;:()[]{}")

	if token == "" {
		return false
	}

	lower := strings.ToLower(token)
	if strings.Contains(token, "(") ||
		strings.Contains(token, "[]") ||
		strings.Contains(token, "&&") ||
		strings.Contains(token, "||") {

		return true
	}

	if strings.HasPrefix(lower, "\"") || strings.HasPrefix(lower, "'") {
		return true
	}

	if strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/*") {
		return true
	}

	for _, prefix := range []string{
		"package",
		"import",
		"func",
		"type",
		"const",
		"var",
		"return",
		"case",
		"if",
		"for",
		"switch",
		"map[",
		"make(",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func looksLikeRawToolResultTranscript(answer string) bool {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	first := ""
	seenSummary := false
	seenOutput := false
	seenStatus := false

	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}

		if first == "" {
			first = lower
		}

		if strings.HasPrefix(lower, "summary:") {
			seenSummary = true
		}

		if strings.HasPrefix(lower, "output:") {
			seenOutput = true
		}

		if strings.HasPrefix(lower, "status:") {
			seenStatus = true
		}
	}

	return strings.HasPrefix(first, "tool: ") && seenSummary && (seenOutput || seenStatus)
}


func normalizeWorkspacePathScanText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(text)
}


func looksLikeSourceEchoOnlyAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !looksLikeToolResultTranscript(lower) {
		return false
	}

	claim := normalizeSourceInventoryClaim(stripPresentableSourceObjectiveEcho(trimmed))
	if sourceInventoryClaimLooksSynthesized(claim) ||
		containsAny(lower,
			" should reject ",
			" should change ",
		) {

		return false
	}

	return true
}


func looksLikeScratchpadLeakedAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	normalized := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	for i, line := range strings.Split(trimmed, "\n") {
		lowerLine := strings.ToLower(strings.TrimSpace(line))
		if lowerLine == "" {
			continue
		}

		if lineStartsWithAny(lowerLine,
			"actually, i should",
			"actually, i need",
			"actually, let me",
			"actually, wait",
			"let me analyze",
			"let me inspect",
			"let me check",
			"let me read",
			"let me search",
			"let me verify",
			"let me gather",
			"let me prepare",
			"let me write",
			"let me provide",
			"let me produce",
			"let me think",
			"i should analyze",
			"i should inspect",
			"i should check",
			"i should read",
			"i should search",
			"i should verify",
			"i should gather",
			"i should prepare",
			"i should write",
			"i should provide",
			"i should produce",
			"i need to analyze",
			"i need to inspect",
			"i need to check",
			"i need to read",
			"i need to search",
			"i need to verify",
			"i need to gather",
			"i need to prepare",
			"i need to write",
			"i need to provide",
			"i need to produce",
			"ok, i'm providing",
			"ok, i’m providing",
			"ok, final answer",
			"ok, i'm ready",
			"ok, i’m ready",
			"wait, i",
		) {

			return true
		}

		if i == 0 && lineStartsWithAny(lowerLine, "the user wants", "the user is asking") {
			if containsAny(normalized,
				" actually, ",
				" actually, i ",
				" actually, let me ",
				" let me ",
				" i should ",
				" i need to ",
				" ok, i'm providing ",
				" ok, i’m providing ",
				" ok, final answer ",
				" wait, ",
			) {

				return true
			}

			return !containsAny(normalized,
				" the answer is ",
				" the likely cause is ",
				" the root cause is ",
				" because ",
				" this means ",
			)
		}
	}

	return false
}


func looksLikeMetaIntentRestatement(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !containsAny(lower,
		" the user is asking me to ",
		" the user asked me to ",
		" the user wants me to ",
		" the user requested me to ",
	) {

		return false
	}

	if containsAny(lower,
		" the answer is ",
		" the likely cause is ",
		" the root cause is ",
		" because ",
		" this means ",
	) {

		return false
	}

	return containsAny(lower,
		" i need to find ",
		" i need to inspect ",
		" i need to search ",
		" i need to read ",
		" i need to check ",
		" i should find ",
		" i should inspect ",
		" i should search ",
		" i should read ",
		" i should check ",
	)
}


func looksLikeLeakedImplementationBlockerTemplate(lowerAnswer string) bool {
	if !strings.HasPrefix(lowerAnswer, "changed paths: none") {
		return false
	}

	return strings.Contains(lowerAnswer, "\nverification: not run") &&
		strings.Contains(lowerAnswer, "\nblocker:")
}


func looksLikeRawReadOnlyEvidencePacketAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" || strings.Contains(lower, "grounded answer:") {
		return false
	}

	packetMarkers := 0

	for _, marker := range append(delegatedReadOnlyPacketLineLabels(), "source: delegated read-only evidence packet") {
		if strings.Contains(lower, marker) {
			packetMarkers++
		}
	}

	return packetMarkers >= 1
}


func looksLikeNonAnswerProse(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)

	if trimmed == "" {
		return false
	}

	// Textual tool-call markup like [Calling tool: ...] in answer prose.
	if looksLikeTextualToolCall(answer) {
		return true
	}

	// Echoed user question: the answer starts with "Question:" followed
	// by the user's own words rather than an actual investigation result.
	if strings.HasPrefix(lower, "question:") {
		return true
	}

	if looksLikeMetaIntentRestatement(trimmed) {
		return true
	}

	if looksLikeScratchpadLeakedAnswer(trimmed) {
		return true
	}

	if looksLikeRawReadOnlyEvidencePacketAnswer(trimmed) {
		return true
	}

	if looksLikeLowValueSourceInventoryNonAnswer(trimmed) {
		return true
	}

	if finalAnswerSectionMarkerCount(answer) > 1 {
		return true
	}

	// Raw tool-result transcript with no user-facing prose.
	if looksLikeRawToolResultTranscript(answer) {
		return true
	}

	if looksLikeSourceEchoOnlyAnswer(trimmed) {
		return true
	}

	// Leaked implementation-result templates are protocol artifacts, not
	// direct answers. Do not reject ordinary diagnostic prose just because it
	// discusses a blocker, a failed command, or an exit status.
	if looksLikeLeakedImplementationBlockerTemplate(lower) {
		return true
	}

	return false
}

