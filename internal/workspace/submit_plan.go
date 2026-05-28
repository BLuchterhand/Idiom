package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SubmitPlanEmptyEditsCode = "submit_plan.empty_edits"

// PlanEdit is one edit spec inside a plan-then-execute artifact. The
// planner emits these via the submit_plan tool; the executor phase
// iterates and dispatches each one against the workspace.
//
// The edit shapes mirror edit_file's shapes plus plan-execute-only shapes:
// substring (OldText/NewText; empty NewText deletes OldText), range
// (StartLine/EndLine/Content), insertion (InsertAfterLine/Content), or
// ReplaceAll with NewText and empty OldText. A spec must populate one of the
// shapes; submit_plan validates this before persisting the plan.
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

// PlanEditHasExplicitReplacementText reports whether a substring edit includes
// the replacement half of the old_text/new_text contract. JSON-decoded tool
// requests can distinguish omitted new_text from explicit empty new_text, which
// is required for deletion edits. Programmatic tests and internal constructors
// cannot carry that field-presence bit, so they retain the historical Go struct
// behavior.
func PlanEditHasExplicitReplacementText(edit PlanEdit) bool {
	if strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "" {
		return true
	}
	if edit.decodedFromJSON {
		return edit.newTextSet
	}
	return true
}

// PlanEditAnchorProvenance is harness-authored evidence that a
// substring edit was validated against specific current file bytes.
// Planners may submit old_text/new_text; submit_plan overwrites this
// field before persisting so executors can reject stale plans even
// when old_text still appears somewhere in a changed file.
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
const PlanArtifactSchemaVersion = "idiom.plan.v3"

const PlanArtifactPreviousSchemaVersion = "idiom.plan.v2"

const PlanEditAnchorProvenanceSchemaVersion = "idiom.plan_edit_anchor.v1"

const planInlineTextSpillThreshold = 4096

const submitPlanNoOpEditGuidance = "submit real edits, or answer `Decision: source-satisfied` with current-state evidence when no changes are needed"

