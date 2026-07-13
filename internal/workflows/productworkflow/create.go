package productworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/atomicfile"
)

// CreationPhase identifies a valid phase in the artifact creation lifecycle.
type CreationPhase string

// Phase constants for the artifact creation workflow.
const (
	PhasePRDDrafting  CreationPhase = "prd_drafting"
	PhasePRDReview    CreationPhase = "prd_review"
	PhaseADRDrafting  CreationPhase = "adr_drafting"
	PhaseADRReview    CreationPhase = "adr_review"
	PhaseTDDSynthesis CreationPhase = "tdd_synthesis"
	PhaseComplete     CreationPhase = "complete"
)

type creationTransitionAuthority uint8

const (
	creationTransitionNoGate creationTransitionAuthority = iota
	creationTransitionPRDApproval
	creationTransitionADRApproval
)

type creationPhaseTransition struct {
	Next              CreationPhase
	RequiredAuthority creationTransitionAuthority
}

// validNextCreationPhases constrains phase transitions and records which
// forward edges are owned by approval gates. A phase not in this map has no
// valid next phase (e.g. PhaseComplete is terminal).
var validNextCreationPhases = map[CreationPhase]creationPhaseTransition{
	PhasePRDDrafting:  {Next: PhasePRDReview, RequiredAuthority: creationTransitionNoGate},
	PhasePRDReview:    {Next: PhaseADRDrafting, RequiredAuthority: creationTransitionPRDApproval},
	PhaseADRDrafting:  {Next: PhaseADRReview, RequiredAuthority: creationTransitionNoGate},
	PhaseADRReview:    {Next: PhaseTDDSynthesis, RequiredAuthority: creationTransitionADRApproval},
	PhaseTDDSynthesis: {Next: PhaseComplete, RequiredAuthority: creationTransitionNoGate},
}

var createChangesTargetPhases = map[CreationPhase]CreationPhase{
	PhasePRDReview: PhasePRDDrafting,
	PhaseADRReview: PhaseADRDrafting,
}

type creationPhaseProjection struct {
	StatusBucket     string
	CurrentState     string
	NextValidActions []string
}

type creationTransitionEvent string

const (
	creationTransitionAdvanced          creationTransitionEvent = "phase"
	creationTransitionPRDApproved       creationTransitionEvent = "prd-approved"
	creationTransitionADRApproved       creationTransitionEvent = "adr-approved"
	creationTransitionChangesRequested  creationTransitionEvent = "request-changes"
	creationTransitionSynthesisRecovery creationTransitionEvent = "tdd-synthesis-recovery"
)

var creationPhaseProjections = map[CreationPhase]creationPhaseProjection{
	PhasePRDDrafting: {
		StatusBucket:     WorkflowStatusBucketStarted,
		CurrentState:     WorkflowStatePlanned,
		NextValidActions: []string{"approve_prd", "edit_requirements"},
	},
	PhasePRDReview: {
		StatusBucket:     WorkflowStatusBucketStarted,
		CurrentState:     WorkflowStateHumanVerification,
		NextValidActions: []string{"approve_prd", "request_prd_changes"},
	},
	PhaseADRDrafting: {
		StatusBucket:     WorkflowStatusBucketStarted,
		CurrentState:     WorkflowStatePlanned,
		NextValidActions: []string{"approve_adrs", "edit_adrs"},
	},
	PhaseADRReview: {
		StatusBucket:     WorkflowStatusBucketStarted,
		CurrentState:     WorkflowStateHumanVerification,
		NextValidActions: []string{"approve_adrs", "request_adr_changes"},
	},
	PhaseTDDSynthesis: {
		StatusBucket:     WorkflowStatusBucketStarted,
		CurrentState:     WorkflowStatePlanned,
		NextValidActions: []string{"finalize"},
	},
	PhaseComplete: {
		StatusBucket:     WorkflowStatusBucketClosed,
		CurrentState:     WorkflowStateClosed,
		NextValidActions: []string{"view_status"},
	},
}

// CreateEffortOptions holds the inputs for InitializeEffort.
type CreateEffortOptions struct {
	EffortRoot string
	EffortID   string
	Title      string
}

// CreateResult reports the outcome of an effort creation or phase transition.
type CreateResult struct {
	Status          ProductWorkflowStatus `json:"status"`
	EffortRoot      string                `json:"effort_root"`
	EffortID        string                `json:"effort_id"`
	CurrentPhase    CreationPhase         `json:"current_phase"`
	Blockers        []string              `json:"blockers,omitempty"`
	NextValidAction string                `json:"next_valid_action"`
}

