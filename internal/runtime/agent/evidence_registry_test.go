package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

// --- path evidence registry tests ---
func TestRegisterToolResultsUsesEvidenceOwnedDTO(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())

	registry.RegisterToolResults([]ToolResult{{
		ToolName: "read_file",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		Summary:  "Read internal/runtime/agent/evidence_registry.go",
	}})

	if !registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("expected body evidence from evidence-owned DTO")
	}
}

func TestPathEvidenceRegistryRejectsAbsoluteAndEscapingBodyReadPaths(t *testing.T) {
	registry := NewPathEvidenceRegistry(t.TempDir())

	registry.RegisterToolResults([]ToolResult{{
		ToolName: "read_file",
		Paths: []string{
			"/tmp/outside.go",
			`C:\tmp\outside.go`,
			"C:/tmp/outside.go",
			"../outside.go",
			"internal/runtime/agent/evidence_registry.go",
		},
		Summary: "Read internal/runtime/agent/evidence_registry.go",
	}})

	if registry.HasBodyEvidence("/tmp/outside.go") {
		t.Fatal("absolute body-read path must not be accepted as workspace evidence")
	}
	if registry.HasBodyEvidence("../outside.go") {
		t.Fatal("parent-escaping body-read path must not be accepted as workspace evidence")
	}
	if registry.HasBodyEvidence(`C:\tmp\outside.go`) || registry.HasBodyEvidence("C:/tmp/outside.go") {
		t.Fatal("Windows absolute body-read paths must not be accepted as workspace evidence")
	}
	if !registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("repo-local body-read path should still be accepted")
	}
}

func TestCandidatePathExistenceResolvesLazily(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "runtime", "agent", "evidence_registry.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture path: %v", err)
	}
	if err := os.WriteFile(path, []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	registry := NewPathEvidenceRegistry(root)
	registry.RegisterToolResults([]ToolResult{{
		ToolName: "search_content",
		Paths:    []string{"internal/runtime/agent/evidence_registry.go"},
		Summary:  "Found internal/runtime/agent/evidence_registry.go",
	}})

	if !registry.EnsurePathExists("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("expected candidate path existence to resolve lazily")
	}
	if registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
		t.Fatal("search candidate must not count as body evidence")
	}
}

func TestPathEvidenceRegistryAndRawReadModelBodyEvidenceParity(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_registry.go"
	const generatedPath = ".idiom/comprehension/views/context.json"

	tests := []struct {
		name             string
		path             string
		result           workspace.ToolResult
		wantRawLevel     PlanThenExecuteSourceEvidenceLevel
		wantRawBody      bool
		wantRegistryBody bool
	}{
		{
			name: "read_file bounded source range agrees as body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read internal/runtime/agent/evidence_registry.go lines 20-40 (total lines: 220)",
				Output:  "func TestRegisterToolResultsUsesEvidenceOwnedDTO(t *testing.T) {}\n",
			},
			wantRawLevel:     planThenExecuteEvidenceAnchorReady,
			wantRawBody:      true,
			wantRegistryBody: true,
		},
		{
			name: "failed read_file agrees as no body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read internal/runtime/agent/evidence_registry.go lines 20-40 (total lines: 220)",
				Error:   "permission denied",
			},
			wantRawLevel:     planThenExecuteEvidenceNone,
			wantRawBody:      false,
			wantRegistryBody: false,
		},
		{
			name: "redirect-only read_file remains pending in raw model and non-body in registry",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Redirected internal/runtime/agent/evidence_registry.go to code_navigate",
				Output:  "Use code_navigate for source-body evidence in internal/runtime/agent/evidence_registry.go.",
			},
			wantRawLevel:     planThenExecuteEvidenceSourceBodyPending,
			wantRawBody:      false,
			wantRegistryBody: false,
		},
		{
			name: "generated artifacts agree as non-body evidence",
			path: generatedPath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-20 (total lines: 20)",
				Output:  `{"candidate_files":[{"path":"internal/runtime/agent/evidence_registry.go"}]}`,
			},
			wantRawLevel:     planThenExecuteEvidenceNone,
			wantRawBody:      false,
			wantRegistryBody: false,
		},
		{
			name: "routed code_navigate source body agrees as body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool: workspace.ToolNameCodeNavigate,
				Output: "route_summary: Read internal/runtime/agent/evidence_registry.go lines 45-70 (total lines: 220)\n" +
					"func TestCandidatePathExistenceResolvesLazily(t *testing.T) {}\n",
			},
			wantRawLevel:     planThenExecuteEvidenceAnchorReady,
			wantRawBody:      true,
			wantRegistryBody: true,
		},
		{
			name: "symbol read agrees as body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    "go_read_symbol",
				Summary: "Read TestRegisterToolResultsUsesEvidenceOwnedDTO in internal/runtime/agent/evidence_registry.go lines 12-24",
				Output:  "func TestRegisterToolResultsUsesEvidenceOwnedDTO(t *testing.T) {}\n",
			},
			wantRawLevel:     planThenExecuteEvidenceAnchorReady,
			wantRawBody:      true,
			wantRegistryBody: true,
		},
		{
			name: "context zoom include source agrees as body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameContextZoom,
				Summary: "Read internal/runtime/agent/evidence_registry.go lines 1-20 with include_source",
				Output:  `{"path":"internal/runtime/agent/evidence_registry.go","source":"func TestRegisterToolResultsUsesEvidenceOwnedDTO(t *testing.T) {}"}`,
			},
			wantRawLevel:     planThenExecuteEvidenceAnchorReady,
			wantRawBody:      true,
			wantRegistryBody: true,
		},
		{
			name: "candidate anchor only agrees as non-body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Read internal/runtime/agent/evidence_registry.go lines 1-20",
				Output:  "path-filter miss\ncandidate_anchor_status: anchors only",
			},
			wantRawLevel:     planThenExecuteEvidencePathSeen,
			wantRawBody:      false,
			wantRegistryBody: false,
		},
		{
			name: "file outline is path progress but not body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Read outline in internal/runtime/agent/evidence_registry.go with capability: file_outline",
				Output:  "capability: file_outline\nfn TestRegisterToolResultsUsesEvidenceOwnedDTO\nstruct PathEvidenceRegistry\n",
			},
			wantRawLevel:     planThenExecuteEvidencePathSeen,
			wantRawBody:      false,
			wantRegistryBody: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []workspace.ToolResult{tt.result}
			rawEvidence := sourceEvidenceForPath(results, tt.path)
			if rawEvidence.Level != tt.wantRawLevel {
				t.Fatalf("raw evidence level = %q, want %q; evidence=%#v", rawEvidence.Level, tt.wantRawLevel, rawEvidence)
			}
			if got := sourceEvidenceHasBody(rawEvidence); got != tt.wantRawBody {
				t.Fatalf("raw body evidence = %v, want %v; evidence=%#v", got, tt.wantRawBody, rawEvidence)
			}

			registry := NewPathEvidenceRegistry(t.TempDir())
			registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(tt.result)})
			if got := registry.HasBodyEvidence(tt.path); got != tt.wantRegistryBody {
				t.Fatalf("registry body evidence = %v, want %v", got, tt.wantRegistryBody)
			}
		})
	}
}

