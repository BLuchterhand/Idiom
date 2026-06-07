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

// RustIndex is a lightweight, regex-based semantic index over the .rs
// files of a workspace. It mirrors the shape of CppIndex so the workspace
// tool surface can stay symmetric. A rust-analyzer LSP client handles the
// hard precision cases (references, implementations, call hierarchy);
// this index is the tree-walker / fallback layer.
type RustIndex struct {
	Root    string
	Files   []RustFile
	Forward map[string][]string // source -> modules it references
	Reverse map[string][]string // module -> files that reference it
}

type RustFile struct {
	Path      string
	Modules   []string // `mod foo;` declarations
	Uses      []string // `use a::b::c;` paths (normalized)
	Symbols   []RustSymbol
	Funcs     int
	Methods   int
	Types     int
	Impls     int
	Traits    int
	Macros    int
	HasMain   bool
	HasTest   bool
	HasBench  bool
	IsTest    bool
	IsLibRoot bool
	IsBinRoot bool
	SourceDir string
}

// RustSymbol is a declaration discovered in a file. Kind is one of:
// func, method, struct, enum, trait, impl, type, const, static, macro,
// mod, test, bench.
type RustSymbol struct {
	Name       string
	Qualified  string // e.g. `impl Foo for Bar::method` for methods
	Kind       string
	Scope      string // enclosing impl/trait/mod block text
	Line       int
	Column     int
	StartLine  int
	EndLine    int
	Calls      []RustCall
	SourceFile string
	Attrs      []string // the #[...] attributes directly preceding the item
}

type RustCall struct {
	Name string
	Line int
}

