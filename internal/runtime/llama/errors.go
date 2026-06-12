package llama

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"idiom/internal/platform/failures"
)

const (
	reasonLLMAPIError failures.Reason = 5001 + iota
	reasonLLMAuthMissing
	reasonLLMTransportFailed
	reasonLLMHTTPError
	reasonLLMDecodeFailed
	reasonLLMMalformedResponse
	reasonLLMUnsupportedOperation
	reasonLLMServerConfigInvalid
	reasonLLMServerModelUnavailable
	reasonLLMServerStartFailed
	reasonLLMServerStopFailed
	reasonLLMServerReadinessFailed
)

// ErrAPIError is returned when the LLM backend responds with an
// error payload instead of a successful completion or chat message.
var ErrAPIError = failures.New(failures.Internal, reasonLLMAPIError, "llm api returned an error")

func llmAuthMissingError(env string, backend string) error {
	return failures.New(
		failures.Unauthenticated,
		reasonLLMAuthMissing,
		fmt.Sprintf("%s is required for %s backend", strings.TrimSpace(env), strings.TrimSpace(backend)),
	)
}

func llmUnsupportedError(message string) error {
	return failures.New(failures.Unimplemented, reasonLLMUnsupportedOperation, strings.TrimSpace(message))
}

func llmTransportError(message string, err error) error {
	code := failures.Unavailable

	switch {
	case errors.Is(err, context.Canceled):
		code = failures.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code = failures.DeadlineExceeded
	}

	return failures.Wrap(code, reasonLLMTransportFailed, strings.TrimSpace(message), err)
}

func llmHTTPStatusError(message string, statusCode int, body string) error {
	body = strings.TrimSpace(body)
	if body != "" {
		message = strings.TrimSpace(message) + ": " + body
	}

	return failures.Wrap(llmFailureCodeForHTTPStatus(statusCode), reasonLLMHTTPError, strings.TrimSpace(message), ErrAPIError)
}

func llmDecodeError(message string, err error) error {
	return failures.Wrap(failures.Internal, reasonLLMDecodeFailed, strings.TrimSpace(message), err)
}

func llmAPIError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "llm api returned an error"
	}

	return failures.Wrap(failures.Internal, reasonLLMAPIError, message, ErrAPIError)
}

func llmMalformedResponseError(message string) error {
	return failures.New(failures.Internal, reasonLLMMalformedResponse, strings.TrimSpace(message))
}

func llmServerConfigError(message string) error {
	return failures.New(failures.InvalidArgument, reasonLLMServerConfigInvalid, strings.TrimSpace(message))
}

func llmServerModelUnavailableError(message string, err error) error {
	code := failures.Unavailable
	if errors.Is(err, os.ErrNotExist) {
		code = failures.NotFound
	} else if errors.Is(err, os.ErrPermission) {
		code = failures.PermissionDenied
	}

	return failures.Wrap(code, reasonLLMServerModelUnavailable, strings.TrimSpace(message), err)
}

func llmServerStartError(message string, err error) error {
	code := failures.Unavailable
	if errors.Is(err, os.ErrPermission) {
		code = failures.PermissionDenied
	}

	return failures.Wrap(code, reasonLLMServerStartFailed, strings.TrimSpace(message), err)
}

func llmServerStopError(message string, err error) error {
	code := failures.Unavailable
	if errors.Is(err, context.Canceled) {
		code = failures.Canceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = failures.DeadlineExceeded
	}

	return failures.Wrap(code, reasonLLMServerStopFailed, strings.TrimSpace(message), err)
}

func llmServerReadinessError(message string) error {
	return failures.New(failures.Unavailable, reasonLLMServerReadinessFailed, strings.TrimSpace(message))
}

func llmFailureCodeForHTTPStatus(statusCode int) failures.Code {
	switch statusCode {
	case http.StatusUnauthorized:
		return failures.Unauthenticated
	case http.StatusForbidden:
		return failures.PermissionDenied
	case http.StatusNotFound:
		return failures.NotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return failures.DeadlineExceeded
	case http.StatusTooManyRequests:
		return failures.ResourceExhausted
	case http.StatusConflict, http.StatusPreconditionFailed:
		return failures.FailedPrecondition
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return failures.InvalidArgument
	default:
		if statusCode >= http.StatusInternalServerError {
			return failures.Unavailable
		}

		return failures.Internal
	}
}
