package workspace

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
			name: "source sanity failed",
			err:  ErrSourceSanityFailed,
			code: failures.FailedPrecondition,
		},
		{
			name: "plan submission rejected",
			err:  ErrPlanSubmissionRejected,
			code: failures.FailedPrecondition,
		},
		{
			name: "python lsp unavailable",
			err:  ErrPythonLSPUnavailable,
			code: failures.Unavailable,
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
