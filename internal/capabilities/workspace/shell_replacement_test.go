package workspace

import "testing"

func TestAnalyzeShellCommandExtractsReadFileReplacements(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		kind      ShellReplacementKind
		path      string
		startLine int
		endLine   int
	}{
		{
			name:      "sed range",
			command:   "sed -n '100,150p' path.go",
			kind:      ShellReplacementSedReadFile,
			path:      "path.go",
			startLine: 100,
			endLine:   150,
		},
		{
			name:      "head shorthand",
			command:   "head -20 file.go",
			kind:      ShellReplacementHeadTailReadFile,
			path:      "file.go",
			startLine: 1,
			endLine:   20,
		},
		{
			name:      "head n flag",
			command:   "head -n 20 file.go",
			kind:      ShellReplacementHeadTailReadFile,
			path:      "file.go",
			startLine: 1,
			endLine:   20,
		},
		{
			name:      "tail open ended",
			command:   "tail -20 file.go",
			kind:      ShellReplacementHeadTailReadFile,
			path:      "file.go",
			startLine: 1,
		},
		{
			name:    "cat whole file",
			command: "cat README.md",
			kind:    ShellReplacementCatReadFile,
			path:    "README.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replacement := requireShellReplacement(t, tt.command, tt.kind)
			if replacement.Capability != Capability(ToolNameReadFile) {
				t.Fatalf("Capability = %q, want read_file", replacement.Capability)
			}
			if replacement.Request.Tool != ToolNameReadFile {
				t.Fatalf("Request.Tool = %q, want read_file", replacement.Request.Tool)
			}
			if replacement.Request.Path != tt.path {
				t.Fatalf("Request.Path = %q, want %q", replacement.Request.Path, tt.path)
			}
			if replacement.Request.StartLine != tt.startLine || replacement.Request.EndLine != tt.endLine {
				t.Fatalf("line range = %d-%d, want %d-%d", replacement.Request.StartLine, replacement.Request.EndLine, tt.startLine, tt.endLine)
			}
		})
	}
}

func TestAnalyzeShellCommandExtractsFileStatsReplacements(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		kind      ShellReplacementKind
		path      string
		startLine int
		endLine   int
	}{
		{
			name:    "wc short flag",
			command: "wc -l file.go",
			kind:    ShellReplacementWCFileStats,
			path:    "file.go",
		},
		{
			name:    "wc long flag",
			command: "wc --lines file.go",
			kind:    ShellReplacementWCFileStats,
			path:    "file.go",
		},
		{
			name:      "sed single line wc",
			command:   "sed -n '84p' file.go | wc -c",
			kind:      ShellReplacementSedWCFileStats,
			path:      "file.go",
			startLine: 84,
			endLine:   84,
		},
		{
			name:      "sed range wc",
			command:   "sed -n '84,90p' file.go | wc -c",
			kind:      ShellReplacementSedWCFileStats,
			path:      "file.go",
			startLine: 84,
			endLine:   90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replacement := requireShellReplacement(t, tt.command, tt.kind)
			if replacement.Capability != Capability(ToolNameFileStats) {
				t.Fatalf("Capability = %q, want file_stats", replacement.Capability)
			}
			if replacement.Request.Tool != ToolNameFileStats {
				t.Fatalf("Request.Tool = %q, want file_stats", replacement.Request.Tool)
			}
			if replacement.Request.Path != tt.path {
				t.Fatalf("Request.Path = %q, want %q", replacement.Request.Path, tt.path)
			}
			if replacement.Request.StartLine != tt.startLine || replacement.Request.EndLine != tt.endLine {
				t.Fatalf("line range = %d-%d, want %d-%d", replacement.Request.StartLine, replacement.Request.EndLine, tt.startLine, tt.endLine)
			}
		})
	}
}

