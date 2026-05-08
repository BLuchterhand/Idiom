package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/comprehension"
)

const (
	discoveryQuerySchemaVersion = 1
	discoveryQueryOutputLines   = 80
	discoveryQueryOutputChars   = 12000
)

type discoveryQueryPacket struct {
	SchemaVersion  int                   `json:"schema_version"`
	ID             string                `json:"id"`
	Purpose        string                `json:"purpose"`
	Query          string                `json:"query,omitempty"`
	Target         string                `json:"target,omitempty"`
	Pattern        string                `json:"pattern,omitempty"`
	Language       string                `json:"language,omitempty"`
	Depth          int                   `json:"depth"`
	GeneratedAt    string                `json:"generated_at"`
	ArtifactPath   string                `json:"artifact_path"`
	ContextIndexID string                `json:"context_index_id,omitempty"`
	ContextViews   []string              `json:"context_views,omitempty"`
	Layers         []discoveryQueryLayer `json:"layers"`
	Gaps           []string              `json:"gaps,omitempty"`
}

type discoveryQueryLayer struct {
	Name          string         `json:"name"`
	Tool          string         `json:"tool"`
	Request       map[string]any `json:"request,omitempty"`
	Summary       string         `json:"summary"`
	Error         string         `json:"error,omitempty"`
	OutputExcerpt string         `json:"output_excerpt,omitempty"`
	OutputLines   int            `json:"output_lines,omitempty"`
	OutputChars   int            `json:"output_chars,omitempty"`
	PrecisionTier string         `json:"precision_tier,omitempty"`
}

type discoveryQuerySummary struct {
	ID             string   `json:"id"`
	Purpose        string   `json:"purpose"`
	ArtifactPath   string   `json:"artifact_path"`
	ContextIndexID string   `json:"context_index_id,omitempty"`
	ContextViews   []string `json:"context_views,omitempty"`
	LayersRun      []string `json:"layers_run"`
	Evidence       []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Errors []string `json:"errors,omitempty"`
	Gaps   []string `json:"gaps,omitempty"`
}

type discoveryQueryRunner struct {
	manager *Manager
	ctx     context.Context
	packet  discoveryQueryPacket
	seen    map[string]struct{}
}

func (m *Manager) discoveryQuery(ctx context.Context, req ToolRequest) ToolResult {
	purpose := normalizeDiscoveryQueryPurpose(firstNonEmptyString(req.Purpose, req.Query, "impact_analysis"))
	if purpose == "" {
		return ToolResult{Tool: "discovery_query", Summary: "Discovery query rejected", Error: "purpose must be one of impact_analysis, function_tracing, architecture_boundary, security_review, or history_context"}
	}
	targetAbs, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "discovery_query", Summary: "Failed to resolve discovery query target", Error: err.Error()}
	}
	targetRel := m.displayPath(targetAbs)
	depth := req.MaxDepth
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}
	language := normalizeDiscoveryQueryLanguage(req.Language)
	if language == "" {
		language = normalizeDiscoveryQueryLanguage(m.inferDependencyLanguage(targetAbs))
	}

	artifactRel := filepath.ToSlash(filepath.Join(".idiom", "discovery", "queries", discoveryQueryID(m.cfg.Root, purpose, req, targetRel, language, depth)+".json"))
	runner := &discoveryQueryRunner{
		manager: m,
		ctx:     ctx,
		seen:    make(map[string]struct{}),
		packet: discoveryQueryPacket{
			SchemaVersion: discoveryQuerySchemaVersion,
			ID:            strings.TrimSuffix(filepath.Base(artifactRel), ".json"),
			Purpose:       purpose,
			Query:         strings.TrimSpace(req.Query),
			Target:        targetRel,
			Pattern:       strings.TrimSpace(req.Pattern),
			Language:      language,
			Depth:         depth,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			ArtifactPath:  artifactRel,
		},
	}

	runner.run(req, targetAbs)
	if err := runner.writeArtifact(); err != nil {
		return ToolResult{Tool: "discovery_query", Summary: "Discovery query artifact write failed", Error: err.Error(), Output: runner.summaryJSON()}
	}

	return ToolResult{
		Tool:    "discovery_query",
		Summary: fmt.Sprintf("Ran %s discovery query with %d layer(s); artifact: %s", purpose, len(runner.packet.Layers), artifactRel),
		Output:  runner.summaryJSON(),
	}
}

