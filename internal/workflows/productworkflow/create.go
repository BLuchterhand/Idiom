package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/atomicfile"
)

// Phase constants for the artifact creation workflow.
const (
	PhaseInit         = "init"
	PhasePRDDrafting  = "prd_drafting"
	PhasePRDReview    = "prd_review"
	PhaseADRDrafting  = "adr_drafting"
	PhaseADRReview    = "adr_review"
	PhaseTDDSynthesis = "tdd_synthesis"
	PhaseComplete     = "complete"
)

// validNextCreationPhases constrains phase transitions. A phase not in this
// map has no valid next phase (e.g. PhaseComplete is terminal).
// PhaseInit is defined for external compatibility but never written to disk
// by InitializeEffort, which starts directly at PhasePRDDrafting.
var validNextCreationPhases = map[string][]string{
	PhasePRDDrafting:  {PhasePRDReview},
	PhasePRDReview:    {PhaseADRDrafting},
	PhaseADRDrafting:  {PhaseADRReview},
	PhaseADRReview:    {PhaseTDDSynthesis},
	PhaseTDDSynthesis: {PhaseComplete},
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
	CurrentPhase    string                `json:"current_phase"`
	Blockers        []string              `json:"blockers,omitempty"`
	NextValidAction string                `json:"next_valid_action"`
}

// InitializeEffort creates the effort directory structure and a
// workflow_state.json at PhasePRDDrafting. If the effort root already exists
// and has a valid workflow_state.json, it returns the current phase without
// overwriting.
func InitializeEffort(opts CreateEffortOptions) (CreateResult, error) {
	absRoot, err := filepath.Abs(opts.EffortRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: opts.EffortRoot}, fmt.Errorf("resolve effort root: %w", err)
	}

	if existing, ok := loadExistingCreatePhase(absRoot); ok {
		return existing, nil
	}

	effortID := strings.TrimSpace(opts.EffortID)
	if effortID == "" {
		effortID = filepath.Base(absRoot)
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = effortID
	}

	dirs := []string{
		absRoot,
		filepath.Join(absRoot, "adrs"),
		filepath.Join(absRoot, "evidence"),
		filepath.Join(absRoot, "briefs"),
		filepath.Join(absRoot, "reports"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("create directory %s: %w", d, err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ws := WorkflowState{
		SchemaVersion: "idiom.product_workflow_state.v1",
		EffortID:      effortID,
		TaskID:        effortID,
		Title:         title,
		CreatedAt:     now,
		UpdatedAt:     now,
		StateRevision: 0,
		LastEventID:   "evt-create-init-" + now,
		StatusBucket:  WorkflowStatusBucketStarted,
		CurrentPhase:  PhasePRDDrafting,
		CurrentState:  WorkflowStatePlanned,
		ArtifactPaths: map[string]string{
			"prd": "prd.md",
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
		NextValidActions: []string{"approve_prd", "edit_requirements"},
	}

	if err := atomicWriteWorkflowState(absRoot, ws); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("write %s: %w", WorkflowStateFileName, err)
	}

	return CreateResult{
		Status:          ProductWorkflowStatusOK,
		EffortRoot:      absRoot,
		EffortID:        effortID,
		CurrentPhase:    PhasePRDDrafting,
		NextValidAction: "approve_prd",
	}, nil
}

// AdvanceCreatePhase writes a phase transition to workflow_state.json.
// It rejects transitions not in validNextCreationPhases.
func AdvanceCreatePhase(absRoot string, nextPhase string) (CreateResult, error) {
	ws, err := LoadWorkflowState(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("load workflow state: %w", err)
	}

	allowed, ok := validNextCreationPhases[ws.CurrentPhase]
	if !ok {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot},
			fmt.Errorf("cannot advance from terminal phase %q", ws.CurrentPhase)
	}

	found := false

	for _, p := range allowed {
		if p == nextPhase {
			found = true
			break
		}
	}

	if !found {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase,
			Blockers:     []string{"cannot transition from " + ws.CurrentPhase + " to " + nextPhase},
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	ws.UpdatedAt = now
	ws.StateRevision++

	ws.CurrentPhase = nextPhase
	ws.LastEventID = "evt-create-phase-" + nextPhase + "-" + now

	switch nextPhase {
	case PhasePRDDrafting:
		ws.CurrentState = WorkflowStatePlanned
		ws.NextValidActions = []string{"approve_prd", "edit_requirements"}
	case PhasePRDReview:
		ws.CurrentState = WorkflowStateHumanVerification
		ws.NextValidActions = []string{"approve_prd", "request_prd_changes"}
	case PhaseADRDrafting:
		ws.CurrentState = WorkflowStatePlanned
		ws.NextValidActions = []string{"approve_adrs", "edit_adrs"}
	case PhaseADRReview:
		ws.CurrentState = WorkflowStateHumanVerification
		ws.NextValidActions = []string{"approve_adrs", "request_adr_changes"}
	case PhaseTDDSynthesis:
		ws.CurrentState = WorkflowStatePlanned
		ws.NextValidActions = []string{"finalize"}
	case PhaseComplete:
		ws.StatusBucket = WorkflowStatusBucketClosed
		ws.CurrentState = WorkflowStateClosed
		ws.NextValidActions = []string{"view_status"}
	}

	if err := atomicWriteWorkflowState(absRoot, ws); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("write %s: %w", WorkflowStateFileName, err)
	}

	return CreateResult{
		Status:          ProductWorkflowStatusOK,
		EffortRoot:      absRoot,
		EffortID:        ws.EffortID,
		CurrentPhase:    ws.CurrentPhase,
		NextValidAction: firstAction(ws.NextValidActions),
	}, nil
}

// ApprovePRDGate runs the PRD intake validator and advances to PhaseADRDrafting.
func ApprovePRDGate(absRoot string) (CreateResult, IntakeResult, error) {
	ws, err := LoadWorkflowState(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, IntakeResult{}, fmt.Errorf("load workflow state: %w", err)
	}

	if ws.CurrentPhase != PhasePRDReview {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase,
			Blockers:     []string{"current phase " + ws.CurrentPhase + " is not prd_review"},
		}, IntakeResult{}, nil
	}

	prdPath := filepath.Join(absRoot, "prd.md")

	data, err := os.ReadFile(prdPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CreateResult{
				Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
				CurrentPhase: ws.CurrentPhase, Blockers: []string{"prd.md not found"},
			}, IntakeResult{}, nil
		}

		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, IntakeResult{}, fmt.Errorf("read prd.md: %w", err)
	}

	prd, err := ParsePRDMarkdown(data)
	if err != nil {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase, Blockers: []string{"prd.md parse error: " + err.Error()},
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
			CurrentPhase: ws.CurrentPhase, Blockers: blockers,
		}, intake, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	prd.FrontMatter.Status = string(ArtifactStateAccepted)
	prd.FrontMatter.AcceptedAt = now
	prd.FrontMatter.AcceptedBy = "operator"
	prd.FrontMatter.AcceptanceProfile = "product_workflow_create"

	if err := WritePRDMarkdown(absRoot, prd); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, intake, fmt.Errorf("write accepted prd.md: %w", err)
	}

	result, err := AdvanceCreatePhase(absRoot, PhaseADRDrafting)

	return result, intake, err
}

