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
	"sync"
	"time"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
)

type lspBaseClient struct {
	process             *commandexec.Process
	workspaceRoot       string
	stdin               io.WriteCloser
	stdout              *bufio.Reader
	nextID              int
	serverName          string
	handleNotification  lspNotificationHandler
	documentMu          sync.Mutex
	documents           map[string]*lspDocumentState
	watchInitialized    bool
	onWatchedFileChange func()
}

type lspDocumentState struct {
	version int
	content []byte
	exists  bool
	active  bool
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
	// Data is opaque server state that must round-trip into the
	// incomingCalls/outgoingCalls request; clangd keys the symbol on it and
	// answers an empty list without it.
	Data json.RawMessage `json:"data,omitempty"`
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
	prepare    func(context.Context, managedLSPClient, string) error
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
	return c.requestWithReader(ctx, method, params, result, func(context.Context) ([]byte, error) {
		return c.readMessage()
	})
}

func (c *lspBaseClient) requestWithin(ctx context.Context, method string, params any, result any) error {
	return c.requestWithReader(ctx, method, params, result, c.readMessageWithin)
}

func (c *lspBaseClient) requestWithReader(ctx context.Context, method string, params any, result any, read func(context.Context) ([]byte, error)) error {
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

		body, err := read(ctx)
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

		// A message with a method is a server notification or a server request.
		// Server requests carry their own ids (clangd starts at 0), so they must
		// never be matched against our pending request id.
		if env.Method != "" {
			if len(env.ID) > 0 && env.Method == "window/workDoneProgress/create" {
				if err := c.writeMessage(map[string]any{"jsonrpc": "2.0", "id": env.ID, "result": nil}); err != nil {
					return err
				}
			}

			if c.handleNotification != nil && len(env.ID) == 0 {
				c.handleNotification(env.Method, env.Params)
			}

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

// readMessageWithin reads one message but returns when ctx ends. The read
// goroutine stays blocked until the server writes or the process is closed.
func (c *lspBaseClient) readMessageWithin(ctx context.Context) ([]byte, error) {
	type readResult struct {
		body []byte
		err  error
	}

	done := make(chan readResult, 1)
	go func() {
		body, err := c.readMessage()
		done <- readResult{body: body, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.body, result.err
	}
}

func (c *lspBaseClient) openFile(path string, languageID string) error {
	content, err := safepath.ReadFileUnderRoot(c.workspaceRoot, path, "language server source", safepath.Options{})
	if err != nil {
		return err
	}

	path = filepath.Clean(path)
	c.documentMu.Lock()
	defer c.documentMu.Unlock()
	if c.documents == nil {
		c.documents = make(map[string]*lspDocumentState)
	}

	state := c.documents[path]
	if state == nil {
		state = &lspDocumentState{}
		c.documents[path] = state
	}
	if state.active {
		if bytes.Equal(state.content, content) {
			return nil
		}

		state.version++
		if err := c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": LSPFileURI(path), "version": state.version},
			"contentChanges": []map[string]any{{"text": string(content)}},
		}); err != nil {
			return err
		}
		state.content = append(state.content[:0], content...)

		return nil
	}

	state.version++
	if err := c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        LSPFileURI(path),
			"languageId": languageID,
			"version":    state.version,
			"text":       string(content),
		},
	}); err != nil {
		state.version--
		return err
	}
	state.content = append(state.content[:0], content...)
	state.exists = true
	state.active = true

	return nil
}

// syncWatchedFiles sends file-watch notifications for indexed files whose
// on-disk contents changed since the last request. The first call seeds the
// observations without emitting events; later calls report created, changed,
// and deleted paths so clangd can refresh its background index.
func (c *lspBaseClient) syncWatchedFiles(paths []string) ([]string, error) {
	c.documentMu.Lock()
	defer c.documentMu.Unlock()
	if c.documents == nil {
		c.documents = make(map[string]*lspDocumentState)
	}

	changedPaths := make([]string, 0)
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.workspaceRoot, path)
		}
		content, err := safepath.ReadFileUnderRoot(c.workspaceRoot, path, "language server watched file", safepath.Options{})
		exists := err == nil
		state := c.documents[path]
		if state == nil {
			state = &lspDocumentState{}
			c.documents[path] = state
		}
		if !c.watchInitialized {
			state.content = append(state.content[:0], content...)
			state.exists = exists
			continue
		}

		changeType := 2 // Changed
		if !state.exists && exists {
			changeType = 1 // Created
		} else if state.exists && !exists {
			changeType = 3 // Deleted
		} else if !exists || bytes.Equal(state.content, content) {
			continue
		}
		if err := c.notify("workspace/didChangeWatchedFiles", map[string]any{
			"changes": []map[string]any{{"uri": LSPFileURI(path), "type": changeType}},
		}); err != nil {
			return nil, err
		}
		changedPaths = append(changedPaths, path)
		if state.active && exists {
			state.version++
			if err := c.notify("textDocument/didChange", map[string]any{
				"textDocument":   map[string]any{"uri": LSPFileURI(path), "version": state.version},
				"contentChanges": []map[string]any{{"text": string(content)}},
			}); err != nil {
				return nil, err
			}
		}
		if changeType == 1 || changeType == 2 || changeType == 3 {
			// A watched-file event invalidates the cross-file readiness proof;
			// the next semantic query must establish it again.
			if c.onWatchedFileChange != nil {
				c.onWatchedFileChange()
			}
		}
		state.content = append(state.content[:0], content...)
		state.exists = exists
	}

	c.watchInitialized = true

	return changedPaths, nil
}

func (c *lspBaseClient) closeOpenFiles() error {
	c.documentMu.Lock()
	defer c.documentMu.Unlock()

	var firstErr error
	for path, state := range c.documents {
		if !state.active {
			continue
		}
		if err := c.notify("textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": LSPFileURI(path)},
		}); err != nil && firstErr == nil {
			firstErr = err
		}
		state.active = false
	}

	return firstErr
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
	if cfg.prepare != nil {
		if err := cfg.prepare(ctx, client, abs); err != nil {
			return "", err
		}
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

// LSPFileURI builds an RFC 8089 file URI. A Windows drive path must become
// file:///C:/dir/file; without the slash before the drive letter the drive is
// parsed as the URI authority and the language server resolves no document.
func LSPFileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if hasWindowsDrivePrefix(slashed) {
		slashed = "/" + slashed
	}

	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// LSPPathFromURI is the inverse of LSPFileURI. Language servers report Windows
// locations as file:///c%3A/dir/file, whose URI path "/c:/dir/file" is not a
// usable Windows path until the slash before the drive letter is removed.
func LSPPathFromURI(uri string, serverName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return "", err
	}

	if parsed.Scheme != "file" && parsed.Scheme != "" {
		return "", fmt.Errorf("unsupported %s URI scheme %q", serverName, parsed.Scheme)
	}

	uriPath := parsed.Path
	if trimmed := strings.TrimPrefix(uriPath, "/"); hasWindowsDrivePrefix(trimmed) {
		uriPath = trimmed
	}

	return filepath.Clean(filepath.FromSlash(uriPath)), nil
}

func hasWindowsDrivePrefix(slashed string) bool {
	if len(slashed) < 2 || slashed[1] != ':' {
		return false
	}

	drive := slashed[0]
	isLetter := drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z'

	return isLetter && (len(slashed) == 2 || slashed[2] == '/')
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
