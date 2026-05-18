package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
)

type StatusBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type StatusSection struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	FindingCount int    `json:"finding_count"`
}

type ProductStatusView struct {
	AuditStatus      string                  `json:"audit_status"`
	EffortRoot       string                  `json:"effort_root"`
	EffortID         string                  `json:"effort_id"`
	TaskID           string                  `json:"task_id"`
	StatusBucket     string                  `json:"status_bucket"`
	CurrentPhase     string                  `json:"current_phase"`
	CurrentState     string                  `json:"current_state"`
	StateRevision    int                     `json:"state_revision"`
	LastEventID      string                  `json:"last_event_id"`
	ContextPackID    string                  `json:"context_pack_id"`
	FreshnessState   string                  `json:"freshness_state"`
	CheckedFileCount int                     `json:"checked_file_count"`
	Sections         []StatusSection         `json:"sections"`
	Blockers         []StatusBlocker         `json:"blockers"`
	OpenObligations  []string                `json:"open_obligations"`
	NextValidActions []string                `json:"next_valid_actions"`
	Findings         []StatusBlocker         `json:"findings,omitempty"`
	Metrics          *ProductWorkflowMetrics `json:"metrics,omitempty"`
}

func LoadProductStatus(absRoot string, lintResult *LintSummaryResult) (ProductStatusView, error) {
	wsPath := filepath.Join(absRoot, "workflow_state.json")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		blocker := StatusBlocker{
			Code:    "missing_workflow_state",
			Message: fmt.Sprintf("workflow_state.json not found: %v", err),
		}
		view := ProductStatusView{
			AuditStatus:      "error",
			EffortRoot:       absRoot,
			Blockers:         []StatusBlocker{blocker},
			Findings:         []StatusBlocker{blocker},
			OpenObligations:  []string{},
			NextValidActions: []string{},
			Sections:         []StatusSection{},
		}
		return view, fmt.Errorf("workflow_state.json: %w", err)
	}

	ws, parseErr := ParseWorkflowStateJSON(data)
	if parseErr != nil {
		blocker := StatusBlocker{
			Code:    "invalid_workflow_state",
			Message: fmt.Sprintf("workflow_state.json invalid: %v", parseErr),
		}
		view := ProductStatusView{
			AuditStatus:      "error",
			EffortRoot:       absRoot,
			Blockers:         []StatusBlocker{blocker},
			Findings:         []StatusBlocker{blocker},
			OpenObligations:  []string{},
			NextValidActions: []string{},
			Sections:         []StatusSection{},
		}
		return view, fmt.Errorf("workflow_state.json: %w", parseErr)
	}

	view := ProductStatusView{
		AuditStatus:      "pass",
		EffortRoot:       absRoot,
		EffortID:         ws.EffortID,
		TaskID:           ws.TaskID,
		StatusBucket:     ws.StatusBucket,
		CurrentPhase:     ws.CurrentPhase,
		CurrentState:     ws.CurrentState,
		StateRevision:    ws.StateRevision,
		LastEventID:      ws.LastEventID,
		ContextPackID:    ws.Context.ContextPackID,
		FreshnessState:   ws.Context.FreshnessState,
		OpenObligations:  ws.OpenObligations,
		NextValidActions: ws.NextValidActions,
		Blockers:         []StatusBlocker{},
		Findings:         []StatusBlocker{},
		Sections:         []StatusSection{},
	}

	if len(ws.Blockers) > 0 {
		for _, b := range ws.Blockers {
			view.Blockers = append(view.Blockers, StatusBlocker{
				Code:    "workflow_blocker",
				Message: b,
			})
		}
	}

	if lintResult != nil {
		view.CheckedFileCount = len(lintResult.CheckedFiles)
		view.AuditStatus = lintResult.Status

		sectionMap := map[string]*StatusSection{
			"prd":          {Name: "prd", Status: "pass"},
			"adr":          {Name: "adr", Status: "pass"},
			"tdd":          {Name: "tdd", Status: "pass"},
			"traceability": {Name: "traceability", Status: "pass"},
			"reports":      {Name: "reports", Status: "pass"},
			"obsidian":     {Name: "obsidian", Status: "pass"},
			"metrics":      {Name: "metrics", Status: "pass"},
		}
		for _, f := range lintResult.Findings {
			sec := ClassifySection(f.Path)
			if sec == "" {
				continue
			}
			if s, ok := sectionMap[sec]; ok {
				s.Status = "fail"
				s.FindingCount++
			}
		}
		for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "metrics"} {
			view.Sections = append(view.Sections, *sectionMap[name])
		}

		for _, f := range lintResult.Findings {
			view.Findings = append(view.Findings, StatusBlocker{
				Code:    f.Code,
				Message: fmt.Sprintf("%s: %s", f.Path, f.Message),
			})
		}
	}

	if view.AuditStatus == "" {
		view.AuditStatus = "pass"
	}

	return view, nil
}

func ClassifySection(relPath string) string {
	switch {
	case relPath == "prd.md":
		return "prd"
	case relPath == "adrs/index.json" || (len(relPath) > 4 && relPath[:5] == "adrs/"):
		return "adr"
	case relPath == "tdd.json":
		return "tdd"
	case relPath == "evidence/traceability_matrix.json":
		return "traceability"
	case len(relPath) > 8 && relPath[:8] == "reports/":
		return "reports"
	case relPath == ObsidianProposalPath || relPath == ObsidianRoadmapPath:
		return "obsidian"
	default:
		return ""
	}
}

type LintSummaryFinding struct {
	Code    string
	Path    string
	Message string
}

type LintSummaryResult struct {
	Status       string
	CheckedFiles []string
	Findings     []LintSummaryFinding
}
