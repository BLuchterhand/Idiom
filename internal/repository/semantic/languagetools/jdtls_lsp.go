package languagetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/hosttools"
	semanticjava "idiom/internal/repository/semantic/java"
)

type jdtlsLSPClient struct {
	lspBaseClient
}

// jdtlsInitializeTimeout is generous: jdtls commonly spends 5–10s on
// project import for a pom.xml / build.gradle workspace.
const jdtlsInitializeTimeout = 30 * time.Second

func (m *Manager) jdtlsBinary() string {
	return hosttools.FirstAvailable("jdtls")
}

func (m *Manager) newJdtlsLSPClient(ctx context.Context) (*jdtlsLSPClient, error) {
	jdtls := m.jdtlsBinary()
	if jdtls == "" {
		return nil, fmt.Errorf("jdtls is not installed")
	}

	base, err := newLSPBaseClient(ctx, m.Root(), "jdtls", jdtls, nil, nil)
	if err != nil {
		return nil, err
	}

	client := &jdtlsLSPClient{
		lspBaseClient: base,
	}

	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   LSPFileURI(m.Root()),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"callHierarchy": map[string]any{
					"dynamicRegistration": false,
				},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
		"workspaceFolders": []map[string]any{{
			"uri":  LSPFileURI(m.Root()),
			"name": filepath.Base(m.Root()),
		}},
		"initializationOptions": map[string]any{
			// jdtls accepts arbitrary keys here; none are required.
		},
	}
	initCtx, cancel := context.WithTimeout(ctx, jdtlsInitializeTimeout)

	defer cancel()

	var initResult map[string]any

	if err := client.request(initCtx, "initialize", params, &initResult); err != nil {
		client.forceClose()

		return nil, fmt.Errorf("jdtls initialize failed: %w", err)
	}

	if err := client.notify("initialized", map[string]any{}); err != nil {
		client.forceClose()

		return nil, fmt.Errorf("jdtls initialized notification failed: %w", err)
	}

	// Readiness probe: jdtls may still be importing projects when it
	// acknowledges `initialize`. We give it a short settle window
	// before returning to the caller. A real workspace/symbol probe
	// loop would be ideal; a small sleep is pragmatic given the
	// in-process tool-call timing budget.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
	}

	return client, nil
}

func (c *jdtlsLSPClient) openFile(path string) error {
	return c.lspBaseClient.openFile(path, "java")
}

func jdtlsPathFromURI(uri string) (string, error) {
	return LSPPathFromURI(uri, "jdtls")
}

func (m *Manager) javaLSPPosition(abs string, match semanticjava.JavaSymbolMatch) map[string]any {
	line := match.Line - 1
	if line < 0 {
		line = 0
	}

	character := match.Column - 1
	if character < 0 {
		character = 0
	}

	if resolved, ok := m.javaLSPCharacter(abs, match); ok {
		character = resolved
	}

	return map[string]any{
		"line":      line,
		"character": character,
	}
}

func (m *Manager) javaLSPCharacter(abs string, match semanticjava.JavaSymbolMatch) (int, bool) {
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

func (m *Manager) formatJdtlsLocations(locations []LSPLocation, pathFilter string, limit int, includeLineText bool) string {
	return m.formatLSPLocations(locations, pathFilter, limit, includeLineText, jdtlsPathFromURI)
}

func (m *Manager) formatJdtlsWorkspaceSymbols(symbols []LSPSymbolInfo, pathFilter string, limit int) string {
	return m.formatLSPWorkspaceSymbols(symbols, pathFilter, limit, jdtlsPathFromURI, jdtlsSymbolKindName, ".")
}

func (m *Manager) formatJdtlsIncomingCalls(calls []LSPIncomingCall, pathFilter string, target semanticjava.JavaSymbolMatch) callHierarchyResult {
	return m.formatLSPIncomingCalls(calls, pathFilter, lspCallTarget{Path: target.Path, Line: target.Line, Name: target.Name}, jdtlsPathFromURI)
}

func (m *Manager) formatJdtlsOutgoingCalls(callerPath string, callerName string, calls []LSPOutgoingCall, pathFilter string) callHierarchyResult {
	return m.formatLSPOutgoingCalls(callerPath, callerName, calls, pathFilter, jdtlsPathFromURI)
}

func jdtlsSymbolKindName(kind int) string {
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
	default:
		return "symbol"
	}
}

