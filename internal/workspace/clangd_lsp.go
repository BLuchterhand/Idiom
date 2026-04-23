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

type clangdLSPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	nextID int
}

type clangdEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *clangdRPCError `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type clangdRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type clangdPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type clangdRange struct {
	Start clangdPosition `json:"start"`
	End   clangdPosition `json:"end"`
}

type clangdLocation struct {
	URI   string      `json:"uri"`
	Range clangdRange `json:"range"`
}

type clangdLocationLink struct {
	TargetURI            string      `json:"targetUri"`
	TargetRange          clangdRange `json:"targetRange"`
	TargetSelectionRange clangdRange `json:"targetSelectionRange"`
}

type clangdSymbolInfo struct {
	Name          string         `json:"name"`
	Kind          int            `json:"kind"`
	Location      clangdLocation `json:"location"`
	ContainerName string         `json:"containerName"`
}

type clangdCallHierarchyItem struct {
	Name           string      `json:"name"`
	Kind           int         `json:"kind"`
	URI            string      `json:"uri"`
	Range          clangdRange `json:"range"`
	SelectionRange clangdRange `json:"selectionRange"`
	Detail         string      `json:"detail"`
}

type clangdDocumentSymbol struct {
	Name           string                 `json:"name"`
	Detail         string                 `json:"detail"`
	Kind           int                    `json:"kind"`
	Range          clangdRange            `json:"range"`
	SelectionRange clangdRange            `json:"selectionRange"`
	Children       []clangdDocumentSymbol `json:"children"`
}

type clangdIncomingCall struct {
	From       clangdCallHierarchyItem `json:"from"`
	FromRanges []clangdRange           `json:"fromRanges"`
}

type clangdOutgoingCall struct {
	To         clangdCallHierarchyItem `json:"to"`
	FromRanges []clangdRange           `json:"fromRanges"`
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

func (c *clangdLSPClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.request(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", map[string]any{})
	c.forceClose()
}

func (c *clangdLSPClient) forceClose() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, c.stdout)
	_ = c.cmd.Wait()
}

func (c *clangdLSPClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *clangdLSPClient) request(ctx context.Context, method string, params any, result any) error {
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
		var env clangdEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if len(env.ID) == 0 {
			continue
		}
		respID, err := parseClangdResponseID(env.ID)
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

func parseClangdResponseID(raw json.RawMessage) (int, error) {
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

func (c *clangdLSPClient) writeMessage(msg any) error {
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

func (c *clangdLSPClient) readMessage() ([]byte, error) {
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
		return nil, fmt.Errorf("missing Content-Length header from clangd")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
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

	var symbols []clangdSymbolInfo
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
	collected := make([]clangdSymbolInfo, 0, len(candidates)*4)
	for _, rel := range candidates {
		abs, err := m.resolvePath(rel, "")
		if err != nil {
			continue
		}
		if err := client.openFile(abs); err != nil {
			continue
		}
		var docSymbols []clangdDocumentSymbol
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
	var locations []clangdLocation
	if err := client.request(ctx, "textDocument/references", params, &locations); err != nil {
		return "", err
	}
	return m.formatClangdLocations(locations, pathFilter, m.cfg.MaxDirectoryEntries, true), nil
}

func (m *Manager) cppCallHierarchyViaClangd(ctx context.Context, match semantic.CppSymbolMatch, pathFilter string, incoming bool) (string, error) {
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
	items, err := m.clangdPrepareCallHierarchy(ctx, client, abs, m.cppLSPPosition(abs, match))
	if err != nil {
		return "", err
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
					if !strings.HasPrefix(targetAbs, m.cfg.Root) {
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
		return "", nil
	}
	if incoming {
		var calls []clangdIncomingCall
		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return "", err
		}
		return m.formatClangdIncomingCalls(calls, pathFilter), nil
	}
	var calls []clangdOutgoingCall
	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return "", err
	}
	return m.formatClangdOutgoingCalls(abs, calls, pathFilter), nil
}

func (m *Manager) clangdPrepareCallHierarchy(ctx context.Context, client *clangdLSPClient, abs string, position map[string]any) ([]clangdCallHierarchyItem, error) {
	if err := client.openFile(abs); err != nil {
		return nil, err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": clangdFileURI(abs)},
		"position":     position,
	}
	var items []clangdCallHierarchyItem
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

func parseClangdLocations(raw json.RawMessage) ([]clangdLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var list []clangdLocation
	if err := json.Unmarshal(trimmed, &list); err == nil {
		return list, nil
	}
	var single clangdLocation
	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []clangdLocation{single}, nil
	}
	var links []clangdLocationLink
	if err := json.Unmarshal(trimmed, &links); err == nil {
		out := make([]clangdLocation, 0, len(links))
		for _, link := range links {
			location := clangdLocation{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			}
			if location.Range == (clangdRange{}) {
				location.Range = link.TargetRange
			}
			out = append(out, location)
		}
		return out, nil
	}
	var link clangdLocationLink
	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		location := clangdLocation{
			URI:   link.TargetURI,
			Range: link.TargetSelectionRange,
		}
		if location.Range == (clangdRange{}) {
			location.Range = link.TargetRange
		}
		return []clangdLocation{location}, nil
	}
	return nil, fmt.Errorf("unsupported clangd location payload")
}

func (m *Manager) formatClangdLocations(locations []clangdLocation, pathFilter string, limit int, includeLineText bool) string {
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

func (m *Manager) formatClangdWorkspaceSymbols(symbols []clangdSymbolInfo, pathFilter string, limit int) string {
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
		kind := clangdSymbolKindName(item.kind)
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + "::" + item.name
		}
		lines = append(lines, fmt.Sprintf("%s:%d %s %s", handle, item.line, kind, qualified))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) formatClangdIncomingCalls(calls []clangdIncomingCall, pathFilter string) string {
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

func (m *Manager) formatClangdOutgoingCalls(callerPath string, calls []clangdOutgoingCall, pathFilter string) string {
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

func flattenDocumentSymbols(path string, container string, symbols []clangdDocumentSymbol) []clangdSymbolInfo {
	out := make([]clangdSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)
		out = append(out, clangdSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: clangdLocation{
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

func filterDocumentSymbolMatches(symbols []clangdSymbolInfo, pattern string) []clangdSymbolInfo {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return symbols
	}
	filtered := make([]clangdSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		name := strings.ToLower(symbol.Name)
		container := strings.ToLower(symbol.ContainerName)
		if strings.Contains(name, pattern) || strings.Contains(container, pattern) {
			filtered = append(filtered, symbol)
		}
	}
	return filtered
}
