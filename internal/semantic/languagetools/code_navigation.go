package languagetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"idiom/internal/discovery"
	"idiom/internal/util"
)

const (
	defaultCodeNavigationMatchLimit = 50
	codeNavigationMaxMatchLimit     = 200
	codeNavigationFileLimit         = 64
)

type codeNavigationSpec struct {
	Name            string
	Capability      Capability
	RequiresPattern bool
	RequiresPath    bool
	Cost            string
	Class           string
}

var codeNavigationSpecs = []codeNavigationSpecDefinition{
	{Spec: codeNavigationSpec{Name: "file_outline", Capability: CapFileOutline, RequiresPath: true, Class: "language_symbol", Cost: "low"}, Aliases: []string{"file_outline", "outline"}},
	{Spec: codeNavigationSpec{Name: "find_symbol", Capability: CapFindSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low"}, Aliases: []string{"find_symbol", "symbol", "symbol_lookup", "lookup"}},
	{Spec: codeNavigationSpec{Name: "workspace_symbol", Capability: CapWorkspaceSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low"}, Aliases: []string{"workspace_symbol", "workspace_symbols", "find_symbol_workspace"}},
	{Spec: codeNavigationSpec{Name: "read_symbol", Capability: CapReadSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low"}, Aliases: []string{"read_symbol", "inspect_symbol", "symbol_body"}},
	{Spec: codeNavigationSpec{Name: "extract_body", Capability: CapExtractBody, RequiresPattern: true, Class: "language_symbol", Cost: "low"}, Aliases: []string{"extract_body", "full_body", "symbol_full_body"}},
	{Spec: codeNavigationSpec{Name: "definition", Capability: CapDefinition, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"definition", "definitions", "declaration", "declarations", "find_definition", "find_definitions", "find_declaration", "find_declarations", "goto_definition", "go_to_definition"}},
	{Spec: codeNavigationSpec{Name: "references", Capability: CapReferences, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"references", "reference", "usages", "find_references", "find_reference"}},
	{Spec: codeNavigationSpec{Name: "calls_from", Capability: CapCallsFrom, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"calls_from", "callees"}},
	{Spec: codeNavigationSpec{Name: "calls_into", Capability: CapCallsInto, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"calls_into", "callers"}},
	{Spec: codeNavigationSpec{Name: "implementations", Capability: CapImplementations, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"implementations", "implementation", "impls"}},
	{Spec: codeNavigationSpec{Name: "related_files", Capability: CapRelatedFiles, RequiresPattern: true, Class: "language_relationship", Cost: "medium"}, Aliases: []string{"related_files", "related"}},
}

type codeNavigationSpecDefinition struct {
	Spec    codeNavigationSpec
	Aliases []string
}

func (m *Manager) codeNavigate(ctx context.Context, req ToolRequest) ToolResult {

	// Fallback: when the model omits capability, infer it from the request
	// shape. The normalizer also handles this, but textual transports can
	// sometimes bypass the normalization pipeline — double-gating here
	// prevents needless "capability is required" errors.
	if strings.TrimSpace(req.Capability) == "" {
		if codeNavigateGraphResolutionOnly(req.Resolution) {
			return ToolResult{
				Tool:    "code_navigate",
				Summary: "Code navigation rejected",
				Error:   fmt.Sprintf("code_navigate received graph resolution %q without a navigation capability; use context_query for graph resolutions or set capability to one of %s", strings.TrimSpace(req.Resolution), CodeNavigationCapabilityList()),
			}
		}
		if strings.TrimSpace(req.Path) != "" {
			req.Capability = "file_outline"
		} else if strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Query) != "" || strings.TrimSpace(req.Symbol) != "" {
			req.Capability = "read_symbol"
		}
	}
	spec, err := codeNavigationSpecFor(req.Capability)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation rejected", Error: err.Error()}
	}
	req.Pattern = util.FirstNonEmpty(req.Pattern, req.Query, req.Symbol)
	if spec.RequiresPattern && strings.TrimSpace(req.Pattern) == "" {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation rejected", Error: fmt.Sprintf("pattern is required for %s", spec.Name)}
	}
	if spec.RequiresPath && strings.TrimSpace(req.Path) == "" {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation rejected", Error: fmt.Sprintf("path is required for %s", spec.Name)}
	}

	lang, ok, reason := m.codeNavigationLanguage(req)
	if ok {
		if tool := strings.TrimSpace(lang.Tool(spec.Capability)); tool != "" {
			routed := req
			routed.Tool = tool
			routed.Capability = ""
			result := m.execute(ctx, routed)
			if !codeNavigationCanFallback(result) {
				return codeNavigationWrapResult(spec, lang.Name, tool, result, "")
			}
			return m.genericCodeNavigationFallback(req, spec, fmt.Sprintf("%s unavailable: %s", tool, codeNavigationResultDetail(result)))
		}
		reason = fmt.Sprintf("%s has no %s route", lang.Name, spec.Name)
	}
	return m.genericCodeNavigationFallback(req, spec, reason)
}

func codeNavigationSpecFor(value string) (codeNavigationSpec, error) {
	key := normalizeCodeNavigationCapability(value)
	for _, entry := range codeNavigationSpecs {
		for _, alias := range entry.Aliases {
			if normalizeCodeNavigationCapability(alias) == key {
				return entry.Spec, nil
			}
		}
	}
	return codeNavigationSpec{}, fmt.Errorf("capability is required; choose one of %s", CodeNavigationCapabilityList())
}

func CodeNavigationSpecFor(value string) (codeNavigationSpec, error) {
	return codeNavigationSpecFor(value)
}

func CodeNavigationCapabilityNames() []string {
	names := make([]string, 0, len(codeNavigationSpecs))
	for _, entry := range codeNavigationSpecs {
		names = append(names, entry.Spec.Name)
	}
	return names
}

func CodeNavigationCapabilities() []Capability {
	capabilities := make([]Capability, 0, len(codeNavigationSpecs))
	for _, entry := range codeNavigationSpecs {
		capabilities = append(capabilities, entry.Spec.Capability)
	}
	return capabilities
}

func CodeNavigationCapabilityList() string {
	return strings.Join(CodeNavigationCapabilityNames(), ", ")
}

func normalizeCodeNavigationCapability(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

func codeNavigateGraphResolutionOnly(value string) bool {
	key := normalizeCodeNavigationCapability(value)
	switch key {
	case "repository", "package", "file", "symbol", "summary", "any":
		return true
	default:
		return false
	}
}

func (m *Manager) codeNavigationLanguage(req ToolRequest) (LanguageSupport, bool, string) {
	if path := strings.TrimSpace(req.Path); path != "" {
		if lang, ok := LanguageForFile(path); ok {
			return lang, true, ""
		}
		if m.codeNavigationPathIsFile(path, req.Workdir) || filepath.Ext(path) != "" {
			return LanguageSupport{}, false, fmt.Sprintf("no registered language route for %s", filepath.ToSlash(path))
		}
	}
	if language := strings.TrimSpace(req.Language); language != "" {
		if lang, ok := LanguageForName(language); ok {
			return lang, true, ""
		}
		return LanguageSupport{}, false, fmt.Sprintf("unsupported language %q", language)
	}
	if m != nil {
		active := m.ActiveLanguages()
		if len(active) == 1 {
			return active[0], true, ""
		}
		if len(active) > 1 {
			names := make([]string, 0, len(active))
			for _, lang := range active {
				names = append(names, lang.Name)
			}
			slices.Sort(names)
			return LanguageSupport{}, false, fmt.Sprintf("multiple active languages (%s); pass path or language to route precisely", strings.Join(names, ", "))
		}
	}
	return LanguageSupport{}, false, "no language route matched; using generic source text fallback"
}

func codeNavigationCanFallback(result ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + " " + result.Error))
	if text == "" {
		return false
	}
	return strings.Contains(text, "unavailable") ||
		strings.Contains(text, "not installed") ||
		strings.Contains(text, "language server") ||
		strings.Contains(text, "not supported")
}

func codeNavigationWrapResult(spec codeNavigationSpec, language string, routedTool string, result ToolResult, fallbackReason string) ToolResult {
	header := []string{
		fmt.Sprintf("capability: %s", spec.Name),
		fmt.Sprintf("language: %s", language),
		fmt.Sprintf("routed_tool: %s", routedTool),
	}
	if fallbackReason != "" {
		header = append(header, fmt.Sprintf("fallback_reason: %s", fallbackReason))
	}
	if detail := codeNavigationResultDetail(result); detail != "" {
		header = append(header, fmt.Sprintf("route_summary: %s", detail))
	}
	output := strings.Join(header, "\n")
	if strings.TrimSpace(result.Output) != "" {
		output += "\n\n" + strings.TrimSpace(result.Output)
	}
	summary := fmt.Sprintf("Code navigation routed %s via %s", spec.Name, routedTool)
	if fallbackReason != "" {
		summary = fmt.Sprintf("Code navigation used generic fallback for %s", spec.Name)
	}
	return ToolResult{
		Tool:          "code_navigate",
		Summary:       summary,
		Output:        strings.TrimSpace(output),
		Error:         strings.TrimSpace(result.Error),
		PrecisionTier: strings.TrimSpace(result.PrecisionTier),
		Relationships: append([]ToolRelationship(nil), result.Relationships...),
	}
}

func codeNavigationResultDetail(result ToolResult) string {
	return util.FirstNonEmpty(result.Error, result.Summary, result.Output)
}

func (m *Manager) genericCodeNavigationFallback(req ToolRequest, spec codeNavigationSpec, reason string) ToolResult {
	if spec.Name == "file_outline" {
		return m.genericCodeNavigationOutline(req, spec, reason)
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation rejected", Error: fmt.Sprintf("pattern is required for generic %s fallback", spec.Name)}
	}
	matches, truncated, err := m.genericCodeNavigationMatches(req)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation generic fallback failed", Error: err.Error()}
	}
	header := []string{
		fmt.Sprintf("capability: %s", spec.Name),
		"routed_tool: generic_source_text",
	}
	if strings.TrimSpace(reason) != "" {
		header = append(header, fmt.Sprintf("fallback_reason: %s", reason))
	}
	if len(matches) == 0 {
		return ToolResult{
			Tool:          "code_navigate",
			Summary:       fmt.Sprintf("No generic code navigation matches for %s", spec.Name),
			Output:        strings.Join(header, "\n"),
			PrecisionTier: PrecisionTierRGFallback,
		}
	}
	if truncated {
		header = append(header, "truncated: true")
	}
	output := strings.Join(header, "\n") + "\n\n" + strings.Join(matches, "\n")
	return ToolResult{
		Tool:          "code_navigate",
		Summary:       fmt.Sprintf("Code navigation used generic fallback for %s", spec.Name),
		Output:        strings.TrimSpace(output),
		PrecisionTier: PrecisionTierRGFallback,
	}
}

func (m *Manager) codeNavigationPathIsFile(path string, workdir string) bool {
	if m == nil || strings.TrimSpace(path) == "" {
		return false
	}
	abs, err := m.resolvePath(path, workdir)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

func (m *Manager) genericCodeNavigationOutline(req ToolRequest, spec codeNavigationSpec, reason string) ToolResult {
	abs, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation generic fallback failed", Error: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation generic fallback failed", Error: err.Error()}
	}
	if info.IsDir() {
		return m.genericCodeNavigationDirectoryOutline(abs, spec, reason)
	}
	start, end := req.StartLine, req.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end < start {
		end = start + 159
	}
	snippet, err := m.readFileLines(abs, start, end)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation generic fallback failed", Error: err.Error()}
	}
	result := ToolResult{
		Tool:          "generic_source_text",
		Summary:       fmt.Sprintf("Read %s lines %d-%d", m.displayPath(abs), start, end),
		Output:        snippet,
		PrecisionTier: PrecisionTierRGFallback,
	}
	return codeNavigationWrapResult(spec, "generic", "generic_source_text", result, reason)
}