type existingCreateStateStatus uint8

const (
	existingCreateStateAbsent existingCreateStateStatus = iota
	existingCreateStateResumable
)

type existingCreateState struct {
	Status existingCreateStateStatus
	Result CreateResult
}

// InitializeEffort creates the effort directory structure and a
// workflow_state.json at PhasePRDDrafting. If the effort root already exists
// and has a valid workflow_state.json, it returns the current phase without
// overwriting.
func InitializeEffort(opts CreateEffortOptions) (CreateResult, error) {
	absRoot, err := filepath.Abs(opts.EffortRoot)
	if err != nil {
		return CreateResult{EffortRoot: opts.EffortRoot}, fmt.Errorf("resolve effort root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return CreateResult{EffortRoot: absRoot}, fmt.Errorf("create effort root: %w", err)
	}
	lease, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{EffortRoot: absRoot}, fmt.Errorf("acquire effort initialization lease: %w", err)
	}
	defer lease.Close()

	existing, err := loadExistingCreateStateRoot(lease.root, absRoot)
	if err != nil {
		return CreateResult{EffortRoot: absRoot}, err
	}
	if existing.Status == existingCreateStateResumable {
		return existing.Result, nil
	}

	effortID := strings.TrimSpace(opts.EffortID)
	if effortID == "" {
		effortID = filepath.Base(absRoot)
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = effortID
	}

	if err := lease.ValidatePathIdentity(); err != nil {
		return CreateResult{EffortRoot: absRoot}, fmt.Errorf("validate effort initialization root: %w", err)
	}
	for _, relPath := range []string{"adrs", "evidence", "briefs", "reports"} {
		info, statErr := lease.root.Lstat(relPath)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := lease.root.Mkdir(relPath, 0o755); err != nil {
				return CreateResult{EffortRoot: absRoot}, fmt.Errorf("create directory %s: %w", filepath.Join(absRoot, relPath), err)
			}
			if err := atomicfile.SyncDirRoot(lease.root, "."); err != nil {
				return CreateResult{EffortRoot: absRoot}, fmt.Errorf("sync directory %s: %w", filepath.Join(absRoot, relPath), err)
			}
			info, statErr = lease.root.Lstat(relPath)
		}
		if statErr != nil {
			return CreateResult{EffortRoot: absRoot}, fmt.Errorf("inspect directory %s: %w", filepath.Join(absRoot, relPath), statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return CreateResult{EffortRoot: absRoot}, fmt.Errorf("effort directory %s must be a directory and must not be a symlink", filepath.Join(absRoot, relPath))
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	projection := creationPhaseProjections[PhasePRDDrafting]
	ws := WorkflowState{
		SchemaVersion: "idiom.product_workflow_state.v1",
		EffortID:      effortID,
		TaskID:        effortID,
		Title:         title,
		CreatedAt:     now,
		UpdatedAt:     now,
		StateRevision: 0,
		LastEventID:   "evt-create-init-" + now,
		StatusBucket:  projection.StatusBucket,
		CurrentPhase:  string(PhasePRDDrafting),
		CurrentState:  projection.CurrentState,
		ArtifactPaths: map[string]string{
			"prd":    "prd.md",
			"events": productWorkflowDefaultEventsPath,
		},
		Context: WorkflowContext{
			ContextPackID:  "create-" + effortID,
			FreshnessState: "fresh",
		},
		Coverage: CoverageState{
			CoverageTier: "unit",
		},
		Privacy: PrivacyState{
			RedactionStatus: "clean",
		},
		DurabilityLevel:  "standard",
		FolderPath:       filepath.ToSlash(absRoot),
		NextValidActions: append([]string(nil), projection.NextValidActions...),
	}

	if err := lease.ValidatePathIdentity(); err != nil {
		return CreateResult{EffortRoot: absRoot, EffortID: effortID}, fmt.Errorf("validate effort initialization root: %w", err)
	}
	eventsPath, err := productWorkflowEventsPath(ws)
	if err != nil {
		return CreateResult{EffortRoot: absRoot, EffortID: effortID}, fmt.Errorf("resolve effort event ledger: %w", err)
	}
	if _, present, err := readWorkflowArtifactSnapshot(lease.root, eventsPath); err != nil {
		return CreateResult{EffortRoot: absRoot, EffortID: effortID}, fmt.Errorf("inspect effort event ledger: %w", err)
	} else if !present {
		if err := writeEffortArtifactAtomicRoot(lease.root, eventsPath, nil, 0o644); err != nil {
			return CreateResult{EffortRoot: absRoot, EffortID: effortID}, fmt.Errorf("initialize effort event ledger: %w", err)
		}
	}
	if err := atomicWriteWorkflowStateRoot(lease.root, ws); err != nil {
		return CreateResult{EffortRoot: absRoot, EffortID: effortID}, fmt.Errorf("write %s: %w", WorkflowStateFileName, err)
	}

	return CreateResult{
		Status:          ProductWorkflowStatusOK,
		EffortRoot:      absRoot,
		EffortID:        effortID,
		CurrentPhase:    PhasePRDDrafting,
		NextValidAction: firstAction(projection.NextValidActions),
	}, nil
}

// SubmitPRDForReview moves a completed PRD draft to its review gate.
func SubmitPRDForReview(absRoot string) (CreateResult, error) {
	return advanceCreatePhase(absRoot, PhasePRDReview, creationTransitionNoGate, creationTransitionAdvanced)
}

// SubmitADRsForReview moves a completed ADR draft generation to its review gate.
func SubmitADRsForReview(absRoot string) (CreateResult, error) {
	return advanceCreatePhase(absRoot, PhaseADRReview, creationTransitionNoGate, creationTransitionAdvanced)
}

func advanceCreatePhase(
	absRoot string,
	nextPhase CreationPhase,
	authority creationTransitionAuthority,
	event creationTransitionEvent,
) (CreateResult, error) {
	batch, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("acquire creation phase lease: %w", err)
	}
	defer batch.Close()

	ws, currentPhase, err := inspectWorkflowAuthority(batch.root)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, err
	}

	transition, ok := validNextCreationPhases[currentPhase]
	if !ok {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot},
			fmt.Errorf("cannot advance from terminal phase %q", currentPhase)
	}

	if transition.Next != nextPhase {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase,
			Blockers:     []string{fmt.Sprintf("cannot transition from %s to %s", currentPhase, nextPhase)},
		}, nil
	}
	if transition.RequiredAuthority != authority {
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        []string{creationTransitionAuthorityBlocker(currentPhase, nextPhase, transition.RequiredAuthority)},
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}

	return commitCreationPhaseTransition(batch, ws, nextPhase, event)
}

