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

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/planartifactstore"
)

const (
	SubmitPlanEmptyEditsCode                      = "submit_plan.empty_edits"
	SubmitPlanSourceSatisfiedTerminalRequiredCode = "submit_plan.source_satisfied_terminal_required"
)

const PlanEditAnchorProvenanceSchemaVersion = "idiom.plan_edit_anchor.v1"

const planInlineTextSpillThreshold = 4096

func submitPlanNoOpEditGuidanceForPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "remove any no-op edit object; use top-level `source_satisfied_paths` for already-final paths, submit real edits, or answer `" + plancontract.SourceSatisfiedDecision + "` if every required path is final"
	}

	return fmt.Sprintf("for %s: remove no-op edit; use `source_satisfied_paths`: [%q] if final; use `%s` only if all paths are final; else submit real edit", path, path, plancontract.SourceSatisfiedDecision)
}

// submitPlan persists the planner's edit-plan artifact to disk and
// returns the path so the caller (orchestrator) can find and execute
// it. The artifact lives under .idiom/plans/<timestamp>.json by
// default.
func (m *Manager) submitPlan(req ToolRequest) ToolResult {
	if req.PlanArtifact == nil && len(req.Edits) == 0 {
		if SubmitPlanRequestClaimsSourceSatisfied(req) {
			errorMessage := fmt.Sprintf("submit_plan cannot carry a source-satisfied/no-op closeout. Answer `%s` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.", plancontract.SourceSatisfiedDecision)
			result := toolInvalidArgumentResult(
				ToolNameSubmitPlan,
				"submit_plan rejected: source-satisfied terminal shape required",
				errorMessage,
			)

			result.Code = SubmitPlanSourceSatisfiedTerminalRequiredCode

			return result
		}

		result := toolInvalidArgumentResult(ToolNameSubmitPlan, "submit_plan rejected", "submit_plan requires at least one edit in the edits array")

		result.Code = SubmitPlanEmptyEditsCode

		return result
	}

	stamp := time.Now().UTC().Format("20060102-150405.000")

	stamp = strings.ReplaceAll(stamp, ".", "")

	created := time.Now().UTC().Format(time.RFC3339Nano)
	planID := "plan_" + stamp

	artifact, err := newPlanArtifactFromSubmitPlanRequest(req, m.cfg.SessionID, created, planID)
	if err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.InvalidArgument, reasonPlanSubmissionRejected)
	}

	if planRationaleClaimsNoEditsNeeded(artifact.Rationale) && !planArtifactAllowsMixedSourceSatisfiedRationale(artifact) {
		result := withToolFailure(ToolResult{
			Tool:    ToolNameSubmitPlan,
			Summary: "submit_plan rejected: source-satisfied rationale",
			Error:   fmt.Sprintf("submit_plan rationale says the requested final state is already present or that edits are placeholders. Do not submit verification/placeholder edits; answer `%s` with per-path current-state evidence, or answer <<QUESTION>> with the exact blocker.", plancontract.SourceSatisfiedDecision),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)

		result.Code = SubmitPlanSourceSatisfiedTerminalRequiredCode

		return result
	}

	if len(artifact.Edits) == 0 {
		result := toolInvalidArgumentResult(ToolNameSubmitPlan, "submit_plan rejected", "submit_plan requires at least one edit in the edits array")

		result.Code = SubmitPlanEmptyEditsCode

		return result
	}

	m.repairPlanArtifactOldTextAnchors(&artifact)

	validationArtifact := artifact

	validationArtifact.Edits = append([]planartifact.PlanEdit(nil), artifact.Edits...)

	refDir, err := m.planArtifactDir()
	if err != nil {
		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("resolve plan dir: %w", err), failures.Internal, reasonToolWriteFailed)
	}
	if err := planartifactstore.HydrateRefs(refDir, &validationArtifact); err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.InvalidArgument, reasonPlanSubmissionRejected)
	}

	if planEditsAreVerificationOnly(artifact.Edits) {
		return withToolFailure(ToolResult{
			Tool:    ToolNameSubmitPlan,
			Summary: "submit_plan rejected: verification-only edits",
			Error:   fmt.Sprintf("submit_plan edits describe verification/checking work rather than state mutation. If the files already satisfy the task, answer `%s`; otherwise submit only real corrective edits or answer <<QUESTION>> with the exact blocker.", plancontract.SourceSatisfiedDecision),
		}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	for i, edit := range validationArtifact.Edits {
		if err := validatePlanEdit(edit); err != nil {
			return toolFailureResult(ToolNameSubmitPlan, "submit_plan rejected", fmt.Sprintf("edits[%d] path %q: %s", i, edit.Path, err.Error()), failures.InvalidArgument, reasonPlanSubmissionRejected)
		}

		if err := m.validatePlanEditAgainstWorkspace(edit); err != nil {
			return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", fmt.Errorf("edits[%d] path %q: %w", i, edit.Path, err), failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
	}

	if err := m.validatePlanEditSequenceAgainstWorkspace(validationArtifact.Edits); err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	artifact.Edits, err = AttachPlanEditAnchorProvenance(m.cfg.Root, artifact.Edits)
	if err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.InvalidArgument, reasonPlanArtifactSchemaInvalid)
	}

	audit := AuditPlanArtifact(m.cfg.Root, artifact, PlanArtifactAuditOptions{EnforcePacketEvidence: req.PlanArtifact != nil})
	if !audit.OK {
		return withToolFailure(ToolResult{Tool: ToolNameSubmitPlan, Summary: "submit_plan rejected", Output: audit.SummaryLine(), Error: audit.ErrorSummary()}, failures.FailedPrecondition, reasonPlanSubmissionRejected)
	}

	dir, err := m.planArtifactDir()
	if err != nil {
		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("resolve plan dir: %w", err), failures.Internal, reasonToolWriteFailed)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("create plan dir: %w", err), failures.Internal, reasonToolWriteFailed)
	}

	edits, spilled, err := spillLargePlanEditBodies(m.cfg.Root, dir, stamp, artifact.Edits)
	if err != nil {
		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan failed", err, failures.Internal, reasonToolWriteFailed)
	}

	artifact.Edits = edits
	artifact = planartifact.NormalizeForSubmit(artifact, planartifact.NormalizeForSubmitOptions{
		SessionID:     m.cfg.SessionID,
		Created:       created,
		PlanID:        planID,
		LegacyRequest: req.PlanArtifact == nil,
	})

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		cleanupPlanEditBodyRefs(m.cfg.Root, dir, artifact.Edits)

		return toolFailureFromMaybeTypedErrorResult(ToolNameSubmitPlan, "submit_plan rejected", err, failures.InvalidArgument, reasonPlanArtifactSchemaInvalid)
	}

	path, _, err := planartifactstore.StoreFilePath(m.cfg.Root, stamp+".plan.json")
	if err != nil {
		cleanupPlanEditBodyRefs(m.cfg.Root, dir, artifact.Edits)

		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("resolve artifact path: %w", err), failures.Internal, reasonToolWriteFailed)
	}

	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		cleanupPlanEditBodyRefs(m.cfg.Root, dir, artifact.Edits)

		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("marshal artifact: %w", err), failures.Internal, reasonToolInternalFailure)
	}

	if err := atomicfile.WriteFile(path, body, 0o644); err != nil {
		cleanupPlanEditBodyRefs(m.cfg.Root, dir, artifact.Edits)

		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("write artifact: %w", err), failures.Internal, reasonToolWriteFailed)
	}

	pointer, _, err := planartifactstore.StoreFilePath(m.cfg.Root, "last.plan.json")
	if err != nil {
		return toolFailureFromErrorResult(ToolNameSubmitPlan, "submit_plan failed", fmt.Errorf("resolve plan pointer: %w", err), failures.Internal, reasonToolWriteFailed)
	}

	_ = atomicfile.WriteFile(pointer, body, 0o644)

	return ToolResult{
		Tool:    ToolNameSubmitPlan,
		Summary: fmt.Sprintf("Plan submitted with %d edit(s); the executor phase will dispatch them.", len(artifact.Edits)),
		Output:  fmt.Sprintf("plan_path: %s\nedits: %d\nschema_version: %s\ncontent_refs: %d\n%s", path, len(artifact.Edits), planartifact.SchemaVersion, spilled, audit.SummaryLine()),
	}
}

