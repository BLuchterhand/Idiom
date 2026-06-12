// Package agent: LanePolicy defines per-lane behavioral customization points
// that were previously scattered as inline TaskFamily/Contract guards across
// the runner, turn controller, lane-policy preflight, and discovery redirect.
//
// Each lane (conversation, delegated_read_only, plan-execute planner, etc.)
// implements this interface once. The runner dispatches through the policy
// instead of reaching for string comparisons against RuntimeBrief fields.
//
// When adding a new lane, create a single implementation here and wire it
// at runner construction time. Do not add new inline TaskFamily guards to
// runner.go.

package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

// LanePolicy encapsulates lane-specific decisions that were previously
// guarded by scattered TaskFamily/Contract string comparisons.
type LanePolicy interface {
	// Name returns a stable identifier for telemetry and logging.
	Name() string

	// --- Budget & termination ---

	// SkipDurationBudget returns true when the lane should not be
	// terminated by elapsed-wall-clock duration limits.
	SkipDurationBudget() bool
	// SkipModelCallBudget returns true when the lane should not be
	// terminated by model-call-count limits.
	SkipModelCallBudget() bool
	// SkipFingerprintHardBlock returns true when the lane should
	// clear the force-completion prompt after a fingerprint hard
	// block fires, allowing the model to continue instead of being
	// force-completed.
	SkipFingerprintHardBlock() bool
	// SkipSynthesisNudge returns true when the generic "answer now"
	// synthesis budget nudge should be suppressed for this lane.
	SkipSynthesisNudge() bool
	// SkipExplorationDelegationNudge returns true when the
	// exploration-delegation nudge (suggesting the model delegate)
	// should be suppressed.
	SkipExplorationDelegationNudge() bool
	// ShouldForceAnswerAfterRejection returns true when the lane
	// should force-complete after N consecutive tool rejections
	// (the "last chance before harness ends" path in turn control).
	ShouldForceAnswerAfterRejection() bool

	// --- Nudge schedule ---

	// CallCountMilestones returns (firstMilestone, nudgeInterval)
	// derived from the model context window. A milestone of 0 means
	// the lane does not use call-count awareness nudges.
	CallCountMilestones(ctxSize int) (firstMilestone, nudgeInterval int)
	// AutoCaptureFindings returns true when the lane should
	// auto-capture inspected path summaries to .idiom/notes/ at
	// the first context-derived milestone.
	AutoCaptureFindings() bool
	// ShouldHaveNoteTool returns true when the lane should expose
	// the note tool for intermediate-finding capture. The contract
	// parameter is the lane's RuntimeBrief.Contract value.
	ShouldHaveNoteTool(contract string) bool

	// --- Evidence & readiness ---

	// EvidenceSufficientForObjective returns true when the lane's
	// NamedEvidencePaths are all satisfied by bounded source reads.
	EvidenceSufficientForObjective(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool
	// NamedEvidencePaths returns the workspace paths that must be
	// covered by bounded source reads before evidence is sufficient.
	NamedEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string
	// EvidenceSatisfiedForceAnswerReady returns true when the
	// lane's evidence threshold is met and no mutation/verification
	// obligations remain.
	EvidenceSatisfiedForceAnswerReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool

	// --- Nudge text ---

	// RejectionRecoveryNudge returns a system-message nudge when
	// the lane should intervene after consecutive tool rejections.
	// Returns "" when the lane does not use rejection-recovery
	// nudges (conversation lanes let the model own readiness).
	RejectionRecoveryNudge(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, results []workspace.ToolResult) string
	// ShouldSkipRecoveryNudge returns true when the lane should skip
	// rejection-recovery nudges entirely (model owns readiness).
	ShouldSkipRecoveryNudge() bool
	// ClosureReadyRecoveryNudge returns a closure-ready nudge or "".
	ClosureReadyRecoveryNudge(brief workerruntime.WorkerRuntimeBrief) string

	// --- Confidence ---

	// ConfidenceCap returns a confidence ceiling for this lane.
	// Returns "" for no cap; "medium" is the standard cap for
	// lanes where the harness cannot evaluate answer quality.
	ConfidenceCap() string
}

// --- DefaultLanePolicy (used for implementation, locate, review, etc.) ---

type DefaultLanePolicy struct{}

func (p DefaultLanePolicy) Name() string                          { return "default" }
func (p DefaultLanePolicy) SkipDurationBudget() bool              { return false }
func (p DefaultLanePolicy) SkipModelCallBudget() bool             { return false }
func (p DefaultLanePolicy) SkipFingerprintHardBlock() bool        { return false }
func (p DefaultLanePolicy) SkipSynthesisNudge() bool              { return false }
func (p DefaultLanePolicy) SkipExplorationDelegationNudge() bool  { return false }
func (p DefaultLanePolicy) ShouldForceAnswerAfterRejection() bool { return true }
func (p DefaultLanePolicy) CallCountMilestones(_ int) (int, int)  { return 0, 0 }
func (p DefaultLanePolicy) AutoCaptureFindings() bool             { return false }
func (p DefaultLanePolicy) ShouldHaveNoteTool(_ string) bool      { return false }
func (p DefaultLanePolicy) ShouldSkipRecoveryNudge() bool         { return false }
func (p DefaultLanePolicy) RejectionRecoveryNudge(_ workerruntime.WorkerRuntimeBrief, _ workspace.ToolRequest, _ []workspace.ToolResult) string {
	return ""
}
func (p DefaultLanePolicy) ClosureReadyRecoveryNudge(_ workerruntime.WorkerRuntimeBrief) string {
	return ""
}
func (p DefaultLanePolicy) ConfidenceCap() string { return "" }

func (p DefaultLanePolicy) EvidenceSufficientForObjective(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return false
}
func (p DefaultLanePolicy) NamedEvidencePaths(_ workerruntime.WorkerRuntimeBrief) []string {
	return nil
}
func (p DefaultLanePolicy) EvidenceSatisfiedForceAnswerReady(_ workerruntime.WorkerRuntimeBrief, _ []workspace.ToolResult) bool {
	return false
}

// --- ConversationLanePolicy ---

type ConversationLanePolicy struct{}

func (p ConversationLanePolicy) Name() string                          { return "conversation" }
func (p ConversationLanePolicy) SkipDurationBudget() bool              { return true }
func (p ConversationLanePolicy) SkipModelCallBudget() bool             { return true }
func (p ConversationLanePolicy) SkipFingerprintHardBlock() bool        { return true }
func (p ConversationLanePolicy) SkipSynthesisNudge() bool              { return true }
func (p ConversationLanePolicy) SkipExplorationDelegationNudge() bool  { return true }
func (p ConversationLanePolicy) ShouldForceAnswerAfterRejection() bool { return false }
func (p ConversationLanePolicy) AutoCaptureFindings() bool             { return true }
func (p ConversationLanePolicy) ShouldHaveNoteTool(_ string) bool      { return true }
func (p ConversationLanePolicy) ConfidenceCap() string                 { return "medium" }

func (p ConversationLanePolicy) CallCountMilestones(ctxSize int) (int, int) {
	return conversationMilestones(ctxSize)
}

func (p ConversationLanePolicy) EvidenceSufficientForObjective(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return conversationEvidenceSatisfiedForObjective(brief, results)
}

func (p ConversationLanePolicy) NamedEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	return conversationNamedEvidencePaths(brief)
}

