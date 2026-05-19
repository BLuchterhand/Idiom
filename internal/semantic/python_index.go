package semantic

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/discovery"
	"idiom/internal/util"
)

// PythonIndex is the tree-sitter-style index for a Python workspace. It
// mirrors CppIndex: a pre-parsed list of files with their symbols and
// import graph. The parser is line-oriented (regex + brace/indent
// tracking) rather than a full AST, matching the cpp_index.go approach.
// That keeps the package pure-Go with no cgo dependency; a future pass
// can swap in go-tree-sitter/python without changing callers.
type PythonIndex struct {
	Root    string
	Files   []PythonFile
	Forward map[string][]string // file -> imported module files
	Reverse map[string][]string // file -> files that import it
}

type PythonFile struct {
	Path       string
	Imports    []string // resolved relative paths when possible; else raw module names
	RawImports []string // raw module strings as seen in source (for UI)
	Symbols    []PythonSymbol
	Funcs      int
	Methods    int
	Classes    int
	HasMain    bool
	IsTest     bool
	IsInit     bool
	SourceDir  string
}

type PythonSymbol struct {
	Name       string
	Qualified  string
	Kind       string // "function", "method", "class", "async_function", "async_method"
	Scope      string
	Line       int
	Column     int
	StartLine  int
	EndLine    int
	Calls      []PythonCall
	SourceFile string
	Indent     int
}

type PythonCall struct {
	Name string
	Line int
}

var (
	pythonImportRe        = regexp.MustCompile(`^\s*import\s+(.+)`)
	pythonFromImportRe    = regexp.MustCompile(`^\s*from\s+(\.*[A-Za-z_][\w\.]*|\.+)\s+import\s+(.+)`)
	pythonDynamicImportRe = regexp.MustCompile(`\b(?:importlib\.import_module|__import__)\s*\(\s*["']([^"']+)["']`)
	pythonFuncRe          = regexp.MustCompile(`^(\s*)(async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)
	pythonClassRe         = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)\s*(?:\(|:)`)
	pythonCallRe          = regexp.MustCompile(`\b([A-Za-z_][\w]*)\s*\(`)
)

var pythonFileExtensions = map[string]struct{}{
	".py":  {},
	".pyi": {},
}

// BuildPythonIndex walks root, parses every .py / .pyi file, and returns
// the populated index. Returns (nil, nil) if no Python files are found.
func BuildPythonIndex(root string) (*PythonIndex, error) {
	return BuildPythonIndexWithOptions(root, IndexOptions{})
}

func BuildPythonIndexWithOptions(root string, opts IndexOptions) (*PythonIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	scope := cleanIndexScope(opts.Scope)

	index := &PythonIndex{
		Root:    root,
		Forward: make(map[string][]string),
		Reverse: make(map[string][]string),
	}
	byModule := make(map[string]string) // dotted module path -> relative file
	byRel := make(map[string]string)
	paths := make([]string, 0, 64)

	errStop := filepath.SkipAll
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if err := indexContextErr(opts); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if !shouldVisitIndexDir(root, path, scope) {
				return filepath.SkipDir
			}
			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}
			if _, skip := pythonSkippedDirNames[d.Name()]; skip && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isPythonPath(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) {
			return nil
		}
		if !pathInIndexScope(rel, scope) {
			return nil
		}
		paths = append(paths, rel)
		byRel[rel] = rel
		for _, module := range pythonModuleNamesFor(rel) {
			if _, exists := byModule[module]; !exists {
				byModule[module] = rel
			}
		}
		if opts.MaxFiles > 0 && len(paths) >= opts.MaxFiles {
			return errStop
		}
		return nil
	})
	if err == errStop {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}

	slices.Sort(paths)
	for _, rel := range paths {
		if err := indexContextErr(opts); err != nil {
			return nil, err
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		contentBytes, readErr := os.ReadFile(full)
		if readErr != nil {
			continue
		}
		content := string(contentBytes)
		rawImports, resolved := parsePythonImports(rel, content, byModule, byRel)
		symbols := parsePythonSymbols(rel, content)
		file := PythonFile{
			Path:       rel,
			Imports:    resolved,
			RawImports: rawImports,
			Symbols:    symbols,
			IsTest:     looksLikePythonTestPath(rel),
			IsInit:     filepath.Base(rel) == "__init__.py",
			SourceDir:  topLevelDir(rel),
		}
		for _, symbol := range symbols {
			switch symbol.Kind {
			case "function", "async_function":
				file.Funcs++
			case "method", "async_method":
				file.Methods++
			case "class":
				file.Classes++
			}
		}
		file.HasMain = pythonHasMainBlock(content)
		index.Files = append(index.Files, file)
	}

	slices.SortFunc(index.Files, func(a, b PythonFile) int {
		return cmp.Compare(a.Path, b.Path)
	})
	for _, file := range index.Files {
		for _, dep := range file.Imports {
			if dep == "" {
				continue
			}
			index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], dep)
			index.Reverse[dep] = util.AppendUnique(index.Reverse[dep], file.Path)
		}
	}
	return index, nil
}