func (m *Manager) genericCodeNavigationDirectoryOutline(abs string, spec codeNavigationSpec, reason string) ToolResult {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return ToolResult{Tool: "code_navigate", Summary: "Code navigation generic fallback failed", Error: err.Error()}
	}
	limit := codeNavigationFileLimit
	lines := []string{
		fmt.Sprintf("capability: %s", spec.Name),
		"routed_tool: directory_candidates",
		"fallback_reason: file_outline target is a directory",
	}
	if strings.TrimSpace(reason) != "" {
		lines = append(lines, fmt.Sprintf("language_reason: %s", reason))
	}
	lines = append(lines, "")
	count := 0
	for _, entry := range entries {
		if count >= limit {
			lines = append(lines, "truncated: true")
			break
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		display := m.displayPath(filepath.Join(abs, name))
		if entry.IsDir() {
			display += "/"
		}
		lines = append(lines, "candidate_file: "+display)
		count++
	}
	if count == 0 {
		lines = append(lines, "candidate_file_count: 0")
	}
	return ToolResult{
		Tool:          "code_navigate",
		Summary:       fmt.Sprintf("Code navigation listed %d candidate file(s) for directory outline", count),
		Output:        strings.TrimSpace(strings.Join(lines, "\n")),
		PrecisionTier: PrecisionTierRGFallback,
	}
}

