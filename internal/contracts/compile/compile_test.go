package contractcompile

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestCompilePlanExecutePlannerBindsAuthorityRuntimeAndProtocol(t *testing.T) {
	result := compilePlannerFixture(t)

	if result.SchemaVersion != CompilerSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", result.SchemaVersion, CompilerSchemaVersion)
	}

	if result.Contract.Scope.InvocationKind != autonomy.InvocationPhase {
		t.Fatalf("InvocationKind = %q, want %q", result.Contract.Scope.InvocationKind, autonomy.InvocationPhase)
	}

	if result.Contract.Scope.Lane != DefaultPlanExecuteLane || result.Contract.Scope.Phase != DefaultPlannerPhase {
		t.Fatalf("scope lane/phase = %q/%q", result.Contract.Scope.Lane, result.Contract.Scope.Phase)
	}

	if result.Contract.Scope.RuntimeContract != result.Runtime.RuntimeContractID {
		t.Fatalf("scope runtime contract = %q, runtime id = %q", result.Contract.Scope.RuntimeContract, result.Runtime.RuntimeContractID)
	}

	if result.Runtime.ActiveInvariantID != InvariantActiveAuthority {
		t.Fatalf("runtime active invariant = %q", result.Runtime.ActiveInvariantID)
	}

	if result.Runtime.BackendKind != "mlx" ||
		result.Runtime.ModelAlias != "qwen3-coder-30b-a3b-instruct" ||
		result.Runtime.ParserTransportMode != "textual_json_terminal" ||
		result.Runtime.ToolTransportMode != "textual" ||
		result.Runtime.ParserContract != "submit_plan_json" ||
		result.Runtime.NativeToolPolicy != "textual_unless_forced" ||
		result.Runtime.ToolChoice != "none" ||
		result.Runtime.ResponseFormat != "json" ||
		result.Runtime.ThinkingPolicy != "disabled_visible_action" ||
		result.Runtime.ThinkingBudget != 128 ||
		result.Runtime.EffectiveMaxTokens != 4096 {

		t.Fatalf("runtime envelope not preserved: %#v", result.Runtime)
	}

	if result.Protocol.ContractID != result.Contract.ContractID {
		t.Fatalf("protocol contract id = %q, want %q", result.Protocol.ContractID, result.Contract.ContractID)
	}

	if result.ActiveAuthority.ContractID != result.Contract.ContractID ||
		result.ActiveAuthority.RuntimeContractID != result.Runtime.RuntimeContractID ||
		result.ActiveAuthority.ProtocolHash != result.Protocol.ProtocolHash ||
		result.ActiveAuthority.ActiveInvariantID != InvariantActiveAuthority {

		t.Fatalf("active authority binding did not mirror compiled ids: %#v", result.ActiveAuthority)
	}

	if result.Protocol.ProtocolHash == "" {
		t.Fatal("protocol hash is empty")
	}

	for _, want := range []string{autonomy.ContractEnvelopeSchemaVersion, autonomy.ContextPacketSchemaVersion, autonomy.ModelResponseSchemaVersion, "submit_plan"} {
		if !slices.Contains(result.Protocol.SchemaNames, want) {
			t.Fatalf("protocol schema names missing %q: %#v", want, result.Protocol.SchemaNames)
		}
	}

	if result.Protocol.PhaseID != string(TurnPhaseRequiredSource) {
		t.Fatalf("protocol phase id = %q", result.Protocol.PhaseID)
	}

	if result.Protocol.ActiveInvariantID != InvariantActiveAuthority {
		t.Fatalf("protocol active invariant = %q", result.Protocol.ActiveInvariantID)
	}

	if result.Protocol.ParserMode != "submit_plan_json" {
		t.Fatalf("protocol parser mode = %q", result.Protocol.ParserMode)
	}

	if result.Protocol.ToolTransportMode != "textual" ||
		result.Protocol.ParserTransportMode != "textual_json_terminal" ||
		result.Protocol.ParserContract != "submit_plan_json" {

		t.Fatalf("protocol transport fields not preserved: %#v", result.Protocol)
	}

	if !slices.Contains(result.Protocol.CallableTools, "read_file") {
		t.Fatalf("callable tools missing read_file: %#v", result.Protocol.CallableTools)
	}

	if !slices.Contains(result.Protocol.HiddenTools, "apply_patch") || !slices.Contains(result.Protocol.HiddenTools, "shell") {
		t.Fatalf("hidden tools missing forbidden tool projection: %#v", result.Protocol.HiddenTools)
	}

	if result.Protocol.NativeToolPolicy != "textual_unless_forced" ||
		result.Protocol.ToolChoice != "none" ||
		result.Protocol.ResponseFormat != "json" {

		t.Fatalf("protocol runtime policy fields not preserved: %#v", result.Protocol)
	}

	if !slices.Contains(result.Protocol.RequiredEditCoverage, "internal/runtime/agent/runner.go") {
		t.Fatalf("required edit coverage missing required path: %#v", result.Protocol.RequiredEditCoverage)
	}

	if !slices.Contains(result.Protocol.PathRoleSummary, "required_edit:internal/runtime/agent/runner.go") {
		t.Fatalf("path role summary missing required path: %#v", result.Protocol.PathRoleSummary)
	}

	if !slices.Contains(result.Protocol.VerificationObligations, "command:go test ./internal/contracts/compile") {
		t.Fatalf("verification obligations missing command: %#v", result.Protocol.VerificationObligations)
	}

	if !slices.Contains(result.Protocol.RequiredTools, "submit_plan") {
		t.Fatalf("required tools missing submit_plan: %#v", result.Protocol.RequiredTools)
	}

	if !slices.Contains(result.Protocol.ReadinessGate, "source_body_before_submit_plan") {
		t.Fatalf("readiness gate missing source_body_before_submit_plan: %#v", result.Protocol.ReadinessGate)
	}

	if !slices.Contains(result.Protocol.Constraints, "phase1_passive") ||
		!slices.Contains(result.Protocol.InvalidActions, "planner_shell") {

		t.Fatalf("protocol constraints/invalid actions not preserved: %#v", result.Protocol)
	}

	if !slices.Contains(result.Protocol.CallableActionClasses, string(autonomy.ActionClassSubmitPlan)) {
		t.Fatalf("callable action classes missing submit_plan: %#v", result.Protocol.CallableActionClasses)
	}

	if !slices.Contains(result.Protocol.InvalidActionClasses, string(autonomy.ActionClassPerEditWrite)) {
		t.Fatalf("invalid action classes missing per_edit_write: %#v", result.Protocol.InvalidActionClasses)
	}

	if !slices.Contains(result.Protocol.TerminalResponseShapes, "Decision: source-satisfied") {
		t.Fatalf("terminal shapes missing source-satisfied: %#v", result.Protocol.TerminalResponseShapes)
	}

	if !slices.Contains(result.Protocol.SourceBodyObligations, "internal/runtime/agent/runner.go") {
		t.Fatalf("source-body obligations missing required path: %#v", result.Protocol.SourceBodyObligations)
	}

	if result.Protocol.CompactPacket.SchemaVersion != autonomy.ContextPacketSchemaVersion {
		t.Fatalf("compact packet schema = %q", result.Protocol.CompactPacket.SchemaVersion)
	}

	if result.Protocol.CompactPacket.PacketID == "" {
		t.Fatal("compact packet id is empty")
	}

	if result.Protocol.CompactPacket.ContractID != result.Protocol.ContractID ||
		result.Protocol.CompactPacket.ProtocolHash != result.Protocol.ProtocolHash {

		t.Fatalf("compact packet refs not bound to protocol: %#v", result.Protocol.CompactPacket)
	}

	if !slices.Contains(result.Protocol.CompactPacket.TargetRefs, "pack:e7818bd1b0006176") {
		t.Fatalf("compact packet missing target packet ref: %#v", result.Protocol.CompactPacket.TargetRefs)
	}
	if !hasAuthoritySource(result.Contract.Authority, "target_packet", "pack:e7818bd1b0006176", autonomy.FreshnessUnknown) {
		t.Fatalf("target packet authority should remain freshness-unknown until typed freshness is supplied: %#v", result.Contract.Authority)
	}

	if !slices.Contains(result.Protocol.CompactPacket.PathRefs, "internal/runtime/agent/runner.go") {
		t.Fatalf("compact packet missing path ref: %#v", result.Protocol.CompactPacket.PathRefs)
	}

	if !slices.Contains(result.Protocol.CompactPacket.RedactionMarkers, "metadata_only") ||
		!slices.Contains(result.Protocol.CompactPacket.RedactionMarkers, "file_content_omitted") {

		t.Fatalf("compact packet missing redaction markers: %#v", result.Protocol.CompactPacket.RedactionMarkers)
	}

	decision := findActiveAuthorityDecision(t, result)
	if decision.Decision != autonomy.PolicyObserve {
		t.Fatalf("active authority decision = %q", decision.Decision)
	}

	if len(decision.ObligationRefs) == 0 {
		t.Fatalf("active authority decision did not cite obligations")
	}

	if countObligationKind(result.Contract.Obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("planner compiler emitted write obligations: %#v", result.Contract.Obligations)
	}

	if !slices.Contains(result.Contract.Capabilities.AllowedEffects, autonomy.SideEffectAdditive) {
		t.Fatalf("planner compiler should allow additive submit_plan/reporting effects: %#v", result.Contract.Capabilities.AllowedEffects)
	}

	for _, forbidden := range []autonomy.SideEffectClass{autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld} {
		if slices.Contains(result.Contract.Capabilities.AllowedEffects, forbidden) {
			t.Fatalf("planner compiler allowed forbidden effect %q: %#v", forbidden, result.Contract.Capabilities.AllowedEffects)
		}
	}

	if !hasObligation(result.Contract.Obligations, autonomy.ObligationEvidence, autonomy.OwnerPlanner, "internal/runtime/agent/runner.go", "submit_plan_required_edit_coverage") {
		t.Fatalf("planner required edit path was not represented as submit-plan coverage: %#v", result.Contract.Obligations)
	}
}

