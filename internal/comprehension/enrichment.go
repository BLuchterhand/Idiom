package comprehension

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type CollectorReport struct {
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Languages    []string       `json:"languages,omitempty"`
	Active       bool           `json:"active"`
	Available    bool           `json:"available"`
	Tools        []string       `json:"tools,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Paths        []string       `json:"paths,omitempty"`
	NodesAdded   int            `json:"nodes_added,omitempty"`
	EdgesAdded   int            `json:"edges_added,omitempty"`
	Diagnostics  int            `json:"diagnostics,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
}

type EnrichmentOptions struct {
	Root      string
	RepoID    string
	Scope     string
	Language  string
	Mode      string
	MaxFiles  int
	Now       string
	GitHead   string
	Languages []string
}

type GraphEnrichment struct {
	Reports []CollectorReport
	Nodes   []Node
	Edges   []Edge
}

type GraphEnricher interface {
	EnrichGraph(context.Context, Graph, EnrichmentOptions) (GraphEnrichment, error)
}

type languageBackendSpec struct {
	Language     string
	BackendName  string
	Binaries     []string
	Tools        []string
	Capabilities []string
	Manifests    []manifestSpec
	Impact       string
}

type manifestSpec struct {
	Path string
	Role string
}

var languageBackendSpecs = []languageBackendSpec{
	{
		Language:    "go",
		BackendName: "gopls",
		Binaries:    []string{"gopls"},
		Tools:       []string{"go_definition", "go_references", "go_calls_from", "go_calls_into", "go_implementations", "go_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{{Path: "go.mod", Role: "module_manifest"}, {Path: "go.work", Role: "workspace_manifest"}},
		Impact:    "Go graph facts start from the syntax index; gopls upgrades live definition, reference, implementation, call, and diagnostic probes.",
	},
	{
		Language:    "cpp",
		BackendName: "clangd",
		Binaries:    []string{"clangd"},
		Tools:       []string{"cpp_definition", "cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_implementations", "cpp_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{
			{Path: "compile_commands.json", Role: "compile_database"},
			{Path: "build/compile_commands.json", Role: "compile_database"},
			{Path: "out/compile_commands.json", Role: "compile_database"},
			{Path: "CMakeLists.txt", Role: "build_manifest"},
		},
		Impact: "C/C++ graph facts start from the syntax index; clangd and compile_commands.json upgrade live location, call, and diagnostic probes.",
	},
	{
		Language:    "python",
		BackendName: "python_lsp",
		Binaries:    []string{"pyright-langserver", "pylsp"},
		Tools:       []string{"python_definition", "python_references", "python_calls_from", "python_calls_into", "python_implementations", "python_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{
			{Path: "pyproject.toml", Role: "project_manifest"},
			{Path: "setup.py", Role: "project_manifest"},
			{Path: "setup.cfg", Role: "project_manifest"},
			{Path: "requirements.txt", Role: "dependency_manifest"},
			{Path: "Pipfile", Role: "dependency_manifest"},
		},
		Impact: "Python graph facts can be collected without an LSP; pyright or pylsp upgrades live locations, call hierarchy, implementations, and diagnostics.",
	},
	{
		Language:    "typescript",
		BackendName: "typescript_language_server",
		Binaries:    []string{"typescript-language-server"},
		Tools:       []string{"ts_definition", "ts_references", "ts_calls_from", "ts_calls_into", "ts_implementations", "ts_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{
			{Path: "package.json", Role: "package_manifest"},
			{Path: "tsconfig.json", Role: "compiler_config"},
			{Path: "jsconfig.json", Role: "compiler_config"},
		},
		Impact: "TypeScript/JavaScript graph facts can be collected without the language server; typescript-language-server upgrades live locations, call hierarchy, implementations, and diagnostics.",
	},
	{
		Language:    "javascript",
		BackendName: "typescript_language_server",
		Binaries:    []string{"typescript-language-server"},
		Tools:       []string{"ts_definition", "ts_references", "ts_calls_from", "ts_calls_into", "ts_implementations", "ts_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{
			{Path: "package.json", Role: "package_manifest"},
			{Path: "jsconfig.json", Role: "compiler_config"},
			{Path: "tsconfig.json", Role: "compiler_config"},
		},
		Impact: "JavaScript uses the TypeScript language server when available, while generic graph facts remain available without it.",
	},
	{
		Language:    "rust",
		BackendName: "rust_analyzer",
		Binaries:    []string{"rust-analyzer"},
		Tools:       []string{"rust_definition", "rust_references", "rust_calls_from", "rust_calls_into", "rust_implementations", "rust_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{{Path: "Cargo.toml", Role: "package_manifest"}, {Path: "Cargo.lock", Role: "lockfile"}},
		Impact:    "Rust graph facts can be collected without rust-analyzer; rust-analyzer upgrades live locations, call hierarchy, implementations, and diagnostics.",
	},
	{
		Language:    "java",
		BackendName: "jdtls",
		Binaries:    []string{"jdtls"},
		Tools:       []string{"java_definition", "java_references", "java_calls_from", "java_calls_into", "java_implementations", "java_diagnostics"},
		Capabilities: []string{
			"workspace_symbol", "definition", "references", "call_hierarchy", "implementations", "diagnostics",
		},
		Manifests: []manifestSpec{
			{Path: "pom.xml", Role: "build_manifest"},
			{Path: "build.gradle", Role: "build_manifest"},
			{Path: "build.gradle.kts", Role: "build_manifest"},
			{Path: "settings.gradle", Role: "workspace_manifest"},
		},
		Impact: "Java graph facts can be collected without JDT-LS; jdtls upgrades live locations, call hierarchy, implementations, and workspace symbols.",
	},
	{
		Language:    "gdscript",
		BackendName: "godot_project_model",
		Binaries:    []string{"godot", "godot4"},
		Tools:       []string{"context_query", "context_zoom", "context_pack"},
		Capabilities: []string{
			"script_symbols", "scene_resources", "autoloads", "resource_dependencies", "project_settings",
		},
		Manifests: []manifestSpec{{Path: "project.godot", Role: "godot_project_manifest"}},
		Impact:    "GDScript/Godot enrichment is graph-native today: scripts, scenes, resources, autoloads, and res:// dependencies are indexed even when a Godot executable is absent.",
	},
}

type languageEnvironmentEnricher struct{}

func defaultGraphEnrichers() []GraphEnricher {
	return []GraphEnricher{languageEnvironmentEnricher{}}
}

func (languageEnvironmentEnricher) EnrichGraph(_ context.Context, graph Graph, opts EnrichmentOptions) (GraphEnrichment, error) {
	activeLanguages := languageSet(opts.Languages)
	if len(activeLanguages) == 0 {
		activeLanguages = languageSet(graph.Languages)
	}
	var enrichment GraphEnrichment
	seenBackendNodes := make(map[string]struct{})
	handledLanguages := make(map[string]struct{})
	for _, spec := range languageBackendSpecs {
		if _, ok := activeLanguages[spec.Language]; !ok {
			continue
		}
		handledLanguages[spec.Language] = struct{}{}
		path, available := firstAvailableBinaryPath(spec.Binaries)
		backendID := toolchainNodeID(spec.Language, spec.BackendName)
		report := CollectorReport{
			Name:         spec.BackendName,
			Kind:         "language_backend",
			Languages:    []string{spec.Language},
			Active:       true,
			Available:    available || spec.Language == "gdscript",
			Tools:        append([]string(nil), spec.Tools...),
			Capabilities: append([]string(nil), spec.Capabilities...),
			Properties: map[string]any{
				"binaries": spec.Binaries,
				"impact":   spec.Impact,
			},
		}
		if path != "" {
			report.Properties["path"] = path
		}
		if !available && spec.Language != "gdscript" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s binary unavailable; graph facts fall back to syntax/generic collectors until live tools are installed", spec.BackendName))
		}
		if _, ok := seenBackendNodes[backendID]; !ok {
			enrichment.Nodes = append(enrichment.Nodes, Node{
				ID:            backendID,
				Kind:          "toolchain_backend",
				Language:      spec.Language,
				QualifiedName: spec.BackendName,
				Collector:     "language_environment",
				Confidence:    "exact",
				Properties: map[string]any{
					"available":    report.Available,
					"backend":      spec.BackendName,
					"binaries":     spec.Binaries,
					"path":         path,
					"tools":        spec.Tools,
					"capabilities": spec.Capabilities,
					"impact":       spec.Impact,
				},
			})
			enrichment.Edges = append(enrichment.Edges, Edge{
				SourceID:   opts.RepoID,
				TargetID:   backendID,
				Kind:       "observes_backend",
				Collector:  "language_environment",
				Confidence: "exact",
				Evidence: map[string]any{
					"language": spec.Language,
					"backend":  spec.BackendName,
				},
			})
			seenBackendNodes[backendID] = struct{}{}
			report.NodesAdded++
			report.EdgesAdded++
		}
		manifests := foundManifests(opts.Root, opts.Scope, spec.Manifests)
		if len(manifests) == 0 && len(spec.Manifests) > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("no %s manifest/config files were found in scope; graph facts remain available, but backend precision may be limited", spec.Language))
		}
		report.Properties["manifest_count"] = len(manifests)
		for _, manifest := range manifests {
			manifestID := manifestNodeID(manifest.Path)
			enrichment.Nodes = append(enrichment.Nodes, Node{
				ID:            manifestID,
				Kind:          "project_manifest",
				Path:          manifest.Path,
				Language:      spec.Language,
				QualifiedName: manifest.Role,
				Fingerprint:   manifest.Fingerprint,
				GitHead:       opts.GitHead,
				Collector:     "language_environment",
				Confidence:    "exact",
				Properties: map[string]any{
					"role":     manifest.Role,
					"language": spec.Language,
				},
			})
			enrichment.Edges = append(enrichment.Edges, Edge{
				SourceID:   opts.RepoID,
				TargetID:   manifestID,
				Kind:       "configures",
				Collector:  "language_environment",
				Confidence: "exact",
				Evidence: map[string]any{
					"path":     manifest.Path,
					"language": spec.Language,
				},
			})
			enrichment.Edges = append(enrichment.Edges, Edge{
				SourceID:   manifestID,
				TargetID:   backendID,
				Kind:       "enables_backend",
				Collector:  "language_environment",
				Confidence: "heuristic",
				Evidence: map[string]any{
					"path":     manifest.Path,
					"language": spec.Language,
				},
			})
			report.Paths = append(report.Paths, manifest.Path)
			report.NodesAdded++
			report.EdgesAdded += 2
		}
		report.Paths = uniqueSortedStrings(report.Paths)
		enrichment.Reports = append(enrichment.Reports, report)
	}
	for language := range activeLanguages {
		if _, ok := handledLanguages[language]; ok {
			continue
		}
		backendID := toolchainNodeID(language, "generic_language_model")
		report := CollectorReport{
			Name:      "generic_language_model",
			Kind:      "language_backend",
			Languages: []string{language},
			Active:    true,
			Available: true,
			Tools:     []string{"context_query", "context_zoom", "context_pack", "ctags_symbols", "structural_search"},
			Capabilities: []string{
				"file_symbols", "imports", "local_calls", "ctags_fallback", "structural_patterns",
			},
			NodesAdded: 1,
			EdgesAdded: 1,
			Properties: map[string]any{
				"impact": "No dedicated language backend is registered; generic source indexing plus ctags/structural_search can still provide language-agnostic graph facts.",
			},
		}
		enrichment.Nodes = append(enrichment.Nodes, Node{
			ID:            backendID,
			Kind:          "toolchain_backend",
			Language:      language,
			QualifiedName: "generic_language_model",
			Collector:     "language_environment",
			Confidence:    "heuristic",
			Properties: map[string]any{
				"available":    true,
				"backend":      "generic_language_model",
				"tools":        report.Tools,
				"capabilities": report.Capabilities,
				"impact":       report.Properties["impact"],
			},
		})
		enrichment.Edges = append(enrichment.Edges, Edge{
			SourceID:   opts.RepoID,
			TargetID:   backendID,
			Kind:       "observes_backend",
			Collector:  "language_environment",
			Confidence: "heuristic",
			Evidence: map[string]any{
				"language": language,
				"backend":  "generic_language_model",
			},
		})
		enrichment.Reports = append(enrichment.Reports, report)
	}
	sort.Slice(enrichment.Reports, func(i, j int) bool {
		if enrichment.Reports[i].Name == enrichment.Reports[j].Name {
			return strings.Join(enrichment.Reports[i].Languages, ",") < strings.Join(enrichment.Reports[j].Languages, ",")
		}
		return enrichment.Reports[i].Name < enrichment.Reports[j].Name
	})
	return enrichment, nil
}

