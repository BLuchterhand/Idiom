package semantic

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"idiom/internal/discovery"
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
	return BuildCppIndexWithOptions(root, IndexOptions{})
}

func BuildCppIndexWithOptions(root string, opts IndexOptions) (*CppIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	scope := cleanIndexScope(opts.Scope)

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
		if !pathInIndexScope(rel, scope) {
			return nil
		}
		paths = append(paths, rel)
		byName[filepath.Base(rel)] = appendUniqueString(byName[filepath.Base(rel)], rel)
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

	sort.Strings(paths)
	for _, rel := range paths {
		if err := indexContextErr(opts); err != nil {
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
			SourceDir: topLevelDir(rel),
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

	sort.Slice(index.Files, func(i, j int) bool {
		return index.Files[i].Path < index.Files[j].Path
	})
	for _, file := range index.Files {
		if len(file.Includes) == 0 {
			continue
		}
		for _, include := range file.Includes {
			index.Forward[file.Path] = appendUniqueString(index.Forward[file.Path], include)
			index.Reverse[include] = appendUniqueString(index.Reverse[include], file.Path)
		}
	}
	return index, nil
}

func (idx *CppIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Indexed ")
	b.WriteString(intString(len(idx.Files)))
	b.WriteString(" C/C++ files\n")
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
			b.WriteString(intString(file.Types))
			b.WriteString(" types, ")
			b.WriteString(intString(len(file.Includes)))
			b.WriteString(" includes)\n")
		}
	}
	return strings.TrimSpace(b.String())
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
		score += minInt(file.Funcs+file.Methods*2, 24)
		score += minInt(file.Types*2, 16)
		return score
	}, limit), limit)
}

func (idx *CppIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := normalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}
	return idx.uniqueRankedPaths(idx.rankFiles(func(file CppFile) int {
		score := 0
		pathLower := strings.ToLower(file.Path)
		for _, term := range normalized {
			if strings.Contains(pathLower, term) {
				score += 16
			}
			for _, include := range file.Includes {
				if strings.Contains(strings.ToLower(include), term) {
					score += 6
				}
			}
			for _, symbol := range file.Symbols {
				if strings.Contains(strings.ToLower(symbol.Name), term) || strings.Contains(strings.ToLower(symbol.Qualified), term) {
					score += 10
				}
			}
		}
		score += cppSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 20
		}
		return score
	}, limit), limit)
}

func (idx *CppIndex) FileByPath(path string) *CppFile {
	if idx == nil {
		return nil
	}
	path = filepath.ToSlash(strings.TrimSpace(path))
	for i := range idx.Files {
		if idx.Files[i].Path == path {
			return &idx.Files[i]
		}
	}
	return nil
}

func (idx *CppIndex) findFiles(pattern string) []string {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	if idx == nil || pattern == "" {
		return nil
	}
	exact := make([]string, 0)
	partial := make([]string, 0)
	for _, file := range idx.Files {
		base := strings.ToLower(filepath.Base(file.Path))
		full := strings.ToLower(file.Path)
		switch {
		case base == pattern || full == pattern:
			exact = append(exact, file.Path)
		case strings.Contains(base, pattern) || strings.Contains(full, pattern):
			partial = append(partial, file.Path)
		}
	}
	if len(exact) > 0 {
		sort.Strings(exact)
		return exact
	}
	sort.Strings(partial)
	return partial
}

func (idx *CppIndex) rankFiles(scoreFn func(CppFile) int, limit int) []CppFile {
	scored := make([]struct {
		file  CppFile
		score int
	}, 0, len(idx.Files))
	for _, file := range idx.Files {
		score := scoreFn(file)
		if score <= 0 {
			continue
		}
		scored = append(scored, struct {
			file  CppFile
			score int
		}{file: file, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].file.Path < scored[j].file.Path
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]CppFile, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.file)
	}
	return out
}

func (idx *CppIndex) uniqueRankedPaths(files []CppFile, limit int) []string {
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
		includes = appendUniqueString(includes, include)
	}
	sort.Strings(includes)
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

	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].SourceFile != symbols[j].SourceFile {
			return symbols[i].SourceFile < symbols[j].SourceFile
		}
		return symbols[i].Line < symbols[j].Line
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
	if startLine <= 0 || endLine < startLine || endLine > len(lines) {
		return nil
	}
	calls := make([]CppCall, 0, 16)
	for line := startLine; line <= endLine; line++ {
		text := stripCppLineComment(lines[line-1])
		for _, match := range cppCallRe.FindAllStringSubmatch(text, -1) {
			if len(match) != 2 {
				continue
			}
			name := match[1]
			if isCppKeywordName(name) {
				continue
			}
			calls = append(calls, CppCall{Name: name, Line: line})
		}
	}
	return calls
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
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
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

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
