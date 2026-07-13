package contractcompile

import (
	"idiom/internal/contracts/autonomy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("binds planner authority, runtime, and protocol contracts", func() {
	result := compilePlannerFixture()

	Expect(result.SchemaVersion).To(Equal(CompilerSchemaVersion))
	Expect(result.Contract.Scope.InvocationKind).To(Equal(autonomy.InvocationPhase))
	Expect(result.Contract.Scope.Lane).To(Equal(DefaultPlanExecuteLane))
	Expect(result.Contract.Scope.Phase).To(Equal(DefaultPlannerPhase))
	Expect(result.Contract.Scope.RuntimeContract).To(Equal(result.Runtime.RuntimeContractID))
	Expect(result.Runtime.ActiveInvariantID).To(Equal(InvariantActiveAuthority))
	Expect(result.Runtime.BackendKind).To(Equal("mlx"))
	Expect(result.Runtime.ModelAlias).To(Equal("qwen3-coder-30b-a3b-instruct"))
	Expect(result.Runtime.ParserTransportMode).To(Equal("textual_json_terminal"))
	Expect(result.Runtime.ToolTransportMode).To(Equal("textual"))
	Expect(result.Runtime.ParserContract).To(Equal("submit_plan_json"))
	Expect(result.Runtime.NativeToolPolicy).To(Equal("textual_unless_forced"))
	Expect(result.Runtime.ToolChoice).To(Equal("none"))
	Expect(result.Runtime.ResponseFormat).To(Equal("json"))
	Expect(result.Runtime.ThinkingPolicy).To(Equal("disabled_visible_action"))
	Expect(result.Runtime.ThinkingBudget).To(Equal(128))
	Expect(result.Runtime.EffectiveMaxTokens).To(Equal(4096))

	Expect(result.Protocol.ContractID).To(Equal(result.Contract.ContractID))
	Expect(result.ActiveAuthority.ContractID).To(Equal(result.Contract.ContractID))
	Expect(result.ActiveAuthority.RuntimeContractID).To(Equal(result.Runtime.RuntimeContractID))
	Expect(result.ActiveAuthority.ProtocolHash).To(Equal(result.Protocol.ProtocolHash))
	Expect(result.ActiveAuthority.ActiveInvariantID).To(Equal(InvariantActiveAuthority))
	Expect(result.Protocol.ProtocolHash).NotTo(BeEmpty())
	Expect(result.Protocol.SchemaNames).To(ContainElements(
		autonomy.ContractEnvelopeSchemaVersion,
		autonomy.ContextPacketSchemaVersion,
		autonomy.ModelResponseSchemaVersion,
		"submit_plan",
	))
	Expect(result.Protocol.PhaseID).To(Equal(string(TurnPhaseRequiredSource)))
	Expect(result.Protocol.ActiveInvariantID).To(Equal(InvariantActiveAuthority))
	Expect(result.Contract.Budget.MaxOperationalModelCalls).To(Equal(5))
	Expect(result.Protocol.ParserMode).To(Equal("submit_plan_json"))
	Expect(result.Protocol.ToolTransportMode).To(Equal("textual"))
	Expect(result.Protocol.ParserTransportMode).To(Equal("textual_json_terminal"))
	Expect(result.Protocol.ParserContract).To(Equal("submit_plan_json"))
	Expect(result.Protocol.CallableTools).To(ContainElement("read_file"))
	Expect(result.Protocol.HiddenTools).To(ContainElements("apply_patch", "shell"))
	Expect(result.Protocol.NativeToolPolicy).To(Equal("textual_unless_forced"))
	Expect(result.Protocol.ToolChoice).To(Equal("none"))
	Expect(result.Protocol.ResponseFormat).To(Equal("json"))
	Expect(result.Protocol.RequiredEditCoverage).To(ContainElement("internal/runtime/agent/runner.go"))
	Expect(result.Protocol.PathRoleSummary).To(ContainElement("required_edit:internal/runtime/agent/runner.go"))
	Expect(result.Protocol.VerificationObligations).To(ContainElement("command:go test ./internal/contracts/compile"))
	Expect(result.Protocol.RequiredTools).To(ContainElement("submit_plan"))
	Expect(result.Protocol.ReadinessGate).To(ContainElement("source_body_before_submit_plan"))
	Expect(result.Protocol.Constraints).To(ContainElement("phase1_passive"))
	Expect(result.Protocol.InvalidActions).To(ContainElement("planner_shell"))
	Expect(result.Protocol.CallableActionClasses).To(ContainElement(string(autonomy.ActionClassSubmitPlan)))
	Expect(result.Protocol.InvalidActionClasses).To(ContainElement(string(autonomy.ActionClassPerEditWrite)))
	Expect(result.Protocol.TerminalResponseShapes).To(ContainElement("Decision: source-satisfied"))
	Expect(result.Protocol.SourceBodyObligations).To(ContainElement("internal/runtime/agent/runner.go"))

	Expect(result.Protocol.CompactPacket.SchemaVersion).To(Equal(autonomy.ContextPacketSchemaVersion))
	Expect(result.Protocol.CompactPacket.PacketID).NotTo(BeEmpty())
	Expect(result.Protocol.CompactPacket.ContractID).To(Equal(result.Protocol.ContractID))
	Expect(result.Protocol.CompactPacket.ProtocolHash).To(Equal(result.Protocol.ProtocolHash))
	Expect(result.Protocol.CompactPacket.TargetRefs).To(ContainElement("pack:e7818bd1b0006176"))
	Expect(hasAuthoritySource(result.Contract.Authority, "target_packet", "pack:e7818bd1b0006176", autonomy.FreshnessUnknown)).To(BeTrue(),
		"target packet authority should remain freshness-unknown until typed freshness is supplied")
	Expect(result.Protocol.CompactPacket.PathRefs).To(ContainElement("internal/runtime/agent/runner.go"))
	Expect(result.Protocol.CompactPacket.RedactionMarkers).To(ContainElements("metadata_only", "file_content_omitted"))

	decision := findActiveAuthorityDecision(result)
	Expect(decision.Decision).To(Equal(autonomy.PolicyObserve))
	Expect(decision.ObligationRefs).NotTo(BeEmpty())
	Expect(countObligationKind(result.Contract.Obligations, autonomy.ObligationWrite)).To(BeZero())
	Expect(result.Contract.Capabilities.AllowedEffects).To(ContainElement(autonomy.SideEffectAdditive))
	Expect(result.Contract.Capabilities.AllowedEffects).NotTo(ContainElement(autonomy.SideEffectDestructive))
	Expect(result.Contract.Capabilities.AllowedEffects).NotTo(ContainElement(autonomy.SideEffectOpenWorld))
	Expect(hasObligation(result.Contract.Obligations, autonomy.ObligationEvidence, autonomy.OwnerPlanner, "internal/runtime/agent/runner.go", "submit_plan_required_edit_coverage")).To(BeTrue(),
		"required edit path should be represented as submit-plan coverage")
})

