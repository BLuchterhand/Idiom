package comprehension

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/repository/semantic"
)

type graphBuilder struct {
	root    string
	now     string
	gitHead string
	nodes   map[string]Node
	edges   map[string]Edge
}

func (b *graphBuilder) addNode(node Node) {
	node.CreatedAt = defaultString(node.CreatedAt, b.now)
	node.UpdatedAt = defaultString(node.UpdatedAt, b.now)
	node.GitHead = defaultString(node.GitHead, b.gitHead)

	if node.Collector == "" {
		node.Collector = "context_index"
	}

	if node.Confidence == "" {
		node.Confidence = "unknown"
	}

	b.nodes[node.ID] = node
}

func (b *graphBuilder) addEdge(kind, sourceID, targetID, collector, confidence string, evidence map[string]any, properties map[string]any) {
	if sourceID == "" || targetID == "" {
		return
	}

	edge := Edge{
		ID:         edgeID(kind, sourceID, targetID, evidence),
		SourceID:   sourceID,
		TargetID:   targetID,
		Kind:       kind,
		Collector:  collector,
		Confidence: confidence,
		Evidence:   evidence,
		Properties: properties,
		CreatedAt:  b.now,
		UpdatedAt:  b.now,
	}

	b.edges[edge.ID] = edge
}

func (b *graphBuilder) addGoIndex(root string, repoID string, scope string, goIndex *semantic.GoIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int) {
	filesIndexed := 0
	symbolsIndexed := 0

	for _, file := range goIndex.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}

		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}

		fileAbs := filepath.Join(root, filepath.FromSlash(file.Path))
		fingerprint, _ := fileFingerprint(fileAbs)
		importAliases := goImportAliases(fileAbs)
		fileID := fileNodeID(file.Path)
		packageID := packageNodeID(file.PathDir())
		b.addNode(Node{
			ID:          packageID,
			Kind:        "package",
			Path:        file.PathDir(),
			Language:    "go",
			Collector:   "go_index",
			Confidence:  "syntax",
			Fingerprint: packageFingerprint(file.PathDir(), goIndex.Package[file.PathDir()]),
			GitHead:     b.gitHead,
			Properties: map[string]any{
				"name": file.Package,
			},
		})
		b.addEdge("contains", repoID, packageID, "go_index", "syntax", map[string]any{"path": file.PathDir()}, nil)
		b.addNode(Node{
			ID:          fileID,
			Kind:        "file",
			Path:        file.Path,
			Language:    "go",
			Fingerprint: fingerprint,
			GitHead:     b.gitHead,
			Collector:   "go_index",
			Confidence:  "syntax",
			Properties: map[string]any{
				"package": file.Package,
				"is_test": file.IsTest,
				"imports": file.Imports,
				"funcs":   file.Funcs,
				"methods": file.Methods,
				"types":   file.Types,
			},
		})
		b.addEdge("contains", packageID, fileID, "go_index", "syntax", map[string]any{"path": file.Path}, nil)

		symbolIDsByName := goSymbolIDsByName(goIndex, file.PathDir())
		for _, imp := range file.Imports {
			depID := externalDependencyNodeID(imp)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: imp,
				Language:      "go",
				Collector:     "go_index",
				Confidence:    "syntax",
			})
			b.addEdge("imports", fileID, depID, "go_index", "syntax", map[string]any{"path": file.Path}, nil)
		}

		for _, symbol := range file.Symbols {
			kind := symbol.Kind
			if file.IsTest && (kind == "func" || kind == "method") && isGoTestSymbol(symbol.Name) {
				kind = "test"
			}

			qualified := qualifiedGoSymbolName(symbol)
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      "go",
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       symbol.EndLine,
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "go_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":     symbol.Name,
					"receiver": symbol.Receiver,
					"package":  symbol.Package,
				},
			})
			b.addEdge("defines", fileID, symbolID, "go_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)

			if kind == "test" {
				b.addEdge("tests", symbolID, packageID, "go_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)

				if targetID := goTestTargetID(symbol.Name, symbolIDsByName); targetID != "" {
					b.addEdge("tests", symbolID, targetID, "go_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
				}
			}

			for _, call := range symbol.Calls {
				targetID, externalNode := goCallTargetID(goIndex, call, scope, file.PathDir(), importAliases)
				if externalNode != nil {
					b.addNode(*externalNode)
				}

				if targetID == "" {
					continue
				}

				b.addEdge("calls", symbolID, targetID, "go_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": qualifiedCallName(call)})
			}

			symbolsIndexed++
		}

		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}

	return filesIndexed, symbolsIndexed
}

