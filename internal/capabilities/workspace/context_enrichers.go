package workspace

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/textutil"
	"idiom/internal/repository/comprehension"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const (
	contextGraphLiveProbeTimeout = 10 * time.Second
	contextGraphFileProbeLimit   = 1
	contextGraphSymbolProbeLimit = 1
)

type workspaceGraphEnricher struct {
	manager *Manager
}

func (m *Manager) contextGraphEnrichers(mode string) []comprehension.GraphEnricher {
	if strings.ToLower(strings.TrimSpace(mode)) != "deep" {
		return nil
	}

	return []comprehension.GraphEnricher{workspaceGraphEnricher{manager: m}}
}

func (e workspaceGraphEnricher) EnrichGraph(ctx context.Context, graph comprehension.Graph, opts comprehension.EnrichmentOptions) (comprehension.GraphEnrichment, error) {
	if e.manager == nil {
		return comprehension.GraphEnrichment{}, nil
	}

	var enrichment comprehension.GraphEnrichment

	languages := graphLanguageSet(graph)

	for _, language := range sortedLanguageKeys(languages) {
		tools := languageEnrichmentTools(language)
		if len(tools) == 0 {
			continue
		}

		report := comprehension.CollectorReport{
			Name:      "workspace_tool_enrichment",
			Kind:      "live_tool_probe",
			Languages: []string{language},
			Active:    true,
			Available: true,
			Tools:     tools,
			Capabilities: []string{
				string(CapDiagnostics), string(CapReferences), string(CapCallsFrom), string(CapCallsInto), string(CapImplementations),
			},
			Properties: map[string]any{
				"mode":          opts.Mode,
				"probe_timeout": contextGraphLiveProbeTimeout.String(),
				"bounded":       true,
			},
		}
		nodes, edges, diagnostics, warnings := e.diagnosticEvidence(ctx, graph, language)

		enrichment.Nodes = append(enrichment.Nodes, nodes...)
		enrichment.Edges = append(enrichment.Edges, edges...)

		report.Diagnostics += diagnostics

		report.Warnings = append(report.Warnings, warnings...)

		report.NodesAdded += len(nodes)
		report.EdgesAdded += len(edges)

		nodes, edges, warnings = e.relationshipEvidence(ctx, graph, language)
		enrichment.Nodes = append(enrichment.Nodes, nodes...)
		enrichment.Edges = append(enrichment.Edges, edges...)
		report.Warnings = append(report.Warnings, warnings...)

		report.NodesAdded += len(nodes)
		report.EdgesAdded += len(edges)

		if report.Properties == nil {
			report.Properties = make(map[string]any)
		}

		report.Properties["normalized_relationship_edges"] = countWorkspaceRelationshipEdges(edges)
		if report.NodesAdded == 0 {
			report.Available = false
		}

		enrichment.Reports = append(enrichment.Reports, report)
	}

	return enrichment, nil
}

func (e workspaceGraphEnricher) diagnosticEvidence(ctx context.Context, graph comprehension.Graph, language string) ([]comprehension.Node, []comprehension.Edge, int, []string) {
	tool := semanticlanguage.DiagnosticToolForLanguage(language)
	if tool == "" {
		return nil, nil, 0, nil
	}

	files := fileNodesForGraphLanguage(graph, language, contextGraphFileProbeLimit)

	var (
		nodes    []comprehension.Node
		edges    []comprehension.Edge
		warnings []string
	)

	diagnostics := 0

	for _, file := range files {
		result := e.runBoundedTool(ctx, ToolRequest{Tool: tool, Path: file.Path, MaxDepth: 1})
		if strings.TrimSpace(result.Error) != "" {
			warnings = append(warnings, toolWarning(tool, file.Path, result))
			continue
		}

		diagnosticCount := countDiagnosticLines(result.Output)

		diagnostics += diagnosticCount

		nodeID := contextDiagnosticNodeID(language, file.Path, tool)

		nodes = append(nodes, comprehension.Node{
			ID:            nodeID,
			Kind:          "diagnostic_report",
			Path:          file.Path,
			Language:      language,
			QualifiedName: tool,
			Collector:     "workspace_tool_enrichment",
			Confidence:    confidenceFromPrecisionTier(result.PrecisionTier),
			Properties: map[string]any{
				"tool":           tool,
				"summary":        result.Summary,
				"precision_tier": result.PrecisionTier,
				"diagnostics":    diagnosticCount,
				"output_excerpt": boundedText(result.Output, 3000),
			},
		})
		edges = append(edges, comprehension.Edge{
			SourceID:   file.ID,
			TargetID:   nodeID,
			Kind:       "diagnosed_by",
			Collector:  "workspace_tool_enrichment",
			Confidence: confidenceFromPrecisionTier(result.PrecisionTier),
			Evidence: map[string]any{
				"path": file.Path,
				"tool": tool,
			},
		})
	}

	return nodes, edges, diagnostics, warnings
}

