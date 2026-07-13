package productworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultTDDMutationMinScore = 80

// ErrADRDraftIndexNotFound identifies the absence of adrs/index.json. Callers
// can distinguish initial ADR generation from failures loading an existing ADR
// draft set with errors.Is(err, ErrADRDraftIndexNotFound).
var ErrADRDraftIndexNotFound = fmt.Errorf("ADR draft index not found: %w", os.ErrNotExist)

type ADRDraftPersistenceRequest struct {
	EffortRoot         string
	Documents          []ADRDocument
	IndexEntries       []ADRIndexEntry
	ExpectedGeneration ADRGenerationToken
}

type ADRDraftPersistenceResult struct {
	Documents    []ADRDocument
	IndexEntries []ADRIndexEntry
}

// PRDDraftIdentity contains stable front-matter values that refinement prompts
// must preserve. TaskID is workflow-owned; PRDID and CreatedAt become
// document-owned once the first valid draft is persisted.
type PRDDraftIdentity struct {
	PRDID     string
	TaskID    string
	CreatedAt string
}

// PRDDraftPersistenceRequest carries a loader-issued generation into the PRD
// persistence use case. The generation is compared under the shared product
// workflow mutation lease before the candidate may replace prd.md.
type PRDDraftPersistenceRequest struct {
	EffortRoot         string
	Document           PRDDocument
	ExpectedGeneration PRDGenerationToken
}

// PRDDraftContext describes the current PRD drafting input. ParseError is set
// when prd.md exists but is malformed, allowing a surface to deliberately
// request a formatting repair without treating an authority read failure as
// absence.
type PRDDraftContext struct {
	Identity         PRDDraftIdentity
	Document         *PRDDocument
	OriginalMarkdown string
	ParseError       error
	Generation       PRDGenerationToken
}

// ADRDraftContext is the validated on-disk PRD and ADR draft set used by
// refinement surfaces. OriginalPRDMarkdown, OriginalIndexJSON, and each
// OriginalMarkdown value preserve the exact source text while PRD, Index, and
// Document provide parsed workflow values.
type ADRDraftContext struct {
	PRD                 PRDDocument
	OriginalPRDMarkdown string
	Index               ADRIndex
	OriginalIndexJSON   []byte
	Documents           []ADRDraftContextDocument
	Generation          ADRGenerationToken
}

// ADRDraftContextDocument binds one parsed ADR to the index entry that named
// its effort-relative path.
type ADRDraftContextDocument struct {
	ADRID            string
	IndexPath        string
	RelativePath     string
	Document         ADRDocument
	OriginalMarkdown string
}

// ADRGenerationContext contains the validated PRD input required to generate
// the first ADR draft set.
type ADRGenerationContext struct {
	PRD                 PRDDocument
	OriginalPRDMarkdown string
	Generation          ADRGenerationToken
}

type TDDLedgerSynthesisResult struct {
	Ledger        TDDLedger
	Plan          TDDObligationSynthesisPlan
	CommitWarning string
}

func PersistPRDDraft(request PRDDraftPersistenceRequest) (IntakeResult, error) {
	if err := request.ExpectedGeneration.validate(); err != nil {
		return IntakeResult{}, fmt.Errorf("validate expected PRD generation: %w", err)
	}

	lease, err := openProductWorkflowMutationLease(request.EffortRoot)
	if err != nil {
		return IntakeResult{}, fmt.Errorf("acquire PRD persistence lease: %w", err)
	}
	defer lease.Close()

	currentGeneration, currentBody, state, err := inspectPRDGeneration(lease.root)
	if err != nil {
		return IntakeResult{}, fmt.Errorf("inspect current PRD generation: %w", err)
	}
	if !request.ExpectedGeneration.equal(currentGeneration) {
		return IntakeResult{}, stalePRDGenerationError(request.ExpectedGeneration, currentGeneration)
	}
	if currentGeneration.generation.phase != PhasePRDDrafting {
		return IntakeResult{}, fmt.Errorf(
			"PRD draft persistence requires workflow phase %q, got %q",
			PhasePRDDrafting,
			currentGeneration.generation.phase,
		)
	}

	context, err := prdDraftContextFromSnapshot(currentGeneration, currentBody, state)
	if err != nil {
		return IntakeResult{}, err
	}
	identity := context.Identity
	prd := request.Document
	if strings.TrimSpace(prd.FrontMatter.Status) != string(ArtifactStateDraft) {
		return IntakeResult{}, fmt.Errorf(
			"PRD draft status must be %q, got %q",
			ArtifactStateDraft,
			prd.FrontMatter.Status,
		)
	}
	if strings.TrimSpace(prd.FrontMatter.AcceptedAt) != "" ||
		strings.TrimSpace(prd.FrontMatter.AcceptedBy) != "" ||
		strings.TrimSpace(prd.FrontMatter.AcceptanceProfile) != "" {
		return IntakeResult{}, fmt.Errorf("PRD draft must not contain acceptance metadata")
	}
	if strings.TrimSpace(prd.FrontMatter.TaskID) != identity.TaskID {
		return IntakeResult{}, fmt.Errorf(
			"PRD task_id %q does not match workflow state task_id %q",
			prd.FrontMatter.TaskID,
			identity.TaskID,
		)
	}
	if context.Document != nil {
		if strings.TrimSpace(prd.FrontMatter.PRDID) != identity.PRDID {
			return IntakeResult{}, fmt.Errorf(
				"PRD prd_id %q does not match existing draft prd_id %q",
				prd.FrontMatter.PRDID,
				identity.PRDID,
			)
		}
		if strings.TrimSpace(prd.FrontMatter.CreatedAt) != identity.CreatedAt {
			return IntakeResult{}, fmt.Errorf(
				"PRD created_at %q does not match existing draft created_at %q",
				prd.FrontMatter.CreatedAt,
				identity.CreatedAt,
			)
		}
	}

	if err := lease.ValidatePathIdentity(); err != nil {
		return IntakeResult{}, fmt.Errorf("validate PRD persistence effort root: %w", err)
	}
	validated, err := persistPRDDocument(lease.root, prd)
	if err != nil {
		return IntakeResult{}, err
	}

	return ValidatePRDIntake(validated), nil
}

