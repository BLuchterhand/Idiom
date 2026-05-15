package workspace

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/semantic"
)

type clangdLSPClient struct {
	lspBaseClient
}



func (m *Manager) newClangdLSPClient(ctx context.Context) (*clangdLSPClient, error) {
	clangd := m.clangdBinary()
	if clangd == "" {
		return nil, fmt.Errorf("clangd is not installed")
	}
	args := []string{"--log=error"}
	if compileDir := m.compileCommandsDir(); compileDir != "" {
		args = append(args, "--compile-commands-dir="+compileDir)
	}

	cmd := exec.CommandContext(ctx, clangd, args...)
	cmd.Dir = m.cfg.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &clangdLSPClient{
		lspBaseClient: lspBaseClient{
			serverName: "clangd",
			cmd:        cmd,
			stdin:      stdin,
			stdout:     bufio.NewReader(stdout),
		},
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   clangdFileURI(m.cfg.Root),
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8"},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  clangdFileURI(m.cfg.Root),
			"name": filepath.Base(m.cfg.Root),
		}},
		"initializationOptions": map[string]any{
			"fallbackFlags": []string{"-xc++", "-std=c++20"},
		},
	}
	var initResult map[string]any
	if err := client.request(ctx, "initialize", params, &initResult); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("clangd initialize failed: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("clangd initialized notification failed: %w", err)
	}
	return client, nil
}

func (c *clangdLSPClient) openFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        clangdFileURI(path),
			"languageId": clangdLanguageID(path),
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

func clangdFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func clangdPathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported clangd URI scheme %q", parsed.Scheme)
	}
	if parsed.Scheme == "" {
		return filepath.Clean(parsed.Path), nil
	}
	return filepath.Clean(parsed.Path), nil
}

func clangdLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c":
		return "c"
	default:
		return "cpp"
	}
}

func (m *Manager) cppSymbolPosition(match semantic.CppSymbolMatch) string {
	line := match.Line
	if line <= 0 {
		line = 1
	}
	column := match.Column
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(match.Path), line, column)
}

func (m *Manager) cppWorkspaceSymbolsViaClangd(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo
	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}
	if output := m.formatClangdWorkspaceSymbols(symbols, pathFilter, limit); strings.TrimSpace(output) != "" {
		return output, nil
	}

	index, err := m.getCppIndex()
	if err != nil {
		return "", err
	}
	candidates := index.RelevantFiles(strings.Fields(pattern), max(limit*2, 6))
	if len(candidates) == 0 {
		candidates = index.DefaultFiles(max(limit*2, 6))
	}
	collected := make([]LSPSymbolInfo, 0, len(candidates)*4)
	for _, rel := range candidates {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			continue
		}
		if err := client.openFile(abs); err != nil {
			continue
		}
		var docSymbols []LSPDocumentSymbol
		if err := client.request(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		}, &docSymbols); err != nil {
			continue
		}
		collected = append(collected, flattenDocumentSymbols(abs, "", docSymbols)...)
	}
	return m.formatClangdWorkspaceSymbols(filterDocumentSymbolMatches(collected, pattern), pathFilter, limit), nil
}

func (m *Manager) cppDefinitionViaClangd(ctx context.Context, match semantic.CppSymbolMatch) (string, error) {
	return m.cppLocationQueryViaClangd(ctx, match, "textDocument/definition", true)
}

func (m *Manager) cppDeclarationViaClangd(ctx context.Context, match semantic.CppSymbolMatch) (string, error) {
	return m.cppLocationQueryViaClangd(ctx, match, "textDocument/declaration", false)
}

func (m *Manager) cppLocationQueryViaClangd(ctx context.Context, match semantic.CppSymbolMatch, method string, declarationFallback bool) (string, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return "", err
	}
	if err := client.openFile(abs); err != nil {
		return "", err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		"position":     m.cppLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}
	locations, err := parseClangdLocations(raw)
	if err != nil {
		return "", err
	}
	if declarationFallback && len(locations) == 0 {
		raw = nil
		if err := client.request(ctx, "textDocument/declaration", params, &raw); err == nil {
			locations, err = parseClangdLocations(raw)
			if err != nil {
				return "", err
			}
		}
	}
	return m.formatClangdLocations(locations, "", 4, false), nil
}

func (m *Manager) cppImplementationsViaClangd(ctx context.Context, match semantic.CppSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return "", err
	}
	if err := client.openFile(abs); err != nil {
		return "", err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		"position":     m.cppLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/implementation", params, &raw); err != nil {
		return "", err
	}
	locations, err := parseClangdLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatClangdLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, false), nil
}

