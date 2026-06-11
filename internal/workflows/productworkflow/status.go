package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/platform/util"
	"idiom/internal/workflows/recovery"
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
	Recovery         *ProductRecoveryStatus  `json:"recovery,omitempty"`
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
	Recovery              *ProductRecoveryStatus    `json:"recovery,omitempty"`
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
	Status             string                 `json:"status"`
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
			AuditStatus:      ProductWorkflowStatusError,
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
			AuditStatus:      ProductWorkflowStatusError,
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
	return view, nil
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
	if status.Recovery != nil && status.Recovery.Status != ProductWorkflowStatusClosed {
		view.NextValidActions = append(view.NextValidActions, productRecoveryOperatorAction(*status.Recovery, blockerCodes, blockingReasons))
	}

	return view
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
	packet, _, found, err := recovery.TryLoadDecompositionPacket(fullPath)
	if err != nil {
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    "invalid_recovery_packet",
			Message: "recovery packet could not be decoded",
		})
		return status
	}
	if !found {
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    "missing_recovery_packet",
			Message: "recovery packet artifact was not found under the effort root",
		})
		return status
	}
	status.Status = productRecoveryPacketStatus(packet)
	status.PacketID = packet.PacketID
	status.FailureID = packet.FailureID
	status.FailureClass = packet.Failure.Class
	status.FailureKind = packet.Failure.Kind
	status.SourceSessionID = packet.SourceRun.SessionID
	status.NextValidAction = packet.NextAction
	status.CandidateAtomCount = len(packet.CandidateAtoms)
	status.RemainingAtomCount = len(packet.CandidateAtoms)
	status.ResidualWorkCount = len(packet.ResidualWork)
	status.Gates = productRecoveryPacketGates(packet)
	status.RequiredTests = productRecoveryPacketRequiredTests(packet)
	status.ProductLinks = productRecoveryLinks(packet.ProductLinks)
	if status.Status == ProductWorkflowStatusBlocked {
		status.Blockers = append(status.Blockers, ProductRecoveryBlock{
			Code:    "recovery_requires_human_action",
			Message: fmt.Sprintf("recovery next_valid_action is %q", packet.NextAction),
		})
	}
	if reconciliation, relPath, found, blockers := loadProductRecoveryReconciliation(absRoot, reconciliationPath, safeRel, packet); len(blockers) > 0 {
		status.Blockers = append(status.Blockers, blockers...)
	} else if found {
		applyProductRecoveryReconciliation(status, packet, reconciliation, relPath)
	}
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

func productRecoveryPacketStatus(packet recovery.DecompositionPacket) string {
	switch packet.NextAction {
	case recovery.NextActionReviewDecomposition:
		if len(packet.CandidateAtoms) > 0 {
			return ProductWorkflowStatusReady
		}
		return ProductWorkflowStatusNeedsInspection
	case recovery.NextActionInspectFailure:
		return ProductWorkflowStatusNeedsInspection
	case recovery.NextActionNone:
		return ProductWorkflowStatusClosed
	case recovery.NextActionAnswerBlocker, recovery.NextActionHumanIntervention:
		return ProductWorkflowStatusBlocked
	default:
		return ProductWorkflowStatusPending
	}
}