// submitPlan persists the planner's edit-plan artifact to disk and
// returns the path so the caller (orchestrator) can find and execute
// it. The artifact lives under .idiom/plans/<timestamp>.json by
// default.
func (m *Manager) submitPlan(req ToolRequest) ToolResult {
	if req.PlanArtifact == nil && len(req.Edits) == 0 {
		return ToolResult{Tool: "submit_plan", Code: SubmitPlanEmptyEditsCode, Summary: "submit_plan rejected", Error: "submit_plan requires at least one edit in the edits array"}
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
		return ToolResult{Tool: "submit_plan", Code: SubmitPlanEmptyEditsCode, Summary: "submit_plan rejected", Error: "submit_plan requires at least one edit in the edits array"}
	}
	m.repairPlanArtifactOldTextAnchors(&artifact)
	validationArtifact := artifact
	validationArtifact.Edits = append([]PlanEdit(nil), artifact.Edits...)
	if err := hydratePlanArtifactRefs(m.planArtifactDir(), &validationArtifact); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
	}
	if planEditsAreVerificationOnly(artifact.Edits) {
		return ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: verification-only edits",
			Error:   "submit_plan edits describe verification/checking work rather than state mutation. If the files already satisfy the task, answer `Decision: source-satisfied`; otherwise submit only real corrective edits or answer <<QUESTION>> with the exact blocker.",
		}
	}
	for i, edit := range validationArtifact.Edits {
		if err := validatePlanEdit(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
		if err := m.validatePlanEditAgainstWorkspace(edit); err != nil {
			return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: fmt.Sprintf("edits[%d]: %s", i, err.Error())}
		}
	}
	if err := m.validatePlanEditSequenceAgainstWorkspace(validationArtifact.Edits); err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
	}
	artifact.Edits, err = AttachPlanEditAnchorProvenance(m.cfg.Root, artifact.Edits)
	if err != nil {
		return ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected", Error: err.Error()}
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
	if err := validatePlanEditAnchorProvenanceShape(edit.AnchorProvenance); err != nil {
		return err
	}
	hasOldText := strings.TrimSpace(edit.OldText) != ""
	hasNewText := strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != ""
	hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
	hasInlineContent := strings.TrimSpace(edit.Content) != ""
	hasSubstring := hasOldText && PlanEditHasExplicitReplacementText(edit)
	hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
	hasInsert := edit.InsertAfterLine > 0 && hasInlineContent
	hasReplace := hasNewText && edit.ReplaceAll
	if edit.InsertAfterLine < 0 {
		return fmt.Errorf("insert_after_line must be >= 1")
	}
	if edit.InsertAfterLine > 0 {
		if strings.TrimSpace(edit.ContentRef) != "" {
			return fmt.Errorf("insert_after_line edits do not support content_ref in this submit_plan shape; inline non-empty content is required")
		}
		if !hasInlineContent {
			return fmt.Errorf("insert_after_line edits require non-empty content")
		}
		if len(edit.Content) > planInlineTextSpillThreshold {
			return fmt.Errorf("insert_after_line content exceeds inline submit_plan limit (%d bytes); content_ref is not supported for insert_after_line in this schema; for large insertions, use a line-range replacement that includes the unchanged anchor line(s) plus inserted content, or use replace_all with a complete file body when replacing the whole file", planInlineTextSpillThreshold)
		}
		if hasOldText || strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "" || edit.StartLine > 0 || edit.EndLine > 0 || edit.ReplaceAll {
			return fmt.Errorf("insert_after_line edits are mutually exclusive with old_text/new_text, start_line/end_line, and replace_all shapes")
		}
	}
	if hasOldText && !hasSubstring {
		return fmt.Errorf("substring edits require `new_text` or `new_text_ref`; use explicit empty `new_text` to delete old_text")
	}
	if (edit.StartLine > 0 || edit.EndLine > 0) && !hasContent {
		return fmt.Errorf("line-range edits require non-empty content; to delete text, use old_text with empty new_text instead")
	}
	if !hasSubstring && !hasRange && !hasInsert && !hasReplace {
		return fmt.Errorf("requires one edit shape: old_text with new_text (new_text may be empty to delete old_text), start_line+end_line+non-empty content for replacement, insert_after_line+non-empty content for insertion, or replace_all=true with new_text and empty old_text for whole-file/new-file writes; large new_text/content may be represented by new_text_ref/content_ref except insert_after_line")
	}
	if hasSubstring && strings.TrimSpace(edit.NewTextRef) == "" && edit.OldText == edit.NewText {
		return fmt.Errorf("old_text and new_text are identical; %s", submitPlanNoOpEditGuidance)
	}
	if field, marker := planEditPlaceholderField(edit); field != "" {
		return fmt.Errorf("%s contains placeholder edit body marker %q; submit complete executable source text, or answer <<QUESTION>> with the missing body/anchor blocker", field, marker)
	}
	if reason := PlanEditUnsupportedProofClaimReason(edit); reason != "" {
		return fmt.Errorf("%w: %s", ErrPlanSubmissionRejected, reason)
	}
	return nil
}

// PlanEditUnsupportedProofClaimReason reports source-edit proof text that would
// make the submitted artifact incapable of proving the requested behavior. Keep
// this at submit_plan intake so planners can repair the edit before executor
// handoff rather than failing after the artifact is accepted.
func PlanEditUnsupportedProofClaimReason(edit PlanEdit) string {
	path := strings.TrimSpace(edit.Path)
	if path == "" || !SourceSanityCoversPath(path) {
		return ""
	}
	added := PlanEditAddedText(edit)
	if strings.TrimSpace(added) == "" {
		return ""
	}
	return UnsupportedProofClaimReason(added)
}

// PlanEditAddedText returns the final content a plan edit contributes.
func PlanEditAddedText(edit PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}
	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}
	return ""
}