var pythonSkippedDirNames = map[string]struct{}{
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	".mypy_cache":   {},
	".pytest_cache": {},
	".tox":          {},
	"site-packages": {},
	".eggs":         {},
}

// Summary produces a short, human-readable overview of the index.
func (idx *PythonIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Indexed ")
	b.WriteString(intString(len(idx.Files)))
	b.WriteString(" Python files\n")
	entrypoints := idx.DefaultFiles(4)
	if len(entrypoints) > 0 {
		b.WriteString("Likely anchors:\n")
		for _, path := range entrypoints {
			file := idx.FileByPath(path)
			if file == nil {
				continue
			}
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString(" (")
			b.WriteString(intString(file.Funcs + file.Methods))
			b.WriteString(" funcs/methods, ")
			b.WriteString(intString(file.Classes))
			b.WriteString(" classes, ")
			b.WriteString(intString(len(file.Imports)))
			b.WriteString(" imports)\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// DefaultFiles ranks files by a "likely anchor" heuristic: files with
// __main__ blocks, top-level src/ directories, and dense symbol counts
// float up; tests and __init__.py shims sink.
func (idx *PythonIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file PythonFile) int {
		score := 0
		base := filepath.Base(file.Path)
		if file.HasMain {
			score += 100
		}
		if base == "main.py" || base == "__main__.py" || base == "app.py" || base == "cli.py" {
			score += 60
		}
		score += pythonSourceDirPriority(file.SourceDir)
		if file.IsInit {
			score -= 25
		}
		if file.IsTest {
			score -= 30
		}
		score += min(file.Funcs+file.Methods*2, 24)
		score += min(file.Classes*2, 16)
		return score
	}, limit), limit)
}

// RelevantFiles ranks files against a free-text set of terms.
func (idx *PythonIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := normalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}
	return idx.uniqueRankedPaths(idx.rankFiles(func(file PythonFile) int {
		score := 0
		pathLower := strings.ToLower(file.Path)
		for _, term := range normalized {
			if strings.Contains(pathLower, term) {
				score += 16
			}
			for _, imp := range file.RawImports {
				if strings.Contains(strings.ToLower(imp), term) {
					score += 6
				}
			}
			for _, symbol := range file.Symbols {
				if strings.Contains(strings.ToLower(symbol.Name), term) || strings.Contains(strings.ToLower(symbol.Qualified), term) {
					score += 10
				}
			}
		}
		score += pythonSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 20
		}
		return score
	}, limit), limit)
}

// FileByPath returns a pointer to the indexed file at rel, or nil.
func (idx *PythonIndex) FileByPath(path string) *PythonFile {
	if idx == nil {
		return nil
	}
	path = normalizeFileLookupPath(path)
	for i := range idx.Files {
		if normalizeFileLookupPath(idx.Files[i].Path) == path {
			return &idx.Files[i]
		}
	}
	return nil
}

func (idx *PythonIndex) findFiles(pattern string) []string {
	if idx == nil {
		return nil
	}
	return FindFilesExactPartial(idx.Files, pattern)
}

