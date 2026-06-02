package planexecute

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/config"
	"idiom/internal/plancontract"
	"idiom/internal/workspace"
)

type planExecuteAutoExactPlanResult struct {
	Submitted       bool
	SourceSatisfied bool
	Result          workspace.ToolResult
	Reason          string
	EditCount       int
	AppliedCount    int
	PendingCount    int
	UnknownCount    int
	AppliedPaths    []string
	PendingPaths    []string
	UnknownPaths    []string
}

func planExecuteTryAutoExactPlan(cfg config.Config, prompt string, requiredEditPaths []string) planExecuteAutoExactPlanResult {
	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, cfg.Workspace.Root)
	if len(edits) == 0 {
		return planExecuteAutoExactPlanResult{Reason: reason}
	}
	classified := planExecuteClassifyAutoExactPlanEdits(cfg.Workspace.Root, edits)
	if classified.UnknownCount > 0 {
		classified.Reason = "exact-anchor progress unknown for required path(s): " + strings.Join(classified.UnknownPaths, ", ")
		classified.EditCount = len(edits)
		return classified
	}
	if classified.PendingCount == 0 {
		classified.SourceSatisfied = true
		classified.Reason = "all exact-anchor edits already match the current workspace"
		classified.EditCount = len(edits)
		return classified
	}
	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Deterministic exact-anchor plan synthesized from the Required Edits packet.",
		Edits:     planExecutePendingAutoExactEdits(cfg.Workspace.Root, edits),
	})
	classified.Result = result
	if strings.TrimSpace(result.Error) != "" {
		classified.Reason = "exact-anchor auto-plan rejected by submit_plan validation"
		classified.EditCount = classified.PendingCount
		return classified
	}
	classified.Submitted = true
	classified.EditCount = classified.PendingCount
	if classified.AppliedCount > 0 {
		classified.Reason = "already-applied exact edits skipped; pending exact edits submitted"
	} else {
		classified.Reason = "all required paths covered by parseable exact old_text/new_text pairs"
	}
	return classified
}

func planExecuteClassifyAutoExactPlanEdits(root string, edits []workspace.PlanEdit) planExecuteAutoExactPlanResult {
	var result planExecuteAutoExactPlanResult
	var appliedPaths []string
	var pendingPaths []string
	var unknownPaths []string
	for _, edit := range edits {
		status := planExecuteAutoExactEditStatus(root, edit)
		path := normalizePlanExecutePath(edit.Path)
		switch status {
		case "applied":
			result.AppliedCount++
			appliedPaths = append(appliedPaths, path)
		case "pending":
			result.PendingCount++
			pendingPaths = append(pendingPaths, path)
		default:
			result.UnknownCount++
			unknownPaths = append(unknownPaths, path)
		}
	}
	result.AppliedPaths = mergePlanExecutePaths(appliedPaths)
	result.PendingPaths = mergePlanExecutePaths(pendingPaths)
	result.UnknownPaths = mergePlanExecutePaths(unknownPaths)
	return result
}

func planExecutePendingAutoExactEdits(root string, edits []workspace.PlanEdit) []workspace.PlanEdit {
	var pending []workspace.PlanEdit
	for _, edit := range edits {
		if planExecuteAutoExactEditStatus(root, edit) == "pending" {
			pending = append(pending, edit)
		}
	}
	return pending
}

func planExecuteAutoExactEditStatus(root string, edit workspace.PlanEdit) string {
	path := normalizePlanExecutePath(edit.Path)
	if path == "" {
		return "unknown"
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(strings.TrimSpace(root), filepath.FromSlash(path))
	}
	body, err := os.ReadFile(filepath.Clean(fullPath))
	if err != nil {
		if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
			return "pending"
		}
		return "unknown"
	}
	status, _ := submittedPlanEditStatus(string(body), edit)
	return status
}

func planExecuteAutoSourceSatisfiedAnswer(requiredEditPaths []string) string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	var b strings.Builder
	b.WriteString("Decision: source-satisfied\n")
	b.WriteString("The requested final state is already present in the current workspace for every required edit path.")
	if len(paths) > 0 {
		b.WriteString("\n\nRequired edit paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func planExecuteSourceSatisfiedEvidencePaths(answer string, requiredEditPaths []string, allowedEditPaths []string) []string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(paths) > 0 {
		return paths
	}
	evidencePaths := planExecuteUniqueRequiredPaths(plancontract.SourceSatisfiedEvidencePaths(answer))
	allowed := planExecutePathSet(allowedEditPaths)
	if len(allowed) == 0 {
		return evidencePaths
	}
	var scoped []string
	for _, path := range evidencePaths {
		if _, ok := allowed[path]; ok {
			scoped = append(scoped, path)
		}
	}
	return scoped
}

func planExecuteMissingSourceSatisfiedCoveragePaths(answer string, requiredEditPaths []string) []string {
	return plancontract.SourceSatisfiedMissingCoveragePaths(answer, requiredEditPaths)
}

func planExecuteAutoPlanScanTexts(prompt string) []string {
	texts := []string{prompt}
	unquoted := planExecuteUnquoteMarkdown(prompt)
	if unquoted != prompt {
		texts = append(texts, unquoted)
	}
	return texts
}

func planExecuteUnquoteMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, ">") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, ">")
		trimmed = strings.TrimPrefix(trimmed, " ")
		lines[i] = trimmed
		changed = true
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

func planExecuteImplicitNewFileBodyContext(contextText string) bool {
	lower := strings.ToLower(contextText)
	return strings.Contains(lower, "create ") ||
		strings.Contains(lower, "new file") ||
		strings.Contains(lower, "full file body") ||
		strings.Contains(lower, "following content")
}
