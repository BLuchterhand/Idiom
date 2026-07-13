package workspace

import (
	"errors"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/failures"
)

var _ = ginkgo.Describe("sentinel failures preserve errors is and canonical codes", func() {

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
		ginkgo.It(tt.name, func() {
			wrapped := fmt.Errorf("wrapped: %w", tt.err)
			gomega.Expect(errors.Is(wrapped, tt.err)).To(gomega.BeTrue(), fmt.Sprintf("errors.Is(wrapped, sentinel) = false"))

			if got := failures.CodeOf(wrapped); got != tt.code {
				ginkgo.Fail(fmt.Sprintf("failures.CodeOf(wrapped) = %v, want %v", got, tt.code))
			}
		})
	}
})
