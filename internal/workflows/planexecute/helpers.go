package planexecute

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
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

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/util"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/workerruntime"
	toolcmd "idiom/internal/surfaces/cli/tool"
)

func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options

	fs := flag.NewFlagSet("idiom plan-execute", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "implementation prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read implementation prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id before running")
	fs.StringVar(&opts.SessionID, "session", "", "plan-execute session id for a new run")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for this planner run")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override planner max_tokens for this run; 0 uses the shape-aware plan-execute default")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", false, "compatibility flag: plan-execute always treats verification as planner-owned; this is a no-op")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag: plan-execute does not run builds or tests; this is a no-op")
	fs.BoolVar(&opts.SkipReviewRepair, "skip-review-repair", false, "skip the post-execute review-repair planner pass")
	fs.Var(&opts.RequiredEditPaths, "required-edit-path", "workspace-relative required edit path for this plan-execute run; repeatable and augments prompt extraction")
	fs.Var(&opts.AllowedEditPaths, "allowed-edit-path", "workspace-relative candidate edit path for this plan-execute run; repeatable and augments prompt extraction without requiring coverage")
	fs.Var(&opts.WriteAllowedPaths, "write-allowed-path", "workspace-relative write envelope path for this plan-execute run; repeatable and limits writes without adding coverage")
	fs.BoolVar(&opts.WriteAllowedPathsExplicit, "write-allowed-paths-explicit", false, "treat write-allowed-path as an explicit write envelope even when empty")
	fs.Var((*MultiFlag)(&opts.AuthorizedNewFiles), "authorized-new-file", "workspace-relative new file path authorized by an upstream typed plan contract; repeatable")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}

	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return Options{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}

		opts.Prompt = strings.Join(fs.Args(), " ")
	}

	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return Options{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}

	return opts, nil
}

func PlanExecutePrompt(opts Options) (string, error) {
	return planExecutePrompt(opts)
}

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

func PlanExecuteRequiredEditPaths(opts Options, prompt string) []string {
	return planExecuteRequiredEditPaths(opts, prompt)
}

func planExecuteRequiredEditPaths(opts Options, prompt string) []string {
	return planExecuteScopeContract(opts, prompt).RequiredCoveragePaths
}

func PlanExecuteAllowedEditPaths(opts Options, prompt string) []string {
	return planExecuteAllowedEditPaths(opts, prompt)
}

func planExecuteAllowedEditPaths(opts Options, prompt string) []string {
	return planExecuteScopeContract(opts, prompt).AllowedCandidatePaths
}

func PlanExecuteScopeContract(opts Options, prompt string) PlanScopeContract {
	return planExecuteScopeContract(opts, prompt)
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

func IsPlanExecutePackID(value string) bool {
	return isPlanExecutePackID(value)
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

func PatchPlanArtifactPackID(path string, packID string) error {
	return patchPlanArtifactPackID(path, packID)
}

func planExecutePlannerSystemAddendum(requiredEditPaths []string, allowedEditPaths []string) string {
	return planExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, nil)
}

func planExecutePlannerSystemAddendumWithObligations(requiredEditPaths []string, allowedEditPaths []string, obligations EditSetObligations) string {
	lines := []string{
		"# Plan-then-execute (planner phase)",
		"",
		"Your job is to produce an executable plan artifact, not a narrative implementation answer. The executor implements accepted plan edits after you stop.",
		"Treat the brief body and any preloaded Context Pack below as the planner target packet.",
		"",
		"Visible response protocol — choose exactly one action per turn:",
		"1. If a required_edit_path does not exist on disk, treat it as a new file only when the brief explicitly authorizes new-file/full-body/create work. Without that authorization, do not invent the path; answer `<<QUESTION>>:` with the missing-path localization blocker.",
		"2. If an existing required_edit_path has not produced source-body evidence in this planner turn, call exactly one source-read tool for that path. Use `read_file`; if it redirects to an outline, follow with `code_navigate` `read_symbol` or bounded `read_file` with `start_line`/`end_line`. Do not narrate the read.",
		"3. Once every existing required_edit_path has satisfied its source-body obligation, call `history_range` or `history_symbol` for each selected existing edit path that still lacks required git history evidence, only when the active runtime contract requires git history evidence. Do not perform additional source reads in that phase. After history evidence is present for every selected existing edit path, the next visible action must be exactly one terminal outcome: `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:`.",
		fmt.Sprintf("4. If the requested final state is already present, answer `Decision: source-satisfied` and cite current-state evidence for every required path. Do not use unsupported inference language such as %s; if any acceptance criterion is only inferred, submit a grounded plan or answer `<<QUESTION>>:` with the blocker.", planExecuteSourceSatisfiedUnsupportedEvidenceMarkerListForPrompt()),
		"5. If the plan cannot be produced from available evidence, answer `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Plan artifact rules:",
		"1. Per-edit write capabilities are blocked in the planner phase; the available write contract is `submit_plan`.",
		"2. Each `submit_plan.edits[]` entry must be a JSON object with `path` + ONE edit shape: (`insert_after_line`+small inline non-empty `content` for insertion after an existing line | `start_line`+`end_line`+non-empty `content` for replacement | `old_text`+`new_text`; empty `new_text` deletes `old_text` | `replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write) + one-sentence `purpose`. `edits` is an array, never a quoted JSON string. Prefer the smallest executable edit body that satisfies the objective: use exact `old_text`/`new_text` for small unique replacements, and use numeric line-range edits only when the replacement spans more than a few lines, contains JSON-hostile syntax, or needs a large insertion anchored to unchanged source lines. Large bodies are spilled to artifact refs after submit_plan except insert_after_line content.",
		"3. Each edit body must be executable final content. Do not submit placeholders, ellipses, `TODO` markers, `existing code` comments, omitted bodies, pseudo-code, prose descriptions, invented symbols, no-op assertions, or self-disqualifying proof text such as `simplified check`, `for now we just verify`, or `verify that it exists` in `old_text`, `new_text`, or `content`.",
		"4. `insert_after_line` inserts small inline non-empty `content` after an existing 1-based line and does not replace current lines; do not use it for large generated blocks. Line-range `content` is the complete replacement for the inclusive range and may include unchanged anchor line(s) plus inserted content for large additions. `old_text` is exact current text copied from source-body evidence. Whole-file/new-file `new_text` is the complete file body.",
		"5. One insertion site = one edit. Don't combine non-adjacent sites into one `old_text` (use \\n\\n only when sites are literally adjacent). Same `path` in two entries is fine.",
		"6. Substring-anchor indentation must match the file byte-for-byte. Brief code blocks under markdown bullets carry 2+ extra leading spaces. Before using any multi-line substring anchor, one bounded source-read capability may verify that path and strip the rendering indent. Single-line / identifier anchors are exempt.",
		"7. Exact anchors, current-state anchors, full new-file bodies, explicit replacement edit shapes, unified patches, and ranked packet evidence can identify the intended edit, but required existing paths still need source-body obligations satisfied before submit_plan unless the run short-circuits as source-satisfied or exact-plan-ready before planner execution.",
		"8. Use source-read capability only for a new named edit-safety fact on a required or candidate path. Do not re-read for confidence; if the brief names a missing required source/test path but gives only behavior or helper-shape requirements with no substantial body/anchor, stop with `<<QUESTION>>:` and name the missing body evidence.",
		"9. Test/source edit coverage requirements are enforced only by typed `Requested edit-set obligations` attached below. Without that section, choose the complete plan, source-satisfied closeout, or exact blocker from required/candidate paths and gathered evidence; do not infer hidden edit-set obligations from objective wording.",
		"10. Submit as soon as you have source anchors plus runtime-required git history evidence for every selected existing edit path. With required_edit_paths, every required existing path is selected unless it is explicitly source-satisfied or blocked; with candidate-only scope, only the needed candidate paths are selected. If current source already contains the requested final state, answer `Decision: source-satisfied` and name every relevant path plus evidence instead of submitting stale edits. After `submit_plan` returns OK, finalize with a one-paragraph summary and stop. Rejected `submit_plan` calls are invalid attempts, not successful plan artifacts. No post-plan verification reads.",
		"11. After replacement, the resulting file must be syntactically valid. Verify that line-range `content` does not leave control flow statements (`if`, `for`, `switch`, `return`) at package scope outside any function or method body. A bare `if`, `for`, `switch`, or `return` that ends up between top-level declarations will be rejected by preflight syntax validation.",
		"",
		"Canonical `submit_plan` shape (real values, never placeholders): emit one visible JSON object with top-level `\"tool\":\"submit_plan\"`, a one-sentence `rationale`, and an `edits` array. Each edit object must use a real required or selected path from gathered evidence, plus exactly one executable edit shape such as `insert_after_line` with inline content, `start_line`/`end_line` with complete replacement `content`, exact `old_text`/`new_text`, or whole/new-file `replace_all:true` with full `new_text`.",
	}
	if checklist := planExecuteRequiredPathChecklist(requiredEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else if checklist := planExecuteAllowedPathChecklist(allowedEditPaths); checklist != "" {
		lines = append(lines, "", checklist)
	} else {
		lines = append(lines, "", "No required edit paths were provided. Use the objective's candidate/target refs as the planning scope: you still must either call `submit_plan` with real edits grounded in the objective/target refs, answer `Decision: source-satisfied` with evidence, or answer `<<QUESTION>>:` with the exact blocker.")
	}

	if checklist := planExecuteEditSetObligationChecklist(obligations); checklist != "" {
		lines = append(lines, "", checklist)
	}

	return strings.Join(lines, "\n")
}

func planExecuteEditSetObligationChecklist(obligations EditSetObligations) string {
	obligations = obligations.Normalized()
	if len(obligations) == 0 {
		return ""
	}

	var lines []string

	lines = append(lines, fmt.Sprintf("Requested edit-set obligations for this run: %d obligation(s).", len(obligations)))

	if obligations.Has(EditSetObligationKindTestEdit) {
		lines = append(lines, "- `test_edit_required`: `submit_plan.edits`, source-satisfied evidence, or a corrective review plan must include at least one real test edit or test-path evidence.")
	}

	if obligations.Has(EditSetObligationKindImplementationEdit) {
		lines = append(lines, "- `implementation_edit_required`: `submit_plan.edits`, source-satisfied evidence, or a corrective review plan must include at least one real non-test implementation edit or implementation-path evidence.")
	}

	lines = append(lines, "These obligations do not add or promote `required_edit_paths`; candidate and allowed paths remain scope only.")

	return strings.Join(lines, "\n")
}

func AppendPlanExecuteScopeToBriefBody(prompt string, requiredEditPaths []string, allowedEditPaths []string) string {
	return appendPlanExecuteScopeToBriefBody(prompt, requiredEditPaths, allowedEditPaths)
}

func appendPlanExecuteScopeToBriefBody(prompt string, requiredEditPaths []string, allowedEditPaths []string) string {
	section := planExecuteScopeSection(requiredEditPaths, allowedEditPaths)
	if section == "" {
		return prompt
	}

	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func planExecuteScopeSection(requiredEditPaths []string, allowedEditPaths []string) string {
	required := planExecuteUniqueRequiredPaths(requiredEditPaths)
	allowed := planExecuteUniqueRequiredPaths(allowedEditPaths)

	allowed = planExecuteScopePathsExcludingRequired(allowed, required)

	if len(required) == 0 && len(allowed) == 0 {
		return ""
	}

	var b strings.Builder

	if len(required) > 0 {
		b.WriteString("## Files to Modify\n\n")
		b.WriteString("required_edit_paths:\n")

		for _, path := range required {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}

	if len(allowed) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}

		b.WriteString("## Candidate Edit Scope From Target References\n\n")
		b.WriteString("allowed_edit_paths:\n")

		for _, path := range allowed {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

func planExecuteScopePathsExcludingRequired(paths []string, required []string) []string {
	requiredSet := make(map[string]struct{}, len(required))
	for _, path := range required {
		if path != "" {
			requiredSet[path] = struct{}{}
		}
	}

	if len(requiredSet) == 0 {
		return paths
	}

	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := requiredSet[path]; ok {
			continue
		}

		out = append(out, path)
	}

	return out
}

func planExecuteRequiredPathChecklist(requiredEditPaths []string) string {
	paths := planExecuteUniqueRequiredPaths(requiredEditPaths)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Required path coverage for this run: %d required path(s).\n", len(paths))

	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}

	b.WriteString("After every existing required path has satisfied its source-body obligation, do not perform additional source reads. Read runtime-required git history evidence for every listed existing required path (new files satisfy this automatically). If at least one required path needs a change, your first successful `submit_plan` must cover every path above with real executable edits for changed paths and top-level `source_satisfied_paths` only for already-final paths. If every required path already contains the requested final state, do not call `submit_plan`; answer `Decision: source-satisfied` with per-path current-state evidence. Do not submit no-op edits for source-satisfied paths. Do not treat a source read or history read as plan coverage. Do not submit a partial plan and do not answer in prose while any listed path remains uncovered.")

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

func planExecuteAllPathsMissing(workspaceRoot string, paths []string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" || len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		path = normalizePlanExecutePath(path)
		if path == "" {
			return false
		}

		if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(path))); err == nil {
			return false
		} else if !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func planExecuteImplicitBodyLooksSafeForPath(path string, body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return strings.HasPrefix(body, "package ")
	default:
		return true
	}
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

func NormalizePlanExecutePath(path string) string {
	return normalizePlanExecutePath(path)
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return plancontract.UniqueScopePaths(requiredEditPaths)
}

func PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths []string, allowedEditPaths []string, obligations EditSetObligations) string {
	return planExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, obligations)
}

func PlanExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEdits(prompt, requiredEditPaths)
}