// SubmitPlanRequestClaimsSourceSatisfied reports whether a submit_plan request
// is trying to encode the terminal source-satisfied closeout instead of a real
// edit plan.
func SubmitPlanRequestClaimsSourceSatisfied(req ToolRequest) bool {
	decision := strings.ToLower(strings.TrimSpace(req.Decision))

	decision = strings.ReplaceAll(decision, "_", "-")

	if decision == "source-satisfied" || decision == "source satisfied" {
		return true
	}

	return planRationaleClaimsNoEditsNeeded(req.Rationale)
}

func planArtifactAllowsMixedSourceSatisfiedRationale(artifact planartifact.PlanArtifact) bool {
	if len(artifact.Edits) == 0 || len(artifact.Paths.SourceSatisfied) == 0 {
		return false
	}

	rationale := planRationaleText(artifact.Rationale)
	if rationale == "" {
		return false
	}

	if planRationaleClaimsPlaceholderEdits(rationale) || planRationaleClaimsUnscopedNoWork(rationale) {
		return false
	}

	return planRationaleClaimsSourceSatisfied(rationale)
}

// validatePlanEdit delegates executable edit-shape ownership to the durable
// plan artifact contract, then layers submit_plan intake constraints that
// depend on workspace policy or planner repair guidance.
func validatePlanEdit(edit planartifact.PlanEdit) error {
	if err := planartifact.ValidatePlanEditShape(edit); err != nil {
		return err
	}

	if err := validatePlanEditAnchorProvenanceShape(edit.AnchorProvenance); err != nil {
		return err
	}

	hasOldText := strings.TrimSpace(edit.OldText) != ""
	hasSubstring := hasOldText && planartifact.PlanEditHasExplicitReplacementText(edit)

	if edit.InsertAfterLine > 0 {
		if len(edit.Content) > planInlineTextSpillThreshold {
			return fmt.Errorf("insert_after_line content exceeds inline submit_plan limit (%d bytes); content_ref is not supported for insert_after_line in this schema; for large insertions in existing files, use a line-range replacement that includes the unchanged anchor line(s) plus inserted content", planInlineTextSpillThreshold)
		}
	}

	if hasSubstring && strings.TrimSpace(edit.NewTextRef) == "" && edit.OldText == edit.NewText {
		return fmt.Errorf("old_text and new_text are identical; %s", submitPlanNoOpEditGuidanceForPath(edit.Path))
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
func PlanEditUnsupportedProofClaimReason(edit planartifact.PlanEdit) string {
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
func PlanEditAddedText(edit planartifact.PlanEdit) string {
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
			return fmt.Sprintf("unsupported proof claim: no-op assertion on line %d (%q); submit a concrete state assertion or report a blocker", lineNo+1, truncateUnsupportedProofLine(line))
		}

		if phrase := selfDisqualifyingProofPhrase(line); phrase != "" {
			return fmt.Sprintf("unsupported proof claim: self-disqualifying verification text %q on line %d (%q); submit concrete evidence or report a blocker", phrase, lineNo+1, truncateUnsupportedProofLine(line))
		}
	}

	return ""
}

func truncateUnsupportedProofLine(line string) string {
	line = strings.TrimSpace(line)
	if len(line) <= 120 {
		return line
	}

	return line[:117] + "..."
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
		"just verify precondition",
		"just verify preconditions",
		"only verify precondition",
		"only verify preconditions",
		"verify precondition",
		"verify preconditions",
		"precondition only",
		"preconditions only",
		"setup-only",
		"setup only",
		"verify that it exists",
		"exists and can be called",
		"exists and can be accessed",
		"for now,",
		"for now ",
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

func planEditPlaceholderField(edit planartifact.PlanEdit) (string, string) {
	fields := []struct {
		name       string
		value      string
		allowTODO  bool
		sourceLike bool
	}{
		{name: "old_text", value: edit.OldText, allowTODO: true},
		{name: "new_text", value: edit.NewText, sourceLike: isSourceLikePath(edit.Path)},
		{name: "content", value: edit.Content, sourceLike: isSourceLikePath(edit.Path)},
	}
	for _, field := range fields {
		if marker := planEditPlaceholderMarker(field.value, field.allowTODO, field.sourceLike); marker != "" {
			return field.name, marker
		}
	}

	return "", ""
}

func planEditPlaceholderMarker(value string, allowTODO bool, sourceLike bool) string {
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
	} {
		if strings.Contains(text, marker) {
			return marker
		}
	}

	if !allowTODO {
		for _, marker := range []string{"<todo", "todo: fill"} {
			if strings.Contains(text, marker) {
				return marker
			}
		}

		if sourceLike && strings.Contains(text, "todo:") {
			return "todo:"
		}
	}

	return ""
}

// AttachPlanEditAnchorProvenance returns a copy of edits with harness-owned
// provenance attached to bounded existing-file edit anchors. Existing
// provenance is ignored and overwritten; missing/new-file edits get nil
// provenance because they intentionally create the full file body.
func AttachPlanEditAnchorProvenance(workspaceRoot string, edits []planartifact.PlanEdit) ([]planartifact.PlanEdit, error) {
	out := append([]planartifact.PlanEdit(nil), edits...)
	for i, edit := range out {
		if !planEditNeedsAnchorProvenance(edit) {
			out[i].AnchorProvenance = nil
			continue
		}

		provenance, err := buildPlanEditAnchorProvenance(workspaceRoot, edit)
		if err != nil {
			return nil, fmt.Errorf("edits[%d] path %q: %w", i, edit.Path, err)
		}

		out[i].AnchorProvenance = &provenance
	}

	return out, nil
}

// ValidatePlanEditAnchorProvenance verifies that a persisted bounded edit still
// targets the same file bytes submit_plan validated. Missing provenance is
// accepted for legacy artifacts; malformed or stale provenance is rejected
// before executor writes.
func ValidatePlanEditAnchorProvenance(workspaceRoot string, edit planartifact.PlanEdit) error {
	provenance := edit.AnchorProvenance
	if provenance == nil {
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

	current := string(body)
	currentFileDigest := planEditSHA256(body)

	if provenance.FileSHA256 != currentFileDigest {
		return fmt.Errorf("anchor provenance stale for %s: file fingerprint changed since submit_plan validation", rel)
	}

	anchor, err := planEditCurrentAnchorText(current, edit)
	if err != nil {
		return fmt.Errorf("anchor provenance stale for %s: %w", rel, err)
	}

	if provenance.AnchorSHA256 != planEditSHA256([]byte(anchor)) {
		return fmt.Errorf("anchor provenance stale for %s: edit anchor fingerprint changed since submit_plan validation", rel)
	}

	if provenance.StartByte < 0 || provenance.EndByte < provenance.StartByte || provenance.EndByte > len(current) {
		return fmt.Errorf("anchor provenance stale for %s: byte range is outside current file", rel)
	}

	if current[provenance.StartByte:provenance.EndByte] != anchor {
		return fmt.Errorf("anchor provenance stale for %s: anchored byte range no longer matches edit anchor", rel)
	}

	matchCount := strings.Count(current, anchor)
	if matchCount != provenance.MatchCount {
		return fmt.Errorf("anchor provenance stale for %s: edit anchor match count changed from %d to %d", rel, provenance.MatchCount, matchCount)
	}

	return nil
}

func buildPlanEditAnchorProvenance(workspaceRoot string, edit planartifact.PlanEdit) (planartifact.PlanEditAnchorProvenance, error) {
	abs, rel, err := planEditWorkspacePath(workspaceRoot, edit.Path)
	if err != nil {
		return planartifact.PlanEditAnchorProvenance{}, err
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return planartifact.PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance could not read %s: %w", rel, err)
	}

	current := string(body)

	anchor, err := planEditCurrentAnchorText(current, edit)
	if err != nil {
		return planartifact.PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance could not select edit anchor in %s: %w", rel, err)
	}

	matchCount := strings.Count(current, anchor)
	if matchCount == 0 {
		return planartifact.PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance could not find edit anchor in %s", rel)
	}

	if matchCount > 1 && strings.TrimSpace(edit.OldText) != "" && !edit.ReplaceAll {
		return planartifact.PlanEditAnchorProvenance{}, fmt.Errorf("anchor provenance found %d matches in %s; exact substring edits must be unique unless replace_all=true", matchCount, rel)
	}

	startByte := strings.Index(current, anchor)
	if edit.StartLine > 0 || edit.InsertAfterLine > 0 {
		if spanStart, spanEnd, ok := planEditAnchorLineOffsets(current, edit); ok {
			startByte = spanStart
			_ = spanEnd
		}
	}

	endByte := startByte + len(anchor)
	startLine := planEditLineNumberAtByte(current, startByte)
	endLine := startLine

	if endByte > startByte {
		endLine = planEditLineNumberAtByte(current, endByte-1)
	}

	return planartifact.PlanEditAnchorProvenance{
		SchemaVersion: PlanEditAnchorProvenanceSchemaVersion,
		Source:        ToolNameSubmitPlan,
		Path:          rel,
		FileSHA256:    planEditSHA256(body),
		AnchorSHA256:  planEditSHA256([]byte(anchor)),
		StartByte:     startByte,
		EndByte:       endByte,
		StartLine:     startLine,
		EndLine:       endLine,
		MatchCount:    matchCount,
	}, nil
}

func planEditNeedsAnchorProvenance(edit planartifact.PlanEdit) bool {
	if strings.TrimSpace(edit.OldText) != "" {
		return true
	}

	if edit.StartLine > 0 || edit.EndLine > 0 || edit.InsertAfterLine > 0 {
		return !planEditIsWholeFileWrite(edit)
	}

	return false
}

func PlanEditNeedsAnchorProvenance(edit planartifact.PlanEdit) bool {
	return planEditNeedsAnchorProvenance(edit)
}

func planEditCurrentAnchorText(current string, edit planartifact.PlanEdit) (string, error) {
	if strings.TrimSpace(edit.OldText) != "" {
		return edit.OldText, nil
	}

	start, end, ok := planEditAnchorLineOffsets(current, edit)
	if !ok {
		return "", fmt.Errorf("line anchor is outside current source")
	}

	if start < 0 || end <= start || end > len(current) {
		return "", fmt.Errorf("line anchor is empty or invalid")
	}

	return current[start:end], nil
}

func planEditAnchorLineOffsets(current string, edit planartifact.PlanEdit) (int, int, bool) {
	spans := lineSpans(current)
	if len(spans) == 0 {
		return 0, 0, false
	}

	if edit.InsertAfterLine > 0 {
		line := edit.InsertAfterLine
		if line > len(spans) {
			return 0, 0, false
		}

		span := spans[line-1]

		return span.start, span.end, true
	}

	startLine := edit.StartLine
	endLine := edit.EndLine

	if endLine == 0 {
		endLine = startLine
	}

	if startLine <= 0 || endLine < startLine || startLine > len(spans) || endLine > len(spans) {
		return 0, 0, false
	}

	return spans[startLine-1].start, spans[endLine-1].end, true
}

func validatePlanEditAnchorProvenanceShape(provenance *planartifact.PlanEditAnchorProvenance) error {
	if provenance == nil {
		return nil
	}

	if provenance.SchemaVersion != PlanEditAnchorProvenanceSchemaVersion {
		return fmt.Errorf("anchor_provenance schema_version must be %s", PlanEditAnchorProvenanceSchemaVersion)
	}

	if strings.TrimSpace(provenance.Source) != ToolNameSubmitPlan {
		return fmt.Errorf("anchor_provenance source must be submit_plan")
	}

	if !planartifact.PathIsSafe(provenance.Path) {
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

	if !planartifact.PathIsSafe(rel) {
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
	text := planRationaleText(rationale)
	if text == "" {
		return false
	}

	return planRationaleClaimsPlaceholderEdits(text) ||
		planRationaleClaimsUnscopedNoWork(text) ||
		planRationaleClaimsSourceSatisfied(text)
}

func planRationaleText(rationale string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(rationale))), " ")
}

func planRationaleClaimsPlaceholderEdits(text string) bool {
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

	return false
}

func planRationaleClaimsUnscopedNoWork(text string) bool {
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

	return false
}

func planRationaleClaimsSourceSatisfied(text string) bool {
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

func planEditsAreVerificationOnly(edits []planartifact.PlanEdit) bool {
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

func (m *Manager) validatePlanEditAgainstWorkspace(edit planartifact.PlanEdit) error {
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

	if err == nil {
		if rejectErr := RejectExistingSourceWholeFileRewrite(path, body, edit); rejectErr != nil {
			return rejectErr
		}
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

			if rejected := m.rejectInvalidPlannedWrite(ToolNameSubmitPlan, "Plan edit rejected", path, []byte(updated)); rejected != nil {
				errorText := strings.TrimSpace(rejected.Error)
				if edit.StartLine > 0 && edit.EndLine >= edit.StartLine {
					errorText += fmt.Sprintf("\nLine-range edit detail: submit_plan edit for %s declared start_line=%d and end_line=%d. The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it, otherwise trim trailing unchanged context from content. If you are inserting after a current line, use insert_after_line with non-empty content instead.", edit.Path, edit.StartLine, edit.EndLine)
				}

				return fmt.Errorf("%w: %s", ErrPlanSubmissionRejected, errorText)
			}

			if err := validateContentOnlyPlanEditAgainstCurrentSource(path, string(body), edit); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("edit would not change %s; %s", edit.Path, submitPlanNoOpEditGuidanceForPath(edit.Path))
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("old_text preflight could not read %s: %w", edit.Path, err)
	}

	current := string(body)
	matchCount := strings.Count(current, oldText)

	if matchCount == 0 {
		if textutil.ContainsFold(current, oldText) {
			return fmt.Errorf("old_text did not appear in %s, but a case-insensitive match exists; preserve exact casing from the file or use start_line/end_line+content", edit.Path)
		}

		if planEditReplacementAlreadyPresent(current, edit) {
			return fmt.Errorf("old_text did not appear in %s, but the replacement text already appears in the file; if every required edit is already present, close the planner phase with `%s` and per-path current-state evidence instead of submitting stale edits", edit.Path, plancontract.SourceSatisfiedDecision)
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

		if rejected := m.rejectInvalidPlannedWrite(ToolNameSubmitPlan, "Plan edit rejected", path, []byte(updated)); rejected != nil {
			return fmt.Errorf("%w: %s", ErrPlanSubmissionRejected, strings.TrimSpace(rejected.Error))
		}
	} else {
		return fmt.Errorf("edit would not change %s; %s", edit.Path, submitPlanNoOpEditGuidanceForPath(edit.Path))
	}

	return nil
}

func (m *Manager) validatePlanEditSequenceAgainstWorkspace(edits []planartifact.PlanEdit) error {
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

		if rejectErr := RejectExistingSourceWholeFileRewrite(path, body, edit); rejectErr != nil {
			return fmt.Errorf("edits[%d]: %w", i, rejectErr)
		}

		updated, changed, previewErr := PreviewPlanEditContent(string(body), edit)
		if previewErr != nil {
			return fmt.Errorf("edits[%d]: after applying prior plan edits, %s", i, previewErr.Error())
		}

		if !changed {
			return fmt.Errorf("edits[%d]: edit would not change %s after applying prior plan edits; %s", i, edit.Path, submitPlanNoOpEditGuidanceForPath(edit.Path))
		}

		if lineNo, line, ok := NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
			return fmt.Errorf("edits[%d]: edit would introduce adjacent duplicate code line %d in %s after applying prior plan edits: %q", i, lineNo, edit.Path, line)
		}

		if rejected := m.rejectInvalidPlannedWrite(ToolNameSubmitPlan, "Plan edit rejected", path, []byte(updated)); rejected != nil {
			errorText := strings.TrimSpace(rejected.Error)
			if edit.StartLine > 0 && edit.EndLine >= edit.StartLine {
				errorText += fmt.Sprintf("\nLine-range edit detail: submit_plan edit for %s declared start_line=%d and end_line=%d. The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it, otherwise trim trailing unchanged context from content. If you are inserting after a current line, use insert_after_line with non-empty content instead.", edit.Path, edit.StartLine, edit.EndLine)
			}

			return fmt.Errorf("edits[%d]: after applying prior plan edits, %s", i, errorText)
		}

		if strings.TrimSpace(edit.OldText) == "" {
			if err := validateContentOnlyPlanEditAgainstCurrentSource(path, string(body), edit); err != nil {
				return fmt.Errorf("edits[%d]: after applying prior plan edits, %s", i, err.Error())
			}
		}

		projected[path] = []byte(updated)
	}

	return nil
}

func validateContentOnlyPlanEditAgainstCurrentSource(path string, current string, edit planartifact.PlanEdit) error {
	if !isSourceLikePath(path) || strings.TrimSpace(current) == "" || strings.TrimSpace(edit.OldText) != "" {
		return nil
	}

	if edit.StartLine > 0 || edit.EndLine > 0 {
		if err := validateSourceLineRangePlanEdit(path, current, edit); err != nil {
			return err
		}
	}

	if (edit.StartLine > 0 || edit.InsertAfterLine > 0) && sourceEditContentDuplicatesExistingBlock(current, edit) {
		return fmt.Errorf("content-only edit would duplicate an existing source block in %s; remove this edit and use source_satisfied_paths if the requested final state already exists, or submit an old_text/new_text edit anchored to the current source", filepath.ToSlash(path))
	}

	return nil
}

// RejectExistingSourceWholeFileRewrite rejects whole-file replacement of an
// existing source/test file. New files still use replace_all:true, but existing
// source edits need bounded anchors so destructive rewrites carry provenance and
// cannot erase unrelated code by accident.
func RejectExistingSourceWholeFileRewrite(path string, current []byte, edit planartifact.PlanEdit) error {
	if !planEditIsWholeFileWrite(edit) || len(current) == 0 || !isSourceLikePath(path) {
		return nil
	}

	return fmt.Errorf("whole-file replace_all edit would rewrite existing source file %s; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker", filepath.ToSlash(path))
}

func validateSourceLineRangePlanEdit(path string, current string, edit planartifact.PlanEdit) error {
	if strings.TrimSpace(edit.Content) == "" {
		return nil
	}

	spans := lineSpans(current)
	if len(spans) == 0 || edit.StartLine <= 0 || edit.EndLine < edit.StartLine ||
		edit.StartLine > len(spans) || edit.EndLine > len(spans) {

		return nil
	}

	startOffset := spans[edit.StartLine-1].start
	endOffset := spans[edit.EndLine-1].end
	selected := current[startOffset:endOffset]

	if err := RejectExistingSourceDestructiveLineRangeRewrite(path, current, edit); err != nil {
		return err
	}

	if err := RejectSourceLineRangeTopLevelConstructDrift(path, selected, edit); err != nil {
		return err
	}

	if lineRangeReplacementDuplicatesPrecedingContext(current, spans, edit.StartLine, edit.Content) {
		return fmt.Errorf("line-range replacement for %s lines %d-%d appears to duplicate context immediately before the selected range; use old_text/new_text with a unique current-source anchor for insertions", filepath.ToSlash(path), edit.StartLine, edit.EndLine)
	}

	selectedLines := textutil.NonEmptyTrimmedLines(selected)
	replacementLines := textutil.NonEmptyTrimmedLines(edit.Content)

	if len(selectedLines) == 0 || len(replacementLines) <= len(selectedLines) {
		return nil
	}

	for _, selectedLine := range selectedLines {
		for _, replacementLine := range replacementLines {
			if selectedLine == replacementLine {
				return nil
			}
		}
	}

	return fmt.Errorf("source line-range edit for %s lines %d-%d expands the selected range but does not preserve any selected anchor line; for insertions, use old_text/new_text or replace the anchor line(s) with unchanged anchor line(s) plus new content", filepath.ToSlash(path), edit.StartLine, edit.EndLine)
}

// RejectExistingSourceDestructiveLineRangeRewrite rejects line-range edits that
// smuggle a whole-file or broad destructive rewrite through an anchored range.
// Small replacements are valid; broad source changes need smaller anchors or an
// explicit blocker because they can silently erase unrelated code.
func RejectExistingSourceDestructiveLineRangeRewrite(path string, current string, edit planartifact.PlanEdit) error {
	if !isSourceLikePath(path) || strings.TrimSpace(current) == "" ||
		strings.TrimSpace(edit.Content) == "" ||
		edit.StartLine <= 0 || edit.EndLine < edit.StartLine {

		return nil
	}

	spans := lineSpans(current)
	if len(spans) == 0 || edit.StartLine > len(spans) || edit.EndLine > len(spans) {
		return nil
	}

	selectedLineCount := edit.EndLine - edit.StartLine + 1
	if selectedLineCount >= len(spans) {
		return fmt.Errorf("line-range replacement for %s lines %d-%d would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors, or answer <<QUESTION>> with the exact missing body/anchor blocker", filepath.ToSlash(path), edit.StartLine, edit.EndLine)
	}

	selectedNonEmpty := len(textutil.NonEmptyTrimmedLines(current[spans[edit.StartLine-1].start:spans[edit.EndLine-1].end]))
	replacementNonEmpty := len(textutil.NonEmptyTrimmedLines(edit.Content))

	if selectedLineCount >= 40 && selectedNonEmpty >= 20 && replacementNonEmpty*2 < selectedNonEmpty {
		return fmt.Errorf("line-range replacement for %s lines %d-%d would collapse %d non-empty source lines to %d; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker", filepath.ToSlash(path), edit.StartLine, edit.EndLine, selectedNonEmpty, replacementNonEmpty)
	}

	return nil
}

func RejectSourceLineRangeTopLevelConstructDrift(path string, selected string, edit planartifact.PlanEdit) error {
	if !cLikeSourcePath(path) || strings.TrimSpace(edit.Content) == "" {
		return nil
	}

	selectedHasPreprocessor := sourceTextHasPreprocessorDirective(selected)

	if edit.StartLine > 1 && !selectedHasPreprocessor {
		if directive := firstFileScopePrologueDirective(edit.Content); directive != "" {
			return fmt.Errorf("line-range replacement for %s lines %d-%d introduces file-scope preprocessor directive %q into a range that did not select existing preprocessor context; use a top-of-file anchored edit or answer <<QUESTION>> with the exact missing body/anchor blocker", filepath.ToSlash(path), edit.StartLine, edit.EndLine, directive)
		}
	}

	if edit.StartLine == 1 && !selectedHasPreprocessor {
		if directive := leadingHeaderGuardDirective(edit.Content); directive != "" {
			return fmt.Errorf("line-range replacement for %s lines %d-%d starts a header guard before selecting the existing file prologue/include guard; use a bounded anchor that includes the current guard/include line or answer <<QUESTION>> with the exact missing body/anchor blocker", filepath.ToSlash(path), edit.StartLine, edit.EndLine)
		}
	}

	return nil
}

func firstFileScopePrologueDirective(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if sourceLineIsFileScopePrologueDirective(trimmed) {
			return trimmed
		}
	}

	return ""
}

func leadingHeaderGuardDirective(text string) string {
	inBlockComment := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed, "*/") {
				inBlockComment = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "*") {
			continue
		}
		if sourceLineIsHeaderGuardDirective(trimmed) {
			return trimmed
		}
		return ""
	}

	return ""
}

