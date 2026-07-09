package planartifact

import (
	"encoding/json"
	"strings"
)

const (
	SchemaVersion         = "idiom.plan.v3"
	PreviousSchemaVersion = "idiom.plan.v2"
	LegacySchemaVersion   = "idiom.plan.v1"
)

// PlanEdit describes one executable edit shape in a durable plan artifact.
// Exactly one supported shape is expected after validation: substring replace,
// line range replace, insert-after-line, or replace-all/new-file content.
type PlanEdit struct {
	Path             string                    `json:"path"`
	OldText          string                    `json:"old_text,omitempty"`
	NewText          string                    `json:"new_text"`
	NewTextRef       string                    `json:"new_text_ref,omitempty"`
	StartLine        int                       `json:"start_line,omitempty"`
	EndLine          int                       `json:"end_line,omitempty"`
	InsertAfterLine  int                       `json:"insert_after_line,omitempty"`
	Content          string                    `json:"content,omitempty"`
	ContentRef       string                    `json:"content_ref,omitempty"`
	ReplaceAll       bool                      `json:"replace_all,omitempty"`
	Purpose          string                    `json:"purpose,omitempty"`
	AnchorProvenance *PlanEditAnchorProvenance `json:"anchor_provenance,omitempty"`

	decodedFromJSON bool
	newTextSet      bool
}

type PlanEditShape string

const (
	PlanEditShapeSubstring       PlanEditShape = "substring"
	PlanEditShapeLineRange       PlanEditShape = "line_range"
	PlanEditShapeInsertAfterLine PlanEditShape = "insert_after_line"
	PlanEditShapeReplaceAll      PlanEditShape = "replace_all"
)

func (edit *PlanEdit) UnmarshalJSON(data []byte) error {
	type planEditAlias PlanEdit

	var decoded planEditAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	*edit = PlanEdit(decoded)
	edit.decodedFromJSON = true
	_, edit.newTextSet = fields["new_text"]

	return nil
}

func PlanEditHasExplicitReplacementText(edit PlanEdit) bool {
	if strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "" {
		return true
	}
	return edit.newTextSet
}

func MarkLegacyDeletionEditsExplicit(edits []PlanEdit) []PlanEdit {
	out := append([]PlanEdit(nil), edits...)
	for i := range out {
		if strings.TrimSpace(out[i].OldText) == "" || strings.TrimSpace(out[i].NewText) != "" || strings.TrimSpace(out[i].NewTextRef) != "" {
			continue
		}
		out[i].newTextSet = true
	}
	return out
}

