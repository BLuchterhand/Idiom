package agent

import (
	"path/filepath"
	"slices"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
)

func TestToolResultEvidenceProjectionSourceBodyBoundaries(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_projection.go"
	const generatedPath = ".idiom/comprehension/views/context.json"

	tests := []struct {
		name      string
		path      string
		result    workspace.ToolResult
		wantLevel PlanThenExecuteSourceEvidenceLevel
		wantBody  bool
	}{
		{
			name: "read_file bounded source range",
			path: sourcePath,
			result: readFileSourceBodyResult(
				sourcePath,
				20,
				40,
				220,
				"func TestToolResultEvidenceProjectionSourceBodyBoundaries(t *testing.T) {}\n",
			),
			wantLevel: planThenExecuteEvidenceAnchorReady,
			wantBody:  true,
		},
		{
			name: "failed read_file",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read internal/runtime/agent/evidence_projection.go lines 20-40 (total lines: 220)",
				Error:   "permission denied",
			},
			wantLevel: planThenExecuteEvidenceNone,
			wantBody:  false,
		},
		{
			name: "read_file path without source body facts is not body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read internal/runtime/agent/evidence_projection.go lines 20-40 (total lines: 220)",
				Output:  "package agent\n",
				Paths:   []string{sourcePath},
			},
			wantLevel: planThenExecuteEvidencePathSeen,
			wantBody:  false,
		},
		{
			name: "redirect-only read_file remains pending",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Redirected internal/runtime/agent/evidence_projection.go to code_navigate",
				Output:  "Use code_navigate for source-body evidence in internal/runtime/agent/evidence_projection.go.",
			},
			wantLevel: planThenExecuteEvidenceSourceBodyPending,
			wantBody:  false,
		},
		{
			name: "generated artifacts are not source body evidence",
			path: generatedPath,
			result: readFileSourceBodyResult(
				generatedPath,
				1,
				20,
				20,
				`{"candidate_files":[{"path":"internal/runtime/agent/evidence_projection.go"}]}`,
			),
			wantLevel: planThenExecuteEvidenceNone,
			wantBody:  false,
		},
		{
			name: "routed code_navigate source body",
			path: sourcePath,
			result: codeNavigateSourceBodyResult(
				sourcePath,
				45,
				70,
				220,
				"route_summary: Read internal/runtime/agent/evidence_projection.go lines 45-70 (total lines: 220)\n"+
					"func TestCandidatePathExistenceResolvesLazily(t *testing.T) {}\n",
			),
			wantLevel: planThenExecuteEvidenceAnchorReady,
			wantBody:  true,
		},
		{
			name: "symbol read source body",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    "go_read_symbol",
				Summary: "Read TestToolResultEvidenceProjectionSourceBodyBoundaries in internal/runtime/agent/evidence_projection.go lines 12-24",
				Output:  "func TestToolResultEvidenceProjectionSourceBodyBoundaries(t *testing.T) {}\n",
				Paths:   []string{sourcePath},
				Facts:   sourceBodyFactsForTest(sourcePath, 12, 24, 0),
			},
			wantLevel: planThenExecuteEvidenceAnchorReady,
			wantBody:  true,
		},
		{
			name: "context zoom include source body",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameContextZoom,
				Summary: "Read internal/runtime/agent/evidence_projection.go lines 1-20 with include_source",
				Output:  `{"path":"internal/runtime/agent/evidence_projection.go","source":"func TestToolResultEvidenceProjectionSourceBodyBoundaries(t *testing.T) {}"}`,
				Paths:   []string{sourcePath},
				Facts:   sourceBodyFactsForTest(sourcePath, 1, 20, 20),
			},
			wantLevel: planThenExecuteEvidenceAnchorReady,
			wantBody:  true,
		},
		{
			name: "candidate anchor only is not body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Read internal/runtime/agent/evidence_projection.go lines 1-20",
				Output:  "path-filter miss\ncandidate_anchor_status: anchors only",
			},
			wantLevel: planThenExecuteEvidencePathSeen,
			wantBody:  false,
		},
		{
			name: "file outline is not body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Read outline in internal/runtime/agent/evidence_projection.go with capability: file_outline",
				Output:  "capability: file_outline\nfn TestToolResultEvidenceProjectionSourceBodyBoundaries\nstruct toolResultEvidenceProjection\n",
			},
			wantLevel: planThenExecuteEvidencePathSeen,
			wantBody:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := newToolResultEvidenceProjection([]workspace.ToolResult{tt.result}).SourceEvidenceForPath(tt.path)
			if evidence.Level != tt.wantLevel {
				t.Fatalf("evidence level = %q, want %q; evidence=%#v", evidence.Level, tt.wantLevel, evidence)
			}
			if got := sourceEvidenceHasBody(evidence); got != tt.wantBody {
				t.Fatalf("body evidence = %v, want %v; evidence=%#v", got, tt.wantBody, evidence)
			}
		})
	}
}

