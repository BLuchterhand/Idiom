package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ObsidianProposalPath = "docs/obsidian/20 - PRD ADR Brief Research Workflow PROPOSAL.md"
	ObsidianRoadmapPath  = "docs/obsidian/22 - Product Workflow ROADMAP.md"
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
	result.CheckedFiles = []string{ObsidianProposalPath, ObsidianRoadmapPath}

	proposal, proposalErr := readObsidian(root, ObsidianProposalPath)
	if proposalErr != nil {
		result.Findings = append(result.Findings, ObsidianParityFinding{
			Code:    "obsidian_read_error",
			Path:    ObsidianProposalPath,
			Message: fileReadFindingMessage(proposalErr),
		})
	}
	roadmap, roadmapErr := readObsidian(root, ObsidianRoadmapPath)
	if roadmapErr != nil {
		result.Findings = append(result.Findings, ObsidianParityFinding{
			Code:    "obsidian_read_error",
			Path:    ObsidianRoadmapPath,
			Message: fileReadFindingMessage(roadmapErr),
		})
	}
	if proposalErr != nil || roadmapErr != nil {
		return result
	}

	proposalText := string(proposal)
	roadmapText := string(roadmap)
	var stale []ObsidianParityFinding
	if strings.Contains(strings.ToUpper(proposalText), "STATUS: NOT STARTED") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_stale_status",
			Path:    ObsidianProposalPath,
			Message: "proposal status claims NOT STARTED while durable workflow is active",
		})
	}
	if strings.Contains(roadmapText, "## NOT STARTED: PRD/ADR/Brief Workflow") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_stale_status",
			Path:    ObsidianRoadmapPath,
			Message: "roadmap marks PRD/ADR/Brief workflow as NOT STARTED while durable workflow is active",
		})
	}
	if ws.CurrentPhase == "phase-5-execution-feedback" {
		for _, phase := range []string{"Phase 0", "Phase 1", "Phase 2", "Phase 3", "Phase 4"} {
			if hasUncheckedPhase(proposalText, phase) {
				stale = append(stale, ObsidianParityFinding{
					Code:    "obsidian_phase_checklist_drift",
					Path:    ObsidianProposalPath,
					Message: fmt.Sprintf("proposal checklist shows %s incomplete while durable workflow is in %s", phase, ws.CurrentPhase),
				})
			}
		}
	}
	if obsidianParityAtomCompleted(ledger) && strings.Contains(roadmapText, "- [ ] Obsidian parity check in product lint/audit") {
		stale = append(stale, ObsidianParityFinding{
			Code:    "obsidian_roadmap_checklist_drift",
			Path:    ObsidianRoadmapPath,
			Message: "roadmap still lists Obsidian parity check as TODO after atom completion",
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
		raw, ok := report.Scope["obsidian_no_change_rationale"]
		if !ok {
			continue
		}
		reason, ok := raw.(string)
		if ok && strings.TrimSpace(reason) != "" {
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
		if rec.RecordType == "atom_closed" && len(rec.OpenBlockers) == 0 {
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
	return os.ReadFile(filepath.Join(root, relPath))
}