var _ = It("compiles a direct-answer protocol without planner authority", func() {
	protocol, err := CompileModelProtocol(ModelProtocolInput{
		ContractID:      "contract-direct",
		ContractVersion: autonomy.SchemaVersion,
		Runtime: RuntimeEnvelopeInput{
			BackendKind:         "openai_compatible",
			ModelAlias:          "direct-answer-model",
			ModelRole:           "direct_answer",
			ParserTransportMode: "native_or_prose",
			ToolTransportMode:   "native",
			ParserContract:      "tool_or_final_answer",
			SupportsNativeTools: true,
		},
		Protocol: ProtocolInput{
			PhaseID:                 "read_only_evidence_pending",
			VisibleTools:            []string{"read_file", "code_navigate"},
			CallableTools:           []string{"read_file", "code_navigate"},
			CallableToolsSpecified:  true,
			CallableActionClasses:   []string{string(autonomy.ActionClassRead), string(autonomy.ActionClassFinalAnswer), string(autonomy.ActionClassBlocker)},
			InvalidActionClasses:    []string{string(autonomy.ActionClassPerEditWrite), string(autonomy.ActionClassVerify)},
			TerminalResponseShapes:  []string{"final answer", "<<QUESTION>>:"},
			SourceBodyObligations:   []string{"internal/runtime/agent/evidence_readonly.go"},
			GroundedSourceBodyPaths: []string{"docs/architecture.md"},
			CandidateOnlyPaths:      []string{"internal/repository/comprehension"},
		},
		ContextPacket: ModelProtocolContextPacketInput{
			PathRefs: []string{"docs/architecture.md", "internal/runtime/agent/evidence_readonly.go"},
		},
	})

	Expect(err).NotTo(HaveOccurred())
	Expect(protocol.ProtocolHash).NotTo(BeEmpty())
	Expect(protocol.CompactPacket.ProtocolHash).To(Equal(protocol.ProtocolHash))
	Expect(protocol.ContractID).To(Equal("contract-direct"))
	Expect(protocol.VisibleTools).To(ConsistOf("read_file", "code_navigate"))
	Expect(protocol.CallableActionClasses).To(ConsistOf(
		string(autonomy.ActionClassRead),
		string(autonomy.ActionClassFinalAnswer),
		string(autonomy.ActionClassBlocker),
	))
	Expect(protocol.TerminalResponseShapes).To(ConsistOf("final answer", "<<QUESTION>>:"))
	Expect(protocol.GroundedSourceBodyPaths).To(Equal([]string{"docs/architecture.md"}))
	Expect(protocol.GroundedSourceBodyPathTotal).To(Equal(1))
	Expect(protocol.GroundedSourceBodyPathOmitted).To(BeZero())
	Expect(protocol.SourceBodyObligations).To(Equal([]string{"internal/runtime/agent/evidence_readonly.go"}))
	Expect(protocol.SourceBodyObligationTotal).To(Equal(1))
	Expect(protocol.SourceBodyObligationOmitted).To(BeZero())
	Expect(protocol.CandidateOnlyPaths).To(Equal([]string{"internal/repository/comprehension"}))
	Expect(protocol.CandidateOnlyPathTotal).To(Equal(1))
	Expect(protocol.CandidateOnlyPathOmitted).To(BeZero())
	Expect(protocol.SchemaNames).NotTo(ContainElement("submit_plan"))
	Expect(protocol.CallableTools).NotTo(ContainElement("submit_plan"))
	Expect(protocol.CallableActionClasses).NotTo(ContainElement(string(autonomy.ActionClassSubmitPlan)))
	Expect(protocol.TerminalResponseShapes).NotTo(ContainElement("submit_plan"))
})

