package agent

import "idiom/internal/failures"

const (
	reasonCompletionGateRejected failures.Reason = 2001 + iota
	reasonAgentTextualToolCall
	reasonAgentToolRequest
	reasonAgentEmptyResponse
)

// Sentinel errors for runner failure modes. Callers can use errors.Is
// to distinguish transient refusal, tool-call-in-final-answer, and
// empty-answer failures from infrastructure errors.
// Agent final-answer sentinels use FailedPrecondition because they
// represent violated runner/model output contracts after the call has
// started, not invalid caller arguments.
var (
	// ErrCompletionGateRejected is returned when the completion gate
	// rejects the model's final answer and no deterministic fallback is
	// available.
	ErrCompletionGateRejected = failures.New(failures.FailedPrecondition, reasonCompletionGateRejected, "completion gate rejected final answer")

	// ErrAgentTextualToolCall is returned when the model emits textual
	// tool-call markup (e.g. raw JSON) in its final answer instead of
	// natural language.
	ErrAgentTextualToolCall = failures.New(failures.FailedPrecondition, reasonAgentTextualToolCall, "agent emitted textual tool call markup instead of answer")

	// ErrAgentToolRequest is returned when the model emits a tool request
	// (native or parsed) when tools are disabled and a plain answer is
	// required.
	ErrAgentToolRequest = failures.New(failures.FailedPrecondition, reasonAgentToolRequest, "agent emitted a tool request instead of answer")

	// ErrAgentEmptyResponse is returned when the model returns an empty
	// response after retry prompting.
	ErrAgentEmptyResponse = failures.New(failures.FailedPrecondition, reasonAgentEmptyResponse, "agent returned an empty response")
)
