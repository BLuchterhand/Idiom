package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/recoverstatus"
)

type StatusBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type StatusSection struct {
	Name         string                `json:"name"`
	Status       ProductWorkflowStatus `json:"status"`
	FindingCount int                   `json:"finding_count"`
}

type ProductStatusView struct {
	AuditStatus           ProductWorkflowStatus       `json:"audit_status"`
	EffortRoot            string                      `json:"effort_root"`
	EffortID              string                      `json:"effort_id"`
	TaskID                string                      `json:"task_id"`
	StatusBucket          string                      `json:"status_bucket"`
	CurrentPhase          string                      `json:"current_phase"`
	CurrentState          string                      `json:"current_state"`
	WorkflowID            workflowstate.WorkflowID    `json:"workflow_id,omitempty"`
	WorkflowState         workflowstate.StateID       `json:"workflow_state,omitempty"`
	WorkflowTerminalState workflowstate.TerminalState `json:"workflow_terminal_state,omitempty"`
	Dispatchable          bool                        `json:"dispatchable"`
	StateRevision         int                         `json:"state_revision"`
	LastEventID           string                      `json:"last_event_id"`
	ContextPackID         string                      `json:"context_pack_id"`
	FreshnessState        string                      `json:"freshness_state"`
	CheckedFileCount      int                         `json:"checked_file_count"`
	Sections              []StatusSection             `json:"sections"`
	Blockers              []StatusBlocker             `json:"blockers"`
	OpenObligations       []string                    `json:"open_obligations"`
	NextValidActions      []string                    `json:"next_valid_actions"`
	Findings              []StatusBlocker             `json:"findings,omitempty"`
	Metrics               *ProductWorkflowMetrics     `json:"metrics,omitempty"`
	Recovery              *ProductRecoveryStatus      `json:"recovery,omitempty"`
}