func TestToolResultEvidenceProjectionCandidateBoundaries(t *testing.T) {
	const sourcePath = "internal/runtime/agent/evidence_projection.go"

	contextResult := workspace.ToolResult{
		Tool:    workspace.ToolNameContextPack,
		Summary: "Context pack returned candidate paths.",
		Facts:   contextPackCandidateFactsForTest(sourcePath),
	}
	if got := newToolResultEvidenceProjection([]workspace.ToolResult{contextResult}).CandidatePaths(); !slices.Contains(got, sourcePath) {
		t.Fatalf("context_pack candidate paths = %#v, want %q", got, sourcePath)
	}

	bodyResult := readFileSourceBodyResult(sourcePath, 1, 20, 0, "package agent\n")
	if got := newToolResultEvidenceProjection([]workspace.ToolResult{bodyResult}).CandidatePaths(); slices.Contains(got, sourcePath) {
		t.Fatalf("source body path must not also be an unread candidate: %#v", got)
	}

	ledger := readOnlyAnswerabilityForResults(directAnswerReadOnlyBrief(), []workspace.ToolResult{contextResult})
	if !slices.Contains(ledger.CandidatePaths, sourcePath) || !ledger.HasUnreadCandidatePaths {
		t.Fatalf("context_pack candidate should remain unread in answerability ledger: %#v", ledger)
	}

	factResult := workspace.ToolResult{
		Tool:    workspace.ToolNameContextQuery,
		Summary: "Context query mentioned internal/runtime/agent/wrong.go",
		Output:  `{"candidates":[{"path":"internal/runtime/agent/wrong.go"}]}`,
		Facts: &toolfacts.ToolResultFacts{
			CandidatePaths:      []string{sourcePath},
			HasDiscoveryTargets: true,
			DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
			SourceOwnerFound:    true,
		},
	}
	if got := newToolResultEvidenceProjection([]workspace.ToolResult{factResult}).CandidatePaths(); !slices.Equal(got, []string{sourcePath}) {
		t.Fatalf("candidate paths should come from facts, got %#v", got)
	}
	outcome := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: workspace.ToolNameContextQuery}, factResult, 4)
	if outcome.Strength != toolfacts.DiscoveryStrengthSourceOwner || !slices.Equal(outcome.Candidates, []string{sourcePath}) {
		t.Fatalf("discovery outcome should come from facts, got %#v", outcome)
	}

	searchResult := workspace.ToolResult{
		Tool:    workspace.ToolNameSearchContent,
		Summary: "Found 1 matching line.",
		Output:  "internal/runtime/agent/wrong.go:1: wrong",
		Facts: &toolfacts.ToolResultFacts{
			CandidatePaths:        []string{sourcePath},
			HasDiscoveryTargets:   true,
			DiscoveryStrength:     toolfacts.DiscoveryStrengthCandidateOnly,
			SearchHitCount:        75,
			SearchHighCardinality: true,
		},
	}
	outcome = discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: workspace.ToolNameSearchContent}, searchResult, 4)
	if outcome.Strength != toolfacts.DiscoveryStrengthCandidateOnly ||
		outcome.SearchHitCount != 75 ||
		!outcome.HighCardinalitySearch ||
		!slices.Equal(outcome.Candidates, []string{sourcePath}) {
		t.Fatalf("text-search discovery outcome should come from facts, got %#v", outcome)
	}
}

