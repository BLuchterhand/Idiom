package agent

import (
	"slices"
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

const turnPhaseContractSchemaVersion = "idiom.turn_phase_contract.v1"

const (
	turnPhaseRequiredSource              = "plan_execute.required_source"
	turnPhaseCandidateSource             = "plan_execute.candidate_source"
	turnPhaseSubmitReadyNativeRequired   = "plan_execute.submit_ready.native_required"
	turnPhaseSubmitReadyTextual          = "plan_execute.submit_ready.textual"
	turnPhaseMalformedSubmitPlanRecovery = "plan_execute.malformed_submit_plan_recovery"
	turnPhaseStaleAnchorRefresh          = "plan_execute.stale_anchor_refresh"
	turnPhaseReviewRepairTerminal        = "plan_execute.review_repair_terminal"
	turnPhaseImplementationTargetSource  = "implementation.target_source"
	turnPhaseImplementationWriteReady    = "implementation.write_ready"
	turnPhaseImplementationVerifyReady   = "implementation.verify_ready"
	turnPhaseProductAcceptanceCloseout   = "product.acceptance_closeout"
	turnPhaseArtifactFinalAnswer         = "artifact.final_answer"
	turnPhaseGeneric                     = "generic"
)

const (
	actionRead             = "read"
	actionSearch           = "search"
	actionSubmitPlan       = "submit_plan"
	actionPerEditWrite     = "per_edit_write"
	actionDeliverableWrite = "deliverable_write"
	actionSourceSatisfied  = "source_satisfied"
	actionBlocker          = "blocker"
	actionVerify           = "verify"
	actionFinalAnswer      = "final_answer"
	actionReviewPass       = "review_pass"
	actionGenericProbe     = "generic_probe"
	actionProseFinal       = "prose_final"
)

// TurnPhaseContract is a shadow projection over the current runner/runtime
// state. It describes the active phase contract without mutating tool
// visibility, emitting events, or replacing existing gates.
type TurnPhaseContract struct {
	SchemaVersion    string
	ContractID       string
	PhaseID          string
	PhaseEpoch       int
	ModelCallID      string
	Step             int
	Phase            string
	DerivationReason string
	InvariantIDs     []string

	PermissionProfile string
	AllowedTools      []string
	HiddenTools       []string
	RequiredNext      []string
	InvalidNext       []string
	StopConditions    []string
	TerminalOutcomes  []string
	Constraints       []string

	ActionPolicy    TurnActionClassPolicy
	RuntimeCall     RuntimeCallContract
	EvidenceState   PhaseEvidenceState
	EnforcementMode string
}

type TurnActionClassPolicy struct {
	AllowedActionClasses   []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
	CallableToolNames      []string
}

type RuntimeCallContract struct {
	BackendKind              string
	ModelAlias               string
	ToolTransportMode        string
	ParserContract           string
	ResponseFormat           string
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ThinkingPolicy           string
	MaxTokens                int
	ExpectedVisibleOutput    string
	ExpressibleActionClasses []string
}

func (contract TurnPhaseContract) autonomyEventPayload() autonomy.TurnPhaseContractEvent {
	return autonomy.TurnPhaseContractEvent{
		SchemaVersion:    contract.SchemaVersion,
		PhaseID:          contract.PhaseID,
		PhaseEpoch:       contract.PhaseEpoch,
		ModelCallID:      contract.ModelCallID,
		Step:             contract.Step,
		Phase:            contract.Phase,
		DerivationReason: contract.DerivationReason,
		InvariantIDs:     append([]string(nil), contract.InvariantIDs...),

		PermissionProfile: contract.PermissionProfile,
		AllowedTools:      append([]string(nil), contract.AllowedTools...),
		HiddenTools:       append([]string(nil), contract.HiddenTools...),
		RequiredNext:      append([]string(nil), contract.RequiredNext...),
		InvalidNext:       append([]string(nil), contract.InvalidNext...),
		StopConditions:    append([]string(nil), contract.StopConditions...),
		TerminalOutcomes:  append([]string(nil), contract.TerminalOutcomes...),
		Constraints:       append([]string(nil), contract.Constraints...),

		ActionPolicy: autonomy.TurnActionClassPolicyEvent{
			AllowedActionClasses:   append([]string(nil), contract.ActionPolicy.AllowedActionClasses...),
			InvalidActionClasses:   append([]string(nil), contract.ActionPolicy.InvalidActionClasses...),
			TerminalResponseShapes: append([]string(nil), contract.ActionPolicy.TerminalResponseShapes...),
			CallableToolNames:      append([]string(nil), contract.ActionPolicy.CallableToolNames...),
		},
		RuntimeCall: autonomy.RuntimeCallContractEvent{
			BackendKind:              contract.RuntimeCall.BackendKind,
			ModelAlias:               contract.RuntimeCall.ModelAlias,
			ToolTransportMode:        contract.RuntimeCall.ToolTransportMode,
			ParserContract:           contract.RuntimeCall.ParserContract,
			ResponseFormat:           contract.RuntimeCall.ResponseFormat,
			Temperature:              contract.RuntimeCall.Temperature,
			TopP:                     contract.RuntimeCall.TopP,
			TopK:                     contract.RuntimeCall.TopK,
			MinP:                     contract.RuntimeCall.MinP,
			PresencePenalty:          contract.RuntimeCall.PresencePenalty,
			RepetitionPenalty:        contract.RuntimeCall.RepetitionPenalty,
			ToolChoice:               contract.RuntimeCall.ToolChoice,
			ThinkingPolicy:           contract.RuntimeCall.ThinkingPolicy,
			MaxTokens:                contract.RuntimeCall.MaxTokens,
			ExpectedVisibleOutput:    contract.RuntimeCall.ExpectedVisibleOutput,
			ExpressibleActionClasses: append([]string(nil), contract.RuntimeCall.ExpressibleActionClasses...),
		},
		EvidenceState: autonomy.PhaseEvidenceStateEvent{
			AcceptanceRefs:           append([]string(nil), contract.EvidenceState.AcceptanceRefs...),
			TDDObligationRefs:        append([]string(nil), contract.EvidenceState.TDDObligationRefs...),
			VerificationEvidenceRefs: append([]string(nil), contract.EvidenceState.VerificationEvidenceRefs...),
			VerificationStatus:       contract.EvidenceState.VerificationStatus,
			ProductWorkflowState:     contract.EvidenceState.ProductWorkflowState,
			AtomStatus:               contract.EvidenceState.AtomStatus,
			RecoveryPacketRefs:       append([]string(nil), contract.EvidenceState.RecoveryPacketRefs...),
			ResidualBlockers:         append([]string(nil), contract.EvidenceState.ResidualBlockers...),
		},
		EnforcementMode: contract.EnforcementMode,
	}
}

type PhaseEvidenceState struct {
	AcceptanceRefs           []string
	TDDObligationRefs        []string
	VerificationEvidenceRefs []string
	VerificationStatus       string
	ProductWorkflowState     string
	AtomStatus               string
	RecoveryPacketRefs       []string
	ResidualBlockers         []string
}

type TurnPhaseContractInput struct {
	ContractID  string
	PhaseEpoch  int
	ModelCallID string
	Step        int
	PhaseHint   string

	Brief       workerruntime.WorkerRuntimeBrief
	Results     []workspace.ToolResult
	LastRequest workspace.ToolRequest
	LastResult  workspace.ToolResult

	BaseAllowedTools []string
	HiddenTools      []string

	BackendKind       string
	ModelAlias        string
	ToolTransportMode string
	ParserContract    string
	ResponseFormat    string
	Temperature       float64
	TopP              float64
	TopK              int
	MinP              float64
	PresencePenalty   float64
	RepetitionPenalty float64
	ToolChoice        string
	ThinkingPolicy    string
	MaxTokens         int

	SupportsNativeTools bool
	TextualToolParser   bool

	AcceptanceRefs           []string
	TDDObligationRefs        []string
	VerificationEvidenceRefs []string
	VerificationStatus       string
	ProductWorkflowState     string
	AtomStatus               string
	RecoveryPacketRefs       []string
	ResidualBlockers         []string
}

func deriveTurnPhaseContract(input TurnPhaseContractInput) TurnPhaseContract {
	brief := workerruntime.NormalizeRuntimeBrief(input.Brief)
	baseTools := phaseContractBaseTools(input, brief)
	contract := TurnPhaseContract{
		SchemaVersion:     turnPhaseContractSchemaVersion,
		ContractID:        strings.TrimSpace(input.ContractID),
		PhaseEpoch:        input.PhaseEpoch,
		ModelCallID:       strings.TrimSpace(input.ModelCallID),
		Step:              input.Step,
		PermissionProfile: brief.PermissionProfile,
		AllowedTools:      uniquePhaseStrings(baseTools),
		HiddenTools:       uniquePhaseStrings(input.HiddenTools),
		RequiredNext:      uniquePhaseStrings(brief.RequiredNext),
		InvalidNext:       uniquePhaseStrings(brief.InvalidActions),
		StopConditions:    uniquePhaseStrings(brief.StopConditions),
		Constraints:       uniquePhaseStrings(brief.Constraints),
		RuntimeCall: RuntimeCallContract{
			BackendKind:           strings.TrimSpace(input.BackendKind),
			ModelAlias:            strings.TrimSpace(input.ModelAlias),
			ToolTransportMode:     strings.TrimSpace(input.ToolTransportMode),
			ParserContract:        strings.TrimSpace(input.ParserContract),
			ResponseFormat:        strings.TrimSpace(input.ResponseFormat),
			Temperature:           input.Temperature,
			TopP:                  input.TopP,
			TopK:                  input.TopK,
			MinP:                  input.MinP,
			PresencePenalty:       input.PresencePenalty,
			RepetitionPenalty:     input.RepetitionPenalty,
			ToolChoice:            strings.TrimSpace(input.ToolChoice),
			ThinkingPolicy:        strings.TrimSpace(input.ThinkingPolicy),
			MaxTokens:             input.MaxTokens,
			ExpectedVisibleOutput: expectedVisibleOutput(input),
		},
		EvidenceState: PhaseEvidenceState{
			AcceptanceRefs:           uniquePhaseStrings(input.AcceptanceRefs),
			TDDObligationRefs:        uniquePhaseStrings(input.TDDObligationRefs),
			VerificationEvidenceRefs: uniquePhaseStrings(input.VerificationEvidenceRefs),
			VerificationStatus:       strings.TrimSpace(input.VerificationStatus),
			ProductWorkflowState:     strings.TrimSpace(input.ProductWorkflowState),
			AtomStatus:               strings.TrimSpace(input.AtomStatus),
			RecoveryPacketRefs:       uniquePhaseStrings(input.RecoveryPacketRefs),
			ResidualBlockers:         uniquePhaseStrings(input.ResidualBlockers),
		},
		EnforcementMode: "observe_only",
	}

	applyTurnPhase(&contract, input, brief, baseTools)
	if phaseContractTextualOnly(input, contract) {
		contract.AllowedTools = nil
		contract.ActionPolicy.CallableToolNames = nil
	}
	contract.AllowedTools = uniquePhaseStrings(contract.AllowedTools)
	contract.HiddenTools = uniquePhaseStrings(append(contract.HiddenTools, hiddenPhaseTools(baseTools, contract.AllowedTools)...))
	contract.RequiredNext = uniquePhaseStrings(contract.RequiredNext)
	contract.InvalidNext = uniquePhaseStrings(contract.InvalidNext)
	contract.StopConditions = uniquePhaseStrings(contract.StopConditions)
	contract.TerminalOutcomes = uniquePhaseStrings(contract.TerminalOutcomes)
	contract.Constraints = uniquePhaseStrings(contract.Constraints)
	contract.ActionPolicy.AllowedActionClasses = uniquePhaseStrings(contract.ActionPolicy.AllowedActionClasses)
	contract.ActionPolicy.InvalidActionClasses = uniquePhaseStrings(contract.ActionPolicy.InvalidActionClasses)
	contract.ActionPolicy.TerminalResponseShapes = uniquePhaseStrings(contract.ActionPolicy.TerminalResponseShapes)
	contract.ActionPolicy.CallableToolNames = uniquePhaseStrings(contract.ActionPolicy.CallableToolNames)
	contract.RuntimeCall.ExpressibleActionClasses = uniquePhaseStrings(contract.RuntimeCall.ExpressibleActionClasses)
	return contract
}

func applyTurnPhase(contract *TurnPhaseContract, input TurnPhaseContractInput, brief workerruntime.WorkerRuntimeBrief, baseTools []string) {
	planningTransport := planExecutePlanningTransportFromPhaseInput(input)
	planningState := DerivePlanExecutePlanningState(brief, input.Results, planningTransport)
	switch {
	case strings.TrimSpace(input.PhaseHint) == turnPhaseProductAcceptanceCloseout:
		applyProductAcceptanceCloseout(contract)
	case artifactFinalAnswerBrief(brief):
		applyArtifactFinalAnswer(contract)
	case planningState.Mode == PlanExecutePlanningModeSubmitCorrection:
		applySubmitReady(contract, input, baseTools, planningState)
	case planThenExecuteRejectedPlanNeedsSourceRefresh(brief, input.Results):
		applyStaleAnchorRefresh(contract, input, baseTools)
	case planThenExecuteSubmitReadyParseFailureSeen(brief, input.Results):
		applyMalformedSubmitPlanRecovery(contract, input, baseTools)
	case planThenExecuteSubmitPlanOnlyReady(brief, input.Results):
		applySubmitReady(contract, input, baseTools, planningState)
	case planThenExecuteUnreadRequiredSourceNudge(brief, input.Results) != "":
		applyRequiredSource(contract, baseTools, planningState)
	case planThenExecuteCandidateSourcePending(brief, input.Results):
		applyCandidateSource(contract, baseTools, planningState)
	case strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily &&
		(strings.TrimSpace(input.PhaseHint) == turnPhaseReviewRepairTerminal ||
			len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) > 0):
		applyReviewRepairTerminal(contract, brief, baseTools)
	case implementationTargetSourceWriteReady(brief, input.Results):
		applyImplementationTargetWriteReady(contract, baseTools)
	case implementationTargetSourcePending(brief, input.Results):
		applyImplementationTargetSource(contract, baseTools)
	case implementationScopedEditSurfaceActive(brief, input.Results):
		applyImplementationWriteReady(contract, baseTools)
	case runnerBriefRequiresMutation(brief) &&
		(hasPendingVerificationAfterWrites(input.Results, brief) ||
			sourceEditsNeedSemanticVerification(brief, input.Results)):
		applyImplementationVerifyReady(contract, brief, baseTools)
	default:
		applyGenericTurnPhase(contract, brief, input.Results)
	}
}

