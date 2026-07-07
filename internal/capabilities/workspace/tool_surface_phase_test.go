package workspace

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestFilterToolNamesPreservesBaseOrderAndUniqueness(t *testing.T) {
	got := FilterToolNames(
		[]string{" search_content ", ToolNameReadFile, ToolNameSubmitPlan, ToolNameReadFile, ToolNameCodeNavigate},
		ToolNameSubmitPlan,
		ToolNameReadFile,
		ToolNameCodeNavigate,
	)
	want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameCodeNavigate}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterToolNames() = %#v, want %#v", got, want)
	}
}

func TestToolSurfacePhaseDisallowedToolsOwnsPhaseComplement(t *testing.T) {
	got := ToolSurfacePhaseDisallowedTools(
		[]string{ToolNameSubmitPlan, ToolNameReadFile, ToolNameShell, ToolNameEditFile, ToolNameReadFile, ""},
		ToolSurfacePhasePlanExecuteSubmitPlanOnly,
	)
	want := []string{ToolNameReadFile, ToolNameShell, ToolNameEditFile}
	if !slices.Equal(got, want) {
		t.Fatalf("submit-ready phase disallowed tools = %#v, want %#v", got, want)
	}
}

func assertContainsTools(t *testing.T, tools []string, required ...string) {
	t.Helper()
	for _, tool := range required {
		if !slices.Contains(tools, tool) {
			t.Fatalf("tools missing %q: %#v", tool, tools)
		}
	}
}

func assertOmitsTools(t *testing.T, tools []string, forbidden ...string) {
	t.Helper()
	for _, tool := range forbidden {
		if slices.Contains(tools, tool) {
			t.Fatalf("tools must omit %q: %#v", tool, tools)
		}
	}
}

