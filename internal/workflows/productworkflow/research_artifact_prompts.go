package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func PlanResearchArtifactPrompts(runPlan ResearchRunPlan, researchPromptDir string) ResearchArtifactPromptPlan {
	plan := ResearchArtifactPromptPlan{
		SelectedProfileID: runPlan.SelectedProfileID,
		SelectedProfile:   runPlan.SelectedProfile,
		ProfileCount:      runPlan.ProfileCount,
		ResearchOutputDir: runPlan.ResearchOutputDir,
		ResearchPromptDir: researchPromptDir,
		Runs:              []ResearchArtifactPromptRunSpec{},
		Findings:          append([]ProfileFinding{}, runPlan.Findings...),
	}
	if hasErrorFinding(plan.Findings) {
		sortProfileFindings(plan.Findings)

		return plan
	}

	cleanPromptDir, ok := cleanResearchOutputDir(researchPromptDir)
	if !ok {
		plan.Findings = append(plan.Findings, ProfileFinding{
			Kind:     "invalid_research_prompt_dir",
			Message:  fmt.Sprintf("research prompt directory %q must be effort-relative and must not contain path traversal", researchPromptDir),
			File:     researchPromptDir,
			Severity: ProductWorkflowSeverityError,
		})
		sortProfileFindings(plan.Findings)

		return plan
	}

	plan.ResearchPromptDir = cleanPromptDir

	runs := make([]ResearchArtifactPromptRunSpec, 0, len(runPlan.Runs))
	for _, run := range runPlan.Runs {
		base := fmt.Sprintf("%s--%s", runPlan.SelectedProfileID, run.ResearcherID)
		promptPath := filepath.ToSlash(filepath.Join(cleanPromptDir, base+".prompt.txt"))
		eventsPath := filepath.ToSlash(filepath.Join(runPlan.ResearchOutputDir, base+".events.jsonl"))
		reportPath := filepath.ToSlash(filepath.Join(runPlan.ResearchOutputDir, base+".report.json"))

		runs = append(runs, ResearchArtifactPromptRunSpec{
			ResearcherID:        run.ResearcherID,
			Alignment:           run.Alignment,
			Question:            run.Question,
			RequiredSources:     append([]string{}, run.RequiredSources...),
			AllowedCapabilities: append([]string{}, run.AllowedCapabilities...),
			OutputPath:          run.OutputPath,
			OutputContract:      run.OutputContract,
			PromptPath:          promptPath,
			EventsPath:          eventsPath,
			ReportPath:          reportPath,
			Command:             artifactCommandRecipe(promptPath, run.OutputPath, eventsPath, reportPath),
		})
	}

	plan.Runs = runs
	plan.RunCount = len(runs)
	sortProfileFindings(plan.Findings)

	return plan
}

func hasErrorFinding(findings []ProfileFinding) bool {
	for _, finding := range findings {
		if finding.Severity == ProductWorkflowSeverityError {
			return true
		}
	}

	return false
}

func RenderResearchArtifactPromptText(run ResearchArtifactPromptRunSpec) string {
	return strings.TrimSpace(fmt.Sprintf(`You are %s with alignment %s.

Research question:
%s

Required sources:
%s

Allowed capabilities:
%s

Output requirements:
- Write JSON only.
- Output path: %s
- Output contract schema_version: %s
- Emit a research cards document with fields schema_version, researcher_id, alignment, cards, and optional conflicts.
- Every card must include card_id, kind, source_handle, claim, confidence, freshness, uncertainty, evidence_refs, researcher_id, and timestamp.

Restrictions:
- Do not call tools or inspect the workspace.
- Cite only evidence handles or source summaries already supplied in the prompt context; required sources and allowed capabilities describe host-provided evidence boundaries, not permission to fetch more context.
- Do not include prose outside JSON.
- Do not include raw prompts, raw transcripts, or raw file bodies in output.
`, run.ResearcherID, run.Alignment, run.Question, renderListOrNone(run.RequiredSources), renderListOrNone(run.AllowedCapabilities), run.OutputPath, run.OutputContract.SchemaVersion)) + "\n"
}

