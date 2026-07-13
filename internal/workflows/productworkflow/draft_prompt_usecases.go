package productworkflow

import (
	"errors"
	"fmt"
	"io/fs"
)

type PRDDraftPromptRenderRequest struct {
	EffortRoot    string
	PromptPath    string
	PRDOutputPath string
}

type PRDDraftPromptRenderResult struct {
	Status           ProductWorkflowStatus `json:"status"`
	EffortRoot       string                `json:"effort_root"`
	PromptWriteCount int                   `json:"prompt_write_count"`
	Plan             PRDDraftPromptPlan    `json:"plan"`
}

type ADRDraftPromptRenderRequest struct {
	EffortRoot    string
	PromptPath    string
	ADROutputPath string
}

type ADRDraftPromptRenderResult struct {
	Status           ProductWorkflowStatus `json:"status"`
	EffortRoot       string                `json:"effort_root"`
	PromptWriteCount int                   `json:"prompt_write_count"`
	Plan             ADRDraftPromptPlan    `json:"plan"`
}

func RenderPRDDraftPromptFromEffort(request PRDDraftPromptRenderRequest) (PRDDraftPromptRenderResult, error) {
	evidence, evidenceFindings := loadEffortEvidenceCards(request.EffortRoot)
	ledger, ledgerFindings := loadEffortTDDLedger(request.EffortRoot)
	plan := PlanPRDDraftPrompt(request.EffortRoot, request.PromptPath, request.PRDOutputPath, evidence, ledger)
	planFindings := plan.Findings

	plan.Findings = append(evidenceFindings, ledgerFindings...)
	plan.Findings = append(plan.Findings, planFindings...)
	SortProfileFindings(plan.Findings)

	result := PRDDraftPromptRenderResult{
		Status:     ProductWorkflowStatusBlocked,
		EffortRoot: request.EffortRoot,
		Plan:       plan,
	}
	if !hasErrorFinding(plan.Findings) {
		if err := writeDraftPrompt(request.EffortRoot, plan.PromptPath, RenderPRDDraftPromptText(plan)); err != nil {
			return result, err
		}
		result.Status = ProductWorkflowStatusPass
		result.PromptWriteCount = 1
	}

	normalizePRDDraftPromptPlan(&result.Plan)

	return result, nil
}

func RenderADRDraftPromptFromEffort(request ADRDraftPromptRenderRequest) (ADRDraftPromptRenderResult, error) {
	evidence, evidenceFindings := loadEffortEvidenceCards(request.EffortRoot)
	prd, prdFindings := loadEffortPRD(request.EffortRoot)
	plan := PlanADRDraftPrompt(request.EffortRoot, request.PromptPath, request.ADROutputPath, evidence, prd)
	planFindings := plan.Findings

	plan.Findings = append(evidenceFindings, prdFindings...)
	plan.Findings = append(plan.Findings, planFindings...)
	SortProfileFindings(plan.Findings)

	result := ADRDraftPromptRenderResult{
		Status:     ProductWorkflowStatusBlocked,
		EffortRoot: request.EffortRoot,
		Plan:       plan,
	}
	if !hasErrorFinding(plan.Findings) {
		if err := writeDraftPrompt(request.EffortRoot, plan.PromptPath, RenderADRDraftPromptText(plan)); err != nil {
			return result, err
		}
		result.Status = ProductWorkflowStatusPass
		result.PromptWriteCount = 1
	}

	normalizeADRDraftPromptPlan(&result.Plan)

	return result, nil
}

func loadEffortEvidenceCards(effortRoot string) ([]EvidenceCard, []ProfileFinding) {
	const relPath = "evidence/evidence.jsonl"

	data, err := readEffortArtifact(effortRoot, relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, []ProfileFinding{profileArtifactFinding("missing_evidence_ledger", "required evidence/evidence.jsonl not found", relPath)}
		}
		return nil, []ProfileFinding{profileArtifactFinding("read_error", fmt.Sprintf("failed to read evidence/evidence.jsonl: %v", err), relPath)}
	}

	cards, err := ParseEvidenceCardsJSONL(data)
	if err != nil {
		return nil, []ProfileFinding{profileArtifactFinding("validation_error", fmt.Sprintf("existing evidence ledger failed validation: %v", err), relPath)}
	}

	return cards, nil
}

func loadEffortTDDLedger(effortRoot string) (TDDLedger, []ProfileFinding) {
	const relPath = "tdd.json"

	data, err := readEffortArtifact(effortRoot, relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return TDDLedger{}, []ProfileFinding{profileArtifactFinding("missing_tdd_ledger", "required tdd.json not found", relPath)}
		}
		return TDDLedger{}, []ProfileFinding{profileArtifactFinding("read_error", fmt.Sprintf("failed to read tdd.json: %v", err), relPath)}
	}

	ledger, err := ParseTDDLedgerJSON(data)
	if err != nil {
		return TDDLedger{}, []ProfileFinding{profileArtifactFinding("validation_error", fmt.Sprintf("existing tdd.json failed validation: %v", err), relPath)}
	}

	return ledger, nil
}

func loadEffortPRD(effortRoot string) (PRDDocument, []ProfileFinding) {
	const relPath = "prd.md"

	data, err := readEffortArtifact(effortRoot, relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PRDDocument{}, []ProfileFinding{profileArtifactFinding("missing_prd", "required prd.md not found", relPath)}
		}
		return PRDDocument{}, []ProfileFinding{profileArtifactFinding("read_error", fmt.Sprintf("failed to read prd.md: %v", err), relPath)}
	}

	prd, err := ParsePRDMarkdown(data)
	if err != nil {
		return PRDDocument{}, []ProfileFinding{profileArtifactFinding("validation_error", fmt.Sprintf("existing prd.md failed validation: %v", err), relPath)}
	}

	return prd, nil
}

func profileArtifactFinding(kind string, message string, file string) ProfileFinding {
	return ProfileFinding{Kind: kind, Message: message, File: file, Severity: ProductWorkflowSeverityError}
}

func writeDraftPrompt(effortRoot string, relPath string, body string) error {
	if err := writeEffortArtifactAtomic(effortRoot, relPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}

	return nil
}

func normalizePRDDraftPromptPlan(plan *PRDDraftPromptPlan) {
	if plan.EvidenceCardIDs == nil {
		plan.EvidenceCardIDs = []string{}
	}
	if plan.EvidenceSummaries == nil {
		plan.EvidenceSummaries = []PRDEvidenceCardSummary{}
	}
	if plan.Findings == nil {
		plan.Findings = []ProfileFinding{}
	}
}

func normalizeADRDraftPromptPlan(plan *ADRDraftPromptPlan) {
	if plan.EvidenceCardIDs == nil {
		plan.EvidenceCardIDs = []string{}
	}
	if plan.PRDRequirementIDs == nil {
		plan.PRDRequirementIDs = []string{}
	}
	if plan.EvidenceSummaries == nil {
		plan.EvidenceSummaries = []ADREvidenceCardSummary{}
	}
	if plan.PRDRequirementSummary == nil {
		plan.PRDRequirementSummary = []ADRPRDRequirementSummary{}
	}
	if plan.Findings == nil {
		plan.Findings = []ProfileFinding{}
	}
}
