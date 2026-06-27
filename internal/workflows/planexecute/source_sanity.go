package planexecute

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
)

type SourceSanityResult struct {
	Summary string
	Error   string
}

type SourceSanityRunner func(context.Context, config.Workspace, string) SourceSanityResult

type SourceSanityCoverage func(string) bool

func runPlanExecuteSourceSanity(ctx context.Context, wsCfg config.Workspace, root string, paths []string, jsonEvents bool, stdout io.Writer, run SourceSanityRunner, covers SourceSanityCoverage) (int, []plancontract.PhaseEvidenceCheck, error) {
	deps := normalizeDependencies(Dependencies{SourceSanity: run, SourceSanityCovers: covers})
	checked := 0
	evidence := make([]plancontract.PhaseEvidenceCheck, 0)

	for _, path := range mergePlanExecutePaths(paths) {
		if !deps.SourceSanityCovers(path) {
			continue
		}

		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, path)
		}

		info, err := os.Stat(filepath.Clean(fullPath))
		if err != nil || info.IsDir() {
			continue
		}

		checked++

		result := deps.SourceSanity(ctx, wsCfg, path)
		status := plancontract.PhaseEvidenceStatusOK
		errText := strings.TrimSpace(result.Error)

		if errText != "" {
			status = plancontract.PhaseEvidenceStatusFailed
		}

		evidence = append(evidence, plancontract.PhaseEvidenceCheck{
			Kind:    plancontract.PhaseEvidenceKindSourceSanity,
			Path:    path,
			Status:  status,
			Summary: strings.TrimSpace(result.Summary),
			Error:   errText,
		})
		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventSourceSanity, map[string]any{
			plancontract.PlanExecuteEventFieldPath:      path,
			plancontract.PlanExecuteTerminalFieldStatus: string(status),
			plancontract.PlanExecuteEventFieldSummary:   strings.TrimSpace(result.Summary),
			plancontract.PlanExecuteEventFieldError:     errText,
		}); err != nil {
			return checked, evidence, planExecuteJSONEventWriteError(err)
		}

		if !jsonEvents {
			fmt.Fprintf(stdout, "  source sanity %s ... %s\n", path, status)
		}

		if errText != "" {
			return checked, evidence, fmt.Errorf("source_sanity failed for %s: %s", path, errText)
		}
	}

	return checked, evidence, nil
}
