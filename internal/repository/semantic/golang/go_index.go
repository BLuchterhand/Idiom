package golang

import (
	"cmp"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic/internal/indexsupport"
)

type GoIndex struct {
	Root    string
	Module  string
	Files   []GoFile
	Package map[string]GoPackage
}

type GoFile struct {
	Path      string
	Package   string
	Imports   []string
	Symbols   []GoSymbol
	Funcs     int
	Methods   int
	Types     int
	HasMain   bool
	IsTest    bool
	SourceDir string
}

type GoSymbol struct {
	Name       string
	Kind       string
	Receiver   string
	Line       int
	Column     int
	StartLine  int
	EndLine    int
	Calls      []GoCall
	Package    string
	SourceFile string
}

type GoCall struct {
	Name      string
	Qualifier string
	Line      int
}

type GoPackage struct {
	Name    string
	Files   int
	Funcs   int
	Methods int
	Types   int
}

func BuildGoIndex(root string) (*GoIndex, error) {
	return BuildGoIndexWithOptions(root, indexsupport.IndexOptions{})
}

func BuildGoIndexWithOptions(root string, opts indexsupport.IndexOptions) (*GoIndex, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}

	scope := indexsupport.CleanIndexScope(opts.Scope)

	module := readGoModule(root)
	fset := token.NewFileSet()
	index := &GoIndex{
		Root:    root,
		Module:  module,
		Package: make(map[string]GoPackage),
	}

	errStop := filepath.SkipAll
	filesParsed := 0
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

		if filepath.Ext(path) != ".go" {
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

		fileNode, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		fileInfo := GoFile{
			Path:      rel,
			Package:   fileNode.Name.Name,
			Imports:   extractImports(fileNode),
			IsTest:    strings.HasSuffix(rel, "_test.go"),
			SourceDir: indexsupport.TopLevelDir(rel),
		}

		for _, decl := range fileNode.Decls {
			switch typed := decl.(type) {
			case *ast.FuncDecl:
				namePos := fset.Position(typed.Name.Pos())
				startPos := fset.Position(typed.Pos())
				endPos := fset.Position(typed.End())
				symbol := GoSymbol{
					Name:       typed.Name.Name,
					Kind:       "func",
					Line:       namePos.Line,
					Column:     namePos.Column,
					StartLine:  startPos.Line,
					EndLine:    endPos.Line,
					Package:    fileNode.Name.Name,
					SourceFile: rel,
				}
				fileInfo.Funcs++

				if typed.Recv != nil && len(typed.Recv.List) > 0 {
					symbol.Kind = "method"
					symbol.Receiver = receiverName(typed.Recv.List[0].Type)
					fileInfo.Methods++
				}

				symbol.Calls = extractCalls(fset, typed.Body)
				if fileNode.Name.Name == "main" && typed.Name.Name == "main" {
					fileInfo.HasMain = true
				}

				fileInfo.Symbols = append(fileInfo.Symbols, symbol)
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					switch typedSpec := spec.(type) {
					case *ast.TypeSpec:
						fileInfo.Types++

						namePos := fset.Position(typedSpec.Name.Pos())

						fileInfo.Symbols = append(fileInfo.Symbols, GoSymbol{
							Name:       typedSpec.Name.Name,
							Kind:       "type",
							Line:       namePos.Line,
							Column:     namePos.Column,
							StartLine:  fset.Position(typedSpec.Pos()).Line,
							EndLine:    fset.Position(typedSpec.End()).Line,
							Package:    fileNode.Name.Name,
							SourceFile: rel,
						})
					case *ast.ValueSpec:
						kind := "var"
						if typed.Tok == token.CONST {
							kind = "const"
						}

						for _, name := range typedSpec.Names {
							namePos := fset.Position(name.Pos())

							fileInfo.Symbols = append(fileInfo.Symbols, GoSymbol{
								Name:       name.Name,
								Kind:       kind,
								Line:       namePos.Line,
								Column:     namePos.Column,
								StartLine:  fset.Position(typedSpec.Pos()).Line,
								EndLine:    fset.Position(typedSpec.End()).Line,
								Package:    fileNode.Name.Name,
								SourceFile: rel,
							})
						}
					}
				}
			}
		}

		index.Files = append(index.Files, fileInfo)

		pkg := index.Package[fileInfo.PathDir()]

		pkg.Name = fileInfo.Package
		pkg.Files++

		pkg.Funcs += fileInfo.Funcs
		pkg.Methods += fileInfo.Methods
		pkg.Types += fileInfo.Types

		index.Package[fileInfo.PathDir()] = pkg
		filesParsed++

		if opts.MaxFiles > 0 && filesParsed >= opts.MaxFiles {
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

	if len(index.Files) == 0 {
		return nil, nil
	}

	slices.SortFunc(index.Files, func(a, b GoFile) int {
		return cmp.Compare(a.Path, b.Path)
	})

	return index, nil
}

