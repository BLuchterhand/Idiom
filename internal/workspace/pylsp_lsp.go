package workspace

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/semantic"
)

// pylspLSPClient drives a Python LSP server over stdio JSON-RPC.
//
// Server choice, in order of preference:
//  1. pyright-langserver --stdio
//  2. pylsp
//
// Neither is bundled with idiom; callers report availability as a capability
// gap instead of prescribing installation.
type pylspLSPClient struct {
	lspBaseClient
	server string // "pyright" or "pylsp"
}

type pylspDiagnostic struct {
	Range    LSPRange `json:"range"`
	Severity int      `json:"severity"`
	Code     any      `json:"code"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

// pythonLSPBinary resolves the preferred LSP server binary. Returns
// ("", "") when neither pyright nor pylsp is on PATH.
func (m *Manager) pythonLSPBinary() (path string, server string) {
	if p, err := exec.LookPath("pyright-langserver"); err == nil {
		return p, "pyright"
	}
	if p, err := exec.LookPath("pylsp"); err == nil {
		return p, "pylsp"
	}
	return "", ""
}

// pythonLSPInstallHint returns a short, user-facing capability note.
func pythonLSPInstallHint() string {
	return "no Python LSP server available on PATH (pyright-langserver or pylsp)"
}

func (m *Manager) newPylspLSPClient(ctx context.Context) (*pylspLSPClient, error) {
	binary, server := m.pythonLSPBinary()
	if binary == "" {
		return nil, fmt.Errorf("%w: %s", ErrPythonLSPUnavailable, pythonLSPInstallHint())
	}
	var args []string
	switch server {
	case "pyright":
		args = []string{"--stdio"}
	case "pylsp":
		// pylsp defaults to stdio; no extra args required.
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = m.cfg.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &pylspLSPClient{server: server}
	client.lspBaseClient = lspBaseClient{
		serverName: "Python LSP",
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
		"rootUri":   pylspFileURI(m.cfg.Root),
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"symbol": map[string]any{
					"dynamicRegistration": false,
				},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  pylspFileURI(m.cfg.Root),
			"name": filepath.Base(m.cfg.Root),
		}},
	}
	var initResult map[string]any
	if err := client.request(ctx, "initialize", params, &initResult); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("%s initialize failed: %w", server, err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("%s initialized notification failed: %w", server, err)
	}
	return client, nil
}

func (c *pylspLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, "python")
}

func pylspFileURI(path string) string {
	return LSPFileURI(path)
}

func pylspPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "Python LSP")
}

func (m *Manager) pythonWorkspaceSymbolsViaLSP(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newPylspLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo
	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}
	if output := m.formatPylspWorkspaceSymbols(symbols, pathFilter, limit); strings.TrimSpace(output) != "" {
		return output, nil
	}

	index, err := m.getPythonIndex()
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
			"textDocument": map[string]any{"uri": pylspFileURI(abs)},
		}, &docSymbols); err != nil {
			continue
		}
		collected = append(collected, flattenPylspDocumentSymbols(abs, "", docSymbols)...)
	}
	return m.formatPylspWorkspaceSymbols(filterPylspDocumentSymbolMatches(collected, pattern), pathFilter, limit), nil
}

func (m *Manager) pythonDefinitionViaLSP(ctx context.Context, match semantic.PythonSymbolMatch) (string, error) {
	return m.pythonLocationQueryViaLSP(ctx, match, "textDocument/definition")
}

func (m *Manager) pythonLocationQueryViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, method string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: pylspFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, method, nil, "", 4, false)
}

func (m *Manager) pythonImplementationsViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: pylspFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.cfg.MaxDirectoryEntries, false)
}

func (m *Manager) pythonReferencesViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: pylspFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.cfg.MaxDirectoryEntries, true)
}

func (m *Manager) pythonCallHierarchyViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newPylspLSPClient(ctx)
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
	params := map[string]any{
		"textDocument": map[string]any{"uri": pylspFileURI(abs)},
		"position":     m.pythonLSPPosition(abs, match),
	}
	var items []LSPCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return callHierarchyResult{}, err
	}
	if len(items) == 0 {
		return callHierarchyResult{}, nil
	}
	if incoming {
		var calls []LSPIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return callHierarchyResult{}, err
		}
		return m.formatPylspIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []LSPOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}
	return m.formatPylspOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) pythonDiagnosticsViaLSP(ctx context.Context, abs string) ([]pylspDiagnostic, error) {
	client, err := m.newPylspLSPClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.close()

	if err := client.openFile(abs); err != nil {
		return nil, err
	}

	// Collect publishDiagnostics notifications for a short window. LSPs
	// publish diagnostics asynchronously; we poll readMessage with a
	// short deadline and stop when nothing comes for a beat.
	deadline := time.Now().Add(3 * time.Second)
	collected := make([]pylspDiagnostic, 0)
	for time.Now().Before(deadline) {
		// Bounded per-read context so we don't block forever.
		readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		body, err := pylspReadWithTimeout(readCtx, client)
		cancel()
		if err != nil {
			break
		}
		var env LSPEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if env.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var payload struct {
			URI         string            `json:"uri"`
			Diagnostics []pylspDiagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(env.Params, &payload); err != nil {
			continue
		}
		if path, err := pylspPathFromURI(payload.URI); err == nil && filepath.Clean(path) == filepath.Clean(abs) {
			collected = append(collected, payload.Diagnostics...)
			break
		}
	}
	return collected, nil
}

// pylspReadWithTimeout delegates to client.readMessage with a fresh
// goroutine so that ctx.Done() can unblock us. On timeout we fall
// through rather than error, signalling "no more diagnostics this
// beat".
func pylspReadWithTimeout(ctx context.Context, client *pylspLSPClient) ([]byte, error) {
	done := make(chan struct{})
	var body []byte
	var readErr error
	go func() {
		body, readErr = client.readMessage()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return body, readErr
	}
}

func (m *Manager) pythonLSPPosition(abs string, match semantic.PythonSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	character := match.Column - 1
	if character < 0 {
		character = 0
	}
	if resolved, ok := m.pythonLSPCharacter(abs, match); ok {
		character = resolved
	}
	return map[string]any{
		"line":      line,
		"character": character,
	}
}

func (m *Manager) pythonLSPCharacter(abs string, match semantic.PythonSymbolMatch) (int, bool) {
	if abs == "" || match.Line <= 0 {
		return 0, false
	}
	lineText := m.sourceLineText(abs, match.Line)
	if lineText == "" {
		return 0, false
	}
	if idx := strings.Index(lineText, match.Name); idx >= 0 {
		return idx, true
	}
	return 0, false
}

func (m *Manager) formatPylspLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}
	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})
	for _, location := range locations {
		abs, err := pylspPathFromURI(location.URI)
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

func (m *Manager) formatPylspWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}
	entries := make([]entry, 0, len(symbols))
	for _, symbol := range symbols {
		abs, err := pylspPathFromURI(symbol.Location.URI)
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
		kind := pylspSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "." + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatPylspIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.PythonSymbolMatch) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		abs, err := pylspPathFromURI(call.From.URI)
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

func (m *Manager) formatPylspOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		targetAbs, err := pylspPathFromURI(call.To.URI)
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

func pylspSymbolKindName(kind int) string {
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
	case 26:
		return "type-parameter"
	default:
		return "symbol"
	}
}

func flattenPylspDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, LSPSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: LSPLocation{
				URI:   pylspFileURI(path),
				Range: symbol.SelectionRange,
			},
		})
		nextContainer := symbol.Name
		if currentContainer != "" {
			nextContainer = currentContainer + "." + symbol.Name
		}
		out = append(out, flattenPylspDocumentSymbols(path, nextContainer, symbol.Children)...)
	}
	return out
}

func filterPylspDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
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