// ApproveADRGate runs the ADR intake validators and advances to
// PhaseTDDSynthesis. All ADRs are validated before any are written to avoid
// partial acceptance on late failures.
func ApproveADRGate(absRoot string) (CreateResult, []IntakeResult, error) {
	ws, err := LoadWorkflowState(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil, fmt.Errorf("load workflow state: %w", err)
	}

	if ws.CurrentPhase != PhaseADRReview {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase,
			Blockers:     []string{"current phase " + ws.CurrentPhase + " is not adr_review"},
		}, nil, nil
	}

	var allIntakes []IntakeResult

	// Load and validate ADR index.
	idxPath := filepath.Join(absRoot, "adrs", "index.json")

	idxData, err := os.ReadFile(idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CreateResult{
				Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
				CurrentPhase: ws.CurrentPhase, Blockers: []string{"adrs/index.json not found"},
			}, nil, nil
		}

		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, nil, fmt.Errorf("read adrs/index.json: %w", err)
	}

	idx, err := ParseADRIndexJSON(idxData)
	if err != nil {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase, Blockers: []string{"adrs/index.json parse error: " + err.Error()},
		}, nil, nil
	}

	idxIntake := ValidateADRIndexIntake(idx)

	allIntakes = append(allIntakes, idxIntake)

	if !idxIntake.Passed {
		blockers := intakeFailureBlockers(idxIntake)

		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase, Blockers: blockers,
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
		adrPath := filepath.Join(absRoot, "adrs", entry.Path)

		data, readErr := os.ReadFile(adrPath)
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
			CurrentPhase: ws.CurrentPhase, Blockers: adrBlockers,
		}, allIntakes, nil
	}

	// Phase 2: all passed — write accepted status to every ADR.
	now := time.Now().UTC().Format(time.RFC3339)

	for _, ar := range adrResults {
		ar.doc.FrontMatter.Status = string(ArtifactStateAccepted)
		ar.doc.FrontMatter.AcceptedAt = now
		ar.doc.FrontMatter.AcceptedBy = "operator"
		ar.doc.FrontMatter.AcceptanceProfile = "product_workflow_create"

		if writeErr := WriteADRMarkdown(absRoot, ar.entry.Path, ar.doc); writeErr != nil {
			return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, allIntakes,
				fmt.Errorf("write %s: %w", ar.entry.Path, writeErr)
		}
	}

	// Mark index entries accepted.
	for i := range idx.ADRs {
		idx.ADRs[i].Status = string(ArtifactStateAccepted)
	}

	if err := WriteADRIndexJSON(absRoot, idx); err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, allIntakes,
			fmt.Errorf("write adrs/index.json: %w", err)
	}

	result, err := AdvanceCreatePhase(absRoot, PhaseTDDSynthesis)

	return result, allIntakes, err
}