var (
	rustUseRe       = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z_][A-Za-z0-9_:]*(?:::\*|::\{[^}]*\})?)`)
	rustModRe       = regexp.MustCompile(`^\s*(?:pub\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)\s*;`)
	rustFnRe        = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+|const\s+|unsafe\s+|extern\s+(?:"[^"]+"\s+)?)*fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustStructRe    = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustEnumRe      = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustTraitRe     = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustTypeAliasRe = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?type\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustConstRe     = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?const\s+([A-Z_][A-Z0-9_]*)`)
	rustStaticRe    = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?static\s+(?:mut\s+)?([A-Z_][A-Z0-9_]*)`)
	rustMacroRe     = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?macro_rules!\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustImplRe      = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl(?:\s*<[^>]*>)?\s+(?:([A-Za-z_][A-Za-z0-9_:<>\s,]*)\s+for\s+)?([A-Za-z_][A-Za-z0-9_:<>\s,]*)`)
	rustCallRe      = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*(?:::\s*<[^>]*>\s*)?\(`)
)

var rustKeywordNames = map[string]struct{}{
	"if": {}, "else": {}, "for": {}, "while": {}, "loop": {}, "match": {},
	"return": {}, "break": {}, "continue": {}, "let": {}, "move": {},
	"as": {}, "ref": {}, "mut": {}, "fn": {}, "impl": {}, "trait": {},
	"struct": {}, "enum": {}, "type": {}, "mod": {}, "use": {}, "pub": {},
	"self": {}, "Self": {}, "super": {}, "crate": {},
	"async": {}, "await": {}, "dyn": {}, "box": {}, "yield": {},
	"true": {}, "false": {}, "unsafe": {}, "where": {}, "in": {},
	"Some": {}, "None": {}, "Ok": {}, "Err": {},
	"println": {}, "print": {}, "eprintln": {}, "eprint": {},
	"format": {}, "write": {}, "writeln": {}, "vec": {}, "panic": {},
	"assert": {}, "assert_eq": {}, "assert_ne": {}, "dbg": {},
}

func isRustPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".rs")
}

func looksLikeRustTestPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(p, "/tests/") || strings.HasSuffix(p, "/tests.rs") {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "test") || strings.HasPrefix(base, "bench")
}

func rustSourceDirPriority(dir string) int {
	switch dir {
	case "src":
		return 30
	case "examples":
		return 22
	case "benches":
		return 18
	case "tests":
		return 14
	case ".":
		return 10
	default:
		return 16
	}
}

// BuildRustIndex walks root looking for .rs files, parses declarations with
// conservative regexes, and returns a RustIndex. Returns nil, nil when no
// .rs files were found (lets callers surface a clean "no rust here" error).
func BuildRustIndex(root string) (*RustIndex, error) {
	return BuildRustIndexWithOptions(root, IndexOptions{})
}

func BuildRustIndexWithOptions(root string, opts IndexOptions) (*RustIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	scope := cleanIndexScope(opts.Scope)

	index := &RustIndex{
		Root:    root,
		Forward: make(map[string][]string),
		Reverse: make(map[string][]string),
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
			name := d.Name()
			if discovery.ShouldSkipDirName(name) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isRustPath(path) {
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
		modules, uses := parseRustUsesAndMods(content)
		symbols := parseRustSymbols(rel, content)

		file := RustFile{
			Path:      rel,
			Modules:   modules,
			Uses:      uses,
			Symbols:   symbols,
			IsTest:    looksLikeRustTestPath(rel),
			IsLibRoot: strings.HasSuffix(rel, "src/lib.rs") || rel == "lib.rs",
			IsBinRoot: strings.HasSuffix(rel, "src/main.rs") || rel == "main.rs",
			SourceDir: topLevelDir(rel),
		}
		for _, symbol := range symbols {
			switch symbol.Kind {
			case "func":
				file.Funcs++
				if symbol.Name == "main" {
					file.HasMain = true
				}
			case "method":
				file.Methods++
			case "struct", "enum", "type":
				file.Types++
			case "trait":
				file.Traits++
			case "impl":
				file.Impls++
			case "macro":
				file.Macros++
			case "test":
				file.HasTest = true
			case "bench":
				file.HasBench = true
			}
		}
		index.Files = append(index.Files, file)
	}

	slices.SortFunc(index.Files, func(a, b RustFile) int {
		return cmp.Compare(a.Path, b.Path)
	})

	// module -> file map via `mod foo;` declarations. We key the reverse
	// graph on the bare module name (not its fully-qualified path) since
	// that is what `related_files` needs to walk.
	for _, file := range index.Files {
		for _, mod := range file.Modules {
			index.Forward[file.Path] = util.AppendUnique(index.Forward[file.Path], mod)
			index.Reverse[mod] = util.AppendUnique(index.Reverse[mod], file.Path)
		}
	}
	return index, nil
}

func (idx *RustIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Indexed ")
	b.WriteString(intString(len(idx.Files)))
	b.WriteString(" Rust files\n")
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
			b.WriteString(intString(file.Funcs + file.Methods))
			b.WriteString(" fns, ")
			b.WriteString(intString(file.Types))
			b.WriteString(" types, ")
			b.WriteString(intString(file.Traits))
			b.WriteString(" traits, ")
			b.WriteString(intString(file.Impls))
			b.WriteString(" impls)\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (idx *RustIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file RustFile) int {
		score := 0
		if file.HasMain || file.IsBinRoot {
			score += 100
		}
		if file.IsLibRoot {
			score += 60
		}
		score += rustSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 30
		}
		score += min(file.Funcs+file.Methods*2, 24)
		score += min(file.Types*2, 16)
		score += min(file.Traits*3, 12)
		return score
	}, limit), limit)
}

func (idx *RustIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := normalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}
	return idx.uniqueRankedPaths(idx.rankFiles(func(file RustFile) int {
		score := 0
		pathLower := strings.ToLower(file.Path)
		for _, term := range normalized {
			if strings.Contains(pathLower, term) {
				score += 16
			}
			for _, mod := range file.Modules {
				if strings.EqualFold(mod, term) {
					score += 12
				}
			}
			for _, use := range file.Uses {
				if strings.Contains(strings.ToLower(use), term) {
					score += 4
				}
			}
			for _, symbol := range file.Symbols {
				if strings.Contains(strings.ToLower(symbol.Name), term) || strings.Contains(strings.ToLower(symbol.Qualified), term) {
					score += 10
				}
			}
		}
		score += rustSourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 20
		}
		return score
	}, limit), limit)
}

func (idx *RustIndex) FileByPath(path string) *RustFile {
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

func (idx *RustIndex) rankFiles(scoreFn func(RustFile) int, limit int) []RustFile {
	return RankFiles(idx.Files, scoreFn, limit)
}

func (idx *RustIndex) uniqueRankedPaths(files []RustFile, limit int) []string {
	return UniqueRankedPaths(files, limit)
}

// EntrypointFiles returns paths that look like Rust entrypoints:
// binary mains, lib roots, anything containing `#[test]` or `#[bench]`.
func (idx *RustIndex) EntrypointFiles(limit int) []string {
	if idx == nil {
		return nil
	}
	type scored struct {
		path  string
		score int
	}
	entries := make([]scored, 0)
	for _, file := range idx.Files {
		score := 0
		if file.HasMain || file.IsBinRoot {
			score += 50
		}
		if file.IsLibRoot {
			score += 30
		}
		if file.HasTest {
			score += 10
		}
		if file.HasBench {
			score += 8
		}
		if score == 0 {
			continue
		}
		entries = append(entries, scored{path: file.Path, score: score})
	}
	slices.SortFunc(entries, func(a, b scored) int {
		if c := -cmp.Compare(a.score, b.score); c != 0 {
			return c
		}
		return cmp.Compare(a.path, b.path)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]string, 0, len(entries))
	for _, item := range entries {
		out = append(out, item.path)
	}
	return out
}

func parseRustUsesAndMods(content string) ([]string, []string) {
	lines := strings.Split(content, "\n")
	mods := make([]string, 0, 4)
	uses := make([]string, 0, 8)
	for _, line := range lines {
		if match := rustModRe.FindStringSubmatch(line); len(match) == 2 {
			mods = util.AppendUnique(mods, match[1])
			continue
		}
		if match := rustUseRe.FindStringSubmatch(line); len(match) == 2 {
			uses = util.AppendUnique(uses, match[1])
		}
	}
	slices.Sort(mods)
	slices.Sort(uses)
	return mods, uses
}

func parseRustSymbols(rel string, content string) []RustSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]RustSymbol, 0, 16)

	// Track impl/trait block scope so methods get a qualified name.
	type scope struct {
		kind    string // "impl" or "trait" or "mod"
		name    string // e.g. "Foo" for `impl Foo` or "Bar for Foo" descriptor
		endLine int
	}
	var scopes []scope

	// Track pending attributes so #[test]/#[bench] promote the next fn.
	var pendingAttrs []string

	for i, raw := range lines {
		lineNo := i + 1
		line := stripRustLineComment(raw)
		trimmed := strings.TrimSpace(line)

		// Pop any closed scopes.
		for len(scopes) > 0 && scopes[len(scopes)-1].endLine <= lineNo {
			scopes = scopes[:len(scopes)-1]
		}

		if trimmed == "" {
			continue
		}

		// Attribute capture. Multi-line attrs are collapsed to one string.
		if strings.HasPrefix(trimmed, "#[") || strings.HasPrefix(trimmed, "#![") {
			pendingAttrs = append(pendingAttrs, trimmed)
			continue
		}

		currentScope := ""
		if len(scopes) > 0 {
			currentScope = scopes[len(scopes)-1].name
		}

		// impl blocks
		if match := rustImplRe.FindStringSubmatch(trimmed); len(match) == 3 {
			end := findRustBraceEnd(lines, i)
			var name string
			if strings.TrimSpace(match[1]) != "" {
				// trait impl: `impl Trait for Type`
				trait := strings.TrimSpace(match[1])
				target := strings.TrimSpace(match[2])
				name = trait + " for " + target
				symbols = append(symbols, RustSymbol{
					Name:       target,
					Qualified:  name,
					Kind:       "impl",
					Line:       lineNo,
					Column:     1,
					StartLine:  lineNo,
					EndLine:    end,
					SourceFile: rel,
					Scope:      currentScope,
					Attrs:      takeAttrs(&pendingAttrs),
				})
			} else {
				// inherent impl: `impl Type`
				name = strings.TrimSpace(match[2])
				symbols = append(symbols, RustSymbol{
					Name:       name,
					Qualified:  name,
					Kind:       "impl",
					Line:       lineNo,
					Column:     1,
					StartLine:  lineNo,
					EndLine:    end,
					SourceFile: rel,
					Scope:      currentScope,
					Attrs:      takeAttrs(&pendingAttrs),
				})
			}
			if strings.Contains(trimmed, "{") {
				scopes = append(scopes, scope{kind: "impl", name: name, endLine: end})
			}
			continue
		}

		// trait blocks
		if match := rustTraitRe.FindStringSubmatch(trimmed); len(match) == 2 {
			end := findRustBraceEnd(lines, i)
			symbols = append(symbols, RustSymbol{
				Name:       match[1],
				Qualified:  match[1],
				Kind:       "trait",
				Line:       lineNo,
				Column:     1,
				StartLine:  lineNo,
				EndLine:    end,
				SourceFile: rel,
				Scope:      currentScope,
				Attrs:      takeAttrs(&pendingAttrs),
			})
			if strings.Contains(trimmed, "{") {
				scopes = append(scopes, scope{kind: "trait", name: match[1], endLine: end})
			}
			continue
		}

		// structs, enums, type aliases, const, static, macro_rules
		if match := rustStructRe.FindStringSubmatch(trimmed); len(match) == 2 {
			end := lineNo
			if strings.Contains(trimmed, "{") {
				end = findRustBraceEnd(lines, i)
			} else if strings.HasSuffix(trimmed, ";") {
				end = lineNo
			}
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "struct",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: end,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}
		if match := rustEnumRe.FindStringSubmatch(trimmed); len(match) == 2 {
			end := lineNo
			if strings.Contains(trimmed, "{") {
				end = findRustBraceEnd(lines, i)
			}
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "enum",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: end,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}
		if match := rustTypeAliasRe.FindStringSubmatch(trimmed); len(match) == 2 {
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "type",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: lineNo,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}
		if match := rustConstRe.FindStringSubmatch(trimmed); len(match) == 2 {
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "const",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: lineNo,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}
		if match := rustStaticRe.FindStringSubmatch(trimmed); len(match) == 2 {
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "static",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: lineNo,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}
		if match := rustMacroRe.FindStringSubmatch(trimmed); len(match) == 2 {
			end := lineNo
			if strings.Contains(trimmed, "{") {
				end = findRustBraceEnd(lines, i)
			}
			symbols = append(symbols, RustSymbol{
				Name: match[1], Qualified: match[1], Kind: "macro",
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: end,
				SourceFile: rel, Scope: currentScope, Attrs: takeAttrs(&pendingAttrs),
			})
			continue
		}

		// functions (free) and methods (inside an impl/trait block)
		if match := rustFnRe.FindStringSubmatch(trimmed); len(match) == 2 {
			name := match[1]
			end := lineNo
			if strings.Contains(trimmed, "{") {
				end = findRustBraceEnd(lines, i)
			} else {
				end = findRustFnBodyEnd(lines, i)
			}
			kind := "func"
			qualified := name
			if len(scopes) > 0 {
				top := scopes[len(scopes)-1]
				if top.kind == "impl" || top.kind == "trait" {
					kind = "method"
					qualified = top.name + "::" + name
				}
			}
			attrs := takeAttrs(&pendingAttrs)
			if kind == "func" {
				for _, attr := range attrs {
					lower := strings.ToLower(attr)
					if strings.Contains(lower, "#[test") {
						kind = "test"
						break
					}
					if strings.Contains(lower, "#[bench") {
						kind = "bench"
						break
					}
				}
			}
			calls := extractRustCalls(lines, lineNo, end)
			symbols = append(symbols, RustSymbol{
				Name: name, Qualified: qualified, Kind: kind, Scope: currentScope,
				Line: lineNo, Column: 1, StartLine: lineNo, EndLine: end,
				SourceFile: rel, Calls: calls, Attrs: attrs,
			})
			continue
		}

		// An inline `mod foo { ... }` block defines a scope (but `mod foo;`
		// was already consumed above).
		if strings.HasPrefix(trimmed, "mod ") && strings.Contains(trimmed, "{") {
			if idx := strings.Index(trimmed, "mod "); idx >= 0 {
				rest := strings.TrimSpace(trimmed[idx+4:])
				fields := strings.FieldsFunc(rest, func(r rune) bool {
					return !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
				})
				if len(fields) > 0 {
					end := findRustBraceEnd(lines, i)
					scopes = append(scopes, scope{kind: "mod", name: fields[0], endLine: end})
				}
			}
			// non-fatal; do not clear pendingAttrs here.
			continue
		}

		// Any non-whitespace, non-attribute line without a match resets
		// the pending attribute window so attrs only attach to their
		// immediate successor.
		if !strings.HasPrefix(trimmed, "//") && trimmed != "{" && trimmed != "}" {
			pendingAttrs = nil
		}
	}

	slices.SortFunc(symbols, func(a, b RustSymbol) int {
		if c := cmp.Compare(a.SourceFile, b.SourceFile); c != 0 {
			return c
		}
		return cmp.Compare(a.Line, b.Line)
	})
	return symbols
}