func (m *Manager) genericCodeNavigationMatches(req ToolRequest) ([]string, bool, error) {
	scope := strings.TrimSpace(req.Path)
	if scope == "" {
		scope = "."
	}
	target, err := m.resolvePath(scope, req.Workdir)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, false, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultCodeNavigationMatchLimit
	}
	if limit > codeNavigationMaxMatchLimit {
		limit = codeNavigationMaxMatchLimit
	}
	files := []string{target}
	if info.IsDir() {
		files, err = m.genericCodeNavigationFiles(target)
		if err != nil {
			return nil, false, err
		}
	}
	pattern := strings.TrimSpace(req.Pattern)
	matches := make([]string, 0, min(limit, defaultCodeNavigationMatchLimit))
	truncated := false
	for _, file := range files {
		lines, err := genericCodeNavigationFileMatches(file, pattern, limit-len(matches))
		if err != nil {
			continue
		}
		for _, line := range lines {
			matches = append(matches, fmt.Sprintf("%s:%s", filepath.ToSlash(m.displayPath(file)), line))
			if len(matches) >= limit {
				truncated = true
				return matches, truncated, nil
			}
		}
	}
	return matches, truncated, nil
}

func (m *Manager) genericCodeNavigationFiles(root string) ([]string, error) {
	files := make([]string, 0, codeNavigationFileLimit)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), ".") || discovery.ShouldSkipRelFile(filepath.ToSlash(m.displayPath(path))) {
			return nil
		}
		if !sourceSanityCoversPath(path) {
			return nil
		}
		files = append(files, path)
		if len(files) >= codeNavigationFileLimit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool {
		return m.displayPath(files[i]) < m.displayPath(files[j])
	})
	return files, err
}

func genericCodeNavigationFileMatches(path string, pattern string, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	matches := make([]string, 0, min(remaining, 8))
	for i, line := range lines {
		if strings.Contains(line, pattern) {
			matches = append(matches, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line)))
			if len(matches) >= remaining {
				return matches, nil
			}
		}
	}
	return matches, nil
}

func codeNavigationDiscoveryProfile(req ToolRequest) ToolDiscoveryProfile {
	spec, err := codeNavigationSpecFor(req.Capability)
	if err != nil {
		return discoveryProfile("language_symbol", scopeFromPathAndPattern(req.Path, util.FirstNonEmpty(req.Pattern, req.Query)), "low", true)
	}
	return discoveryProfile(spec.Class, scopeFromPathAndPattern(req.Path, util.FirstNonEmpty(req.Pattern, req.Query)), spec.Cost, true)
}
