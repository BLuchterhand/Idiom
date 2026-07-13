package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func readOnlySourceGroundingRunner() *runExecution {
	return newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
}

func readOnlySourceGroundingWindowFixture(path string, ranges ...[2]int) (*runExecution, []workspace.ToolResult) {
	t := ginkgo.GinkgoT()
	t.Helper()
	root := t.TempDir()
	const totalLines = 160
	lines := make([]string, totalLines)
	for index := range lines {
		lines[index] = "source line"
	}
	absolute := filepath.Join(root, filepath.FromSlash(path))
	gomega.Expect(os.MkdirAll(filepath.Dir(absolute), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(absolute, []byte(strings.Join(lines, "\n")), 0o600)).To(gomega.Succeed())

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, MaxOutputBytes: 4 << 20})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	results := make([]workspace.ToolResult, 0, len(ranges))
	for _, lineRange := range ranges {
		result := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: path, StartLine: lineRange[0], EndLine: lineRange[1],
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		gomega.Expect(result.SourceWindows()).To(gomega.HaveLen(1))
		results = append(results, result)
	}
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "direct_answer", Contract: "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
	return runner, results
}

var _ = ginkgo.It("SourceGroundingEscapingMentionedPaths", func() {
	answer := "The bug is in `/tmp/outside/labels.go:6`, and a related path is ../other/config.yaml."

	paths := sourceReferencePaths(analyzeSourceCitations(answer).EscapingReferences())
	gomega.Expect(paths).To(gomega.Equal([]string{"../other/config.yaml", "/tmp/outside/labels.go"}))
})

var _ = ginkgo.It("SourceGroundingEscapingUnsupportedPathsAllowUnverifiedQualification", func() {
	answer := "The outside path `/tmp/outside/labels.go:6` is unverified and outside the workspace."

	references := analyzeSourceCitations(answer).EscapingReferences()
	paths := sourceReferencePaths(references)
	gomega.Expect(paths).To(gomega.Equal([]string{"/tmp/outside/labels.go"}), "escaping path detection changed")

	unsupported := sourceGroundingUnsupportedEscapingReferences(answer, references, newToolResultEvidenceProjection(nil))
	gomega.Expect(unsupported).To(gomega.BeEmpty(), "unverified outside-workspace citation should not block source grounding")
})

var _ = ginkgo.It("SourceGroundingRejectsExternalDocumentOutputWithoutSourceWindow", func() {
	t := ginkgo.GinkgoT()
	docPath := filepath.Join(t.TempDir(), "Mobile Documents", "Website Project", "conversation brief.md")
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "What does the Website failure brief say about duplicate surfaces?"
	results := []workspace.ToolResult{
		readFileSourceWindowResult(docPath, 1, 4, 4, "1:The Website failure brief says duplicate surfaces caused drift.\n2:The fix is to consolidate final-answer acceptance and completion-gate projection."),
	}
	answer := "The Website failure brief says duplicate surfaces caused drift, and that is grounded in `" + filepath.ToSlash(filepath.Clean(docPath)) + "`."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring(filepath.ToSlash(filepath.Clean(docPath))), "absolute external output must not become source-window evidence")
})

var _ = ginkgo.It("SourceGroundingRejectsUnbackedExternalDocumentCitation", func() {
	t := ginkgo.GinkgoT()
	docPath := filepath.ToSlash(filepath.Join(t.TempDir(), "Mobile Documents", "Website Project", "conversation brief.md"))
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "What does the Website failure brief say about duplicate surfaces?"
	answer := "The Website failure brief says duplicate surfaces caused drift, and that is grounded in `" + docPath + "`."

	reason := runner.assessSourceGrounding(answer, nil).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring(docPath), "unbacked external document citation should be rejected")
})

var _ = ginkgo.It("SourceGroundingDoesNotTrustLegacyBroadDiscoveryRejectionProse", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "conversation", Contract: "direct_answer", PermissionProfile: "read_only_workspace",
		Objective: "Explain the implementation in pkg/target.go.", AllowedTools: []string{workspace.ToolNameReadFile, workspace.ToolNameSearchContent},
		Readiness: &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"source evidence"}},
	}
	results := []workspace.ToolResult{{
		Tool: workspace.ToolNameSearchContent, Summary: "Conversation contract rejected repeated broad discovery",
		Error: "legacy strategy rejection text",
	}}
	gomega.Expect(genericReadOnlyAnswerEvidenceReady(brief, results)).To(gomega.BeFalse())
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	gomega.Expect(run.assessSourceGrounding("The behavior is implemented in `pkg/target.go`.", results).BlocksFinalAnswer()).To(gomega.BeTrue())
})