func TestContextZoomIncludeSourceFlagWithoutBodyIsNotConcreteSourceEvidence(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_registry.go"
	result := workspace.ToolResult{
		Tool:    workspace.ToolNameContextZoom,
		Summary: "Read internal/runtime/agent/evidence_registry.go lines 1-20 with include_source",
		Output: `{
			"node": {
				"id": "symbol://internal/runtime/agent/evidence_registry.go#PathEvidenceRegistry",
				"path": "internal/runtime/agent/evidence_registry.go",
				"source": "",
				"source_body": "",
				"properties": {"source": "context_graph"}
			}
		}`,
	}

	if contextZoomResultHasConcreteSourceBody(result) {
		t.Fatal("include_source graph metadata must not count as concrete source-body evidence")
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(result)})
	if registry.HasBodyEvidence(sourcePath) {
		t.Fatal("include_source graph metadata must not become registry body evidence")
	}
}

func TestPathEvidenceCandidateOnlyParity(t *testing.T) {
	const sourcePath = "evidence_registry.go"

	result := workspace.ToolResult{
		Tool:    workspace.ToolNameSearchContent,
		Summary: "1 matching line(s) in evidence_registry.go",
		Output:  sourcePath + ":12:type PathEvidenceRegistry struct {",
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(result)})

	if !registry.EnsurePathExists(sourcePath) {
		t.Fatal("search candidate should resolve lazily against the workspace")
	}
	if registry.HasBodyEvidence(sourcePath) {
		t.Fatal("search candidate must not become registry body evidence")
	}
	if sourceEvidenceBodyReadForPath(sourcePath, []workspace.ToolResult{result}) {
		t.Fatal("search candidate must not become raw source-body evidence")
	}
	if got := sourceEvidenceCandidatePaths([]workspace.ToolResult{result}); !slices.Contains(got, sourcePath) {
		t.Fatalf("raw candidate paths = %#v, want %q", got, sourcePath)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain path evidence registry behavior.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}, []workspace.ToolResult{result})

	if !slices.Contains(ledger.CandidatePaths, sourcePath) {
		t.Fatalf("ledger candidate paths = %#v, want %q", ledger.CandidatePaths, sourcePath)
	}
	if slices.Contains(ledger.SourceBodyPaths, sourcePath) {
		t.Fatalf("candidate-only path promoted to source-body paths: %#v", ledger.SourceBodyPaths)
	}
	if !ledger.HasUnreadCandidatePaths {
		t.Fatalf("candidate-only ledger should require a source-body follow-up: %#v", ledger)
	}

	withBody := append([]workspace.ToolResult{result}, workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read evidence_registry.go lines 1-20",
		Output:  "package agent\ntype PathEvidenceRegistry struct{}\n",
	})
	if got := sourceEvidenceCandidatePaths(withBody); slices.Contains(got, sourcePath) {
		t.Fatalf("candidate paths after same-path body read = %#v, must not include %q", got, sourcePath)
	}
	bodyLedger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain path evidence registry behavior.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}, withBody)
	if bodyLedger.HasUnreadCandidatePaths {
		t.Fatalf("same-path body read should clear unread candidate state: %#v", bodyLedger)
	}
	bodyRegistry := NewPathEvidenceRegistry(".")
	bodyRegistry.RegisterToolResults([]ToolResult{
		FromWorkspaceResult(result),
		FromWorkspaceResult(withBody[1]),
	})
	if !bodyRegistry.HasBodyEvidence(sourcePath) {
		t.Fatal("same-path body read should promote registry body evidence")
	}
}

func TestPathEvidenceContextDiscoveryCandidateParity(t *testing.T) {
	const sourcePath = "evidence_registry.go"

	tests := []struct {
		name   string
		result workspace.ToolResult
	}{
		{
			name: "context pack candidate",
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameContextPack,
				Summary: "Context pack includes evidence registry candidates.",
				Output:  `{"candidate_files":[{"path":"evidence_registry.go","reason":"ranked"}]}`,
			},
		},
		{
			name: "discovery query candidate",
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameDiscoveryQuery,
				Summary: "Discovery query found evidence registry implementation paths.",
				Output: `{
					"evidence":[{
						"name":"text_anchor_search",
						"summary":"Found path evidence registry implementation.",
						"output_excerpt":"evidence_registry.go:88:func (r *PathEvidenceRegistry) RegisterToolResults(results []ToolResult)"
					}]
				}`,
			},
		},
		{
			name: "context query candidate",
			result: workspace.ToolResult{
				Tool:    "context_query",
				Summary: "Context query found evidence registry implementation paths.",
				Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
			},
		},
		{
			name: "context impact candidate",
			result: workspace.ToolResult{
				Tool:    "context_impact",
				Summary: "Context impact found evidence registry implementation paths.",
				Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
			},
		},
		{
			name: "structural search candidate",
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameStructuralSearch,
				Summary: "Structural search matched evidence registry implementation paths.",
				Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
			},
		},
		{
			name: "workspace symbol candidate",
			result: workspace.ToolResult{
				Tool:    "go_workspace_symbol",
				Summary: "Workspace symbol matched PathEvidenceRegistry.",
				Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
			},
		},
		{
			name: "rg candidate",
			result: workspace.ToolResult{
				Tool:    "rg",
				Summary: "1 matching line(s) in evidence_registry.go",
				Output:  sourcePath + ":63:type PathEvidenceRegistry struct",
			},
		},
		{
			name: "grep candidate",
			result: workspace.ToolResult{
				Tool:    "grep",
				Summary: "1 matching line(s) in evidence_registry.go",
				Output:  sourcePath + ":63:type PathEvidenceRegistry struct",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []workspace.ToolResult{tt.result}
			if got := sourceEvidenceCandidatePaths(results); !slices.Contains(got, sourcePath) {
				t.Fatalf("raw candidate paths = %#v, want %q", got, sourcePath)
			}
			if got := readOnlyEvidencePacketCandidatePaths(results); !slices.Contains(got, sourcePath) {
				t.Fatalf("packet candidate paths = %#v, want %q", got, sourcePath)
			}

			ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
				Objective:         "Explain path evidence registry behavior.",
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				PermissionProfile: "read_only_workspace",
			}, results)
			if !slices.Contains(ledger.CandidatePaths, sourcePath) {
				t.Fatalf("ledger candidate paths = %#v, want %q", ledger.CandidatePaths, sourcePath)
			}
			if slices.Contains(ledger.SourceBodyPaths, sourcePath) {
				t.Fatalf("context/discovery candidate promoted to source-body paths: %#v", ledger.SourceBodyPaths)
			}
			if !ledger.HasUnreadCandidatePaths {
				t.Fatalf("context/discovery candidate should remain unread until a source-body follow-up: %#v", ledger)
			}

			registry := NewPathEvidenceRegistry(".")
			registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(tt.result)})
			registry.mu.RLock()
			evidence, registered := registry.paths[sourcePath]
			registry.mu.RUnlock()
			if !registered || !evidence.IsCandidate {
				t.Fatalf("registry candidate evidence for %q from %s = %#v, registered=%v", sourcePath, tt.result.Tool, evidence, registered)
			}
			if registry.HasBodyEvidence(sourcePath) {
				t.Fatal("context/discovery candidate must not become registry body evidence")
			}
		})
	}
}