func persistPRDDocument(root *os.Root, prd PRDDocument) (PRDDocument, error) {
	body := []byte(RenderPRDMarkdown(prd))
	validated, err := ParsePRDMarkdown(body)
	if err != nil {
		return PRDDocument{}, fmt.Errorf("validate rendered prd.md: %w", err)
	}
	if err := writeEffortArtifactAtomicRoot(root, "prd.md", body, 0o644); err != nil {
		return PRDDocument{}, err
	}

	return validated, nil
}

// LoadPRDDraftContext loads the workflow task identity and, when present, the
// current document-owned PRD identity. A malformed draft is returned through
// ParseError; missing or unreadable workflow authority still fails closed.
func LoadPRDDraftContext(effortRoot string) (PRDDraftContext, error) {
	lease, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		return PRDDraftContext{}, fmt.Errorf("acquire PRD draft context lease: %w", err)
	}
	defer lease.Close()

	generation, markdown, state, err := inspectPRDGeneration(lease.root)
	if err != nil {
		return PRDDraftContext{}, fmt.Errorf("inspect PRD draft generation: %w", err)
	}
	if generation.generation.phase != PhasePRDDrafting {
		return PRDDraftContext{}, fmt.Errorf(
			"PRD draft persistence requires workflow phase %q, got %q",
			PhasePRDDrafting,
			generation.generation.phase,
		)
	}

	return prdDraftContextFromSnapshot(generation, markdown, state)
}

func prdDraftContextFromSnapshot(
	generation PRDGenerationToken,
	markdown []byte,
	state WorkflowState,
) (PRDDraftContext, error) {
	context := PRDDraftContext{
		Identity: PRDDraftIdentity{
			PRDID:     state.EffortID,
			TaskID:    state.TaskID,
			CreatedAt: state.CreatedAt,
		},
		Generation: generation,
	}
	if generation.generation.state == workflowArtifactGenerationAbsent {
		return context, nil
	}
	context.OriginalMarkdown = string(markdown)

	document, err := ParsePRDMarkdown(markdown)
	if err != nil {
		context.ParseError = fmt.Errorf("parse prd.md: %w", err)

		return context, nil
	}
	if strings.TrimSpace(document.FrontMatter.TaskID) != state.TaskID {
		return PRDDraftContext{}, fmt.Errorf(
			"prd.md task_id %q does not match workflow state task_id %q",
			document.FrontMatter.TaskID,
			state.TaskID,
		)
	}
	context.Identity = PRDDraftIdentity{
		PRDID:     strings.TrimSpace(document.FrontMatter.PRDID),
		TaskID:    state.TaskID,
		CreatedAt: strings.TrimSpace(document.FrontMatter.CreatedAt),
	}
	context.Document = &document

	return context, nil
}

func PersistADRDrafts(request ADRDraftPersistenceRequest) (ADRDraftPersistenceResult, error) {
	return persistADRs(request, adrPersistenceContract{
		phase:  PhaseADRDrafting,
		status: ArtifactStateDraft,
	})
}

type adrPersistenceContract struct {
	phase                     CreationPhase
	status                    ArtifactState
	requireAcceptanceMetadata bool
}

func persistAcceptedADRs(request ADRDraftPersistenceRequest) (ADRDraftPersistenceResult, error) {
	return persistADRs(request, adrPersistenceContract{
		phase:                     PhaseADRReview,
		status:                    ArtifactStateAccepted,
		requireAcceptanceMetadata: true,
	})
}

