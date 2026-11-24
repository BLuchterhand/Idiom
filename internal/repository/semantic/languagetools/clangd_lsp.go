package languagetools

import (
	"context"
	"encoding/json"
	"fmt"
	semanticcpp "idiom/internal/repository/semantic/cpp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	clangdIdleTimeout   = 30 * time.Second
	clangdStartupBudget = 15 * time.Second
	// clangdIndexProbeBudget bounds the semantic readiness probe. It does not
	// start another index; it waits for the persistent server to expose the
	// requested symbol in the requested file.
	clangdIndexProbeBudget = 15 * time.Second

	clangdBackgroundIndexToken = "backgroundIndexProgress"
)

type clangdLSPClient struct {
	lspBaseClient
	queryCtx context.Context // retained for protocol test clients; query budgets use call contexts

	// awaitIndex is set when a compile database exists. A progress end event is
	// not sufficient readiness evidence: clangd can emit it before all shards
	// needed by a cross-file query are visible.
	awaitIndex bool
}

type clangdLSPLease struct {
	client  *clangdLSPClient
	manager *Manager
	once    sync.Once
}

func (l *clangdLSPLease) close() {
	if l == nil {
		return
	}

	l.once.Do(func() {
		if l.manager != nil && l.client != nil {
			l.manager.releaseClangdLSPClient(l.client)
		}
	})
}

func (l *clangdLSPLease) openFile(path string) error {
	return l.client.openFile(path)
}

func (l *clangdLSPLease) request(ctx context.Context, method string, params any, result any) error {
	return l.client.request(ctx, method, params, result)
}

func (l *clangdLSPLease) awaitIndexFor(ctx context.Context, path string, symbol string) error {
	return l.client.awaitIndexFor(ctx, path, symbol)
}

func (m *Manager) newClangdLSPClient(ctx context.Context) (*clangdLSPClient, error) {
	return m.newClangdLSPClientWithContexts(ctx, ctx)
}

func (m *Manager) newClangdLSPClientWithContexts(processCtx context.Context, requestCtx context.Context) (*clangdLSPClient, error) {
	clangd := m.clangdBinary()
	if clangd == "" {
		return nil, fmt.Errorf("clangd is not installed")
	}

	args := []string{"--log=error", "--background-index"}
	compileDir := m.compileCommandsDir()
	if compileDir != "" {
		args = append(args, "--compile-commands-dir="+compileDir)
	}

	base, err := newLSPBaseClient(processCtx, m.Root(), "clangd", clangd, args, nil)
	if err != nil {
		return nil, err
	}

	client := &clangdLSPClient{
		lspBaseClient: base,
		queryCtx:      requestCtx,
		awaitIndex:    compileDir != "",
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   LSPFileURI(m.Root()),
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8"},
			},
			// Without workDoneProgress clangd never reports when its
			// background index is ready.
			"window": map[string]any{
				"workDoneProgress": true,
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  LSPFileURI(m.Root()),
			"name": filepath.Base(m.Root()),
		}},
		"initializationOptions": map[string]any{
			"fallbackFlags": []string{"-xc++", "-std=c++20"},
		},
	}

	var initResult map[string]any

	if err := client.request(requestCtx, "initialize", params, &initResult); err != nil {
		client.forceClose()

		return nil, fmt.Errorf("clangd initialize failed: %w", err)
	}

	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()

		return nil, fmt.Errorf("clangd initialized notification failed: %w", err)
	}

	return client, nil
}

func (m *Manager) acquireClangdLSPClient(ctx context.Context) (*clangdLSPLease, error) {
	m.clangdMu.Lock()
	if m.clangd != nil && m.clangd.process != nil && m.clangd.process.Alive() {
		if m.clangdIdleTimer != nil {
			m.clangdIdleTimer.Stop()
			m.clangdIdleTimer = nil
		}
		m.clangdGeneration++

		return &clangdLSPLease{client: m.clangd, manager: m}, nil
	}

	if m.clangd != nil {
		m.clangd = nil
	}
	if m.clangdIdleTimer != nil {
		m.clangdIdleTimer.Stop()
		m.clangdIdleTimer = nil
	}

	startupCtx, cancel := context.WithTimeout(ctx, clangdStartupBudget)
	defer cancel()
	factory := m.clangdClientFactory
	var client *clangdLSPClient
	var err error
	if factory != nil {
		client, err = factory(context.Background(), startupCtx)
	} else {
		client, err = m.newClangdLSPClientWithContexts(context.Background(), startupCtx)
	}
	if err != nil {
		m.clangdMu.Unlock()

		return nil, err
	}

	m.clangd = client
	m.clangdGeneration++

	return &clangdLSPLease{client: client, manager: m}, nil
}