var _ = It("rejects contradictory grounded and pending source authority", func() {
	_, err := CompileModelProtocol(ModelProtocolInput{
		ContractID:      "contract-direct",
		ContractVersion: autonomy.SchemaVersion,
		Protocol: ProtocolInput{
			GroundedSourceBodyPaths: []string{"README.md"},
			SourceBodyObligations:   []string{"./README.md"},
		},
	})

	Expect(err).To(MatchError(ContainSubstring("cannot be both grounded source-body evidence and a pending source-body obligation")))
})

var _ = It("uses the parent contract ID only for correlation", func() {
	input := plannerFixtureInput()

	input.ParentID = "contract_parent_runner"

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Contract.ParentID).To(Equal(input.ParentID))
	Expect(result.Protocol.ContractID).To(Equal(result.Contract.ContractID))
	Expect(result.ActiveAuthority.ContractID).To(Equal(result.Contract.ContractID))
	Expect(result.ActiveAuthority.RuntimeContractID).To(Equal(result.Runtime.RuntimeContractID))
	Expect(result.ActiveAuthority.ProtocolHash).To(Equal(result.Protocol.ProtocolHash))
	Expect(result.ActiveAuthority.ActiveInvariantID).To(Equal(InvariantActiveAuthority))
	Expect(result.Protocol.ProtocolHash).NotTo(BeEmpty())
})