func flattenJdtlsDocumentSymbols(path string, container string, symbols []LSPDocumentSymbol) []LSPSymbolInfo {
	return flattenLSPDocumentSymbols(path, container, symbols, LSPFileURI, ".")
}

// javaWorkspaceSymbolsViaJdtls performs a workspace/symbol query,
// falling back to per-file documentSymbol scans if the workspace query
// returns nothing (which can happen before jdtls finishes indexing).
func (m *Manager) javaWorkspaceSymbolsViaJdtls(ctx context.Context, pattern string, pathFilter string, limit int) (string, error) {
	return m.lspWorkspaceSymbolQuery(ctx, lspWorkspaceSymbolQueryConfig{
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newJdtlsLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		candidates: func(pattern string, budget int) ([]string, error) {
			index, err := m.getJavaIndex()
			if err != nil {
				return nil, err
			}

			candidates := index.RelevantFiles(strings.Fields(pattern), budget)
			if len(candidates) == 0 {
				candidates = index.DefaultFiles(budget)
			}

			return candidates, nil
		},
		flatten: flattenJdtlsDocumentSymbols,
		filter:  filterLSPDocumentSymbolMatches,
		format:  m.formatJdtlsWorkspaceSymbols,
	}, pattern, pathFilter, limit)
}

func (m *Manager) javaDefinitionViaJdtls(ctx context.Context, match semanticjava.JavaSymbolMatch) (string, error) {
	return m.javaLocationQueryViaJdtls(ctx, match, "textDocument/definition")
}

func (m *Manager) javaLocationQueryViaJdtls(ctx context.Context, match semanticjava.JavaSymbolMatch, method string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "jdtls",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newJdtlsLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatJdtlsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.javaLSPPosition(abs, match)
	}, method, nil, "", 4, false)
}

func (m *Manager) javaImplementationsViaJdtls(ctx context.Context, match semanticjava.JavaSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "jdtls",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newJdtlsLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatJdtlsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.javaLSPPosition(abs, match)
	}, "textDocument/implementation", nil, pathFilter, m.maxDirectoryEntries(), false)
}

func (m *Manager) javaReferencesViaJdtls(ctx context.Context, match semanticjava.JavaSymbolMatch, pathFilter string) (string, error) {
	return m.lspLocationQuery(ctx, lspLocationQueryConfig{
		serverName: "jdtls",
		newClient: func(ctx context.Context) (managedLSPClient, error) {
			return m.newJdtlsLSPClient(ctx)
		},
		fileURI: LSPFileURI,
		format:  m.formatJdtlsLocations,
	}, match.Path, func(abs string) map[string]any {
		return m.javaLSPPosition(abs, match)
	}, "textDocument/references", map[string]any{
		"context": map[string]any{"includeDeclaration": true},
	}, pathFilter, m.maxDirectoryEntries(), true)
}

func (m *Manager) javaCallHierarchyViaJdtls(ctx context.Context, match semanticjava.JavaSymbolMatch, pathFilter string, incoming bool) (callHierarchyResult, error) {
	client, err := m.newJdtlsLSPClient(ctx)
	if err != nil {
		return callHierarchyResult{}, err
	}
	defer client.close()

	abs, err := m.resolvePath(match.Path, "")
	if err != nil {
		return callHierarchyResult{}, err
	}

	items, err := prepareLSPCallHierarchy(ctx, client, LSPFileURI, abs, m.javaLSPPosition(abs, match))
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

		return m.formatJdtlsIncomingCalls(calls, pathFilter, match), nil
	}

	var calls []LSPOutgoingCall

	if err := client.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &calls); err != nil {
		return callHierarchyResult{}, err
	}

	return m.formatJdtlsOutgoingCalls(abs, match.Name, calls, pathFilter), nil
}
