package planexecute

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

func planExecutePrompt(opts Options) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""
	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if hasPrompt {
		return opts.Prompt, nil
	}
	data, err := os.ReadFile(opts.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %q is empty", opts.PromptFile)
	}
	return prompt, nil
}

func planExecuteRequiredEditPaths(opts Options, prompt string) []string {
	paths := make([]string, 0, len(opts.RequiredEditPaths))
	paths = append(paths, []string(opts.RequiredEditPaths)...)
	paths = append(paths, RequiredEditPathsFromPrompt(prompt)...)
	return planExecuteUniqueRequiredPaths(paths)
}

func planExecuteAllowedEditPaths(opts Options) []string {
	return planExecuteUniqueRequiredPaths([]string(opts.AllowedEditPaths))
}

func inheritPlanExecutePromptPackID(plan *workspace.PlanArtifact, prompt string) string {
	if plan == nil || strings.TrimSpace(plan.PackID) != "" {
		return ""
	}
	packID := extractPlanExecutePromptPackID(prompt)
	if packID == "" {
		return ""
	}
	plan.PackID = packID
	if strings.TrimSpace(plan.Packet.PackID) == "" {
		plan.Packet.PackID = packID
	}
	if status := strings.TrimSpace(plan.Packet.Status); status == "" || status == "not_provided" {
		plan.Packet.Status = "provided"
	}
	return packID
}

func extractPlanExecutePromptPackID(prompt string) string {
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "pack") {
			continue
		}
		for _, token := range strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9'))
		}) {
			token = strings.ToLower(strings.TrimSpace(token))
			if isPlanExecutePackID(token) {
				return token
			}
		}
	}
	return ""
}

func isPlanExecutePackID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func patchPlanArtifactPackID(path string, packID string) error {
	packID = strings.TrimSpace(packID)
	if path == "" || packID == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	encodedPackID, err := json.Marshal(packID)
	if err != nil {
		return err
	}
	raw["pack_id"] = encodedPackID
	var packet map[string]json.RawMessage
	if body, ok := raw["packet"]; ok {
		_ = json.Unmarshal(body, &packet)
	}
	if packet == nil {
		packet = map[string]json.RawMessage{}
	}
	if current, ok := packet["pack_id"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" {
		packet["pack_id"] = encodedPackID
	}
	if current, ok := packet["status"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" || strings.Trim(strings.TrimSpace(string(current)), `"`) == "not_provided" {
		encodedStatus, err := json.Marshal("provided")
		if err != nil {
			return err
		}
		packet["status"] = encodedStatus
	}
	encodedPacket, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	raw["packet"] = encodedPacket
	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return os.WriteFile(path, updated, 0o644)
}

func planExecutePlannerSystemAddendum(requiredEditPaths []string, allowedEditPaths []string) string {
	lines := []string{
		"# Plan-then-execute (planner phase)",
		"",
		"The plan-submission capability must cover every brief-required edit once; when no required path list is present, use the objective and candidate/target refs to assemble a bounded real edit plan. The executor dispatches the submitted edits after you stop.",
		"Treat the brief body and any preloaded Context Pack below as the planner target packet.",
		"",
		"1. Per-edit write capabilities are blocked in the planner phase; the available write contract is `submit_plan`.",
		"2. Each edit: `path` + ONE of: (`old_text`+`new_text`; empty `new_text` deletes `old_text` | `start_line`+`end_line`+non-empty `content` | `replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write) + one-sentence `purpose`. Prefer substring/range edits; large bodies are spilled to artifact refs after submit_plan.",
		"3. One insertion site = one edit. Don't combine non-adjacent sites into one `old_text` (use \\n\\n only when sites are literally adjacent). Same `path` in two entries is fine.",
		"4. Anchor indentation must match the file byte-for-byte. Brief code blocks under markdown bullets carry 2+ extra leading spaces. Before copying any multi-line anchor, one bounded source-read capability may verify that path and strip the rendering indent. Single-line / identifier anchors are exempt.",
		"5. Exact anchors, current-state anchors, full new-file bodies, explicit replacement edit shapes, unified patches, and ranked packet evidence are sufficient planning evidence. Re-read the target packet before any source read.",
		"6. Use source-read capability only for a new named edit-safety fact on a required or candidate path. Do not re-read for confidence; if the brief names a missing required source/test path but gives only behavior or helper-shape requirements with no substantial body/anchor, stop with `<<QUESTION>>:` and name the missing body evidence.",
		"7. Submit as soon as you have anchors for every required path, or for the needed candidate paths when no required path list is present. If current source already contains the requested final state, answer `Decision: source-satisfied` and name every relevant path plus evidence instead of submitting stale edits. After `submit_plan` returns OK, finalize with a one-paragraph summary and stop. No post-plan verification reads.",
	}
	if checklist := planExecuteRequiredPathChecklist(requiredEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else if checklist := planExecuteAllowedPathChecklist(allowedEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else {
		lines = append(lines, "", "No required edit paths were provided. You still must either call `submit_plan` with real edits grounded in the objective/target refs, answer `Decision: source-satisfied` with evidence, or answer `<<QUESTION>>:` with the exact blocker.")
	}
	return strings.Join(lines, "\n")
}

func planExecuteRequiredPathChecklist(requiredEditPaths []string) string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Required `submit_plan` coverage for this run: %d required path(s).\n", len(paths))
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("Your first successful `submit_plan.edits` array must include at least one real edit for every path above, with no outside paths. Do not submit a partial plan and do not answer in prose while any listed path remains uncovered.")
	return strings.TrimSpace(b.String())
}

func planExecuteAllowedPathChecklist(allowedEditPaths []string) string {
	paths := planExecuteUniqueRequiredPaths(allowedEditPaths)
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Allowed `submit_plan` edit scope for this run: %d candidate path(s).\n", len(paths))
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("These paths are an allowlist, not coverage obligations. Submit edits only for the paths needed by the objective, and do not submit outside paths.")
	return strings.TrimSpace(b.String())
}

func planExecuteTryAutoExactPlan(cfg config.Config, prompt string, requiredEditPaths []string) (bool, workspace.ToolResult, string, int) {
	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, cfg.Workspace.Root)
	if len(edits) == 0 {
		return false, workspace.ToolResult{}, reason, 0
	}
	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Deterministic exact-anchor plan synthesized from the Required Edits packet.",
		Edits:     edits,
	})
	if strings.TrimSpace(result.Error) != "" {
		return false, result, "exact-anchor auto-plan rejected by submit_plan validation", len(edits)
	}
	return true, result, "all required paths covered by parseable exact old_text/new_text pairs", len(edits)
}

func planExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, "")
}

func planExecuteExactPlanEditsForWorkspace(prompt string, requiredEditPaths []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	required := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(required) == 0 {
		return nil, "no required_edit_paths"
	}
	section := ""
	for _, heading := range []string{"required edits", "edits to apply", "edits"} {
		if candidate := atomMarkdownSectionByHeading(prompt, heading); strings.TrimSpace(candidate) != "" {
			section = candidate
			break
		}
	}
	if strings.TrimSpace(section) == "" {
		return nil, "no exact-edit section"
	}

	type pendingOldBlock struct {
		paths []string
		text  string
	}
	var edits []workspace.PlanEdit
	var pending pendingOldBlock
	var current []string
	var currentPaths []string
	var blockPaths []string
	blockKind := ""
	previousNonEmpty := ""
	fence := ""
	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil
				blockPaths = append([]string(nil), currentPaths...)
				blockKind = planExecuteExactEditBlockKind(previousNonEmpty)
				continue
			}
			if trimmed != "" {
				if paths := atomRequiredEditPathsFromLabel(trimmed, required); len(paths) > 0 {
					currentPaths = paths
				}
				previousNonEmpty = trimmed
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			block := strings.TrimSpace(strings.Join(current, "\n"))
			switch blockKind {
			case "old":
				pending = pendingOldBlock{paths: append([]string(nil), blockPaths...), text: block}
			case "new":
				paths := pending.paths
				if len(paths) == 0 {
					paths = blockPaths
				}
				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}
				if len(paths) == 0 {
					paths = planExecuteInferPathsForOldText(workspaceRoot, required, pending.text)
				}
				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(pending.text) == "" {
						continue
					}
					edits = append(edits, workspace.PlanEdit{
						Path:    path,
						OldText: pending.text,
						NewText: block,
						Purpose: "Apply the exact old_text/new_text edit from the Required Edits packet.",
					})
				}
				pending = pendingOldBlock{}
			case "replace_all":
				paths := blockPaths
				if len(paths) == 0 && len(required) == 1 {
					paths = required
				}
				for _, path := range paths {
					path = strings.TrimSpace(path)
					if path == "" || strings.TrimSpace(block) == "" {
						continue
					}
					edits = append(edits, workspace.PlanEdit{
						Path:       path,
						OldText:    "",
						NewText:    block,
						ReplaceAll: true,
						Purpose:    "Create or replace the full file body from the Required Edits packet.",
					})
				}
			}
			fence = ""
			current = nil
			blockPaths = nil
			blockKind = ""
			previousNonEmpty = ""
			continue
		}
		current = append(current, rawLine)
	}
	if len(edits) == 0 {
		return nil, "no parseable exact old_text/new_text pairs"
	}
	missing := planExecuteMissingEditPaths(required, edits)
	if len(missing) > 0 {
		return nil, "exact old_text/new_text pairs do not cover required path(s): " + strings.Join(missing, ", ")
	}
	return edits, ""
}

func planExecuteInferPathsForOldText(workspaceRoot string, required []string, oldText string) []string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	oldText = strings.TrimSpace(oldText)
	if workspaceRoot == "" || oldText == "" {
		return nil
	}
	var matches []string
	for _, requiredPath := range required {
		path := normalizePlanExecutePath(requiredPath)
		if path == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		if strings.Count(string(body), oldText) == 1 {
			matches = append(matches, path)
		}
	}
	if len(matches) != 1 {
		return nil
	}
	return matches
}

func planExecuteExactEditBlockKind(label string) string {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch {
	case strings.HasPrefix(normalized, "old_text"), strings.HasPrefix(normalized, "old"):
		return "old"
	case strings.HasPrefix(normalized, "new_text"), strings.HasPrefix(normalized, "new"):
		return "new"
	case strings.HasPrefix(normalized, "replace_all"):
		return "replace_all"
	default:
		return ""
	}
}

func PlanExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return planExecuteUniqueRequiredPaths(requiredEditPaths)
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(requiredEditPaths))
	for _, path := range requiredEditPaths {
		path = normalizePlanExecutePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func PlanExecutePlannerSystemAddendum(requiredEditPaths []string) string {
	return planExecutePlannerSystemAddendum(requiredEditPaths, nil)
}

func PlanExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEdits(prompt, requiredEditPaths)
}