func (e workspaceGraphEnricher) relationshipEvidence(ctx context.Context, graph comprehension.Graph, language string) ([]comprehension.Node, []comprehension.Edge, []string) {
	symbols := symbolNodesForGraphLanguage(graph, language, contextGraphSymbolProbeLimit)
	capabilities := relationshipToolsForGraphLanguage(language)
	lookup := newGraphRelationshipLookup(graph)

	var (
		nodes    []comprehension.Node
		edges    []comprehension.Edge
		warnings []string
	)

	for _, symbol := range symbols {
		pattern := strings.TrimSpace(symbol.QualifiedName)
		if pattern == "" {
			continue
		}

		for capability, tool := range capabilities {
			if tool == "" {
				continue
			}

			if capability == string(CapImplementations) && !graphSymbolCanHaveImplementations(symbol.Kind) {
				continue
			}

			result := e.runBoundedTool(ctx, ToolRequest{Tool: tool, Pattern: pattern, Path: symbol.Path, MaxDepth: 4})
			if strings.TrimSpace(result.Error) != "" {
				warnings = append(warnings, toolWarning(tool, pattern, result))
				continue
			}

			if strings.TrimSpace(result.Output+result.Summary) == "" {
				continue
			}

			nodeID := contextEvidenceNodeID(tool, symbol.ID)

			nodes = append(nodes, comprehension.Node{
				ID:            nodeID,
				Kind:          "tool_evidence",
				Path:          symbol.Path,
				Language:      language,
				QualifiedName: capability,
				Collector:     "workspace_tool_enrichment",
				Confidence:    confidenceFromPrecisionTier(result.PrecisionTier),
				Properties: map[string]any{
					"tool":           tool,
					"capability":     capability,
					"summary":        result.Summary,
					"precision_tier": result.PrecisionTier,
					"output_excerpt": boundedText(result.Output, 3000),
				},
			})
			edges = append(edges, comprehension.Edge{
				SourceID:   symbol.ID,
				TargetID:   nodeID,
				Kind:       "enriched_by",
				Collector:  "workspace_tool_enrichment",
				Confidence: confidenceFromPrecisionTier(result.PrecisionTier),
				Evidence: map[string]any{
					"path":       symbol.Path,
					"symbol":     pattern,
					"tool":       tool,
					"capability": capability,
				},
			})
			edges = append(edges, normalizeToolRelationshipEdges(graph, lookup, symbol, capability, tool, result)...)
		}
	}

	return nodes, edges, warnings
}

func (e workspaceGraphEnricher) runBoundedTool(ctx context.Context, req ToolRequest) ToolResult {
	probeCtx, cancel := context.WithTimeout(ctx, contextGraphLiveProbeTimeout)
	defer cancel()

	return e.manager.Execute(probeCtx, req)
}

func graphLanguageSet(graph comprehension.Graph) map[string]struct{} {
	out := make(map[string]struct{}, len(graph.Languages))
	for _, language := range graph.Languages {
		language = strings.ToLower(strings.TrimSpace(language))
		if language != "" {
			out[language] = struct{}{}
		}
	}

	return out
}

func sortedLanguageKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func fileNodesForGraphLanguage(graph comprehension.Graph, language string, limit int) []comprehension.Node {
	var out []comprehension.Node

	for _, node := range graph.Nodes {
		if node.Kind != "file" || !graphLanguageMatches(language, node.Language) {
			continue
		}

		if strings.TrimSpace(node.Path) == "" {
			continue
		}

		out = append(out, node)
	}

	sort.Slice(out, func(i, j int) bool {
		left := liveProbeFileScore(out[i])
		right := liveProbeFileScore(out[j])

		if left != right {
			return left > right
		}

		return out[i].Path < out[j].Path
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

func symbolNodesForGraphLanguage(graph comprehension.Graph, language string, limit int) []comprehension.Node {
	var out []comprehension.Node

	for _, node := range graph.Nodes {
		if !graphLanguageMatches(language, node.Language) || !graphSymbolKindForLiveProbe(node.Kind) {
			continue
		}

		if strings.TrimSpace(node.Path) == "" || strings.TrimSpace(node.QualifiedName) == "" {
			continue
		}

		out = append(out, node)
	}

	sort.Slice(out, func(i, j int) bool {
		left := liveProbeSymbolScore(out[i])
		right := liveProbeSymbolScore(out[j])

		if left != right {
			return left > right
		}

		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].QualifiedName < out[j].QualifiedName
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

func graphLanguageMatches(requested string, actual string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	actual = strings.ToLower(strings.TrimSpace(actual))

	if requested == "typescript" {
		return actual == "typescript" || actual == "javascript"
	}

	if requested == "javascript" {
		return actual == "javascript"
	}

	return requested == actual
}

func graphSymbolKindForLiveProbe(kind string) bool {
	switch kind {
	case "func", "method", "type", "interface", "test":
		return true
	default:
		return false
	}
}

func graphSymbolCanHaveImplementations(kind string) bool {
	return kind == "type" || kind == "interface"
}

func relationshipToolsForGraphLanguage(language string) map[string]string {
	lang, ok := LanguageForName(language)
	if !ok {
		return nil
	}

	out := map[string]string{}
	for _, capability := range []Capability{CapReferences, CapCallsFrom, CapCallsInto, CapImplementations} {
		if tool := strings.TrimSpace(lang.Tool(capability)); tool != "" {
			out[string(capability)] = tool
		}
	}
	return out
}

func languageEnrichmentTools(language string) []string {
	tools := []string{}
	if tool := semanticlanguage.DiagnosticToolForLanguage(language); tool != "" {
		tools = append(tools, tool)
	}

	for _, tool := range relationshipToolsForGraphLanguage(language) {
		tools = append(tools, tool)
	}

	slices.Sort(tools)

	return tools
}

type graphRelationshipLookup struct {
	root          string
	files         map[string]comprehension.Node
	symbolsByPath map[string][]comprehension.Node
	symbolsByName map[string][]comprehension.Node
}

func newGraphRelationshipLookup(graph comprehension.Graph) graphRelationshipLookup {
	lookup := graphRelationshipLookup{
		root:          graph.Root,
		files:         make(map[string]comprehension.Node),
		symbolsByPath: make(map[string][]comprehension.Node),
		symbolsByName: make(map[string][]comprehension.Node),
	}
	for _, node := range graph.Nodes {
		if node.Kind == "file" && strings.TrimSpace(node.Path) != "" {
			lookup.files[node.Path] = node
			continue
		}

		if !graphNodeIsSymbol(node) || strings.TrimSpace(node.Path) == "" {
			continue
		}

		lookup.symbolsByPath[node.Path] = append(lookup.symbolsByPath[node.Path], node)
		for _, key := range graphSymbolLookupKeys(node) {
			lookup.symbolsByName[key] = append(lookup.symbolsByName[key], node)
		}
	}

	for path := range lookup.symbolsByPath {
		slices.SortFunc(lookup.symbolsByPath[path], func(a, b comprehension.Node) int {
			if c := cmp.Compare(a.StartLine, b.StartLine); c != 0 {
				return c
			}

			return cmp.Compare(a.QualifiedName, b.QualifiedName)
		})
	}

	return lookup
}

func normalizeToolRelationshipEdges(graph comprehension.Graph, lookup graphRelationshipLookup, subject comprehension.Node, capability string, tool string, result ToolResult) []comprehension.Edge {
	confidence := confidenceFromPrecisionTier(result.PrecisionTier)
	structured := normalizeStructuredToolRelationshipEdges(lookup, subject, capability, tool, confidence, result.Relationships)

	if len(structured) > 0 {
		return structured
	}

	var edges []comprehension.Edge

	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch capability {
		case string(CapCallsFrom):
			_, right, ok := splitToolArrow(line)
			if !ok {
				continue
			}

			targetID := lookup.resolveSymbolFromText(right)
			if targetID == "" || targetID == subject.ID {
				continue
			}

			edges = append(edges, toolRelationshipEdge("calls", subject.ID, targetID, tool, confidence, subject, line))
		case string(CapCallsInto):
			left, _, ok := splitToolArrow(line)
			if !ok {
				left = line
			}

			sourceID := lookup.resolveSymbolFromText(left)
			if sourceID == "" || sourceID == subject.ID {
				continue
			}

			edges = append(edges, toolRelationshipEdge("calls", sourceID, subject.ID, tool, confidence, subject, line))
		case string(CapImplementations):
			sourceID := lookup.resolveSymbolFromText(line)
			if sourceID == "" || sourceID == subject.ID {
				continue
			}

			edges = append(edges, toolRelationshipEdge("implements", sourceID, subject.ID, tool, confidence, subject, line))
		case string(CapReferences):
			sourceID := lookup.resolveReferenceSource(line)
			if sourceID == "" || sourceID == subject.ID {
				continue
			}

			edges = append(edges, toolRelationshipEdge("references", sourceID, subject.ID, tool, confidence, subject, line))
		}
	}

	return edges
}

func normalizeStructuredToolRelationshipEdges(lookup graphRelationshipLookup, subject comprehension.Node, capability string, tool string, confidence string, relationships []ToolRelationship) []comprehension.Edge {
	var edges []comprehension.Edge

	for _, relationship := range relationships {
		kind := strings.TrimSpace(relationship.Kind)
		if kind == "" {
			kind = relationshipKindForCapability(capability)
		}

		if kind == "" {
			continue
		}

		sourceID := lookup.resolveRelationshipEndpoint(relationship.SourcePath, relationship.SourceLine, relationship.SourceSymbol)
		targetID := lookup.resolveRelationshipEndpoint(relationship.TargetPath, relationship.TargetLine, relationship.TargetSymbol)

		switch capability {
		case string(CapCallsFrom):
			if sourceID == "" {
				sourceID = subject.ID
			}
		case string(CapCallsInto), string(CapReferences), string(CapImplementations):
			if targetID == "" {
				targetID = subject.ID
			}
		}

		if sourceID == "" || targetID == "" || sourceID == targetID {
			continue
		}

		edges = append(edges, toolRelationshipEdge(kind, sourceID, targetID, tool, confidence, subject, relationship.Evidence, true))
	}

	return edges
}

func relationshipKindForCapability(capability string) string {
	switch capability {
	case string(CapCallsFrom), string(CapCallsInto):
		return "calls"
	case string(CapReferences):
		return "references"
	case string(CapImplementations):
		return "implements"
	default:
		return ""
	}
}

func toolRelationshipEdge(kind string, sourceID string, targetID string, tool string, confidence string, subject comprehension.Node, line string, structured ...bool) comprehension.Edge {
	evidence := map[string]any{
		"path":    subject.Path,
		"symbol":  subject.QualifiedName,
		"tool":    tool,
		"line":    line,
		"live":    true,
		"bounded": true,
	}
	if len(structured) > 0 && structured[0] {
		evidence["structured_relationship"] = true
	}

	return comprehension.Edge{
		SourceID:   sourceID,
		TargetID:   targetID,
		Kind:       kind,
		Collector:  "workspace_tool_enrichment",
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func (lookup graphRelationshipLookup) resolveReferenceSource(text string) string {
	path, line, rest, ok := parseToolLocationText(text, lookup.root)
	if !ok {
		return ""
	}

	if node, found := lookup.symbolAt(path, line, firstToolName(rest)); found {
		return node.ID
	}

	if file, found := lookup.files[path]; found {
		return file.ID
	}

	return ""
}

func (lookup graphRelationshipLookup) resolveRelationshipEndpoint(path string, line int, symbol string) string {
	path = normalizeGraphOutputPath(path, lookup.root)
	symbol = strings.TrimSpace(symbol)

	if path != "" {
		if node, found := lookup.symbolAt(path, line, symbol); found {
			return node.ID
		}

		if node, found := lookup.symbolNamedInPath(path, symbol); found {
			return node.ID
		}

		if file, found := lookup.files[path]; found {
			return file.ID
		}
	}

	if node, found := lookup.symbolNamed(symbol); found {
		return node.ID
	}

	return ""
}

func (lookup graphRelationshipLookup) resolveSymbolFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if name, location, ok := strings.Cut(text, " in "); ok {
		path, line, rest, found := parseToolLocationText(location, lookup.root)
		if found {
			if node, ok := lookup.symbolAt(path, line, textutil.FirstNonEmpty(strings.TrimSpace(name), firstToolName(rest))); ok {
				return node.ID
			}
		}
	}

	path, line, rest, ok := parseToolLocationText(text, lookup.root)
	if ok {
		if node, found := lookup.symbolAt(path, line, firstToolName(rest)); found {
			return node.ID
		}
	}

	fields := strings.Fields(text)
	if len(fields) >= 2 {
		path := normalizeGraphOutputPath(fields[0], lookup.root)
		if _, ok := lookup.files[path]; ok {
			if node, found := lookup.symbolNamedInPath(path, firstToolName(strings.Join(fields[1:], " "))); found {
				return node.ID
			}
		}
	}

	if node, found := lookup.symbolNamed(firstToolName(text)); found {
		return node.ID
	}

	return ""
}

func (lookup graphRelationshipLookup) symbolAt(path string, line int, name string) (comprehension.Node, bool) {
	path = normalizeGraphOutputPath(path, lookup.root)
	if path == "" {
		return comprehension.Node{}, false
	}

	symbols := lookup.symbolsByPath[path]

	var best comprehension.Node

	bestScore := -1

	for _, node := range symbols {
		score := 0

		if line > 0 && node.StartLine > 0 {
			end := node.EndLine
			if end == 0 {
				end = node.StartLine
			}

			if line >= node.StartLine && line <= end {
				score += 40
				score += max(0, 20-(end-node.StartLine))
			} else if line == node.StartLine {
				score += 35
			}
		}

		if name != "" && graphSymbolMatchesName(node, name) {
			score += 50
		}

		if score > bestScore {
			best = node
			bestScore = score
		}
	}

	if bestScore <= 0 {
		return comprehension.Node{}, false
	}

	return best, true
}

func (lookup graphRelationshipLookup) symbolNamedInPath(path string, name string) (comprehension.Node, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return comprehension.Node{}, false
	}

	for _, node := range lookup.symbolsByPath[path] {
		if graphSymbolMatchesName(node, name) {
			return node, true
		}
	}

	return comprehension.Node{}, false
}

func (lookup graphRelationshipLookup) symbolNamed(name string) (comprehension.Node, bool) {
	keys := graphNameLookupKeys(name)

	var matches []comprehension.Node

	seen := make(map[string]struct{})

	for _, key := range keys {
		for _, node := range lookup.symbolsByName[key] {
			if _, ok := seen[node.ID]; ok {
				continue
			}

			seen[node.ID] = struct{}{}
			matches = append(matches, node)
		}
	}

	if len(matches) != 1 {
		return comprehension.Node{}, false
	}

	return matches[0], true
}

func parseToolLocationText(text string, root string) (string, int, string, bool) {
	text = strings.TrimSpace(text)
	for i := 0; i < len(text); i++ {
		if text[i] != ':' {
			continue
		}

		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}

		if j == i+1 {
			continue
		}

		line, err := strconv.Atoi(text[i+1 : j])
		if err != nil {
			continue
		}

		k := j
		if k < len(text) && text[k] == ':' {
			k++
			for k < len(text) && text[k] >= '0' && text[k] <= '9' {
				k++
			}
		}

		path := normalizeGraphOutputPath(text[:i], root)
		if path == "" {
			continue
		}

		return path, line, strings.TrimSpace(text[k:]), true
	}

	return "", 0, "", false
}

func normalizeGraphOutputPath(path string, root string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'(),`)
	if path == "" {
		return ""
	}

	path = strings.TrimPrefix(path, "file://")
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			path = rel
		}
	}

	path = pathpkg.Clean(strings.ReplaceAll(filepath.ToSlash(path), "\\", "/"))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}

	return strings.TrimPrefix(path, "./")
}

