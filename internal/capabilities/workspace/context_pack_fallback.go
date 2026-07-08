package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	contextpackcontract "idiom/internal/contracts/contextpack"
	"idiom/internal/platform/failures"
)

type contextPackFallbackPayload struct {
	Schema               string   `json:"schema"`
	Status               string   `json:"status"`
	Reason               string   `json:"reason"`
	Tool                 string   `json:"tool"`
	Mode                 string   `json:"mode,omitempty"`
	Path                 string   `json:"path,omitempty"`
	Workdir              string   `json:"workdir,omitempty"`
	Language             string   `json:"language,omitempty"`
	Limit                int      `json:"limit,omitempty"`
	BudgetTokens         int      `json:"budget_tokens,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	ForbiddenPaths       []string `json:"forbidden_paths,omitempty"`
	Timeout              string   `json:"timeout,omitempty"`
	RetrySameRequest     bool     `json:"retry_same_request"`
	Uncertainty          string   `json:"uncertainty"`
	FallbackCapabilities []string `json:"fallback_capabilities"`
}

func contextPackFallbackResult(req ToolRequest, timeout time.Duration, cause error) ToolResult {
	reason := "unavailable"
	summary := "Context pack unavailable"
	failureCode := failures.Internal
	errorText := strings.TrimSpace(fmt.Sprintf("context_pack did not complete: %v", cause))

	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		reason = "timeout"
		summary = "Context pack timed out"
		failureCode = failures.DeadlineExceeded
		if timeout > 0 {
			errorText = fmt.Sprintf("context_pack did not return within %s; no ranked packet evidence is available from this request.", timeout)
		} else {
			errorText = "context_pack did not return before the active context deadline; no ranked packet evidence is available from this request."
		}
	case errors.Is(cause, context.Canceled):
		reason = "canceled"
		summary = "Context pack canceled"
		failureCode = failures.Canceled
		errorText = "context_pack was canceled before ranked packet evidence was available."
	case strings.TrimSpace(errorText) == "":
		errorText = "context_pack did not return ranked packet evidence."
	}

	payload := contextPackFallbackPayload{
		Schema:            contextpackcontract.FallbackSchema,
		Status:            contextpackcontract.FallbackStatusUnavailable,
		Reason:            reason,
		Tool:              ToolNameContextPack,
		Mode:              strings.TrimSpace(req.Mode),
		Path:              strings.TrimSpace(req.Path),
		Workdir:           strings.TrimSpace(req.Workdir),
		Language:          strings.TrimSpace(req.Language),
		Limit:             req.Limit,
		BudgetTokens:      req.BudgetTokens,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
		ForbiddenPaths:    append([]string(nil), req.ForbiddenPaths...),
		RetrySameRequest:  false,
		Uncertainty:       "The context packet was not produced, so candidate files, symbols, tests, and source ownership from context_pack are unknown.",
		FallbackCapabilities: []string{
			ToolNameContextStatus,
			ToolNameContextQuery,
			ToolNameSearchFiles,
			ToolNameSearchContent,
			ToolNameCodeNavigate,
			ToolNameReadFile,
			ToolNameDiscoveryQuery,
		},
	}

	if timeout > 0 {
		payload.Timeout = timeout.String()
	}

	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		output = []byte(fmt.Sprintf(`{"schema":%q,"status":%q,"reason":%q,"tool":%q,"retry_same_request":false}`, contextpackcontract.FallbackSchema, contextpackcontract.FallbackStatusUnavailable, reason, ToolNameContextPack))
	}

	return ToolResult{
		Tool:    ToolNameContextPack,
		Summary: summary,
		Error:   errorText,
		Output:  string(output),
		Failure: failures.New(failureCode, failures.NoReason, strings.TrimSuffix(errorText, ".")),
	}
}