func (p ConversationLanePolicy) EvidenceSatisfiedForceAnswerReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return conversationEvidenceSatisfied(brief, results)
}

func (p ConversationLanePolicy) ShouldSkipRecoveryNudge() bool { return true }
func (p ConversationLanePolicy) RejectionRecoveryNudge(_ workerruntime.WorkerRuntimeBrief, _ workspace.ToolRequest, _ []workspace.ToolResult) string {
	return "" // model owns readiness
}

func (p ConversationLanePolicy) ClosureReadyRecoveryNudge(_ workerruntime.WorkerRuntimeBrief) string {
	return "" // model owns readiness
}

// --- ReadOnlyInvestigationLanePolicy ---
//
// Used for architecture, review, locate, and similar read-only investigation
// lanes. Per the north star, read-only strategy lanes must not be force-
// completed merely because the harness thinks evidence is sufficient —
// the model owns its own stop decision. This policy therefore skips hard
// budget limits (duration, model-call count) that would produce garbage
// forced answers.
//
// However, the north star explicitly allows "repeated-loop breaks" as a
// real invariant. This policy keeps loop detection (fingerprint hard block,
// same-path fixation) and advisory nudges (synthesis nudge, call-count
// milestones) so a stuck model has lightweight recovery paths without a
// hard force-complete. Force-answer-after-rejection is also suppressed:
// incidental tool gate rejections should nudge, not terminate.

type ReadOnlyInvestigationLanePolicy struct{}

func (p ReadOnlyInvestigationLanePolicy) Name() string                          { return "read_only_investigation" }
func (p ReadOnlyInvestigationLanePolicy) SkipDurationBudget() bool              { return true }
func (p ReadOnlyInvestigationLanePolicy) SkipModelCallBudget() bool             { return true }
func (p ReadOnlyInvestigationLanePolicy) SkipFingerprintHardBlock() bool        { return false }
func (p ReadOnlyInvestigationLanePolicy) SkipSynthesisNudge() bool              { return false }
func (p ReadOnlyInvestigationLanePolicy) SkipExplorationDelegationNudge() bool  { return false }
func (p ReadOnlyInvestigationLanePolicy) ShouldForceAnswerAfterRejection() bool { return false }
func (p ReadOnlyInvestigationLanePolicy) AutoCaptureFindings() bool             { return true }
func (p ReadOnlyInvestigationLanePolicy) ShouldHaveNoteTool(_ string) bool      { return true }
func (p ReadOnlyInvestigationLanePolicy) ConfidenceCap() string                 { return "medium" }