func creationTransitionAuthorityBlocker(
	currentPhase CreationPhase,
	nextPhase CreationPhase,
	authority creationTransitionAuthority,
) string {
	gate := "the owning approval gate"
	switch authority {
	case creationTransitionPRDApproval:
		gate = "ApprovePRDGate"
	case creationTransitionADRApproval:
		gate = "ApproveADRGate"
	}

	return fmt.Sprintf("transition from %s to %s requires %s", currentPhase, nextPhase, gate)
}

// RequestCreateChanges performs the only supported backward creation
// transitions: PRD review to PRD drafting and ADR review to ADR drafting.
func RequestCreateChanges(absRoot string) (CreateResult, error) {
	batch, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("acquire creation changes lease: %w", err)
	}
	defer batch.Close()

	ws, currentPhase, err := inspectWorkflowAuthority(batch.root)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, err
	}
	nextPhase, ok := createChangesTargetPhases[currentPhase]
	if !ok {
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        []string{fmt.Sprintf("cannot request creation changes from %s", currentPhase)},
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}
	if currentPhase == PhaseADRReview {
		indexBody, err := readEffortArtifactRoot(batch.root, "adrs/index.json")
		if err != nil {
			return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("read ADR index before requesting changes: %w", err)
		}
		index, err := ParseADRIndexJSON(indexBody)
		if err != nil {
			return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("parse ADR index before requesting changes: %w", err)
		}
		for _, entry := range index.ADRs {
			if strings.TrimSpace(entry.Status) != string(ArtifactStateDraft) {
				return CreateResult{
					Status:          ProductWorkflowStatusBlocked,
					EffortRoot:      absRoot,
					EffortID:        ws.EffortID,
					CurrentPhase:    currentPhase,
					Blockers:        []string{"ADR review generation is no longer draft; finish or recover the owning approval transition before requesting new changes"},
					NextValidAction: "approve_adrs",
				}, nil
			}
		}
	}
	if currentPhase == PhasePRDReview {
		generation, body, _, err := inspectPRDGeneration(batch.root)
		if err != nil {
			return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("inspect PRD before requesting changes: %w", err)
		}
		if generation.generation.state == workflowArtifactGenerationPresent {
			prd, parseErr := ParsePRDMarkdown(body)
			if parseErr == nil && strings.TrimSpace(prd.FrontMatter.Status) != string(ArtifactStateDraft) {
				return CreateResult{
					Status:          ProductWorkflowStatusBlocked,
					EffortRoot:      absRoot,
					EffortID:        ws.EffortID,
					CurrentPhase:    currentPhase,
					Blockers:        []string{"PRD review generation is no longer draft; finish or recover the owning approval transition before requesting new changes"},
					NextValidAction: "approve_prd",
				}, nil
			}
		}
	}

	return commitCreationPhaseTransition(batch, ws, nextPhase, creationTransitionChangesRequested)
}