func applyRequiredSource(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	contract.PhaseID = firstPhaseString(state.PhaseID, turnPhaseRequiredSource)
	contract.Phase = firstPhaseString(state.Phase, "needs_source_body")
	contract.DerivationReason = firstPhaseString(state.DerivationReason, "plan_execute_required_source_pending")
	contract.AllowedTools = filterPhaseTools(baseTools, state.CallableTools...)
	contract.RequiredNext = []string{"bounded source-body read for the next unread required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"submit_plan before source-body evidence", "per-edit write in planner phase", "broad search instead of bounded required-path source read"}
	contract.TerminalOutcomes = []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   append([]string(nil), state.AllowedActionClasses...),
		InvalidActionClasses:   append([]string(nil), state.InvalidActionClasses...),
		TerminalResponseShapes: append([]string(nil), state.TerminalResponseShapes...),
		CallableToolNames:      filterPhaseTools(baseTools, state.CallableTools...),
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "source_refresh")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionBlocker}
}

func applyCandidateSource(contract *TurnPhaseContract, baseTools []string, state PlanExecutePlanningState) {
	contract.PhaseID = firstPhaseString(state.PhaseID, turnPhaseCandidateSource)
	contract.Phase = firstPhaseString(state.Phase, "candidate_source_pending")
	contract.DerivationReason = firstPhaseString(state.DerivationReason, "plan_execute_unscoped_candidate_source_pending")
	contract.AllowedTools = filterPhaseTools(baseTools, state.CallableTools...)
	contract.RequiredNext = []string{"current typed state: " + string(state.Mode) + "; valid terminal actions: Decision: source-satisfied with observed current-state evidence or <<QUESTION>> with the exact blocker; if no source-body evidence exists yet, use one bounded localization/read action"}
	contract.InvalidNext = []string{"submit_plan before source-body evidence for each planned edit path", "per-edit write in planner phase", "verify/build/test in planner phase"}
	contract.TerminalOutcomes = []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	if slices.Contains(state.AllowedActionClasses, actionSubmitPlan) {
		contract.RequiredNext = []string{"current typed state: " + string(state.Mode) + "; valid terminal actions: submit_plan, Decision: source-satisfied, or <<QUESTION>> with the exact blocker; textual submit_plan must be one visible JSON object with real rationale and executable edits, not placeholders; continue targeted source/test localization only for a named missing edit-safety fact"}
		contract.InvalidNext = []string{"submit_plan edits without source-body evidence for each edited path", "per-edit write in planner phase", "verify/build/test in planner phase"}
		contract.TerminalOutcomes = append(contract.TerminalOutcomes, "submit_plan")
	}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   append([]string(nil), state.AllowedActionClasses...),
		InvalidActionClasses:   append([]string(nil), state.InvalidActionClasses...),
		TerminalResponseShapes: append([]string(nil), state.TerminalResponseShapes...),
		CallableToolNames:      filterPhaseTools(baseTools, state.CallableTools...),
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "source_refresh")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionBlocker}
	if slices.Contains(state.AllowedActionClasses, actionSubmitPlan) {
		contract.RuntimeCall.ExpressibleActionClasses = append(contract.RuntimeCall.ExpressibleActionClasses, actionSubmitPlan, actionSourceSatisfied)
	}
	if len(contract.AllowedTools) == 1 && contract.AllowedTools[0] == "submit_plan" {
		contract.RequiredNext = []string{"read/search/navigation tools are disabled for this recovery turn; the next visible action must be exactly one terminal planner outcome: submit_plan, Decision: source-satisfied, or <<QUESTION>> with the exact blocker"}
		contract.InvalidNext = []string{"read/search/source-refresh after terminal recovery", "non-parser-readable action", "per-edit write in planner phase", "verify/build/test in planner phase"}
		contract.ActionPolicy.AllowedActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionRead, actionSearch, actionGenericProbe, actionPerEditWrite, actionVerify}
		contract.ActionPolicy.TerminalResponseShapes = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
		contract.ActionPolicy.CallableToolNames = []string{"submit_plan"}
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
	}
}

