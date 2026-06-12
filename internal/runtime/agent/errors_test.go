package agent

import (
	"errors"
	"fmt"
	"testing"

	"idiom/internal/platform/failures"
)

func TestSentinelFailuresPreserveErrorsIsAndCanonicalCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code failures.Code
	}{
		{
			name: "completion gate rejected",
			err:  ErrCompletionGateRejected,
			code: failures.FailedPrecondition,
		},
		{
			name: "textual tool call",
			err:  ErrAgentTextualToolCall,
			code: failures.FailedPrecondition,
		},
		{
			name: "tool request",
			err:  ErrAgentToolRequest,
			code: failures.FailedPrecondition,
		},
		{
			name: "empty response",
			err:  ErrAgentEmptyResponse,
			code: failures.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", tt.err)

			if !errors.Is(wrapped, tt.err) {
				t.Fatalf("errors.Is(wrapped, sentinel) = false")
			}

			if got := failures.CodeOf(wrapped); got != tt.code {
				t.Fatalf("failures.CodeOf(wrapped) = %v, want %v", got, tt.code)
			}
		})
	}
}