var _ = It("preserves an explicitly empty callable-tool set", func() {
	input := plannerFixtureInput()

	input.Protocol.CallableTools = []string{}
	input.Protocol.CallableToolsSpecified = true

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Protocol.VisibleTools).NotTo(BeEmpty(), "fixture should retain visible tools")
	Expect(result.Protocol.CallableTools).To(BeEmpty(), "explicit empty callable-tool authority must survive compilation")
})

var _ = It("defaults action classes from the autonomy vocabulary", func() {
	input := plannerFixtureInput()

	input.Protocol.CallableActionClasses = nil
	input.Protocol.InvalidActionClasses = nil

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())

	wantCallable := sortedStrings([]string{
		string(autonomy.ActionClassRead),
		string(autonomy.ActionClassSearch),
		string(autonomy.ActionClassSubmitPlan),
		string(autonomy.ActionClassSourceSatisfied),
		string(autonomy.ActionClassBlocker),
	})
	Expect(result.Protocol.CallableActionClasses).To(Equal(wantCallable))

	wantInvalid := sortedStrings([]string{
		string(autonomy.ActionClassPerEditWrite),
		string(autonomy.ActionClassVerify),
		string(autonomy.ActionClassProseFinal),
	})
	Expect(result.Protocol.InvalidActionClasses).To(Equal(wantInvalid))
})

var _ = It("produces stable IDs for equivalent normalized input", func() {
	first := compilePlannerFixture()

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
			MaxToolSteps:             12,
			MaxModelCalls:            3,
			MaxOperationalModelCalls: 5,
			MaxContextMessages:       40,
			MaxContextChars:          120000,
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
		},
		ToolMetadata: plannerFixtureToolMetadata(),
		MetadataTags: []string{"unit1"},
	})
	Expect(err).NotTo(HaveOccurred(), "compile equivalent normalized planner input")
	Expect(second.Contract.ContractID).To(Equal(first.Contract.ContractID))
	Expect(second.Runtime.RuntimeContractID).To(Equal(first.Runtime.RuntimeContractID))
	Expect(second.Protocol.ProtocolHash).To(Equal(first.Protocol.ProtocolHash))
})

var _ = It("keeps required edits distinct from source-body evidence by default", func() {
	input := plannerFixtureInput()

	input.Protocol.SourceBodyObligations = nil

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Protocol.SourceBodyObligations).To(BeEmpty(), "source-body obligations must not default from required edit paths")
	Expect(result.Protocol.RequiredEditCoverage).To(ContainElement("internal/runtime/agent/runner.go"))
	Expect(hasObligation(result.Contract.Obligations, autonomy.ObligationEvidence, autonomy.OwnerPlanner, "internal/runtime/agent/runner.go", "source_body_obligations")).To(BeFalse(),
		"required edits must not be conflated with source-body evidence")
	Expect(result.Protocol.CallableActionClasses).To(ContainElement(string(autonomy.ActionClassSearch)))
})

var _ = It("accepts the complete planner tool vocabulary", func() {
	input := plannerFixtureInput()
	plannerTools := plannerFixtureAllowedToolNames()

	input.AllowedTools = plannerTools
	input.RequiredTools = []string{"history_range", "history_symbol", "submit_plan"}
	input.Protocol.VisibleTools = plannerTools
	input.Protocol.CallableTools = plannerTools
	input.Protocol.CallableToolsSpecified = true

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred(), "compile the complete planner vocabulary")

	for _, tool := range []string{"blame_range", "context_impact", "harness_feedback", "history_range", "history_symbol"} {
		Expect(result.Protocol.VisibleTools).To(ContainElement(tool), "visible planner tool vocabulary")
		Expect(result.Protocol.CallableTools).To(ContainElement(tool), "callable planner tool vocabulary")
	}

	Expect(result.Contract.Capabilities.AllowedEffects).To(ContainElement(autonomy.SideEffectAdditive),
		"planner vocabulary should allow additive terminal/reporting effects")
})