type ProductOperatorStatusView struct {
	SchemaVersion         string                      `json:"schema_version"`
	ContinuationAuthority string                      `json:"continuation_authority"`
	AuditStatus           ProductWorkflowStatus       `json:"audit_status"`
	EffortID              string                      `json:"effort_id"`
	TaskID                string                      `json:"task_id"`
	StatusBucket          string                      `json:"status_bucket"`
	CurrentPhase          string                      `json:"current_phase"`
	CurrentState          string                      `json:"current_state"`
	WorkflowID            workflowstate.WorkflowID    `json:"workflow_id,omitempty"`
	WorkflowState         workflowstate.StateID       `json:"workflow_state,omitempty"`
	WorkflowTerminalState workflowstate.TerminalState `json:"workflow_terminal_state,omitempty"`
	Dispatchable          bool                        `json:"dispatchable"`
	StateRevision         int                         `json:"state_revision"`
	LastEventID           string                      `json:"last_event_id"`
	ContextPackID         string                      `json:"context_pack_id"`
	FreshnessState        string                      `json:"freshness_state"`
	CheckedFileCount      int                         `json:"checked_file_count"`
	Sections              []StatusSection             `json:"sections"`
	Metrics               *ProductWorkflowMetrics     `json:"metrics,omitempty"`
	Blockers              []ProductOperatorBlocker    `json:"blockers"`
	OpenObligations       []string                    `json:"open_obligations"`
	ArtifactPaths         []ProductOperatorArtifact   `json:"artifact_paths"`
	NextValidActions      []ProductOperatorAction     `json:"next_valid_actions"`
	Recovery              *ProductRecoveryStatus      `json:"recovery,omitempty"`
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

type ProductRecoveryStatus struct {
	Status             ProductWorkflowStatus  `json:"status"`
	PacketPath         string                 `json:"packet_path,omitempty"`
	FailureRecordPath  string                 `json:"failure_record_path,omitempty"`
	ReconciliationPath string                 `json:"reconciliation_path,omitempty"`
	FailureID          string                 `json:"failure_id,omitempty"`
	PacketID           string                 `json:"packet_id,omitempty"`
	FailureClass       string                 `json:"failure_class,omitempty"`
	FailureKind        string                 `json:"failure_kind,omitempty"`
	SourceSessionID    string                 `json:"source_session_id,omitempty"`
	NextValidAction    string                 `json:"next_valid_action,omitempty"`
	CandidateAtomCount int                    `json:"candidate_atom_count,omitempty"`
	CompletedAtomCount int                    `json:"completed_atom_count,omitempty"`
	RemainingAtomCount int                    `json:"remaining_atom_count,omitempty"`
	ResidualWorkCount  int                    `json:"residual_work_count,omitempty"`
	Gates              []string               `json:"gates,omitempty"`
	RequiredTests      []string               `json:"required_tests,omitempty"`
	ProductLinks       ProductRecoveryLinks   `json:"product_links,omitempty"`
	Blockers           []ProductRecoveryBlock `json:"blockers,omitempty"`
}

type ProductRecoveryLinks struct {
	PRDPaths    []string `json:"prd_paths,omitempty"`
	ADRPaths    []string `json:"adr_paths,omitempty"`
	TDDPaths    []string `json:"tdd_paths,omitempty"`
	LedgerPaths []string `json:"ledger_paths,omitempty"`
}

type ProductRecoveryBlock struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func LoadProductStatus(absRoot string, lintResult *LintSummaryResult) (ProductStatusView, error) {
	wsPath := filepath.Join(absRoot, WorkflowStateFileName)

	data, err := os.ReadFile(wsPath)
	if err != nil {
		blocker := StatusBlocker{
			Code:    "missing_workflow_state",
			Message: "workflow_state.json not found",
		}
		view := ProductStatusView{
			AuditStatus:           ProductWorkflowStatusError,
			EffortRoot:            absRoot,
			WorkflowID:            workflowstate.WorkflowProductDispatch,
			WorkflowState:         workflowstate.StateTerminal,
			WorkflowTerminalState: workflowstate.TerminalStateMissingScope,
			Blockers:              []StatusBlocker{blocker},
			Findings:              []StatusBlocker{blocker},
			OpenObligations:       []string{},
			NextValidActions:      []string{"restore workflow_state.json under the effort root and rerun product status"},
			Sections:              []StatusSection{},
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
			AuditStatus:           ProductWorkflowStatusError,
			EffortRoot:            absRoot,
			WorkflowID:            workflowstate.WorkflowProductDispatch,
			WorkflowState:         workflowstate.StateTerminal,
			WorkflowTerminalState: workflowstate.TerminalStateInvalidScope,
			Blockers:              []StatusBlocker{blocker},
			Findings:              []StatusBlocker{blocker},
			OpenObligations:       []string{},
			NextValidActions:      []string{"fix workflow_state.json content and rerun product status"},
			Sections:              []StatusSection{},
		}

		return view, fmt.Errorf("workflow_state.json: %w", parseErr)
	}

	view := ProductStatusView{
		AuditStatus:      ProductWorkflowStatusPass,
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
		Recovery:         loadProductRecoveryStatus(absRoot, ws.ArtifactPaths),
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
			"prd":          {Name: "prd", Status: ProductWorkflowStatusPass},
			"adr":          {Name: "adr", Status: ProductWorkflowStatusPass},
			"tdd":          {Name: "tdd", Status: ProductWorkflowStatusPass},
			"traceability": {Name: "traceability", Status: ProductWorkflowStatusPass},
			"reports":      {Name: "reports", Status: ProductWorkflowStatusPass},
			"obsidian":     {Name: "obsidian", Status: ProductWorkflowStatusPass},
			"metrics":      {Name: "metrics", Status: ProductWorkflowStatusPass},
		}

		for _, f := range lintResult.Findings {
			sec := ClassifySection(f.Path)
			if sec == "" {
				continue
			}

			if s, ok := sectionMap[sec]; ok {
				s.Status = ProductWorkflowStatusFail
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
		view.AuditStatus = ProductWorkflowStatusPass
	}
	view.NextValidActions = productStatusNextActionsForAudit(view.AuditStatus, view.NextValidActions)
	view.WorkflowID = workflowstate.WorkflowProductDispatch
	view.WorkflowState, view.WorkflowTerminalState, view.Dispatchable = classifyProductWorkflowProjection(
		view.StatusBucket,
		view.CurrentState,
		view.AuditStatus,
		view.FreshnessState,
		view.OpenObligations,
		view.Blockers,
		view.NextValidActions,
	)
	workflowstate.MustValidateStateProjection(view.WorkflowID, view.WorkflowState, view.WorkflowTerminalState, view.Dispatchable)

	return view, nil
}

func productStatusNextActionsForAudit(auditStatus ProductWorkflowStatus, actions []string) []string {
	if auditStatus == "" || auditStatus == ProductWorkflowStatusPass {
		return actions
	}
	if len(actions) == 0 {
		return []string{ProductStatusAuditRemediationAction}
	}
	if actions[0] == ProductStatusAuditRemediationAction {
		return actions
	}

	next := make([]string, 0, len(actions)+1)
	next = append(next, ProductStatusAuditRemediationAction)
	for _, action := range actions {
		if action == ProductStatusAuditRemediationAction {
			continue
		}
		next = append(next, action)
	}
	return next
}

func classifyProductWorkflowProjection(statusBucket string, currentState string, auditStatus ProductWorkflowStatus, freshnessState string, openObligations []string, blockers []StatusBlocker, nextActions []string) (workflowstate.StateID, workflowstate.TerminalState, bool) {
	statusBucket = strings.TrimSpace(statusBucket)
	currentState = strings.TrimSpace(currentState)

	if statusBucket == WorkflowStatusBucketClosed && currentState == WorkflowStateClosed {
		return workflowstate.StateTerminal, workflowstate.TerminalStateComplete, false
	}
	if statusBucket == WorkflowStatusBucketClosed || currentState == WorkflowStateClosed {
		return workflowstate.StateTerminal, workflowstate.TerminalStateInvalidScope, false
	}

	if len(blockers) == 0 &&
		len(openObligations) == 0 &&
		(auditStatus == "" || auditStatus == ProductWorkflowStatusPass) &&
		operatorFreshnessCurrent(freshnessState) &&
		productWorkflowHasKnownTransitionAction(statusBucket, currentState, nextActions) {

		return workflowstate.StateDispatchable, "", true
	}

	var codes []string
	for _, blocker := range blockers {
		codes = append(codes, blocker.Code)
	}
	if !operatorFreshnessCurrent(freshnessState) {
		codes = append(codes, "stale_context")
	}
	if len(openObligations) > 0 {
		codes = append(codes, "open_obligations")
	}
	if auditStatus != "" && auditStatus != ProductWorkflowStatusPass {
		codes = append(codes, "audit_not_passing")
	}

	return workflowstate.StateTerminal, productWorkflowTerminalStateForBlockers(codes), false
}

func nonEmptyProductStatusActions(actions []string) []string {
	var out []string
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action != "" && !productStatusActionIsOperatorRemediation(action) {
			out = append(out, action)
		}
	}

	return out
}

func productStatusActionIsOperatorRemediation(action string) bool {
	return strings.TrimSpace(action) == ProductStatusAuditRemediationAction
}

func productWorkflowTerminalStateForBlockers(codes []string) workflowstate.TerminalState {
	for _, code := range codes {
		switch strings.TrimSpace(code) {
		case "missing_workflow_state":
			return workflowstate.TerminalStateMissingScope
		case "invalid_workflow_state", "invalid_state", "invalid_state_bucket", "human_verification_required":
			return workflowstate.TerminalStateInvalidScope
		case "stale_context":
			return workflowstate.TerminalStateStaleNeedsRebase
		case "recovery_packet_open":
			return workflowstate.TerminalStateRecoveryOpen
		}
	}

	return workflowstate.TerminalStateBlocked
}

// LoadWorkflowState reads and validates workflow_state.json from an effort root.
func LoadWorkflowState(absRoot string) (WorkflowState, error) {
	data, err := os.ReadFile(filepath.Join(absRoot, WorkflowStateFileName))
	if err != nil {
		return WorkflowState{}, fmt.Errorf("%s: %w", WorkflowStateFileName, err)
	}

	ws, err := ParseWorkflowStateJSON(data)
	if err != nil {
		return WorkflowState{}, fmt.Errorf("%s: %w", WorkflowStateFileName, err)
	}

	return ws, nil
}

// LoadCheckedProductStatus builds the product status view with the same lint and metrics inputs used by operator presenters.
func LoadCheckedProductStatus(absRoot string) (ProductStatusView, *WorkflowState, error) {
	lintSummary := LintEffortFolder(absRoot)

	view, statusErr := LoadProductStatus(absRoot, &lintSummary)
	if statusErr != nil {
		return view, nil, statusErr
	}

	workflowState, workflowStateErr := LoadWorkflowState(absRoot)
	if workflowStateErr != nil {
		return view, nil, workflowStateErr
	}

	metrics, metricsErr := BuildProductWorkflowMetrics(absRoot)
	if metricsErr != nil {
		view.AuditStatus = ProductWorkflowStatusFail
		view.Findings = append(view.Findings, StatusBlocker{
			Code:    "metrics_error",
			Message: metricsErr.Error(),
		})
		view.Blockers = append(view.Blockers, StatusBlocker{
			Code:    "metrics_error",
			Message: metricsErr.Error(),
		})
		markStatusSectionFailure(&view, "metrics")
	} else {
		view.Metrics = &metrics
	}

	return view, &workflowState, nil
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
		WorkflowID:            status.WorkflowID,
		WorkflowState:         status.WorkflowState,
		WorkflowTerminalState: status.WorkflowTerminalState,
		Dispatchable:          status.Dispatchable,
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
		Recovery:              status.Recovery,
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

	if status.AuditStatus != "" && status.AuditStatus != ProductWorkflowStatusPass {
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

	if productOperatorRequestsImplementationDispatch(status.NextValidActions) {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "human_verification_required",
			Message: "implementation dispatch requires an explicit human verification phase; transition planned work through submit_for_human_verification and approve_for_implementation",
			Source:  "workflow_transition",
		})
	}

	if status.Recovery != nil {
		if status.Recovery.Status != ProductWorkflowStatusClosed {
			view.Blockers = append(view.Blockers, ProductOperatorBlocker{
				Code:    "recovery_packet_open",
				Message: "review recovery packet before continuing workflow dispatch",
				Source:  "recovery_packet",
			})
		}

		for _, blocker := range status.Recovery.Blockers {
			view.Blockers = append(view.Blockers, ProductOperatorBlocker{
				Code:    blocker.Code,
				Message: blocker.Message,
				Source:  "recovery_packet",
			})
		}
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
	dispatchable := len(blockerCodes) == 0 && view.WorkflowState != workflowstate.StateTerminal
	view.Dispatchable = dispatchable
	if dispatchable {
		view.WorkflowState = workflowstate.StateDispatchable
		view.WorkflowTerminalState = ""
	} else if view.WorkflowTerminalState == "" {
		view.WorkflowState = workflowstate.StateTerminal
		view.WorkflowTerminalState = productWorkflowTerminalStateForBlockers(blockerCodes)
	}
	transitionOwners := map[string]string{}

	for _, action := range transitionActions {
		transitionOwners[action.Action] = action.Owner
	}

	knownTransitionOwners := productWorkflowKnownTransitionOwners(status.StatusBucket, status.CurrentState)
	hasKnownTransitionAction := false
	for _, actionText := range status.NextValidActions {
		if _, ok := knownTransitionOwners[strings.TrimSpace(actionText)]; ok {
			hasKnownTransitionAction = true
			break
		}
	}
	if view.WorkflowState != workflowstate.StateTerminal && !hasKnownTransitionAction && len(nonEmptyProductStatusActions(status.NextValidActions)) > 0 {
		view.Blockers = append(view.Blockers, ProductOperatorBlocker{
			Code:    "invalid_transition_action",
			Message: fmt.Sprintf("workflow_state.next_valid_actions contains no action allowed by the transition table from state %q", status.CurrentState),
			Source:  "workflow_transition",
		})
		blockerCodes = append(blockerCodes, "invalid_transition_action")
		blockingReasons = append(blockingReasons, view.Blockers[len(view.Blockers)-1])
		dispatchable = false
		view.Dispatchable = false
	}

	for idx, actionText := range status.NextValidActions {
		actionText = strings.TrimSpace(actionText)
		if actionText == "" {
			continue
		}
		if productStatusActionIsOperatorRemediation(actionText) {
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

		actionBlockerCodes := append([]string{}, blockerCodes...)
		actionBlockers := append([]ProductOperatorBlocker{}, blockingReasons...)
		_, transitionActionKnown := transitionOwners[actionText]
		_, rawTransitionActionKnown := knownTransitionOwners[actionText]
		if !rawTransitionActionKnown {
			actionBlockerCodes = append(actionBlockerCodes, "invalid_transition_action")
			actionBlockers = append(actionBlockers, ProductOperatorBlocker{
				Code:    "invalid_transition_action",
				Message: fmt.Sprintf("workflow_state.next_valid_actions contains %q, but the transition table does not allow it from state %q", actionText, status.CurrentState),
				Source:  "workflow_transition",
			})
		}

		actionDispatchable := dispatchable && transitionActionKnown
		runnableNow := productOperatorActionRunnable(actionText, actionDispatchable)

		view.NextValidActions = append(view.NextValidActions, ProductOperatorAction{
			Action:       actionText,
			ID:           id,
			Label:        productOperatorActionLabel(actionText),
			Owner:        owner,
			Dispatchable: actionDispatchable,
			RunnableNow:  runnableNow,
			Message:      productOperatorActionMessage(actionText, actionDispatchable, actionBlockerCodes),
			BlockedBy:    actionBlockerCodes,
			Blockers:     actionBlockers,
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
			Action:       ProductWorkflowActionInspectOperatorBlockers,
			ID:           ProductWorkflowActionInspectOperatorBlockers,
			Label:        "inspect blockers and refresh durable workflow state",
			Owner:        ProductWorkflowActionOwnerOperator,
			Dispatchable: false,
			RunnableNow:  true,
			Message:      "resolve blockers, then rerun product status",
			BlockedBy:    append([]string{}, blockerCodes...),
			Blockers:     append([]ProductOperatorBlocker{}, blockingReasons...),
		})
	}

	if status.Recovery != nil && status.Recovery.Status != ProductWorkflowStatusClosed {
		view.NextValidActions = append(view.NextValidActions, productRecoveryOperatorAction(*status.Recovery, blockerCodes, blockingReasons))
	}

	workflowstate.MustValidateStateProjection(view.WorkflowID, view.WorkflowState, view.WorkflowTerminalState, view.Dispatchable)

	return view
}

func productWorkflowHasKnownTransitionAction(statusBucket string, currentState string, nextActions []string) bool {
	knownActions := productWorkflowKnownTransitionOwners(statusBucket, currentState)
	if len(knownActions) == 0 {
		return false
	}

	for _, action := range nonEmptyProductStatusActions(nextActions) {
		if _, ok := knownActions[action]; ok {
			return true
		}
	}

	return false
}

func productWorkflowKnownTransitionOwners(statusBucket string, currentState string) map[string]string {
	if !validStatusBuckets[statusBucket] {
		return nil
	}
	if validateStateBucketConsistency(statusBucket, currentState) != nil {
		return nil
	}
	if statusBucket == WorkflowStatusBucketUnstarted {
		return map[string]string{ProductWorkflowActionStart: ProductWorkflowActionOwnerOrchestrator}
	}
	if statusBucket == WorkflowStatusBucketClosed {
		return nil
	}

	entry := findStateEntry(currentState)
	if entry == nil {
		return nil
	}

	owners := make(map[string]string, len(entry.Actions))
	for _, action := range entry.Actions {
		owners[action.Action] = action.Rule.Owner
	}

	return owners
}

func markStatusSectionFailure(view *ProductStatusView, name string) {
	for i := range view.Sections {
		if view.Sections[i].Name == name {
			view.Sections[i].Status = ProductWorkflowStatusFail
			view.Sections[i].FindingCount++

			return
		}
	}

	view.Sections = append(view.Sections, StatusSection{
		Name:         name,
		Status:       ProductWorkflowStatusFail,
		FindingCount: 1,
	})
}

func loadProductRecoveryStatus(absRoot string, artifactPaths map[string]string) *ProductRecoveryStatus {
	packetPath := firstArtifactPath(artifactPaths, "recovery_packet", "recovery_decomposition_packet")
	failurePath := firstArtifactPath(artifactPaths, "recovery_failure", "recovery_failure_record")
	reconciliationPath := firstArtifactPath(artifactPaths, "recovery_reconciliation", "recovery_reconciliation_record")

	if packetPath == "" && failurePath == "" && reconciliationPath == "" {
		return nil
	}

	status := &ProductRecoveryStatus{
		Status:             ProductWorkflowStatusBlocked,
		PacketPath:         productRecoveryArtifactDisplayPath(absRoot, packetPath),
		FailureRecordPath:  productRecoveryArtifactDisplayPath(absRoot, failurePath),
		ReconciliationPath: productRecoveryArtifactDisplayPath(absRoot, reconciliationPath),
		Blockers:           []ProductRecoveryBlock{},
	}
	if packetPath == "" {
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    "missing_recovery_packet",
			Message: "workflow_state artifact_paths includes recovery metadata without a recovery_packet artifact",
		})

		return status
	}

	fullPath, safeRel, ok := resolveProductRecoveryArtifactPath(absRoot, packetPath)
	if !ok {
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    "unsafe_recovery_packet_path",
			Message: "recovery packet path must stay under the effort root",
		})

		return status
	}

	status.PacketPath = safeRel

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			status.Blockers = append(status.Blockers, ProductRecoveryBlock{
				Code:    "missing_recovery_packet",
				Message: "recovery packet artifact was not found under the effort root",
			})
		} else {
			status.Blockers = append(status.Blockers, ProductRecoveryBlock{
				Code:    "invalid_recovery_packet",
				Message: "recovery packet could not be inspected",
			})
		}

		return status
	}

	if strings.TrimSpace(reconciliationPath) != "" {
		reconciliationFullPath, reconciliationRel, ok := resolveProductRecoveryArtifactPath(absRoot, reconciliationPath)
		if !ok {
			status.Blockers = append(status.Blockers, ProductRecoveryBlock{
				Code:    "unsafe_recovery_reconciliation_path",
				Message: "recovery reconciliation path must stay under the effort root",
			})

			return status
		}
		status.ReconciliationPath = reconciliationRel
		if _, err := os.Stat(reconciliationFullPath); err != nil {
			if os.IsNotExist(err) {
				status.Blockers = append(status.Blockers, ProductRecoveryBlock{
					Code:    "missing_recovery_reconciliation",
					Message: "recovery reconciliation artifact was not found under the effort root",
				})
			} else {
				status.Blockers = append(status.Blockers, ProductRecoveryBlock{
					Code:    "invalid_recovery_reconciliation",
					Message: "recovery reconciliation could not be inspected",
				})
			}

			return status
		}
	}

	report, err := recoverstatus.BuildWithOptions(absRoot, safeRel, recoverstatus.BuildOptions{ReconciliationPath: reconciliationPath})
	if err != nil {
		code := "invalid_recovery_packet"
		message := "recovery packet could not be decoded"
		if strings.TrimSpace(reconciliationPath) != "" || strings.Contains(strings.ToLower(err.Error()), "reconciliation") {
			code = "invalid_recovery_reconciliation"
			message = "recovery reconciliation could not be decoded"
		}
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    code,
			Message: message,
		})

		return status
	}

	status.Status = productRecoveryStatusFromPacketStatus(report.Status)
	status.PacketPath = textutil.FirstNonEmpty(report.PacketPath, status.PacketPath)
	status.ReconciliationPath = textutil.FirstNonEmpty(report.ReconciliationPath, status.ReconciliationPath)
	status.PacketID = report.PacketID
	status.FailureID = report.FailureID
	status.FailureClass = report.FailureClass
	status.FailureKind = report.FailureKind
	status.SourceSessionID = report.SourceSessionID
	status.NextValidAction = string(report.NextAction)
	status.CandidateAtomCount = report.CandidateAtomCount
	status.CompletedAtomCount = report.CompletedAtomCount
	status.RemainingAtomCount = report.RemainingAtomCount
	status.ResidualWorkCount = report.ResidualWorkCount
	status.Gates = append([]string(nil), report.Gates...)
	status.RequiredTests = append([]string(nil), report.RequiredTests...)
	status.ProductLinks = productRecoveryLinks(report.ProductLinks)
	status.Blockers = append(status.Blockers, productRecoveryBlocksFromReport(report)...)

	return status
}