// CompleteEffort advances the effort to PhaseComplete.
func CompleteEffort(absRoot string) (CreateResult, error) {
	ws, err := LoadWorkflowState(absRoot)
	if err != nil {
		return CreateResult{Status: ProductWorkflowStatusError, EffortRoot: absRoot}, fmt.Errorf("load workflow state: %w", err)
	}

	if ws.CurrentPhase != PhaseTDDSynthesis {
		return CreateResult{
			Status: ProductWorkflowStatusBlocked, EffortRoot: absRoot, EffortID: ws.EffortID,
			CurrentPhase: ws.CurrentPhase,
			Blockers:     []string{"cannot complete from phase " + ws.CurrentPhase},
		}, nil
	}

	return AdvanceCreatePhase(absRoot, PhaseComplete)
}

// WritePRDMarkdown serializes a PRDDocument to prd.md.
func WritePRDMarkdown(absRoot string, doc PRDDocument) error {
	content := RenderPRDMarkdown(doc)
	return os.WriteFile(filepath.Join(absRoot, "prd.md"), []byte(content), 0o644)
}

// WriteADRMarkdown serializes an ADRDocument.
func WriteADRMarkdown(absRoot, relPath string, doc ADRDocument) error {
	fullPath := filepath.Join(absRoot, "adrs", relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}

	content := RenderADRMarkdown(doc)

	return os.WriteFile(fullPath, []byte(content), 0o644)
}

// WriteADRIndexJSON serializes an ADRIndex to adrs/index.json.
func WriteADRIndexJSON(absRoot string, idx ADRIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ADR index: %w", err)
	}

	data = append(data, '\n')

	return os.WriteFile(filepath.Join(absRoot, "adrs", "index.json"), data, 0o644)
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

func loadExistingCreatePhase(absRoot string) (CreateResult, bool) {
	wsPath := filepath.Join(absRoot, WorkflowStateFileName)

	data, err := os.ReadFile(wsPath)
	if err != nil {
		return CreateResult{}, false
	}

	ws, err := ParseWorkflowStateJSON(data)
	if err != nil {
		return CreateResult{}, false
	}

	switch ws.CurrentPhase {
	case PhasePRDDrafting, PhasePRDReview, PhaseADRDrafting, PhaseADRReview, PhaseTDDSynthesis, PhaseComplete:
		return CreateResult{
			Status:          ProductWorkflowStatusResumed,
			EffortRoot:      absRoot,
			EffortID:        ws.EffortID,
			CurrentPhase:    ws.CurrentPhase,
			NextValidAction: firstAction(ws.NextValidActions),
		}, true
	default:
		return CreateResult{}, false
	}
}

// atomicWriteWorkflowState writes workflow_state.json through the shared
// atomic writer after applying product-workflow JSON formatting.
func atomicWriteWorkflowState(absRoot string, ws WorkflowState) error {
	workflowPath := filepath.Join(absRoot, WorkflowStateFileName)

	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", WorkflowStateFileName, err)
	}

	data = append(data, '\n')

	if err := atomicfile.WriteFile(workflowPath, data, 0o644); err != nil {
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