func (b *graphBuilder) addCppIndex(root string, repoID string, scope string, cppIndex *semantic.CppIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int) {
	filesIndexed := 0
	symbolsIndexed := 0
	callTargets := newCppCallTargetResolver(cppIndex, scope)

	for _, file := range cppIndex.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}

		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}

		fileAbs := filepath.Join(root, filepath.FromSlash(file.Path))
		fingerprint, _ := fileFingerprint(fileAbs)
		fileID := fileNodeID(file.Path)
		dir := pathDir(file.Path)
		packageID := packageNodeID(dir)
		b.addNode(Node{
			ID:          packageID,
			Kind:        "package",
			Path:        dir,
			Language:    "cpp",
			Collector:   "cpp_index",
			Confidence:  "syntax",
			Fingerprint: genericPackageFingerprint(dir, "cpp"),
			GitHead:     b.gitHead,
			Properties: map[string]any{
				"name": packageNameForDir(dir),
			},
		})
		b.addEdge("contains", repoID, packageID, "cpp_index", "syntax", map[string]any{"path": dir}, nil)
		b.addNode(Node{
			ID:          fileID,
			Kind:        "file",
			Path:        file.Path,
			Language:    "cpp",
			Fingerprint: fingerprint,
			GitHead:     b.gitHead,
			Collector:   "cpp_index",
			Confidence:  "syntax",
			Properties: map[string]any{
				"is_header": file.IsHeader,
				"is_test":   file.IsTest,
				"includes":  file.Includes,
				"funcs":     file.Funcs,
				"methods":   file.Methods,
				"types":     file.Types,
			},
		})
		b.addEdge("contains", packageID, fileID, "cpp_index", "syntax", map[string]any{"path": file.Path}, nil)

		for _, include := range file.Includes {
			targetID := cppIncludeTargetID(cppIndex, include)
			if strings.HasPrefix(targetID, "external://") {
				b.addNode(Node{
					ID:            targetID,
					Kind:          "external_dependency",
					QualifiedName: include,
					Language:      "cpp",
					Collector:     "cpp_index",
					Confidence:    "syntax",
				})
			}

			b.addEdge("imports", fileID, targetID, "cpp_index", "syntax", map[string]any{"path": file.Path, "include": include}, nil)
		}

		for _, symbol := range file.Symbols {
			qualified := qualifiedCppSymbolName(symbol)
			kind := cppSymbolKind(symbol.Kind)
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      "cpp",
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       symbol.EndLine,
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "cpp_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":          symbol.Name,
					"scope":         symbol.Scope,
					"original_kind": symbol.Kind,
					"has_body":      symbol.HasBody,
				},
			})
			b.addEdge("defines", fileID, symbolID, "cpp_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)

			if file.IsTest && (kind == "func" || kind == "method") {
				b.addEdge("tests", symbolID, packageID, "cpp_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}

			for _, call := range symbol.Calls {
				targetID := callTargets.targetID(call)
				if targetID == "" {
					continue
				}

				b.addEdge("calls", symbolID, targetID, "cpp_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}

			symbolsIndexed++
		}

		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}

	return filesIndexed, symbolsIndexed
}

type genericSourceFile struct {
	Path     string
	Language string
}

type genericSymbol struct {
	Name   string
	Kind   string
	Line   int
	End    int
	Parent string
	Calls  []genericCall
	Extra  map[string]any
}

type genericDependency struct {
	Name string
	Line int
}

type genericCall struct {
	Name string
	Line int
}

type genericIndexedFile struct {
	genericSourceFile
	Fingerprint string
	Symbols     []genericSymbol
	Deps        []genericDependency
}

func (b *graphBuilder) addGenericSourceIndex(ctx context.Context, root string, repoID string, scope string, language string, maxFiles int, indexedFiles map[string]struct{}) (int, int, []string, error) {
	files, err := collectGenericSourceFiles(ctx, root, scope, language, maxFiles, indexedFiles)
	if err != nil {
		return 0, 0, nil, err
	}

	facts := make([]genericIndexedFile, 0, len(files))
	existingFiles := make(map[string]struct{}, len(files))

	for _, file := range files {
		existingFiles[file.Path] = struct{}{}
	}

	symbolIDsByName := make(map[string][]string)

	for _, file := range files {
		if err := checkBuildContext(ctx); err != nil {
			return 0, 0, nil, err
		}

		fileAbs := filepath.Join(root, filepath.FromSlash(file.Path))
		fingerprint, _ := fileFingerprint(fileAbs)
		content, _ := os.ReadFile(fileAbs)
		symbols := genericSymbols(file.Path, file.Language, string(content))

		for _, symbol := range symbols {
			key := strings.ToLower(symbol.Name)

			symbolIDsByName[key] = append(symbolIDsByName[key], symbolNodeID(file.Path, symbol.Name))
		}

		facts = append(facts, genericIndexedFile{
			genericSourceFile: file,
			Fingerprint:       fingerprint,
			Symbols:           symbols,
			Deps:              genericDependencies(file.Language, string(content)),
		})
	}

	filesIndexed := 0
	symbolsIndexed := 0
	languagesSeen := make(map[string]struct{})

	for _, fact := range facts {
		file := fact.genericSourceFile
		fileID := fileNodeID(file.Path)
		dir := pathDir(file.Path)
		packageID := packageNodeID(dir)

		languagesSeen[file.Language] = struct{}{}
		b.addNode(Node{
			ID:          packageID,
			Kind:        "package",
			Path:        dir,
			Language:    file.Language,
			Collector:   "generic_source_index",
			Confidence:  "heuristic",
			Fingerprint: genericPackageFingerprint(dir, file.Language),
			GitHead:     b.gitHead,
			Properties: map[string]any{
				"name":    packageNameForDir(dir),
				"generic": true,
			},
		})
		b.addEdge("contains", repoID, packageID, "generic_source_index", "heuristic", map[string]any{"path": dir}, nil)
		b.addNode(Node{
			ID:          fileID,
			Kind:        "file",
			Path:        file.Path,
			Language:    file.Language,
			Fingerprint: fact.Fingerprint,
			GitHead:     b.gitHead,
			Collector:   "generic_source_index",
			Confidence:  "syntax",
			Properties: map[string]any{
				"extension": filepath.Ext(file.Path),
				"generic":   true,
				"imports":   dependencyNames(fact.Deps),
				"is_test":   isGenericTestPath(file.Path, file.Language),
				"symbols":   len(fact.Symbols),
			},
		})
		b.addEdge("contains", packageID, fileID, "generic_source_index", "heuristic", map[string]any{"path": file.Path}, nil)

		importedFileIDs := make(map[string]struct{})

		for _, dep := range fact.Deps {
			depID := genericDependencyTargetID(file.Path, file.Language, dep.Name, existingFiles)
			if depID == "" {
				depID = externalDependencyNodeID(file.Language + ":" + dep.Name)
			}

			if strings.HasPrefix(depID, "file://") {
				importedFileIDs[depID] = struct{}{}
			}

			if strings.HasPrefix(depID, "external://") {
				b.addNode(Node{
					ID:            depID,
					Kind:          "external_dependency",
					QualifiedName: dep.Name,
					Language:      file.Language,
					Collector:     "generic_source_index",
					Confidence:    "heuristic",
				})
			}

			b.addEdge("imports", fileID, depID, "generic_source_index", "heuristic", map[string]any{"path": file.Path, "line": dep.Line}, nil)
		}

		for _, symbol := range fact.Symbols {
			kind := symbol.Kind
			if isGenericTestPath(file.Path, file.Language) && isGenericTestSymbol(file.Language, symbol.Name) {
				kind = "test"
			}

			symbolID := symbolNodeID(file.Path, symbol.Name)
			properties := map[string]any{
				"name":    symbol.Name,
				"generic": true,
			}

			if symbol.Parent != "" {
				properties["parent"] = symbol.Parent
			}

			for key, value := range symbol.Extra {
				properties[key] = value
			}

			endLine := symbol.End
			if endLine == 0 {
				endLine = symbol.Line
			}

			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      file.Language,
				QualifiedName: symbol.Name,
				StartLine:     symbol.Line,
				EndLine:       endLine,
				Fingerprint:   fact.Fingerprint,
				GitHead:       b.gitHead,
				Collector:     "generic_source_index",
				Confidence:    "heuristic",
				Properties:    properties,
			})
			b.addEdge("defines", fileID, symbolID, "generic_source_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)

			if symbol.Parent != "" {
				parentID := symbolNodeID(file.Path, symbol.Parent)
				if parentID != symbolID {
					b.addEdge("contains", parentID, symbolID, "generic_source_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
				}
			}

			if kind == "test" {
				b.addEdge("tests", symbolID, packageID, "generic_source_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}

			for _, call := range symbol.Calls {
				targetID := genericCallTargetID(file.Path, call.Name, symbolIDsByName, importedFileIDs)
				if targetID == "" || targetID == symbolID {
					continue
				}

				b.addEdge("calls", symbolID, targetID, "generic_source_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}

			symbolsIndexed++
		}

		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}

	return filesIndexed, symbolsIndexed, sortedStringSet(languagesSeen), nil
}

func (b *graphBuilder) addGDScriptIndex(ctx context.Context, root string, repoID string, scope string, maxFiles int, indexedFiles map[string]struct{}) (int, int, error) {
	files, err := collectGDScriptProjectFiles(ctx, root, scope, maxFiles, indexedFiles)
	if err != nil {
		return 0, 0, err
	}

	existing := make(map[string]struct{}, len(files))
	for _, file := range files {
		existing[file.Path] = struct{}{}
	}

	filesIndexed := 0
	symbolsIndexed := 0

	for _, file := range files {
		if err := checkBuildContext(ctx); err != nil {
			return 0, 0, err
		}

		fileAbs := filepath.Join(root, filepath.FromSlash(file.Path))
		contentBytes, _ := os.ReadFile(fileAbs)
		content := string(contentBytes)
		fingerprint, _ := fileFingerprint(fileAbs)
		fileID := fileNodeID(file.Path)
		dir := pathDir(file.Path)
		packageID := packageNodeID(dir)
		symbols, deps := gdscriptFileFacts(file.Path, content, existing)
		b.addNode(Node{
			ID:          packageID,
			Kind:        "package",
			Path:        dir,
			Language:    "gdscript",
			Collector:   "gdscript_index",
			Confidence:  "syntax",
			Fingerprint: genericPackageFingerprint(dir, "gdscript"),
			GitHead:     b.gitHead,
			Properties: map[string]any{
				"name": packageNameForDir(dir),
			},
		})
		b.addEdge("contains", repoID, packageID, "gdscript_index", "syntax", map[string]any{"path": dir}, nil)
		b.addNode(Node{
			ID:          fileID,
			Kind:        "file",
			Path:        file.Path,
			Language:    "gdscript",
			Fingerprint: fingerprint,
			GitHead:     b.gitHead,
			Collector:   "gdscript_index",
			Confidence:  "syntax",
			Properties: map[string]any{
				"extension": filepath.Ext(file.Path),
				"imports":   dependencyNames(deps),
				"role":      gdscriptFileRole(file.Path),
				"symbols":   len(symbols),
			},
		})
		b.addEdge("contains", packageID, fileID, "gdscript_index", "syntax", map[string]any{"path": file.Path}, nil)

		for _, dep := range deps {
			targetID := dep.Name
			if _, ok := existing[targetID]; ok {
				targetID = fileNodeID(targetID)
			} else {
				targetID = externalDependencyNodeID("gdscript:" + targetID)
				b.addNode(Node{
					ID:            targetID,
					Kind:          "external_dependency",
					QualifiedName: dep.Name,
					Language:      "gdscript",
					Collector:     "gdscript_index",
					Confidence:    "heuristic",
				})
			}

			b.addEdge("imports", fileID, targetID, "gdscript_index", "heuristic", map[string]any{"path": file.Path, "line": dep.Line}, nil)
		}

		for _, symbol := range symbols {
			symbolID := symbolNodeID(file.Path, symbol.Name)
			properties := map[string]any{"name": symbol.Name}

			for key, value := range symbol.Extra {
				properties[key] = value
			}

			b.addNode(Node{
				ID:            symbolID,
				Kind:          symbol.Kind,
				Path:          file.Path,
				Language:      "gdscript",
				QualifiedName: symbol.Name,
				StartLine:     symbol.Line,
				EndLine:       defaultInt(symbol.End, symbol.Line),
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "gdscript_index",
				Confidence:    "syntax",
				Properties:    properties,
			})
			b.addEdge("defines", fileID, symbolID, "gdscript_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)

			symbolsIndexed++
		}

		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}

	return filesIndexed, symbolsIndexed, nil
}