func TestCompilePlanExecutePlannerParentIDIsCorrelationOnly(t *testing.T) {
	input := plannerFixtureInput()

	input.ParentID = "contract_parent_runner"

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if result.Contract.ParentID != input.ParentID {
		t.Fatalf("compiled contract parent id = %q, want %q", result.Contract.ParentID, input.ParentID)
	}

	if result.Protocol.ContractID != result.Contract.ContractID {
		t.Fatalf("protocol contract id = %q, want compiled contract %q", result.Protocol.ContractID, result.Contract.ContractID)
	}

	if result.ActiveAuthority.ContractID != result.Contract.ContractID {
		t.Fatalf("active authority contract id = %q, want compiled contract %q", result.ActiveAuthority.ContractID, result.Contract.ContractID)
	}

	if result.ActiveAuthority.RuntimeContractID != result.Runtime.RuntimeContractID ||
		result.ActiveAuthority.ProtocolHash != result.Protocol.ProtocolHash ||
		result.ActiveAuthority.ActiveInvariantID != InvariantActiveAuthority {

		t.Fatalf("active authority binding did not carry runtime/protocol invariant ids: %#v", result.ActiveAuthority)
	}

	if result.Protocol.ProtocolHash == "" {
		t.Fatal("protocol hash is empty")
	}
}