// RecoverTDDSynthesis returns a failed or blocked TDD synthesis/planning phase
// to ADR drafting so durable workflow state and the interactive drafting route
// remain aligned.
func RecoverTDDSynthesis(absRoot string) (CreateResult, error) {
	batch, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("acquire TDD recovery lease: %w", err)
	}
	defer batch.Close()

	ws, currentPhase, err := inspectWorkflowAuthority(batch.root)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, err
	}
	if currentPhase != PhaseTDDSynthesis {
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        []string{fmt.Sprintf("cannot recover TDD synthesis from %s", currentPhase)},
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}

	return commitCreationPhaseTransition(batch, ws, PhaseADRDrafting, creationTransitionSynthesisRecovery)
}

func commitCreationPhaseTransition(
	lease *productWorkflowMutationLease,
	ws WorkflowState,
	nextPhase CreationPhase,
	event creationTransitionEvent,
) (CreateResult, error) {
	absRoot := ""
	if lease != nil {
		absRoot = lease.effortRoot
	}
	projection, ok := creationPhaseProjections[nextPhase]
	if !ok {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot},
			fmt.Errorf("unknown target creation phase %q", nextPhase)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ws.UpdatedAt = now
	ws.StateRevision++
	ws.LastEventID = fmt.Sprintf("evt-create-%s-%s-%s", event, nextPhase, now)
	ws.StatusBucket = projection.StatusBucket
	ws.CurrentPhase = string(nextPhase)
	ws.CurrentState = projection.CurrentState
	ws.NextValidActions = append([]string(nil), projection.NextValidActions...)

	if err := lease.ValidatePathIdentity(); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("validate creation transition effort root: %w", err)
	}
	if err := atomicWriteWorkflowStateRoot(lease.root, ws); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("write %s: %w", WorkflowStateFileName, err)
	}

	return CreateResult{
		Status:          ProductWorkflowStatusOK,
		EffortRoot:      absRoot,
		EffortID:        ws.EffortID,
		CurrentPhase:    nextPhase,
		NextValidAction: firstAction(ws.NextValidActions),
	}, nil
}

func creationPhaseFromStored(value string) (CreationPhase, bool) {
	phase := CreationPhase(strings.TrimSpace(value))
	_, ok := creationPhaseProjections[phase]

	return phase, ok
}

func validateCreationWorkflowAuthority(ws WorkflowState) (CreationPhase, error) {
	phase, ok := creationPhaseFromStored(ws.CurrentPhase)
	if !ok {
		return "", fmt.Errorf("workflow state has unknown creation phase %q", ws.CurrentPhase)
	}
	projection := creationPhaseProjections[phase]
	if strings.TrimSpace(ws.StatusBucket) != projection.StatusBucket {
		return "", fmt.Errorf(
			"workflow phase %q requires status_bucket %q, got %q",
			phase,
			projection.StatusBucket,
			ws.StatusBucket,
		)
	}
	if strings.TrimSpace(ws.CurrentState) != projection.CurrentState {
		return "", fmt.Errorf(
			"workflow phase %q requires current_state %q, got %q",
			phase,
			projection.CurrentState,
			ws.CurrentState,
		)
	}
	if !equalCreationActions(ws.NextValidActions, projection.NextValidActions) {
		return "", fmt.Errorf(
			"workflow phase %q requires next_valid_actions %v, got %v",
			phase,
			projection.NextValidActions,
			ws.NextValidActions,
		)
	}
	if ws.SourceSatisfiedDelivery != nil && phase != PhaseComplete {
		return "", fmt.Errorf("source-satisfied delivery requires workflow phase %q, got %q", PhaseComplete, phase)
	}

	return phase, nil
}

