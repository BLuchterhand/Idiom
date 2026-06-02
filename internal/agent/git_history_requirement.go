package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

const (
	requiredEvidenceGitHistoryRationale = string(workerruntime.RequiredEvidenceGitHistoryRationale)
	requiredToolGitHistoryLookup        = "history_range_or_history_symbol"
)

type gitHistoryNotApplicableOutput struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Path   string `json:"path"`
}

func gitHistoryLookupToolNames() []string {
	return []string{"history_range", "history_symbol", "blame_range"}
}

func gitHistoryLookupTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "history_range", "history_symbol", "blame_range":
		return true
	default:
		return false
	}
}

func gitHistoryEvidenceTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "history_range", "history_symbol":
		return true
	default:
		return false
	}
}

func mutationHistoryEvidenceRequired(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	for _, label := range brief.RequiredEvidence {
		if workerruntime.EvidenceRequirementMatches(label, workerruntime.RequiredEvidenceGitHistoryRationale) {
			return true
		}
	}
	return false
}

func successfulGitHistoryEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" &&
			gitHistoryEvidenceTool(result.Tool) &&
			gitHistoryResultIncludesCommitDescription(result) {
			return true
		}
	}
	return false
}

func successfulGitHistoryResultForPath(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || !gitHistoryEvidenceTool(result.Tool) {
			continue
		}
		if gitHistoryNotApplicableForPath(path, result) {
			return true
		}
		if !gitHistoryResultIncludesCommitDescription(result) {
			continue
		}
		if normalizeRunnerPath(gitHistoryResultPath(result)) == path {
			return true
		}
	}
	return false
}

func gitHistoryResultIncludesCommitDescription(result workspace.ToolResult) bool {
	output := strings.TrimSpace(result.Output)
	return strings.HasPrefix(output, "commit ") || strings.Contains(output, "\ncommit ")
}

func gitHistoryNotApplicableForPath(path string, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Code) != workspace.HistoryNotApplicableCode {
		return false
	}
	if strings.TrimSpace(result.Error) != "" || !gitHistoryEvidenceTool(result.Tool) {
		return false
	}
	var output gitHistoryNotApplicableOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Output)), &output); err != nil {
		return false
	}
	if strings.TrimSpace(output.Code) != "" && strings.TrimSpace(output.Code) != workspace.HistoryNotApplicableCode {
		return false
	}
	if strings.TrimSpace(output.Status) != "" && strings.TrimSpace(output.Status) != "not_applicable" {
		return false
	}
	return normalizeRunnerPath(output.Path) == normalizeRunnerPath(path)
}

func gitHistoryResultPath(result workspace.ToolResult) string {
	summary := strings.TrimSpace(result.Summary)
	switch strings.TrimSpace(result.Tool) {
	case "history_range":
		const prefix = "Read git history for "
		if !strings.HasPrefix(summary, prefix) {
			return ""
		}
		tail := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
		if idx := strings.Index(tail, " lines "); idx >= 0 {
			return strings.TrimSpace(tail[:idx])
		}
	case "history_symbol":
		const marker = " in "
		if idx := strings.LastIndex(summary, marker); idx >= 0 {
			return strings.TrimSpace(summary[idx+len(marker):])
		}
	}
	return ""
}

func (r *Runner) writeBeforeGitHistoryEvidenceRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return "", "", false
	}
	if checkWriteDescription(req) != nil {
		return "", "", false
	}
	missing := r.writePathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		return "", "", false
	}
	return "Write rejected before git history evidence", gitHistoryEvidenceRequiredMessage(missing), true
}

func (r *Runner) writeBeforeGitHistoryEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}
	if checkWriteDescription(req) != nil {
		return workspace.ToolRequest{}, "", false
	}
	missing := r.writePathsMissingGitHistory(req, results)
	return gitHistoryEvidenceRedirectRequest(missing, results, allowedTools)
}

func (r *Runner) writePathsMissingGitHistory(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}
	return pathsMissingGitHistory(r.workspaceRootForGitHistoryRequirement(), writeRequestPaths(req), results)
}

func (r *Runner) submitPlanPathsMissingGitHistory(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		paths = append(paths, edit.Path)
	}
	return pathsMissingGitHistory(r.workspaceRootForGitHistoryRequirement(), paths, results)
}

func (r *Runner) submitPlanBeforeGitHistoryEvidenceRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" || !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return workspace.ToolRequest{}, "", false
	}
	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) > 0 &&
		len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {
		return workspace.ToolRequest{}, "", false
	}
	if planThenExecuteSubmitPlanHasRequiredPathCoverageProblem(r.RuntimeBrief.RequiredEditPaths, req) {
		return workspace.ToolRequest{}, "", false
	}
	if len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {
		return workspace.ToolRequest{}, "", false
	}
	missing := r.submitPlanPathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		missing = planThenExecuteHistoryPendingPaths(r.RuntimeBrief, results, nil)
	}
	return gitHistoryEvidenceRedirectRequest(missing, results, allowedTools)
}