func TestCompilePlanExecutePlannerPreservesExplicitEmptyCallableTools(t *testing.T) {
	input := plannerFixtureInput()

	input.Protocol.CallableTools = []string{}
	input.Protocol.CallableToolsSpecified = true

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if len(result.Protocol.VisibleTools) == 0 {
		t.Fatal("fixture should retain visible tools")
	}

	if len(result.Protocol.CallableTools) != 0 {
		t.Fatalf("callable tools = %#v, want explicit empty list", result.Protocol.CallableTools)
	}
}

func TestCompilePlanExecutePlannerDefaultActionClassesUseAutonomyVocabulary(t *testing.T) {
	input := plannerFixtureInput()

	input.Protocol.CallableActionClasses = nil
	input.Protocol.InvalidActionClasses = nil

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	wantCallable := sortedStrings(autonomy.ActionClassTokens(
		autonomy.ActionClassRead,
		autonomy.ActionClassSearch,
		autonomy.ActionClassSubmitPlan,
		autonomy.ActionClassSourceSatisfied,
		autonomy.ActionClassBlocker,
	))
	if !slices.Equal(result.Protocol.CallableActionClasses, wantCallable) {
		t.Fatalf("callable action classes = %#v, want autonomy vocabulary %#v", result.Protocol.CallableActionClasses, wantCallable)
	}

	wantInvalid := sortedStrings(autonomy.ActionClassTokens(
		autonomy.ActionClassPerEditWrite,
		autonomy.ActionClassVerify,
		autonomy.ActionClassProseFinal,
	))
	if !slices.Equal(result.Protocol.InvalidActionClasses, wantInvalid) {
		t.Fatalf("invalid action classes = %#v, want autonomy vocabulary %#v", result.Protocol.InvalidActionClasses, wantInvalid)
	}
}