func InheritPlanExecutePromptPackID(plan *workspace.PlanArtifact, prompt string) string {
	return inheritPlanExecutePromptPackID(plan, prompt)
}

func ExtractPlanExecutePromptPackID(prompt string) string {
	return extractPlanExecutePromptPackID(prompt)
}

func TruncatePlanExecuteText(text string, maxBytes int) string {
	return truncatePlanExecuteText(text, maxBytes)
}

func CollectReviewRepairEvidence(root string, paths []string, baseline *ReviewRepairBaseline) ReviewRepairEvidence {
	return collectReviewRepairEvidence(root, paths, baseline)
}

func AutoRepairReviewPatchHygiene(root string, changedPaths []string, evidence ReviewRepairEvidence) (PatchHygieneAutoRepairReport, error) {
	return autoRepairReviewPatchHygiene(root, changedPaths, evidence)
}

func CollectPlanExecutePhaseEvidence(root string, phase string, plan workspace.PlanArtifact, changedPaths []string, requiredPaths []string, allowedPaths []string, reviewEvidence ReviewRepairEvidence, executorReport ExecutorPhaseReport) PhaseEvidenceBundle {
	return collectPlanExecutePhaseEvidence(root, phase, plan, changedPaths, requiredPaths, allowedPaths, reviewEvidence, executorReport)
}

func LoadPlanExecutePacketEvidence(root string, packID string) PhaseEvidencePacket {
	return loadPlanExecutePacketEvidence(root, packID)
}

func PlanExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence ReviewRepairEvidence) string {
	return planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
}

func ReviewAnswerPassContradictionReason(answer string) string {
	return reviewAnswerPassContradictionReason(answer)
}

func PhaseEvidenceTelemetry(bundle PhaseEvidenceBundle) map[string]any {
	return phaseEvidenceTelemetry(bundle)
}

func PreflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	return preflightPlanEdits(workspaceRoot, plan)
}

func PreflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	return preflightPlanUnsupportedProofClaims(plan)
}

func CleanupStalePlanArtifacts(root string, threshold time.Duration, logOut io.Writer) {
	cleanupStalePlanArtifacts(root, threshold, logOut)
}

func PlanChangedPaths(plan workspace.PlanArtifact) []string {
	return planChangedPaths(plan)
}

func planExecuteMissingEditPaths(required []string, edits []workspace.PlanEdit) []string {
	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}
	var missing []string
	for _, path := range required {
		if _, ok := covered[path]; !ok {
			missing = append(missing, path)
		}
	}
	return missing
}

func NewPlanExecuteSessionID() string {
	return newPlanExecuteSessionID()
}

func newPlanExecuteSessionID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func FindLatestPlanArtifact(root string, notBefore time.Time, sessionID string) (string, error) {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("plan dir %q: %w", dir, err)
	}
	type candidate struct {
		path  string
		mtime time.Time
	}
	sessionID = strings.TrimSpace(sessionID)
	var hits []candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "last.plan.json" {
			continue
		}
		if !strings.HasSuffix(name, ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !notBefore.IsZero() && info.ModTime().Before(notBefore) {
			continue
		}
		path := filepath.Join(dir, name)
		if sessionID != "" {
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var head struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(body, &head); err != nil {
				continue
			}
			if strings.TrimSpace(head.SessionID) != sessionID {
				continue
			}
		}
		hits = append(hits, candidate{path: path, mtime: info.ModTime()})
	}
	if len(hits) == 0 {
		if sessionID != "" {
			return "", fmt.Errorf("no plan artifacts under %s written since %s with session_id=%s", dir, notBefore.Format(time.RFC3339), sessionID)
		}
		return "", fmt.Errorf("no plan artifacts under %s written since %s", dir, notBefore.Format(time.RFC3339))
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mtime.After(hits[j].mtime) })
	return hits[0].path, nil
}

func classifyExecutorHandoffPacket(root string, plan workspace.PlanArtifact) ExecutorHandoffPacket {
	handoff := ExecutorHandoffPacket{
		State:                   ExecutorHandoffStateMissing,
		Reason:                  "missing_packet_id",
		PlanID:                  sanitizePlanExecuteEventID(plan.PlanID),
		HandoffID:               sanitizePlanExecuteEventID(plan.Handoff.ID),
		HandoffStatus:           sanitizePlanExecuteEventToken(plan.Handoff.Status),
		ApprovalScopeID:         sanitizePlanExecuteEventID(plan.Approval.ScopeID),
		ApprovalStatus:          sanitizePlanExecuteEventToken(plan.Approval.Status),
		ApprovalPermission:      sanitizePlanExecuteEventToken(plan.Approval.ExecutionPermission),
		ArtifactPacketStatus:    sanitizePlanExecuteEventToken(plan.Packet.Status),
		ArtifactPacketFreshness: sanitizePlanExecuteEventToken(plan.Packet.Freshness),
		IndexID:                 sanitizePlanExecuteEventToken(plan.Packet.IndexID),
		ApprovedPathCount:       len(plan.Approval.ApprovedPaths),
		PendingPathCount:        len(plan.Approval.PendingPaths),
	}
	topLevelPackID, topLevelValid := normalizePlanExecuteEventPackID(plan.PackID)
	declaredPackID, declaredValid := normalizePlanExecuteEventPackID(plan.Packet.PackID)
	rawTopLevelPackID := strings.TrimSpace(plan.PackID)
	rawDeclaredPackID := strings.TrimSpace(plan.Packet.PackID)
	if rawTopLevelPackID != "" && !topLevelValid {
		handoff.Reason = "invalid_pack_id"
		return handoff
	}
	if rawDeclaredPackID != "" && !declaredValid {
		handoff.Reason = "invalid_declared_packet_id"
		if topLevelPackID == "" {
			return handoff
		}
	}
	effectivePackID := firstNonEmptyString(topLevelPackID, declaredPackID)
	handoff.PackID = effectivePackID
	handoff.DeclaredPackID = declaredPackID
	if effectivePackID == "" {
		return handoff
	}
	overridden := topLevelPackID != "" && declaredPackID != "" && topLevelPackID != declaredPackID
	if overridden || (topLevelPackID != "" && rawDeclaredPackID != "" && !declaredValid) {
		handoff.State = ExecutorHandoffStateOverridden
		handoff.Reason = "packet_id_override"
	}
	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, effectivePackID)
	if err != nil {
		if handoff.State != ExecutorHandoffStateOverridden {
			handoff.State = ExecutorHandoffStateMissing
			handoff.Reason = "cache_load_error"
		}
		return handoff
	}
	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = ExecutorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else if view.Fresh {
			handoff.State = ExecutorHandoffStateInherited
			handoff.Reason = "fresh_graph_view"
		} else {
			handoff.State = ExecutorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}
		return handoff
	case comprehension.ViewLoadStateStale:
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = ExecutorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
		} else {
			handoff.State = ExecutorHandoffStateStale
			handoff.Reason = "stale_graph_view"
		}
		return handoff
	}
	view, artifactHit, artifactErr := loadPlanExecutePacketArtifact(root, effectivePackID)
	if artifactErr != nil {
		if handoff.State != ExecutorHandoffStateOverridden {
			handoff.State = ExecutorHandoffStateMissing
			handoff.Reason = "artifact_load_error"
		}
		return handoff
	}
	if artifactHit {
		handoff = applyExecutorHandoffView(handoff, view)
		if overridden {
			handoff.State = ExecutorHandoffStateOverridden
			handoff.Reason = "packet_id_override"
			return handoff
		}
		handoff.State = ExecutorHandoffStateStale
		handoff.Reason = "legacy_artifact_fallback"
		handoff.Fresh = false
		return handoff
	}
	if handoff.State != ExecutorHandoffStateOverridden {
		handoff.State = ExecutorHandoffStateMissing
		handoff.Reason = "cache_miss"
	}
	return handoff
}

func applyExecutorHandoffView(handoff ExecutorHandoffPacket, view comprehension.PackResult) ExecutorHandoffPacket {
	handoff.PackID = firstNonEmptyString(normalizePlanExecutePackID(view.PackID), handoff.PackID)
	handoff.IndexID = sanitizePlanExecuteEventToken(firstNonEmptyString(view.IndexID, handoff.IndexID))
	handoff.Fresh = view.Fresh
	handoff.CandidateFiles = len(view.CandidateFiles)
	handoff.CandidateSymbols = len(view.CandidateSymbols)
	handoff.Tests = len(view.Tests)
	return handoff
}

func emitExecutorHandoffPacketEvents(stdout io.Writer, jsonEvents bool, handoff ExecutorHandoffPacket) {
	fields := executorHandoffPacketEventFields(handoff)
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_handoff_packet", fields)
	switch handoff.State {
	case ExecutorHandoffStateInherited:
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_inherits_pack", fields)
		if !jsonEvents && handoff.PackID != "" {
			fmt.Fprintf(stdout, "  executor inherits planner pack %s (%d files, %d symbols, %d tests)\n",
				handoff.PackID, handoff.CandidateFiles, handoff.CandidateSymbols, handoff.Tests)
		}
	case ExecutorHandoffStateMissing:
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_pack_miss", fields)
	case ExecutorHandoffStateStale:
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_packet_stale", fields)
	case ExecutorHandoffStateOverridden:
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_packet_overridden", fields)
	}
}

func executorHandoffPacketEventFields(handoff ExecutorHandoffPacket) map[string]any {
	fields := map[string]any{
		"packet_state": handoff.State,
		"reason":       handoff.Reason,
		"fresh":        handoff.Fresh,
	}
	addPlanExecuteEventString(fields, "plan_id", handoff.PlanID)
	addPlanExecuteEventString(fields, "handoff_id", handoff.HandoffID)
	addPlanExecuteEventString(fields, "handoff_status", handoff.HandoffStatus)
	addPlanExecuteEventString(fields, "approval_scope_id", handoff.ApprovalScopeID)
	addPlanExecuteEventString(fields, "approval_status", handoff.ApprovalStatus)
	addPlanExecuteEventString(fields, "approval_execution_permission", handoff.ApprovalPermission)
	addPlanExecuteEventString(fields, "pack_id", handoff.PackID)
	addPlanExecuteEventString(fields, "declared_pack_id", handoff.DeclaredPackID)
	addPlanExecuteEventString(fields, "artifact_packet_status", handoff.ArtifactPacketStatus)
	addPlanExecuteEventString(fields, "artifact_packet_freshness", handoff.ArtifactPacketFreshness)
	addPlanExecuteEventString(fields, "index_id", handoff.IndexID)
	if handoff.ApprovedPathCount > 0 {
		fields["approved_path_count"] = handoff.ApprovedPathCount
	}
	if handoff.PendingPathCount > 0 {
		fields["pending_path_count"] = handoff.PendingPathCount
	}
	if handoff.CandidateFiles > 0 {
		fields["candidate_files"] = handoff.CandidateFiles
	}
	if handoff.CandidateSymbols > 0 {
		fields["candidate_symbols"] = handoff.CandidateSymbols
	}
	if handoff.Tests > 0 {
		fields["tests"] = handoff.Tests
	}
	return fields
}