func splitToolArrow(line string) (string, string, bool) {
	left, right, ok := strings.Cut(line, "->")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(left), strings.TrimSpace(right), true
}

func firstToolName(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if before, _, ok := strings.Cut(text, " in "); ok {
		text = before
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}

	switch strings.ToLower(fields[0]) {
	case "func", "function", "method", "class", "type", "interface", "struct", "enum", "constructor":
		if len(fields) > 1 {
			return strings.Trim(fields[1], `"'(),`)
		}
	}

	return strings.Trim(fields[0], `"'(),`)
}

func graphSymbolLookupKeys(node comprehension.Node) []string {
	values := []string{node.QualifiedName}
	if name, ok := node.Properties["name"].(string); ok {
		values = append(values, name)
	}

	return graphNameLookupKeys(values...)
}

func graphNameLookupKeys(values ...string) []string {
	var keys []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		keys = append(keys, strings.ToLower(value))
		for _, sep := range []string{"#", ".", "::"} {
			if idx := strings.LastIndex(value, sep); idx >= 0 && idx+len(sep) < len(value) {
				keys = append(keys, strings.ToLower(value[idx+len(sep):]))
			}
		}
	}

	return textutil.UniqueNonEmpty(keys)
}

func graphSymbolMatchesName(node comprehension.Node, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}

	for _, key := range graphSymbolLookupKeys(node) {
		if key == name {
			return true
		}
	}

	return false
}

