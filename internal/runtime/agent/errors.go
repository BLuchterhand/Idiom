package agent

import (
	"idiom/internal/platform/failures"
	"idiom/internal/runtime/agent/agenterrors"
)

const (
	reasonCompletionGateRejected failures.Reason = agenterrors.ReasonCompletionGateRejected
	reasonAgentTextualToolCall   failures.Reason = agenterrors.ReasonAgentTextualToolCall
	reasonAgentToolRequest       failures.Reason = agenterrors.ReasonAgentToolRequest
	reasonAgentEmptyResponse     failures.Reason = agenterrors.ReasonAgentEmptyResponse
)

var (
	ErrCompletionGateRejected = agenterrors.ErrCompletionGateRejected
	ErrAgentTextualToolCall   = agenterrors.ErrAgentTextualToolCall
	ErrAgentToolRequest       = agenterrors.ErrAgentToolRequest
	ErrAgentEmptyResponse     = agenterrors.ErrAgentEmptyResponse
)
