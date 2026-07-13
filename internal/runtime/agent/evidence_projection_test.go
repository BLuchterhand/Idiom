package agent

import (
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("ToolResultEvidenceProjectionSourceWindowBoundaries", func() {
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
			result: readFileSourceWindowResult(
				sourcePath,
				20,
				40,
				220,
				"func TestToolResultEvidenceProjectionSourceWindowBoundaries(t *testing.T) {}\n",
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
			name: "read_file path without a source-window attachment is not body evidence",
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
			name: "durable source-window metadata without its exact attachment is path evidence only",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameReadFile,
				Summary: "Read internal/runtime/agent/evidence_projection.go lines 20-40 (total lines: 220)",
				Output:  "spoofed body text must not become exact source evidence\n",
				Paths:   []string{sourcePath},
				Facts:   sourceWindowFactsForTest(sourcePath, 20, 40, 220),
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
			wantLevel: planThenExecuteEvidenceNone,
			wantBody:  false,
		},
		{
			name: "generated artifacts are not source-window evidence",
			path: generatedPath,
			result: readFileSourceWindowResult(
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
			name: "routed code_navigate source window",
			path: sourcePath,
			result: codeNavigateSourceWindowResult(
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
			name: "symbol read source window",
			path: sourcePath,
			result: sourceWindowResultForTest(
				"go_read_symbol", sourcePath, 12, 24, 24,
				"Read TestToolResultEvidenceProjectionSourceWindowBoundaries in internal/runtime/agent/evidence_projection.go lines 12-24",
				"func TestToolResultEvidenceProjectionSourceWindowBoundaries(t *testing.T) {}\n",
			),
			wantLevel: planThenExecuteEvidenceAnchorReady,
			wantBody:  true,
		},
		{
			name: "context zoom includes a source window",
			path: sourcePath,
			result: sourceWindowResultForTest(
				workspace.ToolNameContextZoom, sourcePath, 1, 20, 20,
				"Read internal/runtime/agent/evidence_projection.go lines 1-20 with include_source",
				`{"path":"internal/runtime/agent/evidence_projection.go","source":"func TestToolResultEvidenceProjectionSourceWindowBoundaries(t *testing.T) {}"}`,
			),
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
			wantLevel: planThenExecuteEvidenceNone,
			wantBody:  false,
		},
		{
			name: "file outline is not body evidence",
			path: sourcePath,
			result: workspace.ToolResult{
				Tool:    workspace.ToolNameCodeNavigate,
				Summary: "Read outline in internal/runtime/agent/evidence_projection.go with capability: file_outline",
				Output:  "capability: file_outline\nfn TestToolResultEvidenceProjectionSourceWindowBoundaries\nstruct toolResultEvidenceProjection\n",
			},
			wantLevel: planThenExecuteEvidenceNone,
			wantBody:  false,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			evidence := newToolResultEvidenceProjection([]workspace.ToolResult{tt.result}).SourceEvidenceForPath(tt.path)
			gomega.Expect(evidence.Level).To(gomega.Equal(tt.wantLevel), "evidence=%#v", evidence)
			gomega.Expect(sourceEvidenceHasBody(evidence)).To(gomega.Equal(tt.wantBody), "evidence=%#v", evidence)
		})
	}
})

var _ = ginkgo.It("ToolResultEvidenceProjectionCandidateBoundaries", func() {
	const sourcePath = "internal/runtime/agent/evidence_projection.go"

	contextResult := workspace.ToolResult{
		Tool:    workspace.ToolNameContextPack,
		Summary: "Context pack returned candidate paths.",
		Facts:   contextPackCandidateFactsForTest(sourcePath),
	}
	gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{contextResult}).CandidatePaths()).To(gomega.ContainElement(sourcePath))

	bodyResult := readFileSourceWindowResult(sourcePath, 1, 20, 0, "package agent\n")
	gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{bodyResult}).CandidatePaths()).NotTo(gomega.ContainElement(sourcePath), "source body path must not also be an unread candidate")

	ledger := readOnlyAnswerabilityForResults(testDirectAnswerSurfaceBrief(), []workspace.ToolResult{contextResult})
	gomega.Expect(ledger.CandidatePaths).To(gomega.ContainElement(sourcePath), "ledger=%#v", ledger)
	gomega.Expect(ledger.HasUnreadCandidatePaths).To(gomega.BeTrue(), "ledger=%#v", ledger)

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
	gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{factResult}).CandidatePaths()).To(gomega.Equal([]string{sourcePath}))
	outcome := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: workspace.ToolNameContextQuery}, factResult, 4)
	gomega.Expect(outcome.Strength).To(gomega.Equal(toolfacts.DiscoveryStrengthSourceOwner), "outcome=%#v", outcome)
	gomega.Expect(outcome.Candidates).To(gomega.Equal([]string{sourcePath}), "outcome=%#v", outcome)

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
	gomega.Expect(outcome.Strength).To(gomega.Equal(toolfacts.DiscoveryStrengthCandidateOnly), "outcome=%#v", outcome)
	gomega.Expect(outcome.SearchHitCount).To(gomega.Equal(75), "outcome=%#v", outcome)
	gomega.Expect(outcome.HighCardinalitySearch).To(gomega.BeTrue(), "outcome=%#v", outcome)
	gomega.Expect(outcome.Candidates).To(gomega.Equal([]string{sourcePath}), "outcome=%#v", outcome)
})

