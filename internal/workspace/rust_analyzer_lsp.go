package workspace

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/semantic"
)

type rustAnalyzerLSPClient struct {
	lspBaseClient
}

type raLocation = LSPLocation
type raSymbolInfo = LSPSymbolInfo
type raCallHierarchyItem = LSPCallHierarchyItem
type raIncomingCall = LSPIncomingCall
type raOutgoingCall = LSPOutgoingCall

func (m *Manager) rustAnalyzerBinary() string {
	path, _ := exec.LookPath("rust-analyzer")
	return path
}

func (m *Manager) newRustAnalyzerLSPClient(ctx context.Context) (*rustAnalyzerLSPClient, error) {
	ra := m.rustAnalyzerBinary()
	if ra == "" {
		return nil, fmt.Errorf("rust-analyzer is not installed")
	}
	cmd := exec.CommandContext(ctx, ra)
	cmd.Dir = m.cfg.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &rustAnalyzerLSPClient{}
	client.lspBaseClient = lspBaseClient{
		serverName: "rust-analyzer",
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   raFileURI(m.cfg.Root),
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8"},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  raFileURI(m.cfg.Root),
			"name": filepath.Base(m.cfg.Root),
		}},
		"initializationOptions": map[string]any{
			"checkOnSave": map[string]any{"enable": false},
		},
	}
	var initResult map[string]any
	if err := client.request(ctx, "initialize", params, &initResult); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("rust-analyzer initialize failed: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("rust-analyzer initialized notification failed: %w", err)
	}
	return client, nil
}

func (c *rustAnalyzerLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, "rust")
}

func raFileURI(path string) string {
	return LSPFileURI(path)
}

func raPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "rust-analyzer")
}

// -- Capability wrappers ---------------------------------------------------

func (m *Manager) rustWorkspaceSymbolsViaRustAnalyzer(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []raSymbolInfo
	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}
	return m.formatRAWorkspaceSymbols(symbols, pathFilter, limit), nil
}

func (m *Manager) rustDefinitionViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: raFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/definition", nil, "", 4, false)
}

func (m *Manager) rustReferencesViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: raFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.cfg.MaxDirectoryEntries, true)
}

func (m *Manager) rustImplementationsViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: raFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.cfg.MaxDirectoryEntries, false)
}

func (m *Manager) rustCallHierarchyViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
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
	items, err := m.rustAnalyzerPrepareCallHierarchy(ctx, client, abs, m.rustLSPPosition(abs, match))
	if err != nil {
		return callHierarchyResult{}, err
	}
	if len(items) == 0 {
		return callHierarchyResult{}, nil
	}
	if incoming {
		var calls []raIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return callHierarchyResult{}, err
		}
		return m.formatRAIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []raOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}
	return m.formatRAOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) rustAnalyzerPrepareCallHierarchy(ctx context.Context, client *rustAnalyzerLSPClient, abs string, position map[string]any) ([]raCallHierarchyItem, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": raFileURI(abs)},
		"position":     position,
	}
	var items []raCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) rustLSPPosition(abs string, match semantic.RustSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	character := match.Column - 1
	if character < 0 {
		character = 0
	}
	if resolved, ok := m.rustLSPCharacter(abs, match); ok {
		character = resolved
	}
	return map[string]any{"line": line, "character": character}
}

func (m *Manager) rustLSPCharacter(abs string, match semantic.RustSymbolMatch) (int, bool) {
	if abs == "" || match.Line <= 0 {
		return 0, false
	}
	lineText := m.sourceLineText(abs, match.Line)
	if lineText == "" {
		return 0, false
	}
	candidates := []string{match.Name}
	if q := strings.TrimSpace(match.Qualified); q != "" {
		candidates = append([]string{q}, candidates...)
		if idx := strings.LastIndex(q, "::"); idx >= 0 && idx+2 < len(q) {
			candidates = append([]string{q[idx+2:]}, candidates...)
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

// -- Formatting -------------------------------------------------------------

func (m *Manager) formatRALocations(locations []raLocation, pathFilter string, limit int, includeLineText bool) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}
	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})
	for _, location := range locations {
		abs, err := raPathFromURI(location.URI)
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

func (m *Manager) formatRAWorkspaceSymbols(symbols []raSymbolInfo, pathFilter string, limit int) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}
	entries := make([]entry, 0, len(symbols))
	for _, symbol := range symbols {
		abs, err := raPathFromURI(symbol.Location.URI)
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
		kind := raSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "::" + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatRAIncomingCalls(calls []raIncomingCall, pathFilter string, target semantic.RustSymbolMatch) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		abs, err := raPathFromURI(call.From.URI)
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

func (m *Manager) formatRAOutgoingCalls(callerPath string, callerName string, calls []raOutgoingCall, pathFilter string) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		targetAbs, err := raPathFromURI(call.To.URI)
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

func raSymbolKindName(kind int) string {
	switch kind {
	case 5:
		return "struct"
	case 6:
		return "method"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface" // LSP uses 11 for "trait" in rust-analyzer
	case 12:
		return "fn"
	case 13:
		return "variable"
	case 14:
		return "const"
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
