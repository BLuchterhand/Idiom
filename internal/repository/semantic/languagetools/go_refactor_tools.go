package languagetools

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type goParsedFile struct {
	path    string
	display string
	source  []byte
	fset    *token.FileSet
	file    *ast.File
	imports map[string]goImportDecl
	decls   []goTopDecl
}

type goImportDecl struct {
	name string
	path string
	text string
}

type goTopDecl struct {
	names   []string
	start   int
	end     int
	line    int
	endLine int
	text    string
	decl    ast.Decl
}

func (m *Manager) goFileOutline(req ToolRequest) ToolResult {
	path, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "go_file_outline", Summary: "Go file outline rejected", Error: err.Error()}
	}

	parsed, err := m.parseGoFileForRefactor(path)
	if err != nil {
		return ToolResult{Tool: "go_file_outline", Summary: "Go file outline unavailable", Error: err.Error()}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "package %s\n", parsed.file.Name.Name)

	if len(parsed.imports) > 0 {
		b.WriteString("imports:\n")

		importNames := make([]string, 0, len(parsed.imports))
		for name := range parsed.imports {
			importNames = append(importNames, name)
		}

		slices.Sort(importNames)

		for _, name := range importNames {
			imp := parsed.imports[name]
			fmt.Fprintf(&b, "- %s => %s\n", name, imp.path)
		}
	}

	batches := goOutlineMoveBatches(parsed)
	if len(batches) > 0 {
		b.WriteString("structural move batches:\n")

		for _, batch := range batches {
			fmt.Fprintf(&b, "- tag: %s destination: %s\n", batch.tag, batch.destination)
			fmt.Fprintf(&b, "  preview declarations: [%s]\n", strings.Join(limitedGoDeclarationNames(batch.declarations, 24), ", "))
		}
	}

	if len(parsed.decls) > 200 {
		fmt.Fprintf(&b, "declarations: omitted for large file (%d top-level declarations).\n", len(parsed.decls))
	} else {
		b.WriteString("declarations:\n")

		for _, decl := range parsed.decls {
			fmt.Fprintf(
				&b,
				"- %s lines %d-%d tags: %s\n",
				strings.Join(decl.names, ", "),
				decl.line,
				decl.endLine,
				strings.Join(goDeclResponsibilityTags(decl), ", "),
			)
		}
	}

	summary := fmt.Sprintf("Outlined %d top-level declaration(s) in %s.", len(parsed.decls), parsed.display)
	if len(batches) > 0 {
		summary += " Includes declaration tags and suggested move batches in the output."
	}

	return ToolResult{
		Tool:    "go_file_outline",
		Summary: summary,
		Output:  strings.TrimSpace(b.String()),
	}
}

type goOutlineMoveBatch struct {
	destination  string
	tag          string
	declarations []string
}

func goOutlineMoveBatches(parsed *goParsedFile) []goOutlineMoveBatch {
	if parsed == nil {
		return nil
	}

	sourceDir := filepath.Dir(parsed.display)
	groups := make(map[string][]string)

	for _, decl := range parsed.decls {
		tags := goDeclResponsibilityTags(decl)
		if len(tags) == 0 || tags[0] == "core" {
			continue
		}

		file := goResponsibilityFile(tags[0])
		if file == "" {
			continue
		}

		destination := filepath.Join(sourceDir, file)
		if filepath.Clean(destination) == filepath.Clean(parsed.display) {
			continue
		}

		groups[file] = append(groups[file], decl.names...)
	}

	files := make([]string, 0, len(groups))
	for file := range groups {
		files = append(files, file)
	}

	slices.Sort(files)

	batches := make([]goOutlineMoveBatch, 0, len(files))
	for _, file := range files {
		names := groups[file]
		if len(names) == 0 {
			continue
		}

		slices.Sort(names)

		batches = append(batches, goOutlineMoveBatch{
			destination:  filepath.Join(sourceDir, file),
			tag:          strings.TrimSuffix(file, ".go"),
			declarations: names,
		})
	}

	return batches
}

func goResponsibilityFile(tag string) string {
	switch tag {
	case "analysis":
		return "analysis_agents.go"
	case "control-rails":
		return "control_rails.go"
	case "implementation":
		return "implementation.go"
	case "lifecycle":
		return "lifecycle.go"
	case "model-state":
		return "model_state.go"
	case "prompt-routing":
		return "prompt_routing.go"
	case "rendering":
		return "rendering.go"
	case "task-memory":
		return "task_memory.go"
	case "write-guard":
		return "write_guard.go"
	default:
		return ""
	}
}