func TestImplementationScopedEditAllowsSubmitPlan(t *testing.T) {
	if !ImplementationScopedEditToolAllowed(ToolNameSubmitPlan) {
		t.Fatalf("scoped-edit surface must allow submit_plan; otherwise plan-execute lanes lose their write tool")
	}

	if !ToolSurfacePhaseAllowsTool(ToolNameSubmitPlan, ToolSurfacePhaseImplementationPreWrite) {
		t.Fatalf("pre-write surface must allow submit_plan; otherwise exact-anchor pre-write hides the planner's write")
	}

	for _, tool := range []string{ToolNameEditFile, ToolNameApplyPatch, ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan, ToolNameContextQuery, "cpp_find_symbol", "cpp_definition", ToolNameSourceSanity} {
		got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{tool}, nil)
		if len(got) != 1 || got[0] != tool {
			t.Fatalf("scoped-edit allowlist should pass %q through, got %v", tool, got)
		}
	}

	for _, tool := range []string{ToolNameShell, ToolNameGitStatus, ToolNameTask, ToolNameSearchContent, ToolNameListFiles, ToolNameContextIndex} {
		got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{tool}, nil)
		if len(got) != 0 {
			t.Fatalf("scoped-edit allowlist should drop %q, got %v", tool, got)
		}
	}

	reg, err := NewExternalToolRegistry([]ExternalTool{
		{Name: "trace_code", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	got := ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery([]string{"trace_code", "queue_run", ToolNameEditFile}, reg.IsReadOnlyDiscovery)
	want := []string{"trace_code", ToolNameEditFile}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scoped-edit allowlist should keep declared read-only discovery externals only\nwant %v\n got %v", want, got)
	}
}

func TestImplementationTargetSourcePhaseOwnsWorkspaceProjection(t *testing.T) {
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
	assertContainsTools(t, got,
		ToolNameWriteFile,
		ToolNameSearchFiles,
		ToolNameReadFile,
		ToolNameHistoryRange,
		ToolNameCodeNavigate,
		ToolNameGoMoveDeclarations,
		ToolNameDiscoveryQuery,
	)
	assertOmitsTools(t, got, ToolNameShell, ToolNameSourceSanity, ToolNameTask, ToolNameSemgrepScan)
}

func TestImplementationTargetWriteReadyPhaseOwnsWorkspaceProjection(t *testing.T) {
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
	assertContainsTools(t, got,
		ToolNameReadFile,
		ToolNameSearchContent,
		ToolNameFileStats,
		ToolNameHistorySymbol,
		ToolNameEditFile,
	)
	assertOmitsTools(t, got, ToolNameSearchFiles, ToolNameContextQuery, ToolNameShell, ToolNameSubmitPlan)

	refresh := ImplementationTargetWriteReadySourceRefreshTools(base)
	assertContainsTools(t, refresh, ToolNameReadFile, ToolNameSearchContent, ToolNameFileStats)
	assertOmitsTools(t, refresh, ToolNameEditFile, ToolNameHistorySymbol, ToolNameSubmitPlan)
}

func TestImplementationVerificationAllowedToolsOwnsWorkspaceProjection(t *testing.T) {
	base := []string{
		ToolNameReadFile,
		ToolNameSourceSanity,
		ToolNameShell,
		ToolNameGitDiff,
		ToolNameGitStatus,
		ToolNameWriteFile,
		ToolNameEditFile,
		ToolNameApplyPatch,
		ToolNameMkdir,
		ToolNameTaskLedger,
	}

	got := ToolSurfacePhaseAllowedTools(base, ToolSurfacePhaseImplementationVerificationReady)
	assertContainsTools(t, got,
		ToolNameSourceSanity,
		ToolNameShell,
		ToolNameGitDiff,
		ToolNameGitStatus,
		ToolNameWriteFile,
		ToolNameApplyPatch,
		ToolNameMkdir,
	)
	assertOmitsTools(t, got, ToolNameReadFile, ToolNameEditFile, ToolNameTaskLedger)

	if !ImplementationVerificationReadyHasAdditiveWrite(got) {
		t.Fatalf("verification-ready projection should recognize additive write tools: %#v", got)
	}

	if ImplementationVerificationReadyHasAdditiveWrite([]string{ToolNameSourceSanity, ToolNameShell}) {
		t.Fatalf("verification-only tools should not report additive write visibility")
	}
}

func TestImplementationVerificationRepairPhaseOwnsWorkspaceProjection(t *testing.T) {
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
	assertContainsTools(t, got,
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
	assertOmitsTools(t, got, ToolNameListFiles, ToolNameContextPack)
}

func TestPlanExecuteReviewRepairInitialPhaseAllowedTools(t *testing.T) {
	got := ToolSurfacePhaseAllowedToolsOrDefault([]string{
		ToolNameContextPack,
		ToolNameReadFile,
		ToolNameSubmitPlan,
		ToolNameSearchContent,
		ToolNameContextQuery,
		ToolNameReadFile,
	}, ToolSurfacePhasePlanExecuteReviewRepairInitial)
	want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameContextQuery}
	if !slices.Equal(got, want) {
		t.Fatalf("review-repair initial textual tools = %#v, want %#v", got, want)
	}

	fallback := ToolSurfacePhaseAllowedToolsOrDefault(nil, ToolSurfacePhasePlanExecuteReviewRepairInitial)
	if !slices.Equal(fallback, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteReviewRepairInitial)) {
		t.Fatalf("empty available tools should fall back to the stable initial textual surface, got %#v", fallback)
	}
}

func TestPlanExecutePhaseToolNamesOwnStaticPlanningSurfaces(t *testing.T) {
	requiredSource := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource)
	assertContainsTools(t, requiredSource, ToolNameReadFile, ToolNameCodeNavigate)
	assertOmitsTools(t, requiredSource, ToolNameSubmitPlan, ToolNameEditFile, ToolNameSearchContent)

	anchorRefresh := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteAnchorRefresh)
	assertContainsTools(t, anchorRefresh, ToolNameReadFile, ToolNameCodeNavigate)
	assertOmitsTools(t, anchorRefresh, ToolNameSubmitPlan, ToolNameEditFile, ToolNameSearchContent)

	historyPending := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteHistoryPending)
	assertContainsTools(t, historyPending, ToolNameHistoryRange, ToolNameHistorySymbol)
	if ToolSurfacePhaseAllowsTool(ToolNameBlameRange, ToolSurfacePhasePlanExecuteHistoryPending) {
		t.Fatalf("history-pending surface must exclude blame_range; it lacks commit-description rationale")
	}
	if got := PlanExecuteHistoryPendingToolListForPrompt(); got != "`history_range` or `history_symbol`" {
		t.Fatalf("history-pending prompt list = %q", got)
	}

	candidate := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource)
	for _, want := range []string{ToolNameContextPack, ToolNameDiscoveryQuery, ToolNameSearchContent, ToolNameReadFile, ToolNameCodeNavigate, ToolNameProductStatus, ToolNameRecoveryStatus} {
		if !slices.Contains(candidate, want) {
			t.Fatalf("candidate-source tools missing %q: %#v", want, candidate)
		}
	}

	if slices.Contains(candidate, ToolNameSubmitPlan) || slices.Contains(candidate, ToolNameEditFile) {
		t.Fatalf("candidate-source surface must not expose submit_plan or per-edit writes: %#v", candidate)
	}
}