func PlanExecuteExactPlanEditsForWorkspace(prompt string, requiredEditPaths []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	return planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, workspaceRoot)
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

func PlanExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence ReviewRepairEvidence) string {
	return planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
}

func ReviewAnswerPassContradictionReason(answer string) string {
	return reviewAnswerPassContradictionReason(answer)
}

func PlanExecutePathLooksLikeTest(path string) bool {
	return planExecutePathLooksLikeTest(path)
}

func PhaseEvidenceTelemetryChecks(checks []PhaseEvidenceCheck, limit int) []map[string]string {
	return phaseEvidenceTelemetryChecks(checks, limit)
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
		HandoffCreated:          strings.TrimSpace(plan.Handoff.ID) != "",
		PacketFreshnessState:    ExecutorPacketFreshnessMissing,
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
		if util.EqualTrimFold(plan.Packet.Status, "not_used") {
			handoff.PacketNotUsedByDesign = true
			handoff.PacketFreshnessState = ExecutorPacketFreshnessNotUsedByDesign
			handoff.Reason = "packet_not_used_by_design"
		}

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
		} else if handoff.Fresh {
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
		} else if handoff.Fresh {
			handoff.State = ExecutorHandoffStateInherited
			handoff.Reason = "fresh_scoped_graph_view"
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

func ClassifyExecutorHandoffPacket(root string, plan workspace.PlanArtifact) ExecutorHandoffPacket {
	return classifyExecutorHandoffPacket(root, plan)
}

func applyExecutorHandoffView(handoff ExecutorHandoffPacket, view comprehension.PackResult) ExecutorHandoffPacket {
	handoff.PackID = firstNonEmptyString(normalizePlanExecutePackID(view.PackID), handoff.PackID)
	handoff.IndexID = sanitizePlanExecuteEventToken(firstNonEmptyString(view.IndexID, handoff.IndexID))
	handoff.Fresh = executorHandoffViewFresh(view)
	handoff.PacketPresent = true

	if handoff.Fresh {
		handoff.PacketFreshnessState = ExecutorPacketFreshnessFresh
	} else {
		handoff.PacketFreshnessState = ExecutorPacketFreshnessStale
	}

	handoff.CandidateFiles = len(view.CandidateFiles)
	handoff.CandidateSymbols = len(view.CandidateSymbols)
	handoff.Tests = len(view.Tests)

	proj := comprehension.NewCompactPackProjection(view)

	handoff.Quality = proj.Quality

	return handoff
}

func executorHandoffViewFresh(view comprehension.PackResult) bool {
	return view.Fresh || (view.Freshness != nil && view.Freshness.FreshForScope)
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

func EmitExecutorHandoffPacketEvents(stdout io.Writer, jsonEvents bool, handoff ExecutorHandoffPacket) {
	emitExecutorHandoffPacketEvents(stdout, jsonEvents, handoff)
}

func executorHandoffPacketEventFields(handoff ExecutorHandoffPacket) map[string]any {
	fields := map[string]any{
		"packet_state":              handoff.State,
		"reason":                    handoff.Reason,
		"fresh":                     handoff.Fresh,
		"handoff_created":           handoff.HandoffCreated,
		"packet_present":            handoff.PacketPresent,
		"packet_not_used_by_design": handoff.PacketNotUsedByDesign,
		"packet_freshness_state":    firstNonEmptyString(handoff.PacketFreshnessState, ExecutorPacketFreshnessUnknown),
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

	quality := executorHandoffQualityFields(handoff.Quality)
	if len(quality) > 0 {
		fields["quality"] = quality
	}

	return fields
}

// executorHandoffQualityFields converts CompactPackQuality into a
// bounded map for JSON event emission. Returns nil when the quality
// has no localization confidence (unset zero value).
func executorHandoffQualityFields(q comprehension.CompactPackQuality) map[string]any {
	confidence := strings.TrimSpace(q.LocalizationConfidence)
	if confidence == "" {
		return nil
	}

	fields := map[string]any{
		"localization_confidence": confidence,
		"source_owner_found":      q.SourceOwnerFound,
	}
	if len(q.EvidencePaths) > 0 {
		fields["evidence_paths"] = q.EvidencePaths
	}

	if len(q.Reasons) > 0 {
		fields["reasons"] = q.Reasons
	}

	return fields
}

func ExecutorHandoffPacketEventFields(handoff ExecutorHandoffPacket) map[string]any {
	return executorHandoffPacketEventFields(handoff)
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
) (report ReviewRepairPhaseReport, err error) {

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

	editSetObligations := planExecuteEditSetObligations(opts, originalPrompt)

	evidence.PhaseEvidence.EditSetObligations = editSetObligations
	evidence.PhaseEvidence.OpenObligations = append(evidence.PhaseEvidence.OpenObligations, editSetObligations.MissingOpenObligations(changedPaths)...)

	reviewSessionID := strings.TrimSpace(opts.ReviewSessionID)

	if reviewSessionID == "" {
		reviewSessionID = newPlanExecuteSessionID()
	}

	reviewCfg := cfg

	reviewCfg.Workspace.SessionID = reviewSessionID

	reviewStart := time.Now().Add(-1 * time.Second)
	reviewLifecycle := beginPlanExecuteReviewRepairLifecycle(cfg, opts, reviewSessionID, changedPaths, allowedRepairPaths, stdout)
	defer func() {
		emitPlanExecuteReviewRepairLifecycleCloseout(stdout, opts.JSONEvents, reviewLifecycle, report, err)
	}()

	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_start", map[string]any{
		"session_id":                reviewSessionID,
		"changed_paths":             changedPaths,
		"allowed_paths":             allowedRepairPaths,
		"edit_set_obligations":      editSetObligations,
		"edit_set_obligation_count": len(editSetObligations),
		"phase_evidence":            phaseEvidenceTelemetry(evidence.PhaseEvidence),
	})

	if planExecuteDeterministicCleanReviewEnabled() {
		reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
		if reason == "" {
			reason = editSetObligations.RejectionReason(changedPaths)
		}

		if reason == "" && len(evidence.PhaseEvidence.OpenObligations) == 0 {
			emitPlanExecuteSyntheticTurnSummary(stdout, opts.JSONEvents, "review_repair_deterministic_pass")
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":        "pass",
				"repairs":       0,
				"deterministic": true,
			})

			return ReviewRepairPhaseReport{Status: "pass (deterministic evidence)", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}
	}

	prompt := planExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, evidence)
	reviewMaxTokens := opts.MaxTokens

	if reviewMaxTokens <= 0 {
		reviewMaxTokens = 8192
	}

	var (
		planPath string
		found    bool
	)

	for attempt := 0; ; attempt++ {
		answer, err := promptRun(reviewCfg, prompt, RunOptions{
			JSONEvents:                  opts.JSONEvents,
			TaskFamily:                  string(workerruntime.TaskFamilyPlanThenExecuteReview),
			PlannerOwnedVerification:    true,
			MaxTokensOverride:           reviewMaxTokens,
			DisableThinking:             true,
			ReviewRepairOpenObligations: append([]string(nil), evidence.PhaseEvidence.OpenObligations...),
			Telemetry:                   rec,
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

		planPath, found, err = findOptionalReviewPlanArtifact(cfg.Workspace.Root, reviewStart, reviewSessionID)
		if err != nil {
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_failed", map[string]any{
				"reason": err.Error(),
			})

			return ReviewRepairPhaseReport{Status: "failed"}, err
		}

		if found {
			break
		}

		if plancontract.ReviewRepairAnswerDeclaresPass(answer) && plancontract.ReviewRepairTextHasChecklist(answer) {
			reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence)
			if reason == "" {
				reason = reviewAnswerPassContradictionReason(answer)
			}

			if reason == "" {
				reason = editSetObligations.RejectionReason(changedPaths)
			}

			if reason != "" {
				emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_pass_rejected", map[string]any{
					"reason":                    reason,
					"edit_set_obligations":      editSetObligations,
					"edit_set_obligation_count": len(editSetObligations),
					"retry_available":           attempt == 0,
				})

				if attempt == 0 {
					emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_retry", map[string]any{
						"reason": reason,
					})

					prompt = planExecuteReviewRepairRejectedPassRetryPrompt(prompt, reason)

					continue
				}

				return ReviewRepairPhaseReport{Status: "failed", ChecklistFound: true}, fmt.Errorf("review-repair phase rejected contradictory `Decision: pass`: %s", reason)
			}

			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_complete", map[string]any{
				"status":  "pass",
				"repairs": 0,
			})

			return ReviewRepairPhaseReport{Status: "pass (no corrective edits)", AutoRepairs: autoRepair.Lines, ChecklistFound: true}, nil
		}

		if reviewAnswerDeclaresBlocker(answer) {
			blocker := reviewRepairBlockerSummary(answer)
			emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_blocked", map[string]any{
				"status":  "blocked",
				"blocker": blocker,
			})

			status := "blocked"
			if blocker != "" {
				status += ": " + blocker
			}

			return ReviewRepairPhaseReport{Status: status}, nil
		}

		return ReviewRepairPhaseReport{Status: "inconclusive"}, fmt.Errorf("review-repair phase produced no corrective plan and no `Decision: pass` with `Verification Checklist`")
	}

	repairPlan, err := workspace.LoadPlanArtifact(planPath)
	if err != nil {
		return ReviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("read review-repair plan artifact %q: %w", planPath, err)
	}

	if !plancontract.ReviewRepairTextHasChecklist(repairPlan.Rationale) {
		return ReviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath}, fmt.Errorf("review-repair plan %q is missing `Verification Checklist` in submit_plan rationale", planPath)
	}

	repairPaths := planChangedPaths(repairPlan)
	combinedPaths := mergePlanExecutePaths(changedPaths, repairPaths)

	if reason := editSetObligations.RejectionReason(combinedPaths); reason != "" {
		emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, "review_repair_plan_rejected", map[string]any{
			"reason":                    reason,
			"review_plan":               planPath,
			"changed_paths":             changedPaths,
			"repair_paths":              repairPaths,
			"combined_paths":            combinedPaths,
			"edit_set_obligations":      editSetObligations,
			"edit_set_obligation_count": len(editSetObligations),
		})

		return ReviewRepairPhaseReport{Status: "failed", ReviewPlan: planPath, ChecklistFound: true}, fmt.Errorf("review-repair phase rejected corrective plan: %s", reason)
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
		Status:               "repaired",
		Repairs:              repairReport.Succeeded,
		AutoRepairs:          autoRepair.Lines,
		ReviewPlan:           planPath,
		ChecklistFound:       true,
		SourceSanity:         append([]PhaseEvidenceCheck(nil), repairReport.SourceSanity...),
		Verification:         append([]PhaseEvidenceCheck(nil), repairReport.Verification...),
		VerificationCommands: append([]VerificationCommand(nil), repairReport.VerificationCommands...),
		VerificationSkips:    append([]VerificationSkip(nil), repairReport.VerificationSkips...),
		VerificationRejects:  append([]VerificationReject(nil), repairReport.VerificationRejects...),
	}, nil
}