func limitedGoDeclarationNames(names []string, limit int) []string {
	if len(names) <= limit {
		return names
	}

	out := append([]string(nil), names[:limit]...)

	out = append(out, fmt.Sprintf("...%d more", len(names)-limit))

	return out
}

func goMovePatternTag(pattern string) string {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	pattern = strings.TrimPrefix(pattern, "responsibility:")
	pattern = strings.TrimPrefix(pattern, "tag:")
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimSuffix(pattern, ".go")

	switch pattern {
	case "analysis_agents":
		return "analysis"
	case "control_rails":
		return "control-rails"
	case "model_state":
		return "model-state"
	case "prompt_routing":
		return "prompt-routing"
	case "task_memory":
		return "task-memory"
	case "write_guard":
		return "write-guard"
	default:
		return pattern
	}
}

func (m *Manager) goMoveDeclarations(req ToolRequest) ToolResult {
	if !m.host.WritesEnabled() {
		return ToolResult{Tool: "go_move_declarations", Summary: "Go declaration move rejected", Error: "workspace writes are disabled"}
	}
	if strings.TrimSpace(req.Path) == "" {
		return ToolResult{Tool: "go_move_declarations", Summary: "Go declaration batch move rejected", Error: "path is required"}
	}

	sourcePath, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "go_move_declarations", Summary: "Failed to resolve source path", Error: err.Error()}
	}
	planning := newGoMovePlanningSession(m, sourcePath, req.MutationPreimages)
	moves := req.Moves
	if len(moves) == 0 {
		moves = []GoDeclarationMove{{Destination: req.Destination, Pattern: req.Pattern, Declarations: append([]string(nil), req.Declarations...)}}
	}

	lines := make([]string, 0, len(moves))
	var single goMoveResult
	for _, move := range moves {
		destPath, resolveErr := m.resolvePath(move.Destination, req.Workdir)
		if resolveErr != nil {
			return ToolResult{Tool: "go_move_declarations", Summary: "Failed to resolve destination path", Error: resolveErr.Error()}
		}
		moveResult, moveErr := planning.apply(destPath, move.Pattern, move.Declarations)
		if moveErr != nil {
			return ToolResult{Tool: "go_move_declarations", Summary: moveErr.summary, Error: moveErr.err.Error()}
		}
		single = moveResult
		lines = append(lines, moveResult.summary)
	}

	plan, targetPaths := planning.plan()
	if len(req.Moves) == 0 {
		return ToolResult{
			Tool:         "go_move_declarations",
			Summary:      single.summary,
			Output:       fmt.Sprintf("moved declarations: %s\nplanned files:\n- %s", strings.Join(single.movedNames, ", "), strings.Join(targetPaths, "\n- ")),
			MutationPlan: plan,
		}
	}

	return ToolResult{
		Tool:         "go_move_declarations",
		Summary:      fmt.Sprintf("Applied %d Go declaration move batch(es)", len(req.Moves)),
		Output:       strings.Join(append(lines, "planned files:", "- "+strings.Join(targetPaths, "\n- ")), "\n"),
		MutationPlan: plan,
	}
}

type goMovePlanningFile struct {
	path           string
	display        string
	expectedExists bool
	expectedBody   []byte
	body           []byte
}

type goMovePlanningSession struct {
	manager           *Manager
	sourcePath        string
	files             map[string]*goMovePlanningFile
	boundPreimages    map[string]FileMutationPreimage
	preimagesSupplied bool
}

type goMoveResult struct {
	summary    string
	movedNames []string
}

type goMovePlanningError struct {
	summary string
	err     error
}

func newGoMovePlanningSession(manager *Manager, sourcePath string, preimages []FileMutationPreimage) *goMovePlanningSession {
	bound := make(map[string]FileMutationPreimage, len(preimages))
	for _, preimage := range preimages {
		preimage.Path = filepath.Clean(preimage.Path)
		preimage.Body = append([]byte(nil), preimage.Body...)
		bound[preimage.Path] = preimage
	}
	return &goMovePlanningSession{
		manager:           manager,
		sourcePath:        filepath.Clean(sourcePath),
		files:             make(map[string]*goMovePlanningFile),
		boundPreimages:    bound,
		preimagesSupplied: preimages != nil,
	}
}