func TestCompilePlanExecutePlannerIDsAreStableForEquivalentInput(t *testing.T) {
	first := compilePlannerFixture(t)

	second, err := CompilePlanExecutePlanner(PlanExecutePlannerInput{
		Lane:                 " plan-execute ",
		Phase:                " planner ",
		TaskFamily:           "plan_execute_planner",
		RuntimeContract:      "legacy.contract.v1",
		PermissionProfile:    "workspace_write",
		RequiredEditPaths:    []string{"./internal/runtime/agent/runner.go", "internal/contracts/autonomy/contract.go", "internal/runtime/agent/runner.go"},
		AllowedEditPaths:     []string{"internal/runtime/agent/runner.go", "internal/contracts/compile/types.go"},
		EvidencePaths:        []string{"internal/runtime/agent/phase_contract.go", "internal/runtime/agent/runner_autonomy.go"},
		ChangedPaths:         []string{"internal/contracts/autonomy/contract.go"},
		UserBoundaries:       []string{"do not wire runner yet", "no behavior changes"},
		AllowedTools:         []string{"submit_plan", "read_file", "context_pack", "search_content"},
		ForbiddenTools:       []string{"apply_patch", "shell"},
		RequiredEvidence:     []string{"source body for required edits"},
		RequiredTools:        []string{"submit_plan"},
		ReadinessGate:        []string{"source_body_before_submit_plan"},
		VerificationContract: "go test ./internal/contracts/compile",
		VerificationPlan:     []string{"go test ./internal/contracts/compile"},
		StopConditions:       []string{"submit_plan accepted", "source_satisfied"},
		FinalCritic:          []string{"protocol hash cited by prompt and gate"},
		TargetPacket:         []string{"pack:e7818bd1b0006176"},
		Constraints:          []string{"phase1_passive", "no_runner_wiring"},
		InvalidActions:       []string{"planner_shell", "planner_write"},
		Readiness: ReadinessInput{
			ReadyToAnswer:       false,
			ReadyToWrite:        false,
			ReadyToVerify:       false,
			OpenObligations:     []string{"source_body"},
			FurtherReadsRequire: "one exact missing source-body claim",
		},
		Budget: BudgetInput{
			MaxToolSteps:       12,
			MaxModelCalls:      3,
			MaxContextMessages: 40,
			MaxContextChars:    120000,
		},
		Runtime: RuntimeEnvelopeInput{
			BackendKind:         "mlx",
			ModelAlias:          "qwen3-coder-30b-a3b-instruct",
			ModelRole:           "planner",
			ParserTransportMode: "textual_json_terminal",
			ToolTransportMode:   "textual",
			ParserContract:      "submit_plan_json",
			NativeToolPolicy:    "textual_unless_forced",
			ToolChoice:          "none",
			ResponseFormat:      "json",
			ThinkingPolicy:      "disabled_visible_action",
			ThinkingBudget:      128,
			EffectiveMaxTokens:  4096,
			ContextBudget:       ContextBudget{MaxContextMessages: 40, MaxContextChars: 120000},
			QueueState:          QueueState{Pool: "planner", State: "allowed_degraded", Reason: "queue daemon unavailable"},
			ReadinessProfile:    "report_only",
			EnforcementMode:     "report_only",
			DegradedInvariants:  []string{"runtime.queue_unavailable"},
			AuthorizedFallback:  []string{"direct_model_call"},
		},
		Protocol: ProtocolInput{
			PhaseID:                string(TurnPhaseRequiredSource),
			ExpectedVisibleOutput:  "visible_text_tool_request",
			VisibleTools:           []string{"context_pack", "read_file", "search_content", "submit_plan"},
			HiddenTools:            []string{"apply_patch", "shell"},
			CallableActionClasses:  []string{"read", "search", "submit_plan", "source_satisfied", "blocker"},
			InvalidActionClasses:   []string{"per_edit_write", "verify", "prose_final"},
			TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			StructuredSchemaNames:  []string{"submit_plan"},
			MalformedOutputPolicy:  "retry_same_protocol_hash",
			StaleAnchorPolicy:      "bounded_source_refresh_before_submit_plan",
			SourceBodyObligations:  []string{"internal/runtime/agent/runner.go", "internal/contracts/autonomy/contract.go"},
			RecoveryBudget:         2,
		},
		MetadataTags: []string{"unit1"},
	})
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner equivalent input failed: %v", err)
	}

	if second.Contract.ContractID != first.Contract.ContractID {
		t.Fatalf("contract id changed for equivalent input: %q != %q", second.Contract.ContractID, first.Contract.ContractID)
	}

	if second.Runtime.RuntimeContractID != first.Runtime.RuntimeContractID {
		t.Fatalf("runtime id changed for equivalent input: %q != %q", second.Runtime.RuntimeContractID, first.Runtime.RuntimeContractID)
	}

	if second.Protocol.ProtocolHash != first.Protocol.ProtocolHash {
		t.Fatalf("protocol hash changed for equivalent input: %q != %q", second.Protocol.ProtocolHash, first.Protocol.ProtocolHash)
	}
}

func TestCompilePlanExecutePlannerDefaultsKeepRequiredEditsDistinctFromSourceBody(t *testing.T) {
	input := plannerFixtureInput()

	input.Protocol.SourceBodyObligations = nil

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if len(result.Protocol.SourceBodyObligations) != 0 {
		t.Fatalf("source-body obligations defaulted from required edit paths: %#v", result.Protocol.SourceBodyObligations)
	}

	if !slices.Contains(result.Protocol.RequiredEditCoverage, "internal/runtime/agent/runner.go") {
		t.Fatalf("required edit coverage missing required path: %#v", result.Protocol.RequiredEditCoverage)
	}

	if hasObligation(result.Contract.Obligations, autonomy.ObligationEvidence, autonomy.OwnerPlanner, "internal/runtime/agent/runner.go", "source_body_obligations") {
		t.Fatalf("required edit path was conflated with source-body evidence obligations: %#v", result.Contract.Obligations)
	}

	if !slices.Contains(result.Protocol.CallableActionClasses, string(autonomy.ActionClassSearch)) {
		t.Fatalf("default callable classes missing search: %#v", result.Protocol.CallableActionClasses)
	}
}

func TestCompilePlanExecutePlannerAcceptsPlannerToolVocabulary(t *testing.T) {
	input := plannerFixtureInput()
	plannerTools := autonomy.PlanExecutePlannerToolNames()

	input.AllowedTools = plannerTools
	input.RequiredTools = []string{"history_range", "history_symbol", "submit_plan"}
	input.Protocol.VisibleTools = plannerTools
	input.Protocol.CallableTools = plannerTools
	input.Protocol.CallableToolsSpecified = true

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed for planner vocabulary: %v", err)
	}

	for _, tool := range []string{"blame_range", "context_impact", "harness_feedback", "history_range", "history_symbol"} {
		if !slices.Contains(result.Protocol.VisibleTools, tool) {
			t.Fatalf("visible tools missing %s: %#v", tool, result.Protocol.VisibleTools)
		}

		if !slices.Contains(result.Protocol.CallableTools, tool) {
			t.Fatalf("callable tools missing %s: %#v", tool, result.Protocol.CallableTools)
		}
	}

	if !slices.Contains(result.Contract.Capabilities.AllowedEffects, autonomy.SideEffectAdditive) {
		t.Fatalf("planner vocabulary should allow additive terminal/reporting effects: %#v", result.Contract.Capabilities.AllowedEffects)
	}
}