func addPlanExecuteEventString(fields map[string]any, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		fields[key] = value
	}
}

func normalizePlanExecuteEventPackID(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	normalized := normalizePlanExecutePackID(raw)
	if normalized == "" {
		return "", raw == ""
	}
	return normalized, true
}

func normalizePlanExecutePackID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if isPlanExecutePackID(value) {
		return value
	}
	return ""
}

func sanitizePlanExecuteEventID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planExecuteEventTextLooksSensitive(value) {
		return "redacted:" + planExecuteEventHash(value)
	}
	return sanitizePlanExecuteEventToken(value)
}

func sanitizePlanExecuteEventToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || planExecuteEventTextLooksSensitive(value) {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '_' || r == '.' || r == ':':
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

func sanitizePlanExecuteEventPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if planExecuteEventTextLooksSensitive(value) {
		return "redacted:" + planExecuteEventHash(value)
	}
	value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return value
}

func planExecuteEventTextLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"raw_prompt", "raw prompt", "raw_body", "raw body", "raw_content", "raw content",
		"prompt_body", "prompt body", "transcript", "file_body", "file body", "file-body",
		"private key", "secret", "token=", "token:", "access_token", "api token",
		"secret=", "api_key", "api-key", "password=", "bearer ", "customer-",
		"customer_", "secrets/", "/secrets/", "\\secrets\\",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if token == "token" || token == "tokens" || token == "customer" || token == "customers" {
			return true
		}
	}
	normalized := strings.ReplaceAll(lower, "\\", "/")
	for _, marker := range []string{"/users/", "/home/", "/volumes/", "/private/", "c:/users/"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func planExecuteEventHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func RunReviewRepairPhase(
	cfg config.Config,
	originalPrompt string,
	plan workspace.PlanArtifact,
	requiredEditPaths []string,
	baseline *ReviewRepairBaseline,
	executorReport ExecutorPhaseReport,
	opts Options,
	stdout io.Writer,
	promptRun PromptRunner,
	rec *telemetry.Recorder,
) (ReviewRepairPhaseReport, error) {
	changedPaths := planChangedPaths(plan)
	if len(changedPaths) == 0 {
		return ReviewRepairPhaseReport{Status: "skipped (no changed paths)"}, nil
	}
	allowedRepairPaths := mergePlanExecutePaths(changedPaths, requiredEditPaths)
	evidence := collectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	autoRepair, autoRepairErr := autoRepairReviewPatchHygiene(cfg.Workspace.Root, changedPaths, evidence)
	if autoRepairErr != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_auto_patch_hygiene_failed", map[string]any{
			"reason": autoRepairErr.Error(),
		})
		return ReviewRepairPhaseReport{Status: "failed"}, fmt.Errorf("review-repair auto patch hygiene failed: %w", autoRepairErr)
	}
	if autoRepair.Repaired {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_auto_patch_hygiene", map[string]any{
			"paths": autoRepair.Paths,
			"lines": autoRepair.Lines,
		})
		evidence = collectReviewRepairEvidence(cfg.Workspace.Root, changedPaths, baseline)
	}
	evidence.PhaseEvidence = collectPlanExecutePhaseEvidence(cfg.Workspace.Root, "review_repair", plan, changedPaths, requiredEditPaths, allowedRepairPaths, evidence, executorReport)
	reviewSessionID := newPlanExecuteSessionID()
	reviewCfg := cfg
	reviewCfg.Workspace.SessionID = reviewSessionID
	reviewStart := time.Now().Add(-1 * time.Second)
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_start", map[string]any{
		"session_id":     reviewSessionID,
		"changed_paths":  changedPaths,
		"allowed_paths":  allowedRepairPaths,
		"phase_evidence": phaseEvidenceTelemetry(evidence.PhaseEvidence),
	})
	prompt := planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
	reviewMaxTokens := opts.MaxTokens
	if reviewMaxTokens <= 0 {
		reviewMaxTokens = 8192
	}
	answer, err := promptRun(reviewCfg, prompt, RunOptions{
		JSONEvents:               opts.JSONEvents,
		TaskFamily:               agent.PlanThenExecuteReviewRepairTaskFamily,
		PlannerOwnedVerification: true,
		MaxTokensOverride:        reviewMaxTokens,
		DisableThinking:          opts.DisableThinking,
		Telemetry:                rec,
	}, stdout)
	if err != nil {
		reasonClass := reviewRepairDegradedReasonClass(err)
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_degraded", map[string]any{
			"status":        "review_unavailable",
			"reason_class":  reasonClass,
			"changed_paths": changedPaths,
			"allowed_paths": allowedRepairPaths,
		})
		return ReviewRepairPhaseReport{
			Status:      "degraded (review unavailable)",
			AutoRepairs: autoRepair.Lines,
			ReasonClass: reasonClass,
		}, nil
	}
	planPath, found, err := findOptionalReviewPlanArtifact(cfg.Workspace.Root, reviewStart, reviewSessionID)
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_failed", map[string]any{
			"reason": err.Error(),
		})
		return ReviewRepairPhaseReport{Status: "failed"}, err
	}
	if !found {
		if reviewAnswerDeclaresPass(answer) && reviewTextHasVerificationChecklist(answer) {
			if reason := reviewAnswerPassContradictionReason(answer); reason != "" {
				emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_pass_rejected", map[string]any{
					"reason": reason,
				})
				return ReviewRepairPhaseReport{Status: "failed", ChecklistFound: true}, fmt.Errorf("review-repair phase rejected contradictory `Decision: pass`: %s", reason)
			}
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":  "pass",
				"repairs": 0,
			})
			return ReviewRepairPhaseReport{Status: "pass (no corrective edits)", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}
		if reviewAnswerDeclaresBlocker(answer) {
			return ReviewRepairPhaseReport{Status: "blocked"}, fmt.Errorf("review-repair phase reported a blocker without a corrective plan")
		}
		return ReviewRepairPhaseReport{Status: "inconclusive"}, fmt.Errorf("review-repair phase produced no corrective plan and no `Decision: pass` with `Verification Checklist`")
	}
	repairPlan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		return ReviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("read review-repair plan artifact %q: %w", planPath, err)
	}
	if !reviewTextHasVerificationChecklist(repairPlan.Rationale) {
		return ReviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("review-repair plan %q is missing `Verification Checklist` in submit_plan rationale", planPath)
	}
	repairReport, err := RunExecutorPhaseScoped(cfg, repairPlan, nil, allowedRepairPaths, opts.JSONEvents, stdout)
	if err != nil {
		return ReviewRepairPhaseReport{
			Status:         "failed",
			Repairs:        repairReport.Succeeded,
			AutoRepairs:    autoRepair.Lines,
			ReviewPlan:     planPath,
			ChecklistFound: true,
		}, fmt.Errorf("review-repair executor failed: %w", err)
	}
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
		"status":        "repaired",
		"repairs":       repairReport.Succeeded,
		"review_plan":   planPath,
		"changed_paths": allowedRepairPaths,
	})
	return ReviewRepairPhaseReport{
		Status:         "repaired",
		Repairs:        repairReport.Succeeded,
		AutoRepairs:    autoRepair.Lines,
		ReviewPlan:     planPath,
		ChecklistFound: true,
	}, nil
}

func reviewRepairDegradedReasonClass(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case reason == "":
		return "review_model_error"
	case strings.Contains(reason, "eof"):
		return "backend_eof"
	case strings.Contains(reason, "connection refused"), strings.Contains(reason, "connect: connection"):
		return "backend_unreachable"
	case strings.Contains(reason, "context deadline"), strings.Contains(reason, "timeout"), strings.Contains(reason, "deadline exceeded"):
		return "backend_timeout"
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "cancelled"):
		return "backend_canceled"
	default:
		return "review_model_error"
	}
}

func findOptionalReviewPlanArtifact(root string, notBefore time.Time, sessionID string) (string, bool, error) {
	path, err := FindLatestPlanArtifact(root, notBefore, sessionID)
	if err == nil {
		return path, true, nil
	}
	text := err.Error()
	if strings.Contains(text, "no plan artifacts under") || strings.Contains(text, "plan dir") {
		return "", false, nil
	}
	return "", false, err
}

func planExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence ReviewRepairEvidence) string {
	var b strings.Builder
	b.WriteString("# Plan-then-execute review-repair phase\n\n")
	b.WriteString("You are the REVIEW-REPAIR planner for a plan-execute run that already applied executor edits. Review the actual diff and behavioral contract, then either submit a narrow corrective plan or explicitly pass.\n\n")
	b.WriteString("## Required Review Contract\n\n")
	b.WriteString("Create a `Verification Checklist` before deciding. This checklist must verify behavior, not just literal code correctness. Cover:\n\n")
	b.WriteString("- Required behavior from the original task/spec.\n")
	b.WriteString("- Default/non-regression behavior that should remain equivalent.\n")
	b.WriteString("- Data, lifecycle, state, persistence, concurrency/threading, and performance invariants when relevant.\n")
	b.WriteString("- Edge cases and error/empty/default paths implied by the change.\n")
	b.WriteString("- Scope safety: changed paths, out-of-scope writes, duplicate/bespoke logic, and stale-artifact risk.\n")
	b.WriteString("- Test or command evidence available now, plus any planner-owned verification that still must run outside this lane.\n\n")
	b.WriteString("Outcomes:\n\n")
	b.WriteString("- If any checklist item fails and the repair is inside Allowed Repair Paths, call `submit_plan` once with corrective edits only. The `submit_plan.rationale` must start with `Verification Checklist:` and include the checklist plus the failing item each edit addresses.\n")
	b.WriteString("- The submitted plan is pre-executor intent. If `submitted_plan_state` says those edits are already applied, do not re-submit them; submit only new corrective edits for defects found by the checklist.\n")
	b.WriteString("- If the diff satisfies the checklist, do not call `submit_plan`; answer with `Decision: pass` and include the `Verification Checklist`. Empty corrective plans are invalid.\n")
	b.WriteString("- If the issue is ambiguous or outside Allowed Repair Paths, stop with `<<QUESTION>>:` and name the blocker. Do not improvise out-of-scope writes.\n\n")
	b.WriteString("Planner-owned build/test/scenario acceptance commands are pending outside this lane unless Phase Evidence names the exact command as successful. Do not mark them checked/pass or say they ran.\n\n")
	b.WriteString("Patch hygiene and source_sanity may be marked checked only when Phase Evidence shows those results. Unsupported or absent source_sanity is not a failure for non-source files.\n\n")
	b.WriteString("Do not call `ask` or external question tools in this phase. If blocked, return `<<QUESTION>>:` with the blocker.\n\n")
	b.WriteString("A repair that replaces missing behavioral evidence with a tautology (`assert true`, `CHECK(true)`, `_assert_true(true)`) or with comments admitting behavior is not inspected is not a valid repair. Submit a concrete state assertion or stop with `<<QUESTION>>:`.\n\n")
	b.WriteString("## Allowed Repair Paths\n\n")
	for _, path := range allowedRepairPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}
	b.WriteString("\n## Changed Paths\n\n")
	for _, path := range changedPaths {
		fmt.Fprintf(&b, "- `%s`\n", path)
	}
	b.WriteString("\n## Submitted Plan\n\n")
	if strings.TrimSpace(plan.Rationale) != "" {
		b.WriteString("Rationale:\n")
		b.WriteString(truncatePlanExecuteText(plan.Rationale, 4000))
		b.WriteString("\n\n")
	}
	for i, edit := range plan.Edits {
		fmt.Fprintf(&b, "%d. `%s`: %s\n", i+1, edit.Path, firstNonEmptyString(edit.Purpose, "planned edit"))
	}
	b.WriteString("\n## Phase Evidence Bundle\n\n")
	b.WriteString("Use this bundle as the current evidence ledger for the review decision. Treat open obligations as items that must be resolved by the checklist, a narrow repair, or a blocker.\n\n")
	b.WriteString("```text\n")
	b.WriteString(renderPlanExecutePhaseEvidence(evidence.PhaseEvidence))
	b.WriteString("```\n")
	b.WriteString("\n## Diff Evidence\n\n")
	if strings.TrimSpace(evidence.Diff) == "" {
		b.WriteString("(No git diff available or changed paths are not tracked by git.)\n")
	} else {
		b.WriteString("```diff\n")
		b.WriteString(truncatePlanExecuteText(evidence.Diff, 60000))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Patch Hygiene Evidence\n\n")
	fmt.Fprintf(&b, "current-run whitespace check exit: %d\n\n", evidence.DiffCheckExit)
	if strings.TrimSpace(evidence.DiffCheck) != "" {
		b.WriteString("```text\n")
		b.WriteString(truncatePlanExecuteText(evidence.DiffCheck, 12000))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Original Task / Spec\n\n")
	b.WriteString("Reference only. The original spec is quoted so its file allowlist does not become this review pass's active write contract.\n\n")
	b.WriteString(quotePlanExecuteReference(truncatePlanExecuteText(originalPrompt, 40000)))
	return b.String()
}

func quotePlanExecuteReference(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "> (empty)\n"
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func collectReviewRepairEvidence(root string, paths []string, baseline *ReviewRepairBaseline) ReviewRepairEvidence {
	if baseline != nil {
		diff := collectReviewRepairDiffFromBaseline(root, paths, *baseline)
		check, exit := reviewRepairWhitespaceCheck(diff)
		return ReviewRepairEvidence{Diff: diff, DiffCheck: check, DiffCheckExit: exit}
	}
	return ReviewRepairEvidence{
		Diff:          runGitEvidence(root, append([]string{"diff", "--"}, paths...)...),
		DiffCheck:     runGitEvidence(root, append([]string{"diff", "--check", "--"}, paths...)...),
		DiffCheckExit: runGitEvidenceExit(root, append([]string{"diff", "--check", "--"}, paths...)...),
	}
}

func autoRepairReviewPatchHygiene(root string, changedPaths []string, evidence ReviewRepairEvidence) (PatchHygieneAutoRepairReport, error) {
	if evidence.DiffCheckExit != 1 {
		return PatchHygieneAutoRepairReport{}, nil
	}
	trailingWhitespace := reviewRepairTrailingWhitespaceLines(evidence.Diff)
	if len(trailingWhitespace) == 0 {
		return PatchHygieneAutoRepairReport{}, nil
	}
	changedSet := planExecutePathSet(changedPaths)
	paths := make([]string, 0, len(trailingWhitespace))
	for path := range trailingWhitespace {
		normalized := normalizePlanExecutePath(path)
		if _, ok := changedSet[normalized]; !ok {
			return PatchHygieneAutoRepairReport{}, nil
		}
		paths = append(paths, normalized)
	}
	slices.Sort(paths)

	report := PatchHygieneAutoRepairReport{Repaired: true, Paths: paths}
	for _, path := range paths {
		fullPath, err := planExecuteWorkspaceFile(root, path)
		if err != nil {
			return PatchHygieneAutoRepairReport{}, err
		}
		body, err := os.ReadFile(fullPath)
		if err != nil {
			return PatchHygieneAutoRepairReport{}, fmt.Errorf("read %s for patch hygiene repair: %w", path, err)
		}
		repaired, lines := trimTrailingWhitespaceOnLines(string(body), trailingWhitespace[path])
		if lines == 0 {
			continue
		}
		if err := os.WriteFile(fullPath, []byte(repaired), 0o644); err != nil {
			return PatchHygieneAutoRepairReport{}, fmt.Errorf("write %s for patch hygiene repair: %w", path, err)
		}
		report.Lines += lines
	}
	if report.Lines == 0 {
		return PatchHygieneAutoRepairReport{}, nil
	}
	return report, nil
}

func CaptureReviewRepairBaseline(root string, paths []string) ReviewRepairBaseline {
	baseline := ReviewRepairBaseline{Files: map[string]ReviewRepairFileSnapshot{}}
	for _, path := range mergePlanExecutePaths(paths) {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			baseline.Files[path] = ReviewRepairFileSnapshot{Exists: false}
			continue
		}
		baseline.Files[path] = ReviewRepairFileSnapshot{Before: string(data), Exists: true}
	}
	return baseline
}

func collectReviewRepairDiffFromBaseline(root string, paths []string, baseline ReviewRepairBaseline) string {
	var b strings.Builder
	for _, path := range mergePlanExecutePaths(paths) {
		snapshot, ok := baseline.Files[path]
		if !ok {
			snapshot = ReviewRepairFileSnapshot{Exists: false}
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, path)
		}
		var after string
		afterData, err := os.ReadFile(filepath.Clean(full))
		afterExists := err == nil
		if err == nil {
			after = string(afterData)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(&b, "# %s: current content unavailable: %v\n", path, err)
			continue
		}
		if !snapshot.Exists && !afterExists {
			continue
		}
		diff := UnifiedDiff(path, snapshot.Before, after)
		if strings.TrimSpace(diff) == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func reviewRepairWhitespaceCheck(diff string) (string, int) {
	findings := reviewRepairTrailingWhitespaceFindings(diff)
	if len(findings) == 0 {
		return "", 0
	}
	return strings.Join(findings, "\n"), 1
}

func reviewRepairTrailingWhitespaceFindings(diff string) []string {
	var findings []string
	path := ""
	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			inHunk = false
			newLine = 0
			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true
			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}
		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			content := line[1:]
			if strings.TrimRight(content, " \t") != content {
				loc := path
				if newLine > 0 {
					loc = fmt.Sprintf("%s:%d", firstNonEmptyString(path, "(unknown)"), newLine)
				}
				findings = append(findings, fmt.Sprintf("%s: trailing whitespace.\n%s", loc, line))
			}
			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
		}
	}
	return findings
}

func reviewRepairTrailingWhitespaceLines(diff string) map[string]map[int]struct{} {
	out := make(map[string]map[int]struct{})
	path := ""
	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = normalizePlanExecutePath(strings.TrimSpace(strings.TrimPrefix(line, "+++ b/")))
			inHunk = false
			newLine = 0
			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true
			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0
			continue
		}
		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}
		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			content := line[1:]
			if path != "" && newLine > 0 && strings.TrimRight(content, " \t") != content {
				if _, ok := out[path]; !ok {
					out[path] = make(map[int]struct{})
				}
				out[path][newLine] = struct{}{}
			}
			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
		}
	}
	return out
}

func trimTrailingWhitespaceOnLines(text string, targetLines map[int]struct{}) (string, int) {
	if len(targetLines) == 0 {
		return text, 0
	}
	var b strings.Builder
	b.Grow(len(text))
	changed := 0
	lineNo := 1
	for start := 0; start < len(text); lineNo++ {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}
		line := text[start:end]
		newline := ""
		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}
		if _, ok := targetLines[lineNo]; ok {
			repaired := trimTrailingWhitespaceBeforeLineEnding(line)
			if repaired != line {
				changed++
				line = repaired
			}
		}
		b.WriteString(line)
		b.WriteString(newline)
		start = end
	}
	return b.String(), changed
}

func trimTrailingWhitespaceEveryLine(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for start := 0; start < len(text); {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}
		line := text[start:end]
		newline := ""
		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}
		b.WriteString(trimTrailingWhitespaceBeforeLineEnding(line))
		b.WriteString(newline)
		start = end
	}
	return b.String()
}

func trimTrailingWhitespaceBeforeLineEnding(line string) string {
	if strings.HasSuffix(line, "\r") {
		return strings.TrimRight(line[:len(line)-1], " \t") + "\r"
	}
	return strings.TrimRight(line, " \t")
}

func planExecuteWorkspaceFile(root string, path string) (string, error) {
	normalized := normalizePlanExecutePath(path)
	if normalized == "" {
		return "", fmt.Errorf("empty workspace path")
	}
	if filepath.IsAbs(normalized) {
		return "", fmt.Errorf("workspace path must be relative: %s", normalized)
	}
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	fullPath := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(normalized)))
	rel, err := filepath.Rel(cleanRoot, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) {
		return "", fmt.Errorf("workspace path escapes root: %s", normalized)
	}
	return fullPath, nil
}

