package cpp

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/internal/indexsupport"
)

type CppIndex struct {
	Root    string
	Files   []CppFile
	Forward map[string][]string
	Reverse map[string][]string
	Timings CppBuildTimings
	// Paths and WalkComplete allow a budget-limited build to resume without
	// repeating discovery. Partial indexes must never be used to answer queries.
	Paths        []string
	WalkComplete bool
}

// BuildOptions permits reuse because symbols are collected independently per
// file. Includes are always resolved again against the current path set.
type BuildOptions struct {
	Scope        string
	MaxFiles     int
	Context      context.Context
	Previous     *CppIndex
	ChangedPaths map[string]bool
	Resume       bool
}

// CppBuildTimings records where a BuildCppIndexWithOptions call spent its time
// so a slow or budget-exceeded build can name its phase.
type CppBuildTimings struct {
	Walk   time.Duration
	Parse  time.Duration
	Link   time.Duration
	Files  int
	Bytes  int64
	Parsed int
	Reused int
}

type CppFile struct {
	Path        string
	RawIncludes []string
	ContentHash string
	Bytes       int64
	Includes    []string
	Symbols     []CppSymbol
	Funcs       int
	Methods     int
	Types       int
	HasMain     bool
	IsTest      bool
	IsHeader    bool
	SourceDir   string
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
	// Explicit marks a member defined out of class with its own qualifier
	// (void Pool::reset() {}), whose namespace may be implied by context.
	Explicit bool
}

type CppCall struct {
	Name string
	Line int
}