func planExecutePlanningTransportFromPhaseInput(input TurnPhaseContractInput) PlanExecutePlanningTransport {
	return planExecuteRuntimeCallPolicyFromPhaseInput(input).PlanningTransport()
}

func applySubmitReady(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string, state PlanExecutePlanningState) {
	transport := planExecutePlanningTransportFromPhaseInput(input)
	if state.Mode != PlanExecutePlanningModeSubmitReady && state.Mode != PlanExecutePlanningModeSubmitCorrection {
		state = PlanExecutePlanningState{Mode: PlanExecutePlanningModeSubmitReady}
		state.applyProjection(transport)
	}
	nativeRequired := planExecuteSubmitReadyNativeRequired(transport)
	contract.PhaseID = firstPhaseString(state.PhaseID, turnPhaseSubmitReadyTextual)
	contract.Phase = firstPhaseString(state.Phase, "submit_ready_textual")
	contract.DerivationReason = firstPhaseString(state.DerivationReason, "plan_execute_source_ready")
	contract.AllowedTools = filterPhaseTools(baseTools, state.ParserVisibleTools...)
	contract.RequiredNext = []string{"submit_plan with one ordered edit entry for every required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"read/search/source-refresh after plan-execute submit_plan readiness", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
	contract.TerminalOutcomes = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   append([]string(nil), state.AllowedActionClasses...),
		InvalidActionClasses:   append([]string(nil), state.InvalidActionClasses...),
		TerminalResponseShapes: append([]string(nil), state.TerminalResponseShapes...),
		CallableToolNames:      filterPhaseTools(baseTools, state.CallableTools...),
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "submit_plan_json")
	contract.RuntimeCall.ResponseFormat = firstPhaseString(contract.RuntimeCall.ResponseFormat, "json")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
	if nativeRequired {
		contract.PhaseID = turnPhaseSubmitReadyNativeRequired
		contract.Phase = "submit_ready_native"
		contract.RequiredNext = []string{"native submit_plan tool call"}
		contract.InvalidNext = append(contract.InvalidNext, "non-tool prose while ToolChoice=required")
		contract.TerminalOutcomes = []string{"submit_plan"}
		contract.RuntimeCall.ToolTransportMode = firstPhaseString(contract.RuntimeCall.ToolTransportMode, "native")
		contract.RuntimeCall.ToolChoice = "required"
		contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "native_submit_plan")
		contract.RuntimeCall.ResponseFormat = ""
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan}
	}
}