func TestPathEvidenceContextZoomCandidateOnlyRegistersUnreadCandidate(t *testing.T) {
	const sourcePath = "evidence_registry.go"
	result := workspace.ToolResult{
		Tool:    workspace.ToolNameContextZoom,
		Summary: "Context zoom found evidence registry implementation paths.",
		Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
	}
	results := []workspace.ToolResult{result}

	if got := sourceEvidenceCandidatePaths(results); !slices.Contains(got, sourcePath) {
		t.Fatalf("raw candidate paths = %#v, want %q", got, sourcePath)
	}
	if got := readOnlyEvidencePacketCandidatePaths(results); !slices.Contains(got, sourcePath) {
		t.Fatalf("packet candidate paths = %#v, want %q", got, sourcePath)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain path evidence registry behavior.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}, results)
	if !slices.Contains(ledger.CandidatePaths, sourcePath) {
		t.Fatalf("candidate-only context_zoom should promote an answerability ledger candidate: %#v", ledger.CandidatePaths)
	}
	if !ledger.HasUnreadCandidatePaths {
		t.Fatalf("candidate-only context_zoom should remain an unread answerability candidate: %#v", ledger)
	}
	if slices.Contains(ledger.SourceBodyPaths, sourcePath) {
		t.Fatalf("candidate-only context_zoom must not become source-body paths: %#v", ledger.SourceBodyPaths)
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(result)})
	registry.mu.RLock()
	evidence, registered := registry.paths[sourcePath]
	registry.mu.RUnlock()
	if !registered || !evidence.IsCandidate {
		t.Fatalf("registry candidate evidence for %q from context_zoom = %#v, registered=%v", sourcePath, evidence, registered)
	}
	if registry.HasBodyEvidence(sourcePath) {
		t.Fatal("candidate-only context_zoom must not become registry body evidence")
	}
}

func TestPathEvidenceContextZoomGraphOutputRegistersUnreadCandidate(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_registry.go"
	result := workspace.ToolResult{
		Tool:    workspace.ToolNameContextZoom,
		Summary: "Context zoom returned 0 related node(s) and 0 edge(s)",
		Output: `{
			"index_id": "graph-test",
			"fresh": true,
			"resolution": "symbol",
			"provenance": {"source": "context_graph", "operation": "context_zoom", "index_id": "graph-test"},
			"node": {
				"id": "symbol://internal/runtime/agent/evidence_registry.go#PathEvidenceRegistry",
				"kind": "symbol",
				"path": "internal/runtime/agent/evidence_registry.go",
				"qualified_name": "PathEvidenceRegistry",
				"start_line": 63,
				"end_line": 75,
				"confidence": "high"
			}
		}`,
	}
	results := []workspace.ToolResult{result}

	if got := sourceEvidenceCandidatePaths(results); !slices.Contains(got, sourcePath) {
		t.Fatalf("production-shaped context_zoom graph output should register a raw candidate path: %#v", got)
	}
	if sourceEvidenceBodyReadForPath(sourcePath, results) {
		t.Fatal("production-shaped context_zoom graph output must not count as raw source-body evidence")
	}
	if got := readOnlyEvidencePacketCandidatePaths(results); !slices.Contains(got, sourcePath) {
		t.Fatalf("packet candidate paths = %#v, want %q", got, sourcePath)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain path evidence registry behavior.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}, results)
	if !slices.Contains(ledger.CandidatePaths, sourcePath) {
		t.Fatalf("production-shaped context_zoom graph output should register an answerability candidate: %#v", ledger.CandidatePaths)
	}
	if !ledger.HasUnreadCandidatePaths {
		t.Fatalf("production-shaped context_zoom graph output should remain an unread answerability candidate: %#v", ledger)
	}
	if slices.Contains(ledger.SourceBodyPaths, sourcePath) {
		t.Fatalf("graph-shaped context_zoom must not become source-body paths: %#v", ledger.SourceBodyPaths)
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(result)})
	registry.mu.RLock()
	evidence, registered := registry.paths[sourcePath]
	registry.mu.RUnlock()
	if !registered || !evidence.IsCandidate {
		t.Fatalf("production-shaped context_zoom graph output should register candidate state for %q: %#v", sourcePath, evidence)
	}
	if registry.HasBodyEvidence(sourcePath) {
		t.Fatal("graph-shaped context_zoom must not become registry body evidence")
	}
}

func TestPathEvidenceRegistryCandidateBoundaries(t *testing.T) {
	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{{
		ToolName: workspace.ToolNameContextPack,
		Paths: []string{
			"internal/runtime/agent/evidence_registry.go",
			"../outside.go",
			"/tmp/outside.go",
			`C:\tmp\outside.go`,
			".idiom/comprehension/views/context.json",
			".codex/discovery/queries/latest.json",
			"transcripts/run.jsonl",
		},
		Summary: "Context pack candidate boundary fixture.",
	}})

	registry.mu.RLock()
	_, validRegistered := registry.paths["internal/runtime/agent/evidence_registry.go"]
	_, parentRegistered := registry.paths["../outside.go"]
	_, absoluteRegistered := registry.paths["/tmp/outside.go"]
	_, windowsRegistered := registry.paths[`C:\tmp\outside.go`]
	_, idiomRegistered := registry.paths[".idiom/comprehension/views/context.json"]
	_, codexRegistered := registry.paths[".codex/discovery/queries/latest.json"]
	_, transcriptRegistered := registry.paths["transcripts/run.jsonl"]
	registry.mu.RUnlock()

	if !validRegistered {
		t.Fatal("expected source-like workspace candidate to register")
	}
	for name, registered := range map[string]bool{
		"parent escape":  parentRegistered,
		"absolute":       absoluteRegistered,
		"windows abs":    windowsRegistered,
		"idiom artifact": idiomRegistered,
		"codex artifact": codexRegistered,
		"transcript":     transcriptRegistered,
	} {
		if registered {
			t.Fatalf("%s candidate must not register", name)
		}
	}
}

