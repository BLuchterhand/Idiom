package productworkflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/brieflint"
)

type BriefRenderRequest struct {
	EffortRoot string
	OutputPath string
}

type BriefRenderResult struct {
	Status             ProductWorkflowStatus       `json:"status"`
	EffortRoot         string                      `json:"effort_root"`
	BriefOutputPath    string                      `json:"brief_output_path,omitempty"`
	Generated          BriefGenerationResult       `json:"generated"`
	CoverageEvaluation *CoverageGateEvaluation     `json:"coverage_evaluation,omitempty"`
	Lint               *brieflint.Report           `json:"lint,omitempty"`
	Blocker            string                      `json:"blocker,omitempty"`
	NextValidAction    string                      `json:"next_valid_action,omitempty"`
	CoverageEvent      *CoverageGateEvaluatedEvent `json:"-"`
}

func RenderImplementationBriefFromEffort(request BriefRenderRequest) BriefRenderResult {
	input, blockers := LoadBriefGenerationInput(request.EffortRoot)
	generated := GenerateImplementationBrief(input)
	allBlockers := append([]string{}, blockers...)
	allBlockers = append(allBlockers, generated.Blockers...)

	result := BriefRenderResult{
		Status:     ProductWorkflowStatusBlocked,
		EffortRoot: request.EffortRoot,
		Generated:  generated,
	}
	if strings.TrimSpace(string(input.CoverageEvaluation.Status)) != "" {
		evaluation := input.CoverageEvaluation
		event := BuildCoverageGateEvaluatedEvent(evaluation, input.TDD)
		result.CoverageEvaluation = &evaluation
		result.CoverageEvent = &event
	}

	briefPathForLint := ""
	tmp, err := os.CreateTemp("", "idiom-product-brief-*.md")
	if err != nil {
		allBlockers = append(allBlockers, fmt.Sprintf("failed to create temp brief for lint: %v", err))
	} else {
		briefPathForLint = tmp.Name()
		_ = tmp.Close()
		defer os.Remove(briefPathForLint)
	}

	if strings.TrimSpace(request.OutputPath) != "" {
		_, relPath, blocker := resolveBriefOutputPath(request.EffortRoot, request.OutputPath)
		if blocker != "" {
			allBlockers = append(allBlockers, blocker)
		} else {
			result.BriefOutputPath = filepath.ToSlash(relPath)
		}
	}

	if briefPathForLint != "" {
		if err := os.WriteFile(briefPathForLint, []byte(generated.RenderedBrief), 0o644); err != nil {
			allBlockers = append(allBlockers, fmt.Sprintf("failed to stage brief for lint: %v", err))
		} else {
			lintReport, lintErr := brieflint.BuildReport(context.Background(), briefPathForLint, "", 0)
			if lintErr != nil {
				allBlockers = append(allBlockers, fmt.Sprintf("brief lint failed: %v", lintErr))
			} else {
				result.Lint = &lintReport
				if lintReport.Exit != 0 {
					allBlockers = append(allBlockers, fmt.Sprintf("brief lint failed (exit %d)", lintReport.Exit))
				}
			}
		}
	}

	if len(allBlockers) == 0 && strings.TrimSpace(request.OutputPath) != "" {
		if err := writeEffortArtifactAtomic(request.EffortRoot, result.BriefOutputPath, []byte(generated.RenderedBrief), 0o644); err != nil {
			allBlockers = append(allBlockers, fmt.Sprintf("write brief output: %v", err))
		}
	}

	if len(allBlockers) == 0 {
		result.Status = ProductWorkflowStatusPass
		generated.Status = ProductWorkflowStatusPass
	} else {
		result.Blocker = strings.Join(allBlockers, "; ")
		generated.Status = ProductWorkflowStatusBlocked
		generated.Blockers = append([]string{}, allBlockers...)
	}
	result.NextValidAction = briefRenderNextValidAction(result.Status, result.BriefOutputPath)
	result.Generated = generated

	return result
}

func resolveBriefOutputPath(effortRoot string, relPath string) (string, string, string) {
	clean, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return "", "", fmt.Sprintf("brief output path %q must be effort-relative and must not contain path traversal", relPath)
	}
	if !strings.HasPrefix(filepath.ToSlash(clean), "briefs/") {
		return "", "", fmt.Sprintf("brief output path %q must be under briefs/", relPath)
	}

	absPath := filepath.Join(effortRoot, clean)
	if finding, outside := ValidateResearchPromptPathBoundary(effortRoot, absPath, clean); outside {
		return "", "", finding.Message
	}
	if info, err := os.Lstat(absPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Sprintf("brief output path %q must not be a symlink", clean)
	}

	return absPath, clean, ""
}

func briefRenderNextValidAction(status ProductWorkflowStatus, outputPath string) string {
	if status != ProductWorkflowStatusPass {
		return "resolve blockers before validating or dispatching this brief"
	}
	if strings.TrimSpace(outputPath) == "" {
		return "persist the lint-passing brief under briefs/<task>.md"
	}

	return "validate or dispatch from the persisted brief output"
}
