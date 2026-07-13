package languagetools

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) Dispatch(ctx context.Context, req ToolRequest) ToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	req.invocationContext = ctx

	result := m.dispatch(ctx, req)
	return annotateToolFailure(result)
}

func (m *Manager) dispatch(ctx context.Context, req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "code_navigate":
		return m.codeNavigate(ctx, req)
	case "go_file_outline":
		return m.goFileOutline(req)
	case "go_overview":
		return m.goOverview()
	case "go_entrypoints":
		return m.goEntrypoints(req)
	case "go_find_symbol":
		return m.goFindSymbol(req)
	case "go_find_function":
		return m.goFindFunction(req)
	case "go_find_type":
		return m.goFindType(req)
	case "go_find_const":
		return m.goFindConst(req)
	case "go_read_symbol":
		return m.goReadSymbol(req)
	case "go_find_package":
		return m.goFindPackage(req)
	case "go_related_files":
		return m.goRelatedFiles(req)
	case "go_definition":
		return m.goDefinition(ctx, req)
	case "go_references":
		return m.goReferences(ctx, req)
	case "go_implementations":
		return m.goImplementations(ctx, req)
	case "go_calls_from":
		return m.goCallsFrom(ctx, req)
	case "go_calls_into":
		return m.goCallsInto(ctx, req)
	case "go_extract_body":
		return m.goExtractBody(req)
	case "go_importers":
		return m.goImporters(req)
	case "go_diagnostics":
		return m.goDiagnostics(ctx, req)
	case "go_workspace_symbol":
		return m.goWorkspaceSymbol(ctx, req)
	case "go_move_declarations":
		return m.goMoveDeclarations(req)
	case "cpp_overview":
		return m.cppOverview()
	case "cpp_entrypoints":
		return m.cppEntrypoints(req)
	case "cpp_find_symbol":
		return m.cppFindSymbol(req)
	case "cpp_read_symbol":
		return m.cppReadSymbol(req)
	case "cpp_related_files":
		return m.cppRelatedFiles(req)
	case "cpp_definition":
		return m.cppDefinition(ctx, req)
	case "cpp_declaration":
		return m.cppDeclaration(ctx, req)
	case "cpp_implementations":
		return m.cppImplementations(ctx, req)
	case "cpp_references":
		return m.cppReferences(ctx, req)
	case "cpp_calls_from":
		return m.cppCallsFrom(ctx, req)
	case "cpp_calls_into":
		return m.cppCallsInto(ctx, req)
	case "cpp_extract_body":
		return m.cppExtractBody(req)
	case "cpp_includes":
		return m.cppIncludes(req)
	case "cpp_who_includes":
		return m.cppWhoIncludes(req)
	case "cpp_blast_radius":
		return m.cppBlastRadius(req)
	case "cpp_dead_symbol":
		return m.cppDeadSymbol(ctx, req)
	case "cpp_workspace_symbol":
		return m.cppWorkspaceSymbol(ctx, req)
	case "cpp_diagnostics":
		return m.cppDiagnostics(ctx, req)
	case "cpp_file_outline":
		return m.cppFileOutline(req)
	case "python_overview":
		return m.pythonOverview()
	case "python_entrypoints":
		return m.pythonEntrypoints(req)
	case "python_find_symbol":
		return m.pythonFindSymbol(req)
	case "python_read_symbol":
		return m.pythonReadSymbol(req)
	case "python_related_files":
		return m.pythonRelatedFiles(req)
	case "python_definition":
		return m.pythonDefinition(ctx, req)
	case "python_implementations":
		return m.pythonImplementations(ctx, req)
	case "python_references":
		return m.pythonReferences(ctx, req)
	case "python_calls_from":
		return m.pythonCallsFrom(ctx, req)
	case "python_calls_into":
		return m.pythonCallsInto(ctx, req)
	case "python_workspace_symbol":
		return m.pythonWorkspaceSymbol(ctx, req)
	case "python_diagnostics":
		return m.pythonDiagnostics(ctx, req)
	case "python_file_outline":
		return m.pythonFileOutline(req)
	case "ts_overview":
		return m.tsOverview()
	case "ts_entrypoints":
		return m.tsEntrypoints(req)
	case "ts_find_symbol":
		return m.tsFindSymbol(req)
	case "ts_read_symbol":
		return m.tsReadSymbol(req)
	case "ts_related_files":
		return m.tsRelatedFiles(req)
	case "ts_definition":
		return m.tsDefinition(ctx, req)
	case "ts_implementations":
		return m.tsImplementations(ctx, req)
	case "ts_references":
		return m.tsReferences(ctx, req)
	case "ts_calls_from":
		return m.tsCallsFrom(ctx, req)
	case "ts_calls_into":
		return m.tsCallsInto(ctx, req)
	case "ts_workspace_symbol":
		return m.tsWorkspaceSymbol(ctx, req)
	case "ts_diagnostics":
		return m.tsDiagnostics(ctx, req)
	case "ts_file_outline":
		return m.tsFileOutline(req)
	case "rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_workspace_symbol",
		"rust_read_symbol", "rust_related_files", "rust_file_outline", "rust_definition",
		"rust_references", "rust_calls_from", "rust_calls_into", "rust_implementations",
		"rust_diagnostics":
		return m.ExecuteRustTool(ctx, req)
	case "java_overview":
		return m.javaOverview()
	case "java_entrypoints":
		return m.javaEntrypoints(req)
	case "java_find_symbol":
		return m.javaFindSymbol(req)
	case "java_read_symbol":
		return m.javaReadSymbol(req)
	case "java_related_files":
		return m.javaRelatedFiles(req)
	case "java_workspace_symbol":
		return m.javaWorkspaceSymbol(ctx, req)
	case "java_definition":
		return m.javaDefinition(ctx, req)
	case "java_references":
		return m.javaReferences(ctx, req)
	case "java_implementations":
		return m.javaImplementations(ctx, req)
	case "java_calls_from":
		return m.javaCallsFrom(ctx, req)
	case "java_calls_into":
		return m.javaCallsInto(ctx, req)
	case "java_diagnostics":
		return m.javaDiagnostics(ctx, req)
	case "java_file_outline":
		return m.javaFileOutline(req)
	default:
		return ToolResult{Tool: tool, Summary: "Language tool request rejected", Error: fmt.Sprintf("unsupported language tool %q", tool)}
	}
}