func reviewRepairDegradedReasonClass(err error) string {
	if err == nil {
		return ""
	}

	switch failures.CodeOf(err) {
	case failures.Canceled:
		return "backend_canceled"
	case failures.DeadlineExceeded:
		return "backend_timeout"
	case failures.Unavailable, failures.NotFound:
		return "backend_unreachable"
	case failures.Unauthenticated, failures.PermissionDenied, failures.ResourceExhausted,
		failures.InvalidArgument, failures.Internal, failures.DataLoss:
		return "review_model_error"
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

func phaseEvidencePassRejectionReason(bundle PhaseEvidenceBundle) string {
	for _, obligation := range bundle.OpenObligations {
		obligation = strings.TrimSpace(obligation)
		switch {
		case obligation == "patch_hygiene_failed":
			return "patch_hygiene_failed"
		case strings.HasPrefix(obligation, "source_sanity_failed:"),
			strings.HasPrefix(obligation, "source_sanity_error:"),
			strings.HasPrefix(obligation, "verification_failed:"),
			strings.HasPrefix(obligation, "verification_error:"),
			strings.HasPrefix(obligation, "submitted_edit_failed:"):
			return RunFailureVerificationRequired
		}
	}

	return ""
}

func planExecuteDeterministicCleanReviewEnabled() bool {
	return util.EqualTrimFold(os.Getenv("IDIOM_PLAN_EXECUTE_DETERMINISTIC_CLEAN_REVIEW"), "true")
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
	b.WriteString("Use the Phase Evidence Bundle, Diff Evidence, submitted_plan_state, source_sanity, verification entries, and inherited context packet as the primary evidence ledger. Do not restart discovery as if this were a fresh review.\n\n")

	if reason := phaseEvidencePassRejectionReason(evidence.PhaseEvidence); reason != "" {
		fmt.Fprintf(&b, "Phase Evidence already has failed verification or source_sanity obligations (`%s`). Treat that failure as sufficient post-edit evidence: do not perform additional reads, searches, or context refreshes before choosing the terminal outcome. Submit a corrective plan if the failure can be repaired inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` naming the exact failed check.\n\n", reason)
	} else {
		b.WriteString("If exact post-edit evidence is missing, make one targeted probe: refresh/check context state with `context_status` (`refresh=true`) or use `context_query`, `context_zoom`, `code_navigate`, or one bounded `read_file` range for one named checklist fact. Do not sweep large files with unbounded or multi-hundred-line raw reads; broad raw reads waste the review budget and duplicate the sidecar evidence.\n\n")
	}

	b.WriteString("Create a `Verification Checklist` before deciding. This checklist must verify behavior, not just literal code correctness. Cover:\n\n")
	b.WriteString("- Required behavior from the original task/spec.\n")
	b.WriteString("- Default/non-regression behavior that should remain equivalent.\n")
	b.WriteString("- Data, lifecycle, state, persistence, concurrency/threading, and performance invariants when relevant.\n")
	b.WriteString("- Edge cases and error/empty/default paths implied by the change.\n")
	b.WriteString("- Scope safety: changed paths, out-of-scope writes, duplicate/bespoke logic, and stale-artifact risk.\n")
	b.WriteString("- Test or command evidence available now, plus any planner-owned verification that still must run outside this lane.\n\n")
	b.WriteString("Outcomes:\n\n")
	b.WriteString("- If any checklist item fails and the repair is inside Allowed Repair Paths, call `submit_plan` once with corrective edits only. The `submit_plan.rationale` must start with `Verification Checklist:` and include the checklist plus the failing item each edit addresses.\n")
	b.WriteString("- Corrective `submit_plan.edits[]` entries must be executable final content, not review prose: no placeholders, ellipses, `TODO` markers, `existing code` comments, omitted bodies, pseudo-code, or invented symbols. Use `insert_after_line`+`content` only for small inline insertions after an existing line. Use line-range `content` for non-trivial source/test replacements and large additions; it is the complete replacement for the inclusive range and may include unchanged anchor line(s) plus inserted content. Use `old_text` only for tiny exact snippets copied from diff/source evidence. Whole-file/new-file `new_text` is the complete file body. The top-level `edits` value is a JSON array, not a quoted JSON string.\n")
	b.WriteString("- The submitted plan is pre-executor intent. If `submitted_plan_state` says those edits are already applied, do not re-submit them; submit only new corrective edits for defects found by the checklist.\n")
	b.WriteString("- If the diff satisfies the checklist, do not call `submit_plan`; answer with `Decision: pass` and include the `Verification Checklist`. Empty corrective plans are invalid.\n")
	b.WriteString("- If the issue is ambiguous or outside Allowed Repair Paths, stop with `<<QUESTION>>:` and name the blocker. Do not improvise out-of-scope writes.\n\n")
	b.WriteString(plancontract.ReviewRepairTerminalTemplates())
	b.WriteString("\n")
	b.WriteString("If Phase Evidence lists `requested_test_edit_missing`, `Decision: pass` is invalid: a typed test-edit obligation is still unsatisfied because this run changed no test path. Submit a corrective test edit only if it is inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the missing test-edit scope.\n\n")
	b.WriteString("If Phase Evidence lists `requested_implementation_edit_missing`, `Decision: pass` is invalid: a typed implementation-edit obligation is still unsatisfied because this run changed only test paths. Submit a corrective implementation edit if it is inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the missing source-edit scope.\n\n")
	b.WriteString("If Phase Evidence lists `verification_failed:` or any verification/source_sanity check with status `failed`, `Decision: pass` is invalid. Submit a corrective plan if the failing checklist item can be repaired inside Allowed Repair Paths; otherwise stop with `<<QUESTION>>:` and name the exact failed check.\n\n")
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

type planExecuteReviewRepairLifecycle struct {
	Invocation autonomy.ParentChildInvocationRecord
}

func beginPlanExecuteReviewRepairLifecycle(cfg config.Config, opts Options, reviewSessionID string, changedPaths []string, allowedRepairPaths []string, stdout io.Writer) planExecuteReviewRepairLifecycle {
	parentContractID := strings.TrimSpace(opts.ScopeContract.ContractID)
	if parentContractID == "" {
		parentContractID = "plan_execute_" + planExecuteShortHash(cfg.Workspace.Root, reviewSessionID)
	}

	childContractID := "child_contract_" + planExecuteShortHash(parentContractID, reviewSessionID, "review_repair")
	invocationID := "child_invocation_" + planExecuteShortHash(childContractID, strings.Join(changedPaths, "\x00"), strings.Join(allowedRepairPaths, "\x00"))
	modelAlias := firstNonEmptyString(strings.TrimSpace(opts.Model), strings.TrimSpace(cfg.LlamaServer.Model), "default")
	record := autonomy.ParentChildInvocationRecord{
		InvocationID:     invocationID,
		ParentContractID: parentContractID,
		ChildContractID:  childContractID,
		Role:             "review_repair",
		Lane:             "plan_execute",
		Phase:            "review_repair",
		WorkspaceMode:    autonomy.ChildWorkspaceNoWrite,
		ContractSummary: autonomy.ChildContractSummary{
			PathRoleDigest:   "sha256:" + planExecuteFullHash(strings.Join(allowedRepairPaths, "\x00"), "path_roles"),
			ToolPolicyDigest: "sha256:" + planExecuteFullHash("review_repair_no_write", "tool_policy"),
			ObligationDigest: "sha256:" + planExecuteFullHash(strings.Join(changedPaths, "\x00"), "review_obligations"),
		},
		RuntimeEnvelope: autonomy.ChildRuntimeEnvelope{
			BackendKind: "prompt_runner",
			ModelAlias:  modelAlias,
			ModelRole:   "review_repair",
			RoleResolution: autonomy.ChildRoleResolution{
				RequestedRole: "review_repair",
				ResolvedRole:  "review_repair",
				Alias:         modelAlias,
				Source:        autonomy.RoleResolutionSourceRoleTag,
				OK:            true,
			},
			CapacityAdmission: autonomy.ChildCapacityAdmission{
				State:       autonomy.CapacityAdmissionAcquired,
				ModelAlias:  modelAlias,
				Enforcement: autonomy.CapacityAdmissionOpportunistic,
			},
			ToolTransportMode:   autonomy.ChildTransportTextual,
			ParserTransportMode: autonomy.ChildTransportTextual,
			ParserContract:      "review_repair_terminal",
			MaxTokens:           opts.MaxTokens,
		},
		CreatedAt: time.Now().UTC(),
	}
	emitPlanExecuteAutonomyRecord(stdout, opts.JSONEvents, "parent_child_invocation", &record)

	return planExecuteReviewRepairLifecycle{Invocation: record}
}

func emitPlanExecuteReviewRepairLifecycleCloseout(stdout io.Writer, jsonEvents bool, lifecycle planExecuteReviewRepairLifecycle, report ReviewRepairPhaseReport, err error) {
	if strings.TrimSpace(lifecycle.Invocation.InvocationID) == "" {
		return
	}

	terminal := planExecuteReviewRepairTerminalRecord(lifecycle, report, err)
	emitPlanExecuteAutonomyRecord(stdout, jsonEvents, "child_terminal", &terminal)

	acceptance := planExecuteReviewRepairAcceptanceRecord(lifecycle, terminal, report, err)
	emitPlanExecuteAutonomyRecord(stdout, jsonEvents, "child_acceptance", &acceptance)
}

func planExecuteReviewRepairTerminalRecord(lifecycle planExecuteReviewRepairLifecycle, report ReviewRepairPhaseReport, err error) autonomy.ChildTerminalRecord {
	status := autonomy.ChildTerminalCompleted
	objective := autonomy.ChildObjectiveAccepted
	resultKind := autonomy.ChildResultFindings
	if strings.TrimSpace(report.ReviewPlan) != "" {
		resultKind = autonomy.ChildResultPlanArtifact
	}
	if err != nil {
		status = autonomy.ChildTerminalFailed
		objective = autonomy.ChildObjectiveWeak
		resultKind = autonomy.ChildResultTypedBlocker
	} else if strings.HasPrefix(strings.TrimSpace(report.Status), "blocked") || strings.HasPrefix(strings.TrimSpace(report.Status), "degraded") {
		status = autonomy.ChildTerminalBlocked
		objective = autonomy.ChildObjectiveWeak
		resultKind = autonomy.ChildResultTypedBlocker
	}

	evidenceRefs := []string{"review_status:" + planExecuteShortHash(report.Status)}
	if strings.TrimSpace(report.ReviewPlan) != "" {
		evidenceRefs = append(evidenceRefs, "review_plan:"+planExecuteShortHash(report.ReviewPlan))
	}

	return autonomy.ChildTerminalRecord{
		TerminalID:         "child_terminal_" + planExecuteShortHash(lifecycle.Invocation.InvocationID, report.Status, firstNonEmptyString(report.ReviewPlan, "no_plan")),
		InvocationID:       lifecycle.Invocation.InvocationID,
		ChildContractID:    lifecycle.Invocation.ChildContractID,
		ResultKind:         resultKind,
		Status:             status,
		EvidenceRefs:       planExecuteUniqueRefStrings(evidenceRefs),
		ChangedPaths:       nil,
		ObjectiveAlignment: objective,
	}
}

func planExecuteReviewRepairAcceptanceRecord(lifecycle planExecuteReviewRepairLifecycle, terminal autonomy.ChildTerminalRecord, report ReviewRepairPhaseReport, err error) autonomy.ChildAcceptanceRecord {
	decision := autonomy.ChildAcceptanceAccepted
	reasonCode := "review_repair_accepted"
	if err != nil {
		decision = autonomy.ChildAcceptanceRepairRequired
		reasonCode = "review_repair_failed"
	} else if terminal.Status == autonomy.ChildTerminalBlocked {
		decision = autonomy.ChildAcceptanceBlocked
		reasonCode = "review_repair_blocked"
	}

	acceptedEvidenceRefs := []string(nil)
	if decision == autonomy.ChildAcceptanceAccepted {
		acceptedEvidenceRefs = append([]string(nil), terminal.EvidenceRefs...)
	}

	return autonomy.ChildAcceptanceRecord{
		AcceptanceID:         "child_acceptance_" + planExecuteShortHash(terminal.TerminalID, string(decision)),
		ParentContractID:     lifecycle.Invocation.ParentContractID,
		ChildContractID:      lifecycle.Invocation.ChildContractID,
		InvocationID:         lifecycle.Invocation.InvocationID,
		TerminalID:           terminal.TerminalID,
		ResultKind:           terminal.ResultKind,
		Decision:             decision,
		AcceptedEvidenceRefs: acceptedEvidenceRefs,
		RuntimeEnvelopeRef:   "runtime_envelope:" + lifecycle.Invocation.InvocationID,
		AlignmentBasis: autonomy.ChildAlignmentBasis{
			DelegatedObjectiveRef:  "objective:" + planExecuteShortHash(lifecycle.Invocation.InvocationID),
			EffectiveScopeRefs:     []string{"lane:plan_execute", "phase:review_repair"},
			AcceptedSourceBodyRefs: acceptedEvidenceRefs,
			Evaluator:              autonomy.AlignmentEvaluatorHostContract,
		},
		ReasonCode: reasonCode,
	}
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

func TrimTrailingWhitespaceEveryLine(text string) string {
	return trimTrailingWhitespaceEveryLine(text)
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
		case plancontract.ReviewRepairLineIsPlaceholderChecklist(line):
			return "placeholder_checklist_item"
		case strings.Contains(line, "❌"), strings.Contains(line, "[ ]"):
			if reviewLineIsPlannerOwnedPending(line) {
				continue
			}

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
		case reviewLineContainsMissingCommandEvidenceClaim(line):
			if reviewLineIsPlannerOwnedPending(line) {
				continue
			}

			return "missing_command_evidence_claim"
		}
	}

	return ""
}

func reviewLineContainsMissingCommandEvidenceClaim(line string) bool {
	return reviewLineContainsAny(line,
		"didn't run ",
		"did not run ",
		"haven't run ",
		"hasn't run ",
		"wasn't run",
		"weren't run",
		"was not run",
		"were not run",
		"not run ",
		"no test command was run",
		"no verification command was run",
	)
}

func planExecuteChangedPathsIncludeTest(paths []string) bool {
	return plancontract.ChangedPathsIncludeTest(paths)
}

func planExecuteChangedPathsIncludeImplementation(paths []string) bool {
	return plancontract.ChangedPathsIncludeImplementation(paths)
}

func planExecutePathLooksLikeImplementation(path string) bool {
	return plancontract.PathLooksLikeImplementation(path)
}

func planExecutePathLooksLikeTest(path string) bool {
	return plancontract.PathLooksLikeTest(path)
}

func reviewLineIsPlannerOwnedPending(line string) bool {
	return reviewLineContainsAny(line,
		"planner-owned",
		"planner owned",
		"pending outside this lane",
		"pending outside lane",
		"must run outside this lane",
		"not run by this lane",
	)
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
	return RunExecutorPhaseScopedWithVerification(cfg, plan, requiredEditPaths, allowedEditPaths, nil, jsonEvents, stdout, progress...)
}

func RunExecutorPhaseScopedWithVerification(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationCommands []string, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	plan = workspace.NormalizeLoadedPlanArtifact(plan)

	verificationPlan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot:    cfg.Workspace.Root,
		ExplicitCommands: verificationCommands,
		ChangedPaths:     planChangedPaths(plan),
	})

	return runExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, nil, false, nil, verificationPlan, jsonEvents, stdout, progress...)
}

