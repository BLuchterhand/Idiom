package app

import (
	"errors"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("format model failure status classifies retryable transport", func() {
	got := formatModelFailureStatus(errors.New("chat request transport failed: unexpected EOF"))
	for _, want := range []string{
		"Chat failed: chat request transport failed: unexpected EOF",
		"Classification: retryable model failure",
		"existing product artifacts were preserved",
		"retry the same command",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("retryable status missing %q:\n%s", want, got))
	}
})

var _ = ginkgo.It("format model failure status classifies non retryable", func() {
	got := formatModelFailureStatus(errors.New("invalid request: malformed JSON schema"))
	gomega.Expect(strings.Contains(got, "Classification: non-retryable model failure")).To(gomega.BeTrue(), fmt.Sprintf("expected non-retryable status, got %q", got))

	gomega.Expect(strings.Contains(got, "existing product artifacts were preserved")).To(gomega.BeFalse(), fmt.Sprintf("non-retryable status should not offer blind retry guidance: %s", got))
})
