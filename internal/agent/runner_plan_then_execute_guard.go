package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/plancontract"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

// PlanThenExecutePlannerTaskFamily is the runtime-brief task_family
// that marks the planner phase of `idiom plan-execute`. The runner
// uses it to forbid per-edit write tools (edit_file, write_file,
// append_file, apply_patch, delegate_edit) — the planner's only
// writes go through submit_plan. The executor phase, which runs in a
// separate process via the orchestrator in internal/planexecutecmd/plan_execute.go,
// uses the workspace dispatch directly.
const PlanThenExecutePlannerTaskFamily = string(workerruntime.TaskFamilyPlanThenExecutePlanner)

const planThenExecuteAnchorRefreshMaxTokens = 2048

// PlanThenExecuteReviewRepairTaskFamily marks the post-executor
// action-capable review phase. It shares the submit_plan surface for
// corrective edits, but unlike the planner phase it may also close with a
// direct `Decision: pass` answer when the evidence checklist is satisfied.
const PlanThenExecuteReviewRepairTaskFamily = string(workerruntime.TaskFamilyPlanThenExecuteReview)

const planThenExecuteResultCandidateSourceReady = "plan_then_execute.candidate_source_ready"

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
	scope := r.planThenExecuteRequiredEditScope()
	scopeActive := len(scope.coverage) > 0 || len(scope.allowedExact) > 0 || len(scope.directoryScopes) > 0
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 && workspace.SubmitPlanRequestClaimsSourceSatisfied(req) {
		return verificationInterceptResult{
			Intercepted: true,
			Result: workspace.ToolResult{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: source-satisfied terminal shape required",
				Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
				Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
			},
			Reason: "plan-then-execute planner phase: source-satisfied closeout used submit_plan transport",
		}
	}
	sourceSatisfiedPaths := planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req)
	planned := make(map[string]struct{}, len(edits)+len(sourceSatisfiedPaths))
	var outside []string
	var missingPathEdits []string
	for _, path := range sourceSatisfiedPaths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			planned[normalized] = struct{}{}
			if scopeActive && !scope.allowsPlanPath(normalized) {
				outside = append(outside, normalized)
			}
		}
	}
	for i, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			missingPathEdits = append(missingPathEdits, planThenExecuteSubmitPlanEditName(i, edit))
			continue
		}
		planned[path] = struct{}{}
		if scopeActive && !scope.allowsPlanPath(path) {
			outside = append(outside, path)
		}
	}
	var missing []string
	if scopeActive {
		for _, path := range scope.coverage {
			if _, ok := planned[path]; !ok {
				missing = append(missing, path)
			}
		}
		if len(scope.coverage) == 0 && len(scope.directoryScopes) > 0 && len(planned) == 0 {
			missing = append(missing, scope.directoryScopes...)
		}
	}
	missingSnippets := r.missingSubmitPlanRequiredEditSnippets(req, planned)
	invalidAnchors := r.invalidSubmitPlanAnchors(req)
	placeholderBodies := planThenExecutePlaceholderEditBodies(edits)
	if len(missing) == 0 && len(outside) == 0 && len(missingSnippets) == 0 && len(invalidAnchors) == 0 && len(placeholderBodies) == 0 {
		return verificationInterceptResult{}
	}
	details := planThenExecuteSubmitPlanCoverageDetails{
		Code:                    "plan_then_execute_submit_plan_coverage_mismatch",
		MissingRequiredPaths:    append([]string(nil), missing...),
		MissingPathEdits:        append([]string(nil), missingPathEdits...),
		OutsideRequiredPaths:    append([]string(nil), outside...),
		MissingRequiredSnippets: append([]string(nil), missingSnippets...),
		InvalidAnchors:          append([]string(nil), invalidAnchors...),
		PlaceholderEditBodies:   append([]string(nil), placeholderBodies...),
	}
	var parts []string
	scopeName := "required_edit_paths"
	outsideLabel := "path(s) outside required_edit_paths: "
	if scope.allowedOnly {
		scopeName = "allowed_edit_paths"
		outsideLabel = "path(s) outside allowed_edit_paths: "
	}
	if len(missing) > 0 {
		parts = append(parts, "missing required edit path(s): "+quotedRunnerStrings(missing))
	}
	if len(missingPathEdits) > 0 {
		parts = append(parts, "edit entry missing `path`: "+quotedRunnerStrings(missingPathEdits))
	}
	if len(outside) > 0 {
		parts = append(parts, outsideLabel+quotedRunnerStrings(outside))
	}
	if len(missingSnippets) > 0 {
		parts = append(parts, "missing required edit snippet(s): "+quotedRunnerStrings(missingSnippets))
	}
	if len(invalidAnchors) > 0 {
		parts = append(parts, "invalid edit anchor(s): "+quotedRunnerStrings(invalidAnchors))
	}
	if len(placeholderBodies) > 0 {
		parts = append(parts, "placeholder edit body path(s): "+quotedRunnerStrings(placeholderBodies))
	}
	requirement := "submit_plan edit entries must include `path` and executable edit bodies with valid current-source anchors or line ranges"
	if scopeActive {
		requirement = "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths, no edits outside " + scopeName + ", and coverage for every required_edit_snippets final-state snippet"
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Output:  planThenExecuteSubmitPlanCoverageDetailsJSON(details),
			Error: requirement + "; " +
				strings.Join(parts, "; ") +
				". Submit a corrected plan with `edits` as a JSON array, not a quoted string, and include `path` on every edit. Use insert_after_line/content only for small inline insertions/appends after an existing line; use old_text/new_text for exact replacements/deletions; use start_line/end_line/content to replace an exact inclusive current-source range, including large additions by replacing unchanged anchor line(s) with those line(s) plus inserted content. Edit bodies must be complete executable source text, not placeholders, ellipses, or prose.",
		},
		Reason: "plan-then-execute planner phase: submit_plan edits did not satisfy required path/snippet coverage",
	}
}

// preflightPlanThenExecuteSubmitPlanGroundingGuard prevents planner atoms from
// turning guessed file paths into executor plans. Required and allowed paths
// scope the edit, but existing-file edits still need source-body evidence or an
// explicit new-file/body contract that makes the edit executable.
func (r *Runner) preflightPlanThenExecuteSubmitPlanGroundingGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}
	if len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {
		return verificationInterceptResult{}
	}
	missing := r.ungroundedSubmitPlanEditPaths(req, results)
	if len(missing) == 0 {
		return verificationInterceptResult{}
	}
	sort.Strings(missing)
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for " + quotedRunnerStrings(missing) + ". " +
				"Read each existing edit path with `read_file` or " + workspace.CodeNavigateToolName + " " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + ", then submit a corrected plan. If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
		},
		Reason: "plan-then-execute planner phase: submit_plan lacked grounded edit paths",
	}
}

func (r *Runner) ungroundedSubmitPlanEditPaths(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return []string{"edits[]"}
	}
	root := ""
	if r != nil && r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}
	missing := make([]string, 0, len(edits))
	seen := make(map[string]struct{}, len(edits))
	for i, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			path = planThenExecuteSubmitPlanEditName(i, edit)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if r.submitPlanEditPathGrounded(path, edit, root, results) {
			continue
		}
		missing = append(missing, path)
	}
	return missing
}

func (r *Runner) submitPlanEditPathGrounded(path string, edit workspace.PlanEdit, root string, results []workspace.ToolResult) bool {
	return r.submitPlanEditPathGrounding(path, edit, root, results).Level != planThenExecuteEvidenceNone
}

func (r *Runner) submitPlanEditPathGrounding(path string, edit workspace.PlanEdit, root string, results []workspace.ToolResult) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}
	if r != nil {
		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			return evidence
		}
	}
	if planThenExecuteEditHasExactAnchorContract(edit, root, path) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceExactAnchorContract,
			Source: "submit_plan",
			Detail: "exact current-source edit anchor",
		}
	}
	if planThenExecuteCompleteNewFileEdit(edit, root, path) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceNewFileContract,
			Source: "submit_plan",
			Detail: "complete new-file body",
		}
	}
	return planThenExecuteSourceEvidence{Path: path}
}

func planThenExecuteEditHasExactAnchorContract(edit workspace.PlanEdit, root string, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || strings.TrimSpace(root) == "" || filepath.IsAbs(path) {
		return false
	}
	current := readPlanProjectionBase(root, path)
	if current == "" {
		return false
	}
	if strings.TrimSpace(edit.OldText) != "" {
		return strings.Contains(current, edit.OldText)
	}
	if edit.StartLine > 0 || edit.EndLine > 0 {
		_, ok := applyPlanProjectionRange(current, edit.StartLine, edit.EndLine, edit.Content)
		return ok
	}
	return false
}

func planThenExecuteCompleteNewFileEdit(edit workspace.PlanEdit, root string, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || strings.TrimSpace(root) == "" || filepath.IsAbs(path) {
		return false
	}
	if !edit.ReplaceAll || strings.TrimSpace(edit.OldText) != "" || strings.TrimSpace(edit.NewText) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
		return false
	} else {
		return os.IsNotExist(err)
	}
}

type planThenExecuteRequiredEditScope struct {
	coverage        []string
	allowedExact    map[string]struct{}
	directoryScopes []string
	allowedOnly     bool
}

func (r *Runner) planThenExecuteRequiredEditScope() planThenExecuteRequiredEditScope {
	root := ""
	if r != nil && r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}
	if r == nil {
		return planThenExecuteRequiredEditScopeFromPaths(nil, root)
	}
	if len(normalizedRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths)) == 0 && len(r.RuntimeBrief.AllowedEditPaths) > 0 {
		return planThenExecuteAllowedEditScopeFromPaths(r.RuntimeBrief.AllowedEditPaths, root)
	}
	return planThenExecuteRequiredEditScopeFromPaths(r.RuntimeBrief.RequiredEditPaths, root)
}

func planThenExecuteRequiredCoveragePaths(required []string, root string) []string {
	return planThenExecuteRequiredEditScopeFromPaths(required, root).coverage
}

func planThenExecuteRequiredEditScopeFromPaths(required []string, root string) planThenExecuteRequiredEditScope {
	scope := planThenExecuteRequiredEditScope{
		allowedExact: make(map[string]struct{}),
	}
	normalizedRequired := normalizedRequiredEditPaths(required)
	seenCoverage := make(map[string]struct{}, len(normalizedRequired))
	seenDirectories := make(map[string]struct{})
	for _, path := range normalizedRequired {
		scope.allowedExact[path] = struct{}{}
		if planThenExecuteRequiredPathIsDirectoryScope(path, normalizedRequired, root) {
			if _, seen := seenDirectories[path]; !seen {
				scope.directoryScopes = append(scope.directoryScopes, path)
				seenDirectories[path] = struct{}{}
			}
			continue
		}
		if _, seen := seenCoverage[path]; seen {
			continue
		}
		scope.coverage = append(scope.coverage, path)
		seenCoverage[path] = struct{}{}
	}
	return scope
}

