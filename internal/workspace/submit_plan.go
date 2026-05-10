package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PlanEdit is one edit spec inside a plan-then-execute artifact. The
// planner emits these via the submit_plan tool; the executor phase
// iterates and dispatches each one against the workspace.
//
// The two edit shapes mirror edit_file's two shapes: substring
// (OldText/NewText) and range (StartLine/EndLine/Content). A spec
// must populate one of the shapes; submit_plan validates this before
// persisting the plan.
type PlanEdit struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text,omitempty"`
	NewText    string `json:"new_text,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Content    string `json:"content,omitempty"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
}

// PlanArtifact is the persisted shape of a submit_plan call. It
// captures the full ordered edit list plus the planner's rationale
// so the executor (and any reviewer) can read the plan as a single
// document.
//
// PackID, when non-empty, is the content-addressed handle of the
// graph_views cache row the planner used when drafting the plan. The
// executor can rehydrate that packet via comprehension.LoadView
// instead of re-discovering the same files. Optional and additive —
// older v1 artifacts without it remain readable.
//
// SessionID identifies the idiom session that produced this plan.
// Two concurrent plan-execute runs in the same workspace race on the
// shared `.idiom/plans/` directory; without an explicit session
// attribution, the executor phase's findLatestPlanArtifact would
// return whichever plan happened to be written most recently — even
// if it came from another session entirely. Stamping the SessionID
// here lets findLatestPlanArtifact filter to plans this session
// produced. Optional and additive: legacy artifacts have an empty
// SessionID and are still loadable when the executor isn't filtering.
type PlanArtifact struct {
	SchemaVersion string     `json:"schema_version"`
	Created       string     `json:"created"`
	Rationale     string     `json:"rationale,omitempty"`
	PackID        string     `json:"pack_id,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	Edits         []PlanEdit `json:"edits"`
}

// PlanArtifactSchemaVersion is the version stamp written to every
// plan artifact. Bump when the field set changes.
const PlanArtifactSchemaVersion = "idiom.plan.v1"

// submitPlan persists the planner's edit-plan artifact to disk and
// returns the path so the caller (orchestrator) can find and execute
// it. The artifact lives under .idiom/plans/<timestamp>.json by
// default.
func (m *Manager) submitPlan(req ToolRequest) ToolResult {
	if len(req.Edits) == 0 {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "submit_plan requires at least one edit in the edits array"}
	}
	for i, edit := range req.Edits {
		if err := validatePlanEdit(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
	}
	artifact := PlanArtifact{
		SchemaVersion: PlanArtifactSchemaVersion,
		Created:       time.Now().UTC().Format(time.RFC3339Nano),
		Rationale:     strings.TrimSpace(req.Rationale),
		PackID:        strings.TrimSpace(req.PackID),
		SessionID:     strings.TrimSpace(m.cfg.SessionID),
		Edits:         req.Edits,
	}
	dir := m.planArtifactDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("create plan dir: %s", err.Error())}
	}
	stamp := time.Now().UTC().Format("20060102-150405.000")
	stamp = strings.ReplaceAll(stamp, ".", "")
	path := filepath.Join(dir, stamp+".plan.json")
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("marshal artifact: %s", err.Error())}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("write artifact: %s", err.Error())}
	}
	pointer := filepath.Join(dir, "last.plan.json")
	_ = os.WriteFile(pointer, body, 0o644)
	return ToolResult{
		Tool:    "submit_plan",
		Summary: fmt.Sprintf("Plan submitted with %d edit(s); the executor phase will dispatch them.", len(artifact.Edits)),
		Output:  fmt.Sprintf("plan_path: %s\nedits: %d\nschema_version: %s", path, len(artifact.Edits), PlanArtifactSchemaVersion),
	}
}

// validatePlanEdit ensures one of the recognized edit shapes is
// populated. Mirrors the contract delegate_edit and edit_file
// enforce — if the planner's spec is malformed, fail loudly here so
// the executor never sees a half-formed edit.
func validatePlanEdit(edit PlanEdit) error {
	if strings.TrimSpace(edit.Path) == "" {
		return fmt.Errorf("path is required")
	}
	hasSubstring := strings.TrimSpace(edit.OldText) != "" && strings.TrimSpace(edit.NewText) != ""
	hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && strings.TrimSpace(edit.Content) != ""
	hasReplace := strings.TrimSpace(edit.NewText) != "" && edit.ReplaceAll
	if !hasSubstring && !hasRange && !hasReplace {
		return fmt.Errorf("requires either old_text+new_text or start_line+end_line+content")
	}
	return nil
}

// planArtifactDir returns the directory plan artifacts live in,
// rooted at the workspace's repo root.
func (m *Manager) planArtifactDir() string {
	root := strings.TrimSpace(m.cfg.Root)
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".idiom", "plans")
}

// LoadPlanArtifact reads a plan artifact from disk and returns it.
// Used by the plan-execute orchestrator's executor phase.
func LoadPlanArtifact(path string) (PlanArtifact, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return PlanArtifact{}, err
	}
	var artifact PlanArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("decode plan artifact: %w", err)
	}
	if artifact.SchemaVersion != PlanArtifactSchemaVersion {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q has schema_version=%q, expected %q", path, artifact.SchemaVersion, PlanArtifactSchemaVersion)
	}
	if len(artifact.Edits) == 0 {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q is empty", path)
	}
	return artifact, nil
}
