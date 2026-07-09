package failures

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a local, dependency-free error taxonomy modeled after common RPC
// status codes. String values are stable wire/sidecar names.
type Code uint8

const (
	Unknown Code = iota
	Canceled
	InvalidArgument
	DeadlineExceeded
	NotFound
	AlreadyExists
	PermissionDenied
	ResourceExhausted
	FailedPrecondition
	Aborted
	OutOfRange
	Unavailable
	Internal
	DataLoss
	Unauthenticated
	Unimplemented
)

var codeNames = [...]string{
	Unknown:            "unknown",
	Canceled:           "canceled",
	InvalidArgument:    "invalid_argument",
	DeadlineExceeded:   "deadline_exceeded",
	NotFound:           "not_found",
	AlreadyExists:      "already_exists",
	PermissionDenied:   "permission_denied",
	ResourceExhausted:  "resource_exhausted",
	FailedPrecondition: "failed_precondition",
	Aborted:            "aborted",
	OutOfRange:         "out_of_range",
	Unavailable:        "unavailable",
	Internal:           "internal",
	DataLoss:           "data_loss",
	Unauthenticated:    "unauthenticated",
	Unimplemented:      "unimplemented",
}

func (c Code) String() string {
	if int(c) >= 0 && int(c) < len(codeNames) {
		if name := codeNames[c]; name != "" {
			return name
		}
	}

	return "unknown"
}

// Reason is intentionally package-extensible: domain packages may define their
// own typed constants without centralizing every reason in this core package.
type Reason uint16

const NoReason Reason = 0

type Error struct {
	Code    Code
	Reason  Reason
	Message string
	Op      string
	Path    string
	Cause   error
}

func New(code Code, reason Reason, message string) *Error {
	return &Error{
		Code:    code,
		Reason:  reason,
		Message: message,
	}
}

func Wrap(code Code, reason Reason, message string, cause error) *Error {
	return &Error{
		Code:    code,
		Reason:  reason,
		Message: message,
		Cause:   cause,
	}
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	var b strings.Builder

	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}

	b.WriteString(e.Code.String())

	if e.Reason != NoReason {
		fmt.Fprintf(&b, "[%d]", e.Reason)
	}

	if e.Path != "" {
		b.WriteString(" ")
		b.WriteString(e.Path)
	}

	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}

	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}

	return b.String()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Cause
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}

	var failure *Error

	if !errors.As(target, &failure) || failure == nil {
		return false
	}

	return e.Code == failure.Code && e.Reason == failure.Reason
}

type codeProvider interface {
	Code() Code
}

type failureCodeProvider interface {
	FailureCode() Code
}

type reasonProvider interface {
	Reason() Reason
}

func CodeOf(err error) Code {
	if err == nil {
		return Unknown
	}

	var failure *Error

	if errors.As(err, &failure) && failure != nil {
		return failure.Code
	}

	var provider codeProvider

	if errors.As(err, &provider) {
		return provider.Code()
	}

	var failureProvider failureCodeProvider

	if errors.As(err, &failureProvider) {
		return failureProvider.FailureCode()
	}

	return Unknown
}

func ReasonOf(err error) Reason {
	if err == nil {
		return NoReason
	}

	var failure *Error

	if errors.As(err, &failure) && failure != nil {
		return failure.Reason
	}

	var provider reasonProvider

	if errors.As(err, &provider) {
		return provider.Reason()
	}

	return NoReason
}
