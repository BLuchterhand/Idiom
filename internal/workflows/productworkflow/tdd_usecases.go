package productworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TDDObligationPlanningResult struct {
	Status ProductWorkflowStatus
	Plan   TDDObligationSynthesisPlan
}

type BehavioralCoveragePromotionRequest struct {
	EffortRoot   string
	ContractPath string
	Write        bool
}

type BehavioralCoveragePromotionReview struct {
	Status       ProductWorkflowStatus
	ContractPath string
	Promotion    BehavioralCoveragePromotionResult
	Ledger       TDDLedger
	Findings     []TDDObligationPlanFinding
}

type tddEffortDocuments struct {
	WorkflowState WorkflowState
	PRD           PRDDocument
	ADRIndex      ADRIndex
	ADRs          []ParsedADR
	ADRPathByID   map[string]string
	Ledger        TDDLedger
	TDDGeneration tddGenerationToken
	Findings      []TDDObligationPlanFinding
}

type existingTDDLedgerRequirement uint8

const (
	existingTDDLedgerRequired existingTDDLedgerRequirement = iota
	existingTDDLedgerOptional
)

func PlanTDDObligationsFromEffort(effortRoot string) TDDObligationPlanningResult {
	documents := loadTDDEffortDocuments(effortRoot, existingTDDLedgerRequired)
	return planTDDObligationsForDocuments(documents)
}

func planTDDObligationsForDocuments(documents tddEffortDocuments) TDDObligationPlanningResult {
	plan := TDDObligationSynthesisPlan{
		Obligations: []TDDObligationPlanItem{},
		Findings:    []TDDObligationPlanFinding{},
	}
	status := ProductWorkflowStatusBlocked

	if len(documents.Findings) == 0 {
		plan = PlanTDDObligations(documents.PRD, documents.ADRs, documents.Ledger, documents.ADRPathByID)
		if !TDDObligationPlanHasBlockers(plan) {
			status = ProductWorkflowStatusPass
		}
	} else {
		plan.Findings = append(plan.Findings, documents.Findings...)
	}

	if plan.Obligations == nil {
		plan.Obligations = []TDDObligationPlanItem{}
	}
	if plan.Findings == nil {
		plan.Findings = []TDDObligationPlanFinding{}
	}

	return TDDObligationPlanningResult{Status: status, Plan: plan}
}

func PromoteBehavioralCoverageFromEffort(request BehavioralCoveragePromotionRequest) BehavioralCoveragePromotionReview {
	documents := loadTDDEffortDocuments(request.EffortRoot, existingTDDLedgerRequired)
	findings := append([]TDDObligationPlanFinding(nil), documents.Findings...)
	contractPath, pathOK := cleanEffortRelativePath(request.ContractPath)
	if !pathOK {
		findings = append(findings, tddUseCaseFinding(
			"invalid_promote_coverage_path",
			"behavioral coverage contract path must be effort-relative and must not contain path traversal",
			"contract_path",
		))
	}

	var contract BehavioralCoverageContract
	if pathOK {
		data, ok := readTDDUseCaseFile(request.EffortRoot, contractPath, &findings)
		if ok {
			if err := json.Unmarshal(data, &contract); err != nil {
				findings = append(findings, tddUseCaseFinding("parse_error", err.Error(), filepath.ToSlash(contractPath)))
			}
		}
	}

	status := ProductWorkflowStatusBlocked
	promoted := documents.Ledger
	promotion := BehavioralCoveragePromotionResult{State: BehavioralCoveragePromotionStateProposed}
	if len(findings) == 0 {
		var promotionErr error
		if request.Write {
			promotion, promotionErr = commitBehavioralCoveragePromotion(request.EffortRoot, documents.TDDGeneration, contract)
		} else {
			promotion, promotionErr = reviewBehavioralCoverageContractWithResult(documents.WorkflowState, documents.Ledger, contract)
		}
		if promotionErr != nil {
			var commitErr BehavioralCoveragePromotionCommitError
			if errors.As(promotionErr, &commitErr) {
				file := strings.TrimSpace(commitErr.File)
				if file == "" {
					file = "tdd.json"
				}
				findings = append(findings, tddUseCaseFinding("write_error", promotionErr.Error(), filepath.ToSlash(file)))
			} else {
				promotion.State = BehavioralCoveragePromotionStateRejected
				promotion.CommitStatus = BehavioralCoveragePromotionCommitStatusBlocked
				findings = append(findings, tddUseCaseFinding("coverage_contract_invalid", promotionErr.Error(), filepath.ToSlash(contractPath)))
			}
		} else {
			promoted = promotion.Ledger
			status = ProductWorkflowStatusPass
		}
		if len(findings) > 0 {
			status = ProductWorkflowStatusBlocked
			if promotion.LedgerCommitted {
				promoted = promotion.Ledger
			} else {
				promoted = documents.Ledger
			}
		}
	}

	if promoted.Obligations == nil {
		promoted.Obligations = []TDDObligation{}
	}
	if strings.TrimSpace(promotion.AuditStatus) == "" {
		promotion.AuditStatus = BehavioralCoveragePromotionAuditStatusNotAttempted
	}
	if findings == nil {
		findings = []TDDObligationPlanFinding{}
	}

	return BehavioralCoveragePromotionReview{
		Status:       status,
		ContractPath: filepath.ToSlash(contractPath),
		Promotion:    promotion,
		Ledger:       promoted,
		Findings:     findings,
	}
}