func (idx *GoIndex) Summary() string {
	if idx == nil || len(idx.Files) == 0 {
		return ""
	}

	var b strings.Builder

	if idx.Module != "" {
		b.WriteString("Go module: ")
		b.WriteString(idx.Module)
		b.WriteString("\n")
	}

	b.WriteString("Indexed ")
	b.WriteString(indexsupport.IntString(len(idx.Files)))
	b.WriteString(" Go files")

	entrypoints := idx.DefaultFiles(3)
	if len(entrypoints) > 0 {
		b.WriteString("\nEntrypoints and likely anchors:\n")

		for _, path := range entrypoints {
			file := idx.fileByPath(path)
			if file == nil {
				continue
			}

			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString(" (package ")
			b.WriteString(file.Package)
			b.WriteString(", ")
			b.WriteString(indexsupport.IntString(file.Funcs + file.Methods))
			b.WriteString(" funcs/methods, ")
			b.WriteString(indexsupport.IntString(file.Types))
			b.WriteString(" types)\n")
		}
	}

	packages := idx.sortedPackages()
	if len(packages) > 0 {
		b.WriteString("Key packages:\n")

		for i, pkg := range packages {
			if i >= 5 {
				break
			}

			b.WriteString("- ")
			b.WriteString(pkg.path)
			b.WriteString(" (package ")
			b.WriteString(pkg.pkg.Name)
			b.WriteString(", ")
			b.WriteString(indexsupport.IntString(pkg.pkg.Files))
			b.WriteString(" files, ")
			b.WriteString(indexsupport.IntString(pkg.pkg.Funcs + pkg.pkg.Methods))
			b.WriteString(" funcs/methods)\n")
		}
	}

	symbolLines := 0
	for _, file := range idx.rankFiles(func(GoFile) int { return 1 }, 6) {
		if symbolLines >= 4 {
			break
		}

		if len(file.Symbols) == 0 {
			continue
		}

		names := make([]string, 0, 4)
		for _, symbol := range file.Symbols {
			names = append(names, symbolDisplayName(symbol))
			if len(names) >= 4 {
				break
			}
		}

		if len(names) == 0 {
			continue
		}

		if symbolLines == 0 {
			b.WriteString("Representative symbols:\n")
		}

		b.WriteString("- ")
		b.WriteString(file.Path)
		b.WriteString(": ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n")

		symbolLines++
	}

	return strings.TrimSpace(b.String())
}

func (idx *GoIndex) DefaultFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file GoFile) int {
		score := 0
		if file.HasMain {
			score += 100
		}

		score += sourceDirPriority(file.SourceDir)
		if strings.HasSuffix(file.Path, "/main.go") || strings.HasSuffix(file.Path, "main.go") {
			score += 25
		}

		if file.IsTest {
			score -= 30
		}

		score += min(file.Funcs+file.Methods, 20)
		score += min(file.Types*2, 10)

		return score
	}, limit), limit)
}

func (idx *GoIndex) RelevantFiles(terms []string, limit int) []string {
	normalized := indexsupport.NormalizeTerms(terms)
	if len(normalized) == 0 {
		return nil
	}

	return idx.uniqueRankedPaths(idx.rankFiles(func(file GoFile) int {
		score := 0
		pathLower := strings.ToLower(file.Path)
		pkgLower := strings.ToLower(file.Package)

		for _, term := range normalized {
			if strings.Contains(pathLower, term) {
				score += 14
			}

			if strings.Contains(pkgLower, term) {
				score += 10
			}

			for _, symbol := range file.Symbols {
				if strings.Contains(strings.ToLower(symbol.Name), term) {
					score += 8
				}
			}
		}

		score += sourceDirPriority(file.SourceDir)
		if file.IsTest {
			score -= 25
		}

		return score
	}, limit), limit)
}

func (idx *GoIndex) IssueHuntFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file GoFile) int {
		score := 0

		score += sourceDirPriority(file.SourceDir)

		if file.HasMain {
			score -= 10
		}

		if file.IsTest {
			score -= 35
		}

		score += min(file.Funcs+file.Methods*2, 24)
		score += min(file.Types, 10)

		pathLower := strings.ToLower(file.Path)

		if strings.Contains(pathLower, "app") || strings.Contains(pathLower, "manager") || strings.Contains(pathLower, "runner") || strings.Contains(pathLower, "client") {
			score += 6
		}

		return score
	}, limit), limit)
}

