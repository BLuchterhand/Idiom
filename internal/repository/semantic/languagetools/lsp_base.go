package languagetools

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
)

type lspBaseClient struct {
	process            *commandexec.Process
	workspaceRoot      string
	stdin              io.WriteCloser
	stdout             *bufio.Reader
	nextID             int
	serverName         string
	handleNotification lspNotificationHandler
}

type LSPEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *LSPRPCError    `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type LSPRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type LSPPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type LSPRange struct {
	Start LSPPosition `json:"start"`
	End   LSPPosition `json:"end"`
}

type LSPLocation struct {
	URI   string   `json:"uri"`
	Range LSPRange `json:"range"`
}

type LSPLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          LSPRange `json:"targetRange"`
	TargetSelectionRange LSPRange `json:"targetSelectionRange"`
}

type LSPSymbolInfo struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      LSPLocation `json:"location"`
	ContainerName string      `json:"containerName"`
}

type LSPCallHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	URI            string   `json:"uri"`
	Range          LSPRange `json:"range"`
	SelectionRange LSPRange `json:"selectionRange"`
	Detail         string   `json:"detail"`
}

type LSPDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail"`
	Kind           int                 `json:"kind"`
	Range          LSPRange            `json:"range"`
	SelectionRange LSPRange            `json:"selectionRange"`
	Children       []LSPDocumentSymbol `json:"children"`
}

type LSPIncomingCall struct {
	From       LSPCallHierarchyItem `json:"from"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

type LSPOutgoingCall struct {
	To         LSPCallHierarchyItem `json:"to"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

type lspNotificationHandler func(method string, params json.RawMessage)

type managedLSPClient interface {
	close()
	openFile(path string) error
	request(ctx context.Context, method string, params any, result any) error
}

type lspLocationQueryConfig struct {
	serverName string
	newClient  func(context.Context) (managedLSPClient, error)
	fileURI    func(string) string
	format     func([]LSPLocation, string, int, bool) string
}

type lspWorkspaceSymbolQueryConfig struct {
	newClient  func(context.Context) (managedLSPClient, error)
	fileURI    func(string) string
	candidates func(pattern string, budget int) ([]string, error)
	flatten    func(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo
	filter     func([]LSPSymbolInfo, string) []LSPSymbolInfo
	format     func([]LSPSymbolInfo, string, int) string
}

func newLSPBaseClient(ctx context.Context, root string, serverName string, binary string, args []string, handleNotification lspNotificationHandler) (lspBaseClient, error) {
	process, err := commandexec.Start(ctx, commandexec.Spec{
		Name: binary,
		Args: append([]string(nil), args...),
		Dir:  root,
	})
	if err != nil {
		return lspBaseClient{}, err
	}

	return lspBaseClient{
		process:            process,
		workspaceRoot:      root,
		stdin:              process.Stdin(),
		stdout:             bufio.NewReader(process.Stdout()),
		serverName:         serverName,
		handleNotification: handleNotification,
	}, nil
}

func (c *lspBaseClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)

		_ = c.request(ctx, "shutdown", nil, nil)
		_ = c.notify("exit", map[string]any{})
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	c.forceClose()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
}

func (c *lspBaseClient) forceClose() {
	if c.process != nil {
		_ = c.process.Close()

		return
	}

	_ = c.stdin.Close()
	if c.stdout != nil {
		_, _ = io.Copy(io.Discard, c.stdout)
	}
}

func (c *lspBaseClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	return c.writeMessage(msg)
}

func (c *lspBaseClient) request(ctx context.Context, method string, params any, result any) error {
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
			var stderr string
			if c.process != nil {
				stderr, _ = c.process.Stderr()
			}
			if stderr = strings.TrimSpace(stderr); stderr != "" {
				return fmt.Errorf("%w: %s", err, stderr)
			}

			return err
		}

		var env LSPEnvelope

		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}

		if c.handleNotification != nil && env.Method != "" && len(env.ID) == 0 {
			c.handleNotification(env.Method, env.Params)

			continue
		}

		if len(env.ID) == 0 {
			continue
		}

		respID, err := lspParseResponseID(env.ID)
		if err != nil || respID != id {
			continue
		}

		if env.Error != nil {
			return failures.New(lspFailureCode(env.Error.Code), reasonLanguageToolLSPFailure, fmt.Sprintf("%s (code %d)", env.Error.Message, env.Error.Code))
		}

		if result != nil && len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, result); err != nil {
				return err
			}
		}

		return nil
	}
}

