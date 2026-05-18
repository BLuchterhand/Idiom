package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

type ProductOperatorStatusView struct {
	SchemaVersion         string                    `json:"schema_version"`
	ContinuationAuthority string                    `json:"continuation_authority"`
	AuditStatus           string                    `json:"audit_status"`
	EffortID              string                    `json:"effort_id"`
	TaskID                string                    `json:"task_id"`
	StatusBucket          string                    `json:"status_bucket"`
	CurrentPhase          string                    `json:"current_phase"`
	CurrentState          string                    `json:"current_state"`
	StateRevision         int                       `json:"state_revision"`
	LastEventID           string                    `json:"last_event_id"`
	ContextPackID         string                    `json:"context_pack_id"`
	FreshnessState        string                    `json:"freshness_state"`
	CheckedFileCount      int                       `json:"checked_file_count"`
	Sections              []StatusSection           `json:"sections"`
	Metrics               *ProductWorkflowMetrics   `json:"metrics,omitempty"`
	Blockers              []ProductOperatorBlocker  `json:"blockers"`
	OpenObligations       []string                  `json:"open_obligations"`
	ArtifactPaths         []ProductOperatorArtifact `json:"artifact_paths"`
	NextValidActions      []ProductOperatorAction   `json:"next_valid_actions"`
}

type ProductOperatorAction struct {
	Action       string                   `json:"action"`
	ID           string                   `json:"id"`
	Label        string                   `json:"label"`
	Owner        string                   `json:"owner"`
	Dispatchable bool                     `json:"dispatchable"`
	RunnableNow  bool                     `json:"runnable_now"`
	Message      string                   `json:"message,omitempty"`
	BlockedBy    []string                 `json:"blocked_by,omitempty"`
	Blockers     []ProductOperatorBlocker `json:"blockers,omitempty"`
	CommandHint  string                   `json:"command_hint,omitempty"`
}

type ProductOperatorBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

type ProductOperatorArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func LoadProductStatus(absRoot string, lintResult *LintSummaryResult) (ProductStatusView, error) {
	wsPath := filepath.Join(absRoot, "workflow_state.json")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		blocker := StatusBlocker{
			Code:    "missing_workflow_state",
			Message: "workflow_state.json not found",
		}
		view := ProductStatusView{
			AuditStatus:      "error",
			EffortRoot:       absRoot,
			Blockers:         []StatusBlocker{blocker},
			Findings:         []StatusBlocker{blocker},
			OpenObligations:  []string{},
			NextValidActions: []string{"restore workflow_state.json under the effort root and rerun product status"},
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
			NextValidActions: []string{"fix workflow_state.json content and rerun product status"},
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

func BuildProductOperatorStatusView(status ProductStatusView, workflowState *WorkflowState) ProductOperatorStatusView {
	view := ProductOperatorStatusView{
		SchemaVersion:         "idiom.product_operator_status.v1",
		ContinuationAuthority: "workflow_state",
		AuditStatus:           status.AuditStatus,
		EffortID:              status.EffortID,
		TaskID:                status.TaskID,
		StatusBucket:          status.StatusBucket,
		CurrentPhase:          status.CurrentPhase,
		CurrentState:          status.CurrentState,
		StateRevision:         status.StateRevision,
		LastEventID:           status.LastEventID,
		ContextPackID:         status.ContextPackID,
		FreshnessState:        status.FreshnessState,
		CheckedFileCount:      status.CheckedFileCount,
		Sections:              append([]StatusSection{}, status.Sections...),
		Metrics:               status.Metrics,
		OpenObligations:       append([]string{}, status.OpenObligations...),
		Blockers:              []ProductOperatorBlocker{},
		ArtifactPaths:         []ProductOperatorArtifact{},
		NextValidActions:      []ProductOperatorAction{},
	}

	if workflowState != nil {
		view.ArtifactPaths = productOperatorArtifacts(workflowState.ArtifactPaths)
	}

	for _, blocker := range status.Blockers {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    blocker.Code,
			Message: blocker.Message,
			Source:  "workflow_status",
		})
	}
	for _, finding := range status.Findings {
		if hasOperatorBlocker(view.Blockers, finding.Code, finding.Message) {
			continue
		}
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    finding.Code,
			Message: finding.Message,
			Source:  "audit",
		})
	}

	if strings.TrimSpace(status.ContextPackID) == "" {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "missing_context_pack",
			Message: "attach a context_pack_id before continuing",
			Source:  "workflow_state.context",
		})
	}

	if strings.TrimSpace(status.EffortID) == "" || strings.TrimSpace(status.CurrentState) == "" || strings.TrimSpace(status.StatusBucket) == "" {
		if !hasOperatorBlockerCode(view.Blockers, "invalid_workflow_state") && !hasOperatorBlockerCode(view.Blockers, "missing_workflow_state") {
			view.Blockers = append(view.Blockers, ProductOperatorBlocker{
				Code:    "invalid_workflow_state",
				Message: "workflow_state.json is not valid enough to continue",
				Source:  "workflow_state",
			})
		}
	}

	if !operatorFreshnessCurrent(status.FreshnessState) {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "stale_context",
			Message: fmt.Sprintf("workflow freshness_state %q is not current", strings.TrimSpace(status.FreshnessState)),
			Source:  "workflow_state.context",
		})
	}

	if status.AuditStatus != "" && status.AuditStatus != "pass" {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "audit_not_passing",
			Message: fmt.Sprintf("product status audit_status %q blocks continuation dispatch", status.AuditStatus),
			Source:  "audit",
		})
	}

	if len(status.OpenObligations) > 0 {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "open_obligations",
			Message: fmt.Sprintf("%d open obligations remain", len(status.OpenObligations)),
			Source:  "workflow_state",
		})
	}

	transitionActions, transitionBlockers := NextActionsForState(status.StatusBucket, status.CurrentState)
	for _, blocker := range transitionBlockers {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    blocker.Code,
			Message: blocker.Message,
			Source:  "workflow_transition",
		})
	}

	blockerCodes := productOperatorBlockerCodes(view.Blockers)
	blockingReasons := append([]ProductOperatorBlocker{}, view.Blockers...)
	dispatchable := len(blockerCodes) == 0
	transitionOwners := map[string]string{}
	for _, action := range transitionActions {
		transitionOwners[action.Action] = action.Owner
	}

	for idx, actionText := range status.NextValidActions {
		actionText = strings.TrimSpace(actionText)
		if actionText == "" {
			continue
		}
		id := productOperatorFallbackActionID(actionText)
		if id == "" {
			id = fmt.Sprintf("operator_followup_%d", idx+1)
		}
		owner := transitionOwners[actionText]
		if owner == "" {
			owner = productOperatorActionOwner(actionText)
		}
		runnableNow := productOperatorActionRunnable(actionText, dispatchable)
		view.NextValidActions = append(view.NextValidActions, ProductOperatorAction{
			Action:       actionText,
			ID:           id,
			Label:        productOperatorActionLabel(actionText),
			Owner:        owner,
			Dispatchable: dispatchable,
			RunnableNow:  runnableNow,
			Message:      productOperatorActionMessage(actionText, dispatchable, blockerCodes),
			BlockedBy:    append([]string{}, blockerCodes...),
			Blockers:     append([]ProductOperatorBlocker{}, blockingReasons...),
			CommandHint:  productOperatorCommandHint(actionText),
		})
	}

	if len(view.NextValidActions) == 0 {
		for _, action := range transitionActions {
			view.NextValidActions = append(view.NextValidActions, ProductOperatorAction{
				Action:       action.Action,
				ID:           action.Action,
				Label:        productOperatorActionLabel(action.Action),
				Owner:        action.Owner,
				Dispatchable: dispatchable,
				RunnableNow:  dispatchable,
				Message:      productOperatorActionMessage(action.Action, dispatchable, blockerCodes),
				BlockedBy:    append([]string{}, blockerCodes...),
				Blockers:     append([]ProductOperatorBlocker{}, blockingReasons...),
			})
		}
	}

	if len(view.NextValidActions) == 0 && len(blockerCodes) > 0 {
		view.NextValidActions = append(view.NextValidActions, ProductOperatorAction{
			Action:       "inspect_operator_blockers",
			ID:           "inspect_operator_blockers",
			Label:        "inspect blockers and refresh durable workflow state",
			Owner:        "operator",
			Dispatchable: false,
			RunnableNow:  true,
			Message:      "resolve blockers, then rerun product status",
			BlockedBy:    append([]string{}, blockerCodes...),
			Blockers:     append([]ProductOperatorBlocker{}, blockingReasons...),
		})
	}

	return view
}

