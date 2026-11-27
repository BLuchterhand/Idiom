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
	"time"
)

const (
	// clangdBackgroundIndexBudget bounds the wait for clangd's background index.
	// Loading cached shards takes well under a second; a first full index of a
	// large tree can exceed it, and the query then reports clangd unavailable
	// instead of answering from the opened file alone.
	clangdBackgroundIndexBudget = 30 * time.Second

	clangdBackgroundIndexToken = "backgroundIndexProgress"
)

type clangdLSPClient struct {
	lspBaseClient

	// queryCtx is the context the client was created for. openFile has no
	// context parameter (managedLSPClient), but the index wait it performs must
	// stay inside the caller's budget.
	queryCtx context.Context

	// awaitIndex is set when a compile database exists: clangd then builds a
	// background index on the first didOpen, and cross-file references and
	// call hierarchy only exist once that index has loaded.
	awaitIndex bool
}

func (m *Manager) newClangdLSPClient(ctx context.Context) (*clangdLSPClient, error) {
	clangd := m.clangdBinary()
	if clangd == "" {
		return nil, fmt.Errorf("clangd is not installed")
	}

	args := []string{"--log=error"}
	compileDir := m.compileCommandsDir()
	if compileDir != "" {
		args = append(args, "--compile-commands-dir="+compileDir)
	}

	base, err := newLSPBaseClient(ctx, m.Root(), "clangd", clangd, args, nil)
	if err != nil {
		return nil, err
	}

	client := &clangdLSPClient{
		lspBaseClient: base,
		queryCtx:      ctx,
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

// openFile opens the document and, on the first open with a compile database,
// waits for clangd's background index. Querying before it loads answers only
// from the opened file, which silently drops every cross-file reference.
func (c *clangdLSPClient) openFile(path string) error {
	if err := c.lspBaseClient.openFile(path, clangdLanguageID(path)); err != nil {
		return err
	}

	if !c.awaitIndex {
		return nil
	}

	c.awaitIndex = false

	ctx, cancel := context.WithTimeout(c.queryCtx, clangdBackgroundIndexBudget)
	defer cancel()

	if err := c.awaitBackgroundIndex(ctx); err != nil {
		// A read may still be blocked on clangd's stdout; killing the process
		// releases it so no reader is left racing later requests.
		c.forceClose()

		return fmt.Errorf("clangd background index not ready, cross-file results unavailable: %w", err)
	}

	return nil
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
			return m.newClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		candidates: func(pattern string, budget int) ([]string, error) {
			index, err := m.getCppIndex(ctx)
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
			return m.newClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatClangdLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.cppLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.maxDirectoryEntries(), false)
}

func (m *Manager) cppReferencesViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "clangd",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newClangdLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatClangdLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.cppLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.maxDirectoryEntries(), true)
}

func (m *Manager) cppCallHierarchyViaClangd(ctx context.Context, match semanticcpp.CppSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newClangdLSPClient(ctx)
	if err != nil {
		return callHierarchyResult{}, err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return callHierarchyResult{}, err
	}

	items, err := prepareLSPCallHierarchy(ctx, client, LSPFileURI, abs, m.cppLSPPosition(abs, match))
	if err != nil {
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

					if fallbackItems, prepErr := prepareLSPCallHierarchy(ctx, client, LSPFileURI, targetAbs, map[string]any{
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
