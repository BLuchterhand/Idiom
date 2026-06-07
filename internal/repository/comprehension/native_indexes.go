package comprehension

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/repository/semantic"
)

type nativeLanguageIndexes struct {
	requested  string
	python     *semantic.PythonIndex
	typescript *semantic.TsIndex
	rust       *semantic.RustIndex
	java       *semantic.JavaIndex
}

func buildNativeLanguageIndexes(ctx context.Context, root string, language string, scope string, maxFiles int, allowMissingScope bool) (nativeLanguageIndexes, error) {
	language = normalizeLanguage(language)
	out := nativeLanguageIndexes{requested: language}
	indexOpts := semantic.IndexOptions{Scope: scope, MaxFiles: maxFiles, Context: ctx}
	if shouldUsePythonCollector(language) {
		index, err := semantic.BuildPythonIndexWithOptions(root, indexOpts)
		if err != nil {
			return out, fmt.Errorf("build Python index: %w", err)
		}
		if language == "python" && index == nil && !allowMissingScope {
			return out, fmt.Errorf("no Python files found under %s", root)
		}
		out.python = index
	}
	if shouldUseTypeScriptCollector(language) {
		index, err := semantic.BuildTsIndexWithOptions(root, indexOpts)
		if err != nil {
			return out, fmt.Errorf("build TypeScript/JavaScript index: %w", err)
		}
		if (language == "typescript" || language == "javascript") && index == nil && !allowMissingScope {
			return out, fmt.Errorf("no TypeScript/JavaScript files found under %s", root)
		}
		out.typescript = index
	}
	if shouldUseRustCollector(language) {
		index, err := semantic.BuildRustIndexWithOptions(root, indexOpts)
		if err != nil {
			return out, fmt.Errorf("build Rust index: %w", err)
		}
		if language == "rust" && index == nil && !allowMissingScope {
			return out, fmt.Errorf("no Rust files found under %s", root)
		}
		out.rust = index
	}
	if shouldUseJavaCollector(language) {
		index, err := semantic.BuildJavaIndexWithOptions(root, indexOpts)
		if err != nil {
			return out, fmt.Errorf("build Java index: %w", err)
		}
		if language == "java" && index == nil && !allowMissingScope {
			return out, fmt.Errorf("no Java files found under %s", root)
		}
		out.java = index
	}
	return out, nil
}

func (idx nativeLanguageIndexes) addToGraph(root string, repoID string, scope string, builder *graphBuilder, maxFiles int, indexedFiles map[string]struct{}) (int, int, []string, []string) {
	filesIndexed := 0
	symbolsIndexed := 0
	languagesSeen := make(map[string]struct{})
	collectorsSeen := make(map[string]struct{})
	if idx.python != nil && hasNativeRemaining(maxFiles, filesIndexed) {
		addedFiles, addedSymbols := builder.addPythonIndex(root, repoID, scope, idx.python, nativeRemaining(maxFiles, filesIndexed), indexedFiles)
		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols
		if addedFiles > 0 {
			languagesSeen["python"] = struct{}{}
			collectorsSeen["python_index"] = struct{}{}
		}
	}
	if idx.typescript != nil && hasNativeRemaining(maxFiles, filesIndexed) {
		addedFiles, addedSymbols, languages := builder.addTypeScriptIndex(root, repoID, scope, idx.requested, idx.typescript, nativeRemaining(maxFiles, filesIndexed), indexedFiles)
		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols
		for _, language := range languages {
			languagesSeen[language] = struct{}{}
		}
		if addedFiles > 0 {
			collectorsSeen["typescript_index"] = struct{}{}
		}
	}
	if idx.rust != nil && hasNativeRemaining(maxFiles, filesIndexed) {
		addedFiles, addedSymbols := builder.addRustIndex(root, repoID, scope, idx.rust, nativeRemaining(maxFiles, filesIndexed), indexedFiles)
		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols
		if addedFiles > 0 {
			languagesSeen["rust"] = struct{}{}
			collectorsSeen["rust_index"] = struct{}{}
		}
	}
	if idx.java != nil && hasNativeRemaining(maxFiles, filesIndexed) {
		addedFiles, addedSymbols := builder.addJavaIndex(root, repoID, scope, idx.java, nativeRemaining(maxFiles, filesIndexed), indexedFiles)
		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols
		if addedFiles > 0 {
			languagesSeen["java"] = struct{}{}
			collectorsSeen["java_index"] = struct{}{}
		}
	}
	return filesIndexed, symbolsIndexed, sortedStringSet(languagesSeen), sortedStringSet(collectorsSeen)
}

