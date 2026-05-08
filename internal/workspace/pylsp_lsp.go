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

// pylspLSPClient drives a Python LSP server over stdio JSON-RPC.
//
// Server choice, in order of preference:
//  1. pyright-langserver --stdio
//  2. pylsp
//
// Neither is bundled with idiom; callers report availability as a capability
// gap instead of prescribing installation.
type pylspLSPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	nextID int
	server string // "pyright" or "pylsp"
}

type pylspEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *pylspRPCError  `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type pylspRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pylspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type pylspRange struct {
	Start pylspPosition `json:"start"`
	End   pylspPosition `json:"end"`
}

type pylspLocation struct {
	URI   string     `json:"uri"`
	Range pylspRange `json:"range"`
}

type pylspLocationLink struct {
	TargetURI            string     `json:"targetUri"`
	TargetRange          pylspRange `json:"targetRange"`
	TargetSelectionRange pylspRange `json:"targetSelectionRange"`
}

type pylspSymbolInfo struct {
	Name          string        `json:"name"`
	Kind          int           `json:"kind"`
	Location      pylspLocation `json:"location"`
	ContainerName string        `json:"containerName"`
}

type pylspCallHierarchyItem struct {
	Name           string     `json:"name"`
	Kind           int        `json:"kind"`
	URI            string     `json:"uri"`
	Range          pylspRange `json:"range"`
	SelectionRange pylspRange `json:"selectionRange"`
	Detail         string     `json:"detail"`
}

type pylspDocumentSymbol struct {
	Name           string                `json:"name"`
	Detail         string                `json:"detail"`
	Kind           int                   `json:"kind"`
	Range          pylspRange            `json:"range"`
	SelectionRange pylspRange            `json:"selectionRange"`
	Children       []pylspDocumentSymbol `json:"children"`
}

type pylspIncomingCall struct {
	From       pylspCallHierarchyItem `json:"from"`
	FromRanges []pylspRange           `json:"fromRanges"`
}

type pylspOutgoingCall struct {
	To         pylspCallHierarchyItem `json:"to"`
	FromRanges []pylspRange           `json:"fromRanges"`
}

type pylspDiagnostic struct {
	Range    pylspRange `json:"range"`
	Severity int        `json:"severity"`
	Code     any        `json:"code"`
	Source   string     `json:"source"`
	Message  string     `json:"message"`
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
		return nil, fmt.Errorf("%s", pythonLSPInstallHint())
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
	client := &pylspLSPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		server: server,
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

func (c *pylspLSPClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *pylspLSPClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *pylspLSPClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *pylspLSPClient) request(ctx context.Context, method string, params any, result any) error {
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
		var env pylspEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		respID, err := parsePylspResponseID(env.ID)
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

func parsePylspResponseID(raw json.RawMessage) (int, error) {
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

func (c *pylspLSPClient) writeMessage(msg any) error {
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

func (c *pylspLSPClient) readMessage() ([]byte, error) {
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
		return nil, fmt.Errorf("missing Content-Length header from Python LSP")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *pylspLSPClient) openFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        pylspFileURI(path),
			"languageId": "python",
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

func pylspFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func pylspPathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported Python LSP URI scheme %q", parsed.Scheme)
	}
	return filepath.Clean(parsed.Path), nil
}

func (m *Manager) pythonSymbolPosition(match semantic.PythonSymbolMatch) string {
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

func (m *Manager) pythonWorkspaceSymbolsViaLSP(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newPylspLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []pylspSymbolInfo
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
	collected := make([]pylspSymbolInfo, 0, len(candidates)*4)
	for _, rel := range candidates {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			continue
		}
		if err := client.openFile(abs); err != nil {
			continue
		}
		var docSymbols []pylspDocumentSymbol
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
	client, err := m.newPylspLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": pylspFileURI(abs)},
		"position":     m.pythonLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}
	locations, err := parsePylspLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatPylspLocations(locations, "", 4, false), nil
}

func (m *Manager) pythonImplementationsViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newPylspLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": pylspFileURI(abs)},
		"position":     m.pythonLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/implementation", params, &raw); err != nil {
		return "", err
	}
	locations, err := parsePylspLocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatPylspLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, false), nil
}

func (m *Manager) pythonReferencesViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newPylspLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": pylspFileURI(abs)},
		"position":     m.pythonLSPPosition(abs, match),
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}
	var locations []pylspLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatPylspLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
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
	var items []pylspCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return callHierarchyResult{}, err
	}
	if len(items) == 0 {
		return callHierarchyResult{}, nil
	}
	if incoming {
		var calls []pylspIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return callHierarchyResult{}, err
		}
		return m.formatPylspIncomingCalls(calls, pathFilter, match), nil
	}
	var calls []pylspOutgoingCall
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
		var env pylspEnvelope
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

func parsePylspLocations(raw json.RawMessage) ([]pylspLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var list []pylspLocation
	if err := json.Unmarshal(trimmed, &list); err == nil {
		return list, nil
	}
	var single pylspLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []pylspLocation{single}, nil
	}
	var links []pylspLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil {
		out := make([]pylspLocation, 0, len(links))
		for _, link := range links {
			location := pylspLocation{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			}
			if location.Range == (pylspRange{}) {
				location.Range = link.TargetRange
			}
			out = append(out, location)
		}
		return out, nil
	}
	var link pylspLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		location := pylspLocation{
			URI:   link.TargetURI,
			Range: link.TargetSelectionRange,
		}
		if location.Range == (pylspRange{}) {
			location.Range = link.TargetRange
		}
		return []pylspLocation{location}, nil
	}
	return nil, fmt.Errorf("unsupported Python LSP location payload")
}

func (m *Manager) formatPylspLocations(locations []pylspLocation, pathFilter string, limit int, includeLineText bool) string {
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

func (m *Manager) formatPylspWorkspaceSymbols(symbols []pylspSymbolInfo, pathFilter string, limit int) string {
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
		kind := pylspSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "." + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatPylspIncomingCalls(calls []pylspIncomingCall, pathFilter string, target semantic.PythonSymbolMatch) callHierarchyResult {
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

func (m *Manager) formatPylspOutgoingCalls(callerPath string, callerName string, calls []pylspOutgoingCall, pathFilter string) callHierarchyResult {
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

func flattenPylspDocumentSymbols(path string, container string, symbols []pylspDocumentSymbol) []pylspSymbolInfo {
	out := make([]pylspSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, pylspSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: pylspLocation{
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

func filterPylspDocumentSymbolMatches(symbols []pylspSymbolInfo, pattern string) []pylspSymbolInfo {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return symbols
	}
	filtered := make([]pylspSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		name := strings.ToLower(symbol.Name)
		container := strings.ToLower(symbol.ContainerName)
		if strings.Contains(name, pattern) || strings.Contains(container, pattern) {
			filtered = append(filtered, symbol)
		}
	}
	return filtered
}