// UnsupportedProofClaimReason detects test/proof bodies that admit they are not
// meaningful verification, such as no-op assertions or "simplified check"
// comments. It intentionally operates on text only; callers decide whether the
// edited path is source-like.
func UnsupportedProofClaimReason(text string) string {
	for lineNo, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if noOpAssertionLine(line) {
			return fmt.Sprintf("unsupported proof claim: no-op assertion on line %d; submit a concrete state assertion or report a blocker", lineNo+1)
		}
		if phrase := selfDisqualifyingProofPhrase(line); phrase != "" {
			return fmt.Sprintf("unsupported proof claim: self-disqualifying verification text %q on line %d; submit concrete evidence or report a blocker", phrase, lineNo+1)
		}
	}
	return ""
}

func noOpAssertionLine(line string) bool {
	compact := strings.Join(strings.Fields(line), "")
	normalized := strings.ToLower(compact)
	return strings.Contains(compact, "CHECK(true") ||
		strings.Contains(compact, "REQUIRE(true") ||
		strings.Contains(compact, "ASSERT_TRUE(true") ||
		strings.Contains(compact, "EXPECT_TRUE(true") ||
		strings.Contains(normalized, "_assert_true(true") ||
		strings.Contains(normalized, "assert_true(true") ||
		strings.Contains(normalized, "asserttrue(true") ||
		strings.Contains(normalized, "assert(true") ||
		strings.Contains(normalized, "assert.true(true") ||
		strings.Contains(normalized, "assert.true(t,true") ||
		strings.Contains(normalized, "require.true(t,true")
}

func selfDisqualifyingProofPhrase(line string) string {
	lower := strings.ToLower(line)
	phrases := []string{
		"can't easily test",
		"cannot easily test",
		"can't easily access",
		"cannot easily access",
		"can't easily inspect",
		"cannot easily inspect",
		"not easily test",
		"simplified check",
		"for now, we just verify",
		"for now we just verify",
		"verify that it exists",
		"exists and can be called",
		"exists and can be accessed",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) && verificationClaimContext(line, phrase) {
			return phrase
		}
	}
	return ""
}