func graphNodeIsSymbol(node comprehension.Node) bool {
	switch node.Kind {
	case "symbol", "type", "class", "struct", "enum", "namespace", "interface", "method", "function", "func", "test", "bench", "constructor", "field", "const", "var", "signal", "extends", "autoload", "macro", "mod", "impl", "external_symbol":
		return true
	default:
		return false
	}
}

func liveProbeFileScore(node comprehension.Node) int {
	score := 0
	path := strings.ToLower(filepath.ToSlash(node.Path))

	if !pathLooksLikeTest(path) {
		score += 30
	}

	if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "src/") || strings.HasPrefix(path, "pkg/") || strings.HasPrefix(path, "app/") {
		score += 12
	}

	if strings.Contains(path, "/testdata/") || strings.Contains(path, "/vendor/") {
		score -= 40
	}

	return score
}

func liveProbeSymbolScore(node comprehension.Node) int {
	score := liveProbeFileScore(node)
	switch node.Kind {
	case "func", "function", "method":
		score += 40
	case "type", "interface", "class", "struct":
		score += 20
	case "test", "bench":
		score -= 25
	}

	if node.StartLine > 0 {
		score += 4
	}

	return score
}

func pathLooksLikeTest(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))

	base := filepath.Base(path)

	return strings.Contains(path, "/test/") || strings.Contains(path, "/tests/") || strings.Contains(path, "/__tests__/") || strings.Contains(path, "/testdata/") || strings.Contains(base, "test") || strings.Contains(base, "spec")
}