func parseReviewRepairHunkNewStart(line string) int {
	plus := strings.Index(line, " +")
	if plus < 0 {
		return 0
	}
	rest := line[plus+2:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return value
}

func runGitEvidence(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

func runGitEvidenceExit(root string, args ...string) int {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 127
	}
	return 0
}

func planChangedPaths(plan workspace.PlanArtifact) []string {
	paths := make([]string, 0, len(plan.Edits))
	for _, edit := range plan.Edits {
		paths = append(paths, edit.Path)
	}
	return mergePlanExecutePaths(paths)
}

func mergePlanExecutePaths(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range groups {
		for _, path := range group {
			normalized := normalizePlanExecutePath(path)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

func reviewAnswerDeclaresPass(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(lower, "decision: pass")
}

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func planExecuteAnswerDeclaresBlocker(answer string) bool {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") ||
		strings.Contains(lower, "decision: block") ||
		strings.HasPrefix(lower, "blocker:") ||
		strings.Contains(lower, "\nblocker:")
}

func planExecuteBlockerSummary(answer string) string {
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		const max = 240
		if len(line) > max {
			return line[:max] + "..."
		}
		return line
	}
	return "planner stopped with a blocker"
}

func reviewTextHasVerificationChecklist(text string) bool {
	return strings.Contains(strings.ToLower(text), "verification checklist")
}

func reviewAnswerPassContradictionReason(answer string) string {
	for _, rawLine := range strings.Split(answer, "\n") {
		line := normalizeReviewAnswerLine(rawLine)
		if line == "" || strings.Contains(line, "decision: pass") {
			continue
		}
		if reviewLineHasPassNegation(line) {
			continue
		}
		switch {
		case strings.Contains(line, "❌"), strings.Contains(line, "[ ]"):
			return "failed_checklist_item"
		case reviewLineContainsAny(line,
			"appears to be missing",
			"is missing",
			"are missing",
			"missing from",
			"missing the",
			"not present",
			"not implemented",
			"does not implement",
			"doesn't implement",
			"does not satisfy",
			"doesn't satisfy",
			"fails the checklist",
			"checklist item fails",
			"required behavior fails",
			"not visible",
			"not shown",
			"not available in the diff",
			"not visible in the diff",
			"cannot verify",
			"can't verify",
			"could not verify",
			"unable to verify",
			"not enough evidence",
			"insufficient evidence",
			"no evidence",
		):
			return "unresolved_failure_claim"
		case reviewLineContainsProjectedVerificationClaim(line):
			return "unsupported_verification_projection"
		}
	}
	return ""
}

func normalizeReviewAnswerLine(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.TrimLeft(line, "-*0123456789. )\t")
	return strings.Join(strings.Fields(line), " ")
}

func reviewLineHasPassNegation(line string) bool {
	return reviewLineContainsAny(line,
		"no missing",
		"not missing",
		"nothing missing",
		"without missing",
		"no issues",
		"no issue",
		"no failures",
		"no failure",
		"not failed",
		"not a failure",
		"no failed",
		"no unresolved",
		"no blocker",
		"no blockers",
	)
}

func reviewLineContainsProjectedVerificationClaim(line string) bool {
	if !reviewLineContainsAny(line, "should pass", "should still pass", "should pass with", "would pass") {
		return false
	}
	return reviewLineContainsAny(line,
		"test",
		"tests",
		"scenario",
		"scenarios",
		"command",
		"verification",
		"acceptance",
		"git diff",
		"diff --check",
		"go test",
		"pytest",
		"run_scenarios",
	)
}

func reviewLineContainsAny(line string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func truncatePlanExecuteText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	for i := range text {
		if i >= maxBytes {
			return text[:i] + fmt.Sprintf("\n\n[truncated %d bytes]\n", len(text)-i)
		}
	}
	return text
}

func RunExecutorPhase(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, jsonEvents bool, stdout io.Writer) (ExecutorPhaseReport, error) {
	return RunExecutorPhaseScoped(cfg, plan, requiredEditPaths, nil, jsonEvents, stdout)
}

func RunExecutorPhaseScoped(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	plan = workspace.NormalizeLoadedPlanArtifact(plan)
	report := ExecutorPhaseReport{Total: len(plan.Edits)}
	if len(plan.Edits) == 0 {
		return report, fmt.Errorf("plan has no edits")
	}
	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	ctx := context.Background()
	handoff := classifyExecutorHandoffPacket(cfg.Workspace.Root, plan)
	report.Handoff = handoff
	emitExecutorHandoffPacketEvents(stdout, jsonEvents, handoff)

	if mismatch := preflightPlanApprovalAndScope(plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "plan_scope_drift", planScopeDriftEventFields(handoff, mismatch))
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index":        mismatch.Index,
			"path":              sanitizePlanExecuteEventPath(mismatch.Path),
			"reason":            mismatch.Reason,
			"handoff_id":        handoff.HandoffID,
			"approval_scope_id": handoff.ApprovalScopeID,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on approval/scope %s: %s\n", mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan approval/scope preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}
	if mismatch := preflightPlanRequiredEditPaths(requiredEditPaths, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on required_edit_paths %s: %s\n", mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan required_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
	}
	if len(requiredEditPaths) == 0 {
		if mismatch := preflightPlanAllowedEditPaths(allowedEditPaths, plan); mismatch != nil {
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
				"edit_index": mismatch.Index,
				"path":       mismatch.Path,
				"reason":     mismatch.Reason,
			})
			if !jsonEvents {
				fmt.Fprintf(stdout, "  preflight FAILED on allowed_edit_paths %s: %s\n", mismatch.Path, mismatch.Reason)
			}
			return report, fmt.Errorf("plan allowed_edit_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
		}
	}

	if mismatch := preflightPlanEdits(cfg.Workspace.Root, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on edit %d/%d %s: %s\n", mismatch.Index+1, len(plan.Edits), mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}
	if mismatch := preflightPlanUnsupportedProofClaims(plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on edit %d/%d %s: %s\n", mismatch.Index+1, len(plan.Edits), mismatch.Path, mismatch.Reason)
		}
		return report, fmt.Errorf("plan preflight failed on edit %d (%s): %s", mismatch.Index, mismatch.Path, mismatch.Reason)
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_ok", map[string]any{
		"edits": len(plan.Edits),
	})

	executorStartedAt := time.Now()
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_start", map[string]any{
		"edits":        len(plan.Edits),
		"packet_state": handoff.State,
	})
	for i, edit := range plan.Edits {
		isWholeFile := edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != ""
		var req workspace.ToolRequest
		if isWholeFile {
			req = workspace.ToolRequest{
				Tool:        "write_file",
				Path:        edit.Path,
				Content:     edit.NewText,
				Description: firstNonEmptyString(edit.Purpose, "executor phase: whole-file write"),
			}
		} else {
			req = workspace.ToolRequest{
				Tool:        "edit_file",
				Path:        edit.Path,
				OldText:     edit.OldText,
				NewText:     edit.NewText,
				StartLine:   edit.StartLine,
				EndLine:     edit.EndLine,
				Content:     edit.Content,
				ReplaceAll:  edit.ReplaceAll,
				Description: firstNonEmptyString(edit.Purpose, "executor phase: planned edit"),
			}
		}
		result := mgr.Execute(ctx, req)
		status := "ok"
		if strings.TrimSpace(result.Error) != "" {
			status = "failed"
		}
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_edit", map[string]any{
			"step":   i + 1,
			"total":  len(plan.Edits),
			"path":   edit.Path,
			"status": status,
			"error":  strings.TrimSpace(result.Error),
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  edit %d/%d %s ... %s\n", i+1, len(plan.Edits), edit.Path, executorEditOutcome(result))
		}
		if len(progress) > 0 && progress[0] != nil {
			progress[0](i+1, len(plan.Edits), edit.Path, status == "ok")
		}
		if status == "failed" {
			report.Failed++
			report.Failures = append(report.Failures, fmt.Sprintf("edits[%d] %s: %s", i, edit.Path, result.Error))
			emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
				"total":       report.Total,
				"succeeded":   report.Succeeded,
				"failed":      report.Failed,
				"duration_ms": time.Since(executorStartedAt).Milliseconds(),
			})
			return report, fmt.Errorf("executor phase aborted on edit %d (%s): %s", i, edit.Path, result.Error)
		}
		report.Succeeded++
	}
	sourceSanityChecked, sourceSanityEvidence, err := runPlanExecuteSourceSanity(ctx, mgr, cfg.Workspace.Root, planChangedPaths(plan), jsonEvents, stdout)
	report.SourceSanity = sourceSanityEvidence
	if err != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
			"total":                 report.Total,
			"succeeded":             report.Succeeded,
			"failed":                report.Failed,
			"source_sanity_checked": sourceSanityChecked,
			"source_sanity_status":  "failed",
			"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
		})
		return report, err
	}
	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
		"total":                 report.Total,
		"succeeded":             report.Succeeded,
		"failed":                report.Failed,
		"source_sanity_checked": sourceSanityChecked,
		"source_sanity_status":  "passed",
		"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
	})
	return report, nil
}

func runPlanExecuteSourceSanity(ctx context.Context, mgr *workspace.Manager, root string, paths []string, jsonEvents bool, stdout io.Writer) (int, []PhaseEvidenceCheck, error) {
	checked := 0
	evidence := make([]PhaseEvidenceCheck, 0)
	for _, path := range mergePlanExecutePaths(paths) {
		if !workspace.SourceSanityCoversPath(path) {
			continue
		}
		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, path)
		}
		info, err := os.Stat(filepath.Clean(fullPath))
		if err != nil || info.IsDir() {
			continue
		}
		checked++
		result := mgr.Execute(ctx, workspace.ToolRequest{Tool: "source_sanity", Path: path})
		status := "ok"
		errText := strings.TrimSpace(result.Error)
		if errText != "" {
			status = "failed"
		}
		evidence = append(evidence, PhaseEvidenceCheck{
			Kind:    "source_sanity",
			Path:    path,
			Status:  status,
			Summary: strings.TrimSpace(result.Summary),
			Error:   errText,
		})
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "source_sanity", map[string]any{
			"path":    path,
			"status":  status,
			"summary": strings.TrimSpace(result.Summary),
			"error":   errText,
		})
		if !jsonEvents {
			fmt.Fprintf(stdout, "  source sanity %s ... %s\n", path, status)
		}
		if errText != "" {
			return checked, evidence, fmt.Errorf("source_sanity failed for %s: %s", path, errText)
		}
	}
	return checked, evidence, nil
}

func preflightPlanApprovalAndScope(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	if strings.TrimSpace(plan.Handoff.ID) == "" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff id is missing"}
	}
	if !plan.Handoff.Ready {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff is not ready"}
	}
	if len(plan.Handoff.Blockers) > 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "handoff has unresolved blockers"}
	}
	if plan.Approval.Required {
		if planExecuteApprovalPartial(plan.Approval.Status) {
			if mismatch := preflightPlanPartialApprovalScope(plan); mismatch != nil {
				return mismatch
			}
		} else if !planExecuteApprovalSatisfied(plan.Approval.Status) {
			return &PlanEditPreflightMismatch{Index: -1, Reason: "required approval is not satisfied"}
		}
	}
	if plan.Approval.ExecutionPermission == "blocked" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "approval execution permission is blocked"}
	}
	return preflightPlanDeclaredScope(plan)
}

