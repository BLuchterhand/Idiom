package workspace

import (
	"slices"
	"strings"
)

func compactNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func compactSortedNonEmptyStrings(values []string) []string {
	out := compactNonEmptyStrings(values)
	slices.Sort(out)

	return out
}
