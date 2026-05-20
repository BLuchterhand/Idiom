package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// PlanThenExecutePlannerTaskFamily is the runtime-brief task_family
// that marks the planner phase of `idiom plan-execute`. The runner
// uses it to forbid per-edit write tools (edit_file, write_file,
// append_file, apply_patch, delegate_edit) — the planner's only
// writes go through submit_plan. The executor phase, which runs in a
// separate process via the orchestrator in cmd/idiom/plan_execute.go,
// uses the workspace dispatch directly.
const PlanThenExecutePlannerTaskFamily = "plan_then_execute_planner"

// PlanThenExecuteReviewRepairTaskFamily marks the post-executor
// action-capable review phase. It shares the submit_plan surface for
// corrective edits, but unlike the planner phase it may also close with a
// direct `Decision: pass` answer when the evidence checklist is satisfied.
const PlanThenExecuteReviewRepairTaskFamily = "plan_then_execute_review_repair"

// planThenExecutePlannerPhase reports whether the runner is currently
// servicing the planner half of plan-then-execute mode.
func (r *Runner) planThenExecutePlannerPhase() bool {
	if r == nil {
		return false
	}
	return planThenExecutePlannerBrief(r.RuntimeBrief)
}

func (r *Runner) planThenExecutePlanSubmissionPhase() bool {
	if r == nil {
		return false
	}
	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func planThenExecutePlannerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily
}

// preflightPlanThenExecuteWriteGuard rejects per-edit write tools
// during the planner phase of plan-then-execute mode and steers the
// model at submit_plan instead. The system addendum the orchestrator
// injects is a soft instruction; this guard is the hard one — even
// if the model ignores the prompt, the tool call is intercepted and
// converted into a directional rejection.
//
// Returns Intercepted=false (and an empty result) for any other lane
// shape, so the cost when the guard is inactive is a single string
// comparison.
func (r *Runner) preflightPlanThenExecuteWriteGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() {
		return verificationInterceptResult{}
	}
	tool := strings.TrimSpace(req.Tool)
	if !planThenExecuteForbiddenWriteTool(tool) {
		return verificationInterceptResult{}
	}
	directive := "plan-then-execute planner phase: per-edit write tools are unavailable. " +
		"Enumerate every change in a single submit_plan call and stop. " +
		"The orchestrator's executor phase will dispatch each edit deterministically after you submit the plan."
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected per-edit write",
			Error:   directive,
		},
		Reason: "plan-then-execute planner phase: write tool " + tool + " redirected to submit_plan",
	}
}

// preflightPlanThenExecuteSubmitPlanCoverageGuard validates the
// planner's structured plan against the same required_edit_paths
// contract that normal write tools obey. submit_plan is not a
// per-edit write, so the normal write-scope guard cannot see the
// paths it will later cause the executor to mutate. This guard keeps
// the invariant deterministic while still giving the planner a normal
// recoverable tool error it can fix with a new submit_plan call.
func (r *Runner) preflightPlanThenExecuteSubmitPlanCoverageGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}
	required := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)
	if len(required) == 0 {
		return verificationInterceptResult{}
	}
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	planned := make(map[string]struct{}, len(edits))
	var outside []string
	var missingPathEdits []string
	for i, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			missingPathEdits = append(missingPathEdits, planThenExecuteSubmitPlanEditName(i, edit))
			continue
		}
		planned[path] = struct{}{}
		if _, ok := required[path]; !ok {
			outside = append(outside, path)
		}
	}
	var missing []string
	for _, path := range r.RuntimeBrief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := planned[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}
	missingSnippets := r.missingSubmitPlanRequiredEditSnippets(req, planned)
	invalidAnchors := r.invalidSubmitPlanAnchors(req)
	if len(missing) == 0 && len(outside) == 0 && len(missingSnippets) == 0 && len(invalidAnchors) == 0 {
		return verificationInterceptResult{}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing required edit path(s): "+quotedRunnerStrings(missing))
	}
	if len(missingPathEdits) > 0 {
		parts = append(parts, "edit entry missing `path`: "+quotedRunnerStrings(missingPathEdits))
	}
	if len(outside) > 0 {
		parts = append(parts, "path(s) outside required_edit_paths: "+quotedRunnerStrings(outside))
	}
	if len(missingSnippets) > 0 {
		parts = append(parts, "missing required edit snippet(s): "+quotedRunnerStrings(missingSnippets))
	}
	if len(invalidAnchors) > 0 {
		parts = append(parts, "invalid edit anchor(s): "+quotedRunnerStrings(invalidAnchors))
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error: "submit_plan must include at least one edit entry for every required_edit_paths entry, no edits outside that scope, and coverage for every required_edit_snippets final-state snippet; " +
				strings.Join(parts, "; ") +
				". Submit a corrected plan with `edits` as a JSON array, not a quoted string, and include `path` on every edit. Edit bodies must be complete executable source text, not placeholders, ellipses, or prose; answer `Decision: source-satisfied` when current source already contains the requested final state, or answer with <<QUESTION>> and name the exact blocker for each skipped requirement.",
		},
		Reason: "plan-then-execute planner phase: submit_plan edits did not satisfy required path/snippet coverage",
	}
}

func planThenExecuteSubmitPlanEditName(index int, edit workspace.PlanEdit) string {
	if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
		if len(purpose) > 60 {
			purpose = strings.TrimSpace(purpose[:60]) + "..."
		}
		return "edits[" + strconv.Itoa(index) + "] " + purpose
	}
	return "edits[" + strconv.Itoa(index) + "]"
}

