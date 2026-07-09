package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/workflows/productworkflow"
)

func (m *Manager) productEfforts(_ context.Context, req ToolRequest) ToolResult {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		root = productworkflow.DefaultProductEffortsRoot
	}

	absRoot, err := m.pathAccess.ResolvePath(root, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "product_efforts", Summary: "Product efforts rejected", Error: err.Error()}
	}

	result, err := productworkflow.BrowseProductEfforts(productworkflow.ProductEffortsOptions{
		Root:   absRoot,
		Status: req.Status,
		Query:  req.Query,
	})

	output, encodeErr := encodeProductToolOutput(result)
	if encodeErr != nil {
		return ToolResult{Tool: "product_efforts", Summary: "Product efforts encode failed", Error: encodeErr.Error()}
	}

	toolResult := ToolResult{
		Tool:    "product_efforts",
		Summary: fmt.Sprintf("Product efforts status=%s efforts=%d findings=%d", result.Status, result.Counts.EffortCount, result.Counts.FindingCount),
		Output:  output,
	}
	if err != nil {
		toolResult.Error = err.Error()
	}

	return toolResult
}

func (m *Manager) productStatus(_ context.Context, req ToolRequest) ToolResult {
	effortRoot := strings.TrimSpace(req.EffortRoot)
	if effortRoot == "" {
		effortRoot = strings.TrimSpace(req.Path)
	}

	if effortRoot == "" {
		return ToolResult{
			Tool:    "product_status",
			Summary: "Product status rejected",
			Error:   "effort_root is required",
		}
	}

	absRoot, err := m.pathAccess.ResolvePath(effortRoot, req.Workdir)
	if err != nil {
		return ToolResult{
			Tool:    "product_status",
			Summary: "Product status rejected",
			Error:   fmt.Sprintf("resolve effort root: %v", err),
		}
	}

	view, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(absRoot)

	var payload any = view

	if req.OperatorView {
		payload = productworkflow.BuildProductOperatorStatusView(view, workflowState)
	}

	output, encodeErr := encodeProductToolOutput(payload)
	if encodeErr != nil {
		return ToolResult{Tool: "product_status", Summary: "Product status encode failed", Error: encodeErr.Error()}
	}

	result := ToolResult{
		Tool:    "product_status",
		Summary: fmt.Sprintf("Product status audit=%s effort=%s state=%s/%s", view.AuditStatus, view.EffortID, view.CurrentPhase, view.CurrentState),
		Output:  output,
	}
	if statusErr != nil {
		result.Error = statusErr.Error()
	}

	return result
}

func encodeProductToolOutput(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}
