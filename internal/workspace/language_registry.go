package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/discovery"
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
			CapRelatedFiles:    "go_related_files",
			CapImplementations: "go_implementations",
			CapDiagnostics:     "go_diagnostics",
			CapOverview:        "go_overview",
			CapEntrypoints:     "go_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
Go decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .go file: code_navigate capability=file_outline path=<file.go>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file.go>
- Files related to a concept: code_navigate capability=related_files pattern=<term> path=<scope>
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			info, err := os.Stat(filepath.Join(root, "go.mod"))
			return err == nil && !info.IsDir()
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
			CapRelatedFiles:    "cpp_related_files",
			CapImplementations: "cpp_implementations",
			CapDiagnostics:     "cpp_diagnostics",
			CapOverview:        "cpp_overview",
			CapEntrypoints:     "cpp_entrypoints",
		},
		DecisionGuide: strings.TrimSpace(`
C/C++ decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .cpp/.h file: code_navigate capability=file_outline path=<file>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition/declaration: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file>
- Files related to a concept: code_navigate capability=related_files pattern=<term> path=<scope>
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			if info, err := os.Stat(filepath.Join(root, "compile_commands.json")); err == nil && !info.IsDir() {
				return true
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if discovery.ShouldSkipDirName(d.Name()) && path != root {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if isCppExtension(filepath.Ext(path)) {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
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
Python decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .py file: code_navigate capability=file_outline path=<file.py>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file.py>
- Subclasses/implementations: code_navigate capability=implementations pattern=<name>
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			markers := []string{
				"pyproject.toml",
				"setup.py",
				"setup.cfg",
				"requirements.txt",
				"Pipfile",
				"poetry.lock",
			}
			for _, marker := range markers {
				if info, err := os.Stat(filepath.Join(root, marker)); err == nil && !info.IsDir() {
					return true
				}
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if discovery.ShouldSkipDirName(d.Name()) && path != root {
						return filepath.SkipDir
					}
					switch d.Name() {
					case ".venv", "venv", "__pycache__", ".mypy_cache",
						".pytest_cache", ".tox", "site-packages", ".eggs":
						if path != root {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				switch strings.ToLower(filepath.Ext(path)) {
				case ".py", ".pyi":
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
		},
	},
	{
		Name:       "typescript",
		Extensions: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".d.ts"},
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
TypeScript/JavaScript decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .ts/.tsx/.js/.jsx file: code_navigate capability=file_outline path=<file>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file>
- Files related to a concept: code_navigate capability=related_files pattern=<term> path=<scope>
The same route covers .js/.jsx/.mjs/.cjs. A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			for _, marker := range []string{"tsconfig.json", "jsconfig.json", "package.json"} {
				if info, err := os.Stat(filepath.Join(root, marker)); err == nil && !info.IsDir() {
					return true
				}
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if discovery.ShouldSkipDirName(d.Name()) && path != root {
						return filepath.SkipDir
					}
					if name := d.Name(); name == ".next" || name == "node_modules" {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if isTsExtension(filepath.Ext(path)) {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
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
Rust decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .rs file: code_navigate capability=file_outline path=<file.rs>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file.rs>
- Trait impls across the crate: code_navigate capability=implementations pattern=<trait>
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			if info, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil && !info.IsDir() {
				return true
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					name := d.Name()
					if discovery.ShouldSkipDirName(name) && path != root {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if strings.EqualFold(filepath.Ext(path), ".rs") {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
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
Java decision guide - prefer code_navigate capabilities over raw shell/grep:
- Orient a .java file: code_navigate capability=file_outline path=<file.java>
- Find a symbol anywhere: code_navigate capability=workspace_symbol pattern=<name>
- Jump to a definition: code_navigate capability=definition pattern=<name> path=<scope>
- Who calls this / who uses this: code_navigate capability=references or capability=calls_into
- What does this call: code_navigate capability=calls_from
- Read one symbol body: code_navigate capability=read_symbol pattern=<name> path=<file.java>
- Interface implementers: code_navigate capability=implementations pattern=<name>
A narrow read_file range can supply exact context when semantic routes do not land.
`),
		Detect: func(root string) bool {
			for _, manifest := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"} {
				if info, err := os.Stat(filepath.Join(root, manifest)); err == nil && !info.IsDir() {
					return true
				}
			}
			found := false
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if discovery.ShouldSkipDirName(d.Name()) && path != root {
						return filepath.SkipDir
					}
					switch d.Name() {
					case "target", "build", ".gradle", ".mvn":
						if path != root {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if strings.HasPrefix(filepath.Base(path), ".") {
					return nil
				}
				if strings.EqualFold(filepath.Ext(path), ".java") {
					found = true
					return filepath.SkipAll
				}
				return nil
			})
			return found
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
	switch name {
	case "golang":
		name = "go"
	case "c", "cc", "cxx", "c++":
		name = "cpp"
	case "py":
		name = "python"
	case "js", "javascript", "jsx", "ts":
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
// against the workspace root. Used at session start to decide which
// decision guides are even worth tracking.
func (m *Manager) ActiveLanguages() []LanguageSupport {
	if !m.Enabled() {
		return nil
	}
	root := m.cfg.Root
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
