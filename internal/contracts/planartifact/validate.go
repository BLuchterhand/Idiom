package planartifact

import (
	"fmt"
	"path/filepath"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

type ValidationError struct {
	Errors []PlanArtifactSchemaError `json:"errors"`
}

func (e *ValidationError) Error() string {
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
		field := firstNonEmpty(strings.TrimSpace(item.Field), "(root)")
		code := firstNonEmpty(strings.TrimSpace(item.Code), "invalid")
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

func ValidatePlanArtifact(artifact PlanArtifact) error {
	var errs []PlanArtifactSchemaError

	if !schemaIsSupported(artifact.SchemaVersion) {
		errs = appendSchemaError(errs, "schema_version", "unsupported", "expected current or legacy plan artifact schema")
	}

	if schemaIsStructured(artifact.SchemaVersion) {
		if strings.TrimSpace(artifact.PlanID) == "" {
			errs = appendSchemaError(errs, "plan_id", "required", "plan id is required")
		}
		if strings.TrimSpace(artifact.Task.ID) == "" && strings.TrimSpace(artifact.Task.Summary) == "" {
			errs = appendSchemaError(errs, "task", "required", "task id or bounded summary is required")
		}
		if strings.TrimSpace(string(artifact.Contract.Kind)) == "" {
			errs = appendSchemaError(errs, "contract.kind", "required", "contract kind is required")
		} else if !KnownContractKind(string(artifact.Contract.Kind)) {
			errs = appendSchemaError(errs, "contract.kind", "unsupported", "contract kind must be plan_then_execute, implementation, or implementation_result")
		}
		if strings.TrimSpace(string(artifact.Contract.StrategyOwner)) != "" && !KnownOwner(string(artifact.Contract.StrategyOwner)) {
			errs = appendSchemaError(errs, "contract.strategy_owner", "unsupported", "strategy owner must be model, harness, planner, or executor")
		}
		if strings.TrimSpace(string(artifact.Contract.ContractOwner)) != "" && !KnownOwner(string(artifact.Contract.ContractOwner)) {
			errs = appendSchemaError(errs, "contract.contract_owner", "unsupported", "contract owner must be model, harness, planner, or executor")
		}
		if strings.TrimSpace(string(artifact.Contract.VerificationOwner)) != "" && !KnownOwner(string(artifact.Contract.VerificationOwner)) {
			errs = appendSchemaError(errs, "contract.verification_owner", "unsupported", "verification owner must be model, harness, planner, or executor")
		}
		if strings.TrimSpace(string(artifact.Packet.Status)) == "" {
			errs = appendSchemaError(errs, "packet.status", "required", "packet status is required")
		} else if !KnownPacketStatus(string(artifact.Packet.Status)) {
			errs = appendSchemaError(errs, "packet.status", "unsupported", "packet status must be provided, not_provided, available, fresh, or not_used")
		}
		if len(artifact.Paths.Editable) == 0 && len(artifact.Paths.Required) == 0 && len(artifact.Paths.Changed) == 0 {
			errs = appendSchemaError(errs, "paths", "required", "editable, required, or changed paths are required")
		}
		if len(artifact.Evidence) == 0 {
			errs = appendSchemaError(errs, "evidence", "required", "at least one bounded evidence reference is required")
		}
		if len(artifact.VerificationObligations) == 0 {
			errs = appendSchemaError(errs, "verification_obligations", "required", "at least one verification obligation is required")
		}
		for i, obligation := range artifact.VerificationObligations {
			if strings.TrimSpace(string(obligation.Owner)) != "" && !KnownOwner(string(obligation.Owner)) {
				errs = appendSchemaError(errs, fmt.Sprintf("verification_obligations[%d].owner", i), "unsupported", "verification owner must be model, harness, planner, or executor")
			}
			if strings.TrimSpace(string(obligation.Status)) == "" {
				errs = appendSchemaError(errs, fmt.Sprintf("verification_obligations[%d].status", i), "required", "verification status is required")
			} else if !KnownVerificationStatus(string(obligation.Status)) {
				errs = appendSchemaError(errs, fmt.Sprintf("verification_obligations[%d].status", i), "unsupported", "verification status is not recognized")
			}
		}
		if strings.TrimSpace(string(artifact.Approval.Status)) == "" {
			errs = appendSchemaError(errs, "approval.status", "required", "approval status is required")
		} else if !KnownApprovalStatus(string(artifact.Approval.Status)) {
			errs = appendSchemaError(errs, "approval.status", "unsupported", "approval status is not recognized")
		}
		if strings.TrimSpace(artifact.Approval.ScopeID) == "" {
			errs = appendSchemaError(errs, "approval.scope_id", "required", "approval scope id is required")
		}
		if strings.TrimSpace(string(artifact.Approval.Mode)) != "" && !KnownApprovalMode(string(artifact.Approval.Mode)) {
			errs = appendSchemaError(errs, "approval.mode", "unsupported", "approval mode is not recognized")
		}
		if strings.TrimSpace(string(artifact.Approval.ExpirationPolicy)) != "" && !KnownApprovalExpirationPolicy(string(artifact.Approval.ExpirationPolicy)) {
			errs = appendSchemaError(errs, "approval.expiration_policy", "unsupported", "approval expiration policy is not recognized")
		}
		if strings.TrimSpace(string(artifact.Approval.ExecutionPermission)) == "" {
			errs = appendSchemaError(errs, "approval.execution_permission", "required", "approval execution permission is required")
		} else if !KnownExecutionPermission(string(artifact.Approval.ExecutionPermission)) {
			errs = appendSchemaError(errs, "approval.execution_permission", "unsupported", "approval execution permission must be execute_all, execute_approved_scope, or blocked")
		}
		if IsApprovalStatusPartial(string(artifact.Approval.Status)) {
			if len(artifact.Approval.ApprovedPaths) == 0 {
				errs = appendSchemaError(errs, "approval.approved_paths", "required", "partial approval requires explicit approved scope paths")
			}
			if len(artifact.Approval.PendingPaths) == 0 {
				errs = appendSchemaError(errs, "approval.pending_paths", "required", "partial approval requires explicit pending scope paths")
			}
			if artifact.Approval.ExecutionPermission != ExecutionPermissionExecuteApprovedScope && artifact.Approval.ExecutionPermission != ExecutionPermissionBlocked {
				errs = appendSchemaError(errs, "approval.execution_permission", "invalid_partial_permission", "partial approval must execute only approved scope or remain blocked")
			}
		}
		if strings.TrimSpace(artifact.Handoff.ID) == "" {
			errs = appendSchemaError(errs, "handoff.id", "required", "handoff id is required")
		}
		if strings.TrimSpace(string(artifact.Handoff.From)) != "" && !KnownOwner(string(artifact.Handoff.From)) {
			errs = appendSchemaError(errs, "handoff.from", "unsupported", "handoff owner must be model, harness, planner, or executor")
		}
		if strings.TrimSpace(string(artifact.Handoff.To)) != "" && !KnownHandoffTarget(string(artifact.Handoff.To)) {
			errs = appendSchemaError(errs, "handoff.to", "unsupported", "handoff target must be plan_execute_executor or executor")
		}
		if strings.TrimSpace(string(artifact.Handoff.Status)) == "" {
			errs = appendSchemaError(errs, "handoff.status", "required", "handoff status is required")
		} else if !KnownHandoffStatus(string(artifact.Handoff.Status)) {
			errs = appendSchemaError(errs, "handoff.status", "unsupported", "handoff status must be ready or blocked")
		}
		if artifact.Handoff.Ready && artifact.Handoff.Status != HandoffStatusReady {
			errs = appendSchemaError(errs, "handoff.ready", "invalid_status", "ready handoff requires status ready")
		}
		if !artifact.Handoff.Ready && artifact.Handoff.Status == HandoffStatusReady {
			errs = appendSchemaError(errs, "handoff.ready", "invalid_ready_flag", "status ready requires ready flag true")
		}
		if artifact.Handoff.Status == HandoffStatusReady && !handoffExecutable(artifact) {
			errs = appendSchemaError(errs, "handoff.status", "not_executable", "status ready requires executable approval permission and no blockers")
		}
		if artifact.Handoff.Ready && len(artifact.Handoff.Blockers) > 0 {
			errs = appendSchemaError(errs, "handoff.blockers", "invalid_ready_blockers", "ready handoff must not carry blockers")
		}
		if !artifact.Handoff.Ready && artifact.Handoff.Status == HandoffStatusBlocked && len(artifact.Handoff.Blockers) == 0 {
			errs = appendSchemaError(errs, "handoff.blockers", "required", "blocked handoff requires at least one blocker")
		}
	}

	if len(artifact.Edits) == 0 {
		errs = appendSchemaError(errs, "edits", "required", "at least one executable edit is required")
	}

	for i, edit := range artifact.Edits {
		if err := ValidatePlanEditShape(edit); err != nil {
			field := fmt.Sprintf("edits[%d]", i)
			if path := strings.TrimSpace(edit.Path); path != "" {
				field = fmt.Sprintf("edits[%d].path:%s", i, path)
			}
			errs = appendSchemaError(errs, field, "invalid_edit_shape", err.Error())
		}
		if path := strings.TrimSpace(edit.Path); path != "" && !PathIsSafe(path) {
			errs = appendSchemaError(errs, fmt.Sprintf("edits[%d].path", i), "invalid_path", "path must stay relative to the workspace")
		}
	}

	validatePathList(&errs, "paths.editable", artifact.Paths.Editable)
	validatePathList(&errs, "paths.required", artifact.Paths.Required)
	validatePathList(&errs, "paths.source_satisfied", artifact.Paths.SourceSatisfied)
	validatePathList(&errs, "paths.read_only_evidence", artifact.Paths.ReadOnlyEvidence)
	validatePathList(&errs, "paths.forbidden", artifact.Paths.Forbidden)
	validatePathList(&errs, "paths.changed", artifact.Paths.Changed)
	validatePathList(&errs, "contract.required_edit_paths", artifact.Contract.RequiredEditPaths)
	validatePathList(&errs, "contract.forbidden_paths", artifact.Contract.ForbiddenPaths)
	validatePathList(&errs, "approval.approved_paths", artifact.Approval.ApprovedPaths)
	validatePathList(&errs, "approval.pending_paths", artifact.Approval.PendingPaths)

	for i, classification := range artifact.Task.ClientClassifications {
		if !KnownClientClassification(classification) {
			errs = appendSchemaError(errs, fmt.Sprintf("task.client_classifications[%d]", i), "unsupported_client_classification", "client classification must be one of the approved values")
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

func ValidatePlanEditShape(edit PlanEdit) error {
	if strings.TrimSpace(edit.Path) == "" {
		return fmt.Errorf("path is required")
	}

	_, err := ClassifyPlanEditShape(edit)
	return err
}

func ClassifyPlanEditShape(edit PlanEdit) (PlanEditShape, error) {
	hasOldText := strings.TrimSpace(edit.OldText) != ""
	hasNewText := strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != ""
	hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
	hasInlineContent := strings.TrimSpace(edit.Content) != ""
	hasSubstring := hasOldText && PlanEditHasExplicitReplacementText(edit)
	hasLineFields := edit.StartLine > 0 || edit.EndLine > 0
	hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
	hasInsert := edit.InsertAfterLine > 0 && hasInlineContent
	hasReplaceAll := !hasOldText && edit.ReplaceAll && hasNewText

	if edit.StartLine < 0 {
		return "", fmt.Errorf("start_line must be >= 1")
	}
	if edit.EndLine < 0 {
		return "", fmt.Errorf("end_line must be >= 1")
	}
	if edit.InsertAfterLine < 0 {
		return "", fmt.Errorf("insert_after_line must be >= 1")
	}
	if edit.InsertAfterLine > 0 {
		if strings.TrimSpace(edit.ContentRef) != "" {
			return "", fmt.Errorf("insert_after_line edits do not support content_ref in this submit_plan shape; inline non-empty content is required")
		}
		if !hasInlineContent {
			return "", fmt.Errorf("insert_after_line edits require non-empty content")
		}
	}
	if hasOldText && !hasSubstring {
		return "", fmt.Errorf("substring edits require new_text or new_text_ref; use explicit empty new_text to delete old_text")
	}
	if (edit.StartLine > 0 || edit.EndLine > 0) && !hasContent {
		return "", fmt.Errorf("line-range edits require non-empty content; to delete text, use old_text with empty new_text instead")
	}
	if edit.StartLine == 0 && edit.EndLine > 0 {
		return "", fmt.Errorf("line-range edits require start_line when end_line is set")
	}
	if edit.StartLine > 0 && edit.EndLine > 0 && edit.EndLine < edit.StartLine {
		return "", fmt.Errorf("line-range edits require end_line >= start_line")
	}

	shapes := make([]PlanEditShape, 0, 2)
	if hasSubstring {
		shapes = append(shapes, PlanEditShapeSubstring)
	}
	if hasRange {
		shapes = append(shapes, PlanEditShapeLineRange)
	}
	if hasInsert {
		shapes = append(shapes, PlanEditShapeInsertAfterLine)
	}
	if hasReplaceAll {
		shapes = append(shapes, PlanEditShapeReplaceAll)
	}

	if len(shapes) == 0 {
		return "", fmt.Errorf("requires one executable edit shape: old_text+new_text, start_line+end_line+content, insert_after_line+content, or replace_all=true with new_text and empty old_text for missing/new-file writes. Existing source/test files need bounded anchors. If already final, remove it from edits[] and use source_satisfied_paths")
	}
	if len(shapes) > 1 {
		return "", fmt.Errorf("plan edit shapes are mutually exclusive; got %s", joinPlanEditShapeNames(shapes))
	}

	shape := shapes[0]
	switch shape {
	case PlanEditShapeSubstring:
		if hasLineFields || edit.InsertAfterLine > 0 || hasContent {
			return "", fmt.Errorf("substring edits are mutually exclusive with start_line/end_line, insert_after_line, and content/content_ref shapes")
		}
	case PlanEditShapeLineRange:
		if hasOldText || hasNewText || edit.InsertAfterLine > 0 || edit.ReplaceAll {
			return "", fmt.Errorf("line-range edits are mutually exclusive with old_text/new_text, insert_after_line, and replace_all shapes")
		}
	case PlanEditShapeInsertAfterLine:
		if hasOldText || hasNewText || hasLineFields || edit.ReplaceAll {
			return "", fmt.Errorf("insert_after_line edits are mutually exclusive with old_text/new_text, start_line/end_line, and replace_all shapes")
		}
	case PlanEditShapeReplaceAll:
		if hasLineFields || edit.InsertAfterLine > 0 || hasContent {
			return "", fmt.Errorf("replace_all edits with empty old_text are mutually exclusive with start_line/end_line, insert_after_line, and content/content_ref shapes")
		}
	default:
		return "", fmt.Errorf("unknown plan edit shape %q", shape)
	}

	return shape, nil
}

func joinPlanEditShapeNames(shapes []PlanEditShape) string {
	names := make([]string, 0, len(shapes))
	for _, shape := range shapes {
		names = append(names, string(shape))
	}
	return strings.Join(names, ",")
}

func SourceSatisfiedEvidenceRefs(artifact PlanArtifact) []plancontract.SourceSatisfiedEvidenceRef {
	refs := make([]plancontract.SourceSatisfiedEvidenceRef, 0, len(artifact.Evidence))
	for _, evidence := range artifact.Evidence {
		if !plancontract.SourceSatisfiedEvidenceKindProvesCurrentState(plancontract.SourceSatisfiedEvidenceKind(evidence.Kind)) {
			continue
		}
		refs = append(refs, plancontract.SourceSatisfiedEvidenceRef{
			Path: evidence.Path,
			Kind: plancontract.SourceSatisfiedEvidenceKind(evidence.Kind),
		})
	}
	return refs
}

func PathIsSafe(path string) bool {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func schemaIsSupported(version string) bool {
	return schemaIsStructured(version) || version == LegacySchemaVersion
}

func schemaIsStructured(version string) bool {
	switch strings.TrimSpace(version) {
	case SchemaVersion, PreviousSchemaVersion:
		return true
	default:
		return false
	}
}

func appendSchemaError(errs []PlanArtifactSchemaError, field string, code string, detail string) []PlanArtifactSchemaError {
	return append(errs, PlanArtifactSchemaError{
		Field:  field,
		Code:   code,
		Detail: detail,
	})
}

func validatePathList(errs *[]PlanArtifactSchemaError, field string, paths []string) {
	for i, path := range paths {
		if !PathIsSafe(path) {
			*errs = appendSchemaError(*errs, fmt.Sprintf("%s[%d]", field, i), "invalid_path", "path must stay relative to the workspace")
		}
	}
}

func firstNonEmpty(primary string, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}
