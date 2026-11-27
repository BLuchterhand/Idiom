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
	// Aliases maps `using A = B;` and `typedef B A;` names to their target text.
	Aliases map[string]string
	// NamespaceAliases maps every `namespace X = Y;` name in the index to its
	// target ("" when files disagree); call sites expand their own file's
	// aliases at index time, this map covers aliases from included headers.
	NamespaceAliases map[string]string
	Timings          CppBuildTimings
	// Paths and WalkComplete allow a budget-limited build to resume without
	// repeating discovery. A partial index (a build stopped by its deadline) may
	// answer only if the answer is marked partial.
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
	// NamespaceAliases and Aliases are this file's own `namespace X = Y;` and
	// `using`/`typedef` declarations. They live on the file so a file reused
	// from a checkpoint still contributes them; the index-wide maps are merged
	// from every file at link time.
	NamespaceAliases map[string]string `json:",omitempty"`
	Aliases          map[string]string `json:",omitempty"`
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
	// Vars maps parameter and local names in a function body to their declared
	// type text. Fields does the same for the data members of a class, and
	// Bases lists a class's base class names.
	Vars   map[string]string
	Fields map[string]string
	Bases  []string
}

// CppCall is one call site as the syntax index sees it: the callee name plus
// how it was reached, so a query can tell Owner::run from other::run.
type CppCall struct {
	Name string
	Line int
	// Via is how the callee was reached: "" (plain), "::", "." or "->".
	Via string
	// Qualifier is the written scope of a "::" call ("?" if unreadable).
	Qualifier string
	// Receiver is the identifier a "." or "->" call goes through ("this->name"
	// for a data member reached through this); empty when
	// the receiver is a more complex expression.
	Receiver string
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
		Aliases: make(map[string]string),

		NamespaceAliases: make(map[string]string),
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
		// The walk ignores the deadline: it only lists directories, and a walk
		// cut short saves nothing, so a call that arrives with its deadline spent
		// still makes progress (the parse below honours the deadline per file).
		err = safepath.WalkDirUnderRoot(root, root, "C++ index", func(path string, d os.DirEntry, walkErr error) error {
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
	parsedThisCall := 0
	for _, rel := range paths {
		// Stop on the deadline only after this call has parsed its minimum, so
		// every call makes progress and a cold build ends in a bounded number of calls.
		if err := indexsupport.ContextErr(opts.Context); err != nil && parsedThisCall >= cppMinParsedPerCall {
			// Retain unchanged files not reached yet, along with new parses.
			for _, pending := range paths[len(index.Files):] {
				if file, ok := previous[pending]; ok && !opts.ChangedPaths[pending] {
					index.Files = append(index.Files, file)
				}
			}
			index.Timings.Parse, index.Timings.Files = time.Since(parseStarted), len(index.Files)
			// A partial index is linked too, so a caller may answer from it as
			// long as it marks the answer partial.
			linkCppIndex(index, byName, byRel)
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
			parsedThisCall++
		}
	}

	index.Timings.Parse = time.Since(parseStarted)
	index.Timings.Files = len(index.Files)
	// Every file is collected: linking is cheap, so a deadline that passed during
	// the last files no longer fails the build.
	linkCppIndex(index, byName, byRel)

	return index, nil
}

// cppMinParsedPerCall is the files a build parses before it honours its
// deadline. Files parse in milliseconds, so the overrun is small, and it makes
// a cold build finish in at most ceil(files / cppMinParsedPerCall) calls even
// when each call arrives with its deadline already spent.
const cppMinParsedPerCall = 32

// MinParsedPerCall exports the per-call parse floor so callers can state the
// call bound for a cold index.
const MinParsedPerCall = cppMinParsedPerCall

// linkCppIndex sorts the collected files, merges their aliases and resolves
// includes against the full path set into the forward and reverse maps.
func linkCppIndex(index *CppIndex, byName map[string][]string, byRel map[string]string) {
	linkStarted := time.Now()

	slices.SortFunc(index.Files, func(a, b CppFile) int {
		return cmp.Compare(a.Path, b.Path)
	})

	for i := range index.Files {
		file := &index.Files[i]
		mergeCppFileAliases(index, file)

		file.Includes = nil
		for _, raw := range file.RawIncludes {
			include := resolveCppInclude(file.Path, raw, byName, byRel)
			if include == "" {
				include = raw
			}
			file.Includes = textutil.AppendUnique(file.Includes, include)
		}
		slices.Sort(file.Includes)

		for _, include := range file.Includes {
			index.Forward[file.Path] = textutil.AppendUnique(index.Forward[file.Path], include)
			index.Reverse[include] = textutil.AppendUnique(index.Reverse[include], file.Path)
		}
	}

	index.Timings.Link = time.Since(linkStarted)
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

// mergeCppFileAliases folds one file's aliases into the index-wide maps. Both
// merges are order independent: a name two files disagree on becomes "" (a
// namespace alias) or cppAmbiguousType (a type alias), as when one shared map
// was filled during the parse.
func mergeCppFileAliases(index *CppIndex, file *CppFile) {
	for name, target := range file.NamespaceAliases {
		if prior, seen := index.NamespaceAliases[name]; seen && prior != target {
			target = ""
		}

		index.NamespaceAliases[name] = target
	}

	for name, target := range file.Aliases {
		addCppAlias(index.Aliases, name, target)
	}
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
	code := blankCppCommentsAndLiterals(content)
	symbols := parseCppSymbols(rel, code)
	namespaces := parseCppNamespaceAliases(code)
	applyCppNamespaceAliases(symbols, namespaces)

	aliases := make(map[string]string)
	parseCppAliases(code, aliases, namespaces)

	if len(aliases) == 0 {
		aliases = nil
	}

	file := CppFile{
		Path: rel, RawIncludes: parseCppIncludes(content), ContentHash: hash,
		Bytes: int64(len(contentBytes)), Symbols: symbols,
		IsTest: looksLikeCppTestPath(rel), IsHeader: isCppHeader(rel), SourceDir: indexsupport.TopLevelDir(rel),
		NamespaceAliases: namespaces, Aliases: aliases,
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
			if match := cppTypeRe.FindStringSubmatch(trimmed); len(match) == 3 && !isCppNamespaceAlias(match[1], trimmed[len(match[0]):]) {
				end := cppTypeEnd(lines, i)
				kind := strings.ReplaceAll(match[1], " ", "_")

				kind = strings.ReplaceAll(kind, "enum_class", "enum")

				name, scope := match[2], ""
				if cut := strings.LastIndex(name, "::"); cut >= 0 {
					name, scope = name[cut+2:], name[:cut]
				}

				typeSymbol := CppSymbol{
					Name:       name,
					Qualified:  indexsupport.FirstQualified(scope, name, "::"),
					Scope:      scope,
					Kind:       kind,
					Line:       lineNo,
					Column:     1,
					StartLine:  lineNo,
					EndLine:    end,
					SourceFile: rel,
				}

				if kind == "class" || kind == "struct" || kind == "union" {
					typeSymbol.Bases = parseCppBases(lines, i)
					if end > lineNo {
						typeSymbol.Fields = collectCppFields(lines, lineNo, end)
					}
				}

				symbols = append(symbols, typeSymbol)
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
				symbol.Vars = collectCppBodyVars(signature, lines, symbol.StartLine, symbol.EndLine, symbol.Calls)
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

// blankCppCommentsAndLiterals replaces every /* ... */ comment (including one
// that spans lines) and the contents of string and character literals with
// spaces, keeping newlines and quotes so line numbers stay put. The line-based
// parser would otherwise read `run(1); /* x */` as an unfinished statement and
// fold the next signature into it, and a "}" or '{' literal would break brace
// tracking. A "/*" inside a literal or // comment is not a comment.
func blankCppCommentsAndLiterals(content string) string {
	if !strings.ContainsAny(content, "\"'") && !strings.Contains(content, "/*") {
		return content
	}

	out := []byte(content)

	for i := 0; i < len(out); i++ {
		switch out[i] {
		case '"', '\'':
			if out[i] == '\'' && isCppDigitSeparator(out, i) {
				continue // a digit separator (1'000, 0xFFFF'FFFFu)
			}

			quote := out[i]

			for i++; i < len(out) && out[i] != quote && out[i] != '\n'; i++ {
				if out[i] == '\\' && i+1 < len(out) && out[i+1] != '\n' {
					out[i] = ' '
					i++
				}

				out[i] = ' '
			}
		case '/':
			if i+1 >= len(out) {
				continue
			}

			switch out[i+1] {
			case '/':
				for i < len(out) && out[i] != '\n' {
					i++
				}
			case '*':
				end := strings.Index(content[i+2:], "*/")
				stop := len(out)

				if end >= 0 {
					stop = i + 2 + end + 2
				}

				for j := i; j < stop; j++ {
					if out[j] != '\n' {
						out[j] = ' '
					}
				}

				i = stop - 1
			}
		}
	}

	return string(out)
}

// isCppDigitSeparator reports whether the quote at out[i] sits inside a
// numeric literal. A hex digit before it is not enough (0xFFFF'FFFFu); the
// token it ends has to start with a digit, which also keeps u8'a' and
// L'x' character literals out.
func isCppDigitSeparator(out []byte, i int) bool {
	start := i

	for start > 0 && isCppNumberTokenByte(out[start-1]) {
		start--
	}

	return start < i && out[start] >= '0' && out[start] <= '9'
}

func isCppNumberTokenByte(b byte) bool {
	return b == '\'' || b == '.' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isCppNamespaceAlias reports a `namespace X = Y;` line, which declares no
// namespace body (parseCppNamespaceAliases reads it).
func isCppNamespaceAlias(kind string, rest string) bool {
	return kind == "namespace" && strings.HasPrefix(strings.TrimSpace(rest), "=")
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
		beforeParen := stripCppScopeTemplateArgs(strings.TrimSpace(signature[:idx]))
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

var cppScopeTemplateArgsRe = regexp.MustCompile(`<[^<>]*>(\s*::)`)

// stripCppScopeTemplateArgs turns `Mix<T>::go` into `Mix::go` so an
// out-of-class member of a class template keeps its class scope.
func stripCppScopeTemplateArgs(text string) string {
	for strings.Contains(text, ">") {
		next := cppScopeTemplateArgsRe.ReplaceAllString(text, "$1")
		if next == text {
			break
		}

		text = next
	}

	return text
}