func ValidateResearchPromptDirBoundary(effortRoot, promptDir, relDir string) (ProfileFinding, bool) {
	return researchPromptBoundaryFinding(effortRoot, promptDir, relDir, "research_prompt_dir_outside_effort", "research prompt directory")
}

func ValidateResearchPromptPathBoundary(effortRoot, promptPath, relPath string) (ProfileFinding, bool) {
	if info, err := os.Lstat(promptPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ProfileFinding{
			Kind:     "research_prompt_symlink",
			Message:  fmt.Sprintf("research prompt file %q must not be a symlink", relPath),
			File:     relPath,
			Severity: ProductWorkflowSeverityError,
		}, true
	} else if err != nil && !os.IsNotExist(err) {
		return ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to inspect research prompt file %q: %v", relPath, err),
			File:     relPath,
			Severity: ProductWorkflowSeverityError,
		}, true
	}

	return researchPromptBoundaryFinding(effortRoot, promptPath, relPath, "research_prompt_path_outside_effort", "research prompt path")
}

func researchPromptBoundaryFinding(effortRoot, absPath, relPath, outsideKind, label string) (ProfileFinding, bool) {
	rootEval, err := filepath.EvalSymlinks(effortRoot)
	if err != nil {
		return ProfileFinding{
			Kind:     "read_error",
			Message:  fmt.Sprintf("failed to resolve effort root %q: %v", effortRoot, err),
			File:     ".",
			Severity: ProductWorkflowSeverityError,
		}, true
	}

	existing := absPath
	for {
		eval, evalErr := filepath.EvalSymlinks(existing)
		if evalErr == nil {
			rootEval = filepath.Clean(rootEval)
			eval = filepath.Clean(eval)

			if eval == rootEval {
				return ProfileFinding{}, false
			}

			rel, relErr := filepath.Rel(rootEval, eval)
			if relErr != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
				return ProfileFinding{
					Kind:     outsideKind,
					Message:  fmt.Sprintf("%s %q resolves outside the effort root", label, relPath),
					File:     relPath,
					Severity: ProductWorkflowSeverityError,
				}, true
			}

			return ProfileFinding{}, false
		}

		if !os.IsNotExist(evalErr) {
			return ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to resolve %s %q: %v", label, relPath, evalErr),
				File:     relPath,
				Severity: ProductWorkflowSeverityError,
			}, true
		}

		parent := filepath.Dir(existing)
		if parent == existing {
			return ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to resolve %s %q: no existing ancestor", label, relPath),
				File:     relPath,
				Severity: ProductWorkflowSeverityError,
			}, true
		}

		existing = parent
	}
}

func artifactCommandRecipe(promptPath, outputPath, eventsPath, reportPath string) string {
	return artifactCommandRecipeWithValidator(promptPath, outputPath, eventsPath, reportPath, "")
}

func artifactCommandRecipeWithValidator(promptPath, outputPath, eventsPath, reportPath, validator string) string {
	parts := []string{
		"idiom artifact",
		"--prompt-file " + shellQuoteArg(promptPath),
		"--output " + shellQuoteArg(outputPath),
		"--json-out " + shellQuoteArg(eventsPath),
		"--report-out " + shellQuoteArg(reportPath),
		"--no-tools",
		"--mechanical-repair",
	}
	if strings.TrimSpace(validator) != "" {
		parts = append(parts, "--validator "+shellQuoteArg(validator))
	}

	return strings.Join(parts, " ")
}

func productArtifactValidatorCommand(kind string) string {
	return fmt.Sprintf(
		`idiom product validate-artifact --kind %s --path "$IDIOM_ARTIFACT"`,
		shellQuoteArg(kind),
	)
}

func shellQuoteArg(value string) string {
	if value == "" {
		return "''"
	}

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}

		return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
	}

	return value
}

func renderListOrNone(values []string) string {
	if len(values) == 0 {
		return "- none"
	}

	var b strings.Builder

	for _, value := range values {
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

func SortProfileFindings(findings []ProfileFinding) {
	sortProfileFindings(findings)
}