func (c *lspBaseClient) writeMessage(msg any) error {
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

func (c *lspBaseClient) readMessage() ([]byte, error) {
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

		if textutil.EqualTrimFold(parts[0], "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}

			contentLength = n
		}
	}

	if contentLength < 0 {
		return nil, failures.New(failures.Internal, reasonLanguageToolLSPFailure, fmt.Sprintf("missing Content-Length header from %s", c.serverName))
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return nil, err
	}

	return body, nil
}

func (c *lspBaseClient) openFile(path string, languageID string) error {
	content, err := safepath.ReadFileUnderRoot(c.workspaceRoot, path, "language server source", safepath.Options{})
	if err != nil {
		return err
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        LSPFileURI(path),
			"languageId": languageID,
			"version":    1,
			"text":       string(content),
		},
	}

	return c.notify("textDocument/didOpen", params)
}

func (m *Manager) lspLocationQuery(ctx context.Context, cfg lspLocationQueryConfig, sourcePath string, position func(string) map[string]any, method string, extraParams map[string]any, pathFilter string, limit int, includeLineText bool) (string, error) {
	client, err := cfg.newClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	abs, err := m.resolvePath(sourcePath, "")
	if err != nil {
		return "", err
	}

	if err := client.openFile(abs); err != nil {
		return "", err
	}

	params := map[string]any{
		"textDocument": map[string]any{"uri": cfg.fileURI(abs)},
		"position":     position(abs),
	}
	for key, value := range extraParams {
		params[key] = value
	}

	var raw json.RawMessage

	if err := client.request(ctx, method, params, &raw); err != nil {
		return "", err
	}

	locations, err := parseLSPLocations(raw, cfg.serverName)
	if err != nil {
		return "", err
	}

	return cfg.format(locations, pathFilter, limit, includeLineText), nil
}

func (m *Manager) lspWorkspaceSymbolQuery(ctx context.Context, cfg lspWorkspaceSymbolQueryConfig, pattern string, pathFilter string, limit int) (string, error) {
	client, err := cfg.newClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo

	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}

	if output := cfg.format(symbols, pathFilter, limit); strings.TrimSpace(output) != "" {
		return output, nil
	}

	candidates, err := cfg.candidates(pattern, max(limit*2, 6))
	if err != nil {
		return "", err
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
			"textDocument": map[string]any{"uri": cfg.fileURI(abs)},
		}, &docSymbols); err != nil {
			continue
		}

		collected = append(collected, cfg.flatten(abs, "", docSymbols)...)
	}

	return cfg.format(cfg.filter(collected, pattern), pathFilter, limit), nil
}

func prepareLSPCallHierarchy(ctx context.Context, client managedLSPClient, fileURI func(string) string, abs string, position map[string]any) ([]LSPCallHierarchyItem, error) {
	if err := client.openFile(abs); err != nil {
		return nil, err
	}

	params := map[string]any{
		"textDocument": map[string]any{"uri": fileURI(abs)},
		"position":     position,
	}

	var items []LSPCallHierarchyItem

	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return nil, err
	}

	return items, nil
}

func parseLSPLocations(raw json.RawMessage, serverName string) ([]LSPLocation, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var locations []LSPLocation

	if err := json.Unmarshal(trimmed, &locations); err == nil && validLSPLocationList(locations) {
		return locations, nil
	}

	var single LSPLocation

	if err := json.Unmarshal(trimmed, &single); err == nil && single.URI != "" {
		return []LSPLocation{single}, nil
	}

	var links []LSPLocationLink

	if err := json.Unmarshal(trimmed, &links); err == nil && validLSPLocationLinkList(links) {
		return locationsFromLSPLocationLinks(links), nil
	}

	var link LSPLocationLink

	if err := json.Unmarshal(trimmed, &link); err == nil && link.TargetURI != "" {
		return []LSPLocation{locationFromLSPLocationLink(link)}, nil
	}

	return nil, fmt.Errorf("unsupported %s location payload", serverName)
}

func validLSPLocationList(locations []LSPLocation) bool {
	for _, location := range locations {
		if location.URI == "" {
			return false
		}
	}

	return true
}

func validLSPLocationLinkList(links []LSPLocationLink) bool {
	for _, link := range links {
		if link.TargetURI == "" {
			return false
		}
	}

	return true
}

func locationsFromLSPLocationLinks(links []LSPLocationLink) []LSPLocation {
	locations := make([]LSPLocation, 0, len(links))
	for _, link := range links {
		locations = append(locations, locationFromLSPLocationLink(link))
	}

	return locations
}