func planThenExecuteAllowedEditScopeFromPaths(allowed []string, root string) planThenExecuteRequiredEditScope {
	scope := planThenExecuteRequiredEditScope{
		allowedExact: make(map[string]struct{}),
		allowedOnly:  true,
	}
	normalizedAllowed := normalizedRequiredEditPaths(allowed)
	seenDirectories := make(map[string]struct{})
	for _, path := range normalizedAllowed {
		if planThenExecuteRequiredPathIsDirectoryScope(path, normalizedAllowed, root) {
			if _, seen := seenDirectories[path]; !seen {
				scope.directoryScopes = append(scope.directoryScopes, path)
				seenDirectories[path] = struct{}{}
			}
			continue
		}
		scope.allowedExact[path] = struct{}{}
	}
	return scope
}

func normalizedRequiredEditPaths(required []string) []string {
	out := make([]string, 0, len(required))
	seen := make(map[string]struct{}, len(required))
	for _, raw := range required {
		path := normalizeRunnerPath(raw)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		out = append(out, path)
		seen[path] = struct{}{}
	}
	return out
}

func planThenExecuteRequiredPathIsDirectoryScope(path string, all []string, root string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	if root != "" {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil && info.IsDir() {
			return true
		}
	}
	if pathparse.HasWorkspaceFileExtension(path) {
		return false
	}
	prefix := strings.TrimSuffix(path, "/") + "/"
	for _, other := range all {
		if other != path && strings.HasPrefix(other, prefix) {
			return true
		}
	}
	return false
}

func (scope planThenExecuteRequiredEditScope) allowsPlanPath(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, required := range scope.coverage {
		if path == required {
			return true
		}
	}
	for _, dir := range scope.directoryScopes {
		prefix := strings.TrimSuffix(dir, "/") + "/"
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	if _, ok := scope.allowedExact[path]; ok {
		return true
	}
	return false
}

type planThenExecuteSubmitPlanCoverageDetails struct {
	Code                    string   `json:"code,omitempty"`
	MissingRequiredPaths    []string `json:"missing_required_paths,omitempty"`
	MissingPathEdits        []string `json:"missing_path_edits,omitempty"`
	OutsideRequiredPaths    []string `json:"outside_required_paths,omitempty"`
	MissingRequiredSnippets []string `json:"missing_required_snippets,omitempty"`
	InvalidAnchors          []string `json:"invalid_anchors,omitempty"`
	PlaceholderEditBodies   []string `json:"placeholder_edit_bodies,omitempty"`
}

func planThenExecuteSubmitPlanCoverageDetailsJSON(details planThenExecuteSubmitPlanCoverageDetails) string {
	body, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(body)
}

func planThenExecuteSubmitPlanCoverageDetailsFromResult(result workspace.ToolResult) (planThenExecuteSubmitPlanCoverageDetails, bool) {
	var details planThenExecuteSubmitPlanCoverageDetails
	if strings.TrimSpace(result.Output) == "" {
		return details, false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Output)), &details); err != nil {
		return details, false
	}
	return details, strings.TrimSpace(details.Code) == "plan_then_execute_submit_plan_coverage_mismatch"
}

func planThenExecutePlaceholderEditBodies(edits []workspace.PlanEdit) []string {
	var out []string
	for i, edit := range edits {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			path = planThenExecuteSubmitPlanEditName(i, edit)
		}
		if field, marker := planThenExecutePlanEditPlaceholderField(edit); field != "" {
			out = append(out, path+": "+field+" contains placeholder marker "+strconv.Quote(marker))
		}
	}
	return out
}

func planThenExecutePlanEditPlaceholderField(edit workspace.PlanEdit) (string, string) {
	fields := []struct {
		name  string
		value string
	}{
		{name: "old_text", value: edit.OldText},
		{name: "new_text", value: edit.NewText},
		{name: "content", value: edit.Content},
	}
	for _, field := range fields {
		if marker := planThenExecutePlanEditPlaceholderMarker(field.value); marker != "" {
			return field.name, marker
		}
	}
	return "", ""
}

func planThenExecutePlanEditPlaceholderMarker(value string) string {
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
	addSourceSatisfiedPlanProjectionBases(root, projectedContent, planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req))
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

func addSourceSatisfiedPlanProjectionBases(root string, projectedContent map[string]string, paths []string) {
	if len(paths) == 0 {
		return
	}
	for _, rawPath := range paths {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, alreadyProjected := projectedContent[path]; alreadyProjected {
			continue
		}
		projectedContent[path] = readPlanProjectionBase(root, path)
	}
}

func planThenExecuteEffectiveSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]workspace.PlanEdit(nil), req.PlanArtifact.Edits...)
	}
	return append([]workspace.PlanEdit(nil), req.Edits...)
}

func planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req workspace.ToolRequest) []string {
	var paths []string
	if req.PlanArtifact != nil && len(req.PlanArtifact.Paths.SourceSatisfied) > 0 {
		paths = append(paths, req.PlanArtifact.Paths.SourceSatisfied...)
	}
	paths = append(paths, req.SourceSatisfiedPaths...)
	return paths
}

func (r *Runner) planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req workspace.ToolRequest, results []workspace.ToolResult) workspace.ToolRequest {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return req
	}
	sourceSatisfiedPaths := plancontract.UniqueScopePaths(planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req))
	if len(sourceSatisfiedPaths) == 0 {
		return req
	}
	artifact := workspace.PlanArtifact{}
	if req.PlanArtifact != nil {
		artifact = *req.PlanArtifact
	}
	if len(artifact.Edits) == 0 && len(req.Edits) > 0 {
		artifact.Edits = append([]workspace.PlanEdit(nil), req.Edits...)
	}
	if strings.TrimSpace(artifact.Task.Summary) == "" {
		artifact.Task.Summary = util.FirstNonEmpty(strings.TrimSpace(req.Rationale), "legacy submit_plan request")
	}
	artifact.Contract.Kind = util.FirstNonEmpty(strings.TrimSpace(artifact.Contract.Kind), "plan_then_execute")
	artifact.Contract.StrategyOwner = util.FirstNonEmpty(strings.TrimSpace(artifact.Contract.StrategyOwner), "model")
	artifact.Contract.ContractOwner = util.FirstNonEmpty(strings.TrimSpace(artifact.Contract.ContractOwner), "harness")
	artifact.Contract.PermissionProfile = util.FirstNonEmpty(strings.TrimSpace(artifact.Contract.PermissionProfile), "plan_then_execute")
	artifact.Contract.VerificationOwner = util.FirstNonEmpty(strings.TrimSpace(artifact.Contract.VerificationOwner), "planner")
	artifact.Paths.SourceSatisfied = plancontract.UniqueScopePaths(append(artifact.Paths.SourceSatisfied, sourceSatisfiedPaths...))
	if len(artifact.Paths.Required) == 0 {
		artifact.Paths.Required = plancontract.UniqueScopePaths(r.RuntimeBrief.RequiredEditPaths)
	}
	if len(artifact.Contract.RequiredEditPaths) == 0 {
		artifact.Contract.RequiredEditPaths = append([]string(nil), artifact.Paths.Required...)
	}
	if len(artifact.VerificationObligations) == 0 {
		artifact.VerificationObligations = []workspace.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     "executor_lane_sanity",
			Owner:    artifact.Contract.VerificationOwner,
			Status:   "open",
			Required: true,
		}}
	}
	sourceSatisfiedSet := map[string]struct{}{}
	for _, path := range sourceSatisfiedPaths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			sourceSatisfiedSet[normalized] = struct{}{}
		}
	}
	filteredEvidence := artifact.Evidence[:0]
	for _, evidence := range artifact.Evidence {
		path := normalizeRunnerPath(evidence.Path)
		if _, sourceSatisfied := sourceSatisfiedSet[path]; sourceSatisfied {
			continue
		}
		filteredEvidence = append(filteredEvidence, evidence)
	}
	artifact.Evidence = filteredEvidence
	knownEvidence := map[string]struct{}{}
	for _, evidence := range artifact.Evidence {
		path := normalizeRunnerPath(evidence.Path)
		kind := strings.TrimSpace(evidence.Kind)
		if path != "" && kind != "" {
			knownEvidence[path+"|"+kind] = struct{}{}
		}
	}
	for _, path := range sourceSatisfiedPaths {
		evidence := planThenExecuteObservedSourceEvidenceForPath(results, path)
		if evidence.Level != planThenExecuteEvidenceAnchorReady {
			continue
		}
		kind := "harness_observed_source"
		key := normalizeRunnerPath(path) + "|" + kind
		if _, ok := knownEvidence[key]; ok {
			continue
		}
		artifact.Evidence = append(artifact.Evidence, workspace.PlanArtifactEvidence{
			Kind:       kind,
			Path:       path,
			Summary:    util.FirstNonEmpty(strings.TrimSpace(evidence.Detail), "current-state source evidence") + " via " + util.FirstNonEmpty(strings.TrimSpace(evidence.Source), "source tool"),
			Confidence: "observed",
		})
		knownEvidence[key] = struct{}{}
	}
	req.PlanArtifact = &artifact
	return req
}

func planThenExecuteObservedSourceEvidenceForPath(results []workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}
	best := planThenExecuteSourceEvidence{Path: path}
	for _, result := range results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if planThenExecuteEvidenceRank(evidence.Level) > planThenExecuteEvidenceRank(best.Level) {
			best = evidence
		}
	}
	return best
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
				planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
				!successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, target) {
				return "", "", false
			}
			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			nextInstruction := planThenExecuteSourceReadInstruction(next, results)
			return "Bounded submit_plan anchor refresh required",
				"the previous submit_plan named an invalid edit anchor on " + target + ". Refresh that anchor with a source-body request such as bounded `read_file` with `start_line`/`end_line` or " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + ", then submit a corrected plan. Do not use whole-file `read_file` for this refresh because it can redirect to an outline and still leave no edit anchor. Remaining unread required path also needs source evidence: " + nextInstruction + ".",
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
	if planThenExecutePathHasOutlineEvidence(path, results) {
		return "read source for " + path + " using a source-body route: " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + " for the relevant declaration, or `read_file` with `start_line`/`end_line` copied from the outline. Do not repeat whole-file `read_file`; it can redirect to an outline and still leave the path unread"
	}
	return "read source for " + path + " with `read_file`; if the file is large or redirects to an outline, follow with " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + " or bounded `read_file` using `start_line`/`end_line`"
}

func planThenExecutePathHasOutlineEvidence(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
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
		return workspace.CodeNavigateCapabilityPredicate(workspace.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(workspace.CapExtractBody)(capability) ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body")
	default:
		return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
	}
}

func planThenExecuteAnchorRefreshToolNames() []string {
	return []string{"read_file", "code_navigate"}
}

func planThenExecuteAnchorRefreshRequestAllowed(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return req.StartLine > 0 && req.EndLine >= req.StartLine && req.EndLine-req.StartLine+1 <= 400
	case "code_navigate":
		capability := strings.TrimSpace(req.Capability)
		return workspace.CodeNavigateCapabilityPredicate(workspace.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(workspace.CapExtractBody)(capability)
	default:
		return false
	}
}

func planThenExecuteInvalidAnchorRefreshRequestAllowed(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return true
	}
	path = normalizeRunnerPath(path)
	if path == "" || !strings.EqualFold(filepath.Ext(path), ".go") {
		return true
	}
	if planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) {
		return true
	}
	return !planThenExecuteLargePathSeen(path, results)
}