func applyMalformedSubmitPlanRecovery(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string) {
	applySubmitReady(contract, TurnPhaseContractInput{TextualToolParser: true, ParserContract: input.ParserContract}, baseTools, PlanExecutePlanningState{})
	contract.PhaseID = turnPhaseMalformedSubmitPlanRecovery
	contract.Phase = "malformed_submit_plan_recovery"
	contract.DerivationReason = "submit_ready_parse_failure"
	contract.RequiredNext = []string{"parser-readable submit_plan with one ordered edit entry for every required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.RuntimeCall.ToolTransportMode = firstPhaseString(input.ToolTransportMode, "textual")
	contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "submit_plan_json")
	contract.RuntimeCall.ResponseFormat = firstPhaseString(input.ResponseFormat, "json")
	contract.RuntimeCall.ExpectedVisibleOutput = "visible_text_tool_request"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
}

func applyStaleAnchorRefresh(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string) {
	contract.PhaseID = turnPhaseStaleAnchorRefresh
	contract.Phase = "anchor_refresh"
	contract.DerivationReason = "rejected_submit_plan_needs_source_refresh"
	contract.AllowedTools = filterPhaseTools(baseTools, planThenExecuteAnchorRefreshToolNames()...)
	contract.RequiredNext = []string{"bounded source-body refresh for the stale or missing anchor, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"submit_plan before bounded anchor refresh", "broad search", "per-edit write in planner phase"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionBlocker},
		InvalidActionClasses:   []string{actionSubmitPlan, actionSearch, actionPerEditWrite},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "source_refresh")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionBlocker}
}

