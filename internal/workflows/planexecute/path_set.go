package planexecute

import (
	"sort"

	plancontract "idiom/internal/contracts/plan"
)

func mergePlanExecutePaths(groups ...[]string) []string {
	seen := make(map[string]struct{})

	var out []string

	for _, group := range groups {
		for _, path := range group {
			normalized := plancontract.NormalizeScopePath(path)
			if normalized == "" {
				continue
			}

			if _, ok := seen[normalized]; ok {
				continue
			}

			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}

	sort.Strings(out)

	return out
}