func TestPlanExecuteToolSurfaceProjectionOwnsPlanningPhasePolicy(t *testing.T) {
	requiredSource := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteRequiredSource)
	if !slices.Equal(requiredSource.ParserVisibleTools, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource)) {
		t.Fatalf("required-source parser-visible tools = %#v, want phase tools", requiredSource.ParserVisibleTools)
	}
	if !slices.Equal(requiredSource.CallableTools, ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource)) {
		t.Fatalf("required-source callable tools = %#v, want phase tools", requiredSource.CallableTools)
	}
	assertContainsTools(t, requiredSource.AllowedActionClasses,
		string(autonomy.ActionClassRead),
		string(autonomy.ActionClassSourceSatisfied),
		string(autonomy.ActionClassBlocker),
	)
	assertContainsTools(t, requiredSource.InvalidActionClasses,
		string(autonomy.ActionClassSubmitPlan),
		string(autonomy.ActionClassPerEditWrite),
		string(autonomy.ActionClassSearch),
	)
	if !slices.Equal(requiredSource.TerminalResponseShapes, PlanExecuteTerminalSourceSatisfiedOrQuestion()) {
		t.Fatalf("required-source terminal shapes = %#v, want source-satisfied/question", requiredSource.TerminalResponseShapes)
	}

	submitReady := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteSubmitPlanOnly)
	assertContainsTools(t, submitReady.ParserVisibleTools, ToolNameSubmitPlan)
	assertContainsTools(t, submitReady.AllowedActionClasses,
		string(autonomy.ActionClassSubmitPlan),
		string(autonomy.ActionClassSourceSatisfied),
		string(autonomy.ActionClassBlocker),
	)
	assertContainsTools(t, submitReady.InvalidActionClasses,
		string(autonomy.ActionClassRead),
		string(autonomy.ActionClassSearch),
		string(autonomy.ActionClassVerify),
		string(autonomy.ActionClassPerEditWrite),
		string(autonomy.ActionClassGenericProbe),
		string(autonomy.ActionClassProseFinal),
	)
	if !slices.Equal(submitReady.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion()) {
		t.Fatalf("submit-ready terminal shapes = %#v, want submit/source-satisfied/question", submitReady.TerminalResponseShapes)
	}
}

func TestPlanExecuteCandidateProjectionSubmitTerminalWidening(t *testing.T) {
	base := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteCandidateSource)
	widened := base.WithSubmitPlanTerminal()

	assertOmitsTools(t, base.ParserVisibleTools, ToolNameSubmitPlan)
	assertOmitsTools(t, base.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
	assertContainsTools(t, base.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan))

	assertContainsTools(t, widened.ParserVisibleTools, ToolNameSubmitPlan)
	assertContainsTools(t, widened.CallableTools, ToolNameSubmitPlan)
	assertContainsTools(t, widened.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
	assertOmitsTools(t, widened.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan))
	assertContainsTools(t, widened.TerminalResponseShapes, PlanExecuteTerminalSubmitPlan)
}