func equalCreationActions(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if strings.TrimSpace(actual[index]) != expected[index] {
			return false
		}
	}

	return true
}

// ApprovePRDGate runs the PRD intake validator and advances to PhaseADRDrafting.
func ApprovePRDGate(absRoot string) (CreateResult, IntakeResult, error) {
	lease, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, IntakeResult{},
			fmt.Errorf("acquire PRD approval lease: %w", err)
	}
	defer lease.Close()

	generation, data, ws, err := inspectPRDGeneration(lease.root)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, IntakeResult{},
			fmt.Errorf("inspect PRD approval generation: %w", err)
	}
	currentPhase := generation.generation.phase

	if currentPhase != PhasePRDReview {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase,
			Blockers:     []string{fmt.Sprintf("current phase %s is not %s", currentPhase, PhasePRDReview)},
		}, IntakeResult{}, nil
	}

	if generation.generation.state == workflowArtifactGenerationAbsent {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: []string{"prd.md not found"},
		}, IntakeResult{}, nil
	}

	prd, err := ParsePRDMarkdown(data)
	if err != nil {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: []string{"prd.md parse error: " + err.Error()},
		}, IntakeResult{}, nil
	}
	if strings.TrimSpace(prd.FrontMatter.TaskID) != strings.TrimSpace(ws.TaskID) {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase,
			Blockers: []string{fmt.Sprintf(
				"prd.md task_id %q does not match workflow state task_id %q",
				prd.FrontMatter.TaskID,
				ws.TaskID,
			)},
		}, IntakeResult{}, nil
	}

	intake := ValidatePRDIntake(prd)
	if !intake.Passed {
		blockers := make([]string, 0, len(intake.Failures))
		for _, f := range intake.Failures {
			blockers = append(blockers, f.Code+": "+f.Message)
		}

		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: blockers,
		}, intake, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	prd.FrontMatter.Status = string(ArtifactStateAccepted)
	prd.FrontMatter.AcceptedAt = now
	prd.FrontMatter.AcceptedBy = "operator"
	prd.FrontMatter.AcceptanceProfile = "product_workflow_create"

	if err := lease.ValidatePathIdentity(); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, intake,
			fmt.Errorf("validate PRD approval effort root: %w", err)
	}
	if _, err := persistPRDDocument(lease.root, prd); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, intake, fmt.Errorf("write accepted prd.md: %w", err)
	}

	result, err := commitCreationPhaseTransition(
		lease,
		ws,
		PhaseADRDrafting,
		creationTransitionPRDApproved,
	)

	return result, intake, err
}