func sourceTextHasPreprocessorDirective(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if sourceLineIsFilePrologueDirective(strings.TrimSpace(line)) {
			return true
		}
	}

	return false
}

func sourceLineIsFilePrologueDirective(line string) bool {
	return sourceLineIsFileScopePrologueDirective(line) ||
		sourceLineIsHeaderGuardDirective(line)
}

func sourceLineIsFileScopePrologueDirective(line string) bool {
	return strings.HasPrefix(line, "#include") ||
		strings.HasPrefix(line, "#pragma once")
}

func sourceLineIsHeaderGuardDirective(line string) bool {
	for _, prefix := range []string{
		"#if", "#ifdef", "#ifndef", "#elif", "#else", "#endif",
		"#define", "#undef", "#error", "#warning",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}

func cLikeSourcePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return true
	default:
		return false
	}
}

func sourceEditContentDuplicatesExistingBlock(current string, edit planartifact.PlanEdit) bool {
	content := strings.TrimSpace(edit.Content)
	if content == "" || len(textutil.NonEmptyTrimmedLines(content)) < 2 {
		return false
	}

	search := current
	if edit.StartLine > 0 || edit.EndLine > 0 {
		spans := lineSpans(current)
		if len(spans) > 0 && edit.StartLine > 0 {
			endLine := edit.EndLine
			if endLine == 0 {
				endLine = edit.StartLine
			}

			if endLine >= edit.StartLine && edit.StartLine <= len(spans) && endLine <= len(spans) {
				startOffset := spans[edit.StartLine-1].start
				endOffset := spans[endLine-1].end

				search = current[:startOffset] + current[endOffset:]
			}
		}
	}

	return strings.Contains(search, content)
}

