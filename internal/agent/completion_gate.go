// Package agent: CompletionGatePipeline keeps completion checks registered in
// one ordered pipeline instead of open-coded as a long chain in Runner.
//
// When adding a new gate check, create a function matching the
// CompletionGateCheck signature and register it. Do not splice a new
// if-block into the monolithic completionGateRejection.

package agent

import "idiom/internal/workspace"

// CompletionGateCheck inspects a model reply and accumulated results,
// returning a rejection ToolResult with shouldReject=true, or an empty
// ToolResult with shouldReject=false (pass).
type CompletionGateCheck func(reply string, results []workspace.ToolResult) (rejection workspace.ToolResult, shouldReject bool)

// CompletionGatePipeline evaluates checks in registration order and
// returns the first rejection. If no check rejects, the pipeline passes.
type CompletionGatePipeline struct {
	checks []CompletionGateCheck
}

// NewCompletionGatePipeline creates a pipeline with the given checks.
func NewCompletionGatePipeline(checks ...CompletionGateCheck) *CompletionGatePipeline {
	return &CompletionGatePipeline{checks: append([]CompletionGateCheck(nil), checks...)}
}

// Evaluate runs all checks in order and returns the first rejection.
// Returns (zero ToolResult, false) when all checks pass.
func (p *CompletionGatePipeline) Evaluate(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if p == nil {
		return workspace.ToolResult{}, false
	}
	for _, check := range p.checks {
		if rejection, rejected := check(reply, results); rejected {
			return rejection, true
		}
	}
	return workspace.ToolResult{}, false
}

// CompletionGateChecks returns the runner's gate checks in the order they
// historically executed.
func CompletionGateChecks(r *Runner, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) []CompletionGateCheck {
	if r != nil {
		return r.completionGateChecks(step, taskLedger, declarationMovesForbidden)
	}
	return []CompletionGateCheck{
		textualToolCallGateCheck,
		unexecutedToolRequestGateCheck,
		rawToolResultTranscriptGateCheck,
		unresolvedProcessIntentGateCheck,
	}
}

func (r *Runner) completionGatePipeline(step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) *CompletionGatePipeline {
	return NewCompletionGatePipeline(CompletionGateChecks(r, step, taskLedger, declarationMovesForbidden)...)
}

func (r *Runner) completionGateChecks(_ int, taskLedger *taskLedgerState, _ bool) []CompletionGateCheck {
	return []CompletionGateCheck{
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return textualToolCallCompletionRejection(r, reply, results)
		},
		func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.unexecutedToolRequestCompletionRejection(reply)
		},
		rawToolResultTranscriptGateCheck,
		unresolvedProcessIntentGateCheck,
		func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
			ledgerOwnedByMutationContract := r != nil && runnerBriefRequiresMutation(r.RuntimeBrief) && len(r.RuntimeBrief.RequiredEditPaths) > 0
			if ledgerOwnedByMutationContract || taskLedger == nil {
				return workspace.ToolResult{}, false
			}
			return taskLedger.completionRejection(reply)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlyEvidenceCompletionRejection(reply, results)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlyNonSubstantiveCompletionRejection(reply, results)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlyExplanationCompletionRejection(reply, results)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.readOnlySearchHitInventoryCompletionRejection(reply, results)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.mutationCompletionRejection(reply, results)
		},
		func(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
			return r.planThenExecuteReviewRepairCompletionRejection(reply, results)
		},
	}
}

var textualToolCallGateCheck CompletionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return textualToolCallCompletionRejection(nil, reply, nil)
}

func textualToolCallCompletionRejection(r *Runner, reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if tool, transport, ok := unsafeTextualWriteAttempt(reply); ok && r.unsafeTextualWriteRecoverySurfaceActive(results) {
		msg := "the reply contains a " + transport + " " + tool + " write attempt that could not be converted into a safe write request. Emit the write through the native tool-call mechanism, as one visible JSON object with top-level `tool`, `path`, `old_text`, `new_text`, and `description` fields copied from source evidence, or as Qwen key-value `tool:` syntax with those same fields. Function-call write syntax is not executable."
		if r != nil && r.planThenExecutePlannerPhase() {
			msg = "the reply contains a " + transport + " " + tool + " write attempt, but the plan-execute planner phase does not execute per-edit writes. Emit one visible JSON object for `submit_plan` with top-level `tool`, `rationale`, and `edits`, answer `Decision: source-satisfied`, or answer `<<QUESTION>>:` with the exact blocker. Function-call write syntax is not executable."
		}
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Textual write syntax rejected as final answer",
			Error:   msg,
		}, true
	}
	if !looksLikeTextualToolCall(reply) {
		return workspace.ToolResult{}, false
	}
	msg := "the reply contains textual tool-call markup instead of prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose."
	if looksLikeXMLToolCall(reply) {
		msg = "the reply contains XML tool-call markup (e.g. <n_tool_calls>, <invoke>, <janus_tool>) instead of prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose. Do not emit XML or HTML markup."
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Textual tool-call markup rejected as final answer",
		Error:   msg,
	}, true
}

func (r *Runner) unsafeTextualWriteRecoverySurfaceActive(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}
	if len(changedPathsFromToolResults(results)) > 0 {
		return false
	}
	if r.planThenExecutePlannerPhase() {
		return concreteSourceEvidenceAlreadyRead(results)
	}
	return implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results)
}

var unexecutedToolRequestGateCheck CompletionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Unexecuted tool request in final answer",
		Error:   "the reply contains what looks like an attempt to call a tool inside prose. Emit a tool call through the normal tool-call mechanism, or answer in plain prose. Do not emit tool JSON, XML, or CLI syntax inside a final answer.",
	}, true
}

var rawToolResultTranscriptGateCheck CompletionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return rawToolResultTranscriptCompletionRejection(reply)
}

var unresolvedProcessIntentGateCheck CompletionGateCheck = func(reply string, _ []workspace.ToolResult) (workspace.ToolResult, bool) {
	return unresolvedProcessIntentCompletionRejection(reply)
}