// ApproveADRGate runs the ADR intake validators and advances to
// PhaseTDDSynthesis. All ADRs are validated before any are written to avoid
// partial acceptance on late failures.
func ApproveADRGate(absRoot string) (CreateResult, []IntakeResult, error) {
	batch, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil,
			fmt.Errorf("recover ADR batch transaction: %w", err)
	}
	batchOpen := true
	defer func() {
		if batchOpen {
			batch.Close()
		}
	}()
	expectedGeneration, err := batch.ADRGeneration()
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil,
			fmt.Errorf("inspect ADR generation: %w", err)
	}

	ws, currentPhase, err := inspectWorkflowAuthority(batch.root)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil, err
	}

	if currentPhase != PhaseADRReview {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase,
			Blockers:     []string{fmt.Sprintf("current phase %s is not %s", currentPhase, PhaseADRReview)},
		}, nil, nil
	}

	var allIntakes []IntakeResult

	// Load and validate ADR index.
	idxData, err := readEffortArtifactRoot(batch.root, "adrs/index.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CreateResult{
				Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
				CurrentPhase: currentPhase, Blockers: []string{"adrs/index.json not found"},
			}, nil, nil
		}

		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil, fmt.Errorf("read adrs/index.json: %w", err)
	}

	idx, err := ParseADRIndexJSON(idxData)
	if err != nil {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: []string{"adrs/index.json parse error: " + err.Error()},
		}, nil, nil
	}

	idxIntake := ValidateADRIndexIntake(idx)

	allIntakes = append(allIntakes, idxIntake)

	if !idxIntake.Passed {
		blockers := intakeFailureBlockers(idxIntake)

		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: blockers,
		}, allIntakes, nil
	}

	// Phase 1: load and validate every ADR, collecting results.
	type adrResult struct {
		entry   ADRIndexEntry
		doc     ADRDocument
		intake  IntakeResult
		blocked bool
		reason  string
	}

	var (
		adrResults  []adrResult
		adrBlockers []string
	)

	for _, entry := range idx.ADRs {
		ar := adrResult{entry: entry}
		entryPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			ar.blocked = true
			ar.reason = "read " + entry.Path + ": ADR index path must be effort-relative and must not contain path traversal"
			adrBlockers = append(adrBlockers, ar.reason)
			allIntakes = append(allIntakes, IntakeResult{})
			adrResults = append(adrResults, ar)

			continue
		}
		data, readErr := readEffortArtifactRoot(batch.root, filepath.Join("adrs", entryPath))
		if readErr != nil {
			ar.blocked = true
			ar.reason = "read " + entry.Path + ": " + readErr.Error()
			adrBlockers = append(adrBlockers, ar.reason)
			allIntakes = append(allIntakes, IntakeResult{})
			adrResults = append(adrResults, ar)

			continue
		}

		doc, parseErr := ParseADRMarkdown(data)
		if parseErr != nil {
			ar.blocked = true
			ar.reason = "parse " + entry.Path + ": " + parseErr.Error()
			adrBlockers = append(adrBlockers, ar.reason)
			allIntakes = append(allIntakes, IntakeResult{})
			adrResults = append(adrResults, ar)

			continue
		}

		ar.doc = doc
		ar.intake = ValidateADRIntake(doc, entry.Path)
		allIntakes = append(allIntakes, ar.intake)

		if !ar.intake.Passed {
			ar.blocked = true
			ar.reason = entry.Path + ": " + strings.Join(intakeFailureBlockers(ar.intake), "; ")
			adrBlockers = append(adrBlockers, intakeFailureBlockers(ar.intake)...)
		}

		adrResults = append(adrResults, ar)
	}

	if len(adrBlockers) > 0 {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase, Blockers: adrBlockers,
		}, allIntakes, nil
	}

	// Phase 2: all passed — prepare the complete accepted write set. The shared
	// persistence use case validates every rendered document, path, and index
	// relationship before it performs the first write.
	now := time.Now().UTC().Format(time.RFC3339)
	acceptedDocuments := make([]ADRDocument, 0, len(adrResults))
	for _, ar := range adrResults {
		ar.doc.FrontMatter.Status = string(ArtifactStateAccepted)
		ar.doc.FrontMatter.AcceptedAt = now
		ar.doc.FrontMatter.AcceptedBy = "operator"
		ar.doc.FrontMatter.AcceptanceProfile = "product_workflow_create"
		acceptedDocuments = append(acceptedDocuments, ar.doc)
	}

	for i := range idx.ADRs {
		idx.ADRs[i].Status = string(ArtifactStateAccepted)
	}

	batch.Close()
	batchOpen = false
	if _, err := persistAcceptedADRs(ADRDraftPersistenceRequest{
		EffortRoot:         absRoot,
		Documents:          acceptedDocuments,
		IndexEntries:       idx.ADRs,
		ExpectedGeneration: expectedGeneration,
	}); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, allIntakes,
			fmt.Errorf("persist accepted ADRs: %w", err)
	}

	result, err := advanceCreatePhase(
		absRoot,
		PhaseTDDSynthesis,
		creationTransitionADRApproval,
		creationTransitionADRApproved,
	)

	return result, allIntakes, err
}

