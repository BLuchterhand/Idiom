package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"idiom/internal/failures"
	"idiom/internal/plancontract"
	"idiom/internal/util"
)

const PlanArtifactLegacySchemaVersion = "idiom.plan.v1"

var approvedPlanArtifactClientClassifications = []string{
	"finance",
	"healthcare_life_sciences",
	"public_sector",
	"enterprise_saas",
	"tooling_vendor",
	"local_model_private_code",
	"open_source",
}

type PlanArtifactTask struct {
	ID                    string   `json:"id,omitempty"`
	Summary               string   `json:"summary,omitempty"`
	ClientClassifications []string `json:"client_classifications,omitempty"`
}

type PlanArtifactContract struct {
	Kind              string   `json:"kind,omitempty"`
	StrategyOwner     string   `json:"strategy_owner,omitempty"`
	ContractOwner     string   `json:"contract_owner,omitempty"`
	PermissionProfile string   `json:"permission_profile,omitempty"`
	VerificationOwner string   `json:"verification_owner,omitempty"`
	RequiredEditPaths []string `json:"required_edit_paths,omitempty"`
	ForbiddenPaths    []string `json:"forbidden_paths,omitempty"`
}

type PlanArtifactPacket struct {
	PackID       string `json:"pack_id,omitempty"`
	Status       string `json:"status,omitempty"`
	IndexID      string `json:"index_id,omitempty"`
	Freshness    string `json:"freshness,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
}

type PlanArtifactPaths struct {
	Editable         []string `json:"editable,omitempty"`
	Required         []string `json:"required,omitempty"`
	SourceSatisfied  []string `json:"source_satisfied,omitempty"`
	ReadOnlyEvidence []string `json:"read_only_evidence,omitempty"`
	Forbidden        []string `json:"forbidden,omitempty"`
	Changed          []string `json:"changed,omitempty"`
}

type PlanArtifactEvidence struct {
	ID         string `json:"id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Handle     string `json:"handle,omitempty"`
	Path       string `json:"path,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Freshness  string `json:"freshness,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type PlanArtifactQuestion struct {
	ID                    string `json:"id,omitempty"`
	Claim                 string `json:"claim,omitempty"`
	Probe                 string `json:"probe,omitempty"`
	Status                string `json:"status,omitempty"`
	RequiredBeforeHandoff bool   `json:"required_before_handoff,omitempty"`
}

type PlanArtifactVerificationObligation struct {
	ID       string `json:"id,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Status   string `json:"status,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type PlanArtifactApproval struct {
	Required            bool     `json:"required"`
	Status              string   `json:"status,omitempty"`
	ScopeID             string   `json:"scope_id,omitempty"`
	Mode                string   `json:"mode,omitempty"`
	ApprovedBy          string   `json:"approved_by,omitempty"`
	ApprovedAt          string   `json:"approved_at,omitempty"`
	ExpirationPolicy    string   `json:"expiration_policy,omitempty"`
	ApprovedPaths       []string `json:"approved_paths,omitempty"`
	PendingPaths        []string `json:"pending_paths,omitempty"`
	ExecutionPermission string   `json:"execution_permission,omitempty"`
}