func (m *Manager) releaseClangdLSPClient(client *clangdLSPClient) {
	if m == nil || client == nil {
		return
	}

	if m.clangd != client {
		m.clangdMu.Unlock()
		return
	}

	if client.process == nil || !client.process.Alive() {
		m.clangd = nil
		m.clangdGeneration++
		m.clangdMu.Unlock()
		client.forceClose()

		return
	}

	m.clangdGeneration++
	generation := m.clangdGeneration
	if m.clangdIdleTimer != nil {
		m.clangdIdleTimer.Stop()
	}
	m.clangdIdleTimer = time.AfterFunc(m.clangdIdleTimeout, func() {
		m.shutdownIdleClangd(client, generation)
	})
	m.clangdMu.Unlock()
}

func (m *Manager) shutdownIdleClangd(client *clangdLSPClient, generation uint64) {
	m.clangdMu.Lock()
	if m.clangd != client || m.clangdGeneration != generation {
		m.clangdMu.Unlock()

		return
	}
	m.clangd = nil
	m.clangdIdleTimer = nil
	m.clangdGeneration++
	m.clangdMu.Unlock()

	client.close()
}

// openFile opens the document. Readiness is checked by awaitIndexFor once the
// caller supplies the symbol being queried; didOpen alone has no useful
// cross-file readiness predicate.
func (c *clangdLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, clangdLanguageID(path))
}

// awaitIndexFor waits for a semantic workspace/symbol probe followed by a
// cross-file references probe. Finding the symbol in the file just opened is
// not readiness evidence: clangd's dynamic index always contains that file.
// A second location proves that the background index has exposed a using file.
func (c *clangdLSPClient) awaitIndexFor(ctx context.Context, targetPath string, symbol string) error {
	if !c.awaitIndex {
		return nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, clangdIndexProbeBudget)
	defer cancel()

	for {
		var symbols []LSPSymbolInfo
		if err := c.requestWithin(probeCtx, "workspace/symbol", map[string]any{"query": symbol}, &symbols); err != nil {
			c.forceClose()

			return fmt.Errorf("clangd readiness probe failed: %w", err)
		}

		for _, candidate := range symbols {
			path, err := clangdPathFromURI(candidate.Location.URI)
			if err != nil || filepath.Clean(path) != filepath.Clean(targetPath) {
				continue
			}
			if candidate.Name == symbol || strings.TrimSuffix(candidate.Name, "()") == symbol || strings.HasSuffix(candidate.Name, "::"+symbol) {
				var raw json.RawMessage
				if err := c.requestWithin(probeCtx, "textDocument/references", map[string]any{
					"textDocument": map[string]any{"uri": LSPFileURI(targetPath)},
					"position":     candidate.Location.Range.Start,
					"context":      map[string]any{"includeDeclaration": true},
				}, &raw); err != nil {
					c.forceClose()

					return fmt.Errorf("clangd cross-file readiness probe failed: %w", err)
				}

				locations, err := parseClangdLocations(raw)
				if err != nil {
					return fmt.Errorf("clangd cross-file readiness probe returned invalid locations: %w", err)
				}
				for _, location := range locations {
					path, err := clangdPathFromURI(location.URI)
					if err != nil || filepath.Clean(path) == filepath.Clean(targetPath) || !clangdPathWithinRoot(c.workspaceRoot, path) {
						continue
					}

					c.awaitIndex = false

					return nil
				}
			}
		}

		select {
		case <-probeCtx.Done():
			return fmt.Errorf("clangd cross-file semantic index did not expose a using file for %s in %s: %w", symbol, targetPath, probeCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func clangdPathWithinRoot(root string, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c *clangdLSPClient) awaitBackgroundIndex(ctx context.Context) error {
	for {
		body, err := c.readMessageWithin(ctx)
		if err != nil {
			return err
		}

		done, err := c.handleBackgroundIndexMessage(body)
		if err != nil || done {
			return err
		}
	}
}

// handleBackgroundIndexMessage acknowledges clangd's progress-token request and
// reports whether the message ends background indexing.
func (c *clangdLSPClient) handleBackgroundIndexMessage(body []byte) (bool, error) {
	var env LSPEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return false, nil
	}

	switch env.Method {
	case "window/workDoneProgress/create":
		// clangd sends no progress for the token until the client answers.
		return false, c.writeMessage(map[string]any{"jsonrpc": "2.0", "id": env.ID, "result": nil})
	case "$/progress":
		var progress struct {
			Token string `json:"token"`
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if err := json.Unmarshal(env.Params, &progress); err != nil {
			return false, nil
		}

		return progress.Token == clangdBackgroundIndexToken && progress.Value.Kind == "end", nil
	default:
		return false, nil
	}
}

func clangdPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "clangd")
}

func clangdLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c":
		return "c"
	default:
		return "cpp"
	}
}