func TestToolResultEvidenceProjectionCloseoutUsesProjectedResultSnapshot(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Can you tell me how this harness uses subagents?")
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		Question:        brief.Objective,
		SourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{{
			Path: "internal/runtime/agent/runner_core.go",
			Text: "The harness uses the `task` tool as the subagent entrypoint for read-only exploration.",
		}},
	})}

	projection := newToolResultEvidenceProjection(results)
	results[0] = workspace.ToolResult{Tool: workspace.ToolNameSearchFiles}

	closeout := readOnlyClosureDecisionForProjection(brief, projection)
	if !closeout.Answerable() {
		t.Fatalf("projected delegated evidence should remain answerable: %#v", closeout)
	}
	if len(closeout.Results) != 1 || closeout.Results[0].Tool != workspace.ToolNameTask {
		t.Fatalf("closeout must use the projection-owned result snapshot, got %#v", closeout.Results)
	}
}

func TestFactlessDiscoveryQueryOutputDoesNotCreateCandidateAuthority(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    workspace.ToolNameDiscoveryQuery,
		Summary: "Ran discovery query",
		Output:  `{"evidence":[{"name":"text_anchor_search","output_excerpt":"internal/runtime/agent/runner_core.go:10: runChildTask"}]}`,
	}
	results := []workspace.ToolResult{result}

	if got := newToolResultEvidenceProjection(results).CandidatePaths(); len(got) != 0 {
		t.Fatalf("factless discovery_query output should not create candidate paths, got %#v", got)
	}
	if got := discoveryQueryCandidatePaths(results, 4); len(got) != 0 {
		t.Fatalf("factless discovery_query output should not feed discovery candidate paths, got %#v", got)
	}
	outcome := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: workspace.ToolNameDiscoveryQuery}, result, 4)
	if outcome.Strength != "" || len(outcome.Candidates) != 0 {
		t.Fatalf("factless discovery_query should not create discovery outcome, got %#v", outcome)
	}
}

func TestToolResultEvidenceProjectionChangedPaths(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"internal/app/edit.go"}},
		{Tool: workspace.ToolNameWriteFile, ChangedPaths: []string{"internal/app/write.go"}},
		{Tool: workspace.ToolNameAppendFile, ChangedPaths: []string{"internal/app/append.go"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/add.go", "internal/app/update.go"}},
		{Tool: workspace.ToolNameGoMoveDeclarations, ChangedPaths: []string{"internal/app/source.go", "internal/app/destination.go"}},
		{Tool: workspace.ToolNameDelegateEdit, ChangedPaths: []string{"internal/app/delegated.go"}},
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"internal/app/failed.go"}, Error: "permission denied"},
		{Tool: workspace.ToolNameReadFile, Summary: "Read internal/app/read.go lines 1-10"},
	}
	wantChangedSet := []string{
		"internal/app/add.go",
		"internal/app/append.go",
		"internal/app/delegated.go",
		"internal/app/destination.go",
		"internal/app/edit.go",
		"internal/app/source.go",
		"internal/app/update.go",
		"internal/app/write.go",
	}
	wantChangedList := []string{
		"internal/app/edit.go",
		"internal/app/write.go",
		"internal/app/append.go",
		"internal/app/add.go",
		"internal/app/update.go",
		"internal/app/source.go",
		"internal/app/destination.go",
		"internal/app/delegated.go",
	}

	projection := newToolResultEvidenceProjection(results)
	if got := mapKeys(projection.ChangedPathSet()); !slices.Equal(got, wantChangedSet) {
		t.Fatalf("changed paths = %#v, want %#v", got, wantChangedSet)
	}
	if got := projection.ChangedPathList(); !slices.Equal(got, wantChangedList) {
		t.Fatalf("changed path list = %#v, want %#v", got, wantChangedList)
	}
}