func (r *discoveryQueryRunner) run(req ToolRequest, targetAbs string) {
	purpose := r.packet.Purpose
	path := defaultString(req.Path, ".")
	pattern := strings.TrimSpace(req.Pattern)
	language := r.packet.Language

	contextBacked := r.addContextGraphLayer(req, path, pattern, language)
	r.addContextLayer()
	if isRegularFile(targetAbs) {
		r.addFileOrientation(path, language)
	} else if contextBacked {
		r.packet.Gaps = append(r.packet.Gaps, "fresh context graph supplied target orientation; skipped live target listing")
	} else {
		r.addLayer("target_listing", ToolRequest{Tool: "list_files", Path: path, Recursive: true, MaxDepth: 2})
	}

	if pattern != "" {
		r.addLayer("text_anchor_search", ToolRequest{Tool: "search_content", Path: path, Pattern: pattern, Glob: globForDiscoveryLanguage(language)})
	}

	switch purpose {
	case "impact_analysis":
		r.addDependency(path, language)
		r.addLanguageRelationshipLayers(path, pattern, language, true)
		r.addRelatedFileLayer(path, pattern, language)
		r.addHistoryLayers(path, pattern, req)
	case "function_tracing":
		r.addLanguageRelationshipLayers(path, pattern, language, true)
		r.addRelatedFileLayer(path, pattern, language)
		r.addCtagsFallback(path, pattern)
	case "architecture_boundary":
		r.addDependency(path, language)
		r.addRelatedFileLayer(path, pattern, language)
		r.addLanguageRelationshipLayers(path, pattern, language, false)
		r.addCtagsFallback(path, pattern)
	case "security_review":
		r.addDependency(path, language)
		r.addLayer("security_text_sweep", ToolRequest{Tool: "search_content", Path: path, Pattern: `eval\(|exec\(|password|secret|token|unsafe|panic\(`})
		r.addStructuralLayer(path, req)
		r.addLayer("semgrep_scan", ToolRequest{Tool: "semgrep_scan", Path: path, Pattern: defaultString(pattern, "auto")})
		r.addLayer("joern_scan", ToolRequest{Tool: "joern_scan", Path: path})
	case "history_context":
		r.addHistoryLayers(path, pattern, req)
		r.addRelatedFileLayer(path, pattern, language)
	default:
		r.packet.Gaps = append(r.packet.Gaps, "unknown purpose; ran only common orientation layers")
	}
}

type discoveryContextPackSummary struct {
	PackID         string   `json:"pack_id"`
	IndexID        string   `json:"index_id"`
	ArtifactPath   string   `json:"artifact_path"`
	Fresh          bool     `json:"fresh"`
	UnresolvedGaps []string `json:"unresolved_gaps"`
}

func (r *discoveryQueryRunner) addContextGraphLayer(req ToolRequest, path string, pattern string, language string) bool {
	status, err := comprehension.Status(r.manager.cfg.Root)
	if err != nil || !status.Indexed {
		return false
	}
	if status.IndexID != "" {
		r.packet.ContextIndexID = status.IndexID
	}
	if !status.Fresh {
		r.packet.Gaps = append(r.packet.Gaps, "context index is stale; skipped context graph backing and used live discovery layers")
		return false
	}
	task := discoveryContextPackTask(r.packet.Purpose, path, pattern, language, req.Query)
	result, ok := r.addLayerResult("context_pack", ToolRequest{
		Tool:  "context_pack",
		Task:  task,
		Path:  path,
		Mode:  "plan",
		Limit: 8,
	})
	if !ok || strings.TrimSpace(result.Error) != "" {
		return false
	}
	var pack discoveryContextPackSummary
	if err := json.Unmarshal([]byte(result.Output), &pack); err != nil {
		r.packet.Gaps = append(r.packet.Gaps, "context_pack output could not be decoded; used live discovery layers")
		return false
	}
	if pack.IndexID != "" {
		r.packet.ContextIndexID = pack.IndexID
	}
	if pack.ArtifactPath != "" && !slices.Contains(r.packet.ContextViews, pack.ArtifactPath) {
		r.packet.ContextViews = append(r.packet.ContextViews, pack.ArtifactPath)
	}
	if !pack.Fresh {
		r.packet.Gaps = append(r.packet.Gaps, "context_pack reported stale context; live discovery layers remain authoritative")
		return false
	}
	for _, gap := range pack.UnresolvedGaps {
		gap = strings.TrimSpace(gap)
		if gap != "" {
			r.packet.Gaps = append(r.packet.Gaps, "context_pack: "+gap)
		}
	}
	return true
}

