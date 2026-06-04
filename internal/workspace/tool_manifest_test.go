package workspace

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildToolManifestSuppressesPlaceholderRequests(t *testing.T) {
	manifest := BuildToolManifest([]string{"submit_plan", "edit_file", "pwd"})

	byTool := map[string][]ToolManifestEntry{}
	for _, entry := range manifest {
		byTool[entry.Tool] = append(byTool[entry.Tool], entry)
	}

	if got := byTool["submit_plan"][0].Request; got != "" {
		t.Fatalf("submit_plan placeholder request should be suppressed, got %q", got)
	}
	for _, entry := range byTool["edit_file"] {
		if entry.Request != "" {
			t.Fatalf("edit_file placeholder request should be suppressed, got %q", entry.Request)
		}
	}
	if got := byTool["pwd"][0].Request; got == "" || strings.Contains(got, "relative/file") {
		t.Fatalf("concrete request should remain available, got %q", got)
	}
}

func TestRenderToolManifestInstructionsOmitsBlankRequestSlot(t *testing.T) {
	rendered := RenderToolManifestInstructions([]string{"submit_plan"})

	if strings.Contains(rendered, "submit_plan  (") {
		t.Fatalf("blank request should not render as a double-spaced slot:\n%s", rendered)
	}
	if strings.Contains(rendered, "relative/file") || strings.Contains(rendered, "one paragraph") {
		t.Fatalf("placeholder request text leaked into manifest instructions:\n%s", rendered)
	}
	if !strings.Contains(rendered, "- submit_plan (") {
		t.Fatalf("sanitized manifest should still name the tool and purpose:\n%s", rendered)
	}
}

func TestRenderToolManifestInstructionsSuppressesManifestWidePlaceholderRequests(t *testing.T) {
	rendered := strings.ToLower(RenderToolManifestInstructions(nil))
	requestText := renderedManifestRequestText(rendered)
	for _, forbidden := range []string{
		"symbol|text",
		`"pattern":"name"`,
		`"pattern":"funcname"`,
		`"pattern":"functionname"`,
		`"pattern":"typename"`,
		`"pattern":"constname"`,
		`"pattern":"interfacename"`,
		`"pattern":"classname`,
		`"pattern":"methodname"`,
		`"path":"file.go"`,
		`"path":"source.go"`,
		`"destination":"target.go"`,
		`"path":"src/file`,
		`"path":"src/pkg/file.py"`,
		`"path":"src/main/java/com/example/file.java"`,
		`"path":"include/file.h"`,
		`"path":"relative/dir"`,
		`"path":"optional/path"`,
		"session-001",
		"session.jsonl",
		"relative/file",
		"exact substring",
		"replacement text",
		"complete replacement",
		"one paragraph",
	} {
		if strings.Contains(requestText, forbidden) {
			t.Fatalf("placeholder request fragment %q leaked into rendered manifest requests:\n%s", forbidden, requestText)
		}
	}
	if !strings.Contains(requestText, `{"tool":"pwd"}`) {
		t.Fatalf("concrete non-placeholder request should still render:\n%s", rendered)
	}
}

func TestHistoryRangeManifestDescribesPathOnlyDefault(t *testing.T) {
	manifest := BuildToolManifest([]string{"history_range"})
	if len(manifest) != 1 {
		t.Fatalf("expected one history_range manifest entry, got %#v", manifest)
	}
	entry := manifest[0]
	if !strings.Contains(entry.Purpose, "file") || !strings.Contains(entry.Purpose, "paired start_line/end_line") {
		t.Fatalf("history_range purpose should describe path-only default and paired bounds, got %q", entry.Purpose)
	}
	if strings.Contains(entry.Request, "start_line") || strings.Contains(entry.Request, "end_line") {
		t.Fatalf("history_range manifest request should not advertise line bounds as required, got %q", entry.Request)
	}
}

func TestToolManifestToolsDerivesLanguageMembershipFromRegistry(t *testing.T) {
	tools := ToolManifestTools(ToolManifestToolOptions{
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	})
	seen := make(map[string]int, len(tools))
	for _, tool := range tools {
		seen[tool]++
	}
	for _, lang := range LanguageRegistry() {
		for capability, tool := range lang.Tools {
			if seen[tool] != 1 {
				t.Fatalf("%s tool %s for %s manifest count = %d, want 1", lang.Name, tool, capability, seen[tool])
			}
		}
	}
	for _, spec := range supplementalLanguageToolSpecs {
		if seen[spec.Tool] != 1 {
			t.Fatalf("supplemental language tool %s manifest count = %d, want 1", spec.Tool, seen[spec.Tool])
		}
	}
}

