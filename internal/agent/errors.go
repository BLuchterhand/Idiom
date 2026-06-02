package agent

import "errors"

// Sentinel errors for runner failure modes. Callers can use errors.Is
// to distinguish transient refusal, tool-call-in-final-answer, and
// empty-answer failures from infrastructure errors.
var (
	// ErrCompletionGateRejected is returned when the completion gate
	// rejects the model's final answer and no deterministic fallback is
	// available.
	ErrCompletionGateRejected = errors.New("completion gate rejected final answer")

	// ErrAgentTextualToolCall is returned when the model emits textual
	// tool-call markup (e.g. raw JSON) in its final answer instead of
	// natural language.
	ErrAgentTextualToolCall = errors.New("agent emitted textual tool call markup instead of answer")

	// ErrAgentToolRequest is returned when the model emits a tool request
	// (native or parsed) when tools are disabled and a plain answer is
	// required.
	ErrAgentToolRequest = errors.New("agent emitted a tool request instead of answer")

	// ErrAgentEmptyResponse is returned when the model returns an empty
	// response after retry prompting.
	ErrAgentEmptyResponse = errors.New("agent returned an empty response")
)
