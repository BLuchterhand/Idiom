package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ObsidianProductWorkflowPath = "docs/obsidian/20 - Product Workflow and Behavioral Coverage.md"
	ObsidianRecoveryPath        = "docs/obsidian/21 - Recovery and Decomposition.md"
)

type ObsidianParityFinding struct {
	Code    string
	Path    string
	Message string
}

type ObsidianParityResult struct {
	CheckedFiles []string
	Findings     []ObsidianParityFinding
}

func EvaluateObsidianParity(root string, ws *WorkflowState, ledger []ImplementationLedgerRecord, investigations []InvestigationReport) ObsidianParityResult {
	result := ObsidianParityResult{
		CheckedFiles: []string{},
		Findings:     []ObsidianParityFinding{},
	}
	if ws == nil || ws.EffortID != "prd-adr-brief-workflow" {
		return result
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "obsidian")); os.IsNotExist(err) {
		return result
	}

	result.CheckedFiles = []string{ObsidianProductWorkflowPath, ObsidianRecoveryPath}

	productWorkflowDoc, productWorkflowErr := readObsidian(root, ObsidianProductWorkflowPath)
	if productWorkflowErr != nil {
		result.Findings = append(result.Findings, ObsidianParityFinding{
			Code:    "obsidian_read_error",
			Path:    ObsidianProductWorkflowPath,
			Message: fileReadFindingMessage(productWorkflowErr),
		})
	}

	_, recoveryErr := readObsidian(root, ObsidianRecoveryPath)
	if recoveryErr != nil {
		result.Findings = append(result.Findings, ObsidianParityFinding{
			Code:    "obsidian_read_error",
			Path:    ObsidianRecoveryPath,
			Message: fileReadFindingMessage(recoveryErr),
		})
	}

	if productWorkflowErr != nil || recoveryErr != nil {
		return result
	}

	productWorkflowText := string(productWorkflowDoc)

	var stale []ObsidianParityFinding

	if strings.Contains(strings.ToUpper(productWorkflowText), "STATUS: NOT STARTED") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_stale_status",
			Path:    ObsidianProductWorkflowPath,
			Message: "product workflow note status claims NOT STARTED while durable workflow is active",
		})
	}

	if strings.Contains(productWorkflowText, "## NOT STARTED: PRD/ADR/Brief Workflow") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_stale_status",
			Path:    ObsidianProductWorkflowPath,
			Message: "product workflow note marks PRD/ADR/Brief workflow as NOT STARTED while durable workflow is active",
		})
	}

	if ws.CurrentPhase == "phase-5-execution-feedback" {
		for _, phase := range []string{"Phase 0", "Phase 1", "Phase 2", "Phase 3", "Phase 4"} {
			if hasUncheckedPhase(productWorkflowText, phase) {
				stale = append(stale, ObsidianParityFinding{
					Code:    "obsidian_phase_checklist_drift",
					Path:    ObsidianProductWorkflowPath,
					Message: fmt.Sprintf("product workflow checklist shows %s incomplete while durable workflow is in %s", phase, ws.CurrentPhase),
				})
			}
		}
	}

	if obsidianParityAtomCompleted(ledger) && strings.Contains(productWorkflowText, "- [ ] Obsidian parity check in product lint/audit") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_roadmap_checklist_drift",
			Path:    ObsidianProductWorkflowPath,
			Message: "product workflow note still lists Obsidian parity check as TODO after atom completion",
		})
	}

	if len(stale) == 0 {
		return result
	}

	if HasObsidianNoChangeRationale(ws.TaskID, investigations) {
		return result
	}

	result.Findings = append(result.Findings, stale...)

	return result
}

func hasUncheckedPhase(doc, phase string) bool {
	return strings.Contains(doc, "- [ ] **"+phase+"**")
}

func HasObsidianNoChangeRationale(taskID string, investigations []InvestigationReport) bool {
	for _, report := range investigations {
		if report.TaskID != taskID {
			continue
		}

		if strings.TrimSpace(report.Scope.ObsidianNoChangeRationale) != "" {
			return true
		}
	}

	return false
}

func obsidianParityAtomCompleted(ledger []ImplementationLedgerRecord) bool {
	for _, rec := range ledger {
		if !strings.Contains(rec.TaskID, "obsidian-parity-check") {
			continue
		}

		if rec.RecordType == LedgerRecordAtomClosed && len(rec.OpenBlockers) == 0 {
			return true
		}
	}

	return false
}

func fileReadFindingMessage(err error) string {
	if os.IsNotExist(err) {
		return "required file not found"
	}

	return "required file could not be read"
}

func readObsidian(root string, relPath string) ([]byte, error) {
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if info, err := os.Lstat(fullPath); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("required file must not be a symlink")
	}

	return os.ReadFile(fullPath)
}