func (p *goMovePlanningSession) load(path string) (*goMovePlanningFile, error) {
	path = filepath.Clean(path)
	if file := p.files[path]; file != nil {
		return file, nil
	}

	var body []byte
	var exists bool
	if p.preimagesSupplied {
		preimage, ok := p.boundPreimages[path]
		if !ok {
			return nil, fmt.Errorf("rooted mutation preimage for %s was not supplied", p.manager.displayPath(path))
		}
		body = append([]byte(nil), preimage.Body...)
		exists = preimage.Exists
	} else {
		return nil, fmt.Errorf("rooted mutation preimages are required for Go declaration moves")
	}
	file := &goMovePlanningFile{
		path:           path,
		display:        p.manager.displayPath(path),
		expectedExists: exists,
		expectedBody:   append([]byte(nil), body...),
		body:           append([]byte(nil), body...),
	}
	p.files[path] = file
	return file, nil
}

func (p *goMovePlanningSession) apply(destPath string, pattern string, declarations []string) (goMoveResult, *goMovePlanningError) {
	destPath = filepath.Clean(destPath)
	if p.sourcePath == destPath {
		return goMoveResult{}, &goMovePlanningError{summary: "Go declaration move rejected", err: fmt.Errorf("destination must be different from source path")}
	}

	requested := normalizedGoDeclarationSet(declarations)
	if len(requested) == 0 && strings.TrimSpace(pattern) != "" && !strings.Contains(strings.ToLower(pattern), "tag:") && !strings.Contains(strings.ToLower(pattern), "responsibility:") {
		requested = normalizedGoDeclarationSet(strings.Split(pattern, ","))
	}
	tag := goMovePatternTag(pattern)
	if len(requested) == 0 && tag == "" {
		return goMoveResult{}, &goMovePlanningError{summary: "Go declaration move rejected", err: fmt.Errorf("declarations or pattern:\"tag:<responsibility>\" is required")}
	}

	sourceState, err := p.load(p.sourcePath)
	if err != nil || !sourceState.expectedExists {
		if err == nil {
			err = os.ErrNotExist
		}
		return goMoveResult{}, &goMovePlanningError{summary: "Failed to parse source Go file", err: err}
	}
	source, err := p.manager.parseGoSourceForRefactor(sourceState.path, sourceState.body)
	if err != nil {
		return goMoveResult{}, &goMovePlanningError{summary: "Failed to parse source Go file", err: err}
	}
	destState, err := p.load(destPath)
	if err != nil {
		return goMoveResult{}, &goMovePlanningError{summary: "Failed to read destination Go file", err: err}
	}
	var dest *goParsedFile
	if destState.expectedExists || len(destState.body) > 0 {
		dest, err = p.manager.parseGoSourceForRefactor(destState.path, destState.body)
		if err != nil {
			return goMoveResult{}, &goMovePlanningError{summary: "Failed to parse destination Go file", err: err}
		}
		if source.file.Name.Name != dest.file.Name.Name {
			return goMoveResult{}, &goMovePlanningError{summary: "Go declaration move rejected", err: fmt.Errorf("source package %q does not match destination package %q", source.file.Name.Name, dest.file.Name.Name)}
		}
	}

	sourceDecls := make([]goTopDecl, 0, len(source.decls))
	movedDecls := make([]goTopDecl, 0, len(requested))
	matched := make(map[string]struct{}, len(requested))
	for _, decl := range source.decls {
		matches := goDeclMatchesRequested(decl, requested, matched)
		if tag != "" {
			matches = goDeclHasResponsibilityTag(decl, tag)
		}
		if matches {
			movedDecls = append(movedDecls, decl)
		} else {
			sourceDecls = append(sourceDecls, decl)
		}
	}
	if len(movedDecls) == 0 {
		want := strings.Join(declarations, ", ")
		if tag != "" {
			want = "tag:" + tag
		}
		return goMoveResult{}, &goMovePlanningError{summary: "No matching Go declarations found", err: fmt.Errorf("no source declarations matched %s", want)}
	}
	if tag == "" && len(matched) != len(requested) {
		missing := make([]string, 0, len(requested)-len(matched))
		for requestedName := range requested {
			if _, ok := matched[requestedName]; !ok {
				missing = append(missing, requestedName)
			}
		}
		slices.Sort(missing)
		return goMoveResult{}, &goMovePlanningError{summary: "Go declaration move rejected", err: fmt.Errorf("source declarations not found: %s", strings.Join(missing, ", "))}
	}

	destDecls := movedDecls
	destImports := cloneGoImports(source.imports)
	if dest != nil {
		destNames := make(map[string]struct{})
		for _, decl := range dest.decls {
			for _, name := range decl.names {
				destNames[normalizeGoDeclarationName(name)] = struct{}{}
			}
		}
		for _, decl := range movedDecls {
			for _, name := range decl.names {
				if _, exists := destNames[normalizeGoDeclarationName(name)]; exists {
					return goMoveResult{}, &goMovePlanningError{summary: "Go declaration move rejected", err: fmt.Errorf("destination already declares %s", name)}
				}
			}
		}
		destDecls = append(append([]goTopDecl(nil), dest.decls...), movedDecls...)
		destImports = cloneGoImports(dest.imports)
		for name, imp := range source.imports {
			destImports[name] = imp
		}
	}

	sourceBytes, err := renderGoDeclFile(source.file.Name.Name, source.imports, sourceDecls)
	if err != nil {
		return goMoveResult{}, &goMovePlanningError{summary: "Failed to render source Go file", err: err}
	}
	destBytes, err := renderGoDeclFile(source.file.Name.Name, destImports, destDecls)
	if err != nil {
		return goMoveResult{}, &goMovePlanningError{summary: "Failed to render destination Go file", err: err}
	}
	sourceState.body = sourceBytes
	destState.body = destBytes

	movedNames := make([]string, 0, len(movedDecls))
	for _, decl := range movedDecls {
		movedNames = append(movedNames, decl.names...)
	}
	return goMoveResult{
		summary:    fmt.Sprintf("Moved %d Go declaration block(s) from %s to %s", len(movedDecls), source.display, destState.display),
		movedNames: movedNames,
	}, nil
}