func (idx *PythonIndex) rankFiles(scoreFn func(PythonFile) int, limit int) []PythonFile {
	scored := make([]struct {
		file  PythonFile
		score int
	}, 0, len(idx.Files))
	for _, file := range idx.Files {
		score := scoreFn(file)
		if score <= 0 {
			continue
		}
		scored = append(scored, struct {
			file  PythonFile
			score int
		}{file: file, score: score})
	}
	slices.SortFunc(scored, func(a, b struct {
		file  PythonFile
		score int
	}) int {
		if c := -cmp.Compare(a.score, b.score); c != 0 {
			return c
		}
		return cmp.Compare(a.file.Path, b.file.Path)
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]PythonFile, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.file)
	}
	return out
}

func (idx *PythonIndex) uniqueRankedPaths(files []PythonFile, limit int) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(files))
	for _, file := range files {
		if _, ok := seen[file.Path]; ok {
			continue
		}
		seen[file.Path] = struct{}{}
		out = append(out, file.Path)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// parsePythonImports extracts `import X` and `from X import Y` lines and
// attempts to resolve each module to an indexed file path.
func parsePythonImports(rel string, content string, byModule map[string]string, byRel map[string]string) ([]string, []string) {
	lines := strings.Split(content, "\n")
	raw := make([]string, 0, 8)
	resolved := make([]string, 0, 8)
	seenRaw := make(map[string]struct{})
	seenRes := make(map[string]struct{})
	addImport := func(module string) {
		module = strings.TrimSpace(module)
		if module == "" {
			return
		}
		if _, ok := seenRaw[module]; !ok {
			seenRaw[module] = struct{}{}
			raw = append(raw, module)
		}
		if target := resolvePythonModule(rel, module, byModule, byRel); target != "" {
			if _, ok := seenRes[target]; !ok {
				seenRes[target] = struct{}{}
				resolved = append(resolved, target)
			}
		}
	}
	for _, line := range lines {
		clean := stripPythonComment(line)
		if match := pythonImportRe.FindStringSubmatch(clean); len(match) >= 2 {
			for _, module := range parsePythonImportNames(match[1]) {
				addImport(module)
			}
			continue
		}
		if match := pythonFromImportRe.FindStringSubmatch(clean); len(match) >= 2 {
			module := strings.TrimSpace(match[1])
			addImport(module)
			if len(match) >= 3 {
				for _, imported := range parsePythonImportNames(match[2]) {
					if imported == "*" {
						continue
					}
					addImport(joinPythonImportedModule(module, imported))
				}
			}
		}
		for _, match := range pythonDynamicImportRe.FindAllStringSubmatch(clean, -1) {
			if len(match) >= 2 {
				addImport(match[1])
			}
		}
	}
	slices.Sort(raw)
	slices.Sort(resolved)
	return raw, resolved
}

func parsePythonImportNames(spec string) []string {
	spec = strings.TrimSpace(spec)
	spec = strings.TrimPrefix(spec, "(")
	spec = strings.TrimSuffix(spec, ")")
	if spec == "" {
		return nil
	}
	values := strings.Split(spec, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if idx := strings.Index(value, " as "); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		value = strings.Trim(value, " \t()")
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func joinPythonImportedModule(module string, imported string) string {
	module = strings.TrimSpace(module)
	imported = strings.TrimSpace(imported)
	if module == "" || imported == "" {
		return module
	}
	if strings.HasSuffix(module, ".") {
		return module + imported
	}
	return module + "." + imported
}

// resolvePythonModule tries to map an `import foo.bar` or `from
// .sibling` reference to a workspace-relative file path. Returns ""
// when the target is outside the workspace (stdlib / third-party).
func resolvePythonModule(sourceRel string, module string, byModule map[string]string, byRel map[string]string) string {
	module = strings.TrimSpace(module)
	if module == "" {
		return ""
	}
	// Relative: strip leading dots and resolve from source file's package.
	if strings.HasPrefix(module, ".") {
		leading := 0
		for leading < len(module) && module[leading] == '.' {
			leading++
		}
		trailing := module[leading:]
		dir := filepath.ToSlash(filepath.Dir(sourceRel))
		for i := 1; i < leading; i++ {
			dir = filepath.ToSlash(filepath.Dir(dir))
		}
		if dir == "." {
			dir = ""
		}
		parts := []string{}
		if dir != "" {
			parts = append(parts, strings.Split(dir, "/")...)
		}
		if trailing != "" {
			parts = append(parts, strings.Split(trailing, ".")...)
		}
		if len(parts) == 0 {
			return ""
		}
		joined := strings.Join(parts, "/")
		candidates := []string{
			joined + ".py",
			joined + "/__init__.py",
		}
		for _, c := range candidates {
			if _, ok := byRel[c]; ok {
				return c
			}
		}
		return ""
	}
	// Absolute dotted name: check byModule.
	if rel, ok := byModule[module]; ok {
		return rel
	}
	// Try prefix: `from package.sub import name` — the package.sub module
	// may be the interesting target even if its full dotted path isn't
	// registered directly.
	parts := strings.Split(module, ".")
	for len(parts) > 0 {
		key := strings.Join(parts, ".")
		if rel, ok := byModule[key]; ok {
			return rel
		}
		parts = parts[:len(parts)-1]
	}
	return ""
}

// pythonModuleNamesFor returns plausible dotted module names for a file
// path, trying both package-rooted ("foo.bar.baz") and src-rooted
// ("foo.bar.baz" when the path is "src/foo/bar/baz.py") variants.
func pythonModuleNamesFor(rel string) []string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return nil
	}
	parts := strings.Split(rel, "/")
	base := parts[len(parts)-1]
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	parts[len(parts)-1] = stem
	names := make([]string, 0, 4)
	add := func(segments []string) {
		segs := append([]string{}, segments...)
		if len(segs) == 0 {
			return
		}
		// Drop trailing "__init__" so `package/__init__.py` registers as "package".
		if segs[len(segs)-1] == "__init__" {
			segs = segs[:len(segs)-1]
		}
		if len(segs) == 0 {
			return
		}
		names = append(names, strings.Join(segs, "."))
	}
	add(parts)
	// If the path starts with a common src root, also register without it.
	for _, srcRoot := range []string{"src", "lib"} {
		if len(parts) > 1 && parts[0] == srcRoot {
			add(parts[1:])
		}
	}
	return names
}

func parsePythonSymbols(rel string, content string) []PythonSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]PythonSymbol, 0, 16)

	// Stack of (indent, qualified_name) for class nesting.
	type scope struct {
		indent    int
		qualified string
	}
	stack := make([]scope, 0, 4)

	popTo := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
	}

	for i, rawLine := range lines {
		lineNo := i + 1
		if strings.TrimSpace(rawLine) == "" {
			continue
		}
		indent := pythonIndentWidth(rawLine)

		if match := pythonClassRe.FindStringSubmatch(rawLine); len(match) == 3 {
			popTo(indent)
			name := match[2]
			qualified := name
			scopePath := ""
			if len(stack) > 0 {
				scopePath = stack[len(stack)-1].qualified
				qualified = scopePath + "." + name
			}
			end := pythonBlockEnd(lines, i, indent)
			symbols = append(symbols, PythonSymbol{
				Name:       name,
				Qualified:  qualified,
				Kind:       "class",
				Scope:      scopePath,
				Line:       lineNo,
				Column:     indent + 1,
				StartLine:  lineNo,
				EndLine:    end,
				SourceFile: rel,
				Indent:     indent,
			})
			stack = append(stack, scope{indent: indent, qualified: qualified})
			continue
		}

		if match := pythonFuncRe.FindStringSubmatch(rawLine); len(match) == 4 {
			popTo(indent)
			async := strings.TrimSpace(match[2]) != ""
			name := match[3]
			scopePath := ""
			if len(stack) > 0 {
				scopePath = stack[len(stack)-1].qualified
			}
			kind := "function"
			if scopePath != "" {
				kind = "method"
			}
			if async {
				kind = "async_" + kind
			}
			qualified := name
			if scopePath != "" {
				qualified = scopePath + "." + name
			}
			end := pythonBlockEnd(lines, i, indent)
			calls := extractPythonCalls(lines, lineNo+1, end)
			symbols = append(symbols, PythonSymbol{
				Name:       name,
				Qualified:  qualified,
				Kind:       kind,
				Scope:      scopePath,
				Line:       lineNo,
				Column:     indent + 1,
				StartLine:  lineNo,
				EndLine:    end,
				Calls:      calls,
				SourceFile: rel,
				Indent:     indent,
			})
			continue
		}
	}

	slices.SortStableFunc(symbols, func(a, b PythonSymbol) int {
		return cmp.Or(
			cmp.Compare(a.SourceFile, b.SourceFile),
			cmp.Compare(a.Line, b.Line),
		)
	})
	return symbols
}