func persistADRs(
	request ADRDraftPersistenceRequest,
	contract adrPersistenceContract,
) (ADRDraftPersistenceResult, error) {
	if err := request.ExpectedGeneration.validate(); err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("validate expected ADR generation: %w", err)
	}
	lease, err := openProductWorkflowMutationLease(request.EffortRoot)
	if err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("acquire ADR persistence lease: %w", err)
	}
	defer lease.Close()

	state, phase, err := inspectWorkflowAuthority(lease.root)
	if err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("load ADR workflow state: %w", err)
	}
	if phase != contract.phase {
		return ADRDraftPersistenceResult{}, fmt.Errorf(
			"ADR operation requires workflow phase %q, got %q",
			contract.phase,
			phase,
		)
	}
	currentGeneration, err := lease.ADRGeneration()
	if err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("inspect current ADR generation: %w", err)
	}
	if !request.ExpectedGeneration.equal(currentGeneration) {
		return ADRDraftPersistenceResult{}, fmt.Errorf(
			"%w: expected %s generation %q at workflow revision %d/%s/%s, found %s generation %q at workflow revision %d/%s/%s",
			ErrStaleADRGeneration,
			request.ExpectedGeneration.generation.state,
			request.ExpectedGeneration.generation.digest,
			request.ExpectedGeneration.generation.stateRevision,
			request.ExpectedGeneration.generation.lastEventID,
			request.ExpectedGeneration.generation.phase,
			currentGeneration.generation.state,
			currentGeneration.generation.digest,
			currentGeneration.generation.stateRevision,
			currentGeneration.generation.lastEventID,
			currentGeneration.generation.phase,
		)
	}
	existingIndex, previousDocuments, err := loadAuthoritativeADRSetRoot(lease.root, state)
	if err != nil {
		return ADRDraftPersistenceResult{}, err
	}
	if (existingIndex != nil) != currentGeneration.indexPresent {
		return ADRDraftPersistenceResult{}, errors.New("authoritative ADR index presence does not match the locked ADR generation")
	}

	documents := append([]ADRDocument{}, request.Documents...)
	entries := append([]ADRIndexEntry{}, request.IndexEntries...)
	var priorIndex *ADRIndex
	if err := validateADRPersistenceInputIDs("candidate ADR index", entries, documents); err != nil {
		return ADRDraftPersistenceResult{}, err
	}
	if existingIndex != nil {
		if err := validateADRPersistenceInputIDs("existing ADR index", existingIndex.ADRs, previousDocuments); err != nil {
			return ADRDraftPersistenceResult{}, err
		}
		existingCopy := *existingIndex
		existingCopy.ADRs = append([]ADRIndexEntry(nil), existingIndex.ADRs...)
		priorCopy := existingCopy
		priorCopy.ADRs = append([]ADRIndexEntry(nil), existingCopy.ADRs...)
		priorIndex = &priorCopy
		documents, entries = mergeADRDraftIndex(existingCopy, documents, entries)
		documents = preserveADRDraftBodies(documents, previousDocuments)
	}
	tombstones, err := adrPersistenceTombstones(priorIndex, entries)
	if err != nil {
		return ADRDraftPersistenceResult{}, err
	}
	if len(documents) != len(entries) {
		return ADRDraftPersistenceResult{}, fmt.Errorf("ADR document count %d does not match index entry count %d", len(documents), len(entries))
	}
	if err := validateADRPersistenceLifecycle(documents, entries, contract); err != nil {
		return ADRDraftPersistenceResult{}, err
	}

	result := ADRDraftPersistenceResult{Documents: documents, IndexEntries: entries}
	if len(documents) == 0 {
		return result, nil
	}
	adrInput, err := loadADRGenerationContextRoot(lease.root, state)
	if err != nil {
		return ADRDraftPersistenceResult{}, err
	}

	adrIndex := ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      state.EffortID,
		ADRs:          entries,
	}

	draftArtifacts := make([]adrBatchArtifact, 0, len(documents)+1)
	parsedADRs := make([]ParsedADR, 0, len(documents))
	indexRelPath := filepath.Join("adrs", "index.json")
	canonicalIndexPath := filepath.ToSlash(filepath.Clean(indexRelPath))
	indexCollisionKey := strings.ToLower(canonicalIndexPath)
	seenPaths := map[string]string{indexCollisionKey: canonicalIndexPath}
	for index, document := range documents {
		displayPath := entries[index].Path
		cleanPath, ok := cleanEffortRelativePath(displayPath)
		if !ok {
			return ADRDraftPersistenceResult{}, fmt.Errorf("ADR path %q must be relative and must not contain path traversal", displayPath)
		}

		relPath := filepath.Join("adrs", cleanPath)
		canonicalPath := filepath.ToSlash(filepath.Clean(relPath))
		collisionKey := strings.ToLower(canonicalPath)
		for plannedKey, plannedPath := range seenPaths {
			switch {
			case collisionKey == plannedKey:
				if plannedKey == indexCollisionKey {
					return ADRDraftPersistenceResult{}, fmt.Errorf("ADR path %q conflicts with reserved index path %q", displayPath, canonicalIndexPath)
				}
				return ADRDraftPersistenceResult{}, fmt.Errorf("ADR path %q is duplicated", displayPath)
			case strings.HasPrefix(collisionKey, plannedKey+"/") || strings.HasPrefix(plannedKey, collisionKey+"/"):
				return ADRDraftPersistenceResult{}, fmt.Errorf("ADR path %q conflicts with planned path %q", displayPath, plannedPath)
			}
		}
		seenPaths[collisionKey] = canonicalPath

		body := []byte(RenderADRMarkdown(document))
		parsed, err := ParseADRMarkdown(body)
		if err != nil {
			return ADRDraftPersistenceResult{}, fmt.Errorf("validate rendered ADR %s: %w", displayPath, err)
		}
		draftArtifacts = append(draftArtifacts, adrBatchArtifact{
			displayPath: displayPath,
			relPath:     relPath,
			body:        body,
			perm:        0o644,
		})
		parsedADRs = append(parsedADRs, ParsedADR{
			Doc:         parsed,
			RelPath:     canonicalPath,
			RawMarkdown: string(body),
		})
	}
	if linkErrors := ValidateADRCrossLinks(parsedADRs, PRDRequirementIDSet(adrInput.PRD.Requirements), nil, nil); len(linkErrors) > 0 {
		return ADRDraftPersistenceResult{}, fmt.Errorf("validate ADR PRD references: %s", linkErrors[0].Message)
	}

	indexBody, err := json.MarshalIndent(adrIndex, "", "  ")
	if err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("encode adrs/index.json: %w", err)
	}
	indexBody = append(indexBody, '\n')
	validatedIndex, err := ParseADRIndexJSON(indexBody)
	if err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("validate adrs/index.json: %w", err)
	}
	if mismatches := ValidateADRIndexFrontMatterConsistency(validatedIndex, parsedADRs); len(mismatches) > 0 {
		return ADRDraftPersistenceResult{}, fmt.Errorf("validate adrs/index.json: %s", mismatches[0].Message)
	}
	draftArtifacts = append(draftArtifacts, adrBatchArtifact{
		displayPath: "adrs/index.json",
		relPath:     indexRelPath,
		body:        indexBody,
		perm:        0o644,
	})
	if err := commitADRBatch(lease, request.ExpectedGeneration, contract.phase, tombstones, draftArtifacts); err != nil {
		return ADRDraftPersistenceResult{}, fmt.Errorf("commit ADR batch: %w", err)
	}

	return result, nil
}