func RunExecutorPhaseScopedWithVerificationPlan(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationPlan VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	plan = workspace.NormalizeLoadedPlanArtifact(plan)
	return runExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, nil, false, nil, verificationPlan, jsonEvents, stdout, progress...)
}

func RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, writeAllowedPaths []string, writeAllowedExplicit bool, verificationPlan VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	plan = workspace.NormalizeLoadedPlanArtifact(plan)
	return runExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, writeAllowedPaths, writeAllowedExplicit, nil, verificationPlan, jsonEvents, stdout, progress...)
}

func RunExecutorPhaseScopedWithVerificationPlanAndWriteScopeAuthorizedNewFiles(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, writeAllowedPaths []string, writeAllowedExplicit bool, authorizedNewFiles []string, verificationPlan VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	plan = workspace.NormalizeLoadedPlanArtifact(plan)
	return runExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, writeAllowedPaths, writeAllowedExplicit, authorizedNewFiles, verificationPlan, jsonEvents, stdout, progress...)
}

func runExecutorPhaseScopedWithVerificationPlan(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, writeAllowedPaths []string, writeAllowedExplicit bool, authorizedNewFiles []string, verificationPlan VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...EditProgressFunc) (ExecutorPhaseReport, error) {
	report := ExecutorPhaseReport{Total: len(plan.Edits)}

	report.VerificationCommands = append([]VerificationCommand(nil), verificationPlan.Commands...)
	report.VerificationSkips = append([]VerificationSkip(nil), verificationPlan.Skips...)
	report.VerificationRejects = append([]VerificationReject(nil), verificationPlan.Rejects...)

	if len(plan.Edits) == 0 {
		return report, fmt.Errorf("plan has no edits")
	}

	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)

	ctx := context.Background()
	handoff := classifyExecutorHandoffPacket(cfg.Workspace.Root, plan)

	report.Handoff = handoff
	emitExecutorHandoffPacketEvents(stdout, jsonEvents, handoff)

	if mismatch := preflightPlanApprovalAndScope(plan, requiredEditPaths); mismatch != nil {
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

	if mismatch := preflightPlanWriteAllowedPaths(writeAllowedPaths, writeAllowedExplicit, plan); mismatch != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "preflight_failed", map[string]any{
			"edit_index": mismatch.Index,
			"path":       mismatch.Path,
			"reason":     mismatch.Reason,
		})

		if !jsonEvents {
			fmt.Fprintf(stdout, "  preflight FAILED on write_allowed_paths %s: %s\n", mismatch.Path, mismatch.Reason)
		}

		return report, fmt.Errorf("plan write_allowed_paths preflight failed (%s): %s", mismatch.Path, mismatch.Reason)
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
		} else if edit.InsertAfterLine > 0 {
			updated, err := projectedInsertAfterLineContent(cfg.Workspace.Root, edit)
			if err != nil {
				result := workspace.ToolResult{Tool: workspace.ToolNameSubmitPlan, Summary: "Executor insert preflight failed", Error: err.Error()}
				status := "failed"
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

			req = workspace.ToolRequest{
				Tool:        "write_file",
				Path:        edit.Path,
				Content:     updated,
				Description: firstNonEmptyString(edit.Purpose, "executor phase: insert-after-line projection"),
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

	verificationChecked, verificationEvidence, verificationErr := runPlanExecuteVerificationPlan(cfg.Workspace.Root, verificationPlan, jsonEvents, stdout)

	report.Verification = verificationEvidence

	if verificationErr != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
			"total":                    report.Total,
			"succeeded":                report.Succeeded,
			"failed":                   report.Failed,
			"source_sanity_checked":    sourceSanityChecked,
			"source_sanity_status":     "passed",
			"verification_checked":     verificationChecked,
			"verification_status":      "failed",
			"verification_command_cnt": len(verificationEvidence),
			"duration_ms":              time.Since(executorStartedAt).Milliseconds(),
		})

		return report, verificationErr
	}

	emitPlanExecuteJSONEvent(stdout, jsonEvents, "executor_complete", map[string]any{
		"total":                 report.Total,
		"succeeded":             report.Succeeded,
		"failed":                report.Failed,
		"source_sanity_checked": sourceSanityChecked,
		"source_sanity_status":  "passed",
		"verification_checked":  verificationChecked,
		"verification_status":   VerificationStatus(verificationEvidence),
		"duration_ms":           time.Since(executorStartedAt).Milliseconds(),
	})

	return report, nil
}