type PlanArtifactHandoff struct {
	ID       string   `json:"id,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Status   string   `json:"status,omitempty"`
	Ready    bool     `json:"ready"`
	Blockers []string `json:"blockers,omitempty"`
}

type PlanArtifactSchemaError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

type PlanArtifactValidationError struct {
	Errors []PlanArtifactSchemaError `json:"errors"`
}

func (e *PlanArtifactValidationError) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "plan artifact schema invalid"
	}
	limit := len(e.Errors)
	if limit > 6 {
		limit = 6
	}
	parts := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		item := e.Errors[i]
		field := util.FirstNonEmpty(strings.TrimSpace(item.Field), "(root)")
		code := util.FirstNonEmpty(strings.TrimSpace(item.Code), "invalid")
		detail := strings.TrimSpace(item.Detail)
		if detail != "" {
			parts = append(parts, fmt.Sprintf("%s:%s:%s", field, code, detail))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s", field, code))
		}
	}
	if len(e.Errors) > limit {
		parts = append(parts, fmt.Sprintf("and_%d_more", len(e.Errors)-limit))
	}
	return "plan_artifact.schema_errors " + strings.Join(parts, "; ")
}

func (e *PlanArtifactValidationError) Code() failures.Code {
	return failures.InvalidArgument
}

func (e *PlanArtifactValidationError) Reason() failures.Reason {
	return reasonPlanArtifactSchemaInvalid
}

func approvedPlanArtifactClientClasses() []string {
	return append([]string(nil), approvedPlanArtifactClientClassifications...)
}

func ParsePlanArtifactJSON(body []byte) (PlanArtifact, error) {
	var artifact PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("decode plan artifact: %w", err)
	}
	artifact = NormalizeLoadedPlanArtifact(artifact)
	if err := ValidatePlanArtifact(artifact); err != nil {
		return PlanArtifact{}, err
	}
	return artifact, nil
}

func newPlanArtifactFromSubmitPlanRequest(req ToolRequest, sessionID string, created string, planID string) (PlanArtifact, error) {
	if req.PlanArtifact != nil {
		artifact := *req.PlanArtifact
		if len(artifact.Edits) == 0 && len(req.Edits) > 0 {
			artifact.Edits = append([]PlanEdit(nil), req.Edits...)
		}
		if len(artifact.Paths.Required) == 0 && len(artifact.Contract.RequiredEditPaths) > 0 {
			artifact.Paths.Required = append([]string(nil), artifact.Contract.RequiredEditPaths...)
		}
		artifact.Paths.SourceSatisfied = append(append([]string(nil), artifact.Paths.SourceSatisfied...), req.SourceSatisfiedPaths...)
		artifact.Edits = planEditsWithoutAnchorProvenance(artifact.Edits)
		if strings.TrimSpace(artifact.Rationale) == "" {
			artifact.Rationale = req.Rationale
		}
		if strings.TrimSpace(artifact.PackID) == "" {
			artifact.PackID = req.PackID
		}
		artifact = normalizePlanArtifactForSubmit(artifact, sessionID, created, planID, false)
		if err := ValidatePlanArtifact(artifact); err != nil {
			return PlanArtifact{}, err
		}
		return artifact, nil
	}
	artifact := PlanArtifact{
		SchemaVersion: PlanArtifactSchemaVersion,
		PlanID:        planID,
		Created:       created,
		Rationale:     sanitizePlanArtifactText(req.Rationale, 2000),
		PackID:        strings.TrimSpace(req.PackID),
		SessionID:     strings.TrimSpace(sessionID),
		Edits:         planEditsWithoutAnchorProvenance(req.Edits),
		Task: PlanArtifactTask{
			Summary: sanitizePlanArtifactText(util.FirstNonEmpty(req.Rationale, "legacy submit_plan request"), 240),
		},
		Contract: PlanArtifactContract{
			Kind:              "plan_then_execute",
			StrategyOwner:     "model",
			ContractOwner:     "harness",
			PermissionProfile: "plan_then_execute",
			VerificationOwner: "planner",
		},
		Packet: PlanArtifactPacket{
			PackID: strings.TrimSpace(req.PackID),
		},
		Paths: PlanArtifactPaths{
			SourceSatisfied: req.SourceSatisfiedPaths,
		},
		Approval: PlanArtifactApproval{
			Required:         false,
			Status:           "implicit",
			ScopeID:          planID + ":submit_plan",
			Mode:             "plan_then_execute",
			ExpirationPolicy: "workspace_fingerprint",
		},
		Handoff: PlanArtifactHandoff{
			ID:     planID + ":handoff",
			From:   "planner",
			To:     "plan_execute_executor",
			Status: "ready",
			Ready:  true,
		},
	}
	artifact = normalizePlanArtifactForSubmit(artifact, sessionID, created, planID, true)
	if err := ValidatePlanArtifact(artifact); err != nil {
		return PlanArtifact{}, err
	}
	return artifact, nil
}

func PlanArtifactSourceSatisfiedEvidenceRefs(artifact PlanArtifact) []plancontract.SourceSatisfiedEvidenceRef {
	refs := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(artifact.Evidence))
	for _, evidence := range artifact.Evidence {
		refs = append(refs, plancontract.SourceSatisfiedEvidenceRef{
			Path: evidence.Path,
			Kind: evidence.Kind,
		})
	}
	return refs
}

func planEditsWithoutAnchorProvenance(edits []PlanEdit) []PlanEdit {
	out := append([]PlanEdit(nil), edits...)
	for i := range out {
		out[i].AnchorProvenance = nil
	}
	return out
}

func normalizePlanArtifactForSubmit(artifact PlanArtifact, sessionID string, created string, planID string, legacyRequest bool) PlanArtifact {
	switch schemaVersion := strings.TrimSpace(artifact.SchemaVersion); schemaVersion {
	case "", PlanArtifactPreviousSchemaVersion:
		artifact.SchemaVersion = PlanArtifactSchemaVersion
	default:
		artifact.SchemaVersion = schemaVersion
	}
	artifact.PlanID = sanitizePlanArtifactID(util.FirstNonEmpty(artifact.PlanID, planID))
	artifact.Created = util.FirstNonEmpty(strings.TrimSpace(artifact.Created), created)
	artifact.SessionID = sanitizePlanArtifactID(util.FirstNonEmpty(artifact.SessionID, sessionID))
	artifact.Rationale = sanitizePlanArtifactText(artifact.Rationale, 2000)
	artifact.Task.ID = sanitizePlanArtifactID(artifact.Task.ID)
	artifact.Task.Summary = sanitizePlanArtifactText(artifact.Task.Summary, 240)
	if legacyRequest && strings.TrimSpace(artifact.Task.Summary) == "" {
		artifact.Task.Summary = sanitizePlanArtifactText(util.FirstNonEmpty(artifact.Rationale, "legacy submit_plan request"), 240)
	}
	artifact.Task.ClientClassifications = normalizePlanArtifactClientClassifications(artifact.Task.ClientClassifications)
	artifact.Contract.Kind = sanitizePlanArtifactToken(artifact.Contract.Kind)
	artifact.Contract.StrategyOwner = sanitizePlanArtifactToken(artifact.Contract.StrategyOwner)
	artifact.Contract.ContractOwner = sanitizePlanArtifactToken(artifact.Contract.ContractOwner)
	artifact.Contract.PermissionProfile = sanitizePlanArtifactToken(artifact.Contract.PermissionProfile)
	artifact.Contract.VerificationOwner = sanitizePlanArtifactToken(artifact.Contract.VerificationOwner)
	if legacyRequest {
		artifact.Contract.Kind = util.FirstNonEmpty(artifact.Contract.Kind, "plan_then_execute")
		artifact.Contract.StrategyOwner = util.FirstNonEmpty(artifact.Contract.StrategyOwner, "model")
		artifact.Contract.ContractOwner = util.FirstNonEmpty(artifact.Contract.ContractOwner, "harness")
		artifact.Contract.PermissionProfile = util.FirstNonEmpty(artifact.Contract.PermissionProfile, "plan_then_execute")
		artifact.Contract.VerificationOwner = util.FirstNonEmpty(artifact.Contract.VerificationOwner, "planner")
	}
	artifact.Contract.RequiredEditPaths = normalizePlanArtifactPaths(artifact.Contract.RequiredEditPaths)
	artifact.Contract.ForbiddenPaths = normalizePlanArtifactPaths(artifact.Contract.ForbiddenPaths)
	if strings.TrimSpace(artifact.PackID) == "" {
		artifact.PackID = strings.TrimSpace(artifact.Packet.PackID)
	}
	if strings.TrimSpace(artifact.Packet.PackID) == "" {
		artifact.Packet.PackID = strings.TrimSpace(artifact.PackID)
	}
	artifact.PackID = sanitizePlanArtifactPackID(artifact.PackID)
	artifact.Packet.PackID = sanitizePlanArtifactPackID(artifact.Packet.PackID)
	if strings.TrimSpace(artifact.Packet.Status) == "" {
		if strings.TrimSpace(artifact.Packet.PackID) == "" {
			artifact.Packet.Status = "not_provided"
		} else {
			artifact.Packet.Status = "provided"
		}
	}
	artifact.Packet.Status = sanitizePlanArtifactToken(artifact.Packet.Status)
	artifact.Packet.IndexID = sanitizePlanArtifactToken(artifact.Packet.IndexID)
	artifact.Packet.Freshness = sanitizePlanArtifactToken(artifact.Packet.Freshness)
	artifact.Packet.ArtifactPath = sanitizePlanArtifactPathValue(artifact.Packet.ArtifactPath)
	artifact.Edits = normalizePlanArtifactEdits(artifact.Edits)
	editPaths := planArtifactEditPaths(artifact.Edits)
	artifact.Paths.Editable = normalizePlanArtifactPaths(firstNonEmptyStrings(artifact.Paths.Editable, editPaths))
	artifact.Paths.Changed = normalizePlanArtifactPaths(firstNonEmptyStrings(artifact.Paths.Changed, editPaths))
	artifact.Paths.Required = normalizePlanArtifactPaths(firstNonEmptyStrings(artifact.Paths.Required, artifact.Contract.RequiredEditPaths))
	artifact.Paths.SourceSatisfied = normalizePlanArtifactPaths(artifact.Paths.SourceSatisfied)
	artifact.Paths.ReadOnlyEvidence = normalizePlanArtifactPaths(artifact.Paths.ReadOnlyEvidence)
	artifact.Paths.Forbidden = normalizePlanArtifactPaths(firstNonEmptyStrings(artifact.Paths.Forbidden, artifact.Contract.ForbiddenPaths))
	artifact.Evidence = normalizePlanArtifactEvidence(artifact.Evidence)
	if len(artifact.Evidence) == 0 {
		artifact.Evidence = defaultPlanArtifactEvidence(artifact)
	}
	artifact.UnresolvedQuestions = normalizePlanArtifactQuestions(artifact.UnresolvedQuestions)
	artifact.VerificationObligations = normalizePlanArtifactVerification(artifact.VerificationObligations)
	if len(artifact.VerificationObligations) == 0 && legacyRequest {
		artifact.VerificationObligations = []PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     "executor_lane_sanity",
			Owner:    util.FirstNonEmpty(artifact.Contract.VerificationOwner, "planner"),
			Status:   "open",
			Required: true,
		}}
	}
	artifact.Approval.Status = sanitizePlanArtifactToken(util.FirstNonEmpty(artifact.Approval.Status, "implicit"))
	artifact.Approval.ScopeID = sanitizePlanArtifactID(artifact.Approval.ScopeID)
	artifact.Approval.Mode = sanitizePlanArtifactToken(artifact.Approval.Mode)
	artifact.Approval.ApprovedBy = sanitizePlanArtifactID(artifact.Approval.ApprovedBy)
	artifact.Approval.ApprovedAt = sanitizePlanArtifactToken(artifact.Approval.ApprovedAt)
	artifact.Approval.ExpirationPolicy = sanitizePlanArtifactToken(artifact.Approval.ExpirationPolicy)
	artifact.Approval.ApprovedPaths = normalizePlanArtifactPaths(artifact.Approval.ApprovedPaths)
	artifact.Approval.PendingPaths = normalizePlanArtifactPaths(artifact.Approval.PendingPaths)
	artifact.Approval.ExecutionPermission = sanitizePlanArtifactToken(artifact.Approval.ExecutionPermission)
	if artifact.Approval.Required && planArtifactApprovalSatisfied(artifact.Approval.Status) && len(artifact.Approval.ApprovedPaths) == 0 {
		artifact.Approval.ApprovedPaths = normalizePlanArtifactPaths(firstNonEmptyStrings(artifact.Paths.Required, editPaths))
	}
	if artifact.Approval.ScopeID == "" {
		artifact.Approval.ScopeID = planArtifactStableApprovalScopeID(artifact, editPaths)
	}
	if artifact.Approval.ExecutionPermission == "" && !planArtifactApprovalPartial(artifact.Approval.Status) {
		artifact.Approval.ExecutionPermission = defaultPlanArtifactExecutionPermission(artifact)
	}
	artifact.Handoff.ID = sanitizePlanArtifactID(artifact.Handoff.ID)
	if artifact.Handoff.ID == "" {
		artifact.Handoff.ID = planArtifactStableHandoffID(artifact, editPaths)
	}
	artifact.Handoff.From = sanitizePlanArtifactToken(artifact.Handoff.From)
	artifact.Handoff.To = sanitizePlanArtifactToken(artifact.Handoff.To)
	artifact.Handoff.Status = sanitizePlanArtifactToken(util.FirstNonEmpty(artifact.Handoff.Status, "ready"))
	artifact.Handoff.Blockers = normalizePlanArtifactTokens(artifact.Handoff.Blockers)
	if legacyRequest {
		artifact.Handoff.From = util.FirstNonEmpty(artifact.Handoff.From, "planner")
		artifact.Handoff.To = util.FirstNonEmpty(artifact.Handoff.To, "plan_execute_executor")
		if len(artifact.Handoff.Blockers) == 0 {
			artifact.Handoff.Ready = true
		}
	}
	if !artifact.Handoff.Ready && planArtifactHandoffExecutable(artifact) {
		artifact.Handoff.Ready = true
	}
	artifact.LegacyProse = sanitizePlanArtifactText(artifact.LegacyProse, 2000)
	return artifact
}

func NormalizeLoadedPlanArtifact(artifact PlanArtifact) PlanArtifact {
	loadedSchemaVersion := strings.TrimSpace(artifact.SchemaVersion)
	if loadedSchemaVersion == PlanArtifactLegacySchemaVersion || loadedSchemaVersion == PlanArtifactPreviousSchemaVersion {
		artifact.Edits = markLegacyDeletionEditsExplicit(artifact.Edits)
	}
	if loadedSchemaVersion == PlanArtifactLegacySchemaVersion {
		artifact.SchemaVersion = PlanArtifactLegacySchemaVersion
		artifact = normalizePlanArtifactForSubmit(artifact, artifact.SessionID, artifact.Created, util.FirstNonEmpty(artifact.PlanID, "legacy-plan"), true)
		return artifact
	}
	artifact = normalizePlanArtifactForSubmit(artifact, artifact.SessionID, artifact.Created, artifact.PlanID, false)
	if loadedSchemaVersion == PlanArtifactPreviousSchemaVersion {
		artifact.SchemaVersion = PlanArtifactPreviousSchemaVersion
	}
	return artifact
}

func markLegacyDeletionEditsExplicit(edits []PlanEdit) []PlanEdit {
	out := append([]PlanEdit(nil), edits...)
	for i := range out {
		if strings.TrimSpace(out[i].OldText) == "" || strings.TrimSpace(out[i].NewText) != "" || strings.TrimSpace(out[i].NewTextRef) != "" {
			continue
		}
		out[i].newTextSet = true
	}
	return out
}

func ValidatePlanArtifact(artifact PlanArtifact) error {
	var errs []PlanArtifactSchemaError
	if !planArtifactSchemaIsSupported(artifact.SchemaVersion) {
		errs = appendPlanArtifactSchemaError(errs, "schema_version", "unsupported", "expected current or legacy plan artifact schema")
	}
	if planArtifactSchemaIsStructured(artifact.SchemaVersion) {
		if strings.TrimSpace(artifact.PlanID) == "" {
			errs = appendPlanArtifactSchemaError(errs, "plan_id", "required", "plan id is required")
		}
		if strings.TrimSpace(artifact.Task.ID) == "" && strings.TrimSpace(artifact.Task.Summary) == "" {
			errs = appendPlanArtifactSchemaError(errs, "task", "required", "task id or bounded summary is required")
		}
		if strings.TrimSpace(artifact.Contract.Kind) == "" {
			errs = appendPlanArtifactSchemaError(errs, "contract.kind", "required", "contract kind is required")
		}
		if strings.TrimSpace(artifact.Packet.Status) == "" {
			errs = appendPlanArtifactSchemaError(errs, "packet.status", "required", "packet status is required")
		}
		if len(artifact.Paths.Editable) == 0 && len(artifact.Paths.Required) == 0 && len(artifact.Paths.Changed) == 0 {
			errs = appendPlanArtifactSchemaError(errs, "paths", "required", "editable, required, or changed paths are required")
		}
		if len(artifact.Evidence) == 0 {
			errs = appendPlanArtifactSchemaError(errs, "evidence", "required", "at least one bounded evidence reference is required")
		}
		if len(artifact.VerificationObligations) == 0 {
			errs = appendPlanArtifactSchemaError(errs, "verification_obligations", "required", "at least one verification obligation is required")
		}
		if strings.TrimSpace(artifact.Approval.Status) == "" {
			errs = appendPlanArtifactSchemaError(errs, "approval.status", "required", "approval status is required")
		}
		if strings.TrimSpace(artifact.Approval.ScopeID) == "" {
			errs = appendPlanArtifactSchemaError(errs, "approval.scope_id", "required", "approval scope id is required")
		}
		if strings.TrimSpace(artifact.Approval.ExecutionPermission) == "" {
			errs = appendPlanArtifactSchemaError(errs, "approval.execution_permission", "required", "approval execution permission is required")
		}
		if !planArtifactExecutionPermissionKnown(artifact.Approval.ExecutionPermission) {
			errs = appendPlanArtifactSchemaError(errs, "approval.execution_permission", "unsupported", "approval execution permission must be execute_all, execute_approved_scope, or blocked")
		}
		if planArtifactApprovalPartial(artifact.Approval.Status) {
			if len(artifact.Approval.ApprovedPaths) == 0 {
				errs = appendPlanArtifactSchemaError(errs, "approval.approved_paths", "required", "partial approval requires explicit approved scope paths")
			}
			if len(artifact.Approval.PendingPaths) == 0 {
				errs = appendPlanArtifactSchemaError(errs, "approval.pending_paths", "required", "partial approval requires explicit pending scope paths")
			}
			if artifact.Approval.ExecutionPermission != "execute_approved_scope" && artifact.Approval.ExecutionPermission != "blocked" {
				errs = appendPlanArtifactSchemaError(errs, "approval.execution_permission", "invalid_partial_permission", "partial approval must execute only approved scope or remain blocked")
			}
		}
		if strings.TrimSpace(artifact.Handoff.ID) == "" {
			errs = appendPlanArtifactSchemaError(errs, "handoff.id", "required", "handoff id is required")
		}
		if strings.TrimSpace(artifact.Handoff.Status) == "" {
			errs = appendPlanArtifactSchemaError(errs, "handoff.status", "required", "handoff status is required")
		}
	}
	if len(artifact.Edits) == 0 {
		errs = appendPlanArtifactSchemaError(errs, "edits", "required", "at least one executable edit is required")
	}
	for i, edit := range artifact.Edits {
		if err := validatePlanEdit(edit); err != nil {
			errs = appendPlanArtifactSchemaError(errs, fmt.Sprintf("edits[%d]", i), "invalid_edit_shape", err.Error())
		}
		if path := strings.TrimSpace(edit.Path); path != "" && !planArtifactPathIsSafe(path) {
			errs = appendPlanArtifactSchemaError(errs, fmt.Sprintf("edits[%d].path", i), "invalid_path", "path must stay relative to the workspace")
		}
	}
	validatePlanArtifactPathList(&errs, "paths.editable", artifact.Paths.Editable)
	validatePlanArtifactPathList(&errs, "paths.required", artifact.Paths.Required)
	validatePlanArtifactPathList(&errs, "paths.source_satisfied", artifact.Paths.SourceSatisfied)
	validatePlanArtifactPathList(&errs, "paths.read_only_evidence", artifact.Paths.ReadOnlyEvidence)
	validatePlanArtifactPathList(&errs, "paths.forbidden", artifact.Paths.Forbidden)
	validatePlanArtifactPathList(&errs, "paths.changed", artifact.Paths.Changed)
	validatePlanArtifactPathList(&errs, "contract.required_edit_paths", artifact.Contract.RequiredEditPaths)
	validatePlanArtifactPathList(&errs, "contract.forbidden_paths", artifact.Contract.ForbiddenPaths)
	validatePlanArtifactPathList(&errs, "approval.approved_paths", artifact.Approval.ApprovedPaths)
	validatePlanArtifactPathList(&errs, "approval.pending_paths", artifact.Approval.PendingPaths)
	for i, classification := range artifact.Task.ClientClassifications {
		if !isApprovedPlanArtifactClientClassification(classification) {
			errs = appendPlanArtifactSchemaError(errs, fmt.Sprintf("task.client_classifications[%d]", i), "unsupported_client_classification", "client classification must be one of the approved values")
		}
	}
	if len(errs) > 0 {
		return &PlanArtifactValidationError{Errors: errs}
	}
	return nil
}

func planArtifactSchemaIsSupported(version string) bool {
	return planArtifactSchemaIsStructured(version) || version == PlanArtifactLegacySchemaVersion
}

func planArtifactSchemaIsStructured(version string) bool {
	switch strings.TrimSpace(version) {
	case PlanArtifactSchemaVersion, PlanArtifactPreviousSchemaVersion:
		return true
	default:
		return false
	}
}

func appendPlanArtifactSchemaError(errs []PlanArtifactSchemaError, field string, code string, detail string) []PlanArtifactSchemaError {
	return append(errs, PlanArtifactSchemaError{
		Field:  sanitizePlanArtifactField(field),
		Code:   sanitizePlanArtifactToken(code),
		Detail: sanitizePlanArtifactErrorDetail(detail),
	})
}

func validatePlanArtifactPathList(errs *[]PlanArtifactSchemaError, field string, paths []string) {
	for i, path := range paths {
		if !planArtifactPathIsSafe(path) {
			*errs = appendPlanArtifactSchemaError(*errs, fmt.Sprintf("%s[%d]", field, i), "invalid_path", "path must stay relative to the workspace")
		}
	}
}

func defaultPlanArtifactEvidence(artifact PlanArtifact) []PlanArtifactEvidence {
	if strings.TrimSpace(artifact.Packet.PackID) != "" {
		return []PlanArtifactEvidence{{
			ID:        "evidence-1",
			Kind:      "context_packet",
			Handle:    "context_pack://" + artifact.Packet.PackID,
			Freshness: util.FirstNonEmpty(artifact.Packet.Freshness, artifact.Packet.Status),
		}}
	}
	return []PlanArtifactEvidence{{
		ID:      "evidence-1",
		Kind:    "planner_submission",
		Summary: "legacy submit_plan payload normalized into durable artifact",
	}}
}

func defaultPlanArtifactExecutionPermission(artifact PlanArtifact) string {
	status := strings.TrimSpace(artifact.Approval.Status)
	switch {
	case planArtifactApprovalPartial(status):
		return ""
	case planArtifactApprovalSatisfied(status), !artifact.Approval.Required:
		return "execute_all"
	default:
		return "blocked"
	}
}

func planArtifactExecutionPermissionKnown(value string) bool {
	switch strings.TrimSpace(value) {
	case "execute_all", "execute_approved_scope", "blocked":
		return true
	default:
		return false
	}
}

func planArtifactHandoffExecutable(artifact PlanArtifact) bool {
	if strings.TrimSpace(artifact.Handoff.Status) != "ready" || len(artifact.Handoff.Blockers) > 0 {
		return false
	}
	switch strings.TrimSpace(artifact.Approval.ExecutionPermission) {
	case "execute_all", "execute_approved_scope":
		return true
	default:
		return false
	}
}

func planArtifactApprovalPartial(status string) bool {
	switch strings.TrimSpace(status) {
	case "partial", "partially_approved":
		return true
	default:
		return false
	}
}

func planArtifactStableApprovalScopeID(artifact PlanArtifact, editPaths []string) string {
	approved := artifact.Approval.ApprovedPaths
	if len(approved) == 0 {
		approved = firstNonEmptyStrings(artifact.Paths.Required, editPaths)
	}
	parts := []string{
		"plan:" + artifact.PlanID,
		"packet:" + artifact.Packet.PackID,
		"approval:" + artifact.Approval.Status,
	}
	for _, path := range normalizePlanArtifactPaths(approved) {
		parts = append(parts, "approved:"+path)
	}
	for _, path := range normalizePlanArtifactPaths(artifact.Approval.PendingPaths) {
		parts = append(parts, "pending:"+path)
	}
	return planArtifactStableID("scope", parts...)
}

func planArtifactStableHandoffID(artifact PlanArtifact, editPaths []string) string {
	parts := []string{
		"plan:" + artifact.PlanID,
		"scope:" + artifact.Approval.ScopeID,
		"packet:" + artifact.Packet.PackID,
		"from:" + artifact.Handoff.From,
		"to:" + artifact.Handoff.To,
	}
	for _, path := range normalizePlanArtifactPaths(editPaths) {
		parts = append(parts, "edit:"+path)
	}
	return planArtifactStableID("handoff", parts...)
}

func planArtifactStableID(prefix string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "empty")
	}
	return sanitizePlanArtifactID(prefix + "-" + planArtifactHash(strings.Join(parts, "\x00")))
}

func planArtifactEditPaths(edits []PlanEdit) []string {
	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		if path := strings.TrimSpace(edit.Path); path != "" {
			paths = append(paths, path)
		}
	}
	return normalizePlanArtifactPaths(paths)
}

func normalizePlanArtifactEdits(edits []PlanEdit) []PlanEdit {
	out := append([]PlanEdit(nil), edits...)
	for i := range out {
		out[i].Purpose = sanitizePlanArtifactEditPurpose(out[i].Purpose, 240)
	}
	return out
}

func normalizePlanArtifactEvidence(values []PlanArtifactEvidence) []PlanArtifactEvidence {
	out := make([]PlanArtifactEvidence, 0, len(values))
	for i, value := range values {
		item := PlanArtifactEvidence{
			ID:         sanitizePlanArtifactID(util.FirstNonEmpty(value.ID, fmt.Sprintf("evidence-%d", i+1))),
			Kind:       sanitizePlanArtifactToken(value.Kind),
			Handle:     sanitizePlanArtifactHandle(value.Handle),
			Path:       sanitizePlanArtifactPathValue(value.Path),
			Summary:    sanitizePlanArtifactText(value.Summary, 240),
			Freshness:  sanitizePlanArtifactToken(value.Freshness),
			Confidence: sanitizePlanArtifactToken(value.Confidence),
		}
		if item.Kind == "" && item.Handle == "" && item.Path == "" && item.Summary == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizePlanArtifactQuestions(values []PlanArtifactQuestion) []PlanArtifactQuestion {
	out := make([]PlanArtifactQuestion, 0, len(values))
	for i, value := range values {
		item := PlanArtifactQuestion{
			ID:                    sanitizePlanArtifactID(util.FirstNonEmpty(value.ID, fmt.Sprintf("question-%d", i+1))),
			Claim:                 sanitizePlanArtifactText(value.Claim, 240),
			Probe:                 sanitizePlanArtifactText(value.Probe, 240),
			Status:                sanitizePlanArtifactToken(value.Status),
			RequiredBeforeHandoff: value.RequiredBeforeHandoff,
		}
		if item.Claim == "" && item.Probe == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizePlanArtifactVerification(values []PlanArtifactVerificationObligation) []PlanArtifactVerificationObligation {
	out := make([]PlanArtifactVerificationObligation, 0, len(values))
	for i, value := range values {
		item := PlanArtifactVerificationObligation{
			ID:       sanitizePlanArtifactID(util.FirstNonEmpty(value.ID, fmt.Sprintf("verification-%d", i+1))),
			Kind:     sanitizePlanArtifactToken(value.Kind),
			Command:  sanitizePlanArtifactText(value.Command, 240),
			Path:     sanitizePlanArtifactPathValue(value.Path),
			Owner:    sanitizePlanArtifactToken(value.Owner),
			Status:   sanitizePlanArtifactToken(value.Status),
			Required: value.Required,
		}
		if item.Kind == "" && item.Command == "" && item.Path == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizePlanArtifactClientClassifications(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizePlanArtifactClientClassification(value)
		if normalized == "" {
			if strings.TrimSpace(value) == "" {
				continue
			}
			token := sanitizePlanArtifactToken(value)
			if token == "" {
				token = "invalid_client_classification"
			}
			normalized = token
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizePlanArtifactClientClassification(value string) string {
	normalized := sanitizePlanArtifactToken(value)
	switch normalized {
	case "finance", "financial_services":
		return "finance"
	case "healthcare", "healthcare_life_sciences", "life_sciences":
		return "healthcare_life_sciences"
	case "public_sector", "government", "gov":
		return "public_sector"
	case "enterprise", "enterprise_saas", "saas":
		return "enterprise_saas"
	case "tooling_vendor", "tool_vendor", "developer_tooling":
		return "tooling_vendor"
	case "local_model_private_code", "private_code", "local_model":
		return "local_model_private_code"
	case "open_source", "oss":
		return "open_source"
	default:
		return ""
	}
}

func isApprovedPlanArtifactClientClassification(value string) bool {
	normalized := normalizePlanArtifactClientClassification(value)
	if normalized == "" {
		return false
	}
	for _, approved := range approvedPlanArtifactClientClassifications {
		if normalized == approved {
			return true
		}
	}
	return false
}

func normalizePlanArtifactPaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		path := sanitizePlanArtifactPathValue(value)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func normalizePlanArtifactTokens(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		token := sanitizePlanArtifactToken(value)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func firstNonEmptyStrings(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func planArtifactPathIsSafe(path string) bool {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func sanitizePlanArtifactPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || planArtifactTextLooksSensitive(value) {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if !planArtifactPathIsSafe(value) {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return sanitizePlanArtifactVisibleText(value, 240)
}

func sanitizePlanArtifactPackID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 16 {
		return ""
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	return value
}

func sanitizePlanArtifactHandle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || planArtifactTextLooksSensitive(value) {
		return ""
	}
	return sanitizePlanArtifactVisibleText(value, 240)
}

func sanitizePlanArtifactID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || planArtifactTextLooksSensitive(value) {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		}
		if b.Len() >= 120 {
			break
		}
	}
	return strings.Trim(b.String(), "-_.:")
}

func sanitizePlanArtifactField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return sanitizePlanArtifactVisibleText(value, 120)
}

func sanitizePlanArtifactToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || planArtifactTextLooksSensitive(value) {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == '.' || r == ':' || r == '/':
			if !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
		if b.Len() >= 120 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func sanitizePlanArtifactText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planArtifactTextLooksSensitive(value) {
		return "redacted:" + planArtifactHash(value)
	}
	return sanitizePlanArtifactVisibleText(value, maxBytes)
}

func sanitizePlanArtifactEditPurpose(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planArtifactTextLooksSensitive(value) || planArtifactEditPurposeLooksSensitive(value) {
		return "redacted:" + planArtifactHash(value)
	}
	return sanitizePlanArtifactVisibleText(value, maxBytes)
}

func sanitizePlanArtifactErrorDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "insert_after_line content exceeds inline submit_plan limit"):
		return sanitizePlanArtifactVisibleText(value, 600)
	case strings.Contains(lower, "requires one edit shape"), strings.Contains(lower, "requires one executable edit shape"):
		return sanitizePlanArtifactVisibleText(value, 600)
	case strings.Contains(lower, "path"):
		return sanitizePlanArtifactVisibleText(value, 160)
	case strings.Contains(lower, "old_text"):
		return sanitizePlanArtifactVisibleText(value, 600)
	default:
		if planArtifactTextLooksSensitive(value) {
			return "redacted:" + planArtifactHash(value)
		}
		return sanitizePlanArtifactVisibleText(value, 160)
	}
}

func sanitizePlanArtifactVisibleText(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes && len(value) > 0 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return strings.TrimSpace(value) + " [truncated]"
}

func planArtifactTextLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"raw_prompt",
		"raw prompt",
		"raw_body",
		"raw body",
		"raw_content",
		"raw content",
		"prompt_body",
		"prompt body",
		"transcript",
		"file_body",
		"file body",
		"file-body",
		"private key",
		"secret",
		"token=",
		"token:",
		"access_token",
		"api token",
		"secret=",
		"api_key",
		"api-key",
		"password=",
		"bearer ",
		"customer-",
		"customer_",
		"secrets/",
		"/secrets/",
		"\\secrets\\",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func planArtifactEditPurposeLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	if planArtifactContainsSensitiveWord(lower, "token") ||
		planArtifactContainsSensitiveWord(lower, "tokens") ||
		planArtifactContainsSensitiveWord(lower, "customer") ||
		planArtifactContainsSensitiveWord(lower, "customers") {
		return true
	}
	return planArtifactContainsLocalPrivatePath(lower)
}

func planArtifactContainsSensitiveWord(value string, word string) bool {
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if token == word {
			return true
		}
	}
	return false
}

func planArtifactContainsLocalPrivatePath(value string) bool {
	normalized := strings.ReplaceAll(value, "\\", "/")
	localPathMarkers := []string{
		"/users/",
		"/home/",
		"/volumes/",
		"/private/",
		"c:/users/",
	}
	for _, marker := range localPathMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	for _, token := range strings.Fields(normalized) {
		token = strings.Trim(token, "\"'`()[]{}<>,.;")
		if strings.HasPrefix(token, "~/") {
			return true
		}
	}
	return false
}

func planArtifactHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