// pythonBlockEnd returns the 1-based line number of the last line
// belonging to the def/class block that starts at lines[startIdx] and
// has indentation `baseIndent`. The block ends at the last non-blank
// line whose indent is strictly greater than baseIndent (or strictly
// greater for the first body line).
func pythonBlockEnd(lines []string, startIdx int, baseIndent int) int {
	end := startIdx + 1 // at minimum, the def/class line itself
	inBody := false
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		indent := pythonIndentWidth(line)
		if indent <= baseIndent {
			break
		}
		inBody = true
		end = i + 1
	}
	if !inBody {
		return startIdx + 1
	}
	return end
}

// pythonIndentWidth counts leading spaces, treating a tab as one unit
// of indentation (consistent but approximate).
func pythonIndentWidth(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

func extractPythonCalls(lines []string, startLine, endLine int) []PythonCall {
	if startLine <= 0 || endLine < startLine || endLine > len(lines) {
		return nil
	}
	calls := make([]PythonCall, 0, 16)
	for line := startLine; line <= endLine; line++ {
		text := stripPythonComment(lines[line-1])
		for _, match := range pythonCallRe.FindAllStringSubmatch(text, -1) {
			if len(match) != 2 {
				continue
			}
			name := match[1]
			if isPythonKeywordName(name) {
				continue
			}
			calls = append(calls, PythonCall{Name: name, Line: line})
		}
	}
	return calls
}

func stripPythonComment(line string) string {
	// Walk the line char by char, tracking whether we're in a string.
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && (inSingle || inDouble) && i+1 < len(line):
			i++ // skip escaped char
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble:
			return line[:i]
		}
	}
	return line
}