func planThenExecuteReadFileCoversRejectedSyntaxLine(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}
	line, ok := submitPlanSyntaxValidationLineForPath(rejected, path)
	if !ok {
		return false
	}
	return req.StartLine <= line && line <= req.EndLine
}

func submitPlanSyntaxValidationLineForPath(result workspace.ToolResult, path string) (int, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return 0, false
	}
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	if !strings.Contains(strings.ToLower(text), "preflight syntax validation failed") {
		return 0, false
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(line), "preflight syntax validation failed") || !strings.Contains(line, path) {
			continue
		}
		idx := strings.Index(line, " at line ")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len(" at line "):])
		var digits strings.Builder
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			digits.WriteRune(r)
		}
		if digits.Len() == 0 {
			continue
		}
		parsed, err := strconv.Atoi(digits.String())
		if err == nil && parsed > 0 {
			return parsed, true
		}
	}
	return 0, false
}

func planThenExecuteLargePathSeen(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		text := strings.Join([]string{result.Summary, result.Output}, "\n")
		if !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}
		total, ok := planThenExecuteReadSummaryTotalLines(text)
		if ok && total >= planThenExecuteLargeFileGenericHeaderMinTotalLines {
			return true
		}
	}
	return false
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
	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
		target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
		required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
		_, inRequiredScope := required[target]
		if submitPlanRejectionHasInvalidAnchor(rejected) &&
			target != "" &&
			inRequiredScope &&
			submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
			planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
			!successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, target) {
			return "", "", false
		}
		if refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt); len(refreshPaths) > 0 {
			return "Bounded submit_plan anchor refresh required",
				"the previous `submit_plan` was rejected before it became a valid plan artifact, and the corrected plan still needs bounded source-body refresh for: " + strings.Join(nonEmptyRunnerStrings(refreshPaths, 4), ", ") + ". " + lastSubmitPlanRejectionBrief(rejected) + "Use exactly one bounded `read_file` with `start_line`/`end_line` or `" + workspace.CodeNavigateToolName + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " for one of those paths. Do not call `submit_plan` until the remaining anchor refresh returns.",
				true
		}
		if pendingHistory := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pendingHistory) > 0 {
			if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
				return "", "", false
			}
			return "Git history evidence required",
				"the rejected `submit_plan` anchor refresh is complete, but git history evidence is still required before corrected submit_plan. Do not perform additional source reads or search. Call " + planExecuteHistoryPendingToolListForPrompt() + " for the intended logic in: " + strings.Join(nonEmptyRunnerStrings(pendingHistory, 4), ", ") + ".",
				true
		}
		refreshNote := planThenExecutePostSubmitRejectionRefreshNote(brief, results, rejected, rejectedAt)
		return "Corrected submit_plan required",
			"every required_edit_path has satisfied its source-body obligation, but the previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(rejected) + "Submit a corrected parser-readable `submit_plan` request whose coverage includes every required_edit_path with either valid executable edits from existing source-body evidence or `source_satisfied_paths` for paths already at final state. Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not call read/search/discovery in submit-correction mode. " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, rejected) + " " + refreshNote,
			true
	}
	if len(planThenExecuteUnreadRequiredPaths(brief, results)) > 0 {
		return "", "", false
	}
	if pendingHistory := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pendingHistory) > 0 {
		if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
			return "", "", false
		}
		return "Git history evidence required",
			"every required_edit_path has satisfied its source-body obligation, but git history evidence is still required before submit_plan. Do not perform additional source reads or search. Call " + planExecuteHistoryPendingToolListForPrompt() + " for the intended logic in: " + strings.Join(nonEmptyRunnerStrings(pendingHistory, 4), ", ") + ", answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker.",
			true
	}
	if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
		return "", "", false
	}
	return "Plan submission required",
		"every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Or answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker. " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		true
}

func planThenExecutePostSourceHistoryRequestAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !gitHistoryEvidenceTool(req.Tool) {
		return false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}
	for _, pending := range planThenExecuteHistoryPendingPaths(brief, results, nil) {
		if normalizeRunnerPath(pending) == target {
			return true
		}
	}
	return false
}

func planThenExecutePostSubmitRejectionRefreshNote(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	refreshable := make([]string, 0, 2)
	for _, rawPath := range planThenExecuteRefreshScopePaths(brief, results) {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, invalid := invalidAnchorPaths[path]; invalid && !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
			refreshable = append(refreshable, path)
			continue
		}
	}
	if len(refreshable) == 0 {
		return "No further read/search/discovery action is currently allowed for this rejected plan; use the existing source-body evidence to submit corrected edits now."
	}
	return "The only remaining read exception is one bounded source-body refresh for: " + strings.Join(nonEmptyRunnerStrings(refreshable, 4), ", ") + ". For `" + workspace.CodeNavigateToolName + "`, include explicit " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " plus `pattern`. Otherwise do not call read/search/discovery before the corrected `submit_plan`."
}

func planThenExecuteRejectedPlanSourceRefreshAllowed(req workspace.ToolRequest, requiredCoveragePaths []string, results []workspace.ToolResult) bool {
	return planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req, requiredCoveragePaths, results)
}

func planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req workspace.ToolRequest, scopePaths []string, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}
	if !planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) {
		return false
	}
	scope := normalizedRequiredEditPathSet(scopePaths)
	if _, ok := scope[target]; !ok {
		return false
	}
	if _, invalidAnchor := submitPlanInvalidAnchorPaths(rejected)[target]; invalidAnchor {
		if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, target) {
			return false
		}
	} else if successfulSourceEvidencePathAfter(results, rejectedAt, target) {
		return false
	}
	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	if submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) {
		return true
	}
	return false
}

func submitPlanRejectionHasInvalidAnchor(result workspace.ToolResult) bool {
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return len(details.InvalidAnchors) > 0
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error))
	return strings.Contains(text, "invalid edit anchor") ||
		strings.Contains(text, "old_text not found") ||
		strings.Contains(text, "line range outside current source") ||
		strings.Contains(text, "preflight syntax validation failed")
}

func submitPlanInvalidAnchorPaths(result workspace.ToolResult) map[string]struct{} {
	out := submitPlanSyntaxValidationPaths(result)
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		for path := range submitPlanPathMapFromDiagnostics(details.InvalidAnchors, true) {
			out[path] = struct{}{}
		}
		return out
	}
	text := result.Summary + "\n" + result.Error
	for path := range submitPlanPathMapFromDiagnostics(backtickedSubmitPlanDiagnostics(text), true) {
		out[path] = struct{}{}
	}
	return out
}

func submitPlanSyntaxValidationPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	if !strings.Contains(strings.ToLower(text), "preflight syntax validation failed") {
		return out
	}
	for _, path := range pathparse.MentionedWorkspaceFiles(text) {
		path = normalizeRunnerPath(path)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func submitPlanMissingRequiredPaths(result workspace.ToolResult) map[string]struct{} {
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return submitPlanPathMapFromDiagnostics(details.MissingRequiredPaths, false)
	}
	text := result.Summary + "\n" + result.Error
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "missing required edit path")
	if idx < 0 {
		return nil
	}
	section := text[idx:]
	sectionLower := lower[idx:]
	for _, marker := range []string{"invalid edit anchor", "path(s) outside", "required edit snippet", ". submit", ". answer"} {
		if cut := strings.Index(sectionLower, marker); cut > 0 {
			section = section[:cut]
			sectionLower = sectionLower[:cut]
		}
	}
	return submitPlanPathMapFromDiagnostics(strings.Split(section, "`"), false)
}

func submitPlanMissingSourceGroundingPaths(result workspace.ToolResult) map[string]struct{} {
	if !planThenExecuteSubmitPlanGroundingRejectedResult(result) {
		return nil
	}
	text := result.Summary + "\n" + result.Error
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "missing source-body evidence for")
	if idx < 0 {
		return nil
	}
	section := text[idx:]
	sectionLower := lower[idx:]
	for _, marker := range []string{". read each", ". if the feature", ". if no concrete", ". submit"} {
		if cut := strings.Index(sectionLower, marker); cut > 0 {
			section = section[:cut]
			sectionLower = sectionLower[:cut]
		}
	}
	return submitPlanPathMapFromDiagnostics(backtickedSubmitPlanDiagnostics(section), false)
}

func backtickedSubmitPlanDiagnostics(text string) []string {
	parts := strings.Split(text, "`")
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		if segment := strings.TrimSpace(parts[i]); segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

func submitPlanPathMapFromDiagnostics(segments []string, allowColon bool) map[string]struct{} {
	out := make(map[string]struct{})
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if strings.Contains(segment, ":") {
			if !allowColon {
				continue
			}
			segment = strings.SplitN(segment, ":", 2)[0]
		} else if allowColon {
			continue
		}
		path := normalizeRunnerPath(segment)
		if planThenExecuteRecoveryScopePathAllowed(path) {
			out[path] = struct{}{}
		}
	}
	return out
}

func planThenExecuteRecoveryScopePathAllowed(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || submitPlanDiagnosticProtocolToken(path) {
		return false
	}
	if pathparse.LooksLikeWorkspacePathToken(path) || pathparse.LooksLikeWorkspaceFilePath(path) {
		return true
	}
	base := filepath.Base(path)
	if strings.Contains(base, ".") && !strings.HasSuffix(base, ".") && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}
	if strings.HasPrefix(base, ".") && len(base) > 1 && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}
	switch base {
	case "Brewfile", "Justfile", "Pipfile", "Rakefile", "Taskfile":
		return true
	default:
		return false
	}
}

func submitPlanDiagnosticProtocolToken(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	if lower == "" {
		return true
	}
	if !strings.ContainsAny(lower, "abcdefghijklmnopqrstuvwxyz0123456789.") {
		return true
	}
	if strings.Contains(lower, "<<question>>") || lower == "decision" || strings.HasPrefix(lower, "decision ") {
		return true
	}
	switch lower {
	case "read_file", workspace.CodeNavigateToolName, "read_symbol", "extract_body", "submit_plan":
		return true
	default:
		return false
	}
}

func submitPlanInvalidAnchorPathsAllowsRead(paths map[string]struct{}, path string) bool {
	if len(paths) == 0 {
		return false
	}
	_, ok := paths[normalizeRunnerPath(path)]
	return ok
}

func successfulSourceEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for i := index + 1; i < len(results); i++ {
		if planThenExecuteEvidenceRank(planThenExecuteToolResultSourceEvidence(results[i], path).Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {
			return true
		}
	}
	return false
}

func successfulAnchorReadySourceEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for i := index + 1; i < len(results); i++ {
		if planThenExecuteToolResultSourceEvidence(results[i], path).Level == planThenExecuteEvidenceAnchorReady {
			return true
		}
	}
	return false
}

func successfulInvalidAnchorCorrectionEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for i := index + 1; i < len(results); i++ {
		if invalidAnchorCorrectionEvidenceResult(results[i], path, results[:i+1]) {
			return true
		}
	}
	return false
}