func TestCompilePlanExecutePlannerRejectsInvalidProtocolVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PlanExecutePlannerInput)
	}{
		{
			name: "unmapped allowed tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.AllowedTools = append(input.AllowedTools, "magic_tool")
			},
		},
		{
			name: "unmapped required tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.RequiredTools = append(input.RequiredTools, "magic_tool")
			},
		},
		{
			name: "unmapped visible tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.VisibleTools = append(input.Protocol.VisibleTools, "magic_tool")
			},
		},
		{
			name: "unmapped callable tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.CallableTools = append(input.Protocol.CallableTools, "magic_tool")
				input.Protocol.CallableToolsSpecified = true
			},
		},
		{
			name: "unmapped hidden tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.HiddenTools = append(input.Protocol.HiddenTools, "magic_tool")
			},
		},
		{
			name: "hidden visible overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.HiddenTools = append(input.Protocol.HiddenTools, "read_file")
			},
		},
		{
			name: "hidden callable overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.HiddenTools = append(input.Protocol.HiddenTools, "read_file")
				input.Protocol.CallableTools = []string{"read_file"}
				input.Protocol.CallableToolsSpecified = true
			},
		},
		{
			name: "invalid action class",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.CallableActionClasses = append(input.Protocol.CallableActionClasses, "invented_action")
			},
		},
		{
			name: "overlap action class",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.InvalidActionClasses = append(input.Protocol.InvalidActionClasses, "submit_plan")
			},
		},
		{
			name: "mutating allowed tool forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.AllowedTools = append(input.AllowedTools, "apply_patch")
			},
		},
		{
			name: "mutating visible tool forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.VisibleTools = append(input.Protocol.VisibleTools, "apply_patch")
			},
		},
		{
			name: "mutating required tool forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.RequiredTools = append(input.RequiredTools, "apply_patch")
			},
		},
		{
			name: "workspace-write shell forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.AllowedTools = append(input.AllowedTools, "shell")
			},
		},
		{
			name: "workspace-write source sanity forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.RequiredTools = append(input.RequiredTools, "source_sanity")
			},
		},
		{
			name: "workspace-write delegation forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.VisibleTools = append(input.Protocol.VisibleTools, "task")
			},
		},
		{
			name: "delegate edit forbidden for planner",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.CallableTools = append(input.Protocol.CallableTools, "delegate_edit")
			},
		},
		{
			name: "allowed forbidden overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.ForbiddenTools = append(input.ForbiddenTools, "read_file")
			},
		},
		{
			name: "allowed forbidden capability overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.ForbiddenTools = append(input.ForbiddenTools, "search_files")
			},
		},
		{
			name: "required forbidden overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.ForbiddenTools = append(input.ForbiddenTools, "submit_plan")
			},
		},
		{
			name: "required forbidden capability overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.RequiredTools = append(input.RequiredTools, "search_files")
				input.ForbiddenTools = append(input.ForbiddenTools, "search_content")
			},
		},
		{
			name: "visible forbidden overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.ForbiddenTools = append(input.ForbiddenTools, "search_content")
			},
		},
		{
			name: "visible forbidden capability overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.VisibleTools = append(input.Protocol.VisibleTools, "context_query")
				input.ForbiddenTools = append(input.ForbiddenTools, "context_pack")
			},
		},
		{
			name: "callable forbidden overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.CallableTools = []string{"read_file"}
				input.Protocol.CallableToolsSpecified = true
				input.ForbiddenTools = append(input.ForbiddenTools, "read_file")
			},
		},
		{
			name: "callable forbidden capability overlap",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.VisibleTools = append(input.Protocol.VisibleTools, "search_files")
				input.Protocol.CallableTools = []string{"search_files"}
				input.Protocol.CallableToolsSpecified = true
				input.ForbiddenTools = append(input.ForbiddenTools, "search_content")
			},
		},
		{
			name: "callable tool must be visible",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.CallableTools = []string{"history_range"}
				input.Protocol.CallableToolsSpecified = true
			},
		},
		{
			name: "non-passive enforcement mode",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Runtime.EnforcementMode = "enforce"
			},
		},
		{
			name: "free-form required next rejected",
			mutate: func(input *PlanExecutePlannerInput) {
				input.RequiredNext = []string{"edit the file immediately"}
			},
		},
		{
			name: "authorized fallback requires fallback",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Runtime.DegradedInvariants = nil
				input.Runtime.AuthorizedFallback = nil
				input.Runtime.DegradationDecisions = []DegradationDecision{{
					InvariantID: "runtime.queue_unavailable",
					Decision:    "authorized_fallback",
				}}
			},
		},
		{
			name: "raw marker in structured schema",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.StructuredSchemaNames = append(input.Protocol.StructuredSchemaNames, "raw_prompt")
			},
		},
		{
			name: "raw marker in hidden tool",
			mutate: func(input *PlanExecutePlannerInput) {
				input.Protocol.HiddenTools = append(input.Protocol.HiddenTools, "raw_prompt")
			},
		},
		{
			name: "raw marker in context packet ref",
			mutate: func(input *PlanExecutePlannerInput) {
				input.TargetPacket = append(input.TargetPacket, "transcript: full text")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := plannerFixtureInput()
			tc.mutate(&input)

			if _, err := CompilePlanExecutePlanner(input); err == nil {
				t.Fatalf("CompilePlanExecutePlanner succeeded, want validation error")
			}
		})
	}
}