var _ = ginkgo.It("ToolResultEvidenceProjectionCloseoutUsesProjectedResultSnapshot", func() {
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
	gomega.Expect(closeout.Answerable()).To(gomega.BeTrue(), "closeout=%#v", closeout)
	gomega.Expect(closeout.Results).To(gomega.HaveLen(1))
	gomega.Expect(closeout.Results[0].Tool).To(gomega.Equal(workspace.ToolNameTask))
})

var _ = ginkgo.It("FactlessDiscoveryQueryOutputDoesNotCreateCandidateAuthority", func() {
	result := workspace.ToolResult{
		Tool:    workspace.ToolNameDiscoveryQuery,
		Summary: "Ran discovery query",
		Output:  `{"evidence":[{"name":"text_anchor_search","output_excerpt":"internal/runtime/agent/runner_core.go:10: runChildTask"}]}`,
	}
	results := []workspace.ToolResult{result}

	gomega.Expect(newToolResultEvidenceProjection(results).CandidatePaths()).To(gomega.BeEmpty())
	gomega.Expect(discoveryQueryCandidatePaths(results, 4)).To(gomega.BeEmpty())
	outcome := discoveryResultOutcomeFromResult(workspace.ToolRequest{Tool: workspace.ToolNameDiscoveryQuery}, result, 4)
	gomega.Expect(outcome.Strength).To(gomega.BeEmpty(), "outcome=%#v", outcome)
	gomega.Expect(outcome.Candidates).To(gomega.BeEmpty(), "outcome=%#v", outcome)
})

var _ = ginkgo.It("ToolResultEvidenceProjectionChangedPaths", func() {
	results := []workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"internal/app/edit.go"}},
		{Tool: workspace.ToolNameWriteFile, ChangedPaths: []string{"internal/app/write.go"}},
		{Tool: workspace.ToolNameAppendFile, ChangedPaths: []string{"internal/app/append.go"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/add.go", "internal/app/update.go"}},
		{Tool: workspace.ToolNameGoMoveDeclarations, ChangedPaths: []string{"internal/app/source.go", "internal/app/destination.go"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/partial.go"}, Error: "second operation failed after a partial commit"},
		{Tool: workspace.ToolNameReadFile, Summary: "Read internal/app/read.go lines 1-10"},
	}
	wantChangedSet := []string{
		"internal/app/add.go",
		"internal/app/append.go",
		"internal/app/destination.go",
		"internal/app/edit.go",
		"internal/app/partial.go",
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
		"internal/app/partial.go",
	}

	projection := newToolResultEvidenceProjection(results)
	gomega.Expect(mapKeys(projection.ChangedPathSet())).To(gomega.Equal(wantChangedSet))
	gomega.Expect(projection.ChangedPathList()).To(gomega.Equal(wantChangedList))
})

var _ = ginkgo.It("ToolResultEvidenceProjectionChangedSourcePathsUseSourceSanityContract", func() {
	results := []workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"README.md"}},
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"scripts/panel.gd"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/edit.go", "docs/notes.txt"}},
		{Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"internal/app/partial.go"}, Error: "second operation failed after a partial commit"},
	}

	projection := newToolResultEvidenceProjection(results)
	want := []string{"internal/app/edit.go", "internal/app/partial.go", "scripts/panel.gd"}

	gomega.Expect(projection.ChangedSourcePathList()).To(gomega.Equal(want))
	gomega.Expect(projection.HasChangedSourcePath("scripts/panel.gd")).To(gomega.BeTrue())
	gomega.Expect(projection.HasChangedSourcePath("README.md")).To(gomega.BeFalse())
	target, ok := projection.SingleChangedSourcePath()
	gomega.Expect(ok).To(gomega.BeFalse(), "multiple source paths should not choose target %q", target)

	single := newToolResultEvidenceProjection([]workspace.ToolResult{
		{Tool: workspace.ToolNameEditFile, ChangedPaths: []string{"scripts/panel.gd", "README.md"}},
	})
	target, ok = single.SingleChangedSourcePath()
	gomega.Expect(ok).To(gomega.BeTrue(), "target=%q", target)
	gomega.Expect(target).To(gomega.Equal("scripts/panel.gd"))
})

