package app

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatModelFailureStatusClassifiesRetryableTransport(t *testing.T) {
	got := formatModelFailureStatus(errors.New("chat request transport failed: unexpected EOF"))
	for _, want := range []string{
		"Chat failed: chat request transport failed: unexpected EOF",
		"Classification: retryable model failure",
		"existing product artifacts were preserved",
		"retry the same command",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("retryable status missing %q:\n%s", want, got)
		}
	}
}

func TestFormatModelFailureStatusClassifiesNonRetryable(t *testing.T) {
	got := formatModelFailureStatus(errors.New("invalid request: malformed JSON schema"))
	if !strings.Contains(got, "Classification: non-retryable model failure") {
		t.Fatalf("expected non-retryable status, got %q", got)
	}

	if strings.Contains(got, "existing product artifacts were preserved") {
		t.Fatalf("non-retryable status should not offer blind retry guidance: %s", got)
	}
}