func (r *Runner) invalidSubmitPlanAnchors(req workspace.ToolRequest) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}
	var invalid []string
	contentCache := make(map[string]string)
	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		path := normalizeRunnerPath(edit.Path)
		if path == "" || filepath.IsAbs(path) {
			continue
		}
		content, ok := contentCache[path]
		if !ok {
			content = readPlanProjectionBase(root, path)
			contentCache[path] = content
		}
		if edit.OldText != "" {
			if !strings.Contains(content, edit.OldText) {
				invalid = append(invalid, path+": old_text not found in current source")
			}
			continue
		}
		if edit.StartLine > 0 || edit.EndLine > 0 {
			if _, ok := applyPlanProjectionRange(content, edit.StartLine, edit.EndLine, edit.Content); !ok {
				invalid = append(invalid, path+": line range outside current source")
			}
		}
	}
	return invalid
}

func (r *Runner) missingSubmitPlanRequiredEditSnippets(req workspace.ToolRequest, planned map[string]struct{}) []string {
	if r == nil {
		return nil
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}
	root := ""
	if r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}
	projectedContent := projectedPlanContentByPath(root, planThenExecuteEffectiveSubmitPlanEdits(req))
	var missing []string
	for i, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, planned)
		if len(candidates) == 0 {
			candidates = mapKeys(planned)
		}
		if planThenExecuteRequiredSnippetCovered(candidates, snippet.Content, projectedContent) {
			continue
		}
		missing = append(missing, requiredEditSnippetName(snippet, i))
	}
	return missing
}

func projectedPlanContentByPath(root string, edits []workspace.PlanEdit) map[string]string {
	out := make(map[string]string, len(edits))
	for _, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}
		content, loaded := out[path]
		if !loaded {
			content = readPlanProjectionBase(root, path)
		}
		out[path] = applyPlanProjectionEdit(content, edit)
	}
	return out
}

func planThenExecuteEffectiveSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]workspace.PlanEdit(nil), req.PlanArtifact.Edits...)
	}
	return append([]workspace.PlanEdit(nil), req.Edits...)
}

func readPlanProjectionBase(root string, path string) string {
	if strings.TrimSpace(root) == "" || filepath.IsAbs(path) {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	return string(body)
}

func applyPlanProjectionEdit(current string, edit workspace.PlanEdit) string {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}
	if strings.TrimSpace(edit.OldText) != "" {
		if strings.Contains(current, edit.OldText) {
			return strings.Replace(current, edit.OldText, edit.NewText, 1)
		}
		return current + "\n" + edit.NewText
	}
	if edit.StartLine > 0 || edit.EndLine > 0 {
		if projected, ok := applyPlanProjectionRange(current, edit.StartLine, edit.EndLine, edit.Content); ok {
			return projected
		}
		return current + "\n" + edit.Content
	}
	if strings.TrimSpace(edit.NewText) != "" {
		return current + "\n" + edit.NewText
	}
	if strings.TrimSpace(edit.Content) != "" {
		return current + "\n" + edit.Content
	}
	return current
}

func applyPlanProjectionRange(current string, startLine int, endLine int, replacement string) (string, bool) {
	if startLine < 1 || endLine < startLine {
		return "", false
	}
	lines := strings.SplitAfter(current, "\n")
	if current == "" {
		lines = nil
	}
	if endLine > len(lines) {
		return "", false
	}
	start := startLine - 1
	before := strings.Join(lines[:start], "")
	after := strings.Join(lines[endLine:], "")
	if replacement != "" && !strings.HasSuffix(replacement, "\n") && after != "" {
		replacement += "\n"
	}
	return before + replacement + after, true
}

func planThenExecuteRequiredSnippetCovered(candidates []string, snippet string, projectedContent map[string]string) bool {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return true
	}
	for _, candidate := range candidates {
		path := normalizeRunnerPath(candidate)
		if path == "" {
			continue
		}
		if editSnippetMatchesContent(projectedContent[path], snippet) {
			return true
		}
	}
	return false
}

func planThenExecuteRequiredPathDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", "", false
	}
	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) == "submit_plan" || !isReadClassTool(req) {
		return "", "", false
	}
	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	if len(unread) == 0 {
		return "", "", false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok && target != "" {
		invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
		required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
		if _, invalidAnchor := invalidAnchorPaths[target]; invalidAnchor {
			if _, inRequiredScope := required[target]; !inRequiredScope {
				next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
				nextInstruction := planThenExecuteSourceReadInstruction(next, results)
				return "Required edit path source read required",
					"the previous submit_plan named an invalid edit anchor outside required_edit_paths. Do not refresh out-of-scope anchors; " + nextInstruction + ". Then submit_plan, source-satisfied, or <<QUESTION>> with the exact blocker.",
					true
			}
			if submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
				planThenExecuteSourceBodyReadRequest(req) &&
				!successfulReadPathAfter(results, rejectedAt, target) {
				return "", "", false
			}
			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			nextInstruction := planThenExecuteSourceReadInstruction(next, results)
			return "Bounded submit_plan anchor refresh required",
				"the previous submit_plan named an invalid edit anchor on " + target + ". Refresh that anchor with a source-body request such as bounded `read_file` with `start_line`/`end_line` or `code_navigate` `read_symbol`, then submit a corrected plan. Do not use whole-file `read_file` for this refresh because it can redirect to an outline and still leave no edit anchor. Remaining unread required path also needs source evidence: " + nextInstruction + ".",
				true
		}
	}
	if target != "" {
		if _, ok := required[target]; ok {
			if _, missing := unread[target]; missing {
				return "", "", false
			}
			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			return "Required edit path source read required",
				"this required path already has source evidence. Before more discovery on " + target + ", " + planThenExecuteSourceReadInstruction(next, results) + ". Then submit_plan, source-satisfied, or <<QUESTION>> with the exact blocker.",
				true
		}
	}
	next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
	if next == "" {
		return "", "", false
	}
	return "Required edit path source read required",
		"required_edit_paths define the write scope, but not every required path has source evidence yet. Do not use broad context/search discovery here. " + planThenExecuteSourceReadInstruction(next, results) + ". After all required paths are read once, submit_plan, source-satisfied, or <<QUESTION>> with the exact path-specific blocker.",
		true
}

