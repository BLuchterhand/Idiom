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
// whole-file/new-file shape: substring (OldText/NewText; empty
// NewText deletes OldText), range (StartLine/EndLine/Content), or
// ReplaceAll with NewText and empty OldText. A spec must populate one
// of the shapes; submit_plan validates this before persisting the
// plan.
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

// PlanArtifactSchemaVersion is the version stamp written to every
// plan artifact. Bump when the field set changes.
const PlanArtifactSchemaVersion = "idiom.plan.v2"

const planInlineTextSpillThreshold = 4096

// submitPlan persists the planner's edit-plan artifact to disk and
// returns the path so the caller (orchestrator) can find and execute
// it. The artifact lives under .idiom/plans/<timestamp>.json by
// default.
func (m *Manager) submitPlan(req ToolRequest) ToolResult {
	if req.PlanArtifact == nil && len(req.Edits) == 0 {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "submit_plan requires at least one edit in the edits array"}
	}
	stamp := time.Now().UTC().Format("20060102-150405.000")
	stamp = strings.ReplaceAll(stamp, ".", "")
	created := time.Now().UTC().Format(time.RFC3339Nano)
	planID := "plan_" + stamp
	artifact, err := newPlanArtifactFromSubmitPlanRequest(req, m.cfg.SessionID, created, planID)
	if err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
	}
	if planRationaleClaimsNoEditsNeeded(artifact.Rationale) {
		return ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied rationale",
			Error:   "submit_plan rationale says the requested final state is already present or that edits are placeholders. Do not submit verification/placeholder edits; answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact blocker.",
		}
	}
	if len(artifact.Edits) == 0 {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "submit_plan requires at least one edit in the edits array"}
	}
	if planEditsAreVerificationOnly(artifact.Edits) {
		return ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: verification-only edits",
			Error:   "submit_plan edits describe verification/checking work rather than state mutation. If the files already satisfy the task, answer `Decision: source-satisfied`; otherwise submit only real corrective edits or answer <<QUESTION>> with the exact blocker.",
		}
	}
	for i, edit := range artifact.Edits {
		if err := validatePlanEdit(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
		if err := m.validatePlanEditAgainstWorkspace(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
	}
	if err := ValidatePlanArtifact(artifact); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
	}
	audit := AuditPlanArtifact(m.cfg.Root, artifact, PlanArtifactAuditOptions{EnforcePacketEvidence: req.PlanArtifact != nil})
	if !audit.OK {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Output: audit.SummaryLine(), Error: audit.ErrorSummary()}
	}
	dir := m.planArtifactDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("create plan dir: %s", err.Error())}
	}
	edits, spilled, err := spillLargePlanEditBodies(dir, stamp, artifact.Edits)
	if err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: err.Error()}
	}
	artifact.Edits = edits
	artifact = normalizePlanArtifactForSubmit(artifact, m.cfg.SessionID, created, planID, req.PlanArtifact == nil)
	if err := ValidatePlanArtifact(artifact); err != nil {
		cleanupPlanEditBodyRefs(dir, artifact.Edits)
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
	}
	path := filepath.Join(dir, stamp+".plan.json")
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		cleanupPlanEditBodyRefs(dir, artifact.Edits)
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("marshal artifact: %s", err.Error())}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		cleanupPlanEditBodyRefs(dir, artifact.Edits)
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan failed", Error: fmt.Sprintf("write artifact: %s", err.Error())}
	}
	pointer := filepath.Join(dir, "last.plan.json")
	_ = os.WriteFile(pointer, body, 0o644)
	return ToolResult{
		Tool:    "submit_plan",
		Summary: fmt.Sprintf("Plan submitted with %d edit(s); the executor phase will dispatch them.", len(artifact.Edits)),
		Output:  fmt.Sprintf("plan_path: %s\nedits: %d\nschema_version: %s\ncontent_refs: %d\n%s", path, len(artifact.Edits), PlanArtifactSchemaVersion, spilled, audit.SummaryLine()),
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
	hasOldText := strings.TrimSpace(edit.OldText) != ""
	hasNewText := strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != ""
	hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
	hasSubstring := hasOldText
	hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
	hasReplace := hasNewText && edit.ReplaceAll
	if (edit.StartLine > 0 || edit.EndLine > 0) && !hasContent {
		return fmt.Errorf("line-range edits require non-empty content; to delete text, use old_text with empty new_text instead")
	}
	if !hasSubstring && !hasRange && !hasReplace {
		return fmt.Errorf("requires one edit shape: old_text with new_text (new_text may be empty to delete old_text), start_line+end_line+non-empty content, or replace_all=true with new_text and empty old_text for whole-file/new-file writes; large new_text/content may be represented by new_text_ref/content_ref")
	}
	if hasSubstring && strings.TrimSpace(edit.NewTextRef) == "" && edit.OldText == edit.NewText {
		return fmt.Errorf("old_text and new_text are identical; submit only real edits, or close as source-satisfied/pass when no correction is needed")
	}
	return nil
}

