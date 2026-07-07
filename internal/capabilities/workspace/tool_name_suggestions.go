package workspace

import (
	"fmt"
	"sort"
	"strings"
)

// unsupportedToolError returns a model-actionable error for an unknown tool
// name. It lists nearby registered tools so the model can self-correct after a
// typo or hallucinated name.
func (m *Manager) unsupportedToolError(tool string) string {
	candidates := m.knownToolNames()
	suggestions := closestToolNames(tool, candidates, 5)

	if len(suggestions) == 0 {
		return fmt.Sprintf(
			"unsupported tool %q. No registered tools match this name; check the runtime brief's allowed_tools list.",
			tool,
		)
	}

	quoted := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		quoted = append(quoted, fmt.Sprintf("%q", s))
	}

	return fmt.Sprintf(
		"unsupported tool %q. Did you mean one of: %s? If none match, check the runtime brief's allowed_tools list.",
		tool, strings.Join(quoted, ", "),
	)
}

// knownToolNames returns the union of built-in, external, and MCP tool names
// known to this manager.
func (m *Manager) knownToolNames() []string {
	return m.toolSurface.KnownToolNames(m.externalToolBaseDir())
}

// builtinToolNamesForSuggest reuses the manifest inventory so unsupported-tool
// recovery does not drift away from the exported built-in surface.
func builtinToolNamesForSuggest() []string {
	return toolManifestToolsAcrossBuiltInSurfaces()
}

// closestToolNames returns up to maxN candidates closest to target by
// case-insensitive prefix match first, then substring containment, then short
// Levenshtein distance.
func closestToolNames(target string, candidates []string, maxN int) []string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" || len(candidates) == 0 {
		return nil
	}

	type scored struct {
		name string
		rank int
	}

	scoredList := make([]scored, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))

	for _, c := range candidates {
		if _, dup := seen[c]; dup {
			continue
		}

		seen[c] = struct{}{}

		lc := strings.ToLower(c)

		switch {
		case lc == target:
			scoredList = append(scoredList, scored{c, 0})
		case strings.HasPrefix(lc, target) || strings.HasPrefix(target, lc):
			scoredList = append(scoredList, scored{c, 1})
		case strings.Contains(lc, target) || strings.Contains(target, lc):
			scoredList = append(scoredList, scored{c, 2})
		default:
			d := levenshteinDistance(target, lc)
			cap := len(target) / 2
			if cap < 3 {
				cap = 3
			}

			if d <= cap {
				scoredList = append(scoredList, scored{c, 10 + d})
			}
		}
	}

	sort.SliceStable(scoredList, func(i, j int) bool {
		return scoredList[i].rank < scoredList[j].rank
	})

	out := make([]string, 0, maxN)
	for _, s := range scoredList {
		out = append(out, s.name)
		if len(out) >= maxN {
			break
		}
	}

	return out
}

func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}

	if len(a) == 0 {
		return len(b)
	}

	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}

		prev, curr = curr, prev
	}

	return prev[len(b)]
}