var _ = ginkgo.It("ToolResultEvidenceProjectionReadOnlyCategoryBoundaries", func() {
	t := ginkgo.GinkgoT()
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
			Facts:   sourceWindowFactsForTest(externalPath, 1, 20, 0),
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
		readFileSourceWindowResult(sourcePath, 1, 20, 0, "1:package agent\n"),
		readFileSourceWindowResult(generatedPath, 1, 20, 20, `{"candidate_files":[{"path":"`+sourcePath+`"}]}`),
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
			Facts: sourceWindowFactsForTest("internal/runtime/agent/failed.go", 1, 10, 10),
		},
	}

	projection := newToolResultEvidenceProjection(results)
	observations := projection.ReadOnlyEvidenceObservations()
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind": gomega.Equal(readOnlyEvidenceSourceWindow),
		"Path": gomega.Equal(sourcePath),
	})), "source body %s", sourcePath)
	gomega.Expect(observations).NotTo(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind": gomega.Equal(readOnlyEvidenceExternalDocument),
		"Path": gomega.Equal(externalPath),
	})), "absolute external document must not become source-window body evidence")
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":           gomega.Equal(readOnlyEvidenceDiagnostic),
		"CandidatePaths": gomega.ContainElement(diagnosticPath),
	})), "diagnostic %s", diagnosticPath)
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind": gomega.Equal(readOnlyEvidenceGeneratedArtifact),
		"Tool": gomega.Equal(workspace.ToolNameReadFile),
	})), "generated artifact")
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":           gomega.Equal(readOnlyEvidencePlanArtifact),
		"CandidatePaths": gomega.ContainElement(planPath),
	})), "plan artifact %s", planPath)
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":           gomega.Equal(readOnlyEvidenceOutline),
		"CandidatePaths": gomega.ContainElement(outlinePath),
	})), "outline %s", outlinePath)
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":           gomega.Equal(readOnlyEvidenceContextPacket),
		"CandidatePaths": gomega.ContainElement(contextPath),
	})), "context packet %s", contextPath)
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":            gomega.Equal(readOnlyEvidenceSearchHit),
		"Tool":            gomega.Equal(workspace.ToolNameSearchContent),
		"CandidatePaths":  gomega.ContainElement(searchPath),
		"HitCount":        gomega.Equal(75),
		"HighCardinality": gomega.BeTrue(),
	})), "search hit %s", searchPath)
	gomega.Expect(observations).To(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Kind":           gomega.Equal(readOnlyEvidenceSearchHit),
		"Tool":           gomega.Equal(workspace.ToolNameDiscoveryQuery),
		"CandidatePaths": gomega.ContainElement(discoveryPath),
	})), "discovery candidate %s", discoveryPath)
	gomega.Expect(observations).NotTo(gomega.ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Path": gomega.Equal("internal/runtime/agent/failed.go"),
	})), "failed results must not become read-only evidence observations")
	gomega.Expect(projection.SourceWindowPaths()).NotTo(gomega.ContainElement(externalPath), "external document leaked into workspace source-window paths")
	gomega.Expect(projection.ExternalDocumentBodyMatches("brief.md")).To(gomega.BeEmpty())
	gomega.Expect(readOnlySourceLikePaths(results[1].Facts.DiagnosticEvidencePaths, 8)).To(gomega.ContainElement(diagnosticPath))
	gomega.Expect(projection.ReadOnlySourceWindowEvidencePaths()).To(gomega.ContainElements(sourcePath, taskPath))
})

