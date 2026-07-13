package productworkflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/atomicfile"
)

type ProductWorkflowTransitionApplyOptions struct {
	EffortRoot            string
	Action                string
	Actor                 string
	UpdatedAt             string
	EventID               string
	ExpectedStateRevision int
}

type ProductWorkflowTransitionApplyResult struct {
	Status               ProductWorkflowStatus              `json:"status"`
	EffortRoot           string                             `json:"effort_root"`
	Action               string                             `json:"action"`
	FromState            string                             `json:"from_state"`
	ToState              string                             `json:"to_state,omitempty"`
	UpdatedWorkflowState WorkflowState                      `json:"updated_workflow_state,omitempty"`
	Event                ProductWorkflowTransitionEvent     `json:"event,omitempty"`
	Blockers             []ProductWorkflowTransitionBlocker `json:"blockers"`
	CommitWarning        string                             `json:"commit_warning,omitempty"`
	NextValidAction      string                             `json:"next_valid_action"`
}

const (
	productWorkflowTransitionTransactionRelPath = ".idiom-product-transition-transaction"
	productWorkflowTransitionManifestFileName   = "manifest.json"
	productWorkflowTransitionManifestSchema     = "idiom.product_workflow_transition.v1"
	productWorkflowTransitionStatePrepared      = "prepared"
	productWorkflowTransitionStateCommitted     = "committed"
)

type productWorkflowTransitionPublicationBoundary uint8

const (
	productWorkflowTransitionBeforeTransactionEntry productWorkflowTransitionPublicationBoundary = iota
	productWorkflowTransitionBeforePreparedManifest
	productWorkflowTransitionBeforeWorkflowState
	productWorkflowTransitionBeforeEventLedger
	productWorkflowTransitionBeforeCommittedManifest
	productWorkflowTransitionBeforeTransactionCleanup
)

var productWorkflowTransitionPublicationBoundaryHook func(productWorkflowTransitionPublicationBoundary)

type productWorkflowTransitionManifest struct {
	SchemaVersion           string                         `json:"schema_version"`
	State                   string                         `json:"state"`
	PreviousWorkflowDigest  string                         `json:"previous_workflow_digest"`
	CandidateWorkflowDigest string                         `json:"candidate_workflow_digest"`
	EventsPath              string                         `json:"events_path,omitempty"`
	Event                   ProductWorkflowTransitionEvent `json:"event"`
}

type ProductWorkflowTransitionBlocker struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type ProductWorkflowTransitionEvent struct {
	Event     string                               `json:"event"`
	EventID   string                               `json:"event_id"`
	EffortID  string                               `json:"effort_id"`
	TaskID    string                               `json:"task_id"`
	Timestamp string                               `json:"timestamp"`
	Source    string                               `json:"source"`
	Actor     string                               `json:"actor,omitempty"`
	Action    string                               `json:"action"`
	Details   ProductWorkflowTransitionEventDetail `json:"details"`
}

