package language

import (
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/repository/discovery"
)

// Capability names the *kind* of question a tool answers, independent of
// language. The discovery redirect layer reasons in capability space so
// adding a new language is one LanguageSupport struct literal plus the
// tool implementations — no changes to the redirect rules themselves.
type Capability string

const (
	CapFileOutline     Capability = "file_outline"
	CapFindSymbol      Capability = "find_symbol"
	CapWorkspaceSymbol Capability = "workspace_symbol"
	CapDefinition      Capability = "definition"
	CapReferences      Capability = "references"
	CapCallsFrom       Capability = "calls_from"
	CapCallsInto       Capability = "calls_into"
	CapReadSymbol      Capability = "read_symbol"
	CapExtractBody     Capability = "extract_body"
	CapRelatedFiles    Capability = "related_files"
	CapImplementations Capability = "implementations"
	CapDiagnostics     Capability = "diagnostics"
	CapOverview        Capability = "overview"
	CapEntrypoints     Capability = "entrypoints"
)

// LanguageSupport describes what structured tools are available for a
// language, which files trigger it, and a one-paragraph decision guide
// the runner can inject JIT when the model is about to reach for a
// generic tool on a file this language covers.
type LanguageSupport struct {
	Name          string
	Extensions    []string
	Tools         map[Capability]string
	DecisionGuide string
	Detect        func(root string) bool
}