func TestPathEvidenceReadOnlyAnswerabilityConsolidationMatrix(t *testing.T) {
	const sourcePath = "evidence_registry.go"

	brief := func(objective string) workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			Objective:         objective,
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		}
	}
	typedTaskResult := func(objective string) workspace.ToolResult {
		return typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
			Objective:       objective,
			Question:        objective,
			SourceBodyPaths: []string{sourcePath},
			InspectedPaths:  []string{sourcePath},
			Confidence:      "high",
			Findings: []ReadOnlyEvidenceFinding{{
				Path: sourcePath,
				Text: "PathEvidenceRegistry records path evidence for answerability decisions.",
			}},
		})
	}
	registryCandidate := func(registry *PathEvidenceRegistry, path string) bool {
		registry.mu.RLock()
		defer registry.mu.RUnlock()

		evidence, ok := registry.paths[normalizeRegistryPath(path)]
		return ok && evidence.IsCandidate
	}

	tests := []struct {
		name                       string
		brief                      workerruntime.WorkerRuntimeBrief
		results                    []workspace.ToolResult
		wantKind                   readOnlyEvidenceKind
		wantAnswerable             bool
		wantLedgerCandidate        bool
		wantLedgerSourceBody       bool
		wantLedgerRelevantBody     bool
		wantUnreadCandidate        bool
		wantDiagnostic             bool
		wantDiagnosticRelevant     bool
		wantPlanArtifact           bool
		wantPlanArtifactRelevant   bool
		wantRegistryCandidate      bool
		wantRegistryBody           bool
		wantRegistryPathRegistered bool
	}{
		{
			name:  "read_file source body agrees with registry body evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read evidence_registry.go lines 1-20",
				Output:  "package agent\ntype PathEvidenceRegistry struct{}\n",
			}},
			wantKind:                   readOnlyEvidenceSourceBody,
			wantAnswerable:             true,
			wantLedgerSourceBody:       true,
			wantLedgerRelevantBody:     true,
			wantRegistryBody:           true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "search candidate agrees with registry candidate evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameSearchContent,
				Summary: "1 matching line(s) in evidence_registry.go",
				Output:  sourcePath + ":12:type PathEvidenceRegistry struct{}",
			}},
			wantKind:                   readOnlyEvidenceSearchHit,
			wantLedgerCandidate:        true,
			wantUnreadCandidate:        true,
			wantRegistryCandidate:      true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "context pack candidate agrees with registry candidate evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameContextPack,
				Summary: "Context pack includes evidence registry candidates.",
				Output:  `{"candidate_files":[{"path":"evidence_registry.go","reason":"ranked"}]}`,
			}},
			wantKind:                   readOnlyEvidenceContextPacket,
			wantLedgerCandidate:        true,
			wantUnreadCandidate:        true,
			wantRegistryCandidate:      true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "discovery query candidate agrees with registry candidate evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameDiscoveryQuery,
				Summary: "Discovery query found evidence registry implementation paths.",
				Output: `{
					"evidence":[{
						"name":"text_anchor_search",
						"summary":"Found path evidence registry implementation.",
						"output_excerpt":"evidence_registry.go:88:func (r *PathEvidenceRegistry) RegisterToolResults(results []ToolResult)"
					}]
				}`,
			}},
			wantKind:                   readOnlyEvidenceSearchHit,
			wantLedgerCandidate:        true,
			wantUnreadCandidate:        true,
			wantRegistryCandidate:      true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "context query candidate agrees with registry candidate evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    "context_query",
				Summary: "Context query found evidence registry implementation paths.",
				Output:  `{"source_owner_found":false,"candidates":[{"path":"evidence_registry.go"}]}`,
			}},
			wantKind:                   readOnlyEvidenceSearchHit,
			wantLedgerCandidate:        true,
			wantUnreadCandidate:        true,
			wantRegistryCandidate:      true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "context zoom source body agrees with registry body evidence",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameContextZoom,
				Summary: "Read evidence_registry.go lines 1-20 with include_source",
				Output:  `{"path":"evidence_registry.go","source":"type PathEvidenceRegistry struct{}"}`,
			}},
			wantKind:                   readOnlyEvidenceSourceBody,
			wantAnswerable:             true,
			wantLedgerSourceBody:       true,
			wantLedgerRelevantBody:     true,
			wantRegistryCandidate:      true,
			wantRegistryBody:           true,
			wantRegistryPathRegistered: true,
		},
		{
			name:  "diagnostic evidence is answerability evidence but not registry evidence",
			brief: brief("Why did diagnostics fail for evidence_registry.go?"),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameSourceSanity,
				Summary: "source_sanity failed for evidence_registry.go",
				Output:  sourcePath + ":10: undefined: PathEvidenceRegistry\n",
			}},
			wantKind:                   readOnlyEvidenceDiagnostic,
			wantAnswerable:             true,
			wantLedgerCandidate:        true,
			wantUnreadCandidate:        true,
			wantDiagnostic:             true,
			wantDiagnosticRelevant:     true,
			wantRegistryPathRegistered: false,
		},
		{
			name:  "typed task packet source body is answerability evidence but not registry evidence",
			brief: brief("Can you tell me how path evidence registry records answerability evidence?"),
			results: []workspace.ToolResult{
				typedTaskResult("Can you tell me how path evidence registry records answerability evidence?"),
			},
			wantKind:                   readOnlyEvidenceSourceBody,
			wantAnswerable:             true,
			wantLedgerCandidate:        true,
			wantLedgerSourceBody:       true,
			wantLedgerRelevantBody:     true,
			wantRegistryPathRegistered: false,
		},
		{
			name:  "plan artifact answerability is outside registry evidence",
			brief: brief("What does the active plan entail?"),
			results: []workspace.ToolResult{{
				Tool:        featurePlannerPlanArtifactPayloadType,
				PayloadType: featurePlannerPlanArtifactPayloadType,
				Summary:     "Feature planner proposed changes for evidence_registry.go.",
				Payload:     json.RawMessage(`{"proposed_paths":["evidence_registry.go"]}`),
			}},
			wantKind:                 readOnlyEvidencePlanArtifact,
			wantAnswerable:           true,
			wantPlanArtifact:         true,
			wantPlanArtifactRelevant: true,
		},
		{
			name:  "outline evidence remains non-answerable and non-registry-body",
			brief: brief("Explain evidence registry behavior."),
			results: []workspace.ToolResult{{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Outlined 12 top-level declaration(s) in evidence_registry.go",
				Output: strings.Join([]string{
					"file_outline: evidence_registry.go",
					"- PathEvidenceRegistry lines 1-40",
				}, "\n"),
			}},
			wantKind: readOnlyEvidenceOutline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := readOnlyAnswerabilityForResults(tt.brief, tt.results)
			if got := ledger.Answerable(); got != tt.wantAnswerable {
				t.Fatalf("ledger.Answerable() = %v, want %v; ledger=%#v", got, tt.wantAnswerable, ledger)
			}
			if tt.wantKind != "" && !ledger.HasKind(tt.wantKind) {
				t.Fatalf("ledger missing evidence kind %q: %#v", tt.wantKind, ledger.Items)
			}
			if got := slices.Contains(ledger.CandidatePaths, sourcePath); got != tt.wantLedgerCandidate {
				t.Fatalf("ledger candidate path presence = %v, want %v; candidates=%#v", got, tt.wantLedgerCandidate, ledger.CandidatePaths)
			}
			if got := slices.Contains(ledger.SourceBodyPaths, sourcePath); got != tt.wantLedgerSourceBody {
				t.Fatalf("ledger source-body path presence = %v, want %v; source=%#v", got, tt.wantLedgerSourceBody, ledger.SourceBodyPaths)
			}
			if got := slices.Contains(ledger.RelevantSourceBodyPaths, sourcePath); got != tt.wantLedgerRelevantBody {
				t.Fatalf("ledger relevant source-body path presence = %v, want %v; relevant=%#v", got, tt.wantLedgerRelevantBody, ledger.RelevantSourceBodyPaths)
			}
			if ledger.HasUnreadCandidatePaths != tt.wantUnreadCandidate {
				t.Fatalf("ledger.HasUnreadCandidatePaths = %v, want %v; ledger=%#v", ledger.HasUnreadCandidatePaths, tt.wantUnreadCandidate, ledger)
			}
			if ledger.DiagnosticEvidence != tt.wantDiagnostic {
				t.Fatalf("ledger.DiagnosticEvidence = %v, want %v; ledger=%#v", ledger.DiagnosticEvidence, tt.wantDiagnostic, ledger)
			}
			if ledger.DiagnosticRelevant != tt.wantDiagnosticRelevant {
				t.Fatalf("ledger.DiagnosticRelevant = %v, want %v; ledger=%#v", ledger.DiagnosticRelevant, tt.wantDiagnosticRelevant, ledger)
			}
			if got := slices.Contains(ledger.DiagnosticPaths, sourcePath); got != tt.wantDiagnostic {
				t.Fatalf("ledger diagnostic path presence = %v, want %v; paths=%#v", got, tt.wantDiagnostic, ledger.DiagnosticPaths)
			}
			if ledger.PlanArtifactEvidence != tt.wantPlanArtifact {
				t.Fatalf("ledger.PlanArtifactEvidence = %v, want %v; ledger=%#v", ledger.PlanArtifactEvidence, tt.wantPlanArtifact, ledger)
			}
			if ledger.PlanArtifactRelevant != tt.wantPlanArtifactRelevant {
				t.Fatalf("ledger.PlanArtifactRelevant = %v, want %v; ledger=%#v", ledger.PlanArtifactRelevant, tt.wantPlanArtifactRelevant, ledger)
			}
			if got := slices.Contains(ledger.PlanArtifactPaths, sourcePath); got != tt.wantPlanArtifact {
				t.Fatalf("ledger plan artifact path presence = %v, want %v; paths=%#v", got, tt.wantPlanArtifact, ledger.PlanArtifactPaths)
			}

			registry := NewPathEvidenceRegistry(".")
			var registryResults []ToolResult
			for _, result := range tt.results {
				registryResults = append(registryResults, FromWorkspaceResult(result))
			}
			registry.RegisterToolResults(registryResults)
			if got := registryCandidate(registry, sourcePath); got != tt.wantRegistryCandidate {
				t.Fatalf("registry candidate presence = %v, want %v", got, tt.wantRegistryCandidate)
			}
			if got := registry.HasBodyEvidence(sourcePath); got != tt.wantRegistryBody {
				t.Fatalf("registry body evidence = %v, want %v", got, tt.wantRegistryBody)
			}
			registry.mu.RLock()
			_, registered := registry.paths[sourcePath]
			registry.mu.RUnlock()
			if registered != tt.wantRegistryPathRegistered {
				t.Fatalf("registry path registered = %v, want %v; registry=%#v", registered, tt.wantRegistryPathRegistered, registry.paths[sourcePath])
			}
		})
	}
}

