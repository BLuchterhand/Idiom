package agent

import (
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
)

type RunPolicy struct {
	MaxToolSteps       int
	MaxNoProgressTurns int
	MaxContextMessages int
	MaxContextChars    int
	MaxModelCalls      int
	MaxDuration        time.Duration
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
		policy = scaleMaxModelCallsByRequiredPaths(policy, len(r.RuntimeBrief.RequiredEditPaths))
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxToolSteps; briefLimit > 0 && (policy.MaxToolSteps <= 0 || briefLimit < policy.MaxToolSteps) {
		policy.MaxToolSteps = briefLimit
	}

	if briefLimit := r.RuntimeBrief.Budget.MaxModelCalls; briefLimit > 0 && (policy.MaxModelCalls <= 0 || briefLimit < policy.MaxModelCalls) {
		policy.MaxModelCalls = briefLimit
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

// scaleMaxModelCallsByRequiredPaths grows tight model-call ceilings for
// multi-file implementation atoms. Tool-step scaling alone is not enough:
// each successful tool result needs a follow-up model call, so a six-path atom
// capped at three model calls can spend the whole run on legitimate scoped
// reads and never receive a write/blocker turn.
func scaleMaxModelCallsByRequiredPaths(policy RunPolicy, pathCount int) RunPolicy {
	if pathCount <= 1 {
		return policy
	}

	scaled := maxModelCallsBase + 2*(pathCount-1)
	if scaled > maxModelCallsCeiling {
		scaled = maxModelCallsCeiling
	}

	if policy.MaxModelCalls >= maxModelCallsBase && policy.MaxModelCalls < scaled {
		policy.MaxModelCalls = scaled
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
	// maxModelCallsBase is the smallest normal mutation-lane model budget.
	// Values below it are treated as deliberately tight tests or operator
	// limits; values at or above it scale with required edit paths.
	maxModelCallsBase = 3
	// maxModelCallsCeiling keeps scaled model-call budgets bounded while
	// leaving already-higher explicit policies alone.
	maxModelCallsCeiling = 24
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

func (p RunPolicy) shouldStopForModelCalls(used int) bool {
	return p.MaxModelCalls > 0 && used >= p.MaxModelCalls
}

func (p RunPolicy) shouldStopForDuration(startedAt time.Time) bool {
	return p.MaxDuration > 0 && time.Since(startedAt) >= p.MaxDuration
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
	Lane               string   `json:"lane"`
	TaskFamily         string   `json:"task_family,omitempty"`
	Contract           string   `json:"contract,omitempty"`
	Status             string   `json:"status"`
	Reason             string   `json:"reason,omitempty"`
	BlockerKind        string   `json:"blocker_kind,omitempty"`
	RequiresEdit       bool     `json:"requires_edit,omitempty"`
	EditAttempted      bool     `json:"edit_attempted"`
	ChangedPaths       []string `json:"changed_paths,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker,omitempty"`
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
	Lane               string   `json:"lane"`
	Phase              string   `json:"phase,omitempty"`
	Step               int      `json:"step"`
	Reason             string   `json:"reason"`
	LastTool           string   `json:"last_tool,omitempty"`
	LastTarget         string   `json:"last_target,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	ResultError        string   `json:"result_error,omitempty"`
	ResultOutputChars  int      `json:"result_output_chars,omitempty"`
	EvidencePaths      []string `json:"evidence_paths,omitempty"`
	CandidateSymbols   []string `json:"candidate_symbols,omitempty"`
	EvidenceStatus     []string `json:"evidence_status,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	InvalidNextActions []string `json:"invalid_next_actions,omitempty"`
	NextValidActions   []string `json:"next_valid_actions,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
	RecoveryCard       string   `json:"recovery_card,omitempty"`
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