var languageRegistry = []LanguageSupport{
	{
		Name:       "go",
		Extensions: []string{".go"},
		Tools: map[Capability]string{
			CapFileOutline:     "go_file_outline",
			CapFindSymbol:      "go_find_symbol",
			CapWorkspaceSymbol: "go_workspace_symbol",
			CapDefinition:      "go_definition",
			CapReferences:      "go_references",
			CapCallsFrom:       "go_calls_from",
			CapCallsInto:       "go_calls_into",
			CapReadSymbol:      "go_read_symbol",
			CapExtractBody:     "go_extract_body",
			CapRelatedFiles:    "go_related_files",
			CapImplementations: "go_implementations",
			CapDiagnostics:     "go_diagnostics",
			CapOverview:        "go_overview",
			CapEntrypoints:     "go_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Go capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .go file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors where a symbol is declared.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Related-files capability finds files tied to a concept or symbol.
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{markers: []string{"go.mod"}})
		},
	},
	{
		Name:       "cpp",
		Extensions: []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp"},
		Tools: map[Capability]string{
			CapFileOutline:     "cpp_file_outline",
			CapFindSymbol:      "cpp_find_symbol",
			CapWorkspaceSymbol: "cpp_workspace_symbol",
			CapDefinition:      "cpp_definition",
			CapReferences:      "cpp_references",
			CapCallsFrom:       "cpp_calls_from",
			CapCallsInto:       "cpp_calls_into",
			CapReadSymbol:      "cpp_read_symbol",
			CapExtractBody:     "cpp_extract_body",
			CapRelatedFiles:    "cpp_related_files",
			CapImplementations: "cpp_implementations",
			CapDiagnostics:     "cpp_diagnostics",
			CapOverview:        "cpp_overview",
			CapEntrypoints:     "cpp_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
C/C++ capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .cpp/.h file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors declarations or definitions.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Related-files capability finds files tied to a concept or symbol.
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{
				markers:        []string{"compile_commands.json"},
				matchExtension: isCppExtension,
			})
		},
	},
	{
		Name:       "python",
		Extensions: []string{".py", ".pyi"},
		Tools: map[Capability]string{
			CapFileOutline:     "python_file_outline",
			CapFindSymbol:      "python_find_symbol",
			CapWorkspaceSymbol: "python_workspace_symbol",
			CapDefinition:      "python_definition",
			CapReferences:      "python_references",
			CapCallsFrom:       "python_calls_from",
			CapCallsInto:       "python_calls_into",
			CapReadSymbol:      "python_read_symbol",
			CapRelatedFiles:    "python_related_files",
			CapImplementations: "python_implementations",
			CapDiagnostics:     "python_diagnostics",
			CapOverview:        "python_overview",
			CapEntrypoints:     "python_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Python capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .py file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors where a symbol is declared.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Implementations capability finds subclasses or implementers.
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{
				markers: []string{
					"pyproject.toml",
					"setup.py",
					"setup.cfg",
					"requirements.txt",
					"Pipfile",
					"poetry.lock",
				},
				extensions: []string{".py", ".pyi"},
				skipDirs:   []string{".venv", "venv", "__pycache__", ".mypy_cache", ".pytest_cache", ".tox", "site-packages", ".eggs"},
			})
		},
	},
	{
		Name:       "typescript",
		Extensions: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".d.ts", ".d.mts", ".d.cts"},
		Tools: map[Capability]string{
			CapFileOutline:     "ts_file_outline",
			CapFindSymbol:      "ts_find_symbol",
			CapWorkspaceSymbol: "ts_workspace_symbol",
			CapDefinition:      "ts_definition",
			CapReferences:      "ts_references",
			CapCallsFrom:       "ts_calls_from",
			CapCallsInto:       "ts_calls_into",
			CapReadSymbol:      "ts_read_symbol",
			CapRelatedFiles:    "ts_related_files",
			CapImplementations: "ts_implementations",
			CapDiagnostics:     "ts_diagnostics",
			CapOverview:        "ts_overview",
			CapEntrypoints:     "ts_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
TypeScript/JavaScript capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .ts/.tsx/.js/.jsx file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors where a symbol is declared.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Related-files capability finds files tied to a concept or symbol.
The same route covers .js/.jsx/.mjs/.cjs. A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{
				markers:        []string{"tsconfig.json", "jsconfig.json", "package.json"},
				matchExtension: isTsExtension,
				skipDirs:       []string{".next", "node_modules"},
				skipRootDirs:   true,
			})
		},
	},
	{
		Name:       "rust",
		Extensions: []string{".rs"},
		Tools: map[Capability]string{
			CapFileOutline:     "rust_file_outline",
			CapFindSymbol:      "rust_find_symbol",
			CapWorkspaceSymbol: "rust_workspace_symbol",
			CapDefinition:      "rust_definition",
			CapReferences:      "rust_references",
			CapCallsFrom:       "rust_calls_from",
			CapCallsInto:       "rust_calls_into",
			CapReadSymbol:      "rust_read_symbol",
			CapRelatedFiles:    "rust_related_files",
			CapImplementations: "rust_implementations",
			CapDiagnostics:     "rust_diagnostics",
			CapOverview:        "rust_overview",
			CapEntrypoints:     "rust_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Rust capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .rs file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors where a symbol is declared.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Implementations capability finds trait impls across the crate.
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{
				markers:    []string{"Cargo.toml"},
				extensions: []string{".rs"},
			})
		},
	},
	{
		Name:       "java",
		Extensions: []string{".java"},
		Tools: map[Capability]string{
			CapFileOutline:     "java_file_outline",
			CapFindSymbol:      "java_find_symbol",
			CapWorkspaceSymbol: "java_workspace_symbol",
			CapDefinition:      "java_definition",
			CapReferences:      "java_references",
			CapCallsFrom:       "java_calls_from",
			CapCallsInto:       "java_calls_into",
			CapReadSymbol:      "java_read_symbol",
			CapRelatedFiles:    "java_related_files",
			CapImplementations: "java_implementations",
			CapDiagnostics:     "java_diagnostics",
			CapOverview:        "java_overview",
			CapEntrypoints:     "java_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Java capability guide - prefer structured capabilities over raw shell/grep:
- File-outline capability orients a .java file.
- Symbol-lookup capability locates declarations by name or pattern.
- Definition capability anchors where a symbol is declared.
- References and calls-into capabilities show users or callers.
- Calls-from capability shows outgoing handoff from a known symbol.
- Read-symbol capability returns one declaration body.
- Implementations capability finds interface implementers.
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			return detectWorkspace(root, detectionSpec{
				markers:    []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"},
				extensions: []string{".java"},
				skipDirs:   []string{"target", "build", ".gradle", ".mvn"},
			})
		},
	},
}

// LanguageRegistry returns the built-in set. Callers should treat the
// slice as read-only.
func LanguageRegistry() []LanguageSupport {
	out := make([]LanguageSupport, len(languageRegistry))
	copy(out, languageRegistry)

	return out
}

// LanguageForExtension maps a file extension (including the leading dot)
// to the first matching language. Case-insensitive.
func LanguageForExtension(ext string) (LanguageSupport, bool) {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return LanguageSupport{}, false
	}

	for _, lang := range languageRegistry {
		for _, candidate := range lang.Extensions {
			if strings.ToLower(candidate) == ext {
				return lang, true
			}
		}
	}

	return LanguageSupport{}, false
}