func preflightPlanPartialApprovalScope(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	if strings.TrimSpace(plan.Approval.ScopeID) == "" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval scope id is missing"}
	}
	if len(plan.Approval.ApprovedPaths) == 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval has no approved scope paths"}
	}
	if len(plan.Approval.PendingPaths) == 0 {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval has no pending scope paths"}
	}
	if plan.Approval.ExecutionPermission != "execute_approved_scope" {
		return &PlanEditPreflightMismatch{Index: -1, Reason: "partial approval must execute only approved scope"}
	}
	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	pending := planExecutePathSet(plan.Approval.PendingPaths)
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		if _, ok := pending[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is still pending approval"}
		}
		if _, ok := approved[path]; !ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside approved scope"}
		}
	}
	return nil
}

func preflightPlanDeclaredScope(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	forbidden := planExecutePathSet(append(append([]string(nil), plan.Paths.Forbidden...), plan.Contract.ForbiddenPaths...))
	required := planExecutePathSet(append(append([]string(nil), plan.Paths.Required...), plan.Contract.RequiredEditPaths...))
	planned := planExecutePathSet(append(append([]string(nil), plan.Paths.Editable...), plan.Paths.Changed...))
	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	pending := planExecutePathSet(plan.Approval.PendingPaths)
	seen := map[string]struct{}{}
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		seen[path] = struct{}{}
		if _, ok := forbidden[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is forbidden"}
		}
		if _, ok := pending[path]; ok {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is still pending approval"}
		}
		if len(approved) > 0 {
			if _, ok := approved[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside approved scope"}
			}
		}
		if len(required) > 0 {
			if _, ok := required[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside required plan scope"}
			}
		}
		if len(planned) > 0 {
			if _, ok := planned[path]; !ok {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: "plan edit path is outside declared editable scope"}
			}
		}
	}
	for path := range required {
		if _, ok := seen[path]; !ok {
			return &PlanEditPreflightMismatch{Index: -1, Path: path, Reason: "required plan scope path has no submitted edit"}
		}
	}
	return nil
}

func planExecutePathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if normalized := normalizePlanExecutePath(path); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func planExecuteApprovalSatisfied(status string) bool {
	switch strings.TrimSpace(status) {
	case "approved", "implicit", "not_required", "n_a":
		return true
	default:
		return false
	}
}

func planExecuteApprovalPartial(status string) bool {
	switch strings.TrimSpace(status) {
	case "partial", "partially_approved":
		return true
	default:
		return false
	}
}

func planScopeDriftEventFields(handoff ExecutorHandoffPacket, mismatch *PlanEditPreflightMismatch) map[string]any {
	fields := map[string]any{
		"edit_index":        mismatch.Index,
		"reason":            mismatch.Reason,
		"packet_state":      handoff.State,
		"handoff_id":        handoff.HandoffID,
		"approval_scope_id": handoff.ApprovalScopeID,
	}
	if path := sanitizePlanExecuteEventPath(mismatch.Path); path != "" {
		fields["path"] = path
	}
	return fields
}

func preflightPlanRequiredEditPaths(required []string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	requiredSet := make(map[string]struct{}, len(required))
	for _, path := range required {
		normalized := normalizePlanExecutePath(path)
		if normalized != "" {
			requiredSet[normalized] = struct{}{}
		}
	}
	if len(requiredSet) == 0 {
		return nil
	}
	planned := make(map[string]struct{}, len(plan.Edits))
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		planned[path] = struct{}{}
		if _, ok := requiredSet[path]; !ok {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside required_edit_paths allowlist",
			}
		}
	}
	for _, path := range required {
		normalized := normalizePlanExecutePath(path)
		if normalized == "" {
			continue
		}
		if _, ok := planned[normalized]; !ok {
			return &PlanEditPreflightMismatch{
				Index:  -1,
				Path:   normalized,
				Reason: "plan does not include an edit for this required_edit_paths entry",
			}
		}
	}
	return nil
}

func preflightPlanAllowedEditPaths(allowed []string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, path := range allowed {
		normalized := normalizePlanExecutePath(path)
		if normalized != "" {
			allowedSet[normalized] = struct{}{}
		}
	}
	if len(allowedSet) == 0 {
		return nil
	}
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		if _, ok := allowedSet[path]; !ok {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside allowed_edit_paths allowlist",
			}
		}
	}
	return nil
}

func preflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" || !workspace.SourceSanityCoversPath(path) {
			continue
		}
		added := planEditAddedText(edit)
		if strings.TrimSpace(added) == "" {
			continue
		}
		if reason := unsupportedProofClaimReason(added); reason != "" {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: reason}
		}
	}
	return nil
}

func planEditAddedText(edit workspace.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}
	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}
	return ""
}

func unsupportedProofClaimReason(text string) string {
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

func normalizePlanExecutePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

func preflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	cache := make(map[string][]byte)
	for i, edit := range plan.Edits {
		oldText := strings.TrimSpace(edit.OldText)
		if oldText == "" {
			continue
		}
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), path)
		}
		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
			}
			body = content
			cache[abs] = body
		}
		current := string(body)
		matchCount := strings.Count(current, edit.OldText)
		if matchCount == 0 {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "old_text does not appear in file (whitespace/indentation mismatch is the most common cause)",
			}
		}
		if matchCount > 1 && !edit.ReplaceAll {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: fmt.Sprintf("old_text matched %d locations; use a line-range edit, extend old_text with surrounding context, or set replace_all=true when every occurrence is intended", matchCount),
			}
		}
	}
	if mismatch := preflightPlanFinalState(workspaceRoot, plan); mismatch != nil {
		return mismatch
	}
	return nil
}

func preflightPlanFinalState(workspaceRoot string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	cache := make(map[string][]byte)
	for i, edit := range plan.Edits {
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			return &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), path)
		}
		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
					content = nil
				} else {
					return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
				}
			}
			body = content
			cache[abs] = body
		}
		if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" && workspace.IsCompletedPlanningArtifact(path, body) {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "whole-file edit would overwrite a completed planning artifact; use a bounded anchored edit or ask the user to reopen/replace it explicitly",
			}
		}
		updated, changed, err := workspace.PreviewPlanEditContent(string(body), edit)
		if err != nil {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
		}
		if changed {
			if lineNo, line, ok := workspace.NewSuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
				return &PlanEditPreflightMismatch{
					Index:  i,
					Path:   path,
					Reason: fmt.Sprintf("edit would introduce adjacent duplicate code line %d: %q", lineNo, line),
				}
			}
		}
		cache[abs] = []byte(updated)
	}
	return nil
}

func executorEditOutcome(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "FAILED: " + result.Error
	}
	return "ok"
}

func EmitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, fields)
}

func emitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type": "idiom.plan_execute." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func FirstNonEmptyString(values ...string) string {
	return firstNonEmptyString(values...)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cleanupStalePlanArtifacts(root string, threshold time.Duration, logOut io.Writer) {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logOut, "plan-execute cleanup: read %s: %v\n", dir, err)
		}
		return
	}
	cutoff := time.Now().Add(-threshold)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "last.plan.json" {
			continue
		}
		if !strings.HasSuffix(name, ".plan.json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: remove %s: %v\n", path, err)
		}
	}
}

func LogCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	logCLIPhase(rec, eventType, fields)
}

func logCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}

func collectPlanExecutePhaseEvidence(root string, phase string, plan workspace.PlanArtifact, changedPaths []string, requiredPaths []string, allowedPaths []string, reviewEvidence ReviewRepairEvidence, executorReport ExecutorPhaseReport) PhaseEvidenceBundle {
	handoff := executorReport.Handoff
	if strings.TrimSpace(handoff.State) == "" {
		handoff = classifyExecutorHandoffPacket(root, plan)
	}
	packet := loadPlanExecutePacketEvidence(root, handoff.PackID)
	submittedPlan := collectSubmittedPlanState(root, plan)
	sourceSanity := append([]PhaseEvidenceCheck(nil), executorReport.SourceSanity...)
	patchHygiene := PhaseEvidenceCheck{
		Kind:   "git_diff_check",
		Status: "passed",
	}
	if reviewEvidence.DiffCheckExit != 0 {
		patchHygiene.Status = "failed"
		if reviewEvidence.DiffCheckExit != 1 {
			patchHygiene.Status = "unavailable"
		}
		patchHygiene.Error = strings.TrimSpace(reviewEvidence.DiffCheck)
	}
	bundle := PhaseEvidenceBundle{
		SchemaVersion:    PhaseEvidenceSchemaVersion,
		Phase:            firstNonEmptyString(strings.TrimSpace(phase), "unknown"),
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		ChangedPaths:     mergePlanExecutePaths(changedPaths),
		RequiredPaths:    mergePlanExecutePaths(requiredPaths),
		AllowedPaths:     mergePlanExecutePaths(allowedPaths),
		Packet:           packet,
		ExecutorHandoff:  phaseEvidenceHandoffFromExecutor(handoff),
		SubmittedPlan:    submittedPlan,
		SourceSanity:     sourceSanity,
		PatchHygiene:     patchHygiene,
		SuggestedTests:   packetSuggestedTests(packet),
		UnresolvedGaps:   append([]string(nil), packet.UnresolvedGaps...),
		OpenObligations:  phaseEvidenceOpenObligations(plan, packet, changedPaths, submittedPlan, sourceSanity, patchHygiene),
		DecisionEvidence: phaseEvidenceDecisionSummary(packet, submittedPlan, sourceSanity, patchHygiene),
	}
	return bundle
}

func phaseEvidenceHandoffFromExecutor(handoff ExecutorHandoffPacket) phaseEvidenceHandoff {
	return phaseEvidenceHandoff{
		PacketState:      firstNonEmptyString(strings.TrimSpace(handoff.State), ExecutorHandoffStateMissing),
		Reason:           strings.TrimSpace(handoff.Reason),
		PlanID:           strings.TrimSpace(handoff.PlanID),
		HandoffID:        strings.TrimSpace(handoff.HandoffID),
		HandoffStatus:    strings.TrimSpace(handoff.HandoffStatus),
		PackID:           strings.TrimSpace(handoff.PackID),
		DeclaredPackID:   strings.TrimSpace(handoff.DeclaredPackID),
		IndexID:          strings.TrimSpace(handoff.IndexID),
		Fresh:            handoff.Fresh,
		CandidateFiles:   handoff.CandidateFiles,
		CandidateSymbols: handoff.CandidateSymbols,
		Tests:            handoff.Tests,
	}
}