// CompleteEffort advances the effort to PhaseComplete.
func CompleteEffort(absRoot string) (CreateResult, error) {
	lease, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("acquire completion lease: %w", err)
	}
	defer lease.Close()

	recorder := newWorkflowCompletionInputRecorder()
	input, loadBlockers := loadBriefGenerationInputRoot(lease.effortRoot, lease, recorder)
	ws := input.WorkflowState
	currentPhase, ok := creationPhaseFromStored(ws.CurrentPhase)
	if !ok {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("workflow state has unknown creation phase %q", ws.CurrentPhase)
	}
	if currentPhase != PhaseTDDSynthesis {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: currentPhase,
			Blockers:     []string{fmt.Sprintf("cannot complete from phase %s", currentPhase)},
		}, nil
	}
	if blockers := briefGenerationInputParseBlockers(loadBlockers); len(blockers) > 0 {
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        append([]string(nil), blockers...),
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}
	if err := input.adrGeneration.validate(); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("validate completion ADR generation: %w", err)
	}
	if err := input.tddGeneration.validate(); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("validate completion TDD generation: %w", err)
	}
	if CoverageGateHasBlockers(input.CoverageEvaluation) &&
		!CoverageGateHasOnlyTDDEvidenceBlockers(input.CoverageEvaluation) {
		blockers := coverageBlockerMessages(input.CoverageEvaluation)
		if len(blockers) == 0 {
			blockers = []string{"coverage gate evaluation is blocked"}
		}
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        blockers,
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}
	if err := recorder.Verify(lease.root); err != nil {
		return CreateResult{
			Status:          ProductWorkflowStatusBlocked,
			EffortRoot:      lease.effortRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    currentPhase,
			Blockers:        []string{err.Error() + "; reload and retry"},
			NextValidAction: firstAction(ws.NextValidActions),
		}, nil
	}

	return commitCreationPhaseTransition(lease, ws, PhaseComplete, creationTransitionAdvanced)
}

// RenderPRDMarkdown produces markdown matching the ParsePRDMarkdown expected format.
func RenderPRDMarkdown(doc PRDDocument) string {
	var b strings.Builder

	b.WriteString("---\n")

	fm := doc.FrontMatter
	if fm.SchemaVersion != "" {
		b.WriteString("schema_version: " + fm.SchemaVersion + "\n")
	}

	if fm.PRDID != "" {
		b.WriteString("prd_id: " + fm.PRDID + "\n")
	}

	if fm.Status != "" {
		b.WriteString("status: " + fm.Status + "\n")
	}

	if fm.TaskID != "" {
		b.WriteString("task_id: " + fm.TaskID + "\n")
	}

	if fm.CreatedAt != "" {
		b.WriteString("created_at: " + fm.CreatedAt + "\n")
	}

	if fm.AcceptedAt != "" {
		b.WriteString("accepted_at: " + fm.AcceptedAt + "\n")
	}

	if fm.AcceptedBy != "" {
		b.WriteString("accepted_by: " + fm.AcceptedBy + "\n")
	}

	if fm.AcceptanceProfile != "" {
		b.WriteString("acceptance_profile: " + fm.AcceptanceProfile + "\n")
	}

	b.WriteString("---\n\n")

	for _, req := range doc.Requirements {
		b.WriteString("- **id**: " + req.ID + "\n")
		b.WriteString("- **priority**: " + req.Priority + "\n")
		b.WriteString("- **statement**: " + req.Statement + "\n")
		b.WriteString("- **evidence_refs**: [" + strings.Join(req.EvidenceRefs, ", ") + "]\n")
		b.WriteString("- **tdd_obligations**: [" + strings.Join(req.TDDObligations, ", ") + "]\n")
		b.WriteString("- **owner_alignment**: " + req.OwnerAlignment + "\n")
		b.WriteString("- **testability**: " + req.Testability + "\n")
		b.WriteString("- **metric**: " + req.Metric + "\n")

		if req.OpenQuestionsPresent && len(req.OpenQuestions) > 0 {
			b.WriteString("- **open_questions**: [" + strings.Join(req.OpenQuestions, ", ") + "]\n")
		} else {
			b.WriteString("- **open_questions**: []\n")
		}

		b.WriteString("\n")
	}

	return b.String()
}