func loadTDDEffortDocuments(effortRoot string, ledgerRequirement existingTDDLedgerRequirement) tddEffortDocuments {
	documents := tddEffortDocuments{ADRPathByID: map[string]string{}}
	batch, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		documents.Findings = append(documents.Findings, tddUseCaseFinding(
			"adr_batch_recovery_error",
			err.Error(),
			filepath.Join("adrs", "index.json"),
		))

		return documents
	}
	defer batch.Close()

	generation, tddBody, workflowState, err := inspectTDDGeneration(batch.root)
	if err != nil {
		documents.Findings = append(documents.Findings, tddUseCaseFinding(
			"tdd_generation_error",
			err.Error(),
			"tdd.json",
		))
		return documents
	}
	documents.TDDGeneration = generation
	documents.WorkflowState = workflowState

	if data, ok := readTDDUseCaseFileRoot(batch.root, "prd.md", &documents.Findings); ok {
		doc, err := ParsePRDMarkdown(data)
		if err != nil {
			documents.Findings = append(documents.Findings, tddUseCaseFinding("parse_error", err.Error(), "prd.md"))
		} else {
			documents.PRD = doc
		}
	}

	adrIndexPath := filepath.Join("adrs", "index.json")
	if data, ok := readTDDUseCaseFileRoot(batch.root, adrIndexPath, &documents.Findings); ok {
		index, err := ParseADRIndexJSON(data)
		if err != nil {
			documents.Findings = append(documents.Findings, tddUseCaseFinding("parse_error", err.Error(), adrIndexPath))
		} else {
			documents.ADRIndex = index
		}
	}

	documents.ADRs = make([]ParsedADR, 0, len(documents.ADRIndex.ADRs))
	for _, entry := range documents.ADRIndex.ADRs {
		entryPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			documents.Findings = append(documents.Findings, tddUseCaseFinding(
				"invalid_adr_path",
				"ADR index path must be effort-relative and must not contain path traversal",
				filepath.Join("adrs", entry.Path),
			))
			continue
		}

		relPath := filepath.Join("adrs", entryPath)
		documents.ADRPathByID[entry.ADRID] = relPath
		data, ok := readTDDUseCaseFileRoot(batch.root, relPath, &documents.Findings)
		if !ok {
			continue
		}

		doc, err := ParseADRMarkdown(data)
		if err != nil {
			documents.Findings = append(documents.Findings, tddUseCaseFinding("parse_error", err.Error(), relPath))
			continue
		}

		documents.ADRs = append(documents.ADRs, ParsedADR{Doc: doc, RelPath: relPath, RawMarkdown: string(data)})
	}

	if generation.generation.state == workflowArtifactGenerationAbsent {
		if ledgerRequirement == existingTDDLedgerRequired {
			documents.Findings = append(documents.Findings, tddUseCaseFinding("missing_file", "required file not found", "tdd.json"))
		}
	} else {
		ledger, err := ParseTDDLedgerJSON(tddBody)
		if err != nil {
			documents.Findings = append(documents.Findings, tddUseCaseFinding("parse_error", err.Error(), "tdd.json"))
		} else {
			documents.Ledger = ledger
		}
	}
	documents.Findings = append(documents.Findings, tddEffortIdentityFindings(documents.WorkflowState, documents)...)

	return documents
}

func readTDDUseCaseFile(effortRoot string, relPath string, findings *[]TDDObligationPlanFinding) ([]byte, bool) {
	data, err := readEffortArtifact(effortRoot, relPath)
	return classifyTDDUseCaseFileRead(data, err, relPath, findings)
}

func readTDDUseCaseFileRoot(root *os.Root, relPath string, findings *[]TDDObligationPlanFinding) ([]byte, bool) {
	data, err := readEffortArtifactRoot(root, relPath)
	return classifyTDDUseCaseFileRead(data, err, relPath, findings)
}

func classifyTDDUseCaseFileRead(
	data []byte,
	err error,
	relPath string,
	findings *[]TDDObligationPlanFinding,
) ([]byte, bool) {
	if err == nil {
		return data, true
	}

	kind := "read_error"
	message := err.Error()
	if errors.Is(err, os.ErrNotExist) {
		kind = "missing_file"
		message = "required file not found"
	} else if strings.Contains(strings.ToLower(message), "symlink") {
		kind = "unsafe_symlink"
	} else if strings.Contains(message, "must stay under") || strings.Contains(message, "path traversal") {
		kind = "invalid_path"
	}
	*findings = append(*findings, tddUseCaseFinding(kind, message, relPath))

	return nil, false
}

func tddUseCaseFinding(kind string, message string, file string) TDDObligationPlanFinding {
	return TDDObligationPlanFinding{
		Kind:     kind,
		Message:  message,
		File:     file,
		Severity: ProductWorkflowSeverityError,
	}
}

func tddEffortIdentityFindings(state WorkflowState, documents tddEffortDocuments) []TDDObligationPlanFinding {
	var findings []TDDObligationPlanFinding
	addMismatch := func(field string, artifactValue string, stateValue string, file string) {
		artifactValue = strings.TrimSpace(artifactValue)
		stateValue = strings.TrimSpace(stateValue)
		if artifactValue == "" || stateValue == "" || artifactValue == stateValue {
			return
		}

		findings = append(findings, tddUseCaseFinding(
			field+"_mismatch",
			fmt.Sprintf("%s %q does not match workflow_state.json %s %q", field, artifactValue, field, stateValue),
			file,
		))
	}

	addMismatch("effort_id", documents.ADRIndex.EffortID, state.EffortID, "adrs/index.json")
	addMismatch("task_id", documents.PRD.FrontMatter.TaskID, state.TaskID, "prd.md")
	if strings.TrimSpace(documents.Ledger.SchemaVersion) != "" {
		addMismatch("effort_id", documents.Ledger.EffortID, state.EffortID, "tdd.json")
		addMismatch("task_id", documents.Ledger.TaskID, state.TaskID, "tdd.json")
	}

	return findings
}