func loadAuthoritativeADRSetRoot(root *os.Root, state WorkflowState) (*ADRIndex, []ADRDocument, error) {
	const indexRelPath = "adrs/index.json"
	indexBody, present, err := readWorkflowArtifactSnapshot(root, indexRelPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read authoritative %s: %w", indexRelPath, err)
	}
	if !present {
		return nil, nil, nil
	}
	index, err := ParseADRIndexJSON(indexBody)
	if err != nil {
		return nil, nil, fmt.Errorf("parse authoritative %s: %w", indexRelPath, err)
	}
	if strings.TrimSpace(index.EffortID) != strings.TrimSpace(state.EffortID) {
		return nil, nil, fmt.Errorf(
			"authoritative %s effort_id %q does not match workflow state effort_id %q",
			indexRelPath,
			index.EffortID,
			state.EffortID,
		)
	}

	documents := make([]ADRDocument, 0, len(index.ADRs))
	parsed := make([]ParsedADR, 0, len(index.ADRs))
	for _, entry := range index.ADRs {
		cleanPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			return nil, nil, fmt.Errorf("authoritative ADR path %q must be relative and must not contain path traversal", entry.Path)
		}
		relPath := filepath.Join("adrs", cleanPath)
		body, err := readEffortArtifactRoot(root, relPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read authoritative ADR %s: %w", entry.Path, err)
		}
		document, err := ParseADRMarkdown(body)
		if err != nil {
			return nil, nil, fmt.Errorf("parse authoritative ADR %s: %w", entry.Path, err)
		}
		documents = append(documents, document)
		parsed = append(parsed, ParsedADR{
			Doc:         document,
			RelPath:     filepath.ToSlash(filepath.Clean(relPath)),
			RawMarkdown: string(body),
		})
	}
	if mismatches := ValidateADRIndexFrontMatterConsistency(index, parsed); len(mismatches) > 0 {
		return nil, nil, fmt.Errorf("validate authoritative %s: %s", indexRelPath, mismatches[0].Message)
	}

	return &index, documents, nil
}

func validateADRPersistenceInputIDs(label string, entries []ADRIndexEntry, documents []ADRDocument) error {
	entryIDs := map[string]bool{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ADRID)
		if id == "" {
			continue
		}
		if entryIDs[id] {
			return fmt.Errorf("%s duplicates adr_id %q", label, id)
		}
		entryIDs[id] = true
	}
	documentIDs := map[string]bool{}
	for _, document := range documents {
		id := strings.TrimSpace(document.FrontMatter.ADRID)
		if id == "" {
			continue
		}
		if documentIDs[id] {
			return fmt.Errorf("%s documents duplicate adr_id %q", label, id)
		}
		documentIDs[id] = true
	}

	return nil
}