var _ = ginkgo.It("SourceGroundingProjectsBoundPresentationAliasesToCurrentCanonicalWindows", func() {
	root := ginkgo.GinkgoT().TempDir()
	for _, dir := range []string{"real", "other"} {
		gomega.Expect(os.MkdirAll(filepath.Join(root, dir), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, dir, "target.go"), []byte("package "+dir+"\n"), 0o600)).To(gomega.Succeed())
	}
	aliasPath := filepath.Join(root, "alias")
	if err := os.Symlink("real", aliasPath); err != nil {
		ginkgo.Skip("symlinks unavailable: " + err.Error())
	}
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	run := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"alias/target.go"},
		},
	})
	gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
	read := manager.Execute(run.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "alias/target.go", StartLine: 1, EndLine: 1,
	})
	gomega.Expect(read.Error).To(gomega.BeEmpty())
	projection := newToolResultEvidenceProjection([]workspace.ToolResult{read})

	for _, cited := range []string{"alias/target.go", "real/target.go"} {
		canonical, ok := sourceCitationCanonicalPath(run, cited, projection)
		gomega.Expect(ok).To(gomega.BeTrue(), "citation %q should resolve through the bound scope", cited)
		gomega.Expect(canonical).To(gomega.Equal("real/target.go"))
		gomega.Expect(sourceCitationBackedByCurrentWindow(run, canonical, &sourceCitationRange{Start: 1, End: 1}, projection)).To(gomega.BeTrue())
	}
	_, sibling := sourceCitationCanonicalPath(run, "alias/sibling.go", projection)
	gomega.Expect(sibling).To(gomega.BeFalse())

	gomega.Expect(os.Remove(aliasPath)).To(gomega.Succeed())
	gomega.Expect(os.Symlink("other", aliasPath)).To(gomega.Succeed())
	gomega.Expect(sourceCitationBackedByCurrentWindow(run, "real/target.go", &sourceCitationRange{Start: 1, End: 1}, projection)).To(gomega.BeFalse(),
		"retargeting the presented alias must invalidate the durable canonical window")

})

var _ = ginkgo.It("SourceGroundingRejectsRelativeCitationToExternalDocumentOutput", func() {
	t := ginkgo.GinkgoT()
	root := filepath.Join(t.TempDir(), "Mobile Documents", "com~apple~CloudDocs", "Idiom Book Project")
	backMatterPath := filepath.Join(root, "front-back", "back-matter.md")
	bookMatterPath := filepath.Join(root, "front-back", "book-matter-and-cover-copy-proposal.md")
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "What can be added to the Website from the iCloud Idiom book project?"
	results := []workspace.ToolResult{
		readFileSourceWindowResult(bookMatterPath, 1, 80, 120, "1:The book matter proposes Trust the Process, Not the Prompt as the book title.\n"),
		readFileSourceWindowResult(backMatterPath, 1, 80, 95, "1:The back matter includes author biography notes for the website.\n"),
	}
	answer := "Add book-positioning copy from `front-back/book-matter-and-cover-copy-proposal.md` and author bio material from `front-back/back-matter.md`."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).NotTo(gomega.BeEmpty(), "relative external citations require a supported evidence attachment")
})

var _ = ginkgo.It("SourceGroundingPreservesButRejectsBacktickedExternalDocumentPathWithSpaces", func() {
	t := ginkgo.GinkgoT()
	docPath := filepath.ToSlash(filepath.Join(t.TempDir(), "Mobile Documents", "com~apple~CloudDocs", "Idiom Book Project", "front-back", "back-matter.md"))
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "What can be added to the Website from the Idiom book and author material?"
	results := []workspace.ToolResult{
		readFileSourceWindowResult(docPath, 1, 80, 95, "1:The back matter includes author biography notes for the website.\n"),
	}
	answer := "The author material is grounded in `" + docPath + "`."

	paths := analyzeSourceCitations(answer).BodyReadablePaths()
	gomega.Expect(paths).To(gomega.ContainElement(docPath), "external path with spaces must remain intact")
	gomega.Expect(paths).NotTo(gomega.ContainElement("Project/front-back/back-matter.md"), "external path must not be truncated")

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring(docPath), "absolute external output must not authorize source grounding")
})

var _ = ginkgo.It("SourceGroundingMentionedPathsIgnoreBacktickedCommands", func() {
	answer := "| Test file | Command |\n| `tests/test_models.py` | `pytest tests/test_models.py` |"

	paths := analyzeSourceCitations(answer).BodyReadablePaths()
	gomega.Expect(paths).To(gomega.ContainElement("tests/test_models.py"))
	gomega.Expect(paths).NotTo(gomega.ContainElement("pytest tests/test_models.py"), "backticked command spans are not citations")
})

var _ = ginkgo.It("SourceGroundingAllowsQuotedLineRangeForBackedSourceBody", func() {
	runner, results := readOnlySourceGroundingWindowFixture("docs/decisioning-north-star.md", [2]int{26, 53})
	answer := "From `docs/decisioning-north-star.md:28-34`, the north star says the model owns strategy and the harness owns contracts."

	citations := analyzeSourceCitations(answer)
	paths := citations.BodyReadablePaths()
	gomega.Expect(paths).To(gomega.ContainElement("docs/decisioning-north-star.md"), "line range must be stripped")
	gomega.Expect(paths).NotTo(gomega.ContainElement("docs/decisioning-north-star.md:28-34"), "literal line range is not a path")
	gomega.Expect(citations.BodyReadableReferences()).To(gomega.ContainElement(gomega.Satisfy(func(reference sourceReference) bool {
		return reference.Path == "docs/decisioning-north-star.md" && reference.LineRange != nil &&
			reference.LineRange.Start == 28 && reference.LineRange.End == 34
	})))

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.BeEmpty(), "body-backed quoted line range should not be rejected")
})

