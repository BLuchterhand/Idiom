package comprehension

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
)

func (b *graphBuilder) addSummaryIndex(repoID string, languages []string) CollectorReport {
	nodes := sortedNodes(b.nodes)
	edges := sortedEdges(b.edges)
	report := CollectorReport{
		Name:      "summary_index",
		Kind:      "derived_summary",
		Languages: append([]string(nil), languages...),
		Active:    true,
		Available: true,
		Capabilities: []string{
			"repository_summary", "package_summary", "file_summary", "summary_quality_tiers", "model_summary_overlay",
		},
		Properties: map[string]any{
			"durable":              true,
			"base_quality_tier":    "deterministic",
			"model_overlay_status": "not_configured",
		},
	}

	symbolsByPath := make(map[string][]Node)
	importsByFile := make(map[string]int)
	definesByFile := make(map[string]int)
	filesByPackage := make(map[string]int)
	symbolsByPackage := make(map[string]int)
	languagesByPackage := make(map[string]map[string]struct{})
	for _, node := range nodes {
		if isSymbolKind(node.Kind) && strings.TrimSpace(node.Path) != "" {
			symbolsByPath[node.Path] = append(symbolsByPath[node.Path], node)
		}
		if node.Kind == "file" {
			pkg := pathDir(node.Path)
			filesByPackage[pkg]++
			if languagesByPackage[pkg] == nil {
				languagesByPackage[pkg] = make(map[string]struct{})
			}
			if node.Language != "" {
				languagesByPackage[pkg][node.Language] = struct{}{}
			}
		}
	}
	for _, edge := range edges {
		switch edge.Kind {
		case "imports":
			if filePath := filePathForNodeID(edge.SourceID); filePath != "" {
				importsByFile[filePath]++
			}
		case "defines":
			if filePath := filePathForNodeID(edge.SourceID); filePath != "" {
				definesByFile[filePath]++
			}
		}
	}
	for path, symbols := range symbolsByPath {
		symbolsByPackage[pathDir(path)] += len(symbols)
	}

	repoSummaryID := summaryNodeID("repository", repoID)
	b.addNode(Node{
		ID:            repoSummaryID,
		Kind:          "summary",
		Path:          ".",
		QualifiedName: "repository summary",
		Collector:     "summary_index",
		Confidence:    "derived",
		Properties: map[string]any{
			"resolution":     "repository",
			"quality_tier":   "deterministic",
			"model_authored": false,
			"provenance":     "summary_index:repository",
			"ttl_seconds":    0,
			"files":          countNodes(nodes, "file"),
			"symbols":        countSymbolNodes(nodes),
			"relationships":  len(edges),
			"languages":      append([]string(nil), languages...),
		},
	})
	b.addEdge("summarizes", repoSummaryID, repoID, "summary_index", "derived", map[string]any{"resolution": "repository"}, nil)
	report.NodesAdded++
	report.EdgesAdded++

	for _, node := range nodes {
		switch node.Kind {
		case "package", "module":
			languageSet := languagesByPackage[node.Path]
			summaryID := summaryNodeID("package", node.ID)
			b.addNode(Node{
				ID:            summaryID,
				Kind:          "summary",
				Path:          node.Path,
				Language:      node.Language,
				QualifiedName: "package summary " + node.Path,
				Collector:     "summary_index",
				Confidence:    "derived",
				Properties: map[string]any{
					"resolution":     "package",
					"quality_tier":   "deterministic",
					"model_authored": false,
					"provenance":     "summary_index:package",
					"ttl_seconds":    0,
					"name":           firstStringProperty(node.Properties, "name", packageNameForDir(node.Path)),
					"files":          filesByPackage[node.Path],
					"symbols":        symbolsByPackage[node.Path],
					"languages":      sortedStringSet(languageSet),
				},
			})
			b.addEdge("summarizes", summaryID, node.ID, "summary_index", "derived", map[string]any{"resolution": "package", "path": node.Path}, nil)
			report.NodesAdded++
			report.EdgesAdded++
		case "file":
			symbols := symbolsByPath[node.Path]
			summaryID := summaryNodeID("file", node.ID)
			b.addNode(Node{
				ID:            summaryID,
				Kind:          "summary",
				Path:          node.Path,
				Language:      node.Language,
				QualifiedName: "file summary " + node.Path,
				Collector:     "summary_index",
				Confidence:    "derived",
				Properties: map[string]any{
					"resolution":     "file",
					"quality_tier":   "deterministic",
					"model_authored": false,
					"provenance":     "summary_index:file",
					"ttl_seconds":    0,
					"name":           filepath.Base(filepath.FromSlash(node.Path)),
					"symbols":        len(symbols),
					"definitions":    definesByFile[node.Path],
					"imports":        importsByFile[node.Path],
					"symbol_names":   summarySymbolNames(symbols, 12),
					"is_test":        boolProperty(node.Properties, "is_test"),
				},
			})
			b.addEdge("summarizes", summaryID, node.ID, "summary_index", "derived", map[string]any{"resolution": "file", "path": node.Path}, nil)
			report.NodesAdded++
			report.EdgesAdded++
		}
	}
	return report
}

func summaryNodeID(resolution string, targetID string) string {
	sum := sha256.Sum256([]byte(resolution + "\x00" + targetID))
	return "summary://" + resolution + "/" + hex.EncodeToString(sum[:8])
}

func filePathForNodeID(id string) string {
	if !strings.HasPrefix(id, "file://") {
		return ""
	}
	return strings.TrimPrefix(id, "file://")
}

func summarySymbolNames(symbols []Node, limit int) []string {
	values := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		name := strings.TrimSpace(symbol.QualifiedName)
		if name == "" {
			name = firstStringProperty(symbol.Properties, "name", "")
		}
		if name != "" {
			values = append(values, name)
		}
	}
	slices.Sort(values)
	values = uniqueStrings(values)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func firstStringProperty(properties map[string]any, key string, fallback string) string {
	if value, ok := properties[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func boolProperty(properties map[string]any, key string) bool {
	value, _ := properties[key].(bool)
	return value
}