func (m *Manager) cppReferencesViaClangd(ctx context.Context, match semantic.CppSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return "", err
	}
	if err := client.openFile(abs); err != nil {
		return "", err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		"position":     m.cppLSPPosition(abs, match),
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}
	var locations []LSPLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatClangdLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) cppCallHierarchyViaClangd(ctx context.Context, match semantic.CppSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return callHierarchyResult{}, err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return callHierarchyResult{}, err
	}
	if err := client.openFile(abs); err != nil {
		return callHierarchyResult{}, err
	}
	items, err := m.clangdPrepareCallHierarchy(ctx, client, abs, m.cppLSPPosition(abs, match))
	if err != nil {
		return callHierarchyResult{}, err
	}
	if len(items) == 0 {
		params := map[string]any{
			"textDocument": map[string]any{"uri": clangdFileURI(abs)},
			"position":     m.cppLSPPosition(abs, match),
		}
		var raw json.RawMessage
		if err := client.request(ctx, "textDocument/declaration", params, &raw); err == nil {
			if locations, parseErr := parseClangdLocations(raw); parseErr == nil && len(locations) > 0 {
				for _, location := range locations {
					targetAbs, pathErr := clangdPathFromURI(location.URI)
					if pathErr != nil {
						continue
					}
					if !m.pathWithinWorkspace(targetAbs) {
						continue
					}
					if fallbackItems, prepErr := m.clangdPrepareCallHierarchy(ctx, client, targetAbs, map[string]any{
						"line":      location.Range.Start.Line,
						"character": location.Range.Start.Character,
					}); prepErr == nil && len(fallbackItems) > 0 {
						items = fallbackItems
						break
					}
				}
			}
		}
	}
	if len(items) == 0 {
		return callHierarchyResult{}, nil
	}
	if incoming {
		var calls []LSPIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return callHierarchyResult{}, err
		}
		return m.formatClangdIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []LSPOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}
	return m.formatClangdOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) clangdPrepareCallHierarchy(ctx context.Context, client *clangdLSPClient, abs string, position map[string]any) ([]LSPCallHierarchyItem, error) {
	if err := client.openFile(abs); err != nil {
		return nil, err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		"position":     position,
	}
	var items []LSPCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) cppLSPPosition(abs string, match semantic.CppSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	character := match.Column - 1
	if character < 0 {
		character = 0
	}
	if resolved, ok := m.cppLSPCharacter(abs, match); ok {
		character = resolved
	}
	return map[string]any{
		"line":      line,
		"character": character,
	}
}

func (m *Manager) cppLSPCharacter(abs string, match semantic.CppSymbolMatch) (int, bool) {
	if abs == "" || match.Line <= 0 {
		return 0, false
	}
	lineText := m.sourceLineText(abs, match.Line)
	if lineText == "" {
		return 0, false
	}
	candidates := []string{match.Name}
	if strings.TrimSpace(match.Qualified) != "" {
		candidates = append([]string{match.Qualified}, candidates...)
		if idx := strings.LastIndex(match.Qualified, "::"); idx >= 0 && idx+2 < len(match.Qualified) {
			candidates = append([]string{match.Qualified[idx+2:]}, candidates...)
		}
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if idx := strings.Index(lineText, candidate); idx >= 0 {
			return idx, true
		}
	}
	return 0, false
}

func parseClangdLocations(raw json.RawMessage) ([]LSPLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var list []LSPLocation
	if err := json.Unmarshal(trimmed, &list); err == nil {
		return list, nil
	}
	var single LSPLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []LSPLocation{single}, nil
	}
	var links []LSPLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil {
		out := make([]LSPLocation, 0, len(links))
		for _, link := range links {
			location := LSPLocation{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			}
			if location.Range == (LSPRange{}) {
				location.Range = link.TargetRange
			}
			out = append(out, location)
		}
		return out, nil
	}
	var link LSPLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		location := LSPLocation{
			URI:   link.TargetURI,
			Range: link.TargetSelectionRange,
		}
		if location.Range == (LSPRange{}) {
			location.Range = link.TargetRange
		}
		return []LSPLocation{location}, nil
	}
	return nil, fmt.Errorf("unsupported clangd location payload")
}