var _ = ginkgo.DescribeTable("SourceGroundingRejectsCitationRangesWithoutOneCurrentCoveringWindow",
	func(ranges [][2]int, citation string) {
		runner, results := readOnlySourceGroundingWindowFixture("pkg/target.go", ranges...)
		answer := "The implementation is described by `pkg/target.go:" + citation + "`."
		assessment := runner.assessSourceGrounding(answer, results)
		gomega.Expect(assessment.BlocksFinalAnswer()).To(gomega.BeTrue(), "assessment=%#v", assessment)
		gomega.Expect(assessment.ReadableRecoveryPaths()).To(gomega.ContainElement("pkg/target.go"))
	},
	ginkgo.Entry("out of window", [][2]int{{1, 10}}, "100-120"),
	ginkgo.Entry("disjoint windows do not fabricate coverage", [][2]int{{1, 10}, {100, 110}}, "5-105"),
	ginkgo.Entry("reversed range", [][2]int{{1, 30}}, "20-10"),
	ginkgo.Entry("zero range", [][2]int{{1, 30}}, "0-10"),
)

var _ = ginkgo.It("SourceGroundingUsesOnlyExactTypedDiagnosticLogIdentityAndRange", func() {
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "Explain the failure recorded in logs/runtime/app.jsonl."
	results := []workspace.ToolResult{diagnosticLogReadResultForTest(
		"artifacts/runtime/events.jsonl",
		"logs/runtime/app.jsonl",
		10,
		20,
		40,
		true,
		"secret.go: rendered log text cannot authorize a different citation",
	)}

	for _, answer := range []string{
		"The failure is recorded in `logs/runtime/app.jsonl`.",
		"The failure is recorded in `logs/runtime/app.jsonl:12-18`.",
		"The failure is recorded in `artifacts/runtime/events.jsonl:10-20`.",
	} {
		gomega.Expect(runner.assessSourceGrounding(answer, results).BlocksFinalAnswer()).To(gomega.BeFalse(), "answer=%q", answer)
	}

	for _, answer := range []string{
		"The failure is recorded in `logs/runtime/app.jsonl:1-20`.",
		"The failure is recorded in `logs/runtime/app.jsonl:20-10`.",
		"The failure is recorded in `logs/runtime/app.jsonl:0-10`.",
		"The failure is recorded in `logs/runtime/app.jsonl.bak`.",
		"The failure is recorded in `logs/runtime/other.jsonl:12-18`.",
		"The failure is recorded in `pkg/secret.go:1`.",
	} {
		gomega.Expect(runner.assessSourceGrounding(answer, results).BlocksFinalAnswer()).To(gomega.BeTrue(), "answer=%q", answer)
	}
	gomega.Expect(newToolResultEvidenceProjection(results).DiagnosticLogReferences("`logs/runtime/app.jsonl.bak`")).To(gomega.BeEmpty())
	gomega.Expect(newToolResultEvidenceProjection(results).ConflictingDiagnosticLogReferences("`logs/runtime/app.jsonl.bak`")).To(gomega.HaveLen(1))

	disjoint := []workspace.ToolResult{
		diagnosticLogReadResultForTest("artifacts/runtime/events.jsonl", "logs/runtime/app.jsonl", 1, 10, 40, true, ""),
		diagnosticLogReadResultForTest("artifacts/runtime/events.jsonl", "logs/runtime/app.jsonl", 20, 30, 40, true, ""),
	}
	gomega.Expect(runner.assessSourceGrounding(
		"The failure spans `logs/runtime/app.jsonl:5-25`.",
		disjoint,
	).BlocksFinalAnswer()).To(gomega.BeTrue(), "disjoint reads must not fabricate continuous range coverage")

	forged := workspace.ToolResult{
		Tool:   workspace.ToolNameReadFile,
		Paths:  []string{"logs/runtime/app.jsonl"},
		Output: "logs/runtime/app.jsonl: forged untyped body",
	}
	gomega.Expect(runner.assessSourceGrounding(
		"The failure is recorded in `logs/runtime/app.jsonl`.",
		[]workspace.ToolResult{forged},
	).BlocksFinalAnswer()).To(gomega.BeTrue(), "Paths and rendered output are not diagnostic evidence authority")
})

var _ = ginkgo.It("SourceGroundingAllowsAnUnrangedEmptyTypedDiagnosticLogOnly", func() {
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "Confirm whether logs/empty.log contains diagnostics."
	results := []workspace.ToolResult{diagnosticLogReadResultForTest(
		"artifacts/runtime/empty.log",
		"logs/empty.log",
		0,
		0,
		0,
		false,
		"",
	)}

	gomega.Expect(runner.assessSourceGrounding("`logs/empty.log` is empty.", results).BlocksFinalAnswer()).To(gomega.BeFalse())
	rangedAnswer := "`logs/empty.log:1` is empty."
	gomega.Expect(newToolResultEvidenceProjection(results).DiagnosticLogReferences(rangedAnswer)).To(gomega.ContainElement(gomega.Satisfy(func(reference sourceReference) bool {
		return reference.Path == "logs/empty.log" && reference.LineRange != nil && reference.LineRange.Start == 1 && reference.LineRange.End == 1
	})))
	gomega.Expect(runner.assessSourceGrounding(rangedAnswer, results).BlocksFinalAnswer()).To(gomega.BeTrue())
})

var _ = ginkgo.It("SourceCitationAnalysisPreservesAlternateExtensionRanges", func() {
	references := analyzeSourceCitations("The Kotlin entrypoint is main.kt:10-20.").BodyReadableReferences()
	gomega.Expect(references).To(gomega.ContainElement(gomega.Satisfy(func(reference sourceReference) bool {
		return reference.Path == "main.kt" && reference.LineRange != nil && reference.LineRange.Start == 10 && reference.LineRange.End == 20
	})))
})