func RunVerificationPlan(root string, plan VerificationPlan, jsonEvents bool, stdout io.Writer) (int, []PhaseEvidenceCheck, error) {
	return runPlanExecuteVerificationPlan(root, plan, jsonEvents, stdout)
}

func runPlanExecuteVerificationPlan(root string, plan VerificationPlan, jsonEvents bool, stdout io.Writer) (int, []PhaseEvidenceCheck, error) {
	return runPlanExecuteVerificationPlanWithParentContract(root, plan, "", jsonEvents, stdout)
}

func runPlanExecuteVerificationPlanWithParentContract(root string, plan VerificationPlan, parentContractID string, jsonEvents bool, stdout io.Writer) (int, []PhaseEvidenceCheck, error) {
	evidence := make([]PhaseEvidenceCheck, 0, len(plan.Commands)+len(plan.Rejects))
	if len(plan.Rejects) > 0 {
		reject := plan.Rejects[0]
		check := PhaseEvidenceCheck{
			Kind:    "verification",
			Command: strings.TrimSpace(reject.Command),
			Status:  "failed",
			Error:   strings.TrimSpace(reject.Reason),
			Summary: "requested verification command was rejected before execution",
		}

		evidence = append(evidence, check)

		event := map[string]any{
			"command": check.Command,
			"status":  check.Status,
			"summary": check.Summary,
			"error":   check.Error,
		}

		if strings.TrimSpace(reject.Source) != "" {
			event["source"] = strings.TrimSpace(reject.Source)
		}

		emitPlanExecuteJSONEvent(stdout, jsonEvents, "verification", event)
		emitPlanExecuteVerificationAcceptance(stdout, jsonEvents, planExecuteVerificationAcceptanceInput{
			Root:             root,
			ParentContractID: parentContractID,
			Check:            check,
			Decision:         autonomy.VerificationAcceptanceRejected,
			ReasonCode:       "command_rejected",
			Source:           reject.Source,
		})

		if !jsonEvents {
			fmt.Fprintf(stdout, "  verification %s ... failed\n", check.Command)
		}

		return len(evidence), evidence, fmt.Errorf("verification rejected for %q: %s", check.Command, firstNonEmptyString(check.Error, check.Summary))
	}

	for _, command := range plan.Commands {
		check := runPlanExecuteVerificationCommand(root, command)

		evidence = append(evidence, check)

		event := map[string]any{
			"command": command.Command,
			"status":  check.Status,
			"summary": check.Summary,
			"error":   check.Error,
		}

		if check.WorkingDir != "" {
			event["working_dir"] = check.WorkingDir
		}

		if command.Source != "" {
			event["source"] = command.Source
		}

		emitPlanExecuteJSONEvent(stdout, jsonEvents, "verification", event)
		emitPlanExecuteVerificationAcceptance(stdout, jsonEvents, planExecuteVerificationAcceptanceInput{
			Root:             root,
			ParentContractID: parentContractID,
			Check:            check,
			Command:          command,
			Decision:         planExecuteVerificationAcceptanceDecision(check),
			ReasonCode:       planExecuteVerificationAcceptanceReason(check),
			Source:           command.Source,
		})

		if !jsonEvents {
			location := ""
			if check.WorkingDir != "" {
				location = " (dir " + check.WorkingDir + ")"
			}

			fmt.Fprintf(stdout, "  verification %s%s ... %s\n", command.Command, location, check.Status)
		}

		if check.Status != "ok" && check.Status != "passed" {
			return len(evidence), evidence, fmt.Errorf("verification failed for %q: %s", command.Command, firstNonEmptyString(check.Error, check.Summary))
		}
	}

	return len(evidence), evidence, nil
}