func adrPersistenceTombstones(existing *ADRIndex, candidate []ADRIndexEntry) ([]string, error) {
	if existing == nil {
		return nil, nil
	}
	candidatePaths := make(map[string]bool, len(candidate))
	for _, entry := range candidate {
		cleanPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			return nil, fmt.Errorf("ADR path %q must be relative and must not contain path traversal", entry.Path)
		}
		candidatePaths[filepath.ToSlash(filepath.Clean(filepath.Join("adrs", cleanPath)))] = true
	}

	seen := map[string]bool{}
	var tombstones []string
	for _, entry := range existing.ADRs {
		cleanPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			return nil, fmt.Errorf("existing ADR path %q must be relative and must not contain path traversal", entry.Path)
		}
		ownedPath := filepath.ToSlash(filepath.Clean(filepath.Join("adrs", cleanPath)))
		if candidatePaths[ownedPath] || seen[ownedPath] {
			continue
		}
		seen[ownedPath] = true
		tombstones = append(tombstones, ownedPath)
	}
	sort.Strings(tombstones)

	return tombstones, nil
}

func validateADRPersistenceLifecycle(
	documents []ADRDocument,
	entries []ADRIndexEntry,
	contract adrPersistenceContract,
) error {
	expectedStatus := string(contract.status)
	seenDocumentIDs := map[string]bool{}
	seenEntryIDs := map[string]bool{}
	for index, document := range documents {
		path := entries[index].Path
		documentID := strings.TrimSpace(document.FrontMatter.ADRID)
		entryID := strings.TrimSpace(entries[index].ADRID)
		if documentID != "" {
			if seenDocumentIDs[documentID] {
				return fmt.Errorf("ADR persistence documents duplicate adr_id %q", documentID)
			}
			seenDocumentIDs[documentID] = true
		}
		if entryID != "" {
			if seenEntryIDs[entryID] {
				return fmt.Errorf("ADR persistence index entries duplicate adr_id %q", entryID)
			}
			seenEntryIDs[entryID] = true
		}
		if documentID != entryID {
			return fmt.Errorf("ADR %s front matter adr_id %q does not match index adr_id %q", path, documentID, entryID)
		}
		if strings.TrimSpace(document.FrontMatter.Status) != expectedStatus {
			return fmt.Errorf(
				"ADR %s status %q is invalid for workflow phase %q; expected %q",
				path,
				document.FrontMatter.Status,
				contract.phase,
				expectedStatus,
			)
		}
		if strings.TrimSpace(entries[index].Status) != expectedStatus {
			return fmt.Errorf(
				"ADR index entry %s status %q is invalid for workflow phase %q; expected %q",
				path,
				entries[index].Status,
				contract.phase,
				expectedStatus,
			)
		}

		hasAcceptedAt := strings.TrimSpace(document.FrontMatter.AcceptedAt) != ""
		hasAcceptedBy := strings.TrimSpace(document.FrontMatter.AcceptedBy) != ""
		hasAcceptanceProfile := strings.TrimSpace(document.FrontMatter.AcceptanceProfile) != ""
		if contract.requireAcceptanceMetadata {
			if !hasAcceptedAt || !hasAcceptedBy || !hasAcceptanceProfile {
				return fmt.Errorf("accepted ADR %s is missing acceptance metadata", path)
			}
		} else if hasAcceptedAt || hasAcceptedBy || hasAcceptanceProfile {
			return fmt.Errorf("draft ADR %s must not contain acceptance metadata", path)
		}
	}

	return nil
}