func planThenExecuteSourceReadInstruction(path string, results []workspace.ToolResult) string {
	path = normalizeRunnerPath(path)
	if path == "" {
		return "read the next unread required_edit_path with source-body evidence"
	}
	if planThenExecutePathHasOutlineOnlyEvidence(path, results) {
		return "read source for " + path + " using a source-body route: `code_navigate` with `capability:\"read_symbol\"` for the relevant declaration, or `read_file` with `start_line`/`end_line` copied from the outline. Do not repeat whole-file `read_file`; it can redirect to an outline and still leave the path unread"
	}
	return "read source for " + path + " with `read_file`; if the file is large or redirects to an outline, follow with `code_navigate` `read_symbol` or bounded `read_file` using `start_line`/`end_line`"
}

func planThenExecutePathHasOutlineOnlyEvidence(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, readPath := range readPathsFromToolResult(result) {
			if normalizeRunnerPath(readPath) == path {
				return false
			}
		}
		text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}
		if strings.Contains(text, "file_outline") ||
			strings.Contains(text, "go_file_outline") ||
			strings.Contains(text, "redirected "+strings.ToLower(path)+" to code_navigate") ||
			strings.Contains(text, "redirected "+strings.ToLower(path)+" to go_file_outline") {
			return true
		}
	}
	return false
}

func planThenExecuteSourceBodyReadRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "read_file":
		return req.StartLine > 0 || req.EndLine > 0
	case "code_navigate", "read_only_capability":
		capability := strings.TrimSpace(req.Capability)
		return capability == "read_symbol" || capability == "extract_body" ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body")
	default:
		return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
	}
}

func planThenExecutePostReadinessDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", "", false
	}
	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return "", "", false
	}
	if strings.TrimSpace(req.Tool) == "submit_plan" || !isReadClassTool(req) {
		return "", "", false
	}
	if len(planThenExecuteUnreadRequiredPaths(brief, results)) > 0 {
		return "", "", false
	}
	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
		target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
		required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
		_, inRequiredScope := required[target]
		if submitPlanRejectionHasInvalidAnchor(rejected) &&
			target != "" &&
			inRequiredScope &&
			submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
			planThenExecuteSourceBodyReadRequest(req) &&
			!successfulReadPathAfter(results, rejectedAt, target) {
			return "", "", false
		}
		return "Corrected submit_plan required",
			"every required_edit_path has satisfied its source-body obligation, but the previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanRejectionBrief(rejected) + "Submit a corrected parser-readable `submit_plan` request whose `edits[]` coverage includes every required_edit_path with valid anchors or line ranges from existing source-body evidence, answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker. Textual-tool backends must emit exactly one visible JSON object like {\"tool\":\"submit_plan\",\"rationale\":\"...\",\"edits\":[{\"path\":\"path/from/required_edit_paths\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\",\"purpose\":\"one sentence\"}]}. If the rejection named a stale edit anchor, at most one bounded source-body read on that required path is allowed to refresh exact current text.",
			true
	}
	return "Plan submission required",
		"every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request whose `edits[]` coverage includes every required path, answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker. Textual-tool backends must emit exactly one visible JSON object like {\"tool\":\"submit_plan\",\"rationale\":\"...\",\"edits\":[{\"path\":\"path/from/required_edit_paths\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\",\"purpose\":\"one sentence\"}]}.",
		true
}

func submitPlanRejectionHasInvalidAnchor(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error))
	return strings.Contains(text, "invalid edit anchor") ||
		strings.Contains(text, "old_text not found") ||
		strings.Contains(text, "line range outside current source")
}

func submitPlanInvalidAnchorPaths(result workspace.ToolResult) map[string]struct{} {
	text := result.Summary + "\n" + result.Error
	out := make(map[string]struct{})
	for _, segment := range strings.Split(text, "`") {
		segment = strings.TrimSpace(segment)
		if segment == "" || !strings.Contains(segment, ":") {
			continue
		}
		path := normalizeRunnerPath(strings.SplitN(segment, ":", 2)[0])
		if path != "" {
			out[path] = struct{}{}
		}
	}
	return out
}

func submitPlanInvalidAnchorPathsAllowsRead(paths map[string]struct{}, path string) bool {
	if len(paths) == 0 {
		return false
	}
	_, ok := paths[normalizeRunnerPath(path)]
	return ok
}

func successfulReadPathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for i := index + 1; i < len(results); i++ {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, readPath := range readPathsFromToolResult(result) {
			if normalizeRunnerPath(readPath) == path {
				return true
			}
		}
	}
	return false
}

func lastRejectedSubmitPlanCoverageResult(results []workspace.ToolResult) (workspace.ToolResult, int, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if planThenExecuteSubmitPlanRejectedResult(result) {
			return result, i, true
		}
	}
	return workspace.ToolResult{}, -1, false
}

func planThenExecuteSubmitPlanRejectedForCorrection(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == "submit_plan" && planThenExecuteSubmitPlanRejectedResult(result)
}

func planThenExecuteSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}
	return strings.Contains(result.Summary, "submit_plan rejected") || strings.Contains(result.Error, "submit_plan must include")
}

func lastSubmitPlanRejectionBrief(result workspace.ToolResult) string {
	text := strings.TrimSpace(result.Error)
	if text == "" {
		text = strings.TrimSpace(result.Summary)
	}
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	const limit = 360
	if len(text) > limit {
		text = strings.TrimSpace(text[:limit]) + "..."
	}
	return "Last submit_plan rejection: " + text + " "
}

func planThenExecuteUnreadRequiredPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]struct{} {
	read := readPathsFromResults(results)
	unread := make(map[string]struct{}, len(brief.RequiredEditPaths))
	for _, path := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := read[normalized]; ok {
			continue
		}
		unread[normalized] = struct{}{}
	}
	return unread
}

func firstPlanThenExecuteUnreadRequiredPath(required []string, unread map[string]struct{}) string {
	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, ok := unread[normalized]; ok {
			return normalized
		}
	}
	return ""
}

func planThenExecuteRequestTargetPath(req workspace.ToolRequest) string {
	for _, candidate := range []string{req.Path, req.Destination, req.Workdir} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && candidate != "." {
			return candidate
		}
	}
	if path := pathFromPlanThenExecuteHandle(req.Handle); path != "" {
		return path
	}
	if path := pathFromPlanThenExecuteHandle(req.ID); path != "" {
		return path
	}
	return ""
}

func pathFromPlanThenExecuteHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	switch {
	case strings.HasPrefix(handle, "file://"):
		return strings.TrimPrefix(handle, "file://")
	case strings.HasPrefix(handle, "symbol://"):
		rest := strings.TrimPrefix(handle, "symbol://")
		if path, _, ok := strings.Cut(rest, "#"); ok {
			return path
		}
		return rest
	default:
		return ""
	}
}

// preflightPlanThenExecutePlannerToolGuard keeps verification and shell
// affordances out of the planner phase. The planner can gather bounded
// source evidence and submit a plan, report source-satisfied evidence, or
// ask a blocker question; build/test/diff commands belong to the
// orchestrator/review side of the plan-execute workflow.
func (r *Runner) preflightPlanThenExecutePlannerToolGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlannerPhase() {
		return verificationInterceptResult{}
	}
	tool := strings.TrimSpace(req.Tool)
	if !planThenExecutePlannerForbiddenTool(tool) {
		return verificationInterceptResult{}
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected verification tool",
			Error: "plan-then-execute planner phase does not run shell, git, build, test, source-sanity, delegation, or task-ledger commands. " +
				"Use submit_plan, answer `Decision: source-satisfied` with per-path evidence when the requested final state is already present, or answer <<QUESTION>> with the exact blocker.",
		},
		Reason: "plan-then-execute planner phase: verification/shell tool " + tool + " is outside the planner contract",
	}
}

func (r *Runner) planThenExecuteClampedEOFReadRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" || req.StartLine <= 0 {
		return "", "", false
	}
	totalLines, ok := priorClampedEOFReadLineCount(path, results)
	if !ok || req.StartLine <= totalLines {
		return "", "", false
	}
	return "Out-of-range read_file rejected",
		"previous read_file evidence already established that " + path + " has " + strconv.Itoa(totalLines) +
			" lines total, so start_line " + strconv.Itoa(req.StartLine) + " is beyond EOF and cannot add new evidence. " +
			"Do not request another out-of-range window. If the gathered source evidence is sufficient, call submit_plan. " +
			"If the source already satisfies the requested change, answer `Decision: source-satisfied` with per-path evidence. " +
			"Otherwise answer <<QUESTION>> and name the exact missing fact.",
		true
}

func (r *Runner) shouldHideToolAfterDiscoveryHardRedirect(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return false
	}
	if tool == "read_file" && r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 {
		return false
	}
	return true
}

func priorClampedEOFReadLineCount(path string, results []workspace.ToolResult) (int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, false
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}
		total, ok := clampedEOFReadLineCountForPath(result.Summary, path)
		if ok {
			return total, true
		}
	}
	return 0, false
}

func clampedEOFReadLineCountForPath(summary string, path string) (int, bool) {
	summary = strings.TrimSpace(summary)
	path = normalizeRunnerPath(path)
	const prefix = "Read "
	const linesMarker = " lines "
	const clampMarker = " was clamped to file bounds; file has "
	if !strings.HasPrefix(summary, prefix) || !strings.Contains(summary, clampMarker) {
		return 0, false
	}
	rest := strings.TrimPrefix(summary, prefix)
	linesIdx := strings.Index(rest, linesMarker)
	if linesIdx < 0 {
		return 0, false
	}
	description := strings.TrimSpace(rest[:linesIdx])
	if !readFileSummaryDescriptionMatchesPath(description, path) {
		return 0, false
	}
	clampIdx := strings.Index(summary, clampMarker)
	if clampIdx < 0 {
		return 0, false
	}
	tail := strings.TrimSpace(summary[clampIdx+len(clampMarker):])
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return 0, false
	}
	total, err := strconv.Atoi(fields[0])
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

