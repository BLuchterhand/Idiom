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
// The edit shapes mirror edit_file's shapes plus the plan-execute
// whole-file/new-file shape: substring (OldText/NewText), range
// (StartLine/EndLine/Content), or ReplaceAll with NewText and empty
// OldText. A spec must populate one of the shapes; submit_plan
// validates this before persisting the plan.
type PlanEdit struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text,omitempty"`
	NewText    string `json:"new_text,omitempty"`
	NewTextRef string `json:"new_text_ref,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Content    string `json:"content,omitempty"`
	ContentRef string `json:"content_ref,omitempty"`
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

const planInlineTextSpillThreshold = 4096

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
		if err := m.validatePlanEditAgainstWorkspace(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
	}
	dir := m.planArtifactDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("create plan dir: %s", err.Error())}
	}
	stamp := time.Now().UTC().Format("20060102-150405.000")
	stamp = strings.ReplaceAll(stamp, ".", "")
	edits, spilled, err := spillLargePlanEditBodies(dir, stamp, req.Edits)
	if err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: err.Error()}
	}
	artifact := PlanArtifact{
		SchemaVersion: PlanArtifactSchemaVersion,
		Created:       time.Now().UTC().Format(time.RFC3339Nano),
		Rationale:     strings.TrimSpace(req.Rationale),
		PackID:        strings.TrimSpace(req.PackID),
		SessionID:     strings.TrimSpace(m.cfg.SessionID),
		Edits:         edits,
	}
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
		Output:  fmt.Sprintf("plan_path: %s\nedits: %d\nschema_version: %s\ncontent_refs: %d", path, len(artifact.Edits), PlanArtifactSchemaVersion, spilled),
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
	hasNewText := strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != ""
	hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
	hasSubstring := strings.TrimSpace(edit.OldText) != "" && hasNewText
	hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
	hasReplace := hasNewText && edit.ReplaceAll
	if !hasSubstring && !hasRange && !hasReplace {
		return fmt.Errorf("requires one edit shape: old_text+new_text, start_line+end_line+content, or replace_all=true with new_text and empty old_text for whole-file/new-file writes; large new_text/content may be represented by new_text_ref/content_ref")
	}
	return nil
}

func (m *Manager) validatePlanEditAgainstWorkspace(edit PlanEdit) error {
	oldText := edit.OldText
	if strings.TrimSpace(oldText) == "" {
		return nil
	}
	path, err := m.resolvePath(edit.Path, "")
	if err != nil {
		return fmt.Errorf("old_text preflight could not resolve path %q: %w", edit.Path, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("old_text preflight could not read %s: %w", edit.Path, err)
	}
	current := string(body)
	matchCount := strings.Count(current, oldText)
	if matchCount == 0 {
		if containsCaseFoldedSubstring(current, oldText) {
			return fmt.Errorf("old_text did not appear in %s, but a case-insensitive match exists; preserve exact casing from the file or use start_line/end_line+content", edit.Path)
		}
		return fmt.Errorf("old_text did not appear in %s; copy the exact substring from the file or use start_line/end_line+content", edit.Path)
	}
	if matchCount > 1 && !edit.ReplaceAll {
		return fmt.Errorf("old_text matched %d locations in %s; submit_plan rejects ambiguous substring edits before executor writes. Extend old_text with surrounding context, use start_line/end_line+content, or set replace_all=true when every occurrence is intended", matchCount, edit.Path)
	}
	return nil
}

func spillLargePlanEditBodies(dir string, stamp string, edits []PlanEdit) ([]PlanEdit, int, error) {
	out := append([]PlanEdit(nil), edits...)
	spilled := 0
	for i := range out {
		if shouldSpillPlanInlineText(out[i].NewText) {
			ref, err := writePlanEditBodyRef(dir, stamp, i, "new_text", out[i].NewText)
			if err != nil {
				return nil, 0, err
			}
			out[i].NewText = ""
			out[i].NewTextRef = ref
			spilled++
		}
		if shouldSpillPlanInlineText(out[i].Content) {
			ref, err := writePlanEditBodyRef(dir, stamp, i, "content", out[i].Content)
			if err != nil {
				return nil, 0, err
			}
			out[i].Content = ""
			out[i].ContentRef = ref
			spilled++
		}
	}
	return out, spilled, nil
}

func shouldSpillPlanInlineText(value string) bool {
	return len(value) > planInlineTextSpillThreshold
}

func writePlanEditBodyRef(dir string, stamp string, index int, field string, content string) (string, error) {
	assetDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return "", fmt.Errorf("create plan asset dir: %w", err)
	}
	name := fmt.Sprintf("%s-edit-%03d-%s.txt", stamp, index+1, field)
	path := filepath.Join(assetDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write plan asset %s: %w", name, err)
	}
	return filepath.ToSlash(filepath.Join("assets", name)), nil
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
	if err := hydratePlanArtifactRefs(filepath.Dir(path), &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}
	return artifact, nil
}

func hydratePlanArtifactRefs(baseDir string, artifact *PlanArtifact) error {
	if artifact == nil {
		return nil
	}
	for i := range artifact.Edits {
		edit := &artifact.Edits[i]
		if strings.TrimSpace(edit.NewText) == "" && strings.TrimSpace(edit.NewTextRef) != "" {
			content, err := readPlanEditBodyRef(baseDir, edit.NewTextRef)
			if err != nil {
				return fmt.Errorf("edits[%d].new_text_ref: %w", i, err)
			}
			edit.NewText = content
		}
		if strings.TrimSpace(edit.Content) == "" && strings.TrimSpace(edit.ContentRef) != "" {
			content, err := readPlanEditBodyRef(baseDir, edit.ContentRef)
			if err != nil {
				return fmt.Errorf("edits[%d].content_ref: %w", i, err)
			}
			edit.Content = content
		}
		if err := validatePlanEdit(*edit); err != nil {
			return fmt.Errorf("edits[%d]: %w", i, err)
		}
	}
	return nil
}

func readPlanEditBodyRef(baseDir string, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty ref")
	}
	if filepath.IsAbs(ref) {
		return "", fmt.Errorf("absolute refs are not allowed")
	}
	clean := filepath.Clean(filepath.FromSlash(ref))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("ref escapes plan artifact directory")
	}
	path := filepath.Join(baseDir, clean)
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("ref escapes plan artifact directory")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
