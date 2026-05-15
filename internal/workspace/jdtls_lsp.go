package workspace

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

// jdtlsLSPClient is the Java equivalent of clangdLSPClient. It spawns
// jdtls, speaks JSON-RPC over stdio, and exposes a small set of LSP
// methods the Java tools consume. The protocol surface is identical to
// clangd — just a different server — so the scaffolding here mirrors
// clangd_lsp.go beat-for-beat.
type jdtlsLSPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	nextID int
}



type jdtlsDiagnostic struct {
	Range    LSPRange `json:"range"`
	Severity int        `json:"severity"`
	Source   string     `json:"source"`
	Message  string     `json:"message"`
}

// jdtlsInitializeTimeout is generous: jdtls commonly spends 5–10s on
// project import for a pom.xml / build.gradle workspace.
const jdtlsInitializeTimeout = 30 * time.Second

func (m *Manager) jdtlsBinary() string {
	path, _ := exec.LookPath("jdtls")
	return path
}

func (m *Manager) newJdtlsLSPClient(ctx context.Context) (*jdtlsLSPClient, error) {
	jdtls := m.jdtlsBinary()
	if jdtls == "" {
		return nil, fmt.Errorf("jdtls is not installed")
	}
	cmd := exec.CommandContext(ctx, jdtls)
	cmd.Dir = m.cfg.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &jdtlsLSPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   jdtlsFileURI(m.cfg.Root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"callHierarchy": map[string]any{
					"dynamicRegistration": false,
				},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  jdtlsFileURI(m.cfg.Root),
			"name": filepath.Base(m.cfg.Root),
		}},
		"initializationOptions": map[string]any{
			// jdtls accepts arbitrary keys here; none are required.
		},
	}
	initCtx, cancel := context.WithTimeout(ctx, jdtlsInitializeTimeout)
	defer cancel()

	var initResult map[string]any
	if err := client.request(initCtx, "initialize", params, &initResult); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("jdtls initialize failed: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()
		return nil, fmt.Errorf("jdtls initialized notification failed: %w", err)
	}
	// Readiness probe: jdtls may still be importing projects when it
	// acknowledges `initialize`. We give it a short settle window
	// before returning to the caller. A real workspace/symbol probe
	// loop would be ideal; a small sleep is pragmatic given the
	// in-process tool-call timing budget.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
	}
	return client, nil
}

func (c *jdtlsLSPClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *jdtlsLSPClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *jdtlsLSPClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *jdtlsLSPClient) request(ctx context.Context, method string, params any, result any) error {
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
		if len(env.ID) == 0 {
			continue
		}
		respID, err := parseJdtlsResponseID(env.ID)
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

func parseJdtlsResponseID(raw json.RawMessage) (int, error) {
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

func (c *jdtlsLSPClient) writeMessage(msg any) error {
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

func (c *jdtlsLSPClient) readMessage() ([]byte, error) {
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
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header from jdtls")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *jdtlsLSPClient) openFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        jdtlsFileURI(path),
			"languageId": "java",
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

func jdtlsFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func jdtlsPathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported jdtls URI scheme %q", parsed.Scheme)
	}
	return filepath.Clean(parsed.Path), nil
}

func (m *Manager) javaLSPPosition(abs string, match semantic.JavaSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}
	character := match.Column - 1
	if character < 0 {
		character = 0
	}
	if resolved, ok := m.javaLSPCharacter(abs, match); ok {
		character = resolved
	}
	return map[string]any{
		"line":      line,
		"character": character,
	}
}

func (m *Manager) javaLSPCharacter(abs string, match semantic.JavaSymbolMatch) (int, bool) {
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

func parseJdtlsLocations(raw json.RawMessage) ([]LSPLocation, error) {
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
			loc := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
			if loc.Range == (LSPRange{}) {
				loc.Range = link.TargetRange
			}
			out = append(out, loc)
		}
		return out, nil
	}
	var link LSPLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		loc := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
		if loc.Range == (LSPRange{}) {
			loc.Range = link.TargetRange
		}
		return []LSPLocation{loc}, nil
	}
	return nil, fmt.Errorf("unsupported jdtls location payload")
}

