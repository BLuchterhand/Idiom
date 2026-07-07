package planexecute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/gitstate"
)

func CollectReviewRepairEvidence(root string, paths []string, baseline *ReviewRepairBaseline) ReviewRepairEvidence {
	if baseline != nil {
		diff := collectReviewRepairDiffFromBaseline(root, paths, *baseline)
		check, exit := reviewRepairWhitespaceCheck(diff)

		return ReviewRepairEvidence{Diff: diff, DiffCheck: check, DiffCheckExit: exit}
	}

	return ReviewRepairEvidence{
		Diff:          runGitEvidence(root, append([]string{"diff", "--"}, paths...)...),
		DiffCheck:     runGitEvidence(root, append([]string{"diff", "--check", "--"}, paths...)...),
		DiffCheckExit: runGitEvidenceExit(root, append([]string{"diff", "--check", "--"}, paths...)...),
	}
}

func CaptureReviewRepairBaseline(root string, paths []string) ReviewRepairBaseline {
	baseline := ReviewRepairBaseline{Files: map[string]ReviewRepairFileSnapshot{}}

	for _, path := range mergePlanExecutePaths(paths) {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}

		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			baseline.Files[path] = ReviewRepairFileSnapshot{Exists: false}
			continue
		}

		baseline.Files[path] = ReviewRepairFileSnapshot{Before: string(data), Exists: true}
	}

	return baseline
}

func collectReviewRepairDiffFromBaseline(root string, paths []string, baseline ReviewRepairBaseline) string {
	var b strings.Builder

	for _, path := range mergePlanExecutePaths(paths) {
		snapshot, ok := baseline.Files[path]
		if !ok {
			snapshot = ReviewRepairFileSnapshot{Exists: false}
		}

		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}

		var after string

		afterData, err := os.ReadFile(filepath.Clean(full))

		afterExists := err == nil
		if err == nil {
			after = string(afterData)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(&b, "# %s: current content unavailable: %v\n", path, err)

			continue
		}

		if !snapshot.Exists && !afterExists {
			continue
		}

		diff := UnifiedDiff(path, snapshot.Before, after)
		if strings.TrimSpace(diff) == "" {
			continue
		}

		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}

		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
		b.WriteString(diff)

		if !strings.HasSuffix(diff, "\n") {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func runGitEvidence(root string, args ...string) string {
	result := gitstate.CombinedOutput(context.Background(), root, args...)

	if result.Err != nil && result.Stdout == "" {
		return result.Err.Error()
	}

	return result.Stdout
}

func runGitEvidenceExit(root string, args ...string) int {
	result := gitstate.CombinedOutput(context.Background(), root, args...)
	if result.Err != nil {
		if result.ExitError {
			return result.ExitCode
		}

		return 127
	}

	return 0
}

func planChangedPaths(plan planartifact.PlanArtifact) []string {
	paths := make([]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		paths = append(paths, edit.Path)
	}

	return mergePlanExecutePaths(paths)
}
