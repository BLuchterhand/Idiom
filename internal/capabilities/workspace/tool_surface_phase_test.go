package workspace

import (
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace/externaltool"
	"idiom/internal/contracts/autonomy"
)

var _ = ginkgo.Describe("tool surface phase", func() {
	ginkgo.It("filter tool names preserves base order and uniqueness", func() {
		got := FilterToolNames(
			[]string{" search_content ", ToolNameReadFile, ToolNameSubmitPlan, ToolNameReadFile, ToolNameCodeNavigate},
			ToolNameSubmitPlan,
			ToolNameReadFile,
			ToolNameCodeNavigate,
		)
		want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameCodeNavigate}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("FilterToolNames() = %#v, want %#v", got, want))

	})
	ginkgo.It("tool surface phase disallowed tools owns phase complement", func() {
		got := ToolSurfacePhaseDisallowedTools(
			[]string{ToolNameSubmitPlan, ToolNameReadFile, ToolNameShell, ToolNameEditFile, ToolNameReadFile, ""},
			ToolSurfacePhasePlanExecuteSubmitPlanOnly,
		)
		want := []string{ToolNameReadFile, ToolNameShell, ToolNameEditFile}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("submit-ready phase disallowed tools = %#v, want %#v", got, want))

	})
})

func assertContainsTools(tools []string, required ...string) {
	ginkgo.GinkgoHelper()

	for _, tool := range required {
		gomega.Expect(slices.Contains(tools, tool)).To(gomega.BeTrue(), fmt.Sprintf("tools missing %q: %#v", tool, tools))

	}
}

func assertOmitsTools(tools []string, forbidden ...string) {
	ginkgo.GinkgoHelper()

	for _, tool := range forbidden {
		gomega.Expect(slices.Contains(tools, tool)).To(gomega.BeFalse(), fmt.Sprintf("tools must omit %q: %#v", tool, tools))

	}
}