func (m *Manager) formatJdtlsLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}
	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})
	for _, location := range locations {
		abs, err := jdtlsPathFromURI(location.URI)
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

func (m *Manager) formatJdtlsWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}
	entries := make([]entry, 0, len(symbols))
	for _, sym := range symbols {
		abs, err := jdtlsPathFromURI(sym.Location.URI)
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
			line:          sym.Location.Range.Start.Line + 1,
			name:          sym.Name,
			containerName: sym.ContainerName,
			kind:          sym.Kind,
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
		kind := jdtlsSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "." + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatJdtlsIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.JavaSymbolMatch) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		abs, err := jdtlsPathFromURI(call.From.URI)
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

func (m *Manager) formatJdtlsOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}
	entries := make([]entry, 0, len(calls))
	for _, call := range calls {
		targetAbs, err := jdtlsPathFromURI(call.To.URI)
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

func jdtlsSymbolKindName(kind int) string {
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
	default:
		return "symbol"
	}
}

func flattenJdtlsDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, LSPSymbolInfo{
			Name:          sym.Name,
			Kind:          sym.Kind,
			ContainerName: currentContainer,
			Location: LSPLocation{
				URI:   jdtlsFileURI(path),
				Range: sym.SelectionRange,
			},
		})
		nextContainer := sym.Name
		if currentContainer != "" {
			nextContainer = currentContainer + "." + sym.Name
		}
		out = append(out, flattenJdtlsDocumentSymbols(path, nextContainer, sym.Children)...)
	}
	return out
}

func filterJdtlsDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return symbols
	}
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		name := strings.ToLower(sym.Name)
		container := strings.ToLower(sym.ContainerName)
		if strings.Contains(name, pattern) || strings.Contains(container, pattern) {
			out = append(out, sym)
		}
	}
	return out
}

// javaWorkspaceSymbolsViaJdtls performs a workspace/symbol query,
// falling back to per-file documentSymbol scans if the workspace query
// returns nothing (which can happen before jdtls finishes indexing).
func (m *Manager) javaWorkspaceSymbolsViaJdtls(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newJdtlsLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo
	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}
	if output := m.formatJdtlsWorkspaceSymbols(symbols, pathFilter, limit); strings.TrimSpace(output) != "" {
		return output, nil
	}
	index, err := m.getJavaIndex()
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
			"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		}, &docSymbols); err != nil {
			continue
		}
		collected = append(collected, flattenJdtlsDocumentSymbols(abs, "", docSymbols)...)
	}
	return m.formatJdtlsWorkspaceSymbols(filterJdtlsDocumentSymbolMatches(collected, pattern), pathFilter, limit), nil
}

func (m *Manager) javaDefinitionViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch) (string, error) {
	return m.javaLocationQueryViaJdtls(ctx, match, "textDocument/definition")
}

func (m *Manager) javaLocationQueryViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch, method string) (string, error) {
	client, err := m.newJdtlsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		"position":     m.javaLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}
	locations, err := parseJdtlsLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatJdtlsLocations(locations, "", 4, false), nil
}

func (m *Manager) javaImplementationsViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newJdtlsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		"position":     m.javaLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/implementation", params, &raw); err != nil {
		return "", err
	}
	locations, err := parseJdtlsLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatJdtlsLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, false), nil
}

func (m *Manager) javaReferencesViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newJdtlsLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		"position":     m.javaLSPPosition(abs, match),
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}
	var locations []LSPLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatJdtlsLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) javaCallHierarchyViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newJdtlsLSPClient(ctx)
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
	prepareParams := map[string]any{
		"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		"position":     m.javaLSPPosition(abs, match),
	}
	var items []LSPCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", prepareParams, &items); err != nil {
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
		return m.formatJdtlsIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []LSPOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}
	return m.formatJdtlsOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}