func (p *goMovePlanningSession) plan() (*MutationPlan, []string) {
	var destinations []*goMovePlanningFile
	var source *goMovePlanningFile
	var changedPaths []string
	for _, file := range p.files {
		if slices.Equal(file.expectedBody, file.body) && file.expectedExists {
			continue
		}
		changedPaths = append(changedPaths, file.display)
		if file.path == p.sourcePath {
			source = file
		} else {
			destinations = append(destinations, file)
		}
	}
	slices.Sort(changedPaths)
	slices.SortFunc(destinations, func(a, b *goMovePlanningFile) int { return strings.Compare(a.display, b.display) })
	ordered := destinations
	if source != nil {
		ordered = append(ordered, source)
	}
	plan := &MutationPlan{TargetPaths: append([]string(nil), changedPaths...), Files: make([]FileMutationPlan, 0, len(ordered))}
	for _, file := range ordered {
		description := "write Go move destination " + file.display
		if file.path == p.sourcePath {
			description = "rewrite Go move source " + file.display
		}
		plan.Files = append(plan.Files, FileMutationPlan{
			Path:           file.path,
			DisplayPath:    file.display,
			Description:    description,
			ExpectedExists: file.expectedExists,
			ExpectedBody:   append([]byte(nil), file.expectedBody...),
			RenderedBody:   append([]byte(nil), file.body...),
		})
	}
	return plan, changedPaths
}

func (m *Manager) parseGoFileForRefactor(path string) (*goParsedFile, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return m.parseGoSourceForRefactor(path, source)
}

func (m *Manager) parseGoSourceForRefactor(path string, source []byte) (*goParsedFile, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	parsed := &goParsedFile{
		path:    path,
		display: m.displayPath(path),
		source:  source,
		fset:    fset,
		file:    file,
		imports: make(map[string]goImportDecl),
		decls:   make([]goTopDecl, 0, len(file.Decls)),
	}
	for _, spec := range file.Imports {
		imp := goImportDecl{
			name: goImportSpecName(spec),
			path: spec.Path.Value,
			text: goImportSpecText(source, fset, spec),
		}

		parsed.imports[imp.name] = imp
	}

	for _, decl := range file.Decls {
		if _, ok := decl.(*ast.GenDecl); ok && len(goDeclNames(decl)) == 0 {
			continue
		}

		start, end := goDeclOffsets(source, fset, decl)
		if start < 0 || end > len(source) || start >= end {
			continue
		}

		parsed.decls = append(parsed.decls, goTopDecl{
			names:   goDeclNames(decl),
			start:   start,
			end:     end,
			line:    fset.Position(decl.Pos()).Line,
			endLine: fset.Position(decl.End()).Line,
			text:    strings.TrimSpace(string(source[start:end])),
			decl:    decl,
		})
	}

	return parsed, nil
}