func readFileSummaryDescriptionMatchesPath(description string, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range readFileSummaryPathCandidates(description) {
		if normalizeRunnerPath(candidate) == path {
			return true
		}
	}
	return false
}

func readFileSummaryPathCandidates(description string) []string {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil
	}
	candidates := []string{description}
	fields := strings.Fields(description)
	if len(fields) > 1 && looksLikeWorkspacePathHandle(fields[0]) {
		candidates = append(candidates, strings.TrimSpace(strings.TrimPrefix(description, fields[0])))
	}
	return candidates
}

func looksLikeWorkspacePathHandle(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != 'f' {
		return false
	}
	for _, r := range text[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (r *Runner) preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(req workspace.ToolRequest) verificationInterceptResult {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return verificationInterceptResult{}
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}
	edits := req.Edits
	if len(edits) == 0 && req.PlanArtifact != nil {
		edits = req.PlanArtifact.Edits
	}
	if len(edits) == 0 || planEditsHaveNoConcretePath(edits) {
		return reviewRepairNoopSubmitPlanIntercept("no concrete corrective edit paths were provided")
	}
	if r.Workspace != nil && reviewRepairPlanEditsAlreadyApplied(r.Workspace.Root(), edits) {
		return reviewRepairNoopSubmitPlanIntercept("all submitted corrective edits already match the current workspace")
	}
	return verificationInterceptResult{}
}

func reviewRepairNoopSubmitPlanIntercept(reason string) verificationInterceptResult {
	return verificationInterceptResult{
		Intercepted: true,
		Reason:      "review-repair submit_plan suppressed: " + reason,
		Result: workspace.ToolResult{
			Tool:    "review_repair_noop_plan",
			Summary: "review-repair submit_plan ignored: no new corrective plan",
			Output: strings.Join([]string{
				"The attempted review-repair submit_plan did not contain a new corrective edit.",
				"If the verification checklist passes, answer with `Decision: pass` and include the `Verification Checklist`.",
				"If a checklist item fails, call `submit_plan` only with concrete new corrective edits for the failing item.",
			}, " "),
		},
	}
}

func planEditsHaveNoConcretePath(edits []workspace.PlanEdit) bool {
	if len(edits) == 0 {
		return true
	}
	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) != "" {
			return false
		}
	}
	return true
}

func reviewRepairPlanEditsAlreadyApplied(root string, edits []workspace.PlanEdit) bool {
	root = strings.TrimSpace(root)
	if root == "" || len(edits) == 0 {
		return false
	}
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path == "" || filepath.IsAbs(path) {
			return false
		}
		clean := filepath.Clean(path)
		if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
			return false
		}
		bodyBytes, err := os.ReadFile(filepath.Join(root, clean))
		if err != nil {
			return false
		}
		if !reviewRepairPlanEditAlreadyApplied(string(bodyBytes), edit) {
			return false
		}
	}
	return true
}

func reviewRepairPlanEditAlreadyApplied(body string, edit workspace.PlanEdit) bool {
	oldText := edit.OldText
	newText := edit.NewText
	switch {
	case edit.ReplaceAll && oldText == "" && newText != "":
		return body == newText
	case oldText != "" && newText != "":
		return !strings.Contains(body, oldText) && strings.Contains(body, newText)
	case oldText != "" && newText == "":
		return !strings.Contains(body, oldText)
	default:
		return false
	}
}

// planThenExecuteForbiddenWriteTool lists the per-edit write tools
// the planner phase must not call. submit_plan is OUR tool, not a
// per-edit write — it captures the structured plan.
func planThenExecuteForbiddenWriteTool(tool string) bool {
	switch tool {
	case "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "go_move_declarations", "mkdir":
		return true
	}
	return false
}

func planThenExecutePlannerForbiddenTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd":
		return true
	default:
		return false
	}
}

// planThenExecuteShortCircuitAnswer synthesizes the planner-phase
// final answer from a successful submit_plan tool result, letting the
// runner exit immediately without spending another model call on a
// summary that's redundant with the persisted plan artifact.
//
// Returns ("", false) when the short-circuit doesn't apply (wrong
// lane, no successful submit_plan, etc.). When it returns a non-empty
// answer, the runner should call finish(answer, nil,
// "plan_submitted_short_circuit", false).
//
// Why this exists: the planner's natural step-2 output is a short
// "Plan submitted with N edits across M files: ..." paragraph. That
// summary is already implicit in the plan artifact (the executor
// phase will dispatch each edit and emit per-edit JSON events with
// path + status). Asking the model to retype it costs ~4s and ~200
// tokens for zero new information.
func planThenExecuteShortCircuitAnswer(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult) (string, bool) {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily != PlanThenExecutePlannerTaskFamily && taskFamily != PlanThenExecuteReviewRepairTaskFamily {
		return "", false
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return "", false
	}
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return "", false
	}
	if strings.TrimSpace(result.Error) != "" {
		return "", false
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "Plan submitted; the executor phase will dispatch the planned edits."
	}
	body := []string{
		summary,
		"",
		"The plan artifact is on disk under .idiom/plans/. The orchestrator's executor phase will dispatch each edit deterministically and emit per-edit telemetry. No further planner action required.",
	}
	if details := strings.TrimSpace(result.Output); details != "" {
		body = append(body, "", details)
	}
	return strings.Join(body, "\n"), true
}

func planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", false
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" || strings.TrimSpace(result.Tool) != "submit_plan" {
		return "", false
	}
	if !planThenExecuteNoopSubmitPlanRejection(result) {
		return "", false
	}
	paths := planThenExecuteSubmitPlanRequestPaths(req)
	if len(paths) == 0 {
		return "", false
	}
	requiresTest := planThenExecuteObjectiveRequiresTestEdit(strings.Join([]string{brief.Objective, req.Rationale}, "\n"))
	noOpPathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		noOpPathSet[normalizeRunnerPath(path)] = struct{}{}
	}
	if !planThenExecutePathsIncludeTest(paths) {
		paths = append(paths, planThenExecuteReadTestPaths(results)...)
		paths = uniquePlanThenExecutePaths(paths)
	}
	if requiresTest && !planThenExecutePathsIncludeTest(paths) {
		return "", false
	}
	var b strings.Builder
	b.WriteString("Decision: source-satisfied\n")
	b.WriteString("The planner attempted `submit_plan`, but the plan was rejected because the proposed edit would not change the current workspace; no changes needed.\n\n")
	b.WriteString("Current-state evidence paths:\n")
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		if _, ok := noOpPathSet[normalizeRunnerPath(path)]; ok {
			b.WriteString(": submit_plan validation compared the proposed edit against the workspace and reported it as a no-op.\n")
			continue
		}
		b.WriteString(": planner read this path before the no-op plan rejection; current-state evidence was already available, so no separate test edit was needed.\n")
	}
	return strings.TrimSpace(b.String()), true
}

func planThenExecuteNoopSubmitPlanRejection(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "" {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error))
	return strings.Contains(text, "old_text and new_text are identical") ||
		strings.Contains(text, "edit would not change") ||
		strings.Contains(text, "replacement text already appears") ||
		strings.Contains(text, "source-satisfied rationale") ||
		strings.Contains(text, "requested final state is already present") ||
		strings.Contains(text, "verification-only edits")
}

func planThenExecuteSubmitPlanRequestPaths(req workspace.ToolRequest) []string {
	var paths []string
	for _, edit := range req.Edits {
		paths = append(paths, edit.Path)
	}
	if req.PlanArtifact != nil {
		for _, edit := range req.PlanArtifact.Edits {
			paths = append(paths, edit.Path)
		}
	}
	return uniquePlanThenExecutePaths(paths)
}

func uniquePlanThenExecutePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func planThenExecuteObjectiveRequiresTestEdit(objective string) bool {
	lower := strings.ToLower(objective)
	for _, phrase := range []string{
		"add regression test",
		"add regression tests",
		"add test",
		"add tests",
		"add unit test",
		"add unit tests",
		"write regression test",
		"write regression tests",
		"write test",
		"write tests",
		"regression tests proving",
		"tests proving",
		"test coverage for",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func planThenExecutePathsIncludeTest(paths []string) bool {
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if strings.Contains(lower, "/test/") ||
			strings.Contains(lower, "/tests/") ||
			strings.HasSuffix(lower, "_test.go") ||
			strings.HasSuffix(lower, ".test.ts") ||
			strings.HasSuffix(lower, ".test.tsx") ||
			strings.HasSuffix(lower, ".spec.ts") ||
			strings.HasSuffix(lower, ".spec.tsx") ||
			strings.Contains(lower, "test") {
			return true
		}
	}
	return false
}

func planThenExecuteReadTestPaths(results []workspace.ToolResult) []string {
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return nil
	}
	paths := make([]string, 0, len(read))
	for path := range read {
		if planThenExecutePathsIncludeTest([]string{path}) {
			paths = append(paths, path)
		}
	}
	return uniquePlanThenExecutePaths(paths)
}

// successfulSubmitPlanResultCount returns the number of successful
// submit_plan tool results in the lane's recorded history. The
// completion gate uses this as the "the planner did its job" signal
// for plan-then-execute mode, replacing the usual changed-paths check
// (the planner's submit_plan call doesn't change files; the executor
// phase does, in a separate process).
func successfulSubmitPlanResultCount(results []workspace.ToolResult) int {
	count := 0
	for _, r := range results {
		if strings.TrimSpace(r.Tool) != "submit_plan" {
			continue
		}
		if strings.TrimSpace(r.Error) != "" {
			continue
		}
		count++
	}
	return count
}

// PlanThenExecuteSourceSatisfiedAnswerCoversPaths reports whether a planner
// answer explicitly claims the requested final source state is already present
// and names every required path. It intentionally checks the answer shape only;
// Runner.planThenExecuteSourceSatisfiedAnswerCovers adds tool/workspace
// evidence before the completion gate accepts the answer.
func PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer string, requiredPaths []string) bool {
	if !planThenExecuteSourceSatisfiedAnswerIntent(answer) {
		return false
	}
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}
	lowerAnswer := strings.ToLower(answer)
	for path := range required {
		if !strings.Contains(lowerAnswer, strings.ToLower(path)) {
			return false
		}
	}
	return true
}