// LoadADRDraftContext safely loads and parses the current ADR index and every
// ADR document referenced by it. The returned documents preserve index order.
func LoadADRDraftContext(effortRoot string) (ADRDraftContext, error) {
	batch, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		return ADRDraftContext{}, fmt.Errorf("recover ADR batch transaction: %w", err)
	}
	defer batch.Close()
	generation, err := batch.ADRGeneration()
	if err != nil {
		return ADRDraftContext{}, fmt.Errorf("inspect ADR generation: %w", err)
	}

	state, err := loadADRWorkflowStateRoot(batch.root, PhaseADRDrafting)
	if err != nil {
		return ADRDraftContext{}, err
	}

	const indexRelPath = "adrs/index.json"
	indexJSON, err := readEffortArtifactRoot(batch.root, indexRelPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ADRDraftContext{}, fmt.Errorf("%w: %s", ErrADRDraftIndexNotFound, indexRelPath)
		}
		return ADRDraftContext{}, fmt.Errorf("read %s: %w", indexRelPath, err)
	}
	index, err := ParseADRIndexJSON(indexJSON)
	if err != nil {
		return ADRDraftContext{}, fmt.Errorf("parse %s: %w", indexRelPath, err)
	}
	if strings.TrimSpace(index.EffortID) != state.EffortID {
		return ADRDraftContext{}, fmt.Errorf(
			"%s effort_id %q does not match workflow state effort_id %q",
			indexRelPath,
			index.EffortID,
			state.EffortID,
		)
	}

	adrInput, err := loadADRGenerationContextRoot(batch.root, state)
	if err != nil {
		return ADRDraftContext{}, err
	}

	context := ADRDraftContext{
		PRD:                 adrInput.PRD,
		OriginalPRDMarkdown: adrInput.OriginalPRDMarkdown,
		Index:               index,
		OriginalIndexJSON:   append([]byte(nil), indexJSON...),
		Documents:           make([]ADRDraftContextDocument, 0, len(index.ADRs)),
		Generation:          generation,
	}
	parsedADRs := make([]ParsedADR, 0, len(index.ADRs))
	for _, entry := range index.ADRs {
		cleanPath, ok := cleanEffortRelativePath(entry.Path)
		if !ok {
			return ADRDraftContext{}, fmt.Errorf(
				"ADR index path %q must be relative and must not contain path traversal",
				entry.Path,
			)
		}

		relPath := filepath.Join("adrs", cleanPath)
		markdown, err := readEffortArtifactRoot(batch.root, relPath)
		if err != nil {
			return ADRDraftContext{}, fmt.Errorf("read ADR %s: %w", entry.Path, err)
		}
		document, err := ParseADRMarkdown(markdown)
		if err != nil {
			return ADRDraftContext{}, fmt.Errorf("parse ADR %s: %w", entry.Path, err)
		}

		canonicalPath := filepath.ToSlash(filepath.Clean(relPath))
		originalMarkdown := string(markdown)
		context.Documents = append(context.Documents, ADRDraftContextDocument{
			ADRID:            entry.ADRID,
			IndexPath:        entry.Path,
			RelativePath:     canonicalPath,
			Document:         document,
			OriginalMarkdown: originalMarkdown,
		})
		parsedADRs = append(parsedADRs, ParsedADR{
			Doc:         document,
			RelPath:     canonicalPath,
			RawMarkdown: originalMarkdown,
		})
	}

	if mismatches := ValidateADRIndexFrontMatterConsistency(index, parsedADRs); len(mismatches) > 0 {
		return ADRDraftContext{}, fmt.Errorf("validate %s: %s", indexRelPath, mismatches[0].Message)
	}

	return context, nil
}

// LoadADRGenerationContext safely loads the canonical PRD input for initial
// ADR generation while enforcing the ADR drafting lifecycle.
func LoadADRGenerationContext(effortRoot string) (ADRGenerationContext, error) {
	batch, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		return ADRGenerationContext{}, fmt.Errorf("recover ADR batch transaction: %w", err)
	}
	defer batch.Close()
	generation, err := batch.ADRGeneration()
	if err != nil {
		return ADRGenerationContext{}, fmt.Errorf("inspect ADR generation: %w", err)
	}
	if _, err := readEffortArtifactRoot(batch.root, "adrs/index.json"); err == nil {
		return ADRGenerationContext{}, errors.New("initial ADR generation requires adrs/index.json to be absent; reload the existing ADR draft context")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ADRGenerationContext{}, fmt.Errorf("inspect adrs/index.json before initial ADR generation: %w", err)
	}

	state, err := loadADRWorkflowStateRoot(batch.root, PhaseADRDrafting)
	if err != nil {
		return ADRGenerationContext{}, err
	}

	context, err := loadADRGenerationContextRoot(batch.root, state)
	if err != nil {
		return ADRGenerationContext{}, err
	}
	context.Generation = generation

	return context, nil
}

// PersistRawParseFailure stores raw model output for operator inspection
// without bypassing effort-root path containment or atomic replacement.
func PersistRawParseFailure(effortRoot string, relativePath string, content string) error {
	cleanPath, err := rawParseFailureArtifactPath(relativePath)
	if err != nil {
		return err
	}
	if err := writeEffortArtifactAtomic(effortRoot, cleanPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write raw parse failure %s: %w", filepath.ToSlash(cleanPath), err)
	}

	return nil
}

func rawParseFailureArtifactPath(relativePath string) (string, error) {
	cleanPath, ok := cleanEffortRelativePath(relativePath)
	if !ok || filepath.Dir(cleanPath) != "." || !isRawParseFailureArtifactName(filepath.Base(cleanPath)) {
		return "", fmt.Errorf(
			"raw parse failure path %q must name an owned top-level PRD or ADR parse-failure artifact",
			relativePath,
		)
	}

	return cleanPath, nil
}

func isRawParseFailureArtifactName(name string) bool {
	switch name {
	case "prd_parse_failure_final.md", "adrs_parse_failure_final.txt":
		return true
	}

	return isNumberedRawParseFailureArtifact(name, "prd_parse_failure_raw_", ".md") ||
		isNumberedRawParseFailureArtifact(name, "adrs_parse_failure_raw_", ".txt")
}

func isNumberedRawParseFailureArtifact(name, prefix, suffix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	attempt := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(attempt) != 3 {
		return false
	}
	for _, digit := range attempt {
		if digit < '0' || digit > '9' {
			return false
		}
	}

	return true
}