func TestPathEvidenceReadOnlyAnswerabilityNonRegistryEvidenceRequiresObjectiveMatch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain renderer layout behavior.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	tests := []struct {
		name     string
		result   workspace.ToolResult
		wantKind readOnlyEvidenceKind
	}{
		{
			name: "unrelated diagnostic is evidence but not answerable",
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameSourceSanity,
				Summary: "source_sanity failed for internal/runtime/agent/evidence_registry.go",
				Output:  "internal/runtime/agent/evidence_registry.go:10: undefined: PathEvidenceRegistry\n",
			},
			wantKind: readOnlyEvidenceDiagnostic,
		},
		{
			name: "objective-mismatched task packet is evidence but not answerable",
			result: typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
				Objective:       "Explain path evidence registry behavior.",
				Question:        "Explain path evidence registry behavior.",
				SourceBodyPaths: []string{"internal/runtime/agent/evidence_registry.go"},
				InspectedPaths:  []string{"internal/runtime/agent/evidence_registry.go"},
				Confidence:      "high",
				Findings: []ReadOnlyEvidenceFinding{{
					Path: "internal/runtime/agent/evidence_registry.go",
					Text: "PathEvidenceRegistry records source-body and candidate path evidence.",
				}},
			}),
		},
		{
			name: "unrelated plan artifact is evidence but not answerable",
			result: workspace.ToolResult{
				Tool:        featurePlannerPlanArtifactPayloadType,
				PayloadType: featurePlannerPlanArtifactPayloadType,
				Summary:     "Feature planner proposed changes for internal/runtime/agent/evidence_registry.go.",
				Payload:     json.RawMessage(`{"proposed_paths":["internal/runtime/agent/evidence_registry.go"]}`),
			},
			wantKind: readOnlyEvidencePlanArtifact,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := readOnlyAnswerabilityForResults(brief, []workspace.ToolResult{tt.result})
			if tt.wantKind != "" && !ledger.HasKind(tt.wantKind) {
				t.Fatalf("ledger missing evidence kind %q: %#v", tt.wantKind, ledger.Items)
			}
			if ledger.Answerable() {
				t.Fatalf("objective-mismatched evidence must not be answerable: %#v", ledger)
			}

			registry := NewPathEvidenceRegistry(".")
			registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(tt.result)})
			if registry.HasBodyEvidence("internal/runtime/agent/evidence_registry.go") {
				t.Fatal("non-registry answerability evidence must not become registry body evidence")
			}
			registry.mu.RLock()
			_, registered := registry.paths["internal/runtime/agent/evidence_registry.go"]
			registry.mu.RUnlock()
			if registered {
				t.Fatal("diagnostic/task/plan evidence must not register path-candidate state")
			}
		})
	}
}

func TestOutlineEvidenceClassificationDoesNotSatisfyBodyEvidence(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_registry.go"

	outlineResult := workspace.ToolResult{
		Tool:    workspace.ToolNameCodeNavigate,
		Summary: "Read outline in internal/runtime/agent/evidence_registry.go with capability: file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"fn TestRegisterToolResultsUsesEvidenceOwnedDTO",
			"struct PathEvidenceRegistry",
		}, "\n"),
	}
	brief := workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain how retry budget enforcement works.",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}

	raw := sourceEvidenceForPath([]workspace.ToolResult{outlineResult}, sourcePath)
	if raw.Level != planThenExecuteEvidencePathSeen {
		t.Fatalf("outline raw evidence level = %q, want %q; evidence=%#v", raw.Level, planThenExecuteEvidencePathSeen, raw)
	}
	if sourceEvidenceHasBody(raw) {
		t.Fatalf("outline must not count as source-body evidence: %#v", raw)
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(outlineResult)})
	if registry.HasBodyEvidence(sourcePath) {
		t.Fatal("outline-only evidence must not become registry body evidence")
	}

	outlineLedger := readOnlyAnswerabilityForResults(brief, []workspace.ToolResult{outlineResult})
	if !outlineLedger.HasKind(readOnlyEvidenceOutline) {
		t.Fatalf("outline ledger missing outline evidence item: %#v", outlineLedger.Items)
	}
	if outlineLedger.Answerable() {
		t.Fatalf("outline-only evidence must not make read-only answerability ready: %#v", outlineLedger)
	}
	if slices.Contains(outlineLedger.RelevantSourceBodyPaths, sourcePath) {
		t.Fatalf("outline-only evidence must not be relevant source-body readiness: %#v", outlineLedger.RelevantSourceBodyPaths)
	}

	symbolResult := workspace.ToolResult{
		Tool:    "go_read_symbol",
		Summary: "Read enforceRetryBudget in internal/runtime/agent/evidence_registry.go lines 40-52",
		Output: strings.Join([]string{
			"const retryBudget = 3",
			"func enforceRetryBudget(attempts int) bool {",
			"	return attempts < retryBudget",
			"}",
		}, "\n"),
	}
	results := []workspace.ToolResult{outlineResult, symbolResult}

	if !sourceEvidenceBodyReadForPath(sourcePath, results) {
		t.Fatal("later exact symbol body read should promote raw source-body readiness")
	}

	promotedRegistry := NewPathEvidenceRegistry(".")
	promotedRegistry.RegisterToolResults([]ToolResult{
		FromWorkspaceResult(outlineResult),
		FromWorkspaceResult(symbolResult),
	})
	if !promotedRegistry.HasBodyEvidence(sourcePath) {
		t.Fatal("later exact symbol body read should promote registry body evidence")
	}

	promotedLedger := readOnlyAnswerabilityForResults(brief, results)
	if !slices.Contains(promotedLedger.SourceBodyPaths, sourcePath) {
		t.Fatalf("promoted ledger source-body paths = %#v, want %q", promotedLedger.SourceBodyPaths, sourcePath)
	}
	if !slices.Contains(promotedLedger.RelevantSourceBodyPaths, sourcePath) {
		t.Fatalf("promoted ledger relevant source-body paths = %#v, want %q; ledger=%#v", promotedLedger.RelevantSourceBodyPaths, sourcePath, promotedLedger)
	}
	if !promotedLedger.Answerable() {
		t.Fatalf("later exact symbol body read should make read-only answerability ready: %#v", promotedLedger)
	}
}