var (
	cppIncludeRe       = regexp.MustCompile(`^\s*#include\s+[<"]([^>"]+)`)
	cppTypeRe          = regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?(class|struct|enum(?:\s+class)?|union|namespace)\s+([A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)*)`)
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
	return BuildCppIndexWithOptions(root, BuildOptions{})
}

func BuildCppIndexWithOptions(root string, opts BuildOptions) (*CppIndex, error) {
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

	walkStarted := time.Now()
	errStop := filepath.SkipAll
	var err error
	if opts.Resume && opts.Previous != nil && opts.Previous.WalkComplete {
		paths = slices.Clone(opts.Previous.Paths)
	} else if opts.Previous != nil && opts.Previous.WalkComplete && opts.ChangedPaths != nil && opts.Scope == "" && opts.MaxFiles == 0 {
		// Git's changed-path set plus both dirty/ignored snapshots accounts for
		// additions and removals. Reuse discovery as well as parsed files.
		for _, rel := range opts.Previous.Paths {
			if !opts.ChangedPaths[rel] {
				paths = append(paths, rel)
			}
		}
		for rel := range opts.ChangedPaths {
			if !IsIndexPath(rel) {
				continue
			}
			stat, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return nil, statErr
			}
			if !stat.IsDir() {
				paths = append(paths, rel)
			}
		}
	} else {
		err = safepath.WalkDirUnderRoot(root, root, "C++ index", func(path string, d os.DirEntry, walkErr error) error {
			if err := indexsupport.ContextErr(opts.Context); err != nil {
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

			if opts.MaxFiles > 0 && len(paths) >= opts.MaxFiles {
				return errStop
			}

			return nil
		})
	}

	if err == errStop {
		err = nil
	}

	if err != nil {
		if ctxErr := indexsupport.ContextErr(opts.Context); ctxErr != nil && errors.Is(err, ctxErr) {
			index.Timings.Walk = time.Since(walkStarted)

			if opts.Previous != nil {
				for _, file := range opts.Previous.Files {
					if !opts.ChangedPaths[file.Path] {
						index.Files = append(index.Files, file)
					}
				}
			}
			return index, &BudgetError{Phase: "walk", Timings: index.Timings, Cause: err}
		}

		return nil, err
	}

	if len(paths) == 0 {
		return nil, nil
	}

	slices.Sort(paths)
	index.Paths, index.WalkComplete = paths, true
	for _, rel := range paths {
		byName[filepath.Base(rel)] = textutil.AppendUnique(byName[filepath.Base(rel)], rel)
		byRel[rel] = rel
	}
	previous := make(map[string]CppFile)
	if opts.Previous != nil {
		for _, file := range opts.Previous.Files {
			previous[file.Path] = file
		}
	}

	index.Timings.Walk = time.Since(walkStarted)
	parseStarted := time.Now()
	for _, rel := range paths {
		if err := indexsupport.ContextErr(opts.Context); err != nil {
			// Retain unchanged files not reached yet, along with new parses.
			for _, pending := range paths[len(index.Files):] {
				if file, ok := previous[pending]; ok && !opts.ChangedPaths[pending] {
					index.Files = append(index.Files, file)
				}
			}
			index.Timings.Parse, index.Timings.Files = time.Since(parseStarted), len(index.Files)
			return index, &BudgetError{Phase: "parse", Timings: index.Timings, Cause: err}
		}
		if file, ok := previous[rel]; ok && !opts.ChangedPaths[rel] {
			index.Files = append(index.Files, file)
			index.Timings.Bytes += file.Bytes
			index.Timings.Reused++
			continue
		}
		file, reused, err := collectCppFile(root, rel, previous[rel])
		if err != nil {
			return nil, err
		}
		index.Files = append(index.Files, file)
		index.Timings.Bytes += file.Bytes
		if reused {
			index.Timings.Reused++
		} else {
			index.Timings.Parsed++
		}
	}

	index.Timings.Parse = time.Since(parseStarted)
	index.Timings.Files = len(index.Files)
	if err := indexsupport.ContextErr(opts.Context); err != nil {
		return index, &BudgetError{Phase: "parse", Timings: index.Timings, Cause: err}
	}
	linkStarted := time.Now()

	slices.SortFunc(index.Files, func(a, b CppFile) int {
		return cmp.Compare(a.Path, b.Path)
	})

	for i := range index.Files {
		file := &index.Files[i]
		file.Includes = nil
		for _, raw := range file.RawIncludes {
			include := resolveCppInclude(file.Path, raw, byName, byRel)
			if include == "" {
				include = raw
			}
			file.Includes = textutil.AppendUnique(file.Includes, include)
		}
		slices.Sort(file.Includes)
		if len(file.Includes) == 0 {
			continue
		}

		for _, include := range file.Includes {
			index.Forward[file.Path] = textutil.AppendUnique(index.Forward[file.Path], include)
			index.Reverse[include] = textutil.AppendUnique(index.Reverse[include], file.Path)
		}
	}

	index.Timings.Link = time.Since(linkStarted)

	return index, nil
}

// IsIndexPath applies the syntax collector's file and directory filters to a
// repository-relative path. Snapshot identities must use the same scope.
func IsIndexPath(path string) bool {
	if !filepath.IsLocal(filepath.FromSlash(path)) || !isCppPath(path) || discovery.ShouldSkipRelFile(path) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts[:len(parts)-1] {
		if discovery.ShouldSkipDirName(part) {
			return false
		}
	}
	return true
}

func collectCppFile(root, rel string, previous CppFile) (CppFile, bool, error) {
	contentBytes, err := indexsupport.ReadFileUnderRoot(root, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return CppFile{}, false, err
	}
	sum := sha256.Sum256(contentBytes)
	hash := hex.EncodeToString(sum[:])
	if previous.Path == rel && previous.ContentHash == hash {
		return previous, true, nil
	}
	content := string(contentBytes)
	file := CppFile{
		Path: rel, RawIncludes: parseCppIncludes(content), ContentHash: hash,
		Bytes: int64(len(contentBytes)), Symbols: parseCppSymbols(rel, content),
		IsTest: looksLikeCppTestPath(rel), IsHeader: isCppHeader(rel), SourceDir: indexsupport.TopLevelDir(rel),
	}
	for _, symbol := range file.Symbols {
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
	return file, false, nil
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

		return strconv.Itoa(file.Funcs+file.Methods) + " funcs/methods, " +
			strconv.Itoa(file.Types) + " types, " +
			strconv.Itoa(len(file.Includes)) + " includes", true
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

func parseCppIncludes(content string) []string {
	lines := strings.Split(content, "\n")
	includes := make([]string, 0, 8)

	for _, line := range lines {
		match := cppIncludeRe.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}

		include := filepath.ToSlash(strings.TrimSpace(match[1]))

		includes = textutil.AppendUnique(includes, include)
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
		clean := indexsupport.StripLineComment(line)
		trimmed := strings.TrimSpace(clean)

		if trimmed == "" {
			continue
		}

		if pendingStart == 0 {
			if match := cppTypeRe.FindStringSubmatch(trimmed); len(match) == 3 {
				end := cppTypeEnd(lines, i)
				kind := strings.ReplaceAll(match[1], " ", "_")

				kind = strings.ReplaceAll(kind, "enum_class", "enum")

				name, scope := match[2], ""
				if cut := strings.LastIndex(name, "::"); cut >= 0 {
					name, scope = name[cut+2:], name[:cut]
				}

				symbols = append(symbols, CppSymbol{
					Name:       name,
					Qualified:  indexsupport.FirstQualified(scope, name, "::"),
					Scope:      scope,
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

	return attributeCppScopes(symbols)
}

// cppTypeEnd returns the last line of the type or namespace starting at lines[start]:
// the matching brace when the body opens on this line or within the next few
// lines, else the line itself (a forward declaration).
func cppTypeEnd(lines []string, start int) int {
	for i := start; i < len(lines) && i < start+6; i++ {
		clean := indexsupport.StripLineComment(lines[i])
		if strings.Contains(clean, "{") {
			return findCppBraceEnd(lines, i)
		}

		if strings.Contains(clean, ";") {
			break
		}
	}

	return start + 1
}

// attributeCppScopes gives every symbol its enclosing namespace or class by line
// range, so in-class members and same-named types in different namespaces stay
// distinct, and drops signature-shaped call statements that sit inside another
// function's body. symbols must be sorted by line.
func attributeCppScopes(symbols []CppSymbol) []CppSymbol {
	out := make([]CppSymbol, 0, len(symbols))
	stack := make([]CppSymbol, 0, 8)

	for _, symbol := range symbols {
		for len(stack) > 0 && stack[len(stack)-1].EndLine < symbol.Line {
			stack = stack[:len(stack)-1]
		}

		isType := isCppContainerKind(symbol.Kind)

		insideBody := false

		for _, open := range stack {
			if !isCppContainerKind(open.Kind) {
				insideBody = true
			}
		}

		if insideBody && !isType {
			continue
		}

		symbol = scopeCppSymbol(symbol, stack)
		out = append(out, symbol)

		if isType && symbol.EndLine > symbol.StartLine || symbol.HasBody && !isType {
			stack = append(stack, symbol)
		}
	}

	return out
}

func isCppContainerKind(kind string) bool {
	switch kind {
	case "class", "struct", "union", "namespace":
		return true
	}

	return false
}

func scopeCppSymbol(symbol CppSymbol, stack []CppSymbol) CppSymbol {
	innermost, namespace := "", ""

	for i := len(stack) - 1; i >= 0; i-- {
		if !isCppContainerKind(stack[i].Kind) {
			continue
		}

		if innermost == "" {
			innermost = stack[i].Qualified
		}

		if namespace == "" && stack[i].Kind == "namespace" {
			namespace = stack[i].Qualified
		}
	}

	switch {
	case isCppContainerKind(symbol.Kind):
		symbol.Scope = joinCppScope(innermost, symbol.Scope)
	case symbol.Scope != "":
		symbol.Explicit = true
		symbol.Scope = joinCppScope(namespace, symbol.Scope)
	case innermost != "":
		symbol.Scope = innermost
		if symbol.Kind == "func" && namespace != innermost {
			symbol.Kind = "method"
		}
	}

	symbol.Qualified = indexsupport.FirstQualified(symbol.Scope, symbol.Name, "::")

	return symbol
}

func joinCppScope(prefix string, scope string) string {
	if prefix == "" {
		return scope
	}

	if scope == "" {
		return prefix
	}

	return prefix + "::" + scope
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
	return indexsupport.ExtractLineCalls(lines, startLine, endLine, cppCallRe, indexsupport.StripLineComment, isCppKeywordName, func(name string, line int) CppCall {
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
		line := indexsupport.StripLineComment(lines[i])
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