func TestCompilePlanExecutePlannerClampsNegativeRuntimeBudgetsAndDefaultsReportOnly(t *testing.T) {
	input := plannerFixtureInput()

	input.Runtime.EnforcementMode = ""
	input.Runtime.ThinkingBudget = -1
	input.Runtime.EffectiveMaxTokens = -100

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if result.Runtime.EnforcementMode != "report_only" {
		t.Fatalf("EnforcementMode = %q, want report_only", result.Runtime.EnforcementMode)
	}

	if result.Runtime.ThinkingBudget != 0 || result.Runtime.EffectiveMaxTokens != 0 {
		t.Fatalf("negative runtime budgets were not clamped: %#v", result.Runtime)
	}

	for _, rule := range result.Contract.ToolPolicy {
		if !strings.Contains(rule.Source, "report_only") || !strings.Contains(rule.ReasonCode, "report_only") {
			t.Fatalf("tool policy rule missing report-only provenance: %#v", rule)
		}
	}
}

func TestCompilePlanExecutePlannerKeepsExplicitDegradationFallbackAssociations(t *testing.T) {
	input := plannerFixtureInput()

	input.Runtime.DegradedInvariants = []string{"runtime.queue_unavailable", "runtime.native_tools_unavailable"}
	input.Runtime.AuthorizedFallback = []string{"legacy_global_fallback"}
	input.Runtime.DegradationDecisions = []DegradationDecision{
		{
			InvariantID:         " runtime.native_tools_unavailable ",
			ReasonCode:          "native_tools_disabled",
			Decision:            "authorized_fallback",
			AuthorizedFallbacks: []string{" textual_tool_contract ", "textual_tool_contract"},
		},
		{
			InvariantID:         "runtime.queue_unavailable",
			ReasonCode:          "queue_daemon_unavailable",
			Decision:            "authorized_fallback",
			AuthorizedFallbacks: []string{"direct_model_call"},
		},
		{
			InvariantID:         "runtime.queue_unavailable",
			ReasonCode:          "queue_daemon_unavailable",
			Decision:            "authorized_fallback",
			AuthorizedFallbacks: []string{"direct_model_call"},
		},
	}

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if len(result.Runtime.DegradationDecisions) != 2 {
		t.Fatalf("degradation decisions = %#v, want one per explicit invariant decision", result.Runtime.DegradationDecisions)
	}

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	if got := decisions["runtime.queue_unavailable"].AuthorizedFallbacks; !slices.Equal(got, []string{"direct_model_call"}) {
		t.Fatalf("queue fallback association = %#v, want direct_model_call only", got)
	}

	if got := decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks; !slices.Equal(got, []string{"textual_tool_contract"}) {
		t.Fatalf("native-tools fallback association = %#v, want textual_tool_contract only", got)
	}

	if slices.Contains(decisions["runtime.queue_unavailable"].AuthorizedFallbacks, "legacy_global_fallback") ||
		slices.Contains(decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks, "legacy_global_fallback") {

		t.Fatalf("explicit degradation decisions were polluted by legacy fallback fields: %#v", result.Runtime.DegradationDecisions)
	}
}

func TestCompilePlanExecutePlannerPairsLegacyDegradedInvariantsByPosition(t *testing.T) {
	input := plannerFixtureInput()

	input.Runtime.DegradedInvariants = []string{"runtime.queue_unavailable", "runtime.native_tools_unavailable"}
	input.Runtime.AuthorizedFallback = []string{"direct_model_call", "textual_tool_contract"}
	input.Runtime.DegradationDecisions = nil

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	if got := decisions["runtime.queue_unavailable"].AuthorizedFallbacks; !slices.Equal(got, []string{"direct_model_call"}) {
		t.Fatalf("legacy queue fallback association = %#v, want direct_model_call", got)
	}

	if got := decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks; !slices.Equal(got, []string{"textual_tool_contract"}) {
		t.Fatalf("legacy native-tools fallback association = %#v, want textual_tool_contract", got)
	}
}