func (idx *GoIndex) SmellHuntFiles(limit int) []string {
	return idx.uniqueRankedPaths(idx.rankFiles(func(file GoFile) int {
		score := 0

		score += sourceDirPriority(file.SourceDir)

		if file.HasMain {
			score -= 20
		}

		if strings.HasSuffix(file.Path, "/main.go") || strings.HasSuffix(file.Path, "main.go") {
			score -= 12
		}

		if file.IsTest {
			score -= 40
		}

		surface := file.Funcs + file.Methods*2

		score += min(surface, 30)
		score += min(file.Types*3, 18)
		score += min(len(file.Imports), 12)
		score += min(exportedSymbolCount(file.Symbols), 12)

		pathLower := strings.ToLower(file.Path)
		if strings.Contains(pathLower, "manager") || strings.Contains(pathLower, "runner") || strings.Contains(pathLower, "workspace") || strings.Contains(pathLower, "controller") {
			score += 8
		}

		if strings.Contains(pathLower, "app") || strings.Contains(pathLower, "client") || strings.Contains(pathLower, "server") {
			score += 4
		}

		return score
	}, limit), limit)
}

type rankedPackage struct {
	path string
	pkg  GoPackage
}

func (idx *GoIndex) sortedPackages() []rankedPackage {
	pkgs := make([]rankedPackage, 0, len(idx.Package))
	for path, pkg := range idx.Package {
		pkgs = append(pkgs, rankedPackage{path: path, pkg: pkg})
	}

	slices.SortFunc(pkgs, func(a, b rankedPackage) int {
		left := a.pkg.Funcs + a.pkg.Methods + a.pkg.Types*2
		right := b.pkg.Funcs + b.pkg.Methods + b.pkg.Types*2

		if c := -cmp.Compare(left, right); c != 0 {
			return c
		}

		return cmp.Compare(a.path, b.path)
	})

	return pkgs
}

func (idx *GoIndex) rankFiles(scoreFn func(GoFile) int, limit int) []GoFile {
	return indexsupport.RankFiles(idx.Files, func(file GoFile) string { return file.Path }, scoreFn, limit)
}

func (idx *GoIndex) uniqueRankedPaths(files []GoFile, limit int) []string {
	return indexsupport.UniqueRankedPaths(files, func(file GoFile) string { return file.Path }, limit)
}

func exportedSymbolCount(symbols []GoSymbol) int {
	count := 0

	for _, symbol := range symbols {
		if symbol.Name == "" {
			continue
		}

		r := rune(symbol.Name[0])
		if r >= 'A' && r <= 'Z' {
			count++
		}
	}

	return count
}

func (idx *GoIndex) fileByPath(path string) *GoFile {
	path = indexsupport.NormalizeLookupPath(path)
	for i := range idx.Files {
		if indexsupport.NormalizeLookupPath(idx.Files[i].Path) == path {
			return &idx.Files[i]
		}
	}

	return nil
}

func (idx *GoIndex) FileByPath(path string) *GoFile {
	return idx.fileByPath(path)
}

func (f GoFile) PathDir() string {
	dir := filepath.ToSlash(filepath.Dir(f.Path))
	if dir == "." {
		return "."
	}

	return dir
}

func extractImports(file *ast.File) []string {
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if path != "" {
			imports = append(imports, path)
		}
	}

	slices.Sort(imports)

	return imports
}

func extractCalls(fset *token.FileSet, body *ast.BlockStmt) []GoCall {
	if body == nil {
		return nil
	}

	calls := make([]GoCall, 0)

	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		name, qualifier := callName(call.Fun)
		if name == "" {
			return true
		}

		pos := fset.Position(call.Lparen)
		if !pos.IsValid() {
			pos = fset.Position(call.Pos())
		}

		calls = append(calls, GoCall{
			Name:      name,
			Qualifier: qualifier,
			Line:      pos.Line,
		})

		return true
	})

	return calls
}

func callName(expr ast.Expr) (name string, qualifier string) {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name, ""
	case *ast.SelectorExpr:
		name = typed.Sel.Name
		qualifier = exprString(typed.X)

		return name, qualifier
	case *ast.IndexExpr:
		return callName(typed.X)
	case *ast.IndexListExpr:
		return callName(typed.X)
	case *ast.FuncLit:
		return "", ""
	default:
		return "", ""
	}
}

func exprString(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		prefix := exprString(typed.X)
		if prefix == "" {
			return typed.Sel.Name
		}

		return prefix + "." + typed.Sel.Name
	case *ast.StarExpr:
		return exprString(typed.X)
	case *ast.IndexExpr:
		return exprString(typed.X)
	case *ast.IndexListExpr:
		return exprString(typed.X)
	default:
		return ""
	}
}

func receiverName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.IndexListExpr:
		return receiverName(typed.X)
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func readGoModule(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}

	return ""
}

func sourceDirPriority(dir string) int {
	switch dir {
	case "internal":
		return 35
	case "cmd":
		return 30
	case "pkg":
		return 28
	case "src":
		return 24
	case ".":
		return 10
	default:
		return 14
	}
}

func symbolDisplayName(symbol GoSymbol) string {
	if symbol.Kind == "method" && symbol.Receiver != "" {
		return symbol.Receiver + "." + symbol.Name
	}

	return symbol.Name
}