func nativeRemaining(maxFiles int, added int) int {
	if maxFiles <= 0 {
		return 0
	}
	remaining := maxFiles - added
	if remaining < 0 {
		return 0
	}
	return remaining
}

func hasNativeRemaining(maxFiles int, added int) bool {
	return maxFiles <= 0 || added < maxFiles
}

func (b *graphBuilder) addPythonIndex(root string, repoID string, scope string, index *semantic.PythonIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int) {
	table := newNativeSymbolTable()
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		for _, symbol := range file.Symbols {
			table.add(file.Path, qualifiedPythonSymbolName(symbol), symbol.Name, pythonSymbolKind(symbol.Kind))
		}
	}
	existingFiles := nativeIndexFiles(index.Files, func(file semantic.PythonFile) string { return file.Path })
	filesIndexed := 0
	symbolsIndexed := 0
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}
		fingerprint, _ := fileFingerprint(filepath.Join(root, filepath.FromSlash(file.Path)))
		fileID, packageID := b.addNativeFile(repoID, file.Path, "python", "python_index", fingerprint, map[string]any{
			"imports":     file.Imports,
			"raw_imports": file.RawImports,
			"funcs":       file.Funcs,
			"methods":     file.Methods,
			"classes":     file.Classes,
			"has_main":    file.HasMain,
			"is_test":     file.IsTest,
			"is_init":     file.IsInit,
			"source_dir":  file.SourceDir,
		})
		imported := make(map[string]struct{})
		for _, dep := range file.Imports {
			if _, ok := existingFiles[dep]; ok {
				b.addEdge("imports", fileID, fileNodeID(dep), "python_index", "syntax", map[string]any{"path": file.Path, "import": dep}, nil)
				imported[dep] = struct{}{}
			}
		}
		for _, raw := range file.RawImports {
			if pythonRawImportResolved(raw, file.Imports) {
				continue
			}
			depID := externalDependencyNodeID("python:" + raw)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: raw,
				Language:      "python",
				Collector:     "python_index",
				Confidence:    "syntax",
			})
			b.addEdge("imports", fileID, depID, "python_index", "syntax", map[string]any{"path": file.Path, "import": raw}, nil)
		}
		localSymbols := pythonLocalSymbols(file)
		for _, symbol := range file.Symbols {
			qualified := qualifiedPythonSymbolName(symbol)
			kind := pythonSymbolKind(symbol.Kind)
			if file.IsTest && kind == "func" && isGenericTestSymbol("python", symbol.Name) {
				kind = "test"
			}
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      "python",
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       defaultInt(symbol.EndLine, symbol.Line),
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "python_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":          symbol.Name,
					"scope":         symbol.Scope,
					"original_kind": symbol.Kind,
					"async":         strings.HasPrefix(symbol.Kind, "async_"),
				},
			})
			b.addEdge("defines", fileID, symbolID, "python_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			if parentID := nativeParentSymbolID(file.Path, symbol.Scope, localSymbols); parentID != "" && parentID != symbolID {
				b.addEdge("contains", parentID, symbolID, "python_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			if kind == "test" {
				b.addEdge("tests", symbolID, packageID, "python_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			for _, call := range symbol.Calls {
				targetID := table.target(file.Path, call.Name, imported)
				if targetID == "" || targetID == symbolID {
					continue
				}
				b.addEdge("calls", symbolID, targetID, "python_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}
			symbolsIndexed++
		}
		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}
	return filesIndexed, symbolsIndexed
}

func (b *graphBuilder) addTypeScriptIndex(root string, repoID string, scope string, requested string, index *semantic.TsIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int, []string) {
	table := newNativeSymbolTable()
	for _, file := range index.Files {
		language := sourceLanguageForPath(file.Path)
		if !pathInScope(file.Path, scope) || !languageMatches(requested, language) {
			continue
		}
		for _, symbol := range file.Symbols {
			table.add(file.Path, qualifiedTsSymbolName(symbol), symbol.Name, tsSymbolKind(symbol.Kind))
		}
	}
	existingFiles := nativeIndexFiles(index.Files, func(file semantic.TsFile) string { return file.Path })
	filesIndexed := 0
	symbolsIndexed := 0
	languagesSeen := make(map[string]struct{})
	for _, file := range index.Files {
		language := sourceLanguageForPath(file.Path)
		if !pathInScope(file.Path, scope) || !languageMatches(requested, language) {
			continue
		}
		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}
		fingerprint, _ := fileFingerprint(filepath.Join(root, filepath.FromSlash(file.Path)))
		fileID, packageID := b.addNativeFile(repoID, file.Path, language, "typescript_index", fingerprint, map[string]any{
			"imports":         file.Imports,
			"funcs":           file.Funcs,
			"methods":         file.Methods,
			"types":           file.Types,
			"classes":         file.Classes,
			"has_main":        file.HasMain,
			"is_test":         file.IsTest,
			"is_declaration":  file.IsDeclFile,
			"is_tsx":          file.IsTSX,
			"is_javascript":   file.IsJS,
			"source_dir":      file.SourceDir,
			"collector_model": "typescript_javascript",
		})
		imported := make(map[string]struct{})
		for _, dep := range file.Imports {
			if _, ok := existingFiles[dep]; ok {
				b.addEdge("imports", fileID, fileNodeID(dep), "typescript_index", "syntax", map[string]any{"path": file.Path, "import": dep}, nil)
				imported[dep] = struct{}{}
				continue
			}
			depID := externalDependencyNodeID(language + ":" + dep)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: dep,
				Language:      language,
				Collector:     "typescript_index",
				Confidence:    "syntax",
			})
			b.addEdge("imports", fileID, depID, "typescript_index", "syntax", map[string]any{"path": file.Path, "import": dep}, nil)
		}
		localSymbols := tsLocalSymbols(file)
		for _, symbol := range file.Symbols {
			qualified := qualifiedTsSymbolName(symbol)
			kind := tsSymbolKind(symbol.Kind)
			if file.IsTest && kind == "func" && isGenericTestSymbol(language, symbol.Name) {
				kind = "test"
			}
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      language,
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       defaultInt(symbol.EndLine, symbol.Line),
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "typescript_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":          symbol.Name,
					"scope":         symbol.Scope,
					"original_kind": symbol.Kind,
					"exported":      symbol.Exported,
				},
			})
			b.addEdge("defines", fileID, symbolID, "typescript_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			if parentID := nativeParentSymbolID(file.Path, symbol.Scope, localSymbols); parentID != "" && parentID != symbolID {
				b.addEdge("contains", parentID, symbolID, "typescript_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			if kind == "test" {
				b.addEdge("tests", symbolID, packageID, "typescript_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			for _, call := range symbol.Calls {
				targetID := table.target(file.Path, call.Name, imported)
				if targetID == "" || targetID == symbolID {
					continue
				}
				b.addEdge("calls", symbolID, targetID, "typescript_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}
			symbolsIndexed++
		}
		indexedFiles[file.Path] = struct{}{}
		languagesSeen[language] = struct{}{}
		filesIndexed++
	}
	return filesIndexed, symbolsIndexed, sortedStringSet(languagesSeen)
}

func (b *graphBuilder) addRustIndex(root string, repoID string, scope string, index *semantic.RustIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int) {
	table := newNativeSymbolTable()
	existingFiles := nativeIndexFiles(index.Files, func(file semantic.RustFile) string { return file.Path })
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		for _, symbol := range file.Symbols {
			table.add(file.Path, qualifiedRustSymbolName(symbol), symbol.Name, rustSymbolKind(symbol.Kind))
		}
	}
	filesIndexed := 0
	symbolsIndexed := 0
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}
		fingerprint, _ := fileFingerprint(filepath.Join(root, filepath.FromSlash(file.Path)))
		fileID, packageID := b.addNativeFile(repoID, file.Path, "rust", "rust_index", fingerprint, map[string]any{
			"modules":    file.Modules,
			"uses":       file.Uses,
			"funcs":      file.Funcs,
			"methods":    file.Methods,
			"types":      file.Types,
			"traits":     file.Traits,
			"impls":      file.Impls,
			"macros":     file.Macros,
			"has_main":   file.HasMain,
			"has_test":   file.HasTest,
			"has_bench":  file.HasBench,
			"is_test":    file.IsTest,
			"is_lib":     file.IsLibRoot,
			"is_bin":     file.IsBinRoot,
			"source_dir": file.SourceDir,
		})
		imported := make(map[string]struct{})
		for _, module := range file.Modules {
			if target := rustModuleTargetPath(file.Path, module, existingFiles); target != "" {
				b.addEdge("imports", fileID, fileNodeID(target), "rust_index", "syntax", map[string]any{"path": file.Path, "module": module}, nil)
				imported[target] = struct{}{}
				continue
			}
			depID := externalDependencyNodeID("rust:mod:" + module)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: module,
				Language:      "rust",
				Collector:     "rust_index",
				Confidence:    "heuristic",
			})
			b.addEdge("imports", fileID, depID, "rust_index", "heuristic", map[string]any{"path": file.Path, "module": module}, nil)
		}
		for _, usePath := range file.Uses {
			if target := rustUseTargetPath(file.Path, usePath, existingFiles); target != "" {
				b.addEdge("imports", fileID, fileNodeID(target), "rust_index", "heuristic", map[string]any{"path": file.Path, "use": usePath}, nil)
				imported[target] = struct{}{}
				continue
			}
			depID := externalDependencyNodeID("rust:" + usePath)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: usePath,
				Language:      "rust",
				Collector:     "rust_index",
				Confidence:    "syntax",
			})
			b.addEdge("imports", fileID, depID, "rust_index", "syntax", map[string]any{"path": file.Path, "use": usePath}, nil)
		}
		localSymbols := rustLocalSymbols(file)
		for _, symbol := range file.Symbols {
			qualified := qualifiedRustSymbolName(symbol)
			kind := rustSymbolKind(symbol.Kind)
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      "rust",
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       defaultInt(symbol.EndLine, symbol.Line),
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "rust_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":          symbol.Name,
					"scope":         symbol.Scope,
					"original_kind": symbol.Kind,
					"attrs":         symbol.Attrs,
				},
			})
			b.addEdge("defines", fileID, symbolID, "rust_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			if parentID := nativeParentSymbolID(file.Path, symbol.Scope, localSymbols); parentID != "" && parentID != symbolID {
				b.addEdge("contains", parentID, symbolID, "rust_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			if kind == "test" || kind == "bench" {
				b.addEdge("tests", symbolID, packageID, "rust_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			for _, call := range symbol.Calls {
				targetID := table.target(file.Path, call.Name, imported)
				if targetID == "" || targetID == symbolID {
					continue
				}
				b.addEdge("calls", symbolID, targetID, "rust_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}
			symbolsIndexed++
		}
		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}
	return filesIndexed, symbolsIndexed
}

func (b *graphBuilder) addJavaIndex(root string, repoID string, scope string, index *semantic.JavaIndex, maxFiles int, indexedFiles map[string]struct{}) (int, int) {
	table := newNativeSymbolTable()
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		for _, symbol := range file.Symbols {
			table.add(file.Path, qualifiedJavaSymbolName(symbol), symbol.Name, javaSymbolKind(symbol.Kind))
		}
	}
	filesIndexed := 0
	symbolsIndexed := 0
	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}
		if maxFiles > 0 && filesIndexed >= maxFiles {
			break
		}
		fingerprint, _ := fileFingerprint(filepath.Join(root, filepath.FromSlash(file.Path)))
		fileID, packageID := b.addNativeFile(repoID, file.Path, "java", "java_index", fingerprint, map[string]any{
			"package":    file.Package,
			"imports":    file.Imports,
			"classes":    file.Classes,
			"interfaces": file.Interfaces,
			"enums":      file.Enums,
			"methods":    file.Methods,
			"fields":     file.Fields,
			"has_main":   file.HasMain,
			"is_test":    file.IsTest,
			"source_dir": file.SourceDir,
		})
		imported := make(map[string]struct{})
		for _, target := range javaImportTargetPaths(index, file) {
			b.addEdge("imports", fileID, fileNodeID(target), "java_index", "syntax", map[string]any{"path": file.Path, "import": target}, nil)
			imported[target] = struct{}{}
		}
		for _, raw := range file.Imports {
			if javaRawImportResolved(index, raw) {
				continue
			}
			depID := externalDependencyNodeID("java:" + raw)
			b.addNode(Node{
				ID:            depID,
				Kind:          "external_dependency",
				QualifiedName: raw,
				Language:      "java",
				Collector:     "java_index",
				Confidence:    "syntax",
			})
			b.addEdge("imports", fileID, depID, "java_index", "syntax", map[string]any{"path": file.Path, "import": raw}, nil)
		}
		localSymbols := javaLocalSymbols(file)
		for _, symbol := range file.Symbols {
			qualified := qualifiedJavaSymbolName(symbol)
			kind := javaSymbolKind(symbol.Kind)
			if file.IsTest && kind == "method" && (isGenericTestSymbol("java", symbol.Name) || hasShortJavaAnnotation(symbol.Annotations, "Test")) {
				kind = "test"
			}
			symbolID := symbolNodeID(file.Path, qualified)
			b.addNode(Node{
				ID:            symbolID,
				Kind:          kind,
				Path:          file.Path,
				Language:      "java",
				QualifiedName: qualified,
				StartLine:     symbol.StartLine,
				EndLine:       defaultInt(symbol.EndLine, symbol.Line),
				Fingerprint:   fingerprint,
				GitHead:       b.gitHead,
				Collector:     "java_index",
				Confidence:    "syntax",
				Properties: map[string]any{
					"name":          symbol.Name,
					"scope":         symbol.Scope,
					"original_kind": symbol.Kind,
					"modifiers":     symbol.Modifiers,
					"annotations":   symbol.Annotations,
					"extends":       symbol.Extends,
					"implements":    symbol.Implements,
				},
			})
			b.addEdge("defines", fileID, symbolID, "java_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			if parentID := nativeParentSymbolID(file.Path, symbol.Scope, localSymbols); parentID != "" && parentID != symbolID {
				b.addEdge("contains", parentID, symbolID, "java_index", "syntax", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			if kind == "test" {
				b.addEdge("tests", symbolID, packageID, "java_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
			}
			if symbol.Extends != "" {
				if targetID := table.target(file.Path, symbol.Extends, imported); targetID != "" && targetID != symbolID {
					b.addEdge("extends", symbolID, targetID, "java_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
				}
			}
			for _, impl := range symbol.Implements {
				if targetID := table.target(file.Path, impl, imported); targetID != "" && targetID != symbolID {
					b.addEdge("implements", symbolID, targetID, "java_index", "heuristic", map[string]any{"path": file.Path, "line": symbol.Line}, nil)
				}
			}
			for _, call := range symbol.Calls {
				targetID := table.target(file.Path, call.Name, imported)
				if targetID == "" || targetID == symbolID {
					continue
				}
				b.addEdge("calls", symbolID, targetID, "java_index", "heuristic", map[string]any{"path": file.Path, "line": call.Line}, map[string]any{"call": call.Name})
			}
			symbolsIndexed++
		}
		indexedFiles[file.Path] = struct{}{}
		filesIndexed++
	}
	return filesIndexed, symbolsIndexed
}

func (b *graphBuilder) addNativeFile(repoID string, path string, language string, collector string, fingerprint string, properties map[string]any) (string, string) {
	fileID := fileNodeID(path)
	dir := pathDir(path)
	packageID := packageNodeID(dir)
	b.addNode(Node{
		ID:          packageID,
		Kind:        "package",
		Path:        dir,
		Language:    language,
		Collector:   collector,
		Confidence:  "syntax",
		Fingerprint: genericPackageFingerprint(dir, language),
		GitHead:     b.gitHead,
		Properties: map[string]any{
			"name": packageNameForDir(dir),
		},
	})
	b.addEdge("contains", repoID, packageID, collector, "syntax", map[string]any{"path": dir}, nil)
	b.addNode(Node{
		ID:          fileID,
		Kind:        "file",
		Path:        path,
		Language:    language,
		Fingerprint: fingerprint,
		GitHead:     b.gitHead,
		Collector:   collector,
		Confidence:  "syntax",
		Properties:  properties,
	})
	b.addEdge("contains", packageID, fileID, collector, "syntax", map[string]any{"path": path}, nil)
	return fileID, packageID
}

type nativeSymbolRef struct {
	ID        string
	Path      string
	Name      string
	Qualified string
	Kind      string
}

type nativeSymbolTable struct {
	byName      map[string][]nativeSymbolRef
	byQualified map[string][]nativeSymbolRef
}

func newNativeSymbolTable() nativeSymbolTable {
	return nativeSymbolTable{
		byName:      make(map[string][]nativeSymbolRef),
		byQualified: make(map[string][]nativeSymbolRef),
	}
}

func (t nativeSymbolTable) add(path string, qualified string, name string, kind string) {
	qualified = strings.TrimSpace(qualified)
	name = strings.TrimSpace(name)
	if path == "" || qualified == "" {
		return
	}
	ref := nativeSymbolRef{
		ID:        symbolNodeID(path, qualified),
		Path:      path,
		Name:      name,
		Qualified: qualified,
		Kind:      kind,
	}
	for _, key := range nativeSymbolLookupKeys(name, qualified) {
		t.byName[key] = append(t.byName[key], ref)
	}
	t.byQualified[nativeSymbolKey(qualified)] = append(t.byQualified[nativeSymbolKey(qualified)], ref)
}

func (t nativeSymbolTable) target(sourcePath string, callName string, importedPaths map[string]struct{}) string {
	callName = strings.TrimSpace(callName)
	if callName == "" {
		return ""
	}
	candidates := t.byQualified[nativeSymbolKey(callName)]
	for _, key := range nativeSymbolLookupKeys(callName, callName) {
		candidates = append(candidates, t.byName[key]...)
	}
	candidates = uniqueNativeSymbolRefs(candidates)
	if len(candidates) == 0 {
		return ""
	}
	sourceDir := pathDir(sourcePath)
	bestID := ""
	bestScore := -1
	ties := 0
	for _, candidate := range candidates {
		score := 0
		if candidate.Path == sourcePath {
			score += 80
		}
		if _, ok := importedPaths[candidate.Path]; ok {
			score += 60
		}
		if pathDir(candidate.Path) == sourceDir {
			score += 30
		}
		switch candidate.Kind {
		case "func", "method", "constructor", "type", "interface", "test":
			score += 5
		}
		if score > bestScore {
			bestScore = score
			bestID = candidate.ID
			ties = 1
			continue
		}
		if score == bestScore {
			ties++
			if candidate.ID < bestID {
				bestID = candidate.ID
			}
		}
	}
	if bestScore <= 0 && len(candidates) != 1 {
		return ""
	}
	if ties > 1 && bestScore < 80 {
		return ""
	}
	return bestID
}

func uniqueNativeSymbolRefs(in []nativeSymbolRef) []nativeSymbolRef {
	seen := make(map[string]struct{}, len(in))
	out := make([]nativeSymbolRef, 0, len(in))
	for _, ref := range in {
		if ref.ID == "" {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		out = append(out, ref)
	}
	slices.SortFunc(out, func(a, b nativeSymbolRef) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func nativeSymbolLookupKeys(name string, qualified string) []string {
	var keys []string
	for _, value := range []string{name, qualified, nativeShortName(qualified)} {
		key := nativeSymbolKey(value)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return uniqueStrings(keys)
}

func nativeSymbolKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func nativeShortName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, sep := range []string{"#", ".", "::"} {
		if idx := strings.LastIndex(value, sep); idx >= 0 && idx+len(sep) < len(value) {
			value = value[idx+len(sep):]
		}
	}
	return value
}

func nativeParentSymbolID(path string, scope string, localSymbols map[string]string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	for _, key := range nativeSymbolLookupKeys(scope, scope) {
		if id := localSymbols[key]; id != "" {
			return id
		}
	}
	return ""
}

func nativeIndexFiles[T any](files []T, pathFn func(T) string) map[string]struct{} {
	out := make(map[string]struct{}, len(files))
	for _, file := range files {
		if path := strings.TrimSpace(pathFn(file)); path != "" {
			out[path] = struct{}{}
		}
	}
	return out
}

func pythonLocalSymbols(file semantic.PythonFile) map[string]string {
	out := make(map[string]string)
	for _, symbol := range file.Symbols {
		qualified := qualifiedPythonSymbolName(symbol)
		id := symbolNodeID(file.Path, qualified)
		for _, key := range nativeSymbolLookupKeys(symbol.Name, qualified) {
			out[key] = id
		}
	}
	return out
}

func tsLocalSymbols(file semantic.TsFile) map[string]string {
	out := make(map[string]string)
	for _, symbol := range file.Symbols {
		qualified := qualifiedTsSymbolName(symbol)
		id := symbolNodeID(file.Path, qualified)
		for _, key := range nativeSymbolLookupKeys(symbol.Name, qualified) {
			out[key] = id
		}
	}
	return out
}

func rustLocalSymbols(file semantic.RustFile) map[string]string {
	out := make(map[string]string)
	for _, symbol := range file.Symbols {
		qualified := qualifiedRustSymbolName(symbol)
		id := symbolNodeID(file.Path, qualified)
		for _, key := range nativeSymbolLookupKeys(symbol.Name, qualified) {
			out[key] = id
		}
	}
	return out
}

func javaLocalSymbols(file semantic.JavaFile) map[string]string {
	out := make(map[string]string)
	for _, symbol := range file.Symbols {
		qualified := qualifiedJavaSymbolName(symbol)
		id := symbolNodeID(file.Path, qualified)
		for _, key := range nativeSymbolLookupKeys(symbol.Name, qualified) {
			out[key] = id
		}
		if symbol.Scope != "" {
			out[nativeSymbolKey(symbol.Scope)] = symbolNodeID(file.Path, qualifiedJavaScope(file.Package, symbol.Scope))
		}
	}
	return out
}

func qualifiedPythonSymbolName(symbol semantic.PythonSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "." + symbol.Name
	}
	return symbol.Name
}

func pythonSymbolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "class":
		return "type"
	case "function", "async_function":
		return "func"
	case "method", "async_method":
		return "method"
	default:
		return kind
	}
}

func pythonRawImportResolved(raw string, resolved []string) bool {
	raw = strings.TrimSpace(strings.TrimLeft(raw, "."))
	if raw == "" {
		return false
	}
	modulePath := strings.ReplaceAll(raw, ".", "/")
	for _, path := range resolved {
		if path == modulePath+".py" || path == modulePath+"/__init__.py" || strings.HasSuffix(path, "/"+modulePath+".py") || strings.HasSuffix(path, "/"+modulePath+"/__init__.py") {
			return true
		}
	}
	return false
}

func qualifiedTsSymbolName(symbol semantic.TsSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "." + symbol.Name
	}
	return symbol.Name
}

func tsSymbolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "class", "type", "enum":
		return "type"
	case "function":
		return "func"
	case "interface", "namespace", "method":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func qualifiedRustSymbolName(symbol semantic.RustSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "::" + symbol.Name
	}
	return symbol.Name
}

func rustSymbolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "struct", "enum", "type":
		return "type"
	case "trait":
		return "interface"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func rustModuleTargetPath(sourcePath string, module string, existing map[string]struct{}) string {
	module = strings.TrimSpace(module)
	if module == "" {
		return ""
	}
	dir := pathDir(sourcePath)
	candidates := []string{
		filepath.ToSlash(filepath.Join(dir, module+".rs")),
		filepath.ToSlash(filepath.Join(dir, module, "mod.rs")),
		filepath.ToSlash(filepath.Join("src", module+".rs")),
		filepath.ToSlash(filepath.Join("src", module, "mod.rs")),
	}
	return firstExistingPath(candidates, existing)
}

func rustUseTargetPath(sourcePath string, usePath string, existing map[string]struct{}) string {
	module := rustUseModule(usePath)
	if module == "" {
		return ""
	}
	return rustModuleTargetPath(sourcePath, module, existing)
}

func qualifiedJavaSymbolName(symbol semantic.JavaSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}
	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "." + symbol.Name
	}
	return symbol.Name
}

func javaSymbolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "class", "record", "enum", "annotation":
		return "type"
	case "interface":
		return "interface"
	case "method", "constructor", "field":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func javaImportTargetPaths(index *semantic.JavaIndex, file semantic.JavaFile) []string {
	out := make(map[string]struct{})
	for _, raw := range file.Imports {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		wildcard := strings.HasSuffix(raw, ".*")
		prefix := strings.TrimSuffix(raw, ".*")
		for _, candidate := range index.Files {
			for _, symbol := range candidate.Symbols {
				qualified := qualifiedJavaSymbolName(symbol)
				if wildcard {
					if strings.HasPrefix(qualified, prefix+".") {
						out[candidate.Path] = struct{}{}
					}
					continue
				}
				if qualified == prefix {
					out[candidate.Path] = struct{}{}
				}
			}
		}
	}
	return sortedStringSet(out)
}