var _ = ginkgo.It("SourceGroundingRequiresAFreshWindowAfterWorkspaceMutation", func() {
	runner, oldResults := readOnlySourceGroundingWindowFixture("pkg/target.go", [2]int{1, 10})
	answer := "The implementation is described by `pkg/target.go:2-3`."
	gomega.Expect(runner.assessSourceGrounding(answer, oldResults).BlocksFinalAnswer()).To(gomega.BeFalse())
	unrangedAnswer := "The current source `pkg/target.go` describes the implementation."
	gomega.Expect(runner.assessSourceGrounding(unrangedAnswer, oldResults).BlocksFinalAnswer()).To(gomega.BeFalse())

	updatedLines := make([]string, 160)
	updatedLines[0] = "package sample"
	for index := 1; index < len(updatedLines); index++ {
		updatedLines[index] = "// updated source line"
	}
	write := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameWriteFile, Path: "pkg/target.go", Content: strings.Join(updatedLines, "\n"), Description: "update source revision",
	})
	gomega.Expect(write.Error).To(gomega.BeEmpty())
	gomega.Expect(runner.assessSourceGrounding(answer, oldResults).BlocksFinalAnswer()).To(gomega.BeTrue(), "a stale source window must not authorize the old cited range")
	gomega.Expect(runner.assessSourceGrounding(unrangedAnswer, oldResults).BlocksFinalAnswer()).To(gomega.BeTrue(), "a stale source window must not authorize an unranged source-detail citation")

	fresh := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "pkg/target.go", StartLine: 1, EndLine: 10,
	})
	gomega.Expect(fresh.Error).To(gomega.BeEmpty())
	gomega.Expect(runner.assessSourceGrounding(answer, []workspace.ToolResult{fresh}).BlocksFinalAnswer()).To(gomega.BeFalse(), "a fresh current window should restore citation support")
})

var _ = ginkgo.It("ImplementationFinalAnswerRequiresAFreshWindowForCitedSourceRange", func() {
	runner, oldResults := readOnlySourceGroundingWindowFixture("pkg/target.go", [2]int{1, 10})
	runner.state.runtimeBrief = workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          string(workerruntime.ContractImplementationResult),
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		Objective:         "update the target implementation",
	}
	answer := "Updated the target behavior described by `pkg/target.go:2-3`."
	gomega.Expect(runner.assessSourceGrounding(answer, oldResults).BlocksFinalAnswer()).To(gomega.BeFalse())

	write := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameWriteFile, Path: "pkg/target.go", Content: "package sample\n\nfunc updated() {}\n", Description: "update target implementation",
	})
	gomega.Expect(write.Error).To(gomega.BeEmpty())
	gomega.Expect(runner.assessSourceGrounding(answer, append(oldResults, write)).BlocksFinalAnswer()).To(gomega.BeTrue(), "the commit must stale the source window used by an implementation citation")

	fresh := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "pkg/target.go", StartLine: 1, EndLine: 3,
	})
	gomega.Expect(fresh.Error).To(gomega.BeEmpty())
	gomega.Expect(runner.assessSourceGrounding(answer, []workspace.ToolResult{write, fresh}).BlocksFinalAnswer()).To(gomega.BeFalse(), "a fresh post-write window should authorize the cited range")
})

var _ = ginkgo.It("RangedSourceGroundingRecoveryAdvancesOnlyForOneCurrentCoveringWindow", func() {
	runner, stale := readOnlySourceGroundingWindowFixture("pkg/target.go", [2]int{1, 5})
	updatedLines := make([]string, 160)
	updatedLines[0] = "package sample"
	for index := 1; index < len(updatedLines); index++ {
		updatedLines[index] = "// updated source line"
	}
	write := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameWriteFile, Path: "pkg/target.go", Content: strings.Join(updatedLines, "\n"), Description: "advance source revision",
	})
	gomega.Expect(write.Error).To(gomega.BeEmpty())

	reference := sourceReferenceForPath("pkg/target.go:1-5")
	rejection := annotateSourceGroundingAssessment(workspace.ToolResult{Tool: completionGateToolName},
		sourceGroundingMissingCandidateReferenceAssessment([]sourceReference{reference}))
	runner.state.recordCompletionGateRejection(rejection)
	gomega.Expect(runner.state.completionGateRecovery.ActiveViolation.PendingSourceBodyReferences).To(gomega.HaveLen(1))

	assertNoProgress := func(result workspace.ToolResult) {
		ginkgo.GinkgoHelper()
		progress := runner.observeCompletionGateProgress(workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "pkg/target.go"}, result)
		gomega.Expect(progress.MadeProgress()).To(gomega.BeFalse())
		gomega.Expect(runner.state.completionGateRecovery.ActiveViolation.AttemptsSinceProgress).To(gomega.Equal(1))
	}
	assertNoProgress(stale[0])

	for _, lineRange := range [][2]int{{100, 110}, {1, 3}, {4, 5}} {
		read := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "pkg/target.go", StartLine: lineRange[0], EndLine: lineRange[1],
		})
		gomega.Expect(read.Error).To(gomega.BeEmpty())
		assertNoProgress(read)
	}

	fresh := runner.runner.Workspace.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "pkg/target.go", StartLine: 1, EndLine: 5,
	})
	gomega.Expect(fresh.Error).To(gomega.BeEmpty())
	progress := runner.observeCompletionGateProgress(workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "pkg/target.go"}, fresh)
	gomega.Expect(progress.MadeProgress()).To(gomega.BeTrue())
	gomega.Expect(progress.SatisfiedSourceBodyPaths).To(gomega.Equal([]string{"pkg/target.go"}))
	gomega.Expect(runner.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse())
})

