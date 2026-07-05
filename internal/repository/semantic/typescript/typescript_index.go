package typescript

// TypeScript/JavaScript semantic index. Mirrors the structure of
// cpp_index.go: regex-driven "tree-sitter-style" parsing that pulls
// imports, exports, type declarations, functions, classes, methods, and
// call sites out of .ts/.tsx/.js/.jsx/.mjs/.cjs files. The grammar is
// deliberately shallow; it exists to answer orientation questions fast
// and to give the LSP path (tsserver_lsp.go) a semantic fallback when
// the language server is not installed.

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/platform/textutil"
	"idiom/internal/repository/semantic/internal/indexsupport"

	"idiom/internal/repository/discovery"
)

type TsIndex struct {
	Root    string
	Files   []TsFile
	Forward map[string][]string
	Reverse map[string][]string
}

type TsFile struct {
	Path       string
	Imports    []string
	Symbols    []TsSymbol
	Funcs      int
	Methods    int
	Types      int
	Classes    int
	HasMain    bool
	IsTest     bool
	IsDeclFile bool
	IsTSX      bool
	IsJS       bool
	SourceDir  string
}

type TsSymbol struct {
	Name       string
	Qualified  string
	Kind       string
	Scope      string
	Line       int
	Column     int
	StartLine  int
	EndLine    int
	Exported   bool
	Calls      []TsCall
	SourceFile string
}

type TsCall struct {
	Name string
	Line int
}

