package comprehension

import (
	"slices"
	"sort"
)

func packRelationshipLimit(limit int) int {
	if limit <= 0 {
		return maxPackRelationships
	}

	relationshipLimit := limit * relationshipsPerTarget
	if relationshipLimit > maxPackRelationships {
		return maxPackRelationships
	}

	if relationshipLimit < limit {
		return limit
	}

	return relationshipLimit
}

func packNodesByID(nodes []Node) map[string]Node {
	out := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		out[node.ID] = node
	}

	return out
}

func packStalePaths(status StatusReport) map[string]struct{} {
	out := make(map[string]struct{}, len(status.StaleFiles)+len(status.MissingFiles))
	for _, path := range status.StaleFiles {
		out[path] = struct{}{}
	}

	for _, path := range status.MissingFiles {
		out[path] = struct{}{}
	}

	return out
}

func cleanPackPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = cleanScope(path)
		if path == "" || path == "." {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	slices.Sort(out)

	return out
}

func packPathSet(paths []string) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = cleanScope(path)
		if path == "" || path == "." {
			continue
		}

		out[path] = struct{}{}
	}

	return out
}

func packNodesForPaths(nodes []Node, paths map[string]struct{}) []Node {
	if len(paths) == 0 {
		return nil
	}

	out := make([]Node, 0)

	for _, node := range nodes {
		path := cleanScope(node.Path)
		if _, ok := paths[path]; ok {
			out = append(out, node)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].ID < out[j].ID
	})

	return out
}

func packPathIsStale(stale map[string]struct{}, path string) bool {
	if len(stale) == 0 {
		return false
	}

	_, ok := stale[cleanScope(path)]

	return ok
}

func packRoleRank(role string) int {
	switch role {
	case "editable":
		return 3
	case "test":
		return 2
	case "read_only_evidence":
		return 1
	default:
		return 0
	}
}

func packReasonRank(reason string) int {
	switch reason {
	case "required edit path":
		return 4
	case "matched task terms":
		return 3
	case "package matched task terms":
		return 2
	default:
		return 1
	}
}