func invalidAnchorCorrectionEvidenceResult(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	evidence := planThenExecuteToolResultSourceEvidence(result, path)
	if evidence.Level != planThenExecuteEvidenceAnchorReady {
		return false
	}
	if strings.TrimSpace(result.Tool) != "read_file" {
		return true
	}
	_, end, hasRange := planThenExecuteReadSummaryLineRange(result.Summary)
	total, hasTotal := planThenExecuteReadSummaryTotalLines(result.Summary)
	if hasRange && hasTotal && end >= total {
		return true
	}
	if planThenExecuteReadFileCoversRejectedSyntaxLine(workspace.ToolRequest{
		Tool: "read_file",
		Path: path,
		StartLine: func() int {
			start, _, _ := planThenExecuteReadSummaryLineRange(result.Summary)
			return start
		}(),
		EndLine: end,
	}, path, results) {
		return true
	}
	return hasRange
}

func successfulSourceBodyReadPathBefore(results []workspace.ToolResult, index int, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	if index > len(results) {
		index = len(results)
	}
	for i := 0; i < index; i++ {
		if planThenExecuteToolResultSourceEvidence(results[i], path).Level == planThenExecuteEvidenceAnchorReady {
			return true
		}
	}
	return false
}

func lastRejectedSubmitPlanCoverageResult(results []workspace.ToolResult) (workspace.ToolResult, int, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			continue
		}
		if planThenExecuteSubmitPlanRejectedResult(result) {
			return result, i, true
		}
	}
	return workspace.ToolResult{}, -1, false
}

func planThenExecuteSubmitPlanRejectedForCorrection(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == "submit_plan" &&
		planThenExecuteSubmitPlanRejectedResult(result) &&
		!planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
}

func planThenExecuteSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}
	if _, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return true
	}
	return strings.Contains(result.Summary, "submit_plan rejected") || strings.Contains(result.Error, "submit_plan must include")
}

func planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}
	if strings.TrimSpace(result.Code) == workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode {
		return true
	}
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error}, "\n"))
	return strings.Contains(text, "source-satisfied terminal shape required") ||
		strings.Contains(text, "source-satisfied rationale")
}

func planThenExecuteSourceSatisfiedTerminalRequiredPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	scopeLine := "Use the relevant selected edit paths from gathered current-source evidence."
	if len(required) > 0 {
		scopeLine = "Required path(s): " + strings.Join(required, ", ") + "."
	}
	return strings.Join([]string{
		"The previous `submit_plan` attempted a source-satisfied/no-op closeout using the wrong transport. That rejected tool call is not a valid plan artifact and is not an accepted source-satisfied terminal answer.",
		"",
		lastSubmitPlanRejectionBrief(result),
		scopeLine,
		"",
		"The next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required or selected path already contains the requested final state.",
		"2. `submit_plan` with real executable edits for every required or selected path if changes are needed.",
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty `submit_plan`, and do not treat the rejected tool call as plan coverage.",
	}, "\n")
}

func planThenExecuteSubmitPlanGroundingRejectedResult(result workspace.ToolResult) bool {
	return strings.TrimSpace(result.Tool) == "submit_plan" &&
		strings.Contains(strings.ToLower(result.Summary+"\n"+result.Error), "edit paths lack source grounding")
}

func submitPlanRejectionHasLargeInsertAfterLine(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))
	return strings.Contains(text, "insert_after_line content exceeds inline submit_plan limit") ||
		(strings.Contains(text, "insert_after_line") && strings.Contains(text, "content_ref is not supported"))
}

func submitPlanRejectionHasInvalidEditShape(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))
	return strings.Contains(text, "invalid_edit_shape") ||
		strings.Contains(text, "requires one edit shape") ||
		strings.Contains(text, "requires one executable edit shape") ||
		strings.Contains(text, "mutually exclusive") ||
		strings.Contains(text, "line-range edits require non-empty content") ||
		strings.Contains(text, "insert_after_line edits require non-empty content")
}

func lastSubmitPlanRejectionBrief(result workspace.ToolResult) string {
	text := strings.TrimSpace(result.Error)
	if text == "" {
		text = strings.TrimSpace(result.Summary)
	}
	if text == "" {
		return ""
	}
	if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
		lower := strings.ToLower(text)
		for _, marker := range []string{". read each existing edit path", ". if the feature is already present", ". if no concrete path can be identified"} {
			if cut := strings.Index(lower, marker); cut > 0 {
				text = strings.TrimSpace(text[:cut]) + "."
				break
			}
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	const limit = 720
	if len(text) > limit {
		const head = 360
		const tail = 300
		text = strings.TrimSpace(text[:head]) + " ... " + strings.TrimSpace(text[len(text)-tail:])
	}
	return "Last submit_plan rejection: " + text + " "
}

func lastSubmitPlanCorrectionRejectionBrief(result workspace.ToolResult) string {
	return sanitizeSubmitCorrectionRejectionBrief(lastSubmitPlanRejectionBrief(result))
}

func sanitizeSubmitCorrectionRejectionBrief(text string) string {
	replacements := map[string]string{
		"answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"If every required path is already final, answer `Decision: source-satisfied` with per-path current-state evidence instead of using submit_plan": "Do not use terminal source-satisfied in submit-correction mode",
		"or answer `Decision: source-satisfied` if every required path is final":                                                                         "or use `source_satisfied_paths` for already-final paths",
		"answer `Decision: source-satisfied` if all paths are final":                                                                                     "use `source_satisfied_paths` for already-final paths",
		"if all paths are final answer `Decision: source-satisfied`; else":                                                                               "",
		"use `Decision: source-satisfied` only if all paths are final; else":                                                                             "",
	}
	for old, replacement := range replacements {
		text = strings.ReplaceAll(text, old, replacement)
	}
	return text
}

func planThenExecuteUnreadRequiredPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]struct{} {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	unread := make(map[string]struct{}, len(required))
	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}
		unread[path] = struct{}{}
	}
	return unread
}

func planThenExecutePendingRequiredSourceEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []planThenExecuteSourceEvidence {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	pending := make([]planThenExecuteSourceEvidence, 0, len(required))
	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}
		evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}
		if evidence.Path == "" {
			evidence.Path = path
		}
		if evidence.Level == planThenExecuteEvidenceNone {
			evidence.Level = planThenExecuteEvidenceNone
			evidence.Detail = "no source evidence"
		}
		pending = append(pending, evidence)
	}
	return pending
}

func planThenExecutePendingRequiredSourceEvidenceSummary(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) string {
	pending := planThenExecutePendingRequiredSourceEvidence(brief, results)
	if len(pending) == 0 {
		return ""
	}
	if max <= 0 || max > len(pending) {
		max = len(pending)
	}
	parts := make([]string, 0, max)
	for _, evidence := range pending[:max] {
		level := string(evidence.Level)
		if level == "" {
			level = "none"
		}
		detail := strings.TrimSpace(evidence.Detail)
		if detail != "" {
			parts = append(parts, evidence.Path+"("+level+": "+detail+")")
		} else {
			parts = append(parts, evidence.Path+"("+level+")")
		}
	}
	if len(pending) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
	}
	return strings.Join(parts, ", ")
}

type planThenExecuteRequiredPathTypedState string

const (
	planThenExecuteRequiredPathExistingSourceBodyRequired planThenExecuteRequiredPathTypedState = "existing_source_body_required"
	planThenExecuteRequiredPathNewFileBodyRequired        planThenExecuteRequiredPathTypedState = "new_file_body_required"
	planThenExecuteRequiredPathSourceSatisfied            planThenExecuteRequiredPathTypedState = "source_satisfied"
	planThenExecuteRequiredPathStaleAnchor                planThenExecuteRequiredPathTypedState = "stale_anchor"
	planThenExecuteRequiredPathBlocked                    planThenExecuteRequiredPathTypedState = "blocked"
)

func (state planThenExecuteRequiredPathTypedState) requiresSourceBodyRead() bool {
	switch state {
	case planThenExecuteRequiredPathExistingSourceBodyRequired:
		return true
	default:
		return false
	}
}

func planThenExecuteRequiredPathState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, path string) planThenExecuteRequiredPathTypedState {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteRequiredPathBlocked
	}
	if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results) {
		return planThenExecuteRequiredPathNewFileBodyRequired
	}
	if planThenExecuteRequiredPathHasBlockerEvidence(path, results) {
		return planThenExecuteRequiredPathBlocked
	}
	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		if _, stale := submitPlanInvalidAnchorPaths(rejected)[path]; stale && !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
			return planThenExecuteRequiredPathStaleAnchor
		}
	}
	if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
		return planThenExecuteRequiredPathSourceSatisfied
	}
	return planThenExecuteRequiredPathExistingSourceBodyRequired
}

func planThenExecuteRequiredPathHasBlockerEvidence(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if planThenExecuteResultIsHarnessDiagnostic(result) {
			continue
		}
		if strings.TrimSpace(result.Tool) == "submit_plan" || strings.TrimSpace(result.Tool) == "completion_gate" {
			continue
		}
		text := strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output)
		if !strings.Contains(text, path) {
			continue
		}
		lower := strings.ToLower(text)
		if strings.Contains(lower, "<<question>>") ||
			strings.Contains(lower, "path-specific blocker") ||
			strings.Contains(lower, "blocked required") {
			return true
		}
	}
	return false
}

func planThenExecuteResultIsHarnessDiagnostic(result workspace.ToolResult) bool {
	tool := strings.ToLower(strings.TrimSpace(result.Tool))
	if strings.Contains(tool, "autonomy") ||
		strings.Contains(tool, "gate") ||
		strings.Contains(tool, "guard") {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))
	if strings.Contains(text, `"safe_alternatives"`) ||
		strings.Contains(text, `"gate"`) ||
		strings.Contains(text, `"guard_friction"`) ||
		strings.Contains(text, "runner_completion_gate") ||
		strings.Contains(text, "runner_budget_gate") {
		return true
	}
	return false
}

func planThenExecuteRequiredPathStateSummaries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	out := make([]string, 0, len(required))
	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		out = append(out, normalized+":"+string(planThenExecuteRequiredPathState(brief, results, normalized)))
	}
	return out
}

type planThenExecuteSourceEvidenceLevel string

const (
	planThenExecuteEvidenceNone                planThenExecuteSourceEvidenceLevel = ""
	planThenExecuteEvidencePathSeen            planThenExecuteSourceEvidenceLevel = "path_seen"
	planThenExecuteEvidenceSourceBodyPending   planThenExecuteSourceEvidenceLevel = "source_body_pending"
	planThenExecuteEvidenceBodyWindow          planThenExecuteSourceEvidenceLevel = "body_window"
	planThenExecuteEvidenceAnchorReady         planThenExecuteSourceEvidenceLevel = "anchor_ready"
	planThenExecuteEvidenceExactAnchorContract planThenExecuteSourceEvidenceLevel = "exact_anchor_contract"
	planThenExecuteEvidenceNewFileContract     planThenExecuteSourceEvidenceLevel = "new_file_contract"
)

type planThenExecuteSourceEvidence struct {
	Path   string
	Level  planThenExecuteSourceEvidenceLevel
	Source string
	Detail string
}