func loadADRWorkflowStateRoot(root *os.Root, requiredPhase CreationPhase) (WorkflowState, error) {
	state, phase, err := inspectWorkflowAuthority(root)
	if err != nil {
		return WorkflowState{}, fmt.Errorf("load ADR workflow state: %w", err)
	}
	if phase != requiredPhase {
		return WorkflowState{}, fmt.Errorf(
			"ADR operation requires workflow phase %q, got %q",
			requiredPhase,
			phase,
		)
	}

	return state, nil
}

func loadADRGenerationContextRoot(root *os.Root, state WorkflowState) (ADRGenerationContext, error) {
	const prdRelPath = "prd.md"
	prdMarkdown, err := readEffortArtifactRoot(root, prdRelPath)
	if err != nil {
		return ADRGenerationContext{}, fmt.Errorf("read %s: %w", prdRelPath, err)
	}
	prd, err := ParsePRDMarkdown(prdMarkdown)
	if err != nil {
		return ADRGenerationContext{}, fmt.Errorf("parse %s: %w", prdRelPath, err)
	}
	if strings.TrimSpace(prd.FrontMatter.TaskID) != state.TaskID {
		return ADRGenerationContext{}, fmt.Errorf(
			"%s task_id %q does not match workflow state task_id %q",
			prdRelPath,
			prd.FrontMatter.TaskID,
			state.TaskID,
		)
	}
	if strings.TrimSpace(prd.FrontMatter.Status) != string(ArtifactStateAccepted) {
		return ADRGenerationContext{}, fmt.Errorf(
			"%s status %q is invalid for ADR generation; expected %q",
			prdRelPath,
			prd.FrontMatter.Status,
			ArtifactStateAccepted,
		)
	}
	if strings.TrimSpace(prd.FrontMatter.AcceptedAt) == "" ||
		strings.TrimSpace(prd.FrontMatter.AcceptedBy) == "" ||
		strings.TrimSpace(prd.FrontMatter.AcceptanceProfile) == "" {
		return ADRGenerationContext{}, fmt.Errorf("%s is missing acceptance metadata", prdRelPath)
	}

	return ADRGenerationContext{
		PRD:                 prd,
		OriginalPRDMarkdown: string(prdMarkdown),
	}, nil
}

func SynthesizeTDDLedgerFromEffort(effortRoot string) (TDDLedgerSynthesisResult, error) {
	documents := loadTDDEffortDocuments(effortRoot, existingTDDLedgerOptional)
	state := documents.WorkflowState
	if phase, ok := creationPhaseFromStored(state.CurrentPhase); !ok || phase != PhaseTDDSynthesis {
		documents.Findings = append(documents.Findings, tddUseCaseFinding(
			"invalid_workflow_phase",
			fmt.Sprintf("TDD synthesis requires workflow phase %q, got %q", PhaseTDDSynthesis, state.CurrentPhase),
			WorkflowStateFileName,
		))
	}
	ledger := documents.Ledger
	if strings.TrimSpace(ledger.SchemaVersion) == "" {
		ledger = TDDLedger{
			SchemaVersion: "idiom.tdd_ledger.v1",
			EffortID:      state.EffortID,
			TaskID:        state.TaskID,
			CoverageTier:  defaultIfBlank(state.Coverage.CoverageTier, "unit"),
			Coverage: TDDCoverage{
				MutationMinScore: defaultTDDMutationMinScore,
				GatesPassed:      []string{},
				GatesBlocked:     []string{},
			},
			Obligations: []TDDObligation{},
		}
	}
	if ledger.Coverage.GatesPassed == nil {
		ledger.Coverage.GatesPassed = []string{}
	}
	if ledger.Coverage.GatesBlocked == nil {
		ledger.Coverage.GatesBlocked = []string{}
	}
	documents.Ledger = ledger

	planning := planTDDObligationsForDocuments(documents)
	result := TDDLedgerSynthesisResult{Ledger: ledger, Plan: planning.Plan}
	if planning.Status != ProductWorkflowStatusPass {
		return result, tddSynthesisBlockedError(planning.Plan)
	}

	ledger.Obligations = synthesizeTDDLedgerObligations(planning.Plan, documents.Ledger.Obligations)
	result.Ledger = ledger
	if err := validateTDDLedger(ledger); err != nil {
		return result, fmt.Errorf("validate synthesized tdd.json: %w", err)
	}

	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return TDDLedgerSynthesisResult{}, fmt.Errorf("encode tdd.json: %w", err)
	}
	if err := commitSynthesizedTDDLedger(effortRoot, documents.TDDGeneration, append(data, '\n')); err != nil {
		if artifactWriteCommitted(err) {
			return TDDLedgerSynthesisResult{Ledger: ledger, Plan: planning.Plan, CommitWarning: err.Error()}, nil
		}
		return TDDLedgerSynthesisResult{}, fmt.Errorf("write tdd.json: %w", err)
	}

	return TDDLedgerSynthesisResult{Ledger: ledger, Plan: planning.Plan}, nil
}