var _ = ginkgo.It("SourceGroundingRejectsForgedRangeMetadata", func() {
	runner, results := readOnlySourceGroundingWindowFixture("pkg/target.go", [2]int{1, 10})
	gomega.Expect(results).To(gomega.HaveLen(1))
	gomega.Expect(results[0].Facts).NotTo(gomega.BeNil())
	gomega.Expect(results[0].Facts.SourceWindows).To(gomega.HaveLen(1))

	forged := results[0]
	forged.Facts = forged.Facts.Clone()
	forged.Facts.SourceWindows[0].EndLine = 20
	answer := "The implementation is described by `pkg/target.go:2-15`."

	assessment := runner.assessSourceGrounding(answer, []workspace.ToolResult{forged})
	gomega.Expect(assessment.BlocksFinalAnswer()).To(gomega.BeTrue(), "typed range metadata must match a producer-current window, not merely claim coverage")
	gomega.Expect(assessment.ReadableRecoveryPaths()).To(gomega.ContainElement("pkg/target.go"))
})

var _ = ginkgo.It("SourceCitationAnalysisContractAcrossRuntimeConsumers", func() {
	answer := strings.Join([]string{
		"From `docs/decisioning-north-star.md:28-34`, the north star says the model owns strategy.",
		"The Rust entrypoint `main.rs:10` routes that behavior, while `source grounding` is just prose.",
		"The token `path/to/file.go` is a tool schema placeholder, not a repository citation.",
		"```",
		"`fake/path.go:1` is a fenced example.",
		"```",
		"> `quoted/path.go:1` is quoted context.",
	}, "\n")

	citations := analyzeSourceCitations(answer)
	mentioned := citations.BodyReadablePaths()
	gomega.Expect(mentioned).To(gomega.ContainElement("docs/decisioning-north-star.md"), "line range must be stripped")
	gomega.Expect(mentioned).To(gomega.ContainElement("main.rs"), "non-Go file citations must be retained")
	for _, unwanted := range []string{"docs/decisioning-north-star.md:28-34", "path/to/file.go", "fake/path.go", "quoted/path.go"} {
		gomega.Expect(mentioned).NotTo(gomega.ContainElement(unwanted))
	}

	stripped := citations.textWithoutPathCitations()
	gomega.Expect(stripped).NotTo(gomega.ContainSubstring("docs/decisioning-north-star"), "path citations must be stripped from answer prose")
	gomega.Expect(stripped).NotTo(gomega.ContainSubstring("main.rs"), "path citations must be stripped from answer prose")
	gomega.Expect(stripped).NotTo(gomega.ContainSubstring("path/to/file.go"), "path citations must be stripped from answer prose")
	gomega.Expect(stripped).To(gomega.ContainSubstring("source grounding"), "non-path code span should remain in prose")

	inspected := readOnlyEvidencePacketSourceLikePaths(citations.BodyReadablePaths(), 16)
	for _, unwanted := range []string{"path/to/file.go", "fake/path.go", "quoted/path.go"} {
		gomega.Expect(inspected).NotTo(gomega.ContainElement(unwanted))
	}
})

var _ = ginkgo.It("SourceCitationAnalysisKeepsDirectoryLabelsTypedAndNonReadable", func() {
	answer := "The package labels `internal/capabilities/workspace/` and `internal/evidence/eval/` organize behavior implemented by `internal/runtime/agent/source_grounding.go`; external context is in `/tmp/idiom/brief.md`."
	citations := analyzeSourceCitations(answer)

	kindByPath := make(map[string]sourceReferenceKind, len(citations.References))
	for _, reference := range citations.References {
		kindByPath[reference.Path] = reference.Kind
	}

	for _, directory := range []string{"internal/capabilities/workspace", "internal/evidence/eval"} {
		gomega.Expect(kindByPath).To(gomega.HaveKeyWithValue(directory, sourceReferenceDirectoryLabel), "references=%#v", citations.References)
		gomega.Expect(citations.BodyReadablePaths()).NotTo(gomega.ContainElement(directory), "directory labels are not body-readable")
	}

	gomega.Expect(kindByPath).To(gomega.HaveKeyWithValue("internal/runtime/agent/source_grounding.go", sourceReferenceSourceFile), "references=%#v", citations.References)
	gomega.Expect(kindByPath).To(gomega.HaveKeyWithValue("/tmp/idiom/brief.md", sourceReferenceExternalDocument), "references=%#v", citations.References)
})

var _ = ginkgo.It("SourceGroundingDoesNotTurnQuotedDirectoryLabelsIntoBodyReadObligations", func() {
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Readiness = &workerruntime.ReadinessState{ReadyToAnswer: true}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/capabilities/workspace/harness_audit_contract_evidence.go", 1, 20, 100, "package workspace\nfunc auditContractEvidence() {}\n"),
	}
	answer := "The `internal/capabilities/workspace/` package owns the workspace evidence boundary."

	assessment := runner.assessSourceGrounding(answer, results)
	gomega.Expect(assessment.BlocksFinalAnswer()).To(gomega.BeFalse(), "assessment=%#v message=%q", assessment, assessment.Message())
})

