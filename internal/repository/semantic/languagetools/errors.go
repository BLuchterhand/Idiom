package languagetools

import (
	"strings"

	"idiom/internal/platform/failures"
)

const (
	reasonLanguageToolLSPFailure failures.Reason = 12001 + iota
	reasonLanguageToolInvalidArgument
	reasonLanguageToolUnavailable
	reasonLanguageToolUnsupported
	reasonLanguageToolPathResolution
	reasonLanguageToolNotFound
	reasonLanguageToolPermission
	reasonLanguageToolWriteDisabled
	reasonLanguageToolWriteFailed
	reasonLanguageToolInternal
	reasonLanguageToolPythonLSPUnavailable
)

var ErrPythonLSPUnavailable = failures.New(failures.Unavailable, reasonLanguageToolPythonLSPUnavailable, "python language server unavailable")

func annotateToolFailure(result ToolResult) ToolResult {
	if result.Failure != nil || strings.TrimSpace(result.Error) == "" {
		return result
	}

	text := strings.ToLower(strings.Join([]string{result.Code, result.Summary, result.Error}, " "))
	switch {
	case strings.Contains(text, "unsupported") || strings.Contains(text, "unknown"):
		return withToolFailure(result, failures.Unimplemented, reasonLanguageToolUnsupported)
	case strings.Contains(text, "workspace writes are disabled"):
		return withToolFailure(result, failures.PermissionDenied, reasonLanguageToolWriteDisabled)
	case strings.Contains(text, "permission") || strings.Contains(text, "read-only"):
		return withToolFailure(result, failures.PermissionDenied, reasonLanguageToolPermission)
	case strings.Contains(text, " is required") || strings.Contains(text, "missing required") || strings.Contains(text, "invalid ") || strings.Contains(text, "rejected"):
		return withToolFailure(result, failures.InvalidArgument, reasonLanguageToolInvalidArgument)
	case strings.Contains(text, "not installed") || strings.Contains(text, "not configured") || strings.Contains(text, "unavailable"):
		return withToolFailure(result, failures.Unavailable, reasonLanguageToolUnavailable)
	case strings.Contains(text, "not found") || strings.Contains(text, "no such file") || strings.Contains(text, "does not exist") || strings.Contains(text, "no ") && strings.Contains(text, " indexed"):
		return withToolFailure(result, failures.NotFound, reasonLanguageToolNotFound)
	case strings.Contains(text, "failed to resolve"):
		return withToolFailure(result, failures.InvalidArgument, reasonLanguageToolPathResolution)
	case strings.Contains(text, "failed to write") || strings.Contains(text, "failed to create"):
		return withToolFailure(result, failures.Internal, reasonLanguageToolWriteFailed)
	default:
		return withToolFailure(result, failures.Internal, reasonLanguageToolInternal)
	}
}

func withToolFailure(result ToolResult, code failures.Code, reason failures.Reason) ToolResult {
	if result.Failure != nil {
		return result
	}

	message := strings.TrimSpace(result.Error)
	if message == "" {
		message = strings.TrimSpace(result.Summary)
	}

	if message == "" {
		return result
	}

	result.Failure = failures.New(code, reason, message)

	return result
}

func lspFailureCode(code int) failures.Code {
	switch code {
	case -32601:
		return failures.Unimplemented
	case -32602:
		return failures.InvalidArgument
	default:
		return failures.Internal
	}
}
