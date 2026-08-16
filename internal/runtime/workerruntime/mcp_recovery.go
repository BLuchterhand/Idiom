package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
)

func classifyMCPFailure(req workspace.ToolRequest, result workspace.ToolResult) (recoverycontract.FailureClass, string, bool) {
	tool := mcpToolName(req, result)
	if !strings.HasPrefix(strings.ToLower(tool), "mcp:") || strings.TrimSpace(result.Error) == "" {
		return "", "", false
	}

	detail := mcpFailureDetail(result)
	if mcpInputValidationFailure(result) {
		return recoverycontract.FailureClassMCPInputError, detail, true
	}

	return recoverycontract.FailureClassMCPToolFailure, detail, true
}

func mcpToolName(req workspace.ToolRequest, result workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	return tool
}

func mcpInputValidationFailure(result workspace.ToolResult) bool {
	if result.Failure != nil && failures.CodeOf(result.Failure) == failures.InvalidArgument {
		return true
	}

	text := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(result.Summary),
		strings.TrimSpace(result.Error),
		strings.TrimSpace(result.Output),
	}, "\n"))

	return textutil.ContainsAny(text,
		"required",
		"missing required",
		"invalid argument",
		"invalid input",
		"invalid parameter",
		"invalid field",
		"schema",
		"validation",
	)
}

func mcpFailureDetail(result workspace.ToolResult) string {
	for _, value := range []string{result.Error, result.Output, result.Summary} {
		if detail := strings.TrimSpace(value); detail != "" {
			return detail
		}
	}

	return "MCP call failed without a server-provided detail"
}

func mcpRecoveryGuidance(input ActionGuidanceInput, class recoverycontract.FailureClass, detail string) ActionGuidance {
	guidance := ActionGuidance{}
	tool := mcpToolName(input.Request, input.Result)

	guidance.InvalidNext = appendUnique(guidance.InvalidNext, "repeat the failed "+tool+" call with the same MCP input")
	if class == recoverycontract.FailureClassMCPInputError {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "retry "+tool+" with corrected MCP input; server reported: "+detail)
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use the argument shape required by the declared MCP schema for "+tool)
	} else {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "retry the callable MCP tool "+tool+" after addressing this server failure: "+detail)
	}

	guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with <<QUESTION>> naming the exact MCP blocker if a corrected call cannot be formed")

	return guidance
}

func dedicatedMCPRecovery(input ActionGuidanceInput) (recoverycontract.FailureClass, string, bool) {
	class, detail, ok := classifyMCPFailure(input.Request, input.Result)
	if !ok {
		return "", "", false
	}

	status := combinedLower(input.Reason, input.Result.Summary, input.Result.Error)
	if textutil.ContainsAny(status, "approval required", "write approval required", "permission denied", "permission", "guard", "completion gate") ||
		verificationToolFailure(mcpToolName(input.Request, input.Result), status, input.Result) ||
		isActualVerificationRecoverySignal(status) {
		return "", "", false
	}

	return class, detail, true
}