func firstArtifactPath(paths map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(paths[name]); value != "" {
			return value
		}
	}

	return ""
}

func productRecoveryArtifactDisplayPath(absRoot string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	if _, rel, ok := resolveProductRecoveryArtifactPath(absRoot, path); ok {
		return rel
	}

	return "outside_effort_root"
}

func resolveProductRecoveryArtifactPath(absRoot string, relPath string) (string, string, bool) {
	absRoot = filepath.Clean(strings.TrimSpace(absRoot))
	relPath = strings.TrimSpace(relPath)

	if absRoot == "" || relPath == "" {
		return "", "", false
	}

	fullPath := relPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(absRoot, filepath.FromSlash(relPath))
	}

	fullPath = filepath.Clean(fullPath)

	rel, err := filepath.Rel(absRoot, fullPath)

	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", false
	}

	return fullPath, filepath.ToSlash(rel), true
}

func productRecoveryStatusFromPacketStatus(status recoverycontract.PacketStatus) ProductWorkflowStatus {
	return productRecoveryStatusFromReportStatus(string(status))
}

func productRecoveryStatusFromReportStatus(status string) ProductWorkflowStatus {
	switch status {
	case string(recoverycontract.PacketStatusReady):
		return ProductWorkflowStatusReady
	case string(recoverycontract.PacketStatusNeedsInspection):
		return ProductWorkflowStatusNeedsInspection
	case string(recoverycontract.PacketStatusRecovered):
		return ProductWorkflowStatusClosed
	case string(recoverycontract.PacketStatusBlocked):
		return ProductWorkflowStatusBlocked
	case string(recoverycontract.PacketStatusPartial):
		return ProductWorkflowStatusPartial
	default:
		return ProductWorkflowStatusPending
	}
}

