package productworkflow

import (
	"errors"
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
	CheckedFiles     []string
	Findings         []ObsidianParityFinding
	completionInputs []workflowCompletionExternalInput
}

func EvaluateObsidianParity(root string, ws *WorkflowState, ledger []ImplementationLedgerRecord, investigations []InvestigationReport) ObsidianParityResult {
	result := ObsidianParityResult{
		CheckedFiles: []string{},
		Findings:     []ObsidianParityFinding{},
	}
	if ws == nil || ws.EffortID != "prd-adr-brief-workflow" {
		return result
	}
	const obsidianDir = "docs/obsidian"
	directoryValue := obsidianDirectoryCompletionValue(root)
	result.completionInputs = append(result.completionInputs, workflowCompletionExternalInput{
		key:   "obsidian-directory:" + filepath.Clean(root) + "\x00" + obsidianDir,
		value: directoryValue,
		inspect: func() string {
			return obsidianDirectoryCompletionValue(root)
		},
	})
	if directoryValue == "missing" {
		return result
	}

	result.CheckedFiles = []string{ObsidianProductWorkflowPath, ObsidianRecoveryPath}

	productWorkflowDoc, productWorkflowErr := readObsidian(root, ObsidianProductWorkflowPath)
	result.completionInputs = append(result.completionInputs, obsidianFileCompletionInput(root, ObsidianProductWorkflowPath, productWorkflowDoc, productWorkflowErr))
	if productWorkflowErr != nil {
		result.Findings = append(result.Findings, ObsidianParityFinding{
			Code:    "obsidian_read_error",
			Path:    ObsidianProductWorkflowPath,
			Message: fileReadFindingMessage(productWorkflowErr),
		})
	}

	recoveryDoc, recoveryErr := readObsidian(root, ObsidianRecoveryPath)
	result.completionInputs = append(result.completionInputs, obsidianFileCompletionInput(root, ObsidianRecoveryPath, recoveryDoc, recoveryErr))
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

func obsidianDirectoryCompletionValue(rootPath string) string {
	root, relPath, err := openEffortArtifactRoot(rootPath, "docs/obsidian")
	if err == nil {
		defer root.Close()
		_, err = readEffortArtifactDirectoryRoot(root, relPath)
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	case err != nil:
		return "error"
	default:
		return "directory"
	}
}

func obsidianFileCompletionInput(root, relPath string, body []byte, readErr error) workflowCompletionExternalInput {
	root = filepath.Clean(root)
	relPath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))

	return workflowCompletionExternalInput{
		key:   "obsidian-file:" + root + "\x00" + relPath,
		value: workflowCompletionFileValue(body, readErr),
		inspect: func() string {
			current, err := readObsidian(root, relPath)
			return workflowCompletionFileValue(current, err)
		},
	}
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
	if errors.Is(err, os.ErrNotExist) {
		return "required file not found"
	}

	return "required file could not be read"
}

func readObsidian(root string, relPath string) ([]byte, error) {
	repoRoot, cleanRel, err := openEffortArtifactRoot(root, filepath.FromSlash(relPath))
	if err != nil {
		return nil, err
	}
	defer repoRoot.Close()

	return readEffortArtifactRoot(repoRoot, cleanRel)
}
