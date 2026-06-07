package planintent

import (
	"fmt"
	"sort"
	"strings"

	"idiom/internal/contracts/plancontract"
)

const SchemaVersion = "idiom.edit_intent.v1"

type Source string

const (
	SourcePlanner           Source = "planner"
	SourceArtifactDistiller Source = "artifact_distiller"
)

type Operation string

const (
	OperationReplaceSymbolBody     Operation = "replace_symbol_body"
	OperationInsertAfterSymbol     Operation = "insert_after_symbol"
	OperationInsertAfterLineAnchor Operation = "insert_after_line_anchor"
	OperationReplaceExactText      Operation = "replace_exact_text"
	OperationAddNewFile            Operation = "add_new_file"
	OperationAddTestCase           Operation = "add_test_case"
	OperationDeleteExactText       Operation = "delete_exact_text"
)

const (
	CodeInvalidSchemaVersion   = "invalid_schema_version"
	CodeInvalidSource          = "invalid_source"
	CodeInvalidScope           = "invalid_scope"
	CodeRequiredScopeMismatch  = "required_scope_mismatch"
	CodeCandidateScopeRejected = "candidate_scope_rejected"
	CodeIntentScopeRejected    = "intent_scope_rejected"
	CodeInvalidOperation       = "invalid_operation"
	CodeInvalidIntentShape     = "invalid_intent_shape"
	CodeInvalidEvidenceRef     = "invalid_evidence_ref"
	CodeInvalidBlocker         = "invalid_blocker"
)

type Artifact struct {
	SchemaVersion string    `json:"schema_version"`
	Source        Source    `json:"source"`
	TaskSummary   string    `json:"task_summary,omitempty"`
	Scope         Scope     `json:"scope"`
	Intents       []Intent  `json:"intents,omitempty"`
	Blockers      []Blocker `json:"blockers,omitempty"`
}

type Scope struct {
	RequiredEditPaths  []string `json:"required_edit_paths,omitempty"`
	CandidateEditPaths []string `json:"candidate_edit_paths,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
}

type Intent struct {
	Path             string    `json:"path"`
	Operation        Operation `json:"operation"`
	Symbol           string    `json:"symbol,omitempty"`
	DesiredBehavior  string    `json:"desired_behavior,omitempty"`
	Constraints      []string  `json:"constraints,omitempty"`
	CandidateCodeRef string    `json:"candidate_code_ref,omitempty"`
	EvidenceRefs     []string  `json:"evidence_refs,omitempty"`
	OldText          string    `json:"old_text,omitempty"`
	NewText          string    `json:"new_text,omitempty"`
	LineAnchor       string    `json:"line_anchor,omitempty"`
	Content          string    `json:"content,omitempty"`
	NewFileContent   string    `json:"new_file_content,omitempty"`
	Purpose          string    `json:"purpose,omitempty"`
}

type Blocker struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Paths        []string `json:"paths,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type Contract struct {
	RequiredEditPaths  []string
	CandidateEditPaths []string
}

type ValidationError struct {
	Code    string
	Field   string
	Message string
}

func (err *ValidationError) Error() string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Field) == "" {
		return fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	return fmt.Sprintf("%s: %s: %s", err.Code, err.Field, err.Message)
}

func Validate(artifact Artifact, contract Contract) error {
	if artifact.SchemaVersion != SchemaVersion {
		return validationError(CodeInvalidSchemaVersion, "schema_version", "must be "+SchemaVersion)
	}
	if artifact.Source != SourcePlanner && artifact.Source != SourceArtifactDistiller {
		return validationError(CodeInvalidSource, "source", "must be planner or artifact_distiller")
	}

	required, err := normalizePathList(artifact.Scope.RequiredEditPaths)
	if err != nil {
		return validationError(CodeInvalidScope, "scope.required_edit_paths", err.Error())
	}
	contractRequired, err := normalizePathList(contract.RequiredEditPaths)
	if err != nil {
		return validationError(CodeInvalidScope, "contract.required_edit_paths", err.Error())
	}
	if !sameStringSet(required, contractRequired) {
		return validationError(CodeRequiredScopeMismatch, "scope.required_edit_paths", "must match the active contract exactly")
	}

	candidates, err := normalizePathList(artifact.Scope.CandidateEditPaths)
	if err != nil {
		return validationError(CodeInvalidScope, "scope.candidate_edit_paths", err.Error())
	}
	contractCandidates, err := normalizePathList(contract.CandidateEditPaths)
	if err != nil {
		return validationError(CodeInvalidScope, "contract.candidate_edit_paths", err.Error())
	}
	allowed := pathSet(append(contractRequired, contractCandidates...))
	for _, path := range candidates {
		if _, ok := allowed[path]; !ok {
			return validationError(CodeCandidateScopeRejected, "scope.candidate_edit_paths", fmt.Sprintf("%s is not in active candidate scope", path))
		}
	}
	for _, path := range required {
		allowed[path] = struct{}{}
	}
	for _, path := range candidates {
		allowed[path] = struct{}{}
	}
	if err := validateEvidenceRefs("scope.evidence_refs", artifact.Scope.EvidenceRefs); err != nil {
		return err
	}

	if len(artifact.Intents) == 0 && len(artifact.Blockers) == 0 {
		return validationError(CodeInvalidIntentShape, "intents", "artifact must contain at least one intent or blocker")
	}
	if len(artifact.Intents) > 0 && len(artifact.Blockers) > 0 {
		return validationError(CodeInvalidBlocker, "blockers", "artifact must contain intents or blockers, not both")
	}
	for i, intent := range artifact.Intents {
		if err := validateIntent(i, intent, allowed); err != nil {
			return err
		}
	}
	for i, blocker := range artifact.Blockers {
		if err := validateBlocker(i, blocker, allowed); err != nil {
			return err
		}
	}
	return nil
}