func planThenExecuteRequiredSourceEvidenceForPath(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}
	best := planThenExecuteSourceEvidence{Path: path}
	if planThenExecuteRequiredEditSnippetCoversPath(brief, path) {
		best.Level = planThenExecuteEvidenceAnchorReady
		best.Source = "required_edit_snippet"
		best.Detail = "exact current-state/edit snippet"
	}
	for _, result := range results {
		evidence := planThenExecuteToolResultSourceEvidence(result, path)
		if planThenExecuteEvidenceRank(evidence.Level) > planThenExecuteEvidenceRank(best.Level) {
			best = evidence
		}
	}
	return best
}

func planThenExecuteRequiredEditSnippetCoversPath(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, snippet := range workerruntime.NormalizeRuntimeBrief(brief).RequiredEditSnippets {
		if normalizeRunnerPath(snippet.Path) == path && strings.TrimSpace(snippet.Content) != "" {
			return true
		}
	}
	return false
}

func planThenExecuteToolResultSourceEvidence(result workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = normalizeRunnerPath(path)
	if path == "" || isNonSourceEvidenceArtifactPath(path) || strings.TrimSpace(result.Error) != "" {
		return planThenExecuteSourceEvidence{}
	}
	text := strings.Join([]string{result.Summary, result.Output}, "\n")
	evidence := planThenExecuteSourceEvidence{Path: path}
	if strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Source = strings.TrimSpace(result.Tool)
		evidence.Detail = "path mentioned by tool result"
	}
	sourcePaths := readPathsFromToolResult(result)
	sourceMatched := false
	for _, readPath := range sourcePaths {
		if isNonSourceEvidenceArtifactPath(readPath) {
			continue
		}
		if normalizeRunnerPath(readPath) == path {
			sourceMatched = true
			break
		}
	}
	if !sourceMatched {
		return evidence
	}
	tool := strings.TrimSpace(result.Tool)
	evidence.Level = planThenExecuteEvidenceAnchorReady
	evidence.Source = tool
	evidence.Detail = "source body"
	switch {
	case tool == "read_file":
		evidence.Level, evidence.Detail = planThenExecuteReadFileEvidenceLevel(result, path)
	case tool == "code_navigate" || tool == "read_only_capability":
		if !routedSourceReadOutputHasBody(result.Output) {
			evidence.Level = planThenExecuteEvidencePathSeen
			evidence.Detail = "navigation result without source body"
		} else {
			evidence.Detail = "routed source body"
		}
	case strings.HasSuffix(tool, "_file_outline"):
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Detail = "outline only"
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		evidence.Detail = "symbol/body read"
	}
	return evidence
}

func planThenExecuteReadFileEvidenceLevel(result workspace.ToolResult, path string) (planThenExecuteSourceEvidenceLevel, string) {
	start, end, hasRange := planThenExecuteReadSummaryLineRange(result.Summary)
	total, hasTotal := planThenExecuteReadSummaryTotalLines(result.Summary)
	lower := strings.ToLower(result.Summary + "\n" + result.Output)
	if strings.Contains(lower, "bounded large required-path source") ||
		strings.Contains(lower, "full range-less read omitted") ||
		strings.Contains(lower, "output was capped") ||
		strings.Contains(lower, "defaulted to recent log tail") {
		return planThenExecuteEvidenceBodyWindow, "bounded body window"
	}
	if hasRange && hasTotal && end < total {
		if planThenExecuteGenericHeaderWindow(start, end, total, true) {
			return planThenExecuteEvidenceBodyWindow, "generic first-window read"
		}
		if planThenExecuteLargeGoRawRangeNeedsSymbolBody(path, total) {
			return planThenExecuteEvidenceBodyWindow, "large Go raw range; use symbol/body route"
		}
		return planThenExecuteEvidenceAnchorReady, "bounded source range"
	}
	if hasRange && hasTotal && end >= total {
		return planThenExecuteEvidenceAnchorReady, "whole-file/current-state read"
	}
	if hasRange {
		if planThenExecuteGenericHeaderWindow(start, end, 0, false) {
			return planThenExecuteEvidenceBodyWindow, "generic first-window read without file-size evidence"
		}
		return planThenExecuteEvidenceAnchorReady, "bounded source range"
	}
	return planThenExecuteEvidenceBodyWindow, "source body window"
}

const planThenExecuteGenericHeaderReadMaxLines = 220
const planThenExecuteLargeFileGenericHeaderReadMaxLines = 400
const planThenExecuteLargeFileGenericHeaderMinTotalLines = 1000

func planThenExecuteLargeGoRawRangeNeedsSymbolBody(path string, total int) bool {
	return total >= planThenExecuteLargeFileGenericHeaderMinTotalLines &&
		strings.EqualFold(filepath.Ext(normalizeRunnerPath(path)), ".go")
}

func planThenExecuteGenericHeaderWindow(start, end, total int, hasTotal bool) bool {
	if start != 1 || end < start {
		return false
	}
	if end <= planThenExecuteGenericHeaderReadMaxLines {
		return true
	}
	return hasTotal && total >= planThenExecuteLargeFileGenericHeaderMinTotalLines && end <= planThenExecuteLargeFileGenericHeaderReadMaxLines
}

func planThenExecuteEvidenceRank(level planThenExecuteSourceEvidenceLevel) int {
	switch level {
	case planThenExecuteEvidenceAnchorReady, planThenExecuteEvidenceExactAnchorContract, planThenExecuteEvidenceNewFileContract:
		return 3
	case planThenExecuteEvidenceBodyWindow:
		return 2
	case planThenExecuteEvidenceSourceBodyPending, planThenExecuteEvidencePathSeen:
		return 1
	default:
		return 0
	}
}

func planThenExecuteReadSummaryLineRange(summary string) (int, int, bool) {
	fields := strings.Fields(summary)
	for i, field := range fields {
		if field != "lines" || i+1 >= len(fields) {
			continue
		}
		startText, endText, ok := strings.Cut(strings.Trim(fields[i+1], ".,;:)"), "-")
		if !ok {
			return 0, 0, false
		}
		start, startErr := strconv.Atoi(startText)
		end, endErr := strconv.Atoi(endText)
		if startErr != nil || endErr != nil || start <= 0 || end < start {
			return 0, 0, false
		}
		return start, end, true
	}
	return 0, 0, false
}

func planThenExecuteReadSummaryTotalLines(summary string) (int, bool) {
	lower := strings.ToLower(summary)
	if total, ok := planThenExecuteReadSummaryTotalLinesAfter(lower, "total lines:"); ok {
		return total, true
	}
	return planThenExecuteReadSummaryTotalLinesAfter(lower, "file has ")
}

func planThenExecuteReadSummaryTotalLinesAfter(lower string, marker string) (int, bool) {
	idx := strings.Index(lower, "file has ")
	if marker != "file has " {
		idx = strings.Index(lower, marker)
	}
	if idx < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(lower[idx+len(marker):])
	if marker == "file has " {
		end := strings.Index(rest, " lines total")
		if end < 0 {
			return 0, false
		}
		rest = rest[:end]
	} else {
		rest = strings.TrimLeft(rest, " ")
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			return 0, false
		}
		rest = rest[:end]
	}
	total, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
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
				"Use the current planner terminal contract instead; in submit-correction mode, emit corrected `submit_plan` only.",
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
	edits := reviewRepairSubmitPlanEdits(req)
	if len(edits) == 0 || planEditsHaveNoConcretePath(edits) {
		return reviewRepairNoopSubmitPlanIntercept("no concrete corrective edit paths were provided")
	}
	if r.Workspace != nil && reviewRepairPlanEditsAlreadyApplied(r.Workspace.Root(), edits) {
		return reviewRepairNoopSubmitPlanIntercept("all submitted corrective edits already match the current workspace")
	}
	return verificationInterceptResult{}
}

const planThenExecuteReviewRepairMaxRawReadLines = 200

func (r *Runner) preflightPlanThenExecuteReviewRepairBroadReadGuard(req workspace.ToolRequest) verificationInterceptResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return verificationInterceptResult{}
	}
	if strings.TrimSpace(req.Tool) != "read_file" {
		return verificationInterceptResult{}
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || filepath.IsAbs(path) {
		return verificationInterceptResult{}
	}
	start, end := req.StartLine, req.EndLine
	if start > 0 && end > 0 && start > end {
		start, end = end, start
	}
	if start > 0 && end > 0 {
		if end-start+1 <= planThenExecuteReviewRepairMaxRawReadLines {
			return verificationInterceptResult{}
		}
		return reviewRepairBroadReadIntercept(path, fmt.Sprintf("requested %d lines", end-start+1))
	}
	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return reviewRepairBroadReadIntercept(path, "unbounded read_file request")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return verificationInterceptResult{}
	}
	body, err := os.ReadFile(filepath.Join(root, clean))
	if err != nil {
		return verificationInterceptResult{}
	}
	lines := len(strings.Split(string(body), "\n"))
	if len(body) > 0 && body[len(body)-1] == '\n' {
		lines--
	}
	if lines <= planThenExecuteReviewRepairMaxRawReadLines {
		return verificationInterceptResult{}
	}
	return reviewRepairBroadReadIntercept(path, fmt.Sprintf("unbounded read_file would return %d lines", lines))
}

func reviewRepairBroadReadIntercept(path string, reason string) verificationInterceptResult {
	if strings.TrimSpace(reason) == "" {
		reason = "raw read is too broad for review-repair"
	}
	return verificationInterceptResult{
		Intercepted: true,
		Reason:      "review-repair broad read suppressed: " + reason,
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Review-repair broad read rejected",
			Error: strings.Join([]string{
				fmt.Sprintf("review-repair already has Phase Evidence, Diff Evidence, submitted_plan_state, and any inherited context packet; broad raw reads are not valid review evidence (%s).", reason),
				fmt.Sprintf("Use context_status refresh=true, context_query/context_zoom/code_navigate, or read_file with start_line/end_line covering at most %d lines for one named missing checklist fact in %s.", planThenExecuteReviewRepairMaxRawReadLines, path),
				"Then close with `Decision: pass` plus a `Verification Checklist`, a corrective `submit_plan`, or `<<QUESTION>>:`.",
			}, " "),
		},
	}
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

func reviewRepairNoopSubmitPlanCloseoutRequired(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily &&
		strings.TrimSpace(req.Tool) == "submit_plan" &&
		strings.TrimSpace(result.Tool) == "review_repair_noop_plan" &&
		strings.TrimSpace(result.Error) == ""
}

func reviewRepairNoopSubmitPlanPassCloseout(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily ||
		strings.TrimSpace(req.Tool) != "submit_plan" ||
		len(reviewRepairSubmitPlanEdits(req)) > 0 {
		return "", false
	}
	rationale := strings.TrimSpace(req.Rationale)
	if rationale == "" ||
		!plancontract.ReviewRepairAnswerDeclaresPass(rationale) ||
		!plancontract.ReviewRepairTextHasChecklist(rationale) ||
		reviewRepairChecklistDeclaresFailingItem(rationale) {
		return "", false
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "", false
	}
	if _, ok := reviewRepairNoopSubmitPlanEvidencePassCloseout(brief); !ok {
		return "", false
	}
	return normalizeReviewRepairPassCloseout(rationale), true
}