func (m *Manager) cppWorkspaceSymbolsViaClangd(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	return m.lspWorkspaceSymbolQuery(ctx, lspWorkspaceSymbolQueryConfig{
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.acquireClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		candidates: func(pattern string, budget int) ([]string, error) {
			index, err := m.getCppIndex()
			if err != nil {
				return nil, err
			}

			candidates := index.RelevantFiles(strings.Fields(pattern), budget)
			if len(candidates) == 0 {
				candidates = index.DefaultFiles(budget)
			}

			return candidates, nil
		},
		flatten: flattenDocumentSymbols,
		filter:  filterLSPDocumentSymbolMatches,
		format:  m.formatClangdWorkspaceSymbols,
	}, pattern, pathFilter, limit)
}

func (m *Manager) cppDefinitionViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch) (string, error) {
	return m.cppLocationQueryViaClangd(ctx, match, "textDocument/definition", true)
}

func (m *Manager) cppDeclarationViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch) (string, error) {
	return m.cppLocationQueryViaClangd(ctx, match, "textDocument/declaration", false)
}

func (m *Manager) cppLocationQueryViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, method string, declarationFallback bool) (string, error) {
	client, err := m.acquireClangdLSPClient(ctx)
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
		"textDocument": map[string]any{"uri": LSPFileURI(abs)},
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

func (m *Manager) cppImplementationsViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "clangd",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.acquireClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		prepare: func(ctx context.Context, client managedLSPClient, abs string) error {
			return client.(*clangdLSPLease).awaitIndexFor(ctx, abs, match.Name)
		},
		format: m.formatClangdLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.cppLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.maxDirectoryEntries(), false)
}

func (m *Manager) cppReferencesViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "clangd",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.acquireClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		prepare: func(ctx context.Context, client managedLSPClient, abs string) error {
			return client.(*clangdLSPLease).awaitIndexFor(ctx, abs, match.Name)
		},
		format: m.formatClangdLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.cppLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.maxDirectoryEntries(), true)
}

func (m *Manager) cppCallHierarchyViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.acquireClangdLSPClient(ctx)
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
	if err := client.awaitIndexFor(ctx, abs, match.Name); err != nil {
		return callHierarchyResult{}, err
	}

	params := map[string]any{
		"textDocument": map[string]any{"uri": LSPFileURI(abs)},
		"position":     m.cppLSPPosition(abs, match),
	}
	var items []LSPCallHierarchyItem
	if err := client.request(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil {
		return callHierarchyResult{}, err
	}

	if len(items) == 0 {
		params := map[string]any{
			"textDocument": map[string]any{"uri": LSPFileURI(abs)},
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

					if !m.pathWithinWorkspace(targetAbs) {
						continue
					}

					if err := client.openFile(targetAbs); err != nil {
						continue
					}
					if err := client.awaitIndexFor(ctx, targetAbs, match.Name); err != nil {
						continue
					}
					var fallbackItems []LSPCallHierarchyItem
					if prepErr := client.request(ctx, "textDocument/prepareCallHierarchy", map[string]any{
						"textDocument": map[string]any{"uri": LSPFileURI(targetAbs)},
						"position": map[string]any{
							"line":      location.Range.Start.Line,
							"character": location.Range.Start.Character,
						},
					}, &fallbackItems); prepErr == nil && len(fallbackItems) > 0 {
						items = fallbackItems
						break
					}
				}
			}
		}
	}

	if len(items) == 0 {
		return callHierarchyResult{}, nil
	}

	if incoming {
		var calls []LSPIncomingCall

		if err := client.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
			return callHierarchyResult{}, err
		}

		return m.formatClangdIncomingCalls(calls, pathFilter, match), nil
	}

	var calls []LSPOutgoingCall

	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}

	return m.formatClangdOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) cppLSPPosition(abs string, match semanticcpp.CppSymbolMatch) map[string]any {
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

func (m *Manager) cppLSPCharacter(abs string, match semanticcpp.CppSymbolMatch) (int, bool) {
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

func parseClangdLocations(raw json.RawMessage) ([]LSPLocation, error) {
	return parseLSPLocations(raw, "clangd")
}

func (m *Manager) formatClangdLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	return m.formatLSPLocations(locations, pathFilter, limit, includeLineText, clangdPathFromURI)
}

func (m *Manager) formatClangdWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	return m.formatLSPWorkspaceSymbols(symbols, pathFilter, limit, clangdPathFromURI, clangdSymbolKindName, "::")
}

func (m *Manager) formatClangdIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semanticcpp.CppSymbolMatch) callHierarchyResult {
	return m.formatLSPIncomingCalls(calls, pathFilter, lspCallTarget{Path: target.Path, Line: target.Line, Name: target.Name}, clangdPathFromURI)
}

func (m *Manager) formatClangdOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	return m.formatLSPOutgoingCalls(callerPath, callerName, calls, pathFilter, clangdPathFromURI)
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

func flattenDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	return flattenLSPDocumentSymbols(path, container, symbols, LSPFileURI, "::")
}
