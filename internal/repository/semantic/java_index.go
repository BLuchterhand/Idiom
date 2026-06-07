package semantic

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/platform/util"
	"idiom/internal/repository/discovery"
)

// JavaIndex is a lightweight, regex-based semantic index for a Java
// workspace. It mirrors the shape of CppIndex: a slice of files, each
// with its declared symbols, and forward/reverse edges derived from
// `package` + `import` statements.
//
// The index is intentionally shallow — jdtls handles deep resolution
// (types across jars, generics, overloads). JavaIndex carries just
// enough structure for: file outlines, workspace-symbol discovery,
// entrypoint/overview heuristics, related-file navigation, and a
// tree-sitter-free call-graph approximation for calls_from/calls_into.
type JavaIndex struct {
	Root      string
	Files     []JavaFile
	Forward   map[string][]string // file -> files it imports from (resolved in-workspace)
	Reverse   map[string][]string // file -> files that import it
	byPackage map[string][]string // package -> list of file paths
}

type JavaFile struct {
	Path       string
	Package    string
	Imports    []string // fully-qualified imports
	Symbols    []JavaSymbol
	Classes    int
	Interfaces int
	Enums      int
	Methods    int
	Fields     int
	HasMain    bool
	IsTest     bool
	SourceDir  string
}

type JavaSymbol struct {
	Name        string
	Qualified   string // package.Outer.Inner or package.Class#method
	Kind        string // class, interface, enum, record, annotation, method, constructor, field
	Scope       string // enclosing class/package (for display)
	Modifiers   []string
	Annotations []string
	Extends     string   // single superclass, for classes
	Implements  []string // implemented interfaces (for classes) or extended interfaces (for interfaces)
	Line        int
	Column      int
	StartLine   int
	EndLine     int
	Calls       []JavaCall
	SourceFile  string
}

type JavaCall struct {
	Name string
	Line int
}

