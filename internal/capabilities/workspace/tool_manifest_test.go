package workspace

import (
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("tool manifest", func() {
	ginkgo.It("build tool manifest suppresses placeholder requests", func() {
		manifest := BuildToolManifest([]string{"submit_plan", "edit_file", "pwd"})

		byTool := map[string][]ToolManifestEntry{}
		for _, entry := range manifest {
			byTool[entry.Tool] = append(byTool[entry.Tool], entry)
		}

		if got := byTool["submit_plan"][0].Request; got != "" {
			ginkgo.Fail(fmt.Sprintf("submit_plan placeholder request should be suppressed, got %q", got))
		}

		for _, entry := range byTool["edit_file"] {
			gomega.Expect(entry.Request).To(gomega.Equal(""), fmt.Sprintf("edit_file placeholder request should be suppressed, got %q", entry.Request))

		}

		if got := byTool["pwd"][0].Request; got == "" || strings.Contains(got, "relative/file") {
			ginkgo.Fail(fmt.Sprintf("concrete request should remain available, got %q", got))
		}
	})
	ginkgo.It("build tool manifest suppresses manifest wide placeholder requests", func() {
		manifest := BuildToolManifest(nil)
		requests := make([]string, 0, len(manifest))
		for _, entry := range manifest {
			requests = append(requests, entry.Request)
		}
		requestText := strings.ToLower(strings.Join(requests, "\n"))

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
			gomega.Expect(requestText).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("placeholder request fragment %q leaked into rendered manifest requests:\n%s", forbidden, requestText))

		}
		gomega.Expect(requestText).To(gomega.ContainSubstring(`{"tool":"pwd"}`), fmt.Sprintf("concrete non-placeholder request should still render:\n%s", requestText))

	})
	ginkgo.It("history range manifest describes path only default", func() {
		manifest := BuildToolManifest([]string{"history_range"})
		gomega.Expect(manifest).To(gomega.HaveLen(1), fmt.Sprintf("expected one history_range manifest entry, got %#v", manifest))

		entry := manifest[0]
		gomega.Expect(entry.Purpose).To(gomega.ContainSubstring("file"), fmt.Sprintf("history_range purpose should describe path-only default and paired bounds, got %q", entry.Purpose))
		gomega.Expect(entry.Purpose).To(gomega.ContainSubstring("paired start_line/end_line"), fmt.Sprintf("history_range purpose should describe path-only default and paired bounds, got %q", entry.Purpose))
		gomega.Expect(entry.Request).NotTo(gomega.ContainSubstring("start_line"), fmt.Sprintf("history_range manifest request should not advertise line bounds as required, got %q", entry.Request))
		gomega.Expect(entry.Request).NotTo(gomega.ContainSubstring("end_line"), fmt.Sprintf("history_range manifest request should not advertise line bounds as required, got %q", entry.Request))

	})
	ginkgo.It("tool manifest tools derives language membership from registry", func() {
		tools := ToolManifestTools(ToolManifestToolOptions{
			IncludeGo:         true,
			IncludeCpp:        true,
			IncludePython:     true,
			IncludeTypescript: true,
			IncludeRust:       true,
			IncludeJava:       true,
			IncludeGDScript:   true,
		})
		seen := make(map[string]int, len(tools))

		for _, tool := range tools {
			seen[tool]++
		}

		for _, lang := range semanticlanguage.LanguageRegistry() {
			for capability, tool := range lang.Tools {
				gomega.Expect(seen[tool]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("%s tool %s for %s manifest count = %d, want 1", lang.Name, tool, capability, seen[tool]))

			}
		}

		for _, spec := range languagetools.ToolCatalog() {
			if !spec.Supplemental {
				continue
			}
			gomega.Expect(seen[spec.Tool]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("supplemental language tool %s manifest count = %d, want 1", spec.Tool, seen[spec.Tool]))

		}
	})
	ginkgo.It("tool manifest tools preserves language tool order", func() {
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
					"python_overview", "python_entrypoints", "python_find_symbol", "python_read_symbol", "python_related_files", "python_definition",
					"python_implementations", "python_references", "python_calls_from", "python_calls_into", "python_workspace_symbol", "python_diagnostics", "python_file_outline",
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
					"java_overview", "java_entrypoints", "java_find_symbol", "java_read_symbol", "java_related_files", "java_definition",
					"java_implementations", "java_references", "java_calls_from", "java_calls_into", "java_workspace_symbol", "java_diagnostics", "java_file_outline",
				},
			},
			{
				name: "gdscript",
				opts: ToolManifestToolOptions{IncludeGDScript: true},
				want: []string{
					"gdscript_references", "gdscript_calls_into",
				},
			},
		}
		for _, tc := range cases {
			got := ToolManifestTools(tc.opts)
			offset := len(ToolManifestTools(ToolManifestToolOptions{}))
			gomega.Expect(len(got) < offset+len(tc.want)).To(gomega.BeFalse(), fmt.Sprintf("%s manifest too short: %#v", tc.name, got))

			gotLanguageTools := got[offset : offset+len(tc.want)]
			gomega.Expect(strings.Join(gotLanguageTools, ",")).To(gomega.Equal(strings.Join(tc.want, ",")), fmt.Sprintf("%s manifest order = %#v, want %#v", tc.name, gotLanguageTools, tc.want))

		}
	})
	ginkgo.It("tool manifest write tools derive from surface metadata", func() {
		writeTools := ToolManifestTools(ToolManifestToolOptions{IncludeWriteTools: true})
		for _, tool := range []string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch"} {
			gomega.Expect(slices.Contains(writeTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("write manifest missing %s: %#v", tool, writeTools))

		}
		gomega.Expect(slices.Contains(writeTools, "go_move_declarations")).To(gomega.BeFalse(), fmt.Sprintf("generic write manifest should not include Go-specific write tool: %#v", writeTools))

		goWriteTools := ToolManifestTools(ToolManifestToolOptions{IncludeWriteTools: true, IncludeGo: true})
		gomega.Expect(slices.Contains(goWriteTools, "go_move_declarations")).To(gomega.BeTrue(), fmt.Sprintf("Go write manifest should include go_move_declarations: %#v", goWriteTools))

	})
})