func TestToolManifestToolsPreservesLanguageToolOrder(t *testing.T) {
	cases := []struct {
		name string
		opts ToolManifestToolOptions
		want []string
	}{
		{
			name: "go",
			opts: ToolManifestToolOptions{IncludeGo: true},
			want: []string{
				"go_overview", "go_entrypoints", "go_find_symbol", "go_find_function", "go_find_type", "go_find_const",
				"go_find_package", "go_related_files", "go_workspace_symbol", "go_references", "go_definition", "go_implementations",
				"go_calls_from", "go_calls_into", "go_read_symbol", "go_extract_body", "go_importers", "go_diagnostics", "go_file_outline",
			},
		},
		{
			name: "cpp",
			opts: ToolManifestToolOptions{IncludeCpp: true},
			want: []string{
				"cpp_overview", "cpp_entrypoints", "cpp_find_symbol", "cpp_read_symbol", "cpp_related_files", "cpp_references",
				"cpp_definition", "cpp_declaration", "cpp_implementations", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
				"cpp_includes", "cpp_who_includes", "cpp_blast_radius", "cpp_dead_symbol", "cpp_workspace_symbol", "cpp_diagnostics", "cpp_file_outline",
			},
		},
		{
			name: "rust",
			opts: ToolManifestToolOptions{IncludeRust: true},
			want: []string{
				"rust_overview", "rust_entrypoints", "rust_find_symbol", "rust_workspace_symbol", "rust_read_symbol", "rust_related_files",
				"rust_file_outline", "rust_definition", "rust_references", "rust_calls_from", "rust_calls_into", "rust_implementations", "rust_diagnostics",
			},
		},
		{
			name: "python",
			opts: ToolManifestToolOptions{IncludePython: true},
			want: []string{
				"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol", "python_related_files", "python_references",
				"python_definition", "python_implementations", "python_calls_from", "python_calls_into", "python_workspace_symbol", "python_diagnostics", "python_file_outline",
			},
		},
		{
			name: "typescript",
			opts: ToolManifestToolOptions{IncludeTypescript: true},
			want: []string{
				"ts_overview", "ts_entrypoints", "ts_find_symbol", "ts_read_symbol", "ts_related_files", "ts_definition",
				"ts_implementations", "ts_references", "ts_calls_from", "ts_calls_into", "ts_workspace_symbol", "ts_diagnostics", "ts_file_outline",
			},
		},
		{
			name: "java",
			opts: ToolManifestToolOptions{IncludeJava: true},
			want: []string{
				"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files", "java_workspace_symbol",
				"java_definition", "java_references", "java_implementations", "java_calls_from", "java_calls_into", "java_diagnostics", "java_file_outline",
			},
		},
	}
	for _, tc := range cases {
		got := ToolManifestTools(tc.opts)
		offset := len(ToolManifestTools(ToolManifestToolOptions{}))
		if len(got) < offset+len(tc.want) {
			t.Fatalf("%s manifest too short: %#v", tc.name, got)
		}
		gotLanguageTools := got[offset : offset+len(tc.want)]
		if strings.Join(gotLanguageTools, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("%s manifest order = %#v, want %#v", tc.name, gotLanguageTools, tc.want)
		}
	}
}

func TestToolManifestWriteToolsDeriveFromSurfaceMetadata(t *testing.T) {
	writeTools := ToolManifestTools(ToolManifestToolOptions{IncludeWriteTools: true})
	for _, tool := range []string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit"} {
		if !slices.Contains(writeTools, tool) {
			t.Fatalf("write manifest missing %s: %#v", tool, writeTools)
		}
	}
	if slices.Contains(writeTools, "go_move_declarations") {
		t.Fatalf("generic write manifest should not include Go-specific write tool: %#v", writeTools)
	}

	goWriteTools := ToolManifestTools(ToolManifestToolOptions{IncludeWriteTools: true, IncludeGo: true})
	if !slices.Contains(goWriteTools, "go_move_declarations") {
		t.Fatalf("Go write manifest should include go_move_declarations: %#v", goWriteTools)
	}
}

func renderedManifestRequestText(rendered string) string {
	var b strings.Builder
	for _, line := range strings.Split(rendered, "\n") {
		idx := strings.Index(line, " {")
		if idx < 0 {
			continue
		}
		request := line[idx+1:]
		if end := strings.Index(request, " ("); end >= 0 {
			request = request[:end]
		}
		b.WriteString(request)
		b.WriteByte('\n')
	}
	return b.String()
}