type invalidProtocolVocabularyCase struct {
	name   string
	mutate func(*PlanExecutePlannerInput)
}

func invalidProtocolVocabularyCases() []invalidProtocolVocabularyCase {
	return []invalidProtocolVocabularyCase{
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
	}
}

var _ = DescribeTable("rejects invalid planner protocol vocabulary", invalidProtocolVocabularyTableArgs()...)

func invalidProtocolVocabularyTableArgs() []any {
	cases := invalidProtocolVocabularyCases()
	args := []any{func(tc invalidProtocolVocabularyCase) {
		input := plannerFixtureInput()
		tc.mutate(&input)

		_, err := CompilePlanExecutePlanner(input)
		Expect(err).To(HaveOccurred(), "invalid planner protocol input should be rejected")
	}}
	for _, tc := range cases {
		args = append(args, Entry(tc.name, tc))
	}

	return args
}

var _ = It("clamps negative runtime budgets and defaults report-only enforcement", func() {
	input := plannerFixtureInput()

	input.Runtime.EnforcementMode = ""
	input.Runtime.ThinkingBudget = -1
	input.Runtime.EffectiveMaxTokens = -100

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Runtime.EnforcementMode).To(Equal("report_only"))
	Expect(result.Runtime.ThinkingBudget).To(BeZero())
	Expect(result.Runtime.EffectiveMaxTokens).To(BeZero())

	for _, rule := range result.Contract.ToolPolicy {
		Expect(rule.Source).To(ContainSubstring("report_only"), "tool policy source should retain report-only provenance")
		Expect(rule.ReasonCode).To(ContainSubstring("report_only"), "tool policy reason should retain report-only provenance")
	}
})

var _ = It("keeps explicit degradation fallback associations", func() {
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
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Runtime.DegradationDecisions).To(HaveLen(2), "one normalized decision should remain per explicit invariant")

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	Expect(decisions["runtime.queue_unavailable"].AuthorizedFallbacks).To(Equal([]string{"direct_model_call"}))
	Expect(decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks).To(Equal([]string{"textual_tool_contract"}))
	Expect(decisions["runtime.queue_unavailable"].AuthorizedFallbacks).NotTo(ContainElement("legacy_global_fallback"))
	Expect(decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks).NotTo(ContainElement("legacy_global_fallback"))
})

var _ = It("pairs legacy degraded invariants with fallbacks by position", func() {
	input := plannerFixtureInput()

	input.Runtime.DegradedInvariants = []string{"runtime.queue_unavailable", "runtime.native_tools_unavailable"}
	input.Runtime.AuthorizedFallback = []string{"direct_model_call", "textual_tool_contract"}
	input.Runtime.DegradationDecisions = nil

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	Expect(decisions["runtime.queue_unavailable"].AuthorizedFallbacks).To(Equal([]string{"direct_model_call"}))
	Expect(decisions["runtime.native_tools_unavailable"].AuthorizedFallbacks).To(Equal([]string{"textual_tool_contract"}))
})

var _ = It("does not authorize a legacy degradation without a fallback", func() {
	input := plannerFixtureInput()

	input.Runtime.DegradedInvariants = []string{"runtime.queue_unavailable"}
	input.Runtime.AuthorizedFallback = nil
	input.Runtime.DegradationDecisions = nil

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())

	decisions := degradationDecisionsByInvariant(result.Runtime.DegradationDecisions)
	Expect(decisions["runtime.queue_unavailable"].Decision).To(Equal("degraded_no_fallback"))
	Expect(decisions["runtime.queue_unavailable"].AuthorizedFallbacks).To(BeEmpty())
})

var _ = It("derives safe alternatives from terminal response shapes", func() {
	input := plannerFixtureInput()

	input.Protocol.TerminalResponseShapes = []string{"submit_plan"}
	input.Protocol.CallableActionClasses = []string{"submit_plan"}

	result, err := CompilePlanExecutePlanner(input)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.Protocol.SafeAlternatives).To(Equal([]string{"submit_plan"}))
})

