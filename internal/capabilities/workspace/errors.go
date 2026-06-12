package workspace

import (
	"strings"

	"idiom/internal/platform/failures"
)

const (
	reasonSourceSanityFailed failures.Reason = 3001 + iota
	reasonPlanSubmissionRejected
	reasonPythonLSPUnavailable
	reasonToolInvalidArgument
	reasonToolUnavailable
	reasonToolUnsupported
	reasonToolPathResolutionFailed
	reasonToolNotFound
	reasonToolWriteDisabled
	reasonToolWriteFailed
	reasonContextFreshnessBlocker
	reasonHistoryNotApplicable
	reasonGitHistoryUnavailable
	reasonPlanArtifactSchemaInvalid
	reasonToolPermissionPolicy
	reasonToolInternalFailure
)

// Sentinel errors for workspace operations.
var (
	// ErrSourceSanityFailed is returned when source sanity checks
	// detect issues in code files.
	ErrSourceSanityFailed = failures.New(failures.FailedPrecondition, reasonSourceSanityFailed, "source sanity check failed")

	// ErrPlanSubmissionRejected is returned when submit_plan validation
	// or acceptance fails.
	ErrPlanSubmissionRejected = failures.New(failures.FailedPrecondition, reasonPlanSubmissionRejected, "plan submission rejected")

	// ErrPythonLSPUnavailable is returned when the Python language server
	// binary cannot be found or started.
	ErrPythonLSPUnavailable = failures.New(failures.Unavailable, reasonPythonLSPUnavailable, "python language server unavailable")
)

func toolInvalidArgumentResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.InvalidArgument, reasonToolInvalidArgument)
}

func toolUnavailableResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.Unavailable, reasonToolUnavailable)
}

func toolUnsupportedResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.Unimplemented, reasonToolUnsupported)
}

func toolPathResolutionFailureResult(tool string, summary string, err error) ToolResult {
	return toolFailureFromErrorResult(tool, summary, err, failures.InvalidArgument, reasonToolPathResolutionFailed)
}

func toolNotFoundResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.NotFound, reasonToolNotFound)
}

func toolPermissionPolicyResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.PermissionDenied, reasonToolPermissionPolicy)
}

func toolWriteDisabledResult(tool string, summary string, errorText string) ToolResult {
	return toolFailureResult(tool, summary, errorText, failures.PermissionDenied, reasonToolWriteDisabled)
}

func toolWriteFailureResult(tool string, summary string, err error) ToolResult {
	return toolFailureFromErrorResult(tool, summary, err, failures.Internal, reasonToolWriteFailed)
}

func toolInternalFailureResult(tool string, summary string, err error) ToolResult {
	return toolFailureFromErrorResult(tool, summary, err, failures.Internal, reasonToolInternalFailure)
}

func toolFailureFromErrorResult(tool string, summary string, err error, code failures.Code, reason failures.Reason) ToolResult {
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}

	result := toolFailureResult(tool, summary, errorText, code, reason)
	if err != nil {
		result.Failure = failures.Wrap(code, reason, strings.TrimSpace(summary), err)
	}

	return result
}

func toolFailureFromTypedErrorResult(tool string, summary string, err error) ToolResult {
	if err == nil {
		return toolFailureResult(tool, summary, "", failures.Internal, reasonToolInternalFailure)
	}

	code := failures.CodeOf(err)
	reason := failures.ReasonOf(err)

	if code == failures.Unknown {
		code = failures.Internal
	}

	result := toolFailureResult(tool, summary, err.Error(), code, reason)

	result.Failure = err

	return result
}

func toolFailureFromMaybeTypedErrorResult(tool string, summary string, err error, defaultCode failures.Code, defaultReason failures.Reason) ToolResult {
	if err == nil {
		return toolFailureResult(tool, summary, "", defaultCode, defaultReason)
	}

	if failures.CodeOf(err) != failures.Unknown {
		return toolFailureFromTypedErrorResult(tool, summary, err)
	}

	return toolFailureFromErrorResult(tool, summary, err, defaultCode, defaultReason)
}

func toolFailureResult(tool string, summary string, errorText string, code failures.Code, reason failures.Reason) ToolResult {
	message := strings.TrimSpace(errorText)
	if message == "" {
		message = strings.TrimSpace(summary)
	}

	return ToolResult{
		Tool:    tool,
		Summary: summary,
		Error:   errorText,
		Failure: failures.New(code, reason, message),
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

func annotateToolFailure(result ToolResult) ToolResult {
	if result.Failure != nil || strings.TrimSpace(result.Error) == "" {
		return result
	}

	text := strings.ToLower(strings.Join([]string{result.Code, result.Summary, result.Error}, " "))
	switch {
	case strings.Contains(text, "freshness_blocker") || strings.Contains(text, "stale context"):
		return withToolFailure(result, failures.FailedPrecondition, reasonContextFreshnessBlocker)
	case strings.Contains(text, "unsupported tool") || strings.Contains(text, "unknown external tool") || strings.Contains(text, "unknown mcp tool") || strings.Contains(text, "not registered"):
		return withToolFailure(result, failures.Unimplemented, reasonToolUnsupported)
	case strings.Contains(text, "workspace writes are disabled"):
		return withToolFailure(result, failures.PermissionDenied, reasonToolWriteDisabled)
	case strings.Contains(text, "permission") || strings.Contains(text, "read-only") || strings.Contains(text, "protected path") || strings.Contains(text, "tool_rules deny"):
		return withToolFailure(result, failures.PermissionDenied, reasonToolPermissionPolicy)
	case strings.Contains(text, "timed out") || strings.Contains(text, "timeout"):
		return withToolFailure(result, failures.DeadlineExceeded, failures.NoReason)
	case strings.Contains(text, " is required") || strings.Contains(text, "missing required") || strings.Contains(text, "invalid ") || strings.Contains(text, "rejected"):
		return withToolFailure(result, failures.InvalidArgument, reasonToolInvalidArgument)
	case strings.Contains(text, "not installed") || strings.Contains(text, "not configured") || strings.Contains(text, "unavailable"):
		return withToolFailure(result, failures.Unavailable, reasonToolUnavailable)
	case strings.Contains(text, "not found") || strings.Contains(text, "no such file") || strings.Contains(text, "does not exist"):
		return withToolFailure(result, failures.NotFound, reasonToolNotFound)
	case strings.Contains(text, "failed to resolve"):
		return withToolFailure(result, failures.InvalidArgument, reasonToolPathResolutionFailed)
	case strings.Contains(text, "failed to write") || strings.Contains(text, "failed to create") || strings.Contains(text, "failed to open") || strings.Contains(text, "failed to append"):
		return withToolFailure(result, failures.Internal, reasonToolWriteFailed)
	default:
		return withToolFailure(result, failures.Internal, reasonToolInternalFailure)
	}
}
