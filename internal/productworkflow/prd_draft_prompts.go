package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultPRDDraftPromptPath = "prd.prompt.md"
	defaultPRDOutputPath      = "prd.md"
	defaultPRDEventsPath      = "events/prd-draft.events.jsonl"
	defaultPRDReportPath      = "reports/prd-draft.report.json"
)

func PlanPRDDraftPrompt(effortRoot, promptPath, prdOutputPath string, evidence []EvidenceCard) PRDDraftPromptPlan {
	plan := PRDDraftPromptPlan{
		EvidenceLedgerPath: "evidence/evidence.jsonl",
		PromptPath:         defaultIfBlank(promptPath, defaultPRDDraftPromptPath),
		PRDOutputPath:      defaultIfBlank(prdOutputPath, defaultPRDOutputPath),
		EventsPath:         defaultPRDEventsPath,
		ReportPath:         defaultPRDReportPath,
		EvidenceCardIDs:    []string{},
		EvidenceSummaries:  []PRDEvidenceCardSummary{},
		Findings:           []ProfileFinding{},
	}

	if cleanPrompt, ok := cleanResearchOutputDir(plan.PromptPath); ok {
		plan.PromptPath = filepath.ToSlash(cleanPrompt)
	} else {
		plan.Findings = append(plan.Findings, ProfileFinding{
			Kind:     "invalid_prd_prompt_path",
			Message:  fmt.Sprintf("PRD prompt path %q must be effort-relative and must not contain path traversal", plan.PromptPath),
			File:     plan.PromptPath,
			Severity: "error",
		})
	}
	if cleanOutput, ok := cleanResearchOutputDir(plan.PRDOutputPath); ok {
		plan.PRDOutputPath = filepath.ToSlash(cleanOutput)
	} else {
		plan.Findings = append(plan.Findings, ProfileFinding{
			Kind:     "invalid_prd_output_path",
			Message:  fmt.Sprintf("PRD output path %q must be effort-relative and must not contain path traversal", plan.PRDOutputPath),
			File:     plan.PRDOutputPath,
			Severity: "error",
		})
	}

	if !hasErrorFinding(plan.Findings) {
		promptAbs := filepath.Join(effortRoot, plan.PromptPath)
		if finding, outside := ValidateResearchPromptPathBoundary(effortRoot, promptAbs, plan.PromptPath); outside {
			plan.Findings = append(plan.Findings, finding)
		}
		validatePRDPath := func(relPath, outsideKind, label string) {
			absPath := filepath.Join(effortRoot, relPath)
			if finding, outside := researchPromptBoundaryFinding(effortRoot, absPath, relPath, outsideKind, label); outside {
				plan.Findings = append(plan.Findings, finding)
			}
		}
		validatePRDPath(plan.PRDOutputPath, "prd_output_path_outside_effort", "PRD output path")
		validatePRDPath(plan.EventsPath, "prd_events_path_outside_effort", "PRD events path")
		validatePRDPath(plan.ReportPath, "prd_report_path_outside_effort", "PRD report path")
	}

	for _, card := range evidence {
		id := strings.TrimSpace(card.CardID)
		if id == "" {
			continue
		}
		plan.EvidenceCardIDs = append(plan.EvidenceCardIDs, id)
		plan.EvidenceSummaries = append(plan.EvidenceSummaries, PRDEvidenceCardSummary{
			CardID:      id,
			Kind:        strings.TrimSpace(card.Kind),
			Confidence:  strings.TrimSpace(card.Confidence),
			Freshness:   strings.TrimSpace(card.Freshness),
			Claim:       shorten(card.Claim, 140),
			Uncertainty: shorten(card.Uncertainty, 140),
		})
	}
	sort.Slice(plan.EvidenceSummaries, func(i, j int) bool { return plan.EvidenceSummaries[i].CardID < plan.EvidenceSummaries[j].CardID })
	sort.Strings(plan.EvidenceCardIDs)
	plan.EvidenceCardCount = len(plan.EvidenceCardIDs)
	plan.Command = artifactCommandRecipeWithValidator(plan.PromptPath, plan.PRDOutputPath, plan.EventsPath, plan.ReportPath, productArtifactValidatorCommand("prd"))
	sortProfileFindings(plan.Findings)
	return plan
}

func RenderPRDDraftPromptText(plan PRDDraftPromptPlan) string {
	var b strings.Builder
	b.WriteString("Draft a Product Requirements Document (PRD) from the evidence ledger summaries below.\n\n")
	b.WriteString("PRD contract requirements:\n")
	b.WriteString("- Follow the existing PRD parser/audit contract.\n")
	b.WriteString("- Include required front matter fields: schema_version, prd_id, status, task_id, and created_at.\n")
	b.WriteString("- Use status: draft. Leave accepted_at, accepted_by, and acceptance_profile absent unless a human explicitly provided them.\n")
	b.WriteString("- Do not use markdown tables for requirements; the parser only recognizes bullet fields.\n")
	b.WriteString("- For each requirement block, emit these exact bullet keys: - **id**:, - **priority**:, - **statement**:, - **evidence_refs**:, - **tdd_obligations**:, - **owner_alignment**:, - **testability**:, - **metric**:, - **open_questions**:.\n")
	b.WriteString("- Inline array fields must use bracket syntax, for example [\"EV-001\"] or [].\n")
	b.WriteString("- Use evidence_refs that point to card IDs from the supplied evidence list.\n")
	b.WriteString("- Preserve unsupported claims and conflicts as open questions or explicitly labeled inference.\n")
	b.WriteString("- Do not claim acceptance, approval, or final decision status.\n")
	b.WriteString("- Keep output to PRD markdown only.\n\n")

	b.WriteString("Output destinations (for host orchestration):\n")
	b.WriteString("- PRD output path: " + plan.PRDOutputPath + "\n")
	b.WriteString("- Events path: " + plan.EventsPath + "\n")
	b.WriteString("- Report path: " + plan.ReportPath + "\n")
	b.WriteString("- Command recipe: " + plan.Command + "\n\n")

	b.WriteString(fmt.Sprintf("Evidence cards (%d total):\n", plan.EvidenceCardCount))
	for _, summary := range plan.EvidenceSummaries {
		b.WriteString(fmt.Sprintf("- %s | kind=%s | confidence=%s | freshness=%s | claim=%s", summary.CardID, orUnknown(summary.Kind), orUnknown(summary.Confidence), orUnknown(summary.Freshness), summary.Claim))
		if strings.TrimSpace(summary.Uncertainty) != "" {
			b.WriteString(fmt.Sprintf(" | uncertainty=%s", summary.Uncertainty))
		}
		b.WriteByte('\n')
	}
	if len(plan.EvidenceSummaries) == 0 {
		b.WriteString("- none\n")
	}
	return b.String()
}

func defaultIfBlank(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func shorten(value string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