var _ = ginkgo.It("ReadOnlyAnswerabilityUsesTypedDiagnosticLogMetadataWithoutPromotingSourceBody", func() {
	const (
		canonicalLogPath = "artifacts/runtime/events.jsonl"
		presentedLogPath = "logs/runtime/app.jsonl"
	)
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "information_request", Contract: string(workerruntime.ContractDirectAnswer),
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		Objective:         "Explain the failure recorded in logs/runtime/app.jsonl.",
		TargetRefs:        []string{presentedLogPath},
	}
	logRead := diagnosticLogReadResultForTest(
		canonicalLogPath,
		presentedLogPath,
		10,
		20,
		20,
		false,
		"secret.go: rendered output is not diagnostic evidence authority",
	)
	logRead.Paths = []string{"secret.go"}
	logRead.Facts.SourceWindows = sourceWindowFactsForTest(canonicalLogPath, 10, 20, 20).SourceWindows
	ledger := readOnlyAnswerabilityForResults(brief, []workspace.ToolResult{logRead})
	gomega.Expect(ledger.DiagnosticEvidence).To(gomega.BeTrue())
	gomega.Expect(ledger.DiagnosticRelevant).To(gomega.BeTrue())
	gomega.Expect(ledger.DiagnosticPaths).To(gomega.ConsistOf(canonicalLogPath, presentedLogPath))
	gomega.Expect(ledger.Answerable()).To(gomega.BeTrue())
	gomega.Expect(ledger.SourceBodyPaths).To(gomega.BeEmpty(), "diagnostic-log metadata must remain source-ineligible")
	projection := newToolResultEvidenceProjection([]workspace.ToolResult{logRead})
	gomega.Expect(projection.ReadPaths()).To(gomega.HaveKey(canonicalLogPath), "typed diagnostic identity must remain observable")
	gomega.Expect(projection.ReadPaths()).NotTo(gomega.HaveKey("secret.go"), "generic result paths must not override typed diagnostic identity")
	gomega.Expect(projection.HasSourceWindow(canonicalLogPath)).To(gomega.BeFalse(), "a diagnostic result must not co-carry source-body authority")
	gomega.Expect(projection.DiagnosticLogReadCoversPath(canonicalLogPath, 10, 20)).To(gomega.BeTrue())
	gomega.Expect(projection.DiagnosticLogReadCoversPath(presentedLogPath, 12, 18)).To(gomega.BeTrue())
	gomega.Expect(projection.DiagnosticLogReadCoversPath(presentedLogPath, 1, 20)).To(gomega.BeFalse())
	gomega.Expect(projection.DiagnosticLogReadCoversPath(presentedLogPath, 20, 10)).To(gomega.BeFalse())
	gomega.Expect(projection.MentionedEvidencePathsForObjective(brief.Objective, 8)).To(gomega.Equal([]string{canonicalLogPath}),
		"rendered log content must not mint mentioned evidence paths")

	outputOnly := readOnlyAnswerabilityForResults(brief, []workspace.ToolResult{{
		Tool: workspace.ToolNameReadFile, Output: presentedLogPath + ": failure text without producer facts",
	}})
	gomega.Expect(outputOnly.DiagnosticEvidence).To(gomega.BeFalse())
	gomega.Expect(outputOnly.Answerable()).To(gomega.BeFalse())
	for _, rejected := range []workspace.ToolResult{
		{Tool: workspace.ToolNameSearchContent, Facts: logRead.Facts},
		{Tool: workspace.ToolNameReadFile, Error: "read failed", Facts: logRead.Facts},
		{Tool: workspace.ToolNameReadFile, Facts: toolfacts.NewDiagnosticLogReadFacts(toolfacts.DiagnosticLogReadEvidence{
			CanonicalID: "../outside.log", PolicySubject: presentedLogPath, PolicyDecision: toolpolicy.Allow,
			StartLine: 10, EndLine: 20, TotalLines: 20,
		})},
	} {
		gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{rejected}).DiagnosticLogReadCoversPath(presentedLogPath, 0, 0)).To(gomega.BeFalse())
	}

	debugSource := readFileSourceWindowResult("internal/evidence/transcript/debuglog.go", 1, 20, 20, "1:package transcript\n")
	debugLedger := readOnlyAnswerabilityForResults(brief, []workspace.ToolResult{debugSource})
	gomega.Expect(debugLedger.DiagnosticEvidence).To(gomega.BeFalse(), "debuglog source remains SourceWindow evidence, not diagnostic-log evidence")
})

var _ = ginkgo.It("EmptyTypedDiagnosticLogCountsAsInspectedWithoutInventingLineCoverage", func() {
	result := diagnosticLogReadResultForTest(
		"logs/empty.log",
		"logs/empty.log",
		0,
		0,
		0,
		false,
		"",
	)

	window, ok := parseDebugLogReadWindow(result)
	gomega.Expect(ok).To(gomega.BeTrue())
	gomega.Expect(window).To(gomega.Equal(debugLogReadWindow{
		Path:        "logs/empty.log",
		CanonicalID: "logs/empty.log",
	}))
	gomega.Expect(hasDebugLogReadForSelection([]workspace.ToolResult{result}, debugLogFileSelection{Path: "logs/empty.log"})).To(gomega.BeTrue())
	gomega.Expect(newToolResultEvidenceProjection([]workspace.ToolResult{result}).DiagnosticLogReadCoversPath("logs/empty.log", 1, 1)).To(gomega.BeFalse())
})
