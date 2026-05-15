package workspace

// typescript-language-server (tsserver-lsp) client. Mirrors
// clangd_lsp.go: JSON-RPC over stdio, bare-bones LSP lifecycle, just the
// surface area needed by ts_tools.go — workspace/symbol,
// textDocument/documentSymbol, textDocument/definition,
// textDocument/references, textDocument/implementation, call hierarchy,
// and textDocument/publishDiagnostics.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/semantic"
)

type tsLSPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	nextID int

	pendingDiagnostics map[string][]tsDiagnostic
}



type tsDiagnostic struct {
	Range    LSPRange `json:"range"`
	Severity int     `json:"severity"`
	Code     any     `json:"code"`
	Source   string  `json:"source"`
	Message  string  `json:"message"`
}

type tsPublishDiagnosticsParams struct {
	URI         string         `json:"uri"`
	Diagnostics []tsDiagnostic `json:"diagnostics"`
}

// tsserverBinary returns the path to typescript-language-server or "" if
// it's not installed. Callers should degrade gracefully — TS tools
// without a language server fall back to the semantic index.
func (m *Manager) tsserverBinary() string {
	path, _ := exec.LookPath("typescript-language-server")
	return path
}

func (m *Manager) newTsLSPClient(ctx context.Context) (*tsLSPClient, error) {
	tsserver := m.tsserverBinary()
	if tsserver == "" {
		return nil, fmt.Errorf("typescript-language-server is not installed")
	}
	cmd := exec.CommandContext(ctx, tsserver, "--stdio")
	cmd.Dir = m.cfg.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &tsLSPClient{
		cmd:                cmd,
		stdin:              stdin,
		stdout:             bufio.NewReader(stdout),
		pendingDiagnostics: make(map[string][]tsDiagnostic),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   tsFileURI(m.cfg.Root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{"linkSupport": true},
				"references":         map[string]any{},
				"implementation":     map[string]any{"linkSupport": true},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"callHierarchy":      map[string]any{},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  tsFileURI(m.cfg.Root),
			"name": filepath.Base(m.cfg.Root),
		}},
		"initializationOptions": map[string]any{
			"hostInfo":                          "idiom",
			"preferences":                       map[string]any{},
			"tsserver":                          map[string]any{"logVerbosity": "off"},
			"disableAutomaticTypingAcquisition": true,
		},
	}
	var initResult map[string]any
	if err := client.request(ctx, "initialize", params, &initResult); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("typescript-language-server initialize failed: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("typescript-language-server initialized notification failed: %w", err)
	}
	return client, nil
}

func (c *tsLSPClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *tsLSPClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *tsLSPClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *tsLSPClient) request(ctx context.Context, method string, params any, result any) error {
	id := c.nextID + 1
	c.nextID = id
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := c.writeMessage(msg); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := c.readMessage()
		if err != nil {
			if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
				return fmt.Errorf("%w: %s", err, stderr)
			}
			return err
		}
		var env LSPEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		// Route diagnostics notifications into the pending map.
		if env.Method == "textDocument/publishDiagnostics" && len(env.Params) > 0 {
			var params tsPublishDiagnosticsParams
			if err := json.Unmarshal(env.Params, &params); err == nil {
				c.pendingDiagnostics[params.URI] = params.Diagnostics
			}
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		respID, err := parseTsResponseID(env.ID)
		if err != nil || respID != id {
			continue
		}
		if env.Error != nil {
			return fmt.Errorf("%s (code %d)", env.Error.Message, env.Error.Code)
		}
		if result != nil && len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
}

func parseTsResponseID(raw json.RawMessage) (int, error) {
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var idString string
	if err := json.Unmarshal(raw, &idString); err != nil {
		return 0, err
	}
	return strconv.Atoi(idString)
}

func (c *tsLSPClient) writeMessage(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func (c *tsLSPClient) readMessage() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			value := strings.TrimSpace(parts[1])
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header from typescript-language-server")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *tsLSPClient) openFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        tsFileURI(path),
			"languageId": tsLanguageID(path),
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

// waitForDiagnostics drains pending LSP messages for a short window so
// publishDiagnostics has a chance to arrive for the opened document.
func (c *tsLSPClient) waitForDiagnostics(ctx context.Context, uri string, budget time.Duration) []tsDiagnostic {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if diags, ok := c.pendingDiagnostics[uri]; ok {
			return diags
		}
		if ctx.Err() != nil {
			return nil
		}
		// Use a tiny timeout request to force a message pump cycle.
		pumpCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		var dummy json.RawMessage
		_ = c.request(pumpCtx, "workspace/symbol", map[string]any{"query": ""}, &dummy)
		cancel()
	}
	return c.pendingDiagnostics[uri]
}

func tsFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func tsPathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported ts LSP URI scheme %q", parsed.Scheme)
	}
	return filepath.Clean(parsed.Path), nil
}

func tsLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	default:
		return "typescript"
	}
}

func (m *Manager) tsLSPPosition(abs string, match semantic.TsSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	character := match.Column - 1
	if character < 0 {
		character = 0
	}
	if resolved, ok := m.tsLSPCharacter(abs, match); ok {
		character = resolved
	}
	return map[string]any{
		"line":      line,
		"character": character,
	}
}