var _ = ginkgo.It("SourceGroundingStillRejectsConcreteCandidateFileWithoutBodyEvidence", func() {
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Readiness = &workerruntime.ReadinessState{ReadyToAnswer: true}
	path := "internal/runtime/agent/unread_candidate.go"
	answer := "The function is implemented in `" + path + "`."

	assessment := runner.assessSourceGrounding(answer, nil)
	gomega.Expect(assessment.Kind).To(gomega.Equal(sourceGroundingRejectionMissingCandidate), "assessment=%#v", assessment)
	gomega.Expect(assessment.ReadableRecoveryPaths()).To(gomega.ContainElement(path), "assessment=%#v", assessment)
})

var _ = ginkgo.It("SourceGroundingAdvertisedMissingCandidateTerminalTransitionsAreAccepted", func() {
	backedPath := "internal/runtime/agent/source_grounding.go"
	runner, results := readOnlySourceGroundingWindowFixture(backedPath, [2]int{150, 190})
	runner.state.runtimeBrief.Readiness = &workerruntime.ReadinessState{ReadyToAnswer: true}
	missingPath := "internal/runtime/agent/unread_candidate.go"

	rejection := runner.assessSourceGrounding("The function is implemented in `"+missingPath+"`.", results)
	resolutionKinds := make(map[sourceGroundingRecoveryResolutionKind]bool, len(rejection.Resolutions))
	for _, resolution := range rejection.Resolutions {
		resolutionKinds[resolution.Kind] = true
	}
	gomega.Expect(rejection.Kind).To(gomega.Equal(sourceGroundingRejectionMissingCandidate), "rejection=%#v", rejection)
	gomega.Expect(resolutionKinds).To(gomega.HaveKeyWithValue(sourceGroundingRecoveryCiteBodyBacked, true), "rejection=%#v", rejection)
	gomega.Expect(resolutionKinds).To(gomega.HaveKeyWithValue(sourceGroundingRecoveryQualifyUnverified, true), "rejection=%#v", rejection)

	qualified := "The possible implementation path `" + missingPath + "` is unverified because its source body was not read."
	qualifiedAssessment := runner.assessSourceGrounding(qualified, results)
	gomega.Expect(qualifiedAssessment.BlocksFinalAnswer()).To(gomega.BeFalse(), "assessment=%#v message=%q", qualifiedAssessment, qualifiedAssessment.Message())

	bodyBackedRewrite := "The grounding assessment constructs recovery choices in `" + backedPath + "`."
	bodyBackedAssessment := runner.assessSourceGrounding(bodyBackedRewrite, results)
	gomega.Expect(bodyBackedAssessment.BlocksFinalAnswer()).To(gomega.BeFalse(), "assessment=%#v message=%q", bodyBackedAssessment, bodyBackedAssessment.Message())
})

var _ = ginkgo.It("SourceGroundingCandidateOnlyAdvertisesOnlyPassableTransitions", func() {
	assessment := sourceGroundingCandidateOnlyAssessment([]string{"internal/runtime/agent/source_grounding.go"})
	want := []sourceGroundingRecoveryResolutionKind{
		sourceGroundingRecoveryReadBody,
		sourceGroundingRecoveryProbeAlternative,
		sourceGroundingRecoveryStateBlocker,
	}
	got := make([]sourceGroundingRecoveryResolutionKind, 0, len(assessment.Resolutions))
	for _, resolution := range assessment.Resolutions {
		got = append(got, resolution.Kind)
	}

	gomega.Expect(got).To(gomega.Equal(want), "candidate-only resolutions must advertise only passable transitions")
})

var _ = ginkgo.It("SourceGroundingStillRejectsOutlineOnlySourceFile", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := "internal/runtime/agent/outlined.go"
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed(), "mkdir outline fixture")
	gomega.Expect(os.WriteFile(fullPath, []byte("package agent\nfunc outlinedBehavior() {}\n"), 0o644)).To(gomega.Succeed(), "write outline fixture")

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain outlinedBehavior",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: true},
		},
	})
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameCodeNavigate,
		Paths:   []string{path},
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "capability: file_outline\nroute_summary: Outlined 1 top-level declaration in " + path,
	}}
	answer := "The function `outlinedBehavior` is defined in `" + path + "`."

	assessment := runner.assessSourceGrounding(answer, results)
	gomega.Expect(assessment.Kind).To(gomega.Equal(sourceGroundingRejectionMissingCandidate), "assessment=%#v", assessment)
	gomega.Expect(assessment.ReadableRecoveryPaths()).To(gomega.ContainElement(path), "assessment=%#v", assessment)
	resolutionKinds := make(map[sourceGroundingRecoveryResolutionKind]bool, len(assessment.Resolutions))
	for _, resolution := range assessment.Resolutions {
		resolutionKinds[resolution.Kind] = true
	}
	gomega.Expect(resolutionKinds).To(gomega.HaveKeyWithValue(sourceGroundingRecoveryCiteBodyBacked, true), "assessment=%#v", assessment)
	gomega.Expect(resolutionKinds).To(gomega.HaveKeyWithValue(sourceGroundingRecoveryQualifyUnverified, true), "assessment=%#v", assessment)

	qualified := answer + " This source detail is unverified because the body was not read."
	qualifiedAssessment := runner.assessSourceGrounding(qualified, results)
	gomega.Expect(qualifiedAssessment.BlocksFinalAnswer()).To(gomega.BeFalse(), "assessment=%#v message=%q", qualifiedAssessment, qualifiedAssessment.Message())

	bodyResults := []workspace.ToolResult{manager.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 20,
	})}
	gomega.Expect(bodyResults[0].Error).To(gomega.BeEmpty())
	rewrittenAssessment := runner.assessSourceGrounding(answer, bodyResults)
	gomega.Expect(rewrittenAssessment.BlocksFinalAnswer()).To(gomega.BeFalse(), "assessment=%#v message=%q", rewrittenAssessment, rewrittenAssessment.Message())
})