func productRecoveryPacketGates(packet recovery.DecompositionPacket) []string {
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

func productRecoveryPacketRequiredTests(packet recovery.DecompositionPacket) []string {
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

func loadProductRecoveryReconciliation(absRoot string, configuredPath string, packetRelPath string, packet recovery.DecompositionPacket) (recovery.Reconciliation, string, bool, []ProductRecoveryBlock) {
	if strings.TrimSpace(configuredPath) != "" {
		fullPath, safeRel, ok := resolveProductRecoveryArtifactPath(absRoot, configuredPath)
		if !ok {
			return recovery.Reconciliation{}, "outside_effort_root", false, []ProductRecoveryBlock{{
				Code:    "unsafe_recovery_reconciliation_path",
				Message: "recovery reconciliation path must stay under the effort root",
			}}
		}
		reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
		if err != nil {
			return recovery.Reconciliation{}, safeRel, false, []ProductRecoveryBlock{{
				Code:    "invalid_recovery_reconciliation",
				Message: "recovery reconciliation could not be decoded",
			}}
		}
		if !found {
			return recovery.Reconciliation{}, safeRel, false, []ProductRecoveryBlock{{
				Code:    "missing_recovery_reconciliation",
				Message: "recovery reconciliation artifact was not found under the effort root",
			}}
		}
		return reconciliation, safeRel, true, productRecoveryReconciliationValidationBlockers(packet, reconciliation)
	}
	if strings.TrimSpace(packetRelPath) != "" {
		siblingRel := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(packetRelPath)), "reconciliation.json"))
		fullPath := filepath.Join(absRoot, filepath.FromSlash(siblingRel))
		reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
		if err != nil {
			return recovery.Reconciliation{}, siblingRel, false, []ProductRecoveryBlock{{
				Code:    "invalid_recovery_reconciliation",
				Message: "recovery reconciliation could not be decoded",
			}}
		}
		if found {
			return reconciliation, siblingRel, true, productRecoveryReconciliationValidationBlockers(packet, reconciliation)
		}
	}
	fullPath, relPath, err := recovery.ReconciliationPath(absRoot, packet.FailureID, "")
	if err != nil {
		return recovery.Reconciliation{}, "", false, nil
	}
	reconciliation, _, found, err := recovery.TryLoadReconciliation(fullPath)
	if err != nil {
		return recovery.Reconciliation{}, relPath, false, []ProductRecoveryBlock{{
			Code:    "invalid_recovery_reconciliation",
			Message: "recovery reconciliation could not be decoded",
		}}
	}
	if !found {
		return recovery.Reconciliation{}, "", false, nil
	}
	return reconciliation, relPath, true, productRecoveryReconciliationValidationBlockers(packet, reconciliation)
}

func productRecoveryReconciliationValidationBlockers(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) []ProductRecoveryBlock {
	findings := recovery.ValidateReconciliation(packet, reconciliation)
	if len(findings) == 0 {
		return nil
	}
	blockers := make([]ProductRecoveryBlock, 0, len(findings))
	for _, finding := range findings {
		blockers = append(blockers, ProductRecoveryBlock{
			Code:    finding.Code,
			Message: finding.Message,
		})
	}
	return blockers
}

func applyProductRecoveryReconciliation(status *ProductRecoveryStatus, packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation, relPath string) {
	status.ReconciliationPath = relPath
	status.CompletedAtomCount = len(reconciliation.CompletedAtoms)
	status.RemainingAtomCount = len(reconciliation.RemainingAtoms)
	status.NextValidAction = reconciliation.NextAction
	switch reconciliation.Status {
	case recovery.ReconciliationStatusRecovered:
		status.Status = ProductWorkflowStatusClosed
		status.NextValidAction = recovery.NextActionNone
	case recovery.ReconciliationStatusPartial:
		status.Status = ProductWorkflowStatusPartial
		status.NextValidAction = recovery.NextActionReviewDecomposition
		if status.RemainingAtomCount == 0 {
			status.RemainingAtomCount = len(productRecoveryRemainingAtomIDs(packet, reconciliation))
		}
	}
}

func productRecoveryRemainingAtomIDs(packet recovery.DecompositionPacket, reconciliation recovery.Reconciliation) []string {
	completed := map[string]bool{}
	for _, atom := range reconciliation.CompletedAtoms {
		completed[atom.ID] = true
	}
	var remaining []string
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			remaining = append(remaining, atom.ID)
		}
	}
	sort.Strings(remaining)
	return remaining
}

func productRecoveryLinks(links recovery.ProductLinks) ProductRecoveryLinks {
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
		action = "inspect_failure"
	}
	id := "recovery_" + productOperatorFallbackActionID(action)
	if id == "recovery_" {
		id = "recovery_inspect_failure"
	}
	return ProductOperatorAction{
		Action:       action,
		ID:           id,
		Label:        productOperatorActionLabel(action),
		Owner:        "operator",
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
		if util.EqualTrimFold(action, "start_implementation") {
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
		return "operator"
	case strings.Contains(normalized, "approve") || strings.Contains(normalized, "human"):
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
	Status          string               `json:"status"`
	EffortRoot      string               `json:"effort_root,omitempty"`
	CheckedFiles    []string             `json:"checked_files"`
	Findings        []LintSummaryFinding `json:"findings"`
	NextValidAction string               `json:"next_valid_action,omitempty"`
}
