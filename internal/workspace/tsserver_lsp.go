package workspace

// typescript-language-server (tsserver-lsp) client. Mirrors
// clangd_lsp.go: JSON-RPC over stdio, bare-bones LSP lifecycle, just the
// surface area needed by ts_tools.go — workspace/symbol,
// textDocument/documentSymbol, textDocument/definition,
// textDocument/references, textDocument/implementation, call hierarchy,
// and textDocument/publishDiagnostics.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"idiom/internal/semantic"
)

type tsLSPClient struct {
	lspBaseClient
	pendingDiagnostics map[string][]tsDiagnostic
}

type tsDiagnostic struct {
	Range    LSPRange `json:"range"`
	Severity int      `json:"severity"`
	Code     any      `json:"code"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
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
	client := &tsLSPClient{pendingDiagnostics: make(map[string][]tsDiagnostic)}
	client.lspBaseClient = lspBaseClient{
		serverName:         "typescript-language-server",
		cmd:                cmd,
		stdin:              stdin,
		stdout:             bufio.NewReader(stdout),
		handleNotification: client.handleNotification,
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

func (c *tsLSPClient) handleNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" || len(params) == 0 {
		return
	}
	var payload tsPublishDiagnosticsParams
	if err := json.Unmarshal(params, &payload); err == nil {
		c.pendingDiagnostics[payload.URI] = payload.Diagnostics
	}
}

func (c *tsLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, tsLanguageID(path))
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
	return LSPFileURI(path)
}

func tsPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "ts LSP")
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
	return m.lspWorkspaceSymbolQuery(ctx, lspWorkspaceSymbolQueryConfig{
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newTsLSPClient(ctx)
		},
		fileURI: tsFileURI,
		candidates: func(pattern string, budget int) ([]string, error) {
			index, err := m.getTsIndex()
			if err != nil {
				return nil, err
			}
			candidates := index.RelevantFiles(strings.Fields(pattern), budget)
			if len(candidates) == 0 {
				candidates = index.DefaultFiles(budget)
			}
			return candidates, nil
		},
		flatten: flattenTsDocumentSymbols,
		filter:  filterTsDocumentSymbolMatches,
		format:  m.formatTsWorkspaceSymbols,
	}, pattern, pathFilter, limit)
}

func (m *Manager) tsDefinitionViaLSP(ctx context.Context, match semantic.TsSymbolMatch) (string, error) {
	return m.LSPLocationQueryViaLSP(ctx, match, "textDocument/definition")
}

func (m *Manager) LSPLocationQueryViaLSP(ctx context.Context, match semantic.TsSymbolMatch, method string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "ts LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newTsLSPClient(ctx)
		},
		fileURI: tsFileURI,
		format:  m.formatTsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.tsLSPPosition(abs, match)
	}, method, nil, "", 4, false)
}

func (m *Manager) tsImplementationsViaLSP(ctx context.Context, match semantic.TsSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "ts LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newTsLSPClient(ctx)
		},
		fileURI: tsFileURI,
		format:  m.formatTsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.tsLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.cfg.MaxDirectoryEntries, false)
}

func (m *Manager) tsReferencesViaLSP(ctx context.Context, match semantic.TsSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "ts LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newTsLSPClient(ctx)
		},
		fileURI: tsFileURI,
		format:  m.formatTsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.tsLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.cfg.MaxDirectoryEntries, true)
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
	items, err := prepareLSPCallHierarchy(ctx, client, tsFileURI, abs, m.tsLSPPosition(abs, match))
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

func (m *Manager) formatTsLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	return m.formatLSPLocations(locations, pathFilter, limit, includeLineText, tsPathFromURI)
}

func (m *Manager) formatTsWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	return m.formatLSPWorkspaceSymbols(symbols, pathFilter, limit, tsPathFromURI, tsSymbolKindName, ".")
}

func (m *Manager) formatTsIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.TsSymbolMatch) callHierarchyResult {
	return m.formatLSPIncomingCalls(calls, pathFilter, lspCallTarget{Path: target.Path, Line: target.Line, Name: target.Name}, tsPathFromURI)
}

func (m *Manager) formatTsOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	return m.formatLSPOutgoingCalls(callerPath, callerName, calls, pathFilter, tsPathFromURI)
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
	return flattenLSPDocumentSymbols(path, container, symbols, tsFileURI, ".")
}

func filterTsDocumentSymbolMatches(symbols []LSPSymbolInfo, pattern string) []LSPSymbolInfo {
	return filterLSPDocumentSymbolMatches(symbols, pattern)
}