func countWorkspaceRelationshipEdges(edges []comprehension.Edge) int {
	count := 0

	for _, edge := range edges {
		if edge.Kind != "enriched_by" && edge.Kind != "diagnosed_by" {
			count++
		}
	}

	return count
}

func toolWarning(tool string, subject string, result ToolResult) string {
	message := fmt.Sprintf("%s %s: %s", tool, subject, strings.TrimSpace(result.Error))
	if excerpt := boundedText(result.Output, 500); excerpt != "" {
		message += " | output: " + strings.ReplaceAll(excerpt, "\n", " ")
	}

	return message
}

func confidenceFromPrecisionTier(tier string) string {
	switch tier {
	case PrecisionTierLSP:
		return "lsp"
	case PrecisionTierSyntaxIndex:
		return "syntax"
	case PrecisionTierRGFallback:
		return "heuristic"
	default:
		return "observed"
	}
}

func countDiagnosticLines(output string) int {
	count := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.Contains(lower, "no warnings or errors") {
			continue
		}

		if strings.Contains(lower, "no issues found") {
			continue
		}

		count++
	}

	return count
}

func boundedText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}

	return strings.TrimSpace(value[:maxBytes]) + "\n[truncated]"
}

func contextEvidenceNodeID(tool string, subject string) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + subject))
	return "evidence://" + strings.TrimSpace(tool) + "/" + hex.EncodeToString(sum[:8])
}

func contextDiagnosticNodeID(language string, path string, tool string) string {
	rel := filepath.ToSlash(filepath.Clean(path))
	sum := sha256.Sum256([]byte(language + "\x00" + rel + "\x00" + tool))

	return "diagnostic://" + strings.ToLower(strings.TrimSpace(language)) + "/" + hex.EncodeToString(sum[:8])
}