type PlanEditAnchorProvenance struct {
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	Path          string `json:"path"`
	FileSHA256    string `json:"file_sha256"`
	AnchorSHA256  string `json:"anchor_sha256"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	MatchCount    int    `json:"match_count"`
}

// PlanArtifact is the normalized durable contract produced by submit_plan and
// consumed by plan-execute executors.
type PlanArtifact struct {
	SchemaVersion           string                               `json:"schema_version"`
	PlanID                  string                               `json:"plan_id,omitempty"`
	Created                 string                               `json:"created"`
	Rationale               string                               `json:"rationale,omitempty"`
	PackID                  string                               `json:"pack_id,omitempty"`
	SessionID               string                               `json:"session_id,omitempty"`
	Task                    PlanArtifactTask                     `json:"task"`
	Contract                PlanArtifactContract                 `json:"contract"`
	Packet                  PlanArtifactPacket                   `json:"packet"`
	Paths                   PlanArtifactPaths                    `json:"paths"`
	Evidence                []PlanArtifactEvidence               `json:"evidence,omitempty"`
	UnresolvedQuestions     []PlanArtifactQuestion               `json:"unresolved_questions,omitempty"`
	VerificationObligations []PlanArtifactVerificationObligation `json:"verification_obligations"`
	Approval                PlanArtifactApproval                 `json:"approval"`
	Handoff                 PlanArtifactHandoff                  `json:"handoff"`
	LegacyProse             string                               `json:"legacy_prose,omitempty"`
	Edits                   []PlanEdit                           `json:"edits"`
}

type PlanArtifactTask struct {
	ID                    string   `json:"id,omitempty"`
	Summary               string   `json:"summary,omitempty"`
	ClientClassifications []string `json:"client_classifications,omitempty"`
}

type PlanArtifactContract struct {
	Kind              ContractKind `json:"kind,omitempty"`
	StrategyOwner     Owner        `json:"strategy_owner,omitempty"`
	ContractOwner     Owner        `json:"contract_owner,omitempty"`
	PermissionProfile string       `json:"permission_profile,omitempty"`
	VerificationOwner Owner        `json:"verification_owner,omitempty"`
	RequiredEditPaths []string     `json:"required_edit_paths,omitempty"`
	ForbiddenPaths    []string     `json:"forbidden_paths,omitempty"`
}

type PlanArtifactPacket struct {
	PackID       string       `json:"pack_id,omitempty"`
	Status       PacketStatus `json:"status,omitempty"`
	IndexID      string       `json:"index_id,omitempty"`
	Freshness    string       `json:"freshness,omitempty"`
	ArtifactPath string       `json:"artifact_path,omitempty"`
}

type PlanArtifactPaths struct {
	Editable         []string `json:"editable,omitempty"`
	Required         []string `json:"required,omitempty"`
	SourceSatisfied  []string `json:"source_satisfied,omitempty"`
	ReadOnlyEvidence []string `json:"read_only_evidence,omitempty"`
	Forbidden        []string `json:"forbidden,omitempty"`
	Changed          []string `json:"changed,omitempty"`
}

// PlanArtifactEvidence records supporting evidence for a plan artifact. Only
// current-state evidence kinds are promoted into source-satisfied coverage.
type PlanArtifactEvidence struct {
	ID         string       `json:"id,omitempty"`
	Kind       EvidenceKind `json:"kind,omitempty"`
	Handle     string       `json:"handle,omitempty"`
	Path       string       `json:"path,omitempty"`
	Summary    string       `json:"summary,omitempty"`
	Freshness  string       `json:"freshness,omitempty"`
	Confidence string       `json:"confidence,omitempty"`
}

type PlanArtifactQuestion struct {
	ID                    string `json:"id,omitempty"`
	Claim                 string `json:"claim,omitempty"`
	Probe                 string `json:"probe,omitempty"`
	Status                string `json:"status,omitempty"`
	RequiredBeforeHandoff bool   `json:"required_before_handoff,omitempty"`
}

type PlanArtifactVerificationObligation struct {
	ID       string             `json:"id,omitempty"`
	Kind     VerificationKind   `json:"kind,omitempty"`
	Command  string             `json:"command,omitempty"`
	Path     string             `json:"path,omitempty"`
	Owner    Owner              `json:"owner,omitempty"`
	Status   VerificationStatus `json:"status,omitempty"`
	Required bool               `json:"required,omitempty"`
}

type PlanArtifactApproval struct {
	Required            bool                     `json:"required"`
	Status              ApprovalStatus           `json:"status,omitempty"`
	ScopeID             string                   `json:"scope_id,omitempty"`
	Mode                ApprovalMode             `json:"mode,omitempty"`
	ApprovedBy          string                   `json:"approved_by,omitempty"`
	ApprovedAt          string                   `json:"approved_at,omitempty"`
	ExpirationPolicy    ApprovalExpirationPolicy `json:"expiration_policy,omitempty"`
	ApprovedPaths       []string                 `json:"approved_paths,omitempty"`
	PendingPaths        []string                 `json:"pending_paths,omitempty"`
	ExecutionPermission ExecutionPermission      `json:"execution_permission,omitempty"`
}

type PlanArtifactHandoff struct {
	ID       string        `json:"id,omitempty"`
	From     Owner         `json:"from,omitempty"`
	To       HandoffTarget `json:"to,omitempty"`
	Status   HandoffStatus `json:"status,omitempty"`
	Ready    bool          `json:"ready"`
	Blockers []string      `json:"blockers,omitempty"`
}

type PlanArtifactSchemaError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}
