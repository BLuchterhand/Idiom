package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultADRDraftPromptPath = "adrs/adr.prompt.md"
	defaultADROutputPath      = "adrs/ADR-draft.md"
	defaultADREventsPath      = "events/adr-draft.events.jsonl"
	defaultADRReportPath      = "reports/adr-draft.report.json"
)

func PlanADRDraftPrompt(effortRoot, promptPath, adrOutputPath string, evidence []EvidenceCard, prd PRDDocument) ADRDraftPromptPlan {
	plan := ADRDraftPromptPlan{
		EvidenceLedgerPath:    "evidence/evidence.jsonl",
		PRDInputPath:          "prd.md",
		PromptPath:            defaultIfBlank(promptPath, defaultADRDraftPromptPath),
		ADROutputPath:         defaultIfBlank(adrOutputPath, defaultADROutputPath),
		EventsPath:            defaultADREventsPath,
		ReportPath:            defaultADRReportPath,
		EvidenceCardIDs:       []string{},
		EvidenceSummaries:     []ADREvidenceCardSummary{},
		PRDRequirementIDs:     []string{},
		PRDRequirementSummary: []ADRPRDRequirementSummary{},
		Findings:              []ProfileFinding{},
	}

	if cleanPrompt, ok := cleanResearchOutputDir(plan.PromptPath); ok {
		plan.PromptPath = filepath.ToSlash(cleanPrompt)
	} else {
		plan.Findings = append(plan.Findings, ProfileFinding{Kind: "invalid_adr_prompt_path", Message: fmt.Sprintf("ADR prompt path %q must be effort-relative and must not contain path traversal", plan.PromptPath), File: plan.PromptPath, Severity: "error"})
	}
	if cleanOutput, ok := cleanResearchOutputDir(plan.ADROutputPath); ok {
		plan.ADROutputPath = filepath.ToSlash(cleanOutput)
	} else {
		plan.Findings = append(plan.Findings, ProfileFinding{Kind: "invalid_adr_output_path", Message: fmt.Sprintf("ADR output path %q must be effort-relative and must not contain path traversal", plan.ADROutputPath), File: plan.ADROutputPath, Severity: "error"})
	}

	if !hasErrorFinding(plan.Findings) {
		promptAbs := filepath.Join(effortRoot, plan.PromptPath)
		if finding, outside := ValidateResearchPromptPathBoundary(effortRoot, promptAbs, plan.PromptPath); outside {
			plan.Findings = append(plan.Findings, finding)
		}
		validateADRPath := func(relPath, outsideKind, label string) {
			absPath := filepath.Join(effortRoot, relPath)
			if finding, outside := researchPromptBoundaryFinding(effortRoot, absPath, relPath, outsideKind, label); outside {
				plan.Findings = append(plan.Findings, finding)
			}
		}
		validateADRPath(plan.ADROutputPath, "adr_output_path_outside_effort", "ADR output path")
		validateADRPath(plan.EventsPath, "adr_events_path_outside_effort", "ADR events path")
		validateADRPath(plan.ReportPath, "adr_report_path_outside_effort", "ADR report path")
	}

	for _, card := range evidence {
		id := strings.TrimSpace(card.CardID)
		if id == "" {
			continue
		}
		plan.EvidenceCardIDs = append(plan.EvidenceCardIDs, id)
		plan.EvidenceSummaries = append(plan.EvidenceSummaries, ADREvidenceCardSummary{
			CardID:      id,
			Kind:        strings.TrimSpace(card.Kind),
			Confidence:  strings.TrimSpace(card.Confidence),
			Freshness:   strings.TrimSpace(card.Freshness),
			Claim:       shorten(card.Claim, 140),
			Uncertainty: shorten(card.Uncertainty, 140),
		})
	}
	for _, req := range prd.Requirements {
		id := strings.TrimSpace(req.ID)
		if id == "" {
			continue
		}
		plan.PRDRequirementIDs = append(plan.PRDRequirementIDs, id)
		plan.PRDRequirementSummary = append(plan.PRDRequirementSummary, ADRPRDRequirementSummary{
			ID:          id,
			Priority:    strings.TrimSpace(req.Priority),
			Statement:   shorten(req.Statement, 160),
			Evidence:    len(req.EvidenceRefs),
			Obligations: len(req.TDDObligations),
		})
	}
	sort.Slice(plan.EvidenceSummaries, func(i, j int) bool { return plan.EvidenceSummaries[i].CardID < plan.EvidenceSummaries[j].CardID })
	sort.Slice(plan.PRDRequirementSummary, func(i, j int) bool { return plan.PRDRequirementSummary[i].ID < plan.PRDRequirementSummary[j].ID })
	sort.Strings(plan.EvidenceCardIDs)
	sort.Strings(plan.PRDRequirementIDs)
	plan.EvidenceCardCount = len(plan.EvidenceCardIDs)
	plan.PRDRequirementCount = len(plan.PRDRequirementIDs)
	plan.Command = artifactCommandRecipe(plan.PromptPath, plan.ADROutputPath, plan.EventsPath, plan.ReportPath)
	sortProfileFindings(plan.Findings)
	return plan
}

func RenderADRDraftPromptText(plan ADRDraftPromptPlan) string {
	var b strings.Builder
	b.WriteString("Draft an Architecture Decision Record (ADR) markdown document from the PRD requirements and evidence summaries below.\n\n")
	b.WriteString("ADR contract requirements:\n")
	b.WriteString("- Follow the existing ADR parser/audit contract.\n")
	b.WriteString("- Include front matter fields: schema_version, adr_id, status, prd_refs, evidence_refs, tdd_obligations, coverage_tier.\n")
	b.WriteString("- Keep acceptance metadata fields empty unless a human explicitly provided them: accepted_at, accepted_by, acceptance_profile.\n")
	b.WriteString("- Body sections are required: Context, Decision, Alternatives Considered, Evidence, Consequences, Compatibility and Migration, TDD Obligations, Rollback, Open Questions.\n")
	b.WriteString("- Cite PRD requirement IDs from the supplied PRD requirement list and evidence card IDs from the supplied evidence list.\n")
	b.WriteString("- Preserve conflicts and unsupported claims as open questions or explicitly labeled inference.\n")
	b.WriteString("- Do not claim acceptance, approval, or supersession.\n")
	b.WriteString("- Keep output to ADR markdown only.\n\n")

	b.WriteString("Output destinations (for host orchestration):\n")
	b.WriteString("- ADR output path: " + plan.ADROutputPath + "\n")
	b.WriteString("- Events path: " + plan.EventsPath + "\n")
	b.WriteString("- Report path: " + plan.ReportPath + "\n")
	b.WriteString("- Command recipe: " + plan.Command + "\n\n")

	b.WriteString(fmt.Sprintf("PRD requirements (%d total):\n", plan.PRDRequirementCount))
	for _, req := range plan.PRDRequirementSummary {
		b.WriteString(fmt.Sprintf("- %s | priority=%s | statement=%s | evidence_refs=%d | tdd_obligations=%d\n", req.ID, orUnknown(req.Priority), req.Statement, req.Evidence, req.Obligations))
	}
	if len(plan.PRDRequirementSummary) == 0 {
		b.WriteString("- none\n")
	}
	b.WriteByte('\n')

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