func planRationaleClaimsNoEditsNeeded(rationale string) bool {
	text := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(rationale))), " ")
	if text == "" {
		return false
	}
	for _, needle := range []string{
		"placeholder edit",
		"placeholder edits",
		"satisfy the requirement that edits be submitted",
		"content is identical",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	for _, needle := range []string{
		"no edits are needed",
		"no edit is needed",
		"no changes are needed",
		"no change is needed",
		"no work to do",
	} {
		if containsUnscopedPlanNoWorkPhrase(text, needle) {
			return true
		}
	}
	sourceSatisfiedNeedles := []string{
		"source-satisfied",
		"source satisfied",
		"already in the expected state",
		"already in its expected state",
		"already matches the requested final state",
		"already match the requested final state",
		"already contains the requested final state",
		"already contain the requested final state",
	}
	for _, needle := range sourceSatisfiedNeedles {
		if containsUnnegatedPlanRationalePhrase(text, needle) {
			return true
		}
	}
	return false
}

func containsUnscopedPlanNoWorkPhrase(text, phrase string) bool {
	start := 0
	for {
		idx := strings.Index(text[start:], phrase)
		if idx < 0 {
			return false
		}
		idx += start
		after := strings.TrimSpace(text[idx+len(phrase):])
		scopedException := strings.HasPrefix(after, "outside ") ||
			strings.HasPrefix(after, "beyond ") ||
			strings.HasPrefix(after, "for unrelated ")
		if !scopedException {
			return true
		}
		start = idx + len(phrase)
	}
}

func containsUnnegatedPlanRationalePhrase(text, phrase string) bool {
	start := 0
	for {
		idx := strings.Index(text[start:], phrase)
		if idx < 0 {
			return false
		}
		idx += start
		prefixStart := idx - 32
		if prefixStart < 0 {
			prefixStart = 0
		}
		prefix := text[prefixStart:idx]
		negated := strings.Contains(prefix, "not ") ||
			strings.Contains(prefix, "not yet ") ||
			strings.Contains(prefix, "is not ") ||
			strings.Contains(prefix, "isn't ") ||
			strings.Contains(prefix, "cannot ") ||
			strings.Contains(prefix, "can't ")
		if !negated {
			return true
		}
		start = idx + len(phrase)
	}
}

func planEditsAreVerificationOnly(edits []PlanEdit) bool {
	if len(edits) == 0 {
		return false
	}
	for _, edit := range edits {
		if !planEditPurposeIsVerificationOnly(edit.Purpose) {
			return false
		}
	}
	return true
}

