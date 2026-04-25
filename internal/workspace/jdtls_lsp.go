package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

type jdtlsEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *jdtlsRPCError  `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type jdtlsRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jdtlsPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type jdtlsRange struct {
	Start jdtlsPosition `json:"start"`
	End   jdtlsPosition `json:"end"`
}

type jdtlsLocation struct {
	URI   string     `json:"uri"`
	Range jdtlsRange `json:"range"`
}

type jdtlsLocationLink struct {
	TargetURI            string     `json:"targetUri"`
	TargetRange          jdtlsRange `json:"targetRange"`
	TargetSelectionRange jdtlsRange `json:"targetSelectionRange"`
}

type jdtlsSymbolInfo struct {
	Name          string        `json:"name"`
	Kind          int           `json:"kind"`
	Location      jdtlsLocation `json:"location"`
	ContainerName string        `json:"containerName"`
}

type jdtlsDocumentSymbol struct {
	Name           string                `json:"name"`
	Detail         string                `json:"detail"`
	Kind           int                   `json:"kind"`
	Range          jdtlsRange            `json:"range"`
	SelectionRange jdtlsRange            `json:"selectionRange"`
	Children       []jdtlsDocumentSymbol `json:"children"`
}

type jdtlsCallHierarchyItem struct {
	Name           string     `json:"name"`
	Kind           int        `json:"kind"`
	URI            string     `json:"uri"`
	Range          jdtlsRange `json:"range"`
	SelectionRange jdtlsRange `json:"selectionRange"`
	Detail         string     `json:"detail"`
}

type jdtlsIncomingCall struct {
	From       jdtlsCallHierarchyItem `json:"from"`
	FromRanges []jdtlsRange           `json:"fromRanges"`
}

type jdtlsOutgoingCall struct {
	To         jdtlsCallHierarchyItem `json:"to"`
	FromRanges []jdtlsRange           `json:"fromRanges"`
}

type jdtlsDiagnostic struct {
	Range    jdtlsRange `json:"range"`
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
		return nil, fmt.Errorf("jdtls is not installed; install with `brew install jdtls` or from https://download.eclipse.org/jdtls/snapshots/")
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
		var env jdtlsEnvelope
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

func parseJdtlsLocations(raw json.RawMessage) ([]jdtlsLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var list []jdtlsLocation
	if err := json.Unmarshal(trimmed, &list); err == nil {
		return list, nil
	}
	var single jdtlsLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []jdtlsLocation{single}, nil
	}
	var links []jdtlsLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil {
		out := make([]jdtlsLocation, 0, len(links))
		for _, link := range links {
			loc := jdtlsLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
			if loc.Range == (jdtlsRange{}) {
				loc.Range = link.TargetRange
			}
			out = append(out, loc)
		}
		return out, nil
	}
	var link jdtlsLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		loc := jdtlsLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
		if loc.Range == (jdtlsRange{}) {
			loc.Range = link.TargetRange
		}
		return []jdtlsLocation{loc}, nil
	}
	return nil, fmt.Errorf("unsupported jdtls location payload")
}

func (m *Manager) formatJdtlsLocations(locations []jdtlsLocation, pathFilter string, limit int, includeLineText bool) string {
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
		if !strings.HasPrefix(abs, m.cfg.Root) {
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
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		if entries[i].line != entries[j].line {
			return entries[i].line < entries[j].line
		}
		return entries[i].col < entries[j].col
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

func (m *Manager) formatJdtlsWorkspaceSymbols(symbols []jdtlsSymbolInfo, pathFilter string, limit int) string {
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
		if !strings.HasPrefix(abs, m.cfg.Root) {
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
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		if entries[i].line != entries[j].line {
			return entries[i].line < entries[j].line
		}
		return entries[i].name < entries[j].name
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

func (m *Manager) formatJdtlsIncomingCalls(calls []jdtlsIncomingCall, pathFilter string) string {
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
		if !strings.HasPrefix(abs, m.cfg.Root) {
			continue
		}
		displayPath := filepath.ToSlash(m.displayPath(abs))
		if pathFilter != "" && !pathMatchesFilter(displayPath, pathFilter) {
			continue
		}
		line := call.From.SelectionRange.Start.Line + 1
		entries = append(entries, entry{path: abs, line: line, name: call.From.Name})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		if entries[i].line != entries[j].line {
			return entries[i].line < entries[j].line
		}
		return entries[i].name < entries[j].name
	})
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		lines = append(lines, fmt.Sprintf("%s:%d %s", m.handleDisplay(item.path), item.line, item.name))
		if len(lines) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatJdtlsOutgoingCalls(callerPath string, calls []jdtlsOutgoingCall, pathFilter string) string {
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
		if !strings.HasPrefix(targetAbs, m.cfg.Root) {
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
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].callsitePath != entries[j].callsitePath {
			return entries[i].callsitePath < entries[j].callsitePath
		}
		if entries[i].callsiteLine != entries[j].callsiteLine {
			return entries[i].callsiteLine < entries[j].callsiteLine
		}
		return entries[i].name < entries[j].name
	})
	lines := make([]string, 0, len(entries))
	for _, item := range entries {
		lines = append(lines, fmt.Sprintf("%s:%d -> %s in %s:%d", m.handleDisplay(item.callsitePath), item.callsiteLine, item.name, m.handleDisplay(item.targetPath), item.targetLine))
		if len(lines) >= m.cfg.MaxDirectoryEntries {
			break
		}
	}
	return strings.Join(lines, "\n")
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

func flattenJdtlsDocumentSymbols(path string, container string, symbols []jdtlsDocumentSymbol) []jdtlsSymbolInfo {
	out := make([]jdtlsSymbolInfo, 0, len(symbols))
	for _, sym := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, jdtlsSymbolInfo{
			Name:          sym.Name,
			Kind:          sym.Kind,
			ContainerName: currentContainer,
			Location: jdtlsLocation{
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

func filterJdtlsDocumentSymbolMatches(symbols []jdtlsSymbolInfo, pattern string) []jdtlsSymbolInfo {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return symbols
	}
	out := make([]jdtlsSymbolInfo, 0, len(symbols))
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

	var symbols []jdtlsSymbolInfo
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
	collected := make([]jdtlsSymbolInfo, 0, len(candidates)*4)
	for _, rel := range candidates {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			continue
		}
		if err := client.openFile(abs); err != nil {
			continue
		}
		var docSymbols []jdtlsDocumentSymbol
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
	var locations []jdtlsLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatJdtlsLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) javaCallHierarchyViaJdtls(ctx context.Context, match semantic.JavaSymbolMatch, pathFilter string, incoming bool) (string, error) {
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
	prepareParams := map[string]any{
		"textDocument": map[string]any{"uri": jdtlsFileURI(abs)},
		"position":     m.javaLSPPosition(abs, match),
	}
	var items []jdtlsCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", prepareParams, &items); err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil
	}
	if incoming {
		var calls []jdtlsIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return "", err
		}
		return m.formatJdtlsIncomingCalls(calls, pathFilter), nil
	}
	var calls []jdtlsOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return "", err
	}
	return m.formatJdtlsOutgoingCalls(abs, calls, pathFilter), nil
}