func (m *Manager) tsLSPCharacter(abs string, match semantic.TsSymbolMatch) (int, bool) {
	if abs == "" || match.Line <= 0 {
		return 0, false
	}
	lineText := m.sourceLineText(abs, match.Line)
	if lineText == "" {
		return 0, false
	}
	candidates := []string{match.Name}
	if strings.TrimSpace(match.Qualified) != "" {
		if idx := strings.LastIndex(match.Qualified, "."); idx >= 0 && idx+1 < len(match.Qualified) {
			candidates = append([]string{match.Qualified[idx+1:]}, candidates...)
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

func (m *Manager) tsWorkspaceSymbolsViaLSP(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newTsLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo
	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}
	if output := m.formatTsWorkspaceSymbols(symbols, pathFilter, limit); strings.TrimSpace(output) != "" {
		return output, nil
	}

	// Fallback: open likely files and scan documentSymbol results.
	index, err := m.getTsIndex()
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
			"textDocument": map[string]any{"uri": tsFileURI(abs)},
		}, &docSymbols); err != nil {
			continue
		}
		collected = append(collected, flattenTsDocumentSymbols(abs, "", docSymbols)...)
	}
	return m.formatTsWorkspaceSymbols(filterTsDocumentSymbolMatches(collected, pattern), pathFilter, limit), nil
}

func (m *Manager) tsDefinitionViaLSP(ctx context.Context, match semantic.TsSymbolMatch) (string, error) {
	return m.LSPLocationQueryViaLSP(ctx, match, "textDocument/definition")
}

func (m *Manager) LSPLocationQueryViaLSP(ctx context.Context, match semantic.TsSymbolMatch, method string) (string, error) {
	client, err := m.newTsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": tsFileURI(abs)},
		"position":     m.tsLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}
	locations, err := parseTsLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatTsLocations(locations, "", 4, false), nil
}

func (m *Manager) tsImplementationsViaLSP(ctx context.Context, match semantic.TsSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newTsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": tsFileURI(abs)},
		"position":     m.tsLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/implementation", params, &raw); err != nil {
		return "", err
	}
	locations, err := parseTsLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatTsLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, false), nil
}

func (m *Manager) tsReferencesViaLSP(ctx context.Context, match semantic.TsSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newTsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": tsFileURI(abs)},
		"position":     m.tsLSPPosition(abs, match),
		"context":      map[string]any{"includeDeclaration": true},
	}
	var locations []LSPLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatTsLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) tsCallHierarchyViaLSP(ctx context.Context, match semantic.TsSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newTsLSPClient(ctx)
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
	items, err := m.tsPrepareCallHierarchy(ctx, client, abs, m.tsLSPPosition(abs, match))
	if err != nil {
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
		return m.formatTsIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []LSPOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}
	return m.formatTsOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) tsPrepareCallHierarchy(ctx context.Context, client *tsLSPClient, abs string, position map[string]any) ([]LSPCallHierarchyItem, error) {
	if err := client.openFile(abs); err != nil {
		return nil, err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": tsFileURI(abs)},
		"position":     position,
	}
	var items []LSPCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) tsDiagnosticsViaLSP(ctx context.Context, rel string) (string, error) {
	client, err := m.newTsLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(rel, "")
	if err != nil {
		return "", err
	}
	if err := client.openFile(abs); err != nil {
		return "", err
	}
	diagnostics := client.waitForDiagnostics(ctx, tsFileURI(abs), 2*time.Second)
	return m.formatTsDiagnostics(abs, diagnostics), nil
}

func parseTsLocations(raw json.RawMessage) ([]LSPLocation, error) {
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
			location := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
			if location.Range == (LSPRange{}) {
				location.Range = link.TargetRange
			}
			out = append(out, location)
		}
		return out, nil
	}
	var link LSPLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		location := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
		if location.Range == (LSPRange{}) {
			location.Range = link.TargetRange
		}
		return []LSPLocation{location}, nil
	}
	return nil, fmt.Errorf("unsupported ts LSP location payload")
}

func (m *Manager) formatTsLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}
	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})
	for _, location := range locations {
		abs, err := tsPathFromURI(location.URI)
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

func (m *Manager) formatTsWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}
	entries := make([]entry, 0, len(symbols))
	for _, symbol := range symbols {
		abs, err := tsPathFromURI(symbol.Location.URI)
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
		kind := tsSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "." + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatTsIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.TsSymbolMatch) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		abs, err := tsPathFromURI(call.From.URI)
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

func (m *Manager) formatTsOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		targetAbs, err := tsPathFromURI(call.To.URI)
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

func (m *Manager) formatTsDiagnostics(abs string, diagnostics []tsDiagnostic) string {
	if len(diagnostics) == 0 {
		return ""
	}
	display := m.displayPath(abs)
	lines := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		severity := tsSeverityName(diag.Severity)
		line := diag.Range.Start.Line + 1
		col := diag.Range.Start.Character + 1
		message := strings.TrimSpace(diag.Message)
		if code := stringifyTsDiagCode(diag.Code); code != "" {
			message = fmt.Sprintf("%s [%s]", message, code)
		}
		lines = append(lines, fmt.Sprintf("%s:%d:%d %s: %s", display, line, col, severity, message))
	}
	return strings.Join(lines, "\n")
}

func stringifyTsDiagCode(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}

func tsSymbolKindName(kind int) string {
	switch kind {
	case 5:
		return "class"
	case 6:
		return "method"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
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

func tsSeverityName(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "diagnostic"
	}
}

func flattenTsDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, LSPSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: LSPLocation{
				URI:   tsFileURI(path),
				Range: symbol.SelectionRange,
			},
		})
		nextContainer := symbol.Name
		if currentContainer != "" {
			nextContainer = currentContainer + "." + symbol.Name
		}
		out = append(out, flattenTsDocumentSymbols(path, nextContainer, symbol.Children)...)
	}
	return out
}

func filterTsDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
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