func TestCompilePlanExecutePlannerLegacyDegradationWithoutFallbackIsNotAuthorizedFallback(t *testing.T) {
	input := plannerFixtureInput()

	input.Runtime.DegradedInvariants = []string{"runtime.queue_unavailable"}
	input.Runtime.AuthorizedFallback = nil
	input.Runtime.DegradationDecisions = nil

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	if got := decisions["runtime.queue_unavailable"].Decision; got != "degraded_no_fallback" {
		t.Fatalf("legacy degradation decision = %q, want degraded_no_fallback", got)
	}

	if got := decisions["runtime.queue_unavailable"].AuthorizedFallbacks; len(got) != 0 {
		t.Fatalf("legacy degradation fallbacks = %#v, want none", got)
	}
}

func TestCompilePlanExecutePlannerSafeAlternativesFollowTerminalShapes(t *testing.T) {
	input := plannerFixtureInput()

	input.Protocol.TerminalResponseShapes = []string{"submit_plan"}
	input.Protocol.CallableActionClasses = []string{"submit_plan"}

	result, err := CompilePlanExecutePlanner(input)
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	if !slices.Equal(result.Protocol.SafeAlternatives, []string{"submit_plan"}) {
		t.Fatalf("safe alternatives = %#v, want only submit_plan", result.Protocol.SafeAlternatives)
	}
}

func TestCompilePlanExecutePlannerPathRolesRemainDistinct(t *testing.T) {
	result := compilePlannerFixture(t)
	roleByPath := make(map[string][]autonomy.PathRoleKind)

	for _, role := range result.Contract.PathRoles {
		roleByPath[role.PathPattern] = append(roleByPath[role.PathPattern], role.Role)
	}

	assertRole(t, roleByPath, "internal/runtime/agent/runner.go", autonomy.PathRoleRequiredEdit)
	assertRole(t, roleByPath, "internal/runtime/agent/runner.go", autonomy.PathRoleAllowedEdit)
	assertRole(t, roleByPath, "internal/contracts/autonomy/contract.go", autonomy.PathRoleRequiredEdit)
	assertRole(t, roleByPath, "internal/contracts/autonomy/contract.go", autonomy.PathRoleGeneratedArtifact)
	assertRole(t, roleByPath, "internal/runtime/agent/phase_contract.go", autonomy.PathRoleEvidenceReadonly)
	assertRole(t, roleByPath, ".git/**", autonomy.PathRoleProtected)
}

func TestPackageImportBoundaries(t *testing.T) {
	for _, tc := range []struct {
		dir       string
		forbidden []string
	}{
		{
			dir: "internal/contracts/compile",
			forbidden: []string{
				"idiom/cmd/idiom",
				"idiom/internal/runtime/agent",
				"idiom/internal/surfaces/tui/app",
				"idiom/internal/runtime/backends",
				"idiom/internal/runtime/workerruntime",
				"idiom/internal/capabilities/mcp",
				"idiom/internal/repository/semantic",
				"idiom/internal/capabilities/workspace",
			},
		},
		{
			dir: "internal/contracts/autonomy",
			forbidden: []string{
				"idiom/cmd/idiom",
				"idiom/internal/runtime/agent",
				"idiom/internal/surfaces/tui/app",
				"idiom/internal/runtime/backends",
				"idiom/internal/runtime/workerruntime",
				"idiom/internal/capabilities/mcp",
				"idiom/internal/repository/semantic",
				"idiom/internal/capabilities/workspace",
			},
		},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			assertNoForbiddenImports(t, tc.dir, tc.forbidden)
		})
	}
}

func compilePlannerFixture(t *testing.T) CompileResult {
	t.Helper()

	result, err := CompilePlanExecutePlanner(plannerFixtureInput())
	if err != nil {
		t.Fatalf("CompilePlanExecutePlanner failed: %v", err)
	}

	return result
}

