package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"idiom/internal/capabilities/workspace"
	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/runtime/instructions"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type BudgetState struct {
	StepBudget    int
	StepsUsed     int
	ReadCalls     int
	WriteCalls    int
	NoProgress    int
	RejectedCount int
}

type ExecutionState struct {
	NeedsNarrowing bool
	BroadInspects  int
}

type MetricsSnapshot struct {
	ModelCalls       int
	RejectedActions  int
	ProgressTurns    int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type StateStore interface {
	RecordResult(workspace.ToolRequest, workspace.ToolResult)
	Results() []workspace.ToolResult
	LastResult() (workspace.ToolRequest, workspace.ToolResult, bool)
	ResetResults()
	BudgetState() BudgetState
	SetStepBudget(int)
	IncrementSteps()
	IncrementReads()
	IncrementWrites()
	AddRejected()
	ComputeFingerprint(workspace.ToolRequest) string
	TrackFingerprint(string, workspace.ToolResult)
	ExecutionState() ExecutionState
	SetAllowedTools([]string)
	AllowedTools() []string
	SetRequiredEditPaths([]string)
	ChangedPaths() []string
	RequiredEditsRemaining() []string
	TrackWritePath(string)
	WriteDecision(string) WriteDecision
	GateRejectionCount() int
	IncrementGateRejections()
	ResetGateRejections()
	ProseRetriesRemaining() int
	SetProseRetries(int)
	DecrementProseRetries() int
	LastGateError() string
	SetLastGateError(string)
	Metrics() MetricsSnapshot
	AddModelCall()
	AddUsage(llama.Usage)
	AddProgressTurn()
}

type stateStore struct {
	results []workspace.ToolResult
	lastReq workspace.ToolRequest
	lastRes workspace.ToolResult
	hasLast bool

	stepBudget    int
	stepsUsed     int
	readCalls     int
	writeCalls    int
	noProgress    int
	rejectedCount int

	execNeedsNarrowing bool
	execBroadInspects  int

	allowedTools      []string
	requiredEditPaths []string
	changedPaths      map[string]struct{}
	fingerprints      map[string]workspace.ToolResult

	gateRejections int
	proseRetries   int
	lastGateError  string

	modelCalls       int
	progressTurns    int
	promptTokens     int
	completionTokens int
	totalTokens      int
}

func newStateStore(seedResults []workspace.ToolResult, needsNarrowing bool, broadInspects int) StateStore {
	return &stateStore{
		results:            append([]workspace.ToolResult(nil), seedResults...),
		execNeedsNarrowing: needsNarrowing,
		execBroadInspects:  broadInspects,
		changedPaths:       map[string]struct{}{},
		fingerprints:       map[string]workspace.ToolResult{},
	}
}

func (s *stateStore) RecordResult(req workspace.ToolRequest, result workspace.ToolResult) {
	if s == nil {
		return
	}
	s.results = append(s.results, result)
	s.lastReq, s.lastRes, s.hasLast = req, result, true
	if workspaceWriteResultCompleted(req, result) {
		paths := newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathList()
		if len(paths) == 0 {
			paths = writeRequestPaths(req)
		}
		for _, path := range paths {
			s.TrackWritePath(path)
		}
	}
}

func (s *stateStore) Results() []workspace.ToolResult {
	if s == nil {
		return nil
	}
	return append([]workspace.ToolResult(nil), s.results...)
}

func (s *stateStore) LastResult() (workspace.ToolRequest, workspace.ToolResult, bool) {
	if s == nil || !s.hasLast {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}
	return s.lastReq, s.lastRes, true
}

func (s *stateStore) ResetResults() {
	if s == nil {
		return
	}
	s.results = nil
	s.lastReq, s.lastRes, s.hasLast = workspace.ToolRequest{}, workspace.ToolResult{}, false
}

func (s *stateStore) BudgetState() BudgetState {
	if s == nil {
		return BudgetState{}
	}
	return BudgetState{
		StepBudget:    s.stepBudget,
		StepsUsed:     s.stepsUsed,
		ReadCalls:     s.readCalls,
		WriteCalls:    s.writeCalls,
		NoProgress:    s.noProgress,
		RejectedCount: s.rejectedCount,
	}
}

func (s *stateStore) SetStepBudget(v int) {
	if s != nil {
		s.stepBudget = v
	}
}
func (s *stateStore) IncrementSteps() {
	if s != nil {
		s.stepsUsed++
	}
}
func (s *stateStore) IncrementReads() {
	if s != nil {
		s.readCalls++
	}
}
func (s *stateStore) IncrementWrites() {
	if s != nil {
		s.writeCalls++
	}
}
func (s *stateStore) AddRejected() {
	if s != nil {
		s.rejectedCount++
	}
}

func (s *stateStore) ComputeFingerprint(req workspace.ToolRequest) string {
	data := strings.Join([]string{req.Tool, req.Path, req.Pattern, req.Query, req.Command, req.Content}, "\x00")
	sum := sha256.Sum256([]byte(data))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func (s *stateStore) TrackFingerprint(fp string, result workspace.ToolResult) {
	if s == nil || strings.TrimSpace(fp) == "" {
		return
	}
	if s.fingerprints == nil {
		s.fingerprints = map[string]workspace.ToolResult{}
	}
	s.fingerprints[fp] = result
}

func (s *stateStore) ExecutionState() ExecutionState {
	if s == nil {
		return ExecutionState{}
	}
	return ExecutionState{NeedsNarrowing: s.execNeedsNarrowing, BroadInspects: s.execBroadInspects}
}

func (s *stateStore) SetAllowedTools(tools []string) {
	if s == nil {
		return
	}
	s.allowedTools = append([]string(nil), tools...)
}

func (s *stateStore) AllowedTools() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.allowedTools...)
}

func (s *stateStore) SetRequiredEditPaths(paths []string) {
	if s == nil {
		return
	}
	seen := map[string]struct{}{}
	s.requiredEditPaths = nil
	for _, raw := range paths {
		path := normalizeStateStorePath(raw)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		s.requiredEditPaths = append(s.requiredEditPaths, path)
	}
	slices.Sort(s.requiredEditPaths)
}

func (s *stateStore) ChangedPaths() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.changedPaths))
	for path := range s.changedPaths {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func (s *stateStore) RequiredEditsRemaining() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, path := range s.requiredEditPaths {
		if _, ok := s.changedPaths[path]; ok {
			continue
		}
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func (s *stateStore) TrackWritePath(raw string) {
	if s == nil {
		return
	}
	path := normalizeStateStorePath(raw)
	if path == "" {
		return
	}
	if s.changedPaths == nil {
		s.changedPaths = map[string]struct{}{}
	}
	s.changedPaths[path] = struct{}{}
}

func (s *stateStore) WriteDecision(invariantID string) WriteDecision {
	if s == nil {
		return WriteDecision{Allowed: true, InvariantID: invariantID}
	}
	decision := WriteDecision{
		Allowed:       len(s.RequiredEditsRemaining()) == 0,
		InvariantID:   strings.TrimSpace(invariantID),
		RequiredPaths: append([]string(nil), s.requiredEditPaths...),
		ChangedPaths:  s.ChangedPaths(),
	}
	if !decision.Allowed {
		decision.ReasonCode = string(turnReasonRequiredEditPathsRemaining)
	}
	return decision
}

func (s *stateStore) GateRejectionCount() int {
	if s == nil {
		return 0
	}
	return s.gateRejections
}

func (s *stateStore) IncrementGateRejections() {
	if s != nil {
		s.gateRejections++
	}
}
func (s *stateStore) ResetGateRejections() {
	if s != nil {
		s.gateRejections = 0
	}
}

func (s *stateStore) ProseRetriesRemaining() int {
	if s == nil {
		return 0
	}
	return s.proseRetries
}

func (s *stateStore) SetProseRetries(v int) {
	if s != nil {
		s.proseRetries = v
	}
}

func (s *stateStore) DecrementProseRetries() int {
	if s == nil || s.proseRetries <= 0 {
		return 0
	}
	s.proseRetries--
	return s.proseRetries
}

func (s *stateStore) LastGateError() string {
	if s == nil {
		return ""
	}
	return s.lastGateError
}

func (s *stateStore) SetLastGateError(v string) {
	if s != nil {
		s.lastGateError = strings.TrimSpace(v)
	}
}

func (s *stateStore) Metrics() MetricsSnapshot {
	if s == nil {
		return MetricsSnapshot{}
	}
	return MetricsSnapshot{
		ModelCalls:       s.modelCalls,
		RejectedActions:  s.rejectedCount,
		ProgressTurns:    s.progressTurns,
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.completionTokens,
		TotalTokens:      s.totalTokens,
	}
}

func (s *stateStore) AddModelCall() {
	if s != nil {
		s.modelCalls++
	}
}
func (s *stateStore) AddProgressTurn() {
	if s != nil {
		s.progressTurns++
	}
}

func (s *stateStore) AddUsage(usage llama.Usage) {
	if s == nil {
		return
	}
	s.promptTokens += usage.PromptTokens
	s.completionTokens += usage.CompletionTokens
	s.totalTokens += usage.TotalTokens
}

func normalizeStateStorePath(raw string) string {
	path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	path = strings.TrimPrefix(path, "./")
	path = strings.Trim(path, "/")
	return path
}

func planThenExecuteAnchorRefreshWriteOnlyError(tool string, refreshPaths []string) string {
	targets := strings.Join(nonEmptyRunnerStrings(refreshPaths, 4), ", ")
	return fmt.Sprintf("%s is not available in its requested form while plan-execute anchor-refresh mode is still open for: %s. Issue exactly one bounded source-body request for a remaining refresh path: `read_file` with explicit `start_line`/`end_line`, or `%s` with %s plus `pattern`. Do not call `submit_plan` until that refresh returns.", strings.TrimSpace(tool), targets, workspace.ToolNameCodeNavigate, workspace.CodeNavigateSourceBodyCapabilityPhrase())
}

type LifecycleCloseoutPolicyDecision struct {
	Handled bool
	Gate    runnerAutonomyGateDecision
}

func lifecycleLoopCloseoutDecision(stopReason string, hasOpenLoopObligations bool) LifecycleCloseoutPolicyDecision {
	if !hasOpenLoopObligations {
		return LifecycleCloseoutPolicyDecision{}
	}
	reason := strings.TrimSpace(stopReason)
	if reason == "" || reason == "completed" {
		reason = "completed_without_loop_fuse"
	}
	return LifecycleCloseoutPolicyDecision{Handled: true, Gate: runnerAutonomyGateDecision{
		Gate:        autonomy.GateLoop,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "loop.repeated_call",
		ReasonCode:  reason,
		Source:      "runner_loop_closeout",
	}}
}

func lifecycleBudgetCloseoutDecision(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) LifecycleCloseoutPolicyDecision {
	reason := strings.TrimSpace(stopReason)
	if reason == "" {
		reason = "completed"
	}
	decision := autonomy.PolicyObserve
	if exhausted {
		decision = autonomy.PolicyStop
	}
	budget := &autonomy.BudgetState{
		MaxToolSteps:             policy.MaxToolSteps,
		UsedToolSteps:            metrics.ToolCalls,
		MaxModelCalls:            policy.MaxModelCalls,
		MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
		UsedModelCalls:           metrics.ModelCalls,
		MaxContextMessages:       policy.MaxContextMessages,
		MaxContextChars:          policy.MaxContextChars,
		Exhausted:                exhausted,
	}
	if exhausted {
		budget.ExhaustionReason = reason
	}

	return LifecycleCloseoutPolicyDecision{Handled: true, Gate: runnerAutonomyGateDecision{
		Gate:        autonomy.GateBudget,
		Decision:    decision,
		InvariantID: "budget.run_policy",
		ReasonCode:  reason,
		Budget:      budget,
		Source:      "runner_budget_gate",
	}}
}

type ModelCallReadinessPolicyInput struct {
	Policy  RunPolicy
	Metrics runnerMetrics
}

type ModelCallReadinessPolicyDecision struct {
	Stop            bool
	StopReason      string
	BudgetExhausted bool
	CloseoutMode    noToolsCloseoutMode
	CloseoutPrompt  string
}

func modelCallReadinessBeforeModelCall(input ModelCallReadinessPolicyInput) ModelCallReadinessPolicyDecision {
	if input.Policy.shouldStopForOperationalModelCalls(input.Metrics.ModelCalls) {
		return ModelCallReadinessPolicyDecision{
			Stop:            true,
			StopReason:      "operational_model_call_limit_exceeded",
			BudgetExhausted: true,
			CloseoutMode:    noToolsCloseoutDeterministicRequired,
			CloseoutPrompt:  "Operational model-call limit is exhausted. Tools are now disabled. " + forceAnswerTailGatheredEvidence,
		}
	}
	return ModelCallReadinessPolicyDecision{}
}

type ToolAuthorizationPermissionDecision struct {
	Snapshot PermissionDecisionSnapshot
	Policy   ToolAuthorizationPolicyEvent
}

type ToolAuthorizationSurfaceDecision struct {
	Rejected     bool
	Summary      string
	Error        string
	Policy       ToolAuthorizationPolicyEvent
	ParityReport ParityReport
}

type ToolAuthorizationPolicyEvent struct {
	autonomy.ContractPolicyEvent
	EventType    autonomy.EventType
	ParityReport ParityReport
}

const toolAuthorizationPolicyInvariant = "tool.authorization"

func (r *Runner) readOnlyWriteChildTaskPermissionDecision(req workspace.ToolRequest) (PermissionDecisionSnapshot, bool) {
	if r == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameTask || !isWriteChildMode(req.Mode) {
		return PermissionDecisionSnapshot{}, false
	}

	readOnlyProfile := strings.TrimSpace(r.RuntimeBrief.PermissionProfile) == string(workerruntime.PermissionReadOnlyWorkspace)
	readOnlyWorkspace := r.Workspace != nil && r.Workspace.Mode() == workspace.ModeExplore
	if !readOnlyProfile && !readOnlyWorkspace {
		return PermissionDecisionSnapshot{}, false
	}

	return PermissionDecisionSnapshot{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   PermissionTarget(req),
		Decision: toolpolicy.Ask,
		Rule:     "read_only_workspace task.mode=write decision=ask",
	}, true
}

func toolAuthorizationPermissionDecision(req workspace.ToolRequest, policy toolpolicy.Policy, workspaceRulesDeny bool) ToolAuthorizationPermissionDecision {
	snapshot := permissionPolicyDecision(policy, req)
	if workspaceRulesDeny {
		snapshot.Decision = toolpolicy.Deny
		snapshot.Rule = "workspace.tool_rules decision=deny"
	}
	result := "no_approval_needed"
	decision := autonomy.PolicyAllow
	errorClass := ""
	if snapshot.Decision == toolpolicy.Ask {
		decision, result, errorClass = autonomy.PolicyAsk, "approval_required", "permission_policy"
	} else if snapshot.Decision == toolpolicy.Deny {
		decision, result, errorClass = autonomy.PolicyDeny, "denied", "permission_policy"
	}
	return ToolAuthorizationPermissionDecision{
		Snapshot: snapshot,
		Policy:   toolAuthorizationPolicyEvent(req, decision, "permission_policy", result, errorClass, strings.TrimSpace(snapshot.Rule), snapshot.Decision == toolpolicy.Ask),
	}
}

func toolAuthorizationSurfaceDecision(req workspace.ToolRequest, visibleTools []string, recoveryAllowance bool) ToolAuthorizationSurfaceDecision {
	parity := CompareParity(ParityInput{
		SurfaceID:   "current_tool_surface",
		InvariantID: toolAuthorizationPolicyInvariant,
		OldDecision: "reject",
		NewDecision: map[bool]string{true: "allow", false: "reject"}[recoveryAllowance],
	})
	if recoveryAllowance {
		policy := toolAuthorizationSurfacePolicyEvent(req, autonomy.PolicyAllow)
		policy.ParityReport = parity
		return ToolAuthorizationSurfaceDecision{Policy: policy, ParityReport: parity}
	}
	rejection := NewActiveSurface(visibleTools, nil).RejectionForTool(req.Tool)
	if !rejection.Reject {
		parity.NewDecision = "allow"
		return ToolAuthorizationSurfaceDecision{ParityReport: parity}
	}
	policy := toolAuthorizationSurfacePolicyEvent(req, autonomy.PolicyReject)
	policy.ParityReport = parity
	return ToolAuthorizationSurfaceDecision{
		Rejected:     true,
		Summary:      rejection.Summary,
		Error:        rejection.Error,
		Policy:       policy,
		ParityReport: parity,
	}
}

func toolAuthorizationWriteApprovalRequired(req workspace.ToolRequest) ToolAuthorizationPolicyEvent {
	return toolAuthorizationPolicyEvent(req, autonomy.PolicyAsk, "write_approval_required", "approval_required", "approval_guard", "tool_authorization_policy", true)
}

func toolAuthorizationSurfacePolicyEvent(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision) ToolAuthorizationPolicyEvent {
	result := "rejected"
	errorClass := "tool_surface"
	if decision == autonomy.PolicyAllow {
		result = "allowed"
		errorClass = ""
	}
	return toolAuthorizationPolicyEvent(req, decision, "current_tool_surface", result, errorClass, "tool_authorization_policy", false)
}

func emitToolAuthorizationSurfacePolicyResult(recorder *runnerAutonomyRecorder, req workspace.ToolRequest, decision autonomy.ToolPolicyDecision) {
	if recorder == nil {
		return
	}
	parityDecision := "reject"
	if decision == autonomy.PolicyAllow {
		parityDecision = "allow"
	}
	recorder.emitPolicyResult(req, toolAuthorizationPolicyResultInput(
		toolAuthorizationSurfacePolicyEvent(req, decision),
		autonomy.EventToolPolicyResult,
		toolAuthorizationSurfaceParityReport(recorder, parityDecision),
	))
}

func toolAuthorizationPolicyEvent(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reason string, result string, errorClass string, source string, approval bool) ToolAuthorizationPolicyEvent {
	return ToolAuthorizationPolicyEvent{
		EventType: autonomy.EventToolPolicyDecision,
		ContractPolicyEvent: autonomy.ContractPolicyEvent{
			ToolName:         strings.TrimSpace(req.Tool),
			PackID:           autonomy.ContractPackToolAuthorization,
			Decision:         decision,
			InvariantID:      toolAuthorizationPolicyInvariant,
			ReasonCode:       strings.TrimSpace(reason),
			ApprovalRequired: approval,
			Result:           strings.TrimSpace(result),
			ErrorClass:       strings.TrimSpace(errorClass),
			Source:           strings.TrimSpace(source),
		},
	}
}

func toolAuthorizationPolicyResultInput(policy ToolAuthorizationPolicyEvent, eventType autonomy.EventType, parity ParityReport) runnerAutonomyPolicyEventInput {
	if eventType == "" {
		eventType = autonomy.EventToolPolicyResult
	}
	return runnerAutonomyPolicyEventInput{
		EventType:        eventType,
		Decision:         policy.Decision,
		PackID:           policy.PackID,
		InvariantID:      policy.InvariantID,
		ReasonCode:       policy.ReasonCode,
		Transport:        policy.Transport,
		ParserFailure:    policy.ParserFailure,
		RepairSource:     policy.RepairSource,
		ApprovalRequired: policy.ApprovalRequired,
		Result:           policy.Result,
		ErrorClass:       policy.ErrorClass,
		ChangedPaths:     policy.ChangedPaths,
		EvidencePaths:    runnerAutonomyParityEvidenceRefs(parity),
		Source:           policy.Source,
	}
}

func toolAuthorizationSurfaceParityReport(recorder *runnerAutonomyRecorder, decision string) ParityReport {
	return CompareParity(ParityInput{
		ContractID:   recorder.activeAuthorityContractID(),
		ProtocolHash: recorder.activeProtocolHash(),
		SurfaceID:    "current_tool_surface",
		InvariantID:  toolAuthorizationPolicyInvariant,
		OldDecision:  decision,
		NewDecision:  decision,
	})
}

type runnerSession struct {
	runner *Runner

	messages []llama.ChatMessage
	opts     llama.ChatOptions

	onStatus      func(string)
	onToolRequest func(ToolRequestEvent)
	onToolResult  func(ToolResultEvent)

	answer string
	err    error
	ran    bool
}

func (r *Runner) Run(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	onStatus func(string),
	onToolRequest func(ToolRequestEvent),
	onToolResult func(ToolResultEvent),
) (string, error) {
	return RunSession(ctx, SessionConfig{}, &runnerSession{
		runner:        r,
		messages:      append([]llama.ChatMessage(nil), messages...),
		opts:          opts,
		onStatus:      onStatus,
		onToolRequest: onToolRequest,
		onToolResult:  onToolResult,
	})
}

func (s *runnerSession) Bootstrap(_ context.Context, _ *SessionState) error {
	if s == nil || s.runner == nil {
		return nil
	}
	r := s.runner
	if r.LanePolicy == nil {
		r.LanePolicy = LanePolicyForBrief(r.RuntimeBrief)
	}
	if r.StateStore == nil || r.stateStoreOwnedBySession {
		r.StateStore = newStateStore(r.SeedResults, r.InitialNeedsNarrowing, r.InitialBroadInspects)
		r.stateStoreOwnedBySession = true
	}
	r.StateStore.SetRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths)

	return nil
}

func (s *runnerSession) RunStep(ctx context.Context, _ *SessionState) StepResult {
	if s == nil || s.runner == nil || s.ran {
		return StepResult{Answer: s.answer, Err: s.err, Done: true}
	}
	s.ran = true
	s.answer, s.err = s.runner.runLegacySessionBody(ctx, s.messages, s.opts, s.onStatus, s.onToolRequest, s.onToolResult)
	return StepResult{Answer: s.answer, Err: s.err, Done: true}
}

func (s *runnerSession) Finish(_ context.Context, _ *SessionState, result StepResult) (string, error) {
	if result.Answer != "" || result.Err != nil {
		return result.Answer, result.Err
	}
	return s.answer, s.err
}

func (s *runnerSession) Close(_ context.Context, _ *SessionState) {
	if s == nil || s.runner == nil {
		return
	}
	s.runner.currentAllowedTools = nil
	s.runner.activeAutonomy = nil
}

func (r *Runner) presentableFallbackCandidate(results []workspace.ToolResult, answer string) (string, bool) {
	if r == nil {
		return presentableFallbackCandidate("", workerruntime.WorkerRuntimeBrief{}, results, answer)
	}
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return strings.TrimSpace(answer), strings.TrimSpace(answer) != ""
	}
	closeout := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)
	presentable, ok := closeout.PresentableFallbackCandidate(r.agentName(), answer)
	if !ok {
		return "", false
	}
	if genericEvidenceAnswerIncludesLineOrientedSourceBody(answer) || looksLikeLineOrientedSourceSnippetAnswer(answer) {
		if checked, checkedOK := r.presentableOrReadOnlyNonAnswerReplacement(results, presentable); checkedOK {
			return checked, true
		}
	}
	return presentable, true
}

func (r *Runner) presentableOrReadOnlyNonAnswerReplacement(results []workspace.ToolResult, answer string) (string, bool) {
	if r == nil {
		return presentableOrReadOnlyNonAnswerReplacement("", workerruntime.WorkerRuntimeBrief{}, results, answer)
	}
	return readOnlyClosureDecisionForResults(r.RuntimeBrief, results).PresentableOrReadOnlyNonAnswerReplacement(r.agentName(), answer)
}

func (r *Runner) presentableBriefConversationFallbackCandidate(results []workspace.ToolResult) (string, bool) {
	if r == nil {
		return presentableBriefConversationFallbackCandidate("", workerruntime.WorkerRuntimeBrief{}, results)
	}
	return readOnlyClosureDecisionForResults(r.RuntimeBrief, results).PresentableBriefConversationFallbackCandidate(r.agentName())
}

func (r *Runner) sessionID() string {
	if r == nil || r.Workspace == nil {
		return ""
	}
	return r.Workspace.SessionID()
}

func (r *Runner) workspaceAllowedTools() []string {
	if r == nil || r.Workspace == nil {
		return nil
	}
	return r.workspaceToolSurfaceSnapshot().AllowedTools()
}

func (r *Runner) workspaceAdvertisedAllowedTools() []string {
	if r == nil {
		return nil
	}
	return r.advertisedAllowedTools(r.workspaceAllowedTools())
}

func (r *Runner) workspaceToolSurfaceSnapshot() workspace.ToolSurfaceSnapshot {
	if r == nil || r.Workspace == nil {
		return workspace.ToolSurfaceSnapshot{}
	}

	if profile, ok := workerruntime.WorkspaceToolSurfaceOverrideProfileForTaskFamily(r.RuntimeBrief.TaskFamily); ok {
		return r.Workspace.ToolSurfaceSnapshotForProfile(profile)
	}

	return r.Workspace.ToolSurfaceSnapshot()
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
	PermissionPolicy      toolpolicy.Policy
	CheckpointPolicy      CheckpointPolicy
	RuntimeBrief          workerruntime.WorkerRuntimeBrief
	OnEvent               func(RunEvent)
	OnThought             func(ThoughtEvent)
	// ContextSize is the configured llama.cpp context window in tokens.
	// Surfaced in model_call telemetry so log analysis can compute
	// ctx_pct (prompt_tokens / ctx_size) without having to reconcile
	// the running config separately. Zero means unknown.
	ContextSize     int
	DisableTaskTool bool
	// LanePolicy is the lane-specific behavioral policy derived from
	// RuntimeBrief at runner construction time. The policy replaces
	// inline TaskFamily/Contract string-comparison guards with a
	// single dispatch point. When nil, falls back to the default lane policy.
	LanePolicy *LanePolicy

	StateStore               StateStore
	stateStoreOwnedBySession bool

	// InstructionAttacher injects configured per-directory guidance
	// whenever the model touches a file under that directory.
	InstructionAttacher *instructions.Attacher
	// DoomLoopGate, if non-nil, is called when the model emits three identical
	// consecutive tool calls. Returning true approves the call (resets the
	// streak and lets execution continue); returning false rejects it and
	// terminates the turn. When nil the runner falls back to a hard rejection.
	DoomLoopGate         func(req workspace.ToolRequest, count int) bool
	LastAssistantMessage llama.ChatMessage
	// ReadApprovalGate, when wired, lets the operator approve or deny a
	// read that the post-readiness candidate-scope rejection would
	// otherwise block. Only the "Plan-execute read outside allowed
	// candidate scope" rejection is overridable; all other rejections
	// enforce real invariants and pass through unchanged.
	ReadApprovalGate    *ReadApprovalGate
	currentAllowedTools []string
	currentTransport    PlanExecutePlanningTransport
	activeAutonomy      *runnerAutonomyRecorder

	lastCompletionGateSourceGrounding sourceGroundingAssessment
}

func (r *Runner) rememberCompletionGateRejection(result workspace.ToolResult) {
	if r == nil {
		return
	}

	r.lastCompletionGateSourceGrounding = sourceGroundingAssessment{}
	if assessment, ok := sourceGroundingAssessmentFromResult(result); ok {
		r.lastCompletionGateSourceGrounding = assessment
	}
}

func (r *Runner) supportsNativeTools() bool {
	return r == nil || r.Client == nil || r.Client.SupportsNativeTools()
}

func (r *Runner) currentPlanningTransport() PlanExecutePlanningTransport {
	if r == nil {
		return PlanExecutePlanningTransport{TextualToolParser: true}
	}
	if r.currentTransport.SupportsNativeTools ||
		r.currentTransport.ForceNativeTools ||
		r.currentTransport.TextualToolParser ||
		r.currentTransport.ResponseFormatJSON ||
		r.currentTransport.TerminalOnly {
		return r.currentTransport
	}
	return PlanExecutePlanningTransport{TextualToolParser: true}
}

// lanePolicy returns the lane-specific behavioral policy, falling back
// to LanePolicyForBrief when the Runner.LanePolicy field is nil.
func (r *Runner) lanePolicy() *LanePolicy {
	if r != nil && r.LanePolicy != nil {
		return r.LanePolicy
	}

	if r != nil {
		return LanePolicyForBrief(r.RuntimeBrief)
	}

	return &defaultLanePolicy
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

func (r *Runner) runLegacySessionBody(
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
	planThenExecuteSubmitOnlyReadinessNudged := false
	planThenExecuteCandidateReadinessNonSubmitTools := 0
	conversationEvidenceNudged := false
	convFirstMilestone, convNudgeInterval := lanePolicyCallCountMilestones(lp, r.ContextSize)

	planThenExecuteCorrectionAfterRefreshNudged := false
	planThenExecuteUnreadSourceNudgedFor := ""
	delegatedReadOnlyAnswerReady := slices.Contains(r.RuntimeBrief.Constraints, "answer_readiness_reached")
	delegatedReadOnlyPostReadyFollowups := 0
	delegatedReadOnlyDiminishingNudgeStage := 0
	postChildPacketActive := false
	postChildFollowupReads := 0
	postChildDiminishingNudgeStage := 0
	implementationWriteOnlyRecoveryActive := false
	requiredPathCompletionRecoveryActive := false
	scopedHiddenNonWriteRecoveryUsed := false
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
	collectedResults := append([]workspace.ToolResult(nil), r.SeedResults...)
	exactAnchorWriteOnlyActive := exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, collectedResults)
	exactAnchorWriteFailures := 0
	currentAllowedTools := []string(nil)
	implementationTargetWriteReadyNudges := 0
	runState := &runnerRunState{
		ctx:                                     ctx,
		opts:                                    opts,
		collectedResults:                        collectedResults,
		turnMessages:                            turnMessages,
		executionState:                          &executionState,
		delegatedReadOnlyAnswerReady:            delegatedReadOnlyAnswerReady,
		postChildPacketActive:                   postChildPacketActive,
		implementationWriteOnlyRecoveryActive:   implementationWriteOnlyRecoveryActive,
		requiredPathCompletionRecoveryActive:    requiredPathCompletionRecoveryActive,
		exactAnchorWriteOnlyActive:              exactAnchorWriteOnlyActive,
		planThenExecuteNativeSubmitPlanDisabled: planThenExecuteNativeSubmitPlanDisabled,
		implementationTargetWriteReadyNudges:    implementationTargetWriteReadyNudges,
	}
	syncRunState := func() {
		runState.opts = opts
		runState.collectedResults = collectedResults
		runState.turnMessages = turnMessages
		runState.currentAllowedTools = append(runState.currentAllowedTools[:0], currentAllowedTools...)
		runState.executionState = &executionState
		runState.delegatedReadOnlyAnswerReady = delegatedReadOnlyAnswerReady
		runState.postChildPacketActive = postChildPacketActive
		runState.implementationWriteOnlyRecoveryActive = implementationWriteOnlyRecoveryActive
		runState.requiredPathCompletionRecoveryActive = requiredPathCompletionRecoveryActive
		runState.exactAnchorWriteOnlyActive = exactAnchorWriteOnlyActive
		runState.planThenExecuteNativeSubmitPlanDisabled = planThenExecuteNativeSubmitPlanDisabled
		runState.implementationTargetWriteReadyNudges = implementationTargetWriteReadyNudges
	}
	applyRunState := func() {
		opts = runState.opts
		collectedResults = runState.collectedResults
		turnMessages = runState.turnMessages
		currentAllowedTools = runState.currentAllowedTools
		planThenExecuteNativeSubmitPlanDisabled = runState.planThenExecuteNativeSubmitPlanDisabled
		implementationTargetWriteReadyNudges = runState.implementationTargetWriteReadyNudges
	}

	defer func() {
		r.currentAllowedTools = nil
		r.activeAutonomy = nil
	}()

	setCurrentAllowedTools := func(tools []string) {
		r.setCurrentAllowedTools(runState, tools)
	}

	syncCurrentAllowedToolsFromOptions := func() {
		syncRunState()
		r.syncCurrentAllowedToolsFromOptions(runState)
		applyRunState()
	}
	rebuildTools := func() {
		syncRunState()
		r.rebuildRunTools(runState)
		applyRunState()
	}
	terminalSubmitPlanNativeTools := func() bool {
		syncRunState()

		return r.terminalSubmitPlanNativeTools(runState)
	}
	syncPlanThenExecuteRecoveryAllowedTools := func() {
		syncRunState()
		r.syncPlanThenExecuteRecoveryAllowedTools(runState)
		applyRunState()
	}
	refreshPlanThenExecuteTerminalSurface := func(reason string) {
		syncRunState()
		r.refreshPlanThenExecuteTerminalSurface(runState, reason)
		applyRunState()
	}
	transitionPlanThenExecuteSubmitReadyTerminal := func(prompt string, eventRole string, surfaceKey string) {
		syncRunState()
		r.transitionPlanThenExecuteSubmitReadyTerminal(runState, prompt, eventRole, surfaceKey)
		applyRunState()
	}
	forcePlanThenExecuteTerminalSurface := func(reason string) {
		syncRunState()
		r.forcePlanThenExecuteTerminalSurface(runState, reason)
		applyRunState()
	}

	if r.Workspace != nil {
		if exactAnchorWriteOnlyActive {
			runState.hideExactAnchorReadTools(r.workspaceAllowedTools())
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
	completionGateRejections := 0
	emitCompletionStatus := func(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
		completionStatusEmitted = true
		completionStatusLastStatus = strings.TrimSpace(status)
		completionStatusLastBlockerKind = strings.TrimSpace(blockerKind)
		r.emitCompletionStatusFromParams(status, reason, blockerKind, results, userVisibleBlocker, implementationTargetWriteReadyNudges)
	}

	defer func() {
		if autonomyRecorder != nil {
			autonomyRecorder.emitLoopCloseout(stopReason)
			autonomyRecorder.emitBudgetGate(policy, metrics, stopReason, budgetExhausted)
		}

		r.emitBudgetLedger(policy, metrics, stopReason, budgetExhausted)
	}()

	finish := func(value string, finishErr error, reason string, exhausted bool) (string, error) {
		syncRunState()

		runState.policy = policy
		runState.metrics = metrics
		runState.stopReason = stopReason
		runState.budgetExhausted = budgetExhausted
		runState.autonomyRecorder = autonomyRecorder
		runState.completionGateRejections = completionGateRejections
		runState.completionStatusEmitted = completionStatusEmitted
		runState.completionStatusLastStatus = completionStatusLastStatus
		runState.completionStatusLastBlockerKind = completionStatusLastBlockerKind
		runState.acceptedAnswerSignalsEmitted = acceptedAnswerSignalsEmitted
		value, finishErr = r.finish(runState, value, finishErr, reason, exhausted)
		if finishErr == nil &&
			strings.TrimSpace(value) != "" &&
			strings.TrimSpace(value) != strings.TrimSpace(r.LastAssistantMessage.Content) &&
			(strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "") {

			r.LastAssistantMessage.Content = value
		}

		applyRunState()

		stopReason = runState.stopReason
		budgetExhausted = runState.budgetExhausted
		acceptedAnswerSignalsEmitted = runState.acceptedAnswerSignalsEmitted
		completionStatusEmitted = runState.completionStatusEmitted
		completionStatusLastStatus = runState.completionStatusLastStatus
		completionStatusLastBlockerKind = runState.completionStatusLastBlockerKind

		return value, finishErr
	}

	var (
		lastToolKey    string
		lastToolResult *workspace.ToolResult
	)
	// activeToolCall points at the native tool_call we're currently
	// responding to (when the model sent one). Turn helpers consult it to
	// emit role:"tool" + tool_call_id responses instead of
	// role:"system" text. Reset to nil at the top of every iteration.
	var activeToolCall *llama.ToolCall

	replayedToolRequests := make(map[string]int)
	declarationMovesForbidden := declarationMovesForbiddenByMessages(turnMessages)
	rejectedActions := make(map[string]int)
	consecutiveRejectedActions := 0
	transportRetries := 0
	reviewRepairTerminalRequiredRejects := 0
	rejectionRecoveryNudgeUsed := false
	consecutiveNoProgress := 0
	discoveryRedirect := NewDiscoveryRedirect()
	consecutiveNarrowToolCalls := 0
	lastNarrowToolFamily := ""
	narrowToolFamilyNudgeFired := false

	var preimageStore *PreimageCheckpointStore

	if r.Workspace != nil {
		preimageStore = NewPreimageCheckpointStore(r.Workspace.Root())
	}

	taskLedgerPolicy := r.TaskLedger
	if runnerBriefRequiresMutation(r.RuntimeBrief) && !strings.HasPrefix(r.agentName(), "child_write:") {
		taskLedgerPolicy.RequireEvidence = true
	}

	taskLedger := newTaskLedgerState(taskLedgerPolicy)

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

	// Investigation lane: recommended strategy to avoid exhaustive
	// symbol-by-symbol enumeration. One-shot at session start.
	if lanePolicyHasName(r.lanePolicy(), lanePolicyReadOnlyInvestigation) {
		turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: readOnlyInvestigationStrategyPrompt(visibleRuntimeBrief.AllowedTools)})
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

	collectedResults = r.maybeApplyReadOnlyNamedPathPrefetch(recorder, collectedResults)

	// currentFingerprint is the fingerprint of the in-flight tool call,
	// set at the top of each loop iteration. The turn controller reads it
	// via pointer so every rejection path can record the
	// fingerprint in rejectedFingerprints — letting a later pre-execute
	// guard short-circuit a byte-identical retry of a rejected call.
	var currentFingerprint string

	implementationTargetWriteReadySourceRefreshes := 0
	implementationWriteOnlyNonWriteRejects := 0

	var (
		deferredSourceEvidenceWrite     *implementationDeferredSourceEvidenceWrite
		deferredGitHistorySubmitPlan    *workspace.ToolRequest
		deferredGitHistorySubmitPlanRaw string
	)

	controller := runnerTurnController{
		runner:                     r,
		ctx:                        ctx,
		policy:                     policy,
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
	finishNoProgressBudgetExhausted := func(results []workspace.ToolResult) (string, error) {
		if blocker, ok := r.implementationContractCloseoutBlocker(results, "no_progress_budget_exhausted"); ok {
			r.emitImplementationLifecycleState(results, "no_progress_budget_exhausted", implementationTargetWriteReadyNudges)
			return finish(blocker, nil, "no_progress_budget_exhausted", false)
		}

		answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
		return finish(answer, err, "no_progress_budget_exhausted", false)
	}
	if answer, ok := r.runVerificationBootstrap(runnerVerificationBootstrapInput{
		ctx:              ctx,
		collectedResults: collectedResults,
		metrics:          &metrics,
		recorder:         &recorder,
		executionState:   &executionState,
		requestBudget:    &requestBudget,
		autonomyRecorder: autonomyRecorder,
		onToolRequest:    onToolRequest,
	}); ok {
		return finish(answer, nil, "verification_auto_answered", false)
	}

	for step := 0; policy.unbounded() || step < policy.MaxToolSteps; step++ {
		stepNumber := step + 1
		readiness := modelCallReadinessBeforeModelCall(ModelCallReadinessPolicyInput{
			Policy:  policy,
			Metrics: metrics,
		})
		if readiness.Stop {
			if blocker, ok := r.implementationContractCloseoutBlocker(collectedResults, readiness.StopReason); ok {
				r.emitImplementationLifecycleState(collectedResults, readiness.StopReason, implementationTargetWriteReadyNudges)
				return finish(blocker, nil, readiness.StopReason, readiness.BudgetExhausted)
			}
			if strings.TrimSpace(readiness.CloseoutPrompt) != "" {
				answer, err := controller.completeWithoutMoreToolsWithReasonMode(readiness.CloseoutPrompt, readiness.CloseoutMode)
				return finish(answer, err, readiness.StopReason, readiness.BudgetExhausted)
			}
			return finish("", nil, readiness.StopReason, readiness.BudgetExhausted)
		}

		modelCallOpts := readOnlyEvidenceAvailableTerminalChatOptions(opts, r.RuntimeBrief, collectedResults)
		r.currentTransport = PlanExecuteRuntimeCallPolicyFromChatOptions(modelCallOpts, terminalSubmitPlanNativeTools(), false).PlanningTransport()
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

		modelCallNumber := metrics.ModelCalls + 1
		phaseHint := ""

		if implementationWriteOnlyRecoveryActive && strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
			phaseHint = turnPhaseReviewRepairTerminal
		}

		contractID := ""
		if autonomyRecorder != nil {
			contractID = autonomyRecorder.activeAuthorityContractID()
		}

		phaseContract := r.deriveCurrentTurnPhaseContract(runnerTurnPhaseContractInput{
			ContractID:          contractID,
			ModelCallNumber:     modelCallNumber,
			Step:                stepNumber,
			PhaseHint:           phaseHint,
			Brief:               r.RuntimeBrief,
			Results:             collectedResults,
			AllowedTools:        currentAllowedTools,
			ActiveSurface:       runState.toolSurface,
			Options:             modelCallOpts,
			SupportsNativeTools: terminalSubmitPlanNativeTools(),
		})
		modelProtocol, hasModelProtocol := r.compileModelProtocolProjection(r.RuntimeBrief, collectedResults, phaseContract, policy, terminalSubmitPlanNativeTools())

		if autonomyRecorder != nil {
			modelProtocol = autonomyRecorder.bindModelProtocolProjection(modelProtocol)
			autonomyRecorder.emitTurnPhaseContract(phaseContract, modelProtocol)
		}

		modelStart := time.Now()

		requestMessages := turnMessages

		if hasModelProtocol {
			requestMessages = modelMessagesWithProtocolProjection(requestMessages, modelProtocol)
			if autonomyRecorder != nil {
				autonomyRecorder.markModelProtocolPresented(modelProtocol)
			}
		}

		requestMessages = r.chatMessagesForOptions(requestMessages, modelCallOpts)

		assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, modelCallOpts)
		if err != nil {
			if answer, ok := r.modelErrorAfterPartialEditBlockerAnswer(err, collectedResults); ok {
				emitCompletionStatus("blocked", "model_error_after_partial_edit", "model_error_after_partial_edit", collectedResults, true)

				return finish(answer, nil, "model_error_after_partial_edit", false)
			}

			if transportRetries < 1 && retryableModelTransportError(err) {
				transportRetries++

				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The previous model request was interrupted by a temporary connection issue. Continue from where you left off, repeating any incomplete tool call or thought.",
				})
				r.emitEvent(RunEvent{Role: "transport_retry", Content: fmt.Sprintf("transport error, retry %d/1: %s", transportRetries, strings.ToLower(err.Error()))})

				continue
			}

			if prompt, ok := r.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(err, opts, collectedResults, planThenExecuteNativeSubmitPlanNoCallFallbacks); ok {
				planThenExecuteNativeSubmitPlanNoCallFallbacks++

				planThenExecuteNativeSubmitPlanDisabled = true
				opts = planThenExecuteTextualSubmitPlanFallbackChatOptions(opts, r.RuntimeBrief, collectedResults)

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)
				r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&turnMessages, collectedResults, "native_required_no_tool_call")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

				continue
			}

			return finish("", err, "model_error", false)
		}

		metrics.ModelCalls++
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
		normalizedPolicyInput := runnerAutonomyPolicyInputFromNormalization(normalization)

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
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "visible_prose_salvage", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "visible_tool_salvage", Content: fmt.Sprintf("model wrapped parser-readable %s request in prose; executing the request", req.Tool)})
			} else if salvagedReq, salvagedRaw, salvaged := r.salvageToolRequestFromReasoning(assistantMessage, collectedResults); salvaged {
				req, raw, ok = salvagedReq, salvagedRaw, true
				r.emitToolRequestNormalizerEvent("missing_tool_inferred", "reasoning_salvage", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "reasoning_tool_salvage", Content: fmt.Sprintf("model emitted parser-readable %s request in hidden reasoning while visible content was empty; executing the request", req.Tool)})
			} else if answerText, salvaged := r.salvageAnswerFromReasoning(assistantMessage, collectedResults); salvaged {
				r.emitEvent(RunEvent{Role: "reasoning_answer_salvage", Content: fmt.Sprintf("model emitted answer prose in hidden reasoning while visible content was empty; extracted %d chars", len(answerText))})

				return finish(answerText, nil, "reasoning_answer_salvage", false)
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteRejectedRequiredSourceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool planner text and issued source read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicImplementationRejectedRequiredPathRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_required_source_recovery", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_required_read", Content: fmt.Sprintf("completion recovery ignored non-tool implementation text and issued bounded read for unread required edit path %s", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteSourceSatisfiedEvidenceRead(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				deterministicPlanExecuteEvidenceRead = true

				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_source_satisfied_evidence_recovery", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_source_satisfied_read", Content: fmt.Sprintf("completion recovery read source-satisfied evidence path %s before accepting the closeout", req.Path)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteGroundingRecoveryRequest(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_grounding_recovery", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_grounding", Content: fmt.Sprintf("completion recovery ignored non-tool planner text after ungrounded submit_plan and issued %s", req.Tool)})
			} else if recoveredReq, recoveredRaw, recovered := r.deterministicPlanExecuteNonToolCandidateRecovery(reply, collectedResults); recovered {
				req, raw, ok = recoveredReq, recoveredRaw, true
				r.emitToolRequestNormalizerEvent("requires_terminal_action", "deterministic_candidate_recovery", req, nil, autonomy.ParserFailureNone)
				r.emitEvent(RunEvent{Role: "completion_recovery_auto_candidate", Content: fmt.Sprintf("completion recovery ignored non-terminal planner prose and issued %s for %s", req.Tool, planThenExecuteRequestTargetPath(req))})
			} else if strings.TrimSpace(reply) == "" {
				if recoveredReq, recoveredRaw, event, recovered := r.deterministicEmptyReplyRecovery(collectedResults); recovered {
					req, raw, ok = recoveredReq, recoveredRaw, true

					r.emitEvent(event)
				}
			}

			if !ok &&
				normalization.EventOutcome == toolRequestNormalizerOutcomeUnsafeToInfer &&
				r.unsafeToolShapeRecoveryBeforeCompletionGateActive(collectedResults) {
				rebuildTools()
				if normalization.UnsafeExplicitTool {
					controller.recordUnsafeToolShape(stepNumber, activeToolCall, normalization, activeToolCall == nil || r.supportsNativeTools())
					if prompt := r.unsafeToolShapeRecoveryPrompt(normalization, collectedResults); prompt != "" {
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
						r.emitEvent(RunEvent{Role: "unsafe_tool_shape_recovery", Content: prompt})
					}
					continue
				}
				if prompt := r.ambiguousModelOutputRecoveryPrompt(normalization, collectedResults); prompt != "" {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "ambiguous_model_output_recovery", Content: prompt})
				}
				continue
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
			suppressParseFailureModelMessage := false

			if activeToolCall != nil {
				assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, *activeToolCall)
			} else if !r.supportsNativeTools() {
				assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, workspace.ToolRequest{Tool: parserRecoveryToolName}, raw)
				activeToolCall = nil
			} else {
				suppressParseFailureModelMessage = true
			}

			if strings.TrimSpace(assistantToolTurn.Role) != "" {
				assistantToolTurn = r.assistantHistoryMessageForOptions(assistantToolTurn, opts)
				turnMessages = append(turnMessages, assistantToolTurn)
				r.emitThinkingTrace(stepNumber, "assistant_turn_preserved", assistantToolTurn, opts, true)
			}
			controller.recordParseFailure(stepNumber, activeToolCall, raw, err, normalization.EventTransport, normalization.ParserFailureClass, suppressParseFailureModelMessage)

			if recoveryPlan, ok := r.planThenExecuteSubmitPlanParseRecoveryPlan(raw, err, collectedResults); ok {
				prompt := RenderPlanExecuteRecoveryPlan(recoveryPlan)

				implementationWriteOnlyRecoveryActive = true

				if len(r.remainingRequiredEditPaths(collectedResults)) > 0 {
					requiredPathCompletionRecoveryActive = true
				}

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
				rebuildTools()

				opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

				syncPlanThenExecuteRecoveryAllowedTools()
				refreshPlanThenExecuteTerminalSurface("parse_recovery")

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitPlanExecuteSubmitPlanParseRecoveryEvent(recoveryPlan, prompt)
			} else {
				if prompt := r.malformedToolRecoveryPrompt(raw, err, collectedResults); prompt != "" {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "malformed_tool_recovery", Content: prompt})
				}
				r.emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw, err)
			}

			continue
		}

		if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterEvidence(req, collectedResults, currentAllowedTools); replay {
			originalReq := req
			if strings.TrimSpace(originalReq.Tool) == workspace.ToolNameSourceSanity || strings.TrimSpace(originalReq.Tool) == workspace.ToolNameShell {
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

		if ok && strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan &&
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
		if !ok {
			if replayReq, replayRaw, replay := deferredSourceEvidenceWrite.replayAfterCompletionAttempt(collectedResults, currentAllowedTools); replay {
				r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
					"agent":          r.agentName(),
					"original_tool":  "completion",
					"rewritten_tool": strings.TrimSpace(replayReq.Tool),
					"original_raw":   strings.TrimSpace(reply),
					"rewritten_raw":  strings.TrimSpace(replayRaw),
					"reason":         "implementation_delete_after_source_evidence_completion_replay",
				}))

				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: rewriteVisibilityNote("completion", replayReq.Tool),
				})
				req = replayReq
				raw = replayRaw
				ok = true
				activeToolCall = nil
				deferredSourceEvidenceWrite = nil
			}
		}

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
					blockerResult, ok := finalAnswerCompletionGateProjection(
						acceptance,
						"Answer tool rejected by blocker check",
						r.finalAnswerBlockerErrorText(blockerKind, reason, answerContent, collectedResults),
					)
					if !ok {
						return finish(answerContent, nil, "model_answer_tool", false)
					}

					if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
						stepNumber:               stepNumber,
						activeToolCall:           activeToolCall,
						assistantMessage:         assistantMessage,
						acceptance:               acceptance,
						result:                   blockerResult,
						collectedResults:         collectedResults,
						currentAllowedTools:      currentAllowedTools,
						completionGateRejections: &completionGateRejections,
						turnMessages:             &turnMessages,
						controller:               &controller,
						autonomyRecorder:         autonomyRecorder,
						rebuildTools:             rebuildTools,
					}) {
						continue
					}

					if answer, ok := r.deterministicNoToolsFallback(noToolsFallbackAfterRejectedModelCloseout, collectedResults); ok &&
						!readOnlyAnswerReportsInsufficientEvidence(answer) {

						return finish(answer, nil, "deterministic_final_answer_recovery", false)
					}

					return finish(answerContent, nil, blockerKind, true)
				}

				return finish(answerContent, nil, "model_answer_tool", false)
			}
		}

		if !ok {
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

				result.Error = r.escalateCompletionGateResultMessage(result, priorGateRejections, collectedResults, currentAllowedTools)

				if autonomyRecorder != nil {
					autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), collectedResults, priorGateRejections)
				}

				controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)

				if IsTextualToolCall(reply) {
					r.LastAssistantMessage.Content = "[XML tool-call markup detected and sanitized — the model emitted its native XML format instead of a proper tool call or prose. The harness recovery path will retry with a corrected prompt.]"
				}

				completionGateRejections = priorGateRejections + 1

				r.rememberCompletionGateRejection(result)

				retryLimit := r.completionGateRetryLimitForResult(result, collectedResults, currentAllowedTools)
				sourceGroundingReadRecovery := r.sourceGroundingReadRecoveryAvailable(result, currentAllowedTools) &&
					priorGateRejections < retryLimit

				shouldAttachRecoveryPrompt := priorGateRejections == 0 ||
					result.Summary == "Completion gate rejected unresolved process narration" ||
					(r.planThenExecuteCompletionGateSummaryRetryable(result.Summary, collectedResults, priorGateRejections)) ||
					(r.textualRequiredPathCompletionRecoveryApplies(collectedResults) && priorGateRejections < retryLimit) ||
					(r.unscopedNoWriteCompletionRecoveryApplies(collectedResults) && priorGateRejections < retryLimit) ||
					(r.completionGateMutationRecoveryApplies(collectedResults) && priorGateRejections < retryLimit) ||
					// Read-only workspace lanes get the recovery prompt on every
					// rejection — the model needs continuous guidance about what
					// source-body evidence is required and what tools are restricted,
					// not just on the first failed attempt.
					(workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) && priorGateRejections < retryLimit)

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
							prompt = planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(prompt, r.RuntimeBrief, collectedResults)

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
							runState.hideReviewRepairTerminalOnlyTools(r.workspaceAllowedTools())
						} else {
							runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
						}

						rebuildTools()

						opts = planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
						opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
					}

					refreshPlanThenExecuteTerminalSurface("completion_gate_recovery")

					if card := r.renderRecoveryCardForRejectedActionWithTransport(workspace.ToolRequest{Tool: "completion_gate"}, result, collectedResults, currentAllowedTools, r.currentPlanningTransport()); card != "" {
						prompt = appendRecoveryCardToMessage(prompt, card)
					}

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
					r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
				}

				if priorGateRejections >= retryLimit {
					if recovered, _, ok := r.readOnlyTerminalRecoveryCandidate(completionGateRepeatedRejectionAcceptance(), collectedResults); ok {
						emitCompletionStatus("accepted", "read_only_terminal_recovery", "", collectedResults, false)

						return finish(recovered, nil, "read_only_terminal_recovery", false)
					}

					answer := r.completionGateRepeatedBlockerAnswer(result, collectedResults)
					blockerKind := string(r.completionGateRepeatedViolation(collectedResults).Kind)
					emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

					return finish(answer, nil, blockerKind, true)
				}

				if sourceGroundingReadRecovery {
					runState.hideNonSourceBodyReadTools(OwnerSourceGrounding, currentAllowedTools)
					rebuildTools()

					turnMessages = append(turnMessages, llama.ChatMessage{
						Role:    "system",
						Content: "Your response was rejected because it cites source evidence that has not been read as source-body support. Your next visible action must be exactly one bounded `read_file` or `code_navigate` call for a cited unread path, or `<<QUESTION>>:` with the specific blocker if that path cannot be read. Do not answer in prose again until the cited path has source-body evidence or is explicitly qualified as unverified.",
					})

					r.emitEvent(RunEvent{Role: "source_grounding_tool_required", Content: "Source-grounding rejection kept read tools available and required a bounded source-body read"})
					continue
				}

				if policy.shouldStopForNoProgress(consecutiveNoProgress) {
					return finishNoProgressBudgetExhausted(collectedResults)
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
					// If tools were hidden for source-grounding and the model
					// still answered in prose, give one more retry with a
					// stricter tool-call instruction instead of burning another
					// gate retry slot.

					blockerResult, ok := finalAnswerCompletionGateProjection(
						acceptance,
						"Final answer rejected by blocker check",
						r.finalAnswerBlockerErrorText(blockerKind, reason, answer, collectedResults),
					)
					if !ok {
						return finish(answer, err, "model_final", false)
					}
					if r.retryRejectedFinalAnswerForRun(runnerFinalAnswerRetryInput{
						stepNumber:               stepNumber,
						activeToolCall:           activeToolCall,
						assistantMessage:         assistantMessage,
						acceptance:               acceptance,
						result:                   blockerResult,
						collectedResults:         collectedResults,
						currentAllowedTools:      currentAllowedTools,
						completionGateRejections: &completionGateRejections,
						turnMessages:             &turnMessages,
						controller:               &controller,
						autonomyRecorder:         autonomyRecorder,
						rebuildTools:             rebuildTools,
					}) {
						continue
					}

					if answer, ok := r.deterministicNoToolsFallback(noToolsFallbackAfterRejectedModelCloseout, collectedResults); ok &&
						!readOnlyAnswerReportsInsufficientEvidence(answer) {

						return finish(answer, nil, "deterministic_final_answer_recovery", false)
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
			assistantToolTurn = textualToolRequestHistoryMessage(assistantMessage, req, raw)
			activeToolCall = nil
		} else {
			call := syntheticLegacyToolCall(stepNumber, req, raw)

			activeToolCall = &call
			assistantToolTurn = legacyToolCallHistoryMessage(assistantMessage, call)
		}

		assistantToolTurn = r.assistantHistoryMessageForOptions(assistantToolTurn, modelCallOpts)
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
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "repeated_diagnostic_log_read_previous_window")
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
				"from_path": NormalizeRunnerPath(req.Path),
				"to_path":   NormalizeRunnerPath(redirected.Path),
				"reason":    reason,
			}))

			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := structuredSourceReadRewriteRequest(req, r.Workspace, currentAllowedTools, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := pathlessReadSymbolAfterOutlineRewriteRequest(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := repeatedFileOutlineToReadSymbolRewriteRequest(req, collectedResults, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, reason, ok := readOnlyDefinitionToReadSymbolRewriteRequest(req, r.RuntimeBrief); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, reason)
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
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "repeated_rangeless_read_file_bounded_source")
		}

		currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)

		if workspace.IsWorkspaceWriteTool(req.Tool) && implementationTargetSourceWriteTargetsHaveEvidence(req, collectedResults) {
			clearRejectedFingerprintSummary(requestBudget.fingerprint.rejected, currentFingerprint, "Write rejected before source-body evidence")
			delete(rejectedActions, toolActionClass(req))
		}

		if !exactAnchorWriteOnlyActive {
			requestBudget.setRecoveryContext(r.RuntimeBrief, collectedResults)
			decision := attachLoopPolicyDecision(requestBudget.earlyRejectedFingerprintDecision(req, currentFingerprint))
			if result, prompt, ok := r.mutationRejectedRetryWriteRecovery(decision, req, collectedResults); ok {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				implementationWriteOnlyRecoveryActive = true

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
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
					if presentable, ok := r.presentableFallbackCandidate(collectedResults, "The retained source context identifies the relevant implementation surface."); ok {
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

		// Docs context reads under the cap are allowed even when the terminal
		// submit_plan surface is active. Must run before the nudge rejection
		// below so the read can reach the post-readiness candidate-scope check.
		if !recoveryReadAllowanceGrantedThisRequest && workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
			if target != "" && isPlanExecuteContextDocPath(target) &&
				planExecuteContextDocReadCount(collectedResults) < planThenExecuteContextDocReadCap {
				recoveryReadAllowanceGrantedThisRequest = true
			}
		}
		if !recoveryReadAllowanceGrantedThisRequest &&
			r.ReadApprovalGate != nil && r.ReadApprovalGate.Decider != nil &&
			workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			recoveryReadAllowanceGrantedThisRequest = true
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMissingTargetReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_missing_target_read_ranked_candidate")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, prompt, reject := planThenExecuteReadinessNudgedNonSubmitRejection(req, r.RuntimeBrief, collectedResults, planThenExecuteSubmitOnlyReadinessNudged); reject && !recoveryReadAllowanceGrantedThisRequest {
			result := workspace.ToolResult{
				Tool:    req.Tool,
				Summary: summary,
				Error:   errorText,
			}
			controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
			refreshPlanThenExecuteTerminalSurface("readiness_non_submit_rejection")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: prompt})

			continue
		}

		if planThenExecuteReadinessNudged &&
			!planThenExecuteSubmitOnlyReadinessNudged &&
			r.planThenExecutePlannerPhase() &&
			strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan &&
			successfulSubmitPlanResultCount(collectedResults) == 0 {

			planThenExecuteCandidateReadinessNonSubmitTools++
			if planThenExecuteCandidateReadinessNonSubmitTools >= 12 {
				blockerKind := "plan_execute_candidate_readiness_tool_budget_exhausted"
				emitCompletionStatus("blocked", blockerKind, blockerKind, collectedResults, true)

				answer := "<<QUESTION>>: plan-execute gathered candidate source evidence and made `submit_plan` available, but the planner kept requesting additional tools instead of producing a terminal plan artifact, source-satisfied decision, or exact blocker. Rerun with a narrower objective or exact edit anchor if a plan artifact is still required."

				return finish(answer, nil, blockerKind, true)
			}
		}

		if decision := r.implementationTargetWriteReadyHiddenShellRedirect(req, collectedResults, currentAllowedTools, discoveryRedirect); decision.ShouldRedirect {
			result := decision.Result

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "discovery_redirect_target_write_ready_hidden_shell", runnerRecordOptions{})
			r.emitEvent(RunEvent{Role: "discovery_redirect_target_write_ready_hidden_shell", Content: fmt.Sprintf("%s → %s", req.Tool, decision.Capability)})

			continue
		}

		if r.implementationTargetWriteReadySourceReadAllowed(req, collectedResults, implementationTargetWriteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			implementationTargetWriteReadySourceRefreshes++

			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed write-ready %s source expansion on target path %q", strings.TrimSpace(req.Tool), NormalizeRunnerPath(req.Path)),
			})
		} else if prompt, ok := r.implementationTargetWriteReadyNonWritePrompt(req, collectedResults); ok {
			implementationTargetWriteReadyNudges++

			requestBudget.writeReadyNudgeActive = true

			nudgeMsg := targetWriteReadyStagedNudge(prompt, req, implementationTargetWriteReadyNudges)

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_recovery", Content: nudgeMsg})
			r.emitEvent(RunEvent{Role: "implementation_target_write_ready_nudge", Content: nudgeMsg})
			r.emitImplementationLifecycleState(collectedResults, "target_write_ready", implementationTargetWriteReadyNudges)
		}

		requestedTool := strings.TrimSpace(req.Tool)
		toolHiddenByActiveSurface := runState.surfaceHidesTool(requestedTool)
		scopedHiddenNonWrite := runState.surfaceHiddenByOwner(OwnerImplementationScoped, requestedTool) && !workspace.IsWorkspaceWriteTool(req.Tool)
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

		if !toolHiddenByActiveSurface {
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
					runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
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
				Content: fmt.Sprintf("allowed bounded %s for source-satisfied evidence path %q", strings.TrimSpace(req.Tool), NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if refreshReason, ok := r.planThenExecuteDirectSourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes); ok {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for %s %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, refreshReason, NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if redirected, redirectedRaw, ok := r.planThenExecuteRejectedPlanRefreshReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_rejected_plan_source_refresh_region")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("redirected bounded %s source refresh %d/%d for rejected plan path %q", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
			})
		} else if r.planThenExecuteCandidateSourceRefreshAllowed(req, collectedResults, planThenExecuteReadySourceRefreshes) {
			recoveryReadAllowanceGrantedThisRequest = true
			planThenExecuteReadySourceRefreshes++
			r.emitEvent(RunEvent{
				Role:    "plan_then_execute_ready_source_refresh_allowed",
				Content: fmt.Sprintf("allowed bounded %s source refresh %d/%d for candidate path %q after rejected submit_plan showed a stale or missing anchor", strings.TrimSpace(req.Tool), planThenExecuteReadySourceRefreshes, planThenExecuteReadySourceRefreshLimit, NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))),
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
					if result, ok := r.planThenExecuteAnchorRefreshWriteOnlyRejection(req, collectedResults); ok {
						controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")
						refreshPlanThenExecuteTerminalSurface("anchor_refresh_recovery")

						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: result.Error})
						opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
						r.emitEvent(RunEvent{Role: "plan_then_execute_anchor_refresh_required", Content: result.Error})

						continue
					}

					refreshNote := ""
					if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(collectedResults); ok {
						refreshNote = " " + planThenExecutePostSubmitRejectionRefreshNote(r.RuntimeBrief, collectedResults, rejected, rejectedAt)
					}

					result := workspace.ToolResult{
						Tool:    req.Tool,
						Summary: "Plan-execute submit_plan required",
						Error:   fmt.Sprintf("%s is not available while plan-execute planner recovery is waiting for a visible `submit_plan`. The next visible action must be a parser-readable `submit_plan`. %s%s", strings.TrimSpace(req.Tool), planThenExecuteSubmitPlanCoverageSentence(), refreshNote),
					}
					emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)
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

					runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
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

		hiddenCheckTool := strings.TrimSpace(req.Tool)
		if runState.surfaceHidesTool(hiddenCheckTool) && !recoveryReadAllowanceGrantedThisRequest {
			hiddenSummary := "Tool hidden by active contract"
			hiddenError := fmt.Sprintf("%s is hidden for the rest of this turn by the active runtime contract. Use an available tool, or answer with a concrete blocker if the contract prevents completion.", strings.TrimSpace(req.Tool))

			if strings.TrimSpace(req.Tool) == workspace.ToolNameTask && requestBudget.readOnlyChildTaskSatisfied && !isWriteChildMode(req.Mode) {
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
			runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
			rebuildTools()

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
			r.emitEvent(RunEvent{Role: "implementation_write_only_recovery", Content: prompt})

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMissingTargetReadRedirect(req, collectedResults); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_missing_target_read_ranked_candidate")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		planningTransport := r.planExecutePlanningTransportFromOptions(opts, terminalSubmitPlanNativeTools(), false)
		planningState := DerivePlanExecutePlanningActionState(r.RuntimeBrief, collectedResults, planningTransport, req)

		if !recoveryReadAllowanceGrantedThisRequest {
			if reason, prompt, ok := planThenExecuteCandidateLowValueAction(req, r.RuntimeBrief, collectedResults, planningTransport); ok {
				r.emitEvent(marshalRunEvent("plan_then_execute_low_value_action", map[string]any{
					"agent":             r.agentName(),
					"tool":              strings.TrimSpace(req.Tool),
					"path":              NormalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
					"reason":            reason,
					"terminal_options":  planningState.TerminalOptions,
					"low_value_actions": planningState.LowValueActions,
				}))

				if slices.Contains(planningState.TerminalOptions, workspace.ToolNameSubmitPlan) {
					opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
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
			emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)

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
		historyRedirectAllowedTools := append(append([]string(nil), surfaceAllowedTools...), workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol)

		surfaceDecision := toolAuthorizationSurfaceDecision(req, surfaceAllowedTools, false)
		if surfaceDecision.Rejected && !recoveryReadAllowanceGrantedThisRequest {
			summary, errorText := surfaceDecision.Summary, surfaceDecision.Error
			redirected := false

			if planThenExecuteUnscopedEmptySubmitPlanAttempt(r.RuntimeBrief, req) {
				emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)

				result := workspace.ToolResult{
					Tool:    workspace.ToolNameSubmitPlan,
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
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_submit_plan_before_history")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, redirectedRaw, ok := r.sourceReadBeforeGitHistoryEvidenceRedirect(req, collectedResults, historyRedirectAllowedTools); ok {
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_source_read_before_history")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if redirectedReq, ok := postWriteSourceSanityRedirect(req, r.RuntimeBrief, collectedResults, surfaceAllowedTools); ok {
				redirectedRaw := marshalToolRequestRaw(redirectedReq)
				req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "post_write_lane_sanity_requires_source_sanity")
				currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
				redirected = true
			} else if historySummary, historyError, ok := planThenExecuteUnavailableHistoryToolNudge(req, r.RuntimeBrief, collectedResults, surfaceAllowedTools); ok {
				summary = historySummary
				errorText = historyError
			} else if submitCorrectionSummary, submitCorrectionError, prompt, ok := planThenExecuteSubmitCorrectionCurrentSurfaceRejection(req, r.RuntimeBrief, collectedResults); ok {
				summary = submitCorrectionSummary
				errorText = submitCorrectionError
				emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)
				refreshPlanThenExecuteTerminalSurface("submit_correction_wrong_tool")
				syncPlanThenExecuteRecoveryAllowedTools()
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
				emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)
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
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirectedReq, redirectedRaw, "plan_execute_submit_plan_before_history")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.implementationTargetSourceWriteBeforeEvidenceRedirect(req, collectedResults, currentAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "implementation_write_before_evidence_bounded_read")
			deferredSourceEvidenceWrite = r.newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path, currentAllowedTools)
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if summary, errorText, reject := r.implementationTargetSourceWriteBeforeEvidenceRejection(req, collectedResults, currentAllowedTools); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "implementation_target_source_write_rejected", true)
			}

			continue
		}

		implementationHistoryRedirectAllowedTools := append(append([]string(nil), currentAllowedTools...), workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol)
		if redirected, redirectedRaw, ok := r.writeBeforeGitHistoryEvidenceRedirect(req, collectedResults, implementationHistoryRedirectAllowedTools); ok {
			originalReq := req
			originalRaw := raw
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "write_before_git_history_bounded_history")
			deferredSourceEvidenceWrite = r.newImplementationDeferredSourceEvidenceWrite(originalReq, originalRaw, redirected.Path, implementationHistoryRedirectAllowedTools)

			if deferredSourceEvidenceWrite != nil {
				deferredSourceEvidenceWrite.RequiresHistory = true
			}

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
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_candidate_rangeless_read_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteMalformedSymbolReadRedirect(req, collectedResults, currentAllowedTools); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_malformed_symbol_read_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, ok := rewriteVerificationShellReadToSourceSanity(req, r.RuntimeBrief, collectedResults, currentAllowedTools); ok {
			redirectedRaw := marshalToolRequestRaw(redirected)
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":                      "normalized",
				"transport":                    "verification_shell_read_rewrite",
				"tool":                         strings.TrimSpace(redirected.Tool),
				"path":                         strings.TrimSpace(redirected.Path),
				"original_tool":                strings.TrimSpace(req.Tool),
				"original_raw":                 strings.TrimSpace(raw),
				"rewritten_raw":                strings.TrimSpace(redirectedRaw),
				"reason":                       "source_edit_verification_requires_source_sanity",
				workspace.ToolNameSourceSanity: true,
				"changed_source":               true,
			}))

			turnMessages = append(turnMessages, llama.ChatMessage{
				Role:    "system",
				Content: rewriteVisibilityNote(req.Tool, redirected.Tool),
			})
			req = redirected
			raw = redirectedRaw
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		if redirected, ok := fillSourceSanityPathFromSingleChangedSource(req, r.RuntimeBrief, collectedResults, currentAllowedTools); ok {
			redirectedRaw := marshalToolRequestRaw(redirected)
			r.emitEvent(marshalRunEvent("tool_request_normalizer", map[string]any{
				"outcome":                      "normalized",
				"transport":                    "source_sanity_path_repair",
				"tool":                         strings.TrimSpace(redirected.Tool),
				"path":                         strings.TrimSpace(redirected.Path),
				"original_tool":                strings.TrimSpace(req.Tool),
				"original_raw":                 strings.TrimSpace(raw),
				"rewritten_raw":                strings.TrimSpace(redirectedRaw),
				"reason":                       "source_edit_verification_requires_changed_source_path",
				workspace.ToolNameSourceSanity: true,
				"changed_source":               true,
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

		// Narrow-tool repetition nudge: if the model repeatedly calls the
		// same tool family (code_navigate, read_file, search_*) without
		// trying a broader discovery tool, suggest a strategy shift.
		// This catches patterns like 50 consecutive code_navigate
		// read_symbol calls — each targeting a different symbol, so
		// same-path fixation and fingerprint hard block never fire.
		if narrowFamily := workspace.RuntimeNarrowToolFamily(req.Tool); narrowFamily != "" {
			if narrowFamily == lastNarrowToolFamily {
				consecutiveNarrowToolCalls++
			} else {
				consecutiveNarrowToolCalls = 1
				lastNarrowToolFamily = narrowFamily
			}

			// Only nudge if the model has been doing the same narrow
			// family for a while and hasn't used a broad discovery tool
			// recently (tracked via discovery tool reset below).
			if consecutiveNarrowToolCalls >= narrowToolFamilyNudgeAt && !narrowToolFamilyNudgeFired {
				narrowToolFamilyNudgeFired = true
				prompt := narrowToolFamilyRepetitionNudgePrompt(narrowFamily, consecutiveNarrowToolCalls, currentAllowedTools)
				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "narrow_tool_repetition_nudge", Content: prompt})
			}
		} else {
			// Reset on any broad/non-narrow tool — the model tried
			// something different, so clear the repetition tracking.
			consecutiveNarrowToolCalls = 0
			lastNarrowToolFamily = ""
			narrowToolFamilyNudgeFired = false
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

		if summary, errorText, rejectionCode, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject {
			if override, denialContext := r.ReadApprovalGate.MaybeOverrideRejection(ctx, r.sessionID(), req, r.RuntimeBrief, rejectionCode, errorText); override {
				// Gate approved the read — fall through to normal tool dispatch
			} else {
				if denialContext != "" {
					errorText = strings.TrimSpace(errorText) + denialContext
				}

				answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
				if done {
					return finish(answer, err, "plan_then_execute_candidate_post_readiness_discovery_rejected", true)
				}

				if summary == "Anchor refresh capability required" {
					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
					r.emitEvent(RunEvent{Role: "plan_then_execute_anchor_refresh_required", Content: errorText})

					continue
				}

				prompt := r.planThenExecuteSubmitReadyTerminalPrompt(errorText, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
				transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_ready_terminal_required", "candidate_post_readiness_discovery")

				continue
			}
		}

		if summary, errorText, reject := r.repeatedRangeLessReadFileRejection(req, collectedResults); reject {
			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)
			if done {
				return finish(answer, err, "repeated_rangeless_read_file_rejected", true)
			}

			continue
		}

		if redirected, redirectedRaw, ok := r.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, collectedResults, currentAllowedTools); ok {
			req, raw = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_symbol_read_after_miss_bounded")
			currentFingerprint = req.Tool + "|" + normalizedToolRequestRaw(req)
		}

		toolKey := toolRequestKey(req)
		targetWriteReadyReadLoopDefers := r.implementationTargetWriteReadyReadLoopDefersToEvidenceContract(req, collectedResults)
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

		if shouldRejectExactReplayBeforeLoopGate(req) && toolKey != "" && toolKey == lastToolKey && replayedToolRequests[toolKey] > 0 &&
			!targetWriteReadyReadLoopDefers {
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
		if workspace.IsReadTool(strings.TrimSpace(req.Tool)) &&
			!targetWriteReadyReadLoopDefers {
			if decision := attachLoopPolicyDecision(requestBudget.samePathFixationDecision(req, genericReadOnlyEvidenceReady(r.RuntimeBrief, collectedResults))); decision.Handled {
				if autonomyRecorder != nil && decision.Policy.InvariantID != "" {
					autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
						Decision:    decision.Policy.Decision,
						PackID:      decision.Policy.ContractID,
						InvariantID: decision.Policy.InvariantID,
						ReasonCode:  decision.Policy.ReasonCode,
						Result:      runnerLoopPolicyResultLabel(decision.Policy.Decision),
						ErrorClass:  "loop_policy",
						Source:      "runner_loop_policy",
					})
				}
				if tool := strings.TrimSpace(decision.HideTool); runState.hideSurfaceTool(OwnerLoopPolicy, tool) {
					rebuildTools()
					r.emitEvent(RunEvent{Role: "tool_hidden", Content: fmt.Sprintf("%s hidden for rest of turn after same-path fixation", tool)})
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: decision.Nudge})
				r.emitEvent(RunEvent{Role: decision.EventRole, Content: textutil.FirstNonEmpty(decision.EventContent, decision.Nudge)})

				continue
			}
		}

		if identicalCallRun >= 3 {
			if result, prompt, ok := r.mutationNoWriteLoopRecovery(req, collectedResults, identicalCallRun); ok &&
				!targetWriteReadyReadLoopDefers {
				controller.recordRejectedResult(stepNumber, activeToolCall, req, result, "tool_rejected")

				implementationWriteOnlyRecoveryActive = true

				markRuntimeBriefPathsObserved(r.Workspace, r.RuntimeBrief)
				runState.hideImplementationWriteOnlyTools(r.workspaceAllowedTools())
				rebuildTools()

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
				r.emitEvent(RunEvent{Role: "mutation_no_write_loop_recovery", Content: prompt})

				continue
			}

			if !planThenExecuteLoopBudgetRecoveryUsed {
				if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments %d turns in a row", req.Tool, identicalCallRun), chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
					planThenExecuteLoopBudgetRecoveryUsed = true
					opts = forceVisibleActionChatOptions(opts)

					if recovery.TerminalOnly && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
						opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
						opts.ToolChoice = "auto"
					}

					syncCurrentAllowedToolsFromOptions()

					if recovery.TerminalOnly {
						forcePlanThenExecuteTerminalSurface("loop_budget_recovery")

						currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
						setCurrentAllowedTools(currentAllowedTools)
					}

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
					r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

					continue
				}
			} else if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fmt.Sprintf("the model repeated %s with the same arguments after a loop-budget recovery", req.Tool), chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}

			// Conversation lanes: repeated reads on the same file are
			// legitimate investigation, not loops. The model is probing
			// different symbols, line ranges, or patterns — not stuck.
			// Skip the doom-loop gate for read-class tools in conversation;
			// same-path fixation is handled by requestBudget before this
			// exact-argument branch so varied ranges are covered too.
			if (r.lanePolicy().skipFingerprintHardBlock ||
				targetWriteReadyReadLoopDefers) &&
				workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
				requestBudget.fingerprint.identicalRun = 0
			} else if r.DoomLoopGate != nil {
				if autonomyRecorder != nil {
					autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", currentFingerprint, identicalCallRun)
				}
				r.emitEvent(RunEvent{Role: "doom_loop_guard", Content: fmt.Sprintf("model repeated %s %d times — approval required", req.Tool, identicalCallRun)})

				if r.DoomLoopGate(req, identicalCallRun) {
					// User approved — reset the streak and let this call execute.
					requestBudget.fingerprint.identicalRun = 0
				} else {
					if answer, reason, exhausted, ok := r.reviewRepairDoomLoopTerminalCloseout(collectedResults); ok {
						r.emitEvent(RunEvent{Role: "plan_then_execute_review_repair_doom_loop_closeout", Content: reason})

						return finish(answer, nil, reason, exhausted)
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
				if autonomyRecorder != nil {
					autonomyRecorder.emitLoopGate(req, autonomy.PolicyStop, "doom_loop_guard", currentFingerprint, identicalCallRun)
				}
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

				return finish(answer, err, "doom_loop_guard", true)
			}
		}

		requestBudget.setRecoveryContext(r.RuntimeBrief, collectedResults)
		fingerprintDecision := attachLoopPolicyDecision(requestBudget.fingerprintDecision(policy, req, currentFingerprint))
		if targetWriteReadyReadLoopDefers &&
			(fingerprintDecision.ForceAnswerReason != "" || fingerprintDecision.RejectSummary != "") {
			r.emitEvent(RunEvent{
				Role:    "implementation_target_write_ready_read_loop_policy_deferred",
				Content: fmt.Sprintf("deferred %s repeated-call terminal decision for read-class tool %s while target-write-ready implementation evidence remains unmet", textutil.FirstNonEmpty(fingerprintDecision.EventRole, fingerprintDecision.ForceAnswerReason, fingerprintDecision.RejectStopReason), strings.TrimSpace(req.Tool)),
			})
			fingerprintDecision = runnerRequestBudgetDecision{}
		}
		if !planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				planThenExecuteLoopBudgetRecoveryUsed = true
				opts = forceVisibleActionChatOptions(opts)

				if recovery.TerminalOnly && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()

				if recovery.TerminalOnly {
					forcePlanThenExecuteTerminalSurface("loop_budget_recovery")

					currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
					setCurrentAllowedTools(currentAllowedTools)
				}

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}
		} else if planThenExecuteLoopBudgetRecoveryUsed && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
			if recovery, ok := r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, collectedResults, fingerprintDecision.ForceAnswerPrompt, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan)); ok {
				opts = forceVisibleActionChatOptions(opts)
				if chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "auto"
				}

				syncCurrentAllowedToolsFromOptions()
				forcePlanThenExecuteTerminalSurface("loop_budget_recovery_ignored")

				currentAllowedTools = append(currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
				setCurrentAllowedTools(currentAllowedTools)

				turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
				r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

				continue
			}
		}

		// Conversation lanes: the fingerprint hard block's forced
		// completion cuts off legitimate investigation. Clear the
		// ForceAnswerPrompt before apply() so the model can continue.
		if r.lanePolicy().skipFingerprintHardBlock && fingerprintDecision.ForceAnswerReason == "fingerprint_hard_block_exhausted" {
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
			!r.lanePolicy().skipSynthesisNudge {

			if res := requestBudget.synthesisDecision(policy, metrics.ToolCalls, runnerBriefRequiresMutation(r.RuntimeBrief)).apply(budgetDecisionContext{
				controller: &controller, stepNumber: stepNumber, activeToolCall: activeToolCall, req: req,
			}); res.shouldFinish {
				return finish(res.answer, res.err, res.reason, true)
			} else if res.shouldContinue {
				continue
			}
		}

		if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(req, r.RuntimeBrief, collectedResults); reject && !recoveryReadAllowanceGrantedThisRequest {
			if r.planThenExecutePlannerPhase() {
				emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyReject)
			}

			answer, err, done := controller.rejectTool(stepNumber, activeToolCall, req, summary, errorText)

			if r.planThenExecutePlannerPhase() {
				if planThenExecutePostReadinessRejectionRequiresSubmitReadyTransition(summary) {
					prompt := r.planThenExecuteSubmitReadyTerminalPrompt(errorText, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
					transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_plan_required", "corrected_submit_plan_required")
				} else {
					rebuildTools()

					opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

					syncPlanThenExecuteRecoveryAllowedTools()
					r.refreshPlanThenExecuteTerminalToolSurface(&turnMessages, collectedResults, "corrected_submit_plan_required")

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: errorText})
					r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_required", Content: errorText})
				}
			}

			if done {
				return finish(answer, err, "plan_then_execute_post_readiness_discovery_rejected", true)
			}

			continue
		}

		if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
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
			recorder.record(stepNumber, req, noteResult, "note_tool", runnerRecordOptions{})
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
				recorder.record(stepNumber, req, result, "discovery_redirect_conversation_read", runnerRecordOptions{})
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
					if tool := strings.TrimSpace(req.Tool); r.shouldHideToolAfterDiscoveryHardRedirect(req) && runState.hideSurfaceTool(OwnerDiscoveryRedirect, tool) {
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
				recorder.record(stepNumber, req, result, "discovery_redirect", runnerRecordOptions{})

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
				req, _ = r.rewriteToolRequest(&turnMessages, req, raw, redirected, redirectedRaw, "plan_execute_symbol_read_after_read_failure_bounded")
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

		if textutil.EqualTrimFold(req.Tool, workspace.ToolNameListFiles) && executionState.consecutiveListFiles >= 2 {
			// Hide list_files from the manifest for the rest of the turn
			// — at this point we've told the model "stop walking dirs"
			// once already, and keeping list_files in the tool list just
			// invites another rejected call. Mode-gating the tool out is
			// more effective than rejecting each retry with a message the
			// model already ignored.
			if runState.hideSurfaceTool(OwnerListFilesGate, workspace.ToolNameListFiles) {
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

		if toolKey != "" && toolKey == lastToolKey && strings.TrimSpace(req.Tool) != "go_file_outline" &&
			!targetWriteReadyReadLoopDefers {
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

					if policy.shouldStopForNoProgress(consecutiveNoProgress) {
						answer, err := controller.completeWithoutMoreToolsWithReason("Repeated tool request reused prior evidence, but the no-progress budget is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the repeated request leaves one exact claim unsupported, state that uncertainty after the supported answer.")
						return finish(answer, err, "no_progress_budget_exhausted", false)
					}

					continue
				}
			}

			errorText := "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes."
			if strings.TrimSpace(req.Tool) == workspace.ToolNameGoMoveDeclarations {
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

		permissionDecision := permissionPolicyDecision(r.PermissionPolicy, req)
		if r.Workspace != nil && r.Workspace.EvaluateConfiguredToolRule(req) == toolpolicy.Deny {
			permissionDecision = PermissionDecisionSnapshot{
				Tool:     strings.TrimSpace(req.Tool),
				Target:   PermissionTarget(req),
				Decision: toolpolicy.Deny,
				Rule:     "workspace.tool_rules decision=deny",
			}
		} else if permissionDecision.Decision == toolpolicy.Allow {
			if readOnlyWriteChildDecision, ok := r.readOnlyWriteChildTaskPermissionDecision(req); ok {
				permissionDecision = readOnlyWriteChildDecision
			}
		}

		if autonomyRecorder != nil {
			intentPolicyInput := normalizedPolicyInput
			intentPolicyInput.EventType = autonomy.EventToolIntentClassified
			intentPolicyInput.Source = "runner_tool_intent"
			autonomyRecorder.emitIntentClassified(req, intentPolicyInput)

			pathPolicyInput := normalizedPolicyInput
			pathPolicyInput.EventType = autonomy.EventPathRolesResolved
			pathPolicyInput.Source = "runner_path_roles"
			autonomyRecorder.emitPathRolesResolved(req, pathPolicyInput)
		}

		if permissionDecision.Decision != toolpolicy.Allow {

			if autonomyRecorder != nil {
				decision := autonomy.PolicyDeny
				resultLabel := "denied"

				if permissionDecision.Decision == toolpolicy.Ask {
					decision = autonomy.PolicyAsk
					resultLabel = "approval_required"
				}

				autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
					Decision:         decision,
					PackID:           autonomy.ContractPackToolAuthorization,
					InvariantID:      toolAuthorizationPolicyInvariant,
					ReasonCode:       "permission_policy",
					Transport:        normalizedPolicyInput.Transport,
					ParserFailure:    normalizedPolicyInput.ParserFailure,
					RepairSource:     normalizedPolicyInput.RepairSource,
					ApprovalRequired: permissionDecision.Decision == toolpolicy.Ask,
					Result:           resultLabel,
					ErrorClass:       "permission_policy",
					Source:           strings.TrimSpace(permissionDecision.Rule),
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

			if permissionDecision.Decision == toolpolicy.Ask {
				answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()
				return finish(answer, err, "permission_approval_required", false)
			}

			continue
		}

		if autonomyRecorder != nil {
			autonomyRecorder.emitPolicyResult(req, runnerAutonomyPolicyEventInput{
				Decision:      autonomy.PolicyAllow,
				PackID:        autonomy.ContractPackToolAuthorization,
				InvariantID:   toolAuthorizationPolicyInvariant,
				ReasonCode:    "permission_policy",
				Transport:     normalizedPolicyInput.Transport,
				ParserFailure: normalizedPolicyInput.ParserFailure,
				RepairSource:  normalizedPolicyInput.RepairSource,
				Result:        "no_approval_needed",
				Source:        strings.TrimSpace(permissionDecision.Rule),
			})
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTask {
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
				runState.hideSurfaceTools(OwnerTaskDelegation, workspace.ToolNameTask)

				rebuildTools()

				if ReadOnlyTaskResultAnswerableForObjective(result, r.RuntimeBrief.Objective) {
					evidencePaths := postChildClosureEvidencePaths(r.RuntimeBrief.Objective, []workspace.ToolResult{result}, 4)

					requestBudget.broadEvidenceDelegationSatisfied = true
					r.RuntimeBrief.Constraints = textutil.Merge(r.RuntimeBrief.Constraints, []string{"child_packet_available"})
					postChildPacketActive = true
					postChildFollowupReads = 0
					postChildDiminishingNudgeStage = 0
					r.RuntimeBrief.EvidencePaths = textutil.Merge(r.RuntimeBrief.EvidencePaths, evidencePaths)
					requestBudget.noteReadOnlyChildPacket(evidencePaths)
					runState.hideSurfaceTools(OwnerChildPacket, workspace.BroadDiscoveryToolNames()...)
					rebuildTools()

					requestBudget.postChild.readCalls = 0

					nudge := buildReadinessNudge(ReadinessNudge{
						Headline:          "The delegated read_only child result is now available as an evidence packet.",
						EvidencePathLabel: "Packet-named paths: ",
						EvidencePaths:     evidencePaths,
						Objective:         r.RuntimeBrief.Objective,
						RuntimeBrief:      r.RuntimeBrief,
						Results:           []workspace.ToolResult{result},
						ContinuationScope: "child_packet_available",
						FooterLines: []string{
							"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
							"If you answer now, answer from the packet and inspected source evidence.",
						},
						IncludeProbeSuggestions: true,
					})

					turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
					r.emitEvent(RunEvent{Role: "child_packet_boundary", Content: nudge})
				}
			}

			recorder.record(stepNumber, req, result, "child_task_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				if shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults) {
					continue
				}

				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled after the child task. Tools are now disabled. Use the child-task result and gathered evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the supported answer.")

				return finish(answer, err, "no_progress_budget_exhausted", false)
			}

			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameTaskLedger {
			result := taskLedger.apply(req, collectedResults)

			recorder.activeToolCall = activeToolCall
			recorder.record(stepNumber, req, result, "task_ledger_result", runnerRecordOptions{
				TrackProgress: true,
				ResetRejected: true,
			})

			if policy.shouldStopForNoProgress(consecutiveNoProgress) {
				answer, err := controller.completeWithoutMoreToolsWithReason("Progress appears stalled. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
				return finish(answer, err, "no_progress_budget_exhausted", false)
			}

			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameDelegateEdit {
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
				return finish(answer, err, "no_progress_budget_exhausted", false)
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
							PackID:           autonomy.ContractPackToolAuthorization,
							InvariantID:      toolAuthorizationPolicyInvariant,
							ReasonCode:       "write_approval_required",
							ApprovalRequired: true,
							Result:           "approval_required",
							ErrorClass:       "approval_guard",
							Source:           "runner_pre_execute_guard",
						})
					}

					answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()

					return finish(answer, err, "write_approval_required", false)
				}

				if strings.TrimSpace(guarded.Error) != "" && !bootstrapReadReminder && consecutiveRejectedActions >= 2 {
					answer, err := controller.completeWithoutMoreToolsWithDefaultCloseout()
					return finish(answer, err, "guard_rejection_budget_exhausted", true)
				}

				if bootstrapReadReminder && policy.shouldStopForNoProgress(consecutiveNoProgress) {
					answer, err := controller.completeWithoutMoreToolsWithReason("The implementation target was already available from bootstrap evidence, but progress stalled on duplicate reads. Tools are now disabled. " + forceAnswerTailGatheredEvidence)
					return finish(answer, err, "no_progress_budget_exhausted", false)
				}

				continue
			}
		}

		if guarded := CheckDescription(req); guarded != nil {
			recorder.activeToolCall = activeToolCall
			recorder.recordWithOptions(stepNumber, req, *guarded, writeDescriptionMissingCode, runnerRecordOptions{
				CountRejected:           true,
				IncrementRejectedStreak: true,
				SetLastToolKey:          true,
			})

			continue
		}

		if summary, errorText, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
			if exactAnchorWriteOnlyActive && workspace.IsWorkspaceWriteTool(req.Tool) {
				exactAnchorWriteFailures++
			}

			if autonomyRecorder != nil {
				autonomyRecorder.emitWriteGate(req, autonomy.PolicyReject, "required_edit_scope_rejected", mapKeys(newToolResultEvidenceProjection(collectedResults).ChangedPathSet()))
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
			req = r.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, collectedResults)
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
				result = r.executeWorkspaceWithPreimage(ctx, preimageStore, req)
			default:
				result = r.executeWorkspaceWithPreimage(ctx, preimageStore, req)
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
				Content: fmt.Sprintf("allowed %s source read to establish target-write-ready evidence on %q", strings.TrimSpace(req.Tool), NormalizeRunnerPath(req.Path)),
			})
		}

		if completionGateRejections > 0 && strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Tool) != "completion_gate" {
			completionGateRejections = 0

			r.emitEvent(RunEvent{Role: "completion_gate_recovery_progress", Content: "successful tool result reset the completion-gate retry streak"})
		}

		update := executionState.applyResult(req, result, currentFingerprint, r.Workspace, &requestBudget)
		if autonomyRecorder != nil && workspaceWriteResultCompleted(req, result) {
			autonomyRecorder.emitWriteGate(req, autonomy.PolicyAllow, "write_result_completed", mapKeys(newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathSet()))
		}

		if deferredGitHistorySubmitPlan != nil && slices.Contains([]string{workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol}, strings.TrimSpace(req.Tool)) {
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

		if deferredSourceEvidenceWrite != nil && slices.Contains([]string{workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol}, strings.TrimSpace(req.Tool)) {
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

			opts = completionGateRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			opts = forceVisibleActionChatOptions(opts)

			rebuildTools()
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
			len(newToolResultEvidenceProjection(collectedResults).ChangedPathSet()) > 0 &&
			!hasPendingVerificationAfterWrites(collectedResults, r.RuntimeBrief) &&
			!sourceEditsNeedSemanticVerification(r.RuntimeBrief, collectedResults) {

			_ = r.emitPostWriteVerificationTelemetry(req, result)

			answer, err := controller.completeWithoutMoreToolsWithReason("The requested implementation has a recorded workspace write and successful source-aware verification. Tools are now disabled. Write the implementation result now: changed paths and verification status.")

			return finish(answer, err, "source_edit_closeout_after_verification", false)
		}

		if exactAnchorWriteOnlyActive && workspaceWriteResultCompleted(req, result) {
			exactAnchorWriteOnlyActive = false

			runState.releaseSurfaceHiddenTools(OwnerExactAnchorRead)
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

			runState.releaseImplementationWriteOnlyTools()
			rebuildTools()
			r.emitEvent(RunEvent{Role: "tool_hidden", Content: "non-write tools restored after required implementation writes completed"})
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

		if r.planThenExecutePlannerPhase() && planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			prompt := planThenExecuteSourceSatisfiedTerminalRequiredPrompt(r.RuntimeBrief, result)
			eventRole := "plan_then_execute_source_satisfied_terminal_required"

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
				if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(collectedResults); ok {
					prompt = planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(r.RuntimeBrief, collectedResults, rejected, rejectedAt)
				}

				eventRole = "plan_then_execute_anchor_refresh_required"
			}

			refreshPlanThenExecuteTerminalSurface("source_satisfied_terminal_required")

			turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: prompt})

			rebuildTools()

			if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh || surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
				opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			} else {
				opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())
			}

			syncPlanThenExecuteRecoveryAllowedTools()
			r.emitEvent(RunEvent{Role: eventRole, Content: prompt})

			continue
		}

		if r.planThenExecutePlannerPhase() && planThenExecuteCandidateSourceReadyResult(result) {
			prompt := r.planThenExecuteSubmitReadyTerminalPrompt(result.Error, collectedResults, chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan))
			transitionPlanThenExecuteSubmitReadyTerminal(prompt, "plan_then_execute_submit_ready_terminal_required", "candidate_source_ready")

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
			prompt := "The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(result) + planThenExecuteSubmitCorrectionOutcomeInstruction(r.RuntimeBrief, collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, result) + " " + refreshNote
			eventRole := "plan_then_execute_submit_plan_required"

			if planThenExecuteUnscopedEmptySubmitPlanRejected(r.RuntimeBrief, collectedResults) {
				emitToolAuthorizationSurfacePolicyResult(autonomyRecorder, req, autonomy.PolicyAllow)
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

		if !planThenExecuteReadinessNudged || !planThenExecuteSubmitOnlyReadinessNudged {
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
				submitOnlyReadiness := planThenExecuteTerminalSubmitPlanSurfaceReady(r.RuntimeBrief, collectedResults)
				if submitOnlyReadiness || !planThenExecuteReadinessNudged {
					planThenExecuteReadinessNudged = true

					if submitOnlyReadiness {
						planThenExecuteSubmitOnlyReadinessNudged = true
						refreshPlanThenExecuteTerminalSurface("readiness")

						if terminalSubmitPlanNativeTools() {
							nudge = planThenExecuteNativeSubmitPlanNudge(r.RuntimeBrief)
						}

						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})

						rebuildTools()

						opts = planThenExecuteReadyToSubmitChatOptionsForBackend(opts, r.RuntimeBrief, collectedResults, terminalSubmitPlanNativeTools())

						syncPlanThenExecuteRecoveryAllowedTools()
						r.emitEvent(RunEvent{Role: "plan_then_execute_readiness_nudge", Content: nudge})
					} else {
						turnMessages = append(turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
						opts = forceVisibleActionChatOptions(opts)
						syncCurrentAllowedToolsFromOptions()
						r.emitEvent(RunEvent{Role: "plan_then_execute_candidate_readiness_nudge", Content: nudge})
					}
				}
			}
		}

		if !planThenExecuteCorrectionAfterRefreshNudged &&
			r.planThenExecutePlannerPhase() &&
			planThenExecuteReadinessNudged &&
			strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan &&
			successfulSubmitPlanResultCount(collectedResults) == 0 {

			recoveryState := DerivePlanExecutePlanningState(r.RuntimeBrief, collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
			if rejectedResult, _, rejected := lastRejectedSubmitPlanCoverageResult(collectedResults); rejected &&
				recoveryState.Mode != PlanExecutePlanningModeAnchorRefresh &&
				len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, collectedResults)) == 0 {

				planThenExecuteCorrectionAfterRefreshNudged = true

				prompt := "The bounded source refresh after the rejected `submit_plan` is complete. " + planThenExecuteSubmitCorrectionOutcomeInstruction(r.RuntimeBrief, collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejectedResult)

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
			r.RuntimeBrief.Constraints = textutil.Merge(r.RuntimeBrief.Constraints, []string{"answer_readiness_reached"})

			if becameReadyFromDelegated {
				r.emitDelegatedReadOnlyAnswerReadyEvent(req, collectedResults)
			} else {
				r.emitReadOnlyAnswerReadyEvent(req, collectedResults)
			}

			if !delegatedReadOnlyAnswerReadyNudged {
				delegatedReadOnlyAnswerReadyNudged = true

				nudge := buildReadinessNudge(ReadinessNudge{
					Headline:          "The delegated read-only evidence is sufficient for a grounded answer to this question.",
					EvidencePathLabel: "Core evidence paths: ",
					EvidencePaths:     closurePromptEvidencePaths(delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, collectedResults, 4), 4),
					Objective:         r.RuntimeBrief.Objective,
					RuntimeBrief:      r.RuntimeBrief,
					Results:           collectedResults,
					ContinuationScope: "delegated_read_only_answer_ready",
					FooterLines: []string{
						"Carry forward the inspected paths above and keep the answer minimal; avoid side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
					},
				})
				eventRole := "delegated_read_only_answer_ready_nudge"

				if becameReadyFromAnalysis {
					nudge = buildReadinessNudge(ReadinessNudge{
						Headline:          "The read-only evidence is sufficient for a grounded answer to this question.",
						EvidencePathLabel: "Core evidence paths: ",
						EvidencePaths:     closurePromptEvidencePaths(readOnlyPresentationEvidencePathsForBrief(r.RuntimeBrief, collectedResults, 4), 4),
						Objective:         strings.TrimSpace(r.RuntimeBrief.Objective),
						RuntimeBrief:      r.RuntimeBrief,
						Results:           collectedResults,
						ContinuationScope: "answer_readiness_reached",
						FooterLines: []string{
							"Anchor the explanation in the inspected paths above and keep any remaining uncertainty explicit.",
						},
					})
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
		if r.lanePolicy().autoCaptureFindings &&
			metrics.ToolCalls == convFirstMilestone && strings.TrimSpace(result.Error) == "" && r.Workspace != nil {

			if _, err := writeAutoFindingsNoteFromResults(r.Workspace.Root(), r.RuntimeBrief.Objective, collectedResults); err == nil {
				turnMessages = append(turnMessages, llama.ChatMessage{
					Role:    "system",
					Content: "The harness has captured your findings so far to .idiom/notes/auto-findings.md. This note persists across context compactions — review it if your working memory is refreshed. Continue investigating, or answer if you have enough evidence.",
				})
			} else {
				r.emitEvent(RunEvent{Role: "note_tool", Content: "Auto findings note rejected: " + err.Error()})
			}
		}

		if childReadyBeforeResult && !postChildPacketActive && workspace.IsReadTool(strings.TrimSpace(req.Tool)) && strings.TrimSpace(result.Error) == "" {
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

		if postChildPacketActive && strings.TrimSpace(req.Tool) != workspace.ToolNameTask && workspace.IsReadTool(strings.TrimSpace(req.Tool)) && strings.TrimSpace(result.Error) == "" {
			postChildFollowupReads++
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
			answer, err := controller.completeWithoutMoreToolsWithReason("Exploration is probing conventional paths that were discovered as index candidates but not read as source-body evidence. Tools are now disabled. Answer from the bootstrapped evidence; if one exact claim remains unsupported, state that uncertainty after the supported answer.")
			return finish(answer, err, "missing_path_probe_budget_exhausted", true)
		}

		if policy.shouldStopForNoProgress(consecutiveNoProgress) {
			return finishNoProgressBudgetExhausted(collectedResults)
		}
	}

	if blocker, ok := r.implementationToolBudgetExhaustedBlocker(collectedResults); ok {
		r.emitImplementationLifecycleState(collectedResults, "implementation_tool_step_budget_blocked", implementationTargetWriteReadyNudges)
		return finish(blocker, nil, "implementation_tool_step_budget_blocked", true)
	}

	answer, err = controller.completeWithoutMoreToolsWithReason(maxToolStepsExhaustedReason())

	return finish(answer, err, "tool_step_budget_exhausted", true)
}

func (r *Runner) planExecutePlanningTransportFromOptions(opts llama.ChatOptions, supportsNativeTools bool, terminalOnly bool) PlanExecutePlanningTransport {
	return PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, terminalOnly).PlanningTransport()
}

func (r *Runner) planThenExecuteCompletionGateSummaryRetryable(summary string, results []workspace.ToolResult, priorGateRejections int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || priorGateRejections >= r.completionGateRetryLimit(results) {
		return false
	}

	switch strings.TrimSpace(summary) {
	case "Plan-execute completion gate rejected final answer",
		"Completion gate rejected non-specific submit_plan blocker",
		"Completion gate rejected non-submit_plan correction":
		return true
	default:
		return false
	}
}

func maxToolStepsExhaustedReason() string {
	return "Maximum tool steps for this turn have been reached. Tools are now disabled. Respond with a concise, direct, text-only final answer synthesized from the evidence already gathered. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action. Do not leave the response blank."
}

func (r *Runner) implementationToolBudgetExhaustedBlocker(results []workspace.ToolResult) (string, bool) {
	return r.implementationContractCloseoutBlocker(results, "implementation_tool_step_budget_blocked")
}

type implementationCloseoutFacts struct {
	StopLabel           string
	ChangedPaths        []string
	RemainingPaths      []string
	SourcedPaths        string
	InspectedEvidence   string
	RepeatedReadPaths   string
	VerificationPending bool
}

func (r *Runner) implementationContractCloseoutBlocker(results []workspace.ToolResult, stopReason string) (string, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return "", false
	}

	facts := implementationCloseoutFacts{
		StopLabel:           implementationCloseoutStopLabel(stopReason),
		ChangedPaths:        mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet()),
		RemainingPaths:      r.remainingRequiredEditPaths(results),
		SourcedPaths:        implementationTargetWriteReadySourcedPathSummary(results, 5),
		InspectedEvidence:   conversationInspectedPathSummary(results),
		RepeatedReadPaths:   implementationCloseoutRepeatedReadSummary(results, 5),
		VerificationPending: hasPendingVerificationAfterWrites(results, r.RuntimeBrief),
	}

	return composeImplementationCloseoutBlocker(facts), true
}

func implementationCloseoutStopLabel(stopReason string) string {
	switch strings.TrimSpace(stopReason) {
	case "implementation_tool_step_budget_blocked", "tool_step_budget_exhausted":
		return "implementation tool budget"
	case "model_call_budget_exhausted":
		return "implementation model-call budget"
	case "operational_model_call_limit_exceeded":
		return "implementation operational model-call limit"
	case "no_progress_budget_exhausted":
		return "implementation no-progress closeout"
	case "duration_budget_exhausted":
		return "implementation duration budget"
	default:
		return "implementation closeout"
	}
}

func composeImplementationCloseoutBlocker(facts implementationCloseoutFacts) string {
	var b strings.Builder

	label := strings.TrimSpace(facts.StopLabel)
	if label == "" {
		label = "implementation closeout"
	}

	b.WriteString("<<QUESTION>>: ")
	b.WriteString(label)
	b.WriteString(" reached before the implementation contract produced a parser-readable write, no-op evidence, or exact blocker.")

	if len(facts.ChangedPaths) > 0 {
		b.WriteString("\n\nChanged paths:\n")
		for _, path := range facts.ChangedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n\nChanged paths: none\n")
	}

	b.WriteString("\nRemaining obligations:\n")
	b.WriteString("- no parser-readable workspace write was recorded\n")
	b.WriteString("- no no-op evidence or exact blocker was produced\n")

	if len(facts.RemainingPaths) > 0 {
		b.WriteString("- required edit path(s) not changed: ")
		b.WriteString(strings.Join(facts.RemainingPaths, ", "))
		b.WriteString("\n")
	}

	if facts.VerificationPending {
		b.WriteString("- verification pending after recorded workspace write(s)\n")
	}

	if sourced := strings.TrimSpace(facts.SourcedPaths); sourced != "" {
		b.WriteString("\nSource-body evidence already gathered:\n- ")
		b.WriteString(sourced)
		b.WriteString("\n")
	}

	if repeated := strings.TrimSpace(facts.RepeatedReadPaths); repeated != "" {
		b.WriteString("\nRepeated allowed read evidence observed:\n- ")
		b.WriteString(repeated)
		b.WriteString("\n")
	}

	if inspected := strings.TrimSpace(facts.InspectedEvidence); inspected != "" {
		b.WriteString("\nLatest inspected evidence:\n- ")
		b.WriteString(inspected)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func implementationCloseoutRepeatedReadSummary(results []workspace.ToolResult, limit int) string {
	counts := make(map[string]int)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		default:
			continue
		}

		for _, path := range ReadPathsFromToolResult(result) {
			path = NormalizeRunnerPath(path)
			if path == "" {
				continue
			}

			counts[path]++
		}
	}

	var repeated []string
	for path, count := range counts {
		if count > 1 {
			repeated = append(repeated, path)
		}
	}

	if len(repeated) == 0 {
		return ""
	}

	return summarizeRunnerPaths(uniqueSortedRunnerPaths(repeated), limit)
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

	submitCorrectionBlockerAllowed := submitCorrection && planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results)
	if len(missing) == 0 ||
		(!submitCorrection && !anchorRefresh && requiredPathBlockerAnswerCovers(reply, missing)) ||
		(submitCorrectionBlockerAllowed && !anchorRefresh && submitCorrectionMissingBodyAnchorBlockerCovers(reply, missing)) {
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
			if submitCorrectionBlockerAllowed {
				return workspace.ToolResult{
					Tool:    "completion_gate",
					Summary: "Completion gate rejected non-specific submit_plan blocker",
					Error:   "required edit path(s) remain uncovered by corrected submit_plan: " + quotedRunnerStrings(missing) + "." + correction + " Submit a corrected parser-readable submit_plan, or answer <<QUESTION>> with one path-specific missing body/anchor blocker reason for every remaining required path. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied` and do not reply in prose.",
				}, true
			}
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

	if !ContainsAny(lower,
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

func submitCorrectionMissingBodyAnchorBlockerCovers(reply string, missing []string) bool {
	if !requiredPathBlockerAnswerCovers(reply, missing) {
		return false
	}

	lower := strings.ToLower(reply)
	if !ContainsAny(lower,
		"missing body",
		"body evidence",
		"missing anchor",
		"anchor blocker",
		"body/anchor",
		"safe edit body",
		"no safe edit",
		"unsafe edit",
		"cannot produce a safe",
		"can't produce a safe",
		"cannot draft a safe",
		"can't draft a safe",
	) {
		return false
	}

	for _, path := range missing {
		idx := strings.Index(reply, path)
		if idx < 0 {
			return false
		}
		windowStart := idx - 160
		if windowStart < 0 {
			windowStart = 0
		}
		windowEnd := idx + len(path) + 240
		if windowEnd > len(reply) {
			windowEnd = len(reply)
		}
		window := strings.ToLower(reply[windowStart:windowEnd])
		if !ContainsAny(window, "missing body", "body evidence", "missing anchor", "anchor blocker", "body/anchor", "safe edit body", "no safe edit", "unsafe edit") {
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

	changedPaths := mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())

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

				if r.planThenExecutePlannerPhase() && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
					b.WriteString(": no successful corrected submit_plan edit entry or source_satisfied_paths entry was recorded for this path. Submit-correction mode has exactly one valid outcome: parser-readable submit_plan.\n")
				} else if r.planThenExecutePlannerPhase() {
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
		if paths := promptEvidencePathsFromToolResults(results, 8); len(paths) > 0 {
			b.WriteString("\n\nEvidence paths observed before rejection:\n")
			for _, path := range paths {
				b.WriteString("- ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		}
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

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan || !planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
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

	return RenderPlanExecuteRecoveryPlan(planThenExecuteRepeatedSubmitCorrectionRecoveryPlan(*lastResult)), true
}

func (r *Runner) planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult) (string, string, bool) {
	if _, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastResult, results, false); !ok {
		return "", "", false
	}

	summary, errorText := planThenExecuteRepeatedCorrectionExhaustedRejection(*lastResult)
	return summary, errorText, true
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base string) string {
	return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce exactly one visible parser-readable `submit_plan` request now. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not call read/search/discovery.\n\n" + base
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(base string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce exactly one visible parser-readable `submit_plan` request now, or answer `<<QUESTION>>:` only with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts. Do not answer `Decision: source-satisfied`, and do not call read/search/discovery.\n\n" + base
	}

	return planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base)
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

func uppercaseFirstSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	first := value[0]
	if first >= 'a' && first <= 'z' {
		return string(first-'a'+'A') + value[1:]
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

		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(result.Summary))
		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(result.Output))
		paths = textutil.Merge(paths, pathparse.MentionedWorkspacePaths(result.Error))

		if len(paths) >= max {
			break
		}
	}

	return closurePromptEvidencePaths(paths, max)
}

func acceptedMentionedEvidenceResultForObjective(result workspace.ToolResult, objective string) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	diagnostic := result.Facts != nil && len(readOnlySourceLikePaths(result.Facts.DiagnosticEvidencePaths, 8)) > 0
	if concreteSourceEvidenceResult(result) || diagnostic {
		return true
	}

	fact, ok := readOnlyDelegatedEvidenceFactFromResult(result)
	if !ok || fact.Output == "" {
		return false
	}

	return readOnlyDelegatedEvidenceDecisionForObjective(objective, fact).AcceptedMentionedEvidenceForObjective
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
			family == string(autonomy.TaskFamilySecurityReview) ||
			family == string(autonomy.TaskFamilyPerformanceReview)
	}

	if !readOnlyPermission {
		return false
	}

	switch family {
	case string(autonomy.TaskFamilyArchitecture), "review":
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
		"the answer above stands",
		"the answer above cites",
		"i have answered the question",
		"the evidence is sufficient and",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer is a hedge about evidence sufficiency rather than a substantive response to the user's question. If a previous gate rejection told you to read a specific path, call `read_file` or `code_navigate` for that path now — do not explain why you cannot answer."
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

	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 && !ReadOnlyExplanationHasMechanismVerb(lower) {
		return "the answer mostly inventories paths or scope and does not explain what the inspected code does. State the concrete behavior those files establish."
	}

	return ""
}

func architectureUnsupportedExactDetail(reply string, results []workspace.ToolResult) []string {
	exactSymbols := ExactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return nil
	}

	mentioned := ExplicitAnswerCodeSymbols(reply)
	if len(mentioned) == 0 {
		return nil
	}

	var unsupported []string

	for _, symbol := range mentioned {
		key := NormalizedAnswerSymbol(symbol)
		if key == "" {
			continue
		}

		if ArchitectureNonSourceSymbolTerm(key) {
			continue
		}

		if _, ok := exactSymbols[key]; ok {
			continue
		}

		if answerSymbolHasExplicitUncertainty(reply, symbol) {
			continue
		}

		unsupported = textutil.AppendUnique(unsupported, symbol)
	}

	return unsupported
}

func sourceBodyEvidenceSymbolCandidates(line string) []string {
	seen := map[string]struct{}{}

	var out []string

	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}

		key := NormalizedAnswerSymbol(raw)
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

	return ContainsAny(lower,
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

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
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

func implementationScopedEditSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if exactAnchorPreWriteSurfaceActive(brief, results) {
		return false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	return len(missingRequiredEditPaths(brief.RequiredEditPaths, changed)) != 0
}

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
	if r == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameApplyPatch {
		return pass
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return pass
	}

	ops, err := patching.Parse(content)
	if err != nil {
		// Let the real applyPatch surface the parse error so the model
		// gets the actual envelope-grammar problem.
		return pass
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	if len(changed) == 0 {
		return pass
	}

	var (
		keptOps []patching.Op
		skipped []string
	)

	for _, op := range ops {
		if op.Kind != patching.OpUpdate && op.Kind != patching.OpDelete {
			keptOps = append(keptOps, op)
			continue
		}

		path := NormalizeRunnerPath(op.Path)
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
				Tool:    workspace.ToolNameApplyPatch,
				Summary: "apply_patch made no new changes (all sections were redundant)",
				Output:  output.String(),
			},
		}
	}

	rewritten := req

	rewritten.Content = patching.Serialize(keptOps)

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

func (r *Runner) implementationTargetWriteReadyReadLoopDefersToEvidenceContract(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return false
	}

	return implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results)
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

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if used[path] {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	// Allow read_file on remaining required edit paths (existing behavior).
	if tool == workspace.ToolNameReadFile {
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
	if tool == workspace.ToolNameCodeNavigate {
		pending := sourceEvidencePendingRedirectsForBrief(r.RuntimeBrief, results)
		for _, ob := range pending {
			if NormalizeRunnerPath(ob.Path) == path {
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

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
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

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
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

const runnerAutonomyEventRole = "autonomy_event"
const runnerAutonomyDisableModelExchangeEnv = "IDIOM_DISABLE_RUNNER_MODEL_EXCHANGE_EVENTS"
const malformedToolPolicyInvariantToolRequest = "parser.tool_request"

type runnerAutonomyRecorder struct {
	runner                 *Runner
	brief                  workerruntime.WorkerRuntimeBrief
	contract               autonomy.AutonomyContract
	activeAuthority        contractcompile.ActiveAuthorityBinding
	activeTurnContract     TurnContract
	plannerCompile         contractcompile.CompileResult
	hasPlannerCompile      bool
	sequence               int64
	occurredAt             time.Time
	obligations            []autonomy.Obligation
	openObligations        map[string]autonomy.Obligation
	closedObligations      map[string]struct{}
	lastTurnPhase          TurnPhaseContract
	lastTurnPhaseEvent     autonomy.TurnPhaseContractEvent
	lastModelProtocol      contractcompile.ModelProtocolProjection
	lastModelPresented     bool
	lastStoredProtocolHash string
}

type runnerAutonomyGateDecision struct {
	Gate                  autonomy.GateKind
	Decision              autonomy.ToolPolicyDecision
	InvariantID           string
	ReasonCode            string
	ToolCallID            string
	ToolName              string
	TargetRefs            []string
	ChangedPaths          []string
	VerificationStatus    string
	Budget                *autonomy.BudgetState
	LoopFingerprintDigest string
	Attempts              int
	Source                string
}

type runnerAutonomyPolicyEventInput struct {
	EventType        autonomy.EventType
	Decision         autonomy.ToolPolicyDecision
	PackID           autonomy.ContractPackID
	InvariantID      string
	ReasonCode       string
	Transport        autonomy.ToolTransport
	ParserFailure    autonomy.ParserFailureClass
	RepairSource     autonomy.RepairSource
	ApprovalRequired bool
	Result           string
	ErrorClass       string
	ChangedPaths     []string
	EvidencePaths    []string
	Source           string
}

type runnerAutonomyGateEventFields struct {
	PackID           autonomy.ContractPackID
	EnforcementMode  autonomy.EnforcementMode
	ObservedFacts    []autonomy.ContractObservedFact
	SafeAlternatives []string
}

func newRunnerAutonomyRecorder(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *runnerAutonomyRecorder {
	if r == nil || r.OnEvent == nil {
		return nil
	}

	compiled, ok := buildRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return nil
	}

	return &runnerAutonomyRecorder{
		runner:            r,
		brief:             workerruntime.NormalizeRuntimeBrief(brief),
		contract:          compiled.contract,
		activeAuthority:   compiled.activeAuthority,
		plannerCompile:    compiled.plannerCompile,
		hasPlannerCompile: compiled.hasPlannerCompile,
		occurredAt:        time.Now().UTC(),
		obligations:       compiled.obligations,
		openObligations:   make(map[string]autonomy.Obligation),
		closedObligations: make(map[string]struct{}),
	}
}

type runnerAutonomyContractBuild struct {
	contract          autonomy.AutonomyContract
	obligations       []autonomy.Obligation
	activeAuthority   contractcompile.ActiveAuthorityBinding
	plannerCompile    contractcompile.CompileResult
	hasPlannerCompile bool
}

func buildRunnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (runnerAutonomyContractBuild, bool) {
	if planThenExecutePlannerBrief(brief) {
		compiled, ok := compileRunnerPlanExecutePlannerContract(r, policy, brief, results)
		if !ok {
			return runnerAutonomyContractBuild{}, false
		}

		return runnerAutonomyContractBuild{
			contract:          compiled.Contract,
			obligations:       append([]autonomy.Obligation(nil), compiled.Contract.Obligations...),
			activeAuthority:   compiled.ActiveAuthority,
			plannerCompile:    compiled,
			hasPlannerCompile: true,
		}, true
	}

	contract, obligations, ok := legacyRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return runnerAutonomyContractBuild{}, false
	}

	return runnerAutonomyContractBuild{
		contract:        contract,
		obligations:     obligations,
		activeAuthority: runnerLegacyActiveAuthorityBinding(contract),
	}, true
}

func compileRunnerPlanExecutePlannerContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (contractcompile.CompileResult, bool) {
	input, ok := runnerPlanExecutePlannerInput(brief, results, TurnPhaseContract{}, policy, false, "")
	if !ok {
		return contractcompile.CompileResult{}, false
	}

	compiled, err := contractcompile.CompilePlanExecutePlanner(input)
	if err != nil {
		if r != nil {
			r.emitEvent(marshalRunEvent("runner_autonomy_contract_gap", map[string]any{
				"task_family": strings.TrimSpace(brief.TaskFamily),
				"reason":      "compile_failed",
				"error":       err.Error(),
			}))
		}

		return contractcompile.CompileResult{}, false
	}

	return compiled, true
}

func runnerLegacyActiveAuthorityBinding(contract autonomy.AutonomyContract) contractcompile.ActiveAuthorityBinding {
	return contractcompile.ActiveAuthorityBinding{
		ContractID:        contract.ContractID,
		RuntimeContractID: strings.TrimSpace(contract.Scope.RuntimeContract),
		ActiveInvariantID: contractcompile.InvariantActiveAuthority,
	}
}

func (a *runnerAutonomyRecorder) activeAuthorityContractID() string {
	if a == nil {
		return ""
	}

	if a.hasPlannerCompile && strings.TrimSpace(a.activeAuthority.ContractID) != "" {
		return a.activeAuthority.ContractID
	}

	return a.contract.ContractID
}

func (a *runnerAutonomyRecorder) activeAuthorityInvariantID() string {
	if a == nil {
		return contractcompile.InvariantActiveAuthority
	}

	if strings.TrimSpace(a.activeAuthority.ActiveInvariantID) != "" {
		return a.activeAuthority.ActiveInvariantID
	}

	return contractcompile.InvariantActiveAuthority
}

func (a *runnerAutonomyRecorder) activeAuthorityForProtocol(protocol contractcompile.ModelProtocolProjection) contractcompile.ActiveAuthorityBinding {
	if a == nil {
		return contractcompile.ActiveAuthorityBinding{}
	}

	binding := a.activeAuthority
	if strings.TrimSpace(binding.ContractID) == "" {
		binding.ContractID = a.contract.ContractID
	}

	if strings.TrimSpace(binding.RuntimeContractID) == "" {
		binding.RuntimeContractID = strings.TrimSpace(a.contract.Scope.RuntimeContract)
	}

	if a.hasPlannerCompile {
		if strings.TrimSpace(protocol.ProtocolHash) != "" {
			binding.ProtocolHash = protocol.ProtocolHash
		} else if strings.TrimSpace(binding.ProtocolHash) == "" {
			binding.ProtocolHash = protocol.ProtocolHash
		}

		if strings.TrimSpace(binding.ActiveInvariantID) == "" {
			binding.ActiveInvariantID = textutil.FirstNonEmpty(strings.TrimSpace(protocol.ActiveInvariantID), contractcompile.InvariantActiveAuthority)
		}

		return binding
	}

	if strings.TrimSpace(protocol.ContractID) != "" {
		binding.ContractID = protocol.ContractID
	}

	if strings.TrimSpace(protocol.ProtocolHash) != "" {
		binding.ProtocolHash = protocol.ProtocolHash
	}

	if strings.TrimSpace(protocol.ActiveInvariantID) != "" {
		binding.ActiveInvariantID = protocol.ActiveInvariantID
	}

	if strings.TrimSpace(binding.ActiveInvariantID) == "" {
		binding.ActiveInvariantID = contractcompile.InvariantActiveAuthority
	}

	return binding
}

func (a *runnerAutonomyRecorder) bindModelProtocolProjection(protocol contractcompile.ModelProtocolProjection) contractcompile.ModelProtocolProjection {
	if a == nil {
		return protocol
	}

	if a.hasPlannerCompile {
		binding := a.activeAuthority
		if strings.TrimSpace(binding.ContractID) == "" {
			binding.ContractID = a.contract.ContractID
		}

		if strings.TrimSpace(binding.ActiveInvariantID) == "" {
			binding.ActiveInvariantID = contractcompile.InvariantActiveAuthority
		}

		return protocolWithActiveAuthority(protocol, binding)
	}

	return protocolWithActiveAuthority(protocol, a.activeAuthorityForProtocol(protocol))
}

func protocolWithActiveAuthority(protocol contractcompile.ModelProtocolProjection, binding contractcompile.ActiveAuthorityBinding) contractcompile.ModelProtocolProjection {
	if strings.TrimSpace(binding.ContractID) != "" {
		protocol.ContractID = binding.ContractID
	}

	if strings.TrimSpace(binding.ProtocolHash) != "" {
		protocol.ProtocolHash = binding.ProtocolHash
	}

	if strings.TrimSpace(binding.ActiveInvariantID) != "" {
		protocol.ActiveInvariantID = binding.ActiveInvariantID
	}

	return protocol
}

func runnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
	compiled, ok := buildRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return autonomy.AutonomyContract{}, nil, false
	}

	return compiled.contract, compiled.obligations, true
}

func legacyRunnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	lane := textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName())

	if lane == "" {
		lane = "main"
	}

	scope := autonomy.ContractScope{
		InvocationKind:    autonomy.InvocationLane,
		Lane:              lane,
		LaneRunID:         time.Now().UTC().Format(time.RFC3339Nano),
		TaskFamily:        strings.TrimSpace(brief.TaskFamily),
		RuntimeContract:   strings.TrimSpace(brief.Contract),
		PermissionProfile: strings.TrimSpace(brief.PermissionProfile),
	}
	pathRoles := runnerAutonomyPathRoles(brief, results)
	obligations := runnerAutonomyObligations(brief, policy, results)

	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope:        scope,
		Authority:    runnerAutonomyAuthority(brief),
		PathRoles:    pathRoles,
		Capabilities: runnerAutonomyCapabilities(brief),
		Obligations:  obligations,
		Budget: autonomy.BudgetState{
			MaxToolSteps:             policy.MaxToolSteps,
			MaxModelCalls:            policy.MaxModelCalls,
			MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
			MaxContextMessages:       policy.MaxContextMessages,
			MaxContextChars:          policy.MaxContextChars,
		},
		Verification: autonomy.VerificationRequirement{
			Required:               runnerVerificationRequired(brief),
			Contract:               strings.TrimSpace(brief.VerificationContract),
			RequiredAfterWrite:     runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract),
			SourceAwareRequired:    runnerBriefRequiresMutation(brief),
			AcceptedCommands:       append([]string(nil), brief.VerificationPlan...),
			SkippedRequiresBlocker: runnerBriefRequiresMutation(brief),
		},
		Completion: autonomy.CompletionRequirement{
			Required:               true,
			Criteria:               runnerAutonomyCompletionCriteria(brief),
			BlockOnOpenObligations: true,
		},
		Readiness: runnerAutonomyReadiness(brief, obligations),
		Metadata:  autonomy.DefaultContractMetadata("p1.5", "runner_gate_contracts"),
	})
	if err != nil {
		return autonomy.AutonomyContract{}, nil, false
	}

	return contract, obligations, true
}

func runnerAutonomyAuthority(brief workerruntime.WorkerRuntimeBrief) []autonomy.AuthoritySource {
	authority := []autonomy.AuthoritySource{
		{Kind: "runtime_brief", Ref: "runtime_brief", Freshness: autonomy.FreshnessFresh},
	}

	for _, marker := range brief.TargetPacket {
		marker = strings.TrimSpace(marker)
		if marker == "" {
			continue
		}

		authority = append(authority, autonomy.AuthoritySource{
			Kind:      "target_packet",
			Ref:       marker,
			Freshness: autonomy.FreshnessUnknown,
		})
	}

	return authority
}

func runnerAutonomyPathRoles(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []autonomy.PathRole {
	var roles []autonomy.PathRole

	for _, path := range brief.RequiredEditPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleRequiredEdit, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief required_edit_paths"})
		}
	}

	for _, path := range brief.AllowedEditPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleAllowedEdit, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief allowed_edit_paths"})
		}
	}

	for _, path := range brief.EvidencePaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleEvidenceReadonly, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief evidence_paths"})
		}
	}

	for _, path := range mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet()) {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleGeneratedArtifact, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "recorded changed_paths"})
		}
	}

	roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleProtected, PathPattern: ".git/**", Freshness: autonomy.FreshnessFresh, Reason: "repository metadata boundary"})

	return roles
}

func runnerAutonomyCapabilities(brief workerruntime.WorkerRuntimeBrief) autonomy.CapabilityPolicy {
	allowedSet := map[autonomy.Capability]struct{}{}
	forbiddenSet := map[autonomy.Capability]struct{}{}

	for _, tool := range brief.AllowedTools {
		for _, capability := range runnerAutonomyCapabilitiesForTool(brief, tool) {
			allowedSet[capability] = struct{}{}
		}
	}

	for _, tool := range brief.ForbiddenTools {
		for _, capability := range runnerAutonomyCapabilitiesForTool(brief, tool) {
			forbiddenSet[capability] = struct{}{}
		}
	}

	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		forbiddenSet[autonomy.CapabilitySourceEdit] = struct{}{}
		forbiddenSet[autonomy.CapabilityApplyPatch] = struct{}{}
		forbiddenSet[autonomy.CapabilityMutatingShell] = struct{}{}
	}

	return autonomy.CapabilityPolicy{
		Allowed:          mapAutonomyCapabilityKeys(allowedSet),
		Forbidden:        mapAutonomyCapabilityKeys(forbiddenSet),
		AllowedEffects:   runnerAutonomyAllowedEffects(brief),
		ForbiddenEffects: runnerAutonomyForbiddenEffects(brief),
		HiddenTools:      append([]string(nil), brief.ForbiddenTools...),
	}
}

func runnerAutonomyCapabilitiesForTool(brief workerruntime.WorkerRuntimeBrief, tool string) []autonomy.Capability {
	if metadata, ok := runnerAutonomyPlannerToolMetadata(brief, tool); ok {
		return append([]autonomy.Capability(nil), metadata.Capabilities...)
	}

	return runnerAutonomyFallbackCapabilitiesForTool(tool)
}

func runnerAutonomyPlannerToolMetadata(brief workerruntime.WorkerRuntimeBrief, tool string) (workspace.PlanExecutePlannerToolMetadata, bool) {
	if !planThenExecutePlannerBrief(brief) {
		return workspace.PlanExecutePlannerToolMetadata{}, false
	}

	return workspace.LookupPlanExecutePlannerToolMetadata(tool)
}

func runnerAutonomyFallbackCapabilitiesForTool(tool string) []autonomy.Capability {
	if capabilities := workspace.ToolPolicyCapabilities(tool); len(capabilities) > 0 {
		return capabilities
	}

	return nil
}

func runnerAutonomyAllowedEffects(brief workerruntime.WorkerRuntimeBrief) []autonomy.SideEffectClass {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "read_only") {
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
	}

	if runnerBriefRequiresMutation(brief) {
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
	}

	return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
}

func runnerAutonomyForbiddenEffects(brief workerruntime.WorkerRuntimeBrief) []autonomy.SideEffectClass {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "read_only") {
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
	}

	return []autonomy.SideEffectClass{autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
}

func runnerAutonomyObligations(brief workerruntime.WorkerRuntimeBrief, policy RunPolicy, results []workspace.ToolResult) []autonomy.Obligation {
	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	var obligations []autonomy.Obligation

	if runnerBriefRequiresMutation(brief) {
		for _, path := range brief.RequiredEditPaths {
			path = NormalizeRunnerPath(path)
			if path == "" {
				continue
			}

			status := autonomy.ObligationOpen
			if _, ok := changed[path]; ok {
				status = autonomy.ObligationResolved
			}

			obligations = append(obligations, autonomy.Obligation{
				ObligationID: runnerAutonomyObligationID(autonomy.ObligationWrite, path),
				Kind:         autonomy.ObligationWrite,
				Status:       status,
				Owner:        autonomy.OwnerLane,
				TargetRef:    path,
				RequiredBy:   "required_edit_paths",
			})
		}

		if len(brief.RequiredEditPaths) == 0 {
			status := autonomy.ObligationOpen
			if len(changed) > 0 {
				status = autonomy.ObligationResolved
			}

			obligations = append(obligations, autonomy.Obligation{
				ObligationID: "obl-write-workspace-change",
				Kind:         autonomy.ObligationWrite,
				Status:       status,
				Owner:        autonomy.OwnerLane,
				RequiredBy:   "mutation_contract",
			})
		}
	}

	if !runnerBriefRequiresMutation(brief) {
		status := autonomy.ObligationOpen
		if readOnlyAnalysisReadyFromEvidence(results) {
			status = autonomy.ObligationResolved
		}

		obligations = append(obligations, autonomy.Obligation{
			ObligationID: "obl-evidence-read-only",
			Kind:         autonomy.ObligationEvidence,
			Status:       status,
			Owner:        autonomy.OwnerLane,
			RequiredBy:   "read_only_evidence_contract",
		})
	}

	if runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		owner := autonomy.OwnerHost
		if plannerOwnsVerification(brief) {
			owner = autonomy.OwnerPlanner
		}

		status := autonomy.ObligationOpen
		if !hasPendingVerificationAfterWrites(results, brief) && hasSuccessfulVerificationResult(results) {
			status = autonomy.ObligationResolved
		}

		obligations = append(obligations, autonomy.Obligation{
			ObligationID: "obl-verify-post-write",
			Kind:         autonomy.ObligationVerify,
			Status:       status,
			Owner:        owner,
			RequiredBy:   "post_write_verification",
		})
	}

	obligations = append(obligations,
		autonomy.Obligation{
			ObligationID:           "obl-final-answer",
			Kind:                   autonomy.ObligationFinalAnswer,
			Status:                 autonomy.ObligationOpen,
			Owner:                  autonomy.OwnerHost,
			RequiredBy:             "completion_gate",
			FinalAnswerRequirement: textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		},
		autonomy.Obligation{
			ObligationID: "obl-loop-fuse",
			Kind:         autonomy.ObligationLoop,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerHost,
			RequiredBy:   "repeated_call_and_no_progress_fuses",
		},
		autonomy.Obligation{
			ObligationID: "obl-budget",
			Kind:         autonomy.ObligationBudget,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerHost,
			RequiredBy:   "run_policy",
			TargetRef:    runnerAutonomyBudgetTarget(policy),
		},
	)

	return obligations
}

func runnerAutonomyCompletionCriteria(brief workerruntime.WorkerRuntimeBrief) []string {
	criteria := append([]string(nil), brief.RequiredEvidence...)

	criteria = append(criteria, brief.ReadinessGate...)

	if runnerBriefRequiresMutation(brief) {
		criteria = append(criteria, "changed_paths_or_noop_reason", "verification_status_or_blocker")
	}

	if len(criteria) == 0 {
		criteria = []string{"answer_satisfies_runtime_contract"}
	}

	return criteria
}

func runnerAutonomyReadiness(brief workerruntime.WorkerRuntimeBrief, obligations []autonomy.Obligation) autonomy.ReadinessRequirement {
	if brief.Readiness == nil {
		return autonomy.ReadinessRequirement{}
	}

	return autonomy.ReadinessRequirement{
		ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
		ReadyToWrite:        brief.Readiness.ReadyToWrite,
		ReadyToVerify:       brief.Readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
		OpenObligationRefs:  runnerAutonomyReadinessOpenObligationRefs(brief.Readiness.OpenObligations, obligations),
		EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
		FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
	}
}

func runnerAutonomyReadinessOpenObligationRefs(refs []string, obligations []autonomy.Obligation) []string {
	if len(refs) == 0 || len(obligations) == 0 {
		return nil
	}

	obligationsByID := make(map[string]autonomy.Obligation, len(obligations))
	for _, obligation := range obligations {
		obligationID := strings.TrimSpace(obligation.ObligationID)
		if obligationID != "" {
			obligationsByID[obligationID] = obligation
		}
	}

	seen := make(map[string]struct{})

	var out []string

	addID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}

		if obligation, ok := obligationsByID[id]; !ok || obligation.Status != autonomy.ObligationOpen {
			return
		}

		if _, ok := seen[id]; ok {
			return
		}

		seen[id] = struct{}{}
		out = append(out, id)
	}
	addKind := func(kind autonomy.ObligationKind) {
		for _, obligation := range obligations {
			if obligation.Kind == kind {
				addID(obligation.ObligationID)
			}
		}
	}

	for _, ref := range refs {
		trimmed := strings.TrimSpace(ref)
		normalized := strings.ToLower(trimmed)

		if trimmed == "" {
			continue
		}

		addID(trimmed)

		switch {
		case strings.Contains(normalized, "required_edit") || strings.Contains(normalized, "write"):
			addKind(autonomy.ObligationWrite)
		case strings.Contains(normalized, "verify") || strings.Contains(normalized, "verification"):
			addKind(autonomy.ObligationVerify)
		case strings.Contains(normalized, "evidence") || strings.Contains(normalized, "discovery"):
			addKind(autonomy.ObligationEvidence)
		case strings.Contains(normalized, "final") || strings.Contains(normalized, "answer") || strings.Contains(normalized, "completion"):
			addKind(autonomy.ObligationFinalAnswer)
		case strings.Contains(normalized, "budget"):
			addKind(autonomy.ObligationBudget)
		case strings.Contains(normalized, "loop"):
			addKind(autonomy.ObligationLoop)
		}
	}

	return out
}

func (a *runnerAutonomyRecorder) emitContractStarted(brief workerruntime.WorkerRuntimeBrief) {
	if a == nil {
		return
	}

	a.ensureObligationState()

	compiledEvent, compiledErr := autonomy.NewContractCompiledEvent(a.contract, a.nextTime(), a.nextSequence())
	if a.hasPlannerCompile && compiledEvent.Lifecycle != nil {
		compiledEvent.Lifecycle.InvariantID = a.activeAuthorityInvariantID()
		compiledEvent.Lifecycle.ProtocolHash = strings.TrimSpace(a.activeAuthority.ProtocolHash)
		compiledEvent.Lifecycle.StateDelta = append(compiledEvent.Lifecycle.StateDelta,
			"runtime_contract_id:"+strings.TrimSpace(a.activeAuthority.RuntimeContractID),
			"active_invariant:"+a.activeAuthorityInvariantID(),
		)
	}

	a.emitNewEvent(compiledEvent, compiledErr)

	for _, obligation := range a.obligations {
		opened := obligation

		opened.Status = autonomy.ObligationOpen
		opened.SourceEvent = ""
		opened.EvidenceRef = ""
		a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened, a.contract, opened, a.nextTime(), a.nextSequence()))

		switch obligation.Status {
		case autonomy.ObligationOpen:
			a.openObligations[obligation.ObligationID] = opened
		case autonomy.ObligationResolved:
			obligation.SourceEvent = textutil.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
			obligation.EvidenceRef = textutil.FirstNonEmpty(strings.TrimSpace(obligation.EvidenceRef), compiledEvent.EventID)
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, a.contract, obligation, a.nextTime(), a.nextSequence()))

			a.closedObligations[obligation.ObligationID] = struct{}{}
		case autonomy.ObligationWaived:
			obligation.SourceEvent = textutil.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationWaived, a.contract, obligation, a.nextTime(), a.nextSequence()))

			a.closedObligations[obligation.ObligationID] = struct{}{}
		}
	}

	if brief.Readiness != nil {
		a.emitNewEvent(autonomy.NewReadinessStateEvent(a.contract, autonomy.ContractReadinessEvent{
			ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
			ReadyToWrite:        brief.Readiness.ReadyToWrite,
			ReadyToVerify:       brief.Readiness.ReadyToVerify,
			ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
			OpenObligations:     a.openObligationIDsForRefs(a.contract.Readiness.OpenObligationRefs),
			EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
			FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
		}, a.nextTime(), a.nextSequence()))
	}
}

func (a *runnerAutonomyRecorder) emitTurnPhaseContract(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection) {
	if a == nil {
		return
	}

	a.lastTurnPhase = contract

	binding := a.activeAuthorityForProtocol(protocol)

	protocol = protocolWithActiveAuthority(protocol, binding)
	a.activeAuthority = binding
	a.lastModelProtocol = protocol
	a.lastModelPresented = false
	a.lastStoredProtocolHash = strings.TrimSpace(protocol.ProtocolHash)

	payload := contract.autonomyEventPayload()
	protocolPayload := modelProtocolEventPayload(protocol)

	if strings.TrimSpace(binding.ContractID) != "" {
		payload.ContractID = binding.ContractID
	}

	if strings.TrimSpace(protocolPayload.ProtocolHash) != "" {
		payload.ProtocolHash = protocolPayload.ProtocolHash
	}

	if strings.TrimSpace(payload.ProtocolHash) == "" {
		payload.ProtocolHash = runnerTurnPhaseProtocolHash(payload)
	}

	a.lastTurnPhaseEvent = payload
	a.activeTurnContract = a.turnContractProjection(contract, protocol, binding, payload)

	lifecycle := autonomy.ContractLifecycleEvent{
		Reason:               "turn_phase_contract",
		StateDelta:           []string{payload.PhaseID, payload.DerivationReason},
		ProtocolHash:         payload.ProtocolHash,
		AllowedTools:         append([]string(nil), contract.AllowedTools...),
		HiddenTools:          append([]string(nil), contract.HiddenTools...),
		RequiredEvidence:     append([]string(nil), contract.EvidenceState.AcceptanceRefs...),
		ReadinessGate:        contract.Phase,
		VerificationContract: contract.EvidenceState.VerificationStatus,
		InvariantID:          binding.ActiveInvariantID,
		RemainingObligations: a.openObligationIDs(),
		TurnPhaseContract:    &payload,
		SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{
			autonomy.SurfaceSummaryFromTurnPhaseContract(payload),
		},
	}

	if strings.TrimSpace(protocolPayload.ProtocolHash) != "" {
		lifecycle.ModelProtocol = &protocolPayload
	}

	a.emitNewEvent(autonomy.NewContractPresentedEvent(a.contract, lifecycle, a.nextTime(), a.nextSequence()))
	a.emitRollbackControlIfDisabled(payload.ProtocolHash)
	a.emitContextPacket(contract, protocol, payload.ProtocolHash)
}

func (a *runnerAutonomyRecorder) turnContractProjection(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection, binding contractcompile.ActiveAuthorityBinding, payload autonomy.TurnPhaseContractEvent) TurnContract {
	if a == nil {
		return TurnContract{}
	}
	if a.hasPlannerCompile {
		compiled := a.plannerCompile
		if strings.TrimSpace(compiled.Protocol.ProtocolHash) == "" {
			compiled.Protocol.ProtocolHash = protocol.ProtocolHash
		}
		compiled.ActiveAuthority = binding
		if strings.TrimSpace(compiled.Contract.ContractID) == "" {
			compiled.Contract = a.contract
		}
		return FromCompileResult(compiled)
	}

	projection := contract.RuntimeProjection()
	if strings.TrimSpace(projection.ContractID) == "" {
		projection.ContractID = payload.ContractID
	}
	if strings.TrimSpace(projection.ProtocolHash) == "" {
		projection.ProtocolHash = payload.ProtocolHash
	}
	if strings.TrimSpace(projection.PhaseID) == "" {
		projection.PhaseID = payload.PhaseID
	}
	projection.Canonical = a.contract
	projection.Protocol = protocol
	projection.ActiveAuthority = binding
	return projection
}

func (a *runnerAutonomyRecorder) markModelProtocolPresented(protocol contractcompile.ModelProtocolProjection) {
	if a == nil || strings.TrimSpace(protocol.ProtocolHash) == "" {
		return
	}

	binding := a.activeAuthorityForProtocol(protocol)

	protocol = protocolWithActiveAuthority(protocol, binding)

	if !a.hasPlannerCompile {
		a.activeAuthority = binding
	}

	a.lastModelProtocol = protocol
	a.lastModelPresented = true
	a.lastStoredProtocolHash = strings.TrimSpace(protocol.ProtocolHash)
}

func runnerTurnPhaseProtocolHash(payload autonomy.TurnPhaseContractEvent) string {
	payload.ProtocolHash = ""

	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	return "sha256:" + digestRunnerAutonomyValue(string(data))
}

func (a *runnerAutonomyRecorder) emitTurnPhaseSurfaceSummary(surface autonomy.TurnPhaseSurfaceSummaryEvent, phase TurnPhaseContract) {
	if a == nil || strings.TrimSpace(surface.SurfaceID) == "" {
		return
	}

	if strings.TrimSpace(phase.ContractID) == "" {
		phase.ContractID = a.activeAuthorityContractID()
	}

	if strings.TrimSpace(phase.ModelCallID) == "" {
		phase.ModelCallID = a.lastTurnPhaseEvent.ModelCallID
	}

	if len(phase.RuntimeCall.ExpressibleActionClasses) == 0 {
		phase.RuntimeCall = a.lastTurnPhase.RuntimeCall
	}

	phasePayload := phase.autonomyEventPayload()
	if hash := a.currentModelProtocolHash(); hash != "" {
		phasePayload.ProtocolHash = hash
	} else if strings.TrimSpace(phasePayload.ProtocolHash) == "" {
		phasePayload.ProtocolHash = a.lastTurnPhaseEvent.ProtocolHash
	}

	if strings.TrimSpace(surface.ContractID) == "" {
		surface.ContractID = a.activeAuthorityContractID()
	}

	if strings.TrimSpace(surface.ModelCallID) == "" {
		surface.ModelCallID = phasePayload.ModelCallID
	}

	if hash := a.currentModelProtocolHash(); hash != "" {
		surface.ProtocolHash = hash
	} else if strings.TrimSpace(surface.ProtocolHash) == "" {
		surface.ProtocolHash = phasePayload.ProtocolHash
	}

	if strings.TrimSpace(surface.PhaseID) == "" {
		surface.PhaseID = phasePayload.PhaseID
	}

	lifecycle := autonomy.ContractLifecycleEvent{
		Reason:            "turn_phase_surface_summary",
		StateDelta:        []string{surface.SurfaceID, surface.PhaseID},
		ProtocolHash:      surface.ProtocolHash,
		InvariantID:       a.activeAuthorityInvariantID(),
		TurnPhaseContract: &phasePayload,
		SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{
			surface,
		},
	}
	a.emitNewEvent(autonomy.NewContractPresentedEvent(a.contract, lifecycle, a.nextTime(), a.nextSequence()))
}

func (a *runnerAutonomyRecorder) emitGateDecision(input runnerAutonomyGateDecision) {
	if a == nil {
		return
	}

	invariantID := strings.TrimSpace(input.InvariantID)
	if invariantID == "" && a.hasPlannerCompile {
		invariantID = a.activeAuthorityInvariantID()
	}

	gate := autonomy.ContractGateEvent{
		Gate:                  input.Gate,
		Decision:              input.Decision,
		InvariantID:           invariantID,
		ProtocolHash:          a.currentModelProtocolHash(),
		ReasonCode:            input.ReasonCode,
		ToolCallID:            input.ToolCallID,
		ToolName:              input.ToolName,
		TargetRefs:            input.TargetRefs,
		ChangedPaths:          input.ChangedPaths,
		VerificationStatus:    input.VerificationStatus,
		Budget:                input.Budget,
		LoopFingerprintDigest: input.LoopFingerprintDigest,
		Attempts:              input.Attempts,
		Source:                input.Source,
	}

	gate.ObligationRefs = a.obligationIDsForGate(input.Gate)

	if runnerAutonomyGateClosesObligations(input.Gate, input.Decision) {
		gate.ResolvedObligations, gate.OpenObligations = a.resolvedAndOpenObligationsForGate(input, gate.ObligationRefs)
	} else {
		gate.OpenObligations = a.openObligationIDsForRefs(gate.ObligationRefs)
	}

	gateFields := runnerAutonomyGateFields(input, gate, a)

	gate.PackID = gateFields.PackID
	gate.EnforcementMode = gateFields.EnforcementMode
	gate.ObservedFacts = gateFields.ObservedFacts
	gate.SafeAlternatives = gateFields.SafeAlternatives

	gateEvent, err := autonomy.NewGateDecisionEvent(a.contract, gate, a.nextTime(), a.nextSequence())
	a.emitNewEvent(gateEvent, err)

	if err == nil {
		a.emitResolvedObligations(gate.ResolvedObligations, gateEvent.EventID)
		a.emitGateFriction(input, gate, gateEvent.EventID)
		a.emitFailureDeltaIntentFromGate(input, gate, gateEvent.EventID)
		a.emitModelResponseObservationFromGate(input, gate, gateEvent.EventID)
	}

	if input.Gate == autonomy.GateCompletion {
		completionProtocolHash := textutil.FirstNonEmpty(gate.ProtocolHash, a.activeProtocolHash())
		a.emitNewEvent(autonomy.NewCompletionGateDecisionEvent(a.contract, autonomy.ContractCompletionEvent{
			Decision:             input.Decision,
			InvariantID:          input.InvariantID,
			ProtocolHash:         completionProtocolHash,
			RemainingObligations: gate.OpenObligations,
			VerificationStatus:   input.VerificationStatus,
			ChangedPaths:         input.ChangedPaths,
			RequiredPathStatus:   input.TargetRefs,
		}, a.nextTime(), a.nextSequence()))
	}
}

func runnerAutonomyGateFields(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, recorder *runnerAutonomyRecorder) runnerAutonomyGateEventFields {
	return runnerAutonomyGateEventFields{
		PackID:           runnerAutonomyGatePackID(input),
		EnforcementMode:  runnerAutonomyGateEnforcementMode(input.Decision),
		ObservedFacts:    runnerAutonomyGateObservedFacts(input, gate),
		SafeAlternatives: runnerAutonomyGateSafeAlternatives(input, recorder),
	}
}

func runnerAutonomyGatePackID(input runnerAutonomyGateDecision) autonomy.ContractPackID {
	switch strings.TrimSpace(input.InvariantID) {
	case "completion.readiness":
		return autonomy.ContractPackCompletionReadiness
	case "write.required_edit_paths":
		return autonomy.ContractPackWriteRequiredPaths
	case "verification.post_write":
		return autonomy.ContractPackVerificationPostWrite
	case "loop.repeated_call":
		return autonomy.ContractPackLoopRepeatedCall
	case "budget.run_policy":
		return autonomy.ContractPackBudgetRunPolicy
	case "read_only.readiness":
		return autonomy.ContractPackCompletionReadiness
	}

	switch input.Gate {
	case autonomy.GateCompletion:
		return autonomy.ContractPackCompletionReadiness
	case autonomy.GateWrite:
		return autonomy.ContractPackWriteRequiredPaths
	case autonomy.GateVerification:
		return autonomy.ContractPackVerificationPostWrite
	case autonomy.GateLoop:
		return autonomy.ContractPackLoopRepeatedCall
	case autonomy.GateBudget:
		return autonomy.ContractPackBudgetRunPolicy
	case autonomy.GateReadOnly:
		return autonomy.ContractPackCompletionReadiness
	default:
		return ""
	}
}

func runnerAutonomyGateEnforcementMode(decision autonomy.ToolPolicyDecision) autonomy.EnforcementMode {
	if decision == autonomy.PolicyObserve {
		return autonomy.EnforcementModeObserveOnly
	}

	return autonomy.EnforcementModeEnforce
}

func runnerAutonomyGateObservedFacts(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent) []autonomy.ContractObservedFact {
	var facts []autonomy.ContractObservedFact

	addGateFactValue(&facts, autonomy.ObservedFactStatus, "gate", string(input.Gate))
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "decision", string(input.Decision))
	addGateFactCount(&facts, "target_ref_count", len(gate.TargetRefs))
	addGateFactCount(&facts, "obligation_ref_count", len(gate.ObligationRefs))
	addGateFactCount(&facts, "open_obligation_count", len(gate.OpenObligations))
	addGateFactCount(&facts, "resolved_obligation_count", len(gate.ResolvedObligations))
	addGateFactCount(&facts, "changed_path_count", len(gate.ChangedPaths))
	addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "reason_code", runnerAutonomyReasonCode(input.ReasonCode))
	addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "source", runnerAutonomyReasonCode(input.Source))
	addGateFactValue(&facts, autonomy.ObservedFactToolName, "tool", input.ToolName)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "tool_call_id", input.ToolCallID)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "verification_status", input.VerificationStatus)
	addGateFactValue(&facts, autonomy.ObservedFactProtocolHash, "protocol_hash", gate.ProtocolHash)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "loop_fingerprint_digest", gate.LoopFingerprintDigest)
	addGateFactValues(&facts, autonomy.ObservedFactPathRef, "target_refs", gate.TargetRefs, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "obligation_refs", gate.ObligationRefs, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "open_obligations", gate.OpenObligations, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "resolved_obligations", gate.ResolvedObligations, 8)

	if input.Attempts > 0 {
		addGateFactCount(&facts, "attempts", input.Attempts)
	}

	if gate.Budget != nil {
		addGateFactValue(&facts, autonomy.ObservedFactStatus, "budget_exhausted", runnerAutonomyBoolStatus(gate.Budget.Exhausted))
		addGateFactCount(&facts, "budget_used_tool_steps", gate.Budget.UsedToolSteps)
		addGateFactCount(&facts, "budget_max_tool_steps", gate.Budget.MaxToolSteps)
		addGateFactCount(&facts, "budget_used_model_calls", gate.Budget.UsedModelCalls)
		addGateFactCount(&facts, "budget_max_model_calls", gate.Budget.MaxModelCalls)
		addGateFactCount(&facts, "budget_max_operational_model_calls", gate.Budget.MaxOperationalModelCalls)
		if gate.Budget.Exhausted {
			addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "budget_exhaustion_reason", runnerAutonomyReasonCode(gate.Budget.ExhaustionReason))
		}
	}

	return facts
}

func addGateFactValue(facts *[]autonomy.ContractObservedFact, kind autonomy.ObservedFactKind, name string, value string) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)

	if name == "" || value == "" {
		return
	}

	if len(value) > 160 {
		value = value[:160]
	}

	*facts = append(*facts, autonomy.ContractObservedFact(string(kind)+":"+name+"="+value))
}

func addGateFactCount(facts *[]autonomy.ContractObservedFact, name string, count int) {
	name = strings.TrimSpace(name)

	if count < 0 {
		return
	}

	*facts = append(*facts, autonomy.ContractObservedFact(string(autonomy.ObservedFactCount)+":"+name+"="+strconv.Itoa(count)))
}

func addGateFactValues(facts *[]autonomy.ContractObservedFact, kind autonomy.ObservedFactKind, name string, values []string, limit int) {
	values = runnerAutonomyBoundedFactStrings(values, limit)
	for _, value := range values {
		addGateFactValue(facts, kind, name, value)
	}
}

func runnerAutonomyBoolStatus(value bool) string {
	if value {
		return "true"
	}

	return "false"
}

func runnerAutonomyBoundedFactStrings(values []string, limit int) []string {
	if limit <= 0 {
		limit = 8
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, min(len(values), limit))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if len(value) > 160 {
			value = value[:160]
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)

		if len(out) >= limit {
			break
		}
	}

	return out
}

func runnerAutonomyGateSafeAlternatives(input runnerAutonomyGateDecision, recorder *runnerAutonomyRecorder) []string {
	if !runnerAutonomyGateNeedsSafeAlternatives(input.Decision) {
		return nil
	}

	var alternatives []string

	if recorder != nil {
		protocolVisible := recorder.currentModelProtocolHash() != ""

		alternatives = runnerAutonomyFrictionSafeAlternatives(recorder.visibleModelProtocol(), recorder.lastTurnPhase, protocolVisible)
	}

	if len(alternatives) == 0 {
		alternatives = runnerAutonomyDefaultGateSafeAlternatives(input.Gate)
	}

	return runnerAutonomyBoundedFactStrings(alternatives, 8)
}

func runnerAutonomyGateNeedsSafeAlternatives(decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyAsk, autonomy.PolicyDeny, autonomy.PolicyNudge, autonomy.PolicyReject, autonomy.PolicyStop:
		return true
	default:
		return false
	}
}

func runnerAutonomyDefaultGateSafeAlternatives(gate autonomy.GateKind) []string {
	switch gate {
	case autonomy.GateCompletion:
		return []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	case autonomy.GateWrite:
		return []string{"edit required_edit_paths", "<<QUESTION>>:"}
	case autonomy.GateVerification:
		return []string{"run verification", "report verification blocker"}
	case autonomy.GateLoop:
		return []string{"change tool target", "Decision: source-satisfied"}
	case autonomy.GateBudget:
		return []string{"Decision: blocker", "Decision: source-satisfied"}
	case autonomy.GateReadOnly:
		return []string{"read evidence paths", "Decision: source-satisfied"}
	default:
		return []string{"choose allowed action", "<<QUESTION>>:"}
	}
}

func (a *runnerAutonomyRecorder) resolvedAndOpenObligationsForGate(input runnerAutonomyGateDecision, obligationRefs []string) (resolved []string, open []string) {
	if a == nil {
		return nil, nil
	}

	switch input.Gate {
	case autonomy.GateWrite:
		return a.writeGateResolvedAndOpenObligations(input.ChangedPaths)
	case autonomy.GateCompletion:
		return a.completionGateResolvedAndOpenObligations(input, obligationRefs)
	case autonomy.GateVerification:
		if textutil.EqualTrimFold(input.VerificationStatus, "passed") ||
			textutil.EqualTrimFold(input.VerificationStatus, "observed") {

			return a.openObligationIDsForRefs(obligationRefs), nil
		}

		return nil, a.openObligationIDsForRefs(obligationRefs)
	default:
		return a.openObligationIDsForRefs(obligationRefs), nil
	}
}

func (a *runnerAutonomyRecorder) completionGateResolvedAndOpenObligations(input runnerAutonomyGateDecision, refs []string) (resolved []string, open []string) {
	a.ensureObligationState()

	changed := make(map[string]struct{}, len(input.ChangedPaths))
	for _, path := range input.ChangedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			changed[normalized] = struct{}{}
		}
	}

	sourceSatisfiedPaths := map[string]struct{}{}
	sourceSatisfiedReason := strings.TrimSpace(input.ReasonCode) == "plan_source_satisfied_short_circuit" ||
		strings.Contains(strings.ToLower(strings.TrimSpace(input.ReasonCode)), "source_satisfied")

	if sourceSatisfiedReason {
		for _, path := range input.TargetRefs {
			path = strings.TrimSpace(path)
			if idx := strings.LastIndex(path, ":"); idx > 0 {
				path = path[:idx]
			}

			if normalized := NormalizeRunnerPath(path); normalized != "" {
				sourceSatisfiedPaths[normalized] = struct{}{}
			}
		}
	}

	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}

		obligation, ok := a.openObligations[ref]
		if !ok {
			continue
		}

		if obligation.Kind == autonomy.ObligationWrite {
			target := NormalizeRunnerPath(obligation.TargetRef)
			if target == "" {
				if sourceSatisfiedReason {
					resolved = append(resolved, ref)
					continue
				}

				open = append(open, ref)

				continue
			}

			if _, ok := changed[target]; ok {
				resolved = append(resolved, ref)
				continue
			}

			if _, ok := sourceSatisfiedPaths[target]; ok {
				resolved = append(resolved, ref)
				continue
			}

			if len(changed) == 0 {
				open = append(open, ref)
				continue
			}

			open = append(open, ref)

			continue
		}

		if ref == "obl-verify-post-write" && !runnerAutonomyVerificationStatusResolvesPostWriteObligation(input.VerificationStatus) {
			open = append(open, ref)
			continue
		}

		resolved = append(resolved, ref)
	}

	return resolved, open
}

func (a *runnerAutonomyRecorder) openObligationIDsForRefs(refs []string) []string {
	if a == nil || len(refs) == 0 {
		return nil
	}

	a.ensureObligationState()

	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))

	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}

		if _, ok := a.openObligations[ref]; !ok {
			continue
		}

		if _, ok := seen[ref]; ok {
			continue
		}

		seen[ref] = struct{}{}
		out = append(out, ref)
	}

	return out
}

func (a *runnerAutonomyRecorder) openObligationIDs() []string {
	if a == nil {
		return nil
	}

	a.ensureObligationState()

	ids := make([]string, 0, len(a.openObligations))
	for id := range a.openObligations {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}

	slices.Sort(ids)

	return ids
}

func (a *runnerAutonomyRecorder) writeGateResolvedAndOpenObligations(changedPaths []string) (resolved []string, open []string) {
	if a == nil {
		return nil, nil
	}

	a.ensureObligationState()

	obligations := a.obligationsForGate(autonomy.GateWrite)
	hasRequiredPathTargets := false

	for _, obligation := range obligations {
		if strings.TrimSpace(obligation.TargetRef) != "" {
			hasRequiredPathTargets = true
			break
		}
	}

	if !hasRequiredPathTargets {
		for _, obligation := range obligations {
			if _, ok := a.openObligations[obligation.ObligationID]; ok {
				resolved = append(resolved, obligation.ObligationID)
			}
		}

		return resolved, nil
	}

	changed := a.runnerAutonomyChangedPathSet(changedPaths)
	for _, obligation := range obligations {
		if _, ok := a.openObligations[obligation.ObligationID]; !ok {
			continue
		}

		if a.runnerAutonomyPathMatchesChangedSet(obligation.TargetRef, changed) {
			resolved = append(resolved, obligation.ObligationID)
			continue
		}

		open = append(open, obligation.ObligationID)
	}

	return resolved, open
}

func (a *runnerAutonomyRecorder) runnerAutonomyChangedPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{})
	root := a.runnerAutonomyWorkspaceRoot()

	for _, path := range paths {
		for _, variant := range runnerAutonomyPathVariants(path, root) {
			out[variant] = struct{}{}
		}
	}

	return out
}

func (a *runnerAutonomyRecorder) runnerAutonomyPathMatchesChangedSet(path string, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}

	for _, variant := range runnerAutonomyPathVariants(path, a.runnerAutonomyWorkspaceRoot()) {
		if _, ok := changed[variant]; ok {
			return true
		}
	}

	return false
}

func (a *runnerAutonomyRecorder) runnerAutonomyWorkspaceRoot() string {
	if a == nil || a.runner == nil || a.runner.Workspace == nil {
		return ""
	}

	return strings.TrimSpace(a.runner.Workspace.Root())
}

func runnerAutonomyPathVariants(path string, root string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	seen := make(map[string]struct{})

	var variants []string

	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		value = filepath.ToSlash(filepath.Clean(value))
		value = NormalizeRunnerPath(value)

		if value == "" || value == "." {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		variants = append(variants, value)
	}
	add(path)

	canonicalPath := ""

	if filepath.IsAbs(path) {
		if canonical, ok := runnerAutonomyCanonicalPath(path); ok {
			canonicalPath = canonical
			add(canonicalPath)
		}
	}

	root = strings.TrimSpace(root)
	if root == "" {
		return variants
	}

	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			rel = filepath.ToSlash(rel)
			if rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
				add(rel)
			}
		}

		if canonicalPath != "" {
			if rel, err := filepath.Rel(root, canonicalPath); err == nil {
				rel = filepath.ToSlash(rel)
				if rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
					add(rel)
				}
			}
		}

		return variants
	}

	add(filepath.Join(root, path))

	return variants
}

func runnerAutonomyCanonicalPath(path string) (string, bool) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, true
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)

	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base), true
	}

	return "", false
}

func runnerAutonomyGateClosesObligations(gate autonomy.GateKind, decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyAllow, autonomy.PolicyObserve:
		return true
	case autonomy.PolicyStop:
		return gate == autonomy.GateBudget || gate == autonomy.GateLoop
	default:
		return false
	}
}

func (a *runnerAutonomyRecorder) emitResolvedObligations(ids []string, sourceEvent string) {
	if a == nil {
		return
	}

	a.ensureObligationState()

	for _, id := range ids {
		obligation, ok := a.openObligations[strings.TrimSpace(id)]
		if !ok {
			continue
		}

		obligation.Status = autonomy.ObligationResolved
		obligation.SourceEvent = sourceEvent
		obligation.EvidenceRef = sourceEvent
		a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, a.contract, obligation, a.nextTime(), a.nextSequence()))
		delete(a.openObligations, obligation.ObligationID)

		a.closedObligations[obligation.ObligationID] = struct{}{}
	}
}

func (a *runnerAutonomyRecorder) ensureObligationState() {
	if a.openObligations == nil {
		a.openObligations = make(map[string]autonomy.Obligation)
	}

	if a.closedObligations == nil {
		a.closedObligations = make(map[string]struct{})
	}
}

func (a *runnerAutonomyRecorder) emitWriteGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, changedPaths []string) {
	if a == nil {
		return
	}

	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:         autonomy.GateWrite,
		Decision:     decision,
		InvariantID:  "write.required_edit_paths",
		ReasonCode:   reasonCode,
		ToolName:     strings.TrimSpace(req.Tool),
		TargetRefs:   writeRequestPaths(req),
		ChangedPaths: changedPaths,
		Source:       "runner_write_gate",
	})
}

func (a *runnerAutonomyRecorder) emitIntentClassified(req workspace.ToolRequest, inputs ...runnerAutonomyPolicyEventInput) {
	input := runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventToolIntentClassified,
		Source:    "runner_tool_intent",
	}
	if len(inputs) > 0 {
		input = inputs[0]
		if input.EventType == "" {
			input.EventType = autonomy.EventToolIntentClassified
		}
		if strings.TrimSpace(input.Source) == "" {
			input.Source = "runner_tool_intent"
		}
	}
	a.emitPolicyEvent(req, input)
}

func (a *runnerAutonomyRecorder) emitPathRolesResolved(req workspace.ToolRequest, inputs ...runnerAutonomyPolicyEventInput) {
	if a == nil || len(runnerAutonomyPolicyPaths(req)) == 0 {
		return
	}

	input := runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventPathRolesResolved,
		Source:    "runner_path_roles",
	}
	if len(inputs) > 0 {
		input = inputs[0]
		if input.EventType == "" {
			input.EventType = autonomy.EventPathRolesResolved
		}
		if strings.TrimSpace(input.Source) == "" {
			input.Source = "runner_path_roles"
		}
	}
	a.emitPolicyEvent(req, input)
}

func (a *runnerAutonomyRecorder) emitPolicyResult(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if input.EventType == "" {
		input.EventType = autonomy.EventToolPolicyResult
	}

	a.emitPolicyEvent(req, input)

	// Also emit the legacy permission_decision RunEvent so consumers that have not
	// yet migrated to the autonomy event stream continue to receive the signal.
	// The autonomy recorder is now the single source of truth for both events.
	a.emitLegacyPermissionDecision(req, input)
}

// emitLegacyPermissionDecision emits the legacy permission_decision RunEvent,
// deriving its fields from the autonomy policy event data that was already computed.
func (a *runnerAutonomyRecorder) emitLegacyPermissionDecision(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if a == nil || a.runner == nil || a.runner.OnEvent == nil {
		return
	}

	decision := permissionDecisionFromAutonomy(input.Decision)
	snapshot := PermissionDecisionSnapshot{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   PermissionTarget(req),
		Decision: decision,
		Rule:     strings.TrimSpace(input.Source),
	}
	a.runner.emitEvent(marshalRunEvent("permission_decision", snapshot))
}

// permissionDecisionFromAutonomy maps an autonomy ToolPolicyDecision to the
// legacy decision value used by the permission_decision RunEvent.
func permissionDecisionFromAutonomy(d autonomy.ToolPolicyDecision) toolpolicy.Decision {
	switch d {
	case autonomy.PolicyAllow, autonomy.PolicyObserve, autonomy.PolicyNudge:
		return toolpolicy.Allow
	case autonomy.PolicyAsk:
		return toolpolicy.Ask
	case autonomy.PolicyDeny, autonomy.PolicyReject:
		return toolpolicy.Deny
	default:
		return toolpolicy.Deny
	}
}

func (a *runnerAutonomyRecorder) emitVerificationGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, status string) {
	if a == nil {
		return
	}

	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:               autonomy.GateVerification,
		Decision:           decision,
		InvariantID:        "verification.post_write",
		ReasonCode:         reasonCode,
		ToolName:           strings.TrimSpace(req.Tool),
		VerificationStatus: status,
		Source:             "runner_verification_gate",
	})
}

func (a *runnerAutonomyRecorder) emitCompletionGate(decision autonomy.ToolPolicyDecision, reasonCode string, results []workspace.ToolResult) {
	a.emitCompletionGateAttempt(decision, reasonCode, results, 0)
}

func (a *runnerAutonomyRecorder) emitCompletionGateAttempt(decision autonomy.ToolPolicyDecision, reasonCode string, results []workspace.ToolResult, attempts int) {
	if a == nil {
		return
	}

	changed := mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:               autonomy.GateCompletion,
		Decision:           decision,
		InvariantID:        "completion.readiness",
		ReasonCode:         reasonCode,
		ChangedPaths:       changed,
		VerificationStatus: runnerAutonomyVerificationStatus(results, a.brief),
		TargetRefs:         runnerAutonomyRequiredPathStatus(a.contract, results),
		Attempts:           attempts,
		Source:             "runner_completion_gate",
	})
}

func (a *runnerAutonomyRecorder) emitGateFriction(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil {
		return
	}

	decision, ok := runnerAutonomyGateFrictionDecision(input)
	if !ok {
		return
	}

	phase := a.lastTurnPhase
	protocol := a.visibleModelProtocol()
	protocolHash := a.currentModelProtocolHash()
	modelContractVisible := protocolHash != ""
	effectiveHash := protocolHash
	if string(input.Gate) == "completion_gate" && a.lastStoredProtocolHash != "" {
		if !modelContractVisible {
			effectiveHash = a.lastStoredProtocolHash
			modelContractVisible = true
		}
	}
	friction := autonomy.ContractGuardFrictionEvent{
		Step:                 phase.Step,
		Lane:                 a.contract.Scope.Lane,
		Phase:                phase.Phase,
		PhaseID:              phase.PhaseID,
		ModelCallID:          phase.ModelCallID,
		SourceEventID:        sourceEventID,
		ContractVersion:      a.contract.SchemaVersion,
		ProtocolHash:         effectiveHash,
		InvariantID:          input.InvariantID,
		ObligationID:         firstRunnerAutonomyString(gate.OpenObligations),
		Subject:              runnerAutonomyFrictionSubject(input),
		Decision:             decision,
		GuardSurface:         runnerAutonomyFrictionSurface(input.Gate),
		FirstInvalidAction:   runnerAutonomyFrictionInvalidAction(input),
		VisibleActionClasses: runnerAutonomyFrictionVisibleActionClasses(protocol, phase, protocolHash != ""),
		VisibleTools:         runnerAutonomyFrictionVisibleTools(protocol, phase, protocolHash != ""),
		SafeAlternatives:     runnerAutonomyFrictionSafeAlternatives(protocol, phase, protocolHash != ""),
		ModelContractVisible: modelContractVisible,
		RetryNumber:          input.Attempts,
		RetryOutcome:         runnerAutonomyFrictionRetryOutcome(input),
		Avoidability:         autonomy.GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         runnerAutonomyFrictionEvidenceRefs(input),
	}
	a.emitNewEvent(autonomy.NewGuardFrictionEvent(a.contract, friction, a.nextTime(), a.nextSequence()))
}

func (a *runnerAutonomyRecorder) currentModelProtocolHash() string {
	if a == nil || !a.lastModelPresented {
		return ""
	}

	return strings.TrimSpace(a.lastModelProtocol.ProtocolHash)
}

func (a *runnerAutonomyRecorder) visibleModelProtocol() contractcompile.ModelProtocolProjection {
	if a == nil || !a.lastModelPresented {
		return contractcompile.ModelProtocolProjection{}
	}

	return a.lastModelProtocol
}

func runnerAutonomyFrictionDecision(decision autonomy.ToolPolicyDecision) (autonomy.GuardFrictionDecision, bool) {
	switch decision {
	case autonomy.PolicyReject, autonomy.PolicyDeny:
		return autonomy.GuardFrictionDecisionReject, true
	case autonomy.PolicyNudge, autonomy.PolicyAsk:
		return autonomy.GuardFrictionDecisionNudge, true
	case autonomy.PolicyStop:
		return autonomy.GuardFrictionDecisionStop, true
	default:
		return "", false
	}
}

func runnerAutonomyGateFrictionDecision(input runnerAutonomyGateDecision) (autonomy.GuardFrictionDecision, bool) {
	if input.Gate == autonomy.GateCompletion && input.Decision == autonomy.PolicyAllow && input.Attempts > 0 {
		return autonomy.GuardFrictionDecisionRecover, true
	}

	return runnerAutonomyFrictionDecision(input.Decision)
}

func runnerAutonomyFrictionSurface(gate autonomy.GateKind) autonomy.GuardFrictionSurface {
	switch gate {
	case autonomy.GateCompletion:
		return autonomy.GuardFrictionSurfaceCompletionGate
	case autonomy.GateWrite:
		return autonomy.GuardFrictionSurfaceWriteGate
	case autonomy.GateVerification:
		return autonomy.GuardFrictionSurfaceVerification
	case autonomy.GateLoop:
		return autonomy.GuardFrictionSurfaceRunnerLoop
	case autonomy.GateBudget:
		return autonomy.GuardFrictionSurfaceBudget
	case autonomy.GateReadOnly:
		return autonomy.GuardFrictionSurfaceReadiness
	default:
		return autonomy.GuardFrictionSurfaceToolPolicy
	}
}

func runnerAutonomyFrictionSubject(input runnerAutonomyGateDecision) autonomy.GuardFrictionSubject {
	if input.Gate == autonomy.GateCompletion {
		return autonomy.GuardFrictionSubjectFinalAnswer
	}

	return autonomy.GuardFrictionSubjectToolCall
}

func runnerAutonomyFrictionInvalidAction(input runnerAutonomyGateDecision) string {
	if tool := strings.TrimSpace(input.ToolName); tool != "" {
		return tool
	}

	if input.Gate == autonomy.GateCompletion {
		return actionFinalAnswer
	}

	return strings.TrimSpace(string(input.Gate))
}

func runnerAutonomyFrictionRetryOutcome(input runnerAutonomyGateDecision) autonomy.GuardFrictionRetryOutcome {
	if input.Gate == autonomy.GateCompletion && input.Decision == autonomy.PolicyAllow && input.Attempts > 0 {
		return autonomy.GuardFrictionRetrySuccess
	}

	if input.Attempts > 0 && (input.Decision == autonomy.PolicyReject || input.Decision == autonomy.PolicyStop) {
		return autonomy.GuardFrictionRetrySameFailure
	}

	return autonomy.GuardFrictionRetryNotApplicable
}

func runnerAutonomyFrictionEvidenceRefs(input runnerAutonomyGateDecision) []string {
	refs := []string{"reason:" + runnerAutonomyReasonCode(input.ReasonCode)}
	if input.Source != "" {
		refs = append(refs, "source:"+runnerAutonomyReasonCode(input.Source))
	}

	return refs
}

func runnerAutonomyFrictionVisibleActionClasses(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		return append([]string(nil), protocol.CallableActionClasses...)
	}

	return append([]string(nil), phase.ActionPolicy.AllowedActionClasses...)
}

func runnerAutonomyFrictionVisibleTools(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		return append([]string(nil), protocol.VisibleTools...)
	}

	return append([]string(nil), phase.AllowedTools...)
}

func runnerAutonomyFrictionSafeAlternatives(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		if len(protocol.SafeAlternatives) > 0 {
			return append([]string(nil), protocol.SafeAlternatives...)
		}
		return recoverySafeActionAlternatives(protocol.CallableActionClasses)
	}

	if alternatives := recoverySafeActionAlternatives(recoveryCardCallableActionClasses(phase, phase.ActionPolicy.CallableToolNames)); len(alternatives) > 0 {
		return alternatives
	}

	return append([]string(nil), protocol.SafeAlternatives...)
}

func firstRunnerAutonomyString(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func (a *runnerAutonomyRecorder) emitLoopGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, fingerprint string, attempts int) {
	if a == nil {
		return
	}

	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:                  autonomy.GateLoop,
		Decision:              decision,
		InvariantID:           "loop.repeated_call",
		ReasonCode:            reasonCode,
		ToolName:              strings.TrimSpace(req.Tool),
		LoopFingerprintDigest: digestRunnerAutonomyValue(fingerprint),
		Attempts:              attempts,
		Source:                "runner_loop_gate",
	})
}

func (a *runnerAutonomyRecorder) emitLoopCloseout(input any) {
	if a == nil {
		return
	}

	if decision, ok := input.(LifecycleCloseoutPolicyDecision); ok {
		if decision.Handled {
			a.emitGateDecision(decision.Gate)
		}
		return
	}

	stopReason, _ := input.(string)
	if decision := lifecycleLoopCloseoutDecision(stopReason, a.hasOpenObligationsForGate(autonomy.GateLoop)); decision.Handled {
		a.emitGateDecision(decision.Gate)
	}
}

func (a *runnerAutonomyRecorder) emitBudgetGate(args ...any) {
	if a == nil {
		return
	}

	if len(args) == 1 {
		if decision, ok := args[0].(LifecycleCloseoutPolicyDecision); ok {
			if decision.Handled {
				a.emitGateDecision(decision.Gate)
			}
			return
		}
	}
	if len(args) != 4 {
		return
	}
	policy, _ := args[0].(RunPolicy)
	metrics, _ := args[1].(runnerMetrics)
	stopReason, _ := args[2].(string)
	exhausted, _ := args[3].(bool)
	if decision := lifecycleBudgetCloseoutDecision(policy, metrics, stopReason, exhausted); decision.Handled {
		a.emitGateDecision(decision.Gate)
	}
}

func (a *runnerAutonomyRecorder) hasOpenObligationsForGate(gate autonomy.GateKind) bool {
	if a == nil {
		return false
	}

	for _, obligation := range a.obligationsForGate(gate) {
		if _, ok := a.openObligations[obligation.ObligationID]; ok {
			return true
		}
	}

	return false
}

func (a *runnerAutonomyRecorder) emitContextPacket(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection, protocolHash string) {
	if a == nil {
		return
	}

	protocolHash = textutil.FirstNonEmpty(strings.TrimSpace(protocol.ProtocolHash), strings.TrimSpace(protocolHash))

	packet := protocol.CompactPacket

	if packet.SchemaVersion == "" {
		packet.SchemaVersion = autonomy.ContextPacketSchemaVersion
	}

	packet.ContractID = a.contract.ContractID
	packet.ProtocolHash = protocolHash

	if packet.PacketID == "" {
		packet.PacketID = "packet:" + digestRunnerAutonomyValue(strings.Join([]string{
			packet.ContractID,
			packet.ProtocolHash,
			strings.TrimSpace(contract.PhaseID),
			strings.TrimSpace(contract.ModelCallID),
		}, ":"))
	}

	if len(packet.SchemaNames) == 0 {
		packet.SchemaNames = compileModelExchangeSchemaNames()
	}

	if len(packet.RedactionMarkers) == 0 {
		packet.RedactionMarkers = []string{"metadata_only", "prompt_omitted", "transcript_omitted", "file_content_omitted", "secrets_omitted"}
	}

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:          "context_packet",
		ContractID:    a.contract.ContractID,
		ProtocolHash:  protocolHash,
		PhaseID:       textutil.FirstNonEmpty(strings.TrimSpace(contract.PhaseID), strings.TrimSpace(protocol.PhaseID)),
		ModelCallID:   strings.TrimSpace(contract.ModelCallID),
		InvariantID:   firstRunnerAutonomyString(contract.InvariantIDs),
		ContextPacket: &packet,
	})
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:         "trace_store_policy",
		ContractID:   a.contract.ContractID,
		ProtocolHash: protocolHash,
		PhaseID:      textutil.FirstNonEmpty(strings.TrimSpace(contract.PhaseID), strings.TrimSpace(protocol.PhaseID)),
		ModelCallID:  strings.TrimSpace(contract.ModelCallID),
		TraceStorePolicy: &autonomy.TraceStorePolicy{
			ContractID:      a.contract.ContractID,
			ProtocolHash:    protocolHash,
			RetentionRef:    "retention:local_default_30d_raw_180d_redacted",
			RedactionRef:    "redaction:metadata_only",
			RedactionStatus: "pass",
			AllowedFields:   []string{"schema_version", "event_id", "type", "contract_id", "protocol_hash", "phase_id", "model_call_id", "invariant_id", "source_event_id", "source_event_type", "refs", "counts", "redaction_markers"},
			ForbiddenFields: []string{"raw_prompt", "raw_transcript", "prompt_body", "transcript_body", "file_body", "old_text", "new_text", "long_excerpt", "customer_id", "customer_identifier"},
			StoreRefs:       []string{"role:autonomy_event", "event_type:model_exchange"},
			CapturedBy:      "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitPermissionSnapshot(req workspace.ToolRequest, policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil {
		return
	}

	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}

	_ = req

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "permission_snapshot",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		PermissionSnapshot: &autonomy.PermissionSnapshot{
			ContractID:        a.contract.ContractID,
			ProtocolHash:      protocolHash,
			PermissionProfile: a.contract.Scope.PermissionProfile,
			AllowedTools:      append([]string(nil), a.brief.AllowedTools...),
			HiddenTools:       append([]string(nil), a.contract.Capabilities.HiddenTools...),
			CapabilityRefs:    runnerAutonomyPermissionCapabilityRefs(policy),
			PathRoleRefs:      runnerAutonomyPermissionPathRoleRefs(policy),
			CapturedBy:        "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitFailureDeltaIntentFromPolicy(policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || !runnerAutonomyPolicyDecisionIsFailure(policy.Decision) {
		return
	}

	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "failure_delta",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		FailureDelta: &autonomy.FailureDelta{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			FailureClass: textutil.FirstNonEmpty(runnerAutonomyReasonCode(policy.ErrorClass), runnerAutonomyReasonCode(policy.ReasonCode), strings.TrimSpace(string(policy.Decision))),
			BeforeRef:    "policy:before",
			AfterRef:     "policy:" + strings.TrimSpace(string(policy.Decision)),
			EvidenceRefs: []string{"event:" + sourceEventID, "surface:tool_policy"},
			GeneratedBy:  "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitFailureDeltaIntentFromGate(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil || !runnerAutonomyPolicyDecisionIsFailure(input.Decision) {
		return
	}

	protocolHash := textutil.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
	if strings.TrimSpace(protocolHash) == "" {
		return
	}

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "failure_delta",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(input.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventGateDecision),
		FailureDelta: &autonomy.FailureDelta{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			FailureClass: textutil.FirstNonEmpty(runnerAutonomyReasonCode(input.ReasonCode), string(input.Gate), strings.TrimSpace(string(input.Decision))),
			BeforeRef:    "gate:before",
			AfterRef:     "gate:" + strings.TrimSpace(string(input.Decision)),
			EvidenceRefs: []string{"event:" + sourceEventID, "surface:" + string(input.Gate)},
			GeneratedBy:  "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelResponseObservationFromPolicy(policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || policy.ParserFailure == "" || policy.ParserFailure == autonomy.ParserFailureNone {
		return
	}

	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "validation_signal",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		ValidationSignal: &autonomy.ValidationSignal{
			ContractID:    a.contract.ContractID,
			ProtocolHash:  protocolHash,
			ValidationRef: "parser:" + strings.TrimSpace(string(policy.ParserFailure)),
			Status:        "model_response_observed_non_authoritative",
			EvidenceRefs:  []string{"event:" + sourceEventID, "basis:runner_parser_policy_result"},
			CapturedBy:    "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelResponseObservationFromGate(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil || input.Gate != autonomy.GateCompletion || !runnerAutonomyPolicyDecisionIsFailure(input.Decision) {
		return
	}

	protocolHash := textutil.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
	if strings.TrimSpace(protocolHash) == "" {
		return
	}

	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "validation_signal",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(input.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventGateDecision),
		ValidationSignal: &autonomy.ValidationSignal{
			ContractID:    a.contract.ContractID,
			ProtocolHash:  protocolHash,
			ValidationRef: "completion_gate:" + strings.TrimSpace(string(input.Decision)),
			Status:        "model_response_observed_non_authoritative",
			EvidenceRefs:  []string{"event:" + sourceEventID, "basis:runner_completion_gate_decision"},
			CapturedBy:    "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitRollbackControlIfDisabled(protocolHash string) {
	if a == nil || !runnerAutonomyModelExchangeDisabled() {
		return
	}

	protocolHash = textutil.FirstNonEmpty(strings.TrimSpace(protocolHash), a.activeProtocolHash())
	if protocolHash == "" {
		return
	}

	a.emitModelExchangeEventIgnoringRollback(autonomy.ContractModelExchangeEvent{
		Kind:         "rollback_control",
		ContractID:   a.contract.ContractID,
		ProtocolHash: protocolHash,
		PhaseID:      a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:  a.lastTurnPhaseEvent.ModelCallID,
		RollbackControl: &autonomy.RollbackControl{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			RollbackRef:  "env:" + runnerAutonomyDisableModelExchangeEnv,
			Decision:     "model_exchange_disabled",
			OperatorRef:  "operator:env",
			Source:       "runner_autonomy",
			PreviousMode: "observe_only_model_exchange",
			NewMode:      "disabled",
			Reason:       "rollback switch active",
			GuardRefs:    []string{"guard:typed_autonomy_events_remain_enabled"},
			AffectedPackIDs: []string{
				string(autonomy.ContractPackCompletionReadiness),
				string(autonomy.ContractPackVerificationPostWrite),
				string(autonomy.ContractPackWriteRequiredPaths),
			},
			AuthorizedBy: "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelExchangeEvent(event autonomy.ContractModelExchangeEvent) {
	if a == nil || runnerAutonomyModelExchangeDisabled() {
		return
	}

	a.emitModelExchangeEventIgnoringRollback(event)
}

func (a *runnerAutonomyRecorder) emitModelExchangeEventIgnoringRollback(event autonomy.ContractModelExchangeEvent) {
	if a == nil {
		return
	}

	event.ContractID = textutil.FirstNonEmpty(strings.TrimSpace(event.ContractID), a.contract.ContractID)
	if strings.TrimSpace(event.ProtocolHash) == "" || strings.TrimSpace(event.Kind) == "" {
		return
	}

	autonomyEvent, err := autonomy.NewModelExchangeEvent(a.contract, event, a.nextTime(), a.nextSequence())
	if err != nil {
		return
	}

	a.emitNewEvent(autonomyEvent, err)
}

func runnerAutonomyModelExchangeDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(runnerAutonomyDisableModelExchangeEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func compileModelExchangeSchemaNames() []string {
	return []string{
		autonomy.ContractEnvelopeSchemaVersion,
		autonomy.ContextPacketSchemaVersion,
		autonomy.ModelResponseSchemaVersion,
		autonomy.PermissionSnapshotSchemaVersion,
		autonomy.ValidationSignalSchemaVersion,
		autonomy.FailureDeltaSchemaVersion,
		autonomy.TraceStorePolicySchemaVersion,
		autonomy.RollbackControlSchemaVersion,
	}
}

func runnerAutonomyPermissionCapabilityRefs(policy autonomy.ContractPolicyEvent) []string {
	refs := []string{}
	if policy.Capability != "" {
		refs = append(refs, "capability:"+string(policy.Capability))
	}

	if policy.SideEffect != "" {
		refs = append(refs, "side_effect:"+string(policy.SideEffect))
	}

	if policy.ActionClass != "" {
		refs = append(refs, "action_class:"+string(policy.ActionClass))
	}

	if policy.Transport != "" {
		refs = append(refs, "transport:"+string(policy.Transport))
	}

	if policy.Decision != "" {
		refs = append(refs, "decision:"+string(policy.Decision))
	}

	if policy.ArgumentsDigest != "" {
		refs = append(refs, "arguments_digest:"+policy.ArgumentsDigest)
	}

	return runnerAutonomyBoundedFactStrings(refs, 12)
}

func runnerAutonomyPermissionPathRoleRefs(policy autonomy.ContractPolicyEvent) []string {
	refs := []string{}

	for _, match := range policy.PathRoles {
		path := textutil.FirstNonEmpty(match.Path, match.MatchedPattern, match.PacketID)
		if path != "" && match.Role != "" {
			refs = append(refs, string(match.Role)+":"+path)
		}
	}

	for _, path := range policy.ProtectedMatches {
		refs = append(refs, "protected:"+path)
	}

	for _, path := range policy.RequiredEditMatches {
		refs = append(refs, "required_edit:"+path)
	}

	for _, path := range policy.OutsideWorkspace {
		refs = append(refs, "outside_workspace:"+path)
	}

	return runnerAutonomyBoundedFactStrings(refs, 24)
}

func (a *runnerAutonomyRecorder) activeProtocolHash() string {
	if a == nil {
		return ""
	}

	return textutil.FirstNonEmpty(a.currentModelProtocolHash(), strings.TrimSpace(a.lastTurnPhaseEvent.ProtocolHash), a.lastStoredProtocolHash)
}

func runnerAutonomyPolicyDecisionIsFailure(decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyDeny, autonomy.PolicyReject, autonomy.PolicyStop:
		return true
	default:
		return false
	}
}

func (a *runnerAutonomyRecorder) emitNewEvent(event autonomy.Event, err error) {
	if a == nil || err != nil {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	a.runner.emitEvent(RunEvent{Role: runnerAutonomyEventRole, Content: string(data)})
}

func (a *runnerAutonomyRecorder) emitPolicyEvent(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if a == nil {
		return
	}

	policy := a.policyEventForRequest(req, input)
	event, err := autonomy.NewPolicyEvent(input.EventType, a.contract, policy, a.nextTime(), a.nextSequence())
	a.emitNewEvent(event, err)

	if err == nil && input.EventType == autonomy.EventToolPolicyResult {
		a.emitPermissionSnapshot(req, policy, event.EventID)
		a.emitPolicyFriction(req, input, policy, event.EventID)
		a.emitFailureDeltaIntentFromPolicy(policy, event.EventID)
		a.emitModelResponseObservationFromPolicy(policy, event.EventID)
	}
}

func (a *runnerAutonomyRecorder) emitPolicyFriction(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput, policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || runnerAutonomyPolicyFrictionSuppressed(input) {
		return
	}

	decision, ok := runnerAutonomyFrictionDecision(policy.Decision)
	if !ok {
		return
	}

	phase := a.lastTurnPhase
	protocol := a.visibleModelProtocol()
	protocolVisible := a.currentModelProtocolHash() != ""
	friction := autonomy.ContractGuardFrictionEvent{
		Step:                 phase.Step,
		Lane:                 a.contract.Scope.Lane,
		Phase:                phase.Phase,
		PhaseID:              phase.PhaseID,
		ModelCallID:          phase.ModelCallID,
		SourceEventID:        sourceEventID,
		ContractVersion:      a.contract.SchemaVersion,
		ProtocolHash:         a.currentModelProtocolHash(),
		InvariantID:          strings.TrimSpace(policy.InvariantID),
		ObligationID:         firstRunnerAutonomyString(a.openObligationIDs()),
		Subject:              autonomy.GuardFrictionSubjectToolCall,
		Decision:             decision,
		GuardSurface:         runnerAutonomyPolicyFrictionSurface(policy),
		FirstInvalidAction:   runnerAutonomyPolicyFrictionInvalidAction(req, policy),
		VisibleActionClasses: runnerAutonomyFrictionVisibleActionClasses(protocol, phase, protocolVisible),
		VisibleTools:         runnerAutonomyFrictionVisibleTools(protocol, phase, protocolVisible),
		SafeAlternatives:     runnerAutonomyFrictionSafeAlternatives(protocol, phase, protocolVisible),
		ModelContractVisible: protocolVisible,
		RetryOutcome:         autonomy.GuardFrictionRetryNotApplicable,
		Avoidability:         autonomy.GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         runnerAutonomyPolicyFrictionEvidenceRefs(input, policy),
	}
	a.emitNewEvent(autonomy.NewGuardFrictionEvent(a.contract, friction, a.nextTime(), a.nextSequence()))
}

func runnerAutonomyPolicyFrictionSuppressed(input runnerAutonomyPolicyEventInput) bool {
	return strings.TrimSpace(input.Source) == "runner_required_edit_scope"
}

func runnerAutonomyPolicyFrictionSurface(policy autonomy.ContractPolicyEvent) autonomy.GuardFrictionSurface {
	if policy.ParserFailure != "" && policy.ParserFailure != autonomy.ParserFailureNone {
		return autonomy.GuardFrictionSurfaceParser
	}

	return autonomy.GuardFrictionSurfaceToolPolicy
}

func runnerAutonomyPolicyFrictionInvalidAction(req workspace.ToolRequest, policy autonomy.ContractPolicyEvent) string {
	if tool := strings.TrimSpace(policy.ToolName); tool != "" {
		return tool
	}

	if tool := strings.TrimSpace(req.Tool); tool != "" {
		return tool
	}

	return "tool_call"
}

func runnerAutonomyPolicyFrictionEvidenceRefs(input runnerAutonomyPolicyEventInput, policy autonomy.ContractPolicyEvent) []string {
	refs := []string{"reason:" + runnerAutonomyReasonCode(policy.ReasonCode)}
	if policy.Source != "" {
		refs = append(refs, "source:"+runnerAutonomyReasonCode(policy.Source))
	}

	if policy.ParserFailure != "" && policy.ParserFailure != autonomy.ParserFailureNone {
		refs = append(refs, "parser_failure:"+runnerAutonomyReasonCode(string(policy.ParserFailure)))
	}

	if input.ErrorClass != "" {
		refs = append(refs, "error_class:"+runnerAutonomyReasonCode(input.ErrorClass))
	}

	return refs
}

func runnerAutonomyParityEvidenceRefs(report ParityReport) []string {
	refs := []string{}
	if report.ContractID != "" {
		refs = append(refs, "parity_contract_id:"+report.ContractID)
	}
	if report.ProtocolHash != "" {
		refs = append(refs, "parity_protocol_hash:"+report.ProtocolHash)
	}
	if report.SurfaceID != "" {
		refs = append(refs, "parity_surface:"+report.SurfaceID)
	}
	if report.InvariantID != "" {
		refs = append(refs, "parity_invariant:"+report.InvariantID)
	}
	if report.OldDecision != "" {
		refs = append(refs, "parity_old_decision:"+report.OldDecision)
	}
	if report.NewDecision != "" {
		refs = append(refs, "parity_new_decision:"+report.NewDecision)
	}
	refs = append(refs, "parity_diverged:"+strconv.FormatBool(report.Diverged))
	if report.DivergenceReason != "" {
		refs = append(refs, "parity_reason:"+report.DivergenceReason)
	}
	return refs
}

func (a *runnerAutonomyRecorder) policyEventForRequest(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ContractPolicyEvent {
	intent := runnerAutonomyIntentForRequest(a.brief, req, input)
	action := autonomy.ActionFromIntent(intent)
	policy := autonomy.ContractPolicyEvent{
		ToolName:         strings.TrimSpace(req.Tool),
		PackID:           input.PackID,
		ActionClass:      action.ActionClass,
		ActionKey:        action.ActionKey,
		Transport:        intent.Transport,
		ParserFailure:    intent.ParserFailure,
		RepairSource:     intent.RepairSource,
		Capability:       intent.Capability,
		SideEffect:       intent.SideEffect,
		ArgumentsDigest:  runnerAutonomyArgumentsDigest(req),
		Decision:         input.Decision,
		InvariantID:      strings.TrimSpace(input.InvariantID),
		ReasonCode:       strings.TrimSpace(input.ReasonCode),
		ApprovalRequired: input.ApprovalRequired,
		Result:           strings.TrimSpace(input.Result),
		ErrorClass:       strings.TrimSpace(input.ErrorClass),
		ChangedPaths:     compactRunnerAutonomyPaths(input.ChangedPaths),
		EvidencePaths:    compactRunnerAutonomyPaths(input.EvidencePaths),
		Source:           strings.TrimSpace(input.Source),
	}
	resolution, ok := a.resolvePolicyPaths(req)

	if ok {
		policy.PathRoles = resolution.PolicyPathMatches()
		for _, item := range resolution.Resolutions {
			switch item.EffectiveRole {
			case autonomy.PathRoleProtected:
				policy.ProtectedMatches = append(policy.ProtectedMatches, item.Path)
			case autonomy.PathRoleRequiredEdit:
				policy.RequiredEditMatches = append(policy.RequiredEditMatches, item.Path)
			}

			if item.OutsideWorkspace {
				policy.OutsideWorkspace = append(policy.OutsideWorkspace, item.Path)
			}

			if policy.InvariantID == "" && item.EffectiveInvariantID != "" {
				policy.InvariantID = string(item.EffectiveInvariantID)
			}

			if policy.ReasonCode == "" && item.ReasonCode != "" {
				policy.ReasonCode = item.ReasonCode
			}

			if policy.SideEffect == autonomy.SideEffectUnknown {
				for _, role := range item.Roles {
					if role.SideEffect != "" {
						policy.SideEffect = role.SideEffect
						break
					}
				}
			}
		}

		normalizeRequiredEditScopePolicyEvent(&policy, req, resolution)
	}

	policy.ProtectedMatches = compactRunnerAutonomyPaths(policy.ProtectedMatches)
	policy.RequiredEditMatches = compactRunnerAutonomyPaths(policy.RequiredEditMatches)
	policy.OutsideWorkspace = compactRunnerAutonomyPaths(policy.OutsideWorkspace)

	return policy
}

func runnerAutonomyIntentForRequest(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ToolIntent {
	transport := input.Transport
	if transport == "" {
		transport = autonomy.ToolTransportTextualCall
	}

	intent := autonomy.ToolIntent{
		ToolName:        strings.TrimSpace(req.Tool),
		Capability:      runnerAutonomyPrimaryCapability(brief, req.Tool),
		SideEffect:      runnerAutonomySideEffectForBrief(brief, req),
		ActionClass:     workspace.ToolRequestActionClass(req),
		Transport:       transport,
		ParserFailure:   input.ParserFailure,
		RepairSource:    input.RepairSource,
		TargetRefs:      runnerAutonomyPolicyPaths(req),
		ArgumentsDigest: runnerAutonomyArgumentsDigest(req),
		Source:          input.Source,
	}

	return autonomy.CanonicalizeToolIntent(intent)
}

func normalizeRequiredEditScopePolicyEvent(policy *autonomy.ContractPolicyEvent, req workspace.ToolRequest, resolution autonomy.PathRoleResolutionSet) {
	if policy == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return
	}

	if policy.Decision != autonomy.PolicyDeny && policy.Decision != autonomy.PolicyReject {
		return
	}

	if strings.TrimSpace(policy.ReasonCode) != "required_edit_scope_rejected" &&
		strings.TrimSpace(policy.ErrorClass) != "required_edit_scope" {

		return
	}

	hasOutOfScopeTarget := false

	for _, item := range resolution.Resolutions {
		if item.EffectiveRole == autonomy.PathRoleProtected {
			return
		}

		if item.EffectiveRole != autonomy.PathRoleRequiredEdit {
			hasOutOfScopeTarget = true
		}
	}

	if !hasOutOfScopeTarget {
		return
	}

	policy.InvariantID = string(autonomy.InvariantForbiddenPath)
	policy.ReasonCode = "forbidden_path"
	policy.Result = "denied"
}

func (a *runnerAutonomyRecorder) resolvePolicyPaths(req workspace.ToolRequest) (autonomy.PathRoleResolutionSet, bool) {
	if a == nil {
		return autonomy.PathRoleResolutionSet{}, false
	}

	paths := runnerAutonomyPolicyPaths(req)
	if len(paths) == 0 {
		return autonomy.PathRoleResolutionSet{}, false
	}

	workspaceRoot := ""
	if a.runner != nil && a.runner.Workspace != nil {
		workspaceRoot = a.runner.Workspace.Root()
	}

	resolution, err := autonomy.ResolveContractPathRoles(a.contract, workspaceRoot, paths...)
	if err != nil {
		return autonomy.PathRoleResolutionSet{}, false
	}

	return resolution, true
}

func (a *runnerAutonomyRecorder) nextSequence() int64 {
	a.sequence++
	return a.sequence
}

func (a *runnerAutonomyRecorder) nextTime() time.Time {
	if a.occurredAt.IsZero() {
		a.occurredAt = time.Now().UTC()
	}

	return a.occurredAt.Add(time.Duration(a.sequence+1) * time.Millisecond)
}

func (a *runnerAutonomyRecorder) obligationIDsForGate(gate autonomy.GateKind) []string {
	if a == nil {
		return nil
	}

	obligations := a.obligationsForGate(gate)
	ids := make([]string, 0, len(obligations))

	for _, obligation := range obligations {
		ids = append(ids, obligation.ObligationID)
	}

	return ids
}

func (a *runnerAutonomyRecorder) obligationsForGate(gate autonomy.GateKind) []autonomy.Obligation {
	if a == nil {
		return nil
	}

	var kinds []autonomy.ObligationKind

	switch gate {
	case autonomy.GateWrite:
		kinds = []autonomy.ObligationKind{autonomy.ObligationWrite}
	case autonomy.GateVerification:
		kinds = []autonomy.ObligationKind{autonomy.ObligationVerify}
	case autonomy.GateCompletion:
		kinds = []autonomy.ObligationKind{autonomy.ObligationFinalAnswer, autonomy.ObligationWrite, autonomy.ObligationVerify, autonomy.ObligationEvidence}
	case autonomy.GateLoop:
		kinds = []autonomy.ObligationKind{autonomy.ObligationLoop}
	case autonomy.GateBudget:
		kinds = []autonomy.ObligationKind{autonomy.ObligationBudget}
	case autonomy.GateReadOnly:
		kinds = []autonomy.ObligationKind{autonomy.ObligationEvidence, autonomy.ObligationFinalAnswer}
	}

	kindSet := make(map[autonomy.ObligationKind]struct{}, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = struct{}{}
	}

	var obligations []autonomy.Obligation

	for _, obligation := range a.obligations {
		if _, ok := kindSet[obligation.Kind]; ok {
			obligations = append(obligations, obligation)
		}
	}

	return obligations
}

func runnerAutonomyVerificationStatus(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	if runnerBriefRequiresMutation(brief) && workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return "not_required"
	}

	if runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		if _, failed := failedVerificationAfterLatestWrite(results, brief); failed {
			return "failed"
		}

		if hasPendingVerificationAfterWrites(results, brief) || sourceEditsNeedSemanticVerification(brief, results) {
			return "pending"
		}

		if hasSuccessfulVerificationResult(results) {
			return "passed"
		}

		if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
			return "not_required"
		}
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if workspace.AnalyzeVerificationRequest(workspace.ToolRequest{Tool: result.Tool}).Attempted {
			if strings.TrimSpace(result.Error) != "" {
				return "failed"
			}

			return "observed"
		}
	}

	return "pending"
}

func runnerAutonomyVerificationStatusResolvesPostWriteObligation(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "success", "succeeded", "ok", "clean", "verified", "resolved", "observed", "not_required":
		return true
	default:
		return false
	}
}

func runnerAutonomyRequiredPathStatus(contract autonomy.AutonomyContract, results []workspace.ToolResult) []string {
	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	var status []string

	for _, role := range contract.PathRoles {
		if role.Role != autonomy.PathRoleRequiredEdit {
			continue
		}

		path := NormalizeRunnerPath(role.PathPattern)
		if path == "" {
			continue
		}

		state := "pending"
		if _, ok := changed[path]; ok {
			state = "changed"
		}

		status = append(status, path+":"+state)
	}

	return status
}

func runnerAutonomyBudgetTarget(policy RunPolicy) string {
	var parts []string

	if policy.MaxToolSteps > 0 {
		parts = append(parts, "max_tool_steps")
	}

	if policy.MaxModelCalls > 0 {
		parts = append(parts, "max_model_calls")
	}

	if policy.MaxOperationalModelCalls > 0 {
		parts = append(parts, "max_operational_model_calls")
	}

	if policy.MaxContextMessages > 0 {
		parts = append(parts, "max_context_messages")
	}

	if policy.MaxContextChars > 0 {
		parts = append(parts, "max_context_chars")
	}

	if len(parts) == 0 {
		return "unbounded"
	}

	return strings.Join(parts, ",")
}

func runnerAutonomyReasonCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unspecified"
	}

	var b strings.Builder

	lastUnderscore := false

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)

			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')

				lastUnderscore = true
			}
		}

		if b.Len() >= 80 {
			break
		}
	}

	out := strings.Trim(strings.TrimSpace(b.String()), "_")
	if out == "" {
		return "unspecified"
	}

	return out
}

func runnerAutonomyObligationID(kind autonomy.ObligationKind, target string) string {
	target = strings.TrimSpace(filepath.ToSlash(target))
	if target == "" {
		return "obl-" + string(kind)
	}

	return "obl-" + string(kind) + "-" + digestRunnerAutonomyValue(target)[:12]
}

func runnerAutonomyArgumentsDigest(req workspace.ToolRequest) string {
	normalized := workspace.ToolRequest{
		Tool:            strings.TrimSpace(req.Tool),
		Path:            strings.TrimSpace(req.Path),
		Pattern:         strings.TrimSpace(req.Pattern),
		Query:           strings.TrimSpace(req.Query),
		Task:            strings.TrimSpace(req.Task),
		Purpose:         strings.TrimSpace(req.Purpose),
		Workdir:         strings.TrimSpace(req.Workdir),
		Command:         strings.TrimSpace(req.Command),
		Mode:            strings.TrimSpace(req.Mode),
		RequiredTests:   append([]string(nil), req.RequiredTests...),
		ForbiddenPaths:  append([]string(nil), req.ForbiddenPaths...),
		Destination:     strings.TrimSpace(req.Destination),
		Declarations:    append([]string(nil), req.Declarations...),
		Moves:           append([]workspace.GoDeclarationMove(nil), req.Moves...),
		NamedGap:        strings.TrimSpace(req.NamedGap),
		WhyNotAnswering: strings.TrimSpace(req.WhyNotAnswering),
		Description:     strings.TrimSpace(req.Description),
		TaskID:          strings.TrimSpace(req.TaskID),
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}

	return "sha256:" + digestRunnerAutonomyValue(string(data))
}

func runnerAutonomyPrimaryCapability(brief workerruntime.WorkerRuntimeBrief, tool string) autonomy.Capability {
	capabilities := runnerAutonomyCapabilitiesForTool(brief, strings.TrimSpace(tool))
	if len(capabilities) == 0 {
		return ""
	}

	return capabilities[0]
}

func runnerAutonomyPolicyPaths(req workspace.ToolRequest) []string {
	tool := strings.TrimSpace(req.Tool)
	if workspace.IsRuntimeInspectionRequest(req) || workspace.IsLanguageToolName(tool) ||
		tool == workspace.ToolNameGitDiff || tool == workspace.ToolNameGitStatus {

		for _, path := range []string{req.Path, req.Workdir} {
			if trimmed := strings.TrimSpace(path); trimmed != "" {
				return []string{trimmed}
			}
		}

		return nil
	}

	return writeRequestPaths(req)
}

func runnerAutonomySideEffectForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) autonomy.SideEffectClass {
	if metadata, ok := runnerAutonomyPlannerToolMetadata(brief, req.Tool); ok {
		return metadata.SideEffect
	}

	return runnerAutonomyFallbackSideEffectForRequest(req)
}

func runnerAutonomyFallbackSideEffectForRequest(req workspace.ToolRequest) autonomy.SideEffectClass {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case workspace.ToolNameApplyPatch:
		if effect, ok := runnerAutonomyPatchSideEffect(req.Content); ok {
			return effect
		}

		return autonomy.SideEffectUnknown
	case workspace.ToolNameShell, workspace.ToolNameSourceSanity:
		return workspace.AnalyzeShellCommand(req.Command).SideEffect
	}

	if semanticlanguage.IsDiagnosticToolName(tool) {
		return workspace.AnalyzeShellCommand(req.Command).SideEffect
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return autonomy.SideEffectAdditive
	}

	if workspace.IsRuntimeInspectionRequest(req) || workspace.IsLanguageToolName(tool) ||
		tool == workspace.ToolNameGitDiff || tool == workspace.ToolNameGitStatus {

		return autonomy.SideEffectReadOnly
	}

	return autonomy.SideEffectUnknown
}

func runnerAutonomyPatchSideEffect(patch string) (autonomy.SideEffectClass, bool) {
	ops, err := patching.Parse(patch)
	if err != nil {
		return "", false
	}

	effect := autonomy.SideEffectReadOnly

	for _, op := range ops {
		switch op.Kind {
		case "delete":
			return autonomy.SideEffectDestructive, true
		case "add", "update":
			if op.MoveTo != "" {
				return autonomy.SideEffectDestructive, true
			}

			effect = autonomy.SideEffectAdditive
		}
	}

	if effect == autonomy.SideEffectReadOnly {
		return "", false
	}

	return effect, true
}

func compactRunnerAutonomyPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	slices.Sort(out)

	return out
}

func digestRunnerAutonomyValue(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func mapAutonomyCapabilityKeys(values map[autonomy.Capability]struct{}) []autonomy.Capability {
	out := make([]autonomy.Capability, 0, len(values))
	for value := range values {
		out = append(out, value)
	}

	return out
}

func runnerBriefWithPhaseHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	brief = runnerBriefWithRequiredEditPathHints(brief, results)

	switch phase := debugLogPhase(brief, results); phase {
	case "latest_log_selection":
		brief.Constraints = textutil.Merge([]string{"latest_log_selection_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"select the newest real log artifact before inspecting source files",
			"prefer log directories and log extensions over source files whose names merely mention logging",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"source-code inspection before selecting a concrete log artifact",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"prefer newest actual log artifacts over source files with log-like names",
		}, brief.SearchPolicy)
	case "latest_log_tail":
		brief.Constraints = textutil.Merge([]string{"latest_log_tail_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"selected-log tail evidence is required before source inspection or broader log expansion",
			"the selected log remains the evidence boundary until it supports a weird decision or an explicit missing datum",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"additional workspace discovery before selected-log tail evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"selected-log tail evidence must precede deeper search or source inspection",
		}, brief.SearchPolicy)
	case "selected_log_analysis":
		brief.Constraints = textutil.Merge([]string{"selected_log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"answer from the selected log if the weird decision is already clear",
			"otherwise close one named missing selected-log fact before answering",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"source-code inspection outside the selected log before answering",
			"another blind selected-log reread before a concrete marker",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"stay inside the selected log until the answer is supported or the gap is explicit",
		}, brief.SearchPolicy)
	case "failure_window":
		brief.Constraints = textutil.Merge([]string{"failure_window_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"bounded failure-window evidence is required around the concrete failure hit",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"more log searching before failure-window evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"failure-window evidence must follow a concrete hit before broader log expansion",
		}, brief.SearchPolicy)
	case "log_analysis":
		brief.Constraints = textutil.Merge([]string{"log_analysis_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"answer from the gathered log evidence now unless one specific missing datum remains",
		}, brief.RequiredNext)
	}

	switch phase := evidenceFirstMutationPhase(brief, results); phase {
	case "source_evidence":
		brief.Constraints = textutil.Merge([]string{"source_evidence_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"non-target implementation evidence is required before named-target evidence",
			"after concrete non-target evidence, one bounded named-target evidence window satisfies the target-entry contract",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"open-ended named-target read before source evidence",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"prefer semantic inspection of discovered implementation files before target reads",
		}, brief.SearchPolicy)
	case "target_entry":
		brief.Constraints = textutil.Merge([]string{"first_target_read_pending"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"one bounded named-target evidence window is required before renewed source discovery",
			"the named target remains the evidence boundary for the next step",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"additional source discovery before the first target read",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"enter the named target as soon as concrete source evidence exists",
		}, brief.SearchPolicy)
	case "target_inspection":
		brief.Constraints = textutil.Merge([]string{"target_inspection_phase"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"make the edit in the named target once the change is supported by the target evidence",
			"additional inspection is valid only for a named missing target fact and must stay scoped to named-target evidence",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
			"source discovery outside the named target during target inspection",
			"open-ended target-body read during target inspection",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
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
		brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_plan_pending"}, brief.Constraints)

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

			brief.RequiredNext = textutil.Merge([]string{readInstruction}, brief.RequiredNext)
			brief.InvalidActions = textutil.Merge([]string{
				"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
				"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
				"prose final answer while required source evidence is pending",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"new orientation read/search before the next unread required_edit_path source-body read",
			}, brief.InvalidActions)
			brief.SearchPolicy = textutil.Merge([]string{
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

			brief.RequiredNext = textutil.Merge([]string{
				fmt.Sprintf("refresh bounded source-body evidence for %s before corrected submit_plan; then submit a corrected plan using anchors copied from the refreshed source", next),
			}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
			brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_anchor_refresh_required"}, brief.Constraints)
			brief.InvalidActions = textutil.Merge([]string{
				"corrected submit_plan before refreshing the stale or missing edit anchor",
				"final answer, Decision: source-satisfied, or <<QUESTION>> before bounded anchor refresh returns",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
				"open-ended read/search/discovery outside the invalid-anchor source refresh",
			}, rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteSubmitReadySourceHint))
			brief.SearchPolicy = textutil.Merge([]string{
				"stale submit_plan anchors require a bounded source-body refresh before corrected submit_plan",
				"refresh only the rejected required path named by the submit_plan guard",
				"after the refresh, use insert_after_line/content or old_text/new_text copied from fresh source; do not reuse stale anchors",
			}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

			return brief
		}

		brief.RequiredNext = textutil.Merge([]string{
			"submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker",
		}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
		brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_plan_ready"}, brief.Constraints)
		brief.InvalidActions = textutil.Merge([]string{
			"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
			"per-edit write tool in plan-execute planner phase",
			"shell/git/source-sanity verification in plan-execute planner phase",
			"new orientation read/search after enough anchor evidence to draft submit_plan",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"required_edit_paths plus packet evidence define the submit_plan scope",
			"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome",
		}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

		return brief
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	missing := missingRequiredEditPaths(brief.RequiredEditPaths, changed)

	if len(changed) == 0 {
		if briefHasUsableEditAnchors(brief) {
			brief.Constraints = textutil.Merge([]string{"implementation_packet_write_ready"}, brief.Constraints)
		}

		if exactAnchorPreWriteSurfaceActive(brief, results) {
			brief.RequiredNext = textutil.Merge([]string{
				"required-path write capability from the exact target-packet anchor",
				"answer <<QUESTION>> only if the supplied exact anchor is stale or missing",
			}, brief.RequiredNext)
			brief.InvalidActions = textutil.Merge([]string{
				"read/search/task_ledger/shell before first exact-anchor write",
			}, brief.InvalidActions)
			brief.SearchPolicy = textutil.Merge([]string{
				"required_edit_paths plus exact target-packet anchors establish the first write surface",
			}, brief.SearchPolicy)
		} else {
			brief.RequiredNext = textutil.Merge([]string{
				"resolve a required path with packet evidence, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or answer <<QUESTION>> with the exact path-specific blocker",
			}, brief.RequiredNext)
			brief.InvalidActions = textutil.Merge([]string{
				"orientation read/search after required edit paths and packet evidence without a named missing fact",
				"raw read/search outside required edit paths before resolving the required-path contract",
				"task_ledger planning before the first required-path write",
			}, brief.InvalidActions)
			brief.SearchPolicy = textutil.Merge([]string{
				"required_edit_paths plus packet evidence establish the edit scope; prefer semantic/context/declared project discovery for named edit-safety gaps before raw source reads",
			}, brief.SearchPolicy)
		}

		return brief
	}

	if len(missing) > 0 {
		brief.Constraints = textutil.Merge([]string{"implementation_required_paths_remaining"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"resolve each remaining required path with an edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker for that path",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{
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
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = textutil.Merge([]string{workspace.RequiredToolBoundedWorkspaceEvidence}, brief.RequiredTools)
	brief.ReadinessGate = textutil.Merge([]string{"bounded_workspace_evidence_before_final_answer"}, brief.ReadinessGate)
	brief.RequiredNext = textutil.Merge([]string{
		"exactly one parser-readable bounded read/search/context tool call for the user's question, or <<QUESTION>> with the exact evidence blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = textutil.Merge([]string{
		"prose/final answer before a successful bounded evidence tool result",
		"workspace write",
		"shell/mutating command",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"use the narrowest bounded evidence tool that can answer the user's question; answer only after evidence returns",
	}, brief.SearchPolicy)
	brief.Constraints = textutil.Merge([]string{"read_only_evidence_pending"}, brief.Constraints)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

// briefAuditEvent is the structured payload for the per-turn `brief_audit`
// event. The control_rails event already shows the brief's outward-facing
// shape (lane, allowed tools, search_policy). brief_audit fills in the
// fields control_rails omits — Constraints, RequiredEditSnippets,
// TargetPacket size, ChangedPaths, and the full Readiness block — so a
// post-hoc audit can answer "what evidence and contract did the model
// actually receive?" without re-deriving from the prompt text.
type briefAuditEvent struct {
	Lane                         string   `json:"lane,omitempty"`
	TaskFamily                   string   `json:"task_family,omitempty"`
	Contract                     string   `json:"contract,omitempty"`
	PermissionProfile            string   `json:"permission_profile,omitempty"`
	Constraints                  []string `json:"constraints,omitempty"`
	TargetRefs                   []string `json:"target_refs,omitempty"`
	RequiredEditPaths            []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths             []string `json:"allowed_edit_paths,omitempty"`
	RequiredEditSnippetsCount    int      `json:"required_edit_snippets_count"`
	RequiredEditSnippetPaths     []string `json:"required_edit_snippet_paths,omitempty"`
	EvidencePaths                []string `json:"evidence_paths,omitempty"`
	ChangedPaths                 []string `json:"changed_paths,omitempty"`
	TargetPacketLines            int      `json:"target_packet_lines"`
	TargetPacketBytes            int      `json:"target_packet_bytes"`
	ReadinessReadyToAnswer       bool     `json:"readiness_ready_to_answer"`
	ReadinessReadyToWrite        bool     `json:"readiness_ready_to_write"`
	ReadinessReadyToVerify       bool     `json:"readiness_ready_to_verify"`
	ReadinessClosedClaims        []string `json:"readiness_closed_claims,omitempty"`
	ReadinessOpenObligations     []string `json:"readiness_open_obligations,omitempty"`
	ReadinessFurtherReadsRequire string   `json:"readiness_further_reads_require,omitempty"`
	ExactAnchorImplActive        bool     `json:"exact_anchor_implementation_active"`
	TargetPacketSupportsWrite    bool     `json:"target_packet_supports_write"`
	WriteReadyReadContract       bool     `json:"write_ready_read_contract"`
	AllowedToolsCount            int      `json:"allowed_tools_count"`
}

// emitBriefAudit emits a one-shot brief_audit event at turn start so an
// audit can see the brief shape that drove the lane (constraints,
// snippets, packet size, readiness) without re-deriving from the prompt.
func (r *Runner) emitBriefAudit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil {
		return
	}

	allowedToolsCount := len(brief.AllowedTools)
	if surface, ok := DirectAnswerSurfaceForBrief(brief); ok {
		allowedToolsCount = len(surface.ToolNames)
	}

	event := briefAuditEvent{
		Lane:                      strings.TrimSpace(brief.Lane),
		TaskFamily:                strings.TrimSpace(brief.TaskFamily),
		Contract:                  strings.TrimSpace(brief.Contract),
		PermissionProfile:         strings.TrimSpace(brief.PermissionProfile),
		Constraints:               append([]string(nil), brief.Constraints...),
		TargetRefs:                append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:         append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:          append([]string(nil), brief.AllowedEditPaths...),
		RequiredEditSnippetsCount: len(brief.RequiredEditSnippets),
		EvidencePaths:             append([]string(nil), brief.EvidencePaths...),
		ChangedPaths:              append([]string(nil), brief.ChangedPaths...),
		TargetPacketLines:         len(brief.TargetPacket),
		TargetPacketBytes:         targetPacketByteCount(brief.TargetPacket),
		ExactAnchorImplActive:     exactAnchorImplementationActive(brief),
		TargetPacketSupportsWrite: targetPacketSupportsWrite(brief),
		WriteReadyReadContract:    writeReadyPacketReadContractActive(brief, results),
		AllowedToolsCount:         allowedToolsCount,
	}

	if len(brief.RequiredEditSnippets) > 0 {
		event.RequiredEditSnippetPaths = make([]string, 0, len(brief.RequiredEditSnippets))

		seen := make(map[string]struct{}, len(brief.RequiredEditSnippets))

		for _, snippet := range brief.RequiredEditSnippets {
			path := strings.TrimSpace(snippet.Path)
			if path == "" {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			event.RequiredEditSnippetPaths = append(event.RequiredEditSnippetPaths, path)
		}
	}

	if brief.Readiness != nil {
		event.ReadinessReadyToAnswer = brief.Readiness.ReadyToAnswer
		event.ReadinessReadyToWrite = brief.Readiness.ReadyToWrite
		event.ReadinessReadyToVerify = brief.Readiness.ReadyToVerify
		event.ReadinessClosedClaims = append([]string(nil), brief.Readiness.ClosedClaims...)
		event.ReadinessOpenObligations = append([]string(nil), brief.Readiness.OpenObligations...)
		event.ReadinessFurtherReadsRequire = strings.TrimSpace(brief.Readiness.FurtherReadsRequire)
	}

	r.emitEvent(marshalRunEvent("brief_audit", event))
}

func targetPacketByteCount(packet []string) int {
	total := 0
	for _, line := range packet {
		total += len(line)
		total++
	}

	if total > 0 {
		total--
	}

	return total
}

func requestPathInRequiredEditPaths(path string, required []string) bool {
	cleaned := NormalizeRunnerPath(path)
	if cleaned == "" {
		return false
	}

	for _, p := range required {
		if NormalizeRunnerPath(p) == cleaned {
			return true
		}
	}

	return false
}

func unresolvedRejectedMutationPaths(results []workspace.ToolResult, requiredPaths []string) []string {
	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	required := readOnlyPathSet(requiredPaths)

	var rejected []string

	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		paths := readOnlySourceLikePaths(ReadPathsFromToolResult(result), 8)

		paths = textutil.Merge(paths, readOnlySourceLikePaths(sourceLikePathsFromText(result.Summary+"\n"+result.Output+"\n"+result.Error, 8), 8))

		for _, path := range paths {
			path = NormalizeRunnerPath(path)
			if path == "" {
				continue
			}

			if len(required) > 0 {
				if _, ok := required[path]; !ok {
					continue
				}
			}

			if _, ok := changed[path]; ok {
				continue
			}

			rejected = textutil.AppendUniqueMulti(rejected, path)
		}
	}

	return uniqueSortedRunnerPaths(rejected)
}

func unsupportedClaimedChangedPaths(reply string, results []workspace.ToolResult) []string {
	claimed := claimedChangedPathsFromAnswer(reply)
	if len(claimed) == 0 {
		return nil
	}

	actual := newToolResultEvidenceProjection(results).ChangedPathSet()
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
	claimed = NormalizeRunnerPath(claimed)
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
	path = NormalizeRunnerPath(path)
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
		normalized := NormalizeRunnerPath(path)
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

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
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

	for _, path := range mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet()) {
		if testSourcePathCanRemoveDeclarations(path) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"diff", "--unified=0", "--"}, paths...)
	diff, err := gitstate.CombinedOutputText(context.Background(), root, args...)

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
	if !ContainsAny(lower, "removed test", "deleted test", "dropped test", "removed existing test", "test removal", "removed tests", "deleted tests", "dropped tests") {
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

func requiredEditSnippetCandidatePaths(snippet plancontract.RequiredEditSnippet, required []string, changed map[string]struct{}) []string {
	if path := NormalizeRunnerPath(snippet.Path); path != "" {
		return []string{path}
	}

	var candidates []string

	seen := make(map[string]struct{})

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
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
		path := NormalizeRunnerPath(candidate)
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

func requiredEditSnippetName(snippet plancontract.RequiredEditSnippet, index int) string {
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
			path = NormalizeRunnerPath(path)
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

	return ContainsAny(lower, "verification", "test", "planner", "blocker", "validation", "lane sanity")
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

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func hasPendingVerificationAfterWrites(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	if workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	docLane := briefIsDocDeliverableLane(brief)
	pending := false
	var pendingPaths []string

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) {
			if writeResultMadeNoWorkspaceChange(result) {
				continue
			}

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
			pendingPaths = textutil.AppendUniqueMulti(pendingPaths, newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathList()...)

			continue
		}

		if pending && workspace.AnalyzePostWriteVerificationResult(result, pendingPaths).Successful {
			pending = false
			pendingPaths = nil
		}
	}

	return pending
}

func writeResultMadeNoWorkspaceChange(result workspace.ToolResult) bool {
	if len(newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathList()) > 0 {
		return false
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false
	}

	return strings.Contains(text, "no-op") ||
		strings.Contains(text, "no new changes") ||
		strings.Contains(text, "all sections were redundant")
}

// briefIsDocDeliverableLane reports whether the brief specs a doc-only
// deliverable (refine draft/review/patch, design note write-up, etc.).
// The signal is "every RequiredEditPath is a non-source file". Empty
// RequiredEditPaths is treated as not-doc-only so implementation lanes
// with no specced allowlist still trip the verification gate when they
// happen to touch a markdown file.

func runnerGuidanceConstraints(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	constraints := append([]string(nil), brief.Constraints...)
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		constraints = textutil.Merge(constraints, []string{"plan_then_execute_submit_plan_ready"})
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		strings.TrimSpace(brief.TaskFamily) == "delegated_read_only" &&
		delegatedReadOnlyClosureReady(agent, brief, results) {

		constraints = textutil.Merge(constraints, []string{"answer_readiness_reached"})
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		constraints = textutil.Merge(constraints, []string{"child_packet_available"})
	}

	return constraints
}

func applyDelegatedReadOnlyClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") ||
		strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" ||
		!delegatedReadOnlyClosureReady(agent, brief, results) {

		return state
	}

	state.Constraints = textutil.Merge(state.Constraints, []string{"answer_readiness_reached"})
	state.InvalidNextActions = filterRunnerStateStrings(state.InvalidNextActions, func(value string) bool {
		return value == "final_answer_without_satisfying_completion_gate"
	})
	state.NextValidActions = filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "gather or edit the missing evidence")
	})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the evidence already gathered is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
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

	state.Constraints = textutil.Merge(state.Constraints, []string{"child_packet_available"})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the child packet evidence is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
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
		if ReadOnlyTaskResultAnswerableForObjective(result, objective) {
			return true
		}
	}

	return false
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

func (r *Runner) finalAnswerAcceptanceCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	if !r.replyMayProjectFinalAnswerAcceptanceThroughCompletionGate(reply) {
		return workspace.ToolResult{}, false
	}

	acceptance := r.evaluateFinalAnswerAcceptance(reply, results)
	if acceptance.BlockerKind == "" {
		return workspace.ToolResult{}, false
	}
	if !r.finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance) {
		return workspace.ToolResult{}, false
	}

	return finalAnswerCompletionGateProjection(
		acceptance,
		"Final answer rejected by blocker check",
		r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, reply, results),
	)
}

func (r *Runner) replyMayProjectFinalAnswerAcceptanceThroughCompletionGate(reply string) bool {
	if !r.readOnlyProtocolMetaCompletionProjectionLane() {
		return false
	}

	return looksLikePriorAnswerReferenceFinalAnswer(reply) ||
		looksLikeGateSatisfactionMetaFinalAnswer(reply) ||
		looksLikeEmbeddedQuestionProtocolFinalAnswer(reply)
}

func (r *Runner) finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance finalAnswerAcceptance) bool {
	if !r.readOnlyProtocolMetaCompletionProjectionLane() {
		return false
	}

	switch acceptance.BlockerKind {
	case string(turnViolationProtocolMetaFinalAnswer):
		return true
	default:
		return false
	}
}

func (r *Runner) readOnlyProtocolMetaCompletionProjectionLane() bool {
	return r != nil &&
		!runnerBriefRequiresMutation(r.RuntimeBrief) &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile)
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

	candidateOnlyAssessment, directCandidateOnly := sourceGroundingCandidateOnlyCompletionAssessment(brief, reply, results)
	if brief.Readiness == nil &&
		!readOnlyExplanationGateApplies(brief) &&
		!workerruntime.TaskFamilyMatches(brief.TaskFamily, autonomy.TaskFamilyDirectAnswer) &&
		!directCandidateOnly {

		return workspace.ToolResult{}, false
	}

	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 {
		return workspace.ToolResult{}, false
	}

	if strings.HasPrefix(strings.TrimSpace(reply), "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	if readOnlyClosureDecisionForResults(brief, results).Answerable() {
		return workspace.ToolResult{}, false
	}

	if readOnlyDirectAnswerLane(brief) && readOnlyAnswerReportsInsufficientEvidence(reply) {
		ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)
		if len(ledger.SourceBodyPaths) > 0 || len(ledger.ExternalDocumentBodyPaths) > 0 || ledger.DiagnosticEvidence {
			return workspace.ToolResult{}, false
		}
	}

	diagnosticArtifactSupported := sourceGroundingDiagnosticArtifactCitationSupportsObjective(brief, reply, results)
	if readOnlyDirectAnswerLane(brief) &&
		(readOnlyAnswerHasConcreteEvidence(results) ||
			readOnlyPlanArtifactEvidenceSupportsReply(brief, reply, results) ||
			diagnosticArtifactSupported) {

		if acceptance := r.evaluateFinalAnswerAcceptance(reply, results); acceptance.BlockerKind == "" {
			return workspace.ToolResult{}, false
		}
	}

	if candidateOnlyAssessment.BlocksFinalAnswer() {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Error:   candidateOnlyAssessment.Message(),
		}, true
	}

	if genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}

	if diagnosticArtifactSupported {
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
		diagnostic := result.Facts != nil && len(readOnlySourceLikePaths(result.Facts.DiagnosticEvidencePaths, 8)) > 0
		if concreteSourceEvidenceResult(result) ||
			len((toolResultEvidenceProjection{}).ExternalDocumentBodyPathsFromResult(result)) > 0 ||
			diagnostic {

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
		Error:   "read-only workspace evidence has been gathered, but the reply is not a substantive user-facing answer. A substantive final answer should carry forward the inspected path and the concrete value or fact needed by the question.",
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
	if ContainsAny(lower,
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

	if !ContainsAny(lower,
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

	if ContainsAny(lower, " i'll ", " i will ") && !ContainsAny(lower,
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
// a raw tool result transcript (e.g. line-numbered source fragments or
// "contains `func Foo`"). Broader low-value source inventories are
// handled by final-answer acceptance; this completion gate is only for
// raw transcript/search scaffolding that should never be user-visible.
func looksLikeToolResultTranscript(lower string) bool {
	scanText := strings.ToLower(stripQuotedAndFencedSourceExamples(lower))
	hasLead := replyHasDirectSynthesisLead(scanText)

	if looksLikeLowValueSourceInventoryNonAnswer(scanText) && !hasLead {
		return true
	}

	if looksLikeLineOrientedSourceSnippetAnswer(scanText) && !hasLead {
		return true
	}

	// " contains `f" where f is any function/symbol name
	if !hasLead && (strings.Contains(scanText, " contains `func ") ||
		strings.Contains(scanText, " contains `type ") ||
		strings.Contains(scanText, " contains `var ") ||
		strings.Contains(scanText, " contains `const ")) {

		return true
	}

	return false
}

func replyHasDirectSynthesisLead(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	trimmed = strings.TrimPrefix(trimmed, "**")
	lower := strings.ToLower(strings.TrimSpace(trimmed))
	for _, prefix := range []string{
		"i have sufficient evidence to answer.",
		"i now have sufficient evidence to answer.",
		"i now have enough evidence to answer.",
		"i now have enough evidence to answer both questions.",
		"i now have all the evidence needed for a grounded answer.",
		"now i have sufficient evidence to answer.",
		"now i have enough evidence to answer.",
		"now i have enough evidence to answer both questions.",
		"now i have all the evidence needed for a grounded answer.",
		"now i have the full picture.",
		"let me synthesize.",
	} {
		lower = strings.TrimSpace(strings.TrimPrefix(lower, prefix))
	}

	first := lower
	if idx := strings.Index(first, "\n\n"); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(strings.Trim(first, "* "))
	if first == "" {
		return false
	}

	if strings.HasPrefix(first, "for `") ||
		strings.HasPrefix(first, "`") ||
		strings.HasPrefix(first, "the inspected source") ||
		strings.Contains(first, "clearest inspected implementation anchor") ||
		strings.Contains(first, " inspected body shows ") {

		return false
	}

	return strings.Contains(first, "confirmed:") ||
		strings.HasPrefix(first, "confirmed.") ||
		strings.Contains(first, "the answer is:") ||
		strings.HasPrefix(first, "answer:") ||
		strings.HasPrefix(first, "## answer:") ||
		strings.HasPrefix(first, "yes,") ||
		strings.HasPrefix(first, "no,") ||
		strings.HasPrefix(first, "no —") ||
		strings.HasPrefix(first, "yes —") ||
		(strings.Contains(first, " is ") && len(strings.Fields(first)) >= 8)
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
	unquotedReply := stripQuotedAndFencedSourceExamples(trimmed)
	hasTranscript := looksLikeToolResultTranscript(lower)
	hasSearchHandle := toolLocalSearchHandlePattern.MatchString(unquotedReply)
	if !hasTranscript && !(hasSearchHandle && !replyHasDirectSynthesisLead(trimmed)) {
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
			workspaceLookupTransportInstruction(recoveryToolTransportMode(r.currentPlanningTransport())),
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

	return textutil.ContainsAny(lower,
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

func legacyToolCallHistoryMessage(reply llama.ChatMessage, call llama.ToolCall) llama.ChatMessage {
	return llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		ReasoningContent: reply.ReasoningContent,
		ToolCalls:        []llama.ToolCall{call},
	}
}

func textualToolRequestHistoryMessage(reply llama.ChatMessage, req workspace.ToolRequest, raw string) llama.ChatMessage {
	content := strings.TrimSpace(raw)
	if content == "" || !strings.Contains(content, `"tool"`) {
		content = marshalToolRequestRaw(req)
	}

	content = strings.TrimSpace(content)

	return llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          content,
		ReasoningContent: reply.ReasoningContent,
	}
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
	AllowedEditPaths     []string `json:"allowed_edit_paths,omitempty"`
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

func (r *Runner) controlRailsForBrief(brief workerruntime.WorkerRuntimeBrief) runnerControlRails {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	allowedTools := []string(nil)

	if workerruntime.PermissionProfileIsConversationOnly(brief.PermissionProfile) {
		allowedTools = nil
	} else if len(brief.AllowedTools) > 0 {
		allowedTools = append([]string(nil), brief.AllowedTools...)
	} else {
		allowedTools = r.workspaceAllowedTools()
	}

	if exactAnchorPreWriteSurfaceActive(brief, nil) {
		allowedTools = workspace.ToolSurfacePhaseAllowedTools(allowedTools, workspace.ToolSurfacePhaseImplementationPreWrite)
	} else if implementationScopedEditSurfaceActive(brief, nil) {
		allowedTools = workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(allowedTools, r.workspaceToolSurfaceSnapshot().ExternalToolIsReadOnlyDiscovery)
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
		Lane:                 textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		Contract:             textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		TaskFamily:           textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyDirectAnswer)),
		PermissionProfile:    profile,
		TargetRefs:           append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), brief.AllowedEditPaths...),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        append([]string(nil), brief.RequiredTools...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       append([]string(nil), brief.ForbiddenTools...),
		ReadinessGate:        append([]string(nil), brief.ReadinessGate...),
		SearchPolicy:         append([]string(nil), brief.SearchPolicy...),
		VerificationContract: textutil.FirstNonEmpty(strings.TrimSpace(brief.VerificationContract), string(workerruntime.VerificationNotRequiredConversation)),
		StopConditions:       append([]string(nil), brief.StopConditions...),
		FinalCritic:          append([]string(nil), brief.FinalCritic...),
		DynamicInstructions:  r.InstructionAttacher != nil,
	}
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(rails.PermissionProfile) {
		tools := workerruntime.NormalizeReadOnlyWorkspaceToolSet(workerruntime.RuntimeToolSet{
			AllowedTools:   rails.AllowedTools,
			RequiredTools:  rails.RequiredTools,
			ForbiddenTools: rails.ForbiddenTools,
		})
		rails.AllowedTools = tools.AllowedTools
		rails.RequiredTools = tools.RequiredTools
		rails.ForbiddenTools = tools.ForbiddenTools
	}
	if len(rails.RequiredEvidence) == 0 {
		rails.RequiredEvidence = []string{string(workerruntime.RequiredEvidenceAnswerAddressesUserRequest)}
	}

	if len(rails.StopConditions) == 0 {
		rails.StopConditions = []string{string(workerruntime.StopAnsweredUserRequest), string(workerruntime.StopBlockedByMissingInformation)}
	}

	if runnerBriefRequiresMutation(brief) {
		rails.Lane = textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), "main")
		rails.Contract = textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractImplementationResult))
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyImplementation))
		rails.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionGuardedWorkspaceWrite))

		if planThenExecutePlannerBrief(brief) {
			rails.RequiredEvidence = textutil.Merge([]string{string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied)}, rails.RequiredEvidence)
			rails.RequiredTools = textutil.Merge(workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly), rails.RequiredTools)
			rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, "unbounded_write")
			rails.ReadinessGate = textutil.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, rails.ReadinessGate)
			rails.SearchPolicy = textutil.Merge([]string{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "submit_plan_when_edit_set_is_supported", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"}, rails.SearchPolicy)
			rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredTextMutation))
			rails.StopConditions = textutil.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, rails.StopConditions)
			rails.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}

			return rails
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason), string(workerruntime.RequiredEvidenceVerificationStatus)})
		if exactAnchorPreWriteSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch}
		} else if implementationScopedEditSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSemanticDiscoveryOrNarrowReadForNamedGap}
		} else {
			rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell})
		}

		rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, "unbounded_write")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessChangedPathsReported), string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"})
		rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationTestsPassedFailedSkipped))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopChangeVerified), string(workerruntime.StopBlockedWithFailedVerificationOrScope)})
		rails.FinalCritic = []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"}

		return rails
	}

	if slices.Contains(rails.AllowedTools, workspace.ToolNameShell) && !runnerHasWorkspaceWriteCapability(brief, rails.AllowedTools) && runnerVerificationRequired(brief) {
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), "verification")
		rails.Contract = textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractGroundedExplanation))
		rails.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionReadOnlyVerificationShell))
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceVerificationCommand), string(workerruntime.RequiredEvidencePassFailStatus)})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{workspace.ToolNameShell})
		rails.ForbiddenTools = appendRunnerTools(rails.ForbiddenTools, append(workspace.WorkspaceWriteToolNames(), "mutating_shell")...)
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"run_requested_check", "capture_failure_output", "do_not_edit"})
		rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}

		return rails
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
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyDirectAnswer))
		if len(rails.AllowedTools) == 0 {
			rails.PermissionProfile = string(workerruntime.PermissionConversationOnly)
			rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, workspace.ToolNameShell)
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceAnswerAddressesPrompt)})
		rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredReadOnlyLane))
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
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        namedGap,
		WhyNotAnswering: whyNotAnswering,
	}

	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicNoWriteRecoveryRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != workspace.ToolNameSearchContent {
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
		outcome := "The previous read/search request was rejected because plan-execute submit-correction mode is already ready. Do not read, search, inspect, verify, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or describe the plan in prose. The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence()
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
			outcome = "The previous read/search request was rejected because plan-execute submit-correction mode is already ready. Do not read, search, inspect, verify, answer `Decision: source-satisfied`, or describe the plan in prose. The next visible action must be a parser-readable `submit_plan`, unless no safe executable edit body can be produced from the gathered evidence after repeated rejected submit_plan attempts; in that case answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence()
		}
		parts := []string{
			strings.TrimSpace(errorText),
			outcome,
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

		if tool != workspace.ToolNameContextPack {
			return false
		}

		return strings.TrimSpace(results[i].Error) != "" ||
			results[i].Facts == nil ||
			!results[i].Facts.HasDiscoveryTargets
	}

	return false
}

func alreadyReadPath(results []workspace.ToolResult, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
			return true
		}
	}

	return false
}

type runnerExecutionState struct {
	verificationAttempted            bool
	needsNarrowingAfterBroad         bool
	broadInspectionCount             int
	consecutiveListFiles             int
	pendingSearchInspection          bool
	pendingSearchTool                string
	pendingSearchSummary             string
	pendingSearchOutput              string
	pendingDiscoveryConsumption      bool
	pendingDiscoveryTool             string
	pendingDiscoverySummary          string
	pendingDiscoveryOutput           string
	pendingDiscoveryCandidates       []string
	pendingDiscoveryStrength         discoveryConsumptionStrength
	pendingDiscoveryFailedAttempt    bool
	checkpointCreated                bool
	writesSinceVerification          int
	consecutiveMissingPathListErrors int
	consecutiveToolFailures          int
	lastFailedPathStem               string
	softFailureNudgeUsed             bool
}

type runnerExecutionUpdate struct {
	ResetVerificationGuards bool
	VerificationState       string
	VerificationDetail      string
}

func newRunnerExecutionState(initialNeedsNarrowing bool, initialBroadInspects int) runnerExecutionState {
	return runnerExecutionState{
		needsNarrowingAfterBroad: initialNeedsNarrowing,
		broadInspectionCount:     initialBroadInspects,
	}
}

func (s *runnerExecutionState) shouldRequireVerification(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil {
		return false
	}

	if plannerOwnsVerification(brief) || workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	return s.writesSinceVerification >= 2 && !workspace.AnalyzeVerificationRequest(req).Attempted && strings.TrimSpace(req.Tool) != "task_ledger"
}

func (s *runnerExecutionState) needsCheckpoint(policy CheckpointPolicy, req workspace.ToolRequest) bool {
	if s == nil {
		return false
	}

	return policy.Enabled && !s.checkpointCreated && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool))
}

func (s *runnerExecutionState) markCheckpointCreated() {
	if s == nil {
		return
	}

	s.checkpointCreated = true
}

func (s *runnerExecutionState) applyResult(req workspace.ToolRequest, result workspace.ToolResult, currentFingerprint string, mgr *workspace.Manager, requestBudget *runnerRequestBudgetState) runnerExecutionUpdate {
	if s == nil {
		return runnerExecutionUpdate{}
	}

	update := runnerExecutionUpdate{}

	if workspace.AnalyzeVerificationRequest(req).Attempted {
		s.verificationAttempted = true
		// Any verification attempt closes the "run verification before more
		// inspection" guard. A failed verifier opens a repair path instead;
		// completion readiness is still enforced from ToolResult evidence.
		s.writesSinceVerification = 0

		if requestBudget != nil {
			requestBudget.noteVerification()
		}

		update.ResetVerificationGuards = true
		update.VerificationState, update.VerificationDetail = verificationPhaseState(result)
	} else if toolResultMadeProgress(req, result) && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool)) {
		s.writesSinceVerification++

		if requestBudget != nil {
			requestBudget.noteWriteProgress()
		}
	}

	switch {
	case workspace.IsBroadInspectionRequest(req):
		s.needsNarrowingAfterBroad = true
		s.broadInspectionCount++
	case mgr != nil && mgr.ShouldClearBroadInspectionLock(req, result):
		s.needsNarrowingAfterBroad = false
	}

	if textutil.EqualTrimFold(req.Tool, "list_files") {
		s.consecutiveListFiles++
	} else if workspace.IsRuntimeContentInspectionTool(req.Tool) {
		s.consecutiveListFiles = 0
	}

	sourceBodyConsumed := concreteSourceEvidenceResult(result) || (toolResultMadeProgress(req, result) && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool)))
	if successfulSearchDiscoveryResult(req, result) {
		s.pendingSearchInspection = true
		s.pendingSearchTool = strings.TrimSpace(req.Tool)
		s.pendingSearchSummary = truncForRetryReason(result.Summary)
		s.pendingSearchOutput = truncForRetryReason(result.Output)
	} else if sourceBodyConsumed {
		s.pendingSearchInspection = false
		s.pendingSearchTool = ""
		s.pendingSearchSummary = ""
		s.pendingSearchOutput = ""
	}

	if strength := discoveryConsumptionResultStrengthFromLegacySemantics(req, result); strength != discoveryConsumptionNone {
		if s.shouldReplacePendingDiscovery(strength) {
			s.pendingDiscoveryConsumption = true
			s.pendingDiscoveryTool = strings.TrimSpace(req.Tool)
			s.pendingDiscoverySummary = truncForRetryReason(result.Summary)
			s.pendingDiscoveryOutput = truncForRetryReason(result.Output)
			s.pendingDiscoveryCandidates = discoveryConsumptionCandidatesFromResult(result, 6)
			s.pendingDiscoveryStrength = strength
			s.pendingDiscoveryFailedAttempt = false
		}
	} else if sourceBodyConsumed {
		s.clearPendingDiscoveryConsumption()
	} else if s.pendingDiscoveryConsumption && workspace.IsRuntimeContentInspectionTool(req.Tool) && strings.TrimSpace(result.Error) != "" {
		s.pendingDiscoveryFailedAttempt = true
	}

	if requestBudget != nil {
		if currentFingerprint != "" && strings.TrimSpace(result.Error) == "" && workerruntime.IsZeroHitSearchResult(result) {
			requestBudget.noteZeroHitSearch(req, currentFingerprint, result)
		}

		if currentFingerprint != "" && strings.TrimSpace(result.Error) != "" && strings.Contains(strings.ToLower(result.Summary), "rejected") {
			requestBudget.fingerprint.rejected[currentFingerprint] = truncForRetryReason(result.Summary)
		}
	}

	return update
}

func (s *runnerExecutionState) shouldReplacePendingDiscovery(strength discoveryConsumptionStrength) bool {
	return s.pendingDiscoveryStrength != discoveryConsumptionSourceOwner || strength == discoveryConsumptionSourceOwner
}

func (s *runnerExecutionState) clearPendingDiscoveryConsumption() {
	if s == nil {
		return
	}

	s.pendingDiscoveryConsumption = false
	s.pendingDiscoveryTool = ""
	s.pendingDiscoverySummary = ""
	s.pendingDiscoveryOutput = ""
	s.pendingDiscoveryCandidates = nil
	s.pendingDiscoveryStrength = discoveryConsumptionNone
	s.pendingDiscoveryFailedAttempt = false
}

func verificationPhaseState(result workspace.ToolResult) (state string, detail string) {
	detail = strings.TrimSpace(result.Summary)
	if strings.TrimSpace(result.Error) != "" {
		state = "failed"
		detail = strings.TrimSpace(result.Error)
	} else if workspace.AnalyzeVerificationResult(result).PatchHygieneOnly {
		state = "hygiene"

		if detail == "" {
			detail = "patch hygiene completed"
		} else {
			detail += "; patch hygiene completed"
		}
	} else if workspace.AnalyzeVerificationResult(result).Successful {
		state = "done"
	} else {
		state = "insufficient"

		if detail == "" {
			detail = "verification evidence was insufficient"
		} else {
			detail += "; verification evidence was insufficient"
		}
	}

	if strings.TrimSpace(detail) == "" {
		detail = "verification completed"
	}

	return state, detail
}

func (s *runnerExecutionState) noteToolFailure(req workspace.ToolRequest, result workspace.ToolResult) int {
	if s == nil {
		return 0
	}

	if strings.TrimSpace(result.Error) == "" {
		s.consecutiveToolFailures = 0
		s.lastFailedPathStem = ""

		return 0
	}

	stem := toolFailurePathStem(req)
	if stem != "" && stem == s.lastFailedPathStem {
		s.consecutiveToolFailures++
	} else {
		s.consecutiveToolFailures = 1
		s.lastFailedPathStem = stem
	}

	return s.consecutiveToolFailures
}

func (s *runnerExecutionState) noteMissingPathListFailure(exploreMode bool, req workspace.ToolRequest, result workspace.ToolResult) int {
	if s == nil {
		return 0
	}

	if exploreMode && isMissingPathListFailure(req, result) {
		s.consecutiveMissingPathListErrors++
		return s.consecutiveMissingPathListErrors
	}

	s.consecutiveMissingPathListErrors = 0

	return 0
}

func (s *runnerExecutionState) shouldConsumeSearchHitBeforeMoreSearch(req workspace.ToolRequest) bool {
	return false
}

func (s *runnerExecutionState) shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil || !s.pendingDiscoveryConsumption {
		return false
	}

	if s.pendingDiscoveryStrength != discoveryConsumptionSourceOwner {
		return false
	}

	if s.pendingDiscoveryFailedAttempt && strings.TrimSpace(s.pendingDiscoveryTool) != "context_pack" {
		return false
	}

	if !workerruntime.TaskFamilyIsConversation(brief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) ||
		runnerBriefRequiresMutation(brief) ||
		runnerVerificationRequired(brief) {

		return false
	}

	return conversationBroadDiscoveryRequest(req)
}

func (s *runnerExecutionState) discoveryConsumptionError() string {
	base := "a previous discovery result already returned concrete candidates. Before more broad discovery, inspect one returned handle or path with source-body evidence, answer from the evidence already gathered, or state the exact blocker/missing claim."
	if s == nil {
		return base
	}

	var parts []string

	if len(s.pendingDiscoveryCandidates) > 0 {
		candidateList := strings.Join(s.pendingDiscoveryCandidates, ", ")
		parts = append(parts, "Candidate(s): "+candidateList)
		// Add actionable instruction to read candidate files.
		parts = append(parts, "Read one of these candidate paths: "+candidateList)
	}

	if summary := strings.TrimSpace(s.pendingDiscoverySummary); summary != "" {
		parts = append(parts, "Last useful result: "+summary)
	}

	if output := strings.TrimSpace(s.pendingDiscoveryOutput); output != "" && len(s.pendingDiscoveryCandidates) == 0 {
		parts = append(parts, "Returned evidence: "+output)
	}

	if len(parts) == 0 {
		return base
	}

	if tool := strings.TrimSpace(s.pendingDiscoveryTool); tool != "" {
		return fmt.Sprintf("%s The last useful %s result was: %s", base, tool, strings.Join(parts, ". "))
	}

	return fmt.Sprintf("%s %s", base, strings.Join(parts, ". "))
}

func (s *runnerExecutionState) searchHitConsumptionError() string {
	if s == nil {
		return "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	}

	base := "you already have a successful search result with concrete hits. Inspect one returned file or symbol now before doing another search."
	detail := strings.TrimSpace(s.pendingSearchSummary)

	if output := strings.TrimSpace(s.pendingSearchOutput); output != "" {
		if detail != "" {
			detail += ". "
		}

		detail += "Returned hit(s): " + output
	}

	if s.pendingSearchTool != "" && detail != "" {
		return fmt.Sprintf("%s The last useful %s result was: %s", base, s.pendingSearchTool, detail)
	}

	if detail != "" {
		return fmt.Sprintf("%s The last useful result was: %s", base, detail)
	}

	return base
}

func (r *Runner) emitVerificationPhaseEvent(state string, detail string) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(state) == "" {
		return
	}

	data, err := json.Marshal(map[string]string{
		"lane":   "implement",
		"phase":  "verify",
		"state":  state,
		"detail": detail,
	})
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "phase", Content: string(data)})
}

func (r *Runner) attachInstructionsForTouchedPaths(turnMessages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if r == nil || r.InstructionAttacher == nil || turnMessages == nil || strings.TrimSpace(result.Error) != "" {
		return
	}

	for _, touched := range toolResultTouchedPaths(req, result) {
		if attachment := r.InstructionAttacher.OnFileTouch(touched); attachment != nil {
			content := attachment.Render()
			if content != "" {
				*turnMessages = append(*turnMessages, llama.ChatMessage{Role: "system", Content: content})
				r.emitEvent(RunEvent{Role: "instructions_attached", Content: content})
			}
		}
	}
}

func immediateBudgetFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, reason string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return "", false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return "", false
	}

	lowerReason := strings.ToLower(strings.TrimSpace(reason))
	if !ContainsAny(lowerReason,
		"budget",
		"maximum tool steps",
		"tools are now disabled",
		"run duration",
		"model-call budget",
		"operational model-call",
	) {

		return "", false
	}

	if !hasSuccessfulTaskResult(results) && !strings.HasPrefix(agentName, "child:") {
		return "", false
	}

	delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(brief, results)
	if delegatedOwnerReady {
		if output, ok := bestObjectiveAnswerableTaskResultOutput(brief.Objective, results); ok {
			return presentableFallbackCandidate(agentName, brief, results, annotateBudgetLimitedAnswer(output))
		}
	}

	if delegatedOwnerReady {
		if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
			return presentableFallbackCandidate(agentName, brief, results, annotateBudgetLimitedAnswer(answer))
		}
	}

	if implementationShapeOwnerContractApplies(brief) && !delegatedOwnerReady {
		return presentableFallbackCandidate(agentName, brief, results, synthesizeBriefConversationFallback(brief, results))
	}

	if usefulFallbackResultCount(results) < 2 {
		return "", false
	}

	if answer, ok := synthesizeFallbackAnswer(agentName, results); ok {
		return presentableFallbackCandidate(agentName, brief, results, answer)
	}

	return "", false
}

func budgetDeterministicBlocker(reason string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if runnerBriefRequiresMutation(brief) {
		return synthesizeMutationFallbackAnswer(brief, results)
	}

	budgetKind := "Budget"
	lowerReason := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lowerReason, "operational model-call"):
		budgetKind = "Operational model-call limit"
	case strings.Contains(lowerReason, "model-call budget"):
		budgetKind = "Model-call budget"
	case strings.Contains(lowerReason, "duration budget") || strings.Contains(lowerReason, "run duration"):
		budgetKind = "Run duration budget"
	case strings.Contains(lowerReason, "tool budget") || strings.Contains(lowerReason, "tool-step"):
		budgetKind = "Tool budget"
	}

	var b strings.Builder
	b.WriteString(budgetKind)
	b.WriteString(" was exhausted before a final answer could be synthesized from gathered evidence.")

	if len(results) == 0 {
		b.WriteString(" No tool evidence was recorded before exhaustion.")
		return b.String()
	}

	if paths := promptEvidencePathsFromToolResults(results, 6); len(paths) > 0 {
		b.WriteString("\n\nEvidence paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if latest := latestSuccessfulToolSummary(results); latest != "" {
		b.WriteString("\nLatest recorded evidence:\n- ")
		b.WriteString(latest)
	}

	return strings.TrimSpace(b.String())
}

func latestSuccessfulToolSummary(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			return summary
		}
	}

	return ""
}

func synthesizeNoToolsFallbackAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}
	if planThenExecuteAnchorRefreshFallbackPending(brief, results) {
		return "", false
	}

	if answer, ok := synthesizePlanThenExecutePlannerFallbackAnswer(brief, results); ok {
		return answer, true
	}

	if runnerBriefRequiresMutation(brief) {
		return synthesizeMutationFallbackAnswer(brief, results), true
	}

	if !runnerBriefRequiresMutation(brief) && !runnerVerificationRequired(brief) {
		closeout := readOnlyClosureDecisionForResults(brief, results)
		if readOnlyDirectAnswerLane(brief) {
			if answer, ok := closeout.ConcreteFallbackAnswer(); ok {
				return answer, true
			}
		}

		delegatedOwnerReady := delegatedImplementationShapeSynthesisAllowed(brief, results)
		if delegatedOwnerReady {
			if output, ok := bestObjectiveAnswerableTaskResultOutput(brief.Objective, results); ok {
				if normalized := normalizeDelegatedReadOnlyPacket(output); strings.TrimSpace(normalized) != "" {
					return normalized, true
				}
			}
		}

		if delegatedOwnerReady {
			if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
				return answer, true
			}
		}

		if implementationShapeOwnerContractApplies(brief) && !delegatedOwnerReady {
			return synthesizeBriefConversationFallback(brief, results), true
		}
	}

	return synthesizeFallbackAnswer(agentName, results)
}

func planThenExecuteAnchorRefreshFallbackPending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief, ok := planThenExecuteFallbackBrief(brief, results)
	if !ok {
		return false
	}
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return false
	}

	return planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief, results)
}

func synthesizePlanThenExecutePlannerFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	brief, ok := planThenExecuteFallbackBrief(brief, results)
	if !ok {
		return "", false
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		if state.SubmitCorrectionBlockerAllowed {
			if blocker := synthesizePlanThenExecuteSubmitCorrectionBlocker(brief, results); blocker != "" {
				return blocker, true
			}
		}
		return "", false
	}
	if planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief, results) {
		return "", false
	}

	if state.Mode == PlanExecutePlanningModeSubmitCorrection {
		if state.SubmitCorrectionBlockerAllowed {
			if blocker := synthesizePlanThenExecuteSubmitCorrectionBlocker(brief, results); blocker != "" {
				return blocker, true
			}

			return strings.TrimSpace("<<QUESTION>>: Plan-execute submit correction is still required; no valid plan artifact has been accepted.\n\n" +
				"Next valid action: emit one parser-readable submit_plan covering every required edit path with real executable edits, or answer <<QUESTION>> with the exact path-specific missing body/anchor blocker if gathered evidence cannot support a safe executable edit body."), true
		}

		return strings.TrimSpace("<<QUESTION>>: Plan-execute submit correction is still required; no valid plan artifact has been accepted.\n\n" +
			"Next valid action: emit one parser-readable submit_plan covering every required edit path with real executable edits or top-level source_satisfied_paths for already-final paths."), true
	}

	if pending := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pending) > 0 {
		return strings.TrimSpace("<<QUESTION>>: Plan-execute git history evidence is still required before submit_plan.\n\n" +
			"Remaining history paths:\n" + bulletList(pending)), true
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) > 0 {
		return strings.TrimSpace("<<QUESTION>>: Plan-execute source-body evidence is still required before submit_plan.\n\n" +
			"Remaining source paths:\n" + bulletList(mapKeys(unread))), true
	}

	return "", false
}

func planThenExecuteFallbackHasUnresolvedInvalidAnchorRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || len(submitPlanInvalidAnchorPaths(rejected)) == 0 {
		return false
	}

	return len(planThenExecuteFallbackUnresolvedLastRejectedRefreshPaths(brief, results)) > 0
}

func planThenExecuteFallbackUnresolvedLastRejectedRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return nil
	}

	refreshPaths := submitPlanRefreshPathCandidates(rejected)
	if len(refreshPaths) == 0 {
		return nil
	}
	wholeFileRewritePaths := submitPlanWholeFileExistingSourceRewritePaths(rejected)
	allowUnsafeRewriteBlocker := planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) &&
		len(planThenExecuteRejectedSubmitPlanStreak(results)) >= planThenExecuteRejectedSubmitPlanStreakThreshold

	var unresolved []string
	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, required := refreshPaths[path]; !required {
			continue
		}
		if _, wholeFileRewrite := wholeFileRewritePaths[path]; wholeFileRewrite && allowUnsafeRewriteBlocker {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}

		unresolved = append(unresolved, path)
	}

	return uniqueSortedRunnerPaths(unresolved)
}

func synthesizePlanThenExecuteSubmitCorrectionBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || !planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) {
		return ""
	}

	paths := submitPlanRefreshPathCandidates(rejected)
	if len(paths) == 0 {
		paths = planThenExecuteRejectedSubmitPlanStreakInvalidPaths(results)
	}

	rejectedPathList := sortedRunnerPathKeys(paths)
	if len(rejectedPathList) == 0 {
		return ""
	}
	rejectedPathSet := make(map[string]struct{}, len(rejectedPathList))
	for _, path := range rejectedPathList {
		rejectedPathSet[path] = struct{}{}
	}

	requiredCoverage := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	missing := missingRequiredEditPaths(requiredCoverage, newToolResultEvidenceProjection(results).ChangedPathSet())
	if len(missing) == 0 {
		missing = rejectedPathList
	}
	missingSet := make(map[string]struct{}, len(missing)+len(rejectedPathList))
	for _, path := range missing {
		missingSet[path] = struct{}{}
	}
	for _, path := range rejectedPathList {
		if _, ok := missingSet[path]; ok {
			continue
		}
		missing = append(missing, path)
		missingSet[path] = struct{}{}
	}

	reason := strings.TrimSpace(rejected.Error)
	if reason == "" {
		reason = strings.TrimSpace(rejected.Summary)
	}

	var b strings.Builder
	b.WriteString("<<QUESTION>>: Plan-execute submit correction is blocked because repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for every remaining required path.\n\n")
	b.WriteString("Path-level blockers:\n")
	for _, path := range missing {
		b.WriteString("- ")
		b.WriteString(path)
		if _, ok := rejectedPathSet[path]; ok {
			b.WriteString(": repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for this path.\n")
		} else {
			b.WriteString(": no accepted submit_plan artifact has covered this required path; a safe executable edit body or exact path-specific body/anchor blocker is still required.\n")
		}
	}
	b.WriteString("\nLast rejected submit_plan: ")
	b.WriteString(reason)

	return strings.TrimSpace(b.String())
}

func planThenExecuteFallbackBrief(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (workerruntime.WorkerRuntimeBrief, bool) {
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return brief, true
	}

	if !planThenExecuteFallbackEvidenceSeen(brief, results) {
		return brief, false
	}

	copy := brief

	copy.TaskFamily = PlanThenExecutePlannerTaskFamily

	return copy, true
}

func planThenExecuteFallbackEvidenceSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	switch strings.ToLower(strings.TrimSpace(brief.Contract)) {
	case "plan_then_execute", "plan-execute":
		return true
	}

	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "submit_plan" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error}, "\n"))
		if strings.Contains(text, "source_satisfied_paths") ||
			strings.Contains(text, "required edit path") ||
			strings.Contains(text, "bounded source-body refresh") ||
			strings.Contains(text, "plan-execute") {

			return true
		}
	}

	return false
}

func bulletList(values []string) string {
	var b strings.Builder

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		b.WriteString("- ")
		b.WriteString(value)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func synthesizeMutationFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	changedPaths := mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())
	missingRequiredPaths := missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())
	rejectedMutationPaths := unresolvedRejectedMutationPaths(results, brief.RequiredEditPaths)

	var lastError *workspace.ToolResult

	latestProgressIndex := -1

	for i := range results {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) || workspace.AnalyzeVerificationResult(result).Successful {
			latestProgressIndex = i
		}
	}

	if failed, ok := failedVerificationAfterLatestWrite(results, brief); ok {
		copy := failed
		lastError = &copy
	} else {
		for i := range results {
			if i <= latestProgressIndex {
				continue
			}

			result := normalizeFallbackResult(results[i])
			if strings.TrimSpace(result.Error) == "" {
				continue
			}

			if isHarnessInternalRejectionResult(result) {
				continue
			}

			copy := result

			lastError = &copy
		}
	}

	var b strings.Builder

	if len(missingRequiredPaths) > 0 || len(rejectedMutationPaths) > 0 {
		b.WriteString("<<QUESTION>>: Implementation incomplete; required mutation path(s) remain unresolved.\n\n")
	} else if len(changedPaths) > 0 {
		b.WriteString(mutationFallbackStatusLead(results, brief))
		b.WriteString("\n\n")
	}

	if len(changedPaths) > 0 {
		b.WriteString("Changed paths:\n")

		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	} else {
		b.WriteString("Changed paths: none\n\n")
	}

	if len(missingRequiredPaths) > 0 {
		b.WriteString("Required edit paths not changed:\n")

		for _, path := range missingRequiredPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if len(rejectedMutationPaths) > 0 {
		b.WriteString("Rejected mutation paths still unresolved:\n")

		for _, path := range rejectedMutationPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if latestSourceVerification, ok := latestVerificationAfterLatestSourceWrite(results); sourceEditsNeedSemanticVerification(brief, results) && ok && workspace.AnalyzeVerificationResult(latestSourceVerification).PatchHygieneOnly {
		b.WriteString("Verification: ")

		if summary, ok := latestSuccessfulVerificationSummary(results); ok {
			b.WriteString(summary)
			b.WriteString(" (patch hygiene only); ")
		}

		if plannerOwnsVerification(brief) {
			b.WriteString("source-aware lane sanity still pending; planner-owned build/test verification not run by lane.\n")
		} else {
			b.WriteString("source-aware verification pending for source edits.\n")
		}
	} else if summary, ok := latestSuccessfulVerificationSummary(results); ok {
		b.WriteString("Verification: ")
		b.WriteString(summary)
		b.WriteString("\n")
	} else if hasPendingVerificationAfterWrites(results, brief) {
		if plannerOwnsVerification(brief) {
			b.WriteString("Verification: pending lane sanity; build/test verification is planner-owned.\n")
		} else {
			b.WriteString("Verification: blocked; workspace writes are still pending verification.\n")
		}
	} else {
		b.WriteString("Verification: not run or not detected.\n")
	}

	if lastError != nil {
		b.WriteString("\nBlocker:\n- ")
		b.WriteString(lastError.Summary)

		if errText := strings.TrimSpace(lastError.Error); errText != "" {
			b.WriteString(": ")
			b.WriteString(errText)
		}

		b.WriteString("\n")
	} else if len(changedPaths) == 0 {
		b.WriteString("\nBlocker:\n- The implementation lane ended before a successful workspace edit was recorded.\n")
	}

	return strings.TrimSpace(b.String())
}

func mutationFallbackStatusLead(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	if hasPendingVerificationAfterWrites(results, brief) {
		return "Workspace changes were recorded, but post-write verification is still pending."
	}

	if _, ok := latestSuccessfulVerificationSummary(results); ok {
		return "Workspace changes were recorded and verification evidence was found."
	}

	return "Workspace changes were recorded."
}

func (r *Runner) modelErrorAfterPartialEditBlockerAnswer(modelErr error, results []workspace.ToolResult) (string, bool) {
	if r == nil || modelErr == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {
		return "", false
	}

	detail := strings.TrimSpace(modelErr.Error())
	if detail == "" {
		detail = "model transport failed"
	}

	fallback := strings.TrimSpace(synthesizeMutationFallbackAnswer(r.RuntimeBrief, results))
	if strings.HasPrefix(fallback, "<<QUESTION>>:") {
		if _, remainder, ok := strings.Cut(fallback, "\n\n"); ok {
			fallback = strings.TrimSpace(remainder)
		} else {
			fallback = ""
		}
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Local model endpoint failed after workspace edits before implementation closeout: ")
	b.WriteString(detail)
	b.WriteString("\n\n")

	if fallback != "" {
		b.WriteString(fallback)
	}

	return strings.TrimSpace(b.String()), true
}

func latestSuccessfulVerificationSummary(results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !workspace.AnalyzeVerificationResult(result).Successful {
			continue
		}

		if command := workspace.ShellCommandFromToolResult(result); command != "" {
			return command + " passed", true
		}

		if summary := strings.TrimSpace(result.Summary); summary != "" {
			if strings.Contains(strings.ToLower(summary), "passed") {
				return summary, true
			}

			return summary + " passed", true
		}

		return "verification passed", true
	}

	return "", false
}

func synthesizeFallbackAnswer(agentName string, results []workspace.ToolResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}

	var (
		firstSuccessful     *workspace.ToolResult
		lastError           *workspace.ToolResult
		successfulNarrowing []workspace.ToolResult
	)

	for i := range results {
		result := normalizeFallbackResult(results[i])
		if result.Error == "" && strings.TrimSpace(result.Output) != "" && firstSuccessful == nil {
			copy := result

			firstSuccessful = &copy
		}

		if result.Error == "" && strings.TrimSpace(result.Output) != "" && isResultUsefulForFallback(result) {
			successfulNarrowing = append(successfulNarrowing, result)
		}

		if result.Error != "" && !isHarnessInternalRejectionResult(result) {
			copy := result

			lastError = &copy
		}
	}

	// OpenCode-style fallback framing (see docs/opencode-fixes-design.md,
	// `default.txt`): the model failed to synthesize a direct reply, but the
	// evidence collected during the turn is still a legitimate partial
	// answer. Lead with the findings as findings -- not with an apology.
	// Prior wording ("did not complete cleanly, but here is the evidence
	// gathered") read as a system error to users; this reframes the same
	// content as an honest "partial findings" report.
	var b strings.Builder

	if agentName == "explore" {
		b.WriteString("Partial findings from this turn (explore lane reached its budget before synthesizing):\n\n")
	} else {
		b.WriteString("Partial findings from this turn (the budget was reached before a full synthesis):\n\n")
	}

	if len(successfulNarrowing) > 0 || firstSuccessful != nil {
		b.WriteString("Verified context:\n")

		if len(successfulNarrowing) == 0 && firstSuccessful != nil {
			successfulNarrowing = append(successfulNarrowing, *firstSuccessful)
		}

		for _, result := range limitFallbackResults(successfulNarrowing, 3) {
			b.WriteString("- ")
			b.WriteString(result.Summary)

			if output := compactOutput(sanitizeFallbackResultOutput(result), fallbackLineBudgetForTool(result.Tool)); output != "" {
				b.WriteString("\n")
				b.WriteString(output)
			}

			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if lastError != nil {
		b.WriteString("Final blocker:\n")
		b.WriteString("- ")
		b.WriteString(lastError.Summary)

		if errText := strings.TrimSpace(lastError.Error); errText != "" {
			b.WriteString(": ")
			b.WriteString(errText)
		}

		b.WriteString("\n\n")
	}

	// Before giving up, sift the verified context for any concrete
	// observation the agent can offer instead of a generic apology. Files
	// above a size threshold are surfaced as plausible refactor candidates
	// when the user's original prompt looked like a review / cleanup ask.
	if observations := inferFallbackObservations(successfulNarrowing); len(observations) > 0 {
		b.WriteString("Best-effort observations from what was read:\n")

		for _, obs := range observations {
			b.WriteString("- ")
			b.WriteString(obs)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString("Recommended next step:\n")

	if agentName == "explore" {
		b.WriteString("- answer from the verified evidence above; if resuming later, start from one exact missing claim and one exact probe rather than reopening broad discovery")
	} else {
		b.WriteString("- answer from the verified evidence above; if resuming later, name one exact missing claim and one exact probe before any additional reads")
	}

	return strings.TrimSpace(b.String()), true
}

func annotateBudgetLimitedAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}

	return answer
}

// inferFallbackObservations turns a handful of read_file / outline results
// into concrete talking points so the forced-completion fallback can offer
// something useful instead of a bare "I failed" message. Observations are
// bounded in count and content so they do not balloon the reply.
func inferFallbackObservations(results []workspace.ToolResult) []string {
	if len(results) == 0 {
		return nil
	}

	observations := make([]string, 0, 3)
	seen := make(map[string]struct{})

	for _, result := range results {
		if result.Tool != "read_file" {
			continue
		}

		path := extractReadFilePath(result.Summary)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}

		lines := strings.Count(result.Output, "\n")

		if lines >= 600 {
			observations = append(observations, fmt.Sprintf("%s is unusually large (%d lines) -- a plausible refactor candidate on its own, even without deeper evidence.", path, lines))
		} else if lines >= 250 {
			observations = append(observations, fmt.Sprintf("%s (%d lines) was read; scan for functions exceeding one screen or mixed responsibilities as a small follow-up target.", path, lines))
		}

		if len(observations) >= 3 {
			break
		}
	}

	return observations
}

// extractReadFilePath pulls the path out of a read_file result's summary
// (the canonical format is "Read <display> lines N-M"). Returns empty on
// any parse failure so the caller just skips the observation.
func extractReadFilePath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if !strings.HasPrefix(trimmed, "Read ") {
		return ""
	}

	rest := strings.TrimPrefix(trimmed, "Read ")
	idx := strings.Index(rest, " lines ")

	if idx <= 0 {
		return ""
	}

	return strings.TrimSpace(rest[:idx])
}

// normalizeFallbackResult prepares a tool result for fallback synthesis by
// removing nested fallback wrappers from a delegated child's Output. When a
// child lane force-completes, its answer is already a "Partial findings from
// this turn..." template; that wrapper bleeds into the parent's view of the
// task result and produces nested "Partial findings" headers in the final
// answer. We strip the outer template so the substance (Verified context
// bullets, observations) folds cleanly into the parent's own template.
func normalizeFallbackResult(result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Tool) != "task" {
		return result
	}

	stripped := stripNestedFallbackTemplate(result.Output)
	if stripped != result.Output {
		result.Output = stripped
	}

	return result
}

// stripNestedFallbackTemplate removes the outer scaffolding of a
// synthesizeFallbackAnswer output so it can be embedded inside another
// fallback answer without producing duplicated headers. Idempotent: if the
// input is not a fallback template, it is returned unchanged.

// firstBulletText returns the text of the first bullet ("- ") in s, with the
// leading dash trimmed. Returns the empty string if no bullet is found.

// isHarnessRejectionSummary returns true for tool-result Summary strings that
// originate from harness contracts (post-readiness gap requirement, packet
// anchoring, etc.) rather than from real tool failures. These are not useful
// "Final blocker" lines -- the user did nothing wrong and the message is
// internal to the harness's own enforcement loop.

// isHarnessRejectionErrorText returns true when the error text reads like a
// harness contract message rather than a real tool error. Used as a fallback
// when the Summary itself isn't enough to classify the rejection.

func isResultUsefulForFallback(result workspace.ToolResult) bool {
	lowerSummary := strings.ToLower(strings.TrimSpace(result.Summary))
	lowerOutput := strings.ToLower(strings.TrimSpace(result.Output))

	if strings.HasPrefix(lowerSummary, "redirected to ") || ContainsAny(lowerOutput,
		"try: {\"tool\":",
		"retry your original request and it will execute",
	) {

		return false
	}

	switch strings.TrimSpace(result.Tool) {
	case "task", "discovery_query", "read_file", "search_content", "search_files":
		return true
	default:
		return workspace.LanguageToolHasAnyCapability(result.Tool, semanticlanguage.CapReadSymbol, semanticlanguage.CapFileOutline)
	}
}

func usefulFallbackResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || !isResultUsefulForFallback(result) {
			continue
		}

		count++
	}

	return count
}

func limitFallbackResults(results []workspace.ToolResult, max int) []workspace.ToolResult {
	if max <= 0 || len(results) <= max {
		return results
	}

	return results[:max]
}

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
		path = NormalizeRunnerPath(path)
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

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
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

		return len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0
	},
	workerruntime.RequiredEvidenceChangedPaths: func(answer string, results []workspace.ToolResult) bool {
		return len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0
	},
	workerruntime.RequiredEvidenceChangedPathsOrNoopReason: func(answer string, results []workspace.ToolResult) bool {
		if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
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
			if workspace.AnalyzeVerificationResult(r).Attempted {
				return true
			}
		}

		return false
	},
	workerruntime.RequiredEvidenceVerificationCommand: func(answer string, results []workspace.ToolResult) bool {
		for _, r := range results {
			if workspace.AnalyzeVerificationResult(r).Attempted {
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
		if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
			requiredPaths := changedPathsRequiringGitHistory(results)
			if len(requiredPaths) == 0 {
				return true
			}
			for _, path := range requiredPaths {
				if !successfulGitHistoryResultForPath(path, results) {
					return false
				}
			}

			return true
		}

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

func changedPathsRequiringGitHistory(results []workspace.ToolResult) []string {
	var paths []string

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		if strings.TrimSpace(result.Tool) == workspace.ToolNameMkdir {
			continue
		}

		for _, path := range newToolResultEvidenceProjection([]workspace.ToolResult{result}).ChangedPathList() {
			normalized := NormalizeRunnerPath(path)
			if normalized == "" || writePathIsNonSource(normalized) {
				continue
			}

			if toolResultPreimagePathWasMissing(result, normalized) {
				continue
			}

			paths = append(paths, normalized)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func toolResultPreimagePathWasMissing(result workspace.ToolResult, path string) bool {
	if strings.TrimSpace(result.PayloadType) != writePreimagePayloadType || len(result.Payload) == 0 {
		return false
	}

	var payload writePreimagePayload

	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return false
	}

	normalized := NormalizeRunnerPath(path)

	for _, entry := range payload.Paths {
		if NormalizeRunnerPath(entry.Path) == normalized {
			return !entry.Exists
		}
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

type runnerFinalAnswerRetryInput struct {
	stepNumber               int
	activeToolCall           *llama.ToolCall
	assistantMessage         llama.ChatMessage
	acceptance               finalAnswerAcceptance
	result                   workspace.ToolResult
	collectedResults         []workspace.ToolResult
	currentAllowedTools      []string
	completionGateRejections *int
	turnMessages             *[]llama.ChatMessage
	runState                 *runnerRunState
	controller               *runnerTurnController
	autonomyRecorder         *runnerAutonomyRecorder
	rebuildTools             func()
}

func (r *Runner) retryRejectedFinalAnswerForRun(input runnerFinalAnswerRetryInput) bool {
	rejections := 0
	if input.completionGateRejections != nil {
		rejections = *input.completionGateRejections
	}

	state := input.runState
	if state == nil {
		state = &runnerRunState{}
	}
	state.collectedResults = input.collectedResults
	state.currentAllowedTools = input.currentAllowedTools
	state.completionGateRejections = rejections
	state.autonomyRecorder = input.autonomyRecorder
	if input.controller != nil {
		state.controller = *input.controller
	}

	retry, prompt := r.retryRejectedFinalAnswer(state, input.stepNumber, input.activeToolCall, input.assistantMessage, input.acceptance, input.result)
	if !retry {
		return false
	}
	if input.completionGateRejections != nil {
		*input.completionGateRejections = state.completionGateRejections
	}
	if input.turnMessages != nil && prompt != "" {
		*input.turnMessages = append(*input.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	}

	// When source-body read tools are available for a source-grounding
	// rejection, hide non-read tools so the next action is a bounded source
	// read instead of another prose answer.
	if r.sourceGroundingReadRecoveryAvailable(input.result, input.currentAllowedTools) {
		state.hideNonSourceBodyReadTools(OwnerSourceGrounding, input.currentAllowedTools)
	}
	if input.rebuildTools != nil {
		input.rebuildTools()
	}

	return true
}

func readOnlyInvestigationStrategyPrompt(allowedTools []string) string {
	broadOrientation := "Start with an allowed context or search tool that matches the missing evidence; one well-scoped call is usually better than enumerating symbols one by one."
	if toolAllowed(allowedTools, workspace.ToolNameContextPack) && toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		broadOrientation = "Start with context_pack or context_query for a ranked map of relevant files and symbols. One call replaces 20+ individual symbol lookups."
	} else if toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		broadOrientation = "Start with context_query for graph-shaped or symbol-relationship questions, or use search_content for exact text. One well-scoped call is usually better than enumerating symbols one by one."
	} else if toolAllowed(allowedTools, workspace.ToolNameSearchContent) {
		broadOrientation = "Start with search_content or search_files for stable names, labels, symbols, or error text, then read only the highest-confidence result."
	}

	bundledProbe := "Use the broadest visible read-only tool that matches the named gap, then follow with targeted reads only where needed."
	if toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery) {
		bundledProbe = "Use discovery_query with purpose=\"architecture_trace\" or \"impact_analysis\" to orchestrate multiple probes at once. One call replaces 10+ separate searches."
	}

	switchText := "If you are reading symbols one by one, pause and use a broader visible context or search tool instead."
	switch {
	case toolAllowed(allowedTools, workspace.ToolNameContextQuery) && toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery):
		switchText = "If you are reading symbols one by one, pause and use context_query or discovery_query instead."
	case toolAllowed(allowedTools, workspace.ToolNameContextQuery):
		switchText = "If you are reading symbols one by one, pause and use context_query, code_navigate, or search_content for the named gap instead."
	case toolAllowed(allowedTools, workspace.ToolNameSearchContent):
		switchText = "If you are reading symbols one by one, pause and use search_content or search_files for a stable term before the next targeted read."
	}

	return "## Investigation Strategy\n\n" +
		"For read-only investigation questions, an efficient approach:\n\n" +
		"1. Broad orientation (1 call): " + broadOrientation + "\n" +
		"2. Bundled probe (1 call): " + bundledProbe + "\n" +
		"3. Targeted reads (2-3 calls): Read only the 1-3 key files or symbols identified above. Do not enumerate every symbol in a file.\n" +
		"4. Answer: Once the evidence answers the question, stop. You do not need to exhaustively verify every related function.\n\n" +
		switchText
}

func narrowToolFamilyRepetitionNudgePrompt(narrowFamily string, consecutiveCalls int, allowedTools []string) string {
	options := readOnlyStrategyShiftOptions(allowedTools)
	if options == "" {
		options = "switch to a different visible read-only tool that directly matches the remaining evidence gap"
	}

	return fmt.Sprintf(
		"You have called %s %d times consecutively. If you still lack the evidence to answer, switch strategy: %s. Repeated narrow symbol lookups rarely uncover the high-level answer faster than one broader visible probe.",
		narrowFamily, consecutiveCalls, options,
	)
}

func readOnlyStrategyShiftOptions(allowedTools []string) string {
	var options []string
	if toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		options = append(options, "use `context_query` for a graph-shaped question")
	}
	if toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery) {
		options = append(options, "use `discovery_query` for a bundled layered probe")
	}
	if toolAllowed(allowedTools, workspace.ToolNameHarnessAudit) {
		options = append(options, "use `harness_audit` for harness self-inspection")
	}
	if toolAllowed(allowedTools, workspace.ToolNameContextPack) {
		options = append(options, "use `context_pack` for cached workspace localization")
	}
	if toolAllowed(allowedTools, workspace.ToolNameSearchContent) {
		options = append(options, "use `search_content` for a stable term or error text")
	}

	return joinStrategyOptions(options, "or")
}

func toolAllowed(allowedTools []string, tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	for _, allowed := range allowedTools {
		if strings.TrimSpace(allowed) == tool {
			return true
		}
	}

	return false
}

func joinStrategyOptions(options []string, conjunction string) string {
	switch len(options) {
	case 0:
		return ""
	case 1:
		return options[0]
	case 2:
		return options[0] + " " + conjunction + " " + options[1]
	default:
		return strings.Join(options[:len(options)-1], ", ") + ", " + conjunction + " " + options[len(options)-1]
	}
}

type runnerLanePreflightInput struct {
	Request               workspace.ToolRequest
	Raw                   string
	Results               []workspace.ToolResult
	VerificationAttempted bool
}

type runnerLanePreflightDecision struct {
	OriginalRequest  workspace.ToolRequest
	OriginalRaw      string
	Request          workspace.ToolRequest
	Raw              string
	RejectSummary    string
	RejectError      string
	RejectStopReason string
	ForceAnswer      *runnerForcedAnswer
}

type runnerForcedAnswer struct {
	Reason string
	Prompt string
}

type delegatedDiscoveryPlan struct {
	objective       string
	namedScopes     []string
	hasNamedScope   bool
	hasPrimaryScope bool
	primaryScope    string
	detailedScopes  []string
	defaultPurpose  string
	searchPattern   string
	symbolPattern   string
	questionText    string
	promptKeyTerms  []string
}

func newDelegatedDiscoveryPlan(objective string) delegatedDiscoveryPlan {
	objective = strings.TrimSpace(objective)

	plan := delegatedDiscoveryPlan{objective: objective}

	plan.namedScopes = plan.parseNamedScopes()
	plan.hasNamedScope = len(plan.namedScopes) > 0
	plan.hasPrimaryScope = plan.computeHasPrimaryNamedScope()
	plan.primaryScope = plan.computePrimaryScope()
	plan.detailedScopes = plan.computeDetailedNamedScopes()
	plan.questionText = plan.computeQuestionText()
	plan.defaultPurpose = plan.computePurpose()
	plan.searchPattern = plan.computeSearchPattern()
	plan.symbolPattern = plan.computeSymbolPattern()
	plan.promptKeyTerms = plan.computePromptKeyTerms()

	return plan
}

func (p delegatedDiscoveryPlan) repairDiscoveryQuery(req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	repaired := req
	changed := false

	if purpose := p.normalizePurpose(repaired.Purpose); purpose != strings.TrimSpace(repaired.Purpose) {
		repaired.Purpose = purpose
		changed = true
	}

	if p.shouldReplacePath(repaired.Path) {
		repaired.Path = p.primaryScope
		changed = true
	}

	if strings.TrimSpace(repaired.Pattern) == "" {
		repaired.Pattern = p.searchPattern
		changed = true
	}

	if repaired.MaxDepth <= 0 {
		repaired.MaxDepth = 2
		changed = true
	}

	return repaired, changed
}

func (p delegatedDiscoveryPlan) candidateConsumptionRequest(results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, bool) {
	if !slices.Contains(allowedTools, "read_file") || len(newToolResultEvidenceProjection(results).ReadOnlySourceBodyEvidencePaths()) > 0 {
		return workspace.ToolRequest{}, false
	}

	paths := p.consumableSourceBodyCandidatePaths(results, 6)
	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" || newToolResultEvidenceProjection(results).HasSourceBody(path) {
			continue
		}

		return workspace.ToolRequest{
			Tool:            "read_file",
			Path:            path,
			StartLine:       1,
			EndLine:         160,
			NamedGap:        "source-body evidence for delegated read-only synthesis",
			WhyNotAnswering: "the prior discovery query returned candidates but not source-body evidence",
		}, true
	}

	return workspace.ToolRequest{}, false
}

type delegatedDiscoveryConsumableCandidate struct {
	path       string
	score      int
	firstIndex int
}

func (p delegatedDiscoveryPlan) consumableSourceBodyCandidatePaths(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := p.consumableCandidateTerms()
	best := map[string]delegatedDiscoveryConsumableCandidate{}
	seenIndex := 0

	for _, summary := range delegatedDiscoverySummaries(results) {
		for _, evidence := range summary.Evidence {
			layerName := strings.TrimSpace(evidence.Name)
			summaryText := strings.TrimSpace(evidence.Summary)
			text := normalizeWorkspacePathScanText(strings.TrimSpace(evidence.Summary + "\n" + evidence.OutputExcerpt))

			for _, rawLine := range strings.Split(text, "\n") {
				line := strings.TrimSpace(rawLine)
				if line == "" {
					continue
				}

				for _, path := range delegatedDiscoveryLineCandidatePaths(line) {
					path = normalizeDelegatedDiscoveryEvidencePath(path)
					if !delegatedDiscoveryConsumableSourcePath(path) {
						continue
					}

					score := p.consumableCandidateScore(path, layerName, line, summaryText, terms)
					if score <= 0 {
						continue
					}

					candidate := delegatedDiscoveryConsumableCandidate{path: path, score: score, firstIndex: seenIndex}
					seenIndex++

					if prior, ok := best[path]; ok && (prior.score > score || (prior.score == score && prior.firstIndex <= candidate.firstIndex)) {
						continue
					}

					best[path] = candidate
				}
			}
		}
	}

	candidates := make([]delegatedDiscoveryConsumableCandidate, 0, len(best))
	for _, candidate := range best {
		candidates = append(candidates, candidate)
	}

	if len(candidates) == 0 {
		return nil
	}

	sortDelegatedDiscoveryConsumableCandidates(candidates)

	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}

	return closurePromptEvidencePaths(paths, max)
}

func delegatedDiscoveryLineCandidatePaths(line string) []string {
	var paths []string

	for _, path := range pathparse.MentionedWorkspacePaths(line) {
		paths = textutil.AppendUnique(paths, path)
	}

	for _, match := range delegatedPacketPathLinePattern.FindAllString(line, -1) {
		paths = textutil.AppendUnique(paths, match)
	}

	return paths
}

func delegatedDiscoveryConsumableSourcePath(path string) bool {
	path = normalizeDelegatedDiscoveryEvidencePath(path)
	if path == "" || strings.HasPrefix(path, ".idiom/") || strings.HasPrefix(path, ".") {
		return false
	}

	if !delegatedDiscoveryLooksLikeEvidencePath(path) {
		return false
	}

	if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go") {
		return false
	}

	return true
}

func (p delegatedDiscoveryPlan) consumableCandidateTerms() []string {
	var terms []string

	for _, term := range RecoveryObjectiveSearchWords(p.questionText, 16) {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	for _, term := range p.promptKeyTerms {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	for _, term := range delegatedReadOnlyMechanismTerms(p.objective) {
		terms = appendDelegatedDiscoveryCandidateTerm(terms, term)
	}

	return terms
}

func appendDelegatedDiscoveryCandidateTerm(terms []string, raw string) []string {
	term := strings.ToLower(strings.TrimSpace(raw))

	term = strings.Trim(term, "`'\"()[]{}<>,.:;!?")

	if term == "" || len(term) < 4 || pathparse.LooksLikeWorkspacePathToken(term) {
		return terms
	}

	if delegatedDiscoveryStopWord(term, delegatedDiscoveryStopWordSearchTerm) ||
		delegatedDiscoveryStopWord(term, delegatedDiscoveryStopWordPromptKeyTerm) {

		return terms
	}

	return textutil.AppendUnique(terms, term)
}

func (p delegatedDiscoveryPlan) consumableCandidateScore(path string, layerName string, line string, summary string, terms []string) int {
	context := strings.TrimSpace(summary + "\n" + line)
	score := delegatedDiscoveryConsumableLayerScore(layerName)

	score += delegatedDiscoveryConsumableLineScore(line)
	score += delegatedDiscoveryConsumablePathShapeScore(path)
	score += delegatedDiscoveryConsumableObjectiveScore(path, context, terms)
	score += delegatedDiscoveryConsumableMechanismScore(path, layerName, context)
	score += delegatedDiscoveryConsumableInventoryScore(layerName, context)

	if p.pathOutsideDetailedScope(path) {
		score -= 80
	}

	return score
}

func delegatedDiscoveryConsumableLayerScore(name string) int {
	name = strings.TrimSpace(name)
	switch {
	case name == "text_anchor_search":
		return 80
	case workspace.LanguageToolHasAnyCapability(name, semanticlanguage.CapReadSymbol):
		return 75
	case workspace.LanguageToolHasAnyCapability(name, semanticlanguage.CapReferences):
		return 65
	case workspace.LanguageToolHasAnyCapability(name, semanticlanguage.CapDefinition):
		return 60
	case workspace.LanguageToolHasAnyCapability(name, semanticlanguage.CapWorkspaceSymbol):
		return 45
	}

	switch name {
	case "related_files":
		return 10
	case "dependency_graph", "ctags_symbols":
		return 5
	case "target_listing":
		return -45
	case "project_behavior_digest", "project_context_digest":
		return -60
	default:
		return 5
	}
}

func delegatedDiscoveryConsumableLineScore(line string) int {
	line = strings.TrimSpace(line)

	lower := strings.ToLower(line)

	switch {
	case line == "":
		return -20
	case closurePathScopedSymbolPattern.MatchString(line), closurePathCallPattern.MatchString(line):
		return 45
	case strings.Contains(lower, "implementation anchor"), strings.Contains(lower, "implementation path"), strings.Contains(lower, "source anchor"):
		return 40
	case strings.Contains(lower, "func "):
		return 35
	case strings.Contains(lower, "type "):
		return 20
	case strings.Contains(lower, "package "):
		return -25
	case delegatedDiscoveryLineIsBarePath(line):
		return -20
	default:
		return 0
	}
}

func delegatedDiscoveryConsumablePathShapeScore(path string) int {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(path, "internal/"):
		return 25
	case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
		return 20
	case strings.HasPrefix(path, "internal/"):
		return 10
	case strings.HasSuffix(path, "_test.go"):
		return -30
	default:
		return 0
	}
}

func delegatedDiscoveryConsumableObjectiveScore(path string, line string, terms []string) int {
	haystack := strings.ToLower(filepath.ToSlash(path) + "\n" + line)
	score := 0

	for _, term := range terms {
		if term == "" {
			continue
		}

		if strings.Contains(haystack, term) {
			score += 22
		}
	}

	return score
}

func delegatedDiscoveryConsumableMechanismScore(path string, layerName string, line string) int {
	if delegatedDiscoveryConsumableInventoryLayer(layerName) {
		return 0
	}

	haystack := strings.ToLower(filepath.ToSlash(path) + "\n" + line)
	score := 0

	if ContainsAny(haystack,
		"compose", "composer", "composition", "synthesis", "synthesize", "final response", "final answer",
		"closeout", "readiness", "completion", "acceptance", "grounded evidence", "mechanism", "contract",
	) {

		score += 55
	}

	if ContainsAny(haystack, "implementation path", "implementation anchor", "source anchor", "source-body", "source body") {
		score += 25
	}

	if ContainsAny(haystack, "capability:", "language:", "symbol index:", "related files:", "found related") {
		score -= 35
	}

	return score
}

func delegatedDiscoveryConsumableInventoryScore(layerName string, line string) int {
	lowerLayer := strings.ToLower(strings.TrimSpace(layerName))
	lower := strings.ToLower(strings.TrimSpace(line))
	score := 0

	if delegatedDiscoveryConsumableInventoryLayer(lowerLayer) {
		score -= 20
	}

	if ContainsAny(lower,
		"no go workspace symbols matched",
		"no workspace symbols matched",
		"no symbols matched",
		"no related go files matched",
		"found related go file",
		"found related go files",
		"capability:",
		"language:",
		"symbol index:",
		"related files:",
	) {

		score -= 45
	}

	return score
}

func delegatedDiscoveryConsumableInventoryLayer(layerName string) bool {
	lowerLayer := strings.ToLower(strings.TrimSpace(layerName))

	return lowerLayer == "target_listing" ||
		lowerLayer == "related_files" ||
		lowerLayer == "dependency_graph" ||
		lowerLayer == "ctags_symbols" ||
		strings.Contains(lowerLayer, "workspace_symbol")
}

func sortDelegatedDiscoveryConsumableCandidates(candidates []delegatedDiscoveryConsumableCandidate) {
	for i := 1; i < len(candidates); i++ {
		current := candidates[i]
		j := i - 1

		for ; j >= 0 && delegatedDiscoveryConsumableLess(current, candidates[j]); j-- {
			candidates[j+1] = candidates[j]
		}

		candidates[j+1] = current
	}
}

func delegatedDiscoveryConsumableLess(left, right delegatedDiscoveryConsumableCandidate) bool {
	if left.score != right.score {
		return left.score > right.score
	}

	leftPriority := delegatedDiscoveryPathPriority(left.path)
	rightPriority := delegatedDiscoveryPathPriority(right.path)

	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}

	if left.firstIndex != right.firstIndex {
		return left.firstIndex < right.firstIndex
	}

	return left.path < right.path
}

func (p delegatedDiscoveryPlan) normalizePurpose(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if delegatedDiscoveryPurposeValid(purpose) && p.purposeSupportedByObjective(purpose) {
		return purpose
	}

	return p.defaultPurpose
}

func (p delegatedDiscoveryPlan) purposeSupportedByObjective(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case "security_review":
		lower := strings.ToLower(p.objective)
		return ContainsAny(lower, "security", "unsafe", "vulnerability", "injection", "secret", "token")
	default:
		return true
	}
}

func (p delegatedDiscoveryPlan) shouldReplacePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}

	if path == "." && p.primaryScope != "" && p.primaryScope != "." && p.hasNamedScope {
		return true
	}

	if delegatedReadOnlyBroadNamedScope(path) && p.primaryScope != "" && p.primaryScope != "." && NormalizeRunnerPath(path) != NormalizeRunnerPath(p.primaryScope) {
		return true
	}

	return p.pathOutsideDetailedScope(path) && p.primaryScope != "" && p.primaryScope != "."
}

func (p delegatedDiscoveryPlan) pathOutsideDetailedScope(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || path == "." || len(p.detailedScopes) == 0 {
		return false
	}

	for _, scope := range p.detailedScopes {
		if postChildPathsOverlap(path, scope) {
			return false
		}
	}

	return true
}

func (p delegatedDiscoveryPlan) parseNamedScopes() []string {
	scopes := make([]string, 0, 4)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "named workspace scope:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Named workspace scope:"):], ",") {
			candidate = strings.TrimSpace(strings.Trim(candidate, "`'\""))
			if candidate == "" {
				continue
			}

			candidate = filepath.ToSlash(strings.TrimSuffix(strings.TrimPrefix(candidate, "./"), "/"))
			scopes = textutil.AppendUnique(scopes, candidate)
		}
	}

	return scopes
}

func (p delegatedDiscoveryPlan) computeHasPrimaryNamedScope() bool {
	for _, scope := range p.namedScopes {
		if !delegatedReadOnlySecondaryEvidencePath(scope) {
			return true
		}
	}

	return false
}

func (p delegatedDiscoveryPlan) computePrimaryScope() string {
	if delegatedReadOnlyChildMechanismQuestion(p.objective) && !delegatedObjectiveNeedsEntrypointScope(p.objective) {
		return "internal/runtime/agent"
	}

	firstCandidate := ""

	for _, candidate := range p.namedScopes {
		if candidate == "" {
			continue
		}

		if firstCandidate == "" {
			firstCandidate = candidate
		}

		if !delegatedReadOnlySecondaryEvidencePath(candidate) {
			return candidate
		}
	}

	if firstCandidate != "" {
		return firstCandidate
	}

	for _, candidate := range pathparse.MentionedWorkspacePaths(p.objective) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		if pathparse.HasWorkspaceFileExtension(candidate) {
			dir := filepath.ToSlash(filepath.Dir(candidate))
			if dir != "." && dir != "" {
				return dir
			}

			return candidate
		}

		return filepath.ToSlash(candidate)
	}

	return "."
}

func (p delegatedDiscoveryPlan) computeDetailedNamedScopes() []string {
	var scopes []string

	for _, candidate := range p.namedScopes {
		candidate = NormalizeRunnerPath(strings.Trim(strings.TrimSpace(candidate), "`'\""))
		if candidate == "" || candidate == "." || delegatedReadOnlyBroadNamedScope(candidate) || delegatedReadOnlySecondaryEvidencePath(candidate) {
			continue
		}

		scopes = textutil.Merge(scopes, []string{candidate})
	}

	return scopes
}

func (p delegatedDiscoveryPlan) computePurpose() string {
	lower := strings.ToLower(strings.TrimSpace(p.questionText))
	if lower == "" {
		lower = strings.ToLower(p.computeQuestionText())
	}

	switch {
	case ContainsAny(lower, "security", "unsafe", "vulnerability", "injection", "secret", "token"):
		return "security_review"
	case ContainsAny(lower, "git history", "history of", "historical", "evolution", "changed over time", "regression introduced"):
		return "history_context"
	case ContainsAny(lower, "call", "calls", "trace", "tracing", "invoked", "invoke", "entrypoint"):
		return "function_tracing"
	case ContainsAny(lower, "impact", "blast radius", "affected", "change"):
		return "impact_analysis"
	default:
		return "architecture_boundary"
	}
}

func (p delegatedDiscoveryPlan) computeQuestionText() string {
	lines := make([]string, 0, 2)

	for _, raw := range strings.Split(p.objective, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "named workspace scope:") ||
			strings.HasPrefix(lower, "key terms:") ||
			strings.HasPrefix(lower, "scope guidance:") ||
			strings.HasPrefix(lower, "start from ") ||
			strings.HasPrefix(lower, "return ") ||
			strings.HasPrefix(lower, "- ") {

			continue
		}

		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return p.objective
	}

	return strings.Join(lines, " ")
}

func (p delegatedDiscoveryPlan) computeSearchPattern() string {
	terms := make([]string, 0, 6)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)

		lower := strings.ToLower(line)

		if !strings.HasPrefix(lower, "key terms:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Key terms:"):], ",") {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = textutil.AppendUnique(terms, term)
			}
		}
	}

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = textutil.AppendUnique(terms, term)
		}
	}

	if len(terms) == 0 {
		for _, candidate := range strings.Fields(p.questionText) {
			if term, ok := delegatedReadOnlySearchTerm(candidate); ok {
				terms = textutil.AppendUnique(terms, term)
			}

			if len(terms) >= 4 {
				break
			}
		}
	}

	for _, term := range delegatedReadOnlyMechanismTerms(p.objective) {
		if normalized, ok := delegatedReadOnlySearchTerm(term); ok {
			terms = textutil.AppendUnique(terms, normalized)
		}
	}

	return strings.Join(terms, "|")
}

func (p delegatedDiscoveryPlan) computeSymbolPattern() string {
	candidates := make([]string, 0, 6)

	for _, line := range strings.Split(p.objective, "\n") {
		line = strings.TrimSpace(line)

		lower := strings.ToLower(line)

		if !strings.HasPrefix(lower, "key terms:") {
			continue
		}

		for _, candidate := range strings.Split(line[len("Key terms:"):], ",") {
			if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(candidate); ok {
				candidates = textutil.AppendUnique(candidates, symbol)
			}
		}
	}

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		if symbol, ok := delegatedReadOnlyLiteralSymbolCandidate(fragment); ok {
			candidates = textutil.AppendUnique(candidates, symbol)
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	return candidates[0]
}

func (p delegatedDiscoveryPlan) computePromptKeyTerms() []string {
	terms := make([]string, 0, 6)

	for _, fragment := range pathparse.QuotedFragments(p.objective) {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" || pathparse.LooksLikeWorkspacePathToken(fragment) {
			continue
		}

		lower := strings.ToLower(fragment)
		if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordPromptKeyTerm) {
			continue
		}

		terms = textutil.AppendUnique(terms, fragment)
	}

	for _, candidate := range strings.Fields(p.objective) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || pathparse.LooksLikeWorkspacePathToken(candidate) {
			continue
		}

		lower := strings.ToLower(strings.Trim(candidate, "`'\"()[]{}<>,.:;!?"))
		if delegatedDiscoveryStopWord(lower, delegatedDiscoveryStopWordPromptKeyTerm) {
			continue
		}

		if len(lower) < 5 {
			continue
		}

		terms = textutil.AppendUnique(terms, lower)
		if len(terms) >= 8 {
			break
		}
	}

	return terms
}

type delegatedDiscoveryStopWordUse int

const (
	delegatedDiscoveryStopWordSearchTerm delegatedDiscoveryStopWordUse = iota
	delegatedDiscoveryStopWordPromptKeyTerm
	delegatedDiscoveryStopWordSymbol
)

func delegatedDiscoveryStopWord(lower string, use delegatedDiscoveryStopWordUse) bool {
	switch strings.TrimSpace(lower) {
	case "",
		"agent", "codebase", "architecture", "configuration", "config", "configs", "prompt", "prompts", "main",
		"files", "file", "directory", "directories", "entry", "entrypoints", "line", "lines",
		"harness", "implementation", "source", "truth", "source-of-truth", "source_of_truth", "before", "tests", "test", "readme", "docs", "start",
		"internal", "workspace", "named", "scope", "question", "questions", "direct", "directly", "delegated", "repository", "relative",
		"evidence", "sufficient", "inspected", "guidance", "prefer", "targeted", "symbol", "symbols", "bounded", "read", "reads", "overview", "overviews",
		"continue", "readiness", "claim", "claims", "probe", "probes", "resolve", "materially", "incomplete":
		return true
	}

	if use == delegatedDiscoveryStopWordPromptKeyTerm {
		switch strings.TrimSpace(lower) {
		case "how", "does", "this", "explain", "inspect", "explore", "look", "overall", "return", "focus", "specifically", "emphasize",
			"use", "uses", "used", "using", "utilize", "utilizes", "utilized", "utilise", "utilises", "make", "makes", "made",
			"paths", "path", "findings", "finding", "confidence", "gap", "gaps", "key", "terms", "exact", "entrypoint", "agents":
			return true
		}
	}

	if use == delegatedDiscoveryStopWordSymbol {
		switch strings.TrimSpace(lower) {
		case "subagent", "subagents", "delegate", "task", "runner", "package", "import", "const", "type", "func", "return",
			"use", "uses", "using", "utilize", "utilizes", "utilise", "utilises", "make", "makes":
			return true
		}
	}

	return false
}

func (r *Runner) applyLanePreflight(input runnerLanePreflightInput) runnerLanePreflightDecision {
	decision := runnerLanePreflightDecision{
		OriginalRequest: input.Request,
		OriginalRaw:     input.Raw,
		Request:         input.Request,
		Raw:             input.Raw,
	}

	if workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		// Project-defined external tools are allowed even in conversation
		// mode — they're opaque shell invocations the host project
		// registered specifically so the agent could use them. Built-in
		// workspace tools (edit, shell, discovery_query, etc.) remain
		// rejected because conversation_only's contract is "no workspace
		// state change, no idiom-internal exploration." Externals
		// satisfy that contract by being host-controlled.
		toolName := strings.TrimSpace(decision.Request.Tool)
		isExternal := false

		isExternal = r.workspaceToolSurfaceSnapshot().HasExternalTool(toolName)

		if !isExternal {
			decision.RejectSummary = "Tool request rejected by conversation-only contract"
			decision.RejectError = "this turn is conversation-only. Do not call built-in tools; project-defined external tools (registered via workspace.external_tools) are allowed if any are configured. Otherwise answer directly from the prompt and prior visible context."
			decision.RejectStopReason = "conversation_only_tool_rejected"

			return decision
		}
	}

	if redirected, ok := r.mainReadOnlyDelegationBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.mainReadOnlyBroadSearchContextPackRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyDiscoveryQueryRepairRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyPostDiscoveryInventoryRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyBootstrapQueryRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyBootstrapRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlySecondaryEvidenceRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyNamedFileReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlySearchHitReadRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.delegatedReadOnlyIntegrationPivotRedirect(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := postReadinessSymbolBodyUpgradeRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.planThenExecuteRequiredSourceFollowupRedirect(input.Results, decision.Request); ok {
		r.emitEvent(RunEvent{
			Role:    "plan_then_execute_source_followup_redirect",
			Content: "redirected a non-advancing required-path source read to the next bounded source-evidence step",
		})

		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) &&
		!workspace.AnalyzeVerificationRequest(decision.Request).Attempted {

		if !input.VerificationAttempted {
			if redirected, ok := r.verificationBootstrapRequest(); ok {
				decision.Request = redirected
				decision.Raw = marshalToolRequestRaw(redirected)
			} else {
				decision.RejectSummary = "Verification-first contract rejected non-verification action"
				decision.RejectError = "this turn is verification-only. Run the requested verification command first with shell (for example go test ./..., go build ./..., npm test, or the seeded failing command), then inspect files only if the output needs explanation."
				decision.RejectStopReason = "verification_first_contract_exhausted"

				return decision
			}
		} else {
			decision.RejectSummary = "Verification-only contract rejected post-verification drift"
			decision.RejectError = "this turn is verification-only and a verification command has already run. Report the pass/fail result directly unless another verification command is strictly necessary."
			decision.RejectStopReason = "verification_only_post_check_drift"

			return decision
		}
	}

	if summary, errorText, reject := r.conversationRepeatedBroadDiscoveryAfterGate(decision.Request, input.Results); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText

		return decision
	}

	if summary, errorText, reject := prematureTargetReadRejection(decision.Request, input.Results, r.RuntimeBrief); reject {
		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "premature_target_read_exhausted"

		return decision
	}

	if redirected, ok := evidenceFirstTargetReadRedirect(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if redirected, ok := r.debugLogPreflightRequest(input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if summary, errorText, reject := debugLogPhaseScopeRejection(decision.Request, input.Results, r.RuntimeBrief, currentDebugLogSessionID(r.Workspace)); reject {
		if summary == "Blind log reread rejected" {
			decision.RejectSummary = summary
			decision.RejectError = errorText

			return decision
		}

		decision.RejectSummary = summary
		decision.RejectError = errorText
		decision.RejectStopReason = "debug_logs_phase_scope_enforced"

		return decision
	}

	if redirected, ok := debugLogFollowupRequest(r.RuntimeBrief, input.Results, decision.Request); ok {
		decision.Request = redirected
		decision.Raw = marshalToolRequestRaw(redirected)
	}

	if debugLogEvidenceSatisfied(r.RuntimeBrief, input.Results) && workspace.IsReadTool(strings.TrimSpace(decision.Request.Tool)) {
		decision.RejectSummary = "Debug-log contract rejected extra exploration after evidence was satisfied"
		decision.RejectError = "you already have a concrete failure hit from the selected log and a nearby read window. A final answer is available if that evidence supports it; otherwise name the specific missing datum before another probe."
		decision.RejectStopReason = "debug_logs_post_evidence_drift"

		return decision
	}

	return decision
}

func (r *Runner) conversationRepeatedBroadDiscoveryAfterGate(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, reject bool) {
	if r == nil || !workerruntime.TaskFamilyIsConversation(r.RuntimeBrief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) ||
		runnerBriefRequiresMutation(r.RuntimeBrief) ||
		runnerVerificationRequired(r.RuntimeBrief) {

		return "", "", false
	}

	if !conversationBroadLocalizationAfterCompletionGate(results) || !conversationBroadDiscoveryRequest(req) {
		return "", "", false
	}

	summary = "Conversation contract rejected repeated broad discovery after bounded evidence"

	// Check if the completion gate rejection was about source grounding
	// (the model cited files it never read). If so, call out the specific
	// missing files and tell the model to read them instead of searching.
	if missingPaths := conversationSourceGroundingMissingPaths(results); len(missingPaths) > 0 {
		errorText = "The completion gate rejected your answer because it cites file(s) that were never read: " +
			strings.Join(missingPaths, ", ") +
			". Use `read_file` on those file(s) instead of another broad search."
	} else {
		errorText = "the conversation lane already spent its recovery probe on broad localization after a completion-gate miss. Answer from the gathered evidence, or take one narrow read/search that names the concrete missing claim; do not repeat broad discovery."
	}

	return summary, errorText, true
}

// conversationSourceGroundingMissingPaths scans typed completion_gate
// source-grounding payloads and returns the unsupported paths the model should
// read.
func conversationSourceGroundingMissingPaths(results []workspace.ToolResult) []string {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "completion_gate" {
			continue
		}

		if assessment, ok := sourceGroundingAssessmentFromResult(result); ok && len(assessment.Paths) > 0 {
			return uniqueSortedRunnerPaths(assessment.Paths)
		}
	}

	return nil
}

func conversationBroadLocalizationAfterCompletionGate(results []workspace.ToolResult) bool {
	afterGate := false

	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool == "completion_gate" {
			afterGate = true
			continue
		}

		if !afterGate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if conversationBroadDiscoveryToolResult(result) {
			return true
		}
	}

	return false
}

func conversationBroadDiscoveryToolResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Output) == "" && strings.TrimSpace(result.Summary) == "" {
		return false
	}

	switch strings.TrimSpace(result.Tool) {
	case "context_pack", "context_query", "context_zoom", "context_impact", "discovery_query", "search_files", "search_content", "structural_search":
		return true
	default:
		return false
	}
}

type conversationDiscoveryRequestRelationKind string

const (
	conversationDiscoveryRequestRelationNone                  conversationDiscoveryRequestRelationKind = "none"
	conversationDiscoveryRequestRelationSourceOwnerRefinement conversationDiscoveryRequestRelationKind = "source_owner_refinement"
	conversationDiscoveryRequestRelationBroadRediscovery      conversationDiscoveryRequestRelationKind = "broad_rediscovery"
)

func conversationBroadDiscoveryRequest(req workspace.ToolRequest) bool {
	return conversationDiscoveryRequestRelation(req) == conversationDiscoveryRequestRelationBroadRediscovery
}

func conversationDiscoveryRequestRelation(req workspace.ToolRequest) conversationDiscoveryRequestRelationKind {
	if conversationSemanticSourceOwnerRefinement(req) {
		return conversationDiscoveryRequestRelationSourceOwnerRefinement
	}

	if conversationTargetedContentSearch(req) ||
		conversationTargetedContextZoom(req) ||
		conversationTargetedFileNameSearch(req) {

		return conversationDiscoveryRequestRelationNone
	}

	if conversationBroadDiscoveryRequestTool(req.Tool) {
		return conversationDiscoveryRequestRelationBroadRediscovery
	}

	return conversationDiscoveryRequestRelationNone
}

func conversationBroadDiscoveryRequestTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "context_pack", "context_query", "context_zoom", "context_impact", "discovery_query", "search_files", "search_content", "structural_search":
		return true
	default:
		return false
	}
}

func conversationSemanticSourceOwnerRefinement(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "context_query" {
		return false
	}

	if strings.TrimSpace(req.Query) == "" || strings.TrimSpace(req.Resolution) == "" {
		return false
	}

	return conversationContextQueryHasConcreteScope(req)
}

func conversationContextQueryHasConcreteScope(req workspace.ToolRequest) bool {
	if conversationConcreteSearchScope(req.Path) ||
		strings.TrimSpace(req.Handle) != "" ||
		strings.TrimSpace(req.ID) != "" ||
		strings.TrimSpace(req.NamedGap) != "" {

		return true
	}

	return false
}

func conversationTargetedContentSearch(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "search_content" {
		return false
	}

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return false
	}

	return textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) != "" || conversationConcreteSearchScope(req.Path)
}

func conversationTargetedContextZoom(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "context_zoom" {
		return false
	}

	if strings.TrimSpace(req.Handle) == "" && strings.TrimSpace(req.ID) == "" && strings.TrimSpace(req.Path) == "" {
		return false
	}

	return strings.TrimSpace(req.Resolution) != "" || req.IncludeSource || textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) != ""
}

func conversationTargetedFileNameSearch(req workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != "search_files" {
		return false
	}

	pattern := textutil.FirstNonEmpty(req.Pattern, req.Glob)

	pattern = filepath.ToSlash(strings.TrimSpace(pattern))

	if textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) != "" {
		return true
	}

	if pattern != "" && conversationFileSearchPatternIsConcrete(pattern) {
		return true
	}

	// A named directory scope (e.g. "docs", ".idiom", "internal/runtime")
	// narrows the file search to a concrete subdirectory the model chose.
	// Honor it as targeted, mirroring conversationTargetedContentSearch,
	// instead of letting it fall through to broad-rediscovery rejection.
	return conversationConcreteSearchScope(req.Path)
}

func conversationConcreteSearchScope(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || path == "." || path == ".." || path == "~" || strings.ContainsAny(path, "*?[") {
		return false
	}

	if pathparse.HasWorkspaceFileExtension(path) || strings.Contains(path, "/") {
		return true
	}

	// A single named directory segment (e.g. "docs", ".idiom", "internal",
	// "cmd") is concrete scope: the model named a specific subdirectory
	// rather than the workspace root. Treat narrowing into a named
	// directory as targeted, not as broad rediscovery. The trivial roots
	// (".", "..", "~") and glob patterns are already excluded above.
	return len(path) >= 2
}

func conversationFileSearchPatternIsConcrete(pattern string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}

	if !strings.ContainsAny(pattern, "*?[") {
		// Literal filename or directory name (no glob chars). Concrete
		// when it carries path structure: a separator, a file extension,
		// or a leading-dot hidden name like ".idiom" or ".github" that
		// the model is using as a directory anchor. A bare generic word
		// (e.g. "scroll") stays non-concrete so root-scope word searches
		// at Path:"." still count as broad rediscovery.
		if strings.Contains(pattern, "/") || pathparse.HasWorkspaceFileExtension(pattern) {
			return true
		}

		return strings.HasPrefix(pattern, ".") && len(pattern) >= 2
	}

	for _, token := range RecoveryObjectiveSearchWords(pattern, 16) {
		token = strings.ToLower(strings.TrimSpace(token))
		if len(token) < 3 || conversationBroadFileSearchToken(token) {
			continue
		}

		return true
	}

	for _, token := range conversationFileSearchPatternTokens(pattern) {
		if len(token) < 3 || conversationBroadFileSearchToken(token) {
			continue
		}

		return true
	}

	return false
}

func conversationFileSearchPatternTokens(pattern string) []string {
	pattern = strings.ToLower(filepath.ToSlash(strings.TrimSpace(pattern)))
	if pattern == "" {
		return nil
	}

	words := strings.FieldsFunc(pattern, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})

	var out []string

	seen := map[string]struct{}{}

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 3 {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		out = append(out, word)
	}

	return out
}

func conversationBroadFileSearchToken(token string) bool {
	switch token {
	case "cmd", "src", "pkg", "lib", "app", "apps", "test", "tests", "internal", "script", "scripts",
		"go", "rs", "py", "js", "jsx", "ts", "tsx", "java", "cpp", "hpp", "cc", "cxx", "md", "json", "yaml", "yml", "toml":
		return true
	default:
		return false
	}
}

func conversationBroadDiscoveryRejectionRecorded(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		if strings.Contains(strings.ToLower(strings.TrimSpace(result.Summary)), "conversation contract rejected repeated broad discovery") {
			return true
		}
	}

	return false
}

func conversationEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).Answerable()
}

// conversationInspectedPathSummary returns a compact summary of the
// most recent read/navigation results. It reports evidence without
// declaring readiness — the model owns the decision of when evidence
// is sufficient.
func conversationInspectedPathSummary(results []workspace.ToolResult) string {
	var inspected []string

	for i := len(results) - 1; i >= 0 && len(inspected) < 5; i-- {
		r := results[i]
		if strings.TrimSpace(r.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(r.Tool)
		if tool == "context_pack" || tool == "task" {
			continue
		}

		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			continue
		}

		inspected = append(inspected, summary)
	}

	if len(inspected) == 0 {
		return ""
	}

	// Reverse to show oldest first
	for i, j := 0, len(inspected)-1; i < j; i, j = i+1, j-1 {
		inspected[i], inspected[j] = inspected[j], inspected[i]
	}

	return strings.Join(inspected, "; ")
}

// conversationMilestones derives context-window-aware timing for
// conversation-lane auto-findings capture and call-count awareness nudges.
// Returns (firstMilestone, nudgeInterval) in tool calls.
//
// The derivation mirrors compactionParams in internal/surfaces/tui/app/compaction.go:
// firstMilestone is 25% of the compaction threshold in tool calls,
// nudgeInterval is 35%. Both are floored at current hardcoded defaults
// (15 and 20) so small models keep the existing schedule; large models
// get proportionally later milestones to match their larger context windows.
func conversationMilestones(ctxSize int) (firstMilestone int, nudgeInterval int) {
	if ctxSize <= 0 {
		ctxSize = 65536
	}

	const estTokensPerMsg = 125

	thresholdMsgs := ctxSize * 40 / 100 / estTokensPerMsg

	if thresholdMsgs > 200 {
		thresholdMsgs = 200
	}

	maxToolCalls := thresholdMsgs / 2

	firstMilestone = max(15, maxToolCalls*25/100)
	nudgeInterval = max(20, maxToolCalls*35/100)

	return firstMilestone, nudgeInterval
}

func (r *Runner) conversationEvidenceAvailableForObjective(results []workspace.ToolResult) bool {
	if r == nil || runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(r.RuntimeBrief), results).Answerable()
}

func (r *Runner) mainReadOnlyDelegationBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolRequest{}, false
	}

	if mainReadOnlyDelegationBootstrapEvidenceSeen(results) || !prefersEarlyReadOnlyDelegation(r.RuntimeBrief) || strings.TrimSpace(req.Tool) == "task" || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return workspace.ToolRequest{}, false
	}

	if !mainReadOnlyObjectiveShouldDelegate(r.RuntimeBrief.Objective) {
		return workspace.ToolRequest{}, false
	}

	if !shouldDelegateMainReadOnlyBootstrapRequest(req) {
		return workspace.ToolRequest{}, false
	}

	if r.Workspace == nil || !slices.Contains(r.workspaceAdvertisedAllowedTools(), "task") {
		return workspace.ToolRequest{}, false
	}

	prompt := delegatedReadOnlyTaskPrompt(r.RuntimeBrief.Objective, r.Workspace.Root())
	if strings.TrimSpace(prompt) == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:   "task",
		Agent:  "explorer",
		Mode:   "read_only",
		Prompt: prompt,
	}, true
}

func mainReadOnlyObjectiveShouldDelegate(objective string) bool {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	if lower == "" {
		return false
	}

	if mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower) {
		return false
	}

	return ContainsAny(lower,
		"architecture", "boundary", "boundaries", "flow", "handoff", "lifecycle", "orchestration",
		"how does", "how this", "explain how", "trace", "call", "calls", "invoked", "invoke",
		"subagent", "sub-agent", "sub agent", "child task", "child runner",
		"discovery tool", "discovery tools", "tool surface", "increase", "improve", "improvement", "usefulness", "what can we add",
	)
}

func mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower string) bool {
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "what can we add") || strings.Contains(lower, "improve") || strings.Contains(lower, "usefulness") {
		return false
	}

	if strings.HasPrefix(lower, "what does ") || strings.HasPrefix(lower, "where is ") || strings.HasPrefix(lower, "where does ") {
		return true
	}

	return ContainsAny(lower, " mode do", " feature do")
}

func mainReadOnlyDelegationBootstrapEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if tool == "task" {
			return true
		}

		if workspace.IsReadTool(strings.TrimSpace(tool)) && strings.TrimSpace(result.Output) != "" {
			return true
		}
	}

	return false
}

func (r *Runner) mainReadOnlyBroadSearchContextPackRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolRequest{}, false
	}

	if len(results) > 0 || runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) &&
		!workerruntime.RuntimeContractIsArchitecture(r.RuntimeBrief.Contract) {

		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSearchContent || !slices.Contains(r.RuntimeBrief.AllowedTools, workspace.ToolNameContextPack) {
		return workspace.ToolRequest{}, false
	}

	path := NormalizeRunnerPath(req.Path)
	if path != "" && path != "." {
		return workspace.ToolRequest{}, false
	}

	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	task := strings.TrimSpace(r.RuntimeBrief.Objective)
	if task == "" {
		task = pattern
	}

	return workspace.ToolRequest{Tool: workspace.ToolNameContextPack, Task: task, Mode: "plan", Limit: 8}, true
}

func shouldDelegateMainReadOnlyBootstrapRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextStatus, workspace.ToolNameDiscoveryQuery, workspace.ToolNameHarnessAudit:
		return true
	case workspace.ToolNameListFiles:
		return true
	case workspace.ToolNameSearchFiles:
		pattern := strings.TrimSpace(req.Pattern)
		return pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*")
	case workspace.ToolNameSearchContent:
		path := strings.TrimSpace(req.Path)
		return path == "" || path == "." || !pathparse.HasWorkspaceFileExtension(path)
	default:
		return workspace.IsBroadInspectionRequest(req)
	}
}

func postReadinessSymbolBodyUpgradeRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	tool := strings.TrimSpace(req.Tool)
	if !postReadinessSymbolBodyUpgradeableTool(tool, req.Capability) || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, false
	}

	path := normalizeDelegatedDiscoveryEvidencePath(strings.TrimSpace(req.Path))
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	if !hasTruncatedGoReadSymbolResult(results, path) {
		return workspace.ToolRequest{}, false
	}

	upgraded := req
	if tool == workspace.ToolNameCodeNavigate {
		upgraded.Capability = string(semanticlanguage.CapExtractBody)
	} else {
		upgraded.Tool = "go_extract_body"
	}

	return upgraded, true
}

func postReadinessSymbolBodyUpgradeableTool(tool, capability string) bool {
	switch strings.TrimSpace(tool) {
	case "go_read_symbol":
		return true
	case workspace.ToolNameCodeNavigate:
		return workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapReadSymbol)(capability)
	default:
		return false
	}
}

func hasTruncatedGoReadSymbolResult(results []workspace.ToolResult, path string) bool {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if (tool != "go_read_symbol" && tool != workspace.ToolNameCodeNavigate) || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if tool == workspace.ToolNameCodeNavigate && !strings.Contains(strings.ToLower(result.Output), "routed_tool: go_read_symbol") {
			continue
		}

		summary := normalizeWorkspacePathScanText(result.Summary + "\n" + result.Output)
		if !strings.Contains(summary, path) {
			continue
		}

		if strings.Contains(strings.ToLower(summary), "full-body adapter: go_extract_body") {
			return true
		}
	}

	return false
}

func (r *Runner) delegatedReadOnlyDiscoveryQueryRepairRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "discovery_query" {
		return workspace.ToolRequest{}, false
	}

	plan := newDelegatedDiscoveryPlan(r.RuntimeBrief.Objective)
	if redirected, ok := plan.candidateConsumptionRequest(results, r.RuntimeBrief.AllowedTools); ok {
		return redirected, true
	}

	repaired, changed := plan.repairDiscoveryQuery(req)
	if r.delegatedReadOnlyDiscoveryPathMissing(repaired.Path) {
		if replacement := r.delegatedReadOnlyFirstExistingDetailedScope(plan); replacement != "" {
			repaired.Path = replacement
			changed = true
		} else if plan.primaryScope != "" && plan.primaryScope != "." {
			repaired.Path = plan.primaryScope
			changed = true
		}
	}

	return repaired, changed
}

func (r *Runner) delegatedReadOnlyPostDiscoveryInventoryRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if redirected, ok := delegatedReadOnlyPostDiscoveryOutlineReadRedirect(results, req); ok {
		return redirected, true
	}

	if !delegatedReadOnlyPostDiscoveryInventoryTool(req.Tool) || len(results) == 0 {
		return workspace.ToolRequest{}, false
	}

	plan := newDelegatedDiscoveryPlan(r.RuntimeBrief.Objective)

	return plan.candidateConsumptionRequest(results, r.RuntimeBrief.AllowedTools)
}

func delegatedReadOnlyPostDiscoveryOutlineReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if len(results) == 0 || len(newToolResultEvidenceProjection(results).ReadOnlySourceBodyEvidencePaths()) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(req.Capability) != "file_outline" {
		return workspace.ToolRequest{}, false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !delegatedDiscoveryConsumableSourcePath(path) || newToolResultEvidenceProjection(results).HasSourceBody(path) {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         160,
		NamedGap:        "source-body evidence for delegated read-only synthesis",
		WhyNotAnswering: "the prior discovery query returned source candidates but not source-body evidence",
	}, true
}

func delegatedReadOnlyPostDiscoveryInventoryTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameFileStats, workspace.ToolNameListFiles, workspace.ToolNameSearchFiles:
		return true
	default:
		return false
	}
}

func (r *Runner) delegatedReadOnlyFirstExistingDetailedScope(plan delegatedDiscoveryPlan) string {
	for _, scope := range plan.detailedScopes {
		if scope == "" || r.delegatedReadOnlyDiscoveryPathMissing(scope) {
			continue
		}

		return scope
	}

	return ""
}

func (r *Runner) delegatedReadOnlyDiscoveryPathMissing(path string) bool {
	path = NormalizeRunnerPath(path)
	if r == nil || r.Workspace == nil || path == "" || path == "." {
		return false
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}

	if _, err := os.Stat(filepath.Join(root, path)); err != nil {
		return true
	}

	return false
}

func delegatedDiscoveryPurposeValid(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case "impact_analysis", "function_tracing", "architecture_boundary", "security_review", "history_context":
		return true
	default:
		return false
	}
}

func (r *Runner) delegatedReadOnlyBootstrapRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case "list_files":
		if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
			return redirected, true
		}

		return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
	case "search_files":
		pattern := strings.TrimSpace(req.Pattern)
		if pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*") {
			if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
				return redirected, true
			}

			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	case "search_content":
		path := strings.TrimSpace(req.Path)
		if path == "" || path == "." || !pathparse.HasWorkspaceFileExtension(path) {
			if redirected, ok := delegatedReadOnlyBootstrapStructuredRequest(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools); ok {
				return redirected, true
			}

			return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
		}
	}

	return workspace.ToolRequest{}, false
}

func (r *Runner) delegatedReadOnlyBootstrapQueryRequest(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" || len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) == "discovery_query" || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return workspace.ToolRequest{}, false
	}

	if !slices.Contains(r.RuntimeBrief.AllowedTools, "discovery_query") {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyBootstrapHasExactStructuredProbe(r.RuntimeBrief.Objective, r.RuntimeBrief.AllowedTools) {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyObjectiveHasExactPrimarySourceAnchor(r.RuntimeBrief.Objective) &&
		!delegatedReadOnlyCompositeQuestionOverridesExactSourceAnchor(r.RuntimeBrief.Objective) {

		return workspace.ToolRequest{}, false
	}

	if !delegatedReadOnlyObjectiveRequiresDiscoveryQuery(r.RuntimeBrief.Objective) {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:     "discovery_query",
		Purpose:  delegatedReadOnlyDiscoveryPurpose(r.RuntimeBrief.Objective),
		Path:     delegatedReadOnlyBootstrapScope(r.RuntimeBrief.Objective),
		Pattern:  pattern,
		MaxDepth: 2,
	}, true
}

func delegatedReadOnlyObjectiveRequiresDiscoveryQuery(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	// This classifier only identifies broad child-runner mechanism questions
	// that need composite discovery. Exact source anchors are preserved by
	// delegatedReadOnlyBootstrapQueryRequest before this predicate is consulted.
	return ContainsAny(lower,
		"discovery tool",
		"discovery tools",
		"tool usefulness",
		"increase their usefulness",
		"overall usefulness",
		"subagent",
		"sub-agent",
		"sub agent",
		"child-agent",
		"child agent",
		"child task",
		"task tool",
		"delegation",
	)
}

func delegatedReadOnlyObjectiveHasExactPrimarySourceAnchor(objective string) bool {
	for _, path := range pathparse.MentionedWorkspacePaths(objective) {
		path = NormalizeRunnerPath(path)
		if path == "" || delegatedReadOnlySecondaryEvidencePath(path) || delegatedReadOnlyMentionedPathIsPattern(path) {
			continue
		}

		if pathparse.HasWorkspaceFileExtension(path) {
			return true
		}
	}

	return false
}

func delegatedReadOnlyCompositeQuestionOverridesExactSourceAnchor(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	return ContainsAny(lower, "discovery tool", "discovery tools", "tool usefulness", "increase their usefulness", "overall usefulness")
}

func delegatedReadOnlyMentionedPathIsPattern(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	base := filepath.Base(path)
	if strings.ContainsAny(base, "*?[]") {
		return true
	}

	return strings.HasPrefix(path, ".") && !strings.Contains(strings.TrimPrefix(path, "."), "/")
}

func delegatedReadOnlyChildMechanismQuestion(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(delegatedDisplayQuestion(objective)), " "))
	return ContainsAny(lower,
		"subagent",
		"sub-agent",
		"sub agent",
		"child-agent",
		"child agent",
		"child task",
		"task tool",
		"delegation",
	)
}

func delegatedReadOnlyDiscoveryPurpose(objective string) string {
	return newDelegatedDiscoveryPlan(objective).defaultPurpose
}

func delegatedReadOnlyTaskPrompt(objective string, workspaceRoot string) string {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return ""
	}

	parts := []string{objective}
	if scope := delegatedReadOnlyLikelySourceScope(objective, workspaceRoot); scope != "" {
		parts = append(parts, "Named workspace scope: "+scope)
	}

	parts = append(parts,
		"Start from source-of-truth implementation files before tests, README, or docs.",
		"Return inspected paths, key findings, confidence, and named gaps.",
	)

	return strings.Join(parts, "\n")
}

func delegatedReadOnlyLikelySourceScope(objective string, workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return ""
	}

	scopes := make([]string, 0, 6)

	for _, candidate := range delegatedReadOnlyLikelySourceCandidates(objective) {
		info, err := os.Stat(filepath.Join(workspaceRoot, candidate))
		if err != nil || !info.IsDir() {
			continue
		}

		scopes = append(scopes, candidate)
	}

	return strings.Join(scopes, ", ")
}

func delegatedReadOnlyLikelySourceCandidates(objective string) []string {
	if delegatedObjectiveNeedsEntrypointScope(objective) {
		return []string{"internal/runtime/agent", "internal/capabilities/workspace", "cmd", "internal", "pkg", "src"}
	}

	return []string{"internal/runtime/agent", "internal/capabilities/workspace", "internal", "pkg", "src"}
}

func delegatedObjectiveNeedsEntrypointScope(objective string) bool {
	lower := strings.ToLower(objective)

	return ContainsAny(lower,
		"entrypoint",
		"entry point",
		"main entry",
		"main function",
		"cli",
		"command",
		"startup",
		"boot sequence",
		"bootstrap path",
		"program start",
		"cmd directory",
		"cmd/",
	)
}

func delegatedReadOnlyBootstrapSearchRequest(objective string) (workspace.ToolRequest, bool) {
	pattern := delegatedReadOnlyBootstrapPattern(objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	req := workspace.ToolRequest{Tool: "search_content", Pattern: pattern}
	if scope := delegatedReadOnlyBootstrapScope(objective); scope != "" && scope != "." {
		req.Path = scope
	}

	if !pathparse.HasWorkspaceFileExtension(req.Path) {
		req.Glob = "*.go"
	}

	return req, true
}

func delegatedReadOnlyBootstrapStructuredRequest(objective string, allowedTools []string) (workspace.ToolRequest, bool) {
	if !slices.Contains(allowedTools, "go_workspace_symbol") {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapSymbolPattern(objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: pattern}, true
}

func delegatedReadOnlyBootstrapHasExactStructuredProbe(objective string, allowedTools []string) bool {
	_, ok := delegatedReadOnlyBootstrapStructuredRequest(objective, allowedTools)
	return ok
}

func delegatedReadOnlyBootstrapScope(objective string) string {
	return newDelegatedDiscoveryPlan(objective).primaryScope
}

func delegatedReadOnlyBroadNamedScope(scope string) bool {
	switch NormalizeRunnerPath(scope) {
	case "internal", "pkg", "src", "cmd", "prompts", "docs":
		return true
	default:
		return false
	}
}

func delegatedReadOnlyBootstrapPattern(objective string) string {
	return newDelegatedDiscoveryPlan(objective).searchPattern
}

func delegatedReadOnlyBootstrapSymbolPattern(objective string) string {
	return newDelegatedDiscoveryPlan(objective).symbolPattern
}

func delegatedReadOnlySearchTerm(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))

	value = strings.Trim(value, "`'\"()[]{}<>,.:;!?")
	value = strings.TrimSpace(strings.TrimPrefix(value, "./"))

	if value == "" || pathparse.LooksLikeWorkspacePathToken(value) {
		return "", false
	}

	if delegatedDiscoveryStopWord(value, delegatedDiscoveryStopWordSearchTerm) {
		return "", false
	}

	if len(value) < 4 {
		return "", false
	}

	var b strings.Builder

	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteString(regexp.QuoteMeta(string(r)))
		case r == '-', r == '_', unicode.IsSpace(r):
			b.WriteString("[-_ ]?")
		}
	}

	pattern := strings.TrimSpace(b.String())
	if pattern == "" {
		return "", false
	}

	return pattern, true
}

func delegatedReadOnlyLooksCodeShapedSymbol(value string) bool {
	if strings.Contains(value, "_") {
		return true
	}

	hasLower := false

	for i, r := range value {
		switch {
		case unicode.IsDigit(r):
			return true
		case unicode.IsLower(r):
			hasLower = true
		case i > 0 && unicode.IsUpper(r) && hasLower:
			return true
		}
	}

	return false
}

func (r *Runner) delegatedReadOnlySecondaryEvidenceRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	targetPath := r.delegatedReadOnlyRequestPath(req)
	if targetPath == "" || !delegatedReadOnlySecondaryEvidencePath(targetPath) {
		return workspace.ToolRequest{}, false
	}

	if redirected, ok := delegatedReadOnlyNamedPrimaryPathRedirect(r.RuntimeBrief.Objective, targetPath); ok {
		return redirected, true
	}

	if strings.HasSuffix(targetPath, "_test.go") {
		sourcePath := strings.TrimSuffix(targetPath, "_test.go") + ".go"
		candidates := delegatedReadOnlyConsolidatedTestSourceCandidates(targetPath)
		if strings.HasSuffix(filepath.Base(sourcePath), "_tests.go") && len(candidates) > 0 {
			sourcePath = candidates[0]
		} else if r.delegatedReadOnlyDiscoveryPathMissing(sourcePath) {
			for _, candidate := range delegatedReadOnlyConsolidatedTestSourceCandidates(targetPath) {
				if candidate != "" && !r.delegatedReadOnlyDiscoveryPathMissing(candidate) {
					sourcePath = candidate
					break
				}
			}
		}

		pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, sourcePath)

		if pattern != "" {
			return workspace.ToolRequest{Tool: "search_content", Path: sourcePath, Pattern: pattern}, true
		}
	}

	return delegatedReadOnlyBootstrapSearchRequest(r.RuntimeBrief.Objective)
}

func delegatedReadOnlyConsolidatedTestSourceCandidates(testPath string) []string {
	testPath = NormalizeRunnerPath(testPath)
	dir := filepath.Dir(testPath)
	base := filepath.Base(testPath)

	if strings.HasSuffix(base, "_tests_test.go") {
		stem := strings.TrimSuffix(base, "_tests_test.go")
		return []string{
			filepath.ToSlash(filepath.Join(dir, stem+"_core.go")),
			filepath.ToSlash(filepath.Join(dir, stem+".go")),
		}
	}

	return nil
}

func (r *Runner) delegatedReadOnlyRequestPath(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}

	if path := strings.TrimSpace(req.Path); path != "" {
		return filepath.ToSlash(path)
	}

	if r.Workspace == nil || strings.TrimSpace(req.ID) == "" {
		return ""
	}

	path, err := r.Workspace.ResolveHandlePath(req.ID)
	if err != nil {
		return ""
	}

	return filepath.ToSlash(path)
}

func delegatedReadOnlySecondaryEvidencePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	lower := strings.ToLower(path)
	if lower == ".codex/agents" || strings.HasPrefix(lower, ".codex/agents/") {
		return true
	}

	if strings.HasSuffix(lower, "_test.go") || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") {
		return true
	}

	base := strings.ToLower(filepath.Base(lower))

	return base == "readme.md" || strings.HasSuffix(base, ".md")
}

func delegatedReadOnlyNamedPrimaryPathRedirect(objective string, blockedPath string) (workspace.ToolRequest, bool) {
	blockedPath = filepath.ToSlash(strings.TrimSpace(blockedPath))
	for _, candidate := range pathparse.MentionedWorkspacePaths(objective) {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if candidate == "" || candidate == blockedPath || delegatedReadOnlySecondaryEvidencePath(candidate) {
			continue
		}

		pattern := delegatedReadOnlyImplementationPattern(objective, candidate)
		if pattern == "" {
			continue
		}

		return workspace.ToolRequest{Tool: "search_content", Path: candidate, Pattern: pattern}, true
	}

	return workspace.ToolRequest{}, false
}

func delegatedReadOnlyImplementationPattern(objective string, targetPath string) string {
	terms := make([]string, 0, 8)

	for _, part := range strings.Split(delegatedReadOnlyBootstrapPattern(objective), "|") {
		if delegatedReadOnlyImplementationAliasPattern(part) {
			continue
		}

		terms = textutil.AppendUnique(terms, part)
	}

	base := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))

	base = strings.TrimSuffix(base, "_test")

	for _, fragment := range strings.FieldsFunc(base, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	}) {
		if term, ok := delegatedReadOnlySearchTerm(fragment); ok {
			terms = textutil.AppendUnique(terms, term)
		}
	}

	return strings.Join(terms, "|")
}

func delegatedReadOnlyImplementationAliasPattern(part string) bool {
	part = strings.ToLower(strings.TrimSpace(part))
	switch part {
	case "child[-_ ]?task", "task[-_ ]?tool", "delegate", "delegation":
		return true
	default:
		return false
	}
}

func delegatedReadOnlyMechanismTerms(objective string) []string {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	terms := make([]string, 0, 6)

	if ContainsAny(lower, "invoke", "invoked", "execute", "executed", "run", "runner", "workflow", "wired") {
		for _, term := range []string{"invoke", "execute"} {
			terms = textutil.AppendUnique(terms, term)
		}
	}

	if ContainsAny(lower, "subagent", "sub-agent", "sub agent") {
		for _, term := range []string{"child task", "task tool", "delegate", "delegation"} {
			terms = textutil.AppendUnique(terms, term)
		}
	}

	return terms
}

func (r *Runner) delegatedReadOnlyNamedFileReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if len(results) > 0 {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, false
	}

	path := strings.TrimSpace(req.Path)
	if !strings.HasSuffix(path, ".go") {
		return workspace.ToolRequest{}, false
	}

	pattern := delegatedReadOnlyBootstrapPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	if slices.Contains(r.RuntimeBrief.AllowedTools, "go_file_outline") {
		return workspace.ToolRequest{Tool: "go_file_outline", Path: path}, true
	}

	return workspace.ToolRequest{Tool: "search_content", Path: path, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlyIntegrationPivotRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case "read_file", "search_content":
	default:
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyClosureReady(r.agentName(), r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, false
	}

	currentPath := r.delegatedReadOnlyRequestPath(req)
	if currentPath == "" || !strings.HasSuffix(currentPath, ".go") || strings.HasSuffix(currentPath, "_test.go") {
		return workspace.ToolRequest{}, false
	}

	if !delegatedReadOnlyHasBodyRead(results, currentPath) {
		return workspace.ToolRequest{}, false
	}

	targetPath := delegatedReadOnlySiblingImplementationPath(results, currentPath)
	if targetPath == "" || targetPath == currentPath {
		return workspace.ToolRequest{}, false
	}

	if delegatedReadOnlyHasBodyRead(results, targetPath) {
		return workspace.ToolRequest{}, false
	}

	if start, end, ok := delegatedReadOnlySearchHitWindow(results, targetPath); ok {
		return workspace.ToolRequest{
			Tool:      "read_file",
			Path:      targetPath,
			StartLine: start,
			EndLine:   end,
		}, true
	}

	pattern := delegatedReadOnlyImplementationPattern(r.RuntimeBrief.Objective, targetPath)
	if pattern == "" {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{Tool: "search_content", Path: targetPath, Pattern: pattern}, true
}

func (r *Runner) delegatedReadOnlySearchHitReadRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(req.Tool) != "read_file" {
		return workspace.ToolRequest{}, false
	}

	path := r.delegatedReadOnlyRequestPath(req)
	if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return workspace.ToolRequest{}, false
	}

	start, end, ok := delegatedReadOnlySearchHitWindow(results, path)
	if !ok {
		return workspace.ToolRequest{}, false
	}

	if req.StartLine <= 0 || req.EndLine <= 0 {
		return workspace.ToolRequest{}, false
	}

	if req.StartLine == start && req.EndLine == end {
		return workspace.ToolRequest{}, false
	}

	if start >= req.StartLine-20 && end <= req.EndLine+20 {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: start,
		EndLine:   end,
	}, true
}

func (r *Runner) delegatedReadOnlyAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}

	if !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != "delegated_read_only" {
		return false
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}

	tool := strings.TrimSpace(textutil.FirstNonEmpty(result.Tool, req.Tool))
	closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)

	switch {
	case tool == "discovery_query":
		if !delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
			return false
		}

		// A composite discovery packet provides a codebase map but not
		// source content. Require at least one source body read before
		// declaring readiness, otherwise the model is blocked from
		// reading the files the discovery query surfaced.
		if !closure.Answerable() {
			return false
		}
	case workspace.IsReadTool(strings.TrimSpace(tool)):
		if !closure.Answerable() {
			return false
		}
	default:
		return false
	}

	return true
}

func (r *Runner) readOnlyAnalysisAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	_ = req

	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}

	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return false
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) || runnerVerificationRequired(r.RuntimeBrief) {
		return false
	}

	if r.conversationEvidenceAvailableForObjective(results) && !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return true
	}

	if !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return false
	}

	exactProbes := readOnlyAnalysisExactProbeCount(results)
	evidencePaths := promptEvidencePathsFromToolResults(results, 6)

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)
		return exactProbes >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.RuntimeBrief, closure.Evidence)
	}

	closure := readOnlyClosureDecisionForResults(r.RuntimeBrief, results)

	return exactProbes >= 2 && len(evidencePaths) >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.RuntimeBrief, closure.Evidence)
}

func readOnlyAnalysisHasObjectiveRelevantSourceBody(brief workerruntime.WorkerRuntimeBrief, evidence readOnlyAnswerabilityLedger) bool {
	sourceBodies := readOnlyPathSet(evidence.SourceBodyPaths)
	for _, support := range evidence.SourceBodyObjectiveTerms {
		path := NormalizeRunnerPath(support.Path)
		if path == "" || len(support.Terms) == 0 {
			continue
		}

		if _, ok := sourceBodies[path]; ok {
			return true
		}
	}

	for _, path := range textutil.Merge(evidence.SourceBodyObjectivePaths, evidence.SourceBodyPaths) {
		if implementationSourcePathOverlapsObjective(path, brief.Objective) ||
			sourceEvidencePathSharesObjectiveTerm(path, brief.Objective) {

			return true
		}
	}

	return false
}

func readOnlyAnalysisExactProbeCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if CandidateAnchorOnlyResult(result) {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case "read_file":
			if (toolResultEvidenceProjection{}).GeneratedArtifactOnlyReadResult(result) {
				continue
			}

			if span := toolResultReadSpan(result); span > 0 && span <= 120 {
				count++
			}
		case "code_navigate":
			if RoutedSourceReadOutputHasBody(result.Output) &&
				!strings.Contains(result.Output, "capability: file_outline") {

				count++
			}
		default:
			if !workspace.LanguageToolHasAnyCapability(
				result.Tool,
				semanticlanguage.CapReadSymbol,
				semanticlanguage.CapExtractBody,
				semanticlanguage.CapCallsFrom,
				semanticlanguage.CapCallsInto,
				semanticlanguage.CapReferences,
				semanticlanguage.CapDefinition,
				semanticlanguage.CapWorkspaceSymbol,
			) {
				continue
			}
			count++
		}
	}

	return count
}

func toolResultReadSpan(result workspace.ToolResult) int {
	match := regexp.MustCompile(`lines (\d+)-(\d+)`).FindStringSubmatch(result.Summary)
	if len(match) != 3 {
		return 0
	}

	start, errStart := strconv.Atoi(match[1])
	end, errEnd := strconv.Atoi(match[2])

	if errStart != nil || errEnd != nil || end < start {
		return 0
	}

	return end - start + 1
}

func delegatedReadOnlyHasCompositeDiscoveryPacket(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "discovery_query" &&
			strings.TrimSpace(result.Error) == "" &&
			strings.Contains(result.Output, `"artifact_path"`) &&
			strings.Contains(result.Output, `"evidence"`) {

			return true
		}
	}

	return false
}

func delegatedReadOnlyInspectedImplementationPaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if CandidateAnchorOnlyResult(result) {
			continue
		}

		if !workspace.IsSourceBodyInspectionTool(result.Tool) {
			continue
		}

		for _, path := range pinnedPathsFromToolResults([]workspace.ToolResult{result}, 4) {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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

func delegatedReadOnlyHasBodyRead(results []workspace.ToolResult, wantPath string) bool {
	wantPath = filepath.ToSlash(strings.TrimSpace(wantPath))
	if wantPath == "" {
		return false
	}

	for _, path := range delegatedReadOnlyInspectedImplementationPaths(results) {
		if path == wantPath {
			return true
		}
	}

	return false
}

func delegatedReadOnlySiblingImplementationPath(results []workspace.ToolResult, currentPath string) string {
	currentPath = filepath.ToSlash(strings.TrimSpace(currentPath))
	if currentPath == "" {
		return ""
	}

	candidates := make([]string, 0, 6)
	dir := filepath.ToSlash(filepath.Dir(currentPath))
	base := strings.TrimSuffix(filepath.Base(currentPath), filepath.Ext(currentPath))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' })

	for shrink := len(parts) - 1; shrink >= 1; shrink-- {
		candidate := filepath.ToSlash(filepath.Join(dir, strings.Join(parts[:shrink], "_")+".go"))
		if candidate != currentPath {
			candidates = append(candidates, candidate)
		}
	}

	for _, path := range pinnedPathsFromToolResults(results, 32) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == currentPath || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			continue
		}

		if filepath.ToSlash(filepath.Dir(path)) != dir {
			continue
		}

		candidates = append(candidates, path)
	}

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}

		seen[candidate] = struct{}{}
		for _, result := range results {
			if toolResultMentionsPath(result, candidate) {
				return candidate
			}
		}
	}

	return ""
}

func delegatedReadOnlySearchHitWindow(results []workspace.ToolResult, path string) (int, int, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return 0, 0, false
	}

	pattern := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "search_content" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		matches := pattern.FindStringSubmatch(result.Output)
		if len(matches) != 2 {
			continue
		}

		line, err := strconv.Atoi(matches[1])
		if err != nil || line <= 0 {
			continue
		}

		start := line - 20
		if start < 1 {
			start = 1
		}

		return start, line + 80, true
	}

	return 0, 0, false
}

func toolResultMentionsPath(result workspace.ToolResult, path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	for _, field := range []string{result.Summary, result.Output, result.Error} {
		if strings.Contains(filepath.ToSlash(field), path) {
			return true
		}
	}

	return false
}

func allowRecoverablePostReadinessRepairExecution(req workspace.ToolRequest, lastToolResult workspace.ToolResult) bool {
	if textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) == "" {
		return false
	}

	if postReadinessContinuationRejectionSummary(lastToolResult.Summary) {
		return true
	}

	lower := strings.ToLower(strings.TrimSpace(lastToolResult.Summary + " " + lastToolResult.Error))

	return strings.Contains(lower, "missing named gap") || strings.Contains(lower, "include `named_gap`")
}

func (r *Runner) emitThinkingTrace(step int, reason string, message llama.ChatMessage, opts llama.ChatOptions, preservedInHistory bool) {
	if r != nil && r.OnThought != nil && !preservedInHistory {
		for _, text := range thoughtSegmentsFromMessage(message) {
			r.OnThought(ThoughtEvent{Step: step, Text: text})
		}
	}

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
		trace.ContentPreview = textutil.Truncate(strings.TrimSpace(message.Content), 500)
	}

	data, err := json.Marshal(trace)
	if err != nil {
		return
	}

	r.OnEvent(RunEvent{Role: "thinking_trace", Content: string(data)})
}

func thoughtSegmentsFromMessage(message llama.ChatMessage) []string {
	var segments []string
	segments = appendThoughtSegments(segments, message.ReasoningContent)
	segments = appendThoughtSegments(segments, extractThinkTagContent(message.Content)...)
	return segments
}

func appendThoughtSegments(segments []string, values ...string) []string {
	for _, value := range values {
		for _, segment := range splitThoughtSegments(value) {
			if segment != "" {
				segments = append(segments, segment)
			}
		}
	}

	return segments
}

func splitThoughtSegments(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	chunks := strings.Split(text, "\n\n")
	if len(chunks) == 1 {
		chunks = strings.Split(text, "\n")
	}

	var out []string
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		out = append(out, chunk)
	}

	return out
}

func extractThinkTagContent(text string) []string {
	var out []string
	for {
		start := strings.Index(text, "<think>")
		if start < 0 {
			return out
		}

		rest := text[start+len("<think>"):]
		end := strings.Index(rest, "</think>")
		if end < 0 {
			if segment := strings.TrimSpace(rest); segment != "" {
				out = append(out, segment)
			}

			return out
		}

		if segment := strings.TrimSpace(rest[:end]); segment != "" {
			out = append(out, segment)
		}

		text = rest[end+len("</think>"):]
	}
}

func (r *Runner) chatMessagesForOptions(messages []llama.ChatMessage, opts llama.ChatOptions) []llama.ChatMessage {
	return llama.PrepareChatMessagesForBackend(messages, opts, r.clientBackend())
}

func (r *Runner) assistantHistoryMessageForOptions(message llama.ChatMessage, opts llama.ChatOptions) llama.ChatMessage {
	return llama.PrepareAssistantHistoryMessageForBackend(message, opts, r.clientBackend())
}

func (r *Runner) clientBackend() string {
	if r == nil || r.Client == nil {
		return ""
	}

	return r.Client.Backend()
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

func retryableModelTransportError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}

	if text == "eof" || strings.HasSuffix(text, ": eof") {
		return true
	}

	for _, pattern := range []string{
		"transport failed",
		"connection reset",
		"connection refused",
		"deadline exceeded",
		"i/o timeout",
		"network is unreachable",
		"no such host",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}

	return false
}

var (
	stableHandlePathLinePattern      = regexp.MustCompile(`(?m)^f\d+\s+(.+)$`)
	stableHandleInlinePattern        = regexp.MustCompile(`\bf\d+\s+((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+)\b`)
	delegatedPacketPathLinePattern   = regexp.MustCompile(`((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv)):\d+(?:-\d+)?(?:[:][^\s` + "`" + `"'(),.;:]+)?`)
	delegatedPacketPathQuotePattern  = regexp.MustCompile("((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|fish|ps1|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|env|txt|csv|tsv))[`'\"]{2,}")
	closurePathLineSymbolPattern     = regexp.MustCompile(`(?m)(?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathScopedSymbolPattern   = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?[: ]+([A-Za-z_][A-Za-z0-9_]{3,})\b`)
	closurePathCallPattern           = regexp.MustCompile(`(?m)((?:[[:alnum:]_.-]+/)*[[:alnum:]_.-]+\.(?:go|c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|py|pyi|ts|tsx|js|jsx|rs|java|kt|kts|sh|md|json|yaml|yml|toml|txt)):\d+(?::\d+)?:[^\n]*?\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	callLikeIdentifierPattern        = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{3,})\s*\(`)
	qualifiedIdentifierPattern       = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{1,}\.[A-Za-z_][A-Za-z0-9_]{3,})\b`)
	backtickedTokenPattern           = regexp.MustCompile("`([^`]+)`")
	toolLocalSearchHandlePattern     = regexp.MustCompile(`\bf\d+\s+(?:[[:alnum:]_.-]+/)+[[:alnum:]_.-]+\.[[:alnum:]]+:\d+:`)
	goFuncDeclPattern                = regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	codeShapedTokenPattern           = regexp.MustCompile(`\b[A-Za-z][a-z]+(?:[A-Z][a-z0-9]+){1,}\b`)
	codeShapedSnakeCasePattern       = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+){1,}\b`)
	readFileSummaryLineWindowPattern = regexp.MustCompile(`\blines\s+(\d+)-(\d+)\b`)
	readFileSummaryTotalLinesPattern = regexp.MustCompile(`\b(?:file has\s+(\d+)\s+lines\s+total|total\s+lines:\s+(\d+))\b`)
	fileOutlineDeclarationPattern    = regexp.MustCompile(`(?m)^-\s+([A-Za-z_][A-Za-z0-9_\.]*)\s+lines\s+(\d+)-(\d+)\b`)
	fileOutlinePreviewPattern        = regexp.MustCompile(`preview declarations:\s*\[([^\]]*)\]`)
)

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
	if !debugLogContractApplies(brief, results) {
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
	if !workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) || len(brief.TargetRefs) == 0 {
		return ""
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
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

func buildRunnerNextActionStateWithAllowedToolsAndContract(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string, options ...any) (runnerNextActionState, TurnPhaseContract) {
	planningTransport := runnerNextActionPlanningTransport(options)
	surfaceReq, surfaceResult := modelFacingParserRejectionSurface(reason, req, result, recoveryToolTransportMode(planningTransport))
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}
	surfaceTool := strings.TrimSpace(surfaceReq.Tool)
	if surfaceTool == "" {
		surfaceTool = strings.TrimSpace(surfaceResult.Tool)
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
		ToolTransportMode:         recoveryToolTransportMode(planningTransport),
	})
	state := runnerNextActionState{
		Lane:              strings.TrimSpace(agent),
		Phase:             runnerLivePhase(brief, surfaceReq, surfaceResult, results),
		Step:              step,
		Reason:            strings.TrimSpace(reason),
		LastTool:          surfaceTool,
		LastTarget:        workerruntime.ToolRequestTarget(surfaceReq),
		ResultSummary:     textutil.Truncate(strings.TrimSpace(surfaceResult.Summary), 220),
		ResultError:       textutil.Truncate(strings.TrimSpace(surfaceResult.Error), 260),
		ResultOutputChars: len(surfaceResult.Output),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
	}
	// Surface producer-ranked context symbols as actionable annotations.
	if strings.TrimSpace(tool) == workspace.ToolNameContextPack && strings.TrimSpace(result.Error) == "" && result.Facts != nil {
		state.CandidateSymbols = textutil.Prefix(result.Facts.CandidateSymbols, 5)
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

	state.Constraints = textutil.Merge(brief.Constraints, guidance.Constraints)
	state.InvalidNextActions = textutil.Merge(brief.InvalidActions, guidance.InvalidNext)
	state.NextValidActions = textutil.Merge(brief.RequiredNext, guidance.ValidNext)

	phaseHint := ""

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		phaseHint = turnPhaseReviewRepairTerminal
	}

	phaseContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:           phaseHint,
		Brief:               brief,
		Results:             results,
		LastRequest:         req,
		LastResult:          result,
		BaseAllowedTools:    runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
		SupportsNativeTools: planningTransport.SupportsNativeTools,
		TextualToolParser:   planningTransport.TextualToolParser,
	})

	state = applyTurnPhaseContractToNextActionState(state, phaseContract)
	if recoveryState, ok := DerivePlanExecuteRecoveryState(brief, results, planningTransport, phaseContract, req, result, planExecuteRecoveryAttemptsUsed(results)); ok {
		state.RecoveryState = &recoveryState
	}

	if strings.TrimSpace(result.Error) != "" {
		card := BuildRecoveryCard(RecoveryCardInput{
			PhaseContract:       phaseContract,
			CurrentAllowedTools: effectiveRecoveryCardAllowedTools(currentAllowedTools, brief, results, phaseContract),
			RejectedRequest:     surfaceReq,
			RejectedResult:      surfaceResult,
			EvidenceRefs:        state.EvidencePaths,
			RecoveryState:       state.RecoveryState,
		})
		state.recoveryCard = &card
		state.RecoveryCard = RenderRecoveryCard(card)
	}

	if strings.TrimSpace(reason) == parserRecoveryRecordReason {
		state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, guidance.InvalidNext)
		if len(guidance.ValidNext) > 0 {
			state.NextValidActions = append([]string(nil), guidance.ValidNext...)
		}
		state.StateSummary = runnerStateSummary(state)

		return state, phaseContract
	}

	if reviewRepairTerminalOutcomeRequiredState(brief, result) {
		state.Constraints = textutil.Merge(state.Constraints, []string{
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

func runnerNextActionPlanningTransport(options []any) PlanExecutePlanningTransport {
	transport := PlanExecutePlanningTransport{TextualToolParser: true}
	for _, option := range options {
		if candidate, ok := option.(PlanExecutePlanningTransport); ok {
			return candidate
		}
	}
	return transport
}

func applyReadOnlyDiscoveryPendingGuidance(state runnerNextActionState, brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) runnerNextActionState {
	if strings.TrimSpace(state.Phase) != "read_only_evidence_pending" || !readOnlyDirectAnswerLane(brief) {
		return state
	}

	if len(runnerEvidencePathsForBrief(brief, results, 1)) > 0 {
		return state
	}

	strength := discoveryConsumptionResultStrengthFromLegacySemantics(req, result)
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

	state.Constraints = textutil.Merge(state.Constraints, []string{"source_body_evidence_missing_after_discovery"})
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
		"final answer before source-body evidence",
		"treating discovery candidates, context packets, or search hits as source-body evidence",
	})

	switch strength {
	case discoveryConsumptionCandidateOnly:
		state.EvidenceStatus = textutil.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate-only; source owner and source-body evidence are still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"source owner was not established by " + tool + "; use one materially different source-owner or source-body probe scoped to the question, or <<QUESTION>> with the exact unavailable evidence",
			"if candidates are plausible, inspect one returned path with source-body evidence; otherwise switch to an exact lexical/symbol/source-owner probe drawn from the user's claim and the code vocabulary",
		}, state.NextValidActions)
		state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
			"exact replay of the same weak discovery request",
		})
	case discoveryConsumptionSourceOwner:
		state.EvidenceStatus = textutil.Merge(state.EvidenceStatus, []string{
			"discovery_status: candidate owner paths returned, but source-body evidence is still missing",
		})
		state.NextValidActions = prependRunnerStateStrings([]string{
			"inspect one returned path or handle with source-body evidence, take one targeted refinement with concrete scope or named_gap only if the returned owner does not establish the claim, or <<QUESTION>> with the exact blocker",
			"candidate path(s): " + candidateSummary,
		}, state.NextValidActions)
		state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, []string{
			"repeated broad or identical rediscovery before inspecting or materially refining returned candidates",
		})
	}

	return state
}

func buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(agent string, step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool, brief workerruntime.WorkerRuntimeBrief, currentAllowedTools []string, transport PlanExecutePlanningTransport) runnerFailureFeedbackPacket {
	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract(agent, step, reason, req, result, results, declarationMovesForbidden, brief, currentAllowedTools, transport)
	recoveryReq, recoveryResult := modelFacingParserRejectionSurface(reason, req, result, recoveryToolTransportMode(transport))
	recovery := workerruntime.BuildRecoveryPlan(workerruntime.RecoveryInput{
		Lane:                      agent,
		Step:                      step,
		Reason:                    reason,
		Request:                   recoveryReq,
		Result:                    recoveryResult,
		EvidencePaths:             state.EvidencePaths,
		Constraints:               state.Constraints,
		DeclarationMovesForbidden: declarationMovesForbidden,
		EvidenceIntent:            brief.EvidenceIntent,
		WriteCapable:              runnerHasWorkspaceWriteCapability(brief, brief.AllowedTools),
		ToolTransportMode:         recoveryToolTransportMode(transport),
	})

	if scratchpadFailedEvidenceFeedbackApplies(brief, req, result, results) {
		recovery.DoNotRepeat = textutil.AppendUnique(recovery.DoNotRepeat, "same failed evidence lookup through a lower-level or raw read")
		recovery.RequiredNext = textutil.AppendUnique(recovery.RequiredNext, "record the failed, stale, ambiguous, or out-of-scope evidence as a deliverable gap")
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
		FailureClass:       string(recovery.FailureClass),
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
		Brief:               runnerBriefWithPhaseHints(brief, results),
		Results:             results,
		LastRequest:         req,
		LastResult:          result,
		BaseAllowedTools:    runnerCurrentAllowedOrBriefTools(currentAllowedTools, brief.AllowedTools),
		SupportsNativeTools: transport.SupportsNativeTools,
		TextualToolParser:   transport.TextualToolParser,
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

func modelFacingParserRejectionSurface(reason string, req workspace.ToolRequest, result workspace.ToolResult, transportMode string) (workspace.ToolRequest, workspace.ToolResult) {
	if strings.TrimSpace(reason) != parserRecoveryRecordReason {
		return req, result
	}

	surfaceReq := req
	surfaceResult := result
	if recoveryRejectedResultIsSubmitPlan(req, result) {
		surfaceReq.Tool = workspace.ToolNameSubmitPlan
		surfaceResult.Tool = workspace.ToolNameSubmitPlan
	} else {
		surfaceReq.Tool = ""
		surfaceResult.Tool = ""
	}
	surfaceResult.Error = modelFacingToolParseError(surfaceResult.Error, transportMode)

	return surfaceReq, surfaceResult
}

func modelFacingToolParseError(message string, transportMode string) string {
	message = strings.TrimSpace(message)
	if !strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return message
	}

	replacements := []string{
		". Emit exactly one valid tool call or answer normally.",
		" Emit exactly one valid tool call or answer normally.",
		". Emit exactly one valid JSON tool object.",
		" Emit exactly one valid JSON tool object.",
	}
	for _, replacement := range replacements {
		message = strings.ReplaceAll(message, replacement, "")
	}
	if message == "" {
		message = parserRecoverySummaryMalformed + "."
	}
	if !strings.Contains(strings.ToLower(message), "native tool-call transport") {
		message = strings.TrimRight(message, ".") + ". Use the active native tool-call transport for tools, or answer in plain prose if the task is complete."
	}

	return message
}

func recoveryToolTransportMode(transport PlanExecutePlanningTransport) string {
	return DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: transport.SupportsNativeTools,
		ForceNativeTools:    transport.ForceNativeTools,
		TextualToolParser:   transport.TextualToolParser,
		ResponseFormatJSON:  transport.ResponseFormatJSON,
		TerminalOnly:        transport.TerminalOnly,
	}).ToolTransportMode()
}

func effectiveRecoveryCardAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, contract TurnPhaseContract) []string {
	if len(contract.AllowedTools) > 0 {
		return append([]string(nil), contract.AllowedTools...)
	}

	if len(contract.ActionPolicy.CallableToolNames) > 0 {
		return append([]string(nil), contract.ActionPolicy.CallableToolNames...)
	}

	if strings.TrimSpace(contract.PhaseID) == turnPhaseStaleAnchorRefresh {
		return workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)
	}

	if len(currentAllowedTools) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}

	if len(brief.AllowedTools) > 0 {
		return append([]string(nil), brief.AllowedTools...)
	}

	return nil
}

// ReadinessNudge carries the parameters that distinguish the three
// post-readiness nudge variants (child-packet boundary, delegated
// read-only answer ready, and direct read-only answer ready).
type ReadinessNudge struct {
	// Headline is the first line of the nudge.
	Headline string

	// EvidencePathLabel is the label shown before evidence paths
	// (e.g. "Packet-named paths: " or "Core evidence paths: ").
	// Must include the trailing space if a separator is needed.
	EvidencePathLabel string

	// EvidencePaths is the set of paths to display (unfiltered).
	EvidencePaths []string

	// Objective is the original question/objective string, used for
	// the closure decision rationale.
	Objective string

	// RuntimeBrief is the actual lane contract for ledger guidance.
	RuntimeBrief workerruntime.WorkerRuntimeBrief

	// Results are the collected tool results.
	Results []workspace.ToolResult

	// ContinuationScope is passed to postReadinessContinuationContract.
	ContinuationScope string

	// FooterLines are appended after the continuation contract.
	FooterLines []string

	// IncludeProbeSuggestions controls whether closureProbeSuggestionLine
	// is included. Only used by the child-packet variant.
	IncludeProbeSuggestions bool
}

// buildReadinessNudge produces the nudge text from the given parameters.
func buildReadinessNudge(ctx ReadinessNudge) string {
	evidencePaths := closurePromptEvidencePaths(ctx.EvidencePaths, 4)
	validity, _ := closureDecisionRationale(ctx.Objective, ctx.Results, evidencePaths)

	lines := []string{ctx.Headline}

	if validity != "" {
		lines = append(lines, "Why a final answer is now valid: "+validity)
	}

	if len(evidencePaths) > 0 {
		lines = append(lines, ctx.EvidencePathLabel+summarizePostChildEvidencePaths(evidencePaths, 3))
	}

	if ledger := readOnlyEvidenceLedgerGuidanceForBrief(ctx.RuntimeBrief, ctx.Results); ledger != "" {
		lines = append(lines, ledger)
	}

	if ctx.IncludeProbeSuggestions {
		if suggestion := closureProbeSuggestionLine(ctx.Results, 3); suggestion != "" {
			lines = append(lines, suggestion)
		}
	}

	lines = append(lines, postReadinessContinuationContract(ctx.ContinuationScope))
	lines = append(lines, ctx.FooterLines...)

	return strings.Join(lines, "\n")
}

type postReadinessToolContract struct {
	RequireSourceRefreshContract bool
	ReadinessAdvisory            bool
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
		return postReadinessToolContract{
			ReadinessAdvisory: true,
			Reason:            "read_only_readiness_advisory",
		}
	}

	return postReadinessToolContract{Reason: "none"}
}

// postReadinessContinuationContract returns advisory closeout guidance after
// readiness or a child packet. It must not describe read-only readiness as a
// permission gate; explicit source-refresh gates are selected by
// postReadinessToolContractForBrief.
func postReadinessContinuationContract(scope string) string {
	anchor := "prefer a symbol, branch, body, or narrow line-range probe over file-level raw reads"
	if strings.TrimSpace(scope) == "child_packet_available" {
		anchor = "anchor to a named path, symbol, discovered handle, branch, body, or narrow line-range probe"
	} else if strings.TrimSpace(scope) == "answer_readiness_reached" {
		anchor = "target a symbol, call/reference, branch, body, or narrow line-range probe tied to inspected paths"
	}

	lines := []string{
		"A final answer is available unless one exact unresolved claim remains.",
		"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy; " + anchor + ".",
		"The follow-up should name the missing claim and target a code-shaped token (symbol name, file path, or narrow line range), not paraphrase the original question.",
	}

	return strings.Join(lines, "\n")
}

func delegatedReadOnlyDiminishingReturnsNudge(objective string, results []workspace.ToolResult, followups int) string {
	if followups <= 0 || followups > 2 {
		return ""
	}

	// The readiness nudge already disclosed the advisory continuation shape.
	// Repeating that prose burns context for no behavior change. Emit a
	// ledger note instead and let the model decide strategy.
	paths := closurePromptEvidencePaths(delegatedClosureEvidencePaths(objective, results, 4), 3)
	line := "The evidence ledger already supports a final answer. Continue with tools only if you can name an exact material missing claim and a bounded source that would resolve it."

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
			candidates = textutil.AppendUnique(candidates, symbol)
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
	// contract; this is a ledger note, not another prose block telling the
	// model what was already said.
	paths := postChildClosureEvidencePaths(objective, results, 3)
	line := "The child-packet evidence ledger already supports a final answer. Continue with tools only if you can name an exact material missing claim and a bounded source that would resolve it."

	if len(paths) > 0 {
		line += " Packet paths: " + summarizePostChildEvidencePaths(paths, 3) + "."
	}

	return line
}

func delegatedClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max*2)
	for _, packet := range newToolResultEvidenceProjection(results).ReadOnlyEvidencePackets() {
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
		case strings.Contains(lower, workspace.ToolNameSearchFiles),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "produce the missing evidence or edit required by the gate"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "gather or edit the missing evidence"),
			strings.Contains(lower, workspace.ToolNameEditFile),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, workspace.ToolNameGoMoveDeclarations),
			strings.Contains(lower, workspace.ToolNameListFiles):
			return true
		default:
			return false
		}
	}))
	state.InvalidNextActions = textutil.Merge(state.InvalidNextActions, closurePromptInvalidActions(scope))

	return state
}

func closurePromptNextActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return []string{
			"final answer from the child packet evidence is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	default:
		return []string{
			"final answer from the evidence already gathered is available",
			"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
		}
	}
}

func closurePromptInvalidActions(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "child_packet_available":
		return nil
	default:
		return []string{
			"broad rediscovery after answer readiness without one exact missing claim and one exact probe",
			"README/docs or repo-wide search outside named implementation paths after answer readiness",
		}
	}
}

const modelCallPrefixHashVersion = "v1-leading-system-tools-parser"

func modelCallPrefixDiagnostics(prompt []llama.ChatMessage, opts llama.ChatOptions, parserContract string) map[string]any {
	prefix := leadingSystemPromptPrefix(prompt)
	toolSchemaHash := stableHash(opts.Tools)
	payload := map[string]any{
		"prefix_hash_version":          modelCallPrefixHashVersion,
		"prefix_hash":                  stableHash([]any{modelCallPrefixHashVersion, prefix, toolSchemaHash, opts.ToolChoice, parserContract}),
		"prompt_prefix_message_count":  len(prefix),
		"tool_schema_hash":             toolSchemaHash,
		"prefix_hash_components":       []string{"leading_system_messages", "tool_schemas", "tool_choice", "parser_contract"},
		"prefix_change_reason_fields":  []string{"system_prompt_changed", "tool_schema_changed", "tool_choice_changed", "parser_contract_changed"},
		"prefix_cache_metadata_scope":  "telemetry_only",
		"prefix_cache_signal_present":  false,
		"prefix_cache_signal_provider": "",
	}

	return payload
}

func leadingSystemPromptPrefix(prompt []llama.ChatMessage) []llama.ChatMessage {
	var prefix []llama.ChatMessage

	for _, msg := range prompt {
		if msg.Role != "system" {
			break
		}

		prefix = append(prefix, llama.ChatMessage{Role: msg.Role, Content: msg.Content})
	}

	return prefix
}

func stableHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte("{}")
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func (r *Runner) executeWorkspaceWithPreimage(ctx context.Context, store *PreimageCheckpointStore, req workspace.ToolRequest) workspace.ToolResult {
	if result, blocked := snapshotWritePreimages(store, req); blocked {
		return result
	}

	result := r.Workspace.Execute(ctx, req)

	return annotateWritePreimagePayload(result, store, req)
}

func annotateWritePreimagePayload(result workspace.ToolResult, store *PreimageCheckpointStore, req workspace.ToolRequest) workspace.ToolResult {
	if store == nil || strings.TrimSpace(result.Error) != "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return result
	}

	paths, classified, err := workspace.WritePreimagePaths(req)
	if err != nil || !classified || len(paths) == 0 {
		return result
	}

	payload := writePreimagePayload{Paths: make([]writePreimagePath, 0, len(paths))}

	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		existed, ok := store.Existed(normalized)
		if !ok {
			continue
		}

		payload.Paths = append(payload.Paths, writePreimagePath{Path: normalized, Exists: existed})
	}

	if len(payload.Paths) == 0 {
		return result
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return result
	}

	result.PayloadType = writePreimagePayloadType
	result.Payload = encoded

	return result
}

func snapshotWritePreimages(store *PreimageCheckpointStore, req workspace.ToolRequest) (workspace.ToolResult, bool) {
	if store == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return workspace.ToolResult{}, false
	}

	paths, classified, err := workspace.WritePreimagePaths(req)
	if err != nil {
		return workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Preimage checkpoint failed",
			Error:   fmt.Sprintf("could not classify %s preimage paths: %v", strings.TrimSpace(req.Tool), err),
		}, true
	}

	if !classified {
		return workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Preimage checkpoint failed",
			Error:   fmt.Sprintf("workspace write tool %s has no preimage path classification", strings.TrimSpace(req.Tool)),
		}, true
	}

	for _, path := range paths {
		if err := store.Snapshot(path); err != nil {
			return workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Preimage checkpoint failed",
				Error:   fmt.Sprintf("could not snapshot %s before %s: %v", path, strings.TrimSpace(req.Tool), err),
			}, true
		}
	}

	return workspace.ToolResult{}, false
}

type runnerTurnRecorder struct {
	runner                    *Runner
	runState                  *runnerRunState
	activeToolCall            *llama.ToolCall
	onToolResult              func(ToolResultEvent)
	collectedResults          *[]workspace.ToolResult
	turnMessages              *[]llama.ChatMessage
	lastToolKey               *string
	lastToolResult            **workspace.ToolResult
	consecutiveRejectedAction *int
	consecutiveNoProgress     *int
	metrics                   *runnerMetrics
	declarationMovesForbidden bool
}

type runnerRecordOptions struct {
	TrackProgress           bool
	ResetRejected           bool
	CountRejected           bool
	IncrementNoProgress     bool
	IncrementRejectedStreak bool
	SetLastToolKey          bool
	SuppressModelMessage    bool
}

func (r runnerTurnRecorder) record(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	opts.SetLastToolKey = true
	r.recordWithOptions(step, req, result, reason, opts)
}

func (r runnerTurnRecorder) recordWithOptions(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	priorResults := r.snapshotResults()
	result = resultWithRejectedRequestTarget(req, result)
	if r.collectedResults != nil {
		*r.collectedResults = append(*r.collectedResults, result)
	}
	if r.runner != nil && r.runner.StateStore != nil {
		r.runner.StateStore.RecordResult(req, result)
	}

	if opts.SetLastToolKey && r.lastToolKey != nil {
		*r.lastToolKey = toolRequestKey(req)
	}

	if r.lastToolResult != nil {
		resultCopy := result

		*r.lastToolResult = &resultCopy
	}

	if opts.ResetRejected && r.consecutiveRejectedAction != nil {
		*r.consecutiveRejectedAction = 0
	}

	if opts.IncrementRejectedStreak && r.consecutiveRejectedAction != nil {
		*r.consecutiveRejectedAction++
	}

	if opts.CountRejected && r.metrics != nil {
		r.metrics.RejectedActions++
	}

	if opts.TrackProgress && r.consecutiveNoProgress != nil {
		switch r.resultProgress(req, result, priorResults) {
		case runnerResultProgressMade:
			*r.consecutiveNoProgress = 0
			if r.metrics != nil {
				r.metrics.ProgressTurns++
			}
		case runnerResultProgressStalled:
			*r.consecutiveNoProgress++
		}
	} else if opts.IncrementNoProgress && r.consecutiveNoProgress != nil {
		*r.consecutiveNoProgress++
	}

	if r.onToolResult != nil && r.runner != nil {
		r.onToolResult(ToolResultEvent{Step: step, Agent: r.runner.agentName(), Request: req, Result: result})
	}

	if r.turnMessages != nil && !opts.SuppressModelMessage {
		modelResult := r.modelFacingToolResult(step, req, result, reason)

		*r.turnMessages = append(*r.turnMessages, toolResultMessage(r.activeToolCall, modelResult))
	}

	if r.runner != nil {
		r.runner.emitNextActionState(step, reason, req, result, r.snapshotResults(), r.declarationMovesForbidden)

		if strings.TrimSpace(result.Error) != "" {
			packet := buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(r.runner.agentName(), step, reason, req, result, r.snapshotResults(), r.declarationMovesForbidden, r.runner.RuntimeBrief, r.runner.currentAllowedTools, r.runner.currentPlanningTransport())
			if len(packet.DoNotRepeat) > 0 || len(packet.RequiredNext) > 0 || strings.TrimSpace(packet.ResultError) != "" {
				if data, err := json.Marshal(packet); err == nil {
					r.runner.emitEvent(RunEvent{Role: "failure_feedback", Content: string(data)})
				}
			}
		}
	}
}

type runnerResultProgress string

const (
	runnerResultProgressStalled runnerResultProgress = "stalled"
	runnerResultProgressMade    runnerResultProgress = "made"
	runnerResultProgressNeutral runnerResultProgress = "neutral"
)

func (r runnerTurnRecorder) resultProgress(req workspace.ToolRequest, result workspace.ToolResult, priorResults []workspace.ToolResult) runnerResultProgress {
	if r.runner != nil {
		switch implementationTargetWriteReadyReadProgressForResult(r.runner.RuntimeBrief, req, result, priorResults) {
		case implementationTargetWriteReadyReadProgressAddsSource:
			return runnerResultProgressMade
		case implementationTargetWriteReadyReadProgressDuplicate:
			return runnerResultProgressNeutral
		}
	}

	if toolResultMadeProgress(req, result) {
		return runnerResultProgressMade
	}

	return runnerResultProgressStalled
}

func resultWithRejectedRequestTarget(req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return result
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
		return result
	}

	if strings.TrimSpace(result.Output) == "" {
		result.Output = "Rejected write target: `" + path + "`"
	} else {
		result.Output = strings.TrimRight(result.Output, "\n") + "\nRejected write target: `" + path + "`"
	}

	return result
}

func (r runnerTurnRecorder) modelFacingToolResult(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string) workspace.ToolResult {
	if strings.TrimSpace(reason) == parserRecoveryRecordReason && r.activeToolCall != nil {
		if toolName := strings.TrimSpace(r.activeToolCall.Function.Name); toolName != "" {
			modelResult := result

			modelResult.Tool = toolName
			if r.runner != nil {
				modelResult.Error = modelFacingToolParseError(modelResult.Error, recoveryToolTransportMode(r.runner.currentPlanningTransport()))
			}
			return modelResult
		}
	}

	if r.runner == nil || strings.TrimSpace(result.Error) == "" || strings.TrimSpace(reason) != "tool_rejected" {
		return result
	}

	packet := buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(
		r.runner.agentName(),
		step,
		reason,
		req,
		result,
		r.snapshotResults(),
		r.declarationMovesForbidden,
		r.runner.RuntimeBrief,
		r.runner.currentAllowedTools,
		r.runner.currentPlanningTransport(),
	)
	feedback := renderModelFacingFailureFeedback(packet)

	if feedback == "" {
		return result
	}

	modelResult := result

	modelResult.Error = appendRecoveryCardToMessage(modelResult.Error, feedback)

	return modelResult
}

func renderModelFacingFailureFeedback(packet runnerFailureFeedbackPacket) string {
	var b strings.Builder

	b.WriteString("Recovery feedback")
	writeModelFacingFeedbackList(&b, "do_not_repeat", packet.DoNotRepeat, 5)
	writeModelFacingFeedbackList(&b, "required_next", packet.RequiredNext, 5)
	writeModelFacingFeedbackList(&b, "valid_next", packet.ValidNext, 5)
	writeModelFacingFeedbackList(&b, "evidence_status", packet.EvidenceStatus, 3)

	if card := strings.TrimSpace(packet.RecoveryCard); card != "" {
		b.WriteString("\n")
		b.WriteString(card)
	}

	feedback := strings.TrimSpace(b.String())
	if feedback == "Recovery feedback" {
		return ""
	}

	return feedback
}

func writeModelFacingFeedbackList(b *strings.Builder, label string, values []string, limit int) {
	values = nonEmptyRunnerStrings(values, limit)
	if len(values) == 0 {
		return
	}

	b.WriteString("\n")
	b.WriteString(label)
	b.WriteString(":")

	for _, value := range values {
		b.WriteString("\n- ")
		b.WriteString(value)
	}
}

func (r runnerTurnRecorder) snapshotResults() []workspace.ToolResult {
	if r.collectedResults == nil {
		return nil
	}

	return *r.collectedResults
}

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
	if LanePolicyForBrief(brief).skipRecoveryNudge {
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

	missing := missingRequiredEditPaths(in.brief.RequiredEditPaths, newToolResultEvidenceProjection(in.results).ChangedPathSet())
	if len(missing) == 0 {
		return "", false
	}

	var b strings.Builder

	if strings.TrimSpace(in.brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		b.WriteString("Recovery nudge — this plan-execute planner turn still has required edit path(s) without successful `submit_plan` coverage: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(".\n")

		if planThenExecuteSubmitCorrectionActive(in.brief, in.results) {
			if planThenExecuteSubmitCorrectionBlockerAllowed(in.brief, in.results) {
				b.WriteString("Do not answer with partial findings. Submit-correction mode accepts a parser-readable `submit_plan`, or `<<QUESTION>>` only with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied` and do not repeat the rejected tool call.\n")
			} else {
				b.WriteString("Do not answer with partial findings. Submit-correction mode has exactly one valid outcome: parser-readable `submit_plan`. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not repeat the rejected tool call.\n")
			}

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
		b.WriteString("Do not answer with partial findings. For existing source/test required paths, the allowed write outcome is a bounded `edit_file` call using old_text/new_text or start_line/end_line/content, or an `apply_patch` envelope. Do not use `write_file` or `append_file` for existing source/test paths; reserve full-body writes for missing/new files and non-source deliverables. Otherwise answer with <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

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
	if !debugLogContractApplies(in.brief, in.results) {
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
	return workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		hasSuccessfulTargetRead(results, brief.TargetRefs)
}

func evidenceFirstTargetInspectionRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !evidenceFirstTargetInspectionActive(in.brief, in.results) {
		return "", false
	}

	target := textutil.FirstNonEmpty(in.brief.TargetRefs...)
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
	case tool == workspace.ToolNameListFiles || tool == "list_dir":
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
	case tool == workspace.ToolNameReadFile:
		path := strings.TrimSpace(in.req.Path)
		if path != "" {
			b.WriteString("The repeated read should become a smaller window or a structured probe tied to a named gap on ")
			b.WriteString(path)
			b.WriteString(".\n")
		} else {
			b.WriteString("The repeated read needs a concrete discovered path and a named gap.\n")
		}
	case tool == workspace.ToolNameSearchContent:
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
	return workerruntime.TaskFamilyEvidenceFirstMutation(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
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
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles:
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

func repeatedDiagnosticLogReadFileBackfill(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !IsDiagnosticLogArtifactPath(path) {
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
	path = NormalizeRunnerPath(path)
	if path == "" {
		return nil
	}

	var windows []diagnosticLogReadWindow

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
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
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !IsDiagnosticLogArtifactPath(path) {
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
	for _, readPath := range ReadPathsFromToolResult(result) {
		if NormalizeRunnerPath(readPath) == path {
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
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !IsDiagnosticLogArtifactPath(path) {
		return "", "", false
	}

	if strings.TrimSpace(last.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(last.Error) != "" {
		return "", "", false
	}

	for _, readPath := range ReadPathsFromToolResult(last) {
		if NormalizeRunnerPath(readPath) != path {
			continue
		}

		return "Repeated diagnostic log read rejected",
			"this exact diagnostic log read already returned evidence. Do not request the same log window again. If a missing fact remains, request a different bounded `start_line`/`end_line` window tied to that missing timestamp, event, or stop reason; inspect a named source path only if the log evidence points to it; otherwise answer from the gathered evidence.",
			true
	}

	return "", "", false
}

func repeatedDiagnosticLogReadFilePreviouslyReadRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return "", "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !IsDiagnosticLogArtifactPath(path) {
		return "", "", false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
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

const loopPolicyInvariantRepeatedCall = "loop.repeated_call"

type fingerprintTrackingState struct {
	lastFingerprint string
	identicalRun    int
	counts          map[string]int
	samePathCounts  map[string]int
	samePathNudged  map[string]bool
	nudgeFired      map[string]bool
	zeroHit         map[string]string
	zeroHitFamily   map[string]zeroHitFamilyState
	rejected        map[string]string
	// rejectedRetries counts how many times a fingerprint has been
	// retried after a prior rejection. Nudge on first retry, force-answer
	// on the second — matching the nudge-then-block pattern.
	rejectedRetries map[string]int
}

type zeroHitFamilyState struct {
	Count      int
	Summary    string
	NudgeFired bool
}

type postChildBudgetState struct {
	readCalls        int
	readBudget       int
	evidencePaths    []string
	readFamilies     map[string]int
	familyNudgeFired map[string]bool
}

type runnerRequestBudgetState struct {
	fingerprint fingerprintTrackingState
	postChild   postChildBudgetState

	// writeReadyNudgeActive is set by the write-ready nudge path in
	// runner.go when the model is in a write-ready-but-not-writing
	// state. When true, the rejection retry budget defers to the
	// write-ready escalation contract instead of force-answering on the
	// first rejection retry.
	writeReadyNudgeActive bool

	broadEvidenceDelegationRequired  bool
	broadEvidenceDelegationSatisfied bool
	readOnlyChildTaskSatisfied       bool
	synthesisNudgeFired              bool
	readCallCount                    int
	readCallsSinceWrite              int
	scopedReadRejections             map[string]int
	recoveryBrief                    workerruntime.WorkerRuntimeBrief
	recoveryResults                  []workspace.ToolResult
}

func (s runnerRequestBudgetState) postChildActive() bool {
	return s.readOnlyChildTaskSatisfied || s.broadEvidenceDelegationSatisfied
}

func (s *runnerRequestBudgetState) setRecoveryContext(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if s == nil {
		return
	}
	s.recoveryBrief = brief
	s.recoveryResults = append([]workspace.ToolResult(nil), results...)
}

type runnerRequestBudgetDecision struct {
	Handled           bool
	RejectSummary     string
	RejectError       string
	RejectStopReason  string
	ForceAnswerPrompt string
	ForceAnswerReason string
	HideTool          string
	Nudge             string
	EventRole         string
	EventContent      string
	Policy            runnerLoopPolicyDecision
}

type runnerLoopPolicyDecision struct {
	ContractID  autonomy.ContractPackID
	InvariantID string
	Decision    autonomy.ToolPolicyDecision
	ReasonCode  string
}

type budgetDecisionContext struct {
	controller     *runnerTurnController
	stepNumber     int
	activeToolCall *llama.ToolCall
	req            workspace.ToolRequest
}

type budgetDecisionResult struct {
	shouldFinish   bool
	shouldContinue bool
	answer         string
	err            error
	reason         string
}

func (d runnerRequestBudgetDecision) apply(ctx budgetDecisionContext) budgetDecisionResult {
	if !d.Handled {
		return budgetDecisionResult{}
	}

	if ctx.controller != nil &&
		ctx.controller.autonomyRecorder != nil &&
		terminalArtifactRequiredRejectionSummary(d.RejectSummary) {
		emitToolAuthorizationSurfacePolicyResult(ctx.controller.autonomyRecorder, ctx.req, autonomy.PolicyReject)
	}

	if ctx.controller != nil && ctx.controller.autonomyRecorder != nil && d.Policy.InvariantID != "" {
		ctx.controller.autonomyRecorder.emitPolicyResult(ctx.req, runnerAutonomyPolicyEventInput{
			Decision:    d.Policy.Decision,
			PackID:      d.Policy.ContractID,
			InvariantID: d.Policy.InvariantID,
			ReasonCode:  d.Policy.ReasonCode,
			Result:      runnerLoopPolicyResultLabel(d.Policy.Decision),
			ErrorClass:  "loop_policy",
			Source:      "runner_loop_policy",
		})
	}

	if d.Nudge != "" {
		*ctx.controller.turnMessages = append(*ctx.controller.turnMessages, llama.ChatMessage{Role: "system", Content: d.Nudge})
	}

	if d.EventRole != "" {
		ctx.controller.runner.emitEvent(RunEvent{Role: d.EventRole, Content: textutil.FirstNonEmpty(d.EventContent, d.Nudge)})
	}

	if d.ForceAnswerPrompt != "" {
		answer, err := ctx.controller.completeWithoutMoreToolsWithReason(d.ForceAnswerPrompt)

		return budgetDecisionResult{
			shouldFinish: true,
			answer:       answer,
			err:          err,
			reason:       d.ForceAnswerReason,
		}
	}

	if d.RejectSummary != "" {
		answer, err, done := ctx.controller.rejectTool(ctx.stepNumber, ctx.activeToolCall, ctx.req, d.RejectSummary, d.RejectError)
		if done {
			return budgetDecisionResult{
				shouldFinish: true,
				answer:       answer,
				err:          err,
				reason:       d.RejectStopReason,
			}
		}

		return budgetDecisionResult{shouldContinue: true}
	}

	return budgetDecisionResult{}
}

func runnerLoopPolicyResultLabel(decision autonomy.ToolPolicyDecision) string {
	switch decision {
	case autonomy.PolicyObserve:
		return "observed"
	case autonomy.PolicyReject:
		return "rejected"
	case autonomy.PolicyStop:
		return "stopped"
	default:
		return strings.TrimSpace(string(decision))
	}
}

func newRunnerRequestBudgetState(postChildReadBudget int) runnerRequestBudgetState {
	return runnerRequestBudgetState{
		fingerprint: fingerprintTrackingState{
			counts:          make(map[string]int),
			samePathCounts:  make(map[string]int),
			samePathNudged:  make(map[string]bool),
			nudgeFired:      make(map[string]bool),
			zeroHit:         make(map[string]string),
			zeroHitFamily:   make(map[string]zeroHitFamilyState),
			rejected:        make(map[string]string),
			rejectedRetries: make(map[string]int),
		},
		scopedReadRejections: make(map[string]int),
		postChild: postChildBudgetState{
			readBudget:       postChildReadBudget,
			readFamilies:     make(map[string]int),
			familyNudgeFired: make(map[string]bool),
		},
	}
}

func attachLoopPolicyDecision(decision runnerRequestBudgetDecision) runnerRequestBudgetDecision {
	if !decision.Handled {
		return decision
	}
	policyDecision := autonomy.PolicyObserve
	if decision.ForceAnswerReason != "" || decision.RejectSummary != "" {
		policyDecision = autonomy.PolicyStop
		if decision.RejectSummary != "" {
			policyDecision = autonomy.PolicyReject
		}
	}
	reason := strings.TrimSpace(decision.EventRole)
	if reason == "" {
		reason = strings.TrimSpace(decision.ForceAnswerReason)
	}
	if reason == "" {
		reason = strings.TrimSpace(decision.RejectStopReason)
	}
	decision.Policy = runnerLoopPolicyDecision{
		ContractID:  autonomy.ContractPackLoopRepeatedCall,
		InvariantID: loopPolicyInvariantRepeatedCall,
		Decision:    policyDecision,
		ReasonCode:  reason,
	}
	return decision
}

const samePathFixationNudgeAt = 5

// samePathFingerprint collapses different query parameters into a
// same-tool + same-path identifier, catching file fixation even when
// the exact request arguments vary.
func samePathFingerprint(req workspace.ToolRequest) string {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return ""
	}

	path = filepath.ToSlash(filepath.Clean(path))

	return req.Tool + "|path:" + path
}

func (s *runnerRequestBudgetState) noteFingerprint(currentFingerprint string) int {
	if currentFingerprint != "" && currentFingerprint == s.fingerprint.lastFingerprint {
		s.fingerprint.identicalRun++
	} else {
		s.fingerprint.identicalRun = 1
		s.fingerprint.lastFingerprint = currentFingerprint
	}

	if currentFingerprint != "" {
		s.fingerprint.counts[currentFingerprint]++
	}

	return s.fingerprint.identicalRun
}

func (s *runnerRequestBudgetState) samePathFixationDecision(req workspace.ToolRequest, evidenceAvailable bool) runnerRequestBudgetDecision {
	fp := samePathFingerprint(req)
	if fp == "" || IsDiagnosticLogArtifactPath(req.Path) {
		return runnerRequestBudgetDecision{}
	}

	s.fingerprint.samePathCounts[fp]++

	count := s.fingerprint.samePathCounts[fp]

	if count < samePathFixationNudgeAt {
		return runnerRequestBudgetDecision{}
	}

	path := cleanToolRequestPathDisplay(req.Path)
	if s.fingerprint.samePathNudged[fp] {
		return runnerRequestBudgetDecision{}
	}

	s.fingerprint.samePathNudged[fp] = true

	action := "switch to a materially different evidence path, use one structured source-body probe for a named missing claim, or provide the final answer if the gathered evidence supports it"
	if evidenceAvailable {
		action = "a final answer is available if the gathered evidence supports it; otherwise use one structured source-body probe only if you can name the exact missing claim it will resolve"
	}

	return runnerRequestBudgetDecision{
		Handled: true,
		Nudge: fmt.Sprintf(
			"You have called %s on %s %d times this turn with varied arguments. Stop walking the same file by adjacent chunks; %s. Do not request another adjacent range from the same file.",
			req.Tool,
			path,
			count,
			action,
		),
		EventRole: "same_path_fixation_nudge",
	}
}

func (s *runnerRequestBudgetState) fingerprintDecision(policy RunPolicy, req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if currentFingerprint == "" {
		return runnerRequestBudgetDecision{}
	}

	if decision := s.terminalArtifactRejectedRetryDecision(req, currentFingerprint); decision.Handled {
		return decision
	}

	if policy.FingerprintNudgeAt > 0 {
		count := s.fingerprint.counts[currentFingerprint]
		nudgeFired := s.fingerprint.nudgeFired[currentFingerprint]

		if count >= policy.FingerprintNudgeAt && !nudgeFired {
			s.fingerprint.nudgeFired[currentFingerprint] = true

			return runnerRequestBudgetDecision{
				Handled:   true,
				Nudge:     fmt.Sprintf("You have called %s with the same arguments %d times this turn (not consecutively). Summarize what you learned from those calls and pick a materially different next step, or answer with the evidence you already have. Any further identical call will be blocked.", req.Tool, count),
				EventRole: "fingerprint_nudge",
			}
		}

		if count > policy.FingerprintNudgeAt && nudgeFired {
			return runnerRequestBudgetDecision{
				Handled:           true,
				EventRole:         "fingerprint_hard_block",
				EventContent:      fmt.Sprintf("%s repeated %d times; hard-blocking", req.Tool, count),
				ForceAnswerReason: "fingerprint_hard_block_exhausted",
				ForceAnswerPrompt: fmt.Sprintf("You called %s with identical arguments %d times after being warned. Further tools are not available. Provide your final answer from the evidence already gathered. If the repeated request reflects one unsupported claim, state that uncertainty after the supported answer with the smallest useful next action.", req.Tool, count),
			}
		}
	}

	if priorSummary, seen := s.fingerprint.zeroHit[currentFingerprint]; seen {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Zero-hit retry blocked",
			RejectError:      fmt.Sprintf("this exact %s call already returned no results earlier this turn (\"%s\"). Repeating it will not find new matches. Broaden or change the pattern, search a different scope, or answer with the evidence you have.", req.Tool, priorSummary),
			RejectStopReason: "zero_hit_retry_budget_exhausted",
		}
	}

	if decision := s.zeroHitFamilyDecision(req); decision.Handled {
		return decision
	}

	if decision := s.rejectedFingerprintDecision(req, currentFingerprint); decision.Handled {
		return decision
	}

	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteZeroHitSearch(req workspace.ToolRequest, currentFingerprint string, result workspace.ToolResult) {
	summary := truncForRetryReason(result.Summary)
	if strings.TrimSpace(currentFingerprint) != "" {
		s.fingerprint.zeroHit[currentFingerprint] = summary
	}

	family := zeroHitSearchFamilyKey(req)
	if family == "" {
		return
	}

	state := s.fingerprint.zeroHitFamily[family]
	state.Count++

	state.Summary = summary
	s.fingerprint.zeroHitFamily[family] = state
}

func (s *runnerRequestBudgetState) zeroHitFamilyDecision(req workspace.ToolRequest) runnerRequestBudgetDecision {
	family := zeroHitSearchFamilyKey(req)
	if family == "" {
		return runnerRequestBudgetDecision{}
	}

	state, seen := s.fingerprint.zeroHitFamily[family]
	if !seen {
		return runnerRequestBudgetDecision{}
	}

	if state.Count >= 2 {
		return runnerRequestBudgetDecision{
			Handled:          true,
			EventRole:        "zero_hit_family_block",
			RejectSummary:    "Zero-hit search family blocked",
			RejectError:      fmt.Sprintf("closely related %s searches already returned no results this turn (latest: %q). Repeating symbol or spelling variants in the same search family is not progress. Switch to a source-body read from an existing candidate, use a broader semantic/context query, or answer with the evidence already gathered.", req.Tool, state.Summary),
			RejectStopReason: "zero_hit_family_budget_exhausted",
		}
	}

	if !state.NudgeFired {
		state.NudgeFired = true
		s.fingerprint.zeroHitFamily[family] = state

		return runnerRequestBudgetDecision{
			Handled:      true,
			EventRole:    "zero_hit_family_nudge",
			EventContent: fmt.Sprintf("zero-hit search family %q seen once; nudging before another related miss", family),
			Nudge: fmt.Sprintf(
				"A closely related %s search already returned no results this turn (%q). Before trying another spelling or symbol variant, choose a materially different probe: read a localized candidate, use semantic/context navigation, or answer with the evidence you have.",
				req.Tool, state.Summary),
		}
	}

	return runnerRequestBudgetDecision{}
}

func zeroHitSearchFamilyKey(req workspace.ToolRequest) string {
	if !workspace.IsSearchToolName(req.Tool) {
		return ""
	}

	terms := zeroHitSearchFamilyTerms(strings.Join([]string{req.Pattern, req.Query, req.Command}, " "))
	if len(terms) < 2 {
		return ""
	}

	path := normalizePostChildPath(req.Path)

	return strings.TrimSpace(req.Tool) + ":" + path + ":" + strings.Join(terms, ":")
}

func zeroHitSearchFamilyTerms(text string) []string {
	words := splitZeroHitSearchWords(text)
	if len(words) == 0 {
		return nil
	}

	hasKey := requestBudgetContainsString(words, "key")
	hasArrow := requestBudgetContainsString(words, "arrow")

	if hasKey && hasArrow {
		return []string{"key", "arrow"}
	}

	var terms []string

	for _, word := range words {
		switch word {
		case "up", "down", "left", "right", "true", "false":
			continue
		}

		terms = textutil.AppendUnique(terms, word)
		if len(terms) >= 3 {
			break
		}
	}

	return terms
}

func splitZeroHitSearchWords(text string) []string {
	var (
		words []string
		b     strings.Builder
	)

	flush := func() {
		if b.Len() == 0 {
			return
		}

		word := strings.ToLower(b.String())
		b.Reset()

		if len(word) < 3 {
			return
		}

		words = textutil.AppendUnique(words, word)
	}
	prevLower := false

	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			if prevLower {
				flush()
			}

			b.WriteRune(r + ('a' - 'A'))

			prevLower = false

			continue
		}

		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)

			prevLower = true

			continue
		}

		if r >= '0' && r <= '9' {
			b.WriteRune(r)

			prevLower = false

			continue
		}

		flush()

		prevLower = false
	}

	flush()

	return words
}

func requestBudgetContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func (s *runnerRequestBudgetState) rejectedFingerprintDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}

	if decision := s.terminalArtifactRejectedRetryDecision(req, currentFingerprint); decision.Handled {
		return decision
	}

	if priorSummary, ok := s.fingerprint.rejected[currentFingerprint]; ok {
		if postReadinessContinuationRejectionSummary(priorSummary) {
			delete(s.fingerprint.rejected, currentFingerprint)
			delete(s.fingerprint.rejectedRetries, currentFingerprint)

			return runnerRequestBudgetDecision{}
		}

		// When write-ready nudge escalation is active, defer to that
		// contract rather than force-answering on rejection retry.
		if s.writeReadyNudgeActive {
			s.fingerprint.rejectedRetries[currentFingerprint]++

			return runnerRequestBudgetDecision{
				Handled:   true,
				EventRole: "rejection_retry_deferred_to_write_ready",
				Nudge: fmt.Sprintf(
					"You retried a rejected %s call. The write-ready contract is active — either write to a sourced target path or name the exact missing claim and probe. The rejection retry budget is deferred until the write-ready escalation completes.",
					req.Tool),
			}
		}

		retries := s.fingerprint.rejectedRetries[currentFingerprint] + 1

		s.fingerprint.rejectedRetries[currentFingerprint] = retries

		if retries == 1 {
			return runnerRequestBudgetDecision{
				Handled:      true,
				EventRole:    "rejection_retry_nudge",
				EventContent: fmt.Sprintf("model retried a previously rejected %s call (%s) — nudging", req.Tool, priorSummary),
				Nudge: fmt.Sprintf(
					"You retried a %s request that was already rejected this turn (%q). The rejection reason still applies — repeating the same call will not change the outcome. Either take a materially different approach, or answer with the evidence you already have.",
					req.Tool, priorSummary),
			}
		}

		return runnerRequestBudgetDecision{
			Handled:           true,
			EventRole:         "rejection_retry_blocked",
			EventContent:      fmt.Sprintf("model retried a previously rejected %s call %d times (%s) — forcing answer with the evidence already gathered", req.Tool, retries, priorSummary),
			ForceAnswerReason: "rejection_retry_blocked",
			ForceAnswerPrompt: fmt.Sprintf("You retried a %s request that was already rejected %d times this turn and were warned. Further tools are not available. Provide your final answer from the evidence already gathered.", req.Tool, retries),
		}
	}

	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) terminalArtifactRejectedRetryDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}

	priorSummary, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !terminalArtifactRequiredRejectionSummary(priorSummary) {
		return runnerRequestBudgetDecision{}
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	return runnerRequestBudgetDecision{
		Handled:      true,
		EventRole:    "terminal_artifact_retry_recovery",
		EventContent: fmt.Sprintf("model retried %s after terminal artifact recovery rejected it", tool),
		Nudge:        terminalArtifactRejectedRetryNudge(tool, priorSummary, s.recoveryBrief, s.recoveryResults),
	}
}

func terminalArtifactRejectedRetryNudge(tool string, priorSummary string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(priorSummary) == "Corrected submit_plan required" {
		if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
			return fmt.Sprintf(
				"The repeated %s request was already rejected because submit-correction mode requires a corrected terminal plan artifact. Emit the currently allowed terminal artifact (`submit_plan`) now, or answer `<<QUESTION>>:` only with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts. Do not answer `Decision: source-satisfied` and do not report the rejected request as a blocker.",
				tool,
			)
		}
		return fmt.Sprintf(
			"The repeated %s request was already rejected because submit-correction mode requires a corrected terminal plan artifact. Emit the currently allowed terminal artifact (`submit_plan`) now. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not report the rejected request as a blocker.",
			tool,
		)
	}

	return fmt.Sprintf(
		"The repeated %s request was already rejected because the current planner phase requires a terminal plan artifact. Do not report that rejected request as a blocker. Emit the currently allowed terminal artifact (`submit_plan`) now, or use `Decision: source-satisfied` / `<<QUESTION>>:` only for a true terminal exception grounded in the evidence already gathered.",
		tool,
	)
}

func terminalArtifactRequiredRejectionSummary(summary string) bool {
	summary = strings.TrimSpace(strings.TrimSuffix(summary, " (replayed same blocker)"))
	switch summary {
	case "Plan-execute submit_plan required",
		"Corrected submit_plan required",
		"Plan submission required":
		return true
	default:
		return false
	}
}

func (s *runnerRequestBudgetState) earlyRejectedFingerprintDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}

	priorSummary, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !earlyRejectedFingerprintSummary(priorSummary) {
		return runnerRequestBudgetDecision{}
	}

	return s.rejectedFingerprintDecision(req, currentFingerprint)
}

func earlyRejectedFingerprintSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	switch summary {
	case "Plan-execute submit_plan required",
		"Tool not available in current runtime contract",
		"Tool hidden by active contract",
		"Write-only recovery blocked non-write tool",
		"Repeated range-less read_file redirected to bounded source request",
		"Repeated range-less read_file rejected":
		return true
	default:
		return false
	}
}

func normalizedHarnessRejectionSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	return strings.TrimSpace(strings.TrimSuffix(summary, " (replayed same blocker)"))
}

func postReadinessContinuationRejectionSummary(summary string) bool {
	summary = normalizedHarnessRejectionSummary(summary)

	return strings.HasPrefix(summary, "Post-readiness follow-up ") ||
		strings.HasPrefix(summary, "Post-readiness named_gap ") ||
		strings.HasPrefix(summary, "Post-readiness search ") ||
		strings.HasPrefix(summary, "Post-readiness exact probe ") ||
		strings.HasPrefix(summary, "Post-child follow-up ")
}

func (s *runnerRequestBudgetState) explorationDecision(r *Runner, req workspace.ToolRequest, results []workspace.ToolResult) runnerRequestBudgetDecision {
	var decision runnerRequestBudgetDecision

	if !s.broadEvidenceDelegationRequired {
		if nudge := r.explorationDelegationNudge(s.readCallsSinceWrite, results); nudge != "" {
			s.broadEvidenceDelegationRequired = true
			decision = runnerRequestBudgetDecision{
				Handled:   true,
				Nudge:     nudge,
				EventRole: "exploration_task_delegation_nudge",
			}
		}
	}

	if s.broadEvidenceDelegationRequired && !s.broadEvidenceDelegationSatisfied && workspace.IsBroadInspectionRequest(req) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Broad exploration after delegation nudge blocked",
			RejectError:      "broad inspection has already been nudged on this turn. Answer from the evidence gathered, or take one narrower next step naming the exact missing claim it closes.",
			RejectStopReason: "exploration_broad_inspection_after_nudge_blocked",
		}
	}

	mutationParent := runnerExplorationParentIsMutation(r)

	if s.readOnlyChildTaskSatisfied && strings.TrimSpace(req.Tool) == "task" && !isWriteChildMode(req.Mode) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Additional read_only child task blocked",
			RejectError:      "this turn already has a successful read_only child evidence packet. Continue from that packet with at most a couple of targeted follow-ups, or answer with the current evidence.",
			RejectStopReason: "duplicate_read_only_child_task_blocked",
		}
	}

	if s.postChildActive() {
		if decision := s.postChildGuidanceDecision(req, mutationParent); decision.Handled {
			return decision
		}
	}

	return decision
}

// runnerExplorationParentIsMutation reports whether the parent lane
// observing this explorationDecision call has a mutation brief AND is
// itself the parent (not a child:* delegated agent). Mutation parents
// consume child packets to commit a WRITE — the child's evidence is a
// hint, not a hard contract. Suppressing the post-child blocks for
// these parents lets them broaden/probe outside the packet's evidence
// paths when their own RequiredEditPaths need it. The completion gate
// owns mutation closure; doom_loop_guard + MaxToolSteps own the
// runaway-exploration ceiling. Same logic as
// postReadinessToolContractForBrief — keep source-refresh gating centralized.
func runnerExplorationParentIsMutation(r *Runner) bool {
	if r == nil {
		return false
	}

	if strings.HasPrefix(strings.TrimSpace(r.agentName()), "child:") {
		return false
	}

	return runnerBriefRequiresMutation(r.RuntimeBrief)
}

func (s *runnerRequestBudgetState) synthesisDecision(policy RunPolicy, toolCalls int, mutation bool) runnerRequestBudgetDecision {
	if policy.SynthesisNudgeAt > 0 && toolCalls >= policy.SynthesisNudgeAt && !s.synthesisNudgeFired {
		s.synthesisNudgeFired = true

		nudgeText := fmt.Sprintf("You have used %d tool calls this turn. Before more tool calls, pause: summarize what you've learned so far, state the specific question that remains, and commit to the minimal next step. If the remaining gap is small, answer now with what you have.", toolCalls)

		if mutation {
			nudgeText = fmt.Sprintf("You have used %d tool calls this turn. Pause: name the exact edit target you've identified (file and line range), and commit to the smallest necessary edit. If you know the target and the change, write it now. If one edit-safety fact is still missing, take a bounded target-scoped probe for that fact; if the harness cannot provide it, state the exact blocker.", toolCalls)
		}

		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     nudgeText,
			EventRole: "synthesis_nudge",
		}
	}

	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteReadClassRequest(req workspace.ToolRequest) {
	s.readCallCount++

	s.readCallsSinceWrite++

	if s.postChildActive() {
		s.postChild.readCalls++
		if family := postChildRequestFamilyKey(req); family != "" {
			s.postChild.readFamilies[family]++
		}
	}
}

// readBudgetDecision used to force-finish lanes after a fixed post-
// child read budget. Per the project's "no bandaid budgets" memory:
// budget-style caps are bandaids; the structural enforcers
// (MaxToolSteps, doom_loop_guard, completion_gate) are the real
// closure contract. The hard cap fired prematurely on mutation
// parents that legitimately needed several anchor-verification reads
// after a child:explorer evidence packet (see #101 wedges). The
// completion gate enforces RequiredEditPaths on those parents, and
// doom_loop_guard catches genuine read-loops. Returning the empty
// decision here keeps the post-child counters tracking for telemetry
// + diminishing-returns nudges without force-finishing the lane.
func (s *runnerRequestBudgetState) readBudgetDecision(policy RunPolicy, req workspace.ToolRequest) runnerRequestBudgetDecision {
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteVerification() {
	s.readCallsSinceWrite = 0
}

func (s *runnerRequestBudgetState) noteWriteProgress() {
	s.readCallsSinceWrite = 0
	s.writeReadyNudgeActive = false
	// A successful write moves the lane from target/source acquisition
	// into post-write verification. Rejections from the pre-write phase
	// must not poison verification tools with stale retry fingerprints.
	clear(s.fingerprint.rejected)
	clear(s.fingerprint.rejectedRetries)
}

func (s *runnerRequestBudgetState) noteReadOnlyChildPacket(paths []string) {
	s.postChild.evidencePaths = normalizePostChildEvidencePaths(paths)
	s.postChild.readFamilies = make(map[string]int)
	s.postChild.familyNudgeFired = make(map[string]bool)
}

func (s *runnerRequestBudgetState) postChildGuidanceDecision(req workspace.ToolRequest, mutationParent bool) runnerRequestBudgetDecision {
	family := postChildRequestFamilyKey(req)
	if family == "" {
		return runnerRequestBudgetDecision{}
	}

	label := postChildRequestFamilyLabel(req)
	if label == "" {
		label = "the same evidence path"
	}

	// The post-child reread BLOCK was a bandaid budget that fired after
	// the model re-read the same file family 2× post-packet. Per the
	// no-bandaid memory, the structural enforcers (doom_loop_guard for
	// exact-duplicate reads, MaxToolSteps for total budget, completion
	// gate for mutation closure) own the closure contract. The nudge
	// below (educational, fires once) is kept; the hard block is gone.
	if count := s.postChild.readFamilies[family]; count >= 1 && !s.postChild.familyNudgeFired[family] {
		s.postChild.familyNudgeFired[family] = true

		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     fmt.Sprintf("you already inspected %s after the child packet. Either answer now, or take one narrow probe that names the exact missing claim; the same path is fine if you can name what new line you expect.", label),
			EventRole: "post_child_reread_nudge",
		}
	}

	return runnerRequestBudgetDecision{}
}

func postChildRequestFamilyKey(req workspace.ToolRequest) string {
	if workspace.IsSearchToolName(req.Tool) {
		if path := normalizePostChildPath(req.Path); path != "" && path != "." {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	}

	switch strings.TrimSpace(req.Tool) {
	case "read_file", "go_read_symbol", "go_file_outline", "python_read_symbol", "python_file_outline",
		"ts_read_symbol", "ts_file_outline", "rust_read_symbol", "rust_file_outline",
		"java_read_symbol", "java_file_outline", "cpp_read_symbol":
		if path := normalizePostChildPath(req.Path); path != "" {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	}

	return ""
}

func postChildRequestFamilyLabel(req workspace.ToolRequest) string {
	if path := normalizePostChildPath(req.Path); path != "" && path != "." {
		return path
	}

	return strings.TrimSpace(req.Tool)
}

func normalizePostChildEvidencePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))

	for _, path := range paths {
		cleaned := normalizePostChildPath(path)
		if cleaned == "" {
			continue
		}

		if _, ok := seen[cleaned]; ok {
			continue
		}

		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}

	return normalized
}

func summarizePostChildEvidencePaths(paths []string, max int) string {
	if len(paths) == 0 {
		return "the child packet paths"
	}

	limit := len(paths)
	if max > 0 && limit > max {
		limit = max
	}

	quoted := make([]string, 0, limit+1)
	for _, path := range paths[:limit] {
		quoted = append(quoted, "`"+path+"`")
	}

	if len(paths) > limit {
		quoted = append(quoted, "and related nearby files")
	}

	return strings.Join(quoted, ", ")
}

func normalizePostChildPath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}

	cleaned = filepath.ToSlash(filepath.Clean(cleaned))
	cleaned = strings.TrimPrefix(cleaned, "./")

	if cleaned == "" {
		return ""
	}

	return cleaned
}

func postChildPathsOverlap(reqPath string, evidencePath string) bool {
	if reqPath == evidencePath {
		return true
	}

	if strings.HasPrefix(reqPath+"/", evidencePath+"/") || strings.HasPrefix(evidencePath+"/", reqPath+"/") {
		return true
	}

	return filepath.ToSlash(filepath.Dir(evidencePath)) == reqPath
}

// runnerRunState is the bridge between Runner.Run's loop-local variables and
// extracted phase helpers. Keeping the handoff explicit avoids hidden captures
// while Run is still being split apart incrementally.
type runnerRunState struct {
	ctx     context.Context
	policy  RunPolicy
	metrics runnerMetrics

	opts             llama.ChatOptions
	collectedResults []workspace.ToolResult
	turnMessages     []llama.ChatMessage

	budgetExhausted bool
	stopReason      string

	currentAllowedTools                   []string
	toolSurface                           ActiveSurface
	implementationWriteOnlyRecoveryActive bool
	requiredPathCompletionRecoveryActive  bool
	exactAnchorWriteOnlyActive            bool

	executionState               *runnerExecutionState
	delegatedReadOnlyAnswerReady bool
	postChildPacketActive        bool
	directAnswerSurfaceEmitted   bool

	planThenExecuteNativeSubmitPlanDisabled bool

	autonomyRecorder                     *runnerAutonomyRecorder
	completionGateRejections             int
	completionStatusEmitted              bool
	completionStatusLastStatus           string
	completionStatusLastBlockerKind      string
	implementationTargetWriteReadyNudges int
	acceptedAnswerSignalsEmitted         bool

	controller runnerTurnController
}

func (state *runnerRunState) hideExactAnchorReadTools(allowedTools []string) {
	state.hideSurfaceToolsExcept(OwnerExactAnchorRead, allowedTools, func(tool string) bool {
		return workspace.ToolSurfacePhaseAllowsTool(tool, workspace.ToolSurfacePhaseImplementationPreWrite)
	})
}

func (state *runnerRunState) hideImplementationWriteOnlyTools(allowedTools []string) {
	state.hideSurfaceToolsExcept(OwnerImplementationWriteOnly, allowedTools, func(tool string) bool {
		return workspace.ToolSurfacePhaseAllowsTool(tool, workspace.ToolSurfacePhaseImplementationPreWrite)
	})
}

func (state *runnerRunState) hideReviewRepairTerminalOnlyTools(allowedTools []string) {
	state.hideSurfaceToolsExcept(OwnerReviewRepairTerminalOnly, allowedTools, func(tool string) bool {
		return strings.TrimSpace(tool) == workspace.ToolNameSubmitPlan
	})
}

func (state *runnerRunState) hideSurfaceToolsExcept(owner Owner, allowedTools []string, allow func(string) bool) {
	if state == nil || len(allowedTools) == 0 {
		return
	}

	state.toolSurface.SetVisibleTools(allowedTools)
	state.toolSurface.HideExcept(owner, allowedTools, allow)
}

func (state *runnerRunState) hideSurfaceTool(owner Owner, tool string) bool {
	tool = strings.TrimSpace(tool)
	if state == nil || tool == "" || state.surfaceHidesTool(tool) {
		return false
	}

	state.hideSurfaceTools(owner, tool)
	return true
}

func (state *runnerRunState) hideSurfaceTools(owner Owner, tools ...string) {
	if state == nil || len(tools) == 0 {
		return
	}

	if !activeSurfaceProvided(state.toolSurface) {
		state.toolSurface = NewActiveSurface(state.currentAllowedTools, nil)
	}
	state.toolSurface.Hide(owner, tools...)
}

func (state *runnerRunState) hideNonSourceBodyReadTools(owner Owner, allowedTools []string) {
	if state == nil || len(allowedTools) == 0 {
		return
	}

	if !activeSurfaceProvided(state.toolSurface) {
		state.toolSurface = NewActiveSurface(allowedTools, nil)
	} else {
		state.toolSurface.SetVisibleTools(allowedTools)
	}
	state.toolSurface.HideExcept(owner, allowedTools, func(tool string) bool {
		return tool == workspace.ToolNameReadFile || tool == workspace.ToolNameCodeNavigate
	})
}

func (state *runnerRunState) releaseImplementationWriteOnlyTools() {
	state.releaseSurfaceHiddenTools(OwnerImplementationWriteOnly)
	state.releaseSurfaceHiddenTools(OwnerReviewRepairTerminalOnly)
}

func (state *runnerRunState) releaseSurfaceHiddenTools(owner Owner) {
	if state == nil {
		return
	}

	state.toolSurface.Release(owner)
}

func (state *runnerRunState) surfaceHidesTool(tool string) bool {
	return state != nil && state.toolSurface.HidesTool(tool)
}

func (state *runnerRunState) surfaceHiddenByOwner(owner Owner, tool string) bool {
	return state != nil && state.toolSurface.HiddenByOwner(owner, tool)
}

// setCurrentAllowedTools syncs the Runner-level currentAllowedTools field.
func (r *Runner) setCurrentAllowedTools(state *runnerRunState, tools []string) {
	tools = append([]string(nil), tools...)
	if state != nil {
		state.currentAllowedTools = append(state.currentAllowedTools[:0], tools...)
	}

	r.currentAllowedTools = append(r.currentAllowedTools[:0], tools...)
	if r.StateStore != nil {
		r.StateStore.SetAllowedTools(tools)
	}
}

// syncCurrentAllowedToolsFromOptions rebuilds currentAllowedTools from opts.Tools.
func (r *Runner) syncCurrentAllowedToolsFromOptions(state *runnerRunState) {
	state.currentAllowedTools = state.currentAllowedTools[:0]
	for _, tool := range state.opts.Tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name != "" {
			state.currentAllowedTools = append(state.currentAllowedTools, name)
		}
	}

	r.setCurrentAllowedTools(state, state.currentAllowedTools)
}

// terminalSubmitPlanNativeTools reports whether native submit_plan is enabled.
func (r *Runner) terminalSubmitPlanNativeTools(state *runnerRunState) bool {
	return !state.planThenExecuteNativeSubmitPlanDisabled && r.supportsTerminalSubmitPlanNativeTools(state.collectedResults)
}

// syncPlanThenExecuteRecoveryAllowedTools rebuilds allowed tools from opts and
// overrides with plan-execute recovery surface callable tools.
func (r *Runner) syncPlanThenExecuteRecoveryAllowedTools(state *runnerRunState) {
	r.syncCurrentAllowedToolsFromOptions(state)

	if !r.planThenExecutePlannerPhase() {
		return
	}

	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh, PlanExecuteRecoverySurfaceHistoryPending:
		if len(state.opts.Tools) == 0 || !state.opts.ForceNativeTools {
			state.currentAllowedTools = append(state.currentAllowedTools[:0], surface.State.CallableTools...)
			r.setCurrentAllowedTools(state, state.currentAllowedTools)
		}
	case PlanExecuteRecoverySurfaceSubmitReady, PlanExecuteRecoverySurfaceSubmitCorrection:
		state.currentAllowedTools = append(state.currentAllowedTools[:0], workspace.ToolNameSubmitPlan)
		r.setCurrentAllowedTools(state, state.currentAllowedTools)
	}
}

// rebuildRunTools is the central tool-rebuild function. It reads state for
// hidden-tool tracking, write-only/scoped-edit surfaces, execution state,
// and post-readiness contracts, then writes state.opts.Tools and calls sync.
func (r *Runner) rebuildRunTools(state *runnerRunState) {
	if r.Workspace == nil {
		return
	}

	workspaceSurface := r.workspaceToolSurfaceSnapshot()
	allowed := r.workspaceAdvertisedAllowedTools()
	writeOnlySurface := state.implementationWriteOnlyRecoveryActive ||
		state.requiredPathCompletionRecoveryActive ||
		exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, state.collectedResults)
	scopedEditSurface := !writeOnlySurface && implementationScopedEditSurfaceActive(r.RuntimeBrief, state.collectedResults)

	state.toolSurface.Release(OwnerImplementationScoped)
	if !activeSurfaceProvided(state.toolSurface) {
		state.toolSurface = NewActiveSurface(allowed, nil)
	} else {
		state.toolSurface.SetVisibleTools(allowed)
	}
	if scopedEditSurface {
		state.toolSurface.HideExcept(OwnerImplementationScoped, allowed, func(tool string) bool {
			return workspace.ImplementationScopedEditToolAllowedWithExternalReadOnlyDiscovery(tool, workspaceSurface.ExternalToolIsReadOnlyDiscovery)
		})
	}

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.RuntimeBrief.PermissionProfile) && !state.executionState.verificationAttempted {
		filtered := make([]string, 0, 1)

		for _, t := range allowed {
			if t == "shell" {
				filtered = append(filtered, t)
			}
		}

		allowed = filtered
	}

	if runnerBriefAllowsExternalToolAppend(r.RuntimeBrief) && !writeOnlySurface && !scopedEditSurface {
		if externalNames := workspaceSurface.AvailableExternalToolNames(); len(externalNames) > 0 {
			seen := make(map[string]struct{}, len(allowed))
			for _, name := range allowed {
				seen[name] = struct{}{}
			}

			for _, name := range externalNames {
				if _, ok := seen[name]; !ok {
					allowed = append(allowed, name)
					seen[name] = struct{}{}
				}
			}
		}
	}

	if writeOnlySurface {
		allowed = workspace.ToolSurfacePhaseAllowedTools(allowed, workspace.ToolSurfacePhaseImplementationPreWrite)
	} else if scopedEditSurface {
		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			Brief:            r.RuntimeBrief,
			Results:          state.collectedResults,
			BaseAllowedTools: allowed,
			ActiveSurface:    state.toolSurface,
		})

		allowed = append([]string(nil), contract.AllowedTools...)

		for _, name := range workspaceSurface.AvailableExternalToolNames() {
			if workspaceSurface.ExternalToolIsReadOnlyDiscovery(name) && !slices.Contains(allowed, name) {
				allowed = append(allowed, name)
			}
		}
	} else if contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            r.RuntimeBrief,
		Results:          state.collectedResults,
		BaseAllowedTools: allowed,
	}); contract.PhaseID == turnPhaseImplementationTargetSource ||
		contract.PhaseID == turnPhaseImplementationVerifyReady ||
		contract.PhaseID == turnPhaseImplementationWriteReady {

		allowed = append([]string(nil), contract.AllowedTools...)
	}

	if surface, ok := DirectAnswerSurfaceForBrief(r.RuntimeBrief); ok {
		allowed = append([]string(nil), surface.ToolNames...)

		if !state.directAnswerSurfaceEmitted {
			EmitDirectAnswerSurfaceEvent(r.emitEvent, surface)

			state.directAnswerSurfaceEmitted = true
		}
	}

	if activeSurfaceProvided(state.toolSurface) {
		allowed = state.toolSurface.FilterVisible(allowed)
	}

	var tools []llama.Tool

	if len(allowed) > 0 || !workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		tools = buildLlamaTools(workspaceSurface.BuildToolDefinitions(allowed))
		if !workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) &&
			lanePolicyShouldHaveNoteTool(r.lanePolicy(), r.RuntimeBrief.Contract) {

			tools = append(tools, noteToolDefinition())
			if !slices.Contains(r.RuntimeBrief.AllowedTools, noteToolName) {
				r.RuntimeBrief.AllowedTools = append(r.RuntimeBrief.AllowedTools, noteToolName)
			}
		}
	}

	if contract := postReadinessToolContractForBrief(r.RuntimeBrief, state.collectedResults, state.delegatedReadOnlyAnswerReady, state.postChildPacketActive); contract.RequireSourceRefreshContract {
		tools = stripPostReadinessBroadTools(tools)
		tools = applyPostReadinessSchemaRequirements(tools)
	}

	state.opts.Tools = tools
	r.syncCurrentAllowedToolsFromOptions(state)

	if len(state.opts.Tools) == 0 {
		state.opts.ToolChoice = ""
	} else if strings.TrimSpace(state.opts.ToolChoice) == "" {
		state.opts.ToolChoice = "auto"
	}
}

// refreshPlanThenExecuteTerminalSurface refreshes the terminal surface.
func (r *Runner) refreshPlanThenExecuteTerminalSurface(state *runnerRunState, reason string) {
	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh:
		r.refreshPlanThenExecuteAnchorRefreshSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		r.refreshPlanThenExecuteTerminalOutcomeSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSubmitReady, PlanExecuteRecoverySurfaceSubmitCorrection:
	default:
		r.prunePlanThenExecuteTerminalSurfaceHistory(&state.turnMessages, reason)

		return
	}

	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// forcePlanThenExecuteTerminalSurface unconditionally forces the terminal surface.
func (r *Runner) forcePlanThenExecuteTerminalSurface(state *runnerRunState, reason string) {
	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// transitionPlanThenExecuteSubmitReadyTerminal applies the full submit-ready
// runtime surface. Prompt-only transitions are unsafe here: once the model is
// told to call submit_plan, currentAllowedTools must agree even if stale
// read-only native tools are still present in chat options.
func (r *Runner) transitionPlanThenExecuteSubmitReadyTerminal(state *runnerRunState, prompt string, eventRole string, surfaceKey string) {
	r.refreshPlanThenExecuteTerminalSurface(state, surfaceKey)
	state.opts = planThenExecuteReadyToSubmitChatOptionsForBackend(state.opts, r.RuntimeBrief, state.collectedResults, r.terminalSubmitPlanNativeTools(state))
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	r.syncPlanThenExecuteRecoveryAllowedTools(state)
	r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
}

// emitStateCompletionStatus records completion status and delegates to Runner method.
func (r *Runner) emitStateCompletionStatus(state *runnerRunState, payload runnerCompletionStatus) {
	state.completionStatusEmitted = true
	state.completionStatusLastStatus = strings.TrimSpace(payload.Status)
	state.completionStatusLastBlockerKind = strings.TrimSpace(payload.BlockerKind)
	r.emitCompletionStatus(payload)
}

// emitTerminalAnswerSignalsOnce emits final-answer signals exactly once.
func (r *Runner) emitTerminalAnswerSignalsOnce(state *runnerRunState, decision terminalAnswerDecision) {
	if state.acceptedAnswerSignalsEmitted || strings.TrimSpace(decision.RawAnswer) == "" {
		return
	}

	switch decision.Outcome {
	case terminalAnswerAccepted:
		if state.autonomyRecorder != nil {
			reasonCode := "final_answer_accepted"
			if r.planThenExecutePlannerPhase() && plancontract.SourceSatisfiedAnswerIntent(decision.RawAnswer) {
				reasonCode = "plan_source_satisfied_short_circuit"
			}

			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyAllow, reasonCode, state.collectedResults, state.completionGateRejections)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)
	case terminalAnswerBlocked, terminalAnswerTransportBlocked:
		blockerKind := textutil.FirstNonEmpty(strings.TrimSpace(decision.Acceptance.BlockerKind), strings.TrimSpace(decision.StopReason))
		reason := strings.TrimSpace(decision.Acceptance.Reason)
		if hostOwnedQuestionCloseoutStopReason(state.stopReason, decision.RawAnswer) &&
			runnerBriefRequiresMutation(r.RuntimeBrief) &&
			len(newToolResultEvidenceProjection(state.collectedResults).ChangedPathSet()) == 0 {

			blockerKind = strings.TrimSpace(state.stopReason)
			reason = blockerKind
		}
		if state.autonomyRecorder != nil {
			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, blockerKind, state.collectedResults, state.completionGateRejections)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)

		if terminalBlockedStatusShouldEmit(state.completionStatusEmitted, state.completionStatusLastStatus, state.completionStatusLastBlockerKind, blockerKind) {
			// Construct the runnerCompletionStatus once, shared by the legacy event emission below.
			changedPaths := mapKeys(newToolResultEvidenceProjection(state.collectedResults).ChangedPathSet())
			completionPayload := runnerCompletionStatus{
				Lane:          r.agentName(),
				TaskFamily:    strings.TrimSpace(r.RuntimeBrief.TaskFamily),
				Contract:      strings.TrimSpace(r.RuntimeBrief.Contract),
				Status:        "blocked",
				Reason:        reason,
				BlockerKind:   strings.TrimSpace(blockerKind),
				RequiresEdit:  runnerBriefRequiresMutation(r.RuntimeBrief),
				EditAttempted: len(changedPaths) > 0,
				ImplementationState: recoverycontract.ImplementationState{
					ChangedPaths: changedPaths,
				},
				UserVisibleBlocker: true,
			}
			r.attachImplementationLifecycleStatus(&completionPayload, state.collectedResults, state.stopReason, state.implementationTargetWriteReadyNudges)
			r.emitStateCompletionStatus(state, completionPayload)
		}
	}

	state.acceptedAnswerSignalsEmitted = true
}

// finish is the terminal answer path for Run().
func (r *Runner) finish(state *runnerRunState, value string, finishErr error, reason string, exhausted bool) (string, error) {
	if strings.TrimSpace(reason) != "" {
		state.stopReason = reason
	}

	state.budgetExhausted = exhausted
	if finishErr == nil {
		terminalInput := value
		decision := r.classifyTerminalAnswer(value, state.stopReason, state.collectedResults)
		presentedBeforeDecoration := decision.PresentedAnswer

		decision.PresentedAnswer = r.decorateTerminalAnswerDecision(decision, state.collectedResults)

		if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(presentedBeforeDecoration) {
			r.emitFinalAnswerDecorationEvent(decision.StopReason, presentedBeforeDecoration, decision.PresentedAnswer)

			if strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "" {
				r.LastAssistantMessage.Content = decision.PresentedAnswer
			}
		}

		if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(terminalInput) &&
			(strings.TrimSpace(r.LastAssistantMessage.Role) != "" || strings.TrimSpace(r.LastAssistantMessage.Content) != "") {

			r.LastAssistantMessage.Content = decision.PresentedAnswer
		}

		value = decision.PresentedAnswer
		state.stopReason = decision.StopReason

		if decision.RetryExhausted || (decision.Outcome == terminalAnswerAccepted && decision.StopReason == "read_only_terminal_recovery") {
			state.budgetExhausted = false
		}

		if decision.OverrideSource != "" {
			r.emitFinalAnswerBlockerOverride(decision.Acceptance, decision.StopReason, decision.OverrideSource)
		}

		r.emitTerminalAnswerSignalsOnce(state, decision)
	}

	return value, finishErr
}

// retryRejectedFinalAnswer handles rejected final answer with retry logic.
func (r *Runner) retryRejectedFinalAnswer(state *runnerRunState, stepNumber int, activeToolCall *llama.ToolCall, assistantMessage llama.ChatMessage, acceptance finalAnswerAcceptance, result workspace.ToolResult) (bool, string) {
	if !r.retryableFinalAnswerAcceptanceRejection(acceptance, state.collectedResults, state.completionGateRejections, state.currentAllowedTools) {
		return false, ""
	}

	priorGateRejections := state.completionGateRejections

	result.Error = r.escalateCompletionGateResultMessage(result, priorGateRejections, state.collectedResults, state.currentAllowedTools)

	if state.autonomyRecorder != nil {
		state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), state.collectedResults, priorGateRejections)
	}

	state.controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)

	state.completionGateRejections = priorGateRejections + 1

	r.rememberCompletionGateRejection(result)

	var recoveryPrompt string

	if prompt, _, ok := r.completionGateRecoveryPrompt(state.collectedResults); ok {
		if card := r.renderRecoveryCardForRejectedActionWithTransport(workspace.ToolRequest{Tool: "completion_gate"}, result, state.collectedResults, state.currentAllowedTools, r.currentPlanningTransport()); card != "" {
			prompt = appendRecoveryCardToMessage(prompt, card)
		}

		recoveryPrompt = prompt
		r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
	}

	return true, recoveryPrompt
}

type RunPolicy struct {
	MaxToolSteps       int
	MaxNoProgressTurns int
	MaxContextMessages int
	MaxContextChars    int
	// MaxModelCalls is an advisory accounting threshold. It may appear in
	// telemetry and ledgers, but it must not terminally steer model strategy.
	MaxModelCalls int
	// MaxOperationalModelCalls is the explicit hard provider/cost fuse. Zero
	// disables it; callers must opt in when a terminal model-call stop is a
	// real operational invariant.
	MaxOperationalModelCalls int
	// SynthesisNudgeAt injects a one-shot "pause and synthesize" system
	// message when ToolCalls first reaches this value. 0 disables. Mitigates
	// long tool chains that produce short answers.
	SynthesisNudgeAt int
	// FingerprintNudgeAt injects a one-shot system message per fingerprint
	// when a given tool+args pair has been called this many non-consecutive
	// times. 0 disables. Complements the DoomLoopGate (which only catches
	// consecutive triples).
	FingerprintNudgeAt int
}

type TaskLedgerPolicy struct {
	Disabled        bool
	Required        bool
	RequireEvidence bool
}

type CompletionGate func(CompletionGateInput) CompletionGateResult

type CompletionGateInput struct {
	AgentName string
	Reply     string
	Results   []workspace.ToolResult
	Steps     int
}

type CompletionGateResult struct {
	Accept  bool
	Summary string
	Error   string
}

type RunEvent struct {
	Role    string
	Content string
}

type ThoughtEvent struct {
	Step int
	Text string
}

type ToolRequestEvent struct {
	Step              int
	Agent             string
	ChildInvocationID string
	ChildContractID   string
	Raw               string
	Request           workspace.ToolRequest
	Mode              workspace.Mode
	Broad             bool
	Narrowing         bool
	Action            string
	DiscoveryClass    string
	DiscoveryScope    string
	DiscoveryCost     string
}

type ToolResultEvent struct {
	Step              int
	Agent             string
	ChildInvocationID string
	ChildContractID   string
	Request           workspace.ToolRequest
	Result            workspace.ToolResult
}

type runnerFailureFeedbackPacket struct {
	Lane               string   `json:"lane,omitempty"`
	Step               int      `json:"step,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	FailedTool         string   `json:"failed_tool,omitempty"`
	FailedTarget       string   `json:"failed_target,omitempty"`
	FailureClass       string   `json:"failure_class,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	ResultError        string   `json:"result_error,omitempty"`
	EvidenceStatus     []string `json:"evidence_status,omitempty"`
	DoNotRepeat        []string `json:"do_not_repeat,omitempty"`
	RequiredNext       []string `json:"required_next,omitempty"`
	InvalidNext        []string `json:"invalid_next,omitempty"`
	ValidNext          []string `json:"valid_next,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
	RecoveryCard       string   `json:"recovery_card,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker,omitempty"`
}

func (r *Runner) effectivePolicy() RunPolicy {
	policy := r.Policy
	if policy.MaxToolSteps == 0 && r.MaxToolSteps != 0 {
		policy.MaxToolSteps = r.MaxToolSteps
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxToolSteps; briefLimit > 0 && (policy.MaxToolSteps <= 0 || briefLimit < policy.MaxToolSteps) {
		policy.MaxToolSteps = briefLimit
	}

	if policy.MaxNoProgressTurns <= 0 {
		policy.MaxNoProgressTurns = 6
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		policy = scaleMaxToolStepsByRequiredPaths(policy, len(r.RuntimeBrief.RequiredEditPaths))
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxToolSteps; briefLimit > 0 && (policy.MaxToolSteps <= 0 || briefLimit < policy.MaxToolSteps) {
		policy.MaxToolSteps = briefLimit
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxModelCalls; briefLimit > 0 && (policy.MaxModelCalls <= 0 || briefLimit < policy.MaxModelCalls) {
		policy.MaxModelCalls = briefLimit
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxOperationalModelCalls; briefLimit > 0 && (policy.MaxOperationalModelCalls <= 0 || briefLimit < policy.MaxOperationalModelCalls) {
		policy.MaxOperationalModelCalls = briefLimit
	}

	// Default synthesis nudge: fire at 10 tool calls for any lane that
	// doesn't explicitly skip it. The TUI already sets this via ModePolicy;
	// this ensures CLI mode gets the same advisory nudge so the model is
	// prompted to pause and answer rather than exploring indefinitely.
	if policy.SynthesisNudgeAt <= 0 && !r.lanePolicy().skipSynthesisNudge {
		policy.SynthesisNudgeAt = 10
	}

	return policy
}

// scaleMaxToolStepsByRequiredPaths grows the per-run tool-step ceiling for
// multi-file briefs. Single-file atoms keep the configured default; atoms
// with N required edit paths expand to base + 4*(N-1) capped at the
// ceiling.
//
// Scoping rules:
//   - MaxToolSteps == 0 (unbounded) is left alone.
//   - MaxToolSteps < base (explicit tight constraint, e.g. tests forcing
//     budget exhaustion at step 1) is left alone — we treat values below
//     the base as deliberate.
//   - MaxToolSteps >= base is raised to the scaled value when scaled is
//     larger; already-higher policies are left alone.
func scaleMaxToolStepsByRequiredPaths(policy RunPolicy, pathCount int) RunPolicy {
	if pathCount <= 1 {
		return policy
	}

	scaled := maxToolStepsBase + 4*(pathCount-1)
	if scaled > maxToolStepsCeiling {
		scaled = maxToolStepsCeiling
	}

	if policy.MaxToolSteps >= maxToolStepsBase && policy.MaxToolSteps < scaled {
		policy.MaxToolSteps = scaled
	}

	return policy
}

const (
	// maxToolStepsBase is the conservative default budget for a single-file
	// implementation atom. Multi-file briefs scale up via
	// scaleMaxToolStepsByRequiredPaths so a 5-file atom doesn't run out of
	// tool steps doing legitimate per-file source/history reads plus a
	// submit_plan correction or bounded anchor-refresh recovery.
	maxToolStepsBase = 16
	// maxToolStepsCeiling caps the scaled budget so a sprawling brief
	// can't run away even if it slipped past the brief lint.
	maxToolStepsCeiling = 56
	// postCompletionVerificationCutoff bounds how many non-write tool calls
	// the agent can issue after all required edit paths have been written.
	// A small budget lets the model do legitimate "did my edit land?" reads
	// without letting verification thrash exhaust the tool-step budget and
	// produce a false failure signal. Past this count the runner
	// force-answers with a success completion.
	postCompletionVerificationCutoff = 3
)

func (p RunPolicy) unbounded() bool {
	return p.MaxToolSteps <= 0
}

func (p RunPolicy) shouldStopForNoProgress(consecutiveNoProgress int) bool {
	return p.MaxNoProgressTurns > 0 && consecutiveNoProgress >= p.MaxNoProgressTurns
}

func (p RunPolicy) shouldStopForOperationalModelCalls(used int) bool {
	return p.MaxOperationalModelCalls > 0 && used >= p.MaxOperationalModelCalls
}

type runnerMetrics struct {
	startedAt                  time.Time
	ModelCalls                 int  `json:"used_model_calls"`
	ToolCalls                  int  `json:"used_tool_results"`
	PromptTokens               int  `json:"prompt_tokens,omitempty"`
	CompletionTokens           int  `json:"completion_tokens,omitempty"`
	TotalTokens                int  `json:"total_tokens,omitempty"`
	PromptCacheHitTokens       int  `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens      int  `json:"prompt_cache_miss_tokens,omitempty"`
	PromptCacheAccountingKnown bool `json:"prompt_cache_accounting_known,omitempty"`
	ReasoningTokens            int  `json:"reasoning_tokens,omitempty"`
	ProgressTurns              int  `json:"progress_turns"`
	RejectedActions            int  `json:"rejected_actions"`
	CompletionGateRejections   int  `json:"completion_gate_rejections"`
	Compactions                int  `json:"context_compactions"`
	ReadClassCalls             int  `json:"read_class_calls"`
}

type runnerBudgetLedger struct {
	Lane                       string   `json:"lane"`
	UsedModelCalls             int      `json:"used_model_calls"`
	MaxModelCalls              int      `json:"max_model_calls,omitempty"`
	MaxOperationalModelCalls   int      `json:"max_operational_model_calls,omitempty"`
	UsedToolResults            int      `json:"used_tool_results"`
	MaxToolSteps               int      `json:"max_tool_steps,omitempty"`
	PromptTokens               int      `json:"prompt_tokens,omitempty"`
	CompletionTokens           int      `json:"completion_tokens,omitempty"`
	TotalTokens                int      `json:"total_tokens,omitempty"`
	PromptCacheHitTokens       int      `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens      int      `json:"prompt_cache_miss_tokens,omitempty"`
	PromptCacheAccountingKnown bool     `json:"prompt_cache_accounting_known,omitempty"`
	ReasoningTokens            int      `json:"reasoning_tokens,omitempty"`
	ProgressTurns              int      `json:"progress_turns"`
	RejectedActions            int      `json:"rejected_actions"`
	CompletionGateRejections   int      `json:"completion_gate_rejections"`
	ContextCompactions         int      `json:"context_compactions"`
	ReadClassCalls             int      `json:"read_class_calls,omitempty"`
	DurationMillis             int64    `json:"duration_millis"`
	StopReason                 string   `json:"stop_reason"`
	BudgetExhausted            bool     `json:"budget_exhausted"`
	NextAllowedActions         []string `json:"next_allowed_actions,omitempty"`
	UserVisibleUncertainty     string   `json:"user_visible_uncertainty,omitempty"`
}

type runnerCompletionStatus struct {
	Lane          string `json:"lane"`
	TaskFamily    string `json:"task_family,omitempty"`
	Contract      string `json:"contract,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	BlockerKind   string `json:"blocker_kind,omitempty"`
	RequiresEdit  bool   `json:"requires_edit,omitempty"`
	EditAttempted bool   `json:"edit_attempted"`
	recoverycontract.ImplementationState
	UserVisibleBlocker bool `json:"user_visible_blocker,omitempty"`
}

func (m *runnerMetrics) addUsage(usage llama.Usage) {
	if m == nil {
		return
	}

	m.PromptTokens += nonNegativeInt(usage.PromptTokens)
	m.CompletionTokens += nonNegativeInt(usage.CompletionTokens)
	m.PromptCacheHitTokens += nonNegativeInt(usage.PromptCacheHitTokens)
	m.PromptCacheMissTokens += nonNegativeInt(usage.PromptCacheMissTokens)

	m.PromptCacheAccountingKnown = m.PromptCacheAccountingKnown || usage.PromptCacheAccountingKnown

	m.ReasoningTokens += nonNegativeInt(usage.ReasoningTokens)

	total := usage.TotalTokens

	if total <= 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}

	m.TotalTokens += nonNegativeInt(total)
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}

	return value
}

type runnerNextActionState struct {
	Lane               string                    `json:"lane"`
	Phase              string                    `json:"phase,omitempty"`
	Step               int                       `json:"step"`
	Reason             string                    `json:"reason"`
	LastTool           string                    `json:"last_tool,omitempty"`
	LastTarget         string                    `json:"last_target,omitempty"`
	ResultSummary      string                    `json:"result_summary,omitempty"`
	ResultError        string                    `json:"result_error,omitempty"`
	ResultOutputChars  int                       `json:"result_output_chars,omitempty"`
	EvidencePaths      []string                  `json:"evidence_paths,omitempty"`
	CandidateSymbols   []string                  `json:"candidate_symbols,omitempty"`
	EvidenceStatus     []string                  `json:"evidence_status,omitempty"`
	Constraints        []string                  `json:"constraints,omitempty"`
	InvalidNextActions []string                  `json:"invalid_next_actions,omitempty"`
	NextValidActions   []string                  `json:"next_valid_actions,omitempty"`
	StateSummary       string                    `json:"state_summary,omitempty"`
	RecoveryCard       string                    `json:"recovery_card,omitempty"`
	RecoveryState      *PlanExecuteRecoveryState `json:"recovery_state,omitempty"`
	recoveryCard       *RecoveryCard
}

type runnerThinkingTrace struct {
	Lane               string `json:"lane"`
	Step               int    `json:"step"`
	Reason             string `json:"reason"`
	EnableThinking     string `json:"enable_thinking"`
	PreserveThinking   bool   `json:"preserve_thinking"`
	ContentChars       int    `json:"content_chars"`
	ContentPreview     string `json:"content_preview,omitempty"`
	ReasoningChars     int    `json:"reasoning_chars"`
	PreservedInHistory bool   `json:"preserved_in_history"`
}

type runnerContextCompaction struct {
	Lane             string   `json:"lane"`
	DroppedMessages  int      `json:"dropped_messages"`
	RetainedMessages int      `json:"retained_messages"`
	MaxHistory       int      `json:"max_history,omitempty"`
	MaxChars         int      `json:"max_chars,omitempty"`
	PinnedPaths      []string `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string   `json:"pinned_memory_kind"`
	Strategy         string   `json:"strategy"`
}

func (r *Runner) emitNextActionState(step int, reason string, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult, declarationMovesForbidden bool) {
	if r == nil || r.OnEvent == nil {
		return
	}

	brief := r.visibleBriefForToolSurface(r.RuntimeBrief, results)
	state, phaseContract := buildRunnerNextActionStateWithAllowedToolsAndContract(r.agentName(), step, reason, req, result, results, declarationMovesForbidden, brief, r.currentAllowedTools, r.currentPlanningTransport())

	if r.activeAutonomy != nil {
		r.activeAutonomy.emitTurnPhaseSurfaceSummary(turnPhaseSurfaceSummaryFromNextActionState(state, phaseContract), phaseContract)

		if state.recoveryCard != nil {
			r.activeAutonomy.emitTurnPhaseSurfaceSummary(state.recoveryCard.TurnPhaseSurfaceSummary(phaseContract), phaseContract)
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
		brief.TargetPacket = textutil.Merge(brief.TargetPacket, reviewRepairPhaseEvidencePacket(brief.Objective))
	}

	if brief.Budget == (workerruntime.BudgetBrief{}) {
		brief.Budget = workerruntime.BudgetBrief{
			MaxToolSteps:             policy.MaxToolSteps,
			MaxModelCalls:            policy.MaxModelCalls,
			MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
			MaxContextMessages:       policy.MaxContextMessages,
			MaxContextChars:          policy.MaxContextChars,
		}
	}

	liveEvidencePaths := runnerEvidencePathsForBrief(brief, results, 8)
	if readOnlyDirectAnswerLane(brief) {
		if len(liveEvidencePaths) > 0 {
			brief.EvidencePaths = liveEvidencePaths
		}
	} else {
		brief.EvidencePaths = textutil.Merge(brief.EvidencePaths, liveEvidencePaths)
	}

	brief.ChangedPaths = textutil.Merge(brief.ChangedPaths, mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet()))
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
		readiness.ClosedClaims = textutil.Merge(readiness.ClosedClaims, values)
	}
	addOpen := func(values ...string) {
		readiness.OpenObligations = textutil.Merge(readiness.OpenObligations, values)
	}
	addSource := func(values ...string) {
		readiness.EvidenceSources = textutil.Merge(readiness.EvidenceSources, values)
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

	if len(readOnlyLedger.RelevantExternalDocumentPaths) > 0 {
		addSource("external_document_body")
		addClosed("relevant_external_document_evidence")
	}

	if readOnlyLedger.DiagnosticEvidence {
		addSource("diagnostic_evidence")
		addClosed("diagnostic_evidence_available")
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		strings.TrimSpace(brief.TaskFamily) == "delegated_read_only" &&
		delegatedReadOnlyClosureReady(agent, brief, results) {

		readiness.ReadyToAnswer = true

		addClosed("answer_readiness_reached")

		readiness.FurtherReadsRequire = "one exact missing claim, one exact probe, and why the current evidence would be materially incomplete"
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") && postChildPacketClosureReadyWithBrief(brief, results) {
		readiness.ReadyToAnswer = true

		addClosed("child_packet_available")

		readiness.FurtherReadsRequire = "one exact packet-anchored missing claim and one exact probe"
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
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

func runnerCurrentAllowedOrBriefTools(currentAllowedTools []string, briefAllowedTools []string) []string {
	if len(textutil.Unique(currentAllowedTools)) > 0 {
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

	return textutil.Truncate(strings.TrimSpace(strings.Join(parts, " | ")), 320)
}

func prependRunnerStateStrings(preferred []string, existing []string) []string {
	out := make([]string, 0, len(preferred)+len(existing))
	for _, value := range preferred {
		out = textutil.AppendUnique(out, value)
	}

	for _, value := range existing {
		out = textutil.AppendUnique(out, value)
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

func (r *Runner) emitBudgetLedger(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if r == nil || r.OnEvent == nil {
		return
	}

	ledger := runnerBudgetLedger{
		Lane:                       r.agentName(),
		UsedModelCalls:             metrics.ModelCalls,
		MaxModelCalls:              policy.MaxModelCalls,
		MaxOperationalModelCalls:   policy.MaxOperationalModelCalls,
		UsedToolResults:            metrics.ToolCalls,
		PromptTokens:               metrics.PromptTokens,
		CompletionTokens:           metrics.CompletionTokens,
		TotalTokens:                metrics.TotalTokens,
		PromptCacheHitTokens:       metrics.PromptCacheHitTokens,
		PromptCacheMissTokens:      metrics.PromptCacheMissTokens,
		PromptCacheAccountingKnown: metrics.PromptCacheAccountingKnown,
		ReasoningTokens:            metrics.ReasoningTokens,
		ProgressTurns:              metrics.ProgressTurns,
		RejectedActions:            metrics.RejectedActions,
		CompletionGateRejections:   metrics.CompletionGateRejections,
		ContextCompactions:         metrics.Compactions,
		ReadClassCalls:             metrics.ReadClassCalls,
		DurationMillis:             time.Since(metrics.startedAt).Milliseconds(),
		StopReason:                 strings.TrimSpace(stopReason),
		BudgetExhausted:            exhausted,
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

func (r *Runner) emitCompletionStatus(payload runnerCompletionStatus) {
	if r == nil || r.OnEvent == nil {
		return
	}

	r.emitEvent(marshalRunEvent("completion_status", payload))
}

// emitCompletionStatusFromParams constructs the completion-status event from
// the Run loop's terminal decision fields.
func (r *Runner) emitCompletionStatusFromParams(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool, writeReadyNudges int) {
	changedPaths := mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())
	payload := runnerCompletionStatus{
		Lane:          r.agentName(),
		TaskFamily:    strings.TrimSpace(r.RuntimeBrief.TaskFamily),
		Contract:      strings.TrimSpace(r.RuntimeBrief.Contract),
		Status:        strings.TrimSpace(status),
		Reason:        strings.TrimSpace(reason),
		BlockerKind:   strings.TrimSpace(blockerKind),
		RequiresEdit:  runnerBriefRequiresMutation(r.RuntimeBrief),
		EditAttempted: len(changedPaths) > 0,
		ImplementationState: recoverycontract.ImplementationState{
			ChangedPaths: changedPaths,
		},
		UserVisibleBlocker: userVisibleBlocker,
	}
	r.attachImplementationLifecycleStatus(&payload, results, "", writeReadyNudges)
	r.emitCompletionStatus(payload)
}

func workspaceToolRulesPermissionDeny(decision PermissionDecisionSnapshot) bool {
	return decision.Decision == toolpolicy.Deny && strings.Contains(strings.TrimSpace(decision.Rule), "workspace.tool_rules")
}

func workspaceToolRulesPermissionDenyAnswer(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	target := PermissionTarget(req)
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

	if reply.Usage.PromptCacheAccountingKnown {
		payload["prompt_cache_hit_tokens"] = reply.Usage.PromptCacheHitTokens
		payload["prompt_cache_miss_tokens"] = reply.Usage.PromptCacheMissTokens
		payload["prompt_cache_accounting_known"] = true
	}

	if reply.Usage.ReasoningTokens > 0 {
		payload["reasoning_tokens"] = reply.Usage.ReasoningTokens
	}

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

	for key, value := range modelCallPrefixDiagnostics(prompt, opts, parserContract) {
		payload[key] = value
	}

	if finish := strings.TrimSpace(reply.FinishReason); finish != "" {
		payload["finish_reason"] = finish
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	r.OnEvent(RunEvent{Role: "model_call", Content: string(data)})
}

func (r *Runner) emitToolRequestNormalizerEvent(outcome string, transport string, req workspace.ToolRequest, parseErr error, parserFailureClass autonomy.ParserFailureClass) {
	if r == nil || r.OnEvent == nil {
		return
	}

	payload := map[string]any{
		"outcome":   strings.TrimSpace(outcome),
		"transport": strings.TrimSpace(transport),
		"tool":      strings.TrimSpace(req.Tool),
		"path":      NormalizeRunnerPath(planThenExecuteRequestTargetPath(req)),
	}
	if parseErr == nil && strings.TrimSpace(req.Tool) != "" && toolRequestNormalizerTransportIsModelTextual(transport) {
		payload["parsed_textual_tool"] = strings.TrimSpace(req.Tool)
		payload["textual_tool_transport"] = strings.TrimSpace(transport)
	}

	if parseErr != nil {
		payload["error"] = strings.TrimSpace(parseErr.Error())
		_, parserFailureClass = parserPolicyMetadata("", parseErr, "", parserFailureClass)

		if parserFailureClass != "" && parserFailureClass != autonomy.ParserFailureNone {
			payload["parser_failure_class"] = string(parserFailureClass)
		}
	}

	r.emitEvent(marshalRunEvent("tool_request_normalizer", payload))
}

func toolRequestNormalizerTransportIsModelTextual(transport string) bool {
	return toolRequestAutonomyTransport(transport) == autonomy.ToolTransportTextualCall
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

	return textutil.Truncate(alias, 96)
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

	if coverage := implementationSourceCoverageSummary(results, 8); coverage != "" {
		b.WriteString("\nSource coverage already read:\n")
		b.WriteString(coverage)
		b.WriteString("\n")
	}

	if implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		b.WriteString("\nImplementation write-ready ledger:\n")
		b.WriteString("- remaining obligation: parser-readable write to a sourced target path, exact no-op evidence, or <<QUESTION>> with the exact blocker\n")
		b.WriteString("- further reads/searches: allowed only for an exact missing edit-safety fact with bounded scope\n")
		if sourced := implementationTargetWriteReadySourcedPathSummary(results, 8); sourced != "" {
			b.WriteString("- source-body evidence paths: ")
			b.WriteString(sourced)
			b.WriteString("\n")
		}
		if inspected := conversationInspectedPathSummary(results); inspected != "" {
			b.WriteString("- recent inspected evidence: ")
			b.WriteString(inspected)
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
			b.WriteString(textutil.Truncate(strings.TrimSpace(detail), 500))
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
			if workspace.AnalyzeVerificationResult(*result).Attempted {
				score += 0.40
			} else {
				score += 0.10
			}
		}

		if workspace.IsWorkspaceWriteTool(strings.TrimSpace(result.Tool)) && strings.TrimSpace(result.Error) == "" {
			score += 0.20
		}

		tool := strings.TrimSpace(result.Tool)
		if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameSourceSanity ||
			workspace.IsLanguageSourceBodyTool(tool) {

			score += 0.10
		}
	}

	content := strings.ToLower(msg.Content)

	editPaths := append([]string{}, brief.RequiredEditPaths...)

	editPaths = append(editPaths, mapKeys(newToolResultEvidenceProjection(allResults).ChangedPathSet())...)

	editScore := 0.0

	for _, path := range editPaths {
		normalized := NormalizeRunnerPath(path)
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
		normalized := NormalizeRunnerPath(path)
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

type implementationSourceCoverage struct {
	Path          string
	TotalLines    int
	Ranges        []lineRange
	UnknownBody   bool
	CompleteRead  bool
	LatestSummary string
}

type lineRange struct {
	Start int
	End   int
}

func implementationSourceCoverageSummary(results []workspace.ToolResult, maxPaths int) string {
	coverage := implementationSourceCoverageByPath(results)
	if len(coverage) == 0 {
		return ""
	}

	paths := make([]string, 0, len(coverage))
	for path := range coverage {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	if maxPaths <= 0 || maxPaths > len(paths) {
		maxPaths = len(paths)
	}

	var b strings.Builder
	for _, path := range paths[:maxPaths] {
		item := coverage[path]
		b.WriteString("  - ")
		b.WriteString(path)
		b.WriteString(": ")
		b.WriteString(item.describe())
		b.WriteString("\n")
	}

	if len(paths) > maxPaths {
		b.WriteString("  - +")
		b.WriteString(strconv.Itoa(len(paths) - maxPaths))
		b.WriteString(" more source-covered path(s)\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func implementationSourceCoverageByPath(results []workspace.ToolResult) map[string]*implementationSourceCoverage {
	coverage := make(map[string]*implementationSourceCoverage)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := ReadPathsFromToolResult(result)
		if len(paths) == 0 {
			continue
		}

		start, end, hasRange := planThenExecuteReadSummaryLineRange(result.Summary)
		total, hasTotal := planThenExecuteReadSummaryTotalLines(result.Summary)

		for _, rawPath := range paths {
			path := NormalizeRunnerPath(rawPath)
			if path == "" || IsNonSourceEvidenceArtifactPath(path) {
				continue
			}
			hasBody := sourceEvidenceHasBody(planThenExecuteToolResultSourceEvidence(result, path))
			if !hasBody && !hasRange {
				continue
			}

			item := coverage[path]
			if item == nil {
				item = &implementationSourceCoverage{Path: path}
				coverage[path] = item
			}

			if hasTotal && total > item.TotalLines {
				item.TotalLines = total
			}
			if hasRange {
				item.Ranges = append(item.Ranges, lineRange{Start: start, End: end})
			} else if hasBody {
				item.UnknownBody = true
			}
			if summary := strings.TrimSpace(result.Summary); summary != "" {
				item.LatestSummary = summary
			}
		}
	}

	for _, item := range coverage {
		item.Ranges = mergeLineRanges(item.Ranges)
		if item.TotalLines > 0 {
			for _, r := range item.Ranges {
				if r.Start <= 1 && r.End >= item.TotalLines {
					item.CompleteRead = true
					break
				}
			}
		}
	}

	return coverage
}

func mergeLineRanges(ranges []lineRange) []lineRange {
	if len(ranges) == 0 {
		return nil
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})

	merged := make([]lineRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Start <= 0 || r.End < r.Start {
			continue
		}
		if len(merged) == 0 || r.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, r)
			continue
		}
		if r.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = r.End
		}
	}

	return merged
}

func (c implementationSourceCoverage) describe() string {
	parts := make([]string, 0, 4)
	if len(c.Ranges) > 0 {
		rangeParts := make([]string, 0, len(c.Ranges))
		for _, r := range c.Ranges {
			rangeParts = append(rangeParts, fmt.Sprintf("%d-%d", r.Start, r.End))
		}
		parts = append(parts, "lines "+strings.Join(rangeParts, ", "))
	}
	if c.TotalLines > 0 {
		parts = append(parts, fmt.Sprintf("total lines %d", c.TotalLines))
	}
	if c.CompleteRead {
		parts = append(parts, "complete file read")
	} else if c.UnknownBody {
		parts = append(parts, "source body observed")
	}
	if len(parts) == 0 && strings.TrimSpace(c.LatestSummary) != "" {
		parts = append(parts, textutil.Truncate(strings.TrimSpace(c.LatestSummary), 180))
	}
	return strings.Join(parts, "; ")
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

			return textutil.Truncate(strings.TrimSpace(content), 500)
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

	return textutil.Prefix(paths, max)
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
			if path == "" || IsNonSourceEvidenceArtifactPath(path) || !delegatedDiscoveryLooksLikeEvidencePath(path) {
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
	projection := newToolResultEvidenceProjection(results)

	for _, packet := range projection.ReadOnlyEvidencePackets() {
		paths = append(paths, packet.SourceBodyPaths...)
		paths = append(paths, packet.InspectedPaths...)
	}

	paths = append(paths, delegatedReadOnlyInspectedImplementationPaths(results)...)
	paths = append(paths, delegatedDiscoveryQueryEvidencePaths(results, max*4)...)
	paths = append(paths, contextPackCandidatePathsFromResults(results, max*4)...)
	paths = append(paths, pinnedPathsFromToolResults(results, max*6)...)
	paths = prioritizedClosureEvidencePaths(paths, results, max*3)

	return textutil.Prefix(uniqueDelegatedDiscoveryPaths(append(directPaths, closurePromptEvidencePaths(paths, max*2)...)), max)
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
	paths := newToolResultEvidenceProjection(results).ChangedPathList()

	for _, path := range directProbePathsFromToolResults(results, max*2) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !scope[normalized] {
			continue
		}

		paths = append(paths, normalized)
	}

	for _, path := range brief.EvidencePaths {
		normalized := NormalizeRunnerPath(path)
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
	paths = textutil.Merge(paths, ledger.RelevantExternalDocumentPaths)

	if ObjectiveAsksImplementationShape(ledger.Objective) {
		paths = textutil.Merge(paths, ledger.ImplementationOwnerPaths())
	}

	if ledger.DiagnosticRelevant {
		paths = textutil.Merge(paths, readOnlySourceLikePaths(ledger.DiagnosticPaths, max*2))
	}

	return readOnlyEvidenceDisplayPaths(paths, max)
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

	paths = append(paths, mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())...)
	if implementationTargetWriteReadySurfaceActive(brief, results) {
		paths = append(paths, newToolResultEvidenceProjection(results).SourceBodyPaths()...)
	}
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
		mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet()),
	} {
		for _, raw := range values {
			path := NormalizeRunnerPath(raw)
			if path != "" {
				scope[path] = true
			}
		}
	}

	return scope
}

func postChildClosureEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	paths := make([]string, 0, max)
	followupAnchors := make([]string, 0, max)
	acceptedPacket := false

	for _, fact := range newToolResultEvidenceProjection(results).ReadOnlyDelegatedEvidenceFacts() {
		decision := readOnlyDelegatedEvidenceDecisionForObjective(objective, fact)
		if len(decision.ClosureEvidencePaths) == 0 {
			followupAnchors = append(followupAnchors, decision.FollowupAnchors...)
			continue
		}

		acceptedPacket = true
		paths = append(paths, decision.ClosureEvidencePaths...)
	}

	if acceptedPacket {
		paths = append(paths, pinnedPathsFromToolResults(results, max*4)...)
	}

	if len(paths) == 0 {
		paths = append(paths, followupAnchors...)
	}

	paths = prioritizedClosureEvidencePaths(delegatedDiscoveryAnswerEvidencePaths(objective, paths), results, max)

	return closurePromptEvidencePaths(paths, max)
}

func readOnlyChildPacketFollowupAnchors(objective string, packet ReadOnlyEvidencePacket) []string {
	if !weakChildPacketFollowupAnchorsAllowed(objective) {
		return nil
	}

	packet = normalizeReadOnlyEvidencePacket(packet)

	var paths []string
	paths = append(paths, packet.InspectedPaths...)
	paths = append(paths, packet.CandidatePaths...)

	return filterChildPacketFollowupAnchors(objective, paths)
}

func delegatedChildPacketFollowupAnchors(objective string, output string) []string {
	if !weakChildPacketFollowupAnchorsAllowed(objective) {
		return nil
	}

	if !delegatedChildPacketNamesFollowupEvidence(output) {
		return nil
	}

	return filterChildPacketFollowupAnchors(objective, groundedChildPacketEvidencePaths(objective, output))
}

func weakChildPacketFollowupAnchorsAllowed(objective string) bool {
	return ObjectiveAsksImplementationShape(objective) || delegatedReadOnlyChildMechanismQuestion(objective)
}

func delegatedChildPacketNamesFollowupEvidence(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" || readOnlyToolControlGuidanceText(lower) {
		return false
	}

	return ContainsAny(lower,
		"inspected paths:",
		"inspected evidence:",
		"candidate paths:",
		"candidate files:",
		"evidence paths:",
		"relevant paths:",
		"relevant files:",
		"key findings:",
	)
}

func filterChildPacketFollowupAnchors(objective string, paths []string) []string {
	filtered := make([]string, 0, len(paths))

	for _, raw := range paths {
		path := normalizeDelegatedDiscoveryEvidencePath(raw)
		if !isGroundedChildPacketEvidencePath(objective, path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return delegatedDiscoveryAnswerEvidencePaths(objective, filtered)
}

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
	RetryExhausted  bool
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
		acceptance = finalAnswerAcceptanceWithTerminalOverride(
			acceptance,
			finalAnswerAcceptanceRejected,
			string(turnViolationTransportProtocolOutput),
			"the final answer contained tool-call transport markup instead of user-visible answer prose",
		)
		decision.Outcome = terminalAnswerTransportBlocked
		decision.PresentedAnswer = "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?"
		decision.StopReason = acceptance.BlockerKind
		decision.Acceptance = acceptance

		return decision
	}

	if acceptance.BlockerKind == "" {
		return decision
	}

	if recovered, recoveredAcceptance, ok := r.recoveredReadOnlyTerminalAnswer(answer, results, acceptance); ok {
		decision.Outcome = terminalAnswerAccepted
		decision.RawAnswer = recovered
		decision.PresentedAnswer = recovered
		decision.StopReason = "read_only_terminal_recovery"
		decision.Acceptance = recoveredAcceptance

		return decision
	}

	if _, ok := finalAnswerAcceptanceViolationKind(acceptance); !ok {
		return decision
	}

	decision.Outcome = terminalAnswerBlocked
	decision.StopReason = acceptance.BlockerKind
	if hostOwnedQuestionCloseoutStopReason(stopReason, answer) {
		decision.StopReason = stopReason
	}
	decision.RetryExhausted = turnFinalAnswerAcceptanceRetryable(acceptance)

	if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") && r.terminalAnswerShouldOverrideRejectedRaw(answer, acceptance) {
		if blockerResult, ok := finalAnswerCompletionGateProjection(
			acceptance,
			"Final answer rejected by blocker check",
			r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, answer, results),
		); ok {
			decision.BlockerResult = blockerResult
			decision.PresentedAnswer = r.completionGateRepeatedBlockerAnswer(blockerResult, results)
			decision.OverrideSource = "finish"
		}
	}

	return decision
}

func hostOwnedQuestionCloseoutStopReason(stopReason string, answer string) bool {
	if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return false
	}

	switch strings.TrimSpace(stopReason) {
	case "implementation_tool_step_budget_blocked",
		"tool_step_budget_exhausted",
		"model_call_budget_exhausted",
		"operational_model_call_limit_exceeded",
		"duration_budget_exhausted",
		"no_progress_budget_exhausted":
		return true
	default:
		return false
	}
}

func (r *Runner) recoveredReadOnlyTerminalAnswer(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) (string, finalAnswerAcceptance, bool) {
	if r == nil {
		return "", finalAnswerAcceptance{}, false
	}
	if !turnFinalAnswerAcceptanceRecoverable(acceptance) {
		return "", finalAnswerAcceptance{}, false
	}

	return r.readOnlyTerminalRecoveryCandidate(acceptance, results)
}

func (r *Runner) readOnlyTerminalRecoveryCandidate(acceptance finalAnswerAcceptance, results []workspace.ToolResult) (string, finalAnswerAcceptance, bool) {
	if r == nil {
		return "", finalAnswerAcceptance{}, false
	}

	if recovered, ok := r.validationStatusTerminalRecoveryAnswer(results); ok {
		recoveredAcceptance := r.evaluateFinalAnswerAcceptance(recovered, results)
		if recoveredAcceptance.BlockerKind == "" {
			return recovered, recoveredAcceptance, true
		}
	}

	brief := readOnlyDirectAnswerAcceptanceLedgerBrief(workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief))
	closeout := readOnlyClosureDecisionForResults(brief, results)
	recovered, ok := closeout.RecoverableTerminalCandidate(r.agentName(), acceptance.ContractAssessment)
	if !ok {
		return "", finalAnswerAcceptance{}, false
	}

	recoveredAcceptance := r.evaluateFinalAnswerAcceptance(recovered, results)
	if recoveredAcceptance.BlockerKind != "" {
		return "", finalAnswerAcceptance{}, false
	}

	return recovered, recoveredAcceptance, true
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

	return acceptance.BlockerKind != string(turnViolationNonAnswerProse) ||
		looksLikeMetaIntentRestatement(answer) ||
		looksLikeGateSatisfactionMetaFinalAnswer(answer) ||
		looksLikeEmbeddedQuestionProtocolFinalAnswer(answer) ||
		looksLikeLowValueSourceInventoryNonAnswer(answer) ||
		finishShouldOverrideSourceEcho(answer)
}

func terminalAnswerContainsTransportProtocol(answer string) bool {
	return IsTextualToolCall(answer) ||
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
		r.emitCompletionStatusFromParams("blocked", acceptance.Reason, acceptance.BlockerKind, results, true, 0)
	}
}

func (r *Runner) emitFinalAnswerTelemetry(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}

	lane := textutil.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), strings.TrimSpace(r.RuntimeBrief.TaskFamily), r.agentName())
	contract := textutil.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), string(workerruntime.ContractDirectAnswer))
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
			"task_family":    textutil.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
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
		"task_family":    textutil.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
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
		payload["relevant_source_body_paths"] = textutil.LimitSlice(acceptance.RelevantSourceBodyPaths, 8)
	}

	r.emitEvent(marshalRunEvent("final_answer_blocker_override", payload))
}

func (r *Runner) emitPostWriteVerificationTelemetry(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(result.Error) != "" || !workspace.AnalyzeVerificationResult(result).Successful {
		return false
	}

	command := postWriteVerificationTelemetryCommand(req, result)
	if command == "" {
		return false
	}

	analysis := workspace.AnalyzeShellCommand(command)
	usesFullSuite := analysis.FullSuite
	coverage := "targeted verification"
	confidence := "medium"
	riskLevel := "low"
	expectedFailures := []string{"verification failures"}
	recommendation := "keep targeted tests close to the changed surface before widening scope"

	switch analysis.Verification {
	case workspace.ShellVerificationScriptSyntax:
		coverage = "targeted script syntax verification"
		confidence = "low"
		expectedFailures = []string{"shell syntax errors"}
		recommendation = "syntax verification passed; run targeted tests when behavior changes beyond script parsing"
	case workspace.ShellVerificationFullSuite:
		coverage = "full-suite verification"
		confidence = "high"
		riskLevel = "high"
		expectedFailures = []string{"test failures"}
		recommendation = "record full-suite success for similar high-risk surfaces"
	case workspace.ShellVerificationTest:
		coverage = "targeted project verification"
		expectedFailures = []string{"test failures"}
	case workspace.ShellVerificationBuild:
		coverage = "targeted project verification"
		expectedFailures = []string{"build failures"}
	case workspace.ShellVerificationCheck:
		coverage = "targeted project verification"
		expectedFailures = []string{"check failures"}
	}

	plan := map[string]any{
		"commands":          []string{command},
		"uses_full_suite":   usesFullSuite,
		"coverage":          coverage,
		"confidence":        confidence,
		"risk_level":        riskLevel,
		"expected_failures": expectedFailures,
		"repair_policy":     "repair failing checks before widening scope",
	}
	r.emitEvent(marshalRunEvent("verification_plan", plan))

	feedback := map[string]any{
		"commands":               []string{command},
		"failed_commands":        []string{},
		"confidence":             confidence,
		"coverage":               plan["coverage"],
		"risk_level":             plan["risk_level"],
		"learned_recommendation": recommendation,
	}
	r.emitEvent(marshalRunEvent("verification_feedback", feedback))

	return true
}

func postWriteVerificationTelemetryCommand(req workspace.ToolRequest, result workspace.ToolResult) string {
	if strings.TrimSpace(req.Tool) == workspace.ToolNameShell {
		if command := strings.TrimSpace(req.Command); command != "" {
			return command
		}

		if command := workspace.ShellCommandFromToolResult(result); command != "" {
			return command
		}
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameShell {
		if command := workspace.ShellCommandFromToolResult(result); command != "" {
			return command
		}
	}

	if strings.TrimSpace(req.Tool) == workspace.ToolNameSourceSanity && strings.TrimSpace(req.Path) != "" {
		return "source_sanity " + NormalizeRunnerPath(req.Path)
	}

	if semanticlanguage.IsDiagnosticToolName(req.Tool) {
		return strings.TrimSpace(req.Tool)
	}

	return ""
}

// requiredEditPathsProgressMessage returns a compact authoritative
// progress checklist that the runner injects into turnMessages after each
// successful write tool result on a brief that has required_edit_paths.
// The message gives the model running ground truth — paths actually
// changed (✓) and paths still pending (✗) — derived from
// the tool-result evidence projection rather than the model's conversation
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

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()
	if len(changed) == 0 {
		return ""
	}

	var (
		done    []string
		pending []string
	)

	for _, raw := range required {
		path := NormalizeRunnerPath(raw)
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
	kind = strings.TrimSpace(kind)
	reason = strings.TrimSpace(reason)
	if finalAnswerObjectiveRelevanceAdvisoryKind(kind) {
		return "objective relevance diagnostics are advisory. Accept the model's final answer unless a mechanical blocker such as missing source-body evidence, protocol markup, permissions, or required evidence applies."
	}

	switch kind {
	case string(turnViolationRequiredEditPathsRemaining):
		missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())
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
	case string(turnViolationUserVisibleBlocker):
		return "the final answer was a <<QUESTION>>: blocker; surface the path-specific blocker reason for each unfinished required edit path."
	case string(turnViolationReadOnlyProtocolBlocker):
		return "the final answer exposed internal <<QUESTION>> blocker protocol in a read-only direct-answer lane. Answer in normal prose from gathered evidence, or state the exact unresolved claim and the smallest useful next probe without raw blocker markup."
	case string(turnViolationProtocolMetaFinalAnswer):
		if reason == string(turnReasonNonSelfContainedFinalAnswer) {
			return "the final answer referred to a hidden, prior, or rejected answer instead of standing on its own. Reply with self-contained user-visible prose from the gathered evidence; do not say \"the answer above\", \"previous answer\", or similar."
		}
		return "the final answer repeated internal validation status instead of answering the user. Answer in normal prose from the gathered evidence; omit validation status and internal protocol markers."
	case string(turnViolationUnexecutedToolRequestAnswer):
		return "the final answer was an unexecuted workspace-tool request payload. " + workspaceLookupTransportInstruction(recoveryToolTransportMode(r.currentPlanningTransport())) + " Or answer in normal prose from the available evidence."
	case string(turnViolationNonAnswerProse):
		return "the final answer looks like transport-level noise (tool-call markup, echoed user question, or tool-failure language) rather than an actual answer. Reply with substantive prose that addresses the user's request, or use <<QUESTION>>: to state a concrete blocker."
	case string(turnViolationSourceGroundingUnmet):
		return reason
	case string(turnViolationRequiredEvidenceUnmet):
		if r != nil {
			missing := missingRequiredEvidence(r.RuntimeBrief.RequiredEvidence, answer, results)
			return "the answer does not satisfy the required evidence contract. Missing: " + strings.Join(missing, ", ") + ". Address each missing obligation in your answer, or use <<QUESTION>>: to state a concrete blocker."
		}

		return "the answer does not satisfy the required evidence contract. Address each missing obligation, or use <<QUESTION>>: to state a concrete blocker."
	case string(turnViolationValidationStatusContradictsEvidence):
		return reason
	case string(turnViolationRejectedMutationPathsRemaining):
		var requiredPaths []string

		if r != nil {
			requiredPaths = r.RuntimeBrief.RequiredEditPaths
		}

		if missing := unresolvedRejectedMutationPaths(results, requiredPaths); len(missing) > 0 {
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
		IsTextualToolCall(reply) ||
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
		paths := textutil.Merge(exactProbeEvidencePaths(results), pathparse.MentionedWorkspacePaths(answer))
		for _, packet := range newToolResultEvidenceProjection(results).ReadOnlyEvidencePackets() {
			if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
				paths = textutil.Merge(paths, packet.SourceBodyPaths, packet.InspectedPaths)
			}
		}

		paths = textutil.Merge(paths, delegatedReadOnlyInspectedImplementationPaths(results))
		if len(paths) == 0 {
			paths = textutil.Merge(paths, mentionedEvidencePathsFromToolResultsForObjective(results, brief.Objective, 24))
		}

		paths = prioritizedClosureEvidencePaths(paths, results, 8)
		paths = closurePromptEvidencePaths(paths, 8)

		if len(paths) > 0 {
			return paths
		}
	}

	paths := textutil.Merge(pinnedPathsFromToolResults(results, 8), pathparse.MentionedWorkspacePaths(answer))
	for _, packet := range newToolResultEvidenceProjection(results).ReadOnlyEvidencePackets() {
		if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
			paths = textutil.Merge(paths, packet.SourceBodyPaths, packet.InspectedPaths)
		}
	}

	paths = delegatedDiscoveryAnswerEvidencePaths(brief.Objective, paths)

	return closurePromptEvidencePaths(paths, 8)
}

func acceptedImplementationEvidencePaths(results []workspace.ToolResult) []string {
	paths := newToolResultEvidenceProjection(results).ChangedPathList()

	paths = append(paths, directProbePathsFromToolResults(results, 8)...)

	return closurePromptEvidencePathsPreservingTests(paths, 8)
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

	return textutil.Prefix(cleaned, max)
}

func exactProbeEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if !workspace.IsLanguageSourceBodyTool(result.Tool) {
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
	if r.lanePolicy().skipExplorationDelegationNudge {
		return ""
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}

	allowedTools := r.workspaceAdvertisedAllowedTools()
	if !slices.Contains(allowedTools, workspace.ToolNameTask) {
		return ""
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 || hasSuccessfulTaskResult(results) {
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

	return fmt.Sprintf("You have already used %d read/search steps in the main loop without a write. A final answer is available if the current evidence already supports a concrete answer. Only if one broad missing claim genuinely remains and a bounded read_only child would close it faster than more local probing, you may delegate one bounded read_only task with the task tool.", readCallsSinceWrite)
}

func prefersEarlyReadOnlyDelegation(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return false
	}

	for _, policy := range brief.SearchPolicy {
		if textutil.EqualTrimFold(policy, "delegate_broad_discovery_when_needed") {
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

		if workspace.IsSourceBodyInspectionTool(result.Tool) {
			return true
		}
	}

	return false
}

type runnerTurnController struct {
	runner                     *Runner
	ctx                        context.Context
	policy                     RunPolicy
	opts                       *llama.ChatOptions
	metrics                    *runnerMetrics
	requestBudget              *runnerRequestBudgetState
	recorder                   *runnerTurnRecorder
	autonomyRecorder           *runnerAutonomyRecorder
	turnMessages               *[]llama.ChatMessage
	collectedResults           *[]workspace.ToolResult
	rejectedActions            map[string]int
	rejectionRecoveryNudgeUsed *bool
	completionGateRejections   *int
	consecutiveRejected        *int
	consecutiveNoProgress      *int
	currentFingerprint         *string
}

func (c *runnerTurnController) rejectTool(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, summary string, errorText string) (string, error, bool) {
	if c == nil || c.recorder == nil {
		return "", nil, false
	}

	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Error:   errorText,
	}
	if c.requestBudget != nil && c.currentFingerprint != nil && strings.TrimSpace(*c.currentFingerprint) != "" {
		c.requestBudget.fingerprint.rejected[*c.currentFingerprint] = truncForRetryReason(summary)
	}

	countAsDeadEnd := !postReadinessContinuationRejectionSummary(summary)
	if countAsDeadEnd {
		if action := toolActionClass(req); action != "" && c.rejectedActions != nil {
			c.rejectedActions[action]++
		}

		if c.consecutiveRejected != nil {
			*c.consecutiveRejected++
		}
	}

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, "tool_rejected", runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})

	if c.consecutiveRejected != nil && *c.consecutiveRejected >= 2 {
		if c.rejectionRecoveryNudgeUsed != nil && !*c.rejectionRecoveryNudgeUsed {
			if nudge := buildRejectionRecoveryNudge(req, c.snapshotResults(), c.runner.Workspace, c.runner.RuntimeBrief, c.runner.agentName(), false, false); nudge != "" {
				*c.rejectionRecoveryNudgeUsed = true
				if c.turnMessages != nil {
					*c.turnMessages = append(*c.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})
				}

				c.runner.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})

				*c.consecutiveRejected = 0

				return "", nil, false
			}
		}

		// Conversation lanes: the "last chance before harness ends"
		// force-complete contradicts the investigation directive.
		// Instead of force-completing, let the model continue.
		if c.runner != nil && !c.runner.lanePolicy().forceAnswerAfterRejection {
			*c.consecutiveRejected = 0
			return "", nil, false
		}

		answer, err := c.completeWithoutMoreToolsWithDefaultCloseout()

		return answer, err, true
	}

	return "", nil, false
}

func (c *runnerTurnController) rejectCompletion(step int, activeToolCall *llama.ToolCall, reply llama.ChatMessage, result workspace.ToolResult) {
	if c == nil || c.recorder == nil {
		return
	}

	if c.completionGateRejections != nil {
		*c.completionGateRejections++
	}

	if c.metrics != nil {
		c.metrics.CompletionGateRejections++
	}

	historyReply := reply
	if !c.currentOpts().PreserveThinking {
		historyReply.ReasoningContent = ""
	}

	if c.turnMessages != nil && !completionGateRejectedReplyHiddenFromHistory(c.runner, result) {
		*c.turnMessages = append(*c.turnMessages, completionGateRejectedHistoryMessage(c.runner, historyReply, result, c.snapshotResults()))
	}

	c.runner.emitThinkingTrace(step, "completion_gate_reply_preserved", historyReply, c.currentOpts(), true)

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, workspace.ToolRequest{Tool: "completion_gate"}, result, "completion_gate_rejected", runnerRecordOptions{
		IncrementNoProgress: true,
		SetLastToolKey:      false,
	})
}

func completionGateRejectedReplyHiddenFromHistory(r *Runner, result workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Tool) != "completion_gate" {
		return false
	}

	return readOnlyDirectAnswerLane(r.RuntimeBrief) && !runnerBriefRequiresMutation(r.RuntimeBrief)
}

func completionGateRejectedHistoryMessage(r *Runner, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) llama.ChatMessage {
	message := llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          reply.Content,
		ReasoningContent: reply.ReasoningContent,
	}
	if !shouldCompactRejectedCompletionReply(r, reply, result, results) {
		return message
	}

	content := strings.TrimSpace(reply.Content)

	message.Content = fmt.Sprintf(
		"[omitted rejected final answer: %d chars. The completion gate rejected it before required workspace changes were recorded: %s. Next action must satisfy the gate with an allowed tool call or a concrete blocker. Preview: %s]",
		len(content),
		textutil.Truncate(strings.TrimSpace(result.Error), 240),
		textutil.Truncate(strings.TrimSpace(content), 500),
	)
	message.ReasoningContent = ""

	return message
}

func shouldCompactRejectedCompletionReply(r *Runner, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Tool) != "completion_gate" {
		return false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return false
	}

	content := strings.TrimSpace(reply.Content)
	if len(content) < 4000 {
		return false
	}

	return strings.TrimSpace(result.Error) != ""
}

func (c *runnerTurnController) recordParseFailure(step int, activeToolCall *llama.ToolCall, raw string, parseErr error, transport string, parserFailureClass autonomy.ParserFailureClass, suppressModelMessage bool) {
	if c == nil || c.recorder == nil || parseErr == nil {
		return
	}

	decision := malformedToolParseFailureDecision(raw, parseErr, transport, parserFailureClass)

	c.recorder.activeToolCall = activeToolCall

	if c.autonomyRecorder != nil {
		c.autonomyRecorder.emitPolicyResult(decision.Request, decision.Policy)
	}

	c.recorder.recordWithOptions(step, decision.Request, decision.Result, parserRecoveryRecordReason, runnerRecordOptions{
		CountRejected:        true,
		SetLastToolKey:       false,
		SuppressModelMessage: suppressModelMessage,
	})
}

const (
	parserRecoveryToolName         = "tool_parse"
	parserRecoveryRecordReason     = "tool_parse_rejected"
	parserRecoverySummaryMalformed = "Malformed tool request rejected"
	parserRecoverySummaryUnsafe    = "Unsafe tool-shaped request rejected"
)

type malformedToolPolicyDecision struct {
	Request workspace.ToolRequest
	Result  workspace.ToolResult
	Policy  runnerAutonomyPolicyEventInput
}

func (r *Runner) malformedToolRecoveryPrompt(raw string, parseErr error, results []workspace.ToolResult) string {
	if r == nil || parseErr == nil {
		return ""
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var lines []string

	if IsTextualToolCall(raw) {
		lines = append(lines,
			"The last response was rejected by the tool parser because it used XML/HTML/DSML-style tool-call markup instead of the active tool transport.",
			malformedToolRecoveryActionInstruction(recoveryToolTransportMode(r.currentPlanningTransport())),
		)
	} else {
		lines = append(lines,
			"The last response was rejected by the tool parser because it was not one valid tool call.",
			malformedToolRecoveryActionInstruction(recoveryToolTransportMode(r.currentPlanningTransport())),
		)
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		if ledger := readOnlyEvidenceLedgerGuidanceForBrief(brief, results); ledger != "" {
			lines = append(lines, ledger)
		}
		if r.lastCompletionGateSourceGrounding.hasTypedSignal() && len(r.lastCompletionGateSourceGrounding.Paths) > 0 {
			lines = append(lines, "If the malformed tool text was trying to repair missing source evidence, the unsupported citation path(s) are: "+formatInlinePathList(r.lastCompletionGateSourceGrounding.Paths, 4)+". Valid strategies are to read one missing body, cite only body-backed evidence, qualify candidate-only paths as unverified, or state the exact blocker.")
		}
	}

	return strings.Join(lines, "\n\n")
}

func (r *Runner) unsafeToolShapeRecoveryPrompt(normalization toolRequestSyntaxNormalization, results []workspace.ToolResult) string {
	if r == nil || strings.TrimSpace(normalization.EventOutcome) != toolRequestNormalizerOutcomeUnsafeToInfer {
		return ""
	}

	lines := []string{
		"The last response looked like tool-shaped arguments, but the harness cannot safely infer that into an executable tool call.",
	}

	if implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		lines = append(lines, "Do not put tool arguments in answer prose. The active request is the workspace write follow-up; prose answers to the earlier identification question do not satisfy this phase. Use the active native tool-call transport for one callable write or "+implementationTargetWriteReadySourceProbeToolsPhrase+" tool, or answer <<QUESTION>> with the exact blocker/no-op evidence.")
		lines = append(lines, "The implementation lane is target-write-ready. "+implementationTargetWriteReadyRecoveryInstruction(results))
	} else {
		lines = append(lines, malformedToolRecoveryActionInstruction(recoveryToolTransportMode(r.currentPlanningTransport())))
	}

	return strings.Join(lines, "\n\n")
}

func (r *Runner) ambiguousModelOutputRecoveryPrompt(normalization toolRequestSyntaxNormalization, results []workspace.ToolResult) string {
	if r == nil || strings.TrimSpace(normalization.EventOutcome) != toolRequestNormalizerOutcomeUnsafeToInfer {
		return ""
	}

	if implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) {
		lines := []string{
			"The last response was prose, not an implementation action. The harness did not infer or execute any write from it.",
			"The active request is the workspace write follow-up; do not answer the earlier identification question again. Prose is invalid in target-write-ready unless it is the exact <<QUESTION>> blocker/no-op shape.",
			"The implementation lane is target-write-ready. " + implementationTargetWriteReadyRecoveryInstruction(results),
			"If the previous text was intended as an edit, issue it through one active write tool call. If it was intended as a blocker or no-op, answer <<QUESTION>> with the exact blocker/no-op evidence. If one edit-safety fact is still missing, take one bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " probe with `named_gap` for that exact fact.",
		}
		return strings.Join(lines, "\n\n")
	}

	lines := []string{
		"The last response was not an executable tool call and was not accepted as a final answer. The harness did not infer or execute any action from it.",
	}

	return strings.Join(lines, "\n\n")
}

func (r *Runner) unsafeToolShapeRecoveryBeforeCompletionGateActive(results []workspace.ToolResult) bool {
	if r == nil {
		return false
	}

	return implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results)
}

func malformedToolParseFailureDecision(raw string, parseErr error, transport string, parserFailureClass autonomy.ParserFailureClass) malformedToolPolicyDecision {
	policyTransport, parserFailureClass := parserPolicyMetadata(raw, parseErr, transport, parserFailureClass)

	return malformedToolPolicyDecision{
		Request: workspace.ToolRequest{Tool: parserRecoveryToolName},
		Result: workspace.ToolResult{
			Tool:    parserRecoveryToolName,
			Summary: parserRecoverySummaryMalformed,
			Error:   toolParseErrorFeedback(raw, parseErr, ""),
		},
		Policy: runnerAutonomyPolicyEventInput{
			Decision:      autonomy.PolicyReject,
			PackID:        autonomy.ContractPackParserToolRequest,
			InvariantID:   malformedToolPolicyInvariantToolRequest,
			ReasonCode:    "malformed_tool_request",
			Transport:     policyTransport,
			ParserFailure: parserFailureClass,
			Result:        "rejected",
			ErrorClass:    parserRecoveryToolName,
			Source:        "malformed_tool_policy",
		},
	}
}

func unsafeToolShapeDecision(normalization toolRequestSyntaxNormalization, targetWriteReady bool) malformedToolPolicyDecision {
	transport, parserFailureClass := parserPolicyMetadata(normalization.Raw, nil, normalization.EventTransport, autonomy.ParserFailureUnsupportedTransport)
	if parserFailureClass == "" || parserFailureClass == autonomy.ParserFailureNone {
		parserFailureClass = autonomy.ParserFailureUnsupportedTransport
	}

	return malformedToolPolicyDecision{
		Request: workspace.ToolRequest{Tool: parserRecoveryToolName},
		Result: workspace.ToolResult{
			Tool:    parserRecoveryToolName,
			Summary: parserRecoverySummaryUnsafe,
			Error:   unsafeToolShapeErrorFeedback(transport, targetWriteReady),
		},
		Policy: runnerAutonomyPolicyEventInput{
			Decision:      autonomy.PolicyReject,
			PackID:        autonomy.ContractPackParserToolRequest,
			InvariantID:   malformedToolPolicyInvariantToolRequest,
			ReasonCode:    "unsafe_tool_shape",
			Transport:     transport,
			ParserFailure: parserFailureClass,
			Result:        "rejected",
			ErrorClass:    parserRecoveryToolName,
			Source:        "unsafe_tool_shape_policy",
		},
	}
}

func malformedToolRecoveryActionInstruction(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return "Do not emit tags such as `<n_tool_calls>`, `<invoke>`, `<janus_tool>`, or textual tool JSON as answer prose. If a tool is useful, use the active native tool-call transport exposed by this runtime; otherwise answer in plain user-visible prose."
	}
	return "If a tool is useful, emit exactly one parser-readable tool call through the active tool transport; otherwise answer in plain user-visible prose."
}

func workspaceLookupTransportInstruction(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return "If a workspace lookup is needed, use the active native tool-call transport and wait for the result."
	}
	return "If a workspace lookup is needed, issue it as a parser-readable tool call with the `tool` field and wait for the result."
}

func toolParseErrorFeedback(raw string, parseErr error, transportMode string) string {
	if parseErr == nil {
		return modelFacingToolParseError("Emit exactly one valid tool call or answer normally.", transportMode)
	}

	message := parseErr.Error()
	lower := strings.ToLower(message + "\n" + raw)

	if strings.Contains(lower, "toolrequest.edits") && strings.Contains(lower, "string") {
		return message + ". submit_plan.edits was emitted as a quoted string. Emit `edits` as a real JSON array value, not a string. Each edit object must use a real path and exactly one executable edit shape: insert_after_line/content for small inline insertions, old_text/new_text for exact replacements or deletions, start_line/end_line/content for an inclusive current-source range, or replace_all with full new_text for missing/new files only. Existing source/test files must use bounded anchored edits. For native tool-call backends, pass the submit_plan arguments object directly with `rationale` and an `edits` array. Edit bodies must be complete executable source text, not placeholders or prose."
	}

	if IsTextualToolCall(raw) {
		return modelFacingToolParseError(message+". The payload looks like XML/HTML/DSML-style tool-call markup. Do not emit tags such as <n_tool_calls>, <invoke>, or <janus_tool>. Use the runtime's active tool-call mechanism for tools, or answer in plain prose.", transportMode)
	}

	return modelFacingToolParseError(message+". Emit exactly one valid tool call or answer normally.", transportMode)
}

func unsafeToolShapeErrorFeedback(transport autonomy.ToolTransport, targetWriteReady bool) string {
	if targetWriteReady {
		return modelFacingToolParseError("The payload was tool-shaped, but it was not an executable tool call. Use the active native tool-call transport for one callable write/read/search tool, or answer <<QUESTION>> with the exact blocker/no-op evidence. Prose final answers cannot satisfy the current target-write-ready phase.", workerruntime.RecoveryToolTransportNative)
	}

	switch transport {
	case autonomy.ToolTransportNativeCall:
		return modelFacingToolParseError("The payload was tool-shaped, but it was not an executable native tool call. Use the active native tool-call transport for tools, or answer in plain prose.", workerruntime.RecoveryToolTransportNative)
	default:
		return modelFacingToolParseError("The payload was tool-shaped, but it was unsafe to infer as a tool call. Emit exactly one valid tool call through the active transport, or answer in plain prose.", "")
	}
}

func (c *runnerTurnController) recordUnsafeToolShape(step int, activeToolCall *llama.ToolCall, normalization toolRequestSyntaxNormalization, suppressModelMessage bool) {
	if c == nil || c.recorder == nil {
		return
	}

	decision := unsafeToolShapeDecision(normalization, true)

	c.recorder.activeToolCall = activeToolCall

	if c.autonomyRecorder != nil {
		c.autonomyRecorder.emitPolicyResult(decision.Request, decision.Policy)
	}

	c.recorder.recordWithOptions(step, decision.Request, decision.Result, parserRecoveryRecordReason, runnerRecordOptions{
		CountRejected:        true,
		SetLastToolKey:       false,
		SuppressModelMessage: suppressModelMessage,
	})
}

func (c *runnerTurnController) recordRejectedResult(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, result workspace.ToolResult, reason string) {
	if c == nil || c.recorder == nil {
		return
	}

	if c.requestBudget != nil && c.currentFingerprint != nil && strings.TrimSpace(*c.currentFingerprint) != "" {
		c.requestBudget.fingerprint.rejected[*c.currentFingerprint] = truncForRetryReason(result.Summary)
	}

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, reason, runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})
}

func (c *runnerTurnController) snapshotResults() []workspace.ToolResult {
	if c == nil || c.collectedResults == nil {
		return nil
	}

	return *c.collectedResults
}

func (c *runnerTurnController) currentMessages() []llama.ChatMessage {
	if c == nil || c.turnMessages == nil {
		return nil
	}

	return *c.turnMessages
}

func (c *runnerTurnController) currentOpts() llama.ChatOptions {
	if c == nil || c.opts == nil {
		return llama.ChatOptions{}
	}

	return *c.opts
}

func (c *runnerTurnController) completeWithoutMoreToolsWithDefaultCloseout() (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}

	mode := c.noToolsCloseoutMode()
	reason := "Tool budget is exhausted. Tools are now disabled."
	if mode == noToolsCloseoutDeterministicRequired && c.metrics != nil && c.policy.shouldStopForOperationalModelCalls(c.metrics.ModelCalls) {
		reason = "Operational model-call limit is exhausted. Tools are now disabled."
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), reason, c.metrics, mode)
}

func (c *runnerTurnController) completeWithoutMoreToolsWithReason(prompt string) (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), prompt, c.metrics, c.noToolsCloseoutMode())
}

func (c *runnerTurnController) completeWithoutMoreToolsWithReasonMode(prompt string, mode noToolsCloseoutMode) (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	if mode == "" {
		mode = c.noToolsCloseoutMode()
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(c.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), prompt, c.metrics, mode)
}

func (c *runnerTurnController) noToolsCloseoutMode() noToolsCloseoutMode {
	if c == nil || c.metrics == nil {
		return noToolsCloseoutModelAllowed
	}

	if c.policy.shouldStopForOperationalModelCalls(c.metrics.ModelCalls) {
		return noToolsCloseoutDeterministicRequired
	}

	return noToolsCloseoutModelAllowed
}

const (
	ReasonCompletionGateRejected failures.Reason = 2001 + iota
	ReasonAgentTextualToolCall
	ReasonAgentToolRequest
	ReasonAgentEmptyResponse
)

var (
	ErrCompletionGateRejected = failures.New(failures.FailedPrecondition, ReasonCompletionGateRejected, "completion gate rejected final answer")
	ErrAgentTextualToolCall   = failures.New(failures.FailedPrecondition, ReasonAgentTextualToolCall, "agent emitted textual tool call markup instead of answer")
	ErrAgentToolRequest       = failures.New(failures.FailedPrecondition, ReasonAgentToolRequest, "agent emitted a tool request instead of answer")
	ErrAgentEmptyResponse     = failures.New(failures.FailedPrecondition, ReasonAgentEmptyResponse, "agent returned an empty response")
)

type WriteDecision struct {
	Allowed       bool
	InvariantID   string
	RequiredPaths []string
	ChangedPaths  []string
	ReasonCode    string
}

func (d WriteDecision) PendingRequiredPaths() []string {
	changed := make(map[string]struct{}, len(d.ChangedPaths))
	for _, path := range d.ChangedPaths {
		changed[path] = struct{}{}
	}

	var pending []string
	for _, path := range d.RequiredPaths {
		if _, ok := changed[path]; ok {
			continue
		}
		pending = append(pending, path)
	}
	return pending
}

// SessionConfig defines the loop-owned lifecycle budget. A zero MaxSteps means
// the controller is responsible for ending the session.
type SessionConfig struct {
	MaxSteps int
}

// SessionState is the small mutable state owned by the loop package. Concrete
// runner adapters keep domain state behind SessionController.
type SessionState struct {
	Step int
}

// StepResult is the controller's response for one loop step.
type StepResult struct {
	Answer string
	Err    error
	Done   bool
}

// SessionController is the narrow lifecycle adapter implemented by the root
// runner while orchestration is migrated out of the legacy monolith.
type SessionController interface {
	Bootstrap(context.Context, *SessionState) error
	RunStep(context.Context, *SessionState) StepResult
	Finish(context.Context, *SessionState, StepResult) (string, error)
	Close(context.Context, *SessionState)
}

// RunSession owns the agent session lifecycle: bootstrap, bounded step loop,
// finish, and close. Domain packages provide the step adapter; this package
// owns the sequencing contract.
func RunSession(ctx context.Context, cfg SessionConfig, controller SessionController) (answer string, err error) {
	state := &SessionState{}
	if controller == nil {
		return "", nil
	}
	defer controller.Close(ctx, state)

	if err := controller.Bootstrap(ctx, state); err != nil {
		return controller.Finish(ctx, state, StepResult{Err: err, Done: true})
	}

	for cfg.MaxSteps <= 0 || state.Step < cfg.MaxSteps {
		state.Step++
		result := controller.RunStep(ctx, state)
		if result.Done || result.Err != nil {
			return controller.Finish(ctx, state, result)
		}
	}

	return controller.Finish(ctx, state, StepResult{Done: true})
}