// PlanThenExecuteSourceSatisfiedEvidencePaths extracts concrete path evidence
// from a source-satisfied answer. It is intentionally narrower than generic
// path parsing: the answer must first use the exact plan-execute closeout
// decision shape so blocker text that merely mentions the remedy is ignored.
func PlanThenExecuteSourceSatisfiedEvidencePaths(answer string) []string {
	if !planThenExecuteSourceSatisfiedAnswerIntent(answer) {
		return nil
	}
	return uniquePlanThenExecutePaths(pathparse.MentionedWorkspacePaths(answer))
}

func planThenExecuteSourceSatisfiedAnswerIntent(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}
	firstLine := trimmed
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.ToLower(strings.TrimSpace(firstLine))
	if firstLine != "decision: source-satisfied" && firstLine != "decision: source satisfied" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return containsAny(lower,
		"already contains",
		"already implemented",
		"already present",
		"already satisf",
		"current-state",
		"current state",
		"final state",
		"no change",
		"no changes",
		"no-op",
		"no op",
		"nothing to change",
		"stale anchor",
		"stale old_text",
		"stale spec",
	)
}

func (r *Runner) planThenExecuteSourceSatisfiedAnswerCovers(reply string, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}
	missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))
	if len(missing) == 0 {
		missing = PlanThenExecuteSourceSatisfiedEvidencePaths(reply)
	}
	if !PlanThenExecuteSourceSatisfiedAnswerCoversPaths(reply, missing) {
		return false
	}
	if r.planThenExecuteRequiredEditSnippetsSourceSatisfied() {
		return true
	}
	return planThenExecuteSourceSatisfiedResultsCoverPaths(missing, results)
}

func planThenExecuteSourceSatisfiedResultsCoverPaths(requiredPaths []string, results []workspace.ToolResult) bool {
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return false
	}
	for _, path := range requiredPaths {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return false
		}
	}
	return true
}

func (r *Runner) planThenExecuteRequiredEditSnippetsSourceSatisfied() bool {
	if r == nil || r.Workspace == nil {
		return false
	}
	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return false
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}
	contentCache := make(map[string]string)
	for _, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, nil)
		if len(candidates) == 0 {
			return false
		}
		if !requiredEditSnippetPresent(root, candidates, snippet.Content, contentCache) {
			return false
		}
	}
	return true
}

// planThenExecuteReadyToSubmitNudge returns a system-message string
// the runner injects into the next turn when the planner phase has
// read every required_edit_path at least once and has not yet called
// submit_plan. The nudge converts the planner's verification urge
// into closeout pressure: instead of letting the model spend more
// reads narrowing anchors that already exist in the brief, it gets a
// directive saying the state is ready and the next action MUST be
// submit_plan or <<QUESTION>>.
//
// Returns "" when the nudge does not apply (wrong lane, plan already
// submitted, paths still unread, etc.). The runner emits the nudge
// at most once per turn-since-coverage; it is the caller's job to
// gate against re-injection on every subsequent tool result.
func planThenExecuteReadyToSubmitNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}
	required := brief.RequiredEditPaths
	if successfulSubmitPlanResultCount(results) > 0 {
		// Plan already submitted — the gate-exemption path will close
		// the runner cleanly; nothing to nudge.
		return ""
	}
	read := readPathsFromResults(results)
	if len(read) == 0 {
		return ""
	}
	if len(required) == 0 {
		if unread := unreadRecoveryEvidencePathsForObjective(brief.Objective, results, 2); len(unread) > 0 {
			return ""
		}
		paths := make([]string, 0, len(read))
		for path := range read {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		return strings.Join([]string{
			"PLANNER READINESS REACHED: at least one candidate edit path has been read in this turn. The candidate path evidence is sufficient to submit a bounded plan, close as source-satisfied, or name one concrete blocker.",
			"",
			"Read candidate path(s): " + strings.Join(paths, ", "),
			"",
			"Next action MUST be one of:",
			"  1. `submit_plan` with only the edit(s) needed by the objective, using a substring or line-range anchor copied from the read candidate source, plus a one-sentence purpose per edit.",
			"  2. `Decision: source-satisfied` naming the relevant candidate path(s) and observed current-state evidence when the requested final state is already present.",
			"  3. `<<QUESTION>>:` with the exact blocker that prevents drafting the plan.",
			"",
			"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
			"",
			"Default to submitting now. Only read/search again if you can name one exact new edit-safety fact that is still missing after the candidate path read.",
		}, "\n")
	}
	for _, path := range required {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return ""
		}
	}
	return strings.Join([]string{
		"PLANNER READINESS REACHED: every required_edit_path has been read at least once in this turn. State is sufficient to submit the plan or close as source-satisfied.",
		"",
		"Next action MUST be one of:",
		"  1. A parser-readable `submit_plan` request with the full ordered edits[] array (substring, line-range, or whole-file/new-file shape, plus a one-sentence purpose per edit). Textual-tool backends must emit exactly one visible JSON object like {\"tool\":\"submit_plan\",\"rationale\":\"...\",\"edits\":[{\"path\":\"path/from/required_edit_paths\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\",\"purpose\":\"one sentence\"}]}. For a missing/new source or test file, the brief must provide a substantial inline body or explicit replacement edit shape.",
		"  2. `Decision: source-satisfied` naming every required path and the observed current-state evidence when the requested final state is already present.",
		"  3. `<<QUESTION>>:` with the exact path-specific blocker that prevents you from drafting the plan.",
		"",
		"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
		"",
		"Default to submitting now. Only read again if you can name one exact new edit-safety fact that is still missing after the required path reads.",
	}, "\n")
}

func planThenExecuteUnreadRequiredSourceNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}
	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return ""
	}
	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	if len(unread) == 0 {
		return ""
	}
	next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
	if next == "" {
		return ""
	}
	if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(next, results) {
		return ""
	}
	return strings.Join([]string{
		"REQUIRED SOURCE READ PENDING: a required_edit_path has not produced source-body evidence yet.",
		"",
		"Unread required path: " + next,
		"",
		"Next action MUST be exactly one visible source-body tool call for that path before `submit_plan`:",
		"  1. `code_navigate` with `capability:\"read_symbol\"` for the relevant declaration in " + next + ", or",
		"  2. `read_file` with explicit `start_line`/`end_line` copied from the outline/search hit.",
		"",
		"Outline and search hits do not satisfy this contract by themselves. Do not call `submit_plan` until every required_edit_path has source-body evidence. Hidden reasoning is not executable evidence.",
	}, "\n")
}

func planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(normalized)) {
			continue
		}
		if strings.Contains(text, "new file") ||
			strings.Contains(text, "does not exist yet") ||
			strings.Contains(text, "missing parent") {
			return true
		}
	}
	return false
}

func planThenExecuteExplicitSourceSatisfiedAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", false
	}
	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return "", false
	}
	if !explicitSourceSatisfiedNoWriteObjective(brief.Objective) {
		return "", false
	}
	read := readPathsFromResults(results)
	for _, path := range brief.RequiredEditPaths {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
			return "", false
		}
	}
	evidence := readEvidenceSummariesByPath(results)
	lines := []string{"Decision: source-satisfied", "", "No file changes were made. The objective explicitly requested a no-write/source-satisfied closeout, and every required edit path has current-state evidence from this planner turn.", "", "Required path evidence:"}
	for _, path := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
		summary := strings.Join(evidence[normalized], "; ")
		if summary == "" {
			summary = "current-state source evidence gathered"
		}
		lines = append(lines, "- "+path+": "+summary)
	}
	lines = append(lines, "", "Changed paths: none.", "Verification: not run; no edits were planned or applied.")
	return strings.Join(lines, "\n"), true
}

func explicitSourceSatisfiedNoWriteObjective(objective string) bool {
	words := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	if !containsAny(words, " source-satisfied ", " source satisfied ") {
		return false
	}
	return containsAny(words,
		" without changing files ",
		" without file changes ",
		" without editing files ",
		" do not write files ",
		" no file changes ",
		" no files changed ",
	)
}

func readEvidenceSummariesByPath(results []workspace.ToolResult) map[string][]string {
	evidence := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		summary := strings.TrimSpace(result.Summary)
		if summary == "" {
			summary = strings.TrimSpace(result.Tool)
		}
		for _, path := range readPathsFromToolResult(result) {
			normalized := normalizeRunnerPath(path)
			if normalized == "" {
				continue
			}
			if seen[normalized] == nil {
				seen[normalized] = make(map[string]struct{})
			}
			if _, ok := seen[normalized][summary]; ok {
				continue
			}
			seen[normalized][summary] = struct{}{}
			evidence[normalized] = append(evidence[normalized], summary)
		}
	}
	return evidence
}

// readPathsFromResults returns the set of paths that have been
// successfully read in this lane (read_file, code_navigate read_*,
// *_file_outline / *_read_symbol, etc.). Used by the planner readiness
// nudge to check whether every required path has been touched.
func readPathsFromResults(results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{}, len(results))
	for _, r := range results {
		if strings.TrimSpace(r.Error) != "" {
			continue
		}
		for _, path := range readPathsFromToolResult(r) {
			paths[normalizeRunnerPath(path)] = struct{}{}
		}
	}
	return paths
}

func readPathsFromToolResult(r workspace.ToolResult) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, candidate := range []string{
		extractReadPathFromSummary(r.Summary),
		extractReadPathFromSummary(r.Output),
		extractRoutedReadPathFromOutput(r.Output),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		normalized := normalizeRunnerPath(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

// extractReadPathFromSummary parses canonical source-body read summary
// shapes that read_file and language-symbol tools emit to recover the
// path. Recognized shapes:
//
//   - "Read fXX path/to/file lines A-B"  (redirected/read-file handle shape)
//   - "Read path/to/file lines A-B"  (direct read_file)
//   - "Read <symbol> in path/to/file lines A-B"  (cpp_read_symbol /
//     go_read_symbol / etc.)
//
// Returns "" for anything else; the caller falls back to skipping
// that result. Outlines are intentionally not counted as source-body
// reads: they orient the model, but they do not provide safe edit
// anchors for plan-execute readiness.
func extractReadPathFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	fields := strings.Fields(summary)
	if len(fields) < 3 {
		return ""
	}
	if fields[0] != "Read" {
		return ""
	}
	// "Read <symbol> in <path> ..." — language-symbol shape.
	if len(fields) >= 4 && fields[2] == "in" {
		return fields[3]
	}
	if len(fields) >= 3 && fields[2] == "lines" {
		return fields[1]
	}
	// "Read fXX path lines A-B" — read_file shape.
	return fields[2]
}

func extractRoutedReadPathFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "route_summary:") {
			continue
		}
		return extractReadPathFromSummary(strings.TrimSpace(strings.TrimPrefix(line, "route_summary:")))
	}
	return ""
}