func TestPathEvidencePendingSourceObligationParity(t *testing.T) {
	const (
		redirectPath = "internal/app/redirected.go"
		otherPath    = "internal/app/source.go"
	)

	redirectResult := workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Redirected internal/app/redirected.go to code_navigate",
		Output:  "Use code_navigate for source-body evidence in internal/app/redirected.go.",
	}
	directOutlineRedirectResult := workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Redirected internal/app/redirected.go to go_file_outline",
		Output: strings.Join([]string{
			"Use go_file_outline for source orientation in internal/app/redirected.go.",
			"related_capability: read_symbol via go_read_symbol",
		}, "\n"),
	}
	otherBodyResult := workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read internal/app/source.go lines 1-24 (total lines: 24)",
		Output:  "package app\nfunc SourceEvidence() {}\n",
	}
	samePathBodyResult := workspace.ToolResult{
		Tool:    workspace.ToolNameCodeNavigate,
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read RedirectedSource in internal/app/redirected.go lines 1-8",
			"1:func RedirectedSource() {}",
		}, "\n"),
	}
	samePathReadFileBodyResult := workspace.ToolResult{
		Tool:    workspace.ToolNameReadFile,
		Summary: "Read internal/app/redirected.go lines 1-12 (total lines: 12)",
		Output:  "package app\nfunc RedirectedSource() {}\n",
	}
	outlineResult := workspace.ToolResult{
		Tool:    workspace.ToolNameCodeNavigate,
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"language: go",
			"routed_tool: go_file_outline",
			"route_summary: Outlined 1 top-level declaration(s) in internal/app/redirected.go.",
			"- RedirectedSource lines 1-8",
		}, "\n"),
	}
	directOutlineResult := workspace.ToolResult{
		Tool:    "go_file_outline",
		Summary: "Outlined 1 top-level declaration(s) in internal/app/redirected.go.",
		Output:  "- RedirectedSource lines 1-8\n",
	}
	failedNavigateResult := workspace.ToolResult{
		Tool:    workspace.ToolNameCodeNavigate,
		Summary: "zero-hit source-body navigation for internal/app/redirected.go",
		Output:  "internal/app/redirected.go needs route",
		Error:   "no symbol matched",
	}
	baseBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "fix unrelated cache behavior",
	}
	relevantBrief := baseBrief
	relevantBrief.TargetRefs = []string{redirectPath}
	relevantBrief.EvidencePaths = []string{otherPath}

	pendingOnly := sourceEvidencePendingRedirectsForBrief(baseBrief, []workspace.ToolResult{redirectResult})
	if len(pendingOnly) != 1 || pendingOnly[0].Path != redirectPath || pendingOnly[0].PendingTool != workspace.ToolNameCodeNavigate {
		t.Fatalf("redirect-only pending obligations = %#v, want %q via code_navigate", pendingOnly, redirectPath)
	}

	registry := NewPathEvidenceRegistry(".")
	registry.RegisterToolResults([]ToolResult{FromWorkspaceResult(redirectResult)})
	if registry.HasBodyEvidence(redirectPath) {
		t.Fatal("redirect-only read_file must not become registry body evidence")
	}

	directOutlinePending := sourceEvidencePendingRedirectsForBrief(relevantBrief, []workspace.ToolResult{
		directOutlineRedirectResult,
		otherBodyResult,
	})
	if len(directOutlinePending) != 1 || directOutlinePending[0].PendingTool != "go_read_symbol" {
		t.Fatalf("direct file_outline redirect should keep related read_symbol obligation: %#v", directOutlinePending)
	}
	directOutlineRequest := workspace.ToolRequest{
		Tool:            "go_file_outline",
		Path:            redirectPath,
		NamedGap:        "declarations needed before selecting source-body evidence",
		WhyNotAnswering: "need symbol names for the redirected source-body follow-up",
	}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(relevantBrief, directOutlineRequest, []workspace.ToolResult{
		directOutlineRedirectResult,
		otherBodyResult,
	}) {
		t.Fatal("direct file_outline redirect should expose the outline tool only as progress")
	}
	withDirectOutline := []workspace.ToolResult{directOutlineRedirectResult, otherBodyResult, directOutlineResult}
	if pending := sourceEvidencePendingRedirectsForBrief(relevantBrief, withDirectOutline); len(pending) != 1 || pending[0].PendingTool != "go_read_symbol" {
		t.Fatalf("direct file_outline progress must not close related read_symbol obligation: %#v", pending)
	}
	if !sourceEvidencePendingRedirectFollowupAllowedForBrief(relevantBrief, workspace.ToolRequest{
		Tool:    "go_read_symbol",
		Path:    redirectPath,
		Pattern: "RedirectedSource",
	}, withDirectOutline) {
		t.Fatal("related read_symbol follow-up should remain allowed after direct file_outline progress")
	}

	withSamePathBody := []workspace.ToolResult{redirectResult, samePathBodyResult}
	if pending := sourceEvidencePendingRedirectsForBrief(baseBrief, withSamePathBody); len(pending) != 0 {
		t.Fatalf("same-path source-body read should clear pending redirect: %#v", pending)
	}
	promotedRegistry := NewPathEvidenceRegistry(".")
	promotedRegistry.RegisterToolResults([]ToolResult{
		FromWorkspaceResult(redirectResult),
		FromWorkspaceResult(samePathBodyResult),
	})
	if !promotedRegistry.HasBodyEvidence(redirectPath) {
		t.Fatal("same-path source-body read should promote registry body evidence")
	}
	if pending := sourceEvidencePendingRedirectsForBrief(baseBrief, []workspace.ToolResult{
		redirectResult,
		samePathReadFileBodyResult,
	}); len(pending) != 0 {
		t.Fatalf("same-path read_file source body should also clear pending redirect: %#v", pending)
	}

	withOtherBody := []workspace.ToolResult{redirectResult, otherBodyResult}
	if pending := sourceEvidencePendingRedirects(withOtherBody); len(pending) != 1 {
		t.Fatalf("raw pending redirects should retain stale redirect until brief filtering: %#v", pending)
	}
	if pending := sourceEvidencePendingRedirectsForBrief(baseBrief, withOtherBody); len(pending) != 0 {
		t.Fatalf("irrelevant stale redirect should drop after plausible source-body evidence: %#v", pending)
	}
	if pending := sourceEvidencePendingRedirectsForBrief(relevantBrief, withOtherBody); len(pending) != 1 || pending[0].Path != redirectPath {
		t.Fatalf("relevant redirect should remain pending after other source-body evidence: %#v", pending)
	}

	bodyFollowup := workspace.ToolRequest{
		Tool:       workspace.ToolNameCodeNavigate,
		Path:       redirectPath,
		Capability: "read_symbol",
		Pattern:    "RedirectedSource",
	}
	if !sourceEvidencePendingRedirectFollowupAllowedForBrief(relevantBrief, bodyFollowup, withOtherBody) {
		t.Fatal("relevant pending redirect should allow exact source-body follow-up")
	}
	if sourceEvidencePendingRedirectFollowupAllowedForBrief(relevantBrief, workspace.ToolRequest{
		Tool:       workspace.ToolNameCodeNavigate,
		Path:       otherPath,
		Capability: "read_symbol",
		Pattern:    "SourceEvidence",
	}, withOtherBody) {
		t.Fatal("pending redirect follow-up must stay scoped to the redirected path")
	}

	twoFailures := append(append([]workspace.ToolResult{}, withOtherBody...), failedNavigateResult, failedNavigateResult)
	if attempts := sourceEvidenceObligationResolutionAttempts(redirectPath, twoFailures); attempts != 2 {
		t.Fatalf("failed navigation attempts = %d, want 2", attempts)
	}
	if pending := sourceEvidencePendingRedirectsForBrief(relevantBrief, twoFailures); len(pending) != 1 {
		t.Fatalf("redirect should remain pending before failed-attempt limit: %#v", pending)
	}
	threeFailures := append(append([]workspace.ToolResult{}, twoFailures...), failedNavigateResult)
	if attempts := sourceEvidenceObligationResolutionAttempts(redirectPath, threeFailures); attempts != maxObligationResolutionAttempts {
		t.Fatalf("failed navigation attempts = %d, want %d", attempts, maxObligationResolutionAttempts)
	}
	if pending := sourceEvidencePendingRedirectsForBrief(relevantBrief, threeFailures); len(pending) != 0 {
		t.Fatalf("redirect should exhaust after failed-attempt limit: %#v", pending)
	}

	outlineRequest := workspace.ToolRequest{
		Tool:            workspace.ToolNameCodeNavigate,
		Path:            redirectPath,
		Capability:      "file_outline",
		NamedGap:        "declarations needed before selecting source-body evidence",
		WhyNotAnswering: "need symbol names for the redirected source-body follow-up",
	}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(relevantBrief, outlineRequest, withOtherBody) {
		t.Fatal("one same-path file_outline should be allowed as pending redirect progress")
	}

	withOutline := append(append([]workspace.ToolResult{}, withOtherBody...), outlineResult)
	if !sourceEvidenceOutlineReadForPath(redirectPath, withOutline) {
		t.Fatal("test setup should record same-path outline progress")
	}
	if pending := sourceEvidencePendingRedirectsForBrief(relevantBrief, withOutline); len(pending) != 1 || pending[0].Path != redirectPath {
		t.Fatalf("file_outline progress must not close source-body obligation: %#v", pending)
	}
	if sourceEvidencePendingRedirectProgressAllowedForBrief(relevantBrief, outlineRequest, withOutline) {
		t.Fatal("same-path file_outline progress should be single-use")
	}
	if !sourceEvidencePendingRedirectFollowupAllowedForBrief(relevantBrief, bodyFollowup, withOutline) {
		t.Fatal("source-body follow-up should remain allowed after file_outline progress")
	}

	outlineRegistry := NewPathEvidenceRegistry(".")
	outlineRegistry.RegisterToolResults([]ToolResult{
		FromWorkspaceResult(redirectResult),
		FromWorkspaceResult(otherBodyResult),
		FromWorkspaceResult(outlineResult),
	})
	if outlineRegistry.HasBodyEvidence(redirectPath) {
		t.Fatal("registry must not treat file_outline progress as body evidence")
	}
}