func planThenExecuteSubmitPlanHasRequiredPathCoverageProblem(requiredPaths []string, req workspace.ToolRequest) bool {
	required := make([]string, 0, len(requiredPaths))
	for _, raw := range requiredPaths {
		if path := normalizeRunnerPath(raw); path != "" {
			required = append(required, path)
		}
	}
	if len(required) == 0 {
		return false
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, path := range required {
		requiredSet[path] = struct{}{}
	}
	covered := make(map[string]struct{}, len(required))
	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		path := normalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}
		if _, ok := requiredSet[path]; !ok {
			return true
		}
		covered[path] = struct{}{}
	}
	for _, path := range required {
		if _, ok := covered[path]; !ok {
			return true
		}
	}
	return false
}

func gitHistoryEvidenceRedirectRequest(paths []string, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if len(paths) == 0 || !runnerAllowsTool(allowedTools, "history_range") {
		return workspace.ToolRequest{}, "", false
	}
	path := normalizeRunnerPath(paths[0])
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}
	start, end := gitHistoryRedirectRange(path, results)
	req := workspace.ToolRequest{
		Tool:            "history_range",
		Path:            path,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        "git history rationale before changing established logic",
		WhyNotAnswering: "need commit history evidence before write or submit_plan",
	}
	return req, marshalToolRequestRaw(req), true
}

func gitHistoryRedirectRange(path string, results []workspace.ToolResult) (int, int) {
	path = normalizeRunnerPath(path)
	for _, readRange := range readRangesForPathFromResults(results, path) {
		if readRange.start > 0 && readRange.end >= readRange.start {
			return readRange.start, readRange.end
		}
	}
	return 1, 80
}

func (r *Runner) workspaceRootForGitHistoryRequirement() string {
	if r == nil || r.Workspace == nil {
		return ""
	}
	return strings.TrimSpace(r.Workspace.Root())
}

func pathsMissingGitHistory(root string, paths []string, results []workspace.ToolResult) []string {
	seen := make(map[string]struct{}, len(paths))
	var missing []string
	for _, rawPath := range paths {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if !existingFileNeedsGitHistory(root, path) {
			continue
		}
		if successfulGitHistoryResultForPath(path, results) {
			continue
		}
		missing = append(missing, path)
	}
	sort.Strings(missing)
	return missing
}

func existingFileNeedsGitHistory(root string, path string) bool {
	path = cleanRelativeRunnerPath(path)
	if path == "" {
		return false
	}
	if strings.TrimSpace(root) == "" {
		return true
	}
	if !workspaceRootHasGitMetadata(root) {
		return false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

func workspaceRootHasGitMetadata(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	dir, err := filepath.Abs(root)
	if err != nil {
		dir = root
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func gitHistoryEvidenceRequiredMessage(paths []string) string {
	target := "the intended logic"
	if len(paths) > 0 {
		target = quotedRunnerStrings(paths)
	}
	return fmt.Sprintf("git history rationale is required before changing established logic for %s. Use `history_range` or `history_symbol` on the intended logic, read the commit descriptions to understand why and when the current behavior was introduced, then make the write only if that history supports the proposed change. If history shows the change is unsafe, answer `<<QUESTION>>:` with the blocker; genuinely new files do not require history evidence.", target)
}

func (r *Runner) preflightPlanThenExecuteSubmitPlanHistoryGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}
	if !mutationHistoryEvidenceRequired(r.RuntimeBrief) {
		return verificationInterceptResult{}
	}
	missing := r.submitPlanPathsMissingGitHistory(req, results)
	if len(missing) == 0 {
		return verificationInterceptResult{}
	}
	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack git history evidence",
			Error: "submit_plan edits must be backed by git history rationale before executor handoff; missing `history_range`/`history_symbol` commit-description evidence for " + quotedRunnerStrings(missing) + ". " +
				"Read the history for the intended logic, use the commit descriptions to decide why and when the current behavior exists, then submit a corrected plan only if the proposed change is safe. If not safe, answer `<<QUESTION>>:` with the path-specific blocker.",
		},
		Reason: "plan-then-execute planner phase: submit_plan lacked git history evidence",
	}
}

func (r *Runner) implementationTargetWriteReadyHistoryReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !mutationHistoryEvidenceRequired(r.RuntimeBrief) || !gitHistoryLookupTool(req.Tool) {
		return false
	}
	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}
	if successfulGitHistoryResultForPath(path, results) {
		return false
	}
	if !implementationTargetWriteReadySourcePath(path) && !concreteSourceEvidenceReadForPath(path, results) {
		return false
	}
	return true
}

func planThenExecuteSubmitPlanHistoryMissing(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !mutationHistoryEvidenceRequired(brief) {
		return false
	}
	var paths []string
	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if concreteSourceEvidenceReadForPath(path, results) {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true})
		for _, path := range state.SelectedEditPaths {
			if concreteSourceEvidenceReadForPath(path, results) {
				paths = append(paths, path)
			}
		}
	}
	paths = uniquePhaseStrings(paths)
	for _, path := range paths {
		if !successfulGitHistoryResultForPath(path, results) {
			return true
		}
	}
	return false
}

func answerHasGitHistoryNoopOrBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "source-satisfied") ||
		strings.Contains(lower, "no files were changed") ||
		strings.Contains(lower, "changed paths: none") ||
		strings.Contains(lower, "no changes") ||
		strings.Contains(lower, "noop") ||
		strings.Contains(lower, "new file")
}