func (m *Manager) formatClangdLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}
	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})
	for _, location := range locations {
		abs, err := clangdPathFromURI(location.URI)
		if err != nil {
			continue
		}
		if !m.pathWithinWorkspace(abs) {
			continue
		}
		displayPath := filepath.ToSlash(m.displayPath(abs))
		if pathFilter != "" && !pathMatchesFilter(displayPath, pathFilter) {
			continue
		}
		line := location.Range.Start.Line + 1
		col := location.Range.Start.Character + 1
		key := fmt.Sprintf("%s:%d:%d", abs, line, col)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		text := ""
		if includeLineText {
			text = m.sourceLineText(abs, line)
		}
		entries = append(entries, entry{path: abs, line: line, col: col, text: text})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.path, b.path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.line, b.line); c != 0 {
			return c
		}
		return cmp.Compare(a.col, b.col)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		handle := m.rememberPath(item.path)
		display := m.displayPath(item.path)
		if item.text != "" {
			lines = append(lines, fmt.Sprintf("%s %s:%d:%s", handle, display, item.line, item.text))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s:%d", handle, display, item.line))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatClangdWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}
	entries := make([]entry, 0, len(symbols))
	for _, symbol := range symbols {
		abs, err := clangdPathFromURI(symbol.Location.URI)
		if err != nil {
			continue
		}
		if !m.pathWithinWorkspace(abs) {
			continue
		}
		displayPath := filepath.ToSlash(m.displayPath(abs))
		if pathFilter != "" && !pathMatchesFilter(displayPath, pathFilter) {
			continue
		}
		entries = append(entries, entry{
			path:          abs,
			line:          symbol.Location.Range.Start.Line + 1,
			name:          symbol.Name,
			containerName: symbol.ContainerName,
			kind:          symbol.Kind,
		})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.path, b.path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.line, b.line); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		handle := m.handleDisplay(item.path)
		kind := clangdSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "::" + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatClangdIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.CppSymbolMatch) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		abs, err := clangdPathFromURI(call.From.URI)
		if err != nil {
			continue
		}
		if !m.pathWithinWorkspace(abs) {
			continue
		}
		displayPath := filepath.ToSlash(m.displayPath(abs))
		if pathFilter != "" && !pathMatchesFilter(displayPath, pathFilter) {
			continue
		}
		line := call.From.SelectionRange.Start.Line + 1
		entries = append(entries, entry{path: abs, line: line, name: call.From.Name})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.path, b.path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.line, b.line); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	lines := make([]string, 0, len(entries))
	relationships := make([]ToolRelationship, 0, len(entries))
	targetAbs, _ := m.resolvePath(target.Path, "")
	for _, item := range entries {
		line := fmt.Sprintf("%s:%d %s", m.handleDisplay(item.path), item.line, item.name)
		lines = append(lines, line)
		if targetAbs != "" {
			relationships = append(relationships, m.callRelationshipFromAbs(item.path, item.line, item.name, targetAbs, target.Line, target.Name, line))
		}
		if len(lines) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return callHierarchyResult{Output: strings.Join(lines, "\n"), Relationships: relationships}
}

func (m *Manager) formatClangdOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		targetAbs, err := clangdPathFromURI(call.To.URI)
		if err != nil {
			continue
		}
		if !m.pathWithinWorkspace(targetAbs) {
			continue
		}
		displayPath := filepath.ToSlash(m.displayPath(targetAbs))
		if pathFilter != "" && !pathMatchesFilter(displayPath, pathFilter) {
			continue
		}
		callsiteLine := call.To.SelectionRange.Start.Line + 1
		if len(call.FromRanges) > 0 {
			callsiteLine = call.FromRanges[0].Start.Line + 1
		}
		entries = append(entries, entry{
			callsitePath: callerPath,
			callsiteLine: callsiteLine,
			targetPath:   targetAbs,
			targetLine:   call.To.SelectionRange.Start.Line + 1,
			name:         call.To.Name,
		})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.callsitePath, b.callsitePath); c != 0 {
			return c
		}
		if c := cmp.Compare(a.callsiteLine, b.callsiteLine); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	lines := make([]string, 0, len(entries))
	relationships := make([]ToolRelationship, 0, len(entries))
	for _, item := range entries {
		line := fmt.Sprintf("%s:%d -> %s in %s:%d", m.handleDisplay(item.callsitePath), item.callsiteLine, item.name, m.handleDisplay(item.targetPath), item.targetLine)
		lines = append(lines, line)
		relationships = append(relationships, m.callRelationshipFromAbs(item.callsitePath, item.callsiteLine, callerName, item.targetPath, item.targetLine, item.name, line))
		if len(lines) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return callHierarchyResult{Output: strings.Join(lines, "\n"), Relationships: relationships}
}

func (m *Manager) sourceLineText(path string, line int) string {
	snippet, err := m.readFileLines(path, line, line)
	if err != nil {
		return ""
	}
	prefix := strconv.Itoa(line) + ":"
	return strings.TrimPrefix(snippet, prefix)
}

func clangdSymbolKindName(kind int) string {
	switch kind {
	case 5:
		return "class"
	case 6:
		return "method"
	case 9:
		return "constructor"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 22:
		return "struct"
	case 23:
		return "event"
	case 26:
		return "type-parameter"
	default:
		return "symbol"
	}
}

func flattenDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, LSPSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: LSPLocation{
				URI:   clangdFileURI(path),
				Range: symbol.SelectionRange,
			},
		})
		nextContainer := symbol.Name
		if currentContainer != "" {
			nextContainer = currentContainer + "::" + symbol.Name
		}
		out = append(out, flattenDocumentSymbols(path, nextContainer, symbol.Children)...)
	}
	return out
}

func filterDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return symbols
	}
	filtered := make([]LSPSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		name := strings.ToLower(symbol.Name)
		container := strings.ToLower(symbol.ContainerName)
		if strings.Contains(name, pattern) || strings.Contains(container, pattern) {
			filtered = append(filtered, symbol)
		}
	}
	return filtered
}