type planExecuteVerificationAcceptanceInput struct {
	Root             string
	ParentContractID string
	Check            PhaseEvidenceCheck
	Command          VerificationCommand
	Decision         autonomy.VerificationAcceptanceDecision
	ReasonCode       string
	Source           string
}

func emitPlanExecuteVerificationAcceptance(stdout io.Writer, jsonEvents bool, input planExecuteVerificationAcceptanceInput) {
	if !jsonEvents {
		return
	}

	record := planExecuteVerificationAcceptanceRecord(input)
	emitPlanExecuteAutonomyRecord(stdout, jsonEvents, "verification_acceptance", &record)
}

type planExecuteAutonomyRecord interface {
	Normalize()
	Validate() error
}

func emitPlanExecuteAutonomyRecord(stdout io.Writer, jsonEvents bool, eventType string, record planExecuteAutonomyRecord) {
	if !jsonEvents || record == nil {
		return
	}

	record.Normalize()
	if err := record.Validate(); err != nil {
		emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType+"_invalid", map[string]any{
			"error": err.Error(),
		})
		return
	}

	fields := map[string]any{}
	body, err := json.Marshal(record)
	if err != nil {
		return
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return
	}

	emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, fields)
}

func planExecuteVerificationAcceptanceRecord(input planExecuteVerificationAcceptanceInput) autonomy.VerificationAcceptanceRecord {
	command := strings.TrimSpace(firstNonEmptyString(input.Check.Command, input.Command.Command, "verification"))
	resultText := strings.Join([]string{
		input.Check.Kind,
		input.Check.Command,
		input.Check.WorkingDir,
		input.Check.Status,
		input.Check.Summary,
		input.Check.Error,
	}, "\x00")
	commandRef := "command:" + planExecuteShortHash(command)
	resultRef := "verification_result:" + planExecuteShortHash(resultText)
	headSHA := planExecuteGitSHAOrUnknown(input.Root)
	parentContractID := strings.TrimSpace(input.ParentContractID)
	if parentContractID == "" {
		parentContractID = "plan_execute_verification:" + planExecuteShortHash(input.Root)
	}

	return autonomy.VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verification_acceptance_" + planExecuteShortHash(parentContractID, commandRef, resultRef, string(input.Decision)),
		ParentContractID:         parentContractID,
		ObligationRefs:           planExecuteVerificationObligationRefs(input),
		CommandRef:               commandRef,
		CommandFingerprint:       "sha256:" + planExecuteFullHash(command),
		WorkspaceRef:             "workspace:" + planExecuteShortHash(input.Root),
		BaseSHA:                  headSHA,
		HeadSHA:                  headSHA,
		ExitStatus:               planExecuteVerificationExitStatus(input.Check),
		ResultRef:                resultRef,
		ScopeRefs:                planExecuteVerificationScopeRefs(input),
		Decision:                 input.Decision,
		ReasonCode:               strings.TrimSpace(firstNonEmptyString(input.ReasonCode, "verification_recorded")),
	}
}

func planExecuteVerificationAcceptanceDecision(check PhaseEvidenceCheck) autonomy.VerificationAcceptanceDecision {
	switch strings.TrimSpace(strings.ToLower(check.Status)) {
	case "ok", "passed":
		return autonomy.VerificationAcceptanceAccepted
	default:
		return autonomy.VerificationAcceptanceRepairRequired
	}
}

func planExecuteVerificationAcceptanceReason(check PhaseEvidenceCheck) string {
	switch strings.TrimSpace(strings.ToLower(check.Status)) {
	case "ok", "passed":
		return "verification_passed"
	default:
		return "verification_failed"
	}
}

func planExecuteVerificationExitStatus(check PhaseEvidenceCheck) int {
	switch strings.TrimSpace(strings.ToLower(check.Status)) {
	case "ok", "passed":
		return 0
	default:
		return 1
	}
}

func planExecuteVerificationObligationRefs(input planExecuteVerificationAcceptanceInput) []string {
	source := strings.TrimSpace(firstNonEmptyString(input.Source, input.Command.Source, "planner_owned_verification"))
	return []string{"obligation:" + normalizePlanExecuteRefToken(source)}
}

func planExecuteVerificationScopeRefs(input planExecuteVerificationAcceptanceInput) []string {
	refs := make([]string, 0, 3)
	for _, value := range []string{input.Command.Path, input.Check.Path, input.Check.WorkingDir} {
		value = normalizePlanExecutePath(value)
		if value != "" && value != "." {
			refs = append(refs, "scope:"+value)
		}
	}
	return planExecuteUniqueRefStrings(refs)
}

func planExecuteUniqueRefStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func planExecuteGitSHAOrUnknown(root string) string {
	out := strings.TrimSpace(runGitEvidence(root, "rev-parse", "HEAD"))
	if len(out) >= 7 && !strings.ContainsAny(out, " \t\r\n:") {
		return out
	}
	return "unknown"
}

func normalizePlanExecuteRefToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	return strings.Trim(strings.TrimSpace(b.String()), "_")
}

func planExecuteShortHash(values ...string) string {
	return planExecuteFullHash(values...)[:16]
}

func planExecuteFullHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func runPlanExecuteVerificationCommand(root string, command VerificationCommand) PhaseEvidenceCheck {
	command.Command = strings.TrimSpace(command.Command)

	args := normalizeVerificationArgv(command.Argv)

	if len(args) == 0 {
		parsed, ok := VerificationCommandArgs(command.Command)
		if !ok {
			args = nil
		} else {
			args = parsed
		}
	}

	check := PhaseEvidenceCheck{
		Kind:       "verification",
		Command:    command.Command,
		WorkingDir: command.WorkingDir,
		Status:     "failed",
	}
	if len(args) == 0 || !VerificationArgvAllowed(args) {
		check.Error = "unsupported verification command"
		return check
	}

	cmd := exec.Command(args[0], args[1:]...)

	cmd.Dir = strings.TrimSpace(root)

	if cmd.Dir == "" {
		cmd.Dir = "."
	}

	if command.WorkingDir != "" {
		workingDir, ok := cleanVerificationWorkingDir(command.WorkingDir)
		if !ok {
			check.Error = "unsafe verification working_dir"
			return check
		}

		cmd.Dir = filepath.Join(cmd.Dir, filepath.FromSlash(workingDir))
	}

	out, err := cmd.CombinedOutput()
	output := truncatePlanExecuteText(strings.TrimSpace(string(out)), 12000)

	if err != nil {
		check.Error = firstNonEmptyString(output, err.Error())
		check.Summary = err.Error()

		return check
	}

	check.Status = "ok"
	check.Summary = output

	return check
}