func TestAnalyzeShellCommandExtractsSearchFilesReplacements(t *testing.T) {
	tests := []struct {
		command string
		pattern string
	}{
		{command: "find . -name '*.go'", pattern: "*.go"},
		{command: "find internal -name runner_core.go", pattern: "runner_core.go"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			replacement := requireShellReplacement(t, tt.command, ShellReplacementFindSearchFiles)
			if replacement.Capability != Capability(ToolNameSearchFiles) {
				t.Fatalf("Capability = %q, want search_files", replacement.Capability)
			}
			if replacement.Request.Tool != ToolNameSearchFiles {
				t.Fatalf("Request.Tool = %q, want search_files", replacement.Request.Tool)
			}
			if replacement.Request.Pattern != tt.pattern {
				t.Fatalf("Request.Pattern = %q, want %q", replacement.Request.Pattern, tt.pattern)
			}
		})
	}
}

func TestAnalyzeShellCommandExtractsIdentifierGrepReplacementFacts(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		capability Capability
		pattern    string
		path       string
		glob       string
	}{
		{
			name:       "scoped grep uses references intent",
			command:    "grep -n RunPolicy main.go",
			capability: CapReferences,
			pattern:    "RunPolicy",
			path:       "main.go",
		},
		{
			name:       "recursive grep uses workspace symbol intent",
			command:    "grep -rn RunPolicy",
			capability: CapWorkspaceSymbol,
			pattern:    "RunPolicy",
		},
		{
			name:       "include glob is retained",
			command:    "grep -rn --include=*.py Widget",
			capability: CapWorkspaceSymbol,
			pattern:    "Widget",
			glob:       "*.py",
		},
		{
			name:       "declaration pattern extracts identifier",
			command:    "grep -n 'type ToolRequest struct' internal/capabilities/workspace/manager.go",
			capability: CapReferences,
			pattern:    "ToolRequest",
			path:       "internal/capabilities/workspace/manager.go",
		},
		{
			name:       "identifier alternation chooses first",
			command:    `grep -rn 'parseToolCall\|ParseToolRequest' internal/ --include='*.go'`,
			capability: CapReferences,
			pattern:    "parseToolCall",
			path:       "internal/",
			glob:       "*.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replacement := requireShellReplacement(t, tt.command, ShellReplacementSymbolSearch)
			if replacement.Capability != tt.capability {
				t.Fatalf("Capability = %q, want %q", replacement.Capability, tt.capability)
			}
			if replacement.Symbol.Pattern != tt.pattern {
				t.Fatalf("Symbol.Pattern = %q, want %q", replacement.Symbol.Pattern, tt.pattern)
			}
			if replacement.Symbol.Path != tt.path {
				t.Fatalf("Symbol.Path = %q, want %q", replacement.Symbol.Path, tt.path)
			}
			if replacement.Symbol.Glob != tt.glob {
				t.Fatalf("Symbol.Glob = %q, want %q", replacement.Symbol.Glob, tt.glob)
			}
		})
	}
}

func TestAnalyzeShellCommandRejectsUnsafeReplacementCandidates(t *testing.T) {
	for _, command := range []string{
		"sed -i 's/foo/bar/' main.go",
		"sed -e '1d' main.go",
		"sed -n '1,20p' main.go | head -5",
		"wc file1 file2",
		"find . -type f | wc -l",
		"wc -L file.go",
		`grep -rn "func.*Run"`,
		`grep -rn "Run" | head -20`,
		`grep -rn "Run" > out.txt`,
		`grep -rn "$VAR"`,
		`grep -E '^(foo|bar)$' main.go`,
		"head -0 file.go",
		"head -20 '*.go'",
		"cat -n README.md",
		"cat a.go b.go",
		"cat *.go",
		"find . -type f -name '*.go'",
		"find . -name '*.go' -exec cat {} \\;",
		"find . -name '*.go' | head",
	} {
		t.Run(command, func(t *testing.T) {
			if replacement := AnalyzeShellCommand(command).Replacement; replacement != nil {
				t.Fatalf("AnalyzeShellCommand(%q).Replacement = %#v, want nil", command, replacement)
			}
		})
	}
}

func requireShellReplacement(t *testing.T, command string, kind ShellReplacementKind) *ShellReplacementCandidate {
	t.Helper()

	replacement := AnalyzeShellCommand(command).Replacement
	if replacement == nil {
		t.Fatalf("AnalyzeShellCommand(%q).Replacement = nil, want %s", command, kind)
	}
	if replacement.Kind != kind {
		t.Fatalf("Replacement.Kind = %q, want %q; replacement=%#v", replacement.Kind, kind, replacement)
	}

	return replacement
}