func applyReviewRepairTerminal(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string) {
	contract.PhaseID = turnPhaseReviewRepairTerminal
	contract.Phase = "review_repair_terminal"
	contract.DerivationReason = "plan_execute_review_repair_terminal"
	contract.AllowedTools = filterPhaseTools(baseTools, "submit_plan")
	contract.InvalidNext = []string{"read/search/verify after review-repair terminal readiness", "generic probe", "prose-only final answer"}
	if len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) > 0 {
		contract.RequiredNext = []string{"corrective submit_plan, or <<QUESTION>> with the exact failed obligation blocker"}
		contract.InvalidNext = append(contract.InvalidNext, "Decision: pass while failed phase obligations remain")
		contract.TerminalOutcomes = []string{"submit_plan", "<<QUESTION>>:"}
		contract.ActionPolicy = TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionSubmitPlan, actionBlocker},
			InvalidActionClasses:   []string{actionReviewPass, actionRead, actionSearch, actionVerify, actionGenericProbe, actionProseFinal},
			TerminalResponseShapes: []string{"submit_plan", "<<QUESTION>>:"},
			CallableToolNames:      contract.AllowedTools,
		}
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionBlocker}
		return
	}
	contract.RequiredNext = []string{"Decision: pass with checklist, corrective submit_plan, or <<QUESTION>>"}
	contract.TerminalOutcomes = []string{"Decision: pass", "submit_plan", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionReviewPass, actionSubmitPlan, actionBlocker},
		InvalidActionClasses:   []string{actionRead, actionSearch, actionVerify, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: []string{"Decision: pass", "submit_plan", "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionReviewPass, actionSubmitPlan, actionBlocker}
}

func applyImplementationTargetSource(contract *TurnPhaseContract, baseTools []string) {
	contract.PhaseID = turnPhaseImplementationTargetSource
	contract.Phase = "target_source_pending"
	contract.DerivationReason = "guarded_mutation_target_source_pending"
	contract.AllowedTools = implementationTargetSourceAllowedTools(baseTools)
	contract.RequiredNext = []string{"localize the concrete edit target, read bounded source-body evidence with named_gap for the missing edit-safety fact, make one parser-readable write only after exact source-body evidence and replacement shape are known, or <<QUESTION>> with the exact blocker/no-op evidence"}
	contract.InvalidNext = []string{"final answer before a successful write, no-op, or blocker", "write before exact source-body evidence and replacement shape", "verification before a successful write", "generic probe without a named missing target or edit-safety fact"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionFinalAnswer, actionVerify, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "target_source_or_write")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_tool_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionSearch, actionPerEditWrite, actionBlocker}
}