type foundManifest struct {
	Path        string
	Role        string
	Fingerprint string
}

func applyGraphEnrichers(ctx context.Context, opts BuildOptions, graph Graph, repoID string, builder *graphBuilder, existingCoverage []string) ([]CollectorReport, error) {
	enrichers := append(defaultGraphEnrichers(), opts.Enrichers...)
	if len(enrichers) == 0 {
		return nil, nil
	}
	coverage := make(map[string]struct{}, len(existingCoverage))
	for _, collector := range existingCoverage {
		coverage[collector] = struct{}{}
	}
	enrichmentOptions := EnrichmentOptions{
		Root:      graph.Root,
		RepoID:    repoID,
		Scope:     cleanScope(opts.Path),
		Language:  normalizeLanguage(opts.Language),
		Mode:      normalizeIndexMode(opts.Mode),
		MaxFiles:  opts.MaxFiles,
		Now:       graph.GeneratedAt,
		GitHead:   graph.GitHead,
		Languages: graph.Languages,
	}
	var reports []CollectorReport
	for _, enricher := range enrichers {
		if enricher == nil {
			continue
		}
		snapshot := graph
		snapshot.Nodes = sortedNodes(builder.nodes)
		snapshot.Edges = sortedEdges(builder.edges)
		enrichment, err := enricher.EnrichGraph(ctx, snapshot, enrichmentOptions)
		if err != nil {
			return nil, fmt.Errorf("context graph enrichment: %w", err)
		}
		nodesBefore := len(builder.nodes)
		edgesBefore := len(builder.edges)
		for _, node := range enrichment.Nodes {
			if node.ID == "" {
				continue
			}
			builder.addNode(node)
		}
		for _, edge := range enrichment.Edges {
			if edge.SourceID == "" || edge.TargetID == "" || edge.Kind == "" {
				continue
			}
			if edge.ID == "" {
				edge.ID = edgeID(edge.Kind, edge.SourceID, edge.TargetID, edge.Evidence)
			}
			if edge.Collector == "" {
				edge.Collector = "context_enrichment"
			}
			if edge.Confidence == "" {
				edge.Confidence = "unknown"
			}
			if edge.CreatedAt == "" {
				edge.CreatedAt = builder.now
			}
			if edge.UpdatedAt == "" {
				edge.UpdatedAt = builder.now
			}
			builder.edges[edge.ID] = edge
		}
		addedNodes := len(builder.nodes) - nodesBefore
		addedEdges := len(builder.edges) - edgesBefore
		for _, report := range enrichment.Reports {
			report = normalizeCollectorReport(report, addedNodes, addedEdges)
			if report.Name != "" {
				coverage[report.Name] = struct{}{}
			}
			reports = append(reports, report)
		}
		graph.Nodes = sortedNodes(builder.nodes)
		graph.Edges = sortedEdges(builder.edges)
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Name == reports[j].Name {
			return strings.Join(reports[i].Languages, ",") < strings.Join(reports[j].Languages, ",")
		}
		return reports[i].Name < reports[j].Name
	})
	return reports, nil
}

