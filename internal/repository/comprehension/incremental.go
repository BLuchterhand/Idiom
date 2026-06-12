package comprehension

import (
	"path/filepath"
	"slices"
	"strings"
)

func (b *graphBuilder) mergePreviousGraph(previous Graph, scope string) int {
	if strings.TrimSpace(scope) == "" {
		return 0
	}

	removed := make(map[string]struct{})

	for _, node := range previous.Nodes {
		if shouldDropPreviousNodeForIncrementalScope(node, scope) {
			removed[node.ID] = struct{}{}
			continue
		}

		if _, exists := b.nodes[node.ID]; exists {
			continue
		}

		b.addNode(node)
	}

	for _, edge := range previous.Edges {
		if shouldDropPreviousEdgeForIncrementalScope(edge, removed) {
			continue
		}

		if _, ok := b.nodes[edge.SourceID]; !ok {
			continue
		}

		if _, ok := b.nodes[edge.TargetID]; !ok {
			continue
		}

		if edge.ID == "" {
			edge.ID = edgeID(edge.Kind, edge.SourceID, edge.TargetID, edge.Evidence)
		}

		if edge.CreatedAt == "" {
			edge.CreatedAt = b.now
		}

		if edge.UpdatedAt == "" {
			edge.UpdatedAt = b.now
		}

		if _, exists := b.edges[edge.ID]; !exists {
			b.edges[edge.ID] = edge
		}
	}

	return countNodesFromMap(b.nodes, "file") - countNodesInScope(b.nodes, "file", scope)
}

func shouldDropPreviousNodeForIncrementalScope(node Node, scope string) bool {
	if node.Collector == "summary_index" {
		return true
	}

	if node.Kind == "summary" {
		return true
	}

	return strings.TrimSpace(node.Path) != "" && pathInScope(node.Path, scope)
}

func shouldDropPreviousEdgeForIncrementalScope(edge Edge, removed map[string]struct{}) bool {
	if edge.Collector == "summary_index" {
		return true
	}

	if _, ok := removed[edge.SourceID]; ok {
		return true
	}

	if _, ok := removed[edge.TargetID]; ok {
		return true
	}

	return false
}

func builderLanguageSet(nodes map[string]Node) []string {
	values := make(map[string]struct{})

	for _, node := range nodes {
		if strings.TrimSpace(node.Language) != "" {
			values[node.Language] = struct{}{}
		}
	}

	return sortedStringSet(values)
}

func builderCollectorSet(nodes map[string]Node, edges map[string]Edge) []string {
	values := make(map[string]struct{})

	for _, node := range nodes {
		if isCoverageCollector(node.Collector) {
			values[node.Collector] = struct{}{}
		}
	}

	for _, edge := range edges {
		if isCoverageCollector(edge.Collector) {
			values[edge.Collector] = struct{}{}
		}
	}

	return sortedStringSet(values)
}

func isCoverageCollector(collector string) bool {
	collector = strings.TrimSpace(collector)
	return collector != "" && collector != "context_index"
}

func countNodesFromMap(nodes map[string]Node, kind string) int {
	count := 0

	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
	}

	return count
}

func countNodesInScope(nodes map[string]Node, kind string, scope string) int {
	count := 0

	for _, node := range nodes {
		if node.Kind == kind && strings.TrimSpace(node.Path) != "" && pathInScope(node.Path, scope) {
			count++
		}
	}

	return count
}

func filterPathsInScope(paths []string, scope string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if pathInScope(path, scope) {
			out = append(out, path)
		}
	}

	return out
}

func countSymbolsForPaths(nodes []Node, paths []string) int {
	if len(paths) == 0 {
		return 0
	}

	pathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathSet[path] = struct{}{}
	}

	count := 0

	for _, node := range nodes {
		if !isSymbolKind(node.Kind) {
			continue
		}

		if _, ok := pathSet[node.Path]; ok {
			count++
		}
	}

	return count
}

func dependencyRefreshPaths(graph Graph, scope string) []string {
	paths := make(map[string]struct{})

	for _, node := range graph.Nodes {
		if node.Kind == "file" && strings.TrimSpace(node.Path) != "" && pathInScope(node.Path, scope) {
			paths[node.Path] = struct{}{}
		}
	}

	if len(paths) == 0 && scope != "." {
		paths[scope] = struct{}{}
	}

	nodePaths := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if strings.TrimSpace(node.Path) != "" {
			nodePaths[node.ID] = node.Path
		}
	}

	for _, edge := range graph.Edges {
		sourcePath := nodePaths[edge.SourceID]
		targetPath := nodePaths[edge.TargetID]

		if sourcePath == "" || targetPath == "" {
			continue
		}

		switch edge.Kind {
		case "imports", "calls", "tests":
		default:
			continue
		}

		sourceInScope := pathInScope(sourcePath, scope)
		targetInScope := pathInScope(targetPath, scope)

		switch {
		case sourceInScope && !targetInScope:
			paths[targetPath] = struct{}{}
		case targetInScope && !sourceInScope:
			paths[sourcePath] = struct{}{}
		}
	}

	return sortedPathSet(paths)
}

func commonRefreshScope(originalScope string, paths []string) string {
	if len(paths) <= 1 {
		return originalScope
	}

	common := ""

	for _, path := range paths {
		dir := pathDirForRefresh(path)
		if common == "" {
			common = dir
			continue
		}

		common = commonPathPrefix(common, dir)
		if common == "." {
			return "."
		}
	}

	if common == "" {
		return originalScope
	}

	if pathInScope(originalScope, common) {
		return common
	}

	return originalScope
}

func pathDirForRefresh(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))

	dir := filepath.ToSlash(filepath.Dir(path))

	if dir == "." || dir == "" {
		return "."
	}

	return dir
}

func commonPathPrefix(left string, right string) string {
	left = filepath.ToSlash(filepath.Clean(left))
	right = filepath.ToSlash(filepath.Clean(right))

	if left == right {
		return left
	}

	leftParts := strings.Split(left, "/")
	rightParts := strings.Split(right, "/")
	limit := len(leftParts)

	if len(rightParts) < limit {
		limit = len(rightParts)
	}

	var out []string

	for i := 0; i < limit; i++ {
		if leftParts[i] != rightParts[i] {
			break
		}

		out = append(out, leftParts[i])
	}

	if len(out) == 0 {
		return "."
	}

	return strings.Join(out, "/")
}

func sortedPathSet(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		if strings.TrimSpace(path) != "" {
			out = append(out, path)
		}
	}

	slices.Sort(out)

	return out
}