func applyImplementationTargetWriteReady(contract *TurnPhaseContract, baseTools []string) {
	contract.PhaseID = turnPhaseImplementationWriteReady
	contract.Phase = "target_write_ready"
	contract.DerivationReason = "guarded_mutation_target_source_ready"
	contract.AllowedTools = implementationTargetWriteReadyAllowedTools(baseTools)
	contract.RequiredNext = []string{"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"}
	contract.InvalidNext = []string{"additional read/search after source-body evidence", "verification before a successful write", "generic probe", "prose-only final answer"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionRead, actionSearch, actionVerify, actionGenericProbe, actionProseFinal, actionFinalAnswer},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "target_write_or_blocker")
	contract.RuntimeCall.ExpectedVisibleOutput = "parser_readable_write_or_blocker"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionPerEditWrite, actionBlocker}
}

func applyImplementationWriteReady(contract *TurnPhaseContract, baseTools []string) {
	contract.PhaseID = turnPhaseImplementationWriteReady
	contract.Phase = "write_ready"
	contract.DerivationReason = "implementation_required_paths_remaining"
	contract.AllowedTools = implementationScopedEditAllowedTools(baseTools)
	contract.RequiredNext = []string{"scoped required-path edit or <<QUESTION>> with the exact edit blocker"}
	contract.InvalidNext = []string{"broad search", "unrelated per-edit write", "generic probe"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionPerEditWrite, actionBlocker},
		InvalidActionClasses:   []string{actionSearch, actionGenericProbe},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionPerEditWrite, actionBlocker}
}