var _ = ginkgo.Describe("tool surface phase", func() {
	ginkgo.It("implementation scoped edit allows submit plan", func() {
		gomega.Expect(ImplementationScopedEditToolAllowed(ToolNameSubmitPlan)).To(gomega.BeTrue(), fmt.Sprintf("scoped-edit surface must allow submit_plan; otherwise plan-execute lanes lose their write tool"))
		gomega.Expect(ToolSurfacePhaseAllowsTool(ToolNameSubmitPlan, ToolSurfacePhaseImplementationPreWrite)).To(gomega.BeTrue(), fmt.Sprintf("pre-write surface must allow submit_plan; otherwise exact-anchor pre-write hides the planner's write"))

		for _, tool := range []string{ToolNameEditFile, ToolNameApplyPatch, ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan, ToolNameContextQuery, "cpp_find_symbol", "cpp_definition", ToolNameSourceSanity} {
			got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{tool}, nil)
			gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("scoped-edit allowlist should pass %q through, got %v", tool, got))
			gomega.Expect(got[0]).To(gomega.Equal(tool), fmt.Sprintf("scoped-edit allowlist should pass %q through, got %v", tool, got))

		}

		for _, tool := range []string{ToolNameShell, ToolNameGitStatus, ToolNameTask, ToolNameSearchContent, ToolNameListFiles, ToolNameContextIndex} {
			got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{tool}, nil)
			gomega.Expect(got).To(gomega.BeEmpty(), fmt.Sprintf("scoped-edit allowlist should drop %q, got %v", tool, got))

		}

		reg, err := externaltool.NewRegistry([]externaltool.Tool{
			{Name: "trace_code", Command: "true", ReadOnly: true, Discovery: true},
			{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{"trace_code", "queue_run", ToolNameEditFile}, reg.IsReadOnlyDiscovery)
		want := []string{"trace_code", ToolNameEditFile}
		gomega.Expect(strings.Join(got, ",")).To(gomega.Equal(strings.Join(want, ",")), fmt.Sprintf("scoped-edit allowlist should keep declared read-only discovery externals only\nwant %v\n got %v", want, got))

	})
	ginkgo.It("implementation target source phase owns workspace projection", func() {
		base := []string{
			ToolNameShell,
			ToolNameWriteFile,
			ToolNameContextStatus,
			ToolNameSearchFiles,
			ToolNameReadFile,
			ToolNameSourceSanity,
			ToolNameHistoryRange,
			ToolNameCodeNavigate,
			ToolNameTask,
			ToolNameGoMoveDeclarations,
			ToolNameDiscoveryQuery,
			ToolNameSemgrepScan,
		}

		got := ToolSurfacePhaseAllowedTools(base, ToolSurfacePhaseImplementationTargetSource)
		assertContainsTools(got,
			ToolNameWriteFile,
			ToolNameSearchFiles,
			ToolNameReadFile,
			ToolNameHistoryRange,
			ToolNameCodeNavigate,
			ToolNameGoMoveDeclarations,
			ToolNameDiscoveryQuery,
		)
		assertOmitsTools(got, ToolNameShell, ToolNameSourceSanity, ToolNameTask, ToolNameSemgrepScan)
	})
	ginkgo.It("implementation target write ready phase owns workspace projection", func() {
		base := []string{
			ToolNameReadFile,
			ToolNameSearchFiles,
			ToolNameSearchContent,
			ToolNameContextQuery,
			ToolNameFileStats,
			ToolNameHistorySymbol,
			ToolNameEditFile,
			ToolNameShell,
			ToolNameSubmitPlan,
		}

		got := ToolSurfacePhaseAllowedTools(base, ToolSurfacePhaseImplementationTargetWriteReady)
		assertContainsTools(got,
			ToolNameReadFile,
			ToolNameSearchContent,
			ToolNameFileStats,
			ToolNameHistorySymbol,
			ToolNameEditFile,
		)
		assertOmitsTools(got, ToolNameSearchFiles, ToolNameContextQuery, ToolNameShell, ToolNameSubmitPlan)

		refresh := ImplementationTargetWriteReadySourceRefreshTools(base)
		assertContainsTools(refresh, ToolNameReadFile, ToolNameSearchContent, ToolNameFileStats)
		assertOmitsTools(refresh, ToolNameEditFile, ToolNameHistorySymbol, ToolNameSubmitPlan)
	})
	ginkgo.It("implementation verification allowed tools owns workspace projection", func() {
		base := []string{
			ToolNameReadFile,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameGitDiff,
			ToolNameGitStatus,
			ToolNameWriteFile,
			ToolNameEditFile,
			ToolNameAppendFile,
			ToolNameApplyPatch,
			ToolNameMkdir,
			ToolNameGoMoveDeclarations,
			ToolNameTaskLedger,
		}

		got := ToolSurfacePhaseAllowedTools(base, ToolSurfacePhaseImplementationVerificationReady)
		assertContainsTools(got,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameGitDiff,
			ToolNameGitStatus,
			ToolNameWriteFile,
			ToolNameEditFile,
			ToolNameAppendFile,
			ToolNameApplyPatch,
			ToolNameMkdir,
			ToolNameGoMoveDeclarations,
		)
		assertOmitsTools(got, ToolNameReadFile, ToolNameTaskLedger)
		gomega.Expect(ImplementationVerificationReadyHasWrite(got)).To(gomega.BeTrue(), fmt.Sprintf("verification-ready projection should recognize workspace write tools: %#v", got))
		gomega.Expect(ImplementationVerificationReadyHasWrite([]string{ToolNameSourceSanity, ToolNameShell})).To(gomega.BeFalse(), fmt.Sprintf("verification-only tools should not report write visibility"))

	})
	ginkgo.It("implementation verification repair phase owns workspace projection", func() {
		base := []string{
			ToolNameListFiles,
			ToolNameReadFile,
			ToolNameCodeNavigate,
			ToolNameContextQuery,
			ToolNameContextPack,
			ToolNameSearchContent,
			ToolNameEditFile,
			ToolNameAppendFile,
			ToolNameApplyPatch,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameGitStatus,
		}

		got := ToolSurfacePhaseAllowedTools(base, ToolSurfacePhaseImplementationVerificationRepair)
		assertContainsTools(got,
			ToolNameReadFile,
			ToolNameCodeNavigate,
			ToolNameContextQuery,
			ToolNameSearchContent,
			ToolNameEditFile,
			ToolNameAppendFile,
			ToolNameApplyPatch,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameGitStatus,
		)
		assertOmitsTools(got, ToolNameListFiles, ToolNameContextPack)
	})
	ginkgo.It("plan execute review repair initial phase allowed tools", func() {
		got := ToolSurfacePhaseAllowedToolsOrDefault([]string{
			ToolNameContextPack,
			ToolNameReadFile,
			ToolNameSubmitPlan,
			ToolNameSearchContent,
			ToolNameContextQuery,
			ToolNameReadFile,
		}, ToolSurfacePhasePlanExecuteReviewRepairInitial)
		want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameContextQuery}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("review-repair initial textual tools = %#v, want %#v", got, want))

		fallback := ToolSurfacePhaseAllowedToolsOrDefault(nil, ToolSurfacePhasePlanExecuteReviewRepairInitial)
		gomega.Expect(slices.Equal(fallback, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteReviewRepairInitial))).To(gomega.BeTrue(), fmt.Sprintf("empty available tools should fall back to the stable initial textual surface, got %#v", fallback))

	})
	ginkgo.It("plan execute phase tool names own static planning surfaces", func() {
		requiredSource := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource)
		assertContainsTools(requiredSource, ToolNameReadFile, ToolNameCodeNavigate)
		assertOmitsTools(requiredSource, ToolNameSubmitPlan, ToolNameEditFile, ToolNameSearchContent)

		anchorRefresh := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteAnchorRefresh)
		assertContainsTools(anchorRefresh, ToolNameReadFile, ToolNameCodeNavigate)
		assertOmitsTools(anchorRefresh, ToolNameSubmitPlan, ToolNameEditFile, ToolNameSearchContent)

		historyPending := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteHistoryPending)
		assertContainsTools(historyPending, ToolNameHistoryRange, ToolNameHistorySymbol)
		gomega.Expect(ToolSurfacePhaseAllowsTool(ToolNameBlameRange, ToolSurfacePhasePlanExecuteHistoryPending)).To(gomega.BeFalse(), fmt.Sprintf("history-pending surface must exclude blame_range; it lacks commit-description rationale"))

		if got := PlanExecuteHistoryPendingToolListForPrompt(); got != "`history_range` or `history_symbol`" {
			ginkgo.Fail(fmt.Sprintf("history-pending prompt list = %q", got))
		}

		candidate := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource)
		for _, want := range []string{ToolNameContextPack, ToolNameDiscoveryQuery, ToolNameSearchContent, ToolNameReadFile, ToolNameCodeNavigate, ToolNameProductStatus, ToolNameRecoveryStatus} {
			gomega.Expect(slices.Contains(candidate, want)).To(gomega.BeTrue(), fmt.Sprintf("candidate-source tools missing %q: %#v", want, candidate))

		}
		gomega.Expect(candidate).NotTo(gomega.ContainElement(ToolNameSubmitPlan), fmt.Sprintf("candidate-source surface must not expose submit_plan or per-edit writes: %#v", candidate))
		gomega.Expect(candidate).NotTo(gomega.ContainElement(ToolNameEditFile), fmt.Sprintf("candidate-source surface must not expose submit_plan or per-edit writes: %#v", candidate))

	})
	ginkgo.It("plan execute tool surface projection owns planning phase policy", func() {
		requiredSource := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteRequiredSource)
		gomega.Expect(slices.Equal(requiredSource.ParserVisibleTools, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource))).To(gomega.BeTrue(), fmt.Sprintf("required-source parser-visible tools = %#v, want phase tools", requiredSource.ParserVisibleTools))
		gomega.Expect(slices.Equal(requiredSource.CallableTools, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource))).To(gomega.BeTrue(), fmt.Sprintf("required-source callable tools = %#v, want phase tools", requiredSource.CallableTools))

		assertContainsTools(requiredSource.AllowedActionClasses,
			string(autonomy.ActionClassRead),
			string(autonomy.ActionClassSourceSatisfied),
			string(autonomy.ActionClassBlocker),
		)
		assertContainsTools(requiredSource.InvalidActionClasses,
			string(autonomy.ActionClassSubmitPlan),
			string(autonomy.ActionClassPerEditWrite),
			string(autonomy.ActionClassSearch),
		)
		gomega.Expect(slices.Equal(requiredSource.TerminalResponseShapes, PlanExecuteTerminalSourceSatisfiedOrQuestion())).To(gomega.BeTrue(), fmt.Sprintf("required-source terminal shapes = %#v, want source-satisfied/question", requiredSource.TerminalResponseShapes))

		submitReady := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteSubmitPlanOnly)
		assertContainsTools(submitReady.ParserVisibleTools, ToolNameSubmitPlan)
		assertContainsTools(submitReady.AllowedActionClasses,
			string(autonomy.ActionClassSubmitPlan),
			string(autonomy.ActionClassSourceSatisfied),
			string(autonomy.ActionClassBlocker),
		)
		assertContainsTools(submitReady.InvalidActionClasses,
			string(autonomy.ActionClassRead),
			string(autonomy.ActionClassSearch),
			string(autonomy.ActionClassVerify),
			string(autonomy.ActionClassPerEditWrite),
			string(autonomy.ActionClassGenericProbe),
			string(autonomy.ActionClassProseFinal),
		)
		gomega.Expect(slices.Equal(submitReady.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion())).To(gomega.BeTrue(), fmt.Sprintf("submit-ready terminal shapes = %#v, want submit/source-satisfied/question", submitReady.TerminalResponseShapes))

	})
	ginkgo.It("plan execute candidate projection submit terminal widening", func() {
		base := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteCandidateSource)
		widened := base.WithSubmitPlanTerminal()

		assertOmitsTools(base.ParserVisibleTools, ToolNameSubmitPlan)
		assertOmitsTools(base.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
		assertContainsTools(base.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan))

		assertContainsTools(widened.ParserVisibleTools, ToolNameSubmitPlan)
		assertContainsTools(widened.CallableTools, ToolNameSubmitPlan)
		assertContainsTools(widened.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
		assertOmitsTools(widened.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan))
		assertContainsTools(widened.TerminalResponseShapes, PlanExecuteTerminalSubmitPlan)
	})
	ginkgo.It("plan execute submit correction projection owns terminal policy", func() {
		blocking := PlanExecuteSubmitCorrectionProjection(true)
		assertContainsTools(blocking.AllowedActionClasses,
			string(autonomy.ActionClassSubmitPlan),
			string(autonomy.ActionClassBlocker),
		)
		assertContainsTools(blocking.InvalidActionClasses, string(autonomy.ActionClassSourceSatisfied))
		gomega.Expect(slices.Equal(blocking.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanQuestion())).To(gomega.BeTrue(), fmt.Sprintf("blocker-allowed correction terminal shapes = %#v, want submit/question", blocking.TerminalResponseShapes))

		strict := PlanExecuteSubmitCorrectionProjection(false)
		assertContainsTools(strict.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
		assertOmitsTools(strict.AllowedActionClasses, string(autonomy.ActionClassBlocker))
		assertContainsTools(strict.InvalidActionClasses,
			string(autonomy.ActionClassSourceSatisfied),
			string(autonomy.ActionClassBlocker),
		)
		gomega.Expect(slices.Equal(strict.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanOnly())).To(gomega.BeTrue(), fmt.Sprintf("strict correction terminal shapes = %#v, want submit only", strict.TerminalResponseShapes))

		native := blocking.WithNativeSubmitPlanOnly()
		gomega.Expect(slices.Equal(native.AllowedActionClasses, []string{string(autonomy.ActionClassSubmitPlan)})).To(gomega.BeTrue(), fmt.Sprintf("native correction allowed action classes = %#v, want submit only", native.AllowedActionClasses))
		gomega.Expect(slices.Equal(native.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanOnly())).To(gomega.BeTrue(), fmt.Sprintf("native correction terminal shapes = %#v, want submit only", native.TerminalResponseShapes))

		assertContainsTools(native.InvalidActionClasses, string(autonomy.ActionClassSourceSatisfied))
	})
	ginkgo.It("keeps permission authorized inspection additive across non verification planner phases", func() {
		snapshot := ToolSurfaceSnapshot{
			allowedTools:              []string{ToolNameReadFile, ToolNameSearchContent, ToolNameShell, "project_trace", ToolNameEditFile},
			externalReadOnlyDiscovery: map[string]struct{}{"project_trace": {}},
		}
		inspection := snapshot.PermissionAuthorizedInspection([]string{ToolNameReadFile, ToolNameSearchContent, ToolNameShell, "project_trace", ToolNameEditFile})
		gomega.Expect(inspection.ActionClasses()).To(gomega.ContainElements(
			string(autonomy.ActionClassRead),
			string(autonomy.ActionClassSearch),
			string(autonomy.ActionClassVerify),
		))

		for _, phase := range []ToolSurfacePhase{
			ToolSurfacePhasePlanExecuteRequiredSource,
			ToolSurfacePhasePlanExecuteHistoryPending,
			ToolSurfacePhasePlanExecuteAnchorRefresh,
			ToolSurfacePhasePlanExecuteSubmitPlanOnly,
		} {
			projection := PlanExecuteToolSurfaceProjectionForPhase(phase).WithPermissionAuthorizedInspection(inspection)
			assertContainsTools(projection.ParserVisibleTools, ToolNameReadFile, ToolNameSearchContent, ToolNameShell, "project_trace")
			assertContainsTools(projection.CallableTools, ToolNameReadFile, ToolNameSearchContent, ToolNameShell, "project_trace")
			assertOmitsTools(projection.ParserVisibleTools, ToolNameEditFile)
			assertContainsTools(projection.AllowedActionClasses,
				string(autonomy.ActionClassRead),
				string(autonomy.ActionClassSearch),
				string(autonomy.ActionClassVerify),
			)
			assertOmitsTools(projection.InvalidActionClasses,
				string(autonomy.ActionClassRead),
				string(autonomy.ActionClassSearch),
				string(autonomy.ActionClassVerify),
			)
		}

		correction := PlanExecuteSubmitCorrectionProjection(false).WithPermissionAuthorizedInspection(inspection)
		assertContainsTools(correction.CallableTools, ToolNameReadFile, ToolNameSearchContent, ToolNameShell, "project_trace")
		assertContainsTools(correction.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
		assertOmitsTools(correction.InvalidActionClasses, string(autonomy.ActionClassRead), string(autonomy.ActionClassSearch), string(autonomy.ActionClassVerify))
		gomega.Expect(correction.TerminalResponseShapes).To(gomega.Equal(PlanExecuteTerminalSubmitPlanOnly()))
	})
	ginkgo.It("projects shell-only inspection through its concrete non-mutating action classes", func() {
		snapshot := ToolSurfaceSnapshot{allowedTools: []string{ToolNameShell}}
		inspection := snapshot.PermissionAuthorizedInspection([]string{ToolNameShell})
		gomega.Expect(inspection.ActionClasses()).To(gomega.ConsistOf(
			string(autonomy.ActionClassRead),
			string(autonomy.ActionClassVerify),
		))
		gomega.Expect(inspection.AllowsRequest(ToolRequest{Tool: ToolNameShell, Command: "rg -n TODO internal"})).To(gomega.BeTrue())
		gomega.Expect(inspection.AllowsRequest(ToolRequest{Tool: ToolNameShell, Command: "go test ./internal/contracts/..."})).To(gomega.BeTrue())
		gomega.Expect(inspection.AllowsRequest(ToolRequest{Tool: ToolNameShell, Command: "touch unexpected.txt"})).To(gomega.BeFalse())

		for _, phase := range []ToolSurfacePhase{
			ToolSurfacePhasePlanExecuteRequiredSource,
			ToolSurfacePhasePlanExecuteHistoryPending,
			ToolSurfacePhasePlanExecuteAnchorRefresh,
			ToolSurfacePhasePlanExecuteSubmitPlanOnly,
		} {
			projection := PlanExecuteToolSurfaceProjectionForPhase(phase).WithPermissionAuthorizedInspection(inspection)
			assertContainsTools(projection.CallableTools, ToolNameShell)
			assertContainsTools(projection.AllowedActionClasses,
				string(autonomy.ActionClassRead),
				string(autonomy.ActionClassVerify),
			)
			assertOmitsTools(projection.InvalidActionClasses,
				string(autonomy.ActionClassRead),
				string(autonomy.ActionClassVerify),
			)
		}
	})
	ginkgo.It("plan execute submit ready disallowed tools derive from workspace surfaces", func() {
		got := PlanExecuteSubmitReadyDisallowedTools([]string{
			ToolNameSubmitPlan,
			ToolNameReadFile,
			ToolNameSourceSanity,
			ToolNameEditFile,
			"project_trace",
		})

		assertContainsTools(got,
			ToolNameReadFile,
			ToolNameSearchContent,
			ToolNameCodeNavigate,
			ToolNameHistoryRange,
			ToolNameSourceSanity,
			ToolNameShell,
			ToolNameEditFile,
			ToolNameApplyPatch,
			ToolNameMkdir,
			"project_trace",
		)
		assertOmitsTools(got, ToolNameSubmitPlan)

		for _, tool := range WorkspaceWriteToolNames() {
			gomega.Expect(slices.Contains(got, tool)).To(gomega.BeTrue(), fmt.Sprintf("submit-ready disallowed tools should include cataloged write tool %q: %#v", tool, got))

		}
	})
	ginkgo.It("plan execute review repair terminal phase allows probe tools", func() {
		for _, tool := range []string{ToolNameReadFile, ToolNameSearchFiles, ToolNameContextImpact, ToolNameDiscoveryQuery, ToolNameStructuralSearch} {
			gomega.Expect(ToolSurfacePhaseAllowsTool(tool, ToolSurfacePhasePlanExecuteReviewRepairTerminal)).To(gomega.BeTrue(), fmt.Sprintf("review-repair terminal probe should allow %q", tool))

		}

		for _, tool := range []string{ToolNameSubmitPlan, ToolNameEditFile, ToolNameSourceSanity, ToolNameShell, ToolNameHistoryRange} {
			gomega.Expect(ToolSurfacePhaseAllowsTool(tool, ToolSurfacePhasePlanExecuteReviewRepairTerminal)).To(gomega.BeFalse(), fmt.Sprintf("review-repair terminal probe should reject %q", tool))

		}
	})
	ginkgo.It("plan execute textual projection allowed tools prefers earlier projection", func() {
		allowed := PlanExecuteTextualProjectionAllowedTools(
			[]string{ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan},
			[]string{ToolNameSubmitPlan},
			[]string{ToolNameReadFile, ToolNameCodeNavigate},
		)
		gomega.Expect(slices.Equal(allowed, []string{ToolNameSubmitPlan})).To(gomega.BeTrue(), fmt.Sprintf("submit-ready projection should override stale read projection, got %#v", allowed))

		allowed = PlanExecuteTextualProjectionAllowedTools(
			[]string{ToolNameReadFile, ToolNameCodeNavigate},
			[]string{ToolNameSubmitPlan},
			[]string{ToolNameCodeNavigate, ToolNameReadFile},
		)
		gomega.Expect(slices.Equal(allowed, []string{ToolNameReadFile, ToolNameCodeNavigate})).To(gomega.BeTrue(), fmt.Sprintf("projection should fall through when the first projection is unavailable in base tools, got %#v", allowed))

	})
	ginkgo.It("plan execute textual allowed tools owns planner fallback policy", func() {
		allowed := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
			TaskFamily:          string(autonomy.TaskFamilyPlanThenExecutePlanner),
			CurrentAllowedTools: []string{ToolNameReadFile, ToolNameSubmitPlan},
			BaseTools:           []string{ToolNameReadFile, ToolNameSubmitPlan},
			CallableTools:       []string{ToolNameSubmitPlan},
			ParserVisibleTools:  []string{ToolNameReadFile},
		})
		gomega.Expect(slices.Equal(allowed, []string{ToolNameSubmitPlan})).To(gomega.BeTrue(), fmt.Sprintf("planner textual surface should prefer callable projection, got %#v", allowed))

		allowed = PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
			TaskFamily:                       string(autonomy.TaskFamilyPlanThenExecutePlanner),
			TerminalSubmitPlanAllowed:        true,
			SourceSatisfiedTerminalComplete:  false,
			CurrentAllowedTools:              nil,
			ReviewRepairTerminalAllowedTools: []string{ToolNameReadFile},
		})
		gomega.Expect(slices.Equal(allowed, []string{ToolNameSubmitPlan})).To(gomega.BeTrue(), fmt.Sprintf("planner terminal fallback should collapse to submit_plan, got %#v", allowed))

		if allowed = PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
			TaskFamily:                      string(autonomy.TaskFamilyPlanThenExecutePlanner),
			CurrentAllowedTools:             []string{ToolNameReadFile},
			SourceSatisfiedTerminalComplete: true,
		}); len(allowed) != 0 {
			ginkgo.Fail(fmt.Sprintf("source-satisfied terminal contract should expose no tools, got %#v", allowed))
		}
	})
	ginkgo.It("plan execute textual allowed tools owns review repair policy", func() {
		initial := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
			TaskFamily:               string(autonomy.TaskFamilyPlanThenExecuteReview),
			BriefAllowedTools:        []string{ToolNameReadFile, ToolNameSearchContent, ToolNameSubmitPlan},
			CurrentAllowedTools:      []string{ToolNameSubmitPlan},
			ReviewRepairInitialProbe: true,
		})
		assertContainsTools(initial, ToolNameReadFile, ToolNameSubmitPlan)
		assertOmitsTools(initial, ToolNameSearchContent)

		terminal := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
			TaskFamily:                       string(autonomy.TaskFamilyPlanThenExecuteReview),
			CurrentAllowedTools:              []string{ToolNameReadFile},
			ReviewRepairTerminalAllowedTools: []string{ToolNameSubmitPlan},
		})
		gomega.Expect(slices.Equal(terminal, []string{ToolNameSubmitPlan})).To(gomega.BeTrue(), fmt.Sprintf("review-repair terminal policy should use terminal contract tools, got %#v", terminal))

	})
	ginkgo.It("plan execute textual allowed tools for request widens only parser visible base tools", func() {
		allowed := PlanExecuteTextualAllowedToolsForRequest(
			[]string{ToolNameReadFile},
			[]string{ToolNameReadFile, ToolNameCodeNavigate},
			[]string{ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan},
			ToolNameCodeNavigate,
		)
		gomega.Expect(slices.Equal(allowed, []string{ToolNameReadFile, ToolNameCodeNavigate})).To(gomega.BeTrue(), fmt.Sprintf("parser-visible base tool should widen allowed surface, got %#v", allowed))

		hidden := PlanExecuteTextualAllowedToolsForRequest(
			[]string{ToolNameReadFile},
			[]string{ToolNameReadFile},
			[]string{ToolNameReadFile, ToolNameCodeNavigate},
			ToolNameCodeNavigate,
		)
		gomega.Expect(slices.Equal(hidden, []string{ToolNameReadFile})).To(gomega.BeTrue(), fmt.Sprintf("non-parser-visible tool should not widen allowed surface, got %#v", hidden))

		missingFromBase := PlanExecuteTextualAllowedToolsForRequest(
			[]string{ToolNameReadFile},
			[]string{ToolNameReadFile, ToolNameCodeNavigate},
			[]string{ToolNameReadFile},
			ToolNameCodeNavigate,
		)
		gomega.Expect(slices.Equal(missingFromBase, []string{ToolNameReadFile})).To(gomega.BeTrue(), fmt.Sprintf("parser-visible tool outside base surface should not widen allowed surface, got %#v", missingFromBase))

		submitOnly := PlanExecuteTextualAllowedToolsForRequest(
			[]string{ToolNameSubmitPlan},
			[]string{ToolNameSubmitPlan, ToolNameCodeNavigate},
			[]string{ToolNameSubmitPlan, ToolNameCodeNavigate},
			ToolNameCodeNavigate,
		)
		gomega.Expect(slices.Equal(submitOnly, []string{ToolNameSubmitPlan})).To(gomega.BeTrue(), fmt.Sprintf("submit-only terminal surface should not reopen hidden navigation, got %#v", submitOnly))

	})
})