var _ = ginkgo.It("SourceGroundingRecoveryNeverOffersDirectoryRead", func() {
	directory := "internal/evidence/eval/"
	file := "internal/runtime/agent/source_grounding.go"
	assessment := sourceGroundingCandidateOnlyAssessment([]string{directory, file})

	gomega.Expect(assessment.ReadableRecoveryPaths()).To(gomega.Equal([]string{file}), "only body-readable files may be offered for read recovery")
	prompt := assessment.RecoveryPrompt("")
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "- read ") {
			gomega.Expect(line).NotTo(gomega.ContainSubstring(strings.TrimRight(directory, "/")), "recovery offered a directory read; prompt=%s", prompt)
		}
	}
	gomega.Expect(prompt).To(gomega.ContainSubstring(strings.TrimRight(directory, "/")), "typed directory label should remain visible in rejection facts")
})

var _ = ginkgo.It("SourceGroundingRejectsAmbiguousRelativeExternalDocumentCitation", func() {
	t := ginkgo.GinkgoT()
	tempDir := t.TempDir()
	firstPath := filepath.Join(tempDir, "Project A", "front-back", "back-matter.md")
	secondPath := filepath.Join(tempDir, "Project B", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	runner.state.runtimeBrief.Objective = "What can be added to the Website from the iCloud Idiom book project?"
	results := []workspace.ToolResult{
		readFileSourceWindowResult(firstPath, 1, 10, 10, "1:first back matter\n"),
		readFileSourceWindowResult(secondPath, 1, 10, 10, "1:second back matter\n"),
	}
	answer := "Use the author material from `front-back/back-matter.md`."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring("front-back/back-matter.md"), "ambiguous relative external citation should remain rejected")
})

var _ = ginkgo.It("SourceGroundingRejectsUniqueBareExternalDocumentWithoutAttachment", func() {
	t := ginkgo.GinkgoT()
	docPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{
		readFileSourceWindowResult(docPath, 1, 10, 10, "1:The back matter includes author biography notes.\n"),
	}
	answer := "`back-matter.md:1` contains the author notes."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).NotTo(gomega.BeEmpty(), "bare external document output must not become source-window evidence")
})

var _ = ginkgo.It("SourceGroundingRejectsBareCitationAmbiguousAcrossWorkspaceAndExternalBodyReads", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspacePath := "docs/back-matter.md"
	gomega.Expect(os.MkdirAll(filepath.Join(root, "docs"), 0o755)).To(gomega.Succeed(), "mkdir workspace docs")
	gomega.Expect(os.WriteFile(filepath.Join(root, filepath.FromSlash(workspacePath)), []byte("workspace back matter\n"), 0o644)).To(gomega.Succeed(), "write workspace back matter")

	externalPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult(workspacePath, 1, 5, 5, "1:workspace back matter\n"),
		readFileSourceWindowResult(externalPath, 1, 10, 10, "1:external back matter\n"),
	}
	answer := "`back-matter.md:1` contains the author notes."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring("back-matter.md"), "bare citation ambiguous across workspace and external body reads should be rejected")
})

var _ = ginkgo.It("SourceGroundingRejectsExistingWorkspaceCitationEvenWhenExternalSuffixMatches", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	workspacePath := "front-back/back-matter.md"
	gomega.Expect(os.MkdirAll(filepath.Join(root, "front-back"), 0o755)).To(gomega.Succeed(), "mkdir workspace front-back")
	gomega.Expect(os.WriteFile(filepath.Join(root, filepath.FromSlash(workspacePath)), []byte("workspace back matter\n"), 0o644)).To(gomega.Succeed(), "write workspace back matter")

	externalPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult(externalPath, 1, 10, 10, "1:external back matter\n"),
	}
	answer := "`front-back/back-matter.md:1` defines the workspace author bio copy."

	reason := runner.assessSourceGrounding(answer, results).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring(workspacePath), "workspace citation must not be laundered through external suffix evidence")
})

var _ = ginkgo.It("SourceGroundingAssessmentPayloadCarriesMissingPaths", func() {
	assessment := sourceGroundingMissingCandidateReferenceAssessment(sourceReferencesForPaths([]string{
		"essay-series.md",
		"front-back/back-matter.md",
		"front-back/book-matter-and-cover-copy-proposal.md",
	}))

	result := annotateSourceGroundingAssessment(workspace.ToolResult{Tool: "completion_gate"}, assessment)
	got, ok := sourceGroundingAssessmentFromResult(result)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected typed source-grounding payload")

	want := []string{
		"essay-series.md",
		"front-back/back-matter.md",
		"front-back/book-matter-and-cover-copy-proposal.md",
	}
	gomega.Expect(got.Kind).To(gomega.Equal(sourceGroundingRejectionMissingCandidate))
	gomega.Expect(got.ReferencePaths()).To(gomega.Equal(want))
	gomega.Expect(got.ReadableRecoveryPaths()).To(gomega.Equal(want))
})

