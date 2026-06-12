package failures_test

import (
	"errors"
	"fmt"
	"testing"

	"idiom/internal/platform/failures"
)

const (
	reasonQueueFull failures.Reason = 1001
	reasonBadInput  failures.Reason = 1002
)

type customFailure struct {
	code   failures.Code
	reason failures.Reason
}

func (e customFailure) Error() string {
	return "custom failure"
}

func (e customFailure) Code() failures.Code {
	return e.code
}

func (e customFailure) Reason() failures.Reason {
	return e.reason
}

func TestCodeStringStable(t *testing.T) {
	tests := []struct {
		code failures.Code
		want string
	}{
		{failures.Unknown, "unknown"},
		{failures.Canceled, "canceled"},
		{failures.InvalidArgument, "invalid_argument"},
		{failures.DeadlineExceeded, "deadline_exceeded"},
		{failures.NotFound, "not_found"},
		{failures.AlreadyExists, "already_exists"},
		{failures.PermissionDenied, "permission_denied"},
		{failures.ResourceExhausted, "resource_exhausted"},
		{failures.FailedPrecondition, "failed_precondition"},
		{failures.Aborted, "aborted"},
		{failures.OutOfRange, "out_of_range"},
		{failures.Unavailable, "unavailable"},
		{failures.Internal, "internal"},
		{failures.DataLoss, "data_loss"},
		{failures.Unauthenticated, "unauthenticated"},
		{failures.Unimplemented, "unimplemented"},
	}

	for _, test := range tests {
		if got := test.code.String(); got != test.want {
			t.Fatalf("Code(%d).String() = %q, want %q", test.code, got, test.want)
		}
	}

	if got := failures.Code(255).String(); got != "unknown" {
		t.Fatalf("unknown code string = %q, want unknown", got)
	}
}

func TestParseCode(t *testing.T) {
	code, ok := failures.ParseCode("permission_denied")
	if !ok {
		t.Fatal("ParseCode returned ok=false")
	}

	if code != failures.PermissionDenied {
		t.Fatalf("ParseCode returned %v, want %v", code, failures.PermissionDenied)
	}

	if _, ok := failures.ParseCode("PermissionDenied"); ok {
		t.Fatal("ParseCode accepted non-canonical name")
	}

	if code, ok := failures.ParseCode("unimplemented"); !ok || code != failures.Unimplemented {
		t.Fatalf("ParseCode unimplemented = %v, %v; want %v, true", code, ok, failures.Unimplemented)
	}
}

func TestWrapUnwrapAsAndCodeOf(t *testing.T) {
	cause := errors.New("disk unavailable")
	err := failures.Wrap(failures.Unavailable, reasonQueueFull, "cannot read queue", cause)
	wrapped := fmt.Errorf("outer: %w", err)

	if !errors.Is(wrapped, cause) {
		t.Fatal("wrapped failure does not unwrap to cause")
	}

	if !errors.Is(wrapped, failures.New(failures.Unavailable, reasonQueueFull, "")) {
		t.Fatal("errors.Is did not match code and reason")
	}

	if errors.Is(wrapped, failures.New(failures.Unavailable, reasonBadInput, "")) {
		t.Fatal("errors.Is matched different reason")
	}

	var failure *failures.Error

	if !errors.As(wrapped, &failure) {
		t.Fatal("errors.As did not find *failures.Error")
	}

	if failure.Code != failures.Unavailable {
		t.Fatalf("failure.Code = %v, want %v", failure.Code, failures.Unavailable)
	}

	if got := failures.CodeOf(wrapped); got != failures.Unavailable {
		t.Fatalf("CodeOf = %v, want %v", got, failures.Unavailable)
	}

	if got := failures.ReasonOf(wrapped); got != reasonQueueFull {
		t.Fatalf("ReasonOf = %v, want %v", got, reasonQueueFull)
	}
}

func TestCustomProviders(t *testing.T) {
	err := fmt.Errorf("outer: %w", customFailure{
		code:   failures.InvalidArgument,
		reason: reasonBadInput,
	})

	if got := failures.CodeOf(err); got != failures.InvalidArgument {
		t.Fatalf("CodeOf custom error = %v, want %v", got, failures.InvalidArgument)
	}

	if got := failures.ReasonOf(err); got != reasonBadInput {
		t.Fatalf("ReasonOf custom error = %v, want %v", got, reasonBadInput)
	}

	if !failures.IsCode(err, failures.InvalidArgument) {
		t.Fatal("IsCode returned false for custom provider code")
	}
}

func TestFallbacks(t *testing.T) {
	err := errors.New("plain")

	if got := failures.CodeOf(err); got != failures.Unknown {
		t.Fatalf("CodeOf plain error = %v, want %v", got, failures.Unknown)
	}

	if got := failures.CodeOf(nil); got != failures.Unknown {
		t.Fatalf("CodeOf nil = %v, want %v", got, failures.Unknown)
	}

	if got := failures.ReasonOf(err); got != failures.NoReason {
		t.Fatalf("ReasonOf plain error = %v, want %v", got, failures.NoReason)
	}

	if failures.IsCode(err, failures.Internal) {
		t.Fatal("IsCode returned true for wrong fallback code")
	}

	if failures.IsCode(nil, failures.Unknown) {
		t.Fatal("IsCode returned true for nil error")
	}
}
