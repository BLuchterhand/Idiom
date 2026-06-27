package recovery

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	FailureRecordSchemaVersion    = "idiom.recovery.failure_record.v1"
	failureRecordComponentMaxSize = 128
)

type FailureRecord struct {
	SchemaVersion  string            `json:"schema_version"`
	FailureID      string            `json:"failure_id"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	SourceRun      SourceRun         `json:"source_run"`
	LockedContract *ContractSnapshot `json:"locked_contract,omitempty"`
	Failure        Failure           `json:"failure"`
	PartialState   PartialState      `json:"partial_state"`
	NextAction     NextAction        `json:"next_valid_action"`
}

type SourceRun struct {
	Lane              string   `json:"lane"`
	SessionID         string   `json:"session_id"`
	ProgressStatePath string   `json:"progress_state_path,omitempty"`
	PromptSHA256      string   `json:"prompt_sha256,omitempty"`
	PromptFile        string   `json:"prompt_file,omitempty"`
	PlanPath          string   `json:"plan_path,omitempty"`
	PlanID            string   `json:"plan_id,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	ParentAtomID      string   `json:"parent_atom_id,omitempty"`
	ParentAtomTitle   string   `json:"parent_atom_title,omitempty"`
	RequiredEditPaths []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths  []string `json:"allowed_edit_paths,omitempty"`
}

type Failure struct {
	Class             FailureClass       `json:"class"`
	Kind              string             `json:"kind,omitempty"`
	Phase             string             `json:"phase,omitempty"`
	Summary           string             `json:"summary"`
	EvidenceRefs      []EvidenceRef      `json:"evidence_refs,omitempty"`
	InvariantFailures []InvariantFailure `json:"invariant_failures,omitempty"`
}

type EvidenceRef struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	ID      string `json:"id,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type ContractSnapshot struct {
	ContractID           string   `json:"contract_id,omitempty"`
	ContractHash         string   `json:"contract_hash,omitempty"`
	SnapshotID           string   `json:"snapshot_id,omitempty"`
	SnapshotPath         string   `json:"snapshot_path,omitempty"`
	RuntimeContractID    string   `json:"runtime_contract_id,omitempty"`
	ProtocolHash         string   `json:"protocol_hash,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths     []string `json:"allowed_edit_paths,omitempty"`
	WriteAllowedPaths    []string `json:"write_allowed_paths,omitempty"`
	WriteAllowedExplicit bool     `json:"write_allowed_explicit,omitempty"`
}

type InvariantFailure struct {
	InvariantID            string   `json:"invariant_id"`
	FieldPath              string   `json:"field_path,omitempty"`
	Path                   string   `json:"path,omitempty"`
	Expected               string   `json:"expected,omitempty"`
	Actual                 string   `json:"actual,omitempty"`
	RequiredRepairEvidence []string `json:"required_repair_evidence,omitempty"`
}

type PartialState struct {
	DirtyStatus  DirtyStatus `json:"dirty_status"`
	ChangedPaths []string    `json:"changed_paths,omitempty"`
	TotalEdits   int         `json:"total_edits,omitempty"`
	ScopeStatus  ScopeStatus `json:"scope_status"`
}

func ValidateFailureRecord(record FailureRecord) error {
	if record.SchemaVersion != FailureRecordSchemaVersion {
		return fmt.Errorf("unsupported schema %q", record.SchemaVersion)
	}
	if !ValidFailureRecordComponent(record.FailureID) {
		return fmt.Errorf("invalid failure_id %q", record.FailureID)
	}
	if strings.TrimSpace(record.SourceRun.Lane) == "" {
		return fmt.Errorf("source_run.lane is required")
	}
	if !KnownSourceLane(SourceLane(strings.TrimSpace(record.SourceRun.Lane))) {
		return fmt.Errorf("invalid source_run.lane %q", record.SourceRun.Lane)
	}
	if !ValidFailureRecordComponent(record.SourceRun.SessionID) {
		return fmt.Errorf("invalid source_run.session_id %q", record.SourceRun.SessionID)
	}
	if !KnownFailureClass(record.Failure.Class) {
		return fmt.Errorf("invalid failure.class %q", record.Failure.Class)
	}
	if strings.TrimSpace(record.Failure.Summary) == "" {
		return fmt.Errorf("failure.summary is required")
	}
	if _, ok := ParseNextAction(string(record.NextAction)); !ok {
		return fmt.Errorf("invalid next_valid_action %q", record.NextAction)
	}
	if err := validatePartialState(record.PartialState); err != nil {
		return err
	}
	if err := ValidateRecoveryPaths("source_run.required_edit_paths", record.SourceRun.RequiredEditPaths); err != nil {
		return err
	}
	if err := ValidateRecoveryPaths("source_run.allowed_edit_paths", record.SourceRun.AllowedEditPaths); err != nil {
		return err
	}
	if record.LockedContract != nil {
		if err := ValidateRecoveryPaths("locked_contract.required_edit_paths", record.LockedContract.RequiredEditPaths); err != nil {
			return err
		}
		if err := ValidateRecoveryPaths("locked_contract.allowed_edit_paths", record.LockedContract.AllowedEditPaths); err != nil {
			return err
		}
		if err := ValidateRecoveryPaths("locked_contract.write_allowed_paths", record.LockedContract.WriteAllowedPaths); err != nil {
			return err
		}
	}
	return ValidateEvidenceRefs("failure.evidence_refs", record.Failure.EvidenceRefs)
}

func ValidFailureRecordComponent(value string) bool {
	return validateFailureRecordComponent(value) == nil
}

func validateFailureRecordComponent(value string) error {
	if value == "" {
		return fmt.Errorf("component is required")
	}
	if len(value) > failureRecordComponentMaxSize {
		return fmt.Errorf("component length exceeds %d characters", failureRecordComponentMaxSize)
	}
	if strings.TrimSpace(value) != value || value == "." || value == ".." || filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) || strings.Contains(value, "..") {
		return fmt.Errorf("component must be a single safe path component")
	}

	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("component contains unsupported character %q", r)
		}
	}

	return nil
}

func validatePartialState(partial PartialState) error {
	if _, ok := ParseDirtyStatus(string(partial.DirtyStatus)); !ok {
		return fmt.Errorf("invalid partial_state.dirty_status %q", partial.DirtyStatus)
	}
	if _, ok := ParseScopeStatus(string(partial.ScopeStatus)); !ok {
		return fmt.Errorf("invalid partial_state.scope_status %q", partial.ScopeStatus)
	}
	if partial.TotalEdits < 0 {
		return fmt.Errorf("invalid partial_state.total_edits %d", partial.TotalEdits)
	}
	return ValidateRecoveryPaths("partial_state.changed_paths", partial.ChangedPaths)
}

func ValidateRecoveryPaths(field string, paths []string) error {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if normalizeRecoveryPath(path) == "" {
			return fmt.Errorf("%s contains unsafe path %q", field, path)
		}
	}
	return nil
}

func ValidateEvidenceRefs(field string, refs []EvidenceRef) error {
	for i, ref := range refs {
		if strings.TrimSpace(ref.Kind) == "" {
			return fmt.Errorf("%s[%d].kind is required", field, i)
		}
		if strings.TrimSpace(ref.Path) != "" && normalizeRecoveryPath(ref.Path) == "" {
			return fmt.Errorf("%s[%d].path is unsafe: %q", field, i, ref.Path)
		}
	}
	return nil
}

func normalizeRecoveryPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return ""
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}

	return clean
}
