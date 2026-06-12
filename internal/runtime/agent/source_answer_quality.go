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