func TestSourceEvidencePendingRedirectRequestAllowedAcceptsSourceBodyShapes(t *testing.T) {
	tests := []struct {
		name       string
		obligation sourceEvidenceObligation
		request    workspace.ToolRequest
		want       bool
	}{
		{
			name:       "code navigate read symbol with pattern",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "read_symbol", Pattern: "Apply"},
			want:       true,
		},
		{
			name:       "code navigate inspect symbol with symbol",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "inspect_symbol", Symbol: "Apply"},
			want:       true,
		},
		{
			name:       "code navigate symbol body with query",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "symbol_body", Query: "Apply"},
			want:       true,
		},
		{
			name:       "read only capability source body",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameReadOnlyCapability},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameReadOnlyCapability, Path: "a.go", Capability: "read_symbol", Pattern: "Apply"},
			want:       true,
		},
		{
			name:       "direct read symbol",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: "go_read_symbol"},
			request:    workspace.ToolRequest{Tool: "go_read_symbol", Path: "a.go", Symbol: "Apply"},
			want:       true,
		},
		{
			name:       "direct extract body",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: "go_extract_body"},
			request:    workspace.ToolRequest{Tool: "go_extract_body", Path: "a.go", Query: "Apply"},
			want:       true,
		},
		{
			name:       "missing symbol pattern rejected",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "read_symbol"},
		},
		{
			name:       "direct symbol tool missing pattern rejected",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: "go_read_symbol"},
			request:    workspace.ToolRequest{Tool: "go_read_symbol", Path: "a.go"},
		},
		{
			name:       "outline is progress but not source body",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "file_outline", Pattern: "Apply"},
		},
		{
			name:       "references rejected",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Path: "a.go", Capability: "references", Pattern: "Apply"},
		},
		{
			name:       "wrong tool rejected",
			obligation: sourceEvidenceObligation{Path: "a.go", PendingTool: workspace.ToolNameCodeNavigate},
			request:    workspace.ToolRequest{Tool: "go_read_symbol", Path: "a.go", Pattern: "Apply"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceEvidencePendingRedirectRequestAllowed(tt.obligation, tt.request); got != tt.want {
				t.Fatalf("request allowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPathEvidenceMutationHistoryParityBoundaries(t *testing.T) {
	const (
		historyCompletePath      = "a.go"
		historyNotApplicablePath = "b.go"
		historyPendingPath       = "c.go"
		changedOnlyPath          = "d.go"
	)

	readResult := func(path string) workspace.ToolResult {
		return workspace.ToolResult{
			Tool:    workspace.ToolNameReadFile,
			Summary: "Read " + path + " lines 1-10",
			Output:  "package agent\n",
		}
	}
	historyRangeResult := workspace.ToolResult{
		Tool:    workspace.ToolNameHistoryRange,
		Summary: "Read git history for " + historyCompletePath + " lines 1-10",
		Output:  "commit abc123\n\n    explain current behavior\n",
	}
	historyNotApplicableResult := workspace.ToolResult{
		Tool:    workspace.ToolNameHistoryRange,
		Code:    workspace.HistoryNotApplicableCode,
		Summary: "Git history not applicable for " + historyNotApplicablePath,
		Output: `{
			"code":"history_not_applicable",
			"status":"not_applicable",
			"path":"b.go"
		}`,
	}
	editResult := workspace.ToolResult{
		Tool:    workspace.ToolNameEditFile,
		Summary: "Edited " + historyPendingPath,
	}
	patchResult := workspace.ToolResult{
		Tool:   workspace.ToolNameApplyPatch,
		Output: "update " + changedOnlyPath + "\n",
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			historyCompletePath,
			historyNotApplicablePath,
			historyPendingPath,
			changedOnlyPath,
		},
		RequiredEvidence: []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		readResult(historyCompletePath),
		historyRangeResult,
		readResult(historyNotApplicablePath),
		historyNotApplicableResult,
		readResult(historyPendingPath),
		editResult,
		patchResult,
	}

	changed := changedPathsFromToolResults(results)
	for _, want := range []string{historyPendingPath, changedOnlyPath} {
		if _, ok := changed[want]; !ok {
			t.Fatalf("changed paths = %#v, want %q", mapKeys(changed), want)
		}
	}
	if _, ok := changed[historyCompletePath]; ok {
		t.Fatalf("history-only path must not become a changed path: %#v", mapKeys(changed))
	}
	if got, want := changedPathListFromToolResults(results), []string{historyPendingPath, changedOnlyPath}; !slices.Equal(got, want) {
		t.Fatalf("changed path list = %#v, want %#v", got, want)
	}

	completed := modelProtocolCompletedGitHistoryPaths(brief, results)
	if got, want := completed, []string{historyCompletePath, historyNotApplicablePath}; !slices.Equal(got, want) {
		t.Fatalf("completed history paths = %#v, want %#v", got, want)
	}
	pending := modelProtocolPendingGitHistoryPaths(brief, results)
	if got, want := pending, []string{historyPendingPath}; !slices.Equal(got, want) {
		t.Fatalf("pending history paths = %#v, want %#v", got, want)
	}

	registry := NewPathEvidenceRegistry(".")
	var registryResults []ToolResult
	for _, result := range results {
		registryResults = append(registryResults, FromWorkspaceResult(result))
	}
	registry.RegisterToolResults(registryResults)

	for _, wantBody := range []string{historyCompletePath, historyNotApplicablePath, historyPendingPath} {
		if !registry.HasBodyEvidence(wantBody) {
			t.Fatalf("registry body evidence missing for source-read path %q", wantBody)
		}
	}
	if registry.HasBodyEvidence(changedOnlyPath) {
		t.Fatalf("changed-only path %q must not become registry body evidence", changedOnlyPath)
	}
	if successfulGitHistoryResultForPath(historyPendingPath, results) {
		t.Fatalf("body-read and changed path %q must still be pending git-history evidence", historyPendingPath)
	}
	if !successfulGitHistoryResultForPath(historyCompletePath, results) ||
		!successfulGitHistoryResultForPath(historyNotApplicablePath, results) {

		t.Fatalf("history result completion not recognized; completed=%#v", completed)
	}

	mutationOnlyRegistry := NewPathEvidenceRegistry(".")
	mutationOnlyRegistry.RegisterToolResults([]ToolResult{
		FromWorkspaceResult(editResult),
		FromWorkspaceResult(patchResult),
	})
	for _, changedOnly := range []string{historyPendingPath, changedOnlyPath} {
		mutationOnlyRegistry.mu.RLock()
		_, registered := mutationOnlyRegistry.paths[changedOnly]
		mutationOnlyRegistry.mu.RUnlock()
		if registered {
			t.Fatalf("registry unexpectedly registered mutation-only path %q; changed-path evidence is not modeled here", changedOnly)
		}
	}
}

func TestPathEvidenceChangedPathsParity(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, Summary: "Edited internal/app/edit.go"},
		{Tool: workspace.ToolNameWriteFile, Summary: "Wrote internal/app/write.go"},
		{Tool: workspace.ToolNameAppendFile, Summary: "Appended internal/app/append.go"},
		{Tool: workspace.ToolNameMkdir, Summary: "Created directory internal/app/generated"},
		{
			Tool:   workspace.ToolNameApplyPatch,
			Output: "add internal/app/add.go\nupdate internal/app/update.go\ndelete internal/app/delete.go\nupdate+move internal/app/old.go -> internal/app/new.go\n",
		},
		{Tool: workspace.ToolNameGoMoveDeclarations, Summary: "Moved declarations from internal/app/source.go to internal/app/destination.go"},
		{Tool: workspace.ToolNameDelegateEdit, Summary: "Edited internal/app/delegated.go"},
		{Tool: workspace.ToolNameDelegateEdit, Output: "update internal/app/delegated_patch.go\n"},
		{Tool: workspace.ToolNameEditFile, Summary: "Edited internal/app/failed.go", Error: "permission denied"},
		{Tool: workspace.ToolNameReadFile, Summary: "Read internal/app/read.go lines 1-10"},
		{Tool: workspace.ToolNameSearchContent, Summary: "1 matching line(s) in internal/app/search.go"},
	}
	wantChanged := []string{
		"internal/app/add.go",
		"internal/app/append.go",
		"internal/app/delegated.go",
		"internal/app/delegated_patch.go",
		"internal/app/delete.go",
		"internal/app/destination.go",
		"internal/app/edit.go",
		"internal/app/generated",
		"internal/app/new.go",
		"internal/app/old.go",
		"internal/app/source.go",
		"internal/app/update.go",
		"internal/app/write.go",
	}
	wantChangedList := []string{
		"internal/app/edit.go",
		"internal/app/write.go",
		"internal/app/append.go",
		"internal/app/generated",
		"internal/app/add.go",
		"internal/app/update.go",
		"internal/app/delete.go",
		"internal/app/old.go",
		"internal/app/new.go",
		"internal/app/source.go",
		"internal/app/destination.go",
		"internal/app/delegated.go",
		"internal/app/delegated_patch.go",
	}

	if got := mapKeys(changedPathsFromToolResults(results)); !slices.Equal(got, wantChanged) {
		t.Fatalf("changed paths = %#v, want %#v", got, wantChanged)
	}
	if got := changedPathListFromToolResults(results); !slices.Equal(got, wantChangedList) {
		t.Fatalf("changed path list = %#v, want %#v", got, wantChangedList)
	}

	registry := NewPathEvidenceRegistry(".")
	var registryResults []ToolResult
	for _, result := range results {
		registryResults = append(registryResults, FromWorkspaceResult(result))
	}
	registry.RegisterToolResults(registryResults)
	for _, changed := range wantChanged {
		registry.mu.RLock()
		_, registered := registry.paths[changed]
		registry.mu.RUnlock()
		if registered {
			t.Fatalf("registry unexpectedly registered changed path %q; mutation evidence is not body/candidate evidence", changed)
		}
	}

	store := newStateStore(nil, false, 0)
	store.RecordResult(
		workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch, Content: "*** Begin Patch\n*** Update File: internal/app/request.go\n@@\n-old\n+new\n*** End Patch"},
		workspace.ToolResult{Tool: workspace.ToolNameApplyPatch, Summary: "Patch applied"},
	)
	store.RecordResult(
		workspace.ToolRequest{Tool: workspace.ToolNameEditFile, Path: "internal/app/request-edit.go"},
		workspace.ToolResult{Tool: workspace.ToolNameEditFile, Summary: "Edited a different display path"},
	)
	store.RecordResult(
		workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "internal/app/rejected.go"},
		workspace.ToolResult{Tool: workspace.ToolNameWriteFile, Summary: "Wrote internal/app/rejected.go", Error: "denied"},
	)
	wantStore := []string{"internal/app/request-edit.go", "internal/app/request.go"}
	if got := store.ChangedPaths(); !slices.Equal(got, wantStore) {
		t.Fatalf("state store changed paths = %#v, want %#v", got, wantStore)
	}
}