func takeAttrs(pending *[]string) []string {
	if pending == nil || len(*pending) == 0 {
		return nil
	}
	out := make([]string, len(*pending))
	copy(out, *pending)
	*pending = nil
	return out
}

func extractRustCalls(lines []string, startLine int, endLine int) []RustCall {
	if startLine <= 0 || endLine < startLine || endLine > len(lines) {
		return nil
	}
	calls := make([]RustCall, 0, 16)
	for line := startLine; line <= endLine; line++ {
		text := stripRustLineComment(lines[line-1])
		for _, match := range rustCallRe.FindAllStringSubmatch(text, -1) {
			if len(match) != 2 {
				continue
			}
			name := match[1]
			if _, keyword := rustKeywordNames[name]; keyword {
				continue
			}
			calls = append(calls, RustCall{Name: name, Line: line})
		}
	}
	return calls
}

func findRustBraceEnd(lines []string, startIdx int) int {
	depth := 0
	seen := false
	inString := false
	inRawString := false
	rawHashes := 0
	for i := startIdx; i < len(lines); i++ {
		line := stripRustLineComment(lines[i])
		runes := []rune(line)
		for j := 0; j < len(runes); j++ {
			r := runes[j]
			if inRawString {
				if r == '"' {
					// close if followed by the right number of #
					closing := true
					for k := 1; k <= rawHashes; k++ {
						if j+k >= len(runes) || runes[j+k] != '#' {
							closing = false
							break
						}
					}
					if closing {
						j += rawHashes
						inRawString = false
						rawHashes = 0
					}
				}
				continue
			}
			if inString {
				if r == '\\' && j+1 < len(runes) {
					j++
					continue
				}
				if r == '"' {
					inString = false
				}
				continue
			}
			if r == 'r' {
				// detect raw string: r#*"
				k := j + 1
				hashes := 0
				for k < len(runes) && runes[k] == '#' {
					hashes++
					k++
				}
				if k < len(runes) && runes[k] == '"' {
					inRawString = true
					rawHashes = hashes
					j = k
					continue
				}
			}
			if r == '"' {
				inString = true
				continue
			}
			if r == '{' {
				depth++
				seen = true
			} else if r == '}' {
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

// findRustFnBodyEnd picks up bodies whose opening brace is on a following
// line (common for long generic signatures). If we can't find a body, we
// return the signature line itself — parseRustSymbols handles ";" trait
// declarations this way too.
func findRustFnBodyEnd(lines []string, startIdx int) int {
	for i := startIdx; i < len(lines) && i < startIdx+20; i++ {
		trimmed := strings.TrimSpace(stripRustLineComment(lines[i]))
		if strings.HasSuffix(trimmed, ";") {
			return i + 1
		}
		if strings.Contains(trimmed, "{") {
			return findRustBraceEnd(lines, i)
		}
	}
	return startIdx + 1
}

func stripRustLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return line[:idx]
	}
	return line
}