func collectSubmittedPlanState(root string, plan workspace.PlanArtifact) []PhaseEvidenceCheck {
	out := make([]PhaseEvidenceCheck, 0, len(plan.Edits))
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		check := PhaseEvidenceCheck{
			Kind:   "submitted_edit",
			Path:   path,
			Status: "unknown",
		}
		if path == "" {
			check.Status = "failed"
			check.Error = "edit has no path"
			out = append(out, check)
			continue
		}
		fullPath := path
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(strings.TrimSpace(root), path)
		}
		body, err := os.ReadFile(filepath.Clean(fullPath))
		if err != nil {
			check.Status = "failed"
			check.Error = err.Error()
			out = append(out, check)
			continue
		}
		status, summary := submittedPlanEditStatus(string(body), edit)
		check.Status = status
		check.Summary = fmt.Sprintf("edit %d/%d: %s", i+1, len(plan.Edits), summary)
		out = append(out, check)
	}
	return out
}

func submittedPlanEditStatus(current string, edit workspace.PlanEdit) (string, string) {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		if current == edit.NewText {
			return "applied", "whole-file content matches submitted new_text"
		}
		if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && current == repaired {
			return "applied", "whole-file content matches submitted new_text after mechanical trailing-whitespace repair"
		}
		return "pending", "whole-file content does not match submitted new_text"
	}
	if strings.TrimSpace(edit.OldText) != "" {
		if strings.TrimSpace(edit.NewText) == "" {
			if !strings.Contains(current, edit.OldText) {
				return "applied", "submitted old_text is absent after deletion"
			}
			return "pending", "submitted old_text is still present after deletion"
		}
		if strings.Contains(current, edit.NewText) {
			return "applied", "submitted new_text is present"
		}
		if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.Contains(current, repaired) {
			return "applied", "submitted new_text is present after mechanical trailing-whitespace repair"
		}
		if strings.Contains(current, edit.OldText) {
			return "pending", "submitted old_text is still present and new_text is absent"
		}
		return "unknown", "neither submitted old_text nor new_text is present"
	}
	if edit.StartLine > 0 || edit.EndLine > 0 {
		if strings.TrimSpace(edit.Content) != "" && strings.Contains(current, edit.Content) {
			return "applied", "submitted range content is present"
		}
		return "unknown", "submitted range content is not present as an exact substring"
	}
	if strings.TrimSpace(edit.NewText) != "" && strings.Contains(current, edit.NewText) {
		return "applied", "submitted new_text is present"
	}
	if repaired := trimTrailingWhitespaceEveryLine(edit.NewText); repaired != edit.NewText && strings.TrimSpace(repaired) != "" && strings.Contains(current, repaired) {
		return "applied", "submitted new_text is present after mechanical trailing-whitespace repair"
	}
	return "unknown", "submitted edit shape has no deterministic postcondition"
}

func loadPlanExecutePacketEvidence(root string, packID string) PhaseEvidencePacket {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return PhaseEvidencePacket{Status: "not_provided"}
	}
	if !isPlanExecutePackID(packID) {
		return PhaseEvidencePacket{PackID: packID, Status: "invalid_pack_id", Error: "pack_id must be a 16-character lowercase hex context view id"}
	}
	view, viewStatus, err := comprehension.LoadViewByIDStatus(root, packID)
	if err != nil {
		return PhaseEvidencePacket{PackID: packID, Status: "error", Error: err.Error()}
	}
	switch viewStatus.State {
	case comprehension.ViewLoadStateFresh:
		packet := PhaseEvidencePacketFromView(view)
		if !view.Fresh {
			packet.Status = "available_stale"
			packet.Fresh = false
			packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
		}
		return packet
	case comprehension.ViewLoadStateStale:
		packet := PhaseEvidencePacketFromView(view)
		packet.Status = "available_stale"
		packet.Fresh = false
		packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
		return packet
	}
	view, artifactHit, artifactErr := loadPlanExecutePacketArtifact(root, packID)
	if artifactErr != nil {
		return PhaseEvidencePacket{PackID: packID, Status: "error", Error: artifactErr.Error()}
	}
	if !artifactHit {
		return PhaseEvidencePacket{PackID: packID, Status: "miss"}
	}
	packet := PhaseEvidencePacketFromView(view)
	packet.Status = "available_stale"
	packet.Fresh = false
	packet.Confidence = firstNonEmptyString(packet.Confidence, "stale")
	return packet
}

func loadPlanExecutePacketArtifact(root string, packID string) (comprehension.PackResult, bool, error) {
	path := filepath.Join(strings.TrimSpace(root), ".idiom", "comprehension", "views", packID+".json")
	body, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return comprehension.PackResult{}, false, nil
	}
	if err != nil {
		return comprehension.PackResult{}, false, err
	}
	var view comprehension.PackResult
	if err := json.Unmarshal(body, &view); err != nil {
		return comprehension.PackResult{}, false, err
	}
	if strings.TrimSpace(view.PackID) == "" {
		view.PackID = packID
	}
	return view, true, nil
}

func PhaseEvidencePacketFromView(view comprehension.PackResult) PhaseEvidencePacket {
	packet := PhaseEvidencePacket{
		PackID:           strings.TrimSpace(view.PackID),
		Status:           "available",
		IndexID:          strings.TrimSpace(view.IndexID),
		Fresh:            view.Fresh,
		Confidence:       strings.TrimSpace(view.Confidence.Level),
		StoreKind:        firstNonEmptyString(strings.TrimSpace(view.StoreKind), strings.TrimSpace(view.Provenance.StoreKind)),
		CandidateFiles:   PhaseEvidenceFiles(view.CandidateFiles, 8),
		CandidateSymbols: PhaseEvidenceSymbols(view.CandidateSymbols, 8),
	}
	packet.UnresolvedGaps = append(packet.UnresolvedGaps, cappedStrings(view.UnresolvedGaps, 8)...)
	for _, risk := range cappedStrings(view.Risks, 4) {
		packet.UnresolvedGaps = append(packet.UnresolvedGaps, "risk="+risk)
	}
	for _, test := range view.Tests {
		packet.SuggestedTests = append(packet.SuggestedTests, PhaseEvidenceTest{
			Command:  strings.TrimSpace(test.Command),
			Path:     strings.TrimSpace(test.Path),
			Symbol:   strings.TrimSpace(test.Symbol),
			Reason:   strings.TrimSpace(test.Reason),
			Required: test.Required,
		})
		if len(packet.SuggestedTests) >= 8 {
			break
		}
	}
	return packet
}