var (
	tsImportFromRe = regexp.MustCompile(`(?:^|\s)(?:import|export)\b[^;]*?from\s+['"]([^'"]+)['"]`)
	tsImportSideRe = regexp.MustCompile(`(?:^|\s)import\s+['"]([^'"]+)['"]`)
	tsRequireRe    = regexp.MustCompile(`\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	tsDynImportRe  = regexp.MustCompile(`\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	tsClassRe      = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsInterfaceRe  = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsTypeAliasRe  = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>]*>)?\s*=`)
	tsEnumRe       = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsNamespaceRe  = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?(?:namespace|module)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsFunctionRe   = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsArrowRe      = regexp.MustCompile(`^(?:\s*export\s+)?(?:default\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=>`)
	tsDeclConstRe  = regexp.MustCompile(`^(?:\s*export\s+)?(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^;]+)?;?\s*$`)
	tsMethodRe     = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|readonly\s+|async\s+|override\s+|abstract\s+)*(?:get\s+|set\s+)?([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	tsCallRe       = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
)

var tsFileExtensions = map[string]struct{}{
	".ts":  {},
	".tsx": {},
	".mts": {},
	".cts": {},
	".js":  {},
	".jsx": {},
	".mjs": {},
	".cjs": {},
}

var tsDeclExtensions = map[string]struct{}{
	".d.ts":  {},
	".d.mts": {},
	".d.cts": {},
}

var tsSkippedDirs = map[string]struct{}{
	"node_modules": {},
	".next":        {},
	"dist":         {},
	"build":        {},
	"out":          {},
	"coverage":     {},
	".turbo":       {},
	".cache":       {},
	".git":         {},
	".svn":         {},
	".hg":          {},
}

func BuildTsIndex(root string) (*TsIndex, error) {
	return BuildTsIndexWithOptions(root, indexsupport.IndexOptions{})
}

func BuildTsIndexWithOptions(root string, opts indexsupport.IndexOptions) (*TsIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}

	scope := indexsupport.CleanIndexScope(opts.Scope)

	index := &TsIndex{
		Root:    root,
		Forward: make(map[string][]string),
		Reverse: make(map[string][]string),
	}
	byName := make(map[string]string)
	byRel := make(map[string]string)
	paths := make([]string, 0, 64)

	errStop := filepath.SkipAll
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if err := indexsupport.ContextErr(opts); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if !indexsupport.ShouldVisitDir(root, path, scope) {
				return filepath.SkipDir
			}

			if _, skip := tsSkippedDirs[d.Name()]; skip && path != root {
				return filepath.SkipDir
			}

			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		if !isTsPath(path) {
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

		if !indexsupport.PathInScope(rel, scope) {
			return nil
		}

		paths = append(paths, rel)
		byName[filepath.Base(rel)] = rel
		byRel[rel] = rel

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
		if err := indexsupport.ContextErr(opts); err != nil {
			return nil, err
		}

		full := filepath.Join(root, filepath.FromSlash(rel))

		contentBytes, err := os.ReadFile(full)
		if err != nil {
			continue
		}

		content := string(contentBytes)
		imports := parseTsImports(rel, content, byName, byRel)
		symbols := parseTsSymbols(rel, content)
		file := TsFile{
			Path:       rel,
			Imports:    imports,
			Symbols:    symbols,
			IsTest:     looksLikeTsTestPath(rel),
			IsDeclFile: isTsDeclPath(rel),
			IsTSX:      strings.HasSuffix(strings.ToLower(rel), ".tsx") || strings.HasSuffix(strings.ToLower(rel), ".jsx"),
			IsJS:       isTsJavaScriptPath(rel),
			SourceDir:  indexsupport.TopLevelDir(rel),
		}

		for _, symbol := range symbols {
			switch symbol.Kind {
			case "function":
				file.Funcs++
			case "method":
				file.Methods++
			case "class":
				file.Classes++

				file.Types++
			case "interface", "type", "enum", "namespace":
				file.Types++
			}

			if symbol.Name == "main" || symbol.Name == "run" || symbol.Name == "start" {
				file.HasMain = true
			}
		}

		index.Files = append(index.Files, file)
	}

	slices.SortFunc(index.Files, func(a, b TsFile) int {
		return cmp.Compare(a.Path, b.Path)
	})

	for _, file := range index.Files {
		if len(file.Imports) == 0 {
			continue
		}

		for _, imp := range file.Imports {
			index.Forward[file.Path] = textutil.AppendUnique(index.Forward[file.Path], imp)
			index.Reverse[imp] = textutil.AppendUnique(index.Reverse[imp], file.Path)
		}
	}

	return index, nil
}

func (idx *TsIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}

	return indexsupport.FormatIndexSummary(len(idx.Files), "TypeScript/JavaScript", idx.DefaultFiles(4), func(path string) (string, bool) {
		file := idx.FileByPath(path)
		if file == nil {
			return "", false
		}

		return strconv.Itoa(file.Funcs+file.Methods) + " funcs/methods, " +
			strconv.Itoa(file.Types) + " types, " +
			strconv.Itoa(len(file.Imports)) + " imports", true
	})
}

func (idx *TsIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file TsFile) int {
		score := 0
		if file.HasMain {
			score += 100
		}

		score += tsSourceDirPriority(file.SourceDir)
		if file.IsDeclFile {
			score -= 20
		}

		if file.IsTest {
			score -= 30
		}

		score += min(file.Funcs+file.Methods*2, 24)
		score += min(file.Types*2, 16)

		base := strings.ToLower(filepath.Base(file.Path))

		if base == "index.ts" || base == "index.tsx" || base == "index.js" || base == "index.jsx" || base == "main.ts" || base == "main.tsx" || base == "main.js" {
			score += 18
		}

		return score
	}, limit), limit)
}

func (idx *TsIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := indexsupport.NormalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}

	return idx.uniqueRankedPaths(idx.rankFiles(func(file TsFile) int {
		return indexsupport.RelevantFileScore(normalized, file.Path, file.Imports, tsSymbolSearchTerms(file.Symbols), tsSourceDirPriority(file.SourceDir), file.IsTest)
	}, limit), limit)
}

func (idx *TsIndex) FileByPath(path string) *TsFile {
	if idx == nil {
		return nil
	}

	path = indexsupport.NormalizeLookupPath(path)
	for i := range idx.Files {
		if indexsupport.NormalizeLookupPath(idx.Files[i].Path) == path {
			return &idx.Files[i]
		}
	}

	return nil
}

func (idx *TsIndex) rankFiles(scoreFn func(TsFile) int, limit int) []TsFile {
	return indexsupport.RankFiles(idx.Files, func(file TsFile) string { return file.Path }, scoreFn, limit)
}

func (idx *TsIndex) uniqueRankedPaths(files []TsFile, limit int) []string {
	return indexsupport.UniqueRankedPaths(files, func(file TsFile) string { return file.Path }, limit)
}

func tsSymbolSearchTerms(symbols []TsSymbol) []string {
	terms := make([]string, 0, len(symbols)*2)
	for _, symbol := range symbols {
		terms = append(terms, symbol.Name, symbol.Qualified)
	}

	return terms
}

// parseTsImports extracts module specifiers from import/export/require/dynamic
// import forms, resolves relative specifiers against the source file's
// directory, and falls back to a basename lookup for bare imports.
func parseTsImports(rel string, content string, byName map[string]string, byRel map[string]string) []string {
	// Strip comments cheaply so regexes don't fire inside them.
	stripped := stripTsComments(content)
	rawMatches := make([]string, 0, 8)

	for _, re := range []*regexp.Regexp{tsImportFromRe, tsImportSideRe, tsRequireRe, tsDynImportRe} {
		for _, match := range re.FindAllStringSubmatch(stripped, -1) {
			if len(match) < 2 {
				continue
			}

			rawMatches = append(rawMatches, match[1])
		}
	}

	includes := make([]string, 0, len(rawMatches))
	for _, spec := range rawMatches {
		resolved := resolveTsImport(rel, spec, byName, byRel)
		if resolved == "" {
			resolved = filepath.ToSlash(strings.TrimSpace(spec))
		}

		includes = textutil.AppendUnique(includes, resolved)
	}

	slices.Sort(includes)

	return includes
}

// resolveTsImport resolves a specifier like './foo' or '../bar/baz' against
// the workspace index. TS allows extension-less imports, so we try several
// extensions including /index.ts variants.
func resolveTsImport(sourceRel string, spec string, byName map[string]string, byRel map[string]string) string {
	spec = filepath.ToSlash(strings.TrimSpace(spec))
	if spec == "" {
		return ""
	}

	if !strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "/") {
		// Bare specifier — probably a package import. Fall through to basename
		// lookup which will almost always fail for external packages; that's
		// fine — we want external imports to pass through as-is.
		if rel, ok := byName[filepath.Base(spec)]; ok {
			return rel
		}

		return ""
	}

	sourceDir := filepath.ToSlash(filepath.Dir(sourceRel))
	base := filepath.ToSlash(filepath.Clean(sourceDir + "/" + spec))
	candidates := []string{
		base,
		base + ".ts",
		base + ".tsx",
		base + ".mts",
		base + ".cts",
		base + ".js",
		base + ".jsx",
		base + ".mjs",
		base + ".cjs",
		base + "/index.ts",
		base + "/index.tsx",
		base + "/index.js",
		base + "/index.jsx",
	}

	for _, candidate := range candidates {
		if _, ok := byRel[candidate]; ok {
			return candidate
		}
	}

	return ""
}

// parseTsSymbols extracts top-level declarations and class methods. Bodies
// of functions and methods are scanned for call sites.
func parseTsSymbols(rel string, content string) []TsSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]TsSymbol, 0, 16)
	classStack := make([]classFrame, 0, 4)

	for i, line := range lines {
		lineNo := i + 1
		clean := stripTsLineComment(line)
		trimmed := strings.TrimSpace(clean)

		if trimmed == "" {
			// still need to track brace depth so methods close out correctly
			popClassFrames(&classStack, line, lineNo)

			continue
		}

		inClass := len(classStack) > 0

		// Top-level type-ish declarations. These are recognized even inside a
		// namespace; we don't try to nest namespace scope into qualified
		// names — the LSP handles that.
		if match := tsInterfaceRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "interface",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsTypeAliasRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "type",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: lineNo,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsEnumRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "enum",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsNamespaceRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "namespace",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsClassRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "class",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
			classStack = append(classStack, classFrame{name: match[1], startLine: lineNo, depth: tsBraceDelta(line)})

			continue
		} else if match := tsFunctionRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)
			calls := extractTsCalls(lines, lineNo, end)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "function",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end, Calls: calls,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsArrowRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass {
			end := findTsBraceEnd(lines, i)
			if end < lineNo {
				end = lineNo
			}

			calls := extractTsCalls(lines, lineNo, end)

			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "function",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: end, Calls: calls,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if match := tsDeclConstRe.FindStringSubmatch(trimmed); len(match) == 2 && !inClass && (isTsDeclPath(rel) || strings.Contains(trimmed, "declare")) {
			symbols = append(symbols, TsSymbol{
				Name: match[1], Qualified: match[1], Kind: "const",
				Line: lineNo, Column: indexOfIdent(line, match[1]) + 1,
				StartLine: lineNo, EndLine: lineNo,
				Exported: strings.Contains(trimmed, "export"), SourceFile: rel,
			})
		} else if inClass {
			// Method declaration inside a class body.
			if match := tsMethodRe.FindStringSubmatch(trimmed); len(match) == 2 {
				name := match[1]
				if !isTsMethodKeyword(name) && !isTsJSBuiltinKeyword(name) {
					end := findTsBraceEnd(lines, i)
					calls := extractTsCalls(lines, lineNo, end)
					class := classStack[len(classStack)-1]
					qualified := class.name + "." + name

					symbols = append(symbols, TsSymbol{
						Name: name, Qualified: qualified, Kind: "method",
						Scope: class.name, Line: lineNo,
						Column:    indexOfIdent(line, name) + 1,
						StartLine: lineNo, EndLine: end, Calls: calls,
						SourceFile: rel,
					})
				}
			}
		}

		// Update class-frame depth after the heuristic above so we don't
		// pop the class we just opened.
		popClassFrames(&classStack, line, lineNo)
	}

	slices.SortFunc(symbols, func(a, b TsSymbol) int {
		if c := cmp.Compare(a.SourceFile, b.SourceFile); c != 0 {
			return c
		}

		return cmp.Compare(a.Line, b.Line)
	})

	return symbols
}

type classFrame struct {
	name      string
	startLine int
	depth     int
}

func popClassFrames(stack *[]classFrame, line string, lineNo int) {
	delta := tsBraceDelta(stripTsLineComment(line))
	if len(*stack) > 0 && delta != 0 {
		top := &(*stack)[len(*stack)-1]

		top.depth += delta

		if top.depth <= 0 {
			*stack = (*stack)[:len(*stack)-1]
		}
	}
}

func extractTsCalls(lines []string, startLine int, endLine int) []TsCall {
	return indexsupport.ExtractLineCalls(lines, startLine, endLine, tsCallRe, stripTsLineComment, isTsJSBuiltinKeyword, func(name string, line int) TsCall {
		return TsCall{Name: name, Line: line}
	})
}

func findTsBraceEnd(lines []string, startIdx int) int {
	depth := 0
	seen := false

	for i := startIdx; i < len(lines); i++ {
		line := stripTsLineComment(lines[i])
		for _, r := range line {
			switch r {
			case '{':
				depth++

				seen = true
			case '}':
				if seen {
					depth--
					if depth <= 0 {
						return i + 1
					}
				}
			}
		}
	}

	return startIdx + 1
}

func tsBraceDelta(line string) int {
	delta := 0

	for _, r := range line {
		switch r {
		case '{':
			delta++
		case '}':
			delta--
		}
	}

	return delta
}

func stripTsLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}

	return line
}

// stripTsComments is a very cheap block-comment stripper used only before
// the import scan. It's not a full JS tokenizer; strings that contain /*
// will be clobbered, but those are astronomically rare in real code.
func stripTsComments(content string) string {
	var b strings.Builder

	b.Grow(len(content))

	i := 0
	for i < len(content) {
		if i+1 < len(content) && content[i] == '/' && content[i+1] == '*' {
			end := strings.Index(content[i+2:], "*/")
			if end < 0 {
				break
			}

			i += end + 4

			continue
		}

		if i+1 < len(content) && content[i] == '/' && content[i+1] == '/' {
			end := strings.Index(content[i:], "\n")
			if end < 0 {
				break
			}

			i += end

			continue
		}

		b.WriteByte(content[i])

		i++
	}

	return b.String()
}

func indexOfIdent(line string, name string) int {
	if idx := strings.Index(line, name); idx >= 0 {
		return idx
	}

	return 0
}

func isTsPath(path string) bool {
	lower := strings.ToLower(path)
	for ext := range tsDeclExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	ext := strings.ToLower(filepath.Ext(path))
	_, ok := tsFileExtensions[ext]

	return ok
}

func isTsDeclPath(path string) bool {
	lower := strings.ToLower(path)
	for ext := range tsDeclExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	return false
}

func isTsJavaScriptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func looksLikeTsTestPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)

	if strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") {
		return true
	}

	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func tsSourceDirPriority(dir string) int {
	switch dir {
	case "src":
		return 30
	case "lib":
		return 26
	case "app":
		return 26
	case "packages":
		return 22
	case "server":
		return 20
	case "client":
		return 20
	case ".":
		return 10
	default:
		return 16
	}
}

func isTsMethodKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "typeof", "catch", "throw", "new", "delete", "await", "yield", "function", "class", "interface", "type", "enum", "namespace", "import", "export", "const", "let", "var", "do", "else", "case":
		return true
	}

	return false
}

func isTsJSBuiltinKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "typeof", "catch", "throw", "new", "delete", "await", "yield", "function", "class", "do", "else", "case":
		return true
	}

	return false
}