func normalizeCollectorReport(report CollectorReport, addedNodes int, addedEdges int) CollectorReport {
	report.Name = strings.TrimSpace(report.Name)
	report.Kind = strings.TrimSpace(report.Kind)
	report.Languages = uniqueSortedStrings(report.Languages)
	report.Tools = uniqueSortedStrings(report.Tools)
	report.Capabilities = uniqueSortedStrings(report.Capabilities)
	report.Paths = uniqueSortedStrings(report.Paths)
	report.Warnings = uniqueSortedStrings(report.Warnings)
	if report.NodesAdded == 0 {
		report.NodesAdded = addedNodes
	}
	if report.EdgesAdded == 0 {
		report.EdgesAdded = addedEdges
	}
	return report
}

func foundManifests(root string, scope string, manifests []manifestSpec) []foundManifest {
	out := make([]foundManifest, 0, len(manifests))
	for _, manifest := range manifests {
		rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(manifest.Path)))
		if rel == "" || rel == "." || !pathInScope(rel, scope) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		fingerprint, _ := fileFingerprint(abs)
		out = append(out, foundManifest{Path: rel, Role: manifest.Role, Fingerprint: fingerprint})
	}
	slices.SortFunc(out, func(a, b foundManifest) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

func languageSet(languages []string) map[string]struct{} {
	out := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		language = normalizeLanguage(language)
		if language != "" && language != "auto" {
			out[language] = struct{}{}
		}
	}
	return out
}

func firstAvailableBinaryPath(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}

func toolchainNodeID(language string, backend string) string {
	return "toolchain://" + normalizeLanguage(language) + "/" + strings.TrimSpace(backend)
}

func manifestNodeID(path string) string {
	return "manifest://" + filepath.ToSlash(filepath.Clean(path))
}

func evidenceNodeID(tool string, subject string) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + subject))
	return "evidence://" + strings.TrimSpace(tool) + "/" + hex.EncodeToString(sum[:8])
}

func diagnosticNodeID(language string, path string, tool string) string {
	sum := sha256.Sum256([]byte(language + "\x00" + path + "\x00" + tool))
	return "diagnostic://" + normalizeLanguage(language) + "/" + hex.EncodeToString(sum[:8])
}

func normalizeIndexMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick", "standard", "deep":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "standard"
	}
}

func timestampNowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