func TestPlanExecuteSubmitCorrectionProjectionOwnsTerminalPolicy(t *testing.T) {
	blocking := PlanExecuteSubmitCorrectionProjection(true)
	assertContainsTools(t, blocking.AllowedActionClasses,
		string(autonomy.ActionClassSubmitPlan),
		string(autonomy.ActionClassBlocker),
	)
	assertContainsTools(t, blocking.InvalidActionClasses, string(autonomy.ActionClassSourceSatisfied))
	if !slices.Equal(blocking.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanQuestion()) {
		t.Fatalf("blocker-allowed correction terminal shapes = %#v, want submit/question", blocking.TerminalResponseShapes)
	}

	strict := PlanExecuteSubmitCorrectionProjection(false)
	assertContainsTools(t, strict.AllowedActionClasses, string(autonomy.ActionClassSubmitPlan))
	assertOmitsTools(t, strict.AllowedActionClasses, string(autonomy.ActionClassBlocker))
	assertContainsTools(t, strict.InvalidActionClasses,
		string(autonomy.ActionClassSourceSatisfied),
		string(autonomy.ActionClassBlocker),
	)
	if !slices.Equal(strict.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanOnly()) {
		t.Fatalf("strict correction terminal shapes = %#v, want submit only", strict.TerminalResponseShapes)
	}

	native := blocking.WithNativeSubmitPlanOnly()
	if !slices.Equal(native.AllowedActionClasses, []string{string(autonomy.ActionClassSubmitPlan)}) {
		t.Fatalf("native correction allowed action classes = %#v, want submit only", native.AllowedActionClasses)
	}
	if !slices.Equal(native.TerminalResponseShapes, PlanExecuteTerminalSubmitPlanOnly()) {
		t.Fatalf("native correction terminal shapes = %#v, want submit only", native.TerminalResponseShapes)
	}
	assertContainsTools(t, native.InvalidActionClasses, string(autonomy.ActionClassSourceSatisfied))
}

func TestPlanExecuteSubmitReadyDisallowedToolsDeriveFromWorkspaceSurfaces(t *testing.T) {
	got := PlanExecuteSubmitReadyDisallowedTools([]string{
		ToolNameSubmitPlan,
		ToolNameReadFile,
		ToolNameSourceSanity,
		ToolNameEditFile,
		"project_trace",
	})

	assertContainsTools(t, got,
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
	assertOmitsTools(t, got, ToolNameSubmitPlan)

	for _, tool := range WorkspaceWriteToolNames() {
		if !slices.Contains(got, tool) {
			t.Fatalf("submit-ready disallowed tools should include cataloged write tool %q: %#v", tool, got)
		}
	}
}

func TestPlanExecuteReviewRepairTerminalPhaseAllowsProbeTools(t *testing.T) {
	for _, tool := range []string{ToolNameReadFile, ToolNameSearchFiles, ToolNameContextImpact, ToolNameDiscoveryQuery, ToolNameStructuralSearch} {
		if !ToolSurfacePhaseAllowsTool(tool, ToolSurfacePhasePlanExecuteReviewRepairTerminal) {
			t.Fatalf("review-repair terminal probe should allow %q", tool)
		}
	}

	for _, tool := range []string{ToolNameSubmitPlan, ToolNameEditFile, ToolNameSourceSanity, ToolNameShell, ToolNameHistoryRange} {
		if ToolSurfacePhaseAllowsTool(tool, ToolSurfacePhasePlanExecuteReviewRepairTerminal) {
			t.Fatalf("review-repair terminal probe should reject %q", tool)
		}
	}
}

func TestPlanExecuteTextualProjectionAllowedToolsPrefersEarlierProjection(t *testing.T) {
	allowed := PlanExecuteTextualProjectionAllowedTools(
		[]string{ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan},
		[]string{ToolNameSubmitPlan},
		[]string{ToolNameReadFile, ToolNameCodeNavigate},
	)
	if !slices.Equal(allowed, []string{ToolNameSubmitPlan}) {
		t.Fatalf("submit-ready projection should override stale read projection, got %#v", allowed)
	}

	allowed = PlanExecuteTextualProjectionAllowedTools(
		[]string{ToolNameReadFile, ToolNameCodeNavigate},
		[]string{ToolNameSubmitPlan},
		[]string{ToolNameCodeNavigate, ToolNameReadFile},
	)
	if !slices.Equal(allowed, []string{ToolNameReadFile, ToolNameCodeNavigate}) {
		t.Fatalf("projection should fall through when the first projection is unavailable in base tools, got %#v", allowed)
	}
}

func TestPlanExecuteTextualAllowedToolsOwnsPlannerFallbackPolicy(t *testing.T) {
	allowed := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
		TaskFamily:          string(autonomy.TaskFamilyPlanThenExecutePlanner),
		CurrentAllowedTools: []string{ToolNameReadFile, ToolNameSubmitPlan},
		BaseTools:           []string{ToolNameReadFile, ToolNameSubmitPlan},
		CallableTools:       []string{ToolNameSubmitPlan},
		ParserVisibleTools:  []string{ToolNameReadFile},
	})
	if !slices.Equal(allowed, []string{ToolNameSubmitPlan}) {
		t.Fatalf("planner textual surface should prefer callable projection, got %#v", allowed)
	}

	allowed = PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
		TaskFamily:                       string(autonomy.TaskFamilyPlanThenExecutePlanner),
		TerminalSubmitPlanAllowed:        true,
		SourceSatisfiedTerminalComplete:  false,
		CurrentAllowedTools:              nil,
		ReviewRepairTerminalAllowedTools: []string{ToolNameReadFile},
	})
	if !slices.Equal(allowed, []string{ToolNameSubmitPlan}) {
		t.Fatalf("planner terminal fallback should collapse to submit_plan, got %#v", allowed)
	}

	if allowed = PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
		TaskFamily:                      string(autonomy.TaskFamilyPlanThenExecutePlanner),
		CurrentAllowedTools:             []string{ToolNameReadFile},
		SourceSatisfiedTerminalComplete: true,
	}); len(allowed) != 0 {
		t.Fatalf("source-satisfied terminal contract should expose no tools, got %#v", allowed)
	}
}