func tddSynthesisBlockedError(plan TDDObligationSynthesisPlan) error {
	reasons := make([]string, 0, len(plan.Findings)+len(plan.Obligations))
	for _, finding := range plan.Findings {
		reasons = append(reasons, fmt.Sprintf("%s (%s): %s", filepath.ToSlash(finding.File), finding.Kind, finding.Message))
	}
	for _, obligation := range plan.Obligations {
		if reason := strings.TrimSpace(obligation.BlockerReason); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no valid TDD obligations were produced")
	}

	return fmt.Errorf("TDD synthesis blocked: %s", strings.Join(stableUniqueStrings(reasons), "; "))
}

func synthesizeTDDLedgerObligations(plan TDDObligationSynthesisPlan, existing []TDDObligation) []TDDObligation {
	existingByID := make(map[string]TDDObligation, len(existing))
	usedIDs := make(map[string]bool, len(existing)+len(plan.Obligations))
	for _, obligation := range existing {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			existingByID[id] = obligation
		}
	}

	obligations := make([]TDDObligation, 0, len(plan.Obligations)+len(existing))
	nextGeneratedID := 1
	for _, item := range plan.Obligations {
		ids := stableUniqueStrings(item.ExistingObligationRefs)
		if len(ids) == 0 {
			for {
				candidate := fmt.Sprintf("TDD-%03d", nextGeneratedID)
				nextGeneratedID++
				if _, exists := existingByID[candidate]; !exists && !usedIDs[candidate] {
					ids = []string{candidate}
					break
				}
			}
		}

		for _, id := range ids {
			if usedIDs[id] {
				continue
			}
			usedIDs[id] = true

			obligation := existingByID[id]
			obligation.ID = id
			obligation.Type = defaultIfBlank(obligation.Type, defaultIfBlank(item.SuggestedType, "acceptance"))
			obligation.Behavior = defaultIfBlank(obligation.Behavior, synthesizedTDDBehavior(item))
			obligation.Owner = defaultIfBlank(obligation.Owner, item.OwnerPath)
			obligation.Command = defaultIfBlank(obligation.Command, defaultIfBlank(item.Command, "go test ./..."))
			obligation.RequiredBefore = defaultIfBlank(obligation.RequiredBefore, "implementation")
			obligation.Status = defaultIfBlank(obligation.Status, "planned")
			obligations = append(obligations, obligation)
		}
	}

	for _, obligation := range existing {
		id := strings.TrimSpace(obligation.ID)
		if id == "" || usedIDs[id] {
			continue
		}
		usedIDs[id] = true
		obligations = append(obligations, obligation)
	}

	return obligations
}

func synthesizedTDDBehavior(item TDDObligationPlanItem) string {
	if adrRef := strings.TrimSpace(item.ADRRef); adrRef != "" {
		return fmt.Sprintf("Verify %s behavior for PRD requirement %s", adrRef, item.PRDRequirementRef)
	}

	return fmt.Sprintf("Verify PRD requirement %s", item.PRDRequirementRef)
}

func mergeADRDraftIndex(existing ADRIndex, newDocuments []ADRDocument, newEntries []ADRIndexEntry) ([]ADRDocument, []ADRIndexEntry) {
	entryIndexByID := make(map[string]int, len(existing.ADRs))
	for index, entry := range existing.ADRs {
		entryIndexByID[entry.ADRID] = index
	}
	for _, entry := range newEntries {
		if index, ok := entryIndexByID[entry.ADRID]; ok {
			existing.ADRs[index] = entry
		} else {
			existing.ADRs = append(existing.ADRs, entry)
		}
	}

	documentByID := make(map[string]ADRDocument, len(newDocuments))
	for _, document := range newDocuments {
		documentByID[document.FrontMatter.ADRID] = document
	}
	orderedDocuments := make([]ADRDocument, 0, len(existing.ADRs))
	for _, entry := range existing.ADRs {
		if document, ok := documentByID[entry.ADRID]; ok {
			orderedDocuments = append(orderedDocuments, document)
		} else {
			orderedDocuments = append(orderedDocuments, ADRDocument{
				FrontMatter: ADRFrontMatter{ADRID: entry.ADRID, Status: entry.Status},
			})
		}
	}

	return orderedDocuments, existing.ADRs
}

func preserveADRDraftBodies(documents []ADRDocument, previous []ADRDocument) []ADRDocument {
	if len(documents) == 0 || len(previous) == 0 {
		return documents
	}

	previousByID := make(map[string]ADRDocument, len(previous))
	for _, document := range previous {
		id := strings.TrimSpace(document.FrontMatter.ADRID)
		if id != "" {
			previousByID[id] = document
		}
	}
	for index, document := range documents {
		id := strings.TrimSpace(document.FrontMatter.ADRID)
		if id == "" || strings.TrimSpace(document.BodyMarkdown) != "" {
			continue
		}
		if previousDocument, ok := previousByID[id]; ok && strings.TrimSpace(previousDocument.BodyMarkdown) != "" {
			documents[index] = previousDocument
		}
	}

	return documents
}
