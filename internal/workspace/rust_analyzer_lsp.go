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

// rustAnalyzerLSPClient wraps a subprocess rust-analyzer and speaks LSP
// over stdio. Shape mirrors clangdLSPClient — we intentionally keep the
// two clients parallel so adding a new LSP is a copy-and-rename job.
type rustAnalyzerLSPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	nextID int
}

type raEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *raRPCError     `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type raRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type raPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type raRange struct {
	Start raPosition `json:"start"`
	End   raPosition `json:"end"`
}

type raLocation struct {
	URI   string  `json:"uri"`
	Range raRange `json:"range"`
}

type raLocationLink struct {
	TargetURI            string  `json:"targetUri"`
	TargetRange          raRange `json:"targetRange"`
	TargetSelectionRange raRange `json:"targetSelectionRange"`
}

type raSymbolInfo struct {
	Name          string     `json:"name"`
	Kind          int        `json:"kind"`
	Location      raLocation `json:"location"`
	ContainerName string     `json:"containerName"`
}

type raCallHierarchyItem struct {
	Name           string  `json:"name"`
	Kind           int     `json:"kind"`
	URI            string  `json:"uri"`
	Range          raRange `json:"range"`
	SelectionRange raRange `json:"selectionRange"`
	Detail         string  `json:"detail"`
}

type raIncomingCall struct {
	From       raCallHierarchyItem `json:"from"`
	FromRanges []raRange           `json:"fromRanges"`
}

type raOutgoingCall struct {
	To         raCallHierarchyItem `json:"to"`
	FromRanges []raRange           `json:"fromRanges"`
}

func (m *Manager) rustAnalyzerBinary() string {
	path, _ := exec.LookPath("rust-analyzer")
	return path
}

func (m *Manager) newRustAnalyzerLSPClient(ctx context.Context) (*rustAnalyzerLSPClient, error) {
	ra := m.rustAnalyzerBinary()
	if ra == "" {
		return nil, fmt.Errorf("rust-analyzer is not installed; run `rustup component add rust-analyzer` or see https://rust-analyzer.github.io/")
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
	client := &rustAnalyzerLSPClient{
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

func (c *rustAnalyzerLSPClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *rustAnalyzerLSPClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *rustAnalyzerLSPClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *rustAnalyzerLSPClient) request(ctx context.Context, method string, params any, result any) error {
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
		var env raEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		respID, err := parseRAResponseID(env.ID)
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

func parseRAResponseID(raw json.RawMessage) (int, error) {
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

func (c *rustAnalyzerLSPClient) writeMessage(msg any) error {
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

func (c *rustAnalyzerLSPClient) readMessage() ([]byte, error) {
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
		return nil, fmt.Errorf("missing Content-Length header from rust-analyzer")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *rustAnalyzerLSPClient) openFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        raFileURI(path),
			"languageId": "rust",
			"version":    1,
			"text":       string(content),
		},
	}
	return c.notify("textDocument/didOpen", params)
}

func raFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func raPathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported rust-analyzer URI scheme %q", parsed.Scheme)
	}
	return filepath.Clean(parsed.Path), nil
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
	client, err := m.newRustAnalyzerLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": raFileURI(abs)},
		"position":     m.rustLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/definition", params, &raw); err != nil {
		return "", err
	}
	locations, err := parseRALocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatRALocations(locations, "", 4, false), nil
}

func (m *Manager) rustReferencesViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": raFileURI(abs)},
		"position":     m.rustLSPPosition(abs, match),
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}
	var locations []raLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatRALocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) rustImplementationsViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": raFileURI(abs)},
		"position":     m.rustLSPPosition(abs, match),
	}
	var raw json.RawMessage
	if err := client.request(ctx, "textDocument/implementation", params, &raw); err != nil {
		return "", err
	}
	locations, err := parseRALocations(raw)
	if err != nil {
		return "", err
	}
	return m.formatRALocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, false), nil
}

func (m *Manager) rustCallHierarchyViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string, incoming bool) (string, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
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
	items, err := m.rustAnalyzerPrepareCallHierarchy(ctx, client, abs, m.rustLSPPosition(abs, match))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", nil
	}
	if incoming {
		var calls []raIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return "", err
		}
		return m.formatRAIncomingCalls(calls, pathFilter), nil
	}
	var calls []raOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return "", err
	}
	return m.formatRAOutgoingCalls(abs, calls, pathFilter), nil
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

func parseRALocations(raw json.RawMessage) ([]raLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var list []raLocation
	if err := json.Unmarshal(trimmed, &list); err == nil {
		return list, nil
	}
	var single raLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []raLocation{single}, nil
	}
	var links []raLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil {
		out := make([]raLocation, 0, len(links))
		for _, link := range links {
			location := raLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
			if location.Range == (raRange{}) {
				location.Range = link.TargetRange
			}
			out = append(out, location)
		}
		return out, nil
	}
	var link raLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		location := raLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
		if location.Range == (raRange{}) {
			location.Range = link.TargetRange
		}
		return []raLocation{location}, nil
	}
	return nil, fmt.Errorf("unsupported rust-analyzer location payload")
}

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
		if !strings.HasPrefix(abs, m.cfg.Root) {
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
		kind := raSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "::" + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatRAIncomingCalls(calls []raIncomingCall, pathFilter string) string {
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

func (m *Manager) formatRAOutgoingCalls(callerPath string, calls []raOutgoingCall, pathFilter string) string {
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