func planEditPurposeIsVerificationOnly(purpose string) bool {
	text := strings.ToLower(strings.TrimSpace(purpose))
	if text == "" {
		return false
	}
	for _, prefix := range []string{
		"verify ",
		"verify that ",
		"check ",
		"check that ",
		"confirm ",
		"confirm that ",
		"validate ",
		"validate that ",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (m *Manager) validatePlanEditAgainstWorkspace(edit PlanEdit) error {
	path, err := m.resolvePath(edit.Path, "")
	if err != nil {
		return fmt.Errorf("plan edit preflight could not resolve path %q: %w", edit.Path, err)
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("plan edit preflight could not read %s: %w", edit.Path, err)
	}
	if err == nil && planEditIsWholeFileWrite(edit) && IsCompletedPlanningArtifact(path, body) {
		return fmt.Errorf("whole-file edit would overwrite completed planning artifact %s; use a bounded anchored edit or ask the user to reopen/replace the completed artifact explicitly", edit.Path)
	}
	oldText := edit.OldText
	if strings.TrimSpace(oldText) == "" && err != nil {
		return nil
	}
	if strings.TrimSpace(oldText) == "" {
		updated, changed, previewErr := PreviewPlanEditContent(string(body), edit)
		if previewErr != nil {
			return previewErr
		}
		if changed {
			if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
				return fmt.Errorf("edit would introduce adjacent duplicate code line %d in %s: %q", lineNo, edit.Path, line)
			}
		} else {
			return fmt.Errorf("edit would not change %s; submit only real edits, or close as source-satisfied/pass when no correction is needed", edit.Path)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("old_text preflight could not read %s: %w", edit.Path, err)
	}
	current := string(body)
	matchCount := strings.Count(current, oldText)
	if matchCount == 0 {
		if containsCaseFoldedSubstring(current, oldText) {
			return fmt.Errorf("old_text did not appear in %s, but a case-insensitive match exists; preserve exact casing from the file or use start_line/end_line+content", edit.Path)
		}
		if planEditReplacementAlreadyPresent(current, edit) {
			return fmt.Errorf("old_text did not appear in %s, but the replacement text already appears in the file; if every required edit is already present, close the planner phase with `Decision: source-satisfied` and per-path current-state evidence instead of submitting stale edits", edit.Path)
		}
		return fmt.Errorf("old_text did not appear in %s; copy the exact substring from the file or use start_line/end_line+content", edit.Path)
	}
	if matchCount > 1 && !edit.ReplaceAll {
		return fmt.Errorf("old_text matched %d locations in %s; submit_plan rejects ambiguous substring edits before executor writes. Extend old_text with surrounding context, use start_line/end_line+content, or set replace_all=true when every occurrence is intended", matchCount, edit.Path)
	}
	updated, changed, previewErr := PreviewPlanEditContent(current, edit)
	if previewErr != nil {
		return previewErr
	}
	if changed {
		if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
			return fmt.Errorf("edit would introduce adjacent duplicate code line %d in %s: %q", lineNo, edit.Path, line)
		}
	} else {
		return fmt.Errorf("edit would not change %s; submit only real edits, or close as source-satisfied/pass when no correction is needed", edit.Path)
	}
	return nil
}

// PreviewPlanEditContent applies one PlanEdit to current content without
// touching disk. It is shared by submit_plan validation and the deterministic
// executor preflight so malformed or suspicious plans fail before writes land.
func PreviewPlanEditContent(current string, edit PlanEdit) (string, bool, error) {
	if planEditIsWholeFileWrite(edit) {
		return edit.NewText, edit.NewText != current, nil
	}
	if strings.TrimSpace(edit.OldText) != "" {
		matchCount := strings.Count(current, edit.OldText)
		if matchCount == 0 {
			return current, false, fmt.Errorf("old_text does not appear in %s", edit.Path)
		}
		if matchCount > 1 && !edit.ReplaceAll {
			return current, false, fmt.Errorf("old_text matched %d locations in %s", matchCount, edit.Path)
		}
		if edit.ReplaceAll {
			updated := strings.ReplaceAll(current, edit.OldText, edit.NewText)
			return updated, updated != current, nil
		}
		updated := strings.Replace(current, edit.OldText, edit.NewText, 1)
		return updated, updated != current, nil
	}
	if edit.StartLine > 0 || edit.EndLine > 0 {
		updated, err := previewPlanLineRangeEdit(current, edit)
		return updated, updated != current, err
	}
	return current, false, nil
}

func planEditIsWholeFileWrite(edit PlanEdit) bool {
	return edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != ""
}

func planEditReplacementAlreadyPresent(current string, edit PlanEdit) bool {
	replacement := strings.TrimSpace(edit.NewText)
	if replacement == "" || strings.TrimSpace(edit.NewTextRef) != "" {
		return false
	}
	return strings.Contains(current, edit.NewText)
}

func previewPlanLineRangeEdit(current string, edit PlanEdit) (string, error) {
	startLine := edit.StartLine
	endLine := edit.EndLine
	if startLine <= 0 {
		return current, fmt.Errorf("start_line must be >= 1 for %s", edit.Path)
	}
	if endLine == 0 {
		endLine = startLine
	}
	if endLine < startLine {
		return current, fmt.Errorf("end_line (%d) must be >= start_line (%d) for %s", endLine, startLine, edit.Path)
	}
	spans := lineSpans(current)
	if len(spans) == 0 {
		return current, fmt.Errorf("cannot apply a line edit to empty %s", edit.Path)
	}
	if startLine > len(spans) || endLine > len(spans) {
		return current, fmt.Errorf("requested lines %d-%d are outside %s (file has %d lines)", startLine, endLine, edit.Path, len(spans))
	}
	startOffset := spans[startLine-1].start
	endOffset := spans[endLine-1].end
	replacedRange := current[startOffset:endOffset]
	replacement := edit.Content
	if replacement != "" && strings.HasSuffix(replacedRange, "\n") && !strings.HasSuffix(replacement, "\n") {
		replacement += lineEndingSuffix(replacedRange)
	}
	return current[:startOffset] + replacement + current[endOffset:], nil
}

func spillLargePlanEditBodies(dir string, stamp string, edits []PlanEdit) ([]PlanEdit, int, error) {
	out := append([]PlanEdit(nil), edits...)
	spilled := 0
	for i := range out {
		if shouldSpillPlanInlineText(out[i].NewText) {
			ref, err := writePlanEditBodyRef(dir, stamp, i, "new_text", out[i].NewText)
			if err != nil {
				cleanupPlanEditBodyRefs(dir, out)
				return nil, 0, err
			}
			out[i].NewText = ""
			out[i].NewTextRef = ref
			spilled++
		}
		if shouldSpillPlanInlineText(out[i].Content) {
			ref, err := writePlanEditBodyRef(dir, stamp, i, "content", out[i].Content)
			if err != nil {
				cleanupPlanEditBodyRefs(dir, out)
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

func cleanupPlanEditBodyRefs(dir string, edits []PlanEdit) {
	for _, edit := range edits {
		for _, ref := range []string{edit.NewTextRef, edit.ContentRef} {
			path, ok := planEditBodyRefPath(dir, ref)
			if !ok {
				continue
			}
			_ = os.Remove(path)
		}
	}
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
	artifact, err := ParsePlanArtifactJSON(body)
	if err != nil {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}
	if len(artifact.Edits) == 0 {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q is empty", path)
	}
	if err := hydratePlanArtifactRefs(filepath.Dir(path), &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}
	if err := ValidatePlanArtifact(artifact); err != nil {
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

func planEditBodyRefPath(baseDir string, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || filepath.IsAbs(ref) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(ref))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", false
	}
	path := filepath.Join(baseDir, clean)
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return path, true
}
