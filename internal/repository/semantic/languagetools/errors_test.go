package languagetools

import (
	"fmt"
	"testing"

	"idiom/internal/platform/failures"
)

func TestDispatchAnnotatesUnsupportedToolFailure(t *testing.T) {
	result := NewManager(nil).Dispatch(t.Context(), ToolRequest{Tool: "not_a_real_language_tool"})

	if result.Failure == nil {
		t.Fatal("Dispatch did not annotate unsupported tool failure")
	}
	if got := failures.CodeOf(result.Failure); got != failures.Unimplemented {
		t.Fatalf("failure code = %v, want %v", got, failures.Unimplemented)
	}
}

func TestPythonLSPUnavailableIsTyped(t *testing.T) {
	err := fmt.Errorf("outer: %w", ErrPythonLSPUnavailable)

	if got := failures.CodeOf(err); got != failures.Unavailable {
		t.Fatalf("failure code = %v, want %v", got, failures.Unavailable)
	}
	if got := failures.ReasonOf(err); got != reasonLanguageToolPythonLSPUnavailable {
		t.Fatalf("failure reason = %v, want %v", got, reasonLanguageToolPythonLSPUnavailable)
	}
}