func locationFromLSPLocationLink(link LSPLocationLink) LSPLocation {
	location := LSPLocation{URI: link.TargetURI, Range: link.TargetSelectionRange}
	if location.Range == (LSPRange{}) {
		location.Range = link.TargetRange
	}

	return location
}

func lspParseResponseID(raw json.RawMessage) (int, error) {
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

func LSPFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func LSPPathFromURI(uri string, serverName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}

	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported %s URI scheme %q", serverName, parsed.Scheme)
	}

	return filepath.Clean(parsed.Path), nil
}

type lspURIPathFunc func(string) (string, error)

type lspSymbolKindNameFunc func(int) string

type lspCallTarget struct {
	Path string
	Line int
	Name string
}

func (m *Manager) formatLSPLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool, pathFromURI lspURIPathFunc) string {
	type entry struct {
		path string
		line int
		col  int
		text string
	}

	entries := make([]entry, 0, len(locations))
	seen := make(map[string]struct{})

	for _, location := range locations {
		abs, err := pathFromURI(location.URI)
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

func (m *Manager) formatLSPWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int, pathFromURI lspURIPathFunc, kindName lspSymbolKindNameFunc, qualifierSeparator string) string {
	type entry struct {
		path          string
		line          int
		name          string
		containerName string
		kind          int
	}

	entries := make([]entry, 0, len(symbols))

	for _, symbol := range symbols {
		abs, err := pathFromURI(symbol.Location.URI)
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
		qualified := item.name
		if strings.TrimSpace(item.containerName) != "" {
			qualified = item.containerName + qualifierSeparator + item.name
		}

		lines = append(lines, fmt.Sprintf("%s:%d %s %s", m.handleDisplay(item.path), item.line, kindName(item.kind), qualified))
	}

	return strings.Join(lines, "\n")
}

func (m *Manager) formatLSPIncomingCalls(calls []LSPIncomingCall, pathFilter string, target lspCallTarget, pathFromURI lspURIPathFunc) callHierarchyResult {
	type entry struct {
		path string
		line int
		name string
	}

	entries := make([]entry, 0, len(calls))

	for _, call := range calls {
		abs, err := pathFromURI(call.From.URI)
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
			path: abs,
			line: call.From.SelectionRange.Start.Line + 1,
			name: call.From.Name,
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

	lines := make([]string, 0, len(entries))
	relationships := make([]ToolRelationship, 0, len(entries))
	targetAbs, _ := m.resolvePath(target.Path, "")

	for _, item := range entries {
		line := fmt.Sprintf("%s:%d %s", m.handleDisplay(item.path), item.line, item.name)

		lines = append(lines, line)

		if targetAbs != "" {
			relationships = append(relationships, m.callRelationshipFromAbs(item.path, item.line, item.name, targetAbs, target.Line, target.Name, line))
		}

		if len(lines) >= m.maxDirectoryEntries() {
			break
		}
	}

	return callHierarchyResult{Output: strings.Join(lines, "\n"), Relationships: relationships}
}

func (m *Manager) formatLSPOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string, pathFromURI lspURIPathFunc) callHierarchyResult {
	type entry struct {
		callsitePath string
		callsiteLine int
		targetPath   string
		targetLine   int
		name         string
	}

	entries := make([]entry, 0, len(calls))

	for _, call := range calls {
		targetAbs, err := pathFromURI(call.To.URI)
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

		if len(lines) >= m.maxDirectoryEntries() {
			break
		}
	}

	return callHierarchyResult{Output: strings.Join(lines, "\n"), Relationships: relationships}
}

func flattenLSPDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol, fileURI func(string) string, qualifierSeparator string) []LSPSymbolInfo {
	out := make([]LSPSymbolInfo, 0, len(symbols))
	for _, symbol := range symbols {
		currentContainer := strings.TrimSpace(container)

		out = append(out, LSPSymbolInfo{
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ContainerName: currentContainer,
			Location: LSPLocation{
				URI:   fileURI(path),
				Range: symbol.SelectionRange,
			},
		})

		nextContainer := symbol.Name

		if currentContainer != "" {
			nextContainer = currentContainer + qualifierSeparator + symbol.Name
		}

		out = append(out, flattenLSPDocumentSymbols(path, nextContainer, symbol.Children, fileURI, qualifierSeparator)...)
	}

	return out
}

func filterLSPDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
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
