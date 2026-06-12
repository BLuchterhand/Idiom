package comprehension

import (
	"idiom/internal/platform/util"
	"slices"
	"sort"
	"strings"
)

type packFileBuilder struct {
	files            map[string]PackFile
	stale            map[string]struct{}
	forbidden        []string
	omittedForbidden map[string]bool
}

func newPackFileBuilder(stale map[string]struct{}, forbidden []string) *packFileBuilder {
	return &packFileBuilder{
		files:            make(map[string]PackFile),
		stale:            stale,
		forbidden:        forbidden,
		omittedForbidden: make(map[string]bool),
	}
}

func (b *packFileBuilder) add(node Node, role string, reason string, handle string) {
	if node.Path == "" {
		return
	}

	for _, forbidden := range b.forbidden {
		if pathInScope(node.Path, forbidden) {
			b.omittedForbidden[forbidden] = true
			return
		}
	}

	existing := b.files[node.Path]
	if existing.Path == "" || packRoleRank(role) > packRoleRank(existing.Role) {
		existing.Path = node.Path
		existing.Role = role
		existing.Language = node.Language
		existing.Confidence = node.Confidence
	}

	if existing.Reason == "" {
		existing.Reason = reason
	}

	if _, ok := b.stale[node.Path]; ok {
		existing.Stale = true
	}

	if handle != "" && !slices.Contains(existing.Handles, handle) {
		existing.Handles = append(existing.Handles, handle)
		slices.Sort(existing.Handles)
	}

	b.files[node.Path] = existing
}

func (b *packFileBuilder) addPackFile(file PackFile) {
	if file.Path == "" {
		return
	}

	for _, forbidden := range b.forbidden {
		if pathInScope(file.Path, forbidden) {
			b.omittedForbidden[forbidden] = true
			return
		}
	}

	existing := b.files[file.Path]
	if existing.Path == "" || packRoleRank(file.Role) > packRoleRank(existing.Role) {
		existing.Path = file.Path
		existing.Role = file.Role
		existing.Language = file.Language
		existing.Confidence = file.Confidence
	}

	if existing.Reason == "" {
		existing.Reason = file.Reason
	}

	existing.Stale = existing.Stale || file.Stale
	for _, handle := range file.Handles {
		if handle != "" && !slices.Contains(existing.Handles, handle) {
			existing.Handles = append(existing.Handles, handle)
		}
	}

	slices.Sort(existing.Handles)

	b.files[file.Path] = existing
}

// all returns every accumulated candidate file in role-stable order, before
// any score-based ranking, limit, or handle bounding is applied.
func (b *packFileBuilder) all() []PackFile {
	out := make([]PackFile, 0, len(b.files))
	for _, file := range b.files {
		out = append(out, file)
	}

	sort.Slice(out, func(i, j int) bool {
		if packRoleRank(out[i].Role) != packRoleRank(out[j].Role) {
			return packRoleRank(out[i].Role) > packRoleRank(out[j].Role)
		}

		if out[i].Stale != out[j].Stale {
			return !out[i].Stale
		}

		return out[i].Path < out[j].Path
	})

	return out
}

