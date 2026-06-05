package agent

import "strings"

type sourceInventoryStats struct {
	MarkerCount int
	Definitions int
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
		Definitions: sourceInventoryMarkerCount(normalized, "defines"),
		Returns:     sourceInventoryMarkerCount(normalized, "returns"),
		Sets:        sourceInventoryMarkerCount(normalized, "sets"),
		Calls:       sourceInventoryMarkerCount(normalized, "calls"),
		Handles:     sourceInventoryMarkerCount(normalized, "handles"),
		ListLike:    strings.Contains(normalized, ",") || strings.Contains(normalized, " and ") || strings.Contains(normalized, ";"),
		Synthesized: sourceInventoryClaimLooksSynthesized(normalized),
	}
	stats.MarkerCount = stats.Definitions + stats.Returns + stats.Sets + stats.Calls + stats.Handles
	for _, marker := range []string{" contains ", " declares ", " uses "} {
		stats.MarkerCount += strings.Count(normalized, marker)
	}
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
		" the answer is ",
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
		stats.Sets >= 2 ||
		(stats.Definitions >= 2 && stats.Returns >= 1) ||
		(stats.Definitions >= 1 && stats.Calls+stats.Sets >= 2)
}