func productRecoveryPacketGates(packet recoverycontract.DecompositionPacket) []string {
	seen := map[string]bool{}

	var gates []string

	for _, atom := range packet.CandidateAtoms {
		for _, gate := range atom.Gates {
			gate = strings.TrimSpace(gate)
			if gate == "" || seen[gate] {
				continue
			}

			seen[gate] = true
			gates = append(gates, gate)
		}
	}

	sort.Strings(gates)

	return gates
}

func productRecoveryPacketRequiredTests(packet recoverycontract.DecompositionPacket) []string {
	seen := map[string]bool{}

	var tests []string

	for _, atom := range packet.CandidateAtoms {
		for _, test := range atom.RequiredTests {
			test = strings.TrimSpace(test)
			if test == "" || seen[test] {
				continue
			}

			seen[test] = true
			tests = append(tests, test)
		}
	}

	sort.Strings(tests)

	return tests
}

func productRecoveryBlocksFromReport(report recoverstatus.Report) []ProductRecoveryBlock {
	var blocks []ProductRecoveryBlock
	for _, finding := range report.ReconciliationFindings {
		if strings.TrimSpace(finding.Code) == "" && strings.TrimSpace(finding.Message) == "" {
			continue
		}
		blocks = append(blocks, ProductRecoveryBlock{
			Code:    textutil.FirstNonEmpty(finding.Code, "invalid_recovery_reconciliation"),
			Message: finding.Message,
		})
	}

	for _, blocker := range report.Blockers {
		blocker = strings.TrimSpace(blocker)
		if blocker == "" {
			continue
		}
		blocks = append(blocks, ProductRecoveryBlock{
			Code:    "recovery_requires_human_action",
			Message: blocker,
		})
	}
	return blocks
}