type reviewRepairTerminalCloseout struct {
	Answer       string
	EventRole    string
	EventContent string
	StopReason   string
}

func reviewRepairTerminalCloseoutForRequest(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (reviewRepairTerminalCloseout, bool) {
	if answer, ok := reviewRepairNoopSubmitPlanPassCloseout(brief, req); ok {
		return reviewRepairTerminalCloseout{
			Answer:       answer,
			EventRole:    "plan_then_execute_review_repair_noop_pass",
			EventContent: "review-repair no-op submit_plan carried valid phase/pass evidence; closing with Decision: pass",
			StopReason:   "review_repair_noop_plan_pass",
		}, true
	}
	if answer, ok := reviewRepairTerminalProbePassCloseout(brief, req); ok {
		return reviewRepairTerminalCloseout{
			Answer:       answer,
			EventRole:    "plan_then_execute_review_repair_evidence_pass",
			EventContent: "review-repair requested an unnecessary hidden probe after phase evidence already satisfied pass; closing with Decision: pass",
			StopReason:   "review_repair_evidence_pass_after_hidden_probe",
		}, true
	}
	return reviewRepairTerminalCloseout{}, false
}

func reviewRepairTerminalProbePassCloseout(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return "", false
	}
	if !reviewRepairTerminalProbeTool(req.Tool) {
		return "", false
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "", false
	}
	return reviewRepairNoopSubmitPlanEvidencePassCloseout(brief)
}

func normalizeReviewRepairPassCloseout(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "Decision: pass") {
			continue
		}
		kept = append(kept, line)
	}
	body := strings.TrimSpace(strings.Join(kept, "\n"))
	if body == "" {
		return "Decision: pass"
	}
	return "Decision: pass\n\n" + body
}

func reviewRepairTerminalProbeTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file", "search_files", "search_content", "file_stats", "code_navigate",
		"context_status", "context_query", "context_zoom", "context_pack", "context_impact",
		"discovery_query", "structural_search":
		return true
	default:
		return false
	}
}

func reviewRepairNoopSubmitPlanEvidencePassCloseout(brief workerruntime.WorkerRuntimeBrief) (string, bool) {
	evidence := strings.ToLower(reviewRepairTrustedPhaseEvidence(brief))
	if evidence == "" {
		return "", false
	}
	for _, required := range []string{
		"phase: review_repair",
		"submitted_plan_state:",
		"status=applied",
		"source_sanity:",
		"verification:",
		"patch_hygiene: status=passed",
	} {
		if !strings.Contains(evidence, required) {
			return "", false
		}
	}
	if !reviewRepairPhaseEvidenceSectionHasStatus(evidence, "source_sanity:", "status=ok") ||
		!reviewRepairPhaseEvidenceSectionHasStatus(evidence, "verification:", "status=ok") {
		return "", false
	}
	if reviewRepairPhaseEvidenceSectionHasEntries(evidence, "open_obligations:") {
		return "", false
	}
	if strings.Contains(evidence, "verification_failed:") ||
		strings.Contains(evidence, "verification_error:") ||
		strings.Contains(evidence, "source_sanity_failed:") ||
		strings.Contains(evidence, "source_sanity_error:") ||
		strings.Contains(evidence, "requested_test_edit_missing") ||
		strings.Contains(evidence, "requested_implementation_edit_missing") {
		return "", false
	}
	changed := nonEmptyRunnerStrings(brief.ChangedPaths, 6)
	if len(changed) == 0 {
		changed = nonEmptyRunnerStrings(brief.AllowedEditPaths, 6)
	}
	pathText := "the changed path(s)"
	if len(changed) > 0 {
		pathText = strings.Join(changed, ", ")
	}
	return strings.Join([]string{
		"Decision: pass",
		"",
		"Verification Checklist:",
		"- Required behavior: submitted_plan_state reports the planned edit applied for " + pathText + ".",
		"- Scope safety: Phase Evidence confines changed paths to the allowed review-repair scope.",
		"- Test or command evidence: Phase Evidence reports source_sanity status=ok, verification status=ok, and patch_hygiene status=passed.",
	}, "\n"), true
}

func reviewRepairPhaseEvidenceSectionHasStatus(evidence, sectionHeader, status string) bool {
	lines := strings.Split(evidence, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != sectionHeader {
			continue
		}
		for _, candidate := range lines[i+1:] {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" {
				continue
			}
			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				return false
			}
			if strings.Contains(trimmed, status) {
				return true
			}
		}
	}
	return false
}

func reviewRepairPhaseEvidenceSectionHasEntries(evidence, sectionHeader string) bool {
	lines := strings.Split(evidence, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != sectionHeader {
			continue
		}
		for _, candidate := range lines[i+1:] {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" {
				continue
			}
			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				return false
			}
			return true
		}
	}
	return false
}

func reviewRepairSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if len(req.Edits) == 0 && req.PlanArtifact == nil {
		return nil
	}
	edits := append([]workspace.PlanEdit(nil), req.Edits...)
	if req.PlanArtifact != nil {
		edits = append(edits, req.PlanArtifact.Edits...)
	}
	return edits
}

func reviewRepairChecklistDeclaresFailingItem(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if strings.HasPrefix(line, "-") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		}
		if strings.HasPrefix(line, "failing item:") {
			return true
		}
	}
	return false
}

func reviewRepairNoopSubmitPlanCloseoutPrompt(req workspace.ToolRequest, result workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("The review-repair `submit_plan` was ignored because it did not contain a new corrective edit. Tools are now disabled. ")
	b.WriteString("Use the review checklist and phase evidence already gathered to emit exactly one terminal response: ")
	b.WriteString("`Decision: pass` with the `Verification Checklist` if the checklist passes, or `<<QUESTION>>:` with the exact blocker if it does not. ")
	b.WriteString("Do not call `submit_plan` again unless there is a new concrete corrective edit, and do not narrate the ignored tool call as the outcome.")
	if rationale := strings.TrimSpace(req.Rationale); rationale != "" {
		b.WriteString("\n\nPrior checklist/rationale from the ignored request:\n")
		b.WriteString(rationale)
	}
	if output := strings.TrimSpace(result.Output); output != "" {
		b.WriteString("\n\nHarness note:\n")
		b.WriteString(output)
	}
	return b.String()
}

func reviewRepairNoopSubmitPlanCloseoutRejection(brief workerruntime.WorkerRuntimeBrief, answer string) (workspace.ToolResult, bool) {
	if planThenExecuteReviewRepairAnswerDeclaresBlocker(answer) {
		return workspace.ToolResult{}, false
	}
	if plancontract.ReviewRepairAnswerDeclaresPass(answer) && plancontract.ReviewRepairTextHasChecklist(answer) {
		if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Review-repair completion gate rejected contradictory pass",
				Error:   "review-repair phase evidence still has failed obligations " + quotedRunnerStrings(failed) + "; choose a corrective submit_plan inside allowed paths or `<<QUESTION>>:` with the exact blocker instead of `Decision: pass`.",
			}, true
		}
		return workspace.ToolResult{}, false
	}
	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error: strings.Join([]string{
			"review-repair must close with exactly one of:",
			"`Decision: pass` with a `Verification Checklist`,",
			"or `<<QUESTION>>:` naming the exact blocker after a no-op corrective plan.",
		}, " "),
	}, true
}

func reviewRepairNoopSubmitPlanDeterministicCloseout(brief workerruntime.WorkerRuntimeBrief, rejection workspace.ToolResult) string {
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "<<QUESTION>>: Review-repair phase evidence still has failed obligations: " + strings.Join(failed, ", ") + ". Submit a corrective plan inside the allowed repair paths or provide updated phase evidence before declaring pass."
	}
	if answer, ok := reviewRepairNoopSubmitPlanEvidencePassCloseout(brief); ok {
		return answer
	}
	errText := strings.TrimSpace(rejection.Error)
	if errText == "" {
		errText = "review-repair closeout did not produce `Decision: pass` with a `Verification Checklist` or a typed blocker"
	}
	return "<<QUESTION>>: " + errText
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
	// A rejected no-op submit_plan is still an invalid plan attempt. The
	// next model turn may explicitly choose Decision: source-satisfied with
	// current-state evidence, but the harness must not convert the rejected
	// tool call itself into a successful terminal artifact.
	return "", false
}

func planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	// A rejected empty submit_plan is not a terminal source-satisfied artifact.
	// The planner must answer with the explicit Decision: source-satisfied
	// terminal shape and current-state evidence on a model turn.
	return "", false
}

func planThenExecuteEmptySubmitPlanRejection(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}
	return strings.TrimSpace(result.Code) == workspace.SubmitPlanEmptyEditsCode
}

func planThenExecuteUnscopedEmptySubmitPlanRejected(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		return false
	}
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	return ok && planThenExecuteEmptySubmitPlanRejection(rejected)
}

func planThenExecuteUnscopedEmptySubmitPlanAttempt(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		strings.TrimSpace(req.Tool) != "submit_plan" {
		return false
	}
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		return false
	}
	return len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0
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
	return plancontract.SourceSatisfiedAnswerCoversPaths(answer, requiredPaths)
}

func PlanThenExecuteSourceSatisfiedMissingCoveragePaths(answer string, requiredPaths []string) []string {
	return plancontract.SourceSatisfiedMissingCoveragePaths(answer, requiredPaths)
}

// PlanThenExecuteSourceSatisfiedEvidencePaths extracts concrete path evidence
// from a source-satisfied answer. It is intentionally narrower than generic
// path parsing: the answer must first use the exact plan-execute closeout
// decision shape so blocker text that merely mentions the remedy is ignored.
func PlanThenExecuteSourceSatisfiedEvidencePaths(answer string) []string {
	return plancontract.SourceSatisfiedEvidencePaths(answer)
}

func planThenExecuteSourceSatisfiedAnswerIntent(answer string) bool {
	return plancontract.SourceSatisfiedAnswerIntent(answer)
}

func (r *Runner) planThenExecuteSourceSatisfiedAnswerCovers(reply string, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}
	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return false
	}
	missing := missingRequiredEditPaths(r.requiredEditCoveragePaths(), changedPathsFromToolResults(results))
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

func planThenExecuteSubmitCorrectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	return DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}).Mode == PlanExecutePlanningModeSubmitCorrection
}

