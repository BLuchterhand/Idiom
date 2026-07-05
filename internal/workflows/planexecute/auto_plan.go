package planexecute

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
)

type planExecuteAutoExactPlanResult struct {
	Submitted       bool
	SourceSatisfied bool
	Result          AutoPlanSubmitResult
	Reason          string
	EditCount       int
	AppliedCount    int
	PendingCount    int
	UnknownCount    int
	AppliedPaths    []string
	PendingPaths    []string
	UnknownPaths    []string
}

type AutoPlanSubmitRequest struct {
	Rationale string
	Edits     []planartifact.PlanEdit
}

type AutoPlanSubmitResult struct {
	Summary string
	Error   string
}

type AutoPlanSubmitter func(context.Context, config.Workspace, AutoPlanSubmitRequest) AutoPlanSubmitResult

func planExecuteTryAutoExactPlan(ctx context.Context, cfg config.Config, prompt string, requiredEditPaths []string, submit AutoPlanSubmitter) planExecuteAutoExactPlanResult {
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

	submit = normalizeDependencies(Dependencies{SubmitAutoPlan: submit}).SubmitAutoPlan
	result := submit(planExecuteContext(ctx), cfg.Workspace, AutoPlanSubmitRequest{
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

func planExecuteClassifyAutoExactPlanEdits(root string, edits []planartifact.PlanEdit) planExecuteAutoExactPlanResult {
	var (
		result       planExecuteAutoExactPlanResult
		appliedPaths []string
		pendingPaths []string
		unknownPaths []string
	)

	for _, edit := range edits {
		status := planExecuteAutoExactEditStatus(root, edit)
		path := plancontract.NormalizeScopePath(edit.Path)

		switch status {
		case plancontract.PhaseEvidenceStatusApplied:
			result.AppliedCount++

			appliedPaths = append(appliedPaths, path)
		case plancontract.PhaseEvidenceStatusPending:
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

func planExecutePendingAutoExactEdits(root string, edits []planartifact.PlanEdit) []planartifact.PlanEdit {
	var pending []planartifact.PlanEdit

	for _, edit := range edits {
		if planExecuteAutoExactEditStatus(root, edit) == plancontract.PhaseEvidenceStatusPending {
			pending = append(pending, edit)
		}
	}

	return pending
}

func planExecuteAutoExactEditStatus(root string, edit planartifact.PlanEdit) plancontract.PhaseEvidenceStatus {
	path := plancontract.NormalizeScopePath(edit.Path)
	if path == "" {
		return plancontract.PhaseEvidenceStatusUnknown
	}

	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(strings.TrimSpace(root), filepath.FromSlash(path))
	}

	body, err := os.ReadFile(filepath.Clean(fullPath))
	if err != nil {
		if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
			return plancontract.PhaseEvidenceStatusPending
		}

		return plancontract.PhaseEvidenceStatusUnknown
	}

	status, _ := submittedPlanEditStatus(string(body), edit)

	return status
}

func planExecuteAutoSourceSatisfiedAnswer(requiredEditPaths []string) string {
	paths := plancontract.UniqueScopePaths(requiredEditPaths)

	var b strings.Builder

	b.WriteString(plancontract.SourceSatisfiedDecision)
	b.WriteByte('\n')
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
	paths := plancontract.UniqueScopePaths(requiredEditPaths)
	if len(paths) > 0 {
		return paths
	}

	evidencePaths := plancontract.UniqueScopePaths(plancontract.SourceSatisfiedEvidencePaths(answer))
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

func planExecuteSourceSatisfiedUnsupportedEvidenceMarkers() []string {
	return []string{
		"not shown in full",
		"implied by",
		"not shown",
		"assumed",
		"likely",
		"probably",
		"appears to",
		"seems to",
		"should already",
		"stale anchor",
		"stale old_text",
		"stale spec",
	}
}

func planExecuteSourceSatisfiedUnsupportedEvidenceMarker(answer string, evidencePaths []string) string {
	pathSet := planExecutePathSet(evidencePaths)
	for _, line := range strings.Split(answer, "\n") {
		if len(pathSet) > 0 {
			linePaths := plancontract.UniqueScopePaths(pathparse.MentionedWorkspacePaths(line))
			matchesEvidencePath := false

			for _, path := range linePaths {
				if _, ok := pathSet[path]; ok {
					matchesEvidencePath = true
					break
				}
			}

			if !matchesEvidencePath {
				continue
			}
		}

		if marker := planExecuteSourceSatisfiedUnsupportedMarkerInText(line); marker != "" {
			return marker
		}
	}

	return ""
}

func planExecuteSourceSatisfiedUnsupportedEvidenceMarkerListForPrompt() string {
	var quoted []string

	for _, marker := range planExecuteSourceSatisfiedUnsupportedEvidenceMarkers() {
		quoted = append(quoted, "`"+marker+"`")
	}

	return strings.Join(quoted, ", ")
}

func planExecuteSourceSatisfiedUnsupportedMarkerInText(text string) string {
	lower := strings.ToLower(text)
	for _, marker := range planExecuteSourceSatisfiedUnsupportedEvidenceMarkers() {
		if planExecuteTextContainsUnsupportedEvidenceMarker(lower, marker) {
			return marker
		}
	}

	return ""
}

func planExecuteTextContainsUnsupportedEvidenceMarker(lowerText string, marker string) bool {
	start := 0
	for {
		idx := strings.Index(lowerText[start:], marker)
		if idx < 0 {
			return false
		}

		idx += start

		before := idx - 1
		after := idx + len(marker)

		if planExecuteUnsupportedMarkerBoundary(lowerText, before) &&
			planExecuteUnsupportedMarkerBoundary(lowerText, after) {

			return true
		}

		start = idx + len(marker)
		if start >= len(lowerText) {
			return false
		}
	}
}

func planExecuteUnsupportedMarkerBoundary(text string, idx int) bool {
	if idx < 0 || idx >= len(text) {
		return true
	}

	ch := text[idx]

	return !((ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_' ||
		ch == '-')
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
	return strings.Contains(lower, "following content") ||
		strings.Contains(lower, "full file body")
}