func productRecoveryLinks(links recoverycontract.ProductLinks) ProductRecoveryLinks {
	return ProductRecoveryLinks{
		PRDPaths:    productRecoverySafePaths(links.PRDPaths),
		ADRPaths:    productRecoverySafePaths(links.ADRPaths),
		TDDPaths:    productRecoverySafePaths(links.TDDPaths),
		LedgerPaths: productRecoverySafePaths(links.LedgerPaths),
	}
}

func productRecoverySafePaths(paths []string) []string {
	seen := map[string]bool{}

	var out []string

	for _, path := range paths {
		raw := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
		if raw == "" {
			continue
		}

		for _, part := range strings.Split(raw, "/") {
			if part == ".." {
				raw = ""
				break
			}
		}

		if raw == "" {
			continue
		}

		path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
		if path == "" || path == "." || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) || seen[path] {
			continue
		}

		seen[path] = true
		out = append(out, path)
	}

	sort.Strings(out)

	return out
}

func productRecoveryOperatorAction(recoveryStatus ProductRecoveryStatus, blockerCodes []string, blockers []ProductOperatorBlocker) ProductOperatorAction {
	action := strings.TrimSpace(recoveryStatus.NextValidAction)
	if action == "" {
		action = ProductWorkflowActionInspectFailure
	}

	id := "recovery_" + productOperatorFallbackActionID(action)
	if id == "recovery_" {
		id = "recovery_inspect_failure"
	}

	return ProductOperatorAction{
		Action:       action,
		ID:           id,
		Label:        productOperatorActionLabel(action),
		Owner:        ProductWorkflowActionOwnerOperator,
		Dispatchable: false,
		RunnableNow:  true,
		Message:      productRecoveryOperatorMessage(recoveryStatus),
		BlockedBy:    append([]string{}, blockerCodes...),
		Blockers:     append([]ProductOperatorBlocker{}, blockers...),
		CommandHint:  productRecoveryCommandHint(recoveryStatus),
	}
}