var (
	javaPackageRe    = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	javaImportRe     = regexp.MustCompile(`^\s*import\s+(static\s+)?([A-Za-z_][A-Za-z0-9_.]*(?:\.\*)?)\s*;`)
	javaAnnotationRe = regexp.MustCompile(`^@([A-Za-z_][A-Za-z0-9_.]*)`)
	// Matches a class/interface/enum/record/annotation header line. Captures:
	// 1: kind, 2: name, 3: rest (extends/implements/permits ...)
	javaTypeRe = regexp.MustCompile(`^\s*(?:public\s+|protected\s+|private\s+|abstract\s+|final\s+|static\s+|sealed\s+|non-sealed\s+)*(class|interface|enum|record|@interface)\s+([A-Za-z_][A-Za-z0-9_]*)([^{]*)`)
	// Method/constructor signature candidates — the parser below still
	// verifies parentheses and rejects control-flow keywords.
	javaNameBeforeParen = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>]*>\s*)?$`)
	javaCallRe          = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

var javaFileExtensions = map[string]struct{}{
	".java": {},
}

var javaSkippedDirNames = map[string]struct{}{
	".git":         {},
	".gradle":      {},
	".idea":        {},
	".mvn":         {},
	"build":        {},
	"target":       {},
	"out":          {},
	"bin":          {},
	"node_modules": {},
}

// BuildJavaIndex walks root and parses every .java file using the
// regex pipeline above. It never returns (nil, nil) for a directory
// that actually contains .java files; if root has no Java sources it
// returns (nil, nil).
func BuildJavaIndex(root string) (*JavaIndex, error) {
	return BuildJavaIndexWithOptions(root, IndexOptions{})
}

func BuildJavaIndexWithOptions(root string, opts IndexOptions) (*JavaIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	scope := cleanIndexScope(opts.Scope)
	index := &JavaIndex{
		Root:      root,
		Forward:   make(map[string][]string),
		Reverse:   make(map[string][]string),
		byPackage: make(map[string][]string),
	}
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
			if _, skip := javaSkippedDirNames[d.Name()]; skip && path != root {
				return filepath.SkipDir
			}
			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isJavaPath(path) {
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

	classByFQN := make(map[string]string) // fq class name -> file path
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
		file := parseJavaFile(rel, content)
		index.Files = append(index.Files, file)
		index.byPackage[file.Package] = append(index.byPackage[file.Package], rel)
		for _, sym := range file.Symbols {
			switch sym.Kind {
			case "class", "record":
				if sym.Qualified != "" {
					classByFQN[sym.Qualified] = rel
				}
			case "interface", "enum", "annotation":
				if sym.Qualified != "" {
					classByFQN[sym.Qualified] = rel
				}
			}
		}
	}

	// Build forward/reverse edges based on (a) same-package siblings
	// and (b) fully-qualified imports matching an indexed FQN.
	for _, file := range index.Files {
		// Same-package edges — every file in the package is "related".
		siblings := index.byPackage[file.Package]
		for _, sib := range siblings {
			if sib == file.Path {
				continue
			}
			index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], sib)
			index.Reverse[sib] = util.AppendUnique(index.Reverse[sib], file.Path)
		}
		// Import edges — only those pointing at in-workspace FQNs.
		for _, imp := range file.Imports {
			target := strings.TrimSuffix(imp, ".*")
			if rel, ok := classByFQN[target]; ok {
				index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], rel)
				index.Reverse[rel] = util.AppendUnique(index.Reverse[rel], file.Path)
				continue
			}
			// Wildcard: pull in every file in the matching package.
			if strings.HasSuffix(imp, ".*") {
				pkg := strings.TrimSuffix(imp, ".*")
				for _, rel := range index.byPackage[pkg] {
					if rel == file.Path {
						continue
					}
					index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], rel)
					index.Reverse[rel] = util.AppendUnique(index.Reverse[rel], file.Path)
				}
			}
		}
	}
	return index, nil
}

// Summary produces a short human-readable blurb for overview tools.
func (idx *JavaIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Indexed ")
	b.WriteString(intString(len(idx.Files)))
	b.WriteString(" Java files\n")
	anchors := idx.DefaultFiles(4)
	if len(anchors) > 0 {
		b.WriteString("Likely anchors:\n")
		for _, path := range anchors {
			file := idx.FileByPath(path)
			if file == nil {
				continue
			}
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString(" (")
			b.WriteString(intString(file.Methods))
			b.WriteString(" methods, ")
			b.WriteString(intString(file.Classes + file.Interfaces + file.Enums))
			b.WriteString(" types, ")
			b.WriteString(intString(len(file.Imports)))
			b.WriteString(" imports)\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// DefaultFiles returns paths ranked by "anchor-ness" — files likely to
// be the first thing you should read when orienting in the workspace.
func (idx *JavaIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file JavaFile) int {
		score := 0
		if file.HasMain {
			score += 100
		}
		score += javaSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 30
		}
		score += min(file.Methods*2, 24)
		score += min((file.Classes+file.Interfaces+file.Enums)*3, 18)
		return score
	}, limit), limit)
}

// RelevantFiles returns paths matching any of the provided terms.
func (idx *JavaIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := normalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}
	return idx.uniqueRankedPaths(idx.rankFiles(func(file JavaFile) int {
		score := 0
		pathLower := strings.ToLower(file.Path)
		for _, term := range normalized {
			if strings.Contains(pathLower, term) {
				score += 16
			}
			if strings.Contains(strings.ToLower(file.Package), term) {
				score += 12
			}
			for _, imp := range file.Imports {
				if strings.Contains(strings.ToLower(imp), term) {
					score += 6
				}
			}
			for _, sym := range file.Symbols {
				if strings.Contains(strings.ToLower(sym.Name), term) || strings.Contains(strings.ToLower(sym.Qualified), term) {
					score += 10
				}
			}
		}
		score += javaSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 20
		}
		return score
	}, limit), limit)
}

// FileByPath looks up a file by its workspace-relative slash path.
func (idx *JavaIndex) FileByPath(path string) *JavaFile {
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

func (idx *JavaIndex) findFiles(pattern string) []string {
	if idx == nil {
		return nil
	}
	return FindFilesExactPartial(idx.Files, pattern)
}

func (idx *JavaIndex) rankFiles(scoreFn func(JavaFile) int, limit int) []JavaFile {
	return RankFiles(idx.Files, scoreFn, limit)
}

func (idx *JavaIndex) uniqueRankedPaths(files []JavaFile, limit int) []string {
	return UniqueRankedPaths(files, limit)
}

// Entrypoints scans every file for likely entry functions:
//   - public static void main(String[])
//   - methods annotated with @Test (JUnit 4/5)
//   - classes annotated with @SpringBootApplication
//
// Result is the set of *file paths* containing any such entry, ranked.
func (idx *JavaIndex) Entrypoints(limit int) []string {
	if idx == nil {
		return nil
	}
	out := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, file := range idx.Files {
		if file.HasMain {
			add(file.Path)
			continue
		}
		for _, sym := range file.Symbols {
			if hasJavaAnnotation(sym.Annotations, "Test") {
				add(file.Path)
				break
			}
			if hasJavaAnnotation(sym.Annotations, "SpringBootApplication") {
				add(file.Path)
				break
			}
		}
	}
	slices.Sort(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func hasJavaAnnotation(annotations []string, name string) bool {
	for _, a := range annotations {
		a = strings.TrimSpace(a)
		a = strings.TrimPrefix(a, "@")
		// Drop arg suffix, e.g. `Test(expected = ...)`.
		if idx := strings.Index(a, "("); idx >= 0 {
			a = a[:idx]
		}
		// Drop package prefix, e.g. `org.junit.Test`.
		if idx := strings.LastIndex(a, "."); idx >= 0 {
			a = a[idx+1:]
		}
		if a == name {
			return true
		}
	}
	return false
}

// parseJavaFile runs the regex pipeline on a single file's contents.
func parseJavaFile(rel string, content string) JavaFile {
	lines := strings.Split(content, "\n")
	file := JavaFile{
		Path:      rel,
		IsTest:    looksLikeJavaTestPath(rel),
		SourceDir: topLevelDir(rel),
	}

	// First pass: package + imports.
	for _, raw := range lines {
		line := stripJavaLineComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if file.Package == "" {
			if match := javaPackageRe.FindStringSubmatch(trimmed); len(match) == 2 {
				file.Package = match[1]
				continue
			}
		}
		if match := javaImportRe.FindStringSubmatch(trimmed); len(match) == 3 {
			file.Imports = util.AppendUnique(file.Imports, match[2])
			continue
		}
		// Once we see a type declaration the preamble is over.
		if javaTypeRe.MatchString(trimmed) {
			break
		}
	}

	// Second pass: symbols. We walk line-by-line tracking a brace-scoped
	// stack of enclosing classes so inner/anonymous classes come out
	// properly nested in file_outline.
	var (
		classStack []string
		braceDepth = 0
		// Pending annotations apply to the next declaration.
		pendingAnnotations []string
		pendingSignature   []string
		pendingStart       int
		parenDepth         int
	)

	for i, raw := range lines {
		lineNo := i + 1
		line := stripJavaLineComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Annotations on their own line (single-line only; multi-line
		// annotations are uncommon and we'd rather skip a few than
		// over-parse).
		if strings.HasPrefix(trimmed, "@") && !strings.Contains(trimmed, "(") && !javaTypeRe.MatchString(trimmed) {
			if match := javaAnnotationRe.FindStringSubmatch(trimmed); len(match) == 2 {
				pendingAnnotations = append(pendingAnnotations, match[1])
				continue
			}
		}
		if strings.HasPrefix(trimmed, "@") && strings.Contains(trimmed, "(") {
			// Annotation with args: @Test(expected = ...) — still capture the name.
			if match := javaAnnotationRe.FindStringSubmatch(trimmed); len(match) == 2 {
				pendingAnnotations = append(pendingAnnotations, match[1])
				// Continue only if the annotation fully closes on this line.
				if strings.Count(trimmed, "(") == strings.Count(trimmed, ")") {
					continue
				}
			}
		}

		// Type declarations (class / interface / enum / record / @interface).
		if pendingStart == 0 {
			if match := javaTypeRe.FindStringSubmatch(trimmed); len(match) == 4 {
				kind := javaTypeKind(match[1])
				name := match[2]
				rest := match[3]
				scope := strings.Join(classStack, ".")
				qualified := javaQualifiedName(file.Package, classStack, name)
				end := lineNo
				if strings.Contains(trimmed, "{") {
					end = findJavaBraceEnd(lines, i)
				}
				extends, implements := parseJavaTypeClause(rest)
				sym := JavaSymbol{
					Name:        name,
					Qualified:   qualified,
					Kind:        kind,
					Scope:       scope,
					Annotations: append([]string(nil), pendingAnnotations...),
					Extends:     extends,
					Implements:  implements,
					Line:        lineNo,
					Column:      1,
					StartLine:   lineNo,
					EndLine:     end,
					SourceFile:  rel,
				}
				file.Symbols = append(file.Symbols, sym)
				switch kind {
				case "class", "record":
					file.Classes++
				case "interface", "annotation":
					file.Interfaces++
				case "enum":
					file.Enums++
				}
				// Push onto class stack if this type opens a body on the same line.
				if strings.Contains(trimmed, "{") {
					classStack = append(classStack, name)
				}
				pendingAnnotations = nil
				braceDepth += javaBraceBalance(trimmed)
				continue
			}
		}

		// Method signatures. We treat any line with `(` as a candidate
		// and let parseJavaMethodSignature reject non-signature lines
		// (control flow, calls, etc.).
		switch {
		case pendingStart == 0 && shouldStartJavaSignature(trimmed):
			pendingSignature = []string{trimmed}
			pendingStart = lineNo
			parenDepth = javaParenBalance(trimmed)
		case pendingStart > 0:
			pendingSignature = append(pendingSignature, trimmed)
			parenDepth += javaParenBalance(trimmed)
		default:
			braceDepth += javaBraceBalance(trimmed)
			if strings.Contains(trimmed, "}") {
				popJavaClasses(&classStack, &braceDepth, trimmed)
			}
			continue
		}

		ready := pendingStart > 0 && parenDepth <= 0 && (strings.Contains(trimmed, "{") || strings.HasSuffix(trimmed, ";"))
		if !ready {
			continue
		}
		signature := strings.Join(pendingSignature, " ")
		if sym, ok := parseJavaMethodSignature(signature, file.Package, classStack); ok {
			sym.Line = pendingStart
			sym.Column = 1
			sym.StartLine = pendingStart
			sym.EndLine = pendingStart
			sym.SourceFile = rel
			sym.Annotations = append([]string(nil), pendingAnnotations...)
			if strings.Contains(signature, "{") {
				sym.EndLine = findJavaBraceEnd(lines, i)
				sym.Calls = extractJavaCalls(lines, sym.StartLine, sym.EndLine)
			}
			if sym.Kind == "method" || sym.Kind == "constructor" {
				file.Methods++
			}
			if sym.Name == "main" && hasJavaModifier(sym.Modifiers, "static") && hasJavaModifier(sym.Modifiers, "public") {
				file.HasMain = true
			}
			file.Symbols = append(file.Symbols, sym)
		}
		pendingSignature = nil
		pendingStart = 0
		parenDepth = 0
		pendingAnnotations = nil

		braceDepth += javaBraceBalance(trimmed)
		if strings.Contains(trimmed, "}") {
			popJavaClasses(&classStack, &braceDepth, trimmed)
		}
	}

	// Count field-like declarations very approximately — lines that end
	// with `;` at the class top level and don't contain `(`. We don't
	// track full field symbols here to keep the parser tight.
	file.Fields = countJavaFieldLines(lines)

	slices.SortFunc(file.Symbols, func(a, b JavaSymbol) int {
		return cmp.Compare(a.Line, b.Line)
	})
	return file
}

func parseJavaTypeClause(rest string) (extends string, implements []string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", nil
	}
	// Split off the opening brace if present.
	if idx := strings.Index(rest, "{"); idx >= 0 {
		rest = rest[:idx]
	}
	tokens := strings.Fields(rest)
	mode := ""
	var implList []string
	for _, tok := range tokens {
		tok = strings.TrimRight(tok, ",")
		switch tok {
		case "extends":
			mode = "extends"
		case "implements":
			mode = "implements"
		case "permits":
			mode = "permits"
		default:
			switch mode {
			case "extends":
				if extends == "" {
					extends = strings.Split(tok, "<")[0]
				} else {
					implList = append(implList, strings.Split(tok, "<")[0])
				}
			case "implements":
				implList = append(implList, strings.Split(tok, "<")[0])
			}
		}
	}
	return extends, implList
}

func parseJavaMethodSignature(signature string, pkg string, classStack []string) (JavaSymbol, bool) {
	signature = strings.TrimSpace(signature)
	if signature == "" || strings.HasPrefix(signature, "//") || strings.HasPrefix(signature, "*") {
		return JavaSymbol{}, false
	}
	idx := strings.Index(signature, "(")
	if idx < 0 {
		return JavaSymbol{}, false
	}
	before := strings.TrimSpace(signature[:idx])
	match := javaNameBeforeParen.FindStringSubmatch(before)
	if len(match) != 2 {
		return JavaSymbol{}, false
	}
	name := match[1]
	if isJavaKeywordName(name) {
		return JavaSymbol{}, false
	}
	modifiers := extractJavaModifiers(before)
	nonModifierFields := strings.Fields(stripJavaModifiers(before))
	scope := strings.Join(classStack, ".")
	kind := "method"
	if len(classStack) > 0 && classStack[len(classStack)-1] == name && len(nonModifierFields) == 1 {
		kind = "constructor"
	} else if len(nonModifierFields) < 2 {
		return JavaSymbol{}, false
	}
	qualified := javaMemberQualifiedName(pkg, classStack, name)
	return JavaSymbol{
		Name:      name,
		Qualified: qualified,
		Kind:      kind,
		Scope:     scope,
		Modifiers: modifiers,
	}, true
}

func extractJavaCalls(lines []string, startLine int, endLine int) []JavaCall {
	return ExtractLineCalls(lines, startLine, endLine, javaCallRe, stripJavaLineComment, isJavaKeywordName, func(name string, line int) JavaCall {
		return JavaCall{Name: name, Line: line}
	})
}

func findJavaBraceEnd(lines []string, startIdx int) int {
	depth := 0
	seen := false
	for i := startIdx; i < len(lines); i++ {
		line := stripJavaLineComment(lines[i])
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

func shouldStartJavaSignature(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
		return false
	}
	if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "/*") {
		return false
	}
	if !strings.Contains(line, "(") {
		return false
	}
	// Skip lines that look like statements, not signatures.
	if strings.HasPrefix(line, "if") || strings.HasPrefix(line, "for") || strings.HasPrefix(line, "while") ||
		strings.HasPrefix(line, "switch") || strings.HasPrefix(line, "return") || strings.HasPrefix(line, "throw") ||
		strings.HasPrefix(line, "catch") || strings.HasPrefix(line, "new ") {
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
	// A signature typically has at least one modifier or a return type
	// token before the name. A bare `foo(...)` at class level is either
	// a call (rejected) or a constructor (accepted only when the prefix
	// equals a simple identifier + matches a class on the stack — we
	// accept here and let parseJavaMethodSignature settle it).
	return !isJavaKeywordName(fields[0])
}

func javaParenBalance(text string) int {
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

func javaBraceBalance(text string) int {
	balance := 0
	inString := false
	inChar := false
	for i := 0; i < len(text); i++ {
		r := text[i]
		if r == '\\' {
			i++
			continue
		}
		if r == '"' && !inChar {
			inString = !inString
			continue
		}
		if r == '\'' && !inString {
			inChar = !inChar
			continue
		}
		if inString || inChar {
			continue
		}
		switch r {
		case '{':
			balance++
		case '}':
			balance--
		}
	}
	return balance
}

func popJavaClasses(stack *[]string, depth *int, line string) {
	// We approximate class-stack depth with brace depth. Every closing
	// brace that drops the depth below the stack length pops the top.
	opens := strings.Count(line, "{")
	closes := strings.Count(line, "}")
	net := opens - closes
	for net < 0 && len(*stack) > 0 {
		*stack = (*stack)[:len(*stack)-1]
		net++
	}
}

func stripJavaLineComment(line string) string {
	// Strip //... comments, honoring simple string quoting.
	inString := false
	for i := 0; i < len(line)-1; i++ {
		r := line[i]
		if r == '\\' {
			i++
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && line[i] == '/' && line[i+1] == '/' {
			return line[:i]
		}
	}
	return line
}

func isJavaPath(path string) bool {
	_, ok := javaFileExtensions[strings.ToLower(filepath.Ext(path))]
	return ok
}

func looksLikeJavaTestPath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(path, "/test/") || strings.Contains(path, "/tests/") ||
		strings.HasSuffix(path, "test.java") || strings.HasSuffix(path, "tests.java") ||
		strings.Contains(path, "src/test/java")
}

func javaSourceDirPriority(dir string) int {
	switch dir {
	case "src":
		return 30
	case "app":
		return 28
	case "lib":
		return 20
	case ".":
		return 10
	default:
		return 16
	}
}

func isJavaKeywordName(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "return", "catch", "new", "throw",
		"try", "do", "else", "synchronized", "assert", "case", "break",
		"continue", "goto", "instanceof", "super", "this":
		return true
	default:
		return false
	}
}

func javaTypeKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case "@interface":
		return "annotation"
	default:
		return strings.TrimSpace(raw)
	}
}

func javaQualifiedName(pkg string, classStack []string, name string) string {
	parts := make([]string, 0, 2+len(classStack))
	if pkg != "" {
		parts = append(parts, pkg)
	}
	parts = append(parts, classStack...)
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

func javaMemberQualifiedName(pkg string, classStack []string, name string) string {
	scope := javaQualifiedName(pkg, classStack, "")
	scope = strings.TrimSuffix(scope, ".")
	if scope == "" {
		return name
	}
	return scope + "#" + name
}

func extractJavaModifiers(before string) []string {
	keywords := map[string]struct{}{
		"public": {}, "protected": {}, "private": {},
		"static": {}, "final": {}, "abstract": {},
		"synchronized": {}, "native": {}, "default": {},
		"strictfp": {}, "transient": {}, "volatile": {},
	}
	out := make([]string, 0, 4)
	for _, tok := range strings.Fields(before) {
		if _, ok := keywords[tok]; ok {
			out = append(out, tok)
		}
	}
	return out
}

func stripJavaModifiers(before string) string {
	keywords := map[string]struct{}{
		"public": {}, "protected": {}, "private": {},
		"static": {}, "final": {}, "abstract": {},
		"synchronized": {}, "native": {}, "default": {},
		"strictfp": {}, "transient": {}, "volatile": {},
	}
	var out []string
	for _, tok := range strings.Fields(before) {
		if _, ok := keywords[tok]; ok {
			continue
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

func hasJavaModifier(modifiers []string, name string) bool {
	for _, m := range modifiers {
		if m == name {
			return true
		}
	}
	return false
}

func countJavaFieldLines(lines []string) int {
	// Approximate: count lines that look like field declarations.
	// (Used only as a display hint, not for routing.)
	count := 0
	for _, raw := range lines {
		trimmed := strings.TrimSpace(stripJavaLineComment(raw))
		if trimmed == "" || !strings.HasSuffix(trimmed, ";") {
			continue
		}
		if strings.Contains(trimmed, "(") {
			continue
		}
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "package ") {
			continue
		}
		count++
	}
	return count
}
