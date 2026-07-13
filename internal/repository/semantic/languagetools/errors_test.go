package languagetools

import (
	"fmt"

	"idiom/internal/platform/failures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Language-tool failures", func() {
	It("annotates unsupported dispatches", func() {
		result := NewManager(nil).Dispatch(GinkgoT().Context(), ToolRequest{Tool: "not_a_real_language_tool"})
		Expect(result.Failure).NotTo(BeNil())
		Expect(failures.CodeOf(result.Failure)).To(Equal(failures.Unimplemented))
	})

	It("preserves typed Python LSP unavailability through wrapping", func() {
		err := fmt.Errorf("outer: %w", ErrPythonLSPUnavailable)
		Expect(failures.CodeOf(err)).To(Equal(failures.Unavailable))
		Expect(failures.ReasonOf(err)).To(Equal(reasonLanguageToolPythonLSPUnavailable))
	})
})
