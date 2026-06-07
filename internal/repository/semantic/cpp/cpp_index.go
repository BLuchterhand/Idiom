package cpp

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/platform/util"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/internal/indexsupport"
)

type CppIndex struct {
	Root    string
	Files   []CppFile
	Forward map[string][]string
	Reverse map[string][]string
}

type CppFile struct {
	Path      string
	Includes  []string
	Symbols   []CppSymbol
	Funcs     int
	Methods   int
	Types     int
	HasMain   bool
	IsTest    bool
	IsHeader  bool
	SourceDir string
}

type CppSymbol struct {
	Name       string
	Qualified  string
	Kind       string
	Scope      string
	Line       int
	Column     int
	StartLine  int
	EndLine    int
	Calls      []CppCall
	HasBody    bool
	SourceFile string
}

type CppCall struct {
	Name string
	Line int
}

var (
	cppIncludeRe       = regexp.MustCompile(`^\s*#include\s+[<"]([^>"]+)`)
	cppTypeRe          = regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?(class|struct|enum(?:\s+class)?|union|namespace)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	cppNameBeforeParen = regexp.MustCompile(`([A-Za-z_~][A-Za-z0-9_:~]*)\s*$`)
	cppCallRe          = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^;{}()\n]*>)?\s*\(`)
)

var cppFileExtensions = map[string]struct{}{
	".c":   {},
	".cc":  {},
	".cpp": {},
	".cxx": {},
	".h":   {},
	".hh":  {},
	".hpp": {},
	".hxx": {},
	".ipp": {},
	".inl": {},
	".tpp": {},
}

var cppHeaderExtensions = map[string]struct{}{
	".h":   {},
	".hh":  {},
	".hpp": {},
	".hxx": {},
	".ipp": {},
	".inl": {},
	".tpp": {},
}

func BuildCppIndex(root string) (*CppIndex, error) {
	return BuildCppIndexWithOptions(root, indexsupport.IndexOptions{})
}

func BuildCppIndexWithOptions(root string, opts indexsupport.IndexOptions) (*CppIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	scope := indexsupport.CleanIndexScope(opts.Scope)

	index := &CppIndex{
		Root:    root,
		Forward: make(map[string][]string),
		Reverse: make(map[string][]string),
	}
	byName := make(map[string][]string)
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
			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isCppPath(path) {
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
		byName[filepath.Base(rel)] = util.AppendUnique(byName[filepath.Base(rel)], rel)
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
		includes := parseCppIncludes(rel, content, byName, byRel)
		symbols := parseCppSymbols(rel, content)
		file := CppFile{
			Path:      rel,
			Includes:  includes,
			Symbols:   symbols,
			IsTest:    looksLikeCppTestPath(rel),
			IsHeader:  isCppHeader(rel),
			SourceDir: indexsupport.TopLevelDir(rel),
		}
		for _, symbol := range symbols {
			switch symbol.Kind {
			case "func":
				file.Funcs++
			case "method":
				file.Methods++
			case "class", "struct", "enum", "union", "namespace":
				file.Types++
			}
			if symbol.Name == "main" {
				file.HasMain = true
			}
		}
		index.Files = append(index.Files, file)
	}

	slices.SortFunc(index.Files, func(a, b CppFile) int {
		return cmp.Compare(a.Path, b.Path)
	})
	for _, file := range index.Files {
		if len(file.Includes) == 0 {
			continue
		}
		for _, include := range file.Includes {
			index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], include)
			index.Reverse[include] = util.AppendUnique(index.Reverse[include], file.Path)
		}
	}
	return index, nil
}

func (idx *CppIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}
	return indexsupport.FormatIndexSummary(len(idx.Files), "C/C++", idx.DefaultFiles(4), func(path string) (string, bool) {
		file := idx.FileByPath(path)
		if file == nil {
			return "", false
		}
		return indexsupport.IntString(file.Funcs+file.Methods) + " funcs/methods, " +
			indexsupport.IntString(file.Types) + " types, " +
			indexsupport.IntString(len(file.Includes)) + " includes", true
	})
}

func (idx *CppIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file CppFile) int {
		score := 0
		if file.HasMain {
			score += 100
		}
		score += cppSourceDirPriority(file.SourceDir)
		if file.IsHeader {
			score -= 10
		}
		if file.IsTest {
			score -= 30
		}
		score += min(file.Funcs+file.Methods*2, 24)
		score += min(file.Types*2, 16)
		return score
	}, limit), limit)
}

func (idx *CppIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := indexsupport.NormalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}
	return idx.uniqueRankedPaths(idx.rankFiles(func(file CppFile) int {
		return indexsupport.RelevantFileScore(normalized, file.Path, file.Includes, cppSymbolSearchTerms(file.Symbols), cppSourceDirPriority(file.SourceDir), file.IsTest)
	}, limit), limit)
}

func (idx *CppIndex) FileByPath(path string) *CppFile {
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

func (idx *CppIndex) findFiles(pattern string) []string {
	if idx == nil {
		return nil
	}
	return indexsupport.FindFilesExactPartial(idx.Files, func(file CppFile) string { return file.Path }, pattern)
}

func (idx *CppIndex) rankFiles(scoreFn func(CppFile) int, limit int) []CppFile {
	return indexsupport.RankFiles(idx.Files, func(file CppFile) string { return file.Path }, scoreFn, limit)
}

func (idx *CppIndex) uniqueRankedPaths(files []CppFile, limit int) []string {
	return indexsupport.UniqueRankedPaths(files, func(file CppFile) string { return file.Path }, limit)
}

func cppSymbolSearchTerms(symbols []CppSymbol) []string {
	terms := make([]string, 0, len(symbols)*2)
	for _, symbol := range symbols {
		terms = append(terms, symbol.Name, symbol.Qualified)
	}
	return terms
}

func parseCppIncludes(rel string, content string, byName map[string][]string, byRel map[string]string) []string {
	lines := strings.Split(content, "\n")
	includes := make([]string, 0, 8)
	for _, line := range lines {
		match := cppIncludeRe.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		include := resolveCppInclude(rel, match[1], byName, byRel)
		if include == "" {
			include = filepath.ToSlash(strings.TrimSpace(match[1]))
		}
		includes = util.AppendUnique(includes, include)
	}
	slices.Sort(includes)
	return includes
}

func resolveCppInclude(sourceRel string, include string, byName map[string][]string, byRel map[string]string) string {
	include = filepath.ToSlash(strings.TrimSpace(include))
	if include == "" {
		return ""
	}
	sourceDir := filepath.ToSlash(filepath.Dir(sourceRel))
	if sourceDir != "." && sourceDir != "" {
		candidate := filepath.ToSlash(filepath.Clean(sourceDir + "/" + include))
		if _, ok := byRel[candidate]; ok {
			return candidate
		}
	}
	candidate := filepath.ToSlash(filepath.Clean(include))
	if _, ok := byRel[candidate]; ok {
		return candidate
	}
	base := filepath.Base(include)
	if matches := byName[base]; len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func parseCppSymbols(rel string, content string) []CppSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]CppSymbol, 0, 16)
	var pending []string
	pendingStart := 0
	parenDepth := 0
	for i, line := range lines {
		lineNo := i + 1
		clean := stripCppLineComment(line)
		trimmed := strings.TrimSpace(clean)
		if trimmed == "" {
			continue
		}

		if pendingStart == 0 {
			if match := cppTypeRe.FindStringSubmatch(trimmed); len(match) == 3 {
				end := lineNo
				if strings.Contains(trimmed, "{") {
					end = findCppBraceEnd(lines, i)
				}
				kind := strings.ReplaceAll(match[1], " ", "_")
				kind = strings.ReplaceAll(kind, "enum_class", "enum")
				symbols = append(symbols, CppSymbol{
					Name:       match[2],
					Qualified:  match[2],
					Kind:       kind,
					Line:       lineNo,
					Column:     1,
					StartLine:  lineNo,
					EndLine:    end,
					SourceFile: rel,
				})
			}
		}

		switch {
		case pendingStart == 0 && shouldStartCppSignature(trimmed):
			pending = []string{trimmed}
			pendingStart = lineNo
			parenDepth = cppParenBalance(trimmed)
		case pendingStart > 0:
			pending = append(pending, trimmed)
			parenDepth += cppParenBalance(trimmed)
		default:
			continue
		}

		ready := pendingStart > 0 && parenDepth <= 0 && (strings.Contains(trimmed, "{") || strings.HasSuffix(trimmed, ";"))
		if !ready {
			continue
		}
		signature := strings.Join(pending, " ")
		symbol, ok := parseCppFunctionSignature(signature)
		if ok {
			symbol.Line = pendingStart
			symbol.Column = 1
			symbol.StartLine = pendingStart
			symbol.EndLine = pendingStart
			symbol.SourceFile = rel
			symbol.HasBody = strings.Contains(signature, "{")
			if symbol.HasBody {
				symbol.EndLine = findCppBraceEnd(lines, i)
				symbol.Calls = filterCppDefinitionCalls(symbol, extractCppCalls(lines, symbol.StartLine, symbol.EndLine))
			}
			symbols = append(symbols, symbol)
		}
		pending = nil
		pendingStart = 0
		parenDepth = 0
	}

	slices.SortStableFunc(symbols, func(a, b CppSymbol) int {
		return cmp.Or(
			cmp.Compare(a.SourceFile, b.SourceFile),
			cmp.Compare(a.Line, b.Line),
		)
	})
	return symbols
}

func parseCppFunctionSignature(signature string) (CppSymbol, bool) {
	signature = strings.TrimSpace(signature)
	if signature == "" || strings.HasPrefix(signature, "#") {
		return CppSymbol{}, false
	}
	if idx := strings.Index(signature, "("); idx >= 0 {
		beforeParen := strings.TrimSpace(signature[:idx])
		match := cppNameBeforeParen.FindStringSubmatch(beforeParen)
		if len(match) != 2 {
			return CppSymbol{}, false
		}
		token := strings.TrimSpace(match[1])
		if token == "" {
			return CppSymbol{}, false
		}
		name := token
		scope := ""
		if strings.Contains(token, "::") {
			parts := strings.Split(token, "::")
			name = parts[len(parts)-1]
			scope = strings.Join(parts[:len(parts)-1], "::")
		}
		if isCppKeywordName(name) {
			return CppSymbol{}, false
		}
		kind := "func"
		if scope != "" {
			kind = "method"
		}
		qualified := name
		if scope != "" {
			qualified = scope + "::" + name
		}
		return CppSymbol{Name: name, Qualified: qualified, Scope: scope, Kind: kind}, true
	}
	return CppSymbol{}, false
}

func extractCppCalls(lines []string, startLine int, endLine int) []CppCall {
	return indexsupport.ExtractLineCalls(lines, startLine, endLine, cppCallRe, stripCppLineComment, isCppKeywordName, func(name string, line int) CppCall {
		return CppCall{Name: name, Line: line}
	})
}

func filterCppDefinitionCalls(symbol CppSymbol, calls []CppCall) []CppCall {
	if len(calls) == 0 {
		return nil
	}
	filtered := make([]CppCall, 0, len(calls))
	for _, call := range calls {
		if call.Line == symbol.StartLine && call.Name == symbol.Name {
			continue
		}
		filtered = append(filtered, call)
	}
	return filtered
}

func findCppBraceEnd(lines []string, startIdx int) int {
	depth := 0
	seen := false
	for i := startIdx; i < len(lines); i++ {
		line := stripCppLineComment(lines[i])
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

func shouldStartCppSignature(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	if !strings.Contains(line, "(") {
		return false
	}
	prefix := line
	if idx := strings.Index(line, "("); idx >= 0 {
		prefix = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	return !isCppKeywordName(fields[0])
}

func cppParenBalance(text string) int {
	balance := 0
	for _, r := range text {
		switch r {
		case '(':
			balance++
		case ')':
			balance--
		}
	}
	return balance
}

func stripCppLineComment(line string) string {
	return indexsupport.StripLineComment(line)
}

func isCppPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := cppFileExtensions[ext]
	return ok
}

func isCppHeader(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := cppHeaderExtensions[ext]
	return ok
}

func looksLikeCppTestPath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(path, "/test") || strings.Contains(path, "/tests/") || strings.Contains(filepath.Base(path), "test")
}

func cppSourceDirPriority(dir string) int {
	switch dir {
	case "native":
		return 35
	case "src":
		return 30
	case "include":
		return 24
	case ".":
		return 10
	default:
		return 16
	}
}

func isCppKeywordName(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "sizeof", "catch", "new", "delete", "throw", "static_cast", "dynamic_cast", "reinterpret_cast", "const_cast":
		return true
	default:
		return false
	}
}