func VerificationStatus(evidence []PhaseEvidenceCheck) string {
	if len(evidence) == 0 {
		return "not_applicable"
	}

	for _, check := range evidence {
		if check.Status != "ok" && check.Status != "passed" {
			return "failed"
		}
	}

	return "passed"
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

		result := mgr.Execute(ctx, workspace.ToolRequest{Tool: workspace.ToolNameSourceSanity, Path: path})
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

func preflightPlanApprovalAndScope(plan workspace.PlanArtifact, requiredEditPaths []string) *PlanEditPreflightMismatch {
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

	return preflightPlanDeclaredScope(plan, requiredEditPaths)
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

func preflightPlanDeclaredScope(plan workspace.PlanArtifact, originalRequiredPaths ...[]string) *PlanEditPreflightMismatch {
	forbidden := planExecutePathSet(append(append([]string(nil), plan.Paths.Forbidden...), plan.Contract.ForbiddenPaths...))
	required := planExecutePathSet(append(append([]string(nil), plan.Paths.Required...), plan.Contract.RequiredEditPaths...))

	for _, paths := range originalRequiredPaths {
		for _, path := range paths {
			if normalized := normalizePlanExecutePath(path); normalized != "" {
				required[normalized] = struct{}{}
			}
		}
	}

	planned := planExecutePathSet(append(append([]string(nil), plan.Paths.Editable...), plan.Paths.Changed...))
	approved := planExecutePathSet(plan.Approval.ApprovedPaths)
	pending := planExecutePathSet(plan.Approval.PendingPaths)
	sourceSatisfiedViolations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        planExecuteSetStrings(required),
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             workspace.PlanArtifactSourceSatisfiedEvidenceRefs(plan),
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	})

	if mismatch := preflightPlanSourceSatisfiedViolationMismatch(sourceSatisfiedViolations); mismatch != nil {
		if mismatch.Reason == "source-satisfied path is outside declared required scope" {
			mismatch.Reason = "source-satisfied path is outside required plan scope"
		}

		return mismatch
	}

	seen := map[string]struct{}{}

	for path := range planExecutePathSet(plan.Paths.SourceSatisfied) {
		if _, ok := forbidden[path]; ok {
			return &PlanEditPreflightMismatch{Index: -1, Path: path, Reason: "source-satisfied path is forbidden"}
		}

		seen[path] = struct{}{}
	}

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

func planExecuteSetStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}

	sort.Strings(out)

	return out
}

func preflightPlanSourceSatisfiedPaths(input plancontract.SourceSatisfiedPathCoverageInput) *PlanEditPreflightMismatch {
	return preflightPlanSourceSatisfiedViolationMismatch(plancontract.ValidateSourceSatisfiedPathCoverage(input))
}

func preflightPlanSourceSatisfiedViolationMismatch(violations []plancontract.SourceSatisfiedPathViolation) *PlanEditPreflightMismatch {
	if len(violations) == 0 {
		return nil
	}

	violation := violations[0]

	return &PlanEditPreflightMismatch{
		Index:  -1,
		Path:   normalizePlanExecutePath(violation.Path),
		Reason: violation.Reason,
	}
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
		"edit_index":                mismatch.Index,
		"reason":                    mismatch.Reason,
		"packet_state":              handoff.State,
		"handoff_id":                handoff.HandoffID,
		"approval_scope_id":         handoff.ApprovalScopeID,
		"handoff_created":           handoff.HandoffCreated,
		"packet_present":            handoff.PacketPresent,
		"packet_not_used_by_design": handoff.PacketNotUsedByDesign,
		"packet_freshness_state":    firstNonEmptyString(handoff.PacketFreshnessState, ExecutorPacketFreshnessUnknown),
	}
	if path := sanitizePlanExecuteEventPath(mismatch.Path); path != "" {
		fields["path"] = path
	}

	return fields
}

func PlanScopeDriftEventFields(handoff ExecutorHandoffPacket, mismatch *PlanEditPreflightMismatch) map[string]any {
	return planScopeDriftEventFields(handoff, mismatch)
}

func preflightPlanRequiredEditPaths(required []string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths: required,
		Source:            "planexecute.required_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.RequiredCoveragePaths) == 0 {
		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	if mismatch := preflightPlanSourceSatisfiedPaths(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        coverage.RequiredCoveragePaths,
		SourceSatisfiedPaths: plan.Paths.SourceSatisfied,
		Evidence:             workspace.PlanArtifactSourceSatisfiedEvidenceRefs(plan),
		ApprovedPaths:        plan.Approval.ApprovedPaths,
		PendingPaths:         plan.Approval.PendingPaths,
		RequireEvidence:      true,
	}); mismatch != nil {
		if mismatch.Reason == "source-satisfied path is outside declared required scope" {
			mismatch.Reason = "source-satisfied path is outside required_edit_paths allowlist"
		}

		return mismatch
	}

	candidatePaths = append(candidatePaths, plan.Paths.SourceSatisfied...)

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      coverage.RequiredCoveragePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.required_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if !coverageByPath[path].SatisfiesRequiredCoverage {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside required_edit_paths allowlist",
			}
		}
	}

	for _, path := range coverage.MissingRequiredCoveragePaths {
		return &PlanEditPreflightMismatch{
			Index:  -1,
			Path:   path,
			Reason: "plan does not include an edit for this required_edit_paths entry",
		}
	}

	return nil
}

func preflightPlanMissingPathAuthorization(root string, authorizedNewFiles []string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	authorized := map[string]struct{}{}
	for _, path := range mergePlanExecutePaths(authorizedNewFiles) {
		authorized[path] = struct{}{}
	}

	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			continue
		}

		if _, ok := authorized[path]; ok {
			continue
		}

		abs := filepath.Join(root, filepath.FromSlash(path))
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				return &PlanEditPreflightMismatch{
					Index:  i,
					Path:   path,
					Reason: "plan would create missing edit path without new-file authorization",
				}
			}

			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "could not stat edit path before write: " + err.Error(),
			}
		}
	}

	return nil
}

func preflightPlanWriteAllowedPaths(writeAllowed []string, explicit bool, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths: writeAllowed,
		Source:                "planexecute.write_scope_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.AllowedCandidatePaths) == 0 {
		if explicit && len(plan.Edits) > 0 {
			return &PlanEditPreflightMismatch{
				Index:  0,
				Path:   normalizePlanExecutePath(plan.Edits[0].Path),
				Reason: "edit path is outside explicit empty write_allowed_paths envelope",
			}
		}

		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths:  coverage.AllowedCandidatePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.write_scope_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if !coverageByPath[path].WithinAllowedCandidatePath {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside write_allowed_paths envelope",
			}
		}
	}

	return nil
}

func preflightPlanAllowedEditPaths(allowed []string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths: allowed,
		Source:                "planexecute.allowed_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	if len(coverage.AllowedCandidatePaths) == 0 {
		return nil
	}

	candidatePaths, invalid := preflightPlanCandidatePaths(plan)
	if invalid != nil {
		return invalid
	}

	coverage, err = autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		AllowedCandidatePaths:  coverage.AllowedCandidatePaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "planexecute.allowed_preflight",
	})
	if err != nil {
		return &PlanEditPreflightMismatch{Index: -1, Reason: err.Error()}
	}

	coverageByPath := planScopeCandidateCoverageByPath(coverage.CandidateCoverage)

	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if !coverageByPath[path].WithinAllowedCandidatePath {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "plan edit path is outside allowed_edit_paths allowlist",
			}
		}
	}

	return nil
}

func preflightPlanCandidatePaths(plan workspace.PlanArtifact) ([]string, *PlanEditPreflightMismatch) {
	var paths []string

	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if path == "" {
			return nil, &PlanEditPreflightMismatch{Index: i, Reason: "edit has no path"}
		}

		paths = append(paths, path)
	}

	return paths, nil
}

func planScopeCandidateCoverageByPath(coverage []autonomy.PlanScopePathRoleCoverage) map[string]autonomy.PlanScopePathRoleCoverage {
	out := make(map[string]autonomy.PlanScopePathRoleCoverage, len(coverage))
	for _, item := range coverage {
		out[item.Path] = item
	}

	return out
}

func preflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	for i, edit := range plan.Edits {
		path := normalizePlanExecutePath(edit.Path)
		if reason := workspace.PlanEditUnsupportedProofClaimReason(edit); reason != "" {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: reason}
		}
	}

	return nil
}

func normalizePlanExecutePath(path string) string {
	return plancontract.NormalizeScopePath(path)
}

func preflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *PlanEditPreflightMismatch {
	cache := make(map[string][]byte)

	for i, edit := range plan.Edits {
		oldText := strings.TrimSpace(edit.OldText)
		if !workspace.PlanEditNeedsAnchorProvenance(edit) {
			continue
		}

		rawPath := strings.TrimSpace(edit.Path)
		path := normalizePlanExecutePath(rawPath)

		if path == "" {
			reason := "edit has no path"
			if rawPath != "" {
				reason = "edit has invalid path"
			}

			return &PlanEditPreflightMismatch{Index: i, Path: rawPath, Reason: reason}
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
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

		if workspace.PlanArtifactRequiresSubmitPlanAnchorProvenance(plan) && edit.AnchorProvenance == nil {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "current-schema plan edit is missing submit_plan anchor provenance",
			}
		}

		if err := workspace.ValidatePlanEditAnchorProvenance(workspaceRoot, edit); err != nil {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: err.Error(),
			}
		}

		if oldText == "" {
			continue
		}

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
		rawPath := strings.TrimSpace(edit.Path)
		path := normalizePlanExecutePath(rawPath)

		if path == "" {
			reason := "edit has no path"
			if rawPath != "" {
				reason = "edit has invalid path"
			}

			return &PlanEditPreflightMismatch{Index: i, Path: rawPath, Reason: reason}
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
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

func projectedInsertAfterLineContent(workspaceRoot string, edit workspace.PlanEdit) (string, error) {
	path := normalizePlanExecutePath(edit.Path)
	if path == "" {
		return "", fmt.Errorf("edit has invalid path")
	}

	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("insert_after_line could not read existing %s: %w", path, err)
	}

	updated, changed, err := workspace.PreviewPlanEditContent(string(body), edit)
	if err != nil {
		return "", err
	}

	if !changed {
		return "", fmt.Errorf("insert_after_line would not change %s", path)
	}

	return updated, nil
}

func executorEditOutcome(result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "FAILED: " + result.Error
	}

	return "ok"
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

func emitPlanExecuteSyntheticTurnSummary(stdout io.Writer, jsonEvents bool, stopReason string) {
	if !jsonEvents {
		return
	}

	payload := map[string]any{
		"type":                       "turn_summary",
		"ts":                         time.Now().UTC().Format(time.RFC3339Nano),
		"lane":                       "plan-execute",
		"used_model_calls":           0,
		"max_model_calls":            0,
		"used_tool_results":          0,
		"max_tool_steps":             0,
		"prompt_tokens":              0,
		"completion_tokens":          0,
		"total_tokens":               0,
		"progress_turns":             0,
		"rejected_actions":           0,
		"completion_gate_rejections": 0,
		"context_compactions":        0,
		"duration_ms":                0,
		"stop_reason":                strings.TrimSpace(stopReason),
		"budget_exhausted":           false,
	}

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
	toolcmd.LogCLIPhase(rec, eventType, fields)
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
		Verification:     append([]PhaseEvidenceCheck(nil), executorReport.Verification...),
		PatchHygiene:     patchHygiene,
		SuggestedTests:   packetSuggestedTests(packet),
		UnresolvedGaps:   append([]string(nil), packet.UnresolvedGaps...),
		OpenObligations:  phaseEvidenceOpenObligations(plan, packet, changedPaths, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene),
		DecisionEvidence: phaseEvidenceDecisionSummary(packet, submittedPlan, sourceSanity, executorReport.Verification, patchHygiene),
	}

	return bundle
}

func phaseEvidenceHandoffFromExecutor(handoff ExecutorHandoffPacket) phaseEvidenceHandoff {
	return phaseEvidenceHandoff{
		PacketState:      firstNonEmptyString(strings.TrimSpace(handoff.State), ExecutorHandoffStateMissing),
		Reason:           strings.TrimSpace(handoff.Reason),
		HandoffCreated:   handoff.HandoffCreated,
		PacketPresent:    handoff.PacketPresent,
		PacketNotUsed:    handoff.PacketNotUsedByDesign,
		PacketFreshness:  firstNonEmptyString(strings.TrimSpace(handoff.PacketFreshnessState), ExecutorPacketFreshnessUnknown),
		PlanID:           strings.TrimSpace(handoff.PlanID),
		HandoffID:        strings.TrimSpace(handoff.HandoffID),
		HandoffStatus:    strings.TrimSpace(handoff.HandoffStatus),
		PackID:           strings.TrimSpace(handoff.PackID),
		DeclaredPackID:   strings.TrimSpace(handoff.DeclaredPackID),
		IndexID:          strings.TrimSpace(handoff.IndexID),
		Fresh:            handoff.Fresh,
		Quality:          handoff.Quality,
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
	proj := comprehension.NewCompactPackProjection(view)
	packet := PhaseEvidencePacket{
		PackID:           strings.TrimSpace(view.PackID),
		Status:           "available",
		IndexID:          strings.TrimSpace(view.IndexID),
		Fresh:            view.Fresh,
		Confidence:       strings.TrimSpace(view.Confidence.Level),
		Quality:          proj.Quality,
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

func phaseEvidenceOpenObligations(plan workspace.PlanArtifact, packet PhaseEvidencePacket, changedPaths []string, submittedPlan []PhaseEvidenceCheck, sourceSanity []PhaseEvidenceCheck, verification []PhaseEvidenceCheck, patchHygiene PhaseEvidenceCheck) []string {
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

	for _, check := range verification {
		status := firstNonEmptyString(strings.TrimSpace(check.Status), "unknown")
		if status != "ok" && status != "passed" {
			out = append(out, "verification_"+status+":"+firstNonEmptyString(strings.TrimSpace(check.Command), "(unknown)"))
		}
	}

	return cappedStrings(out, 16)
}

func phaseEvidenceDecisionSummary(packet PhaseEvidencePacket, submittedPlan []PhaseEvidenceCheck, sourceSanity []PhaseEvidenceCheck, verification []PhaseEvidenceCheck, patchHygiene PhaseEvidenceCheck) []phaseEvidenceSummary {
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

	verificationStatus := "not_applicable"

	if len(verification) > 0 {
		verificationStatus = "passed"

		for _, check := range verification {
			if check.Status != "ok" && check.Status != "passed" {
				verificationStatus = "failed"
				break
			}
		}
	}

	out = append(out, phaseEvidenceSummary{Kind: "verification", Status: verificationStatus, Summary: fmt.Sprintf("%d command(s)", len(verification))})

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
	fmt.Fprintf(&b, "allowed_repair_paths: %s\n", formatInlineList(bundle.AllowedPaths))
	fmt.Fprintf(&b, "edit_set_obligations: %s\n\n", formatInlineList(bundle.EditSetObligations.Kinds()))
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

	if bundle.Packet.Quality.LocalizationConfidence != "" {
		fmt.Fprintf(&b, "packet_quality: localization_confidence=%s source_owner_found=%t\n",
			bundle.Packet.Quality.LocalizationConfidence,
			bundle.Packet.Quality.SourceOwnerFound)
	}

	fmt.Fprintf(&b, "executor_handoff_packet: state=%s reason=%s handoff_created=%t packet_present=%t packet_not_used_by_design=%t packet_freshness_state=%s",
		bundle.ExecutorHandoff.PacketState,
		bundle.ExecutorHandoff.Reason,
		bundle.ExecutorHandoff.HandoffCreated,
		bundle.ExecutorHandoff.PacketPresent,
		bundle.ExecutorHandoff.PacketNotUsed,
		bundle.ExecutorHandoff.PacketFreshness,
	)

	if bundle.ExecutorHandoff.PackID != "" {
		fmt.Fprintf(&b, " pack_id=%s", bundle.ExecutorHandoff.PackID)
	}

	if bundle.ExecutorHandoff.IndexID != "" {
		fmt.Fprintf(&b, " index_id=%s fresh=%t", bundle.ExecutorHandoff.IndexID, bundle.ExecutorHandoff.Fresh)
	}

	b.WriteString("\n")

	if bundle.ExecutorHandoff.Quality.LocalizationConfidence != "" {
		fmt.Fprintf(&b, "executor_handoff_quality: localization_confidence=%s source_owner_found=%t\n",
			bundle.ExecutorHandoff.Quality.LocalizationConfidence,
			bundle.ExecutorHandoff.Quality.SourceOwnerFound)
	}

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

	if len(bundle.Verification) > 0 {
		b.WriteString("verification:\n")

		for _, check := range bundle.Verification {
			workingDir := ""
			if strings.TrimSpace(check.WorkingDir) != "" {
				workingDir = " working_dir=" + strings.TrimSpace(check.WorkingDir)
			}

			fmt.Fprintf(&b, "- `%s`%s status=%s summary=%s error=%s\n", check.Command, workingDir, check.Status, check.Summary, check.Error)
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
		"schema_version":            bundle.SchemaVersion,
		"phase":                     bundle.Phase,
		"changed_paths":             cappedStrings(bundle.ChangedPaths, 16),
		"required_paths":            cappedStrings(bundle.RequiredPaths, 16),
		"allowed_paths":             cappedStrings(bundle.AllowedPaths, 16),
		"edit_set_obligations":      bundle.EditSetObligations.Kinds(),
		"edit_set_obligation_count": len(bundle.EditSetObligations.Normalized()),
		"packet_status":             bundle.Packet.Status,
		"pack_id":                   bundle.Packet.PackID,
		"executor_packet_state":     bundle.ExecutorHandoff.PacketState,
		"executor_packet_reason":    bundle.ExecutorHandoff.Reason,
		"executor_handoff_id":       bundle.ExecutorHandoff.HandoffID,
		"handoff_created":           bundle.ExecutorHandoff.HandoffCreated,
		"packet_present":            bundle.ExecutorHandoff.PacketPresent,
		"packet_not_used_by_design": bundle.ExecutorHandoff.PacketNotUsed,
		"packet_freshness_state":    bundle.ExecutorHandoff.PacketFreshness,
		"candidate_files":           len(bundle.Packet.CandidateFiles),
		"candidate_file_paths":      phaseEvidenceTelemetryFilePaths(bundle.Packet.CandidateFiles, 8),
		"candidate_symbols":         len(bundle.Packet.CandidateSymbols),
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
		"verification_checks":   len(bundle.Verification),
		"verification":          phaseEvidenceTelemetryChecks(bundle.Verification, 8),
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
		if command := strings.TrimSpace(check.Command); command != "" {
			entry["command"] = phaseEvidenceTruncate(command, 240)
		}

		if workingDir := strings.TrimSpace(check.WorkingDir); workingDir != "" {
			entry["working_dir"] = phaseEvidenceTruncate(workingDir, 240)
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