// finalizePackFiles applies score-based ranking via RankPackFiles, then the
// candidate limit and per-file handle bounding. The Hub flag is set inside
// RankPackFiles so callers that bypass finalizePackFiles still see it.
func finalizePackFiles(files []PackFile, in RankInputs, limit int) []PackFile {
	ranked := RankPackFiles(RankInputs{
		Files:          files,
		Graph:          in.Graph,
		MatchedIDs:     in.MatchedIDs,
		Intent:         in.Intent,
		LearnedWeights: in.LearnedWeights,
		QueryTokens:    in.QueryTokens,
		RequiredPaths:  in.RequiredPaths,
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}

	for i := range ranked {
		ranked[i] = boundedPackFileHandles(ranked[i])
	}

	return ranked
}

func boundedPackFileHandles(file PackFile) PackFile {
	if len(file.Handles) <= maxPackFileHandles {
		return file
	}

	file.OmittedHandles = len(file.Handles) - maxPackFileHandles
	file.Handles = append([]string(nil), file.Handles[:maxPackFileHandles]...)

	return file
}

type packSymbolBuilder struct {
	symbols map[string]PackSymbol
	stale   map[string]struct{}
}

func newPackSymbolBuilder(stale map[string]struct{}) *packSymbolBuilder {
	return &packSymbolBuilder{symbols: make(map[string]PackSymbol), stale: stale}
}

func (b *packSymbolBuilder) add(node Node, reason string) {
	if !isSymbolKind(node.Kind) || strings.HasPrefix(node.ID, "external_symbol://") {
		return
	}

	symbol := PackSymbol{
		ID:            node.ID,
		Kind:          node.Kind,
		Path:          node.Path,
		Language:      node.Language,
		QualifiedName: node.QualifiedName,
		StartLine:     node.StartLine,
		EndLine:       node.EndLine,
		Confidence:    node.Confidence,
		Reason:        reason,
	}
	if _, ok := b.stale[node.Path]; ok {
		symbol.Stale = true
	}

	if existing := b.symbols[node.ID]; existing.ID != "" && packReasonRank(existing.Reason) >= packReasonRank(reason) {
		return
	}

	b.symbols[node.ID] = symbol
}

func (b *packSymbolBuilder) list(limit int) []PackSymbol {
	out := make([]PackSymbol, 0, len(b.symbols))
	for _, symbol := range b.symbols {
		out = append(out, symbol)
	}

	sort.Slice(out, func(i, j int) bool {
		if packReasonRank(out[i].Reason) != packReasonRank(out[j].Reason) {
			return packReasonRank(out[i].Reason) > packReasonRank(out[j].Reason)
		}

		if out[i].Stale != out[j].Stale {
			return !out[i].Stale
		}

		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].QualifiedName < out[j].QualifiedName
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

type packTestBuilder struct {
	tests map[string]PackTest
}

func newPackTestBuilder() *packTestBuilder {
	return &packTestBuilder{tests: make(map[string]PackTest)}
}

func (b *packTestBuilder) add(test PackTest) {
	test.Command = strings.TrimSpace(test.Command)
	test.Path = strings.TrimSpace(test.Path)
	test.Symbol = strings.TrimSpace(test.Symbol)
	test.Reason = strings.TrimSpace(test.Reason)

	if test.Command == "" && test.Path == "" && test.Symbol == "" {
		return
	}

	key := util.FirstNonEmpty(test.Command, test.Path+"\x00"+test.Symbol)
	if existing := b.tests[key]; existing.Command != "" || existing.Path != "" || existing.Symbol != "" {
		if !existing.Required && test.Required {
			existing.Required = true
			b.tests[key] = existing
		}

		return
	}

	b.tests[key] = test
}

func (b *packTestBuilder) list(limit int) []PackTest {
	out := make([]PackTest, 0, len(b.tests))
	for _, test := range b.tests {
		out = append(out, test)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}

		if out[i].Command != out[j].Command {
			return out[i].Command < out[j].Command
		}

		return out[i].Path < out[j].Path
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

func addPackNode(node Node, direct bool, reason string, files *packFileBuilder, symbols *packSymbolBuilder) {
	if node.ID == "" {
		return
	}

	role := "read_only_evidence"
	if direct {
		role = "editable"
	}

	if node.Kind == "test" || strings.HasSuffix(node.Path, "_test.go") {
		role = "test"
	}

	if node.Kind == "file" || isSymbolKind(node.Kind) {
		files.add(node, role, reason, node.ID)
	}

	if isSymbolKind(node.Kind) {
		symbols.add(node, reason)
	}
}

func addPackRelatedNode(node Node, edge Edge, files *packFileBuilder, symbols *packSymbolBuilder) {
	if node.ID == "" || strings.HasPrefix(node.ID, "external://") || strings.HasPrefix(node.ID, "external_symbol://") {
		return
	}

	reason := "related by " + edge.Kind + " relationship"
	addPackNode(node, false, reason, files, symbols)
}

func addPackageContainedFiles(graph Graph, packageID string, nodesByID map[string]Node, files *packFileBuilder, limit int) {
	added := 0

	for _, edge := range graph.Edges {
		if edge.Kind != "contains" || edge.SourceID != packageID {
			continue
		}

		node := nodesByID[edge.TargetID]
		if node.Kind != "file" {
			continue
		}

		files.add(node, "editable", "package matched task terms", node.ID)

		added++
		if limit > 0 && added >= limit {
			return
		}
	}
}

func addPackTestFromNode(source Node, target Node, tests *packTestBuilder) {
	if source.Kind != "test" {
		return
	}

	test := PackTest{
		Path:   source.Path,
		Symbol: source.QualifiedName,
		Reason: "context graph test edge covers " + util.FirstNonEmpty(target.QualifiedName, target.Path, target.ID),
	}
	if source.Language == "go" {
		test.Command = goTestCommandForPath(source.Path)
	}

	tests.add(test)
}

func addPackTestCommandForFile(file PackFile, tests *packTestBuilder) {
	if file.Language != "go" {
		return
	}

	command := goTestCommandForPath(file.Path)
	if command == "" {
		return
	}

	tests.add(PackTest{Command: command, Path: file.Path, Reason: "inferred from Go candidate file"})
}

func goTestCommandForPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}

	dir := pathDir(path)
	if dir == "." || dir == "" {
		return "go test ."
	}

	return "go test ./" + dir
}