func goImportSpecName(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}

	path := strings.Trim(spec.Path.Value, `"`)
	base := filepath.Base(path)

	base = strings.ReplaceAll(base, "-", "_")

	return base
}

func goImportSpecText(source []byte, fset *token.FileSet, spec *ast.ImportSpec) string {
	start := fset.Position(spec.Pos()).Offset
	end := fset.Position(spec.End()).Offset

	if spec.Comment != nil {
		end = fset.Position(spec.Comment.End()).Offset
	}

	if start < 0 || end > len(source) || start >= end {
		return ""
	}

	return strings.TrimSpace(string(source[start:end]))
}

func goDeclNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil || len(d.Recv.List) == 0 {
			return []string{d.Name.Name}
		}

		receiver := goReceiverTypeName(d.Recv.List[0].Type)
		if receiver == "" {
			return []string{d.Name.Name}
		}

		return []string{receiver + "." + d.Name.Name}
	case *ast.GenDecl:
		names := make([]string, 0, len(d.Specs))
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, name := range s.Names {
					names = append(names, name.Name)
				}
			}
		}

		return names
	default:
		return nil
	}
}

func goReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return goReceiverTypeName(t.X)
	case *ast.IndexExpr:
		return goReceiverTypeName(t.X)
	case *ast.IndexListExpr:
		return goReceiverTypeName(t.X)
	default:
		return ""
	}
}

func goDeclOffsets(source []byte, fset *token.FileSet, decl ast.Decl) (int, int) {
	start := fset.Position(decl.Pos()).Offset
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Doc != nil {
			start = fset.Position(d.Doc.Pos()).Offset
		}
	case *ast.GenDecl:
		if d.Doc != nil {
			start = fset.Position(d.Doc.Pos()).Offset
		}
	}

	end := fset.Position(decl.End()).Offset
	for end < len(source) && (source[end] == '\n' || source[end] == '\r') {
		end++
		if end < len(source) && source[end-1] == '\n' {
			break
		}
	}

	return start, end
}

func normalizedGoDeclarationSet(names []string) map[string]struct{} {
	out := make(map[string]struct{})

	for _, name := range names {
		if normalized := normalizeGoDeclarationName(name); normalized != "" {
			out[normalized] = struct{}{}
		}
	}

	return out
}

func normalizeGoDeclarationName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "func ")
	name = strings.TrimPrefix(name, "type ")
	name = strings.TrimPrefix(name, "var ")
	name = strings.TrimPrefix(name, "const ")
	name = strings.ReplaceAll(name, "(*", "")
	name = strings.ReplaceAll(name, ")", "")
	name = strings.ReplaceAll(name, "*", "")
	name = strings.Join(strings.Fields(name), "")

	return strings.ToLower(name)
}

func goDeclMatchesRequested(decl goTopDecl, requested map[string]struct{}, matched map[string]struct{}) bool {
	found := false

	for _, name := range decl.names {
		normalized := normalizeGoDeclarationName(name)
		if _, ok := requested[normalized]; ok {
			matched[normalized] = struct{}{}
			found = true
		}
	}

	return found
}

func goDeclHasResponsibilityTag(decl goTopDecl, tag string) bool {
	tag = goMovePatternTag(tag)
	for _, candidate := range goDeclResponsibilityTags(decl) {
		if goMovePatternTag(candidate) == tag {
			return true
		}
	}

	return false
}

func renderGoDeclFile(pkg string, imports map[string]goImportDecl, decls []goTopDecl) ([]byte, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "package %s\n\n", pkg)

	needed := goNeededImports(imports, decls)
	if len(needed) > 0 {
		b.WriteString("import (\n")

		for _, imp := range needed {
			if strings.TrimSpace(imp.text) != "" {
				b.WriteString("\t")
				b.WriteString(imp.text)
				b.WriteString("\n")
			}
		}

		b.WriteString(")\n\n")
	}

	for i, decl := range decls {
		if i > 0 {
			b.WriteString("\n")
		}

		b.WriteString(strings.TrimSpace(decl.text))
		b.WriteString("\n")
	}

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, err
	}

	return formatted, nil
}