func (p ReadOnlyInvestigationLanePolicy) CallCountMilestones(ctxSize int) (int, int) {
	// Investigation lanes need earlier nudges than conversation lanes: local
	// models (Qwen 35b) lack the metacognition to self-regulate exploration
	// and may read indefinitely without answering. A first milestone of 10
	// tool calls (~2-3 minutes of wall-clock time for local models) gives a
	// timely "consider answering" nudge, with repeats every 10 calls.
	// Context-window scaling is unnecessary — investigation answers are
	// typically concise and should not require hundreds of tool calls.
	return 10, 10
}

func (p ReadOnlyInvestigationLanePolicy) EvidenceSufficientForObjective(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return false
}

func (p ReadOnlyInvestigationLanePolicy) NamedEvidencePaths(brief workerruntime.WorkerRuntimeBrief) []string {
	return nil
}

func (p ReadOnlyInvestigationLanePolicy) EvidenceSatisfiedForceAnswerReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return false
}

func (p ReadOnlyInvestigationLanePolicy) ShouldSkipRecoveryNudge() bool { return false }
func (p ReadOnlyInvestigationLanePolicy) RejectionRecoveryNudge(_ workerruntime.WorkerRuntimeBrief, _ workspace.ToolRequest, _ []workspace.ToolResult) string {
	return ""
}

func (p ReadOnlyInvestigationLanePolicy) ClosureReadyRecoveryNudge(_ workerruntime.WorkerRuntimeBrief) string {
	return ""
}

// --- DelegatedReadOnlyLanePolicy ---

type DelegatedReadOnlyLanePolicy struct{}

func (p DelegatedReadOnlyLanePolicy) Name() string                          { return "delegated_read_only" }
func (p DelegatedReadOnlyLanePolicy) SkipDurationBudget() bool              { return false }
func (p DelegatedReadOnlyLanePolicy) SkipModelCallBudget() bool             { return false }
func (p DelegatedReadOnlyLanePolicy) SkipFingerprintHardBlock() bool        { return false }
func (p DelegatedReadOnlyLanePolicy) SkipSynthesisNudge() bool              { return false }
func (p DelegatedReadOnlyLanePolicy) SkipExplorationDelegationNudge() bool  { return false }
func (p DelegatedReadOnlyLanePolicy) ShouldForceAnswerAfterRejection() bool { return true }
func (p DelegatedReadOnlyLanePolicy) CallCountMilestones(_ int) (int, int)  { return 0, 0 }
func (p DelegatedReadOnlyLanePolicy) AutoCaptureFindings() bool             { return false }
func (p DelegatedReadOnlyLanePolicy) ShouldHaveNoteTool(contract string) bool {
	return strings.EqualFold(contract, "grounded_evidence_summary")
}
func (p DelegatedReadOnlyLanePolicy) ConfidenceCap() string { return "" }

func (p DelegatedReadOnlyLanePolicy) EvidenceSufficientForObjective(_ workerruntime.WorkerRuntimeBrief, _ []workspace.ToolResult) bool {
	return false
}
func (p DelegatedReadOnlyLanePolicy) NamedEvidencePaths(_ workerruntime.WorkerRuntimeBrief) []string {
	return nil
}
func (p DelegatedReadOnlyLanePolicy) EvidenceSatisfiedForceAnswerReady(_ workerruntime.WorkerRuntimeBrief, _ []workspace.ToolResult) bool {
	return false
}

func (p DelegatedReadOnlyLanePolicy) ShouldSkipRecoveryNudge() bool { return false }
func (p DelegatedReadOnlyLanePolicy) RejectionRecoveryNudge(_ workerruntime.WorkerRuntimeBrief, _ workspace.ToolRequest, _ []workspace.ToolResult) string {
	return ""
}

func (p DelegatedReadOnlyLanePolicy) ClosureReadyRecoveryNudge(_ workerruntime.WorkerRuntimeBrief) string {
	return ""
}

// --- LanePolicyForBrief selects the appropriate policy from a RuntimeBrief ---

func LanePolicyForBrief(brief workerruntime.WorkerRuntimeBrief) LanePolicy {
	if workerruntime.TaskFamilyIsConversation(brief.TaskFamily) || readOnlyDirectAnswerLane(brief) {
		return ConversationLanePolicy{}
	}

	if workerruntime.TaskFamilyMatches(brief.TaskFamily, workerruntime.TaskFamilyDelegatedReadOnly) {
		return DelegatedReadOnlyLanePolicy{}
	}

	// Read-only investigation lanes (architecture, review, locate, etc.)
	// that do not require mutation use ReadOnlyInvestigationLanePolicy so
	// budget enforcement and loop detection remain active (the model cannot
	// read forever) while force-answer-after-rejection is suppressed.
	//
	// Without this explicit routing, the LLM route classifier's
	// non-determinism (temp 0.1) causes similar evidence-based questions to
	// land in different lane policies with inconsistent force-answer
	// behavior, violating the north-star principle of preferring nudges and
	// bounded recovery over hard-picking the model's next step.
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!runnerBriefRequiresMutation(brief) {

		return ReadOnlyInvestigationLanePolicy{}
	}

	return DefaultLanePolicy{}
}