func applyImplementationVerifyReady(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, baseTools []string) {
	contract.PhaseID = turnPhaseImplementationVerifyReady
	contract.Phase = "verification"
	contract.DerivationReason = "implementation_post_write_verification_pending"
	contract.AllowedTools = filterPhaseTools(baseTools, "source_sanity", "shell", "git_diff", "git_status")
	contract.RequiredNext = []string{"source_sanity for changed source files, one relevant source-aware test/build/check, or <<QUESTION>> with the exact verification blocker"}
	contract.InvalidNext = []string{"final answer before post-write verification", "more source reads/search before naming a verification blocker", "additional edits before verification unless verification fails"}
	contract.TerminalOutcomes = []string{"<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionVerify, actionBlocker},
		InvalidActionClasses:   []string{actionFinalAnswer, actionRead, actionSearch, actionPerEditWrite, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: []string{"<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "verification_tool_or_blocker")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionVerify, actionBlocker}
}

func applyArtifactFinalAnswer(contract *TurnPhaseContract) {
	contract.PhaseID = turnPhaseArtifactFinalAnswer
	contract.Phase = "artifact_final_answer"
	contract.DerivationReason = "artifact_no_tools_final_answer"
	contract.AllowedTools = nil
	contract.RequiredNext = []string{"artifact body as final answer, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"tool call", "workspace read/search", "workspace write", "shell command", "submit_plan", "delegated task", "generic probe", "process narration before artifact"}
	contract.StopConditions = []string{"artifact_body_returned", "blocked_by_missing_information"}
	contract.TerminalOutcomes = []string{"artifact final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
		InvalidActionClasses:   []string{actionRead, actionSearch, actionSubmitPlan, actionPerEditWrite, actionDeliverableWrite, actionVerify, actionGenericProbe},
		TerminalResponseShapes: []string{"artifact final answer", "<<QUESTION>>:"},
		CallableToolNames:      nil,
	}
	contract.RuntimeCall.ExpectedVisibleOutput = "final_answer_artifact"
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionFinalAnswer, actionBlocker}
}

func applyProductAcceptanceCloseout(contract *TurnPhaseContract) {
	contract.PhaseID = turnPhaseProductAcceptanceCloseout
	contract.Phase = "terminal_ready"
	contract.DerivationReason = "product_acceptance_closeout"
	contract.AllowedTools = nil
	contract.RequiredNext = []string{"final answer or <<QUESTION>> with the exact unresolved obligation"}
	contract.InvalidNext = []string{"final answer while TDD, verification, product workflow, recovery, or review obligations remain open"}
	contract.TerminalOutcomes = []string{"final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
		InvalidActionClasses:   []string{},
		TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
		CallableToolNames:      nil,
	}
	if phaseEvidenceHasOpenObligation(contract.EvidenceState) {
		contract.ActionPolicy.AllowedActionClasses = []string{actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionFinalAnswer}
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionBlocker}
	}
	if len(contract.RuntimeCall.ExpressibleActionClasses) == 0 {
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionFinalAnswer, actionBlocker}
	}
}

func applyGenericTurnPhase(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	contract.PhaseID = turnPhaseGeneric
	contract.Phase = "generic"
	contract.DerivationReason = "no_specialized_phase_matched"
	contract.TerminalOutcomes = []string{"final answer", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
		InvalidActionClasses:   nil,
		TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	if brief.Readiness != nil && brief.Readiness.ReadyToVerify {
		contract.ActionPolicy.AllowedActionClasses = []string{actionVerify, actionBlocker}
		contract.ActionPolicy.InvalidActionClasses = []string{actionFinalAnswer}
	}
	if genericReadOnlyEvidenceReady(brief, results) {
		contract.Phase = "read_only_evidence_ready"
		contract.DerivationReason = "read_only_workspace_evidence_ready"
		contract.AllowedTools = nil
		contract.RequiredNext = []string{"final answer from the gathered bounded evidence, or <<QUESTION>> with the exact unsupported claim"}
		contract.InvalidNext = []string{"additional read/search without one exact unsupported claim", "workspace write", "shell/mutating command"}
		contract.ActionPolicy = TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionFinalAnswer, actionBlocker},
			InvalidActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite, actionVerify, actionGenericProbe},
			TerminalResponseShapes: []string{"final answer", "<<QUESTION>>:"},
			CallableToolNames:      nil,
		}
		contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)
		return
	}
	if genericReadOnlyEvidencePending(brief, contract.AllowedTools, results) {
		contract.Phase = "read_only_evidence_pending"
		contract.DerivationReason = "read_only_workspace_evidence_pending"
		contract.AllowedTools = genericReadOnlyEvidenceTools(contract.AllowedTools)
		contract.RequiredNext = []string{"bounded read/search evidence for the question, final answer once evidence is sufficient, or <<QUESTION>> with the exact blocker"}
		contract.InvalidNext = []string{"workspace write", "shell/mutating command", "final answer before sufficient evidence"}
		contract.ActionPolicy = TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionRead, actionSearch, actionBlocker},
			InvalidActionClasses:   []string{actionPerEditWrite, actionVerify, actionFinalAnswer},
			TerminalResponseShapes: []string{"<<QUESTION>>:"},
			CallableToolNames:      contract.AllowedTools,
		}
	}
	contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)
}

func genericReadOnlyEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !strings.EqualFold(strings.TrimSpace(brief.PermissionProfile), "read_only_workspace") {
		return false
	}
	if slices.Contains(brief.Constraints, "answer_readiness_reached") {
		return true
	}
	return conversationEvidenceSatisfied(brief, results)
}

func genericReadOnlyEvidencePending(brief workerruntime.WorkerRuntimeBrief, allowedTools []string, results []workspace.ToolResult) bool {
	if !strings.EqualFold(strings.TrimSpace(brief.PermissionProfile), "read_only_workspace") {
		return false
	}
	if brief.Readiness == nil || brief.Readiness.ReadyToAnswer {
		return false
	}
	if genericReadOnlyAnswerEvidenceReady(results) {
		return false
	}
	return len(genericReadOnlyEvidenceTools(allowedTools)) > 0
}

func genericReadOnlyAnswerEvidenceReady(results []workspace.ToolResult) bool {
	for _, result := range results {
		if concreteSourceEvidenceResult(result) {
			return true
		}
	}
	return readOnlyAnalysisReadyFromEvidence(results)
}

func genericReadOnlyEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := strings.TrimSpace(result.Tool)
		if tool == "code_navigate" && (!routedSourceReadOutputHasBody(result.Output) || len(readPathsFromToolResult(result)) == 0) {
			continue
		}
		switch tool {
		case "task", "context_pack", "context_query", "context_zoom", "discovery_query",
			"search_files", "search_content", "read_file", "code_navigate",
			"file_stats", "structural_search",
			"mcp:exa/web_fetch_exa", "mcp:exa/web_search_exa":
			return true
		}
	}
	return false
}

