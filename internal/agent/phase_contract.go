package agent

import (
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

const turnPhaseContractSchemaVersion = "idiom.turn_phase_contract.v1"

const (
	turnPhaseRequiredSource              = "plan_execute.required_source"
	turnPhaseSubmitReadyNativeRequired   = "plan_execute.submit_ready.native_required"
	turnPhaseSubmitReadyTextual          = "plan_execute.submit_ready.textual"
	turnPhaseMalformedSubmitPlanRecovery = "plan_execute.malformed_submit_plan_recovery"
	turnPhaseStaleAnchorRefresh          = "plan_execute.stale_anchor_refresh"
	turnPhaseReviewRepairTerminal        = "plan_execute.review_repair_terminal"
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
	if phaseContractTextualOnly(input) {
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
	switch {
	case strings.TrimSpace(input.PhaseHint) == turnPhaseProductAcceptanceCloseout:
		applyProductAcceptanceCloseout(contract)
	case artifactFinalAnswerBrief(brief):
		applyArtifactFinalAnswer(contract)
	case planThenExecuteRejectedPlanNeedsSourceRefresh(brief, input.Results):
		applyStaleAnchorRefresh(contract, input, baseTools)
	case planThenExecuteSubmitReadyParseFailureSeen(brief, input.Results):
		applyMalformedSubmitPlanRecovery(contract, input, baseTools)
	case planThenExecuteSubmitPlanOnlyReady(brief, input.Results):
		applySubmitReady(contract, input, baseTools)
	case planThenExecuteUnreadRequiredSourceNudge(brief, input.Results) != "":
		applyRequiredSource(contract, baseTools)
	case strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily &&
		(strings.TrimSpace(input.PhaseHint) == turnPhaseReviewRepairTerminal ||
			len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) > 0):
		applyReviewRepairTerminal(contract, brief, baseTools)
	case implementationScopedEditSurfaceActive(brief, input.Results):
		applyImplementationWriteReady(contract, baseTools)
	case runnerBriefRequiresMutation(brief) &&
		(hasPendingVerificationAfterWrites(input.Results, brief) ||
			sourceEditsNeedSemanticVerification(brief, input.Results)):
		applyImplementationVerifyReady(contract, baseTools)
	default:
		applyGenericTurnPhase(contract, brief)
	}
}

func applyRequiredSource(contract *TurnPhaseContract, baseTools []string) {
	contract.PhaseID = turnPhaseRequiredSource
	contract.Phase = "needs_source_body"
	contract.DerivationReason = "plan_execute_required_source_pending"
	contract.AllowedTools = filterPhaseTools(baseTools, "read_file", "code_navigate")
	contract.RequiredNext = []string{"bounded source-body read for the next unread required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"submit_plan before source-body evidence", "per-edit write in planner phase", "broad search instead of bounded required-path source read"}
	contract.TerminalOutcomes = []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionRead, actionSourceSatisfied, actionBlocker},
		InvalidActionClasses:   []string{actionSubmitPlan, actionPerEditWrite, actionSearch},
		TerminalResponseShapes: []string{"Decision: source-satisfied", "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "source_refresh")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionRead, actionBlocker}
}

func applySubmitReady(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string) {
	nativeRequired := input.SupportsNativeTools && !input.TextualToolParser
	contract.PhaseID = turnPhaseSubmitReadyTextual
	contract.Phase = "submit_ready_textual"
	contract.DerivationReason = "plan_execute_source_ready"
	contract.AllowedTools = filterPhaseTools(baseTools, "submit_plan")
	contract.RequiredNext = []string{"submit_plan with one ordered edit entry for every required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
	contract.InvalidNext = []string{"read/search/source-refresh after plan-execute submit_plan readiness", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
	contract.TerminalOutcomes = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
	contract.ActionPolicy = TurnActionClassPolicy{
		AllowedActionClasses:   []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
		InvalidActionClasses:   []string{actionRead, actionSearch, actionVerify, actionPerEditWrite, actionGenericProbe, actionProseFinal},
		TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
		CallableToolNames:      contract.AllowedTools,
	}
	contract.RuntimeCall.ParserContract = firstPhaseString(contract.RuntimeCall.ParserContract, "submit_plan_json")
	contract.RuntimeCall.ResponseFormat = firstPhaseString(contract.RuntimeCall.ResponseFormat, "json")
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker}
	if nativeRequired {
		contract.PhaseID = turnPhaseSubmitReadyNativeRequired
		contract.Phase = "submit_ready_native"
		contract.RequiredNext = []string{"native submit_plan tool call"}
		contract.InvalidNext = append(contract.InvalidNext, "non-tool prose while ToolChoice=required")
		contract.RuntimeCall.ToolTransportMode = firstPhaseString(contract.RuntimeCall.ToolTransportMode, "native")
		contract.RuntimeCall.ToolChoice = "required"
		contract.RuntimeCall.ParserContract = firstPhaseString(input.ParserContract, "native_submit_plan")
		contract.RuntimeCall.ResponseFormat = ""
		contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan}
	}
}

func applyMalformedSubmitPlanRecovery(contract *TurnPhaseContract, input TurnPhaseContractInput, baseTools []string) {
	applySubmitReady(contract, TurnPhaseContractInput{TextualToolParser: true, ParserContract: input.ParserContract}, baseTools)
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

func applyImplementationVerifyReady(contract *TurnPhaseContract, baseTools []string) {
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

func applyGenericTurnPhase(contract *TurnPhaseContract, brief workerruntime.WorkerRuntimeBrief) {
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
	contract.RuntimeCall.ExpressibleActionClasses = append([]string(nil), contract.ActionPolicy.AllowedActionClasses...)
}

func artifactFinalAnswerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer"
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

func phaseContractTextualOnly(input TurnPhaseContractInput) bool {
	return input.TextualToolParser ||
		strings.EqualFold(strings.TrimSpace(input.ToolTransportMode), "textual")
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