type ProductWorkflowTransitionEventDetail struct {
	FromState        string `json:"from_state"`
	ToState          string `json:"to_state"`
	StateRevision    int    `json:"state_revision"`
	Outcome          string `json:"outcome,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	CompletionDigest string `json:"completion_digest,omitempty"`
}

func ApplyProductWorkflowTransition(options ProductWorkflowTransitionApplyOptions) ProductWorkflowTransitionApplyResult {
	result := ProductWorkflowTransitionApplyResult{
		Status:          ProductWorkflowStatusBlocked,
		EffortRoot:      strings.TrimSpace(options.EffortRoot),
		Action:          strings.TrimSpace(options.Action),
		Blockers:        []ProductWorkflowTransitionBlocker{},
		NextValidAction: "resolve product workflow transition blockers before continuing",
	}
	if result.EffortRoot == "" {
		result.addBlocker("missing_effort_root", "", "effort root is required")

		return result
	}

	if result.Action == "" {
		result.addBlocker("missing_action", "", "transition action is required")

		return result
	}

	absRoot, err := filepath.Abs(result.EffortRoot)
	if err != nil {
		result.addBlocker("resolve_effort_root_error", "", err.Error())

		return result
	}

	result.EffortRoot = filepath.Clean(absRoot)

	// The lease is acquired before lint, status, metrics, and workflow-state
	// evaluation so every transition gate consumes one rooted authority snapshot.
	lease, err := openProductWorkflowMutationLease(result.EffortRoot)
	if err != nil {
		result.addBlocker("product_workflow_lock_error", WorkflowStateFileName, err.Error())

		return result
	}
	defer lease.Close()

	statusView, workflowState, statusErr := loadCheckedProductStatusRoot(result.EffortRoot, lease.root)
	if statusErr != nil {
		result.addBlocker("product_status_error", WorkflowStateFileName, statusErr.Error())

		return result
	}

	if workflowState == nil {
		result.addBlocker("missing_workflow_state", WorkflowStateFileName, WorkflowStateFileName+" not loaded")

		return result
	}

	result.FromState = workflowState.CurrentState

	operatorStatus := BuildProductOperatorStatusView(statusView, workflowState)

	for _, blocker := range operatorStatus.Blockers {
		result.addBlocker(blocker.Code, blocker.Source, blocker.Message)
	}

	if len(result.Blockers) > 0 {
		return result
	}

	if !stringSliceContains(workflowState.NextValidActions, result.Action) {
		result.addBlocker("action_not_listed", "workflow_state.next_valid_actions", fmt.Sprintf("action %q is not listed as a next_valid_action", result.Action))

		return result
	}

	transition := EvaluateTransition(TransitionInput{
		StatusBucket:    workflowState.StatusBucket,
		CurrentState:    workflowState.CurrentState,
		RequestedAction: result.Action,
		OpenBlockers:    workflowState.Blockers,
		OpenObligations: workflowState.OpenObligations,
		GateResults:     workflowState.Coverage.GateResults,
	})
	for _, blocker := range transition.Blockers {
		result.addBlocker(blocker.Code, "workflow_transition", blocker.Message)
	}

	if len(result.Blockers) > 0 {
		return result
	}

	if strings.TrimSpace(transition.ToState) == "" {
		result.addBlocker("empty_transition_target", "workflow_transition", fmt.Sprintf("action %q did not produce a target state", result.Action))

		return result
	}

	if options.ExpectedStateRevision > 0 && workflowState.StateRevision != options.ExpectedStateRevision {
		result.addBlocker("stale_workflow_state_revision", WorkflowStateFileName, fmt.Sprintf("workflow state revision %d does not match expected revision %d", workflowState.StateRevision, options.ExpectedStateRevision))

		return result
	}

	updatedAt := strings.TrimSpace(options.UpdatedAt)
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	eventID := strings.TrimSpace(options.EventID)
	if eventID == "" {
		eventID = "evt-product-transition-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}

	updated := *workflowState

	updated.CurrentState = transition.ToState
	if bucket, ok := validStates[transition.ToState]; ok {
		updated.StatusBucket = bucket
	}
	updated.CurrentPhase = productWorkflowTransitionPhase(updated.CurrentPhase, transition.ToState)
	updated.UpdatedAt = updatedAt
	updated.StateRevision++

	updated.LastEventID = eventID
	updated.NextValidActions = transitionActionNames(nextActionsFor(transition.ToState, updated.Coverage.GateResults))

	event := ProductWorkflowTransitionEvent{
		Event:     string(ProductWorkflowEventTransitionApplied),
		EventID:   eventID,
		EffortID:  updated.EffortID,
		TaskID:    updated.TaskID,
		Timestamp: updatedAt,
		Source:    string(ProductWorkflowEventSourceTransitionApply),
		Actor:     strings.TrimSpace(options.Actor),
		Action:    result.Action,
		Details: ProductWorkflowTransitionEventDetail{
			FromState:     workflowState.CurrentState,
			ToState:       updated.CurrentState,
			StateRevision: updated.StateRevision,
		},
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		result.addBlocker("stale_effort_root_identity", WorkflowStateFileName, err.Error())

		return result
	}
	writeErr := writeProductWorkflowTransition(lease, updated, event)
	if writeErr != nil && !artifactWriteCommitted(writeErr) {
		result.addBlocker("transition_write_error", WorkflowStateFileName, writeErr.Error())

		return result
	}

	result.Status = ProductWorkflowStatusApplied
	result.ToState = updated.CurrentState
	result.UpdatedWorkflowState = updated
	result.Event = event
	if writeErr != nil {
		result.CommitWarning = writeErr.Error()
	}
	result.NextValidAction = firstTransitionAction(updated.NextValidActions)

	return result
}

func (r *ProductWorkflowTransitionApplyResult) addBlocker(code, path, message string) {
	r.Blockers = append(r.Blockers, ProductWorkflowTransitionBlocker{
		Code:    strings.TrimSpace(code),
		Path:    strings.TrimSpace(path),
		Message: strings.TrimSpace(message),
	})
}

func productWorkflowTransitionPhase(currentPhase, toState string) string {
	switch strings.TrimSpace(toState) {
	case WorkflowStateHumanVerification:
		return "phase-human-verification"
	case WorkflowStateImplementing:
		return "phase-implementation"
	default:
		if strings.TrimSpace(currentPhase) != "" {
			return currentPhase
		}

		return "phase-" + strings.ReplaceAll(strings.TrimSpace(toState), "_", "-")
	}
}

func firstTransitionAction(actions []string) string {
	for _, action := range actions {
		if strings.TrimSpace(action) != "" {
			return strings.TrimSpace(action)
		}
	}

	return ""
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}

func writeProductWorkflowTransition(lease *productWorkflowMutationLease, state WorkflowState, event ProductWorkflowTransitionEvent) error {
	if lease == nil || lease.root == nil {
		return fmt.Errorf("product workflow mutation root is not open")
	}
	if err := recoverProductWorkflowTransitionLocked(lease); err != nil {
		return fmt.Errorf("recover interrupted product workflow transition: %w", err)
	}
	root := lease.root
	originalWorkflowBytes, err := readProductWorkflowTransitionRegularFile(root, WorkflowStateFileName)
	if err != nil {
		return fmt.Errorf("read %s: %w", WorkflowStateFileName, err)
	}

	workflowBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", WorkflowStateFileName, err)
	}

	workflowBytes = append(workflowBytes, '\n')

	eventsPath, err := productWorkflowEventsPath(state)
	if err != nil {
		return err
	}
	if err := rejectTransitionSymlinkParentRoot(root, eventsPath); err != nil {
		return err
	}
	if err := rejectTransitionSymlinkRoot(root, eventsPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	exists, _, inspectErr := inspectProductWorkflowTransitionEvent(root, eventsPath, event)
	if inspectErr != nil {
		return fmt.Errorf("inspect transition event ledger: %w", inspectErr)
	}
	if exists && productWorkflowTransitionDigest(originalWorkflowBytes) == productWorkflowTransitionDigest(workflowBytes) {
		return fmt.Errorf("transition event_id %q already exists", event.EventID)
	}

	manifest := productWorkflowTransitionManifest{
		SchemaVersion:           productWorkflowTransitionManifestSchema,
		State:                   productWorkflowTransitionStatePrepared,
		PreviousWorkflowDigest:  productWorkflowTransitionDigest(originalWorkflowBytes),
		CandidateWorkflowDigest: productWorkflowTransitionDigest(workflowBytes),
		EventsPath:              filepath.ToSlash(eventsPath),
		Event:                   event,
	}
	if err := prepareProductWorkflowTransition(lease, manifest); err != nil {
		return err
	}

	if productWorkflowTransitionPublicationBoundaryHook != nil {
		productWorkflowTransitionPublicationBoundaryHook(productWorkflowTransitionBeforeWorkflowState)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return abortProductWorkflowTransition(lease, fmt.Errorf("revalidate effort root before workflow-state publication: %w", err))
	}
	stateWriteErr := atomicfile.WriteFileRootChecked(root, WorkflowStateFileName, workflowBytes, 0o644, lease.ValidatePathIdentity)
	if stateWriteErr != nil && !artifactWriteCommitted(stateWriteErr) {
		return abortProductWorkflowTransition(lease, fmt.Errorf("replace %s: %w", WorkflowStateFileName, stateWriteErr))
	}
	if err := verifyProductWorkflowTransitionState(root, manifest); err != nil {
		return productWorkflowTransitionPostCommit("workflow state committed but candidate verification failed", fmt.Errorf("%v: %w", stateWriteErr, err))
	}
	if stateWriteErr != nil {
		if err := lease.ValidatePathIdentity(); err != nil {
			return productWorkflowTransitionPostCommit("workflow state committed but effort-root identity changed before sync retry", fmt.Errorf("%v: %w", stateWriteErr, err))
		}
		if err := atomicfile.SyncDirRoot(root, "."); err != nil {
			return productWorkflowTransitionPostCommit("workflow state committed but effort-root sync retry failed", fmt.Errorf("%v: %w", stateWriteErr, err))
		}
	}

	if err := ensureProductWorkflowTransitionEvent(lease, manifest); err != nil {
		return productWorkflowTransitionPostCommit("workflow state committed but transition event completion failed", err)
	}

	manifest.State = productWorkflowTransitionStateCommitted
	if err := writeProductWorkflowTransitionManifestDurable(lease, manifest); err != nil {
		return productWorkflowTransitionPostCommit("workflow state and event committed but journal finalization failed", err)
	}
	if err := removeProductWorkflowTransitionTransaction(lease); err != nil {
		return productWorkflowTransitionPostCommit("workflow state and event committed but journal cleanup failed", err)
	}

	return nil
}

func rejectTransitionSymlinkParentRoot(root *os.Root, targetPath string) error {
	parent := filepath.Dir(targetPath)
	if parent == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(filepath.ToSlash(parent), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not be a symlink", filepath.Base(current))
		}
	}

	return nil
}

func recoverProductWorkflowTransitionLocked(lease *productWorkflowMutationLease) error {
	if lease == nil || lease.root == nil {
		return fmt.Errorf("product workflow mutation root is not open")
	}
	root := lease.root
	transactionInfo, err := root.Lstat(productWorkflowTransitionTransactionRelPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect product workflow transition transaction: %w", err)
	}
	if transactionInfo.Mode()&os.ModeSymlink != 0 || !transactionInfo.IsDir() {
		return fmt.Errorf("product workflow transition transaction path must be a directory and must not be a symlink")
	}

	manifest, err := readProductWorkflowTransitionManifest(root)
	if os.IsNotExist(err) {
		// The authoritative workflow is not replaced until the prepared manifest
		// is durable. A transaction without a manifest is therefore safe to abort.
		return removeProductWorkflowTransitionTransaction(lease)
	}
	if err != nil {
		return fmt.Errorf("read product workflow transition journal: %w", err)
	}

	workflowBytes, err := readProductWorkflowTransitionRegularFile(root, WorkflowStateFileName)
	if err != nil {
		return fmt.Errorf("read workflow state during transition recovery: %w", err)
	}
	workflowDigest := productWorkflowTransitionDigest(workflowBytes)
	if workflowDigest == manifest.PreviousWorkflowDigest {
		if manifest.State == productWorkflowTransitionStateCommitted {
			return fmt.Errorf("committed product workflow transition reverted to its previous workflow generation")
		}

		return removeProductWorkflowTransitionTransaction(lease)
	}
	if workflowDigest != manifest.CandidateWorkflowDigest {
		return fmt.Errorf("workflow state matches neither transition journal generation")
	}
	if err := verifyProductWorkflowTransitionState(root, manifest); err != nil {
		return productWorkflowTransitionPostCommit("journaled workflow state committed but candidate verification failed", err)
	}
	if err := ensureProductWorkflowTransitionEvent(lease, manifest); err != nil {
		return productWorkflowTransitionPostCommit("journaled workflow state committed but event recovery failed", err)
	}
	if manifest.State != productWorkflowTransitionStateCommitted {
		manifest.State = productWorkflowTransitionStateCommitted
		if err := writeProductWorkflowTransitionManifestDurable(lease, manifest); err != nil {
			return productWorkflowTransitionPostCommit("journaled workflow state and event committed but journal finalization failed", err)
		}
	}

	if err := removeProductWorkflowTransitionTransaction(lease); err != nil {
		return productWorkflowTransitionPostCommit("journaled workflow state and event committed but journal cleanup failed", err)
	}

	return nil
}

func prepareProductWorkflowTransition(lease *productWorkflowMutationLease, manifest productWorkflowTransitionManifest) error {
	if err := validateProductWorkflowTransitionManifest(manifest); err != nil {
		return err
	}
	root := lease.root
	if productWorkflowTransitionPublicationBoundaryHook != nil {
		productWorkflowTransitionPublicationBoundaryHook(productWorkflowTransitionBeforeTransactionEntry)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate effort root before transition journal publication: %w", err)
	}
	if err := root.Mkdir(productWorkflowTransitionTransactionRelPath, 0o700); err != nil {
		return fmt.Errorf("create product workflow transition transaction: %w", err)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return abortProductWorkflowTransition(lease, fmt.Errorf("revalidate effort root before transition journal directory sync: %w", err))
	}
	if err := atomicfile.SyncDirRoot(root, "."); err != nil {
		return abortProductWorkflowTransition(lease, fmt.Errorf("sync product workflow transition transaction entry: %w", err))
	}
	if err := writeProductWorkflowTransitionManifestDurable(lease, manifest); err != nil {
		return abortProductWorkflowTransition(lease, fmt.Errorf("prepare product workflow transition journal: %w", err))
	}

	return nil
}

func abortProductWorkflowTransition(lease *productWorkflowMutationLease, cause error) error {
	if cleanupErr := removeProductWorkflowTransitionTransaction(lease); cleanupErr != nil {
		return fmt.Errorf("%w (transition transaction cleanup failed: %v)", cause, cleanupErr)
	}

	return cause
}

func productWorkflowTransitionPostCommit(operation string, err error) error {
	return &atomicfile.PostCommitError{Op: strings.TrimSpace(operation), Err: err}
}

func productWorkflowTransitionDigest(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func writeProductWorkflowTransitionManifestDurable(lease *productWorkflowMutationLease, manifest productWorkflowTransitionManifest) error {
	if err := validateProductWorkflowTransitionManifest(manifest); err != nil {
		return err
	}
	root := lease.root
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(productWorkflowTransitionTransactionRelPath, productWorkflowTransitionManifestFileName)
	boundary := productWorkflowTransitionBeforePreparedManifest
	if manifest.State == productWorkflowTransitionStateCommitted {
		boundary = productWorkflowTransitionBeforeCommittedManifest
	}
	if productWorkflowTransitionPublicationBoundaryHook != nil {
		productWorkflowTransitionPublicationBoundaryHook(boundary)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate effort root before transition manifest publication: %w", err)
	}
	writeErr := atomicfile.WriteFileRootChecked(root, manifestPath, append(body, '\n'), 0o600, lease.ValidatePathIdentity)
	if writeErr == nil {
		return nil
	}
	if !artifactWriteCommitted(writeErr) {
		return writeErr
	}

	written, readErr := readProductWorkflowTransitionManifest(root)
	if readErr != nil {
		return fmt.Errorf("%v; verify committed transition journal: %w", writeErr, readErr)
	}
	if written != manifest {
		return fmt.Errorf("%v; committed transition journal differs from requested manifest", writeErr)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("%v; revalidate effort root before transition journal sync retry: %w", writeErr, err)
	}
	if syncErr := atomicfile.SyncDirRoot(root, productWorkflowTransitionTransactionRelPath); syncErr != nil {
		return fmt.Errorf("%v; retry transition journal directory sync: %w", writeErr, syncErr)
	}

	return nil
}

func readProductWorkflowTransitionManifest(root *os.Root) (productWorkflowTransitionManifest, error) {
	manifestPath := filepath.Join(productWorkflowTransitionTransactionRelPath, productWorkflowTransitionManifestFileName)
	body, err := readProductWorkflowTransitionRegularFile(root, manifestPath)
	if err != nil {
		return productWorkflowTransitionManifest{}, err
	}
	var manifest productWorkflowTransitionManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return productWorkflowTransitionManifest{}, err
	}
	if err := validateProductWorkflowTransitionManifest(manifest); err != nil {
		return productWorkflowTransitionManifest{}, err
	}

	return manifest, nil
}

func validateProductWorkflowTransitionManifest(manifest productWorkflowTransitionManifest) error {
	if manifest.SchemaVersion != productWorkflowTransitionManifestSchema {
		return fmt.Errorf("unsupported product workflow transition manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.State != productWorkflowTransitionStatePrepared && manifest.State != productWorkflowTransitionStateCommitted {
		return fmt.Errorf("unsupported product workflow transition manifest state %q", manifest.State)
	}
	if !validWorkflowArtifactDigest(manifest.PreviousWorkflowDigest) || !validWorkflowArtifactDigest(manifest.CandidateWorkflowDigest) {
		return fmt.Errorf("product workflow transition manifest contains an invalid workflow digest")
	}
	if manifest.PreviousWorkflowDigest == manifest.CandidateWorkflowDigest {
		return fmt.Errorf("product workflow transition manifest does not change the workflow generation")
	}
	if strings.TrimSpace(manifest.Event.EventID) == "" {
		return fmt.Errorf("product workflow transition manifest event_id is required")
	}
	if strings.TrimSpace(manifest.Event.Details.ToState) == "" || manifest.Event.Details.StateRevision <= 0 {
		return fmt.Errorf("product workflow transition manifest has invalid event details")
	}
	switch manifest.Event.Event {
	case string(ProductWorkflowEventTransitionApplied):
	case string(ProductWorkflowEventSourceSatisfiedRecorded):
		if manifest.Event.Source != string(ProductWorkflowEventSourceSatisfiedDelivery) ||
			manifest.Event.Action != "record_source_satisfied_delivery" ||
			strings.TrimSpace(manifest.Event.Details.FromState) == "" ||
			manifest.Event.Details.FromState != manifest.Event.Details.ToState ||
			manifest.Event.Details.Outcome != string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied) ||
			strings.TrimSpace(manifest.Event.Details.SessionID) == "" {
			return fmt.Errorf("product workflow transition manifest has invalid source-satisfied event authority")
		}
		digest := strings.TrimPrefix(strings.TrimSpace(manifest.Event.Details.CompletionDigest), "sha256:")
		if !validWorkflowArtifactDigest(digest) ||
			manifest.Event.EventID != sourceSatisfiedDeliveryEventID(manifest.Event.Details.CompletionDigest) {
			return fmt.Errorf("product workflow transition manifest has invalid source-satisfied event digest identity")
		}
		if _, err := time.Parse(time.RFC3339Nano, manifest.Event.Timestamp); err != nil {
			return fmt.Errorf("product workflow transition manifest has invalid source-satisfied event timestamp: %w", err)
		}
	default:
		return fmt.Errorf("product workflow transition manifest has unsupported event %q", manifest.Event.Event)
	}
	if strings.TrimSpace(manifest.EventsPath) == "" {
		return fmt.Errorf("product workflow transition events path is required")
	}
	cleanPath, ok := normalizedSafeEffortPath(manifest.EventsPath)
	if !ok || cleanPath != filepath.ToSlash(filepath.Clean(filepath.FromSlash(manifest.EventsPath))) {
		return fmt.Errorf("product workflow transition events path must be a normalized safe relative path")
	}
	if err := validateProductWorkflowEventsPathConflicts(cleanPath, nil); err != nil {
		return fmt.Errorf("product workflow transition events path: %w", err)
	}

	return nil
}

func verifyProductWorkflowTransitionState(root *os.Root, manifest productWorkflowTransitionManifest) error {
	body, err := readProductWorkflowTransitionRegularFile(root, WorkflowStateFileName)
	if err != nil {
		return err
	}
	if digest := productWorkflowTransitionDigest(body); digest != manifest.CandidateWorkflowDigest {
		return fmt.Errorf("workflow state digest %s does not match transition candidate %s", digest, manifest.CandidateWorkflowDigest)
	}
	state, err := ParseWorkflowStateJSON(body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.LastEventID) != strings.TrimSpace(manifest.Event.EventID) ||
		state.StateRevision != manifest.Event.Details.StateRevision ||
		state.CurrentState != manifest.Event.Details.ToState ||
		state.EffortID != manifest.Event.EffortID ||
		state.TaskID != manifest.Event.TaskID {
		return fmt.Errorf("workflow state authority does not match the journaled transition event")
	}
	if manifest.Event.Event == string(ProductWorkflowEventSourceSatisfiedRecorded) {
		delivery := state.SourceSatisfiedDelivery
		if delivery == nil ||
			ValidateSourceSatisfiedDeliveryCloseout(*delivery) != nil ||
			state.StatusBucket != WorkflowStatusBucketClosed ||
			state.CurrentPhase != string(PhaseComplete) ||
			state.CurrentState != WorkflowStateClosed ||
			delivery.EffortID != state.EffortID ||
			delivery.TaskID != state.TaskID ||
			delivery.Outcome != plancontract.PlanExecuteTerminalOutcomeSourceSatisfied ||
			delivery.SessionID != manifest.Event.Details.SessionID ||
			delivery.CompletionDigest != manifest.Event.Details.CompletionDigest ||
			delivery.RecordedAt != manifest.Event.Timestamp {
			return fmt.Errorf("workflow state source-satisfied authority does not match the journaled event")
		}
	}
	eventsPath, err := productWorkflowEventsPath(state)
	if err != nil {
		return err
	}
	if filepath.ToSlash(eventsPath) != manifest.EventsPath {
		return fmt.Errorf("workflow state event authority does not match the journaled transition event path")
	}

	return nil
}

func ensureProductWorkflowTransitionEvent(lease *productWorkflowMutationLease, manifest productWorkflowTransitionManifest) error {
	if strings.TrimSpace(manifest.EventsPath) == "" {
		return fmt.Errorf("product workflow transition events path is required")
	}
	root := lease.root
	eventsPath := filepath.FromSlash(manifest.EventsPath)
	exists, body, err := inspectProductWorkflowTransitionEvent(root, eventsPath, manifest.Event)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	eventLine, err := json.Marshal(manifest.Event)
	if err != nil {
		return err
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	body = append(body, eventLine...)
	body = append(body, '\n')
	if productWorkflowTransitionPublicationBoundaryHook != nil {
		productWorkflowTransitionPublicationBoundaryHook(productWorkflowTransitionBeforeEventLedger)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate effort root before transition event publication: %w", err)
	}
	writeErr := atomicfile.WriteFileRootChecked(root, eventsPath, body, 0o644, lease.ValidatePathIdentity)
	if writeErr == nil {
		return nil
	}
	if !artifactWriteCommitted(writeErr) {
		return writeErr
	}
	completed, _, inspectErr := inspectProductWorkflowTransitionEvent(root, eventsPath, manifest.Event)
	if inspectErr != nil {
		return fmt.Errorf("%v; verify committed transition event: %w", writeErr, inspectErr)
	}
	if !completed {
		return fmt.Errorf("%v; committed transition event_id %q is not present", writeErr, manifest.Event.EventID)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("%v; revalidate effort root before transition event sync retry: %w", writeErr, err)
	}
	if syncErr := atomicfile.SyncDirRoot(root, filepath.Dir(eventsPath)); syncErr != nil {
		return fmt.Errorf("%v; retry transition event directory sync: %w", writeErr, syncErr)
	}

	return nil
}

func inspectProductWorkflowTransitionEvent(
	root *os.Root,
	path string,
	expected ProductWorkflowTransitionEvent,
) (bool, []byte, error) {
	body, err := readProductWorkflowTransitionRegularFile(root, path)
	if err != nil {
		return false, nil, err
	}
	matches, err := productWorkflowEventEnvelopesByID(body, expected.EventID)
	if err != nil {
		return false, nil, fmt.Errorf("parse transition event ledger: %w", err)
	}
	expectedBody, err := json.Marshal(expected)
	if err != nil {
		return false, nil, fmt.Errorf("encode expected transition event: %w", err)
	}
	for _, match := range matches {
		if !bytes.Equal(match.raw, expectedBody) {
			return false, nil, fmt.Errorf("transition event_id %q already belongs to a different event", expected.EventID)
		}
	}
	if len(matches) > 1 {
		return false, nil, fmt.Errorf("transition event_id %q appears more than once", expected.EventID)
	}

	return len(matches) > 0, body, nil
}

func readProductWorkflowTransitionRegularFile(root *os.Root, path string) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("transition path %s must be a regular file and must not be a symlink", filepath.ToSlash(path))
	}

	return root.ReadFile(path)
}

func removeProductWorkflowTransitionTransaction(lease *productWorkflowMutationLease) error {
	root := lease.root
	info, err := root.Lstat(productWorkflowTransitionTransactionRelPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("product workflow transition transaction path must be a directory and must not be a symlink")
	}
	if productWorkflowTransitionPublicationBoundaryHook != nil {
		productWorkflowTransitionPublicationBoundaryHook(productWorkflowTransitionBeforeTransactionCleanup)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate effort root before transition journal cleanup: %w", err)
	}
	if err := root.RemoveAll(productWorkflowTransitionTransactionRelPath); err != nil {
		return err
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate effort root before transition journal cleanup sync: %w", err)
	}

	return atomicfile.SyncDirRoot(root, ".")
}

func rejectTransitionSymlinkRoot(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", filepath.Base(path))
	}

	return nil
}