// LanguageForFile inspects a path (absolute or relative) and returns
// the matching language, if any.
func LanguageForFile(path string) (LanguageSupport, bool) {
	return LanguageForExtension(filepath.Ext(path))
}

// LanguageForName maps model-facing language names and common aliases
// to the registry entry used by capability routers.
func LanguageForName(name string) (LanguageSupport, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, ".")

	switch name {
	case "golang":
		name = "go"
	case "c", "cc", "cxx", "c++":
		name = "cpp"
	case "py":
		name = "python"
	case "js", "javascript", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts", "d.ts", "d.mts", "d.cts":
		name = "typescript"
	case "rs":
		name = "rust"
	}

	for _, lang := range languageRegistry {
		if strings.EqualFold(lang.Name, name) {
			return lang, true
		}
	}

	return LanguageSupport{}, false
}

// DiagnosticToolForLanguage returns the diagnostics tool for a language
// name used by graph/context records. JavaScript shares the TypeScript
// toolchain in this harness.
func DiagnosticToolForLanguage(language string) string {
	if lang, ok := LanguageForName(language); ok {
		return lang.Tool(CapDiagnostics)
	}

	return ""
}

// DiagnosticToolNames returns the built-in diagnostics tools derived from
// the language registry rather than a separate per-language list.
func DiagnosticToolNames() []string {
	seen := make(map[string]struct{}, len(languageRegistry))
	out := make([]string, 0, len(languageRegistry))

	for _, lang := range languageRegistry {
		tool := strings.TrimSpace(lang.Tool(CapDiagnostics))
		if tool == "" {
			continue
		}

		if _, ok := seen[tool]; ok {
			continue
		}

		seen[tool] = struct{}{}
		out = append(out, tool)
	}

	return out
}

func IsDiagnosticToolName(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	for _, candidate := range DiagnosticToolNames() {
		if candidate == tool {
			return true
		}
	}

	return false
}

// ActiveLanguages returns languages whose Detect function reports true
// against the workspace root.
func ActiveLanguages(root string) []LanguageSupport {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}

	out := make([]LanguageSupport, 0, len(languageRegistry))
	for _, lang := range languageRegistry {
		if lang.Detect != nil && lang.Detect(root) {
			out = append(out, lang)
		}
	}

	return out
}

// Tool looks up the tool name that implements a capability for this
// language. Returns "" if the language does not expose that capability.
func (l LanguageSupport) Tool(cap Capability) string {
	if l.Tools == nil {
		return ""
	}

	return l.Tools[cap]
}

type detectionSpec struct {
	markers        []string
	extensions     []string
	skipDirs       []string
	skipRootDirs   bool
	matchExtension func(string) bool
}

func detectWorkspace(root string, spec detectionSpec) bool {
	for _, marker := range spec.markers {
		if info, err := os.Stat(filepath.Join(root, marker)); err == nil && !info.IsDir() {
			return true
		}
	}

	if len(spec.extensions) == 0 && spec.matchExtension == nil {
		return false
	}

	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			skipDiscoveryDir := path != root && discovery.ShouldSkipDirName(d.Name())
			skipCustomDir := spec.skipDir(d.Name()) && (path != root || spec.skipRootDirs)
			if skipDiscoveryDir || skipCustomDir {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}
		if spec.matchesExtension(filepath.Ext(path)) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})

	return found
}

func (spec detectionSpec) matchesExtension(ext string) bool {
	if spec.matchExtension != nil {
		return spec.matchExtension(ext)
	}

	for _, candidate := range spec.extensions {
		if strings.EqualFold(ext, candidate) {
			return true
		}
	}
	return false
}

func (spec detectionSpec) skipDir(name string) bool {
	for _, candidate := range spec.skipDirs {
		if name == candidate {
			return true
		}
	}
	return false
}

func isCppExtension(ext string) bool {
	_, ok := map[string]struct{}{
		".c": {}, ".cc": {}, ".cpp": {}, ".cxx": {},
		".h": {}, ".hh": {}, ".hpp": {}, ".hxx": {}, ".ipp": {}, ".inl": {}, ".tpp": {},
	}[strings.ToLower(ext)]

	return ok
}

func isTsExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}
