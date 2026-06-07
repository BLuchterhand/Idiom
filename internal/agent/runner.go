package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

var (
	stableHandlePathLinePattern            = regexp.MustCompile(`(?m)^f\d+\s+(.+)$`)
	stableHandleInlinePattern              = regexp.MustCompile(`\bf\d+\s+((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+)\b`)
	delegatedPacketPathLinePattern         = regexp.MustCompile(`((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv)):\d+(?:-\d+)?(?:[:][^\s` + "`" + `"'(),.;:]+)?`)
	delegatedPacketPathQuotePattern        = regexp.MustCompile("((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv))[`'\"]{2,}")
	closurePathLineSymbolPattern           = regexp.MustCompile(`(?m)(?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathScopedSymbolPattern         = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathCallPattern                 = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?:[^\n]*?\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	callLikeIdentifierPattern              = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	qualifiedIdentifierPattern             = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{1,}\.[A-Za-z_][A-Za-z0-9_]{3,})\b`)
	backtickedTokenPattern                 = regexp.MustCompile("`([^`]+)`")
	toolLocalSearchHandlePattern           = regexp.MustCompile(`\bf\d+\s+(?:[[:alnum:]_.-]+/)+[[:alnum:]_.-]+\.[[:alnum:]]+:\d+:`)
	goFuncDeclPattern                      = regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	codeShapedTokenPattern                 = regexp.MustCompile(`\b[A-Za-z][a-z]+(?:[A-Z][a-z0-9]+){1,}\b`)
	codeShapedSnakeCasePattern             = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+){1,}\b`)
	shortPostReadinessSearchLiteralPattern = regexp.MustCompile(`^[A-Za-z0-9_]{2,3}$`)
	codeShapedLineRangePattern             = regexp.MustCompile(`(?i)\blines?\s+\d+(?:\s*[-–]\s*\d+)?\b`)
	readFileSummaryLineWindowPattern       = regexp.MustCompile(`\blines\s+(\d+)-(\d+)\b`)
	readFileSummaryTotalLinesPattern       = regexp.MustCompile(`\b(?:file has\s+(\d+)\s+lines\s+total|total\s+lines:\s+(\d+))\b`)
	fileOutlineDeclarationPattern          = regexp.MustCompile(`(?m)^-\s+([A-Za-z_][A-Za-z0-9_\.]*)\s+lines\s+(\d+)-(\d+)\b`)
	fileOutlinePreviewPattern              = regexp.MustCompile(`preview declarations:\s*\[([^\]]*)\]`)
)

// delegatedReadOnlyProbeBudget is the soft cap disclosed to post-readiness
// read-only continuations. It is not a hard tool counter; the runner uses the
// advisory closeout nudge, loop fuses, and MaxToolSteps for runaway protection.
const delegatedReadOnlyProbeBudget = 2

const scopedImplementationLargeReadLineThreshold = 800
const scopedImplementationLargeReadByteThreshold = 96 * 1024
const scopedImplementationBoundedReadWindowLines = 180
const scopedImplementationBoundedReadLineCharCap = 400

const forceAnswerTailGatheredEvidence = "Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer. Do not emit XML tool-call tags, tool JSON, or tool-request syntax — write only the plain user-visible final answer."

type terminalAnswerOutcome string

const (
	terminalAnswerAccepted         terminalAnswerOutcome = "accepted"
	terminalAnswerBlocked          terminalAnswerOutcome = "blocked"
	terminalAnswerTransportBlocked terminalAnswerOutcome = "transport_protocol_blocked"
)

type terminalAnswerDecision struct {
	Outcome         terminalAnswerOutcome
	RawAnswer       string
	PresentedAnswer string
	StopReason      string
	Acceptance      finalAnswerAcceptance
	BlockerResult   workspace.ToolResult
	OverrideSource  string
}

type Runner struct {
	Client llama.LLMClient
	// ImplementerClient, when non-nil, is used for write-enabled child
	// tasks (isolated workbranch children). Read-only children and the
	// parent loop stay on Client. When nil, all child work uses Client.
	ImplementerClient     llama.LLMClient
	Workspace             *workspace.Manager
	MaxToolSteps          int
	Policy                RunPolicy
	AgentName             string
	InitialNeedsNarrowing bool
	InitialBroadInspects  int
	SeedResults           []workspace.ToolResult
	PreExecute            func(step int, req workspace.ToolRequest) *workspace.ToolResult
	CompletionGate        CompletionGate
	TaskLedger            TaskLedgerPolicy
	PermissionPolicy      ToolPermissionPolicy
	CheckpointPolicy      CheckpointPolicy
	RuntimeBrief          workerruntime.WorkerRuntimeBrief
	OnEvent               func(RunEvent)
	// ContextSize is the configured llama.cpp context window in tokens.
	// Surfaced in model_call telemetry so log analysis can compute
	// ctx_pct (prompt_tokens / ctx_size) without having to reconcile
	// the running config separately. Zero means unknown.
	ContextSize     int
	DisableTaskTool bool
	// LanePolicy is the lane-specific behavioral policy derived from
	// RuntimeBrief at runner construction time. The policy replaces
	// inline TaskFamily/Contract string-comparison guards with a
	// single dispatch point. When nil, falls back to DefaultLanePolicy.
	LanePolicy LanePolicy

	// InstructionAttacher injects configured per-directory guidance
	// whenever the model touches a file under that directory.
	InstructionAttacher *InstructionAttacher
	// DoomLoopGate, if non-nil, is called when the model emits three identical
	// consecutive tool calls. Returning true approves the call (resets the
	// streak and lets execution continue); returning false rejects it and
	// terminates the turn. When nil the runner falls back to a hard rejection.
	DoomLoopGate         func(req workspace.ToolRequest, count int) bool
	LastAssistantMessage llama.ChatMessage
	currentAllowedTools  []string
	activeAutonomy       *runnerAutonomyRecorder
}

func (r *Runner) supportsNativeTools() bool {
	return r == nil || r.Client == nil || r.Client.SupportsNativeTools()
}

// lanePolicy returns the lane-specific behavioral policy, falling back
// to LanePolicyForBrief when the Runner.LanePolicy field is nil.
func (r *Runner) lanePolicy() LanePolicy {
	if r != nil && r.LanePolicy != nil {
		return r.LanePolicy
	}
	if r != nil {
		return LanePolicyForBrief(r.RuntimeBrief)
	}
	return DefaultLanePolicy{}
}
func (r *Runner) supportsTerminalSubmitPlanNativeTools(results []workspace.ToolResult) bool {
	if r == nil || r.Client == nil {
		return true
	}
	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if r.planThenExecutePlannerPhase() && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return false
	}
	return r.Client.SupportsNativeTools()
}

func (r *Runner) Run(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	onStatus func(string),
	onToolRequest func(ToolRequestEvent),
	onToolResult func(ToolResultEvent),
) (answer string, err error) {
	turnMessages := append([]llama.ChatMessage(nil), messages...)

	// Ensure LanePolicy is initialized — falls back to auto-detection from RuntimeBrief.
	if r.LanePolicy == nil {
		r.LanePolicy = LanePolicyForBrief(r.RuntimeBrief)
	}
	lp := r.LanePolicy

	executionState := newRunnerExecutionState(r.InitialNeedsNarrowing, r.InitialBroadInspects)
	delegatedReadOnlyAnswerReadyNudged := false
	planThenExecuteReadinessNudged := false
	conversationEvidenceNudged := false
	convFirstMilestone, convNudgeInterval := lp.CallCountMilestones(r.ContextSize)

	planThenExecuteCorrectionAfterRefreshNudged := false
	planThenExecuteUnreadSourceNudgedFor := ""
	delegatedReadOnlyAnswerReady := slices.Contains(r.RuntimeBrief.Constraints, "answer_readiness_reached")
	delegatedReadOnlyPostReadyFollowups := 0
	delegatedReadOnlyDiminishingNudgeStage := 0
	postChildPacketActive := false
	postChildFollowupReads := 0
	postChildDiminishingNudgeStage := 0
	postReadinessProbeBudget := postReadinessProbeBudgetForBrief(r.RuntimeBrief)
	implementationWriteOnlyRecoveryActive := false
	requiredPathCompletionRecoveryActive := false
	writeDescriptionRepairActive := false
	writeDescriptionDetourRejections := 0
	writeDescriptionRepairPath := ""
	implementationWriteOnlyHiddenTools := make(map[string]bool)
	scopedHiddenNonWriteRecoveryUsed := false
	scopedLastChanceWriteOnlyActive := false
	scratchpadCloseoutNonWriteRejections := 0
	// recoveryReadAllowanceUsed tracks paths that have already consumed
	// their one bounded read during the current write-only recovery. The
	// allowance lets an agent that exhausted its pre-write budget still
	// confirm one anchor on each remaining required edit path before
	// giving up. A path can be granted at most one read; subsequent reads
	// fall back to the standard "answer with <<QUESTION>>" blocker.
	recoveryReadAllowanceUsed := map[string]bool{}
	planThenExecuteReadySourceRefreshes := 0
	planThenExecuteNativeSubmitPlanNoCallFallbacks := 0
	planThenExecuteNativeSubmitPlanDisabled := false
	// postCompletionVerificationCalls counts non-write tool requests issued
	// AFTER all required edit paths have been written. Models commonly use
	// this phase to read the files they just wrote and confirm the edits
	// landed — useful in moderation, but past a small budget it just burns
	// tool steps and surfaces as a budget-exhausted "failure" even though
	// the implementation contract is already fulfilled. Once the counter
	// crosses postCompletionVerificationCutoff, the runner force-answers
	// with a CLEAN success completion.
	postCompletionVerificationCalls := 0
	// turnHiddenTools tracks tools that have been gated enough times this
	// turn that we should stop advertising them in the manifest. Removes
	// a class of waste outright: if list_files is chained past the gate,
	// hiding it for the rest of the turn prevents another round of token
	// spend on calls we're going to reject anyway.
	turnHiddenTools := make(map[string]bool)
	exactAnchorHiddenTools := make(map[string]bool)
	implementationScopedHiddenTools := make(map[string]bool)
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	exactAnchorWriteOnlyActive := exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, collectedResults)
	exactAnchorWriteFailures := 0
	currentAllowedTools := []string(nil)
	defer func() {
		r.currentAllowedTools = nil
		r.activeAutonomy = nil
	}()
	setCurrentAllowedTools := func(tools []string) {
		r.currentAllowedTools = append(r.currentAllowedTools[:0], tools...)
	}
	if r.Workspace != nil {
		priorPlanThenExecuteMode := r.Workspace.PlanThenExecutePlannerMode()
		switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
		case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
			r.Workspace.SetPlanThenExecutePlannerMode(true)
		default:
			r.Workspace.SetPlanThenExecutePlannerMode(false)
		}
		restoreWorkspacePlanThenExecuteMode := func() {
			r.Workspace.SetPlanThenExecutePlannerMode(priorPlanThenExecuteMode)
		}
		defer restoreWorkspacePlanThenExecuteMode()
	}
	syncCurrentAllowedToolsFromOptions := func() {
		currentAllowedTools = currentAllowedTools[:0]
		for _, tool := range opts.Tools {
			name := strings.TrimSpace(tool.Function.Name)
			if name != "" {
				currentAllowedTools = append(currentAllowedTools, name)
			}
		}
		setCurrentAllowedTools(currentAllowedTools)
	}
	rebuildTools := func() {
		if r.Workspace == nil {
			return
		}
		allowed := r.advertisedAllowedTools(r.Workspace.AllowedTools())
		writeOnlySurface := implementationWriteOnlyRecoveryActive ||
			requiredPathCompletionRecoveryActive ||
			implementationPreWriteToolSurfaceActive(r.RuntimeBrief, collectedResults)
		scopedEditSurface := !writeOnlySurface && implementationScopedEditSurfaceActive(r.RuntimeBrief, collectedResults)
		if scopedEditSurface {
			hideImplementationScopedToolsForTurn(turnHiddenTools, implementationScopedHiddenTools, r.Workspace)
		} else {
			releaseHiddenTools(turnHiddenTools, implementationScopedHiddenTools)
		}
		if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) && !executionState.verificationAttempted {
			filtered := make([]string, 0, 1)
			for _, t := range allowed {
				if t == "shell" {
					filtered = append(filtered, t)
				}
			}
			allowed = filtered
		}
		// Broad discovery/planning/implementation lanes inherit
		// project-defined external tools because those are host-owned
		// context affordances. Bounded review and verification contracts
		// keep the exact runtime allow-list; otherwise opaque externals
		// can widen the phase after the contract layer already narrowed it.
		if runnerBriefAllowsExternalToolAppend(r.RuntimeBrief) && !writeOnlySurface && !scopedEditSurface {
			if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
				seen := make(map[string]struct{}, len(allowed))
				for _, name := range allowed {
					seen[name] = struct{}{}
				}
				for _, name := range r.Workspace.AvailableExternalToolNames() {
					if _, ok := seen[name]; !ok {
						allowed = append(allowed, name)
						seen[name] = struct{}{}
					}
				}
			}
		}
		if writeOnlySurface {
			allowed = implementationPreWriteAllowedTools(allowed)
		} else if scopedEditSurface {
			contract := deriveTurnPhaseContract(TurnPhaseContractInput{
				Brief:            r.RuntimeBrief,
				Results:          collectedResults,
				BaseAllowedTools: allowed,
			})
			allowed = append([]string(nil), contract.AllowedTools...)
			// applyImplementationWriteReady uses a nil external registry.
			// External read-only discovery tools bypass that narrow.
			if extReg := r.Workspace.ExternalToolsRegistry(); extReg != nil {
				for _, name := range r.Workspace.AvailableExternalToolNames() {
					if extReg.IsReadOnlyDiscovery(name) && !containsRunnerTool(allowed, name) {
						allowed = append(allowed, name)
					}
				}
			}
		} else if contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief:            r.RuntimeBrief,
			Results:          collectedResults,
			BaseAllowedTools: allowed,
		}); contract.PhaseID == turnPhaseImplementationTargetSource ||
			contract.PhaseID == turnPhaseImplementationVerifyReady ||
			contract.PhaseID == turnPhaseImplementationWriteReady {
			allowed = append([]string(nil), contract.AllowedTools...)
		}
		if len(turnHiddenTools) > 0 {
			filtered := make([]string, 0, len(allowed))
			for _, t := range allowed {
				if !turnHiddenTools[t] {
					filtered = append(filtered, t)
				}
			}
			allowed = filtered
		}
		var tools []llama.Tool
		if len(allowed) > 0 || !workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
			tools = buildLlamaTools(r.Workspace.BuildToolDefinitions(allowed))
			// Conversation and read-only lanes may expose a harness-owned note
			// tool. It writes only scratch metadata under .idiom/notes and is
			// synchronized from the final schema into currentAllowedTools below.
			if !workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) &&
				r.lanePolicy().ShouldHaveNoteTool(r.RuntimeBrief.Contract) {
				tools = append(tools, noteToolDefinition())
				if !slices.Contains(r.RuntimeBrief.AllowedTools, noteToolName) {
					r.RuntimeBrief.AllowedTools = append(r.RuntimeBrief.AllowedTools, noteToolName)
				}
			}
		}
		// One selector owns post-readiness tool contraction so schema
		// narrowing and runtime recovery cannot drift apart.
		if contract := postReadinessToolContractForBrief(r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive); contract.AnswerOnly {
			tools = nil
		} else if contract.RequireSourceRefreshContract {
			tools = stripPostReadinessBroadTools(tools)
			tools = applyPostReadinessSchemaRequirements(tools)
		}
		opts.Tools = tools
		syncCurrentAllowedToolsFromOptions()
		if len(opts.Tools) == 0 {
			opts.ToolChoice = ""
		} else if strings.TrimSpace(opts.ToolChoice) == "" {
			opts.ToolChoice = "auto"
		}
	}
	terminalSubmitPlanNativeTools := func() bool {
		return !planThenExecuteNativeSubmitPlanDisabled && r.supportsTerminalSubmitPlanNativeTools(collectedResults)
	}
	syncPlanThenExecuteRecoveryAllowedTools := func() {
		syncCurrentAllowedToolsFromOptions()
		if !opts.ForceNativeTools &&
			r.planThenExecutePlannerPhase() &&
			len(opts.Tools) == 0 {
			state := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			if state.Mode == PlanExecutePlanningModeAnchorRefresh {
				currentAllowedTools = append(currentAllowedTools[:0], planThenExecuteAnchorRefreshToolNames()...)
			} else if state.Mode == PlanExecutePlanningModeHistoryPending {
				currentAllowedTools = append(currentAllowedTools[:0], planExecuteHistoryPendingToolNames()...)
			} else if state.Mode == PlanExecutePlanningModeSubmitReady ||
				state.Mode == PlanExecutePlanningModeSubmitCorrection ||
				planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, collectedResults) {
				currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
			}
			setCurrentAllowedTools(currentAllowedTools)
		}
	}
	refreshPlanThenExecuteTerminalSurface := func(reason string) {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode == PlanExecutePlanningModeAnchorRefresh {
			r.refreshPlanThenExecuteAnchorRefreshSurface(&turnMessages, collectedResults, reason)
			return
		}
		if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, collectedResults) {
			r.prunePlanThenExecuteTerminalSurfaceHistory(&turnMessages, reason)
			return
		}
		if planThenExecuteNativeSubmitPlanDisabled {
			r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&turnMessages, collectedResults, reason)
			return
		}
		r.refreshPlanThenExecuteTerminalToolSurface(&turnMessages, collectedResults, reason)
	}
	forcePlanThenExecuteTerminalSurface := func(reason string) {
		if planThenExecuteNativeSubmitPlanDisabled {
			r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&turnMessages, collectedResults, reason)
			return
		}
		r.refreshPlanThenExecuteTerminalToolSurface(&turnMessages, collectedResults, reason)
	}
	if r.Workspace != nil {
		if exactAnchorWriteOnlyActive {
			hideExactAnchorReadTools(turnHiddenTools, exactAnchorHiddenTools, r.Workspace)
		}
		rebuildTools()
	}
	if strings.TrimSpace(opts.ToolChoice) == "" && len(opts.Tools) > 0 {
		opts.ToolChoice = "auto"
	}
	policy := r.effectivePolicy()
	metrics := runnerMetrics{startedAt: time.Now()}
	stopReason := "completed"
	budgetExhausted := false
	planThenExecuteLoopBudgetRecoveryUsed := false
	var autonomyRecorder *runnerAutonomyRecorder
	acceptedAnswerSignalsEmitted := false
	completionStatusEmitted := false
	completionStatusLastStatus := ""
	completionStatusLastBlockerKind := ""
	verificationTelemetryEmitted := false
	completionGateRejections := 0
	emitCompletionStatus := func(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
		completionStatusEmitted = true
		completionStatusLastStatus = strings.TrimSpace(status)
		completionStatusLastBlockerKind = strings.TrimSpace(blockerKind)
		r.emitCompletionStatus(status, reason, blockerKind, results, userVisibleBlocker)
	}
	defer func() {
		if autonomyRecorder != nil {
			autonomyRecorder.emitLoopCloseout(stopReason)
			autonomyRecorder.emitBudgetGate(policy, metrics, stopReason, budgetExhausted)
		}
		r.emitBudgetLedger(policy, metrics, stopReason, budgetExhausted)
	}()
	emitTerminalAnswerSignalsOnce := func(decision terminalAnswerDecision) {
		if acceptedAnswerSignalsEmitted || strings.TrimSpace(decision.RawAnswer) == "" {
			return
		}
		switch decision.Outcome {
		case terminalAnswerAccepted:
			if autonomyRecorder != nil {
				reasonCode := "final_answer_accepted"
				if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedAnswerIntent(decision.RawAnswer) {
					reasonCode = "plan_source_satisfied_short_circuit"
				}
				autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyAllow, reasonCode, collectedResults, completionGateRejections)
			}
			r.emitFinalAnswerTelemetry(decision.RawAnswer, collectedResults, decision.Acceptance)
		case terminalAnswerBlocked, terminalAnswerTransportBlocked:
			blockerKind := util.FirstNonEmpty(strings.TrimSpace(decision.Acceptance.BlockerKind), strings.TrimSpace(decision.StopReason))
			if autonomyRecorder != nil {
				autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, blockerKind, collectedResults, completionGateRejections)
			}
			r.emitFinalAnswerTelemetry(decision.RawAnswer, collectedResults, decision.Acceptance)
			if terminalBlockedStatusShouldEmit(completionStatusEmitted, completionStatusLastStatus, completionStatusLastBlockerKind, blockerKind) {
				emitCompletionStatus("blocked", decision.Acceptance.Reason, blockerKind, collectedResults, true)
			}
		}
		acceptedAnswerSignalsEmitted = true
	}
	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		if strings.TrimSpace(reason) != "" {
			stopReason = reason
		}
		budgetExhausted = exhausted
		if finishErr == nil {
			decision := r.classifyTerminalAnswer(value, stopReason, collectedResults)
			presentedBeforeDecoration := decision.PresentedAnswer
			decision.PresentedAnswer = r.decorateTerminalAnswerDecision(decision, collectedResults)
			if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(presentedBeforeDecoration) {
				r.emitFinalAnswerDecorationEvent(decision.StopReason, presentedBeforeDecoration, decision.PresentedAnswer)
				if strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "" {
					r.LastAssistantMessage.Content = decision.PresentedAnswer
				}
			}
			value = decision.PresentedAnswer
			stopReason = decision.StopReason
			if decision.OverrideSource != "" {
				r.emitFinalAnswerBlockerOverride(decision.Acceptance, decision.StopReason, decision.OverrideSource)
			}
			emitTerminalAnswerSignalsOnce(decision)
		}
		return value, finishErr
	}
	var lastToolKey string
	var lastToolResult *workspace.ToolResult
	// activeToolCall points at the native tool_call we're currently
	// responding to (when the model sent one). Turn helpers consult it to
	// emit role:"tool" + tool_call_id responses instead of
	// role:"system" text. Reset to nil at the top of every iteration.
	var activeToolCall *llama.ToolCall
	replayedToolRequests := make(map[string]int)
	declarationMovesForbidden := declarationMovesForbiddenByMessages(turnMessages)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	reviewRepairTerminalRequiredRejects := 0
	rejectionRecoveryNudgeUsed := false
	consecutiveNoProgress := 0
	discoveryRedirect := NewDiscoveryRedirect()
	taskLedger := newTaskLedgerState(r.TaskLedger)
	r.RuntimeBrief = r.runtimeBriefWithLiveState(policy, turnMessages, collectedResults)
	if r.Workspace != nil {
		rebuildTools()
	}
	visibleRuntimeBrief := r.visibleBriefForToolSurface(r.RuntimeBrief, collectedResults)
	autonomyRecorder = newRunnerAutonomyRecorder(r, policy, visibleRuntimeBrief, collectedResults)
	r.activeAutonomy = autonomyRecorder
	if autonomyRecorder != nil {
		autonomyRecorder.emitContractStarted(visibleRuntimeBrief)
	}
	if initialBrief := workerruntime.RenderInitialRuntimeBrief(visibleRuntimeBrief); strings.TrimSpace(initialBrief) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: initialBrief})
	}
	if protocol := r.textualToolProtocolPrompt(visibleRuntimeBrief); strings.TrimSpace(protocol) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: protocol})
	}
	if bootstrap := r.initialBootstrapInstruction(); strings.TrimSpace(bootstrap) != "" {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: bootstrap})
	}
	r.emitControlRailsForBrief(turnMessages, visibleRuntimeBrief)
	r.emitBriefAudit(visibleRuntimeBrief, collectedResults)
	evidenceFirstTargetPhaseCleared := false
	postChildReadBudget := postChildReadBudgetForBrief(r.RuntimeBrief)
	requestBudget := newRunnerRequestBudgetState(postChildReadBudget)
	recorder := runnerTurnRecorder{
		runner:                    r,
		onToolResult:              onToolResult,
		collectedResults:          &collectedResults,
		turnMessages:              &turnMessages,
		lastToolKey:               &lastToolKey,
		lastToolResult:            &lastToolResult,
		consecutiveRejectedAction: &consecutiveRejectedActions,
		consecutiveNoProgress:     &consecutiveNoProgress,
		metrics:                   &metrics,
		declarationMovesForbidden: declarationMovesForbidden,
	}

	// currentFingerprint is the fingerprint of the in-flight tool call,
	// set at the top of each loop iteration. The turn controller reads it
	// via pointer so every rejection path can record the
	// fingerprint in rejectedFingerprints — letting a later pre-execute
	// guard short-circuit a byte-identical retry of a rejected call.
	var currentFingerprint string
	implementationTargetWriteReadySourceRefreshes := 0
	implementationTargetWriteReadyNonWriteRejections := 0
	implementationWriteOnlyNonWriteRejects := 0
	var deferredSourceEvidenceWrite *implementationDeferredSourceEvidenceWrite
	var deferredGitHistorySubmitPlan *workspace.ToolRequest
	var deferredGitHistorySubmitPlanRaw string
	controller := runnerTurnController{
		runner:                     r,
		ctx:                        ctx,
		opts:                       &opts,
		metrics:                    &metrics,
		requestBudget:              &requestBudget,
		recorder:                   &recorder,
		autonomyRecorder:           autonomyRecorder,
		turnMessages:               &turnMessages,
		collectedResults:           &collectedResults,
		rejectedActions:            rejectedActions,
		rejectionRecoveryNudgeUsed: &rejectionRecoveryNudgeUsed,
		completionGateRejections:   &completionGateRejections,
		consecutiveRejected:        &consecutiveRejectedActions,
		consecutiveNoProgress:      &consecutiveNoProgress,
		currentFingerprint:         &currentFingerprint,
	}
	retryRejectedFinalAnswer := func(stepNumber int, activeToolCall *llama.ToolCall, assistantMessage llama.ChatMessage, acceptance finalAnswerAcceptance, result workspace.ToolResult) bool {
		if !r.retryableFinalAnswerAcceptanceRejection(acceptance, collectedResults, completionGateRejections, currentAllowedTools) {
			return false
		}
		priorGateRejections := completionGateRejections
		result.Error = escalateCompletionGateMessage(result.Error, priorGateRejections)
		if autonomyRecorder != nil {
			autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), collectedResults, priorGateRejections)
		}
		controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
		completionGateRejections = priorGateRejections + 1
		if prompt, _, ok := r.completionGateRecoveryPrompt(collectedResults); ok {
			if card := r.renderRecoveryCardForRejectedAction(workspace.ToolRequest{Tool: "completion_gate"}, result, collectedResults, currentAllowedTools); card != "" {
				prompt = appendRecoveryCardToMessage(prompt, card)
			}
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
		}
		return true
	}

	if req, ok := r.verificationBootstrapRequest(); ok && r.Workspace != nil && len(collectedResults) == 0 {
		raw := marshalToolRequestRaw(req)
		metrics.ToolCalls++
		if onToolRequest != nil {
			profile := workspace.DiscoveryProfileForRequest(req)
			onToolRequest(ToolRequestEvent{
				Step:           1,
				Agent:          r.agentName(),
				Raw:            raw,
				Request:        req,
				Mode:           r.Workspace.Mode(),
				Broad:          workspace.IsBroadInspectionRequest(req),
				Narrowing:      workspace.IsNarrowingRequest(req),
				Action:         toolActionClass(req),
				DiscoveryClass: profile.Class,
				DiscoveryScope: profile.Scope,
				DiscoveryCost:  profile.Cost,
			})
		}
		result := r.Workspace.Execute(ctx, req)
		recorder.recordWithOptions(1, req, result, "verification_auto_bootstrap", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		update := executionState.applyResult(req, result, toolRequestKey(req), r.Workspace, &requestBudget)
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		if autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}
			autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}
		answer := synthesizeVerificationOnlyAnswer(req, result)
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		// Conversation lanes: the model owns the stop decision. Duration
		// and model-call budgets cut off legitimate investigation and
		// produce garbage forced answers. Per north star, trust the
		// model's intelligence — if it needs more time, let it continue.
		if !r.lanePolicy().SkipDurationBudget() {
			if policy.shouldStopForDuration(metrics.startedAt) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Run duration budget is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "duration_budget_exhausted", true)
			}
		}
		if !r.lanePolicy().SkipModelCallBudget() {
			if policy.shouldStopForModelCalls(metrics.ModelCalls) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Model-call budget is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "model_call_budget_exhausted", true)
			}
		}
		if r.scopedImplementationLastChanceWriteOnlyApplies(policy, metrics.ModelCalls, collectedResults) && !scopedLastChanceWriteOnlyActive {
			scopedLastChanceWriteOnlyActive = true
			implementationWriteOnlyRecoveryActive = true
			requiredPathCompletionRecoveryActive = true
			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()
			prompt := r.scopedImplementationLastChanceWriteOnlyPrompt(collectedResults)
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			opts = forceVisibleActionChatOptions(opts)
			opts = planExecuteSetExactMaxTokens(opts, 768)
			r.emitEvent(RunEvent{Role: "implementation_scoped_last_chance_write_only", Content: prompt})
		}
		modelCallOpts := readOnlyEvidenceAvailableTerminalChatOptions(opts, r.RuntimeBrief, collectedResults)
		if r.compactTurnMessagesIfNeeded(&turnMessages, policy, taskLedger, collectedResults) {
			metrics.Compactions++
		}
		if onStatus != nil && step > 0 {
			if policy.unbounded() {
				onStatus(fmt.Sprintf("Qwen is still working (%d steps).", stepNumber))
			} else {
				onStatus(fmt.Sprintf("Qwen is still working (%d/%d).", stepNumber, policy.MaxToolSteps))
			}
		}

		metrics.ModelCalls++
		phaseHint := ""
		if implementationWriteOnlyRecoveryActive && strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
			phaseHint = turnPhaseReviewRepairTerminal
		}
		contractID := ""
		if autonomyRecorder != nil {
			contractID = autonomyRecorder.contractIDForTurnPhase()
		}
		phaseContract := r.deriveCurrentTurnPhaseContract(runnerTurnPhaseContractInput{
			ContractID:          contractID,
			ModelCallNumber:     metrics.ModelCalls,
			Step:                stepNumber,
			PhaseHint:           phaseHint,
			Brief:               r.RuntimeBrief,
			Results:             collectedResults,
			AllowedTools:        currentAllowedTools,
			HiddenTools:         turnHiddenTools,
			Options:             modelCallOpts,
			SupportsNativeTools: terminalSubmitPlanNativeTools(),
		})
		modelProtocol, hasModelProtocol := r.compileModelProtocolProjection(r.RuntimeBrief, collectedResults, phaseContract, policy, terminalSubmitPlanNativeTools())
		if autonomyRecorder != nil {
			modelProtocol = autonomyRecorder.bindModelProtocolProjection(modelProtocol)
			autonomyRecorder.emitTurnPhaseContract(phaseContract, modelProtocol)
		}
		modelStart := time.Now()
		turnMessages = stripOrphanedToolCalls(turnMessages)
		requestMessages := turnMessages
		if hasModelProtocol {
			requestMessages = modelMessagesWithProtocolProjection(requestMessages, modelProtocol)
			if autonomyRecorder != nil {
				autonomyRecorder.markModelProtocolPresented(modelProtocol)
			}
		}
		requestMessages = chatMessagesForOptions(requestMessages, modelCallOpts)
		assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, modelCallOpts)
		if err != nil {
			if prompt, ok := r.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(err, opts, collectedResults, planThenExecuteNativeSubmitPlanNoCallFallbacks); ok {
				planThenExecuteNativeSubmitPlanNoCallFallbacks++
				planThenExecuteNativeSubmitPlanDisabled = true
				opts = planThenExecuteTextualSubmitPlanFallbackChatOptions(opts, r.RuntimeBrief, collectedResults)
				currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
				setCurrentAllowedTools(currentAllowedTools)
				r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&turnMessages, collectedResults, "native_required_no_tool_call")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})
				continue
			}
			if answer, ok := r.modelErrorAfterPartialEditBlockerAnswer(err, collectedResults); ok {
				emitCompletionStatus("blocked", "model_error_after_partial_edit", "model_error_after_partial_edit", collectedResults, true)
				return finish(answer, nil, "model_error_after_partial_edit", false)
			}
			if answer, ok := r.modelErrorAfterReadOnlyEvidenceAnswer(err, collectedResults); ok {
				r.emitEvent(RunEvent{Role: "model_error_after_grounded_evidence", Content: "model transport failed after grounded read-only evidence was collected; returning deterministic evidence fallback"})
				if closeout, closeoutOK := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); closeoutOK {
					answer = closeout
				}
				return finish(answer, nil, "model_error_after_grounded_evidence", false)
			}
			return finish("", err, "model_error", false)
		}
		metrics.addUsage(assistantMessage.Usage)
		r.emitModelCallTraceWithOptions(stepNumber, "main_loop", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), modelCallOpts, terminalSubmitPlanNativeTools(), collectedResults)
		reply := assistantMessage.Content
		// Capture the full message (including ReasoningContent) so the
		// auto-salvage path can recover review prose that landed inside
		// a runaway <think> block instead of visible content (#95).
		r.LastAssistantMessage = assistantMessage
		r.emitThinkingTrace(stepNumber, "model_reply", assistantMessage, modelCallOpts, false)

		activeToolCall = nil
		var (
			req                                  workspace.ToolRequest
			raw                                  string
			ok                                   bool
			parseErr                             error
			deterministicPlanExecuteEvidenceRead bool
			replayedOverVerificationRequest      *workspace.ToolRequest
		)
		normalization := normalizeToolRequestSyntax(assistantMessage)
		req, raw, ok, parseErr = normalization.Request, normalization.Raw, normalization.OK, normalization.ParseErr
		activeToolCall = normalization.ActiveToolCall
		if normalization.EventOutcome != "" && normalization.EventTransport != "" {
			r.emitToolRequestNormalizerEvent(normalization.EventOutcome, normalization.EventTransport, req, parseErr, normalization.ParserFailureClass)
		}
		if normalization.Repaired && normalization.EventTransport == toolRequestNormalizerTransportStringifiedSubmitRepair {
			r.emitEvent(RunEvent{Role: "submit_plan_stringified_edits_repaired", Content: "repaired submit_plan.edits from a quoted JSON string into a typed edits array before validation"})
		}
		if !ok && parseErr == nil {
			if intentReq, intentRaw, compiled := r.compilePlanIntentToolRequest(reply); compiled {
				req, raw, ok = intentReq, intentRaw, true
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "visible_prose_salvage", req, nil, "")
				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in prose; executing the request", req.Tool)})
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageToolRequestFromReasoning(assistantMessage, collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "reasoning_salvage", req, nil, "")
				r.emitEvent(RunEvent{Role: "reasoning_tool_salvage", Content: fmt.Sprintf("model emitted parser-readable %s request in hidden reasoning while visible content was empty; executing the request", req.Tool)})
			} else if answerText, salvaged := r.salvageAnswerFromReasoning(assistantMessage, collectedResults); salvaged {
				r.emitEvent(RunEvent{Role: "reasoning_answer_salvage", Content: fmt.Sprintf("model emitted answer prose in hidden reasoning while visible content was empty; extracted %d chars", len(answerText))})
				return finish(answerText, nil, "reasoning_answer_salvage", false)
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteRejectedRequiredSourceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool planner text and issued source read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicImplementationRejectedRequiredPathRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool implementation text and issued bounded read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				deterministicPlanExecuteEvidenceRead = true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_source_satisfied_evidence_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_source_satisfied_read", Content: fmt.Sprintf("completion recovery read source-satisfied evidence path %s before accepting the closeout", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_grounding_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery ignored non-tool planner text after ungrounded submit_plan and issued %s", req.Tool)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteNonToolCandidateRecovery(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_candidate_recovery", req, nil, "")
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_candidate", Content: fmt.Sprintf("completion recovery ignored non-terminal planner prose and issued %s for %s", req.Tool, planThenExecuteRequestTargetPath(req))})
			} else if strings.TrimSpace(reply) == "" {
				if recoveredReq, recoveredRaw, event, recovered := r.deterministicEmptyReplyRecovery(collectedResults); recovered {
					req, raw, ok = recoveredReq, recoveredRaw, true
					r.emitEvent(event)
				}
			}
		}
		if parseErr != nil {
			if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(reply, collectedResults); salvaged {
				req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
				activeToolCall = nil
				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool text; executing the safe request before recording tool_parse", req.Tool)})
			} else if raw != reply {
				if salvagedReq, salvagedRaw, salvaged := r.salvageVisibleToolRequestFromProse(raw, collectedResults); salvaged {
					req, raw, ok, parseErr = salvagedReq, salvagedRaw, true, nil
					activeToolCall = nil
					r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in malformed tool arguments; executing the safe request before recording tool_parse", req.Tool)})
				}
			}
		}
		if parseErr != nil {
			if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest(reply, collectedResults); recovered {
				req, raw, ok, parseErr = recoveredReq, recoveredRaw, true, nil
				activeToolCall = nil
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery ignored malformed planner tool JSON after ungrounded submit_plan and issued %s", req.Tool)})
			}
		}
		err = parseErr
		if err != nil {
			var assistantToolTurn llama.ChatMessage
			if activeToolCall != nil {
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
			} else if !r.supportsNativeTools() {
				assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, workspace.ToolRequest{Tool: "tool_parse"}, raw, opts)
				activeToolCall = nil
			} else {
				call := syntheticLegacyParseToolCall(stepNumber, raw)
				activeToolCall = &call
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
			}
			assistantToolTurn = assistantHistoryMessageForOptions(assistantToolTurn, opts)
			turnMessages = append(turnMessages, assistantToolTurn)
			r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, opts, true)
			controller.recordParseFailure(stepNumber, activeToolCall, raw, err)
			if prompt, ok := r.planThenExecuteSubmitPlanParseRecoveryPrompt(raw, err, collectedResults); ok {
				implementationWriteOnlyRecoveryActive = true
				if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
					requiredPathCompletionRecoveryActive = true
				}
				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()
				opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				refreshPlanThenExecuteTerminalSurface("parse_recovery")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_parse_recovery", Content: prompt})
			}
			continue
		}
		if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterEvidence(req, collectedResults, currentAllowedTools); replay {
			originalReq := req
			if strings.TrimSpace(originalReq.Tool) == "source_sanity" || strings.TrimSpace(originalReq.Tool) == "shell" {
				reqCopy := originalReq
				replayedOverVerificationRequest = &reqCopy
			} else if deferredSourceEvidenceWrite != nil && deferredSourceEvidenceWrite.RequiresHistory {
				if verifyReq, ok := deferredSourceEvidenceWrite.fallbackVerificationRequest(); ok {
					replayedOverVerificationRequest = &verifyReq
				}
			}
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(replayReq.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(replayRaw),
				"reason":         "implementation_write_after_source_evidence_replay",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, replayReq.Tool),
			})
			req = replayReq
			raw = replayRaw
			ok = true
			activeToolCall = nil
			deferredSourceEvidenceWrite = nil
		}
		if ok && strings.TrimSpace(req.Tool) == "submit_plan" &&
			planThenExecuteGroundingRejectedSeen(collectedResults) &&
			!planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, collectedResults) &&
			len(r.ungroundedSubmitPlanEditPaths(req, collectedResults)) > 0 {
			if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest("", collectedResults); recovered {
				req, raw = recoveredReq, recoveredRaw
				activeToolCall = nil
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery replaced repeated ungrounded submit_plan with %s", req.Tool)})
			}
		}
		// Answer tool: the model can explicitly signal it's ready to
		// answer by calling the "answer" tool. This gives tool-biased
		// models a tool-call-shaped way to produce prose, matching their
		// training bias toward tool use over spontaneous prose.
		if ok && strings.TrimSpace(req.Tool) == "answer" {
			// The answer content comes from the tool call's arguments.
			// For native tool calls, raw is the JSON arguments string
			// like {"content": "the answer text"}. For textual tool
			// calls, raw is the full tool request JSON.
			answerContent := ""
			var answerArgs struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(raw), &answerArgs); err == nil && answerArgs.Content != "" {
				answerContent = answerArgs.Content
			}
			if answerContent != "" {
				if acceptance := r.evaluateFinalAnswerAcceptance(answerContent, collectedResults); acceptance.BlockerKind != "" {
					blockerKind := acceptance.BlockerKind
					reason := acceptance.Reason
					blockerResult := workspace.ToolResult{
						Tool:    "completion_gate",
						Summary: "Answer tool rejected by blocker check",
						Error:   r.finalAnswerBlockerErrorText(blockerKind, reason, answerContent, collectedResults),
					}
					if retryRejectedFinalAnswer(stepNumber, activeToolCall, assistantMessage, acceptance, blockerResult) {
						continue
					}
					return finish(answerContent, nil, blockerKind, true)
				}
				return finish(answerContent, nil, "model_answer_tool", false)
			}
		}
		if !ok {
			if len(changedPathsFromToolResults(collectedResults)) == 0 {
				if req, result, salvaged := r.autoSalvageMarkdownDeliverable(reply); salvaged {
					metrics.ToolCalls++
					if onToolRequest != nil {
						profile := workspace.DiscoveryProfileForRequest(req)
						onToolRequest(ToolRequestEvent{
							Step:           stepNumber,
							Agent:          r.agentName(),
							Raw:            marshalToolRequestRaw(req),
							Request:        req,
							Mode:           r.Workspace.Mode(),
							Broad:          workspace.IsBroadInspectionRequest(req),
							Narrowing:      workspace.IsNarrowingRequest(req),
							Action:         toolActionClass(req),
							DiscoveryClass: profile.Class,
							DiscoveryScope: profile.Scope,
							DiscoveryCost:  profile.Cost,
						})
					}
					recorder.activeToolCall = activeToolCall
					recorder.recordWithOptions(stepNumber, req, result, "auto_salvage_markdown_deliverable", runnerRecordOptions{
						TrackProgress:  true,
						ResetRejected:  true,
						SetLastToolKey: true,
					})
					r.emitEvent(RunEvent{
						Role:    "auto_salvage_markdown_deliverable",
						Content: fmt.Sprintf("model emitted a single-file markdown deliverable as final-answer text; recorded it through write_file for %s", strings.TrimSpace(req.Path)),
					})
					emitCompletionStatus("completed", "auto_salvage_markdown_deliverable", "", collectedResults, false)
					return finish(fmt.Sprintf("Done. Wrote %s via auto-salvage after the model emitted the deliverable as final-answer text.", strings.TrimSpace(req.Path)), nil, "auto_salvage_markdown_deliverable", false)
				}
			}
			result, rejected := workspace.ToolResult{}, false
			if requiredPathCompletionRecoveryActive {
				result, rejected = r.requiredPathCompletionRecoveryRejection(reply, collectedResults)
			}
			gateReply := reply
			if !rejected {
				gateReply = r.decoratedReadOnlyReplyForCompletionGate(reply, collectedResults)
				result, rejected = r.completionGateRejection(gateReply, collectedResults, stepNumber, taskLedger, declarationMovesForbidden)
			}
			if rejected {
				priorGateRejections := completionGateRejections
				// Escalate the gate's directive with each retry so the
				// model gets progressively-clearer guidance instead of
				// the same message N times. Generic — independent of
				// task family or project.
				result.Error = escalateCompletionGateMessage(result.Error, priorGateRejections)
				if autonomyRecorder != nil {
					autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), collectedResults, priorGateRejections)
				}
				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
				// When the gate rejects XML tool-call markup, clear
				// the persisted assistant message so the raw XML does
				// not appear in the conversation log as a user-visible
				// artifact.
				if looksLikeTextualToolCall(reply) {
					r.LastAssistantMessage.Content = "[XML tool-call markup detected and sanitized — the model emitted its native XML format instead of a proper tool call or prose. The harness recovery path will retry with a corrected prompt.]"
				}
				completionGateRejections = priorGateRejections + 1
				shouldAttachRecoveryPrompt := priorGateRejections == 0 ||
					result.Summary == "Completion gate rejected unresolved process narration" ||
					(r.planThenExecutePlannerPhase() && result.Summary == "Plan-execute completion gate rejected final answer" && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.planThenExecutePlannerPhase() && result.Summary == "Completion gate rejected non-specific submit_plan blocker" && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.textualRequiredPathCompletionRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.unscopedNoWriteCompletionRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults)) ||
					(r.completionGateMutationRecoveryApplies(collectedResults) && priorGateRejections < r.completionGateRetryLimit(collectedResults))
				if prompt, hideReads, ok := r.completionGateRecoveryPrompt(collectedResults); ok && shouldAttachRecoveryPrompt {
					opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
					hiddenOnlyNoWriteTerminalRecovery := mutationHiddenOnlyNoWriteTerminalRecoveryStall(r.RuntimeBrief, collectedResults, assistantMessage, priorGateRejections, terminalSubmitPlanNativeTools())
					if hiddenOnlyNoWriteTerminalRecovery {
						opts = forceVisibleActionChatOptions(opts)
						prompt = hiddenOnlyNoWriteTerminalRecoveryPrompt()
						hideReads = true
						r.emitEvent(RunEvent{Role: "mutation_visible_write_mode", Content: "textual mutation recovery produced hidden-only output after concrete source evidence; retrying once with hidden thinking disabled for the terminal write/blocker action"})
					} else if implementationRequiredPathHiddenOnlyRecoveryStall(r.RuntimeBrief, collectedResults, assistantMessage, priorGateRejections, terminalSubmitPlanNativeTools()) {
						opts = forceVisibleActionChatOptions(opts)
						if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, collectedResults) {
							prompt = planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(prompt)
							r.emitEvent(RunEvent{Role: "plan_then_execute_submit_correction_visible_mode", Content: "submit-correction recovery produced hidden-only output; retrying once with hidden thinking disabled for the terminal submit_plan action"})
						} else {
							prompt = "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce the required visible tool JSON or a visible `<<QUESTION>>:` blocker now.\n\n" + prompt
							r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "anchor-ready textual recovery produced hidden-only output; retrying once with hidden thinking disabled for the terminal write/blocker action"})
						}
					}
					if hideReads {
						implementationWriteOnlyRecoveryActive = true
						if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
							requiredPathCompletionRecoveryActive = true
						}
						markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
						if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
							hideReviewRepairTerminalOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
						} else {
							hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
						}
						rebuildTools()
						opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
						opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
						syncPlanThenExecuteRecoveryAllowedTools()
					}
					refreshPlanThenExecuteTerminalSurface("completion_gate_recovery")
					if card := r.renderRecoveryCardForRejectedAction(workspace.ToolRequest{Tool: "completion_gate"}, result, collectedResults, currentAllowedTools); card != "" {
						prompt = appendRecoveryCardToMessage(prompt, card)
					}
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
				}
				if priorGateRejections >= r.completionGateRetryLimit(collectedResults) {
					if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
						return finish(answer, nil, "host_read_only_closeout", true)
					}
					answer := r.completionGateRepeatedBlockerAnswer(result, collectedResults)
					blockerKind := r.completionGateRepeatedBlockerKind(collectedResults)
					emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
					return finish(answer, nil, blockerKind, true)
				}
				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
			answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, modelCallOpts, collectedResults, &metrics)
			if strings.TrimSpace(gateReply) != "" &&
				strings.TrimSpace(gateReply) != strings.TrimSpace(reply) &&
				strings.TrimSpace(answer) == strings.TrimSpace(reply) &&
				!strings.HasPrefix(strings.TrimSpace(gateReply), "<<QUESTION>>") {
				answer = gateReply
			}
			if reason := retryAnswerHedgeRejectionReason(answer); reason != "" {
				// The retry answer is a hedge or meta-response about evidence
				// sufficiency rather than a substantive answer — reject and block.
				r.emitCompletionStatus("blocked", reason, "content_gap", collectedResults, true)
				blockerResult := workspace.ToolResult{Tool: "completion_gate", Summary: "Retry answer rejected as hedge", Error: reason}
				overridden := r.completionGateRepeatedBlockerAnswer(blockerResult, collectedResults)
				return finish(overridden, nil, "content_gap", true)
			}
			if acceptance := r.evaluateFinalAnswerAcceptance(answer, collectedResults); acceptance.BlockerKind != "" {
				blockerKind := acceptance.BlockerKind
				reason := acceptance.Reason
				// Override only fires when the model fabricates
				// completion. A <<QUESTION>>: per-path blocker IS the
				// proper way to report unfulfilled paths, so we keep
				// the model's text in that case — only swap when the
				// answer claims success against the harness's actual
				// changed_paths state.
				if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
					blockerResult := workspace.ToolResult{
						Tool:    "completion_gate",
						Summary: "Final answer rejected by blocker check",
						Error:   r.finalAnswerBlockerErrorText(blockerKind, reason, answer, collectedResults),
					}
					if retryRejectedFinalAnswer(stepNumber, activeToolCall, assistantMessage, acceptance, blockerResult) {
						continue
					}
					return finish(answer, err, blockerKind, true)
				}
			}
			return finish(answer, err, "model_final", false)
		}
		var assistantToolTurn llama.ChatMessage
		if activeToolCall != nil {
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
		} else if !r.supportsNativeTools() {
			assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, req, raw, modelCallOpts)
			activeToolCall = nil
		} else {
			call := syntheticLegacyToolCall(stepNumber, req, raw)
			activeToolCall = &call
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
		}
		assistantToolTurn = assistantHistoryMessageForOptions(assistantToolTurn, modelCallOpts)
		turnMessages = append(turnMessages, assistantToolTurn)
		r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, modelCallOpts, true)
		if r.shouldForceAnswerForPostCompletionVerification(req, collectedResults, &postCompletionVerificationCalls) {
			emitCompletionStatus("completed", "post_completion_verification_cutoff", "", collectedResults, false)
			r.emitEvent(RunEvent{
				Role:    "post_completion_verification_cutoff",
				Content: fmt.Sprintf("all required edit paths written; cutting off self-verification after %d non-write call(s) to avoid false budget-exhausted failure signal", postCompletionVerificationCalls),
			})
			answer, err := controller.completeWithoutMoreToolsWithReason("All required edit paths have been written and the implementation contract is fulfilled. Tools are now disabled. Write the standard final answer now: report the changed paths and verification status. Do not call further tools.")
			return finish(answer, err, "post_completion_verification_cutoff", false)
		}
		preflight := r.applyLanePreflight(runnerLanePreflightInput{
			Request:               req,
			Raw:                   raw,
			Results:               collectedResults,
			VerificationAttempted: executionState.verificationAttempted,
		})
		req = preflight.Request
		raw = preflight.Raw
		if preflight.OriginalRaw != "" && strings.TrimSpace(preflight.OriginalRaw) != strings.TrimSpace(preflight.Raw) {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(preflight.OriginalRequest.Tool),
				"rewritten_tool": strings.TrimSpace(preflight.Request.Tool),
				"original_raw":   strings.TrimSpace(preflight.OriginalRaw),
				"rewritten_raw":  strings.TrimSpace(preflight.Raw),
			}))
			// Make the rewrite visible to the model. A silent substitution
			// is firmer than a "you must" rejection — it removes the
			// model's signal entirely. A visible note keeps the contract
			// (the rewritten call still runs) while letting the model
			// learn the routing rule for next time.
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(preflight.OriginalRequest.Tool, preflight.Request.Tool),
			})
		}
		if redirected, redirectedRaw, ok := repeatedDiagnosticLogReadFileBackfill(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "repeated_diagnostic_log_read_previous_window",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
		}
		if summary, errorText, reject := diagnosticLogUnboundedReadCoverageRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "diagnostic_log_read_coverage_rejected", true)
			}
			continue
		}
		if redirected, redirectedRaw, reason, ok := generatedEvidenceArtifactReadRedirectRequest(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("generated_evidence_artifact_read_redirect", map[string]any{
				"from_path": normalizeRunnerPath(req.Path),
				"to_path":   normalizeRunnerPath(redirected.Path),
				"reason":    reason,
			}))
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, reason, ok := structuredSourceReadRewriteRequest(req, r.Workspace, currentAllowedTools, r.RuntimeBrief); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         reason,
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, reason, ok := pathlessReadSymbolAfterOutlineRewriteRequest(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         reason,
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, reason, ok := repeatedFileOutlineToReadSymbolRewriteRequest(req, collectedResults, r.RuntimeBrief); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         reason,
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, reason, ok := readOnlyDefinitionToReadSymbolRewriteRequest(req, r.RuntimeBrief); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         reason,
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if summary, errorText, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "repeated_diagnostic_log_window_rejected", true)
			}
			continue
		}
		if redirected, redirectedRaw, ok := r.repeatedRangeLessReadFileRedirect(req, collectedResults, currentAllowedTools); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "repeated_rangeless_read_file_bounded_source",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
		}
		if repaired, repairedRaw, ok := r.writeDescriptionRepairSynthesizedRequest(req, writeDescriptionRepairActive); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(repaired.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(repairedRaw),
				"reason":         "write_description_repair_synthesized",
			}))
			r.emitWriteDescriptionValidatorDecision(stepNumber, repaired, "repair", "description_synthesized", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_synthesized", Content: fmt.Sprintf("synthesized missing write description for %s on %q", strings.TrimSpace(repaired.Tool), normalizeRunnerPath(repaired.Path))})
			req = repaired
			raw = repairedRaw
		}
		currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		if (writeDescriptionRepairActive ||
			lastResultSummaryIs(collectedResults, "Missing write description") ||
			lastResultSummaryIs(collectedResults, "Write-description repair rejected non-write detour")) &&
			!r.writeOnlyRecoveryToolIsWrite(req.Tool) {
			writeDescriptionRepairActive = true
			writeDescriptionDetourRejections++
			prompt := r.writeDescriptionRepairPrompt(req)
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Write-description repair rejected non-write detour",
				Error:   fmt.Sprintf("%s is not available while write-description repair is pending. Reissue the same in-scope write with a non-empty description, or answer with <<QUESTION>> and the exact blocker.", strings.TrimSpace(req.Tool)),
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			r.emitWriteDescriptionValidatorDecision(stepNumber, req, "reject", "non_write_detour", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_recovery", Content: prompt})
			if writeDescriptionDetourRejections >= 2 {
				blockerKind := "write_description_repair_detour_repeated"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
				answer := "<<QUESTION>>: write-description repair rejected repeated non-write detours. The required edit path remains unchanged; reissue the same in-scope write with a non-empty description, or provide the exact blocker."
				return finish(answer, nil, blockerKind, true)
			}
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			continue
		}
		if workspace.IsWorkspaceWriteTool(req.Tool) && implementationTargetSourceWriteTargetsHaveEvidence(req, collectedResults) {
			clearRejectedFingerprintSummary(requestBudget.fingerprint.rejected, currentFingerprint, "Write rejected before source-body evidence")
			delete(rejectedActions, toolActionClass(req))
		}
		if !exactAnchorWriteOnlyActive {
			decision := requestBudget.earlyRejectedFingerprintDecision(req, currentFingerprint)
			if result, prompt, ok := r.mutationRejectedRetryWriteRecovery(decision, req, collectedResults); ok {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				implementationWriteOnlyRecoveryActive = true
				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "mutation_rejected_retry_write_recovery", Content: prompt})
				continue
			}
			if res := decision.apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}
		if preflight.RejectSummary != "" {
			if preflight.ForceAnswer != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: preflight.RejectSummary,
					Error:   preflight.RejectError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				if preflight.ForceAnswer.Reason == "conversation_evidence_sufficient" {
					if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, "The retained source context identifies the relevant implementation surface."); ok {
						return finish(presentable, nil, preflight.ForceAnswer.Reason, true)
					}
				}
				answer, err := controller.completeWithoutMoreToolsWithReason(preflight.ForceAnswer.Prompt)
				return finish(answer, err, preflight.ForceAnswer.Reason, true)
			}
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, preflight.RejectSummary, preflight.RejectError)
			if done {
				return finish(answer, err, preflight.RejectStopReason, true)
			}
			continue
		}
		recoveryReadAllowanceGrantedThisRequest := false
		if summary, errorText, prompt, target, reject := r.scratchpadDeliverableCloseoutNonWriteRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			scratchpadCloseoutNonWriteRejections++
			if scratchpadCloseoutNonWriteRejections >= 2 {
				blockerKind := "scratchpad_deliverable_closeout_non_write_repeated"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
				answer := fmt.Sprintf("<<QUESTION>>: completion gate rejected a prose-only response for %s, but the model kept selecting non-write tools. The deliverable-write contract must be satisfied, recording uncertainty in the file, or the lane must be rerun with corrected evidence.", target)
				return finish(answer, nil, blockerKind, true)
			}
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_closeout_recovery", Content: prompt})
			continue
		}
		if summary, errorText, prompt, reject := r.scratchpadDeliverableSourceDiscoveryRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_source_discovery_recovery", Content: prompt})
			continue
		}
		if r.implementationTargetWriteReadySourceReadAllowed(req, collectedResults, implementationTargetWriteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			implementationTargetWriteReadySourceRefreshes++
			implementationTargetWriteReadyNonWriteRejections = 0
			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed write-ready %s source expansion on target path %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(req.Path)),
			})
		} else if _, _, prompt, reject := r.implementationTargetWriteReadyNonWriteRecovery(req, collectedResults); reject {
			implementationTargetWriteReadyNonWriteRejections++
			if implementationTargetWriteReadyNonWriteRejections >= 12 {
				blockerKind := "implementation_target_write_ready_non_write_repeated"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
				answer := fmt.Sprintf("<<QUESTION>>: after %d nudges toward writing, no workspace edit was recorded. Source evidence is available. Report the exact edit blocker or no-op evidence.", implementationTargetWriteReadyNonWriteRejections)
				return finish(answer, nil, blockerKind, true)
			}
			requestBudget.writeReadyNudgeActive = true
			nudgeMsg := targetWriteReadyStagedNudge(prompt, req, implementationTargetWriteReadyNonWriteRejections)
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_recovery", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_nudge", Content: nudgeMsg})
		}
		scopedHiddenNonWrite := implementationScopedHiddenTools[strings.TrimSpace(req.Tool)] && !workspace.IsWorkspaceWriteTool(req.Tool)
		if summary, errorText, prompt, reject := r.implementationScopedDisallowedToolRecovery(req, collectedResults); reject && !(scopedHiddenNonWrite && scopedHiddenNonWriteRecoveryUsed) {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			if scopedHiddenNonWrite {
				scopedHiddenNonWriteRecoveryUsed = true
			}
			rebuildTools()
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "implementation_scoped_tool_recovery", Content: prompt})
			continue
		}
		if !turnHiddenTools[strings.TrimSpace(req.Tool)] {
			if summary, errorText, prompt, hideNonWrite, reject := r.writeReadyPacketReadWithoutGapRecovery(req, collectedResults); reject {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: summary,
					Error:   errorText,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				if requestBudget.scopedReadRejections != nil {
					key := currentFingerprint
					if strings.TrimSpace(key) == "" {
						key = scopedReadRecoveryRejectionKey(req)
					}
					requestBudget.scopedReadRejections[key]++
					if requestBudget.scopedReadRejections[key] >= 2 {
						blockerKind := "scoped_implementation_read_repeated"
						emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
						target := "the required edit path"
						if paths := r.remainingRequiredEditPaths(collectedResults); len(paths) > 0 {
							target = strings.Join(paths, ", ")
						} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
							target = strings.Join(paths, ", ")
						}
						answer := fmt.Sprintf("<<QUESTION>>: scoped implementation recovery rejected the same %s request twice for %s. The next valid outcome is a required-path write from the packet and scoped evidence, or a blocker naming the exact missing edit-safety fact.", strings.TrimSpace(req.Tool), target)
						return finish(answer, nil, blockerKind, true)
					}
				}
				if hideNonWrite {
					implementationWriteOnlyRecoveryActive = true
					if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
						requiredPathCompletionRecoveryActive = true
					}
					markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
					hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				}
				rebuildTools()
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				role := "implementation_scoped_read_recovery"
				if hideNonWrite {
					role = "implementation_write_only_recovery"
				}
				r.emitEvent(RunEvent{Role: role, Content: prompt})
				continue
			}
		}
		if summary, errorText, prompt, reject := r.unscopedContextPackCandidateReadRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "context_pack_read_recovery", Content: prompt})
			continue
		}
		if deterministicPlanExecuteEvidenceRead {
			recoveryReadAllowanceGrantedThisRequest = true
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_source_satisfied_evidence_read_allowed",
				Content: fmt.Sprintf("allowed bounded %s for source-satisfied evidence path %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if r.planThenExecuteReadySourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for required path %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if redirected, redirectedRaw, ok := r.planThenExecuteRejectedPlanRefreshReadRedirect(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_rejected_plan_source_refresh_region",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("redirected bounded %s source refresh %d/%d for rejected plan path %q", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if r.planThenExecuteCandidateSourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for candidate path %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, normalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		}
		if implementationWriteOnlyRecoveryActive && !r.writeOnlyRecoveryToolIsWrite(req.Tool) {
			if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily && !recoveryReadAllowanceGrantedThisRequest {
				reviewRepairTerminalRequiredRejects++
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Review-repair terminal outcome required",
					Error:   reviewRepairTerminalOutcomeRequiredPrompt(r.RuntimeBrief, req.Tool),
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				refreshPlanThenExecuteTerminalSurface("review_repair_terminal_recovery")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
				opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_terminal_required", Content: result.Error})
				if reviewRepairTerminalRequiredRejects >= 2 {
					return finish("<<QUESTION>>: review-repair could not close because it repeatedly requested hidden discovery after the closeout gate instead of a terminal `Decision: pass`, corrective `submit_plan`, or blocker.", nil, "review_repair_terminal_recovery_budget_exhausted", true)
				}
				continue
			}
			if r.planThenExecutePlannerPhase() {
				if !recoveryReadAllowanceGrantedThisRequest {
					refreshNote := ""
					if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(collectedResults); ok {
						refreshNote = " " + planThenExecutePostSubmitRejectionRefreshNote(r.RuntimeBrief, collectedResults, rejected, rejectedAt)
					}
					result := workspace.ToolResult{
						Tool:    req.Tool,
						Summary: "Plan-execute submit_plan required",
						Error:   fmt.Sprintf("%s is not available while plan-execute planner recovery is waiting for a visible `submit_plan`. The next visible action must be a parser-readable `submit_plan`. %s%s", strings.TrimSpace(req.Tool), planThenExecuteSubmitPlanCoverageSentence(), refreshNote),
					}
					controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
					refreshPlanThenExecuteTerminalSurface("submit_plan_recovery")
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
					opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
					syncPlanThenExecuteRecoveryAllowedTools()
					r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: result.Error})
					continue
				}
			}
			if recoveryReadAllowanceGrantedThisRequest {
				// The plan-execute branch above grants only the explicit
				// source-refresh exception after a rejected submit_plan.
			} else if r.consumeRecoveryReadAllowance(req, recoveryReadAllowanceUsed, collectedResults) {
				recoveryReadAllowanceGrantedThisRequest = true
				r.emitEvent(RunEvent{
					Role:    "implementation_write_only_recovery_read_allowance",
					Content: fmt.Sprintf("granted one bounded %s on remaining required edit path %q", strings.TrimSpace(req.Tool), strings.TrimSpace(req.Path)),
				})
			} else {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Write-only recovery blocked non-write tool",
					Error:   fmt.Sprintf("%s is not available while write-ready recovery is active and required edit paths remain unchanged. Use an available write tool, or answer with <<QUESTION>> and the exact path-specific blocker.", strings.TrimSpace(req.Tool)),
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				implementationWriteOnlyNonWriteRejects++
				if implementationWriteOnlyNonWriteRejects < 2 {
					prompt := r.writeOnlyRecoveryNonWriteRepairPrompt(req, collectedResults)
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					opts = forceVisibleActionChatOptions(opts)
					opts = planExecuteSetExactMaxTokens(opts, 768)
					hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
					rebuildTools()
					r.emitEvent(RunEvent{Role: "implementation_write_only_recovery_non_write_repair", Content: prompt})
					continue
				}
				if answer, ok := r.writeOnlyRecoveryBlockerAnswer(collectedResults); ok {
					blockerKind := r.writeOnlyRecoveryBlockerKind(collectedResults)
					emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)
					return finish(answer, nil, blockerKind, true)
				}
				answer, err := controller.completeWithoutMoreToolsWithReason("Write-only recovery is active. Tools are now disabled. Answer with the changed paths already produced, or with <<QUESTION>> and the exact blocker that prevents the required edit.")
				return finish(answer, err, "write_only_recovery_non_write_tool", true)
			}
		}
		if turnHiddenTools[strings.TrimSpace(req.Tool)] && !recoveryReadAllowanceGrantedThisRequest {
			hiddenSummary := "Tool hidden by active contract"
			hiddenError := fmt.Sprintf("%s is hidden for the rest of this turn by the active runtime contract. Use an available tool, or answer with a concrete blocker if the contract prevents completion.", strings.TrimSpace(req.Tool))
			if strings.TrimSpace(req.Tool) == "task" && postChildPacketClosureReadyWithBrief(r.RuntimeBrief, collectedResults) {
				answer, err := controller.completeWithoutMoreToolsWithReason("The delegated read-only child packet is already available and answerable. Do not start another child task. Answer directly from the child packet and inspected evidence; qualify only material uncertainty.")
				return finish(answer, err, "post_child_answerable_task_retry", true)
			}
			if strings.TrimSpace(req.Tool) == "task" && requestBudget.readOnlyChildTaskSatisfied && !isWriteChildMode(req.Mode) {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Duplicate child task rejected",
					Error:   "the delegated read-only child result is already available, and the task tool is no longer callable this turn. Use an available bounded read/search tool for one exact remaining evidence gap, or answer from the child result and inspected evidence with material uncertainty.",
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
				r.emitEvent(RunEvent{Role: "post_child_duplicate_task_rejected", Content: result.Error})
				continue
			}
			if exactAnchorWriteOnlyActive && !workspace.IsWorkspaceWriteTool(req.Tool) && rejectionRecoveryNudgeUsed {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: hiddenSummary,
					Error:   hiddenError,
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				if answer, ok := r.exactAnchorHiddenToolBlockerAnswer(req, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_prewrite_hidden_tool_repeated", "exact_anchor_prewrite_hidden_tool_repeated", collectedResults, true)
					return finish(answer, nil, "exact_anchor_prewrite_hidden_tool_repeated", true)
				}
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				hiddenSummary,
				hiddenError,
			)
			if done {
				if answer, ok := r.exactAnchorHiddenToolBlockerAnswer(req, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_prewrite_hidden_tool_repeated", "exact_anchor_prewrite_hidden_tool_repeated", collectedResults, true)
					return finish(answer, nil, "exact_anchor_prewrite_hidden_tool_repeated", true)
				}
				return finish(answer, err, "hidden_tool_repeated", true)
			}
			continue
		}
		if summary, errorText, prompt, reject := r.implementationPreWriteNonWriteRecovery(req, collectedResults); reject {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			implementationWriteOnlyRecoveryActive = true
			if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
				requiredPathCompletionRecoveryActive = true
			}
			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "implementation_write_only_recovery", Content: prompt})
			continue
		}
		if redirected, redirectedRaw, ok := r.planThenExecuteMissingTargetReadRedirect(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_missing_target_read_ranked_candidate",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		planningTransport := r.planExecutePlanningTransportFromOptions(opts, terminalSubmitPlanNativeTools(), false)
		planningState := DerivePlanExecutePlanningActionState(r.RuntimeBrief, collectedResults, planningTransport, req)
		if !recoveryReadAllowanceGrantedThisRequest {
			if reason, prompt, ok := planThenExecuteCandidateLowValueAction(req, r.RuntimeBrief, collectedResults, planningTransport); ok {
				r.emitEvent(marshalRunEvent("plan_then_execute_low_value_action", map[string]any{
					"agent":             r.agentName(),
					"tool":              strings.TrimSpace(req.Tool),
					"path":              normalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
					"reason":            reason,
					"terminal_options":  planningState.TerminalOptions,
					"low_value_actions": planningState.LowValueActions,
				}))
				if slices.Contains(planningState.TerminalOptions, "submit_plan") {
					opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
					currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
					setCurrentAllowedTools(currentAllowedTools)
					refreshPlanThenExecuteTerminalSurface("low_value_candidate_action")
					syncPlanThenExecuteRecoveryAllowedTools()
				}
				if planThenExecuteLowValueActionShouldReject(reason) {
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						planThenExecuteLowValueActionRejectSummary(reason),
						prompt,
					)
					if done {
						return finish(answer, err, planThenExecuteLowValueActionStopReason(reason), true)
					}
					continue
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				continue
			}
		}
		if summary, errorText, prompt, reject := r.planThenExecuteSubmitPlanOnlySurfaceRejection(req, collectedResults, currentAllowedTools); reject && !recoveryReadAllowanceGrantedThisRequest {
			controller.recordRejectedResult(stepNumber, activeToolCall, req, workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}, "tool_rejected")
			refreshPlanThenExecuteTerminalSurface("submit_plan_only_rejection")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})
			continue
		}
		if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
			r.emitEvent(RunEvent{
				Role:    closeout.EventRole,
				Content: closeout.EventContent,
			})
			return finish(closeout.Answer, nil, closeout.StopReason, false)
		}
		if summary, errorText, reject := r.planThenExecuteClampedEOFReadRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_clamped_eof_read_rejected", true)
			}
			continue
		}
		surfaceAllowedTools := runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools, r.RuntimeBrief, collectedResults, req)
		historyRedirectAllowedTools := append(append([]string(nil), surfaceAllowedTools...), "history_range", "history_symbol")
		if summary, errorText, reject := currentToolSurfaceRejection(req, surfaceAllowedTools); reject && !recoveryReadAllowanceGrantedThisRequest {
			if postReadinessToolContractForBrief(r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive).AnswerOnly {
				if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
					return finish(answer, nil, "read_only_answer_only_host_closeout", false)
				}
				answer, err := controller.completeWithoutMoreToolsWithReason("The gathered read-only evidence is answerable and the current phase exposes no more tools. Answer directly from the inspected evidence; qualify only material uncertainty.")
				return finish(answer, err, "read_only_answer_only_tool_attempt", true)
			}
			redirected := false
			if planThenExecuteUnscopedEmptySubmitPlanAttempt(r.RuntimeBrief, req) {
				result := workspace.ToolResult{
					Tool:    "submit_plan",
					Code:    workspace.SubmitPlanEmptyEditsCode,
					Summary: "submit_plan rejected",
					Error:   "submit_plan requires at least one edit in the edits array",
				}
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())
				refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				rebuildTools()
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_empty_submit_plan_recovery", Content: prompt})
				continue
			} else if redirectedReq, redirectedRaw, ok := r.submitPlanBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
				submitReq := req
				deferredGitHistorySubmitPlan = &submitReq
				deferredGitHistorySubmitPlanRaw = raw
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(redirectedReq.Tool),
					"original_raw":   strings.TrimSpace(raw),
					"rewritten_raw":  strings.TrimSpace(redirectedRaw),
					"reason":         "plan_execute_submit_plan_before_history",
				}))
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: rewriteVisibilityNote(req.Tool, redirectedReq.Tool),
				})
				req = redirectedReq
				raw = redirectedRaw
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, redirectedRaw, ok := r.sourceReadBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(redirectedReq.Tool),
					"original_raw":   strings.TrimSpace(raw),
					"rewritten_raw":  strings.TrimSpace(redirectedRaw),
					"reason":         "plan_execute_source_read_before_history",
				}))
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: rewriteVisibilityNote(req.Tool, redirectedReq.Tool),
				})
				req = redirectedReq
				raw = redirectedRaw
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, ok := postWriteSourceSanityRedirect(req, r.RuntimeBrief, collectedResults, surfaceAllowedTools); ok {
				redirectedRaw := marshalToolRequestRaw(redirectedReq)
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(redirectedReq.Tool),
					"original_raw":   strings.TrimSpace(raw),
					"rewritten_raw":  strings.TrimSpace(redirectedRaw),
					"reason":         "post_write_lane_sanity_requires_source_sanity",
				}))
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: rewriteVisibilityNote(req.Tool, redirectedReq.Tool),
				})
				req = redirectedReq
				raw = redirectedRaw
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if newFileSummary, newFileError, ok := unavailableRequiredNewFileProbeNudge(req, r.RuntimeBrief, r.Workspace); ok {
				summary = newFileSummary
				errorText = newFileError
			} else if action, ok := r.planThenExecuteRequiredSourceSurfaceAction(req, collectedResults, surfaceAllowedTools); ok {
				if action.Redirect {
					r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
						"agent":          r.agentName(),
						"original_tool":  strings.TrimSpace(req.Tool),
						"rewritten_tool": strings.TrimSpace(action.Request.Tool),
						"original_raw":   strings.TrimSpace(raw),
						"rewritten_raw":  strings.TrimSpace(action.Raw),
					}))
					turnMessages = append(turnMessages, llama.ChatMessage{
						Role:    "system",
						Content: rewriteVisibilityNote(req.Tool, action.Request.Tool),
					})
					req = action.Request
					raw = action.Raw
					currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
					redirected = true
				} else {
					summary = action.Summary
					errorText = action.Error
				}
			}
			if !redirected {
				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
				if done {
					return finish(answer, err, "tool_not_allowed_by_runtime_surface", true)
				}
				continue
			}
		}
		if redirectedReq, redirectedRaw, ok := r.submitPlanBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
			submitReq := req
			deferredGitHistorySubmitPlan = &submitReq
			deferredGitHistorySubmitPlanRaw = raw
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirectedReq.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_submit_plan_before_history",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirectedReq.Tool),
			})
			req = redirectedReq
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.implementationTargetSourceWriteBeforeEvidenceRedirect(req, collectedResults, currentAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "implementation_write_before_evidence_bounded_read",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			deferredSourceEvidenceWrite = newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path)
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if summary, errorText, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, collectedResults, currentAllowedTools); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "implementation_target_source_write_rejected", true)
			}
			continue
		}
		implementationHistoryRedirectAllowedTools := append(append([]string(nil), currentAllowedTools...), "history_range", "history_symbol")
		if redirected, redirectedRaw, ok := r.writeBeforeGitHistoryEvidenceRedirect(req, collectedResults, implementationHistoryRedirectAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "write_before_git_history_bounded_history",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			deferredSourceEvidenceWrite = newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path)
			if deferredSourceEvidenceWrite != nil {
				deferredSourceEvidenceWrite.RequiresHistory = true
			}
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if summary, errorText, reject := r.writeBeforeGitHistoryEvidenceRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "write_before_git_history_rejected", true)
			}
			continue
		}
		if redirected, redirectedRaw, ok := r.planThenExecuteCandidateRangeLessReadRedirect(req, collectedResults); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_candidate_rangeless_read_bounded",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, ok := r.planThenExecuteMalformedSymbolReadRedirect(req, collectedResults, currentAllowedTools); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_malformed_symbol_read_bounded",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, ok := rewriteVerificationShellReadToSourceSanity(req, r.RuntimeBrief, collectedResults, currentAllowedTools); ok {
			redirectedRaw := marshalToolRequestRaw(redirected)
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":        "normalized",
				"transport":      "verification_shell_read_rewrite",
				"tool":           strings.TrimSpace(redirected.Tool),
				"path":           strings.TrimSpace(redirected.Path),
				"original_tool":  strings.TrimSpace(req.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "source_edit_verification_requires_source_sanity",
				"source_sanity":  true,
				"changed_source": true,
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}
		if redirected, redirectedRaw, ok := r.deterministicPlanExecuteChainedSearchRecovery(req, collectedResults, &executionState); ok {
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":       toolRequestNormalizerOutcomeNormalized,
				"transport":     "plan_execute_chained_search_recovery",
				"tool":          strings.TrimSpace(redirected.Tool),
				"path":          strings.TrimSpace(redirected.Path),
				"original_tool": strings.TrimSpace(req.Tool),
				"original_raw":  strings.TrimSpace(raw),
				"rewritten_raw": strings.TrimSpace(redirectedRaw),
				"reason":        "consume_context_pack_candidate_before_more_search",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		metrics.ToolCalls++
		// Conversation lane: periodic call-count awareness nudges.
		// Milestones derived from model context window size so
		// smaller models get earlier nudges, larger models later ones.
		// The auto-findings capture fires at convFirstMilestone;
		// awareness nudges begin one nudgeInterval later so the two
		// signals are staggered rather than competing at the same call.
		if convFirstMilestone > 0 && metrics.ToolCalls > 5 {
			shouldNudge := false
			var nudgeMsg string
			firstNudgeAt := convFirstMilestone + convNudgeInterval
			if metrics.ToolCalls == firstNudgeAt {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. If you have evidence to answer this specific question, answer now in plain prose. Do not begin new investigation on neighboring topics.", metrics.ToolCalls, strings.TrimSpace(r.RuntimeBrief.Objective))
			} else if metrics.ToolCalls >= firstNudgeAt+convNudgeInterval && (metrics.ToolCalls-firstNudgeAt)%convNudgeInterval == 0 {
				shouldNudge = true
				nudgeMsg = fmt.Sprintf("You have used %d tool calls. The user asked: %q. Answer this question directly from the evidence gathered, or if more evidence is genuinely needed, take one narrow next step. Do not explore related code or begin new investigations.", metrics.ToolCalls, strings.TrimSpace(r.RuntimeBrief.Objective))
			}
			if shouldNudge {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
				r.emitEvent(RunEvent{Role: "call_count_awareness", Content: nudgeMsg})
			}
		}
		actionClass := toolActionClass(req)
		discoveryProfile := workspace.DiscoveryProfileForRequest(req)
		if !discoveryProfile.Discovery {
			discoveryProfile = workspace.ToolDiscoveryProfile{}
		}
		if onToolRequest != nil {
			onToolRequest(ToolRequestEvent{
				Step:           stepNumber,
				Agent:          r.agentName(),
				Raw:            raw,
				Request:        req,
				Mode:           r.Workspace.Mode(),
				Broad:          workspace.IsBroadInspectionRequest(req),
				Narrowing:      workspace.IsNarrowingRequest(req),
				Action:         actionClass,
				DiscoveryClass: discoveryProfile.Class,
				DiscoveryScope: discoveryProfile.Scope,
				DiscoveryCost:  discoveryProfile.Cost,
			})
		}

		if summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_required_path_discovery_rejected", true)
			}
			continue
		}
		if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "plan_then_execute_candidate_post_readiness_discovery_rejected", true)
			}
			if summary == "Anchor refresh capability required" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
				r.emitEvent(RunEvent{Role: "plan_then_execute_anchor_refresh_required", Content: errorText})
				continue
			}
			prompt := r.planThenExecuteSubmitReadyTerminalPrompt(errorText, collectedResults, chatOptionsExposeTool(opts, "submit_plan"))
			opts = forceVisibleActionChatOptions(opts)
			if chatOptionsExposeTool(opts, "submit_plan") {
				opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
				opts.ToolChoice = "auto"
			}
			syncCurrentAllowedToolsFromOptions()
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_ready_terminal_required", Content: prompt})
			continue
		}
		if summary, errorText, reject := r.repeatedRangeLessReadFileRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "repeated_rangeless_read_file_rejected", true)
			}
			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, collectedResults, currentAllowedTools); ok {
			r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
				"agent":          r.agentName(),
				"original_tool":  strings.TrimSpace(req.Tool),
				"rewritten_tool": strings.TrimSpace(redirected.Tool),
				"original_raw":   strings.TrimSpace(raw),
				"rewritten_raw":  strings.TrimSpace(redirectedRaw),
				"reason":         "plan_execute_symbol_read_after_miss_bounded",
			}))
			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		toolKey := toolRequestKey(req)
		if prompt, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastToolResult, collectedResults, terminalSubmitPlanNativeTools()); ok && toolKey != "" && toolKey == lastToolKey && replayedToolRequests[toolKey] < 2 {
			replayedToolRequests[toolKey]++
			refreshPlanThenExecuteTerminalSurface("submit_plan_replayed_invalid")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_repeated_submit_plan_correction", Content: prompt})
			continue
		}
		if shouldRejectExactReplayBeforeLoopGate(req) && toolKey != "" && toolKey == lastToolKey && replayedToolRequests[toolKey] > 0 {
			summary := "Repeated tool request rejected"
			errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."
			if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, lastToolResult, collectedResults); ok {
				summary = replaySummary
				errorText = replayError
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				summary,
				errorText,
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}
			continue
		}
		// DOOM_LOOP guard — if the model emits exactly the same tool + args
		// three turns in a row, stop the auto-execute path and surface a
		// concrete blocker to the user. OpenCode uses threshold=3 at
		// session/processor.ts; we match that. Counting includes accepted
		// calls (not just replays/rejections) because the pathology is the
		// model getting stuck even when the harness doesn't push back.
		identicalCallRun := requestBudget.noteFingerprint(currentFingerprint)
		if r.lanePolicy().SkipFingerprintHardBlock() && isReadClassTool(req) {
			if decision := requestBudget.samePathFixationDecision(req, genericReadOnlyEvidenceReady(r.RuntimeBrief, collectedResults)); decision.Handled {
				if tool := strings.TrimSpace(decision.HideTool); tool != "" && !turnHiddenTools[tool] {
					turnHiddenTools[tool] = true
					rebuildTools()
					r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after same-path fixation", tool)})
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: util.FirstNonEmpty(decision.EventContent, decision.Nudge)})
				continue
			}
		}
		if identicalCallRun >= 3 {
			if result, prompt, ok := r.mutationNoWriteLoopRecovery(req, collectedResults, identicalCallRun); ok {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
				implementationWriteOnlyRecoveryActive = true
				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
				rebuildTools()
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "mutation_no_write_loop_recovery", Content: prompt})
				continue
			}
			if !planThenExecuteLoopBudgetRecoveryUsed {
				if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row", req.Tool, identicalCallRun), chatOptionsExposeTool(opts, "submit_plan")); ok {
					planThenExecuteLoopBudgetRecoveryUsed = true
					opts = forceVisibleActionChatOptions(opts)
					if recovery.TerminalOnly && chatOptionsExposeTool(opts, "submit_plan") {
						opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
						opts.ToolChoice = "auto"
					}
					syncCurrentAllowedToolsFromOptions()
					if recovery.TerminalOnly {
						forcePlanThenExecuteTerminalSurface("loop_budget_recovery")
						currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
						setCurrentAllowedTools(currentAllowedTools)
					}
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
					r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})
					continue
				}
			} else if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments after a loop-budget recovery", req.Tool), chatOptionsExposeTool(opts, "submit_plan")); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, "submit_plan") {
					opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
					opts.ToolChoice = "auto"
				}
				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")
				currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
				setCurrentAllowedTools(currentAllowedTools)
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})
				continue
			}
			if autonomyRecorder != nil {
				autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", currentFingerprint, identicalCallRun)
			}
			// Conversation lanes: repeated reads on the same file are
			// legitimate investigation, not loops. The model is probing
			// different symbols, line ranges, or patterns — not stuck.
			// Skip the doom-loop gate for read-class tools in conversation;
			// same-path fixation is handled by requestBudget before this
			// exact-argument branch so varied ranges are covered too.
			if r.lanePolicy().SkipFingerprintHardBlock() && isReadClassTool(req) {
				requestBudget.fingerprint.identicalRun = 0
			} else if r.DoomLoopGate != nil {
				r.emitEvent(RunEvent{Role: "doom_loop_guard", Content: fmt.Sprintf("model repeated %s %d times — approval required", req.Tool, identicalCallRun)})
				if r.DoomLoopGate(req, identicalCallRun) {
					// User approved — reset the streak and let this call execute.
					requestBudget.fingerprint.identicalRun = 0
				} else {
					if answer, reason, exhausted, ok := r.reviewRepairDoomLoopTerminalCloseout(collectedResults); ok {
						r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_doom_loop_closeout", Content: reason})
						return finish(answer, nil, reason, exhausted)
					}
					if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
						return finish(answer, nil, "host_read_only_closeout", true)
					}
					// User denied — feed a blocker result, then force a
					// no-tools closeout from gathered evidence. Returning the
					// raw rejectTool result can produce an empty final answer
					// when this is the first rejection in the turn.
					answer, err, done := controller.rejectTool(
						stepNumber,
						activeToolCall,
						req,
						"Doom-loop guard: user denied continuation",
						fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; user denied continuation. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
					)
					if !done {
						answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
					}
					return finish(answer, err, "doom_loop_guard", true)
				}
			} else {
				if answer, reason, exhausted, ok := r.reviewRepairDoomLoopTerminalCloseout(collectedResults); ok {
					r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_doom_loop_closeout", Content: reason})
					return finish(answer, nil, reason, exhausted)
				}
				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Doom-loop guard tripped",
					fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row; stopping to prevent budget exhaustion. Answer from the evidence already gathered, and mention only one unsupported claim if that repetition prevented completion.", req.Tool, identicalCallRun),
				)
				if !done {
					answer, err = controller.completeWithoutMoreToolsWithReason(doomLoopForcedCloseoutPrompt(req, identicalCallRun))
				}
				if closeout, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
					return finish(closeout, nil, "host_read_only_closeout", true)
				}
				return finish(answer, err, "doom_loop_guard", true)
			}
		}
		fingerprintDecision := requestBudget.fingerprintDecision(policy, req, currentFingerprint)
		if !planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, "submit_plan")); ok {
				planThenExecuteLoopBudgetRecoveryUsed = true
				opts = forceVisibleActionChatOptions(opts)
				if recovery.TerminalOnly && chatOptionsExposeTool(opts, "submit_plan") {
					opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
					opts.ToolChoice = "auto"
				}
				syncCurrentAllowedToolsFromOptions()
				if recovery.TerminalOnly {
					forcePlanThenExecuteTerminalSurface("loop_budget_recovery")
					currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
					setCurrentAllowedTools(currentAllowedTools)
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})
				continue
			}
		} else if planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, "submit_plan")); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, "submit_plan") {
					opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
					opts.ToolChoice = "auto"
				}
				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")
				currentAllowedTools = append(currentAllowedTools[:0], "submit_plan")
				setCurrentAllowedTools(currentAllowedTools)
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})
				continue
			}
		}
		// Conversation lanes: the fingerprint hard block's forced
		// completion cuts off legitimate investigation. Clear the
		// ForceAnswerPrompt before apply() so the model can continue.
		if r.lanePolicy().SkipFingerprintHardBlock() && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			fingerprintDecision.ForceAnswerPrompt = ""
			fingerprintDecision.ForceAnswerReason = ""
		}
		if res := fingerprintDecision.apply(budgetDecisionContext{
			controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
		}); res.shouldFinish {
			return finish(res.answer, res.err, res.reason, true)
		} else if res.shouldContinue {
			continue
		}
		if prompt, ok := closureSynthesisAfterIgnoredNudgePrompt(
			r.agentName(),
			r.RuntimeBrief,
			collectedResults,
			req,
			delegatedReadOnlyAnswerReady,
			postChildPacketActive,
			delegatedReadOnlyDiminishingNudgeStage,
			postChildDiminishingNudgeStage,
		); ok {
			answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
			return finish(answer, err, "closure_synthesis_after_ignored_nudge", true)
		}
		if res := requestBudget.explorationDecision(r, req, collectedResults).apply(budgetDecisionContext{
			controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
		}); res.shouldFinish {
			return finish(res.answer, res.err, res.reason, true)
		} else if res.shouldContinue {
			continue
		}
		if !r.planThenExecutePlannerPhase() &&
			// The synthesis nudge uses generic "answer now" language that
			// contradicts the conversation lane's evidence requirement
			// (file:line from at least one inspected source location).
			// The conversation lane has its own evidence-aligned nudge.
			!r.lanePolicy().SkipSynthesisNudge() {
			if res := requestBudget.synthesisDecision(policy, metrics.ToolCalls, runnerBriefRequiresMutation(r.RuntimeBrief)).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}
		if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject && !recoveryReadAllowanceGrantedThisRequest {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if r.planThenExecutePlannerPhase() {
				rebuildTools()
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.refreshPlanThenExecuteTerminalToolSurface(&turnMessages, collectedResults, "corrected_submit_plan_required")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: errorText})
			}
			if done {
				return finish(answer, err, "plan_then_execute_post_readiness_discovery_rejected", true)
			}
			continue
		}
		if isReadClassTool(req) {
			requestBudget.noteReadClassRequest(req)
			metrics.ReadClassCalls++
			if res := requestBudget.readBudgetDecision(policy, req).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}
		// Note tool: harness-owned scratch metadata, not a workspace mutation.
		if ok && strings.TrimSpace(req.Tool) == noteToolName {
			noteResult := runNoteTool(r.Workspace.Root(), raw)
			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, noteResult, "note_tool", false, false)
			r.emitEvent(RunEvent{Role: "note_tool", Content: noteResult.Summary})
			continue
		}
		discoveryRedirect.SetAllowedTools(currentAllowedTools)
		if !implementationScopedReadFileRequestInExplicitScope(r.RuntimeBrief, collectedResults, req) {
			// Conversation-lane redirect: repeated read_file → code_navigate.
			// The model often reads the same file in 50-line chunks when it
			// could use read_symbol to get full symbol bodies in one call.
			if decision := discoveryRedirect.considerConversationReadFileRedirect(req, r.RuntimeBrief); decision.ShouldRedirect {
				result := decision.Result
				recorder.activeToolCall = activeToolCall
				recorder.record(stepNumber, req, result, "discovery_redirect_conversation_read", false, false)
				r.emitEvent(RunEvent{Role: "discovery_redirect_conversation", Content: fmt.Sprintf("%s → code_navigate read_symbol", req.Tool)})
				continue
			}
			if decision := discoveryRedirect.Consider(req, r.Workspace); decision.ShouldRedirect {
				// Hard-reject path: the model has been nudged enough times
				// on this redirect class. Route through rejectTool so the
				// fingerprint is recorded in rejectedFingerprints and a
				// repeat terminates the turn. The soft-redirect path stays
				// as a result append with no rejection counters bumped.
				if decision.HardReject {
					if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
						r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
					}
					r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s) HARD_REJECT", req.Tool, decision.Language, decision.Capability)})
					// Hide the source tool from the manifest for the rest
					// of the turn. If shell was redirected to a structured
					// tool N times and now hard-rejected, the model has
					// shown it won't self-correct — remove the tool.
					if tool := strings.TrimSpace(req.Tool); tool != "" && !turnHiddenTools[tool] && r.shouldHideToolAfterDiscoveryHardRedirect(req) {
						turnHiddenTools[tool] = true
						rebuildTools()
						r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after hard-redirect", tool)})
					}
					answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, decision.Result.Summary, decision.Result.Error)
					if done {
						return finish(answer, err, "redirect_hard_block_exhausted", true)
					}
					continue
				}
				result := decision.Result
				recorder.activeToolCall = activeToolCall
				recorder.record(stepNumber, req, result, "discovery_redirect", false, false)
				if strings.TrimSpace(decision.DecisionGuide) != "" && !runnerClosureGuidanceActive(r.agentName(), r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive) {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.DecisionGuide})
					r.emitEvent(RunEvent{Role: "discovery_decision_guide", Content: decision.DecisionGuide})
				}
				r.emitEvent(RunEvent{Role: "discovery_redirect", Content: fmt.Sprintf("%s → %s (%s)", req.Tool, decision.Language, decision.Capability)})
				continue
			}
		}
		if summary, errorText, reject := declarationMoveConstraintRejection(req, declarationMovesForbidden); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "go_refactor_guard_exhausted", true)
			}
			continue
		}
		if !evidenceFirstTargetPhaseCleared && evidenceFirstTargetReadUnlocked(r.RuntimeBrief, collectedResults) {
			clearEvidenceFirstTargetReadActionClasses(rejectedActions, r.RuntimeBrief)
			evidenceFirstTargetPhaseCleared = true
		}
		repeatedDeadEndRecovered := false
		if actionClass != "" && rejectedActions[actionClass] > 0 {
			if redirected, redirectedRaw, ok := r.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, collectedResults, currentAllowedTools); ok {
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(redirected.Tool),
					"original_raw":   strings.TrimSpace(raw),
					"rewritten_raw":  strings.TrimSpace(redirectedRaw),
					"reason":         "plan_execute_symbol_read_after_read_failure_bounded",
				}))
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
				})
				req = redirected
				raw = redirectedRaw
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				repeatedDeadEndRecovered = true
			}
		}
		if actionClass != "" && rejectedActions[actionClass] > 0 && !repeatedDeadEndRecovered {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Repeated dead-end action rejected",
				"this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
			)
			if done {
				return finish(answer, err, "repeated_dead_end_budget_exhausted", true)
			}
			continue
		}
		if r.Workspace.IsExploreMode() && workspace.IsBroadInspectionRequest(req) && executionState.broadInspectionCount >= 1 {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Additional broad inspection rejected",
				"explore mode already has a workspace overview for this turn. Available contract outcomes are an answer from the existing overview, file-name search, exact text search, or bounded reads on discovered handles instead of another broad listing.",
			)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(req.Tool), "list_files") && executionState.consecutiveListFiles >= 2 {
			// Hide list_files from the manifest for the rest of the turn
			// — at this point we've told the model "stop walking dirs"
			// once already, and keeping list_files in the tool list just
			// invites another rejected call. Mode-gating the tool out is
			// more effective than rejecting each retry with a message the
			// model already ignored.
			if !turnHiddenTools["list_files"] {
				turnHiddenTools["list_files"] = true
				rebuildTools()
				r.emitEvent(RunEvent{Role: "tool_hidden", Content: "list_files hidden for rest of turn after chained-gate fire"})
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Chained list_files rejected",
				fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files is hidden for the rest of this turn. Take one narrower next step on a path you already discovered, or answer with the evidence already gathered.", executionState.consecutiveListFiles),
			)
			if done {
				return finish(answer, err, "list_files_chain_budget_exhausted", true)
			}
			continue
		}
		if executionState.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, r.RuntimeBrief) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Discovery consumption required before more broad discovery",
				executionState.discoveryConsumptionError(),
			)
			if done {
				return finish(answer, err, "discovery_consumption_budget_exhausted", true)
			}
			continue
		}
		if executionState.shouldConsumeSearchHitBeforeMoreSearch(req) {
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Chained search rejected",
				executionState.searchHitConsumptionError(),
			)
			if done {
				return finish(answer, err, "search_chain_budget_exhausted", true)
			}
			continue
		}
		if executionState.needsNarrowingAfterBroad && workspace.IsBroadInspectionRequest(req) {
			errorText := "you already have a broad workspace overview. Either answer from the evidence already gathered, or take one narrower next step (a discovered handle, a single named symbol, or a focused search) before any further broad inspection."
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, "Broad inspection request rejected", errorText)
			if done {
				return finish(answer, err, "broad_inspection_budget_exhausted", true)
			}
			continue
		}
		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" {
			if lastToolResult != nil {
				if allowRecoverablePostReadinessRepairExecution(req, *lastToolResult) {
					delete(replayedToolRequests, toolKey)
				} else {
					if summary, errorText, reject := repeatedDiagnosticLogReadFileExactRejection(req, *lastToolResult); reject {
						answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
						if done {
							return finish(answer, err, "repeated_diagnostic_log_read_rejected", true)
						}
						continue
					}
					if replayedToolRequests[toolKey] > 0 {
						summary := "Repeated tool request rejected"
						errorText := "this exact request already replayed prior evidence once. Do not call it again. Choose a materially different tool or target, edit within the approved scope, request a target-scope update, or answer with the concrete blocker."
						if replaySummary, replayError, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req, lastToolResult, collectedResults); ok {
							summary = replaySummary
							errorText = replayError
						}
						answer, err, done := controller.rejectTool(
							stepNumber,
							activeToolCall,
							req,
							summary,
							errorText,
						)
						if done {
							return finish(answer, err, "repeated_tool_budget_exhausted", true)
						}
						continue
					}
					replayed := replayRepeatedToolRequest(req, *lastToolResult)
					replayedToolRequests[toolKey]++
					recorder.activeToolCall = activeToolCall
					recorder.recordWithOptions(stepNumber, req, replayed, "tool_replayed", runnerRecordOptions{
						ResetRejected:       true,
						IncrementNoProgress: true,
						SetLastToolKey:      false,
					})
					if readOnlyReplayedEvidenceCloseoutApplies(r.agentName(), r.RuntimeBrief, collectedResults, req, replayed) {
						if readOnlyDirectAnswerExactSearchReplayCloseoutCandidate(r.RuntimeBrief, req) {
							if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
								return finish(answer, nil, "host_read_only_closeout", true)
							}
						}
						answer, err := controller.completeWithoutMoreToolsWithReason(readOnlyReplayedEvidenceCloseoutPrompt(r.RuntimeBrief))
						return finish(answer, err, "read_only_replayed_evidence_closeout", true)
					}
					if policy.shouldStopForNoProgress(consecutiveNoProgress) {
						answer, err := controller.completeWithoutMoreToolsWithReason("Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the repeated request leaves one exact claim unsupported, state that uncertainty after the supported answer.")
						return finish(answer, err, "no_progress_budget_exhausted", true)
					}
					continue
				}
			}
			errorText := "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes."
			if strings.TrimSpace(req.Tool) == "go_move_declarations" {
				errorText = "you just ran the same declaration move. Choose a different destination/declaration batch, run verification, or answer with the current blocker."
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				"Repeated tool request rejected",
				errorText,
			)
			if done {
				return finish(answer, err, "repeated_tool_budget_exhausted", true)
			}
			continue
		}
		permissionDecision := r.PermissionPolicy.Decide(req)
		if r.Workspace != nil && r.Workspace.EvaluateConfiguredToolRule(req) == workspace.ToolRuleDeny {
			permissionDecision = PermissionDecisionSnapshot{
				Tool:     strings.TrimSpace(req.Tool),
				Target:   permissionTarget(req),
				Decision: PermissionDeny,
				Rule:     "workspace.tool_rules decision=deny",
			}
		}
		if autonomyRecorder != nil {
			autonomyRecorder.emitIntentClassified(req)
			autonomyRecorder.emitPathRolesResolved(req)
		}
		if permissionDecision.Decision != PermissionAllow {
			r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))
			if autonomyRecorder != nil {
				decision := autonomy.PolicyDeny
				resultLabel := "denied"
				if permissionDecision.Decision == PermissionAsk {
					decision = autonomy.PolicyAsk
					resultLabel = "approval_required"
				}
				autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:         decision,
					ReasonCode:       "permission_policy",
					ApprovalRequired: permissionDecision.Decision == PermissionAsk,
					Result:           resultLabel,
					ErrorClass:       "permission_policy",
					Source:           "runner_permission_policy",
				})
			}
			result := permissionDecision.ToolResult()
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "permission_denied", runnerRecordOptions{
				CountRejected:  true,
				SetLastToolKey: true,
			})
			if workspaceToolRulesPermissionDeny(permissionDecision) {
				answer := workspaceToolRulesPermissionDenyAnswer(req, result)
				emitCompletionStatus("blocked", "workspace_tool_rules_deny", "workspace_tool_rules_deny", collectedResults, true)
				return finish(answer, nil, "workspace_tool_rules_deny", true)
			}
			if permissionDecision.Decision == PermissionAsk {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "permission_approval_required", false)
			}
			continue
		}
		r.emitEvent(marshalRunEvent("permission_decision", permissionDecision))
		if autonomyRecorder != nil {
			autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
				Decision:   autonomy.PolicyAllow,
				ReasonCode: "permission_policy",
				Result:     "no_approval_needed",
				Source:     "runner_permission_policy",
			})
		}
		if strings.TrimSpace(req.Tool) == "task" {
			if r.DisableTaskTool {
				answer, err, done := controller.rejectTool(
					stepNumber,
					activeToolCall,
					req,
					"Child task request rejected",
					"nested task spawning is disabled inside child tasks. Answer with the evidence already collected or inspect directly with read-only tools.",
				)
				if done {
					return finish(answer, err, "nested_task_budget_exhausted", true)
				}
				continue
			}
			result := r.runChildTask(ctx, req, opts)
			recorder.activeToolCall = activeToolCall
			if strings.TrimSpace(result.Error) == "" && !isWriteChildMode(req.Mode) {
				requestBudget.readOnlyChildTaskSatisfied = true
				turnHiddenTools["task"] = true
				rebuildTools()
				if readOnlyTaskResultAnswerableForObjective(result, r.RuntimeBrief.Objective) {
					evidencePaths := postChildClosureEvidencePaths(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, 4)
					requestBudget.broadEvidenceDelegationSatisfied = true
					r.RuntimeBrief.Constraints = mergeRunnerStateStrings(r.RuntimeBrief.Constraints, []string{"child_packet_available"})
					postChildPacketActive = true
					postChildFollowupReads = 0
					postChildDiminishingNudgeStage = 0
					r.RuntimeBrief.EvidencePaths = mergeRunnerStateStrings(r.RuntimeBrief.EvidencePaths, evidencePaths)
					requestBudget.noteReadOnlyChildPacket(evidencePaths)
					hideBroadDiscoveryTools(turnHiddenTools)
					rebuildTools()
					requestBudget.postChild.readCalls = 0
					nudge := childPacketBoundaryNudge(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, evidencePaths, postChildReadBudget)
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
				}
			}
			recorder.record(stepNumber, req, result, "child_task_result", true, true)
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				if shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults) {
					continue
				}
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Tools are now disabled. Use the child-task result and gathered evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the supported answer.")
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "task_ledger" {
			result := taskLedger.apply(req)
			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", true, true)
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}
		if strings.TrimSpace(req.Tool) == "delegate_edit" {
			result := r.runDelegateEdit(ctx, req)
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
				TrackProgress:  true,
				ResetRejected:  true,
				SetLastToolKey: true,
			})
			if checklist := r.requiredEditPathsProgressMessage(req, result, collectedResults); checklist != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: checklist})
				r.emitEvent(RunEvent{Role: "required_edit_paths_progress", Content: checklist})
			}
			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the delegated edit. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", true)
			}
			continue
		}

		if r.PreExecute != nil {
			if guarded := r.PreExecute(stepNumber, req); guarded != nil {
				bootstrapReadReminder := isBootstrappedTargetReadReminder(req, *guarded)
				rejectedStreak := false
				rejectedActionCounted := false
				if actionClass != "" && strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder {
					rejectedActions[actionClass]++
					rejectedStreak = true
					rejectedActionCounted = true
				} else if strings.TrimSpace(guarded.Error) != "" {
					rejectedActionCounted = true
				}
				recorder.activeToolCall = activeToolCall
				recorder.recordWithOptions(stepNumber, req, *guarded, "pre_execute_guard", runnerRecordOptions{
					CountRejected:           rejectedActionCounted,
					IncrementRejectedStreak: rejectedStreak,
					IncrementNoProgress:     strings.TrimSpace(guarded.Error) != "" && !rejectedStreak,
					SetLastToolKey:          true,
				})
				r.markConcreteSourceEvidenceObserved(*guarded)
				if strings.TrimSpace(guarded.Summary) == "Write approval required" {
					if autonomyRecorder != nil {
						autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
							Decision:         autonomy.PolicyAsk,
							ReasonCode:       "write_approval_required",
							ApprovalRequired: true,
							Result:           "approval_required",
							ErrorClass:       "approval_guard",
							Source:           "runner_pre_execute_guard",
						})
					}
					answer, err := controller.completeWithoutMoreTools()
					return finish(answer, err, "write_approval_required", false)
				}
				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && consecutiveRejectedActions >= 2 {
					answer, err := controller.completeWithoutMoreTools()
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}
				if bootstrapReadReminder && policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", true)
				}
				continue
			}
		}

		if guarded := checkWriteDescription(req); guarded != nil {
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, *guarded, "write_description_missing", runnerRecordOptions{
				CountRejected:           true,
				IncrementRejectedStreak: true,
				SetLastToolKey:          true,
			})
			writeDescriptionRepairActive = true
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = normalizeRunnerPath(req.Path)
			implementationWriteOnlyRecoveryActive = true
			if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
				requiredPathCompletionRecoveryActive = true
			}
			markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
			hideImplementationWriteOnlyToolsForTurn(turnHiddenTools, implementationWriteOnlyHiddenTools, r.Workspace)
			rebuildTools()
			prompt := r.writeDescriptionRepairPrompt(req)
			r.emitWriteDescriptionValidatorDecision(stepNumber, req, "force_write_or_block", "write_description_missing", "write_description_repair")
			r.emitEvent(RunEvent{Role: "write_description_repair_recovery", Content: prompt})
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			if consecutiveRejectedActions >= 2 {
				answer, err := controller.completeWithoutMoreTools()
				return finish(answer, err, "write_description_budget_exhausted", true)
			}
			continue
		}

		if summary, errorText, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
			if exactAnchorWriteOnlyActive && workspace.IsWorkspaceWriteTool(req.Tool) {
				exactAnchorWriteFailures++
			}
			if autonomyRecorder != nil {
				autonomyRecorder.emitWriteGate(req, autonomy.PolicyReject, "required_edit_scope_rejected", mapKeys(changedPathsFromToolResults(collectedResults)))
				autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:   autonomy.PolicyDeny,
					ReasonCode: "required_edit_scope_rejected",
					Result:     "denied",
					ErrorClass: "required_edit_scope",
					Source:     "runner_required_edit_scope",
				})
			}
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if exactAnchorWriteOnlyActive && (rejectionRecoveryNudgeUsed || exactAnchorWriteFailures >= 3) {
				result := workspace.ToolResult{Tool: req.Tool, Summary: summary, Error: errorText}
				if blocker, ok := r.exactAnchorRepeatedWriteFailureBlockerAnswer(req, result, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_write_failure_repeated", "exact_anchor_write_failure_repeated", collectedResults, true)
					return finish(blocker, nil, "exact_anchor_write_failure_repeated", true)
				}
			}
			if done {
				return finish(answer, err, "required_edit_scope_rejected", true)
			}
			continue
		}

		if summary, errorText, reject := plannerOwnedVerificationShellRejection(req, r.RuntimeBrief); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "planner_owned_verification_shell_rejected", true)
			}
			continue
		}

		if summary, errorText, reject := sourceEditVerificationRejection(req, r.RuntimeBrief, collectedResults); reject {
			if autonomyRecorder != nil {
				autonomyRecorder.emitVerificationGate(req, autonomy.PolicyReject, "source_edit_verification_required", "pending")
			}
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "source_edit_verification_required", true)
			}
			continue
		}

		if prompt, ok := sourceEditCloseoutPrompt(req, r.RuntimeBrief, collectedResults); ok {
			answer, err := controller.completeWithoutMoreToolsWithReason(prompt)
			return finish(answer, err, "source_edit_closeout_after_verification", true)
		}

		if executionState.needsCheckpoint(r.CheckpointPolicy, req) {
			checkpoint, checkpointErr := CaptureGitCheckpoint(ctx, r.Workspace.Root())
			if checkpointErr != nil {
				result := workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Checkpoint creation failed",
					Error:   checkpointErr.Error(),
				}
				recorder.activeToolCall = activeToolCall
				recorder.recordWithOptions(stepNumber, req, result, "checkpoint_failed", runnerRecordOptions{
					CountRejected:  true,
					SetLastToolKey: true,
				})
				continue
			}
			executionState.markCheckpointCreated()
			r.emitEvent(marshalRunEvent("checkpoint", checkpoint))
		}

		if executionState.shouldRequireVerification(req, r.RuntimeBrief) {
			summary, errorText := verificationRequiredAfterWritesMessage(r.RuntimeBrief, executionState.writesSinceVerification)
			if autonomyRecorder != nil {
				autonomyRecorder.emitVerificationGate(req, autonomy.PolicyReject, "verification_required_after_writes", "pending")
			}
			answer, err, done := controller.rejectTool(
				stepNumber,
				activeToolCall,
				req,
				summary,
				errorText,
			)
			if done {
				return finish(answer, err, "verification_required_after_writes", true)
			}
			continue
		}

		var result workspace.ToolResult
		reviewRepairNoopPassAnswer := ""
		if guard := r.preflightPlanThenExecuteWriteGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_write_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(req); guard.Intercepted {
			result = guard.Result
			if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
				reviewRepairNoopPassAnswer = closeout.Answer
			}
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_review_repair_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, collectedResults); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, collectedResults); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_plan_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecutePlannerToolGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_tool_guard",
				Content: guard.Reason,
			})
		} else if guard := r.preflightPlanThenExecuteReviewRepairBroadReadGuard(req); guard.Intercepted {
			result = guard.Result
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_review_repair_guard",
				Content: guard.Reason,
			})
		} else if intercept := r.preflightVerificationOnAlreadyEditedPath(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "verification_intercept_already_edited",
				Content: intercept.Reason,
			})
		} else if intercept := r.preflightScopedImplementationBoundedSourceRead(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "scoped_bounded_source_read",
				Content: intercept.Reason,
			})
		} else if intercept := r.preflightScopedImplementationLargeWriteFileReplacement(req, collectedResults); intercept.Intercepted {
			result = intercept.Result
			r.emitEvent(RunEvent{
				Role:    "scoped_large_write_file_guard",
				Content: intercept.Reason,
			})
		} else {
			switch precheck := r.preflightApplyPatchAgainstChangedPaths(req, collectedResults); precheck.Action {
			case applyPatchPreflightAllSkipped:
				result = precheck.Result
				r.emitEvent(RunEvent{
					Role:    "apply_patch_changed_path_preflight",
					Content: fmt.Sprintf("all patch sections redundant; skipped %s", strings.Join(precheck.SkippedPaths, ", ")),
				})
			case applyPatchPreflightRewrite:
				r.emitEvent(RunEvent{
					Role:    "apply_patch_changed_path_preflight",
					Content: fmt.Sprintf("dropped %d redundant section(s) from apply_patch envelope before workspace dispatch: %s", len(precheck.SkippedPaths), strings.Join(precheck.SkippedPaths, ", ")),
				})
				req = precheck.Request
				result = r.Workspace.Execute(ctx, req)
			default:
				result = r.Workspace.Execute(ctx, req)
			}
			// Convert "file not found" probes on required_edit_paths
			// into an actionable nudge that points the planner at the
			// whole-file shape on submit_plan. Without this rewrite,
			// the planner treats absent new-file paths as a blocker
			// and gives up. See harness fix #7.
			result = rewriteMissingRequiredNewFileProbe(req, result, r.RuntimeBrief)
			result = r.recoverContextPackFreshness(ctx, req, result)
		}
		recorder.activeToolCall = activeToolCall
		recorder.recordWithOptions(stepNumber, req, result, "tool_result", runnerRecordOptions{
			TrackProgress:  true,
			ResetRejected:  true,
			SetLastToolKey: true,
		})
		r.markConcreteSourceEvidenceObserved(result)
		if !recoveryReadAllowanceGrantedThisRequest && implementationTargetSourceReadEstablishedWriteReady(r.RuntimeBrief, req, result, collectedResults) {
			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed %s source read to establish target-write-ready evidence on %q", strings.TrimSpace(req.Tool), normalizeRunnerPath(req.Path)),
			})
		}
		if completionGateRejections > 0 && strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Tool) != "completion_gate" {
			completionGateRejections = 0
			r.emitEvent(RunEvent{Role: "completion_gate_recovery_progress", Content: "successful tool result reset the completion-gate retry streak"})
		}
		update := executionState.applyResult(req, result, currentFingerprint, r.Workspace, &requestBudget)
		if autonomyRecorder != nil && workspaceWriteResultCompleted(req, result) {
			autonomyRecorder.emitWriteGate(req, autonomy.PolicyAllow, "write_result_completed", mapKeys(changedPathsFromToolResults([]workspace.ToolResult{result})))
		}
		if deferredGitHistorySubmitPlan != nil && gitHistoryEvidenceTool(req.Tool) {
			submitReq := *deferredGitHistorySubmitPlan
			if len(r.submitPlanPathsMissingGitHistory(submitReq, collectedResults)) == 0 {
				submitRaw := deferredGitHistorySubmitPlanRaw
				if strings.TrimSpace(submitRaw) == "" {
					submitRaw = marshalToolRequestRaw(submitReq)
				}
				metrics.ToolCalls++
				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(submitReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            submitRaw,
						Request:        submitReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(submitReq),
						Narrowing:      workspace.IsNarrowingRequest(submitReq),
						Action:         toolActionClass(submitReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}
				var submitResult workspace.ToolResult
				if guard := r.preflightPlanThenExecuteSubmitPlanGroundingGuard(submitReq, collectedResults); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else if guard := r.preflightPlanThenExecuteSubmitPlanHistoryGuard(submitReq, collectedResults); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else if guard := r.preflightPlanThenExecuteSubmitPlanCoverageGuard(submitReq); guard.Intercepted {
					submitResult = guard.Result
					r.emitEvent(RunEvent{Role: "plan_then_execute_plan_guard", Content: guard.Reason})
				} else {
					submitResult = r.Workspace.Execute(ctx, submitReq)
				}
				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, submitReq, submitResult, "deferred_git_history_submit_plan", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})
				req = submitReq
				result = submitResult
				update = executionState.applyResult(submitReq, submitResult, toolRequestKey(submitReq), r.Workspace, &requestBudget)
				deferredGitHistorySubmitPlan = nil
				deferredGitHistorySubmitPlanRaw = ""
			}
		}
		if deferredSourceEvidenceWrite != nil && gitHistoryEvidenceTool(req.Tool) {
			if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterHistoryEvidence(collectedResults); replay {
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  strings.TrimSpace(req.Tool),
					"rewritten_tool": strings.TrimSpace(replayReq.Tool),
					"original_raw":   marshalToolRequestRaw(req),
					"rewritten_raw":  strings.TrimSpace(replayRaw),
					"reason":         "implementation_write_after_history_evidence_replay",
				}))
				metrics.ToolCalls++
				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(replayReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            replayRaw,
						Request:        replayReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(replayReq),
						Narrowing:      workspace.IsNarrowingRequest(replayReq),
						Action:         toolActionClass(replayReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}
				replayResult := r.Workspace.Execute(ctx, replayReq)
				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, replayReq, replayResult, "deferred_git_history_write", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})
				r.markConcreteSourceEvidenceObserved(replayResult)
				req = replayReq
				result = replayResult
				update = executionState.applyResult(replayReq, replayResult, toolRequestKey(replayReq), r.Workspace, &requestBudget)
				deferredSourceEvidenceWrite = nil
			}
		}
		if replayedOverVerificationRequest != nil && workspaceWriteResultCompleted(req, result) {
			verifyReq := *replayedOverVerificationRequest
			verifyRaw := marshalToolRequestRaw(verifyReq)
			if hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) ||
				sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults) {
				prompt := postWriteVerificationRequiredPrompt(collectedResults, plannerOwnsVerification(r.RuntimeBrief))
				r.emitEvent(RunEvent{Role: "post_write_verification_required", Content: prompt})
			}
			metrics.ToolCalls++
			if onToolRequest != nil {
				profile := workspace.DiscoveryProfileForRequest(verifyReq)
				onToolRequest(ToolRequestEvent{
					Step:           stepNumber,
					Agent:          r.agentName(),
					Raw:            verifyRaw,
					Request:        verifyReq,
					Mode:           r.Workspace.Mode(),
					Broad:          workspace.IsBroadInspectionRequest(verifyReq),
					Narrowing:      workspace.IsNarrowingRequest(verifyReq),
					Action:         toolActionClass(verifyReq),
					DiscoveryClass: profile.Class,
					DiscoveryScope: profile.Scope,
					DiscoveryCost:  profile.Cost,
				})
			}
			verifyResult := r.Workspace.Execute(ctx, verifyReq)
			recorder.activeToolCall = nil
			recorder.recordWithOptions(stepNumber, verifyReq, verifyResult, "deferred_post_write_verification", runnerRecordOptions{
				TrackProgress:  true,
				ResetRejected:  true,
				SetLastToolKey: true,
			})
			r.markConcreteSourceEvidenceObserved(verifyResult)
			req = verifyReq
			result = verifyResult
			update = executionState.applyResult(verifyReq, verifyResult, toolRequestKey(verifyReq), r.Workspace, &requestBudget)
		}
		if workspaceWriteResultCompleted(req, result) {
			if verifyReq, ok := deterministicPostWriteVerificationRequest(collectedResults, r.RuntimeBrief); ok {
				verifyRaw := marshalToolRequestRaw(verifyReq)
				metrics.ToolCalls++
				if onToolRequest != nil {
					profile := workspace.DiscoveryProfileForRequest(verifyReq)
					onToolRequest(ToolRequestEvent{
						Step:           stepNumber,
						Agent:          r.agentName(),
						Raw:            verifyRaw,
						Request:        verifyReq,
						Mode:           r.Workspace.Mode(),
						Broad:          workspace.IsBroadInspectionRequest(verifyReq),
						Narrowing:      workspace.IsNarrowingRequest(verifyReq),
						Action:         toolActionClass(verifyReq),
						DiscoveryClass: profile.Class,
						DiscoveryScope: profile.Scope,
						DiscoveryCost:  profile.Cost,
					})
				}
				verifyResult := r.Workspace.Execute(ctx, verifyReq)
				recorder.activeToolCall = nil
				recorder.recordWithOptions(stepNumber, verifyReq, verifyResult, "deterministic_post_write_verification", runnerRecordOptions{
					TrackProgress:  true,
					ResetRejected:  true,
					SetLastToolKey: true,
				})
				r.markConcreteSourceEvidenceObserved(verifyResult)
				req = verifyReq
				result = verifyResult
				update = executionState.applyResult(verifyReq, verifyResult, toolRequestKey(verifyReq), r.Workspace, &requestBudget)
			}
		}
		if update.ResetVerificationGuards {
			clearVerificationGateFingerprints(requestBudget.fingerprint.rejected)
			clearRunnerStringIntMap(rejectedActions)
			rebuildTools()
		}
		if workspaceWriteResultCompleted(req, result) &&
			(hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) ||
				sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults)) {
			prompt := postWriteVerificationRequiredPrompt(collectedResults, plannerOwnsVerification(r.RuntimeBrief))
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			opts = forceVisibleActionChatOptions(opts)
			syncCurrentAllowedToolsFromOptions()
			r.emitEvent(RunEvent{Role: "post_write_verification_required", Content: prompt})
		}
		if !terminalSubmitPlanNativeTools() && len(r.remainingRequiredEditPaths(collectedResults)) > 0 &&
			implementationRequiredPathAnchorsReady(r.RuntimeBrief, collectedResults) &&
			(!r.planThenExecutePlannerPhase() || planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, collectedResults)) {
			opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, false)
			r.emitEvent(RunEvent{Role: "required_edit_paths_visible_write_mode", Content: "all remaining required edit paths have anchor-ready source evidence; textual backend recovery will disable hidden thinking and require visible write/blocker output"})
		}
		r.emitVerificationPhaseEvent(update.VerificationState, update.VerificationDetail)
		if autonomyRecorder != nil && update.VerificationState != "" {
			decision := autonomy.PolicyAllow
			if update.VerificationState == "failed" {
				decision = autonomy.PolicyReject
			}
			autonomyRecorder.emitVerificationGate(req, decision, "verification_result_"+update.VerificationState, update.VerificationState)
		}
		if update.VerificationState == "done" &&
			runnerBriefRequiresMutation(r.RuntimeBrief) &&
			len(r.RuntimeBrief.RequiredEditPaths) == 0 &&
			len(changedPathsFromToolResults(collectedResults)) > 0 &&
			!hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) &&
			!sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults) {
			if !verificationTelemetryEmitted {
				verificationTelemetryEmitted = r.emitPostWriteVerificationTelemetry(req, result)
			}
			answer, err := controller.completeWithoutMoreToolsWithReason("The requested implementation has a recorded workspace write and successful source-aware verification. Tools are now disabled. Write the implementation result now: changed paths and verification status.")
			return finish(answer, err, "source_edit_closeout_after_verification", false)
		}
		if exactAnchorWriteOnlyActive && workspaceWriteResultCompleted(req, result) {
			exactAnchorWriteOnlyActive = false
			releaseExactAnchorReadTools(turnHiddenTools, exactAnchorHiddenTools)
			rebuildTools()
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "read-class tools restored after successful exact-anchor write"})
		} else if exactAnchorWriteOnlyActive && workspace.IsWorkspaceWriteTool(req.Tool) && strings.TrimSpace(result.Error) != "" {
			exactAnchorWriteFailures++
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "read-class tools remain hidden after failed exact-anchor write"})
			if rejectionRecoveryNudgeUsed || exactAnchorWriteFailures >= 3 {
				if answer, ok := r.exactAnchorRepeatedWriteFailureBlockerAnswer(req, result, collectedResults); ok {
					emitCompletionStatus("blocked", "exact_anchor_write_failure_repeated", "exact_anchor_write_failure_repeated", collectedResults, true)
					return finish(answer, nil, "exact_anchor_write_failure_repeated", true)
				}
			}
		}
		if implementationWriteOnlyRecoveryActive && workspaceWriteResultCompleted(req, result) && len(r.remainingRequiredEditPaths(collectedResults)) == 0 {
			implementationWriteOnlyRecoveryActive = false
			implementationWriteOnlyNonWriteRejects = 0
			requiredPathCompletionRecoveryActive = false
			writeDescriptionRepairActive = false
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = ""
			releaseHiddenTools(turnHiddenTools, implementationWriteOnlyHiddenTools)
			rebuildTools()
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "non-write tools restored after required implementation writes completed"})
		}
		if writeDescriptionRepairActive && workspaceWriteResultCompleted(req, result) && normalizeRunnerPath(req.Path) == writeDescriptionRepairPath {
			writeDescriptionRepairActive = false
			writeDescriptionDetourRejections = 0
			writeDescriptionRepairPath = ""
		}
		if prompt, ok := r.scratchpadDeliverableFailedEvidenceNudge(req, result, collectedResults); ok {
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "scratchpad_deliverable_failed_evidence_nudge", Content: prompt})
		}
		if checklist := r.requiredEditPathsProgressMessage(req, result, collectedResults); checklist != "" {
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: checklist})
			r.emitEvent(RunEvent{Role: "required_edit_paths_progress", Content: checklist})
		}
		if reviewRepairNoopPassAnswer != "" {
			closeout, _ := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req)
			r.emitEvent(RunEvent{Role: closeout.EventRole, Content: closeout.EventContent})
			return finish(reviewRepairNoopPassAnswer, nil, closeout.StopReason, false)
		}
		if reviewRepairNoopSubmitPlanCloseoutRequired(r.RuntimeBrief, req, result) {
			if closeout, ok := reviewRepairTerminalCloseoutForRequest(r.RuntimeBrief, req); ok {
				r.emitEvent(RunEvent{Role: closeout.EventRole, Content: closeout.EventContent})
				return finish(closeout.Answer, nil, closeout.StopReason, false)
			}
			answer, err := controller.completeWithoutMoreToolsWithReason(reviewRepairNoopSubmitPlanCloseoutPrompt(req, result))
			if err == nil {
				if gateResult, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(r.RuntimeBrief, answer); rejected {
					answer = reviewRepairNoopSubmitPlanDeterministicCloseout(r.RuntimeBrief, gateResult)
				}
			}
			return finish(answer, err, "review_repair_noop_plan_closeout", false)
		}
		if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(r.RuntimeBrief, req, result, collectedResults); ok {
			recorder.activeToolCall = activeToolCall
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_source_satisfied_short_circuit",
				Content: "empty submit_plan encoded a source-satisfied terminal outcome; synthesizing closeout from required-path source evidence",
			})
			return finish(answer, nil, "plan_source_satisfied_short_circuit", false)
		}
		if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			prompt := planThenExecuteSourceSatisfiedTerminalRequiredPrompt(r.RuntimeBrief, result)
			refreshPlanThenExecuteTerminalSurface("source_satisfied_terminal_required")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			opts = forceVisibleActionChatOptions(opts)
			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_source_satisfied_terminal_required", Content: prompt})
			continue
		}
		if r.planThenExecutePlannerPhase() && planThenExecuteCandidateSourceReadyResult(result) {
			prompt := r.planThenExecuteSubmitReadyTerminalPrompt(result.Error, collectedResults, chatOptionsExposeTool(opts, "submit_plan"))
			opts = forceVisibleActionChatOptions(opts)
			if chatOptionsExposeTool(opts, "submit_plan") {
				opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
				opts.ToolChoice = "auto"
			}
			syncCurrentAllowedToolsFromOptions()
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_ready_terminal_required", Content: prompt})
			continue
		}
		if r.planThenExecutePlannerPhase() && planThenExecuteSubmitPlanRejectedForCorrection(req, result) {
			recoveryState := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
				replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&turnMessages, req, result)
				eventRole := "plan_then_execute_grounding_required"
				prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteGroundingRequiredRecoveryPlan())
				if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
					rejectedAt := len(collectedResults) - 1
					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, result, rejectedAt)
					eventRole = "plan_then_execute_anchor_refresh_required"
				}
				refreshPlanThenExecuteTerminalSurface("submit_plan_grounding_rejected")
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				rebuildTools()
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
				continue
			}
			replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&turnMessages, req, result)
			rejectedAt := len(collectedResults) - 1
			refreshNote := planThenExecutePostSubmitRejectionRefreshNote(r.RuntimeBrief, collectedResults, result, rejectedAt)
			prompt := "The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(result) + "The next visible action must be a corrected parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not call read/search/discovery in submit-correction mode. " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, result) + " " + refreshNote
			eventRole := "plan_then_execute_submit_plan_required"
			if planThenExecuteUnscopedEmptySubmitPlanRejected(r.RuntimeBrief, collectedResults) {
				prompt = RenderPlanExecuteRecoveryPlan(planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan())
				eventRole = "plan_then_execute_empty_submit_plan_recovery"
			} else if recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh {
				prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, result, rejectedAt)
				eventRole = "plan_then_execute_anchor_refresh_required"
			} else if terminalSubmitPlanNativeTools() {
				prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, result)
			}
			refreshPlanThenExecuteTerminalSurface("submit_plan_rejected")
			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			rebuildTools()
			opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
			continue
		}
		if !planThenExecuteReadinessNudged {
			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, collectedResults); nudge != "" {
				nextUnread := firstPlanThenExecuteUnreadRequiredPath(r.requiredEditCoveragePaths(), planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, collectedResults))
				if nextUnread != "" && nextUnread != planThenExecuteUnreadSourceNudgedFor {
					planThenExecuteUnreadSourceNudgedFor = nextUnread
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					opts = planThenExecuteUnreadRequiredSourceChatOptions(opts, r.RuntimeBrief, collectedResults)
					syncCurrentAllowedToolsFromOptions()
					r.emitEvent(RunEvent{Role: "plan_then_execute_unread_source_nudge", Content: nudge})
				}
			}
			if nudge := planThenExecuteReadyToSubmitNudge(r.RuntimeBrief, collectedResults); nudge != "" {
				planThenExecuteReadinessNudged = true
				refreshPlanThenExecuteTerminalSurface("readiness")
				if terminalSubmitPlanNativeTools() {
					nudge = planThenExecuteNativeSubmitPlanNudge(r.RuntimeBrief)
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				rebuildTools()
				opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_readiness_nudge", Content: nudge})
			}
		}
		if !planThenExecuteCorrectionAfterRefreshNudged &&
			r.planThenExecutePlannerPhase() &&
			planThenExecuteReadinessNudged &&
			strings.TrimSpace(result.Tool) != "submit_plan" &&
			successfulSubmitPlanResultCount(collectedResults) == 0 {
			recoveryState := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			if rejectedResult, _, rejected := lastRejectedSubmitPlanCoverageResult(collectedResults); rejected &&
				recoveryState.Mode != PlanExecutePlanningModeAnchorRefresh &&
				len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, collectedResults)) == 0 {
				planThenExecuteCorrectionAfterRefreshNudged = true
				prompt := "The bounded source refresh after the rejected `submit_plan` is complete. The next visible action must be a corrected parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not call read/search/discovery in submit-correction mode. " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejectedResult)
				refreshPlanThenExecuteTerminalSurface("submit_plan_after_refresh")
				if terminalSubmitPlanNativeTools() {
					prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, rejectedResult)
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				rebuildTools()
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				syncPlanThenExecuteRecoveryAllowedTools()
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_after_refresh", Content: prompt})
			}
		}
		// Plan-then-execute short-circuit: once the planner emits a
		// successful submit_plan call, the next model turn would just
		// retype a summary already implicit in the plan artifact.
		// Synthesize the final answer from the tool result and exit
		// the runner instead of spending another ~4s of model time.
		if answer, ok := planThenExecuteShortCircuitAnswer(r.RuntimeBrief, req, result); ok {
			recorder.activeToolCall = activeToolCall
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_short_circuit",
				Content: "submit_plan succeeded; synthesizing final answer from plan artifact instead of spending a second model turn",
			})
			return finish(answer, nil, "plan_submitted_short_circuit", false)
		}
		if !delegatedReadOnlyAnswerReady && !postChildPacketActive {
			r.attachInstructionsForTouchedPaths(&turnMessages, req, result)
		}
		childReadyBeforeResult := delegatedReadOnlyAnswerReady
		becameReadyFromDelegated := r.delegatedReadOnlyAnswerReadyAfterResult(req, result, collectedResults)
		becameReadyFromAnalysis := r.readOnlyAnalysisAnswerReadyAfterResult(req, result, collectedResults)
		if becameReadyFromDelegated || becameReadyFromAnalysis {
			justBecameReady := !delegatedReadOnlyAnswerReady
			delegatedReadOnlyAnswerReady = true
			r.RuntimeBrief.Constraints = mergeRunnerStateStrings(r.RuntimeBrief.Constraints, []string{"answer_readiness_reached"})
			if becameReadyFromDelegated {
				r.emitDelegatedReadOnlyAnswerReadyEvent(req, collectedResults)
			} else {
				r.emitReadOnlyAnswerReadyEvent(req, collectedResults)
			}
			if !delegatedReadOnlyAnswerReadyNudged {
				delegatedReadOnlyAnswerReadyNudged = true
				nudge := delegatedReadOnlyAnswerReadyNudge(r.RuntimeBrief.Objective, collectedResults, postReadinessProbeBudget)
				eventRole := "delegated_read_only_answer_ready_nudge"
				if becameReadyFromAnalysis {
					nudge = readOnlyAnswerReadyNudge(r.RuntimeBrief, collectedResults, postReadinessProbeBudget)
					eventRole = "read_only_answer_ready_nudge"
				}
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				r.emitEvent(RunEvent{Role: eventRole, Content: nudge})
			}
			// Rebuild tools so readiness state can affect explicit
			// mutation/source-refresh contracts. Read-only readiness is
			// advisory and must not hide or reshape discovery tools.
			if justBecameReady {
				rebuildTools()
			}
			if strings.HasPrefix(r.agentName(), "child:") &&
				postReadinessToolContractForBrief(r.RuntimeBrief, collectedResults, delegatedReadOnlyAnswerReady, postChildPacketActive).AnswerOnly {
				if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
					return finish(answer, nil, "delegated_read_only_host_closeout", false)
				}
			}
		}
		if r.readOnlyNoMatchCloseoutApplies(req, result, collectedResults) {
			if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(collectedResults); ok {
				return finish(answer, nil, "read_only_no_match_closeout", false)
			}
			answer, err := controller.completeWithoutMoreToolsWithReason("The latest symbol probe returned no matches after source evidence was already gathered. Tools are now disabled. Answer from the gathered evidence and name any remaining uncertainty after the supported answer.")
			return finish(answer, err, "read_only_no_match_closeout", true)
		}
		// Conversation lane: once source evidence is available, the
		// generic post-readiness contract above owns further probes.
		// Keep this inspected-path note only for lanes that have not
		// entered answer readiness.
		if r.conversationEvidenceAvailableForObjective(collectedResults) && !conversationEvidenceNudged && !delegatedReadOnlyAnswerReady {
			conversationEvidenceNudged = true
			inspected := conversationInspectedPathSummary(collectedResults)
			if inspected != "" {
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "Inspected paths so far: " + inspected + ". Answer if the evidence supports the requested claim; otherwise continue only with one exact missing claim and one exact probe that would resolve it.",
				})
			}
		}

		// Conversation lane: auto-capture findings at the first context-derived
		// milestone so discoveries survive context compactions.
		if r.lanePolicy().AutoCaptureFindings() &&
			metrics.ToolCalls == convFirstMilestone && strings.TrimSpace(result.Error) == "" && r.Workspace != nil {
			if _, err := writeAutoFindingsNote(r.Workspace.Root(), r.RuntimeBrief, collectedResults); err == nil {
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The harness has captured your findings so far to .idiom/notes/auto-findings.md. This note persists across context compactions — review it if your working memory is refreshed. Continue investigating, or answer if you have enough evidence.",
				})
			} else {
				r.emitEvent(RunEvent{Role: "note_tool", Content: "Auto findings note rejected: " + err.Error()})
			}
		}
		if childReadyBeforeResult && !postChildPacketActive && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			delegatedReadOnlyPostReadyFollowups++
			// Diminishing-returns nudge stays as a soft signal; the hard
			// budget force-finish was a bandaid (per the no-bandaid
			// memory) and got removed. MaxToolSteps + doom_loop_guard
			// catch pathological loops; the completion gate enforces
			// mutation closure.
			if delegatedReadOnlyPostReadyFollowups > delegatedReadOnlyDiminishingNudgeStage {
				if nudge := delegatedReadOnlyDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, delegatedReadOnlyPostReadyFollowups); nudge != "" {
					delegatedReadOnlyDiminishingNudgeStage = delegatedReadOnlyPostReadyFollowups
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					role := "read_only_diminishing_returns_nudge"
					if strings.HasPrefix(r.agentName(), "child:") {
						role = "delegated_read_only_diminishing_returns_nudge"
					}
					r.emitEvent(RunEvent{Role: role, Content: nudge})
				}
			}
		}
		if postChildPacketActive && strings.TrimSpace(req.Tool) != "task" && isReadClassTool(req) && strings.TrimSpace(result.Error) == "" {
			postChildFollowupReads++
			// Same removal: keep the soft nudge, drop the hard budget
			// force-finish. The previous mutation-parent exemption is
			// subsumed since no force-finish fires anymore.
			if postChildFollowupReads > postChildDiminishingNudgeStage {
				if nudge := postChildDiminishingReturnsNudge(r.RuntimeBrief.Objective, collectedResults, postChildFollowupReads); nudge != "" {
					postChildDiminishingNudgeStage = postChildFollowupReads
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_diminishing_returns_nudge", Content: nudge})
				}
			}
		}
		if strings.TrimSpace(result.Error) != "" {
			// Track same-stem tool failures so we can inject a recovery
			// nudge before burning the whole budget on hallucinated paths
			// — and, if the nudge already fired once this turn and the
			// streak starts up again, terminate the turn rather than let
			// the model keep churning (observed in live runs: 60+
			// tool calls on a single doc-edit task with only one nudge
			// fired and no escape).
			stem := toolFailurePathStem(req)
			if executionState.noteToolFailure(req, result) >= 3 {
				if !executionState.softFailureNudgeUsed {
					if nudge := buildRejectionRecoveryNudge(req, collectedResults, r.Workspace, r.RuntimeBrief, r.agentName(), delegatedReadOnlyAnswerReady, postChildPacketActive); nudge != "" {
						executionState.softFailureNudgeUsed = true
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						r.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})
						executionState.consecutiveToolFailures = 0
					}
				} else {
					// Soft nudge already fired. Another 3-failure streak
					// on the same stem means the model isn't absorbing
					// the recovery hint. Terminate with what we have.
					answer, err := controller.completeWithoutMoreToolsWithReason(fmt.Sprintf("Three more %s failures on %s after the recovery nudge. Tools are now disabled. Write the normal final answer from the evidence already gathered. If those failures leave one exact claim unsupported, state that uncertainty after the supported answer.", req.Tool, stem))
					return finish(answer, err, "tool_failure_streak_exhausted", true)
				}
			}
		} else {
			executionState.noteToolFailure(req, result)
		}
		if executionState.noteMissingPathListFailure(r.Workspace.IsExploreMode(), req, result) >= 2 {
			answer, err := controller.completeWithoutMoreToolsWithReason("Exploration is probing conventional paths that do not exist in this repository. Tools are now disabled. Answer from the bootstrapped evidence; if one exact claim remains unsupported, state that uncertainty after the supported answer.")
			return finish(answer, err, "missing_path_probe_budget_exhausted", true)
		}
		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
			return finish(answer, err, "no_progress_budget_exhausted", true)
		}
	}

	answer, err = controller.completeWithoutMoreToolsWithReason(maxToolStepsExhaustedReason())
	return finish(answer, err, "tool_step_budget_exhausted", true)
}

func (r *Runner) planExecutePlanningTransportFromOptions(opts llama.ChatOptions, supportsNativeTools bool, terminalOnly bool) PlanExecutePlanningTransport {
	return PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, terminalOnly).PlanningTransport()
}

func implementationScopedReadFileRequestInExplicitScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) bool {
	if !implementationScopedEditSurfaceActive(brief, results) {
		return false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	return readRequestTargetsRequiredEditPath(req, implementationScopedExplicitReadScope(brief))
}

func (r *Runner) preflightScopedImplementationLargeWriteFileReplacement(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}
	if strings.TrimSpace(req.Tool) != "write_file" {
		return verificationInterceptResult{}
	}
	if briefIsDocDeliverableLane(r.RuntimeBrief) {
		return verificationInterceptResult{}
	}
	if !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) {
		return verificationInterceptResult{}
	}
	relPath := scopedImplementationRequiredPath(req.Path, r.RuntimeBrief.RequiredEditPaths)
	if relPath == "" || writePathIsNonSource(relPath) {
		return verificationInterceptResult{}
	}
	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return verificationInterceptResult{}
	}
	lineCount := 0
	byteCount := info.Size()
	if byteCount <= scopedImplementationLargeReadByteThreshold {
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return verificationInterceptResult{}
		}
		lineCount = countTextLines(data)
		byteCount = int64(len(data))
	}
	if lineCount <= scopedImplementationLargeReadLineThreshold && byteCount <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}
	detail := fmt.Sprintf("%d bytes", byteCount)
	if lineCount > 0 {
		detail = fmt.Sprintf("%d lines, %d bytes", lineCount, byteCount)
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "write_file",
			Summary: fmt.Sprintf("Rejected large existing write_file replacement for %s", relPath),
			Error: fmt.Sprintf(
				"write_file would replace existing large scoped source path %s (%s). Use apply_patch or edit_file with old_text/new_text or start_line/end_line/content for a bounded edit. write_file is reserved here for new files, small files, and non-source deliverables.",
				relPath,
				detail,
			),
		},
		Reason: fmt.Sprintf("rejected range-less full-file replacement on large scoped source path %s", relPath),
	}
}

func scopedImplementationRequiredPath(path string, required []string) string {
	cleaned := cleanRelativeRunnerPath(path)
	if cleaned == "" {
		return ""
	}
	for _, candidate := range required {
		if cleanRelativeRunnerPath(candidate) == cleaned {
			return cleaned
		}
	}
	return ""
}

func cleanRelativeRunnerPath(path string) string {
	normalized := normalizeRunnerPath(path)
	if normalized == "" || filepath.IsAbs(normalized) {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(normalized))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func countTextLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func (r *Runner) preflightScopedImplementationBoundedSourceRead(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || r.Workspace == nil {
		return verificationInterceptResult{}
	}
	if !implementationScopedReadFileRequestInExplicitScope(r.RuntimeBrief, results, req) {
		return verificationInterceptResult{}
	}
	if req.StartLine > 0 || req.EndLine > 0 {
		return verificationInterceptResult{}
	}
	relPath := normalizeRunnerPath(req.Path)
	if relPath == "" || strings.HasPrefix(relPath, "../") || relPath == ".." || filepath.IsAbs(relPath) {
		return verificationInterceptResult{}
	}
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || strings.HasPrefix(relPath, "../") || relPath == ".." {
		return verificationInterceptResult{}
	}
	absPath := filepath.Join(r.Workspace.Root(), filepath.FromSlash(relPath))
	data, err := os.ReadFile(absPath)
	if err != nil {
		return verificationInterceptResult{}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) <= scopedImplementationLargeReadLineThreshold && len(data) <= scopedImplementationLargeReadByteThreshold {
		return verificationInterceptResult{}
	}
	start, end, selector := scopedImplementationBoundedReadWindow(r.RuntimeBrief, relPath, lines)
	output := scopedImplementationBoundedReadOutput(r.RuntimeBrief, relPath, lines, start, end, selector)
	r.Workspace.MarkPathObserved(relPath)
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool: "read_file",
			Summary: fmt.Sprintf(
				"Read %s lines %d-%d (bounded large required-path source; file has %d lines total; full range-less read omitted)",
				relPath, start, end, len(lines),
			),
			Output: output,
		},
		Reason: fmt.Sprintf("bounded range-less read_file on large scoped path %s to lines %d-%d of %d", relPath, start, end, len(lines)),
	}
}

func scopedImplementationBoundedReadWindow(brief workerruntime.WorkerRuntimeBrief, path string, lines []string) (int, int, string) {
	total := len(lines)
	if total <= 0 {
		return 1, 1, "empty file fallback"
	}
	terms := scopedImplementationSourceSelectionTerms(brief, path)
	if line := firstLineMatchingAnyTerm(lines, terms); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first task/path term match near line %d", line)
	}
	if line := firstDeclarationLine(lines); line > 0 {
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		return start, end, fmt.Sprintf("first declaration fallback near line %d", line)
	}
	end := scopedImplementationBoundedReadWindowLines
	if end > total {
		end = total
	}
	return 1, end, "top-of-file fallback"
}

func scopedImplementationBoundedReadOutput(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, start int, end int, selector string) string {
	total := len(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "Bounded source packet for %s\n", path)
	fmt.Fprintf(&b, "- file_lines: %d\n", total)
	fmt.Fprintf(&b, "- returned_lines: %d-%d\n", start, end)
	fmt.Fprintf(&b, "- selector: %s\n", selector)
	fmt.Fprintf(&b, "- reason: range-less read_file on a large scoped required/evidence path was bounded to preserve context.\n")
	fmt.Fprintf(&b, "- next: request a smaller read_file range or a code_navigate read_symbol for the exact edit-safety fact you still need.\n")
	for _, suggestion := range scopedImplementationSuggestedReadRanges(brief, path, lines, start, end) {
		fmt.Fprintf(&b, "- suggested_next_read: %s\n", suggestion)
	}
	b.WriteString("\nSource lines:\n")
	for i := start - 1; i < end && i < len(lines); i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, truncateScopedImplementationBoundedReadLine(lines[i]))
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncateScopedImplementationBoundedReadLine(line string) string {
	if len(line) <= scopedImplementationBoundedReadLineCharCap {
		return line
	}
	return fmt.Sprintf("%s... [truncated %d chars]", line[:scopedImplementationBoundedReadLineCharCap], len(line)-scopedImplementationBoundedReadLineCharCap)
}

func scopedImplementationSuggestedReadRanges(brief workerruntime.WorkerRuntimeBrief, path string, lines []string, returnedStart int, returnedEnd int) []string {
	terms := scopedImplementationSourceSelectionTerms(brief, path)
	total := len(lines)
	seen := map[string]struct{}{
		fmt.Sprintf("%d-%d", returnedStart, returnedEnd): {},
	}
	var suggestions []string
	add := func(line int, why string) {
		if line <= 0 || line > total || len(suggestions) >= 3 {
			return
		}
		start, end := centeredLineWindow(line, total, scopedImplementationBoundedReadWindowLines)
		key := fmt.Sprintf("%d-%d", start, end)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		suggestions = append(suggestions, fmt.Sprintf(
			`{"tool":"read_file","path":%q,"start_line":%d,"end_line":%d,"named_gap":%q,"why_not_answering":"need bounded current source before the edit"}`,
			path, start, end, why,
		))
	}
	for _, line := range allDeclarationLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant declaration in large scoped file")
	}
	for _, line := range allLinesMatchingAnyTerm(lines, terms, 6) {
		add(line, "inspect task-relevant source range in large scoped file")
	}
	if len(suggestions) == 0 {
		add(1, "inspect file header/imports before selecting a narrower symbol or range")
	}
	return suggestions
}

func scopedImplementationSourceSelectionTerms(brief workerruntime.WorkerRuntimeBrief, path string) []string {
	seen := map[string]struct{}{}
	var terms []string
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		term = strings.Trim(term, "`'\".,:;()[]{}")
		if len(term) < 4 || scopedImplementationSourceSelectionStopWord(term) {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	text := strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredNext, " "),
		strings.Join(brief.SearchPolicy, " "),
		strings.Join(brief.Constraints, " "),
	}, " ")
	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 && !strings.Contains(match[1], "/") {
			add(match[1])
		}
	}
	for _, token := range codeShapedTokenPattern.FindAllString(text, -1) {
		add(token)
	}
	for _, token := range codeShapedSnakeCasePattern.FindAllString(text, -1) {
		add(token)
		for _, part := range strings.Split(token, "_") {
			add(part)
		}
	}
	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	add(stem)
	for _, part := range strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		add(part)
	}
	add(filepath.Base(path))
	if len(terms) > 16 {
		return terms[:16]
	}
	return terms
}

func scopedImplementationSourceSelectionStopWord(term string) bool {
	switch term {
	case "this", "that", "with", "from", "into", "file", "files", "path", "paths",
		"source", "read", "edit", "edits", "write", "writes", "scope", "scoped",
		"required", "implementation", "implement", "contract", "evidence", "exact",
		"task", "test", "tests", "need", "needs", "before", "after", "current", "main":
		return true
	default:
		return false
	}
}

func firstLineMatchingAnyTerm(lines []string, terms []string) int {
	for _, line := range allLinesMatchingAnyTerm(lines, terms, 1) {
		return line
	}
	return 0
}

func allLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}
	var out []int
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}
		if len(out) >= max {
			return out
		}
	}
	return out
}

func firstDeclarationLine(lines []string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ") {
			return i + 1
		}
	}
	return 0
}

func allDeclarationLinesMatchingAnyTerm(lines []string, terms []string, max int) []int {
	if max <= 0 || len(terms) == 0 {
		return nil
	}
	var out []int
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isDecl := strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "var ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ")
		if !isDecl {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				out = append(out, i+1)
				break
			}
		}
		if len(out) >= max {
			return out
		}
	}
	return out
}

func centeredLineWindow(line int, total int, size int) (int, int) {
	if total <= 0 {
		return 1, 1
	}
	if size <= 0 {
		size = scopedImplementationBoundedReadWindowLines
	}
	if size > total {
		size = total
	}
	start := line - size/3
	if start < 1 {
		start = 1
	}
	end := start + size - 1
	if end > total {
		end = total
		start = end - size + 1
		if start < 1 {
			start = 1
		}
	}
	return start, end
}

// maxToolStepsExhaustedReason mirrors OpenCode's max-steps.txt: when the
// per-turn tool-step budget is spent, tools are disabled and the model must
// synthesize a text-only reply now from the evidence already gathered. The
// wording is deliberately blunt about both constraints — no more tool calls,
// and no empty responses — to steer small local models away from the
// "keep probing" loop that produced the empty-answer failure in
// logs/conversations/20260421-053044.479.jsonl.
func maxToolStepsExhaustedReason() string {
	return "Maximum tool steps for this turn have been reached. Tools are now disabled. Respond with a concise, direct, text-only final answer synthesized from the evidence already gathered. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action. Do not leave the response blank."
}

func replayRepeatedToolRequest(req workspace.ToolRequest, source workspace.ToolResult) workspace.ToolResult {
	summary := strings.TrimSpace(source.Summary)
	if summary == "" {
		if strings.TrimSpace(source.Error) != "" {
			summary = "Repeated tool request replayed the same blocker"
		} else {
			summary = "Repeated tool request reused prior evidence"
		}
	} else if strings.TrimSpace(source.Error) != "" {
		summary += " (replayed same blocker)"
	} else {
		summary += " (replayed prior evidence)"
	}
	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  source.Output,
		Error:   source.Error,
	}
}

func readOnlyReplayedEvidenceCloseoutApplies(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest, replayed workspace.ToolResult) bool {
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") ||
		strings.HasPrefix(strings.TrimSpace(agent), "child_write:") ||
		runnerBriefRequiresMutation(brief) ||
		runnerVerificationRequired(brief) ||
		!workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		!isReadClassTool(req) ||
		strings.TrimSpace(replayed.Error) != "" {
		return false
	}
	directAnswerExactSearchReplay := readOnlyDirectAnswerExactSearchReplayCloseoutCandidate(brief, req)
	if LanePolicyForBrief(brief).ShouldSkipRecoveryNudge() && !directAnswerExactSearchReplay {
		return false
	}
	if !readOnlyExplanationGateApplies(brief) && !directAnswerExactSearchReplay {
		return false
	}
	closure := readOnlyClosureDecisionForResults(brief, results)
	if directAnswerExactSearchReplay {
		_, ok := closure.HostOwnedAnswer(agent)
		return ok
	}
	return closure.Answerable()
}

func readOnlyDirectAnswerExactSearchReplayCloseoutCandidate(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) bool {
	return readOnlyDirectAnswerLane(brief) && shouldRejectExactReplayBeforeLoopGate(req)
}

func readOnlyReplayedEvidenceCloseoutPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	label := "read-only analysis"
	switch {
	case workerruntime.RuntimeContractIsReviewFindings(brief.Contract):
		label = "read-only review"
	case workerruntime.RuntimeContractIsArchitecture(brief.Contract):
		label = "read-only architecture analysis"
	}
	return fmt.Sprintf("The latest %s source probe replayed evidence that was already gathered after the evidence gate was satisfied. Tools are now disabled. Synthesize the final answer from the inspected evidence: state the bounded finding or no-finding, cite exact files or symbols, include confidence and uncertainty, and do not request more tools.", label)
}

func repeatedDiagnosticLogReadFileBackfill(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return workspace.ToolRequest{}, "", false
	}
	windows := diagnosticLogReadFileWindows(path, results)
	nextStart, nextEnd, ok := diagnosticLogNextUnreadPreviousWindow(windows)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	rewritten := req
	rewritten.StartLine = nextStart
	rewritten.EndLine = nextEnd
	raw := marshalToolRequestRaw(rewritten)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}
	return rewritten, raw, true
}

type diagnosticLogReadWindow struct {
	start int
	end   int
	total int
}

func diagnosticLogReadFileWindows(path string, results []workspace.ToolResult) []diagnosticLogReadWindow {
	path = normalizeRunnerPath(path)
	if path == "" {
		return nil
	}
	var windows []diagnosticLogReadWindow
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if !runnerToolResultReadPathMatches(result, path) {
			continue
		}
		start, end, total, ok := readFileSummaryLineWindow(result.Summary)
		if !ok || start <= 0 || end < start || total <= 0 {
			continue
		}
		windows = append(windows, diagnosticLogReadWindow{start: start, end: end, total: total})
	}
	return windows
}

func diagnosticLogUnboundedReadCoverageRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}
	if !diagnosticLogWindowsCoverContiguousLog(diagnosticLogReadFileWindows(path, results)) {
		return "", "", false
	}
	return "Diagnostic log read coverage exhausted",
		"this diagnostic log has already been read back to line 1 and includes the current tail window. Do not replay the unbounded read. Answer from the gathered log evidence, or request one different bounded `start_line`/`end_line` window only if you can name the exact missing event range.",
		true
}

func diagnosticLogNextUnreadPreviousWindow(windows []diagnosticLogReadWindow) (int, int, bool) {
	merged, total := mergeDiagnosticLogReadWindows(windows)
	if len(merged) == 0 || total <= 0 {
		return 0, 0, false
	}
	tailIndex := -1
	for i := len(merged) - 1; i >= 0; i-- {
		if merged[i].end >= total {
			tailIndex = i
			break
		}
	}
	if tailIndex < 0 {
		return 0, 0, false
	}
	tail := merged[tailIndex]
	gapEnd := tail.start - 1
	if gapEnd < 1 {
		return 0, 0, false
	}
	windowSize := tail.end - tail.start + 1
	if windowSize <= 0 {
		return 0, 0, false
	}
	lowerBound := 1
	if tailIndex > 0 {
		lowerBound = merged[tailIndex-1].end + 1
	}
	gapStart := gapEnd - windowSize + 1
	if gapStart < lowerBound {
		gapStart = lowerBound
	}
	if gapStart < 1 {
		gapStart = 1
	}
	if gapStart > gapEnd {
		return 0, 0, false
	}
	return gapStart, gapEnd, true
}

func diagnosticLogWindowsCoverContiguousLog(windows []diagnosticLogReadWindow) bool {
	merged, total := mergeDiagnosticLogReadWindows(windows)
	if total <= 0 {
		return false
	}
	for _, window := range merged {
		if window.start <= 1 && window.end >= total {
			return true
		}
	}
	return false
}

func mergeDiagnosticLogReadWindows(windows []diagnosticLogReadWindow) ([]diagnosticLogReadWindow, int) {
	if len(windows) == 0 {
		return nil, 0
	}
	total := 0
	filtered := make([]diagnosticLogReadWindow, 0, len(windows))
	for _, window := range windows {
		if window.start <= 0 || window.end < window.start || window.total <= 0 {
			continue
		}
		if window.total > total {
			total = window.total
		}
		filtered = append(filtered, window)
	}
	if len(filtered) == 0 || total <= 0 {
		return nil, total
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].start == filtered[j].start {
			return filtered[i].end < filtered[j].end
		}
		return filtered[i].start < filtered[j].start
	})
	merged := make([]diagnosticLogReadWindow, 0, len(filtered))
	for _, window := range filtered {
		if window.end > total {
			window.end = total
		}
		if len(merged) == 0 || window.start > merged[len(merged)-1].end+1 {
			merged = append(merged, diagnosticLogReadWindow{start: window.start, end: window.end, total: total})
			continue
		}
		if window.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = window.end
		}
		merged[len(merged)-1].total = total
	}
	return merged, total
}

func runnerToolResultReadPathMatches(result workspace.ToolResult, path string) bool {
	for _, readPath := range readPathsFromToolResult(result) {
		if normalizeRunnerPath(readPath) == path {
			return true
		}
	}
	return false
}

func readFileSummaryLineWindow(summary string) (int, int, int, bool) {
	match := readFileSummaryLineWindowPattern.FindStringSubmatch(summary)
	if len(match) != 3 {
		return 0, 0, 0, false
	}
	totalMatch := readFileSummaryTotalLinesPattern.FindStringSubmatch(summary)
	if len(totalMatch) != 3 {
		return 0, 0, 0, false
	}
	start, errStart := strconv.Atoi(match[1])
	end, errEnd := strconv.Atoi(match[2])
	totalText := totalMatch[1]
	if totalText == "" {
		totalText = totalMatch[2]
	}
	total, errTotal := strconv.Atoi(totalText)
	if errStart != nil || errEnd != nil || errTotal != nil {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

func repeatedDiagnosticLogReadFileExactRejection(req workspace.ToolRequest, last workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}
	if strings.TrimSpace(last.Tool) != "read_file" || strings.TrimSpace(last.Error) != "" {
		return "", "", false
	}
	for _, readPath := range readPathsFromToolResult(last) {
		if normalizeRunnerPath(readPath) != path {
			continue
		}
		return "Repeated diagnostic log read rejected",
			"this exact diagnostic log read already returned evidence. Do not request the same log window again. If a missing fact remains, request a different bounded `start_line`/`end_line` window tied to that missing timestamp, event, or stop reason; inspect a named source path only if the log evidence points to it; otherwise answer from the gathered evidence.",
			true
	}
	return "", "", false
}

func repeatedDiagnosticLogReadFilePreviouslyReadRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if !runnerToolResultReadPathMatches(result, path) {
			continue
		}
		start, end, _, ok := readFileSummaryLineWindow(result.Summary)
		if !ok || start != req.StartLine || end != req.EndLine {
			continue
		}
		return "Repeated diagnostic log window rejected",
			"this bounded diagnostic log window already returned evidence earlier in the run. Do not request the same `start_line`/`end_line` window again. If a missing fact remains, request a different bounded window tied to the exact missing event, inspect a named source path pointed to by the log, or answer from the gathered evidence.",
			true
	}
	return "", "", false
}

func allowRecoverablePostReadinessRepairExecution(req workspace.ToolRequest, lastToolResult workspace.ToolResult) bool {
	if postReadinessGap(req) == "" {
		return false
	}
	if isRecoverablePostReadinessGapRejection(lastToolResult.Summary) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(lastToolResult.Summary + " " + lastToolResult.Error))
	return strings.Contains(lower, "missing named gap") || strings.Contains(lower, "include `named_gap`")
}

func (r *Runner) emitThinkingTrace(step int, reason string, message llama.ChatMessage, opts llama.ChatOptions, preservedInHistory bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	if opts.EnableThinking == nil && !opts.PreserveThinking && strings.TrimSpace(message.ReasoningContent) == "" {
		return
	}
	trace := runnerThinkingTrace{
		Lane:               r.agentName(),
		Step:               step,
		Reason:             strings.TrimSpace(reason),
		EnableThinking:     thinkingOptionForLog(opts.EnableThinking),
		PreserveThinking:   opts.PreserveThinking,
		ContentChars:       len(message.Content),
		ReasoningChars:     len(message.ReasoningContent),
		PreservedInHistory: preservedInHistory && strings.TrimSpace(message.ReasoningContent) != "",
	}
	if strings.TrimSpace(reason) == "completion_gate_reply_preserved" {
		trace.ContentPreview = truncateRunnerStateText(strings.TrimSpace(message.Content), 500)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "thinking_trace", Content: string(data)})
}

// stripOrphanedToolCalls returns a copy of messages with any assistant
// messages removed that have tool_calls but no matching role:"tool"
// responses. When the budget gate or post-readiness nudge continues
// the loop without executing a pending tool, the orphaned tool_calls
// must not reach the model — strict backends (DeepSeek) reject the
// request with "insufficient tool messages following tool_calls".
func stripOrphanedToolCalls(messages []llama.ChatMessage) []llama.ChatMessage {
	// Build lookup: tool_call_id -> has matching tool response
	hasToolResponse := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			hasToolResponse[msg.ToolCallID] = true
		}
	}
	out := make([]llama.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			orphaned := false
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" && !hasToolResponse[tc.ID] {
					orphaned = true
					break
				}
			}
			if orphaned {
				continue // drop this assistant message entirely
			}
		}
		out = append(out, msg)
	}
	return out
}

func chatMessagesForOptions(messages []llama.ChatMessage, opts llama.ChatOptions) []llama.ChatMessage {
	if opts.PreserveThinking {
		return messages
	}
	out := make([]llama.ChatMessage, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ReasoningContent = ""
	}
	return out
}

func assistantHistoryMessageForOptions(message llama.ChatMessage, opts llama.ChatOptions) llama.ChatMessage {
	if !opts.PreserveThinking {
		message.ReasoningContent = ""
	}
	return message
}

func thinkingOptionForLog(enableThinking *bool) string {
	if enableThinking == nil {
		return "auto"
	}
	if *enableThinking {
		return "on"
	}
	return "off"
}

func (r *Runner) emitNextActionState(step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	brief := r.visibleBriefForToolSurface(r.RuntimeBrief, results)
	state, phaseContract := buildRunnerNextActionStateWithAllowedToolsAndContract(r.agentName(), step, reason, req, result, results, declarationMovesForbidden, brief, r.currentAllowedTools)
	if r.activeAutonomy != nil {
		r.activeAutonomy.emitTurnPhaseSurfaceSummary(turnPhaseSurfaceSummaryFromNextActionState(state, phaseContract), phaseContract)
		if strings.TrimSpace(result.Error) != "" {
			card := BuildRecoveryCard(RecoveryCardInput{
				PhaseContract:       phaseContract,
				CurrentAllowedTools: effectiveRecoveryCardAllowedTools(r.currentAllowedTools, brief, results, phaseContract),
				RejectedRequest:     req,
				RejectedResult:      result,
				EvidenceRefs:        state.EvidencePaths,
			})
			r.activeAutonomy.emitTurnPhaseSurfaceSummary(card.TurnPhaseSurfaceSummary(phaseContract), phaseContract)
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "next_action_state", Content: string(data)})
}

func (r *Runner) runtimeBriefWithLiveState(policy RunPolicy, messages []llama.ChatMessage, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	if r == nil {
		return workerruntime.WorkerRuntimeBrief{}
	}
	brief := r.RuntimeBrief
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}
	if strings.TrimSpace(brief.Objective) == "" {
		brief.Objective = latestUserObjective(messages)
	}
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		brief.TargetPacket = mergeRunnerStateStrings(brief.TargetPacket, reviewRepairPhaseEvidencePacket(brief.Objective))
	}
	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps:       policy.MaxToolSteps,
			MaxModelCalls:      policy.MaxModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		}
	}
	liveEvidencePaths := runnerEvidencePathsForBrief(brief, results, 8)
	if readOnlyDirectAnswerLane(brief) {
		if len(liveEvidencePaths) > 0 {
			brief.EvidencePaths = liveEvidencePaths
		}
	} else {
		brief.EvidencePaths = mergeRunnerStateStrings(brief.EvidencePaths, liveEvidencePaths)
	}
	brief.ChangedPaths = mergeRunnerStateStrings(brief.ChangedPaths, mapKeys(changedPathsFromToolResults(results)))
	brief = runnerBriefWithPhaseHints(brief, results)
	brief.Readiness = runnerReadinessState(r.agentName(), brief, results)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerReadinessState(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *workerruntime.ReadinessState {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	readiness := &workerruntime.ReadinessState{}
	readOnlyBrief := brief
	if readOnlyDirectAnswerLane(brief) {
		readOnlyBrief = readOnlyDirectAnswerAcceptanceLedgerBrief(brief)
	}
	readOnlyLedger := readOnlyAnswerabilityForResults(readOnlyBrief, results)
	addClosed := func(values ...string) {
		readiness.ClosedClaims = mergeRunnerStateStrings(readiness.ClosedClaims, values)
	}
	addOpen := func(values ...string) {
		readiness.OpenObligations = mergeRunnerStateStrings(readiness.OpenObligations, values)
	}
	addSource := func(values ...string) {
		readiness.EvidenceSources = mergeRunnerStateStrings(readiness.EvidenceSources, values)
	}

	if len(brief.TargetRefs) > 0 {
		addSource("prompt_target_refs")
	}
	if len(brief.TargetPacket) > 0 {
		addSource("context_packet")
		addClosed("context_packet_available")
	}
	if len(brief.EvidencePaths) > 0 {
		addSource("evidence_paths")
	}
	if len(brief.RequiredEditPaths) > 0 {
		addSource("required_edit_paths")
		addClosed("required_edit_paths_define_write_scope")
	}
	if len(brief.RequiredEditSnippets) > 0 {
		addSource("required_edit_snippets")
		addClosed("required_edit_snippets_available")
	}
	if len(results) > 0 {
		addSource("tool_results")
	}
	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		addSource("discovery_packet")
		addClosed("composite_discovery_packet_available")
	}
	if readOnlyLedger.HasKind(readOnlyEvidenceContextPacket) {
		addSource("context_packet")
		addClosed("context_packet_localized_candidates")
	}
	if readOnlyLedger.HasKind(readOnlyEvidenceSearchHit) {
		addSource("search_hits")
		addClosed("search_hits_localized_candidates")
	}
	if readOnlyLedger.HasKind(readOnlyEvidenceOutline) {
		addSource("source_outline")
		addClosed("source_outline_localized_candidates")
	}
	if len(readOnlyLedger.RelevantSourceBodyPaths) > 0 {
		addSource("source_body")
		addClosed("relevant_source_body_evidence")
	}
	if readOnlyLedger.DiagnosticEvidence {
		addSource("diagnostic_evidence")
		addClosed("diagnostic_evidence_available")
	}
	if delegatedReadOnlyClosureReady(agent, brief, results) {
		readiness.ReadyToAnswer = true
		addClosed("answer_readiness_reached")
		readiness.FurtherReadsRequire = "one exact missing claim, one exact probe, and why the current evidence would be materially incomplete"
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		readiness.ReadyToAnswer = true
		addClosed("child_packet_available")
		readiness.FurtherReadsRequire = "one exact packet-anchored missing claim and one exact probe"
	}

	changed := changedPathsFromToolResults(results)
	if len(changed) > 0 {
		addSource("changed_paths")
		addClosed("workspace_changes_present")
	}
	if hasSuccessfulVerificationResult(results) {
		addSource("verification_result")
		addClosed("verification_result_observed")
	}
	if sourceEditsAreSemanticallyVerified(brief, results) {
		addClosed("source_aware_verification_after_source_edit")
	}

	if runnerBriefRequiresMutation(brief) {
		if planThenExecutePlannerBrief(brief) && len(brief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
			addSource("required_source_evidence_contract")
			if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6); pending != "" {
				addOpen("pending_required_source_anchors: " + pending)
				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else {
				addClosed("required_source_anchors_ready")
			}
		} else if len(brief.RequiredEditPaths) > 0 {
			addSource("required_source_evidence_contract")
			if implementationRequiredPathAnchorsReady(brief, results) {
				addClosed("required_source_anchors_ready")
			} else if len(results) > 0 {
				if pending := planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 6); pending != "" {
					addOpen("pending_required_source_anchors: " + pending)
				}
			}
		}
		missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)
		switch {
		case len(brief.RequiredEditPaths) > 0 && len(changed) == 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true
			addOpen("required_edit_paths_not_changed")
			if planThenExecutePlannerBrief(brief) && planThenExecutePendingRequiredSourceEvidenceSummary(brief, results, 1) != "" {
				readiness.FurtherReadsRequire = "anchor-ready source evidence for pending required_edit_paths before submit_plan"
			} else if exactAnchorPreWriteSurfaceActive(brief, results) {
				readiness.FurtherReadsRequire = "no further reads before the first exact-anchor write; edit an in-scope required path or report <<QUESTION>> with the stale or missing exact anchor"
			} else {
				readiness.FurtherReadsRequire = "only semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact; otherwise edit an in-scope required path or report <<QUESTION>> with the exact path-specific blocker"
			}
		case len(missing) > 0:
			readiness.ReadyToAnswer = false
			readiness.ReadyToWrite = true
			addOpen("required_edit_paths_remaining: " + strings.Join(missing, ", "))
			readiness.FurtherReadsRequire = "resolve each remaining required path with a write, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> naming the exact blocker"
		case len(brief.RequiredEditPaths) > 0:
			addClosed("required_edit_paths_changed")
		case len(changed) == 0 && targetPacketSupportsWrite(brief):
			readiness.ReadyToWrite = true
			addOpen("workspace_change_not_attempted")
			readiness.FurtherReadsRequire = "one exact edit-safety gap grounded in the context packet"
		}

		switch {
		case hasPendingVerificationAfterWrites(results, brief):
			readiness.ReadyToAnswer = false
			if plannerOwnsVerification(brief) {
				if sourceVerificationGateApplies(brief, results) {
					addOpen("planner_owned_source_lane_sanity_pending")
				} else {
					addOpen("planner_owned_verification_status_pending")
				}
			} else {
				readiness.ReadyToVerify = true
				if sourceVerificationGateApplies(brief, results) {
					addOpen("source_aware_verification_pending")
				} else {
					addOpen("verification_pending_after_write")
				}
			}
		case sourceEditsNeedSemanticVerification(brief, results):
			if plannerOwnsVerification(brief) {
				readiness.ReadyToAnswer = false
				readiness.ReadyToVerify = true
				addOpen("planner_owned_source_lane_sanity_pending")
				readiness.FurtherReadsRequire = "compiler-backed source-sanity or language diagnostics for the changed source path, or a concrete lane-sanity blocker if source sanity is unavailable"
			} else {
				readiness.ReadyToAnswer = false
				readiness.ReadyToVerify = true
				addOpen("source_aware_verification_pending")
			}
		case len(changed) > 0 && len(missing) == 0:
			readiness.ReadyToAnswer = true
		}
	} else if runnerVerificationRequired(brief) && !hasSuccessfulVerificationResult(results) {
		readiness.ReadyToAnswer = false
		readiness.ReadyToVerify = true
		addOpen("verification_result_missing")
	} else if readOnlyDirectAnswerLane(brief) && readOnlyLedger.Answerable() {
		readiness.ReadyToAnswer = true
		addClosed("read_only_evidence_ready")
		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if readOnlyDirectAnswerLane(brief) {
		if len(readOnlyLedger.CandidatePaths) > 0 {
			addOpen("read_only_source_body_evidence_pending")
			readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
		}
	} else if readOnlyAnalysisReadyFromEvidence(results) {
		readiness.ReadyToAnswer = true
		addClosed("read_only_evidence_ready")
		readiness.FurtherReadsRequire = "one exact missing claim and one exact bounded probe"
	} else if len(readOnlyLedger.CandidatePaths) > 0 {
		addOpen("read_only_source_body_evidence_pending")
		readiness.FurtherReadsRequire = "one source-body read from a localized candidate, or one exact missing claim and bounded probe"
	}

	return workerruntime.NormalizeReadinessState(readiness)
}

func targetPacketSupportsWrite(brief workerruntime.WorkerRuntimeBrief) bool {
	return len(brief.TargetPacket) > 0 &&
		(len(brief.TargetRefs) > 0 ||
			len(brief.EvidencePaths) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0)
}

func readOnlyAnalysisReadyFromEvidence(results []workspace.ToolResult) bool {
	if !readOnlyClosureDecisionForResults(workerruntime.WorkerRuntimeBrief{}, results).Answerable() {
		return false
	}
	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		return readOnlyAnalysisExactProbeCount(results) >= 1 && len(promptEvidencePathsFromToolResults(results, 6)) >= 2
	}
	return readOnlyAnalysisExactProbeCount(results) >= 2 && len(promptEvidencePathsFromToolResults(results, 6)) >= 1
}

func runnerBriefWithPhaseHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	brief = runnerBriefWithRequiredEditPathHints(brief, results)
	switch phase := debugLogPhase(brief, results); phase {
	case "latest_log_selection":
		brief.Constraints = mergeRunnerStateStrings([]string{"latest_log_selection_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"select the newest real log artifact before inspecting source files",
			"prefer log directories and log extensions over source files whose names merely mention logging",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source-code inspection before selecting a concrete log artifact",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer newest actual log artifacts over source files with log-like names",
		}, brief.SearchPolicy)
	case "latest_log_tail":
		brief.Constraints = mergeRunnerStateStrings([]string{"latest_log_tail_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"selected-log tail evidence is required before source inspection or broader log expansion",
			"the selected log remains the evidence boundary until it supports a weird decision or an explicit missing datum",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"additional workspace discovery before selected-log tail evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"selected-log tail evidence must precede deeper search or source inspection",
		}, brief.SearchPolicy)
	case "selected_log_analysis":
		brief.Constraints = mergeRunnerStateStrings([]string{"selected_log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"answer from the selected log if the weird decision is already clear",
			"otherwise close one named missing selected-log fact before answering",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source-code inspection outside the selected log before answering",
			"another blind selected-log reread before a concrete marker",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"stay inside the selected log until the answer is supported or the gap is explicit",
		}, brief.SearchPolicy)
	case "failure_window":
		brief.Constraints = mergeRunnerStateStrings([]string{"failure_window_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"bounded failure-window evidence is required around the concrete failure hit",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"more log searching before failure-window evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"failure-window evidence must follow a concrete hit before broader log expansion",
		}, brief.SearchPolicy)
	case "log_analysis":
		brief.Constraints = mergeRunnerStateStrings([]string{"log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"answer from the gathered log evidence now unless one specific missing datum remains",
		}, brief.RequiredNext)
	}
	switch phase := evidenceFirstMutationPhase(brief, results); phase {
	case "source_evidence":
		brief.Constraints = mergeRunnerStateStrings([]string{"source_evidence_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"non-target implementation evidence is required before named-target evidence",
			"after concrete non-target evidence, one bounded named-target evidence window satisfies the target-entry contract",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"open-ended named-target read before source evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer semantic inspection of discovered implementation files before target reads",
		}, brief.SearchPolicy)
	case "target_entry":
		brief.Constraints = mergeRunnerStateStrings([]string{"first_target_read_pending"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"one bounded named-target evidence window is required before renewed source discovery",
			"the named target remains the evidence boundary for the next step",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"additional source discovery before the first target read",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"enter the named target as soon as concrete source evidence exists",
		}, brief.SearchPolicy)
	case "target_inspection":
		brief.Constraints = mergeRunnerStateStrings([]string{"target_inspection_phase"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"make the edit in the named target once the change is supported by the target evidence",
			"additional inspection is valid only for a named missing target fact and must stay scoped to named-target evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"source discovery outside the named target during target inspection",
			"open-ended target-body read during target inspection",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"prefer target-scoped follow-up over renewed source discovery; do not keep reading once the edit is clear",
		}, brief.SearchPolicy)
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerBriefWithRequiredEditPathHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return brief
	}
	if planThenExecutePlannerBrief(brief) {
		if successfulSubmitPlanResultCount(results) > 0 {
			return brief
		}
		brief = clearPlanThenExecuteRequiredSourceHints(brief)
		brief.Constraints = mergeRunnerStateStrings([]string{"plan_then_execute_submit_plan_pending"}, brief.Constraints)
		unread := planThenExecuteUnreadRequiredPaths(brief, results)
		for path := range unread {
			if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results) {
				delete(unread, path)
			}
		}
		if len(unread) > 0 {
			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			readInstruction := "read the next unread required_edit_path with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker"
			if next != "" {
				readInstruction = fmt.Sprintf("read required_edit_path %s with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker", next)
			}
			brief.RequiredNext = mergeRunnerStateStrings([]string{readInstruction}, brief.RequiredNext)
			brief.InvalidActions = mergeRunnerStateStrings([]string{
				"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
				"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
				"prose final answer while required source evidence is pending",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"new orientation read/search before the next unread required_edit_path source-body read",
			}, brief.InvalidActions)
			brief.SearchPolicy = mergeRunnerStateStrings([]string{
				"required_edit_paths plus packet evidence define the submit_plan scope",
				"progress through unread required_edit_paths one source-body read at a time before submit_plan",
				"outline/search hits orient but do not satisfy required source-body evidence",
				"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			}, brief.SearchPolicy)
			return brief
		}
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
			refreshPaths := state.RefreshPaths
			next := "the invalid-anchor required path"
			if len(refreshPaths) > 0 {
				next = refreshPaths[0]
			}
			brief.RequiredNext = mergeRunnerStateStrings([]string{
				fmt.Sprintf("refresh bounded source-body evidence for %s before corrected submit_plan; then submit a corrected plan using anchors copied from the refreshed source", next),
			}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
			brief.Constraints = mergeRunnerStateStrings([]string{"plan_then_execute_submit_anchor_refresh_required"}, brief.Constraints)
			brief.InvalidActions = mergeRunnerStateStrings([]string{
				"corrected submit_plan before refreshing the stale or missing edit anchor",
				"final answer, Decision: source-satisfied, or <<QUESTION>> before bounded anchor refresh returns",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"open-ended read/search/discovery outside the invalid-anchor source refresh",
			}, rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteSubmitReadySourceHint))
			brief.SearchPolicy = mergeRunnerStateStrings([]string{
				"stale submit_plan anchors require a bounded source-body refresh before corrected submit_plan",
				"refresh only the rejected required path named by the submit_plan guard",
				"after the refresh, use insert_after_line/content or old_text/new_text copied from fresh source; do not reuse stale anchors",
			}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))
			return brief
		}
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker",
		}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
		brief.Constraints = mergeRunnerStateStrings([]string{"plan_then_execute_submit_plan_ready"}, brief.Constraints)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
			"per-edit write tool in plan-execute planner phase",
			"shell/git/source-sanity verification in plan-execute planner phase",
			"new orientation read/search after enough anchor evidence to draft submit_plan",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"required_edit_paths plus packet evidence define the submit_plan scope",
			"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome",
		}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))
		return brief
	}
	changed := changedPathsFromToolResults(results)
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)
	if len(changed) == 0 {
		if briefHasUsableEditAnchors(brief) {
			brief.Constraints = mergeRunnerStateStrings([]string{"implementation_packet_write_ready"}, brief.Constraints)
		}
		if exactAnchorPreWriteSurfaceActive(brief, results) {
			brief.RequiredNext = mergeRunnerStateStrings([]string{
				"required-path write capability from the exact target-packet anchor",
				"answer <<QUESTION>> only if the supplied exact anchor is stale or missing",
			}, brief.RequiredNext)
			brief.InvalidActions = mergeRunnerStateStrings([]string{
				"read/search/task_ledger/shell before first exact-anchor write",
			}, brief.InvalidActions)
			brief.SearchPolicy = mergeRunnerStateStrings([]string{
				"required_edit_paths plus exact target-packet anchors establish the first write surface",
			}, brief.SearchPolicy)
		} else {
			brief.RequiredNext = mergeRunnerStateStrings([]string{
				"resolve a required path with packet evidence, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or answer <<QUESTION>> with the exact path-specific blocker",
			}, brief.RequiredNext)
			brief.InvalidActions = mergeRunnerStateStrings([]string{
				"orientation read/search after required edit paths and packet evidence without a named missing fact",
				"raw read/search outside required edit paths before resolving the required-path contract",
				"task_ledger planning before the first required-path write",
			}, brief.InvalidActions)
			brief.SearchPolicy = mergeRunnerStateStrings([]string{
				"required_edit_paths plus packet evidence establish the edit scope; prefer semantic/context/declared project discovery for named edit-safety gaps before raw source reads",
			}, brief.SearchPolicy)
		}
		return brief
	}
	if len(missing) > 0 {
		brief.Constraints = mergeRunnerStateStrings([]string{"implementation_required_paths_remaining"}, brief.Constraints)
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"resolve each remaining required path with an edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker for that path",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"final answer while required edit paths remain unchanged",
			"new orientation read/search outside remaining required edit paths before resolving the required-path contract",
		}, brief.InvalidActions)
	}
	return brief
}

func clearPlanThenExecuteRequiredSourceHints(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief.RequiredNext = rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteRequiredSourceHint)
	brief.InvalidActions = rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteRequiredSourceHint)
	brief.SearchPolicy = rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteRequiredSourceHint)
	return brief
}

func planThenExecuteRequiredSourceHint(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, marker := range []string{
		"read required_edit_path ",
		"read the next unread required_edit_path",
		"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
		"prose final answer while required source evidence is pending",
		"new orientation read/search before the next unread required_edit_path source-body read",
		"progress through unread required_edit_paths one source-body read at a time before submit_plan",
		"outline/search hits orient but do not satisfy required source-body evidence",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func planThenExecuteSubmitReadySourceHint(value string) bool {
	if planThenExecuteRequiredSourceHint(value) {
		return true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, marker := range []string{
		"for existing required paths, satisfy source-body obligations before submit_plan",
		"packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
		"source-body obligations before submit_plan",
		"use reads only for exact anchor confirmation before submit_plan",
		"raw source reads are only for exact anchor confirmation",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func rejectRunnerStateStrings(values []string, reject func(string) bool) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if reject != nil && reject(value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func runnerVisibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if exactAnchorPreWriteSurfaceActive(brief, results) {
		return exactAnchorPreWriteRuntimeBrief(brief)
	}
	if implementationPreWriteToolSurfaceActive(brief, results) {
		return implementationPreWriteRuntimeBrief(brief)
	}
	if implementationTargetSourcePending(brief, results) {
		return implementationTargetSourceRuntimeBrief(brief)
	}
	if implementationTargetWriteReadySurfaceActive(brief, results) {
		return implementationTargetWriteReadyRuntimeBrief(brief, results)
	}
	if implementationScopedEditSurfaceActive(brief, results) {
		return implementationScopedEditRuntimeBrief(brief)
	}
	if genericReadOnlyEvidencePending(brief, brief.AllowedTools, results) {
		return readOnlyEvidencePendingRuntimeBrief(brief)
	}
	return brief
}

func readOnlyEvidencePendingRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := genericReadOnlyEvidenceTools(brief.AllowedTools)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = mergeRunnerStateStrings([]string{"bounded_workspace_evidence_tool"}, brief.RequiredTools)
	brief.ReadinessGate = mergeRunnerStateStrings([]string{"bounded_workspace_evidence_before_final_answer"}, brief.ReadinessGate)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"exactly one parser-readable bounded read/search/context tool call for the user's question, or <<QUESTION>> with the exact evidence blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = mergeRunnerStateStrings([]string{
		"prose/final answer before a successful bounded evidence tool result",
		"workspace write",
		"shell/mutating command",
	}, brief.InvalidActions)
	brief.SearchPolicy = mergeRunnerStateStrings([]string{
		"use the narrowest bounded evidence tool that can answer the user's question; answer only after evidence returns",
	}, brief.SearchPolicy)
	brief.Constraints = mergeRunnerStateStrings([]string{"read_only_evidence_pending"}, brief.Constraints)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func (r *Runner) visibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	visible := runnerVisibleBriefForToolSurface(brief, results)
	if r == nil || r.Workspace == nil ||
		(!implementationPreWriteToolSurfaceActive(brief, results) &&
			!implementationTargetSourcePending(brief, results) &&
			!implementationTargetWriteReadySurfaceActive(brief, results) &&
			!implementationScopedEditSurfaceActive(brief, results)) {
		return visible
	}
	allowed := implementationPreWriteAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	if implementationTargetSourcePending(brief, results) {
		allowed = implementationTargetSourceAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	} else if implementationTargetWriteReadySurfaceActive(brief, results) {
		allowed = implementationTargetWriteReadyAllowedToolsForBrief(r.advertisedAllowedTools(r.Workspace.AllowedTools()), brief, results)
	} else if implementationScopedEditSurfaceActive(brief, results) {
		allowed = implementationScopedEditAllowedToolsWithRegistry(r.advertisedAllowedTools(r.Workspace.AllowedTools()), r.Workspace.ExternalToolsRegistry())
	}
	visible.AllowedTools = allowed
	visible.ToolManifest = nil
	if len(allowed) > 0 {
		visible.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	return workerruntime.NormalizeRuntimeBrief(visible)
}

func (r *Runner) textualToolProtocolPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	if r == nil || r.Client == nil || r.Client.SupportsNativeTools() {
		return ""
	}
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return ""
	}
	entries := textualToolProtocolEntries(brief)
	var b strings.Builder
	b.WriteString("# Textual Tool Call Protocol\n\n")
	b.WriteString("Native tool calls are unavailable for this backend. Idiom will execute tools only when your entire reply is one textual JSON tool request.\n\n")
	b.WriteString("When taking a tool action, reply with exactly one JSON object for a real allowed tool call and no prose, markdown commentary, XML tags, or second JSON object. Do not copy generic field names or schema examples as values.\n\n")
	b.WriteString("Keep structured fields structured: arrays and objects must be JSON arrays/objects, not quoted JSON strings. For `submit_plan`, `edits` must be an array and every edit object must include `path`.\n")
	b.WriteString("Use the runtime brief's `tool_manifest` for allowed tools and fields. If the runtime brief requires a tool, do not answer in prose while that tool contract is still open.\n")
	if planThenExecutePlannerBrief(brief) && toolManifestHasTool(brief.ToolManifest, "submit_plan") {
		b.WriteString("\nFor plan-execute `submit_plan`, emit one JSON object with top-level `tool`, `rationale`, and `edits`. Use only real paths and complete current/final source text from gathered evidence; never copy placeholders, ellipses, `TODO`, schema examples, or `existing code` comments.\n")
	}
	if len(entries) > 0 {
		b.WriteString("\nCurrent required/allowed examples:\n")
		for _, entry := range entries {
			b.WriteString("- ")
			tool := strings.TrimSpace(entry.Tool)
			b.WriteString(tool)
			if req := strings.TrimSpace(entry.Request); req != "" {
				b.WriteByte(' ')
				b.WriteString(req)
			}
			if purpose := strings.TrimSpace(entry.Purpose); purpose != "" {
				b.WriteString(" (")
				b.WriteString(purpose)
				b.WriteString(")")
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func toolManifestHasTool(manifest []workerruntime.ToolManifestEntry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, entry := range manifest {
		if strings.TrimSpace(entry.Tool) == name {
			return true
		}
	}
	return false
}

func runnerSanitizedToolManifest(tools []string) []workerruntime.ToolManifestEntry {
	return workerruntime.BuildToolManifest(tools)
}

func textualToolProtocolEntries(brief workerruntime.WorkerRuntimeBrief) []workerruntime.ToolManifestEntry {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return nil
	}
	suppressWholeFileWrites := scopedImplementationUsesBoundedSourceEdits(brief, brief.RequiredEditPaths)
	required := make(map[string]struct{}, len(brief.RequiredTools))
	for _, tool := range brief.RequiredTools {
		for _, concrete := range textualToolProtocolConcreteRequiredTools(tool) {
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(concrete) {
				continue
			}
			if concrete != "" {
				required[concrete] = struct{}{}
			}
		}
	}
	selected := make([]workerruntime.ToolManifestEntry, 0, len(brief.ToolManifest))
	seenEntries := make(map[string]struct{}, len(brief.ToolManifest))
	seenTools := make(map[string]struct{}, len(brief.ToolManifest))
	if len(required) > 0 {
		for _, entry := range brief.ToolManifest {
			tool := strings.TrimSpace(entry.Tool)
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
				continue
			}
			if _, ok := required[tool]; ok {
				key := textualToolProtocolEntryKey(entry)
				if _, seen := seenEntries[key]; seen {
					continue
				}
				selected = append(selected, entry)
				seenEntries[key] = struct{}{}
				seenTools[tool] = struct{}{}
			}
		}
	}
	const maxExamples = 8
	for _, entry := range brief.ToolManifest {
		if len(selected) >= maxExamples {
			break
		}
		tool := strings.TrimSpace(entry.Tool)
		if tool == "" {
			continue
		}
		if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
			continue
		}
		if _, ok := seenTools[tool]; ok {
			continue
		}
		key := textualToolProtocolEntryKey(entry)
		if _, seen := seenEntries[key]; seen {
			continue
		}
		selected = append(selected, entry)
		seenEntries[key] = struct{}{}
		seenTools[tool] = struct{}{}
	}
	return selected
}

func textualToolProtocolWholeFileWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "write_file", "append_file":
		return true
	default:
		return false
	}
}

func textualToolProtocolConcreteRequiredTools(tool string) []string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "edit_file_or_apply_patch":
		return []string{"edit_file", "write_file", "append_file", "apply_patch"}
	case "source_sanity_or_verification_shell", "verification_shell":
		return []string{"source_sanity", "shell"}
	case "bounded_workspace_evidence_tool":
		return []string{"read_file", "code_navigate", "search_content", "context_pack", "discovery_query"}
	default:
		if tool == "" {
			return nil
		}
		return []string{tool}
	}
}

func textualToolProtocolEntryKey(entry workerruntime.ToolManifestEntry) string {
	return strings.TrimSpace(entry.Tool) + "\x00" + strings.TrimSpace(entry.Request)
}

func exactAnchorPreWriteSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) && !planThenExecuteExactAnchorPlanReady(brief) {
		return false
	}
	if !exactAnchorImplementationActive(brief) {
		return false
	}
	return len(brief.ChangedPaths) == 0 && len(changedPathsFromToolResults(results)) == 0
}

func planThenExecuteExactAnchorPlanReady(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) == 0 || len(brief.RequiredEditSnippets) == 0 {
		return false
	}
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
	if len(required) == 0 {
		return false
	}
	covered := make(map[string]struct{}, len(brief.RequiredEditSnippets))
	for _, snippet := range brief.RequiredEditSnippets {
		path := normalizeRunnerPath(snippet.Path)
		if path == "" {
			continue
		}
		if _, ok := required[path]; ok {
			covered[path] = struct{}{}
		}
	}
	for path := range required {
		if _, ok := covered[path]; !ok {
			return false
		}
	}
	return true
}

func implementationPreWriteRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationPreWriteAllowedTools(brief.AllowedTools)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = mergeRunnerStateStrings([]string{"implementation_prewrite_write_only"}, brief.Constraints)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"exact-anchor required-path write capability",
		"answer <<QUESTION>> only if the packet lacks the exact anchor needed for that required path",
	}, brief.RequiredNext)
	brief.InvalidActions = mergeRunnerStateStrings([]string{
		"read/search/task_ledger/shell before first exact-anchor required-path write",
	}, brief.InvalidActions)
	brief.SearchPolicy = mergeRunnerStateStrings([]string{
		"the exact target packet and required_edit_paths are the pre-write evidence surface",
		"do not use discovery or task planning before the first exact-anchor required-path write",
	}, brief.SearchPolicy)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationScopedEditRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationScopedEditAllowedToolsWithRegistry(brief.AllowedTools, nil)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}
	brief.Constraints = mergeRunnerStateStrings([]string{"implementation_required_path_scoped_surface"}, brief.Constraints)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"required-path edit, semantic/context/project discovery or narrow required-path read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = mergeRunnerStateStrings([]string{
		"task_ledger or broad discovery before resolving the required-path contract",
		"raw read/search outside required edit paths before resolving the required-path contract",
	}, brief.InvalidActions)
	brief.SearchPolicy = mergeRunnerStateStrings([]string{
		"required edit paths define the implementation scope",
		"prefer context, language, and declared read-only project discovery tools for named edit-safety gaps before raw source reads",
		"raw source reads are only for exact anchor confirmation on required paths or explicit evidence paths",
		"large range-less raw source reads on required/evidence paths are bounded by the harness; request start_line/end_line or a symbol for follow-up",
	}, brief.SearchPolicy)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetWriteReadyRuntimeBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	pendingTools := sourceEvidencePendingRedirectTools(brief.AllowedTools, pending)
	sourceRefresh := len(pending) == 0 && implementationTargetWriteReadySourceRefreshProjectionActive(brief, results, brief.AllowedTools)
	sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3)
	allowed := implementationTargetWriteReadyAllowedToolsForBrief(brief.AllowedTools, brief, results)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = mergeRunnerStateStrings([]string{"implementation_target_write_ready"}, brief.Constraints)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
	}, brief.RequiredNext)
	if sourceRefresh {
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"parser-readable write to the sourced target path, bounded read_file/search_content source refresh with named_gap for a precise edit-safety fact, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"broad read/search after source-body evidence",
			"read/search for already-sourced paths unless the request adds a bounded missing range or exact named_gap",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"use bounded source refresh only when an exact missing edit-safety fact remains; otherwise write or block",
			"read_file/search_content refreshes must name the gap and stay scoped to source code, not docs or broad discovery",
		}, brief.SearchPolicy)
		brief.Constraints = mergeRunnerStateStrings([]string{"bounded_source_refresh_allowance"}, brief.Constraints)
	} else {
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"additional read/search after source-body evidence",
			"verification before a successful write",
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"the gathered source-body evidence is the pre-write target surface",
			"do not inspect further before the first write",
		}, brief.SearchPolicy)
	}
	if sourcedSummary != "" {
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"write using the already-read source-body evidence for: " + sourcedSummary,
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"read/search/navigation for already-sourced path(s): " + sourcedSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"already-sourced path(s) must not be read, searched, or navigated again before the first write: " + sourcedSummary,
		}, brief.SearchPolicy)
		brief.Constraints = mergeRunnerStateStrings([]string{"source_body_evidence_paths: " + sourcedSummary}, brief.Constraints)
	}
	if len(pendingTools) > 0 {
		pendingSummary := sourceEvidencePendingRedirectSummary(pending, 3)
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)
		brief.RequiredTools = []string{"exact_pending_source_body_followup_or_edit_file_or_apply_patch"}
		brief.RequiredNext = mergeRunnerStateStrings([]string{
			"resolve the pending redirected source-body obligation (" + pendingActionSummary + "), then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = mergeRunnerStateStrings([]string{
			"broad read/search while a redirected source-body obligation is pending",
			"read/navigation for any path except the exact pending redirected source-body follow-up: " + pendingActionSummary,
		}, brief.InvalidActions)
		brief.SearchPolicy = mergeRunnerStateStrings([]string{
			"one redirected source-body obligation remains open: " + pendingActionSummary,
			"use only the pending redirect's symbol/body follow-up, with at most one same-path outline first when the symbol is unknown; do not use references, broad discovery, or unrelated navigation",
		}, brief.SearchPolicy)
		brief.Constraints = mergeRunnerStateStrings([]string{"pending_redirected_source_body_obligation: " + pendingSummary}, brief.Constraints)
	}
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func implementationTargetSourceRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationTargetSourceAllowedTools(brief.AllowedTools)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = []string{"bounded_workspace_evidence_tool", "edit_file_or_apply_patch"}
	brief.Constraints = mergeRunnerStateStrings([]string{"implementation_target_source_pending"}, brief.Constraints)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"localize the concrete edit target, read bounded source-body evidence with named_gap for the missing edit-safety fact, write only after exact source-body evidence and replacement shape are known, or <<QUESTION>> with the exact blocker/no-op evidence",
	}, brief.RequiredNext)
	brief.InvalidActions = mergeRunnerStateStrings([]string{
		"final answer before a successful write, no-op, or blocker",
		"write before exact source-body evidence and replacement shape",
		"verification before a successful write",
	}, brief.InvalidActions)
	brief.SearchPolicy = mergeRunnerStateStrings([]string{
		"do not run verification before the first write",
		"use bounded read/search/context only for a named missing target or edit-safety fact",
	}, brief.SearchPolicy)
	brief.ForbiddenTools = mergeRunnerStateStrings(brief.ForbiddenTools, []string{"source_sanity", "shell", "git_diff", "git_status"})
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func exactAnchorPreWriteRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := implementationPreWriteAllowedTools(brief.AllowedTools)
	brief.AllowedTools = allowed
	brief.ToolManifest = nil
	if len(allowed) > 0 {
		brief.ToolManifest = runnerSanitizedToolManifest(allowed)
	}
	brief.RequiredTools = []string{"edit_file_or_apply_patch"}
	brief.Constraints = mergeRunnerStateStrings([]string{"exact_anchor_prewrite_write_first"}, brief.Constraints)
	brief.RequiredNext = mergeRunnerStateStrings([]string{
		"exact-anchor write capability with the supplied target-packet anchor",
		"answer <<QUESTION>> only if the supplied anchor is missing or stale",
	}, brief.RequiredNext)
	brief.InvalidActions = mergeRunnerStateStrings([]string{
		"read/search/shell before first exact-anchor write",
		"verification before first exact-anchor write",
	}, brief.InvalidActions)
	brief.SearchPolicy = mergeRunnerStateStrings([]string{
		"target-packet exact anchors count as current-state path and source evidence for the first write",
		"do not inspect before the first exact-anchor write",
	}, brief.SearchPolicy)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerLivePhase(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) string {
	if phase := debugLogPhase(brief, results); phase != "" {
		return phase
	}
	if phase := evidenceFirstMutationPhase(brief, results); phase != "" {
		return phase
	}
	return workerruntime.ClassifyPhase(req, result)
}

func debugLogPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return ""
	}
	if debugLogEvidenceSatisfied(brief, results) {
		return "log_analysis"
	}
	if _, ok := latestDebugLogSearchHit(results); ok {
		return "failure_window"
	}
	selection, ok := latestDebugLogSelection(results)
	if !ok {
		return "latest_log_selection"
	}
	if hasDebugLogReadForSelection(results, selection) {
		return "selected_log_analysis"
	}
	return "latest_log_tail"
}

func evidenceFirstMutationPhase(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return ""
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "post_target_write"
	}
	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "source_evidence"
	}
	if targetReadCount == 0 {
		return "target_entry"
	}
	return "target_inspection"
}

func buildRunnerNextActionState(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerNextActionState {
	return buildRunnerNextActionStateWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, nil)
}

func buildRunnerNextActionStateWithAllowedTools(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) runnerNextActionState {
	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools)
	return state
}

func buildRunnerNextActionStateWithAllowedToolsAndContract(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) (runnerNextActionState, TurnPhaseContract) {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	brief = runnerBriefWithPhaseHints(brief, results)
	guidance := workerruntime.BuildActionGuidance(workerruntime.ActionGuidanceInput{
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		Constraints:               runnerGuidanceConstraints(agent, brief, results),
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Phase:             runnerLivePhase(brief, req, result, results),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          tool,
		LastTarget:        workerruntime.ToolRequestTarget(req),
		ResultSummary:     truncateRunnerStateText(result.Summary, 220),
		ResultError:       truncateRunnerStateText(result.Error, 260),
		ResultOutputChars: len(result.Output),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
	}
	// When context_pack returns a view with candidate files that carry
	// symbol handles, surface the top symbols as actionable annotations.
	if strings.TrimSpace(tool) == "context_pack" && strings.TrimSpace(result.Error) == "" {
		state.CandidateSymbols = contextPackCandidateSymbols(result.Output, 5)
	}
	if status := scratchpadDeliverableFailedEvidenceStatus(brief, results); status != "" {
		state.EvidenceStatus = []string{status}
	}
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") && delegatedReadOnlyClosureReady(agent, brief, results) {
		state.EvidencePaths = delegatedClosureEvidencePaths(brief.Objective, results, 6)
	}
	if !runnerBriefRequiresMutation(brief) && !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		state.EvidencePaths = postChildClosureEvidencePaths(brief.Objective, results, 6)
	}
	state.Constraints = mergeRunnerStateStrings(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = mergeRunnerStateStrings(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = mergeRunnerStateStrings(brief.RequiredNext, guidance.ValidNext)
	phaseHint := ""
	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		phaseHint = turnPhaseReviewRepairTerminal
	}
	phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:        phaseHint,
		Brief:            brief,
		Results:          results,
		LastRequest:      req,
		LastResult:       result,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
	})
	state = applyTurnPhaseContractToNextActionState(state, phaseContract)
	if strings.TrimSpace(result.Error) != "" {
		state.RecoveryCard = RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
			PhaseContract:       phaseContract,
			CurrentAllowedTools: effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, phaseContract),
			RejectedRequest:     req,
			RejectedResult:      result,
			EvidenceRefs:        state.EvidencePaths,
		}))
	}
	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{
			"review_repair_closeout_gate_fired",
			"terminal_review_repair_outcome_required",
		})
		state.InvalidNextActions = reviewRepairTerminalInvalidNextActions()
		state.NextValidActions = reviewRepairTerminalValidNextActions()
		state.StateSummary = runnerStateSummary(state)
		return state, phaseContract
	}
	state = applyDelegatedReadOnlyClosureBias(state, agent, brief, results)
	state = applyPostChildClosureBias(state, agent, brief, results)
	state = applyScratchpadDeliverableCloseoutBias(state, brief, results)
	state = applyReadOnlyDiscoveryPendingGuidance(state, brief, req, result, results)
	state = applyClosurePromptRefinement(state)
	state.StateSummary = runnerStateSummary(state)
	return state, phaseContract
}

func applyReadOnlyDiscoveryPendingGuidance(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) runnerNextActionState {
	if strings.TrimSpace(state.Phase) != "read_only_evidence_pending" || !readOnlyDirectAnswerLane(brief) {
		return state
	}
	if len(runnerEvidencePathsForBrief(brief, results, 1)) > 0 {
		return state
	}
	strength := discoveryConsumptionResultStrength(req, result)
	if strength == discoveryConsumptionNone {
		return state
	}
	candidates := readOnlySourceLikePaths(discoveryConsumptionCandidatesFromResult(result, 4), 4)
	if len(candidates) == 0 {
		return state
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	candidateSummary := strings.Join(candidates, ", ")
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{"source_body_evidence_missing_after_discovery"})
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"final answer before source-body evidence",
		"treating discovery candidates, context packets, or search hits as source-body evidence",
	})
	switch strength {
	case discoveryConsumptionCandidateOnly:
		state.EvidenceStatus = mergeRunnerStateStrings(state.EvidenceStatus, []string{
			"discovery_status: candidate-only; source owner and source-body evidence are still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"source owner was not established by " + tool + "; use one materially different source-owner or source-body probe scoped to the question, or <<QUESTION>> with the exact unavailable evidence",
			"if candidates are plausible, inspect one returned path with source-body evidence; otherwise switch to an exact lexical/symbol/source-owner probe drawn from the user's claim and the code vocabulary",
		}, state.NextValidActions)
		state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
			"exact replay of the same weak discovery request",
		})
	case discoveryConsumptionSourceOwner:
		state.EvidenceStatus = mergeRunnerStateStrings(state.EvidenceStatus, []string{
			"discovery_status: candidate owner paths returned, but source-body evidence is still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"inspect one returned path or handle with source-body evidence, take one targeted refinement with concrete scope or named_gap only if the returned owner does not establish the claim, or <<QUESTION>> with the exact blocker",
			"candidate path(s): " + candidateSummary,
		}, state.NextValidActions)
		state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
			"repeated broad or identical rediscovery before inspecting or materially refining returned candidates",
		})
	}
	return state
}

func buildRunnerFailureFeedbackPacket(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief) runnerFailureFeedbackPacket {
	return buildRunnerFailureFeedbackPacketWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, nil)
}

func buildRunnerFailureFeedbackPacketWithAllowedTools(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string) runnerFailureFeedbackPacket {
	state := buildRunnerNextActionStateWithAllowedTools(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools)
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   req,
		Result:                    result,
		EvidencePaths:             state.EvidencePaths,
		Constraints:               state.Constraints,
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
	})
	if scratchpadFailedEvidenceFeedbackApplies(brief, req, result, results) {
		recovery.DoNotRepeat = util.AppendUnique(recovery.DoNotRepeat, "same failed evidence lookup through a lower-level or raw read")
		recovery.RequiredNext = util.AppendUnique(recovery.RequiredNext, "record the failed, stale, ambiguous, or out-of-scope evidence as a deliverable gap")
		recovery.ValidNext = []string{
			"write the non-source deliverable with the evidence gap recorded",
			"answer with <<QUESTION>> only if the evidence gap makes the deliverable impossible",
		}
	}
	// failure_feedback is emitted right after next_action_state, which
	// already carries Lane/Step/Reason/InvalidNext/ValidNext/StateSummary
	// for the same step. Re-emitting those fields wastes context. Keep
	// only the failure-specific signals (FailedTool/FailedTarget for
	// quick reference, FailureClass, the recovery-specific
	// DoNotRepeat/RequiredNext, and the UserVisibleBlocker flag). Tests
	// still rely on RequiredNext/ValidNext carrying closure-biased
	// options, so ValidNext keeps recovery.ValidNext (the *new* options
	// the recovery plan introduces) but drops the duplicates that
	// next_action_state already published.
	packet := runnerFailureFeedbackPacket{
		Lane:               state.Lane,
		Step:               state.Step,
		Reason:             state.Reason,
		FailedTool:         state.LastTool,
		FailedTarget:       state.LastTarget,
		FailureClass:       recovery.FailureClass,
		ResultSummary:      state.ResultSummary,
		ResultError:        state.ResultError,
		EvidenceStatus:     append([]string(nil), state.EvidenceStatus...),
		ValidNext:          append([]string(nil), recovery.ValidNext...),
		RecoveryCard:       state.RecoveryCard,
		UserVisibleBlocker: recovery.UserVisibleBlocker,
	}
	packet.DoNotRepeat = append([]string(nil), recovery.DoNotRepeat...)
	packet.RequiredNext = append([]string(nil), recovery.RequiredNext...)
	if phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            runnerBriefWithPhaseHints(brief, results),
		Results:          results,
		LastRequest:      req,
		LastResult:       result,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
	}); phaseContract.PhaseID != "" && phaseContract.PhaseID != turnPhaseGeneric && !scratchpadFailedEvidenceFeedbackApplies(brief, req, result, results) {
		packet.ValidNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.RequiredNext = append([]string(nil), phaseContract.RequiredNext...)
		packet.InvalidNext = append([]string(nil), phaseContract.InvalidNext...)
	}
	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		packet.FailureClass = "terminal_outcome_required"
		packet.DoNotRepeat = reviewRepairTerminalInvalidNextActions()
		packet.RequiredNext = []string{
			"choose exactly one terminal review-repair outcome using the gathered evidence",
		}
		packet.ValidNext = reviewRepairTerminalValidNextActions()
		packet.InvalidNext = reviewRepairTerminalInvalidNextActions()
		packet.StateSummary = runnerStateSummary(state)
	}
	return packet
}

func effectiveRecoveryCardAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, contract TurnPhaseContract) []string {
	if planThenExecutePlannerBrief(brief) {
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if len(uniquePhaseStrings(state.CallableTools)) > 0 {
			return state.CallableTools
		}
	}
	tools := runnerPlanExecuteTextualAllowedTools(currentAllowedTools, brief, results)
	if len(uniquePhaseStrings(tools)) > 0 {
		return tools
	}
	tools = uniquePhaseStrings(currentAllowedTools)
	if len(tools) > 0 {
		return tools
	}
	return firstNonEmptyRecoveryCardStrings(
		contract.ActionPolicy.CallableToolNames,
		contract.AllowedTools,
	)
}

func reviewRepairTerminalOutcomeRequiredState(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}
	summary := strings.TrimSpace(result.Summary)
	return strings.EqualFold(summary, "Review-repair terminal outcome required") ||
		strings.HasPrefix(summary, "Review-repair completion gate rejected")
}

func reviewRepairTerminalValidNextActions() []string {
	return []string{
		"`Decision: pass` with a `Verification Checklist`",
		"a corrective `submit_plan` whose rationale starts with `Verification Checklist:`",
		"`<<QUESTION>>:` naming the exact blocker that prevents a terminal review-repair outcome",
	}
}

func reviewRepairTerminalInvalidNextActions() []string {
	return []string{
		"read/search/discovery/context tools after the review-repair closeout gate",
		"running commands after the review-repair closeout gate",
		"planning, summarizing, or adding prose before or after the terminal outcome",
	}
}

func applyScratchpadDeliverableCloseoutBias(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !scratchpadDeliverableCloseoutReady(brief, results) {
		return state
	}
	target := strings.Join(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results)), ", ")
	if target == "" {
		target = "the required deliverable"
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{
		"scratchpad_deliverable_closeout_ready",
		"non_source_deliverable_path_unwritten",
	})
	state.EvidenceStatus = mergeRunnerStateStrings(state.EvidenceStatus, []string{
		fmt.Sprintf("non-source deliverable still unwritten: %s; gathered evidence and accepted gaps should be recorded in the deliverable", target),
	})
	state.NextValidActions = []string{
		fmt.Sprintf("deliverable-write capability for %s with the corrected content, recording source uncertainty as Source verification pending or Scope Correction Required", target),
		"answer <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty",
	}
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"new source discovery after completion gate rejected a prose-only non-source deliverable",
		"repairing an optional evidence lookup instead of recording the gap in the scratchpad deliverable",
		"final answer without write_file for the required non-source deliverable",
	})
	return state
}

func scratchpadDeliverableCloseoutReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) {
		return false
	}
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(result.Summary + " " + result.Error))
		if strings.Contains(lower, "no successful write") ||
			strings.Contains(lower, "no successful write tool") ||
			strings.Contains(lower, "workspace change") ||
			strings.Contains(lower, "remain unwritten") {
			return true
		}
	}
	return false
}

func (r *Runner) scratchpadDeliverableCloseoutNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, target string, reject bool) {
	if r == nil || !scratchpadDeliverableCloseoutReady(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", "", false
	}
	missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))
	target = "the required deliverable"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	summary = "Scratchpad deliverable closeout blocked non-write tool"
	errorText = fmt.Sprintf("%s is not available after the completion gate rejected a prose-only response for non-source deliverable path(s): %s. The available contract outcomes are deliverable-write capability with full content or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The completion gate already determined that %s is still unwritten. Do not perform additional reads, searches, discovery, planning, or optional evidence repair from this recovery state. The deliverable-write contract records missing/stale/ambiguous evidence as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> if the deliverable is impossible.", target)
	return summary, errorText, prompt, target, true
}

func (r *Runner) scratchpadDeliverableSourceDiscoveryRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", false
	}
	if len(r.remainingRequiredEditPaths(results)) == 0 || !r.scopedReadClassTool(req) {
		return "", "", "", false
	}
	if scratchpadDeliverablePrewriteReadAllowed(req) {
		return "", "", "", false
	}
	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}
	summary = "Scratchpad deliverable source discovery redirected to write"
	errorText = fmt.Sprintf("%s is not available before writing non-source scratchpad deliverable path(s): %s. Read required scratchpad inputs only; record missing or uncertain source evidence in the deliverable instead of reopening source discovery.", tool, target)
	prompt = fmt.Sprintf("This lane is a non-source scratchpad writer/reviewer phase for %s. Source discovery is closed here: the deliverable-write contract uses the preloaded evidence already in the brief, records missing/stale/ambiguous facts as `Source verification pending:` or `## Scope Correction Required`, or answers with <<QUESTION>> and the exact blocker if the deliverable cannot be written from the scratchpad inputs plus recorded uncertainty.", target)
	return summary, errorText, prompt, true
}

func scratchpadDeliverablePrewriteReadAllowed(req workspace.ToolRequest) bool {
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}
	return writePathIsNonSource(path)
}

func scratchpadFailedEvidenceFeedbackApplies(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if !briefIsScratchpadDeliverableLane(brief) || strings.TrimSpace(result.Error) == "" {
		return false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) || !isReadClassTool(req) {
		return false
	}
	return len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) > 0
}

func mergeRunnerStateStrings(groups ...[]string) []string {
	var out []string
	for _, group := range groups {
		for _, value := range group {
			out = util.AppendUnique(out, value)
		}
	}
	return out
}

func runnerCurrentAllowedOrBriefTools(currentAllowedTools []string, briefAllowedTools []string) []string {
	if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}
	return append([]string(nil), briefAllowedTools...)
}

func runnerStateSummary(state runnerNextActionState) string {
	parts := []string{state.Reason}
	if state.LastTool != "" {
		if state.LastTarget != "" {
			parts = append(parts, state.LastTool+" "+state.LastTarget)
		} else {
			parts = append(parts, state.LastTool)
		}
	}
	if state.ResultError != "" {
		parts = append(parts, "blocked: "+state.ResultError)
	} else if state.ResultSummary != "" {
		parts = append(parts, state.ResultSummary)
	}
	return truncateRunnerStateText(strings.Join(parts, " | "), 320)
}

func prependRunnerStateStrings(preferred []string, existing []string) []string {
	out := make([]string, 0, len(preferred)+len(existing))
	for _, value := range preferred {
		out = util.AppendUnique(out, value)
	}
	for _, value := range existing {
		out = util.AppendUnique(out, value)
	}
	return out
}

func filterRunnerStateStrings(values []string, drop func(string) bool) []string {
	if len(values) == 0 {
		return values
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || drop(trimmed) {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return filtered
}

func runnerGuidanceConstraints(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	constraints := append([]string(nil), brief.Constraints...)
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		constraints = mergeRunnerStateStrings(constraints, []string{"plan_then_execute_submit_plan_ready"})
	}
	if delegatedReadOnlyClosureReady(agent, brief, results) {
		constraints = mergeRunnerStateStrings(constraints, []string{"answer_readiness_reached"})
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		constraints = mergeRunnerStateStrings(constraints, []string{"child_packet_available"})
	}
	return constraints
}

func applyDelegatedReadOnlyClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !delegatedReadOnlyClosureReady(agent, brief, results) {
		return state
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{"answer_readiness_reached"})
	state.InvalidNextActions = filterRunnerStateStrings(state.InvalidNextActions, func(value string) bool {
		return value == "final_answer_without_satisfying_completion_gate"
	})
	state.NextValidActions = filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "gather or edit the missing evidence")
	})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"answer directly from the evidence already gathered",
	}, state.NextValidActions)
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"open-ended discovery after answer readiness without naming a concrete gap",
	})
	return state
}

func delegatedReadOnlyClosureReady(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if slices.Contains(brief.Constraints, "answer_readiness_reached") {
		return true
	}
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") || strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" {
		return false
	}
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func applyPostChildClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") || !postChildPacketClosureReadyWithBrief(brief, results) {
		return state
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, []string{"child_packet_available"})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"answer directly from the child packet if it already resolves the question",
	}, state.NextValidActions)
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, []string{
		"open-ended discovery after a child packet without naming a concrete gap",
	})
	return state
}

func postChildPacketClosureReadyWithBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if !postChildReadOnlyPacketAvailable(brief, results) {
		return false
	}
	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func postChildReadOnlyPacketAvailable(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	objective := strings.TrimSpace(workerruntime.NormalizeRuntimeBrief(brief).Objective)
	for _, result := range results {
		if readOnlyTaskResultAnswerableForObjective(result, objective) {
			return true
		}
	}
	return false
}

func readOnlyTaskResultAnswerableForObjective(result workspace.ToolResult, objective string) bool {
	if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
		return false
	}
	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		if strings.TrimSpace(objective) == "" {
			return readOnlyEvidencePacketAnswerable(packet)
		}
		return readOnlyEvidencePacketAnswerableForObjective(packet, objective)
	}
	return childPacketContainsGroundedAnswerEvidence(objective, result.Output)
}

func runnerClosureGuidanceActive(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) bool {
	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		return true
	}
	if delegatedReadOnlyClosureReady(agent, brief, results) {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") {
		return false
	}
	return postChildPacketClosureReadyWithBrief(brief, results)
}

func closureSynthesisAfterIgnoredNudgePrompt(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool, delegatedDiminishingNudges int, postChildDiminishingNudges int) (string, bool) {
	// Read-only closure nudges are advisory. The harness may reject repeated
	// identical or broad probes, but it must not convert its readiness estimate
	// into a no-tools final-answer requirement.
	return "", false
}

func truncateRunnerStateText(value string, max int) string {
	return util.Truncate(strings.TrimSpace(value), max)
}

func (r *Runner) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	ledger := runnerBudgetLedger{
		Lane:                     r.agentName(),
		UsedModelCalls:           metrics.ModelCalls,
		MaxModelCalls:            policy.MaxModelCalls,
		UsedToolResults:          metrics.ToolCalls,
		PromptTokens:             metrics.PromptTokens,
		CompletionTokens:         metrics.CompletionTokens,
		TotalTokens:              metrics.TotalTokens,
		ProgressTurns:            metrics.ProgressTurns,
		RejectedActions:          metrics.RejectedActions,
		CompletionGateRejections: metrics.CompletionGateRejections,
		ContextCompactions:       metrics.Compactions,
		ReadClassCalls:           metrics.ReadClassCalls,
		DurationMillis:           time.Since(metrics.startedAt).Milliseconds(),
		StopReason:               strings.TrimSpace(stopReason),
		BudgetExhausted:          exhausted,
	}
	if policy.MaxToolSteps > 0 {
		ledger.MaxToolSteps = policy.MaxToolSteps
	}
	if exhausted {
		ledger.NextAllowedActions = []string{"answer_with_explicit_uncertainty", "ask_user_for_narrower_scope", "resume_with_fresh_budget"}
		ledger.UserVisibleUncertainty = "answer from gathered evidence and mention only unsupported claims that remain material"
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "budget_ledger", Content: string(data)})
}

func (r *Runner) emitCompletionStatus(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
	if r == nil || r.OnEvent == nil {
		return
	}
	changedPaths := mapKeys(changedPathsFromToolResults(results))
	payload := runnerCompletionStatus{
		Lane:               r.agentName(),
		TaskFamily:         strings.TrimSpace(r.RuntimeBrief.TaskFamily),
		Contract:           strings.TrimSpace(r.RuntimeBrief.Contract),
		Status:             strings.TrimSpace(status),
		Reason:             strings.TrimSpace(reason),
		BlockerKind:        strings.TrimSpace(blockerKind),
		RequiresEdit:       runnerBriefRequiresMutation(r.RuntimeBrief),
		EditAttempted:      len(changedPaths) > 0,
		ChangedPaths:       changedPaths,
		UserVisibleBlocker: userVisibleBlocker,
	}
	r.emitEvent(marshalRunEvent("completion_status", payload))
}

func workspaceToolRulesPermissionDeny(decision PermissionDecisionSnapshot) bool {
	return decision.Decision == PermissionDeny && strings.Contains(strings.TrimSpace(decision.Rule), "workspace.tool_rules")
}

func workspaceToolRulesPermissionDenyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	target := permissionTarget(req)
	if target == "" {
		target = "the requested target"
	}
	return fmt.Sprintf("I could not read `%s` because workspace `tool_rules` deny `%s` for that path. No file contents were returned.", target, tool)
}

func (r *Runner) emitModelCallTraceWithOptions(step int, label string, prompt []llama.ChatMessage, reply llama.ChatMessage, durationMs int64, opts llama.ChatOptions, supportsNativeTools bool, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	chosen := "<answer>"
	if len(reply.ToolCalls) > 0 && strings.TrimSpace(reply.ToolCalls[0].Function.Name) != "" {
		chosen = strings.TrimSpace(reply.ToolCalls[0].Function.Name)
	}
	roleChars := make(map[string]int, 4)
	for _, m := range prompt {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "unknown"
		}
		roleChars[role] += len(m.Content)
	}
	parserContract := runnerTurnPhaseParserContract(opts, r.RuntimeBrief, results, supportsNativeTools)
	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, parserContract == "submit_plan_json")
	payload := policy.TelemetryFields()
	payload["step"] = step
	payload["label"] = label
	payload["agent"] = r.agentName()
	payload["prompt_tokens"] = reply.Usage.PromptTokens
	payload["completion_tokens"] = reply.Usage.CompletionTokens
	payload["total_tokens"] = reply.Usage.TotalTokens
	payload["ctx_size"] = r.ContextSize
	payload["message_count"] = len(prompt)
	payload["role_chars"] = roleChars
	payload["chosen_tool"] = chosen
	if textual := modelCallParsedTextualToolTelemetry(reply); textual != nil {
		for key, value := range textual {
			payload[key] = value
		}
	}
	payload["duration_ms"] = durationMs
	backendKind := runnerTurnPhaseBackendKind(r)
	payload["backend_kind"] = backendKind
	backendCaps := llama.BackendStructuredOutputCapabilities(backendKind)
	payload["backend_capability_native_tools"] = backendCaps.NativeTools
	payload["backend_capability_json_object"] = backendCaps.JSONObject
	payload["backend_capability_strict_json_schema"] = backendCaps.StrictJSONSchema
	payload["backend_capability_grammar"] = backendCaps.Grammar
	payload["backend_capability_thinking_controls"] = backendCaps.ThinkingControls
	payload["backend_capability_request_timeout_telemetry"] = backendCaps.RequestTimeoutTelemetry
	payload["model_alias"] = safeModelAliasForTelemetry(opts.Model)
	payload["parser_contract"] = parserContract
	if finish := strings.TrimSpace(reply.FinishReason); finish != "" {
		payload["finish_reason"] = finish
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "model_call", Content: string(data)})
}

func (r *Runner) emitToolRequestNormalizerEvent(outcome string, transport string, req workspace.ToolRequest, parseErr error, parserFailureClass string) {
	if r == nil || r.OnEvent == nil {
		return
	}
	payload := map[string]any{
		"outcome":   strings.TrimSpace(outcome),
		"transport": strings.TrimSpace(transport),
		"tool":      strings.TrimSpace(req.Tool),
		"path":      normalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
	}
	if parseErr == nil && strings.TrimSpace(req.Tool) != "" && toolRequestNormalizerTransportIsModelTextual(transport) {
		payload["parsed_textual_tool"] = strings.TrimSpace(req.Tool)
		payload["textual_tool_transport"] = strings.TrimSpace(transport)
	}
	if parseErr != nil {
		payload["error"] = strings.TrimSpace(parseErr.Error())
		if parserFailureClass == "" {
			parserFailureClass = toolRequestParserFailureClass("", parseErr)
		}
		if parserFailureClass != "" {
			payload["parser_failure_class"] = parserFailureClass
		}
	}
	r.emitEvent(marshalRunEvent("tool_request_normalizer", payload))
}

func toolRequestNormalizerTransportIsModelTextual(transport string) bool {
	switch strings.TrimSpace(transport) {
	case toolRequestNormalizerTransportTextual,
		toolRequestNormalizerTransportFencedJSON,
		toolRequestNormalizerTransportKeyValue,
		toolRequestNormalizerTransportKeyValueArgs,
		toolRequestNormalizerTransportFunctionCall,
		toolRequestNormalizerTransportNestedArguments,
		toolRequestNormalizerTransportIdiomCLI,
		toolRequestNormalizerTransportXMLToolMarkup,
		toolRequestNormalizerTransportStringifiedSubmitRepair:
		return true
	default:
		return false
	}
}

func modelCallParsedTextualToolTelemetry(reply llama.ChatMessage) map[string]any {
	if len(reply.ToolCalls) > 0 {
		return nil
	}
	normalization := normalizeToolRequestSyntax(reply)
	if !normalization.OK || strings.TrimSpace(normalization.Request.Tool) == "" {
		return nil
	}
	transport := strings.TrimSpace(normalization.EventTransport)
	if transport == "" || transport == toolRequestNormalizerTransportNative {
		return nil
	}
	return map[string]any{
		"parsed_textual_tool":       strings.TrimSpace(normalization.Request.Tool),
		"parsed_tool_transport":     transport,
		"parsed_tool_request_valid": true,
	}
}

func safeModelAliasForTelemetry(model string) string {
	alias := strings.TrimSpace(model)
	if alias == "" {
		return ""
	}
	if strings.ContainsAny(alias, `/\`) {
		alias = filepath.Base(alias)
	}
	return util.Truncate(alias, 96)
}

func (r *Runner) emitEvent(event RunEvent) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(event.Role) == "" {
		return
	}
	r.OnEvent(event)
}

func (r *Runner) emitDelegatedReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, results, 8)
	payload := map[string]any{
		"agent":                      r.agentName(),
		"reason":                     "sufficient_delegated_read_only_evidence",
		"trigger_tool":               strings.TrimSpace(req.Tool),
		"composite_discovery_packet": delegatedReadOnlyHasCompositeDiscoveryPacket(results),
		"evidence_paths":             paths,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "delegated_read_only_answer_ready", Content: string(data)})
}

func (r *Runner) emitReadOnlyAnswerReadyEvent(req workspace.ToolRequest, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}
	paths := readOnlyPresentationEvidencePathsForBrief(r.RuntimeBrief, results, 8)
	payload := map[string]any{
		"agent":          r.agentName(),
		"reason":         "sufficient_read_only_evidence",
		"trigger_tool":   strings.TrimSpace(req.Tool),
		"evidence_paths": paths,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "read_only_answer_ready", Content: string(data)})
}

func (r *Runner) compactTurnMessagesIfNeeded(messages *[]llama.ChatMessage, policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) bool {
	if messages == nil || len(*messages) == 0 {
		return false
	}
	if policy.MaxContextMessages <= 0 && policy.MaxContextChars <= 0 {
		return false
	}
	current := *messages
	tooManyMessages := policy.MaxContextMessages > 0 && len(current) > policy.MaxContextMessages
	tooManyChars := policy.MaxContextChars > 0 && chatMessagesCharCount(current) > policy.MaxContextChars
	if !tooManyMessages && !tooManyChars {
		return false
	}
	if len(current) <= 3 {
		return false
	}

	prefixEnd := runnerCompactionPrefixEnd(current)
	middleBudget, tailFloor := compactionBudget(policy)

	// Always keep a tail floor for conversation continuity.
	tailStart := len(current) - tailFloor
	if tailStart < prefixEnd {
		tailStart = prefixEnd
	}

	// Score middle messages (between prefix and tail floor) for evidence value.
	middleEnd := tailStart
	if middleEnd <= prefixEnd {
		middleEnd = prefixEnd
	}
	type scoredMsg struct {
		msg     llama.ChatMessage
		origIdx int
		score   float64
	}
	var scored []scoredMsg
	usedChars := 0
	for i := prefixEnd; i < middleEnd; i++ {
		msg := current[i]
		resultIdx := resultIndexForMessage(current, i, len(results))
		var result *workspace.ToolResult
		if resultIdx >= 0 {
			result = &results[resultIdx]
		}
		s := scoreMessageForRetention(msg, result, r.RuntimeBrief, results)
		scored = append(scored, scoredMsg{msg: msg, origIdx: i, score: s})
	}
	// Sort by score descending; tie-break by position (earlier first).
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].origIdx < scored[j].origIdx
	})

	// Keep scored messages verbatim up to the evidence budget.
	keptMiddle := make(map[int]bool)
	const minScoreThreshold = 0.15
	for _, sm := range scored {
		if sm.score < minScoreThreshold {
			break
		}
		chars := msgCharCount(sm.msg)
		if usedChars+chars > middleBudget {
			break
		}
		usedChars += chars
		keptMiddle[sm.origIdx] = true
	}

	summary := llama.ChatMessage{
		Role:    "system",
		Content: r.runnerCompactionSummary(policy, ledger, results),
	}

	// Build compacted: prefix + kept middle + summary + tail floor.
	compacted := make([]llama.ChatMessage, 0, prefixEnd+len(keptMiddle)+1+tailFloor)
	compacted = append(compacted, current[:prefixEnd]...)
	for i := prefixEnd; i < middleEnd; i++ {
		if keptMiddle[i] {
			compacted = append(compacted, current[i])
		}
	}
	compacted = append(compacted, summary)
	compacted = append(compacted, current[tailStart:]...)
	if len(compacted) >= len(current) {
		return false
	}
	*messages = compacted
	r.emitContextCompaction(runnerContextCompaction{
		Lane:             r.agentName(),
		DroppedMessages:  len(current) - len(compacted),
		RetainedMessages: len(compacted),
		MaxHistory:       policy.MaxContextMessages,
		MaxChars:         policy.MaxContextChars,
		PinnedPaths:      runnerPinnedPathsForBrief(r.RuntimeBrief, results, 8),
		PinnedMemoryKind: "runner_task_state",
		Strategy:         "retain_system_and_user_objective_with_evidence_weighted_middle",
	})
	return true
}

func (r *Runner) emitContextCompaction(compaction runnerContextCompaction) {
	if r == nil || r.OnEvent == nil {
		return
	}
	data, err := json.Marshal(compaction)
	if err != nil {
		return
	}
	r.OnEvent(RunEvent{Role: "context_compaction", Content: string(data)})
}

func (r *Runner) runnerCompactionSummary(policy RunPolicy, ledger *taskLedgerState, results []workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("[runner context compaction]\n")
	b.WriteString("agent: ")
	b.WriteString(r.agentName())
	b.WriteString("\nstrategy: older runner turns were compacted; preserve this state when deciding the next action.\n")
	if brief := workerruntime.RenderRuntimeBriefSummary(r.runtimeBriefWithLiveState(policy, nil, results)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}
	if ledger != nil && ledger.created {
		b.WriteString("\nTask ledger:\n")
		b.WriteString(ledger.render())
		b.WriteString("\n")
	}
	if paths := runnerPinnedPathsForBrief(r.RuntimeBrief, results, 8); len(paths) > 0 {
		b.WriteString("\nPinned paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}
	if failed, ok := failedVerificationAfterLatestWrite(results, r.RuntimeBrief); ok {
		b.WriteString("\nOpen failed verification repair packet:\n")
		b.WriteString("- tool: ")
		b.WriteString(strings.TrimSpace(failed.Tool))
		b.WriteString("\n")
		if summary := strings.TrimSpace(failed.Summary); summary != "" {
			b.WriteString("- summary: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}
		if detail := strings.TrimSpace(failed.Error); detail != "" {
			b.WriteString("- error: ")
			b.WriteString(truncateRunnerStateText(detail, 500))
			b.WriteString("\n")
		}
		b.WriteString("- required next: repair the failing changed/required path, gather one exact edit-safety fact, rerun verification after repair, or answer <<QUESTION>> only for an external blocker.\n")
	}
	if len(results) > 0 {
		b.WriteString("\nRecent tool evidence:\n")
		for _, result := range tailToolResults(results, 8) {
			b.WriteString("- ")
			b.WriteString(result.Tool)
			if strings.TrimSpace(result.Summary) != "" {
				b.WriteString(": ")
				b.WriteString(result.Summary)
			}
			if strings.TrimSpace(result.Error) != "" {
				b.WriteString(" [error: ")
				b.WriteString(result.Error)
				b.WriteString("]")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func runnerCompactionPrefixEnd(messages []llama.ChatMessage) int {
	prefixEnd := 0
	for prefixEnd < len(messages) && messages[prefixEnd].Role == "system" {
		prefixEnd++
	}
	for i := prefixEnd; i < len(messages); i++ {
		if messages[i].Role == "user" {
			return i + 1
		}
	}
	if prefixEnd > 0 {
		return prefixEnd
	}
	if len(messages) < 2 {
		return len(messages)
	}
	return 2
}

func chatMessagesCharCount(messages []llama.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent)
	}
	return total
}

func msgCharCount(msg llama.ChatMessage) int {
	return len(msg.Role) + len(msg.Content) + len(msg.ReasoningContent)
}

func compactionBudget(policy RunPolicy) (middleBudget int, tailFloor int) {
	const estCharsPerMsg = 500
	totalBudget := policy.MaxContextChars
	if totalBudget <= 0 && policy.MaxContextMessages > 0 {
		totalBudget = policy.MaxContextMessages * estCharsPerMsg
	}
	if totalBudget <= 0 {
		totalBudget = 40 * estCharsPerMsg
	}
	// 30% middle evidence, 50% tail continuity, 20% buffer.
	middleBudget = totalBudget * 30 / 100
	tailChars := totalBudget * 50 / 100
	tailFloor = tailChars / estCharsPerMsg
	if tailFloor < 2 {
		tailFloor = 2
	}
	if tailFloor > 8 {
		tailFloor = 8
	}
	return middleBudget, tailFloor
}

func resultIndexForMessage(messages []llama.ChatMessage, msgIndex int, resultsLen int) int {
	if resultsLen == 0 {
		return -1
	}
	toolResultCount := 0
	for i := 0; i <= msgIndex && i < len(messages); i++ {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" || role == "tool_result" {
			toolResultCount++
		}
		if role == "system" && strings.Contains(messages[i].Content, "Tool:") {
			toolResultCount++
		}
	}
	idx := toolResultCount - 1
	if idx >= 0 && idx < resultsLen {
		return idx
	}
	return -1
}

// scoreMessageForRetention scores a message in the compaction middle range.
// Returns 0.0-1.0 where higher means "keep this verbatim."
// Weights reflect evidence hierarchy (verification > edit paths > evidence paths > tool type),
// not tuned constants. Tune only when eval runs show systematic quality degradation.
func scoreMessageForRetention(msg llama.ChatMessage, result *workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, allResults []workspace.ToolResult) float64 {
	if strings.TrimSpace(msg.Content) == "" {
		return 0.0
	}
	var score float64
	if result != nil {
		if strings.TrimSpace(result.Error) != "" {
			if resultIsVerificationAttempt(*result) {
				score += 0.40
			} else {
				score += 0.10
			}
		}
		if workspace.IsWorkspaceWriteTool(strings.TrimSpace(result.Tool)) && strings.TrimSpace(result.Error) == "" {
			score += 0.20
		}
		tool := strings.TrimSpace(result.Tool)
		if tool == "read_file" || tool == "code_navigate" || tool == "source_sanity" ||
			tool == "go_read_symbol" || tool == "go_extract_body" ||
			tool == "cpp_read_symbol" || tool == "cpp_extract_body" ||
			tool == "python_read_symbol" || tool == "python_extract_body" {
			score += 0.10
		}
	}
	content := strings.ToLower(msg.Content)
	editPaths := append([]string{}, brief.RequiredEditPaths...)
	editPaths = append(editPaths, mapKeys(changedPathsFromToolResults(allResults))...)
	editScore := 0.0
	for _, path := range editPaths {
		normalized := normalizeRunnerPath(path)
		if normalized != "" && strings.Contains(content, strings.ToLower(normalized)) {
			editScore += 0.25
			break
		}
	}
	if editScore > 0.50 {
		editScore = 0.50
	}
	score += editScore
	evidenceScore := 0.0
	for _, path := range brief.EvidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized != "" && strings.Contains(content, strings.ToLower(normalized)) {
			evidenceScore += 0.15
			break
		}
	}
	if evidenceScore > 0.30 {
		evidenceScore = 0.30
	}
	score += evidenceScore
	if score > 1.0 {
		score = 1.0
	}
	return score
}

func tailToolResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}
	return results[len(results)-max:]
}

func latestUserObjective(messages []llama.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}
		if content := strings.TrimSpace(messages[i].Content); content != "" {
			if scoped := strings.TrimSpace(pathparse.ScopeToBriefBody(content)); scoped != "" {
				content = scoped
			}
			return truncateRunnerStateText(content, 500)
		}
	}
	return ""
}

func pinnedPathsFromToolResults(results []workspace.ToolResult, max int) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, result := range results {
		for _, field := range []string{result.Summary, result.Output, result.Error} {
			for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(field)) {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}
	paths = prioritizedClosureEvidencePaths(paths, results, len(paths))
	return limitRunnerStrings(paths, max)
}

func directProbePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	seen := make(map[string]struct{}, max)
	var paths []string
	for _, result := range results {
		if !concreteSourceEvidenceResult(result) {
			continue
		}
		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(result.Summary)) {
			path = normalizeDelegatedDiscoveryEvidencePath(path)
			if path == "" || isNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
			if len(paths) >= max {
				return paths
			}
		}
	}
	return paths
}

func promptEvidencePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	paths := make([]string, 0, max*2)
	directPaths := directProbePathsFromToolResults(results, max)
	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		paths = append(paths, packet.SourceBodyPaths...)
		paths = append(paths, packet.InspectedPaths...)
	}
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*6)...)
	paths = prioritizedClosureEvidencePaths(paths, results, max*3)
	return limitRunnerStrings(uniqueDelegatedDiscoveryPaths(append(directPaths, closurePromptEvidencePaths(paths, max*2)...)), max)
}

func runnerEvidencePathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
		}
		return promptEvidencePathsFromToolResults(results, max)
	}
	scope := mutationEvidenceScope(brief, results)
	paths := changedPathListFromToolResults(results)
	for _, path := range directProbePathsFromToolResults(results, max*2) {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}
		paths = append(paths, normalized)
	}
	for _, path := range brief.EvidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}
		paths = append(paths, normalized)
	}
	return closurePromptEvidencePathsPreservingTests(paths, max)
}

func readOnlyPresentationEvidencePathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if readOnlyDirectAnswerLane(brief) {
		return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
	}
	return promptEvidencePathsFromToolResults(results, max)
}

func readOnlyDirectAnswerPresentationEvidencePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
	paths := append([]string{}, ledger.RelevantSourceBodyPaths...)
	if objectiveAsksImplementationShape(ledger.Objective) {
		paths = mergeRunnerStateStrings(paths, ledger.ImplementationOwnerPaths())
	}
	if ledger.DiagnosticRelevant {
		paths = mergeRunnerStateStrings(paths, readOnlySourceLikePaths(ledger.DiagnosticPaths, max*2))
	}
	return closurePromptEvidencePaths(paths, max)
}

func runnerPinnedPathsForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	if !runnerBriefRequiresMutation(brief) {
		if readOnlyDirectAnswerLane(brief) {
			return readOnlyDirectAnswerPresentationEvidencePaths(brief, results, max)
		}
		return promptEvidencePathsFromToolResults(results, max)
	}
	paths := append([]string{}, brief.RequiredEditPaths...)
	paths = append(paths, mapKeys(changedPathsFromToolResults(results))...)
	paths = append(paths, runnerEvidencePathsForBrief(brief, results, max*2)...)
	return closurePromptEvidencePathsPreservingTests(paths, max)
}

func mutationEvidenceScope(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]bool {
	scope := map[string]bool{}
	for _, values := range [][]string{
		brief.RequiredEditPaths,
		brief.TargetRefs,
		brief.ChangedPaths,
		brief.EvidencePaths,
		mapKeys(changedPathsFromToolResults(results)),
	} {
		for _, raw := range values {
			path := normalizeRunnerPath(raw)
			if path != "" {
				scope[path] = true
			}
		}
	}
	return scope
}

func postChildClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max)
	acceptedPacket := false
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			packetAnswerable := readOnlyEvidencePacketAnswerableForObjective(packet, objective)
			if strings.TrimSpace(objective) == "" {
				packetAnswerable = readOnlyEvidencePacketAnswerable(packet)
			}
			if !packetAnswerable {
				continue
			}
			acceptedPacket = true
			paths = append(paths, packet.SourceBodyPaths...)
			paths = append(paths, packet.InspectedPaths...)
			paths = append(paths, packet.CandidatePaths...)
			continue
		}
		sourcePaths := sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
		if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
			sourcePaths = groundedChildPacketEvidencePaths(objective, result.Output)
		}
		if len(sourcePaths) == 0 {
			continue
		}
		if !childPacketContainsGroundedAnswerEvidence(objective, result.Output) &&
			!delegatedPacketAddressesObjective(objective, result.Output) {
			continue
		}
		acceptedPacket = true
		paths = append(paths, sourcePaths...)
	}
	if acceptedPacket {
		paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	}
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)
	return closurePromptEvidencePaths(paths, max)
}

func childPacketBoundaryNudge(objective string, results []workspace.ToolResult, evidencePaths []string, probeBudget int) string {
	evidencePaths = closurePromptEvidencePaths(evidencePaths, 4)
	validity, _ := closureDecisionRationale(objective, results, evidencePaths)
	lines := []string{
		"The delegated read_only child result is now your evidence packet.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(evidencePaths) > 0 {
		lines = append(lines, "Packet-named paths: "+summarizePostChildEvidencePaths(evidencePaths, 3))
	}
	if suggestion := closureProbeSuggestionLine(results, 3); suggestion != "" {
		lines = append(lines, suggestion)
	}
	lines = append(lines, postReadinessContinuationContract("child_packet_available", probeBudget))
	lines = append(lines,
		"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
		"Answer now from the packet.",
	)
	return strings.Join(lines, "\n")
}

type postReadinessToolContract struct {
	RequireSourceRefreshContract bool
	ReadinessAdvisory            bool
	AnswerOnly                   bool
	Reason                       string
}

func postReadinessToolContractForBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) postReadinessToolContract {
	if writeReadyPacketReadContractActive(brief, results) || implementationScopedEditSurfaceActive(brief, results) {
		return postReadinessToolContract{
			RequireSourceRefreshContract: true,
			Reason:                       "mutation_source_refresh_contract",
		}
	}
	if delegatedReadOnlyAnswerReady || postChildPacketActive {
		if postReadinessReadOnlyAnswerOnlyContractApplies(brief, results, postChildPacketActive) {
			return postReadinessToolContract{
				AnswerOnly: true,
				Reason:     "read_only_answerable_evidence_contract",
			}
		}
		return postReadinessToolContract{
			ReadinessAdvisory: true,
			Reason:            "read_only_readiness_advisory",
		}
	}
	return postReadinessToolContract{Reason: "none"}
}

func postReadinessReadOnlyAnswerOnlyContractApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, postChildPacketActive bool) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}
	if postChildPacketActive && postChildPacketClosureReadyWithBrief(brief, results) {
		return true
	}
	if workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDelegatedReadOnly) &&
		workerruntime.RuntimeContractMatches(brief.Contract, workerruntime.ContractGroundedEvidence) {
		closure := readOnlyClosureDecisionForResults(brief, results)
		if !closure.Answerable() {
			return false
		}
		_, ok := closure.HostOwnedAnswer("child:delegated-read-only")
		return ok
	}
	if !workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) {
		return false
	}
	closure := readOnlyClosureDecisionForResults(brief, results)
	if !closure.Answerable() {
		return false
	}
	return readOnlyAnalysisHasObjectiveRelevantSourceBody(brief, closure.Evidence)
}

func postReadinessGap(req workspace.ToolRequest) string {
	return util.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering)
}

func delegatedReadOnlyAnswerReadyNudge(objective string, results []workspace.ToolResult, probeBudget int) string {
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"Answer now from the evidence above. The delegated read-only evidence is sufficient for this question.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}
	lines = append(lines, postReadinessContinuationContract("delegated_read_only_answer_ready", probeBudget))
	lines = append(lines,
		"Carry forward the inspected paths above and keep the answer minimal; avoid side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
	)
	return strings.Join(lines, "\n")
}

func readOnlyAnswerReadyNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, probeBudget int) string {
	objective := strings.TrimSpace(brief.Objective)
	paths := closurePromptEvidencePaths(readOnlyPresentationEvidencePathsForBrief(brief, results, 4), 4)
	validity, _ := closureDecisionRationale(objective, results, paths)
	lines := []string{
		"Answer now from the evidence above. The read-only evidence is sufficient for this question.",
	}
	if validity != "" {
		lines = append(lines, "Why answer now is valid: "+validity)
	}
	if len(paths) > 0 {
		lines = append(lines, "Core evidence paths: "+summarizePostChildEvidencePaths(paths, 3))
	}
	lines = append(lines, postReadinessContinuationContract("answer_readiness_reached", probeBudget))
	lines = append(lines,
		"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit.",
	)
	return strings.Join(lines, "\n")
}

func postReadinessProbeBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) {
		return 1
	}
	return delegatedReadOnlyProbeBudget
}

// postReadinessContinuationContract returns advisory closeout guidance after
// readiness or a child packet. It must not describe read-only readiness as a
// permission gate; explicit source-refresh gates are selected by
// postReadinessToolContractForBrief.
func postReadinessContinuationContract(scope string, budget int) string {
	anchor := "prefer a symbol, branch, body, or narrow line-range probe over file-level raw reads"
	if strings.TrimSpace(scope) == "child_packet_available" {
		anchor = "anchor to a packet-named path or symbol with a symbol, branch, body, or narrow line-range probe"
	} else if strings.TrimSpace(scope) == "answer_readiness_reached" {
		anchor = "target a symbol, call/reference, branch, body, or narrow line-range probe tied to inspected paths"
	}
	probeBudgetLine := "one targeted probe"
	switch {
	case budget <= 0:
		probeBudgetLine = "answer unless the missing claim is exact and material"
	case budget == 1:
	default:
		probeBudgetLine = fmt.Sprintf("at most %d targeted probes", budget)
	}
	lines := []string{
		"Answer now unless one exact unresolved claim remains.",
		"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy; " + anchor + "; " + probeBudgetLine + " before answering.",
		"The follow-up should target a code-shaped token (symbol name, file path, or narrow line range) and should not paraphrase the original question.",
	}
	return strings.Join(lines, "\n")
}

func delegatedReadOnlyDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}
	// The readiness nudge already disclosed the advisory continuation shape.
	// Repeating that prose burns context for no behavior change. Emit a
	// factual probe-budget note instead and let the model decide.
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 3)
	line := fmt.Sprintf("Post-readiness probes used: %d. You have already used the post-readiness follow-up budget disclosed for this lane. Answer now from the evidence above; do not spend another tool call for completeness.", followups)
	if len(paths) > 0 {
		line += " Inspected paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}
	return line
}

func closureProbeSuggestionLine(results []workspace.ToolResult, max int) string {
	candidates := closureProbeCandidates(results, max)
	if len(candidates) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		quoted = append(quoted, "`"+candidate+"`")
	}
	return "Evidence-grounded exact probes to prefer: " + strings.Join(quoted, ", ") + "."
}

func closureProbeCandidates(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 3
	}
	candidates := make([]string, 0, max)
	add := func(raw string) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw); ok {
			candidates = util.AppendUnique(candidates, symbol)
		}
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathLineSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[2])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
			for _, line := range strings.Split(text, "\n") {
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) > 1 {
						add(match[1])
						if len(candidates) >= max {
							return candidates[:max]
						}
					}
				}
			}
			for _, match := range goFuncDeclPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 1 {
					add(match[1])
					if len(candidates) >= max {
						return candidates[:max]
					}
				}
			}
		}
	}
	return candidates
}

func closureProbeCandidatesByPath(results []workspace.ToolResult, maxPerPath int) map[string][]string {
	if maxPerPath <= 0 {
		maxPerPath = 3
	}
	byPath := make(map[string][]string)
	add := func(path string, raw string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			return
		}
		symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
		if !ok {
			return
		}
		current := byPath[path]
		if containsExactProbeCandidate(current, symbol) {
			return
		}
		if len(current) >= maxPerPath {
			return
		}
		byPath[path] = append(current, symbol)
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, field := range []string{result.Output, result.Summary} {
			text := normalizeWorkspacePathScanText(field)
			for _, match := range closurePathScopedSymbolPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}
			for _, match := range closurePathCallPattern.FindAllStringSubmatch(text, -1) {
				if len(match) > 2 {
					add(match[1], match[2])
				}
			}
			for _, line := range strings.Split(text, "\n") {
				paths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
				if len(paths) == 0 {
					continue
				}
				for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
					if len(match) <= 1 {
						continue
					}
					for _, path := range paths {
						add(path, match[1])
					}
				}
			}
		}
	}
	return byPath
}

func containsExactProbeCandidate(candidates []string, pattern string) bool {
	pattern, ok := delegatedReadOnlyLiteralSymbolCandidate(pattern)
	if !ok {
		pattern = strings.TrimSpace(pattern)
	}
	pattern = strings.ToLower(pattern)
	for _, candidate := range candidates {
		if strings.ToLower(strings.TrimSpace(candidate)) == pattern {
			return true
		}
	}
	return false
}

func quotedRunnerStrings(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

func postChildDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}
	// The child-packet boundary nudge already disclosed the continuation
	// contract; this is a factual probe-budget note, not another prose
	// block telling the model what was already said.
	paths := postChildClosureEvidencePaths(objective, results, 3)
	line := fmt.Sprintf("Post-child probes used: %d. You have already used the post-child follow-up budget disclosed for this lane. Answer now from the packet above; do not spend another tool call for completeness.", followups)
	if len(paths) > 0 {
		line += " Packet paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}
	return line
}

func delegatedClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max*2)
	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		paths = append(paths, packet.SourceBodyPaths...)
		paths = append(paths, packet.InspectedPaths...)
		paths = append(paths, packet.CandidatePaths...)
	}
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)
	return closurePromptEvidencePaths(paths, max)
}

func closureDecisionRationale(objective string, results []workspace.ToolResult, fallbackPaths []string) (string, string) {
	paths := closurePromptEvidencePaths(fallbackPaths, 4)
	if len(paths) == 0 {
		paths = delegatedClosureEvidencePaths(objective, results, 4)
	}
	switch {
	case len(paths) > 0 && closureHasExactProbeEvidence(results):
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3) + " and includes at least one exact inspected probe", "another follow-up is only likely to help if it changes one named claim; otherwise it will mostly restate adjacent detail"
	case len(paths) > 0:
		return "the current evidence already names inspected source paths " + summarizePostChildEvidencePaths(paths, 3), "another follow-up is only likely to help if it closes one named gap; otherwise additional reads mostly restate nearby files"
	default:
		return "the current evidence already covers the named scope well enough to support a grounded answer", "another follow-up is only likely to help if it closes one named gap"
	}
}

func closureHasExactProbeEvidence(results []workspace.ToolResult) bool {
	return len(closureProbeCandidates(results, 1)) > 0
}

func applyClosurePromptRefinement(state runnerNextActionState) runnerNextActionState {
	scope := ""
	switch {
	case slices.Contains(state.Constraints, "child_packet_available"):
		scope = "child_packet_available"
	case slices.Contains(state.Constraints, "answer_readiness_reached"):
		scope = "answer_readiness_reached"
	default:
		return state
	}
	state.NextValidActions = prependRunnerStateStrings(closurePromptNextActions(scope), filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, "search_files"),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "exact text search"),
			strings.Contains(lower, "narrow to one exact term or scope"),
			strings.Contains(lower, "continue from the latest evidence with a targeted tool"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "produce the missing evidence or edit required by the gate"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "gather or edit the missing evidence"),
			strings.Contains(lower, "edit_file"),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, "go_move_declarations"),
			strings.Contains(lower, "read_file"),
			strings.Contains(lower, "search_content"),
			strings.Contains(lower, "list_files"),
			strings.Contains(lower, "inspect"),
			strings.Contains(lower, "outline"),
			strings.Contains(lower, "workspace_symbol"):
			return true
		default:
			return false
		}
	}))
	state.InvalidNextActions = mergeRunnerStateStrings(state.InvalidNextActions, closurePromptInvalidActions(scope))
	return state
}

func closurePromptNextActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"answer directly from the child packet if it already resolves the question",
		}
	default:
		return []string{
			"answer directly from the evidence already gathered",
		}
	}
}

func closurePromptInvalidActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"broad rediscovery outside packet-named paths or symbols after the child packet",
			"README/docs or repo-wide search outside packet-named paths or symbols after the child packet",
		}
	default:
		return []string{
			"broad rediscovery after answer readiness without one exact missing claim and one exact probe",
			"README/docs or repo-wide search outside named implementation paths after answer readiness",
		}
	}
}

func (r *Runner) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) (workspace.ToolResult, bool) {
	if result, rejected := r.completionGatePipeline(step, taskLedger, declarationMovesForbidden).Evaluate(reply, results); rejected {
		return result, true
	}
	if r.CompletionGate == nil {
		return workspace.ToolResult{}, false
	}
	gate := r.CompletionGate(CompletionGateInput{
		AgentName: r.agentName(),
		Reply:     reply,
		Results:   append([]workspace.ToolResult(nil), results...),
		Steps:     step,
	})
	if gate.Accept {
		return workspace.ToolResult{}, false
	}
	summary := strings.TrimSpace(gate.Summary)
	if summary == "" {
		summary = "Final answer rejected"
	}
	errorText := strings.TrimSpace(gate.Error)
	if errorText == "" {
		errorText = "the answer does not satisfy the task completion gate; continue with the next tool call or state a concrete blocker"
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: summary,
		Error:   errorText,
	}, true
}

func rawToolResultTranscriptCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeRawToolResultTranscript(reply) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Raw tool result transcript rejected as final answer",
		Error:   "the reply is an internal tool-result transcript, not a user-facing answer. Synthesize the answer from the gathered evidence, or state the concrete blocker/no-op reason; do not copy tool/status/output fields.",
	}, true
}

func (r *Runner) readOnlyEvidenceCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}
	if brief.Readiness != nil && brief.Readiness.ReadyToAnswer {
		return workspace.ToolResult{}, false
	}
	directCandidateOnlyLedger, directCandidateOnly := readOnlyDirectAnswerCandidateOnlyLedger(brief, results)
	if brief.Readiness == nil &&
		!readOnlyExplanationGateApplies(brief) &&
		!workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDirectAnswer) &&
		!directCandidateOnly {
		return workspace.ToolResult{}, false
	}
	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 {
		return workspace.ToolResult{}, false
	}
	if strings.HasPrefix(strings.TrimSpace(reply), "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}
	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerReportsInsufficientEvidence(reply) {
		ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		if len(ledger.SourceBodyPaths) > 0 || ledger.DiagnosticEvidence {
			return workspace.ToolResult{}, false
		}
	}
	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerHasConcreteEvidence(results) {
		if acceptance := r.evaluateFinalAnswerAcceptance(reply, results); acceptance.BlockerKind == "" {
			return workspace.ToolResult{}, false
		}
	}
	if directCandidateOnly {
		candidates := closurePromptEvidencePaths(directCandidateOnlyLedger.CandidatePaths, 4)
		if len(candidates) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Read-only completion gate rejected final answer",
				Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Candidate/search/context paths are not source-body evidence. Candidate path(s): " + strings.Join(candidates, ", ") + ". Inspect one candidate with source-body evidence, use one materially different source-owner probe if these candidates do not establish the claim, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
			}, true
		}
	}
	if genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}
	if readPaths := sortedRunnerPathKeys(readPathsFromResults(results)); len(readPaths) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Already inspected path(s) should not be reread: " + strings.Join(readPaths, ", ") + ". Use one targeted source search/read/navigation request for a different concrete code path tied to the explicit question, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
		}, true
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected final answer",
		Error:   "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Use one targeted read/search/discovery tool for the explicit question, or answer `<<QUESTION>>:` with the exact unavailable evidence source.",
	}, true
}

func readOnlyAnswerHasConcreteEvidence(results []workspace.ToolResult) bool {
	for _, result := range results {
		if concreteSourceEvidenceResult(result) || readOnlyDiagnosticEvidenceResult(result) {
			return true
		}
	}
	return false
}

func (r *Runner) readOnlyNonSubstantiveCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}
	if !genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}
	if len(strings.Fields(trimmed)) > 2 {
		return workspace.ToolResult{}, false
	}
	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected non-substantive final answer",
		Error:   "read-only workspace evidence has been gathered, but the reply is not a substantive user-facing answer. Answer directly from the gathered evidence, carrying forward the inspected path and the concrete value or fact needed by the question.",
	}, true
}

func unresolvedProcessIntentCompletionRejection(reply string) (workspace.ToolResult, bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return workspace.ToolResult{}, false
	}
	inspect := stripQuotedEvidenceForProcessIntent(trimmed)
	if strings.TrimSpace(inspect) == "" {
		return workspace.ToolResult{}, false
	}
	lower := " " + strings.ToLower(strings.Join(strings.Fields(inspect), " ")) + " "
	if containsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	) {
		return workspace.ToolResult{}, false
	}
	if !containsAny(lower,
		" let me search ", " let me inspect ", " let me read ", " let me refresh ", " let me run ", " let me check ", " let me audit ", " let me investigate ", " let me explore ", " let me look ",
		" let me call ", " let me use ",
		" i need to search ", " i need to inspect ", " i need to find ", " i need to read ", " i need to refresh ", " i need to run ", " i need to check ", " i need to audit ", " i need to investigate ", " i need to explore ", " i need to look ",
		" i need to call ", " i need to use ",
		" i should search ", " i should inspect ", " i should find ", " i should read ", " i should refresh ", " i should run ", " i should check ", " i should audit ", " i should investigate ", " i should explore ", " i should look ",
		" i should call ", " i should use ",
		" i'll search ", " i'll inspect ", " i'll read ", " i'll refresh ", " i'll run ", " i'll check ", " i'll audit ", " i'll investigate ", " i'll explore ", " i'll look ",
		" i'll call ", " i'll use ",
		" i will search ", " i will inspect ", " i will read ", " i will refresh ", " i will run ", " i will check ", " i will audit ", " i will investigate ", " i will explore ", " i will look ",
		" i will call ", " i will use ",
		" i'm going to search ", " i'm going to inspect ", " i'm going to read ", " i'm going to run ", " i'm going to check ", " i'm going to audit ", " i'm going to investigate ", " i'm going to explore ", " i'm going to look ",
		" i'm going to call ", " i'm going to use ",
		" i'll start by ", " i will start by ",
		" i'll ",
		" i will ",
	) {
		return workspace.ToolResult{}, false
	}
	if containsAny(lower, " i'll ", " i will ") && !containsAny(lower,
		" search ", " inspect ", " read ", " refresh ", " run ", " check ", " verify ",
	) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Completion gate rejected unresolved process narration",
		Error:   "the reply describes an intended future search/read/inspection instead of completing the answer. Issue the needed tool call now, answer from gathered evidence, or state a concrete blocker.",
	}, true
}

func stripQuotedEvidenceForProcessIntent(text string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// looksLikeToolResultTranscript returns true when the reply looks like
// a raw tool result transcript (e.g. "The inspected source in X contains
// func Foo, if bar != nil, and return baz"). Catches the pattern where
// the model dumps raw function signatures instead of synthesizing prose.
func looksLikeToolResultTranscript(lower string) bool {
	// "The inspected source in `path` contains `code`"
	if strings.Contains(lower, "the inspected source in") &&
		strings.Contains(lower, " contains `") {
		return true
	}
	if looksLikePathShowsSourceInventory(lower) {
		return true
	}
	if looksLikeInspectedBodySourceInventory(lower) {
		return true
	}
	if looksLikeLineOrientedSourceSnippetAnswer(lower) {
		return true
	}
	// " contains `f" where f is any function/symbol name
	if strings.Contains(lower, " contains `func ") ||
		strings.Contains(lower, " contains `type ") ||
		strings.Contains(lower, " contains `var ") ||
		strings.Contains(lower, " contains `const ") {
		return true
	}
	return false
}

func (r *Runner) readOnlySearchHitInventoryCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}
	// A tool-result transcript answer is garbage regardless of
	// whether the evidence gate declares evidence ready. Check format
	// first, evidence status second.
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}
	lower := strings.ToLower(trimmed)
	if !looksLikeToolResultTranscript(lower) && !toolLocalSearchHandlePattern.MatchString(trimmed) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Read-only completion gate rejected search-hit inventory",
		Error:   "the reply inventories search handles or raw hit lines instead of answering the user's question. Synthesize the answer from the inspected evidence; do not expose tool-local handles or search-result scaffolding.",
	}, true
}

func (r *Runner) unexecutedToolRequestCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Unexecuted tool request rejected as final answer",
		Error: strings.Join([]string{
			"the reply is a workspace-tool request payload, not a final answer.",
			"If a workspace lookup is needed, issue it as a parser-readable tool call with the `tool` field and wait for the result.",
			"If no lookup is needed, answer the user in normal prose.",
			"Do not stop with raw JSON.",
		}, " "),
	}, true
}

func looksLikeUnexecutedToolRequestAnswer(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}
	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return false
	}
	if markdownFenceOpenBefore(trimmed, start) {
		return false
	}
	outside := strings.TrimSpace(trimmed[:start] + "\n" + trimmed[end:])
	outside = strings.Trim(outside, "` \t\r\n:")
	if outside == "" {
		return true
	}
	if toolRequestJSONObjectAppearsAsStandaloneBlock(trimmed, start, end) {
		return true
	}
	fields := strings.Fields(outside)
	if len(fields) > 24 {
		return false
	}
	lower := " " + strings.ToLower(outside) + " "
	return util.ContainsAny(lower,
		" let me ",
		" i need ",
		" i will ",
		" i'll ",
		" start by ",
		" first ",
		" understanding ",
		" locating ",
		" inspect ",
		" checking ",
		" call ",
		" calling ",
		" use ",
	)
}

func toolRequestJSONObjectAppearsAsStandaloneBlock(text string, start int, end int) bool {
	if markdownFenceOpenBefore(text, start) {
		return false
	}
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	if strings.TrimSpace(text[lineStart:start]) != "" {
		return false
	}
	lineEnd := len(text)
	if nextNewline := strings.IndexByte(text[end:], '\n'); nextNewline >= 0 {
		lineEnd = end + nextNewline
	}
	return strings.TrimSpace(text[end:lineEnd]) == ""
}

func markdownFenceOpenBefore(text string, pos int) bool {
	open := false
	for _, line := range strings.Split(text[:pos], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			open = !open
		}
	}
	return open
}

func firstToolRequestShapedJSONObjectSpan(text string) (int, int, bool) {
	for searchFrom := 0; searchFrom < len(text); {
		relativeStart := strings.IndexByte(text[searchFrom:], '{')
		if relativeStart < 0 {
			return 0, 0, false
		}
		start := searchFrom + relativeStart
		object, end, ok := extractJSONObjectAt(text, start)
		if !ok {
			return 0, 0, false
		}
		if markdownFenceOpenBefore(text, start) {
			searchFrom = end
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(object), &fields); err == nil && toolRequestShapedJSONFields(fields) {
			return start, end, true
		}
		searchFrom = start + 1
	}
	return 0, 0, false
}

func extractJSONObjectAt(text string, start int) (string, int, bool) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return "", 0, false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '{':
			depth++
		case !inString && c == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1]), i + 1, true
			}
		}
	}
	return "", 0, false
}

func toolRequestShapedJSONFields(fields map[string]json.RawMessage) bool {
	has := func(keys ...string) bool {
		for _, key := range keys {
			if _, ok := fields[key]; ok {
				return true
			}
		}
		return false
	}
	if has("tool") {
		return true
	}
	if has("edits") && has("rationale") {
		return true
	}
	if has("plan_artifact") {
		return true
	}
	if has("task") && has("mode", "limit", "budget_tokens", "required_tests", "required_edit_paths", "forbidden_paths", "scope", "path") {
		return true
	}
	if has("query") && has("resolution", "limit", "mode", "path", "language") {
		return true
	}
	if has("command") && has("workdir", "timeout_ms", "yield_time_ms", "sandbox_permissions", "justification") {
		return true
	}
	if has("agent") && has("message", "task", "items") {
		return true
	}
	if has("todos") && has("task_id", "items") {
		return true
	}
	if has("path") && has("old_text", "new_text", "replace_all") {
		return true
	}
	if has("capability") && has("path", "pattern", "query", "language") {
		return true
	}
	if has("path") && has("start_line", "end_line", "pattern", "glob", "language", "content", "recursive", "include_source", "include_files", "refresh", "freshness_policy", "handle", "symbol") {
		return true
	}
	if has("pattern") && has("path", "glob") {
		return true
	}
	return false
}

func syntheticLegacyToolCall(step int, req workspace.ToolRequest, raw string) llama.ToolCall {
	name := strings.TrimSpace(req.Tool)
	if name == "" {
		name = "workspace_tool"
	}
	args := strings.TrimSpace(raw)
	if args == "" {
		args = marshalToolRequestRaw(req)
	}
	return llama.ToolCall{
		ID:   fmt.Sprintf("idiom_legacy_tool_%d", step),
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      name,
			Arguments: args,
		},
	}
}

func syntheticLegacyParseToolCall(step int, raw string) llama.ToolCall {
	args := "{}"
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if data, err := json.Marshal(map[string]string{"raw": truncateRunnerStateText(trimmed, 1000)}); err == nil {
			args = string(data)
		}
	}
	return llama.ToolCall{
		ID:   fmt.Sprintf("idiom_legacy_tool_%d", step),
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "tool_parse",
			Arguments: args,
		},
	}
}

func legacyToolCallHistoryMessage(reply llama.ChatMessage, call llama.ToolCall) llama.ChatMessage {
	return llama.ChatMessage{
		Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		ReasoningContent: reply.ReasoningContent,
		ToolCalls:        []llama.ToolCall{call},
	}
}

func textualToolRequestHistoryMessage(reply llama.ChatMessage, req workspace.ToolRequest, raw string, opts llama.ChatOptions) llama.ChatMessage {
	content := strings.TrimSpace(raw)
	if content == "" || !strings.Contains(content, `"tool"`) {
		content = marshalToolRequestRaw(req)
	}
	content = strings.TrimSpace(content)
	message := llama.ChatMessage{
		Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          content,
		ReasoningContent: reply.ReasoningContent,
	}
	return assistantHistoryMessageForOptions(message, opts)
}

func (r *Runner) prunePlanThenExecuteTerminalSurfaceHistory(messages *[]llama.ChatMessage, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	if droppedActions == 0 && droppedScaffolding == 0 {
		return
	}
	*messages = pruned
	r.emitEvent(marshalRunEvent("plan_then_execute_terminal_surface_history_pruned", map[string]any{
		"reason":                      strings.TrimSpace(reason),
		"dropped_tool_requests":       droppedActions,
		"dropped_surface_scaffolding": droppedScaffolding,
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalToolSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}
	if strings.TrimSpace(reason) == "source_satisfied_terminal_required" {
		r.refreshPlanThenExecuteTerminalOutcomeSurface(messages, results, reason)
		return
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		r.prunePlanThenExecuteTerminalSurfaceHistory(messages, reason)
		return
	}
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      []string{"submit_plan"},
		ToolManifest:      runnerSanitizedToolManifest([]string{"submit_plan"}),
		RequiredTools:     []string{"submit_plan"},
		ReadinessGate:     append([]string(nil), terminalBrief.ReadinessGate...),
		SearchPolicy:      append([]string(nil), terminalBrief.SearchPolicy...),
		StopConditions:    append([]string(nil), terminalBrief.StopConditions...),
		FinalCritic:       append([]string(nil), terminalBrief.FinalCritic...),
		NextActions:       append([]string(nil), terminalBrief.RequiredNext...),
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	transport := "textual"
	surfaceTitle := "# Terminal Plan-Execute Textual Tool Surface"
	nativeTerminal := r.supportsTerminalSubmitPlanNativeTools(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	compactDropped := 0
	compactEvidenceMessages := 0
	if !nativeTerminal {
		pruned, compactDropped, compactEvidenceMessages = compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	}
	actionInstruction := r.textualToolProtocolPrompt(terminalBrief)
	submitPlanSkeleton := planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, nativeTerminal)
	renderedState := workerruntime.RenderRunState(terminalState)
	if nativeTerminal {
		transport = "native"
		surfaceTitle = "# Terminal Plan-Execute Native Tool Surface"
		terminalState.ReadinessGate = []string{"native_submit_plan_tool_call_required"}
		terminalState.StopConditions = []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"}
		terminalState.FinalCritic = []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		terminalState.NextActions = []string{"call native submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		renderedState = workerruntime.RenderStepInstruction(workerruntime.StepInstruction{
			CurrentState: terminalState,
			Directive:    "Use this current-state packet for the next action. Plan-execute source obligations are closed and this request exposes exactly one native tool: submit_plan. Call native submit_plan now. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not read, search, shell, verify, use per-edit write tools, or answer in prose.",
		})
		actionInstruction = "Native `submit_plan` is the only exposed tool in this request. Use the `submit_plan` tool call now; do not answer in prose."
	}
	terminalPrompt := strings.Join([]string{
		surfaceTitle,
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `read_file`, `code_navigate`, search, shell, source-sanity, and per-edit write examples are stale because source obligations are closed.",
		"",
		renderedState,
		"",
		submitPlanSkeleton,
		"",
		actionInstruction,
	}, "\n")
	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     transport,
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 []string{"submit_plan"},
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalOutcomeSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_terminal_outcome",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      nil,
		ToolManifest:      nil,
		RequiredTools:     nil,
		ReadinessGate:     []string{"terminal_outcome_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness"},
		StopConditions:    []string{"decision_source_satisfied", "textual_submit_plan_returned_ok", "question_blocker_reported"},
		FinalCritic:       []string{"one_terminal_outcome_only", "per_path_current_state_evidence_for_source_satisfied"},
		NextActions: []string{
			"answer `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state",
			"or emit one visible textual submit_plan JSON object with real executable edits if changes are still needed",
			"or answer `<<QUESTION>>:` with the exact path-specific blocker",
		},
		InvalidActions: []string{
			"empty or source-satisfied submit_plan transport",
			"read/search/discovery after terminal readiness",
			"verification/build/test in planner phase",
			"per-edit write in planner phase",
			"prose-only answer without Decision/source evidence or blocker",
		},
		Readiness: terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Outcome Surface",
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `submit_plan`-only recovery text is stale because the previous `submit_plan` attempted a source-satisfied closeout through the wrong transport.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		"Next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state.",
		"2. One visible textual `submit_plan` JSON object with real executable edits if changes are still needed. " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty or source-satisfied `submit_plan` object. Do not call read/search/discovery, shell, verification, or per-edit write tools.",
	}, "\n")
	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "terminal_outcome",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 []string{},
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalTextualFallbackSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil || !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		return
	}
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready_textual_fallback",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      []string{"submit_plan"},
		ToolManifest:      runnerSanitizedToolManifest([]string{"submit_plan"}),
		RequiredTools:     []string{"submit_plan"},
		ReadinessGate:     []string{"visible_textual_submit_plan_json_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"},
		StopConditions:    []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"},
		FinalCritic:       []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"},
		NextActions:       []string{"emit exactly one visible submit_plan JSON object using edits[] for changed paths and source_satisfied_paths for already-final paths"},
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"",
		"Native tool-call transport failed to produce a required `submit_plan` call. Native tools are disabled for this retry; the only accepted action is one visible textual `submit_plan` JSON object.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, false),
		"",
		r.textualToolProtocolPrompt(terminalBrief),
	}, "\n")
	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual_fallback",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 []string{"submit_plan"},
	}))
}

func (r *Runner) refreshPlanThenExecuteAnchorRefreshSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}
	stateProjection := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if stateProjection.Mode != PlanExecutePlanningModeAnchorRefresh || len(stateProjection.RefreshPaths) == 0 {
		return
	}
	refreshPaths := stateProjection.RefreshPaths
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	state := workerruntime.RunState{
		Lane:              util.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		TaskFamily:        brief.TaskFamily,
		Contract:          brief.Contract,
		Phase:             "plan_submit_anchor_refresh_required",
		Objective:         brief.Objective,
		Permission:        brief.PermissionProfile,
		TargetRefs:        append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), brief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
		Constraints:       append([]string(nil), brief.Constraints...),
		AllowedTools:      planThenExecuteAnchorRefreshToolNames(),
		ToolManifest:      runnerSanitizedToolManifest(planThenExecuteAnchorRefreshToolNames()),
		RequiredTools:     planThenExecuteAnchorRefreshToolNames(),
		ReadinessGate:     []string{"bounded_anchor_refresh_before_corrected_submit_plan"},
		SearchPolicy:      append([]string(nil), brief.SearchPolicy...),
		StopConditions:    []string{"bounded_source_refresh_returned"},
		FinalCritic:       []string{"refresh_targets_rejected_anchor_path", "no unrelated discovery"},
		NextActions:       append([]string(nil), brief.RequiredNext...),
		InvalidActions:    append([]string(nil), brief.InvalidActions...),
		Readiness:         brief.Readiness,
	}
	targetText := "the stale or missing required edit path named by the submit_plan rejection"
	if len(refreshPaths) > 0 {
		targetText = strings.Join(refreshPaths, ", ")
	}
	prompt := strings.Join([]string{
		"# Plan-Execute Anchor Refresh Surface",
		"",
		"A previous `submit_plan` was rejected because one or more edit anchors were stale or missing. This is a source-refresh phase, not a submit-plan phase.",
		"",
		workerruntime.RenderRunState(state),
		"",
		"Refresh only: " + targetText + ".",
		"Use exactly one bounded source-body request: `" + workspace.CodeNavigateToolName + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " for the relevant declaration, or `read_file` with explicit `start_line`/`end_line` only when that exact range comes from current source evidence.",
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not emit `submit_plan` until this bounded refresh returns.",
		"After the refresh, use anchors copied from the refreshed source. Prefer `insert_after_line` plus `content` for insertions/appends; use `old_text` only when copied exactly from fresh source.",
	}, "\n")
	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(prompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_anchor_refresh_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"allowed_tools":                 planThenExecuteAnchorRefreshToolNames(),
		"refresh_paths":                 refreshPaths,
	}))
}

func (r *Runner) planThenExecuteTerminalToolBrief(results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}
	brief.AllowedTools = []string{"submit_plan"}
	brief.RequiredTools = []string{"submit_plan"}
	brief.ForbiddenTools = mergeRunnerStateStrings(brief.ForbiddenTools,
		[]string{"read_file", "code_navigate", "search_files", "search_content", "list_files", "shell", "source_sanity", "edit_file", "write_file", "append_file", "apply_patch"},
	)
	brief.ToolManifest = runnerSanitizedToolManifest([]string{"submit_plan"})
	brief.Constraints = mergeRunnerStateStrings(brief.Constraints, []string{"plan_then_execute_submit_plan_ready", "required_edit_paths_define_write_scope", "planner_owned_verification"})
	brief.SearchPolicy = []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"}
	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		brief.Constraints = mergeRunnerStateStrings(brief.Constraints, []string{"plan_then_execute_submit_correction_only"})
		brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		brief.InvalidActions = []string{"read/search/source-refresh after submit_plan correction is ready", "Decision: source-satisfied after an incomplete submit_plan", "<<QUESTION>> after source evidence is sufficient for correction", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		brief.ReadinessGate = []string{"submit_plan_correction_required"}
		brief.StopConditions = []string{"submit_plan_returned_ok"}
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
	} else {
		brief.ReadinessGate = mergeRunnerStateStrings([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, brief.ReadinessGate)
		brief.StopConditions = mergeRunnerStateStrings([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, brief.StopConditions)
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}
	}
	brief.Readiness = runnerReadinessState(r.agentName(), brief, results)
	return workerruntime.NormalizeRuntimeBrief(brief)
}

const planThenExecuteCurrentSubmitPlanSkeletonPrefix = "# Current Submit Plan Skeleton"

func planThenExecuteTerminalSubmitPlanSkeleton(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Use `Decision: source-satisfied` with current-state evidence, answer `<<QUESTION>>:` with the exact blocker, or perform one bounded read of a concrete candidate path before submitting a plan."
	}
	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}
	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

func planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Because the loop budget is exhausted, do not call more tools. Use `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
	}
	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}
	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

type planThenExecuteSubmitPlanSkeletonEntry struct {
	Path            string
	Evidence        []string
	EvidenceOmitted bool
	StartLine       int
	EndLine         int
	NewFile         bool
}

const planThenExecuteSubmitPlanSkeletonMaxEvidenceLines = 16

func planThenExecuteTerminalSubmitPlanSkeletonEntries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []planThenExecuteSubmitPlanSkeletonEntry {
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{})
	paths := planThenExecuteSubmitPlanSkeletonPaths(state)
	evidenceByPath := planThenExecuteTerminalSubmitPlanEvidenceByPath(results, 2)
	entries := make([]planThenExecuteSubmitPlanSkeletonEntry, 0, len(paths))
	evidenceLinesRemaining := planThenExecuteSubmitPlanSkeletonMaxEvidenceLines
	for _, path := range paths {
		entry := planThenExecuteSubmitPlanSkeletonEntry{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			NewFile:   planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results),
		}
		for _, evidence := range evidenceByPath[normalizeRunnerPath(path)] {
			if evidenceLinesRemaining > 0 {
				entry.Evidence = append(entry.Evidence, evidence.Summary)
				evidenceLinesRemaining--
			} else {
				entry.EvidenceOmitted = true
			}
			if entry.StartLine == 1 && entry.EndLine == 1 && evidence.StartLine > 0 && evidence.EndLine >= evidence.StartLine {
				entry.StartLine = evidence.StartLine
				entry.EndLine = evidence.EndLine
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

type planThenExecuteTerminalSubmitPlanEvidence struct {
	Summary   string
	StartLine int
	EndLine   int
}

func planThenExecuteTerminalSubmitPlanEvidenceByPath(results []workspace.ToolResult, maxPerPath int) map[string][]planThenExecuteTerminalSubmitPlanEvidence {
	if maxPerPath <= 0 {
		maxPerPath = 2
	}
	out := map[string][]planThenExecuteTerminalSubmitPlanEvidence{}
	seen := map[string]map[string]struct{}{}
	add := func(path string, summary string) {
		path = normalizeRunnerPath(path)
		summary = strings.TrimSpace(summary)
		if path == "" || summary == "" || len(out[path]) >= maxPerPath {
			return
		}
		if seen[path] == nil {
			seen[path] = map[string]struct{}{}
		}
		if _, ok := seen[path][summary]; ok {
			return
		}
		seen[path][summary] = struct{}{}
		start, end, _ := planThenExecuteReadSummaryLineRange(summary)
		out[path] = append(out[path], planThenExecuteTerminalSubmitPlanEvidence{
			Summary:   summary,
			StartLine: start,
			EndLine:   end,
		})
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range readPathsFromToolResult(result) {
			add(path, result.Summary)
		}
		for _, line := range strings.Split(result.Output, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "route_summary:") {
				continue
			}
			summary := strings.TrimSpace(strings.TrimPrefix(line, "route_summary:"))
			path := extractReadPathFromSummary(summary)
			if path == "" {
				path = extractRoutedReadPathFromOutput(line)
			}
			add(path, summary)
		}
	}
	return out
}

func planThenExecuteNativeSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder
	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nNative `submit_plan` tool-argument shape. Fill these arguments in the tool call; do not answer with this template as prose.\n\n")
	b.WriteString("rationale: write one concrete sentence explaining why the edit set satisfies the request.\n")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n")
	b.WriteString("edits:\n")
	for _, entry := range entries {
		b.WriteString("- path: ")
		b.WriteString(strconv.Quote(entry.Path))
		b.WriteString("\n")
		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}
		b.WriteString("  purpose: write one concrete sentence for this path.\n")
		b.WriteString("  choose exactly one edit shape:\n")
		if entry.NewFile {
			b.WriteString("    whole_or_new_file: replace_all=true + old_text=\"\" + new_text\n")
		} else {
			b.WriteString(fmt.Sprintf("    line_range_default: start_line=%d + end_line=%d + content (replacement only; safest for source/test edits and large additions)\n", entry.StartLine, entry.EndLine))
			b.WriteString("    substring_small_exact: old_text + new_text\n")
			b.WriteString("    insertion_small_only: insert_after_line + small inline content\n")
			b.WriteString("    whole_or_new_file: replace_all=true + old_text=\"\" + new_text\n")
		}
	}
	b.WriteString("\nEdit shape semantics:\n")
	b.WriteString("- The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("- Line-range `content` replaces the inclusive current-source range from `start_line` through `end_line`; include the complete final replacement for that range.\n")
	b.WriteString("- `insert_after_line` inserts small inline non-empty `content` after an existing 1-based line in an existing file; it does not replace current lines, must not be combined with other edit shapes, and must not be used for large generated blocks.\n")
	b.WriteString("- For large additions inside existing files, use a line-range replacement covering a small current anchor range and include the unchanged anchor line(s) plus inserted content.\n")
	b.WriteString("- Substring `old_text` must be copied exactly from current source and match once; `new_text` is the complete final replacement.\n")
	b.WriteString("- Whole-file/new-file `new_text` is the complete final file body.\n")
	b.WriteString("- For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")
	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("- Additional evidence handles were omitted after the template evidence cap; use the current-source evidence already present in this turn.\n")
	}
	b.WriteString("- Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")
	return b.String()
}

func planThenExecuteTextualSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder
	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nParser-readable submit_plan contract. Emit exactly one visible JSON object with top-level `tool:\"submit_plan\"`, `rationale`, and an `edits` array. The JSON must contain real values only; do not copy placeholders, ellipses, or schema examples. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n\n")
	for _, entry := range entries {
		b.WriteString("- required edit path: ")
		b.WriteString(entry.Path)
		b.WriteString("\n")
		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}
		if entry.NewFile {
			b.WriteString("  expected shape: replace_all true, empty old_text, complete final new_text file body, and a concrete purpose.\n")
		} else {
			startLine := entry.StartLine
			endLine := entry.EndLine
			if startLine <= 0 {
				startLine = 1
			}
			if endLine < startLine {
				endLine = startLine
			}
			b.WriteString("  default source range evidence: start_line ")
			b.WriteString(strconv.Itoa(startLine))
			b.WriteString(", end_line ")
			b.WriteString(strconv.Itoa(endLine))
			b.WriteString(". Use that range only if it is the exact replacement range; otherwise adjust from source evidence.\n")
			b.WriteString("  allowed shapes: start_line/end_line/content, old_text/new_text, insert_after_line/content for small insertions, or replace_all/new_text only for whole-file/new-file writes.\n")
		}
	}
	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("\nAdditional evidence handles were omitted after the evidence cap; use the current-source evidence already present in this turn.\n\n")
	}
	b.WriteString("Edit shape choices: prefer `start_line` + `end_line` + `content` for source/test edits, multi-line changes, and large additions anchored to existing lines; use `old_text` + `new_text` for small exact replacements/deletions; use `insert_after_line` + small inline `content` only for small insertions/appends after an existing line; use `replace_all:true` + empty `old_text` + full `new_text` for a whole-file/new-file write.\n")
	b.WriteString("The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("`insert_after_line` targets an existing 1-based line in an existing file, inserts small inline non-empty `content`, and must not be combined with other edit shapes or used with large content.\n")
	b.WriteString("For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")
	b.WriteString("Line-range `content` replaces every current source line from `start_line` through `end_line`, inclusive. It is not a patch hunk; include the complete final replacement for that range.\n")
	b.WriteString("Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")
	return b.String()
}

func planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries []planThenExecuteSubmitPlanSkeletonEntry) bool {
	for _, entry := range entries {
		if entry.EvidenceOmitted {
			return true
		}
	}
	return false
}

const planThenExecuteTerminalSourceEvidencePrefix = "# Terminal Source Evidence"

func compactPlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ([]llama.ChatMessage, int, int) {
	out := make([]llama.ChatMessage, 0, len(messages)+4)
	dropped := 0
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "user":
			out = append(out, message)
		case "system":
			if planThenExecuteKeepTerminalCompactSystemMessage(message) {
				out = append(out, message)
			} else {
				dropped++
			}
		default:
			dropped++
		}
	}
	evidence := planThenExecuteTerminalSourceEvidenceMessages(brief, results)
	out = append(out, evidence...)
	return out, dropped, len(evidence)
}

func planThenExecuteKeepTerminalCompactSystemMessage(message llama.ChatMessage) bool {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}
	return strings.HasPrefix(content, "AUTHORITATIVE_CONSTRAINT:") ||
		strings.HasPrefix(content, planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
}

func planThenExecuteTerminalSourceEvidenceMessages(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []llama.ChatMessage {
	required := map[string]struct{}{}
	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			required[normalized] = struct{}{}
		}
	}
	messages := make([]llama.ChatMessage, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		paths := readPathsFromToolResult(result)
		paths = nonArtifactEvidencePaths(paths)
		if len(paths) == 0 {
			continue
		}
		if len(required) > 0 && !planThenExecuteEvidenceIntersectsRequiredPaths(paths, required) {
			continue
		}
		summary := strings.TrimSpace(result.Summary)
		output := strings.TrimSpace(result.Output)
		if summary == "" && output == "" {
			continue
		}
		key := strings.TrimSpace(result.Tool) + "\x00" + summary + "\x00" + output
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		var b strings.Builder
		b.WriteString(planThenExecuteTerminalSourceEvidencePrefix)
		b.WriteString("\n\n")
		if summary != "" {
			b.WriteString("summary: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}
		if len(paths) > 0 {
			b.WriteString("paths: ")
			b.WriteString(strings.Join(normalizedRunnerPaths(paths), ", "))
			b.WriteString("\n")
		}
		if output != "" {
			b.WriteString("\n")
			b.WriteString(output)
		}
		messages = append(messages, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(b.String())})
	}
	return messages
}

func planThenExecuteEvidenceIntersectsRequiredPaths(paths []string, required map[string]struct{}) bool {
	for _, path := range paths {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}
		if _, ok := required[normalizeRunnerPath(path)]; ok {
			return true
		}
	}
	return false
}

func normalizedRunnerPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if isNonSourceEvidenceArtifactPath(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func nonArtifactEvidencePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || isNonSourceEvidenceArtifactPath(path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

func prunePlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage) ([]llama.ChatMessage, int, int) {
	if len(messages) == 0 {
		return messages, 0, 0
	}
	out := make([]llama.ChatMessage, 0, len(messages))
	droppedActions := 0
	droppedScaffolding := 0
	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "tool" {
			droppedActions++
			continue
		}
		if planThenExecuteDropTextualToolRequestHistoryMessage(message) || strings.TrimSpace(message.Role) == "assistant" {
			droppedActions++
			continue
		}
		if planThenExecuteDropTerminalTextualScaffoldingMessage(message) {
			droppedScaffolding++
			continue
		}
		out = append(out, message)
	}
	if droppedActions == 0 && droppedScaffolding == 0 {
		return messages, 0, 0
	}
	return out, droppedActions, droppedScaffolding
}

func prunePlanThenExecuteTextualToolRequestHistory(messages []llama.ChatMessage) ([]llama.ChatMessage, int) {
	if len(messages) == 0 {
		return messages, 0
	}
	out := make([]llama.ChatMessage, 0, len(messages))
	dropped := 0
	for _, message := range messages {
		if planThenExecuteDropTextualToolRequestHistoryMessage(message) {
			dropped++
			continue
		}
		out = append(out, message)
	}
	if dropped == 0 {
		return messages, 0
	}
	return out, dropped
}

func planThenExecuteDropTerminalTextualScaffoldingMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "system" {
		return false
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}
	return strings.HasPrefix(content, "# Textual Tool Call Protocol") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Textual Tool Surface") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Native Tool Surface") ||
		strings.HasPrefix(content, planThenExecuteCurrentSubmitPlanSkeletonPrefix) ||
		strings.HasPrefix(content, "[frontier step instruction]") ||
		strings.HasPrefix(content, "[frontier runtime brief]") ||
		strings.HasPrefix(content, "REQUIRED SOURCE READ PENDING:") ||
		strings.HasPrefix(content, "PLANNER READINESS REACHED:") ||
		strings.HasPrefix(content, "SUBMIT_PLAN PARSE RECOVERY:") ||
		strings.HasPrefix(content, "Plan-execute submit_plan is now the only allowed planner action.") ||
		strings.HasPrefix(content, "The completion gate rejected") ||
		strings.HasPrefix(content, "The previous `submit_plan` was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "The previous native `submit_plan` tool call was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "Native submit_plan tool-call transport failed before the model produced a tool call.") ||
		strings.HasPrefix(content, "The previous retry produced hidden-only output with no parser-readable action.") ||
		strings.HasPrefix(content, "Note: your `read_file` call was redirected") ||
		strings.HasPrefix(content, "Note: your `code_navigate` call was redirected")
}

func planThenExecuteDropTextualToolRequestHistoryMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "assistant" || len(message.ToolCalls) > 0 {
		return false
	}
	content := strings.TrimSpace(message.Content)
	if content == "" || !strings.Contains(content, `"tool"`) {
		return false
	}
	req, _, ok, parseErr := workspace.ParseToolRequest(content)
	if ok {
		tool := strings.TrimSpace(req.Tool)
		return tool != "submit_plan"
	}
	return parseErr != nil || strings.Contains(content, `"tool"`)
}

const planThenExecuteRejectedSubmitPlanDraftEvidencePrefix = "# Rejected Submit Plan Draft Evidence"

func replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(messages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if messages == nil {
		return
	}
	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(req, result)
	if prompt == "" {
		return
	}
	out := make([]llama.ChatMessage, 0, len(*messages)+1)
	for _, message := range *messages {
		if strings.TrimSpace(message.Role) == "system" &&
			strings.HasPrefix(strings.TrimSpace(message.Content), planThenExecuteRejectedSubmitPlanDraftEvidencePrefix) {
			continue
		}
		out = append(out, message)
	}
	out = append(out, llama.ChatMessage{Role: "system", Content: prompt})
	*messages = out
}

func planThenExecuteRejectedSubmitPlanDraftEvidence(req workspace.ToolRequest, result workspace.ToolResult) string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return ""
	}
	invalidPaths := submitPlanInvalidAnchorPaths(result)
	missingPaths := submitPlanMissingRequiredPaths(result)
	var b strings.Builder
	b.WriteString(planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
	b.WriteString("\n\nFactual evidence from the previous `submit_plan` request. The harness rejected that request; no plan artifact was created.\n")
	if brief := strings.TrimSpace(lastSubmitPlanCorrectionRejectionBrief(result)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}
	b.WriteString("\nDraft edit entries from the rejected request. Rejected body excerpts are not source evidence; correct them against the latest read_file/code_navigate source evidence before resubmitting.\n")
	for i, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			path = strings.TrimSpace(edit.Path)
		}
		if path == "" {
			path = fmt.Sprintf("edits[%d]", i)
		}
		b.WriteString(fmt.Sprintf("- edits[%d] path=%s", i, path))
		if edit.StartLine > 0 || edit.EndLine > 0 {
			b.WriteString(fmt.Sprintf(" lines=%d-%d", edit.StartLine, edit.EndLine))
		}
		if _, ok := invalidPaths[normalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=invalid_anchor_or_syntax")
		}
		if _, ok := missingPaths[normalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=missing_required_path")
		}
		if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
			b.WriteString(" purpose=")
			b.WriteString(truncateRunnerStateText(purpose, 180))
		}
		b.WriteString("\n")
		if planThenExecuteShouldIncludeRejectedDraftBody(edit, invalidPaths) {
			body := rejectedDraftEditBody(edit)
			if body != "" {
				b.WriteString("  rejected_body_excerpt_not_source_evidence:\n")
				for _, line := range strings.Split(truncateRunnerStateText(body, 2200), "\n") {
					b.WriteString("    ")
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
		}
		if b.Len() > 7000 {
			b.WriteString("- remaining rejected draft entries omitted after bounded evidence cap.\n")
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func planThenExecuteShouldIncludeRejectedDraftBody(edit workspace.PlanEdit, invalidPaths map[string]struct{}) bool {
	if len(invalidPaths) == 0 {
		return false
	}
	_, ok := invalidPaths[normalizeRunnerPath(edit.Path)]
	return ok
}

func rejectedDraftEditBody(edit workspace.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}
	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}
	return ""
}

func (r *Runner) readOnlyExplanationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}
	if reason := readOnlyExplanationMetaRejectionReason(reply); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected meta final answer",
			Error:   reason,
		}, true
	}
	if reason := readOnlyExplanationObjectiveRejectionReason(reply, r.RuntimeBrief); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected off-objective final answer",
			Error:   reason,
		}, true
	}
	if unsupported := architectureUnsupportedExactDetail(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Architecture completion gate rejected unsupported detail",
			Error:   "the answer names additional implementation detail " + quotedRunnerStrings(unsupported) + " without an exact inspected probe or explicit uncertainty. Either anchor that detail with one exact read, or qualify it as unverified.",
		}, true
	}
	if reason := r.sourceGroundingRejectionReason(reply, results); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected ungrounded source citation",
			Error:   reason,
		}, true
	}
	return workspace.ToolResult{}, false
}

func (r *Runner) sourceGroundingRejectionReason(answer string, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}
	if !sourceGroundingGateApplies(brief) {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return ""
	}
	if readOnlyScaffoldFinalAnswer(answer) {
		return "the answer is only a retained-context or evidence-scaffold summary. Final answers must state the concrete behavior supported by relevant source-body or diagnostic evidence, not only list evidence surfaces."
	}
	paths := sourceGroundingMentionedPaths(answer)
	if len(paths) == 0 {
		return ""
	}
	var missing []string
	var unsupported []string
	for _, rawPath := range paths {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		citationPath := path
		if resolved, ok := sourceCitationCanonicalPath(r, path, results); ok {
			citationPath = resolved
		}
		if sourceEvidenceBodyReadForPath(citationPath, results) || slices.Contains(delegatedReadOnlySourceBodyEvidencePaths(results), citationPath) {
			continue
		}
		if !sourceCitationExists(r, citationPath) {
			if !strings.Contains(path, "/") &&
				!sourceCitationLooksLikeRootPath(path) &&
				!sourceCitationHasLineSuffix(answer, path) &&
				!sourceCitationClaimsSourceDetail(answer, path) {
				continue
			}
			if !sourceCitationExplicitlyNonCurrent(answer, path) {
				missing = append(missing, path)
			}
			continue
		}
		if !sourceGroundingPathRequiresBody(path) {
			continue
		}
		if !sourceCitationClaimsSourceDetail(answer, path) {
			continue
		}
		if sourceCitationExplicitlyNonCurrent(answer, path) || sourceCitationExplicitlyUnverified(answer, path) {
			continue
		}
		unsupported = append(unsupported, citationPath)
	}
	if len(missing) > 0 {
		return "the answer cites current-workspace path(s) that do not exist: " + quotedRunnerStrings(uniqueSortedRunnerPaths(missing)) + ". Correct the citation, label it as historical/log-only evidence, or answer from inspected current source."
	}
	if len(unsupported) > 0 {
		return "the answer cites source path/line/symbol detail not backed by a source-body read: " + quotedRunnerStrings(uniqueSortedRunnerPaths(unsupported)) + ". Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."
	}
	return ""
}

func sourceCitationCanonicalPath(r *Runner, path string, results []workspace.ToolResult) (string, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return "", false
	}
	if sourceCitationExists(r, path) {
		return path, true
	}
	if strings.Contains(path, "/") || sourceCitationLooksLikeRootPath(path) {
		return path, false
	}
	base := strings.ToLower(filepath.Base(path))
	var matches []string
	for _, candidate := range mergeRunnerStateStrings(sourceEvidenceBodyPaths(results), delegatedReadOnlySourceBodyEvidencePaths(results)) {
		candidate = normalizeRunnerPath(candidate)
		if candidate == "" || strings.EqualFold(candidate, path) {
			continue
		}
		if strings.ToLower(filepath.Base(candidate)) != base {
			continue
		}
		if !sourceCitationExists(r, candidate) {
			continue
		}
		matches = append(matches, candidate)
	}
	matches = uniqueSortedRunnerPaths(matches)
	if len(matches) != 1 {
		return path, false
	}
	return matches[0], true
}

func sourceGroundingMentionedPaths(answer string) []string {
	paths := append([]string(nil), pathparse.MentionedWorkspaceFiles(answer)...)
	lineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])([a-z0-9_.-]+\\.(?:go|py|ts|tsx|js|jsx|rs|java|c|cc|cpp|h|hpp|sh|zsh|gd)):\\d+")
	for _, match := range lineRE.FindAllStringSubmatch(answer, -1) {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}
	rootLineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])((?:go\\.mod|go\\.sum|makefile|dockerfile|readme\\.md|agents\\.md|package\\.json|package-lock\\.json|pnpm-lock\\.yaml|yarn\\.lock|pyproject\\.toml|poetry\\.lock|cargo\\.toml|cargo\\.lock)):\\d+")
	for _, match := range rootLineRE.FindAllStringSubmatch(answer, -1) {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}
	return uniqueSortedRunnerPaths(paths)
}

func sourceGroundingGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}
	if readOnlyDirectAnswerLane(brief) {
		return true
	}
	lowerObjective := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if strings.Contains(lowerObjective, "root cause") ||
		strings.Contains(lowerObjective, "debug") ||
		strings.Contains(lowerObjective, "failure") {
		return true
	}
	return false
}

func sourceCitationExists(r *Runner, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return false
	}
	root := "."
	if r != nil && r.Workspace != nil && strings.TrimSpace(r.Workspace.Root()) != "" {
		root = r.Workspace.Root()
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

func sourceGroundingPathRequiresBody(path string) bool {
	switch strings.ToLower(filepath.Ext(filepath.ToSlash(path))) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".py", ".pyi", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
		".kt", ".kts", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".gd":
		return true
	default:
		return false
	}
}

func sourceCitationLooksLikeRootPath(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	switch base {
	case "go.mod", "go.sum", "makefile", "dockerfile", "readme.md", "agents.md",
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock":
		return true
	default:
		return false
	}
}

func sourceCitationHasLineSuffix(answer string, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer)
}

func sourceCitationClaimsSourceDetail(answer string, path string) bool {
	window := sourceCitationWindow(answer, path)
	lower := strings.ToLower(window)
	if strings.Contains(lower, "line ") || strings.Contains(lower, "lines ") ||
		strings.Contains(lower, "symbol") || strings.Contains(lower, "function") ||
		strings.Contains(lower, "method") || strings.Contains(lower, "handler") ||
		strings.Contains(lower, "root cause") {
		return true
	}
	if regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer) {
		return true
	}
	for _, token := range backtickedCodeLikeTokens(window) {
		if sourceGroundingTokenLooksLikeSymbol(token) {
			return true
		}
	}
	return false
}

func sourceCitationExplicitlyNonCurrent(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"does not exist", "do not exist", "doesn't exist",
		"not in the current checkout", "not current",
		"historical", "log-only", "from the log", "in the log",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func sourceCitationExplicitlyUnverified(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"unverified", "not yet read", "not source-read", "not source read", "not backed",
		"not enough evidence", "not enough to name", "not enough to identify",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func sourceCitationWindow(answer string, path string) string {
	if strings.TrimSpace(answer) == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	idx := strings.Index(answer, path)
	if idx < 0 {
		return answer
	}
	start := idx - 180
	if start < 0 {
		start = 0
	}
	end := idx + len(path) + 180
	if end > len(answer) {
		end = len(answer)
	}
	return answer[start:end]
}

func backtickedCodeLikeTokens(text string) []string {
	matches := backtickedTokenPattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return out
}

func sourceGroundingTokenLooksLikeSymbol(token string) bool {
	if token == "" || strings.Contains(token, "/") || strings.Contains(token, " ") {
		return false
	}
	return codeShapedTokenPattern.MatchString(token) ||
		codeShapedSnakeCasePattern.MatchString(token) ||
		qualifiedIdentifierPattern.MatchString(token)
}

func planThenExecuteAnswerDeclaresTypedBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") ||
		strings.Contains(trimmed, "\n<<QUESTION>>") {
		return true
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "decision: block") {
			return true
		}
	}
	return false
}

func (r *Runner) mutationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolResult{}, false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}
	// Plan-then-execute planner phase: the planner's deliverable is a
	// successful submit_plan call, not file mutations. The executor
	// phase (run by the orchestrator AFTER this runner exits) is what
	// actually changes files. If submit_plan succeeded, the planner is
	// done — short-circuit the rest of the gate so we don't reject the
	// answer for "no successful write tool result".
	if r.planThenExecutePlannerPhase() && successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolResult{}, false
	}
	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if r.planThenExecutePlannerPhase() && len(changedPathsFromToolResults(results)) == 0 {
		if r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
			return workspace.ToolResult{}, false
		}
		if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
			if requiredPathBlockerAnswerCovers(reply, missing) {
				return workspace.ToolResult{}, false
			}
		} else if planThenExecuteAnswerDeclaresTypedBlocker(reply) {
			return workspace.ToolResult{}, false
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   planThenExecuteMissingSubmitPlanError(r.RuntimeBrief.RequiredEditPaths),
		}, true
	}
	if len(changedPathsFromToolResults(results)) == 0 {
		if unsupported := unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected unsupported change claim",
				Error:   "final answer reports changed paths not backed by successful write tool results: " + quotedRunnerStrings(unsupported) + ". Satisfy the missing write contract, or correct the final answer to a concrete blocker/no-op reason only.",
			}, true
		}
		if mutationReplyClaimsCompletedWorkspaceChange(reply) {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected unsupported change claim",
				Error:   "final answer claims the workspace change was completed, but no successful write tool result is present. Satisfy the missing write contract, or answer only with the concrete blocker/no-op evidence.",
			}, true
		}
		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
			if requiredPathBlockerAnswerCovers(reply, missing) {
				return workspace.ToolResult{}, false
			}
		} else if mutationReplyReportsConcreteNoopOrBlocker(lowerReply) {
			return workspace.ToolResult{}, false
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present. The contract outcomes are the smallest necessary edit, or a concrete blocker/no-op reason.",
		}, true
	}
	if unsupported := unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "final answer reports changed paths not backed by successful write tool results: " + quotedRunnerStrings(unsupported) + ". Satisfy the missing write contract, or correct the changed-paths summary to only include recorded edits.",
		}, true
	}
	if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
		if !requiredPathBlockerAnswerCovers(reply, missing) {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected final answer",
				Error:   "required edit path(s) were not changed: " + quotedRunnerStrings(missing) + ". Satisfy the required-path write contract, or state a concrete blocker/no-op reason for the skipped path(s).",
			}, true
		}
	}
	if removed := r.unreportedRemovedTestDeclarations(reply, results); len(removed) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected silent test removal",
			Error:   "git diff shows existing test declaration(s) removed without the final answer reporting them: " + quotedRunnerStrings(removed) + ". If the removal is intentional, report each removed test with verification status; otherwise restore the deleted test or fix the edit boundary.",
		}, true
	}
	if missing := r.missingRequiredEditSnippets(results); len(missing) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "required edit snippet(s) are not present after the recorded edits: " + quotedRunnerStrings(missing) + ". Make the missing edit(s), or state a concrete blocker for each skipped required edit.",
		}, true
	}
	if verificationContractSkipsGate(r.RuntimeBrief.VerificationContract) {
		return workspace.ToolResult{}, false
	}
	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) {
		errorText := "workspace changes are present, but the latest successful write is not followed by successful verification. Required evidence is a relevant language/project test/build/check result, compiler-backed diagnostics, or the concrete verification blocker."
		if plannerOwnsVerification(r.RuntimeBrief) {
			errorText = "workspace changes are present, but the latest successful write is not followed by lane sanity. This route delegates build/test verification to the planner; source edits require compiler-backed source-sanity evidence, and non-source edits may use git diff --check only as patch-hygiene evidence or state the concrete planner-owned verification blocker."
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   errorText,
		}, true
	}
	if sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
		errorText := "source edits are present, but the available verification is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. Required evidence is a focused language-appropriate build/test/check command, compiler-backed diagnostics/source-sanity, or the concrete verification blocker; patch hygiene alone cannot certify source edits."
		if plannerOwnsVerification(r.RuntimeBrief) {
			errorText = "source edits are present, but the available lane sanity is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. planner-owned verification lanes require compiler-backed source-sanity or language diagnostics for the changed source path. If source sanity is unavailable, answer with changed paths plus the concrete lane-sanity blocker. Do not claim patch hygiene certifies source edits."
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   errorText,
		}, true
	}
	if mutationReplyReportsConcreteNoopOrBlocker(lowerReply) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{}, false
}

// refineReasoningShape captures one recognized markdown structural
// shape the auto-salvage can recover. anchor is the marker we search
// for as the "primary" structural section start; corroborator is a
// second marker that must ALSO appear so we don't extract on a
// passing mention. Each shape's primary anchor defines where the
// extracted block begins (we walk back from its last occurrence).
type refineReasoningShape struct {
	name         string
	primary      string
	corroborator string
}

var refineReasoningShapes = []refineReasoningShape{
	// Refine review deliverable: structured bug list + verdict.
	{name: "review", primary: "## Bugs found", corroborator: "## Verdict"},
	// Refine markdown-deliverable mode: the draft carries the target
	// file's content in a dedicated inline-content section.
	{name: "markdown-deliverable", primary: "## Inline content", corroborator: "## Deliverable"},
	// Refine draft / patch deliverable: implementation brief layout
	// (the canonical brief skeleton sections). Required Behavior +
	// Files To Modify both appear in every well-formed refine draft;
	// requiring both lets us reject thinking chatter that mentions a
	// section heading in passing.
	{name: "draft", primary: "## Files To Modify", corroborator: "## Required Behavior"},
	// Lowercase variant — the draft template uses "## Files to
	// Modify" (small 't') in some renderings.
	{name: "draft-alt", primary: "## Files to Modify", corroborator: "## Required Behavior"},
	// Some project and refine templates use "Required Edits" for the
	// concrete implementation section. Treat that as the same
	// corrected-brief shape so a prose preface does not strand a full
	// markdown deliverable in the final answer.
	{name: "draft-required-edits", primary: "## Files To Modify", corroborator: "## Required Edits"},
	{name: "draft-required-edits-alt", primary: "## Files to Modify", corroborator: "## Required Edits"},
}

// extractStructuredMarkdownFromReasoning recovers a refine-shaped
// markdown deliverable (review or draft/patch) from inside a model's
// reasoning content, used by tryAutoSalvageMarkdownDeliverable as
// a fallback when the visible reply is empty. Returns "" when no
// safe extraction is possible — the auto-salvage caller then skips
// and the completion gate rejects normally.
//
// Safety preconditions (all must hold):
//   - reasoning >= 200 chars (some signal there)
//   - reasoning matches one of refineReasoningShapes (primary +
//     corroborator both present)
//   - extracted block is >= 100 chars
//
// Extraction strategy: find the LAST occurrence of the matched
// shape's primary anchor and take from there to the end (walking
// back through any preceding `# Title`). Last-occurrence is
// deliberate — the model's thinking often iterates a few times
// before settling on the final structure; the last occurrence is
// closest to the model's final intent.
func extractStructuredMarkdownFromReasoning(reasoning string) string {
	r := strings.TrimSpace(reasoning)
	if len(r) < 200 {
		return ""
	}
	var matched refineReasoningShape
	for _, shape := range refineReasoningShapes {
		if strings.Contains(r, shape.primary) && strings.Contains(r, shape.corroborator) {
			matched = shape
			break
		}
	}
	if matched.primary == "" {
		return ""
	}
	primaryIdx := strings.LastIndex(r, matched.primary)
	if primaryIdx < 0 {
		return ""
	}
	startIdx := primaryIdx
	if nl := strings.LastIndex(r[:primaryIdx], "\n"); nl >= 0 {
		startIdx = nl + 1
	}
	// Walk back to a preceding `# Title` if one is directly above
	// (within 400 chars). Conservative cap — beyond that we'd risk
	// grabbing arbitrary thinking.
	lookbackStart := startIdx - 400
	if lookbackStart < 0 {
		lookbackStart = 0
	}
	preceding := r[lookbackStart:startIdx]
	if hdr := strings.LastIndex(preceding, "\n# "); hdr >= 0 {
		startIdx = lookbackStart + hdr + 1
	}
	candidate := strings.TrimSpace(r[startIdx:])
	if len(candidate) < 100 {
		return ""
	}
	if reasoningCandidateLooksLikeUnfilledTemplate(candidate) {
		return ""
	}
	return candidate
}

// reasoningCandidateLooksLikeUnfilledTemplate rejects salvaged
// content that's the meta-brief's skeleton placeholder itself —
// observed during refine v10 (#97): Coder-30B emitted 0 visible
// content and 69KB of reasoning content that turned out to be the
// model "thinking about" the template (copy-pasting `<RELATIVE_PATH>`
// and `<numbered step...>` markers). The salvage matched the
// structural anchors and would have shipped that garbage as a draft.
//
// Markers checked are the exact placeholder strings the refine
// templates ship. If two or more appear in the candidate, treat it
// as model-didn't-actually-fill-it and refuse to salvage.
func reasoningCandidateLooksLikeUnfilledTemplate(candidate string) bool {
	markers := []string{
		"RELATIVE_PATH",
		"<numbered step",
		"<workspace/relative",
		"<bracket placeholder>",
		"<short title>",
		"<description>",
		"<how to fix>",
		"<one or two sentences",
		"<quote relevant existing code",
		"<shell command that verifies",
	}
	hits := 0
	for _, m := range markers {
		if strings.Contains(candidate, m) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

// extractStructuredMarkdownFromReply recovers a full markdown
// deliverable from visible final-answer text. Unlike reasoning
// salvage, visible reply salvage may walk back to a much earlier H1:
// long refine drafts often place `## Files To Modify` near the end of
// the document, well beyond the conservative reasoning lookback.
func extractStructuredMarkdownFromReply(reply string) string {
	r := strings.TrimSpace(reply)
	if len(r) < 100 || strings.HasPrefix(r, "<<QUESTION>>") {
		return ""
	}
	if recoverableMarkdownDeliverableShape(r) && markdownBodyStartsAsDeliverable(r) && !reasoningCandidateLooksLikeUnfilledTemplate(r) {
		return r
	}
	var matched refineReasoningShape
	for _, shape := range refineReasoningShapes {
		if strings.Contains(r, shape.primary) && strings.Contains(r, shape.corroborator) {
			matched = shape
			break
		}
	}
	if matched.primary == "" {
		return ""
	}
	primaryIdx := strings.LastIndex(r, matched.primary)
	if primaryIdx < 0 {
		return ""
	}
	startIdx := primaryIdx
	if strings.HasPrefix(r, "# ") {
		startIdx = 0
	} else if hdr := strings.LastIndex(r[:primaryIdx], "\n# "); hdr >= 0 {
		startIdx = hdr + 1
	} else if nl := strings.LastIndex(r[:primaryIdx], "\n"); nl >= 0 {
		startIdx = nl + 1
	}
	candidate := strings.TrimSpace(r[startIdx:])
	if len(candidate) < 100 || !recoverableMarkdownDeliverableShape(candidate) {
		return ""
	}
	if reasoningCandidateLooksLikeUnfilledTemplate(candidate) {
		return ""
	}
	return candidate
}

func recoverableMarkdownDeliverableShape(body string) bool {
	body = strings.TrimSpace(body)
	if len(body) < 100 {
		return false
	}
	for _, shape := range refineReasoningShapes {
		if strings.Contains(body, shape.primary) && strings.Contains(body, shape.corroborator) {
			return true
		}
	}
	return false
}

// autoSalvageMarkdownDeliverable handles a specific Qwen-at-low-temp
// failure mode: the deliverable is a single new .md file (refine
// review/draft/patch lanes), the model wrote the entire markdown
// content into its final-answer text instead of calling write_file,
// and the completion gate is about to reject for "no successful write."
//
// When the brief has exactly one required edit path that ends in .md,
// the path doesn't yet exist on disk, and the reply is a recognizable
// refine-shaped markdown payload, we write the reply to that path on
// behalf of the model and return the synthetic write_file result so
// the main runner can record it as normal completion evidence.
//
// Returns false (no salvage) when the criteria don't match — caller
// proceeds with the normal rejection. Salvage failures (file write
// errors) also return false; the original gate rejection wins.
func (r *Runner) autoSalvageMarkdownDeliverable(reply string) (workspace.ToolRequest, workspace.ToolResult, bool) {
	if r == nil || r.Workspace == nil {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	if len(r.RuntimeBrief.RequiredEditPaths) != 1 {
		if len(r.RuntimeBrief.RequiredEditPaths) > 0 {
			r.emitAutoSalvageEvent("skip: required_edit_paths count=%d, want 1", len(r.RuntimeBrief.RequiredEditPaths))
		}
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	path := strings.TrimSpace(r.RuntimeBrief.RequiredEditPaths[0])
	if path == "" || !strings.HasSuffix(strings.ToLower(path), ".md") {
		r.emitAutoSalvageEvent("skip: path %q is not a .md file", path)
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	if filepath.IsAbs(path) || cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		r.emitAutoSalvageEvent("skip: path %q is not a safe relative path", path)
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	if root := strings.TrimSpace(r.Workspace.Root()); root != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(cleanPath))); err == nil {
			r.emitAutoSalvageEvent("skip: path %q already exists", path)
			return workspace.ToolRequest{}, workspace.ToolResult{}, false
		} else if !os.IsNotExist(err) {
			r.emitAutoSalvageEvent("skip: cannot stat %q: %v", path, err)
			return workspace.ToolRequest{}, workspace.ToolResult{}, false
		}
	}
	body := extractStructuredMarkdownFromReply(reply)
	source := "reply"
	if body != "" && strings.TrimSpace(body) != strings.TrimSpace(reply) {
		source = "reply-structured"
		r.emitAutoSalvageEvent("extracted structured markdown from reply prose (%d chars)", len(body))
	}
	// Fallback path (#95): Qwen3 thinking mode sometimes burns the
	// entire generation budget inside the <think> block and exits
	// with empty visible content even when the thinking-text contains
	// a structured review. Recover by extracting from
	// LastAssistantMessage.ReasoningContent — bounded to the
	// review-template structural markers so we don't ship random
	// reasoning artifacts.
	if len(body) < 100 {
		if salvaged := extractStructuredMarkdownFromReasoning(r.LastAssistantMessage.ReasoningContent); salvaged != "" {
			body = salvaged
			source = "reasoning"
			r.emitAutoSalvageEvent("recovering from reasoning content (reply was %d chars, reasoning yielded %d)", len(strings.TrimSpace(reply)), len(body))
		}
	}
	if len(body) < 100 {
		r.emitAutoSalvageEvent("skip: body too short (%d chars, source=%s)", len(body), source)
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	if !recoverableMarkdownDeliverableShape(body) {
		r.emitAutoSalvageEvent("skip: body is not a recognized markdown deliverable shape (first 100 chars: %q)", body[:min(100, len(body))])
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	ctx := context.Background()
	req := workspace.ToolRequest{
		Tool:        "write_file",
		Path:        path,
		Content:     body,
		Description: "auto-salvage: model put deliverable in final-answer text instead of calling write_file",
	}
	result := r.Workspace.Execute(ctx, req)
	if strings.TrimSpace(result.Error) != "" {
		r.emitAutoSalvageEvent("FAILED: write_file %s: %s", path, result.Error)
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	r.emitAutoSalvageEvent("SUCCESS: wrote %s (%d bytes)", path, len(body))
	return req, result, true
}

func (r *Runner) emitAutoSalvageEvent(format string, args ...any) {
	if r == nil {
		return
	}
	r.emitEvent(RunEvent{Role: "auto_salvage", Content: fmt.Sprintf(format, args...)})
}

// tryAutoSalvageMarkdownDeliverable is retained for focused unit tests
// and callers that only care whether the recovery wrote a file. The
// main runner uses autoSalvageMarkdownDeliverable so the synthetic
// write_file result is recorded in collectedResults.
func (r *Runner) tryAutoSalvageMarkdownDeliverable(reply string) bool {
	_, _, ok := r.autoSalvageMarkdownDeliverable(reply)
	return ok
}

func markdownBodyStartsAsDeliverable(body string) bool {
	body = strings.TrimSpace(body)
	return strings.HasPrefix(body, "# ") || strings.HasPrefix(body, "## ")
}

func mutationReplyReportsConcreteNoopOrBlocker(lowerReply string) bool {
	lowerReply = strings.ToLower(strings.TrimSpace(lowerReply))
	if lowerReply == "" {
		return false
	}
	if containsAny(lowerReply,
		"<<question>>",
		"blocked",
		"blocker",
		"cannot",
		"can't",
		"could not",
		"unable",
		"permission",
		"approval",
		"no-op",
		"no op",
		"no change needed",
		"no changes needed",
		"nothing to change",
	) {
		return true
	}
	return containsAny(lowerReply, "already present", "already implemented", "already satisfied", "already up to date") &&
		containsAny(lowerReply, "no change", "no-op", "no op", "nothing to change")
}

func mutationReplyClaimsCompletedWorkspaceChange(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	if containsAny(lower, "already present", "already implemented", "already satisfied", "already up to date") &&
		containsAny(lower, "no change", "no-op", "no op", "nothing to change") {
		return false
	}
	if len(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(trimmed))) == 0 {
		for _, fragment := range pathparse.QuotedFragments(trimmed) {
			if _, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				return containsAny(lower,
					"has been edited",
					"have edited",
					"has been updated",
					"have updated",
					"updated so",
					"changed so",
					"implemented",
					"applied the edit",
					"made the edit",
					"source read confirms the edit",
				)
			}
		}
		return false
	}
	return containsAny(lower,
		"has been edited",
		"have edited",
		"has been updated",
		"have updated",
		"updated so",
		"changed so",
		"implemented",
		"applied the edit",
		"made the edit",
		"source read confirms the edit",
	)
}

// escalateCompletionGateMessage prefixes the gate's existing error
// text with progressively-firmer instructions so the model gets
// clearer guidance on each retry instead of the same message N times.
// priorRejections is the number of completion-gate rejections this
// turn BEFORE the current one (so 0 = first rejection). Generic — no
// task-family or project assumptions.
//
// Tier 0 (first rejection): pass through unchanged. Most rejections
//
//	resolve on first retry; extra prefix would be noise.
//
// Tier 1 (second rejection): mark it explicitly + remind the model
//
//	the gate already fired once and the next move must address the
//	stated blocker.
//
// Tier 2 (third rejection, last before tools disable): demand a
//
//	concrete tool call by name OR an explicit blocker statement.
func escalateCompletionGateMessage(base string, priorRejections int) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return base
	}
	switch {
	case priorRejections <= 0:
		return base
	case priorRejections == 1:
		return "[gate retry 2/3] " + trimmed +
			" — the gate already rejected one prior attempt with this error;" +
			" your next move MUST resolve the stated blocker (use a different tool" +
			" call or state a concrete reason this cannot proceed)."
	default:
		return "[gate retry 3/3 — last attempt before tools disable] " + trimmed +
			" — repeated rejections indicate the active reply does not satisfy" +
			" the contract. Either issue ONE concrete tool call that produces the" +
			" missing evidence, or write a final answer that explicitly names the" +
			" blocker. Do not narrate; act."
	}
}

func planThenExecuteMissingSubmitPlanError(requiredEditPaths []string) string {
	if len(requiredEditPaths) > 0 {
		return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names every required path and cites current-state evidence. Call `submit_plan`: " + planThenExecuteSubmitPlanCoverageSentence() + " Or answer `Decision: source-satisfied`, or answer with <<QUESTION>> naming the exact path-specific blocker."
	}
	return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names the relevant path(s) and cites current-state evidence. Call `submit_plan` for the needed candidate/allowed edit path(s), answer `Decision: source-satisfied`, or answer with `<<QUESTION>>:` naming the exact blocker."
}

func (r *Runner) completionGateRetryLimit(results []workspace.ToolResult) int {
	if r != nil && r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return 2
	}
	if r != nil && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return 2
	}
	if r != nil && r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		return 1
	}
	if r != nil && r.textualRequiredPathCompletionRecoveryApplies(results) {
		return 2
	}
	if r != nil && r.completionGateMutationRecoveryApplies(results) {
		if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
			sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
			if completionGateRejectionBeforeLatestWrite(results) {
				return 2
			}
			return 1
		}
		return 1
	}
	return 2
}

func (r *Runner) retryableFinalAnswerAcceptanceRejection(acceptance finalAnswerAcceptance, results []workspace.ToolResult, priorGateRejections int, allowedTools []string) bool {
	if r == nil || !retryableReadOnlyFinalAnswerBlockerKind(acceptance.BlockerKind) {
		return false
	}
	if priorGateRejections >= r.completionGateRetryLimit(results) {
		return false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) ||
		!workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {
		return false
	}
	tools := uniquePhaseStrings(append(append([]string(nil), brief.AllowedTools...), allowedTools...))
	return len(genericReadOnlyEvidenceTools(tools)) > 0
}

func retryableReadOnlyFinalAnswerBlockerKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "source_grounding_unmet", "answer_objective_unmet", "read_only_protocol_blocker":
		return true
	default:
		return false
	}
}

func completionGateRejectionBeforeLatestWrite(results []workspace.ToolResult) bool {
	latestWrite := -1
	for i, result := range results {
		if strings.TrimSpace(result.Error) == "" && workspace.IsWorkspaceWriteTool(result.Tool) {
			latestWrite = i
		}
	}
	if latestWrite <= 0 {
		return false
	}
	for _, result := range results[:latestWrite] {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			return true
		}
	}
	return false
}

func (r *Runner) textualRequiredPathCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	return r != nil &&
		!r.supportsNativeTools() &&
		runnerBriefRequiresMutation(r.RuntimeBrief) &&
		len(r.remainingRequiredEditPaths(results)) > 0
}

func (r *Runner) unscopedNoWriteCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}
	return len(changedPathsFromToolResults(results)) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0
}

func (r *Runner) completionGateMutationRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}
	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return true
	}
	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return true
	}
	if len(changedPathsFromToolResults(results)) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return true
	}
	return hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results)
}

func (r *Runner) completionGateRepeatedBlockerKind(results []workspace.ToolResult) string {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "completion_gate_repeated_rejection"
	}
	if r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) != "" {
			return "completion_gate_required_source_reads_remaining_repeated"
		}
		if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			return "completion_gate_submit_plan_invalid_repeated"
		}
		return "completion_gate_submit_plan_missing_repeated"
	}
	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return "completion_gate_required_paths_remaining_repeated"
	}
	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return "completion_gate_required_snippets_missing_repeated"
	}
	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
		if plannerOwnsVerification(r.RuntimeBrief) {
			return "completion_gate_lane_sanity_repeated"
		}
		return "completion_gate_verification_repeated"
	}
	return "completion_gate_repeated_rejection"
}

func (r *Runner) completionGateRecoveryPrompt(results []workspace.ToolResult) (prompt string, hideReads bool, ok bool) {
	if r == nil {
		return "", false, false
	}
	if prompt := r.readOnlyEvidenceCompletionRecoveryPrompt(results); prompt != "" {
		return prompt, false, true
	}
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		if reviewRepairSourceSanityRepairNeedsEvidence(r.RuntimeBrief, results) {
			return reviewRepairSourceSanityRepairEvidencePrompt(r.RuntimeBrief), false, true
		}
		return reviewRepairCompletionGateRecoveryPrompt(r.RuntimeBrief), true, true
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false, false
	}
	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		if r.planThenExecutePlannerPhase() {
			state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
			if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
				refreshPaths := state.RefreshPaths
				target := "the rejected required path"
				if len(refreshPaths) > 0 {
					target = strings.Join(refreshPaths, ", ")
				}
				return strings.Join([]string{
					"The completion gate rejected a final answer after `submit_plan` was rejected for stale or missing edit anchors.",
					"This is an anchor-refresh phase. Read/search/discovery is allowed only for one bounded source-body refresh on: " + target + ".",
					"Your next visible message must be exactly one parser-readable bounded `read_file` or `code_navigate` source-body request for one of those paths. Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or reply in prose until the bounded refresh returns.",
				}, "\n\n"), false, true
			}
			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results); nudge != "" {
				return strings.Join([]string{
					"The completion gate rejected a final answer before every required_edit_path produced source-body evidence.",
					"Do not draft `submit_plan` from outline/search hits. The next visible message must be exactly one parser-readable source-body tool call for the unread required path, or <<QUESTION>> with the exact path-specific blocker.",
					nudge,
				}, "\n\n"), false, true
			}
			if pending := planThenExecuteHistoryPendingPaths(r.RuntimeBrief, results, nil); len(pending) > 0 {
				return strings.Join([]string{
					"The completion gate rejected a final answer before required git-history evidence was produced for every required_edit_path.",
					"Source-body evidence is complete, but the planner is still in history-pending state. Do not call `submit_plan` yet and do not perform additional source reads.",
					"Your next visible message must be exactly one parser-readable `history_range` or `history_symbol` request for one pending path, or `<<QUESTION>>:` with the exact path-specific blocker.",
					"Pending git-history path(s): " + strings.Join(pending, ", "),
				}, "\n\n"), false, true
			}
			correction := planThenExecuteSubmitPlanVisibleJSONInstruction()
			if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
				correctionBrief := lastSubmitPlanRejectionBrief(rejected)
				if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
					correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
				}
				correction = correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejected)
			}
			if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
				return fmt.Sprintf("The completion gate rejected a final answer before the corrected plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. Your next visible message must be exactly one parser-readable `submit_plan` request. %s Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not reply in prose. %s For a new file or whole-file write use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`.", strings.Join(missing, ", "), planThenExecuteSubmitPlanCoverageSentence(), correction), true, true
			}
			return fmt.Sprintf("The completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. Your next visible message must be exactly one parser-readable action, not the same prose. Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. If the gathered evidence supports the requested changes, your next response must be a parser-readable `submit_plan` request, not prose. %s %s For a new file or whole-file write use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. If a required missing source/test path has only behavior/helper requirements and no substantial inline body or explicit replacement shape, answer with <<QUESTION>> naming the missing body evidence. The other valid outcome is `Decision: source-satisfied` naming every path plus current-state evidence if the requested final state is already present. Do not call read_file, search_content, context_pack, context_query, code_navigate, or any other discovery tool.", strings.Join(missing, ", "), planThenExecuteSubmitPlanCoverageSentence(), correction), true, true
		}
		if briefIsScratchpadDeliverableLane(r.RuntimeBrief) {
			return fmt.Sprintf("The completion gate rejected a final answer while non-source deliverable path(s) remain unwritten: %s. The deliverable-write contract requires full corrected content with gathered evidence plus any missing, stale, ambiguous, or out-of-scope source fact recorded as `Source verification pending:` or `## Scope Correction Required`. Answer with <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty.", strings.Join(missing, ", ")), false, true
		}
		if implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results) {
			return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, true), true, true
		}
		return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, false), false, true
	}
	if missing := r.missingRequiredEditSnippets(results); len(missing) > 0 {
		return fmt.Sprintf("The completion gate rejected a final answer while required edit snippet(s) are still missing: %s. Read/search tools are hidden for this turn. The available contract outcomes are snippet-backed write capability or <<QUESTION>> naming the exact blocker for each missing snippet; do not perform additional discovery.", strings.Join(missing, ", ")), true, true
	}
	if r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) == 0 && successfulSubmitPlanResultCount(results) == 0 {
		gateRejected := completionGateResultSeen(results)
		gateRejections := completionGateResultCount(results)
		readPathSet := readPathsFromResults(results)
		readPaths := make([]string, 0, len(readPathSet))
		for path := range readPathSet {
			readPaths = append(readPaths, path)
		}
		sort.Strings(readPaths)
		candidatePaths := candidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
		for _, path := range readPaths {
			if !slices.Contains(candidatePaths, path) {
				candidatePaths = append([]string{path}, candidatePaths...)
			}
		}
		targetSummary := "No concrete candidate path has been read yet."
		nextAction := "If one edit-safety fact is missing, call one bounded read/search on the highest-confidence candidate path with a named missing edit-safety fact."
		hideReads := false
		if len(candidatePaths) > 0 {
			targetSummary = "Current candidate path evidence: " + strings.Join(candidatePaths, ", ")
			nextAction = fmt.Sprintf("If one edit-safety fact is missing, read the highest-confidence candidate path now: `read_file` on `%s` with a named missing edit-safety fact. Otherwise call `submit_plan`.", candidatePaths[0])
		}
		if len(readPaths) > 0 {
			targetSummary = "Read candidate path(s): " + strings.Join(readPaths, ", ")
			if gateRejected && gateRejections >= 2 {
				nextAction = "Read/search tools are hidden for this recovery turn because the planner already attempted to close after source evidence. Allowed/candidate paths are not coverage obligations. The next visible action must be one terminal planner outcome: call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
				hideReads = true
			} else if gateRejected {
				if unread := unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 8); len(unread) > 0 {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If the rejected answer was premature because one concrete edit-safety fact is still missing on another source/test surface, make exactly one bounded source read for a different unread candidate path with a named edit-safety fact: " + strings.Join(unread, ", ") + "."
				} else {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If one concrete edit-safety fact is still missing, make exactly one bounded read/search request naming that missing fact instead of repeating the final-answer prose."
				}
			} else if unread := unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 4); len(unread) > 0 {
				nextAction = "Allowed/candidate paths are not coverage obligations. Call `submit_plan` using exact source anchors from the read output when enough evidence exists. If source-satisfied closeout depends on another candidate path, read one different unread candidate path with a bounded `read_file` range now: " + strings.Join(unread, ", ") + ". Do not reread a candidate path that already has source evidence."
			} else {
				nextAction = "Read/search tools are hidden for this recovery turn because the surfaced candidate source has already been read. Allowed/candidate paths are not coverage obligations. Call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. Do not request another read."
				hideReads = true
			}
		}
		return strings.Join([]string{
			"The completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced an artifact for candidate/allowed scope. No required_edit_paths list is active, so choose the needed candidate path(s) from the objective and target refs instead of treating the allowlist as coverage.",
			targetSummary,
			"Your next visible message must be exactly one parser-readable action, not the same prose. Do not include analysis before or after the action.",
			"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. The `submit_plan`, `Decision: source-satisfied`, `<<QUESTION>>:` outcome must appear in visible content or a native tool call.",
			"For textual tool transport, any bounded read/search/navigation action is one visible JSON object with a top-level `tool` field plus that tool's arguments, for example `" + workspace.CodeNavigateRequestExample(workspace.CapReadSymbol, "path/from/evidence.go", "SymbolFromEvidence") + "`. XML-like tags and key-value notes are not accepted tool calls unless they contain a parser-readable JSON object.",
			"For textual tool transport, a `submit_plan` action is one visible JSON object with top-level `tool`, `rationale`, and `edits` keys; `edits` must be a JSON array, not a quoted string.",
			"When submitting, each `submit_plan.edits[]` entry must use a real candidate path plus exactly one executable shape: exact `old_text`/`new_text`, replacement `start_line`/`end_line`/`content`, small inline insertion-only `insert_after_line`/`content`, or whole/new-file `replace_all:true`/`new_text`; include a one-sentence `purpose`. For large additions, use line-range replacement of unchanged anchor line(s) plus inserted content. Edit bodies must be complete executable source text; do not copy schema examples, placeholders, ellipses, or prose as field values.",
			nextAction,
			"The only non-tool closeouts are a visible answer starting exactly with `Decision: source-satisfied` plus current-state evidence for the relevant path(s), or a visible `<<QUESTION>>:` naming the exact blocker. Do not call per-edit write tools.",
		}, "\n\n"), hideReads, true
	}
	if r.unscopedNoWriteCompletionRecoveryApplies(results) {
		if r.unscopedNoWriteTerminalRecoveryReady(results) {
			return r.unscopedNoWriteTerminalRecoveryPrompt(results), true, true
		}
		candidatePaths := concreteRecoveryEvidencePaths(results, 4)
		writeStep := "Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`."
		if len(candidatePaths) > 0 {
			writeStep = fmt.Sprintf("Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. Candidate path(s) with evidence so far: %s. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`, using only real path and text values from the source evidence. Do not copy placeholder edit bodies or schema examples.", strings.Join(candidatePaths, ", "))
		}
		targetDiscoveryStep := "If the edit target is not identified, call `context_pack` for the user's objective rather than repeating a literal search that already missed. Use `mode:\"plan\"`, a small limit, and a task value copied from the actual user objective rather than a placeholder."
		if len(candidatePaths) > 0 {
			targetDiscoveryStep = fmt.Sprintf("Current evidence paths already identify candidate target(s): %s.\n\nIf one edit-safety fact is still missing, read the highest-confidence candidate instead of restarting discovery:\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"%s\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"exact current source for the requested behavior\",\"why_not_answering\":\"need current source before the write\"}\n```\n\nDo not call `context_pack` again unless every listed candidate is proven irrelevant.", strings.Join(candidatePaths, ", "), candidatePaths[0])
		} else if contextPackUnavailableForRecovery(results) {
			targetDiscoveryStep = "The previous `context_pack` result failed or returned no usable target candidates, so do not call `context_pack` again in this recovery. Run a bounded fallback discovery phase instead: first `search_files` for likely filenames/components/domain terms, then `search_content` for stable labels, symbols, config keys, command names, or error text. If a literal phrase missed, broaden to nearby concepts. Read the highest-confidence candidate with `read_file` or a language-aware symbol tool before writing."
		} else if contextPackSeen(results) {
			targetDiscoveryStep = "The previous `context_pack` did not establish an exact edit target. Do not write or read a context-pack candidate unless it contains exact current-state evidence for the requested behavior. Run bounded fallback discovery instead: use `search_content` for stable domain terms, symbols, config keys, command names, or error text from the user's objective, then read the highest-confidence hit before writing."
		}
		return strings.Join([]string{
			"The completion gate rejected a final answer before any successful workspace write. No required_edit_paths list is active, so this is a target-selection failure, not a required-path coverage failure.",
			"Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.",
			writeStep,
			textualMutationEditFileJSONInstruction(),
			targetDiscoveryStep,
			"If one edit-safety fact is missing after a concrete candidate exists, call exactly one bounded discovery/read tool with `named_gap` and `why_not_answering` that names the fact needed before the write.",
			"The only non-tool closeout is `<<QUESTION>>:` naming the exact blocker, such as no resolvable edit target, missing permission, or missing body evidence. Do not repeat a final answer without a tool-backed write.",
		}, "\n\n"), false, true
	}
	if failed, ok := failedVerificationAfterLatestWrite(results, r.RuntimeBrief); ok {
		return postWriteVerificationFailedRecoveryPrompt(failed, plannerOwnsVerification(r.RuntimeBrief)), false, true
	}
	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
		if plannerOwnsVerification(r.RuntimeBrief) {
			return postWriteVerificationPendingRecoveryPrompt(results, true), false, true
		}
		return postWriteVerificationPendingRecoveryPrompt(results, false), false, true
	}
	return "", false, false
}

func textualMutationWriteJSONInstruction() string {
	return "For textual tool transport, a write action is either one visible JSON object with a top-level `tool` field plus real source-backed arguments, or the Qwen key-value `tool:` format with the same real arguments. Function-call write syntax is not executable."
}

func textualMutationEditFileJSONInstruction() string {
	return textualMutationWriteJSONInstruction() + " For `edit_file`, include a real required path when one is active, exact `old_text` copied from source evidence, complete replacement `new_text`, and a one-sentence `description`. Do not copy placeholders, ellipses, schema examples, or prose as edit fields."
}

func (r *Runner) readOnlyEvidenceCompletionRecoveryPrompt(results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}
	if brief.Readiness == nil || brief.Readiness.ReadyToAnswer {
		return ""
	}
	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 || genericReadOnlyAnswerEvidenceReady(brief, results) {
		return ""
	}
	targets := pathparse.MentionedWorkspacePaths(brief.Objective)
	if len(targets) == 0 {
		targets = append(targets, brief.TargetRefs...)
	}
	readPaths := readPathsFromResults(results)
	target := ""
	for _, rawTarget := range targets {
		candidate := normalizeRunnerPath(rawTarget)
		if candidate == "" {
			continue
		}
		if _, alreadyRead := readPaths[candidate]; alreadyRead {
			continue
		}
		target = candidate
		break
	}
	if target != "" {
		return fmt.Sprintf("The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. The user named `%s`; make one parser-readable `read_file` request for that path now, then answer from the returned source. Do not edit files, run shell commands, or answer before the read result.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"%s\"}\n```", target, target)
	}
	if len(readPaths) > 0 {
		return "The completion gate rejected a read-only final answer because the gathered evidence does not yet include the codebase source needed to answer the explicit question. The named path(s) already read should not be reread. Make one targeted source search/read/navigation request for a different concrete code path tied to the missing claim, then answer from that evidence. Do not edit files or run shell commands."
	}
	return "The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. Make one targeted read/search/discovery request tied to the explicit question, then answer from the returned evidence. Do not edit files, run shell commands, or answer before evidence."
}

func postWriteVerificationPendingRecoveryPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "The completion gate rejected a final answer because")
}

func postWriteVerificationRequiredPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "A workspace write completed and")
}

func postWriteVerificationPrompt(results []workspace.ToolResult, plannerOwned bool, intro string) string {
	paths := changedPathListFromToolResults(results)
	target := "changed source files"
	if len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	scope := "post-write verification"
	requirement := "Required evidence is one focused language-appropriate verification command/tool for changed source files, compiler-backed diagnostics, or `<<QUESTION>>:` with the concrete verification blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."
	if plannerOwned {
		scope = "post-write lane sanity"
		requirement = "Required evidence is compiler-backed source-sanity or language diagnostics for changed source files, or `<<QUESTION>>:` with the concrete source-sanity blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."
	}
	action := "Call one parser-readable verification tool now."
	if len(paths) == 1 {
		action = "Choose the relevant language/project verification command or diagnostics route for this changed path and issue one parser-readable verification action now."
	}
	return strings.Join([]string{
		strings.TrimSpace(intro) + " " + scope + " is still unresolved for: " + target + ".",
		"Your next visible message must be one parser-readable verification action or `<<QUESTION>>:` blocker. Do not answer in prose, request more reads, or attempt additional edits in this verification phase.",
		requirement,
		action,
	}, "\n\n")
}

func (r *Runner) implementationRequiredPathCompletionRecoveryPrompt(missing []string, results []workspace.ToolResult, writeOnly bool) string {
	missing = nonEmptyRunnerStrings(missing, 12)
	target := "the remaining required edit path(s)"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}

	var b strings.Builder
	if writeOnly {
		b.WriteString("The completion gate rejected a final answer while exact-anchor required edit path(s) remain unchanged: ")
	} else {
		b.WriteString("The completion gate rejected a final answer while required edit path(s) remain unchanged: ")
	}
	b.WriteString(target)
	b.WriteString(".\n\n")
	b.WriteString("Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.\n\n")
	b.WriteString("Hidden-only reasoning is not completion evidence and may not be available to the tool parser. The contract outcome is a visible write, bounded read/discovery action, native tool call, or `<<QUESTION>>` blocker.\n\n")
	if scopedImplementationMissingPathsAreExistingSourceFiles(missing, r.Workspace) {
		b.WriteString("If the current evidence supports the requested change, call one bounded edit capability now. Use `apply_patch` for one coherent multi-file patch across required source/test paths, or `edit_file` with `old_text`+`new_text` / `start_line`+`end_line`+`content` for one remaining path. Do not use `write_file` or `append_file` for existing source/test edits in this recovery; `write_file` is whole-file replacement for new/small/doc deliverables, and `append_file` is only for true end-of-file appends.\n\n")
	} else {
		b.WriteString("If the current evidence supports the requested change, call one write capability now for a remaining required path: `edit_file`, `write_file`, `append_file`, or `apply_patch`. Use real current text, complete replacement text, or a valid patch; do not copy placeholders or ellipses.\n\n")
	}

	unread := make([]string, 0, len(missing))
	for _, path := range missing {
		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}
		unread = append(unread, path)
	}
	if writeOnly {
		b.WriteString("Read/search/discovery tools are hidden for this turn because the packet already claimed exact write anchors. If an anchor is stale or missing, answer `<<QUESTION>>:` and name the stale or missing anchor for each blocked remaining path.\n")
	} else if len(unread) > 0 {
		path := unread[0]
		nextRead := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         220,
			NamedGap:        "exact current source needed before editing this required path",
			WhyNotAnswering: "need current source evidence before the write",
		}
		if recoveredReq, _, recovered := r.deterministicImplementationRequiredPathRead(results); recovered {
			nextRead = recoveredReq
		}
		b.WriteString("If one edit-safety fact is still missing, call exactly one bounded discovery/read action for a remaining required path or declared project symbol. Prefer the next unread source-body request now:\n\n")
		b.WriteString("```tool\n")
		b.WriteString(marshalToolRequestRaw(nextRead))
		b.WriteByte('\n')
		b.WriteString("```\n\n")
		b.WriteString("Do not perform broad discovery or task planning. A semantic/context lookup is valid only when it names the exact edit-safety fact needed before writing a remaining required path.\n")
	} else {
		b.WriteString("Every remaining required path already has source-body anchor evidence. Do not restart broad discovery or task planning. Call a write capability, or answer `<<QUESTION>>:` with the exact path-specific blocker that prevents the write.\n")
		if scopedImplementationMissingPathsAreExistingSourceFiles(missing, r.Workspace) {
			b.WriteString("\n" + textualMutationEditFileJSONInstruction() + "\n")
		}
	}
	b.WriteString("\nThe only non-tool closeout is `<<QUESTION>>:` with one concrete blocker reason for every remaining required path.")
	return b.String()
}

func scopedImplementationMissingPathsAreExistingSourceFiles(paths []string, manager *workspace.Manager) bool {
	if len(paths) == 0 || manager == nil {
		return false
	}
	for _, path := range paths {
		relPath := cleanRelativeRunnerPath(path)
		if relPath == "" || writePathIsNonSource(relPath) {
			return false
		}
		info, err := os.Stat(filepath.Join(manager.Root(), filepath.FromSlash(relPath)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func postWriteVerificationFailedRecoveryPrompt(result workspace.ToolResult, plannerOwned bool) string {
	summary := strings.TrimSpace(result.Summary)
	detail := strings.TrimSpace(result.Error)
	if detail == "" {
		detail = strings.TrimSpace(result.Output)
	}
	if detail != "" {
		detail = truncateRunnerStateText(detail, 320)
	}
	var b strings.Builder
	if plannerOwned {
		b.WriteString("Post-write lane sanity failed after the required edits. Treat the failed sanity output as repair evidence, not as successful completion.\n\n")
	} else {
		b.WriteString("Post-write verification failed after the required edits. Treat the failed verification output as repair evidence, not as successful completion.\n\n")
	}
	if summary != "" {
		b.WriteString("Failed check: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if detail != "" {
		b.WriteString("Failure detail: ")
		b.WriteString(detail)
		b.WriteString("\n")
	}
	b.WriteString("\nNext action must be one parser-readable action. If the failure names a changed or required path, line, symbol, or type error, repair the affected in-scope file with a write tool. If exactly one edit-safety fact is missing, use one narrow read/language/context lookup for that fact, then repair. Rerun verification only after a repair write or when the failure is clearly environmental. Answer `<<QUESTION>>:` only for an external, permission, or environment blocker that prevents repair.")
	return b.String()
}

func contextPackUnavailableForRecovery(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "context_pack" {
			continue
		}
		if strings.TrimSpace(result.Error) != "" {
			return true
		}
		if !contextPackResultHasTargets(result) {
			return true
		}
	}
	return false
}

func (r *Runner) recoverContextPackFreshness(ctx context.Context, req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(req.Tool) != "context_pack" || !contextPackResultNeedsFreshnessRecovery(result) {
		return result
	}
	r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_pack freshness blocked; refreshing context status before retry"})
	refresh := workspace.ToolRequest{
		Tool:              "context_status",
		Refresh:           true,
		Limit:             50,
		FreshnessMaxFiles: 2000,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	}
	if strings.TrimSpace(req.Language) != "" {
		refresh.Language = req.Language
	}
	refreshResult := r.Workspace.Execute(ctx, refresh)
	if summary := strings.TrimSpace(util.FirstNonEmpty(refreshResult.Summary, refreshResult.Error)); summary != "" {
		r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_status refresh result: " + truncateRunnerStateText(summary, 260)})
	}
	retry := r.Workspace.Execute(ctx, req)
	if strings.TrimSpace(retry.Error) == "" || strings.TrimSpace(refreshResult.Error) == "" {
		return retry
	}
	return result
}

func contextPackResultNeedsFreshnessRecovery(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "freshness_blocker" {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	return strings.Contains(text, "freshness_blocker") ||
		strings.Contains(text, "stale_unbounded_graph") ||
		strings.Contains(text, "context freshness")
}

func contextPackSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "context_pack" {
			return true
		}
	}
	return false
}

func contextPackResultHasTargets(result workspace.ToolResult) bool {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return false
	}
	var view struct {
		CandidateFiles   []json.RawMessage `json:"candidate_files"`
		CandidateSymbols []json.RawMessage `json:"candidate_symbols"`
		Tests            []json.RawMessage `json:"tests"`
	}
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return false
	}
	return len(view.CandidateFiles) > 0 || len(view.CandidateSymbols) > 0 || len(view.Tests) > 0
}

// contextPackCandidateSymbols extracts the top-ranked symbol references from
// a context_pack view JSON. It prefers top-level candidate_symbols and packet
// quality evidence, then falls back to candidate_files.handles from older views.
func contextPackCandidateSymbols(output string, max int) []string {
	output = strings.TrimSpace(output)
	if output == "" || max <= 0 {
		return nil
	}
	var view struct {
		Task    string `json:"task"`
		Quality struct {
			EvidencePaths []string `json:"evidence_paths"`
		} `json:"quality"`
		CandidateFiles []struct {
			Path    string   `json:"path"`
			Role    string   `json:"role"`
			Reason  string   `json:"reason"`
			Handles []string `json:"handles"`
		} `json:"candidate_files"`
		CandidateSymbols []struct {
			Path          string `json:"path"`
			Kind          string `json:"kind"`
			QualifiedName string `json:"qualified_name"`
			Reason        string `json:"reason"`
		} `json:"candidate_symbols"`
	}
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return nil
	}
	evidenceRank := map[string]int{}
	for i, path := range view.Quality.EvidencePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := evidenceRank[path]; !ok {
			evidenceRank[path] = i
		}
	}
	tokens := runnerEvidenceTokens(view.Task)
	candidates := make([]contextPackSymbolCandidate, 0, len(view.CandidateSymbols))
	for _, symbol := range view.CandidateSymbols {
		path := strings.TrimSpace(symbol.Path)
		name := strings.TrimSpace(symbol.QualifiedName)
		if path == "" || name == "" {
			continue
		}
		candidates = append(candidates, contextPackSymbolCandidate{
			value:        path + "#" + name,
			path:         path,
			evidenceRank: contextPackEvidenceRank(evidenceRank, path),
			score:        runnerEvidenceOverlap(tokens, strings.Join([]string{path, symbol.Kind, name, symbol.Reason}, " ")),
		})
	}
	sortContextPackSymbolCandidates(candidates)
	var symbols []string
	seen := map[string]struct{}{}
	addSymbol := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(symbols) >= max {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		symbols = append(symbols, value)
	}
	for _, candidate := range candidates {
		addSymbol(candidate.value)
	}
	for _, f := range view.CandidateFiles {
		if len(symbols) >= max {
			break
		}
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		if strings.TrimSpace(f.Role) == "test" {
			continue
		}
		for _, handle := range f.Handles {
			if len(symbols) >= max {
				break
			}
			handle = strings.TrimPrefix(strings.TrimSpace(handle), "symbol://")
			if handle == "" || handle == path {
				continue
			}
			addSymbol(handle)
		}
	}
	if len(symbols) == 0 {
		return nil
	}
	return symbols
}

type contextPackSymbolCandidate struct {
	value        string
	path         string
	evidenceRank int
	score        int
}

func contextPackEvidenceRank(ranks map[string]int, path string) int {
	if len(ranks) == 0 {
		return -1
	}
	if rank, ok := ranks[strings.TrimSpace(path)]; ok {
		return rank
	}
	return -1
}

func sortContextPackSymbolCandidates(candidates []contextPackSymbolCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].evidenceRank >= 0 || candidates[j].evidenceRank >= 0 {
			if candidates[i].evidenceRank < 0 {
				return false
			}
			if candidates[j].evidenceRank < 0 {
				return true
			}
			if candidates[i].evidenceRank != candidates[j].evidenceRank {
				return candidates[i].evidenceRank < candidates[j].evidenceRank
			}
		}
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].value < candidates[j].value
	})
}

func concreteRecoveryEvidencePaths(results []workspace.ToolResult, max int) []string {
	filtered := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		switch strings.TrimSpace(result.Tool) {
		case "context_pack", "completion_gate":
			continue
		default:
			filtered = append(filtered, result)
		}
	}
	return promptEvidencePathsFromToolResults(filtered, max)
}

func discoveryConsumptionCandidatesFromResult(result workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	if strings.TrimSpace(result.Error) != "" {
		return nil
	}
	if strings.TrimSpace(result.Tool) == "context_pack" {
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, max)
	}
	return promptEvidencePathsFromToolResults([]workspace.ToolResult{result}, max)
}

func recoveryEvidencePathsForObjective(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	searchRanked := rankedSearchContentEvidencePaths(objective, results, max)
	if len(searchRanked) >= max {
		return searchRanked[:max]
	}
	seen := make(map[string]struct{}, len(searchRanked))
	paths := make([]string, 0, max)
	for _, path := range searchRanked {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, path := range concreteRecoveryEvidencePaths(results, max*2) {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}
	return paths
}

func candidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	seen := make(map[string]struct{}, max)
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" {
			return
		}
		if !planThenExecuteRecoveryScopePathAllowed(path) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, path := range brief.AllowedEditPaths {
		add(path)
		if len(paths) >= max {
			return paths
		}
	}
	for _, path := range recoveryEvidencePathsForObjective(brief.Objective, results, max) {
		add(path)
		if len(paths) >= max {
			return paths
		}
	}
	for _, path := range contextPackCandidatePathsFromResults(results, max) {
		add(path)
		if len(paths) >= max {
			return paths
		}
	}
	return paths
}

func contextPackCandidatePathsFromResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	addRanked := func(candidates []contextPackEvidenceCandidate) bool {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			if candidates[i].source != candidates[j].source {
				return candidates[i].source
			}
			return candidates[i].path < candidates[j].path
		})
		for _, candidate := range candidates {
			if candidate.score <= 0 {
				continue
			}
			add(candidate.path)
			if len(paths) >= max {
				return true
			}
		}
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "context_pack" || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		var view struct {
			Task    string `json:"task"`
			Quality struct {
				LocalizationConfidence string   `json:"localization_confidence"`
				SourceOwnerFound       bool     `json:"source_owner_found"`
				EvidencePaths          []string `json:"evidence_paths"`
			} `json:"quality"`
			CandidateFiles []struct {
				Path    string   `json:"path"`
				Role    string   `json:"role"`
				Reason  string   `json:"reason"`
				Handles []string `json:"handles"`
			} `json:"candidate_files"`
			CandidateSymbols []struct {
				Path          string `json:"path"`
				Kind          string `json:"kind"`
				QualifiedName string `json:"qualified_name"`
				Reason        string `json:"reason"`
			} `json:"candidate_symbols"`
			Tests []struct {
				Path    string `json:"path"`
				Symbol  string `json:"symbol"`
				Command string `json:"command"`
				Reason  string `json:"reason"`
			} `json:"tests"`
		}
		if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
			continue
		}
		weakQuality := strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
		for _, path := range view.Quality.EvidencePaths {
			if weakQuality && !runnerEvidencePathIsTest(path) {
				continue
			}
			add(path)
			if len(paths) >= max {
				return paths
			}
		}
		if tokens := runnerEvidenceTokens(view.Task); len(tokens) > 0 {
			candidates := make([]contextPackEvidenceCandidate, 0, len(view.CandidateFiles)+len(view.CandidateSymbols)+len(view.Tests))
			for _, file := range view.CandidateFiles {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   file.Path,
					source: !runnerEvidencePathIsTest(file.Path),
					score:  runnerEvidenceOverlap(tokens, strings.Join(append([]string{file.Path, file.Role, file.Reason}, file.Handles...), " ")),
				})
			}
			for _, symbol := range view.CandidateSymbols {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   symbol.Path,
					source: !runnerEvidencePathIsTest(symbol.Path) && symbol.Kind != "test",
					score:  runnerEvidenceOverlap(tokens, strings.Join([]string{symbol.Path, symbol.Kind, symbol.QualifiedName, symbol.Reason}, " ")),
				})
			}
			for _, test := range view.Tests {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:  test.Path,
					score: runnerEvidenceOverlap(tokens, strings.Join([]string{test.Path, test.Symbol, test.Command, test.Reason}, " ")),
				})
			}
			if addRanked(candidates) {
				return paths
			}
		}
		for _, file := range view.CandidateFiles {
			add(file.Path)
			if len(paths) >= max {
				return paths
			}
		}
		for _, symbol := range view.CandidateSymbols {
			add(symbol.Path)
			if len(paths) >= max {
				return paths
			}
		}
		for _, test := range view.Tests {
			add(test.Path)
			if len(paths) >= max {
				return paths
			}
		}
	}
	return paths
}

type contextPackEvidenceCandidate struct {
	path   string
	source bool
	score  int
}

func contextPackResultWeakSourceOwner(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "context_pack" || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
		return false
	}
	var view struct {
		Quality struct {
			LocalizationConfidence string `json:"localization_confidence"`
			SourceOwnerFound       bool   `json:"source_owner_found"`
		} `json:"quality"`
	}
	if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
		return false
	}
	return strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
}

func runnerEvidenceTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range splitRunnerEvidenceText(text) {
		if len(token) < 3 || runnerEvidenceStopToken(token) {
			continue
		}
		out[runnerEvidenceStem(token)] = struct{}{}
	}
	return out
}

func runnerEvidenceOverlap(tokens map[string]struct{}, text string) int {
	seen := map[string]struct{}{}
	for _, token := range splitRunnerEvidenceText(text) {
		stem := runnerEvidenceStem(token)
		if _, ok := tokens[stem]; ok {
			seen[stem] = struct{}{}
		}
	}
	return len(seen)
}

func splitRunnerEvidenceText(text string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			flush()
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func runnerEvidenceStem(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return runnerEvidenceTrimDoubledFinalConsonant(strings.TrimSuffix(token, suffix))
		}
	}
	return token
}

func runnerEvidenceTrimDoubledFinalConsonant(token string) string {
	if len(token) < 2 {
		return token
	}
	last := token[len(token)-1]
	prev := token[len(token)-2]
	if last == prev {
		switch last {
		case 'b', 'd', 'g', 'm', 'n', 'p', 'r', 't':
			return token[:len(token)-1]
		}
	}
	return token
}

func runnerEvidenceStopToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "where", "when", "that", "this", "from", "into", "over",
		"do", "not", "no", "only", "same", "way", "longer", "first", "later", "time",
		"bug", "fix", "issue", "fail", "fails", "failure", "source", "file", "files", "symbol", "symbols",
		"research", "repo", "repository", "edit", "edits", "editing", "investigate", "identify", "name",
		"likely", "smallest", "proper", "propose", "return", "answer", "evidence", "codebase", "exact",
		"missing", "insufficient", "sufficient", "claim":
		return true
	default:
		return false
	}
}

func runnerEvidencePathIsTest(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/fixtures/") ||
		strings.Contains(path, "/tests/")
}

func unreadCandidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}
	read := readPathsFromResults(results)
	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; ok {
			return nil
		}
	}
	unread := make([]string, 0, len(candidates))
	unread = append(unread, candidates...)
	return unread
}

func unreadCandidateRecoveryScopePathsForRecovery(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}
	read := readPathsFromResults(results)
	unread := make([]string, 0, len(candidates))
	for _, path := range candidates {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if candidateRecoveryScopePathHasMissingFileEvidence(normalized, results) {
			continue
		}
		if _, ok := read[normalized]; ok {
			continue
		}
		unread = append(unread, normalized)
	}
	return unread
}

func candidateRecoveryScopePathHasMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}
	lowerPath := strings.ToLower(normalized)
	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}
		if strings.Contains(text, "file not found") ||
			strings.Contains(text, "no such file") ||
			strings.Contains(text, "cannot find the file") ||
			strings.Contains(text, "does not exist") {
			return true
		}
	}
	return false
}

func rankedSearchContentEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}
	terms := recoveryObjectiveSignalTerms(objective)
	lowerObjective := strings.ToLower(objective)
	uiObjective := objectiveMentionsUI(lowerObjective)
	type scoredPath struct {
		path  string
		score int
		order int
	}
	scores := map[string]*scoredPath{}
	order := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, line := range strings.Split(result.Output, "\n") {
			path, text, ok := searchContentPathAndText(line)
			if !ok {
				continue
			}
			entry, exists := scores[path]
			if !exists {
				entry = &scoredPath{path: path, order: order}
				scores[path] = entry
				order++
			}
			entry.score += recoverySearchLineScore(path, text, terms, uiObjective)
		}
	}
	if len(scores) == 0 {
		return nil
	}
	paths := make([]scoredPath, 0, len(scores))
	for _, entry := range scores {
		paths = append(paths, *entry)
	}
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].score != paths[j].score {
			return paths[i].score > paths[j].score
		}
		return paths[i].order < paths[j].order
	})
	out := make([]string, 0, min(max, len(paths)))
	for _, entry := range paths {
		if entry.score <= -50 {
			continue
		}
		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}
	return out
}

func searchContentPathAndText(line string) (path string, text string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", "", false
	}
	before := strings.TrimSpace(line[:firstColon])
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return "", "", false
	}
	path = fields[len(fields)-1]
	if !recoverySearchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}
	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")
	if secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}
	return filepath.ToSlash(path), text, true
}

func recoverySearchLineScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1
	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			score += 10
		}
		if strings.Contains(lowerText, term) {
			score += 12
		}
	}
	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}
	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}
	if strings.Contains(lowerPath, "tool_schemas.go") || strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}
	if strings.HasPrefix(lowerPath, "cmd/") && !containsAny(lowerText, "implementation", "core", "internal") {
		score -= 20
	}
	if slices.Contains(terms, "apply") && containsAny(lowerPath, "migration_apply", "_apply.", "/apply") {
		score += 60
	}
	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/app/") {
			score += 45
		}
		if containsAny(lowerPath, "render", "view", "model_state", "update_handlers") {
			score += 25
		}
		if containsAny(lowerPath, "frontier/", "comprehension/", "workspace/") {
			score -= 25
		}
	}
	return score
}

func recoveryObjectiveSignalTerms(objective string) []string {
	pattern := literalRecoverySearchPattern(objective)
	if pattern == "" {
		return nil
	}
	parts := strings.Split(pattern, "|")
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		terms = append(terms, strings.ReplaceAll(part, `\`, ""))
	}
	return terms
}

func objectiveMentionsUI(lowerObjective string) bool {
	return containsAny(lowerObjective, "tui", "ui", "banner", "header", "footer", "top-right", "top right", "window", "screen", "view", "render")
}

func (r *Runner) salvageToolRequestFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return workspace.ToolRequest{}, "", false
	}
	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return workspace.ToolRequest{}, "", false
	}
	for _, candidate := range reasoningToolRequestCandidates(reasoning) {
		req, raw, ok, err := workspace.ParseToolRequest(candidate)
		if !ok || err != nil {
			continue
		}
		if r.safeReasoningToolRequest(req, results) {
			return req, raw, true
		}
	}
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) salvageVisibleToolRequestFromProse(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil {
		return workspace.ToolRequest{}, "", false
	}
	trimmed := strings.TrimSpace(reply)
	if req, raw, ok := parseVisibleLineToolRequest(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}
	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}
	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	raw := trimmed[start:end]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return workspace.ToolRequest{}, "", false
	}
	req, parsedRaw, ok := parseVisibleToolRequestJSONObject(raw, fields)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" &&
		!looksLikeUnexecutedToolRequestAnswer(trimmed) &&
		!r.explicitVisibleToolJSONRecoveryAllowed(trimmed, fields, results) {
		return workspace.ToolRequest{}, "", false
	}
	if r.safeVisibleToolRequest(req, results) {
		return req, parsedRaw, true
	}
	if repaired, repairedRaw, repairedOK := r.delegatedReadOnlyVisibleDiscoveryRepair(results, req); repairedOK && r.safeVisibleToolRequest(repaired, results) {
		return repaired, repairedRaw, true
	}
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) delegatedReadOnlyVisibleDiscoveryRepair(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, string, bool) {
	if r == nil || len(results) > 0 || strings.TrimSpace(req.Tool) != "discovery_query" {
		return workspace.ToolRequest{}, "", false
	}
	repaired, ok := r.delegatedReadOnlyDiscoveryQueryRepairRequest(results, req)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	raw := marshalToolRequestRaw(repaired)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}
	return repaired, raw, true
}

func parseMalformedVisibleReadOnlyToolJSONPrefix(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return workspace.ToolRequest{}, "", false
	}
	if _, _, ok := firstToolRequestShapedJSONObjectSpan(text); ok {
		return workspace.ToolRequest{}, "", false
	}
	start := strings.Index(text, "{")
	if start < 0 || markdownFenceOpenBefore(text, start) {
		return workspace.ToolRequest{}, "", false
	}
	candidate := text[start:]
	if !strings.Contains(candidate, `"tool"`) {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            firstJSONLikeStringField(candidate, "tool"),
		Path:            firstJSONLikeStringField(candidate, "path"),
		Pattern:         firstJSONLikeStringField(candidate, "pattern"),
		Query:           firstJSONLikeStringField(candidate, "query"),
		Glob:            firstJSONLikeStringField(candidate, "glob"),
		Capability:      firstJSONLikeStringField(candidate, "capability"),
		NamedGap:        firstJSONLikeStringField(candidate, "named_gap"),
		WhyNotAnswering: firstJSONLikeStringField(candidate, "why_not_answering"),
		Symbol:          firstJSONLikeStringField(candidate, "symbol"),
	}
	if v, ok := firstJSONLikeIntField(candidate, "start_line"); ok {
		req.StartLine = v
	}
	if v, ok := firstJSONLikeIntField(candidate, "end_line"); ok {
		req.EndLine = v
	}
	if v, ok := firstJSONLikeIntField(candidate, "limit"); ok {
		req.Limit = v
	}
	if v, ok := firstJSONLikeIntField(candidate, "budget_tokens"); ok {
		req.BudgetTokens = v
	}
	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}
	if !malformedVisibleReadOnlyToolCanSalvage(req.Tool) {
		return workspace.ToolRequest{}, "", false
	}
	raw := marshalToolRequestRaw(req)
	return req, raw, true
}

func malformedVisibleReadOnlyToolCanSalvage(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file":
		return true
	case "search_content", "search_files", "context_pack", "context_query", "context_zoom", "discovery_query", "structural_search", "file_stats", "code_navigate":
		return true
	default:
		return false
	}
}

func firstJSONLikeStringField(text string, key string) string {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"((?:\\.|[^"\\])*)"`)
	match := pattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	decoded, err := strconv.Unquote(`"` + match[1] + `"`)
	if err != nil {
		return strings.TrimSpace(match[1])
	}
	return strings.TrimSpace(decoded)
}

func firstJSONLikeIntField(text string, key string) (int, bool) {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"?([0-9]+)"?`)
	match := pattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

func (r *Runner) explicitVisibleToolJSONRecoveryAllowed(text string, fields map[string]json.RawMessage, results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) {
		return false
	}
	rawTool, ok := fields["tool"]
	if !ok {
		return false
	}
	var tool string
	if err := json.Unmarshal(rawTool, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return false
	}
	return countToolRequestShapedJSONObjects(text) == 1
}

func countToolRequestShapedJSONObjects(text string) int {
	count := 0
	for searchFrom := 0; searchFrom < len(text); {
		start, end, ok := firstToolRequestShapedJSONObjectSpan(text[searchFrom:])
		if !ok {
			break
		}
		count++
		searchFrom += end
		if start == end {
			searchFrom++
		}
	}
	return count
}

func parseVisibleLineToolRequest(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "{") || strings.Contains(text, "```") {
		return workspace.ToolRequest{}, "", false
	}
	// Scan for the first tool: line, skipping leading prose.
	// Models sometimes emit conversational text before a tool call.
	lines := strings.Split(text, "\n")
	toolLineIdx := -1
	proseLines := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "tool") {
			toolLineIdx = i
			break
		}
		proseLines++
		if proseLines > 10 {
			return workspace.ToolRequest{}, "", false
		}
	}
	if toolLineIdx < 0 {
		return workspace.ToolRequest{}, "", false
	}
	// Parse from the tool: line onward.
	values := map[string]string{}
	for _, line := range lines[toolLineIdx:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return workspace.ToolRequest{}, "", false
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || value == "" {
			return workspace.ToolRequest{}, "", false
		}
		switch key {
		case "tool", "path", "pattern", "query", "capability", "mode", "task", "named_gap", "why_not_answering",
			"start_line", "end_line", "limit", "budget_tokens":
			values[key] = value
		default:
			return workspace.ToolRequest{}, "", false
		}
	}
	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            tool,
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Capability:      values["capability"],
		Mode:            values["mode"],
		Task:            values["task"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
	}
	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}
			assign(v)
		}
	}
	return req, marshalToolRequestRaw(req), true
}

func parseVisibleToolRequestJSONObject(raw string, fields map[string]json.RawMessage) (workspace.ToolRequest, string, bool) {
	if _, hasExplicitTool := fields["tool"]; hasExplicitTool {
		req, parsedRaw, ok, err := workspace.ParseToolRequest(raw)
		if !ok || err != nil {
			return workspace.ToolRequest{}, "", false
		}
		return req, parsedRaw, true
	}
	if _, hasEdits := fields["edits"]; hasEdits {
		if _, hasRationale := fields["rationale"]; hasRationale {
			req, err := workspace.DecodeToolRequestJSON([]byte(raw))
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}
			req.Tool = "submit_plan"
			return req, marshalToolRequestRaw(req), true
		}
	}
	if _, hasPlanArtifact := fields["plan_artifact"]; hasPlanArtifact {
		req, err := workspace.DecodeToolRequestJSON([]byte(raw))
		if err != nil {
			return workspace.ToolRequest{}, "", false
		}
		req.Tool = "submit_plan"
		return req, marshalToolRequestRaw(req), true
	}
	if _, hasCapability := fields["capability"]; !hasCapability {
		return workspace.ToolRequest{}, "", false
	}
	req, err := workspace.DecodeToolRequestJSON([]byte(raw))
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}
	if strings.TrimSpace(req.Tool) == "" && strings.TrimSpace(req.Capability) != "" {
		if !visibleCodeNavigateCapability(req.Capability) {
			return workspace.ToolRequest{}, "", false
		}
		req.Tool = "code_navigate"
	}
	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}
	return req, marshalToolRequestRaw(req), true
}

func visibleCodeNavigateCapability(value string) bool {
	return workspace.CodeNavigateCapabilityAlias(value) != ""
}

func (r *Runner) safeVisibleToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) == "submit_plan" {
		return r.safeVisibleSubmitPlanRequest(req, results)
	}
	if r.safeVisiblePlanExecuteMissingTargetReadRequest(req) {
		return true
	}
	if r.safeVisibleWorkspaceWriteRequest(req, results) {
		return true
	}
	return r.safeReasoningToolRequest(req, results)
}

func (r *Runner) safeVisibleWorkspaceWriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	if reasoningToolRequestHasPlaceholder(req) || !safeWorkspaceWriteRequestShape(req) {
		return false
	}
	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}
	if len(r.RuntimeBrief.RequiredEditPaths) > 0 {
		return r.safeReasoningWorkspaceWriteRequest(req)
	}
	if !completionGateResultSeen(results) {
		return false
	}
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !concreteSourceEvidenceReadForPath(path, results) {
			return false
		}
	}
	return true
}

func (r *Runner) safeVisiblePlanExecuteMissingTargetReadRequest(req workspace.ToolRequest) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	return planThenExecuteReadRequestMissingConcreteTarget(req)
}

func (r *Runner) safeVisibleSubmitPlanRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return false
	}
	if reasoningToolRequestHasPlaceholder(req) {
		return false
	}
	if !r.planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results) {
		return false
	}
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}
	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}
	required := r.requiredEditCoveragePaths()
	if len(required) == 0 {
		return len(covered) > 0
	}
	return len(missingRequiredEditPaths(required, covered)) == 0
}

func (r *Runner) planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}
	return planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) ||
		planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) ||
		planThenExecuteRejectedPlanCorrectionOnly(r.RuntimeBrief, results)
}

func reasoningToolRequestCandidates(reasoning string) []string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}
	candidates := make([]string, 0, 3)
	for offset := 0; offset < len(reasoning); {
		idx := strings.Index(reasoning[offset:], "```tool")
		if idx < 0 {
			break
		}
		start := offset + idx
		rest := reasoning[start:]
		end := strings.Index(rest, "\n```")
		if end < 0 {
			break
		}
		candidates = append(candidates, rest[:end+4])
		offset = start + end + 4
	}
	if strings.Contains(reasoning, "<tool_call>") || strings.Contains(reasoning, "<function=") {
		candidates = append(candidates, reasoning)
	}
	if strings.Contains(reasoning, `"tool"`) {
		candidates = append(candidates, reasoning)
	}
	return candidates
}

func (r *Runner) safeReasoningToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || reasoningToolRequestHasPlaceholder(req) {
		return false
	}
	switch tool {
	case "read_file":
		return strings.TrimSpace(req.Path) != ""
	case "search_content":
		return strings.TrimSpace(req.Pattern) != ""
	case "search_files":
		return strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Glob) != ""
	case "context_pack":
		return strings.TrimSpace(req.Task) != "" && !contextPackUnavailableForRecovery(results)
	case "context_query":
		return strings.TrimSpace(req.Query) != ""
	case "context_zoom":
		return strings.TrimSpace(req.Handle) != ""
	case "discovery_query":
		return safeDiscoveryQueryRequest(req)
	case "structural_search":
		return strings.TrimSpace(req.Pattern) != ""
	case "code_navigate":
		return visibleCodeNavigateCapability(req.Capability) &&
			(strings.TrimSpace(req.Path) != "" || strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Query) != "")
	case "submit_plan":
		return false
	default:
		return r.safeReasoningWorkspaceWriteRequest(req)
	}
}

func safeDiscoveryQueryRequest(req workspace.ToolRequest) bool {
	if strings.TrimSpace(util.FirstNonEmpty(req.Purpose, req.Query, req.Pattern, req.Path)) == "" {
		return false
	}
	if purpose := strings.TrimSpace(req.Purpose); purpose != "" {
		return normalizeRunnerDiscoveryQueryPurpose(purpose) != ""
	}
	if query := strings.TrimSpace(req.Query); query != "" {
		return normalizeRunnerDiscoveryQueryPurpose(query) != ""
	}
	return true
}

func normalizeRunnerDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "impact", "impact_analysis":
		return "impact_analysis"
	case "trace", "function_trace", "function_tracing":
		return "function_tracing"
	case "architecture", "architecture_boundary", "boundary", "tool_usefulness", "tooling_usefulness", "discovery_tooling", "discovery_tools", "usefulness":
		return "architecture_boundary"
	case "security", "security_review":
		return "security_review"
	case "history", "history_context":
		return "history_context"
	default:
		if strings.Contains(value, "security") || strings.Contains(value, "vulnerability") {
			return "security_review"
		}
		if strings.Contains(value, "history") || strings.Contains(value, "evolution") {
			return "history_context"
		}
		if strings.Contains(value, "impact") || strings.Contains(value, "blast_radius") {
			return "impact_analysis"
		}
		if strings.Contains(value, "trace") || strings.Contains(value, "tracing") || strings.Contains(value, "call_graph") {
			return "function_tracing"
		}
		if strings.Contains(value, "architecture") ||
			strings.Contains(value, "boundary") ||
			(strings.Contains(value, "discovery") && (strings.Contains(value, "implementation") || strings.Contains(value, "definition") || strings.Contains(value, "orchestration") || strings.Contains(value, "pattern"))) {
			return "architecture_boundary"
		}
		return ""
	}
}

func (r *Runner) safeReasoningWorkspaceWriteRequest(req workspace.ToolRequest) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}
	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}
	return safeWorkspaceWriteRequestShape(req)
}

func safeWorkspaceWriteRequestShape(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "edit_file":
		hasOldNew := strings.TrimSpace(req.OldText) != "" && (strings.TrimSpace(req.NewText) != "" || req.NewText == "")
		hasRange := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""
		return hasOldNew || hasRange
	case "write_file", "append_file":
		return strings.TrimSpace(req.Content) != ""
	case "apply_patch":
		if strings.TrimSpace(req.Content) == "" {
			return false
		}
		_, err := workspace.ParsePatch(req.Content)
		return err == nil
	default:
		return false
	}
}

func reasoningToolRequestHasPlaceholder(req workspace.ToolRequest) bool {
	fields := []string{
		req.Path,
		req.Task,
		req.Pattern,
		req.Query,
		req.Content,
		req.OldText,
		req.NewText,
		req.NamedGap,
		req.WhyNotAnswering,
		req.Rationale,
	}
	for _, edit := range reasoningSubmitPlanEdits(req) {
		fields = append(fields,
			edit.Path,
			edit.OldText,
			edit.NewText,
			edit.NewTextRef,
			edit.Content,
			edit.ContentRef,
			edit.Purpose,
		)
	}
	for _, field := range fields {
		lower := strings.ToLower(strings.TrimSpace(field))
		if lower == "" {
			continue
		}
		if containsAny(lower,
			"candidate/path.go",
			"relative/file",
			"exact current text",
			"exact current substring",
			"replacement text",
			"complete replacement text",
			"why these candidate path edits satisfy the objective",
			"one paragraph on the overall plan",
			"why this edit",
			"one sentence",
			"<full file body>",
			"<user objective>",
			"<path>",
			"<pattern>",
		) {
			return true
		}
	}
	return false
}

func reasoningSubmitPlanHasConcreteEdit(req workspace.ToolRequest) bool {
	edits := reasoningSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}
	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) == "" {
			continue
		}
		hasOldText := strings.TrimSpace(edit.OldText) != ""
		hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
		hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
		hasReplace := edit.ReplaceAll && (strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "")
		if (hasOldText && workspace.PlanEditHasExplicitReplacementText(edit)) || hasRange || hasReplace {
			return true
		}
	}
	return false
}

func reasoningSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]workspace.PlanEdit(nil), req.PlanArtifact.Edits...)
	}
	return append([]workspace.PlanEdit(nil), req.Edits...)
}

type deterministicEmptyReplyRecoveryStep struct {
	recover  func([]workspace.ToolResult) (workspace.ToolRequest, string, bool)
	runEvent func(workspace.ToolRequest) RunEvent
}

func (r *Runner) deterministicEmptyReplyRecovery(results []workspace.ToolResult) (workspace.ToolRequest, string, RunEvent, bool) {
	for _, step := range r.deterministicEmptyReplyRecoverySteps() {
		req, raw, ok := step.recover(results)
		if !ok {
			continue
		}
		return req, raw, step.runEvent(req), true
	}
	return workspace.ToolRequest{}, "", RunEvent{}, false
}

func (r *Runner) deterministicEmptyReplyRecoverySteps() []deterministicEmptyReplyRecoveryStep {
	return []deterministicEmptyReplyRecoveryStep{
		{
			recover: r.deterministicPlanExecuteRequiredSourceRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery found unread required edit path %s; issuing source read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicImplementationRequiredPathRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery found unread required edit path %s; issuing bounded read before another implementation attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicImplementationEvidencePathRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_evidence_read", Content: fmt.Sprintf("completion recovery found single implementation evidence path %s; issuing bounded read before another implementation attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryTargetRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_target_read", Content: fmt.Sprintf("completion recovery found candidate target %s in the runtime brief; issuing bounded read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoverySearch,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_search", Content: fmt.Sprintf("completion recovery found only context-packet candidates; issuing bounded literal search %q", req.Pattern)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryUnreadCandidateRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_read", Content: fmt.Sprintf("completion recovery found unread higher-confidence candidate path %s; issuing bounded read before another plan attempt", req.Path)}
			},
		},
		{
			recover: r.deterministicNoWriteRecoveryRead,
			runEvent: func(req workspace.ToolRequest) RunEvent {
				return RunEvent{Role: "completion_recovery_auto_read", Content: fmt.Sprintf("completion recovery found candidate path %s after literal search; issuing bounded read before another final-answer attempt", req.Path)}
			},
		},
	}
}

func (r *Runner) noWriteRecoveryAllowed(results []workspace.ToolResult) bool {
	return r != nil &&
		r.unscopedNoWriteCompletionRecoveryApplies(results) &&
		!r.unscopedImplementationWriteReadyAfterNoWriteGate(results)
}

func noWriteRecoveryReadRequest(path, namedGap, whyNotAnswering string) (workspace.ToolRequest, string, bool) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        namedGap,
		WhyNotAnswering: whyNotAnswering,
	}
	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicNoWriteRecoveryRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != "search_content" {
		return workspace.ToolRequest{}, "", false
	}
	paths := candidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
	if len(paths) == 0 || alreadyReadPath(results, paths[0]) {
		return workspace.ToolRequest{}, "", false
	}
	return noWriteRecoveryReadRequest(
		paths[0],
		"exact current code needed before writing the requested implementation change",
		"need current source before the write",
	)
}

func (r *Runner) deterministicPlanExecuteRequiredSourceRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	if successfulSubmitPlanResultCount(results) > 0 || lastNonGateTool(results) == "submit_plan" {
		return workspace.ToolRequest{}, "", false
	}
	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	next := ""
	for _, path := range r.requiredEditCoveragePaths() {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := unread[normalized]; !ok {
			continue
		}
		if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(normalized, results) || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicPlanExecuteRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}
			continue
		}
		next = normalized
		break
	}
	if next == "" {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            next,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence for required plan-execute edit path",
		WhyNotAnswering: "need current source before submit_plan can anchor the required edit",
	}
	return req, marshalToolRequestRaw(req), true
}

type planThenExecuteRequiredSourceSurfaceAction struct {
	Request  workspace.ToolRequest
	Raw      string
	Summary  string
	Error    string
	Redirect bool
}

func (r *Runner) planThenExecuteRequiredSourceSurfaceAction(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (planThenExecuteRequiredSourceSurfaceAction, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}
	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == "submit_plan" || !isReadClassTool(req) {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}
	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if len(unread) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}
	if redirected, raw, ok := r.deterministicPlanExecuteRequiredSourceRead(results); ok &&
		runnerAllowsTool(allowedTools, redirected.Tool) &&
		normalizedToolRequestRaw(redirected) != normalizedToolRequestRaw(req) {
		target := normalizeRunnerPath(planThenExecuteRequestTargetPath(redirected))
		if _, missing := unread[target]; missing {
			evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
			if planThenExecuteRequiredSourceRequestAdvances(redirected, target, evidence, results) {
				return planThenExecuteRequiredSourceSurfaceAction{
					Request:  redirected,
					Raw:      raw,
					Redirect: true,
				}, true
			}
		}
	}
	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, r.RuntimeBrief, results)
	if !reject {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}
	return planThenExecuteRequiredSourceSurfaceAction{
		Summary: summary,
		Error:   errorText,
	}, true
}

func (r *Runner) deterministicPlanExecuteRejectedRequiredSourceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}
	if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) == "" {
		return workspace.ToolRequest{}, "", false
	}
	return r.deterministicPlanExecuteRequiredSourceRead(results)
}

func (r *Runner) deterministicImplementationRejectedRequiredPathRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}
	remaining := r.remainingRequiredEditPaths(results)
	if !scopedImplementationUsesBoundedSourceEdits(r.RuntimeBrief, remaining) {
		return workspace.ToolRequest{}, "", false
	}
	if implementationRequiredPathAnchorsReady(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationRejectedTextCanAdvanceRequiredRead(trimmed) {
		return workspace.ToolRequest{}, "", false
	}
	return r.deterministicImplementationRequiredPathRead(results)
}

func implementationRejectedTextCanAdvanceRequiredRead(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	return !containsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	)
}

func (r *Runner) deterministicImplementationRequiredPathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	if len(r.remainingRequiredEditPaths(results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	changed := changedPathsFromToolResults(results)
	for _, path := range r.requiredEditCoveragePaths() {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := changed[normalized]; ok {
			continue
		}
		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}
		if evidence.Level != planThenExecuteEvidenceNone || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicImplementationRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}
			if alreadyReadPath(results, normalized) {
				continue
			}
		}
		req := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            normalized,
			StartLine:       1,
			EndLine:         220,
			NamedGap:        "exact current source needed before editing this required path",
			WhyNotAnswering: "need current source evidence before the write",
		}
		return req, marshalToolRequestRaw(req), true
	}
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicImplementationEvidencePathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !r.unscopedNoWriteCompletionRecoveryApplies(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}
	seen := map[string]struct{}{}
	var paths []string
	for _, path := range r.RuntimeBrief.EvidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !recoverySearchPathLooksEditableSource(normalized) || alreadyReadPath(results, normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            paths[0],
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "exact current source needed before editing the implementation evidence path",
		WhyNotAnswering: "need current source before the write",
	}
	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicPlanExecuteRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required plan-execute edit path",
		SymbolGap:        "source-body evidence for required plan-execute edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before submit_plan can anchor the required edit",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before submit_plan can anchor the required edit",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before submit_plan can anchor the required edit",
	})
}

func (r *Runner) planThenExecuteRequiredSourceFollowupRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, false
	}
	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == "submit_plan" || !isReadClassTool(req) {
		return workspace.ToolRequest{}, false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, false
	}
	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return workspace.ToolRequest{}, false
	}
	if planThenExecuteInvalidAnchorRefreshAllowed(req, target, results) {
		return workspace.ToolRequest{}, false
	}
	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if _, missing := unread[target]; !missing {
		if len(unread) == 0 {
			return workspace.ToolRequest{}, false
		}
		redirected, _, ok := r.deterministicPlanExecuteRequiredSourceRead(results)
		if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
			return workspace.ToolRequest{}, false
		}
		return redirected, true
	}
	evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
	if evidence.Level == planThenExecuteEvidenceNone && !planThenExecutePathHasAnyToolEvidence(target, results) {
		return workspace.ToolRequest{}, false
	}
	if planThenExecuteRequiredSourceRequestAdvances(req, target, evidence, results) {
		return workspace.ToolRequest{}, false
	}
	redirected, ok := r.deterministicPlanExecuteRequiredSourceFollowup(target, results)
	if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, false
	}
	return redirected, true
}

func planThenExecuteInvalidAnchorRefreshAllowed(req workspace.ToolRequest, target string, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}
	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	if _, invalidAnchor := invalidAnchorPaths[normalizeRunnerPath(target)]; !invalidAnchor {
		return false
	}
	return submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
		planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
		!successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, target)
}

const planThenExecuteReadySourceRefreshLimit = 4

func (r *Runner) planThenExecuteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}
	if len(r.RuntimeBrief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 || len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {
		return false
	}
	if planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(r.RuntimeBrief, results) {
		return false
	}
	if !planThenExecuteRejectedPlanSourceRefreshAllowed(req, r.requiredEditCoveragePaths(), results) {
		return false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}
	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return false
	}
	if !planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) {
		return false
	}
	return true
}

func (r *Runner) planThenExecuteCandidateSourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}
	if len(r.RuntimeBrief.RequiredEditPaths) > 0 || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	return planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req, r.RuntimeBrief, results)
}

func (r *Runner) planThenExecuteRejectedPlanRefreshReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolRequest{}, "", false
	}
	if len(r.RuntimeBrief.RequiredEditPaths) > 0 || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}
	if !planThenExecuteCandidateRejectedPlanSourceRefreshTarget(r.RuntimeBrief, results, target) {
		return workspace.ToolRequest{}, "", false
	}
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}
	line, ok := submitPlanSyntaxValidationLineForPath(rejected, target)
	if !ok || (req.StartLine <= line && line <= req.EndLine) {
		return workspace.ToolRequest{}, "", false
	}
	start := line - 20
	if start < 1 {
		start = 1
	}
	end := line + 60
	redirected := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            target,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        "refresh current source around rejected submit_plan syntax failure",
		WhyNotAnswering: "need current source around rejected plan line before corrected submit_plan",
	}
	return redirected, marshalToolRequestRaw(redirected), true
}

func planThenExecuteSubmitReadyParseFailureSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return planThenExecuteSubmitReadyParseFailureIndex(brief, results) >= 0
}

func planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	parseFailureAt := planThenExecuteSubmitReadyParseFailureIndex(brief, results)
	if parseFailureAt < 0 {
		return false
	}
	_, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	return !ok || rejectedAt < parseFailureAt
}

func planThenExecuteSubmitReadyParseFailureIndex(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	last := -1
	for i, result := range results {
		if strings.TrimSpace(result.Tool) != "tool_parse" || strings.TrimSpace(result.Error) == "" {
			continue
		}
		if planThenExecuteSubmitPlanOnlyReady(brief, results[:i]) {
			last = i
		}
	}
	return last
}

func (r *Runner) planThenExecuteSubmitPlanOnlySurfaceRejection(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (string, string, string, bool) {
	if r == nil || !planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, results) {
		return "", "", "", false
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || tool == "submit_plan" || runnerAllowsTool(allowedTools, tool) {
		return "", "", "", false
	}
	if len(allowedTools) != 1 || !runnerAllowsTool(allowedTools, "submit_plan") {
		return "", "", "", false
	}
	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		prompt := strings.Join([]string{
			"Plan-execute submit-correction mode is now the only allowed planner action. Every required_edit_path already has source-body evidence, and the previous `submit_plan` was rejected before it became a valid plan artifact.",
			"The next visible action must be exactly one parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
			"Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not describe the correction in prose.",
		}, "\n")
		return "Corrected submit_plan required", fmt.Sprintf("%s is not available because plan-execute submit-correction mode exposes only `submit_plan`. Emit one parser-readable `submit_plan` JSON object now. %s No terminal source-satisfied/question/prose fallback is valid in this state.", tool, planThenExecuteSubmitPlanCoverageSentence()), prompt, true
	}
	prompt := strings.Join([]string{
		"Plan-execute submit_plan is now the only allowed planner action. Every required_edit_path already has anchor-ready source evidence, so read/search/source-refresh tools are closed for this planner turn.",
		"The next visible action must be a parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. The rejected request will not become available again in this planner phase.",
		"Only use `Decision: source-satisfied` if every required path already contains the requested final state and you cite per-path current-state evidence. Only use `<<QUESTION>>:` if one exact path-specific blocker prevents drafting any `submit_plan` edit.",
	}, "\n")
	return "Plan-execute submit_plan required", fmt.Sprintf("%s is not available because the plan-execute planner is source-ready and exposes only `submit_plan`. Emit a parser-readable `submit_plan` JSON object now. %s Use `Decision: source-satisfied` or `<<QUESTION>>:` only for a true terminal exception; do not request another source read.", tool, planThenExecuteSubmitPlanCoverageSentence()), prompt, true
}

func planThenExecuteRequiredSourceRequestAdvances(req workspace.ToolRequest, path string, evidence planThenExecuteSourceEvidence, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		start, end := req.StartLine, req.EndLine
		if start <= 0 && end <= 0 {
			return evidence.Level == planThenExecuteEvidenceNone
		}
		if start <= 0 {
			start = 1
		}
		if end <= 0 || end < start {
			return false
		}
		if planThenExecuteReadRangeSeen(path, start, end, results) {
			return false
		}
		if planThenExecuteLargeGoRawRangeNeedsSymbolBody(path, planThenExecuteRequiredSourceReadTotal(path, results)) &&
			!planThenExecuteFileOutlineSeen(path, results) {
			return false
		}
		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines && planThenExecutePathHasOutlineEvidence(path, results) {
			return false
		}
		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines &&
			planThenExecuteEvidenceRank(evidence.Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {
			return false
		}
		return true
	case "code_navigate", "read_only_capability":
		capability := strings.TrimSpace(req.Capability)
		switch {
		case capability == "file_outline":
			return !planThenExecuteFileOutlineSeen(path, results)
		case capability == "read_symbol" || capability == "extract_body" ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body"):
			symbol := strings.TrimSpace(req.Pattern)
			if symbol == "" {
				symbol = strings.TrimSpace(req.Symbol)
			}
			return symbol == "" || !planThenExecuteReadSymbolSeen(path, symbol, results)
		default:
			return false
		}
	default:
		tool := strings.TrimSpace(req.Tool)
		if strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body") {
			return true
		}
		return false
	}
}

func planThenExecuteRequiredSourceReadTotal(path string, results []workspace.ToolResult) int {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0
	}
	total := 0
	lowerPath := strings.ToLower(path)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}
		if lineCount, ok := planThenExecuteReadSummaryTotalLines(result.Summary); ok && lineCount > total {
			total = lineCount
		}
	}
	return total
}

func planThenExecuteReadRangeSeen(path string, start, end int, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" || start <= 0 || end < start {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}
		priorStart, priorEnd, ok := planThenExecuteReadSummaryLineRange(result.Summary)
		if ok && priorStart == start && priorEnd == end {
			return true
		}
	}
	return false
}

func (r *Runner) deterministicImplementationRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required implementation edit path",
		SymbolGap:        "source-body evidence for required implementation edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before editing this required path",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before editing this required path",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before editing this required path",
	})
}

type requiredSourceFollowupWording struct {
	OutlineGap       string
	SymbolGap        string
	OutlineWhy       string
	SymbolWhy        string
	RangeWhy         string
	FallbackRangeWhy string
}

func (r *Runner) deterministicRequiredSourceFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return workspace.ToolRequest{}, false
	}
	if strings.TrimSpace(wording.OutlineGap) == "" {
		wording.OutlineGap = "source-body line anchors for required edit path"
	}
	if strings.TrimSpace(wording.SymbolGap) == "" {
		wording.SymbolGap = "source-body evidence for required edit path"
	}
	if !planThenExecutePathSupportsCodeNavigate(path) {
		return deterministicRequiredSourceRangeFollowup(path, results, wording)
	}
	if !planThenExecuteFileOutlineSeen(path, results) {
		return workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            path,
			Capability:      "file_outline",
			NamedGap:        wording.OutlineGap,
			WhyNotAnswering: wording.OutlineWhy,
		}, true
	}
	if symbol := planThenExecuteBestOutlineSymbol(path, r.RuntimeBrief.Objective, results); symbol != "" && !planThenExecuteReadSymbolSeen(path, symbol, results) {
		return workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            path,
			Capability:      "read_symbol",
			Pattern:         symbol,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.SymbolWhy,
		}, true
	}
	if start, end, ok := planThenExecuteBestOutlineRange(path, r.RuntimeBrief.Objective, results); ok {
		return workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       start,
			EndLine:         end,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.RangeWhy,
		}, true
	}
	return deterministicRequiredSourceRangeFollowup(path, results, wording)
}

func deterministicRequiredSourceRangeFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	start, end, ok := planThenExecuteNextRequiredSourceReadRange(path, results)
	if !ok {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:            "read_file",
		Path:            path,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        wording.SymbolGap,
		WhyNotAnswering: wording.FallbackRangeWhy,
	}, true
}

func planThenExecuteNextRequiredSourceReadRange(path string, results []workspace.ToolResult) (int, int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}
	maxEnd, total := 0, 0
	sawReadFileEvidence := false
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}
		if !strings.Contains(text, "redirected") || !planThenExecutePathSupportsCodeNavigate(path) {
			sawReadFileEvidence = true
		}
		if priorStart, priorEnd, ok := planThenExecuteReadSummaryLineRange(result.Summary); ok && priorStart > 0 && priorEnd >= priorStart && priorEnd > maxEnd {
			maxEnd = priorEnd
		}
		if lineCount, ok := planThenExecuteReadSummaryTotalLines(result.Summary); ok && lineCount > total {
			total = lineCount
		}
	}
	if sawReadFileEvidence && maxEnd == 0 {
		return 0, 0, false
	}
	start := 1
	if maxEnd > 0 {
		start = maxEnd + 1
	}
	if total > 0 && start > total {
		return 0, 0, false
	}
	end := start + 179
	if total > 0 && end > total {
		end = total
	}
	if start <= 0 || end < start {
		return 0, 0, false
	}
	return start, end, true
}

func planThenExecutePathSupportsCodeNavigate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".kts", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hxx":
		return true
	default:
		return false
	}
}

func planThenExecuteFileOutlineSeen(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	lowerPath := strings.ToLower(path)
	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Output)
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(text, lowerPath) {
			continue
		}
		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") {
			return true
		}
	}
	return false
}

func planThenExecuteReadSymbolSeen(path, symbol string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	symbol = strings.ToLower(strings.TrimSpace(symbol))
	if path == "" || symbol == "" {
		return false
	}
	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Error + "\n" + result.Output)
		if strings.TrimSpace(result.Tool) == "code_navigate" &&
			strings.Contains(text, "read_symbol") &&
			strings.Contains(text, strings.ToLower(path)) &&
			strings.Contains(text, symbol) {
			return true
		}
	}
	return false
}

func planThenExecuteBestOutlineSymbol(path, objective string, results []workspace.ToolResult) string {
	path = normalizeRunnerPath(path)
	if path == "" {
		return ""
	}
	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	best, bestScore := "", 0
	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}
		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			name := strings.TrimSpace(match[1])
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)
			if score > bestScore {
				best, bestScore = name, score
			}
		}
		for _, name := range planThenExecutePreviewDeclarationNames(text) {
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)
			if score > bestScore {
				best, bestScore = name, score
			}
		}
	}
	return best
}

func planThenExecutePreviewDeclarationNames(text string) []string {
	var names []string
	seen := map[string]struct{}{}
	for _, match := range fileOutlinePreviewPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		for _, raw := range strings.Split(match[1], ",") {
			name := strings.TrimSpace(raw)
			if name == "" || strings.HasPrefix(name, "...") || strings.Contains(name, " more") {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

func planThenExecuteBestOutlineRange(path, objective string, results []workspace.ToolResult) (int, int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}
	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	bestStart, bestEnd, bestScore := 0, 0, 0
	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}
		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 4 {
				continue
			}
			start, startErr := strconv.Atoi(match[2])
			end, endErr := strconv.Atoi(match[3])
			if startErr != nil || endErr != nil || start <= 0 || end < start {
				continue
			}
			score := planThenExecuteSymbolScore(path, strings.TrimSpace(match[1]), objectiveText, objectiveTokens)
			if score > bestScore {
				bestStart, bestEnd, bestScore = start, end, score
			}
		}
	}
	if bestScore <= 0 {
		return 0, 0, false
	}
	return bestStart, bestEnd, true
}

func planThenExecuteSymbolScore(path, name, objective string, objectiveTokens map[string]int) int {
	score := 0
	for token := range planThenExecuteSymbolScoreTokens(name) {
		score += objectiveTokens[token]
	}
	lowerName := strings.ToLower(name)
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	if strings.HasSuffix(lowerPath, "_test.go") && strings.HasPrefix(lowerName, "test") {
		score += 4
	}
	if strings.HasPrefix(lowerPath, "cmd/") && strings.HasPrefix(lowerName, "run") {
		score += 4
	}
	if strings.Contains(lowerName, "validator") {
		score -= 3
	}
	return score
}

func planThenExecuteSymbolScoreTokens(text string) map[string]int {
	out := make(map[string]int)
	runes := []rune(text)
	var current strings.Builder
	flush := func() {
		token := strings.ToLower(strings.TrimSpace(current.String()))
		if len(token) >= 3 {
			out[token]++
		}
		current.Reset()
	}
	for i, r := range runes {
		if r == '_' || r == '-' || r == '/' || r == '.' || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && current.Len() > 0 {
			prev := runes[i-1]
			var next rune
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (next != 0 && unicode.IsLower(next)) {
				flush()
			}
		}
		current.WriteRune(unicode.ToLower(r))
	}
	flush()
	return out
}

func planThenExecutePathHasAnyToolEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}
	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if strings.Contains(text, strings.ToLower(normalized)) {
			return true
		}
	}
	return false
}

func (r *Runner) deterministicNoWriteRecoveryUnreadCandidateRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) {
		return workspace.ToolRequest{}, "", false
	}
	if r.planThenExecutePlannerPhase() {
		return workspace.ToolRequest{}, "", false
	}
	if successfulSubmitPlanResultCount(results) > 0 || len(readPathsFromResults(results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	if last := lastNonGateTool(results); last != "read_file" && last != "search_content" && last != "code_navigate" {
		return workspace.ToolRequest{}, "", false
	}
	paths := unreadCandidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
	if completionGateResultSeen(results) {
		paths = unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 4)
	}
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	return noWriteRecoveryReadRequest(
		paths[0],
		"exact current code needed before writing the requested implementation change",
		"need current source before the write",
	)
}

func (r *Runner) deterministicPlanExecuteNonToolCandidateRecovery(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	// Open-candidate localization is strategic work, not a hard invariant.
	// Required edit paths and rejected stale anchors may still use deterministic
	// recovery, but open-ended plan-execute prose must be repaired by a
	// model-facing contract nudge/rejection instead of a host-chosen probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicPlanExecuteChainedSearchRecovery(req workspace.ToolRequest, results []workspace.ToolResult, executionState *runnerExecutionState) (workspace.ToolRequest, string, bool) {
	// Chained search is strategy, not a hard invariant. Exact-repeat and
	// no-progress guards still prevent loops; otherwise the model owns whether
	// a follow-up search or a source read is the better next probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoveryTargetRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}
	for _, ref := range r.RuntimeBrief.TargetRefs {
		path := strings.TrimSpace(filepath.ToSlash(ref))
		if path == "" || !recoverySearchPathLooksEditableSource(path) || alreadyReadPath(results, path) {
			continue
		}
		return noWriteRecoveryReadRequest(
			path,
			"exact current code needed before submitting the requested plan",
			"need current source before submit_plan can anchor the edit",
		)
	}
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoverySearch(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || (lastNonGateTool(results) != "context_pack" && !lastNonGateAttemptWasUnavailableContextPack(results)) {
		return workspace.ToolRequest{}, "", false
	}
	if len(concreteRecoveryEvidencePaths(results, 1)) > 0 || successfulToolSeen(results, "search_content") {
		return workspace.ToolRequest{}, "", false
	}
	pattern := literalRecoverySearchPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, "", false
	}
	req := workspace.ToolRequest{
		Tool:            "search_content",
		Path:            ".",
		Pattern:         pattern,
		NamedGap:        "locate exact current source for the requested behavior",
		WhyNotAnswering: "need a grounded source hit before reading or writing",
	}
	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) unscopedImplementationWriteReadyAfterNoWriteGate(results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) || r.planThenExecutePlannerPhase() {
		return false
	}
	return implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results)
}

func successfulToolSeen(results []workspace.ToolResult, tool string) bool {
	tool = strings.TrimSpace(tool)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == tool && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}

func completionGateResultSeen(results []workspace.ToolResult) bool {
	return completionGateResultCount(results) > 0
}

func completionGateResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			count++
		}
	}
	return count
}

func (r *Runner) planThenExecuteSubmitReadyTerminalPrompt(errorText string, results []workspace.ToolResult, native bool) string {
	if r != nil && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		parts := []string{
			strings.TrimSpace(errorText),
			"The previous read/search request was rejected because plan-execute submit-correction mode is already ready. Do not read, search, inspect, verify, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or describe the plan in prose. The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence(),
			planThenExecuteTerminalSubmitPlanSkeleton(r.RuntimeBrief, results, native),
			"Fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders and do not request another tool.",
		}
		return strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n")
	}
	parts := []string{
		strings.TrimSpace(errorText),
		"The previous read/search request was rejected because plan-execute planner source evidence is already ready. Do not read, search, inspect, or verify again. The next visible action must be exactly one terminal outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing the current source path(s), or `<<QUESTION>>:` naming the exact blocker. Do not report this recovery rejection itself as the blocker.",
	}
	if r != nil {
		parts = append(parts, planThenExecuteTerminalSubmitPlanSkeleton(r.RuntimeBrief, results, native))
	}
	parts = append(parts, "If submitting a plan, fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.")
	return strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n")
}

func nonEmptyRunnerPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func lastNonGateTool(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == "completion_gate" {
			continue
		}
		if strings.TrimSpace(results[i].Error) != "" {
			continue
		}
		return tool
	}
	return ""
}

func lastNonGateAttemptWasUnavailableContextPack(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == "completion_gate" {
			continue
		}
		if tool != "context_pack" {
			return false
		}
		return strings.TrimSpace(results[i].Error) != "" || !contextPackResultHasTargets(results[i])
	}
	return false
}

func alreadyReadPath(results []workspace.ToolResult, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
			return true
		}
	}
	return false
}

func (r *Runner) repeatedRangeLessReadFileRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return "", "", false
	}
	if rangeLessReadFileWasRedirectedToOutline(path, results) && !concreteSourceEvidenceReadForPath(path, results) {
		followup := r.repeatedRangeLessReadFileFollowup(path, results)
		return "Repeated range-less read_file redirected to bounded source request",
			fmt.Sprintf("A prior range-less `read_file` for `%s` was already redirected to outline evidence. Do not retry the same range-less read or dump the full file. Request bounded source-body evidence instead:\n\n```tool\n%s\n```", path, marshalToolRequestRaw(followup)),
			true
	}
	return "", "", false
}

func (r *Runner) mutationRejectedRetryWriteRecovery(decision runnerRequestBudgetDecision, req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, string, bool) {
	if r == nil || decision.ForceAnswerReason != "rejection_retry_blocked" {
		return workspace.ToolResult{}, "", false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, "", false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) || len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolResult{}, "", false
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Mutation write required after repeated rejected request",
		Error:   fmt.Sprintf("%s was already rejected and then repeated before any successful workspace write. The implementation contract is not satisfied by repeating rejected non-write requests.", tool),
	}
	prompt := result.Error + " Use the gathered source evidence to make the smallest parser-readable write now. If no write is valid, answer exactly `<<QUESTION>>:` with the concrete path-specific blocker or no-op evidence. Do not repeat the rejected non-write request."
	return result, prompt, true
}

func (r *Runner) mutationNoWriteLoopRecovery(req workspace.ToolRequest, results []workspace.ToolResult, identicalCallRun int) (workspace.ToolResult, string, bool) {
	if r == nil || identicalCallRun < 3 {
		return workspace.ToolResult{}, "", false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, "", false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) || len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolResult{}, "", false
	}
	if !mutationNoWriteLoopEvidenceReady(r.RuntimeBrief, results) {
		return workspace.ToolResult{}, "", false
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Mutation write required after repeated non-write request",
		Error:   fmt.Sprintf("%s was repeated %d times before any successful workspace write. The explicit implementation contract is not satisfied by repeating non-write requests.", tool, identicalCallRun),
	}
	prompt := result.Error + " Use the gathered source evidence to make the smallest parser-readable write now. If no write is valid, answer exactly `<<QUESTION>>:` with the concrete path-specific blocker or no-op evidence. Do not repeat the non-write request."
	return result, prompt, true
}

func mutationNoWriteLoopEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(brief.RequiredEditPaths) > 0 {
		return implementationRequiredPathAnchorsReady(brief, results)
	}
	for _, result := range results {
		if concreteSourceEvidenceResult(result) {
			return true
		}
	}
	return false
}

type planThenExecuteLoopBudgetRecovery struct {
	Prompt       string
	TerminalOnly bool
}

func (r *Runner) planThenExecuteLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}
	var unreadCandidatePaths []string
	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) == 0 {
		unreadCandidatePaths = unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 8)
	}
	noBodyMissRecovery := planThenExecuteNoBodyMissRecoveryApplies(results)
	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) &&
		!planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) &&
		(len(unreadCandidatePaths) == 0 || !noBodyMissRecovery) {
		return planThenExecuteLoopBudgetRecovery{}, false
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated without advancing the planner contract", tool)
	}
	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Submit-correction mode is already ready; do not call more read, search, navigation, or verification tools.", tool, reason)
		parts := []string{
			lead,
			"The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not report the loop-budget recovery itself as the blocker.",
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
			"Fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
		}
		return planThenExecuteLoopBudgetRecovery{
			Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
			TerminalOnly: true,
		}, true
	}
	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) == 0 && noBodyMissRecovery {
		if unread := unreadCandidatePaths; len(unread) > 0 {
			read := sortedRunnerPathKeys(readPathsFromResults(results))
			lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that request.", tool, reason)
			parts := []string{
				lead,
				"Unscoped plan-execute candidate paths are not coverage obligations, but this planner still has unread candidate surfaces that may close a concrete edit-safety fact.",
				"Read candidate path(s) so far: " + util.FirstNonEmpty(strings.Join(read, ", "), "(none)"),
				"Unread candidate path(s): " + strings.Join(unread, ", "),
				"The next visible action must be exactly one parser-readable action: one bounded source-body read/search for a model-selected unread candidate path with a named edit-safety fact, a parser-readable `submit_plan` using the source evidence already gathered, `Decision: source-satisfied` with current-state evidence, or `<<QUESTION>>:` naming the exact blocker. If the prior symbol lookup missed before source-body evidence existed, a bounded read on that same candidate path is valid.",
				"Do not call the same tool with the same arguments again, do not run verification, and do not report the loop-budget recovery itself as the blocker.",
			}
			return planThenExecuteLoopBudgetRecovery{
				Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
				TerminalOnly: false,
			}, true
		}
	}
	lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. The planner already has source evidence; do not call more read, search, navigation, or verification tools.", tool, reason)
	parts := []string{
		lead,
		"The next visible action must be exactly one terminal outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing the current source path(s), or `<<QUESTION>>:` naming the exact blocker. Do not report the loop-budget recovery itself as the blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
		"If submitting a plan, fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
	}
	return planThenExecuteLoopBudgetRecovery{
		Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		TerminalOnly: true,
	}, true
}

func planThenExecuteNoBodyMissRecoveryApplies(results []workspace.ToolResult) bool {
	return len(readPathsFromResults(results)) == 0 && planThenExecuteHasPriorReadClassFailure(results)
}

func (r *Runner) planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) &&
		!planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) {
		return planThenExecuteLoopBudgetRecovery{}, false
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated after a loop-budget recovery", tool)
	}
	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. Submit-correction mode is already ready; repeating the same request is not progress.", tool, reason)
		parts := []string{
			lead,
			"Read/search/navigation tools are disabled for this recovery turn. The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied` and do not answer `<<QUESTION>>:`.",
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
			"Fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
		}
		return planThenExecuteLoopBudgetRecovery{
			Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
			TerminalOnly: true,
		}, true
	}
	lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. The previous recovery already allowed either a different bounded read/search with a named edit-safety fact or a terminal planner outcome; repeating the same request is not progress.", tool, reason)
	parts := []string{
		lead,
		"Read/search/navigation tools are disabled for this recovery turn. The next visible action must be exactly one terminal planner outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing current-state evidence, or `<<QUESTION>>:` naming the exact blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
		"If submitting a plan, fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
	}
	return planThenExecuteLoopBudgetRecovery{
		Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		TerminalOnly: true,
	}, true
}

func (r *Runner) repeatedRangeLessReadFileRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !rangeLessReadFileWasRedirectedToOutline(path, results) || concreteSourceEvidenceReadForPath(path, results) {
		return workspace.ToolRequest{}, "", false
	}
	followup := r.repeatedRangeLessReadFileFollowup(path, results)
	if strings.TrimSpace(followup.Tool) == "" || normalizedToolRequestRaw(followup) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", false
	}
	if len(allowedTools) > 0 && !runnerAllowsTool(allowedTools, followup.Tool) {
		return workspace.ToolRequest{}, "", false
	}
	return followup, marshalToolRequestRaw(followup), true
}

func (r *Runner) planThenExecuteCandidateRangeLessReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) > 0 ||
		successfulSubmitPlanResultCount(results) > 0 ||
		len(planThenExecuteAnchorReadyPaths(r.RuntimeBrief, results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	target := normalizeRunnerPath(req.Path)
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}
	if _, alreadyRead := readPathsFromResults(results)[target]; alreadyRead {
		return workspace.ToolRequest{}, "", false
	}
	candidateSet := make(map[string]struct{})
	for _, path := range candidateRecoveryScopePaths(r.RuntimeBrief, results, 8) {
		candidateSet[normalizeRunnerPath(path)] = struct{}{}
	}
	if _, ok := candidateSet[target]; !ok {
		return workspace.ToolRequest{}, "", false
	}
	redirected := req
	redirected.Path = target
	redirected.StartLine = 1
	redirected.EndLine = 180
	if strings.TrimSpace(redirected.NamedGap) == "" {
		redirected.NamedGap = "bounded source window for unread candidate path"
	}
	if strings.TrimSpace(redirected.WhyNotAnswering) == "" {
		redirected.WhyNotAnswering = "need bounded source evidence before deciding whether this candidate path needs an edit"
	}
	return redirected, marshalToolRequestRaw(redirected), true
}

func (r *Runner) planThenExecuteMalformedSymbolReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if !runnerAllowsTool(allowedTools, "read_file") {
		return workspace.ToolRequest{}, "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}
	if _, ok := workspace.LanguageForFile(path); !ok {
		return workspace.ToolRequest{}, "", false
	}
	tool := strings.TrimSpace(req.Tool)
	capability := strings.TrimSpace(req.Capability)
	if tool != "code_navigate" && tool != "read_only_capability" &&
		!strings.HasSuffix(tool, "_read_symbol") && !strings.HasSuffix(tool, "_extract_body") {
		return workspace.ToolRequest{}, "", false
	}
	if capability != "" && capability != "read_symbol" && capability != "extract_body" &&
		!strings.HasSuffix(capability, "_read_symbol") && !strings.HasSuffix(capability, "_extract_body") {
		return workspace.ToolRequest{}, "", false
	}
	if strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Symbol) != "" || strings.TrimSpace(req.Query) != "" {
		return workspace.ToolRequest{}, "", false
	}
	redirected := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "bounded source window after malformed symbol read omitted a pattern",
		WhyNotAnswering: "need source-body evidence for the model-selected candidate path before submit_plan",
	}
	return redirected, marshalToolRequestRaw(redirected), true
}

func (r *Runner) planThenExecuteRepeatedSymbolReadFallbackRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 || len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if !runnerAllowsTool(allowedTools, "read_file") {
		return workspace.ToolRequest{}, "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || concreteSourceEvidenceReadForPath(path, results) {
		return workspace.ToolRequest{}, "", false
	}
	if _, ok := workspace.LanguageForFile(path); !ok {
		return workspace.ToolRequest{}, "", false
	}
	tool := strings.TrimSpace(req.Tool)
	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		capability = strings.TrimSpace(req.Operation)
	}
	if tool != "code_navigate" && tool != "read_only_capability" &&
		!strings.HasSuffix(tool, "_read_symbol") && !strings.HasSuffix(tool, "_extract_body") {
		return workspace.ToolRequest{}, "", false
	}
	if capability != "" && capability != "read_symbol" && capability != "extract_body" &&
		!strings.HasSuffix(capability, "_read_symbol") && !strings.HasSuffix(capability, "_extract_body") {
		return workspace.ToolRequest{}, "", false
	}
	if !planThenExecuteHasPriorReadClassFailure(results) {
		return workspace.ToolRequest{}, "", false
	}
	redirected := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "bounded source window after semantic symbol lookup failed",
		WhyNotAnswering: "need source-body evidence for the model-selected candidate path before submit_plan",
	}
	return redirected, marshalToolRequestRaw(redirected), true
}

func planThenExecuteHasPriorReadClassFailure(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "" {
			continue
		}
		if workerruntime.IsZeroHitSearchResult(result) {
			return true
		}
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		lower := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if strings.Contains(lower, "zero-hit retry blocked") ||
			strings.Contains(lower, "no symbols matched") ||
			strings.Contains(lower, "no go symbols matched") ||
			strings.Contains(lower, "repeating it will not find new matches") {
			return true
		}
	}
	return false
}

func (r *Runner) planThenExecuteMissingTargetReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	// A missing target on an open-candidate read is a model contract failure, not
	// permission for the host to choose the path. Let the normal rejection/nudge
	// path ask for a concrete target instead.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) repeatedRangeLessReadFileFollowup(path string, results []workspace.ToolResult) workspace.ToolRequest {
	if r != nil {
		if req, ok := r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
			OutlineGap:       "source-body evidence for repeated range-less read_file",
			SymbolGap:        "source-body evidence for repeated range-less read_file",
			OutlineWhy:       "range-less read was redirected; need declarations before selecting source-body evidence",
			SymbolWhy:        "need current declaration body instead of another full-file read",
			RangeWhy:         "outline did not produce a readable symbol; need bounded source instead of another full-file read",
			FallbackRangeWhy: "need bounded source instead of another full-file read",
		}); ok {
			return req
		}
	}
	return workspace.ToolRequest{
		Tool:            "read_file",
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "exact source fact still missing after prior full-file read",
		WhyNotAnswering: "need bounded source instead of repeating range-less read_file",
	}
}

func rangeLessReadFileWasRedirectedToOutline(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	lowerPath := strings.ToLower(path)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}
		if strings.TrimSpace(result.Tool) == "read_file" &&
			strings.Contains(text, "redirected") &&
			(strings.Contains(text, "file_outline") || strings.Contains(text, "outline")) {
			return true
		}
		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") {
			return true
		}
	}
	return false
}

func concreteSourceEvidenceReadForPath(path string, results []workspace.ToolResult) bool {
	return sourceEvidenceBodyReadForPath(path, results)
}

func concreteSourceEvidenceResultMatchesPath(result workspace.ToolResult, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || isNonSourceEvidenceArtifactPath(path) {
		return false
	}
	if strings.TrimSpace(result.Tool) == "read_file" {
		readPath := normalizeRunnerPath(extractReadPathFromSummary(result.Summary))
		return readPath == path && !isNonSourceEvidenceArtifactPath(readPath)
	}
	for _, readPath := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(readPath) {
			continue
		}
		if normalizeRunnerPath(readPath) == path {
			return true
		}
	}
	return false
}

func fullSourceReadFileSeen(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if !readFileResultHasConcreteSourceBody(result) {
			continue
		}
		readPath := normalizeRunnerPath(extractReadPathFromSummary(result.Summary))
		if readPath != path {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		start, end, hasRange := planThenExecuteReadSummaryLineRange(result.Summary)
		total, hasTotal := planThenExecuteReadSummaryTotalLines(result.Summary)
		if hasRange && hasTotal && start == 1 && end >= total {
			return true
		}
		if hasRange || strings.Contains(text, "output was capped") || strings.Contains(text, "full range-less read omitted") || strings.Contains(text, "bounded large required-path source") {
			continue
		}
		return true
	}
	return false
}

func completionGateRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return completionGateRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func implementationRequiredPathHiddenOnlyRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
		return false
	}
	if !implementationRequiredPathAnchorsReady(brief, results) {
		return false
	}
	return strings.TrimSpace(reply.Content) == "" && strings.TrimSpace(reply.ReasoningContent) != ""
}

func mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}
	if !runnerBriefRequiresMutation(brief) {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) == "" {
		return false
	}
	return concreteSourceEvidenceAlreadyRead(results)
}

func (r *Runner) unscopedNoWriteTerminalRecoveryReady(results []workspace.ToolResult) bool {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 || len(r.remainingRequiredEditPaths(results)) > 0 {
		return false
	}
	return concreteSourceEvidenceAlreadyRead(results) && mutationNoWriteCompletionGateRejectionCount(results) >= 2
}

func mutationNoWriteCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}
		if !strings.Contains(strings.ToLower(result.Summary), "mutation completion gate rejected final answer") {
			continue
		}
		if strings.Contains(strings.ToLower(result.Error), "no successful write tool result is present") {
			count++
		}
	}
	return count
}

func (r *Runner) unscopedNoWriteTerminalRecoveryPrompt(results []workspace.ToolResult) string {
	lines := []string{
		"The completion gate has now rejected multiple no-write final answers after source evidence was gathered.",
		"Read/search/discovery tools are hidden for this turn. Do not inspect, search, navigate, plan, or summarize again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}
	lines = append(lines, "Choose the path and edit shape from gathered evidence and the active runtime contract. Actions written only in hidden reasoning are ignored by the parser.")
	return strings.Join(lines, "\n\n")
}

func hiddenOnlyNoWriteTerminalRecoveryPrompt() string {
	return strings.Join([]string{
		"The previous retry produced hidden-only output after concrete source evidence was already read. Hidden thinking is disabled for this retry.",
		"Read/search/discovery tools are hidden for this turn. Do not inspect, search, plan, or summarize again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using the current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
		textualMutationWriteJSONInstruction(),
		"Choose the path and edit shape from the gathered evidence and active runtime contract. Do not copy placeholders, schema examples, or prose as edit fields.",
	}, "\n\n")
}

func reviewRepairCompletionGateRecoveryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response because it did not visibly close the review contract.",
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		lines = append(lines,
			"Phase Evidence still has failed obligations: "+strings.Join(failed, ", ")+". `Decision: pass` is invalid in this state.",
			"Your next visible response must be exactly one terminal review-repair outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
			"Corrective plans must be one parser-readable `submit_plan` object with a real `Verification Checklist:` rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact failed obligation.",
		)
	} else {
		lines = append(lines,
			"Your next visible response must be exactly one terminal review-repair outcome: `Decision: pass` with a `Verification Checklist`, a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact blocker.",
			"Pass responses start with `Decision: pass` followed by `Verification Checklist:` bullets. Corrective plans must be one parser-readable `submit_plan` object with a real checklist rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact blocker.",
		)
	}
	lines = append(lines,
		"Do not add prose before or after the chosen terminal shape.",
		"Use the existing Phase Evidence Bundle, Diff Evidence, submitted_plan_state, source_sanity, verification entries, and inherited context packet as the evidence ledger. The closeout gate has fired, so do not read, search, refresh context, run commands, or ask for more inspection before choosing the terminal outcome.",
		"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract.",
	)
	return strings.Join(lines, "\n\n")
}

func reviewRepairSourceSanityRepairNeedsEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}
	failed := planThenExecuteReviewRepairFailedPhaseObligations(brief)
	hasSourceSanityFailure := false
	for _, obligation := range failed {
		obligation = strings.TrimSpace(obligation)
		if strings.HasPrefix(obligation, "source_sanity_failed:") || strings.HasPrefix(obligation, "source_sanity_error:") {
			hasSourceSanityFailure = true
			break
		}
	}
	if !hasSourceSanityFailure {
		return false
	}
	return reviewRepairCompletionGateRejectionCount(results) < 2
}

func reviewRepairCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(result.Summary), "Review-repair completion gate rejected") {
			count++
		}
	}
	return count
}

func reviewRepairSourceSanityRepairEvidencePrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response, and Phase Evidence still has failed source_sanity obligations: " + strings.Join(planThenExecuteReviewRepairFailedPhaseObligations(brief), ", ") + ".",
		"Before choosing the terminal review-repair outcome, you may take one bounded source-read action only if it is needed to produce an executable corrective plan for the failed source_sanity diagnostics.",
		"Prefer the changed/allowed repair path first. If the compiler error names a referenced local type or symbol outside the repair path, read only the bounded source body needed to match that existing type shape.",
		"Do not run commands, do not broaden discovery, and do not inspect for confidence. After the bounded evidence returns, close with exactly one terminal outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
		"`Decision: pass` is invalid while source_sanity remains failed.",
	}
	return strings.Join(lines, "\n\n")
}

func reviewRepairTerminalOutcomeRequiredPrompt(brief workerruntime.WorkerRuntimeBrief, tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "that tool"
	}
	lines := []string{
		fmt.Sprintf("%s is not available after the review-repair closeout gate has fired.", tool),
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		lines = append(lines,
			"Phase Evidence still has failed obligations: "+strings.Join(failed, ", ")+". `Decision: pass` is invalid in this state.",
			"Your next visible response must be exactly one terminal review-repair outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
		)
	} else {
		lines = append(lines, "Your next visible response must be exactly one terminal review-repair outcome: `Decision: pass` with a `Verification Checklist`, a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact blocker.")
	}
	lines = append(lines, "Do not read, search, refresh context, run commands, or add prose before or after the chosen terminal shape.")
	return strings.Join(lines, "\n\n")
}

func concreteSourceEvidenceAlreadyRead(results []workspace.ToolResult) bool {
	for _, result := range results {
		if concreteSourceEvidenceResult(result) {
			return true
		}
	}
	return false
}

func concreteSourceEvidencePaths(results []workspace.ToolResult) []string {
	return sourceEvidenceBodyPaths(results)
}

func implementationTargetWriteReadySourcedPathSummary(results []workspace.ToolResult, max int) string {
	paths := concreteSourceEvidencePaths(results)
	if len(paths) == 0 {
		return ""
	}
	paths = uniqueSortedRunnerPaths(paths)
	if max <= 0 || max > len(paths) {
		max = len(paths)
	}
	parts := append([]string{}, paths[:max]...)
	if len(paths) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(paths)-max))
	}
	return strings.Join(parts, ", ")
}

func (r *Runner) markConcreteSourceEvidenceObserved(result workspace.ToolResult) {
	if r == nil || r.Workspace == nil || !concreteSourceEvidenceResult(result) {
		return
	}
	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}
		if !sourceEvidenceHasBody(planThenExecuteToolResultSourceEvidence(result, path)) {
			continue
		}
		r.Workspace.MarkPathObserved(path)
	}
}

func concreteSourceEvidenceResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	if candidateAnchorOnlyResult(result) {
		return false
	}
	tool := strings.TrimSpace(result.Tool)
	switch {
	case tool == "read_file":
		return readFileResultHasConcreteSourceBody(result)
	case tool == "context_zoom":
		return contextZoomResultHasConcreteSourceBody(result)
	case tool == "code_navigate" || tool == "read_only_capability":
		return routedSourceReadOutputHasBody(result.Output) && toolResultHasNonArtifactReadPath(result)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		return toolResultHasNonArtifactReadPath(result)
	default:
		return false
	}
}

func contextZoomResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	text := strings.Join([]string{result.Summary, result.Output}, "\n")
	if strings.TrimSpace(text) == "" {
		return false
	}
	hasSourcePath := false
	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}
		hasSourcePath = true
		break
	}
	if !hasSourcePath {
		return false
	}
	lower := strings.ToLower(text)
	return routedSourceReadOutputHasBody(result.Output) ||
		strings.Contains(lower, `"source"`) ||
		strings.Contains(lower, "source body") ||
		strings.Contains(lower, "include_source")
}

func readFileResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
	if strings.TrimSpace(result.Summary) == "" || !strings.Contains(text, "read ") {
		return false
	}
	hasSourcePath := false
	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			return false
		}
		hasSourcePath = true
	}
	if !hasSourcePath {
		return false
	}
	if strings.Contains(text, "redirected") ||
		strings.Contains(text, "file_outline") ||
		strings.Contains(text, "capability: outline") ||
		strings.Contains(text, "candidate_request") {
		return false
	}
	return true
}

func toolResultHasNonArtifactReadPath(result workspace.ToolResult) bool {
	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}
		return true
	}
	return false
}

func readFileResultOnlyMentionsDiagnosticLogArtifacts(result workspace.ToolResult) bool {
	paths := readPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !isNonSourceEvidenceArtifactPath(path) {
			return false
		}
	}
	return true
}

func completionGateRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if briefIsScratchpadDeliverableLane(brief) {
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 16384
		}
		return opts
	}
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		opts = forceVisibleActionChatOptions(opts)
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 8192
		}
		return opts
	}
	if planThenExecutePlannerBrief(brief) {
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
		if state.Mode == PlanExecutePlanningModeAnchorRefresh || state.Mode == PlanExecutePlanningModeHistoryPending {
			return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
		}
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily &&
		runnerBriefRequiresMutation(brief) &&
		len(brief.RequiredEditPaths) == 0 &&
		len(changedPathsFromToolResults(results)) == 0 &&
		concreteSourceEvidenceAlreadyRead(results) &&
		mutationNoWriteCompletionGateRejectionCount(results) >= 2 {
		opts = forceVisibleActionChatOptions(opts)
		opts = planExecuteSetExactMaxTokens(opts, 768)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
		return opts
	}
	if runnerBriefRequiresMutation(brief) && len(brief.RequiredEditPaths) > 0 &&
		len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) > 0 {
		opts = implementationRequiredPathForceVisibleActionChatOptions(opts)
		if !supportsNativeTools && implementationRequiredPathAnchorsReady(brief, results) {
			opts = forceVisibleActionChatOptions(opts)
		}
		limit := implementationRequiredPathRecoveryTokenLimit(brief, results)
		opts = planExecuteSetExactMaxTokens(opts, limit)
		return opts
	}
	limit := 1536
	if runnerBriefRequiresMutation(brief) {
		limit = 1024
		if len(brief.RequiredEditPaths) == 0 {
			limit = 2048
		}
		if len(brief.RequiredEditPaths) == 0 && lastNonGateTool(results) == "read_file" && successfulToolSeen(results, "read_file") {
			// Keep the model's current thinking capability available for
			// the recovery response, but strip prior hidden reasoning from
			// the next request. For local Qwen backends, forcing thinking
			// off here can produce an empty visible response; preserving
			// current-response reasoning still lets parser-readable hidden
			// tool calls be salvaged.
			opts.PreserveThinking = false
			limit = 1024
		} else if len(brief.RequiredEditPaths) == 0 && strings.EqualFold(strings.TrimSpace(opts.ToolChoice), "required") {
			opts.ToolChoice = "auto"
		}
	}
	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}
	return opts
}

func implementationRequiredPathForceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	return opts
}

func implementationRequiredPathAnchorsReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}
	changed := changedPathsFromToolResults(results)
	for _, path := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := changed[normalized]; ok {
			continue
		}
		evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, normalized)
		if evidence.Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}
	return true
}

func implementationRequiredPathRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	if implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}
	return 4096
}

func implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	var resultText strings.Builder
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			continue
		}
		resultText.WriteString(result.Summary)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Error)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Output)
		resultText.WriteByte('\n')
	}
	return recoveryNeedsLargeBodyBudgetText(brief, resultText.String())
}

func planThenExecuteCandidatePostReadChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) {
		return opts
	}
	if planThenExecuteUnreadRequiredSourceNudge(brief, results) != "" {
		return planThenExecuteUnreadRequiredSourceChatOptions(opts, brief, results)
	}
	readEvidenceSeen := successfulToolSeen(results, "read_file") || lastNonGateTool(results) == "read_file" || len(readPathsFromResults(results)) > 0
	if len(brief.RequiredEditPaths) > 0 {
		if !readEvidenceSeen {
			if opts.MaxTokens <= 0 {
				opts.MaxTokens = 16384
			}
			return opts
		}
		if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
			opts = forceVisibleActionChatOptions(opts)
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
			return planExecuteCapMaxTokens(opts, 4096)
		}
		opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
		limit := planThenExecuteRecoveryTokenLimit(brief, results)
		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			limit = 16384
		}
		if opts.MaxTokens <= 0 || opts.MaxTokens < limit || opts.MaxTokens > limit {
			opts.MaxTokens = limit
		}
		return opts
	}
	if readEvidenceSeen {
		opts = forceVisibleActionChatOptions(opts)
		if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
			opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
			if supportsNativeTools {
				if planThenExecuteCandidateScopeFullyRead(brief, results, 4) && chatOptionsExposeTool(opts, "submit_plan") {
					opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
					opts.ToolChoice = "required"
				} else {
					opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
				}
			}
		} else {
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
		}
	}
	opts = planExecuteCapMaxTokens(opts, 4096)
	return opts
}

func planThenExecuteRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return recoveryNeedsLargeBodyBudgetText(brief, planThenExecuteRecoveryResultText(results))
}

func recoveryNeedsLargeBodyBudgetText(brief workerruntime.WorkerRuntimeBrief, resultText string) bool {
	text := strings.ToLower(strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredEditPaths, "\n"),
		resultText,
	}, "\n"))
	for _, phrase := range []string{"create new file", "add new file", "write new file", "whole-file", "replace_all", "full file body", "new file", "missing parent"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	for _, path := range brief.RequiredEditPaths {
		lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if lowerPath == "" {
			continue
		}
		base := filepath.Base(lowerPath)
		if strings.Contains(lowerPath, "/new/") || strings.HasPrefix(base, "new_") {
			return true
		}
		if strings.Contains(text, lowerPath) &&
			(strings.Contains(text, " create ") || strings.HasPrefix(text, "create ") ||
				strings.Contains(text, " add ") || strings.HasPrefix(text, "add ") ||
				strings.Contains(text, " write ") || strings.HasPrefix(text, "write ")) {
			return true
		}
	}
	return false
}

func planThenExecuteRecoveryResultText(results []workspace.ToolResult) string {
	var b strings.Builder
	for _, result := range results {
		b.WriteString(result.Summary)
		b.WriteByte('\n')
		b.WriteString(result.Error)
		b.WriteByte('\n')
		b.WriteString(result.Output)
		b.WriteByte('\n')
	}
	return b.String()
}

func (r *Runner) planThenExecuteSubmitPlanParseRecoveryPrompt(raw string, parseErr error, results []workspace.ToolResult) (string, bool) {
	plan, ok := r.planThenExecuteSubmitPlanParseRecoveryPlan(raw, parseErr, results)
	if !ok {
		return "", false
	}
	return RenderPlanExecuteRecoveryPlan(plan), true
}

func (r *Runner) planThenExecuteSubmitPlanParseRecoveryPlan(raw string, parseErr error, results []workspace.ToolResult) (PlanExecuteRecoveryPlan, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || parseErr == nil {
		return PlanExecuteRecoveryPlan{}, false
	}
	stringifiedEdits := submitPlanEditsStringParseError(raw, parseErr)
	if !stringifiedEdits && !planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, results) {
		return PlanExecuteRecoveryPlan{}, false
	}
	if planThenExecuteReadyToSubmitNudge(r.RuntimeBrief, results) == "" {
		return PlanExecuteRecoveryPlan{}, false
	}
	required := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 12)
	return planThenExecuteSubmitPlanParseRecoveryPlan(stringifiedEdits, required), true
}

func submitPlanEditsStringParseError(raw string, parseErr error) bool {
	if parseErr == nil {
		return false
	}
	lower := strings.ToLower(parseErr.Error() + "\n" + raw)
	return strings.Contains(lower, "toolrequest.edits") && strings.Contains(lower, "string")
}

func planThenExecuteReadyToSubmitChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteReadyToSubmitChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecuteNativeSubmitPlanNudge(brief workerruntime.WorkerRuntimeBrief) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	var b strings.Builder
	b.WriteString("PLANNER READINESS REACHED: every required_edit_path has anchor-ready source evidence in this turn.\n\n")
	b.WriteString("This backend now exposes only the native `submit_plan` tool for the terminal planner action. Call `submit_plan` now. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString(" Do not answer in prose.")
	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}
	return b.String()
}

func planThenExecuteNativeSubmitPlanCorrectionPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	var b strings.Builder
	b.WriteString("The previous native `submit_plan` tool call was rejected before it became a valid plan artifact. ")
	b.WriteString(lastSubmitPlanCorrectionRejectionBrief(result))
	if details := planThenExecuteNativeSubmitPlanCorrectionDetails(brief, result); details != "" {
		b.WriteString(details)
		b.WriteString(" ")
	}
	b.WriteString("Call native `submit_plan` again now. ")
	b.WriteString("The next tool call must be a corrected `submit_plan`. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString(" Do not answer in prose, and do not call read/search/discovery, shell, verification, or per-edit write tools.")
	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}
	return b.String()
}

func (r *Runner) planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(modelErr error, opts llama.ChatOptions, results []workspace.ToolResult, used int) (string, bool) {
	if r == nil || r.Client == nil || modelErr == nil || used > 0 || !r.planThenExecutePlannerPhase() {
		return "", false
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		return "", false
	}
	detail := strings.TrimSpace(modelErr.Error())
	lower := strings.ToLower(detail)
	if !strings.Contains(lower, "tool_choice") || !strings.Contains(lower, "required") || !strings.Contains(lower, "did not produce any tool calls") {
		return "", false
	}
	required := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 12)
	var b strings.Builder
	b.WriteString("Native submit_plan tool-call transport failed before the model produced a tool call. For this one recovery retry, native tools are disabled and the textual tool parser is active.\n\n")
	b.WriteString("Emit exactly one visible JSON object for `submit_plan` now. Do not write prose before or after it. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}
	return b.String(), true
}

func planThenExecuteReadyToSubmitChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if planThenExecuteReadyToSubmitNudge(brief, results) == "" {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}
	if len(brief.RequiredEditPaths) == 0 && !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}
	if planThenExecuteSourceSatisfiedTerminalRequiredActive(results) {
		return planThenExecuteSourceSatisfiedTerminalChatOptions(opts, brief, results)
	}
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if chatOptionsExposeTool(opts, "submit_plan") {
		opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
	}
	if supportsNativeTools && chatOptionsExposeTool(opts, "submit_plan") {
		opts.ToolChoice = "required"
	}
	limit := planThenExecuteRecoveryTokenLimit(brief, results)
	opts = planExecuteSetExactMaxTokens(opts, limit)
	return opts
}

func planThenExecuteSourceSatisfiedTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts = forceVisibleActionChatOptions(opts)
	opts.Tools = nil
	opts.ToolChoice = ""
	opts.ForceNativeTools = false
	opts.ResponseFormatJSON = false
	limit := planThenExecuteRecoveryTokenLimit(brief, results)
	opts = planExecuteSetExactMaxTokens(opts, limit)
	return opts
}

func planThenExecuteSourceSatisfiedTerminalRequiredActive(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "submit_plan" {
			continue
		}
		if strings.TrimSpace(result.Error) == "" {
			return false
		}
		return planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
	}
	return false
}

func planThenExecuteTextualSubmitPlanFallbackChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, false)
	limit := planThenExecuteRecoveryTokenLimit(brief, results)
	opts = planExecuteSetExactMaxTokens(opts, limit)
	return opts
}

func planThenExecuteUnreadRequiredSourceChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if planThenExecuteUnreadRequiredSourceNudge(brief, results) == "" {
		return opts
	}
	opts = forceVisibleActionChatOptions(opts)
	if chatOptionsExposeAnyTool(opts, "read_file", "code_navigate") {
		opts.Tools = filterChatOptionsTools(opts.Tools, "read_file", "code_navigate")
		opts.ToolChoice = "required"
	}
	opts = planExecuteCapMaxTokens(opts, 768)
	return opts
}

func planThenExecuteCandidateWriteOnlyRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 {
		return opts
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}
	if len(readPathsFromResults(results)) == 0 && !successfulToolSeen(results, "read_file") && lastNonGateTool(results) != "read_file" {
		return opts
	}
	if supportsNativeTools && !chatOptionsExposeTool(opts, "submit_plan") {
		return opts
	}
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
		opts.ToolChoice = "required"
	}
	limit := planThenExecuteRecoveryTokenLimit(brief, results)
	opts = planExecuteSetExactMaxTokens(opts, limit)
	return opts
}

func planThenExecuteRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	requiredPathCount := len(normalizedRequiredEditPathSet(brief.RequiredEditPaths))
	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			return 16384
		}
		if requiredPathCount == 0 {
			return 16384
		}
		// Multi-file textual submit_plan output serializes full edit
		// bodies into one JSON object. Four-path source+test plans have
		// hit the 8192-token ceiling mid-JSON, so keep the larger budget
		// scoped to terminal plan synthesis/correction.
		if requiredPathCount >= 4 {
			return 16384
		}
		if requiredPathCount >= 3 {
			return 8192
		}
		return 1024
	}
	if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}
	if lastNonGateTool(results) == "submit_plan" {
		return 4096
	}
	if requiredPathCount >= 4 {
		return 16384
	}
	if requiredPathCount >= 3 {
		return 8192
	}
	return 4096
}

func planThenExecuteRejectedPlanCorrectionOnly(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); !ok {
		return false
	}
	if planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results) {
		return false
	}
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}
	return !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results)
}

func planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results []workspace.ToolResult) bool {
	lastCoverage := -1
	lastSourceSatisfied := -1
	for i, result := range results {
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			lastSourceSatisfied = i
			continue
		}
		if planThenExecuteSubmitPlanRejectedResult(result) {
			lastCoverage = i
		}
	}
	return lastCoverage >= 0 && lastSourceSatisfied > lastCoverage
}

func planThenExecutePostReadinessRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts)
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		opts = forceVisibleActionChatOptions(opts)
		refreshTools := planThenExecuteAnchorRefreshToolNames()
		if chatOptionsExposeAnyTool(opts, refreshTools...) {
			opts.Tools = filterChatOptionsTools(opts.Tools, refreshTools...)
			opts.ToolChoice = "required"
		} else {
			opts.Tools = nil
			opts.ToolChoice = ""
			opts.ForceNativeTools = false
			opts.ResponseFormatJSON = false
		}
		opts = planExecuteSetExactMaxTokens(opts, planThenExecuteAnchorRefreshMaxTokens)
		return opts
	}
	if state.Mode == PlanExecutePlanningModeHistoryPending {
		opts = forceVisibleActionChatOptions(opts)
		historyTools := planExecuteHistoryPendingToolNames()
		if chatOptionsExposeAnyTool(opts, historyTools...) {
			opts.Tools = filterChatOptionsTools(opts.Tools, historyTools...)
			opts.ToolChoice = "required"
		} else {
			opts.Tools = nil
			opts.ToolChoice = ""
			opts.ForceNativeTools = false
			opts.ResponseFormatJSON = false
		}
		opts = planExecuteSetExactMaxTokens(opts, 1024)
		return opts
	}
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) == 0 {
		return planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}
	return planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
}

func planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts.ResponseFormatJSON = false
	opts.EnableThinking = nil
	opts.ChatTemplateKwargs = false
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	opts = planExecuteCapMaxTokens(opts, 4096)
	return opts
}

func planThenExecuteRejectedPlanNeedsSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}
	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}
	if len(refreshPaths) == 0 {
		return false
	}
	for _, path := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		normalized := normalizeRunnerPath(path)
		if _, ok := refreshPaths[normalized]; !ok {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[normalized]; invalid {
			if !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, normalized) {
				return true
			}
			continue
		}
		if !successfulSourceEvidencePathAfter(results, rejectedAt, normalized) {
			return true
		}
	}
	return false
}

func planThenExecuteRejectedPlanRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}
	var out []string
	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, ok := refreshPaths[path]; !ok {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

func planThenExecuteRefreshScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	if len(required) > 0 {
		return required
	}
	return candidateRecoveryScopePaths(brief, results, 16)
}

func planThenExecuteRefreshScopePathsForRejectedPlan(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, refreshPaths map[string]struct{}) []string {
	paths := planThenExecuteRefreshScopePaths(brief, results)
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 || len(refreshPaths) == 0 {
		return paths
	}
	seen := normalizedRequiredEditPathSet(paths)
	for path := range refreshPaths {
		normalized := normalizeRunnerPath(path)
		if !planThenExecuteRecoveryScopePathAllowed(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}
	return paths
}

func planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	target := "the stale or missing required edit path named by the submit_plan rejection"
	if len(refreshPaths) > 0 {
		target = strings.Join(refreshPaths, ", ")
	}
	return strings.Join([]string{
		"The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanRejectionBrief(rejected),
		"This is an anchor-refresh phase, not a submit-plan phase. The corrected plan cannot be valid until source evidence is refreshed for: " + target + ".",
		"Your next visible message must be exactly one parser-readable bounded source-body request for one of those paths. For symbol reads, use " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + ", a real path from the rejection/current evidence, and a real declaration pattern from current evidence. Or use `read_file` with explicit `start_line`/`end_line` only when that exact range comes from current source evidence.",
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or reply in prose until the bounded refresh returns.",
	}, "\n\n")
}

func planThenExecuteRequiredWriteOnlyRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) == 0 {
		return opts
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}
	if supportsNativeTools && !chatOptionsExposeTool(opts, "submit_plan") {
		return opts
	}
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.Tools = filterChatOptionsTools(opts.Tools, "submit_plan")
		opts.ToolChoice = "required"
	}
	limit := planThenExecuteRecoveryTokenLimit(brief, results)
	opts = planExecuteSetExactMaxTokens(opts, limit)
	return opts
}

func planThenExecuteSubmitPlanTerminalChatOptions(opts llama.ChatOptions, supportsNativeTools bool) llama.ChatOptions {
	opts = planExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools && !chatOptionsExposeTool(opts, "submit_plan") {
		opts.Tools = nil
		opts.ToolChoice = ""
		opts.ForceNativeTools = false
		opts.ResponseFormatJSON = true
	}
	return opts
}

func planThenExecuteTerminalSubmitPlanAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}
	if planThenExecuteSubmitPlanHistoryMissing(brief, results) {
		return false
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true})
	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		return false
	}
	if state.Mode == PlanExecutePlanningModeSubmitReady || state.Mode == PlanExecutePlanningModeSubmitCorrection {
		return true
	}
	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		allowed := normalizedRequiredEditPaths(brief.AllowedEditPaths)
		return len(allowed) == 1 && len(state.SelectedEditPaths) > 0
	}
	if len(unreadCandidateRecoveryScopePathsForRecovery(brief, results, 8)) > 0 {
		return false
	}
	return len(state.SelectedEditPaths) > 0
}

func planThenExecuteTerminalSubmitPlanAllowedLegacy(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !planThenExecutePlannerBrief(brief) || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	if len(brief.RequiredEditPaths) > 0 {
		return planThenExecuteReadyToSubmitNudge(brief, results) != ""
	}
	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
		return false
	}
	if !(successfulToolSeen(results, "read_file") || len(readPathsFromResults(results)) > 0 || lastNonGateTool(results) == "read_file") {
		return false
	}
	return planThenExecuteCandidateScopeFullyRead(brief, results, 4)
}

func planThenExecuteOpenCandidateSourceCanSubmit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 || len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		return false
	}
	return len(readPathsFromResults(results)) > 0
}

func planThenExecuteCandidateScopeFullyRead(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) bool {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return true
	}
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return false
	}
	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return false
		}
	}
	return true
}

func forceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false
	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false
	return opts
}

func planThenExecuteTerminalAwareToolChoice(current string) string {
	if strings.TrimSpace(current) == "" || strings.EqualFold(strings.TrimSpace(current), "required") {
		return "auto"
	}
	return current
}

func chatOptionsExposeTool(opts llama.ChatOptions, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, tool := range opts.Tools {
		if strings.TrimSpace(tool.Function.Name) == name {
			return true
		}
	}
	return false
}

func chatOptionsExposeAnyTool(opts llama.ChatOptions, names ...string) bool {
	for _, name := range names {
		if chatOptionsExposeTool(opts, name) {
			return true
		}
	}
	return false
}

func filterChatOptionsTools(tools []llama.Tool, names ...string) []llama.Tool {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	filtered := make([]llama.Tool, 0, len(tools))
	for _, tool := range tools {
		if _, ok := allowed[strings.TrimSpace(tool.Function.Name)]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func (r *Runner) requiredPathCompletionRecoveryRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	missing := r.remainingRequiredEditPaths(results)
	submitCorrection := r.planThenExecutePlannerPhase() && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results)
	anchorRefreshState := PlanExecutePlanningState{}
	anchorRefresh := false
	if r.planThenExecutePlannerPhase() {
		anchorRefreshState = DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		anchorRefresh = anchorRefreshState.Mode == PlanExecutePlanningModeAnchorRefresh && len(anchorRefreshState.RefreshPaths) > 0
	}
	if len(missing) == 0 || (!submitCorrection && !anchorRefresh && requiredPathBlockerAnswerCovers(reply, missing)) {
		return workspace.ToolResult{}, false
	}
	if !anchorRefresh && r.planThenExecutePlannerPhase() && r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
		return workspace.ToolResult{}, false
	}
	if r.planThenExecutePlannerPhase() {
		if anchorRefresh {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Completion gate rejected anchor-refresh final answer",
				Error:   "bounded source-body refresh remains required for rejected submit_plan anchor path(s): " + quotedRunnerStrings(anchorRefreshState.RefreshPaths) + ". Final answers, `Decision: source-satisfied`, and `<<QUESTION>>:` do not satisfy this phase while a concrete refresh path is available. Issue exactly one bounded `read_file` or `code_navigate` source-body request for one refresh path before corrected `submit_plan`.",
			}, true
		}
		correction := ""
		if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			correctionBrief := lastSubmitPlanRejectionBrief(rejected)
			if submitCorrection {
				correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
			}
			correction = " Previous rejected submit_plan drafts do not count as coverage. " + correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejected)
		}
		if submitCorrection {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Completion gate rejected non-submit_plan correction",
				Error:   "required edit path(s) remain uncovered by corrected submit_plan: " + quotedRunnerStrings(missing) + "." + correction + " Submit-correction mode has exactly one valid outcome: parser-readable submit_plan. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not reply in prose.",
			}, true
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected non-specific submit_plan blocker",
			Error:   "required edit path(s) remain uncovered by submit_plan: " + quotedRunnerStrings(missing) + "." + correction + " After completion-gate recovery, the required contract outcome is submit_plan (" + planThenExecuteSubmitPlanCoverageSentence() + "), `Decision: source-satisfied` with current-state evidence for every remaining required path, or <<QUESTION>> with one path-specific blocker reason for every remaining required path.",
		}, true
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Completion gate rejected non-specific required-path blocker",
		Error:   "required edit path(s) remain unchanged: " + quotedRunnerStrings(missing) + ". After completion-gate recovery, the required contract outcome is a concrete write or <<QUESTION>> with one path-specific blocker reason for every remaining required path.",
	}, true
}

func requiredPathBlockerAnswerCovers(reply string, missing []string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || len(missing) == 0 {
		return false
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	if !containsAny(lower,
		"blocked",
		"blocker",
		"cannot",
		"can't",
		"could not",
		"unable",
		"permission",
		"approval",
	) {
		return false
	}
	for _, path := range missing {
		if !strings.Contains(trimmed, path) {
			return false
		}
	}
	return true
}

func (r *Runner) completionGateRepeatedBlockerAnswer(result workspace.ToolResult, results []workspace.ToolResult) string {
	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = "the final answer did not satisfy the active completion gate"
	}
	if r != nil && !runnerBriefRequiresMutation(r.RuntimeBrief) && strings.TrimSpace(result.Summary) != "Final answer rejected by blocker check" {
		if answer, ok := r.hostOwnedReadOnlyCloseoutAnswer(results); ok {
			return answer
		}
	}
	changedPaths := mapKeys(changedPathsFromToolResults(results))

	var b strings.Builder
	b.WriteString("<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.")
	if len(changedPaths) > 0 {
		b.WriteString("\n\nChanged paths:\n")
		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		missing := r.remainingRequiredEditPaths(results)
		if len(missing) > 0 {
			b.WriteString("\nRequired edit paths not changed:\n")
			for _, path := range missing {
				b.WriteString("- ")
				b.WriteString(path)
				b.WriteString("\n")
			}
			b.WriteString("\nPath-level blockers:\n")
			for _, path := range missing {
				b.WriteString("- ")
				b.WriteString(path)
				if r.planThenExecutePlannerPhase() {
					b.WriteString(": no successful submit_plan edit entry was recorded for this path, and the recovery reply did not provide source-satisfied evidence or a path-specific blocker reason.\n")
				} else {
					b.WriteString(": no successful write was recorded for this path, and the recovery reply did not provide a path-specific blocker reason.\n")
				}
			}
		}
	}
	b.WriteString("\nMissing completion evidence: ")
	b.WriteString(reason)
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		b.WriteString("\n\nRequired next evidence: state the supported concrete finding from the gathered source evidence, or name the exact missing claim and the one bounded probe that would resolve it.")
	}
	if plannerOwnsVerification(r.RuntimeBrief) && hasPendingVerificationAfterWrites(results, r.RuntimeBrief) {
		b.WriteString("\n\nRequired next evidence: compiler-backed source-sanity or language diagnostics for source edits, one cheap non-build sanity check for non-source edits, or a concrete planner-owned verification blocker. `git diff --check` and generic source-sanity fallback are only patch hygiene for source edits.")
	}
	return strings.TrimSpace(b.String())
}

func (r *Runner) planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult, nativeSubmitPlan bool) (string, bool) {
	if r == nil || lastResult == nil || !r.planThenExecutePlannerPhase() {
		return "", false
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" || !planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return "", false
	}
	if !planThenExecuteSubmitPlanRejectedForCorrection(req, *lastResult) {
		return "", false
	}
	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return "", false
	}
	if nativeSubmitPlan {
		return planThenExecuteNativeSubmitPlanCorrectionPrompt(r.RuntimeBrief, *lastResult), true
	}
	prompt := "The last request repeated the same invalid `submit_plan`; it was not executed again. " +
		lastSubmitPlanCorrectionRejectionBrief(*lastResult) +
		"Submit a corrected parser-readable `submit_plan` now. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not read/search/discovery again, and do not reply in prose. " +
		planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, *lastResult)
	return prompt, true
}

func (r *Runner) planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult) (string, string, bool) {
	if _, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastResult, results, false); !ok {
		return "", "", false
	}
	reason := ""
	if lastResult != nil {
		reason = lastSubmitPlanCorrectionRejectionBrief(*lastResult)
	}
	return "Repeated invalid submit_plan rejected",
		"this exact invalid `submit_plan` was repeated after submit-correction prompts. " + reason + "The only valid next action is a corrected parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not call read/search/discovery, and do not choose a different tool.",
		true
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base string) string {
	return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce exactly one visible parser-readable `submit_plan` request now. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not call read/search/discovery.\n\n" + base
}

func (r *Runner) hostOwnedReadOnlyCloseoutAnswer(results []workspace.ToolResult) (string, bool) {
	if r == nil || runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return "", false
	}
	return readOnlyClosureDecisionForResults(r.RuntimeBrief, results).HostOwnedAnswer(r.agentName())
}

func readOnlyHostCloseoutHasObjectiveRelevantEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, evidence readOnlyAnswerabilityLedger) bool {
	closeout := readOnlyClosureDecisionForResults(brief, results)
	closeout.Evidence = evidence
	return closeout.HasObjectiveRelevantEvidence()
}

func textSharesObjectiveTerm(text string, objective string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	objective = strings.ToLower(strings.TrimSpace(objective))
	if text == "" || objective == "" {
		return false
	}
	for _, term := range recoveryObjectiveSearchWords(objective, 32) {
		term = singularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 {
			continue
		}
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func lowercaseFirstSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	first := value[0]
	if first >= 'A' && first <= 'Z' {
		return string(first+'a'-'A') + value[1:]
	}
	return value
}

func mentionedEvidencePathsFromToolResults(results []workspace.ToolResult, max int) []string {
	return mentionedEvidencePathsFromToolResultsForObjective(results, "", max)
}

func mentionedEvidencePathsFromToolResultsForObjective(results []workspace.ToolResult, objective string, max int) []string {
	var paths []string
	for _, result := range results {
		if !acceptedMentionedEvidenceResultForObjective(result, objective) {
			continue
		}
		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Summary))
		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Output))
		paths = mergeRunnerStateStrings(paths, pathparse.MentionedWorkspacePaths(result.Error))
		if len(paths) >= max {
			break
		}
	}
	return closurePromptEvidencePaths(paths, max)
}

func acceptedMentionedEvidenceResult(result workspace.ToolResult) bool {
	return acceptedMentionedEvidenceResultForObjective(result, "")
}

func acceptedMentionedEvidenceResultForObjective(result workspace.ToolResult, objective string) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	if concreteSourceEvidenceResult(result) || readOnlyDiagnosticEvidenceResult(result) {
		return true
	}
	if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Output) == "" {
		return false
	}
	if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
		return readOnlyEvidencePacketAnswerableForObjective(packet, objective)
	}
	if strings.TrimSpace(objective) != "" {
		return childPacketContainsGroundedAnswerEvidence(objective, result.Output) ||
			delegatedPacketAddressesObjective(objective, result.Output)
	}
	return delegatedTaskOutputHasFindingEvidence(result.Output)
}

// verificationContractSkipsGate reports whether the brief's
// VerificationContract value declares the post-mutation verification
// gate inapplicable. The gate is the right shape for atoms that
// produce code requiring tests/builds to confirm correctness; it's
// the wrong shape for atoms that only edit prose, prompts, or other
// text where the file write IS the deliverable.
//
// Generic mechanism — task-family declarations choose the value:
//   - "tests_passed_or_failed_or_skipped_with_blocker": gate fires
//     (current default for code-producing families).
//   - "not_required_for_text_mutations": gate skips (text-only
//     families like docs_edit, prompt_edit_security).
//   - any "not_required_for_*_lane" suffix: gate skips (read-only
//     lanes, by definition).
//
// The host project picks the right verification_contract per family
// in its workerruntime catalog; the runner enforces consistently.
// Adding a new "no verification needed" task family is now a one-line
// catalog change instead of a runner patch.
func verificationContractSkipsGate(contract string) bool {
	return workerruntime.VerificationContractSkipsGate(contract)
}

func readOnlyExplanationGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.TrimSpace(brief.Contract)
	readOnlyPermission := workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
	family := strings.TrimSpace(brief.TaskFamily)
	if workerruntime.TaskFamilyIsPlanThenExecuteReview(family) {
		return false
	}
	switch contract {
	case string(workerruntime.ContractArchitecture):
		return readOnlyPermission || workerruntime.TaskFamilyIsArchitecture(family)
	case string(workerruntime.ContractReviewFindings):
		return readOnlyPermission ||
			family == "review" ||
			family == string(workerruntime.TaskFamilySecurityReview) ||
			family == string(workerruntime.TaskFamilyPerformanceReview)
	}
	if !readOnlyPermission {
		return false
	}
	switch family {
	case string(workerruntime.TaskFamilyArchitecture), "review":
		return true
	default:
		return false
	}
}

// retryAnswerHedgeRejectionReason checks whether the retry answer from
// ensureNonEmptyAnswer is a hedge or meta-response about evidence sufficiency
// rather than a substantive answer. This catches cases where the model, after
// being forced to retry an empty response, produces text that describes why it
// cannot answer instead of actually answering.
func retryAnswerHedgeRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || len(trimmed) > 500 {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"does not retain enough",
		"not retain enough",
		"cannot provide",
		"unable to provide",
		"retained source context",
		"not sufficient",
		"insufficient evidence",
		"does not contain enough",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer is a hedge about evidence sufficiency rather than a substantive response to the user's question"
		}
	}
	return ""
}

func readOnlyExplanationMetaRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"i have sufficient evidence",
		"the strongest implementation evidence is in",
		"the inspected implementation evidence is enough",
		"the retained source context identifies",
		"the gathered evidence points to these implementation surfaces",
		"enough for a bounded answer",
		"implementation surface",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer describes evidence sufficiency or implementation surface instead of explaining the concrete behavior from the inspected code. State the mechanism or findings directly."
		}
	}
	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 && !readOnlyExplanationHasMechanismVerb(lower) {
		return "the answer mostly inventories paths or scope and does not explain what the inspected code does. State the concrete behavior those files establish."
	}
	return ""
}

func readOnlyExplanationHasMechanismVerb(lower string) bool {
	for _, phrase := range []string{
		" uses ", " use ", " used ", " delegates ", " delegate ", " delegated ",
		" creates ", " create ", " created ", " calls ", " call ", " called ",
		" contains ", " contain ", " contained ",
		" invokes ", " invoke ", " invoked ", " routes ", " route ", " routed ",
		" runs ", " run ", " ran ", " returns ", " return ", " returned ",
		" passes ", " pass ", " passed ", " enforces ", " enforce ", " enforced ",
		" computes ", " compute ", " computed ", " evaluates ", " evaluate ", " evaluated ",
		" determines ", " determine ", " determined ", " derives ", " derive ", " derived ",
		" spawns ", " spawn ", " spawned ", " builds ", " build ", " built ",
		" wires ", " wire ", " wired ", " folds ", " fold ", " folded ",
		" hands ", " hand ", " handed ", " launches ", " launch ", " launched ",
		" finds ", " find ", " found ", " locates ", " locate ", " located ",
		" identifies ", " identify ", " identified ",
		" sanitizes ", " sanitize ", " sanitized ", " redacts ", " redact ", " redacted ",
		" masks ", " mask ", " masked ", " replaces ", " replace ", " replaced ", " replacing ",
		" filters ", " filter ", " filtered ", " scrubs ", " scrub ", " scrubbed ",
		" summarizes ", " summarize ", " summarized ",
	} {
		if strings.Contains(" "+lower+" ", phrase) {
			return true
		}
	}
	return false
}

func readOnlyExplanationObjectiveRejectionReason(reply string, brief workerruntime.WorkerRuntimeBrief) string {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	objective := strings.TrimSpace(brief.Objective)
	if objective == "" || !readOnlyExplanationGateApplies(brief) {
		return ""
	}
	body := substantiveAnswerWithoutEchoedObjective(reply, objective)
	if strings.TrimSpace(body) == "" {
		return "the answer repeats the question but does not provide a substantive explanation from the inspected evidence."
	}
	terms := readOnlySearchHitObjectiveTerms(objective)
	if len(terms) == 0 {
		return ""
	}
	objectiveText := answerTextWithoutPathCitations(body)
	lower := strings.ToLower(answerProseForObjectiveMatching(body))
	mechanismText := strings.ToLower(answerProseForObjectiveMatching(objectiveText))
	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return ""
	}
	matches := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			matches++
		}
	}
	if matches >= 2 {
		return ""
	}
	if sourceBodyHasStrongObjectiveToken(objectiveText, objective) && readOnlyExplanationHasMechanismVerb(mechanismText) {
		return ""
	}
	for _, term := range longestObjectiveTerms(terms, 2) {
		if strings.Contains(lower, term) && readOnlyExplanationHasMechanismVerb(mechanismText) {
			return ""
		}
	}
	return "the answer is grounded in inspected evidence, but the substantive answer body does not address the user's objective terms. Do not rely on an echoed question or path citation as the answer; state the concrete behavior or finding tied to the objective."
}

func answerTextWithoutPathCitations(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(answer); {
		if answer[i] != '`' {
			b.WriteByte(answer[i])
			i++
			continue
		}
		end := strings.IndexByte(answer[i+1:], '`')
		if end < 0 {
			b.WriteByte(' ')
			i++
			continue
		}
		span := answer[i+1 : i+1+end]
		if !looksLikePathCitationToken(span) {
			b.WriteByte(' ')
			b.WriteString(span)
			b.WriteByte(' ')
		} else {
			b.WriteByte(' ')
		}
		i += end + 2
	}
	words := strings.Fields(b.String())
	if len(words) == 0 {
		return ""
	}
	kept := make([]string, 0, len(words))
	for _, word := range words {
		trimmed := strings.Trim(word, ".,:;!?()[]{}\"'")
		if looksLikePathCitationToken(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, " ")
}

func looksLikePathCitationToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	value = strings.Trim(value, ".,:;!?()[]{}\"'")
	return strings.Contains(value, "/") ||
		strings.HasSuffix(value, ".go") ||
		strings.Contains(value, ".go:") ||
		strings.Contains(value, "\\")
}

func answerProseForObjectiveMatching(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	var b strings.Builder
	inCodeSpan := false
	for i := 0; i < len(answer); i++ {
		ch := answer[i]
		if ch == '`' {
			inCodeSpan = !inCodeSpan
			b.WriteByte(' ')
			continue
		}
		if inCodeSpan {
			continue
		}
		b.WriteByte(ch)
	}
	words := strings.Fields(b.String())
	if len(words) == 0 {
		return ""
	}
	kept := make([]string, 0, len(words))
	for _, word := range words {
		trimmed := strings.Trim(word, ".,:;!?()[]{}\"'")
		if looksLikePathCitationToken(trimmed) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, " ")
}

func substantiveAnswerWithoutEchoedObjective(answer string, objective string) string {
	body := strings.TrimSpace(answer)
	if body == "" {
		return ""
	}
	lowerBody := strings.ToLower(body)
	if strings.HasPrefix(lowerBody, "for `") {
		if end := strings.Index(body[5:], "`,"); end >= 0 {
			body = strings.TrimSpace(body[5+end+2:])
			body = strings.TrimLeft(body, " ,:")
		}
	}
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(lines[0])), "question:") {
		body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	for _, variant := range []string{
		strings.TrimSpace(objective),
		strings.TrimSpace(delegatedDisplayQuestion(objective)),
	} {
		if variant == "" {
			continue
		}
		body = strings.ReplaceAll(body, variant, "")
		body = strings.ReplaceAll(body, strings.ToLower(variant), "")
	}
	return strings.TrimSpace(body)
}

func architectureUnsupportedExactDetail(reply string, results []workspace.ToolResult) []string {
	exactSymbols := exactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return nil
	}
	mentioned := explicitAnswerCodeSymbols(reply)
	if len(mentioned) == 0 {
		return nil
	}
	var unsupported []string
	for _, symbol := range mentioned {
		key := normalizedAnswerSymbol(symbol)
		if key == "" {
			continue
		}
		if architectureNonSourceSymbolTerm(key) {
			continue
		}
		if _, ok := exactSymbols[key]; ok {
			continue
		}
		if answerSymbolHasExplicitUncertainty(reply, symbol) {
			continue
		}
		unsupported = util.AppendUnique(unsupported, symbol)
	}
	return unsupported
}

func exactEvidenceSymbolSet(results []workspace.ToolResult) map[string]struct{} {
	symbols := make(map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		if candidateAnchorOnlyResult(result) {
			continue
		}
		if concreteSourceEvidenceResult(result) {
			addSourceBodyEvidenceSymbols(symbols, result.Output)
		}
		addOutlineEvidenceSymbols(symbols, result)
		if !presentableExactProbeResult(result) {
			continue
		}
		_, symbol := extractExactProbeAnchor(result)
		key := normalizedAnswerSymbol(symbol)
		if key != "" {
			symbols[key] = struct{}{}
		}
		for _, call := range exactProbeCallNames(symbol, result.Output, 8) {
			key := normalizedAnswerSymbol(call)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
	return symbols
}

func architectureNonSourceSymbolTerm(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "code_navigate",
		"context_impact",
		"context_index",
		"context_merge",
		"context_pack",
		"context_query",
		"context_status",
		"context_trace",
		"context_trace_pack",
		"context_zoom",
		"ctags_symbols",
		"dependency_graph",
		"discovery_query",
		"discovery_state",
		"file_stats",
		"harness_audit",
		"history_range",
		"history_symbol",
		"joern_scan",
		"progressive_refinement",
		"read_file",
		"read_only_capability",
		"search_content",
		"search_files",
		"semgrep_scan",
		"source_sanity",
		"structural_search":
		return true
	default:
		return false
	}
}

func addOutlineEvidenceSymbols(symbols map[string]struct{}, result workspace.ToolResult) {
	if strings.TrimSpace(result.Error) != "" {
		return
	}
	if strings.TrimSpace(result.Tool) != "code_navigate" && !strings.HasSuffix(strings.TrimSpace(result.Tool), "_file_outline") {
		return
	}
	text := result.Summary + "\n" + result.Output
	if !strings.Contains(strings.ToLower(text), "file_outline") && !strings.Contains(strings.ToLower(text), "outlined ") {
		return
	}
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") || !strings.Contains(line, " lines ") {
			continue
		}
		head := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if before, _, ok := strings.Cut(head, " lines "); ok {
			head = before
		}
		for _, part := range strings.Split(head, ",") {
			key := normalizedAnswerSymbol(part)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
}

func addSourceBodyEvidenceSymbols(symbols map[string]struct{}, output string) {
	for _, line := range exactProbeSourceLines(output) {
		for _, raw := range sourceBodyEvidenceSymbolCandidates(line) {
			key := normalizedAnswerSymbol(raw)
			if key != "" {
				symbols[key] = struct{}{}
			}
		}
	}
}

func sourceBodyEvidenceSymbolCandidates(line string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		key := normalizedAnswerSymbol(raw)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, raw)
	}
	if name := sourceBodyFunctionName(stripSourceEvidenceLinePrefix(strings.TrimSpace(line))); name != "" {
		add(name)
	}
	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, token := range codeShapedTokenPattern.FindAllString(line, -1) {
		add(token)
	}
	for _, token := range codeShapedSnakeCasePattern.FindAllString(line, -1) {
		add(token)
	}
	for _, token := range strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}
	return out
}

func explicitAnswerCodeSymbols(reply string) []string {
	symbols := make([]string, 0, 6)
	add := func(raw string) {
		key := normalizedAnswerSymbol(raw)
		if key == "" {
			return
		}
		symbols = util.AppendUnique(symbols, raw)
	}
	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(reply, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	return symbols
}

func normalizedAnswerSymbol(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`'\"()[]{}<>,.:;!?")
	qualified := raw
	if dot := strings.LastIndex(raw, "."); dot >= 0 {
		raw = strings.TrimSpace(raw[dot+1:])
	}
	candidate, ok := delegatedReadOnlyLiteralSymbolCandidate(raw)
	if !ok {
		if qualifiedCandidate, qualifiedOK := delegatedReadOnlyQualifiedSymbolCandidate(qualified); qualifiedOK {
			return strings.ToLower(qualifiedCandidate)
		}
		return ""
	}
	return strings.ToLower(candidate)
}

func delegatedReadOnlyQualifiedSymbolCandidate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	if value == "" || !strings.Contains(value, ".") || pathparse.LooksLikeWorkspacePathToken(strings.ToLower(value)) {
		return "", false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 2 {
			return "", false
		}
		hasLetter := false
		for _, r := range part {
			switch {
			case unicode.IsLetter(r):
				hasLetter = true
			case unicode.IsDigit(r), r == '_':
			default:
				return "", false
			}
		}
		if !hasLetter || (!delegatedReadOnlyLooksCodeShapedSymbol(part) && !delegatedReadOnlyLooksExportedTypeName(part)) {
			return "", false
		}
	}
	return value, true
}

func delegatedReadOnlyLooksExportedTypeName(value string) bool {
	if len(value) < 2 {
		return false
	}
	first := rune(value[0])
	if !unicode.IsUpper(first) {
		return false
	}
	hasLower := false
	for _, r := range value[1:] {
		if unicode.IsLower(r) {
			hasLower = true
			continue
		}
		if unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return hasLower
}

func answerSymbolHasExplicitUncertainty(reply string, symbol string) bool {
	window := answerSymbolEvidenceWindow(reply, symbol, 180)
	if window == "" {
		return false
	}
	return answerHasExplicitUncertainty(window)
}

func answerSymbolEvidenceWindow(reply string, symbol string, radius int) string {
	reply = strings.TrimSpace(reply)
	symbol = strings.TrimSpace(symbol)
	if reply == "" || symbol == "" {
		return ""
	}
	candidates := []string{symbol}
	if dot := strings.LastIndex(symbol, "."); dot >= 0 && dot+1 < len(symbol) {
		candidates = append(candidates, strings.TrimSpace(symbol[dot+1:]))
	}
	lowerReply := strings.ToLower(reply)
	idx := -1
	length := 0
	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\"()[]{}<>,.:;!?")
		if candidate == "" {
			continue
		}
		found := strings.Index(lowerReply, strings.ToLower(candidate))
		if found >= 0 {
			idx = found
			length = len(candidate)
			break
		}
	}
	if idx < 0 {
		return ""
	}
	if sentence := answerSentenceContainingIndex(reply, idx, length); sentence != "" {
		return sentence
	}
	if radius <= 0 {
		radius = 120
	}
	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + length + radius
	if end > len(reply) {
		end = len(reply)
	}
	return reply[start:end]
}

func answerSentenceContainingIndex(text string, idx int, length int) string {
	if idx < 0 || idx >= len(text) {
		return ""
	}
	start := 0
	for i := idx - 1; i >= 0; i-- {
		switch text[i] {
		case '.', '!', '?', '\n':
			start = i + 1
			i = -1
		}
	}
	end := len(text)
	for i := idx + length; i < len(text); i++ {
		switch text[i] {
		case '.', '!', '?', '\n':
			end = i + 1
			i = len(text)
		}
	}
	return strings.TrimSpace(text[start:end])
}

func answerHasExplicitUncertainty(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"not directly inspected",
		"not directly read",
		"not confirmed",
		"not verified",
		"did not inspect",
		"didn't inspect",
		"uncertain",
		"may ",
		"might ",
		"appears ",
		"suggests ",
		"likely ",
		"seems ",
	)
}

func runnerBriefRequiresMutation(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	contract := strings.ToLower(strings.TrimSpace(brief.Contract))
	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))
	return strings.Contains(family, "implement") ||
		strings.Contains(family, "edit") ||
		strings.Contains(contract, "implementation") ||
		strings.Contains(permission, "write")
}

func writeReadyPacketReadContractActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	return true
}

// briefHasUsableEditAnchors reports whether the brief carries inline
// edit anchors the model can actually act on without first reading
// source. Required edit paths alone don't qualify — they're a write
// allowlist, not evidence of where to put the change. Without anchors,
// the harness shouldn't claim packet-driven write-readiness, and the
// pre-write read guidance should match an abstract brief.
func briefHasUsableEditAnchors(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditSnippets) > 0 {
		return true
	}
	return exactAnchorImplementationActive(brief)
}

func runtimeBriefDefinesWriteReadyReadContract(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefHasUsableEditAnchors(brief) {
		return false
	}
	if slices.Contains(brief.Constraints, "implementation_packet_write_ready") ||
		slices.Contains(brief.Constraints, "required_edit_paths_define_write_scope") {
		return true
	}
	if brief.Readiness != nil && brief.Readiness.ReadyToWrite {
		if slices.Contains(brief.Readiness.ClosedClaims, "required_edit_paths_define_write_scope") ||
			strings.Contains(strings.ToLower(brief.Readiness.FurtherReadsRequire), "named_gap") {
			return true
		}
	}
	for _, policy := range brief.SearchPolicy {
		policy = strings.ToLower(strings.TrimSpace(policy))
		if strings.Contains(policy, "required_edit_paths") &&
			(strings.Contains(policy, "readiness-to-write") ||
				strings.Contains(policy, "readiness to write") ||
				strings.Contains(policy, "named edit-safety") ||
				strings.Contains(policy, "named_gap")) {
			return true
		}
	}
	return false
}

func implementationPreWriteToolSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return exactAnchorPreWriteSurfaceActive(brief, results)
}

func implementationScopedEditSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}
	if implementationPreWriteToolSurfaceActive(brief, results) {
		return false
	}
	changed := changedPathsFromToolResults(results)
	return len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) != 0
}

func (r *Runner) implementationTargetWriteReadyNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) || workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", "", false
	}
	if pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results); len(pending) > 0 {
		pendingActionSummary := sourceEvidencePendingRedirectActionSummary(pending, 3)
		tool := strings.TrimSpace(req.Tool)
		if tool == "" {
			tool = "requested tool"
		}
		summary = "Implementation target write-ready pending source-body obligation rejected"
		errorText = fmt.Sprintf("%s is outside the pending redirected source-body obligation. The only valid read/navigation follow-up is the exact pending source-body request: %s. Otherwise write to a sourced target path or answer <<QUESTION>> with the exact blocker/no-op evidence.", tool, pendingActionSummary)
		prompt = "The implementation lane is target-write-ready, but a redirected source-body obligation is still pending: " + pendingActionSummary + ". Use only that exact pending source-body follow-up, make one parser-readable write from already gathered source evidence, or answer <<QUESTION>> with the exact blocker/no-op evidence. Do not use references, broad read, search, shell, or unrelated navigation."
		return summary, errorText, prompt, true
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}
	sourceSentence := "Source-body evidence is already available"
	if sourcedSummary := implementationTargetWriteReadySourcedPathSummary(results, 3); sourcedSummary != "" {
		sourceSentence += " for: " + sourcedSummary
	}
	summary = "Implementation target write-ready tool rejected"
	errorText = fmt.Sprintf("%s is outside the unscoped implementation target-write-ready surface. %s; the next valid action is a parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence.", tool, sourceSentence)
	prompt = "The implementation lane is target-write-ready. " + sourceSentence + ". Do not inspect, search, or navigate that source again before the first write. Use a parser-readable write tool for the sourced target path, or answer <<QUESTION>> with the exact blocker/no-op evidence."
	return summary, errorText, prompt, true
}

func (r *Runner) writeDescriptionRepairSynthesizedRequest(req workspace.ToolRequest, repairActive bool) (workspace.ToolRequest, string, bool) {
	if r == nil || checkWriteDescription(req) == nil {
		return workspace.ToolRequest{}, "", false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}
	if reasoningToolRequestHasPlaceholder(req) || !safeWriteDescriptionSynthesisShape(req) {
		return workspace.ToolRequest{}, "", false
	}
	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return workspace.ToolRequest{}, "", false
	}
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return workspace.ToolRequest{}, "", false
		}
	}
	repaired := req
	repaired.Description = synthesizedWriteDescription(req, paths)
	return repaired, marshalToolRequestRaw(repaired), true
}

func safeWriteDescriptionSynthesisShape(req workspace.ToolRequest) bool {
	if !safeWorkspaceWriteRequestShape(req) {
		return false
	}
	if strings.TrimSpace(req.Tool) != "edit_file" {
		return true
	}
	if req.StartLine > 0 || req.EndLine > 0 {
		return req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""
	}
	return strings.TrimSpace(req.OldText) != "" && strings.TrimSpace(req.NewText) != ""
}

func synthesizedWriteDescription(req workspace.ToolRequest, paths []string) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "write"
	}
	target := "selected path"
	if len(paths) > 0 {
		target = normalizeRunnerPath(paths[0])
		if len(paths) > 1 {
			target = fmt.Sprintf("%s and %d related path(s)", target, len(paths)-1)
		}
	}
	switch tool {
	case "edit_file":
		return fmt.Sprintf("Apply the requested edit to %s after adding the missing write description.", target)
	case "write_file":
		return fmt.Sprintf("Write the requested file content to %s after adding the missing write description.", target)
	case "append_file":
		return fmt.Sprintf("Append the requested content to %s after adding the missing write description.", target)
	case "apply_patch":
		return fmt.Sprintf("Apply the requested patch to %s after adding the missing write description.", target)
	default:
		return fmt.Sprintf("Apply the requested workspace change to %s after adding the missing write description.", target)
	}
}

func (r *Runner) implementationTargetWriteReadySourceReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if r == nil || workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	if !implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		return false
	}
	if implementationTargetWriteReadyPendingRedirectProgressAllowed(r.RuntimeBrief, req, results) {
		return true
	}
	if implementationTargetWriteReadyHasPendingSourceRedirect(r.RuntimeBrief, results) {
		return false
	}
	if r.implementationTargetWriteReadyHistoryReadAllowed(req, results) {
		return true
	}
	if implementationTargetWriteReadyTargetedDiscoveryAllowed(req, results, refreshes) {
		return true
	}
	path := normalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	if concreteSourceEvidenceReadForPath(path, results) {
		if implementationTargetWriteReadySamePathContinuationReadAllowed(req, results) {
			return true
		}
		if refreshes > 0 {
			return false
		}
		return !fullSourceReadFileSeen(path, results)
	}
	if refreshes >= implementationTargetWriteReadySourceReadAllowanceLimit {
		return false
	}
	return implementationTargetWriteReadyNewTargetRead(req)
}

func implementationTargetWriteReadyTargetedDiscoveryAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if refreshes >= implementationTargetWriteReadySourceReadAllowanceLimit {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "code_navigate":
		return implementationTargetWriteReadyTargetedCodeNavigate(req, results)
	case "search_content":
		return implementationTargetWriteReadyTargetedSearchContent(req, results)
	default:
		if implementationTargetWriteReadySourceSymbolTool(req.Tool) {
			return implementationTargetWriteReadyTargetedCodeNavigate(req, results)
		}
		return false
	}
}

func implementationTargetWriteReadySourceSymbolTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
}

// targetWriteReadyStagedNudge builds a progressively stronger nudge message
// as the model accumulates non-write calls without making an edit. Early
// nudges are soft reminders; late stages require action before more discovery.
func targetWriteReadyStagedNudge(basePrompt string, req workspace.ToolRequest, nudgeCount int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}
	suffix := fmt.Sprintf("\n\nYour %s request will execute. Nudge %d of 12.", tool, nudgeCount)

	evidencePrefix := strings.TrimSpace(basePrompt)
	if evidencePrefix == "" {
		evidencePrefix = "Source evidence is available for sourced target path(s)."
	}

	if nudgeCount <= 6 {
		return fmt.Sprintf("%s Either write to a sourced target path, or name the one exact missing claim and one exact probe that would resolve it, then take that probe.%s", evidencePrefix, suffix)
	}
	return fmt.Sprintf("You have taken %d discovery calls without a write. %s Before more discovery: name the one exact missing claim and one exact probe that would resolve it. If you cannot name both, you have enough evidence — write to a sourced target path or answer <<QUESTION>> with the exact blocker.%s", nudgeCount, evidencePrefix, suffix)
}
func implementationTargetWriteReadyTargetedCodeNavigate(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	if !implementationTargetWriteReadySourcePath(path) {
		return false
	}
	if !concreteSourceEvidenceReadForPath(path, results) {
		return false
	}
	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		capability = strings.TrimSpace(req.Operation)
	}
	if capability == "" {
		return false
	}
	switch capability {
	case "find_definition", "read_symbol", "extract_body", "outline":
		return true
	default:
		return strings.TrimSpace(req.Symbol) != "" && strings.Contains(strings.ToLower(capability), "symbol")
	}
}

func implementationTargetWriteReadyTargetedSearchContent(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	pattern := strings.TrimSpace(req.Pattern)
	if len(pattern) < 4 {
		return false
	}
	if strings.TrimSpace(req.NamedGap) == "" && strings.TrimSpace(req.WhyNotAnswering) == "" {
		return false
	}
	if !implementationTargetWriteReadySearchPatternLooksNarrow(pattern) {
		return false
	}
	if implementationTargetWriteReadySearchPatternAlreadySourced(pattern, results) {
		return false
	}
	path := normalizeRunnerPath(req.Path)
	if path != "" {
		return implementationTargetWriteReadySearchPathLooksScoped(path)
	}
	return true
}

func implementationTargetWriteReadySearchPathLooksScoped(path string) bool {
	path = strings.Trim(normalizeRunnerPath(path), "/")
	if path == "" || path == "." {
		return false
	}
	switch path {
	case "internal", "cmd", "pkg", "src", "lib", "app", "apps", "packages", "test", "tests":
		return false
	default:
		return strings.Contains(path, "/") || recoverySearchPathLooksEditableSource(path)
	}
}

func implementationTargetWriteReadySearchPatternAlreadySourced(pattern string, results []workspace.ToolResult) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		tool := strings.TrimSpace(result.Tool)
		if tool != "read_file" && tool != "code_navigate" &&
			!strings.HasSuffix(tool, "_read_symbol") &&
			!strings.HasSuffix(tool, "_extract_body") {
			continue
		}
		if strings.Contains(strings.ToLower(result.Output), pattern) {
			return true
		}
	}
	return false
}

func implementationTargetWriteReadySearchPatternLooksNarrow(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) < 8 {
		return false
	}
	if strings.Contains(pattern, " ") || strings.ContainsAny(pattern, ".:_-(){}[]") {
		return true
	}
	hasUpper := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0
	hasLower := strings.IndexFunc(pattern, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
	return hasUpper && hasLower
}

func implementationTargetWriteReadyPendingRedirectProgressAllowed(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, results []workspace.ToolResult) bool {
	return sourceEvidencePendingRedirectProgressAllowedForBrief(brief, req, results)
}

func implementationTargetWriteReadyHasPendingSourceRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return len(sourceEvidencePendingRedirectsForBrief(brief, results)) > 0
}

func implementationTargetWriteReadyRedirectedReadPath(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
		return ""
	}
	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)
	if !strings.HasPrefix(lower, "redirected ") {
		return ""
	}
	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")
	if idx < 0 {
		return ""
	}
	return normalizeRunnerPath(strings.TrimSpace(tail[:idx]))
}

func implementationTargetWriteReadyRedirectedReadTool(result workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
		return "", false
	}
	summary := strings.TrimSpace(result.Summary)
	lower := strings.ToLower(summary)
	if !strings.HasPrefix(lower, "redirected ") {
		return "", false
	}
	tail := summary[len("Redirected "):]
	idx := strings.Index(strings.ToLower(tail), " to ")
	if idx < 0 {
		return "", false
	}
	fields := strings.Fields(tail[idx+len(" to "):])
	if len(fields) == 0 {
		return "", false
	}
	tool := strings.Trim(fields[0], "`'\".,;:")
	if !implementationTargetWriteReadyRedirectedSourceTool(tool) {
		if !strings.HasSuffix(tool, "_file_outline") {
			return "", false
		}
		related, ok := implementationTargetWriteReadyRedirectedRelatedSourceTool(result)
		if !ok {
			return "", false
		}
		return related, true
	}
	return tool, true
}

func implementationTargetWriteReadyRedirectedRelatedSourceTool(result workspace.ToolResult) (string, bool) {
	for _, line := range strings.Split(result.Output, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "related_capability:") ||
			!strings.Contains(lower, "read_symbol") ||
			!strings.Contains(lower, " via ") {
			continue
		}
		idx := strings.Index(lower, " via ")
		fields := strings.Fields(line[idx+len(" via "):])
		if len(fields) == 0 {
			continue
		}
		tool := strings.Trim(fields[0], "`'\".,;:")
		if implementationTargetWriteReadyRedirectedSourceTool(tool) {
			return tool, true
		}
	}
	return "", false
}

func implementationTargetWriteReadyRedirectedSourceTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	return tool == "code_navigate" ||
		tool == "read_only_capability" ||
		strings.HasSuffix(tool, "_read_symbol") ||
		strings.HasSuffix(tool, "_extract_body")
}

func implementationTargetWriteReadySamePathContinuationReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	path := normalizeRunnerPath(req.Path)
	if path == "" || strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	if req.StartLine <= 0 || req.EndLine < req.StartLine || req.EndLine-req.StartLine+1 > 400 {
		return false
	}
	ranges := readRangesForPathFromResults(results, path)
	if len(ranges) == 0 || len(ranges) >= implementationTargetWriteReadySamePathContinuationLimit {
		return false
	}
	return implementationTargetWriteReadyRequestedRangeAddsSource(req.StartLine, req.EndLine, ranges)
}

// Caps total same-path read_file ranges, including the range already read
// before continuations. New-target source expansions use their own turn-level
// allowance; this cap prevents one admitted path from becoming open discovery.
const implementationTargetWriteReadySamePathContinuationLimit = 3

func implementationTargetWriteReadyRequestedRangeAddsSource(start int, end int, ranges []planThenExecuteReadRange) bool {
	if start <= 0 || end < start {
		return false
	}
	if total, ok := implementationTargetWriteReadyKnownTotalLines(ranges); ok {
		if start > total {
			return false
		}
		if end > total {
			end = total
		}
	}
	for line := start; line <= end; line++ {
		covered := false
		for _, readRange := range ranges {
			rangeStart := readRange.start
			rangeEnd := readRange.end
			if rangeStart <= 0 || rangeEnd < rangeStart {
				continue
			}
			if readRange.total > 0 && rangeEnd > readRange.total {
				rangeEnd = readRange.total
			}
			if line >= rangeStart && line <= rangeEnd {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}

func implementationTargetWriteReadyKnownTotalLines(ranges []planThenExecuteReadRange) (int, bool) {
	total := 0
	for _, readRange := range ranges {
		if readRange.total <= 0 {
			continue
		}
		if total == 0 || readRange.total < total {
			total = readRange.total
		}
	}
	return total, total > 0
}

func (r *Runner) implementationTargetWriteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, refreshes int) bool {
	if !r.implementationTargetWriteReadySourceReadAllowed(req, results, refreshes) {
		return false
	}
	return concreteSourceEvidenceReadForPath(req.Path, results)
}

func implementationTargetWriteReadyNewTargetRead(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Path) != ""
}

const implementationTargetWriteReadySourceReadAllowanceLimit = 3

func implementationTargetSourceReadEstablishedWriteReady(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || !implementationTargetWriteReadySourcePath(path) {
		return false
	}
	if !runnerStringSliceContainsPath(brief.RequiredEditPaths, path) &&
		!runnerStringSliceContainsPath(brief.EvidencePaths, path) &&
		!runnerStringSliceContainsPath(brief.AllowedEditPaths, path) {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "read_file", "code_navigate":
	default:
		if !strings.HasSuffix(strings.TrimSpace(req.Tool), "_read_symbol") &&
			!strings.HasSuffix(strings.TrimSpace(req.Tool), "_extract_body") {
			return false
		}
	}
	return concreteSourceEvidenceReadForPath(path, results)
}

func runnerStringSliceContainsPath(values []string, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, value := range values {
		if normalizeRunnerPath(value) == path {
			return true
		}
	}
	return false
}

func implementationTargetWriteReadySourcePath(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	return !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".mdx")
}

func (r *Runner) implementationPreWriteNonWriteRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results) || r.writeOnlyRecoveryToolIsWrite(req.Tool) {
		return "", "", "", false
	}
	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "non-write tool"
	}
	summary = "Write-ready implementation non-write tool rejected"
	errorText = fmt.Sprintf("the target packet and required_edit_paths already establish write readiness for %s. %s does not satisfy the pre-write implementation contract; available outcomes are an allowed write capability or <<QUESTION>> with the exact path-specific blocker.", target, tool)
	prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s. Non-write tools are now hidden for this turn. The available contract outcome is the smallest safe write capability for each remaining required path. If the supplied anchor is stale or missing, answer with <<QUESTION>> and name that exact blocker; do not perform additional discovery or task planning.", target)
	return summary, errorText, prompt, true
}

func (r *Runner) unscopedContextPackCandidateReadRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) || !contextPackSeen(results) || len(concreteRecoveryEvidencePaths(results, 1)) > 0 {
		return "", "", "", false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", "", false
	}
	if contextPackCandidateReadAllowed(req, results) {
		return "", "", "", false
	}
	summary = "Context packet candidate read rejected"
	errorText = fmt.Sprintf("context_pack has not established an exact edit target yet. %s is only a packet candidate, so recovery must first gather literal or semantic evidence tied to the user's requested behavior.", strings.TrimSpace(req.Path))
	prompt = "The context packet did not establish an exact edit target. Do not read or write packet candidates yet. Run one bounded fallback discovery action tied to literal words, symbols, or behavior from the user's actual request, and include a named gap explaining that the missing fact is a grounded source hit. After a concrete hit, read the highest-confidence path before writing."
	return summary, errorText, prompt, true
}

func (r *Runner) deterministicPlanExecuteGroundingRecoveryRequest(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if planThenExecuteTerminalReplyShape(reply) {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
			return workspace.ToolRequest{}, "", false
		}
		rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
		if !ok || !submitPlanRejectionHasInvalidAnchor(rejected) {
			return workspace.ToolRequest{}, "", false
		}
	}
	rejectedAt := -1
	for i := len(results) - 1; i >= 0; i-- {
		if planThenExecuteSubmitPlanGroundingRejectedResult(results[i]) {
			rejectedAt = i
			break
		}
	}
	if rejectedAt < 0 {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		if rejected, fallbackRejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok &&
			submitPlanRejectionHasInvalidAnchor(rejected) &&
			state.Mode == PlanExecutePlanningModeAnchorRefresh {
			rejectedAt = fallbackRejectedAt
		}
	}
	if rejectedAt < 0 {
		return workspace.ToolRequest{}, "", false
	}
	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && state.Mode == PlanExecutePlanningModeAnchorRefresh {
		for _, path := range state.RefreshPaths {
			normalized := normalizeRunnerPath(path)
			if normalized == "" {
				continue
			}
			start, end := planThenExecuteAnchorRefreshFallbackRange(normalized, results, rejected)
			req := workspace.ToolRequest{
				Tool:            "read_file",
				Path:            normalized,
				StartLine:       start,
				EndLine:         end,
				NamedGap:        "refresh current source evidence for the rejected submit_plan edit path",
				WhyNotAnswering: "need bounded source evidence before resubmitting the corrected plan",
			}
			return req, marshalToolRequestRaw(req), true
		}
		return workspace.ToolRequest{}, "", false
	}
	if !successfulToolAfter(results, rejectedAt, "context_pack") {
		task := strings.TrimSpace(r.RuntimeBrief.Objective)
		if task == "" {
			task = "localize candidate edit paths for the unscoped plan-execute atom"
		}
		req := workspace.ToolRequest{Tool: "context_pack", Task: task, Mode: "plan", Limit: 8}
		return req, marshalToolRequestRaw(req), true
	}
	if !successfulToolAfter(results, rejectedAt, "search_content") {
		if pattern := r.planExecuteGroundingSearchPattern(); pattern != "" {
			req := workspace.ToolRequest{
				Tool:            "search_content",
				Path:            ".",
				Pattern:         pattern,
				NamedGap:        "locate exact current source for the requested plan-execute atom before submit_plan",
				WhyNotAnswering: "need grounded source hits before plan-execute can submit edits or source-satisfied",
			}
			return req, marshalToolRequestRaw(req), true
		}
	}
	candidates := append([]string{}, candidateRecoveryScopePaths(r.RuntimeBrief, results[rejectedAt+1:], 8)...)
	candidates = appendUniqueRunnerPaths(candidates, contextPackCandidatePaths(results[rejectedAt+1:], 8)...)
	read := readPathsFromResults(results[rejectedAt+1:])
	if len(read) >= 4 {
		return workspace.ToolRequest{}, "", false
	}
	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; ok {
			continue
		}
		req := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "current source evidence for the candidate edit path before submit_plan",
			WhyNotAnswering: "need source-body evidence before plan-execute can submit edits or source-satisfied",
		}
		return req, marshalToolRequestRaw(req), true
	}
	return workspace.ToolRequest{}, "", false
}

func planThenExecuteAnchorRefreshFallbackRange(path string, results []workspace.ToolResult, rejected workspace.ToolResult) (int, int) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 1, 180
	}
	if line, ok := submitPlanSyntaxValidationLineForPath(rejected, path); ok {
		start := line - 40
		if start < 1 {
			start = 1
		}
		return start, line + 40
	}
	ranges := readRangesForPathFromResults(results, path)
	for i := len(ranges) - 1; i >= 0; i-- {
		if ranges[i].start <= 0 || ranges[i].end < ranges[i].start {
			continue
		}
		end := ranges[i].end
		if ranges[i].total > 0 && end > ranges[i].total {
			end = ranges[i].total
		}
		return ranges[i].start, end
	}
	return 1, 180
}

func (r *Runner) deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if !planThenExecuteSourceSatisfiedAnswerIntent(reply) || r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
		return workspace.ToolRequest{}, "", false
	}
	evidencePaths := PlanThenExecuteSourceSatisfiedEvidencePaths(reply)
	if len(evidencePaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	candidateSet := normalizedRequiredEditPathSet(candidateRecoveryScopePaths(r.RuntimeBrief, results, 16))
	requiredSet := normalizedRequiredEditPathSet(r.requiredEditCoveragePaths())
	for _, path := range evidencePaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
			continue
		}
		if len(requiredSet) > 0 {
			if _, ok := requiredSet[normalized]; !ok {
				continue
			}
		} else if len(candidateSet) > 0 {
			if _, ok := candidateSet[normalized]; !ok {
				continue
			}
		}
		if planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized).Level == planThenExecuteEvidenceAnchorReady {
			continue
		}
		req := workspace.ToolRequest{
			Tool:            "read_file",
			Path:            normalized,
			StartLine:       1,
			EndLine:         180,
			NamedGap:        "source-satisfied current-state evidence for the named path",
			WhyNotAnswering: "need source-body evidence before accepting the plan-execute source-satisfied closeout",
		}
		return req, marshalToolRequestRaw(req), true
	}
	return workspace.ToolRequest{}, "", false
}

func planThenExecuteGroundingRejectedSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
			return true
		}
	}
	return false
}

func (r *Runner) planExecuteGroundingSearchPattern() string {
	if r == nil {
		return ""
	}
	objectiveTerms := recoveryObjectiveSearchWords(r.RuntimeBrief.Objective, 24)
	objectiveSet := map[string]struct{}{}
	for _, term := range objectiveTerms {
		objectiveSet[singularObjectiveWord(term)] = struct{}{}
	}
	patterns := make([]string, 0, 12)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		patterns = append(patterns, regexp.QuoteMeta(value))
	}
	for _, tool := range r.RuntimeBrief.AllowedTools {
		tool = strings.TrimSpace(tool)
		if !toolNameOverlapsObjective(tool, objectiveSet) {
			continue
		}
		add(tool)
		if len(patterns) >= 4 {
			break
		}
	}
	if len(patterns) > 0 {
		return strings.Join(patterns, "|")
	}
	for _, term := range objectiveTerms {
		add(term)
		if len(patterns) >= 8 {
			break
		}
	}
	return strings.Join(patterns, "|")
}

func toolNameOverlapsObjective(tool string, objectiveSet map[string]struct{}) bool {
	if strings.TrimSpace(tool) == "" || len(objectiveSet) == 0 {
		return false
	}
	parts := strings.FieldsFunc(strings.ToLower(tool), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	matches := 0
	for _, part := range parts {
		if len(part) < 4 {
			continue
		}
		if _, ok := objectiveSet[singularObjectiveWord(part)]; ok {
			matches++
		}
	}
	return matches >= 2 || (len(parts) == 1 && matches == 1)
}

func recoveryObjectiveSearchWords(objective string, max int) []string {
	if max <= 0 {
		max = 8
	}
	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return nil
	}
	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")
	lower = replacer.Replace(lower)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := recoverySearchStopWords()
	terms := make([]string, 0, max)
	seen := make(map[string]struct{})
	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}
		if _, skip := stop[word]; skip {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		terms = append(terms, word)
		if len(terms) >= max {
			break
		}
	}
	return terms
}

func singularObjectiveWord(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return strings.TrimSuffix(word, "s")
	}
	return word
}

func planThenExecuteTerminalReplyShape(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "Decision: source-satisfied") ||
		strings.HasPrefix(trimmed, "Decision: block") ||
		strings.HasPrefix(trimmed, "<<QUESTION>>:")
}

func successfulToolAfter(results []workspace.ToolResult, index int, tool string) bool {
	tool = strings.TrimSpace(tool)
	for i := index + 1; i < len(results); i++ {
		if strings.TrimSpace(results[i].Tool) == tool && strings.TrimSpace(results[i].Error) == "" {
			return true
		}
	}
	return false
}

func appendUniqueRunnerPaths(paths []string, more ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths)+len(more))
	for _, path := range append(paths, more...) {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || !pathparse.HasWorkspaceFileExtension(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func contextPackCandidateReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	target := normalizeRunnerPath(req.Path)
	if target == "" || !pathparse.HasWorkspaceFileExtension(target) {
		return false
	}
	candidates := contextPackCandidatePaths(results, 32)
	for _, candidate := range candidates {
		if candidate == target {
			return true
		}
	}
	return false
}

func contextPackCandidatePaths(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 8
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "context_pack" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		var view struct {
			CandidateFiles []struct {
				Path string `json:"path"`
				Role string `json:"role"`
			} `json:"candidate_files"`
		}
		if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
			continue
		}
		for _, file := range view.CandidateFiles {
			role := strings.TrimSpace(file.Role)
			if role != "" && role != "editable" && role != "read_only_evidence" && role != "test" {
				continue
			}
			add(file.Path)
			if len(paths) >= max {
				return paths
			}
		}
	}
	return paths
}

func recoverySearchPathLooksEditableSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}
	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml", ".md", ".mdx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func literalRecoverySearchPattern(objective string) string {
	terms := recoveryObjectiveSearchWords(objective, 8)
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, regexp.QuoteMeta(term))
	}
	return strings.Join(quoted, "|")
}

func recoverySearchStopWords() map[string]struct{} {
	return map[string]struct{}{
		"actual": {}, "please": {}, "replace": {}, "with": {}, "from": {}, "that": {}, "this": {}, "the": {}, "and": {}, "for": {}, "you": {}, "can": {}, "top": {}, "right": {}, "left": {}, "path": {}, "alias": {},
		"continuing": {}, "continue": {}, "completion": {}, "complete": {}, "work": {}, "docs": {}, "document": {}, "design": {}, "implement": {}, "implementation": {}, "bounded": {}, "topic": {}, "only": {}, "must": {},
		"against": {}, "existing": {}, "focus": {}, "focused": {}, "add": {}, "regression": {}, "tests": {}, "test": {}, "proving": {}, "without": {}, "partial": {}, "durable": {}, "mutation": {},
		"repo": {}, "repository": {}, "discovery": {}, "first": {}, "make": {}, "minimal": {}, "code": {}, "changes": {}, "change": {}, "requested": {}, "request": {},
		"tdd": {}, "prd": {}, "adr": {}, "brief": {}, "research": {}, "workflow": {}, "workflows": {}, "markdown": {},
		// "model" intentionally kept — it carries domain signal for path→objective matching.
		"send": {}, "through": {}, "plan": {}, "execute": {}, "expose": {}, "read": {}, "external": {}, "agents": {},
	}
}

func (r *Runner) implementationScopedDisallowedToolRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	var extReg *workspace.ExternalToolRegistry
	if r != nil && r.Workspace != nil {
		extReg = r.Workspace.ExternalToolsRegistry()
	}
	if lastResultSummaryIs(results, "Missing write description") {
		return "", "", "", false
	}
	if r == nil || !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) || implementationScopedEditToolAllowedWithRegistry(req.Tool, extReg) {
		return "", "", "", false
	}
	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}
	summary = "Scoped implementation tool rejected"
	errorText = fmt.Sprintf("%s is outside the scoped required-path implementation surface for %s. The available contract outcomes are required-path write capability, semantic/context/declared project discovery for a named gap, a narrow raw read on a required path or evidence path, or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract menu is: required-path edit, context/language/declared read-only project discovery for the exact edit-safety fact, narrow raw read only for anchor confirmation on a required path or evidence path, or <<QUESTION>> with the exact blocker. Do not use task planning, shell, opaque external tools, or broad raw search until the required-path contract is resolved.", target)
	return summary, errorText, prompt, true
}

func (r *Runner) scopedImplementationLastChanceWriteOnlyApplies(policy RunPolicy, usedModelCalls int, results []workspace.ToolResult) bool {
	if r == nil || policy.MaxModelCalls <= 0 || usedModelCalls <= 0 {
		return false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return false
	}
	if !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	return usedModelCalls+1 >= policy.MaxModelCalls
}

func (r *Runner) scopedImplementationLastChanceWriteOnlyPrompt(results []workspace.ToolResult) string {
	target := "the remaining required edit path(s)"
	if r != nil {
		if paths := r.remainingRequiredEditPaths(results); len(paths) > 0 {
			target = strings.Join(paths, ", ")
		} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
			target = strings.Join(paths, ", ")
		}
	}
	return fmt.Sprintf("This is the final model-call budget slot for the scoped implementation run. Required edit paths remain unchanged: %s. Non-write tools are now hidden. Use one available parser-readable write tool for an in-scope required path from the packet and gathered source evidence, or answer exactly `<<QUESTION>>:` with the concrete path-specific blocker/no-op evidence. Do not read, search, inspect, or narrate a plan.", target)
}

func lastResultSummaryIs(results []workspace.ToolResult, summary string) bool {
	if len(results) == 0 {
		return false
	}
	return strings.TrimSpace(results[len(results)-1].Summary) == strings.TrimSpace(summary)
}

func (r *Runner) writeDescriptionRepairPrompt(req workspace.ToolRequest) string {
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		path = "the required edit path"
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the write tool"
	}
	return fmt.Sprintf("Write-description repair is active for %s on %s. Missing: a non-empty `description` field. Valid next actions: reissue the same in-scope write with `description`, or answer `<<QUESTION>>:` with the exact blocker. Invalid now: search, broad discovery, unrelated reads, unrelated writes, or prose final answer.", tool, path)
}

func (r *Runner) emitWriteDescriptionValidatorDecision(step int, req workspace.ToolRequest, status string, lastFailure string, recoveryID string) {
	if r == nil {
		return
	}
	path := normalizeRunnerPath(req.Path)
	payload := map[string]any{
		"schema":               "idiom.dynamic_validator_decision.v1",
		"step":                 step,
		"status":               strings.TrimSpace(status),
		"invariant_id":         "write.description.required",
		"recovery_id":          strings.TrimSpace(recoveryID),
		"last_failure":         strings.TrimSpace(lastFailure),
		"tool":                 strings.TrimSpace(req.Tool),
		"path":                 path,
		"missing":              []string{"write_description_missing"},
		"valid_next_actions":   []string{"same_write_with_description", "typed_blocker"},
		"invalid_next_actions": []string{"search", "broad_discovery", "unrelated_read", "unrelated_write", "prose_final"},
		"hide_tools":           []string{"non_write_tools"},
	}
	r.emitEvent(marshalRunEvent("dynamic_validator_decision", payload))
}

func (r *Runner) writeReadyPacketReadWithoutGapRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, hideNonWrite bool, reject bool) {
	if r == nil || !postReadinessToolContractForBrief(r.RuntimeBrief, results, false, false).RequireSourceRefreshContract || !r.scopedReadClassTool(req) {
		return "", "", "", false, false
	}
	hideNonWrite = implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results)
	missingGap := postReadinessGap(req) == ""
	// The read scope is the FULL required-edit-paths set plus explicit
	// packet evidence paths, not just the remaining edit paths. Once the
	// model has written one file in the brief, reads on that file are still
	// legitimate — the model often needs to reference its own header to write
	// the impl, verify a declaration it just landed, or mirror an existing
	// pattern from a sibling required file. Explicit evidence paths are also
	// legitimate read-only inputs (for example refine's previous draft/review
	// files). Tool-result evidence is intentionally not scope: context/query
	// packets can mention candidate or related files, and those candidates
	// must support reasoning without expanding the allowed read surface.
	readScope := implementationScopedExplicitReadScope(r.RuntimeBrief)
	outOfScope := implementationScopedEditSurfaceActive(r.RuntimeBrief, results) && !readRequestTargetsRequiredEditPath(req, readScope)
	if outOfScope && r.scopedImplementationDiscoveryLookupAllowed(req, readScope) {
		return "", "", "", false, false
	}
	// In-scope reads (target one of the brief's required_edit_paths) are
	// allowed without an explicit named_gap. The safety reason is
	// implicit — "verify the anchor on a path we're about to edit" — and
	// the schema describes named_gap as optional, so requiring it on
	// in-scope reads contradicted the surface the planner sees. Local
	// Qwen consistently calls code_navigate without named_gap and got
	// stuck; the previous behavior also rejected the asymmetric case
	// where read_file happened to include named_gap and code_navigate
	// didn't (Phase 6.5 / Phase 5b dispatch debugging, 2026-05-10).
	// Out-of-scope reads still get blocked regardless of named_gap —
	// that's the protection the gate is meant to provide.
	if !outOfScope {
		return "", "", "", false, false
	}
	scratchpadDeliverable := briefIsScratchpadDeliverableLane(r.RuntimeBrief)
	_ = missingGap // retained for the rejection-message switch below
	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	summary = "Scoped implementation read rejected"
	switch {
	case missingGap && outOfScope:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must target a required edit path or explicit evidence path and include named_gap describing one exact edit-safety fact.", target)
	case missingGap:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must include named_gap describing one exact edit-safety fact.", target)
	default:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. This read/search targets outside the required edit paths and explicit evidence paths.", target)
	}
	if scratchpadDeliverable {
		prompt = fmt.Sprintf("The implementation packet defines a non-source deliverable for %s, and the attempted read/search was outside the required or evidence paths. Treat this as scope evidence for the deliverable: record the missing or out-of-scope path in a blocker or scope-correction section instead of broadening discovery.", target)
	} else if hideNonWrite {
		prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s, and the attempted read/search did not satisfy the contract. Read/search tools are now hidden for this turn. The available contract outcomes are the smallest safe write to each remaining required path, or <<QUESTION>> naming a stale or missing supplied anchor.", target)
	} else {
		prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract outcomes are an allowed edit on a required path, semantic/context/declared project discovery for a named edit-safety gap, one narrow raw read inside a required path or explicit evidence path for anchor confirmation, or <<QUESTION>> naming the blocker when a remaining path cannot be edited safely from the packet plus scoped evidence.", target)
	}
	return summary, errorText, prompt, hideNonWrite, true
}

func implementationScopedExplicitReadScope(brief workerruntime.WorkerRuntimeBrief) []string {
	return mergeRunnerStateStrings(brief.RequiredEditPaths, brief.EvidencePaths)
}

func (r *Runner) scratchpadDeliverableFailedEvidenceNudge(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || strings.TrimSpace(result.Error) == "" {
		return "", false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) || !r.scopedReadClassTool(req) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return "", false
	}
	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"
	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}
	if reason == "" {
		reason = "the optional evidence request failed"
	}
	return fmt.Sprintf("The optional evidence request for %s failed: %s. For this non-source deliverable, treat that as an evidence fact: record the missing, stale, ambiguous, or out-of-scope evidence in the deliverable itself rather than repairing the lookup through another raw or lower-level read. The next action should be either writing the deliverable with the evidence gap recorded, or answering <<QUESTION>> if the gap makes the deliverable impossible.", target, reason), true
}

func scratchpadDeliverableFailedEvidenceResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if !briefIsScratchpadDeliverableLane(brief) {
		return workspace.ToolResult{}, false
	}
	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
		return workspace.ToolResult{}, false
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" || workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}
		if strings.TrimSpace(result.Summary) == "Scratchpad evidence follow-up rejected" {
			continue
		}
		if isReadClassTool(workspace.ToolRequest{Tool: result.Tool}) {
			return result, true
		}
	}
	return workspace.ToolResult{}, false
}

func scratchpadDeliverableFailedEvidenceStatus(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	failed, ok := scratchpadDeliverableFailedEvidenceResult(brief, results)
	if !ok {
		return ""
	}
	target := "the required deliverable"
	if missing := missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}
	reason := strings.TrimSpace(failed.Error)
	if reason == "" {
		reason = strings.TrimSpace(failed.Summary)
	}
	if reason == "" {
		reason = "the optional evidence request failed"
	}
	tool := strings.TrimSpace(failed.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}
	return fmt.Sprintf("closed_negative_evidence: optional %s evidence for %s failed (%s); record this as an evidence gap in the non-source deliverable if the exact source fact remains unavailable", tool, target, truncateRunnerStateText(reason, 220))
}

func (r *Runner) scopedReadClassTool(req workspace.ToolRequest) bool {
	if isReadClassTool(req) {
		return true
	}
	return r != nil && r.externalReadOnlyDiscoveryTool(req.Tool)
}

func (r *Runner) externalReadOnlyDiscoveryTool(tool string) bool {
	if r == nil || r.Workspace == nil {
		return false
	}
	extReg := r.Workspace.ExternalToolsRegistry()
	return extReg != nil && extReg.IsReadOnlyDiscovery(tool)
}

func (r *Runner) scopedImplementationDiscoveryLookupAllowed(req workspace.ToolRequest, pathScope []string) bool {
	if !scopedImplementationDiscoveryPathAllowed(req, pathScope) {
		return false
	}
	if r != nil && r.externalReadOnlyDiscoveryTool(req.Tool) {
		return true
	}
	if scopedImplementationSymbolOwnerLookupAllowed(req) {
		return true
	}
	if !implementationScopedSemanticDiscoveryToolAllowed(req.Tool) {
		return false
	}
	if strings.TrimSpace(req.Tool) == "context_status" {
		return true
	}
	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.Path, req.Command))
	if target == "" || len(target) < 3 {
		return false
	}
	profile := workspace.DiscoveryProfileForRequest(req)
	if profile.Scope == "broad" || profile.Cost == "high" {
		return false
	}
	return true
}

func (r *Runner) writeOnlyRecoveryToolIsWrite(tool string) bool {
	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}
	if r == nil || strings.TrimSpace(tool) != "submit_plan" {
		return false
	}
	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func scopedImplementationDiscoveryPathAllowed(req workspace.ToolRequest, pathScope []string) bool {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return true
	}
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}
	for _, scoped := range pathScope {
		scoped = normalizeRunnerPath(scoped)
		if scoped == "" {
			continue
		}
		if normalized == scoped {
			return true
		}
		if !runnerPathLooksLikeFile(scoped) && strings.HasPrefix(normalized, scoped+"/") {
			return true
		}
	}
	return scopedImplementationPackageDiscoveryPathAllowed(req, normalized, pathScope)
}

func runnerPathLooksLikeFile(path string) bool {
	base := filepath.Base(strings.TrimSpace(filepath.ToSlash(path)))
	return strings.Contains(base, ".")
}

func scopedImplementationPackageDiscoveryPathAllowed(req workspace.ToolRequest, normalized string, pathScope []string) bool {
	normalized = normalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(normalized)))
	if normalized == "" || normalized == "." || normalized == "/" || runnerPathLooksLikeFile(normalized) {
		return false
	}
	if !scopedImplementationDirectoryDiscoveryHasExactTarget(req) {
		return false
	}
	for _, scoped := range pathScope {
		scoped = normalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(scoped)))
		if scoped == "" || !runnerPathLooksLikeFile(scoped) {
			continue
		}
		parent := normalizeRunnerPath(filepath.ToSlash(filepath.Dir(scoped)))
		if parent == "" || parent == "." || parent == "/" {
			continue
		}
		if normalized == parent {
			return true
		}
	}
	return false
}

func scopedImplementationDirectoryDiscoveryHasExactTarget(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.NamedGap))
	if target == "" || len(target) < 3 {
		return false
	}
	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}
	if strings.Contains(target, "$") {
		return false
	}
	return true
}

func scopedImplementationSymbolOwnerLookupAllowed(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "code_navigate" || tool == "read_only_capability" {
		capability := strings.ToLower(strings.TrimSpace(req.Capability))
		switch capability {
		case "find_symbol", "definition", "declaration", "workspace_symbol", "symbol", "symbol-lookup", "symbol_lookup":
		default:
			return false
		}
		return scopedImplementationLookupTargetAllowed(req)
	}
	_, suffix, ok := runnerLanguageToolParts(tool)
	if !ok {
		return false
	}
	switch suffix {
	case "find_symbol", "find_function", "find_type", "find_const", "find_package",
		"definition", "declaration", "workspace_symbol", "read_symbol":
	default:
		return false
	}
	return scopedImplementationLookupTargetAllowed(req)
}

func scopedImplementationLookupTargetAllowed(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Query, req.ID, req.Handle))
	if target == "" || len(target) < 3 {
		return false
	}
	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}
	return true
}

func runnerLanguageToolParts(tool string) (language string, suffix string, ok bool) {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			return strings.TrimSuffix(prefix, "_"), strings.TrimPrefix(tool, prefix), true
		}
	}
	return "", "", false
}

// writeOnlyRecoveryBlockerAnswer returns the <<QUESTION>> answer the runner
// emits when write-only recovery is active and the model tried a non-write
// tool that the bounded read allowance can no longer cover. Returns ok=false
// when the situation doesn't warrant a recovery blocker (e.g. read-only
// lane, no required edit paths remain).
func (r *Runner) writeOnlyRecoveryBlockerAnswer(results []workspace.ToolResult) (string, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}
	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and required edit path(s) remain unchanged: %s. Complete those edits from the current packet/evidence, or re-dispatch with a narrower anchor for each skipped path.", strings.Join(missing, ", ")), true
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}
	target := "the requested implementation target"
	if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}
	return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and no edit has been attempted. Provide a narrower target packet or preloaded current-state anchor for %s; no workspace change was made.", target), true
}

func (r *Runner) writeOnlyRecoveryBlockerKind(results []workspace.ToolResult) string {
	if r != nil && len(r.remainingRequiredEditPaths(results)) > 0 {
		return "write_only_recovery_required_paths_remaining"
	}
	return "write_only_recovery_no_edit_attempted"
}

func (r *Runner) writeOnlyRecoveryNonWriteRepairPrompt(req workspace.ToolRequest, results []workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested non-write tool"
	}
	lines := []string{
		fmt.Sprintf("%s was rejected because write-only recovery is active after repeated no-write completion attempts.", tool),
		"Do not read, search, navigate, inspect, summarize, or plan again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}
	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		lines = append(lines, "Required edit path(s) still unchanged: "+strings.Join(missing, ", ")+".")
	}
	lines = append(lines, "Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")
	return strings.Join(lines, "\n\n")
}

func (r *Runner) remainingRequiredEditPaths(results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}
	return missingRequiredEditPaths(r.requiredEditCoveragePaths(), changedPathsFromToolResults(results))
}

func (r *Runner) requiredEditCoveragePaths() []string {
	if r == nil {
		return nil
	}
	required := r.RuntimeBrief.RequiredEditPaths
	if !r.planThenExecutePlanSubmissionPhase() {
		return required
	}
	root := ""
	if r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}
	return planThenExecuteRequiredCoveragePaths(required, root)
}

// applyPatchPreflightAction describes what the runner should do with an
// apply_patch request after the changed-paths pre-flight has inspected it.
type applyPatchPreflightAction string

const (
	// applyPatchPreflightPassthrough means the request can flow to the
	// real applyPatch unchanged.
	applyPatchPreflightPassthrough applyPatchPreflightAction = "passthrough"
	// applyPatchPreflightRewrite means the patch had at least one section
	// targeting a path already in changed_paths; those sections were
	// dropped and the runner should send the trimmed envelope to applyPatch.
	applyPatchPreflightRewrite applyPatchPreflightAction = "rewrite"
	// applyPatchPreflightAllSkipped means EVERY section in the patch
	// targeted an already-changed path. The runner short-circuits with a
	// synthesized success result instead of running an empty patch.
	applyPatchPreflightAllSkipped applyPatchPreflightAction = "all_skipped"
)

// applyPatchPreflightResult carries the pre-flight verdict back to the
// runner loop.
type applyPatchPreflightResult struct {
	Action       applyPatchPreflightAction
	Request      workspace.ToolRequest
	Result       workspace.ToolResult
	SkippedPaths []string
}

// preflightApplyPatchAgainstChangedPaths inspects an apply_patch request
// against the lane's recorded changed paths. The model commonly issues a
// multi-file patch that includes a section for a file it already wrote
// via a prior edit_file/apply_patch — the previous edit makes that
// section's hunk context stale, so applyPatch's all-or-nothing semantics
// would roll back the entire envelope including the still-valid sections
// for the remaining required paths.
//
// The pre-flight strips any Update/Delete section targeting an
// already-changed path and re-submits the trimmed envelope. If every
// section was redundant the runner skips the workspace round-trip and
// emits a success result noting which paths were already applied. If
// nothing is redundant the request flows through unchanged.
//
// This compatibility shim does not change applyPatch's semantics; it just
// drops sections that would silently fail under the existing all-or-
// nothing contract because the file is already in changed_paths.
func (r *Runner) preflightApplyPatchAgainstChangedPaths(req workspace.ToolRequest, results []workspace.ToolResult) applyPatchPreflightResult {
	pass := applyPatchPreflightResult{Action: applyPatchPreflightPassthrough, Request: req}
	if r == nil || strings.TrimSpace(req.Tool) != "apply_patch" {
		return pass
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return pass
	}
	ops, err := workspace.ParsePatch(content)
	if err != nil {
		// Let the real applyPatch surface the parse error so the model
		// gets the actual envelope-grammar problem.
		return pass
	}
	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return pass
	}
	var keptOps []workspace.PatchOp
	var skipped []string
	for _, op := range ops {
		if op.Kind != workspace.PatchOpUpdate && op.Kind != workspace.PatchOpDelete {
			keptOps = append(keptOps, op)
			continue
		}
		path := normalizeRunnerPath(op.Path)
		if path == "" {
			keptOps = append(keptOps, op)
			continue
		}
		if _, ok := changed[path]; ok {
			skipped = append(skipped, op.Path)
			continue
		}
		keptOps = append(keptOps, op)
	}
	if len(skipped) == 0 {
		return pass
	}
	if len(keptOps) == 0 {
		// Every section in the patch was a redundant touch on an
		// already-changed path. Skip the workspace call entirely and
		// report this as a no-op patch (no new changes) so the model
		// cannot confuse the synthesized result with completion. The
		// summary and output explicitly state that NO new files were
		// modified and list the remaining required paths so the model
		// has a directional next step.
		remaining := r.remainingRequiredEditPaths(results)
		var output strings.Builder
		output.WriteString("apply_patch made NO new changes — every section in this envelope targets a path already modified earlier in this lane (")
		output.WriteString(strings.Join(skipped, ", "))
		output.WriteString("), so the patch was a redundant restatement of work already done.")
		switch {
		case len(remaining) > 0:
			output.WriteString(" The implementation contract is NOT yet fulfilled: required edit path(s) ")
			output.WriteString(strings.Join(remaining, ", "))
			output.WriteString(" are still unchanged. The remaining-path write contract requires a fresh edit covering ONLY those paths; do not declare completion until each is written.")
		case len(r.RuntimeBrief.RequiredEditPaths) > 0:
			output.WriteString(" All required edit paths are already in changed_paths from earlier writes; the implementation contract is fulfilled. Submit the standard final answer with the changed-paths summary instead of any further patches.")
		}
		return applyPatchPreflightResult{
			Action:       applyPatchPreflightAllSkipped,
			Request:      req,
			SkippedPaths: skipped,
			Result: workspace.ToolResult{
				Tool:    "apply_patch",
				Summary: "apply_patch made no new changes (all sections were redundant)",
				Output:  output.String(),
			},
		}
	}
	rewritten := req
	rewritten.Content = workspace.SerializePatch(keptOps)
	return applyPatchPreflightResult{
		Action:       applyPatchPreflightRewrite,
		Request:      rewritten,
		SkippedPaths: skipped,
	}
}

// shouldForceAnswerForPostCompletionVerification returns true when the
// implementation contract is already fulfilled (all required edit paths
// have been written) and the agent is now thrashing on non-write tool
// calls to self-verify its work. Counts the current request if it is a
// non-write tool, then returns true when the running counter exceeds the
// configured cutoff. Write tools never count and reset the cutoff in
// effect because a write means the agent is actually doing more work, not
// just verifying.
func (r *Runner) shouldForceAnswerForPostCompletionVerification(req workspace.ToolRequest, results []workspace.ToolResult, counter *int) bool {
	if r == nil || counter == nil {
		return false
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}
	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return false
	}
	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
		return false
	}
	if workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	*counter++
	return *counter > postCompletionVerificationCutoff
}

// consumeRecoveryReadAllowance returns true and marks the path consumed if
// this request is a bounded read on a remaining required edit path that has
// not yet used its one-read recovery allowance. Only read_file qualifies —
// search/list/navigation tools are deliberately not permitted in recovery
// because they invite further unbounded discovery.
func (r *Runner) consumeRecoveryReadAllowance(req workspace.ToolRequest, used map[string]bool, results []workspace.ToolResult) bool {
	if r == nil || used == nil {
		return false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}
	if used[path] {
		return false
	}
	tool := strings.TrimSpace(req.Tool)
	// Allow read_file on remaining required edit paths (existing behavior).
	if tool == "read_file" {
		remaining := r.remainingRequiredEditPaths(results)
		if requestPathInRequiredEditPaths(req.Path, remaining) {
			used[path] = true
			return true
		}
		return false
	}
	// Allow code_navigate on pending obligation paths so write-only recovery
	// does not trap the model with an obligation that only code_navigate can
	// resolve.
	if tool == "code_navigate" {
		pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results)
		for _, ob := range pending {
			if normalizeRunnerPath(ob.Path) == path {
				used[path] = true
				return true
			}
		}
	}
	return false
}

func (r *Runner) exactAnchorHiddenToolBlockerAnswer(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	if r == nil || !exactAnchorImplementationActive(r.RuntimeBrief) {
		return "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}
	target := "the requested exact-anchor implementation target"
	if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "hidden tool"
	}
	return fmt.Sprintf("<<QUESTION>>: Exact-anchor implementation did not complete because %s was repeatedly requested before applying the supplied write anchor for %s. No workspace change was made. Re-dispatch with the same exact anchor as a write-capability request, or provide a narrower current-state packet if the anchor is stale.", tool, target), true
}

func (r *Runner) exactAnchorRepeatedWriteFailureBlockerAnswer(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !exactAnchorImplementationActive(r.RuntimeBrief) {
		return "", false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return "", false
	}
	target := "the requested exact-anchor implementation target"
	if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "write tool"
	}
	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}
	if reason == "" {
		reason = "the corrected write did not satisfy the active exact-anchor contract"
	}
	return fmt.Sprintf("<<QUESTION>>: Exact-anchor implementation did not complete because repeated %s write attempts for %s were rejected after the recovery nudge. No workspace change was made. Last rejection: %s", tool, target, reason), true
}

func hideImplementationWriteOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}
	for _, tool := range manager.AllowedTools() {
		if implementationPreWriteToolAllowed(tool) {
			continue
		}
		hidden[tool] = true
		owned[tool] = true
	}
}

func hideReviewRepairTerminalOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}
	for _, tool := range manager.AllowedTools() {
		if strings.TrimSpace(tool) == "submit_plan" {
			continue
		}
		hidden[tool] = true
		owned[tool] = true
	}
}

func hideImplementationScopedToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}
	extReg := manager.ExternalToolsRegistry()
	for _, tool := range manager.AllowedTools() {
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) {
			continue
		}
		hidden[tool] = true
		owned[tool] = true
	}
}

func releaseHiddenTools(hidden map[string]bool, owned map[string]bool) {
	if hidden == nil || owned == nil {
		return
	}
	for tool := range owned {
		delete(hidden, tool)
		delete(owned, tool)
	}
}

func hideExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool, manager *workspace.Manager) {
	if hidden == nil || exactHidden == nil || manager == nil {
		return
	}
	for _, tool := range manager.AllowedTools() {
		if !implementationPreWriteToolAllowed(tool) {
			hidden[tool] = true
			exactHidden[tool] = true
		}
	}
}

func implementationPreWriteToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}
	// submit_plan is the planner-phase write for plan-then-execute
	// mode; without this exception the exact-anchor pre-write filter
	// hides it and the planner has nothing to call.
	if tool == "submit_plan" {
		return true
	}
	return false
}

func implementationPreWriteAllowedTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationPreWriteToolAllowed(tool) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func implementationScopedEditToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}
	switch tool {
	case "read_file", "code_navigate":
		return true
	case "submit_plan":
		// Plan-then-execute planner phase: submit_plan IS the planner's
		// write — it captures the structured plan the executor phase
		// will dispatch. Without this case the scoped-edit surface
		// filter strips it before the model ever sees it, leaving the
		// planner with only read tools and no way to finish.
		return true
	default:
		return implementationScopedSemanticDiscoveryToolAllowed(tool)
	}
}

func implementationScopedEditToolAllowedWithRegistry(tool string, extReg *workspace.ExternalToolRegistry) bool {
	tool = strings.TrimSpace(tool)
	if implementationScopedEditToolAllowed(tool) {
		return true
	}
	return extReg != nil && extReg.IsReadOnlyDiscovery(tool)
}

func implementationScopedEditAllowedToolsWithRegistry(tools []string, extReg *workspace.ExternalToolRegistry) []string {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func implementationScopedSemanticDiscoveryToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	// Delegate explicit tool-name classification to the catalog profile.
	if len(workerruntime.AllowedToolsForCapabilityProfile([]string{tool}, workerruntime.CapabilityImplementationScopedEdit)) > 0 {
		return true
	}
	// Language-prefixed tools (go_find_symbol, cpp_definition, etc.) are
	// not in the explicit name list; classify via their discovery profile.
	profile := workspace.DiscoveryProfileForRequest(workspace.ToolRequest{Tool: tool})
	switch profile.Class {
	case "language_symbol", "language_relationship", "diagnostics":
		return true
	default:
		return false
	}
}

func releaseExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool) {
	if hidden == nil || exactHidden == nil {
		return
	}
	for tool := range exactHidden {
		delete(hidden, tool)
		delete(exactHidden, tool)
	}
}

func workspaceWriteResultCompleted(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}
	return strings.TrimSpace(result.Error) == ""
}

func exactAnchorImplementationActive(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}
	return slices.Contains(brief.Constraints, "exact_anchor_implementation")
}

func requiredEditPathScopeRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
	if len(required) == 0 || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return "", "", false
	}
	var outside []string
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if strings.TrimSpace(req.Tool) == "mkdir" && requiredEditPathAllowsMkdir(normalized, required) {
			continue
		}
		if _, ok := required[normalized]; !ok {
			outside = append(outside, normalized)
		}
	}
	if len(outside) == 0 {
		return "", "", false
	}
	return "Write outside required edit paths rejected",
		fmt.Sprintf("this implementation contract restricts writes to required_edit_paths %s; attempted write path(s) outside that scope: %s. Use an allowed path or report a concrete scope blocker.",
			quotedRunnerStrings(mapKeys(required)), quotedRunnerStrings(outside)),
		true
}

func writeRequestPaths(req workspace.ToolRequest) []string {
	switch strings.TrimSpace(req.Tool) {
	case "edit_file", "write_file", "append_file", "mkdir":
		if strings.TrimSpace(req.Path) != "" {
			return []string{req.Path}
		}
	case "apply_patch":
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}
		return workspace.PatchOpPaths(ops)
	case "go_move_declarations":
		var paths []string
		if strings.TrimSpace(req.Path) != "" {
			paths = append(paths, req.Path)
		}
		if strings.TrimSpace(req.Destination) != "" {
			paths = append(paths, req.Destination)
		}
		for _, move := range req.Moves {
			if strings.TrimSpace(move.Destination) != "" {
				paths = append(paths, move.Destination)
			}
		}
		return paths
	}
	return nil
}

func normalizedRequiredEditPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func readRequestTargetsRequiredEditPath(req workspace.ToolRequest, requiredPaths []string) bool {
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}
	if _, ok := required[path]; ok {
		return true
	}
	return false
}

func scopedReadRecoveryRejectionKey(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	target := normalizeRunnerPath(util.FirstNonEmpty(req.Path, req.ID, req.Handle, req.Query, req.Pattern, req.Task, req.Command))
	if target == "" {
		target = "<empty-target>"
	}
	return tool + ":" + normalizeActionToken(target)
}

func requiredEditPathAllowsMkdir(dir string, required map[string]struct{}) bool {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return false
	}
	for path := range required {
		parent := strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Dir(path))), "/")
		if parent == dir || strings.HasPrefix(parent, dir+"/") {
			return true
		}
	}
	return false
}

func markRuntimeBriefPathsObserved(manager *workspace.Manager, brief workerruntime.WorkerRuntimeBrief) {
	if manager == nil {
		return
	}
	candidates := append([]string{}, brief.TargetRefs...)
	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, brief.ChangedPaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.TargetPacket, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)
	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\" ,.;:")
		if candidate == "" || !strings.Contains(candidate, ".") {
			continue
		}
		manager.MarkPathObserved(candidate)
	}
}

func nonEmptyRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) == 0 {
		return nil
	}
	out := make([]string, 0, max)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
		if len(out) >= max {
			return out
		}
	}
	return out
}

func hasSuccessfulVerificationResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if resultCountsAsSuccessfulVerification(result) {
			return true
		}
	}
	return false
}

func shellCommandFromToolResult(result workspace.ToolResult) string {
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "$ "))
		}
		if line != "" {
			return ""
		}
	}
	return ""
}

func hasSuccessfulTaskResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" {
			continue
		}
		if strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}

func postChildReadBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return 3
	}
	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return 0
	}
	if runnerBriefThoroughness(brief) == "standard" {
		return 1
	}
	return 2
}

func runnerBriefThoroughness(brief workerruntime.WorkerRuntimeBrief) string {
	for _, constraint := range brief.Constraints {
		key, value, ok := strings.Cut(strings.TrimSpace(constraint), "=")
		if !ok || strings.TrimSpace(key) != "thoroughness" {
			continue
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func successfulReadClassResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		req := workspace.ToolRequest{Tool: strings.TrimSpace(result.Tool)}
		if isReadClassTool(req) {
			count++
		}
	}
	return count
}

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}
	// Args is `json:"-"` on ToolRequest (it intentionally stays out of
	// log/event serialization), so it would NOT contribute to this key
	// otherwise. For MCP tools and external tools the typed fields are
	// empty and Args carries the entire request shape — without this
	// suffix two materially-different calls (e.g. `lookup` with
	// {"enum":"JobType"} vs {"enum":"ColonistState"}) would collapse to
	// the same key and trip the "replayed prior evidence" guard.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}
	return string(data)
}

func normalizedToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}
	// Mirror the toolRequestKey suffix: Args is `json:"-"` on
	// ToolRequest so the marshal above misses it. The doom_loop_guard
	// and other fingerprint-based heuristics need to distinguish
	// external/MCP calls that carry their entire request shape in
	// Args; without this, three different lookup({enum:X}),
	// lookup({enum:Y}), lookup({enum:Z}) calls all collapse to one
	// fingerprint and trip the doom_loop_guard at threshold 3.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}
	return string(data)
}

func doomLoopForcedCloseoutPrompt(req workspace.ToolRequest, identicalCallRun int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the same tool"
	}
	return fmt.Sprintf("The exact same %s request was stopped after %d identical attempts. Tools are now disabled. Complete the active contract from the evidence already gathered. If the evidence is sufficient, answer with the required final decision or summary. If one concrete claim remains unsupported, answer with <<QUESTION>> and the exact unresolved blocker; do not request more tools.", tool, identicalCallRun)
}

func shouldRejectExactReplayBeforeLoopGate(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content", "rg", "grep":
		return true
	default:
		return false
	}
}

func normalizeRunnerToolRequest(req workspace.ToolRequest) workspace.ToolRequest {
	req.Path = normalizeRunnerPath(req.Path)
	req.Destination = normalizeRunnerPath(req.Destination)
	req.Workdir = normalizeRunnerPath(req.Workdir)
	req.NamedGap = ""
	req.WhyNotAnswering = ""
	for i := range req.Moves {
		req.Moves[i].Destination = normalizeRunnerPath(req.Moves[i].Destination)
	}
	return req
}

// truncForRetryReason trims a summary so the stored "why this was
// blocked" string stays small — model-facing text, not log-sized.
func truncForRetryReason(s string) string {
	s = strings.TrimSpace(s)
	const max = 160
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// isContentInspectionTool reports whether a tool actually inspects
// content (as opposed to listing directory names). Used to reset the
// consecutive-list_files streak — running search_content, read_file, a
// go_* symbol tool, or a cpp_* symbol tool is exactly the narrowing
// behavior we want to reward.
func isContentInspectionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "search_content", "search_files", "rg", "grep", "read_file",
		"code_navigate", "read_only_capability",
		"go_file_outline", "go_read_symbol", "go_find_symbol", "go_find_type",
		"go_find_const", "go_workspace_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body",
		"go_related_files", "go_find_package", "go_importers", "go_diagnostics",
		"cpp_file_outline", "cpp_find_symbol", "cpp_read_symbol", "cpp_workspace_symbol",
		"cpp_definition", "cpp_declaration", "cpp_implementations",
		"cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_related_files", "cpp_includes", "cpp_who_includes",
		"cpp_blast_radius", "cpp_dead_symbol", "cpp_diagnostics":
		return true
	}
	return false
}

// isReadClassTool reports whether a tool request is read-only / inspection.
// Used to drive the plan-mode read-budget nudge. Kept explicit (allow-list)
// so mutating tools can never be miscounted. Shell is excluded because it
// can mutate even when the command looks benign.
func isReadClassTool(req workspace.ToolRequest) bool {
	return workspace.IsReadTool(strings.TrimSpace(req.Tool))
}

func toolActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "list_files":
		if workspace.IsScopedInspectionRequest(req) {
			return "list_files:" + normalizeActionToken(req.Path)
		}
		return "broad_inspection"
	case "shell":
		return shellActionClass(req.Command)
	case "read_file":
		if target := util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path)); target != "" {
			action := "read_file:" + normalizeActionToken(target)
			if strings.TrimSpace(req.ID) == "" && (req.StartLine > 0 || req.EndLine > 0) {
				action += fmt.Sprintf(":%d-%d", req.StartLine, req.EndLine)
			}
			return action
		}
		return "read_file"
	case "source_sanity":
		return tool + ":" + normalizeActionToken(req.Path)
	case "code_navigate":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case "read_only_capability":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case "edit_file":
		shape := strings.TrimSpace(req.OldText) + "->" + strings.TrimSpace(req.NewText)
		if req.StartLine > 0 || req.EndLine > 0 || strings.TrimSpace(req.Content) != "" {
			shape = fmt.Sprintf("%d-%d:%s", req.StartLine, req.EndLine, strings.TrimSpace(req.Content))
		}
		return "edit_file:" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(shape)
	case "search_files", "search_content", "rg":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
	case "discovery_query":
		purpose := util.FirstNonEmpty(strings.TrimSpace(req.Purpose), strings.TrimSpace(req.Query))
		return tool + ":" + normalizeActionToken(purpose) + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case "structural_search":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case "dependency_graph":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Language)
	case "history_range", "blame_range":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(fmt.Sprintf("%d-%d", req.StartLine, req.EndLine))
	case "history_symbol", "ctags_symbols", "semgrep_scan":
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern)
	case "joern_scan":
		return tool + ":" + normalizeActionToken(req.Path)
	case "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	case "go_move_declarations":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Path)+"->"+strings.TrimSpace(req.Destination), strings.TrimSpace(req.Pattern))
		if len(req.Moves) > 0 {
			target = strings.TrimSpace(req.Path) + "->batch"
		}
		return tool + ":" + normalizeActionToken(target)
	case "task_ledger":
		return "task_ledger"
	default:
		return tool
	}
}

func isBootstrappedTargetReadReminder(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == "read_file" &&
		strings.TrimSpace(result.Summary) == "Implementation bootstrap already inspected this target" &&
		strings.TrimSpace(result.Error) != ""
}

func declarationMovesForbiddenByMessages(messages []llama.ChatMessage) bool {
	if len(messages) == 0 {
		return false
	}
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == "user" {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return false
	}
	for _, message := range messages[lastUser:] {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "system" {
			continue
		}
		if textForbidsDeclarationMoves(message.Content) {
			return true
		}
	}
	return false
}

func textForbidsDeclarationMoves(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return containsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func declarationMoveConstraintRejection(req workspace.ToolRequest, declarationMovesForbidden bool) (string, string, bool) {
	if !declarationMovesForbidden {
		return "", "", false
	}
	switch strings.TrimSpace(req.Tool) {
	case "go_move_declarations":
		return "Declaration move rejected by task constraint",
			"the current task explicitly forbids moving declarations. Do not call go_move_declarations; allowed contract outcomes are narrow same-file read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	case "go_file_outline":
		return "Go outline rejected by task constraint",
			"the current task explicitly forbids declaration moves, and go_file_outline primarily suggests declaration-move batches. Do not call go_file_outline again; allowed contract outcomes are exact-line read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	default:
		return "", "", false
	}
}

func marshalToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(data)
}

func shellActionClass(command string) string {
	if isVerificationCommandText(command) {
		return "shell_verification:" + normalizeActionToken(command)
	}

	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return "shell"
	}
	switch fields[0] {
	case "ls", "find", "tree":
		return "broad_inspection"
	case "rg", "grep", "awk", "sed", "cat", "head", "tail":
		return "shell_narrow:" + normalizeActionToken(command)
	default:
		return "shell:" + normalizeActionToken(fields[0])
	}
}

func normalizeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "_"
	}
	if len(value) > 96 {
		return value[:96]
	}
	return value
}

func containsRunnerTool(tools []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, tool := range tools {
		if strings.TrimSpace(tool) == target {
			return true
		}
	}
	return false
}

func runnerAllowsTool(tools []string, tool string) bool {
	return containsRunnerTool(tools, tool)
}

func runnerPlanExecuteTextualAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily == PlanThenExecuteReviewRepairTaskFamily {
		if !completionGateResultSeen(results) && len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) == 0 {
			allowed := filterPhaseTools(brief.AllowedTools, "read_file", "code_navigate", "context_status", "context_query", "context_zoom", "submit_plan")
			if len(allowed) > 0 {
				return allowed
			}
			return []string{"read_file", "code_navigate", "context_status", "context_query", "context_zoom", "submit_plan"}
		}
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			PhaseHint:        turnPhaseReviewRepairTerminal,
			Brief:            brief,
			Results:          results,
			BaseAllowedTools: brief.AllowedTools,
		})
		if len(contract.AllowedTools) > 0 {
			return contract.AllowedTools
		}
		return currentAllowedTools
	}
	if taskFamily != PlanThenExecutePlannerTaskFamily {
		if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
			return currentAllowedTools
		}
		return currentAllowedTools
	}
	state, contract, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if allowed := runnerPlanExecuteTextualAllowedToolsFromProjection(state, contract, baseTools); len(allowed) > 0 {
		return allowed
	}
	if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
		return currentAllowedTools
	}
	if slices.Contains(state.TerminalOptions, "submit_plan") || planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return []string{"submit_plan"}
	}
	return currentAllowedTools
}

func runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) []string {
	allowed := runnerPlanExecuteTextualAllowedTools(currentAllowedTools, brief, results)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return allowed
	}
	if len(uniquePhaseStrings(allowed)) == 1 && runnerAllowsTool(allowed, "submit_plan") {
		return allowed
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || runnerAllowsTool(allowed, tool) {
		return allowed
	}
	state, _, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if !slices.Contains(state.ParserVisibleTools, tool) {
		return allowed
	}
	if len(baseTools) > 0 && !runnerAllowsTool(baseTools, tool) {
		return allowed
	}
	return uniquePhaseStrings(append(append([]string(nil), allowed...), tool))
}

func runnerPlanExecuteTextualPlanningProjection(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (PlanExecutePlanningState, TurnPhaseContract, []string) {
	baseTools := uniquePhaseStrings(append(append([]string(nil), brief.AllowedTools...), currentAllowedTools...))
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  baseTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	return state, contract, baseTools
}

func runnerPlanExecuteTextualAllowedToolsFromProjection(state PlanExecutePlanningState, contract TurnPhaseContract, baseTools []string) []string {
	for _, tools := range [][]string{
		contract.ActionPolicy.CallableToolNames,
		contract.AllowedTools,
		state.CallableTools,
		state.ParserVisibleTools,
	} {
		allowed := runnerFilterPlanExecuteProjectionTools(baseTools, tools)
		if len(allowed) > 0 {
			return allowed
		}
	}
	return nil
}

func runnerFilterPlanExecuteProjectionTools(baseTools []string, projectionTools []string) []string {
	projectionTools = uniquePhaseStrings(projectionTools)
	if len(projectionTools) == 0 {
		return nil
	}
	if len(baseTools) == 0 {
		return projectionTools
	}
	return filterPhaseTools(baseTools, projectionTools...)
}

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !runnerAllowsTool(currentAllowedTools, "read_file") {
		return workspace.ToolRequest{}, "", false
	}
	if summary, _, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, results, currentAllowedTools); !reject || summary != "Write rejected before source-body evidence" {
		return workspace.ToolRequest{}, "", false
	}
	paths := writeRequestPaths(req)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}
	target := normalizeRunnerPath(paths[0])
	if target == "" || concreteSourceEvidenceReadForPath(target, results) {
		return workspace.ToolRequest{}, "", false
	}
	redirected := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            target,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence before applying the requested write",
		WhyNotAnswering: "need current source evidence for the model-selected write target before editing",
	}
	return redirected, marshalToolRequestRaw(redirected), true
}

type implementationDeferredSourceEvidenceWrite struct {
	Request         workspace.ToolRequest
	Raw             string
	EvidencePath    string
	RequiresHistory bool
}

func newImplementationDeferredSourceEvidenceWrite(req workspace.ToolRequest, raw string, evidencePath string) *implementationDeferredSourceEvidenceWrite {
	target := normalizeRunnerPath(evidencePath)
	if target == "" {
		paths := writeRequestPaths(req)
		if len(paths) == 1 {
			target = normalizeRunnerPath(paths[0])
		}
	}
	if target == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return nil
	}
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(req)
	}
	return &implementationDeferredSourceEvidenceWrite{
		Request:      req,
		Raw:          raw,
		EvidencePath: target,
	}
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterEvidence(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (workspace.ToolRequest, string, bool) {
	if d == nil || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, d.RequiresHistory) {
		return workspace.ToolRequest{}, "", false
	}
	if d.RequiresHistory && !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}
	if !d.replayAllowedForRequest(req) {
		return workspace.ToolRequest{}, "", false
	}
	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}
	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAfterHistoryEvidence(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if d == nil || !d.RequiresHistory || strings.TrimSpace(d.EvidencePath) == "" {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationTargetSourceWriteTargetsHaveEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationDeferredWriteCoveredBySourceEvidence(d.Request, d.EvidencePath, results, true) {
		return workspace.ToolRequest{}, "", false
	}
	if !implementationDeferredWriteHasHistoryEvidence(d.Request, results) {
		return workspace.ToolRequest{}, "", false
	}
	raw := d.Raw
	if strings.TrimSpace(raw) == "" {
		raw = marshalToolRequestRaw(d.Request)
	}
	return d.Request, raw, true
}

func (d *implementationDeferredSourceEvidenceWrite) fallbackVerificationRequest() (workspace.ToolRequest, bool) {
	if d == nil {
		return workspace.ToolRequest{}, false
	}
	paths := writeRequestPaths(d.Request)
	if len(paths) != 1 {
		return workspace.ToolRequest{}, false
	}
	path := normalizeRunnerPath(paths[0])
	if path == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool: "source_sanity",
		Path: path,
	}, true
}

func (d *implementationDeferredSourceEvidenceWrite) replayAllowedForRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || workspace.IsWorkspaceWriteTool(tool) {
		return false
	}
	if d.RequiresHistory && tool == "shell" {
		return true
	}
	switch tool {
	case "read_file", "code_navigate", "read_only_capability", "source_sanity", "history_range", "history_symbol":
	default:
		return false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		target = normalizeRunnerPath(req.Path)
	}
	return target == d.EvidencePath
}

func implementationDeferredWriteHasHistoryEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !successfulGitHistoryResultForPath(path, results) {
			return false
		}
	}
	return true
}

func implementationDeferredWriteCoveredBySourceEvidence(req workspace.ToolRequest, path string, results []workspace.ToolResult, allowAnchorMismatch bool) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "edit_file":
		oldText := strings.TrimSpace(req.OldText)
		if oldText == "" {
			return false
		}
		if allowAnchorMismatch {
			return concreteSourceEvidenceReadForPath(path, results)
		}
		return implementationSourceEvidenceTextContains(path, results, oldText)
	case "write_file":
		return fullSourceReadFileSeen(path, results)
	case "append_file":
		return fullSourceReadFileSeen(path, results)
	default:
		return false
	}
}

func implementationSourceEvidenceTextContains(path string, results []workspace.ToolResult, want string) bool {
	path = normalizeRunnerPath(path)
	want = strings.TrimSpace(want)
	if path == "" || want == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !concreteSourceEvidenceResultMatchesPath(result, path) {
			continue
		}
		if strings.Contains(strings.Join([]string{result.Summary, result.Output}, "\n"), want) {
			return true
		}
	}
	return false
}

func planThenExecuteCandidateSourceReadyRejectionSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if planThenExecuteCandidateSourceReadyResult(result) && strings.TrimSpace(result.Error) != "" {
			return true
		}
	}
	return false
}

func planThenExecuteCandidateSourceReadyResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Code) == planThenExecuteResultCandidateSourceReady {
		return true
	}
	// Compatibility for already-recorded logs and older seeded tests while
	// new behavior writes and reads the typed result code above.
	return strings.TrimSpace(result.Code) == "" &&
		strings.TrimSpace(result.Summary) == "Candidate source already ready"
}

func currentToolSurfaceRejection(req workspace.ToolRequest, allowedTools []string) (string, string, bool) {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || runnerAllowsTool(allowedTools, tool) {
		return "", "", false
	}
	if len(allowedTools) == 0 {
		return "Tool not available in current runtime contract", fmt.Sprintf("%s is not available because the current runtime contract exposes no tools. Answer from gathered evidence or report a concrete blocker.", tool), true
	}
	return "Tool not available in current runtime contract", fmt.Sprintf("%s is not in the current allowed tool surface. Available tools: %s. Use an available tool, answer from gathered evidence, or report a concrete blocker.", tool, strings.Join(allowedTools, ", ")), true
}

func (r *Runner) implementationTargetSourceWriteBeforeEvidenceRejection(req workspace.ToolRequest, results []workspace.ToolResult, currentAllowedTools []string) (string, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          results,
		BaseAllowedTools: runnerCurrentAllowedOrBriefTools(currentAllowedTools, r.RuntimeBrief.AllowedTools),
	})
	if contract.PhaseID != turnPhaseImplementationTargetSource && contract.Phase != "target_write_ready" {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) == "write_file" && strings.TrimSpace(req.Content) != "" && r.writeRequestTargetsAllMissing(req) {
		return "", "", false
	}
	if implementationTargetSourceWriteTargetsHaveEvidence(req, results) {
		return "", "", false
	}
	return "Write rejected before source-body evidence",
		"implementation.target_source requires target localization and source-body evidence before changing existing source. Use a bounded read/discovery tool with named_gap for the missing target or edit-safety fact, use write_file without prior source-body evidence only for a genuinely new file, or answer <<QUESTION>> with the exact blocker/no-op evidence.",
		true
}

func (r *Runner) writeRequestTargetsAllMissing(req workspace.ToolRequest) bool {
	if r == nil || r.Workspace == nil {
		return false
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		clean := cleanRelativeRunnerPath(path)
		if clean == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(clean)))
		if err == nil || !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func implementationTargetSourceWriteTargetsHaveEvidence(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		path = normalizeRunnerPath(path)
		if path == "" || !concreteSourceEvidenceReadForPath(path, results) {
			return false
		}
	}
	return true
}

func runnerHasWorkspaceWriteCapability(brief workerruntime.WorkerRuntimeBrief, tools []string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "write") {
		return true
	}
	for _, tool := range tools {
		if workspace.IsWorkspaceWriteTool(tool) {
			return true
		}
	}
	return false
}

func runnerVerificationRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.ToLower(strings.TrimSpace(brief.VerificationContract))
	return contract != "" && !strings.Contains(contract, "not_required")
}

func defaultRunnerStateSlice(values []string, defaults []string) []string {
	if len(values) > 0 {
		return values
	}
	return append([]string(nil), defaults...)
}

func appendRunnerTools(values []string, tools ...string) []string {
	for _, tool := range tools {
		values = util.AppendUnique(values, tool)
	}
	return values
}

func filterOutRunnerTools(values []string, disallowed ...string) []string {
	if len(values) == 0 || len(disallowed) == 0 {
		return values
	}
	blocked := make(map[string]struct{}, len(disallowed))
	for _, tool := range disallowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			blocked[tool] = struct{}{}
		}
	}
	filtered := values[:0]
	for _, tool := range values {
		if _, skip := blocked[strings.TrimSpace(tool)]; skip {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func childPacketContainsGroundedAnswerEvidence(objective string, output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	if readOnlyToolControlGuidanceText(lower) {
		return false
	}
	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}
	if !containsAny(lower,
		"grounded answer",
		"core files:",
		"primary files:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"important flow:",
		"key findings:",
		"\n- read ",
		"\n- found ",
		"\n- located ",
		"\n- calls ",
		"\n- uses ",
		"\n- references ",
	) {
		return false
	}
	if strings.TrimSpace(objective) == "" {
		return true
	}
	return delegatedPacketAddressesObjective(objective, output)
}

func delegatedSourceBodyPacketHasGroundedFinding(output string) bool {
	if len(sourceBodyEvidencePathsFromDelegatedPacket(output)) == 0 {
		return false
	}
	return delegatedTaskOutputHasFindingEvidence(output)
}

func delegatedPacketAddressesObjective(objective string, output string) bool {
	objective = strings.TrimSpace(objective)
	output = strings.TrimSpace(output)
	if objective == "" || output == "" {
		return false
	}
	body := substantiveAnswerWithoutEchoedObjective(stripDelegatedReadOnlyInternalPacket(output), objective)
	if strings.TrimSpace(body) == "" {
		return false
	}
	objectiveText := answerTextWithoutPathCitations(body)
	if readOnlyObjectiveAnswerEvidenceSupported(objectiveText, objective) {
		return true
	}
	if delegatedPacketNamesObjectiveSource(objective, output) &&
		readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(objectiveText, objective) {
		return true
	}
	return false
}

func delegatedPacketNamesObjectiveSource(objective string, output string) bool {
	objective = strings.ToLower(strings.TrimSpace(objective))
	if objective == "" {
		return false
	}
	for _, path := range sourceBodyEvidencePathsFromDelegatedPacket(output) {
		normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if normalized == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(normalized))
		if strings.Contains(objective, normalized) || (base != "" && strings.Contains(objective, base)) {
			return true
		}
	}
	return false
}

func delegatedReadOnlySourceBodyEvidencePaths(results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)
	paths = append(paths, concreteSourceEvidencePaths(results)...)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			paths = append(paths, packet.SourceBodyPaths...)
			continue
		}
		paths = append(paths, sourceBodyEvidencePathsFromDelegatedPacket(result.Output)...)
	}
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath("", path) {
			continue
		}
		filtered = append(filtered, path)
	}
	return uniqueDelegatedDiscoveryPaths(filtered)
}

func delegatedReadOnlySourceBodyReadinessPaths(objective string, results []workspace.ToolResult) []string {
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
				paths = append(paths, packet.SourceBodyPaths...)
			}
			continue
		}
		sourcePaths := sourceBodyEvidencePathsFromDelegatedPacket(result.Output)
		if len(sourcePaths) == 0 && delegatedTaskOutputHasFindingEvidence(result.Output) {
			sourcePaths = groundedChildPacketEvidencePaths(objective, result.Output)
		}
		if len(sourcePaths) == 0 {
			continue
		}
		if childPacketContainsGroundedAnswerEvidence(objective, result.Output) || delegatedPacketAddressesObjective(objective, result.Output) {
			paths = append(paths, sourcePaths...)
		}
	}
	return readOnlySourceLikePaths(paths, 16)
}

func sourceBodyEvidencePathsFromDelegatedPacket(output string) []string {
	var paths []string
	inSection := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			if inSection {
				break
			}
			continue
		}
		lower := strings.ToLower(strings.TrimRight(line, ":"))
		switch lower {
		case "source-body evidence", "source-body evidence paths", "source body evidence", "source body evidence paths":
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "- ") {
			break
		}
		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)) {
			if isNonSourceEvidenceArtifactPath(path) {
				continue
			}
			paths = append(paths, path)
		}
	}
	return uniqueDelegatedDiscoveryPaths(paths)
}

func groundedChildPacketEvidencePaths(objective string, output string) []string {
	rawPaths := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(output))
	if len(rawPaths) == 0 {
		return nil
	}
	paths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath(objective, path) {
			continue
		}
		paths = append(paths, path)
	}
	return uniqueDelegatedDiscoveryPaths(paths)
}

func isGroundedChildPacketEvidencePath(objective string, path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(normalizeDelegatedDiscoveryEvidencePath(path))))
	if path == "" {
		return false
	}
	switch {
	case strings.HasPrefix(path, ".idiom/"),
		strings.HasPrefix(path, "idiom/"),
		strings.HasPrefix(path, "logs/"),
		strings.HasPrefix(path, "transcripts/"),
		strings.HasPrefix(path, "testdata/swe/fixtures/"),
		strings.Contains(path, "/testdata/swe/fixtures/"):
		return false
	}
	base := filepath.Base(path)
	if allowsDocumentationOnlyPacket(objective) && (strings.HasSuffix(path, ".md") || base == "readme.md") {
		return true
	}
	switch base {
	case "go.mod", "go.sum", "go.work", "makefile", "package.json", "cargo.toml", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	}
	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".rs"),
		strings.HasSuffix(path, ".py"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".java"),
		strings.HasSuffix(path, ".c"),
		strings.HasSuffix(path, ".cc"),
		strings.HasSuffix(path, ".cpp"),
		strings.HasSuffix(path, ".cxx"),
		strings.HasSuffix(path, ".h"),
		strings.HasSuffix(path, ".hpp"),
		strings.HasSuffix(path, ".m"),
		strings.HasSuffix(path, ".mm"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".toml"),
		strings.HasSuffix(path, ".sh"),
		strings.HasSuffix(path, ".sql"),
		strings.HasSuffix(path, ".proto"):
		return true
	}
	return strings.HasPrefix(path, "internal/") ||
		strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "pkg/") ||
		strings.HasPrefix(path, "src/") ||
		strings.HasPrefix(path, "scripts/")
}

func allowsDocumentationOnlyPacket(objective string) bool {
	lower := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	return containsAny(lower, " readme ", " doc ", " docs ", " documentation ", " markdown ")
}

func annotateScopedFallbackAnswer(answer string, objective string) string {
	answer = strings.TrimSpace(answer)
	objective = strings.TrimSpace(objective)
	if objective == "" || answer == "" || !strings.HasPrefix(answer, "Partial findings from this turn") {
		return answer
	}
	return "Question in scope: " + objective + "\nSource: delegated read-only evidence packet.\n\n" + answer
}

func normalizeDelegatedReadOnlyPacket(answer string) string {
	answer = strings.TrimSpace(normalizeWorkspacePathScanText(answer))
	if answer == "" {
		return ""
	}
	answer = stableHandlePathLinePattern.ReplaceAllString(answer, "$1")
	answer = stableHandleInlinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathLinePattern.ReplaceAllString(answer, "$1")
	answer = delegatedPacketPathQuotePattern.ReplaceAllString(answer, "$1")
	return answer
}

func augmentDelegatedReadOnlyPacket(answer string, objective string, results []workspace.ToolResult) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	sourceBodyPaths := delegatedReadOnlySourceBodyReadinessPaths(objective, results)
	paths := delegatedClosureEvidencePaths(objective, results, 8)
	mentioned := normalizePostChildEvidencePaths(pathparse.MentionedWorkspacePaths(answer))
	missing := make([]string, 0, len(paths))
	mentionedSet := make(map[string]struct{}, len(mentioned))
	for _, path := range mentioned {
		mentionedSet[path] = struct{}{}
	}
	for _, path := range paths {
		if _, ok := mentionedSet[path]; ok {
			continue
		}
		missing = append(missing, path)
	}
	candidates := closureProbeCandidates(results, 4)
	missingCandidates := make([]string, 0, len(candidates))
	lowerAnswer := strings.ToLower(answer)
	for _, candidate := range candidates {
		if strings.Contains(lowerAnswer, strings.ToLower(candidate)) {
			continue
		}
		missingCandidates = append(missingCandidates, candidate)
	}
	if len(missing) == 0 && len(missingCandidates) == 0 {
		if len(sourceBodyPaths) == 0 || len(sourceBodyEvidencePathsFromDelegatedPacket(answer)) > 0 {
			return answer
		}
	}
	var b strings.Builder
	b.WriteString(answer)
	if len(sourceBodyPaths) > 0 && len(sourceBodyEvidencePathsFromDelegatedPacket(answer)) == 0 {
		b.WriteString("\n\nSource-body evidence paths:\n")
		for _, path := range limitRunnerStrings(sourceBodyPaths, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}
	if len(missing) > 0 {
		b.WriteString("\n\nAdditional inspected paths:\n")
		for _, path := range limitRunnerStrings(missing, 4) {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}
	if len(missingCandidates) > 0 {
		b.WriteString("\nExact probes already inspected:\n")
		for _, candidate := range limitRunnerStrings(missingCandidates, 3) {
			b.WriteString("- `")
			b.WriteString(candidate)
			b.WriteString("`\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func synthesizeDelegatedReadOnlyAnswer(objective string, results []workspace.ToolResult) (string, bool) {
	objective = strings.TrimSpace(objective)
	useful := make([]workspace.ToolResult, 0, len(results))
	answerabilityResults := make([]workspace.ToolResult, 0, len(results))
	sanitizedResults := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		result = normalizeFallbackResult(result)
		answerabilityResults = append(answerabilityResults, result)
		if strings.TrimSpace(result.Tool) == "task" {
			if packet, ok := readOnlyEvidencePacketFromResult(result); ok && !readOnlyEvidencePacketAnswerableForObjective(packet, objective) {
				continue
			}
		}
		sanitizedResult := result
		sanitizedResult.Output = sanitizeDelegatedReadOnlyResultOutput(result)
		sanitizedResults = append(sanitizedResults, sanitizedResult)
		if strings.TrimSpace(sanitizedResult.Error) != "" || strings.TrimSpace(sanitizedResult.Output) == "" || !isResultUsefulForFallback(sanitizedResult) {
			continue
		}
		useful = append(useful, sanitizedResult)
	}
	if answer, ok := synthesizeReadOnlyEvidencePacketAnswer(objective, readOnlyEvidencePacketsFromResults(answerabilityResults)); ok {
		return answer, true
	}
	answerability := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         objective,
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}, answerabilityResults)
	sourceBodyPaths := answerability.RelevantSourceBodyPaths
	if len(sourceBodyPaths) == 0 {
		return "", false
	}
	paths := append([]string{}, sourceBodyPaths...)
	paths = append(paths, answerability.CandidatePaths...)
	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(sanitizedResults)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(sanitizedResults, 8)...)
	paths = append(paths, pinnedPathsFromToolResults(sanitizedResults, 8)...)
	paths = prioritizedClosureEvidencePaths(paths, sanitizedResults, 8)
	paths = closurePromptEvidencePaths(paths, 8)
	if len(paths) == 0 {
		return "", false
	}
	if answer, ok := synthesizeDelegatedDiscoveryAnswer(objective, sanitizedResults, paths); ok {
		if !looksLikeNonAnswerProse(answer) {
			return answer, true
		}
	}
	if answer, ok := synthesizeDelegatedSourceBodyFactsAnswer(objective, sanitizedResults, sourceBodyPaths, paths); ok {
		return answer, true
	}

	lines := delegatedReadOnlyFindingLines(useful, sourceBodyPaths, 4)
	if len(lines) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("The answer is grounded in ")
	b.WriteString(formatInlinePathList(paths, 4))
	b.WriteString(".")
	if question := delegatedDisplayQuestion(objective); question != "" {
		b.WriteString(" Use those findings as the source of truth for `")
		b.WriteString(question)
		b.WriteString("`.")
	}
	b.WriteString("\n\nWhat this establishes:\n")
	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), true
}

func synthesizeDelegatedSourceBodyFactsAnswer(objective string, results []workspace.ToolResult, sourceBodyPaths []string, evidencePaths []string) (string, bool) {
	sourceBodySet := readOnlyPathSet(sourceBodyPaths)
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Tool) != "read_file" {
			continue
		}
		paths := readPathsFromToolResult(result)
		if len(paths) == 0 {
			continue
		}
		path := normalizeRunnerPath(paths[0])
		if _, ok := sourceBodySet[path]; !ok {
			continue
		}
		factAnswer, ok := presentableSourceBodyFactsAnswer(objective, path, result.Output)
		if !ok || looksLikeNonAnswerProse(factAnswer) {
			continue
		}
		factText := stripPresentableSourceObjectiveEcho(factAnswer)
		if looksLikeLowValueSourceInventoryFallback(factText) {
			continue
		}
		if strings.TrimSpace(objective) != "" && !delegatedSourceFactAddressesObjective(factText, objective) {
			continue
		}
		var b strings.Builder
		b.WriteString("The answer is grounded in ")
		b.WriteString(formatInlinePathList(evidencePaths, 4))
		b.WriteString(".")
		if question := delegatedDisplayQuestion(objective); question != "" {
			b.WriteString(" Use those findings as the source of truth for `")
			b.WriteString(question)
			b.WriteString("`.")
		}
		b.WriteString("\n\nWhat this establishes:\n")
		b.WriteString("- ")
		b.WriteString(factText)
		b.WriteString("\n")
		return strings.TrimSpace(b.String()), true
	}
	return "", false
}

func delegatedSourceFactAddressesObjective(factText string, objective string) bool {
	factText = strings.ToLower(strings.TrimSpace(factText))
	if factText == "" {
		return false
	}
	for _, term := range recoveryObjectiveSearchWords(objective, 32) {
		term = singularObjectiveWord(strings.ToLower(strings.TrimSpace(term)))
		if len(term) < 4 || delegatedSourceFactGenericObjectiveTerm(term) {
			continue
		}
		if strings.Contains(factText, term) {
			return true
		}
	}
	return false
}

func delegatedSourceFactGenericObjectiveTerm(term string) bool {
	switch term {
	case "internal", "agent", "runner", "source", "evidence", "inspect", "inspected", "path", "paths", "report", "file", "code", "explain", "harness":
		return true
	default:
		return false
	}
}

func sanitizeDelegatedReadOnlyResultOutput(result workspace.ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return ""
	}
	if strings.TrimSpace(result.Tool) != "task" {
		return output
	}
	return stripDelegatedReadOnlyInternalPacket(output)
}

func stripDelegatedReadOnlyInternalPacket(output string) string {
	var kept []string
	skipList := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			skipList = false
			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "question:"),
			strings.HasPrefix(lower, "source:"),
			strings.HasPrefix(lower, "confidence:"):
			skipList = false
			continue
		case lower == "inspected paths:" ||
			lower == "additional inspected paths:" ||
			lower == "exact probes already inspected:":
			skipList = true
			continue
		case lower == "key findings:":
			skipList = false
			continue
		case skipList && strings.HasPrefix(line, "- "):
			continue
		case strings.HasPrefix(line, "<<QUESTION>>:"),
			strings.Contains(lower, "completion gate rejected"),
			strings.Contains(lower, "missing completion evidence"),
			strings.Contains(lower, "missing: inspected_paths"),
			strings.Contains(lower, "final answer looks like transport-level noise"):
			skipList = false
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(collapseBlankRunnerLines(kept), "\n"))
}

func collapseBlankRunnerLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func delegatedReadOnlyFindingLines(results []workspace.ToolResult, supportedPaths []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	supported := readOnlyPathSet(supportedPaths)
	var lines []string
	seen := map[string]struct{}{}
	for _, result := range results {
		output := compactOutput(result.Output, fallbackLineBudgetForTool(result.Tool))
		for _, raw := range strings.Split(output, "\n") {
			line := cleanDelegatedReadOnlyFindingLine(strings.TrimSpace(strings.TrimPrefix(raw, "- ")))
			if !delegatedReadOnlyFindingLineIsUseful(line) {
				continue
			}
			if !delegatedReadOnlyFindingLineSupportedByPaths(line, supported) {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			lines = append(lines, line)
			if len(lines) >= limit {
				return lines
			}
		}
	}
	return lines
}

func cleanDelegatedReadOnlyFindingLine(line string) string {
	line = strings.TrimSpace(line)
	for {
		lower := strings.ToLower(line)
		stripped := false
		for _, label := range delegatedReadOnlyPacketLineLabels() {
			if !strings.HasPrefix(lower, label) {
				continue
			}
			line = strings.TrimSpace(line[len(label):])
			stripped = true
			break
		}
		if !stripped {
			return line
		}
	}
}

func delegatedReadOnlyPacketLineLabels() []string {
	return []string{
		"source-body evidence paths:",
		"source body evidence paths:",
		"source body paths:",
		"grounded findings:",
		"grounded answer:",
		"key findings:",
		"verified context:",
		"inspected paths:",
		"evidence used:",
		"core files:",
		"primary files:",
		"important flow:",
	}
}

func delegatedReadOnlyFindingLineSupportedByPaths(line string, supported map[string]struct{}) bool {
	mentionedAny := false
	supportedAny := false
	for _, path := range readOnlySourceLikePaths(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)), 16) {
		mentionedAny = true
		if _, ok := supported[path]; ok {
			supportedAny = true
		}
	}
	return !mentionedAny || supportedAny
}

func delegatedReadOnlyFindingLineIsUseful(line string) bool {
	if _, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return false
	}
	if line == "" ||
		delegatedDiscoveryLineIsBarePath(line) ||
		looksLikeLineOrientedSourceSnippetLine(line) ||
		sourceEvidenceLineIsBoilerplate(stripSourceEvidenceLinePrefix(line)) {
		return false
	}
	lower := strings.ToLower(line)
	if readOnlyToolControlGuidanceText(lower) ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "completion gate") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "child task completed") ||
		strings.Contains(lower, "the retained source context identifies") ||
		strings.Contains(lower, "enough for a bounded answer") ||
		strings.Contains(lower, "safely expand beyond the inspected anchors") ||
		strings.Contains(lower, "the strongest implementation evidence is in") ||
		strings.Contains(lower, "use those findings as the source of truth") ||
		lower == "context:" ||
		lower == "context" ||
		strings.Contains(lower, "top-level declaration") ||
		strings.HasPrefix(lower, "source-body evidence paths:") ||
		strings.HasPrefix(lower, "source-body evidence:") ||
		strings.HasPrefix(lower, "source body evidence paths:") ||
		strings.HasPrefix(lower, "source body evidence:") ||
		strings.HasPrefix(lower, "what this establishes:") ||
		strings.HasPrefix(lower, "outlined ") ||
		strings.HasPrefix(lower, "source:") ||
		strings.HasPrefix(lower, "question:") ||
		strings.HasPrefix(lower, "confidence:") {
		return false
	}
	return strings.Contains(line, ":") || strings.Contains(line, "`") || strings.Contains(line, ".")
}

func readOnlyToolControlGuidanceText(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"generated evidence artifact",
		"generated discovery/context json is not source-body evidence",
		"not source-body evidence",
		"use the source paths already named by the current discovery packet",
		"next exact source probe example",
		"after one source-body read resolves the claim",
		"redirected to ",
		"try: {\"tool\":",
		"retry your original request and it will execute",
	)
}

type delegatedDiscoverySummary struct {
	Purpose      string `json:"purpose"`
	ArtifactPath string `json:"artifact_path"`
	Evidence     []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Gaps []string `json:"gaps,omitempty"`
}

func synthesizeDelegatedDiscoveryAnswer(objective string, results []workspace.ToolResult, paths []string) (string, bool) {
	summaries := delegatedDiscoverySummaries(results)
	question := delegatedDisplayQuestion(objective)
	evidencePaths := delegatedDiscoveryAnswerEvidencePaths(question, paths)
	return AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      question,
		EvidencePaths: evidencePaths,
		Summaries:     summaries,
	})
}

func delegatedDiscoverySummaries(results []workspace.ToolResult) []delegatedDiscoverySummary {
	var summaries []delegatedDiscoverySummary
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "discovery_query" || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		var summary delegatedDiscoverySummary
		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}
		if summary.ArtifactPath == "" && len(summary.Evidence) == 0 {
			continue
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func delegatedDisplayQuestion(objective string) string {
	for _, line := range strings.Split(strings.TrimSpace(objective), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return inspected paths") ||
			strings.HasPrefix(lower, "- ") {
			continue
		}
		return line
	}
	return strings.TrimSpace(objective)
}

func delegatedDiscoveryAnswerEvidencePaths(objective string, paths []string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}
	return prioritizedDelegatedEvidencePaths(paths, len(paths))
}

func uniqueDelegatedDiscoveryPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	fullByBase := make(map[string]string)
	ambiguousBase := make(map[string]bool)
	dirCounts := make(map[string]int)
	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if path == "" {
			continue
		}
		normalized = append(normalized, path)
		if strings.Contains(path, "/") {
			base := filepath.Base(path)
			dirCounts[filepath.Dir(path)]++
			if prior, ok := fullByBase[base]; ok && prior != path {
				ambiguousBase[base] = true
				continue
			}
			fullByBase[base] = path
		}
	}
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(normalized))
	dominantDir := ""
	dominantCount := 0
	for dir, count := range dirCounts {
		if count > dominantCount || (count == dominantCount && (dominantDir == "" || dir < dominantDir)) {
			dominantDir = dir
			dominantCount = count
		}
	}
	for _, path := range normalized {
		if !strings.Contains(path, "/") {
			base := filepath.Base(path)
			if full, ok := fullByBase[base]; ok && !ambiguousBase[base] {
				path = full
			} else if dominantCount >= 2 {
				path = filepath.ToSlash(filepath.Join(dominantDir, base))
			}
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	return unique
}

func prioritizedDelegatedEvidencePaths(paths []string, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)
	if len(paths) <= 1 {
		return paths
	}
	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])
		return pi < pj
	})
	return limitRunnerStrings(paths, max)
}

func prioritizedClosureEvidencePaths(paths []string, results []workspace.ToolResult, max int) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	paths = preferNonTestEvidencePaths(paths)
	if len(paths) <= 1 {
		return paths
	}
	sharedCounts, candidateCounts := closureEvidenceProbeScores(results)
	sort.SliceStable(paths, func(i, j int) bool {
		pi := delegatedDiscoveryPathPriority(paths[i])
		pj := delegatedDiscoveryPathPriority(paths[j])
		if pi != pj {
			return pi < pj
		}
		ni := normalizeDelegatedDiscoveryEvidencePath(paths[i])
		nj := normalizeDelegatedDiscoveryEvidencePath(paths[j])
		if sharedCounts[ni] != sharedCounts[nj] {
			return sharedCounts[ni] > sharedCounts[nj]
		}
		if candidateCounts[ni] != candidateCounts[nj] {
			return candidateCounts[ni] > candidateCounts[nj]
		}
		return false
	})
	return limitRunnerStrings(paths, max)
}

func delegatedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	var lines []string
	for _, summary := range summaries {
		for _, evidence := range summary.Evidence {
			name := strings.TrimSpace(evidence.Name)
			if name == "" || isDelegatedDiscoveryContextDigestLayer(name) || name == "target_listing" {
				continue
			}
			text := strings.TrimSpace(evidence.Summary)
			if text == "" || strings.TrimSpace(evidence.Error) != "" {
				continue
			}
			if !delegatedDiscoveryFindingSummaryIsUseful(name, text) {
				continue
			}
			lines = append(lines, name+": "+text)
			if limit > 0 && len(lines) >= limit {
				return lines
			}
		}
	}
	return lines
}

func delegatedDiscoveryFindingSummaryIsUseful(name string, text string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "context pack wrote") ||
		strings.Contains(lower, "no related go files matched") ||
		strings.Contains(lower, "no go symbols matched") ||
		strings.Contains(lower, "no symbols matched") ||
		strings.Contains(lower, "no matching symbol") ||
		strings.Contains(lower, "semantic fallback after gopls failure") ||
		strings.Contains(lower, "collected go dependency graph") {
		return false
	}
	if lowerName == "dependency_graph" && strings.HasPrefix(lower, "collected ") {
		return false
	}
	if lowerName == "text_anchor_search" && delegatedDiscoverySummaryIsBroadCountOnly(lower) {
		return false
	}
	return true
}

func delegatedDiscoverySummaryIsBroadCountOnly(lower string) bool {
	if !strings.Contains(lower, "matching line") {
		return false
	}
	if strings.Contains(lower, "implementation path") ||
		strings.Contains(lower, "implementation anchor") ||
		strings.Contains(lower, "source anchor") {
		return false
	}
	return true
}

func delegatedDiscoveryLineIsBarePath(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}
	path := normalizeDelegatedDiscoveryEvidencePath(line)
	return path != "" && delegatedDiscoveryLooksLikeEvidencePath(path)
}

func limitRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	return values[:max]
}

func closurePromptEvidencePaths(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range preferNonTestEvidencePaths(uniqueDelegatedDiscoveryPaths(paths)) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}
		cleaned = append(cleaned, path)
	}
	return limitRunnerStrings(cleaned, max)
}

func closureEvidenceProbeScores(results []workspace.ToolResult) (map[string]int, map[string]int) {
	byPath := closureProbeCandidatesByPath(results, 6)
	probeFrequency := make(map[string]int)
	for _, candidates := range byPath {
		seen := make(map[string]struct{})
		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			probeFrequency[key]++
		}
	}
	sharedCounts := make(map[string]int, len(byPath))
	candidateCounts := make(map[string]int, len(byPath))
	for rawPath, candidates := range byPath {
		path := normalizeDelegatedDiscoveryEvidencePath(rawPath)
		if path == "" {
			continue
		}
		seen := make(map[string]struct{})
		for _, candidate := range candidates {
			key := strings.ToLower(strings.TrimSpace(candidate))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidateCounts[path]++
			if probeFrequency[key] > 1 {
				sharedCounts[path] += probeFrequency[key]
			}
		}
	}
	return sharedCounts, candidateCounts
}

func delegatedDiscoveryQueryEvidencePaths(results []workspace.ToolResult, limit int) []string {
	if limit <= 0 {
		limit = 4
	}
	type discoveryPathCandidate struct {
		path  string
		score int
	}
	best := make(map[string]int)
	addCandidate := func(path string, layerName string, line string) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
			return
		}
		if !delegatedDiscoveryLooksLikeEvidencePath(path) {
			return
		}
		if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
			return
		}
		score := delegatedDiscoveryLayerPriority(layerName)*100 + delegatedDiscoveryEvidenceLinePriority(line)*10 + delegatedDiscoveryPathPriority(path)
		if prior, ok := best[path]; ok && prior <= score {
			return
		}
		best[path] = score
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "discovery_query" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		var summary struct {
			Evidence []struct {
				Name          string `json:"name"`
				Summary       string `json:"summary"`
				OutputExcerpt string `json:"output_excerpt"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
			continue
		}
		for _, evidence := range summary.Evidence {
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				for _, path := range pathparse.MentionedWorkspacePaths(line) {
					addCandidate(path, evidence.Name, line)
				}
				for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
					addCandidate(match, evidence.Name, line)
				}
			}
		}
	}
	candidates := make([]discoveryPathCandidate, 0, len(best))
	for path, score := range best {
		candidates = append(candidates, discoveryPathCandidate{path: path, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].path < candidates[j].path
	})
	paths := make([]string, 0, limit)
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
		if len(paths) >= limit {
			return preferNonTestEvidencePaths(paths)
		}
	}
	return preferNonTestEvidencePaths(paths)
}

func delegatedDiscoveryEvidenceLinePriority(line string) int {
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	switch {
	case line == "":
		return 5
	case strings.Contains(lower, "package "):
		return 5
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 0
	case strings.Contains(lower, "func "):
		return 1
	case strings.Contains(lower, "type "):
		return 2
	default:
		return 3
	}
}

func normalizeDelegatedDiscoveryEvidencePath(path string) string {
	path = normalizeWorkspacePathScanText(path)
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"()[]{}<>,.;")
	if path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	for _, ext := range []string{".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt"} {
		if idx := strings.Index(lower, ext); idx >= 0 {
			end := idx + len(ext)
			suffix := path[end:]
			switch {
			case suffix == "":
				return path[:end]
			case strings.HasPrefix(suffix, ":"):
				return path[:end]
			case strings.Trim(suffix, "`'\"") == "":
				return path[:end]
			}
		}
	}
	return path
}

func normalizeWorkspacePathScanText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(text)
}

func preferNonTestEvidencePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	hasNonTestImplementation := false
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			hasNonTestImplementation = true
			break
		}
	}
	if !hasNonTestImplementation {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		filtered = append(filtered, raw)
	}
	if len(filtered) == 0 {
		return paths
	}
	return filtered
}

func delegatedDiscoveryLooksLikeEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	switch {
	case strings.HasPrefix(path, "search_content/"),
		strings.HasPrefix(path, "search_files/"),
		strings.HasPrefix(path, "read/"),
		strings.HasPrefix(path, "list_files/"):
		return false
	case pathparse.HasWorkspaceFileExtension(path):
		return true
	default:
		return false
	}
}

func delegatedDiscoveryLayerPriority(name string) int {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return 0
	case "go_read_symbol", "cpp_read_symbol", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		return 1
	case "go_references", "cpp_references", "python_references", "ts_references", "rust_references", "java_references":
		return 2
	case "go_definition", "cpp_definition", "python_definition", "ts_definition", "rust_definition", "java_definition":
		return 3
	case "go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return 4
	case "related_files":
		return 5
	case "dependency_graph":
		return 6
	case "ctags_symbols":
		return 7
	case "target_listing":
		return 20
	case "project_behavior_digest", "project_context_digest":
		return 30
	default:
		return 10
	}
}

func isDelegatedDiscoveryContextDigestLayer(name string) bool {
	switch strings.TrimSpace(name) {
	case "project_behavior_digest", "project_context_digest":
		return true
	default:
		return false
	}
}

func delegatedDiscoveryPathPriority(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 0
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 1
	case strings.HasPrefix(path, "internal/"):
		return 2
	case strings.HasSuffix(path, "_test.go"):
		return 3
	default:
		return 4
	}
}

func (r *Runner) classifyTerminalAnswer(answer string, stopReason string, results []workspace.ToolResult) terminalAnswerDecision {
	stopReason = strings.TrimSpace(stopReason)
	if stopReason == "" {
		stopReason = "completed"
	}
	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	decision := terminalAnswerDecision{
		Outcome:         terminalAnswerAccepted,
		RawAnswer:       answer,
		PresentedAnswer: answer,
		StopReason:      stopReason,
		Acceptance:      acceptance,
	}
	if terminalAnswerContainsTransportProtocol(answer) {
		acceptance.Decision = finalAnswerAcceptanceRejected
		acceptance.BlockerKind = "transport_protocol_output"
		acceptance.Reason = "the final answer contained tool-call transport markup instead of user-visible answer prose"
		decision.Outcome = terminalAnswerTransportBlocked
		decision.PresentedAnswer = "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?"
		decision.StopReason = acceptance.BlockerKind
		decision.Acceptance = acceptance
		return decision
	}
	if r.hostOwnedReadOnlyCloseoutAccepted(answer, stopReason, results, acceptance) {
		acceptance.Decision = finalAnswerAcceptanceAccepted
		acceptance.BlockerKind = ""
		acceptance.Reason = "host_read_only_closeout"
		decision.Outcome = terminalAnswerAccepted
		decision.StopReason = stopReason
		decision.Acceptance = acceptance
		return decision
	}
	if acceptance.BlockerKind == "" {
		return decision
	}
	decision.Outcome = terminalAnswerBlocked
	decision.StopReason = acceptance.BlockerKind
	if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") && r.terminalAnswerShouldOverrideRejectedRaw(answer, acceptance) {
		blockerResult := workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Final answer rejected by blocker check",
			Error:   r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, answer, results),
		}
		decision.BlockerResult = blockerResult
		decision.PresentedAnswer = r.completionGateRepeatedBlockerAnswer(blockerResult, results)
		decision.OverrideSource = "finish"
	}
	return decision
}

func (r *Runner) hostOwnedReadOnlyCloseoutAccepted(answer string, stopReason string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) bool {
	if r == nil || strings.TrimSpace(stopReason) != "host_read_only_closeout" || acceptance.BlockerKind == "" {
		return false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !readOnlyDirectAnswerLane(brief) || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	return readOnlyClosureDecisionForResults(brief, results).AcceptsHostOwnedAnswer(r.agentName(), answer)
}

func terminalBlockedStatusShouldEmit(alreadyEmitted bool, lastStatus string, lastBlockerKind string, terminalBlockerKind string) bool {
	return !alreadyEmitted || strings.TrimSpace(lastStatus) != "blocked"
}

func (r *Runner) terminalAnswerShouldOverrideRejectedRaw(answer string, acceptance finalAnswerAcceptance) bool {
	if acceptance.BlockerKind == "" {
		return false
	}
	if r != nil && strings.HasPrefix(r.agentName(), "child:") && !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}
	return acceptance.BlockerKind != "non_answer_prose" ||
		looksLikeMetaIntentRestatement(answer) ||
		finishShouldOverrideSourceEcho(answer)
}

func terminalAnswerContainsTransportProtocol(answer string) bool {
	return looksLikeTextualToolCall(answer) ||
		strings.Contains(answer, "<n_tool") ||
		strings.Contains(answer, "<n_invoke") ||
		strings.Contains(answer, "<janus_tool") ||
		strings.Contains(answer, "<invoke name=") ||
		strings.Contains(answer, "DSML")
}

func (r *Runner) decorateTerminalAnswerDecision(decision terminalAnswerDecision, results []workspace.ToolResult) string {
	if decision.Outcome == terminalAnswerTransportBlocked {
		return decision.PresentedAnswer
	}
	return r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     decision.PresentedAnswer,
		StopReason: decision.StopReason,
		Brief:      r.RuntimeBrief,
		Results:    results,
		UserFacing: r.userFacingFinalAnswer(),
	})
}

func (r *Runner) emitAcceptedAnswerSignals(answer string, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}
	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	r.emitFinalAnswerTelemetry(answer, results, acceptance)
	if acceptance.BlockerKind != "" {
		r.emitCompletionStatus("blocked", acceptance.Reason, acceptance.BlockerKind, results, true)
	}
}

func (r *Runner) emitFinalAnswerTelemetry(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}
	lane := util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), strings.TrimSpace(r.RuntimeBrief.TaskFamily), r.agentName())
	contract := util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), string(workerruntime.ContractDirectAnswer))
	evidencePaths := acceptance.EvidencePaths
	var presentationEvidencePaths []string
	if readOnlyDirectAnswerLane(r.RuntimeBrief) && len(evidencePaths) == 0 {
		presentationEvidencePaths = readOnlyDirectAnswerPresentationEvidencePaths(r.RuntimeBrief, results, 8)
	}
	addPresentationEvidencePaths := func(payload map[string]any) {
		if len(presentationEvidencePaths) > 0 {
			payload["presentation_evidence_paths"] = presentationEvidencePaths
		}
	}
	acceptanceFields := finalAnswerAcceptanceEventFields(acceptance)
	if acceptance.BlockerKind != "" {
		readiness := map[string]any{
			"lane":           lane,
			"ready":          false,
			"confidence":     "blocked",
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(readiness)
		for key, value := range acceptanceFields {
			readiness[key] = value
		}
		r.emitEvent(marshalRunEvent("readiness_check", readiness))
		critic := map[string]any{
			"contract":       contract,
			"passed":         false,
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(critic)
		for key, value := range acceptanceFields {
			critic[key] = value
		}
		r.emitEvent(marshalRunEvent("final_answer_critic", critic))
		rails := map[string]any{
			"lane":           lane,
			"task_family":    util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
			"contract":       contract,
			"passed":         false,
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(rails)
		for key, value := range acceptanceFields {
			rails[key] = value
		}
		r.emitEvent(marshalRunEvent("control_rails_enforcement", rails))
		return
	}
	confidence := "medium"
	if len(evidencePaths) >= 2 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "partial findings from this turn") {
		// Conversation lanes have no semantic relevance evaluation; the
		// harness can verify answer form but not answer quality. Confidence
		// must not claim "high" when relevance is unevaluated.
		if conversationLaneWithoutSemanticEval(r.RuntimeBrief) {
			confidence = "medium"
		} else {
			confidence = "high"
		}
	}
	readiness := map[string]any{
		"lane":           lane,
		"ready":          true,
		"confidence":     confidence,
		"action":         "answer",
		"reason":         "answer satisfies lane contract and evidence gate",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(readiness)
	for key, value := range acceptanceFields {
		readiness[key] = value
	}
	r.emitEvent(marshalRunEvent("readiness_check", readiness))
	critic := map[string]any{
		"contract":       contract,
		"passed":         true,
		"action":         "accept",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(critic)
	for key, value := range acceptanceFields {
		critic[key] = value
	}
	r.emitEvent(marshalRunEvent("final_answer_critic", critic))
	rails := map[string]any{
		"lane":           lane,
		"task_family":    util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
		"contract":       contract,
		"passed":         true,
		"action":         "accept",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(rails)
	for key, value := range acceptanceFields {
		rails[key] = value
	}
	r.emitEvent(marshalRunEvent("control_rails_enforcement", rails))
}

func (r *Runner) emitFinalAnswerBlockerOverride(acceptance finalAnswerAcceptance, replacementStopReason string, source string) {
	if r == nil || r.OnEvent == nil || acceptance.BlockerKind == "" {
		return
	}
	payload := map[string]any{
		"summary":                      "model final answer rejected and replaced with deterministic blocker narrative",
		"source":                       strings.TrimSpace(source),
		"original_acceptance_contract": acceptance.SchemaVersion,
		"original_acceptance_decision": string(acceptance.Decision),
		"original_blocker_kind":        strings.TrimSpace(acceptance.BlockerKind),
		"original_reason":              strings.TrimSpace(acceptance.Reason),
		"original_evidence_paths":      acceptance.EvidencePaths,
		"replacement_stop_reason":      strings.TrimSpace(replacementStopReason),
		"raw_answer_logged":            false,
	}
	if acceptance.ObjectiveRelevanceStatus != "" {
		payload["objective_relevance"] = acceptance.ObjectiveRelevanceStatus
	}
	if len(acceptance.RelevantSourceBodyPaths) > 0 {
		payload["relevant_source_body_paths"] = cappedStrings(acceptance.RelevantSourceBodyPaths, 8)
	}
	r.emitEvent(marshalRunEvent("final_answer_blocker_override", payload))
}

func (r *Runner) emitPostWriteVerificationTelemetry(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(result.Error) != "" || !resultCountsAsSuccessfulVerification(result) {
		return false
	}
	command := postWriteVerificationTelemetryCommand(req, result)
	if command == "" {
		return false
	}
	confidence := postWriteVerificationTelemetryConfidence(command)
	plan := map[string]any{
		"commands":          []string{command},
		"uses_full_suite":   postWriteVerificationUsesFullSuite(command),
		"coverage":          postWriteVerificationTelemetryCoverage(command),
		"confidence":        confidence,
		"risk_level":        postWriteVerificationTelemetryRisk(command),
		"expected_failures": postWriteVerificationExpectedFailures(command),
		"repair_policy":     "repair failing checks before widening scope",
	}
	r.emitEvent(marshalRunEvent("verification_plan", plan))
	feedback := map[string]any{
		"commands":               []string{command},
		"failed_commands":        []string{},
		"confidence":             confidence,
		"coverage":               plan["coverage"],
		"risk_level":             plan["risk_level"],
		"learned_recommendation": postWriteVerificationRecommendation(command),
	}
	r.emitEvent(marshalRunEvent("verification_feedback", feedback))
	return true
}

func postWriteVerificationTelemetryCommand(req workspace.ToolRequest, result workspace.ToolResult) string {
	if strings.TrimSpace(req.Tool) == "shell" {
		if command := strings.TrimSpace(req.Command); command != "" {
			return command
		}
		if command := shellCommandFromToolResult(result); command != "" {
			return command
		}
	}
	if strings.TrimSpace(result.Tool) == "shell" {
		if command := shellCommandFromToolResult(result); command != "" {
			return command
		}
	}
	if strings.TrimSpace(req.Tool) == "source_sanity" && strings.TrimSpace(req.Path) != "" {
		return "source_sanity " + normalizeRunnerPath(req.Path)
	}
	if isDiagnosticsVerificationTool(req.Tool) {
		return strings.TrimSpace(req.Tool)
	}
	return ""
}

func postWriteVerificationUsesFullSuite(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "go test ./...") ||
		strings.Contains(lower, "make test") ||
		strings.Contains(lower, "npm test") ||
		strings.Contains(lower, "pnpm test") ||
		strings.Contains(lower, "cargo test")
}

func postWriteVerificationTelemetryCoverage(command string) string {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck"):
		return "targeted script syntax verification"
	case postWriteVerificationUsesFullSuite(command):
		return "full-suite verification"
	case strings.Contains(lower, "test") || strings.Contains(lower, "build") || strings.Contains(lower, "check"):
		return "targeted project verification"
	default:
		return "targeted verification"
	}
}

func postWriteVerificationTelemetryConfidence(command string) string {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck") {
		return "low"
	}
	if postWriteVerificationUsesFullSuite(command) {
		return "high"
	}
	return "medium"
}

func postWriteVerificationTelemetryRisk(command string) string {
	if postWriteVerificationUsesFullSuite(command) {
		return "high"
	}
	return "low"
}

func postWriteVerificationExpectedFailures(command string) []string {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck"):
		return []string{"shell syntax errors"}
	case strings.Contains(lower, "test"):
		return []string{"test failures"}
	case strings.Contains(lower, "build"):
		return []string{"build failures"}
	case strings.Contains(lower, "check"):
		return []string{"check failures"}
	default:
		return []string{"verification failures"}
	}
}

func postWriteVerificationRecommendation(command string) string {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck") {
		return "syntax verification passed; run targeted tests when behavior changes beyond script parsing"
	}
	if postWriteVerificationUsesFullSuite(command) {
		return "record full-suite success for similar high-risk surfaces"
	}
	return "keep targeted tests close to the changed surface before widening scope"
}

// requiredEditPathsProgressMessage returns a compact authoritative
// progress checklist that the runner injects into turnMessages after each
// successful write tool result on a brief that has required_edit_paths.
// The message gives the model running ground truth — paths actually
// changed (✓) and paths still pending (✗) — derived from
// changedPathsFromToolResults rather than the model's conversation
// memory. Returns empty string when no checklist is appropriate (no
// required paths in the brief, write was unsuccessful, no state change
// since the last write, etc.).
//
// Why we inject only after a successful write rather than every turn:
// per-turn injection accumulates stale checklists in the conversation;
// after-write injection is exactly when the running state changes and
// the model needs the truth most.
func (r *Runner) requiredEditPathsProgressMessage(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}
	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}
	required := r.RuntimeBrief.RequiredEditPaths
	if len(required) == 0 {
		return ""
	}
	if !workspaceWriteResultCompleted(req, result) {
		return ""
	}
	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return ""
	}
	var done []string
	var pending []string
	for _, raw := range required {
		path := normalizeRunnerPath(raw)
		if path == "" {
			continue
		}
		if _, ok := changed[path]; ok {
			done = append(done, raw)
		} else {
			pending = append(pending, raw)
		}
	}
	if len(done) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Required edit paths progress (authoritative — derived from successful write tool results, not from model claims):\n")
	for _, p := range done {
		b.WriteString("  ✓ ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	for _, p := range pending {
		b.WriteString("  ✗ ")
		b.WriteString(p)
		b.WriteString(" (pending)\n")
	}
	switch {
	case len(pending) == 0:
		b.WriteString("All required edit paths are written. Submit the standard final answer with the changed-paths summary now; do not call further tools beyond brief verification.")
	case len(pending) == 1:
		b.WriteString("One required edit path remains. The remaining-path write contract must target only that path, or report a concrete blocker for that path with <<QUESTION>>:.")
	default:
		b.WriteString(fmt.Sprintf("%d required edit paths remain. Continue with one remaining-path write contract per path; do not declare completion until each is written or report a per-path concrete blocker with <<QUESTION>>:.", len(pending)))
	}
	return b.String()
}

// finalAnswerBlockerErrorText returns the directional rejection message
// embedded in a synthesized completion_gate ToolResult when the runner
// overrides a fabricated final answer with a deterministic blocker
// narrative. It mirrors the existing mutation-completion gate text so the
// model gets the same shape of guidance it would have seen via a
// rejected-completion retry, plus an explicit note that its claimed
// changed-paths summary did not match reality.
func (r *Runner) finalAnswerBlockerErrorText(kind string, reason string, answer string, results []workspace.ToolResult) string {
	switch kind {
	case "required_edit_paths_remaining":
		missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))
		if r.planThenExecutePlannerPhase() {
			base := "the plan-execute planner contract is not fulfilled. The final answer claimed completion, but the harness has no successful submit_plan edit entry for the following required edit path(s): "
			if len(missing) > 0 {
				base += quotedRunnerStrings(missing)
			} else {
				base += "(see required_edit_paths in the brief)"
			}
			base += ". Submit a parser-readable submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, or report a concrete blocker per skipped path with <<QUESTION>>:."
			return base
		}
		base := "the implementation contract is not fulfilled. The final answer claimed completion, but the harness has no successful write tool result for the following required edit path(s): "
		if len(missing) > 0 {
			base += quotedRunnerStrings(missing)
		} else {
			base += "(see required_edit_paths in the brief)"
		}
		base += ". Satisfy the remaining-path write contract one path at a time, or report a concrete blocker per skipped path with <<QUESTION>>:."
		if unsupported := unsupportedClaimedChangedPaths(answer, results); len(unsupported) > 0 {
			base += " The previous answer also reported changed paths not backed by tool results: " + quotedRunnerStrings(unsupported) + " — do not include those in any future answer until they are actually written."
		}
		return base
	case "user_visible_blocker":
		return "the final answer was a <<QUESTION>>: blocker; surface the path-specific blocker reason for each unfinished required edit path."
	case "read_only_protocol_blocker":
		return "the final answer exposed internal <<QUESTION>> blocker protocol in a read-only direct-answer lane. Answer in normal prose from gathered evidence, or state the exact unresolved claim and the smallest useful next probe without raw blocker markup."
	case "unexecuted_tool_request_final_answer":
		return "the final answer was an unexecuted workspace-tool request payload. Issue the lookup as a parser-readable tool call with the `tool` field, or answer in normal prose from the available evidence."
	case "non_answer_prose":
		return "the final answer looks like transport-level noise (tool-call markup, echoed user question, or tool-failure language) rather than an actual answer. Reply with substantive prose that addresses the user's request, or use <<QUESTION>>: to state a concrete blocker."
	case "source_grounding_unmet":
		return reason
	case "answer_objective_unmet":
		return reason
	case "required_evidence_unmet":
		if r != nil {
			missing := missingRequiredEvidence(r.RuntimeBrief.RequiredEvidence, answer, results)
			return "the answer does not satisfy the required evidence contract. Missing: " + strings.Join(missing, ", ") + ". Address each missing obligation in your answer, or use <<QUESTION>>: to state a concrete blocker."
		}
		return "the answer does not satisfy the required evidence contract. Address each missing obligation, or use <<QUESTION>>: to state a concrete blocker."
	case "rejected_mutation_paths_remaining":
		if missing := unresolvedRejectedMutationPaths(results); len(missing) > 0 {
			return "the implementation contract is not fulfilled. The model attempted workspace write(s) that were rejected and not later satisfied by successful writes: " + quotedRunnerStrings(missing) + ". Make those intended edits, or report a concrete no-op/blocker with <<QUESTION>>:."
		}
		return "the implementation contract is not fulfilled. A rejected workspace write attempt remains unresolved; make the intended edit, or report a concrete no-op/blocker with <<QUESTION>>:."
	default:
		return "the final answer did not satisfy the active blocker check; continue with concrete writes or a per-path blocker reason."
	}
}

func (r *Runner) finalAnswerBlockerKind(answer string, results []workspace.ToolResult) (string, string) {
	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	return acceptance.BlockerKind, acceptance.Reason
}

func (r *Runner) decoratedReadOnlyReplyForCompletionGate(reply string, results []workspace.ToolResult) string {
	if r == nil || !r.userFacingFinalAnswer() || runnerBriefRequiresMutation(r.RuntimeBrief) {
		return reply
	}
	if looksLikeNonAnswerProse(reply) {
		return reply
	}
	if (!finalAnswerNeedsDecoration(reply) && !looksLikeGenericEvidenceComposerAnswer(reply)) ||
		looksLikeRawToolResultTranscript(reply) ||
		looksLikeTextualToolCall(reply) ||
		looksLikeUnexecutedToolRequestAnswer(reply) {
		return reply
	}
	decorated := r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     reply,
		StopReason: "model_final",
		Brief:      r.RuntimeBrief,
		Results:    results,
		UserFacing: true,
	})
	if strings.TrimSpace(decorated) == "" || strings.TrimSpace(decorated) == strings.TrimSpace(reply) {
		return reply
	}
	if finalAnswerNeedsDecoration(decorated) {
		return reply
	}
	return decorated
}

func acceptedAnswerEvidencePaths(answer string, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) []string {
	if runnerBriefRequiresMutation(brief) {
		if paths := acceptedImplementationEvidencePaths(results); len(paths) > 0 {
			return paths
		}
	}
	if readOnlyDirectAnswerAcceptanceApplies(brief) {
		return acceptedReadOnlyDirectAnswerEvidencePaths(answer, brief, results)
	}
	if readOnlyExplanationGateApplies(brief) {
		paths := mergeRunnerStateStrings(exactProbeEvidencePaths(results), pathparse.MentionedWorkspacePaths(answer))
		for _, packet := range readOnlyEvidencePacketsFromResults(results) {
			if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
				paths = mergeRunnerStateStrings(paths, packet.SourceBodyPaths, packet.InspectedPaths)
			}
		}
		paths = mergeRunnerStateStrings(paths, delegatedReadOnlyInspectedImplementationPaths(results))
		if len(paths) == 0 {
			paths = mergeRunnerStateStrings(paths, mentionedEvidencePathsFromToolResultsForObjective(results, brief.Objective, 24))
		}
		paths = prioritizedClosureEvidencePaths(paths, results, 8)
		paths = closurePromptEvidencePaths(paths, 8)
		if len(paths) > 0 {
			return paths
		}
	}
	paths := mergeRunnerStateStrings(pinnedPathsFromToolResults(results, 8), pathparse.MentionedWorkspacePaths(answer))
	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
			paths = mergeRunnerStateStrings(paths, packet.SourceBodyPaths, packet.InspectedPaths)
		}
	}
	paths = delegatedDiscoveryAnswerEvidencePaths(brief.Objective, paths)
	return closurePromptEvidencePaths(paths, 8)
}

func acceptedImplementationEvidencePaths(results []workspace.ToolResult) []string {
	paths := changedPathListFromToolResults(results)
	paths = append(paths, directProbePathsFromToolResults(results, 8)...)
	return closurePromptEvidencePathsPreservingTests(paths, 8)
}

func changedPathListFromToolResults(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, rawPath := range changedPathsFromToolResult(result) {
			path := normalizeRunnerPath(rawPath)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths
}

func closurePromptEvidencePathsPreservingTests(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range uniqueDelegatedDiscoveryPaths(paths) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}
		cleaned = append(cleaned, path)
	}
	return limitRunnerStrings(cleaned, max)
}

func exactProbeEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		default:
			continue
		}
		path, _ := extractExactProbeAnchor(result)
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return preferNonTestEvidencePaths(paths)
}

func (r *Runner) agentName() string {
	if strings.TrimSpace(r.AgentName) == "" {
		return "main"
	}
	return r.AgentName
}

func (r *Runner) explorationDelegationNudge(readCallsSinceWrite int, results []workspace.ToolResult) string {
	if r == nil || r.Workspace == nil || r.DisableTaskTool || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return ""
	}
	// The exploration delegation nudge uses mutation language
	// ("without a write") and assumes write-then-answer lanes.
	// The conversation lane has its own evidence path and should
	// not receive mutation-framed nudges.
	if r.lanePolicy().SkipExplorationDelegationNudge() {
		return ""
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}
	allowedTools := r.advertisedAllowedTools(r.Workspace.AllowedTools())
	if !containsRunnerTool(allowedTools, "task") {
		return ""
	}
	if len(changedPathsFromToolResults(results)) > 0 || hasSuccessfulTaskResult(results) {
		return ""
	}
	minEvidenceResults := 2
	threshold := 4
	if prefersEarlyReadOnlyDelegation(r.RuntimeBrief) {
		minEvidenceResults = 1
		threshold = 1
	}
	if workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) {
		if hasConcreteReadOnlyInspectionEvidence(results) {
			return ""
		}
		minEvidenceResults = 2
		threshold = 2
	}
	if successfulReadClassResultCount(results) < minEvidenceResults {
		return ""
	}
	if readCallsSinceWrite < threshold {
		return ""
	}
	return fmt.Sprintf("You have already used %d read/search steps in the main loop without a write. Answer now if the current evidence already supports a concrete answer. Only if one broad missing claim genuinely remains and a bounded read_only child would close it faster than more local probing, you may delegate one bounded read_only task with the task tool.", readCallsSinceWrite)
}

func prefersEarlyReadOnlyDelegation(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return false
	}
	for _, policy := range brief.SearchPolicy {
		if strings.EqualFold(strings.TrimSpace(policy), "delegate_broad_discovery_when_needed") {
			return true
		}
	}
	return false
}

func hasConcreteReadOnlyInspectionEvidence(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}
		switch strings.TrimSpace(result.Tool) {
		case "read_file",
			"go_read_symbol", "go_file_outline",
			"cpp_read_symbol", "cpp_file_outline",
			"python_read_symbol", "python_file_outline",
			"ts_read_symbol", "ts_file_outline",
			"rust_read_symbol", "rust_file_outline",
			"java_read_symbol", "java_file_outline":
			return true
		}
	}
	return false
}

func (r *Runner) initialBootstrapInstruction() string {
	if r == nil {
		return ""
	}
	family := strings.TrimSpace(r.RuntimeBrief.TaskFamily)
	profile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile)
	switch {
	case strings.HasPrefix(r.agentName(), "child:") && family == "delegated_read_only":
		return strings.Join([]string{
			"[delegated read-only bootstrap]",
			"Consume the delegated scope first. Do not start with directory walking when the delegated prompt already names paths or components.",
			"Use one exact-text or language-symbol capability to locate the mechanism, then inspect the best hit directly with a bounded read or symbol-read capability.",
			"For broad architecture or boundary questions, prefer a composite discovery_query first, then inspect only the named paths or symbols that answer the delegated question.",
			"After any successful search hit, inspect that hit before doing another filename or content search.",
			"Stop as soon as the delegated question is answered. Return concise grounded findings with inspected paths and confidence.",
		}, "\n")
	case workerruntime.TaskFamilyIsArchitecture(family) && workerruntime.RuntimeContractIsArchitecture(r.RuntimeBrief.Contract):
		return strings.Join([]string{
			"[architecture bootstrap]",
			"Start broad architecture questions with delegated read-only discovery when the task tool is available.",
			"The delegated packet should establish inspected paths, boundary evidence, confidence, and named gaps before local follow-up.",
			"After a child packet, answer unless one exact missing claim needs one bounded probe.",
		}, "\n")
	case workerruntime.TaskFamilyIsVerificationOnly(family) || workerruntime.PermissionProfileIsReadOnlyVerificationShell(profile):
		cmd := "go test ./..."
		if len(r.RuntimeBrief.VerificationPlan) > 0 && strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0]) != "" {
			cmd = strings.TrimSpace(r.RuntimeBrief.VerificationPlan[0])
		}
		return strings.Join([]string{
			"[verification bootstrap]",
			"Verification task contract: run the configured verification command before inspection.",
			fmt.Sprintf("Configured verification command: %s", cmd),
			"After it runs, answer with the observed pass/fail result.",
		}, "\n")
	case family == "debug_logs":
		return strings.Join([]string{
			"[debug-log bootstrap]",
			"Start from the newest relevant log artifact; source-code orientation is lower value until log evidence is exhausted.",
			"First choose the newest real log file (for example under logs/, *.log, or *.jsonl), then read its recent tail.",
			"Stay on that selected log until you can cite the weird decision or declare the missing datum.",
			"After one bounded log read, answer from the logs if the cause is already clear; otherwise do one targeted follow-up inside the same log.",
		}, "\n")
	case isEvidenceFirstMutationTaskFamily(family) && len(r.RuntimeBrief.TargetRefs) > 0:
		return strings.Join([]string{
			"[evidence-first mutation bootstrap]",
			fmt.Sprintf("This turn will update named target file(s): %s.", strings.Join(r.RuntimeBrief.TargetRefs, ", ")),
			"First gather source evidence from the implementation files or configs that drive the change.",
			"Then read only the target section you plan to edit.",
		}, "\n")
	default:
		return ""
	}
}

func (r *Runner) verificationBootstrapRequest() (workspace.ToolRequest, bool) {
	if r == nil {
		return workspace.ToolRequest{}, false
	}
	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) {
		return workspace.ToolRequest{}, false
	}
	cmd := "go test ./..."
	for _, candidate := range r.RuntimeBrief.VerificationPlan {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			cmd = trimmed
			break
		}
	}
	if strings.TrimSpace(cmd) == "" {
		return workspace.ToolRequest{}, false
	}
	return workspace.ToolRequest{
		Tool:    "shell",
		Command: cmd,
		Workdir: ".",
	}, true
}

func synthesizeVerificationOnlyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(req.Tool)
	}
	status := "passed"
	if strings.TrimSpace(result.Error) != "" {
		status = "failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Verification %s.\n\n", status)
	if command != "" {
		fmt.Fprintf(&b, "Command: `%s`\n", command)
	}
	if errText := strings.TrimSpace(result.Error); errText != "" {
		fmt.Fprintf(&b, "Failure: %s\n", errText)
	}
	if output := compactOutput(result.Output, 24); strings.TrimSpace(output) != "" {
		b.WriteString("\nOutput:\n")
		b.WriteString(output)
		b.WriteString("\n")
	}
	b.WriteString("\nNo files were edited.")
	return strings.TrimSpace(b.String())
}

func (r *Runner) debugLogPreflightRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "debug_logs" || debugLogEvidenceSatisfied(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}
	discovery, _ := debugLogDiscoveryRequest(r.Workspace)
	selection, ok := latestDebugLogSelection(results)
	if !ok {
		if debugLogRequestMatchesDiscovery(req, discovery) || isConcreteDebugLogArtifactRequest(req) {
			return workspace.ToolRequest{}, false
		}
		return discovery, true
	}
	if !hasDebugLogReadForSelection(results, selection) {
		if strings.TrimSpace(req.Tool) == "read_file" && debugLogRequestTargetsSelection(req, selection) {
			return workspace.ToolRequest{}, false
		}
		return debugLogTailRequest(selection), true
	}
	if _, ok := latestDebugLogSearchHit(results); !ok {
		if shouldDeepenDebugLogSearch(results, selection) {
			if next, ok := nextDebugLogSelection(results, selection); ok {
				if strings.TrimSpace(req.Tool) == "read_file" && debugLogRequestTargetsSelection(req, next) {
					return workspace.ToolRequest{}, false
				}
				return debugLogTailRequest(next), true
			}
		}
		if redirected, ok := debugLogScopedSearchRedirect(req, selection); ok {
			return redirected, true
		}
	}
	return workspace.ToolRequest{}, false
}

func prematureTargetReadRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || !slices.Contains(brief.TargetRefs, path) {
		return "", "", false
	}
	if !isLargeTargetRead(req) {
		return "", "", false
	}
	if hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return "", "", false
	}
	return "Premature target-file read rejected",
		"this turn needs source evidence before editing the named target file. Inspect the implementation files or configs that drive the change, then read only the target section you plan to edit.",
		true
}

func evidenceFirstTargetReadRedirect(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !isEvidenceFirstMutationTaskFamily(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return workspace.ToolRequest{}, false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, false
	}
	targetReadCount := successfulTargetReadCount(results, brief.TargetRefs)
	if !hasFocusedNonTargetEvidence(results, brief.TargetRefs) {
		return workspace.ToolRequest{}, false
	}
	if targetReadCount == 0 {
		if !isReadClassTool(req) {
			return workspace.ToolRequest{}, false
		}
		path := strings.TrimSpace(req.Path)
		if strings.TrimSpace(req.Tool) == "read_file" && slices.Contains(brief.TargetRefs, path) && !isLargeTargetRead(req) {
			return workspace.ToolRequest{}, false
		}
		return firstTargetReadWindowRequest(brief.TargetRefs), true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !slices.Contains(brief.TargetRefs, strings.TrimSpace(req.Path)) {
		return workspace.ToolRequest{}, false
	}
	if !targetReadNeedsBounding(req) {
		return workspace.ToolRequest{}, false
	}
	return boundedFollowupTargetReadRequest(req, results, brief.TargetRefs), true
}

func isEvidenceFirstMutationTaskFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return true
	default:
		return false
	}
}

func isLargeTargetRead(req workspace.ToolRequest) bool {
	start := req.StartLine
	end := req.EndLine
	if start <= 0 && end <= 0 {
		return true
	}
	if start <= 0 {
		start = 1
	}
	if end <= 0 {
		return true
	}
	return end-start > 160
}

func hasFocusedNonTargetEvidence(results []workspace.ToolResult, targetRefs []string) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		switch tool := strings.TrimSpace(result.Tool); {
		case tool == "read_file":
			path := extractRunnerFilePath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		case strings.HasSuffix(tool, "_file_outline"), strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			path := extractSemanticInspectionPath(result.Summary)
			if path != "" && !slices.Contains(targetRefs, path) {
				return true
			}
		}
	}
	return false
}

func hasSuccessfulTargetRead(results []workspace.ToolResult, targetRefs []string) bool {
	return successfulTargetReadCount(results, targetRefs) > 0
}

func successfulTargetReadCount(results []workspace.ToolResult, targetRefs []string) int {
	count := 0
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if path := extractRunnerFilePath(result.Summary); slices.Contains(targetRefs, path) {
			count++
		}
	}
	return count
}

func evidenceFirstTargetReadUnlocked(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		!hasSuccessfulTargetRead(results, brief.TargetRefs)
}

const (
	initialEvidenceFirstTargetReadWindowEnd = 220
	followupEvidenceFirstTargetReadLines    = 140
)

func firstTargetReadWindowRequest(targetRefs []string) workspace.ToolRequest {
	for _, path := range targetRefs {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			return workspace.ToolRequest{
				Tool:      "read_file",
				Path:      trimmed,
				StartLine: 1,
				EndLine:   initialEvidenceFirstTargetReadWindowEnd,
			}
		}
	}
	return workspace.ToolRequest{}
}

func boundedFollowupTargetReadRequest(req workspace.ToolRequest, results []workspace.ToolResult, targetRefs []string) workspace.ToolRequest {
	start := req.StartLine
	if start <= 0 {
		if window, ok := latestSuccessfulTargetReadWindow(results, targetRefs); ok {
			start = window.End + 1
		} else {
			start = 1
		}
	}
	if start <= 0 {
		start = 1
	}
	return workspace.ToolRequest{
		Tool:      "read_file",
		Path:      strings.TrimSpace(req.Path),
		StartLine: start,
		EndLine:   start + followupEvidenceFirstTargetReadLines - 1,
	}
}

func targetReadNeedsBounding(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	start := req.StartLine
	end := req.EndLine
	if start <= 0 || end <= 0 {
		return true
	}
	return end-start+1 > followupEvidenceFirstTargetReadLines
}

func latestSuccessfulTargetReadWindow(results []workspace.ToolResult, targetRefs []string) (debugLogReadWindow, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok || !slices.Contains(targetRefs, strings.TrimSpace(window.Path)) {
			continue
		}
		return window, true
	}
	return debugLogReadWindow{}, false
}

func clearEvidenceFirstTargetReadActionClasses(rejectedActions map[string]int, brief workerruntime.WorkerRuntimeBrief) {
	if len(rejectedActions) == 0 {
		return
	}
	for _, path := range brief.TargetRefs {
		actionClass := toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: strings.TrimSpace(path)})
		if actionClass == "" {
			continue
		}
		delete(rejectedActions, actionClass)
	}
}

func clearRejectedFingerprintSummary(rejected map[string]string, fingerprint string, summary string) {
	fingerprint = strings.TrimSpace(fingerprint)
	summary = strings.TrimSpace(summary)
	if fingerprint == "" || summary == "" || len(rejected) == 0 {
		return
	}
	if strings.TrimSpace(rejected[fingerprint]) == summary {
		delete(rejected, fingerprint)
	}
}

type debugLogSearchHit struct {
	Handle string
	Path   string
	Line   int
}

type debugLogFileSelection struct {
	Handle string
	Path   string
}

type debugLogReadWindow struct {
	Handle string
	Path   string
	Start  int
	End    int
}

var singleSearchContentSummaryRE = regexp.MustCompile(`^Found 1 matching line at (.+):([0-9]+)\.`)

func debugLogEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return false
	}
	hit, ok := latestDebugLogSearchHit(results)
	if !ok {
		return false
	}
	return hasDebugLogReadWindowForHit(results, hit)
}

func debugLogPhaseScopeRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" || debugLogEvidenceSatisfied(brief, results) {
		return "", "", false
	}
	selection, ok := latestDebugLogSelection(results)
	if !ok || !hasDebugLogReadForSelection(results, selection) {
		return "", "", false
	}
	if isReadClassTool(req) && !debugLogRequestTargetsSelection(req, selection) {
		return "Selected-log scope enforced",
			"you already selected and read the newest relevant log. Stay on that log until you can cite the weird decision or declare the missing datum.",
			true
	}
	if strings.TrimSpace(req.Tool) != "read_file" || !debugLogRequestTargetsSelection(req, selection) {
		return "", "", false
	}
	if _, ok := latestDebugLogSearchHit(results); ok {
		return "", "", false
	}
	return "Blind log reread rejected",
		"you already have the newest tail of the selected log. Answer from that tail if it is sufficient, or search within the selected log for one concrete marker.",
		true
}

func debugLogFollowupRequest(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return workspace.ToolRequest{}, false
	}
	hit, ok := latestDebugLogSearchHit(results)
	if !ok || hasDebugLogReadWindowForHit(results, hit) {
		return workspace.ToolRequest{}, false
	}
	switch strings.TrimSpace(req.Tool) {
	case "search_content":
		return debugLogReadWindowRequest(hit), true
	case "read_file":
		if debugLogRequestTargetsHit(req, hit) && debugLogRequestCoversHit(req, hit) {
			return workspace.ToolRequest{}, false
		}
		return debugLogReadWindowRequest(hit), true
	default:
		return workspace.ToolRequest{}, false
	}
}

func latestDebugLogSelection(results []workspace.ToolResult) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case "read_file":
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !isStrictDebugLogArtifactPath(window.Path) {
				continue
			}
			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case "search_content":
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) {
				continue
			}
			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case "search_files":
			selection, ok := parseDebugLogFileSelection(result)
			if ok {
				return selection, true
			}
		}
	}
	return debugLogFileSelection{}, false
}

func parseDebugLogFileSelection(result workspace.ToolResult) (debugLogFileSelection, bool) {
	selections := parseDebugLogFileSelections(result)
	if len(selections) == 0 {
		return debugLogFileSelection{}, false
	}
	return selections[0], true
}

func parseDebugLogFileSelections(result workspace.ToolResult) []debugLogFileSelection {
	if strings.TrimSpace(result.Tool) != "search_files" || strings.TrimSpace(result.Error) != "" {
		return nil
	}
	var selections []debugLogFileSelection
	for _, line := range strings.Split(result.Output, "\n") {
		handle, path := parseSearchFilesSelectionLine(line)
		if path == "" || !isStrictDebugLogArtifactPath(path) {
			continue
		}
		selections = append(selections, debugLogFileSelection{Handle: handle, Path: path})
	}
	return selections
}

func parseSearchFilesSelectionLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	handle := ""
	if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "f") {
		handle = strings.TrimSpace(fields[0])
		line = strings.TrimSpace(strings.TrimPrefix(line, handle))
	}
	if idx := strings.Index(line, " (modified "); idx >= 0 {
		line = line[:idx]
	}
	return handle, strings.TrimSpace(line)
}

func hasDebugLogReadForSelection(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}
		if selection.Handle != "" && window.Handle == selection.Handle {
			return true
		}
		if selection.Path != "" && window.Path == selection.Path {
			return true
		}
	}
	return false
}

func nextDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_files" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		selections := parseDebugLogFileSelections(result)
		for idx := range selections {
			selection := selections[idx]
			if !sameDebugLogSelection(selection, current) {
				continue
			}
			for nextIdx := idx + 1; nextIdx < len(selections); nextIdx++ {
				if !hasDebugLogReadForSelection(results, selections[nextIdx]) {
					return selections[nextIdx], true
				}
			}
		}
	}
	return debugLogFileSelection{}, false
}

func sameDebugLogSelection(a debugLogFileSelection, b debugLogFileSelection) bool {
	if a.Handle != "" && b.Handle != "" && a.Handle == b.Handle {
		return true
	}
	return a.Path != "" && b.Path != "" && a.Path == b.Path
}

func shouldDeepenDebugLogSearch(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	_ = selection
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_content" {
			continue
		}
		if strings.TrimSpace(result.Error) != "" {
			return false
		}
		return workerruntime.IsZeroHitSearchResult(result)
	}
	return false
}

func debugLogDiscoveryRequest(mgr *workspace.Manager) (workspace.ToolRequest, bool) {
	pattern := "*.log"
	if mgr != nil {
		root := strings.TrimSpace(mgr.Root())
		for _, rel := range []string{"logs", "log", "traces", "trace"} {
			if root == "" {
				break
			}
			if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
				pattern = filepath.ToSlash(rel) + "/**/*"
				break
			}
		}
	}
	return workspace.ToolRequest{Tool: "search_files", Pattern: pattern, Path: "."}, true
}

func debugLogTailRequest(selection debugLogFileSelection) workspace.ToolRequest {
	req := workspace.ToolRequest{Tool: "read_file"}
	if selection.Handle != "" {
		req.ID = selection.Handle
		return req
	}
	req.Path = selection.Path
	return req
}

func debugLogRequestTargetsSelection(req workspace.ToolRequest, selection debugLogFileSelection) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		if selection.Handle != "" && strings.TrimSpace(req.ID) == selection.Handle {
			return true
		}
		return selection.Path != "" && strings.TrimSpace(req.Path) == selection.Path
	case "search_content":
		if selection.Path == "" {
			return false
		}
		return strings.TrimSpace(req.Path) == selection.Path || strings.TrimSpace(req.Glob) == selection.Path
	default:
		return false
	}
}

func debugLogRequestMatchesDiscovery(req workspace.ToolRequest, discovery workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != strings.TrimSpace(discovery.Tool) {
		return false
	}
	return strings.TrimSpace(req.Pattern) == strings.TrimSpace(discovery.Pattern) &&
		strings.TrimSpace(req.Path) == strings.TrimSpace(discovery.Path)
}

func isConcreteDebugLogArtifactRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path))
	case "search_content":
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path)) || isStrictDebugLogArtifactPath(strings.TrimSpace(req.Glob))
	default:
		return false
	}
}

func debugLogScopedSearchRedirect(req workspace.ToolRequest, selection debugLogFileSelection) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != "search_content" || debugLogRequestTargetsSelection(req, selection) {
		return workspace.ToolRequest{}, false
	}
	if !mentionsConcreteDebugLogSelector(req.Path) && !mentionsConcreteDebugLogSelector(req.Glob) {
		return workspace.ToolRequest{}, false
	}
	redirected := req
	redirected.Path = selection.Path
	redirected.Glob = ""
	return redirected, true
}

func mentionsConcreteDebugLogSelector(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.Contains(lower, "/log/") ||
		strings.HasPrefix(lower, "log/") ||
		strings.Contains(lower, "/traces/") ||
		strings.HasPrefix(lower, "traces/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func isStrictDebugLogArtifactPath(path string) bool {
	return mentionsConcreteDebugLogSelector(path)
}

func isDiagnosticLogArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	lower = strings.TrimPrefix(lower, "./")
	switch {
	case lower == "logs" || strings.HasPrefix(lower, "logs/"):
		return true
	case lower == ".idiom/logs" || strings.HasPrefix(lower, ".idiom/logs/"):
		return true
	case lower == ".idiom/comprehension/traces" || strings.HasPrefix(lower, ".idiom/comprehension/traces/"):
		return true
	case lower == "traces" || strings.HasPrefix(lower, "traces/"):
		return true
	case strings.Contains(lower, "/.idiom/logs/") || strings.Contains(lower, "/.idiom/comprehension/traces/"):
		return true
	default:
		return false
	}
}

func isNonSourceEvidenceArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	lower = strings.TrimPrefix(lower, "./")
	switch {
	case lower == "":
		return false
	case isDiagnosticLogArtifactPath(lower):
		return true
	case lower == "transcripts" || strings.HasPrefix(lower, "transcripts/"):
		return true
	case lower == ".idiom" || strings.HasPrefix(lower, ".idiom/"):
		return true
	case lower == "idiom" || strings.HasPrefix(lower, "idiom/"):
		return true
	case lower == ".codex/discovery" || strings.HasPrefix(lower, ".codex/discovery/"):
		return true
	case lower == "testdata/swe/generated-logs" || strings.HasPrefix(lower, "testdata/swe/generated-logs/"):
		return true
	default:
		return false
	}
}

func generatedEvidenceArtifactReadRedirectResult(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolResult{}, false
	}
	path := normalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolResult{}, false
	}
	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}
	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolResult{}, false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Generated evidence artifact %s was not read because generated discovery/context JSON is not source-body evidence.\n", path)
	b.WriteString("Use the source paths already named by the current discovery packet instead:\n")
	for _, sourcePath := range limitRunnerStrings(paths, 6) {
		fmt.Fprintf(&b, "- %s\n", sourcePath)
	}
	fmt.Fprintf(&b, "Next exact source probe example: {\"tool\":\"read_file\",\"path\":%q}\n", paths[0])
	b.WriteString("After one source-body read resolves the claim, answer from the gathered evidence instead of reading artifact JSON.")
	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Redirected generated evidence artifact read to source paths",
		Output:  strings.TrimSpace(b.String()),
	}, true
}

func generatedEvidenceArtifactReadRedirectRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolRequest{}, "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolRequest{}, "", "", false
	}
	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}
	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", "", false
	}
	redirected := req
	redirected.Path = paths[0]
	redirected.StartLine = 0
	redirected.EndLine = 0
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence named by generated discovery artifact")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "generated discovery/context JSON is provenance, not source-body evidence")
	return redirected, marshalToolRequestRaw(redirected), "generated discovery/context artifact redirected to source-body path", true
}

func structuredSourceReadRewriteRequest(req workspace.ToolRequest, ws *workspace.Manager, allowedTools []string, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if ws == nil || runnerBriefRequiresMutation(brief) || !redirectToolAllowed(allowedTools, "code_navigate") {
		return workspace.ToolRequest{}, "", "", false
	}
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", "", false
	}
	lang, cap, ok := matchRedirectRule(req, ws)
	if !ok || cap != workspace.CapFileOutline {
		return workspace.ToolRequest{}, "", "", false
	}
	path, ok := redirectReadFilePath(req, ws)
	if !ok || strings.TrimSpace(path) == "" {
		return workspace.ToolRequest{}, "", "", false
	}
	redirected := workspace.ToolRequest{
		Tool:            "code_navigate",
		Capability:      string(cap),
		Path:            normalizeRunnerPath(path),
		Language:        lang.Name,
		NamedGap:        req.NamedGap,
		WhyNotAnswering: req.WhyNotAnswering,
	}
	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}
	return redirected, raw, "source read redirected to structured navigation", true
}

func pathlessReadSymbolAfterOutlineRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != "code_navigate" || strings.TrimSpace(req.Path) != "" {
		return workspace.ToolRequest{}, "", "", false
	}
	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "read_symbol" && capability != "symbol_body" && capability != "inspect_symbol" {
		return workspace.ToolRequest{}, "", "", false
	}
	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}
	path := latestCodeNavigateOutlinePath(results)
	if path == "" {
		return workspace.ToolRequest{}, "", "", false
	}
	redirected := req
	redirected.Path = path
	if strings.TrimSpace(redirected.Pattern) == "" {
		redirected.Pattern = symbol
	}
	if strings.TrimSpace(redirected.Symbol) == "" {
		redirected.Symbol = symbol
	}
	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}
	return redirected, raw, "pathless read_symbol anchored to latest file_outline path", true
}

func latestCodeNavigateOutlinePath(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "code_navigate" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}
		for _, path := range readPathsFromToolResult(result) {
			path = normalizeRunnerPath(path)
			if path != "" && !isNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}
		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			path = normalizeRunnerPath(path)
			if path != "" && !isNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}
	}
	return ""
}

func repeatedFileOutlineToReadSymbolRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != "code_navigate" {
		return workspace.ToolRequest{}, "", "", false
	}
	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "file_outline" && capability != "outline" {
		return workspace.ToolRequest{}, "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || concreteSourceEvidenceReadForPath(path, results) || !priorFileOutlineForPath(path, results) {
		return workspace.ToolRequest{}, "", "", false
	}
	symbol := planThenExecuteBestOutlineSymbol(path, brief.Objective, results)
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}
	redirected := req
	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for repeated outline path")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "need a declaration body from the outlined file rather than another outline")
	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}
	return redirected, raw, "repeated file_outline upgraded to source-body read_symbol", true
}

func priorFileOutlineForPath(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "code_navigate" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}
		for _, candidate := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			if normalizeRunnerPath(candidate) == path {
				return true
			}
		}
	}
	return false
}

func readOnlyDefinitionToReadSymbolRewriteRequest(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != "code_navigate" {
		return workspace.ToolRequest{}, "", "", false
	}
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsArchitecture(brief.Contract) &&
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return workspace.ToolRequest{}, "", "", false
	}
	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "find_definition" && capability != "definition" {
		return workspace.ToolRequest{}, "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))
	if path == "" || symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}
	redirected := req
	redirected.Path = path
	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for definition target")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "definition location is anchor evidence; the answer needs the declaration body")
	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}
	return redirected, raw, "read-only definition probe upgraded to source-body read_symbol", true
}

func isGeneratedDiscoveryContextArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	lower = strings.TrimPrefix(lower, "./")
	return strings.HasPrefix(lower, ".idiom/discovery/queries/") ||
		strings.HasPrefix(lower, ".idiom/comprehension/views/") ||
		strings.HasPrefix(lower, ".codex/discovery/")
}

func latestDebugLogSearchHit(results []workspace.ToolResult) (debugLogSearchHit, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if hit, ok := parseDebugLogSearchHit(result); ok {
			return hit, true
		}
	}
	return debugLogSearchHit{}, false
}

func parseDebugLogSearchHit(result workspace.ToolResult) (debugLogSearchHit, bool) {
	match := singleSearchContentSummaryRE.FindStringSubmatch(strings.TrimSpace(result.Summary))
	if len(match) != 3 {
		return debugLogSearchHit{}, false
	}
	line := parseRunnerInt(match[2])
	if line <= 0 {
		return debugLogSearchHit{}, false
	}
	hit := debugLogSearchHit{Path: strings.TrimSpace(match[1]), Line: line}
	firstLine := firstNonEmptyLine(result.Output)
	if firstLine == "" {
		return hit, true
	}
	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "f") {
		hit.Handle = strings.TrimSpace(parts[0])
	}
	return hit, true
}

func hasDebugLogReadWindowForHit(results []workspace.ToolResult, hit debugLogSearchHit) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}
		if hit.Handle != "" && window.Handle != "" && hit.Handle == window.Handle && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
		if hit.Path != "" && window.Path == hit.Path && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
	}
	return false
}

func debugLogReadWindowRequest(hit debugLogSearchHit) workspace.ToolRequest {
	start := hit.Line - 20
	if start < 1 {
		start = 1
	}
	end := hit.Line + 20
	req := workspace.ToolRequest{
		Tool:      "read_file",
		StartLine: start,
		EndLine:   end,
	}
	if hit.Handle != "" {
		req.ID = hit.Handle
	} else {
		req.Path = hit.Path
	}
	return req
}

func debugLogRequestTargetsHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Handle != "" && strings.TrimSpace(req.ID) == hit.Handle {
		return true
	}
	return hit.Path != "" && strings.TrimSpace(req.Path) == hit.Path
}

func debugLogRequestCoversHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Line <= 0 {
		return false
	}
	start := req.StartLine
	end := req.EndLine
	if start <= 0 {
		start = hit.Line
	}
	if end <= 0 {
		end = start + 80
	}
	return hit.Line >= start && hit.Line <= end
}

func parseDebugLogReadWindow(result workspace.ToolResult) (debugLogReadWindow, bool) {
	summary := strings.TrimSpace(result.Summary)
	if !strings.HasPrefix(summary, "Read ") {
		return debugLogReadWindow{}, false
	}
	body := strings.TrimPrefix(summary, "Read ")
	linesIdx := strings.Index(body, " lines ")
	if linesIdx <= 0 {
		return debugLogReadWindow{}, false
	}
	left := strings.TrimSpace(body[:linesIdx])
	right := body[linesIdx+len(" lines "):]
	rangeField := right
	if space := strings.IndexByte(rangeField, ' '); space >= 0 {
		rangeField = rangeField[:space]
	}
	rangeParts := strings.SplitN(rangeField, "-", 2)
	if len(rangeParts) != 2 {
		return debugLogReadWindow{}, false
	}
	start := parseRunnerInt(rangeParts[0])
	end := parseRunnerInt(rangeParts[1])
	if start <= 0 || end < start {
		return debugLogReadWindow{}, false
	}
	window := debugLogReadWindow{Start: start, End: end}
	leftParts := strings.SplitN(left, " ", 2)
	if len(leftParts) == 2 && strings.HasPrefix(leftParts[0], "f") {
		window.Handle = strings.TrimSpace(leftParts[0])
		window.Path = strings.TrimSpace(leftParts[1])
		return window, window.Path != ""
	}
	window.Path = left
	return window, window.Path != ""
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func parseRunnerInt(s string) int {
	value := 0
	for _, ch := range strings.TrimSpace(s) {
		if ch < '0' || ch > '9' {
			return 0
		}
		value = value*10 + int(ch-'0')
	}
	return value
}

type runnerControlRails struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	DynamicInstructions  bool     `json:"dynamic_instruction_attachment,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

func (r *Runner) emitControlRailsForBrief(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief) {
	if r == nil || r.OnEvent == nil {
		return
	}
	rails := r.controlRailsForBrief(brief)
	data, err := json.Marshal(rails)
	if err != nil {
		return
	}
	r.emitEvent(RunEvent{Role: "control_rails", Content: string(data)})
}

func (r *Runner) controlRails(messages []llama.ChatMessage) runnerControlRails {
	if r == nil {
		return runnerControlRails{}
	}
	return r.controlRailsForBrief(r.RuntimeBrief)
}

func (r *Runner) controlRailsForBrief(brief workerruntime.WorkerRuntimeBrief) runnerControlRails {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	allowedTools := []string(nil)
	if workerruntime.PermissionProfileIsConversationOnly(brief.PermissionProfile) {
		allowedTools = nil
	} else if len(brief.AllowedTools) > 0 {
		allowedTools = append([]string(nil), brief.AllowedTools...)
	} else if r.Workspace != nil {
		allowedTools = r.Workspace.AllowedTools()
	}
	if implementationPreWriteToolSurfaceActive(brief, nil) {
		allowedTools = implementationPreWriteAllowedTools(allowedTools)
	} else if implementationScopedEditSurfaceActive(brief, nil) {
		var extReg *workspace.ExternalToolRegistry
		if r.Workspace != nil {
			extReg = r.Workspace.ExternalToolsRegistry()
		}
		allowedTools = implementationScopedEditAllowedToolsWithRegistry(allowedTools, extReg)
	}
	profile := strings.TrimSpace(brief.PermissionProfile)
	if profile == "" {
		if len(allowedTools) == 0 {
			profile = string(workerruntime.PermissionConversationOnly)
		} else {
			profile = string(workerruntime.PermissionReadOnlyWorkspace)
		}
	}
	rails := runnerControlRails{
		Lane:                 util.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		Contract:             util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		TaskFamily:           util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyDirectAnswer)),
		PermissionProfile:    profile,
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        append([]string(nil), brief.RequiredTools...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       append([]string(nil), brief.ForbiddenTools...),
		ReadinessGate:        append([]string(nil), brief.ReadinessGate...),
		SearchPolicy:         append([]string(nil), brief.SearchPolicy...),
		VerificationContract: util.FirstNonEmpty(strings.TrimSpace(brief.VerificationContract), string(workerruntime.VerificationNotRequiredConversation)),
		StopConditions:       append([]string(nil), brief.StopConditions...),
		FinalCritic:          append([]string(nil), brief.FinalCritic...),
		DynamicInstructions:  r.InstructionAttacher != nil,
	}
	if len(rails.RequiredEvidence) == 0 {
		rails.RequiredEvidence = []string{string(workerruntime.RequiredEvidenceAnswerAddressesUserRequest)}
	}
	if len(rails.StopConditions) == 0 {
		rails.StopConditions = []string{string(workerruntime.StopAnsweredUserRequest), string(workerruntime.StopBlockedByMissingInformation)}
	}
	if runnerBriefRequiresMutation(brief) {
		rails.Lane = util.FirstNonEmpty(strings.TrimSpace(brief.Lane), "main")
		rails.Contract = util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractImplementationResult))
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyImplementation))
		rails.PermissionProfile = util.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionGuardedWorkspaceWrite))
		if planThenExecutePlannerBrief(brief) {
			rails.RequiredEvidence = mergeRunnerStateStrings([]string{string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied)}, rails.RequiredEvidence)
			rails.RequiredTools = mergeRunnerStateStrings([]string{"submit_plan"}, rails.RequiredTools)
			rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, "unbounded_write")
			rails.ReadinessGate = mergeRunnerStateStrings([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, rails.ReadinessGate)
			rails.SearchPolicy = mergeRunnerStateStrings([]string{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "submit_plan_when_edit_set_is_supported", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"}, rails.SearchPolicy)
			rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredTextMutation))
			rails.StopConditions = mergeRunnerStateStrings([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, rails.StopConditions)
			rails.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}
			return rails
		}
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason), string(workerruntime.RequiredEvidenceVerificationStatus)})
		if implementationPreWriteToolSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{"edit_file_or_apply_patch"}
		} else if implementationScopedEditSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}
		} else {
			rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{"edit_file_or_apply_patch", "source_sanity_or_verification_shell"})
		}
		rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, "unbounded_write")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessChangedPathsReported), string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"})
		rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationTestsPassedFailedSkipped))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopChangeVerified), string(workerruntime.StopBlockedWithFailedVerificationOrScope)})
		rails.FinalCritic = []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"}
		return rails
	}
	if runnerAllowsTool(rails.AllowedTools, "shell") && !runnerHasWorkspaceWriteCapability(brief, rails.AllowedTools) && runnerVerificationRequired(brief) {
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), "verification")
		rails.Contract = util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractGroundedExplanation))
		rails.PermissionProfile = util.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionReadOnlyVerificationShell))
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceVerificationCommand), string(workerruntime.RequiredEvidencePassFailStatus)})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{"shell"})
		rails.ForbiddenTools = appendRunnerTools(rails.ForbiddenTools, "edit_file", "write_file", "append_file", "mkdir", "mutating_shell")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"run_requested_check", "capture_failure_output", "do_not_edit"})
		rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		return rails
	}
	if strings.EqualFold(strings.TrimSpace(rails.TaskFamily), "debug_logs") {
		rails.FailureToEval = true
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"latest_log_selected", "failure_cause_supported_by_log", "low_confidence_triggers_deepening"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}
	if rails.Contract == string(workerruntime.ContractArchitecture) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}
	if rails.Contract == string(workerruntime.ContractReviewFindings) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}
	if rails.Contract == string(workerruntime.ContractDirectAnswer) {
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyDirectAnswer))
		if len(rails.AllowedTools) == 0 {
			rails.PermissionProfile = string(workerruntime.PermissionConversationOnly)
			rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, "shell")
		}
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceAnswerAddressesPrompt)})
		rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredReadOnlyLane))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopRequiredEvidenceSatisfied)})
		if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
			rails.ReadinessGate = nil
			rails.FinalCritic = nil
		}
	}
	if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
		rails.ReadinessGate = nil
		rails.FinalCritic = nil
	}
	return rails
}

func runnerUsesSoftReadOnlyShapingOnly(brief workerruntime.WorkerRuntimeBrief, rails runnerControlRails) bool {
	if runnerBriefRequiresMutation(brief) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(rails.TaskFamily), "debug_logs") {
		return false
	}
	switch strings.TrimSpace(rails.Contract) {
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return false
	}
	if runnerVerificationRequired(brief) {
		return false
	}
	switch strings.TrimSpace(rails.PermissionProfile) {
	case string(workerruntime.PermissionReadOnlyWorkspace):
		return true
	default:
		return false
	}
}

func hideBroadDiscoveryTools(hidden map[string]bool) {
	for _, tool := range []string{
		"list_files",
		"discovery_query", "dependency_graph", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints",
		"cpp_overview", "cpp_entrypoints",
		"python_overview", "python_entrypoints",
		"ts_overview", "ts_entrypoints",
		"rust_overview", "rust_entrypoints",
		"java_overview", "java_entrypoints",
	} {
		hidden[tool] = true
	}
}

func (r *Runner) augmentReadOnlyChildPrompt(prompt string) string {
	guidance := []string{
		"Answer the delegated question directly once the evidence is sufficient. After readiness, answer immediately — do not continue reading for completeness.",
		"Treat inherited instructions to explore directories, entrypoints, or patterns as background only unless the delegated question explicitly requires them.",
		"Use repository-relative workspace paths in the answer. Do not rely on transient file handles such as f23 or tool-local aliases.",
		"Prefer targeted symbol tools and bounded file reads over workspace-wide overviews.",
	}
	if namedPaths := pathparse.MentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		guidance = append(guidance, fmt.Sprintf("Start from these named workspace paths: %s.", strings.Join(namedPaths, ", ")))
	}
	return strings.TrimSpace(prompt) + "\n\nScope guidance:\n- " + strings.Join(guidance, "\n- ")
}

func (r *Runner) completeWithoutMoreTools(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {
	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Tools are now disabled.", metrics)
}

func answerFirstNoToolsPrompt(reason string) string {
	reason = strings.TrimSpace(reason)
	contract := "Final answer contract: tools are unavailable now. Write the normal final answer from the evidence already gathered. Do not emit XML tool-call tags, tool JSON, or tool-request syntax of any kind — write only plain prose. If the evidence supports a concrete answer, answer directly and do not lead with budget, blocker, partial-findings, or recommended-next-step framing. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action.\n\nA good answer explains the mechanism and names the evidence: state what happens, which code paths are involved, and cite the files or functions you inspected. For example: 'Post-write validation runs through source_sanity (runner_verification.go:120-200) which checks changed files with go vet. The implement lane calls this directly after each write, while plan-execute defers verification to the review-repair phase.' Do not dump raw function signatures — synthesize what you learned."
	if reason == "" {
		return contract
	}
	if strings.Contains(reason, "Final answer contract:") {
		return reason
	}
	return strings.TrimSpace(reason + "\n\n" + contract)
}

func (r *Runner) completeWithoutMoreToolsWithReason(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
	metrics *runnerMetrics,
) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Tools are now disabled."
	}
	reason = answerFirstNoToolsPrompt(reason)
	preferModelSynthesis := shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults)
	if !preferModelSynthesis && postReadinessForcedCompletionReason(reason) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
				return answer, nil
			}
		}
	}
	if !preferModelSynthesis {
		if answer, ok := immediateBudgetFallbackAnswer(r.agentName(), r.RuntimeBrief, reason, collectedResults); ok {
			return answer, nil
		}
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return answer, nil
		}
	}
	if preferModelSynthesis {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
		if paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, collectedResults, 4); len(paths) > 0 {
			reason = strings.TrimSpace(reason + "\nCarry forward the inspected implementation paths explicitly in the answer: " + summarizePostChildEvidencePaths(paths, 4) + ". Do not collapse the answer to a single file when the evidence already spans multiple relevant implementation paths.")
		}
	}
	// If any task_ledger items are still in_progress, prepend a closure
	// hint so the forced-completion reply actually resolves them instead
	// of leaving dangling in_progress todos in the transcript.
	if hint := taskLedgerClosureHint(collectedResults); hint != "" {
		reason = hint + "\n\n" + reason
	}
	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	turnMessages = stripOrphanedToolCalls(turnMessages)
	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(turnMessages, textOnlyOpts)
	assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}
	r.emitModelCallTraceWithOptions(0, "forced_completion", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)
	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, textOnlyOpts, false)
	preferDeterministicFallback := shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults)

	if len(assistantMessage.ToolCalls) > 0 {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "native tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}
	if looksLikeTextualToolCall(reply) {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "textual tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "malformed tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", parseErr
	} else if ok {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}
		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}
		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}
		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
	if err != nil {
		return "", err
	}
	if _, rejected := r.completionGateRejection(answer, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		// No deterministic fallback available — return a clean user-facing
		// message instead of an error so the user sees something useful.
		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}
	if _, rejected := r.readOnlyNonSubstantiveCompletionRejection(answer, collectedResults); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}
	if finalAnswerNeedsDecoration(answer) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, textOnlyOpts, answer, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, nil
			}
		}
		if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, answer); ok {
			return presentable, nil
		}
	}
	return answer, nil
}

func postReadinessForcedCompletionReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(lower, "post-readiness") &&
		strings.Contains(lower, "tools are now disabled")
}

func (r *Runner) repairOrFallbackFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {
	rawFallback, ok := synthesizeNoToolsFallbackAnswer(r.agentName(), r.RuntimeBrief, collectedResults)
	if !ok {
		return "", false, nil
	}
	if finalAnswerNeedsDecoration(rawFallback) && r.userFacingFinalAnswer() {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, rawFallback, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}
	}
	if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, rawFallback); ok {
		return presentable, true, nil
	}
	return "", false, nil
}

func (r *Runner) repairInternalFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	internalAnswer string,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {
	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false, nil
	}
	repairMessages := append([]llama.ChatMessage(nil), turnMessages...)
	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: "The previous final synthesis still did not produce a usable user-facing answer. Tools remain disabled. " +
			"The text below is an internal evidence summary, not a final-answer template. Rewrite it into the normal final answer to the user's request using only claims supported by that evidence and the prior tool results. " +
			"Do not mention evidence packets, probes, tool calls, budgets, fallback behavior, or internal sections. If one exact claim is unsupported, state that uncertainty at the end.\n\nInternal evidence summary:\n" + internalAnswer,
	})
	if metrics != nil {
		metrics.ModelCalls++
	}
	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	repairMessages = stripOrphanedToolCalls(repairMessages)
	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(repairMessages, opts)
	repairMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}
	if metrics != nil {
		metrics.addUsage(repairMessage.Usage)
	}
	r.emitModelCallTraceWithOptions(0, "fallback_final_answer_retry", requestMessages, repairMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "fallback_final_answer_retry_reply", repairMessage, opts, false)

	if len(repairMessage.ToolCalls) > 0 {
		return "", false, nil
	}
	content := strings.TrimSpace(repairMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}
	if finalAnswerNeedsDecoration(content) {
		return "", false, nil
	}
	// Run the completion gate on the repaired answer. The retry path
	// (retryInvalidFinalAnswer) already runs this gate, but this
	// fallback-repair path was skipping it, allowing garbage answers
	// like raw tool result transcripts to reach the user.
	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		return "", false, nil
	}
	r.LastAssistantMessage = repairMessage
	return content, true, nil
}

func (r *Runner) retryInvalidFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
	reason string,
) (string, bool, error) {
	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool request"
	}
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous final response was a " + reason + ", but tools are disabled for final synthesis. " +
			"Do not emit tool JSON, XML, tool blocks, or another probe. Reply in normal prose from the evidence already gathered. " +
			"If the evidence is sufficient, answer directly; if one exact claim remains unsupported, add that uncertainty after the supported answer in one sentence. " +
			"Do not list internal tool traces, exact probes, or inspected-path inventories unless they are necessary to explain the answer.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)
	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(retryMessages, opts)
	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}
	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}
	r.emitModelCallTraceWithOptions(0, "invalid_final_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "invalid_final_answer_retry_reply", retryMessage, opts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return "", false, nil
	}
	content := strings.TrimSpace(retryMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}
	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}
	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}
		return "", false, nil
	}
	if finalAnswerNeedsDecoration(content) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, content, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := presentableFallbackCandidate(r.agentName(), r.RuntimeBrief, collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}
		return "", false, nil
	}
	r.LastAssistantMessage = retryMessage
	return content, true, nil
}

func shouldPreferDeterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) {
		return len(changedPathsFromToolResults(results)) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0 ||
			targetPacketSupportsWrite(brief)
	}
	if runnerVerificationRequired(brief) {
		return false
	}
	if strings.HasPrefix(agentName, "child:") {
		return true
	}
	return workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func deterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	answer, ok := synthesizeNoToolsFallbackAnswer(agentName, brief, results)
	if !ok {
		return "", false
	}
	return presentableFallbackCandidate(agentName, brief, results, answer)
}

func presentableFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	if runnerBriefRequiresMutation(brief) {
		return answer, true
	}
	return readOnlyClosureDecisionForResults(brief, results).PresentableFallbackCandidate(agentName, answer)
}

func presentableOrReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableOrReadOnlyNonAnswerReplacement(agentName, answer)
}

func presentableBriefConversationFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableBriefConversationFallbackCandidate(agentName)
}

func looksLikeDelegatedGroundedFallbackAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}
	if genericEvidenceAnswerIncludesLineOrientedSourceBody(trimmed) || looksLikeLineOrientedSourceSnippetAnswer(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "the answer is grounded in ") &&
		strings.Contains(lower, "\n\nwhat this establishes:\n- ")
}

func fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.HasPrefix(agentName, "child:") || strings.HasPrefix(agentName, "child_write:") {
		return false
	}
	return readOnlyDirectAnswerLane(brief) && !runnerBriefRequiresMutation(brief)
}

func deterministicNoToolsNonAnswerReplacement(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).NonAnswerReplacement()
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}
	if strings.HasPrefix(agentName, "child:") {
		return true
	}
	return postChildPacketClosureReadyWithBrief(brief, results) ||
		postChildSourceBodyPacketEvidenceAvailable(results)
}

func postChildSourceBodyPacketEvidenceAvailable(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "task" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if len(packet.SourceBodyPaths) > 0 {
				return true
			}
			continue
		}
		if delegatedSourceBodyPacketHasGroundedFinding(result.Output) {
			return true
		}
	}
	return false
}

func runnerBriefAllowsExternalToolAppend(brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer" {
		return false
	}
	switch strings.TrimSpace(brief.PermissionProfile) {
	case string(workerruntime.PermissionPlanSubmissionReview), string(workerruntime.PermissionReadOnlyVerificationShell):
		return false
	}
	switch strings.TrimSpace(brief.TaskFamily) {
	case PlanThenExecuteReviewRepairTaskFamily, "debug_logs":
		return false
	default:
		return true
	}
}

func (r *Runner) advertisedAllowedTools(base []string) []string {
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(r.RuntimeBrief.Contract) == "artifact_final_answer" {
		return nil
	}
	if workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		// Even in conversation_only mode, expose project-defined external
		// tools so the model can call host-registered discovery commands
		// (e.g. project_search, api_access). Built-in mutating tools
		// stay hidden — that's what conversation_only is for.
		if r.Workspace == nil {
			return nil
		}
		if r.Workspace.ExternalToolsRegistry() == nil {
			return nil
		}
		names := r.Workspace.AvailableExternalToolNames()
		// Preserve order by filtering base to only externals; this also
		// drops any non-external entry that snuck into base via prior
		// pipeline mutations.
		known := make(map[string]struct{}, len(names))
		for _, n := range names {
			known[n] = struct{}{}
		}
		var out []string
		for _, name := range base {
			if _, ok := known[strings.TrimSpace(name)]; ok {
				out = append(out, name)
			}
		}
		// If base was empty (e.g. brief AllowedTools = nil), append
		// directly from the registry so the externals still surface.
		if len(out) == 0 {
			out = append(out, names...)
		}
		return out
	}
	allowed := append([]string(nil), base...)
	if len(r.RuntimeBrief.AllowedTools) > 0 {
		available := make(map[string]string, len(base))
		for _, tool := range base {
			trimmed := strings.TrimSpace(tool)
			if trimmed != "" {
				available[trimmed] = tool
			}
		}
		filtered := make([]string, 0, len(r.RuntimeBrief.AllowedTools))
		seen := make(map[string]struct{}, len(r.RuntimeBrief.AllowedTools))
		for _, tool := range r.RuntimeBrief.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			if _, ok := seen[tool]; ok {
				continue
			}
			if original, ok := available[tool]; ok {
				filtered = append(filtered, original)
				seen[tool] = struct{}{}
			}
		}
		allowed = filtered
	}
	if r.DisableTaskTool {
		filtered := allowed[:0]
		for _, tool := range allowed {
			if tool != "task" {
				filtered = append(filtered, tool)
			}
		}
		allowed = filtered
	}
	return allowed
}

func textOnlyChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""
	thinkingOff := false
	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false
	limit := textOnlyMaxTokensForBrief(brief)
	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}
	return opts
}

func readOnlyEvidenceAvailableTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if !readOnlyEvidenceAvailableTerminalVisibleOutput(brief, results) {
		return opts
	}
	thinkingOff := false
	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false
	return opts
}

func readOnlyEvidenceAvailableTerminalVisibleOutput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || artifactFinalAnswerBrief(brief) {
		return false
	}
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}
	if !readOnlyDirectAnswerLane(brief) {
		return false
	}
	return genericReadOnlyEvidenceReady(brief, results)
}

func textOnlyMaxTokensForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case string(workerruntime.ContractArtifactFinalAnswer):
		return 8192
	case string(workerruntime.ContractDirectAnswer):
		return 1024
	case string(workerruntime.ContractGroundedExplanation), string(workerruntime.ContractGroundedEvidence), string(workerruntime.ContractLocateTargets):
		return 1536
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return 2048
	default:
		return 768
	}
}

// IsTextualToolCall returns true when text looks like tool-call markup
// that should never reach the user-facing presentation layer.
func IsTextualToolCall(text string) bool {
	return looksLikeTextualToolCall(text)
}

// looksLikeXMLToolCall returns true when the text contains XML/HTML
// tool-call markup tags (llama.cpp DSML, janus, n_tool, etc.).
// Does not match line-oriented key-value tool requests.
func looksLikeXMLToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml")
}

func looksLikeTextualToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml") ||
		strings.Contains(lower, "[calling tool:") {
		return true
	}
	return looksLikeLineOrientedToolRequest(lower)
}

func looksLikeLineOrientedToolRequest(lower string) bool {
	lines := strings.Split(lower, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "tool:") {
		return false
	}
	toolFields := 0
	for _, line := range lines[1:] {
		field := strings.TrimSpace(line)
		if strings.Contains(field, `"tool"`) {
			return true
		}
		if strings.HasPrefix(field, "{") && strings.Contains(field, `"tool"`) {
			return true
		}
		switch {
		case strings.HasPrefix(field, "capability:"),
			strings.HasPrefix(field, "path:"),
			strings.HasPrefix(field, "pattern:"),
			strings.HasPrefix(field, "query:"),
			strings.HasPrefix(field, "task:"),
			strings.HasPrefix(field, "mode:"),
			strings.HasPrefix(field, "limit:"),
			strings.HasPrefix(field, "command:"),
			strings.HasPrefix(field, "args:"):
			toolFields++
		}
	}
	return toolFields > 0
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	switch strings.TrimSpace(req.Tool) {
	case "task_ledger":
		return strings.TrimSpace(result.Output) != ""
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations", "apply_patch":
		summary := strings.ToLower(strings.TrimSpace(result.Summary))
		return summary != "" && !strings.Contains(summary, "no-op")
	case "read_file", "search_files", "search_content", "rg",
		"go_file_outline", "go_find_symbol", "go_find_type", "go_find_const", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	case "shell":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	default:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	}
}

func isMissingPathListFailure(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "list_files" {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(result.Error))
	return strings.Contains(errText, "no such file or directory") || strings.Contains(errText, "cannot find the path")
}

// rewriteMissingRequiredNewFileProbe converts a "file not found" error
// on a probe targeting a required_edit_path, or a missing parent dir of
// required_edit_paths, into an actionable nudge: the planner can create
// those files via submit_plan's whole-file shape (replace_all=true with
// empty old_text + new_text=full body). Without this rewrite the planner
// sees the bare "no such file" error, treats it as a blocker, and gives
// up. That is the failure mode local Qwen hit on plan-execute dispatch.
//
// The rewrite preserves the result as an Error (so the planner can't
// use it as evidence of an actual file body) but replaces the message
// with one that names the right next step. We intentionally do NOT
// convert to a successful no-op: a successful probe of a non-existent
// path would leak into downstream "did the planner read this path"
// heuristics in misleading ways.
func rewriteMissingRequiredNewFileProbe(req workspace.ToolRequest, result workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) workspace.ToolResult {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return result
	}
	if strings.TrimSpace(result.Error) == "" {
		return result
	}
	lower := strings.ToLower(result.Error)
	if !strings.Contains(lower, "no such file") && !strings.Contains(lower, "does not exist") {
		return result
	}
	target := normalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return result
	}
	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return result
	}
	if len(matches) == 1 && normalizeRunnerPath(matches[0]) == target {
		result.Summary = "Required new-file path does not exist yet"
		result.Error = fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. Do NOT keep trying to inspect it. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence. Large bodies are moved to artifact refs after submission.", target, target)
		return result
	}
	result.Summary = "Required new-file directory does not exist yet"
	result.Error = fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. Do NOT keep trying to inspect the missing directory. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches))
	return result
}

func unavailableRequiredNewFileProbeNudge(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, manager *workspace.Manager) (string, string, bool) {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return "", "", false
	}
	target := normalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return "", "", false
	}
	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return "", "", false
	}
	if !requiredNewFileProbeTargetMissing(target, manager) {
		return "", "", false
	}
	if len(matches) == 1 && normalizeRunnerPath(matches[0]) == target {
		return "Required new-file path does not exist yet",
			fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence.", target, strings.TrimSpace(req.Tool), target),
			true
	}
	return "Required new-file directory does not exist yet",
		fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches), strings.TrimSpace(req.Tool)),
		true
}

func requiredNewFileProbeTargetMissing(target string, manager *workspace.Manager) bool {
	root := ""
	if manager != nil {
		root = strings.TrimSpace(manager.Root())
	}
	if root == "" {
		return true
	}
	path := target
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func missingRequiredNewFileProbeTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "read_file" || tool == "list_files" {
		return true
	}
	return strings.HasSuffix(tool, "_file_outline")
}

func requiredNewFileProbeMatches(target string, requiredPaths []string) []string {
	target = strings.Trim(strings.TrimSpace(normalizeRunnerPath(target)), "/")
	if target == "" {
		return nil
	}
	var matches []string
	for _, p := range requiredPaths {
		path := strings.Trim(strings.TrimSpace(normalizeRunnerPath(p)), "/")
		if path == "" {
			continue
		}
		if path == target || strings.HasPrefix(path, target+"/") || filepath.Dir(path) == target {
			matches = appendUniqueRunnerStrings(matches, path)
		}
	}
	return matches
}

func appendUniqueRunnerStrings(values []string, additions ...string) []string {
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(values, value) {
			continue
		}
		values = append(values, value)
	}
	return values
}

func changedPathsFromToolResults(results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range changedPathsFromToolResult(result) {
			paths[normalizeRunnerPath(path)] = struct{}{}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func unresolvedRejectedMutationPaths(results []workspace.ToolResult) []string {
	changed := changedPathsFromToolResults(results)
	var rejected []string
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}
		paths := readOnlySourceLikePaths(readPathsFromToolResult(result), 8)
		paths = mergeRunnerStateStrings(paths, readOnlySourceLikePaths(sourceLikePathsFromText(result.Summary+"\n"+result.Output+"\n"+result.Error, 8), 8))
		for _, path := range paths {
			path = normalizeRunnerPath(path)
			if path == "" {
				continue
			}
			if _, ok := changed[path]; ok {
				continue
			}
			rejected = appendUniqueRunnerStrings(rejected, path)
		}
	}
	return uniqueSortedRunnerPaths(rejected)
}

func unsupportedClaimedChangedPaths(reply string, results []workspace.ToolResult) []string {
	claimed := claimedChangedPathsFromAnswer(reply)
	if len(claimed) == 0 {
		return nil
	}
	actual := changedPathsFromToolResults(results)
	if len(actual) == 0 {
		return claimed
	}
	var unsupported []string
	for _, path := range claimed {
		if _, ok := actual[path]; !ok {
			if claimedPathMatchesActualBasename(path, actual) || !looksLikeStrictChangedPathClaim(path) {
				continue
			}
			unsupported = append(unsupported, path)
		}
	}
	return unsupported
}

func claimedPathMatchesActualBasename(claimed string, actual map[string]struct{}) bool {
	claimed = normalizeRunnerPath(claimed)
	if claimed == "" || strings.Contains(claimed, "/") {
		return false
	}
	matches := 0
	for path := range actual {
		if filepath.Base(path) == claimed {
			matches++
		}
	}
	return matches == 1
}

func looksLikeStrictChangedPathClaim(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	if pathparse.HasWorkspaceFileExtension(path) {
		return true
	}
	first, _, hasSlash := strings.Cut(path, "/")
	if !hasSlash {
		return false
	}
	switch first {
	case ".codex", ".github", ".idiom", ".vscode", "app", "apps", "bin", "cmd", "config", "configs", "docs", "examples", "internal", "lib", "native", "pkg", "prompts", "scripts", "src", "test", "tests", "tools", "web":
		return true
	default:
		return false
	}
}

func missingRequiredEditPaths(required []string, actual map[string]struct{}) []string {
	if len(required) == 0 {
		return nil
	}
	var missing []string
	seen := make(map[string]struct{}, len(required))
	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		if _, ok := actual[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}
	return missing
}

func (r *Runner) missingRequiredEditSnippets(results []workspace.ToolResult) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}
	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return nil
	}
	contentCache := make(map[string]string)
	var missing []string
	for i, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, changed)
		if len(candidates) == 0 {
			candidates = mapKeys(changed)
		}
		if requiredEditSnippetPresent(root, candidates, snippet.Content, contentCache) {
			continue
		}
		missing = append(missing, requiredEditSnippetName(snippet, i))
	}
	return missing
}

func (r *Runner) unreportedRemovedTestDeclarations(reply string, results []workspace.ToolResult) []string {
	removed := r.removedTestDeclarations(results)
	if len(removed) == 0 || replyReportsRemovedTests(reply, removed) {
		return nil
	}
	return removed
}

func (r *Runner) removedTestDeclarations(results []workspace.ToolResult) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}
	var paths []string
	for _, path := range mapKeys(changedPathsFromToolResults(results)) {
		if testSourcePathCanRemoveDeclarations(path) {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"diff", "--unified=0", "--"}, paths...)
	diff, err := gitCombinedOutput(context.Background(), root, args...)
	if err != nil || strings.TrimSpace(diff) == "" {
		return nil
	}
	return removedTestDeclarationsFromDiff(diff)
}

func testSourcePathCanRemoveDeclarations(path string) bool {
	if !sourcePathNeedsSemanticVerification(path) {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	base := filepath.Base(lower)
	return strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(base, "test") ||
		strings.Contains(base, "spec")
}

func removedTestDeclarationsFromDiff(diff string) []string {
	type testDecl struct {
		display string
		count   int
	}
	removed := make(map[string]testDecl)
	added := make(map[string]int)
	for _, rawLine := range strings.Split(diff, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		switch {
		case strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+++"):
			continue
		case strings.HasPrefix(line, "-"):
			key, display, ok := testDeclarationFromLine(strings.TrimSpace(strings.TrimPrefix(line, "-")))
			if !ok {
				continue
			}
			entry := removed[key]
			entry.display = display
			entry.count++
			removed[key] = entry
		case strings.HasPrefix(line, "+"):
			key, _, ok := testDeclarationFromLine(strings.TrimSpace(strings.TrimPrefix(line, "+")))
			if ok {
				added[key]++
			}
		}
	}
	var missing []string
	for key, entry := range removed {
		if added[key] >= entry.count {
			continue
		}
		missing = append(missing, entry.display)
	}
	slices.Sort(missing)
	return missing
}

var (
	cppCatchTestCasePattern = regexp.MustCompile(`\b(TEST_CASE|SCENARIO)\s*\(\s*"([^"]+)"`)
	cppGTestPattern         = regexp.MustCompile(`\b(?:TEST|TEST_F|TEST_P)\s*\(\s*([A-Za-z_][\w:]*|"[^"]+")\s*,\s*([A-Za-z_][\w:]*|"[^"]+")`)
	goTestFuncPattern       = regexp.MustCompile(`^func\s+(Test[A-Za-z0-9_]+)\s*\(`)
	pythonTestFuncPattern   = regexp.MustCompile(`^def\s+(test_[A-Za-z0-9_]+)\s*\(`)
	jsTestCallPattern       = regexp.MustCompile(`\b(?:describe|it|test)\s*\(\s*['"]([^'"]+)['"]`)
	rustTestFuncPattern     = regexp.MustCompile(`^fn\s+(test_[A-Za-z0-9_]+)\s*\(`)
)

func testDeclarationFromLine(line string) (key string, display string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	if match := cppCatchTestCasePattern.FindStringSubmatch(line); len(match) == 3 {
		return "cpp:" + strings.ToLower(match[1]+":"+match[2]), match[1] + ` "` + match[2] + `"`, true
	}
	if match := cppGTestPattern.FindStringSubmatch(line); len(match) == 3 {
		suite := strings.Trim(match[1], `"`)
		name := strings.Trim(match[2], `"`)
		return "gtest:" + strings.ToLower(suite+"."+name), "TEST " + suite + "." + name, true
	}
	if match := goTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "go:" + strings.ToLower(match[1]), "func " + match[1], true
	}
	if match := pythonTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "python:" + strings.ToLower(match[1]), "def " + match[1], true
	}
	if match := jsTestCallPattern.FindStringSubmatch(line); len(match) == 2 {
		return "js:" + strings.ToLower(match[1]), `test "` + match[1] + `"`, true
	}
	if match := rustTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "rust:" + strings.ToLower(match[1]), "fn " + match[1], true
	}
	return "", "", false
}

func replyReportsRemovedTests(reply string, removed []string) bool {
	lower := strings.ToLower(reply)
	if !containsAny(lower, "removed test", "deleted test", "dropped test", "removed existing test", "test removal", "removed tests", "deleted tests", "dropped tests") {
		return false
	}
	for _, declaration := range removed {
		if !replyMentionsRemovedTestDeclaration(lower, declaration) {
			return false
		}
	}
	return true
}

func replyMentionsRemovedTestDeclaration(lowerReply string, declaration string) bool {
	for _, term := range removedTestDeclarationTerms(declaration) {
		if strings.Contains(lowerReply, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func removedTestDeclarationTerms(declaration string) []string {
	declaration = strings.TrimSpace(declaration)
	if declaration == "" {
		return nil
	}
	terms := []string{declaration}
	if start := strings.Index(declaration, `"`); start >= 0 {
		if end := strings.LastIndex(declaration, `"`); end > start {
			terms = append(terms, declaration[start+1:end])
		}
	}
	fields := strings.Fields(declaration)
	if len(fields) > 1 {
		terms = append(terms, fields[len(fields)-1])
	}
	return terms
}

func requiredEditSnippetCandidatePaths(snippet workerruntime.RequiredEditSnippet, required []string, changed map[string]struct{}) []string {
	if path := normalizeRunnerPath(snippet.Path); path != "" {
		return []string{path}
	}
	var candidates []string
	seen := make(map[string]struct{})
	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if len(changed) > 0 {
			if _, ok := changed[normalized]; !ok {
				continue
			}
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}
	slices.Sort(candidates)
	return candidates
}

func requiredEditSnippetPresent(root string, candidates []string, snippet string, contentCache map[string]string) bool {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return true
	}
	for _, candidate := range candidates {
		path := normalizeRunnerPath(candidate)
		if path == "" || filepath.IsAbs(path) {
			continue
		}
		content, ok := contentCache[path]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				continue
			}
			content = string(data)
			contentCache[path] = content
		}
		if editSnippetMatchesContent(content, snippet) {
			return true
		}
	}
	return false
}

func editSnippetMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")
	if snippet == "" {
		return true
	}
	if strings.Contains(content, snippet) {
		return true
	}
	snippetLines := nonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}
	contentLines := nonEmptyTrimmedLines(content)
	next := 0
	for _, line := range contentLines {
		if line != snippetLines[next] {
			continue
		}
		next++
		if next == len(snippetLines) {
			return true
		}
	}
	return false
}

func nonEmptyTrimmedLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func requiredEditSnippetName(snippet workerruntime.RequiredEditSnippet, index int) string {
	if id := strings.TrimSpace(snippet.ID); id != "" {
		return id
	}
	if path := strings.TrimSpace(snippet.Path); path != "" {
		return fmt.Sprintf("required_edit_%d (%s)", index+1, path)
	}
	return fmt.Sprintf("required_edit_%d", index+1)
}

func claimedChangedPathsFromAnswer(reply string) []string {
	var paths []string
	seen := make(map[string]struct{})
	inChanges := false
	linesRemaining := 0
	pathsSeenInSection := false
	for _, rawLine := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if changedPathsSectionHeader(lower) {
			inChanges = true
			linesRemaining = 14
			pathsSeenInSection = false
		} else if inChanges {
			if linesRemaining <= 0 || changedPathsSectionStop(lower, pathsSeenInSection) {
				inChanges = false
				linesRemaining = 0
				pathsSeenInSection = false
			} else {
				linesRemaining--
			}
		}
		if !inChanges {
			continue
		}
		lineCandidates := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
		for _, fragment := range pathparse.QuotedFragments(line) {
			if normalized, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				lineCandidates = append(lineCandidates, normalized)
			}
		}
		for _, path := range lineCandidates {
			path = normalizeRunnerPath(path)
			if path == "" {
				continue
			}
			pathsSeenInSection = true
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func changedPathsSectionHeader(lower string) bool {
	lower = strings.Trim(strings.TrimSpace(lower), "*#:")
	lower = strings.TrimSpace(lower)
	return strings.Contains(lower, "changed paths") ||
		strings.Contains(lower, "changed files") ||
		strings.Contains(lower, "files changed") ||
		strings.Contains(lower, "modified files") ||
		strings.Contains(lower, "changes made")
}

func changedPathsSectionStop(lower string, pathsSeen bool) bool {
	if lower == "" {
		return false
	}
	trimmed := strings.TrimSpace(lower)
	label := strings.TrimSpace(strings.TrimLeft(trimmed, "-* "))
	if pathsSeen && changedPathsSectionStatusLabel(label) {
		return true
	}
	headingLike := strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "**") ||
		strings.HasSuffix(trimmed, ":")
	if !headingLike {
		return false
	}
	if !pathsSeen && changedPathsSectionHeader(lower) {
		return false
	}
	return containsAny(lower, "verification", "test", "planner", "blocker", "validation", "lane sanity")
}

func changedPathsSectionStatusLabel(label string) bool {
	label = strings.Trim(strings.TrimSpace(label), "`")
	return strings.HasPrefix(label, "verification:") ||
		strings.HasPrefix(label, "validation:") ||
		strings.HasPrefix(label, "planner-owned ") ||
		strings.HasPrefix(label, "planner owned ") ||
		strings.HasPrefix(label, "blocker:") ||
		strings.HasPrefix(label, "lane sanity")
}

func changedPathsFromToolResult(result workspace.ToolResult) []string {
	switch strings.TrimSpace(result.Tool) {
	case "edit_file", "write_file", "append_file", "mkdir":
		return changedPathFromSummary(result.Summary)
	case "go_move_declarations":
		return changedGoMovePathsFromSummary(result.Summary)
	case "apply_patch":
		return changedPathsFromPatchOutput(result.Output)
	case "delegate_edit":
		// wrapDelegateEditResult preserves the underlying edit_file /
		// apply_patch summary so we can parse it the same way. Without
		// this case the planner's required_edit_paths_progress would
		// never tick paths off after a delegate_edit success.
		if paths := changedPathFromSummary(result.Summary); len(paths) > 0 {
			return paths
		}
		return changedPathsFromPatchOutput(result.Output)
	default:
		return nil
	}
}

func changedPathsFromPatchOutput(output string) []string {
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "add "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "add ")))
		case strings.HasPrefix(line, "delete "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "delete ")))
		case strings.HasPrefix(line, "update+move "):
			rest := strings.TrimPrefix(line, "update+move ")
			source, destination, moved := strings.Cut(rest, " -> ")
			if moved {
				paths = append(paths, patchOutputPath(source), patchOutputPath(destination))
			} else {
				paths = append(paths, patchOutputPath(rest))
			}
		case strings.HasPrefix(line, "update "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "update ")))
		}
	}
	return paths
}

func patchOutputPath(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, " ("); ok {
		value = before
	}
	return strings.TrimSpace(value)
}

func changedPathFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) < 2 {
		return nil
	}
	switch fields[0] {
	case "Edited", "Wrote", "Appended":
		return []string{fields[1]}
	case "Created":
		if len(fields) >= 3 && fields[1] == "directory" {
			return []string{fields[2]}
		}
	}
	return nil
}

func changedGoMovePathsFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) == 0 || fields[0] != "Moved" {
		return nil
	}
	var paths []string
	for i, field := range fields {
		switch field {
		case "from", "to":
			if i+1 < len(fields) {
				paths = append(paths, fields[i+1])
			}
		}
	}
	return paths
}

func normalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func isWriteTool(req workspace.ToolRequest) bool {
	return workspace.IsWorkspaceWriteTool(req.Tool)
}

func isVerificationRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "source_sanity" {
		return true
	}
	if isDiagnosticsVerificationTool(tool) {
		return true
	}
	switch tool {
	case "shell":
		return isVerificationCommandText(req.Command)
	}
	return false
}

func hasPendingVerificationAfterWrites(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	if verificationContractSkipsGate(brief.VerificationContract) {
		return false
	}
	docLane := briefIsDocDeliverableLane(brief)
	pending := false
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if workspace.IsWorkspaceWriteTool(result.Tool) {
			// Doc-deliverable lanes (refine drafts/reviews/patches, design
			// notes, etc.) have RequiredEditPaths populated with .md/.txt
			// targets. For those there's no source-aware sanity to run, so
			// non-source writes don't set the pending flag. Implementation
			// lanes that touch a markdown file still verify — patch hygiene
			// (`git diff --check`) IS available and satisfies the gate. A
			// SUBSEQUENT source write still sets pending — mixed atoms
			// require source-aware sanity regardless of lane shape.
			if writeTouchesOnlyNonSourcePaths(result) || (docLane && mkdirTouchesOnlyRequiredPathParents(result, brief.RequiredEditPaths)) {
				continue
			}
			pending = true
			continue
		}
		if pending && resultCountsAsSuccessfulVerification(result) {
			pending = false
		}
	}
	return pending
}

// briefIsDocDeliverableLane reports whether the brief specs a doc-only
// deliverable (refine draft/review/patch, design note write-up, etc.).
// The signal is "every RequiredEditPath is a non-source file". Empty
// RequiredEditPaths is treated as not-doc-only so implementation lanes
// with no specced allowlist still trip the verification gate when they
// happen to touch a markdown file.
func briefIsDocDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}
	for _, p := range brief.RequiredEditPaths {
		if !writePathIsNonSource(p) {
			return false
		}
	}
	return true
}

func briefIsScratchpadDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefIsDocDeliverableLane(brief) {
		return false
	}
	for _, p := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(p)
		if !strings.HasPrefix(normalized, ".idiom/refine/") {
			return false
		}
	}
	return true
}

func scopedImplementationUsesBoundedSourceEdits(brief workerruntime.WorkerRuntimeBrief, paths []string) bool {
	if !implementationScopedEditSurfaceActive(brief, nil) || briefIsDocDeliverableLane(brief) {
		return false
	}
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" || writePathIsNonSource(normalized) {
			continue
		}
		if recoverySearchPathLooksEditableSource(normalized) {
			return true
		}
	}
	return false
}

// writeTouchesOnlyNonSourcePaths returns true when every path in a
// write result is a non-source file (markdown, plain text). Used to
// exempt doc-deliverable lanes from the post-write verification gate.
func writeTouchesOnlyNonSourcePaths(result workspace.ToolResult) bool {
	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !writePathIsNonSource(p) {
			return false
		}
	}
	return true
}

func mkdirTouchesOnlyRequiredPathParents(result workspace.ToolResult, requiredPaths []string) bool {
	if strings.TrimSpace(result.Tool) != "mkdir" || len(requiredPaths) == 0 {
		return false
	}
	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !mkdirPathIsRequiredParent(path, requiredPaths) {
			return false
		}
	}
	return true
}

func mkdirPathIsRequiredParent(path string, requiredPaths []string) bool {
	path = strings.Trim(normalizeRunnerPath(path), "/")
	if path == "" {
		return false
	}
	for _, required := range requiredPaths {
		required = strings.Trim(normalizeRunnerPath(required), "/")
		if required == "" || !writePathIsNonSource(required) {
			continue
		}
		parent := strings.Trim(filepath.ToSlash(filepath.Dir(required)), "/")
		if path == parent || strings.HasPrefix(parent, path+"/") {
			return true
		}
	}
	return false
}

// writePathIsNonSource returns true for paths whose verification has
// no source-aware analog. Conservative list — adding extensions risks
// silently bypassing real verification gates.
func writePathIsNonSource(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}
	return strings.HasSuffix(p, ".md") ||
		strings.HasSuffix(p, ".markdown") ||
		strings.HasSuffix(p, ".txt") ||
		strings.HasSuffix(p, ".rst")
}

func (r *Runner) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {
	trimmed := strings.TrimSpace(reply.Content)
	if trimmed != "" {
		if looksLikeTextualToolCall(trimmed) {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
			return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
		}
		r.LastAssistantMessage = reply
		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: emptyAnswerRetryPrompt(r.RuntimeBrief),
	})

	if metrics != nil {
		metrics.ModelCalls++
	}
	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)
	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(retryMessages, textOnlyOpts)
	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}
	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}
	r.emitModelCallTraceWithOptions(0, "empty_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)
	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, textOnlyOpts, false)
	if len(retryMessage.ToolCalls) > 0 {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}
	if looksLikeTextualToolCall(retryReply) {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
	}
	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}
		return "", fmt.Errorf("%s agent returned an empty response", r.agentName())
	}
	r.LastAssistantMessage = retryMessage
	return retryTrimmed, nil
}

func extractRunnerFilePath(summary string) string {
	path := extractReadFilePath(summary)
	if path == "" {
		return ""
	}
	fields := strings.Fields(path)
	if len(fields) >= 2 && looksLikeRunnerFileHandle(fields[0]) {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return path
}

func extractSemanticInspectionPath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	for _, marker := range []string{" lines ", ". Use "} {
		idx := strings.Index(trimmed, marker)
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			break
		}
	}
	for _, prefix := range []string{"Read ", "Extracted ", "Outlined "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		for _, separator := range []string{" in ", " from "} {
			idx := strings.LastIndex(trimmed, separator)
			if idx <= len(prefix) {
				continue
			}
			return strings.TrimSpace(trimmed[idx+len(separator):])
		}
	}
	return ""
}

func looksLikeRunnerFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}
	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// rewriteVisibilityNote returns a short system-message body explaining that
// the requested tool was redirected to a different tool by harness routing
// rules. Generic across rewrite kinds (delegated bootstrap, verification
// bootstrap, debug-log preflight, etc.) — it states the fact without
// hard-picking the next tool, leaving the model free to act on the result.
func rewriteVisibilityNote(originalTool, rewrittenTool string) string {
	original := strings.TrimSpace(originalTool)
	rewritten := strings.TrimSpace(rewrittenTool)
	switch {
	case original != "" && rewritten != "" && original != rewritten:
		return fmt.Sprintf("Note: your `%s` call was redirected to `%s` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `%s`.", original, rewritten, original)
	case rewritten != "":
		return fmt.Sprintf("Note: your tool call was redirected to `%s` by lane routing. The rewritten call will execute and return its result.", rewritten)
	default:
		return "Note: your tool call was rewritten by lane routing. The rewritten call will execute and return its result."
	}
}

// taskLedgerClosureHint inspects the collected tool-results set and, if the
// most recent task_ledger state still has any items flagged in_progress,
// returns a short reminder asking the model to close them with a final
// status in the upcoming reply. Returns the empty string when there is
// nothing open.
func taskLedgerClosureHint(results []workspace.ToolResult) string {
	var latest *workspace.ToolResult
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Tool == "task_ledger" && strings.TrimSpace(results[i].Error) == "" {
			copyResult := results[i]
			latest = &copyResult
			break
		}
	}
	if latest == nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(latest.Output), "in_progress") {
		return ""
	}
	var openItems []string
	for _, line := range strings.Split(latest.Output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "in_progress") {
			openItems = append(openItems, strings.TrimSpace(line))
		}
	}
	if len(openItems) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Task ledger has open items you never closed. Resolve them in your reply (state each as completed / blocked / cancelled with one short evidence sentence):\n")
	for _, item := range openItems {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// toolFailurePathStem returns the first two path segments of the request's
// target path (e.g. "internal/llm" for "internal/llm/llm.go"). Used by the
// same-stem failure counter so that five different hallucinated filenames
// inside the same made-up directory count as repeated failures, not fresh
// attempts.
func toolFailurePathStem(req workspace.ToolRequest) string {
	raw := strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// recoveryNudgeInputs bundles the rejection-recovery-nudge state so
// each rule can be unit-tested in isolation without a 7-arg signature.
type recoveryNudgeInputs struct {
	req                          workspace.ToolRequest
	results                      []workspace.ToolResult
	mgr                          *workspace.Manager
	brief                        workerruntime.WorkerRuntimeBrief
	agent                        string
	delegatedReadOnlyAnswerReady bool
	postChildPacketActive        bool
}

// recoveryNudgeRule is a priority-ordered rejection-recovery nudge.
// Returns (nudge, true) when fired or ("", false) when the rule does
// not apply. The dispatcher tries rules in order; first match wins.
type recoveryNudgeRule func(in recoveryNudgeInputs) (string, bool)

// recoveryNudgeRules lists each rule in priority order. Order is
// load-bearing: a rule that fires returns before later rules get a
// chance, so insertion position matters. When adding a new rule,
// place it at the priority that matches the scenario it handles.
//
//  1. verification-only shell (read-only lanes need to run the check)
//  2. exact-anchor pre-write (write-ready packet, no more discovery)
//  3. missing required-edit paths (allowlist not yet covered)
//  4. pending verification after writes (writes need sanity)
//  5. debug-log investigation (deepen or pivot inside the same log)
//  6. closure ready (delegated/child packet justifies answer)
//  7. premature evidence-first target read (need non-target evidence)
//  8. evidence-first target inspection (keep follow-up reads target-scoped)
//
// When every priority rule declines, lastChanceRecoveryNudge produces
// the tool-shape-specific default.
var recoveryNudgeRules = []recoveryNudgeRule{
	verificationOnlyShellRecoveryNudge,
	exactAnchorPreWriteRecoveryNudge,
	missingRequiredEditPathsRecoveryNudge,
	pendingVerificationAfterWritesRecoveryNudge,
	debugLogInvestigationRecoveryNudge,
	prematureEvidenceFirstTargetRecoveryNudge,
	evidenceFirstTargetInspectionRecoveryNudge,
}

// buildRejectionRecoveryNudge gives the model one last closure-biased repair
// after repeated rejected tool calls. It states the contract and avoids
// prescribing a copy-paste tool sequence unless a hard invariant requires it.
// Behavior is decomposed into priority-ordered rules (recoveryNudgeRules);
// the first rule that fires returns its nudge. If none fire, the
// tool-shape-specific lastChanceRecoveryNudge always produces a default.
func buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult, mgr *workspace.Manager, brief workerruntime.WorkerRuntimeBrief, agent string, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) string {
	// Conversation lanes: the model owns readiness. Do not inject
	// nudges that declare evidence is sufficient or that the harness
	// will end the turn. The force-completion path is also gated in
	// rejectTool; this keeps the nudge text consistent.
	if LanePolicyForBrief(brief).ShouldSkipRecoveryNudge() {
		return ""
	}
	in := recoveryNudgeInputs{
		req:                          req,
		results:                      results,
		mgr:                          mgr,
		brief:                        brief,
		agent:                        agent,
		delegatedReadOnlyAnswerReady: delegatedReadOnlyAnswerReady,
		postChildPacketActive:        postChildPacketActive,
	}
	for _, rule := range recoveryNudgeRules {
		if nudge, ok := rule(in); ok {
			return nudge
		}
	}
	return lastChanceRecoveryNudge(in)
}

func verificationOnlyShellRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(in.brief.PermissionProfile) {
		return "", false
	}
	cmd := "go test ./..."
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}
	var b strings.Builder
	b.WriteString("This is a verification-only turn; the requested check has not yet run. Run it (the configured plan starts with `")
	b.WriteString(cmd)
	b.WriteString("`), then answer with the observed pass/fail. If the check has already run, answer with that result.\n")
	return b.String(), true
}

func exactAnchorPreWriteRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !exactAnchorPreWriteSurfaceActive(in.brief, in.results) {
		return "", false
	}
	target := "the supplied exact-anchor target"
	if paths := nonEmptyRunnerStrings(in.brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(in.brief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}
	var b strings.Builder
	b.WriteString("This is an exact-anchor implementation pre-write turn. The target packet is the current-state path and source evidence for the first write; do not inspect, search, shell, or verify before that write.\n")
	b.WriteString("Exact-anchor write capability is required for ")
	b.WriteString(target)
	b.WriteString(". If the supplied anchor is missing or stale, answer with <<QUESTION>> naming that exact anchor gap; no additional discovery is allowed in this lane.\n")
	return b.String(), true
}

func missingRequiredEditPathsRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !runnerBriefRequiresMutation(in.brief) {
		return "", false
	}
	missing := missingRequiredEditPaths(in.brief.RequiredEditPaths, changedPathsFromToolResults(in.results))
	if len(missing) == 0 {
		return "", false
	}
	var b strings.Builder
	if strings.TrimSpace(in.brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		b.WriteString("Recovery nudge — this plan-execute planner turn still has required edit path(s) without successful `submit_plan` coverage: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(".\n")
		if planThenExecuteSubmitCorrectionActive(in.brief, in.results) {
			b.WriteString("Do not answer with partial findings. Submit-correction mode has exactly one valid outcome: parser-readable `submit_plan`. ")
			b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
			b.WriteString(" Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not repeat the rejected tool call.\n")
			return b.String(), true
		}
		b.WriteString("Do not answer with partial findings. Required outcomes are `submit_plan` (")
		b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
		b.WriteString("), `Decision: source-satisfied` with current-state evidence for every remaining path, or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")
		return b.String(), true
	}
	b.WriteString("Recovery nudge — this implementation turn still has required edit path(s) without a successful write: ")
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".\n")
	if scopedImplementationUsesBoundedSourceEdits(in.brief, missing) {
		b.WriteString("Do not answer with partial findings. For existing source/test required paths, the allowed write outcome is a bounded `edit_file` call using old_text/new_text or start_line/end_line/content, or an `apply_patch` envelope. Do not use `write_file` or `append_file` for existing source/test paths; reserve whole-file writes for new files, small files, and non-source deliverables. Otherwise answer with <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")
		return b.String(), true
	}
	b.WriteString("Do not answer with partial findings. Required outcomes are an allowed write or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")
	return b.String(), true
}

func pendingVerificationAfterWritesRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if failed, ok := failedVerificationAfterLatestWrite(in.results, in.brief); ok {
		return postWriteVerificationFailedRecoveryPrompt(failed, plannerOwnsVerification(in.brief)) + "\n", true
	}
	if !hasPendingVerificationAfterWrites(in.results, in.brief) {
		return "", false
	}
	cmd := "git diff --check"
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}
	var b strings.Builder
	if plannerOwnsVerification(in.brief) {
		b.WriteString("This implementation turn has successful writes that are still pending lane sanity. Do not inspect or edit again yet. Build/test verification is planner-owned for this route, so required evidence is compiler-backed source-sanity or language diagnostics for changed source files; for non-source edits one cheap non-build sanity command such as `")
		b.WriteString(cmd)
		b.WriteString("`. For source edits, `git diff --check` and generic source-sanity fallback are only patch hygiene. If source sanity is forbidden or impossible, answer with the changed paths and the concrete lane-sanity blocker.\n")
		return b.String(), true
	}
	b.WriteString("This implementation turn has successful writes that are still pending verification. Do not inspect or edit again yet. Required evidence is one focused language/project verification command or compiler-backed diagnostics. If a verification plan exists, start with `")
	b.WriteString(cmd)
	b.WriteString("`. If verification is forbidden or impossible, answer with the changed paths and the concrete verification blocker.\n")
	return b.String(), true
}

func debugLogInvestigationRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if strings.TrimSpace(in.brief.TaskFamily) != "debug_logs" {
		return "", false
	}
	selection, ok := latestDebugLogSelection(in.results)
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString("This is a debug-log investigation. Stay on real log artifacts.\n")
	if shouldDeepenDebugLogSearch(in.results, selection) {
		if next, nextOK := nextDebugLogSelection(in.results, selection); nextOK {
			b.WriteString("The current selected log did not yield a concrete hit. Deepen to the next newest log now:\n")
			b.WriteString("  ")
			b.WriteString(marshalToolRequestRaw(debugLogTailRequest(next)))
			b.WriteString("\n")
			b.WriteString("If that next log is still not useful, answer with the current uncertainty and name the missing symptom.\n")
			return b.String(), true
		}
	}
	b.WriteString("Answer from the selected log if it is already clear.\n")
	if redirected, redirectedOK := debugLogScopedSearchRedirect(in.req, selection); redirectedOK {
		b.WriteString("Or search inside the same selected log with your current pattern:\n")
		b.WriteString("  ")
		b.WriteString(marshalToolRequestRaw(redirected))
		b.WriteString("\n")
	}
	return b.String(), true
}

func prematureEvidenceFirstTargetRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !isPrematureEvidenceFirstTargetRead(in.req, in.results, in.brief) {
		return "", false
	}
	var b strings.Builder
	b.WriteString("This turn still needs source evidence before the named target file.\n")
	candidates := nonTargetRecoveryFileCandidates(in.results, in.brief.TargetRefs)
	if len(candidates) > 0 {
		b.WriteString("Candidate source paths:\n")
		for _, path := range candidates {
			b.WriteString("  - ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("Use a non-target implementation file or search result that can explain the change before returning to the target.\n")
	}
	b.WriteString("\nIf the source evidence is already sufficient in prior results, answer the user directly instead of calling another tool.\n")
	return b.String(), true
}

func evidenceFirstTargetInspectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		len(changedPathsFromToolResults(results)) == 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		hasSuccessfulTargetRead(results, brief.TargetRefs)
}

func evidenceFirstTargetInspectionRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !evidenceFirstTargetInspectionActive(in.brief, in.results) {
		return "", false
	}
	target := util.FirstNonEmpty(in.brief.TargetRefs...)
	if target == "" {
		target = strings.TrimSpace(in.req.Path)
	}
	var b strings.Builder
	b.WriteString("This turn already has source evidence and target-file evidence.\n")
	b.WriteString("Either edit the target now, or if one exact detail is still missing, stay inside the named target with a named, narrow read or search.\n")
	if target != "" {
		start, end := 1, 40
		if window, ok := latestSuccessfulTargetReadWindow(in.results, in.brief.TargetRefs); ok {
			start = window.End - 19
			if start < 1 {
				start = 1
			}
			end = window.End
		}
		b.WriteString("Relevant target window: ")
		b.WriteString(target)
		b.WriteString(fmt.Sprintf(" lines %d-%d\n", start, end))
	}
	b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")
	return b.String(), true
}

// lastChanceRecoveryNudge is the always-fires terminator. It produces
// a tool-shape-specific default when no priority rule fired.
func lastChanceRecoveryNudge(in recoveryNudgeInputs) string {
	tool := strings.TrimSpace(in.req.Tool)
	var b strings.Builder
	b.WriteString("Recovery nudge — last chance before the harness ends the turn. Answer with the evidence already gathered. Only take one narrow next step if you can name the exact missing claim it closes. Do not repeat the rejected tool call.\n\n")
	switch {
	case tool == "list_files" || tool == "list_dir":
		candidates := rejectionRecoveryFileCandidates(in.results)
		if len(candidates) > 0 {
			b.WriteString("You already have a workspace overview. Choose one discovered path that can close a named gap:\n")
			for _, path := range candidates {
				b.WriteString("  - ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		} else {
			b.WriteString("You already have a workspace overview. Either answer now or take one focused search/read step tied to a named gap.\n")
		}
	case tool == "read_file":
		path := strings.TrimSpace(in.req.Path)
		if path != "" {
			b.WriteString("The repeated read should become a smaller window or a structured probe tied to a named gap on ")
			b.WriteString(path)
			b.WriteString(".\n")
		} else {
			b.WriteString("The repeated read needs a concrete discovered path and a named gap.\n")
		}
	case tool == "search_content":
		b.WriteString("A follow-up search should change the claim being tested or narrow to a specific discovered path.\n")
		if strings.TrimSpace(in.req.Path) != "" {
			b.WriteString("Current scope: ")
			b.WriteString(in.req.Path)
			b.WriteString("\n")
		}
	default:
		b.WriteString("Pick a materially different move that names the exact missing claim it closes.\n")
	}
	b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")
	return b.String()
}

func isPrematureEvidenceFirstTargetRead(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		strings.TrimSpace(req.Tool) == "read_file" &&
		slices.Contains(brief.TargetRefs, strings.TrimSpace(req.Path)) &&
		!hasFocusedNonTargetEvidence(results, brief.TargetRefs)
}

func nonTargetRecoveryFileCandidates(results []workspace.ToolResult, targetRefs []string) []string {
	raw := rejectionRecoveryFileCandidates(results)
	filtered := make([]string, 0, len(raw))
	for _, path := range raw {
		if slices.Contains(targetRefs, path) {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

// rejectionRecoveryFileCandidates pulls up to 3 plausible files the model
// can read_file next, harvested from whatever listing results it already
// collected this turn. Preference: .go files in internal/, then anything
// .go, then any discovered path.
func rejectionRecoveryFileCandidates(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	internalGo := make([]string, 0, 3)
	anyGo := make([]string, 0, 3)
	anything := make([]string, 0, 3)
	for _, r := range results {
		for _, line := range strings.Split(r.Output, "\n") {
			path := recoveryCandidatePath(line)
			if path == "" || strings.HasPrefix(path, "/") {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			switch {
			case strings.HasSuffix(path, ".go") && strings.HasPrefix(path, "internal/"):
				if len(internalGo) < 3 {
					internalGo = append(internalGo, path)
				}
			case strings.HasSuffix(path, ".go"):
				if len(anyGo) < 3 {
					anyGo = append(anyGo, path)
				}
			default:
				if len(anything) < 3 {
					anything = append(anything, path)
				}
			}
		}
	}
	if len(internalGo) > 0 {
		return internalGo
	}
	if len(anyGo) > 0 {
		return anyGo
	}
	return anything
}

func recoveryCandidatePath(line string) string {
	path := strings.TrimSpace(line)
	path = strings.TrimPrefix(path, "- ")
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if colon := strings.IndexByte(path, ':'); colon > 0 {
		prefix := strings.TrimSpace(path[:colon])
		if strings.Contains(prefix, "/") || strings.Contains(prefix, ".") {
			path = prefix
		}
	}
	if strings.Contains(path, " ") {
		return ""
	}
	return path
}

func compactOutput(output string, maxLines int) string {
	output = stripFallbackJSONArtifacts(output)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

// sanitizeFallbackResultOutput returns a tool-result Output that's safe to
// embed under "Verified context" in a forced-completion fallback. Some
// tools produce Output that is fine for the model's tool-call loop but
// reads as noise in the final user-facing answer:
//
//   - search_content matches against test fixtures often span multi-line
//     quoted Go strings containing JSON, so the per-line strip can't
//     reach the JSON cleanly. The count summary ("Found N matching
//     lines.") is sufficient context for synthesis; drop the raw output.
//   - read_file and symbol-read outputs are kept — those are the actual
//     evidence the model needs to summarize.
//
// Generic across languages: applies to *any* tool whose output is a
// long quoted-string match list.
func sanitizeFallbackResultOutput(result workspace.ToolResult) string {
	switch strings.TrimSpace(result.Tool) {
	case "search_content", "search_files":
		return ""
	default:
		return result.Output
	}
}

// stripFallbackJSONArtifacts removes JSON-shaped object dumps from a tool
// result Output before it gets surfaced under "Verified context" in the
// forced-completion fallback. When a child lane's task result Output
// includes a discovery_query summary, the artifact metadata JSON often
// follows verbatim ({"id": "...", "purpose": "...", "layers_run": [...]
// }) and bleeds into the user-facing answer as opaque blocks. The
// fallback should show prose bullets; metadata blobs belong in the
// artifact file, not the answer. Generic across artifact JSON — any
// brace-delimited object whose first content line starts with a quoted
// key is treated as a metadata dump and dropped.

// conversationLaneWithoutSemanticEval reports true when the lane has no
// deterministic final-answer relevance contract. Read-only direct-answer
// lanes now have final_answer_acceptance.v1, so they no longer fall into
// that bucket when the objective is explicit enough to check.
func conversationLaneWithoutSemanticEval(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !readOnlyDirectAnswerTaskFamily(brief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return false
	}
	return !readOnlyDirectAnswerLane(brief) || strings.TrimSpace(brief.Objective) == ""
}

// emptyAnswerRetryPrompt builds the retry system message when the model
// produces an empty answer. The prompt re-anchors to the user's original
// objective so the model reorients rather than repeating its mistake.
func emptyAnswerRetryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	base := "Your previous response was empty. Tools are disabled now — do NOT emit a tool block. "
	if obj := strings.TrimSpace(brief.Objective); obj != "" {
		base += fmt.Sprintf("The user asked: %q. ", obj)
	}
	base += "Reply with a direct, non-empty answer based on the evidence already gathered. Keep it short " +
		"(a few sentences or a brief list). No preamble like \"Based on...\" or \"Here is...\" — just the answer. " +
		"If one exact claim remains unsupported, state that uncertainty after the supported answer in one sentence."
	return base
}

func finishShouldOverrideSourceEcho(answer string) bool {
	if !looksLikeSourceEchoOnlyAnswer(answer) {
		return false
	}
	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "no files were changed") {
		return false
	}
	for _, path := range pathparse.MentionedWorkspacePaths(answer) {
		path = normalizeRunnerPath(path)
		if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "logs/") {
			return true
		}
	}
	return false
}

// cleanToolRequestPathDisplay returns a short display value for a tool
// request path suitable for progress nudges and event logs.
func cleanToolRequestPathDisplay(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "."
	}
	return p
}

// looksLikeNonAnswerProse checks whether the answer text is clearly a
// non-answer (tool-call markup, echoed user question, tool-failure
// language without substantive findings) rather than an actual answer.
// These patterns indicate the model emitted transport-level noise
// instead of addressing the user's request.
func looksLikeNonAnswerProse(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return false
	}
	// Textual tool-call markup like [Calling tool: ...] in answer prose.
	if looksLikeTextualToolCall(answer) {
		return true
	}
	// Echoed user question: the answer starts with "Question:" followed
	// by the user's own words rather than an actual investigation result.
	if strings.HasPrefix(lower, "question:") {
		return true
	}
	if looksLikeMetaIntentRestatement(trimmed) {
		return true
	}
	if looksLikeScratchpadLeakedAnswer(trimmed) {
		return true
	}
	if looksLikeRawReadOnlyEvidencePacketAnswer(trimmed) {
		return true
	}
	if looksLikeLowValueSourceInventoryNonAnswer(trimmed) {
		return true
	}
	if finalAnswerSectionMarkerCount(answer) > 1 {
		return true
	}
	// Raw tool-result transcript with no user-facing prose.
	if looksLikeRawToolResultTranscript(answer) {
		return true
	}
	if looksLikeSourceEchoOnlyAnswer(trimmed) {
		return true
	}
	// Leaked implementation-result templates are protocol artifacts, not
	// direct answers. Do not reject ordinary diagnostic prose just because it
	// discusses a blocker, a failed command, or an exit status.
	if looksLikeLeakedImplementationBlockerTemplate(lower) {
		return true
	}
	return false
}

func looksLikeRawReadOnlyEvidencePacketAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" || strings.Contains(lower, "grounded answer:") {
		return false
	}
	packetMarkers := 0
	for _, marker := range append(delegatedReadOnlyPacketLineLabels(), "source: delegated read-only evidence packet") {
		if strings.Contains(lower, marker) {
			packetMarkers++
		}
	}
	return packetMarkers >= 1
}

func looksLikeLeakedImplementationBlockerTemplate(lowerAnswer string) bool {
	if !strings.HasPrefix(lowerAnswer, "changed paths: none") {
		return false
	}
	return strings.Contains(lowerAnswer, "\nverification: not run") &&
		strings.Contains(lowerAnswer, "\nblocker:")
}

func looksLikeMetaIntentRestatement(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !containsAny(lower,
		" the user is asking me to ",
		" the user asked me to ",
		" the user wants me to ",
		" the user requested me to ",
	) {
		return false
	}
	if containsAny(lower,
		" the answer is ",
		" the likely cause is ",
		" the root cause is ",
		" because ",
		" this means ",
	) {
		return false
	}
	return containsAny(lower,
		" i need to find ",
		" i need to inspect ",
		" i need to search ",
		" i need to read ",
		" i need to check ",
		" i should find ",
		" i should inspect ",
		" i should search ",
		" i should read ",
		" i should check ",
	)
}

func looksLikeScratchpadLeakedAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	for i, line := range strings.Split(trimmed, "\n") {
		lowerLine := strings.ToLower(strings.TrimSpace(line))
		if lowerLine == "" {
			continue
		}
		if lineStartsWithAny(lowerLine,
			"actually, i should",
			"actually, i need",
			"actually, let me",
			"actually, wait",
			"let me analyze",
			"let me inspect",
			"let me check",
			"let me read",
			"let me search",
			"let me verify",
			"let me gather",
			"let me prepare",
			"let me write",
			"let me provide",
			"let me produce",
			"let me think",
			"i should analyze",
			"i should inspect",
			"i should check",
			"i should read",
			"i should search",
			"i should verify",
			"i should gather",
			"i should prepare",
			"i should write",
			"i should provide",
			"i should produce",
			"i need to analyze",
			"i need to inspect",
			"i need to check",
			"i need to read",
			"i need to search",
			"i need to verify",
			"i need to gather",
			"i need to prepare",
			"i need to write",
			"i need to provide",
			"i need to produce",
			"ok, i'm providing",
			"ok, i’m providing",
			"ok, final answer",
			"ok, i'm ready",
			"ok, i’m ready",
			"wait, i",
		) {
			return true
		}
		if i == 0 && lineStartsWithAny(lowerLine, "the user wants", "the user is asking") {
			if containsAny(normalized,
				" actually, ",
				" actually, i ",
				" actually, let me ",
				" let me ",
				" i should ",
				" i need to ",
				" ok, i'm providing ",
				" ok, i’m providing ",
				" ok, final answer ",
				" wait, ",
			) {
				return true
			}
			return !containsAny(normalized,
				" the answer is ",
				" the likely cause is ",
				" the root cause is ",
				" because ",
				" this means ",
			)
		}
	}
	return false
}

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func looksLikeSourceEchoOnlyAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}
	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !looksLikeToolResultTranscript(lower) {
		return false
	}
	claim := normalizeSourceInventoryClaim(stripPresentableSourceObjectiveEcho(trimmed))
	if sourceInventoryClaimLooksSynthesized(claim) ||
		containsAny(lower,
			" should reject ",
			" should change ",
		) {
		return false
	}
	return true
}

// requiredEvidenceCheck maps structurally-verifiable evidence labels to
// a check that returns true when the obligation is met. Labels that
// require semantic evaluation (e.g. answer_addresses_user_request) are
// excluded — the harness cannot evaluate answer quality.
var requiredEvidenceCheck = map[workerruntime.EvidenceRequirementID]func(answer string, results []workspace.ToolResult) bool{
	workerruntime.RequiredEvidenceTargetLocation: func(answer string, results []workspace.ToolResult) bool {
		if len(pathparse.MentionedWorkspacePaths(answer)) > 0 {
			return true
		}
		return len(changedPathsFromToolResults(results)) > 0
	},
	workerruntime.RequiredEvidenceChangedPaths: func(answer string, results []workspace.ToolResult) bool {
		return len(changedPathsFromToolResults(results)) > 0
	},
	workerruntime.RequiredEvidenceChangedPathsOrNoopReason: func(answer string, results []workspace.ToolResult) bool {
		if len(changedPathsFromToolResults(results)) > 0 {
			return true
		}
		lower := strings.ToLower(answer)
		return strings.Contains(lower, "no files were changed") ||
			strings.Contains(lower, "changed paths: none") ||
			strings.Contains(lower, "no changes") ||
			strings.Contains(lower, "noop")
	},
	workerruntime.RequiredEvidenceVerificationStatus: func(answer string, results []workspace.ToolResult) bool {
		lower := strings.ToLower(answer)
		if strings.Contains(lower, "verification:") ||
			strings.Contains(lower, "tests passed") ||
			strings.Contains(lower, "tests failed") ||
			strings.Contains(lower, "not run") ||
			strings.Contains(lower, "not detected") {
			return true
		}
		for _, r := range results {
			if isVerificationToolResult(r) {
				return true
			}
		}
		return false
	},
	workerruntime.RequiredEvidenceVerificationCommand: func(answer string, results []workspace.ToolResult) bool {
		for _, r := range results {
			if isVerificationToolResult(r) {
				return true
			}
		}
		return false
	},
	workerruntime.RequiredEvidencePassFailStatus: func(answer string, results []workspace.ToolResult) bool {
		lower := strings.ToLower(answer)
		return strings.Contains(lower, "pass") || strings.Contains(lower, "fail") ||
			strings.Contains(lower, "error") || strings.Contains(lower, "exit code")
	},
	workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied: func(answer string, results []workspace.ToolResult) bool {
		return successfulSubmitPlanResultCount(results) > 0 ||
			strings.Contains(strings.ToLower(answer), "source-satisfied") ||
			strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>")
	},
	workerruntime.RequiredEvidenceGitHistoryRationale: func(answer string, results []workspace.ToolResult) bool {
		return successfulGitHistoryEvidenceSeen(results) || answerHasGitHistoryNoopOrBlocker(answer)
	},
	workerruntime.RequiredEvidenceInspectedPaths: func(answer string, results []workspace.ToolResult) bool {
		if len(pathparse.MentionedWorkspacePaths(answer)) > 0 {
			return true
		}
		for _, r := range results {
			for _, rel := range r.Relationships {
				if strings.TrimSpace(rel.TargetPath) != "" || strings.TrimSpace(rel.SourcePath) != "" {
					return true
				}
			}
		}
		return false
	},
	workerruntime.RequiredEvidenceExactWorkspacePath: func(answer string, results []workspace.ToolResult) bool {
		return len(pathparse.MentionedWorkspacePaths(answer)) > 0
	},
	workerruntime.RequiredEvidenceArtifactBodyOrTypedBlocker: func(answer string, results []workspace.ToolResult) bool {
		trimmed := strings.TrimSpace(answer)
		return len(trimmed) > 40 || strings.HasPrefix(trimmed, "<<QUESTION>>")
	},
}

// missingRequiredEvidence returns required_evidence labels that are
// structurally verifiable and not satisfied by the answer or results.
// Semantic labels (answer_addresses_user_request, confidence, etc.) are
// skipped — the harness cannot evaluate answer quality deterministically.
func missingRequiredEvidence(evidence []string, answer string, results []workspace.ToolResult) []string {
	var missing []string
	for _, label := range evidence {
		trimmed := strings.TrimSpace(label)
		check, ok := requiredEvidenceCheck[workerruntime.EvidenceRequirementID(trimmed)]
		if !ok {
			// Non-deterministic label — skip (model guidance only)
			continue
		}
		if !check(answer, results) {
			missing = append(missing, trimmed)
		}
	}
	return missing
}

// isVerificationToolResult reports whether a tool result came from a
// verification-oriented tool (source_sanity, shell with test invocation).
func isVerificationToolResult(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(strings.ToLower(result.Tool))
	if tool == "source_sanity" {
		return true
	}
	if tool == "shell" && strings.TrimSpace(result.Error) == "" {
		return true
	}
	return false
}

func stripFallbackJSONArtifacts(output string) string {
	if !strings.Contains(output, "{") {
		return output
	}
	lines := strings.Split(output, "\n")
	out := make([]string, 0, len(lines))
	skipUntilBaseline := false
	skipDepth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipUntilBaseline {
			// Track nested braces. We exit the skip when depth returns
			// to zero, when we see a blank line, or when a new bullet
			// starts (lines beginning with "- " mark the next section).
			skipDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				skipUntilBaseline = false
				skipDepth = 0
			}
			if trimmed == "" || strings.HasPrefix(trimmed, "- ") {
				skipUntilBaseline = false
				skipDepth = 0
				if strings.HasPrefix(trimmed, "- ") {
					out = append(out, line)
				}
			}
			continue
		}
		// A bare "{" line, or a "{...}" JSON one-liner whose first
		// inner content is a quoted key, marks the start of a metadata
		// dump. Drop the block.
		if trimmed == "{" || (strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\":")) {
			skipUntilBaseline = true
			skipDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				// One-liner JSON; consume just this line.
				skipUntilBaseline = false
				skipDepth = 0
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