func PhaseEvidenceFiles(files []comprehension.PackFile, limit int) []PhaseEvidenceFile {
	out := make([]PhaseEvidenceFile, 0, phaseEvidenceMinInt(limit, len(files)))
	for _, file := range files {
		out = append(out, PhaseEvidenceFile{
			Path:       strings.TrimSpace(file.Path),
			Role:       strings.TrimSpace(file.Role),
			Reason:     strings.TrimSpace(file.Reason),
			Confidence: strings.TrimSpace(file.Confidence),
			Stale:      file.Stale,
			Hub:        file.Hub,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func PhaseEvidenceSymbols(symbols []comprehension.PackSymbol, limit int) []PhaseEvidenceSymbol {
	out := make([]PhaseEvidenceSymbol, 0, phaseEvidenceMinInt(limit, len(symbols)))
	for _, symbol := range symbols {
		out = append(out, PhaseEvidenceSymbol{
			Name:       firstNonEmptyString(strings.TrimSpace(symbol.QualifiedName), strings.TrimSpace(symbol.ID)),
			Kind:       strings.TrimSpace(symbol.Kind),
			Path:       strings.TrimSpace(symbol.Path),
			Reason:     strings.TrimSpace(symbol.Reason),
			Confidence: strings.TrimSpace(symbol.Confidence),
			Stale:      symbol.Stale,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func packetSuggestedTests(packet PhaseEvidencePacket) []PhaseEvidenceTest {
	return append([]PhaseEvidenceTest(nil), packet.SuggestedTests...)
}

func phaseEvidenceOpenObligations(plan workspace.PlanArtifact, packet PhaseEvidencePacket, changedPaths []string, submittedPlan []PhaseEvidenceCheck, sourceSanity []PhaseEvidenceCheck, patchHygiene PhaseEvidenceCheck) []string {
	var out []string
	if strings.TrimSpace(plan.PackID) != "" {
		if packet.Status != "available" && packet.Status != "available_stale" {
			out = append(out, "context_packet_unavailable:"+firstNonEmptyString(packet.Status, "unknown"))
		}
	}
	for i, check := range submittedPlan {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status == "applied" {
			continue
		}
		out = append(out, fmt.Sprintf("submitted_edit_%s:%s#%d", status, firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"), i+1))
	}
	if patchHygiene.Status == "failed" {
		out = append(out, "patch_hygiene_failed")
	} else if patchHygiene.Status == "unavailable" {
		out = append(out, "patch_hygiene_unavailable")
	}
	sourceSanityByPath := map[string]bool{}
	for _, check := range sourceSanity {
		if path := strings.TrimSpace(check.Path); path != "" {
			sourceSanityByPath[path] = true
		}
	}
	for _, path := range mergePlanExecutePaths(changedPaths) {
		if workspace.SourceSanityCoversPath(path) {
			if !sourceSanityByPath[path] {
				out = append(out, "source_sanity_missing:"+path)
			}
		}
	}
	for _, check := range sourceSanity {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status != "ok" && status != "passed" {
			out = append(out, "source_sanity_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Path), "(unknown)"))
		}
	}
	return cappedStrings(out, 16)
}

func phaseEvidenceDecisionSummary(packet PhaseEvidencePacket, submittedPlan []PhaseEvidenceCheck, sourceSanity []PhaseEvidenceCheck, patchHygiene PhaseEvidenceCheck) []phaseEvidenceSummary {
	out := []phaseEvidenceSummary{
		{Kind: "context_packet", Status: packet.Status, Summary: PhaseEvidencePacketSummary(packet)},
		{Kind: "patch_hygiene", Status: patchHygiene.Status, Summary: strings.TrimSpace(patchHygiene.Error)},
	}
	submittedStatus := "not_applicable"
	if len(submittedPlan) > 0 {
		submittedStatus = "applied"
		for _, check := range submittedPlan {
			if check.Status != "applied" {
				submittedStatus = "open"
				break
			}
		}
	}
	out = append(out, phaseEvidenceSummary{Kind: "submitted_plan", Status: submittedStatus, Summary: fmt.Sprintf("%d edit(s)", len(submittedPlan))})
	status := "not_applicable"
	if len(sourceSanity) > 0 {
		status = "passed"
		for _, check := range sourceSanity {
			if check.Status != "ok" && check.Status != "passed" {
				status = "failed"
				break
			}
		}
	}
	out = append(out, phaseEvidenceSummary{Kind: "source_sanity", Status: status, Summary: fmt.Sprintf("%d check(s)", len(sourceSanity))})
	return out
}

func PhaseEvidencePacketSummary(packet PhaseEvidencePacket) string {
	switch packet.Status {
	case "available", "available_stale":
		return fmt.Sprintf("%d file(s), %d symbol(s), %d suggested test(s), %d unresolved gap(s)", len(packet.CandidateFiles), len(packet.CandidateSymbols), len(packet.SuggestedTests), len(packet.UnresolvedGaps))
	case "error":
		return strings.TrimSpace(packet.Error)
	default:
		return ""
	}
}

func renderPlanExecutePhaseEvidence(bundle PhaseEvidenceBundle) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: %s\nphase: %s\n", bundle.SchemaVersion, bundle.Phase)
	fmt.Fprintf(&b, "changed_paths: %s\n", formatInlineList(bundle.ChangedPaths))
	fmt.Fprintf(&b, "required_paths: %s\n", formatInlineList(bundle.RequiredPaths))
	fmt.Fprintf(&b, "allowed_repair_paths: %s\n\n", formatInlineList(bundle.AllowedPaths))
	fmt.Fprintf(&b, "context_packet: status=%s", bundle.Packet.Status)
	if bundle.Packet.PackID != "" {
		fmt.Fprintf(&b, " pack_id=%s", bundle.Packet.PackID)
	}
	if bundle.Packet.IndexID != "" {
		fmt.Fprintf(&b, " index_id=%s fresh=%t", bundle.Packet.IndexID, bundle.Packet.Fresh)
	}
	if summary := PhaseEvidencePacketSummary(bundle.Packet); summary != "" {
		fmt.Fprintf(&b, " summary=%s", summary)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "executor_handoff_packet: state=%s reason=%s", bundle.ExecutorHandoff.PacketState, bundle.ExecutorHandoff.Reason)
	if bundle.ExecutorHandoff.PackID != "" {
		fmt.Fprintf(&b, " pack_id=%s", bundle.ExecutorHandoff.PackID)
	}
	if bundle.ExecutorHandoff.IndexID != "" {
		fmt.Fprintf(&b, " index_id=%s fresh=%t", bundle.ExecutorHandoff.IndexID, bundle.ExecutorHandoff.Fresh)
	}
	b.WriteString("\n")
	if len(bundle.Packet.CandidateFiles) > 0 {
		b.WriteString("candidate_files:\n")
		for _, file := range bundle.Packet.CandidateFiles {
			fmt.Fprintf(&b, "- `%s` role=%s reason=%s confidence=%s\n", file.Path, file.Role, file.Reason, file.Confidence)
		}
	}
	if len(bundle.Packet.CandidateSymbols) > 0 {
		b.WriteString("candidate_symbols:\n")
		for _, symbol := range bundle.Packet.CandidateSymbols {
			fmt.Fprintf(&b, "- `%s` (%s) path=%s reason=%s\n", symbol.Name, symbol.Kind, symbol.Path, symbol.Reason)
		}
	}
	if len(bundle.SuggestedTests) > 0 {
		b.WriteString("suggested_tests:\n")
		for _, test := range bundle.SuggestedTests {
			fmt.Fprintf(&b, "- `%s` path=%s reason=%s\n", test.Command, test.Path, test.Reason)
		}
	}
	if len(bundle.SubmittedPlan) > 0 {
		b.WriteString("submitted_plan_state:\n")
		for _, check := range bundle.SubmittedPlan {
			fmt.Fprintf(&b, "- `%s` status=%s summary=%s error=%s\n", check.Path, check.Status, check.Summary, check.Error)
		}
	}
	if len(bundle.SourceSanity) > 0 {
		b.WriteString("source_sanity:\n")
		for _, check := range bundle.SourceSanity {
			fmt.Fprintf(&b, "- `%s` status=%s summary=%s error=%s\n", check.Path, check.Status, check.Summary, check.Error)
		}
	}
	fmt.Fprintf(&b, "patch_hygiene: status=%s", bundle.PatchHygiene.Status)
	if strings.TrimSpace(bundle.PatchHygiene.Error) != "" {
		fmt.Fprintf(&b, " error=%s", strings.TrimSpace(bundle.PatchHygiene.Error))
	}
	b.WriteString("\n")
	if len(bundle.UnresolvedGaps) > 0 {
		b.WriteString("unresolved_gaps:\n")
		for _, gap := range bundle.UnresolvedGaps {
			fmt.Fprintf(&b, "- %s\n", gap)
		}
	}
	if len(bundle.OpenObligations) > 0 {
		b.WriteString("open_obligations:\n")
		for _, obligation := range bundle.OpenObligations {
			fmt.Fprintf(&b, "- %s\n", obligation)
		}
	}
	return b.String()
}

func phaseEvidenceTelemetry(bundle PhaseEvidenceBundle) map[string]any {
	return map[string]any{
		"schema_version":         bundle.SchemaVersion,
		"phase":                  bundle.Phase,
		"changed_paths":          cappedStrings(bundle.ChangedPaths, 16),
		"required_paths":         cappedStrings(bundle.RequiredPaths, 16),
		"allowed_paths":          cappedStrings(bundle.AllowedPaths, 16),
		"packet_status":          bundle.Packet.Status,
		"pack_id":                bundle.Packet.PackID,
		"executor_packet_state":  bundle.ExecutorHandoff.PacketState,
		"executor_packet_reason": bundle.ExecutorHandoff.Reason,
		"executor_handoff_id":    bundle.ExecutorHandoff.HandoffID,
		"candidate_files":        len(bundle.Packet.CandidateFiles),
		"candidate_file_paths":   phaseEvidenceTelemetryFilePaths(bundle.Packet.CandidateFiles, 8),
		"candidate_symbols":      len(bundle.Packet.CandidateSymbols),
		"candidate_symbol_names": phaseEvidenceTelemetrySymbolNames(
			bundle.Packet.CandidateSymbols,
			8,
		),
		"suggested_tests": len(bundle.SuggestedTests),
		"suggested_test_commands": phaseEvidenceTelemetryTestCommands(
			bundle.SuggestedTests,
			8,
		),
		"submitted_plan_checks": len(bundle.SubmittedPlan),
		"submitted_plan":        phaseEvidenceTelemetryChecks(bundle.SubmittedPlan, 8),
		"source_sanity_checks":  len(bundle.SourceSanity),
		"source_sanity":         phaseEvidenceTelemetryChecks(bundle.SourceSanity, 8),
		"patch_hygiene_status":  bundle.PatchHygiene.Status,
		"unresolved_gap_count":  len(bundle.UnresolvedGaps),
		"unresolved_gaps":       cappedStrings(bundle.UnresolvedGaps, 8),
		"open_obligation_count": len(bundle.OpenObligations),
		"open_obligations":      bundle.OpenObligations,
	}
}

func phaseEvidenceTelemetryFilePaths(files []PhaseEvidenceFile, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(files)))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		out = append(out, path)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func phaseEvidenceTelemetrySymbolNames(symbols []PhaseEvidenceSymbol, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(symbols)))
	for _, symbol := range symbols {
		name := firstNonEmptyString(strings.TrimSpace(symbol.Name), strings.TrimSpace(symbol.Path))
		if name == "" {
			continue
		}
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func phaseEvidenceTelemetryTestCommands(tests []PhaseEvidenceTest, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(tests)))
	for _, test := range tests {
		command := firstNonEmptyString(strings.TrimSpace(test.Command), strings.TrimSpace(test.Path))
		if command == "" {
			continue
		}
		out = append(out, command)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func phaseEvidenceTelemetryChecks(checks []PhaseEvidenceCheck, limit int) []map[string]string {
	out := make([]map[string]string, 0, phaseEvidenceMinInt(limit, len(checks)))
	for _, check := range checks {
		entry := map[string]string{
			"kind":   strings.TrimSpace(check.Kind),
			"path":   strings.TrimSpace(check.Path),
			"status": strings.TrimSpace(check.Status),
		}
		if summary := strings.TrimSpace(check.Summary); summary != "" {
			entry["summary"] = phaseEvidenceTruncate(summary, 240)
		}
		if errText := strings.TrimSpace(check.Error); errText != "" {
			entry["error"] = phaseEvidenceTruncate(errText, 240)
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func phaseEvidenceTruncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	for i := range text {
		if i >= limit {
			return text[:i] + "...(truncated)"
		}
	}
	return text
}

func cappedStrings(values []string, limit int) []string {
	out := make([]string, 0, phaseEvidenceMinInt(limit, len(values)))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func phaseEvidenceMinInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatInlineList(values []string) string {
	values = cappedStrings(values, 12)
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func atomMarkdownSectionByHeading(markdown string, headingName string) string {
	want := atomNormalizeMarkdownHeadingText(headingName)
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	inFence := false
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := atomMarkdownHeading(trimmed)
		if !ok || !atomMarkdownHeadingMatches(atomNormalizeMarkdownHeadingText(text), want) {
			continue
		}
		start = i + 1
		startLevel = level
		break
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	inFence = false
	fence = ""
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, _, ok := atomMarkdownHeading(trimmed)
		if ok && level <= startLevel {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func atomMarkdownHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(line[level+1:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func atomNormalizeMarkdownHeadingText(text string) string {
	text = strings.Trim(strings.ToLower(strings.TrimSpace(text)), ": ")
	return strings.Join(strings.Fields(text), " ")
}

func atomMarkdownHeadingMatches(candidate string, want string) bool {
	candidate = util.TrimHeadingQualifier(candidate)
	want = util.TrimHeadingQualifier(want)
	return candidate == want
}

func atomMarkdownFenceOpen(line string) (marker string, info string, ok bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	default:
		return "", "", false
	}
}

func atomRequiredEditPathsFromLabel(label string, scopePaths []string) []string {
	label = strings.ReplaceAll(strings.TrimSpace(label), "\\", "/")
	if label == "" {
		return nil
	}
	baseCounts := make(map[string]int, len(scopePaths))
	for _, path := range scopePaths {
		baseCounts[filepath.Base(filepath.FromSlash(path))]++
	}
	matched := make(map[string]struct{}, len(scopePaths))
	for _, path := range scopePaths {
		normalized := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
		if normalized == "" {
			continue
		}
		if strings.Contains(label, "`"+normalized+"`") || strings.Contains(label, normalized) {
			matched[path] = struct{}{}
		}
		base := filepath.Base(filepath.FromSlash(normalized))
		if base != "" && baseCounts[base] == 1 && strings.Contains(label, "`"+base+"`") {
			matched[path] = struct{}{}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	out := make([]string, 0, len(matched))
	for _, path := range scopePaths {
		if _, ok := matched[path]; ok {
			out = append(out, path)
		}
	}
	return out
}