func TestPlanExecuteTextualAllowedToolsOwnsReviewRepairPolicy(t *testing.T) {
	initial := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
		TaskFamily:               string(autonomy.TaskFamilyPlanThenExecuteReview),
		BriefAllowedTools:        []string{ToolNameReadFile, ToolNameSearchContent, ToolNameSubmitPlan},
		CurrentAllowedTools:      []string{ToolNameSubmitPlan},
		ReviewRepairInitialProbe: true,
	})
	assertContainsTools(t, initial, ToolNameReadFile, ToolNameSubmitPlan)
	assertOmitsTools(t, initial, ToolNameSearchContent)

	terminal := PlanExecuteTextualAllowedTools(PlanExecuteTextualAllowedToolsInput{
		TaskFamily:                       string(autonomy.TaskFamilyPlanThenExecuteReview),
		CurrentAllowedTools:              []string{ToolNameReadFile},
		ReviewRepairTerminalAllowedTools: []string{ToolNameSubmitPlan},
	})
	if !slices.Equal(terminal, []string{ToolNameSubmitPlan}) {
		t.Fatalf("review-repair terminal policy should use terminal contract tools, got %#v", terminal)
	}
}

func TestPlanExecuteTextualAllowedToolsForRequestWidensOnlyParserVisibleBaseTools(t *testing.T) {
	allowed := PlanExecuteTextualAllowedToolsForRequest(
		[]string{ToolNameReadFile},
		[]string{ToolNameReadFile, ToolNameCodeNavigate},
		[]string{ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan},
		ToolNameCodeNavigate,
	)
	if !slices.Equal(allowed, []string{ToolNameReadFile, ToolNameCodeNavigate}) {
		t.Fatalf("parser-visible base tool should widen allowed surface, got %#v", allowed)
	}

	hidden := PlanExecuteTextualAllowedToolsForRequest(
		[]string{ToolNameReadFile},
		[]string{ToolNameReadFile},
		[]string{ToolNameReadFile, ToolNameCodeNavigate},
		ToolNameCodeNavigate,
	)
	if !slices.Equal(hidden, []string{ToolNameReadFile}) {
		t.Fatalf("non-parser-visible tool should not widen allowed surface, got %#v", hidden)
	}

	missingFromBase := PlanExecuteTextualAllowedToolsForRequest(
		[]string{ToolNameReadFile},
		[]string{ToolNameReadFile, ToolNameCodeNavigate},
		[]string{ToolNameReadFile},
		ToolNameCodeNavigate,
	)
	if !slices.Equal(missingFromBase, []string{ToolNameReadFile}) {
		t.Fatalf("parser-visible tool outside base surface should not widen allowed surface, got %#v", missingFromBase)
	}

	submitOnly := PlanExecuteTextualAllowedToolsForRequest(
		[]string{ToolNameSubmitPlan},
		[]string{ToolNameSubmitPlan, ToolNameCodeNavigate},
		[]string{ToolNameSubmitPlan, ToolNameCodeNavigate},
		ToolNameCodeNavigate,
	)
	if !slices.Equal(submitOnly, []string{ToolNameSubmitPlan}) {
		t.Fatalf("submit-only terminal surface should not reopen hidden navigation, got %#v", submitOnly)
	}
}