func productOperatorArtifacts(paths map[string]string) []ProductOperatorArtifact {
	if len(paths) == 0 {
		return []ProductOperatorArtifact{}
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	artifacts := make([]ProductOperatorArtifact, 0, len(names))
	for _, name := range names {
		artifacts = append(artifacts, ProductOperatorArtifact{Name: name, Path: paths[name]})
	}
	return artifacts
}

func hasOperatorBlocker(blockers []ProductOperatorBlocker, code, message string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code && blocker.Message == message {
			return true
		}
	}
	return false
}

func hasOperatorBlockerCode(blockers []ProductOperatorBlocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func productOperatorBlockerCodes(blockers []ProductOperatorBlocker) []string {
	if len(blockers) == 0 {
		return nil
	}
	seen := map[string]bool{}
	codes := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if blocker.Code == "" || seen[blocker.Code] {
			continue
		}
		seen[blocker.Code] = true
		codes = append(codes, blocker.Code)
	}
	sort.Strings(codes)
	return codes
}

func productOperatorActionMessage(action string, dispatchable bool, blockerCodes []string) string {
	if dispatchable {
		return "valid workflow continuation action: " + action
	}
	if len(blockerCodes) == 0 {
		return "inspect workflow_state next_valid_actions before dispatch"
	}
	return "blocked by " + strings.Join(blockerCodes, ", ")
}

func operatorFreshnessCurrent(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "current", "fresh", "verified":
		return true
	default:
		return false
	}
}

func productOperatorActionLabel(action string) string {
	action = strings.TrimSpace(action)
	if strings.Contains(action, "`") {
		return action
	}
	return strings.ReplaceAll(action, "_", " ")
}

func productOperatorActionOwner(action string) string {
	normalized := strings.ToLower(strings.TrimSpace(action))
	switch {
	case strings.Contains(normalized, "restore") || strings.Contains(normalized, "fix") || strings.Contains(normalized, "repair"):
		return "operator"
	case strings.Contains(normalized, "review"):
		return "reviewer"
	case strings.Contains(normalized, "verify"):
		return "verifier"
	case strings.Contains(normalized, "implement") || strings.Contains(normalized, "worker"):
		return "implementor"
	case strings.Contains(normalized, "investigat"):
		return "investigator"
	case strings.Contains(normalized, "audit") || strings.Contains(normalized, "lint") || strings.Contains(normalized, "status"):
		return "product_workflow"
	default:
		return "operator"
	}
}

func productOperatorActionRunnable(action string, dispatchable bool) bool {
	if dispatchable {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(action))
	return strings.Contains(normalized, "restore") ||
		strings.Contains(normalized, "fix") ||
		strings.Contains(normalized, "repair") ||
		strings.Contains(normalized, "rerun")
}

func productOperatorFallbackActionID(label string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func productOperatorCommandHint(label string) string {
	start := strings.Index(label, "`")
	if start == -1 {
		return ""
	}
	rest := label[start+1:]
	end := strings.Index(rest, "`")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
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