func TestToolResultEvidenceProjectionChangedSourcePathsUseSourceSanityContract(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"README.md"}},
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"scripts/panel.gd"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/edit.go", "docs/notes.txt"}},
		{Tool: workspace.ToolNameWriteFile, ChangedPaths: []string{"internal/app/failed.go"}, Error: "permission denied"},
	}

	projection := newToolResultEvidenceProjection(results)
	want := []string{"internal/app/edit.go", "scripts/panel.gd"}

	if got := projection.ChangedSourcePathList(); !slices.Equal(got, want) {
		t.Fatalf("changed source paths = %#v, want %#v", got, want)
	}
	if !projection.HasChangedSourcePath("scripts/panel.gd") {
		t.Fatal("changed source path should be found")
	}
	if projection.HasChangedSourcePath("README.md") {
		t.Fatal("non-source changed path should not be source-sanity-backed")
	}
	if target, ok := projection.SingleChangedSourcePath(); ok {
		t.Fatalf("multiple source paths should not choose a single target, got %q", target)
	}

	single := newToolResultEvidenceProjection([]workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"scripts/panel.gd", "README.md"}},
	})
	if target, ok := single.SingleChangedSourcePath(); !ok || target != "scripts/panel.gd" {
		t.Fatalf("single changed source target = %q, %v", target, ok)
	}
}