func pythonHasMainBlock(content string) bool {
	return strings.Contains(content, `if __name__ == "__main__"`) ||
		strings.Contains(content, `if __name__ == '__main__'`)
}

func isPythonPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := pythonFileExtensions[ext]
	return ok
}

// IsPythonExtension reports whether ext (with leading dot) is a Python
// source extension. Exported mirror of isCppExtension's role.
func IsPythonExtension(ext string) bool {
	_, ok := pythonFileExtensions[strings.ToLower(strings.TrimSpace(ext))]
	return ok
}

func looksLikePythonTestPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py" {
		return true
	}
	return strings.Contains(p, "/tests/") || strings.Contains(p, "/test/")
}

func pythonSourceDirPriority(dir string) int {
	switch dir {
	case "src":
		return 30
	case "app":
		return 28
	case "lib":
		return 20
	case ".":
		return 14
	case "tests", "test":
		return -10
	default:
		return 16
	}
}

func isPythonKeywordName(name string) bool {
	switch name {
	case "if", "for", "while", "return", "yield", "raise", "except",
		"print", "len", "range", "isinstance", "issubclass", "str",
		"int", "float", "list", "dict", "set", "tuple", "bool",
		"type", "super", "hasattr", "getattr", "setattr", "delattr",
		"any", "all", "sum", "min", "max", "sorted", "reversed",
		"map", "filter", "zip", "enumerate", "open", "iter", "next",
		"repr", "id", "abs", "round", "divmod", "format":
		return true
	default:
		return false
	}
}
