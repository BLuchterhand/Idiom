package refine

import (
	"context"
	"io"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"idiom/internal/workflows/brieflint"
)

func stubRefineLint(reportStub func(path string) (brieflint.Report, error), textStub func(path string, stdout, stderr io.Writer) (brieflint.TextRun, error)) {
	origReport := refineBuildLintReport
	origText := refineRunLintText

	if reportStub != nil {
		refineBuildLintReport = func(_ context.Context, path string, _ string, _ time.Duration) (brieflint.Report, error) {
			return reportStub(path)
		}
	}
	if textStub != nil {
		refineRunLintText = func(_ context.Context, path string, _ string, _ time.Duration, stdout io.Writer, stderr io.Writer) (brieflint.TextRun, error) {
			return textStub(path, stdout, stderr)
		}
	}
	ginkgo.DeferCleanup(func() {
		refineBuildLintReport = origReport
		refineRunLintText = origText
	})
}

func stubRefineLintPass() {
	stubRefineLint(func(path string) (brieflint.Report, error) {
		return brieflint.Report{Brief: path, Exit: 0}, nil
	}, func(string, io.Writer, io.Writer) (brieflint.TextRun, error) {
		return brieflint.TextRun{Exit: 0}, nil
	})
}

func refineLintReport(path string, exit int, findings ...brieflint.Finding) brieflint.Report {
	for i := range findings {
		if findings[i].Brief == "" {
			findings[i].Brief = path
		}
	}

	return brieflint.Report{Brief: path, Exit: exit, Findings: findings}
}