func TestToolResultEvidenceProjectionReadOnlyCategoryBoundaries(t *testing.T) {
	sourcePath := "internal/runtime/agent/evidence_projection.go"
	externalPath := filepath.Join(t.TempDir(), "Mobile Documents", "Project Notes", "brief.md")
	diagnosticPath := "internal/runtime/agent/runner_core.go"
	taskPath := "internal/runtime/agent/evidence_readonly.go"
	generatedPath := ".idiom/comprehension/views/context.json"
	planPath := "internal/runtime/agent/plan_artifact.go"
	outlinePath := "internal/runtime/agent/outline.go"
	contextPath := "internal/runtime/agent/context_packet.go"
	searchPath := "internal/runtime/agent/search_hit.go"
	discoveryPath := "internal/runtime/agent/discovery_candidate.go"
	results := []workspace.ToolResult{
		{
			Tool:    workspace.ToolNameReadFile,
			Summary: "Read " + externalPath + " lines 1-20",
			Output:  "1:external planning notes\n",
			Paths:   []string{externalPath},
			Facts:   sourceBodyFactsForTest(externalPath, 1, 20, 0),
		},
		{
			Tool:    workspace.ToolNameSourceSanity,
			Summary: "source_sanity failed for " + diagnosticPath,
			Output:  diagnosticPath + ":10: undefined: Runner\n",
			Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{diagnosticPath}},
		},
		typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
			Objective:       "Explain read-only packet evidence.",
			Question:        "Explain read-only packet evidence.",
			SourceBodyPaths: []string{taskPath},
			Findings: []ReadOnlyEvidenceFinding{{
				Path: taskPath,
				Text: "Read-only evidence packets carry delegated source-body paths.",
			}},
		}),
		readFileSourceBodyResult(sourcePath, 1, 20, 0, "1:package agent\n"),
		readFileSourceBodyResult(generatedPath, 1, 20, 20, `{"candidate_files":[{"path":"`+sourcePath+`"}]}`),
		{
			Tool:        featurePlannerPlanArtifactPayloadType,
			PayloadType: featurePlannerPlanArtifactPayloadType,
			Summary:     "Feature planner plan artifact proposed files.",
			Payload:     []byte(`{"proposed_paths":["` + planPath + `"]}`),
		},
		{
			Tool:    workspace.ToolNameCodeNavigate,
			Summary: "Read outline in " + outlinePath + " with capability: file_outline",
			Output:  "capability: file_outline\nfn ObserveReadOnlyEvidence\n",
			Paths:   []string{outlinePath},
		},
		{
			Tool:    workspace.ToolNameContextPack,
			Summary: "Context pack returned candidate paths.",
			Facts:   contextPackCandidateFactsForTest(contextPath),
		},
		{
			Tool:    workspace.ToolNameSearchContent,
			Summary: "Found 75 matching lines",
			Output:  searchPath + ":12: read-only observation\n",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:        []string{searchPath},
				HasDiscoveryTargets:   true,
				DiscoveryStrength:     toolfacts.DiscoveryStrengthCandidateOnly,
				SearchHitCount:        75,
				SearchHighCardinality: true,
			},
		},
		{
			Tool:    workspace.ToolNameDiscoveryQuery,
			Summary: "Discovery query produced a candidate.",
			Output:  discoveryPath + ": semantic discovery candidate\n",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths: []string{discoveryPath},
				RankedDiscoveryCandidates: []toolfacts.DiscoveryCandidateEvidence{{
					Path:         discoveryPath,
					LayerName:    "text_anchor_search",
					Tool:         workspace.ToolNameSearchContent,
					Summary:      "Discovery query produced a candidate.",
					EvidenceLine: discoveryPath + ": semantic discovery candidate",
				}},
				HasDiscoveryTargets: true,
				DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
				SourceOwnerFound:    true,
			},
		},
		{
			Tool:  workspace.ToolNameReadFile,
			Error: "permission denied",
			Facts: sourceBodyFactsForTest("internal/runtime/agent/failed.go", 1, 10, 10),
		},
	}

	projection := newToolResultEvidenceProjection(results)
	observations := projection.ReadOnlyEvidenceObservations()
	requireObservation := func(name string, match func(readOnlyEvidenceObservation) bool) {
		t.Helper()
		if !slices.ContainsFunc(observations, match) {
			t.Fatalf("read-only evidence observations missing %s: %#v", name, observations)
		}
	}
	requireObservation("source body "+sourcePath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceSourceBody && observation.Path == sourcePath
	})
	requireObservation("external document "+externalPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceExternalDocument && observation.Path == externalPath
	})
	requireObservation("diagnostic "+diagnosticPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceDiagnostic && slices.Contains(observation.CandidatePaths, diagnosticPath)
	})
	requireObservation("generated artifact", func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceGeneratedArtifact && observation.Tool == workspace.ToolNameReadFile
	})
	requireObservation("plan artifact "+planPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidencePlanArtifact && slices.Contains(observation.CandidatePaths, planPath)
	})
	requireObservation("outline "+outlinePath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceOutline && slices.Contains(observation.CandidatePaths, outlinePath)
	})
	requireObservation("context packet "+contextPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceContextPacket && slices.Contains(observation.CandidatePaths, contextPath)
	})
	requireObservation("search hit "+searchPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceSearchHit &&
			observation.Tool == workspace.ToolNameSearchContent &&
			observation.HitCount == 75 &&
			observation.HighCardinality &&
			slices.Contains(observation.CandidatePaths, searchPath)
	})
	requireObservation("discovery candidate "+discoveryPath, func(observation readOnlyEvidenceObservation) bool {
		return observation.Kind == readOnlyEvidenceSearchHit &&
			observation.Tool == workspace.ToolNameDiscoveryQuery &&
			slices.Contains(observation.CandidatePaths, discoveryPath)
	})
	if slices.ContainsFunc(observations, func(observation readOnlyEvidenceObservation) bool {
		return observation.Path == "internal/runtime/agent/failed.go"
	}) {
		t.Fatalf("failed results must not become read-only evidence observations: %#v", observations)
	}
	if slices.Contains(projection.SourceBodyPaths(), externalPath) {
		t.Fatalf("external document leaked into workspace source-body paths: %#v", projection.SourceBodyPaths())
	}
	if got := projection.ExternalDocumentBodyMatches("brief.md"); !slices.Contains(got, externalPath) {
		t.Fatalf("external document matches = %#v, want %q", got, externalPath)
	}
	if paths := readOnlySourceLikePaths(results[1].Facts.DiagnosticEvidencePaths, 8); !slices.Contains(paths, diagnosticPath) {
		t.Fatalf("diagnostic evidence paths = %#v, want %q", paths, diagnosticPath)
	}
	if got := projection.ReadOnlySourceBodyEvidencePaths(); !slices.Contains(got, sourcePath) || !slices.Contains(got, taskPath) {
		t.Fatalf("read-only source body evidence paths = %#v, want %q and %q", got, sourcePath, taskPath)
	}
}