func plannerFixtureInput() PlanExecutePlannerInput {
	return PlanExecutePlannerInput{
		Lane:                 DefaultPlanExecuteLane,
		Phase:                DefaultPlannerPhase,
		TaskFamily:           InvocationPlanExecutePlanner,
		RuntimeContract:      "legacy.contract.v1",
		PermissionProfile:    "workspace_write",
		RequiredEditPaths:    []string{"internal/runtime/agent/runner.go", "internal/contracts/autonomy/contract.go"},
		AllowedEditPaths:     []string{"internal/contracts/compile/types.go", "internal/runtime/agent/runner.go"},
		EvidencePaths:        []string{"internal/runtime/agent/runner_autonomy.go", "internal/runtime/agent/phase_contract.go"},
		ChangedPaths:         []string{"internal/contracts/autonomy/contract.go"},
		UserBoundaries:       []string{"do not wire runner yet", "no behavior changes"},
		AllowedTools:         []string{"context_pack", "read_file", "search_content", "submit_plan"},
		ForbiddenTools:       []string{"apply_patch", "shell"},
		RequiredEvidence:     []string{"source body for required edits"},
		RequiredTools:        []string{"submit_plan"},
		ReadinessGate:        []string{"source_body_before_submit_plan"},
		VerificationContract: "go test ./internal/contracts/compile",
		VerificationPlan:     []string{"go test ./internal/contracts/compile"},
		StopConditions:       []string{"submit_plan accepted", "source_satisfied"},
		FinalCritic:          []string{"protocol hash cited by prompt and gate"},
		TargetPacket:         []string{"pack:e7818bd1b0006176"},
		Constraints:          []string{"phase1_passive", "no_runner_wiring"},
		InvalidActions:       []string{"planner_shell", "planner_write"},
		Readiness: ReadinessInput{
			OpenObligations:     []string{"source_body"},
			FurtherReadsRequire: "one exact missing source-body claim",
		},
		Budget: BudgetInput{
			MaxToolSteps:       12,
			MaxModelCalls:      3,
			MaxContextMessages: 40,
			MaxContextChars:    120000,
		},
		Runtime: RuntimeEnvelopeInput{
			BackendKind:         "mlx",
			ModelAlias:          "qwen3-coder-30b-a3b-instruct",
			ModelRole:           "planner",
			ParserTransportMode: "textual_json_terminal",
			ToolTransportMode:   "textual",
			ParserContract:      "submit_plan_json",
			NativeToolPolicy:    "textual_unless_forced",
			ToolChoice:          "none",
			ResponseFormat:      "json",
			ThinkingPolicy:      "disabled_visible_action",
			ThinkingBudget:      128,
			EffectiveMaxTokens:  4096,
			ContextBudget:       ContextBudget{MaxContextMessages: 40, MaxContextChars: 120000},
			QueueState:          QueueState{Pool: "planner", State: "allowed_degraded", Reason: "queue daemon unavailable"},
			ReadinessProfile:    "report_only",
			EnforcementMode:     "report_only",
			DegradedInvariants:  []string{"runtime.queue_unavailable"},
			AuthorizedFallback:  []string{"direct_model_call"},
		},
		Protocol: ProtocolInput{
			PhaseID:                string(TurnPhaseRequiredSource),
			ExpectedVisibleOutput:  "visible_text_tool_request",
			VisibleTools:           []string{"context_pack", "read_file", "search_content", "submit_plan"},
			HiddenTools:            []string{"apply_patch", "shell"},
			CallableActionClasses:  []string{"read", "search", "submit_plan", "source_satisfied", "blocker"},
			InvalidActionClasses:   []string{"per_edit_write", "verify", "prose_final"},
			TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			StructuredSchemaNames:  []string{"submit_plan"},
			MalformedOutputPolicy:  "retry_same_protocol_hash",
			StaleAnchorPolicy:      "bounded_source_refresh_before_submit_plan",
			SourceBodyObligations:  []string{"internal/runtime/agent/runner.go", "internal/contracts/autonomy/contract.go"},
			RecoveryBudget:         2,
		},
		MetadataTags: []string{"unit1"},
	}
}

func findActiveAuthorityDecision(t *testing.T, result CompileResult) autonomy.PolicyDecision {
	t.Helper()

	for _, decision := range result.Contract.PolicyDecisions {
		if decision.InvariantID == InvariantActiveAuthority {
			return decision
		}
	}

	t.Fatalf("missing %s policy decision in %#v", InvariantActiveAuthority, result.Contract.PolicyDecisions)

	return autonomy.PolicyDecision{}
}

func hasAuthoritySource(sources []autonomy.AuthoritySource, kind string, ref string, freshness autonomy.Freshness) bool {
	for _, source := range sources {
		if source.Kind == kind && source.Ref == ref && source.Freshness == freshness {
			return true
		}
	}

	return false
}

func assertRole(t *testing.T, roleByPath map[string][]autonomy.PathRoleKind, path string, role autonomy.PathRoleKind) {
	t.Helper()

	if !slices.Contains(roleByPath[path], role) {
		t.Fatalf("path %q roles = %#v, want %q", path, roleByPath[path], role)
	}
}

func assertNoForbiddenImports(t *testing.T, dir string, forbidden []string) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("..", "..", dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}

	fset := token.NewFileSet()

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", file, err)
		}

		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			if isForbiddenImport(path, forbidden) {
				t.Fatalf("%s imports forbidden package %q", file, path)
			}
		}
	}
}

func countObligationKind(obligations []autonomy.Obligation, kind autonomy.ObligationKind) int {
	var count int

	for _, obligation := range obligations {
		if obligation.Kind == kind {
			count++
		}
	}

	return count
}

func hasObligation(obligations []autonomy.Obligation, kind autonomy.ObligationKind, owner autonomy.ObligationOwner, targetRef string, requiredBy string) bool {
	for _, obligation := range obligations {
		if obligation.Kind == kind &&
			obligation.Owner == owner &&
			obligation.TargetRef == targetRef &&
			obligation.RequiredBy == requiredBy {

			return true
		}
	}

	return false
}

func degradationDecisionsByInvariant(decisions []DegradationDecision) map[string]DegradationDecision {
	out := make(map[string]DegradationDecision, len(decisions))
	for _, decision := range decisions {
		out[decision.InvariantID] = decision
	}

	return out
}

func isForbiddenImport(path string, forbidden []string) bool {
	for _, prefix := range forbidden {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}

	return false
}