// RenderADRMarkdown produces a valid ADR markdown document with all required
// sections. Parsed ADRs preserve BodyMarkdown so workflow round-trips keep the
// model's substantive decision text while still allowing metadata-only tests and
// template creation to fall back to the canonical empty section scaffold.
func RenderADRMarkdown(doc ADRDocument) string {
	var b strings.Builder

	b.WriteString("---\n")

	fm := doc.FrontMatter
	if fm.SchemaVersion != "" {
		b.WriteString("schema_version: " + fm.SchemaVersion + "\n")
	}

	if fm.ADRID != "" {
		b.WriteString("adr_id: " + fm.ADRID + "\n")
	}

	if fm.Status != "" {
		b.WriteString("status: " + fm.Status + "\n")
	}

	if len(fm.PRDRefs) > 0 {
		b.WriteString("prd_refs:\n")

		for _, ref := range fm.PRDRefs {
			b.WriteString("  - " + ref + "\n")
		}
	}

	if len(fm.EvidenceRefs) > 0 {
		b.WriteString("evidence_refs:\n")

		for _, ref := range fm.EvidenceRefs {
			b.WriteString("  - " + ref + "\n")
		}
	}

	if len(fm.TDDObligations) > 0 {
		b.WriteString("tdd_obligations:\n")

		for _, ref := range fm.TDDObligations {
			b.WriteString("  - " + ref + "\n")
		}
	}

	if fm.CoverageTier != "" {
		b.WriteString("coverage_tier: " + fm.CoverageTier + "\n")
	}

	if fm.AcceptedAt != "" {
		b.WriteString("accepted_at: " + fm.AcceptedAt + "\n")
	}

	if fm.AcceptedBy != "" {
		b.WriteString("accepted_by: " + fm.AcceptedBy + "\n")
	}

	if fm.AcceptanceProfile != "" {
		b.WriteString("acceptance_profile: " + fm.AcceptanceProfile + "\n")
	}

	b.WriteString("---\n\n")

	if body := strings.TrimSpace(doc.BodyMarkdown); body != "" {
		b.WriteString(body)

		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}

		return b.String()
	}

	b.WriteString("## Context\n\n\n\n")
	b.WriteString("## Decision\n\n\n\n")
	b.WriteString("## Alternatives Considered\n\n\n\n")
	b.WriteString("## Evidence\n\n\n\n")
	b.WriteString("## Consequences\n\n\n\n")
	b.WriteString("## Compatibility and Migration\n\n\n\n")
	b.WriteString("## TDD Obligations\n\n\n\n")
	b.WriteString("## Rollback\n\n\n\n")
	b.WriteString("## Open Questions\n\n- \n")

	return b.String()
}

func loadExistingCreateStateRoot(root *os.Root, absRoot string) (existingCreateState, error) {
	data, present, err := readWorkflowArtifactSnapshot(root, WorkflowStateFileName)
	if err != nil {
		return existingCreateState{}, fmt.Errorf("read existing %s: %w", WorkflowStateFileName, err)
	}
	if !present {
		return existingCreateState{Status: existingCreateStateAbsent}, nil
	}

	ws, err := ParseWorkflowStateJSON(data)
	if err != nil {
		return existingCreateState{}, fmt.Errorf("parse existing %s: %w", WorkflowStateFileName, err)
	}

	phase, err := validateCreationWorkflowAuthority(ws)
	if err != nil {
		return existingCreateState{}, fmt.Errorf("existing %s is not valid creation authority: %w", WorkflowStateFileName, err)
	}
	eventsPath, err := productWorkflowEventsPath(ws)
	if err != nil {
		return existingCreateState{}, fmt.Errorf("existing %s has invalid event-ledger authority: %w", WorkflowStateFileName, err)
	}
	if _, present, err := readWorkflowArtifactSnapshot(root, eventsPath); err != nil {
		return existingCreateState{}, fmt.Errorf("read existing event ledger %s: %w", filepath.ToSlash(eventsPath), err)
	} else if !present {
		return existingCreateState{}, fmt.Errorf("existing event ledger %s is required", filepath.ToSlash(eventsPath))
	}

	return existingCreateState{
		Status: existingCreateStateResumable,
		Result: CreateResult{
			Status:          ProductWorkflowStatusResumed,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    phase,
			NextValidAction: firstAction(ws.NextValidActions),
		},
	}, nil
}

// atomicWriteWorkflowStateRoot writes workflow_state.json relative to the root
// inode owned by the surrounding product-workflow transaction.
func atomicWriteWorkflowStateRoot(root *os.Root, ws WorkflowState) error {
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", WorkflowStateFileName, err)
	}

	data = append(data, '\n')

	if err := writeEffortArtifactAtomicRoot(root, WorkflowStateFileName, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", WorkflowStateFileName, err)
	}

	return nil
}

func intakeFailureBlockers(intake IntakeResult) []string {
	blockers := make([]string, 0, len(intake.Failures))
	for _, f := range intake.Failures {
		blockers = append(blockers, f.Code+": "+f.Message)
	}

	return blockers
}

func firstAction(actions []string) string {
	for _, a := range actions {
		if strings.TrimSpace(a) != "" {
			return strings.TrimSpace(a)
		}
	}

	return ""
}