func (m *Manager) repairPlanArtifactOldTextAnchors(artifact *planartifact.PlanArtifact) {
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

// PreviewPlanEditContent applies one planartifact.PlanEdit to current content without
// touching disk. It is shared by submit_plan validation and the deterministic
// executor preflight so malformed or suspicious plans fail before writes land.
func PreviewPlanEditContent(current string, edit planartifact.PlanEdit) (string, bool, error) {
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

func planEditIsWholeFileWrite(edit planartifact.PlanEdit) bool {
	return edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != ""
}

func planEditReplacementAlreadyPresent(current string, edit planartifact.PlanEdit) bool {
	replacement := strings.TrimSpace(edit.NewText)
	if replacement == "" || strings.TrimSpace(edit.NewTextRef) != "" {
		return false
	}

	return strings.Contains(current, edit.NewText)
}

func previewPlanLineRangeEdit(current string, edit planartifact.PlanEdit) (string, error) {
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

func previewPlanInsertAfterLineEdit(current string, edit planartifact.PlanEdit) (string, error) {
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

func spillLargePlanEditBodies(root string, dir string, stamp string, edits []planartifact.PlanEdit) ([]planartifact.PlanEdit, int, error) {
	out := append([]planartifact.PlanEdit(nil), edits...)
	spilled := 0

	for i := range out {
		if shouldSpillPlanInlineText(out[i].NewText) {
			ref, err := writePlanEditBodyRef(root, dir, stamp, i, "new_text", out[i].NewText)
			if err != nil {
				cleanupPlanEditBodyRefs(root, dir, out)

				return nil, 0, err
			}

			out[i].NewText = ""
			out[i].NewTextRef = ref
			spilled++
		}

		if shouldSpillPlanInlineText(out[i].Content) {
			if out[i].InsertAfterLine > 0 {
				return nil, 0, fmt.Errorf("edits[%d]: insert_after_line content exceeds inline submit_plan limit (%d bytes); content_ref is not supported for insert_after_line in this schema; for large insertions in existing files, use a line-range replacement that includes the unchanged anchor line(s) plus inserted content", i, planInlineTextSpillThreshold)
			}

			ref, err := writePlanEditBodyRef(root, dir, stamp, i, "content", out[i].Content)
			if err != nil {
				cleanupPlanEditBodyRefs(root, dir, out)

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

func cleanupPlanEditBodyRefs(root string, dir string, edits []planartifact.PlanEdit) {
	for _, edit := range edits {
		for _, ref := range []string{edit.NewTextRef, edit.ContentRef} {
			path, ok := planEditBodyRefPath(root, dir, ref)
			if !ok {
				continue
			}

			_ = os.Remove(path)
		}
	}
}

func writePlanEditBodyRef(root string, dir string, stamp string, index int, field string, content string) (string, error) {
	assetDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return "", fmt.Errorf("create plan asset dir: %w", err)
	}

	name := fmt.Sprintf("%s-edit-%03d-%s.txt", stamp, index+1, field)
	path, _, err := planartifactstore.StoreAssetPath(root, name)
	if err != nil {
		return "", fmt.Errorf("resolve plan asset %s: %w", name, err)
	}

	if err := atomicfile.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write plan asset %s: %w", name, err)
	}

	return filepath.ToSlash(filepath.Join("assets", name)), nil
}

// planArtifactDir returns the directory plan artifacts live in,
// rooted at the workspace's repo root.
func (m *Manager) planArtifactDir() (string, error) {
	root := strings.TrimSpace(m.cfg.Root)
	if root == "" {
		root = "."
	}

	dir, _, err := planartifactstore.StoreDir(root)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// LoadPlanArtifact reads a plan artifact from disk and returns it.
// Used by the plan-execute orchestrator's executor phase.
func LoadPlanArtifact(path string) (planartifact.PlanArtifact, error) {
	artifact, err := planartifactstore.Load(path)
	if err != nil {
		return planartifact.PlanArtifact{}, err
	}

	for i, edit := range artifact.Edits {
		if err := validatePlanEdit(edit); err != nil {
			return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: edits[%d] path %q: %w", path, i, edit.Path, err)
		}
	}

	if err := attachMissingLoadedPlanAnchorProvenance(path, &artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	return artifact, nil
}

func attachMissingLoadedPlanAnchorProvenance(planPath string, artifact *planartifact.PlanArtifact) error {
	if artifact == nil || artifact.SchemaVersion != planartifact.SchemaVersion || !planArtifactHasMissingAnchorProvenance(*artifact) {
		return nil
	}

	if PlanArtifactRequiresSubmitPlanAnchorProvenance(*artifact) {
		return nil
	}

	root := workspaceRootFromPlanArtifactPath(planPath)
	if root == "" {
		return nil
	}

	for i, edit := range artifact.Edits {
		if !planEditNeedsAnchorProvenance(edit) || edit.AnchorProvenance != nil {
			continue
		}

		edits, err := AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{edit})
		if err == nil && len(edits) == 1 {
			artifact.Edits[i] = edits[0]
		}
	}

	return nil
}

func planArtifactHasMissingAnchorProvenance(artifact planartifact.PlanArtifact) bool {
	for _, edit := range artifact.Edits {
		if planEditNeedsAnchorProvenance(edit) && edit.AnchorProvenance == nil {
			return true
		}
	}

	return false
}

func PlanArtifactRequiresSubmitPlanAnchorProvenance(artifact planartifact.PlanArtifact) bool {
	return strings.TrimSpace(artifact.SchemaVersion) == planartifact.SchemaVersion &&
		strings.TrimSpace(string(artifact.Handoff.From)) == string(planartifact.OwnerPlanner) &&
		artifact.Handoff.To == planartifact.HandoffTargetPlanExecuteExecutor
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

func planEditBodyRefPath(root string, baseDir string, ref string) (string, bool) {
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

	if filepath.Dir(clean) == "assets" {
		safePath, _, err := planartifactstore.StoreAssetPath(root, filepath.Base(clean))
		if err != nil {
			return "", false
		}
		path = safePath
	}

	return path, true
}