var _ = ginkgo.It("SourceGroundingAssessmentPayloadRequiresCompletionGatePayloadType", func() {
	assessment := sourceGroundingMissingCandidateReferenceAssessment(sourceReferencesForPaths([]string{"essay-series.md"}))
	result := annotateSourceGroundingAssessment(workspace.ToolResult{Tool: "completion_gate"}, assessment)

	wrongTool := result
	wrongTool.Tool = workspace.ToolNameReadFile
	_, ok := sourceGroundingAssessmentFromResult(wrongTool)
	gomega.Expect(ok).To(gomega.BeFalse(), "source-grounding payload on a non-completion-gate result must not carry authority")

	wrongPayloadType := result
	wrongPayloadType.PayloadType = "agent.source_grounding_assessment.v0"
	_, ok = sourceGroundingAssessmentFromResult(wrongPayloadType)
	gomega.Expect(ok).To(gomega.BeFalse(), "source-grounding payload with an unexpected payload type must not carry authority")
})

var _ = ginkgo.It("SourceGroundingRejectsPathOnlyCitationSupport", func() {
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Paths:   []string{"internal/workflows/productworkflow/coverage_gate.go"},
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go",
		Output:  "package productworkflow\nfunc EvaluateCoverageGate() {}\n",
	}}

	reason := runner.assessSourceGrounding(
		"The current source `internal/workflows/productworkflow/coverage_gate.go` defines the coverage gate behavior.",
		results,
	).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring("internal/workflows/productworkflow/coverage_gate.go"), "path-only metadata must not satisfy source-body citation support")
})

var _ = ginkgo.It("SourceGroundingRawBodyEvidenceSatisfiesCitationSupport", func() {
	runner, results := readOnlySourceGroundingWindowFixture("internal/workflows/productworkflow/coverage_gate.go", [2]int{1, 80})

	reason := runner.assessSourceGrounding(
		"The current source `internal/workflows/productworkflow/coverage_gate.go` defines the coverage gate behavior.",
		results,
	).Message()
	gomega.Expect(reason).To(gomega.BeEmpty(), "raw read_file source-body evidence should satisfy citation support")
})

var _ = ginkgo.It("SourceGroundingBasenameResolutionUsesResultBackedEvidence", func() {
	t := ginkgo.GinkgoT()
	writeFixture := func(root string, path string) {
		t.Helper()
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed(), "mkdir fixture path %q", path)
		gomega.Expect(os.WriteFile(fullPath, []byte("package app\nfunc targetBehavior() {}\n"), 0o644)).To(gomega.Succeed(), "write fixture path %q", path)
	}

	const uniquePath = "internal/app/target.go"
	root := t.TempDir()
	writeFixture(root, uniquePath)

	rawManager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	rawRunner := newRunnerRunFixture(&Runner{
		Workspace: rawManager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
	rawResult := rawManager.Execute(rawRunner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: uniquePath, StartLine: 1, EndLine: 20,
	})
	gomega.Expect(rawResult.Error).To(gomega.BeEmpty())
	rawResults := []workspace.ToolResult{rawResult}
	rawCitations := analyzeSourceCitations("`target.go:2` defines target behavior.")
	gomega.Expect(rawCitations.BodyReadableReferences()).To(gomega.HaveLen(1))
	resolvedPath, resolved := sourceCitationCanonicalPath(rawRunner, "target.go", newToolResultEvidenceProjection(rawResults))
	gomega.Expect(resolved).To(gomega.BeTrue())
	gomega.Expect(resolvedPath).To(gomega.Equal(uniquePath))
	gomega.Expect(sourceCitationBackedByCurrentWindow(
		rawRunner,
		resolvedPath,
		rawCitations.BodyReadableReferences()[0].LineRange,
		newToolResultEvidenceProjection(rawResults),
	)).To(gomega.BeTrue())
	rawReason := rawRunner.assessSourceGrounding("`target.go:2` defines target behavior.", rawResults).Message()
	gomega.Expect(rawReason).To(gomega.BeEmpty(), "unique basename backed by raw source-body evidence should be accepted")

	ambiguousRoot := t.TempDir()
	writeFixture(ambiguousRoot, uniquePath)
	writeFixture(ambiguousRoot, "internal/other/target.go")
	ambiguousRunner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: ambiguousRoot, CWD: ambiguousRoot}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	})
	ambiguousResults := []workspace.ToolResult{
		readFileSourceWindowResult(uniquePath, 1, 20, 0, "package app\nfunc targetBehavior() {}\n"),
		readFileSourceWindowResult("internal/other/target.go", 1, 20, 0, "package other\nfunc targetBehavior() {}\n"),
	}
	reason := ambiguousRunner.assessSourceGrounding("`target.go:2` defines target behavior.", ambiguousResults).Message()
	gomega.Expect(reason).To(gomega.ContainSubstring("target.go"), "ambiguous basename should remain unsupported")
})

var _ = ginkgo.It("SourceCitationLocalWindowReturnsEmptyWhenPathAbsent", func() {
	answer := "The current source `internal/app/current.go` defines the active behavior."

	gomega.Expect(sourceCitationLocalWindow(answer, "internal/app/missing.go", 80, 140)).To(gomega.BeEmpty())
})

var _ = ginkgo.It("SourceCitationClaimsDoNotBorrowTextFromAnotherCitation", func() {
	answer := "The current source `internal/app/current.go` defines the active behavior. Future work could add `internal/app/future.go`."

	gomega.Expect(sourceCitationClaimsCurrentSourcePath(answer, "internal/app/current.go")).To(gomega.BeTrue(), "current-source claim next to cited path should be detected")
	gomega.Expect(sourceCitationClaimsCurrentSourcePath(answer, "internal/app/missing.go")).To(gomega.BeFalse(), "absent path must not inherit current-source wording from another citation")
})
