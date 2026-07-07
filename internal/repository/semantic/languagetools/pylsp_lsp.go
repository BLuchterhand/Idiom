package languagetools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/hosttools"
	"idiom/internal/repository/semantic"
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
	lspBaseClient
	server string // "pyright" or "pylsp"
}

type pylspDiagnostic struct {
	Range    LSPRange `json:"range"`
	Severity int      `json:"severity"`
	Code     any      `json:"code"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

// pythonLSPBinary resolves the preferred LSP server binary. Returns
// ("", "") when neither pyright nor pylsp is on PATH.
func (m *Manager) pythonLSPBinary() (path string, server string) {
	if p := hosttools.FirstAvailable("pyright-langserver"); p != "" {
		return p, "pyright"
	}

	if p := hosttools.FirstAvailable("pylsp"); p != "" {
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
		return nil, fmt.Errorf("%w: %s", ErrPythonLSPUnavailable, pythonLSPInstallHint())
	}

	var args []string

	switch server {
	case "pyright":
		args = []string{"--stdio"}
	case "pylsp":
		// pylsp defaults to stdio; no extra args required.
	}

	cmd := exec.CommandContext(ctx, binary, args...)

	cmd.Dir = m.Root()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	client := &pylspLSPClient{server: server}

	client.lspBaseClient = lspBaseClient{
		serverName: "Python LSP",
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
	}
	cmd.Stderr = &client.stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   LSPFileURI(m.Root()),
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"symbol": map[string]any{
					"dynamicRegistration": false,
				},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  LSPFileURI(m.Root()),
			"name": filepath.Base(m.Root()),
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

func (c *pylspLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, "python")
}

func pylspPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "Python LSP")
}

func (m *Manager) pythonWorkspaceSymbolsViaLSP(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	return m.lspWorkspaceSymbolQuery(ctx, lspWorkspaceSymbolQueryConfig{
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		candidates: func(pattern string, budget int) ([]string, error) {
			index, err := m.getPythonIndex()
			if err != nil {
				return nil, err
			}

			candidates := index.RelevantFiles(strings.Fields(pattern), budget)
			if len(candidates) == 0 {
				candidates = index.DefaultFiles(budget)
			}

			return candidates, nil
		},
		flatten: flattenPylspDocumentSymbols,
		filter:  filterLSPDocumentSymbolMatches,
		format:  m.formatPylspWorkspaceSymbols,
	}, pattern, pathFilter, limit)
}

func (m *Manager) pythonDefinitionViaLSP(ctx context.Context, match semantic.PythonSymbolMatch) (string, error) {
	return m.pythonLocationQueryViaLSP(ctx, match, "textDocument/definition")
}

func (m *Manager) pythonLocationQueryViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, method string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, method, nil, "", 4, false)
}

func (m *Manager) pythonImplementationsViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.maxDirectoryEntries(), false)
}

func (m *Manager) pythonReferencesViaLSP(ctx context.Context, match semantic.PythonSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "Python LSP",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newPylspLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatPylspLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.pythonLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.maxDirectoryEntries(), true)
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

	items, err := prepareLSPCallHierarchy(ctx, client, LSPFileURI, abs, m.pythonLSPPosition(abs, match))
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

		return m.formatPylspIncomingCalls(calls, pathFilter, match), nil
	}

	var calls []LSPOutgoingCall

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

		var env LSPEnvelope

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

	var (
		body    []byte
		readErr error
	)

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

func (m *Manager) formatPylspLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	return m.formatLSPLocations(locations, pathFilter, limit, includeLineText, pylspPathFromURI)
}

func (m *Manager) formatPylspWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	return m.formatLSPWorkspaceSymbols(symbols, pathFilter, limit, pylspPathFromURI, pylspSymbolKindName, ".")
}

func (m *Manager) formatPylspIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.PythonSymbolMatch) callHierarchyResult {
	return m.formatLSPIncomingCalls(calls, pathFilter, lspCallTarget{Path: target.Path, Line: target.Line, Name: target.Name}, pylspPathFromURI)
}

func (m *Manager) formatPylspOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	return m.formatLSPOutgoingCalls(callerPath, callerName, calls, pathFilter, pylspPathFromURI)
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

func flattenPylspDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	return flattenLSPDocumentSymbols(path, container, symbols, LSPFileURI, ".")
}