func discoveryContextPackTask(purpose string, path string, pattern string, language string, query string) string {
	parts := []string{"Discovery query", purpose}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, strings.TrimSpace(query))
	}
	if strings.TrimSpace(pattern) != "" {
		parts = append(parts, "pattern "+strings.TrimSpace(pattern))
	}
	if strings.TrimSpace(path) != "" {
		parts = append(parts, "path "+strings.TrimSpace(path))
	}
	if strings.TrimSpace(language) != "" {
		parts = append(parts, "language "+strings.TrimSpace(language))
	}
	return strings.Join(parts, " ")
}

func (r *discoveryQueryRunner) addContextLayer() {
	path := filepath.Join(r.manager.cfg.Root, ".idiom", "discovery", "context.md")
	data, err := os.ReadFile(path)
	if err != nil {
		r.packet.Gaps = append(r.packet.Gaps, ".idiom/discovery/context.md was not present")
		return
	}
	r.packet.Layers = append(r.packet.Layers, discoveryQueryLayer{
		Name:          "project_behavior_digest",
		Tool:          "artifact_read",
		Request:       map[string]any{"path": ".idiom/discovery/context.md"},
		Summary:       "Read project behavior digest",
		OutputExcerpt: compactDiscoveryQueryOutput(string(data)),
		OutputLines:   countNonEmptyOutputLines(string(data)),
		OutputChars:   len(data),
	})
}

func (r *discoveryQueryRunner) addFileOrientation(path string, language string) {
	if prefix := discoveryLanguageToolPrefix(language); prefix != "" {
		r.addLayer("file_outline", ToolRequest{Tool: prefix + "_file_outline", Path: path})
		return
	}
	r.addLayer("file_read", ToolRequest{Tool: "read_file", Path: path, StartLine: 1, EndLine: 160})
}

func (r *discoveryQueryRunner) addDependency(path string, language string) {
	r.addLayer("dependency_graph", ToolRequest{Tool: "dependency_graph", Path: path, Language: language})
}

