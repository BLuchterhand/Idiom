package languagetools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/repository/semantic"
)

type rustAnalyzerLSPClient struct {
	lspBaseClient
}

func (m *Manager) rustAnalyzerBinary() string {
	path, _ := exec.LookPath("rust-analyzer")
	return path
}

func (m *Manager) newRustAnalyzerLSPClient(ctx context.Context) (*rustAnalyzerLSPClient, error) {
	ra := m.rustAnalyzerBinary()
	if ra == "" {
		return nil, fmt.Errorf("rust-analyzer is not installed")
	}

	cmd := exec.CommandContext(ctx, ra)

	cmd.Dir = m.Root()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	client := &rustAnalyzerLSPClient{}

	client.lspBaseClient = lspBaseClient{
		serverName: "rust-analyzer",
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
			"general": map[string]any{
				"positionEncodings": []string{"utf-8"},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  LSPFileURI(m.Root()),
			"name": filepath.Base(m.Root()),
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

func (c *rustAnalyzerLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, "rust")
}

func raPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "rust-analyzer")
}

// -- Capability wrappers ---------------------------------------------------

func (m *Manager) rustWorkspaceSymbolsViaRustAnalyzer(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.close()

	var symbols []LSPSymbolInfo

	if err := client.request(ctx, "workspace/symbol", map[string]any{"query": pattern}, &symbols); err != nil {
		return "", err
	}

	return m.formatRAWorkspaceSymbols(symbols, pathFilter, limit), nil
}

func (m *Manager) rustDefinitionViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/definition", nil, "", 4, false)
}

func (m *Manager) rustReferencesViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.maxDirectoryEntries(), true)
}

func (m *Manager) rustImplementationsViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "rust-analyzer",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newRustAnalyzerLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatRALocations,
	}, match.Path, func(abs string) map[string]any {
		return m.rustLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.maxDirectoryEntries(), false)
}

func (m *Manager) rustCallHierarchyViaRustAnalyzer(ctx context.Context, match semantic.RustSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newRustAnalyzerLSPClient(ctx)
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

	items, err := m.rustAnalyzerPrepareCallHierarchy(ctx, client, abs, m.rustLSPPosition(abs, match))
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

		return m.formatRAIncomingCalls(calls, pathFilter, match), nil
	}

	var calls []LSPOutgoingCall

	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}

	return m.formatRAOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}

func (m *Manager) rustAnalyzerPrepareCallHierarchy(ctx context.Context, client *rustAnalyzerLSPClient, abs string, position map[string]any) ([]LSPCallHierarchyItem, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": LSPFileURI(abs)},
		"position":     position,
	}

	var items []LSPCallHierarchyItem

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

func (m *Manager) formatRALocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	return m.formatLSPLocations(locations, pathFilter, limit, includeLineText, raPathFromURI)
}

func (m *Manager) formatRAWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	return m.formatLSPWorkspaceSymbols(symbols, pathFilter, limit, raPathFromURI, raSymbolKindName, "::")
}

func (m *Manager) formatRAIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semantic.RustSymbolMatch) callHierarchyResult {
	return m.formatLSPIncomingCalls(calls, pathFilter, lspCallTarget{Path: target.Path, Line: target.Line, Name: target.Name}, raPathFromURI)
}

func (m *Manager) formatRAOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	return m.formatLSPOutgoingCalls(callerPath, callerName, calls, pathFilter, raPathFromURI)
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
