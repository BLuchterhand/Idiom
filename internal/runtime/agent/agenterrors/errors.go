package agenterrors

import "idiom/internal/platform/failures"

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