var _ = It("keeps distinct path-role authorities for the same path", func() {
	result := compilePlannerFixture()
	roleByPath := make(map[string][]autonomy.PathRoleKind)

	for _, role := range result.Contract.PathRoles {
		roleByPath[role.PathPattern] = append(roleByPath[role.PathPattern], role.Role)
	}

	assertRole(roleByPath, "internal/runtime/agent/runner.go", autonomy.PathRoleRequiredEdit)
	assertRole(roleByPath, "internal/runtime/agent/runner.go", autonomy.PathRoleAllowedEdit)
	assertRole(roleByPath, "internal/contracts/autonomy/contract.go", autonomy.PathRoleRequiredEdit)
	assertRole(roleByPath, "internal/contracts/autonomy/contract.go", autonomy.PathRoleGeneratedArtifact)
	assertRole(roleByPath, "internal/runtime/agent/phase_contract.go", autonomy.PathRoleEvidenceReadonly)
	assertRole(roleByPath, ".git/**", autonomy.PathRoleProtected)
})

func compilePlannerFixture() CompileResult {
	result, err := CompilePlanExecutePlanner(plannerFixtureInput())
	Expect(err).NotTo(HaveOccurred(), "compile the canonical planner fixture")

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
			MaxToolSteps:             12,
			MaxModelCalls:            3,
			MaxOperationalModelCalls: 5,
			MaxContextMessages:       40,
			MaxContextChars:          120000,
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
		},
		ToolMetadata: plannerFixtureToolMetadata(),
		MetadataTags: []string{"unit1"},
	}
}

func plannerFixtureToolMetadata() []PlanExecutePlannerToolMetadata {
	return []PlanExecutePlannerToolMetadata{
		{Tool: "apply_patch", Capabilities: []autonomy.Capability{autonomy.CapabilityApplyPatch}, SideEffect: autonomy.SideEffectAdditive},
		{Tool: "blame_range", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "code_navigate", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "context_impact", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "context_pack", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "context_query", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "harness_feedback", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectAdditive, PlannerAllowed: true},
		{Tool: "history_range", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "history_symbol", Capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "read_file", Capabilities: []autonomy.Capability{autonomy.CapabilityBoundedRead}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "search_content", Capabilities: []autonomy.Capability{autonomy.CapabilityLiteralSearch}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "search_files", Capabilities: []autonomy.Capability{autonomy.CapabilityLiteralSearch}, SideEffect: autonomy.SideEffectReadOnly, PlannerAllowed: true},
		{Tool: "shell", Capabilities: []autonomy.Capability{autonomy.CapabilityShellVerification}, SideEffect: autonomy.SideEffectUnknown},
		{Tool: "source_sanity", Capabilities: []autonomy.Capability{autonomy.CapabilityShellVerification}, SideEffect: autonomy.SideEffectUnknown},
		{Tool: "submit_plan", Capabilities: []autonomy.Capability{autonomy.CapabilityPlanSubmission}, SideEffect: autonomy.SideEffectAdditive, PlannerAllowed: true},
		{Tool: "task", Capabilities: []autonomy.Capability{autonomy.CapabilityDelegation}, SideEffect: autonomy.SideEffectAdditive},
	}
}

func plannerFixtureAllowedToolNames() []string {
	var tools []string
	for _, entry := range plannerFixtureToolMetadata() {
		if entry.PlannerAllowed {
			tools = append(tools, entry.Tool)
		}
	}

	return sortedStrings(tools)
}

func findActiveAuthorityDecision(result CompileResult) autonomy.PolicyDecision {
	for _, decision := range result.Contract.PolicyDecisions {
		if decision.InvariantID == InvariantActiveAuthority {
			return decision
		}
	}

	Fail("missing active-authority policy decision in compiled contract")

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

func assertRole(roleByPath map[string][]autonomy.PathRoleKind, path string, role autonomy.PathRoleKind) {
	Expect(roleByPath).To(HaveKey(path), "compiled path roles should include %q", path)
	Expect(roleByPath[path]).To(ContainElement(role), "compiled roles for %q", path)
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