func genericReadOnlyEvidenceTools(tools []string) []string {
	return filterPhaseTools(tools,
		"task",
		"context_pack", "context_query", "context_zoom", "discovery_query",
		"search_files", "search_content", "read_file", "code_navigate",
		"file_stats", "structural_search",
		"mcp:exa/web_fetch_exa", "mcp:exa/web_search_exa",
	)
}

func artifactFinalAnswerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer"
}

func implementationTargetSourcePending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}
	if !guardedMutationBriefRequiresTargetSourceContract(brief) {
		return false
	}
	if len(brief.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	if implementationTargetSourceEvidenceReady(results) {
		return false
	}
	return brief.Readiness == nil || !brief.Readiness.ReadyToWrite
}

func implementationTargetWriteReadyAllowedTools(baseTools []string) []string {
	return filterPhaseTools(baseTools, "edit_file", "write_file", "append_file", "apply_patch", "mkdir", "go_move_declarations", "delegate_edit")
}

func implementationTargetWriteReadySurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return implementationTargetSourceWriteReady(brief, results)
}

func implementationTargetSourceWriteReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}
	if !guardedMutationBriefRequiresTargetSourceContract(brief) {
		return false
	}
	if len(brief.RequiredEditPaths) > 0 || len(brief.RequiredEditSnippets) > 0 {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	return implementationTargetSourceEvidenceReady(results)
}

func implementationTargetSourceEvidenceReady(results []workspace.ToolResult) bool {
	return len(concreteSourceBodyReadPaths(results)) == 1
}

func guardedMutationBriefRequiresTargetSourceContract(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	if family == "delegated_isolated_implementation" {
		return false
	}
	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))
	return permission == "guarded_workspace_write" ||
		permission == "workspace_write" ||
		permission == "write_enabled" ||
		strings.TrimSpace(brief.TaskFamily) == "implementation"
}

func phaseContractBaseTools(input TurnPhaseContractInput, brief workerruntime.WorkerRuntimeBrief) []string {
	if len(input.BaseAllowedTools) > 0 {
		return uniquePhaseStrings(input.BaseAllowedTools)
	}
	return uniquePhaseStrings(brief.AllowedTools)
}

func filterPhaseTools(tools []string, allowed ...string) []string {
	if len(tools) == 0 || len(allowed) == 0 {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, tool := range allowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowedSet[tool] = struct{}{}
		}
	}
	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if _, ok := allowedSet[tool]; ok {
			out = append(out, tool)
		}
	}
	return uniquePhaseStrings(out)
}

func implementationTargetSourceAllowedTools(baseTools []string) []string {
	writes := filterPhaseTools(baseTools,
		"edit_file", "write_file", "append_file", "apply_patch", "mkdir", "go_move_declarations", "delegate_edit",
	)
	reads := filterPhaseTools(baseTools,
		"list_files", "search_files", "search_content", "rg",
		"context_pack", "context_query", "context_zoom", "discovery_query",
		"read_file", "code_navigate", "read_only_capability",
	)
	return uniquePhaseStrings(append(writes, reads...))
}

func hiddenPhaseTools(baseTools []string, allowedTools []string) []string {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	var hidden []string
	for _, tool := range baseTools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := allowed[tool]; !ok {
			hidden = append(hidden, tool)
		}
	}
	return hidden
}

func firstPhaseString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func expectedVisibleOutput(input TurnPhaseContractInput) string {
	switch {
	case input.SupportsNativeTools && !input.TextualToolParser:
		return "native_tool_call"
	case input.TextualToolParser || strings.EqualFold(strings.TrimSpace(input.ToolTransportMode), "textual"):
		return "visible_text_tool_request"
	default:
		return ""
	}
}

func phaseContractTextualOnly(input TurnPhaseContractInput, contract TurnPhaseContract) bool {
	if !input.TextualToolParser && !strings.EqualFold(strings.TrimSpace(input.ToolTransportMode), "textual") {
		return false
	}
	switch contract.PhaseID {
	case turnPhaseSubmitReadyTextual, turnPhaseSubmitReadyNativeRequired,
		turnPhaseMalformedSubmitPlanRecovery, turnPhaseReviewRepairTerminal:
		return true
	default:
		return false
	}
}

func phaseEvidenceHasOpenObligation(state PhaseEvidenceState) bool {
	verificationStatus := strings.ToLower(strings.TrimSpace(state.VerificationStatus))
	return len(state.TDDObligationRefs) > 0 ||
		len(state.ResidualBlockers) > 0 ||
		len(state.RecoveryPacketRefs) > 0 ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "recovery_open") ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "incomplete") ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "skipped") ||
		verificationStatus == "missing" ||
		verificationStatus == "failed" ||
		verificationStatus == "pre_write"
}

func uniquePhaseStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