func javaRawImportResolved(index *semantic.JavaIndex, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || index == nil {
		return false
	}
	wildcard := strings.HasSuffix(raw, ".*")
	prefix := strings.TrimSuffix(raw, ".*")
	for _, file := range index.Files {
		for _, symbol := range file.Symbols {
			qualified := qualifiedJavaSymbolName(symbol)
			if wildcard && strings.HasPrefix(qualified, prefix+".") {
				return true
			}
			if qualified == prefix {
				return true
			}
		}
	}
	return false
}

func qualifiedJavaScope(pkg string, scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	if pkg != "" && !strings.HasPrefix(scope, pkg+".") {
		return pkg + "." + scope
	}
	return scope
}

func hasShortJavaAnnotation(values []string, want string) bool {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if idx := strings.LastIndex(value, "."); idx >= 0 {
			value = value[idx+1:]
		}
		if strings.TrimSuffix(value, "()") == want {
			return true
		}
	}
	return false
}

func firstExistingPath(candidates []string, existing map[string]struct{}) string {
	for _, candidate := range candidates {
		candidate = filepath.ToSlash(filepath.Clean(candidate))
		if _, ok := existing[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func shouldUsePythonCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "python"
}

func shouldUseTypeScriptCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "typescript" || language == "javascript"
}

func shouldUseRustCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "rust"
}

func shouldUseJavaCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "java"
}