func productRecoveryOperatorMessage(recoveryStatus ProductRecoveryStatus) string {
	if recoveryStatus.CandidateAtomCount > 0 {
		return fmt.Sprintf("review %d recovery candidate atom(s) before continuing", recoveryStatus.CandidateAtomCount)
	}

	if recoveryStatus.Status == ProductWorkflowStatusBlocked {
		return "resolve recovery blockers before continuing"
	}

	return "inspect recovery packet before continuing"
}

func productRecoveryCommandHint(recoveryStatus ProductRecoveryStatus) string {
	if recoveryStatus.PacketPath != "" {
		return "idiom recover status --packet " + recoveryStatus.PacketPath
	}

	if recoveryStatus.SourceSessionID != "" {
		return "idiom recover inspect --session " + recoveryStatus.SourceSessionID
	}

	return "idiom recover status"
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

func productOperatorRequestsImplementationDispatch(actions []string) bool {
	for _, action := range actions {
		if textutil.EqualTrimFold(action, ProductWorkflowActionStartImplementation) {
			return true
		}
	}

	return false
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
		return ProductWorkflowActionOwnerOperator
	case strings.Contains(normalized, "approve") || strings.Contains(normalized, "human"):
		return ProductWorkflowActionOwnerOperator
	case strings.Contains(normalized, "review"):
		return ProductWorkflowActionOwnerReviewer
	case strings.Contains(normalized, "verify"):
		return ProductWorkflowActionOwnerVerifier
	case strings.Contains(normalized, "implement") || strings.Contains(normalized, "worker"):
		return ProductWorkflowActionOwnerImplementor
	case strings.Contains(normalized, "investigat"):
		return ProductWorkflowActionOwnerInvestigator
	case strings.Contains(normalized, "audit") || strings.Contains(normalized, "lint") || strings.Contains(normalized, "status"):
		return ProductWorkflowActionOwnerProduct
	default:
		return ProductWorkflowActionOwnerOperator
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
	case relPath == ObsidianProductWorkflowPath || relPath == ObsidianRecoveryPath:
		return "obsidian"
	default:
		return ""
	}
}

type LintSummaryFinding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type LintSummaryResult struct {
	Status          ProductWorkflowStatus `json:"status"`
	EffortRoot      string                `json:"effort_root,omitempty"`
	CheckedFiles    []string              `json:"checked_files"`
	Findings        []LintSummaryFinding  `json:"findings"`
	NextValidAction string                `json:"next_valid_action,omitempty"`
}