func goNeededImports(imports map[string]goImportDecl, decls []goTopDecl) []goImportDecl {
	used := make(map[string]struct{})

	for _, decl := range decls {
		ast.Inspect(decl.decl, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				if ident, ok := n.X.(*ast.Ident); ok {
					used[ident.Name] = struct{}{}
				}
			}

			return true
		})
	}

	needed := make([]goImportDecl, 0, len(imports))
	for name, imp := range imports {
		if name == "_" || name == "." {
			needed = append(needed, imp)
			continue
		}

		if _, ok := used[name]; ok {
			needed = append(needed, imp)
		}
	}

	slices.SortFunc(needed, func(a, b goImportDecl) int {
		return cmp.Compare(a.path, b.path)
	})

	return needed
}

func cloneGoImports(imports map[string]goImportDecl) map[string]goImportDecl {
	out := make(map[string]goImportDecl, len(imports))
	for name, imp := range imports {
		out[name] = imp
	}

	return out
}

func goDeclResponsibilityTags(decl goTopDecl) []string {
	if containsGoDeclName(strings.ToLower(strings.Join(decl.names, " ")), "locate") {
		return []string{"core"}
	}

	if tag := goDeclNameResponsibilityTag(decl.names); tag != "" {
		return []string{tag}
	}

	text := strings.ToLower(strings.Join(decl.names, " ") + " " + decl.text)
	tags := make([]string, 0, 3)
	add := func(tag string) {
		for _, existing := range tags {
			if existing == tag {
				return
			}
		}

		tags = append(tags, tag)
	}

	switch {
	case strings.Contains(text, "controlrail") || strings.Contains(text, "control rail"):
		add("control-rails")
	case strings.Contains(text, "writeapproval") || strings.Contains(text, "write approval") || strings.Contains(text, "writeguard") || strings.Contains(text, "guarded write"):
		add("write-guard")
	case strings.Contains(text, "render") || strings.Contains(text, "view") || strings.Contains(text, "style"):
		add("rendering")
	case strings.Contains(text, "route") || strings.Contains(text, "prompt") || strings.Contains(text, "contract"):
		add("prompt-routing")
	case strings.Contains(text, "memory") || strings.Contains(text, "journal") || strings.Contains(text, "resume"):
		add("task-memory")
	case strings.Contains(text, "implement") || strings.Contains(text, "verification") || strings.Contains(text, "writeguard") || strings.Contains(text, "shortcut"):
		add("implementation")
	case strings.Contains(text, "explore") || strings.Contains(text, "analysis") || strings.Contains(text, "evidence") || strings.Contains(text, "critic"):
		add("analysis")
	case strings.Contains(text, "init") || strings.Contains(text, "bootstrap") || strings.Contains(text, "update(") || strings.Contains(text, "slashcommand") || strings.Contains(text, "sendprompt"):
		add("lifecycle")
	case strings.Contains(text, "model") || strings.Contains(text, "message") || strings.Contains(text, "state"):
		add("model-state")
	}

	if len(tags) == 0 {
		add("core")
	}

	return tags
}

func goDeclNameResponsibilityTag(names []string) string {
	joined := strings.ToLower(strings.Join(names, " "))
	switch {
	case containsGoDeclName(joined, "writeapproval", "writeguard", "approval", "guarded", "guardpath", "preexecute", "protectedpath"):
		return "write-guard"
	case containsGoDeclName(joined, "implementation", "implement", "verification", "directpromptedit", "structuredexecution", "focused", "changedpaths", "finalanswercritic", "repair", "refactorstage"):
		return "implementation"
	case containsGoDeclName(joined, "taskmemory", "memory", "resume", "replan", "structuralrefactor"):
		return "task-memory"
	case containsGoDeclName(joined, "prompt", "route", "contract", "lane", "targetpacket", "classification", "ambig", "systemprompt", "profiles", "thoroughness"):
		return "prompt-routing"
	case containsGoDeclName(joined, "view", "render", "viewport", "style", "phasecard", "panel", "bodyview", "messages", "scroll"):
		return "rendering"
	case containsGoDeclName(joined, "explore", "review", "architecture", "analysis", "evidence", "hotspot", "candidate", "readiness", "deepening", "capability", "ledger", "issue"):
		return "analysis"
	case containsGoDeclName(joined, "new", "init", "update", "runpromptonce", "ensureserverready", "sendpromptasync", "runpromptplan", "runmainagent", "serverstatus", "stream", "transcript", "reload"):
		return "lifecycle"
	case containsGoDeclName(joined, "model", "msg", "state", "trace", "journal", "phase", "executionpanel", "fallback"):
		return "model-state"
	default:
		return ""
	}
}

func containsGoDeclName(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}

	return false
}