func validateIntent(index int, intent Intent, allowed map[string]struct{}) error {
	field := func(name string) string {
		return fmt.Sprintf("intents[%d].%s", index, name)
	}
	path, err := normalizePath(intent.Path)
	if err != nil {
		return validationError(CodeInvalidScope, field("path"), err.Error())
	}
	if _, ok := allowed[path]; !ok {
		return validationError(CodeIntentScopeRejected, field("path"), fmt.Sprintf("%s is outside active edit scope", path))
	}
	if !operationAllowed(intent.Operation) {
		return validationError(CodeInvalidOperation, field("operation"), fmt.Sprintf("%s is not in edit_intent.v1 operation vocabulary", intent.Operation))
	}
	if err := validateEvidenceRefs(field("evidence_refs"), intent.EvidenceRefs); err != nil {
		return err
	}
	switch intent.Operation {
	case OperationReplaceExactText:
		if strings.TrimSpace(intent.OldText) == "" || strings.TrimSpace(intent.NewText) == "" {
			return validationError(CodeInvalidIntentShape, field("old_text"), "replace_exact_text requires non-empty old_text and new_text")
		}
		if intent.OldText == intent.NewText {
			return validationError(CodeInvalidIntentShape, field("new_text"), "replace_exact_text cannot be a no-op")
		}
	case OperationInsertAfterLineAnchor:
		if strings.TrimSpace(intent.LineAnchor) == "" || strings.TrimSpace(intent.Content) == "" {
			return validationError(CodeInvalidIntentShape, field("line_anchor"), "insert_after_line_anchor requires non-empty line_anchor and content")
		}
	case OperationAddNewFile:
		if strings.TrimSpace(intent.NewFileContent) == "" {
			return validationError(CodeInvalidIntentShape, field("new_file_content"), "add_new_file requires a complete non-empty file body")
		}
	case OperationDeleteExactText:
		if strings.TrimSpace(intent.OldText) == "" {
			return validationError(CodeInvalidIntentShape, field("old_text"), "delete_exact_text requires non-empty old_text")
		}
	}
	return nil
}

func validateBlocker(index int, blocker Blocker, allowed map[string]struct{}) error {
	field := func(name string) string {
		return fmt.Sprintf("blockers[%d].%s", index, name)
	}
	if strings.TrimSpace(blocker.Code) == "" || strings.TrimSpace(blocker.Message) == "" {
		return validationError(CodeInvalidBlocker, field("code"), "blockers require non-empty code and message")
	}
	for _, rawPath := range blocker.Paths {
		path, err := normalizePath(rawPath)
		if err != nil {
			return validationError(CodeInvalidScope, field("paths"), err.Error())
		}
		if _, ok := allowed[path]; !ok {
			return validationError(CodeIntentScopeRejected, field("paths"), fmt.Sprintf("%s is outside active edit scope", path))
		}
	}
	return validateEvidenceRefs(field("evidence_refs"), blocker.EvidenceRefs)
}

func operationAllowed(operation Operation) bool {
	switch operation {
	case OperationReplaceSymbolBody,
		OperationInsertAfterSymbol,
		OperationInsertAfterLineAnchor,
		OperationReplaceExactText,
		OperationAddNewFile,
		OperationAddTestCase,
		OperationDeleteExactText:
		return true
	default:
		return false
	}
}

func validateEvidenceRefs(field string, refs []string) error {
	for i, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.ContainsRune(ref, 0) || strings.Contains(ref, "\n") || strings.Contains(ref, "\r") {
			return validationError(CodeInvalidEvidenceRef, fmt.Sprintf("%s[%d]", field, i), "evidence refs must be non-empty single-line strings")
		}
	}
	return nil
}

func normalizePathList(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, raw := range paths {
		path, err := normalizePath(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func normalizePath(path string) (string, error) {
	normalized := plancontract.NormalizeWorkspacePath(path)
	if normalized == "" {
		return "", fmt.Errorf("path must stay relative to the workspace")
	}
	return normalized, nil
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func pathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			out[path] = struct{}{}
		}
	}
	return out
}

func validationError(code string, field string, message string) error {
	return &ValidationError{Code: code, Field: field, Message: message}
}