func verificationClaimContext(line string, phrase string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if strings.Contains(phrase, "test") || strings.Contains(phrase, "verify") || strings.Contains(phrase, "check") {
		return true
	}
	if strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/*") || strings.HasPrefix(lower, "*") {
		return true
	}
	return strings.Contains(lower, "assert") ||
		strings.Contains(lower, "check") ||
		strings.Contains(lower, "verify") ||
		strings.Contains(lower, "test")
}

func planEditPlaceholderField(edit PlanEdit) (string, string) {
	fields := []struct {
		name  string
		value string
	}{
		{name: "old_text", value: edit.OldText},
		{name: "new_text", value: edit.NewText},
		{name: "content", value: edit.Content},
	}
	for _, field := range fields {
		if marker := planEditPlaceholderMarker(field.value); marker != "" {
			return field.name, marker
		}
	}
	return "", ""
}

func planEditPlaceholderMarker(value string) string {
	text := strings.ToLower(value)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, marker := range []string{
		"... existing code ...",
		"omitted for brevity",
		"rest of file unchanged",
		"remaining code",
		"unchanged code",
		"<todo",
		"todo: fill",
	} {
		if strings.Contains(text, marker) {
			return marker
		}
	}
	return ""
}

// AttachPlanEditAnchorProvenance returns a copy of edits with
// harness-owned provenance attached to substring edits. Existing
// provenance is ignored and overwritten; non-substring edits get nil
// provenance so the executor treats them according to their own
// edit-shape validation.
func AttachPlanEditAnchorProvenance(workspaceRoot string, edits []PlanEdit) ([]PlanEdit, error) {
	out := append([]PlanEdit(nil), edits...)
	for i, edit := range out {
		if strings.TrimSpace(edit.OldText) == "" {
			out[i].AnchorProvenance = nil
			continue
		}
		provenance, err := buildPlanEditAnchorProvenance(workspaceRoot, edit)
		if err != nil {
			return nil, fmt.Errorf("edits[%d]: %w", i, err)
		}
		out[i].AnchorProvenance = &provenance
	}
	return out, nil
}

// ValidatePlanEditAnchorProvenance verifies that a persisted substring
// edit still targets the same file bytes submit_plan validated. Missing
// provenance is accepted for legacy artifacts; malformed or stale
// provenance is rejected before executor writes.
func ValidatePlanEditAnchorProvenance(workspaceRoot string, edit PlanEdit) error {
	provenance := edit.AnchorProvenance
	if provenance == nil || strings.TrimSpace(edit.OldText) == "" {
		return nil
	}
	if err := validatePlanEditAnchorProvenanceShape(provenance); err != nil {
		return err
	}
	abs, rel, err := planEditWorkspacePath(workspaceRoot, edit.Path)
	if err != nil {
		return err
	}
	if provenance.Path != rel {
		return fmt.Errorf("anchor provenance path mismatch: edit path %s, provenance path %s", rel, provenance.Path)
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("anchor provenance stale for %s: could not read current file: %w", rel, err)
	}
	currentFileDigest := planEditSHA256(body)
	if provenance.FileSHA256 != currentFileDigest {
		return fmt.Errorf("anchor provenance stale for %s: file fingerprint changed since submit_plan validation", rel)
	}
	if provenance.AnchorSHA256 != planEditSHA256([]byte(edit.OldText)) {
		return fmt.Errorf("anchor provenance stale for %s: old_text fingerprint changed since submit_plan validation", rel)
	}
	current := string(body)
	if provenance.StartByte < 0 || provenance.EndByte < provenance.StartByte || provenance.EndByte > len(current) {
		return fmt.Errorf("anchor provenance stale for %s: byte range is outside current file", rel)
	}
	if current[provenance.StartByte:provenance.EndByte] != edit.OldText {
		return fmt.Errorf("anchor provenance stale for %s: anchored byte range no longer matches old_text", rel)
	}
	matchCount := strings.Count(current, edit.OldText)
	if matchCount != provenance.MatchCount {
		return fmt.Errorf("anchor provenance stale for %s: old_text match count changed from %d to %d", rel, provenance.MatchCount, matchCount)
	}
	return nil
}

func buildPlanEditAnchorProvenance(workspaceRoot string, edit PlanEdit) (PlanEditAnchorProvenance, error) {
	abs, rel, err := planEditWorkspacePath(workspaceRoot, edit.Path)
	if err != nil {
		return PlanEditAnchorProvenance{}, err
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance could not read %s: %w", rel, err)
	}
	current := string(body)
	matchCount := strings.Count(current, edit.OldText)
	if matchCount == 0 {
		return PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance could not find old_text in %s", rel)
	}
	if matchCount > 1 && !edit.ReplaceAll {
		return PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance found %d matches in %s; exact substring edits must be unique unless replace_all=true", matchCount, rel)
	}
	startByte := strings.Index(current, edit.OldText)
	endByte := startByte + len(edit.OldText)
	startLine := planEditLineNumberAtByte(current, startByte)
	endLine := startLine
	if endByte > startByte {
		endLine = planEditLineNumberAtByte(current, endByte-1)
	}
	return PlanEditAnchorProvenance{
		SchemaVersion: PlanEditAnchorProvenanceSchemaVersion,
		Source:        "submit_plan",
		Path:          rel,
		FileSHA256:    planEditSHA256(body),
		AnchorSHA256:  planEditSHA256([]byte(edit.OldText)),
		StartByte:     startByte,
		EndByte:       endByte,
		StartLine:     startLine,
		EndLine:       endLine,
		MatchCount:    matchCount,
	}, nil
}

func validatePlanEditAnchorProvenanceShape(provenance *PlanEditAnchorProvenance) error {
	if provenance == nil {
		return nil
	}
	if provenance.SchemaVersion != PlanEditAnchorProvenanceSchemaVersion {
		return fmt.Errorf("anchor_provenance schema_version must be %s", PlanEditAnchorProvenanceSchemaVersion)
	}
	if strings.TrimSpace(provenance.Source) != "submit_plan" {
		return fmt.Errorf("anchor_provenance source must be submit_plan")
	}
	if !planArtifactPathIsSafe(provenance.Path) {
		return fmt.Errorf("anchor_provenance path must stay relative to the workspace")
	}
	if !planEditDigestIsSHA256(provenance.FileSHA256) {
		return fmt.Errorf("anchor_provenance file_sha256 must be a sha256 digest")
	}
	if !planEditDigestIsSHA256(provenance.AnchorSHA256) {
		return fmt.Errorf("anchor_provenance anchor_sha256 must be a sha256 digest")
	}
	if provenance.StartByte < 0 || provenance.EndByte <= provenance.StartByte {
		return fmt.Errorf("anchor_provenance byte range is invalid")
	}
	if provenance.StartLine <= 0 || provenance.EndLine < provenance.StartLine {
		return fmt.Errorf("anchor_provenance line range is invalid")
	}
	if provenance.MatchCount <= 0 {
		return fmt.Errorf("anchor_provenance match_count must be positive")
	}
	return nil
}

func planEditWorkspacePath(workspaceRoot string, rawPath string) (string, string, error) {
	rel := strings.TrimSpace(rawPath)
	rel = strings.ReplaceAll(rel, "\\", "/")
	for strings.HasPrefix(rel, "./") {
		rel = strings.TrimPrefix(rel, "./")
	}
	if !planArtifactPathIsSafe(rel) {
		return "", "", fmt.Errorf("plan edit path must stay relative to the workspace: %q", rawPath)
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is required for anchor provenance")
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("workspace root is invalid for anchor provenance: %w", err)
	}
	cleanAbs, err := filepath.Abs(filepath.Join(cleanRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", fmt.Errorf("plan edit path is invalid for anchor provenance: %w", err)
	}
	if cleanAbs != cleanRoot && !strings.HasPrefix(cleanAbs, cleanRoot+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("plan edit path must stay relative to the workspace: %q", rawPath)
	}
	return cleanAbs, rel, nil
}

func planEditSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
}

func planEditDigestIsSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}

func planEditLineNumberAtByte(content string, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return strings.Count(content[:offset], "\n") + 1
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
		if edit.InsertAfterLine > 0 {
			return fmt.Errorf("insert_after_line preflight could not read existing %s: %w", edit.Path, err)
		}
		if edit.StartLine > 0 || edit.EndLine > 0 {
			return fmt.Errorf("line-range preflight could not read existing %s: %w", edit.Path, err)
		}
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
			if rejected := m.rejectInvalidPlannedWrite("submit_plan", "Plan edit rejected", path, []byte(updated)); rejected != nil {
				errorText := strings.TrimSpace(rejected.Error)
				if edit.StartLine > 0 && edit.EndLine >= edit.StartLine {
					errorText += fmt.Sprintf("\nLine-range edit detail: submit_plan edit for %s declared start_line=%d and end_line=%d. The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it, otherwise trim trailing unchanged context from content. If you are inserting after a current line, use insert_after_line with non-empty content instead.", edit.Path, edit.StartLine, edit.EndLine)
				}
				return fmt.Errorf("%w: %s", ErrPlanSubmissionRejected, errorText)
			}
		} else {
			return fmt.Errorf("edit would not change %s; %s", edit.Path, submitPlanNoOpEditGuidance)
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
		if rejected := m.rejectInvalidPlannedWrite("submit_plan", "Plan edit rejected", path, []byte(updated)); rejected != nil {
			return fmt.Errorf("%w: %s", ErrPlanSubmissionRejected, strings.TrimSpace(rejected.Error))
		}
	} else {
		return fmt.Errorf("edit would not change %s; %s", edit.Path, submitPlanNoOpEditGuidance)
	}
	return nil
}

func (m *Manager) validatePlanEditSequenceAgainstWorkspace(edits []PlanEdit) error {
	projected := make(map[string][]byte)
	for i, edit := range edits {
		path, err := m.resolvePath(edit.Path, "")
		if err != nil {
			return fmt.Errorf("edits[%d]: plan edit preflight could not resolve path %q: %w", i, edit.Path, err)
		}
		body, ok := projected[path]
		if !ok {
			body, err = os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) && planEditIsWholeFileWrite(edit) {
					body = nil
				} else {
					return fmt.Errorf("edits[%d]: plan edit preflight could not read %s: %w", i, edit.Path, err)
				}
			}
			projected[path] = body
		}
		if planEditIsWholeFileWrite(edit) && IsCompletedPlanningArtifact(path, body) {
			return fmt.Errorf("edits[%d]: whole-file edit would overwrite completed planning artifact %s; use a bounded anchored edit or ask the user to reopen/replace the completed artifact explicitly", i, edit.Path)
		}
		updated, changed, previewErr := PreviewPlanEditContent(string(body), edit)
		if previewErr != nil {
			return fmt.Errorf("edits[%d]: after applying prior plan edits, %s", i, previewErr.Error())
		}
		if !changed {
			return fmt.Errorf("edits[%d]: edit would not change %s after applying prior plan edits; %s", i, edit.Path, submitPlanNoOpEditGuidance)
		}
		if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
			return fmt.Errorf("edits[%d]: edit would introduce adjacent duplicate code line %d in %s after applying prior plan edits: %q", i, lineNo, edit.Path, line)
		}
		if rejected := m.rejectInvalidPlannedWrite("submit_plan", "Plan edit rejected", path, []byte(updated)); rejected != nil {
			errorText := strings.TrimSpace(rejected.Error)
			if edit.StartLine > 0 && edit.EndLine >= edit.StartLine {
				errorText += fmt.Sprintf("\nLine-range edit detail: submit_plan edit for %s declared start_line=%d and end_line=%d. The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it, otherwise trim trailing unchanged context from content. If you are inserting after a current line, use insert_after_line with non-empty content instead.", edit.Path, edit.StartLine, edit.EndLine)
			}
			return fmt.Errorf("edits[%d]: after applying prior plan edits, %s", i, errorText)
		}
		projected[path] = []byte(updated)
	}
	return nil
}

func (m *Manager) repairPlanArtifactOldTextAnchors(artifact *PlanArtifact) {
	if m == nil || artifact == nil {
		return
	}
	for i, edit := range artifact.Edits {
		if strings.TrimSpace(edit.OldText) == "" || strings.Contains(edit.OldText, "\r") {
			continue
		}
		path, err := m.resolvePath(edit.Path, "")
		if err != nil {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		current := string(body)
		if strings.Count(current, edit.OldText) > 0 {
			continue
		}
		repaired, ok := uniqueLeadingWhitespaceInsensitiveAnchor(current, edit.OldText)
		if !ok || strings.Count(current, repaired) != 1 {
			continue
		}
		artifact.Edits[i].OldText = repaired
	}
}

func uniqueLeadingWhitespaceInsensitiveAnchor(current string, oldText string) (string, bool) {
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 {
		return "", false
	}
	currentLines := strings.Split(current, "\n")
	if len(oldLines) > len(currentLines) {
		return "", false
	}
	var match string
	matches := 0
	for start := 0; start+len(oldLines) <= len(currentLines); start++ {
		if !leadingWhitespaceInsensitiveLinesMatch(currentLines[start:start+len(oldLines)], oldLines) {
			continue
		}
		match = strings.Join(currentLines[start:start+len(oldLines)], "\n")
		matches++
		if matches > 1 {
			return "", false
		}
	}
	if matches != 1 {
		return "", false
	}
	return match, true
}

func leadingWhitespaceInsensitiveLinesMatch(currentLines []string, oldLines []string) bool {
	if len(currentLines) != len(oldLines) {
		return false
	}
	for i := range oldLines {
		if strings.TrimLeft(currentLines[i], " \t") != strings.TrimLeft(oldLines[i], " \t") {
			return false
		}
	}
	return true
}

// PreviewPlanEditContent applies one PlanEdit to current content without
// touching disk. It is shared by submit_plan validation and the deterministic
// executor preflight so malformed or suspicious plans fail before writes land.
func PreviewPlanEditContent(current string, edit PlanEdit) (string, bool, error) {
	if planEditIsWholeFileWrite(edit) {
		return edit.NewText, edit.NewText != current, nil
	}
	if edit.InsertAfterLine > 0 {
		updated, err := previewPlanInsertAfterLineEdit(current, edit)
		return updated, updated != current, err
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

func previewPlanInsertAfterLineEdit(current string, edit PlanEdit) (string, error) {
	line := edit.InsertAfterLine
	if line <= 0 {
		return current, fmt.Errorf("insert_after_line must be >= 1 for %s", edit.Path)
	}
	if strings.TrimSpace(edit.Content) == "" {
		return current, fmt.Errorf("insert_after_line content must be non-empty for %s", edit.Path)
	}
	spans := lineSpans(current)
	if len(spans) == 0 {
		return current, fmt.Errorf("cannot apply insert_after_line to empty %s", edit.Path)
	}
	if line > len(spans) {
		return current, fmt.Errorf("insert_after_line %d is outside %s (file has %d lines)", line, edit.Path, len(spans))
	}
	offset := spans[line-1].end
	insertion := normalizePlanInsertionContent(current, offset, edit.Content)
	return current[:offset] + insertion + current[offset:], nil
}

func normalizePlanInsertionContent(current string, offset int, content string) string {
	if content == "" {
		return content
	}
	ending := preferredLineEnding(current)
	insertion := content
	if offset > 0 && offset <= len(current) && !strings.HasSuffix(current[:offset], "\n") && !strings.HasPrefix(insertion, "\n") && !strings.HasPrefix(insertion, "\r\n") {
		insertion = ending + insertion
	}
	if offset < len(current) && !strings.HasSuffix(insertion, "\n") {
		insertion += ending
	}
	if offset == len(current) && strings.HasSuffix(current, "\n") && !strings.HasSuffix(insertion, "\n") {
		insertion += ending
	}
	return insertion
}

func preferredLineEnding(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}
	return "\n"
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
			if out[i].InsertAfterLine > 0 {
				return nil, 0, fmt.Errorf("edits[%d]: insert_after_line content exceeds inline submit_plan limit (%d bytes); content_ref is not supported for insert_after_line in this schema; for large insertions, use a line-range replacement that includes the unchanged anchor line(s) plus inserted content, or use replace_all with a complete file body when replacing the whole file", i, planInlineTextSpillThreshold)
			}
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
	if err := attachMissingLoadedPlanAnchorProvenance(path, &artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}
	if err := ValidatePlanArtifact(artifact); err != nil {
		return PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}
	return artifact, nil
}

func attachMissingLoadedPlanAnchorProvenance(planPath string, artifact *PlanArtifact) error {
	if artifact == nil || artifact.SchemaVersion != PlanArtifactSchemaVersion || !planArtifactHasMissingAnchorProvenance(*artifact) {
		return nil
	}
	root := workspaceRootFromPlanArtifactPath(planPath)
	if root == "" {
		return nil
	}
	for i, edit := range artifact.Edits {
		if strings.TrimSpace(edit.OldText) == "" || edit.AnchorProvenance != nil {
			continue
		}
		edits, err := AttachPlanEditAnchorProvenance(root, []PlanEdit{edit})
		if err == nil && len(edits) == 1 {
			artifact.Edits[i] = edits[0]
		}
	}
	return nil
}

func planArtifactHasMissingAnchorProvenance(artifact PlanArtifact) bool {
	for _, edit := range artifact.Edits {
		if strings.TrimSpace(edit.OldText) != "" && edit.AnchorProvenance == nil {
			return true
		}
	}
	return false
}

func workspaceRootFromPlanArtifactPath(planPath string) string {
	clean := filepath.Clean(planPath)
	plansDir := filepath.Dir(clean)
	if filepath.Base(plansDir) != "plans" {
		return ""
	}
	idiomDir := filepath.Dir(plansDir)
	if filepath.Base(idiomDir) != ".idiom" {
		return ""
	}
	root := filepath.Dir(idiomDir)
	if root == "." || root == string(filepath.Separator) {
		return ""
	}
	return root
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
