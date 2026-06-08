package agent

import (
	"idiom/internal/runtime/agent/agenterrors"
)

var (
	ErrCompletionGateRejected = agenterrors.ErrCompletionGateRejected
	ErrAgentTextualToolCall   = agenterrors.ErrAgentTextualToolCall
	ErrAgentToolRequest       = agenterrors.ErrAgentToolRequest
	ErrAgentEmptyResponse     = agenterrors.ErrAgentEmptyResponse
)