func (r *discoveryQueryRunner) addLanguageRelationshipLayers(path string, pattern string, language string, includeCalls bool) {
	prefix := discoveryLanguageToolPrefix(language)
	if prefix == "" {
		if pattern != "" {
			r.addCtagsFallback(path, pattern)
		}
		return
	}
	if pattern == "" {
		r.packet.Gaps = append(r.packet.Gaps, "pattern was empty; skipped symbol relationship layers")
		return
	}
	for _, tool := range []string{
		prefix + "_workspace_symbol",
		prefix + "_definition",
		prefix + "_references",
		prefix + "_read_symbol",
	} {
		r.addLayer(tool, ToolRequest{Tool: tool, Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
	}
	if includeCalls {
		for _, tool := range []string{prefix + "_calls_from", prefix + "_calls_into"} {
			r.addLayer(tool, ToolRequest{Tool: tool, Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
		}
	}
}

func (r *discoveryQueryRunner) addRelatedFileLayer(path string, pattern string, language string) {
	prefix := discoveryLanguageToolPrefix(language)
	if prefix == "" {
		return
	}
	r.addLayer("related_files", ToolRequest{Tool: prefix + "_related_files", Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
}

func (r *discoveryQueryRunner) addHistoryLayers(path string, pattern string, req ToolRequest) {
	if pattern != "" {
		r.addLayer("history_symbol", ToolRequest{Tool: "history_symbol", Path: path, Pattern: pattern})
	}
	if req.StartLine > 0 && req.EndLine >= req.StartLine {
		r.addLayer("history_range", ToolRequest{Tool: "history_range", Path: path, StartLine: req.StartLine, EndLine: req.EndLine})
		r.addLayer("blame_range", ToolRequest{Tool: "blame_range", Path: path, StartLine: req.StartLine, EndLine: req.EndLine})
		return
	}
	if path != "." {
		r.addLayer("blame_range", ToolRequest{Tool: "blame_range", Path: path, StartLine: 1, EndLine: 80})
	}
}

func (r *discoveryQueryRunner) addStructuralLayer(path string, req ToolRequest) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" || !strings.Contains(pattern, "$") {
		r.packet.Gaps = append(r.packet.Gaps, "structural_search skipped because pattern was not an ast-grep shape")
		return
	}
	r.addLayer("structural_search", ToolRequest{Tool: "structural_search", Path: path, Pattern: pattern, Language: r.packet.Language})
}

func (r *discoveryQueryRunner) addCtagsFallback(path string, pattern string) {
	if strings.TrimSpace(pattern) == "" {
		return
	}
	r.addLayer("ctags_symbols", ToolRequest{Tool: "ctags_symbols", Path: path, Pattern: pattern})
}

func (r *discoveryQueryRunner) addLayer(name string, req ToolRequest) {
	_, _ = r.addLayerResult(name, req)
}

func (r *discoveryQueryRunner) addLayerResult(name string, req ToolRequest) (ToolResult, bool) {
	if strings.TrimSpace(req.Tool) == "" || req.Tool == "discovery_query" {
		return ToolResult{}, false
	}
	keyData, _ := json.Marshal(req)
	key := name + ":" + string(keyData)
	if _, ok := r.seen[key]; ok {
		return ToolResult{}, false
	}
	r.seen[key] = struct{}{}

	result := r.manager.Execute(r.ctx, req)
	layer := discoveryQueryLayer{
		Name:          name,
		Tool:          result.Tool,
		Request:       requestMap(req),
		Summary:       result.Summary,
		Error:         result.Error,
		OutputExcerpt: compactDiscoveryQueryOutput(result.Output),
		OutputLines:   countNonEmptyOutputLines(result.Output),
		OutputChars:   len(result.Output),
		PrecisionTier: result.PrecisionTier,
	}
	r.packet.Layers = append(r.packet.Layers, layer)
	return result, true
}

func (r *discoveryQueryRunner) writeArtifact() error {
	path := filepath.Join(r.manager.cfg.Root, filepath.FromSlash(r.packet.ArtifactPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir query artifact dir: %w", err)
	}
	_ = ensureDiscoveryArtifactsIgnored(r.manager.cfg.Root)
	data, err := json.MarshalIndent(r.packet, "", "  ")
	if err != nil {
		return fmt.Errorf("encode query packet: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write query artifact: %w", err)
	}
	return nil
}

func (r *discoveryQueryRunner) summaryJSON() string {
	summary := discoveryQuerySummary{
		ID:             r.packet.ID,
		Purpose:        r.packet.Purpose,
		ArtifactPath:   r.packet.ArtifactPath,
		ContextIndexID: r.packet.ContextIndexID,
		ContextViews:   slices.Clone(r.packet.ContextViews),
		Gaps:           slices.Clone(r.packet.Gaps),
	}
	for _, layer := range r.packet.Layers {
		summary.LayersRun = append(summary.LayersRun, layer.Name+":"+layer.Tool)
		summary.Evidence = append(summary.Evidence, struct {
			Name          string `json:"name"`
			Tool          string `json:"tool"`
			Summary       string `json:"summary"`
			Error         string `json:"error,omitempty"`
			OutputExcerpt string `json:"output_excerpt,omitempty"`
		}{
			Name:          layer.Name,
			Tool:          layer.Tool,
			Summary:       layer.Summary,
			Error:         layer.Error,
			OutputExcerpt: compactDiscoveryQuerySummaryLayerOutput(layer),
		})
		if strings.TrimSpace(layer.Error) != "" {
			summary.Errors = append(summary.Errors, layer.Name+":"+layer.Tool+": "+layer.Error)
		}
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "impact", "impact_analysis":
		return "impact_analysis"
	case "trace", "function_trace", "function_tracing":
		return "function_tracing"
	case "architecture", "architecture_boundary", "boundary":
		return "architecture_boundary"
	case "security", "security_review":
		return "security_review"
	case "history", "history_context":
		return "history_context"
	default:
		return ""
	}
}

func normalizeDiscoveryQueryLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "go", "golang":
		return "go"
	case "ts", "typescript", "js", "javascript":
		return "typescript"
	case "py", "python":
		return "python"
	case "cpp", "c++", "cc", "cxx", "c":
		return "cpp"
	case "rs", "rust":
		return "rust"
	case "java":
		return "java"
	default:
		return ""
	}
}

func discoveryLanguageToolPrefix(language string) string {
	switch normalizeDiscoveryQueryLanguage(language) {
	case "go":
		return "go"
	case "typescript":
		return "ts"
	case "python":
		return "python"
	case "cpp":
		return "cpp"
	case "rust":
		return "rust"
	case "java":
		return "java"
	default:
		return ""
	}
}

func globForDiscoveryLanguage(language string) string {
	switch normalizeDiscoveryQueryLanguage(language) {
	case "go":
		return "*.go"
	case "typescript":
		return "*.{ts,tsx,js,jsx,mjs,cjs}"
	case "python":
		return "*.py"
	case "cpp":
		return "*.{c,cc,cpp,cxx,h,hpp,hxx}"
	case "rust":
		return "*.rs"
	case "java":
		return "*.java"
	default:
		return ""
	}
}

func discoveryQueryID(root string, purpose string, req ToolRequest, target string, language string, depth int) string {
	status, _ := execOutput(root, "git", "-C", root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all")
	status = filterDiscoveryQueryStatus(status)
	head, _ := execOutput(root, "git", "-C", root, "rev-parse", "HEAD")
	payload, _ := json.Marshal(map[string]any{
		"purpose":  purpose,
		"path":     target,
		"pattern":  strings.TrimSpace(req.Pattern),
		"query":    strings.TrimSpace(req.Query),
		"language": language,
		"depth":    depth,
		"head":     strings.TrimSpace(head),
		"status":   strings.TrimSpace(status),
	})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:8])
}

func filterDiscoveryQueryStatus(status string) string {
	var lines []string
	for _, line := range strings.Split(status, "\n") {
		if strings.Contains(line, ".idiom/") {
			continue
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func execOutput(dir string, name string, args ...string) (string, error) {
	cmd := execCommand(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

var execCommand = osExecCommand

func osExecCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func ensureDiscoveryArtifactsIgnored(root string) error {
	excludePath, err := gitInfoExcludePath(root)
	if err != nil || strings.TrimSpace(excludePath) == "" {
		return err
	}
	data, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".idiom/", "/.idiom/":
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	addition := "# Idiom discovery artifacts\n.idiom/\n"
	if len(data) > 0 {
		prefix := "\n"
		if strings.HasSuffix(string(data), "\n") {
			prefix = ""
		}
		addition = prefix + addition
	}
	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(addition)
	return err
}

func gitInfoExcludePath(root string) (string, error) {
	output, err := execOutput(root, "git", "-C", root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return path, nil
}

func requestMap(req ToolRequest) map[string]any {
	data, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	for key, value := range out {
		if emptyJSONValue(value) {
			delete(out, key)
		}
	}
	return out
}

func emptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case float64:
		return typed == 0
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func compactDiscoveryQuerySummaryLayerOutput(layer discoveryQueryLayer) string {
	if layer.Name == "project_behavior_digest" {
		return ""
	}
	return compactDiscoveryQueryEvidenceOutput(layer.OutputExcerpt)
}

func compactDiscoveryQueryOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > discoveryQueryOutputLines {
		lines = append(lines[:discoveryQueryOutputLines], "...")
	}
	out := strings.Join(lines, "\n")
	if len(out) > discoveryQueryOutputChars {
		out = out[:discoveryQueryOutputChars] + "\n..."
	}
	return out
}

func compactDiscoveryQueryEvidenceOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], "...")
	}
	out := strings.Join(lines, "\n")
	if len(out) > 2500 {
		out = out[:2500] + "\n..."
	}
	return out
}

func countNonEmptyOutputLines(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