func planThenExecuteSourceSatisfiedResultsCoverPaths(requiredPaths []string, results []workspace.ToolResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, path := range requiredPaths {
		if planThenExecuteRequiredSourceEvidenceForPath(workerruntime.WorkerRuntimeBrief{}, results, path).Level != planThenExecuteEvidenceAnchorReady {
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
// anchor-ready source evidence for every required_edit_path and has not
// yet called submit_plan. The nudge converts the planner's verification urge
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
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	if successfulSubmitPlanResultCount(results) > 0 {
		// Plan already submitted — the gate-exemption path will close
		// the runner cleanly; nothing to nudge.
		return ""
	}
	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && planThenExecuteSubmitCorrectionActive(brief, results) {
		return strings.Join([]string{
			"PLANNER SUBMIT-CORRECTION READY: every required_edit_path has source-body evidence, but the previous `submit_plan` was rejected before it became a valid plan artifact.",
			"",
			lastSubmitPlanCorrectionRejectionBrief(rejected),
			"The next visible action MUST be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
			planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, rejected),
			"",
			"Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not read/search/discovery again, and do not describe the correction in prose outside the parser-readable action.",
		}, "\n")
	}
	if len(required) == 0 {
		if len(readPathsFromResults(results)) == 0 {
			return ""
		}
		state := PlanExecutePlanningState{
			EvidencePaths: sortedRunnerPathKeys(readPathsFromResults(results)),
		}
		paths := planThenExecuteSelectedEditPaths(brief, results, state)
		if len(paths) == 0 {
			return ""
		}
		observed := readPathsFromResults(results)
		var observedSelected []string
		for _, path := range paths {
			normalized := normalizeRunnerPath(path)
			if _, ok := observed[normalized]; ok {
				observedSelected = append(observedSelected, normalized)
			}
		}
		if len(observedSelected) > 0 {
			paths = uniqueSortedRunnerPaths(observedSelected)
		}
		if len(planThenExecuteHistoryPendingPaths(brief, results, paths)) > 0 {
			return ""
		}
		unreadCandidateLine := "Allowed/candidate paths are not coverage obligations: do not read every allowed path by default."
		readActionLine := "  2. One bounded source-read/search for a different candidate path, but only when it names one concrete edit-safety fact still needed before the plan."
		finalLine := "Do not reread a candidate path that already has source-body evidence before the first valid `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:` blocker."
		return strings.Join([]string{
			"PLANNER READINESS REACHED: at least one selected candidate edit path has source evidence in this turn. This makes `submit_plan` available, but it does not prove every candidate path is complete.",
			"",
			"Selected candidate path(s): " + strings.Join(paths, ", "),
			"",
			unreadCandidateLine,
			"",
			"Next action must be exactly one parser-readable action:",
			"  1. `submit_plan` with only the edit(s) needed by the objective, using insertion, substring, replacement-only line range, or whole/new-file shape, plus a one-sentence purpose per edit.",
			readActionLine,
			"  3. `Decision: source-satisfied` naming the relevant candidate path(s) and observed current-state evidence when the requested final state is already present.",
			"  4. `<<QUESTION>>:` with the exact blocker that prevents drafting the plan.",
			"",
			"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
			"",
			finalLine,
		}, "\n")
	}
	var readyPaths []string
	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		state := planThenExecuteRequiredPathState(brief, results, normalized)
		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			readyPaths = append(readyPaths, normalized)
		default:
			return ""
		}
	}
	if len(readyPaths) == 0 {
		return ""
	}
	if len(planThenExecuteHistoryPendingPaths(brief, results, readyPaths)) > 0 {
		return ""
	}
	return strings.Join([]string{
		"PLANNER READINESS REACHED: every required_edit_path is ready for a terminal planner outcome; existing files have anchor-ready source evidence and missing/new files have explicit new-file evidence.",
		"",
		"Next visible action MUST be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
		"Use the smallest executable edit body that satisfies the objective. Prefer exact `old_text`/`new_text` for small unique replacements; use line-range `content` only for multi-line replacements or large insertions anchored to unchanged source lines. Do not replace whole functions or files when a smaller anchored edit is enough.",
		planThenExecuteSubmitPlanVisibleJSONInstruction() + " For a missing/new source or test file, the brief must provide a substantial inline body or explicit replacement edit shape.",
		"",
		"Narrow exceptions only:",
		"- Use `Decision: source-satisfied` only when every required path already contains the requested final state, and cite current-state evidence for every required path.",
		"- Use `<<QUESTION>>:` only when one exact path-specific blocker prevents drafting any valid `submit_plan` edit.",
		"",
		"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
		"",
		"Default to submitting `submit_plan` now. Do not read/search/discovery again before the first valid `submit_plan` or narrow terminal exception.",
	}, "\n")
}

func planThenExecuteSubmitPlanOnlyReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	if len(required) == 0 {
		if len(unreadCandidateRecoveryScopePathsForRecovery(brief, results, 8)) > 0 {
			return false
		}
		if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
			state := PlanExecutePlanningState{
				EvidencePaths: sortedRunnerPathKeys(readPathsFromResults(results)),
			}
			selected := planThenExecuteSelectedEditPaths(brief, results, state)
			return len(selected) > 0 && len(planThenExecuteHistoryPendingPaths(brief, results, selected)) == 0
		}
		ready := sortedRunnerPathKeys(planThenExecuteAnchorReadyPaths(brief, results))
		return len(ready) > 0 && len(planThenExecuteHistoryPendingPaths(brief, results, ready)) == 0
	}
	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			continue
		default:
			return false
		}
	}
	return len(planThenExecuteHistoryPendingPaths(brief, results, required)) == 0
}

func planThenExecuteCandidateSourcePending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}
	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}
	normalized := workerruntime.NormalizeRuntimeBrief(brief)
	if len(normalized.RequiredEditSnippets) > 0 {
		return false
	}
	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
		return true
	}
	if len(planThenExecuteAnchorReadyPaths(brief, results)) > 0 {
		return false
	}
	return true
}

func planThenExecuteCandidatePostReadinessDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		successfulSubmitPlanResultCount(results) > 0 ||
		!isReadClassTool(req) ||
		strings.TrimSpace(req.Tool) == "submit_plan" {
		return "", "", false
	}
	anchorReady := planThenExecuteAnchorReadyPaths(brief, results)
	if len(anchorReady) == 0 {
		return "", "", false
	}
	target := normalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target != "" && planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief, results, target) && planThenExecuteCodeNavigateMissingAnchorCapability(req) {
		return "Anchor refresh capability required",
			"the previous `submit_plan` named an invalid edit anchor on " + target + ". `" + workspace.CodeNavigateToolName + "` anchor refresh requires an explicit source-body capability such as " + strings.Join(workspace.CodeNavigateSourceBodyCapabilityNames(), " or ") + " on the same path and concrete symbol/pattern. Alternatively use `read_file` with explicit start_line/end_line copied from current source evidence.",
			true
	}
	if target != "" && planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req, brief, results) {
		return "", "", false
	}
	if target != "" {
		if summary, errorText, reject := planThenExecutePostGateOutOfScopeCandidateReadRejection(target, brief, results); reject {
			return summary, errorText, true
		}
		if gitHistoryEvidenceTool(req.Tool) && len(planThenExecuteHistoryPendingPaths(brief, results, []string{target})) > 0 {
			return "", "", false
		}
		if planThenExecuteCandidateSymbolReadAllowed(req, results, target) {
			return "", "", false
		}
		if planThenExecuteCandidateSamePathContinuationReadAllowed(req, results, target) {
			return "", "", false
		}
		if _, alreadyReady := anchorReady[target]; !alreadyReady && planThenExecuteCandidateDifferentPathReadAllowed(req, brief, results, target) {
			return "", "", false
		}
		if _, alreadyReady := anchorReady[target]; alreadyReady && strings.TrimSpace(req.Tool) != "read_file" && !planThenExecuteSourceBodyReadRequest(req) {
			return "Plan-execute candidate source already ready",
				target + " already has source-body evidence for this planner turn. Non-body navigation/search on the same candidate is not executable edit evidence. Use `submit_plan` with the gathered source evidence, use a bounded source-body continuation with a concrete named edit-safety gap, read a different candidate path only for one concrete missing edit-safety fact, answer `Decision: source-satisfied`, or answer `<<QUESTION>>:` with the exact blocker.",
				true
		}
	}
	paths := make([]string, 0, len(anchorReady))
	for path := range anchorReady {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return "", "", false
}

func planThenExecutePostGateOutOfScopeCandidateReadRejection(target string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	target = normalizeRunnerPath(target)
	if target == "" || !completionGateResultSeen(results) {
		return "", "", false
	}
	if planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief, results, target) {
		return "", "", false
	}
	candidateSet := normalizedRequiredEditPathSet(brief.AllowedEditPaths)
	if len(candidateSet) == 0 {
		return "", "", false
	}
	if _, ok := candidateSet[target]; ok {
		return "", "", false
	}
	if _, ok := readPathsFromResults(results)[target]; ok {
		return "", "", false
	}
	candidates := sortedRunnerPathKeys(candidateSet)
	return "Plan-execute post-gate read outside candidate scope",
		"the completion gate already rejected a non-terminal planner answer. " + target + " is not an explicit allowed candidate path or prior evidence path for this plan-execute atom. Use a bounded read on one explicit allowed candidate path if one concrete edit-safety fact is still missing (" + strings.Join(candidates, ", ") + "), or call `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:` with the exact blocker.",
		true
}

func planThenExecuteCandidateLowValueAction(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport) (string, string, bool) {
	state := DerivePlanExecutePlanningActionState(brief, results, transport, req)
	if len(state.LowValueActions) == 0 {
		return "", "", false
	}
	reason := state.LowValueActions[0]
	prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteLowValueActionRecoveryPlan(state))
	return reason, prompt, true
}

func planThenExecuteCandidateDifferentPathReadAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, target string) bool {
	target = normalizeRunnerPath(target)
	if target == "" {
		return false
	}
	for readPath := range readPathsFromResults(results) {
		if normalizeRunnerPath(readPath) == target {
			return false
		}
	}
	candidateSet := make(map[string]struct{})
	for _, path := range candidateRecoveryScopePaths(brief, results, 8) {
		candidateSet[normalizeRunnerPath(path)] = struct{}{}
	}
	if _, ok := candidateSet[target]; !ok {
		return false
	}
	return planThenExecuteAnchorRefreshRequestAllowed(req)
}

func planThenExecuteCandidateSymbolReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, target string) bool {
	target = normalizeRunnerPath(target)
	if target == "" || strings.TrimSpace(req.Tool) != "code_navigate" || !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}
	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) == "" {
		return false
	}
	if _, ok := readPathsFromResults(results)[target]; ok {
		return true
	}
	return false
}

func planThenExecuteCandidateSamePathContinuationReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, target string) bool {
	target = normalizeRunnerPath(target)
	if target == "" || strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}
	if req.StartLine <= 0 && req.EndLine <= 0 {
		for _, readRange := range readRangesForPathFromResults(results, target) {
			if readRange.total > 0 && readRange.total <= 400 && !(readRange.start <= 1 && readRange.end >= readRange.total) {
				return true
			}
		}
		return false
	}
	if !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}
	for _, readRange := range readRangesForPathFromResults(results, target) {
		if planThenExecuteRequestedRangeCoveredByRead(req.StartLine, req.EndLine, readRange) {
			return false
		}
	}
	return true
}

func planThenExecuteRequestedRangeCoveredByRead(start int, end int, prior planThenExecuteReadRange) bool {
	if start <= 0 {
		start = 1
	}
	if prior.total > 0 {
		if end <= 0 || end > prior.total {
			end = prior.total
		}
		if start > prior.total {
			start = prior.total
		}
	}
	if end <= 0 || end < start {
		return false
	}
	return start >= prior.start && end <= prior.end
}

func planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}
	return planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req, candidateRecoveryScopePaths(brief, results, 8), results)
}

func planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, target string) bool {
	target = normalizeRunnerPath(target)
	if target == "" || len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}
	if _, invalidAnchor := submitPlanInvalidAnchorPaths(rejected)[target]; !invalidAnchor {
		return false
	}
	scope := normalizedRequiredEditPathSet(candidateRecoveryScopePaths(brief, results, 8))
	_, ok = scope[target]
	return ok
}

func planThenExecuteCodeNavigateMissingAnchorCapability(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Tool) == "code_navigate" &&
		strings.TrimSpace(req.Capability) == "" &&
		strings.TrimSpace(req.Pattern) != ""
}

func planThenExecuteSubmitPlanVisibleJSONInstruction() string {
	return "Textual-tool backends must emit exactly one visible JSON object with top-level `\"tool\":\"submit_plan\"` and top-level `edits` as a JSON array, not a quoted string. " + planThenExecuteSubmitPlanCoverageSentence() + " If a previous rejection says an edit would not change a path, move that path to top-level `source_satisfied_paths` only when current evidence shows it is already at final state; otherwise replace the no-op with a real edit. Every edit path must be a real required or selected path from gathered source evidence, and every edit body must be executable final source. Keep edit bodies as small as executable: prefer exact `old_text`/`new_text` for small unique replacements; use numeric `start_line`/`end_line` plus complete `content` for multi-line source/test edits and large additions anchored to unchanged source lines. For small insertions/appends, use `insert_after_line` plus inline non-empty `content` only after an existing line. Do not replace whole functions or files when a smaller anchored edit is enough."
}

func planThenExecuteSubmitPlanCoverageSentence() string {
	return "Cover each required edit path exactly once: put paths needing changes in `edits[]` with one real executable edit object, and put paths already at the requested final state in top-level `source_satisfied_paths`; never create an `edits[]` object whose only purpose is source-satisfied coverage."
}

func planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string
	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}
	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "The rejected edit used an oversized or ref-backed `insert_after_line` body. Do not repeat `insert_after_line` for that edit, and do not use `content_ref` with `insert_after_line`. Emit exactly one visible JSON object with top-level `edits` as a JSON array, using `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range on the real edit path with the unchanged anchor line(s) plus inserted content from existing evidence.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "The rejected plan contained an invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each `edits[]` object must choose exactly one executable shape: `start_line`/`end_line`/non-empty `content` for a line-range replacement, exact `old_text`/`new_text`, small `insert_after_line`/inline non-empty `content`, or `replace_all:true` with full `new_text` for whole/new-file writes. Do not combine shapes, do not use placeholders, and do not use content refs with `insert_after_line`.")
	} else {
		parts = append(parts, planThenExecuteSubmitPlanVisibleJSONInstruction())
	}
	return strings.Join(parts, " ")
}

func planThenExecuteNativeSubmitPlanCorrectionDetails(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string
	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}
	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "do not repeat `insert_after_line` for the oversized/ref-backed edit. Use `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range with the unchanged anchor line(s) plus inserted content. `insert_after_line` must have small inline non-empty `content` and cannot use `content_ref`.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "Correct the invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each edit must choose exactly one executable shape: line-range replacement, exact substring replacement, small inline insertion, or whole/new-file write. Do not combine shapes or use placeholders.")
	}
	return strings.Join(parts, " ")
}

func planThenExecuteSubmitPlanCorrectionChecklist(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	if len(required) == 0 {
		return ""
	}
	missing := nilIfEmptyRunnerPathSet(submitPlanMissingRequiredPaths(result))
	invalid := nilIfEmptyRunnerPathSet(submitPlanInvalidAnchorPaths(result))
	noop := nilIfEmptyRunnerPathSet(submitPlanNoOpEditPaths(result))
	unpathableNoop := submitPlanHasUnpathableNoOpEdit(result) && len(noop) == 0
	var b strings.Builder
	b.WriteString("Next submit_plan coverage checklist:")
	for _, rawPath := range required {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		b.WriteString(" ")
		b.WriteString(path)
		b.WriteString("=")
		switch {
		case noop != nil:
			if _, ok := noop[path]; ok {
				b.WriteString("use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else if missing != nil {
				if _, ok := missing[path]; ok {
					b.WriteString("missing-entry")
				} else {
					b.WriteString("must-keep-valid-coverage")
				}
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case unpathableNoop:
			b.WriteString("must-have-real-edit-or-source_satisfied_paths-entry-no-noop")
		case missing != nil:
			if _, ok := missing[path]; ok {
				b.WriteString("missing-entry")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case invalid != nil:
			if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		default:
			b.WriteString("must-have-valid-edit-or-source_satisfied_paths-entry")
		}
		b.WriteString(";")
	}
	return strings.TrimSpace(b.String())
}

func nilIfEmptyRunnerPathSet(paths map[string]struct{}) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func submitPlanNoOpEditPaths(result workspace.ToolResult) map[string]struct{} {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return nil
	}
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	if !strings.Contains(strings.ToLower(text), "edit would not change ") {
		return nil
	}
	paths := make(map[string]struct{})
	for _, line := range strings.Split(text, "\n") {
		remaining := line
		for {
			idx := strings.Index(strings.ToLower(remaining), "edit would not change ")
			if idx < 0 {
				break
			}
			after := remaining[idx+len("edit would not change "):]
			path := strings.TrimSpace(after)
			if cut := strings.Index(path, " after applying prior plan edits"); cut >= 0 {
				path = path[:cut]
			}
			if cut := strings.IndexAny(path, ";,`"); cut >= 0 {
				path = path[:cut]
			}
			if normalized := normalizeRunnerPath(path); normalized != "" {
				paths[normalized] = struct{}{}
			}
			remaining = after
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func submitPlanHasUnpathableNoOpEdit(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
	return strings.Contains(text, "old_text and new_text are identical")
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
	next := firstPlanThenExecuteUnreadRequiredPath(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, ""), unread)
	if next == "" {
		return ""
	}
	if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(next, results) {
		return ""
	}
	evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, next)
	state := string(evidence.Level)
	if state == "" {
		state = "none"
	}
	detail := strings.TrimSpace(evidence.Detail)
	if detail != "" {
		state += " (" + detail + ")"
	}
	return strings.Join([]string{
		"REQUIRED SOURCE READ PENDING: a required_edit_path has not produced source-body evidence yet.",
		"",
		"Unread required path: " + next,
		"Current evidence state: " + state,
		"",
		"Next action MUST be exactly one visible source-body tool call for that path before `submit_plan`:",
		"  1. " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + " for the relevant declaration in " + next + ", or",
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

func planThenExecuteAnchorReadyPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, path := range planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "") {
		if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}
	if len(paths) > 0 {
		return paths
	}
	for path := range readPathsFromResults(results) {
		if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}
	return paths
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
	if candidateAnchorOnlyResult(r) {
		return nil
	}
	tool := strings.TrimSpace(r.Tool)
	if (tool == "code_navigate" || tool == "read_only_capability") && !routedSourceReadOutputHasBody(r.Output) {
		return nil
	}
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

type planThenExecuteReadRange struct {
	start int
	end   int
	total int
}

func readRangesForPathFromResults(results []workspace.ToolResult, target string) []planThenExecuteReadRange {
	target = normalizeRunnerPath(target)
	if target == "" {
		return nil
	}
	ranges := []planThenExecuteReadRange{}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, text := range []string{result.Summary, result.Output} {
			path, readRange, ok := extractReadPathAndRangeFromSummary(text)
			if ok && normalizeRunnerPath(path) == target {
				ranges = append(ranges, readRange)
			}
		}
		for _, line := range strings.Split(result.Output, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "route_summary:") {
				continue
			}
			path, readRange, ok := extractReadPathAndRangeFromSummary(strings.TrimSpace(strings.TrimPrefix(line, "route_summary:")))
			if ok && normalizeRunnerPath(path) == target {
				ranges = append(ranges, readRange)
			}
		}
	}
	return ranges
}

func routedSourceReadOutputHasBody(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}
	if candidateAnchorOnlyText(output) {
		return false
	}
	if !strings.Contains(output, "route_summary:") {
		return true
	}
	seenRouteSummary := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			seenRouteSummary = true
			continue
		}
		if !seenRouteSummary || line == "" {
			continue
		}
		if routedSourceReadOutputHeaderLine(line) {
			continue
		}
		return true
	}
	return false
}

func candidateAnchorOnlyResult(result workspace.ToolResult) bool {
	return candidateAnchorOnlyText(strings.Join([]string{result.Summary, result.Output}, "\n"))
}

func candidateAnchorOnlyText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "path-filter miss") &&
		strings.Contains(lower, "candidate_anchor_status: anchors only")
}

func routedSourceReadOutputHeaderLine(line string) bool {
	for _, prefix := range []string{
		"capability:",
		"language:",
		"routed_tool:",
		"fallback_reason:",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
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
	path, _, ok := extractReadPathAndRangeFromSummary(summary)
	if ok {
		return path
	}
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

func extractReadPathAndRangeFromSummary(summary string) (string, planThenExecuteReadRange, bool) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", planThenExecuteReadRange{}, false
	}
	fields := strings.Fields(summary)
	if len(fields) < 4 || fields[0] != "Read" {
		return "", planThenExecuteReadRange{}, false
	}
	lineIndex := -1
	for i, field := range fields {
		if field == "lines" {
			lineIndex = i
			break
		}
	}
	if lineIndex < 0 || lineIndex+1 >= len(fields) {
		return "", planThenExecuteReadRange{}, false
	}
	pathIndex := -1
	if lineIndex >= 2 && fields[lineIndex-2] == "in" {
		pathIndex = lineIndex - 1
	} else if lineIndex >= 2 && planThenExecuteReadFileHandleField(fields[1]) {
		pathIndex = 2
	} else if lineIndex >= 1 {
		pathIndex = lineIndex - 1
	}
	if pathIndex < 0 || pathIndex >= len(fields) {
		return "", planThenExecuteReadRange{}, false
	}
	parts := strings.Split(strings.Trim(fields[lineIndex+1], ".,;:"), "-")
	if len(parts) != 2 {
		return "", planThenExecuteReadRange{}, false
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || start <= 0 || end < start {
		return "", planThenExecuteReadRange{}, false
	}
	total := extractReadTotalLineCount(fields)
	if parsedTotal, ok := planThenExecuteReadSummaryTotalLines(summary); ok {
		total = parsedTotal
	}
	return fields[pathIndex], planThenExecuteReadRange{start: start, end: end, total: total}, true
}

func planThenExecuteReadFileHandleField(field string) bool {
	field = strings.TrimSpace(field)
	if len(field) < 2 || field[0] != 'f' {
		return false
	}
	for _, ch := range field[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func extractReadTotalLineCount(fields []string) int {
	for i := 0; i+2 < len(fields); i++ {
		if strings.Trim(fields[i], "(),.;:") != "total" || strings.Trim(fields[i+1], "(),.;:") != "lines" {
			continue
		}
		total, err := strconv.Atoi(strings.Trim(fields[i+2], "(),.;:"))
		if err == nil && total > 0 {
			return total
		}
	}
	return 0
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
